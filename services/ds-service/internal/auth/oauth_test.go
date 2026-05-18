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
		`CREATE TABLE tenants (
			id   TEXT PRIMARY KEY,
			name TEXT NOT NULL
		)`,
		// Seed both tenants the test harness uses so the FK
		// REFERENCES tenants(id) holds.
		`INSERT INTO tenants(id, name) VALUES ('t-alpha', 'Tenant Alpha')`,
		`INSERT INTO tenants(id, name) VALUES ('t-bravo', 'Tenant Bravo')`,
		`CREATE TABLE oauth_tokens (
			id                    TEXT PRIMARY KEY,
			user_id               TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			tenant_id             TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
			kind                  TEXT NOT NULL CHECK (kind IN ('authorization_code','refresh_token')),
			client_id             TEXT NOT NULL,
			redirect_uri          TEXT NOT NULL,
			scope                 TEXT NOT NULL DEFAULT '',
			code_challenge        TEXT,
			code_challenge_method TEXT,
			expires_at            INTEGER NOT NULL,
			consumed_at           INTEGER,
			revoked_at            INTEGER,
			created_at            INTEGER NOT NULL,
			last_access_jti       TEXT,
			parent_id             TEXT
		)`,
		`CREATE INDEX idx_oauth_tokens_user_id ON oauth_tokens(user_id)`,
		`CREATE INDEX idx_oauth_tokens_kind_expires ON oauth_tokens(kind, expires_at)`,
		`CREATE TABLE revoked_jtis (
			jti        TEXT PRIMARY KEY,
			revoked_at TEXT NOT NULL,
			revoked_by TEXT,
			reason     TEXT
		)`,
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
	if cfg.Clients == nil {
		// Test harness default: claude.ai with the redirect_uri the
		// existing scenarios use.
		cfg.Clients = []OAuthClient{{
			ID:                  "claude.ai",
			AllowedRedirectURIs: []string{"https://claude.ai/oauth/callback"},
		}}
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

// TestOAuth_MultiTenant_RequiresExplicitTenantID — finding #18 (P2).
// Previously the authorize handler silently pinned multi-tenant users
// to claims.Tenants[0]. With no UI signal of which tenant was being
// connected, and non-default tenants unreachable, the connector was
// half-broken for multi-tenant accounts. Fix: require tenant_id query
// param, enforce it's a member of claims.Tenants.
func TestOAuth_MultiTenant_RequiresExplicitTenantID(t *testing.T) {
	h := newHarness(t, OAuthConfig{})
	seedUser(t, h.db, "u-multi", "u-multi@example.com")
	h.curClaims = &Claims{
		Sub:     "u-multi",
		Email:   "u-multi@example.com",
		Role:    RoleDesigner,
		Tenants: []string{"t-alpha", "t-bravo"},
	}
	defer func() { h.curClaims = nil }()

	authzURL := func(extraParams map[string]string) string {
		p := url.Values{}
		p.Set("response_type", "code")
		p.Set("client_id", "claude.ai")
		p.Set("redirect_uri", "https://claude.ai/oauth/callback")
		p.Set("code_challenge", pkceChallenge(pkceVerifier))
		p.Set("code_challenge_method", "S256")
		for k, v := range extraParams {
			p.Set(k, v)
		}
		return "/v1/oauth/authorize?" + p.Encode()
	}

	cases := []struct {
		name       string
		params     map[string]string
		wantStatus int
		wantError  string
	}{
		{
			name:       "no_tenant_id",
			params:     map[string]string{},
			wantStatus: http.StatusBadRequest,
			wantError:  "invalid_request",
		},
		{
			name:       "non_member_tenant",
			params:     map[string]string{"tenant_id": "t-charlie"},
			wantStatus: http.StatusForbidden,
			wantError:  "invalid_request",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, authzURL(tc.params), nil)
			w := httptest.NewRecorder()
			h.mux.ServeHTTP(w, req)
			if w.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tc.wantStatus)
			}
			body := map[string]any{}
			_ = json.Unmarshal(w.Body.Bytes(), &body)
			if got, _ := body["error"].(string); got != tc.wantError {
				t.Errorf("error = %q, want %q", got, tc.wantError)
			}
		})
	}

	// Member tenant — accepted, 302 redirect with code.
	req := httptest.NewRequest(http.MethodGet, authzURL(map[string]string{"tenant_id": "t-bravo"}), nil)
	w := httptest.NewRecorder()
	h.mux.ServeHTTP(w, req)
	if w.Code != http.StatusFound {
		t.Fatalf("member tenant_id: status = %d, want 302; body=%s", w.Code, w.Body.String())
	}
	loc, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse location: %v", err)
	}
	code := loc.Query().Get("code")
	// Verify the stored row was pinned to t-bravo, not t-alpha.
	var storedTenant string
	if err := h.db.QueryRow(`SELECT tenant_id FROM oauth_tokens WHERE id = ?`, code).Scan(&storedTenant); err != nil {
		t.Fatalf("lookup code: %v", err)
	}
	if storedTenant != "t-bravo" {
		t.Errorf("stored tenant = %q, want t-bravo (caller's explicit pick, not Tenants[0])", storedTenant)
	}
}

