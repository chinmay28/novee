package costco

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/chinmay28/novee/skills/receipts/receipt"
)

// num accepts a JSON number, a numeric string or null.
type num float64

func (n *num) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `" `)
	if s == "" || s == "null" {
		return nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fmt.Errorf("not a number: %s", b)
	}
	*n = num(f)
	return nil
}

// str accepts a JSON string, number or null.
type str string

func (s *str) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		return nil
	}
	if b[0] == '"' {
		var v string
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		*s = str(strings.TrimSpace(v))
		return nil
	}
	*s = str(b)
	return nil
}

type whReceipt struct {
	WarehouseName       str `json:"warehouseName"`
	ReceiptType         str `json:"receiptType"`
	DocumentType        str `json:"documentType"`
	TransactionDateTime str `json:"transactionDateTime"`
	TransactionDate     str `json:"transactionDate"`
	WarehouseNumber     str `json:"warehouseNumber"`
	RegisterNumber      str `json:"registerNumber"`
	TransactionNumber   str `json:"transactionNumber"`
	TransactionBarcode  str `json:"transactionBarcode"`
	Address1            str `json:"warehouseAddress1"`
	City                str `json:"warehouseCity"`
	State               str `json:"warehouseState"`
	PostalCode          str `json:"warehousePostalCode"`
	SubTotal            num `json:"subTotal"`
	Taxes               num `json:"taxes"`
	Total               num `json:"total"`
	Items               []struct {
		ItemNumber str  `json:"itemNumber"`
		Desc1      str  `json:"itemDescription01"`
		Desc2      str  `json:"itemDescription02"`
		Unit       num  `json:"unit"`
		Amount     *num `json:"amount"`
		TaxFlag    str  `json:"taxFlag"`
	} `json:"itemArray"`
	Tenders []struct {
		TypeName    str `json:"tenderTypeName"`
		Description str `json:"tenderDescription"`
		Amount      num `json:"amountTender"`
	} `json:"tenderArray"`
}

var (
	notIDChar   = regexp.MustCompile(`[^A-Za-z0-9_-]+`)
	discountRow = regexp.MustCompile(`^/\s*(\d+)`)
	timeLayouts = []string{"2006-01-02T15:04:05", time.RFC3339, "2006-01-02T15:04", "2006-01-02 15:04:05", "2006-01-02", "01/02/2006", "1/2/2006"}
)

func parseTime(candidates ...str) (time.Time, bool) {
	for _, c := range candidates {
		for _, layout := range timeLayouts {
			if t, err := time.Parse(layout, string(c)); err == nil {
				// keep the printed wall-clock time, drop any zone
				return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), 0, time.UTC), true
			}
		}
	}
	return time.Time{}, false
}

