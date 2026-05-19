// OAuth 2.1 + PKCE authorization-code flow for the MCP Connector
// (Plan 2026-05-18-002 U8).
//
// Three endpoints, all mounted via RegisterOAuthRoutes:
//
//   GET  /v1/oauth/authorize   (auth-required: existing /v1/auth/login JWT)
//        Validates client_id + redirect_uri + PKCE challenge, mints a UUID
//        authorization code, INSERTs an oauth_tokens row, 302s back to
//        `redirect_uri?code=<uuid>&state=<state>`. No consent UI — Claude
//        renders its own. Acceptance is implicit (back-channel API).
//
//   POST /v1/oauth/token       (no auth — the code IS the credential)
//        Two grant types:
//          - authorization_code: PKCE-verifies code_verifier → code_challenge,
//            marks the code consumed, mints a 1h access JWT + a 30-day
//            refresh token. Returns {access_token, refresh_token, ...}.
//          - refresh_token: rotates — revokes the old refresh row, INSERTs
//            a new one, mints a fresh access JWT under the SAME tenant_id
//            stored on the row (never caller-supplied state). Replay of a
//            consumed refresh token returns invalid_grant AND best-effort
//            revokes all live refresh rows for that user (OAuth 2.1 BCP).
//
//   POST /v1/oauth/revoke      (no auth — RFC 7009)
//        Sets revoked_at=now() on the row matching hex(sha256(token)).
//        Returns 200 even if the token doesn't exist (RFC 7009 §2.2 —
//        don't leak validity).
//
// Access tokens are minted via the existing SigningKey.MintAccessToken
// (Ed25519 JWT). The MCP transport's requireAuth middleware verifies
// any Ed25519-signed JWT regardless of which path minted it, so no
// transport-side change is needed for the happy path.
//
// Refresh-token storage: the raw 32 random bytes are base64url-encoded
// and returned to the client. Only hex(sha256(token)) sits in the DB.
// A leaked oauth_tokens row cannot be replayed as a refresh token.

package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/google/uuid"
)

// OAuthClient is one registered third-party that may complete the
// OAuth flow against this server. Each client has a stable id and an
// exact-match list of redirect_uris. Per RFC 6749 §3.1.2.2 the server
// MUST require exact-match (no path/query partial) and MUST reject any
// redirect_uri not pre-registered for the client_id — without this,
// the OAuth handshake is a code-phishing primitive.
type OAuthClient struct {
	ID                  string
	AllowedRedirectURIs []string
}

// HasRedirectURI returns true when raw exactly matches one of the
// client's pre-registered redirect_uris. Exact match per RFC 6749
// §3.1.2.2 — no scheme upgrades, no trailing-slash equivalence, no
// query-parameter tolerance.
func (c OAuthClient) HasRedirectURI(raw string) bool {
	for _, allowed := range c.AllowedRedirectURIs {
		if allowed == raw {
			return true
		}
	}
	return false
}

// OAuthConfig is the per-process OAuth configuration.
type OAuthConfig struct {
	// Clients enumerates the registered third-parties that may complete
	// the authorize step. Each client carries an exact-match list of
	// redirect_uris. Env-var driven (OAUTH_CLIENTS as JSON); seeded at
	// server boot. Unknown client_id → invalid_client. Unknown
	// redirect_uri for a known client_id → invalid_request.
	Clients []OAuthClient

	// AccessTTL is the lifetime of OAuth-minted access JWTs. Spec says
	// 1h; we honor the spec by default. Calling code can override for
	// tests.
	AccessTTL time.Duration

	// CodeTTL is the lifetime of an authorization_code row. 60s per
	// OAuth 2.1 §4.1.2 ("the authorization code MUST expire shortly
	// after it is issued to mitigate the risk of leaks").
	CodeTTL time.Duration

	// RefreshTTL is the lifetime of a refresh_token row. 30 days is
	// the OAuth 2.1 BCP soft cap for delegated agents.
	RefreshTTL time.Duration

	// Now is the clock — overridable for tests. nil means time.Now.
	Now func() time.Time
}

func (c OAuthConfig) accessTTL() time.Duration {
	if c.AccessTTL > 0 {
		return c.AccessTTL
	}
	return time.Hour
}

func (c OAuthConfig) codeTTL() time.Duration {
	if c.CodeTTL > 0 {
		return c.CodeTTL
	}
	return 60 * time.Second
}

func (c OAuthConfig) refreshTTL() time.Duration {
	if c.RefreshTTL > 0 {
		return c.RefreshTTL
	}
	return 30 * 24 * time.Hour
}

func (c OAuthConfig) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// lookupClient checks ONLY the static config slice. Kept for callers
// that need to distinguish pre-configured clients from DCR'd ones (none
// today, but the symmetry helps tests).
func (c OAuthConfig) lookupClient(id string) (OAuthClient, bool) {
	for _, cl := range c.Clients {
		if cl.ID == id {
			return cl, true
		}
	}
	return OAuthClient{}, false
}

// lookupAnyClient tries the static config first, then falls back to the
// oauth_clients table (RFC 7591 dynamically-registered clients). The
// Claude.ai connector flow registers itself dynamically per-install, so
// every real-world OAuth client_id we see at authorize/token time comes
// from the DB, not the static slice. Static remains as a configuration
// affordance for hand-curated test clients.
func lookupAnyClient(ctx context.Context, db *sql.DB, cfg OAuthConfig, id string) (OAuthClient, bool) {
	if c, ok := cfg.lookupClient(id); ok {
		return c, true
	}
	return LookupDynamicClient(ctx, db, id)
}

