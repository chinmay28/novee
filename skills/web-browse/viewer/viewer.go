// Package viewer lets the owner sign in to a site in Novee's browser from his
// own phone or PC.
//
// The agent never types a password. When a page asks for one, a viewer is
// started: a small web server on the home network that shows the browser's
// screen and passes on taps and keystrokes. The owner gets a link, signs in
// there, and taps Done. What he types goes from his device to this process
// and into the browser; it is not printed, logged or shown to the agent.
//
// The link carries a random token and stops working when the owner taps
// Done, when the browser closes, or after the time limit.
package viewer

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/chinmay28/novee/skills/web-browse/page"
	"github.com/chinmay28/novee/skills/web-browse/session"
)

// Info describes the running viewer.
type Info struct {
	Port    int       `json:"port"`
	Pid     int       `json:"pid"`
	Token   string    `json:"token"`
	Site    string    `json:"site"`
	Started time.Time `json:"started"`
	Until   time.Time `json:"until"`
}

func file() string { return filepath.Join(session.Dir(), "viewer.json") }

// Current returns the running viewer, if any.
func Current() (*Info, bool) {
	data, err := os.ReadFile(file())
	if err != nil {
		return nil, false
	}
	var in Info
	if json.Unmarshal(data, &in) != nil || in.Pid == 0 || syscall.Kill(in.Pid, 0) != nil || time.Now().After(in.Until) {
		return nil, false
	}
	return &in, true
}

// Stop ends the running viewer, if any.
func Stop() {
	if in, ok := Current(); ok {
		_ = syscall.Kill(in.Pid, syscall.SIGTERM)
		for i := 0; i < 50 && syscall.Kill(in.Pid, 0) == nil; i++ {
			time.Sleep(100 * time.Millisecond)
		}
	}
	_ = os.Remove(file())
}

// Link is the address the owner opens. The host is $NOVEE_BROWSE_VIEW_HOST or
// this machine's address on the network it reaches the internet through.
func (in *Info) Link() string {
	host := os.Getenv("NOVEE_BROWSE_VIEW_HOST")
	if host == "" {
		host = "127.0.0.1"
		if c, err := net.Dial("udp", "192.0.2.1:9"); err == nil { // no packet is sent
			host = c.LocalAddr().(*net.UDPAddr).IP.String()
			c.Close()
		}
	}
	return fmt.Sprintf("http://%s/?t=%s", net.JoinHostPort(host, strconv.Itoa(in.Port)), in.Token)
}

//go:embed viewer.html
var pageHTML string

var pageTmpl = template.Must(template.New("viewer").Parse(pageHTML))

type server struct {
	mu    sync.Mutex // one browser command at a time
	port  int        // the browser's debugging port
	pg    *page.Page
	id    string // the tab pg is attached to
	info  Info
	done  chan struct{}
	close sync.Once
	w, h  int // the size the page was last laid out for, if the owner's screen is small
}

// tab returns the attached tab. It follows the browser to a new tab or
// window: "Continue with Google" and the like open the sign-in in one.
func (s *server) tab() (*page.Page, error) {
	in, ok := session.Current()
	if !ok {
		return nil, errors.New("the browser is closed")
	}
	id, err := session.PageTarget(in)
	if err != nil {
		return nil, err
	}
	if s.pg != nil && id == s.id {
		return s.pg, nil
	}
	s.w, s.h = 0, 0
	s.pg, err = page.Attach(in.Port, id)
	s.id = id
	return s.pg, err
}

// with runs f on the tab, with a time limit, and forgets the tab on failure.
func (s *server) with(f func(context.Context, *page.Page) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	pg, err := s.tab()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := f(ctx, pg); err != nil {
		s.pg = nil
		return err
	}
	session.Touch() // the owner is using the browser: keep it open
	return nil
}

func (s *server) authed(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("t")), []byte(s.info.Token)) != 1 {
			http.Error(w, "This sign-in link is not valid (any more). Ask Novee for a new one.", http.StatusForbidden)
			return
		}
		h(w, r)
	}
}

func (s *server) index(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = pageTmpl.Execute(w, map[string]any{"Token": s.info.Token, "Site": strings.TrimPrefix(s.info.Site, "live:"), "Live": strings.HasPrefix(s.info.Site, "live:"),
		"Minutes": int(time.Until(s.info.Until).Minutes())})
}

