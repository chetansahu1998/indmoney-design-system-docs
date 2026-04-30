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
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/auditbyslug"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/auth"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/db"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/figma/client"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/figma/extractor"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/projects"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/projects/rules"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/sse"
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

	// ─── Projects (U4) wiring ────────────────────────────────────────────
	// Build the SSE broker, ticket store, rate limiter, idempotency cache,
	// and audit-job enqueuer once at process boot — all are in-memory and
	// outlive any single request.
	broker := sse.NewMemoryBroker(sse.BrokerOptions{Logger: log})
	tickets := sse.NewMemoryTicketStore(sse.DefaultTicketGCInterval)
	rateLimiter := projects.NewRateLimiter()
	idempotencyCache := projects.NewIdempotencyCache()
	auditEnqueuer := projects.NewAuditEnqueuer()
	projectsAuditLogger := &projects.AuditLogger{DB: dbConn}

	dataDir := filepath.Join(cfg.RepoDir, "services/ds-service/data")
	// Phase 3.5 U2: KTX2 transcoder. Probes basisu on PATH at boot;
	// when missing, the transcoder is Available=false and every
	// Transcode call short-circuits gracefully.
	ktx2 := projects.NewKTX2Transcoder(log)
	pipelineFactory := func(ctx context.Context, tenantID string, repo *projects.TenantRepo) (*projects.Pipeline, error) {
		// Decrypt per-tenant Figma PAT.
		rec, err := dbConn.GetFigmaToken(ctx, tenantID)
		if err != nil {
			return nil, fmt.Errorf("get figma token: %w", err)
		}
		pat, err := cfg.EncryptionKey.Decrypt(rec.EncryptedToken)
		if err != nil {
			return nil, fmt.Errorf("decrypt figma token: %w", err)
		}
		fc := client.New(string(pat))
		renderer := projects.NewHTTPFigmaRenderer(string(pat))
		return &projects.Pipeline{
			Repo:          repo,
			Renderer:      renderer,
			NodeFetcher:   fc,
			SSE:           broker,
			AuditEnqueuer: auditEnqueuer,
			AuditLogger:   projectsAuditLogger,
			DataDir:       dataDir,
			Log:           log,
			KTX2:          ktx2,
		}, nil
	}

	projectsServer := projects.NewServer(projects.ServerDeps{
		DB:              dbConn,
		Broker:          broker,
		Tickets:         tickets,
		RateLimiter:     rateLimiter,
		Idempotency:     idempotencyCache,
		AuditLogger:     projectsAuditLogger,
		AuditEnqueuer:   auditEnqueuer,
		DataDir:         dataDir,
		PipelineFactory: pipelineFactory,
		Log:             log,
	})

	// Recovery sweeper — startup sweep + 60s loop. Background goroutine.
	recoveryCtx, recoveryCancel := context.WithCancel(context.Background())
	defer recoveryCancel()
	go projects.RunRecoveryLoop(recoveryCtx, dbConn.DB, log)

	// ─── Audit-job worker pool (Phase 1 U5; Phase 2 U7 scaling) ──────────
	// Phase 1 shipped size=1. Phase 2 scales to 6 (env-tunable via
	// DS_AUDIT_WORKERS) so AE-7's 47-flow fan-out can land within the 5-min
	// budget. Heartbeat goroutine + ResetStaleRunningJobs + ClaimNextJob's
	// stale-lease takeover already shipped in Phase 1.
	workerRepo := projects.NewWorkerRepo(dbConn.DB, nil)
	auditCoreRunner := projects.NewAuditCoreRunner(projects.AuditCoreRunnerConfig{
		Loader: projects.NewDBVersionScreenLoader(dbConn.DB),
		// Phase 1 ships with empty token + candidate slices; the audit core
		// emits zero violations against an empty catalog.
	})
	// Phase 2 prod-wire: wrap the audit-core runner in a tenant-aware
	// composite that fans out to every Phase 2 rule (theme parity / cross-
	// persona / a11y contrast / a11y touch target / flow graph / component
	// governance). The composite reads the tenant_id from the version on each
	// Run() call so the worker can hold a single Runner pointer at boot.
	//
	// Variable resolution caveat: until the Go-side resolver mirroring
	// lib/projects/resolveTreeForMode.ts ships, theme_parity + a11y_contrast
	// run in degraded mode (per-mode trees are identical until the resolver
	// can apply per-mode bindings). See loaders.go header comment.
	compositeRunner := rules.NewTenantAwareRunner(dbConn.DB, auditCoreRunner, nil)
	workerPoolSize := workerPoolSizeFromEnv(log)
	workerPool := &projects.WorkerPool{
		Size:          workerPoolSize,
		Repo:          workerRepo,
		Runner:        compositeRunner,
		Broker:        broker,
		Notifications: auditEnqueuer.Notifications(),
		Log:           log,
	}
	workerCtx, workerCancel := context.WithCancel(context.Background())
	defer workerCancel()
	if err := workerPool.Start(workerCtx); err != nil {
		log.Error("worker pool start", "err", err)
		os.Exit(1)
	}

	srv := &server{
		cfg:            cfg,
		db:             dbConn,
		jwt:            cfg.JWTKey,
		enc:            cfg.EncryptionKey,
		orch:           orch,
		log:            log,
		projectsServer: projectsServer,
		broker:         broker,
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

// workerPoolSizeFromEnv reads DS_AUDIT_WORKERS and returns a clamped pool size.
// Defaults to 6 (Phase 2 target — AE-7 47-flow fan-out under 5min). Clamped to
// [1, 32]: 0/negative → 1 (single worker still serves), >32 → 32 (avoid
// runaway parallelism on shared SQLite which serializes writes anyway). Logs
// the resolved size + reason at boot for ops visibility.
func workerPoolSizeFromEnv(log *slog.Logger) int {
	const defaultSize = 6
	const maxSize = 32
	raw := os.Getenv("DS_AUDIT_WORKERS")
	if raw == "" {
		return defaultSize
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		log.Warn("worker_pool: DS_AUDIT_WORKERS not an integer; using default",
			"raw", raw, "default", defaultSize, "err", err.Error())
		return defaultSize
	}
	if n <= 0 {
		log.Warn("worker_pool: DS_AUDIT_WORKERS clamped to 1", "raw", n)
		return 1
	}
	if n > maxSize {
		log.Warn("worker_pool: DS_AUDIT_WORKERS clamped to max", "raw", n, "max", maxSize)
		return maxSize
	}
	return n
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
	cfg            *config
	db             *db.DB
	jwt            *auth.SigningKey
	enc            *auth.EncryptionKey
	orch           *sync.Orchestrator
	log            *slog.Logger
	projectsServer *projects.Server
	broker         projects.SSEPublisher // shared SSE publisher; used by Phase 2 fan-out + audit-job runs
}

func (s *server) routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /__health", s.handleHealth)
	mux.HandleFunc("POST /v1/auth/login", s.handleLogin)
	mux.HandleFunc("POST /v1/admin/bootstrap", s.handleBootstrap)
	mux.HandleFunc("POST /v1/admin/figma-token", s.requireSuperAdmin(s.handleFigmaTokenUpload))
	mux.HandleFunc("POST /v1/sync/{tenant}", s.requireAuth(s.handleSync))
	mux.HandleFunc("GET /v1/audit/{tenant}", s.requireAuth(s.handleAudit))
	mux.HandleFunc("GET /v1/me", s.requireAuth(s.handleMe))

	// ─── Projects (U4) ───────────────────────────────────────────────────
	// Auth shape is identical to the existing /v1/audit and /v1/sync routes:
	// JWT bearer token verified by requireAuth, claims placed in r.Context()
	// under the cmd/server key. The projects package re-reads them via the
	// AdaptAuthMiddleware shim to keep its handlers stdlib-portable.
	claimsReader := func(r *http.Request) *auth.Claims { return claimsFrom(r) }
	mux.HandleFunc("POST /v1/projects/export",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleExport)))
	mux.HandleFunc("GET /v1/projects",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleProjectList)))
	mux.HandleFunc("GET /v1/projects/{slug}",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleProjectGet)))
	mux.HandleFunc("POST /v1/projects/{slug}/events/ticket",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleEventsTicket)))
	// SSE events endpoint is ticket-authed (NOT JWT) — EventSource cannot send
	// Authorization headers. Ticket is single-use, scoped to user/tenant/trace,
	// 60s TTL.
	mux.HandleFunc("GET /v1/projects/{slug}/events", s.projectsServer.HandleProjectEvents)

	// PNG screenshot route (U11). Auth-gated, tenant-scoped via repo. Files
	// live under services/ds-service/data/screens/ (NOT public/) — the
	// route streams them with Cache-Control: private and tenant-isolation
	// returning 404 (no existence oracle). Phase 8 swaps to S3 signed URLs
	// without changing the route shape.
	mux.HandleFunc("GET /v1/projects/{slug}/screens/{id}/png",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleScreenPNG())))
	// Phase 3.5 U2 — KTX2 sidecar route. Returns 404 when basisu wasn't
	// on PATH at persist time; frontend falls back to .png.
	mux.HandleFunc("GET /v1/projects/{slug}/screens/{id}/ktx2",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleScreenKTX2())))

	// Canonical-tree lazy-fetch (U8). The JSON tab calls this on screen
	// click. Auth-gated, tenant-scoped, returns the raw canonical_tree JSON
	// from screen_canonical_trees with a 60s private cache.
	mux.HandleFunc("GET /v1/projects/{slug}/screens/{id}/canonical-tree",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleScreenCanonicalTree)))

	// DRD per-flow content (U9). GET returns the BlockNote document +
	// monotonic revision counter; PUT updates with optimistic concurrency
	// (409 on stale revision). Body capped at 1MB.
	mux.HandleFunc("GET /v1/projects/{slug}/flows/{flow_id}/drd",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleGetDRD)))
	mux.HandleFunc("PUT /v1/projects/{slug}/flows/{flow_id}/drd",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandlePutDRD)))

	// Phase 2 U10 — Audit-by-slug read path. /files/[slug] in the docs site
	// reads from this endpoint instead of importing the JSON sidecar at build
	// time. Returns the same lib/audit/types.ts AuditResult shape so the
	// frontend doesn't change types. System-tenant fallback gated by env
	// DS_AUDIT_BY_SLUG_INCLUDE_SYSTEM (default on) — backfilled sidecar rows
	// live under the system tenant and cross-tenant query is a 404 by default.
	mux.HandleFunc("GET /v1/audit/by-slug/{slug}",
		s.requireAuth(auditbyslug.Handler(auditbyslug.Deps{
			DB:           s.db.DB,
			ClaimsReader: claimsReader,
			Log:          s.log,
		})))

	// Phase 2 U8 — Audit fan-out trigger. Super-admin only. Enqueues
	// audit_jobs at priority=10 for every active flow's latest version when
	// DS lead publishes a token catalog change or curates a rule. CLI at
	// services/ds-service/cmd/admin wraps this endpoint.
	fanoutHandler := &projects.FanoutHandler{DB: s.db.DB, Broker: s.broker}
	mux.HandleFunc("POST /v1/admin/audit/fanout", s.requireSuperAdmin(fanoutHandler.HandleAdminFanout))

	// Phase 4 U1 — violation lifecycle. Acknowledge / Dismiss /
	// Reactivate (admin override) on a single violation. Audit-log + SSE
	// fan-out happen in the same DB transaction as the status flip.
	mux.HandleFunc("PATCH /v1/projects/{slug}/violations/{id}",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandlePatchViolation)))
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
