// Package session keeps one browser alive between commands.
//
// Each novee-browse command is a separate short process, but a web page has
// state (where you are, what you typed), so the browser itself must outlive
// them. Start launches a detached "keeper" process that owns a Chromium with
// a local debugging port; later commands find it through a session file and
// attach. The keeper closes the browser after a period without commands, so a
// signed-in browser is never left running by accident.
package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Info describes the live browser.
type Info struct {
	Port    int    `json:"port"`    // debugging port on 127.0.0.1
	Keeper  int    `json:"keeper"`  // pid of the keeper process
	Profile string `json:"profile"` // name of the browser profile in use
	Mode    string `json:"mode"`    // Headless, Virtual or Headed
}

// How the browser is shown. Some sites (Costco's sign-in among them) send a
// windowless browser an empty page, so a window on a virtual screen that
// nobody sees is the default wherever Xvfb is installed.
const (
	Headless = "headless" // no window at all
	Virtual  = "virtual"  // a real window on a private virtual screen (Xvfb)
	Headed   = "headed"   // a real window on the desktop of whoever runs the command
)

// Mode picks how to show the browser: on the desktop if asked, else on a
// virtual screen if Xvfb is installed and $NOVEE_BROWSE_HEADLESS is not 1.
func Mode(headed bool) string {
	if headed {
		return Headed
	}
	if os.Getenv("NOVEE_BROWSE_HEADLESS") != "1" {
		if _, err := exec.LookPath("Xvfb"); err == nil {
			return Virtual
		}
	}
	return Headless
}

// virtualScreen starts an Xvfb on a free display number and returns it as a
// DISPLAY value.
func virtualScreen() (*exec.Cmd, string, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, "", err
	}
	defer r.Close()
	x := exec.Command("Xvfb", "-displayfd", "3", "-screen", "0", "1280x900x24", "-nolisten", "tcp")
	x.ExtraFiles = []*os.File{w}
	x.Stderr = os.Stderr
	err = x.Start()
	w.Close()
	if err != nil {
		return nil, "", err
	}
	_ = r.SetReadDeadline(time.Now().Add(10 * time.Second))
	buf := make([]byte, 16)
	n, _ := r.Read(buf) // Xvfb writes the number and a newline once it is ready
	num := strings.TrimSpace(string(buf[:n]))
	if _, err := strconv.Atoi(num); err != nil {
		_ = x.Process.Kill()
		return nil, "", errors.New("the virtual screen (Xvfb) did not start")
	}
	go x.Wait()
	return x, ":" + num, nil
}

// Dir is where the session file, screenshots and saved pages go.
func Dir() string {
	if d := os.Getenv("NOVEE_BROWSE_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "novee", "browse")
}

// ProfileDir is the Chromium profile for a site. It is the same folder the
// receipts skill uses, so one sign-in by the owner serves both.
func ProfileDir(name string) string {
	root := os.Getenv("NOVEE_BROWSER_DIR")
	if root == "" {
		home, _ := os.UserHomeDir()
		root = filepath.Join(home, ".local", "share", "novee", "browser")
	}
	return filepath.Join(root, name)
}

var twoPart = map[string]bool{"co": true, "com": true, "org": true, "net": true, "ac": true, "gov": true, "edu": true}
var unsafe = regexp.MustCompile(`[^a-z0-9-]`)

// SiteProfile names the profile for a URL after the site, so that
// www.costco.com and signin.costco.com share "costco".
func SiteProfile(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return "main"
	}
	host := strings.ToLower(u.Hostname())
	if net.ParseIP(host) != nil {
		return "main"
	}
	l := strings.Split(host, ".")
	name := l[0]
	switch {
	case len(l) >= 3 && len(l[len(l)-1]) == 2 && twoPart[l[len(l)-2]]:
		name = l[len(l)-3] // example.co.uk
	case len(l) >= 2:
		name = l[len(l)-2]
	}
	if name = unsafe.ReplaceAllString(name, ""); name == "" {
		return "main"
	}
	return name
}

func file() string { return filepath.Join(Dir(), "session.json") }

// Current returns the live browser, if there is one that answers.
func Current() (*Info, bool) {
	data, err := os.ReadFile(file())
	if err != nil {
		return nil, false
	}
	var in Info
	if json.Unmarshal(data, &in) != nil || in.Port == 0 {
		return nil, false
	}
	if _, err := targets(in.Port); err != nil {
		return nil, false
	}
	return &in, true
}

// Touch tells the keeper the session is in use.
func Touch() {
	now := time.Now()
	_ = os.Chtimes(file(), now, now)
}

type targetInfo struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	URL  string `json:"url"`
}

func targets(port int) ([]targetInfo, error) {
	c := http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get(fmt.Sprintf("http://127.0.0.1:%d/json/list", port))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var ts []targetInfo
	return ts, json.NewDecoder(resp.Body).Decode(&ts)
}

// PageTarget returns the id of the tab commands work in: the newest page.
func PageTarget(in *Info) (string, error) {
	ts, err := targets(in.Port)
	if err != nil {
		return "", err
	}
	for _, t := range ts {
		if t.Type == "page" && !strings.HasPrefix(t.URL, "devtools://") {
			return t.ID, nil
		}
	}
	return "", errors.New("the browser has no open page")
}

// FindExec returns the browser binary: $NOVEE_CHROMIUM or the first usual name on PATH.
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

