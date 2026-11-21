package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/chinmay28/novee/skills/web-browse/page"
	"github.com/chinmay28/novee/skills/web-browse/session"
)

// One PDF per calendar year with every original Costco receipt in it:
// warehouse receipts and online orders, oldest first.
//
// A year is a few dozen receipts, each opened and printed on the live page,
// so this takes minutes. No command the agent runs may take that long, and a
// model left to fetch receipts one by one sends the owner each of them. So
// the work is a background job: the command starts it and returns, the job
// sends the owner the finished PDFs itself, and while it runs the other
// commands leave the browser alone.

type yearJob struct {
	Store   string    `json:"store"` // "costco" or "doordash"
	Pid     int       `json:"pid"`
	Started time.Time `json:"started"`
	Years   string    `json:"years"` // "all" or "2024,2025"
	Phase   string    `json:"phase"`
	Fetched int       `json:"fetched"`
}

func jobFile() string { return filepath.Join(session.Dir(), "costco-job.json") }

func currentJob() (*yearJob, bool) {
	data, err := os.ReadFile(jobFile())
	if err != nil {
		return nil, false
	}
	var j yearJob
	if json.Unmarshal(data, &j) != nil || j.Pid == 0 || syscall.Kill(j.Pid, 0) != nil {
		return nil, false
	}
	return &j, true
}

func (j *yearJob) save() {
	data, _ := json.Marshal(j)
	_ = os.WriteFile(jobFile(), data, 0o600)
}

func (j *yearJob) describe() string {
	return fmt.Sprintf("%s, started %d minutes ago for %s; now: %s; %d receipts fetched so far",
		storeName[j.Store], int(time.Since(j.Started).Minutes()), j.Years, j.Phase, j.Fetched)
}

// parseYears turns "all" or a list of years into a filter; nil means every year.
func parseYears(args []string) (map[int]bool, string, error) {
	if len(args) == 1 && strings.EqualFold(args[0], "all") {
		return nil, "all years", nil
	}
	want := map[int]bool{}
	var names []string
	for _, a := range args {
		for _, f := range strings.FieldsFunc(a, func(r rune) bool { return r == ',' || r == ' ' }) {
			y, err := strconv.Atoi(f)
			if err != nil || y < 2000 || y > time.Now().Year() {
				return nil, "", fmt.Errorf("%q is not a year", f)
			}
			if !want[y] {
				want[y] = true
				names = append(names, f)
			}
		}
	}
	if len(want) == 0 {
		return nil, "", errors.New("no year given")
	}
	return want, strings.Join(names, ","), nil
}

var storeName = map[string]string{"costco": "Costco", "doordash": "DoorDash"}

func (a *app) costcoYearPDF(ctx context.Context, args []string) (int, error) {
	return a.yearPDF(ctx, "costco", args)
}

func (a *app) doordashYearPDF(ctx context.Context, args []string) (int, error) {
	return a.yearPDF(ctx, "doordash", args)
}

