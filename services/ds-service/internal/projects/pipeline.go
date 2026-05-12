package projects

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/figma/client"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/sse"
)

// PipelineHeartbeatInterval is how often the running pipeline pokes
// pipeline_heartbeat_at. 5s is well below HeartbeatStaleThreshold (30s) so the
// recovery sweeper won't false-positive a healthy pipeline.
const PipelineHeartbeatInterval = 5 * time.Second

// Stage4DownloadConcurrency caps concurrent Figma CDN PNG downloads. Mirrors
// the per-leaf image-fill resolver (screen_image_fills.go) so both I/O waves
// in Stage 4 share the same wall-clock budget shape; raise only with evidence
// of CDN pushback.
const Stage4DownloadConcurrency = 8

// canonicalTreeEntry is the in-memory form of a single screen's extracted
// canonical_tree, shared between Stage 4's image-fill warmer and Stage 6's
// override-reattach pass so both consume one extraction.
type canonicalTreeEntry struct {
	treeJSON string
	hash     string
}

// FigmaImagesAPIBase is overrideable in tests via SetFigmaImagesBase().
var figmaImagesAPIBase = "https://api.figma.com"

// SetFigmaImagesBase swaps the Figma REST base URL for tests pointing at
// httptest.Server. Production callers never use this.
func SetFigmaImagesBase(u string) { figmaImagesAPIBase = u }

// FigmaImageRenderer abstracts the call to /v1/images/{key}?ids=...&format=png&scale=2.
// Pipeline tests inject a fake renderer; production uses the http-backed default.
type FigmaImageRenderer interface {
	RenderPNGs(ctx context.Context, fileKey string, nodeIDs []string) (map[string]string, error)
	DownloadPNG(ctx context.Context, url string) ([]byte, error)
}

// FigmaNodeFetcher abstracts the /v1/files/{key}/nodes call so tests can stub it.
type FigmaNodeFetcher interface {
	GetFileNodes(ctx context.Context, fileKey string, nodeIDs []string, depth int) (map[string]any, error)
}

// AuditEnqueuer is the channel-write helper that worker U5 will hook into. The
// pipeline writes (versionID, traceID) tuples after audit_jobs row is committed.
// U5's WorkerPool subscribes to this channel.
type AuditEnqueuer struct {
	ch chan AuditJobNotification
}

// AuditJobNotification is the payload pipeline writes after queuing a job.
type AuditJobNotification struct {
	VersionID string
	TraceID   string
}

// NewAuditEnqueuer constructs an enqueuer with a buffered channel. Buffer 64
// is plenty for Phase 1 — even a burst of 64 simultaneous exports is well
// outside any plausible designer cohort.
func NewAuditEnqueuer() *AuditEnqueuer {
	return &AuditEnqueuer{ch: make(chan AuditJobNotification, 64)}
}

// EnqueueAuditJob writes a notification non-blockingly. If the channel is full
// (no worker draining), the worker's safety-net poll will pick the job up
// from the DB anyway.
func (e *AuditEnqueuer) EnqueueAuditJob(versionID, traceID string) {
	if e == nil {
		return
	}
	select {
	case e.ch <- AuditJobNotification{VersionID: versionID, TraceID: traceID}:
	default:
		// Drop notification — worker's safety-net poll will catch it.
	}
}

// Notifications returns the read-only channel U5's worker subscribes to.
func (e *AuditEnqueuer) Notifications() <-chan AuditJobNotification {
	if e == nil {
		return nil
	}
	return e.ch
}

// SSEPublisher is the subset of sse.BrokerService the pipeline depends on.
// Defining a local interface keeps the fake test broker simple.
type SSEPublisher interface {
	Publish(traceID string, event sse.Event)
}

// Pipeline is a runnable orchestrator that pulls Figma metadata + PNGs for
// the screens of a freshly-created project_version, persists them, and emits
// the SSE event that unblocks the UI.
type Pipeline struct {
	Repo            *TenantRepo
	Renderer        FigmaImageRenderer
	NodeFetcher     FigmaNodeFetcher
	SSE             SSEPublisher
	AuditEnqueuer   *AuditEnqueuer
	AuditLogger     *AuditLogger
	DataDir         string // services/ds-service/data — caller passes absolute path
	Log             *slog.Logger
	// Phase 3.5 U2: optional KTX2 transcoder. nil = disabled (PNG-only
	// flow). When set, persistPNG forks basisu to emit a sibling .ktx2
	// after the rename. Failure is non-fatal — the PNG is the source
	// of truth and the frontend falls back when .ktx2 is missing.
	KTX2 *KTX2Transcoder
	// Stage 6.5 (cluster prerender) deps. Optional — when nil, Stage 6.5
	// is skipped and the frontend falls back to on-demand cluster render
	// via HandleAssetDownload.
	PreviewPyramid *PreviewPyramidGenerator
	// AssetExporter — used by Stage 9 phase 1 to do ONE batched
	// /v1/images call per chunk-of-80-clusters, vs the per-node
	// thrashing that previously 429'd Figma for the entire budget.
	AssetExporter *AssetExporter
	// ImageFillResolver — when set, Stage 4 runs a sibling goroutine
	// alongside the PNG download pool that pre-warms asset_cache for
	// every imageRef in the version's canonical_trees. Without it the
	// per-leaf lazy fallback in ResolveImageRefsForLeaf still works,
	// just adds a /v1/files/.../images round-trip and N CDN GETs to
	// first-render latency.
	ImageFillResolver *ImageFillResolver
	// ShutdownCtx — process-wide cancellation signal wired in by main()
	// from signal.NotifyContext(SIGTERM, SIGINT). Background work (Stage 9
	// cluster prerender) derives its bgCtx from this so a graceful deploy
	// cancels in-flight prerenders instead of killing the goroutine
	// mid-StoreAsset write. nil is allowed (tests, embedded use); the
	// stage-9 spawn falls back to context.Background() in that case.
	ShutdownCtx context.Context
	// PrerenderStatus — process-wide ring buffer that captures one
	// PrerenderRun per Stage 9 invocation for operator triage. Wired in
	// by main() and surfaced via GET /v1/admin/prerender/status. nil is
	// allowed (tests, embedded use); the stage-9 spawn skips the
	// recording cleanly via the buffer's nil-safe Append.
	PrerenderStatus *PrerenderStatusBuffer
}

