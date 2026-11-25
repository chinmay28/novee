package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/chinmay28/novee/skills/web-browse/page"
	"github.com/chinmay28/novee/skills/web-browse/pge"
	"github.com/chinmay28/novee/skills/web-browse/session"
)

// PG&E energy statements: fetched from the signed-in account's billing
// history as the PDFs they are, read with the pge package, and reported.
//
// The history page lists payments and bills together, a few to a page, with a
// Next button; each bill has a "View Bill PDF" link that builds the PDF in
// the page and shows it in a new tab. A statement never changes, so each is
// fetched once and kept (PDF and text) next to the session file.

const pgeHistory = "https://myaccount.pge.com/myaccount/s/bill-and-payment-history"

func pgeURL() string {
	if u := os.Getenv("NOVEE_PGE_URL"); u != "" { // tests point this at a fake site
		return u
	}
	return pgeHistory
}

func pgeDir() string { return filepath.Join(session.Dir(), "pge-bills") }

// pgeEntry is one bill in the history list.
type pgeEntry struct {
	Date   time.Time
	Amount string
}

var pgeRow = regexp.MustCompile(`(\d\d/\d\d/\d{4})\s+Bill Charges(?:\s+View Bill PDF)?\s+(-?\$-?[\d,]+\.\d{2})`)

// pgeOnPage reads the bills listed on the page as it is, with the number of each one's PDF link.
func pgeOnPage(s page.Snapshot) (entries []pgeEntry, refs []int) {
	var links []page.Element
	for _, e := range s.Elements {
		if e.Kind == "link" && e.Name == "View Bill PDF" {
			links = append(links, e)
		}
	}
	rows := pgeRow.FindAllStringSubmatch(strings.Join(strings.Fields(s.Text), " "), -1)
	for i, m := range rows {
		d, err := time.Parse("01/02/2006", m[1])
		if err != nil || i >= len(links) {
			continue
		}
		entries = append(entries, pgeEntry{d, m[2]})
		refs = append(refs, links[i].Ref)
	}
	return entries, refs
}

// pgeWalk opens the billing history and calls visit for every bill, newest
// first, page after page, until visit returns true. fetch gets that bill's PDF.
func (a *app) pgeWalk(ctx context.Context, visit func(e pgeEntry, fetch func() ([]byte, error)) (bool, error)) (bool, int, error) {
	if ok, code, err := a.signedIn(ctx, pgeURL(), "PG&E"); !ok {
		return false, code, err
	}
	code, err := a.withPage(func(p *page.Page) (int, error) {
		in, _ := session.Current()
		self := ""
		if in != nil {
			self, _ = session.PageTarget(in)
		}
		// The list is drawn after the page has loaded.
		for deadline := time.Now().Add(40 * time.Second); time.Now().Before(deadline) && ctx.Err() == nil; time.Sleep(time.Second) {
			if s, err := p.Snapshot(ctx); err == nil {
				if s.Dialog { // a cookie notice
					for _, name := range []string{"Reject All", "Opt out"} {
						if b, ok := named(s, "button", name); ok {
							_ = p.Click(ctx, b.Ref)
						}
					}
					continue
				}
				if es, _ := pgeOnPage(s); len(es) > 0 {
					break
				}
			}
		}
		first := ""
		for pageNo := 0; pageNo < 40 && ctx.Err() == nil; pageNo++ {
			s, err := p.Snapshot(ctx)
			if err != nil {
				return exitError, err
			}
			entries, _ := pgeOnPage(s)
			top := ""
			if len(entries) > 0 {
				top = entries[0].Date.Format("2006-01-02")
			}
			if pageNo > 0 && top == first { // Next led nowhere
				return exitOK, nil
			}
			first = top
			for i, e := range entries {
				i := i
				fetch := func() ([]byte, error) {
					s, err := p.Snapshot(ctx) // the numbers may have changed since the list was read
					if err != nil {
						return nil, err
					}
					es, refs := pgeOnPage(s)
					if i >= len(es) || !es[i].Date.Equal(e.Date) {
						return nil, errors.New("the bill is not on the page any more")
					}
					data, _, err := p.CaptureFile(ctx, refs[i])
					if in != nil { // the PDF opened in a tab of its own: close it
						for _, t := range session.Tabs(in) {
							if t.ID != self && (strings.HasPrefix(t.URL, "blob:") || t.URL == "about:blank") {
								session.CloseTab(in, t.ID)
							}
						}
					}
					if err == nil && !bytes.HasPrefix(data, []byte("%PDF")) {
						err = errors.New("what the site handed over is not a PDF")
					}
					return data, err
				}
				if stop, err := visit(e, fetch); stop || err != nil {
					return exitOK, err
				}
			}
			s, err = p.Snapshot(ctx)
			if err != nil {
				return exitError, err
			}
			// The next page: a Next button, or the page-number box ("Jump to 1 of 7") and the next number in it.
			if next, ok := named(s, "button", "Next"); ok {
				if err := p.Click(ctx, next.Ref); err != nil {
					return exitError, err
				}
			} else {
				box, ok := named(s, "select", strconv.Itoa(pageNo+1))
				if !ok {
					return exitOK, nil
				}
				if err := p.Click(ctx, box.Ref); err != nil {
					return exitError, err
				}
				s, err = p.Snapshot(ctx)
				if err != nil {
					return exitError, err
				}
				want, found := strconv.Itoa(pageNo+2), false
				for _, e := range s.Elements {
					if e.Name == want && e.Kind != "select" && e.Kind != "link" {
						found = true
						if err := p.Click(ctx, e.Ref); err != nil {
							return exitError, err
						}
						break
					}
				}
				if !found { // that was the last page; close the box again
					_ = p.Click(ctx, box.Ref)
					return exitOK, nil
				}
			}
			time.Sleep(2 * time.Second)
		}
		return exitOK, nil
	})
	return err == nil, code, err
}

