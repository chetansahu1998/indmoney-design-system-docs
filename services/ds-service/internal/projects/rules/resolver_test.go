package rules

import (
	"testing"
)

// classifyValue is the heart of the resolver — every other path
// delegates here. Test it explicitly.
func TestClassifyValue_Color(t *testing.T) {
	v := classifyValue(map[string]any{"r": 0.5, "g": 0.25, "b": 1.0, "a": 0.5})
	if v == nil || v.Kind != ResolvedKindColor {
		t.Fatalf("expected color kind, got %+v", v)
	}
	if v.RGBA == nil || v.RGBA.R != 0.5 || v.RGBA.A != 0.5 {
		t.Errorf("rgba mismatch: %+v", v.RGBA)
	}
	// 0.5×255=127.5 rounds to 128 (0x80); 0.25×255=63.75→64 (0x40);
	// 1.0×255=255 (0xff); alpha 0.5×255=127.5→128 (0x80).
	if v.Hex != "#8040ff80" {
		t.Errorf("hex mismatch: got %q, want #8040ff80", v.Hex)
	}
}

func TestClassifyValue_ColorWithoutAlpha_DefaultsToOne(t *testing.T) {
	v := classifyValue(map[string]any{"r": 1.0, "g": 1.0, "b": 1.0})
	if v.Hex != "#ffffff" {
		t.Errorf("white (no alpha) hex: got %q, want #ffffff", v.Hex)
	}
	if v.RGBA == nil || v.RGBA.A != 1.0 {
		t.Errorf("default alpha not 1.0: %+v", v.RGBA)
	}
}

func TestClassifyValue_ColorClamps(t *testing.T) {
	v := classifyValue(map[string]any{"r": 1.5, "g": -0.2, "b": 0.5})
	if v.RGBA.R != 1.0 || v.RGBA.G != 0.0 {
		t.Errorf("clamp failed: %+v", v.RGBA)
	}
	// 0.5×255=127.5 → 128 (0x80) per banker's-style rounding.
	if v.Hex != "#ff0080" {
		t.Errorf("clamped hex: got %q, want #ff0080", v.Hex)
	}
}

func TestClassifyValue_Number(t *testing.T) {
	v := classifyValue(float64(42))
	if v.Kind != ResolvedKindNumber || v.Number != 42 {
		t.Errorf("number: %+v", v)
	}
}

func TestClassifyValue_String(t *testing.T) {
	v := classifyValue("Inter")
	if v.Kind != ResolvedKindString || v.String != "Inter" {
		t.Errorf("string: %+v", v)
	}
}

func TestClassifyValue_NilReturnsNil(t *testing.T) {
	if got := classifyValue(nil); got != nil {
		t.Errorf("nil should return nil resolved, got %+v", got)
	}
}

func TestClassifyValue_UnknownPreservesRaw(t *testing.T) {
	v := classifyValue([]any{"a", "b"})
	if v.Kind != ResolvedKindUnknown {
		t.Errorf("unknown kind: %+v", v)
	}
	if v.Raw == nil {
		t.Errorf("raw missing")
	}
}

// ─── ModeResolver behavior ──────────────────────────────────────────────────

func TestModeResolver_HitsActiveMode(t *testing.T) {
	r := NewModeResolver("light", []ModeBindings{
		{Label: "light", Values: VariableValueMap{
			"VariableID:bg/0:0": map[string]any{"r": 1.0, "g": 1.0, "b": 1.0},
		}},
		{Label: "dark", Values: VariableValueMap{
			"VariableID:bg/0:0": map[string]any{"r": 0.1, "g": 0.1, "b": 0.1},
		}},
	})
	v := r.Resolve(BoundVariableRef{ID: "VariableID:bg/0:0"})
	if v == nil || v.Hex != "#ffffff" {
		t.Errorf("light mode resolution: %+v", v)
	}
}

func TestModeResolver_UnknownModeReturnsNil(t *testing.T) {
	r := NewModeResolver("sepia", []ModeBindings{
		{Label: "light", Values: VariableValueMap{
			"VariableID:bg/0:0": map[string]any{"r": 1.0, "g": 1.0, "b": 1.0},
		}},
	})
	if v := r.Resolve(BoundVariableRef{ID: "VariableID:bg/0:0"}); v != nil {
		t.Errorf("unknown mode should return nil, got %+v", v)
	}
}

func TestModeResolver_MissingVariableReturnsNil(t *testing.T) {
	r := NewModeResolver("light", []ModeBindings{
		{Label: "light", Values: VariableValueMap{}},
	})
	if v := r.Resolve(BoundVariableRef{ID: "VariableID:not-found:0"}); v != nil {
		t.Errorf("missing variable should return nil, got %+v", v)
	}
}

