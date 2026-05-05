package projects

import (
	"archive/zip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/auth"
)

// U4 — Figma asset export proxy + cache.
//
// Server-side wrapper around `/v1/images/<file>?ids=<csv>&format={png|svg}&scale={1|2|3}`
// that lets the leaf canvas export atomic Figma children (icons, illustrations,
// glyphs as SVG) without leaking the per-tenant Figma PAT to the browser.
//
// Lifecycle per call:
//
//   1. Resolve (file_id, version_index) from the leaf (flow → project → latest version).
//   2. For each requested node_id, look up asset_cache; collect cache hits.
//   3. For misses, batch through Client.GetImages (chunk size 80, mirroring
//      pipeline.go renderChunk's render-budget guard) under figmaProxyLimiter
//      to get pre-signed Figma CDN URLs.
//   4. Download each URL's bytes under a SEPARATE rate-limit bucket (50 req/s)
//      since CDN GETs are cheap relative to /v1/images render slots.
//   5. Persist bytes to data/assets/<tenant>/<file>/<version>/<node>.<format>
//      atomically (.tmp → os.Rename, mirroring Pipeline.persistPNG).
//   6. INSERT the asset_cache row.
//   7. Return [{node_id, storage_key, mime}] in the order requested.
//
// Errors at any step return early WITHOUT poisoning the cache — only
// successfully downloaded + persisted assets get rows. A 5xx on render or a
// download failure yields an error; the next call re-attempts cleanly.
//
// HTTP layer (signed-token-protected GET of the storage_key) is U5's scope.

// AssetExportResult is one rendered + persisted asset.
type AssetExportResult struct {
	NodeID     string
	StorageKey string
	Mime       string
}

// FigmaImageURLFetcher abstracts /v1/images so tests can stub it without
// hitting Figma.
type FigmaImageURLFetcher interface {
	GetImages(ctx context.Context, fileKey string, nodeIDs []string, format string, scale int) (map[string]string, error)
}

// AssetByteFetcher downloads the bytes at a pre-signed CDN URL. Stubbable.
type AssetByteFetcher interface {
	Fetch(ctx context.Context, url string) ([]byte, error)
}

// HTTPAssetByteFetcher is the production AssetByteFetcher.
type HTTPAssetByteFetcher struct {
	Client *http.Client
}

// Fetch implements AssetByteFetcher.
func (h *HTTPAssetByteFetcher) Fetch(ctx context.Context, url string) ([]byte, error) {
	c := h.Client
	if c == nil {
		// Mirrors Pipeline.HTTPFigmaRenderer's 5-min cap: large SVGs are tiny
		// but PNG@3x of a full-frame export can be ~30 MB and we don't want
		// to strand a slow CDN forever.
		c = &http.Client{Timeout: 5 * time.Minute}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("asset download: 429 rate limit")
	}
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("asset download: HTTP %d — %s", resp.StatusCode, string(body))
	}
	// 200 MB cap — generous: a full-frame PNG@3x export of a complex screen
	// runs ~20-40 MB; SVG is single-digit KB. 200 MB is the same upper-bound
	// the screens pipeline uses for the largest possible Figma render.
	bs, err := io.ReadAll(io.LimitReader(resp.Body, 200<<20))
	if err != nil {
		return nil, err
	}
	return bs, nil
}

// AssetExporter is the long-lived component that owns the cache repo,
// data-dir, Figma URL fetcher, byte fetcher, and rate-limiter. Constructed
// once per server boot; RenderAssetsForLeaf is called per request.
type AssetExporter struct {
	Repo      *TenantRepo
	URLs      FigmaImageURLFetcher  // /v1/images
	Bytes     AssetByteFetcher      // CDN GET
	DataDir   string                // data/ root, same convention as Pipeline.DataDir
	Limiter   *figmaRateLimiter     // shared with HandleFigmaFrameMetadata
	Now       func() time.Time      // injectable clock for tests
	ChunkSize int                   // 0 → AssetExportChunkSize default
}

// AssetExportChunkSize bounds the number of node_ids per /v1/images call. Matches
// the plan's "chunk size 80" target — Figma's per-request render budget for
// asset exports (icons, vectors, small frames) is much higher than for full
// frame PNGs (handled at 25 in pipeline.go), so 80 stays well under any real
// 400-render-timeout response while cutting URL-fetch round-trips ~3x vs 25.
const AssetExportChunkSize = 80

// AssetExportURLBurst / AssetExportURLRefill — per-tenant token bucket for
// /v1/images URL-fetch calls. 5 burst @ 1 token / 200 ms ≈ 5 req/s sustained,
// reusing the existing FigmaProxyBurstSize / FigmaProxyRefillEvery values so
// asset-export and frame-metadata calls share a single tenant-wide budget
// (Figma's PAT-level rate limit is 60 req/min ~ 1 req/s; 5 burst absorbs
// designer-driven spikes without exceeding the upstream cap).
const (
	AssetExportURLBurst    = FigmaProxyBurstSize
	AssetExportURLRefill   = FigmaProxyRefillEvery
	AssetExportBytesBurst  = 50
	AssetExportBytesRefill = 20 * time.Millisecond // 50 req/s
)

// assetByteLimiter is a separate token bucket for CDN GETs, per-tenant. The
// /v1/images URL-fetch and the bytes download have very different cost
// profiles (Figma render slots vs. CDN cache hits) so they shouldn't share
// a budget — see plan U4 "bytes downloads count separately at 50 req/sec".
//
// We keep this distinct from figmaProxyLimiter so /v1/images ratelimit
// state (used by the legacy frame-metadata proxy too) isn't drained by
// large bulk-export downloads.
var assetByteLimiter = &figmaRateLimiter{
	buckets: map[string]*figmaBucket{},
}

// errFigmaNoSVGForNode is returned when Figma's response for an SVG export
// omits the requested node — most often because the node type can't be
// rendered as SVG (RECTANGLE with effects, RASTER fills, masked groups).
// The error wraps the offending node_id so callers can surface it to the user.
type errFigmaNoSVGForNode struct {
	nodeID string
	format string
}

func (e *errFigmaNoSVGForNode) Error() string {
	return fmt.Sprintf("figma: no %s render for node_id=%q", e.format, e.nodeID)
}

// IsAssetExportNodeMissing reports whether err is a "Figma returned no asset
// for this node" error. Useful for HTTP layer (U5) to map to 422.
func IsAssetExportNodeMissing(err error) bool {
	var e *errFigmaNoSVGForNode
	return errors.As(err, &e)
}