// yearPDF starts, shows or stops the job of a store.
func (a *app) yearPDF(ctx context.Context, store string, args []string) (int, error) {
	name, cmd := storeName[store], store+"-year-pdf"
	usage := fmt.Sprintf("usage: %s all | YEAR [YEAR ...] | status | stop. Examples: %s all, %s 2025", cmd, cmd, cmd)
	if len(args) == 0 {
		return exitError, errors.New(usage)
	}
	job, running := currentJob()
	switch args[0] {
	case "status":
		if !running {
			fmt.Fprintln(a.out, "No receipts PDF job is running. The last one has finished; its PDFs were sent to Chinmay if it found receipts.")
			return exitOK, nil
		}
		fmt.Fprintf(a.out, "A receipts PDF job is RUNNING: %s. Chinmay gets the PDFs in the chat when it is done. Run nothing more.\n", job.describe())
		return exitOK, nil
	case "stop":
		if !running {
			fmt.Fprintln(a.out, "No receipts PDF job was running.")
			return exitOK, nil
		}
		_ = syscall.Kill(job.Pid, syscall.SIGTERM)
		_ = os.Remove(jobFile())
		fmt.Fprintln(a.out, "The receipts PDF job was stopped. Nothing was sent.")
		return exitOK, nil
	}
	_, names, err := parseYears(args)
	if err != nil {
		return exitError, fmt.Errorf("%w. %s", err, usage)
	}
	if running {
		fmt.Fprintf(a.out, "A receipts PDF job is ALREADY RUNNING: %s. Only one runs at a time; do not start another. Tell Chinmay it is still working and that the PDFs will arrive in the chat.\n", job.describe())
		return exitOK, nil
	}
	if _, err := exec.LookPath("gs"); err != nil {
		return exitError, errors.New("Ghostscript is needed to join PDFs and is not installed (sudo apt install ghostscript)")
	}
	// Find out now whether the site is signed in, so that the model can say so.
	if store == "costco" {
		if ok, code, err := a.costcoWarehouse(ctx); !ok {
			return code, err
		}
	} else if ok, code, err := a.signedIn(ctx, doordashOrders, name); !ok {
		return code, err
	}
	logPath := filepath.Join(session.Dir(), "costco-job.log")
	_ = os.Remove(logPath)
	if err := session.Spawn("costco", append(append([]string{}, a.job...), store+":"+strings.Join(args, ",")), logPath); err != nil {
		return exitError, err
	}
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if _, ok := currentJob(); ok {
			fmt.Fprintf(a.out, "STARTED a background job for %s, %s. It opens every receipt, joins them into ONE PDF per calendar year and sends those PDFs to Chinmay itself. It takes about 5 minutes per year.\nNow reply to Chinmay: \"I am collecting your %s receipts for %s. You will get one PDF per year here in a few minutes.\" Then stop. Do not fetch receipts yourself and do not run this again. If he asks how it is going: novee-browse %s status\n", name, names, name, names, cmd)
			return exitOK, nil
		}
	}
	tail, _ := os.ReadFile(logPath)
	return exitError, fmt.Errorf("the job did not start: %s", strings.TrimSpace(string(tail)))
}

// costcoJobMain is the detached process that does the work.
func costcoJobMain(arg string) int {
	a := &app{out: os.Stdout}
	exe, _ := os.Executable()
	a.keeper, a.viewer, a.job = []string{exe, "_keeper"}, []string{exe, "_viewer"}, []string{exe, "_costcojob"}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
	defer cancel()
	store, years, _ := strings.Cut(arg, ":")
	job := &yearJob{Store: store, Pid: os.Getpid(), Started: time.Now(), Years: years, Phase: "opening " + storeName[store]}
	job.save()
	defer os.Remove(jobFile())
	want, names, err := parseYears(strings.Split(years, ","))
	if err == nil {
		job.Years = names
		if store == "doordash" {
			err = a.runDoordashJob(ctx, job, want)
		} else {
			err = a.runYearJob(ctx, job, want)
		}
	}
	if err != nil {
		fmt.Fprintln(a.out, "ERROR:", err)
		_ = notifyOwner(storeName[store] + " receipts: the job stopped before it was done. " + err.Error())
		return exitError
	}
	return exitOK
}

// doc is one fetched receipt or order.
type doc struct {
	When time.Time
	Path string
	Kind string // "warehouse" or "online"
}

var onlineEntry = regexp.MustCompile(`Order Number\s+(\d+)\s+(\d{2}/\d{2}/\d{4})\s+Total\s+(-?\$[\d,]+\.\d{2})`)

