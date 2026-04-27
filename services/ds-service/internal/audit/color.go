package audit

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// sRGB → OKLab → OKLCH → distance.
//
// Reference: https://bottosson.github.io/posts/oklab/ — minimal port. We use
// OKLCH distance (perceptually uniform) because RGB / Lab Euclidean distances
// disagree with what designers consider "close" — purple→blue can be 30 in RGB
// but feel adjacent. OKLCH is roughly: same chroma + lightness + slightly
// different hue → small distance. Default drift threshold is 0.03; this is
// tunable per token type via the audit manifest.

// HexToRGB parses "#RRGGBB" or "#RRGGBBAA" → linear sRGB triple in [0,1].
// Alpha is dropped (we audit colors, not opacities).
func HexToRGB(hex string) (r, g, b float64, ok bool) {
	s := strings.TrimPrefix(strings.TrimSpace(hex), "#")
	if len(s) != 6 && len(s) != 8 {
		return 0, 0, 0, false
	}
	parse := func(off int) (float64, bool) {
		v, err := strconv.ParseInt(s[off:off+2], 16, 32)
		if err != nil {
			return 0, false
		}
		return float64(v) / 255.0, true
	}
	var ok1, ok2, ok3 bool
	r, ok1 = parse(0)
	g, ok2 = parse(2)
	b, ok3 = parse(4)
	if !ok1 || !ok2 || !ok3 {
		return 0, 0, 0, false
	}
	return r, g, b, true
}

// RGBToHex formats a triple in [0,1] as "#RRGGBB". Used for canonicalizing
// observed values in fix recommendations.
func RGBToHex(r, g, b float64) string {
	clamp := func(x float64) int {
		v := int(math.Round(x * 255))
		if v < 0 {
			return 0
		}
		if v > 255 {
			return 255
		}
		return v
	}
	return fmt.Sprintf("#%02X%02X%02X", clamp(r), clamp(g), clamp(b))
}

// linearize undoes the sRGB gamma. Required before OKLab transform.
func linearize(c float64) float64 {
	if c <= 0.04045 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}

// rgbToOKLab converts sRGB → OKLab. Input in [0,1].
func rgbToOKLab(r, g, b float64) (L, a, bLab float64) {
	rl := linearize(r)
	gl := linearize(g)
	bl := linearize(b)

	l := 0.4122214708*rl + 0.5363325363*gl + 0.0514459929*bl
	m := 0.2119034982*rl + 0.6806995451*gl + 0.1073969566*bl
	s := 0.0883024619*rl + 0.2817188376*gl + 0.6299787005*bl

	l = math.Cbrt(l)
	m = math.Cbrt(m)
	s = math.Cbrt(s)

	L = 0.2104542553*l + 0.7936177850*m - 0.0040720468*s
	a = 1.9779984951*l - 2.4285922050*m + 0.4505937099*s
	bLab = 0.0259040371*l + 0.7827717662*m - 0.8086757660*s
	return
}

// OKLCHDistance returns the perceptual distance between two hex colors.
// Returns ∞ for invalid inputs so callers can detect parse failures.
func OKLCHDistance(hexA, hexB string) float64 {
	rA, gA, bA, okA := HexToRGB(hexA)
	rB, gB, bB, okB := HexToRGB(hexB)
	if !okA || !okB {
		return math.Inf(1)
	}
	la, aa, baa := rgbToOKLab(rA, gA, bA)
	lb, ab, bbb := rgbToOKLab(rB, gB, bB)
	dl := la - lb
	da := aa - ab
	db := baa - bbb
	return math.Sqrt(dl*dl + da*da + db*db)
}

// PxDistance returns the absolute px difference, used for spacing/radius drift.
func PxDistance(a, b float64) float64 {
	d := a - b
	if d < 0 {
		return -d
	}
	return d
}

// DefaultColorDriftThreshold is the OKLCH distance at or below which a
// color is considered "close enough" to a token to recommend binding to it.
// 0.03 is slightly more permissive than the typical 0.02 used in color
// tooling; tunable per-brand in the audit manifest.
const DefaultColorDriftThreshold = 0.03

// DefaultPxDriftThreshold is the absolute px tolerance for spacing/radius.
// 1.0 means values within 1 pixel of a token are considered matching.
const DefaultPxDriftThreshold = 1.0
