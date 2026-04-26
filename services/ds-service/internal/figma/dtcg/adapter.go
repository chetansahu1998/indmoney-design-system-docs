// Package dtcg converts extractor.Result into W3C Design Tokens (DTCG) JSON files.
//
// Output shape:
//
//	lib/tokens/<brand>/
//	  base.tokens.json       — primitives (one entry per distinct hex)
//	  semantic.tokens.json   — semantic aliases (light = default mode)
//	  semantic-dark.tokens.json — dark-mode overrides
//	  text-styles.tokens.json — typography (from published TEXT styles)
//
// Token shape (v1 — kept simple):
//
//	{
//	  "colour": {
//	    "surface": {
//	      "primary": {
//	        "$type": "color",
//	        "$value": "{base.colour.token-c-FFFFFF}"
//	      }
//	    }
//	  }
//	}
package dtcg

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/figma/extractor"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/figma/types"
)

// Tree is a recursive map that JSON-encodes to a DTCG document.
type Tree map[string]any

// AdaptResult converts a Result into the four DTCG files.
type Files struct {
	Base         []byte // base.tokens.json
	Semantic     []byte // semantic.tokens.json (light)
	SemanticDark []byte // semantic-dark.tokens.json
	TextStyles   []byte // text-styles.tokens.json
	ContractMeta []byte // _contract_check block — sidecar for CI
}

func Adapt(r *extractor.Result) (*Files, error) {
	base := buildBase(r.BasePalette)
	semLight, semDark := buildSemantic(r.Roles)

	baseBytes, err := encode(base)
	if err != nil {
		return nil, fmt.Errorf("encode base: %w", err)
	}
	semBytes, err := encode(semLight)
	if err != nil {
		return nil, fmt.Errorf("encode semantic: %w", err)
	}
	semDarkBytes, err := encode(semDark)
	if err != nil {
		return nil, fmt.Errorf("encode semantic-dark: %w", err)
	}
	textStyles := buildTextStyles(r.TextStyles)
	textBytes, err := encode(textStyles)
	if err != nil {
		return nil, fmt.Errorf("encode text-styles: %w", err)
	}

	sourceSummaries := make([]map[string]any, 0, len(r.Sources))
	for _, s := range r.Sources {
		sourceSummaries = append(sourceSummaries, map[string]any{
			"kind":         string(s.Source.Kind),
			"file_key":     s.Source.FileKey,
			"file_name":    s.Name,
			"node_id":      s.Source.NodeID,
			"frames":       s.CandidateCount,
			"pairs":        s.PairCount,
			"observations": len(s.Observations),
			"text_styles":  len(s.TextStyles),
		})
	}
	contract := map[string]any{
		"brand":          r.Brand,
		"sources":        sourceSummaries,
		"frames":         r.CandidateCount(),
		"pairs":          r.PairCount(),
		"observations":   len(r.Observations),
		"roles":          len(r.Roles),
		"base_colors":    len(r.BasePalette),
		"text_styles":    len(r.TextStyles),
	}
	contractBytes, _ := json.MarshalIndent(contract, "", "  ")

	return &Files{
		Base:         baseBytes,
		Semantic:     semBytes,
		SemanticDark: semDarkBytes,
		TextStyles:   textBytes,
		ContractMeta: contractBytes,
	}, nil
}

func encode(t Tree) ([]byte, error) {
	return json.MarshalIndent(t, "", "  ")
}

// buildBase emits primitives keyed by a deterministic name derived from the hex.
//
// Naming: `colour.<bucket>.<token-id>` where bucket is one of {green, red, orange,
// blue, grey, neutral-light, neutral-dark, other} and token-id is the hex (lowercase, no #).
// Example: `base.colour.surface.ffffff`.
//
// Color values use W3C-DTCG 2024 object form per Terrazzo 2.x:
//
//	{ "colorSpace": "srgb", "components": [r, g, b], "alpha": <0-1> }
func buildBase(palette map[string]types.Color) Tree {
	colours := Tree{}
	hexes := make([]string, 0, len(palette))
	for h := range palette {
		hexes = append(hexes, h)
	}
	sort.Strings(hexes)

	for _, h := range hexes {
		c := palette[h]
		bucket := paletteBucket(c)
		if _, ok := colours[bucket]; !ok {
			colours[bucket] = Tree{
				"$type": "color",
			}
		}
		bucketTree := colours[bucket].(Tree)
		id := strings.ToLower(strings.TrimPrefix(h, "#"))
		bucketTree[id] = Tree{
			"$value": colorValue(c),
		}
	}

	return Tree{
		"base": Tree{
			"colour": colours,
		},
	}
}

