package auth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lporcheron/quorum/internal/config"
)

const (
	testClientID     = "quorum-client"
	testClientSecret = "quorum-secret"
	testRedirect     = "https://quorum.example/auth/test/callback"
	testCode         = "good-code"
	testAccessToken  = "access-token"
)

// testKeys are generated once: RSA generation dominates the runtime
// of these tests otherwise.
var testKeys = sync.OnceValue(func() [2]*rsa.PrivateKey {
	var keys [2]*rsa.PrivateKey
	for i := range keys {
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic(err) // test setup only
		}
		keys[i] = k
	}
	return keys
})

// fakeIdP is an OpenID Connect provider serving discovery, JWKS and a
// token endpoint that enforces the code, client credentials and PKCE.
type fakeIdP struct {
	t   *testing.T
	srv *httptest.Server
	key *rsa.PrivateKey

	// discoveredIssuer is the issuer the discovery document advertises
	// (defaults to the server URL).
	discoveredIssuer string
	// claims builds the id_token claims; nonce comes from the
	// authorization URL of the flow under test.
	claims func(nonce string) map[string]any
	// signer signs the id_token (defaults to key, the published one).
	signer    *rsa.PrivateKey
	noIDToken bool

	// challenge and nonce are recorded from the authorization URL.
	challenge string
	nonce     string
}

func newFakeIdP(t *testing.T) *fakeIdP {
	t.Helper()
	f := &fakeIdP{t: t, key: testKeys()[0]}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	f.discoveredIssuer = f.srv.URL
	f.claims = func(nonce string) map[string]any { return f.defaultClaims(f.srv.URL, nonce) }
	return f
}

func (f *fakeIdP) defaultClaims(iss, nonce string) map[string]any {
	now := time.Now()
	return map[string]any{
		"iss":            iss,
		"sub":            "user-123",
		"aud":            testClientID,
		"iat":            now.Unix(),
		"exp":            now.Add(time.Hour).Unix(),
		"nonce":          nonce,
		"email":          "alice@example.com",
		"email_verified": true,
		"name":           "Alice Example",
		"picture":        "https://example.com/alice.png",
	}
}

