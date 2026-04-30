package rules

import (
	"math"
	"testing"
)

// approxEqual returns true when two floats are within tol of each other. The
// W3C contrast-tool publishes ratios rounded to 2 decimals so we compare with
// a 0.01 tolerance; that's well below any single-bit rounding wobble in the
// linearize() / pow() chain.
func approxEqual(a, b, tol float64) bool {
	return math.Abs(a-b) <= tol
}

// TestRelativeLuminance covers both ends of the sRGB space.
func TestRelativeLuminance_BlackAndWhite(t *testing.T) {
	if l := RelativeLuminance(0, 0, 0); l != 0 {
		t.Fatalf("black luminance: want 0, got %v", l)
	}
	// White's luminance is exactly 1 by spec — every channel hits the high-
	// branch and ((1+0.055)/1.055)^2.4 = 1.
	if l := RelativeLuminance(1, 1, 1); !approxEqual(l, 1.0, 1e-9) {
		t.Fatalf("white luminance: want 1.0, got %v", l)
	}
}

// TestContrastRatio_WhiteOnBlack — the textbook 21:1 reference.
func TestContrastRatio_WhiteOnBlack(t *testing.T) {
	lb := RelativeLuminance(0, 0, 0)
	lw := RelativeLuminance(1, 1, 1)
	got := ContrastRatio(lw, lb)
	if !approxEqual(got, 21.0, 0.01) {
		t.Fatalf("white-on-black: want 21.00, got %.4f", got)
	}
}

// TestContrastRatio_BlackOnWhite — order shouldn't matter; helper picks the
// lighter as the numerator.
func TestContrastRatio_BlackOnWhite(t *testing.T) {
	lb := RelativeLuminance(0, 0, 0)
	lw := RelativeLuminance(1, 1, 1)
	got := ContrastRatio(lb, lw)
	if !approxEqual(got, 21.0, 0.01) {
		t.Fatalf("black-on-white: want 21.00, got %.4f", got)
	}
}

// TestContrastRatio_W3CReference767676 — #767676 on white = 4.54:1, the
// canonical W3C "just passes AA normal text" reference pair.
func TestContrastRatio_W3CReference767676(t *testing.T) {
	r, g, b, err := HexToRGB("#767676")
	if err != nil {
		t.Fatalf("HexToRGB: %v", err)
	}
	lFG := RelativeLuminance(r, g, b)
	lBG := RelativeLuminance(1, 1, 1)
	got := ContrastRatio(lFG, lBG)
	if !approxEqual(got, 4.54, 0.02) {
		t.Fatalf("#767676 on white: want 4.54, got %.4f", got)
	}
}

// TestContrastRatio_W3CReferenceA0A0A0 — #A0A0A0 on white per the WCAG 2.1
// formula. The plan quoted 2.84:1, but that was a transcription error: the
// spec-correct value with the 0.03928 inverse-companding split is ~2.62:1.
// We assert the spec-correct value; the U4 a11y_contrast error-path tests
// still rely on this being below the 3.0 large-text threshold (verified by
// the explicit guard below).
func TestContrastRatio_W3CReferenceA0A0A0(t *testing.T) {
	r, g, b, err := HexToRGB("#A0A0A0")
	if err != nil {
		t.Fatalf("HexToRGB: %v", err)
	}
	lFG := RelativeLuminance(r, g, b)
	lBG := RelativeLuminance(1, 1, 1)
	got := ContrastRatio(lFG, lBG)
	if !approxEqual(got, 2.62, 0.02) {
		t.Fatalf("#A0A0A0 on white: want ~2.62, got %.4f", got)
	}
	// Sanity: still well below the 3.0 large-text threshold so error-path
	// fixtures using this color produce violations regardless of font size.
	if got >= 3.0 {
		t.Fatalf("#A0A0A0 on white must stay below 3.0 large-text threshold; got %.4f", got)
	}
}

// TestHexToRGB_RoundTrip exercises both the 6-digit and 8-digit forms and
// verifies RGBToHex is the inverse.
func TestHexToRGB_RoundTrip(t *testing.T) {
	cases := []string{"#000000", "#FFFFFF", "#767676", "#A0A0A0", "#1A2B3C", "#FFAB00"}
	for _, in := range cases {
		r, g, b, err := HexToRGB(in)
		if err != nil {
			t.Fatalf("HexToRGB(%q): %v", in, err)
		}
		out := RGBToHex(r, g, b)
		if out != in {
			t.Fatalf("round-trip %q -> %q", in, out)
		}
	}
}

// TestHexToRGB_AlphaSuffix verifies the #RRGGBBAA form parses (alpha is
// dropped — RGB matches the 6-digit form).
func TestHexToRGB_AlphaSuffix(t *testing.T) {
	r, g, b, err := HexToRGB("#767676FF")
	if err != nil {
		t.Fatalf("HexToRGB with alpha: %v", err)
	}
	out := RGBToHex(r, g, b)
	if out != "#767676" {
		t.Fatalf("alpha-suffixed round-trip: want #767676, got %q", out)
	}
}

// TestHexToRGB_Errors exercises the malformed-input paths.
func TestHexToRGB_Errors(t *testing.T) {
	bad := []string{"", "#", "#FFF", "#XYZXYZ", "not-a-color"}
	for _, in := range bad {
		if _, _, _, err := HexToRGB(in); err == nil {
			t.Fatalf("HexToRGB(%q): expected error, got nil", in)
		}
	}
}

// TestRGBToHex_Clamping checks that out-of-gamut floats clamp instead of
// wrapping or producing nonsense bytes.
func TestRGBToHex_Clamping(t *testing.T) {
	if got := RGBToHex(1.5, -0.2, 0.5); got != "#FF0080" {
		t.Fatalf("clamping: want #FF0080, got %q", got)
	}
}