// Command builds the browser command line for a profile.
func Command(profile string, headed bool, extra ...string) (*exec.Cmd, error) {
	bin, err := FindExec()
	if err != nil {
		return nil, err
	}
	dir := ProfileDir(profile)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	args := []string{"--user-data-dir=" + dir, "--no-first-run", "--no-default-browser-check",
		"--disable-blink-features=AutomationControlled", "--password-store=basic", "--window-size=1280,900", "--window-position=0,0"}
	if !headed {
		args = append(args, "--headless=new")
	}
	if os.Geteuid() == 0 {
		args = append(args, "--no-sandbox") // Chromium refuses its sandbox as root
	}
	return exec.Command(bin, append(args, extra...)...), nil
}

// Spawn starts a long-lived helper process (the keeper, the sign-in viewer)
// that must outlive the command that starts it, with its output in logPath.
//
// An agent runtime cleans up everything a command leaves behind, including
// processes that moved to a session of their own, so where a systemd user
// manager is running the helper is handed to it as a transient unit. Elsewhere
// (and with NOVEE_BROWSE_NO_SYSTEMD=1) it is started in a new session.
func Spawn(name string, args []string, logPath string) error {
	if run, err := exec.LookPath("systemd-run"); err == nil && os.Getenv("NOVEE_BROWSE_NO_SYSTEMD") != "1" {
		runtime := os.Getenv("XDG_RUNTIME_DIR")
		if runtime == "" {
			runtime = fmt.Sprintf("/run/user/%d", os.Getuid())
		}
		if _, err := os.Stat(filepath.Join(runtime, "systemd", "private")); err == nil {
			a := []string{"--user", "--collect", "--quiet", "--unit", fmt.Sprintf("novee-browse-%s-%d", name, time.Now().UnixNano()),
				"-p", "StandardOutput=append:" + logPath, "-p", "StandardError=append:" + logPath}
			for _, kv := range os.Environ() {
				k, _, _ := strings.Cut(kv, "=")
				if strings.HasPrefix(k, "NOVEE_") || k == "PATH" || k == "HOME" || k == "DISPLAY" || k == "XAUTHORITY" || k == "WAYLAND_DISPLAY" {
					a = append(a, "-E", kv)
				}
			}
			cmd := exec.Command(run, append(append(a, "--"), args...)...)
			cmd.Env = append(os.Environ(), "XDG_RUNTIME_DIR="+runtime)
			if out, err := cmd.CombinedOutput(); err == nil {
				return nil
			} else if len(out) > 0 {
				_ = os.WriteFile(logPath, out, 0o600) // fall through to the plain way
			}
		}
	}
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}

// Start launches a keeper and its browser and waits until the browser answers.
// keeperArgs is the command line that runs RunKeeper in a new process, without
// the profile, port and mode arguments, which Start appends.
func Start(profile, mode string, keeperArgs []string) (*Info, error) {
	if _, err := FindExec(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(Dir(), 0o700); err != nil {
		return nil, err
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()

	logPath := filepath.Join(Dir(), "keeper.log")
	_ = os.Remove(logPath)
	if err := Spawn("keeper", append(append([]string{}, keeperArgs...), profile, strconv.Itoa(port), mode), logPath); err != nil {
		return nil, err
	}
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if in, ok := Current(); ok && in.Port == port {
			return in, nil
		}
	}
	_ = Stop()
	tail, _ := os.ReadFile(filepath.Join(Dir(), "keeper.log"))
	return nil, fmt.Errorf("the browser did not start (is profile %q open in another browser?): %s", profile, strings.TrimSpace(string(tail)))
}

// Stop closes the live browser, if any, and waits for it to be gone.
func Stop() error {
	data, err := os.ReadFile(file())
	if err != nil {
		return nil
	}
	var in Info
	if json.Unmarshal(data, &in) == nil && in.Keeper > 0 {
		_ = syscall.Kill(in.Keeper, syscall.SIGTERM)
		for i := 0; i < 100 && syscall.Kill(in.Keeper, 0) == nil; i++ {
			time.Sleep(100 * time.Millisecond)
		}
	}
	_ = os.Remove(file())
	return nil
}

// RunKeeper owns the browser: it starts it, records the session, and closes
// it when asked to (SIGTERM), when the browser exits, or after idle time
// without a Touch.
func RunKeeper(profile string, port int, mode string, idle time.Duration) error {
	cmd, err := Command(profile, mode != Headless, "--remote-debugging-port="+strconv.Itoa(port), "about:blank")
	if err != nil {
		return err
	}
	if mode == Virtual {
		x, display, err := virtualScreen()
		if err != nil {
			return err
		}
		defer x.Process.Kill()
		for _, kv := range os.Environ() {
			if !strings.HasPrefix(kv, "DISPLAY=") && !strings.HasPrefix(kv, "WAYLAND_DISPLAY=") {
				cmd.Env = append(cmd.Env, kv)
			}
		}
		cmd.Env = append(cmd.Env, "DISPLAY="+display)
	}
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	data, _ := json.Marshal(Info{Port: port, Keeper: os.Getpid(), Profile: profile, Mode: mode})
	if err := os.WriteFile(file(), data, 0o600); err != nil {
		_ = cmd.Process.Kill()
		return err
	}
	defer os.Remove(file())

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		select {
		case err := <-exited:
			return err
		case <-sig:
		case <-tick.C:
			st, err := os.Stat(file())
			if err == nil && time.Since(st.ModTime()) < idle {
				continue
			}
		}
		// SIGTERM lets Chromium save its cookies before it goes.
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-exited:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
		}
		return nil
	}
}