// pgeLoad returns a statement that was fetched before, if it was.
func pgeLoad(d time.Time) (pge.Bill, string, bool) {
	base := filepath.Join(pgeDir(), d.Format("2006-01-02"))
	text, err := os.ReadFile(base + ".txt")
	if err != nil {
		return pge.Bill{}, "", false
	}
	if _, err := os.Stat(base + ".pdf"); err != nil {
		return pge.Bill{}, "", false
	}
	return pge.Parse(string(text)), base + ".pdf", true
}

// pgeKeep saves a statement's PDF, turns it into text and reads it.
func pgeKeep(ctx context.Context, d time.Time, data []byte) (pge.Bill, string, error) {
	if err := os.MkdirAll(pgeDir(), 0o700); err != nil {
		return pge.Bill{}, "", err
	}
	base := filepath.Join(pgeDir(), d.Format("2006-01-02"))
	if err := os.WriteFile(base+".pdf", data, 0o600); err != nil {
		return pge.Bill{}, "", err
	}
	exe := os.Getenv("NOVEE_PDFTOTEXT") // tests put a stand-in here
	if exe == "" {
		var err error
		if exe, err = exec.LookPath("pdftotext"); err != nil {
			return pge.Bill{}, "", errors.New("pdftotext is needed to read a bill and is not installed (sudo apt install poppler-utils)")
		}
	}
	text, err := exec.CommandContext(ctx, exe, "-layout", base+".pdf", "-").Output()
	if err != nil {
		return pge.Bill{}, "", fmt.Errorf("the bill PDF could not be read: %w", err)
	}
	if err := os.WriteFile(base+".txt", text, 0o600); err != nil {
		return pge.Bill{}, "", err
	}
	return pge.Parse(string(text)), base + ".pdf", nil
}

// pgeBills lists the statements of a year.
func (a *app) pgeBills(ctx context.Context, args []string) (int, error) {
	year, err := yearArg(args, "usage: pge-bills [YEAR], for example: pge-bills 2026")
	if err != nil {
		return exitError, err
	}
	var got []pgeEntry
	ok, code, err := a.pgeWalk(ctx, func(e pgeEntry, _ func() ([]byte, error)) (bool, error) {
		if e.Date.Year() == year {
			got = append(got, e)
		}
		return e.Date.Year() < year, nil
	})
	if !ok {
		return code, err
	}
	if len(got) == 0 {
		fmt.Fprintf(a.out, "Verdict: PG&E's billing history has no statements dated %d.\n", year)
		return exitOK, nil
	}
	fmt.Fprintf(a.out, "Verdict: %d PG&E statements dated %d, newest first:\n", len(got), year)
	for _, e := range got {
		fmt.Fprintf(a.out, "- %s: %s\n", e.Date.Format("01/02/2006"), e.Amount)
	}
	fmt.Fprintf(a.out, "One bill in full: novee-browse pge-bill MM/DD/YYYY. The year analysed: novee-browse pge-report %d\n", year)
	return exitOK, nil
}