// RenderAssetsForLeaf is the U4 entry point. Resolves leafID → (file_id,
// version_index), then per-node: cache lookup → miss → fetch URL → download
// bytes → persist → insert cache row. Returns one result per requested node
// in the original order.
//
// On any error mid-batch, returns the error and any results already produced
// stay persisted (the cache survives partial calls; the error is surfaced
// to U5's HTTP layer which decides whether to retry).
//
// tenantID is required; the Repo's tenant filter handles row-level scoping.
func (a *AssetExporter) RenderAssetsForLeaf(ctx context.Context, tenantID, leafID string, nodeIDs []string, format string, scale int) ([]AssetExportResult, error) {
	if a == nil {
		return nil, errors.New("asset exporter: nil receiver")
	}
	if a.Repo == nil {
		return nil, errors.New("asset exporter: nil repo")
	}
	if a.URLs == nil {
		return nil, errors.New("asset exporter: nil url fetcher")
	}
	if a.Bytes == nil {
		return nil, errors.New("asset exporter: nil byte fetcher")
	}
	if a.DataDir == "" {
		return nil, errors.New("asset exporter: DataDir not configured")
	}
	if tenantID == "" {
		return nil, errors.New("asset exporter: tenantID required")
	}
	if leafID == "" {
		return nil, errors.New("asset exporter: leafID required")
	}
	if len(nodeIDs) == 0 {
		return nil, errors.New("asset exporter: empty nodeIDs")
	}
	if format != "png" && format != "svg" {
		return nil, fmt.Errorf("asset exporter: unsupported format %q (want png|svg)", format)
	}
	if scale < 1 || scale > 3 {
		return nil, fmt.Errorf("asset exporter: unsupported scale %d (want 1|2|3)", scale)
	}

	// Resolve leaf → file_id, version_index.
	fileID, versionIndex, err := a.Repo.LookupLeafFigmaContext(ctx, leafID)
	if err != nil {
		return nil, fmt.Errorf("asset exporter: resolve leaf: %w", err)
	}

	mime := mimeForAssetFormat(format)
	limiter := a.Limiter
	if limiter == nil {
		limiter = figmaProxyLimiter
	}
	chunkSize := a.ChunkSize
	if chunkSize <= 0 {
		chunkSize = AssetExportChunkSize
	}

	// Collect cache hits + remaining misses preserving caller order.
	results := make([]AssetExportResult, len(nodeIDs))
	resultIdx := make(map[string]int, len(nodeIDs))
	misses := make([]string, 0, len(nodeIDs))
	for i, nid := range nodeIDs {
		resultIdx[nid] = i
		hit, ok, err := a.Repo.LookupAsset(ctx, tenantID, fileID, nid, format, scale, versionIndex)
		if err != nil {
			return nil, fmt.Errorf("asset exporter: cache lookup %s: %w", nid, err)
		}
		if ok {
			results[i] = AssetExportResult{NodeID: nid, StorageKey: hit.StorageKey, Mime: hit.Mime}
			continue
		}
		misses = append(misses, nid)
	}
	if len(misses) == 0 {
		return results, nil
	}

	// Fetch URLs in chunks. Per-chunk: rate-limit, then GetImages with
	// 3-attempt 429 backoff (mirrors pipeline.go renderChunk).
	urlMap := make(map[string]string, len(misses))
	for i := 0; i < len(misses); i += chunkSize {
		j := i + chunkSize
		if j > len(misses) {
			j = len(misses)
		}
		chunk := misses[i:j]
		if err := limiter.wait(ctx, tenantID, AssetExportURLBurst, AssetExportURLRefill); err != nil {
			return nil, fmt.Errorf("asset exporter: rate limit wait: %w", err)
		}
		// Stash tenantID on ctx so a tenant-aware URL fetcher
		// (production wiring uses figmaPATResolver) can pick it up
		// without changing the FigmaImageURLFetcher interface.
		fetchCtx := withAssetExportTenant(ctx, tenantID)
		got, err := fetchImagesWithRetry(fetchCtx, a.URLs, fileID, chunk, format, scale)
		if err != nil {
			return nil, fmt.Errorf("asset exporter: fetch images: %w", err)
		}
		for k, v := range got {
			urlMap[k] = v
		}
	}

	// Download bytes per node, persist, insert cache row.
	now := a.Now
	if now == nil {
		now = time.Now
	}
	for _, nid := range misses {
		u, ok := urlMap[nid]
		if !ok || u == "" {
			return nil, &errFigmaNoSVGForNode{nodeID: nid, format: format}
		}
		if err := assetByteLimiter.wait(ctx, tenantID, AssetExportBytesBurst, AssetExportBytesRefill); err != nil {
			return nil, fmt.Errorf("asset exporter: bytes rate limit wait: %w", err)
		}
		bs, err := a.Bytes.Fetch(ctx, u)
		if err != nil {
			return nil, fmt.Errorf("asset exporter: download %s: %w", nid, err)
		}
		key, err := persistAssetBytes(a.DataDir, tenantID, fileID, versionIndex, nid, format, bs)
		if err != nil {
			return nil, fmt.Errorf("asset exporter: persist %s: %w", nid, err)
		}
		row := AssetCacheRow{
			TenantID:     tenantID,
			FileID:       fileID,
			NodeID:       nid,
			Format:       format,
			Scale:        scale,
			VersionIndex: versionIndex,
			StorageKey:   key,
			Bytes:        int64(len(bs)),
			Mime:         mime,
			CreatedAt:    now().UTC(),
		}
		if err := a.Repo.StoreAsset(ctx, row); err != nil {
			return nil, fmt.Errorf("asset exporter: store cache row %s: %w", nid, err)
		}
		results[resultIdx[nid]] = AssetExportResult{NodeID: nid, StorageKey: key, Mime: mime}
	}
	return results, nil
}

// fetchImagesWithRetry calls URLs.GetImages with up to 3 attempts on 429
// (mirrors pipeline.go renderPNGsWithRetry / renderChunk's pattern). The
// retry honours Retry-After when surfaced via the figma client APIError.
func fetchImagesWithRetry(ctx context.Context, fetcher FigmaImageURLFetcher, fileKey string, nodeIDs []string, format string, scale int) (map[string]string, error) {
	var lastErr error
	delay := 500 * time.Millisecond
	for attempt := 0; attempt < 3; attempt++ {
		out, err := fetcher.GetImages(ctx, fileKey, nodeIDs, format, scale)
		if err == nil {
			return out, nil
		}
		lastErr = err
		if !isRateLimitErr(err) {
			return nil, err
		}
		// Honour explicit Retry-After when the error carries it.
		retry := delay
		if ra, ok := retryAfterFromErr(err); ok && ra > 0 {
			retry = ra
		}
		if attempt < 2 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(retry):
			}
			delay *= 2
		}
	}
	return nil, fmt.Errorf("figma images after 3 attempts: %w", lastErr)
}

// retryAfterFromErr peeks at a Figma APIError-shaped error for an explicit
// Retry-After header value. We use errors.As against a duck-typed interface
// rather than importing figma/client to keep the dependency one-way.
//
// Concrete *figma/client.APIError exposes RetryAfter as a public field; we
// expose a method-shaped probe via an interface so future error types can
// opt in by adding `RetryAfter() time.Duration` without changing this code.
func retryAfterFromErr(err error) (time.Duration, bool) {
	// Method-shaped (preferred): caller exposes RetryAfter() time.Duration.
	type retryAfterMethod interface {
		RetryAfter() time.Duration
	}
	var m retryAfterMethod
	if errors.As(err, &m) {
		if d := m.RetryAfter(); d > 0 {
			return d, true
		}
	}
	// Field-shaped (figma/client.APIError today): we type-assert through a
	// small surface that exposes both IsRateLimit() and a RetryAfter field
	// reachable via errors.As to a *struct{...} clone. Skipped to avoid
	// importing figma/client; pipeline.go uses the same ladder fallback.
	return 0, false
}

// persistAssetBytes writes the asset to data/assets/<tenant>/<file>/<vn>/<node>.<format>
// atomically (.tmp → os.Rename). Returns the storage key (relative path under
// DataDir, mirroring screens.png_storage_key's convention).
func persistAssetBytes(dataDir, tenantID, fileID string, versionIndex int, nodeID, format string, data []byte) (string, error) {
	relDir := filepath.Join("assets", tenantID, fileID, fmt.Sprintf("v%d", versionIndex))
	absDir := filepath.Join(dataDir, relDir)
	if err := os.MkdirAll(absDir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir: %w", err)
	}
	relPath := filepath.Join(relDir, sanitizeNodeIDForFS(nodeID)+"."+format)
	absPath := filepath.Join(dataDir, relPath)
	tmp := absPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, absPath); err != nil {
		return "", err
	}
	return relPath, nil
}

// sanitizeNodeIDForFS replaces filesystem-hostile characters in a Figma
// node-id ("X:Y" canonical form) with safe ones. We use ':' → '_' so the
// reverse mapping isn't necessary (the cache row preserves the canonical
// form; the storage_key is opaque to consumers).
func sanitizeNodeIDForFS(nodeID string) string {
	s := strings.ReplaceAll(nodeID, ":", "_")
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, "\\", "_")
	s = strings.ReplaceAll(s, "..", "_")
	return s
}

// mimeForAssetFormat maps the format axis to the right Content-Type.
// PNG → image/png, SVG → image/svg+xml. Anything else is a programming error
// (already validated upstream).
func mimeForAssetFormat(format string) string {
	switch format {
	case "png":
		return "image/png"
	case "svg":
		return "image/svg+xml"
	default:
		return "application/octet-stream"
	}
}

// ─── Repo helpers ───────────────────────────────────────────────────────────

// AssetCacheRow is the wire shape between RenderAssetsForLeaf and the repo.
type AssetCacheRow struct {
	TenantID     string
	FileID       string
	NodeID       string
	Format       string
	Scale        int
	VersionIndex int
	StorageKey   string
	Bytes        int64
	Mime         string
	CreatedAt    time.Time
}

