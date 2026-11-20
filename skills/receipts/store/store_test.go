package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chinmay28/novee/skills/receipts/receipt"
)

func rec(store, id, date string) receipt.Receipt {
	t, _ := time.Parse("2006-01-02 15:04", date)
	return receipt.Receipt{Store: store, ID: id, Kind: receipt.KindWarehouse, When: t,
		Lines: []receipt.Line{{Name: "X", Amount: 100, Priced: true}}, Total: 100}
}

func TestPutAllRoundTrip(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b, a, other := rec("costco", "b", "2025-03-14 15:03"), rec("costco", "a", "2023-12-02 09:00"), rec("amazon", "z", "2024-06-01 00:00")
	for _, r := range []receipt.Receipt{b, a, other} {
		if created, err := s.Put(r); err != nil || !created {
			t.Fatalf("Put(%s) = %v, %v", r.ID, created, err)
		}
	}
	b.Total = 200
	if created, err := s.Put(b); err != nil || created {
		t.Fatalf("second Put must replace, got created=%v err=%v", created, err)
	}
	if err := s.PutPDF(b, []byte("%PDF")); err != nil || !s.HasPDF(b) || s.HasPDF(a) {
		t.Fatalf("pdf: %v", err)
	}
	if _, err := s.SetState("costco", SyncState{At: time.Now(), OK: true}); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(s.Root(), "exports"), 0o700)
	os.WriteFile(filepath.Join(s.Root(), "exports", "x.json"), []byte("not a receipt"), 0o600)

	all, err := s.All(Filter{})
	if err != nil || len(all) != 3 || all[0].ID != "a" || all[2].ID != "b" || all[2].Total != 200 {
		t.Fatalf("All = %+v, %v (want a, z, b oldest first)", all, err)
	}
	day := func(d string) time.Time { t, _ := time.Parse("2006-01-02", d); return t }
	for _, c := range []struct {
		f    Filter
		want int
	}{
		{Filter{Store: "costco"}, 2},
		{Filter{From: day("2025-03-14")}, 1}, // same day, later hour: inclusive
		{Filter{To: day("2023-12-02")}, 1},
		{Filter{From: day("2024-01-01"), To: day("2024-12-31")}, 1},
		{Filter{Store: "nobody"}, 0},
	} {
		if got, _ := s.All(c.f); len(got) != c.want {
			t.Errorf("All(%+v) = %d, want %d", c.f, len(got), c.want)
		}
	}
	if st, _ := os.Stat(filepath.Join(s.Root(), "costco", "2025", "2025-03-14_b.json")); st == nil || st.Mode().Perm() != 0o600 {
		t.Error("record must be at <store>/<year>/<date>_<id>.json, readable by the owner only")
	}
}

func TestBadRecords(t *testing.T) {
	s, _ := Open(t.TempDir())
	bad := rec("costco", "../escape", "2025-01-01 00:00")
	if _, err := s.Put(bad); err == nil {
		t.Error("an id that is not filename-safe must be refused")
	}
	os.MkdirAll(filepath.Join(s.Root(), "costco", "2025"), 0o700)
	os.WriteFile(filepath.Join(s.Root(), "costco", "2025", "junk.json"), []byte("{"), 0o600)
	if _, err := s.All(Filter{}); err == nil || !strings.Contains(err.Error(), "junk.json") {
		t.Errorf("an unreadable record must be an error naming the file, got %v", err)
	}
}

func TestState(t *testing.T) {
	s, _ := Open(t.TempDir())
	t1 := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	s.SetState("costco", SyncState{At: t1, OK: true, Message: "fine"})
	prev, err := s.SetState("costco", SyncState{At: t1.Add(time.Hour), OK: false, Message: "expired"})
	if err != nil || !prev.OK || prev.Message != "fine" {
		t.Fatalf("prev = %+v, %v", prev, err)
	}
	m, _ := s.State()
	if st := m["costco"]; st.OK || !st.LastOK.Equal(t1) {
		t.Errorf("a failed sync must keep the time of the last good one: %+v", st)
	}
}
