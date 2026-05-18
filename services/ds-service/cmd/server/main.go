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
//	GET  /v1/admin/prerender/status     — Stage 9 prerender ring buffer (auth: super_admin)
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
	"bytes"
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/auditbyslug"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/auth"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/db"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/figma/client"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/figma/inventory"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/figma/extractor"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/mcp"
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

	// DevAuthBypass — when true (env DEV_AUTH_BYPASS=1), the auth
	// middleware skips JWT verification entirely and injects synthetic
	// dev-user claims so local development needs no token paste / login
	// dance. Production MUST leave this unset; startup logs WARN when set
	// so an accidental prod deploy is loud. The synthetic claims pin to
	// DevAuthBypassTenant so cross-tenant data is still segregated.
	DevAuthBypass       bool
	DevAuthBypassTenant string
	DevAuthBypassEmail  string

	// DevAuthBypassAdmin — opt-in escalation on top of DevAuthBypass. When
	// true (env DEV_AUTH_BYPASS_ADMIN=1) AND DevAuthBypass is true, the
	// synthetic dev claims also carry Role=super_admin / IsAdmin=true so
	// local devs can hit /v1/admin/* routes without minting a separate
	// super-admin JWT. Defaults to false — the conservative shape keeps
	// the bypass at Role=user. Production refusal (looksLikeProd guard
	// above) still blocks any DEV_AUTH_BYPASS variant; this var only
	// changes the local-dev role.
	DevAuthBypassAdmin bool

	// HTTP/2 via TLS. When both cert + key paths are non-empty the server
	// also binds a TLS listener on TLSAddr (default :8443). Browsers won't
	// negotiate HTTP/2 over cleartext, so this is the only way the canvas
	// asset GETs escape the HTTP/1.1 6-conn-per-origin cap. Production
	// behind an h2-terminating edge (Vercel, Fly, Cloudflare) doesn't
	// need this set — the edge speaks h2 to the browser regardless.
	TLSCertPath string
	TLSKeyPath  string
	TLSAddr     string
}