// TestOAuth_TenantFK_DeleteCascadesToRows — finding #11 (P1). Migration
// 0045 retrofits REFERENCES tenants(id) ON DELETE CASCADE on the
// oauth_tokens.tenant_id column. Deleting a tenant must wipe its
// oauth_tokens rows; without the FK orphans would accumulate.
func TestOAuth_TenantFK_DeleteCascadesToRows(t *testing.T) {
	h := newHarness(t, OAuthConfig{})
	_ = runFullAuthorizeStep(t, h, "u-fk", "t-alpha")

	// One row should exist for t-alpha after the authorize step.
	var before int
	if err := h.db.QueryRow(`SELECT COUNT(*) FROM oauth_tokens WHERE tenant_id = ?`, "t-alpha").Scan(&before); err != nil {
		t.Fatalf("count before: %v", err)
	}
	if before == 0 {
		t.Fatal("expected at least one oauth_tokens row for t-alpha")
	}

	if _, err := h.db.Exec(`DELETE FROM tenants WHERE id = ?`, "t-alpha"); err != nil {
		t.Fatalf("delete tenant: %v", err)
	}

	var after int
	if err := h.db.QueryRow(`SELECT COUNT(*) FROM oauth_tokens WHERE tenant_id = ?`, "t-alpha").Scan(&after); err != nil {
		t.Fatalf("count after: %v", err)
	}
	if after != 0 {
		t.Errorf("cascade did not fire: %d orphan oauth_tokens rows after tenant delete", after)
	}
}

// TestOAuth_AccessJTI_RevokedOnRotation — finding #8 (P1). The access
// JTI minted by the initial token exchange must be added to revoked_jtis
// after a refresh rotation, so the middleware's IsJTIRevoked check
// returns 401 on any in-flight access token from the old chain.
func TestOAuth_AccessJTI_RevokedOnRotation(t *testing.T) {
	h := newHarness(t, OAuthConfig{})
	code := runFullAuthorizeStep(t, h, "u-jti-rot", "t-alpha")
	_, body := redeemCode(t, h, code)
	oldAccess, _ := body["access_token"].(string)
	refresh, _ := body["refresh_token"].(string)

	oldClaims, err := h.signer.VerifyAccessToken(oldAccess)
	if err != nil {
		t.Fatalf("verify old access: %v", err)
	}
	oldJTI := oldClaims.ID

	// Before rotation, old JTI is NOT in revoked_jtis.
	if jtiRevoked(t, h.db, oldJTI) {
		t.Fatalf("old access JTI %q revoked before rotation", oldJTI)
	}

	// Rotate.
	rform := url.Values{}
	rform.Set("grant_type", "refresh_token")
	rform.Set("refresh_token", refresh)
	if status, body2 := h.token(t, rform); status != http.StatusOK {
		t.Fatalf("rotate: status=%d body=%v", status, body2)
	}

	// After rotation, old access JTI MUST be in revoked_jtis. The
	// middleware will catch the next request that presents the old
	// access token.
	if !jtiRevoked(t, h.db, oldJTI) {
		t.Errorf("old access JTI %q not revoked after rotation — leaked access token remains valid until 1h TTL", oldJTI)
	}
}

// TestOAuth_AccessJTI_RevokedOnRevokeEndpoint — finding #8 (P1).
// /v1/oauth/revoke must invalidate the in-flight access token, not just
// the refresh row.
func TestOAuth_AccessJTI_RevokedOnRevokeEndpoint(t *testing.T) {
	h := newHarness(t, OAuthConfig{})
	code := runFullAuthorizeStep(t, h, "u-jti-rev", "t-alpha")
	_, body := redeemCode(t, h, code)
	access, _ := body["access_token"].(string)
	refresh, _ := body["refresh_token"].(string)

	claims, err := h.signer.VerifyAccessToken(access)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if jtiRevoked(t, h.db, claims.ID) {
		t.Fatalf("access JTI %q revoked before /v1/oauth/revoke called", claims.ID)
	}

	// POST /v1/oauth/revoke with the refresh token.
	form := url.Values{}
	form.Set("token", refresh)
	req := httptest.NewRequest(http.MethodPost, "/v1/oauth/revoke", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("revoke status = %d, want 200", w.Code)
	}

	// Access JTI must now be in revoked_jtis.
	if !jtiRevoked(t, h.db, claims.ID) {
		t.Errorf("access JTI %q not revoked after /v1/oauth/revoke — access token still valid until 1h TTL", claims.ID)
	}
}

