package rules

import (
	"context"
	"testing"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/projects"
)

// fakeTouchLoader is the in-memory test fixture for TouchTargetTreeLoader.
// One per test — no global state to leak between scenarios.
type fakeTouchLoader struct {
	rows []TouchTargetScreenTree
}

func (f *fakeTouchLoader) LoadScreenTrees(_ context.Context, _ string) ([]TouchTargetScreenTree, error) {
	return f.rows, nil
}

// makeInstance is a convenience builder for an INSTANCE node with name +
// dimensions. The dimensions go on absoluteBoundingBox to match the Figma
// REST shape the canonical_tree pipeline preserves.
func makeInstance(name string, w, h float64) map[string]any {
	return map[string]any{
		"type": "INSTANCE",
		"name": name,
		"absoluteBoundingBox": map[string]any{
			"width":  w,
			"height": h,
		},
	}
}

// runTouchTargets is the test boilerplate — runs the rule against a single-
// screen tree and returns the violation slice.
func runTouchTargets(t *testing.T, root map[string]any) []projects.Violation {
	t.Helper()
	loader := &fakeTouchLoader{
		rows: []TouchTargetScreenTree{
			{ScreenID: "screen-1", CanonicalTree: root},
		},
	}
	r := NewA11yTouchTarget(A11yTouchTargetConfig{Loader: loader})
	v := &projects.ProjectVersion{ID: "v1", TenantID: "t1"}
	out, err := r.Run(context.Background(), v)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return out
}

// Happy path: 44×44 button → no violation.
func TestA11yTouchTarget_Happy_44(t *testing.T) {
	root := map[string]any{
		"type":     "FRAME",
		"name":     "Screen",
		"children": []any{makeInstance("Button", 44, 44)},
	}
	out := runTouchTargets(t, root)
	if len(out) != 0 {
		t.Fatalf("44×44 should pass; got %d violations: %+v", len(out), out)
	}
}

// Error path: 36×36 button → High a11y_touch_target_44pt.
func TestA11yTouchTarget_Error_36(t *testing.T) {
	root := map[string]any{
		"type":     "FRAME",
		"children": []any{makeInstance("Button", 36, 36)},
	}
	out := runTouchTargets(t, root)
	if len(out) != 1 {
		t.Fatalf("36×36 should violate; got %d", len(out))
	}
	v := out[0]
	if v.RuleID != A11yTouchTargetRuleID {
		t.Errorf("RuleID: want %q, got %q", A11yTouchTargetRuleID, v.RuleID)
	}
	if v.Severity != projects.SeverityHigh {
		t.Errorf("Severity: want high, got %q", v.Severity)
	}
	if v.Category != A11yTouchTargetCategory {
		t.Errorf("Category: want %q, got %q", A11yTouchTargetCategory, v.Category)
	}
	if v.Property != "touch_target" {
		t.Errorf("Property: want touch_target, got %q", v.Property)
	}
	if v.Observed != "36×36 below 44×44pt" {
		t.Errorf("Observed: want %q, got %q", "36×36 below 44×44pt", v.Observed)
	}
	if v.Suggestion == "" {
		t.Error("Suggestion: should be non-empty")
	}
	if v.ScreenID != "screen-1" {
		t.Errorf("ScreenID: want screen-1, got %q", v.ScreenID)
	}
	if v.VersionID != "v1" || v.TenantID != "t1" {
		t.Errorf("Version/Tenant not propagated: %+v", v)
	}
	if v.Status != "active" {
		t.Errorf("Status: want active, got %q", v.Status)
	}
}

// Edge: non-clickable INSTANCE (Card) is ignored regardless of size.
func TestA11yTouchTarget_NonClickable_Ignored(t *testing.T) {
	root := map[string]any{
		"type": "FRAME",
		"children": []any{
			makeInstance("Card", 20, 20),       // far below 44 but not clickable
			makeInstance("Container", 1, 1),    // not in allowlist
		},
	}
	out := runTouchTargets(t, root)
	if len(out) != 0 {
		t.Fatalf("non-clickable INSTANCEs must not violate; got %+v", out)
	}
}

// Edge: clickable INSTANCE nested inside a non-clickable INSTANCE is still
// inspected; outer non-clickable is not.
func TestA11yTouchTarget_NestedClickable(t *testing.T) {
	inner := makeInstance("IconButton", 32, 32) // below threshold
	outer := makeInstance("Card", 200, 200)     // above, but not clickable
	outer["children"] = []any{inner}

	root := map[string]any{
		"type":     "FRAME",
		"children": []any{outer},
	}
	out := runTouchTargets(t, root)
	if len(out) != 1 {
		t.Fatalf("nested IconButton must violate; got %d: %+v", len(out), out)
	}
	if out[0].Observed != "32×32 below 44×44pt" {
		t.Errorf("Observed: want 32×32 below 44×44pt, got %q", out[0].Observed)
	}
}

