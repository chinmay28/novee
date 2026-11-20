// Package costco is the Costco connector.
//
// Costco has no public API. The "Orders & Purchases" page of costco.com gets
// its data from a GraphQL endpoint, authorised by a token the page keeps in
// localStorage after sign-in. This connector asks that endpoint the same
// questions from inside the signed-in page. The query shapes come from public
// community scripts, not from Costco, so the endpoint may change: when it
// rejects a field we asked for, the field is dropped and the query retried.
package costco

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/chinmay28/novee/skills/receipts/receipt"
	"github.com/chinmay28/novee/skills/receipts/source"
)

const (
	defaultEndpoint = "https://ecom-api.costco.com/ebusiness/order/v1/orders/graphql"
	defaultStart    = "https://www.costco.com/OrderStatusCmd"
	clientID        = "481b1aec-aa3b-454b-b81b-48187e28f205" // the web app's own identifier
)

// Costco implements source.Source. The zero value talks to costco.com.
type Costco struct {
	Endpoint  string           // GraphQL URL; empty means Costco's
	Start     string           // account page; empty means Costco's
	Warehouse string           // home warehouse number for online order lookups; empty means "847"
	Now       func() time.Time // empty means time.Now
}

var _ source.Source = (*Costco)(nil)

func (c *Costco) Name() string           { return "costco" }
func (c *Costco) History() time.Duration { return 3 * 366 * 24 * time.Hour }

func (c *Costco) StartURL() string {
	if c.Start != "" {
		return c.Start
	}
	return defaultStart
}

func (c *Costco) endpoint() string {
	if c.Endpoint != "" {
		return c.Endpoint
	}
	return defaultEndpoint
}

func (c *Costco) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// tokenExpiry returns the expiry (Unix seconds) of the page's id token, or 0.
const tokenExpiry = `(() => { try {
  const t = localStorage.getItem('idToken'); if (!t) return 0;
  const p = JSON.parse(atob(t.split('.')[1].replace(/-/g, '+').replace(/_/g, '/')));
  return p.exp || 0;
} catch (e) { return 0; } })()`

// SignedIn is true while the page holds an id token that has not expired.
func (c *Costco) SignedIn(ctx context.Context, p source.Page) (bool, error) {
	var exp float64
	if err := p.Eval(ctx, tokenExpiry, &exp); err != nil {
		return false, err
	}
	return int64(exp) > c.now().Unix()+60, nil
}

// Fetch implements source.Source.
func (c *Costco) Fetch(ctx context.Context, p source.Page, from, to time.Time) ([]receipt.Receipt, error) {
	var out []receipt.Receipt
	var errs []error

	fields := warehouseFields()
	for a := from; !a.After(to); a = a.AddDate(0, 6, 0) {
		b := a.AddDate(0, 6, -1)
		if b.After(to) {
			b = to
		}
		vars := map[string]any{
			"startDate": a.Format("1/02/2006"), "endDate": b.Format("1/02/2006"),
			"documentType": "all", "documentSubType": "all",
		}
		data, err := c.ask(ctx, p, fields, vars)
		if errors.Is(err, source.ErrSignInNeeded) {
			return out, err
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("warehouse receipts %s to %s: %w", a.Format("2006-01-02"), b.Format("2006-01-02"), err))
			continue
		}
		rs, err := ParseWarehouse(data)
		out = append(out, rs...)
		if err != nil {
			errs = append(errs, err)
		}
	}

	wh := c.Warehouse
	if wh == "" {
		wh = "847"
	}
	ofields := onlineFields()
	for page := 1; page <= 200; page++ {
		vars := map[string]any{
			"startDate": from.Format("2006-1-02"), "endDate": to.Format("2006-1-02"),
			"pageNumber": page, "pageSize": 50, "warehouseNumber": wh,
		}
		data, err := c.ask(ctx, p, ofields, vars)
		if errors.Is(err, source.ErrSignInNeeded) {
			return out, err
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("online orders: %w", err))
			break
		}
		rs, total, err := ParseOnline(data)
		out = append(out, rs...)
		if err != nil {
			errs = append(errs, err)
		}
		if len(rs) == 0 || page*50 >= total {
			break
		}
	}
	return out, errors.Join(errs...)
}

// query is a GraphQL query whose field lists can shrink.
type query struct {
	name, args, call string
	pre, post        string   // around the record's selection
	record           []string // fields of the record
	subs             []sub    // nested selections of the record
}

type sub struct {
	name   string
	fields []string
}

