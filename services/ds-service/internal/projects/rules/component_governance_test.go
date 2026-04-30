package rules

// component_governance_test.go covers the U6 ComponentGovernanceRunner across
// the table-driven scenarios called out in the Phase 2 plan:
//
//   1. Happy path: clean instances → zero violations.
//   2. Error: detached lookalike (RECTANGLE named "Button" + TEXT child) →
//      Medium component_detached.
//   3. Error: heavy override (9 overrides) → Low component_override_sprawl.
//   4. Error: sprawling flow (84 distinct componentSetKeys) → one Info
//      component_set_sprawl per flow.
//   5. Edge: 7 overrides → no violation.
//   6. Edge: name not in allowlist → no violation.
//   7. Edge: detached well-named ("Button") flagged; "Buttons" (plural) NOT
//      flagged (leading-alpha-run match semantics).
//   8. Edge: INSTANCE with valid componentId — even with 100 overrides only
//      override-sprawl fires (not detached).
//   9. Edge: empty flow (no INSTANCEs) → no sprawl violation.
//  10. Multi-flow: two flows in same version each with 84+ unique components
//      → two Info violations, one per flow.
//
// All fixtures are in-memory; the loader is a stub returning a pre-built
// []ScreenWithFlow. No SQL, no manifest read.

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/projects"
)

// ─── In-memory loader ────────────────────────────────────────────────────────

type stubScreensLoader struct {
	rows []ScreenWithFlow
	err  error
}

func (s *stubScreensLoader) LoadScreensWithFlows(_ context.Context, _ string) ([]ScreenWithFlow, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.rows, nil
}

// ─── Tree-builder helpers ────────────────────────────────────────────────────

// jsonOf marshals a node tree to a string. Test-only sugar — failures should
// never happen on synthetic fixtures, so we ignore the error.
func jsonOf(node map[string]any) string {
	b, _ := json.Marshal(node)
	return string(b)
}

// frame wraps the given children in a top-level FRAME node.
func frame(children ...map[string]any) map[string]any {
	return map[string]any{
		"type":     "FRAME",
		"name":     "Root",
		"children": toAnySlice(children),
	}
}

// toAnySlice converts []map[string]any → []any so the children array round-
// trips through encoding/json the same way Figma exports do.
func toAnySlice(in []map[string]any) []any {
	out := make([]any, 0, len(in))
	for _, m := range in {
		out = append(out, m)
	}
	return out
}

// validInstance builds an INSTANCE node with a stable componentSetKey via
// mainComponent.componentSetKey + mainComponent.id. Used for the happy path
// where every instance has a real DS link.
func validInstance(name, key string) map[string]any {
	return map[string]any{
		"type": "INSTANCE",
		"name": name,
		"mainComponent": map[string]any{
			"id":              "main-" + key,
			"componentSetKey": key,
			"name":            name,
		},
	}
}

// detachedRect builds a RECTANGLE with the given name and (optionally) one
// TEXT child. No componentId, no mainComponent. Used for the detached
// lookalike scenario.
func detachedRect(name string, textChild string) map[string]any {
	m := map[string]any{
		"type": "RECTANGLE",
		"name": name,
	}
	if textChild != "" {
		m["children"] = []any{
			map[string]any{
				"type":        "TEXT",
				"name":        textChild,
				"characters":  textChild,
			},
		}
	}
	return m
}

// instanceWithOverrides builds an INSTANCE node with the requested number of
// componentProperties / boundVariables / direct visual props. Useful for the
// override-sprawl scenarios.
func instanceWithOverrides(name, key string, cpCount, bvCount, visualCount int) map[string]any {
	inst := validInstance(name, key)

	if cpCount > 0 {
		cp := map[string]any{}
		for i := 0; i < cpCount; i++ {
			// value differs from defaultValue → counts as override.
			cp[fmt.Sprintf("prop-%d", i)] = map[string]any{
				"value":        "user-set",
				"defaultValue": "default",
			}
		}
		inst["componentProperties"] = cp
	}

	if bvCount > 0 {
		bv := map[string]any{}
		for i := 0; i < bvCount; i++ {
			bv[fmt.Sprintf("var-%d", i)] = map[string]any{
				"id":   fmt.Sprintf("VariableID:%d", i),
				"type": "VARIABLE_ALIAS",
			}
		}
		inst["boundVariables"] = bv
	}

	visualKeys := []string{
		"fills", "strokes", "effects", "cornerRadius",
		"paddingLeft", "paddingRight", "paddingTop", "paddingBottom",
		"itemSpacing",
	}
	for i := 0; i < visualCount && i < len(visualKeys); i++ {
		inst[visualKeys[i]] = "overridden"
	}

	return inst
}