func (a *app) runYearJob(ctx context.Context, job *yearJob, want map[int]bool) error {
	if ok, _, err := a.costcoWarehouse(ctx); err != nil {
		return err
	} else if !ok {
		return errors.New("Costco is not signed in. A sign-in link was sent; sign in and ask again")
	}
	work := filepath.Join(session.Dir(), "costco-job")
	_ = os.RemoveAll(work)
	if err := os.MkdirAll(work, 0o700); err != nil {
		return err
	}
	defer os.RemoveAll(work)
	wanted := func(t time.Time) bool { return want == nil || want[t.Year()] }
	var docs []doc
	failed := 0
	keep := func(kind, name string, when time.Time, data []byte) error {
		path := filepath.Join(work, name+".pdf")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			return err
		}
		docs = append(docs, doc{When: when, Path: path, Kind: kind})
		job.Fetched = len(docs)
		job.save()
		session.Touch()
		return nil
	}

	_, err := a.withPage(func(p *page.Page) (int, error) {
		s, err := p.Snapshot(ctx)
		if err != nil {
			return exitError, err
		}
		sel, ok := named(s, "select", "Showing")
		if !ok {
			return exitError, errors.New("the Costco receipts page has no period drop-down; the site may have changed")
		}
		// The periods that can hold a wanted year, newest first.
		var labels []string
		for _, o := range sel.Options {
			m := periodYear.FindStringSubmatch(o)
			if m == nil || want == nil {
				labels = append(labels, o) // "Last 3 Months", or every period
				continue
			}
			y, _ := strconv.Atoi(m[1])
			if want[y] || (want[y-1] && strings.Contains(o, "December")) {
				labels = append(labels, o)
			}
		}

		// Warehouse receipts: each opens in a pop-up on the list page.
		seen := map[string]bool{}
		for _, label := range labels {
			job.Phase = "warehouse receipts, " + label
			job.save()
			if err := eachPage(ctx, p, label, func(s page.Snapshot) (string, error) {
				top := ""
				occ := map[string]int{}
				for _, e := range s.Elements {
					r, ok := parseReceipt(e, s.Text)
					if !ok {
						continue
					}
					key := r.String()
					if top == "" {
						top = key
					}
					n := occ[key]
					occ[key]++
					id := fmt.Sprintf("%s#%d", key, n)
					if !wanted(r.When) || seen[id] {
						continue
					}
					seen[id] = true
					data, err := fetchReceipt(ctx, p, r, n)
					if err != nil {
						fmt.Fprintf(a.out, "could not fetch %s: %v\n", key, err)
						failed++
						continue
					}
					if err := keep("warehouse", fmt.Sprintf("W-%s-%d", r.When.Format("20060102-1504"), len(docs)), r.When, data); err != nil {
						return "", err
					}
				}
				return top, nil
			}); err != nil {
				return exitError, err
			}
		}

		// Online orders: listed on the other tab, each with a details page of its own.
		s, err = p.Snapshot(ctx)
		if err != nil {
			return exitError, err
		}
		type order struct {
			Num  string
			When time.Time
		}
		var orders []order
		if tab, ok := named(s, "button", "Online"); ok {
			if err := p.Click(ctx, tab.Ref); err != nil {
				return exitError, err
			}
			time.Sleep(2 * time.Second)
			got := map[string]bool{}
			for _, label := range labels {
				job.Phase = "online orders, " + label
				job.save()
				if err := eachPage(ctx, p, label, func(s page.Snapshot) (string, error) {
					top := ""
					for _, m := range onlineEntry.FindAllStringSubmatch(strings.Join(strings.Fields(s.Text), " "), -1) {
						when, err := time.Parse("01/02/2006", m[2])
						if err != nil {
							continue
						}
						if top == "" {
							top = m[1]
						}
						if wanted(when) && !got[m[1]] {
							got[m[1]] = true
							orders = append(orders, order{m[1], when})
						}
					}
					return top, nil
				}); err != nil {
					return exitError, err
				}
			}
		}
		for i, o := range orders {
			job.Phase = fmt.Sprintf("online order %d of %d", i+1, len(orders))
			job.save()
			data, err := fetchOrder(ctx, p, o.Num)
			if err != nil {
				fmt.Fprintf(a.out, "could not fetch online order %s: %v\n", o.Num, err)
				failed++
				continue
			}
			if err := keep("online", "O-"+o.When.Format("20060102")+"-"+o.Num, o.When, data); err != nil {
				return exitError, err
			}
		}
		return exitOK, nil
	})
	if err != nil {
		return err
	}

	return a.sendYearPDFs(ctx, job, docs, failed, func(ds []doc) string {
		on := 0
		for _, d := range ds {
			if d.Kind == "online" {
				on++
			}
		}
		return fmt.Sprintf("%d warehouse receipts and %d online orders", len(ds)-on, on)
	})
}

