package page

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/chromedp/cdproto/emulation"
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

// Reload loads the page again.
func (p *Page) Reload(ctx context.Context) error {
	if err := p.run(ctx, chromedp.Reload()); err != nil {
		return err
	}
	p.settle(ctx)
	return nil
}

// WaitContent gives a page that draws itself with scripts (most sign-in
// pages) up to limit to show something that can be clicked, and reports
// whether it did.
func (p *Page) WaitContent(ctx context.Context, limit time.Duration) bool {
	for deadline := time.Now().Add(limit); time.Now().Before(deadline) && ctx.Err() == nil; time.Sleep(time.Second) {
		if s, err := p.Snapshot(ctx); err == nil && len(s.Elements) > 0 {
			return true
		}
	}
	return false
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

// printOnlyDialog makes the open pop-up, if any, the only thing that prints,
// at its full length. It returns whether there was one.
const printOnlyDialog = `(() => {` + describe + `
  const d = __dialog(); if (!d) return false;
  d.setAttribute('data-novee-print', '');
  for (let a = d.parentElement; a && a !== document.documentElement; a = a.parentElement) a.setAttribute('data-novee-print-up', '');
  const st = document.createElement('style'); st.id = 'novee-print';
  st.textContent = '@media print {' +
    'body * { visibility: hidden !important; }' +
    '[data-novee-print], [data-novee-print] * { visibility: visible !important; max-height: none !important; overflow: visible !important; }' +
    '[data-novee-print-up] { position: static !important; transform: none !important; overflow: visible !important; max-height: none !important; height: auto !important; }' +
    '[data-novee-print] { position: absolute !important; left: 0 !important; top: 0 !important; margin: 0 !important; height: auto !important; transform: none !important; box-shadow: none !important; }' +
    '}';
  document.head.appendChild(st);
  return true;
})()`

const printEverything = `(() => {
  const st = document.getElementById('novee-print'); if (st) st.remove();
  document.querySelectorAll('[data-novee-print]').forEach(e => e.removeAttribute('data-novee-print'));
  document.querySelectorAll('[data-novee-print-up]').forEach(e => e.removeAttribute('data-novee-print-up'));
})()`

// PDF prints the page as the site lays it out for printing. When a pop-up is
// open (a receipt, an invoice), it prints the pop-up alone.
func (p *Page) PDF(ctx context.Context) ([]byte, error) {
	var only bool
	if err := p.eval(ctx, printOnlyDialog, &only); err != nil {
		return nil, err
	}
	if only {
		defer p.eval(context.WithoutCancel(ctx), printEverything, nil)
	}
	var pdf []byte
	err := p.run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		var err error
		pdf, _, err = cdppage.PrintToPDF().WithPrintBackground(true).Do(ctx)
		return err
	}))
	return pdf, err
}

// The rest lets a person operate the tab from a picture of it: the sign-in
// viewer shows JPEG and passes on where the owner tapped and what he typed.

// JPEG returns a picture of the visible part of the page, small enough to send often.
func (p *Page) JPEG(ctx context.Context) ([]byte, error) {
	var img []byte
	err := p.run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		var err error
		img, err = cdppage.CaptureScreenshot().WithFormat(cdppage.CaptureScreenshotFormatJpeg).WithQuality(60).Do(ctx)
		return err
	}))
	return img, err
}

// ClickAt clicks the point that is fx of the way across and fy of the way down the screen.
func (p *Page) ClickAt(ctx context.Context, fx, fy float64) error {
	var size struct{ W, H float64 }
	if err := p.eval(ctx, `({w: innerWidth, h: innerHeight})`, &size); err != nil {
		return err
	}
	return p.run(ctx, chromedp.MouseClickXY(fx*size.W, fy*size.H))
}

// Insert types text into whatever has the focus.
func (p *Page) Insert(ctx context.Context, text string) error {
	return p.run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		return input.InsertText(text).Do(ctx)
	}))
}

// Press presses one of the keys "Enter", "Tab" or "Backspace".
func (p *Page) Press(ctx context.Context, key string) error {
	k, ok := map[string]string{"Enter": kb.Enter, "Tab": kb.Tab, "Backspace": kb.Backspace}[key]
	if !ok {
		return fmt.Errorf("unknown key %q", key)
	}
	return p.run(ctx, chromedp.KeyEvent(k))
}

// ScrollBy moves by a fraction of a screen; negative is up.
func (p *Page) ScrollBy(ctx context.Context, screens float64) error {
	return p.eval(ctx, fmt.Sprintf(`scrollBy(0, innerHeight * %f)`, screens), nil)
}

// Where returns the title and address of the page, and whether it shows a password field.
func (p *Page) Where(ctx context.Context) (title, url string, password bool, err error) {
	var w struct {
		Title, URL string
		Password   bool
	}
	err = p.eval(ctx, `({title: document.title, url: location.href, password: Array.from(document.querySelectorAll('input[type=password]')).some(e => e.offsetParent !== null)})`, &w)
	return w.Title, w.URL, w.Password, err
}

// Resize lays the page out for a screen of w by h points, drawn at twice that
// many pixels, so that it is readable on a phone. It lasts as long as this
// process stays attached.
func (p *Page) Resize(ctx context.Context, w, h int) error {
	return p.run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		return emulation.SetDeviceMetricsOverride(int64(w), int64(h), 2, false).Do(ctx)
	}))
}
