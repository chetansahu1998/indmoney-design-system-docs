package projects

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/auth"
)

// Phase 7.5 / U3 — taxonomy curator.
//
// The canonical_taxonomy table holds the authoritative Product → folder
// tree per tenant. Designer exports land in `projects` with arbitrary
// `path` values; paths not yet in canonical_taxonomy are "designer-
// extended" — surfaced to admins for promotion or archival.
//
// v1 ships a flat list view (canonical + extended); the drag-tree editor
// is a future polish. Promote / archive are the two actions an admin
// performs.

// TaxonomyEntry is a row in either canonical_taxonomy or — for "extended"
// rows — a synthetic stand-in derived from `projects.path`. OrderIndex
// (Phase 7.6) is the per-(tenant, product) drag-to-reorder slot; extended
// rows get OrderIndex=0 (sort to top) until promoted.
type TaxonomyEntry struct {
	Product    string `json:"product"`
	Path       string `json:"path"`
	Canonical  bool   `json:"canonical"`
	ArchivedAt string `json:"archived_at,omitempty"`
	FlowCount  int    `json:"flow_count,omitempty"`
	OrderIndex int    `json:"order_index"`
}

// listTaxonomy returns the union of canonical_taxonomy + designer-extended
// path strings (paths in `projects` that aren't yet in canonical_taxonomy).
// Tenant-scoped.
func listTaxonomy(ctx context.Context, db *sql.DB, tenantID string) ([]TaxonomyEntry, error) {
	canon := map[string]TaxonomyEntry{}
	rows, err := db.QueryContext(ctx,
		`SELECT product, path, COALESCE(archived_at, ''), order_index
		   FROM canonical_taxonomy
		  WHERE tenant_id = ?
		  ORDER BY product, order_index, path`,
		tenantID,
	)
	if err != nil {
		return nil, fmt.Errorf("list canonical_taxonomy: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var e TaxonomyEntry
		if err := rows.Scan(&e.Product, &e.Path, &e.ArchivedAt, &e.OrderIndex); err != nil {
			return nil, err
		}
		e.Canonical = true
		canon[e.Product+"\x00"+e.Path] = e
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Designer-extended: distinct (product, path) from projects, minus
	// what's already in canonical. Group-by includes a flow count for
	// quick triage.
	rows2, err := db.QueryContext(ctx,
		`SELECT product, path, COUNT(*) as flow_count
		   FROM projects
		  WHERE tenant_id = ? AND deleted_at IS NULL
		  GROUP BY product, path`,
		tenantID,
	)
	if err != nil {
		return nil, fmt.Errorf("list extended taxonomy: %w", err)
	}
	defer rows2.Close()
	for rows2.Next() {
		var e TaxonomyEntry
		if err := rows2.Scan(&e.Product, &e.Path, &e.FlowCount); err != nil {
			return nil, err
		}
		key := e.Product + "\x00" + e.Path
		if existing, ok := canon[key]; ok {
			existing.FlowCount = e.FlowCount
			canon[key] = existing
			continue
		}
		canon[key] = e
	}
	if err := rows2.Err(); err != nil {
		return nil, err
	}

	out := make([]TaxonomyEntry, 0, len(canon))
	for _, e := range canon {
		out = append(out, e)
	}
	return out, nil
}

// promoteTaxonomy inserts a (product, path) row into canonical_taxonomy.
// Idempotent via the composite PK; ON CONFLICT clears archived_at so
// re-promoting an archived path "un-archives" it.
func promoteTaxonomy(ctx context.Context, db *sql.DB, tenantID, product, path, promoterID string) error {
	now := time.Now().UTC()
	_, err := db.ExecContext(ctx,
		`INSERT INTO canonical_taxonomy (tenant_id, product, path, promoted_by, promoted_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(tenant_id, product, path) DO UPDATE
		   SET archived_at = NULL,
		       promoted_by = excluded.promoted_by,
		       promoted_at = excluded.promoted_at`,
		tenantID, product, path, promoterID, rfc3339(now),
	)
	if err != nil {
		return fmt.Errorf("promote taxonomy: %w", err)
	}
	return nil
}

// archiveTaxonomy sets archived_at on the canonical row. The row stays
// for audit; flows under that path keep working but the path is marked
// "archived" in the listing.
func archiveTaxonomy(ctx context.Context, db *sql.DB, tenantID, product, path string) error {
	now := time.Now().UTC()
	res, err := db.ExecContext(ctx,
		`UPDATE canonical_taxonomy
		    SET archived_at = ?
		  WHERE tenant_id = ? AND product = ? AND path = ?`,
		rfc3339(now), tenantID, product, path,
	)
	if err != nil {
		return fmt.Errorf("archive taxonomy: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// HandleAdminListTaxonomy serves GET /v1/atlas/admin/taxonomy.
func (s *Server) HandleAdminListTaxonomy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET only")
		return
	}
	tenantID, ok := s.requireAdminTenant(w, r)
	if !ok {
		return
	}
	entries, err := listTaxonomy(r.Context(), s.deps.DB.DB, tenantID)
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"taxonomy": entries})
}

// HandleAdminPromoteTaxonomy serves POST /v1/atlas/admin/taxonomy/promote.
// Body: {"product": "...", "path": "..."}
func (s *Server) HandleAdminPromoteTaxonomy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST only")
		return
	}
	tenantID, ok := s.requireAdminTenant(w, r)
	if !ok {
		return
	}
	// claims is still needed below for promoter audit (claims.Sub).
	claims, _ := r.Context().Value(ctxKeyClaims).(*auth.Claims)
	var body struct {
		Product string `json:"product"`
		Path    string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Product == "" {
		writeJSONErr(w, http.StatusBadRequest, "invalid_body", "product required")
		return
	}
	if err := promoteTaxonomy(r.Context(), s.deps.DB.DB, tenantID, body.Product, body.Path, claims.Sub); err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "promote_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ErrReorderMixedProducts is returned when a reorder payload contains
// entries from more than one product. Reorder is a sibling-group
// operation; mixing products is a programming error from the client.
var ErrReorderMixedProducts = errors.New("reorder: entries must all belong to a single product")

// reorderTaxonomy writes contiguous order_index values for the given
// sibling list. Each entry's (product, path) must already exist in
// canonical_taxonomy — promote first, reorder after. All entries must
// belong to the same (tenant, product); a payload spanning products is
// rejected with ErrReorderMixedProducts.
//
// Wrapped in a single transaction so a partial-write doesn't leave the
// tree in a half-sorted state.
func reorderTaxonomy(ctx context.Context, db *sql.DB, tenantID string, entries []reorderEntry) error {
	if len(entries) == 0 {
		return nil
	}
	// Single-product invariant: every entry's product must match the first.
	product := entries[0].Product
	if product == "" {
		return fmt.Errorf("reorder: entry product cannot be empty")
	}
	for _, e := range entries[1:] {
		if e.Product != product {
			return ErrReorderMixedProducts
		}
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx,
		`UPDATE canonical_taxonomy
		    SET order_index = ?
		  WHERE tenant_id = ? AND product = ? AND path = ?`)
	if err != nil {
		return fmt.Errorf("prepare reorder: %w", err)
	}
	defer stmt.Close()
	for i, e := range entries {
		if _, err := stmt.ExecContext(ctx, i, tenantID, e.Product, e.Path); err != nil {
			return fmt.Errorf("reorder %s/%s: %w", e.Product, e.Path, err)
		}
	}
	return tx.Commit()
}

type reorderEntry struct {
	Product string `json:"product"`
	Path    string `json:"path"`
}

// HandleAdminReorderTaxonomy serves POST /v1/atlas/admin/taxonomy/reorder.
// Body: {"entries": [{"product": "...", "path": "..."}, ...]} — the array
// order is the new order_index sequence (0..N).
func (s *Server) HandleAdminReorderTaxonomy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST only")
		return
	}
	tenantID, ok := s.requireAdminTenant(w, r)
	if !ok {
		return
	}
	var body struct {
		Entries []reorderEntry `json:"entries"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONErr(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	if err := reorderTaxonomy(r.Context(), s.deps.DB.DB, tenantID, body.Entries); err != nil {
		if errors.Is(err, ErrReorderMixedProducts) {
			writeJSONErr(w, http.StatusBadRequest, "mixed_products", err.Error())
			return
		}
		writeJSONErr(w, http.StatusInternalServerError, "reorder_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "count": len(body.Entries)})
}

// HandleAdminArchiveTaxonomy serves POST /v1/atlas/admin/taxonomy/archive.
func (s *Server) HandleAdminArchiveTaxonomy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST only")
		return
	}
	tenantID, ok := s.requireAdminTenant(w, r)
	if !ok {
		return
	}
	var body struct {
		Product string `json:"product"`
		Path    string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Product == "" {
		writeJSONErr(w, http.StatusBadRequest, "invalid_body", "product required")
		return
	}
	if err := archiveTaxonomy(r.Context(), s.deps.DB.DB, tenantID, body.Product, body.Path); err != nil {
		if errors.Is(err, ErrNotFound) {
			writeJSONErr(w, http.StatusNotFound, "not_canonical", "path is not in canonical_taxonomy")
			return
		}
		writeJSONErr(w, http.StatusInternalServerError, "archive_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