// ParseWarehouse turns the value of data.receiptsWithCounts into receipts.
// Records it cannot use are reported in the error; the rest are returned.
func ParseWarehouse(data json.RawMessage) ([]receipt.Receipt, error) {
	var top struct {
		Receipts []json.RawMessage `json:"receipts"`
	}
	if err := json.Unmarshal(data, &top); err != nil {
		return nil, fmt.Errorf("costco: receiptsWithCounts: %w", err)
	}
	var out []receipt.Receipt
	var errs []error
	for i, raw := range top.Receipts {
		var w whReceipt
		if err := json.Unmarshal(raw, &w); err != nil {
			errs = append(errs, fmt.Errorf("costco: receipt %d: %w", i, err))
			continue
		}
		when, ok := parseTime(w.TransactionDateTime, w.TransactionDate)
		if !ok {
			errs = append(errs, fmt.Errorf("costco: receipt %d: no usable date (%q)", i, w.TransactionDateTime))
			continue
		}
		id := notIDChar.ReplaceAllString(string(w.TransactionBarcode), "")
		if id == "" {
			id = notIDChar.ReplaceAllString(fmt.Sprintf("%s-%s-%s-%s", w.WarehouseNumber, w.RegisterNumber,
				w.TransactionNumber, when.Format("20060102T1504")), "")
		}
		kind := receipt.KindWarehouse
		if t := strings.ToLower(string(w.ReceiptType + " " + w.DocumentType)); strings.Contains(t, "gas") || strings.Contains(t, "fuel") {
			kind = receipt.KindGas
		}
		r := receipt.Receipt{
			Store: "costco", ID: id, Kind: kind, When: when,
			Location: strings.TrimSpace(fmt.Sprintf("%s #%s", w.WarehouseName, w.WarehouseNumber)),
			Address:  strings.Trim(fmt.Sprintf("%s, %s, %s %s", w.Address1, w.City, w.State, w.PostalCode), ", "),
			Subtotal: receipt.FromFloat(float64(w.SubTotal)), Tax: receipt.FromFloat(float64(w.Taxes)),
			Total: receipt.FromFloat(float64(w.Total)), Raw: raw,
		}
		for _, it := range w.Items {
			l := receipt.Line{Item: string(it.ItemNumber), Name: string(it.Desc1), Detail: string(it.Desc2),
				Qty: float64(it.Unit), TaxFlag: string(it.TaxFlag)}
			if it.Amount != nil {
				l.Amount, l.Priced = receipt.FromFloat(float64(*it.Amount)), true
			}
			// A coupon prints as a row named "/1370476" with a negative amount.
			if m := discountRow.FindStringSubmatch(l.Name); m != nil && l.Amount < 0 {
				l.DiscountFor = m[1]
			}
			r.Lines = append(r.Lines, l)
		}
		for _, t := range w.Tenders {
			name := string(t.Description)
			if name == "" {
				name = string(t.TypeName)
			}
			r.Tenders = append(r.Tenders, receipt.Tender{Name: name, Amount: receipt.FromFloat(float64(t.Amount))})
		}
		out = append(out, r)
	}
	return out, errors.Join(errs...)
}

// ParseOnline turns the value of data.getOnlineOrders into receipts and
// reports how many orders the whole date range has. Costco's order list
// carries the order total but no price per line.
func ParseOnline(data json.RawMessage) (rs []receipt.Receipt, total int, err error) {
	var top struct {
		Total  num               `json:"totalNumberOfRecords"`
		Orders []json.RawMessage `json:"bcOrders"`
	}
	if err := json.Unmarshal(data, &top); err != nil {
		return nil, 0, fmt.Errorf("costco: getOnlineOrders: %w", err)
	}
	var errs []error
	for i, raw := range top.Orders {
		var o struct {
			Placed str `json:"orderPlacedDate"`
			Number str `json:"orderNumber"`
			Header str `json:"orderHeaderId"`
			Total  num `json:"orderTotal"`
			Lines  []struct {
				ItemNumber str `json:"itemNumber"`
				Desc       str `json:"itemDescription"`
			} `json:"orderLineItems"`
		}
		if err := json.Unmarshal(raw, &o); err != nil {
			errs = append(errs, fmt.Errorf("costco: online order %d: %w", i, err))
			continue
		}
		when, ok := parseTime(o.Placed)
		id := notIDChar.ReplaceAllString(string(o.Number), "")
		if id == "" {
			id = notIDChar.ReplaceAllString(string(o.Header), "")
		}
		if !ok || id == "" {
			errs = append(errs, fmt.Errorf("costco: online order %d: no usable date or number (%q, %q)", i, o.Placed, o.Number))
			continue
		}
		r := receipt.Receipt{Store: "costco", ID: "online-" + id, Kind: receipt.KindOnline, When: when,
			Location: "costco.com", Total: receipt.FromFloat(float64(o.Total)), Raw: raw}
		for _, l := range o.Lines {
			r.Lines = append(r.Lines, receipt.Line{Item: string(l.ItemNumber), Name: string(l.Desc)})
		}
		rs = append(rs, r)
	}
	return rs, int(top.Total), errors.Join(errs...)
}
