package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/lporcheron/quorum/internal/config"
)

// Login is what a completed provider flow asserts about the person.
type Login struct {
	Provider      string
	Subject       string
	Email         string
	EmailVerified bool
	Name          string
	AvatarURL     string
}

// Provider is one way to sign in. OIDC discovery happens lazily on
// first use so the binary boots with no network access.
type Provider struct {
	Key   string
	Label string

	client      config.OAuthClient
	redirectURL string
	issuer      string // empty for GitHub (plain OAuth2)
	// relaxIssuer works around Microsoft's multi-tenant endpoints,
	// whose issued tokens carry a per-tenant issuer.
	relaxIssuer bool
	// trustEmail treats the provider's email as verified even without
	// an email_verified claim. Set only for identity providers that
	// hand out organization-verified addresses (Microsoft Entra).
	trustEmail bool

	once     sync.Once
	oidcProv *oidc.Provider
	initErr  error
}

// NewProviders assembles the enabled providers.
func NewProviders(cfg config.Config, baseURL string) []*Provider {
	redirect := func(key string) string { return baseURL + "/auth/" + key + "/callback" }
	var out []*Provider
	if cfg.Google.Enabled() {
		out = append(out, &Provider{
			Key: "google", Label: "Google",
			client: cfg.Google, redirectURL: redirect("google"),
			issuer: "https://accounts.google.com",
		})
	}
	if cfg.GitHub.Enabled() {
		out = append(out, &Provider{
			Key: "github", Label: "GitHub",
			client: cfg.GitHub, redirectURL: redirect("github"),
		})
	}
	if cfg.Microsoft.Enabled() {
		multiTenant := cfg.MicrosoftTenant == "common" || cfg.MicrosoftTenant == "organizations" || cfg.MicrosoftTenant == "consumers"
		out = append(out, &Provider{
			Key: "microsoft", Label: "Microsoft",
			client: cfg.Microsoft, redirectURL: redirect("microsoft"),
			issuer:      "https://login.microsoftonline.com/" + cfg.MicrosoftTenant + "/v2.0",
			relaxIssuer: multiTenant,
			trustEmail:  true,
		})
	}
	if cfg.OIDC.Enabled() && cfg.OIDC.IssuerURL != "" {
		out = append(out, &Provider{
			Key: "oidc", Label: cfg.OIDC.Name,
			client: cfg.OIDC.OAuthClient, redirectURL: redirect("oidc"),
			issuer: cfg.OIDC.IssuerURL,
		})
	}
	return out
}

func (p *Provider) init(ctx context.Context) error {
	if p.issuer == "" {
		return nil
	}
	p.once.Do(func() {
		if p.relaxIssuer {
			ctx = oidc.InsecureIssuerURLContext(ctx, p.issuer)
		}
		p.oidcProv, p.initErr = oidc.NewProvider(ctx, p.issuer)
	})
	if p.initErr != nil {
		return fmt.Errorf("oidc discovery for %s: %w", p.Key, p.initErr)
	}
	return nil
}

func (p *Provider) oauthConfig(ctx context.Context) (*oauth2.Config, error) {
	if err := p.init(ctx); err != nil {
		return nil, err
	}
	conf := &oauth2.Config{
		ClientID:     p.client.ClientID,
		ClientSecret: p.client.ClientSecret,
		RedirectURL:  p.redirectURL,
	}
	if p.oidcProv != nil {
		conf.Endpoint = p.oidcProv.Endpoint()
		conf.Scopes = []string{oidc.ScopeOpenID, "email", "profile"}
	} else { // GitHub
		conf.Endpoint = oauth2.Endpoint{
			AuthURL:  "https://github.com/login/oauth/authorize",
			TokenURL: "https://github.com/login/oauth/access_token",
		}
		conf.Scopes = []string{"read:user", "user:email"}
	}
	return conf, nil
}

