package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
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

// What was spent on DoorDash food and groceries, month by month, worked out
// from every receipt.
//
// The order list is not enough for this: an order paid with DoorDash credits
// shows a total of $0.00 there. The receipt has the line "DoorDash Credits
// -$20.35", and what the order cost is what was charged plus those credits.
// A model asked for the breakdown added up list totals in its head and got
// the count and the sums wrong, so the reading and the arithmetic are done
// here and the finished report goes to the owner.
//
// Three things are kept apart from the food and grocery figures: gift cards
// bought through DoorDash (not food), cancelled orders, and the credits
// themselves, which are checked against where they came from. DoorDash's
// website shows a credit balance but no history of gift cards redeemed, so
// the check is an account of the year: credits used, credits given back for
// refunds, the balance now, and the DoorDash gift cards bought at Costco.

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
		case line == "Payment" || line == "Address" || line == "Store Address" || line == "Recipient" || line == "Refund issued":
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

var (
	// "Refund amount: $23.56 Issued to: DoorDash Credits", and on a cancelled order "We've issued $44.13 credits".
	refundToCredits = regexp.MustCompile(`Refund amount:\s*\$([\d,]+\.\d{2})\s*Issued to:\s*DoorDash Credits`)
	issuedCredits   = regexp.MustCompile(`issued \$([\d,]+\.\d{2}) credits`)
	itemCount       = regexp.MustCompile(`^\d+ Items?$`)
)

// creditsBack is what a receipt says was given back as DoorDash credits.
func creditsBack(text string) float64 {
	flat := strings.Join(strings.Fields(text), " ")
	sum := 0.0
	for _, re := range []*regexp.Regexp{refundToCredits, issuedCredits} {
		for _, m := range re.FindAllStringSubmatch(flat, -1) {
			sum += dollars(m[1])
		}
	}
	return sum
}

const (
	catRestaurant = "Restaurants"
	catGrocery    = "Grocery and retail"
	catGift       = "Gift cards"
)

// spend is what one order cost.
type spend struct {
	When     time.Time
	Store    string
	Category string
	Cuisine  string
	Item     string // for a gift card: which one
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
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if itemCount.MatchString(line) && i+1 < len(lines) {
			sp.Item = lines[i+1]
			break
		}
	}
	return sp, true
}

// A store's kind is read once from its own page and kept.
type storeInfo struct {
	Category string `json:"category"`
	Cuisine  string `json:"cuisine"`
}

var groceryName = regexp.MustCompile(`(?i)\b(grocery|groceries|bazar|bazaar|market|supermarket|mart|dashmart|pharmacy|convenience|liquor|foods)\b`)

func storesFile() string { return filepath.Join(session.Dir(), "doordash-stores.json") }

// ddStore says what kind of store this is. DoorDash sends grocery, pharmacy
// and retail stores to an address with "convenience" in it; a restaurant's
// page names its cuisine after the rating.
func ddStore(ctx context.Context, p *page.Page, known map[string]storeInfo, name, url string) storeInfo {
	if strings.Contains(strings.ToLower(name), "gift card") {
		return storeInfo{Category: catGift}
	}
	if in, ok := known[name]; ok {
		return in
	}
	in := storeInfo{Category: catRestaurant}
	if groceryName.MatchString(name) {
		in.Category = catGrocery
	}
	if url != "" && p.Goto(ctx, url) == nil {
		p.WaitContent(ctx, 8*time.Second)
		if s, err := p.Snapshot(ctx); err == nil {
			if strings.Contains(s.URL, "/convenience/") {
				in.Category = catGrocery
			}
			lines := strings.Split(s.Text, "\n")
			for i, line := range lines {
				if line == "DashPass" && i+2 < len(lines) && lines[i+1] == "•" {
					if c := lines[i+2]; c != name && len(c) < 40 && !strings.ContainsAny(c, "$0123456789") {
						in.Cuisine = c
					}
					break
				}
			}
			if groceryName.MatchString(in.Cuisine) {
				in.Category = catGrocery
			}
			known[name] = in // only what was really looked up is kept
		}
	}
	return in
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
			fmt.Fprintf(a.out, "STARTED a background job. It reads every DoorDash receipt of %d and sends Chinmay the report itself: food and grocery spending per month and per category (what was charged plus the DoorDash credits used), gift card purchases listed apart, and a check of the credits. The first time this takes about 25 minutes; later runs take a few minutes.\nNow reply to Chinmay: \"I am reading every DoorDash receipt of %d, including the DoorDash Credits line. You will get the breakdown here when it is done.\" Then stop. Do not add up amounts yourself and do not run this again. If he asks how it is going: novee-browse doordash-spending status\n", year, year)
			return exitOK, nil
		}
	}
	tail, _ := os.ReadFile(logPath)
	return exitError, fmt.Errorf("the job did not start: %s", strings.TrimSpace(string(tail)))
}