// clientAllowed is retained as a thin wrapper for the callers that
// only care about id-level acceptance (revoke; rate-limited paths).
// Checks static only — dynamic clients hit lookupAnyClient where the
// caller has a context + db handy.
func (c OAuthConfig) clientAllowed(id string) bool {
	_, ok := c.lookupClient(id)
	return ok
}

// RegisterOAuthRoutes mounts the three OAuth endpoints on mux. The
// authorize handler is wrapped in requireAuth so it can read the
// caller's existing session JWT (set by the regular auth middleware);
// token + revoke are unauthenticated per RFC 6749 / RFC 7009.
//
// The signer is the same Ed25519 SigningKey used for /v1/auth/login —
// the resulting access tokens verify under the existing
// SigningKey.VerifyAccessToken used by the MCP transport's
// requireAuth, so no transport change is required.
//
// claimsFromRequest extracts the *Claims that requireAuth stashed in
// the request context. The auth package doesn't own the context key
// (it's defined in cmd/server/main.go), so the caller passes in a
// reader function.
func RegisterOAuthRoutes(
	mux *http.ServeMux,
	db *sql.DB,
	signer *SigningKey,
	cfg OAuthConfig,
	requireAuth func(http.HandlerFunc) http.HandlerFunc,
	claimsFromRequest func(*http.Request) *Claims,
) {
	if db == nil {
		panic("auth.RegisterOAuthRoutes: db is nil")
	}
	if signer == nil {
		panic("auth.RegisterOAuthRoutes: signer is nil")
	}
	if requireAuth == nil {
		panic("auth.RegisterOAuthRoutes: requireAuth is nil")
	}
	if claimsFromRequest == nil {
		panic("auth.RegisterOAuthRoutes: claimsFromRequest is nil")
	}

	mux.HandleFunc("GET /v1/oauth/authorize",
		requireAuth(handleOAuthAuthorize(db, cfg, claimsFromRequest)))
	mux.HandleFunc("POST /v1/oauth/token",
		handleOAuthToken(db, signer, cfg))
	mux.HandleFunc("POST /v1/oauth/revoke",
		handleOAuthRevoke(db, cfg))
	// RFC 7591 Dynamic Client Registration — open (no auth). Required
	// by Anthropic's Claude.ai Custom Connector flow: Claude mints its
	// own client_id per install via this endpoint, then runs the
	// standard authorize+token dance with that client_id.
	mux.HandleFunc("POST /v1/oauth/register",
		handleOAuthRegister(db))
}

// ─── error helpers (RFC 6749 §5.2) ────────────────────────────────────────────

type oauthErrorCode string

const (
	errInvalidRequest oauthErrorCode = "invalid_request"
	errInvalidClient  oauthErrorCode = "invalid_client"
	errInvalidGrant   oauthErrorCode = "invalid_grant"
	errServerError    oauthErrorCode = "server_error"
)

func writeOAuthError(w http.ResponseWriter, status int, code oauthErrorCode, desc string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":             string(code),
		"error_description": desc,
	})
}

// ─── /v1/oauth/authorize ──────────────────────────────────────────────────────

