// Rich-extraction helpers for the variants pipeline.
//
// Every helper takes the raw map[string]any returned by Figma's REST API
// (because we use untyped JSON walking — the alternative would be to
// import figma/rest-api-spec types, but those are TypeScript) and emits
// the strongly-typed projections defined in types.go.
//
// Conventions:
//   - All helpers are defensive: missing fields collapse to zero values.
//     A variant with no fills emits Fills: nil, not Fills: [].
//   - Rounding: float coordinates are kept as-is in the rich types so
//     consumers can decide how to render. Width/Height ints are kept on
//     the legacy Variant fields for compat.
//   - boundVariables: we only surface the variable id string, not the
//     full {id, type} object the Plugin API exposes — REST returns just
//     ids and consumers cross-reference at apply time anyway.

package main

import (
	"fmt"
	"sort"
	"strings"
)

// parseComponentPropertyDefinitions converts the raw
// componentPropertyDefinitions map into a sorted slice of
// ComponentProperty. Keys are sorted alphabetically for stable JSON
// output; this also groups properties of the same type together since
// VARIANT names are bare and other types carry "#N:M" suffixes.
func parseComponentPropertyDefinitions(raw map[string]any) []ComponentProperty {
	if len(raw) == 0 {
		return nil
	}
	keys := make([]string, 0, len(raw))
	for k := range raw {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]ComponentProperty, 0, len(keys))
	for _, name := range keys {
		def, _ := raw[name].(map[string]any)
		if def == nil {
			continue
		}
		typeStr, _ := def["type"].(string)
		if typeStr == "" {
			continue
		}
		p := ComponentProperty{
			Name:         name,
			Type:         typeStr,
			DefaultValue: def["defaultValue"],
		}
		// VARIANT options
		if vo, ok := def["variantOptions"].([]any); ok {
			for _, v := range vo {
				if s, ok := v.(string); ok {
					p.VariantOptions = append(p.VariantOptions, s)
				}
			}
		}
		// INSTANCE_SWAP preferred values — Figma returns a list of
		// {type, key} pointing at swappable component sets.
		if pv, ok := def["preferredValues"].([]any); ok {
			for _, v := range pv {
				vm, _ := v.(map[string]any)
				if vm == nil {
					continue
				}
				t, _ := vm["type"].(string)
				k, _ := vm["key"].(string)
				if t != "" && k != "" {
					p.PreferredValues = append(p.PreferredValues, PreferredValue{Type: t, Key: k})
				}
			}
		}
		out = append(out, p)
	}
	return out
}

