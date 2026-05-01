package projects

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Phase 8 U8 — FTS5 search index population.
//
// Lives next to the Phase 6 mind-graph sources because the same worker
// (RebuildGraphIndex) writes both indexes in one transaction. The
// SearchIndexRow shape mirrors search_index_fts column-by-column.
//
// Index lifecycle:
//   - Cold rebuild: BuildSearchRowsForTenant(ctx, db, tenantID) returns
//     every entity. Worker DELETEs the tenant slice from search_index_fts
//     then inserts everything fresh.
//   - Incremental: per-event hooks call UpsertSearchIndexRows with the
//     affected entity row(s). Idempotent via DELETE-then-INSERT keyed on
//     (tenant_id, entity_kind, entity_id).

// SearchIndexRow is one row in search_index_fts.
type SearchIndexRow struct {
	TenantID   string
	EntityKind string // flow | drd | decision | persona | component
	EntityID   string
	OpenURL    string
	Title      string
	Body       string
}

// UpsertSearchIndexRows is the worker write path. SQLite FTS5 doesn't
// support ON CONFLICT, so we DELETE the matching (tenant_id, kind, id)
// rows then INSERT fresh ones. Caller passes a transaction so search +
// graph writes commit atomically.
func UpsertSearchIndexRows(ctx context.Context, tx *sql.Tx, tenantID string, rows []SearchIndexRow) error {
	if tenantID == "" {
		return errors.New("search_index: tenant_id required")
	}
	if len(rows) == 0 {
		return nil
	}
	// Delete by (tenant_id, kind, id) — covers both cold rebuilds (every
	// row in the slice will be re-inserted) and incremental updates (one
	// row's kind+id won't match anything else).
	delStmt, err := tx.PrepareContext(ctx,
		`DELETE FROM search_index_fts
		   WHERE tenant_id = ? AND entity_kind = ? AND entity_id = ?`)
	if err != nil {
		return fmt.Errorf("search prepare delete: %w", err)
	}
	defer delStmt.Close()

	insStmt, err := tx.PrepareContext(ctx,
		`INSERT INTO search_index_fts
		    (tenant_id, entity_kind, entity_id, open_url, title, body)
		 VALUES (?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("search prepare insert: %w", err)
	}
	defer insStmt.Close()

	for i := range rows {
		r := &rows[i]
		if r.TenantID == "" {
			r.TenantID = tenantID
		} else if r.TenantID != tenantID {
			return fmt.Errorf("search_index: row tenant_id %q != caller %q", r.TenantID, tenantID)
		}
		if _, err := delStmt.ExecContext(ctx, r.TenantID, r.EntityKind, r.EntityID); err != nil {
			return fmt.Errorf("search delete %s/%s: %w", r.EntityKind, r.EntityID, err)
		}
		if _, err := insStmt.ExecContext(ctx,
			r.TenantID, r.EntityKind, r.EntityID, r.OpenURL, r.Title, r.Body,
		); err != nil {
			return fmt.Errorf("search insert %s/%s: %w", r.EntityKind, r.EntityID, err)
		}
	}
	return nil
}

// DeleteSearchIndexBySource removes rows by (entity_kind, entity_id). Used
// when an upstream entity is hard-deleted.
func DeleteSearchIndexBySource(ctx context.Context, tx *sql.Tx, tenantID, kind, id string) error {
	_, err := tx.ExecContext(ctx,
		`DELETE FROM search_index_fts
		   WHERE tenant_id = ? AND entity_kind = ? AND entity_id = ?`,
		tenantID, kind, id,
	)
	return err
}

// DeleteSearchIndexForTenant clears every row for a tenant. Used by the
// cold-rebuild path to guarantee a fresh slice.
func DeleteSearchIndexForTenant(ctx context.Context, tx *sql.Tx, tenantID string) error {
	_, err := tx.ExecContext(ctx,
		`DELETE FROM search_index_fts WHERE tenant_id = ?`,
		tenantID,
	)
	return err
}

// ─── Source readers (one per entity kind) ────────────────────────────────

// BuildSearchRowsForTenant runs the per-kind source readers and concatenates
// their output. Used by RebuildGraphIndex for cold rebuilds. Each reader is
// also exported for incremental updates.
func BuildSearchRowsForTenant(ctx context.Context, db *sql.DB, tenantID string) ([]SearchIndexRow, error) {
	if tenantID == "" {
		return nil, errors.New("search_index: tenant_id required")
	}
	var all []SearchIndexRow

	flows, err := buildFlowSearchRows(ctx, db, tenantID)
	if err != nil {
		return nil, fmt.Errorf("flow search rows: %w", err)
	}
	all = append(all, flows...)

	drds, err := buildDRDSearchRows(ctx, db, tenantID)
	if err != nil {
		return nil, fmt.Errorf("drd search rows: %w", err)
	}
	all = append(all, drds...)

	decisions, err := buildDecisionSearchRows(ctx, db, tenantID)
	if err != nil {
		return nil, fmt.Errorf("decision search rows: %w", err)
	}
	all = append(all, decisions...)

	personas := buildPersonaSearchRows(ctx, db, tenantID)
	all = append(all, personas...)

	// Component search rows are per-tenant duplicates of the manifest;
	// they get added by the worker's full-rebuild path which already has
	// the manifest parsed.
	return all, nil
}

func buildFlowSearchRows(ctx context.Context, db *sql.DB, tenantID string) ([]SearchIndexRow, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT f.id, f.name, p.product, p.path, p.slug,
		        COALESCE(per.name, '')
		   FROM flows f
		   JOIN projects p ON p.id = f.project_id
		   LEFT JOIN personas per ON per.id = f.persona_id
		  WHERE f.tenant_id = ?
		    AND f.deleted_at IS NULL
		    AND p.deleted_at IS NULL`,
		tenantID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SearchIndexRow
	for rows.Next() {
		var id, name, product, path, slug, persona string
		if err := rows.Scan(&id, &name, &product, &path, &slug, &persona); err != nil {
			return nil, err
		}
		body := strings.Join([]string{product, path, persona}, " ")
		out = append(out, SearchIndexRow{
			TenantID:   tenantID,
			EntityKind: "flow",
			EntityID:   id,
			OpenURL:    "/projects/" + slug,
			Title:      name,
			Body:       body,
		})
	}
	return out, rows.Err()
}

func buildDRDSearchRows(ctx context.Context, db *sql.DB, tenantID string) ([]SearchIndexRow, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT d.flow_id, f.name, p.slug, d.content_json
		   FROM flow_drd d
		   JOIN flows f ON f.id = d.flow_id
		   JOIN projects p ON p.id = f.project_id
		  WHERE f.tenant_id = ?
		    AND f.deleted_at IS NULL
		    AND p.deleted_at IS NULL`,
		tenantID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SearchIndexRow
	for rows.Next() {
		var flowID, flowName, slug string
		var contentJSON []byte
		if err := rows.Scan(&flowID, &flowName, &slug, &contentJSON); err != nil {
			return nil, err
		}
		body := extractPlainText(contentJSON)
		if body == "" {
			continue
		}
		out = append(out, SearchIndexRow{
			TenantID:   tenantID,
			EntityKind: "drd",
			EntityID:   flowID, // 1:1 with flow
			OpenURL:    "/projects/" + slug + "?tab=drd",
			Title:      flowName + " — DRD",
			Body:       body,
		})
	}
	return out, rows.Err()
}

func buildDecisionSearchRows(ctx context.Context, db *sql.DB, tenantID string) ([]SearchIndexRow, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT d.id, d.flow_id, d.title, d.body_json, p.slug
		   FROM decisions d
		   JOIN flows f ON f.id = d.flow_id
		   JOIN projects p ON p.id = f.project_id
		  WHERE d.tenant_id = ?
		    AND d.deleted_at IS NULL
		    AND f.deleted_at IS NULL
		    AND p.deleted_at IS NULL`,
		tenantID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SearchIndexRow
	for rows.Next() {
		var id, flowID, title, slug string
		var bodyJSON []byte
		if err := rows.Scan(&id, &flowID, &title, &bodyJSON, &slug); err != nil {
			return nil, err
		}
		body := extractPlainText(bodyJSON)
		out = append(out, SearchIndexRow{
			TenantID:   tenantID,
			EntityKind: "decision",
			EntityID:   id,
			OpenURL:    "/projects/" + slug + "?decision=" + id,
			Title:      title,
			Body:       body,
		})
	}
	return out, rows.Err()
}

func buildPersonaSearchRows(ctx context.Context, db *sql.DB, tenantID string) []SearchIndexRow {
	rows, err := db.QueryContext(ctx,
		`SELECT id, name FROM personas WHERE status = 'approved' AND deleted_at IS NULL`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []SearchIndexRow
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			continue
		}
		out = append(out, SearchIndexRow{
			TenantID:   tenantID,
			EntityKind: "persona",
			EntityID:   id,
			OpenURL:    "",
			Title:      name,
			Body:       "",
		})
	}
	return out
}

