package projects

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/auth"
)

// ─── HTTP handlers for screen_text_overrides (U2 of plan 2026-05-05-002) ────
//
// Body shape, MaxBytesReader cap, and 409-on-revision-conflict mirror
// HandlePutDRD exactly so the frontend save-state machine is reusable.

// MaxOverrideValueBytes caps the PUT body for a single override. Override
// values are short strings (UI labels, microcopy), never long-form content;
// 16 KB leaves headroom for legitimate copy + JSON wrapper.
const MaxOverrideValueBytes = 16 * 1024

// MaxOverrideBulkRows is the per-request cap for HandleBulkUpsertOverrides.
// Mirrors MaxBulkLifecycleRows from Phase 4 for consistency.
const MaxOverrideBulkRows = 100

// MaxOverrideBulkBodyBytes covers 100 rows * 16 KB value + ~512 B framing
// per row. Slight headroom for unicode-heavy bodies. Anything past this →
// 413 (don't even try to parse).
const MaxOverrideBulkBodyBytes = (MaxOverrideValueBytes + 1024) * (MaxOverrideBulkRows + 1)

// putOverrideRequest is the JSON body for PUT
// /v1/projects/{slug}/screens/{id}/text-overrides/{figma_node_id}.
type putOverrideRequest struct {
	Value                string `json:"value"`
	ExpectedRevision     int    `json:"expected_revision"`
	CanonicalPath        string `json:"canonical_path"`
	LastSeenOriginalText string `json:"last_seen_original_text"`
}

// bulkUpsertOverridesRequest mirrors the shape POST'd by U12's CSV import +
// future bulk callers. Each row is one override; up to MaxOverrideBulkRows.
type bulkUpsertOverridesRequest struct {
	Items []bulkOverrideItem `json:"items"`
}

type bulkOverrideItem struct {
	ScreenID             string `json:"screen_id"`
	FigmaNodeID          string `json:"figma_node_id"`
	Value                string `json:"value"`
	CanonicalPath        string `json:"canonical_path"`
	LastSeenOriginalText string `json:"last_seen_original_text"`
}

// HandleListOverrides serves both
//   GET /v1/projects/{slug}/screens/{id}/text-overrides
//   GET /v1/projects/{slug}/leaves/{leaf_id}/text-overrides
//
// The router dispatches both paths to the same handler; it picks which list
// path to use by checking which path-value is set.
func (s *Server) HandleListOverrides(w http.ResponseWriter, r *http.Request) {
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
	screenID := r.PathValue("id")
	leafID := r.PathValue("leaf_id")
	if slug == "" || (screenID == "" && leafID == "") {
		writeJSONErr(w, http.StatusBadRequest, "missing_path_params", "")
		return
	}

	repo := NewTenantRepo(s.deps.DB.DB, tenantID)
	var (
		out []ScreenTextOverride
		err error
	)
	if screenID != "" {
		out, err = repo.ListOverridesByScreen(r.Context(), slug, screenID)
	} else {
		out, err = repo.ListOverridesByLeaf(r.Context(), slug, leafID)
	}
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "list_overrides", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"overrides": out,
	})
}