// sendYearPDFs joins the fetched receipts into one PDF per calendar year,
// oldest first, and sends each to the owner. count words the receipts of a year.
func (a *app) sendYearPDFs(ctx context.Context, job *yearJob, docs []doc, failed int, count func([]doc) string) error {
	name := storeName[job.Store]
	// One PDF per calendar year, oldest receipt first.
	job.Phase = "joining the PDFs"
	job.save()
	sort.SliceStable(docs, func(i, j int) bool { return docs[i].When.Before(docs[j].When) })
	byYear := map[int][]doc{}
	var years []int
	for _, d := range docs {
		if _, ok := byYear[d.When.Year()]; !ok {
			years = append(years, d.When.Year())
		}
		byYear[d.When.Year()] = append(byYear[d.When.Year()], d)
	}
	if len(years) == 0 {
		return notifyOwner(fmt.Sprintf("%s receipts: the site lists no receipts or orders for %s.", name, job.Years))
	}
	outDir := filepath.Join(outDir(), job.Store)
	if err := os.MkdirAll(outDir, 0o700); err != nil {
		return err
	}
	for _, y := range years {
		ds := byYear[y]
		var in []string
		for _, d := range ds {
			in = append(in, d.Path)
		}
		out := filepath.Join(outDir, fmt.Sprintf("%s-receipts-%d.pdf", name, y))
		if err := mergePDFs(ctx, out, in); err != nil {
			return err
		}
		caption := fmt.Sprintf("%s %d: %s, %s to %s, oldest first.", name, y, count(ds),
			ds[0].When.Format("01/02/2006"), ds[len(ds)-1].When.Format("01/02/2006"))
		fmt.Fprintln(a.out, caption, out)
		if err := notifyOwnerFile(caption, out); err != nil {
			return fmt.Errorf("the PDF for %d is saved as %s but could not be sent: %w", y, out, err)
		}
	}
	if failed > 0 {
		return notifyOwner(fmt.Sprintf("%s receipts: done, but %d receipts could not be opened and are missing from the PDFs. Ask again to retry.", name, failed))
	}
	if len(years) > 1 {
		return notifyOwner(fmt.Sprintf("%s receipts: done. One PDF each for %s.", name, strings.Trim(strings.Join(strings.Fields(fmt.Sprint(years)), ", "), "[]")))
	}
	return nil
}

// eachPage shows a period and calls visit for every page of its list. visit
// returns what identifies the page (its first entry), so that a "next" button
// that leads nowhere is noticed. visit may open and close pop-ups.
func eachPage(ctx context.Context, p *page.Page, label string, visit func(page.Snapshot) (string, error)) error {
	s, err := p.Snapshot(ctx)
	if err != nil {
		return err
	}
	sel, ok := named(s, "select", "Showing")
	if !ok {
		return errors.New("the period drop-down is gone; the site may have changed")
	}
	if err := p.Select(ctx, sel.Ref, label); err != nil {
		return err
	}
	time.Sleep(1500 * time.Millisecond) // the list is fetched after the choice
	last := ""
	for n := 0; n < 30 && ctx.Err() == nil; n++ {
		if s, err = p.Snapshot(ctx); err != nil {
			return err
		}
		top, err := visit(s)
		if err != nil {
			return err
		}
		if top == "" || top == last {
			return nil
		}
		last = top
		if s, err = p.Snapshot(ctx); err != nil { // visit may have renumbered the page
			return err
		}
		next, ok := named(s, "button", "Go to next page")
		if !ok {
			return nil
		}
		if err := p.Click(ctx, next.Ref); err != nil {
			return err
		}
	}
	return ctx.Err()
}

