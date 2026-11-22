package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// hit is one torrent in a list of results.
type hit struct {
	Name     string `json:"name"`
	Details  string `json:"details"`  // address of its page
	Download string `json:"download"` // address of its .torrent file
	Magnet   bool   `json:"magnet"`   // the row offers only a magnet link
	Size     string `json:"size"`
	Seeds    int    `json:"seeds"` // -1 when the site does not say
	Leech    int    `json:"leech"`
	Snatched int    `json:"snatched"`
	Added    string `json:"added"`
	Category string `json:"category"`
	Free     bool   `json:"free"`
}

// listing is what a results page says.
type listing struct {
	Title    string `json:"title"`
	URL      string `json:"url"`
	Password bool   `json:"password"` // the page asks for a sign-in
	Hits     []hit  `json:"hits"`
}

// extractJS reads a torrent site's list of results without knowing the site.
//
// Every such list is rows that each hold a link to a .torrent file (or to the
// torrent's page), a size, and counts of seeders and leechers under headings
// that say so in words, letters or little pictures. So: find the download
// links, take the row around each, name the columns from the header row, and
// where a header says nothing fall back on what the cells look like.
const extractJS = `((WORDS) => {
  const clean = s => (s || '').replace(/\s+/g, ' ').trim();
  const dlRe = /(download\.php|\/download\/|\/download\?|\/dl\/|\.torrent(\?|$)|action=download|torrents\/download|\/get\/)/i;
  const detRe = /(details?\.php|torrent[-_]?details|torrents?\.php\?.*\bid=|\/torrents?\/\d+|\/torrent\/[^/]+|\/t\/\d+|\/details\/)/i;
  // Links in a row that are about something else than the torrent itself: its uploader, its peers, its category.
  const otherRe = /(user|member|profile|peers|history|categor|comment|imdb\.com|tmdb|tvdb|page=torrents&|[?&]cat=)/i;
  const sizeRe = /(\d+(?:[.,]\d+)?)\s*(bytes|B|KB|MB|GB|TB|KiB|MiB|GiB|TiB)\b/i;
  const rowOf = a => a.closest('tr') || a.closest('li, article, [class*=torrent], [class*=result], [class*=row]');
  const rows = new Map();
  for (const a of document.querySelectorAll('a[href]')) {
    const h = a.getAttribute('href') || '';
    if (dlRe.test(h) && !/^magnet:/i.test(h)) { const r = rowOf(a); if (r && !rows.has(r)) rows.set(r, {dl: a}); }
  }
  for (const a of document.querySelectorAll('a[href^="magnet:"]')) { const r = rowOf(a); if (r && !rows.has(r)) rows.set(r, {magnet: true}); }
  if (rows.size === 0) // no download links in the list: rows that link to a torrent's page and state a size
    for (const a of document.querySelectorAll('a[href]')) {
      if (!detRe.test(a.getAttribute('href') || '')) continue;
      const r = rowOf(a); if (r && !rows.has(r) && sizeRe.test(r.innerText || '')) rows.set(r, {});
    }
  // What a header cell is about: its text, or the title or alt of the picture in it.
  const label = c => clean([c.innerText, c.getAttribute('title'), c.getAttribute('aria-label'),
    ...Array.from(c.querySelectorAll('[title],[alt],[aria-label]')).map(e => e.getAttribute('title') || e.getAttribute('alt') || e.getAttribute('aria-label')),
    ...Array.from(c.querySelectorAll('[class]')).map(e => e.className && e.className.baseVal === undefined ? e.className : '')].join(' ')).toLowerCase();
  const headers = new Map();
  const colsOf = table => {
    if (headers.has(table)) return headers.get(table);
    const m = {};
    const head = table.querySelector('thead tr') || Array.from(table.querySelectorAll('tr')).find(r => r.querySelector('th')) || table.querySelector('tr');
    if (head) Array.from(head.children).forEach((c, i) => {
      const t = label(c), w = clean(c.innerText).toLowerCase();
      const set = k => { if (m[k] === undefined) m[k] = i; };
      // A date column first: its header often carries a sort arrow, which says nothing about peers.
      if (/added|adddate|age|date|uploaded|\btime\b|when/.test(t)) set('added');
      else if (/size/.test(t)) set('size');
      else if (/seed|\bup\b|uploaders|arrow-?up/.test(t) || ['s', 'se', '▲', '↑'].includes(w)) set('seeds');
      else if (/leech|peers|\bdown\b|downloaders|arrow-?down/.test(t) || ['l', 'le', '▼', '↓'].includes(w)) set('leech');
      else if (/snatch|complet|grabs|times|downloaded|finished|\bcheck/.test(t) || w === 'c') set('snatched');
      else if (/categ|\btype\b|\bcat\b/.test(t)) set('category');
      else if (/name|title|torrent/.test(t)) set('name');
    });
    headers.set(table, m); return m;
  };
  const int = s => { const m = clean(s).replace(/[,.\s](?=\d{3}\b)/g, '').match(/^\d+$/); return m ? parseInt(m[0], 10) : -1; };
  // A page often shows other lists too (featured, recommended, newest). The results are the list
  // whose rows mention the searched words most; with no words, the longest list.
  const groups = new Map();
  for (const row of rows.keys()) {
    const g = row.closest('table') || row.parentElement, low = (row.innerText || '').toLowerCase();
    const n = groups.get(g) || 0;
    groups.set(g, n + 1 + 1000 * (WORDS || []).filter(w => low.includes(w)).length);
  }
  let main = null, most = 0;
  for (const [g, n] of groups) if (n >= most) { main = g; most = n; } // on a tie the later list: results come below what is featured
  for (const row of Array.from(rows.keys())) if ((row.closest('table') || row.parentElement) !== main) rows.delete(row);
  const hits = [];
  for (const [row, how] of rows) {
    if (row.querySelector('th') && !row.querySelector('td')) continue;
    const cells = Array.from(row.children), table = row.closest('table'), cols = table ? colsOf(table) : {};
    const cell = k => cols[k] !== undefined && cells[cols[k]] ? cells[cols[k]] : null;
    // The name is the longest link text that leads to the torrent's own page.
    let name = '', details = '', best = null;
    for (const a of row.querySelectorAll('a[href]')) {
      const h = a.getAttribute('href') || '', t = clean(a.innerText || a.getAttribute('title'));
      if (dlRe.test(h) || /^magnet:/i.test(h) || t.length < 3) continue;
      const score = l => l.t.length + (detRe.test(l.h) ? 1000 : 0) - (otherRe.test(l.h) ? 2000 : 0);
      if (!best || score({t: t, h: h}) > score(best)) best = {t: t, h: h, href: a.href};
    }
    if (best) { name = best.t; details = best.href; }
    if (!name) continue;
    let size = '';
    const sc = cell('size');
    if (sc && sizeRe.test(sc.innerText)) size = clean(sc.innerText.match(sizeRe)[0]);
    else for (const c of cells) { const m = (c.innerText || '').match(sizeRe); if (m && clean(c.innerText).length < 20) { size = clean(m[0]); break; } }
    let seeds = cell('seeds') ? int(cell('seeds').innerText) : -1, leech = cell('leech') ? int(cell('leech').innerText) : -1;
    if (seeds < 0 && cols.seeds === undefined) { // no header says: the bare numbers after the size are seeders, then leechers
      const nums = cells.map(c => int(c.innerText)), from = cells.findIndex(c => sizeRe.test(c.innerText || '') && clean(c.innerText).length < 20);
      const after = nums.slice(from + 1).filter(n => n >= 0);
      if (from >= 0 && after.length >= 2) { seeds = after[after.length - 2]; leech = after[after.length - 1]; }
    }
    const cat = cell('category');
    hits.push({name: name.slice(0, 200), details: details, download: how.dl ? how.dl.href : '', magnet: !!how.magnet && !how.dl, size: size,
      seeds: seeds, leech: leech, snatched: cell('snatched') ? int(cell('snatched').innerText) : -1,
      added: cell('added') ? clean(cell('added').innerText).slice(0, 40) : '',
      category: cat ? clean(cat.innerText || (cat.querySelector('[alt],[title]') || {getAttribute: () => ''}).getAttribute('alt') || (cat.querySelector('[title]') || {getAttribute: () => ''}).getAttribute('title')).slice(0, 40) : '',
      free: /free\s?leech|\bfreeleech\b|\b100% free\b|class="[^"]*\bfree\b/i.test(row.outerHTML)});
  }
  const password = Array.from(document.querySelectorAll('input[type=password]')).some(e => e.offsetParent !== null);
  return {title: document.title, url: location.href, password: password, hits: hits};
})`

