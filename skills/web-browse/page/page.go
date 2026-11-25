package page

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/input"
	cdppage "github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"
)

// Page is an attached browser tab.
type Page struct {
	ctx context.Context
	// Check, if set, is asked before every click, typing and choice; an error
	// stops the action. It is how a read-only site stays read-only whichever
	// command is at work.
	Check func(action string, e Element, enter bool) error
}

// allowed asks Check about an action on element ref.
func (p *Page) allowed(ctx context.Context, action string, ref int, enter bool) error {
	if p.Check == nil {
		return nil
	}
	e, err := p.Element(ctx, ref)
	if err != nil {
		return err
	}
	return p.Check(action, e, enter)
}

// ErrPrints means a download button only prints the page; PDF does the same.
var ErrPrints = errors.New("this button prints the page instead of handing over a file")

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

// Eval runs a script in the page and decodes what it returns into out. It is
// for tools built on this package that read a kind of page in their own way.
func (p *Page) Eval(ctx context.Context, js string, out any) error { return p.eval(ctx, js, out) }

// Settle waits for the page to finish what an action started.
func (p *Page) Settle(ctx context.Context) { p.settle(ctx) }

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

// sel is the script expression for element ref, wherever it is: in the page
// or inside the shadow tree of a web component.
func sel(ref int) string {
	return fmt.Sprintf(`(function f(r) { const hit = r.querySelector('[data-novee-ref="%d"]'); if (hit) return hit;
  for (const e of r.querySelectorAll('*')) if (e.shadowRoot) { const h = f(e.shadowRoot); if (h) return h; } return null; })(document)`, ref)
}

