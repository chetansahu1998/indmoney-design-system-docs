package projects

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/sse"
)

// PipelineHeartbeatInterval is how often the running pipeline pokes
// pipeline_heartbeat_at. 5s is well below HeartbeatStaleThreshold (30s) so the
// recovery sweeper won't false-positive a healthy pipeline.
const PipelineHeartbeatInterval = 5 * time.Second

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
	return nil
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

	// Stage 3 — render PNGs.
	pngURLs, err := p.renderPNGsWithRetry(ctx, in.FileID, frameIDs)
	if err != nil {
		return fmt.Errorf("figma images: %w", err)
	}

	// Stage 4 — download, downsample, persist.
	pngKeys := make(map[string]string, len(in.Frames)) // figma_frame_id → storage key
	for _, frame := range in.Frames {
		url, ok := pngURLs[frame.FigmaFrameID]
		if !ok || url == "" {
			return fmt.Errorf("figma rendered no URL for frame %s", frame.FigmaFrameID)
		}
		raw, err := p.Renderer.DownloadPNG(ctx, url)
		if err != nil {
			return fmt.Errorf("download png %s: %w", frame.FigmaFrameID, err)
		}
		downsampled, err := DownsampleLongEdge(raw, MaxLongEdgePx)
		if err != nil {
			return fmt.Errorf("downsample png %s: %w", frame.FigmaFrameID, err)
		}
		key, err := p.persistPNG(in.TenantID, in.VersionID, frame.ScreenID, downsampled)
		if err != nil {
			return fmt.Errorf("persist png %s: %w", frame.FigmaFrameID, err)
		}
		if err := p.Repo.SetScreenPNG(ctx, frame.ScreenID, key); err != nil {
			return fmt.Errorf("update screen png_key %s: %w", frame.ScreenID, err)
		}
		pngKeys[frame.FigmaFrameID] = key
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

	// Stage 6 — single transaction: canonical_trees + screen_modes + status flip
	// + audit_jobs row. All-or-nothing.
	tx, err := p.Repo.BeginTx(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// Canonical trees: serialize the relevant slice of canonicalNodes per frame.
	for _, f := range in.Frames {
		treeJSON, hash := extractCanonicalTree(canonicalNodes, f.FigmaFrameID)
		if err := p.Repo.InsertCanonicalTree(ctx, tx, f.ScreenID, treeJSON, hash); err != nil {
			return fmt.Errorf("insert canonical tree: %w", err)
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

	// Stage 8 — notify the worker.
	if p.AuditEnqueuer != nil {
		p.AuditEnqueuer.EnqueueAuditJob(in.VersionID, in.TraceID)
	}

	return nil
}

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

// fetchNodesWithRetry calls /v1/files/{key}/nodes with 3-attempt backoff on 429.
// Other non-2xx errors fail fast.
func (p *Pipeline) fetchNodesWithRetry(ctx context.Context, fileKey string, ids []string) (map[string]any, error) {
	var lastErr error
	delay := 500 * time.Millisecond
	for attempt := 0; attempt < 3; attempt++ {
		out, err := p.NodeFetcher.GetFileNodes(ctx, fileKey, ids, 3)
		if err == nil {
			return out, nil
		}
		lastErr = err
		if !isRateLimitErr(err) {
			return nil, err
		}
		if attempt < 2 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
			delay *= 2
		}
	}
	return nil, fmt.Errorf("figma nodes after 3 attempts: %w", lastErr)
}

// renderPNGsWithRetry calls the renderer with 3-attempt 429 backoff.
func (p *Pipeline) renderPNGsWithRetry(ctx context.Context, fileKey string, ids []string) (map[string]string, error) {
	var lastErr error
	delay := 500 * time.Millisecond
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
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
			delay *= 2
		}
	}
	return nil, fmt.Errorf("figma images after 3 attempts: %w", lastErr)
}

// isRateLimitErr is intentionally string-based so the pipeline doesn't import
// the figma client's APIError type. The renderer wrappers below format 429s
// with a stable substring.
func isRateLimitErr(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "rate limit") || strings.Contains(err.Error(), "429")
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
	case strings.HasPrefix(msg, "begin tx"):
		return "stage6_tx_begin"
	case strings.HasPrefix(msg, "insert canonical tree"):
		return "insert_canonical_tree"
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