// makeRow wraps a tree node into a ScreenWithFlow row with the given screen
// + flow id and the JSON-encoded tree blob.
func makeRow(screenID, flowID string, tree map[string]any) ScreenWithFlow {
	return ScreenWithFlow{
		ScreenID:      screenID,
		FlowID:        flowID,
		CanonicalTree: jsonOf(tree),
	}
}

// makeVersion is a tiny constructor for ProjectVersion in tests.
func makeVersion() *projects.ProjectVersion {
	return &projects.ProjectVersion{
		ID:        "v1",
		ProjectID: "p1",
		TenantID:  "t1",
	}
}

// countByRule counts emitted violations per rule_id.
func countByRule(vs []projects.Violation) map[string]int {
	out := map[string]int{}
	for _, v := range vs {
		out[v.RuleID]++
	}
	return out
}

// ─── 1. Happy path: clean instances ──────────────────────────────────────────

func TestComponentGovernance_HappyPath_CleanInstances(t *testing.T) {
	loader := &stubScreensLoader{rows: []ScreenWithFlow{
		makeRow("s1", "flow-a", frame(
			validInstance("Button / Primary", "btn-primary"),
			validInstance("Card", "card-default"),
		)),
	}}
	r := NewComponentGovernanceRunner(loader)

	got, err := r.Run(context.Background(), makeVersion())
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 violations on clean instances, got %d: %+v", len(got), got)
	}
}

// ─── 2. Error: detached lookalike ────────────────────────────────────────────

func TestComponentGovernance_ErrorPath_DetachedLookalike(t *testing.T) {
	loader := &stubScreensLoader{rows: []ScreenWithFlow{
		makeRow("s1", "flow-a", frame(
			detachedRect("Button", "Submit"),
		)),
	}}
	r := NewComponentGovernanceRunner(loader)

	got, err := r.Run(context.Background(), makeVersion())
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 violation, got %d: %+v", len(got), got)
	}
	v := got[0]
	if v.RuleID != "component_detached" {
		t.Errorf("RuleID = %q, want component_detached", v.RuleID)
	}
	if v.Severity != projects.SeverityMedium {
		t.Errorf("Severity = %q, want medium", v.Severity)
	}
	if v.Category != "component_governance" {
		t.Errorf("Category = %q, want component_governance", v.Category)
	}
	if v.Property != "instance" {
		t.Errorf("Property = %q, want instance", v.Property)
	}
	if v.ScreenID != "s1" {
		t.Errorf("ScreenID = %q, want s1", v.ScreenID)
	}
}

// ─── 3. Error: heavy override (9 overrides) ──────────────────────────────────

func TestComponentGovernance_ErrorPath_HeavyOverride(t *testing.T) {
	// 4 componentProperties + 3 boundVariables + 2 direct visual props = 9.
	loader := &stubScreensLoader{rows: []ScreenWithFlow{
		makeRow("s1", "flow-a", frame(
			instanceWithOverrides("Button", "btn-key", 4, 3, 2),
		)),
	}}
	r := NewComponentGovernanceRunner(loader)

	got, err := r.Run(context.Background(), makeVersion())
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 violation, got %d: %+v", len(got), got)
	}
	v := got[0]
	if v.RuleID != "component_override_sprawl" {
		t.Errorf("RuleID = %q, want component_override_sprawl", v.RuleID)
	}
	if v.Severity != projects.SeverityLow {
		t.Errorf("Severity = %q, want low", v.Severity)
	}
	if v.Property != "overrides" {
		t.Errorf("Property = %q, want overrides", v.Property)
	}
	wantObs := "9 overrides on instance"
	if v.Observed != wantObs {
		t.Errorf("Observed = %q, want %q", v.Observed, wantObs)
	}
}