func handleOAuthAuthorize(db *sql.DB, cfg OAuthConfig, claimsFromRequest func(*http.Request) *Claims) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()

		responseType := q.Get("response_type")
		clientID := q.Get("client_id")
		redirectURI := q.Get("redirect_uri")
		state := q.Get("state")
		codeChallenge := q.Get("code_challenge")
		codeChallengeMethod := q.Get("code_challenge_method")
		scope := q.Get("scope")

		if responseType != "code" {
			writeOAuthError(w, http.StatusBadRequest, errInvalidRequest, "response_type must be 'code'")
			return
		}
		if clientID == "" {
			writeOAuthError(w, http.StatusBadRequest, errInvalidRequest, "client_id required")
			return
		}
		client, ok := lookupAnyClient(r.Context(), db, cfg, clientID)
		if !ok {
			writeOAuthError(w, http.StatusUnauthorized, errInvalidClient, "client_id not allowlisted")
			return
		}
		if redirectURI == "" {
			writeOAuthError(w, http.StatusBadRequest, errInvalidRequest, "redirect_uri required")
			return
		}
		// RFC 6749 §3.1.2.2 — exact-match against the client's
		// pre-registered redirect_uris. The OAuth 2.1 BCP makes this
		// non-optional. Without it, an attacker who knows the client_id
		// can phish auth codes by passing their own callback URL.
		if !client.HasRedirectURI(redirectURI) {
			writeOAuthError(w, http.StatusBadRequest, errInvalidRequest, "redirect_uri not registered for client_id")
			return
		}
		if codeChallenge == "" {
			writeOAuthError(w, http.StatusBadRequest, errInvalidRequest, "code_challenge required (PKCE)")
			return
		}
		// OAuth 2.1 §4.1.1 — only S256 is permitted; plain is forbidden.
		if codeChallengeMethod != "S256" {
			writeOAuthError(w, http.StatusBadRequest, errInvalidRequest, "code_challenge_method must be S256")
			return
		}

		claims := claimsFromRequest(r)
		if claims == nil || claims.Sub == "" {
			// Belt-and-braces — requireAuth should have rejected this
			// already. If it didn't, fail safe.
			writeOAuthError(w, http.StatusUnauthorized, errInvalidRequest, "no session")
			return
		}
		// Pin exactly one tenant_id at authorize time. Single-tenant
		// users get auto-pinned; multi-tenant users MUST pass an
		// explicit `?tenant_id=` query param naming a tenant they belong
		// to. Silently defaulting multi-tenant users to Tenants[0]
		// (the pre-#18 behavior) was wrong — there's no UI signal of
		// which tenant is being delegated to the connector, and a
		// non-default tenant would be unreachable. NEVER trust caller-
		// supplied tenant beyond the membership check — see Rule 4 in
		// CLAUDE.md.
		if len(claims.Tenants) == 0 {
			writeOAuthError(w, http.StatusForbidden, errInvalidRequest, "session has no tenant membership")
			return
		}
		requestedTenant := q.Get("tenant_id")
		var tenantID string
		switch {
		case len(claims.Tenants) == 1:
			// Single-tenant user — auto-pin. If they passed tenant_id
			// anyway, enforce it matches.
			tenantID = claims.Tenants[0]
			if requestedTenant != "" && requestedTenant != tenantID {
				writeOAuthError(w, http.StatusForbidden, errInvalidRequest,
					"tenant_id does not match session membership")
				return
			}
		case requestedTenant == "":
			writeOAuthError(w, http.StatusBadRequest, errInvalidRequest,
				"tenant_id query param required for multi-tenant sessions")
			return
		default:
			// Multi-tenant + explicit pick. Verify membership.
			found := false
			for _, t := range claims.Tenants {
				if t == requestedTenant {
					found = true
					break
				}
			}
			if !found {
				writeOAuthError(w, http.StatusForbidden, errInvalidRequest,
					"tenant_id not in session membership")
				return
			}
			tenantID = requestedTenant
		}

		now := cfg.now()
		code := uuid.NewString()

		_, err := db.ExecContext(r.Context(), `
			INSERT INTO oauth_tokens (
				id, user_id, tenant_id, kind, client_id, redirect_uri, scope,
				code_challenge, code_challenge_method, expires_at, created_at
			) VALUES (?, ?, ?, 'authorization_code', ?, ?, ?, ?, 'S256', ?, ?)`,
			code, claims.Sub, tenantID, clientID, redirectURI, scope,
			codeChallenge, now.Add(cfg.codeTTL()).Unix(), now.Unix())
		if err != nil {
			writeOAuthError(w, http.StatusInternalServerError, errServerError, "store code: "+err.Error())
			return
		}

		// 302 redirect to the client. Build the URL with the existing
		// query string preserved so the client can pass non-OAuth
		// params through if needed (rare in practice).
		u, err := url.Parse(redirectURI)
		if err != nil {
			writeOAuthError(w, http.StatusBadRequest, errInvalidRequest, "redirect_uri parse: "+err.Error())
			return
		}
		rq := u.Query()
		rq.Set("code", code)
		if state != "" {
			rq.Set("state", state)
		}
		u.RawQuery = rq.Encode()
		http.Redirect(w, r, u.String(), http.StatusFound)
	}
}

// ─── /v1/oauth/token ──────────────────────────────────────────────────────────

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope,omitempty"`
}

func handleOAuthToken(db *sql.DB, signer *SigningKey, cfg OAuthConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// RFC 6749 §4.1.3 — token request is application/x-www-form-urlencoded.
		if err := r.ParseForm(); err != nil {
			writeOAuthError(w, http.StatusBadRequest, errInvalidRequest, "parse form: "+err.Error())
			return
		}
		grantType := r.PostFormValue("grant_type")
		switch grantType {
		case "authorization_code":
			handleTokenAuthCode(w, r, db, signer, cfg)
		case "refresh_token":
			handleTokenRefresh(w, r, db, signer, cfg)
		default:
			writeOAuthError(w, http.StatusBadRequest, errInvalidRequest, "unsupported grant_type")
		}
	}
}