// colorValue converts a Color to the W3C-DTCG 2024 sRGB object form.
// Components are 0..1 floats; alpha is omitted when >= 1.0.
func colorValue(c types.Color) Tree {
	out := Tree{
		"colorSpace": "srgb",
		"components": []float64{
			roundFloat(float64(c.R)/255, 4),
			roundFloat(float64(c.G)/255, 4),
			roundFloat(float64(c.B)/255, 4),
		},
	}
	if c.A < 0.999 {
		out["alpha"] = roundFloat(c.A, 3)
	}
	return out
}

func roundFloat(v float64, places int) float64 {
	scale := 1.0
	for i := 0; i < places; i++ {
		scale *= 10
	}
	return float64(int(v*scale+0.5)) / scale
}

// paletteBucket assigns a colour to a high-level family bucket based on lightness/saturation.
func paletteBucket(c types.Color) string {
	r, g, b := float64(c.R)/255, float64(c.G)/255, float64(c.B)/255
	max_ := r
	if g > max_ {
		max_ = g
	}
	if b > max_ {
		max_ = b
	}
	min_ := r
	if g < min_ {
		min_ = g
	}
	if b < min_ {
		min_ = b
	}
	chroma := max_ - min_
	l := c.Lightness()

	switch {
	case chroma < 0.05 && l > 0.85:
		return "neutral-light"
	case chroma < 0.05 && l < 0.15:
		return "neutral-dark"
	case chroma < 0.05:
		return "grey"
	case r > 0.6 && g < 0.5 && b < 0.5:
		return "red"
	case r > 0.7 && g > 0.4 && b < 0.4:
		return "orange"
	case g > 0.5 && r < 0.6 && b < 0.6:
		return "green"
	case b > 0.5 && r < 0.5:
		return "blue"
	}
	return "other"
}

// buildSemantic emits the (light, dark) mode-paired semantic tokens.
//
// We emit ONE token per role, keyed by a path derived from the canonical name,
// with $value = the light hex. Dark mode overrides go into a parallel file with
// the same paths but dark hex values. Terrazzo merges both via permutations.
func buildSemantic(roles []extractor.SemanticRole) (Tree, Tree) {
	light := Tree{}
	dark := Tree{}

	for i, r := range roles {
		path, _ := derivePath(r, i)
		if path == nil {
			continue
		}
		// Light value
		if r.HasLight {
			setNested(light, path, Tree{
				"$type":  "color",
				"$value": colorValue(r.Light),
				"$description": fmt.Sprintf("Observed in %d Figma nodes; canonical name: %q. Other names: %s",
					r.InstanceCount, r.NamesCanonical, joinShort(r.Names, 5)),
			})
		}
		if r.HasDark {
			setNested(dark, path, Tree{
				"$type":  "color",
				"$value": colorValue(r.Dark),
			})
		}
	}

	return wrapColour(light), wrapColour(dark)
}

func wrapColour(t Tree) Tree {
	if len(t) == 0 {
		return Tree{}
	}
	return Tree{"colour": t}
}

// derivePath turns a SemanticRole into a structured DTCG path like
// ["surface", "primary"] or ["text-n-icon", "secondary"].
//
// Strategy v1: bucket by role characteristics (lightness, dark mode pair),
// then derive a per-token leaf name from canonical Figma name OR from
// instance-rank within the bucket. Real semantic naming requires designer review (Phase 9).
func derivePath(r extractor.SemanticRole, idx int) ([]string, error) {
	bucket := classifyRole(r)
	leaf := deriveLeaf(r, idx)
	return []string{bucket, leaf}, nil
}