// ─── 4. Error: sprawling flow (84 distinct componentSetKeys) ────────────────

func TestComponentGovernance_ErrorPath_SprawlingFlow(t *testing.T) {
	// 84 instances across one screen, each with a unique componentSetKey.
	children := make([]map[string]any, 0, 84)
	for i := 0; i < 84; i++ {
		children = append(children, validInstance(
			fmt.Sprintf("Comp-%d", i),
			fmt.Sprintf("set-key-%d", i),
		))
	}
	loader := &stubScreensLoader{rows: []ScreenWithFlow{
		makeRow("s1", "flow-a", frame(children...)),
	}}
	r := NewComponentGovernanceRunner(loader)

	got, err := r.Run(context.Background(), makeVersion())
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	counts := countByRule(got)
	if counts["component_set_sprawl"] != 1 {
		t.Fatalf("expected exactly 1 component_set_sprawl, got %d (all: %+v)", counts["component_set_sprawl"], got)
	}
	// Find the sprawl violation.
	var sprawl projects.Violation
	for _, v := range got {
		if v.RuleID == "component_set_sprawl" {
			sprawl = v
			break
		}
	}
	if sprawl.Severity != projects.SeverityInfo {
		t.Errorf("Severity = %q, want info", sprawl.Severity)
	}
	if sprawl.Property != "component_set_count" {
		t.Errorf("Property = %q, want component_set_count", sprawl.Property)
	}
	wantObs := "84 distinct components in flow"
	if sprawl.Observed != wantObs {
		t.Errorf("Observed = %q, want %q", sprawl.Observed, wantObs)
	}
	if sprawl.ScreenID != "s1" {
		t.Errorf("ScreenID = %q, want s1 (first screen of flow)", sprawl.ScreenID)
	}
}

// ─── 5. Edge: 7 overrides — below threshold ──────────────────────────────────

func TestComponentGovernance_Edge_SevenOverridesBelowThreshold(t *testing.T) {
	// 3 + 2 + 2 = 7 overrides; below the 8 threshold.
	loader := &stubScreensLoader{rows: []ScreenWithFlow{
		makeRow("s1", "flow-a", frame(
			instanceWithOverrides("Button", "btn-key", 3, 2, 2),
		)),
	}}
	r := NewComponentGovernanceRunner(loader)

	got, err := r.Run(context.Background(), makeVersion())
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if c := countByRule(got)["component_override_sprawl"]; c != 0 {
		t.Fatalf("expected 0 override_sprawl violations at 7 overrides, got %d", c)
	}
}

// ─── 6. Edge: name not in allowlist ──────────────────────────────────────────

func TestComponentGovernance_Edge_NameNotInAllowlist(t *testing.T) {
	loader := &stubScreensLoader{rows: []ScreenWithFlow{
		makeRow("s1", "flow-a", frame(
			detachedRect("MyCustomShape", ""),
		)),
	}}
	r := NewComponentGovernanceRunner(loader)

	got, err := r.Run(context.Background(), makeVersion())
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 violations for non-allowlisted name, got %d: %+v", len(got), got)
	}
}

// ─── 7. Edge: prefix-match semantics — "Button" ✓, "Buttons" ✗ ───────────────

func TestComponentGovernance_Edge_PluralDoesNotMatch(t *testing.T) {
	// "Buttons" leading alpha run is "Buttons" — not in allowlist. Documented
	// in matchAllowlist (leading-alpha-run match, plural rejected).
	loader := &stubScreensLoader{rows: []ScreenWithFlow{
		makeRow("s1", "flow-a", frame(
			detachedRect("Button", ""),  // matches → flagged
			detachedRect("Buttons", ""), // does not match
		)),
	}}
	r := NewComponentGovernanceRunner(loader)

	got, err := r.Run(context.Background(), makeVersion())
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	detached := 0
	for _, v := range got {
		if v.RuleID == "component_detached" {
			detached++
		}
	}
	if detached != 1 {
		t.Fatalf("expected exactly 1 component_detached (Button matches, Buttons doesn't), got %d: %+v", detached, got)
	}
}

// ─── 8. Edge: valid instance with many overrides → no detached, only sprawl ─

