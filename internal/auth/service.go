// Package auth implements sign-in: OAuth/OIDC providers, magic links,
// and the account/identity model. The merge rule is strict: an
// identity only attaches to an existing account through an email the
// provider has verified.
package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/lporcheron/quorum/internal/ids"
	"github.com/lporcheron/quorum/internal/store"
	"github.com/lporcheron/quorum/internal/store/sqlite"
)

const magicLinkTTL = 15 * time.Minute

var (
	ErrRegistrationsClosed = errors.New("registrations are closed")
	ErrEmailNotAllowed     = errors.New("email domain not allowed")
	ErrEmailUnverified     = errors.New("provider did not assert a verified email")
	ErrInvalidToken        = errors.New("invalid or expired login token")
	ErrNotFound            = errors.New("not found")
)

// User is the signed-in account.
type User struct {
	ID              int64
	PublicID        string
	Email           string
	Name            string
	AvatarURL       string
	Locale          string
	Timezone        string
	PersonalSpaceID int64
}

// Policy is the instance gate on new accounts.
type Policy struct {
	// RegistrationsOpen is consulted per attempt: the admin page can
	// flip it at runtime. Nil means always open.
	RegistrationsOpen func(context.Context) bool
	// AllowedDomains restricts sign-up email domains when non-empty.
	AllowedDomains []string
	// AdminEmails open an account whatever the two fields above say;
	// see registrationAllowed.
	AdminEmails []string
}

// Service owns accounts, identities and login tokens.
type Service struct {
	store       *store.Store
	now         func() time.Time
	policy      Policy
	adminEmails map[string]bool
}

// NewService wires the auth service; now is injectable for tests.
func NewService(st *store.Store, now func() time.Time, p Policy) *Service {
	if now == nil {
		now = time.Now
	}
	if p.RegistrationsOpen == nil {
		p.RegistrationsOpen = func(context.Context) bool { return true }
	}
	admins := make(map[string]bool, len(p.AdminEmails))
	for _, e := range p.AdminEmails {
		admins[strings.ToLower(strings.TrimSpace(e))] = true
	}
	return &Service{store: st, now: now, policy: p, adminEmails: admins}
}

// registrationAllowed reports why email may not open an account on this
// instance, or nil.
//
// An address listed in QUORUM_ADMIN_EMAILS always may. That list comes
// from the environment, not from the database, and it only grants the
// /admin page to an account that already exists — so without this
// exception an operator who closes registrations (or sets a domain
// allowlist that misses their own address) before signing in once has
// locked every door from the outside, with no way back in and no way
// to reopen registrations.
func (s *Service) registrationAllowed(ctx context.Context, email string) error {
	if s.adminEmails[email] {
		return nil
	}
	if !s.policy.RegistrationsOpen(ctx) {
		return ErrRegistrationsClosed
	}
	if !s.domainAllowed(email) {
		return ErrEmailNotAllowed
	}
	return nil
}

// Defaults are profile hints for a first sign-in, taken from the
// request (Accept-Language, tz cookie).
type Defaults struct {
	Locale   string
	Timezone string
}

// UserByID loads a user for the session middleware.
func (s *Service) UserByID(ctx context.Context, id int64) (User, error) {
	row, err := s.store.GetUserByID(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("get user: %w", err)
	}
	return userFromRow(row), nil
}

