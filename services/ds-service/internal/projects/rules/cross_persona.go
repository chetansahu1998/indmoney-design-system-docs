// Package rules implements Phase 2 audit-engine rule classes — the cross-
// persona consistency rule, theme-parity, WCAG accessibility, and flow-graph
// rules. Each rule type implements the projects.RuleRunner interface so the
// worker pool can fan out across them and merge violations before persisting.
//
// Rules are pure: they read canonical_tree JSON via small loader interfaces
// and never write to the database. The worker owns persistence.
package rules

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/projects"
)

// ─── Loader interface (test-injectable) ──────────────────────────────────────

// FlowsByProjectLoader returns every flow under a project at the given
// version, paired with the persona row and the screens (each carrying its
// canonical_tree JSON). The CrossPersonaRunner needs the persona axis to
// build per-persona component sets, then pair-wise compare them.
//
// Implementations live in two places:
//   - The production SQL loader (TODO U3-prod-wire below) — wired by the
//     orchestrator alongside U2's loader.
//   - In-memory test fakes that synthesize FlowWithPersona slices directly.
type FlowsByProjectLoader interface {
	LoadFlowsForProjectVersion(ctx context.Context, projectID, versionID string) ([]FlowWithPersona, error)
}

// FlowWithPersona is one flow + its persona row + its screens. PersonaID is a
// pointer to model the schema's nullable column (single-persona flows have
// nil); PersonaName is the resolved label used in violation suggestions.
type FlowWithPersona struct {
	FlowID      string
	PersonaID   *string
	PersonaName string
	Screens     []projects.ScreenWithTree
}

// ─── Runner ──────────────────────────────────────────────────────────────────

// CrossPersonaRunner implements projects.RuleRunner for the cross-persona
// consistency rule (U3 / R7 cross-persona class / AE-3).
//
// Algorithm:
//
//  1. Load all flows for the version's project.
//  2. Group flows by persona — flows under the same project with different
//     persona_id form the cross-persona set (personas under the same project
//     share the project's path).
//  3. For each persona, walk every screen's canonical_tree and collect the
//     set of componentSetKey strings on every INSTANCE node. Track each key's
//     first-seen component name for use in the violation message.
//  4. Pair-wise compare across personas: for each component present in
//     persona A but missing in persona B, emit one High violation on B's
//     flow.
//
// Skip cases:
//   - Solo persona (only one persona group OR only one flow under the
//     project): no comparison possible. Zero violations.
//   - No INSTANCE nodes anywhere: zero comparable components. Zero violations.
//   - One persona has no components: emit violations for every component the
//     other personas have — this is exactly the gap cross-persona catches.
type CrossPersonaRunner struct {
	loader FlowsByProjectLoader
}

// NewCrossPersonaRunner constructs the U3 RuleRunner. The loader is injected
// so tests can supply an in-memory fake without touching SQL.
func NewCrossPersonaRunner(loader FlowsByProjectLoader) *CrossPersonaRunner {
	return &CrossPersonaRunner{loader: loader}
}

