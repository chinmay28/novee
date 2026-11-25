// Command novee-receipts keeps a private archive of shop receipts and answers
// questions about it. Run it without arguments for the list of commands.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/chinmay28/novee/skills/receipts/browser"
	"github.com/chinmay28/novee/skills/receipts/costco"
	"github.com/chinmay28/novee/skills/receipts/receipt"
	"github.com/chinmay28/novee/skills/receipts/render"
	"github.com/chinmay28/novee/skills/receipts/search"
	"github.com/chinmay28/novee/skills/receipts/source"
	"github.com/chinmay28/novee/skills/receipts/store"
)

const usage = `novee-receipts: a private archive of shop receipts.

Questions (read-only, instant):
  status                         what is stored: shops, date range, counts, last sync
  search WORDS [--price LO-HI] [--item N] [--store S] [--from DATE] [--to DATE] [--limit N]
                                 did I ever buy it, when, for how much
  show ID                        one receipt in full
Files:
  pdf [--store S] [--year Y | --from DATE --to DATE]
                                 one PDF with every receipt in the range; prints its path
Fetching (uses the browser profile the owner signed in to):
  sync [STORE] [--full] [--headed] [--notify]
                                 fetch new receipts and make their PDFs
  import STORE FILE...           load saved JSON replies from the shop instead of fetching
Owner only (opens a browser window):
  login STORE                    sign in by hand once; later syncs reuse the session

Shops: %s. Dates are YYYY-MM-DD.
`

// tab is the browser as the commands need it.
type tab interface {
	source.Page
	PDF(ctx context.Context, html string) ([]byte, error)
	Close()
}

// env is everything a command touches outside its arguments, so tests can replace it.
type env struct {
	out     io.Writer
	st      *store.Store
	sources map[string]source.Source
	open    func(profile string, headed bool) (tab, error)
	notify  func(text string)
	now     func() time.Time
	wait    time.Duration // how long sync waits for the session to appear
}

const (
	exitOK = iota
	exitError
	exitSignIn // sync could not run because the owner must sign in again
)

func main() {
	st, err := store.Open(store.DefaultRoot())
	if err != nil {
		fmt.Fprintln(os.Stderr, "ERROR:", err)
		os.Exit(exitError)
	}
	e := &env{
		out: os.Stdout, st: st, now: time.Now, wait: 45 * time.Second,
		sources: map[string]source.Source{"costco": &costco.Costco{Warehouse: os.Getenv("NOVEE_COSTCO_WAREHOUSE")}},
		open: func(profile string, headed bool) (tab, error) {
			return browser.Open(browser.Options{ProfileDir: profile, Headed: headed})
		},
		notify: notifyOwner,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(e.run(ctx, os.Args[1:]))
}

func (e *env) run(ctx context.Context, args []string) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprintf(e.out, usage, strings.Join(e.names(), ", "))
		return exitOK
	}
	cmds := map[string]func(context.Context, []string) (int, error){
		"status": e.status, "search": e.search, "show": e.show, "pdf": e.pdf,
		"sync": e.sync, "import": e.importFiles, "login": e.login,
	}
	cmd, ok := cmds[args[0]]
	if !ok {
		fmt.Fprintf(e.out, "ERROR: unknown command %q. Commands: status, search, show, pdf, sync, import, login.\n", args[0])
		return exitError
	}
	code, err := cmd(ctx, args[1:])
	if err != nil {
		fmt.Fprintln(e.out, "ERROR:", err)
		if code == exitOK {
			code = exitError
		}
	}
	return code
}

func (e *env) names() []string {
	var n []string
	for k := range e.sources {
		n = append(n, k)
	}
	sort.Strings(n)
	return n
}

// parse lets flags appear before, between or after the positional arguments.
func parse(fs *flag.FlagSet, args []string) ([]string, error) {
	fs.SetOutput(io.Discard)
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return pos, nil
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

func day(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return t, fmt.Errorf("bad date %q, want YYYY-MM-DD", s)
	}
	return t, nil
}

type rangeFlags struct{ store, from, to, year *string }

