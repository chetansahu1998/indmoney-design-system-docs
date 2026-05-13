package projects

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/auth"
)

// server_organism_admin.go — U10 of the organism-pattern-detection plan.
// Three super-admin endpoints powering the atlas admin dashboard (U11) +
// the promotion candidates panel (U14):
//
//   GET /v1/admin/organisms/adoption
//   GET /v1/admin/organisms/{slug}/matches
//   GET /v1/admin/organisms/promotion-candidates
//
// All endpoints are read-only, tenant-scoped via JWT, and return JSON
// shaped for direct consumption by the React admin page. None recompute
// detection or aggregation — they read the precomputed corpus written by
// Stage 6.7 (U5) and the Part D rebuild (U13).

// HandleOrganismAdoption serves GET /v1/admin/organisms/adoption.
//
// Per-organism adoption + drift counts. Today the response is bucketed by
// detected_organism_match.suspected_slug; rows with empty suspected_slug
// (i.e. novel matches with no published organism to attribute to) collapse
// into a single "(unmatched-novel)" entry so the dashboard can render
// "X novel patterns detected" alongside per-slug rows.
//
// Future evolution: when the manifest's composition_refs are repopulated
// and Stage 6.7 produces exact/near matches, the rows array will gain one
// entry per published slug. The `signature_catalog_empty` flag in the
// response tells the UI when this is the case so it can show an info
// banner.
func (s *Server) HandleOrganismAdoption(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET only")
		return
	}
	tenantID, ok := s.requireOrganismAdminTenant(w, r)
	if !ok {
		return
	}
	repo := NewTenantRepo(s.deps.DB.DB, tenantID)
	rows, err := repo.OrganismAdoptionRollup(r.Context())
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "adoption_rollup", err.Error())
		return
	}
	// Surface manifest emptiness so the UI can render the "publish your
	// composition_refs to unlock matching" banner. We don't load the
	// manifest here — the corpus itself tells us: if 100% of rows are
	// novel-with-empty-suspected_slug, the catalog is effectively empty.
	signatureCatalogEmpty := true
	totalMatches := 0
	for _, r := range rows {
		totalMatches += r.Exact + r.Near + r.Novel
		if r.Slug != "" {
			signatureCatalogEmpty = false
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"rows":                    rows,
		"total_matches":           totalMatches,
		"signature_catalog_empty": signatureCatalogEmpty,
	})
}

// HandleOrganismMatchesBySlug serves
//
//	GET /v1/admin/organisms/{slug}/matches?kind=exact|near|novel&limit=&offset=
//
// Returns one row per detected match for the slug, ordered by detected_at
// DESC. `kind` filter is optional — omit to include all kinds. The empty-
// slug bucket is queried via slug = "_unmatched" so the URL path stays
// route-safe.
func (s *Server) HandleOrganismMatchesBySlug(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET only")
		return
	}
	tenantID, ok := s.requireOrganismAdminTenant(w, r)
	if !ok {
		return
	}
	slug := r.PathValue("slug")
	if slug == "" {
		writeJSONErr(w, http.StatusBadRequest, "missing_slug", "slug required")
		return
	}
	// "_unmatched" is the URL-safe alias for the empty-slug novel bucket.
	if slug == "_unmatched" {
		slug = ""
	}
	kind := r.URL.Query().Get("kind")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))

	repo := NewTenantRepo(s.deps.DB.DB, tenantID)
	matches, err := repo.ListOrganismMatchesBySlug(r.Context(), slug, kind, limit, offset)
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "list_matches", err.Error())
		return
	}
	// DTO conversion — the table-level struct has time.Time fields that
	// JSON-encode reliably, but we slim down to the fields the UI actually
	// renders so future schema changes don't accidentally leak.
	out := make([]organismMatchDTO, 0, len(matches))
	for _, m := range matches {
		out = append(out, organismMatchToDTO(m))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"matches": out,
		"slug":    r.PathValue("slug"), // echo the URL-form slug
		"count":   len(out),
	})
}