// pgeBill fetches one statement, prints what it says and sends the PDF.
func (a *app) pgeBill(ctx context.Context, args []string) (int, error) {
	const usage = "usage: pge-bill last  or  pge-bill MM/DD/YYYY (the statement date; a month like 11/2026 works too)"
	noSend := false
	var pos []string
	for _, x := range args {
		if x == "--no-send" {
			noSend = true
		} else {
			pos = append(pos, x)
		}
	}
	if len(pos) != 1 {
		return exitError, errors.New(usage)
	}
	match := func(d time.Time) bool { return true } // "last": the newest
	if !strings.EqualFold(pos[0], "last") && !strings.EqualFold(pos[0], "latest") {
		if d, err := time.Parse("01/02/2006", pos[0]); err == nil {
			match = func(x time.Time) bool { return x.Equal(d) }
		} else if m, err := time.Parse("01/2006", pos[0]); err == nil {
			match = func(x time.Time) bool { return x.Year() == m.Year() && x.Month() == m.Month() }
		} else {
			return exitError, errors.New(usage)
		}
	}
	var bill pge.Bill
	path := ""
	found := false
	ok, code, err := a.pgeWalk(ctx, func(e pgeEntry, fetch func() ([]byte, error)) (bool, error) {
		if !match(e.Date) {
			return false, nil
		}
		found = true
		if b, p, ok := pgeLoad(e.Date); ok {
			bill, path = b, p
			return true, nil
		}
		data, err := fetch()
		if err != nil {
			return true, err
		}
		bill, path, err = pgeKeep(ctx, e.Date, data)
		return true, err
	})
	if !ok {
		return code, err
	}
	if !found {
		fmt.Fprintf(a.out, "Verdict: NOT FOUND. PG&E's billing history has no statement for %s. For the dates that exist run: novee-browse pge-bills\n", pos[0])
		return exitOK, nil
	}
	fmt.Fprintln(a.out, "Verdict: FOUND. "+strings.Join(pgeBillLines(bill), "\n"))
	caption := fmt.Sprintf("PG&E statement %s, %s", bill.Statement.Format("01/02/2006"), usd(bill.TotalDue))
	if data, err := os.ReadFile(path); err == nil { // a copy where a chat message may take it from
		dir := filepath.Join(outDir(), "pge")
		if os.MkdirAll(dir, 0o700) == nil {
			out := filepath.Join(dir, "PGE-statement-"+bill.Statement.Format("2006-01-02")+".pdf")
			if os.WriteFile(out, data, 0o600) == nil {
				path = out
			}
		}
	}
	if noSend || notifyOwnerFile(caption, path) != nil {
		fmt.Fprintf(a.out, "The PDF was saved but NOT sent. Send it to Chinmay with the message tool.\nFile: %s\n", path)
		return exitOK, nil
	}
	fmt.Fprintln(a.out, "PDF SENT to Chinmay in a separate message. Tell him the figures above that he asked about, copied exactly. Run no more commands.")
	return exitOK, nil
}

func kwh(v float64) string { return strconv.FormatFloat(v, 'f', 0, 64) + " kWh" }

// rate writes a price per unit: 39.8¢/kWh, $2.42/therm.
func cents(v float64) string { return fmt.Sprintf("%.1f¢", v*100) }