func (f *fakeIdP) serve(w http.ResponseWriter, r *http.Request) {
	switch {
	case strings.HasSuffix(r.URL.Path, "/.well-known/openid-configuration"):
		writeJSON(w, map[string]any{
			"issuer":                                f.discoveredIssuer,
			"authorization_endpoint":                f.srv.URL + "/authorize",
			"token_endpoint":                        f.srv.URL + "/token",
			"jwks_uri":                              f.srv.URL + "/jwks",
			"response_types_supported":              []string{"code"},
			"subject_types_supported":               []string{"public"},
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	case r.URL.Path == "/jwks":
		writeJSON(w, map[string]any{"keys": []any{jwk(f.key)}})
	case r.URL.Path == "/token":
		if !validTokenRequest(r, f.challenge) {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		resp := map[string]any{"access_token": testAccessToken, "token_type": "Bearer", "expires_in": 3600}
		if !f.noIDToken {
			signer := f.signer
			if signer == nil {
				signer = f.key
			}
			resp["id_token"] = signJWT(f.t, signer, f.claims(f.nonce))
		}
		writeJSON(w, resp)
	default:
		http.NotFound(w, r)
	}
}

// begin starts a flow and records what the IdP would receive on its
// authorization endpoint.
func (f *fakeIdP) begin(t *testing.T, p *Provider) FlowState {
	t.Helper()
	authURL, fs, err := p.Begin(context.Background())
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("parse auth URL: %v", err)
	}
	f.challenge = u.Query().Get("code_challenge")
	f.nonce = u.Query().Get("nonce")
	return fs
}

func (f *fakeIdP) provider() *Provider {
	return &Provider{
		Key: "oidc", Label: "Test IdP",
		client:      config.OAuthClient{ClientID: testClientID, ClientSecret: testClientSecret},
		redirectURL: testRedirect,
		issuer:      f.srv.URL,
	}
}

// validTokenRequest checks what a real authorization server checks on
// the code exchange: grant, code, redirect, client and PKCE verifier.
func validTokenRequest(r *http.Request, challenge string) bool {
	if err := r.ParseForm(); err != nil {
		return false
	}
	id, secret, ok := r.BasicAuth()
	if !ok {
		id, secret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	}
	sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
	return r.PostForm.Get("grant_type") == "authorization_code" &&
		r.PostForm.Get("code") == testCode &&
		r.PostForm.Get("redirect_uri") == testRedirect &&
		id == testClientID && secret == testClientSecret &&
		challenge != "" && base64.RawURLEncoding.EncodeToString(sum[:]) == challenge
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v) //nolint:errcheck // test server
}

func jwk(k *rsa.PrivateKey) map[string]string {
	enc := base64.RawURLEncoding
	return map[string]string{
		"kty": "RSA", "alg": "RS256", "use": "sig", "kid": "test-key",
		"n": enc.EncodeToString(k.N.Bytes()),
		"e": enc.EncodeToString(big.NewInt(int64(k.E)).Bytes()),
	}
}

func signJWT(t *testing.T, k *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	enc := base64.RawURLEncoding
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": "test-key"})
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	signing := enc.EncodeToString(header) + "." + enc.EncodeToString(payload)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, k, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return signing + "." + enc.EncodeToString(sig)
}

func TestOIDCBeginAuthURL(t *testing.T) {
	f := newFakeIdP(t)
	authURL, fs, err := f.provider().Begin(context.Background())
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	q := u.Query()
	if got := u.Scheme + "://" + u.Host + u.Path; got != f.srv.URL+"/authorize" {
		t.Errorf("endpoint = %q, want the discovered authorization endpoint", got)
	}
	want := map[string]string{
		"response_type":         "code",
		"client_id":             testClientID,
		"redirect_uri":          testRedirect,
		"scope":                 "openid email profile",
		"state":                 fs.State,
		"nonce":                 fs.Nonce,
		"code_challenge_method": "S256",
	}
	for k, v := range want {
		if q.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, q.Get(k), v)
		}
	}
	sum := sha256.Sum256([]byte(fs.Verifier))
	if q.Get("code_challenge") != base64.RawURLEncoding.EncodeToString(sum[:]) {
		t.Error("code_challenge is not the S256 of the flow verifier")
	}
	if fs.State == "" || fs.Nonce == "" || fs.Verifier == "" ||
		fs.State == fs.Nonce || fs.State == fs.Verifier || fs.Nonce == fs.Verifier {
		t.Errorf("flow secrets must be non-empty and distinct: %+v", fs)
	}
}

func TestOIDCFinish(t *testing.T) {
	f := newFakeIdP(t)
	p := f.provider()
	fs := f.begin(t, p)

	got, err := p.Finish(context.Background(), testCode, fs)
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	want := Login{
		Provider: "oidc", Subject: "user-123",
		Email: "alice@example.com", EmailVerified: true,
		Name: "Alice Example", AvatarURL: "https://example.com/alice.png",
	}
	if got != want {
		t.Errorf("Finish = %+v, want %+v", got, want)
	}
}

func TestOIDCFinishEmailVerified(t *testing.T) {
	for _, tc := range []struct {
		name       string
		claim      any // email_verified; nil drops it
		trustEmail bool
		want       bool
	}{
		{"claim true", true, false, true},
		{"claim false", false, false, false},
		{"claim absent", nil, false, false},
		{"claim false, trusted provider", false, true, true},
		{"claim absent, trusted provider", nil, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeIdP(t)
			f.claims = func(nonce string) map[string]any {
				c := f.defaultClaims(f.srv.URL, nonce)
				if tc.claim == nil {
					delete(c, "email_verified")
				} else {
					c["email_verified"] = tc.claim
				}
				return c
			}
			p := f.provider()
			p.trustEmail = tc.trustEmail
			fs := f.begin(t, p)
			got, err := p.Finish(context.Background(), testCode, fs)
			if err != nil {
				t.Fatalf("Finish: %v", err)
			}
			if got.EmailVerified != tc.want {
				t.Errorf("EmailVerified = %v, want %v", got.EmailVerified, tc.want)
			}
		})
	}
}