// HandleOrganismVerdictLookup serves
//
//	POST /v1/audit/organism-match
//	Body: { "node_id": "<figma_node_id>", "file_id": "<figma_file_id>" }
//
// U7 of the organism-pattern-detection plan — the plugin's "Check
// selection against DS" command calls this with the selected FRAME's
// node id. Returns the latest verdict row for that node within the
// caller's tenant, or 200 + null when no import covers the node yet.
//
// Read-only, tenant-scoped, no recomputation at request time. If the
// plugin asks about a node never seen by the pipeline, the response is
// 200 + null + an explanatory `reason` field so the plugin UI can
// render a "no verdict — please import this file first" hint.
func (s *Server) HandleOrganismVerdictLookup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST only")
		return
	}
	tenantID, ok := s.requireOrganismAdminTenant(w, r)
	if !ok {
		return
	}
	var req struct {
		NodeID string `json:"node_id"`
		FileID string `json:"file_id,omitempty"` // optional — used only for telemetry today
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if strings.TrimSpace(req.NodeID) == "" {
		writeJSONErr(w, http.StatusBadRequest, "missing_node_id", "node_id required")
		return
	}
	repo := NewTenantRepo(s.deps.DB.DB, tenantID)
	match, err := repo.LookupOrganismMatchByFrame(r.Context(), req.NodeID)
	if errors.Is(err, ErrNotFound) {
		writeJSON(w, http.StatusOK, map[string]any{
			"verdict": nil,
			"reason":  "no_import_covers_this_frame",
		})
		return
	}
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "verdict_lookup", err.Error())
		return
	}
	dto := organismMatchToDTO(match)
	// U9 — surface designer's intentional-fork assertion when present.
	// Not an error path when the fork-mark table doesn't have an entry;
	// just leave the field unset.
	resp := map[string]any{"verdict": dto}
	if mark, ferr := repo.LookupOrganismForkMark(r.Context(), req.NodeID); ferr == nil {
		resp["is_intentional_fork"] = true
		resp["fork_reason"] = mark.Reason
		resp["fork_marked_at"] = mark.MarkedAt.Format(rfc3339NoTZ)
	}
	writeJSON(w, http.StatusOK, resp)
}

