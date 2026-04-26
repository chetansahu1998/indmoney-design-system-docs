// Command server is the ds-service HTTP API.
//
// Routes (v1):
//
//	GET  /__health                      — liveness + DB ping
//	POST /v1/auth/login                 — email + password → JWT
//	POST /v1/sync/:tenant               — trigger sync (auth: tenant member with sync role)
//	GET  /v1/audit/:tenant              — recent audit log entries (auth: tenant_admin)
//	POST /v1/admin/bootstrap            — one-shot create super-admin (header: X-Bootstrap-Token)
//	POST /v1/admin/figma-token          — upload Figma PAT for a tenant (auth: super_admin)
//
// Configuration via env (loaded from .env.local at repo root if present):
//
//	SQLITE_PATH                — path to ds.db (default: ./services/ds-service/data/ds.db)
//	JWT_SIGNING_KEY            — base64 ed25519 private key (auto-generate on first run if empty)
//	JWT_PUBLIC_KEY             — base64 ed25519 public key (auto-generated alongside)
//	ENCRYPTION_KEY             — base64 32-byte AES-256 key for Figma PAT at-rest encryption
//	BOOTSTRAP_TOKEN            — secret required for /v1/admin/bootstrap (set this at first launch)
//	REPO_DIR                   — absolute path to docs site repo (default: parent of services/)
//	SYNC_GIT_PUSH              — "true" to commit + push tokens after sync (default: false in dev)
//	PORT                       — HTTP port (default: 8080)
//	CORS_ALLOW_ORIGIN          — comma-separated CORS origins (default: http://localhost:3001)
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/auth"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/db"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/figma/client"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/figma/extractor"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/sync"
)

type config struct {
	SQLitePath      string
	JWTKey          *auth.SigningKey
	EncryptionKey   *auth.EncryptionKey
	BootstrapToken  string
	RepoDir         string
	SyncGitPush     bool
	Port            string
	CORSAllowOrigin []string
}

func main() {
	loadDotEnv()
	log := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := loadConfig(log)
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(1)
	}

	dbConn, err := db.Open(cfg.SQLitePath)
	if err != nil {
		log.Error("db open", "err", err)
		os.Exit(1)
	}
	defer dbConn.Close()
	log.Info("db ready", "path", cfg.SQLitePath)

	orch := &sync.Orchestrator{
		DB:      dbConn,
		Enc:     cfg.EncryptionKey,
		RepoDir: cfg.RepoDir,
		Log:     log,
		GitPush: cfg.SyncGitPush,
	}

	srv := &server{
		cfg:  cfg,
		db:   dbConn,
		jwt:  cfg.JWTKey,
		enc:  cfg.EncryptionKey,
		orch: orch,
		log:  log,
	}

	mux := http.NewServeMux()
	srv.routes(mux)

	addr := ":" + cfg.Port
	log.Info("ds-service listening",
		"addr", addr,
		"repo_dir", cfg.RepoDir,
		"sync_git_push", cfg.SyncGitPush,
	)
	if err := http.ListenAndServe(addr, srv.cors(srv.requestLog(mux))); err != nil {
		log.Error("server", "err", err)
		os.Exit(1)
	}
}

// ─── Config / bootstrap ─────────────────────────────────────────────────────