// extractor is the script that reads the page's list; words are what was
// searched for, if anything, and decide which list on the page is the answer.
func extractor(words string) string {
	var ws []string
	for _, w := range strings.Fields(strings.ToLower(words)) {
		if len(w) >= 3 {
			ws = append(ws, w)
		}
	}
	list, _ := json.Marshal(ws)
	return extractJS + "(" + string(list) + ")"
}

var sizeUnit = regexp.MustCompile(`(?i)^(\d+(?:[.,]\d+)?)\s*(bytes|B|KB|MB|GB|TB|KiB|MiB|GiB|TiB)$`)

// bytesOf turns "4.3 GB" into a number to sort by.
func bytesOf(size string) float64 {
	m := sizeUnit.FindStringSubmatch(strings.TrimSpace(size))
	if m == nil {
		return 0
	}
	v, _ := strconv.ParseFloat(strings.Replace(m[1], ",", ".", 1), 64)
	switch strings.ToLower(m[2])[:1] {
	case "k":
		v *= 1 << 10
	case "m":
		v *= 1 << 20
	case "g":
		v *= 1 << 30
	case "t":
		v *= 1 << 40
	}
	return v
}

// filter keeps the hits that have every word in must, none in exclude, and enough seeders.
func filter(hits []hit, must, exclude []string, minSeeds int) []hit {
	var out []hit
next:
	for _, h := range hits {
		low := strings.ToLower(h.Name)
		for _, w := range must {
			if !strings.Contains(low, strings.ToLower(w)) {
				continue next
			}
		}
		for _, w := range exclude {
			if strings.Contains(low, strings.ToLower(w)) {
				continue next
			}
		}
		if h.Seeds >= 0 && h.Seeds < minSeeds {
			continue
		}
		out = append(out, h)
	}
	return out
}

