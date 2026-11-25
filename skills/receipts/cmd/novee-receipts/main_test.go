package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chinmay28/novee/skills/receipts/browser"
	"github.com/chinmay28/novee/skills/receipts/costco"
	"github.com/chinmay28/novee/skills/receipts/source"
	"github.com/chinmay28/novee/skills/receipts/store"
)

func newEnv(t *testing.T) (*env, *bytes.Buffer) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	out := &bytes.Buffer{}
	return &env{out: out, st: st, now: time.Now, wait: time.Second,
		sources: map[string]source.Source{"costco": &costco.Costco{}},
		open:    func(string, bool) (tab, error) { return nil, fmt.Errorf("no browser in this test") },
		notify:  func(string) {}}, out
}

func run(t *testing.T, e *env, out *bytes.Buffer, args ...string) (int, string) {
	t.Helper()
	out.Reset()
	code := e.run(context.Background(), args)
	return code, out.String()
}

func wants(t *testing.T, got string, subs ...string) {
	t.Helper()
	for _, s := range subs {
		if !strings.Contains(got, s) {
			t.Errorf("output lacks %q:\n%s", s, got)
		}
	}
}

func TestSearchAnswers(t *testing.T) {
	e, out := newEnv(t)
	if code, got := run(t, e, out, "search", "command"); code != exitOK || !strings.Contains(got, "CANNOT ANSWER") {
		t.Fatalf("an empty archive must not answer \"not found\": %d %s", code, got)
	}
	if code, got := run(t, e, out, "import", "costco", "../../costco/testdata/warehouse.json", "../../costco/testdata/online.json"); code != exitError {
		t.Fatalf("the fixture has one unusable record, so import must say so: %d %s", code, got)
	} else {
		wants(t, got, "Imported 4 receipts, 4 new", "no usable date")
	}

	// flags may come after the words, the way a person or a small model writes them
	_, got := run(t, e, out, "search", "3m", "command", "hanging", "strips", "--price", "10-20", "--store", "costco")
	wants(t, got, "Verdict: NOT FOUND", "Partial matches",
		"- 2025-03-14, TESTVILLE #999: item 1234567 CMND PICT STRP, $15.99, coupon -$4.00, paid $11.99 [receipt 21099901102482503141503]",
		"Words found on no receipt line: 3m, hanging.", "Not searched: purchases before 2025-03-14")

	_, got = run(t, e, out, "search", "command", "strips")
	wants(t, got, "Verdict: FOUND. 1 line(s) match \"command strips\" in 4 costco receipts dated 2025-03-14 to 2026-10-10")

	_, got = run(t, e, out, "search", "--item", "1068083")
	wants(t, got, "FOUND. 2 line(s)", "$13.99", "-$13.99 (RETURN)")

	_, got = run(t, e, out, "search", "tortellini")
	wants(t, got, "KS TORTELLON FIVE CHEESE, 2 x $10.99 = $21.98")

	_, got = run(t, e, out, "search", "mattress")
	wants(t, got, "line price not reported (order total $787.97)", "[receipt online-1234567890]")

	_, got = run(t, e, out, "search", "--price", "12.34")
	wants(t, got, "NOT FOUND. No line matches all of unit price $12.34")

	_, got = run(t, e, out, "search", "umbrella", "--year", "2019")
	wants(t, got, "CANNOT ANSWER")

	for _, bad := range [][]string{{"search"}, {"search", "x", "--price", "cheap"}, {"search", "x", "--from", "March"}, {"frobnicate"}, {"show", "nope"}} {
		if code, got := run(t, e, out, bad...); code != exitError || !strings.Contains(got, "ERROR:") {
			t.Errorf("%v: code %d, output %q", bad, code, got)
		}
	}

	_, got = run(t, e, out, "show", "online-1234567890")
	wants(t, got, "costco online receipt online-1234567890", "Example Hybrid Mattress, Queen: (no price reported)")

	_, got = run(t, e, out, "status")
	wants(t, got, "Verdict: 4 costco receipts dated 2025-03-14 to 2026-10-10 are stored", "- receipts: 4 (2 warehouse, 1 gas, 1 online), of which 1 are returns",
		"- net spend (returns subtracted): $863.04", "- PDFs: 0 of 4", "- last sync: never")
}

// fakeCostco behaves like the two things the connector touches: an account
// page that holds a token in localStorage when signed in, and the GraphQL
// endpoint, which here does not know the field instantSavings.
type fakeCostco struct {
	signedIn atomic.Bool
	rejected atomic.Int32
	asked    atomic.Int32
}