// Complete turns a provider assertion into a signed-in user, creating
// the account (and its personal space) on first sign-in.
//
// Merge rule, in order:
//  1. the (provider, subject) identity already exists → that user;
//  2. a user owns the asserted email AND the provider verified it →
//     attach the identity to that user;
//  3. otherwise → new account, gated by the registration settings.
//
// An unverified email never matches rule 2 or creates an account: that
// path is the classic account-takeover hole.
func (s *Service) Complete(ctx context.Context, login Login, d Defaults) (User, error) {
	login.Email = strings.ToLower(strings.TrimSpace(login.Email))

	if row, err := s.store.GetIdentity(ctx, sqlite.GetIdentityParams{Provider: login.Provider, Subject: login.Subject}); err == nil {
		urow, err := s.store.GetUserByID(ctx, row.UserID)
		if err != nil {
			return User{}, fmt.Errorf("get user for identity: %w", err)
		}
		return userFromRow(urow), s.refreshProfile(ctx, urow, login)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return User{}, fmt.Errorf("get identity: %w", err)
	}

	if login.Email == "" || !login.EmailVerified {
		return User{}, ErrEmailUnverified
	}

	now := store.FormatTime(s.now())
	if urow, err := s.store.GetUserByEmail(ctx, login.Email); err == nil {
		// Verified email → safe to attach this new identity.
		if _, err := s.store.CreateIdentity(ctx, sqlite.CreateIdentityParams{
			UserID: urow.ID, Provider: login.Provider, Subject: login.Subject, CreatedAt: now,
		}); err != nil {
			return User{}, fmt.Errorf("attach identity: %w", err)
		}
		return userFromRow(urow), nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return User{}, fmt.Errorf("get user by email: %w", err)
	}

	return s.register(ctx, login, d)
}

// register creates the account, its identity, and the personal space.
func (s *Service) register(ctx context.Context, login Login, d Defaults) (User, error) {
	if err := s.registrationAllowed(ctx, login.Email); err != nil {
		return User{}, err
	}

	name := strings.TrimSpace(login.Name)
	if name == "" {
		name, _, _ = strings.Cut(login.Email, "@")
	}
	locale := d.Locale
	if locale == "" {
		locale = "en"
	}
	timezone := d.Timezone
	if timezone == "" {
		timezone = "UTC"
	}

	now := store.FormatTime(s.now())
	var user User
	err := s.store.Tx(ctx, func(q *sqlite.Queries) error {
		urow, err := q.CreateUser(ctx, sqlite.CreateUserParams{
			PublicID:  ids.PublicID(),
			Email:     login.Email,
			Name:      name,
			AvatarUrl: nullString(login.AvatarURL),
			Locale:    locale,
			Timezone:  timezone,
			CreatedAt: now,
		})
		if err != nil {
			return fmt.Errorf("create user: %w", err)
		}
		if _, err := q.CreateIdentity(ctx, sqlite.CreateIdentityParams{
			UserID: urow.ID, Provider: login.Provider, Subject: login.Subject, CreatedAt: now,
		}); err != nil {
			return fmt.Errorf("create identity: %w", err)
		}
		space, err := q.CreateSpace(ctx, sqlite.CreateSpaceParams{
			PublicID:    ids.PublicID(),
			Slug:        strings.ToLower(ids.PublicID()),
			Name:        name,
			OwnerUserID: urow.ID,
			CreatedAt:   now,
		})
		if err != nil {
			return fmt.Errorf("create personal space: %w", err)
		}
		if err := q.CreateSpaceMember(ctx, sqlite.CreateSpaceMemberParams{
			SpaceID: space.ID, UserID: urow.ID, Role: "owner", CreatedAt: now,
		}); err != nil {
			return fmt.Errorf("create membership: %w", err)
		}
		if err := q.SetUserPersonalSpace(ctx, sqlite.SetUserPersonalSpaceParams{
			ID: urow.ID, PersonalSpaceID: sql.NullInt64{Int64: space.ID, Valid: true},
		}); err != nil {
			return fmt.Errorf("set personal space: %w", err)
		}
		urow.PersonalSpaceID = sql.NullInt64{Int64: space.ID, Valid: true}
		user = userFromRow(urow)
		return nil
	})
	return user, err
}

// refreshProfile keeps name/avatar current on returning sign-ins.
func (s *Service) refreshProfile(ctx context.Context, urow sqlite.User, login Login) error {
	name := strings.TrimSpace(login.Name)
	if name == "" || (name == urow.Name && login.AvatarURL == urow.AvatarUrl.String) {
		return nil
	}
	err := s.store.UpdateUserProfile(ctx, sqlite.UpdateUserProfileParams{
		ID: urow.ID, Name: name, AvatarUrl: nullString(login.AvatarURL),
	})
	if err != nil {
		return fmt.Errorf("refresh profile: %w", err)
	}
	return nil
}

