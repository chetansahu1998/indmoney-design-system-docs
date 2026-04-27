// Rich extraction types for the variants pipeline.
//
// These mirror the Figma REST API model as documented at
// https://github.com/figma/rest-api-spec/blob/main/dist/api_types.ts
// and the W3C DTCG token-format conventions Tokens Studio + Style
// Dictionary follow. The shape is intentionally a strict superset of the
// older flat manifest — old fields stay where they were so consumers
// (manifest.ts, ComponentInspector, audit-server) keep working untouched
// while richer surfaces opt into the new fields lazily.
//
// What "rich" buys over the old flat shape (slug + name + variant_id):
//   - All four component-property types (VARIANT, BOOLEAN, TEXT,
//     INSTANCE_SWAP) instead of only VARIANT name parsing.
//   - The `#0:0` suffix preserved on non-VARIANT properties so plugin
//     runtime calls to setProperties keep matching.
//   - The variant axis matrix: per-axis values, default, and complete
//     enumeration so designers see the variant grid at a glance.
//   - Component description (markdown) + documentationLinks.
//   - Default-variant detection by spatial sort (top-left wins per
//     Figma convention).
//   - Per-variant root frame: layout config (autolayout, padding, gap,
//     alignment, sizing, wrap), fills/strokes (with bound-variable
//     refs), effects, corner radius, opacity. These are the things that
//     make the audit honest about whether a token is bound at the
//     variant level.
//   - Children summary one level deep with property cascade refs
//     (componentPropertyReferences) so consumers see which child uses
//     which property — the structure of a variant without a full deep
//     copy of the node tree.
//   - Durable component_set / component `key` from
//     /v1/files/:key/components and /component_sets — stable across
//     publishes, the only safe identifier for cross-file references.
package main

// ComponentProperty mirrors a single entry in
// COMPONENT_SET.componentPropertyDefinitions. See
// https://www.figma.com/plugin-docs/api/properties/ComponentPropertiesMixin-componentpropertydefinitions/
type ComponentProperty struct {
	// Name preserves the full identifier as it appears in Figma —
	// VARIANT props are bare ("Size"), other types carry the "#N:M"
	// suffix ("ButtonText#0:1") to disambiguate same-display-name
	// properties. Plugin runtime APIs require this exact string.
	Name string `json:"name"`
	// Type is one of VARIANT / BOOLEAN / TEXT / INSTANCE_SWAP.
	// SLOT exists in newer Plugin API releases but isn't surfaced via
	// REST; ignored here.
	Type string `json:"type"`
	// DefaultValue is whatever the property defaults to when the
	// component is inserted from the library. Type matches Type:
	//   VARIANT       → string (one of VariantOptions)
	//   BOOLEAN       → bool
	//   TEXT          → string
	//   INSTANCE_SWAP → string (a node id like "1:1")
	DefaultValue any `json:"default_value,omitempty"`
	// VariantOptions is the full enumeration for VARIANT properties.
	// Empty for the other three types.
	VariantOptions []string `json:"variant_options,omitempty"`
	// PreferredValues is for INSTANCE_SWAP only — an ordered list of
	// {type, key} pointing at the swappable component sets. The `key`
	// (not id) is the durable identifier; key matches the Figma
	// component key returned by /v1/files/:key/components.
	PreferredValues []PreferredValue `json:"preferred_values,omitempty"`
}

// PreferredValue is a swappable component reference for INSTANCE_SWAP.
type PreferredValue struct {
	Type string `json:"type"` // "COMPONENT" | "COMPONENT_SET"
	Key  string `json:"key"`
}

// VariantAxis is the projection of one VARIANT property as a matrix
// axis. Built from a ComponentProperty with Type=VARIANT plus the
// observed values across the actual children (a partial axis is allowed
// when a designer skips combinations).
type VariantAxis struct {
	Name    string   `json:"name"`
	Values  []string `json:"values"`
	Default string   `json:"default,omitempty"`
}

