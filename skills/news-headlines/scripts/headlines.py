#!/usr/bin/env python3
"""Print today's top news headlines from RSS feeds (stdlib only, no API keys).

Examples:
  headlines.py                      # top 10 from Google News (US/English)
  headlines.py -n 5 -s bbc          # top 5 from BBC
  headlines.py -t technology        # Google News technology section
  headlines.py -q "nvidia earnings" # Google News search for a topic
  headlines.py --json               # machine-readable output
"""
import argparse, json, re, sys, urllib.request, urllib.parse, html
from datetime import datetime, timezone
from email.utils import parsedate_to_datetime
import xml.etree.ElementTree as ET

GN = "https://news.google.com/rss"
SOURCES = {
    "google":     GN + "?hl={hl}&gl={gl}&ceid={gl}:{lang}",
    "bbc":        "https://feeds.bbci.co.uk/news/rss.xml",
    "bbc-world":  "https://feeds.bbci.co.uk/news/world/rss.xml",
    "npr":        "https://feeds.npr.org/1001/rss.xml",
    "nytimes":    "https://rss.nytimes.com/services/xml/rss/nyt/HomePage.xml",
    "guardian":   "https://www.theguardian.com/world/rss",
    "aljazeera":  "https://www.aljazeera.com/xml/rss/all.xml",
    "hackernews": "https://hnrss.org/frontpage",
}
TOPICS = ["world", "nation", "business", "technology", "entertainment", "sports", "science", "health"]
UA = "Mozilla/5.0 (X11; Linux x86_64) headlines-skill/1.0"


def fetch(url, timeout):
    req = urllib.request.Request(url, headers={"User-Agent": UA, "Accept": "application/rss+xml, application/xml, text/xml, */*"})
    with urllib.request.urlopen(req, timeout=timeout) as r:
        return r.read()


def text(el, tag):
    x = el.find(tag)
    return html.unescape((x.text or "").strip()) if x is not None and x.text else ""


def parse(xml_bytes, fallback_source):
    root = ET.fromstring(xml_bytes)
    items = []
    # RSS 2.0
    for it in root.iter("item"):
        title = text(it, "title")
        link = text(it, "link")
        src = text(it, "source") or fallback_source
        # Google News titles end with " - Publisher"; strip when we know the publisher
        if src and title.endswith(" - " + src):
            title = title[: -len(src) - 3].rstrip()
        pub = text(it, "pubDate")
        desc = text(it, "description")
        related = set(re.findall(r'href="([^"]+)"', desc))
        # Google News lists other outlets covering the same story: <a>title</a> ... <font>Outlet</font>
        outlets = [html.unescape(o).strip() for o in re.findall(r"<font[^>]*>([^<]+)</font>", desc)]
        also = []
        for o in outlets:
            if o and o != src and o not in also:
                also.append(o)
        items.append({"title": title, "url": link, "source": src, "published": pub, "related": related, "also": also})
    # Atom fallback
    if not items:
        ns = {"a": "http://www.w3.org/2005/Atom"}
        for e in root.findall("a:entry", ns):
            t = e.find("a:title", ns); l = e.find("a:link", ns); u = e.find("a:updated", ns)
            items.append({"title": html.unescape((t.text or "").strip()) if t is not None else "",
                          "url": l.get("href") if l is not None else "", "source": fallback_source,
                          "published": (u.text or "") if u is not None else "", "related": set(), "also": []})
    return items


def age(pub):
    try:
        dt = parsedate_to_datetime(pub)
    except Exception:
        try:
            dt = datetime.fromisoformat(pub.replace("Z", "+00:00"))
        except Exception:
            return ""
    if dt.tzinfo is None:
        dt = dt.replace(tzinfo=timezone.utc)
    s = (datetime.now(timezone.utc) - dt).total_seconds()
    if s < 0: s = 0
    if s < 3600: return f"{int(s // 60)}m ago"
    if s < 86400: return f"{int(s // 3600)}h ago"
    return f"{int(s // 86400)}d ago"

STOP = set("""a an the of to in on for and or but as at by from with about over after before into
is are was were be been has have had will would could should says said say new amid his her their
its it he she they them this that these those up out off down more most than then vs""".split())


