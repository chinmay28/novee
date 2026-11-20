package session

import "testing"

func TestSiteProfile(t *testing.T) {
	for raw, want := range map[string]string{
		"https://www.costco.com/OrderStatusCmd": "costco",
		"https://signin.costco.com/x":           "costco",
		"https://costco.com":                    "costco",
		"https://www.amazon.co.uk/orders":       "amazon",
		"https://smile.amazon.com":              "amazon",
		"http://127.0.0.1:8080/":                "main",
		"https://localhost/":                    "localhost",
		"nonsense":                              "main",
	} {
		if got := SiteProfile(raw); got != want {
			t.Errorf("SiteProfile(%q) = %q, want %q", raw, got, want)
		}
	}
}