func addRange(fs *flag.FlagSet) rangeFlags {
	return rangeFlags{fs.String("store", "", ""), fs.String("from", "", ""), fs.String("to", "", ""), fs.String("year", "", "")}
}

func (r rangeFlags) filter() (store.Filter, error) {
	f := store.Filter{Store: *r.store}
	var err error
	if *r.year != "" {
		if f.From, err = day(*r.year + "-01-01"); err != nil {
			return f, fmt.Errorf("bad year %q", *r.year)
		}
		f.To = f.From.AddDate(1, 0, -1)
		return f, nil
	}
	if f.From, err = day(*r.from); err != nil {
		return f, err
	}
	f.To, err = day(*r.to)
	return f, err
}

// span describes a set of receipts in words the model can repeat.
func span(rs []receipt.Receipt) string {
	if len(rs) == 0 {
		return "0 receipts"
	}
	shops := map[string]bool{}
	for _, r := range rs {
		shops[r.Store] = true
	}
	var names []string
	for s := range shops {
		names = append(names, s)
	}
	sort.Strings(names)
	return fmt.Sprintf("%d %s receipts dated %s to %s", len(rs), strings.Join(names, "+"),
		rs[0].When.Format("2006-01-02"), rs[len(rs)-1].When.Format("2006-01-02"))
}

func (e *env) status(_ context.Context, _ []string) (int, error) {
	all, err := e.st.All(store.Filter{})
	if err != nil {
		return exitError, err
	}
	states, err := e.st.State()
	if err != nil {
		return exitError, err
	}
	if len(all) == 0 {
		fmt.Fprintln(e.out, "Verdict: no receipts stored yet. The owner signs in once with `novee-receipts login STORE`, then `sync` fetches them.")
	} else {
		fmt.Fprintf(e.out, "Verdict: %s are stored. Anything older was never fetched.\n", span(all))
	}
	for _, name := range e.names() {
		var rs []receipt.Receipt
		kinds := map[string]int{}
		var net receipt.Cents
		returns, pdfs := 0, 0
		for _, r := range all {
			if r.Store != name {
				continue
			}
			rs = append(rs, r)
			kinds[r.Kind]++
			net += r.Total
			if r.IsReturn() {
				returns++
			}
			if e.st.HasPDF(r) {
				pdfs++
			}
		}
		fmt.Fprintf(e.out, "%s:\n", name)
		if len(rs) > 0 {
			fmt.Fprintf(e.out, "- receipts: %d (%d warehouse, %d gas, %d online), of which %d are returns\n",
				len(rs), kinds[receipt.KindWarehouse], kinds[receipt.KindGas], kinds[receipt.KindOnline], returns)
			fmt.Fprintf(e.out, "- dates: %s to %s\n", rs[0].When.Format("2006-01-02"), rs[len(rs)-1].When.Format("2006-01-02"))
			fmt.Fprintf(e.out, "- net spend (returns subtracted): %s\n", net)
			fmt.Fprintf(e.out, "- PDFs: %d of %d\n", pdfs, len(rs))
		} else {
			fmt.Fprintln(e.out, "- receipts: 0")
		}
		switch s, ok := states[name]; {
		case !ok:
			fmt.Fprintln(e.out, "- last sync: never")
		case s.OK:
			fmt.Fprintf(e.out, "- last sync: %s, OK. %s\n", s.At.Local().Format("2006-01-02 15:04"), s.Message)
		default:
			last := "never"
			if !s.LastOK.IsZero() {
				last = s.LastOK.Local().Format("2006-01-02 15:04")
			}
			fmt.Fprintf(e.out, "- last sync: %s, FAILED: %s (last good sync: %s)\n", s.At.Local().Format("2006-01-02 15:04"), s.Message, last)
		}
	}
	fmt.Fprintf(e.out, "Files are in %s\n", e.st.Root())
	return exitOK, nil
}