def norm(title):
    # unify curly quotes, drop possessives ("Trump's" -> "Trump"), expand a few common abbreviations
    t = title.replace("\u2019", "'").replace("\u2018", "'")
    t = re.sub(r"'s\b", "", t)
    t = re.sub(r"\bSCOTUS\b", "Supreme Court", t)
    t = re.sub(r"\bCalif\.", "California", t)
    return t


def words(title):
    return {w for w in re.findall(r"[a-z0-9]+", norm(title).lower()) if len(w) > 2 and w not in STOP}


def same_story(x, y):
    # shared article links (Google News lists related coverage per item)
    lx = x["related"] | ({x["url"]} if x["url"] else set())
    ly = y["related"] | ({y["url"]} if y["url"] else set())
    if lx & ly:
        return True
    wx, wy = words(x["title"]), words(y["title"])
    if not wx or not wy:
        return False
    shared = wx & wy
    jacc = len(shared) / len(wx | wy)
    # proper-noun-ish overlap: capitalised words in the original titles
    px = {w.lower() for w in re.findall(r"\b[A-Z][a-zA-Z-]{2,}", norm(x["title"]))} - STOP
    py = {w.lower() for w in re.findall(r"\b[A-Z][a-zA-Z-]{2,}", norm(y["title"]))} - STOP
    names = px & py
    return jacc >= 0.5 or (len(shared) >= 3 and len(names) >= 2) or len(names) >= 3


def cluster(items, count):
    """Collapse items about the same story; keep the earliest-listed one, note other outlets."""
    stories = []
    for it in items:
        if not it["title"]:
            continue
        for st in stories:
            if same_story(st, it):
                for o in [it["source"]] + it["also"]:
                    if o and o != st["source"] and o not in st["also"]:
                        st["also"].append(o)
                st["related"] |= it["related"]
                break
        else:
            it = dict(it); it["also"] = list(it["also"])
            stories.append(it)
            if len(stories) >= count:
                pass  # keep scanning so later duplicates still merge into existing stories
    return stories[:count]


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("-n", "--count", type=int, default=10, help="number of headlines (default 10)")
    ap.add_argument("-s", "--source", default="google", choices=sorted(SOURCES), help="feed to use (default google)")
    ap.add_argument("-t", "--topic", choices=TOPICS, help="Google News section (overrides --source)")
    ap.add_argument("-q", "--query", help="Google News search query (overrides --source/--topic)")
    ap.add_argument("--lang", default="en", help="language code for Google News (default en)")
    ap.add_argument("--country", default="US", help="country code for Google News (default US)")
    ap.add_argument("--timeout", type=float, default=15)
    ap.add_argument("--json", action="store_true", help="emit JSON instead of text")
    a = ap.parse_args()

    hl, gl, lang = f"{a.lang}-{a.country}", a.country, a.lang
    loc = f"hl={hl}&gl={gl}&ceid={gl}:{lang}"
    if a.query:
        url, label = f"{GN}/search?q={urllib.parse.quote(a.query)}&{loc}", f"Google News search: {a.query}"
    elif a.topic:
        url, label = f"{GN}/headlines/section/topic/{a.topic.upper()}?{loc}", f"Google News: {a.topic}"
    else:
        url, label = SOURCES[a.source].format(hl=hl, gl=gl, lang=lang), a.source

    try:
        items = parse(fetch(url, a.timeout), label if a.source != "google" or a.topic or a.query else "")
    except Exception as e:
        print(f"ERROR: could not fetch {label}: {e}", file=sys.stderr)
        sys.exit(1)

    out = cluster(items, a.count)

    if a.json:
        slim = [{k: v for k, v in it.items() if k != "related"} for it in out]
        json.dump({"feed": label, "fetched": datetime.now(timezone.utc).isoformat(timespec="seconds"), "count": len(slim), "items": slim}, sys.stdout, indent=1)
        print()
        return
    print(f"Top {len(out)} headlines — {label} — {datetime.now().strftime('%Y-%m-%d %H:%M')}")
    for i, it in enumerate(out, 1):
        meta = " · ".join(x for x in (it["source"], age(it["published"])) if x)
        print(f"{i}. {it['title']}" + (f"  ({meta})" if meta else ""))
        if it["also"]:
            print(f"   also: {', '.join(it['also'][:4])}" + (f" +{len(it['also'])-4} more" if len(it["also"]) > 4 else ""))
        if it["url"]:
            print(f"   {it['url']}")
    if not out:
        print("No items found in feed.", file=sys.stderr); sys.exit(2)


if __name__ == "__main__":
    main()
