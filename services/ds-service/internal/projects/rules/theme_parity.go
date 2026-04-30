package rules

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/projects"
)

// ThemeParityRunner implements projects.RuleRunner for the theme_parity_break
// rule (R7, AE-2). For each screen with two or more modes it pair-wise diffs
// the per-mode RESOLVED canonical_trees and emits a Critical Violation per
// structural divergence outside legitimate Variable bindings.
//
// Why "resolved" trees instead of the raw screen_canonical_tree blob:
//
//   - Phase 1 stores ONE canonical_tree per screen (the structural shape) and
//     a screen_modes sidecar carrying per-mode VariableModeID overlays.
//   - Theme-parity violations show up only AFTER each mode's Variables are
//     resolved against the design-token catalog — at that point a hand-painted
//     dark color stands out next to a Variable-bound light one.
//
// The ResolvedTreeLoader interface lives at the top of this file so the
// production wiring can be a single follow-up unit (TODO U2-prod-wire below)
// without touching the runner contract. Tests use a fake that returns
// pre-resolved trees built in-memory.
//
// Bound-only divergences (the fills value differs across modes BUT both nodes
// declare boundVariables.fills) are filtered before the diff. See
// stripBoundProperties for the contract.
//
// Severity defaults to "critical" — Phase 7 admin overrides via audit_rules.
// We do NOT query that table here; the worker's PersistRunIdempotent will
// re-tag severity at insert time once U7 wires the rule registry through.

// RuleID returned on every violation this runner emits.
const ruleIDThemeParityBreak = "theme_parity_break"

// CategoryThemeParity is the violations.category value emitted by this rule.
const categoryThemeParity = "theme_parity"

// boundVariablesKey is the JSON property Figma writes on each node to declare
// "this property is bound to Variable X". Walking both modes' trees, any
// property whose sibling boundVariables[prop] exists is treated as a
// legitimate Variable resolution and stripped before diff.
const boundVariablesKey = "boundVariables"

// ResolvedScreen is one (screen, mode) pair with its mode-resolved canonical
// tree as JSON. The ResolvedTree blob has Variables already substituted.
type ResolvedScreen struct {
	ScreenID     string
	ModeLabel    string
	ResolvedTree string
}

// ResolvedTreeLoader returns one ResolvedScreen per (screen, mode) for the
// version. Implementations may load the base canonical_tree and apply each
// mode's variable bindings on the fly, or read pre-computed blobs.
//
// TODO(U2-prod-wire): The production implementation needs to:
//
//  1. Load each screen_canonical_trees row for the version (one base tree
//     per screen).
//  2. Load each screen_modes row for the version (one row per mode).
//  3. Load the indmoney semantic-light + semantic-dark token catalog from
//     lib/tokens/indmoney/semantic-{light,dark}.tokens.json.
//  4. For each (screen, mode) pair, resolve the base canonical_tree against
//     the mode's explicit_variable_modes_json + the matching token catalog
//     by walking nodes and substituting boundVariables targets with the
//     resolved hex values.
//
// That work is substantial enough to live in its own follow-up unit — the
// orchestrator wires NewDBResolvedTreeLoader (or similar) into the worker
// when U2 lands behind the rule registry.
type ResolvedTreeLoader interface {
	LoadResolvedScreens(ctx context.Context, versionID string) ([]ResolvedScreen, error)
}

// ThemeParityConfig configures the runner. Loader is required; everything
// else has sensible defaults.
type ThemeParityConfig struct {
	Loader ResolvedTreeLoader
	// Severity is what every emitted violation carries. Defaults to
	// "critical" per the Phase 2 plan; the worker may re-tag via
	// audit_rules.default_severity in a future unit.
	Severity string
}

// NewThemeParityRunner constructs the runner. Loader must be non-nil; tests
// pass an in-memory fake, production wiring passes a DB-backed impl.
func NewThemeParityRunner(cfg ThemeParityConfig) projects.RuleRunner {
	if cfg.Severity == "" {
		cfg.Severity = projects.SeverityCritical
	}
	return &themeParityRunner{cfg: cfg}
}

type themeParityRunner struct {
	cfg ThemeParityConfig
}

// Run implements projects.RuleRunner. Loads resolved screens for the version,
// groups them by ScreenID, pair-wise diffs the modes within each group, and
// emits one Violation per Delta per pair.
func (r *themeParityRunner) Run(ctx context.Context, v *projects.ProjectVersion) ([]projects.Violation, error) {
	if v == nil {
		return nil, fmt.Errorf("themeParityRunner: nil version")
	}
	if r.cfg.Loader == nil {
		return nil, fmt.Errorf("themeParityRunner: loader not configured")
	}
	rows, err := r.cfg.Loader.LoadResolvedScreens(ctx, v.ID)
	if err != nil {
		return nil, fmt.Errorf("load resolved screens: %w", err)
	}

	// Group by ScreenID with a deterministic mode order for stable
	// per-pair iteration. Tests that count violations rely on this.
	byScreen := make(map[string][]ResolvedScreen, len(rows))
	screenOrder := make([]string, 0)
	for _, sc := range rows {
		if _, seen := byScreen[sc.ScreenID]; !seen {
			screenOrder = append(screenOrder, sc.ScreenID)
		}
		byScreen[sc.ScreenID] = append(byScreen[sc.ScreenID], sc)
	}
	for _, sid := range screenOrder {
		modes := byScreen[sid]
		sort.SliceStable(modes, func(i, j int) bool { return modes[i].ModeLabel < modes[j].ModeLabel })
		byScreen[sid] = modes
	}

	out := make([]projects.Violation, 0)
	for _, sid := range screenOrder {
		modes := byScreen[sid]
		if len(modes) < 2 {
			continue // single-mode screens cannot exhibit theme-parity drift
		}
		// Pair-wise compare every (i, j) where i < j.
		for i := 0; i < len(modes); i++ {
			for j := i + 1; j < len(modes); j++ {
				pairViolations := r.diffPair(v, modes[i], modes[j])
				out = append(out, pairViolations...)
			}
		}
	}
	return out, nil
}

