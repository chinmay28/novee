package main

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/chinmay28/novee/skills/web-browse/page"
	"github.com/chinmay28/novee/skills/web-browse/session"
)

// The test binary doubles as the browser's keeper process.
func TestMain(m *testing.M) {
	if len(os.Args) == 5 && os.Args[1] == "_keeper" {
		port, _ := strconv.Atoi(os.Args[3])
		if session.RunKeeper(os.Args[2], port, os.Args[4], 20*time.Minute) != nil {
			os.Exit(1)
		}
		return
	}
	os.Exit(m.Run())
}

type row struct {
	id                     int
	name, size             string
	seeds, leech, snatched int
	magnet                 bool
}

var shouted int

var catalogue = []row{
	{1, "Ubuntu 26.04 Desktop amd64 1080p", "5.8 GB", 12, 3, 140, false},
	{2, "Ubuntu 26.04 Server amd64", "2.1 GB", 310, 4, 9000, false},
	{3, "Ubuntu 24.04 Desktop amd64", "4.9 GB", 0, 1, 77, false},
	{4, "Ubuntu Studio 26.04", "6.5 GB", 45, 0, 300, true},
	{5, "Debian 14 netinst", "780 MB", 88, 2, 1200, false},
}

func torrentFile(name string) []byte {
	return []byte(fmt.Sprintf("d8:announce15:http://t.test/a4:infod6:lengthi5e4:name%d:%s12:piece lengthi16384eee", len(name), name))
}