// pgeBillLines is one statement in words.
func pgeBillLines(b pge.Bill) []string {
	e, g := b.Electric, b.GasUse
	head := fmt.Sprintf("PG&E statement of %s: charges for the month %s; amount due %s by %s.", b.Statement.Format("01/02/2006"), usd(pgeCharges(b)), usd(b.TotalDue), b.Due.Format("01/02/2006"))
	if b.CreditBalance < 0 {
		head = fmt.Sprintf("PG&E statement of %s: charges for the month %s; the account is in credit by %s, nothing to pay.", b.Statement.Format("01/02/2006"), usd(pgeCharges(b)), usd(-b.CreditBalance))
	}
	out := []string{head}
	for _, l := range pgeOther(b) {
		out = append(out, "Also on this statement: "+pgeLine(l))
	}
	if b.ClimateCredit > 0 {
		out = append(out, fmt.Sprintf("The charges include the California Climate Credit of %s.", usd(b.ClimateCredit)))
	}
	out = append(out, fmt.Sprintf("Electric, PG&E charges: %s", usd(b.Delivery)))
	for _, l := range e.Lines {
		out = append(out, "  - "+pgeLine(l))
	}
	if b.GenerationBy != "" {
		out = append(out, fmt.Sprintf("Electric generation, %s: %s (%s)", b.GenerationBy, usd(b.Generation), e.GenSchedule))
		for _, l := range e.GenLines {
			out = append(out, "  - "+pgeLine(l))
		}
	}
	if e.Days > 0 {
		out = append(out, fmt.Sprintf("Electric use %s to %s (%d days), %s:", e.From.Format("01/02"), e.To.Format("01/02"), e.Days, e.Schedule))
		if e.ExportsKnown {
			out = append(out, fmt.Sprintf("  - taken from the grid %s, sent to the grid (solar) %s, net %s", kwh(e.Imports), kwh(e.Exports), kwh(e.Net)))
		} else {
			out = append(out, fmt.Sprintf("  - used %s", kwh(e.Net)))
		}
	}
	if e.HasNEM {
		out = append(out, fmt.Sprintf("Net energy metering (%s): this month's NEM charges %s. They are not billed now; they are added up and settled at the true-up.", e.NEMProgram, usd(e.NEMCharges)))
		for _, l := range e.NEM {
			out = append(out, "  - "+pgeLine(l))
		}
	}
	if b.YTD.Known {
		out = append(out, fmt.Sprintf("NEM year to date: estimated %s owed at the true-up in %s.", usd(b.YTD.Estimate), b.YTD.TrueUp))
	}
	if g.Days > 0 {
		est := ""
		if g.Estimated {
			est = ", estimated by PG&E, not read from the meter"
		}
		per := ""
		if g.Therms > 0 {
			per = fmt.Sprintf(", %s per therm all in", usd(g.Total/g.Therms))
		}
		out = append(out, fmt.Sprintf("Gas %s to %s (%d days): %s for %.0f therms%s%s", g.From.Format("01/02"), g.To.Format("01/02"), g.Days, usd(g.Total), g.Therms, per, est))
		for _, l := range g.Lines {
			out = append(out, "  - "+pgeLine(l))
		}
	}
	return out
}

func pgeLine(l pge.Line) string {
	switch l.Unit {
	case "kWh":
		return fmt.Sprintf("%s: %.0f kWh at %s/kWh = %s", l.Label, l.Qty, cents(l.Rate), usd(l.Amount))
	case "Therms":
		return fmt.Sprintf("%s: %.1f therms at %s/therm = %s", l.Label, l.Qty, usd(l.Rate), usd(l.Amount))
	case "days":
		return fmt.Sprintf("%s: %.0f days at %s/day = %s", l.Label, l.Qty, cents(l.Rate), usd(l.Amount))
	}
	return fmt.Sprintf("%s: %s", l.Label, usd(l.Amount))
}