func main() {
	loadDotEnv()
	log := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// Process-wide shutdown context. Cancelled on SIGTERM / SIGINT so
	// background goroutines (currently Stage 9 cluster prerender) can
	// observe the signal and exit cleanly rather than getting killed
	// mid-write. HTTP handlers continue to use request-scoped contexts;
	// only background work derives from this.
	shutdownCtx, stopShutdown := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stopShutdown()

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
	// Stage 9 (cluster prerender) status ring buffer. Process-wide,
	// single instance, surfaced via GET /v1/admin/prerender/status.
	// nil-safe Append in pipeline.go means tests / embedded callers
	// that bypass this wiring still work.
	prerenderStatus := projects.NewPrerenderStatusBuffer(0)
	// Forward-declared so the pipelineFactory closure can pass them into
	// the per-tenant Pipeline. Actual instances are built further below
	// (they depend on the DB + figmaPATResolver which the closure path
	// also threads through). Stage 9 (cluster prerender) needs both.
	var previewPyramid *projects.PreviewPyramidGenerator
	var assetExporter *projects.AssetExporter
	var imageFillResolver *projects.ImageFillResolver
	pipelineFactory := func(ctx context.Context, tenantID string, repo *projects.TenantRepo) (*projects.Pipeline, error) {
		// Decrypt per-tenant Figma PAT. When decrypt fails it usually means
		// the row was encrypted under a different ENCRYPTION_KEY (typical
		// after an ephemeral-key restart). Surface that explicitly so the
		// admin UI can prompt re-upload instead of silently failing the
		// whole pipeline (audit finding B6). Falls back to FIGMA_PAT env
		// var when set so cmd-line workflows keep working during recovery.
		rec, err := dbConn.GetFigmaToken(ctx, tenantID)
		var pat []byte
		if err != nil {
			// No per-tenant row (e.g. autosync runs before admin uploaded
			// a token, or a fresh tenant). Fall back to FIGMA_PAT env var
			// — mirrors the decrypt-failure branch below + the poller's
			// figmaPATResolver fallback.
			if envPAT := os.Getenv("FIGMA_PAT"); envPAT != "" {
				log.Warn("no figma_tokens row; falling back to FIGMA_PAT env var",
					"tenant", tenantID, "err", err.Error())
				pat = []byte(envPAT)
			} else {
				return nil, fmt.Errorf("get figma token: %w", err)
			}
		} else {
			pat, err = cfg.EncryptionKey.Decrypt(rec.EncryptedToken)
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
		}
		fc := client.New(string(pat))
		renderer := projects.NewHTTPFigmaRenderer(string(pat))
		return &projects.Pipeline{
			Repo:           repo,
			Renderer:       renderer,
			NodeFetcher:    fc,
			SSE:            broker,
			AuditEnqueuer:  auditEnqueuer,
			AuditLogger:    projectsAuditLogger,
			DataDir:        dataDir,
			Log:            log,
			KTX2:           ktx2,
			PreviewPyramid:    previewPyramid,    // captured by reference; nil until below assigns
			AssetExporter:     assetExporter,     // ditto
			ImageFillResolver: imageFillResolver, // ditto — pre-warms image-fill cache during Stage 4
			ShutdownCtx:       shutdownCtx,       // wires SIGTERM into Stage 9 background prerender
			PrerenderStatus:   prerenderStatus,   // U8 — operator observability
			ManifestPath: filepath.Join(cfg.RepoDir, "public/icons/glyph/manifest.json"), // Stage 6.7 organism detection
		}, nil
	}

	// Phase 5.2 P4 — Figma PAT resolver. Returns the decrypted per-tenant
	// PAT for the figma-frame-metadata proxy and the FIGMA DB inventory
	// poller. Tenants without a configured PAT get the FIGMA_PAT env var
	// (if set) so local dev / first-run sync works without uploading a
	// token, matching the pipelineFactory's existing fallback. If neither
	// the DB row nor the env var has a value, returns empty + nil so
	// callers can degrade gracefully (e.g. the frame-metadata proxy).
	figmaPATResolver := func(ctx context.Context, tenantID string) (string, error) {
		rec, err := dbConn.GetFigmaToken(ctx, tenantID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			if envPAT := os.Getenv("FIGMA_PAT"); envPAT != "" {
				return envPAT, nil
			}
			return "", err
		}
		if rec == nil {
			if envPAT := os.Getenv("FIGMA_PAT"); envPAT != "" {
				return envPAT, nil
			}
			return "", nil
		}
		pat, err := cfg.EncryptionKey.Decrypt(rec.EncryptedToken)
		if err != nil {
			if envPAT := os.Getenv("FIGMA_PAT"); envPAT != "" {
				return envPAT, nil
			}
			return "", err
		}
		return string(pat), nil
	}

	// U5 — asset exporter: server-side renderer for Figma asset exports
	// (icons / illustrations / glyphs as SVG/PNG) backed by the asset_cache
	// table. The per-tenant Figma PAT is decrypted on each call via
	// figmaPATResolver above; tenantID is read from the ctx that
	// RenderAssetsForLeaf stashes before invoking GetImages.
	assetExporter = &projects.AssetExporter{
		Repo: projects.NewTenantRepo(dbConn.DB, ""),
		URLs: figmaImageURLFetcherFunc(func(ctx context.Context, fileKey string, nodeIDs []string, format string, scale int) (map[string]string, error) {
			tenantID := projects.AssetExportTenantFromCtx(ctx)
			if tenantID == "" {
				return nil, fmt.Errorf("asset export: no tenant in ctx")
			}
			pat, err := figmaPATResolver(ctx, tenantID)
			if err != nil {
				return nil, err
			}
			if pat == "" {
				return nil, fmt.Errorf("figma pat not configured for tenant %s", tenantID)
			}
			return client.New(pat).GetImages(ctx, fileKey, nodeIDs, format, scale)
		}),
		Bytes: &projects.HTTPAssetByteFetcher{
			Client: &http.Client{Timeout: 5 * time.Minute},
		},
		DataDir: dataDir,
		Now:     time.Now,
	}

	// Image-fill resolver — proxies Figma /v1/files/<key>/images so the
	// canvas-v2 LeafFrameRenderer can resolve canonical_tree imageRef
	// hashes to cached blobs. Reuses the same byte fetcher + data dir as
	// the asset exporter; the URL fetcher is a separate stub so it can
	// hit a different Figma endpoint without sharing /v1/images plumbing.
	imageFillResolver = &projects.ImageFillResolver{
		DB: dbConn.DB,
		URLs: figmaImageFillURLFetcherFunc(func(ctx context.Context, fileKey string) (map[string]string, error) {
			tenantID := projects.AssetExportTenantFromCtx(ctx)
			if tenantID == "" {
				return nil, fmt.Errorf("image-fill resolver: no tenant in ctx")
			}
			pat, err := figmaPATResolver(ctx, tenantID)
			if err != nil {
				return nil, err
			}
			if pat == "" {
				return nil, fmt.Errorf("figma pat not configured for tenant %s", tenantID)
			}
			return client.New(pat).GetFileImageFills(ctx, fileKey)
		}),
		Bytes: &projects.HTTPAssetByteFetcher{
			Client: &http.Client{Timeout: 5 * time.Minute},
		},
		DataDir: dataDir,
		Now:     time.Now,
	}

	// Preview-pyramid generator — U1 of plan 2026-05-06-001. Uses
	// AssetExporter as the source for the PNG@scale=2 it downsamples
	// to 128/512/1024/2048 tiers. Lives next to the asset exporter so
	// they share dataDir + the bytes fetcher implicitly via the source
	// path's RenderAssetsForLeaf call.
	//
	// Assigns into the previewPyramid forward-declaration above so the
	// pipelineFactory closure picks it up — every Pipeline built from
	// this point on will run Stage 9 (cluster prerender).
	previewPyramid = &projects.PreviewPyramidGenerator{
		Source: &projects.AssetExporterPreviewSource{
			Exporter: assetExporter,
			TenantBoundExporter: func(tenantID string) *projects.AssetExporter {
				return projects.TenantBoundExporter(assetExporter, dbConn.DB, tenantID)
			},
			DataDir: dataDir,
		},
		DataDir: dataDir,
		Now:     time.Now,
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

	// projectsServer is constructed below, AFTER graphRebuildPool exists, so
	// the T3 enqueueGraphRebuild hook can hold a real reference instead of
	// a forward-declared closure. (`var projectsServer *projects.Server`
	// would also work but defers the type check; the explicit ordering is
	// easier to follow.)
	var projectsServer *projects.Server

	// Recovery sweeper — startup sweep + 60s loop. Background goroutine.
	// REL-B1 audit fix: derive from shutdownCtx (not context.Background)
	// so SIGTERM/SIGINT actually cancels the sweep + blocklist loop. Pre-
	// fix the recoveryCancel deferred below only fired if main() returned,
	// which it didn't under signal-driven shutdown (http.ListenAndServe
	// blocks). Same bug class as F6's workerCtx fix.
	recoveryCtx, recoveryCancel := context.WithCancel(shutdownCtx)
	defer recoveryCancel()
	go projects.RunRecoveryLoop(recoveryCtx, dbConn.DB, log)
	// figma_render_blocklist stale-row sweep (2026-05-12). Hourly GC of
	// rows past BlocklistStaleThreshold (7 days). Bounded growth even
	// under heavy file-churn workloads.
	go projects.RunBlocklistSweepLoop(recoveryCtx, dbConn.DB, log)

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
	// F6 — derive workerCtx from shutdownCtx so SIGTERM/SIGINT cancels
	// every background goroutine (inventory poller, worker pool, graph
	// rebuild, autosync retry loop, audit-pipeline goroutines) at the
	// same time the HTTP server gets a shutdown signal. Pre-fix this
	// was rooted at context.Background() and the defer workerCancel
	// below never fired because http.ListenAndServe blocks until
	// os.Exit, leaving every <-ctx.Done() branch dead code under
	// signal-driven shutdown.
	workerCtx, workerCancel := context.WithCancel(shutdownCtx)
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

	// FIGMA DB inventory poller (migration 0025). Re-discovers tenants on
	// each cycle via discoverTenantIDs so a freshly-onboarded tenant joins
	// without a restart. Uses the same figmaPATResolver as the audit
	// pipeline — the shared per-PAT rate limiter inside client.Client
	// keeps the inventory poller polite alongside Pipeline calls.
	inventoryPoller, err := inventory.New(inventory.Config{
		DB:         dbConn.DB,
		ResolvePAT: figmaPATResolver,
		ListTenants: func(ctx context.Context) []string {
			return discoverTenantIDs(ctx, dbConn.DB, log)
		},
		Logger: log,
		// Plan U3b — wire the SSE broker so the autosync sub_flow bridge
		// can emit figma.design_shipped when a section first appears (or
		// moves) onto a final-classified page. The same MemoryBroker the
		// HTTP server uses; *MemoryBroker satisfies SubFlowEventBroker.
		Broker: broker,
		// Plan 2026-05-17-004 U5 — populate figma_node_metadata (mig
		// 0034) with depth=1 direct-child frames per section as part of
		// the deep-sync cycle. Replaces the manual /tmp/run_step2_*.py
		// scripts: a fresh DB now backfills automatically.
		NodeMetadataExtractor: &inventory.NodeMetadataExtractor{
			ResolvePAT: figmaPATResolver,
			NewClient: func(pat string) inventory.FigmaNodesFetcher {
				return client.New(pat)
			},
			Repo: func(tenantID string) *projects.TenantRepo {
				// Pool-aware: writes hit the write pool, reads use
				// the read pool — same wiring adminAutoSyncDB uses
				// for the autosync executor.
				return projects.NewTenantRepoFromPool(dbConn, tenantID)
			},
			Log: log.With("component", "node_metadata_extractor"),
		},
	})
	if err != nil {
		log.Error("figma_inventory poller init", "err", err)
		os.Exit(1)
	}

	// Now that graphRebuildPool exists, build projectsServer with T3's
	// post-commit enqueue dependency wired in. NewServer captures the
	// ServerDeps by value, so subsequent changes to graphRebuildPool's
	// fields (TenantIDs etc.) still take effect because we're handing it
	// the same pointer.
	projectsServer = projects.NewServer(projects.ServerDeps{
		DB:               dbConn,
		Broker:           broker,
		Tickets:          tickets,
		RateLimiter:      rateLimiter,
		Idempotency:      idempotencyCache,
		AuditLogger:      projectsAuditLogger,
		AuditEnqueuer:    auditEnqueuer,
		DataDir:          dataDir,
		PipelineFactory:  pipelineFactory,
		FigmaPATResolver: figmaPATResolver,
		AssetSigner:      assetSigner,
		// U1 (plan 2026-05-17-004) — frame-PNG proxy plumbing. Reuses
		// AssetExporter's URL fetcher (which already encapsulates the
		// per-tenant PAT resolution via ctx-stashed tenant id) and a
		// dedicated HTTPAssetByteFetcher with a tighter timeout — frame
		// thumbnails are 50-200 KB so a 30s timeout is generous.
		FigmaImageURLs: assetExporter.URLs,
		FigmaImageBytes: &projects.HTTPAssetByteFetcher{
			Client: &http.Client{Timeout: 30 * time.Second},
		},
		AssetExporter:     assetExporter,
		ImageFillResolver: imageFillResolver,
		PreviewPyramid:    previewPyramid,
		GraphRebuildPool:  graphRebuildPool,
		InventoryPoller:   inventoryPoller,
		ShutdownCtx:       workerCtx, // #3 audit fix — RunExport pipeline goroutines
		Log:              log,
	})

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
	// Start the FIGMA DB inventory poller. Lives off workerCtx so it stops
	// on the same SIGTERM as the audit + graph workers. First crawl runs
	// 30s after boot (avoids hammering Figma while the server is still
	// finishing other init); subsequent cycles run every 5 min.
	//
	// FIGMA_INVENTORY_DISABLED=1 short-circuits startup. Useful during
	// bulk re-extraction runs where the autosync executor wants the entire
	// per-PAT tier-1 budget for its own Figma calls, and any poller
	// activity is just contention. Also handy for offline development.
	if os.Getenv("FIGMA_INVENTORY_DISABLED") == "1" {
		log.Warn("FIGMA_INVENTORY_DISABLED=1 — inventory poller will not start")
	} else {
		inventoryPoller.Start(workerCtx)
	}

	// OAuth tokens reaper — /ce-code-review #20. Periodically purges
	// expired authorization_codes (60s TTL — biggest growth vector),
	// long-revoked rows (30-day forensic retention), and unrevoked
	// expired refresh tokens. Lives off workerCtx so SIGTERM cancels
	// the loop cleanly. Multi-replica safe: WHERE clauses are
	// idempotent and the DB serializes the DELETEs.
	auth.StartOAuthTokenReaper(workerCtx, dbConn.DB, log)

	// Autosync auto-retry ticker. Re-runs the Planner+Executor across
	// every in-window allowlist file at a regular cadence. The planner
	// now treats project_versions.status='failed' as a retry trigger
	// (SkipRetryFailedPipeline), so this loop self-heals async-pipeline
	// failures (Figma 5xx, PNG render timeouts) without operator action.
	// Interval is env-configurable; default 15 min keeps it well below
	// Figma's per-PAT tier-1 budget. Set FIGMA_AUTOSYNC_INTERVAL=0 to
	// disable (useful for tests + when running cmd/figma-autosync-* CLIs
	// against a server-managed DB).
	if interval := autosyncIntervalFromEnv(); interval > 0 {
		startAutosyncRetryLoop(workerCtx, log, projectsServer, dbConn, interval, inventoryPoller.Ready())
	} else {
		log.Info("autosync retry loop disabled (FIGMA_AUTOSYNC_INTERVAL=0)")
	}

	srv := &server{
		cfg:             cfg,
		db:              dbConn,
		jwt:             cfg.JWTKey,
		enc:             cfg.EncryptionKey,
		orch:            orch,
		log:             log,
		projectsServer:  projectsServer,
		broker:          broker,
		prerenderStatus: prerenderStatus,
	}

	mux := http.NewServeMux()
	srv.routes(mux)

	addr := ":" + cfg.Port
	handler := srv.cors(srv.requestLog(mux))

	// Optional TLS listener for HTTP/2. When DS_TLS_CERT + DS_TLS_KEY are set,
	// run a second listener on DS_TLS_ADDR (default :8443) with TLS — Go's
	// stdlib auto-enables HTTP/2 via ALPN when serving over TLS, so browsers
	// multiplex hundreds of asset GETs over one connection instead of queueing
	// on the HTTP/1.1 6-conn-per-origin cap.
	//
	// Cleartext HTTP/1.1 stays bound on :Port for non-browser clients
	// (curl scripts, sheets-sync internal calls, server-to-server) until
	// every caller is migrated. Both listeners share the same handler so
	// auth / CORS / routes / SSE behaviour are identical.
	// F6 — *http.Server with explicit Shutdown so SIGTERM drains in-flight
	// HTTP requests gracefully instead of getting SIGKILL'd 30s later.
	// Both listeners (TLS + cleartext) share the same handler but get
	// their own server struct so Shutdown can flip them independently.
	// #7 audit fix: explicit timeouts. WriteTimeout is generous (5 min)
	// to accommodate /v1/admin/figma-autosync/execute and similar
	// long-running admin handlers; ReadHeaderTimeout protects against
	// slowloris-style clients. IdleTimeout keeps keep-alive connections
	// from leaking. ReadTimeout deliberately omitted because some POST
	// handlers stream large bodies (Figma trees) and capping the *total*
	// read time would kill legitimate uploads on slow links.
	cleartextSrv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      5 * time.Minute,
		IdleTimeout:       120 * time.Second,
	}
	var tlsSrv *http.Server
	if cfg.TLSCertPath != "" && cfg.TLSKeyPath != "" {
		tlsAddr := cfg.TLSAddr
		if tlsAddr == "" {
			tlsAddr = ":8443"
		}
		tlsSrv = &http.Server{
			Addr:              tlsAddr,
			Handler:           handler,
			ReadHeaderTimeout: 10 * time.Second,
			WriteTimeout:      5 * time.Minute,
			IdleTimeout:       120 * time.Second,
		}
		log.Info("ds-service listening (TLS, HTTP/2)",
			"addr", tlsAddr,
			"cert", cfg.TLSCertPath,
		)
		go func() {
			if err := tlsSrv.ListenAndServeTLS(cfg.TLSCertPath, cfg.TLSKeyPath); err != nil && err != http.ErrServerClosed {
				log.Error("tls server", "err", err)
				os.Exit(1)
			}
		}()
	}

	// Shutdown goroutine. On SIGTERM/SIGINT it asks both servers to drain
	// for 30s (matches Fly's default graceful-shutdown window). Any
	// in-flight goroutines spawned with workerCtx have already started
	// observing cancellation via the F6 derivation above.
	go func() {
		<-shutdownCtx.Done()
		log.Info("shutdown signal received, draining HTTP server (30s grace)")
		drainCtx, drainCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer drainCancel()
		if tlsSrv != nil {
			_ = tlsSrv.Shutdown(drainCtx)
		}
		_ = cleartextSrv.Shutdown(drainCtx)
	}()

	log.Info("ds-service listening",
		"addr", addr,
		"repo_dir", cfg.RepoDir,
		"sync_git_push", cfg.SyncGitPush,
	)
	if err := cleartextSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Error("server", "err", err)
		os.Exit(1)
	}
	log.Info("ds-service stopped cleanly")
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
		// Local-dev auth bypass — see config struct comment.
		DevAuthBypass:       os.Getenv("DEV_AUTH_BYPASS") == "1",
		DevAuthBypassTenant: getenv("DEV_AUTH_BYPASS_TENANT", "e090530f-2698-489d-934a-c821cb925c8a"),
		DevAuthBypassEmail:  getenv("DEV_AUTH_BYPASS_EMAIL", "dev@local"),
		DevAuthBypassAdmin:  os.Getenv("DEV_AUTH_BYPASS_ADMIN") == "1",

		// HTTP/2 TLS — see config struct comment for the why. Both DS_TLS_CERT
		// and DS_TLS_KEY must be set for the TLS listener to bind; either
		// missing leaves the server in cleartext-only mode.
		TLSCertPath: os.Getenv("DS_TLS_CERT"),
		TLSKeyPath:  os.Getenv("DS_TLS_KEY"),
		TLSAddr:     getenv("DS_TLS_ADDR", ":8443"),
	}
	if c.DevAuthBypass {
		// #9 audit fix: refuse to start when DEV_AUTH_BYPASS is set in
		// an environment that smells like production. Combined with the
		// SEC-2/3 admin-gate fixes this still leaves an attacker with
		// no entry, but defense-in-depth: production must never honor
		// the bypass without an explicit operator override.
		looksLikeProd := os.Getenv("FLY_APP_NAME") != "" ||
			os.Getenv("GO_ENV") == "production" ||
			os.Getenv("NODE_ENV") == "production" ||
			os.Getenv("DS_ENV") == "production"
		if looksLikeProd && os.Getenv("ALLOW_DEV_AUTH_BYPASS_IN_PROD") != "1" {
			return nil, fmt.Errorf("DEV_AUTH_BYPASS=1 refused in production environment "+
				"(detected via FLY_APP_NAME=%q, GO_ENV=%q, NODE_ENV=%q, DS_ENV=%q); "+
				"unset DEV_AUTH_BYPASS, or set ALLOW_DEV_AUTH_BYPASS_IN_PROD=1 to override",
				os.Getenv("FLY_APP_NAME"), os.Getenv("GO_ENV"),
				os.Getenv("NODE_ENV"), os.Getenv("DS_ENV"))
		}
		log.Warn("DEV_AUTH_BYPASS=1 — JWT verification SKIPPED for every request",
			"tenant", c.DevAuthBypassTenant, "email", c.DevAuthBypassEmail,
			"admin_escalation", c.DevAuthBypassAdmin,
			"warning", "MUST NOT be set in production. Unset on Fly to disable.")
		if c.DevAuthBypassAdmin {
			log.Warn("DEV_AUTH_BYPASS_ADMIN=1 — synthetic claims escalated to Role=super_admin / IsAdmin=true",
				"warning", "Local dev only; production startup refuses both vars.")
		}
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
	orch            *sync.Orchestrator
	log             *slog.Logger
	projectsServer  *projects.Server
	broker          projects.SSEPublisher // shared SSE publisher; used by Phase 2 fan-out + audit-job runs
	prerenderStatus *projects.PrerenderStatusBuffer // U8 — operator observability for Stage 9
}