// fetchReceipt opens the pop-up of the occ-th list entry that reads like r,
// prints it and closes it again.
func fetchReceipt(ctx context.Context, p *page.Page, r costcoReceipt, occ int) ([]byte, error) {
	s, err := p.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	var target *page.Element
	n := 0
	for _, e := range s.Elements {
		if r2, ok := parseReceipt(e, s.Text); ok && r2.String() == r.String() {
			if n == occ {
				target = &e
				break
			}
			n++
		}
	}
	if target == nil {
		return nil, errors.New("its button is not on the page any more")
	}
	if err := p.Click(ctx, target.Ref); err != nil {
		return nil, err
	}
	closePopup := func() {
		for i := 0; i < 8; i++ {
			s, err := p.Snapshot(ctx)
			if err != nil || !s.Dialog {
				return
			}
			if c, ok := named(s, "button", "Close"); ok {
				_ = p.Click(ctx, c.Ref)
			} else {
				time.Sleep(500 * time.Millisecond)
			}
		}
	}
	defer closePopup()
	date := r.When.Format("01/02/2006")
	for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(700 * time.Millisecond) {
		if s, err = p.Snapshot(ctx); err != nil {
			return nil, err
		}
		// A return is listed under the date of the purchase but carries the date of the return.
		if s.Dialog && (strings.Contains(s.Text, date) || (r.Return && strings.Contains(s.Text, "TOTAL"))) {
			break
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return nil, errors.New("the receipt did not open")
		}
	}
	return p.ContentPDF(ctx)
}

// fetchOrder shows the details page of an online order and prints its content.
func fetchOrder(ctx context.Context, p *page.Page, num string) ([]byte, error) {
	ok, err := p.Hash(ctx, `(ordersandpurchases|orderdetails).*$`, "orderdetails/"+num)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("the address of the orders page has changed")
	}
	var s page.Snapshot
	for deadline := time.Now().Add(25 * time.Second); ; time.Sleep(time.Second) {
		if s, err = p.Snapshot(ctx); err != nil {
			return nil, err
		}
		if strings.Contains(s.Text, num) && strings.Contains(s.Text, "Order Details") && strings.Contains(s.Text, "Total") {
			break
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return nil, errors.New("the order page did not load")
		}
	}
	for _, e := range s.Elements { // the price breakdown is folded away
		if e.Name == "Show Details" && (e.Kind == "button" || e.Kind == "link") {
			_ = p.Click(ctx, e.Ref)
			break
		}
	}
	return p.ContentPDF(ctx)
}

// mergePDFs joins PDFs in order with Ghostscript. Pictures in them (store
// logos, drop-off photos) are reduced to screen quality; text stays text.
func mergePDFs(ctx context.Context, out string, in []string) error {
	gs, err := exec.LookPath("gs")
	if err != nil {
		return errors.New("Ghostscript is needed to join PDFs and is not installed (sudo apt install ghostscript)")
	}
	for _, quality := range []string{"/ebook", "/screen"} {
		args := append([]string{"-q", "-dBATCH", "-dNOPAUSE", "-sDEVICE=pdfwrite", "-dPDFSETTINGS=" + quality,
			"-dAutoRotatePages=/None", "-sOutputFile=" + out}, in...)
		if msg, err := exec.CommandContext(ctx, gs, args...).CombinedOutput(); err != nil {
			return fmt.Errorf("joining the PDFs failed: %v: %s", err, strings.TrimSpace(string(msg)))
		}
		if st, err := os.Stat(out); err == nil && st.Size() < 45<<20 { // a chat takes files up to about 50 MB
			break
		}
	}
	return nil
}