// tracker is a private torrent site: a login page until the visitor has the
// cookie, a search box, and a table whose headings are words, letters and pictures.
func tracker() http.Handler {
	mux := http.NewServeMux()
	in := func(r *http.Request) bool { c, err := r.Cookie("sid"); return err == nil && c.Value == "ok" }
	login := `<title>Login</title><form><input name="user"><input type="password" name="pass"><button>Log in</button></form>`
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if !in(r) {
			fmt.Fprint(w, login)
			return
		}
		// Like many trackers: the front page (and any address it does not know) lists the newest
		// torrents, has a chat box, and keeps the search on the Torrents page.
		fmt.Fprint(w, `<title>Tracker</title><main><h1>Welcome</h1><a href="/torrents-page">Torrents</a>
<form action="/shout"><input name="chatbarText" value="This is a Shout Box not a Search engine!!"></form>
<table><tr><th>Name</th><th>DL</th><th>Size</th><th>S</th><th>L</th></tr>
<tr><td><a href="/details.php?id=90">Old Release 1978</a></td><td><a href="/download.php?id=1">DL</a></td><td>57 GB</td><td>3</td><td>0</td></tr></table></main>`)
	})
	mux.HandleFunc("/shout", func(w http.ResponseWriter, r *http.Request) { shouted++ })
	mux.HandleFunc("/torrents-page", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<title>Torrents</title><main><form action="/browse.php"><input name="search"><input type="hidden" name="active" value="1"><button>Search</button></form></main>`)
	})
	mux.HandleFunc("/dologin", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "sid", Value: "ok", Path: "/"})
		fmt.Fprint(w, `<title>Tracker</title><main>Signed in</main>`)
	})
	mux.HandleFunc("/browse.php", func(w http.ResponseWriter, r *http.Request) {
		if !in(r) {
			fmt.Fprint(w, login)
			return
		}
		q := strings.ToLower(r.URL.Query().Get("search"))
		b := &strings.Builder{}
		// Above the results, a short list of featured torrents that has nothing to do with the search.
		b.WriteString(`<title>Browse</title><main><table><tr><th>Name</th><th>DL</th><th>Size</th><th>S</th><th>L</th></tr>
<tr><td><a href="/index.php?page=torrent-details&id=aa11">Featured Ubuntu Remix</a></td><td><a href="/download.php?id=1">DL</a></td><td>80 GB</td><td>999</td><td>0</td></tr></table>`)
		b.WriteString(`<table><tr><th>Type</th><th>Name</th><th>DL</th><th>AddDate ↓</th><th>Size</th><th><img alt="Snatched" src="x"></th><th>S</th><th><img title="Leechers" src="x"></th></tr>`)
		pageNo, _ := strconv.Atoi(r.URL.Query().Get("pages"))
		shown, matched := 0, 0
		for _, t := range catalogue {
			if !strings.Contains(strings.ToLower(t.name), q) {
				continue
			}
			if matched++; matched <= pageNo*3 || shown == 3 { // three to a page
				continue
			}
			shown++
			dl := fmt.Sprintf(`<a href="/download.php?id=%d">DL</a>`, t.id)
			if t.magnet {
				dl = `<a href="magnet:?xt=urn:btih:abc">M</a>`
			}
			fmt.Fprintf(b, `<tr><td>Apps</td><td><a href="/index.php?page=torrent-details&id=e%dc154647">%s</a> <a href="/index.php?page=userdetails&id=255718">an-uploader-with-a-long-name-to-outweigh-the-title</a></td><td>%s</td><td>May 04, 2026</td><td>%s</td><td>%d</td><td>%d</td><td>%d</td></tr>`,
				t.id, t.name, dl, t.size, t.snatched, t.seeds, t.leech)
		}
		b.WriteString(`</table>`)
		if matched > (pageNo+1)*3 {
			fmt.Fprintf(b, `<a href="/browse.php?search=%s&active=1&pages=%d">Next &gt;&gt;</a>`, url.QueryEscape(q), pageNo+1)
		}
		b.WriteString(`</main>`)
		fmt.Fprint(w, b.String())
	})
	mux.HandleFunc("/download.php", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.Atoi(r.URL.Query().Get("id"))
		if !in(r) || id < 1 || id > len(catalogue) {
			fmt.Fprint(w, login)
			return
		}
		w.Header().Set("Content-Type", "application/x-bittorrent")
		_, _ = w.Write(torrentFile(catalogue[id-1].name))
	})
	mux.HandleFunc("/index.php", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `<title>Details</title><main><h1>Torrent %s</h1><p>Files: 1. Official image.</p></main>`, r.URL.Query().Get("id"))
	})
	mux.HandleFunc("/details.php", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `<title>Details</title><main><h1>Torrent %s</h1><p>Files: 1. Official image.</p></main>`, r.URL.Query().Get("id"))
	})
	return mux
}

func TestTorrentTool(t *testing.T) {
	if _, err := session.FindExec(); err != nil {
		t.Skip("no browser installed:", err)
	}
	srv := httptest.NewServer(tracker())
	defer srv.Close()
	dir := t.TempDir()
	t.Setenv("NOVEE_TORRENT_DIR", dir)
	t.Setenv("NOVEE_BROWSE_DIR", t.TempDir())
	t.Setenv("NOVEE_BROWSER_DIR", t.TempDir())
	t.Setenv("NOVEE_BROWSE_HEADLESS", "1")
	t.Setenv("NOVEE_BROWSE_NO_SYSTEMD", "1")
	sent := filepath.Join(dir, "sent")
	notify := filepath.Join(dir, "notify")
	if err := os.WriteFile(notify, []byte("#!/bin/sh\necho \"$1|$2\" >> "+sent+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NOVEE_TORRENT_NOTIFY_CMD", notify)
	t.Cleanup(func() { session.Stop() })

	// What novee-browse does in real use: load the page in the kept browser,
	// and say that a sign-in link went out when the page asks for a password.
	open := func(ctx context.Context, addr string) (string, error) {
		in, ok := session.Current()
		if !ok {
			var err error
			if in, err = session.Start("main", session.Headless, []string{os.Args[0], "_keeper"}); err != nil {
				return "", err
			}
		}
		id, err := session.PageTarget(in)
		if err != nil {
			return "", err
		}
		p, err := page.Attach(in.Port, id)
		if err != nil {
			return "", err
		}
		if err := p.Goto(ctx, addr); err != nil {
			return "", err
		}
		var l listing
		if err := p.Eval(ctx, extractor(""), &l); err != nil {
			return "", err
		}
		if l.Password {
			return "Page: Login\nNOTE: this page asks you to sign in.\nSIGN-IN LINK SENT to Chinmay in a separate message (valid 15 minutes).\n", nil
		}
		return "Page: " + l.Title + "\n", nil
	}
	out := &bytes.Buffer{}
	run := func(args ...string) (int, string) {
		t.Helper()
		out.Reset()
		tool := &tool{out: out, open: open}
		code := tool.run(args)
		return code, out.String()
	}
	wants := func(got string, subs ...string) {
		t.Helper()
		for _, s := range subs {
			if !strings.Contains(got, s) {
				t.Errorf("output lacks %q:\n%s", s, got)
			}
		}
	}

	_, got := run("search", "ubuntu")
	wants(got, "NO SITE SET")
	_, got = run("site", srv.URL)
	wants(got, "The site is now "+srv.URL)

	// Not signed in: the sign-in link is what the model hears about.
	code, got := run("search", "ubuntu")
	if code == exitOK {
		t.Errorf("a search without a sign-in must fail: %s", got)
	}
	wants(got, "NOT SIGNED IN", "SIGN-IN LINK SENT")
	if _, err := open(context.Background(), srv.URL+"/dologin"); err != nil { // the owner signs in
		t.Fatal(err)
	}
	_, got = run("login")
	wants(got, "SIGNED IN")

	// A search through the site's own box: columns named by words, letters and pictures, best seeded first.
	_, got = run("search", "ubuntu")
	wants(got, "Verdict: 4 results", "every page of the results was read", "most seeders first",
		"1. Ubuntu 26.04 Server amd64 | 2.1 GB | 310 seeders, 4 leechers | completed 9000 times | added May 04, 2026",
		"2. Ubuntu Studio 26.04 | 6.5 GB | 45 seeders, 0 leechers | completed 300 times | added May 04, 2026 | Apps | magnet link only",
		"4. Ubuntu 24.04 Desktop amd64 | 4.9 GB | 0 seeders, 1 leechers")
	var cfg config
	if !loadJSON(configPath(), &cfg) || !strings.Contains(cfg.Search, "browse.php?search={q}&active=1") {
		t.Errorf("the search address must be learned from the site's own search box, got %q", cfg.Search)
	}
	if shouted != 0 {
		t.Error("search words must never be typed into a chat box")
	}
	// A wrong remembered address (the site answers it with its front page) is noticed and replaced.
	cfg.Search, cfg.Box = "/torrents?name={q}", false // an address that was guessed, not learned from the site
	_ = saveJSON(configPath(), cfg)
	_, got = run("search", "debian")
	wants(got, "Verdict: 1 results", "Debian 14 netinst")
	if loadJSON(configPath(), &cfg); !strings.Contains(cfg.Search, "browse.php?search={q}") {
		t.Errorf("a search address that ignores the words must be dropped, got %q", cfg.Search)
	}
	_, got = run("search", "ubuntu")
	_, got = run("search", "ubuntu", "--sort", "size", "--quality", "desktop", "--min-seeds", "1")
	wants(got, "Verdict: 1 results", "1. Ubuntu 26.04 Desktop amd64 1080p | 5.8 GB")

	// Is it out yet?
	_, got = run("check", "fedora")
	wants(got, "Verdict: NOT AVAILABLE YET", "novee-torrent watch add fedora")
	_, got = run("check", "ubuntu", "24.04")
	wants(got, "Verdict: LISTED BUT NOT SEEDED")
	_, got = run("check", "debian")
	wants(got, "Verdict: AVAILABLE", "Debian 14 netinst | 780 MB | 88 seeders")

	// One file goes as it is.
	_, got = run("get", "1")
	wants(got, "Verdict: FETCHED 1 of 1 .torrent files", "- Debian 14 netinst.torrent", "ZIP SENT to Chinmay")
	log, _ := os.ReadFile(sent)
	if !strings.Contains(string(log), "Torrent file (in a zip): Debian 14 netinst|") || !strings.Contains(string(log), "Debian 14 netinst.zip") {
		t.Errorf("the owner must get the file, zipped: %s", log)
	}

	// Several go as one zip; a magnet-only row is left out and said so.
	_, _ = run("search", "ubuntu")
	_, got = run("get", "--top", "3")
	wants(got, "NOT FETCHED: Ubuntu Studio 26.04: the site gives only a magnet link", "Verdict: FETCHED 2 of 3 .torrent files", "ZIP SENT to Chinmay in a separate message: 2 .torrent files")
	log, _ = os.ReadFile(sent)
	lines := strings.Split(strings.TrimSpace(string(log)), "\n")
	zipPath := lines[len(lines)-1][strings.LastIndex(lines[len(lines)-1], "|")+1:]
	zr, err := zip.OpenReader(zipPath)
	if err != nil || len(zr.File) != 2 {
		t.Fatalf("the zip %q: %v", zipPath, err)
	}
	for _, f := range zr.File {
		rc, _ := f.Open()
		data := &bytes.Buffer{}
		_, _ = data.ReadFrom(rc)
		rc.Close()
		if !isTorrent(data.Bytes()) {
			t.Errorf("%s in the zip is not a torrent file", f.Name)
		}
	}
	zr.Close()

	_, got = run("info", "1")
	wants(got, "Result 1: Ubuntu 26.04 Server amd64", "Official image.")
	_, got = run("grab", "debian", "--no-send")
	wants(got, "Best of 1 results", "Verdict: FETCHED 1 of 1", "NOT sent", "novee-torrent send --last 1")
	// "Send me all of them as a zip": what was fetched goes out again as one archive, by one command.
	_, got = run("fetched")
	wants(got, "most recently fetched .torrent files", "1. Debian 14 netinst (fetched ", "novee-torrent send --last K")
	_, got = run("send", "--last", "3")
	wants(got, "ZIP SENT to Chinmay in a separate message: 3 .torrent files")
	_, got = run("send")
	wants(got, "Every fetched file was already sent")
	// The site's own name among the words is not searched for.
	_, got = run("search", "debian", "on", strings.TrimPrefix(srv.URL, "http://"), "--site", srv.URL)
	wants(got, "Verdict: 1 results for \"debian\"")
	_, got = run("latest", "--limit", "2")
	wants(got, "Verdict: the 2 newest", "1. Ubuntu 26.04 Desktop amd64 1080p")

	// Waiting for something: nothing at first, then it is there.
	_, got = run("watch", "add", "fedora", "--get")
	wants(got, "WATCHING for \"fedora\"", "send him the .torrent file")
	_, got = run("watch", "run")
	wants(got, "not yet: \"fedora\"")
	catalogue = append(catalogue, row{6, "Fedora 45 Workstation", "2.4 GB", 20, 1, 50, false})
	_, got = run("watch", "run")
	wants(got, "Available now on", "Fedora 45 Workstation | 2.4 GB | 20 seeders")
	log, _ = os.ReadFile(sent)
	wants(string(log), "Available now on", "Fedora 45 Workstation.zip")
	_, got = run("watch", "list")
	wants(got, "Nothing is being watched for.")
}

func TestOnlyTorrentFiles(t *testing.T) {
	if !isTorrent(torrentFile("x")) || isTorrent([]byte("<html>login</html>")) || isTorrent([]byte("d4:spam4:eggse")) {
		t.Error("a torrent file is a bencoded dictionary with an info entry, and nothing else is")
	}
	if n := torrentName(torrentFile("Some.Release.1080p")); n != "Some.Release.1080p" {
		t.Errorf("name: %q", n)
	}
	if s := safeName("../../etc/pass\x00wd: a*b"); strings.ContainsAny(s, "/\\\x00*:") {
		t.Errorf("unsafe file name %q", s)
	}
}
