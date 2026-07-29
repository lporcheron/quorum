package auth

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/lporcheron/quorum/internal/store"
)

var testNow = time.Date(2026, time.July, 29, 10, 0, 0, 0, time.UTC)

func newTestService(t *testing.T, open bool, domains []string) (context.Context, *Service) {
	return newTestServiceVar(t, &open, domains)
}

func newTestServiceVar(t *testing.T, open *bool, domains []string) (context.Context, *Service) {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := store.Migrate(ctx, db, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return ctx, NewService(store.New(db), func() time.Time { return testNow }, func(context.Context) bool { return *open }, domains)
}

func google(sub, email string) Login {
	return Login{Provider: "google", Subject: sub, Email: email, EmailVerified: true, Name: "Alice Google"}
}

func TestFirstSignInCreatesAccountAndPersonalSpace(t *testing.T) {
	ctx, s := newTestService(t, true, nil)
	u, err := s.Complete(ctx, google("g-1", "alice@example.com"), Defaults{Locale: "fr", Timezone: "Europe/Paris"})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if u.Email != "alice@example.com" || u.Name != "Alice Google" || u.Locale != "fr" || u.Timezone != "Europe/Paris" {
		t.Errorf("user = %+v", u)
	}
	if u.PersonalSpaceID == 0 {
		t.Error("personal space not created")
	}

	// Same identity again → same user, no duplicate.
	again, err := s.Complete(ctx, google("g-1", "alice@example.com"), Defaults{})
	if err != nil || again.ID != u.ID {
		t.Errorf("returning sign-in: %v (id %d vs %d)", err, again.ID, u.ID)
	}
}

func TestMergeOnVerifiedEmailOnly(t *testing.T) {
	ctx, s := newTestService(t, true, nil)
	u, err := s.Complete(ctx, google("g-1", "alice@example.com"), Defaults{})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	// Same verified email through another provider → same account.
	merged, err := s.Complete(ctx, Login{
		Provider: "oidc", Subject: "corp-77", Email: "Alice@Example.com", EmailVerified: true, Name: "Alice Corp",
	}, Defaults{})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if merged.ID != u.ID {
		t.Errorf("verified-email merge failed: %d vs %d", merged.ID, u.ID)
	}

	// The attacker's classic: same email, NOT verified → refused, and
	// no ghost account either.
	_, err = s.Complete(ctx, Login{
		Provider: "oidc", Subject: "evil-1", Email: "alice@example.com", EmailVerified: false,
	}, Defaults{})
	if !errors.Is(err, ErrEmailUnverified) {
		t.Errorf("unverified merge: err = %v, want ErrEmailUnverified", err)
	}

	// No email at all → refused too.
	_, err = s.Complete(ctx, Login{Provider: "oidc", Subject: "evil-2"}, Defaults{})
	if !errors.Is(err, ErrEmailUnverified) {
		t.Errorf("missing email: err = %v", err)
	}

	// But the already-attached identity keeps signing in even though it
	// was registered before: rule 1 beats rule 2.
	back, err := s.Complete(ctx, Login{Provider: "oidc", Subject: "corp-77", EmailVerified: false}, Defaults{})
	if err != nil || back.ID != u.ID {
		t.Errorf("existing identity sign-in: %v", err)
	}
}

func TestRegistrationGates(t *testing.T) {
	ctx, s := newTestService(t, false, nil)
	_, err := s.Complete(ctx, google("g-9", "new@example.com"), Defaults{})
	if !errors.Is(err, ErrRegistrationsClosed) {
		t.Errorf("closed registrations: err = %v", err)
	}

	ctx, s = newTestService(t, true, []string{"bleemeo.com"})
	if _, err := s.Complete(ctx, google("g-1", "lionel@bleemeo.com"), Defaults{}); err != nil {
		t.Errorf("allowed domain rejected: %v", err)
	}
	if _, err := s.Complete(ctx, google("g-2", "someone@gmail.com"), Defaults{}); !errors.Is(err, ErrEmailNotAllowed) {
		t.Errorf("blocked domain accepted: %v", err)
	}

	// Closed registrations never lock out existing users.
	openFlag := true
	ctx2, closed := newTestServiceVar(t, &openFlag, nil)
	if _, err := closed.Complete(ctx2, google("g-5", "old@example.com"), Defaults{}); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	openFlag = false
	if _, err := closed.Complete(ctx2, google("g-5", "old@example.com"), Defaults{}); err != nil {
		t.Errorf("existing user locked out: %v", err)
	}
}

func TestMagicLinkFlow(t *testing.T) {
	ctx, s := newTestService(t, true, nil)

	var sentTo, sentToken string
	send := func(email, token string) error { sentTo, sentToken = email, token; return nil }

	if err := s.RequestMagicLink(ctx, "Carol@Example.com", "/dashboard", send); err != nil {
		t.Fatalf("RequestMagicLink: %v", err)
	}
	if sentTo != "carol@example.com" || sentToken == "" {
		t.Fatalf("mail: to=%q token=%q", sentTo, sentToken)
	}

	u, redirect, err := s.ConsumeMagicLink(ctx, sentToken, Defaults{Locale: "fr"})
	if err != nil {
		t.Fatalf("ConsumeMagicLink: %v", err)
	}
	if u.Email != "carol@example.com" || redirect != "/dashboard" {
		t.Errorf("user %+v redirect %q", u, redirect)
	}

	// Single use.
	if _, _, err := s.ConsumeMagicLink(ctx, sentToken, Defaults{}); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("token reuse: err = %v", err)
	}
	// Garbage token.
	if _, _, err := s.ConsumeMagicLink(ctx, "nope", Defaults{}); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("garbage token: err = %v", err)
	}
}

func TestMagicLinkExpiry(t *testing.T) {
	ctx, s := newTestService(t, true, nil)
	var token string
	if err := s.RequestMagicLink(ctx, "dave@example.com", "", func(_, tok string) error { token = tok; return nil }); err != nil {
		t.Fatalf("RequestMagicLink: %v", err)
	}
	s.now = func() time.Time { return testNow.Add(16 * time.Minute) }
	if _, _, err := s.ConsumeMagicLink(ctx, token, Defaults{}); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("expired token accepted: %v", err)
	}
}

func TestMagicLinkDoesNotLeakAccounts(t *testing.T) {
	// Registrations closed, unknown email: succeed silently, send nothing.
	ctx, s := newTestService(t, false, nil)
	sent := false
	err := s.RequestMagicLink(ctx, "stranger@example.com", "", func(_, _ string) error { sent = true; return nil })
	if err != nil || sent {
		t.Errorf("err=%v sent=%v, want silent no-op", err, sent)
	}

	// Evil redirects are stripped.
	ctx, s = newTestService(t, true, nil)
	var token string
	if err := s.RequestMagicLink(ctx, "eve@example.com", "https://evil.example", func(_, tok string) error { token = tok; return nil }); err != nil {
		t.Fatal(err)
	}
	_, redirect, err := s.ConsumeMagicLink(ctx, token, Defaults{})
	if err != nil || redirect != "" {
		t.Errorf("redirect = %q, want empty", redirect)
	}
}
