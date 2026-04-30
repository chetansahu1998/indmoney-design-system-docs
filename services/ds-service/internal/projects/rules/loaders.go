// Package rules — production loader implementations (resolves the
// TODO(U*-prod-wire) blocks shipped by U2-U6).
//
// Each rule shipped its own loader interface so unit tests could supply
// in-memory fakes without touching the DB. This file ships the *sql.DB-backed
// implementations the worker uses in production.
//
// ─── Phase 2 Variable-resolution caveat ─────────────────────────────────────
//
// Phase 1 stores ONE canonical_tree per screen plus a `modes[]` sidecar
// (`screen_modes`). Per-mode trees with Variables resolved to concrete RGBA
// values are NOT persisted — the spec deferred that work. The TS-side
// resolveTreeForMode.ts mirror in Go is owed.
//
// What this means today:
//   - theme_parity_break: the tree-diff still catches **structural** drift
//     between modes (a node present in one mode's tree but missing from the
//     other; a `type` mismatch; a hand-painted property captured in the
//     stored canonical_tree but absent from the bound side). It does NOT
//     yet catch the case where both modes' trees are identical structurally
//     and only the Variable-resolved values differ — that needs the
//     Variable resolver.
//   - a11y_contrast: same constraint. When `fills[0].color` is on the node
//     directly we compute contrast; when it's bound via `boundVariables`,
//     we emit `a11y_unverifiable` (Info) since we can't resolve here.
//   - cross_persona, a11y_touch_target, flow_graph, component_governance:
//     unaffected — all four work on the stored canonical_tree shape directly.
//
// Until the Go-side Variable resolver lands, the headline AE-2 case
// ("hand-painted dark mode fill") still fires when the painted value is
// captured in the stored tree's mode-specific node — i.e., when Phase 1's
// pipeline persisted dark's tree for that screen instead of light's. For
// the cleanest version of the AE-2 catch, the resolver upgrade is owed.

package rules

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/projects"
)

// ─── Touch-target loader ────────────────────────────────────────────────────

// dbTouchTargetLoader reads screens × canonical_trees from SQLite and decodes
// each tree into map[string]any so the rule can walk it.
type dbTouchTargetLoader struct {
	db       *sql.DB
	tenantID string
}

// NewDBTouchTargetLoader wraps *sql.DB as a TouchTargetTreeLoader. tenantID is
// applied as a WHERE clause filter to keep cross-tenant queries returning
// zero rows (no existence oracle).
func NewDBTouchTargetLoader(db *sql.DB, tenantID string) TouchTargetTreeLoader {
	return &dbTouchTargetLoader{db: db, tenantID: tenantID}
}

func (l *dbTouchTargetLoader) LoadScreenTrees(ctx context.Context, versionID string) ([]TouchTargetScreenTree, error) {
	if l.tenantID == "" {
		return nil, errors.New("rules: tenant_id required for production loader")
	}
	rows, err := l.db.QueryContext(ctx,
		`SELECT s.id, COALESCE(t.canonical_tree, '{}')
		   FROM screens s
		   LEFT JOIN screen_canonical_trees t ON t.screen_id = s.id
		  WHERE s.version_id = ? AND s.tenant_id = ?
		  ORDER BY s.created_at ASC`,
		versionID, l.tenantID,
	)
	if err != nil {
		return nil, fmt.Errorf("touch_target loader: %w", err)
	}
	defer rows.Close()
	var out []TouchTargetScreenTree
	for rows.Next() {
		var screenID, treeJSON string
		if err := rows.Scan(&screenID, &treeJSON); err != nil {
			return nil, err
		}
		out = append(out, TouchTargetScreenTree{
			ScreenID:      screenID,
			CanonicalTree: decodeTree(treeJSON),
		})
	}
	return out, rows.Err()
}

// ─── A11y contrast / theme parity per-mode loader ───────────────────────────

// dbScreenModeLoader reads screens × screen_modes × canonical_trees and emits
// one row per (screen, mode) pair. Until the Go Variable resolver lands, every
// mode of the same screen returns the SAME canonical_tree (the one Phase 1
// persisted) — degraded but defensive.
type dbScreenModeLoader struct {
	db       *sql.DB
	tenantID string
}

// NewDBScreenModeLoader wraps *sql.DB as a ScreenModeLoader.
func NewDBScreenModeLoader(db *sql.DB, tenantID string) ScreenModeLoader {
	return &dbScreenModeLoader{db: db, tenantID: tenantID}
}