// HandlePutOverride serves PUT
// /v1/projects/{slug}/screens/{id}/text-overrides/{figma_node_id}.
//
// Body capped at MaxOverrideValueBytes (16 KB); oversize → 413.
// 409 on stale expected_revision exactly like HandlePutDRD.
//
// On success the handler kicks the GraphRebuildPool. Per the Phase 6
// flush-driven SSE rule (docs/solutions/2026-05-01-001-phase-6-closure.md),
// the worker rebuilds search_index_fts inside its own tx and only then
// publishes the SSE event — subscribers therefore never observe a stale
// search slice immediately after a PUT.
func (s *Server) HandlePutOverride(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		writeJSONErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "PUT only")
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
	screenID := r.PathValue("id")
	figmaNodeID := r.PathValue("figma_node_id")
	if slug == "" || screenID == "" || figmaNodeID == "" {
		writeJSONErr(w, http.StatusBadRequest, "missing_path_params", "")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, MaxOverrideValueBytes)
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "too large") {
			writeJSONErr(w, http.StatusRequestEntityTooLarge, "body_too_large",
				fmt.Sprintf("override body exceeds %d bytes", MaxOverrideValueBytes))
			return
		}
		writeJSONErr(w, http.StatusBadRequest, "read_body", err.Error())
		return
	}

	var req putOverrideRequest
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		writeJSONErr(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if req.ExpectedRevision < 0 {
		writeJSONErr(w, http.StatusBadRequest, "invalid_revision",
			"expected_revision must be >= 0")
		return
	}

	repo := NewTenantRepo(s.deps.DB.DB, tenantID)
	endpoint := fmt.Sprintf("/v1/projects/%s/screens/%s/text-overrides/%s",
		slug, screenID, figmaNodeID)
	auditFn := func(tx *sql.Tx, flowID string, oldValue string, newRev int) error {
		return WriteOverrideEvent(r.Context(), tx, OverrideEvent{
			EventType:   AuditActionOverrideTextSet,
			TenantID:    tenantID,
			UserID:      claims.Sub,
			FlowID:      flowID,
			ScreenID:    screenID,
			FigmaNodeID: figmaNodeID,
			OldValue:    oldValue,
			NewValue:    req.Value,
			IPAddress:   clientIP(r),
			Endpoint:    endpoint,
			Method:      "PUT",
			StatusCode:  http.StatusOK,
		})
	}

	res, err := repo.UpsertOverride(r.Context(), OverrideUpsertInput{
		ProjectSlug:          slug,
		ScreenID:             screenID,
		FigmaNodeID:          figmaNodeID,
		Value:                req.Value,
		CanonicalPath:        req.CanonicalPath,
		LastSeenOriginalText: req.LastSeenOriginalText,
		ExpectedRevision:     req.ExpectedRevision,
		UpdatedByUserID:      claims.Sub,
	}, auditFn)
	if errors.Is(err, ErrNotFound) {
		writeJSONErr(w, http.StatusNotFound, "not_found", "")
		return
	}
	if errors.Is(err, ErrRevisionConflict) {
		// Look up the live row so the client can render a diff. Read happens
		// outside the failed write tx — fine, the conflict already means
		// someone else committed.
		current := s.lookupOverrideForConflict(r.Context(), repo, screenID, figmaNodeID)
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":            "revision_conflict",
			"current_revision": current.Revision,
			"current_value":    current.Value,
		})
		return
	}
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "override_upsert", err.Error())
		return
	}

	// U10 — kick the search-rebuild worker AFTER the override tx commits.
	// The worker's RebuildFull rewrites search_index_fts (which now includes
	// `text_override` rows), then publishes the SSE event from inside its
	// own commit window. Per the Phase 6 read-after-write rule, subscribers
	// of `search.reindexed` therefore never observe a stale index.
	s.enqueueGraphRebuild(tenantID, GraphSourceFlows, res.FlowID)

	writeJSON(w, http.StatusOK, map[string]any{
		"revision":   res.Revision,
		"updated_at": res.UpdatedAt.Format(time.RFC3339),
	})
}

// HandleDeleteOverride serves DELETE
// /v1/projects/{slug}/screens/{id}/text-overrides/{figma_node_id}.
//
// Idempotent: missing rows return 204 too. On a real delete we emit one
// `override.text.reset` audit_log row inside the same transaction.
func (s *Server) HandleDeleteOverride(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeJSONErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "DELETE only")
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
	screenID := r.PathValue("id")
	figmaNodeID := r.PathValue("figma_node_id")
	if slug == "" || screenID == "" || figmaNodeID == "" {
		writeJSONErr(w, http.StatusBadRequest, "missing_path_params", "")
		return
	}

	repo := NewTenantRepo(s.deps.DB.DB, tenantID)
	endpoint := fmt.Sprintf("/v1/projects/%s/screens/%s/text-overrides/%s",
		slug, screenID, figmaNodeID)
	auditFn := func(tx *sql.Tx, flowID string, oldValue string) error {
		return WriteOverrideEvent(r.Context(), tx, OverrideEvent{
			EventType:   AuditActionOverrideTextReset,
			TenantID:    tenantID,
			UserID:      claims.Sub,
			FlowID:      flowID,
			ScreenID:    screenID,
			FigmaNodeID: figmaNodeID,
			OldValue:    oldValue,
			IPAddress:   clientIP(r),
			Endpoint:    endpoint,
			Method:      "DELETE",
			StatusCode:  http.StatusNoContent,
		})
	}

	_, flowID, err := repo.DeleteOverride(r.Context(), slug, screenID, figmaNodeID, auditFn)
	if errors.Is(err, ErrNotFound) {
		writeJSONErr(w, http.StatusNotFound, "not_found", "")
		return
	}
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "override_delete", err.Error())
		return
	}

	// U10 — kick search rebuild after commit. flowID may be "" if nothing was
	// actually deleted (idempotent no-op); enqueueGraphRebuild tolerates that.
	s.enqueueGraphRebuild(tenantID, GraphSourceFlows, flowID)

	w.WriteHeader(http.StatusNoContent)
}

