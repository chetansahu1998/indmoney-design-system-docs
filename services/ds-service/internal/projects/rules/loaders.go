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
	"encoding/json"
	"errors"
	"fmt"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/projects"
)

// annotateResolved attaches a `_resolved` sidecar to the base tree's root
// keyed by node_id → field → ResolvedValue. Phase 3.5 introduces this
// shape so theme_parity (resolved-tree diff) and a11y_contrast (per-fill
// resolved hex lookup) can both read mode-specific values without
// re-running the resolver. The original tree shape is preserved
// untouched — the sidecar lives at the root level under a leading
// underscore key so canonical-tree consumers ignore it.
//
// Returns a SHALLOW-cloned root with the sidecar key set; nested nodes
// are reused by reference.
func annotateResolved(
	base map[string]any,
	resolved map[string]map[string]*ResolvedValue,
) map[string]any {
	if base == nil {
		return map[string]any{"_resolved": resolved}
	}
	out := make(map[string]any, len(base)+1)
	for k, v := range base {
		out[k] = v
	}
	out["_resolved"] = resolved
	return out
}

// encodeTree marshals a (possibly annotated) tree back to JSON. Used by
// the resolved-tree loader to emit the per-mode JSON the
// ResolvedScreen.ResolvedTree field carries.
func encodeTree(tree map[string]any) (string, error) {
	bs, err := json.Marshal(tree)
	if err != nil {
		return "", err
	}
	return string(bs), nil
}

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
		`SELECT s.id, COALESCE(t.canonical_tree, ''), t.canonical_tree_gz
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
		var screenID, legacy string
		var gz []byte
		if err := rows.Scan(&screenID, &legacy, &gz); err != nil {
			return nil, err
		}
		treeJSON, err := projects.ResolveCanonicalTree(legacy, gz)
		if err != nil {
			return nil, fmt.Errorf("touch_target loader: decompress %s: %w", screenID, err)
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
		`SELECT s.id, m.mode_label, m.explicit_variable_modes_json,
		        COALESCE(t.canonical_tree, ''), t.canonical_tree_gz
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

	// Group rows by screen so we can build a per-screen list of mode bindings
	// before resolving — the resolver needs all modes for a screen up-front to
	// pick the active one.
	type screenAccum struct {
		screenID string
		modes    []ModeBindings
		base     map[string]any
	}
	bySID := map[string]*screenAccum{}
	order := []string{}
	for rows.Next() {
		var screenID, modeLabel, varJSON, legacy string
		var gz []byte
		if err := rows.Scan(&screenID, &modeLabel, &varJSON, &legacy, &gz); err != nil {
			return nil, err
		}
		treeJSON, err := projects.ResolveCanonicalTree(legacy, gz)
		if err != nil {
			return nil, fmt.Errorf("screen_mode loader: decompress %s: %w", screenID, err)
		}
		acc, ok := bySID[screenID]
		if !ok {
			acc = &screenAccum{
				screenID: screenID,
				base:     decodeTree(treeJSON),
			}
			bySID[screenID] = acc
			order = append(order, screenID)
		}
		acc.modes = append(acc.modes, ModeBindings{
			Label:  modeLabel,
			Values: ParseVariableValueMap(varJSON),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Phase 3.5 — emit one ScreenModeTree per (screen, mode). The base tree
	// is shared across modes; the active resolution is what the rule reads
	// when it walks bindings. We attach a hidden _resolved sidecar map at
	// the root so a11y_contrast can ask "what's mode X's hex for binding Y?"
	// without re-running the resolver.
	out := make([]ScreenModeTree, 0, len(bySID))
	for _, sid := range order {
		acc := bySID[sid]
		for _, mb := range acc.modes {
			resolver := NewModeResolver(mb.Label, acc.modes)
			resolved := ResolveTreeForMode(acc.base, resolver)
			treeCopy := annotateResolved(acc.base, resolved)
			out = append(out, ScreenModeTree{
				ScreenID:      sid,
				ModeLabel:     mb.Label,
				CanonicalTree: treeCopy,
			})
		}
	}
	return out, nil
}

// dbResolvedTreeLoader returns ResolvedScreen rows for theme parity.
// Phase 3.5 — applies the Variable resolver per-mode so the ResolvedTree
// JSON carries each binding's resolved value. theme_parity.Diff() can
// then catch the AE-2 case (hand-painted dark fill while light is bound):
// the resolved color differs on the offending node even though the raw
// tree structure is identical.
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
		`SELECT s.id, m.mode_label, m.explicit_variable_modes_json,
		        COALESCE(t.canonical_tree, ''), t.canonical_tree_gz
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

	// Same per-screen accumulation as dbScreenModeLoader so each screen's
	// per-mode resolution sees ALL its modes when the resolver constructs.
	type screenAccum struct {
		modes    []ModeBindings
		baseJSON string
	}
	bySID := map[string]*screenAccum{}
	order := []string{}
	for rows.Next() {
		var screenID, modeLabel, varJSON, legacy string
		var gz []byte
		if err := rows.Scan(&screenID, &modeLabel, &varJSON, &legacy, &gz); err != nil {
			return nil, err
		}
		treeJSON, err := projects.ResolveCanonicalTree(legacy, gz)
		if err != nil {
			return nil, fmt.Errorf("resolved_tree loader: decompress %s: %w", screenID, err)
		}
		acc, ok := bySID[screenID]
		if !ok {
			acc = &screenAccum{baseJSON: treeJSON}
			bySID[screenID] = acc
			order = append(order, screenID)
		}
		acc.modes = append(acc.modes, ModeBindings{
			Label:  modeLabel,
			Values: ParseVariableValueMap(varJSON),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Emit one ResolvedScreen per (screen, mode). ResolvedTree is the JSON
	// of the base canonical_tree with a `_resolved_<mode>` sidecar map at
	// the root carrying every (node_id, field) → resolved-hex binding.
	// theme_parity's tree-diff strips boundVariables (so unbound divergences
	// don't false-positive) but reads the sidecar to compare resolved
	// values across modes.
	out := make([]ResolvedScreen, 0, len(bySID))
	for _, sid := range order {
		acc := bySID[sid]
		base := decodeTree(acc.baseJSON)
		for _, mb := range acc.modes {
			resolver := NewModeResolver(mb.Label, acc.modes)
			resolved := ResolveTreeForMode(base, resolver)
			annotated := annotateResolved(base, resolved)
			treeJSON, err := encodeTree(annotated)
			if err != nil {
				return nil, fmt.Errorf("encode resolved tree: %w", err)
			}
			out = append(out, ResolvedScreen{
				ScreenID:     sid,
				ModeLabel:    mb.Label,
				ResolvedTree: treeJSON,
			})
		}
	}
	return out, nil
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
		`SELECT s.id, s.flow_id, COALESCE(t.canonical_tree, ''), t.canonical_tree_gz
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
		var legacy string
		var gz []byte
		if err := rows.Scan(&sw.ScreenID, &sw.FlowID, &legacy, &gz); err != nil {
			return nil, err
		}
		tree, err := projects.ResolveCanonicalTree(legacy, gz)
		if err != nil {
			return nil, fmt.Errorf("screens_with_flows loader: decompress %s: %w", sw.ScreenID, err)
		}
		sw.CanonicalTree = tree
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
		`SELECT s.id, s.flow_id, f.file_id, COALESCE(t.canonical_tree, ''), t.canonical_tree_gz
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
		var legacy string
		var gz []byte
		if err := rows.Scan(&s.ScreenID, &s.FlowID, &s.FileID, &legacy, &gz); err != nil {
			return nil, err
		}
		tree, err := projects.ResolveCanonicalTree(legacy, gz)
		if err != nil {
			return nil, fmt.Errorf("flow_graph loader: decompress %s: %w", s.ScreenID, err)
		}
		s.CanonicalTree = tree
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
