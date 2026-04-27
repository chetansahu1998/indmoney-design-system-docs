// Border-radius classification. Unlike spacing, radius is property-derived:
// pill-shaped components use radius = height/2; everything else lives on a
// short list of multiples of 2 or 4 ({2, 4, 6, 8, 12, 16}).
//
// Designer rule (verbatim):
//
//	"Border radius is whatever the height of the component, like button half
//	 its height, all the other values are invalid for max. And here also we
//	 will stick to using in multiple of 2 or 4 max."
//
// 23, 19.5, 7, 12.5 etc. are invalid; should snap to the nearest multiple
// of 2 or 4 — or, when a host height is known and r ≥ height/2 - 1px, the
// rule is "Pill (height/2)" rather than a px token.
package audit

import "math"

// AllowedRadiusValues is the small explicit list that may be tokenised.
// Anything outside is drift.
var AllowedRadiusValues = []float64{0, 2, 4, 6, 8, 12, 16}

// RadiusKind tells the caller whether the observed radius is on the short
// allowed list, expresses a pill rule, or off-grid drift.
type RadiusKind string

const (
	// RadiusOnGrid: r ∈ AllowedRadiusValues exactly.
	RadiusOnGrid RadiusKind = "on_grid"
	// RadiusPill: r ≈ height/2 (within 1px) on a node tall enough to read
	// as pill-shaped (height ≥ 16). Emit `radius.pill` rule, not a px token.
	RadiusPill RadiusKind = "pill"
	// RadiusOffGrid: r isn't on the allowed list and the host height isn't
	// large enough or ratio doesn't match pill rule. Snap to the nearest
	// allowed value; emit drift fix.
	RadiusOffGrid RadiusKind = "off_grid"
)

// RadiusClassification is the outcome of evaluating one cornerRadius.
type RadiusClassification struct {
	Observed float64
	Height   float64 // 0 when caller can't supply host height
	Kind     RadiusKind
	Snapped  float64 // nearest allowed value when Kind != Pill (0 when Pill)
	Distance float64 // |Observed - Snapped|, in px (0 for OnGrid + Pill)
	// Suggestion describes the fix in human-readable form, e.g.:
	//   "Use radius 8 (multiple of 4)"
	//   "Use radius rule 'Pill' (height/2)"
	//   "Sits between 4 and 8; round up to 8"
	Suggestion string
}

// ClassifyRadius applies the designer's rule. Pass height=0 if the host
// node's height isn't available (rare but possible — top-level frames lack
// the absoluteBoundingBox in some response shapes).
func ClassifyRadius(observed, height float64) RadiusClassification {
	out := RadiusClassification{Observed: observed, Height: height}
	if observed < 0 {
		observed = 0
	}
	// Pill detection: requires a known height, a tall-enough node, and
	// r within 1px of height/2. The 1px slack covers Figma rounding when
	// designers type "20" radius on a 41-tall component.
	if height >= 16 && observed >= height/2-1 {
		out.Kind = RadiusPill
		out.Snapped = 0
		out.Suggestion = "Use radius rule 'Pill' (height/2)"
		return out
	}
	// On-grid check: hits an allowed value exactly.
	for _, v := range AllowedRadiusValues {
		if math.Abs(v-observed) < 0.001 {
			out.Kind = RadiusOnGrid
			out.Snapped = v
			out.Suggestion = "On grid"
			return out
		}
	}
	// Off-grid: snap to nearest allowed value (ties round up).
	bestDist := math.Inf(1)
	var best float64
	for _, v := range AllowedRadiusValues {
		d := math.Abs(v - observed)
		if d < bestDist {
			bestDist = d
			best = v
		}
	}
	candidates := []float64{best}
	for _, v := range AllowedRadiusValues {
		if v == best {
			continue
		}
		if math.Abs(math.Abs(v-observed)-bestDist) < 0.001 {
			candidates = append(candidates, v)
		}
	}
	snapped := best
	if len(candidates) > 1 {
		// Round up.
		for _, v := range candidates {
			if v > snapped {
				snapped = v
			}
		}
	}
	out.Kind = RadiusOffGrid
	out.Snapped = snapped
	out.Distance = math.Abs(snapped - observed)
	if len(candidates) > 1 {
		out.Suggestion = "Sits between allowed values; round up to " + radiusKey(snapped)
	} else {
		out.Suggestion = "Use radius " + radiusKey(snapped) + " (multiple of 2 or 4)"
	}
	return out
}

func radiusKey(v float64) string {
	// Local helper — radius values are always integers in the allowed list.
	return fmtFloat(v)
}

func fmtFloat(v float64) string {
	if v == math.Trunc(v) {
		return intToStr(int64(v))
	}
	// Allowed list never produces a fractional radius, but keep the
	// fallback for completeness.
	return floatToStr(v)
}

func intToStr(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if neg {
		digits = append([]byte{'-'}, digits...)
	}
	return string(digits)
}

func floatToStr(v float64) string {
	// Cheap, dependency-free formatter for the rare fractional case.
	whole := int64(v)
	frac := v - float64(whole)
	if frac < 0 {
		frac = -frac
	}
	out := intToStr(whole) + "."
	for i := 0; i < 2 && frac > 0; i++ {
		frac *= 10
		d := int64(frac)
		out += intToStr(d)
		frac -= float64(d)
	}
	return out
}