func (s *server) routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /__health", s.handleHealth)
	mux.HandleFunc("POST /v1/auth/login", s.handleLogin)

	// OAuth 2.0 authorization-server metadata (RFC 8414). Claude.ai and
	// other MCP clients hit this to auto-discover the authorize / token
	// endpoints; without it they fall through to manual config or fail
	// "could not connect". Returned content is static — we don't expose
	// DCR (registration_endpoint), and grant_types reflect what
	// /v1/oauth/token actually supports.
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", s.handleOAuthDiscovery)
	mux.HandleFunc("GET /.well-known/oauth-protected-resource", s.handleOAuthProtectedResource)
	mux.HandleFunc("POST /v1/admin/bootstrap", s.handleBootstrap)
	mux.HandleFunc("POST /v1/admin/figma-token", s.requireSuperAdmin(s.handleFigmaTokenUpload))
	mux.HandleFunc("GET /v1/admin/prerender/status", s.requireSuperAdmin(projects.HandlePrerenderStatus(s.prerenderStatus)))
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
	// T4 — retry a failed version's render pipeline. Re-uses the original
	// export's audit-log frame snapshot; no Figma re-walk required.
	mux.HandleFunc("POST /v1/projects/{slug}/versions/{version_id}/retry",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleVersionRetry)))
	// figma_render_blocklist (2026-05-12) — list + manual-clear admin endpoints.
	// Used by ops to see which (file_id, node_id) frames are currently
	// suppressed by the persistent-failure skip list and to manually
	// clear an entry when the upstream issue is known-fixed.
	// #8 audit fix: requireSuperAdmin (not requireAuth) — handlers also
	// re-check via requireAdminTenant, defense-in-depth.
	mux.HandleFunc("GET /v1/admin/figma-render-blocklist",
		s.requireSuperAdmin(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleFigmaBlocklistList)))
	mux.HandleFunc("DELETE /v1/admin/figma-render-blocklist/{file_id}/{node_id}",
		s.requireSuperAdmin(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleFigmaBlocklistClear)))
	// FIGMA DB inventory admin (2026-05-13, migration 0025). Admin-managed
	// team seed list + read-only inventory tree + manual sync trigger +
	// per-cycle run log. The poller (Started above) does the actual crawling.
	mux.HandleFunc("GET /v1/admin/figma-inventory/teams",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleFigmaInventoryListTeams)))
	mux.HandleFunc("POST /v1/admin/figma-inventory/teams",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleFigmaInventoryAddTeam)))
	mux.HandleFunc("DELETE /v1/admin/figma-inventory/teams/{team_id}",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleFigmaInventoryRemoveTeam)))
	mux.HandleFunc("GET /v1/admin/figma-inventory/tree",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleFigmaInventoryTree)))
	mux.HandleFunc("POST /v1/admin/figma-inventory/sync",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleFigmaInventorySync)))
	mux.HandleFunc("GET /v1/admin/figma-inventory/runs",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleFigmaInventoryRuns)))
	// Phase 2B U5 — Promote-to-project. Creates (or returns) a DS-internal
	// `projects` row linked on (tenant_id, file_id=file_key). Idempotent.
	mux.HandleFunc("POST /v1/admin/figma-inventory/files/{file_key}/promote",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleFigmaInventoryPromote)))
	// Phase 2C admin routes for the deep node-tree browser + cross-file
	// component usage analytics were deleted by plan 002 U6. The
	// underlying figma_node table is dropped by migration 0031.
	// organism-pattern-detection (2026-05-13) — Part C dashboard endpoints
	// powering /atlas/admin/organisms.
	mux.HandleFunc("GET /v1/admin/organisms/adoption",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleOrganismAdoption)))
	mux.HandleFunc("GET /v1/admin/organisms/promotion-candidates",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleOrganismPromotionCandidates)))
	mux.HandleFunc("GET /v1/admin/organisms/{slug}/matches",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleOrganismMatchesBySlug)))
	// Part B U7 — plugin "Check selection against DS" verdict lookup.
	// Read-through cache only; no recomputation at request time.
	mux.HandleFunc("POST /v1/audit/organism-match",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleOrganismVerdictLookup)))
	// Part B U9 — designer "Mark as intentional fork" assertion.
	mux.HandleFunc("POST /v1/audit/organism-match/fork",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleOrganismForkMark)))
	// U14b — reviewer sets a name on a promotion candidate.
	mux.HandleFunc("PATCH /v1/admin/organisms/promotion-candidates/{hash}",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandlePromotionCandidatePatch)))
	// Plugin "Replace with INSTANCE" deeplink — resolves a slug to the
	// Figma file + node id where the published organism lives, so the
	// plugin can open the source file and the designer can drag the
	// real INSTANCE manually. Library-key + automated swap is a
	// separate follow-up.
	mux.HandleFunc("GET /v1/admin/organisms/{slug}/deeplink",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleOrganismDeeplink)))
	// Autosync bridge — POST /v1/admin/figma-autosync/execute. Drives the
	// Planner + Executor in-process: builds a FilePlan for every in-window
	// mapped file (or one -file_key=) and runs each section's full_export /
	// cheap_update / skip action. Synchronous from the HTTP caller's
	// perspective; the pipeline goroutines spawned by RunExport keep
	// running after the response returns.
	// SEC-1 fix: requireSuperAdmin (not requireAuth) — the handler also
	// re-checks via RequireAdminTenant, defense-in-depth.
	mux.HandleFunc("POST /v1/admin/figma-autosync/execute",
		s.requireSuperAdmin(projects.AdaptAuthMiddleware(claimsReader, func(w http.ResponseWriter, r *http.Request) {
			handleAutosyncExecute(w, r, s.projectsServer, s.db, s.log)
		})))
	// F4 follow-up — manual escape hatch from auto-quarantine. Operators
	// hit this after fixing whatever caused 5 consecutive failures (file
	// renamed, frame removed, designer recreated a section, etc.). SEC-2
	// fix: requireSuperAdmin (handler also re-checks admin).
	mux.HandleFunc("DELETE /v1/admin/figma-autosync/state/{file_key}/{page_id}/{section_id}/quarantine",
		s.requireSuperAdmin(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleFigmaAutosyncClearQuarantine)))
	// #12 audit fix — paginated state inspection so operators can find
	// quarantined sections (and any other state) without raw SQL.
	mux.HandleFunc("GET /v1/admin/figma-autosync/state",
		s.requireSuperAdmin(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleFigmaAutosyncListState)))
	// #25 audit fix — figma_project_mapping upsert via HTTP so the
	// runbook's "raw SQL" instruction can be retired.
	mux.HandleFunc("POST /v1/admin/figma-project-mapping",
		s.requireSuperAdmin(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleFigmaProjectMappingUpsert)))
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

	// Plan 2026-05-05-002 U2 — text overrides (Zeplin-grade leaf canvas).
	// Per-screen + per-leaf list paths, plus PUT/DELETE/bulk-upsert.
	// Bodies capped at 16KB per override. Optimistic concurrency mirrors
	// HandlePutDRD's contract (409 on stale expected_revision).
	mux.HandleFunc("GET /v1/projects/{slug}/screens/{id}/text-overrides",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleListOverrides)))
	mux.HandleFunc("GET /v1/projects/{slug}/leaves/{leaf_id}/text-overrides",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleListOverrides)))
	mux.HandleFunc("PUT /v1/projects/{slug}/screens/{id}/text-overrides/{figma_node_id}",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandlePutOverride)))
	mux.HandleFunc("DELETE /v1/projects/{slug}/screens/{id}/text-overrides/{figma_node_id}",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleDeleteOverride)))
	mux.HandleFunc("POST /v1/projects/{slug}/text-overrides/bulk",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleBulkUpsertOverrides)))

	// U12 — CSV bulk export/import for translators / PMs. Export streams
	// every TEXT node across the leaf as CSV; import accepts a multipart
	// upload (5 MB cap), detects conflicts via last_edited_at vs DB
	// updated_at, and chunks dirty rows into 100-row bulk-upsert calls.
	mux.HandleFunc("GET /v1/projects/{slug}/leaves/{leaf_id}/text-overrides/csv",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleCSVExport)))
	mux.HandleFunc("POST /v1/projects/{slug}/leaves/{leaf_id}/text-overrides/csv",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleCSVImport)))

	// Plan 2026-05-05-002 U5 — asset download endpoints (single + bulk → zip).
	// The mint endpoints (POST) require JWT; the GET download endpoints are
	// authenticated solely via the signed asset token in `?at=` so image
	// loaders / file-save dialogs never see the JWT. Tokens bind
	// (tenant, file_id, node_id, format, scale) for single assets and
	// (tenant, bulk_id) for bulk zips.
	// All literal-segment asset routes are registered BEFORE the generic
	// `/assets/{node_id}` GET catch-all so Go 1.22 ServeMux's most-specific
	// pattern rule binds the literal segments ("warm", "raw", "bulk", and
	// "export-url" / "bulk-export") to their dedicated handlers. With the
	// catch-all preceding them, certain POST literal routes silently 404'd
	// despite being registered (observed for /warm and /bulk-export).
	mux.HandleFunc("POST /v1/projects/{slug}/assets/export-url",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleMintAssetExportToken())))
	mux.HandleFunc("POST /v1/projects/{slug}/assets/bulk-export",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleBulkAssetExport())))
	mux.HandleFunc("GET /v1/projects/{slug}/leaves/{leaf_id}/image-refs",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleListImageRefs)))
	// Per-leaf SSE asset hydration. Two-step: ticket POST (JWT-authed) issues a
	// 60s single-use ticket bound to assets:<tenant>:<leafID>; GET stream
	// redeems the ticket (no JWT — EventSource cannot send Authorization
	// headers) and emits asset-ready events as each cluster pyramid lands.
	// Designed to replace the per-cluster /assets/export-url + cache-miss
	// fanout that produced minutes of dashed placeholders on cold-cache leaf
	// open. See internal/projects/asset_stream.go header for the why.
	mux.HandleFunc("POST /v1/projects/{slug}/leaves/{leaf_id}/asset-stream/ticket",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleAssetStreamTicket)))
	mux.HandleFunc("GET /v1/projects/{slug}/leaves/{leaf_id}/asset-stream",
		s.projectsServer.HandleAssetStream)
	mux.HandleFunc("GET /v1/projects/{slug}/assets/raw/{imageRef}",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleServeRawAsset)))
	mux.HandleFunc("GET /v1/projects/{slug}/assets/bulk/{token}",
		s.projectsServer.HandleBulkDownload())
	// Generic catch-all GET — matches any other /assets/<node_id>. Must
	// come last so the literal segments above bind first.
	mux.HandleFunc("GET /v1/projects/{slug}/assets/{node_id}",
		s.projectsServer.HandleAssetDownload())

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
	// Atlas brain-graph: per-project rolled-up counts (screens, flows,
	// active violations) for the Canvas2D brain consumer at /atlas. Powers
	// the FLOWS list in the reference UI; SYNAPSES come from the existing
	// /v1/projects/graph aggregate (graph_index edges).
	mux.HandleFunc("GET /v1/projects/atlas/brain-nodes",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleAtlasBrainNodes)))
	mux.HandleFunc("GET /v1/projects/atlas/brain-products",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleAtlasBrainProducts)))
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

	// Personas — non-admin list endpoint, used by the Figma plugin's
	// persona dropdown and the atlas inspector chips. Default ?status=
	// is approved (the common case); 'pending' / 'all' available without
	// the super-admin gate.
	mux.HandleFunc("GET /v1/personas",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleListPersonas)))

	// Telemetry drop-zone — anonymous-allowed by design. Plugin + web
	// post errors / lifecycle events here; we log them to stdout so
	// `fly logs -a indmoney-ds-service | grep telemetry` produces a live
	// stream. Used to debug cross-machine sessions.
	mux.HandleFunc("POST /v1/telemetry/event", s.projectsServer.HandleTelemetryEvent)

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

	// Plan 2026-05-18-001 U1 — Atlas leaf-overlay sub_flow lookup.
	// Returns the sub_flow bound to a flow via flows.section_id →
	// sub_flow.figma_section_id, or 404 if no binding exists. Powers the
	// PM-authoring tabs in Atlas's right rail (PRD/Activity/Comments).
	mux.HandleFunc("GET /v1/projects/{slug}/flows/{flow_id}/sub-flow",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleSubFlowForLeaf)))

	// Phase 5.2 P4 — Figma frame metadata proxy. Auth-gated, tenant-
	// scoped. Powers the DRD's figmaLink custom block thumbnails.
	mux.HandleFunc("GET /v1/figma/frame-metadata",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleFigmaFrameMetadata)))

	// U1 (plan 2026-05-17-004) — Figma frame-PNG proxy. The mint route is
	// JWT-only; the bytes route accepts ?at= via the middleware's asset-
	// token bypass (path suffix /frame-png is in pathAllowsTokenQueryParam
	// below — keep that list and this registration in sync).
	mux.HandleFunc("POST /v1/figma/frame-png-token",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleMintFigmaFramePNGToken)))
	mux.HandleFunc("GET /v1/figma/frame-png",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleFigmaFramePNG)))

	// Phase 5 U1 — DRD collab. Public ticket endpoint (auth-gated)
	// + the loopback-only auth/load/snapshot bridge endpoints the
	// Hocuspocus sidecar calls. The sidecar is expected to run on
	// the internal network and to send DS_HOCUSPOCUS_SHARED_SECRET in
	// the X-DS-Hocuspocus-Secret header for the /internal/* routes.
	mux.HandleFunc("POST /v1/projects/{slug}/flows/{flow_id}/drd/ticket",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleDRDTicket)))
	// U3 follow-up: slug-keyed parallel endpoint for the PRD viewer's
	// DRDPane. Resolves sub_flow → flow_id then mints the same ticket
	// shape the flow_id-keyed endpoint above returns.
	mux.HandleFunc("POST /v1/sub-flows/{sub_product_slug}/{sub_flow_slug}/drd/ticket",
		s.requireAuth(projects.AdaptAuthMiddleware(claimsReader, s.projectsServer.HandleSubFlowDRDTicket)))
	mux.HandleFunc("POST /internal/drd/auth",
		s.projectsServer.HandleDRDInternalAuthGated())
	mux.HandleFunc("GET /internal/drd/load",
		s.projectsServer.HandleDRDInternalLoadGated())
	mux.HandleFunc("POST /internal/drd/snapshot",
		s.projectsServer.HandleDRDInternalSnapshotGated())

	// ─── MCP (plan 002 — PM workflow tool surface) ────────────────────────
	// GET  /v1/mcp/tools           — cold catalog (3 visible meta-verbs)
	// POST /v1/mcp/invoke/{name}   — invoke any registered tool by name
	//
	// Consumed by the ind-suite stdio MCP bridge (U7) which Claude Code
	// loads from the indmoney-ds plugin. Tenant scoping comes from the
	// JWT claims via requireAuth → claimsFrom; multi-tenant claims are
	// rejected at the handler level (Phase 1 doesn't support cross-tenant
	// invocation; Phase 2 U11 will add file-scoped capability tokens).
	//
	// s.broker is stored interface-typed (projects.SSEPublisher) on the
	// server struct but is concretely a *sse.MemoryBroker — handler
	// signatures want the concrete type so the broker can be inspected
	// for in-memory fan-out diagnostics. Assert is safe at process boot.
	mcpBroker, _ := s.broker.(*sse.MemoryBroker)
	mcpDeps := mcp.HandlerDeps{
		DB:           s.db,
		Broker:       mcpBroker,
		ClaimsReader: claimsReader,
		Registry:     mcp.NewDefaultRegistry(),
		Log:          s.log,
	}
	mcp.RegisterRoutes(mux, mcpDeps, s.requireAuth)

	// Plan 002 U1 — sibling MCP-spec Streamable HTTP transport. The REST
	// surface above keeps Atlas + local stdio bridge working unchanged;
	// `POST /mcp` is the JSON-RPC entry point Claude Custom Connectors use.
	mcp.RegisterMCPRoutes(mux, mcpDeps, s.requireAuth)

	// Plan 002 U8 — OAuth 2.1 + PKCE endpoints for the Claude.ai Custom
	// Connector flow. Authorize requires an existing /v1/auth/login
	// session JWT (carried by Authorization header); token + revoke are
	// unauthenticated per RFC 6749 / RFC 7009 — the code / refresh token
	// IS the credential.
	//
	// OAuth-minted access tokens are JWTs signed by the same Ed25519
	// SigningKey as the /v1/auth/login flow, so the existing
	// requireAuth middleware accepts them transparently. The MCP
	// transport needs no change.
	//
	// OAUTH_CLIENTS is a JSON env var enumerating registered clients +
	// their exact-match redirect_uri allowlists. Unset → DefaultClients()
	// (claude.ai with its standard callback). Malformed JSON fails-fast
	// at boot rather than silently dropping the allowlist. Dynamic
	// Client Registration is out of scope (plan KTD-5).
	clients, err := auth.ParseClients(os.Getenv("OAUTH_CLIENTS"))
	if err != nil {
		s.log.Error("OAUTH_CLIENTS parse failed", "err", err)
		os.Exit(2)
	}
	// Browser-friendly wrapper for /v1/oauth/authorize: when the user
	// hits authorize via a redirect from Claude.ai's Connector UI, they
	// don't have an Authorization header. If our cookie isn't there
	// either, the standard requireAuth returns JSON 401 — Claude.ai's
	// UI shows "could not connect" because it expected a 302 to a
	// login flow. Wrap requireAuth so unauth'd browser requests get
	// redirected to /oauth/login?next=<original-url> instead. The
	// login page sets the cookie and bounces back to authorize.
	requireAuthOrLoginRedirect := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			// Check for any of the auth signals requireAuth accepts.
			hasAuth := r.Header.Get("Authorization") != ""
			if !hasAuth {
				if c, err := r.Cookie("__Host-ds_session"); err == nil && c.Value != "" {
					hasAuth = true
				}
			}
			if !hasAuth {
				// No session → bounce to the inline login page. Encode
				// the full original URL (path + query) so login can
				// resume exactly where we left off.
				origURL := r.URL.RequestURI()
				http.Redirect(w, r, "/oauth/login?next="+url.QueryEscape(origURL), http.StatusFound)
				return
			}
			// Has auth → defer to the standard requireAuth so claims
			// land in the request context the way the handler expects.
			s.requireAuth(next)(w, r)
		}
	}
	auth.RegisterOAuthRoutes(mux, s.db.DB, s.jwt, auth.OAuthConfig{
		Clients: clients,
	}, requireAuthOrLoginRedirect, claimsFrom)

	// Inline login page for the OAuth browser flow. Serves an HTML form
	// on GET; on POST, runs the same handleLogin path then 302s to the
	// `next` URL (which is the original /v1/oauth/authorize that
	// triggered the redirect). Kept tiny + self-contained so we don't
	// need cross-origin coordination with the Atlas frontend.
	mux.HandleFunc("GET /oauth/login", s.handleOAuthLoginPage)
	mux.HandleFunc("POST /oauth/login", s.handleOAuthLoginSubmit)
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
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, If-Match, X-Trace-ID, X-Bootstrap-Token")
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