// LayoutInfo captures the autolayout configuration of a variant's root
// frame. All fields are zero-valued when the frame doesn't use
// autolayout (Mode="" or "NONE").
type LayoutInfo struct {
	Mode          string  `json:"mode,omitempty"`           // HORIZONTAL / VERTICAL / NONE
	Wrap          string  `json:"wrap,omitempty"`           // NO_WRAP / WRAP
	PaddingLeft   float64 `json:"padding_left,omitempty"`
	PaddingRight  float64 `json:"padding_right,omitempty"`
	PaddingTop    float64 `json:"padding_top,omitempty"`
	PaddingBottom float64 `json:"padding_bottom,omitempty"`
	ItemSpacing   float64 `json:"item_spacing,omitempty"`
	CounterAxisSpacing float64 `json:"counter_axis_spacing,omitempty"`
	PrimaryAlign  string  `json:"primary_align,omitempty"`  // MIN/CENTER/MAX/SPACE_BETWEEN
	CounterAlign  string  `json:"counter_align,omitempty"`  // MIN/CENTER/MAX/BASELINE
	PrimarySizing string  `json:"primary_sizing,omitempty"` // FIXED / AUTO (legacy) — newer files use HUG/FILL/FIXED
	CounterSizing string  `json:"counter_sizing,omitempty"`
	ClipsContent  bool    `json:"clips_content,omitempty"`
	MinWidth      float64 `json:"min_width,omitempty"`
	MaxWidth      float64 `json:"max_width,omitempty"`
	MinHeight     float64 `json:"min_height,omitempty"`
	MaxHeight     float64 `json:"max_height,omitempty"`
}

// FillInfo describes a single Paint entry. Solid colors land as a hex
// string; gradients/images carry only the type tag (the visible delta
// between fills is the kind, not the gradient stops, for our use case).
type FillInfo struct {
	Type            string  `json:"type"`              // SOLID / GRADIENT_LINEAR / IMAGE / ...
	Color           string  `json:"color,omitempty"`   // hex when SOLID
	Opacity         float64 `json:"opacity,omitempty"`
	Visible         bool    `json:"visible"`
	BlendMode       string  `json:"blend_mode,omitempty"`
	BoundVariableID string  `json:"bound_variable_id,omitempty"`
}

// EffectInfo describes a single Effect entry. Mirrors REST shape; only
// the fields we surface in the docs UI are kept.
type EffectInfo struct {
	Type    string  `json:"type"`              // DROP_SHADOW / INNER_SHADOW / LAYER_BLUR / BACKGROUND_BLUR
	Radius  float64 `json:"radius,omitempty"`
	Spread  float64 `json:"spread,omitempty"`
	OffsetX float64 `json:"offset_x,omitempty"`
	OffsetY float64 `json:"offset_y,omitempty"`
	Color   string  `json:"color,omitempty"`
	Visible bool    `json:"visible"`
	BoundVariableID string `json:"bound_variable_id,omitempty"`
}

// CornerInfo summarises corner radius. Uniform when all four corners
// are equal (the common case); otherwise Individual carries
// [topLeft, topRight, bottomRight, bottomLeft] per the REST schema.
type CornerInfo struct {
	Uniform    float64   `json:"uniform,omitempty"`
	Individual []float64 `json:"individual,omitempty"`
	Smoothing  float64   `json:"smoothing,omitempty"`
	BoundVariableID string `json:"bound_variable_id,omitempty"`
}

// ChildSummary is one-level-deep info about a variant's children. It's
// intentionally NOT a full recursive node copy — it surfaces the things
// designers and the audit care about: what type of child, what role it
// plays (is it the icon? the label?), what properties it cascades from
// (componentPropertyReferences), and what variables it's bound to.
type ChildSummary struct {
	ID             string            `json:"id"`
	Type           string            `json:"type"`
	Name           string            `json:"name"`
	ComponentID    string            `json:"component_id,omitempty"`
	Characters     string            `json:"characters,omitempty"`
	PropertyRefs   map[string]string `json:"property_refs,omitempty"`
	BoundVariables map[string]string `json:"bound_variables,omitempty"`
	Width          int               `json:"width,omitempty"`
	Height         int               `json:"height,omitempty"`
}

// Note: Variant and IconEntry are extended in main.go; their existing
// definitions there are kept for backwards compat and additional fields
// are added inline. See main.go for the full struct.