func warehouseFields() *query {
	return &query{
		name: "receiptsWithCounts",
		args: "$startDate: String!, $endDate: String!, $documentType: String!, $documentSubType: String!",
		call: "startDate: $startDate, endDate: $endDate, documentType: $documentType, documentSubType: $documentSubType",
		pre:  "receipts {", post: "}",
		record: strings.Fields(`warehouseName receiptType documentType transactionDateTime transactionDate
			warehouseNumber warehouseShortName registerNumber transactionNumber operatorNumber transactionType
			transactionBarcode warehouseAddress1 warehouseAddress2 warehouseCity warehouseState warehousePostalCode
			totalItemCount subTotal taxes total instantSavings`),
		subs: []sub{
			{"itemArray", strings.Fields(`itemNumber itemDescription01 itemDescription02 itemIdentifier unit amount taxFlag itemUnitPriceAmount`)},
			{"tenderArray", strings.Fields(`tenderTypeName tenderDescription amountTender`)},
		},
	}
}

func onlineFields() *query {
	return &query{
		name: "getOnlineOrders",
		args: "$startDate: String!, $endDate: String!, $pageNumber: Int, $pageSize: Int, $warehouseNumber: String!",
		call: "startDate: $startDate, endDate: $endDate, pageNumber: $pageNumber, pageSize: $pageSize, warehouseNumber: $warehouseNumber",
		pre:  "pageNumber pageSize totalNumberOfRecords bcOrders {", post: "}",
		record: []string{"orderHeaderId", "orderPlacedDate: orderedDate", "orderNumber: sourceOrderNumber",
			"orderTotal", "warehouseNumber", "status"},
		subs: []sub{{"orderLineItems", strings.Fields(`itemNumber itemDescription lineNumber status`)}},
	}
}

func (q *query) text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "query %s(%s) { %s(%s) { %s %s", q.name, q.args, q.name, q.call, q.pre, strings.Join(q.record, " "))
	for _, s := range q.subs {
		if len(s.fields) > 0 {
			fmt.Fprintf(&b, " %s { %s }", s.name, strings.Join(s.fields, " "))
		}
	}
	fmt.Fprintf(&b, " %s } }", q.post)
	return b.String()
}

// drop removes a field by its real name and reports whether it was there.
func (q *query) drop(field string) bool {
	found := false
	keep := func(in []string) []string {
		var out []string
		for _, f := range in {
			real := f[strings.LastIndex(f, " ")+1:] // "alias: real" -> "real"
			if real == field {
				found = true
				continue
			}
			out = append(out, f)
		}
		return out
	}
	q.record = keep(q.record)
	for i := range q.subs {
		if q.subs[i].name == field {
			q.subs[i].fields, found = nil, true
			continue
		}
		q.subs[i].fields = keep(q.subs[i].fields)
	}
	return found
}

var unknownField = regexp.MustCompile(`(?i)(?:cannot query field|field) ["'\x60]?(\w+)["'\x60]?`)

type reply struct {
	Status int    `json:"status"`
	Body   string `json:"body"`
}

// ask runs q and returns the object under data.<name>. Fields the endpoint
// does not know are dropped from q (so later calls skip them) and the query
// is retried.
func (c *Costco) ask(ctx context.Context, p source.Page, q *query, vars map[string]any) (json.RawMessage, error) {
	for attempt := 0; attempt < 12; attempt++ {
		body, _ := json.Marshal(map[string]any{"query": q.text(), "variables": vars})
		js := fmt.Sprintf(`(async () => {
  const r = await fetch(%q, {method: 'POST', credentials: 'omit', headers: {
    'accept': '*/*', 'content-type': 'application/json-patch+json',
    'costco.env': 'ecom', 'costco.service': 'restOrders', 'client-identifier': %q,
    'costco-x-authorization': 'Bearer ' + localStorage.getItem('idToken'),
    'costco-x-wcs-clientid': localStorage.getItem('clientID') || ''}, body: %q});
  return {status: r.status, body: await r.text()};
})()`, c.endpoint(), clientID, string(body))
		var rep reply
		if err := p.Eval(ctx, js, &rep); err != nil {
			return nil, fmt.Errorf("request failed: %w", err)
		}
		if rep.Status == 401 || rep.Status == 403 {
			return nil, fmt.Errorf("%w (HTTP %d from Costco)", source.ErrSignInNeeded, rep.Status)
		}
		var env struct {
			Data   map[string]json.RawMessage `json:"data"`
			Errors []struct {
				Message string `json:"message"`
			} `json:"errors"`
		}
		if err := json.Unmarshal([]byte(rep.Body), &env); err != nil {
			return nil, fmt.Errorf("HTTP %d, not JSON: %s", rep.Status, clip(rep.Body))
		}
		if len(env.Errors) > 0 {
			dropped := false
			for _, e := range env.Errors {
				if m := unknownField.FindStringSubmatch(e.Message); m != nil && q.drop(m[1]) {
					dropped = true
				}
			}
			if dropped {
				continue
			}
			return nil, fmt.Errorf("Costco said: %s", clip(env.Errors[0].Message))
		}
		data, ok := env.Data[q.name]
		if !ok || string(data) == "null" {
			return nil, fmt.Errorf("HTTP %d, no %s in reply: %s", rep.Status, q.name, clip(rep.Body))
		}
		return data, nil
	}
	return nil, errors.New("Costco rejected too many fields; the query needs updating")
}

func clip(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}
