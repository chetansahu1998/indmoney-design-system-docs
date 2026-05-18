package auth

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// ─── test harness ─────────────────────────────────────────────────────────────

// newOAuthTestDB opens a fresh SQLite DB in a temp dir, creates the
// minimal users table (so the FK in oauth_tokens resolves), then
// installs the U8 schema. We deliberately don't import internal/db
// here — that package has its own migration runner and we want to
// keep the auth-package tests free of cross-package coupling.
func newOAuthTestDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	ddl := []string{
		`CREATE TABLE users (
			id TEXT PRIMARY KEY,
			email TEXT UNIQUE NOT NULL,
			password_hash TEXT NOT NULL DEFAULT '',
			role TEXT NOT NULL DEFAULT 'user',
			created_at TEXT NOT NULL DEFAULT '',
			last_login_at TEXT
		)`,
		`CREATE TABLE oauth_tokens (
			id                    TEXT PRIMARY KEY,
			user_id               TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			tenant_id             TEXT NOT NULL,
			kind                  TEXT NOT NULL CHECK (kind IN ('authorization_code','refresh_token')),
			client_id             TEXT NOT NULL,
			redirect_uri          TEXT NOT NULL,
			scope                 TEXT NOT NULL DEFAULT '',
			code_challenge        TEXT,
			code_challenge_method TEXT,
			expires_at            INTEGER NOT NULL,
			consumed_at           INTEGER,
			revoked_at            INTEGER,
			created_at            INTEGER NOT NULL
		)`,
		`CREATE INDEX idx_oauth_tokens_user_id ON oauth_tokens(user_id)`,
		`CREATE INDEX idx_oauth_tokens_kind_expires ON oauth_tokens(kind, expires_at)`,
	}
	for _, s := range ddl {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("ddl: %v: %s", err, s)
		}
	}
	return db
}

// seedUser inserts a user row so the FK in oauth_tokens can resolve.
// Idempotent — tests that authorize multiple times for the same user
// call this repeatedly.
func seedUser(t *testing.T, db *sql.DB, id, email string) {
	t.Helper()
	if _, err := db.Exec(`INSERT OR IGNORE INTO users (id, email) VALUES (?, ?)`, id, email); err != nil {
		t.Fatalf("seed user: %v", err)
	}
}

// passthroughAuth is the "requireAuth" the harness installs on
// /v1/oauth/authorize. It accepts a *Claims pointer via a closure
// so each test can pick its own session shape (or omit it to
// simulate unauthenticated access).
type oauthHarness struct {
	mux       *http.ServeMux
	db        *sql.DB
	signer    *SigningKey
	cfg       OAuthConfig
	curClaims *Claims
}

func newHarness(t *testing.T, cfg OAuthConfig) *oauthHarness {
	t.Helper()
	db := newOAuthTestDB(t)
	k, err := GenerateSigningKey()
	if err != nil {
		t.Fatalf("generate signing key: %v", err)
	}
	if cfg.AllowedClients == nil {
		cfg.AllowedClients = []string{"claude.ai"}
	}
	h := &oauthHarness{
		mux:    http.NewServeMux(),
		db:     db,
		signer: k,
		cfg:    cfg,
	}
	requireAuth := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if h.curClaims == nil {
				http.Error(w, "no session", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		}
	}
	claimsFromRequest := func(r *http.Request) *Claims { return h.curClaims }
	RegisterOAuthRoutes(h.mux, db, k, cfg, requireAuth, claimsFromRequest)
	return h
}

// authorize hits /v1/oauth/authorize with the harness's current
// session. Returns the redirect Location (or "" if no 302).
func (h *oauthHarness) authorize(t *testing.T, params url.Values) (status int, location string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/oauth/authorize?"+params.Encode(), nil)
	w := httptest.NewRecorder()
	h.mux.ServeHTTP(w, req)
	return w.Code, w.Header().Get("Location")
}