func handleTokenAuthCode(w http.ResponseWriter, r *http.Request, db *sql.DB, signer *SigningKey, cfg OAuthConfig) {
	code := r.PostFormValue("code")
	codeVerifier := r.PostFormValue("code_verifier")
	clientID := r.PostFormValue("client_id")
	redirectURI := r.PostFormValue("redirect_uri")

	if code == "" || codeVerifier == "" || clientID == "" || redirectURI == "" {
		writeOAuthError(w, http.StatusBadRequest, errInvalidRequest, "code, code_verifier, client_id, redirect_uri all required")
		return
	}
	if _, ok := lookupAnyClient(r.Context(), db, cfg, clientID); !ok {
		writeOAuthError(w, http.StatusUnauthorized, errInvalidClient, "client_id not allowlisted")
		return
	}

	row := db.QueryRowContext(r.Context(), `
		SELECT user_id, tenant_id, client_id, redirect_uri, scope,
		       code_challenge, expires_at, consumed_at, revoked_at
		FROM oauth_tokens
		WHERE id = ? AND kind = 'authorization_code'`,
		code)

	var (
		userID, tenantID, storedClient, storedRedirect, scope, challenge string
		expiresAt                                                        int64
		consumedAt, revokedAt                                            sql.NullInt64
	)
	if err := row.Scan(&userID, &tenantID, &storedClient, &storedRedirect, &scope, &challenge, &expiresAt, &consumedAt, &revokedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeOAuthError(w, http.StatusBadRequest, errInvalidGrant, "code not found")
			return
		}
		writeOAuthError(w, http.StatusInternalServerError, errServerError, "load code: "+err.Error())
		return
	}

	now := cfg.now()
	if consumedAt.Valid {
		// Code replay. We don't sweep here — codes are single-use and
		// expire in 60s; the marker is enough.
		writeOAuthError(w, http.StatusBadRequest, errInvalidGrant, "code already consumed")
		return
	}
	if revokedAt.Valid {
		writeOAuthError(w, http.StatusBadRequest, errInvalidGrant, "code revoked")
		return
	}
	if now.Unix() > expiresAt {
		writeOAuthError(w, http.StatusBadRequest, errInvalidGrant, "code expired")
		return
	}
	if storedClient != clientID {
		writeOAuthError(w, http.StatusBadRequest, errInvalidGrant, "client_id mismatch")
		return
	}
	if storedRedirect != redirectURI {
		writeOAuthError(w, http.StatusBadRequest, errInvalidGrant, "redirect_uri mismatch")
		return
	}

	// PKCE verification — RFC 7636 §4.6.
	// challenge = base64url(sha256(code_verifier))
	sum := sha256.Sum256([]byte(codeVerifier))
	expected := base64.RawURLEncoding.EncodeToString(sum[:])
	if subtle.ConstantTimeCompare([]byte(expected), []byte(challenge)) != 1 {
		writeOAuthError(w, http.StatusBadRequest, errInvalidGrant, "code_verifier mismatch")
		return
	}

	// Mark the code consumed BEFORE minting tokens — and check RowsAffected
	// to defend against concurrent double-redemption. The
	// `WHERE consumed_at IS NULL` clause is the atomic precondition:
	// SQLite's serial-writer pool ensures exactly one redemption updates
	// the row from NULL → now; the parallel attempt sees 0 rows affected
	// and we reject before minting any tokens. Belt-and-braces vs the
	// scan-then-update check above which can race across the round-trip.
	res, err := db.ExecContext(r.Context(),
		`UPDATE oauth_tokens SET consumed_at = ? WHERE id = ? AND kind = 'authorization_code' AND consumed_at IS NULL`,
		now.Unix(), code)
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, errServerError, "consume code: "+err.Error())
		return
	}
	n, err := res.RowsAffected()
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, errServerError, "consume code rows: "+err.Error())
		return
	}
	if n == 0 {
		// A parallel request raced us and consumed the code first. Treat
		// as a replay attempt — same outcome as the scan-time check.
		writeOAuthError(w, http.StatusBadRequest, errInvalidGrant, "code already consumed")
		return
	}

	// Initial mint at code-redemption time — no parent chain yet.
	access, refresh, err := mintAccessAndRefresh(r.Context(), db, signer, cfg, userID, tenantID, clientID, redirectURI, scope, "", now)
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, errServerError, err.Error())
		return
	}

	writeTokenResponse(w, access, refresh, scope, cfg.accessTTL())
}

func handleTokenRefresh(w http.ResponseWriter, r *http.Request, db *sql.DB, signer *SigningKey, cfg OAuthConfig) {
	refresh := r.PostFormValue("refresh_token")
	if refresh == "" {
		writeOAuthError(w, http.StatusBadRequest, errInvalidRequest, "refresh_token required")
		return
	}
	id := hashToken(refresh)

	row := db.QueryRowContext(r.Context(), `
		SELECT user_id, tenant_id, client_id, redirect_uri, scope,
		       expires_at, consumed_at, revoked_at, last_access_jti
		FROM oauth_tokens
		WHERE id = ? AND kind = 'refresh_token'`,
		id)

	var (
		userID, tenantID, clientID, redirectURI, scope string
		expiresAt                                      int64
		consumedAt, revokedAt                          sql.NullInt64
		lastAccessJTI                                  sql.NullString
	)
	if err := row.Scan(&userID, &tenantID, &clientID, &redirectURI, &scope, &expiresAt, &consumedAt, &revokedAt, &lastAccessJTI); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeOAuthError(w, http.StatusBadRequest, errInvalidGrant, "refresh_token not found")
			return
		}
		writeOAuthError(w, http.StatusInternalServerError, errServerError, "load refresh: "+err.Error())
		return
	}

	now := cfg.now()
	if revokedAt.Valid || consumedAt.Valid {
		sweepRefreshAndAccessJTIs(r.Context(), db, userID, id, now, "refresh_replay")
		writeOAuthError(w, http.StatusBadRequest, errInvalidGrant, "refresh_token reused — all sessions revoked")
		return
	}
	if now.Unix() > expiresAt {
		writeOAuthError(w, http.StatusBadRequest, errInvalidGrant, "refresh_token expired")
		return
	}

	// Rotate. Mark old row revoked first; this serves as the
	// consumed-marker — a parallel replay attempt sees revoked_at and
	// triggers the sweep above. Then INSERT the new refresh row.
	//
	// The `WHERE revoked_at IS NULL` + RowsAffected check is the atomic
	// guard against concurrent rotation: only one parallel caller's
	// UPDATE flips the row to revoked; the loser sees 0 rows and falls
	// into the same replay-defense path as if it had retried with a
	// stale token.
	res, err := db.ExecContext(r.Context(),
		`UPDATE oauth_tokens SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`,
		now.Unix(), id)
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, errServerError, "rotate old: "+err.Error())
		return
	}
	n, err := res.RowsAffected()
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, errServerError, "rotate old rows: "+err.Error())
		return
	}
	if n == 0 {
		// Concurrent rotation already revoked this row. Fall into the
		// replay-defense sweep — matches the behavior of a sequential
		// replay-after-rotate.
		sweepRefreshAndAccessJTIs(r.Context(), db, userID, id, now, "refresh_race_loss")
		writeOAuthError(w, http.StatusBadRequest, errInvalidGrant, "refresh_token reused — all sessions revoked")
		return
	}

	// Rotation succeeded — revoke the OLD access JTI so it can't keep
	// working until its 1h TTL. The middleware's IsJTIRevoked check
	// picks this up on the very next authenticated request (worst case:
	// 60s in-memory cache TTL on revoked_jtis).
	if lastAccessJTI.Valid && lastAccessJTI.String != "" {
		if err := revokeAccessJTI(r.Context(), db, lastAccessJTI.String, "system", "rotation"); err != nil {
			slog.Warn("rotate_revoke_old_access_jti_failed", "user_id", userID, "jti", lastAccessJTI.String, "err", err.Error())
		}
	}

	// Rotation — link the new row to the old via parent_id so a future
	// replay of any row in this chain triggers a chain-only sweep.
	access, newRefresh, err := mintAccessAndRefresh(r.Context(), db, signer, cfg, userID, tenantID, clientID, redirectURI, scope, id, now)
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, errServerError, err.Error())
		return
	}
	writeTokenResponse(w, access, newRefresh, scope, cfg.accessTTL())
}

