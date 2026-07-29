package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/lporcheron/quorum/internal/auth"
	"github.com/lporcheron/quorum/internal/config"
	"github.com/lporcheron/quorum/internal/handler"
	"github.com/lporcheron/quorum/internal/i18n"
	"github.com/lporcheron/quorum/internal/poll"
	"github.com/lporcheron/quorum/internal/space"
	"github.com/lporcheron/quorum/internal/store"
)

func newTestServer(t *testing.T) (*httptest.Server, *testMailer) {
	t.Helper()
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := store.Migrate(ctx, db, log); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	tr, err := i18n.New()
	if err != nil {
		t.Fatalf("i18n.New: %v", err)
	}

	cfg, err := config.Load(func(string) string { return "" })
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	st := store.New(db)
	polls := poll.NewService(st, nil)
	spaces := space.NewService(st, nil)
	authsvc := auth.NewService(st, nil, cfg.RegistrationsOpen, cfg.EmailAllowedDomains)
	sessions := auth.NewSessionManager(db, cfg.BaseURL)
	mailer := &testMailer{}
	h := handler.New(log, db, tr, polls, spaces, authsvc, nil, sessions, mailer, cfg.BaseURL)
	srv := New(cfg, log, h, sessions)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, mailer
}

// testMailer records outgoing messages so tests can fish the magic
// link out of the body.
type testMailer struct {
	mu   sync.Mutex
	to   []string
	body []string
}

func (m *testMailer) Enabled() bool { return true }

func (m *testMailer) Send(_ context.Context, to, _ string, body string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.to = append(m.to, to)
	m.body = append(m.body, body)
	return nil
}

func (m *testMailer) last(t *testing.T) (string, string) {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.to) == 0 {
		t.Fatal("no mail sent")
	}
	return m.to[len(m.to)-1], m.body[len(m.body)-1]
}

func get(t *testing.T, ts *httptest.Server, path string, header http.Header) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, ts.URL+path, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	for k, vs := range header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp, string(body)
}

func TestHealthz(t *testing.T) {
	ts, _ := newTestServer(t)
	resp, body := get(t, ts, "/healthz", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if strings.TrimSpace(body) != "ok" {
		t.Errorf("body = %q, want ok", body)
	}
}

func TestHomeLocalized(t *testing.T) {
	ts, _ := newTestServer(t)

	resp, body := get(t, ts, "/", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, `lang="en"`) || !strings.Contains(body, "Count the votes") {
		t.Errorf("english home page missing expected content:\n%s", body)
	}

	_, body = get(t, ts, "/", http.Header{"Accept-Language": {"fr-FR,fr;q=0.9"}})
	if !strings.Contains(body, `lang="fr"`) || !strings.Contains(body, "Comptez les voix") {
		t.Errorf("french home page missing expected content:\n%s", body)
	}
}

func TestSecurityHeadersAndRequestID(t *testing.T) {
	ts, _ := newTestServer(t)
	resp, _ := get(t, ts, "/", nil)

	for header, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "strict-origin-when-cross-origin",
	} {
		if got := resp.Header.Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	if !strings.Contains(resp.Header.Get("Content-Security-Policy"), "default-src 'self'") {
		t.Errorf("CSP = %q", resp.Header.Get("Content-Security-Policy"))
	}
	if resp.Header.Get("X-Request-Id") == "" {
		t.Error("X-Request-Id header missing")
	}
}

func TestUnknownPathIs404(t *testing.T) {
	ts, _ := newTestServer(t)
	resp, _ := get(t, ts, "/polls/nope", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestStaticServed(t *testing.T) {
	ts, _ := newTestServer(t)
	resp, _ := get(t, ts, "/static/css/app.css", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (run `make css` if app.css is missing)", resp.StatusCode)
	}
	if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "max-age") {
		t.Errorf("Cache-Control = %q", cc)
	}
}
