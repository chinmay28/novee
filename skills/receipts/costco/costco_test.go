package costco

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/chinmay28/novee/skills/receipts/receipt"
)

func data(t *testing.T, file, key string) json.RawMessage {
	t.Helper()
	b, err := os.ReadFile("testdata/" + file)
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(b, &env); err != nil {
		t.Fatal(err)
	}
	return env.Data[key]
}

func TestParseWarehouse(t *testing.T) {
	rs, err := ParseWarehouse(data(t, "warehouse.json", "receiptsWithCounts"))
	if err == nil || !strings.Contains(err.Error(), "no usable date") {
		t.Errorf("the record with a bad date must be reported, got %v", err)
	}
	if len(rs) != 3 {
		t.Fatalf("got %d receipts, want the 3 usable ones", len(rs))
	}
	r := rs[0]
	if r.ID != "21099901102482503141503" || r.Kind != receipt.KindWarehouse || r.Location != "TESTVILLE #999" {
		t.Errorf("header: %+v", r)
	}
	if got := r.When.Format("2006-01-02 15:04"); got != "2025-03-14 15:03" {
		t.Errorf("when = %s", got)
	}
	if r.Total != 4906 || r.Tax != 110 || r.Subtotal != 4796 {
		t.Errorf("totals: %v %v %v", r.Subtotal, r.Tax, r.Total)
	}
	if l := r.Lines[0]; l.Item != "1234567" || l.Name != "CMND PICT STRP" || l.Amount != 1599 || !l.Priced || l.DiscountFor != "" {
		t.Errorf("line 0: %+v", l)
	}
	if l := r.Lines[1]; l.DiscountFor != "1234567" || l.Amount != -400 {
		t.Errorf("coupon row: %+v", l)
	}
	if r.Discount("1234567") != -400 {
		t.Errorf("discount = %v", r.Discount("1234567"))
	}
	if l := r.Lines[2]; l.Qty != 2 || l.Detail != "FIVE CHEESE" {
		t.Errorf("line 2: %+v", l)
	}
	if l := r.Lines[3]; l.Amount != 1399 {
		t.Errorf("amount given as a string: %+v", l)
	}
	if len(r.Tenders) != 1 || r.Tenders[0].Amount != 4906 {
		t.Errorf("tenders: %+v", r.Tenders)
	}
	if !json.Valid(r.Raw) || !strings.Contains(string(r.Raw), "itemIdentifier") {
		t.Error("raw record must be kept untouched")
	}
	if !rs[1].IsReturn() {
		t.Error("negative total must be a return")
	}
	if rs[2].Kind != receipt.KindGas || rs[2].ID != "999-51-77-20250403T0800" {
		t.Errorf("gas receipt without a barcode: kind %q id %q", rs[2].Kind, rs[2].ID)
	}
	for _, r := range rs {
		if err := r.Validate(); err != nil {
			t.Error(err)
		}
	}
}

func TestParseOnline(t *testing.T) {
	rs, total, err := ParseOnline(data(t, "online.json", "getOnlineOrders"))
	if err != nil || total != 1 || len(rs) != 1 {
		t.Fatalf("rs=%d total=%d err=%v", len(rs), total, err)
	}
	r := rs[0]
	if r.ID != "online-1234567890" || r.Kind != receipt.KindOnline || r.Total != 78797 {
		t.Errorf("%+v", r)
	}
	if l := r.Lines[0]; l.Priced || l.Item != "1611111" {
		t.Errorf("online lines carry no price: %+v", l)
	}
	if err := r.Validate(); err != nil {
		t.Error(err)
	}
}

func TestQueryDrop(t *testing.T) {
	q := warehouseFields()
	if !strings.Contains(q.text(), "itemArray { itemNumber") || !strings.HasPrefix(q.text(), "query receiptsWithCounts(") {
		t.Fatalf("query text: %s", q.text())
	}
	if strings.Count(q.text(), "{") != strings.Count(q.text(), "}") {
		t.Fatalf("unbalanced: %s", q.text())
	}
	if !q.drop("instantSavings") || strings.Contains(q.text(), "instantSavings") {
		t.Error("record field not dropped")
	}
	if !q.drop("taxFlag") || strings.Contains(q.text(), "taxFlag") {
		t.Error("nested field not dropped")
	}
	if !q.drop("tenderArray") || strings.Contains(q.text(), "tenderArray") {
		t.Error("whole nested selection not dropped")
	}
	if q.drop("nonsense") {
		t.Error("dropping an unknown field must report false")
	}
	o := onlineFields()
	if !o.drop("orderedDate") || strings.Contains(o.text(), "orderPlacedDate") {
		t.Error("aliased field must be dropped by its real name")
	}
}