func (l *dbScreenModeLoader) LoadScreenModesForVersion(ctx context.Context, versionID string) ([]ScreenModeTree, error) {
	if l.tenantID == "" {
		return nil, errors.New("rules: tenant_id required for production loader")
	}
	rows, err := l.db.QueryContext(ctx,
		`SELECT s.id, m.mode_label, COALESCE(t.canonical_tree, '{}')
		   FROM screens s
		   JOIN screen_modes m       ON m.screen_id = s.id
		   LEFT JOIN screen_canonical_trees t ON t.screen_id = s.id
		  WHERE s.version_id = ? AND s.tenant_id = ?
		  ORDER BY s.created_at ASC, m.mode_label ASC`,
		versionID, l.tenantID,
	)
	if err != nil {
		return nil, fmt.Errorf("screen_mode loader: %w", err)
	}
	defer rows.Close()
	var out []ScreenModeTree
	for rows.Next() {
		var screenID, modeLabel, treeJSON string
		if err := rows.Scan(&screenID, &modeLabel, &treeJSON); err != nil {
			return nil, err
		}
		out = append(out, ScreenModeTree{
			ScreenID:      screenID,
			ModeLabel:     modeLabel,
			CanonicalTree: decodeTree(treeJSON),
		})
	}
	return out, rows.Err()
}

// dbResolvedTreeLoader returns ResolvedScreen rows for theme parity. Same
// degraded-but-defensive behavior as ScreenModeLoader: every mode returns the
// same canonical_tree blob until the Variable resolver ships.
type dbResolvedTreeLoader struct {
	db       *sql.DB
	tenantID string
}

// NewDBResolvedTreeLoader wraps *sql.DB as a ResolvedTreeLoader for theme parity.
func NewDBResolvedTreeLoader(db *sql.DB, tenantID string) ResolvedTreeLoader {
	return &dbResolvedTreeLoader{db: db, tenantID: tenantID}
}

func (l *dbResolvedTreeLoader) LoadResolvedScreens(ctx context.Context, versionID string) ([]ResolvedScreen, error) {
	if l.tenantID == "" {
		return nil, errors.New("rules: tenant_id required for production loader")
	}
	rows, err := l.db.QueryContext(ctx,
		`SELECT s.id, m.mode_label, COALESCE(t.canonical_tree, '{}')
		   FROM screens s
		   JOIN screen_modes m       ON m.screen_id = s.id
		   LEFT JOIN screen_canonical_trees t ON t.screen_id = s.id
		  WHERE s.version_id = ? AND s.tenant_id = ?
		  ORDER BY s.created_at ASC, m.mode_label ASC`,
		versionID, l.tenantID,
	)
	if err != nil {
		return nil, fmt.Errorf("resolved_tree loader: %w", err)
	}
	defer rows.Close()
	var out []ResolvedScreen
	for rows.Next() {
		var screenID, modeLabel, treeJSON string
		if err := rows.Scan(&screenID, &modeLabel, &treeJSON); err != nil {
			return nil, err
		}
		out = append(out, ResolvedScreen{
			ScreenID:     screenID,
			ModeLabel:    modeLabel,
			ResolvedTree: treeJSON,
		})
	}
	return out, rows.Err()
}

// ─── Cross-persona loader ───────────────────────────────────────────────────

// dbFlowsByProjectLoader reads flows × personas × screens for a project at a
// specific version. One FlowWithPersona row per flow (with its screens).
type dbFlowsByProjectLoader struct {
	db       *sql.DB
	tenantID string
}

// NewDBFlowsByProjectLoader wraps *sql.DB as a FlowsByProjectLoader.
func NewDBFlowsByProjectLoader(db *sql.DB, tenantID string) FlowsByProjectLoader {
	return &dbFlowsByProjectLoader{db: db, tenantID: tenantID}
}

func (l *dbFlowsByProjectLoader) LoadFlowsForProjectVersion(ctx context.Context, projectID, versionID string) ([]FlowWithPersona, error) {
	if l.tenantID == "" {
		return nil, errors.New("rules: tenant_id required for production loader")
	}
	flowRows, err := l.db.QueryContext(ctx,
		`SELECT f.id, f.persona_id, COALESCE(p.name, '')
		   FROM flows f
		   LEFT JOIN personas p ON p.id = f.persona_id
		  WHERE f.project_id = ? AND f.tenant_id = ? AND f.deleted_at IS NULL`,
		projectID, l.tenantID,
	)
	if err != nil {
		return nil, fmt.Errorf("flows query: %w", err)
	}
	defer flowRows.Close()

	type flowRow struct {
		id          string
		personaID   *string
		personaName string
	}
	var flows []flowRow
	for flowRows.Next() {
		var fr flowRow
		var pid sql.NullString
		if err := flowRows.Scan(&fr.id, &pid, &fr.personaName); err != nil {
			return nil, err
		}
		if pid.Valid {
			s := pid.String
			fr.personaID = &s
		}
		flows = append(flows, fr)
	}
	if err := flowRows.Err(); err != nil {
		return nil, err
	}

	out := make([]FlowWithPersona, 0, len(flows))
	for _, fr := range flows {
		screens, err := loadScreensWithTreesForFlow(ctx, l.db, l.tenantID, versionID, fr.id)
		if err != nil {
			return nil, fmt.Errorf("screens for flow %s: %w", fr.id, err)
		}
		out = append(out, FlowWithPersona{
			FlowID:      fr.id,
			PersonaID:   fr.personaID,
			PersonaName: fr.personaName,
			Screens:     screens,
		})
	}
	return out, nil
}

