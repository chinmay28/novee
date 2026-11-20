package page

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/chromedp/cdproto/input"
	cdppage "github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"
)

// Page is an attached browser tab.
type Page struct{ ctx context.Context }

// ErrGone means the numbered element is no longer on the page.
var ErrGone = errors.New("that element is not on the page any more")

// Attach connects to tab targetID of the browser listening on port.
//
// There is deliberately no way to let go again: the driver ends a connection
// by closing the browser it is connected to, and the browser must outlive
// this process. The connection simply drops when the process exits, which
// the browser does not mind.
func Attach(port int, targetID string) (*Page, error) {
	actx, _ := chromedp.NewRemoteAllocator(context.Background(), fmt.Sprintf("http://127.0.0.1:%d", port))
	// The driver logs browser events it does not know; they are harmless here.
	ctx, _ := chromedp.NewContext(actx, chromedp.WithTargetID(target.ID(targetID)),
		chromedp.WithErrorf(func(string, ...any) {}))
	if err := chromedp.Run(ctx); err != nil {
		return nil, fmt.Errorf("cannot attach to the browser: %w", err)
	}
	return &Page{ctx: ctx}, nil
}

func (p *Page) run(ctx context.Context, actions ...chromedp.Action) error {
	rctx, cancel := context.WithCancel(p.ctx)
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	err := chromedp.Run(rctx, actions...)
	if err != nil && ctx.Err() != nil {
		return fmt.Errorf("the page took too long: %w", ctx.Err())
	}
	return err
}

func (p *Page) eval(ctx context.Context, js string, out any) error {
	var raw json.RawMessage
	err := p.run(ctx, chromedp.Evaluate(js, &raw, func(e *runtime.EvaluateParams) *runtime.EvaluateParams {
		return e.WithAwaitPromise(true)
	}))
	if err != nil || out == nil || len(raw) == 0 {
		return err
	}
	return json.Unmarshal(raw, out)
}

// settle waits for the page to finish what an action started: a navigation,
// or a script redrawing part of the page.
func (p *Page) settle(ctx context.Context) {
	time.Sleep(700 * time.Millisecond)
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(250 * time.Millisecond) {
		var state string
		if err := p.eval(ctx, `document.readyState`, &state); err != nil && ctx.Err() != nil {
			return
		} else if state == "complete" {
			break
		}
	}
	time.Sleep(500 * time.Millisecond)
}

// Goto loads url.
func (p *Page) Goto(ctx context.Context, url string) error {
	if err := p.run(ctx, chromedp.Navigate(url)); err != nil {
		return err
	}
	p.settle(ctx)
	return nil
}

// Back goes to the previous page.
func (p *Page) Back(ctx context.Context) error {
	if err := p.eval(ctx, `history.back()`, nil); err != nil {
		return err
	}
	p.settle(ctx)
	return nil
}

// Scroll moves one screen "down" or "up", or to the "top" or "bottom".
func (p *Page) Scroll(ctx context.Context, where string) error {
	js, ok := map[string]string{
		"down": `scrollBy(0, innerHeight * 0.85)`, "up": `scrollBy(0, -innerHeight * 0.85)`,
		"top": `scrollTo(0, 0)`, "bottom": `scrollTo(0, document.documentElement.scrollHeight)`,
	}[where]
	if !ok {
		return fmt.Errorf("scroll %q: use down, up, top or bottom", where)
	}
	if err := p.eval(ctx, js, nil); err != nil {
		return err
	}
	time.Sleep(800 * time.Millisecond) // pages often load more as you scroll
	return nil
}

// Snapshot describes the page and renumbers its elements.
func (p *Page) Snapshot(ctx context.Context) (Snapshot, error) {
	var s Snapshot
	err := p.eval(ctx, snapshotJS, &s)
	return s, err
}

// Element returns element ref of the latest snapshot as it is now.
func (p *Page) Element(ctx context.Context, ref int) (Element, error) {
	var e *Element
	if err := p.eval(ctx, lookupJS(ref), &e); err != nil {
		return Element{}, err
	}
	if e == nil {
		return Element{}, ErrGone
	}
	return *e, nil
}

