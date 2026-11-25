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

func TestReadOnly(t *testing.T) {
	el := func(kind, name string) page.Element { return page.Element{Kind: kind, Name: name} }
	for _, name := range []string{"Pay Now", "Pay Bill", "Reorder", "Add to cart", "Add an Account", "Rate your order", "Leave a review", "Buy Items Again",
		"Return or Replace Items", "Update My Information", "Set up autopay", "Enroll", "Change my mailing address", "Sign Out", "Do not show this message again",
		"Redeem", "Save", "Edit profile", "Cancel order", "Resend", "Download Bill and Payment History. Submit", "Accept All", "Start service", "Tip"} {
		if ReadOnly("click", el("button", name), false) == nil {
			t.Errorf("%q must be refused on a read-only site", name)
		}
	}
	for _, name := range []string{"View Receipt", "View Return Receipt", "View Bill PDF", "Warehouse", "Online", "Go to next page", "Next", "Load More", "Show more",
		"Orders & Purchases", "Usage and Rates", "Billing", "Close", "Reject All", "Opt out", "Download receipt", "View Order Details", "2", "Address Book", "Returns"} {
		if err := ReadOnly("click", el("button", name), false); err != nil {
			t.Errorf("%q only shows something and must be allowed: %v", name, err)
		}
	}
	if ReadOnly("click", page.Element{Kind: "button", Name: "Go", Submit: true}, false) == nil {
		t.Error("a form may not be sent")
	}
	if err := ReadOnly("click", page.Element{Kind: "button", Name: "Search", Submit: true, Search: true}, false); err != nil {
		t.Errorf("a search may be sent: %v", err)
	}
	if ReadOnly("type", el("input", "Gift note"), false) == nil || ReadOnly("type", page.Element{Kind: "input", Name: "Search", Search: true}, true) != nil {
		t.Error("only search boxes are typed into")
	}
	if ReadOnly("select", el("select", "Payment frequency"), false) == nil || ReadOnly("select", el("select", "Showing"), false) != nil {
		t.Error("a list that filters is allowed, one that may be a setting is not")
	}
}
