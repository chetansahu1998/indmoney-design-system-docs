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
	"database/sql"
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
		// Decrypt per-tenant Figma PAT. When decrypt fails it usually means
		// the row was encrypted under a different ENCRYPTION_KEY (typical
		// after an ephemeral-key restart). Surface that explicitly so the
		// admin UI can prompt re-upload instead of silently failing the
		// whole pipeline (audit finding B6). Falls back to FIGMA_PAT env
		// var when set so cmd-line workflows keep working during recovery.
		rec, err := dbConn.GetFigmaToken(ctx, tenantID)
		if err != nil {
			return nil, fmt.Errorf("get figma token: %w", err)
		}
		pat, err := cfg.EncryptionKey.Decrypt(rec.EncryptedToken)
		if err != nil {
			if envPAT := os.Getenv("FIGMA_PAT"); envPAT != "" {
				log.Warn("figma token decrypt failed; falling back to FIGMA_PAT env var (please re-upload via admin UI to clear this)",
					"tenant", tenantID, "key_version", rec.KeyVersion, "err", err.Error())
				pat = []byte(envPAT)
			} else {
				return nil, fmt.Errorf("decrypt figma token (tenant=%s key_version=%d — re-upload via admin UI): %w",
					tenantID, rec.KeyVersion, err)
			}
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

	// Phase 5.2 P4 — Figma PAT resolver. Returns the decrypted per-tenant
	// PAT for the figma-frame-metadata proxy. Tenants without a configured
	// PAT get an empty string + nil error so the proxy falls back to URL-
	// only metadata gracefully.
	figmaPATResolver := func(ctx context.Context, tenantID string) (string, error) {
		rec, err := dbConn.GetFigmaToken(ctx, tenantID)
		if err != nil {
			return "", err
		}
		if rec == nil {
			return "", nil
		}
		pat, err := cfg.EncryptionKey.Decrypt(rec.EncryptedToken)
		if err != nil {
			return "", err
		}
		return string(pat), nil
	}

	// Pr8 / D1 — asset-token signer for PNG/KTX2 image-loader auth. Derives
	// the HMAC key from the encryption key (already 32 bytes, persisted in
	// .env.local, rotates with the same operator workflow). Falls back to
	// nil when EncryptionKey isn't configured — the handler then rejects
	// `?at=` requests and the legacy `?token=<jwt>` fallback continues.
	var assetSigner *auth.AssetTokenSigner
	if cfg.EncryptionKey != nil {
		s, err := auth.NewAssetTokenSigner(cfg.EncryptionKey[:])
		if err == nil {
			assetSigner = s
		} else {
			log.Warn("asset signer init failed; PNG/KTX2 will require JWT bearer", "err", err.Error())
		}
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
		FigmaPATResolver: figmaPATResolver,
		AssetSigner:     assetSigner,
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
	workerCtx, workerCancel := context.WithCancel(context.Background())
	defer workerCancel()

	// Phase 6 — RebuildGraphIndex worker materialises the mind-graph
	// `graph_index` table from upstream sources. Constructed BEFORE the
	// audit WorkerPool so we can wire the audit-complete → graph-rebuild
	// hook with a real reference instead of a forward-declared closure.
	graphRebuildPool := &projects.GraphRebuildPool{
		Size:      graphRebuildWorkersFromEnv(log),
		DB:        dbConn.DB,
		TenantIDs: discoverTenantIDs(workerCtx, dbConn.DB, log),
		Sources: projects.GraphRebuildSources{
			ManifestPath: getenv("GRAPH_INDEX_MANIFEST_PATH",
				filepath.Join(cfg.RepoDir, "public/icons/glyph/manifest.json")),
			TokensDir: getenv("GRAPH_INDEX_TOKENS_DIR",
				filepath.Join(cfg.RepoDir, "lib/tokens/indmoney")),
		},
		Publisher: &projects.SSEGraphPublisher{Broker: broker},
		Log:       log,
	}

	workerPool := &projects.WorkerPool{
		Size:          workerPoolSize,
		Repo:          workerRepo,
		Runner:        compositeRunner,
		Broker:        broker,
		Notifications: auditEnqueuer.Notifications(),
		Log:           log,
		// Audit findings A5 / D3 — when an audit job marks done, refresh the
		// graph_index for both platforms of the affected tenant so the mind
		// graph's flow severity counts reflect the freshly-persisted
		// violations without waiting for the safety-net poll cycle.
		OnAuditComplete: func(tenantID, _versionID string) {
			for _, platform := range []string{"mobile", "web"} {
				graphRebuildPool.EnqueueIncremental(tenantID, platform,
					projects.GraphSourceFlows, "")
			}
		},
	}
	if err := workerPool.Start(workerCtx); err != nil {
		log.Error("worker pool start", "err", err)
		os.Exit(1)
	}
	if err := graphRebuildPool.Start(workerCtx); err != nil {
		log.Error("graph rebuild pool start", "err", err)
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

// graphRebuildWorkersFromEnv reads GRAPH_INDEX_REBUILD_WORKERS (default 1).
// Phase 6 — Phase 1 learning #6 mandates env-driven pool sizing on day one
// so production tuning doesn't require a code change.
func graphRebuildWorkersFromEnv(log *slog.Logger) int {
	const defaultSize = 1
	const maxSize = 8
	raw := os.Getenv("GRAPH_INDEX_REBUILD_WORKERS")
	if raw == "" {
		return defaultSize
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		log.Warn("graph_rebuild: GRAPH_INDEX_REBUILD_WORKERS not an integer; using default",
			"raw", raw, "default", defaultSize, "err", err.Error())
		return defaultSize
	}
	if n <= 0 {
		log.Warn("graph_rebuild: GRAPH_INDEX_REBUILD_WORKERS clamped to 1", "raw", n)
		return 1
	}
	if n > maxSize {
		log.Warn("graph_rebuild: GRAPH_INDEX_REBUILD_WORKERS clamped to max", "raw", n, "max", maxSize)
		return maxSize
	}
	return n
}

// discoverTenantIDs reads the `tenants` table so the rebuild worker can
// iterate every tenant's slice on the safety-net ticker. Failures degrade to
// "no full rebuilds" — incremental SSE-driven updates still work because
// they pass the tenant_id explicitly. The query is cheap (a single SELECT
// over a tiny table) so we run it at boot rather than caching.
func discoverTenantIDs(ctx context.Context, db *sql.DB, log *slog.Logger) []string {
	rows, err := db.QueryContext(ctx, `SELECT id FROM tenants ORDER BY id`)
	if err != nil {
		log.Warn("graph_rebuild: tenant discovery failed; safety-net ticker disabled",
			"err", err.Error())
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil && id != "" {
			out = append(out, id)
		}
	}
	if err := rows.Err(); err != nil {
		log.Warn("graph_rebuild: tenant discovery iter error; partial list",
			"err", err.Error(), "found", len(out))
	}
	return out
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
	// Pr8 — image-loader-friendly asset token. Authenticated mint endpoint
	// returns a short-lived `?at=<token>` URL the frontend can paste into
	// <img src> / TextureLoader without leaking the JWT into URLs.
	mux.HandleFunc("POST /v1/projects/{slug}/screens/{id}/png-url",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleMintAssetToken())))
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

	// Phase 4 U2 — bulk lifecycle endpoint. Accepts up to 100 violation_ids
	// in a single transaction; per-row audit_log entries share a bulk_id.
	mux.HandleFunc("POST /v1/projects/{slug}/violations/bulk-acknowledge",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleBulkAcknowledge)))

	// Phase 4 U4 — designer personal inbox. Tenant-scoped Active
	// violations across the user's projects + flows, with filter chips
	// (rule_id, category, persona, mode, project, severity, dates) and
	// "Load more" pagination capped at 100 rows per page.
	mux.HandleFunc("GET /v1/inbox",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleInbox)))

	// Phase 4.1 — tenant-scoped SSE for the inbox. Lifecycle events
	// fan out under both the project trace_id (Violations tab) and
	// the synthetic inbox:<tenant_id> channel so cross-project
	// reconciliation works without per-project subscriptions.
	mux.HandleFunc("POST /v1/inbox/events/ticket",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleInboxEventsTicket)))
	mux.HandleFunc("GET /v1/inbox/events", s.projectsServer.HandleInboxEvents)

	// Phase 6 U2 — mind-graph aggregate read + SSE bust channel.
	// The handler runs a single indexed SELECT against graph_index; the
	// RebuildGraphIndex worker materialises rows out-of-band. SSE channel
	// graph:<tenant>:<platform> emits GraphIndexUpdated whenever the
	// worker flushes for that slice (read-after-write contract).
	mux.HandleFunc("GET /v1/projects/graph",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleGraphAggregate)))
	mux.HandleFunc("POST /v1/projects/graph/events/ticket",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleGraphEventsTicket)))
	mux.HandleFunc("GET /v1/projects/graph/events", s.projectsServer.HandleGraphEvents)

	// Phase 8 U9 — global search backed by SQLite FTS5. Tenant scoped via
	// claims; ACL filter joins flow_grants. See internal/projects/search.go.
	mux.HandleFunc("GET /v1/search",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleSearch)))

	// Phase 7 U2 — rule catalog editor (super-admin gated inside handler).
	mux.HandleFunc("GET /v1/atlas/admin/rules",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleAdminListRules)))
	mux.HandleFunc("PATCH /v1/atlas/admin/rules/{rule_id}",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleAdminPatchRule)))

	// Phase 7 U4 — persona library approval queue.
	mux.HandleFunc("GET /v1/atlas/admin/personas/pending",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleAdminListPendingPersonas)))
	mux.HandleFunc("POST /v1/atlas/admin/personas/{id}/approve",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleAdminApprovePersona)))
	mux.HandleFunc("POST /v1/atlas/admin/personas/{id}/reject",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleAdminRejectPersona)))

	// Phase 7.5 / U3 — taxonomy curator.
	mux.HandleFunc("GET /v1/atlas/admin/taxonomy",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleAdminListTaxonomy)))
	mux.HandleFunc("POST /v1/atlas/admin/taxonomy/promote",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleAdminPromoteTaxonomy)))
	mux.HandleFunc("POST /v1/atlas/admin/taxonomy/archive",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleAdminArchiveTaxonomy)))
	mux.HandleFunc("POST /v1/atlas/admin/taxonomy/reorder",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleAdminReorderTaxonomy)))

	// Phase 7.5 — notification preferences (per-user, per-channel CRUD).
	mux.HandleFunc("GET /v1/users/me/notification-preferences",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleListMyNotificationPrefs)))
	mux.HandleFunc("PUT /v1/users/me/notification-preferences",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleUpsertMyNotificationPref)))

	// Phase 4 U7 — per-component reverse view. Cross-tenant aggregate +
	// tenant-scoped per-flow detail for "Where this breaks". The
	// component is identified by display name via ?name= rather than
	// slug, since the slug→name map lives in the docs site's manifest.
	mux.HandleFunc("GET /v1/components/violations",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleComponentViolations)))

	// Phase 4 U9 — DS-lead dashboard summary (5 aggregations: by_product,
	// by_severity, trend, top_violators, recent_decisions). Super-admin
	// only; wraps every aggregation in a parallel goroutine.
	mux.HandleFunc("GET /v1/atlas/admin/summary",
		s.requireSuperAdmin(s.projectsServer.HandleDashboardSummary))

	// Phase 4 U12 — single-violation fetch + fix-applied success ping.
	// Used by the plugin's auto-fix deeplink flow: GET resolves the
	// violation context, POST flips status to fixed via the system
	// transition (idempotent on already-fixed rows).
	// Phase 7.8 — list violations for the active version. Backs the
	// Violations tab on /projects/{slug}; was missing while the
	// frontend client.ts shipped pointing at this URL.
	mux.HandleFunc("GET /v1/projects/{slug}/violations",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleListViolations)))
	mux.HandleFunc("GET /v1/projects/{slug}/violations/{id}",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleViolationGet)))
	mux.HandleFunc("POST /v1/projects/{slug}/violations/{id}/fix-applied",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleViolationFixApplied)))

	// Phase 5 U3 — Decisions as a first-class entity. Per-flow create +
	// list, single-decision get, and a super-admin recent-decisions
	// feed that powers /atlas/admin's panel.
	mux.HandleFunc("POST /v1/projects/{slug}/flows/{flow_id}/decisions",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleDecisionCreate)))
	mux.HandleFunc("GET /v1/projects/{slug}/flows/{flow_id}/decisions",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleDecisionList)))
	mux.HandleFunc("GET /v1/decisions/{id}",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleDecisionGet)))
	// Phase 6 U7 — Linked violations subsection on DecisionCard.
	mux.HandleFunc("GET /v1/decisions/{id}/violations",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleDecisionViolations)))
	mux.HandleFunc("GET /v1/atlas/admin/decisions/recent",
		s.requireSuperAdmin(s.projectsServer.HandleRecentDecisions))

	// Phase 5.2 P1 — admin moderation: re-flip a superseded decision
	// back to accepted. Cross-tenant write — guarded by requireSuperAdmin.
	mux.HandleFunc("POST /v1/atlas/admin/decisions/{id}/reactivate",
		s.requireSuperAdmin(s.projectsServer.HandleAdminReactivateDecision))

	// Phase 5 U6 — universal comments with @mention parsing. Comments
	// can target a DRD block, a decision, or a violation; the server
	// parses @mentions inside the body and emits notification rows in
	// the same DB tx as the comment insert. Resolution is per-comment.
	mux.HandleFunc("POST /v1/projects/{slug}/comments",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleCommentCreate)))
	mux.HandleFunc("GET /v1/projects/{slug}/comments",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleCommentList)))
	mux.HandleFunc("POST /v1/comments/{id}/resolve",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleCommentResolve)))

	// Phase 5 U7 — notifications inbox API. The SSE channel for new
	// notifications is the existing tenant inbox channel (Phase 4.1
	// U2); these endpoints back the Mentions filter chip on /inbox.
	mux.HandleFunc("GET /v1/notifications",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleNotificationsList)))
	mux.HandleFunc("POST /v1/notifications/mark-read",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleNotificationsMarkRead)))

	// Phase 5 U12 — activity rail timeline. Reads audit_log scoped by
	// tenant + (json_extract details.flow_id == flow_id) ordered DESC.
	mux.HandleFunc("GET /v1/projects/{slug}/flows/{flow_id}/activity",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleFlowActivity)))

	// Phase 5.2 P4 — Figma frame metadata proxy. Auth-gated, tenant-
	// scoped. Powers the DRD's figmaLink custom block thumbnails.
	mux.HandleFunc("GET /v1/figma/frame-metadata",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleFigmaFrameMetadata)))

	// Phase 5 U1 — DRD collab. Public ticket endpoint (auth-gated)
	// + the loopback-only auth/load/snapshot bridge endpoints the
	// Hocuspocus sidecar calls. The sidecar is expected to run on
	// the internal network and to send DS_HOCUSPOCUS_SHARED_SECRET in
	// the X-DS-Hocuspocus-Secret header for the /internal/* routes.
	mux.HandleFunc("POST /v1/projects/{slug}/flows/{flow_id}/drd/ticket",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleDRDTicket)))
	mux.HandleFunc("POST /internal/drd/auth",
		s.projectsServer.HandleDRDInternalAuthGated())
	mux.HandleFunc("GET /internal/drd/load",
		s.projectsServer.HandleDRDInternalLoadGated())
	mux.HandleFunc("POST /internal/drd/snapshot",
		s.projectsServer.HandleDRDInternalSnapshotGated())
}