func (s *server) frame(w http.ResponseWriter, r *http.Request) {
	var img []byte
	var title, url string
	var password bool
	// A phone says how big its screen is, and the page is laid out to fit it.
	w0, _ := strconv.Atoi(r.URL.Query().Get("w"))
	h0, _ := strconv.Atoi(r.URL.Query().Get("h"))
	err := s.with(func(ctx context.Context, pg *page.Page) (err error) {
		if w0 >= 320 && w0 < 1000 && h0 >= 300 && h0 < 2000 && (w0 != s.w || h0 != s.h) {
			if err := pg.Resize(ctx, w0, h0); err != nil {
				return err
			}
			s.w, s.h = w0, h0
			time.Sleep(400 * time.Millisecond) // let the page lay itself out again
		}
		if title, url, password, err = pg.Where(ctx); err != nil {
			return err
		}
		img, err = pg.JPEG(ctx)
		return err
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("X-Page-Title", strconv.QuoteToASCII(title))
	w.Header().Set("X-Page-Url", strconv.QuoteToASCII(url))
	w.Header().Set("X-Page-Password", strconv.FormatBool(password))
	_, _ = w.Write(img)
}

// event is one thing the owner did in the viewer.
type event struct {
	Type string  `json:"type"` // click, text, key, scroll, reload, done
	X    float64 `json:"x"`    // click: fraction of the screen width
	Y    float64 `json:"y"`
	Text string  `json:"text"`
	Key  string  `json:"key"`
	By   float64 `json:"by"` // scroll: screens, negative is up
}

func (s *server) input(w http.ResponseWriter, r *http.Request) {
	var e event
	if r.Method != http.MethodPost || json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&e) != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if e.Type == "done" {
		fmt.Fprintln(w, "ok")
		s.close.Do(func() { close(s.done) })
		return
	}
	err := s.with(func(ctx context.Context, pg *page.Page) error {
		switch e.Type {
		case "click":
			if e.X < 0 || e.X > 1 || e.Y < 0 || e.Y > 1 {
				return errors.New("click outside the screen")
			}
			return pg.ClickAt(ctx, e.X, e.Y)
		case "text":
			return pg.Insert(ctx, e.Text)
		case "key":
			return pg.Press(ctx, e.Key)
		case "reload":
			return pg.Reload(ctx)
		case "scroll":
			return pg.ScrollBy(ctx, max(-3, min(3, e.By)))
		}
		return fmt.Errorf("unknown event %q", e.Type)
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	fmt.Fprintln(w, "ok")
}

// Run serves the viewer until the owner is done, the browser closes, the time
// limit passes or the process is told to stop. It returns how it ended:
// "done", "expired", "closed" or "stopped".
func Run(site string, limit time.Duration, stop <-chan os.Signal) (string, error) {
	if _, ok := session.Current(); !ok {
		return "", errors.New("no browser is open")
	}
	port, _ := strconv.Atoi(os.Getenv("NOVEE_BROWSE_VIEW_PORT"))
	if os.Getenv("NOVEE_BROWSE_VIEW_PORT") == "" {
		port = 7717
	}
	l, err := net.Listen("tcp", ":"+strconv.Itoa(port))
	if err != nil { // the usual port is taken: any free one will do
		if l, err = net.Listen("tcp", ":0"); err != nil {
			return "", err
		}
	}
	tok := make([]byte, 16)
	if _, err := rand.Read(tok); err != nil {
		return "", err
	}
	s := &server{done: make(chan struct{}), info: Info{Port: l.Addr().(*net.TCPAddr).Port, Pid: os.Getpid(),
		Token: hex.EncodeToString(tok), Site: site, Started: time.Now(), Until: time.Now().Add(limit)}}
	mux := http.NewServeMux()
	mux.HandleFunc("/{$}", s.authed(s.index))
	mux.HandleFunc("/frame", s.authed(s.frame))
	mux.HandleFunc("/input", s.authed(s.input))
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go srv.Serve(l)

	data, _ := json.Marshal(s.info)
	if err := os.WriteFile(file(), data, 0o600); err != nil {
		return "", err
	}
	defer os.Remove(file())
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	tick := time.NewTicker(3 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-s.done:
			return "done", nil
		case <-stop:
			return "stopped", nil
		case <-tick.C:
			if time.Now().After(s.info.Until) {
				return "expired", nil
			}
			if _, ok := session.Current(); !ok {
				return "closed", nil
			}
		}
	}
}
