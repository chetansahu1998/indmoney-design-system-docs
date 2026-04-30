// Package rules — Phase 3.5 — Go-side mirror of
// lib/projects/resolveTreeForMode.ts.
//
// The TS resolver runs in the JSON tab to render bound-variable chips
// with their resolved hex per active mode. This Go mirror runs in the
// audit pipeline so theme_parity + a11y_contrast can compare per-mode
// resolved values, not just the static canonical_tree.
//
// Why the Go version exists at all (Phase 2 prod-wire deferred this):
//   theme_parity must catch the AE-2 case — designer hand-paints a fill
//   in dark mode while light is bound. Without the resolver, the rule
//   sees identical canonical_trees for both modes (Phase 1 stores ONE
//   tree per screen). With the resolver, the rule applies each mode's
//   variable bindings to the tree and the diff catches the divergence.
//
//   a11y_contrast wants the resolved fill color (not the binding ID)
//   to compute contrast ratios. Bound fills currently emit
//   "a11y_unverifiable Info"; with the resolver they get evaluated
//   like raw fills.
//
// The semantics match the TS version exactly:
//   - kind="color" with hex + rgba {r,g,b,a} 0..1 floats
//   - kind="number" for plain numeric values
//   - kind="string" for plain strings
//   - kind="unknown" for everything else (including null/undefined)
//
// The TS file's `extractBoundVariables` helper is also ported here
// for callers that want to walk a node's bindings without descending.

package rules

import (
	"encoding/json"
	"fmt"
)

// ResolvedKind is the discriminator on ResolvedValue.
type ResolvedKind string

const (
	ResolvedKindColor   ResolvedKind = "color"
	ResolvedKindNumber  ResolvedKind = "number"
	ResolvedKindString  ResolvedKind = "string"
	ResolvedKindUnknown ResolvedKind = "unknown"
)

// ResolvedRGBA carries Figma-shaped color floats (0..1).
type ResolvedRGBA struct {
	R float64 `json:"r"`
	G float64 `json:"g"`
	B float64 `json:"b"`
	A float64 `json:"a"`
}

// ResolvedValue mirrors the TS ResolvedValue discriminated union.
// Hex/RGBA populated when Kind=="color"; Number when "number"; String
// when "string"; Raw is the original value preserved for "unknown".
type ResolvedValue struct {
	Kind   ResolvedKind `json:"kind"`
	Hex    string       `json:"hex,omitempty"`
	RGBA   *ResolvedRGBA `json:"rgba,omitempty"`
	Number float64      `json:"number,omitempty"`
	String string       `json:"string,omitempty"`
	Raw    any          `json:"raw,omitempty"`
}

// BoundVariableRef is the Figma REST shape for a binding.
type BoundVariableRef struct {
	Type string `json:"type,omitempty"`
	ID   string `json:"id"`
}

// VariableValueMap is the per-mode JSON object stored in
// screen_modes.explicit_variable_modes_json. Keys are Figma Variable
// IDs ("VariableID:abc/123:0"); values are the raw mode value
// (color / number / string / object).
type VariableValueMap map[string]any

// ModeBindings pairs a mode label with its parsed value map.
type ModeBindings struct {
	Label  string
	Values VariableValueMap
}

// ModeResolver is a per-mode lookup with internal caching. Mirror of
// the TS interface; build a fresh resolver per mode, never share
// across modes.
type ModeResolver struct {
	mode   string
	values VariableValueMap
	cache  map[string]*ResolvedValue
}

// NewModeResolver builds a resolver for the given activeMode label by
// looking up its values map in modeBindings. If activeMode doesn't
// match any provided binding, every Resolve() call returns nil.
func NewModeResolver(activeMode string, modeBindings []ModeBindings) *ModeResolver {
	var values VariableValueMap
	for _, mb := range modeBindings {
		if mb.Label == activeMode {
			values = mb.Values
			break
		}
	}
	return &ModeResolver{
		mode:   activeMode,
		values: values,
		cache:  map[string]*ResolvedValue{},
	}
}

// Mode returns the active mode label.
func (r *ModeResolver) Mode() string { return r.mode }

// Resolve looks up the binding's variable id in the active mode's
// values map. Returns nil when the resolver has no values for this
// mode OR the binding's id isn't present.
func (r *ModeResolver) Resolve(binding BoundVariableRef) *ResolvedValue {
	if binding.ID == "" {
		return nil
	}
	if cached, ok := r.cache[binding.ID]; ok {
		return cached
	}
	if r.values == nil {
		r.cache[binding.ID] = nil
		return nil
	}
	raw, ok := r.values[binding.ID]
	if !ok {
		r.cache[binding.ID] = nil
		return nil
	}
	resolved := classifyValue(raw)
	r.cache[binding.ID] = resolved
	return resolved
}

