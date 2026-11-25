// Package receipt defines the one record every store connector produces and
// every other package consumes. A connector's only job is to turn what a shop
// returns into []Receipt; store, search and render know nothing about shops.
package receipt

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"time"
)

// Cents is an amount of money in hundredths. Negative means money back
// (a discount line, a return).
type Cents int64

// FromFloat converts a decimal amount such as 11.99 to Cents, rounding half away from zero.
func FromFloat(f float64) Cents { return Cents(math.Round(f * 100)) }

// String formats like $11.99 or -$4.00.
func (c Cents) String() string {
	sign := ""
	if c < 0 {
		sign, c = "-", -c
	}
	return fmt.Sprintf("%s$%d.%02d", sign, c/100, c%100)
}

// Kinds of receipt.
const (
	KindWarehouse = "warehouse" // bought in a physical shop
	KindGas       = "gas"
	KindOnline    = "online"
)

// Receipt is one purchase (or return) at one shop.
type Receipt struct {
	Store string `json:"store"` // connector name, e.g. "costco"
	ID    string `json:"id"`    // unique within Store; letters, digits, '-' and '_' only
	Kind  string `json:"kind"`
	// When is the wall-clock time printed on the receipt. It is kept in UTC
	// with no conversion, because shops print local time without a zone.
	When     time.Time `json:"when"`
	Location string    `json:"location,omitempty"` // shop name, e.g. "TESTVILLE #999"
	Address  string    `json:"address,omitempty"`
	Lines    []Line    `json:"lines"`
	Subtotal Cents     `json:"subtotal"`
	Tax      Cents     `json:"tax"`
	Total    Cents     `json:"total"`
	Tenders  []Tender  `json:"tenders,omitempty"`
	// Raw is the shop's own record, untouched, so a better parser can be run
	// later without fetching again.
	Raw json.RawMessage `json:"raw,omitempty"`
}

// Line is one printed row of a receipt.
type Line struct {
	Item   string  `json:"item,omitempty"`   // the shop's item number
	Name   string  `json:"name"`             // as printed, usually abbreviated
	Detail string  `json:"detail,omitempty"` // second description line, if any
	Qty    float64 `json:"qty,omitempty"`
	Amount Cents   `json:"amount"`
	// Priced is false when the shop did not report an amount for this line
	// (online order lists); Amount is then meaningless.
	Priced  bool   `json:"priced"`
	TaxFlag string `json:"tax_flag,omitempty"`
	// DiscountFor is set on a discount row and names the item it reduces.
	DiscountFor string `json:"discount_for,omitempty"`
}

// Tender is one payment on a receipt.
type Tender struct {
	Name   string `json:"name"`
	Amount Cents  `json:"amount"`
}

// IsReturn reports whether the receipt as a whole gave money back.
func (r Receipt) IsReturn() bool { return r.Total < 0 }

// Discount returns the total of the discount rows for item (zero or negative).
func (r Receipt) Discount(item string) Cents {
	var d Cents
	if item == "" {
		return 0
	}
	for _, l := range r.Lines {
		if l.DiscountFor == item {
			d += l.Amount
		}
	}
	return d
}

var idOK = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)

// Validate reports the first way r breaks the contract.
func (r Receipt) Validate() error {
	switch {
	case !idOK.MatchString(r.Store):
		return fmt.Errorf("receipt: bad store %q", r.Store)
	case !idOK.MatchString(r.ID):
		return fmt.Errorf("receipt: bad id %q", r.ID)
	case r.Kind != KindWarehouse && r.Kind != KindGas && r.Kind != KindOnline:
		return fmt.Errorf("receipt %s: bad kind %q", r.ID, r.Kind)
	case r.When.IsZero():
		return errors.New("receipt " + r.ID + ": no date")
	}
	return nil
}