// pgeReport starts the job that analyses a year of statements.
func (a *app) pgeReport(ctx context.Context, args []string) (int, error) {
	const usage = "usage: pge-report YEAR | status. Example: pge-report 2026"
	if len(args) == 1 && (args[0] == "status" || args[0] == "stop") {
		return a.yearPDF(ctx, "pge", args)
	}
	year, err := yearArg(args, usage)
	if err != nil || len(args) != 1 {
		return exitError, errors.New(usage)
	}
	if job, running := currentJob(); running {
		fmt.Fprintf(a.out, "A job is ALREADY RUNNING: %s. Only one runs at a time; do not start another. Tell Chinmay it is still working and that the result will arrive in the chat.\n", job.describe())
		return exitOK, nil
	}
	if _, err := exec.LookPath("pdftotext"); err != nil && os.Getenv("NOVEE_PDFTOTEXT") == "" {
		return exitError, errors.New("pdftotext is needed to read a bill and is not installed (sudo apt install poppler-utils)")
	}
	if ok, code, err := a.signedIn(ctx, pgeURL(), "PG&E"); !ok {
		return code, err
	}
	logPath := filepath.Join(session.Dir(), "costco-job.log")
	_ = os.Remove(logPath)
	if err := session.Spawn("pge", append(append([]string{}, a.job...), fmt.Sprintf("pge-report:%d", year)), logPath); err != nil {
		return exitError, err
	}
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if _, ok := currentJob(); ok {
			fmt.Fprintf(a.out, "STARTED a background job. It reads every PG&E statement of %d and sends Chinmay the analysis itself: each bill split into PG&E electric charges, generation charges and gas; electricity taken from and sent to the grid, the rates per kWh, the net energy metering balance and true-up; gas use and the rates per therm. A few minutes the first time.\nNow reply to Chinmay: \"I am reading your PG&E statements of %d. You will get the analysis here in a few minutes.\" Then stop. Do not work out figures yourself and do not run this again. If he asks how it is going: novee-browse pge-report status\n", year, year)
			return exitOK, nil
		}
	}
	tail, _ := os.ReadFile(logPath)
	return exitError, fmt.Errorf("the job did not start: %s", strings.TrimSpace(string(tail)))
}

func (a *app) runPGEReport(ctx context.Context, job *yearJob, year int) error {
	job.Phase = "reading the billing history"
	job.save()
	var bills []pge.Bill
	var failed []string
	ok, _, err := a.pgeWalk(ctx, func(e pgeEntry, fetch func() ([]byte, error)) (bool, error) {
		if e.Date.Year() < year {
			return true, nil
		}
		if e.Date.Year() > year {
			return false, nil
		}
		job.Phase = "statement of " + e.Date.Format("01/02/2006")
		job.Fetched = len(bills)
		job.save()
		session.Touch()
		b, _, have := pgeLoad(e.Date)
		if !have {
			data, err := fetch()
			if err == nil {
				b, _, err = pgeKeep(ctx, e.Date, data)
			}
			if err != nil {
				fmt.Fprintf(a.out, "could not read the statement of %s: %v\n", e.Date.Format("01/02/2006"), err)
				failed = append(failed, e.Date.Format("01/02"))
				return false, nil
			}
		}
		if b.Statement.IsZero() {
			b.Statement = e.Date
		}
		bills = append(bills, b)
		return false, nil
	})
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("PG&E is not signed in. A sign-in link was sent; sign in and ask again")
	}
	if len(bills) == 0 {
		return notifyOwner(fmt.Sprintf("PG&E %d: no statements could be read.", year))
	}
	sort.SliceStable(bills, func(i, j int) bool { return bills[i].Statement.Before(bills[j].Statement) })
	report := pgeReportText(year, bills, failed)
	fmt.Fprintln(a.out, strings.Join(report, "\n\n"))

	dir := filepath.Join(outDir(), "pge")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(dir, fmt.Sprintf("PGE-bills-%d.csv", year))
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	w := csv.NewWriter(f)
	_ = w.Write([]string{"statement", "total_due", "pge_electric", "generation", "generation_by", "gas",
		"electric_from", "electric_to", "days", "imported_kwh", "exported_kwh", "net_kwh", "peak_net_kwh", "peak_rate", "offpeak_net_kwh", "offpeak_rate",
		"gen_peak_rate", "gen_offpeak_rate", "nem_charges_month", "nem_estimate_at_trueup",
		"gas_from", "gas_to", "gas_days", "therms", "gas_estimated", "gas_per_therm", "tier1_rate", "tier2_rate"})
	n := func(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
	for _, b := range bills {
		e, g := b.Electric, b.GasUse
		peak, off := pgeFind(e.NEM, "Peak"), pgeFind(e.NEM, "Off Peak")
		gp, gop := pgeFindHas(e.GenLines, "On Peak"), pgeFindHas(e.GenLines, "Off Peak")
		t1, t2 := pgeFind(g.Lines, "Tier 1 Usage"), pgeFind(g.Lines, "Tier 2 Usage")
		per := 0.0
		if g.Therms > 0 {
			per = g.Total / g.Therms
		}
		d := func(t time.Time) string {
			if t.IsZero() {
				return ""
			}
			return t.Format("2006-01-02")
		}
		_ = w.Write([]string{d(b.Statement), n(b.TotalDue), n(b.Delivery), n(b.Generation), b.GenerationBy, n(b.Gas),
			d(e.From), d(e.To), strconv.Itoa(e.Days), n(e.Imports), n(e.Exports), n(e.Net), n(peak.Qty), n(peak.Rate), n(off.Qty), n(off.Rate),
			n(gp.Rate), n(gop.Rate), n(e.NEMCharges), n(b.YTD.Estimate),
			d(g.From), d(g.To), strconv.Itoa(g.Days), n(g.Therms), strconv.FormatBool(g.Estimated), fmt.Sprintf("%.4f", per), n(t1.Rate), n(t2.Rate)})
	}
	w.Flush()
	if err := f.Close(); err != nil {
		return err
	}
	for _, msg := range report {
		if err := notifyOwner(msg); err != nil {
			return err
		}
	}
	return notifyOwnerFile(fmt.Sprintf("PG&E %d, one line per statement with every figure (opens in a spreadsheet).", year), path)
}