// LookupAsset reads a single asset_cache row by its full PK. Returns
// (row, true, nil) on hit, (zero, false, nil) on miss, or (zero, false, err)
// on db failure.
func (t *TenantRepo) LookupAsset(ctx context.Context, tenantID, fileID, nodeID, format string, scale, versionIndex int) (AssetCacheRow, bool, error) {
	if t == nil {
		return AssetCacheRow{}, false, errors.New("nil repo")
	}
	// We accept tenantID as an explicit param so callers can be defensive at
	// the boundary; for safety we still cross-check against the repo's bound
	// tenant when configured (avoids cross-tenant reads via a stray param).
	if t.tenantID != "" && tenantID != t.tenantID {
		return AssetCacheRow{}, false, fmt.Errorf("tenant mismatch: repo=%s arg=%s", t.tenantID, tenantID)
	}
	row := t.handle().QueryRowContext(ctx, `
		SELECT storage_key, bytes, mime, created_at
		  FROM asset_cache
		 WHERE tenant_id = ? AND file_id = ? AND node_id = ?
		   AND format = ? AND scale = ? AND version_index = ?
	`, tenantID, fileID, nodeID, format, scale, versionIndex)
	var r AssetCacheRow
	var createdAt string
	if err := row.Scan(&r.StorageKey, &r.Bytes, &r.Mime, &createdAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AssetCacheRow{}, false, nil
		}
		return AssetCacheRow{}, false, err
	}
	r.TenantID = tenantID
	r.FileID = fileID
	r.NodeID = nodeID
	r.Format = format
	r.Scale = scale
	r.VersionIndex = versionIndex
	r.CreatedAt = parseTime(createdAt)
	return r, true, nil
}

