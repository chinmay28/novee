package search

import (
	"testing"
	"time"

	"github.com/chinmay28/novee/skills/receipts/receipt"
)

func TestSimilar(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"command", "cmnd", true}, // abbreviation
		{"strips", "strp", true},  // abbreviation
		{"picture", "pict", true}, // prefix
		{"hanging", "hang", true}, // prefix
		{"tortellini", "tortellon", true},
		{"organic", "org", true},
		{"spinach", "spinich", true}, // typo
		{"3m", "3m", true},
		{"3m", "3mm", false},         // short codes: exact only
		{"1370476", "137047", false}, // numbers: exact only
		{"command", "coconut", false},
		{"hook", "hang", false},
		{"milk", "mlk", true},
		{"water", "wire", false},
		{"cat", "command", false},
	} {
		if got := Similar(c.a, c.b) >= matchAt; got != c.want {
			t.Errorf("Similar(%q, %q) matched = %v, want %v", c.a, c.b, got, c.want)
		}
		if Similar(c.a, c.b) != Similar(c.b, c.a) {
			t.Errorf("Similar(%q, %q) is not symmetric", c.a, c.b)
		}
	}
}

func line(item, name string, cents receipt.Cents) receipt.Line {
	return receipt.Line{Item: item, Name: name, Amount: cents, Priced: true, Qty: 1}
}

func fixture() []receipt.Receipt {
	d := func(s string) time.Time { t, _ := time.Parse("2006-01-02", s); return t }
	return []receipt.Receipt{
		{Store: "costco", ID: "a", When: d("2024-01-05"), Lines: []receipt.Line{
			line("1", "ORG SPINACH", 449), line("2", "COCONUT WATER", 1359), line("3", "A2 MILK", 1369)}},
		{Store: "costco", ID: "b", When: d("2025-03-14"), Lines: []receipt.Line{
			line("1370476", "CMND PICT STRP", 1599),
			{Item: "9", Name: "/1370476", Amount: -400, Priced: true, DiscountFor: "1370476"},
			{Item: "4", Name: "KS TORTELLON", Amount: 2198, Priced: true, Qty: 2}}},
		{Store: "costco", ID: "c", When: d("2026-10-10"), Kind: receipt.KindOnline, Lines: []receipt.Line{
			{Item: "5", Name: "Command Large Wire Hooks"}}},
	}
}

func ids(hs []Hit) (out []string) {
	for _, h := range hs {
		out = append(out, h.Receipt.ID+":"+h.Line.Item)
	}
	return
}

func eq(a []string, b ...string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestFind(t *testing.T) {
	c := func(v receipt.Cents) *receipt.Cents { return &v }
	for _, tc := range []struct {
		name          string
		q             Query
		full, partial []string
	}{
		{"abbreviated name, all words", Query{Text: "command picture strips"}, []string{"b:1370476"}, []string{"c:5"}},
		{"extra words fall back to partial, closest spelling first", Query{Text: "3m command hanging assortment"}, nil, []string{"c:5", "b:1370476"}},
		{"item number as a word", Query{Text: "1370476"}, []string{"b:1370476"}, nil},
		{"item filter", Query{Item: "1370476"}, []string{"b:1370476"}, nil},
		{"price band only", Query{Min: c(1300), Max: c(1400)}, []string{"a:2", "a:3"}, nil},
		{"price is per unit", Query{Text: "tortellini", Min: c(1000), Max: c(1200)}, []string{"b:4"}, nil},
		{"unpriced lines never pass a price filter", Query{Text: "command", Min: c(1), Max: c(99999)}, []string{"b:1370476"}, nil},
		{"nothing", Query{Text: "umbrella"}, nil, nil},
	} {
		res := Find(fixture(), tc.q)
		if !eq(ids(res.Full), tc.full...) || !eq(ids(res.Partial), tc.partial...) {
			t.Errorf("%s: full %v partial %v, want %v %v", tc.name, ids(res.Full), ids(res.Partial), tc.full, tc.partial)
		}
	}
	if res := Find(fixture(), Query{Text: "x"}); res.Lines != 6 {
		t.Errorf("lines looked at = %d, want 6 (coupon rows are not counted)", res.Lines)
	}
}