func loadConfig(log *slog.Logger) (*config, error) {
	// Default SQLite path resolves relative to REPO_DIR (not cwd), so the
	// service can be launched from anywhere without doubling the path.
	defaultSQLite := filepath.Join(getenv("REPO_DIR", absPath("../..")), "services/ds-service/data/ds.db")
	c := &config{
		SQLitePath:     getenv("SQLITE_PATH", defaultSQLite),
		BootstrapToken: os.Getenv("BOOTSTRAP_TOKEN"),
		RepoDir:        getenv("REPO_DIR", absPath("../..")),
		SyncGitPush:    os.Getenv("SYNC_GIT_PUSH") == "true",
		Port:           getenv("PORT", "8080"),
		CORSAllowOrigin: strings.Split(
			getenv("CORS_ALLOW_ORIGIN", "http://localhost:3001"), ","),
	}

	// Ensure data dir exists
	if err := os.MkdirAll(filepath.Dir(c.SQLitePath), 0o755); err != nil {
		return nil, fmt.Errorf("mkdir data: %w", err)
	}

	// JWT signing key — auto-generate if missing (with prominent warning)
	priv := os.Getenv("JWT_SIGNING_KEY")
	pub := os.Getenv("JWT_PUBLIC_KEY")
	if priv == "" {
		k, err := auth.GenerateSigningKey()
		if err != nil {
			return nil, err
		}
		c.JWTKey = k
		log.Warn("JWT_SIGNING_KEY not set — generated ephemeral key (tokens won't survive restart)",
			"add_to_env_local", "JWT_SIGNING_KEY="+k.EncodePriv(),
			"add_to_env_local_pub", "JWT_PUBLIC_KEY="+k.EncodePub(),
		)
	} else {
		k, err := auth.LoadSigningKey(priv, pub)
		if err != nil {
			return nil, fmt.Errorf("load JWT key: %w", err)
		}
		c.JWTKey = k
	}

	// Encryption key — auto-generate if missing
	encB64 := os.Getenv("ENCRYPTION_KEY")
	if encB64 == "" {
		generated, err := auth.GenerateEncryptionKey()
		if err != nil {
			return nil, err
		}
		k, _ := auth.LoadEncryptionKey(generated)
		c.EncryptionKey = k
		log.Warn("ENCRYPTION_KEY not set — generated ephemeral key (PATs won't decrypt after restart)",
			"add_to_env_local", "ENCRYPTION_KEY="+generated,
		)
	} else {
		k, err := auth.LoadEncryptionKey(encB64)
		if err != nil {
			return nil, fmt.Errorf("load encryption key: %w", err)
		}
		c.EncryptionKey = k
	}

	if c.BootstrapToken == "" {
		log.Warn("BOOTSTRAP_TOKEN not set — /v1/admin/bootstrap is OFF. Set it to enable initial admin creation.")
	}
	return c, nil
}

func absPath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// loadDotEnv reads .env.local from cwd or any ancestor.
func loadDotEnv() {
	for _, path := range []string{".env.local", "../.env.local", "../../.env.local", "../../../.env.local"} {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		defer f.Close()
		buf := make([]byte, 1<<20)
		n, _ := f.Read(buf)
		for _, line := range strings.Split(string(buf[:n]), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if eq := strings.Index(line, "="); eq > 0 {
				k := strings.TrimSpace(line[:eq])
				v := strings.Trim(strings.TrimSpace(line[eq+1:]), "\"'")
				if os.Getenv(k) == "" {
					os.Setenv(k, v)
				}
			}
		}
		return
	}
}

// ─── Server ─────────────────────────────────────────────────────────────────

type server struct {
	cfg  *config
	db   *db.DB
	jwt  *auth.SigningKey
	enc  *auth.EncryptionKey
	orch *sync.Orchestrator
	log  *slog.Logger
}

func (s *server) routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /__health", s.handleHealth)
	mux.HandleFunc("POST /v1/auth/login", s.handleLogin)
	mux.HandleFunc("POST /v1/admin/bootstrap", s.handleBootstrap)
	mux.HandleFunc("POST /v1/admin/figma-token", s.requireSuperAdmin(s.handleFigmaTokenUpload))
	mux.HandleFunc("POST /v1/sync/{tenant}", s.requireAuth(s.handleSync))
	mux.HandleFunc("GET /v1/audit/{tenant}", s.requireAuth(s.handleAudit))
	mux.HandleFunc("GET /v1/me", s.requireAuth(s.handleMe))
}

// ─── Middleware ─────────────────────────────────────────────────────────────