func (f *fakeCostco) handler(t *testing.T) http.Handler {
	fixture := func(file string) map[string]json.RawMessage {
		b, err := os.ReadFile("../../costco/testdata/" + file)
		if err != nil {
			t.Fatal(err)
		}
		var env struct {
			Data map[string]json.RawMessage `json:"data"`
		}
		json.Unmarshal(b, &env)
		return env.Data
	}
	var wh struct {
		Receipts []json.RawMessage `json:"receipts"`
	}
	json.Unmarshal(fixture("warehouse.json")["receiptsWithCounts"], &wh)
	wh.Receipts = wh.Receipts[:3] // without the deliberately broken record
	online := fixture("online.json")["getOnlineOrders"]

	mux := http.NewServeMux()
	mux.HandleFunc("/account", func(w http.ResponseWriter, r *http.Request) {
		if !f.signedIn.Load() {
			fmt.Fprint(w, `<h1>Sign In</h1><script>localStorage.clear()</script>`)
			return
		}
		claims := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"exp":%d}`, time.Now().Add(time.Hour).Unix())))
		fmt.Fprintf(w, `<h1>Orders</h1><script>localStorage.setItem('idToken','h.%s.s');localStorage.setItem('clientID','cid')</script>`, claims)
	})
	mux.HandleFunc("/graphql", func(w http.ResponseWriter, r *http.Request) {
		f.asked.Add(1)
		if !strings.HasPrefix(r.Header.Get("costco-x-authorization"), "Bearer h.") || r.Header.Get("costco-x-wcs-clientid") != "cid" {
			w.WriteHeader(401)
			return
		}
		var req struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &req); err != nil {
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(req.Query, "instantSavings"):
			f.rejected.Add(1)
			fmt.Fprint(w, `{"errors":[{"message":"Cannot query field \"instantSavings\" on type \"Receipt\"."}]}`)
		case strings.Contains(req.Query, "getOnlineOrders"):
			fmt.Fprintf(w, `{"data":{"getOnlineOrders":%s}}`, online)
		case strings.HasSuffix(fmt.Sprint(req.Variables["startDate"]), "/2024") && strings.HasPrefix(fmt.Sprint(req.Variables["startDate"]), "11/"):
			out, _ := json.Marshal(wh)
			fmt.Fprintf(w, `{"data":{"receiptsWithCounts":%s}}`, out)
		default:
			fmt.Fprint(w, `{"data":{"receiptsWithCounts":{"receipts":[]}}}`)
		}
	})
	return mux
}

// TestSyncEndToEnd drives a real browser against the fake shop.
func TestSyncEndToEnd(t *testing.T) {
	if _, err := browser.FindExec(); err != nil {
		t.Skip("no browser installed:", err)
	}
	fake := &fakeCostco{}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()

	e, out := newEnv(t)
	t.Setenv("NOVEE_BROWSER_DIR", t.TempDir())
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	e.now = func() time.Time { return now }
	e.sources["costco"] = &costco.Costco{Endpoint: srv.URL + "/graphql", Start: srv.URL + "/account"}
	e.open = func(profile string, headed bool) (tab, error) {
		return browser.Open(browser.Options{ProfileDir: profile, Headed: headed})
	}
	var notes []string
	e.notify = func(s string) { notes = append(notes, s) }

	// Signed out: say so, fetch nothing, tell the owner once.
	code, got := run(t, e, out, "sync", "costco", "--notify")
	if code != exitSignIn {
		t.Fatalf("code %d: %s", code, got)
	}
	wants(t, got, "Verdict: SIGN-IN NEEDED", "Nothing was fetched")
	run(t, e, out, "sync", "--notify")
	if len(notes) != 1 || !strings.Contains(notes[0], "SIGN-IN NEEDED") {
		t.Fatalf("the owner must be told once, not every run: %q", notes)
	}
	if fake.asked.Load() != 0 {
		t.Fatal("the endpoint must not be asked while signed out")
	}
	_, got = run(t, e, out, "status")
	wants(t, got, "FAILED: SIGN-IN NEEDED", "(last good sync: never)")

	// Signed in: fetch everything, make a PDF of each.
	fake.signedIn.Store(true)
	code, got = run(t, e, out, "sync", "--notify")
	if code != exitOK {
		t.Fatalf("code %d: %s", code, got)
	}
	wants(t, got, "Verdict: SYNC OK. Asked costco for 2023-11-", "to 2026-11-20: 4 receipts, 4 new, 4 PDFs made.")
	if fake.rejected.Load() != 1 {
		t.Errorf("a field the endpoint rejects must be dropped once and for all, was sent %d times", fake.rejected.Load())
	}
	pdfs, _ := filepath.Glob(filepath.Join(e.st.Root(), "costco", "*", "*.pdf"))
	if len(pdfs) != 4 {
		t.Fatalf("PDFs: %v", pdfs)
	}
	if data, _ := os.ReadFile(pdfs[0]); !bytes.HasPrefix(data, []byte("%PDF-")) || len(data) < 2000 {
		t.Errorf("%s is not a PDF (%d bytes)", pdfs[0], len(data))
	}
	_, got = run(t, e, out, "search", "command", "picture", "strips")
	wants(t, got, "Verdict: FOUND", "paid $11.99")

	// The next run asks only for the recent past and adds nothing.
	now = now.Add(7 * 24 * time.Hour)
	code, got = run(t, e, out, "sync")
	wants(t, got, "SYNC OK. Asked costco for 2026-10-21 to 2026-11-27: 1 receipts, 0 new, 0 PDFs made.")
	if code != exitOK || len(notes) != 1 {
		t.Errorf("code %d notes %q", code, notes)
	}

	// One PDF of a range.
	code, got = run(t, e, out, "pdf", "--year", "2025")
	wants(t, got, "Verdict: one PDF with 3 costco receipts dated 2025-03-14 to 2025-04-03")
	path := strings.TrimSpace(got[strings.LastIndex(got, "File: ")+6:])
	if data, err := os.ReadFile(path); code != exitOK || err != nil || !bytes.HasPrefix(data, []byte("%PDF-")) {
		t.Errorf("merged PDF %q: code %d err %v", path, code, err)
	}

	// The session expires: stored receipts stay searchable, the owner hears about it.
	fake.signedIn.Store(false)
	if code, _ = run(t, e, out, "sync", "--notify"); code != exitSignIn || len(notes) != 2 {
		t.Errorf("code %d notes %q", code, notes)
	}
	_, got = run(t, e, out, "status")
	wants(t, got, "FAILED: SIGN-IN NEEDED", "(last good sync: 2026-11-27")
}