// HandleBulkUpsertOverrides serves POST
// /v1/projects/{slug}/text-overrides/bulk.
//
// Accepts up to MaxOverrideBulkRows items in one transaction. Per-row
// audit_log rows share a single `bulk_id` so log queries can re-aggregate
// the bulk operation. Last-write-wins per row (no expected_revision in bulk
// mode) — bulk callers (CSV import) would otherwise have to fetch every
// row's current revision first which would defeat the bulk endpoint's
// purpose.
func (s *Server) HandleBulkUpsertOverrides(w http.ResponseWriter, r *http.Request) {
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
	slug := r.PathValue("slug")
	if slug == "" {
		writeJSONErr(w, http.StatusBadRequest, "missing_slug", "")
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, MaxOverrideBulkBodyBytes))
	if err != nil {
		writeJSONErr(w, http.StatusBadRequest, "read_body", err.Error())
		return
	}
	var req bulkUpsertOverridesRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSONErr(w, http.StatusBadRequest, "invalid_payload", err.Error())
		return
	}
	if len(req.Items) == 0 {
		writeJSONErr(w, http.StatusBadRequest, "invalid_payload", "items required")
		return
	}
	if len(req.Items) > MaxOverrideBulkRows {
		writeJSONErr(w, http.StatusBadRequest, "invalid_payload",
			fmt.Sprintf("max %d items per request", MaxOverrideBulkRows))
		return
	}
	for i, it := range req.Items {
		if it.ScreenID == "" || it.FigmaNodeID == "" {
			writeJSONErr(w, http.StatusBadRequest, "invalid_payload",
				fmt.Sprintf("items[%d]: screen_id + figma_node_id required", i))
			return
		}
		if len(it.Value) > MaxOverrideValueBytes {
			writeJSONErr(w, http.StatusBadRequest, "invalid_payload",
				fmt.Sprintf("items[%d]: value exceeds %d bytes", i, MaxOverrideValueBytes))
			return
		}
	}

	bulkID := uuid.NewString()
	endpoint := fmt.Sprintf("/v1/projects/%s/text-overrides/bulk", slug)
	rows := make([]*BulkOverrideRow, 0, len(req.Items))
	for _, it := range req.Items {
		// Capture loop vars in the closure.
		item := it
		row := &BulkOverrideRow{
			ScreenID:             item.ScreenID,
			FigmaNodeID:          item.FigmaNodeID,
			Value:                item.Value,
			CanonicalPath:        item.CanonicalPath,
			LastSeenOriginalText: item.LastSeenOriginalText,
		}
		row.PerRowAudit = func(tx *sql.Tx, flowID string, oldValue string, newRev int) error {
			return WriteOverrideEvent(r.Context(), tx, OverrideEvent{
				EventType:   AuditActionOverrideTextBulkSet,
				TenantID:    tenantID,
				UserID:      claims.Sub,
				FlowID:      flowID,
				ScreenID:    item.ScreenID,
				FigmaNodeID: item.FigmaNodeID,
				OldValue:    oldValue,
				NewValue:    item.Value,
				BulkID:      bulkID,
				IPAddress:   clientIP(r),
				Endpoint:    endpoint,
				Method:      "POST",
				StatusCode:  http.StatusOK,
			})
		}
		rows = append(rows, row)
	}

	repo := NewTenantRepo(s.deps.DB.DB, tenantID)
	summary, err := repo.BulkUpsertOverrides(r.Context(), slug, rows, claims.Sub)
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "bulk_upsert", err.Error())
		return
	}

	// U10 — one debounced graph rebuild per tenant covers every row in this
	// bulk batch. Multiple flows touched ⇒ multiple enqueues, but the
	// debounce flusher coalesces and the worker re-derives the whole
	// (tenant, platform) slice anyway.
	touchedFlows := map[string]struct{}{}
	for _, row := range rows {
		if row.FlowID != "" {
			touchedFlows[row.FlowID] = struct{}{}
		}
	}
	for flowID := range touchedFlows {
		s.enqueueGraphRebuild(tenantID, GraphSourceFlows, flowID)
	}

	results := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		// Skipped rows have Revision == 0 and FlowID == "".
		if row.FlowID == "" {
			continue
		}
		results = append(results, map[string]any{
			"screen_id":     row.ScreenID,
			"figma_node_id": row.FigmaNodeID,
			"revision":      row.Revision,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"bulk_id": bulkID,
		"updated": summary.Updated,
		"skipped": summary.Skipped,
		"results": results,
	})
}