// diffPair runs the pure tree-diff between two modes of the same screen and
// converts each Delta into a Violation. The runner pre-strips bound
// properties from BOTH trees so legitimate Variable resolutions don't surface
// as violations (Test 2 in the suite).
func (r *themeParityRunner) diffPair(v *projects.ProjectVersion, a, b ResolvedScreen) []projects.Violation {
	ta := decodeTreeMap(a.ResolvedTree)
	tb := decodeTreeMap(b.ResolvedTree)
	if ta == nil || tb == nil {
		return nil
	}

	// Strip properties that are bound on EITHER side. The bound side carries
	// boundVariables.<prop>; the property's resolved value is whatever the
	// resolver substituted. Mode A and B may legitimately produce different
	// resolved values, so we drop both before diffing.
	stripBoundProperties(ta)
	stripBoundProperties(tb)

	deltas := Diff(ta, tb, DiffOpts{
		IgnoreKeys: []string{
			"boundVariables",
			"explicitVariableModes",
			"componentPropertyReferences",
		},
	})

	out := make([]projects.Violation, 0, len(deltas))
	for _, d := range deltas {
		out = append(out, r.deltaToViolation(v, a, b, d))
	}
	return out
}

// deltaToViolation maps a single Delta into a Violation row. The mode the
// runner attaches to the violation is the one whose value is the "outlier" —
// which we approximate as the mode that lacks a Variable binding when the
// other has one. When both sides are unbound, we attribute to mode B (the
// later one in label order) — this keeps reporting deterministic across runs.
func (r *themeParityRunner) deltaToViolation(v *projects.ProjectVersion, a, b ResolvedScreen, d Delta) projects.Violation {
	mode := b.ModeLabel
	suggestion := suggestionForKind(d.Kind, d.Property)
	observed := observedSummary(a.ModeLabel, d.ObservedA, b.ModeLabel, d.ObservedB)
	mLabel := mode
	return projects.Violation{
		ID:          uuid.NewString(),
		VersionID:   v.ID,
		ScreenID:    a.ScreenID,
		TenantID:    v.TenantID,
		RuleID:      ruleIDThemeParityBreak,
		Severity:    r.cfg.Severity,
		Category:    categoryThemeParity,
		Property:    d.Property,
		Observed:    observed,
		Suggestion:  suggestion,
		ModeLabel:   &mLabel,
		Status:      "active",
		AutoFixable: false,
	}
}

// suggestionForKind picks a short, action-oriented suggestion based on the
// delta's kind. The strings are stable so the frontend can substring-match
// for icon selection without a separate enum on the wire.
func suggestionForKind(kind, property string) string {
	switch kind {
	case "type_mismatch", "name_divergence":
		return "Use the same node structure across modes"
	case "layout_drift":
		return fmt.Sprintf("Bind %s to a Variable across both modes", property)
	case "visual_drift":
		return fmt.Sprintf("Bind %s to a Variable across both modes", property)
	}
	return fmt.Sprintf("Resolve divergence on %s across both modes", property)
}

// observedSummary renders a one-line description of the mode-pair divergence
// suitable for the Violations tab's "Observed" column.
func observedSummary(modeA, valueA, modeB, valueB string) string {
	return fmt.Sprintf("%s in %s, %s in %s",
		shortObserved(valueA), modeA, shortObserved(valueB), modeB)
}

// shortObserved tightens a Delta observed-string into something legible in a
// table cell. Empty/zero strings become a sensible placeholder.
func shortObserved(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "<empty>"
	}
	return s
}

// decodeTreeMap parses a JSON canonical-tree blob into map[string]any.
// Returns nil on parse failure so the caller can short-circuit cleanly. We
// intentionally mirror the runner.go:147 helper pattern instead of importing
// it — the rules package owns its own decode contract so a future change to
// the canonical-tree shape can land here without touching runner.go.
func decodeTreeMap(s string) map[string]any {
	if s == "" || s == "{}" {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil
	}
	return m
}

// stripBoundProperties walks a tree and removes any property whose sibling
// boundVariables[prop] exists. The boundVariables key itself is also removed
// (the diff opts already ignore it, but stripping it here keeps the tree
// shape clean for downstream consumers if any).
//
// The mutation is in-place — the runner owns these maps for the duration of
// the diff and they are not shared with anyone else.
func stripBoundProperties(node map[string]any) {
	if node == nil {
		return
	}
	if bv, ok := node[boundVariablesKey].(map[string]any); ok {
		for prop := range bv {
			delete(node, prop)
		}
		delete(node, boundVariablesKey)
	}
	for _, val := range node {
		switch v := val.(type) {
		case map[string]any:
			stripBoundProperties(v)
		case []any:
			for _, child := range v {
				if cm, ok := child.(map[string]any); ok {
					stripBoundProperties(cm)
				}
			}
		}
	}
}
