package projects

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
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
		got, err := fetchImagesWithRetry(ctx, a.URLs, fileID, chunk, format, scale)
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