// Run implements projects.RuleRunner.
func (r *CrossPersonaRunner) Run(ctx context.Context, v *projects.ProjectVersion) ([]projects.Violation, error) {
	if v == nil {
		return nil, fmt.Errorf("CrossPersonaRunner: nil version")
	}
	if r.loader == nil {
		return nil, fmt.Errorf("CrossPersonaRunner: loader not configured")
	}

	flows, err := r.loader.LoadFlowsForProjectVersion(ctx, v.ProjectID, v.ID)
	if err != nil {
		return nil, fmt.Errorf("load flows for project version: %w", err)
	}

	// Group flows by persona. Flows with nil PersonaID land in a synthetic
	// "<no-persona>" bucket; if every flow shares a single bucket we skip.
	groups := groupFlowsByPersona(flows)
	if len(groups) < 2 {
		return nil, nil
	}

	// Build the per-persona componentSet set + first-seen name map.
	type personaComponents struct {
		personaID    *string
		personaName  string
		flowID       string
		firstScreen  string // screen_id used for the violation row
		set          map[string]string // componentSetKey → component name (first-seen)
	}
	personas := make([]personaComponents, 0, len(groups))
	for _, g := range groups {
		pc := personaComponents{
			personaID:   g.personaID,
			personaName: g.personaName,
			flowID:      g.flowID,
			firstScreen: g.firstScreen,
			set:         map[string]string{},
		}
		for _, screen := range g.screens {
			tree := decodeTree(screen.CanonicalTree)
			collectInstances(tree, pc.set)
		}
		personas = append(personas, pc)
	}

	// Short-circuit: if no persona has any components, nothing to compare.
	anyHasComponents := false
	for _, p := range personas {
		if len(p.set) > 0 {
			anyHasComponents = true
			break
		}
	}
	if !anyHasComponents {
		return nil, nil
	}

	// For each persona B, the gap set is (union of every other persona's
	// componentSet) MINUS B's componentSet. Emit one violation on B per
	// missing key. Pair-wise iteration with deduplication via the gap map
	// keeps the output stable when 3+ personas share a gap (e.g. KYC is
	// missing component B, which both Default and Logged-out have — we
	// surface that as a single violation, not two).
	out := make([]projects.Violation, 0)
	for j := range personas {
		b := personas[j]
		// Collect the deduplicated gap: setKey → first-seen component name.
		// "First-seen" walks the personas slice in order so the resulting
		// name is deterministic across runs (the loader is expected to
		// return personas in a stable order — created_at ASC is the
		// production loader's contract).
		gap := map[string]string{}
		for i := range personas {
			if i == j {
				continue
			}
			a := personas[i]
			for setKey, compName := range a.set {
				if _, present := b.set[setKey]; present {
					continue
				}
				if _, alreadyGapped := gap[setKey]; !alreadyGapped {
					gap[setKey] = compName
				}
			}
		}
		for _, compName := range gap {
			// Violation row attached to persona B's flow's first screen.
			// The Phase 1 violations schema requires a screen_id; the
			// cross-persona rule fires at flow-level so we use B's first
			// screen as the anchor.
			vio := projects.Violation{
				ID:         uuid.NewString(),
				VersionID:  v.ID,
				ScreenID:   b.firstScreen,
				TenantID:   v.TenantID,
				RuleID:     "cross_persona_component_gap",
				Severity:   "high",
				Category:   "cross_persona",
				Property:   "component_coverage",
				Observed:   compName + " missing",
				Suggestion: "Add " + compName + " to " + b.personaName + " or Acknowledge as deliberate",
				PersonaID:  b.personaID,
				Status:     "active",
			}
			out = append(out, vio)
		}
	}
	return out, nil
}

// ─── Internals ───────────────────────────────────────────────────────────────

// flowGroup buckets flows by persona for the pair-wise compare. Each persona
// surfaces as one group even if it owns multiple flows under the project.
type flowGroup struct {
	personaID   *string
	personaName string
	flowID      string                   // representative flow id (first seen)
	firstScreen string                   // representative screen id for violation rows
	screens     []projects.ScreenWithTree
}

// groupFlowsByPersona reduces a flat flow list into one group per distinct
// persona_id. Flows with nil PersonaID are bucketed by the synthetic key
// "<no-persona>" so they form at most one solo group.
func groupFlowsByPersona(flows []FlowWithPersona) []flowGroup {
	idx := map[string]int{}
	out := make([]flowGroup, 0)
	for _, f := range flows {
		key := "<no-persona>"
		if f.PersonaID != nil {
			key = *f.PersonaID
		}
		i, ok := idx[key]
		if !ok {
			fs := ""
			if len(f.Screens) > 0 {
				fs = f.Screens[0].ScreenID
			}
			g := flowGroup{
				personaID:   f.PersonaID,
				personaName: f.PersonaName,
				flowID:      f.FlowID,
				firstScreen: fs,
				screens:     append([]projects.ScreenWithTree{}, f.Screens...),
			}
			idx[key] = len(out)
			out = append(out, g)
		} else {
			out[i].screens = append(out[i].screens, f.Screens...)
			if out[i].firstScreen == "" && len(f.Screens) > 0 {
				out[i].firstScreen = f.Screens[0].ScreenID
			}
		}
	}
	return out
}

