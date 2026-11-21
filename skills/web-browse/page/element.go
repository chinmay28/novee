// Package page reads and operates one browser tab in terms a small language
// model can follow: a page is a title, some text, and a numbered list of the
// things that can be clicked or typed into.
package page

import (
	"fmt"
	"strings"
)

// Element is one thing on the page that can be acted on. Ref is its number
// in the latest snapshot.
type Element struct {
	Ref   int    `json:"ref"`
	Kind  string `json:"kind"` // link, button, input, select, textarea, checkbox, other
	Type  string `json:"type"` // the input type, if any
	Name  string `json:"name"` // what a person would call it
	Value string `json:"value"`
	Host  string `json:"host"` // for a link that leaves the site: where it goes
	Href  string `json:"href"` // for a link: its full address
	// InView is true when the element is on or just below the visible screen.
	InView  bool     `json:"inView"`
	Options []string `json:"options"`
	// Context tells apart elements with the same name ("View Receipt" ten
	// times): the text of the list entry or card the element sits in.
	Context string `json:"context"`
	// Facts the guard needs.
	Password bool `json:"password"` // a password field
	Payment  bool `json:"payment"`  // a card, bank or identity number field
	Submit   bool `json:"submit"`   // sends a form
	Search   bool `json:"search"`   // belongs to a search box
}

// String is the line shown to the model.
func (e Element) String() string {
	kind := e.Kind
	if e.Kind == "input" && e.Type != "" && e.Type != "text" {
		kind += "(" + e.Type + ")"
	}
	s := fmt.Sprintf("[%d] %s %q", e.Ref, kind, e.Name)
	if e.Value != "" {
		s += fmt.Sprintf(" value=%q", e.Value)
	}
	if len(e.Options) > 0 {
		s += " options: " + strings.Join(e.Options, " | ")
	}
	if e.Host != "" {
		s += " (goes to " + e.Host + ")"
	}
	if e.Context != "" {
		s += " (for: " + e.Context + ")"
	}
	return s
}

// Snapshot is what a page looks like right now.
type Snapshot struct {
	Title    string    `json:"title"`
	URL      string    `json:"url"`
	Text     string    `json:"text"` // the readable text of the whole page
	Elements []Element `json:"elements"`
	Dialog   bool      `json:"dialog"` // a pop-up is open; Text and Elements are the pop-up's
	ScrollY  int       `json:"scrollY"`
	Height   int       `json:"height"` // full page height in pixels
	ViewH    int       `json:"viewH"`
}

