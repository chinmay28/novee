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

func storesFile(sh *shop) string { return filepath.Join(session.Dir(), sh.key+"-stores.json") }

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

func (a *app) doordashSpending(ctx context.Context, args []string) (int, error) {
	return a.shopSpending(ctx, shops["doordash"], args)
}

// ddBalance reads the credit balance from the payment page.
func ddBalance(ctx context.Context, p *page.Page) float64 {
	if p.Goto(ctx, strings.Replace(doordashURL(), "/orders", "/consumer/payment", 1)) != nil {
		return -1
	}
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline) && ctx.Err() == nil; time.Sleep(time.Second) {
		s, err := p.Snapshot(ctx)
		if err != nil {
			break
		}
		lines := strings.Split(s.Text, "\n")
		for i, line := range lines {
			if line == "DoorDash Credits" && i+1 < len(lines) && money.MatchString(lines[i+1]) {
				return dollars(lines[i+1])
			}
		}
	}
	return -1
}

// shopSpending starts the report job, or shows how far it is.
func (a *app) shopSpending(ctx context.Context, sh *shop, args []string) (int, error) {
	usage := fmt.Sprintf("usage: %s-spending YEAR | status. Example: %s-spending 2026", sh.key, sh.key)
	if len(args) == 1 && (args[0] == "status" || args[0] == "stop") {
		return a.yearPDF(ctx, sh.key, args)
	}
	year, err := yearArg(args, usage)
	if err != nil || len(args) != 1 {
		return exitError, errors.New(usage)
	}
	if job, running := currentJob(); running {
		fmt.Fprintf(a.out, "A job is ALREADY RUNNING: %s. Only one runs at a time; do not start another. Tell Chinmay it is still working and that the result will arrive in the chat.\n", job.describe())
		return exitOK, nil
	}
	if ok, code, err := a.signedIn(ctx, sh.url(), sh.name); !ok {
		return code, err
	}
	logPath := filepath.Join(session.Dir(), "costco-job.log")
	_ = os.Remove(logPath)
	if err := session.Spawn("spending", append(append([]string{}, a.job...), fmt.Sprintf("%s-spending:%d", sh.key, year)), logPath); err != nil {
		return exitError, err
	}
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if _, ok := currentJob(); ok {
			fmt.Fprintf(a.out, "STARTED a background job. It reads every %s receipt of %d and sends Chinmay the report itself: food and grocery spending per month and per category (what was charged plus the %s used), gift card purchases listed apart, and a check of the credits. The first time this takes up to 25 minutes; later runs take a few minutes.\nNow reply to Chinmay: \"I am reading every %s receipt of %d, including the %s. You will get the breakdown here when it is done.\" Then stop. Do not add up amounts yourself and do not run this again. If he asks how it is going: novee-browse %s-spending status\n", sh.name, year, sh.credit, sh.name, year, sh.credit, sh.key)
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

func (a *app) runShopSpending(ctx context.Context, sh *shop, job *yearJob, want map[int]bool) error {
	year := 0
	for y := range want {
		year = y
	}
	job.Phase = "reading the order history"
	job.save()
	orders, ok, _, err := sh.list(a, ctx, year)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New(sh.name + " is not signed in. A sign-in link was sent; sign in and ask again")
	}
	var todo []ddOrder
	var unread, cancelled []string
	for _, o := range orders {
		switch {
		case o.Day.Year() != year:
		case o.Cancelled:
			cancelled = append(cancelled, o.Day.Format("01/02")+" "+o.Store)
		default:
			todo = append(todo, o)
		}
	}
	shopURL := map[string]string{}
	for _, o := range todo {
		if o.Shop != "" {
			shopURL[o.Store] = o.Shop
		}
	}
	var spends []spend
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
				when, text, _, err := sh.fetch(ctx, p, o)
				if err != nil {
					fmt.Fprintf(a.out, "round %d: could not read %s: %v\n", round, o, err)
					again = append(again, o)
					continue
				}
				if when.Year() != year {
					continue
				}
				if back := sh.back(text); back > 0 {
					led.Back += back
					led.BackNotes = append(led.BackNotes, fmt.Sprintf("%s %s $%.2f", when.Format("01/02"), o.Store, back))
				}
				if strings.Contains(text, "Order Cancelled") {
					cancelled = append(cancelled, o.Day.Format("01/02")+" "+o.Store)
					continue
				}
				sp, ok := sh.spend(o, when, text)
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
		if data, err := os.ReadFile(storesFile(sh)); err == nil {
			_ = json.Unmarshal(data, &known)
		}
		for i := range spends {
			job.Phase = fmt.Sprintf("looking up store %d of %d", i+1, len(spends))
			job.save()
			session.Touch()
			in := sh.store(ctx, p, known, spends[i].Store, shopURL[spends[i].Store])
			spends[i].Category, spends[i].Cuisine = in.Category, in.Cuisine
		}
		if data, err := json.Marshal(known); err == nil {
			_ = os.WriteFile(storesFile(sh), data, 0o600)
		}

		// The credit balance, from the payment page.
		job.Phase = "reading the credit balance"
		job.save()
		led.Balance = sh.balance(ctx, p)
		return exitOK, nil
	})
	if err != nil {
		return err
	}
	if len(spends) == 0 {
		return notifyOwner(fmt.Sprintf("%s spending %d: no receipts could be read.", sh.name, year))
	}
	sort.SliceStable(spends, func(i, j int) bool { return spends[i].When.Before(spends[j].When) })

	// DoorDash gift cards bought at Costco are where the credits come from.
	job.Phase = "checking Costco for gift cards"
	job.save()
	if cards, err := a.costcoGiftCards(ctx, sh.card); err != nil {
		led.CostcoErr = err.Error()
		fmt.Fprintln(a.out, "Costco check:", err)
	} else {
		led.Costco = cards
	}

	for i, j := 0, len(led.BackNotes)-1; i < j; i, j = i+1, j-1 { // oldest first, like everything else
		led.BackNotes[i], led.BackNotes[j] = led.BackNotes[j], led.BackNotes[i]
	}
	report := spendingReport(sh, year, spends, unread, cancelled, led)
	fmt.Fprintln(a.out, strings.Join(report, "\n\n"))

	dir := filepath.Join(outDir(), sh.key)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(dir, fmt.Sprintf("%s-spending-%d.csv", strings.ReplaceAll(sh.name, " ", ""), year))
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	w := csv.NewWriter(f)
	rate := costcoRate(led.Costco) // credits at the price paid for them at Costco, if that is known
	if rate == 0 || !sh.priced {
		rate = 1
	}
	_ = w.Write([]string{"date", "time", "store", "category", "cuisine", "item", "subtotal", "charged", "credits", "net", "real_cost", "receipt"})
	for _, s := range spends {
		item := ""
		if s.Category == catGift {
			item = s.Item
		}
		_ = w.Write([]string{s.When.Format("2006-01-02"), s.When.Format("15:04"), s.Store, s.Category, s.Cuisine, item, fmt.Sprintf("%.2f", s.Subtotal),
			fmt.Sprintf("%.2f", s.Charged), fmt.Sprintf("%.2f", s.Credits), fmt.Sprintf("%.2f", s.Net()), fmt.Sprintf("%.2f", s.Charged+s.Credits*rate), s.URL})
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
	return notifyOwnerFile(fmt.Sprintf("%s %d, one line per order with its category (opens in a spreadsheet).", sh.name, year), path)
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

// usd writes an amount the way a person does: $5,059.43.
func usd(v float64) string {
	neg := v < 0
	if neg {
		v = -v
	}
	whole, cents, _ := strings.Cut(fmt.Sprintf("%.2f", v), ".")
	for i := len(whole) - 3; i > 0; i -= 3 {
		whole = whole[:i] + "," + whole[i:]
	}
	if neg {
		return "-$" + whole + "." + cents
	}
	return "$" + whole + "." + cents
}

// costcoRate is what a dollar of DoorDash credit cost at Costco; 0 if no
// purchase is known.
func costcoRate(cards []costcoCards) float64 {
	paid, face := 0.0, 0.0
	for _, c := range cards {
		paid += c.Paid
		face += c.Face
	}
	if face == 0 {
		return 0
	}
	return paid / face
}

// spendingReport is what the owner reads in a chat: a heading per month with
// a few bullet lines under it, no tables. It comes as two messages, the
// months and the credits, because one would be too long for a chat.
func spendingReport(sh *shop, year int, spends []spend, unread, cancelled []string, led ledger) []string {
	var months, rest, groc [13]sum
	var all, allRest, allGroc, gifts sum
	var giftLines []string
	var first, last time.Time
	for _, s := range spends {
		if s.Category == catGift {
			gifts.add(s)
			line := fmt.Sprintf("• %s %s: %s", s.When.Format("01/02"), s.Item, usd(s.Net()))
			if s.Credits > 0 {
				line += fmt.Sprintf(" (%s of it with credits)", usd(s.Credits))
			}
			giftLines = append(giftLines, line)
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
	rate := costcoRate(led.Costco)
	if !sh.priced {
		rate = 0
	}
	now := time.Now()

	// First message: the months.
	b := &strings.Builder{}
	fmt.Fprintf(b, "%s Spending Breakdown - %d\n", sh.name, year)
	fmt.Fprintf(b, "Food and grocery from %d receipts", all.n)
	if all.n > 0 {
		fmt.Fprintf(b, ", %s to %s", first.Format("01/02"), last.Format("01/02"))
	}
	fmt.Fprintf(b, ".\nSpent = charged to your card + %s used.\n", sh.credit)
	block := func(title string, s, r, g sum) {
		fmt.Fprintf(b, "\n%s\n• Orders: %d\n• Spent: %s\n", title, s.n, usd(s.net))
		if s.credits > 0.005 {
			fmt.Fprintf(b, "• Card %s + %s %s\n", usd(s.charged), sh.short, usd(s.credits))
		}
		if g.n > 0 {
			fmt.Fprintf(b, "• Restaurants %s, grocery %s\n", usd(r.net), usd(g.net))
		}
		if rate > 0 && s.credits > 0.005 {
			fmt.Fprintf(b, "• Real cost: %s\n", usd(s.charged+s.credits*rate))
		}
	}
	for m := time.January; m <= time.December; m++ {
		if s := months[m]; s.n > 0 {
			title := m.String()
			if year == now.Year() && m == now.Month() {
				title += " (so far)"
			}
			block(title, s, rest[m], groc[m])
		}
	}
	fmt.Fprintf(b, "\nTotal %d\n• Orders: %d\n• Spent: %s\n• Card %s + %s %s\n", year, all.n, usd(all.net), usd(all.charged), sh.short, usd(all.credits))
	if allGroc.n > 0 {
		fmt.Fprintf(b, "• Restaurants: %d orders, %s\n• Grocery and retail: %d orders, %s\n", allRest.n, usd(allRest.net), allGroc.n, usd(allGroc.net))
	}
	if rate > 0 {
		fmt.Fprintf(b, "• Real cost: %s (gift cards bought at Costco for %.0f cents per $1 saved you %s)\n", usd(all.charged+all.credits*rate), rate*100, usd(all.credits*(1-rate)))
	}
	if len(cancelled) > 0 {
		fmt.Fprintf(b, "\nNot counted, cancelled:\n• %s\n", strings.Join(cancelled, "\n• "))
	}
	if len(unread) > 0 {
		fmt.Fprintf(b, "\n⚠️ Not included, could not be read:\n• %s\n", strings.Join(unread, "\n• "))
	}
	if rate > 0 {
		fmt.Fprint(b, "\nReal cost counts each credit dollar at what its gift card cost at Costco.")
	}
	monthsMsg := strings.TrimSpace(b.String())

	// Second message: gift cards and the credits, checked against where they came from.
	b = &strings.Builder{}
	fmt.Fprintf(b, "%s: %s and Gift Cards - %d\n", sh.name, sh.credit, year)
	if gifts.n > 0 {
		fmt.Fprintf(b, "\nOther gift cards bought through "+sh.name+"\n(not in the food figures)\n%s\n• Total: %s\n", strings.Join(giftLines, "\n"), usd(gifts.net))
	}
	used := all.credits + gifts.credits
	fmt.Fprintf(b, "\n"+sh.credit+" check\n• Used on food and grocery: %s\n• Used on gift cards: %s\n", usd(all.credits), usd(gifts.credits))
	fmt.Fprintf(b, "• Given back for refunds and cancellations: %s\n", usd(led.Back))
	for _, n := range led.BackNotes {
		fmt.Fprintf(b, "   - %s\n", n)
	}
	loaded := 0.0
	if led.Balance >= 0 {
		loaded = used + led.Balance - led.Back
		cards := int(loaded / 50)
		fmt.Fprintf(b, "• Balance now: %s\n• Came from gift cards, or was there on 01/01: %s (%d x $50 + %s)\n", usd(led.Balance), usd(loaded), cards, usd(loaded-float64(cards)*50))
	} else {
		fmt.Fprintln(b, "• The balance now is not known, so how much came from gift cards cannot be worked out")
	}
	switch {
	case led.CostcoErr != "":
		fmt.Fprintf(b, "\n⚠️ %s gift cards bought at Costco could not be checked: %s\n", sh.cardName, led.CostcoErr)
	case len(led.Costco) == 0:
		fmt.Fprintf(b, "\nCostco's online orders show no %s gift cards.\n", sh.cardName)
	default:
		var yr, total struct {
			n          int
			face, paid float64
		}
		before := 0.0
		fmt.Fprintln(b, "\n"+sh.cardName+" gift cards bought at Costco\n(every online order Costco still lists)")
		for _, c := range led.Costco {
			fmt.Fprintf(b, "• %s: %d x $%.0f for %s\n", c.When.Format("01/02/2006"), c.Cards, c.Each, usd(c.Paid))
			total.n += c.Cards
			total.face += c.Face
			total.paid += c.Paid
			switch {
			case c.When.Year() == year:
				yr.n += c.Cards
				yr.face += c.Face
				yr.paid += c.Paid
			case c.When.Year() < year:
				before += c.Face
			}
		}
		fmt.Fprintf(b, "• All together: %d cards, %s of credit for %s (%.0f cents per $1)\n", total.n, usd(total.face), usd(total.paid), 100*total.paid/total.face)
		fmt.Fprintf(b, "• Bought in %d: %d cards, %s of credit\n• Bought before %d: %s of credit\n", year, yr.n, usd(yr.face), year, usd(before))
		if led.Balance >= 0 {
			fmt.Fprintln(b, "\nDoes it add up?")
			if gap := loaded - yr.face; gap > 0.005 {
				fmt.Fprintf(b, "• %d needed %s of gift card credit; cards bought in %d give %s\n• The other %s came from cards bought earlier (Costco lists %s before %d) or from somewhere else\n", year, usd(loaded), year, usd(yr.face), usd(gap), usd(before), year)
			} else {
				fmt.Fprintf(b, "• %d needed %s of gift card credit; cards bought in %d give %s\n• %s of them is not used up, or not redeemed yet\n", year, usd(loaded), year, usd(yr.face), usd(-gap))
			}
		}
	}
	fmt.Fprint(b, "\n"+sh.history)
	return []string{monthsMsg, strings.TrimSpace(b.String())}
}

// costcoCards is one Costco online order of DoorDash gift cards.
type costcoCards struct {
	Order string
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

// costcoDoordashCards finds the DoorDash gift cards among all the Costco
// online orders the site still lists (about three years) and reads each
// order's page for how many were bought and at what price.
func (a *app) costcoGiftCards(ctx context.Context, card string) ([]costcoCards, error) {
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
		sel, ok := named(s, "select", "Showing")
		if !ok {
			return exitError, errors.New("the Costco orders page has no period drop-down")
		}
		labels := sel.Options
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
					if err == nil && !got[m[1]] && strings.Contains(low, card) && strings.Contains(low, "gift") {
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
			c := costcoCards{When: o.When, Paid: o.Paid, Order: o.Num}
			lines := strings.Split(text, "\n")
			unit := 0.0
			for i, line := range lines {
				low := strings.ToLower(line)
				if c.Each > 0 || !strings.Contains(low, card) || !strings.Contains(low, "gift") {
					continue
				}
				for _, next := range lines[i+1 : min(i+6, len(lines))] { // the price of one pack follows its name
					if money.MatchString(next) {
						unit = dollars(next)
						break
					}
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
				if unit > 0 { // what the cards cost, without anything else that was in the order
					c.Paid = unit * float64(q)
				}
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
