package main

import (
	"testing"
)

// TestParseComponentPropertyDefinitions covers all four property types
// Figma exposes on a COMPONENT_SET — VARIANT, BOOLEAN, TEXT, INSTANCE_SWAP.
// The shapes differ enough that any future Figma schema change is most
// likely to leak through here first; this test keeps the contract honest.
func TestParseComponentPropertyDefinitions(t *testing.T) {
	raw := map[string]any{
		// VARIANT — bare name, variantOptions list.
		"State": map[string]any{
			"type":           "VARIANT",
			"defaultValue":   "Default",
			"variantOptions": []any{"Default", "Hover", "Pressed", "Disabled"},
		},
		// BOOLEAN — "#N:M" suffix preserved on the key. Default is a bool.
		"Show Icon#1:0": map[string]any{
			"type":         "BOOLEAN",
			"defaultValue": true,
		},
		// TEXT — also suffixed. Default is a string (the placeholder text).
		"Label#2:1": map[string]any{
			"type":         "TEXT",
			"defaultValue": "Continue",
		},
		// INSTANCE_SWAP — preferredValues list of {type, key} pointers.
		"Trailing Icon#3:2": map[string]any{
			"type":         "INSTANCE_SWAP",
			"defaultValue": "1234:5678",
			"preferredValues": []any{
				map[string]any{"type": "COMPONENT", "key": "abc-def"},
				map[string]any{"type": "COMPONENT_SET", "key": "ghi-jkl"},
			},
		},
	}

	got := parseComponentPropertyDefinitions(raw)
	if len(got) != 4 {
		t.Fatalf("want 4 props, got %d", len(got))
	}

	byName := map[string]ComponentProperty{}
	for _, p := range got {
		byName[p.Name] = p
	}

	// VARIANT
	v, ok := byName["State"]
	if !ok {
		t.Fatal("missing VARIANT prop State")
	}
	if v.Type != "VARIANT" {
		t.Errorf("State type = %q, want VARIANT", v.Type)
	}
	if dv, _ := v.DefaultValue.(string); dv != "Default" {
		t.Errorf("State default = %v, want Default", v.DefaultValue)
	}
	if len(v.VariantOptions) != 4 {
		t.Errorf("State options = %v, want 4", v.VariantOptions)
	}

	// BOOLEAN — name must keep its #1:0 suffix.
	b, ok := byName["Show Icon#1:0"]
	if !ok {
		t.Fatal("missing BOOLEAN prop Show Icon#1:0")
	}
	if b.Type != "BOOLEAN" {
		t.Errorf("Show Icon type = %q, want BOOLEAN", b.Type)
	}
	if dv, _ := b.DefaultValue.(bool); !dv {
		t.Errorf("Show Icon default = %v, want true", b.DefaultValue)
	}

	// TEXT
	tx, ok := byName["Label#2:1"]
	if !ok {
		t.Fatal("missing TEXT prop Label#2:1")
	}
	if tx.Type != "TEXT" {
		t.Errorf("Label type = %q, want TEXT", tx.Type)
	}
	if dv, _ := tx.DefaultValue.(string); dv != "Continue" {
		t.Errorf("Label default = %v, want Continue", tx.DefaultValue)
	}

	// INSTANCE_SWAP — preferredValues list survives.
	i, ok := byName["Trailing Icon#3:2"]
	if !ok {
		t.Fatal("missing INSTANCE_SWAP prop Trailing Icon#3:2")
	}
	if i.Type != "INSTANCE_SWAP" {
		t.Errorf("Trailing Icon type = %q, want INSTANCE_SWAP", i.Type)
	}
	if len(i.PreferredValues) != 2 {
		t.Errorf("Trailing Icon preferred = %v, want 2", i.PreferredValues)
	}
	if i.PreferredValues[0].Type != "COMPONENT" || i.PreferredValues[0].Key != "abc-def" {
		t.Errorf("Trailing Icon[0] = %+v, want {COMPONENT abc-def}", i.PreferredValues[0])
	}
}

// TestParseComponentPropertyDefinitions_Empty ensures graceful handling
// of components without prop definitions (single COMPONENT, not a set).
func TestParseComponentPropertyDefinitions_Empty(t *testing.T) {
	if got := parseComponentPropertyDefinitions(nil); got != nil {
		t.Errorf("nil input → %v, want nil", got)
	}
	if got := parseComponentPropertyDefinitions(map[string]any{}); got != nil {
		t.Errorf("empty map → %v, want nil", got)
	}
}

// TestParseAxisValues covers the variant-name parser used to derive
// per-variant axis tuples for the matrix view.
func TestParseAxisValues(t *testing.T) {
	cases := []struct {
		in   string
		want map[string]string
	}{
		{"State=Default, Size=Large", map[string]string{"State": "Default", "Size": "Large"}},
		{"  State = Hover ,  Size = Medium ", map[string]string{"State": "Hover", "Size": "Medium"}},
		{"Slide to Pay", nil},        // no = signs → nil (single-axis edge case)
		{"", nil},
		{"=", nil},                   // malformed → still nil
		{"Single=Only", map[string]string{"Single": "Only"}},
	}
	for _, c := range cases {
		got := parseAxisValues(c.in)
		if !mapEq(got, c.want) {
			t.Errorf("parseAxisValues(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func mapEq(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// TestComputeDefaultVariantID confirms the spatial top-left-wins rule
// for default-variant detection, matching Figma's own UI behavior.
func TestComputeDefaultVariantID(t *testing.T) {
	variants := []map[string]any{
		{"id": "1:1", "absoluteBoundingBox": map[string]any{"x": 100.0, "y": 100.0}},
		{"id": "1:2", "absoluteBoundingBox": map[string]any{"x": 0.0, "y": 0.0}},   // top-left
		{"id": "1:3", "absoluteBoundingBox": map[string]any{"x": 0.0, "y": 100.0}},
	}
	if id := computeDefaultVariantID(variants); id != "1:2" {
		t.Errorf("default = %q, want 1:2 (top-left)", id)
	}
	if id := computeDefaultVariantID(nil); id != "" {
		t.Errorf("nil → %q, want empty", id)
	}
}