// ─── U12: CSV bulk export / import ───────────────────────────────────────────
//
// Translation / PM workflow: PM exports every TEXT node in a leaf as a CSV
// (one row per text node), edits in Sheets/Excel, uploads back. Conflicts
// (DB updated_at later than CSV's last_edited_at) are surfaced in the
// response so the client can present a confirm-before-apply modal.
//
// Wire shape:
//   GET  /v1/projects/{slug}/leaves/{leaf_id}/text-overrides/csv
//        → text/csv stream; columns:
//          screen | screen_id | node_path | figma_node_id | original |
//          current | last_edited_by | last_edited_at
//   POST /v1/projects/{slug}/leaves/{leaf_id}/text-overrides/csv
//        multipart/form-data with file field "file"; optional form field
//        "force=true" to skip the conflict short-circuit.
//        → JSON { applied, skipped, conflicts: [...] } or
//                { applied: 0, skipped: 0, conflicts, errors: [...] } on
//                stale-row detection.
//
// Per the SQLite single-writer learning (2026-05-01-003-phase-7-8-closure.md),
// canonical-tree reads + the conflict-detection SELECT happen BEFORE the
// override+audit_log+search write tx — so a chunky CSV import never holds
// the writer lock while it's parsing.

// MaxCSVImportBytes caps the multipart body for CSV import. 5 MB covers
// 1000+ rows of realistic copy with headroom for unicode-heavy strings.
const MaxCSVImportBytes = 5 * 1024 * 1024

// csvImportChunk is the per-call cap when fanning a parsed CSV out to
// BulkUpsertOverrides. Mirrors MaxOverrideBulkRows so each chunk fits in
// one bulk-upsert tx.
const csvImportChunk = MaxOverrideBulkRows

// csvHeader is the canonical CSV column order for both export + import.
// Import tolerates a missing trailing column (last_edited_at) so older
// CSVs round-trip without a forced re-export.
var csvHeader = []string{
	"screen",
	"screen_id",
	"node_path",
	"figma_node_id",
	"original",
	"current",
	"last_edited_by",
	"last_edited_at",
}

// csvLeafScreen pairs a screen with the metadata needed to walk its
// canonical_tree for export. Resolved up-front before any rendering so the
// export streams without holding a transaction.
type csvLeafScreen struct {
	ID    string
	Label string
	Tree  string
}