// ─── Middleware ─────────────────────────────────────────────────────────────

func (s *server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		for _, raw := range s.cfg.CORSAllowOrigin {
			allowed := strings.TrimSpace(raw)
			if originMatchesAllowed(origin, allowed) {
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

// originMatchesAllowed compares an origin against an allow-list entry.
// Supports a single leading-glob `https://*.vercel.app` so Vercel preview
// deployments (which mint a unique hash per build) don't have to be added
// one-by-one. The glob position is fixed: only the host's leftmost label
// can be `*` — `https://*.vercel.app` matches `https://foo.vercel.app`
// but NOT `https://foo.bar.vercel.app` (no nested wildcards) and NOT
// `https://vercel.app` (the wildcard requires at least one label).
func originMatchesAllowed(origin, allowed string) bool {
	if origin == allowed {
		return true
	}
	// Glob pattern: scheme://*.suffix
	idx := strings.Index(allowed, "://*.")
	if idx == -1 {
		return false
	}
	scheme := allowed[:idx+3]              // "https://"
	suffix := allowed[idx+5:]              // "vercel.app"
	if !strings.HasPrefix(origin, scheme) {
		return false
	}
	host := origin[len(scheme):]
	// Reject a bare suffix (no subdomain) so the wildcard can't match
	// the apex domain.
	if !strings.HasSuffix(host, "."+suffix) {
		return false
	}
	// And reject nested subdomains so a single `*` doesn't match
	// arbitrarily-deep hosts.
	prefix := strings.TrimSuffix(host, "."+suffix)
	return prefix != "" && !strings.Contains(prefix, ".")
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

// Flush passes through to the underlying writer so SSE handlers can
// stream. Without this, w.(http.Flusher) returns false on the wrapper
// and HandleGraphEvents / HandleProjectEvents 500 with "no_streaming"
// every connect — exactly the failure the /atlas mind-graph mount
// reported when each ticket-redeem turned into a 500.
func (sr *statusRecorder) Flush() {
	if f, ok := sr.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

type ctxKey string

const ctxClaims ctxKey = "claims"

func claimsFrom(r *http.Request) *auth.Claims {
	c, _ := r.Context().Value(ctxClaims).(*auth.Claims)
	return c
}

func (s *server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Primary: Authorization header (Bearer token) — what every JSON
		// caller uses (browser fetch, plugin POSTs, curl smoke tests).
		raw := r.Header.Get("Authorization")
		token := ""
		if strings.HasPrefix(raw, "Bearer ") {
			token = strings.TrimPrefix(raw, "Bearer ")
		}
		// Fallback for GET requests only: ?token=<jwt> in the query string.
		// Image / binary loaders (THREE.TextureLoader, KTX2Loader,
		// <img src>) cannot set custom headers — the browser's image
		// pipeline owns that request. Without this fallback, every
		// /screens/:id/{png,ktx2} URL 401s on the project view's atlas
		// canvas. Gated to GET so we don't accept tokens-in-URL on any
		// state-changing request — bears the same tradeoffs as
		// CloudFront signed URLs / Vercel image-signing tokens.
		if token == "" && r.Method == http.MethodGet {
			token = r.URL.Query().Get("token")
		}
		// Pr8 — asset-token path: when the caller supplies `?at=<token>`
		// on a GET, skip JWT verification at the middleware. The downstream
		// handler verifies the asset MAC against the resolved (tenant, screen)
		// pair before serving any bytes. Restricted to GET so state-changing
		// routes still demand a real JWT.
		if token == "" && r.Method == http.MethodGet && r.URL.Query().Get("at") != "" {
			next.ServeHTTP(w, r)
			return
		}
		if token == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "missing bearer token"})
			return
		}
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

	// Resolve real tenant UUIDs from tenant_users — projects.HandleExport
	// uses claims.Tenants[0] verbatim as the tenant_id in INSERTs, so a
	// slug here triggers a FOREIGN KEY violation on the tenants(id) ref.
	tenants, err := s.db.GetUserTenantIDs(r.Context(), u.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "tenant lookup: " + err.Error()})
		return
	}
	if len(tenants) == 0 {
		// Super-admins without an explicit membership still need to land
		// somewhere — fall back to the bootstrap tenant (the first row
		// created by migrations or the bootstrap-token flow). Regular users
		// without a membership are rejected.
		if u.Role == auth.RoleSuperAdmin {
			var fallback string
			if err := s.db.QueryRowContext(r.Context(),
				`SELECT id FROM tenants WHERE id != 'system' ORDER BY created_at LIMIT 1`).Scan(&fallback); err == nil && fallback != "" {
				tenants = []string{fallback}
			}
		}
		if len(tenants) == 0 {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "no tenant membership"})
			return
		}
	}
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