// decodeTree parses a canonical-tree JSON blob, returning nil on parse failure
// or empty input. Mirrors the Phase 1 pattern in projects/runner.go so rules
// degrade gracefully on missing or malformed trees rather than crashing.
func decodeTree(s string) map[string]any {
	if s == "" || s == "{}" {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil
	}
	return m
}

// collectInstances walks a canonical_tree node and every descendant, adding
// each INSTANCE node's componentSetKey to set with its first-seen name as the
// value. Non-INSTANCE nodes (FRAME / GROUP / RECTANGLE / TEXT / etc.) are
// recursed into but do not contribute keys. INSTANCE nodes lacking any usable
// key field are skipped (degraded fixtures shouldn't crash the run).
func collectInstances(node any, set map[string]string) {
	if node == nil {
		return
	}
	m, ok := node.(map[string]any)
	if !ok {
		// Could be a []any (children array) — handle that.
		if arr, ok := node.([]any); ok {
			for _, c := range arr {
				collectInstances(c, set)
			}
		}
		return
	}
	if t, _ := m["type"].(string); t == "INSTANCE" {
		if key, name := componentSetKeyOf(m); key != "" {
			if _, exists := set[key]; !exists {
				set[key] = name
			}
		}
	}
	// Recurse into children (always — INSTANCE nodes can themselves contain
	// nested instances, e.g., a Card with a Button inside).
	if children, ok := m["children"].([]any); ok {
		for _, c := range children {
			collectInstances(c, set)
		}
	}
}

// componentSetKeyOf extracts the durable identifier + display name from an
// INSTANCE node, in priority order:
//
//  1. mainComponent.componentSetKey — preferred (most stable; survives
//     component-set republishes).
//  2. componentSetId — fallback when mainComponent isn't inlined.
//  3. componentId — last resort. Component-level keys are less stable than
//     component-set keys but are better than skipping the instance entirely.
//
// If none of the three are present, returns ("", "") and the caller skips
// this instance.
//
// Name resolution mirrors the key cascade: prefer mainComponent.name when
// the instance pulled its key from there; otherwise fall back to the
// instance's own name.
func componentSetKeyOf(inst map[string]any) (string, string) {
	if mc, ok := inst["mainComponent"].(map[string]any); ok {
		if k, _ := mc["componentSetKey"].(string); k != "" {
			name, _ := mc["name"].(string)
			if name == "" {
				name, _ = inst["name"].(string)
			}
			return k, name
		}
	}
	if k, _ := inst["componentSetId"].(string); k != "" {
		name, _ := inst["name"].(string)
		return k, name
	}
	if k, _ := inst["componentId"].(string); k != "" {
		name, _ := inst["name"].(string)
		return k, name
	}
	return "", ""
}

// ─── Production wire-up (deferred) ───────────────────────────────────────────
//
// TODO(U3-prod-wire): The production FlowsByProjectLoader implementation —
// reading flows JOIN personas + flows ↔ screens ↔ screen_canonical_trees —
// belongs to the orchestrator unit that wires all Phase 2 rules into the
// worker pool. That unit lands alongside U2's loader so a single PR threads
// every rule's loader through the pipeline.
//
// The expected shape (rough sketch):
//
//	type dbFlowsByProjectLoader struct{ db *sql.DB }
//
//	func (l *dbFlowsByProjectLoader) LoadFlowsForProjectVersion(ctx context.Context, projectID, versionID string) ([]FlowWithPersona, error) {
//	    // 1. SELECT f.id, f.persona_id, p.name FROM flows f LEFT JOIN personas p ON p.id = f.persona_id WHERE f.project_id = ?
//	    // 2. For each flow: SELECT s.id, s.flow_id, COALESCE(t.canonical_tree, '') FROM screens s LEFT JOIN screen_canonical_trees t ON t.screen_id = s.id WHERE s.flow_id = ? AND s.version_id = ? ORDER BY s.created_at ASC
//	    // 3. Stitch into []FlowWithPersona.
//	}
//
// Until that unit lands, NewCrossPersonaRunner is invokable in tests (via the
// stubLoader pattern in cross_persona_test.go) but is not wired into the
// worker pool.
