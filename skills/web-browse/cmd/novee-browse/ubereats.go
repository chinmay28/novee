package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/chinmay28/novee/skills/web-browse/page"
	"github.com/chinmay28/novee/skills/web-browse/session"
)

// Uber Eats orders, read like DoorDash's (see doordash.go for the commands).
//
// What is different here: the past orders page shows a few orders and a
// "Show more" button; a receipt is a pop-up on that page holding a framed
// document, with the full date, the total and under "Payments" how it was
// paid. "Uber Cash" there is this site's stored money, the counterpart of
// DoorDash credits, and it is filled from Uber gift cards.

const ubereatsOrders = "https://www.ubereats.com/orders"

func ubereatsURL() string {
	if u := os.Getenv("NOVEE_UBEREATS_URL"); u != "" { // tests point this at a fake site
		return u
	}
	return ubereatsOrders
}

var (
	uePrice = regexp.MustCompile(`^\d+ items? for (-?\$[\d,]+\.\d{2})$`)
	ueWhen  = regexp.MustCompile(`^(Canceled on )?([A-Z][a-z]{2}) (\d{1,2})(?:, (\d{4}))? at (\d{1,2}:\d{2} [AP]M)$`)
	ueDate  = regexp.MustCompile(`\b([A-Z][a-z]{2} \d{1,2}, \d{4})\b`)
)

// ueParse reads the past orders as they are on screen, newest first: per
// order the store, "2 items for $36.76", "Oct 30 at 12:31 PM", "View receipt".
func ueParse(s page.Snapshot, now time.Time) ([]ddOrder, error) {
	var links []string
	shopURLs := map[string]string{}
	for _, e := range s.Elements {
		if e.Kind == "link" && e.Name == "View receipt" {
			links = append(links, e.Href)
		} else if e.Kind == "link" && strings.Contains(e.Href, "/store/") && e.Name != "View store" {
			shopURLs[e.Name] = e.Href
		}
	}
	var out []ddOrder
	year, lastMonth := now.Year(), now.Month()
	store, total, prev := "", "", ""
	var day time.Time
	cancelled := false
	for _, line := range strings.Split(s.Text, "\n") {
		switch {
		case uePrice.MatchString(line):
			store, total = prev, uePrice.FindStringSubmatch(line)[1]
			day = time.Time{}
		case ueWhen.MatchString(line):
			m := ueWhen.FindStringSubmatch(line)
			m = append([]string{m[0]}, m[2:]...)
			cancelled = strings.HasPrefix(line, "Canceled")
			t, err := time.Parse("Jan 2 3:04 PM", m[1]+" "+m[2]+" "+m[4])
			if err != nil {
				break
			}
			if m[3] != "" {
				year, _ = strconv.Atoi(m[3])
			} else if t.Month() > lastMonth { // going back in time past a New Year
				year--
			}
			lastMonth = t.Month()
			day = time.Date(year, t.Month(), t.Day(), t.Hour(), t.Minute(), 0, 0, time.Local)
		case line == "View receipt":
			if len(out) < len(links) && !day.IsZero() {
				out = append(out, ddOrder{Day: day, Store: store, Total: total, URL: links[len(out)], Shop: shopURLs[store], Cancelled: cancelled})
			}
		}
		prev = line
	}
	if len(out) != len(links) {
		return nil, fmt.Errorf("the Uber Eats order list has changed its layout (%d receipt links, %d orders read)", len(links), len(out))
	}
	return out, nil
}

// ueDismiss closes the cookie notice that covers the page, choosing "Opt out".
func ueDismiss(ctx context.Context, p *page.Page) {
	for i := 0; i < 3; i++ {
		s, err := p.Snapshot(ctx)
		if err != nil || !s.Dialog {
			return
		}
		b, ok := named(s, "button", "Opt out")
		if !ok {
			return
		}
		_ = p.Click(ctx, b.Ref)
	}
}

// ueList opens the past orders and presses "Show more" until it has every
// order back to the start of year since (0: everything).
func (a *app) ueList(ctx context.Context, since int) ([]ddOrder, bool, int, error) {
	if ok, code, err := a.signedIn(ctx, ubereatsURL(), "Uber Eats"); !ok {
		return nil, false, code, err
	}
	var orders []ddOrder
	code, err := a.withPage(func(p *page.Page) (int, error) {
		for deadline := time.Now().Add(25 * time.Second); time.Now().Before(deadline) && ctx.Err() == nil; time.Sleep(time.Second) {
			ueDismiss(ctx, p)
			if s, err := p.Snapshot(ctx); err == nil && (strings.Contains(s.Text, "View receipt") || strings.Contains(s.Text, "Past Orders")) {
				break
			}
		}
		same := 0
		for round := 0; round < 300 && ctx.Err() == nil; round++ {
			s, err := p.Snapshot(ctx)
			if err != nil {
				return exitError, err
			}
			got, err := ueParse(s, time.Now())
			if err != nil {
				return exitError, err
			}
			if len(got) == len(orders) {
				if same++; same >= 3 {
					break
				}
			} else {
				same = 0
			}
			orders = got
			if n := len(orders); n > 0 && since > 0 && orders[n-1].Day.Year() < since {
				break
			}
			session.Touch()
			// The last "Show more" on the page loads older orders; the others unfold an order's items.
			more := 0
			for _, e := range s.Elements {
				if e.Kind == "button" && e.Name == "Show more" {
					more = e.Ref
				}
			}
			if more == 0 {
				if err := p.Scroll(ctx, "bottom"); err != nil {
					return exitError, err
				}
			} else if err := p.Click(ctx, more); err != nil {
				return exitError, err
			}
			time.Sleep(700 * time.Millisecond)
		}
		return exitOK, nil
	})
	return orders, err == nil, code, err
}