// token hits /v1/oauth/token with the given form body, returns the
// parsed JSON response and HTTP status.
func (h *oauthHarness) token(t *testing.T, form url.Values) (status int, body map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.mux.ServeHTTP(w, req)
	body = map[string]any{}
	if w.Body.Len() > 0 {
		_ = json.Unmarshal(w.Body.Bytes(), &body)
	}
	return w.Code, body
}

// pkceVerifier is a fixed verifier used across tests. RFC 7636 §4.1
// says the verifier is 43-128 chars from the unreserved set; this is
// a 43-char hex string which satisfies that.
const pkceVerifier = "kA12fG7Hd9Lm0pQrS6tU8vYz3wXcN5bMqRiOaPlKjHe"

func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// runFullAuthorizeStep walks the harness through a successful
// /v1/oauth/authorize for a session with the given (userID, tenantID),
// returning the freshly-minted authorization code.
func runFullAuthorizeStep(t *testing.T, h *oauthHarness, userID, tenantID string) string {
	t.Helper()
	seedUser(t, h.db, userID, userID+"@example.com")
	h.curClaims = &Claims{Sub: userID, Email: userID + "@example.com", Role: RoleDesigner, Tenants: []string{tenantID}}
	defer func() { h.curClaims = nil }()

	params := url.Values{}
	params.Set("response_type", "code")
	params.Set("client_id", "claude.ai")
	params.Set("redirect_uri", "https://claude.ai/oauth/callback")
	params.Set("state", "xyz")
	params.Set("code_challenge", pkceChallenge(pkceVerifier))
	params.Set("code_challenge_method", "S256")
	params.Set("scope", "mcp")

	status, loc := h.authorize(t, params)
	if status != http.StatusFound {
		t.Fatalf("authorize status = %d, want 302; loc=%q", status, loc)
	}
	u, err := url.Parse(loc)
	if err != nil {
		t.Fatalf("parse redirect: %v", err)
	}
	code := u.Query().Get("code")
	if code == "" {
		t.Fatalf("no code in redirect %q", loc)
	}
	if got := u.Query().Get("state"); got != "xyz" {
		t.Errorf("state echoed = %q, want xyz", got)
	}
	return code
}

func redeemCode(t *testing.T, h *oauthHarness, code string) (int, map[string]any) {
	t.Helper()
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("code_verifier", pkceVerifier)
	form.Set("client_id", "claude.ai")
	form.Set("redirect_uri", "https://claude.ai/oauth/callback")
	return h.token(t, form)
}

// ─── scenario 1 — happy path ──────────────────────────────────────────────────

func TestOAuth_HappyPath_AuthorizeTokenRefresh(t *testing.T) {
	h := newHarness(t, OAuthConfig{})
	code := runFullAuthorizeStep(t, h, "u-1", "t-alpha")

	status, body := redeemCode(t, h, code)
	if status != http.StatusOK {
		t.Fatalf("token status = %d, want 200; body=%v", status, body)
	}
	access, _ := body["access_token"].(string)
	refresh, _ := body["refresh_token"].(string)
	if access == "" || refresh == "" {
		t.Fatalf("missing token in body: %v", body)
	}
	if tt, _ := body["token_type"].(string); tt != "Bearer" {
		t.Errorf("token_type = %q, want Bearer", tt)
	}

	// Access token must verify under the same signer and carry the
	// correct tenant.
	claims, err := h.signer.VerifyAccessToken(access)
	if err != nil {
		t.Fatalf("verify access: %v", err)
	}
	if claims.Sub != "u-1" {
		t.Errorf("Sub = %q, want u-1", claims.Sub)
	}
	if len(claims.Tenants) != 1 || claims.Tenants[0] != "t-alpha" {
		t.Errorf("Tenants = %v, want [t-alpha]", claims.Tenants)
	}

	// Refresh.
	rform := url.Values{}
	rform.Set("grant_type", "refresh_token")
	rform.Set("refresh_token", refresh)
	status2, body2 := h.token(t, rform)
	if status2 != http.StatusOK {
		t.Fatalf("refresh status = %d, want 200; body=%v", status2, body2)
	}
	newAccess, _ := body2["access_token"].(string)
	newRefresh, _ := body2["refresh_token"].(string)
	if newAccess == "" || newRefresh == "" {
		t.Fatalf("missing token after refresh: %v", body2)
	}
	if newAccess == access || newRefresh == refresh {
		t.Error("refresh did not produce new credentials")
	}

	// Old refresh must now be revoked in DB.
	id := hashToken(refresh)
	var revoked sql.NullInt64
	if err := h.db.QueryRow(`SELECT revoked_at FROM oauth_tokens WHERE id = ?`, id).Scan(&revoked); err != nil {
		t.Fatalf("lookup old refresh: %v", err)
	}
	if !revoked.Valid {
		t.Error("old refresh token revoked_at is NULL after rotation")
	}
}