func parsePrice(s string) (lo, hi *receipt.Cents, err error) {
	if s == "" {
		return nil, nil, nil
	}
	one := func(p string) (*receipt.Cents, error) {
		p = strings.TrimPrefix(strings.TrimSpace(p), "$")
		if p == "" {
			return nil, nil
		}
		f, err := strconv.ParseFloat(p, 64)
		if err != nil {
			return nil, fmt.Errorf("bad price %q, want LO-HI like 10-15 or one amount like 12.34", s)
		}
		c := receipt.FromFloat(f)
		return &c, nil
	}
	a, b, isRange := strings.Cut(s, "-")
	if lo, err = one(a); err != nil {
		return nil, nil, err
	}
	if !isRange {
		return lo, lo, nil
	}
	hi, err = one(b)
	return lo, hi, err
}

func (e *env) hitLine(h search.Hit) string {
	var b strings.Builder
	fmt.Fprintf(&b, "- %s, %s: ", h.Receipt.When.Format("2006-01-02"), h.Receipt.Location)
	if h.Line.Item != "" {
		fmt.Fprintf(&b, "item %s ", h.Line.Item)
	}
	b.WriteString(strings.TrimSpace(h.Line.Name + " " + h.Line.Detail))
	switch {
	case !h.Line.Priced:
		fmt.Fprintf(&b, ", line price not reported (order total %s)", h.Receipt.Total)
	case h.Line.Qty > 1:
		fmt.Fprintf(&b, ", %g x %s = %s", h.Line.Qty, h.Unit, h.Line.Amount)
	default:
		fmt.Fprintf(&b, ", %s", h.Line.Amount)
	}
	if d := h.Receipt.Discount(h.Line.Item); d != 0 && h.Line.Priced {
		fmt.Fprintf(&b, ", coupon %s, paid %s", d, h.Line.Amount+d)
	}
	if h.Line.Amount < 0 || h.Receipt.IsReturn() {
		b.WriteString(" (RETURN)")
	}
	fmt.Fprintf(&b, " [receipt %s]", h.Receipt.ID)
	return b.String()
}

func (e *env) search(_ context.Context, args []string) (int, error) {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	rf := addRange(fs)
	price := fs.String("price", "", "")
	item := fs.String("item", "", "")
	limit := fs.Int("limit", 15, "")
	words, err := parse(fs, args)
	if err != nil {
		return exitError, err
	}
	f, err := rf.filter()
	if err != nil {
		return exitError, err
	}
	q := search.Query{Text: strings.Join(words, " "), Item: *item}
	if q.Min, q.Max, err = parsePrice(*price); err != nil {
		return exitError, err
	}
	if q.Text == "" && q.Item == "" && q.Min == nil && q.Max == nil {
		return exitError, errors.New("nothing to search for: give words, --item N or --price LO-HI")
	}
	rs, err := e.st.All(f)
	if err != nil {
		return exitError, err
	}
	if len(rs) == 0 {
		fmt.Fprintln(e.out, "Verdict: CANNOT ANSWER. No receipts are stored for that shop and date range. Run `status` to see what is stored.")
		return exitOK, nil
	}
	res := search.Find(rs, q)

	var what []string
	if q.Text != "" {
		what = append(what, fmt.Sprintf("%q", q.Text))
	}
	if q.Item != "" {
		what = append(what, "item "+q.Item)
	}
	switch {
	case q.Min != nil && q.Max != nil && *q.Min == *q.Max:
		what = append(what, "unit price "+q.Min.String())
	case q.Min != nil && q.Max != nil:
		what = append(what, fmt.Sprintf("unit price %s to %s", *q.Min, *q.Max))
	case q.Min != nil:
		what = append(what, "unit price at least "+q.Min.String())
	case q.Max != nil:
		what = append(what, "unit price at most "+q.Max.String())
	}
	desc := strings.Join(what, ", ")
	scope := fmt.Sprintf("%s (%d lines)", span(rs), res.Lines)

	if len(res.Full) == 0 {
		fmt.Fprintf(e.out, "Verdict: NOT FOUND. No line matches all of %s in %s.\n", desc, scope)
	} else {
		fmt.Fprintf(e.out, "Verdict: FOUND. %d line(s) match %s in %s.\n", len(res.Full), desc, scope)
		e.list(res.Full, *limit)
	}
	if len(res.Partial) > 0 {
		fmt.Fprintf(e.out, "Partial matches (only some of the words; receipt names are abbreviated, so check these), best first:\n")
		e.list(res.Partial, *limit)
	}
	if len(res.Words) > 1 {
		seen := map[string]bool{}
		for _, h := range append(res.Full, res.Partial...) {
			for _, w := range h.Matched {
				seen[w] = true
			}
		}
		var missing []string
		for _, w := range res.Words {
			if !seen[w] {
				missing = append(missing, w)
			}
		}
		if len(missing) > 0 {
			fmt.Fprintf(e.out, "Words found on no receipt line: %s.\n", strings.Join(missing, ", "))
		}
	}
	fmt.Fprintf(e.out, "Not searched: purchases before %s, other shops, other accounts.\n", rs[0].When.Format("2006-01-02"))
	return exitOK, nil
}

