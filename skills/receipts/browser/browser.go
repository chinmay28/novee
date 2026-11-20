// Package browser drives one tab of a locally installed Chromium or Chrome.
//
// With a profile directory the tab keeps its cookies between runs, which is
// how a sign-in done once by hand (headed) is reused by later unattended runs
// (headless). The package knows nothing about any shop.
package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

// Options configure Open.
type Options struct {
	ProfileDir string // empty means a throwaway profile
	Headed     bool   // show a window (needs a display)
	ExecPath   string // empty means look for an installed browser
}

// Browser is one open tab. It satisfies source.Page.
type Browser struct {
	ctx    context.Context
	cancel []context.CancelFunc
}

// FindExec returns the browser binary to use: $NOVEE_CHROMIUM, or the first
// of the usual names on PATH.
func FindExec() (string, error) {
	if p := os.Getenv("NOVEE_CHROMIUM"); p != "" {
		return p, nil
	}
	for _, name := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	return "", errors.New("no Chrome or Chromium found; install one or set NOVEE_CHROMIUM to its path")
}

// Open starts the browser. Close it when done.
func Open(o Options) (*Browser, error) {
	if o.ExecPath == "" {
		p, err := FindExec()
		if err != nil {
			return nil, err
		}
		o.ExecPath = p
	}
	opts := []chromedp.ExecAllocatorOption{
		chromedp.ExecPath(o.ExecPath),
		chromedp.NoFirstRun, chromedp.NoDefaultBrowserCheck,
		// Look like the ordinary browser the owner signed in with.
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.Flag("password-store", "basic"),
		chromedp.WindowSize(1280, 900),
	}
	if !o.Headed {
		opts = append(opts, chromedp.Flag("headless", "new"))
	}
	if o.ProfileDir != "" {
		if err := os.MkdirAll(o.ProfileDir, 0o700); err != nil {
			return nil, err
		}
		opts = append(opts, chromedp.UserDataDir(o.ProfileDir))
	}
	if os.Geteuid() == 0 {
		opts = append(opts, chromedp.NoSandbox) // Chromium refuses its sandbox as root
	}
	actx, c1 := chromedp.NewExecAllocator(context.Background(), opts...)
	// The driver logs browser events it does not know; they are harmless here.
	ctx, c2 := chromedp.NewContext(actx, chromedp.WithErrorf(func(string, ...any) {}))
	b := &Browser{ctx: ctx, cancel: []context.CancelFunc{c2, c1}}
	if err := chromedp.Run(ctx); err != nil {
		b.Close()
		return nil, fmt.Errorf("browser: cannot start %s: %w", o.ExecPath, err)
	}
	return b, nil
}

// Close shuts the browser down. With a profile directory, cookies are saved.
func (b *Browser) Close() {
	tctx, cancel := context.WithTimeout(b.ctx, 10*time.Second)
	_ = chromedp.Cancel(tctx) // graceful: lets Chromium flush the profile
	cancel()
	for _, c := range b.cancel {
		c()
	}
}

// run executes actions and stops them when either ctx or the browser ends.
func (b *Browser) run(ctx context.Context, actions ...chromedp.Action) error {
	rctx, cancel := context.WithCancel(b.ctx)
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	err := chromedp.Run(rctx, actions...)
	if err != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

// Goto loads url and returns when the document has loaded.
func (b *Browser) Goto(ctx context.Context, url string) error {
	return b.run(ctx, chromedp.Navigate(url))
}

// Eval runs a JavaScript expression, awaits it if it is a promise, and
// decodes its value into out (which may be nil to ignore it).
func (b *Browser) Eval(ctx context.Context, js string, out any) error {
	var raw json.RawMessage
	err := b.run(ctx, chromedp.Evaluate(js, &raw, func(p *runtime.EvaluateParams) *runtime.EvaluateParams {
		return p.WithAwaitPromise(true)
	}))
	if err != nil || out == nil || len(raw) == 0 {
		return err
	}
	return json.Unmarshal(raw, out)
}

// PDF prints an HTML document to PDF (US Letter, with backgrounds).
func (b *Browser) PDF(ctx context.Context, html string) ([]byte, error) {
	var pdf []byte
	err := b.run(ctx,
		chromedp.Navigate("about:blank"),
		chromedp.ActionFunc(func(ctx context.Context) error {
			tree, err := page.GetFrameTree().Do(ctx)
			if err != nil {
				return err
			}
			return page.SetDocumentContent(tree.Frame.ID, html).Do(ctx)
		}),
		chromedp.ActionFunc(func(ctx context.Context) error {
			var err error
			pdf, _, err = page.PrintToPDF().WithPrintBackground(true).WithPreferCSSPageSize(true).Do(ctx)
			return err
		}),
	)
	return pdf, err
}