// pgeCharges is what a statement charged for its month: every line of its summary.
func pgeCharges(b pge.Bill) float64 {
	if len(b.Summary) == 0 {
		return b.Delivery + b.Generation + b.Gas
	}
	t := 0.0
	for _, l := range b.Summary {
		t += l.Amount
	}
	return t
}

// pgeOther is the lines of the summary besides electric, generation and gas:
// a true-up, a correction, an adjustment.
func pgeOther(b pge.Bill) []pge.Line {
	var out []pge.Line
	for _, l := range b.Summary {
		if strings.HasPrefix(l.Label, "Current PG&E Electric") || strings.HasSuffix(l.Label, "Electric Generation Charges") || l.Label == "Current Gas Charges" {
			continue
		}
		out = append(out, l)
	}
	return out
}

func pgeFind(ls []pge.Line, label string) pge.Line {
	for _, l := range ls {
		if l.Label == label {
			return l
		}
	}
	return pge.Line{}
}

func pgeFindHas(ls []pge.Line, part string) pge.Line {
	for _, l := range ls {
		if strings.Contains(l.Label, part) {
			return l
		}
	}
	return pge.Line{}
}

// pgeSum adds up the amounts of the lines whose label contains one of the words.
func pgeSum(ls []pge.Line, words ...string) float64 {
	t := 0.0
	for _, l := range ls {
		for _, w := range words {
			if strings.Contains(l.Label, w) {
				t += l.Amount
				break
			}
		}
	}
	return t
}

