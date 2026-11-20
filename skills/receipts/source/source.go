// Package source is the contract between a shop connector and the rest of the
// skill. To add a shop (Amazon, Target, ...), implement Source in a new
// package and register it in cmd/novee-receipts.
package source

import (
	"context"
	"errors"
	"time"

	"github.com/chinmay28/novee/skills/receipts/receipt"
)

// Page is the one browser tab a connector works in. The tab belongs to a
// profile the owner signed in to by hand; connectors never see a password.
type Page interface {
	// Goto loads url and returns when the document has loaded.
	Goto(ctx context.Context, url string) error
	// Eval runs a JavaScript expression in the page, waits for it if it is a
	// promise, and decodes its JSON-serialisable value into out.
	Eval(ctx context.Context, js string, out any) error
}

// ErrSignInNeeded means the saved browser profile is not signed in to the
// shop (never was, or the session expired). Only the owner can fix it.
var ErrSignInNeeded = errors.New("sign-in needed")

// Source fetches receipts from one shop.
type Source interface {
	// Name is the store name written into every receipt, e.g. "costco".
	Name() string
	// StartURL is a page of the shop that shows the sign-in form when signed
	// out and the account when signed in.
	StartURL() string
	// SignedIn reports whether the page, already at StartURL, has a usable
	// session right now. It must not wait.
	SignedIn(ctx context.Context, p Page) (bool, error)
	// Fetch returns every receipt dated from..to (inclusive) that the shop
	// offers. It returns the receipts it did get together with an error when
	// only part of the range could be fetched.
	Fetch(ctx context.Context, p Page, from, to time.Time) ([]receipt.Receipt, error)
	// History is how far back the shop keeps receipts; the first sync asks
	// for this much.
	History() time.Duration
}

// WaitSignedIn polls SignedIn until it is true or the wait is over, in which
// case it returns ErrSignInNeeded.
func WaitSignedIn(ctx context.Context, s Source, p Page, wait, every time.Duration) error {
	deadline := time.Now().Add(wait)
	for {
		ok, err := s.SignedIn(ctx, p)
		if err == nil && ok {
			return nil
		}
		if time.Now().After(deadline) {
			if err != nil {
				return errors.Join(ErrSignInNeeded, err)
			}
			return ErrSignInNeeded
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(every):
		}
	}
}
