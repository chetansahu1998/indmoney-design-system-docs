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
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/auth"
)

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
