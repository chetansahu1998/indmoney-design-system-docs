package projects

// screen_image_fills_handler.go — HTTP layer for the image-fill resolver.
//
// Two endpoints:
//
//	GET /v1/projects/{slug}/leaves/{leaf_id}/image-refs
//	    Returns { "image_refs": { "<imageRef>": "/v1/projects/<slug>/assets/raw/<imageRef>" } }
//	    Calls ImageFillResolver.ResolveImageRefsForLeaf which is idempotent
//	    after first warm-up; subsequent calls hit the cache exclusively.
//
//	GET /v1/projects/{slug}/assets/raw/{imageRef}
//	    Streams the cached blob bytes off disk. Tenant-scoped lookup means
//	    cross-tenant access is impossible regardless of slug. Cache-miss → 404.
//
// Why two endpoints:
//
//	The first lets the frontend pre-resolve every imageRef in a leaf with
//	one round-trip (so the canvas can inspect URLs before painting). The
//	second is the actual serve path that <img src="..."> hits per-fill.
//	Splitting them keeps the per-tile request lightweight (no Figma roundtrip)
//	and avoids leaking the resolver's batched fetch behavior to every
//	browser image request.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/auth"
)

// HandleWarmAssetCache serves
//
//	POST /v1/projects/{slug}/assets/warm
//
// Body: `{ "leaf_id": "...", "node_ids": [...], "format": "png|svg", "scale": 1|2|3 }`.
// Response: `{ "warmed": <int>, "results": [{node_id, mime, storage_key}, ...] }`.
//
// Why this exists: HandleAssetDownload synchronously renders on cache miss
// with a 5-second budget; if it elapses, returns 425. Browsers don't retry
// `<img>` fetches on 425 — they just show a broken image. For canvas-v2 we
// need every cluster's PNG cache-warm before any `<img>` request fires;
// otherwise 30 simultaneous misses race the 5-req/sec Figma render budget
// and most lose. This endpoint runs RenderAssetsForLeaf upfront, batched
// at 80 ids/Figma-call, and returns when every requested node is cached.
//
// Frontend flow:
//
//  1. Walk canonical_tree, collect cluster ids
//  2. POST /assets/warm with all ids — wait for response (~10-30s typical)
//  3. Parallel-mint /assets/export-url tokens — every fetch hits cache
//  4. Render `<img src=…?at=…>` — instant, no 425s
//
// Cost: roughly the same Figma API calls the old per-cluster path made,
// but front-loaded into one orchestrated burst instead of spread across
// failing browser retries. Net: faster and more deterministic.
func (s *Server) HandleWarmAssetCache(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST only")
		return
	}
	claims, _ := r.Context().Value(ctxKeyClaims).(*auth.Claims)
	if claims == nil {
		writeJSONErr(w, http.StatusUnauthorized, "unauthorized", "missing claims")
		return
	}
	tenantID := s.resolveTenantID(claims)
	if tenantID == "" {
		writeJSONErr(w, http.StatusForbidden, "no_tenant", "")
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
	if err := readJSON(r, &req, 256*1024); err != nil {
		writeJSONErr(w, http.StatusBadRequest, "decode", err.Error())
		return
	}
	if req.LeafID == "" || len(req.NodeIDs) == 0 {
		writeJSONErr(w, http.StatusBadRequest, "invalid_payload", "leaf_id + node_ids required")
		return
	}
	// Cap matches the bulk-export ceiling so a runaway client can't
	// queue 10k Figma render slots in one request.
	if len(req.NodeIDs) > MaxBulkAssetExportRows {
		writeJSONErr(w, http.StatusBadRequest, "too_many_nodes",
			fmt.Sprintf("max %d node_ids per warm call", MaxBulkAssetExportRows))
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

	// Tenant scope check — same as HandleBulkAssetExport.
	repo := NewTenantRepo(s.deps.DB.DB, tenantID)
	if _, err := repo.GetProjectBySlug(r.Context(), slug); err != nil {
		if errors.Is(err, ErrNotFound) {
			writeJSONErr(w, http.StatusNotFound, "warm_project_not_found",
				fmt.Sprintf("slug=%q tenant=%q", slug, tenantID))
			return
		}
		writeJSONErr(w, http.StatusInternalServerError, "lookup", err.Error())
		return
	}

	results, err := s.tenantExporter(tenantID).RenderAssetsForLeaf(
		r.Context(), tenantID, req.LeafID, req.NodeIDs, req.Format, req.Scale,
	)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if IsAssetExportNodeMissing(err) {
		// Some clusters can't be SVG-rendered (e.g. raster-only fills) —
		// surface as 200 with partial results so the frontend can render
		// placeholders for the un-warmable ones.
		writeJSON(w, http.StatusOK, map[string]any{
			"warmed":  countNonZero(results),
			"results": results,
			"warning": err.Error(),
		})
		return
	}
	if err != nil {
		writeJSONErr(w, http.StatusBadGateway, "render_failed", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"warmed":  len(results),
		"results": results,
	})
}

func countNonZero(rs []AssetExportResult) int {
	n := 0
	for _, r := range rs {
		if r.NodeID != "" {
			n++
		}
	}
	return n
}

// readJSON reads up to `cap` bytes from r.Body and unmarshals into out.
// Mirrors the inline pattern used by HandleBulkAssetExport — pulled here
// so HandleWarmAssetCache stays focused.
func readJSON(r *http.Request, out any, cap int64) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, cap))
	if err != nil {
		return err
	}
	return json.Unmarshal(body, out)
}