// Click clicks element ref the way a person would, with the mouse.
func (p *Page) Click(ctx context.Context, ref int) error {
	if err := p.allowed(ctx, "click", ref, false); err != nil {
		return err
	}
	var at struct {
		X, Y    float64
		Covered bool
	}
	err := p.eval(ctx, fmt.Sprintf(`(() => { const el = %s; if (!el) return null;
  el.scrollIntoView({block: 'center', inline: 'center'});
  el.removeAttribute('target'); // stay in this tab
  const r = el.getBoundingClientRect(), x = r.left + r.width / 2, y = r.top + r.height / 2;
  let top = document.elementFromPoint(x, y);
  while (top && top.shadowRoot && top.shadowRoot.elementFromPoint(x, y) && top.shadowRoot.elementFromPoint(x, y) !== top) top = top.shadowRoot.elementFromPoint(x, y);
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
	if err := p.allowed(ctx, "type", ref, enter); err != nil {
		return err
	}
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
	if err := p.allowed(ctx, "select", ref, false); err != nil {
		return err
	}
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

// printOnly makes one part of the page the only thing that prints, at its
// full length: the open pop-up if there is one, else (when content is set)
// the block that holds the page's own content without the site's header,
// menus and footer. It returns which it found: "dialog", "content" or "".
func printOnly(content bool) string {
	return `(() => {` + describe + `
  let kind = 'dialog', d = __dialog();
  if (!d && ` + fmt.Sprint(content) + `) {
    kind = 'content';
    d = document.querySelector('main, [role=main]');
    if (!d) { // no landmark: the smallest block that holds the page's main heading and most of its text
      const h = document.querySelector('h1');
      for (d = h; d && d.parentElement && d.parentElement !== document.body && (d.innerText || '').length < 400; d = d.parentElement);
    }
  }
  if (!d || d === document.body) return '';
  d.setAttribute('data-novee-print', '');
  for (let a = d; a.parentElement && a !== document.body; a = a.parentElement) {
    if (a !== d) a.setAttribute('data-novee-print-up', '');
    for (const sib of a.parentElement.children)
      if (sib !== a && !['SCRIPT', 'STYLE', 'LINK'].includes(sib.tagName)) sib.setAttribute('data-novee-print-hide', '');
  }
  const st = document.createElement('style'); st.id = 'novee-print';
  st.textContent = '@media print {' +
    '[data-novee-print-hide] { display: none !important; }' +
    '[data-novee-print-up] { display: block !important; position: static !important; transform: none !important; overflow: visible !important; max-height: none !important; height: auto !important; min-height: 0 !important; margin: 0 !important; padding: 0 !important; border: 0 !important; background: none !important; }' +
    '[data-novee-print] { position: static !important; margin: 0 !important; height: auto !important; max-height: none !important; overflow: visible !important; transform: none !important; box-shadow: none !important; }' +
    '[data-novee-print] * { max-height: none !important; overflow: visible !important; }' +
    'html, body { height: auto !important; min-height: 0 !important; overflow: visible !important; background: #fff !important; }' +
    // A pop-up keeps the width it has on screen and loses its own Close and Print buttons.
    (kind === 'dialog' ? '[data-novee-print] { width: ' + Math.round(d.getBoundingClientRect().width) + 'px !important; } [data-novee-print] button { display: none !important; }' : '') +
    '}';
  document.head.appendChild(st);
  return kind;
})()`
}

const printEverything = `(() => {
  const st = document.getElementById('novee-print'); if (st) st.remove();
  for (const a of ['data-novee-print', 'data-novee-print-up', 'data-novee-print-hide'])
    document.querySelectorAll('[' + a + ']').forEach(e => e.removeAttribute(a));
})()`

// PDF prints the page as the site lays it out for printing. When a pop-up is
// open (a receipt, an invoice), it prints the pop-up alone.
func (p *Page) PDF(ctx context.Context) ([]byte, error) { return p.pdf(ctx, false) }

// ContentPDF prints the open pop-up, or else the page's own content without
// the site's header, menus and footer.
func (p *Page) ContentPDF(ctx context.Context) ([]byte, error) { return p.pdf(ctx, true) }

func (p *Page) pdf(ctx context.Context, content bool) ([]byte, error) {
	var only string
	if err := p.eval(ctx, printOnly(content), &only); err != nil {
		return nil, err
	}
	if only != "" {
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

// OnlyFrame replaces the page by the document of its largest frame from the
// same site, such as a receipt shown in a pop-up, so that printing gives
// that document whole, on as many pages as it needs. It reports whether
// there was one. The page is gone afterwards: load something else next.
func (p *Page) OnlyFrame(ctx context.Context) (bool, error) {
	var ok bool
	err := p.eval(ctx, `(() => {
  let best = null, area = 0;
  for (const f of document.querySelectorAll('iframe')) {
    try {
      const a = f.offsetWidth * f.offsetHeight;
      if (a > area && f.contentDocument && f.contentDocument.body && f.contentDocument.body.innerText.trim().length > 40) { best = f; area = a; }
    } catch (e) {}
  }
  if (!best) return false;
  const html = '<!doctype html>' + best.contentDocument.documentElement.outerHTML;
  document.open(); document.write(html); document.close();
  return true;
})()`, &ok)
	if err == nil && ok {
		time.Sleep(700 * time.Millisecond) // pictures in it load again
	}
	return ok, err
}

// Hash changes the part of the address after "#" the way a link inside a
// one-page site does: from, a pattern, is replaced by to. It returns false
// when the address has no such part.
func (p *Page) Hash(ctx context.Context, from, to string) (bool, error) {
	var ok bool
	err := p.eval(ctx, fmt.Sprintf(`(() => { const re = new RegExp(%q); if (!re.test(location.hash)) return false;
  location.hash = location.hash.replace(re, %q); return true; })()`, from, to), &ok)
	if err == nil && ok {
		p.settle(ctx)
	}
	return ok, err
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

// Insert types text into whatever has the focus, key by key as a keyboard
// would: some sign-in forms reject text that arrives without key presses.
// Characters no key produces are inserted as they are.
func (p *Page) Insert(ctx context.Context, text string) error {
	for _, r := range text {
		if r < 0x20 || r > 0x7e {
			return p.run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
				return input.InsertText(text).Do(ctx)
			}))
		}
	}
	return p.run(ctx, chromedp.KeyEvent(text))
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
	err = p.eval(ctx, `({title: document.title, url: location.href, password: (function all(r) { let o = Array.from(r.querySelectorAll('input[type=password]')); for (const e of r.querySelectorAll('*')) if (e.shadowRoot) o = o.concat(all(e.shadowRoot)); return o; })(document).some(e => e.offsetParent !== null)})`, &w)
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

// Download clicks element ref and returns the file the site hands over, saved
// in dir under the name the site gave it.
func (p *Page) Download(ctx context.Context, ref int, dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	before := map[string]bool{}
	old, _ := os.ReadDir(dir)
	for _, f := range old {
		before[f.Name()] = true
	}
	if err := p.run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		return browser.SetDownloadBehavior(browser.SetDownloadBehaviorBehaviorAllow).WithDownloadPath(dir).Do(ctx)
	})); err != nil {
		return "", err
	}
	// A "download" that is really the browser's print dialog would block the page: note it instead.
	if err := p.eval(ctx, `window.print = () => { window.__noveePrint = true; }`, nil); err != nil {
		return "", err
	}
	if err := p.Click(ctx, ref); err != nil {
		return "", err
	}
	var printed bool
	if p.eval(ctx, `!!window.__noveePrint`, &printed); printed {
		return "", ErrPrints
	}
	// The file is complete when it has its final name and has stopped growing.
	last, size := "", int64(-1)
	for deadline := time.Now().Add(40 * time.Second); time.Now().Before(deadline) && ctx.Err() == nil; time.Sleep(500 * time.Millisecond) {
		now, _ := os.ReadDir(dir)
		for _, f := range now {
			if before[f.Name()] || strings.HasSuffix(f.Name(), ".crdownload") || strings.HasPrefix(f.Name(), ".") {
				continue
			}
			st, err := f.Info()
			if err != nil || st.Size() == 0 {
				continue
			}
			if f.Name() == last && st.Size() == size {
				return filepath.Join(dir, f.Name()), nil
			}
			last, size = f.Name(), st.Size()
		}
	}
	return "", errors.New("the site offered no file after the click")
}

// CaptureFile clicks element ref and returns the file the page builds in
// answer, when the page does not download it but shows it: a "View PDF" link
// that fetches the document, wraps it in an object address of its own and
// opens that in a new tab. The file is taken at the moment the page makes
// it. The caller closes the tab that opened.
func (p *Page) CaptureFile(ctx context.Context, ref int) ([]byte, string, error) {
	if err := p.eval(ctx, `(() => { window.__noveeFiles = [];
  if (!window.__noveeHooked) { window.__noveeHooked = true; const make = URL.createObjectURL;
    URL.createObjectURL = function (b) { try { if (b instanceof Blob) window.__noveeFiles.push(b); } catch (e) {} return make.call(URL, b); }; }
})()`, nil); err != nil {
		return nil, "", err
	}
	if err := p.Click(ctx, ref); err != nil {
		return nil, "", err
	}
	var f struct {
		N    int
		Type string
		Data string
	}
	for deadline := time.Now().Add(40 * time.Second); time.Now().Before(deadline) && ctx.Err() == nil; time.Sleep(700 * time.Millisecond) {
		if err := p.eval(ctx, `(async () => { const fs = window.__noveeFiles || []; if (!fs.length) return {n: 0};
  const b = fs[fs.length - 1], u = new Uint8Array(await b.arrayBuffer()); let s = '';
  for (let i = 0; i < u.length; i += 0x8000) s += String.fromCharCode.apply(null, u.subarray(i, i + 0x8000));
  return {n: u.length, type: b.type, data: btoa(s)}; })()`, &f); err != nil {
			return nil, "", err
		}
		if f.N > 0 {
			data, err := base64.StdEncoding.DecodeString(f.Data)
			return data, f.Type, err
		}
	}
	return nil, "", errors.New("the page made no file after the click")
}