func (s *server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		for _, allowed := range s.cfg.CORSAllowOrigin {
			if origin == strings.TrimSpace(allowed) {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Credentials", "true")
				w.Header().Set("Vary", "Origin")
				break
			}
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Trace-ID, X-Bootstrap-Token")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *server) requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := &statusRecorder{ResponseWriter: w, status: 200}
		next.ServeHTTP(ww, r)
		s.log.Info("http",
			"method", r.Method,
			"path", r.URL.Path,
			"status", ww.status,
			"dur_ms", time.Since(start).Milliseconds(),
			"remote", r.RemoteAddr,
		)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (sr *statusRecorder) WriteHeader(code int) {
	sr.status = code
	sr.ResponseWriter.WriteHeader(code)
}

type ctxKey string

const ctxClaims ctxKey = "claims"

func claimsFrom(r *http.Request) *auth.Claims {
	c, _ := r.Context().Value(ctxClaims).(*auth.Claims)
	return c
}

func (s *server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw := r.Header.Get("Authorization")
		if !strings.HasPrefix(raw, "Bearer ") {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "missing bearer token"})
			return
		}
		token := strings.TrimPrefix(raw, "Bearer ")
		claims, err := s.jwt.VerifyAccessToken(token)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid token", "detail": err.Error()})
			return
		}
		ctx := context.WithValue(r.Context(), ctxClaims, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	}
}

func (s *server) requireSuperAdmin(next http.HandlerFunc) http.HandlerFunc {
	return s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		c := claimsFrom(r)
		if c == nil || !c.IsAdmin {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "super-admin only"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ─── Handlers ───────────────────────────────────────────────────────────────

func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if err := s.db.PingContext(r.Context()); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "db": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":             true,
		"db":             "ok",
		"sync_git_push":  s.cfg.SyncGitPush,
		"version":        "v1",
	})
}

type loginReq struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (s *server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
		return
	}
	u, err := s.db.GetUserByEmail(r.Context(), req.Email)
	if err != nil {
		// Constant-time fail to prevent email enumeration
		_ = auth.VerifyPassword("$2a$12$0000000000000000000000000000000000000000000000000000O", req.Password)
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid credentials"})
		return
	}
	if err := auth.VerifyPassword(u.PasswordHash, req.Password); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid credentials"})
		return
	}
	_ = s.db.UpdateUserLastLogin(r.Context(), u.ID)

	tenants := []string{"indmoney"} // v1: hardcoded; resolve from tenant_users in v1.1
	tok, err := s.jwt.MintAccessToken(u.ID, u.Email, u.Role, tenants, 7*24*time.Hour)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": tok,
		"expires_in":   int((7 * 24 * time.Hour).Seconds()),
		"user": map[string]any{
			"id":    u.ID,
			"email": u.Email,
			"role":  u.Role,
		},
	})
}

type bootstrapReq struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Tenant   string `json:"tenant"` // slug
}

func (s *server) handleBootstrap(w http.ResponseWriter, r *http.Request) {
	if s.cfg.BootstrapToken == "" {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "bootstrap disabled"})
		return
	}
	if r.Header.Get("X-Bootstrap-Token") != s.cfg.BootstrapToken {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "invalid bootstrap token"})
		return
	}
	var req bootstrapReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	userID := uuid.NewString()
	now := time.Now().UTC()
	if err := s.db.CreateUser(r.Context(), db.User{
		ID:           userID,
		Email:        req.Email,
		PasswordHash: hash,
		Role:         auth.RoleSuperAdmin,
		CreatedAt:    now,
	}); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	tenantID := uuid.NewString()
	if err := s.db.CreateTenant(r.Context(), db.Tenant{
		ID:        tenantID,
		Slug:      req.Tenant,
		Name:      strings.ToTitle(req.Tenant),
		Status:    "active",
		PlanType:  "free",
		CreatedAt: now,
		CreatedBy: userID,
	}); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if err := s.db.AddTenantUser(r.Context(), tenantID, userID, auth.RoleTenantAdmin); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user_id":   userID,
		"tenant_id": tenantID,
		"message":   "Bootstrap complete. POST /v1/auth/login to get a token.",
	})
}

type figmaTokenReq struct {
	Tenant string `json:"tenant"`
	PAT    string `json:"pat"`
}