func (e *env) list(hits []search.Hit, limit int) {
	for i, h := range hits {
		if limit > 0 && i == limit {
			fmt.Fprintf(e.out, "- ... and %d more (add --limit %d to see all)\n", len(hits)-limit, len(hits))
			return
		}
		fmt.Fprintln(e.out, e.hitLine(h))
	}
}

func (e *env) show(_ context.Context, args []string) (int, error) {
	if len(args) != 1 {
		return exitError, errors.New("usage: show ID")
	}
	rs, err := e.st.All(store.Filter{})
	if err != nil {
		return exitError, err
	}
	var found []receipt.Receipt
	for _, r := range rs {
		if r.ID == args[0] {
			found = []receipt.Receipt{r}
			break
		}
		if strings.Contains(r.ID, args[0]) {
			found = append(found, r)
		}
	}
	if len(found) != 1 {
		return exitError, fmt.Errorf("%d receipts match id %q; copy the id from a search result", len(found), args[0])
	}
	r := found[0]
	fmt.Fprintf(e.out, "%s %s receipt %s\n%s, %s\n", r.Store, r.Kind, r.ID, r.When.Format("2006-01-02 15:04"), r.Location)
	for _, l := range r.Lines {
		amount := "(no price reported)"
		if l.Priced {
			amount = l.Amount.String()
		}
		fmt.Fprintf(e.out, "- %s %s: %s\n", l.Item, strings.TrimSpace(l.Name+" "+l.Detail), amount)
	}
	fmt.Fprintf(e.out, "Subtotal %s, tax %s, total %s\n", r.Subtotal, r.Tax, r.Total)
	if e.st.HasPDF(r) {
		fmt.Fprintf(e.out, "PDF: %s\n", e.st.PDFPath(r))
	}
	return exitOK, nil
}

func (e *env) pdf(ctx context.Context, args []string) (int, error) {
	fs := flag.NewFlagSet("pdf", flag.ContinueOnError)
	rf := addRange(fs)
	if _, err := parse(fs, args); err != nil {
		return exitError, err
	}
	f, err := rf.filter()
	if err != nil {
		return exitError, err
	}
	rs, err := e.st.All(f)
	if err != nil {
		return exitError, err
	}
	if len(rs) == 0 {
		return exitError, errors.New("no receipts stored for that shop and date range")
	}
	html, err := render.HTML(rs)
	if err != nil {
		return exitError, err
	}
	b, err := e.open("", false)
	if err != nil {
		return exitError, err
	}
	defer b.Close()
	data, err := b.PDF(ctx, html)
	if err != nil {
		return exitError, err
	}
	name := "all"
	if f.Store != "" {
		name = f.Store
	}
	path := filepath.Join(e.st.Root(), "exports", fmt.Sprintf("%s-receipts_%s_to_%s.pdf", name,
		rs[0].When.Format("2006-01-02"), rs[len(rs)-1].When.Format("2006-01-02")))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return exitError, err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return exitError, err
	}
	fmt.Fprintf(e.out, "Verdict: one PDF with %s, one receipt per page, oldest first, %d KB.\nFile: %s\n",
		span(rs), (len(data)+1023)/1024, path)
	return exitOK, nil
}