// Edge: IconButton 32×32 — the canonical example.
func TestA11yTouchTarget_IconButton_32(t *testing.T) {
	root := map[string]any{
		"type":     "FRAME",
		"children": []any{makeInstance("IconButton", 32, 32)},
	}
	out := runTouchTargets(t, root)
	if len(out) != 1 || out[0].Observed != "32×32 below 44×44pt" {
		t.Fatalf("IconButton 32×32: want 1 violation 32×32 below 44×44pt, got %+v", out)
	}
}

// Edge: Button 100×40 — width passes but height fails. Must still violate.
func TestA11yTouchTarget_OneDimensionShort(t *testing.T) {
	root := map[string]any{
		"type":     "FRAME",
		"children": []any{makeInstance("Button", 100, 40)},
	}
	out := runTouchTargets(t, root)
	if len(out) != 1 {
		t.Fatalf("100×40 must violate (height < 44); got %d", len(out))
	}
	if out[0].Observed != "100×40 below 44×44pt" {
		t.Errorf("Observed: want 100×40 below 44×44pt, got %q", out[0].Observed)
	}
}

// Edge: variant-suffixed name still matches the allowlist.
func TestA11yTouchTarget_VariantSuffix(t *testing.T) {
	root := map[string]any{
		"type": "FRAME",
		"children": []any{
			makeInstance("Button/Primary", 30, 30),
			makeInstance("Button=Primary, Size=Md", 30, 30),
		},
	}
	out := runTouchTargets(t, root)
	if len(out) != 2 {
		t.Fatalf("variant-suffixed buttons must violate; got %d: %+v", len(out), out)
	}
}

// Edge: dimensions read from top-level width/height when absoluteBoundingBox
// is absent (some canonical-tree fixtures store dims that way).
func TestA11yTouchTarget_TopLevelDims(t *testing.T) {
	node := map[string]any{
		"type":   "INSTANCE",
		"name":   "Button",
		"width":  20.0,
		"height": 20.0,
	}
	root := map[string]any{
		"type":     "FRAME",
		"children": []any{node},
	}
	out := runTouchTargets(t, root)
	if len(out) != 1 || out[0].Observed != "20×20 below 44×44pt" {
		t.Fatalf("top-level dims: want 1 violation 20×20, got %+v", out)
	}
}

// Edge: missing dimensions → silently skipped.
func TestA11yTouchTarget_MissingDims(t *testing.T) {
	node := map[string]any{"type": "INSTANCE", "name": "Button"}
	root := map[string]any{
		"type":     "FRAME",
		"children": []any{node},
	}
	out := runTouchTargets(t, root)
	if len(out) != 0 {
		t.Fatalf("missing dims must skip silently; got %+v", out)
	}
}

// Edge: mainComponent.name fallback (covers cases where Figma exports the
// instance with a synthesized name but the underlying main-component name
// matches the allowlist).
func TestA11yTouchTarget_MainComponentName(t *testing.T) {
	node := map[string]any{
		"type": "INSTANCE",
		"name": "primary-cta", // not in allowlist
		"mainComponent": map[string]any{
			"name": "Button",
		},
		"absoluteBoundingBox": map[string]any{
			"width":  30.0,
			"height": 30.0,
		},
	}
	root := map[string]any{
		"type":     "FRAME",
		"children": []any{node},
	}
	out := runTouchTargets(t, root)
	if len(out) != 1 {
		t.Fatalf("mainComponent name match must violate; got %+v", out)
	}
}

// Error path: nil version returns error.
func TestA11yTouchTarget_NilVersion(t *testing.T) {
	r := NewA11yTouchTarget(A11yTouchTargetConfig{Loader: &fakeTouchLoader{}})
	if _, err := r.Run(context.Background(), nil); err == nil {
		t.Fatal("nil version: expected error, got nil")
	}
}

// Error path: missing loader returns error.
func TestA11yTouchTarget_NilLoader(t *testing.T) {
	r := NewA11yTouchTarget(A11yTouchTargetConfig{Loader: nil})
	v := &projects.ProjectVersion{ID: "v1", TenantID: "t1"}
	if _, err := r.Run(context.Background(), v); err == nil {
		t.Fatal("nil loader: expected error, got nil")
	}
}