// pathAllowsTokenQueryParam returns true for binary-loader endpoints
// where browsers cannot attach an Authorization header (THREE textures,
// KTX2, raw canonical-tree bytes for the atlas viewer). Every other GET
// must use Authorization: Bearer or the short-lived ?at= asset token.
//
// #10 audit fix — narrows the ?token=<jwt> fallback so a 7-day JWT no
// longer leaks via Referer / CDN logs / browser history on arbitrary
// GET endpoints. Update this list when a new binary-loader route is
// added; non-binary GETs should never accept JWTs in the URL.
func pathAllowsTokenQueryParam(path string) bool {
	return strings.HasSuffix(path, "/png") ||
		strings.HasSuffix(path, "/ktx2") ||
		strings.HasSuffix(path, "/canonical-tree")
}

func claimsFrom(r *http.Request) *auth.Claims {
	c, _ := r.Context().Value(ctxClaims).(*auth.Claims)
	return c
}

func (s *server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Local-dev auth bypass — short-circuit with synthetic claims so
		// `npm run dev` works without minted JWTs. Production never sets
		// DEV_AUTH_BYPASS; the startup banner WARNs loudly when it's on.
		// The synthetic user is pinned to DevAuthBypassTenant so cross-
		// tenant data still segregates.
		if s.cfg.DevAuthBypass {
			// DEV_AUTH_BYPASS alone gives a regular user (Role=user,
			// IsAdmin=false). Devs who need /v1/admin/* access (e.g. the
			// Figma inventory + organism dashboards) opt in separately via
			// DEV_AUTH_BYPASS_ADMIN=1. Splitting the two keeps the
			// security-relevant escalation explicit at config time rather
			// than silently bundled with the bypass. The startup banner
			// (looksLikeProd guard) prevents either var from firing on Fly.
			devClaims := &auth.Claims{
				Sub:     "dev-user-local",
				Email:   s.cfg.DevAuthBypassEmail,
				Role:    "user",
				Tenants: []string{s.cfg.DevAuthBypassTenant},
				IsAdmin: false,
			}
			if s.cfg.DevAuthBypassAdmin {
				devClaims.Role = "super_admin"
				devClaims.IsAdmin = true
			}
			devClaims.ID = "dev-bypass"
			ctx := context.WithValue(r.Context(), ctxClaims, devClaims)
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
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
		//
		// #10 audit fix: previously this fallback applied to EVERY GET,
		// which leaked full 7-day JWTs to CDN/proxy/Referer logs for any
		// API call that pasted ?token= in. Restrict to the binary-loader
		// path suffixes that genuinely cannot use the Authorization
		// header. All other GETs must use Authorization or ?at= asset
		// tokens (short-lived scoped tokens).
		if token == "" && r.Method == http.MethodGet && pathAllowsTokenQueryParam(r.URL.Path) {
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
		// Cookie fallback for browser-driven flows that can't set custom
		// headers — specifically /v1/oauth/authorize, which Claude.ai
		// redirects the browser to during the Connector setup. The
		// cookie is minted by /v1/auth/login with HttpOnly + Secure +
		// SameSite=Lax + __Host- prefix. Same JWT, same signer — just a
		// different transport.
		if token == "" {
			if c, err := r.Cookie("__Host-ds_session"); err == nil && c.Value != "" {
				token = c.Value
			}
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
		// Plan 2026-05-03-001 / T1 — revocation list. Signature is valid; check
		// whether ops has explicitly disabled this jti (e.g. leaked token,
		// former designer). 60 s in-memory cache so the happy path doesn't
		// pay an extra SQL round-trip per request.
		if claims.ID != "" && s.db.IsJTIRevoked(r.Context(), claims.ID) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "token_revoked"})
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
	// Set the JWT as a session cookie so browser-driven flows
	// (specifically /v1/oauth/authorize, which is hit as a redirect from
	// Claude.ai's Connector UI) can authenticate without setting an
	// Authorization header. Atlas keeps using the access_token in
	// localStorage + the Authorization header for its API calls — both
	// auth paths verify the same JWT against the same signer.
	//
	// `__Host-` prefix enforces Secure + Path=/ + no Domain attribute,
	// the strongest cookie-isolation policy modern browsers offer.
	// HttpOnly blocks JS access, so this cookie can't be exfiltrated by
	// XSS the way localStorage can. SameSite=Lax lets it ride
	// top-level GET navigations (the OAuth redirect) but not silent
	// cross-site POSTs.
	http.SetCookie(w, &http.Cookie{
		Name:     "__Host-ds_session",
		Value:    tok,
		Path:     "/",
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int((7 * 24 * time.Hour).Seconds()),
	})
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

// handleOAuthLoginPage serves a minimal HTML email/password form. The
// form action is `/oauth/login` (POST); the `next` query param carries
// the destination to bounce to after the cookie is set. This page
// fires when an unauthenticated browser hits /v1/oauth/authorize
// (typically via Claude.ai's Connector redirect).
//
// Self-contained — no JS, no external assets, no Atlas dependency. The
// styling is inline so the page survives even if the Atlas frontend is
// down or unreachable from the user's network.
func (s *server) handleOAuthLoginPage(w http.ResponseWriter, r *http.Request) {
	next := r.URL.Query().Get("next")
	// Defense: only accept same-origin / relative `next` URLs. Anything
	// starting with "http" or "//" would be an open-redirect primitive.
	if next == "" || strings.HasPrefix(next, "http") || strings.HasPrefix(next, "//") {
		next = "/v1/oauth/authorize"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	// html/template would be overkill for a static form; the only
	// dynamic value is `next`, which we run through html-escape to
	// block injection via a crafted authorize URL.
	escapedNext := htmlEscapeForAttr(next)
	_, _ = w.Write([]byte(`<!doctype html><html><head><meta charset="utf-8">
<title>Sign in — INDmoney DS</title>
<meta name="viewport" content="width=device-width,initial-scale=1">
<style>
body{font:14px/1.5 -apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;background:#f6f7f9;color:#1f2937;margin:0;display:flex;min-height:100vh;align-items:center;justify-content:center}
.card{background:#fff;border-radius:14px;box-shadow:0 12px 32px rgba(0,0,0,.08);padding:28px 32px;width:340px}
h1{margin:0 0 4px;font-size:20px}
.sub{color:#6b7280;font-size:13px;margin-bottom:20px}
label{display:block;font-weight:600;font-size:12px;margin:14px 0 6px;color:#374151}
input{width:100%;box-sizing:border-box;padding:10px 12px;border:1px solid #d1d5db;border-radius:8px;font-size:14px}
input:focus{outline:none;border-color:#6366f1;box-shadow:0 0 0 3px rgba(99,102,241,.15)}
button{width:100%;margin-top:18px;padding:10px 12px;background:#111827;color:#fff;border:0;border-radius:8px;font-weight:600;font-size:14px;cursor:pointer}
button:hover{background:#0b1220}
.foot{margin-top:14px;font-size:12px;color:#6b7280;text-align:center}
.err{margin-top:14px;color:#b91c1c;font-size:13px;background:#fee2e2;border-radius:8px;padding:8px 12px;display:none}
.err.show{display:block}
</style></head><body>
<form class="card" method="post" action="/oauth/login">
  <h1>Sign in</h1>
  <div class="sub">Continue to authorize the connector.</div>
  <input type="hidden" name="next" value="` + escapedNext + `">
  <label for="e">Email</label>
  <input id="e" name="email" type="email" autocomplete="email" required autofocus>
  <label for="p">Password</label>
  <input id="p" name="password" type="password" autocomplete="current-password" required>
  <button type="submit">Sign in &amp; continue</button>
  <div class="foot">indmoney-ds-service.fly.dev</div>
</form>
</body></html>`))
}

// handleOAuthLoginSubmit accepts the form POST from the inline login
// page. Runs the same credentials check as POST /v1/auth/login (DRY via
// a shared verifyUser helper), sets the session cookie on success, and
// 302s to the `next` URL. On failure, re-renders the form with an
// error banner (kept minimal — the dev workflow is "log in correctly
// or try again").
func (s *server) handleOAuthLoginSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	email := r.PostFormValue("email")
	password := r.PostFormValue("password")
	next := r.PostFormValue("next")
	if next == "" || strings.HasPrefix(next, "http") || strings.HasPrefix(next, "//") {
		next = "/v1/oauth/authorize"
	}

	// Reuse handleLogin's verification path by dispatching a JSON
	// request to it internally — keeps credential logic single-sourced.
	loginBody, _ := json.Marshal(map[string]string{"email": email, "password": password})
	loginReq := httptest.NewRequest(http.MethodPost, "/v1/auth/login", bytes.NewReader(loginBody))
	loginReq.Header.Set("Content-Type", "application/json")
	loginResp := httptest.NewRecorder()
	s.handleLogin(loginResp, loginReq)

	if loginResp.Code != http.StatusOK {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`<!doctype html><body style="font:14px sans-serif;padding:40px;background:#f6f7f9">
<div style="max-width:340px;margin:0 auto;background:#fff;padding:24px;border-radius:12px;box-shadow:0 8px 24px rgba(0,0,0,.06)">
<h2 style="margin:0 0 8px">Sign in failed</h2>
<p style="color:#6b7280;margin:0 0 16px">Check your email and password, then try again.</p>
<a href="/oauth/login?next=` + htmlEscapeForAttr(next) + `" style="color:#4f46e5">&larr; Back to sign in</a>
</div></body>`))
		return
	}
	// Replay the Set-Cookie header from handleLogin onto our response so
	// the browser actually persists the session.
	for _, c := range loginResp.Result().Cookies() {
		http.SetCookie(w, c)
	}
	http.Redirect(w, r, next, http.StatusFound)
}