// ledger is the account of the year's DoorDash credits.
type ledger struct {
	Back      float64 // given back for refunds and cancelled orders
	BackNotes []string
	Balance   float64 // on the payment page now; negative if it could not be read
	Costco    []costcoCards
	CostcoErr string
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
	shop := map[string]string{}
	for _, o := range todo {
		if o.Shop != "" {
			shop[o.Store] = o.Shop
		}
	}
	var spends []spend
	var unread, cancelled []string
	led := ledger{Balance: -1}
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
				if back := creditsBack(text); back > 0 {
					led.Back += back
					led.BackNotes = append(led.BackNotes, fmt.Sprintf("%s %s $%.2f", when.Format("01/02"), o.Store, back))
				}
				if strings.Contains(text, "Order Cancelled") {
					cancelled = append(cancelled, o.Day.Format("01/02")+" "+o.Store)
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

		// What kind of store each one is.
		known := map[string]storeInfo{}
		if data, err := os.ReadFile(storesFile()); err == nil {
			_ = json.Unmarshal(data, &known)
		}
		for i := range spends {
			job.Phase = fmt.Sprintf("looking up store %d of %d", i+1, len(spends))
			job.save()
			session.Touch()
			in := ddStore(ctx, p, known, spends[i].Store, shop[spends[i].Store])
			spends[i].Category, spends[i].Cuisine = in.Category, in.Cuisine
		}
		if data, err := json.Marshal(known); err == nil {
			_ = os.WriteFile(storesFile(), data, 0o600)
		}

		// The credit balance, from the payment page.
		job.Phase = "reading the credit balance"
		job.save()
		if p.Goto(ctx, strings.Replace(doordashURL(), "/orders", "/consumer/payment", 1)) == nil {
			for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline) && ctx.Err() == nil; time.Sleep(time.Second) {
				s, err := p.Snapshot(ctx)
				if err != nil {
					break
				}
				lines := strings.Split(s.Text, "\n")
				for i, line := range lines {
					if line == "DoorDash Credits" && i+1 < len(lines) && money.MatchString(lines[i+1]) {
						led.Balance = dollars(lines[i+1])
					}
				}
				if led.Balance >= 0 {
					break
				}
			}
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

	// DoorDash gift cards bought at Costco are where the credits come from.
	job.Phase = "checking Costco for DoorDash gift cards"
	job.save()
	if cards, err := a.costcoDoordashCards(ctx, year); err != nil {
		led.CostcoErr = err.Error()
		fmt.Fprintln(a.out, "Costco check:", err)
	} else {
		led.Costco = cards
	}

	report := spendingReport(year, spends, unread, cancelled, led)
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
	_ = w.Write([]string{"date", "time", "store", "category", "cuisine", "item", "subtotal", "charged", "doordash_credits", "net", "receipt"})
	for _, s := range spends {
		item := ""
		if s.Category == catGift {
			item = s.Item
		}
		_ = w.Write([]string{s.When.Format("2006-01-02"), s.When.Format("15:04"), s.Store, s.Category, s.Cuisine, item, fmt.Sprintf("%.2f", s.Subtotal),
			fmt.Sprintf("%.2f", s.Charged), fmt.Sprintf("%.2f", s.Credits), fmt.Sprintf("%.2f", s.Net()), s.URL})
	}
	w.Flush()
	if err := f.Close(); err != nil {
		return err
	}
	if err := notifyOwner(report); err != nil {
		return err
	}
	return notifyOwnerFile(fmt.Sprintf("DoorDash %d, one line per order with its category (opens in a spreadsheet).", year), path)
}

type sum struct {
	n                     int
	charged, credits, net float64
}

func (t *sum) add(s spend) {
	t.n++
	t.charged += s.Charged
	t.credits += s.Credits
	t.net += s.Net()
}

// spendingReport is the message the owner gets: plain lines, no table.
func spendingReport(year int, spends []spend, unread, cancelled []string, led ledger) string {
	var months, rest, groc [13]sum
	var all, allRest, allGroc, gifts sum
	var giftLines []string
	var first, last time.Time
	for _, s := range spends {
		if s.Category == catGift {
			gifts.add(s)
			giftLines = append(giftLines, fmt.Sprintf("- %s %s: charged $%.2f + credits $%.2f", s.When.Format("01/02"), s.Item, s.Charged, s.Credits))
			continue
		}
		if first.IsZero() {
			first = s.When
		}
		last = s.When
		months[s.When.Month()].add(s)
		all.add(s)
		if s.Category == catGrocery {
			groc[s.When.Month()].add(s)
			allGroc.add(s)
		} else {
			rest[s.When.Month()].add(s)
			allRest.add(s)
		}
	}
	b := &strings.Builder{}
	fmt.Fprintf(b, "DoorDash food and grocery spending %d, from %d receipts", year, all.n)
	if all.n > 0 {
		fmt.Fprintf(b, " (%s to %s)", first.Format("01/02"), last.Format("01/02"))
	}
	fmt.Fprintln(b, ".\nNet = charged to your card + DoorDash credits used. Gift card purchases and cancelled orders are not in these figures.")
	for m := time.January; m <= time.December; m++ {
		if s := months[m]; s.n > 0 {
			fmt.Fprintf(b, "- %s: %d orders, net $%.2f (charged $%.2f + credits $%.2f). Restaurants $%.2f, grocery $%.2f\n",
				m.String()[:3], s.n, s.net, s.charged, s.credits, rest[m].net, groc[m].net)
		}
	}
	fmt.Fprintf(b, "Total: %d orders, net $%.2f (charged $%.2f + credits $%.2f)\n", all.n, all.net, all.charged, all.credits)
	fmt.Fprintf(b, "- Restaurants: %d orders, net $%.2f\n- Grocery and retail: %d orders, net $%.2f\n", allRest.n, allRest.net, allGroc.n, allGroc.net)

	if gifts.n > 0 {
		fmt.Fprintf(b, "\nGift cards bought through DoorDash, counted apart: %d, charged $%.2f + credits $%.2f\n%s\n",
			gifts.n, gifts.charged, gifts.credits, strings.Join(giftLines, "\n"))
	}

	// The credits, checked against where they came from.
	used := all.credits + gifts.credits
	fmt.Fprintf(b, "\nDoorDash credits check for %d:\n", year)
	fmt.Fprintf(b, "- Credits used: $%.2f on food and grocery, $%.2f on gift cards\n", all.credits, gifts.credits)
	fmt.Fprintf(b, "- Credits given back to you for refunds and cancelled orders: $%.2f", led.Back)
	if len(led.BackNotes) > 0 {
		fmt.Fprintf(b, " (%s)", strings.Join(led.BackNotes, "; "))
	}
	fmt.Fprintln(b)
	if led.Balance >= 0 {
		fmt.Fprintf(b, "- Credit balance now: $%.2f\n", led.Balance)
		loaded := used + led.Balance - led.Back
		cards := int(loaded / 50)
		fmt.Fprintf(b, "- So the credits that came from gift cards (or were there on 01/01) come to $%.2f: %d x $50 and $%.2f over\n", loaded, cards, loaded-float64(cards)*50)
	} else {
		fmt.Fprintln(b, "- The credit balance could not be read from the payment page")
	}
	switch {
	case led.CostcoErr != "":
		fmt.Fprintf(b, "- DoorDash gift cards bought at Costco could not be checked: %s\n", led.CostcoErr)
	case len(led.Costco) == 0:
		fmt.Fprintf(b, "- Costco's online orders of %d show no DoorDash gift cards\n", year)
	default:
		n, face, paid := 0, 0.0, 0.0
		var when []string
		for _, c := range led.Costco {
			n += c.Cards
			face += c.Face
			paid += c.Paid
			when = append(when, fmt.Sprintf("%s %d x $%.0f", c.When.Format("01/02"), c.Cards, c.Each))
		}
		fmt.Fprintf(b, "- DoorDash gift cards bought at Costco in %d: %d cards, $%.2f of credit for $%.2f (%s)\n", year, n, face, paid, strings.Join(when, "; "))
	}
	fmt.Fprint(b, "DoorDash's website shows the balance but no list of redeemed gift cards, so this is worked out from the receipts.")

	if len(cancelled) > 0 {
		fmt.Fprintf(b, "\n\nNot counted, cancelled: %s.", strings.Join(cancelled, "; "))
	}
	if len(unread) > 0 {
		fmt.Fprintf(b, "\nNOT included, could not be read: %s.", strings.Join(unread, "; "))
	}
	return b.String()
}

// costcoCards is one Costco online order of DoorDash gift cards.
type costcoCards struct {
	When  time.Time
	Cards int
	Each  float64 // the value of one card
	Face  float64 // the credit they add up to
	Paid  float64
}

var (
	cardPack = regexp.MustCompile(`(?i)\b(one|two|three|four|five|six|eight|ten|\d+)\s+\$(\d+)`)
	oneCard  = regexp.MustCompile(`\$(\d+)`)
	quantity = regexp.MustCompile(`Quantity\s+(\d+)`)
	numWords = map[string]int{"one": 1, "two": 2, "three": 3, "four": 4, "five": 5, "six": 6, "eight": 8, "ten": 10}
)

// costcoDoordashCards finds the DoorDash gift cards among the year's Costco
// online orders and reads each order's page for how many were bought.
func (a *app) costcoDoordashCards(ctx context.Context, year int) ([]costcoCards, error) {
	real := a.out
	a.out = &strings.Builder{} // a sign-in request here is not the owner's business in the middle of this report
	ok, _, err := a.costcoWarehouse(ctx)
	a.out = real
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("Costco is not signed in")
	}
	type order struct {
		Num  string
		When time.Time
		Paid float64
	}
	var orders []order
	var cards []costcoCards
	_, err = a.withPage(func(p *page.Page) (int, error) {
		s, err := p.Snapshot(ctx)
		if err != nil {
			return exitError, err
		}
		_, labels, err := costcoPeriods(s, year)
		if err != nil {
			return exitError, err
		}
		tab, ok := named(s, "button", "Online")
		if !ok {
			return exitError, errors.New("the Costco orders page has no Online tab")
		}
		if err := p.Click(ctx, tab.Ref); err != nil {
			return exitError, err
		}
		time.Sleep(2 * time.Second)
		got := map[string]bool{}
		for _, label := range labels {
			if err := eachPage(ctx, p, label, func(s page.Snapshot) (string, error) {
				top := ""
				// One piece of text per order: its number, date, total and what was in it.
				for _, chunk := range strings.Split(strings.Join(strings.Fields(s.Text), " "), "Online Order Number ")[1:] {
					m := onlineEntry.FindStringSubmatch(chunk)
					if m == nil {
						continue
					}
					if top == "" {
						top = m[1]
					}
					when, err := time.Parse("01/02/2006", m[2])
					low := strings.ToLower(chunk)
					if err == nil && when.Year() == year && !got[m[1]] && strings.Contains(low, "doordash") && strings.Contains(low, "gift") {
						got[m[1]] = true
						orders = append(orders, order{m[1], when, dollars(m[3])})
					}
				}
				return top, nil
			}); err != nil {
				return exitError, err
			}
		}
		for _, o := range orders {
			session.Touch()
			if ok, err := p.Hash(ctx, `(ordersandpurchases|orderdetails).*$`, "orderdetails/"+o.Num); err != nil || !ok {
				return exitError, errors.New("the Costco order page could not be opened")
			}
			text := ""
			for deadline := time.Now().Add(25 * time.Second); time.Now().Before(deadline) && ctx.Err() == nil; time.Sleep(time.Second) {
				if s, err := p.Snapshot(ctx); err == nil && strings.Contains(s.Text, o.Num) && strings.Contains(s.Text, "Quantity") {
					text = s.Text
					break
				}
			}
			c := costcoCards{When: o.When, Paid: o.Paid}
			for _, line := range strings.Split(text, "\n") {
				low := strings.ToLower(line)
				if c.Each > 0 || !strings.Contains(low, "doordash") || !strings.Contains(low, "gift") {
					continue
				}
				per := 1
				if m := cardPack.FindStringSubmatch(line); m != nil { // "DoorDash Two $50 eGift Cards"
					if per = numWords[strings.ToLower(m[1])]; per == 0 {
						per, _ = strconv.Atoi(m[1])
					}
					c.Each, _ = strconv.ParseFloat(m[2], 64)
				} else if m := oneCard.FindStringSubmatch(line); m != nil {
					c.Each, _ = strconv.ParseFloat(m[1], 64)
				}
				c.Cards = per
			}
			if m := quantity.FindStringSubmatch(text); m != nil && c.Each > 0 {
				q, _ := strconv.Atoi(m[1])
				c.Cards *= q
			}
			c.Face = float64(c.Cards) * c.Each
			if c.Each > 0 {
				cards = append(cards, c)
			}
		}
		return exitOK, nil
	})
	sort.SliceStable(cards, func(i, j int) bool { return cards[i].When.Before(cards[j].When) })
	return cards, err
}