func TestModeResolver_CachesAcrossCalls(t *testing.T) {
	calls := 0
	values := VariableValueMap{
		"VariableID:bg/0:0": map[string]any{"r": 1.0, "g": 1.0, "b": 1.0},
	}
	// Wrap values map to count Get accesses... actually map access isn't
	// instrumentable cheaply. The cache is observable via second-call
	// determinism: even if we mutate `values` mid-stream the cached
	// resolution is sticky.
	r := NewModeResolver("light", []ModeBindings{{Label: "light", Values: values}})
	first := r.Resolve(BoundVariableRef{ID: "VariableID:bg/0:0"})
	delete(values, "VariableID:bg/0:0")
	second := r.Resolve(BoundVariableRef{ID: "VariableID:bg/0:0"})
	if first == nil || second == nil || first.Hex != second.Hex {
		t.Errorf("cache miss after mutation: first=%+v second=%+v", first, second)
	}
	_ = calls
}

// ─── ExtractBoundVariables ──────────────────────────────────────────────────

func TestExtractBoundVariables_SingleBinding(t *testing.T) {
	node := map[string]any{
		"id": "n1",
		"boundVariables": map[string]any{
			"fillStyleId": map[string]any{
				"type": "VARIABLE_ALIAS",
				"id":   "VariableID:text/0:0",
			},
		},
	}
	out := ExtractBoundVariables(node)
	if len(out) != 1 || out[0].Field != "fillStyleId" {
		t.Errorf("single binding: %+v", out)
	}
	if out[0].Binding.ID != "VariableID:text/0:0" {
		t.Errorf("id mismatch: %+v", out[0].Binding)
	}
}

func TestExtractBoundVariables_ArrayBinding(t *testing.T) {
	node := map[string]any{
		"id": "n2",
		"boundVariables": map[string]any{
			"fills": []any{
				map[string]any{"type": "VARIABLE_ALIAS", "id": "VariableID:bg/0:0"},
				map[string]any{"type": "VARIABLE_ALIAS", "id": "VariableID:bg/0:1"},
			},
		},
	}
	out := ExtractBoundVariables(node)
	if len(out) != 2 {
		t.Fatalf("array binding count: %+v", out)
	}
	if out[0].Field != "fills[0]" || out[1].Field != "fills[1]" {
		t.Errorf("field index mismatch: %+v", out)
	}
}

func TestExtractBoundVariables_NoBindings(t *testing.T) {
	node := map[string]any{"id": "n3"}
	if got := ExtractBoundVariables(node); got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

func TestExtractBoundVariables_DoesNotRecurse(t *testing.T) {
	// Children's boundVariables should NOT show up in the parent's extraction.
	node := map[string]any{
		"id":             "parent",
		"boundVariables": map[string]any{},
		"children": []any{
			map[string]any{
				"id": "child",
				"boundVariables": map[string]any{
					"fills": []any{map[string]any{"id": "VariableID:c/0:0"}},
				},
			},
		},
	}
	if got := ExtractBoundVariables(node); got != nil {
		t.Errorf("parent (empty bindings) should return nil; got %+v", got)
	}
}

// ─── ResolveTreeForMode (recursive walker) ──────────────────────────────────

func TestResolveTreeForMode_WalksChildrenAndResolves(t *testing.T) {
	tree := map[string]any{
		"id":   "root",
		"name": "Frame",
		"type": "FRAME",
		"children": []any{
			map[string]any{
				"id":   "child-1",
				"name": "Card",
				"type": "FRAME",
				"boundVariables": map[string]any{
					"fills": []any{
						map[string]any{"id": "VariableID:bg/0:0"},
					},
				},
			},
			map[string]any{
				"id":   "child-2",
				"name": "Text",
				"type": "TEXT",
			},
		},
	}
	resolver := NewModeResolver("light", []ModeBindings{
		{Label: "light", Values: VariableValueMap{
			"VariableID:bg/0:0": map[string]any{"r": 1.0, "g": 1.0, "b": 1.0},
		}},
	})
	out := ResolveTreeForMode(tree, resolver)
	if _, ok := out["child-1"]; !ok {
		t.Fatalf("child-1 should have a resolution map, got: %+v", out)
	}
	if v := out["child-1"]["fills[0]"]; v == nil || v.Hex != "#ffffff" {
		t.Errorf("child-1 fills[0]: %+v", v)
	}
	if _, ok := out["child-2"]; ok {
		t.Errorf("child-2 (no bindings) should be absent: %+v", out["child-2"])
	}
	if _, ok := out["root"]; ok {
		t.Errorf("root (no bindings) should be absent")
	}
}

// ─── ParseVariableValueMap ──────────────────────────────────────────────────

func TestParseVariableValueMap_ValidJSON(t *testing.T) {
	got := ParseVariableValueMap(`{"VariableID:a:0": 42}`)
	if v, ok := got["VariableID:a:0"]; !ok {
		t.Errorf("missing key: %+v", got)
	} else if vf, _ := v.(float64); vf != 42 {
		t.Errorf("value mismatch: got %v", v)
	}
}

func TestParseVariableValueMap_EmptyOrInvalid(t *testing.T) {
	if got := ParseVariableValueMap(""); got == nil || len(got) != 0 {
		t.Errorf("empty string should return empty map, got %+v", got)
	}
	if got := ParseVariableValueMap("not-json"); got == nil || len(got) != 0 {
		t.Errorf("invalid JSON should return empty map, got %+v", got)
	}
}