// htmlEscapeForAttr escapes a string for safe placement inside a
// double-quoted HTML attribute. Sufficient for our single use case
// (the `next` URL); does not handle textnode or script contexts.
func htmlEscapeForAttr(s string) string {
	r := strings.NewReplacer(
		`&`, `&amp;`,
		`"`, `&quot;`,
		`'`, `&#39;`,
		`<`, `&lt;`,
		`>`, `&gt;`,
	)
	return r.Replace(s)
}

// handleOAuthDiscovery returns the RFC 8414 OAuth 2.0
// Authorization-Server Metadata document. Lets Claude.ai's Custom
// Connector auto-discover the authorize/token URLs instead of
// requiring manual config. Static fields — we don't expose Dynamic
// Client Registration; grant_types_supported matches what
// /v1/oauth/token actually accepts.
func (s *server) handleOAuthDiscovery(w http.ResponseWriter, r *http.Request) {
	base := serverPublicBaseURL(r)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=300")
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                base,
		"authorization_endpoint":                base + "/v1/oauth/authorize",
		"token_endpoint":                        base + "/v1/oauth/token",
		"revocation_endpoint":                   base + "/v1/oauth/revoke",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"none"}, // public client (PKCE) per RFC 8252
		"scopes_supported":                      []string{}, // scopes captured but not enforced (yet)
	})
}