// describe is the JavaScript both snapshot and lookup use to turn a DOM
// element into an Element.
const describe = `
const __clean = s => (s || '').replace(/\s+/g, ' ').trim();
// The pop-up that is open on top of the page, if any.
const __dialog = () => {
  const ds = Array.from(document.querySelectorAll('dialog[open],[role=dialog],[role=alertdialog],[aria-modal=true]')).filter(d => {
    const r = d.getBoundingClientRect(), cs = getComputedStyle(d);
    return r.width > 50 && r.height > 50 && cs.visibility !== 'hidden' && cs.display !== 'none' && !d.closest('[aria-hidden=true]');
  });
  return ds.length ? ds[ds.length - 1] : null;
};
const __nv = (el, ref) => {
  const tag = el.tagName.toLowerCase(), role = el.getAttribute('role') || '';
  const type = (el.getAttribute('type') || '').toLowerCase();
  let kind = 'other';
  if (tag === 'a' || role === 'link') kind = 'link';
  else if (tag === 'select' || role === 'combobox' && tag !== 'input') kind = 'select';
  else if (tag === 'textarea' || el.isContentEditable) kind = 'textarea';
  else if (type === 'checkbox' || type === 'radio' || role === 'checkbox' || role === 'radio') kind = 'checkbox';
  else if (tag === 'button' || role === 'button' || ['submit', 'button', 'image', 'reset'].includes(type)) kind = 'button';
  else if (tag === 'input') kind = 'input';
  const label = el.labels && el.labels[0] ? el.labels[0].innerText : '';
  const img = el.querySelector && el.querySelector('img[alt]');
  const name = __clean(el.getAttribute('aria-label') || label || el.innerText ||
    (kind === 'button' ? el.value : '') || el.getAttribute('placeholder') || el.getAttribute('title') ||
    (img ? img.alt : '') || el.getAttribute('name') || '').slice(0, 80);
  const ids = [el.getAttribute('name'), el.id, el.getAttribute('placeholder'), el.getAttribute('aria-label'), el.getAttribute('autocomplete')].join(' ');
  const form = el.form || el.closest('form');
  const searchForm = !!form && (form.getAttribute('role') === 'search' || !!form.querySelector('input[type=search]') ||
    /search/i.test((form.getAttribute('action') || '') + ' ' + (form.id || '') + ' ' + (form.getAttribute('name') || '')));
  const r = el.getBoundingClientRect();
  let value = '';
  if (kind === 'checkbox') value = (el.checked || el.getAttribute('aria-checked') === 'true') ? 'checked' : 'not checked';
  else if (kind === 'select' && tag === 'select') value = __clean(el.selectedOptions[0] ? el.selectedOptions[0].text : '');
  else if ((kind === 'input' || kind === 'textarea') && type !== 'password') value = __clean(el.value || '').slice(0, 60);
  let host = '', href = '';
  if (kind === 'link' && el.href) { try { const u = new URL(el.href); href = u.href; if (u.host && u.host !== location.host) host = u.host; } catch (e) {} }
  return {
    ref: ref, kind: kind, type: type, name: name, value: value, host: host, href: href,
    inView: r.bottom > -50 && r.top < innerHeight * 1.5,
    options: tag === 'select' ? Array.from(el.options).slice(0, 20).map(o => __clean(o.text)) : [],
    password: type === 'password',
    payment: /^cc-/.test(el.getAttribute('autocomplete') || '') || /card.?(num|no)|cardnumber|cvv|cvc|security.?code|ssn|social.?sec|routing|iban|account.?(num|no)/i.test(ids),
    submit: (tag === 'button' && !!form && (type === 'submit' || type === '')) || type === 'submit' || type === 'image',
    search: type === 'search' || role === 'searchbox' || searchForm || (kind === 'input' && /search|query|keyword|(^| )q( |$)/i.test(ids)),
  };
};`

const snapshotJS = `(() => {` + describe + `
  const sel = 'a[href],button,input:not([type=hidden]),select,textarea,summary,[role=button],[role=link],[role=tab],' +
    '[role=menuitem],[role=checkbox],[role=radio],[role=option],[role=combobox],[role=searchbox],[onclick],[contenteditable=true]';
  document.querySelectorAll('[data-novee-ref]').forEach(e => e.removeAttribute('data-novee-ref'));
  const dlg = __dialog();
  const out = [], nodes = []; let n = 0;
  for (const el of (dlg || document).querySelectorAll(sel)) {
    const r = el.getBoundingClientRect(), cs = getComputedStyle(el);
    if (r.width < 1 || r.height < 1 || cs.visibility === 'hidden' || cs.display === 'none' || el.disabled ||
        el.closest('[aria-hidden=true]')) continue;
    const d = __nv(el, ++n);
    if (!d.name && d.kind === 'link') { n--; continue; }
    el.setAttribute('data-novee-ref', n);
    out.push(d); nodes.push(el);
  }
  const root = dlg || document.querySelector('main') || document.body;
  // Elements that share a name get the text of the smallest block that holds only one of them.
  const groups = {};
  out.forEach((d, i) => { if (d.name) (groups[d.kind + '|' + d.name] = groups[d.kind + '|' + d.name] || []).push(i); });
  for (const idx of Object.values(groups)) {
    if (idx.length < 2) continue;
    for (const i of idx) {
      let box = nodes[i];
      while (box.parentElement && box.parentElement !== root && box.parentElement !== document.body &&
             idx.filter(j => box.parentElement.contains(nodes[j])).length === 1) box = box.parentElement;
      if (box === nodes[i]) continue;
      const ctx = __clean((box.innerText || '').split(out[i].name).join(' '));
      if (ctx) out[i].context = ctx.slice(0, 90);
    }
  }
  const text = (root ? root.innerText : '').split('\n').map(__clean).filter(x => x).join('\n');
  return {title: document.title, url: location.href, text: text, elements: out, dialog: !!dlg,
    scrollY: Math.round(scrollY), height: document.documentElement.scrollHeight, viewH: innerHeight};
})()`

func lookupJS(ref int) string {
	return fmt.Sprintf(`(() => {%s
  const el = document.querySelector('[data-novee-ref="%d"]');
  return el ? __nv(el, %d) : null;
})()`, describe, ref, ref)
}