// Release names write a picture quality in more than one way.
var qualities = map[string]*regexp.Regexp{
	"2160p": regexp.MustCompile(`(?i)\b(2160p?|4k)\b`),
	"1080p": regexp.MustCompile(`(?i)\b1080[pi]?\b`),
	"720p":  regexp.MustCompile(`(?i)\b720p?\b`),
	"480p":  regexp.MustCompile(`(?i)\b(480p?|sd)\b`),
}

// ofQuality keeps the hits whose name states one of the qualities. "4k" and
// "uhd" asked for mean 2160p; a name that says "1080p UHD" is a 1080p copy
// made from a UHD disc, so "uhd" alone in a name does not count as 2160p.
func ofQuality(hits []hit, want []string) []hit {
	if len(want) == 0 {
		return hits
	}
	var res []*regexp.Regexp
	for _, q := range want {
		q = strings.ToLower(strings.TrimSpace(q))
		switch q {
		case "4k", "uhd", "2160":
			q = "2160p"
		case "1080", "fhd", "1080i":
			q = "1080p"
		case "720", "hd":
			q = "720p"
		}
		if re, ok := qualities[q]; ok {
			res = append(res, re)
		} else {
			res = append(res, regexp.MustCompile(`(?i)`+regexp.QuoteMeta(q)))
		}
	}
	var out []hit
	for _, h := range hits {
		for _, re := range res {
			if re.MatchString(h.Name) {
				out = append(out, h)
				break
			}
		}
	}
	return out
}

// ladder is the qualities from best to least.
var ladder = []string{"2160p", "1080p", "720p", "480p"}

// nextBest returns the hits of the best quality below the one asked for that
// the list has at all, and that quality's name.
func nextBest(hits []hit, asked []string) (string, []hit) {
	if len(asked) == 0 {
		return "", nil
	}
	from := -1
	for _, q := range asked {
		q = strings.ToLower(q)
		if q == "4k" || q == "uhd" || q == "2160" {
			q = "2160p"
		}
		for i, l := range ladder {
			if strings.TrimSuffix(l, "p") == strings.TrimSuffix(q, "p") && i > from {
				from = i
			}
		}
	}
	if from < 0 {
		return "", nil
	}
	for _, l := range ladder[from+1:] {
		if got := ofQuality(hits, []string{l}); len(got) > 0 {
			return l, got
		}
	}
	return "", nil
}

// rank orders hits: by "seeds" (the default; most seeders first, then most
// completed downloads), "size" (largest first), "small" (smallest first),
// "new" (as the site listed them) or "name".
func rank(hits []hit, by string) {
	switch by {
	case "new":
	case "size":
		sort.SliceStable(hits, func(i, j int) bool { return bytesOf(hits[i].Size) > bytesOf(hits[j].Size) })
	case "small":
		sort.SliceStable(hits, func(i, j int) bool { return bytesOf(hits[i].Size) < bytesOf(hits[j].Size) })
	case "name":
		sort.SliceStable(hits, func(i, j int) bool { return strings.ToLower(hits[i].Name) < strings.ToLower(hits[j].Name) })
	default:
		sort.SliceStable(hits, func(i, j int) bool {
			if hits[i].Seeds != hits[j].Seeds {
				return hits[i].Seeds > hits[j].Seeds
			}
			return hits[i].Snatched > hits[j].Snatched
		})
	}
}

// line is how a hit reads in a list.
func (h hit) line() string {
	parts := []string{h.Name}
	if h.Size != "" {
		parts = append(parts, h.Size)
	}
	switch {
	case h.Seeds >= 0 && h.Leech >= 0:
		parts = append(parts, fmt.Sprintf("%d seeders, %d leechers", h.Seeds, h.Leech))
	case h.Seeds >= 0:
		parts = append(parts, fmt.Sprintf("%d seeders", h.Seeds))
	default:
		parts = append(parts, "seeders not shown")
	}
	if h.Snatched >= 0 {
		parts = append(parts, fmt.Sprintf("completed %d times", h.Snatched))
	}
	if h.Added != "" {
		parts = append(parts, "added "+h.Added)
	}
	if h.Category != "" {
		parts = append(parts, h.Category)
	}
	if h.Free {
		parts = append(parts, "freeleech")
	}
	if h.Magnet {
		parts = append(parts, "magnet link only, no .torrent file")
	}
	return strings.Join(parts, " | ")
}