// HandleCSVExport serves
//   GET /v1/projects/{slug}/leaves/{leaf_id}/text-overrides/csv
//
// Streams a CSV of every TEXT node across every screen in the leaf. The
// `current` column reflects an active override if any, otherwise mirrors
// `original`. Orphaned overrides are still surfaced — translators may
// choose to update them and let the import path re-attach.
func (s *Server) HandleCSVExport(w http.ResponseWriter, r *http.Request) {
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

	repo := NewTenantRepo(s.deps.DB.DB, tenantID)

	screens, err := repo.listLeafScreensForCSV(r.Context(), slug, leafID)
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "list_screens", err.Error())
		return
	}
	overrides, err := repo.ListOverridesByLeaf(r.Context(), slug, leafID)
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "list_overrides", err.Error())
		return
	}
	overrideByKey := make(map[string]ScreenTextOverride, len(overrides))
	for _, o := range overrides {
		overrideByKey[o.ScreenID+"\x00"+o.FigmaNodeID] = o
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(
		`attachment; filename="overrides-%s.csv"`, leafID))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)

	cw := csv.NewWriter(w)
	_ = cw.Write(csvHeader)

	for _, sc := range screens {
		idx, err := indexCanonicalTree(sc.Tree)
		if err != nil {
			// One bad tree shouldn't tank the whole export; log via the
			// CSV itself with a synthetic row so the importer sees the
			// gap. Keep walking the rest of the leaf.
			_ = cw.Write([]string{
				sc.Label, sc.ID, "(error)", "", "(canonical tree parse error)",
				"", "", "",
			})
			continue
		}
		// Stable order: walk by path so the export is reproducible even if
		// Go's map iteration order shifts.
		paths := make([]string, 0, len(idx.byPath))
		for p := range idx.byPath {
			paths = append(paths, p)
		}
		sortStringsByPath(paths)

		for _, p := range paths {
			ref := idx.byPath[p]
			if !isTextNode(ref.raw) {
				continue
			}
			original := stringField(ref.raw, "characters")
			figmaNodeID := stringField(ref.raw, "id")
			current := original
			lastBy := ""
			lastAt := ""
			if ov, ok := overrideByKey[sc.ID+"\x00"+figmaNodeID]; ok {
				current = ov.Value
				lastBy = ov.UpdatedByUserID
				lastAt = ov.UpdatedAt
			}
			_ = cw.Write([]string{
				sc.Label, sc.ID, p, figmaNodeID,
				original, current, lastBy, lastAt,
			})
		}
	}
	cw.Flush()
}

// csvImportRow is the parsed shape of one CSV row keyed by header name. We
// use a struct-of-strings so individual fields are easy to surface in the
// `errors` / `conflicts` arrays without re-walking the original CSV.
type csvImportRow struct {
	RowIndex     int    // 1-based, excluding header
	Screen       string
	ScreenID     string
	NodePath     string
	FigmaNodeID  string
	Original     string
	Current      string
	LastEditedBy string
	LastEditedAt string
}

// csvImportConflict is one row that the server refused to apply because the
// DB version is newer than the CSV's `last_edited_at`. The client renders
// these in a confirm-before-apply modal.
type csvImportConflict struct {
	RowIndex     int    `json:"row_index"`
	ScreenID     string `json:"screen_id"`
	FigmaNodeID  string `json:"figma_node_id"`
	CSVValue     string `json:"csv_value"`
	CurrentValue string `json:"current_value"`
}

// csvImportError is one row that the server couldn't even parse — typically
// missing required columns or unresolvable screen.
type csvImportError struct {
	RowIndex int    `json:"row_index"`
	Reason   string `json:"reason"`
}