// sweepRefreshAndAccessJTIs is the replay-defense + race-loss sweep
// helper. Walks the descendant chain of `rootID` via recursive CTE and
// revokes every row in that chain (refresh rows + their access JTIs).
// Other chains for the same user — different devices, separate
// authorize sessions — are left alive (plan-002 finding #7).
//
// rootID is the id of the refresh row that triggered the sweep (the
// row whose replay or race-loss was detected). The chain set includes
// rootID itself plus every row with parent_id transitively descending
// from it.
//
// Logs sweep outcome for post-hoc audit — replay attempts are rare and
// worth tracing.
func sweepRefreshAndAccessJTIs(ctx context.Context, db *sql.DB, userID, rootID string, now time.Time, reason string) {
	// Build the chain via recursive CTE. SQLite has supported WITH
	// RECURSIVE since 3.8; the runner pins a modern build.
	const chainCTE = `
		WITH RECURSIVE chain(id) AS (
			SELECT id FROM oauth_tokens WHERE id = ?
			UNION ALL
			SELECT t.id FROM oauth_tokens t
			  JOIN chain c ON t.parent_id = c.id
		)
	`

	// Collect access JTIs of the chain BEFORE flipping revoked_at, so
	// we can hand them to revokeAccessJTI after the UPDATE. The IN
	// (SELECT id FROM chain) predicate is the chain's transitive closure.
	jtiRows, err := db.QueryContext(ctx,
		chainCTE+`
		SELECT last_access_jti FROM oauth_tokens
		 WHERE id IN (SELECT id FROM chain)
		   AND kind = 'refresh_token'
		   AND revoked_at IS NULL
		   AND last_access_jti IS NOT NULL AND last_access_jti != ''`,
		rootID)
	jtis := make([]string, 0, 4)
	if err == nil {
		for jtiRows.Next() {
			var j sql.NullString
			if scanErr := jtiRows.Scan(&j); scanErr == nil && j.Valid {
				jtis = append(jtis, j.String)
			}
		}
		_ = jtiRows.Close()
	}

	sweepRes, sweepErr := db.ExecContext(ctx,
		chainCTE+`
		UPDATE oauth_tokens SET revoked_at = ?
		 WHERE id IN (SELECT id FROM chain)
		   AND kind = 'refresh_token'
		   AND revoked_at IS NULL`,
		rootID, now.Unix())
	if sweepErr != nil {
		slog.Warn("refresh_replay_sweep_failed", "user_id", userID, "root_id", rootID, "reason", reason, "err", sweepErr.Error())
		return
	}
	rows, _ := sweepRes.RowsAffected()

	// Revoke each access JTI — the middleware check turns them into
	// 401s on the next request.
	for _, j := range jtis {
		if jerr := revokeAccessJTI(ctx, db, j, "system", reason); jerr != nil {
			slog.Warn("sweep_revoke_access_jti_failed", "user_id", userID, "root_id", rootID, "jti", j, "err", jerr.Error())
		}
	}
	slog.Warn("refresh_replay_sweep_triggered",
		"user_id", userID,
		"root_id", rootID,
		"reason", reason,
		"refresh_rows_revoked", rows,
		"access_jtis_revoked", len(jtis))
}