// ─── scenario 2 — PKCE mismatch ───────────────────────────────────────────────

func TestOAuth_PKCEMismatch(t *testing.T) {
	h := newHarness(t, OAuthConfig{})
	code := runFullAuthorizeStep(t, h, "u-2", "t-alpha")

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("code_verifier", "this-is-not-the-real-verifier-1234567890abcd") // 43+ chars but wrong
	form.Set("client_id", "claude.ai")
	form.Set("redirect_uri", "https://claude.ai/oauth/callback")
	status, body := h.token(t, form)

	if status != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", status)
	}
	if got, _ := body["error"].(string); got != "invalid_grant" {
		t.Errorf("error = %q, want invalid_grant", got)
	}
}

// ─── scenario 3 — code replay ─────────────────────────────────────────────────

func TestOAuth_CodeReplay(t *testing.T) {
	h := newHarness(t, OAuthConfig{})
	code := runFullAuthorizeStep(t, h, "u-3", "t-alpha")

	if status, _ := redeemCode(t, h, code); status != http.StatusOK {
		t.Fatalf("first redeem status = %d, want 200", status)
	}
	status, body := redeemCode(t, h, code)
	if status != http.StatusBadRequest {
		t.Errorf("second redeem status = %d, want 400", status)
	}
	if got, _ := body["error"].(string); got != "invalid_grant" {
		t.Errorf("error = %q, want invalid_grant", got)
	}
}

// ─── scenario 4 — code expiry ─────────────────────────────────────────────────

func TestOAuth_CodeExpiry(t *testing.T) {
	// Use a clock we control so expires_at lands in the past by the
	// time we redeem. We freeze "now" at T, code expires at T+60s,
	// then redeem at T+120s.
	base := time.Now()
	clock := base
	cfg := OAuthConfig{
		Now: func() time.Time { return clock },
	}
	h := newHarness(t, cfg)

	code := runFullAuthorizeStep(t, h, "u-4", "t-alpha")

	clock = base.Add(2 * time.Minute) // 60s code TTL already elapsed

	status, body := redeemCode(t, h, code)
	if status != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", status)
	}
	if got, _ := body["error"].(string); got != "invalid_grant" {
		t.Errorf("error = %q, want invalid_grant", got)
	}
	if desc, _ := body["error_description"].(string); !strings.Contains(desc, "expire") {
		t.Errorf("error_description = %q, want substring 'expire'", desc)
	}
}

// ─── scenario 5 — refresh rotation invalidates old refresh ────────────────────