// HandleCSVImport serves
//   POST /v1/projects/{slug}/leaves/{leaf_id}/text-overrides/csv
//
// Accepts a multipart upload (field "file") + optional form field
// "force=true". When `force` is unset and any row's last_edited_at predates
// the DB's updated_at, the response carries `conflicts` and `applied=0` so
// the client can present a per-row confirmation modal. When `force=true`
// every dirty row is applied last-write-wins.
func (s *Server) HandleCSVImport(w http.ResponseWriter, r *http.Request) {
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
	slug := r.PathValue("slug")
	leafID := r.PathValue("leaf_id")
	if slug == "" || leafID == "" {
		writeJSONErr(w, http.StatusBadRequest, "missing_path_params", "")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, MaxCSVImportBytes)
	if err := r.ParseMultipartForm(MaxCSVImportBytes); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "too large") {
			writeJSONErr(w, http.StatusRequestEntityTooLarge, "body_too_large",
				fmt.Sprintf("CSV upload exceeds %d bytes", MaxCSVImportBytes))
			return
		}
		writeJSONErr(w, http.StatusBadRequest, "invalid_form", err.Error())
		return
	}
	force := strings.EqualFold(r.FormValue("force"), "true")

	file, _, err := r.FormFile("file")
	if err != nil {
		// Fall back to the raw body if the caller posted text/csv directly.
		// Some sandboxed test runners drop multipart, so we accept the
		// simpler path too.
		if r.Body == nil {
			writeJSONErr(w, http.StatusBadRequest, "missing_file", "expected multipart file field 'file'")
			return
		}
		file = nil
	}
	var reader io.Reader
	if file != nil {
		defer file.Close()
		reader = file
	} else {
		reader = r.Body
	}

	rows, parseErrs := parseCSVImport(reader)
	if len(parseErrs) > 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":  "invalid_csv",
			"errors": parseErrs,
		})
		return
	}
	if len(rows) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{
			"applied":   0,
			"skipped":   0,
			"conflicts": []csvImportConflict{},
		})
		return
	}

	// PRE-WRITE READ: walk every dirty row's current DB state so the
	// conflict check + bulk fan-out happens with a clean snapshot. This
	// avoids holding the SQLite writer lock while we look up 1000 rows
	// (Learning #4 from phase-7-8-closure).
	repo := NewTenantRepo(s.deps.DB.DB, tenantID)

	dirty := make([]csvImportRow, 0, len(rows))
	for _, row := range rows {
		if row.Current == row.Original {
			continue
		}
		if row.ScreenID == "" || row.FigmaNodeID == "" {
			// Treat as skipped — translator probably hand-typed a row that
			// has no anchor. Surface as a reason in errors so the UI can
			// flag it.
			parseErrs = append(parseErrs, csvImportError{
				RowIndex: row.RowIndex,
				Reason:   "missing screen_id or figma_node_id",
			})
			continue
		}
		dirty = append(dirty, row)
	}

	if len(parseErrs) > 0 && len(dirty) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":  "invalid_csv",
			"errors": parseErrs,
		})
		return
	}

	// Conflict detection — pull the current DB value for every dirty row.
	// One round-trip per row at most; for typical CSV uploads (<= 200 rows)
	// this finishes in milliseconds. We could batch via IN-clause but the
	// chunky import path runs every 100ms+ anyway.
	conflicts := make([]csvImportConflict, 0)
	if !force {
		for _, row := range dirty {
			cur := s.lookupOverrideForConflict(r.Context(), repo, row.ScreenID, row.FigmaNodeID)
			if cur.UpdatedAt == "" || row.LastEditedAt == "" {
				continue // no DB row yet, or CSV doesn't carry a timestamp
			}
			csvT, err1 := time.Parse(time.RFC3339, row.LastEditedAt)
			dbT, err2 := time.Parse(time.RFC3339, cur.UpdatedAt)
			if err1 != nil || err2 != nil {
				continue // unparseable timestamps fall through to apply
			}
			if dbT.After(csvT) {
				conflicts = append(conflicts, csvImportConflict{
					RowIndex:     row.RowIndex,
					ScreenID:     row.ScreenID,
					FigmaNodeID:  row.FigmaNodeID,
					CSVValue:     row.Current,
					CurrentValue: cur.Value,
				})
			}
		}
	}
	if len(conflicts) > 0 && !force {
		writeJSON(w, http.StatusOK, map[string]any{
			"applied":   0,
			"skipped":   len(dirty),
			"conflicts": conflicts,
		})
		return
	}

	// Apply in chunks of csvImportChunk (= MaxOverrideBulkRows). Each chunk
	// is one transaction; partial failure of one chunk doesn't roll back
	// chunks that already committed (matches the bulk endpoint's semantics).
	endpoint := fmt.Sprintf("/v1/projects/%s/leaves/%s/text-overrides/csv", slug, leafID)
	bulkID := uuid.NewString()
	applied := 0
	skipped := 0
	touchedFlows := map[string]struct{}{}

	for start := 0; start < len(dirty); start += csvImportChunk {
		end := start + csvImportChunk
		if end > len(dirty) {
			end = len(dirty)
		}
		chunk := dirty[start:end]

		bulkRows := make([]*BulkOverrideRow, 0, len(chunk))
		for _, row := range chunk {
			rowCapture := row
			br := &BulkOverrideRow{
				ScreenID:             rowCapture.ScreenID,
				FigmaNodeID:          rowCapture.FigmaNodeID,
				Value:                rowCapture.Current,
				CanonicalPath:        rowCapture.NodePath,
				LastSeenOriginalText: rowCapture.Original,
			}
			br.PerRowAudit = func(tx *sql.Tx, flowID string, oldValue string, newRev int) error {
				return WriteOverrideEvent(r.Context(), tx, OverrideEvent{
					EventType:   AuditActionOverrideTextBulkSet,
					TenantID:    tenantID,
					UserID:      claims.Sub,
					FlowID:      flowID,
					ScreenID:    rowCapture.ScreenID,
					FigmaNodeID: rowCapture.FigmaNodeID,
					OldValue:    oldValue,
					NewValue:    rowCapture.Current,
					BulkID:      bulkID,
					IPAddress:   clientIP(r),
					Endpoint:    endpoint,
					Method:      "POST",
					StatusCode:  http.StatusOK,
				})
			}
			bulkRows = append(bulkRows, br)
		}

		summary, err := repo.BulkUpsertOverrides(r.Context(), slug, bulkRows, claims.Sub)
		if err != nil {
			writeJSONErr(w, http.StatusInternalServerError, "bulk_upsert", err.Error())
			return
		}
		applied += len(summary.Updated)
		skipped += len(summary.Skipped)
		for _, br := range bulkRows {
			if br.FlowID != "" {
				touchedFlows[br.FlowID] = struct{}{}
			}
		}
	}

	// One debounced graph rebuild per tenant covers every row in this CSV
	// run. The worker re-derives the (tenant, platform) slice anyway so the
	// per-flow enqueue is just a lock-priming nudge.
	for flowID := range touchedFlows {
		s.enqueueGraphRebuild(tenantID, GraphSourceFlows, flowID)
	}

	resp := map[string]any{
		"applied":   applied,
		"skipped":   skipped,
		"bulk_id":   bulkID,
		"conflicts": conflicts,
	}
	if len(parseErrs) > 0 {
		resp["errors"] = parseErrs
	}
	writeJSON(w, http.StatusOK, resp)
}

