package rules

import (
	"reflect"
	"strings"
	"testing"
)

// Treediff is a pure structural-diff helper. The tests below pin the contract
// the theme_parity rule (and Phase 2 U6 component-governance) depend on:
//
//   - Two equal trees → zero deltas.
//   - Keys listed in DiffOpts.IgnoreKeys are skipped at every depth.
//   - Type mismatches (string vs object, RECTANGLE vs ELLIPSE) emit a delta
//     with kind = "type_mismatch" and Property = the mismatched key.
//   - Layout primitives (paddingLeft, width, etc.) emit "layout_drift".
//   - Visual primitives (fills.color, strokes.color) emit "visual_drift".
//   - Children arrays diff pair-wise; missing-child cases emit a delta whose
//     observed-side string captures "<missing>".
//
// Path semantics: a slash-joined breadcrumb is the simplest representation
// (e.g. "Frame/Card/0/fills"). Tests assert the slice form and the joined
// form so future changes to the path representation surface here first.

func TestDiff_EqualTrees_ZeroDeltas(t *testing.T) {
	a := map[string]any{
		"type": "FRAME",
		"name": "Card",
		"children": []any{
			map[string]any{"type": "TEXT", "name": "Title"},
		},
	}
	b := map[string]any{
		"type": "FRAME",
		"name": "Card",
		"children": []any{
			map[string]any{"type": "TEXT", "name": "Title"},
		},
	}
	deltas := Diff(a, b, DiffOpts{})
	if len(deltas) != 0 {
		t.Fatalf("expected zero deltas, got %d: %+v", len(deltas), deltas)
	}
}

func TestDiff_IgnoreKeysAtEveryDepth(t *testing.T) {
	// Both trees structurally equal once boundVariables / explicitVariableModes
	// are dropped — even though the inner blobs differ.
	a := map[string]any{
		"type": "FRAME",
		"boundVariables": map[string]any{"fills": "var.surface.bg"},
		"children": []any{
			map[string]any{
				"type":           "RECTANGLE",
				"boundVariables": map[string]any{"fills": "var.surface.bg"},
				"fills":          "RESOLVED_LIGHT",
			},
		},
		"explicitVariableModes":       map[string]any{"collection-1": "mode-light"},
		"componentPropertyReferences": map[string]any{"text": "label"},
	}
	b := map[string]any{
		"type": "FRAME",
		"boundVariables": map[string]any{"fills": "var.surface.bg"},
		"children": []any{
			map[string]any{
				"type":           "RECTANGLE",
				"boundVariables": map[string]any{"fills": "var.surface.bg"},
				"fills":          "RESOLVED_DARK",
			},
		},
		"explicitVariableModes":       map[string]any{"collection-1": "mode-dark"},
		"componentPropertyReferences": map[string]any{"text": "label-dark"},
	}
	opts := DiffOpts{IgnoreKeys: []string{"boundVariables", "explicitVariableModes", "componentPropertyReferences"}}
	deltas := Diff(a, b, opts)
	// fills is NOT ignored — but per theme_parity contract, when the property
	// is bound to a Variable the parent node carries boundVariables.fills and
	// the diff still flags it. That's by design: the runner is responsible
	// for treating bound divergences as legitimate by inspecting the sibling
	// boundVariables key BEFORE the IgnoreKeys filter strips it. For this
	// pure-helper test we DO expect a fills delta to surface — the runner
	// suppresses it via its own logic in theme_parity.go.
	if len(deltas) == 0 {
		t.Fatalf("expected fills delta to surface from raw diff, got 0")
	}
	for _, d := range deltas {
		for _, p := range d.Path {
			if p == "boundVariables" || p == "explicitVariableModes" || p == "componentPropertyReferences" {
				t.Fatalf("ignored key %q leaked into delta path %v", p, d.Path)
			}
		}
		if d.Property == "boundVariables" || d.Property == "explicitVariableModes" || d.Property == "componentPropertyReferences" {
			t.Fatalf("ignored key %q leaked into delta property", d.Property)
		}
	}
}

func TestDiff_TypeMismatch(t *testing.T) {
	a := map[string]any{"type": "RECTANGLE"}
	b := map[string]any{"type": "ELLIPSE"}
	deltas := Diff(a, b, DiffOpts{})
	if len(deltas) != 1 {
		t.Fatalf("expected 1 delta, got %d: %+v", len(deltas), deltas)
	}
	if deltas[0].Property != "type" {
		t.Fatalf("expected Property=type, got %q", deltas[0].Property)
	}
	if deltas[0].Kind != "type_mismatch" {
		t.Fatalf("expected Kind=type_mismatch, got %q", deltas[0].Kind)
	}
	if deltas[0].ObservedA != "RECTANGLE" || deltas[0].ObservedB != "ELLIPSE" {
		t.Fatalf("unexpected observed values: A=%q B=%q", deltas[0].ObservedA, deltas[0].ObservedB)
	}
}