// ueFetch opens an order's receipt and returns its time, its text and the
// receipt document as a PDF. Receipts more than three days old are kept.
func ueFetch(ctx context.Context, p *page.Page, o ddOrder) (time.Time, string, []byte, error) {
	id := "order"
	if m := regexp.MustCompile(`modctx=([A-Za-z0-9-]+)`).FindStringSubmatch(o.URL); m != nil {
		id = m[1]
	}
	base := filepath.Join(session.Dir(), "ubereats-orders", id)
	var c struct {
		When time.Time `json:"when"`
		Text string    `json:"text"`
	}
	if data, err := os.ReadFile(base + ".json"); err == nil && json.Unmarshal(data, &c) == nil && !c.When.IsZero() {
		if pdf, err := os.ReadFile(base + ".pdf"); err == nil {
			return c.When, c.Text, pdf, nil
		}
	}
	if err := p.Goto(ctx, o.URL); err != nil {
		return time.Time{}, "", nil, err
	}
	text := ""
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(time.Second) {
		ueDismiss(ctx, p)
		s, err := p.Snapshot(ctx)
		if err != nil {
			return time.Time{}, "", nil, err
		}
		if s.Dialog && strings.Contains(s.Text, "Total") && strings.Contains(s.Text, "Payments") {
			text = s.Text
			break
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return time.Time{}, "", nil, errors.New("the receipt did not open")
		}
	}
	// The receipt's own date has the year; the list has the time of the order.
	when := o.Day
	if m := ueDate.FindStringSubmatch(text); m != nil {
		if d, err := time.Parse("Jan 2, 2006", m[1]); err == nil {
			when = time.Date(d.Year(), d.Month(), d.Day(), o.Day.Hour(), o.Day.Minute(), 0, 0, time.Local)
		}
	}
	// Leave out the pop-up's own buttons.
	for _, drop := range []string{"Receipt\n", "Print\n", "Download PDF\n", "Resend by email\n", "Get Help\n"} {
		text = strings.Replace(text, drop, "", 1)
	}
	if ok, err := p.OnlyFrame(ctx); err != nil || !ok {
		return when, text, nil, errors.New("the receipt could not be printed")
	}
	pdf, err := p.PDF(ctx)
	if err == nil && time.Since(when) > 72*time.Hour && os.MkdirAll(filepath.Dir(base), 0o700) == nil {
		c.When, c.Text = when, text
		data, _ := json.Marshal(c)
		if os.WriteFile(base+".pdf", pdf, 0o600) == nil {
			_ = os.WriteFile(base+".json", data, 0o600)
		}
	}
	return when, strings.TrimSpace(text), pdf, err
}

// ueSpend reads a receipt: "Total" and its amount, the price lines, and
// under "Payments" what was paid with Uber Cash and what with a card.
func ueSpend(o ddOrder, when time.Time, text string) (spend, bool) {
	lines := strings.Split(text, "\n")
	sp := spend{When: when, Store: o.Store, URL: o.URL}
	total, found, pay := 0.0, false, false
	for i, line := range lines {
		next := ""
		if i+1 < len(lines) {
			next = lines[i+1]
		}
		switch {
		case line == "Payments":
			pay = true
		case line == "Order Details" || line == "Uber Delivery":
			pay = false
		case !money.MatchString(next):
		case line == "Total" && !found:
			total, found = dollars(next), true
		case line == "Item Subtotal" || line == "Subtotal":
			sp.Subtotal = dollars(next)
		case pay && strings.Contains(line, "Uber Cash"):
			sp.Credits += dollars(next)
		}
	}
	if !found {
		return spend{}, false
	}
	if sp.Credits > total {
		sp.Credits = total
	}
	sp.Charged = total - sp.Credits
	return sp, true
}

// ueStore sorts a store by its name; Uber Eats gives no surer sign on the
// pages read here.
func ueStore(_ context.Context, _ *page.Page, known map[string]storeInfo, name, _ string) storeInfo {
	if in, ok := known[name]; ok {
		return in
	}
	in := storeInfo{Category: catRestaurant}
	switch low := strings.ToLower(name); {
	case strings.Contains(low, "gift card"):
		in.Category = catGift
	case groceryName.MatchString(name) || regexp.MustCompile(`(?i)\b(safeway|costco|target|cvs|walgreens|lucky|sprouts|whole foods|trader joe|7-eleven|smart & final|grocery outlet)\b`).MatchString(low):
		in.Category = catGrocery
	}
	known[name] = in
	return in
}

func (a *app) ubereatsReceipts(ctx context.Context, args []string) (int, error) {
	return a.shopReceipts(ctx, shops["ubereats"], args)
}

func (a *app) ubereatsReceipt(ctx context.Context, args []string) (int, error) {
	return a.shopReceipt(ctx, shops["ubereats"], args)
}

func (a *app) ubereatsYearPDF(ctx context.Context, args []string) (int, error) {
	return a.yearPDF(ctx, "ubereats", args)
}

func (a *app) ubereatsSpending(ctx context.Context, args []string) (int, error) {
	return a.shopSpending(ctx, shops["ubereats"], args)
}