// parseCSVImport reads + validates an uploaded CSV. Returns (rows, errors)
// where errors is non-empty when at least one row failed to parse — the
// caller surfaces these line-level errors as a 400 so the user can fix
// them in Sheets/Excel without re-uploading the whole file.
func parseCSVImport(r io.Reader) ([]csvImportRow, []csvImportError) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1 // tolerate trailing blank columns
	cr.TrimLeadingSpace = true

	header, err := cr.Read()
	if err == io.EOF {
		return nil, []csvImportError{{RowIndex: 0, Reason: "empty file"}}
	}
	if err != nil {
		return nil, []csvImportError{{RowIndex: 0, Reason: fmt.Sprintf("read header: %v", err)}}
	}
	colIndex := map[string]int{}
	for i, h := range header {
		colIndex[strings.TrimSpace(strings.ToLower(h))] = i
	}
	required := []string{"screen_id", "figma_node_id", "original", "current"}
	for _, c := range required {
		if _, ok := colIndex[c]; !ok {
			return nil, []csvImportError{{
				RowIndex: 0,
				Reason:   fmt.Sprintf("missing required column: %s", c),
			}}
		}
	}

	out := make([]csvImportRow, 0)
	errs := make([]csvImportError, 0)
	rowIdx := 0
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		rowIdx++
		if err != nil {
			errs = append(errs, csvImportError{
				RowIndex: rowIdx,
				Reason:   fmt.Sprintf("parse: %v", err),
			})
			// On malformed rows we keep parsing the rest so the error list
			// is useful — but cap so a totally garbage file doesn't accumulate
			// unbounded errors.
			if len(errs) > 200 {
				break
			}
			continue
		}
		row := csvImportRow{
			RowIndex:     rowIdx,
			Screen:       fieldAt(rec, colIndex, "screen"),
			ScreenID:     fieldAt(rec, colIndex, "screen_id"),
			NodePath:     fieldAt(rec, colIndex, "node_path"),
			FigmaNodeID:  fieldAt(rec, colIndex, "figma_node_id"),
			Original:     fieldAt(rec, colIndex, "original"),
			Current:      fieldAt(rec, colIndex, "current"),
			LastEditedBy: fieldAt(rec, colIndex, "last_edited_by"),
			LastEditedAt: fieldAt(rec, colIndex, "last_edited_at"),
		}
		out = append(out, row)
	}
	return out, errs
}