// StoreAsset inserts (or replaces) an asset_cache row. INSERT OR REPLACE so
// re-export under the same PK overwrites the prior storage_key cleanly —
// the prior file may already be replaced on disk by the caller, but we
// don't try to garbage-collect the stale path here (out of U4 scope).
func (t *TenantRepo) StoreAsset(ctx context.Context, r AssetCacheRow) error {
	if t == nil {
		return errors.New("nil repo")
	}
	if t.tenantID != "" && r.TenantID != t.tenantID {
		return fmt.Errorf("tenant mismatch: repo=%s row=%s", t.tenantID, r.TenantID)
	}
	createdAt := rfc3339(r.CreatedAt)
	if r.CreatedAt.IsZero() {
		createdAt = rfc3339(t.now().UTC())
	}
	_, err := t.handle().ExecContext(ctx, `
		INSERT OR REPLACE INTO asset_cache
		    (tenant_id, file_id, node_id, format, scale, version_index,
		     storage_key, bytes, mime, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, r.TenantID, r.FileID, r.NodeID, r.Format, r.Scale, r.VersionIndex,
		r.StorageKey, r.Bytes, r.Mime, createdAt)
	return err
}

// LookupLeafFigmaContext resolves a leafID (≡ flow.id) to the (file_id,
// version_index) pair the asset_cache PK needs.
//
// version_index is the latest project_versions row for the flow's project —
// re-imports advance the index, which the cache PK keys on, so the next
// render after a re-import correctly cache-misses.
func (t *TenantRepo) LookupLeafFigmaContext(ctx context.Context, leafID string) (string, int, error) {
	if t == nil {
		return "", 0, errors.New("nil repo")
	}
	if t.tenantID == "" {
		return "", 0, errors.New("projects: tenant_id required")
	}
	row := t.handle().QueryRowContext(ctx, `
		SELECT f.file_id,
		       COALESCE((SELECT MAX(v.version_index)
		                   FROM project_versions v
		                  WHERE v.project_id = f.project_id
		                    AND v.tenant_id = ?), 0) AS version_index
		  FROM flows f
		 WHERE f.id = ? AND f.tenant_id = ? AND f.deleted_at IS NULL
	`, t.tenantID, leafID, t.tenantID)
	var fileID string
	var versionIndex int
	if err := row.Scan(&fileID, &versionIndex); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", 0, ErrNotFound
		}
		return "", 0, err
	}
	return fileID, versionIndex, nil
}

// ─── U5 — HTTP layer (single + bulk asset download) ─────────────────────────
//
// Four endpoints:
//
//	POST /v1/projects/{slug}/assets/export-url      — mint a signed `?at=<token>`
//	                                                   for a single (file_id, node_id, format, scale)
//	GET  /v1/projects/{slug}/assets/{node_id}        — verify token, look up
//	                                                   cache (or render
//	                                                   on-demand <=5s),
//	                                                   stream bytes
//	POST /v1/projects/{slug}/assets/bulk-export     — batch RenderAssetsForLeaf,
//	                                                   register a one-shot
//	                                                   bulk_id, return signed
//	                                                   `download_url`
//	GET  /v1/projects/{slug}/assets/bulk/{token}     — verify, stream zip
//
// Token strategy: the existing AssetTokenSigner.Mint(tenantID, screenID, ttl)
// signs `(tenant|screen|expires)`. We repurpose `screenID` as a composite
// "asset key":
//
//	single → "<file_id>|<node_id>|<format>|<scale>"
//	bulk   → "bulk:<bulk_id>"
//
// The verify path reconstructs the same composite and the HMAC fails closed
// on any mismatch.
//
// Audit log: every successfully-streamed asset (single OR per-asset inside a
// bulk) writes an `asset.exported` row. Bulk exports share a `bulk_id` so
// post-hoc analytics can re-aggregate them, mirroring HandleBulkAcknowledge.

// AssetExportTokenTTL — single-asset signed-URL lifetime. Matches asset.token's
// 60s default for screen PNGs (Pr8) — short enough that a leaked log entry is
// mostly harmless, long enough that <img src=…> retries don't fall off a cliff.
const AssetExportTokenTTL = 60 * time.Second

// BulkExportTokenTTL — one-shot zip-download lifetime. The plan caps at 5 min:
// "download_url expires in 5 min." Long enough for a slow connection on a
// 100-icon zip, short enough that the in-memory bulk registry stays bounded.
const BulkExportTokenTTL = 5 * time.Minute

// MaxBulkAssetExportRows — body cap on the bulk endpoint. Plan: "Body size cap
// on bulk: max 100 node_ids per request."
const MaxBulkAssetExportRows = 100

// MaxBulkZipBufferBytes — when assembling the in-memory zip, refuse to keep
// more than this much in RAM. Plan: "Stream zips (don't buffer in memory
// beyond ~10 MB)." Above the cap, we spool to a temp file before serving.
const MaxBulkZipBufferBytes = 10 << 20

// MaxBulkZipTotalBytes — overall ceiling on a single bulk zip. Defends
// against a designer accidentally requesting 200 huge full-frame PNGs and
// stranding the server's tempdir.
const MaxBulkZipTotalBytes = 256 << 20

// isAcceptedAssetFormat reports whether the given asset format string is
// recognised by the mint + download endpoints. The legacy node-render
// formats (png/svg) plus the preview-pyramid tiers from migration 0021
// are accepted; anything else is a 400.
func isAcceptedAssetFormat(format string) bool {
	if format == "png" || format == "svg" {
		return true
	}
	_, ok := ParsePreviewTierFormat(format)
	return ok
}

// AssetCacheRetryAfterSeconds — Retry-After header value when a single-asset
// download must surface a 425 because the synchronous render budget elapses
// before the asset materialises.
const AssetCacheRetryAfterSeconds = 5

// SingleAssetSyncRenderBudget bounds the on-demand render path inside
// HandleAssetDownload. With 30+ standalone-shape requests per leaf racing
// Figma's 5-req/sec PAT cap, a 5-second budget loses for the majority of
// shapes (30 * 200ms minimum spacing = 6 seconds for the LAST shape just
// to dispatch). Bumped to 30s — tradeoff: HTTP threads are tied up
// longer for slow renders, but designers see actual icons instead of
// 90% placeholder canvases. The frontend's img-onError retry-with-
// backoff (nodeToHTML.ts) absorbs the rare cases that still time out.
const SingleAssetSyncRenderBudget = 30 * time.Second

// ─── Bulk export registry ────────────────────────────────────────────────────

// bulkExportEntry tracks a pending bulk download. Stored in process memory
// keyed by bulk_id; a 5-minute TTL evicts stale entries on the next access.
// Each entry records the sealed zip data (or temp-file path) plus the audit
// metadata to write per-asset audit_log rows when the GET is served.
type bulkExportEntry struct {
	tenantID    string
	leafID      string
	projectSlug string
	bulkID      string
	format      string
	scale       int
	results     []AssetExportResult // per-node order matches request
	nodeNames   map[string]string   // node_id → friendly name (best-effort)
	dataDir     string
	expiresAt   time.Time
	zipBytes    []byte // populated when zip <= MaxBulkZipBufferBytes
	zipPath     string // populated when zip is spooled to disk (large)
	zipSize     int64
	actorUserID string
}

// bulkExportRegistry is the in-process store of bulk-export staging data.
// Single-instance is fine for Phase 1: bulk URLs are issued + consumed by
// the same designer in a tight loop; we don't need cross-replica continuity.
type bulkExportRegistry struct {
	mu      sync.Mutex
	entries map[string]*bulkExportEntry
}

func newBulkExportRegistry() *bulkExportRegistry {
	return &bulkExportRegistry{entries: map[string]*bulkExportEntry{}}
}

func (b *bulkExportRegistry) Put(e *bulkExportEntry) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.entries[e.bulkID] = e
	// Opportunistic GC: sweep expired entries on every put.
	now := time.Now()
	for k, v := range b.entries {
		if now.After(v.expiresAt) {
			cleanupBulkEntry(v)
			delete(b.entries, k)
		}
	}
}

func (b *bulkExportRegistry) Take(bulkID string) (*bulkExportEntry, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	e, ok := b.entries[bulkID]
	if !ok {
		return nil, false
	}
	if time.Now().After(e.expiresAt) {
		cleanupBulkEntry(e)
		delete(b.entries, bulkID)
		return nil, false
	}
	// One-shot — remove on take.
	delete(b.entries, bulkID)
	return e, true
}

func cleanupBulkEntry(e *bulkExportEntry) {
	if e == nil {
		return
	}
	if e.zipPath != "" {
		_ = os.Remove(e.zipPath)
	}
}

// ─── Server hookup ──────────────────────────────────────────────────────────

// assetExportSlug derives the project slug component used as the file-name
// prefix of downloaded assets (`<flow-slug>__<node-name>.<ext>`). The plan
// specifies "flow-slug = the project slug", so we use the URL slug directly.

// HandleMintAssetExportToken serves
//
//	POST /v1/projects/{slug}/assets/export-url
//
// JSON body: `{ "node_id": "...", "format": "png|svg", "scale": 1|2|3 }`.
// Response: `{ "url": "...?at=<token>", "expires_in": 60 }`.
//
// Verifies the caller has access to the project (via tenant_id resolved from
// JWT + slug lookup) before minting. The token binds (tenant, file_id,
// node_id, format, scale) so a leaked URL only exposes the one asset.
func (s *Server) HandleMintAssetExportToken() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		claims, _ := ctx.Value(ctxKeyClaims).(*auth.Claims)
		if claims == nil {
			writeJSONErr(w, http.StatusUnauthorized, "unauthorized", "missing claims")
			return
		}
		tenantID := s.resolveTenantID(claims)
		if tenantID == "" {
			writeJSONErr(w, http.StatusForbidden, "no_tenant", "")
			return
		}
		if s.deps.AssetSigner == nil {
			writeJSONErr(w, http.StatusServiceUnavailable, "asset_signer", "not configured")
			return
		}
		slug := r.PathValue("slug")
		if slug == "" {
			writeJSONErr(w, http.StatusBadRequest, "missing_slug", "")
			return
		}

		var req struct {
			NodeID string `json:"node_id"`
			Format string `json:"format"`
			Scale  int    `json:"scale"`
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 8*1024))
		if err != nil {
			writeJSONErr(w, http.StatusBadRequest, "read_body", err.Error())
			return
		}
		if err := json.Unmarshal(body, &req); err != nil {
			writeJSONErr(w, http.StatusBadRequest, "decode", err.Error())
			return
		}
		if req.NodeID == "" {
			writeJSONErr(w, http.StatusBadRequest, "invalid_payload", "node_id required")
			return
		}
		if !isAcceptedAssetFormat(req.Format) {
			writeJSONErr(w, http.StatusBadRequest, "invalid_payload", "format must be png|svg|preview-128|preview-512|preview-1024|preview-2048")
			return
		}
		if _, isPreview := ParsePreviewTierFormat(req.Format); isPreview {
			// Preview tiers are content-addressed by tier size only — scale is
			// always 1 because the tier IS the resolution. Coerce silently so
			// callers don't have to know the convention.
			req.Scale = 1
		}
		if req.Scale < 1 || req.Scale > 3 {
			writeJSONErr(w, http.StatusBadRequest, "invalid_payload", "scale must be 1|2|3")
			return
		}

		// Tenant scoping: the project must belong to this tenant. Cross-tenant
		// reads return 404 (no existence oracle) — same posture as the PNG
		// route.
		repo := NewTenantRepo(s.deps.DB.DB, tenantID)
		project, err := repo.GetProjectBySlug(ctx, slug)
		if errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			writeJSONErr(w, http.StatusInternalServerError, "lookup", err.Error())
			return
		}

		composite := singleAssetTokenKey(project.FileID, req.NodeID, req.Format, req.Scale)
		token := s.deps.AssetSigner.Mint(tenantID, composite, AssetExportTokenTTL)
		writeJSON(w, http.StatusOK, map[string]any{
			"url": fmt.Sprintf("/v1/projects/%s/assets/%s?format=%s&scale=%d&at=%s",
				slug, req.NodeID, req.Format, req.Scale, token),
			"expires_in": int(AssetExportTokenTTL.Seconds()),
		})
	}
}

// HandleAssetDownload serves
//
//	GET /v1/projects/{slug}/assets/{node_id}?format=&scale=&at=<token>
//
// Verifies the token (HMAC-bound to tenant + file_id + node_id + format +
// scale), looks up the asset cache, and streams the bytes with the correct
// Content-Type and a Content-Disposition: attachment filename built from
// `<slug>__<sanitized-node-name>.<ext>`. On cache miss, attempts a
// synchronous render with a 5-second budget; if that elapses, returns 425
// with a Retry-After hint.
func (s *Server) HandleAssetDownload() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		slug := r.PathValue("slug")
		nodeID := r.PathValue("node_id")
		format := r.URL.Query().Get("format")
		scaleStr := r.URL.Query().Get("scale")
		at := r.URL.Query().Get("at")

		if slug == "" || nodeID == "" || format == "" || scaleStr == "" {
			writeJSONErr(w, http.StatusBadRequest, "missing_params", "slug,node_id,format,scale required")
			return
		}
		if !isAcceptedAssetFormat(format) {
			writeJSONErr(w, http.StatusBadRequest, "invalid_payload", "format must be png|svg|preview-128|preview-512|preview-1024|preview-2048")
			return
		}
		previewTier, isPreview := ParsePreviewTierFormat(format)
		scale, err := strconv.Atoi(scaleStr)
		if err != nil {
			writeJSONErr(w, http.StatusBadRequest, "invalid_payload", "scale must be 1|2|3")
			return
		}
		if isPreview {
			// Coerce scale to 1 for preview-tier requests — matches the
			// mint-side coercion above so token verification lines up.
			scale = 1
		} else if scale < 1 || scale > 3 {
			writeJSONErr(w, http.StatusBadRequest, "invalid_payload", "scale must be 1|2|3")
			return
		}
		if at == "" {
			writeJSONErr(w, http.StatusUnauthorized, "missing_token", "?at= required")
			return
		}
		if s.deps.AssetSigner == nil {
			writeJSONErr(w, http.StatusServiceUnavailable, "asset_signer", "not configured")
			return
		}

		// We don't know which tenant from the URL alone — but the project's
		// tenant is derivable from the slug (slugs are unique per tenant
		// scope; a cross-tenant slug collision yields 404 below since the
		// query joins on tenant_id implicitly via the row's tenant column).
		tenantID, fileID, projectSlug, err := s.lookupProjectTenantBySlug(ctx, slug)
		if err != nil || tenantID == "" {
			http.NotFound(w, r)
			return
		}
		// Verify the signed token against the same composite the mint used.
		composite := singleAssetTokenKey(fileID, nodeID, format, scale)
		if verifyErr := s.deps.AssetSigner.Verify(at, tenantID, composite); verifyErr != nil {
			status := assetTokenErrToStatus(verifyErr)
			writeJSONErr(w, status, "asset_token", verifyErr.Error())
			return
		}

		// Cache lookup with a synchronous render fallback bounded by
		// SingleAssetSyncRenderBudget. The render path delegates to U4's
		// AssetExporter.RenderAssetsForLeaf — which honours the per-tenant
		// rate limiter so we never accidentally swing past Figma's quota.
		repo := NewTenantRepo(s.deps.DB.DB, tenantID)
		_, versionIndex, lerr := s.lookupVersionForFile(ctx, repo, fileID)
		if lerr != nil {
			writeJSONErr(w, http.StatusInternalServerError, "version_lookup", lerr.Error())
			return
		}

		row, ok, err := repo.LookupAsset(ctx, tenantID, fileID, nodeID, format, scale, versionIndex)
		if err != nil {
			writeJSONErr(w, http.StatusInternalServerError, "lookup", err.Error())
			return
		}
		if !ok && isPreview {
			// Preview-tier cache miss — generate the entire pyramid in one
			// pass so subsequent requests for any other tier hit cache. The
			// generator does ONE Figma render (PNG @ scale=2) and downsamples
			// locally to all four tiers, so render-budget cost is the same
			// as a single legacy /assets/<node>?format=png&scale=2 call.
			if s.deps.PreviewPyramid == nil {
				w.Header().Set("Retry-After", strconv.Itoa(AssetCacheRetryAfterSeconds))
				writeJSONErr(w, http.StatusTooEarly, "preview_pyramid_unavailable", "preview generator not wired")
				return
			}
			leafID, lerr := s.lookupAnyLeafForFile(ctx, repo, fileID)
			if lerr != nil || leafID == "" {
				http.NotFound(w, r)
				return
			}
			renderCtx, cancel := context.WithTimeout(ctx, SingleAssetSyncRenderBudget)
			pyramidResults, perr := s.deps.PreviewPyramid.RenderPreviewPyramid(renderCtx, tenantID, leafID, fileID, nodeID, versionIndex)
			cancel()
			if errors.Is(perr, context.DeadlineExceeded) {
				w.Header().Set("Retry-After", strconv.Itoa(AssetCacheRetryAfterSeconds))
				writeJSONErr(w, http.StatusTooEarly, "render_in_progress", "try again in a moment")
				return
			}
			// Persist every successfully-rendered tier — even if one tier
			// failed, the rest are valid cache rows.
			for _, pr := range pyramidResults {
				crow := AssetCacheRow{
					TenantID:     tenantID,
					FileID:       fileID,
					NodeID:       nodeID,
					Format:       pr.Tier.FormatString(),
					Scale:        1,
					VersionIndex: versionIndex,
					StorageKey:   pr.StorageKey,
					Bytes:        pr.Bytes,
					Mime:         pr.Mime,
					CreatedAt:    s.deps.PreviewPyramid.now(),
				}
				if perr := repo.StoreAsset(ctx, crow); perr != nil {
					// Disk-write succeeded but DB row failed → log via the
					// generic 500 path. Subsequent requests will refetch.
					writeJSONErr(w, http.StatusInternalServerError, "preview_persist", perr.Error())
					return
				}
			}
			if perr != nil && len(pyramidResults) == 0 {
				writeJSONErr(w, http.StatusBadGateway, "preview_render_failed", perr.Error())
				return
			}
			// Find the requested tier in the freshly-persisted set.
			for _, pr := range pyramidResults {
				if pr.Tier == previewTier {
					row = AssetCacheRow{
						StorageKey: pr.StorageKey,
						Mime:       pr.Mime,
					}
					ok = true
					break
				}
			}
			if !ok {
				// Generator partial-failed for the specific tier we wanted.
				http.NotFound(w, r)
				return
			}
		}
		if !ok {
			// Cache miss — try one synchronous render.
			if s.deps.AssetExporter == nil {
				w.Header().Set("Retry-After", strconv.Itoa(AssetCacheRetryAfterSeconds))
				writeJSONErr(w, http.StatusTooEarly, "render_unavailable", "asset exporter not wired")
				return
			}
			leafID, lerr := s.lookupAnyLeafForFile(ctx, repo, fileID)
			if lerr != nil || leafID == "" {
				http.NotFound(w, r)
				return
			}
			renderCtx, cancel := context.WithTimeout(ctx, SingleAssetSyncRenderBudget)
			results, rerr := s.tenantExporter(tenantID).RenderAssetsForLeaf(renderCtx, tenantID, leafID, []string{nodeID}, format, scale)
			cancel()
			if errors.Is(rerr, context.DeadlineExceeded) {
				w.Header().Set("Retry-After", strconv.Itoa(AssetCacheRetryAfterSeconds))
				writeJSONErr(w, http.StatusTooEarly, "render_in_progress", "try again in a moment")
				return
			}
			if IsAssetExportNodeMissing(rerr) {
				writeJSONErr(w, http.StatusUnprocessableEntity, "node_not_renderable", rerr.Error())
				return
			}
			if rerr != nil {
				writeJSONErr(w, http.StatusBadGateway, "render_failed", rerr.Error())
				return
			}
			if len(results) == 0 {
				http.NotFound(w, r)
				return
			}
			row = AssetCacheRow{
				StorageKey: results[0].StorageKey,
				Mime:       results[0].Mime,
			}
		}

		// Resolve storage key → absolute path under DataDir, with the same
		// path-traversal guard as HandleScreenPNG.
		baseDir, err := filepath.Abs(s.deps.DataDir)
		if err != nil {
			writeJSONErr(w, http.StatusInternalServerError, "abs_base", err.Error())
			return
		}
		fullPath, err := filepath.Abs(filepath.Join(baseDir, filepath.Clean(row.StorageKey)))
		if err != nil {
			writeJSONErr(w, http.StatusInternalServerError, "abs_full", err.Error())
			return
		}
		if !strings.HasPrefix(fullPath, baseDir+string(os.PathSeparator)) && fullPath != baseDir {
			writeJSONErr(w, http.StatusBadRequest, "path_traversal", "")
			return
		}
		f, err := os.Open(fullPath)
		if errors.Is(err, os.ErrNotExist) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			writeJSONErr(w, http.StatusInternalServerError, "open", err.Error())
			return
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil {
			writeJSONErr(w, http.StatusInternalServerError, "stat", err.Error())
			return
		}

		// Friendly file name: `<flow-slug>__<sanitized-node-name>.<ext>`.
		// We don't have a Figma-side "name" for the node here without a JOIN,
		// so we use the sanitized node_id as a reasonable default — designers
		// can rename in their browser. (The UI bulk path already passes
		// names through nodeNames; the single path uses the bare node_id.)
		filename := buildAssetFilename(projectSlug, nodeID, format)
		w.Header().Set("Content-Type", row.Mime)
		w.Header().Set("Content-Length", fmt.Sprintf("%d", st.Size()))
		w.Header().Set("Cache-Control", "private, max-age=300")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))

		// Best-effort audit-log row. Failure to log shouldn't block the
		// download — we log at debug and continue (mirrors the
		// HandleBulkAcknowledge pattern of preferring delivery over logging
		// on the read path).
		s.writeAssetExportAudit(ctx, r, tenantID, "", nodeID, fileID, format, scale)

		if _, err := io.Copy(w, f); err != nil {
			slog.Debug("asset download: copy failed (client disconnect?)", "err", err, "path", fullPath)
		}
	}
}

// HandleBulkAssetExport serves
//
//	POST /v1/projects/{slug}/assets/bulk-export
//
// Body: `{ "leaf_id": "...", "node_ids": [...], "format": "...", "scale": N }`.
// Renders each via U4's RenderAssetsForLeaf (cache-aware, rate-limited),
// assembles a zip in memory (or spools to a temp file when > 10 MB),
// registers the bulk under a random `bulk_id`, signs a one-shot URL bound
// to (tenant, "bulk:<bulk_id>"), and returns
// `{ "download_url": "...?at=<token>", "expires_in": 300 }`.
func (s *Server) HandleBulkAssetExport() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		claims, _ := ctx.Value(ctxKeyClaims).(*auth.Claims)
		if claims == nil {
			writeJSONErr(w, http.StatusUnauthorized, "unauthorized", "missing claims")
			return
		}
		tenantID := s.resolveTenantID(claims)
		if tenantID == "" {
			writeJSONErr(w, http.StatusForbidden, "no_tenant", "")
			return
		}
		if s.deps.AssetSigner == nil {
			writeJSONErr(w, http.StatusServiceUnavailable, "asset_signer", "not configured")
			return
		}
		if s.deps.AssetExporter == nil {
			writeJSONErr(w, http.StatusServiceUnavailable, "asset_exporter", "not configured")
			return
		}
		slug := r.PathValue("slug")
		if slug == "" {
			writeJSONErr(w, http.StatusBadRequest, "missing_slug", "")
			return
		}

		var req struct {
			LeafID  string   `json:"leaf_id"`
			NodeIDs []string `json:"node_ids"`
			Format  string   `json:"format"`
			Scale   int      `json:"scale"`
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 256*1024))
		if err != nil {
			writeJSONErr(w, http.StatusBadRequest, "read_body", err.Error())
			return
		}
		if err := json.Unmarshal(body, &req); err != nil {
			writeJSONErr(w, http.StatusBadRequest, "decode", err.Error())
			return
		}
		if req.LeafID == "" {
			writeJSONErr(w, http.StatusBadRequest, "invalid_payload", "leaf_id required")
			return
		}
		if len(req.NodeIDs) == 0 {
			writeJSONErr(w, http.StatusBadRequest, "invalid_payload", "node_ids required")
			return
		}
		if len(req.NodeIDs) > MaxBulkAssetExportRows {
			writeJSONErr(w, http.StatusBadRequest, "too_many_nodes",
				fmt.Sprintf("max %d node_ids per bulk request", MaxBulkAssetExportRows))
			return
		}
		if req.Format != "png" && req.Format != "svg" {
			writeJSONErr(w, http.StatusBadRequest, "invalid_payload", "format must be png|svg")
			return
		}
		if req.Scale < 1 || req.Scale > 3 {
			writeJSONErr(w, http.StatusBadRequest, "invalid_payload", "scale must be 1|2|3")
			return
		}

		// Tenant scope check.
		repo := NewTenantRepo(s.deps.DB.DB, tenantID)
		if _, err := repo.GetProjectBySlug(ctx, slug); err != nil {
			if errors.Is(err, ErrNotFound) {
				http.NotFound(w, r)
				return
			}
			writeJSONErr(w, http.StatusInternalServerError, "lookup", err.Error())
			return
		}

		// Render via U4. RenderAssetsForLeaf already paces requests through
		// figmaProxyLimiter so a 200-icon export back-pressures inside
		// `len(node_ids)/chunkSize` /v1/images calls without ever spiking
		// past Figma's per-tenant 5 req/sec budget.
		results, rerr := s.tenantExporter(tenantID).RenderAssetsForLeaf(ctx, tenantID, req.LeafID, req.NodeIDs, req.Format, req.Scale)
		if errors.Is(rerr, ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		if IsAssetExportNodeMissing(rerr) {
			writeJSONErr(w, http.StatusUnprocessableEntity, "node_not_renderable", rerr.Error())
			return
		}
		if rerr != nil {
			writeJSONErr(w, http.StatusBadGateway, "render_failed", rerr.Error())
			return
		}

		// Build the zip. Stream into a buffer first; if it grows past
		// MaxBulkZipBufferBytes, copy what we have to a temp file and
		// continue streaming there. Either way the registry holds bytes
		// (small) or a file path (large) to feed the GET.
		entry := &bulkExportEntry{
			tenantID:    tenantID,
			leafID:      req.LeafID,
			projectSlug: slug,
			bulkID:      uuid.NewString(),
			format:      req.Format,
			scale:       req.Scale,
			results:     results,
			nodeNames:   map[string]string{}, // friendly names not yet wired; node_id is the fallback
			dataDir:     s.deps.DataDir,
			expiresAt:   time.Now().Add(BulkExportTokenTTL),
			actorUserID: claims.Sub,
		}
		size, zipBytes, zipPath, zerr := assembleBulkZip(s.deps.DataDir, slug, results, req.Format)
		if zerr != nil {
			writeJSONErr(w, http.StatusInternalServerError, "zip_failed", zerr.Error())
			return
		}
		if size > MaxBulkZipTotalBytes {
			if zipPath != "" {
				_ = os.Remove(zipPath)
			}
			writeJSONErr(w, http.StatusRequestEntityTooLarge, "zip_too_large",
				fmt.Sprintf("zip %d bytes exceeds cap %d", size, MaxBulkZipTotalBytes))
			return
		}
		entry.zipBytes = zipBytes
		entry.zipPath = zipPath
		entry.zipSize = size
		s.bulkRegistry().Put(entry)

		composite := bulkAssetTokenKey(entry.bulkID)
		token := s.deps.AssetSigner.Mint(tenantID, composite, BulkExportTokenTTL)
		writeJSON(w, http.StatusOK, map[string]any{
			"download_url": fmt.Sprintf("/v1/projects/%s/assets/bulk/%s?at=%s", slug, entry.bulkID, token),
			"expires_in":   int(BulkExportTokenTTL.Seconds()),
			"bulk_id":      entry.bulkID,
			"size_bytes":   size,
			"count":        len(results),
		})
	}
}

// HandleBulkDownload serves
//
//	GET /v1/projects/{slug}/assets/bulk/{token}?at=<signed_token>
//
// `{token}` is the bulk_id; `?at=` is the signed token bound to
// (tenant, "bulk:<bulk_id>"). On verify success, the registry entry is
// consumed (one-shot semantics) and the zip streams. Each asset inside the
// archive writes its own `asset.exported` audit-log row sharing the bulk_id.
func (s *Server) HandleBulkDownload() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		slug := r.PathValue("slug")
		bulkID := r.PathValue("token") // path component is named `token` in the route but carries the bulk_id
		at := r.URL.Query().Get("at")

		if slug == "" || bulkID == "" {
			writeJSONErr(w, http.StatusBadRequest, "missing_params", "slug,bulk_id required")
			return
		}
		if at == "" {
			writeJSONErr(w, http.StatusUnauthorized, "missing_token", "?at= required")
			return
		}
		if s.deps.AssetSigner == nil {
			writeJSONErr(w, http.StatusServiceUnavailable, "asset_signer", "not configured")
			return
		}

		// We need the tenant to verify the MAC. The bulk registry knows
		// (since it stored tenantID at mint time); fetch the entry first
		// (one-shot — succeeds at most once), then verify the token. If
		// verify fails, requeue the entry so a benign retry isn't punished
		// by losing the staged data.
		entry, ok := s.bulkRegistry().Take(bulkID)
		if !ok {
			// Could be: never minted, already consumed, or expired. We map
			// any of these to 410 Gone so a stale link doesn't hang.
			writeJSONErr(w, http.StatusGone, "expired_or_consumed", "bulk download not available")
			return
		}
		composite := bulkAssetTokenKey(bulkID)
		if verifyErr := s.deps.AssetSigner.Verify(at, entry.tenantID, composite); verifyErr != nil {
			// Re-park the entry so the legitimate caller (with a correct
			// token) can still retrieve it within the TTL.
			s.bulkRegistry().Put(entry)
			status := assetTokenErrToStatus(verifyErr)
			writeJSONErr(w, status, "asset_token", verifyErr.Error())
			return
		}
		// Verified — clean the temp file (if any) after streaming.
		defer cleanupBulkEntry(entry)

		filename := fmt.Sprintf("%s__bulk-%s.zip", entry.projectSlug, entry.bulkID[:8])
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Length", fmt.Sprintf("%d", entry.zipSize))
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))

		// Per-asset audit_log rows — share bulk_id, mirror HandleBulkAcknowledge.
		for _, res := range entry.results {
			s.writeAssetExportAudit(ctx, r, entry.tenantID, entry.bulkID, res.NodeID,
				deriveFileIDFromStorageKey(res.StorageKey), entry.format, entry.scale)
		}

		// Stream zip — from memory (small) or temp file (large).
		if entry.zipBytes != nil {
			if _, err := w.Write(entry.zipBytes); err != nil {
				slog.Debug("bulk download: write failed (client disconnect?)", "err", err)
			}
			return
		}
		if entry.zipPath != "" {
			f, err := os.Open(entry.zipPath)
			if err != nil {
				writeJSONErr(w, http.StatusInternalServerError, "open_zip", err.Error())
				return
			}
			defer f.Close()
			if _, err := io.Copy(w, f); err != nil {
				slog.Debug("bulk download: copy failed (client disconnect?)", "err", err, "path", entry.zipPath)
			}
		}
	}
}

// ─── Helpers ────────────────────────────────────────────────────────────────

// singleAssetTokenKey is the composite key the AssetTokenSigner signs for a
// single-asset URL. Unique per (tenant, file_id, node_id, format, scale) so
// a leaked URL only exposes the one asset.
func singleAssetTokenKey(fileID, nodeID, format string, scale int) string {
	return fmt.Sprintf("asset:%s|%s|%s|%d", fileID, nodeID, format, scale)
}

// bulkAssetTokenKey is the composite key for a bulk one-shot URL. The
// bulk_id is server-generated, so a leaked URL only ever maps to its own
// staged registry entry — no cross-bulk replay.
func bulkAssetTokenKey(bulkID string) string {
	return "bulk:" + bulkID
}

// assetTokenErrToStatus maps the AssetTokenSigner.Verify error sentinels to
// HTTP status codes per the U5 plan: expired → 410, mismatch/malformed → 403.
func assetTokenErrToStatus(err error) int {
	switch {
	case errors.Is(err, auth.ErrAssetTokenExpired):
		return http.StatusGone
	case errors.Is(err, auth.ErrAssetTokenInvalidMAC):
		return http.StatusForbidden
	case errors.Is(err, auth.ErrAssetTokenMalformed):
		return http.StatusForbidden
	default:
		return http.StatusForbidden
	}
}

// safeFilenameRe matches any character outside the plan's allowlist
// `[a-zA-Z0-9-_]`. Non-matching characters get replaced with `_` so the
// filename stays portable across browsers + filesystems.
var safeFilenameRe = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

// sanitiseAssetName replaces filesystem-/HTTP-hostile characters with `_`.
// Leading/trailing underscores collapse so we don't accidentally produce
// `_my_icon_.svg` for an empty name. Empty input → "asset".
func sanitiseAssetName(name string) string {
	if name == "" {
		return "asset"
	}
	out := safeFilenameRe.ReplaceAllString(name, "_")
	out = strings.Trim(out, "_")
	if out == "" {
		return "asset"
	}
	return out
}

// buildAssetFilename produces `<slug>__<sanitised-name>.<ext>` per the plan.
func buildAssetFilename(slug, nodeName, format string) string {
	cleanSlug := sanitiseAssetName(slug)
	cleanName := sanitiseAssetName(nodeName)
	return fmt.Sprintf("%s__%s.%s", cleanSlug, cleanName, format)
}

// assembleBulkZip walks the rendered results, reading each from disk under
// `dataDir/<storage_key>` and writing into a zip archive. Returns
// (size, in-memory zip bytes, temp-file path, error). Exactly one of
// zipBytes / zipPath is non-empty:
//
//   - small zips (<= MaxBulkZipBufferBytes) are returned in memory
//   - larger zips are spooled to a temp file the caller is responsible for
//     deleting after streaming.
//
// Each asset is named `<slug>__<sanitised-node-id>.<ext>` so a designer
// gets a flat, human-readable archive (no nested dirs).
// bulkZipEntry is one row in the assembled archive — local file path on
// disk + the in-zip filename. Defined at package scope so the spool helper
// can take it by slice without an anon-struct mismatch.
type bulkZipEntry struct {
	filename string
	fullPath string
	size     int64
}

func assembleBulkZip(dataDir, projectSlug string, results []AssetExportResult, format string) (int64, []byte, string, error) {
	baseDir, err := filepath.Abs(dataDir)
	if err != nil {
		return 0, nil, "", err
	}
	entries := make([]bulkZipEntry, 0, len(results))
	var totalIn int64
	for _, r := range results {
		fullPath, err := filepath.Abs(filepath.Join(baseDir, filepath.Clean(r.StorageKey)))
		if err != nil {
			return 0, nil, "", fmt.Errorf("abs %s: %w", r.StorageKey, err)
		}
		if !strings.HasPrefix(fullPath, baseDir+string(os.PathSeparator)) && fullPath != baseDir {
			return 0, nil, "", fmt.Errorf("path escapes data dir: %s", r.StorageKey)
		}
		st, err := os.Stat(fullPath)
		if err != nil {
			return 0, nil, "", fmt.Errorf("stat %s: %w", r.StorageKey, err)
		}
		entries = append(entries, bulkZipEntry{
			filename: buildAssetFilename(projectSlug, r.NodeID, format),
			fullPath: fullPath,
			size:     st.Size(),
		})
		totalIn += st.Size()
	}

	// Decide spool path: if uncompressed input already exceeds the buffer
	// cap, go straight to a temp file. Otherwise try in memory.
	if totalIn > int64(MaxBulkZipBufferBytes) {
		return spoolBulkZipToFile(entries)
	}

	// Use a sized buffer + io.Writer. zip.NewWriter is happy to write to
	// any io.Writer; we use a *bufferingWriter that flips to a temp file
	// once we exceed MaxBulkZipBufferBytes.
	bw := &flushToFileWriter{maxMem: MaxBulkZipBufferBytes}
	zw := zip.NewWriter(bw)
	for _, e := range entries {
		fw, err := zw.Create(e.filename)
		if err != nil {
			_ = bw.cleanup()
			return 0, nil, "", err
		}
		f, err := os.Open(e.fullPath)
		if err != nil {
			_ = bw.cleanup()
			return 0, nil, "", err
		}
		if _, err := io.Copy(fw, f); err != nil {
			_ = f.Close()
			_ = bw.cleanup()
			return 0, nil, "", err
		}
		_ = f.Close()
	}
	if err := zw.Close(); err != nil {
		_ = bw.cleanup()
		return 0, nil, "", err
	}
	if bw.spilled {
		return bw.totalWritten, nil, bw.tempPath, nil
	}
	return bw.totalWritten, bw.mem, "", nil
}

// spoolBulkZipToFile is the fallback for "definitely too big to keep in RAM"
// inputs. Writes the zip directly to a temp file and returns its path.
func spoolBulkZipToFile(entries []bulkZipEntry) (int64, []byte, string, error) {
	tmp, err := os.CreateTemp("", "asset-bulk-*.zip")
	if err != nil {
		return 0, nil, "", err
	}
	zw := zip.NewWriter(tmp)
	for _, e := range entries {
		fw, err := zw.Create(e.filename)
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
			return 0, nil, "", err
		}
		f, err := os.Open(e.fullPath)
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
			return 0, nil, "", err
		}
		if _, err := io.Copy(fw, f); err != nil {
			_ = f.Close()
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
			return 0, nil, "", err
		}
		_ = f.Close()
	}
	if err := zw.Close(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return 0, nil, "", err
	}
	st, err := tmp.Stat()
	if err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return 0, nil, "", err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return 0, nil, "", err
	}
	return st.Size(), nil, tmp.Name(), nil
}

// flushToFileWriter is a Writer that buffers in memory up to maxMem bytes,
// then transparently spills to a temp file. Used so the zip-build path
// doesn't have to know whether it'll ultimately fit in RAM.
type flushToFileWriter struct {
	maxMem       int
	mem          []byte
	spilled      bool
	tempPath     string
	tempFile     *os.File
	totalWritten int64
}

func (b *flushToFileWriter) Write(p []byte) (int, error) {
	b.totalWritten += int64(len(p))
	if b.spilled {
		return b.tempFile.Write(p)
	}
	if len(b.mem)+len(p) <= b.maxMem {
		b.mem = append(b.mem, p...)
		return len(p), nil
	}
	// Spill: flush mem to a temp file, then write p there.
	tmp, err := os.CreateTemp("", "asset-bulk-*.zip")
	if err != nil {
		return 0, err
	}
	b.tempFile = tmp
	b.tempPath = tmp.Name()
	if _, err := tmp.Write(b.mem); err != nil {
		return 0, err
	}
	b.mem = nil
	b.spilled = true
	return tmp.Write(p)
}

func (b *flushToFileWriter) cleanup() error {
	if b.tempFile != nil {
		_ = b.tempFile.Close()
	}
	if b.tempPath != "" {
		return os.Remove(b.tempPath)
	}
	return nil
}

// tenantExporter returns a request-scoped AssetExporter clone whose Repo
// is bound to tenantID. Production wiring stores a tenant-less exporter on
// ServerDeps so the boot path doesn't have to enumerate tenants; per-call
// we shallow-copy the exporter and swap in a tenant-scoped repo so the
// LookupLeafFigmaContext / LookupAsset / StoreAsset chain inside
// RenderAssetsForLeaf passes its `tenantID required` checks.
//
// Returns nil when AssetExporter isn't configured. Callers must nil-check.
func (s *Server) tenantExporter(tenantID string) *AssetExporter {
	base := s.deps.AssetExporter
	if base == nil {
		return nil
	}
	cp := *base
	cp.Repo = NewTenantRepo(s.deps.DB.DB, tenantID)
	return &cp
}

// TenantBoundExporter exposes the same per-tenant copy-on-clone the
// internal handlers use, for callers that need a tenant-scoped
// AssetExporter outside the Server type — specifically the preview-
// pyramid generator's source fetcher (asset_preview_pyramid.go).
//
// Returns nil if the underlying AssetExporter isn't wired, mirroring
// tenantExporter's behaviour so consumers don't have to special-case
// "exporter unavailable" twice.
func TenantBoundExporter(base *AssetExporter, db *sql.DB, tenantID string) *AssetExporter {
	if base == nil {
		return nil
	}
	cp := *base
	cp.Repo = NewTenantRepo(db, tenantID)
	return &cp
}

// bulkRegistry returns the (lazily initialised) per-server registry. We use
// a method on Server so the registry's lifetime ties to the server's, and
// tests can construct a fresh Server without leaking entries across runs.
func (s *Server) bulkRegistry() *bulkExportRegistry {
	s.bulkRegistryOnce.Do(func() {
		s.bulkRegistryV = newBulkExportRegistry()
	})
	return s.bulkRegistryV
}

// writeAssetExportAudit best-effort writes a single audit_log row for an
// asset.exported event. Per-asset rows in a bulk export share `bulk_id`.
// Failure to log is logged at debug and never blocks the response.
func (s *Server) writeAssetExportAudit(ctx context.Context, r *http.Request, tenantID, bulkID, nodeID, fileID, format string, scale int) {
	if s.deps.DB == nil || s.deps.DB.DB == nil {
		return
	}
	claims, _ := ctx.Value(ctxKeyClaims).(*auth.Claims)
	userID := ""
	if claims != nil {
		userID = claims.Sub
	}
	details, _ := json.Marshal(map[string]any{
		"node_id":    nodeID,
		"format":     format,
		"scale":      scale,
		"bulk_id":    bulkID,
		"file_id":    fileID,
		"schema_ver": ProjectsSchemaVersion,
	})
	_, err := s.deps.DB.DB.ExecContext(ctx,
		`INSERT INTO audit_log
		    (id, ts, event_type, tenant_id, user_id, method, endpoint, status_code, duration_ms, ip_address, details)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		uuid.NewString(),
		time.Now().UTC().Format(time.RFC3339Nano),
		"asset.exported",
		tenantID,
		userID,
		r.Method,
		r.URL.Path,
		http.StatusOK,
		0,
		clientIP(r),
		string(details),
	)
	if err != nil {
		slog.Debug("asset export audit insert failed", "err", err, "node", nodeID)
	}
}

