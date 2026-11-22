// Package guard decides which browser actions the agent may take on its own.
//
// Reading is free. An action that could spend money, send something, or
// change an account needs the owner's approval. Typing secrets is never
// allowed: the owner signs in by hand. The rules look only at what the page
// says about an element, so they are a seat belt, not a proof: a page can
// label a dangerous button harmlessly.
package guard

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"

	"github.com/chinmay28/novee/skills/web-browse/page"
)

// Decision is what the guard says about an action.
type Decision int

const (
	Allow         Decision = iota // go ahead
	NeedsApproval                 // only with the owner's approval (--yes)
	Refuse                        // never
)

var consequential = regexp.MustCompile(`(?i)\b(buy|purchase|order now|place (your |my |the )?order|reorder|check ?out|pay|payment|` +
	`add to (cart|bag|basket)|subscribe|unsubscribe|sign ?up|register|enroll|delete|remove|confirm|submit|send|` +
	`cancel (my |the |this |your )?(order|subscription|membership|plan|item|booking|reservation)|` +
	`transfer|donate|book|reserve|log ?out|sign ?out|post|publish|redeem|renew|upgrade|return items?)\b`)

// Click judges clicking e.
func Click(e page.Element) (Decision, string) {
	switch {
	case e.Password || e.Payment:
		return Allow, "" // focusing a field changes nothing; typing into it is refused
	case e.Submit && e.Search:
		return Allow, ""
	case consequential.MatchString(e.Name):
		return NeedsApproval, fmt.Sprintf("%s %q looks like it changes something (orders, payments, account or messages)", e.Kind, e.Name)
	case e.Submit:
		return NeedsApproval, fmt.Sprintf("%s %q sends a form", e.Kind, e.Name)
	}
	return Allow, ""
}

// Type judges typing into e, and pressing Enter afterwards if enter is set.
func Type(e page.Element, enter bool) (Decision, string) {
	switch {
	case e.Password:
		return Refuse, "this is a password field. Novee never types passwords. Run `novee-browse login-request`: it sends Chinmay a link to sign in himself"
	case e.Payment:
		return Refuse, "this field takes a card, bank or identity number. Novee never types those"
	case e.Kind != "input" && e.Kind != "textarea":
		return Refuse, fmt.Sprintf("element %d is a %s, not a text field", e.Ref, e.Kind)
	case enter && !e.Search:
		return NeedsApproval, fmt.Sprintf("pressing Enter in %q sends a form that is not a search", e.Name)
	}
	return Allow, ""
}

var consequentialPath = regexp.MustCompile(`(?i)(log|sign)[-_]?out|check[-_]?out|place[-_]?order|buy[-_]?now|delete|unsubscribe|/pay(ment)?s?(/|$)`)

// Open judges going straight to an address: some links act just by being opened.
func Open(raw string) (Decision, string) {
	u, err := url.Parse(raw)
	if err == nil && consequentialPath.MatchString(u.Path+"?"+u.RawQuery) {
		return NeedsApproval, "opening " + u.Host + u.Path + " looks like it changes something (signs out, pays, orders or deletes)"
	}
	return Allow, ""
}

// noAgents matches sites that turn AI agents away in so many words. Amazon
// answered a sign-in with "Continued access by an unauthorized AI agent
// violates Amazon's Conditions of Use"; that is a decision to respect, not
// an obstacle to get around.
var noAgents = regexp.MustCompile(`(^|\.)amazon\.[a-z.]+$`)

// URL checks an address the agent wants to open. Only public http(s) sites
// are allowed unless allowLAN is set: pages on this machine or the home
// network (routers, admin panels) are not for unattended browsing.
func URL(raw string, allowLAN bool) error {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return fmt.Errorf("%q is not a web address; give a full one like https://www.example.com/page", raw)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("only http and https addresses can be opened, not %s:", u.Scheme)
	}
	if u.User != nil {
		return errors.New("addresses with a user name or password in them are not opened")
	}
	if noAgents.MatchString(strings.ToLower(u.Hostname())) {
		return fmt.Errorf("%s does not allow AI agents and says so on its pages, so it is not opened. Tell Chinmay that; do not look for another way in", u.Hostname())
	}
	if allowLAN {
		return nil
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") ||
		strings.HasSuffix(host, ".lan") || strings.HasSuffix(host, ".internal") || !strings.Contains(host, ".") {
		return fmt.Errorf("%s is on this machine or the home network; only public websites are opened", host)
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified()) {
		return fmt.Errorf("%s is a private address; only public websites are opened", host)
	}
	return nil
}