// mintAccessAndRefresh mints a fresh access JWT under (userID, tenantID)
// and INSERTs a new refresh-token row. The refresh token returned to
// the client is the raw base64url-encoded value; only its sha256 hash
// is persisted.
//
// CRITICAL — tenantID comes from the stored row, never from caller
// state. This is the cross-tenant safety boundary referenced in
// Rule 4 of CLAUDE.md.
// mintAccessAndRefresh mints a fresh (access, refresh) pair and stores
// the refresh row. parentID is the id of the refresh row this mint
// descends from (empty string for the initial mint at the
// authorization_code redemption). The parent linkage powers the
// per-chain replay sweep (plan-002 finding #7).
func mintAccessAndRefresh(ctx context.Context, db *sql.DB, signer *SigningKey, cfg OAuthConfig, userID, tenantID, clientID, redirectURI, scope, parentID string, now time.Time) (access, refresh string, err error) {
	// The Claims.Tenants slice is what every downstream tenant-check
	// reads. Build it from the stored tenant_id and ONLY that — no
	// merging with any other source.
	tenants := []string{tenantID}

	// Preserve the user's actual role + email in the OAuth-minted JWT.
	// Earlier shape hardcoded Role="designer" as "least-privilege agent"
	// — wrong reasoning: OAuth delegates the USER's identity to the
	// client, so a super-admin user driving Claude.ai should retain
	// super-admin reach. Least-privilege belongs at scope-granularity
	// (which we don't enforce yet), not at role-granularity. Looking up
	// the role from `users` once per mint is cheap.
	var userEmail, userRole string
	err = db.QueryRowContext(ctx,
		`SELECT email, role FROM users WHERE id = ?`, userID).
		Scan(&userEmail, &userRole)
	if err != nil {
		return "", "", fmt.Errorf("lookup user %q: %w", userID, err)
	}
	if userRole == "" {
		// Defensive: every user row should have a role; if NULL somehow,
		// fall back to the most conservative default.
		userRole = RoleDesigner
	}
	access, accessJTI, err := signer.MintOAuthAccessToken(userID, userEmail, userRole, tenants, cfg.accessTTL())
	if err != nil {
		return "", "", fmt.Errorf("mint access: %w", err)
	}

	refresh, err = generateRefreshToken()
	if err != nil {
		return "", "", fmt.Errorf("generate refresh: %w", err)
	}
	id := hashToken(refresh)

	// last_access_jti captured at mint time so the next rotation / revoke
	// can look it up and INSERT into revoked_jtis. Without this column
	// the access token stayed valid for its full 1h TTL after a revoke
	// (plan-002 finding #8).
	// parent_id links this row back to the refresh row it descends from
	// (plan-002 finding #7). NULL on the initial mint (empty parentID
	// arg); the rotation path passes the OLD row's id.
	var parentArg any
	if parentID != "" {
		parentArg = parentID
	}
	_, err = db.ExecContext(ctx, `
		INSERT INTO oauth_tokens (
			id, user_id, tenant_id, kind, client_id, redirect_uri, scope,
			expires_at, created_at, last_access_jti, parent_id
		) VALUES (?, ?, ?, 'refresh_token', ?, ?, ?, ?, ?, ?, ?)`,
		id, userID, tenantID, clientID, redirectURI, scope,
		now.Add(cfg.refreshTTL()).Unix(), now.Unix(), accessJTI, parentArg)
	if err != nil {
		return "", "", fmt.Errorf("store refresh: %w", err)
	}
	return access, refresh, nil
}

// revokeAccessJTI marks a single JWT id revoked in the shared
// revoked_jtis table. The middleware's IsJTIRevoked check (60s in-memory
// cache) picks it up on the next authenticated request. Safe to call
// with an empty jti (no-op). Idempotent on the PK.
func revokeAccessJTI(ctx context.Context, db *sql.DB, jti, revokedBy, reason string) error {
	if jti == "" {
		return nil
	}
	_, err := db.ExecContext(ctx,
		`INSERT INTO revoked_jtis (jti, revoked_at, revoked_by, reason)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(jti) DO UPDATE SET
		     revoked_at = excluded.revoked_at,
		     revoked_by = excluded.revoked_by,
		     reason = excluded.reason`,
		jti, time.Now().UTC().Format(time.RFC3339), revokedBy, reason)
	return err
}