func TestDiff_LayoutDrift(t *testing.T) {
	a := map[string]any{"type": "FRAME", "paddingLeft": 16.0}
	b := map[string]any{"type": "FRAME", "paddingLeft": 12.0}
	deltas := Diff(a, b, DiffOpts{})
	if len(deltas) != 1 {
		t.Fatalf("expected 1 delta, got %d: %+v", len(deltas), deltas)
	}
	if deltas[0].Property != "paddingLeft" {
		t.Fatalf("expected Property=paddingLeft, got %q", deltas[0].Property)
	}
	if deltas[0].Kind != "layout_drift" {
		t.Fatalf("expected Kind=layout_drift, got %q", deltas[0].Kind)
	}
}

func TestDiff_VisualDrift(t *testing.T) {
	a := map[string]any{
		"type":  "RECTANGLE",
		"fills": map[string]any{"r": 1.0, "g": 1.0, "b": 1.0, "a": 1.0},
	}
	b := map[string]any{
		"type":  "RECTANGLE",
		"fills": map[string]any{"r": 0.42, "g": 0.45, "b": 0.5, "a": 1.0},
	}
	deltas := Diff(a, b, DiffOpts{})
	// Each r/g/b component differs → three deltas (or one parent delta —
	// implementation-dependent). Either way, at least one delta with a
	// "fills" segment in the path and Kind="visual_drift".
	if len(deltas) == 0 {
		t.Fatalf("expected at least 1 delta, got 0")
	}
	foundVisual := false
	for _, d := range deltas {
		joined := strings.Join(d.Path, "/")
		if strings.Contains(joined, "fills") && d.Kind == "visual_drift" {
			foundVisual = true
		}
	}
	if !foundVisual {
		t.Fatalf("expected at least one visual_drift delta under fills, got %+v", deltas)
	}
}

func TestDiff_NestedArrays_PairwiseByIndex(t *testing.T) {
	a := map[string]any{
		"type": "FRAME",
		"children": []any{
			map[string]any{"type": "TEXT", "name": "Title"},
			map[string]any{"type": "RECTANGLE", "name": "Divider"},
		},
	}
	b := map[string]any{
		"type": "FRAME",
		"children": []any{
			map[string]any{"type": "TEXT", "name": "Title"},
			map[string]any{"type": "ELLIPSE", "name": "Divider"}, // type drift at index 1
		},
	}
	deltas := Diff(a, b, DiffOpts{})
	if len(deltas) != 1 {
		t.Fatalf("expected 1 delta, got %d: %+v", len(deltas), deltas)
	}
	d := deltas[0]
	if d.Property != "type" {
		t.Fatalf("expected Property=type, got %q", d.Property)
	}
	if d.Kind != "type_mismatch" {
		t.Fatalf("expected Kind=type_mismatch, got %q", d.Kind)
	}
	// Path must reach index 1 of children.
	wantPath := []string{"children", "1", "type"}
	if !reflect.DeepEqual(d.Path, wantPath) {
		t.Fatalf("expected path %v, got %v", wantPath, d.Path)
	}
}

func TestDiff_MissingChild(t *testing.T) {
	a := map[string]any{
		"type": "FRAME",
		"children": []any{
			map[string]any{"type": "TEXT"},
			map[string]any{"type": "TEXT"},
			map[string]any{"type": "TEXT"},
		},
	}
	b := map[string]any{
		"type": "FRAME",
		"children": []any{
			map[string]any{"type": "TEXT"},
			map[string]any{"type": "TEXT"},
		},
	}
	deltas := Diff(a, b, DiffOpts{})
	if len(deltas) != 1 {
		t.Fatalf("expected 1 delta, got %d: %+v", len(deltas), deltas)
	}
	d := deltas[0]
	if d.Kind != "type_mismatch" && d.Kind != "name_divergence" {
		t.Fatalf("expected missing-child delta, got Kind=%q", d.Kind)
	}
	if !strings.Contains(d.ObservedB, "<missing>") {
		t.Fatalf("expected observedB to mention <missing>, got %q", d.ObservedB)
	}
	// Path must include the index of the dropped child.
	if len(d.Path) < 2 || d.Path[0] != "children" || d.Path[1] != "2" {
		t.Fatalf("expected path to start with children/2, got %v", d.Path)
	}
}