// deriveLeaf picks the most descriptive token-leaf name from a role's observations.
// Priority: descriptive canonical name → instance-count rank label → numeric fallback.
func deriveLeaf(r extractor.SemanticRole, idx int) string {
	if r.NamesCanonical != "" && !isAutoGen(r.NamesCanonical) {
		return slugify(r.NamesCanonical)
	}
	// Rank-based label: tokens with most observations get descriptive names.
	switch {
	case r.InstanceCount >= 100:
		return fmt.Sprintf("primary-%03d", idx+1)
	case r.InstanceCount >= 30:
		return fmt.Sprintf("secondary-%03d", idx+1)
	case r.InstanceCount >= 10:
		return fmt.Sprintf("muted-%03d", idx+1)
	default:
		return fmt.Sprintf("accent-%03d", idx+1)
	}
}

func isAutoGen(s string) bool {
	lower := strings.ToLower(s)
	for _, p := range []string{"rectangle ", "frame ", "ellipse ", "vector", "path", "group ", "instance ", "oval", "combined shape", "right text", "right icon", "left icon"} {
		if strings.HasPrefix(lower, p) || lower == p {
			return true
		}
	}
	return false
}

// classifyRole returns a high-level grouping for the semantic token based on
// (a) light/dark contrast pattern, (b) saturation (status colors), and
// (c) mode-invariance signal.
//
// Buckets emitted:
//   surface, surface-elevated, text-n-icon, border,
//   success, danger, warning, info, accent (mode-invariant saturated),
//   constant (mode-invariant neutral), other
func classifyRole(r extractor.SemanticRole) string {
	// Mode-invariant case: same color in both modes — these aren't theme tokens.
	// Saturated → accent / status; neutral → constant.
	if r.IsModeInvariant {
		c := r.Light // == Dark
		if isSaturated(c) {
			return statusFamily(c, c)
		}
		if c.Lightness() > 0.85 {
			return "constant-light" // platform white / on-dark text constant
		}
		if c.Lightness() < 0.15 {
			return "constant-dark"
		}
		return "constant"
	}

	if r.HasLight && r.HasDark {
		ll := r.Light.Lightness()
		dl := r.Dark.Lightness()

		// Saturated pair — status color regardless of contrast pattern.
		if isSaturated(r.Light) || isSaturated(r.Dark) {
			return statusFamily(r.Light, r.Dark)
		}

		// High contrast page-bg pair (light very light, dark very dark).
		if ll > 0.92 && dl < 0.10 {
			return "surface"
		}
		// Slightly less contrast — elevated surface (cards on page).
		if ll > 0.85 && dl < 0.20 {
			return "surface-elevated"
		}
		// Inverted contrast: dark on light, light on dark — text/icon.
		if (ll < 0.45 && dl > 0.55) || (ll > 0.55 && dl < 0.45) {
			return "text-n-icon"
		}
		// Both moderately light/dark — likely border or divider.
		if ll > 0.55 && ll < 0.92 && dl > 0.20 && dl < 0.55 {
			return "border"
		}
		// Default fallback.
		return "surface-elevated"
	}

	// Single-mode entries — likely status accents.
	if r.HasLight && isSaturated(r.Light) {
		return statusFamily(r.Light, r.Light)
	}
	if r.HasDark && isSaturated(r.Dark) {
		return statusFamily(r.Dark, r.Dark)
	}
	return "other"
}

func isSaturated(c types.Color) bool {
	r, g, b := float64(c.R)/255, float64(c.G)/255, float64(c.B)/255
	max_ := r
	if g > max_ {
		max_ = g
	}
	if b > max_ {
		max_ = b
	}
	min_ := r
	if g < min_ {
		min_ = g
	}
	if b < min_ {
		min_ = b
	}
	return max_-min_ > 0.3
}