func (s *Service) domainAllowed(email string) bool {
	if len(s.policy.AllowedDomains) == 0 {
		return true
	}
	_, domain, ok := strings.Cut(email, "@")
	if !ok {
		return false
	}
	for _, d := range s.policy.AllowedDomains {
		if domain == d {
			return true
		}
	}
	return false
}

// RequestMagicLink creates a single-use token and sends it via send.
// For an unknown email that could not register anyway, it silently
// does nothing: the response never reveals whether an account exists.
func (s *Service) RequestMagicLink(ctx context.Context, email, redirect string, send func(email, token string) error) error {
	email = strings.ToLower(strings.TrimSpace(email))
	if _, err := mail.ParseAddress(email); err != nil {
		return ErrEmailNotAllowed
	}
	if _, err := s.store.GetUserByEmail(ctx, email); errors.Is(err, sql.ErrNoRows) {
		if s.registrationAllowed(ctx, email) != nil {
			return nil
		}
	} else if err != nil {
		return fmt.Errorf("get user by email: %w", err)
	}

	token := ids.Token()
	now := s.now()
	_, err := s.store.CreateLoginToken(ctx, sqlite.CreateLoginTokenParams{
		Email:     email,
		TokenHash: ids.HashToken(token),
		Redirect:  SanitizeRedirect(redirect),
		ExpiresAt: store.FormatTime(now.Add(magicLinkTTL)),
		CreatedAt: store.FormatTime(now),
	})
	if err != nil {
		return fmt.Errorf("create login token: %w", err)
	}
	return send(email, token)
}

// ConsumeMagicLink validates a token (single use, TTL) and signs the
// email in — clicking the link proves the address, so the email is
// verified by construction.
func (s *Service) ConsumeMagicLink(ctx context.Context, token string, d Defaults) (User, string, error) {
	row, err := s.store.GetLoginTokenByHash(ctx, ids.HashToken(token))
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, "", ErrInvalidToken
	}
	if err != nil {
		return User{}, "", fmt.Errorf("get login token: %w", err)
	}
	expires, err := store.ParseTime(row.ExpiresAt)
	if err != nil || s.now().After(expires) || row.ConsumedAt.Valid {
		return User{}, "", ErrInvalidToken
	}
	n, err := s.store.ConsumeLoginToken(ctx, sqlite.ConsumeLoginTokenParams{
		ID: row.ID, ConsumedAt: sql.NullString{String: store.FormatTime(s.now()), Valid: true},
	})
	if err != nil {
		return User{}, "", fmt.Errorf("consume login token: %w", err)
	}
	if n != 1 {
		return User{}, "", ErrInvalidToken // raced with another use
	}

	user, err := s.Complete(ctx, Login{
		Provider:      "email",
		Subject:       row.Email,
		Email:         row.Email,
		EmailVerified: true,
	}, d)
	if err != nil {
		return User{}, "", err
	}
	return user, row.Redirect, nil
}

// SanitizeRedirect keeps only local absolute paths, and is the single
// rule for every post-sign-in destination: a user hands one over in a
// URL, and it ends up in a Location header at the moment they trust
// what they see the most.
//
// Rejecting a leading "//" is not enough. Browsers parse "\" as "/" at
// the start of a path, so "/\evil.com" reaches the network as
// "//evil.com" — a protocol-relative URL onto someone else's host. CR
// and LF are refused on the same principle, before anything downstream
// gets a chance to split a header on them.
func SanitizeRedirect(p string) string {
	if strings.HasPrefix(p, "/") && !strings.HasPrefix(p, "//") && !strings.ContainsAny(p, "\r\n\\") {
		return p
	}
	return ""
}

func userFromRow(r sqlite.User) User {
	return User{
		ID:              r.ID,
		PublicID:        r.PublicID,
		Email:           r.Email,
		Name:            r.Name,
		AvatarURL:       r.AvatarUrl.String,
		Locale:          r.Locale,
		Timezone:        r.Timezone,
		PersonalSpaceID: r.PersonalSpaceID.Int64,
	}
}

func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}
