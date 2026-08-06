// Command screenshots regenerates the README images: it seeds a
// throwaway instance with demo data, serves it, and drives the locally
// installed Chrome to capture the poll grid in both themes and at both
// widths.
//
//	make screenshots
//
// It is a development tool. Nothing here ships: the Dockerfile and the
// release workflow build ./cmd/quorum only. No Node, no browser
// automation library — Chrome's own --screenshot flag does the work, and
// a tiny reverse proxy injects the theme cookie the app already reads.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lporcheron/quorum/internal/poll"
	"github.com/lporcheron/quorum/internal/store"
)

// shot is one image to produce.
type shot struct {
	name   string
	theme  string // "light", "dark", or "" for the system default
	width  int
	height int
}

// The desktop height stops in the gap after the second voting row:
// slicing through a row reads as a broken image in a README.
var shots = []shot{
	{name: "grid-light", theme: "light", width: 1280, height: 880},
	{name: "grid-dark", theme: "dark", width: 1280, height: 880},
	// The mobile grid is a designed alternative, not a scrolled desktop
	// one, so it earns its own image.
	{name: "grid-mobile-light", theme: "light", width: 390, height: 844},
	{name: "grid-mobile-dark", theme: "dark", width: 390, height: 844},
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "screenshots:", err)
		os.Exit(1)
	}
}

func run() error {
	out := flag.String("out", "docs/screenshots", "directory for the PNGs")
	bin := flag.String("bin", "bin/quorum", "quorum binary to serve with (build it first)")
	chrome := flag.String("chrome", defaultChrome(), "Chrome or Chromium executable")
	scale := flag.String("scale", "2", "device pixel ratio (2 = retina-sharp README images)")
	flag.Parse()

	if _, err := os.Stat(*chrome); err != nil {
		return fmt.Errorf("no browser at %q: pass -chrome with the path to Chrome or Chromium", *chrome)
	}
	if _, err := os.Stat(*bin); err != nil {
		return fmt.Errorf("no binary at %q: run `make build` first", *bin)
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		return err
	}

	work, err := os.MkdirTemp("", "quorum-screenshots-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(work) }()

	ctx := context.Background()
	dbPath := filepath.Join(work, "demo.db")
	pollID, err := seed(ctx, dbPath)
	if err != nil {
		return fmt.Errorf("seed the demo instance: %w", err)
	}

	// Serve the seeded database, then proxy it so each capture carries the
	// theme cookie. Chrome's CLI cannot set one; the app reads nothing else.
	appAddr, stopApp, err := serve(*bin, dbPath)
	if err != nil {
		return err
	}
	defer stopApp()

	var theme atomic.Value
	theme.Store("")
	proxyAddr, stopProxy, err := themeProxy(appAddr, &theme)
	if err != nil {
		return err
	}
	defer stopProxy()

	for _, s := range shots {
		theme.Store(s.theme)
		dest := filepath.Join(*out, s.name+".png")
		page := fmt.Sprintf("http://%s/polls/%s", proxyAddr, pollID)
		if err := capture(*chrome, page, dest, s, *scale, work); err != nil {
			return fmt.Errorf("capture %s: %w", s.name, err)
		}
		info, err := os.Stat(dest)
		if err != nil {
			return err
		}
		fmt.Printf("%-24s %5d×%-5d %6.0f KB\n", dest, s.width, s.height, float64(info.Size())/1024)
	}
	fmt.Printf("\n%d images in %s. The demo instance was thrown away.\n", len(shots), *out)
	return nil
}

// seed builds the poll the README shows: a timed poll whose tallies leave
// one column clearly ahead, so the winning-column treatment is visible.
func seed(ctx context.Context, dbPath string) (string, error) {
	db, err := store.Open(ctx, dbPath)
	if err != nil {
		return "", err
	}
	defer db.Close()
	quiet := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	if err := store.Migrate(ctx, db, store.DialectSQLite, quiet); err != nil {
		return "", err
	}
	st := store.New(db, store.DialectSQLite)
	polls := poll.NewService(st, nil)

	// Anchored on a fixed future week so the images are reproducible and
	// never show a date in the past.
	monday := poll.Date{Year: 2026, Month: time.October, Day: 12}
	slot := func(dayOffset, hour int) poll.TimedSlot {
		return poll.TimedSlot{
			Date:     poll.Date{Year: monday.Year, Month: monday.Month, Day: monday.Day + dayOffset},
			Hour:     hour,
			Duration: time.Hour,
		}
	}

	p, _, err := polls.Create(ctx, poll.NewPoll{
		Title:         "Sprint review",
		Description:   "One hour, camera optional. Pick every slot that works — “if need be” counts.",
		Location:      "Room Ada Lovelace, or the usual video link",
		Kind:          poll.KindTimed,
		Timezone:      "Europe/Paris",
		AllowComments: true,
		Slots: []poll.TimedSlot{
			slot(0, 10), slot(0, 14), slot(1, 11), slot(2, 9), slot(2, 16),
		},
	})
	if err != nil {
		return "", err
	}

	view, err := polls.View(ctx, p)
	if err != nil {
		return "", err
	}
	const (
		y = "y" // yes
		i = "i" // if need be
		n = "n" // no
	)
	// Column 3 (Tuesday 11:00) is the winner: everyone can make it.
	ballots := []struct {
		name    string
		pattern string
	}{
		{"Claire Fontaine", "y n y i n"},
		{"Malik Boureau", "n y y y i"},
		{"Ingrid Sørensen", "i y y n y"},
		{"Tomás Ferreira", "y n y y n"},
		{"Nour El Amrani", "n i y i y"},
		{"Jonas Weber", "y y y n i"},
	}
	for _, b := range ballots {
		votes := make(map[int64]poll.VoteValue, len(view.Options))
		for idx, opt := range view.Options {
			switch string(b.pattern[idx*2]) {
			case y:
				votes[opt.ID] = poll.VoteYes
			case i:
				votes[opt.ID] = poll.VoteIfNeedBe
			default:
				votes[opt.ID] = poll.VoteNo
			}
		}
		if _, _, err := polls.Join(ctx, p, b.name, "", 0, votes); err != nil {
			return "", fmt.Errorf("ballot for %s: %w", b.name, err)
		}
	}

	for _, c := range []struct{ who, body string }{
		{"Malik Boureau", "Tuesday suits me best — the morning is quieter."},
		{"Claire Fontaine", "Works for me too. I can bring the release notes."},
	} {
		if _, err := polls.AddComment(ctx, p, nil, c.who, c.body); err != nil {
			return "", err
		}
	}
	return p.PublicID, nil
}

// serve starts the real binary on a free port and waits for /healthz.
func serve(bin, dbPath string) (string, func(), error) {
	port, err := freePort()
	if err != nil {
		return "", nil, err
	}
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	cmd := exec.Command(bin) //nolint:gosec // path comes from a developer flag
	cmd.Env = append(os.Environ(),
		"QUORUM_DB_PATH="+dbPath,
		"QUORUM_ADDR="+addr,
		"QUORUM_BASE_URL=http://"+addr,
		"QUORUM_LOG_LEVEL=error",
	)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Start(); err != nil {
		return "", nil, fmt.Errorf("start %s: %w", bin, err)
	}
	stop := func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}

	deadline := time.Now().Add(15 * time.Second)
	for {
		resp, err := http.Get("http://" + addr + "/healthz") //nolint:noctx // local readiness probe
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return addr, stop, nil
			}
		}
		if time.Now().After(deadline) {
			stop()
			return "", nil, errors.New("the server did not become healthy in 15s")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// themeProxy forwards to the app, adding the theme cookie the current
// shot asks for. theme holds a string; empty means the system default.
func themeProxy(appAddr string, theme *atomic.Value) (string, func(), error) {
	target, err := url.Parse("http://" + appAddr)
	if err != nil {
		return "", nil, err
	}
	port, err := freePort()
	if err != nil {
		return "", nil, err
	}
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	rp := httputil.NewSingleHostReverseProxy(target)
	srv := &http.Server{
		Addr:              addr,
		ReadHeaderTimeout: 5 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if t, _ := theme.Load().(string); t != "" {
				r.Header.Set("Cookie", "quorum_theme="+t)
			}
			rp.ServeHTTP(w, r)
		}),
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return "", nil, err
	}
	go func() { _ = srv.Serve(ln) }()
	return addr, func() { _ = srv.Close() }, nil
}