// PipelineInputs is what the HTTP handler hands off after persisting the
// project skeleton. The pipeline takes ownership of all of it.
type PipelineInputs struct {
	VersionID      string
	ProjectID      string
	ProjectSlug    string
	TenantID       string
	UserID         string
	FileID         string
	IdempotencyKey string
	TraceID        string
	IP             string
	UserAgent      string
	// Frames carries (screen_id, figma_frame_id, ...) so pipeline can correlate
	// PNG renders back to DB rows without re-fetching.
	Frames []PipelineFrame
}

// PipelineFrame is one row in PipelineInputs.Frames.
type PipelineFrame struct {
	ScreenID                  string
	FigmaFrameID              string
	X, Y                      float64
	Width, Height             float64
	VariableCollectionID      string
	ModeID                    string
	ModeLabel                 string
	ExplicitVariableModesJSON string
}

// RunFastPreview is the orchestrator's entry point. Idempotent on retry: if a
// version has already moved to view_ready or failed, the function returns
// immediately. Background goroutine; intended to run as `go p.RunFastPreview(...)`.
//
// Stages:
//
//  1. Heartbeat goroutine — refreshes pipeline_heartbeat_at every 5s.
//  2. Pull frames in batches via Figma REST /v1/files/{key}/nodes (3-attempt
//     429 backoff).
//  3. Render PNGs via /v1/images/{key}?ids=...&format=png&scale=2 (3-attempt
//     429 backoff).
//  4. Per PNG: cap long edge to 4096px, persist atomically to data/screens/<tenant>/<version>/<screen>@2x.png.
//  5. Re-run mode-pair detection server-side.
//  6. Single transaction: insert screen_canonical_trees + screen_modes,
//     UPDATE project_versions SET status='view_ready', INSERT audit_jobs.
//  7. SSE.Publish ProjectViewReady.
//  8. Notify worker via AuditEnqueuer.
//
// Errors transition the version to 'failed', write audit_log, publish ProjectExportFailed.
func (p *Pipeline) RunFastPreview(ctx context.Context, in PipelineInputs) error {
	// Detach from the request context — the HTTP handler returned 202 already
	// and we don't want a request cancel to abort an in-flight pipeline.
	// Caller may pass context.Background() but we still derive a fresh one
	// here to make the contract explicit.
	pipelineCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Heartbeat goroutine — runs until pipeline returns.
	hbCtx, hbCancel := context.WithCancel(pipelineCtx)
	var hbWG sync.WaitGroup
	hbWG.Add(1)
	go func() {
		defer hbWG.Done()
		tk := time.NewTicker(PipelineHeartbeatInterval)
		defer tk.Stop()
		for {
			select {
			case <-hbCtx.Done():
				return
			case <-tk.C:
				_ = p.Repo.HeartbeatVersion(hbCtx, in.VersionID)
			}
		}
	}()

	defer func() {
		hbCancel()
		hbWG.Wait()
	}()

	if err := p.runStages(pipelineCtx, in); err != nil {
		// Failure path: mark version failed, audit, publish.
		p.fail(pipelineCtx, in, err)
		return err
	}

	// T6 — retention sweep. The version we just landed bumped this
	// project to N+1 versions. If N+1 exceeds the retention budget,
	// reclaim the oldest versions' on-disk PNG dirs. Runs async with a
	// detached context so a slow sweep doesn't block the HTTP response
	// (already sent) or the SSE bus.
	if p.DataDir != "" {
		retain := versionRetention()
		go func() {
			swCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			pruned, _, err := PruneOldVersionDirs(swCtx, p.Log, p.Repo, p.DataDir, in.ProjectID, retain)
			if err != nil && p.Log != nil {
				p.Log.Warn("retention sweep failed", "project_id", in.ProjectID, "err", err.Error())
			}
			if pruned > 0 && p.Log != nil {
				p.Log.Info("retention sweep", "project_id", in.ProjectID, "pruned_versions", pruned, "retain", retain)
			}
		}()
	}
	return nil
}

// versionRetention reads VERSION_RETENTION env var (defaults to
// DefaultVersionRetention=3). Floored at 1 — retaining zero versions
// would prune even the just-landed view_ready, which is never what we
// want.
func versionRetention() int {
	if raw := os.Getenv("VERSION_RETENTION"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 1 {
			return n
		}
	}
	return DefaultVersionRetention
}