func TestOIDCFinishRejects(t *testing.T) {
	claim := func(k string, v any) func(*fakeIdP) {
		return func(f *fakeIdP) {
			f.claims = func(nonce string) map[string]any {
				c := f.defaultClaims(f.srv.URL, nonce)
				c[k] = v
				return c
			}
		}
	}
	for _, tc := range []struct {
		name  string
		setup func(*fakeIdP)
		flow  func(*FlowState)
		code  string
	}{
		{name: "wrong code", code: "stolen-code"},
		{name: "wrong PKCE verifier", flow: func(fs *FlowState) { fs.Verifier = "another-verifier-another-verifier-00" }},
		{name: "nonce from another flow", flow: func(fs *FlowState) { fs.Nonce = "another-nonce" }},
		{name: "replayed nonce claim", setup: claim("nonce", "old-nonce")},
		{name: "wrong audience", setup: claim("aud", "another-client")},
		{name: "wrong issuer", setup: claim("iss", "https://evil.example")},
		{name: "expired", setup: claim("exp", time.Now().Add(-time.Hour).Unix())},
		{name: "unknown signing key", setup: func(f *fakeIdP) { f.signer = testKeys()[1] }},
		{name: "no id_token", setup: func(f *fakeIdP) { f.noIDToken = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeIdP(t)
			if tc.setup != nil {
				tc.setup(f)
			}
			p := f.provider()
			fs := f.begin(t, p)
			if tc.flow != nil {
				tc.flow(&fs)
			}
			code := testCode
			if tc.code != "" {
				code = tc.code
			}
			got, err := p.Finish(context.Background(), code, fs)
			if err == nil {
				t.Fatalf("Finish accepted the sign-in: %+v", got)
			}
			if got != (Login{}) {
				t.Errorf("Finish returned a login alongside the error: %+v", got)
			}
		})
	}
}

func TestOIDCDiscoveryFailure(t *testing.T) {
	f := newFakeIdP(t)
	p := f.provider()
	p.issuer = f.srv.URL + "/missing"
	if _, _, err := p.Begin(context.Background()); err == nil {
		t.Fatal("Begin succeeded without a discovery document")
	}
}

// Microsoft's multi-tenant endpoints advertise a templated issuer and
// sign each token with the issuer of the user's own tenant.
func TestMicrosoftMultiTenant(t *testing.T) {
	newTenantIdP := func(t *testing.T, iss func(base string) string, tid any) (*fakeIdP, *Provider) {
		f := newFakeIdP(t)
		f.discoveredIssuer = f.srv.URL + "/{tenantid}/v2.0"
		f.claims = func(nonce string) map[string]any {
			c := f.defaultClaims(iss(f.srv.URL), nonce)
			delete(c, "email_verified") // Entra omits it
			if tid != nil {
				c["tid"] = tid
			}
			return c
		}
		p := f.provider()
		p.Key = "microsoft"
		p.issuer = f.srv.URL + "/common/v2.0"
		p.relaxIssuer = true
		p.trustEmail = true
		return f, p
	}
	tenantA := func(base string) string { return base + "/tenant-a/v2.0" }

	t.Run("accepts the token's own tenant", func(t *testing.T) {
		f, p := newTenantIdP(t, tenantA, "tenant-a")
		fs := f.begin(t, p)
		got, err := p.Finish(context.Background(), testCode, fs)
		if err != nil {
			t.Fatalf("Finish: %v", err)
		}
		if got.Provider != "microsoft" || got.Subject != "user-123" || !got.EmailVerified {
			t.Errorf("Finish = %+v", got)
		}
	})

	for _, tc := range []struct {
		name string
		iss  func(base string) string
		tid  any
	}{
		{"issuer of another tenant", tenantA, "tenant-b"},
		{"no tenant claim", tenantA, nil},
		{"issuer on another host", func(string) string { return "https://evil.example/tenant-a/v2.0" }, "tenant-a"},
		{"templated issuer left as is", func(base string) string { return base + "/{tenantid}/v2.0" }, "tenant-a"},
	} {
		t.Run("rejects "+tc.name, func(t *testing.T) {
			f, p := newTenantIdP(t, tc.iss, tc.tid)
			fs := f.begin(t, p)
			if got, err := p.Finish(context.Background(), testCode, fs); err == nil {
				t.Fatalf("Finish accepted the sign-in: %+v", got)
			}
		})
	}
}