func (e *env) pick(args []string) (source.Source, error) {
	if len(args) == 0 && len(e.sources) == 1 {
		args = e.names()
	}
	if len(args) == 0 {
		return nil, fmt.Errorf("which shop? One of: %s", strings.Join(e.names(), ", "))
	}
	s, ok := e.sources[strings.ToLower(args[0])]
	if !ok {
		return nil, fmt.Errorf("unknown shop %q. Known: %s", args[0], strings.Join(e.names(), ", "))
	}
	return s, nil
}

func profileDir(shop string) string {
	root := os.Getenv("NOVEE_BROWSER_DIR")
	if root == "" {
		home, _ := os.UserHomeDir()
		root = filepath.Join(home, ".local", "share", "novee", "browser")
	}
	return filepath.Join(root, shop)
}

func (e *env) sync(ctx context.Context, args []string) (int, error) {
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	full := fs.Bool("full", false, "")
	headed := fs.Bool("headed", false, "")
	tell := fs.Bool("notify", false, "")
	pos, err := parse(fs, args)
	if err != nil {
		return exitError, err
	}
	src, err := e.pick(pos)
	if err != nil {
		return exitError, err
	}
	name := src.Name()

	// finish records the outcome and, with --notify, tells the owner about a
	// failure once (not again while the message stays the same).
	finish := func(ok bool, msg string) {
		prev, err := e.st.SetState(name, store.SyncState{At: e.now(), OK: ok, Message: msg})
		if err != nil {
			fmt.Fprintln(e.out, "ERROR: could not record the sync state:", err)
		}
		if *tell && !ok && (prev.OK || prev.Message != msg) {
			e.notify("Receipts: " + name + " sync failed. " + msg)
		}
	}

	b, err := e.open(profileDir(name), *headed)
	if err != nil {
		finish(false, "The browser did not start: "+err.Error())
		return exitError, err
	}
	defer b.Close()
	if err := b.Goto(ctx, src.StartURL()); err != nil {
		finish(false, "The shop's page did not load: "+err.Error())
		return exitError, fmt.Errorf("%s did not load: %w", src.StartURL(), err)
	}
	signIn := func() (int, error) {
		msg := fmt.Sprintf("SIGN-IN NEEDED: the saved %s session is missing or expired. Only the owner can fix this: run `novee-receipts login %s` from a desktop and sign in by hand.", name, name)
		fmt.Fprintln(e.out, "Verdict:", msg)
		fmt.Fprintln(e.out, "Nothing was fetched. Stored receipts are unchanged and can still be searched.")
		finish(false, msg)
		return exitSignIn, nil
	}
	if err := source.WaitSignedIn(ctx, src, b, e.wait, 2*time.Second); errors.Is(err, source.ErrSignInNeeded) {
		return signIn()
	} else if err != nil {
		return exitError, err
	}

	states, err := e.st.State()
	if err != nil {
		return exitError, err
	}
	to := e.now()
	from := to.Add(-src.History())
	if last := states[name].LastOK; !*full && !last.IsZero() {
		from = last.AddDate(0, 0, -30) // receipts and returns can show up late
	}
	got, ferr := src.Fetch(ctx, b, from, to)
	if errors.Is(ferr, source.ErrSignInNeeded) && len(got) == 0 {
		return signIn()
	}
	added := 0
	for _, r := range got {
		created, err := e.st.Put(r)
		if err != nil {
			ferr = errors.Join(ferr, err)
			continue
		}
		if created {
			added++
		}
	}

	// Print every receipt of this shop that has no PDF yet, so an interrupted
	// run is completed by the next one.
	all, err := e.st.All(store.Filter{Store: name})
	if err != nil {
		return exitError, err
	}
	printed := 0
	for _, r := range all {
		if e.st.HasPDF(r) {
			continue
		}
		html, err := render.HTML([]receipt.Receipt{r})
		if err == nil {
			var data []byte
			if data, err = b.PDF(ctx, html); err == nil {
				err = e.st.PutPDF(r, data)
			}
		}
		if err != nil {
			ferr = errors.Join(ferr, fmt.Errorf("PDF for %s: %w", r.ID, err))
			break
		}
		printed++
	}

	summary := fmt.Sprintf("Asked %s for %s to %s: %d receipts, %d new, %d PDFs made. Stored now: %s.",
		name, from.Format("2006-01-02"), to.Format("2006-01-02"), len(got), added, printed, span(all))
	switch {
	case ferr == nil:
		fmt.Fprintln(e.out, "Verdict: SYNC OK.", summary)
		finish(true, summary)
		return exitOK, nil
	case len(got) > 0:
		problems := strings.ReplaceAll(ferr.Error(), "\n", "; ")
		fmt.Fprintln(e.out, "Verdict: SYNC PARTIAL.", summary)
		fmt.Fprintln(e.out, "Not everything was fetched:", problems)
		finish(false, "Partial: "+problems)
		return exitError, nil
	default:
		problems := strings.ReplaceAll(ferr.Error(), "\n", "; ")
		fmt.Fprintln(e.out, "Verdict: SYNC FAILED. Nothing was fetched:", problems)
		finish(false, problems)
		return exitError, nil
	}
}

