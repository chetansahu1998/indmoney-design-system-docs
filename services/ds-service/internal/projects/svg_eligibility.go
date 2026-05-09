package projects

import (
	"encoding/json"
	"strings"
)

// svg_eligibility.go — Phase 2 eligibility check for SVG cluster rendering.
//
// Pixelation post-mortem (2026-05-09): the preview-pyramid ladder caps at
// tier-2048, so any cluster whose displayPx × zoom × DPR exceeds 2048
// upscales the largest cached PNG with bilinear interpolation. For vector-
// pure clusters (icons, illustrations) we can do better: ask Figma to
// render as SVG, store the text bytes, let the browser draw the vector at
// any zoom — zero pixelation, smaller storage (typically 5-30 KB vs
// 36 KB avg for the pyramid).
//
// Eligibility blocklist — emit SVG only when ALL of these hold for the
// subtree rooted at the cluster:
//
//   1. Every node's `type` is in svgRenderableTypes (VECTOR, ELLIPSE, …)
//      OR a wrapper type that recursively passes the check.
//   2. No fills[].type == "IMAGE" anywhere — Figma SVG export inlines
//      raster fills as base64 <image>, which defeats both the storage win
//      and the zoom-infinite property.
//   3. No effects[].type is LAYER_BLUR or BACKGROUND_BLUR — SVG's
//      feGaussianBlur differs subtly from Figma's blur algorithm; mixing
//      the two produces visible "halo" diffs.
//   4. No blendMode outside {"", NORMAL, PASS_THROUGH} — SVG mix-blend-
//      mode browser support varies for LUMINOSITY / COLOR / HUE in
//      enough edge cases that we'd ship visual diffs to users.
//   5. No clipsContent: true on a node whose children are rotated past
//      the {0, 90, 180, 270} cardinals — SVG clipPath transform semantics
//      drift from Figma's around skewed clips.
//
// Each rejection records a short reason in the result so a future audit
// can grep "why didn't this cluster go SVG?" without re-walking the tree.
// Reasons are also cited in the asset_cache key (svg_skip_reason field,
// follow-up) so a Figma file shape change re-evaluates eligibility on
// next pipeline run.

// svgRenderableTypes — node types Figma renders crisply as native SVG
// elements. RECTANGLE is included only because it can carry a SOLID fill
// + stroke + cornerRadius that all map to SVG <rect>. If a RECTANGLE has
// an IMAGE fill anywhere in the subtree, rule 2 catches it before we
// reach this set.
var svgRenderableTypes = map[string]struct{}{
	"VECTOR":            {},
	"BOOLEAN_OPERATION": {},
	"ELLIPSE":           {},
	"RECTANGLE":         {},
	"LINE":              {},
	"STAR":              {},
	"POLYGON":           {},
	"REGULAR_POLYGON":   {},
	"TEXT":              {},
}

// svgWrapperTypes — wrapper / container nodes that don't directly render
// pixels but whose children must all be svgRenderableTypes (or nested
// wrappers that recursively satisfy the rule).
var svgWrapperTypes = map[string]struct{}{
	"FRAME":     {},
	"GROUP":     {},
	"INSTANCE":  {},
	"COMPONENT": {},
	"COMPONENT_SET": {},
}

// svgSafeBlendModes — blend modes that SVG handles consistently across
// browsers. Empty string is included because Figma omits the field for
// the default mode rather than emitting "NORMAL" everywhere.
var svgSafeBlendModes = map[string]struct{}{
	"":              {},
	"NORMAL":        {},
	"PASS_THROUGH":  {},
}

// SVGEligibility is the structured result of a subtree walk. Eligible
// callers read .OK; rejected callers cite .Reasons in cache keys + logs
// so a Figma shape change re-evaluates without manual cache busts.
type SVGEligibility struct {
	OK      bool
	Reasons []string
}

// IsSVGEligible walks a JSON-encoded canonical_tree (the same shape
// passed to ExtractClusterIDs) and reports whether the root subtree
// can render as a faithful SVG. nil/empty input → not eligible with a
// single reason — callers should branch into raster path.
//
// Walk is depth-first with a depth cap matching walkClusters' so a
// pathological tree can't stack-overflow. First failure short-circuits
// — callers don't need exhaustive reasons; one is enough to know the
// subtree must rasterize.
func IsSVGEligible(canonicalTreeJSON []byte) SVGEligibility {
	if len(canonicalTreeJSON) == 0 {
		return SVGEligibility{Reasons: []string{"empty_tree"}}
	}
	var root any
	if err := json.Unmarshal(canonicalTreeJSON, &root); err != nil {
		return SVGEligibility{Reasons: []string{"malformed_json"}}
	}
	// Trees come wrapped: {"document": <node>} from the Figma response
	// envelope. Unwrap if present.
	if m, ok := root.(map[string]any); ok {
		if doc, has := m["document"]; has {
			root = doc
		}
	}
	res := SVGEligibility{OK: true}
	walkSVGEligible(root, &res, 0)
	if !res.OK && len(res.Reasons) == 0 {
		// Defensive: should never happen, but if a check flips OK
		// without recording a reason the diagnostic is wrong.
		res.Reasons = []string{"unknown"}
	}
	return res
}

const svgWalkMaxDepth = 60