// fakeGitHub serves GitHub's OAuth2 token endpoint and the two REST
// resources the provider reads.
type fakeGitHub struct {
	srv       *httptest.Server
	challenge string
	user      map[string]any
	emails    []map[string]any
	apiStatus int
}

func newFakeGitHub(t *testing.T) (*fakeGitHub, *Provider) {
	t.Helper()
	g := &fakeGitHub{
		user:      map[string]any{"id": 4242, "login": "alice", "name": "Alice Hub", "avatar_url": "https://avatars.example/alice"},
		apiStatus: http.StatusOK,
	}
	g.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login/oauth/access_token":
			if !validTokenRequest(r, g.challenge) {
				http.Error(w, `{"error":"bad_verification_code"}`, http.StatusBadRequest)
				return
			}
			writeJSON(w, map[string]any{"access_token": testAccessToken, "token_type": "bearer"})
		case "/user", "/user/emails":
			if r.Header.Get("Authorization") != "Bearer "+testAccessToken {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			if g.apiStatus != http.StatusOK {
				http.Error(w, "boom", g.apiStatus)
				return
			}
			if r.URL.Path == "/user" {
				writeJSON(w, g.user)
			} else {
				writeJSON(w, g.emails)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(g.srv.Close)
	p := NewProviders(config.Config{GitHub: config.OAuthClient{ClientID: testClientID, ClientSecret: testClientSecret}}, "https://quorum.example")[0]
	p.redirectURL = testRedirect
	p.endpoint.AuthURL = g.srv.URL + "/login/oauth/authorize"
	p.endpoint.TokenURL = g.srv.URL + "/login/oauth/access_token"
	p.apiURL = g.srv.URL
	return g, p
}

func (g *fakeGitHub) begin(t *testing.T, p *Provider) FlowState {
	t.Helper()
	authURL, fs, err := p.Begin(context.Background())
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("parse auth URL: %v", err)
	}
	q := u.Query()
	if q.Get("nonce") != "" {
		t.Error("GitHub is plain OAuth2: the auth URL must not carry a nonce")
	}
	if q.Get("scope") != "read:user user:email" || q.Get("state") != fs.State || q.Get("code_challenge_method") != "S256" {
		t.Errorf("unexpected auth URL query: %v", q)
	}
	g.challenge = q.Get("code_challenge")
	return fs
}

func TestGitHubFinish(t *testing.T) {
	email := func(addr string, primary, verified bool) map[string]any {
		return map[string]any{"email": addr, "primary": primary, "verified": verified}
	}
	for _, tc := range []struct {
		name         string
		emails       []map[string]any
		userName     string
		wantEmail    string
		wantVerified bool
		wantName     string
	}{
		{
			name:     "verified primary wins over other verified",
			emails:   []map[string]any{email("old@example.com", false, true), email("main@example.com", true, true), email("new@example.com", false, true)},
			userName: "Alice Hub", wantEmail: "main@example.com", wantVerified: true, wantName: "Alice Hub",
		},
		{
			name:     "unverified primary is skipped",
			emails:   []map[string]any{email("main@example.com", true, false), email("alt@example.com", false, true)},
			userName: "Alice Hub", wantEmail: "alt@example.com", wantVerified: true, wantName: "Alice Hub",
		},
		{
			name:     "no verified email",
			emails:   []map[string]any{email("main@example.com", true, false)},
			userName: "Alice Hub", wantEmail: "", wantVerified: false, wantName: "Alice Hub",
		},
		{
			name:     "login stands in for an empty name",
			emails:   []map[string]any{email("main@example.com", true, true)},
			userName: "", wantEmail: "main@example.com", wantVerified: true, wantName: "alice",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g, p := newFakeGitHub(t)
			g.emails = tc.emails
			g.user["name"] = tc.userName
			fs := g.begin(t, p)
			got, err := p.Finish(context.Background(), testCode, fs)
			if err != nil {
				t.Fatalf("Finish: %v", err)
			}
			want := Login{
				Provider: "github", Subject: "4242",
				Email: tc.wantEmail, EmailVerified: tc.wantVerified,
				Name: tc.wantName, AvatarURL: "https://avatars.example/alice",
			}
			if got != want {
				t.Errorf("Finish = %+v, want %+v", got, want)
			}
		})
	}
}

