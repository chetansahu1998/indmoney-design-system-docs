package projects

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Phase 6 — Mind-graph data layer.
//
// The mind graph at /atlas reads from a single materialised table,
// `graph_index`, populated by the RebuildGraphIndex worker (graph_rebuild.go).
// This file owns: (a) the Go-side types that mirror the table shape and the
// wire response, and (b) the tenant-scoped repository methods the worker and
// HTTP handler call.
//
// Read pattern: HandleGraphAggregate → TenantRepo.LoadGraph(platform) →
//   single indexed SELECT → explode rows into the wire shape.
// Write pattern: RebuildGraphIndex worker → TenantRepo.UpsertGraphIndexRows
//   inside a sql.Tx so multi-row updates are atomic per (tenant, platform)
//   flush.
//
// See docs/plans/2026-05-02-002-feat-projects-flow-atlas-phase-6-plan.md for
// the design rationale (Data Model section).

// ─── Type discriminators ─────────────────────────────────────────────────────

// GraphNodeKind is the value of graph_index.type. Stored as a free-form TEXT
// column at the SQL layer (no CHECK constraint per Phase 1 forward-only
// discipline), but only these seven values are emitted by the worker.
type GraphNodeKind string

const (
	GraphNodeProduct   GraphNodeKind = "product"
	GraphNodeFolder    GraphNodeKind = "folder"
	GraphNodeFlow      GraphNodeKind = "flow"
	GraphNodePersona   GraphNodeKind = "persona"
	GraphNodeComponent GraphNodeKind = "component"
	GraphNodeToken     GraphNodeKind = "token"
	GraphNodeDecision  GraphNodeKind = "decision"
)

// GraphEdgeClass is the value emitted on the wire as `edge.class`. Hierarchy
// edges are derived from parent_id; the other three are stored as JSON arrays
// on the source row.
type GraphEdgeClass string

const (
	GraphEdgeHierarchy  GraphEdgeClass = "hierarchy"
	GraphEdgeUses       GraphEdgeClass = "uses"
	GraphEdgeBindsTo    GraphEdgeClass = "binds-to"
	GraphEdgeSupersedes GraphEdgeClass = "supersedes"
)

// GraphSourceKind is graph_index.source_kind — used by the worker to scope
// incremental rebuilds to rows backed by a particular source.
type GraphSourceKind string

const (
	GraphSourceProjects  GraphSourceKind = "projects"
	GraphSourceFlows     GraphSourceKind = "flows"
	GraphSourcePersonas  GraphSourceKind = "personas"
	GraphSourceDecisions GraphSourceKind = "decisions"
	GraphSourceManifest  GraphSourceKind = "manifest"
	GraphSourceTokens    GraphSourceKind = "tokens"
	GraphSourceDerived   GraphSourceKind = "derived"
)

// Platform discriminator (R25 — Mobile vs Web are separate IA trees).
const (
	GraphPlatformMobile = "mobile"
	GraphPlatformWeb    = "web"
)

// ─── Storage shape (mirrors the graph_index table) ──────────────────────────

// GraphIndexRow is one row in graph_index. Used by the worker (writes) and
// the handler (reads). JSON edge arrays are unmarshalled on read; consumers
// see []string slices.
type GraphIndexRow struct {
	ID       string
	TenantID string
	Platform string

	Type     GraphNodeKind
	Label    string
	ParentID string // empty when no parent

	EdgesUses       []string
	EdgesBindsTo    []string
	EdgesSupersedes []string

	SeverityCritical int
	SeverityHigh     int
	SeverityMedium   int
	SeverityLow      int
	SeverityInfo     int
	PersonaCount     int
	LastUpdatedAt    time.Time
	LastEditor       string
	OpenURL          string

	SourceKind GraphSourceKind
	SourceRef  string

	MaterializedAt time.Time
}

// ─── Wire shape (response of GET /v1/projects/graph) ─────────────────────────

// GraphAggregate is the JSON returned to the frontend. Edges are exploded
// from the per-row JSON arrays at handler time.
type GraphAggregate struct {
	Nodes       []GraphNode `json:"nodes"`
	Edges       []GraphEdge `json:"edges"`
	GeneratedAt string      `json:"generated_at"`
	Platform    string      `json:"platform"`
	CacheKey    string      `json:"cache_key"`
}