// ─── Component-governance loader ────────────────────────────────────────────

// dbScreensWithFlowsLoader reads every screen + flow_id for a version. Used by
// component-governance to group screens by flow for the sprawl rule.
type dbScreensWithFlowsLoader struct {
	db       *sql.DB
	tenantID string
}

// NewDBScreensWithFlowsLoader wraps *sql.DB as a ScreensWithFlowsLoader.
func NewDBScreensWithFlowsLoader(db *sql.DB, tenantID string) ScreensWithFlowsLoader {
	return &dbScreensWithFlowsLoader{db: db, tenantID: tenantID}
}

func (l *dbScreensWithFlowsLoader) LoadScreensWithFlows(ctx context.Context, versionID string) ([]ScreenWithFlow, error) {
	if l.tenantID == "" {
		return nil, errors.New("rules: tenant_id required for production loader")
	}
	rows, err := l.db.QueryContext(ctx,
		`SELECT s.id, s.flow_id, COALESCE(t.canonical_tree, '{}')
		   FROM screens s
		   LEFT JOIN screen_canonical_trees t ON t.screen_id = s.id
		  WHERE s.version_id = ? AND s.tenant_id = ?
		  ORDER BY s.flow_id, s.created_at ASC`,
		versionID, l.tenantID,
	)
	if err != nil {
		return nil, fmt.Errorf("screens_with_flows loader: %w", err)
	}
	defer rows.Close()
	var out []ScreenWithFlow
	for rows.Next() {
		var sw ScreenWithFlow
		if err := rows.Scan(&sw.ScreenID, &sw.FlowID, &sw.CanonicalTree); err != nil {
			return nil, err
		}
		out = append(out, sw)
	}
	return out, rows.Err()
}

// ─── Flow-graph loader ──────────────────────────────────────────────────────

// dbFlowGraphLoader reads screens with file_id (joined via flows) and
// canonical_tree for the flow-graph rule. LoadStartNodeID returns "" today —
// the prototype start node lives in Figma file metadata, which Phase 1 doesn't
// pull. The rule falls back to "first screen by created_at".
type dbFlowGraphLoader struct {
	db       *sql.DB
	tenantID string
}

// NewDBFlowGraphLoader wraps *sql.DB as a FlowGraphLoader.
func NewDBFlowGraphLoader(db *sql.DB, tenantID string) FlowGraphLoader {
	return &dbFlowGraphLoader{db: db, tenantID: tenantID}
}

func (l *dbFlowGraphLoader) LoadScreensForFlowGraph(ctx context.Context, versionID string) ([]ScreenForFlowGraph, error) {
	if l.tenantID == "" {
		return nil, errors.New("rules: tenant_id required for production loader")
	}
	rows, err := l.db.QueryContext(ctx,
		`SELECT s.id, s.flow_id, f.file_id, COALESCE(t.canonical_tree, '{}')
		   FROM screens s
		   JOIN flows f ON f.id = s.flow_id
		   LEFT JOIN screen_canonical_trees t ON t.screen_id = s.id
		  WHERE s.version_id = ? AND s.tenant_id = ?
		  ORDER BY s.created_at ASC`,
		versionID, l.tenantID,
	)
	if err != nil {
		return nil, fmt.Errorf("flow_graph loader: %w", err)
	}
	defer rows.Close()
	var out []ScreenForFlowGraph
	for rows.Next() {
		var s ScreenForFlowGraph
		if err := rows.Scan(&s.ScreenID, &s.FlowID, &s.FileID, &s.CanonicalTree); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// LoadStartNodeID returns empty string + nil error today. The prototype start
// node lives in Figma file metadata which Phase 1's pipeline doesn't pull.
// The flow-graph rule treats empty as "unknown" and falls back to first-screen-
// by-created-at. A follow-up unit can wire this through the Figma client.
func (l *dbFlowGraphLoader) LoadStartNodeID(ctx context.Context, fileID string) (string, error) {
	return "", nil
}

// ─── Shared screen loader (used by FlowsByProject for cross-persona) ────────

// loadScreensWithTreesForFlow returns every screen in a (version, flow) pair
// joined to its canonical_tree blob. Tenant-scoped.
func loadScreensWithTreesForFlow(ctx context.Context, db *sql.DB, tenantID, versionID, flowID string) ([]projects.ScreenWithTree, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT s.id, s.flow_id, COALESCE(t.canonical_tree, '{}')
		   FROM screens s
		   LEFT JOIN screen_canonical_trees t ON t.screen_id = s.id
		  WHERE s.version_id = ? AND s.flow_id = ? AND s.tenant_id = ?
		  ORDER BY s.created_at ASC`,
		versionID, flowID, tenantID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []projects.ScreenWithTree
	for rows.Next() {
		var sc projects.ScreenWithTree
		if err := rows.Scan(&sc.ScreenID, &sc.FlowID, &sc.CanonicalTree); err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}