func TestGitHubFinishRejects(t *testing.T) {
	t.Run("wrong PKCE verifier", func(t *testing.T) {
		g, p := newFakeGitHub(t)
		fs := g.begin(t, p)
		fs.Verifier = "another-verifier-another-verifier-00"
		if _, err := p.Finish(context.Background(), testCode, fs); err == nil {
			t.Fatal("Finish accepted a mismatched verifier")
		}
	})
	t.Run("API error", func(t *testing.T) {
		g, p := newFakeGitHub(t)
		g.apiStatus = http.StatusInternalServerError
		fs := g.begin(t, p)
		if _, err := p.Finish(context.Background(), testCode, fs); err == nil {
			t.Fatal("Finish ignored a failing GitHub API")
		}
	})
}

func TestNewProviders(t *testing.T) {
	client := config.OAuthClient{ClientID: "id", ClientSecret: "secret"}
	all := config.Config{
		Google: client, GitHub: client, Microsoft: client,
		MicrosoftTenant: "common",
		OIDC:            config.OIDC{IssuerURL: "https://sso.example", Name: "Corp SSO", OAuthClient: client},
	}

	got := NewProviders(all, "https://quorum.example")
	var keys []string
	for _, p := range got {
		keys = append(keys, p.Key)
		if want := "https://quorum.example/auth/" + p.Key + "/callback"; p.redirectURL != want {
			t.Errorf("%s redirect = %q, want %q", p.Key, p.redirectURL, want)
		}
	}
	if strings.Join(keys, ",") != "google,github,microsoft,oidc" {
		t.Errorf("providers = %v", keys)
	}
	if got[3].Label != "Corp SSO" || got[3].issuer != "https://sso.example" {
		t.Errorf("oidc provider = %+v", got[3])
	}

	if got := NewProviders(config.Config{Google: config.OAuthClient{ClientID: "id"}}, "https://quorum.example"); len(got) != 0 {
		t.Errorf("a client without secret must stay disabled, got %d providers", len(got))
	}
	if got := NewProviders(config.Config{OIDC: config.OIDC{OAuthClient: client}}, "https://quorum.example"); len(got) != 0 {
		t.Errorf("OIDC without an issuer must stay disabled, got %d providers", len(got))
	}

	for tenant, relaxed := range map[string]bool{
		"common": true, "organizations": true, "consumers": true,
		"8eaef023-2b34-4da1-9baa-8bc8c9d6a490": false,
	} {
		p := NewProviders(config.Config{Microsoft: client, MicrosoftTenant: tenant}, "https://quorum.example")[0]
		if p.relaxIssuer != relaxed || !p.trustEmail {
			t.Errorf("tenant %s: relaxIssuer = %v, trustEmail = %v", tenant, p.relaxIssuer, p.trustEmail)
		}
		if want := "https://login.microsoftonline.com/" + tenant + "/v2.0"; p.issuer != want {
			t.Errorf("tenant %s: issuer = %q", tenant, p.issuer)
		}
	}
}