// handleOAuthProtectedResource returns the RFC 9728 Protected Resource
// Metadata for /mcp. Anthropic Claude Connectors fetch this off the
// resource URL to discover which authorization server to use.
func (s *server) handleOAuthProtectedResource(w http.ResponseWriter, r *http.Request) {
	base := serverPublicBaseURL(r)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=300")
	writeJSON(w, http.StatusOK, map[string]any{
		"resource":              base + "/mcp",
		"authorization_servers": []string{base},
		"bearer_methods_supported": []string{"header"},
	})
}

// serverPublicBaseURL derives the public origin (scheme + host) the
// client used to reach us. Honors X-Forwarded-Proto/Host (Fly's proxy
// sets these) so the discovery doc returns https://indmoney-ds-service.fly.dev/
// not http://internal-vm-name/.
func serverPublicBaseURL(r *http.Request) string {
	scheme := r.Header.Get("X-Forwarded-Proto")
	if scheme == "" {
		if r.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}
	return scheme + "://" + host
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
	// SEC-6 audit fix: constant-time compare to avoid leaking timing
	// information about prefix match length on the bootstrap token.
	got := r.Header.Get("X-Bootstrap-Token")
	if subtle.ConstantTimeCompare([]byte(got), []byte(s.cfg.BootstrapToken)) != 1 {
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

// figmaImageURLFetcherFunc adapts a closure to the
// projects.FigmaImageURLFetcher interface. Used by the U5 wiring above so
// the per-tenant PAT decrypt can happen inline without defining a named
// type.
type figmaImageURLFetcherFunc func(ctx context.Context, fileKey string, nodeIDs []string, format string, scale int) (map[string]string, error)

func (f figmaImageURLFetcherFunc) GetImages(ctx context.Context, fileKey string, nodeIDs []string, format string, scale int) (map[string]string, error) {
	return f(ctx, fileKey, nodeIDs, format, scale)
}

// figmaImageFillURLFetcherFunc adapts a closure to the
// projects.FigmaImageFillURLFetcher interface. Mirrors
// figmaImageURLFetcherFunc above, but for the /v1/files/<key>/images
// endpoint (imageRef → S3 URL map) instead of /v1/images (node renders).
type figmaImageFillURLFetcherFunc func(ctx context.Context, fileKey string) (map[string]string, error)

func (f figmaImageFillURLFetcherFunc) GetFileImageFills(ctx context.Context, fileKey string) (map[string]string, error) {
	return f(ctx, fileKey)
}

// adminAutoSyncDB adapts a *db.DB to inventory.AutoSyncDB. Returns a
// fresh TenantRepo per tenant, wired to both pools (plan
// 2026-05-16-001 U3). The planner's per-section LookupAutoSyncState
// runs on the read pool; the executor's UpsertAutoSyncState runs on
// the write pool. Used by handleAutosyncExecute + startAutosyncRetryLoop.
type adminAutoSyncDB struct{ pool *db.DB }

func (a adminAutoSyncDB) NewTenantRepo(tenantID string) *projects.TenantRepo {
	return projects.NewTenantRepoFromPool(a.pool, tenantID)
}

// autosyncLeaseTTL bounds how long one acquisition holds the autosync
// lease (#10 audit fix — see migration 0033). Sized to per-cycle
// budget plus comfortable slack so a crashed replica's lease auto-
// reclaims on the next attempt.
const autosyncLeaseTTL = 15 * time.Minute

// autosyncHolderID identifies this replica for figma_autosync_lease
// rows. Generated once at process start so a successful
// TryAcquireAutosyncLease + later ReleaseAutosyncLease are guaranteed
// to use the same id. Format: hostname:pid:nanoid (nanoid avoids
// collisions when the same hostname/pid combo cycles fast under
// orchestrators).
var autosyncHolderID = func() string {
	host, _ := os.Hostname()
	if host == "" {
		host = "unknown"
	}
	return fmt.Sprintf("%s:%d:%s", host, os.Getpid(), uuid.NewString()[:8])
}()

// tryAcquireAutosyncLease wraps projects.TryAcquireAutosyncLease with
// the boot-generated holder id + the standard TTL. Returns true iff
// the caller now holds the per-tenant lease.
func tryAcquireAutosyncLease(ctx context.Context, db *sql.DB, tenantID string) (bool, error) {
	return projects.TryAcquireAutosyncLease(ctx, db, tenantID, autosyncHolderID, autosyncLeaseTTL)
}

// releaseAutosyncLease wraps projects.ReleaseAutosyncLease. Logs but
// doesn't fail on error — the row will expire on its own TTL anyway.
func releaseAutosyncLease(ctx context.Context, db *sql.DB, log *slog.Logger, tenantID string) {
	if err := projects.ReleaseAutosyncLease(ctx, db, tenantID, autosyncHolderID); err != nil && log != nil {
		log.Warn("autosync: release lease failed", "tenant", tenantID, "err", err.Error())
	}
}

// handleAutosyncExecute runs the autosync Planner + Executor in-process.
// Body (optional): {"file_key": "..."} to scope to a single file.
// Default: every in-window mapped file for the tenant.
//
// Response:
//
//	{
//	  "tenant_id": "...",
//	  "files":     [{"file_key":"...", "file_name":"...",
//	                 "sections":N, "full_export":N, "cheap_update":N,
//	                 "skip_unchanged":N, "quarantined":N, "errors":[...]}],
//	  "totals":    {...}
//	}
func handleAutosyncExecute(w http.ResponseWriter, r *http.Request, ps *projects.Server, pool *db.DB, log *slog.Logger) {
	sqlDB := pool.Write() // lease + raw-SQL helpers use the write pool
	if r.Method != http.MethodPost {
		projects.WriteJSONErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST only")
		return
	}
	// SEC-1 fix: tenant comes from authenticated claims, never the URL.
	// Combined with the requireSuperAdmin gate at the route, this is
	// admin-only AND tenant-scoped to the caller — cross-tenant
	// invocations are no longer possible.
	tenantID, ok := ps.RequireAdminTenant(w, r)
	if !ok {
		return
	}
	type bodyT struct {
		FileKey string `json:"file_key"`
	}
	var body bodyT
	_ = json.NewDecoder(r.Body).Decode(&body) // empty body is fine

	// F11 — collapse concurrent runs (HTTP + ticker, or two HTTP calls
	// or two replicas). Per-tenant SQLite advisory lease (#10) covers
	// multi-replica deployments — TryAcquire returns false when a
	// healthy holder owns the lease.
	ok, err := tryAcquireAutosyncLease(r.Context(), sqlDB, tenantID)
	if err != nil {
		projects.WriteJSONErr(w, http.StatusInternalServerError, "lease_error", err.Error())
		return
	}
	if !ok {
		projects.WriteJSONErr(w, http.StatusConflict, "already_running", "autosync already running, retry shortly")
		return
	}
	defer releaseAutosyncLease(r.Context(), sqlDB, log, tenantID)

	planner := inventory.NewPlanner(adminAutoSyncDB{pool: pool}, inventory.PlannerConfig{Log: log})
	executor := inventory.NewExecutor(adminAutoSyncDB{pool: pool}, ps.RunExport)

	var plans []inventory.FilePlan
	if body.FileKey != "" {
		fp, err := planner.Plan(r.Context(), tenantID, body.FileKey)
		if err != nil {
			status, code := autosyncPlanErrorMapping(err)
			projects.WriteJSONErr(w, status, code, err.Error())
			return
		}
		plans = []inventory.FilePlan{fp}
	} else {
		ps2, err := planner.PlanTenant(r.Context(), tenantID)
		if err != nil {
			status, code := autosyncPlanErrorMapping(err)
			projects.WriteJSONErr(w, status, code, err.Error())
			return
		}
		plans = ps2
	}

	type fileSummary struct {
		FileKey       string   `json:"file_key"`
		FileName      string   `json:"file_name"`
		Sections      int      `json:"sections"`
		FullExport    int      `json:"full_export"`
		CheapUpdate   int      `json:"cheap_update"`
		SkipUnchanged int      `json:"skip_unchanged"`
		Quarantined   int      `json:"quarantined"`
		FileSkip      string   `json:"file_skip,omitempty"`
		Errors        []string `json:"errors,omitempty"`
	}
	out := struct {
		TenantID string        `json:"tenant_id"`
		Files    []fileSummary `json:"files"`
		Totals   struct {
			Files         int `json:"files"`
			FullExport    int `json:"full_export"`
			CheapUpdate   int `json:"cheap_update"`
			SkipUnchanged int `json:"skip_unchanged"`
			Quarantined   int `json:"quarantined"`
			Errors        int `json:"errors"`
		} `json:"totals"`
	}{TenantID: tenantID}

	for _, plan := range plans {
		fs := fileSummary{FileKey: plan.FileKey, FileName: plan.FileName}
		if plan.FileSkip != nil {
			fs.FileSkip = string(plan.FileSkip.Code)
			out.Files = append(out.Files, fs)
			continue
		}
		res, err := executor.Execute(r.Context(), plan)
		if err != nil {
			fs.Errors = []string{"execute: " + err.Error()}
			out.Files = append(out.Files, fs)
			out.Totals.Errors++
			continue
		}
		fs.Sections = res.Sections
		fs.FullExport = res.FullExported
		fs.CheapUpdate = res.CheapUpdated
		fs.SkipUnchanged = res.SkippedAlready
		fs.Quarantined = res.SkippedQuar
		fs.Errors = res.Errors
		out.Files = append(out.Files, fs)
		out.Totals.Files++
		out.Totals.FullExport += res.FullExported
		out.Totals.CheapUpdate += res.CheapUpdated
		out.Totals.SkipUnchanged += res.SkippedAlready
		out.Totals.Quarantined += res.SkippedQuar
		out.Totals.Errors += len(res.Errors)
	}
	projects.WriteJSON(w, http.StatusOK, out)
}

// autosyncPlanErrorMapping converts the planner's error into the
// (HTTP status, error code) tuple. Input-shape errors return 400 so the
// operator sees them as their own bug; everything else is a real
// incident (DB unreachable, txn aborted, etc.) and surfaces as 500.
// project_unmapped / mapping_disabled never bubble here — the planner
// returns them as FilePlan.FileSkip values inline.
func autosyncPlanErrorMapping(err error) (int, string) {
	if err == nil {
		return http.StatusOK, ""
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "tenant_id required"),
		strings.Contains(msg, "file_key required"):
		return http.StatusBadRequest, "bad_request"
	default:
		return http.StatusInternalServerError, "plan_error"
	}
}

// autosyncIntervalFromEnv parses FIGMA_AUTOSYNC_INTERVAL. Accepts a Go
// duration ("15m", "30s") or seconds-as-int. 0 disables the loop.
// Default: 15 min when the var is unset.
// autosyncMinInterval is the floor for FIGMA_AUTOSYNC_INTERVAL. Set
// below this and the loop would burn ~2500 SQLite reads per cycle (per
// PlanTenant's doc comment) plus outbound Figma calls — operator typo
// territory. Anything > 0 but < 1m gets clamped to 1m; 0 stays
// special-cased as "disabled".
const autosyncMinInterval = time.Minute

// autosyncIntervalFromEnv parses FIGMA_AUTOSYNC_INTERVAL. Accepts:
//   - ""              → 15m default
//   - "0"             → 0 (disabled)
//   - duration ("15m") → as-parsed, clamped to >= 1m
//   - int seconds ("900") → seconds, clamped to >= 1m
//   - garbage or negative → 15m default
func autosyncIntervalFromEnv() time.Duration {
	raw := strings.TrimSpace(os.Getenv("FIGMA_AUTOSYNC_INTERVAL"))
	if raw == "" {
		return 15 * time.Minute
	}
	if raw == "0" {
		return 0
	}
	var parsed time.Duration
	if d, err := time.ParseDuration(raw); err == nil && d > 0 {
		parsed = d
	} else if secs, err := strconv.Atoi(raw); err == nil && secs > 0 {
		parsed = time.Duration(secs) * time.Second
	} else {
		return 15 * time.Minute
	}
	if parsed < autosyncMinInterval {
		return autosyncMinInterval
	}
	return parsed
}

// startAutosyncRetryLoop kicks off a goroutine that calls the Planner +
// Executor for the bypass tenant every `interval`. First run fires 90s
// after boot so the inventory poller can finish its initial cycle (and
// populate hashes) before autosync inspects them. Subsequent runs fire
// every `interval`.
//
// Bypass-tenant only as of 2026-05-16 (F22): production multi-tenant
// rollout will iterate figma_team_seed and run one Planner+Executor
// pair per seeded tenant. See plan
// docs/plans/2026-05-14-001-feat-figma-db-autosync-bridge-plan.md
// (Phase D — webhook trigger + tenant-wide rollout).
// TODO(figma-autosync-multitenant): switch to figma_team_seed iteration.
func startAutosyncRetryLoop(
	ctx context.Context,
	log *slog.Logger,
	ps *projects.Server,
	pool *db.DB,
	interval time.Duration,
	pollerReady <-chan struct{},
) {
	sqlDB := pool.Write() // lease + raw-SQL helpers use the write pool
	tenantID := strings.TrimSpace(os.Getenv("DEV_AUTH_BYPASS_TENANT"))
	if tenantID == "" {
		log.Info("autosync retry loop: no DEV_AUTH_BYPASS_TENANT, skipping")
		return
	}
	go func() {
		log.Info("autosync retry loop started",
			"tenant", tenantID, "interval", interval.String(),
			"first_run", "after inventory poller's first cycle completes")
		// F18 — wait for the poller's first cycle to finish so hashes
		// + classifier columns are populated before the planner reads
		// them. Replaces the previous fixed 90s sleep that coupled
		// silently to internal poller timing. ctx.Done() still wins
		// when the operator SIGTERMs before the poller readies (e.g.
		// PAT misconfigured → poller errors but never readies).
		//
		// When FIGMA_INVENTORY_DISABLED=1 the poller never starts, so
		// pollerReady stays open forever. Skip the wait in that case:
		// the operator is responsible for ensuring figma_section +
		// figma_node_metadata are populated through other means
		// (cmd/figma-inventory-sync, the depth=3 curl trigger, etc.).
		if os.Getenv("FIGMA_INVENTORY_DISABLED") != "1" {
			select {
			case <-pollerReady:
			case <-ctx.Done():
				return
			}
		}
		runOnce := func() {
			// F1: panic guard. The retry loop is the autosync subsystem's
			// only self-heal mechanism; a single panic in PlanTenant /
			// Execute / RunExport would silently kill it for the rest of
			// the process lifetime. Recover, log, and let the next tick fire.
			defer func() {
				if r := recover(); r != nil {
					log.Error("autosync retry: PANIC recovered",
						"panic", fmt.Sprint(r),
						"stack", string(debug.Stack()))
				}
			}()
			// F11 — collapse with any in-flight HTTP /execute. TryLock
			// rather than block: a queued cycle would repeat the same
			// work, and the next tick fires in `interval` anyway.
			ok, err := tryAcquireAutosyncLease(ctx, sqlDB, tenantID)
			if err != nil {
				log.Warn("autosync retry: lease acquire failed", "err", err.Error())
				return
			}
			if !ok {
				log.Info("autosync retry: skipped (handler already running)")
				return
			}
			defer releaseAutosyncLease(context.Background(), sqlDB, log, tenantID)
			// #6 audit fix: per-cycle deadline. A hung PlanTenant or
			// Execute would otherwise hold the lease forever (until
			// TTL) and block every subsequent tick + HTTP /execute.
			// 10 minutes is comfortably above the documented worst-
			// case full-tenant cycle (~5min at 502 files) and well
			// below the 15min default tick interval.
			cycleCtx, cycleCancel := context.WithTimeout(ctx, 10*time.Minute)
			defer cycleCancel()
			planner := inventory.NewPlanner(adminAutoSyncDB{pool: pool}, inventory.PlannerConfig{Log: log})
			executor := inventory.NewExecutor(adminAutoSyncDB{pool: pool}, ps.RunExport)
			plans, err := planner.PlanTenant(cycleCtx, tenantID)
			if err != nil {
				log.Warn("autosync retry: plan failed", "err", err.Error())
				return
			}
			var totalFull, totalCheap, totalRetry, totalErr int
			var totalQuarantined, totalSkipUnchanged, totalSections int
			var workableFiles int // files with at least one non-skip section
			for _, plan := range plans {
				if plan.FileSkip != nil {
					continue
				}
				hasWork := false
				// Count retries (FullExports whose reason is the new
				// retry_failed_pipeline) so the log line tells the
				// operator "self-heal" vs "fresh export" volume.
				for _, ps2 := range plan.Sections {
					totalSections++
					if ps2.Action == inventory.ActionFullExport && ps2.Reason == inventory.SkipRetryFailedPipeline {
						totalRetry++
					}
					if ps2.Action != inventory.ActionSkipQuarantined && ps2.Action != inventory.ActionSkipUnchanged {
						hasWork = true
					}
				}
				if hasWork {
					workableFiles++
				}
				res, err := executor.Execute(cycleCtx, plan)
				if err != nil {
					log.Warn("autosync retry: execute failed", "file", plan.FileKey, "err", err.Error())
					totalErr++
					continue
				}
				totalFull += res.FullExported
				totalCheap += res.CheapUpdated
				totalQuarantined += res.SkippedQuar
				totalSkipUnchanged += res.SkippedAlready
				// F19 — surface each per-section error, not just the
				// count. The cycle-complete summary's "errors=N" was
				// useless for debugging — the messages were dropped on
				// the floor. Now each one shows up with file context.
				for _, errMsg := range res.Errors {
					log.Warn("autosync retry: section error",
						"file", plan.FileKey, "err", errMsg)
				}
				totalErr += len(res.Errors)
			}
			// #29 audit fix: surface the cycle's *health shape*, not
			// just the totals. "healthy=true" means at least one file
			// had real work and no errors fired; "everything_quarantined"
			// means the cycle saw sections but ALL were stuck on the
			// F4 freeze — silent feature death otherwise.
			cycleHealthy := totalErr == 0 && (totalFull+totalCheap+totalSkipUnchanged > 0)
			everythingQuarantined := totalSections > 0 && totalQuarantined == totalSections
			log.Info("autosync retry cycle complete",
				"files", len(plans),
				"workable_files", workableFiles,
				"sections", totalSections,
				"full_export", totalFull,
				"cheap_update", totalCheap,
				"skip_unchanged", totalSkipUnchanged,
				"quarantined", totalQuarantined,
				"retried_failed_pipeline", totalRetry,
				"errors", totalErr,
				"healthy", cycleHealthy,
				"everything_quarantined", everythingQuarantined,
			)
		}
		runOnce()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				log.Info("autosync retry loop stopped")
				return
			case <-ticker.C:
				runOnce()
			}
		}
	}()
}