// classifyValue converts a raw Figma Variable value into a typed
// ResolvedValue. Mirrors the TS implementation: detects color-shaped
// objects ({r,g,b[,a]}) first, then numbers, then strings, fallthrough
// to unknown. Returns nil for null/missing.
func classifyValue(raw any) *ResolvedValue {
	if raw == nil {
		return nil
	}
	switch v := raw.(type) {
	case float64:
		return &ResolvedValue{Kind: ResolvedKindNumber, Number: v}
	case float32:
		return &ResolvedValue{Kind: ResolvedKindNumber, Number: float64(v)}
	case int:
		return &ResolvedValue{Kind: ResolvedKindNumber, Number: float64(v)}
	case int64:
		return &ResolvedValue{Kind: ResolvedKindNumber, Number: float64(v)}
	case string:
		return &ResolvedValue{Kind: ResolvedKindString, String: v}
	case map[string]any:
		// Color-shape detection: {r, g, b} with optional a.
		r, rok := numericField(v, "r")
		g, gok := numericField(v, "g")
		b, bok := numericField(v, "b")
		if rok && gok && bok {
			a, aok := numericField(v, "a")
			if !aok {
				a = 1
			}
			r = clamp01(r)
			g = clamp01(g)
			b = clamp01(b)
			a = clamp01(a)
			return &ResolvedValue{
				Kind: ResolvedKindColor,
				Hex:  rgbaToHex(r, g, b, a),
				RGBA: &ResolvedRGBA{R: r, G: g, B: b, A: a},
			}
		}
	}
	return &ResolvedValue{Kind: ResolvedKindUnknown, Raw: raw}
}

func numericField(m map[string]any, k string) (float64, bool) {
	v, ok := m[k]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}

func clamp01(n float64) float64 {
	if n < 0 {
		return 0
	}
	if n > 1 {
		return 1
	}
	return n
}

func rgbaToHex(r, g, b, a float64) string {
	ri := int(r*255 + 0.5)
	gi := int(g*255 + 0.5)
	bi := int(b*255 + 0.5)
	if a >= 0.999 {
		return fmt.Sprintf("#%02x%02x%02x", ri, gi, bi)
	}
	ai := int(a*255 + 0.5)
	return fmt.Sprintf("#%02x%02x%02x%02x", ri, gi, bi, ai)
}

// ─── extractBoundVariables (TS port) ────────────────────────────────────────

// BoundVariableEntry is one (field, binding) tuple. Field is the
// canonical_tree property name (e.g. "fills" or "fills[0]").
type BoundVariableEntry struct {
	Field   string
	Binding BoundVariableRef
}

// ExtractBoundVariables returns the boundVariables on a node — single-
// binding shape `{id, type}` and array shape `[{id, type}, …]` both
// supported. Returns nil when the node has no bindings (callers can
// early-return without invoking the resolver).
//
// Does NOT recurse into children — like the TS version.
func ExtractBoundVariables(node map[string]any) []BoundVariableEntry {
	if node == nil {
		return nil
	}
	bv, ok := node["boundVariables"].(map[string]any)
	if !ok {
		return nil
	}
	var out []BoundVariableEntry
	for field, val := range bv {
		// Single-binding {id, type}.
		if obj, ok := val.(map[string]any); ok {
			if id, _ := obj["id"].(string); id != "" {
				typ, _ := obj["type"].(string)
				out = append(out, BoundVariableEntry{
					Field:   field,
					Binding: BoundVariableRef{ID: id, Type: typ},
				})
				continue
			}
		}
		// Array binding [{id, type}, …].
		if arr, ok := val.([]any); ok {
			for i, entry := range arr {
				obj, ok := entry.(map[string]any)
				if !ok {
					continue
				}
				id, _ := obj["id"].(string)
				if id == "" {
					continue
				}
				typ, _ := obj["type"].(string)
				out = append(out, BoundVariableEntry{
					Field:   fmt.Sprintf("%s[%d]", field, i),
					Binding: BoundVariableRef{ID: id, Type: typ},
				})
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ─── Tree-wide resolution ───────────────────────────────────────────────────

// ResolveTreeForMode walks the canonical_tree and produces a parallel
// map of node-id → resolved-fields map. Used by theme_parity to
// compare per-mode resolved values across two trees.
//
// Each entry maps a field path (e.g. "fills[0]") to its ResolvedValue
// for the active mode. Nodes without any bindings are absent from the
// result map.
//
// The walker is recursive over `children` arrays — Figma's standard
// canonical_tree shape. Other arrays + scalar children are ignored.
func ResolveTreeForMode(
	tree map[string]any,
	resolver *ModeResolver,
) map[string]map[string]*ResolvedValue {
	out := map[string]map[string]*ResolvedValue{}
	walkResolveNode(tree, resolver, out)
	return out
}

func walkResolveNode(
	node any,
	resolver *ModeResolver,
	out map[string]map[string]*ResolvedValue,
) {
	m, ok := node.(map[string]any)
	if !ok {
		return
	}
	id, _ := m["id"].(string)
	if id != "" {
		entries := ExtractBoundVariables(m)
		if len(entries) > 0 {
			fields := map[string]*ResolvedValue{}
			for _, e := range entries {
				fields[e.Field] = resolver.Resolve(e.Binding)
			}
			out[id] = fields
		}
	}
	if children, ok := m["children"].([]any); ok {
		for _, c := range children {
			walkResolveNode(c, resolver, out)
		}
	}
}

// ─── Convenience: parse JSON-encoded VariableValueMap ───────────────────────

// ParseVariableValueMap decodes the screen_modes.explicit_variable_modes_json
// blob into a VariableValueMap. Empty / invalid JSON returns an empty
// (non-nil) map so callers don't have to nil-check.
func ParseVariableValueMap(raw string) VariableValueMap {
	if raw == "" {
		return VariableValueMap{}
	}
	var out VariableValueMap
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return VariableValueMap{}
	}
	return out
}