func sel(ref int) string { return fmt.Sprintf(`document.querySelector('[data-novee-ref="%d"]')`, ref) }

// Click clicks element ref the way a person would, with the mouse.
func (p *Page) Click(ctx context.Context, ref int) error {
	var at struct {
		X, Y    float64
		Covered bool
	}
	err := p.eval(ctx, fmt.Sprintf(`(() => { const el = %s; if (!el) return null;
  el.scrollIntoView({block: 'center', inline: 'center'});
  el.removeAttribute('target'); // stay in this tab
  const r = el.getBoundingClientRect(), x = r.left + r.width / 2, y = r.top + r.height / 2;
  const top = document.elementFromPoint(x, y);
  return {x: x, y: y, covered: !top || !(el.contains(top) || top.contains(el))};
})()`, sel(ref)), &at)
	if err != nil {
		return err
	}
	if at.X == 0 && at.Y == 0 {
		return ErrGone
	}
	if at.Covered { // something overlaps it; a scripted click still reaches it
		err = p.eval(ctx, sel(ref)+`.click()`, nil)
	} else {
		err = p.run(ctx, chromedp.MouseClickXY(at.X, at.Y))
	}
	if err != nil {
		return err
	}
	p.settle(ctx)
	return nil
}

// Type replaces the content of text field ref with text, then presses Enter if enter is set.
func (p *Page) Type(ctx context.Context, ref int, text string, enter bool) error {
	var ok bool
	err := p.eval(ctx, fmt.Sprintf(`(() => { const el = %s; if (!el) return false;
  el.scrollIntoView({block: 'center'}); el.focus();
  if (el.select) el.select(); else document.execCommand('selectAll');
  document.execCommand('delete');
  return true; })()`, sel(ref)), &ok)
	if err != nil {
		return err
	}
	if !ok {
		return ErrGone
	}
	if text != "" {
		if err := p.run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
			return input.InsertText(text).Do(ctx)
		})); err != nil {
			return err
		}
	}
	if enter {
		if err := p.run(ctx, chromedp.KeyEvent(kb.Enter)); err != nil {
			return err
		}
		p.settle(ctx)
	}
	return nil
}

// Select chooses the option of drop-down ref whose text contains want.
func (p *Page) Select(ctx context.Context, ref int, want string) error {
	var res string
	err := p.eval(ctx, fmt.Sprintf(`(() => { const el = %s; if (!el) return 'gone';
  if (el.tagName !== 'SELECT') return 'notselect';
  const w = %q.toLowerCase();
  const o = Array.from(el.options).find(o => o.text.trim().toLowerCase() === w) ||
            Array.from(el.options).find(o => o.text.toLowerCase().includes(w));
  if (!o) return 'nooption';
  el.value = o.value;
  el.dispatchEvent(new Event('input', {bubbles: true})); el.dispatchEvent(new Event('change', {bubbles: true}));
  return 'ok'; })()`, sel(ref), want), &res)
	switch {
	case err != nil:
		return err
	case res == "gone":
		return ErrGone
	case res == "notselect":
		return fmt.Errorf("element %d is not a drop-down list; click it instead", ref)
	case res == "nooption":
		return fmt.Errorf("the list has no option containing %q", want)
	}
	p.settle(ctx)
	return nil
}

// Screenshot returns a PNG of the visible part of the page.
func (p *Page) Screenshot(ctx context.Context) ([]byte, error) {
	var png []byte
	return png, p.run(ctx, chromedp.CaptureScreenshot(&png))
}

// PDF prints the page as the site lays it out for printing.
func (p *Page) PDF(ctx context.Context) ([]byte, error) {
	var pdf []byte
	err := p.run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		var err error
		pdf, _, err = cdppage.PrintToPDF().WithPrintBackground(true).Do(ctx)
		return err
	}))
	return pdf, err
}