// GraphNode is the frontend-friendly projection of one GraphIndexRow.
type GraphNode struct {
	ID       string      `json:"id"`
	Type     string      `json:"type"`
	Label    string      `json:"label"`
	ParentID string      `json:"parent_id,omitempty"`
	Platform string      `json:"platform"`
	Signal   GraphSignal `json:"signal"`
}

// GraphSignal denormalises everything the hover card needs to render.
type GraphSignal struct {
	SeverityCounts SeverityCounts `json:"severity_counts"`
	PersonaCount   int            `json:"persona_count"`
	LastUpdatedAt  string         `json:"last_updated_at"`
	LastEditor     string         `json:"last_editor,omitempty"`
	OpenURL        string         `json:"open_url,omitempty"`
}

// SeverityCounts mirrors the 5-tier model (Phase 4 lifecycle, status='active').
type SeverityCounts struct {
	Critical int `json:"critical"`
	High     int `json:"high"`
	Medium   int `json:"medium"`
	Low      int `json:"low"`
	Info     int `json:"info"`
}

// GraphEdge is one (source, target, class) triple. The frontend's d3-force-3d
// consumer reads this directly.
type GraphEdge struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Class  string `json:"class"`
}

// ─── Repository methods ──────────────────────────────────────────────────────

// LoadGraph reads every graph_index row for (tenant, platform) and returns
// them in stable order (type, id) so test fixtures stay deterministic. The
// caller is responsible for the wire-shape projection (BuildAggregate below).
//
// One indexed SELECT against idx_graph_index_tenant_platform_type. On a 1500-
// row tenant slice the cold response is ~10ms; the bottleneck is JSON
// unmarshalling of the three edge arrays.
func (t *TenantRepo) LoadGraph(ctx context.Context, platform string) ([]GraphIndexRow, error) {
	if t.tenantID == "" {
		return nil, errors.New("projects: tenant_id required")
	}
	if platform != GraphPlatformMobile && platform != GraphPlatformWeb {
		return nil, fmt.Errorf("projects: invalid platform %q", platform)
	}

	rows, err := t.r.db.QueryContext(ctx,
		`SELECT id, tenant_id, platform, type, label,
		        COALESCE(parent_id, ''),
		        COALESCE(edges_uses_json, ''),
		        COALESCE(edges_binds_to_json, ''),
		        COALESCE(edges_supersedes_json, ''),
		        severity_critical, severity_high, severity_medium, severity_low, severity_info,
		        persona_count, last_updated_at, COALESCE(last_editor, ''), COALESCE(open_url, ''),
		        source_kind, source_ref, materialized_at
		   FROM graph_index
		  WHERE tenant_id = ? AND platform = ?
		  ORDER BY type, id`,
		t.tenantID, platform,
	)
	if err != nil {
		return nil, fmt.Errorf("query graph_index: %w", err)
	}
	defer rows.Close()

	var out []GraphIndexRow
	for rows.Next() {
		var (
			row                 GraphIndexRow
			usesJSON, bindsJSON string
			supersJSON          string
			lastUpdated, materializedAt string
			typeStr             string
			sourceKindStr       string
		)
		if err := rows.Scan(
			&row.ID, &row.TenantID, &row.Platform, &typeStr, &row.Label,
			&row.ParentID,
			&usesJSON, &bindsJSON, &supersJSON,
			&row.SeverityCritical, &row.SeverityHigh, &row.SeverityMedium, &row.SeverityLow, &row.SeverityInfo,
			&row.PersonaCount, &lastUpdated, &row.LastEditor, &row.OpenURL,
			&sourceKindStr, &row.SourceRef, &materializedAt,
		); err != nil {
			return nil, fmt.Errorf("scan graph_index row: %w", err)
		}
		row.Type = GraphNodeKind(typeStr)
		row.SourceKind = GraphSourceKind(sourceKindStr)
		row.LastUpdatedAt = parseTime(lastUpdated)
		row.MaterializedAt = parseTime(materializedAt)
		if usesJSON != "" {
			if err := json.Unmarshal([]byte(usesJSON), &row.EdgesUses); err != nil {
				return nil, fmt.Errorf("unmarshal edges_uses_json on %s: %w", row.ID, err)
			}
		}
		if bindsJSON != "" {
			if err := json.Unmarshal([]byte(bindsJSON), &row.EdgesBindsTo); err != nil {
				return nil, fmt.Errorf("unmarshal edges_binds_to_json on %s: %w", row.ID, err)
			}
		}
		if supersJSON != "" {
			if err := json.Unmarshal([]byte(supersJSON), &row.EdgesSupersedes); err != nil {
				return nil, fmt.Errorf("unmarshal edges_supersedes_json on %s: %w", row.ID, err)
			}
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// UpsertGraphIndexRows writes (insert-or-replace) the given rows. Caller
// passes a transaction so multi-row updates are atomic per flush. Tenant ID
// on each row is validated against t.tenantID — cross-tenant writes are a
// programming error and panic-class.
//
// The worker calls this after computing a fresh batch for a (tenant, platform)
// slice. SQLite's INSERT OR REPLACE is idempotent against the composite PK.
func (t *TenantRepo) UpsertGraphIndexRows(ctx context.Context, tx *sql.Tx, rows []GraphIndexRow) error {
	if t.tenantID == "" {
		return errors.New("projects: tenant_id required")
	}
	if len(rows) == 0 {
		return nil
	}
	stmt, err := tx.PrepareContext(ctx,
		`INSERT OR REPLACE INTO graph_index (
		    id, tenant_id, platform, type, label, parent_id,
		    edges_uses_json, edges_binds_to_json, edges_supersedes_json,
		    severity_critical, severity_high, severity_medium, severity_low, severity_info,
		    persona_count, last_updated_at, last_editor, open_url,
		    source_kind, source_ref, materialized_at
		 ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare upsert: %w", err)
	}
	defer stmt.Close()

	for i := range rows {
		row := &rows[i]
		if row.TenantID == "" {
			row.TenantID = t.tenantID
		} else if row.TenantID != t.tenantID {
			return fmt.Errorf("graph_index: row tenant_id %q != repo tenant_id %q", row.TenantID, t.tenantID)
		}
		usesJSON, err := encodeEdgeJSON(row.EdgesUses)
		if err != nil {
			return fmt.Errorf("encode edges_uses on %s: %w", row.ID, err)
		}
		bindsJSON, err := encodeEdgeJSON(row.EdgesBindsTo)
		if err != nil {
			return fmt.Errorf("encode edges_binds_to on %s: %w", row.ID, err)
		}
		supersJSON, err := encodeEdgeJSON(row.EdgesSupersedes)
		if err != nil {
			return fmt.Errorf("encode edges_supersedes on %s: %w", row.ID, err)
		}
		if row.MaterializedAt.IsZero() {
			row.MaterializedAt = t.now().UTC()
		}
		if row.LastUpdatedAt.IsZero() {
			row.LastUpdatedAt = row.MaterializedAt
		}
		if _, err := stmt.ExecContext(ctx,
			row.ID, row.TenantID, row.Platform, string(row.Type), row.Label,
			nullStringRow(row.ParentID),
			usesJSON, bindsJSON, supersJSON,
			row.SeverityCritical, row.SeverityHigh, row.SeverityMedium, row.SeverityLow, row.SeverityInfo,
			row.PersonaCount, rfc3339(row.LastUpdatedAt), nullStringRow(row.LastEditor), nullStringRow(row.OpenURL),
			string(row.SourceKind), row.SourceRef, rfc3339(row.MaterializedAt),
		); err != nil {
			return fmt.Errorf("upsert graph_index %s: %w", row.ID, err)
		}
	}
	return nil
}

// DeleteGraphIndexBySource removes rows backed by (source_kind, source_ref).
// Used when an upstream source row is hard-deleted (e.g. a project is purged
// — soft delete is the common case but doesn't cascade).
func (t *TenantRepo) DeleteGraphIndexBySource(ctx context.Context, tx *sql.Tx, sourceKind GraphSourceKind, sourceRef string) error {
	if t.tenantID == "" {
		return errors.New("projects: tenant_id required")
	}
	_, err := tx.ExecContext(ctx,
		`DELETE FROM graph_index
		   WHERE tenant_id = ? AND source_kind = ? AND source_ref = ?`,
		t.tenantID, string(sourceKind), sourceRef,
	)
	if err != nil {
		return fmt.Errorf("delete graph_index by source: %w", err)
	}
	return nil
}

// DeleteGraphIndexForPlatform clears all rows for (tenant, platform). Used
// by the cold-backfill command to guarantee a fresh start.
func (t *TenantRepo) DeleteGraphIndexForPlatform(ctx context.Context, tx *sql.Tx, platform string) error {
	if t.tenantID == "" {
		return errors.New("projects: tenant_id required")
	}
	_, err := tx.ExecContext(ctx,
		`DELETE FROM graph_index WHERE tenant_id = ? AND platform = ?`,
		t.tenantID, platform,
	)
	return err
}

// ─── Wire-shape projection ───────────────────────────────────────────────────

// BuildAggregate explodes the per-row storage shape into the (nodes, edges)
// wire format. Called once per HTTP request. The cache_key is derived from
// the latest materialized_at across the slice — the SSE channel emits the
// same value so the frontend can short-circuit re-fetches.
func BuildAggregate(rows []GraphIndexRow, platform string) GraphAggregate {
	nodes := make([]GraphNode, 0, len(rows))
	// Edge slice grows as we visit each row; rough capacity guess (≈3 edges
	// per node) keeps allocations bounded.
	edges := make([]GraphEdge, 0, len(rows)*3)
	var latest time.Time

	for i := range rows {
		row := &rows[i]
		if row.MaterializedAt.After(latest) {
			latest = row.MaterializedAt
		}
		nodes = append(nodes, GraphNode{
			ID:       row.ID,
			Type:     string(row.Type),
			Label:    row.Label,
			ParentID: row.ParentID,
			Platform: row.Platform,
			Signal: GraphSignal{
				SeverityCounts: SeverityCounts{
					Critical: row.SeverityCritical,
					High:     row.SeverityHigh,
					Medium:   row.SeverityMedium,
					Low:      row.SeverityLow,
					Info:     row.SeverityInfo,
				},
				PersonaCount:  row.PersonaCount,
				LastUpdatedAt: rfc3339(row.LastUpdatedAt),
				LastEditor:    row.LastEditor,
				OpenURL:       row.OpenURL,
			},
		})
		// Hierarchy edge (one per row with non-empty parent_id).
		if row.ParentID != "" {
			edges = append(edges, GraphEdge{
				Source: row.ID,
				Target: row.ParentID,
				Class:  string(GraphEdgeHierarchy),
			})
		}
		for _, target := range row.EdgesUses {
			edges = append(edges, GraphEdge{Source: row.ID, Target: target, Class: string(GraphEdgeUses)})
		}
		for _, target := range row.EdgesBindsTo {
			edges = append(edges, GraphEdge{Source: row.ID, Target: target, Class: string(GraphEdgeBindsTo)})
		}
		for _, target := range row.EdgesSupersedes {
			edges = append(edges, GraphEdge{Source: row.ID, Target: target, Class: string(GraphEdgeSupersedes)})
		}
	}

	cacheKey := ""
	if !latest.IsZero() {
		cacheKey = rfc3339(latest)
	}
	return GraphAggregate{
		Nodes:       nodes,
		Edges:       edges,
		GeneratedAt: cacheKey,
		Platform:    platform,
		CacheKey:    cacheKey,
	}
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

// encodeEdgeJSON marshals a string slice to JSON, returning empty string when
// the slice is nil OR empty so the column stays compact (NULL/'' both
// decode-safe).
func encodeEdgeJSON(targets []string) (string, error) {
	if len(targets) == 0 {
		return "", nil
	}
	b, err := json.Marshal(targets)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// nullString converts an empty string to sql.NullString{Valid:false} so the
// column stores NULL rather than an empty TEXT. Mirrors the helper used in
// repository.go for project ownership columns.
func nullStringRow(s string) sql.NullString {
	if s == "" {
		return sql.NullString{Valid: false}
	}
	return sql.NullString{String: s, Valid: true}
}