// HandleOrganismForkMark serves
//
//	POST /v1/audit/organism-match/fork
//	Body: { "node_id": "<figma_node_id>", "reason": "<free-form note>" }
//
// U9 of the plan — designer's "Mark as intentional fork" action records
// that this frame is an authored choice, not drift. Subsequent verdict
// lookups surface `is_intentional_fork: true` so the plugin can render
// a "Marked as intentional fork" affordance instead of nudging again,
// and the admin dashboard buckets these separately from real drift.
//
// Idempotent — re-marking the same frame replaces the prior reason.
func (s *Server) HandleOrganismForkMark(w http.ResponseWriter, r *http.Request) {
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
	if strings.TrimSpace(claims.Sub) == "" {
		writeJSONErr(w, http.StatusUnauthorized, "unauthorized", "missing user id in claims")
		return
	}
	var req struct {
		NodeID string `json:"node_id"`
		Reason string `json:"reason,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if strings.TrimSpace(req.NodeID) == "" {
		writeJSONErr(w, http.StatusBadRequest, "missing_node_id", "node_id required")
		return
	}
	repo := NewTenantRepo(s.deps.DB.DB, tenantID)
	if err := repo.UpsertOrganismForkMark(r.Context(), OrganismForkMark{
		FrameID:        req.NodeID,
		MarkedByUserID: claims.Sub,
		Reason:         req.Reason,
	}); err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "fork_mark_upsert", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":     true,
		"frame_id": req.NodeID,
	})
}

// HandleOrganismPromotionCandidates serves
//
//	GET /v1/admin/organisms/promotion-candidates?limit=
//
// Returns the tenant's promotion_candidate rows ranked by composite score
// (frequency × stability_score × atom_reuse_rate) DESC. Powers U14's
// panel. Excludes dismissed rows by default.
func (s *Server) HandleOrganismPromotionCandidates(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET only")
		return
	}
	tenantID, ok := s.requireOrganismAdminTenant(w, r)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	repo := NewTenantRepo(s.deps.DB.DB, tenantID)
	candidates, err := repo.ListPromotionCandidates(r.Context(), limit)
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "list_promotion_candidates", err.Error())
		return
	}
	out := make([]promotionCandidateDTO, 0, len(candidates))
	for _, c := range candidates {
		out = append(out, promotionCandidateToDTO(c))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"candidates": out,
		"count":      len(out),
	})
}

// requireOrganismAdminTenant is a small helper that consolidates the
// claims-extraction + tenant-resolution dance every organism admin
// handler does. Returns (tenantID, ok); on !ok the response has already
// been written with the appropriate 401/403.
func (s *Server) requireOrganismAdminTenant(w http.ResponseWriter, r *http.Request) (string, bool) {
	claims, _ := r.Context().Value(ctxKeyClaims).(*auth.Claims)
	if claims == nil {
		writeJSONErr(w, http.StatusUnauthorized, "unauthorized", "missing claims")
		return "", false
	}
	tenantID := s.resolveTenantID(claims)
	if tenantID == "" {
		writeJSONErr(w, http.StatusForbidden, "no_tenant", "")
		return "", false
	}
	return tenantID, true
}

// ─── DTOs ────────────────────────────────────────────────────────────────────

type organismMatchDTO struct {
	FrameID             string  `json:"frame_id"`
	ScreenID            string  `json:"screen_id"`
	VersionID           string  `json:"version_id"`
	MatchKind           string  `json:"match_kind"`
	SuspectedSlug       string  `json:"suspected_slug,omitempty"`
	SuspectedVariantKey string  `json:"suspected_variant_key,omitempty"`
	Confidence          float64 `json:"confidence"`
	FingerprintHash     string  `json:"fingerprint_hash"`
	AtomSignatureJSON   string  `json:"atom_signature_json"`
	SlotTopologyJSON    string  `json:"slot_topology_json"`
	DiffJSON            string  `json:"diff_json,omitempty"`
	ParentFrameID       string  `json:"parent_frame_id,omitempty"`
	ManifestHash        string  `json:"manifest_hash"`
	DetectedAt          string  `json:"detected_at"`
}

func organismMatchToDTO(m DetectedOrganismMatch) organismMatchDTO {
	return organismMatchDTO{
		FrameID:             m.FrameID,
		ScreenID:            m.ScreenID,
		VersionID:           m.VersionID,
		MatchKind:           m.MatchKind,
		SuspectedSlug:       m.SuspectedSlug,
		SuspectedVariantKey: m.SuspectedVariantKey,
		Confidence:          m.Confidence,
		FingerprintHash:     m.FingerprintHash,
		AtomSignatureJSON:   m.AtomSignatureJSON,
		SlotTopologyJSON:    m.SlotTopologyJSON,
		DiffJSON:            m.DiffJSON,
		ParentFrameID:       m.ParentFrameID,
		ManifestHash:        m.ManifestHash,
		DetectedAt:          m.DetectedAt.Format(rfc3339NoTZ),
	}
}

type promotionCandidateDTO struct {
	FingerprintHash string  `json:"fingerprint_hash"`
	Frequency       int     `json:"frequency"`
	FileCount       int     `json:"file_count"`
	StabilityScore  float64 `json:"stability_score"`
	AtomReuseRate   float64 `json:"atom_reuse_rate"`
	CompositeScore  float64 `json:"composite_score"`
	ProposedName    string  `json:"proposed_name,omitempty"`
	FirstSeen       string  `json:"first_seen"`
	LastSeen        string  `json:"last_seen"`
}

func promotionCandidateToDTO(c PromotionCandidate) promotionCandidateDTO {
	return promotionCandidateDTO{
		FingerprintHash: c.FingerprintHash,
		Frequency:       c.Frequency,
		FileCount:       c.FileCount,
		StabilityScore:  c.StabilityScore,
		AtomReuseRate:   c.AtomReuseRate,
		CompositeScore:  float64(c.Frequency) * c.StabilityScore * c.AtomReuseRate,
		ProposedName:    c.ProposedName,
		FirstSeen:       c.FirstSeen.Format(rfc3339NoTZ),
		LastSeen:        c.LastSeen.Format(rfc3339NoTZ),
	}
}

// rfc3339NoTZ is RFC 3339 with seconds precision and no timezone padding,
// matching the storage format used by other handlers in this package.
const rfc3339NoTZ = "2006-01-02T15:04:05Z"