// jtiRevoked is a small read of the revoked_jtis table used by the
// new JTI revocation tests. Mirrors the production IsJTIRevoked SQL.
func jtiRevoked(t *testing.T, db *sql.DB, jti string) bool {
	t.Helper()
	var hit int
	err := db.QueryRow(`SELECT 1 FROM revoked_jtis WHERE jti = ?`, jti).Scan(&hit)
	return err == nil && hit == 1
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

// ─── scenario 6 — refresh-token replay sweeps the CHAIN only ─────────────────

// TestOAuth_RefreshReplay_SweepsChainOnly — finding #7 (P1). Before
// the chain-id retrofit, replaying any of the user's old refresh tokens
// nuked every live refresh token they owned — a victim DoS primitive
// for anyone who learned a single historical token. The fix tracks
// parent_id on each rotation so the sweep walks descendants only.
//
// Setup: user runs two independent authorize→token flows, producing
// two chains. Chain 1 gets rotated once to give it depth 2. Replaying
// chain-1's root must revoke both nodes of chain 1, but leave chain 2
// fully alive.
func TestOAuth_RefreshReplay_SweepsChainOnly(t *testing.T) {
	h := newHarness(t, OAuthConfig{})

	// Build two independent chains for the same user.
	code1 := runFullAuthorizeStep(t, h, "u-6", "t-alpha")
	_, body1 := redeemCode(t, h, code1)
	refresh1Root, _ := body1["refresh_token"].(string)

	code2 := runFullAuthorizeStep(t, h, "u-6", "t-alpha")
	_, body2 := redeemCode(t, h, code2)
	refresh2, _ := body2["refresh_token"].(string)

	if refresh1Root == "" || refresh2 == "" || refresh1Root == refresh2 {
		t.Fatalf("expected two distinct refresh tokens; got %q / %q", refresh1Root, refresh2)
	}

	// Rotate chain 1 once so it has depth 2 — refresh1Root is now the
	// consumed root, refresh1Rotated is its live descendant.
	rform := url.Values{}
	rform.Set("grant_type", "refresh_token")
	rform.Set("refresh_token", refresh1Root)
	status, rotateBody := h.token(t, rform)
	if status != http.StatusOK {
		t.Fatal("first rotation must succeed")
	}
	refresh1Rotated, _ := rotateBody["refresh_token"].(string)
	if refresh1Rotated == "" || refresh1Rotated == refresh1Root {
		t.Fatalf("rotation didn't yield a new refresh token")
	}

	// Replay chain 1's root. The chain-only sweep should:
	//   - revoke refresh1Root (already revoked from rotation; idempotent)
	//   - revoke refresh1Rotated (the live descendant)
	//   - leave refresh2 (a different chain) alive.
	replayStatus, replayBody := h.token(t, rform)
	if replayStatus != http.StatusBadRequest {
		t.Errorf("replay status = %d, want 400", replayStatus)
	}
	if got, _ := replayBody["error"].(string); got != "invalid_grant" {
		t.Errorf("error = %q, want invalid_grant", got)
	}

	// refresh1Rotated must now be revoked (descendant of the replayed root).
	var rev1Rot sql.NullInt64
	if err := h.db.QueryRow(`SELECT revoked_at FROM oauth_tokens WHERE id = ?`,
		hashToken(refresh1Rotated)).Scan(&rev1Rot); err != nil {
		t.Fatalf("lookup refresh1Rotated: %v", err)
	}
	if !rev1Rot.Valid {
		t.Error("chain sweep did not revoke refresh1Rotated (live descendant of replayed root)")
	}

	// refresh2 must STILL be alive — it's a different chain, not a
	// descendant of refresh1Root. This is the heart of the fix: a
	// replay only kills the chain it leaked from.
	var rev2 sql.NullInt64
	if err := h.db.QueryRow(`SELECT revoked_at FROM oauth_tokens WHERE id = ?`,
		hashToken(refresh2)).Scan(&rev2); err != nil {
		t.Fatalf("lookup refresh2: %v", err)
	}
	if rev2.Valid {
		t.Error("chain sweep revoked refresh2 — should have been untouched (independent chain)")
	}

	// And refresh2 itself must still work (rotation succeeds).
	rform2 := url.Values{}
	rform2.Set("grant_type", "refresh_token")
	rform2.Set("refresh_token", refresh2)
	s2, body3 := h.token(t, rform2)
	if s2 != http.StatusOK {
		t.Errorf("refresh2 rotation after chain-1 sweep: status=%d, want 200; body=%v", s2, body3)
	}
}

// TestOAuth_RefreshReplay_DeepChainAllRevoked — finding #7 (P1).
// Builds a 3-deep chain (R0 → R1 → R2 active) then replays R0. All
// three rows must be revoked by the descendant-chain walk.
func TestOAuth_RefreshReplay_DeepChainAllRevoked(t *testing.T) {
	h := newHarness(t, OAuthConfig{})

	r0 := runAuthorizeAndRedeem(t, h, "u-deep", "t-alpha")
	r1 := rotate(t, h, r0)
	r2 := rotate(t, h, r1)
	if r0 == r1 || r1 == r2 {
		t.Fatal("rotation did not yield distinct tokens")
	}

	// Replay R0.
	rform := url.Values{}
	rform.Set("grant_type", "refresh_token")
	rform.Set("refresh_token", r0)
	status, _ := h.token(t, rform)
	if status != http.StatusBadRequest {
		t.Errorf("replay R0 status = %d, want 400", status)
	}

	// All three rows must be revoked.
	for label, raw := range map[string]string{"R0": r0, "R1": r1, "R2": r2} {
		var rev sql.NullInt64
		if err := h.db.QueryRow(`SELECT revoked_at FROM oauth_tokens WHERE id = ?`,
			hashToken(raw)).Scan(&rev); err != nil {
			t.Fatalf("lookup %s: %v", label, err)
		}
		if !rev.Valid {
			t.Errorf("%s not revoked after deep-chain replay sweep", label)
		}
	}
}

// runAuthorizeAndRedeem is a small helper for chain tests — performs
// the full authorize→redeem flow and returns the refresh token.
func runAuthorizeAndRedeem(t *testing.T, h *oauthHarness, userID, tenantID string) string {
	t.Helper()
	code := runFullAuthorizeStep(t, h, userID, tenantID)
	_, body := redeemCode(t, h, code)
	r, _ := body["refresh_token"].(string)
	if r == "" {
		t.Fatalf("authorize/redeem produced no refresh token")
	}
	return r
}

// rotate consumes a refresh token, returns the rotated one. Fails the
// test on any non-200 response.
func rotate(t *testing.T, h *oauthHarness, refresh string) string {
	t.Helper()
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refresh)
	status, body := h.token(t, form)
	if status != http.StatusOK {
		t.Fatalf("rotate status = %d, body=%v", status, body)
	}
	r, _ := body["refresh_token"].(string)
	if r == "" {
		t.Fatalf("rotation returned no refresh token: %v", body)
	}
	return r
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

// TestOAuth_RedirectURI_MustExactMatchPerClient — code-review finding #3
// (P0). A known client_id with an unregistered redirect_uri must be
// rejected with invalid_request. Without this gate, the authorize
// handler becomes an open-redirect / code-phishing primitive — an
// attacker who knows the registered client_id can siphon auth codes
// to their own callback. RFC 6749 §3.1.2.2 mandates exact-match.
func TestOAuth_RedirectURI_MustExactMatchPerClient(t *testing.T) {
	h := newHarness(t, OAuthConfig{})
	seedUser(t, h.db, "u-redir", "u-redir@example.com")
	h.curClaims = &Claims{Sub: "u-redir", Email: "u-redir@example.com", Role: RoleDesigner, Tenants: []string{"t-alpha"}}
	defer func() { h.curClaims = nil }()

	params := url.Values{}
	params.Set("response_type", "code")
	params.Set("client_id", "claude.ai") // known, allowlisted client
	// Unregistered URI — looks valid (https), but is NOT in the
	// client's exact-match list.
	params.Set("redirect_uri", "https://attacker.example.com/callback")
	params.Set("code_challenge", pkceChallenge(pkceVerifier))
	params.Set("code_challenge_method", "S256")

	req := httptest.NewRequest(http.MethodGet, "/v1/oauth/authorize?"+params.Encode(), nil)
	w := httptest.NewRecorder()
	h.mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (invalid_request for unregistered redirect_uri)", w.Code)
	}
	body := map[string]any{}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if got, _ := body["error"].(string); got != "invalid_request" {
		t.Errorf("error = %q, want invalid_request", got)
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
	// Multi-tenant user — per #18, explicit tenant_id required.
	// The pin to t-alpha here is exactly what the test asserts ends
	// up in the issued token regardless of session-list tampering.
	params.Set("tenant_id", "t-alpha")
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
