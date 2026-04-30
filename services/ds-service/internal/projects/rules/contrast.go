// Package rules implements Phase 2 RuleRunner classes for the projects audit
// engine. Each runner satisfies the projects.RuleRunner interface defined at
// services/ds-service/internal/projects/runner.go and emits a slice of
// projects.Violation rows.
//
// This file holds the WCAG 2.1 contrast utilities consumed by
// a11y_contrast.go. They are intentionally pure (no DB, no global state) so
// they can be exercised against the W3C-published reference pairs without any
// fixture wiring.
package rules

import (
	"fmt"
	"math"
	"strings"
)

// RelativeLuminance computes WCAG 2.1 relative luminance for an sRGB color.
// channels are 0..1 floats. Returns 0..1.
//
// Per https://www.w3.org/TR/WCAG21/#dfn-relative-luminance:
//
//	if c <= 0.03928, c_lin = c / 12.92
//	else            c_lin = pow((c + 0.055) / 1.055, 2.4)
//	L = 0.2126*R_lin + 0.7152*G_lin + 0.0722*B_lin
func RelativeLuminance(r, g, b float64) float64 {
	return 0.2126*linearize(r) + 0.7152*linearize(g) + 0.0722*linearize(b)
}

// ContrastRatio returns the WCAG 2.1 contrast ratio between two relative-
// luminance values. Always returns >= 1.0. Inputs are 0..1.
//
// Per spec: ratio = (L_lighter + 0.05) / (L_darker + 0.05).
func ContrastRatio(la, lb float64) float64 {
	hi, lo := la, lb
	if lo > hi {
		hi, lo = lo, hi
	}
	return (hi + 0.05) / (lo + 0.05)
}

// HexToRGB parses #RRGGBB or #RRGGBBAA. Returns r, g, b in 0..1. The alpha
// channel is parsed-but-discarded so callers that want it must reach for the
// raw bytes themselves; for U4 we only need the 3 RGB components for
// luminance computation.
func HexToRGB(hex string) (r, g, b float64, err error) {
	s := strings.TrimSpace(hex)
	s = strings.TrimPrefix(s, "#")
	if len(s) != 6 && len(s) != 8 {
		return 0, 0, 0, fmt.Errorf("HexToRGB: expected #RRGGBB or #RRGGBBAA, got %q", hex)
	}
	var ri, gi, bi int
	if _, scanErr := fmt.Sscanf(s[0:6], "%02x%02x%02x", &ri, &gi, &bi); scanErr != nil {
		return 0, 0, 0, fmt.Errorf("HexToRGB: parse %q: %w", hex, scanErr)
	}
	return float64(ri) / 255.0, float64(gi) / 255.0, float64(bi) / 255.0, nil
}

// RGBToHex formats RGB (0..1) as "#RRGGBB" upper-case hex. Out-of-range
// channels are clamped to [0,1] before formatting so violation messages never
// contain malformed colors when the canonical tree carries unusual data.
func RGBToHex(r, g, b float64) string {
	return fmt.Sprintf("#%02X%02X%02X", clampByte(r), clampByte(g), clampByte(b))
}

// linearize applies the sRGB inverse companding curve from WCAG 2.1.
func linearize(c float64) float64 {
	if c <= 0.03928 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}

// clampByte rescales a 0..1 float to 0..255 with clamping at both ends.
func clampByte(c float64) int {
	v := int(math.Round(c * 255.0))
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return v
}