// statusFamily classifies a saturated color into success/warning/danger/info
// using HSL hue ranges. Operates on whichever side has more saturation (the
// "signal" side); the other side is usually a desaturated container.
func statusFamily(light, dark types.Color) string {
	c := light
	if saturationFloat(dark) > saturationFloat(light) {
		c = dark
	}
	hue := hueDeg(c)
	// Hue ranges (HSL), tuned to match common UI palettes:
	//   red/danger:    340..360 OR 0..15
	//   orange/warning:15..45
	//   yellow/warning:45..70
	//   green/success: 70..170
	//   teal/info:     170..200
	//   blue/info:     200..260
	//   purple/info:   260..340
	switch {
	case hue >= 340 || hue < 15:
		return "danger"
	case hue < 45:
		return "warning"
	case hue < 70:
		return "warning"
	case hue < 170:
		return "success"
	case hue < 200:
		return "info"
	case hue < 260:
		return "info"
	default:
		return "info" // purple → info bucket for now
	}
}

// hueDeg returns HSL hue in degrees [0, 360).
func hueDeg(c types.Color) float64 {
	r := float64(c.R) / 255
	g := float64(c.G) / 255
	b := float64(c.B) / 255
	max_, min_ := r, r
	if g > max_ {
		max_ = g
	}
	if b > max_ {
		max_ = b
	}
	if g < min_ {
		min_ = g
	}
	if b < min_ {
		min_ = b
	}
	chroma := max_ - min_
	if chroma == 0 {
		return 0
	}
	var h float64
	switch max_ {
	case r:
		h = (g - b) / chroma
		if g < b {
			h += 6
		}
	case g:
		h = (b-r)/chroma + 2
	default:
		h = (r-g)/chroma + 4
	}
	return h * 60
}

func saturationFloat(c types.Color) float64 {
	r := float64(c.R) / 255
	g := float64(c.G) / 255
	b := float64(c.B) / 255
	max_, min_ := r, r
	if g > max_ {
		max_ = g
	}
	if b > max_ {
		max_ = b
	}
	if g < min_ {
		min_ = g
	}
	if b < min_ {
		min_ = b
	}
	l := (max_ + min_) / 2
	if max_ == min_ {
		return 0
	}
	if l < 0.5 {
		return (max_ - min_) / (max_ + min_)
	}
	return (max_ - min_) / (2 - max_ - min_)
}

// setNested writes value at path into a nested Tree, creating intermediate maps.
func setNested(t Tree, path []string, value any) {
	cursor := t
	for i, seg := range path {
		if i == len(path)-1 {
			cursor[seg] = value
			return
		}
		next, ok := cursor[seg].(Tree)
		if !ok {
			next = Tree{}
			cursor[seg] = next
		}
		cursor = next
	}
}

// buildTextStyles emits typography tokens only if the style has resolved font metadata.
// /v1/files/<key>/styles returns names + node_ids only — actual font data requires
// follow-up /v1/files/<key>/nodes calls (deferred to v1.1). Until then we skip
// emission so Field's existing text-styles.tokens.json is preserved.
func buildTextStyles(styles []extractor.TextStyle) Tree {
	if len(styles) == 0 {
		return Tree{}
	}
	t := Tree{}
	for _, s := range styles {
		if s.FontFamily == "" || s.FontSize <= 0 {
			continue // metadata not yet resolved; leave Field's defaults in place
		}
		key := slugify(s.Name)
		t[key] = Tree{
			"$type": "typography",
			"$value": Tree{
				"fontFamily":     []string{s.FontFamily},
				"fontWeight":     s.FontWeight,
				"fontSize":       Tree{"value": s.FontSize, "unit": "px"},
				"lineHeight":     s.LineHeight,
				"letterSpacing":  Tree{"value": s.LetterSpace, "unit": "px"},
			},
			"$description": fmt.Sprintf("Source: Figma TEXT style %q", s.Name),
		}
	}
	if len(t) == 0 {
		return Tree{}
	}
	return Tree{"text": t}
}

func slugify(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, " ", "-")
	s = strings.ReplaceAll(s, "_", "-")
	out := strings.Builder{}
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			out.WriteRune(r)
		}
	}
	return out.String()
}

func joinShort(ss []string, max int) string {
	if len(ss) > max {
		return strings.Join(ss[:max], ", ") + fmt.Sprintf(" +%d more", len(ss)-max)
	}
	return strings.Join(ss, ", ")
}
