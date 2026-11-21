package main

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/chinmay28/novee/skills/web-browse/page"
	"github.com/chinmay28/novee/skills/web-browse/session"
)

// What was spent on DoorDash, month by month, worked out from every receipt.
//
// The order list is not enough for this: an order paid with DoorDash credits
// shows a total of $0.00 there. The receipt has the line "DoorDash Credits
// -$20.35", and what the order cost is what was charged plus those credits.
// A model asked for the breakdown added up list totals in its head and got
// the count and the sums wrong, so the reading and the arithmetic are done
// here and the finished report goes to the owner.

var money = regexp.MustCompile(`^-?\$[\d,]+\.\d{2}$`)

func dollars(s string) float64 {
	neg := strings.HasPrefix(s, "-")
	f, _ := strconv.ParseFloat(strings.NewReplacer("$", "", ",", "", "-", "").Replace(s), 64)
	if neg {
		return -f
	}
	return f
}

// ddAmounts reads the price lines of a receipt: a label, then one amount, or
// two when a fee was reduced (the one charged comes last).
func ddAmounts(text string) map[string]float64 {
	out := map[string]float64{}
	label, started := "", false
	for _, line := range strings.Split(text, "\n") {
		switch {
		case line == "Subtotal":
			started, label = true, line
		case !started:
		case line == "Payment" || line == "Address" || line == "Store Address" || line == "Recipient":
			return out
		case money.MatchString(line):
			if label != "" {
				out[label] = dollars(line)
			}
		default:
			label = line
		}
	}
	return out
}

// spend is what one order cost.
type spend struct {
	When     time.Time
	Store    string
	Subtotal float64
	Charged  float64 // the receipt's Total: what the card paid
	Credits  float64 // DoorDash credits used, as a positive amount
	URL      string
}

func (s spend) Net() float64 { return s.Charged + s.Credits }

func ddSpend(o ddOrder, when time.Time, text string) (spend, bool) {
	am := ddAmounts(text)
	total, ok := am["Total"]
	if !ok {
		return spend{}, false
	}
	sp := spend{When: when, Store: o.Store, Subtotal: am["Subtotal"], Charged: total, URL: o.URL}
	for label, v := range am {
		if strings.Contains(strings.ToLower(label), "credit") && v < 0 {
			sp.Credits += -v
		}
	}
	return sp, true
}

// doordashSpending starts the report job, or shows how far it is.
func (a *app) doordashSpending(ctx context.Context, args []string) (int, error) {
	const usage = "usage: doordash-spending YEAR | status. Example: doordash-spending 2026"
	if len(args) == 1 && (args[0] == "status" || args[0] == "stop") {
		return a.yearPDF(ctx, "doordash", args)
	}
	year, err := yearArg(args, usage)
	if err != nil || len(args) != 1 {
		return exitError, errors.New(usage)
	}
	if job, running := currentJob(); running {
		fmt.Fprintf(a.out, "A job is ALREADY RUNNING: %s. Only one runs at a time; do not start another. Tell Chinmay it is still working and that the result will arrive in the chat.\n", job.describe())
		return exitOK, nil
	}
	if ok, code, err := a.signedIn(ctx, doordashURL(), "DoorDash"); !ok {
		return code, err
	}
	logPath := filepath.Join(session.Dir(), "costco-job.log")
	_ = os.Remove(logPath)
	if err := session.Spawn("spending", append(append([]string{}, a.job...), fmt.Sprintf("doordash-spending:%d", year)), logPath); err != nil {
		return exitError, err
	}
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if _, ok := currentJob(); ok {
			fmt.Fprintf(a.out, "STARTED a background job. It reads every DoorDash receipt of %d, adds up per month what was charged and the DoorDash credits used, and sends Chinmay the breakdown itself. The first time this takes about 20 minutes; later runs take a minute or two.\nNow reply to Chinmay: \"I am reading every DoorDash receipt of %d, including the DoorDash Credits line. You will get the monthly breakdown here when it is done, in about 20 minutes.\" Then stop. Do not add up amounts yourself and do not run this again. If he asks how it is going: novee-browse doordash-spending status\n", year, year)
			return exitOK, nil
		}
	}
	tail, _ := os.ReadFile(logPath)
	return exitError, fmt.Errorf("the job did not start: %s", strings.TrimSpace(string(tail)))
}