func writeTokenResponse(w http.ResponseWriter, access, refresh, scope string, ttl time.Duration) {
	resp := tokenResponse{
		AccessToken:  access,
		TokenType:    "Bearer",
		ExpiresIn:    int(ttl.Seconds()),
		RefreshToken: refresh,
		Scope:        scope,
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

// ─── /v1/oauth/revoke ─────────────────────────────────────────────────────────

// revokeRateLimiter rate-limits /v1/oauth/revoke per source IP. RFC 7009
// §2.1 makes revoke unauthenticated by design — anyone who knows a
// refresh token can revoke it — but §2.2 acknowledges this opens a DoS
// vector if an attacker who learns one token can spam the endpoint to
// thrash the DB / log fan-out. /ce-code-review finding #26.
//
// 60 ops/min/IP is generous for legitimate clients (a connector that
// rotates tokens hourly hits this maybe twice an hour) and tight
// against scripted abuse. Tunable via OAuthConfig.RevokeRateLimit.
var revokeRateLimiter = newPerIPRateLimiter(60, time.Minute)

func handleOAuthRevoke(db *sql.DB, cfg OAuthConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !revokeRateLimiter.Allow(clientIP(r)) {
			w.Header().Set("Retry-After", "60")
			writeOAuthError(w, http.StatusTooManyRequests, errInvalidRequest,
				"rate limit exceeded — retry after 60s")
			return
		}
		if err := r.ParseForm(); err != nil {
			writeOAuthError(w, http.StatusBadRequest, errInvalidRequest, "parse form: "+err.Error())
			return
		}
		tok := r.PostFormValue("token")
		if tok == "" {
			writeOAuthError(w, http.StatusBadRequest, errInvalidRequest, "token required")
			return
		}
		// RFC 7009 §2.2 — even if the token doesn't exist (or already
		// revoked, or wrong type), return 200. We don't leak which.
		id := hashToken(tok)

		// Capture last_access_jti BEFORE the UPDATE so we can revoke the
		// in-flight access token too. Otherwise the access token kept
		// working until its 1h TTL — see plan-002 finding #8.
		var lastJTI sql.NullString
		_ = db.QueryRowContext(r.Context(),
			`SELECT last_access_jti FROM oauth_tokens
			 WHERE id = ? AND kind = 'refresh_token' AND revoked_at IS NULL`,
			id).Scan(&lastJTI)

		_, _ = db.ExecContext(r.Context(),
			`UPDATE oauth_tokens SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`,
			cfg.now().Unix(), id)

		if lastJTI.Valid && lastJTI.String != "" {
			if err := revokeAccessJTI(r.Context(), db, lastJTI.String, "client_revoke", "explicit_revoke"); err != nil {
				slog.Warn("revoke_endpoint_jti_failed", "jti", lastJTI.String, "err", err.Error())
			}
		}
		w.WriteHeader(http.StatusOK)
	}
}

// ─── token primitives ─────────────────────────────────────────────────────────

// generateRefreshToken returns 32 cryptographically random bytes
// base64url-encoded. The raw string is the "refresh token" handed to
// the client; only its sha256 is stored.
func generateRefreshToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// hashToken returns hex(sha256(token)). Used as the primary key for
// refresh_token rows so a leaked DB can't be replayed against the
// authorization server.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// ParseClients parses the OAUTH_CLIENTS env var into a slice of
// OAuthClient. Two formats are accepted:
//
//  1. JSON (preferred, supports per-client allowlists):
//     OAUTH_CLIENTS='[{"id":"claude.ai","redirect_uris":["https://claude.ai/api/mcp/auth_callback"]}]'
//
//  2. Empty string → DefaultClients() (claude.ai with its standard callback).
//
// Returns an error on malformed JSON so cmd/server can fail-fast at boot
// rather than silently dropping the allowlist.
func ParseClients(env string) ([]OAuthClient, error) {
	if env == "" {
		return DefaultClients(), nil
	}
	var raw []struct {
		ID           string   `json:"id"`
		RedirectURIs []string `json:"redirect_uris"`
	}
	if err := json.Unmarshal([]byte(env), &raw); err != nil {
		return nil, fmt.Errorf("parse OAUTH_CLIENTS JSON: %w", err)
	}
	out := make([]OAuthClient, 0, len(raw))
	for i, r := range raw {
		if r.ID == "" {
			return nil, fmt.Errorf("OAUTH_CLIENTS[%d]: id required", i)
		}
		if len(r.RedirectURIs) == 0 {
			return nil, fmt.Errorf("OAUTH_CLIENTS[%d] (%q): at least one redirect_uri required", i, r.ID)
		}
		out = append(out, OAuthClient{
			ID:                  r.ID,
			AllowedRedirectURIs: r.RedirectURIs,
		})
	}
	return out, nil
}

// ─── reaper — periodic cleanup of expired / long-revoked rows ────────────────

// reaperRevokedRetention is how long a revoked row stays around before
// being purged. 30 days gives security forensics a window to inspect
// recently-revoked tokens (replay investigations, leak audits).
const reaperRevokedRetention = 30 * 24 * time.Hour

// reaperInterval is how often the reaper wakes up. 1h keeps the table
// bounded without thrashing the disk; the per-row work is a single
// indexed DELETE.
const reaperInterval = time.Hour

// StartOAuthTokenReaper spawns a background goroutine that periodically
// purges expired authorization_codes (60s TTL — fast-rotating, big
// volume contributor) and long-revoked rows (refresh + auth-code).
// /ce-code-review finding #20. Returns immediately; the goroutine
// shuts down when ctx is cancelled.
//
// The reaper is deliberately tolerant of failure: errors are logged
// but don't terminate the loop. A failed cleanup leaves rows for the
// next tick to retry. There's no leadership / lock — running on multi-
// replica deploys means each replica does its own DELETE pass, but
// the WHERE clause is idempotent and the DB serializes the writes.
func StartOAuthTokenReaper(ctx context.Context, db *sql.DB, log *slog.Logger) {
	if log == nil {
		log = slog.Default()
	}
	go func() {
		ticker := time.NewTicker(reaperInterval)
		defer ticker.Stop()
		// Run once immediately on startup — clears anything that piled
		// up while the process was down.
		runOAuthReaperPass(ctx, db, log)
		for {
			select {
			case <-ctx.Done():
				log.Info("oauth_token_reaper_stopped")
				return
			case <-ticker.C:
				runOAuthReaperPass(ctx, db, log)
			}
		}
	}()
}

// runOAuthReaperPass executes a single DELETE pass. Exported as a
// distinct function so tests can call it deterministically without
// waiting on the ticker.
func runOAuthReaperPass(ctx context.Context, db *sql.DB, log *slog.Logger) {
	now := time.Now()

	// Expired authorization codes that were never consumed (user
	// abandoned mid-flow). idx_oauth_tokens_kind_expires covers this.
	res1, err := db.ExecContext(ctx,
		`DELETE FROM oauth_tokens
		 WHERE kind = 'authorization_code'
		   AND expires_at < ?`,
		now.Unix())
	codeRows := int64(0)
	if err != nil {
		log.Warn("oauth_token_reaper_codes_failed", "err", err.Error())
	} else {
		codeRows, _ = res1.RowsAffected()
	}

	// Long-revoked refresh + code rows. Retention horizon lets us
	// investigate a replay incident post-mortem.
	retentionCutoff := now.Add(-reaperRevokedRetention).Unix()
	res2, err := db.ExecContext(ctx,
		`DELETE FROM oauth_tokens
		 WHERE revoked_at IS NOT NULL
		   AND revoked_at < ?`,
		retentionCutoff)
	revokedRows := int64(0)
	if err != nil {
		log.Warn("oauth_token_reaper_revoked_failed", "err", err.Error())
	} else {
		revokedRows, _ = res2.RowsAffected()
	}

	// Expired refresh tokens that were never rotated or revoked
	// (orphans — user disconnected the connector without explicit
	// revoke). Keep these around for the same retention window so
	// the row's last_access_jti is still queryable during incident
	// review.
	res3, err := db.ExecContext(ctx,
		`DELETE FROM oauth_tokens
		 WHERE kind = 'refresh_token'
		   AND revoked_at IS NULL
		   AND expires_at < ?`,
		retentionCutoff)
	expiredRows := int64(0)
	if err != nil {
		log.Warn("oauth_token_reaper_expired_failed", "err", err.Error())
	} else {
		expiredRows, _ = res3.RowsAffected()
	}

	if codeRows+revokedRows+expiredRows > 0 {
		log.Info("oauth_token_reaper_pass",
			"expired_codes", codeRows,
			"long_revoked", revokedRows,
			"long_expired_refreshes", expiredRows)
	}
}

// DefaultClients returns the production-default client allowlist —
// claude.ai with its documented Custom Connector callback. Used when
// OAUTH_CLIENTS env is unset.
func DefaultClients() []OAuthClient {
	return []OAuthClient{{
		ID:                  "claude.ai",
		AllowedRedirectURIs: []string{"https://claude.ai/api/mcp/auth_callback"},
	}}
}

// ─── per-IP rate limiter (minimal in-memory token bucket) ────────────────────

// perIPRateLimiter caps requests per source IP at `maxOps` per `window`.
// Self-contained — no x/time/rate dependency. Buckets are stored in a
// sync.Map keyed by IP string; entries are GC'd lazily when a request
// for that IP refills its full bucket (no background sweep needed).
// Acceptable for /v1/oauth/revoke's traffic profile (low-frequency,
// high-cardinality IPs); switch to a real LRU if revoke moves to a
// hot path.
type perIPRateLimiter struct {
	maxOps int
	window time.Duration
	mu     sync.Mutex
	state  map[string]*ipBucket
}

type ipBucket struct {
	tokens  int
	refill  time.Time // wall clock when next token regenerates
	updated time.Time // last activity — for sloppy eviction
}

func newPerIPRateLimiter(maxOps int, window time.Duration) *perIPRateLimiter {
	return &perIPRateLimiter{
		maxOps: maxOps,
		window: window,
		state:  make(map[string]*ipBucket),
	}
}

// Allow returns true and consumes one token if available; false when
// the IP has exceeded its budget for the current window.
func (l *perIPRateLimiter) Allow(ip string) bool {
	if ip == "" {
		// No source IP — best-effort allow; the rate limiter is one
		// layer of defense, not the only one.
		return true
	}
	now := time.Now()
	tokenInterval := l.window / time.Duration(l.maxOps)

	l.mu.Lock()
	defer l.mu.Unlock()

	b, ok := l.state[ip]
	if !ok {
		// First-seen IP — full bucket minus this op.
		l.state[ip] = &ipBucket{
			tokens:  l.maxOps - 1,
			refill:  now.Add(tokenInterval),
			updated: now,
		}
		l.gcExpired(now)
		return true
	}
	// Refill: add as many tokens as elapsed full intervals, capped at maxOps.
	if now.After(b.refill) {
		elapsed := now.Sub(b.refill)
		add := int(elapsed/tokenInterval) + 1
		b.tokens += add
		if b.tokens > l.maxOps {
			b.tokens = l.maxOps
		}
		b.refill = now.Add(tokenInterval)
	}
	b.updated = now
	if b.tokens <= 0 {
		return false
	}
	b.tokens--
	return true
}

// gcExpired drops bucket entries idle for >5× the window. Called
// opportunistically on new-IP inserts so the map doesn't grow
// unbounded under burst traffic from many IPs.
func (l *perIPRateLimiter) gcExpired(now time.Time) {
	cutoff := now.Add(-5 * l.window)
	for k, b := range l.state {
		if b.updated.Before(cutoff) {
			delete(l.state, k)
		}
	}
}

// clientIP extracts the request's source IP. Honors X-Forwarded-For
// (first hop) when present — Fly's proxy sets it — and falls back to
// RemoteAddr's host portion. Returns "" if neither yields anything.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// First IP in the list is the original client per RFC 7239.
		if comma := indexByte(xff, ','); comma > 0 {
			xff = xff[:comma]
		}
		xff = trimSpaces(xff)
		if xff != "" {
			return xff
		}
	}
	if r.RemoteAddr == "" {
		return ""
	}
	// RemoteAddr is "host:port"; strip the port.
	if colon := lastIndexByte(r.RemoteAddr, ':'); colon > 0 {
		return r.RemoteAddr[:colon]
	}
	return r.RemoteAddr
}

func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

func lastIndexByte(s string, c byte) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == c {
			return i
		}
	}
	return -1
}

func trimSpaces(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}