// lookupProjectTenantBySlug resolves (tenant_id, file_id, slug) from a project
// slug without requiring the caller to pass tenant_id. Used by the unauth-
// JWT asset-token download path: the asset signed token IS the auth, so we
// derive tenant from the row instead of trusting URL input.
//
// Returns ("", "", "", err) on miss; the handler treats that as 404 to avoid
// an existence oracle.
func (s *Server) lookupProjectTenantBySlug(ctx context.Context, slug string) (string, string, string, error) {
	if s.deps.DB == nil || s.deps.DB.DB == nil {
		return "", "", "", errors.New("no db")
	}
	var tenantID, fileID, projSlug string
	err := s.deps.DB.DB.QueryRowContext(ctx,
		`SELECT tenant_id, file_id, slug FROM projects WHERE slug = ? LIMIT 1`, slug,
	).Scan(&tenantID, &fileID, &projSlug)
	if err != nil {
		return "", "", "", err
	}
	return tenantID, fileID, projSlug, nil
}

// lookupVersionForFile returns (file_id, latest_version_index) for any project
// matching file_id under this tenant. Used by the GET asset path which
// already has file_id from the slug lookup but not the version_index that
// the asset_cache PK requires.
func (s *Server) lookupVersionForFile(ctx context.Context, repo *TenantRepo, fileID string) (string, int, error) {
	row := repo.handle().QueryRowContext(ctx, `
		SELECT COALESCE(MAX(v.version_index), 0)
		  FROM project_versions v
		  JOIN projects p ON p.id = v.project_id
		 WHERE v.tenant_id = ? AND p.file_id = ?
	`, repo.tenantID, fileID)
	var vi int
	if err := row.Scan(&vi); err != nil {
		return "", 0, err
	}
	return fileID, vi, nil
}