// BuildComponentSearchRowsFromManifest builds search rows from the parsed
// manifest. Called by the worker's full-rebuild path which already has the
// manifest in memory; we don't re-parse here.
func BuildComponentSearchRowsFromManifest(tenantID string, manifestComponents []ComponentManifestEntry) []SearchIndexRow {
	out := make([]SearchIndexRow, 0, len(manifestComponents))
	for _, c := range manifestComponents {
		body := strings.Join([]string{c.Category, c.Description}, " ")
		out = append(out, SearchIndexRow{
			TenantID:   tenantID,
			EntityKind: "component",
			EntityID:   c.Slug,
			OpenURL:    "/components/" + c.Slug,
			Title:      c.Name,
			Body:       body,
		})
	}
	return out
}

// ComponentManifestEntry is the slice of a manifest entry the search
// index needs. Mirrors the BuildComponentRows reader so the worker can
// reuse a single manifest parse.
type ComponentManifestEntry struct {
	Slug        string
	Name        string
	Category    string
	Description string
}

// ─── BlockNote → plain text extractor ────────────────────────────────────

// extractPlainText walks a BlockNote / Yjs-rendered JSON document and
// returns a space-joined plain-text rendering. Heuristic: any string field
// named "text" or "characters" inside a "content" array is text content.
// We don't try to preserve formatting — FTS5 only indexes tokens.
//
// Empty input or unparseable JSON returns "" — search just shows no hits
// for that entity, which is the correct user-facing behaviour.
func extractPlainText(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return ""
	}
	var b strings.Builder
	walkPlainText(doc, &b)
	return strings.TrimSpace(b.String())
}

func walkPlainText(node any, b *strings.Builder) {
	switch v := node.(type) {
	case string:
		// Bare strings that aren't IDs land here when the tree is heterogeneous.
		// Skip — we want only deliberately-marked text leaves.
		_ = v
	case []any:
		for _, child := range v {
			walkPlainText(child, b)
		}
	case map[string]any:
		// Direct text fields BlockNote / Yjs commonly use.
		if t, ok := v["text"].(string); ok && t != "" {
			b.WriteString(t)
			b.WriteByte(' ')
		}
		if t, ok := v["characters"].(string); ok && t != "" {
			b.WriteString(t)
			b.WriteByte(' ')
		}
		// Recurse into common containers.
		if c, ok := v["content"]; ok {
			walkPlainText(c, b)
		}
		if c, ok := v["children"]; ok {
			walkPlainText(c, b)
		}
		if c, ok := v["body"]; ok {
			walkPlainText(c, b)
		}
		// BlockNote 0.49 stores doc.content[].content[] for nested blocks.
		if c, ok := v["blocks"]; ok {
			walkPlainText(c, b)
		}
	}
}