// capture drives Chrome headless. --virtual-time-budget lets webfonts and
// CSS settle before the shot, which is what keeps runs identical.
//
// Chrome's new headless mode writes the PNG and then keeps running — it
// never returns, and the old mode that did was removed in Chrome 132. So
// success is "the file stopped growing", not "the process exited", and we
// stop the browser we started ourselves.
func capture(chrome, page, dest string, s shot, scale, work string) error {
	// A leftover file from a previous run would read as instant success.
	if err := os.Remove(dest); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	profile, err := os.MkdirTemp(work, "profile-")
	if err != nil {
		return err
	}
	var out lockedBuffer
	cmd := exec.Command(chrome, //nolint:gosec // path comes from a developer flag
		"--headless=new",
		"--disable-gpu",
		"--hide-scrollbars",
		"--no-first-run",
		"--no-default-browser-check",
		"--user-data-dir="+profile,
		"--force-device-scale-factor="+scale,
		fmt.Sprintf("--window-size=%d,%d", s.width, s.height),
		"--virtual-time-budget=3000",
		"--screenshot="+dest,
		page,
	)
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		return err
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	reaped := false
	defer func() {
		if !reaped {
			_ = cmd.Process.Kill()
			<-exited
		}
	}()

	deadline := time.Now().Add(60 * time.Second)
	var lastSize int64 = -1
	stable := 0
	for {
		select {
		case err := <-exited:
			reaped = true
			// A build that does exit on its own, or a failure.
			if info, statErr := os.Stat(dest); statErr == nil && info.Size() > 0 {
				return nil
			}
			return fmt.Errorf("chrome exited without writing the image: %v: %s", err, out.String())
		default:
		}
		if info, statErr := os.Stat(dest); statErr == nil && info.Size() > 0 {
			switch {
			case info.Size() == lastSize:
				if stable++; stable >= 3 {
					return nil // three quiet polls: the PNG is complete
				}
			default:
				stable, lastSize = 0, info.Size()
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("no image after 60s: %s", out.String())
		}
		time.Sleep(150 * time.Millisecond)
	}
}

// lockedBuffer collects Chrome's output; Stdout and Stderr share it, and
// the reader races the process, so the writes need guarding.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func freePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer func() { _ = ln.Close() }()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

func defaultChrome() string {
	if runtime.GOOS == "darwin" {
		return "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
	}
	for _, c := range []string{
		"/usr/bin/google-chrome", "/usr/bin/chromium", "/usr/bin/chromium-browser",
	} {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return "/usr/bin/google-chrome"
}