// lookupAnyLeafForFile returns *any* leaf (flow.id) for the given file_id
// under this tenant — used as the leaf_id input to RenderAssetsForLeaf when
// the GET path needs to trigger a synchronous render but only knows the
// file_id (the URL doesn't carry leaf_id). The flow merely anchors the
// tenant-scoped lookup; per-asset rate limiting and cache keys depend on
// (tenant, file_id, node_id, format, scale) only.
func (s *Server) lookupAnyLeafForFile(ctx context.Context, repo *TenantRepo, fileID string) (string, error) {
	row := repo.handle().QueryRowContext(ctx, `
		SELECT id FROM flows
		 WHERE tenant_id = ? AND file_id = ? AND deleted_at IS NULL
		 LIMIT 1
	`, repo.tenantID, fileID)
	var id string
	if err := row.Scan(&id); err != nil {
		return "", err
	}
	return id, nil
}

// assetExportTenantCtxKey is the (private) context key under which
// RenderAssetsForLeaf stashes the tenantID before calling the URL fetcher.
// A tenant-aware fetcher (production wiring) uses AssetExportTenantFromCtx
// to recover it without the FigmaImageURLFetcher interface having to grow
// a tenantID parameter.
type assetExportTenantCtxKeyT struct{}

var assetExportTenantCtxKey = assetExportTenantCtxKeyT{}

func withAssetExportTenant(ctx context.Context, tenantID string) context.Context {
	return context.WithValue(ctx, assetExportTenantCtxKey, tenantID)
}

// AssetExportTenantFromCtx retrieves the tenantID stashed by
// RenderAssetsForLeaf so a tenant-aware FigmaImageURLFetcher implementation
// can decrypt the right Figma PAT. Returns "" when not present.
func AssetExportTenantFromCtx(ctx context.Context) string {
	if v, ok := ctx.Value(assetExportTenantCtxKey).(string); ok {
		return v
	}
	return ""
}

// deriveFileIDFromStorageKey reverses the persistAssetBytes layout
// (`assets/<tenant>/<file>/v<n>/<node>.<ext>`) to recover the file_id.
// Best-effort — used only in the bulk audit log for the `file_id` field.
// On any malformed input returns "" so the audit row simply lacks the file
// hint.
func deriveFileIDFromStorageKey(key string) string {
	parts := strings.Split(key, "/")
	if len(parts) >= 3 && parts[0] == "assets" {
		return parts[2]
	}
	return ""
}