func fieldAt(rec []string, idx map[string]int, name string) string {
	i, ok := idx[name]
	if !ok || i >= len(rec) {
		return ""
	}
	return rec[i]
}

// listLeafScreensForCSV reads every screen + decoded canonical_tree for a
// leaf in a single round-trip. Tenant-scoped; cross-tenant returns ([], nil).
//
// Per the SQLite single-writer learning, this read happens BEFORE any
// override write tx so a chunky CSV import never starves concurrent
// editors (the tx in HandleCSVImport only opens once parsing + conflict
// detection completes).
func (t *TenantRepo) listLeafScreensForCSV(ctx context.Context, projectSlug, flowID string) ([]csvLeafScreen, error) {
	if t.tenantID == "" {
		return nil, errors.New("projects: tenant_id required")
	}
	rows, err := t.handle().QueryContext(ctx,
		`SELECT s.id, COALESCE(s.screen_logical_id, s.id),
		        COALESCE(sct.canonical_tree, ''),
		        sct.canonical_tree_gz
		   FROM screens s
		   LEFT JOIN screen_canonical_trees sct ON sct.screen_id = s.id
		   JOIN flows f ON f.id = s.flow_id
		   JOIN projects p ON p.id = f.project_id
		  WHERE p.slug = ? AND p.tenant_id = ? AND p.deleted_at IS NULL
		    AND f.id = ? AND s.tenant_id = ?
		  ORDER BY s.created_at`,
		projectSlug, t.tenantID, flowID, t.tenantID,
	)
	if err != nil {
		return nil, fmt.Errorf("list leaf screens for csv: %w", err)
	}
	defer rows.Close()
	var out []csvLeafScreen
	for rows.Next() {
		var sc csvLeafScreen
		var legacy string
		var gz []byte
		if err := rows.Scan(&sc.ID, &sc.Label, &legacy, &gz); err != nil {
			return nil, fmt.Errorf("scan leaf screen: %w", err)
		}
		tree, derr := ResolveCanonicalTree(legacy, gz)
		if derr != nil {
			// Surface as empty tree; export will still emit a row noting
			// the parse error rather than failing the whole download.
			tree = ""
		}
		sc.Tree = tree
		out = append(out, sc)
	}
	return out, rows.Err()
}

// sortStringsByPath orders canonical_path strings so the CSV export is
// reproducible. Paths look like "0", "0.children.1", "0.children.2.children.0";
// a plain string sort puts ".children." segments correctly relative to
// peers but mis-orders multi-digit indices ("10" before "2"). Walk segment
// by segment, parsing each as an int when possible.
func sortStringsByPath(paths []string) {
	sort.SliceStable(paths, func(i, j int) bool {
		as := strings.Split(paths[i], ".")
		bs := strings.Split(paths[j], ".")
		for k := 0; k < len(as) && k < len(bs); k++ {
			ai, aErr := strconv.Atoi(as[k])
			bi, bErr := strconv.Atoi(bs[k])
			if aErr == nil && bErr == nil {
				if ai != bi {
					return ai < bi
				}
				continue
			}
			if as[k] != bs[k] {
				return as[k] < bs[k]
			}
		}
		return len(as) < len(bs)
	})
}

// lookupOverrideForConflict fetches just enough of the current row to
// surface in a 409 body. Best-effort — if the lookup itself fails we
// return zero values rather than masking the original conflict.
func (s *Server) lookupOverrideForConflict(ctx context.Context, repo *TenantRepo, screenID, figmaNodeID string) ScreenTextOverride {
	var out ScreenTextOverride
	_ = repo.handle().QueryRowContext(ctx,
		`SELECT id, screen_id, figma_node_id, canonical_path,
		        last_seen_original_text, value, revision, status,
		        updated_by_user_id, updated_at
		   FROM screen_text_overrides
		  WHERE screen_id = ? AND figma_node_id = ? AND tenant_id = ?
		  LIMIT 1`,
		screenID, figmaNodeID, repo.tenantID,
	).Scan(&out.ID, &out.ScreenID, &out.FigmaNodeID, &out.CanonicalPath,
		&out.LastSeenOriginalText, &out.Value, &out.Revision, &out.Status,
		&out.UpdatedByUserID, &out.UpdatedAt)
	return out
}