// runStages is the inner pipeline. Split out so RunFastPreview can wrap it
// with a single defer-driven error path.
func (p *Pipeline) runStages(ctx context.Context, in PipelineInputs) error {
	if len(in.Frames) == 0 {
		return fmt.Errorf("pipeline: no frames in version %s", in.VersionID)
	}

	// Stage 2 — fetch node metadata. Phase 1 ignores the response (the canonical
	// tree it returns is what we'll persist into screen_canonical_trees), but we
	// still call it to validate the file is accessible AND to get any naming
	// hints for screen_modes. Single batch keeps the request count low.
	frameIDs := make([]string, 0, len(in.Frames))
	for _, f := range in.Frames {
		frameIDs = append(frameIDs, f.FigmaFrameID)
	}
	canonicalNodes, err := p.fetchNodesWithRetry(ctx, in.FileID, frameIDs)
	if err != nil {
		return fmt.Errorf("figma nodes: %w", err)
	}

	// Blocklist consult (2026-05-12 — figma_render_blocklist). Filter
	// out any frames that hit the consecutive-failure threshold and are
	// still inside their cooldown window. Without this, every sync
	// cycle would re-burn the per-PAT rate-limit budget on frames Figma
	// deterministically can't render (e.g., goals-revamp 782:89312).
	//
	// Stage 4+ runs only on `renderFrameIDs`. Stage 6 still walks
	// `in.Frames` so blocklisted frames keep their canonical_tree (the
	// vector renderer can paint from the tree alone even without a PNG).
	renderFrameIDs := make([]string, 0, len(frameIDs))
	blocklistedFrameIDs := make([]string, 0)
	for _, fid := range frameIDs {
		_, blocked, berr := p.Repo.IsFigmaRenderBlocked(ctx, in.FileID, fid)
		if berr != nil {
			if p.Log != nil {
				p.Log.Warn("blocklist consult failed; treating frame as not blocked",
					"version_id", in.VersionID, "frame_id", fid, "err", berr)
			}
			renderFrameIDs = append(renderFrameIDs, fid)
			continue
		}
		if blocked {
			blocklistedFrameIDs = append(blocklistedFrameIDs, fid)
			continue
		}
		renderFrameIDs = append(renderFrameIDs, fid)
	}
	if len(blocklistedFrameIDs) > 0 && p.Log != nil {
		p.Log.Info("pipeline: frames suppressed by figma_render_blocklist (cooldown active)",
			"version_id", in.VersionID,
			"suppressed_count", len(blocklistedFrameIDs),
			"suppressed_ids", blocklistedFrameIDs,
		)
	}

	// Stage 3 — render PNGs (skips blocklisted frames).
	var pngURLs map[string]string
	if len(renderFrameIDs) > 0 {
		pngURLs, err = p.renderPNGsWithRetry(ctx, in.FileID, renderFrameIDs)
		if err != nil {
			return fmt.Errorf("figma images: %w", err)
		}
	} else {
		pngURLs = map[string]string{}
	}

	// Pre-extract canonical trees once. Stage 6 (override reattach) and the
	// optional Stage-4 image-fill warmer both consume them; doing it here
	// avoids walking canonicalNodes twice.
	extractedTrees := make(map[string]canonicalTreeEntry, len(in.Frames))
	for _, f := range in.Frames {
		treeJSON, hash := extractCanonicalTree(canonicalNodes, f.FigmaFrameID)
		extractedTrees[f.FigmaFrameID] = canonicalTreeEntry{treeJSON: treeJSON, hash: hash}
	}

	// Partition frames by render-URL availability. Pre-2026-05-09 the
	// pipeline aborted the entire version when ANY frame was missing a
	// PNG URL — so a single Figma-side render bug on one node ID stranded
	// every other frame in the same import (goals-revamp-iteration-2:
	// 1 bad frame, 122 sibling frames lost). Now we keep going for the
	// renderable frames and skip the stragglers; they end up with a
	// canonical_tree but NULL png_storage_key (Stage 2's /nodes call
	// already populated the tree, which is independent of /images).
	// Fast-membership check for blocklisted frames so we don't
	// re-attribute a "we never asked Figma for it" as a fresh failure.
	blocklisted := make(map[string]struct{}, len(blocklistedFrameIDs))
	for _, fid := range blocklistedFrameIDs {
		blocklisted[fid] = struct{}{}
	}

	goodFrames := make([]PipelineFrame, 0, len(in.Frames))
	skippedFrameIDs := make([]string, 0)
	for _, frame := range in.Frames {
		if _, isBlocked := blocklisted[frame.FigmaFrameID]; isBlocked {
			// Suppressed earlier; don't count this as a new failure.
			continue
		}
		if url, ok := pngURLs[frame.FigmaFrameID]; ok && url != "" {
			goodFrames = append(goodFrames, frame)
		} else {
			skippedFrameIDs = append(skippedFrameIDs, frame.FigmaFrameID)
		}
	}

	// Blocklist accounting (2026-05-12 — figma_render_blocklist).
	// CLEAR on success so a recovered frame stops being suppressed.
	// MARK on miss so persistent upstream-broken frames stop burning
	// rate-limit budget every cycle (after BlocklistFailureThreshold).
	// Order matters: clear before mark in case a frame's prior failure
	// just cleared its cooldown AND this cycle's render finally
	// succeeded — we don't want to spuriously re-mark it.
	for _, frame := range goodFrames {
		if cerr := p.Repo.ClearFigmaRenderFailure(ctx, in.FileID, frame.FigmaFrameID); cerr != nil && p.Log != nil {
			p.Log.Warn("blocklist clear (on success) failed; non-fatal",
				"version_id", in.VersionID, "frame_id", frame.FigmaFrameID, "err", cerr)
		}
	}
	for _, fid := range skippedFrameIDs {
		var clearHash string
		if entry, ok := extractedTrees[fid]; ok {
			clearHash = entry.hash
		}
		if blockEntry, merr := p.Repo.MarkFigmaRenderFailure(ctx, in.FileID, fid,
			"figma rendered no URL for frame", clearHash); merr != nil && p.Log != nil {
			p.Log.Warn("blocklist mark failed; non-fatal",
				"version_id", in.VersionID, "frame_id", fid, "err", merr)
		} else if blockEntry != nil && p.Log != nil {
			p.Log.Warn("frame blocklisted after consecutive Figma render failures",
				"version_id", in.VersionID, "file_id", in.FileID, "frame_id", fid,
				"consecutive_failures", blockEntry.ConsecutiveFailures,
				"cooldown_until", blockEntry.CooldownUntil.UTC().Format(time.RFC3339))
		}
	}

	if len(goodFrames) == 0 {
		// All frames failed Figma's image render — there's nothing to
		// recover. Keep the historical hard-fail so the version flips
		// to `failed` and the FE retry button stays available. The
		// list of frame IDs is in the error so ops can grep it.
		return fmt.Errorf("figma rendered no URL for any frame in version %s (frames=%v)",
			in.VersionID, skippedFrameIDs)
	}
	if len(skippedFrameIDs) > 0 && p.Log != nil {
		p.Log.Warn("pipeline: figma render-URL miss; skipping affected frames",
			"version_id", in.VersionID,
			"total_frames", len(in.Frames),
			"skipped_frames", len(skippedFrameIDs),
			"skipped_ids", skippedFrameIDs,
		)
	}

	// Stage 4 — download, downsample, persist (parallel, bounded).
	// Sibling goroutine pre-warms image-fill cache when ImageFillResolver
	// is wired so first canvas render hits warm asset_cache rows instead
	// of waiting on /v1/files/.../images.
	pngKeys := make(map[string]string, len(in.Frames))
	var keysMu sync.Mutex

	gctx, gcancel := context.WithCancel(ctx)
	defer gcancel()
	errCh := make(chan error, len(in.Frames))

	var stage4WG sync.WaitGroup

	if p.ImageFillResolver != nil {
		treesJSON := make([]string, 0, len(extractedTrees))
		for _, e := range extractedTrees {
			if e.treeJSON != "" {
				treesJSON = append(treesJSON, e.treeJSON)
			}
		}
		stage4WG.Add(1)
		go func() {
			defer stage4WG.Done()
			if werr := p.ImageFillResolver.WarmImageFillsForVersion(gctx, in.TenantID, in.FileID, in.VersionID, treesJSON); werr != nil {
				if p.Log != nil {
					p.Log.Warn("image-fill warm failed (lazy fallback active)",
						"version_id", in.VersionID,
						"err", werr,
					)
				}
			}
		}()
	}

	sem := make(chan struct{}, Stage4DownloadConcurrency)
	for _, frame := range goodFrames {
		frame := frame
		url := pngURLs[frame.FigmaFrameID]
		stage4WG.Add(1)
		sem <- struct{}{}
		go func() {
			defer stage4WG.Done()
			defer func() { <-sem }()
			if gctx.Err() != nil {
				return
			}
			raw, err := p.Renderer.DownloadPNG(gctx, url)
			if err != nil {
				errCh <- fmt.Errorf("download png %s: %w", frame.FigmaFrameID, err)
				gcancel()
				return
			}
			downsampled, err := DownsampleLongEdge(raw, MaxLongEdgePx)
			if err != nil {
				errCh <- fmt.Errorf("downsample png %s: %w", frame.FigmaFrameID, err)
				gcancel()
				return
			}
			key, err := p.persistPNG(in.TenantID, in.VersionID, frame.ScreenID, downsampled)
			if err != nil {
				errCh <- fmt.Errorf("persist png %s: %w", frame.FigmaFrameID, err)
				gcancel()
				return
			}
			if err := p.Repo.SetScreenPNG(gctx, frame.ScreenID, key); err != nil {
				errCh <- fmt.Errorf("update screen png_key %s: %w", frame.ScreenID, err)
				gcancel()
				return
			}
			keysMu.Lock()
			pngKeys[frame.FigmaFrameID] = key
			keysMu.Unlock()
		}()
	}

	stage4WG.Wait()
	close(errCh)
	if firstErr, ok := <-errCh; ok {
		return firstErr
	}

	// Stage 5 — re-run mode-pair detection on the server (canonicalize across
	// plugin payload). Result drives screen_modes inserts in the next stage.
	infos := make([]FrameInfo, 0, len(in.Frames))
	for _, f := range in.Frames {
		infos = append(infos, FrameInfo{
			FrameID:              f.FigmaFrameID,
			X:                    f.X,
			Y:                    f.Y,
			Width:                f.Width,
			Height:               f.Height,
			VariableCollectionID: f.VariableCollectionID,
			ModeID:               f.ModeID,
			ModeLabel:            f.ModeLabel,
		})
	}
	groups := DetectModePairs(infos)

	// Build screen_modes rows. Each plugin frame becomes one screen_modes row;
	// the mode_label comes from the plugin payload (default "default" if blank).
	frameByID := make(map[string]PipelineFrame, len(in.Frames))
	for _, f := range in.Frames {
		frameByID[f.FigmaFrameID] = f
	}
	var modes []ScreenMode
	for _, g := range groups {
		for _, gf := range g.Frames {
			pf, ok := frameByID[gf.FrameID]
			if !ok {
				continue
			}
			label := pf.ModeLabel
			if label == "" {
				label = "default"
			}
			modes = append(modes, ScreenMode{
				ScreenID:                  pf.ScreenID,
				ModeLabel:                 label,
				FigmaFrameID:              pf.FigmaFrameID,
				ExplicitVariableModesJSON: pf.ExplicitVariableModesJSON,
			})
		}
	}

	// Pre-Stage-6 — read every screen's existing override rows BEFORE we
	// open the canonical_tree write tx. SQLite serialises writers; if we
	// held the tx open and then SELECTed for overrides we'd deadlock the
	// single writer connection (same pattern as the Phase 7+8 graph-rebuild
	// fix in docs/solutions/2026-05-01-003-phase-7-8-closure.md). The
	// captured rows are passed straight into ReattachOverridesForScreen
	// inside the tx — no additional reads happen there.
	type screenReattach struct {
		screenID  string
		treeJSON  string
		hash      string
		overrides []ScreenOverride
	}
	reattaches := make([]screenReattach, 0, len(in.Frames))
	for _, f := range in.Frames {
		entry := extractedTrees[f.FigmaFrameID]
		overrides, oerr := p.Repo.ListActiveOverridesForScreen(ctx, f.ScreenID)
		if oerr != nil {
			return fmt.Errorf("list overrides for screen %s: %w", f.ScreenID, oerr)
		}
		reattaches = append(reattaches, screenReattach{
			screenID:  f.ScreenID,
			treeJSON:  entry.treeJSON,
			hash:      entry.hash,
			overrides: overrides,
		})
	}

	// Stage 6 — single transaction: canonical_trees + screen_modes + status flip
	// + audit_jobs row + override re-anchor. All-or-nothing.
	tx, err := p.Repo.BeginTx(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// Canonical trees + per-screen override re-anchor in the same tx. Order
	// matters: InsertCanonicalTree first so the row exists for any tx-local
	// integrity checks; ReattachOverridesForScreen rewrites override anchors
	// to match the just-written tree.
	auditWriter := OverrideAuditLogger{}
	for _, r := range reattaches {
		if err := p.Repo.InsertCanonicalTree(ctx, tx, r.screenID, r.treeJSON, r.hash); err != nil {
			return fmt.Errorf("insert canonical tree: %w", err)
		}
		if len(r.overrides) == 0 {
			continue
		}
		results, rerr := p.Repo.ReattachOverridesForScreen(ctx, tx, r.screenID, r.overrides, r.treeJSON, auditWriter)
		if rerr != nil {
			// A failure here means the UPDATE statement itself broke (DB
			// error, not a per-override resolution miss). Fail the whole
			// pipeline tx so the canonical_tree change rolls back too —
			// otherwise we'd leave overrides pointing into a tree that's
			// already been replaced.
			return fmt.Errorf("reattach overrides for screen %s: %w", r.screenID, rerr)
		}
		if p.Log != nil && len(results) > 0 {
			orphans := 0
			for _, res := range results {
				if res.NewStatus == ScreenOverrideStatusOrphaned {
					orphans++
				}
			}
			p.Log.Debug("override reattach",
				"screen_id", r.screenID,
				"total", len(results),
				"orphaned", orphans,
			)
		}
	}

	if err := p.Repo.InsertScreenModes(ctx, tx, modes); err != nil {
		return fmt.Errorf("insert screen_modes: %w", err)
	}
	if err := p.Repo.RecordViewReady(ctx, tx, in.VersionID); err != nil {
		return fmt.Errorf("flip view_ready: %w", err)
	}
	if _, err := p.Repo.EnqueueAuditJob(ctx, tx, in.VersionID, in.TraceID, in.IdempotencyKey); err != nil {
		return fmt.Errorf("enqueue audit: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	// Stage 7 — SSE event.
	if p.SSE != nil {
		p.SSE.Publish(in.TraceID, sse.ProjectViewReady{
			ProjectSlug: in.ProjectSlug,
			VersionID:   in.VersionID,
			Tenant:      in.TenantID,
		})
	}

	// Stage 8 — notify the audit worker.
	if p.AuditEnqueuer != nil {
		p.AuditEnqueuer.EnqueueAuditJob(in.VersionID, in.TraceID)
	}

	// Stage 9 — cluster pre-render. Walk every screen's canonical_tree,
	// extract icon/illustration/shape clusters, and render the preview
	// pyramid for each so the frontend gets cache-only GETs at leaf-open.
	// Pre-fix the frontend raced ~1500-2000 concurrent /assets/<node>
	// fetches at leaf-open against Figma's render budget; most timed out
	// or 502'd, leaving illustrations blank in random frames.
	//
	// Spawned in a goroutine with a fresh context so view_ready isn't
	// blocked. Failures are logged but never re-surface — the existing
	// HandleAssetDownload on-demand path remains as a safety net.
	if p.PreviewPyramid != nil {
		seen := make(map[string]struct{})
		clusterIDs := make([]string, 0, 256)
		svgIDs := make([]string, 0, 64)
		for _, r := range reattaches {
			for _, c := range ExtractClustersWithSVGFlag([]byte(r.treeJSON)) {
				if _, dup := seen[c.ID]; dup {
					continue
				}
				seen[c.ID] = struct{}{}
				if c.SVGEligible {
					// SVG-eligible clusters bypass the pyramid entirely —
					// one Figma /v1/images call with format=svg per chunk
					// returns vector text the browser scales without
					// pixelation. Recorded separately so Phase 2 doesn't
					// downsample raster bytes that don't exist.
					svgIDs = append(svgIDs, c.ID)
				} else {
					clusterIDs = append(clusterIDs, c.ID)
				}
			}
		}
		if len(svgIDs) > 0 || len(clusterIDs) > 0 {
			go func(versionID string, ids []string, svgs []string) {
				// Outer goroutine panic recovery. PrerenderClusters spawns
				// per-node goroutines with their own recover, but ExtractClusterIDs
				// already ran upstream and any panic from setup logic
				// (GetAnyLeafIDForVersion / GetVersionIndex / type-assert) would
				// otherwise crash the entire ds-service process. Recover here
				// and log; the on-demand HandleAssetDownload path is the
				// safety net.
				defer func() {
					if r := recover(); r != nil && p.Log != nil {
						p.Log.Error("stage 9: panic in prerender goroutine",
							"version_id", versionID,
							"panic", fmt.Sprintf("%v", r),
						)
					}
				}()
				// Per-version dedup. HandleVersionRetry + a quick-fire
				// double-export must not double-spend Figma quota or
				// double-write asset_cache. If another goroutine is already
				// prerendering this version, log + skip; the on-demand path
				// covers any caller that needed the result fresh.
				if !AcquirePrerenderSlot(versionID) {
					if p.Log != nil {
						p.Log.Info("stage 9: skip — prerender already running for this version",
							"version_id", versionID,
						)
					}
					return
				}
				defer ReleasePrerenderSlot(versionID)
				// Derive bgCtx from the process-wide shutdown context so
				// SIGTERM cancels in-flight prerenders. Falls back to
				// Background() for tests / embedded callers that don't wire
				// ShutdownCtx.
				parent := p.ShutdownCtx
				if parent == nil {
					parent = context.Background()
				}
				bgCtx, cancel := context.WithTimeout(
					parent,
					ClusterPrerenderTotalBudget,
				)
				defer cancel()
				deps := ClusterPrerenderDeps{
					Repo:           p.Repo,
					PreviewPyramid: p.PreviewPyramid,
					AssetExporter:  p.AssetExporter,
				}
				startedAt := time.Now().UTC()

				// Phase 2.1 — render SVG-eligible clusters as vector
				// faithful Figma exports BEFORE the raster pyramid
				// pass. SVGs are tier-agnostic (one cache row instead
				// of four) and the browser scales them at any zoom
				// without pixelation, so they fix the canvas zoom
				// ceiling complaint without ballooning storage.
				//
				// RenderAssetsForLeaf already supports format="svg"
				// and persists the bytes to asset_cache + disk via
				// the same chunked / rate-limited / retry-aware path
				// as the PNG export. Failures are logged but never
				// fail the pipeline — a cluster that fails SVG
				// render falls through to the on-demand HandleAsset-
				// Download path, which still serves the historical
				// raster.
				if len(svgs) > 0 && p.AssetExporter != nil {
					svgRendered, svgErr := renderSVGClustersForVersion(
						bgCtx, p.AssetExporter, in, svgs)
					if p.Log != nil {
						if svgErr != nil {
							p.Log.Warn("stage 9: svg cluster render failed",
								"version_id", versionID,
								"requested", len(svgs),
								"rendered", svgRendered,
								"err", svgErr.Error(),
							)
						} else {
							p.Log.Info("stage 9: svg cluster render done",
								"version_id", versionID,
								"requested", len(svgs),
								"rendered", svgRendered,
							)
						}
					}
				}

				rendered, perr := PrerenderClusters(bgCtx, p.Log, deps, in, ids, DefaultClusterPrerenderConfig)
				if perr != nil {
					if p.Log != nil {
						p.Log.Warn("stage 9: cluster prerender failed",
							"version_id", versionID,
							"err", perr.Error(),
						)
					}
				}
				// Record the run into the status ring buffer for operator
				// observability. nil-safe so tests / embedded callers don't
				// need to wire one up. Failed count is len(ids) - rendered
				// when PrerenderClusters returned without setup-error;
				// when setup-error fired, total work was 0 and the
				// outcome string carries the diagnostic.
				if p.PrerenderStatus != nil {
					finishedAt := time.Now().UTC()
					failed := 0
					if perr == nil {
						failed = len(ids) - rendered
						if failed < 0 {
							failed = 0
						}
					}
					setupErrStr := ""
					if perr != nil {
						setupErrStr = perr.Error()
					}
					p.PrerenderStatus.Append(PrerenderRun{
						VersionID:     versionID,
						FileID:        in.FileID,
						TenantID:      in.TenantID,
						StartedAt:     startedAt,
						FinishedAt:    finishedAt,
						DurationMs:    finishedAt.Sub(startedAt).Milliseconds(),
						TotalClusters: len(ids),
						Rendered:      rendered,
						Failed:        failed,
						Outcome:       ClassifyOutcome(len(ids), rendered, failed, perr),
						SetupError:    setupErrStr,
					})
				}
			}(in.VersionID, clusterIDs, svgIDs)
		}
	}

	return nil
}

// ClusterPrerenderTotalBudget caps Stage 9's wall-clock so a stuck
// Figma render (e.g., a malformed node) can't hold the prerender
// goroutine open forever. Real-world sizing for the NRI VKYC dataset:
//
//   ~4400 unique cluster IDs across two concurrently-running export
//   pipelines, each chunked at 80 IDs per Figma /v1/images call.
//   Per-chunk: ~5-15s for URL fetch (rate-limited 5 req/s shared) +
//   per-node bytes download (50/s) + persist. Round-trip per chunk
//   averages ~12s, so 55 chunks × 2 projects on shared limiter = ~25
//   min worst case. Phase 2 then downsamples each cached node
//   locally (~50ms each) — bounded by Phase 1's success count.
//
// 30 min covers a fresh export of two large NRI projects without
// cutting Phase 2 off; smaller exports finish well inside this and
// Phase 2 short-circuits on cache lookups.
const ClusterPrerenderTotalBudget = 30 * time.Minute


// fail is the centralized failure path. Marks the version failed, writes the
// audit log row, and publishes ProjectExportFailed.
func (p *Pipeline) fail(ctx context.Context, in PipelineInputs, err error) {
	// Audit B4 — extract the failed stage from the wrapping prefix so the
	// log carries a structured `stage=...` field. Operators can grep for
	// `stage=render_pngs` or `stage=download_png` instead of pattern-
	// matching the freeform error message.
	stage := pipelineStageFromError(err)
	if p.Log != nil {
		p.Log.Error("pipeline failed",
			"version_id", in.VersionID, "trace_id", in.TraceID,
			"stage", stage, "err", err.Error())
	}
	if rerr := p.Repo.RecordFailed(ctx, in.VersionID, err.Error()); rerr != nil && p.Log != nil {
		p.Log.Error("pipeline failure: cannot mark version failed",
			"version_id", in.VersionID, "err", rerr.Error())
	}
	if p.AuditLogger != nil {
		_ = p.AuditLogger.WriteExport(ctx, AuditExportEvent{
			Action:    AuditActionExportFailed,
			UserID:    in.UserID,
			TenantID:  in.TenantID,
			FileID:    in.FileID,
			ProjectID: in.ProjectID,
			VersionID: in.VersionID,
			IP:        in.IP,
			UserAgent: in.UserAgent,
			TraceID:   in.TraceID,
			Error:     err.Error(),
		})
	}
	if p.SSE != nil {
		p.SSE.Publish(in.TraceID, sse.ProjectExportFailed{
			ProjectSlug: in.ProjectSlug,
			VersionID:   in.VersionID,
			Tenant:      in.TenantID,
			Error:       err.Error(),
		})
	}
}

// CanonicalTreeFetchDepth is the depth Figma's /v1/files/<key>/nodes
// endpoint walks under each requested screen frame. depth=3 (the prior
// value) cut subtrees off at three levels — frames at depth ≥ 4 came
// back as empty bounding boxes and rendered as grey columns in the
// canvas-v2 LeafFrameRenderer. Real INDmoney screens nest 6–8 levels:
//
//	screen → header/body → card/list → row → label/icon → vector path
//
// depth=10 covers virtually every observed nesting on prod files while
// staying well under Figma's response-size limits (the API itself has
// no documented depth cap; the practical bound is the 100 MB JSON
// response limit, which we never approach for typical leaf exports).
//
// Tradeoff: payload + canonical_tree blob grows ~3-5x at depth=10 vs
// depth=3 for content-heavy screens. The canonical_tree is gzipped on
// disk (T8 migration) so the storage hit is bounded; render-side memory
// usage scales linearly with node count (~1 KB per node).
//
// Tracked migration note: existing canonical_trees stored at depth=3
// won't grow on their own — projects need to be re-exported (POST
// /v1/projects/<slug>/export or the sheets-sync re-import flow) to pick
// up the deeper trees. See docs/issues/2026-05-05-canonical-tree-depth.md
// for the operator runbook.
//
// 2026-05-12 round-2 fidelity audit (P8 + P12) raised the cap from
// 10 → 14. Production cases that needed depth ≥ 11:
//   - Tax Centre `Net P&L` row composes 4 colored chip frames (16×16
//     each) inside an inner autolayout row. The chip TEXT leaves
//     ("A"/"B"/"C"/"D") sat at depth 11 from the screen root; at
//     depth=10 the inner Frame 1686556590 came back with empty children
//     and the chips rendered as empty boxes (visible regression in
//     `3267:107865`).
//   - Networth bottomsheet `us_v2` (375×573) nests one wrapper deeper
//     than full screens because the sheet adds an extra "Category"
//     frame at the top level. Depth-cap=10 sheared every leaf TEXT at
//     depth ≥ 11 — "Invested", "Absolute returns", "1D Change" labels
//     all disappeared from the rendered output.
// 14 is a generous ceiling that covers both patterns + future bottom-
// sheet nesting without bloating the Figma response payload meaningfully
// (each step adds ~10-20 nodes typical).
const CanonicalTreeFetchDepth = 14

// MinRateLimitWait floors Retry-After-derived sleeps so a buggy or zero
// header doesn't degenerate into a tight retry loop. Mirrors the prior
// hard-coded backoff base. Exposed as a var so retry-helper tests can
// lower it without busy-waiting on real-world durations.
var MinRateLimitWait = 500 * time.Millisecond

// MaxRateLimitWait caps Retry-After-derived sleeps so a pathological header
// (Figma occasionally returns multi-minute values under sustained pressure)
// can't hold a Stage 2/3 goroutine open beyond the pipeline's overall budget.
// 60s is well over Figma's typical 30s ceiling and well under our 30-min
// Stage 9 budget. Exposed as a var so retry-helper tests can lower it.
var MaxRateLimitWait = 60 * time.Second

// fetchNodesWithRetry calls /v1/files/{key}/nodes with 3-attempt backoff on 429.
// When the response carries a Retry-After header (typed-error path via
// client.APIError), we honor it within [MinRateLimitWait, MaxRateLimitWait];
// otherwise we fall back to 500ms→1s→2s exponential. Other non-2xx errors
// fail fast.
func (p *Pipeline) fetchNodesWithRetry(ctx context.Context, fileKey string, ids []string) (map[string]any, error) {
	var lastErr error
	fallback := 500 * time.Millisecond
	for attempt := 0; attempt < 3; attempt++ {
		out, err := p.NodeFetcher.GetFileNodes(ctx, fileKey, ids, CanonicalTreeFetchDepth)
		if err == nil {
			return out, nil
		}
		lastErr = err
		if !isRateLimitErr(err) {
			return nil, err
		}
		if attempt < 2 {
			delay := nextRateLimitDelay(err, fallback)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
			fallback *= 2
		}
	}
	return nil, fmt.Errorf("figma nodes after 3 attempts: %w", lastErr)
}

// renderPNGsWithRetry calls the renderer with 3-attempt 429 backoff. Same
// Retry-After handling as fetchNodesWithRetry.
func (p *Pipeline) renderPNGsWithRetry(ctx context.Context, fileKey string, ids []string) (map[string]string, error) {
	var lastErr error
	fallback := 500 * time.Millisecond
	for attempt := 0; attempt < 3; attempt++ {
		out, err := p.Renderer.RenderPNGs(ctx, fileKey, ids)
		if err == nil {
			return out, nil
		}
		lastErr = err
		if !isRateLimitErr(err) {
			return nil, err
		}
		if attempt < 2 {
			delay := nextRateLimitDelay(err, fallback)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
			fallback *= 2
		}
	}
	return nil, fmt.Errorf("figma images after 3 attempts: %w", lastErr)
}

// isRateLimitErr detects 429s. Primary path: errors.As to extract the
// figma client's typed *APIError. Secondary path: substring match — the
// renderer's HTTP wrappers (HTTPFigmaRenderer.RenderPNGs / DownloadPNG)
// format 429 responses as plain errors with a stable "rate limit" or
// "429" substring, and pipeline tests use stubRenderer that returns
// the same shape.
func isRateLimitErr(err error) bool {
	if err == nil {
		return false
	}
	var apiErr *client.APIError
	if errors.As(err, &apiErr) {
		return apiErr.IsRateLimit()
	}
	s := err.Error()
	return strings.Contains(s, "rate limit") || strings.Contains(s, "429")
}

// nextRateLimitDelay extracts a Retry-After-derived wait from the error
// when the typed *client.APIError is available, falling back to the
// caller's exponential schedule otherwise. Result is clamped to
// [MinRateLimitWait, MaxRateLimitWait].
func nextRateLimitDelay(err error, fallback time.Duration) time.Duration {
	var apiErr *client.APIError
	if errors.As(err, &apiErr) && apiErr.RetryAfter > 0 {
		d := apiErr.RetryAfter
		if d < MinRateLimitWait {
			d = MinRateLimitWait
		}
		if d > MaxRateLimitWait {
			d = MaxRateLimitWait
		}
		return d
	}
	return fallback
}

// persistPNG writes data/screens/<tenant>/<version>/<screen>@2x.png atomically
// (.tmp → os.Rename). Returns the storage key (relative path inside the data
// dir) so callers can store it in the screens row.
func (p *Pipeline) persistPNG(tenantID, versionID, screenID string, data []byte) (string, error) {
	if p.DataDir == "" {
		return "", fmt.Errorf("pipeline: DataDir not configured")
	}
	relDir := filepath.Join("screens", tenantID, versionID)
	absDir := filepath.Join(p.DataDir, relDir)
	if err := os.MkdirAll(absDir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir: %w", err)
	}
	relPath := filepath.Join(relDir, screenID+"@2x.png")
	absPath := filepath.Join(p.DataDir, relPath)
	tmp := absPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, absPath); err != nil {
		return "", err
	}

	// Phase 3.5 U2: opportunistic KTX2 transcode. Failure is non-fatal.
	if p.KTX2 != nil && p.KTX2.Available {
		if err := p.KTX2.Transcode(context.Background(), absPath); err != nil {
			if p.Log != nil {
				p.Log.Warn("ktx2: transcode failed; PNG remains the source",
					"screen_id", screenID, "err", err.Error())
			}
		}
	}

	// Phase 3.5 follow-up #2: LOD tier generation. Emit @2x.l1.png (50%)
	// and @2x.l2.png (25%) sibling files so the frontend's pickLOD()
	// helper can request smaller textures at thumbnail-zoom and full
	// textures only when the frame fills a meaningful chunk of the
	// viewport. Failures are non-fatal — the canonical full-size PNG
	// is what the frontend falls back to (lodURL gracefully resolves
	// to the full URL when tier-specific files are absent).
	for _, tier := range []LODTier{LODL1, LODL2} {
		tierBytes, terr := DownsampleByFraction(data, LODFractionFor(tier))
		if terr != nil {
			if p.Log != nil {
				p.Log.Warn("lod: downsample failed",
					"tier", tier, "screen_id", screenID, "err", terr.Error())
			}
			continue
		}
		tierPath := absPath[:len(absPath)-len(".png")] + LODSuffixFor(tier) + ".png"
		tierTmp := tierPath + ".tmp"
		if err := os.WriteFile(tierTmp, tierBytes, 0o644); err != nil {
			if p.Log != nil {
				p.Log.Warn("lod: write failed",
					"tier", tier, "path", tierPath, "err", err.Error())
			}
			continue
		}
		if err := os.Rename(tierTmp, tierPath); err != nil {
			if p.Log != nil {
				p.Log.Warn("lod: rename failed",
					"tier", tier, "path", tierPath, "err", err.Error())
			}
			continue
		}
		// Sibling KTX2 transcode for this tier — same opportunistic
		// failure semantics as the full-size KTX2 above.
		if p.KTX2 != nil && p.KTX2.Available {
			if err := p.KTX2.Transcode(context.Background(), tierPath); err != nil {
				if p.Log != nil {
					p.Log.Warn("ktx2: tier transcode failed",
						"tier", tier, "screen_id", screenID, "err", err.Error())
				}
			}
		}
	}

	return relPath, nil
}

// extractCanonicalTree pulls the per-frame subtree out of the /v1/files/.../nodes
// response and returns its JSON serialization + sha256 hash. If the frame
// isn't in the response (e.g. partial test fixture), an empty tree is returned
// so the screen still has a row in screen_canonical_trees.
func extractCanonicalTree(nodes map[string]any, frameID string) (string, string) {
	if nodes == nil {
		return "{}", ""
	}
	inner, _ := nodes["nodes"].(map[string]any)
	if inner == nil {
		return "{}", ""
	}
	subtree, ok := inner[frameID]
	if !ok {
		return "{}", ""
	}
	bs, err := json.Marshal(subtree)
	if err != nil {
		return "{}", ""
	}
	h := sha256.Sum256(bs)
	return string(bs), hex.EncodeToString(h[:])
}

// ─── Default HTTP-backed renderer ────────────────────────────────────────────

// HTTPFigmaRenderer is the production FigmaImageRenderer. It hits Figma REST
// directly using the same PAT as figma/client.Client so callers don't have to
// wire two auth paths.
type HTTPFigmaRenderer struct {
	Token  string
	Client *http.Client
}

// NewHTTPFigmaRenderer constructs the default renderer. PAT is the per-tenant
// Figma token, decrypted before calling.
func NewHTTPFigmaRenderer(pat string) *HTTPFigmaRenderer {
	return &HTTPFigmaRenderer{
		Token:  pat,
		Client: &http.Client{Timeout: 5 * time.Minute},
	}
}

// figmaRenderChunkSize bounds the number of node IDs per /v1/images call.
// Figma fails large batches with 400 "Render timeout, try requesting fewer
// or smaller images". 25 is conservative for typical mobile frames; on 400
// for an individual chunk we recursively halve.
const figmaRenderChunkSize = 25

// RenderPNGs implements FigmaImageRenderer. Chunks node IDs to stay under
// Figma's per-request render budget; on a render-timeout 400 for a chunk,
// recursively halves the chunk before giving up.
func (r *HTTPFigmaRenderer) RenderPNGs(ctx context.Context, fileKey string, nodeIDs []string) (map[string]string, error) {
	if len(nodeIDs) == 0 {
		return nil, fmt.Errorf("RenderPNGs: empty nodeIDs")
	}
	out := make(map[string]string, len(nodeIDs))
	for i := 0; i < len(nodeIDs); i += figmaRenderChunkSize {
		j := i + figmaRenderChunkSize
		if j > len(nodeIDs) {
			j = len(nodeIDs)
		}
		chunk := nodeIDs[i:j]
		images, err := r.renderChunk(ctx, fileKey, chunk)
		if err != nil {
			return nil, err
		}
		for k, v := range images {
			out[k] = v
		}
	}
	return out, nil
}

// renderChunk hits /v1/images for a single chunk. On a 400 render-timeout
// (Figma's "fewer or smaller images" response), splits the chunk in half
// and retries each half; gives up at single-frame chunks that still 400.
func (r *HTTPFigmaRenderer) renderChunk(ctx context.Context, fileKey string, nodeIDs []string) (map[string]string, error) {
	csv := strings.Join(nodeIDs, ",")
	url := fmt.Sprintf("%s/v1/images/%s?ids=%s&format=png&scale=2", figmaImagesAPIBase, fileKey, csv)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Figma-Token", r.Token)
	req.Header.Set("Accept", "application/json")
	resp, err := r.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 5<<20))
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("figma images: 429 rate limit")
	}
	if resp.StatusCode == http.StatusBadRequest && len(nodeIDs) > 1 && strings.Contains(string(body), "Render timeout") {
		// Halve and retry — one of the frames is too heavy on its own.
		mid := len(nodeIDs) / 2
		left, err := r.renderChunk(ctx, fileKey, nodeIDs[:mid])
		if err != nil {
			return nil, err
		}
		right, err := r.renderChunk(ctx, fileKey, nodeIDs[mid:])
		if err != nil {
			return nil, err
		}
		for k, v := range right {
			left[k] = v
		}
		return left, nil
	}
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("figma images: %d %s", resp.StatusCode, string(body))
	}
	var parsed struct {
		Err    any               `json:"err"`
		Images map[string]string `json:"images"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	if parsed.Err != nil {
		return nil, fmt.Errorf("figma api error: %v", parsed.Err)
	}
	return parsed.Images, nil
}

// DownloadPNG implements FigmaImageRenderer.
func (r *HTTPFigmaRenderer) DownloadPNG(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := r.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("png download: 429 rate limit")
	}
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("png download: %d", resp.StatusCode)
	}
	// 50 MB cap — Figma returns ~5-10 MB PNGs at scale=2 for typical mobile frames.
	bs, err := io.ReadAll(io.LimitReader(resp.Body, 50<<20))
	if err != nil {
		return nil, err
	}
	return bs, nil
}

// pipelineStageFromError maps the freeform error message to a structured
// stage label so logs / dashboards can group by failure stage. Each runStages
// `return fmt.Errorf("<prefix>: %w", err)` site has a unique prefix; this
// table mirrors them. Returns "unknown" when no prefix matches so the field
// is always populated.
func pipelineStageFromError(err error) string {
	if err == nil {
		return "ok"
	}
	msg := err.Error()
	switch {
	case strings.HasPrefix(msg, "pipeline: no frames"):
		return "validate_input"
	case strings.HasPrefix(msg, "figma nodes:"):
		return "fetch_nodes"
	case strings.HasPrefix(msg, "figma images:"):
		return "render_pngs"
	case strings.HasPrefix(msg, "figma rendered no URL"):
		return "render_pngs_partial"
	case strings.HasPrefix(msg, "download png"):
		return "download_png"
	case strings.HasPrefix(msg, "downsample png"):
		return "downsample_png"
	case strings.HasPrefix(msg, "persist png"):
		return "persist_png"
	case strings.HasPrefix(msg, "update screen png_key"):
		return "update_png_key"
	case strings.HasPrefix(msg, "list overrides for screen"):
		return "list_overrides"
	case strings.HasPrefix(msg, "begin tx"):
		return "stage6_tx_begin"
	case strings.HasPrefix(msg, "insert canonical tree"):
		return "insert_canonical_tree"
	case strings.HasPrefix(msg, "reattach overrides for screen"):
		return "reattach_overrides"
	case strings.HasPrefix(msg, "insert screen_modes"):
		return "insert_screen_modes"
	case strings.HasPrefix(msg, "flip view_ready"):
		return "flip_view_ready"
	case strings.HasPrefix(msg, "enqueue audit"):
		return "enqueue_audit_job"
	case strings.HasPrefix(msg, "commit:"):
		return "stage6_tx_commit"
	default:
		return "unknown"
	}
}