func walkSVGEligible(node any, res *SVGEligibility, depth int) {
	if !res.OK {
		return
	}
	if depth > svgWalkMaxDepth {
		res.OK = false
		res.Reasons = append(res.Reasons, "max_depth_exceeded")
		return
	}
	m, ok := node.(map[string]any)
	if !ok {
		return
	}
	// Skip invisible / removed nodes — they don't render and don't
	// constrain SVG eligibility either way.
	if v, has := m["visible"].(bool); has && !v {
		return
	}
	if removed, has := m["removed"].(bool); has && removed {
		return
	}

	nodeType, _ := m["type"].(string)
	_, isRenderable := svgRenderableTypes[nodeType]
	_, isWrapper := svgWrapperTypes[nodeType]
	if nodeType != "" && !isRenderable && !isWrapper {
		res.OK = false
		res.Reasons = append(res.Reasons,
			"unsupported_type:"+sanitizeReasonValue(nodeType))
		return
	}

	// Rule 2: image fills anywhere in the subtree are a hard reject.
	if hasImageFillSVG(m) {
		res.OK = false
		res.Reasons = append(res.Reasons, "image_fill")
		return
	}

	// Rule 3: blur effects (layer or background) defeat fidelity.
	if hasBlurEffectSVG(m) {
		res.OK = false
		res.Reasons = append(res.Reasons, "blur_effect")
		return
	}

	// Rule 4: unsafe blend modes.
	if blend, _ := m["blendMode"].(string); blend != "" {
		if _, safe := svgSafeBlendModes[blend]; !safe {
			res.OK = false
			res.Reasons = append(res.Reasons,
				"blend_mode:"+sanitizeReasonValue(blend))
			return
		}
	}

	// Rule 5: clipsContent + rotated children. Cardinal-only rotations
	// (0/90/180/270) are safe because SVG handles axis-aligned clips
	// faithfully; non-cardinals introduce browser-engine drift.
	if clip, _ := m["clipsContent"].(bool); clip {
		if hasNonCardinalRotation(m) {
			res.OK = false
			res.Reasons = append(res.Reasons, "clip_with_skew_rotation")
			return
		}
	}

	if children, ok := m["children"].([]any); ok {
		for _, c := range children {
			walkSVGEligible(c, res, depth+1)
			if !res.OK {
				return
			}
		}
	}
}

// hasImageFillSVG reports whether the node carries a fill of type IMAGE
// directly. Children are walked recursively by walkSVGEligible itself
// so we don't double-recurse here — this is a SHALLOW check on m only.
func hasImageFillSVG(m map[string]any) bool {
	fills, ok := m["fills"].([]any)
	if !ok {
		return false
	}
	for _, f := range fills {
		fm, ok := f.(map[string]any)
		if !ok {
			continue
		}
		if t, _ := fm["type"].(string); t == "IMAGE" {
			return true
		}
	}
	return false
}

// hasBlurEffectSVG reports whether the node carries a layer or background
// blur effect directly. Same shallow-only contract as hasImageFillSVG.
func hasBlurEffectSVG(m map[string]any) bool {
	effects, ok := m["effects"].([]any)
	if !ok {
		return false
	}
	for _, e := range effects {
		em, ok := e.(map[string]any)
		if !ok {
			continue
		}
		t, _ := em["type"].(string)
		if t == "LAYER_BLUR" || t == "BACKGROUND_BLUR" {
			// Visible flag — skip blur effects that are toggled off.
			if v, has := em["visible"].(bool); has && !v {
				continue
			}
			return true
		}
	}
	return false
}

// hasNonCardinalRotation reports whether ANY child of m carries a
// rotation that isn't a multiple of 90°. Reads the relativeTransform
// matrix (Figma's per-node 2x3) and computes the rotation from
// atan2(m[1][0], m[0][0]) — but to keep this dependency-free we
// just check if either off-diagonal is present (a clean cardinal
// rotation has m[0][1] = ±1 OR m[1][0] = ±1 when rotated, but
// also has off-diagonals zeroed at 0/180). Rather than a full
// matrix decomp we use a conservative heuristic: any non-zero,
// non-±1 off-diagonal flags the subtree as skewed.
func hasNonCardinalRotation(m map[string]any) bool {
	children, ok := m["children"].([]any)
	if !ok {
		return false
	}
	for _, c := range children {
		cm, ok := c.(map[string]any)
		if !ok {
			continue
		}
		rt, ok := cm["relativeTransform"].([]any)
		if !ok || len(rt) < 2 {
			continue
		}
		row0, ok0 := rt[0].([]any)
		row1, ok1 := rt[1].([]any)
		if !ok0 || !ok1 || len(row0) < 2 || len(row1) < 2 {
			continue
		}
		off01, _ := row0[1].(float64)
		off10, _ := row1[0].(float64)
		// Cardinals: off-diagonals are exactly 0 (0°/180°) or ±1 (90°/270°).
		// A small epsilon catches floating-point drift in Figma's exporter.
		if !isCardinalOffDiag(off01) || !isCardinalOffDiag(off10) {
			return true
		}
	}
	return false
}

func isCardinalOffDiag(v float64) bool {
	const eps = 1e-6
	if v > -eps && v < eps {
		return true
	}
	if v > 1-eps && v < 1+eps {
		return true
	}
	if v > -1-eps && v < -1+eps {
		return true
	}
	return false
}

// sanitizeReasonValue strips characters that would break the cache-key
// or grep-friendly log formatting. Reasons land in asset_cache + slog
// fields; constraining them to identifier-shape characters prevents
// downstream parser drift.
func sanitizeReasonValue(v string) string {
	if v == "" {
		return ""
	}
	const max = 32
	var b strings.Builder
	for i, r := range v {
		if i >= max {
			break
		}
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}