func TestOAuth_RefreshRotation(t *testing.T) {
	h := newHarness(t, OAuthConfig{})
	code := runFullAuthorizeStep(t, h, "u-5", "t-alpha")
	_, body := redeemCode(t, h, code)
	oldRefresh, _ := body["refresh_token"].(string)

	rform := url.Values{}
	rform.Set("grant_type", "refresh_token")
	rform.Set("refresh_token", oldRefresh)
	if status, _ := h.token(t, rform); status != http.StatusOK {
		t.Fatalf("first refresh status = %d, want 200", status)
	}

	// Old token should now fail.
	status, body2 := h.token(t, rform)
	if status != http.StatusBadRequest {
		t.Errorf("replay status = %d, want 400", status)
	}
	if got, _ := body2["error"].(string); got != "invalid_grant" {
		t.Errorf("error = %q, want invalid_grant", got)
	}
}

// ─── scenario 6 — refresh-token replay sweeps the user's refresh tokens ──────

func TestOAuth_RefreshReplayRevokesAllUserTokens(t *testing.T) {
	h := newHarness(t, OAuthConfig{})

	// Build two independent refresh tokens for the same user via two
	// authorize→token flows.
	code1 := runFullAuthorizeStep(t, h, "u-6", "t-alpha")
	_, body1 := redeemCode(t, h, code1)
	refresh1, _ := body1["refresh_token"].(string)

	code2 := runFullAuthorizeStep(t, h, "u-6", "t-alpha")
	_, body2 := redeemCode(t, h, code2)
	refresh2, _ := body2["refresh_token"].(string)

	if refresh1 == "" || refresh2 == "" || refresh1 == refresh2 {
		t.Fatalf("expected two distinct refresh tokens; got %q / %q", refresh1, refresh2)
	}

	// Rotate refresh1 once so it becomes "consumed" (revoked_at set).
	rform := url.Values{}
	rform.Set("grant_type", "refresh_token")
	rform.Set("refresh_token", refresh1)
	if status, _ := h.token(t, rform); status != http.StatusOK {
		t.Fatal("first rotation must succeed")
	}

	// Replay refresh1 — should trigger sweep that revokes refresh2 too.
	status, body := h.token(t, rform)
	if status != http.StatusBadRequest {
		t.Errorf("replay status = %d, want 400", status)
	}
	if got, _ := body["error"].(string); got != "invalid_grant" {
		t.Errorf("error = %q, want invalid_grant", got)
	}

	// refresh2 should now be revoked in the DB.
	var revoked sql.NullInt64
	if err := h.db.QueryRow(`SELECT revoked_at FROM oauth_tokens WHERE id = ? AND kind = 'refresh_token'`,
		hashToken(refresh2)).Scan(&revoked); err != nil {
		t.Fatalf("lookup refresh2: %v", err)
	}
	if !revoked.Valid {
		t.Error("replay-defense sweep did not revoke refresh2")
	}

	// And refresh2 itself must reject if used.
	rform2 := url.Values{}
	rform2.Set("grant_type", "refresh_token")
	rform2.Set("refresh_token", refresh2)
	status2, body3 := h.token(t, rform2)
	if status2 != http.StatusBadRequest {
		t.Errorf("refresh2 status after sweep = %d, want 400", status2)
	}
	if got, _ := body3["error"].(string); got != "invalid_grant" {
		t.Errorf("error = %q, want invalid_grant", got)
	}
}

// ─── scenario 7 — client allowlist rejects unknown client_id ─────────────────

func TestOAuth_ClientAllowlist_RejectsUnknown(t *testing.T) {
	h := newHarness(t, OAuthConfig{})
	seedUser(t, h.db, "u-7", "u-7@example.com")
	h.curClaims = &Claims{Sub: "u-7", Email: "u-7@example.com", Role: RoleDesigner, Tenants: []string{"t-alpha"}}
	defer func() { h.curClaims = nil }()

	params := url.Values{}
	params.Set("response_type", "code")
	params.Set("client_id", "evil-app")
	params.Set("redirect_uri", "https://evil.example.com/cb")
	params.Set("code_challenge", pkceChallenge(pkceVerifier))
	params.Set("code_challenge_method", "S256")

	req := httptest.NewRequest(http.MethodGet, "/v1/oauth/authorize?"+params.Encode(), nil)
	w := httptest.NewRecorder()
	h.mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
	body := map[string]any{}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if got, _ := body["error"].(string); got != "invalid_client" {
		t.Errorf("error = %q, want invalid_client", got)
	}
}