func (s *server) handleFigmaTokenUpload(w http.ResponseWriter, r *http.Request) {
	var req figmaTokenReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
		return
	}
	t, err := s.db.GetTenantBySlug(r.Context(), req.Tenant)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "tenant not found"})
		return
	}
	// Validate PAT against /v1/me
	c := client.New(req.PAT)
	me, err := c.Identity(r.Context())
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "PAT validation failed", "detail": err.Error()})
		return
	}
	encrypted, err := s.enc.Encrypt([]byte(req.PAT))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	now := time.Now().UTC()
	email, _ := me["email"].(string)
	handle, _ := me["handle"].(string)
	if err := s.db.UpsertFigmaToken(r.Context(), db.FigmaTokenRecord{
		TenantID:        t.ID,
		EncryptedToken:  encrypted,
		KeyVersion:      1,
		FigmaUserEmail:  email,
		FigmaUserHandle: handle,
		LastValidatedAt: &now,
		CreatedAt:       now,
	}); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":            true,
		"tenant":        req.Tenant,
		"figma_user":    map[string]any{"email": email, "handle": handle},
		"validated_at":  now,
	})
}

func (s *server) handleSync(w http.ResponseWriter, r *http.Request) {
	c := claimsFrom(r)
	tenantSlug := r.PathValue("tenant")
	tenant, err := s.db.GetTenantBySlug(r.Context(), tenantSlug)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "tenant not found"})
		return
	}

	// Permission check: super_admin OR tenant member with sync role
	if !c.IsAdmin {
		role, err := s.db.GetTenantRole(r.Context(), tenant.ID, c.Sub)
		if err != nil || !auth.CanSync(role) {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "no sync permission for this tenant"})
			return
		}
	}

	// Resolve sources from env (auto-derive same as CLI extractor)
	sources, err := resolveSourcesFromEnv(tenantSlug)
	if err != nil || len(sources) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "no sources configured for tenant"})
		return
	}

	traceID := r.Header.Get("X-Trace-ID")
	res, err := s.orch.Run(r.Context(), tenant.ID, tenantSlug, c.Sub, traceID, sources)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error(), "result": res})
		return
	}
	writeJSON(w, http.StatusAccepted, res)
}

func (s *server) handleAudit(w http.ResponseWriter, r *http.Request) {
	c := claimsFrom(r)
	tenantSlug := r.PathValue("tenant")
	tenant, err := s.db.GetTenantBySlug(r.Context(), tenantSlug)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "tenant not found"})
		return
	}
	if !c.IsAdmin {
		role, err := s.db.GetTenantRole(r.Context(), tenant.ID, c.Sub)
		if err != nil || !auth.CanAudit(role) {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "no audit access"})
			return
		}
	}
	entries, err := s.db.QueryAudit(r.Context(), tenant.ID, 50)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"tenant":  tenantSlug,
		"entries": entries,
		"count":   len(entries),
	})
}

func (s *server) handleMe(w http.ResponseWriter, r *http.Request) {
	c := claimsFrom(r)
	writeJSON(w, http.StatusOK, c)
}

// ─── Helpers ────────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		// Best-effort
		_ = err
	}
}

// resolveSourcesFromEnv mirrors cmd/extractor/main.go's resolveSources logic.
// Read FIGMA_FILE_KEY_<BRAND>_GLYPH + FIGMA_FILE_KEY_<BRAND> + FIGMA_NODE_ID_<BRAND>.
func resolveSourcesFromEnv(brand string) ([]extractor.Source, error) {
	bUp := strings.ToUpper(brand)
	var out []extractor.Source
	if v := os.Getenv("FIGMA_FILE_KEY_" + bUp + "_GLYPH"); v != "" {
		spec := string(extractor.SourceDesignSystem) + ":" + v
		if nid := os.Getenv("FIGMA_NODE_ID_" + bUp + "_GLYPH"); nid != "" {
			spec += ":" + nid
		}
		s, err := extractor.ParseSource(spec)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	if v := os.Getenv("FIGMA_FILE_KEY_" + bUp); v != "" {
		spec := string(extractor.SourceProduct) + ":" + v
		if nid := os.Getenv("FIGMA_NODE_ID_" + bUp); nid != "" {
			spec += ":" + nid
		}
		s, err := extractor.ParseSource(spec)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil, errors.New("no FIGMA_FILE_KEY_<BRAND>* env vars set")
	}
	return out, nil
}