func (a *app) runDoordashSpending(ctx context.Context, job *yearJob, want map[int]bool) error {
	year := 0
	for y := range want {
		year = y
	}
	job.Phase = "reading the order history"
	job.save()
	orders, ok, _, err := a.ddList(ctx, year)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("DoorDash is not signed in. A sign-in link was sent; sign in and ask again")
	}
	var todo []ddOrder
	for _, o := range orders {
		if o.Day.Year() == year {
			todo = append(todo, o)
		}
	}
	var spends []spend
	var unread []string
	_, err = a.withPage(func(p *page.Page) (int, error) {
		for round := 1; round <= 2 && len(todo) > 0; round++ {
			var again []ddOrder
			for i, o := range todo {
				if ctx.Err() != nil {
					return exitError, ctx.Err()
				}
				job.Phase = fmt.Sprintf("receipt %d of %d", i+1, len(todo))
				job.Fetched = len(spends)
				job.save()
				session.Touch()
				when, text, _, err := ddFetch(ctx, p, o)
				if err != nil {
					fmt.Fprintf(a.out, "round %d: could not read %s: %v\n", round, o, err)
					again = append(again, o)
					continue
				}
				if when.Year() != year {
					continue
				}
				sp, ok := ddSpend(o, when, text)
				if !ok {
					fmt.Fprintf(a.out, "no Total on the receipt of %s\n", o)
					unread = append(unread, o.String())
					continue
				}
				spends = append(spends, sp)
			}
			todo = again
		}
		for _, o := range todo {
			unread = append(unread, o.String())
		}
		return exitOK, nil
	})
	if err != nil {
		return err
	}
	if len(spends) == 0 {
		return notifyOwner(fmt.Sprintf("DoorDash spending %d: no receipts could be read.", year))
	}
	sort.SliceStable(spends, func(i, j int) bool { return spends[i].When.Before(spends[j].When) })
	report := spendingReport(year, spends, unread)
	fmt.Fprintln(a.out, report)

	dir := filepath.Join(outDir(), "doordash")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(dir, fmt.Sprintf("DoorDash-spending-%d.csv", year))
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	w := csv.NewWriter(f)
	_ = w.Write([]string{"date", "time", "store", "subtotal", "charged", "doordash_credits", "net", "receipt"})
	for _, s := range spends {
		_ = w.Write([]string{s.When.Format("2006-01-02"), s.When.Format("15:04"), s.Store, fmt.Sprintf("%.2f", s.Subtotal),
			fmt.Sprintf("%.2f", s.Charged), fmt.Sprintf("%.2f", s.Credits), fmt.Sprintf("%.2f", s.Net()), s.URL})
	}
	w.Flush()
	if err := f.Close(); err != nil {
		return err
	}
	if err := notifyOwner(report); err != nil {
		return err
	}
	return notifyOwnerFile(fmt.Sprintf("DoorDash %d, one line per order (opens in a spreadsheet).", year), path)
}

// spendingReport is the message the owner gets: plain lines, no table.
func spendingReport(year int, spends []spend, unread []string) string {
	type sum struct {
		n                     int
		charged, credits, net float64
	}
	var months [13]sum
	var all, gifts sum
	for _, s := range spends {
		for _, t := range []*sum{&months[s.When.Month()], &all} {
			t.n++
			t.charged += s.Charged
			t.credits += s.Credits
			t.net += s.Net()
		}
		if strings.Contains(strings.ToLower(s.Store), "gift card") {
			gifts.n++
			gifts.net += s.Net()
		}
	}
	b := &strings.Builder{}
	fmt.Fprintf(b, "DoorDash spending %d, from %d receipts (%s to %s).\n", year, all.n,
		spends[0].When.Format("01/02"), spends[len(spends)-1].When.Format("01/02"))
	fmt.Fprintln(b, "Net = charged to your card + DoorDash credits used.")
	for m := time.January; m <= time.December; m++ {
		if s := months[m]; s.n > 0 {
			fmt.Fprintf(b, "- %s: %d orders, charged $%.2f + credits $%.2f = net $%.2f\n", m.String()[:3], s.n, s.charged, s.credits, s.net)
		}
	}
	fmt.Fprintf(b, "Total: %d orders, charged $%.2f + credits $%.2f = net $%.2f", all.n, all.charged, all.credits, all.net)
	if gifts.n > 0 {
		fmt.Fprintf(b, "\nIncluded above: %d gift card purchases through DoorDash, $%.2f. If those gift cards became the credits, that money is counted twice.", gifts.n, gifts.net)
	}
	if len(unread) > 0 {
		fmt.Fprintf(b, "\nNOT included, could not be read: %s.", strings.Join(unread, "; "))
	}
	return b.String()
}