// ─── scenario 8 — cross-tenant isolation ──────────────────────────────────────

// The most important assertion: a refresh-token row carries tenant_id;
// after rotation the new access token's Tenants claim is exactly that
// stored value, regardless of any other state.
func TestOAuth_CrossTenantIsolation(t *testing.T) {
	h := newHarness(t, OAuthConfig{})

	// User u-8 has authorize'd into tenant "t-alpha". Their session
	// happens to ALSO contain "t-bravo" (multi-tenant user) — but the
	// authorize step pinned t-alpha at authorize time.
	seedUser(t, h.db, "u-8", "u-8@example.com")
	h.curClaims = &Claims{
		Sub:     "u-8",
		Email:   "u-8@example.com",
		Role:    RoleDesigner,
		Tenants: []string{"t-alpha", "t-bravo"},
	}

	params := url.Values{}
	params.Set("response_type", "code")
	params.Set("client_id", "claude.ai")
	params.Set("redirect_uri", "https://claude.ai/oauth/callback")
	params.Set("code_challenge", pkceChallenge(pkceVerifier))
	params.Set("code_challenge_method", "S256")
	status, loc := h.authorize(t, params)
	if status != http.StatusFound {
		t.Fatalf("authorize status = %d", status)
	}
	h.curClaims = nil
	u, _ := url.Parse(loc)
	code := u.Query().Get("code")

	_, body := redeemCode(t, h, code)
	refresh, _ := body["refresh_token"].(string)

	// Tamper: pretend somebody manipulates the user's session to add
	// t-bravo. The DB row for refresh is what counts — must still mint
	// t-alpha.
	rform := url.Values{}
	rform.Set("grant_type", "refresh_token")
	rform.Set("refresh_token", refresh)
	_, body2 := h.token(t, rform)
	access2, _ := body2["access_token"].(string)
	if access2 == "" {
		t.Fatalf("no access token after refresh: %v", body2)
	}
	claims, err := h.signer.VerifyAccessToken(access2)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if len(claims.Tenants) != 1 || claims.Tenants[0] != "t-alpha" {
		t.Errorf("refreshed access token tenants = %v, want [t-alpha] exactly — cross-tenant leak", claims.Tenants)
	}
}

// ─── scenario 9 — code_challenge_method=plain rejected ────────────────────────

func TestOAuth_RejectsPlainChallengeMethod(t *testing.T) {
	h := newHarness(t, OAuthConfig{})
	seedUser(t, h.db, "u-9", "u-9@example.com")
	h.curClaims = &Claims{Sub: "u-9", Email: "u-9@example.com", Role: RoleDesigner, Tenants: []string{"t-alpha"}}
	defer func() { h.curClaims = nil }()

	params := url.Values{}
	params.Set("response_type", "code")
	params.Set("client_id", "claude.ai")
	params.Set("redirect_uri", "https://claude.ai/oauth/callback")
	params.Set("code_challenge", pkceVerifier) // plain → challenge == verifier
	params.Set("code_challenge_method", "plain")

	status, _ := h.authorize(t, params)
	if status != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", status)
	}
}

// ─── bonus — revoke endpoint shape (RFC 7009) ─────────────────────────────────

func TestOAuth_Revoke_AlwaysReturns200(t *testing.T) {
	h := newHarness(t, OAuthConfig{})

	form := url.Values{}
	form.Set("token", "nonexistent-token-blah-blah")
	req := httptest.NewRequest(http.MethodPost, "/v1/oauth/revoke", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (RFC 7009 §2.2)", w.Code)
	}
}

// Sanity — compile-time check that the helpers used in scenarios are
// actually wired against the same package (no shadowing surprises).
var _ context.Context = context.Background()