func TestComponentGovernance_Edge_ValidInstanceWithManyOverrides(t *testing.T) {
	// Real INSTANCE link present → detached should NOT fire even if name
	// matches allowlist. 10 overrides → override_sprawl fires.
	loader := &stubScreensLoader{rows: []ScreenWithFlow{
		makeRow("s1", "flow-a", frame(
			instanceWithOverrides("Button", "btn-key", 5, 3, 2),
		)),
	}}
	r := NewComponentGovernanceRunner(loader)

	got, err := r.Run(context.Background(), makeVersion())
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	counts := countByRule(got)
	if counts["component_detached"] != 0 {
		t.Errorf("expected 0 component_detached on real INSTANCE, got %d", counts["component_detached"])
	}
	if counts["component_override_sprawl"] != 1 {
		t.Errorf("expected 1 component_override_sprawl, got %d", counts["component_override_sprawl"])
	}
}

// ─── 9. Edge: empty flow (no INSTANCEs) ──────────────────────────────────────

func TestComponentGovernance_Edge_EmptyFlow(t *testing.T) {
	loader := &stubScreensLoader{rows: []ScreenWithFlow{
		makeRow("s1", "flow-a", frame(
			map[string]any{
				"type": "TEXT",
				"name": "Hello",
				"characters": "Hello",
			},
		)),
	}}
	r := NewComponentGovernanceRunner(loader)

	got, err := r.Run(context.Background(), makeVersion())
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 violations on empty (text-only) flow, got %d: %+v", len(got), got)
	}
}

// ─── 10. Multi-flow: sprawl per flow ────────────────────────────────────────

func TestComponentGovernance_MultiFlow_SprawlPerFlow(t *testing.T) {
	// Two flows, each with 84 distinct INSTANCEs → two Info violations.
	makeBigFlow := func(prefix string) map[string]any {
		children := make([]map[string]any, 0, 84)
		for i := 0; i < 84; i++ {
			children = append(children, validInstance(
				fmt.Sprintf("Comp-%s-%d", prefix, i),
				fmt.Sprintf("set-key-%s-%d", prefix, i),
			))
		}
		return frame(children...)
	}
	loader := &stubScreensLoader{rows: []ScreenWithFlow{
		makeRow("s1", "flow-a", makeBigFlow("a")),
		makeRow("s2", "flow-b", makeBigFlow("b")),
	}}
	r := NewComponentGovernanceRunner(loader)

	got, err := r.Run(context.Background(), makeVersion())
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	counts := countByRule(got)
	if counts["component_set_sprawl"] != 2 {
		t.Fatalf("expected exactly 2 component_set_sprawl (one per flow), got %d (all: %+v)", counts["component_set_sprawl"], got)
	}
	// Anchor screen ids should be the first screen of each flow.
	seen := map[string]bool{}
	for _, v := range got {
		if v.RuleID == "component_set_sprawl" {
			seen[v.ScreenID] = true
		}
	}
	if !seen["s1"] || !seen["s2"] {
		t.Errorf("expected sprawl violations anchored to s1 and s2, got %v", seen)
	}
}

// ─── Misc: nil/loader-error guard rails ─────────────────────────────────────

func TestComponentGovernance_Run_NilVersion_Errors(t *testing.T) {
	r := NewComponentGovernanceRunner(&stubScreensLoader{})
	_, err := r.Run(context.Background(), nil)
	if err == nil {
		t.Fatalf("expected error on nil version, got nil")
	}
}

func TestComponentGovernance_Run_NoLoader_Errors(t *testing.T) {
	r := NewComponentGovernanceRunner(nil)
	_, err := r.Run(context.Background(), makeVersion())
	if err == nil {
		t.Fatalf("expected error on nil loader, got nil")
	}
}

func TestComponentGovernance_Run_LoaderError_Propagates(t *testing.T) {
	loader := &stubScreensLoader{err: fmt.Errorf("db down")}
	r := NewComponentGovernanceRunner(loader)
	_, err := r.Run(context.Background(), makeVersion())
	if err == nil {
		t.Fatalf("expected error to propagate, got nil")
	}
}

// ─── RuleRunner conformance ──────────────────────────────────────────────────

// Compile-time assertion that ComponentGovernanceRunner implements
// projects.RuleRunner.
var _ projects.RuleRunner = (*ComponentGovernanceRunner)(nil)