// HandleListImageRefs serves
//
//	GET /v1/projects/{slug}/leaves/{leaf_id}/image-refs
func (s *Server) HandleListImageRefs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET only")
		return
	}
	claims, _ := r.Context().Value(ctxKeyClaims).(*auth.Claims)
	if claims == nil {
		writeJSONErr(w, http.StatusUnauthorized, "unauthorized", "missing claims")
		return
	}
	tenantID := s.resolveTenantID(claims)
	if tenantID == "" {
		writeJSONErr(w, http.StatusForbidden, "no_tenant", "")
		return
	}
	slug := r.PathValue("slug")
	leafID := r.PathValue("leaf_id")
	if slug == "" || leafID == "" {
		writeJSONErr(w, http.StatusBadRequest, "missing_path_params", "")
		return
	}
	if s.deps.ImageFillResolver == nil {
		writeJSONErr(w, http.StatusServiceUnavailable, "resolver_unavailable",
			"ImageFillResolver not configured")
		return
	}

	out, err := s.deps.ImageFillResolver.ResolveImageRefsForLeaf(
		r.Context(), tenantID, slug, leafID,
	)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeJSONErr(w, http.StatusNotFound, "leaf_not_found", "")
			return
		}
		writeJSONErr(w, http.StatusInternalServerError, "resolve_image_refs", err.Error())
		return
	}

	// Convert {imageRef → ImageFillRef} to {imageRef → serve-URL}. We also
	// echo the recorded MIME + bytes so the frontend can size <img>
	// elements correctly and avoid a layout shift on first paint.
	type entry struct {
		URL   string `json:"url"`
		Mime  string `json:"mime"`
		Bytes int64  `json:"bytes"`
	}
	resp := make(map[string]entry, len(out))
	for ref, ifr := range out {
		resp[ref] = entry{
			URL:   fmt.Sprintf("/v1/projects/%s/assets/raw/%s", slug, ref),
			Mime:  ifr.Mime,
			Bytes: ifr.Bytes,
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"image_refs": resp,
	})
}

// HandleServeRawAsset serves
//
//	GET /v1/projects/{slug}/assets/raw/{imageRef}
//
// Streams the cached blob with its recorded MIME + Content-Length. The
// slug is verified to belong to the caller's tenant (via the standard
// requireAuth + resolveTenantID flow), but the actual lookup is by
// (tenantID, imageRef) since the same imageRef can appear in many
// projects within the tenant. Cross-tenant access fails because the
// LookupImageFillByRef query filters on tenant_id.
func (s *Server) HandleServeRawAsset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET only")
		return
	}
	claims, _ := r.Context().Value(ctxKeyClaims).(*auth.Claims)
	if claims == nil {
		writeJSONErr(w, http.StatusUnauthorized, "unauthorized", "missing claims")
		return
	}
	tenantID := s.resolveTenantID(claims)
	if tenantID == "" {
		writeJSONErr(w, http.StatusForbidden, "no_tenant", "")
		return
	}
	imageRef := r.PathValue("imageRef")
	if imageRef == "" || strings.ContainsAny(imageRef, "./\\") {
		// Defensive: imageRefs are 32 hex chars (sha1 prefix in observed
		// payloads). Reject anything with path traversal characters before
		// it reaches filepath.Join.
		writeJSONErr(w, http.StatusBadRequest, "invalid_image_ref", "")
		return
	}

	repo := NewTenantRepo(s.deps.DB.DB, tenantID)
	row, hit, err := repo.LookupImageFillByRef(r.Context(), tenantID, imageRef)
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "lookup_image_fill", err.Error())
		return
	}
	if !hit {
		// Don't 404 with a JSON body — <img> tags can't surface an error
		// detail anyway, and a 404 with empty body keeps the wire small.
		w.WriteHeader(http.StatusNotFound)
		return
	}

	dataDir := s.deps.DataDir
	if s.deps.ImageFillResolver != nil && s.deps.ImageFillResolver.DataDir != "" {
		dataDir = s.deps.ImageFillResolver.DataDir
	}
	abs := filepath.Join(dataDir, row.StorageKey)

	f, err := os.Open(abs)
	if err != nil {
		// Disk delete without DB delete — fall through to 404 so the
		// browser stops retrying. A future GC sweeper will reconcile.
		w.WriteHeader(http.StatusNotFound)
		return
	}
	defer f.Close()

	w.Header().Set("Content-Type", row.Mime)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", row.Bytes))
	// Image fills are content-addressed by sha-derived imageRef, so the
	// content for a given ref never changes. Aggressive cache headers
	// shave repeat-paint latency to ~0 (browser memory cache).
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, f)
}