// FlowState is the per-attempt secret material kept in the session
// between the redirect and the callback.
type FlowState struct {
	State    string
	Nonce    string
	Verifier string
}

// Begin returns the authorization URL and the state to remember.
func (p *Provider) Begin(ctx context.Context) (string, FlowState, error) {
	conf, err := p.oauthConfig(ctx)
	if err != nil {
		return "", FlowState{}, err
	}
	fs := FlowState{
		State:    oauth2.GenerateVerifier(),
		Nonce:    oauth2.GenerateVerifier(),
		Verifier: oauth2.GenerateVerifier(),
	}
	opts := []oauth2.AuthCodeOption{oauth2.S256ChallengeOption(fs.Verifier)}
	if p.oidcProv != nil {
		opts = append(opts, oidc.Nonce(fs.Nonce))
	}
	return conf.AuthCodeURL(fs.State, opts...), fs, nil
}

// Finish exchanges the code and returns the provider's assertion. The
// caller has already checked the state parameter against the session.
func (p *Provider) Finish(ctx context.Context, code string, fs FlowState) (Login, error) {
	conf, err := p.oauthConfig(ctx)
	if err != nil {
		return Login{}, err
	}
	token, err := conf.Exchange(ctx, code, oauth2.VerifierOption(fs.Verifier))
	if err != nil {
		return Login{}, fmt.Errorf("token exchange with %s: %w", p.Key, err)
	}
	if p.oidcProv == nil {
		return p.finishGitHub(ctx, token)
	}
	return p.finishOIDC(ctx, token, fs.Nonce)
}

func (p *Provider) finishOIDC(ctx context.Context, token *oauth2.Token, nonce string) (Login, error) {
	raw, ok := token.Extra("id_token").(string)
	if !ok {
		return Login{}, fmt.Errorf("%s returned no id_token", p.Key)
	}
	verifier := p.oidcProv.Verifier(&oidc.Config{ClientID: p.client.ClientID})
	idToken, err := verifier.Verify(ctx, raw)
	if err != nil {
		return Login{}, fmt.Errorf("verify id_token from %s: %w", p.Key, err)
	}
	if idToken.Nonce != nonce {
		return Login{}, fmt.Errorf("id_token nonce mismatch from %s", p.Key)
	}
	var claims struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		Name          string `json:"name"`
		Picture       string `json:"picture"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return Login{}, fmt.Errorf("parse claims from %s: %w", p.Key, err)
	}
	return Login{
		Provider:      p.Key,
		Subject:       idToken.Subject,
		Email:         claims.Email,
		EmailVerified: claims.EmailVerified || p.trustEmail,
		Name:          claims.Name,
		AvatarURL:     claims.Picture,
	}, nil
}

// finishGitHub reads the user and their verified primary email from
// the REST API; GitHub is plain OAuth2, not OIDC.
func (p *Provider) finishGitHub(ctx context.Context, token *oauth2.Token) (Login, error) {
	var user struct {
		ID        int64  `json:"id"`
		Login     string `json:"login"`
		Name      string `json:"name"`
		AvatarURL string `json:"avatar_url"`
	}
	if err := githubGet(ctx, token, "https://api.github.com/user", &user); err != nil {
		return Login{}, err
	}
	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if err := githubGet(ctx, token, "https://api.github.com/user/emails", &emails); err != nil {
		return Login{}, err
	}
	login := Login{
		Provider:  "github",
		Subject:   strconv.FormatInt(user.ID, 10),
		Name:      user.Name,
		AvatarURL: user.AvatarURL,
	}
	if login.Name == "" {
		login.Name = user.Login
	}
	for _, e := range emails {
		if e.Verified && (login.Email == "" || e.Primary) {
			login.Email = e.Email
			login.EmailVerified = true
		}
	}
	return login, nil
}

func githubGet(ctx context.Context, token *oauth2.Token, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("github api %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("github api %s: status %d: %s", url, resp.StatusCode, body)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
