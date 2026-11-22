package guard

import (
	"testing"

	"github.com/chinmay28/novee/skills/web-browse/page"
)

func TestClick(t *testing.T) {
	for _, c := range []struct {
		e    page.Element
		want Decision
	}{
		{page.Element{Kind: "link", Name: "Orders & Purchases"}, Allow},
		{page.Element{Kind: "link", Name: "View Receipt"}, Allow},
		{page.Element{Kind: "button", Name: "Show more"}, Allow},
		{page.Element{Kind: "button", Name: "Accept all cookies"}, Allow},
		{page.Element{Kind: "button", Name: "Cancel"}, Allow}, // dismissing a dialog
		{page.Element{Kind: "button", Name: "Search", Submit: true, Search: true}, Allow},
		{page.Element{Kind: "input", Name: "Password", Password: true}, Allow},
		{page.Element{Kind: "button", Name: "Place your order"}, NeedsApproval},
		{page.Element{Kind: "button", Name: "Add to Cart"}, NeedsApproval},
		{page.Element{Kind: "link", Name: "Buy Again"}, NeedsApproval},
		{page.Element{Kind: "button", Name: "Proceed to checkout"}, NeedsApproval},
		{page.Element{Kind: "link", Name: "Cancel order"}, NeedsApproval},
		{page.Element{Kind: "link", Name: "Sign Out"}, NeedsApproval},
		{page.Element{Kind: "button", Name: "Send message"}, NeedsApproval},
		{page.Element{Kind: "button", Name: "Delete address"}, NeedsApproval},
		{page.Element{Kind: "button", Name: "Continue", Submit: true}, NeedsApproval}, // an unlabelled form
		{page.Element{Kind: "link", Name: "Paylocity careers"}, Allow},                // "pay" inside a word
	} {
		if got, why := Click(c.e); got != c.want {
			t.Errorf("Click(%q submit=%v) = %v (%s), want %v", c.e.Name, c.e.Submit, got, why, c.want)
		}
	}
}

func TestType(t *testing.T) {
	text := page.Element{Kind: "input", Name: "Gift note"}
	for _, c := range []struct {
		e     page.Element
		enter bool
		want  Decision
	}{
		{page.Element{Kind: "input", Name: "Search", Search: true}, true, Allow},
		{text, false, Allow},
		{text, true, NeedsApproval},
		{page.Element{Kind: "input", Name: "Password", Password: true}, false, Refuse},
		{page.Element{Kind: "input", Name: "Card number", Payment: true}, false, Refuse},
		{page.Element{Kind: "button", Name: "Go"}, false, Refuse},
	} {
		if got, _ := Type(c.e, c.enter); got != c.want {
			t.Errorf("Type(%q, enter=%v) = %v, want %v", c.e.Name, c.enter, got, c.want)
		}
	}
}

func TestOpen(t *testing.T) {
	for raw, want := range map[string]Decision{
		"https://www.costco.com/OrderStatusCmd":              Allow,
		"https://www.costco.com/command-strips.product.html": Allow,
		"https://www.costco.com/LogoutCmd?x=1":               NeedsApproval,
		"https://shop.example.com/checkout/confirm":          NeedsApproval,
		"https://news.example.com/unsubscribe?id=7":          NeedsApproval,
		"https://example.com/account/delete":                 NeedsApproval,
	} {
		if got, _ := Open(raw); got != want {
			t.Errorf("Open(%q) = %v, want %v", raw, got, want)
		}
	}
}

func TestURL(t *testing.T) {
	for raw, ok := range map[string]bool{
		"https://www.costco.com/OrderStatusCmd": true,
		"http://example.org":                    true,
		"file:///etc/passwd":                    false,
		"chrome://settings":                     false,
		"javascript:alert(1)":                   false,
		"https://user:pw@example.com/":          false,
		"http://localhost:18789/":               false,
		"http://127.0.0.1/":                     false,
		"http://192.168.1.1/admin":              false,
		"http://10.0.0.5/":                      false,
		"http://[::1]/":                         false,
		"http://router/":                        false,
		"http://umbrel.local/":                  false,
		"not a url":                             false,
	} {
		if err := URL(raw, false); (err == nil) != ok {
			t.Errorf("URL(%q) = %v, want ok=%v", raw, err, ok)
		}
	}
	if err := URL("http://127.0.0.1:8080/", true); err != nil {
		t.Errorf("with allowLAN: %v", err)
	}
	if err := URL("file:///etc/passwd", true); err == nil {
		t.Error("allowLAN must not allow other schemes")
	}
}

func TestNoAgentsSites(t *testing.T) {
	for _, u := range []string{"https://www.amazon.com/your-orders", "https://amazon.com/", "https://www.amazon.co.uk/x"} {
		if err := URL(u, true); err == nil {
			t.Errorf("%s must not be opened", u)
		}
	}
	if err := URL("https://www.amazonia-travel.example/", false); err != nil {
		t.Errorf("a name that merely starts like it is fine: %v", err)
	}
}
