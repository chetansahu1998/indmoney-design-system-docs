package rules

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/projects"
)

// A11yTouchTargetRuleID is the stable rule identifier persisted on the
// violations row for sub-44pt clickable atoms.
const A11yTouchTargetRuleID = "a11y_touch_target_44pt"

// A11yTouchTargetCategory is the violations.category value for this rule.
// Drives the Violations tab's filter chip.
const A11yTouchTargetCategory = "a11y_touch_target"

// minTouchTargetPt is the WCAG / Apple HIG minimum hit area for an
// interactive element. Hardcoded for U4; Phase 7 plumbs this through the
// audit_rules.target_node_types config.
const minTouchTargetPt = 44.0

// clickableAtomNames is the U4 hardcoded allowlist of "clickable atom"
// component names. Matched against a node's `name` and (when present) its
// `mainComponent.name`. Phase 7 will replace this with a configurable list
// drawn from audit_rules.target_node_types.
//
// Kept private to this file so other forks (U2/U3) can't accidentally couple
// to it — per plan U4 we explicitly do NOT export shared helpers.
var clickableAtomNames = map[string]struct{}{
	"Button":     {},
	"IconButton": {},
	"Link":       {},
	"Tab":        {},
	"MenuItem":   {},
	"Chip":       {},
	"Toggle":     {},
	"Checkbox":   {},
	"Radio":      {},
}

// TouchTargetTreeLoader supplies canonical-tree blobs to the runner. Defined
// inline here (rather than importing from the parent package) so U4 stays
// self-contained — production wiring will pass an adapter that delegates to
// projects.VersionScreenLoader.
type TouchTargetTreeLoader interface {
	// LoadScreenTrees returns each screen in the version paired with its
	// canonical-tree blob. Matches the shape of
	// projects.VersionScreenLoader.LoadScreensWithTrees but stays interface-
	// compatible with synthetic in-memory fixtures used in tests.
	LoadScreenTrees(ctx context.Context, versionID string) ([]TouchTargetScreenTree, error)
}

// TouchTargetScreenTree pairs a screen ID with its canonical-tree blob.
type TouchTargetScreenTree struct {
	ScreenID      string
	CanonicalTree map[string]any
}

// A11yTouchTargetConfig collects the runner's dependencies.
type A11yTouchTargetConfig struct {
	Loader TouchTargetTreeLoader
}

// A11yTouchTargetRunner walks each canonical-tree, finds INSTANCE nodes whose
// name matches the clickable-atom allowlist, and emits a High violation when
// the node's bounding box is below 44×44pt.
type A11yTouchTargetRunner struct {
	loader TouchTargetTreeLoader
}

// NewA11yTouchTarget constructs the runner. Returns the concrete type rather
// than projects.RuleRunner so callers in this package can keep direct access
// to the inspect helpers in tests.
func NewA11yTouchTarget(cfg A11yTouchTargetConfig) *A11yTouchTargetRunner {
	return &A11yTouchTargetRunner{loader: cfg.Loader}
}

// Run implements projects.RuleRunner.
//
// TODO(U4-prod-wire): the production *sql.DB-backed loader still has to be
// wired up — for U4 we ship the runner against synthetic in-memory fixtures.
// Adapter goes alongside the worker pool in cmd/audit-server.
func (r *A11yTouchTargetRunner) Run(ctx context.Context, v *projects.ProjectVersion) ([]projects.Violation, error) {
	if v == nil {
		return nil, fmt.Errorf("a11y_touch_target: nil version")
	}
	if r.loader == nil {
		return nil, fmt.Errorf("a11y_touch_target: loader not configured")
	}
	rows, err := r.loader.LoadScreenTrees(ctx, v.ID)
	if err != nil {
		return nil, fmt.Errorf("a11y_touch_target: load trees: %w", err)
	}
	out := make([]projects.Violation, 0)
	for _, row := range rows {
		walkInstancesForTouchTarget(row.CanonicalTree, func(node map[string]any) {
			if !isClickableAtom(node) {
				return
			}
			w, h, ok := readBoundingBox(node)
			if !ok {
				return
			}
			if w >= minTouchTargetPt && h >= minTouchTargetPt {
				return
			}
			out = append(out, projects.Violation{
				ID:         uuid.NewString(),
				VersionID:  v.ID,
				ScreenID:   row.ScreenID,
				TenantID:   v.TenantID,
				RuleID:     A11yTouchTargetRuleID,
				Severity:   projects.SeverityHigh,
				Category:   A11yTouchTargetCategory,
				Property:   "touch_target",
				Observed:   fmt.Sprintf("%s below 44×44pt", formatDims(w, h)),
				Suggestion: "Increase the component's hit area to at least 44×44pt",
				Status:     "active",
			})
		})
	}
	return out, nil
}