func (e *env) login(ctx context.Context, args []string) (int, error) {
	src, err := e.pick(args)
	if err != nil {
		return exitError, err
	}
	if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		return exitError, fmt.Errorf("login opens a browser window and there is no display here. From a desktop on the LAN run: ssh -X THIS-VM novee-receipts login %s", src.Name())
	}
	b, err := e.open(profileDir(src.Name()), true)
	if err != nil {
		return exitError, err
	}
	defer b.Close()
	if err := b.Goto(ctx, src.StartURL()); err != nil {
		return exitError, err
	}
	fmt.Fprintf(e.out, "A browser window is open on %s. Sign in there by hand (tick \"keep me signed in\"). Waiting up to 15 minutes...\n", src.Name())
	if err := source.WaitSignedIn(ctx, src, b, 15*time.Minute, 2*time.Second); err != nil {
		return exitSignIn, fmt.Errorf("not signed in: %w", err)
	}
	fmt.Fprintf(e.out, "Signed in. The session is saved in %s. Now run: novee-receipts sync %s\n", profileDir(src.Name()), src.Name())
	return exitOK, nil
}

// importFiles loads replies saved from the shop's own page (the whole
// GraphQL reply, or just the object under "data").
func (e *env) importFiles(_ context.Context, args []string) (int, error) {
	if len(args) < 2 {
		return exitError, errors.New("usage: import STORE FILE...")
	}
	if strings.ToLower(args[0]) != "costco" {
		return exitError, fmt.Errorf("import is not available for %q yet", args[0])
	}
	added, total := 0, 0
	var problems error
	for _, path := range args[1:] {
		data, err := os.ReadFile(path)
		if err != nil {
			return exitError, err
		}
		var reply struct {
			Data map[string]json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(data, &reply); err != nil {
			return exitError, fmt.Errorf("%s: not JSON: %w", path, err)
		}
		var rs []receipt.Receipt
		if raw, ok := reply.Data["getOnlineOrders"]; ok {
			rs, _, err = costco.ParseOnline(raw)
		} else if raw, ok := reply.Data["receiptsWithCounts"]; ok {
			rs, err = costco.ParseWarehouse(raw)
		} else {
			rs, err = costco.ParseWarehouse(data) // a bare {"receipts": [...]}
		}
		problems = errors.Join(problems, err)
		if len(rs) == 0 && err == nil {
			problems = errors.Join(problems, fmt.Errorf("%s: no receipts found in it", path))
		}
		for _, r := range rs {
			total++
			created, err := e.st.Put(r)
			if err != nil {
				problems = errors.Join(problems, err)
			} else if created {
				added++
			}
		}
	}
	fmt.Fprintf(e.out, "Imported %d receipts, %d new. PDFs are made by the next `sync`.\n", total, added)
	if problems != nil {
		return exitError, problems
	}
	return exitOK, nil
}