// pgeReportText is the analysis of a year as chat messages: the bills, the
// electricity with solar and net metering, and the gas.
func pgeReportText(year int, bills []pge.Bill, failed []string) []string {
	var total, deliv, gen, gasT, climate float64
	other := map[string]float64{}
	b1 := &strings.Builder{}
	fmt.Fprintf(b1, "PG&E Bills - %d\n%d statements, %s to %s.\n", year, len(bills), bills[0].Statement.Format("01/02"), bills[len(bills)-1].Statement.Format("01/02"))
	by := ""
	for _, b := range bills {
		if b.GenerationBy != "" {
			by = b.GenerationBy
		}
		total += pgeCharges(b)
		deliv += b.Delivery
		gen += b.Generation
		gasT += b.Gas
		for _, l := range pgeOther(b) {
			other[l.Label] += l.Amount
		}
		climate += b.ClimateCredit
		fmt.Fprintf(b1, "\n%s (statement %s)\n• Charges: %s\n• Electric, PG&E: %s\n", b.Statement.Format("January"), b.Statement.Format("01/02"), usd(pgeCharges(b)), usd(b.Delivery))
		if b.GenerationBy != "" {
			fmt.Fprintf(b1, "• Electric generation: %s\n", usd(b.Generation))
		}
		fmt.Fprintf(b1, "• Gas: %s", usd(b.Gas))
		if b.GasUse.Therms > 0 {
			fmt.Fprintf(b1, " (%.0f therms)", b.GasUse.Therms)
		}
		fmt.Fprintln(b1)
		for _, l := range pgeOther(b) {
			fmt.Fprintf(b1, "• %s: %s\n", l.Label, usd(l.Amount))
		}
		if b.ClimateCredit > 0 {
			fmt.Fprintf(b1, "• Includes California Climate Credit: -%s\n", usd(b.ClimateCredit))
		}
		if b.CreditBalance < 0 {
			fmt.Fprintf(b1, "• Account in credit after this bill: %s\n", usd(-b.CreditBalance))
		}
	}
	fmt.Fprintf(b1, "\nTotal %d\n• Charges: %s\n• Electric, PG&E: %s\n", year, usd(total), usd(deliv))
	if by != "" {
		fmt.Fprintf(b1, "• Electric generation (%s): %s\n", by, usd(gen))
	}
	fmt.Fprintf(b1, "• Gas: %s\n", usd(gasT))
	var names []string
	for name := range other {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(b1, "• %s: %s\n", name, usd(other[name]))
	}
	if climate > 0 {
		fmt.Fprintf(b1, "• California Climate Credit included: -%s\n", usd(climate))
	}
	if len(failed) > 0 {
		fmt.Fprintf(b1, "\n⚠️ Not included, could not be read: statements of %s\n", strings.Join(failed, ", "))
	}

	// Electricity, solar and net metering.
	b2 := &strings.Builder{}
	fmt.Fprintf(b2, "PG&E Electricity and Solar - %d\n", year)
	var imp, exp, net, nem float64
	solar := false
	var last pge.Bill
	for _, b := range bills {
		e := b.Electric
		if e.Days == 0 {
			continue
		}
		last = b
		imp += e.Imports
		exp += e.Exports
		net += e.Net
		nem += e.NEMCharges
		fmt.Fprintf(b2, "\n%s to %s (%d days)\n", e.From.Format("01/02"), e.To.Format("01/02"), e.Days)
		if e.ExportsKnown {
			solar = true
			fmt.Fprintf(b2, "• From grid %s, to grid %s, net %s\n", kwh(e.Imports), kwh(e.Exports), kwh(e.Net))
		} else {
			fmt.Fprintf(b2, "• Used %s\n", kwh(e.Net))
		}
		// Net use at PG&E's rates, one line per time of day; two rates when the price changed within the period.
		var order []string
		parts := map[string][]string{}
		for _, l := range e.NEM {
			if l.Unit == "kWh" && !strings.Contains(l.Label, "Credit") {
				if _, ok := parts[l.Label]; !ok {
					order = append(order, l.Label)
				}
				parts[l.Label] = append(parts[l.Label], fmt.Sprintf("net %.0f kWh at %s", l.Qty, cents(l.Rate)))
			}
		}
		for _, label := range order {
			fmt.Fprintf(b2, "• %s: %s per kWh\n", label, strings.Join(parts[label], ", then "))
		}
		var gens []string
		seenGen := map[string]bool{}
		for _, l := range e.GenLines {
			name := strings.TrimPrefix(l.Label, "Generation - ")
			if l.Unit == "kWh" && !seenGen[name] {
				seenGen[name] = true
				gens = append(gens, fmt.Sprintf("%s %s", name, cents(l.Rate)))
			}
		}
		if len(gens) > 0 {
			fmt.Fprintf(b2, "• Generation rates per kWh: %s\n", strings.Join(gens, ", "))
		}
		if e.HasNEM {
			fmt.Fprintf(b2, "• NEM charges this month: %s", usd(e.NEMCharges))
			var cr []string
			if v := pgeSum(e.NEM, "Baseline Credit"); v != 0 {
				cr = append(cr, "baseline credit "+usd(v))
			}
			if v := pgeSum(e.NEM, "Generation Credit"); v != 0 {
				cr = append(cr, "generation credit "+usd(v))
			}
			if len(cr) > 0 {
				fmt.Fprintf(b2, " (after %s)", strings.Join(cr, ", "))
			}
			fmt.Fprintln(b2)
		}
		if b.GenerationBy != "" {
			fmt.Fprintf(b2, "• Generation billed: %s\n", usd(b.Generation))
		}
	}
	if last.Electric.Days > 0 {
		fmt.Fprintf(b2, "\nYear so far\n")
		if solar {
			fmt.Fprintf(b2, "• From grid %s, to grid %s, net %s\n", kwh(imp), kwh(exp), kwh(net))
			if imp > 0 {
				fmt.Fprintf(b2, "• Your exports cover %.0f%% of what you took from the grid\n", 100*exp/imp)
			}
		} else {
			fmt.Fprintf(b2, "• Used %s\n", kwh(net))
		}
		if last.Electric.HasNEM {
			fmt.Fprintf(b2, "• NEM charges added up over these statements: %s\n", usd(nem))
		}
		if last.YTD.Known {
			fmt.Fprintf(b2, "• PG&E's own estimate owed at the true-up in %s: %s\n", last.YTD.TrueUp, usd(last.YTD.Estimate))
		}
		if len(last.YTD.Rows) > 0 {
			fmt.Fprintf(b2, "\nNEM by month (PG&E's year-to-date table, statement of %s)\n", last.Statement.Format("01/02"))
			for _, r := range last.YTD.Rows {
				fmt.Fprintf(b2, "• to %s: net %d kWh (peak %d, off-peak %d), %s\n", r.End.Format("01/02"), r.Net, r.Peak, r.OffPeak, usd(r.Total))
			}
		}
		if solar {
			fmt.Fprint(b2, "\n\"To grid\" is the solar power you did not use yourself. What the panels produced in total is not on a PG&E bill; your solar inverter's app has that.")
		}
	}

	// Gas.
	b3 := &strings.Builder{}
	fmt.Fprintf(b3, "PG&E Gas - %d\n", year)
	var therms, gasCost float64
	days := 0
	for _, b := range bills {
		g := b.GasUse
		if g.Days == 0 {
			continue
		}
		therms += g.Therms
		gasCost += g.Total
		days += g.Days
		fmt.Fprintf(b3, "\n%s to %s (%d days)\n• %.0f therms, %.2f a day", g.From.Format("01/02"), g.To.Format("01/02"), g.Days, g.Therms, g.Therms/float64(g.Days))
		if g.Estimated {
			fmt.Fprint(b3, " (estimated by PG&E)")
		}
		fmt.Fprintf(b3, "\n• Charges: %s", usd(g.Total))
		if g.Therms > 0 {
			fmt.Fprintf(b3, " = %s per therm all in", usd(g.Total/g.Therms))
		}
		fmt.Fprintln(b3)
		var tiers []string
		rates := map[string][]string{}
		for _, l := range g.Lines {
			if l.Unit != "Therms" {
				continue
			}
			name, r := strings.TrimSuffix(l.Label, " Usage"), usd(l.Rate)
			if _, ok := rates[name]; !ok {
				tiers = append(tiers, name)
			}
			if n := len(rates[name]); n == 0 || rates[name][n-1] != r {
				rates[name] = append(rates[name], r)
			}
		}
		for _, name := range tiers {
			fmt.Fprintf(b3, "• %s: %s per therm\n", name, strings.Join(rates[name], ", then "))
		}
	}
	if days > 0 {
		fmt.Fprintf(b3, "\nYear so far\n• %.0f therms over %d days, %.2f a day\n• Charges: %s", therms, days, therms/float64(days), usd(gasCost))
		if therms > 0 {
			fmt.Fprintf(b3, " = %s per therm on average", usd(gasCost/therms))
		}
		fmt.Fprintln(b3)
	}
	var out []string
	for _, msg := range []string{b1.String(), b2.String(), b3.String()} {
		out = append(out, pieces(strings.TrimSpace(msg), 3500)...)
	}
	return out
}

// pieces cuts a long message at blank lines into parts a chat will take whole.
func pieces(msg string, limit int) []string {
	var out []string
	cur := ""
	for _, block := range strings.Split(msg, "\n\n") {
		if cur != "" && len(cur)+len(block)+2 > limit {
			out = append(out, cur)
			cur = "(continued)\n\n" + block
			continue
		}
		if cur != "" {
			cur += "\n\n"
		}
		cur += block
	}
	return append(out, cur)
}