// isClickableAtom returns true when the node is an INSTANCE whose `name` (or
// `mainComponent.name`) appears in the U4 allowlist.
func isClickableAtom(node map[string]any) bool {
	if node == nil {
		return false
	}
	if t, _ := node["type"].(string); t != "INSTANCE" {
		return false
	}
	if n, _ := node["name"].(string); matchClickableName(n) {
		return true
	}
	if mc, ok := node["mainComponent"].(map[string]any); ok {
		if mcName, _ := mc["name"].(string); matchClickableName(mcName) {
			return true
		}
	}
	return false
}

// matchClickableName accepts both bare allowlist entries (e.g. "Button") and
// Figma's typical variant-suffix form (e.g. "Button/Primary",
// "Button=Primary, Size=Md"). The allowlist key is the prefix before any
// `/`, `=`, or `,` separator — Figma's variant syntax never injects those
// chars into the bare component name.
func matchClickableName(name string) bool {
	if name == "" {
		return false
	}
	root := name
	for i, ch := range name {
		if ch == '/' || ch == '=' || ch == ',' {
			root = name[:i]
			break
		}
	}
	// Trim trailing whitespace — Figma sometimes pads variant prefixes.
	for len(root) > 0 && root[len(root)-1] == ' ' {
		root = root[:len(root)-1]
	}
	_, ok := clickableAtomNames[root]
	return ok
}

// readBoundingBox extracts the width/height of a node. Prefers
// `absoluteBoundingBox` (matches Figma REST shape) and falls back to top-
// level `width` / `height` when the absolute box is absent. Returns ok=false
// when no usable dimensions are available so the caller can skip silently.
func readBoundingBox(node map[string]any) (w, h float64, ok bool) {
	if abb, _ := node["absoluteBoundingBox"].(map[string]any); abb != nil {
		w, wOk := readFloat(abb["width"])
		h, hOk := readFloat(abb["height"])
		if wOk && hOk {
			return w, h, true
		}
	}
	w, wOk := readFloat(node["width"])
	h, hOk := readFloat(node["height"])
	if wOk && hOk {
		return w, h, true
	}
	return 0, 0, false
}

// readFloat coerces a `map[string]any`-decoded numeric value to float64.
// Handles JSON-decoded float64 plus the int variants that test fixtures
// occasionally hand-encode.
func readFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	}
	return 0, false
}

// formatDims renders a width × height as e.g. "36×36" — uses integer form
// when both dimensions are integral, otherwise falls back to %.0f.
func formatDims(w, h float64) string {
	if w == float64(int(w)) && h == float64(int(h)) {
		return fmt.Sprintf("%d×%d", int(w), int(h))
	}
	return fmt.Sprintf("%.0f×%.0f", w, h)
}

// walkInstancesForTouchTarget walks the canonical-tree depth-first and
// invokes `visit` for every INSTANCE node it encounters. Non-INSTANCE
// containers (FRAME, GROUP, etc.) are still recursed into so nested
// clickables are inspected. Visit decides whether to actually emit a
// violation (via isClickableAtom).
//
// The walk does NOT short-circuit when it sees a clickable atom — by plan
// "nested clickable inside non-clickable" must still be inspected, and
// having a clickable inside another clickable is rare enough that we accept
// double-counting if it ever happens (caller can dedupe on RuleID + screen).
func walkInstancesForTouchTarget(node any, visit func(map[string]any)) {
	if node == nil {
		return
	}
	m, ok := node.(map[string]any)
	if !ok {
		return
	}
	if t, _ := m["type"].(string); t == "INSTANCE" {
		visit(m)
		// Fall through — children of an INSTANCE may include nested
		// INSTANCEs (e.g. an IconButton inside a Card-shaped Button).
	}
	if children, ok := m["children"].([]any); ok {
		for _, c := range children {
			walkInstancesForTouchTarget(c, visit)
		}
	}
}