// parseAxisValues extracts the variant axis tuple from a variant name.
// "State=Default, Size=Large" → {"State":"Default","Size":"Large"}.
// Returns nil if the name doesn't parse as axis tuples (e.g. a
// non-VARIANT-organized component set).
func parseAxisValues(name string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(name, ",") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			continue
		}
		k := strings.TrimSpace(kv[0])
		v := strings.TrimSpace(kv[1])
		if k != "" {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// computeDefaultVariantID picks the variant Figma considers the default
// — the spatially top-left one. REST doesn't expose Plugin's
// `defaultVariant`, so we reconstruct it by sorting children by their
// absoluteBoundingBox (y first, then x). Empty list returns "".
func computeDefaultVariantID(variants []map[string]any) string {
	type pos struct {
		id string
		x  float64
		y  float64
	}
	xs := make([]pos, 0, len(variants))
	for _, cm := range variants {
		id, _ := cm["id"].(string)
		bbox, _ := cm["absoluteBoundingBox"].(map[string]any)
		var x, y float64
		if bbox != nil {
			if v, ok := bbox["x"].(float64); ok {
				x = v
			}
			if v, ok := bbox["y"].(float64); ok {
				y = v
			}
		}
		xs = append(xs, pos{id, x, y})
	}
	if len(xs) == 0 {
		return ""
	}
	sort.SliceStable(xs, func(i, j int) bool {
		if xs[i].y != xs[j].y {
			return xs[i].y < xs[j].y
		}
		return xs[i].x < xs[j].x
	})
	return xs[0].id
}

// buildVariantAxes synthesises the matrix view of VARIANT properties
// from the property definitions and the actually-observed variants.
// VariantOptions on the def is the canonical list; observed values
// (parsed from variant names) act as a sanity check — if a variant has
// a value not in VariantOptions, that means the definition has drifted
// (rare but possible mid-edit).
func buildVariantAxes(defs []ComponentProperty, variants []map[string]any, defaultID string) []VariantAxis {
	if len(defs) == 0 {
		return nil
	}
	// Find default-variant axis values to mark per-axis defaults.
	var defaultAxes map[string]string
	for _, cm := range variants {
		if id, _ := cm["id"].(string); id == defaultID {
			name, _ := cm["name"].(string)
			defaultAxes = parseAxisValues(name)
			break
		}
	}
	out := make([]VariantAxis, 0)
	for _, p := range defs {
		if p.Type != "VARIANT" {
			continue
		}
		axis := VariantAxis{
			Name:   p.Name,
			Values: append([]string{}, p.VariantOptions...),
		}
		if defaultAxes != nil {
			axis.Default = defaultAxes[p.Name]
		}
		// Fall back to the def's defaultValue if we couldn't compute
		// from the default variant.
		if axis.Default == "" {
			if dv, ok := p.DefaultValue.(string); ok {
				axis.Default = dv
			}
		}
		out = append(out, axis)
	}
	return out
}

// extractLayout reads autolayout config off a frame node. Returns nil
// when the node has no autolayout (layoutMode unset or "NONE") since
// the rest of the fields are meaningless without a mode.
func extractLayout(n map[string]any) *LayoutInfo {
	mode, _ := n["layoutMode"].(string)
	if mode == "" || mode == "NONE" {
		return nil
	}
	li := &LayoutInfo{Mode: mode}
	if v, ok := n["layoutWrap"].(string); ok {
		li.Wrap = v
	}
	li.PaddingLeft, _ = n["paddingLeft"].(float64)
	li.PaddingRight, _ = n["paddingRight"].(float64)
	li.PaddingTop, _ = n["paddingTop"].(float64)
	li.PaddingBottom, _ = n["paddingBottom"].(float64)
	li.ItemSpacing, _ = n["itemSpacing"].(float64)
	li.CounterAxisSpacing, _ = n["counterAxisSpacing"].(float64)
	li.PrimaryAlign, _ = n["primaryAxisAlignItems"].(string)
	li.CounterAlign, _ = n["counterAxisAlignItems"].(string)
	// Newer files use layoutSizingHorizontal/Vertical; older have
	// primaryAxisSizingMode/counterAxisSizingMode. Pick whichever is
	// present and fold into the same fields.
	if v, ok := n["primaryAxisSizingMode"].(string); ok {
		li.PrimarySizing = v
	}
	if v, ok := n["counterAxisSizingMode"].(string); ok {
		li.CounterSizing = v
	}
	if v, ok := n["layoutSizingHorizontal"].(string); ok && li.PrimarySizing == "" {
		li.PrimarySizing = v
	}
	if v, ok := n["layoutSizingVertical"].(string); ok && li.CounterSizing == "" {
		li.CounterSizing = v
	}
	li.ClipsContent, _ = n["clipsContent"].(bool)
	li.MinWidth, _ = n["minWidth"].(float64)
	li.MaxWidth, _ = n["maxWidth"].(float64)
	li.MinHeight, _ = n["minHeight"].(float64)
	li.MaxHeight, _ = n["maxHeight"].(float64)
	return li
}

// extractPaints converts a fills/strokes array into FillInfo records.
// Defaults visible=true when the field is absent (Figma's behavior).
func extractPaints(raw any) []FillInfo {
	arr, _ := raw.([]any)
	if len(arr) == 0 {
		return nil
	}
	out := make([]FillInfo, 0, len(arr))
	for _, p := range arr {
		pm, _ := p.(map[string]any)
		if pm == nil {
			continue
		}
		fi := FillInfo{Visible: true}
		if v, ok := pm["visible"].(bool); ok {
			fi.Visible = v
		}
		fi.Type, _ = pm["type"].(string)
		if v, ok := pm["opacity"].(float64); ok {
			fi.Opacity = v
		}
		if v, ok := pm["blendMode"].(string); ok {
			fi.BlendMode = v
		}
		if fi.Type == "SOLID" {
			if cm, ok := pm["color"].(map[string]any); ok {
				fi.Color = colorToHex(cm)
			}
		}
		// boundVariables on a paint live at .boundVariables.color
		if bv, ok := pm["boundVariables"].(map[string]any); ok {
			if c, ok := bv["color"].(map[string]any); ok {
				if id, _ := c["id"].(string); id != "" {
					fi.BoundVariableID = id
				}
			}
		}
		out = append(out, fi)
	}
	return out
}

// extractEffects converts the effects array. Mirrors REST schema.
func extractEffects(raw any) []EffectInfo {
	arr, _ := raw.([]any)
	if len(arr) == 0 {
		return nil
	}
	out := make([]EffectInfo, 0, len(arr))
	for _, e := range arr {
		em, _ := e.(map[string]any)
		if em == nil {
			continue
		}
		ei := EffectInfo{Visible: true}
		if v, ok := em["visible"].(bool); ok {
			ei.Visible = v
		}
		ei.Type, _ = em["type"].(string)
		if v, ok := em["radius"].(float64); ok {
			ei.Radius = v
		}
		if v, ok := em["spread"].(float64); ok {
			ei.Spread = v
		}
		if off, ok := em["offset"].(map[string]any); ok {
			if x, _ := off["x"].(float64); x != 0 {
				ei.OffsetX = x
			}
			if y, _ := off["y"].(float64); y != 0 {
				ei.OffsetY = y
			}
		}
		if cm, ok := em["color"].(map[string]any); ok {
			ei.Color = colorToHex(cm)
		}
		if bv, ok := em["boundVariables"].(map[string]any); ok {
			if c, ok := bv["color"].(map[string]any); ok {
				if id, _ := c["id"].(string); id != "" {
					ei.BoundVariableID = id
				}
			}
		}
		out = append(out, ei)
	}
	return out
}

// extractCorner reads corner radius — uniform when all four are equal,
// individual otherwise. Smoothing is the squircle factor; bound variable
// is the single uniform-corner binding (per-corner bindings rare, ignored).
func extractCorner(n map[string]any) *CornerInfo {
	ci := &CornerInfo{}
	hasAny := false
	if v, ok := n["cornerRadius"].(float64); ok && v > 0 {
		ci.Uniform = v
		hasAny = true
	}
	if v, ok := n["cornerSmoothing"].(float64); ok && v > 0 {
		ci.Smoothing = v
		hasAny = true
	}
	if arr, ok := n["rectangleCornerRadii"].([]any); ok && len(arr) == 4 {
		ind := make([]float64, 4)
		for i, x := range arr {
			if f, ok := x.(float64); ok {
				ind[i] = f
			}
		}
		// Only emit individual when not all equal.
		if !(ind[0] == ind[1] && ind[1] == ind[2] && ind[2] == ind[3]) {
			ci.Individual = ind
			hasAny = true
		}
	}
	if bv, ok := n["boundVariables"].(map[string]any); ok {
		// boundVariables.cornerRadius lives at top level (uniform)
		if c, ok := bv["cornerRadius"].(map[string]any); ok {
			if id, _ := c["id"].(string); id != "" {
				ci.BoundVariableID = id
				hasAny = true
			}
		}
	}
	if !hasAny {
		return nil
	}
	return ci
}

// extractBoundVarIDs flattens a node's top-level boundVariables map
// into {field → variable_id}. Used for fields that aren't paint-specific
// (fontSize, fontWeight, itemSpacing, paddingLeft, etc.).
//
// Paint bindings (.boundVariables.fills[].color, .strokes[].color) are
// NOT included here — they live on the FillInfo entries themselves so
// each paint keeps its own binding.
func extractBoundVarIDs(raw any) map[string]string {
	m, _ := raw.(map[string]any)
	if len(m) == 0 {
		return nil
	}
	out := map[string]string{}
	for k, v := range m {
		// Skip paint-array bindings (those go on FillInfo).
		if k == "fills" || k == "strokes" || k == "effects" {
			continue
		}
		// Single-binding shape: { id, type }
		if vm, ok := v.(map[string]any); ok {
			if id, _ := vm["id"].(string); id != "" {
				out[k] = id
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// extractChildren walks a variant's first-level children and emits a
// summary per child. Intentionally NOT recursive — designers and the
// audit just need to see "here's the structure": 1 icon instance, 1
// text node, 1 background frame. Deeper nesting is captured at audit
// time when we walk node trees from screens.
func extractChildren(raw any) []ChildSummary {
	arr, _ := raw.([]any)
	if len(arr) == 0 {
		return nil
	}
	out := make([]ChildSummary, 0, len(arr))
	for _, ch := range arr {
		cm, _ := ch.(map[string]any)
		if cm == nil {
			continue
		}
		cs := ChildSummary{}
		cs.ID, _ = cm["id"].(string)
		cs.Type, _ = cm["type"].(string)
		cs.Name, _ = cm["name"].(string)
		if cs.Type == "INSTANCE" {
			cs.ComponentID, _ = cm["componentId"].(string)
		}
		if cs.Type == "TEXT" {
			cs.Characters, _ = cm["characters"].(string)
		}
		if bbox, ok := cm["absoluteBoundingBox"].(map[string]any); ok {
			w, h := dim(bbox)
			cs.Width = w
			cs.Height = h
		}
		// Property cascade: visible→IconVisible#0:0, characters→Label#0:1, etc.
		if refs, ok := cm["componentPropertyReferences"].(map[string]any); ok {
			cs.PropertyRefs = make(map[string]string, len(refs))
			for k, v := range refs {
				if s, ok := v.(string); ok {
					cs.PropertyRefs[k] = s
				}
			}
		}
		// Top-level bound variables on the child.
		cs.BoundVariables = extractBoundVarIDs(cm["boundVariables"])
		out = append(out, cs)
	}
	return out
}

// colorToHex converts a Figma color object {r,g,b,a} (0..1) to
// "#RRGGBB" (6-char, alpha stripped — alpha lives on the parent paint's
// `opacity`). Returns "" on malformed input.
func colorToHex(c map[string]any) string {
	if c == nil {
		return ""
	}
	r, rOk := c["r"].(float64)
	g, gOk := c["g"].(float64)
	b, bOk := c["b"].(float64)
	if !rOk || !gOk || !bOk {
		return ""
	}
	clamp := func(x float64) int {
		v := int(x*255 + 0.5)
		if v < 0 {
			v = 0
		}
		if v > 255 {
			v = 255
		}
		return v
	}
	return fmt.Sprintf("#%02X%02X%02X", clamp(r), clamp(g), clamp(b))
}
