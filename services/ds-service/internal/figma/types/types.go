// Package types defines the shared types used across the Figma extraction pipeline.
package types

import (
	"fmt"
	"math"
)

// Color is a 24-bit RGB color with alpha. Values are 0-255 for r/g/b.
// Alpha is 0.0–1.0; <1.0 means semi-transparent.
type Color struct {
	R, G, B uint8
	A       float64 // 0.0–1.0
}

// FromFigma converts Figma's normalized {r,g,b,a} (each 0.0–1.0) into a Color.
func FromFigma(r, g, b float64, opacity *float64) Color {
	a := 1.0
	if opacity != nil {
		a = *opacity
	}
	return Color{
		R: uint8(math.Round(clamp01(r) * 255)),
		G: uint8(math.Round(clamp01(g) * 255)),
		B: uint8(math.Round(clamp01(b) * 255)),
		A: clamp01(a),
	}
}

// Hex returns "#RRGGBB" (uppercase). Alpha is ignored for hex form.
func (c Color) Hex() string {
	return fmt.Sprintf("#%02X%02X%02X", c.R, c.G, c.B)
}

// HexWithAlpha returns "#RRGGBBAA" if alpha < 1.0, else "#RRGGBB".
func (c Color) HexWithAlpha() string {
	if c.A >= 1.0 {
		return c.Hex()
	}
	a := uint8(math.Round(c.A * 255))
	return fmt.Sprintf("#%02X%02X%02X%02X", c.R, c.G, c.B, a)
}

// Lightness returns perceptual lightness in [0,1] using Rec. 709 luma.
// Used to classify frame backgrounds as light/dark mode.
func (c Color) Lightness() float64 {
	r := float64(c.R) / 255
	g := float64(c.G) / 255
	b := float64(c.B) / 255
	return 0.2126*r + 0.7152*g + 0.0722*b
}

func (c Color) String() string { return c.HexWithAlpha() }

// IsLightMode reports whether the color is light enough to be a light-mode background.
// Threshold tuned at 0.7 — actual measurements: light frames hit 0.95+, dark frames hit 0.05.
func (c Color) IsLightMode() bool { return c.Lightness() > 0.7 && c.A >= 0.99 }

// IsDarkMode reports whether the color is dark enough to be a dark-mode background.
func (c Color) IsDarkMode() bool { return c.Lightness() < 0.3 && c.A >= 0.99 }

// ModePair holds the same semantic token resolved across light and dark modes.
type ModePair struct {
	Light Color
	Dark  Color
	// HasLight/HasDark allow expressing "only seen in one mode."
	HasLight bool
	HasDark  bool
	// Confidence: number of (lightFrame, darkFrame) pairs where the same logical
	// element resolved to (Light, Dark). Higher = more reliable.
	Confidence int
	// Names: every Figma node name observed at this color pair (deduplicated).
	Names []string
}

// PairKey uniquely identifies a color pair for clustering.
// Example: "#FFFFFF↔#171A1E" — used to merge nodes that share the same role.
func (m ModePair) Key() string {
	if !m.HasLight {
		return "↔" + m.Dark.Hex()
	}
	if !m.HasDark {
		return m.Light.Hex() + "↔"
	}
	return m.Light.Hex() + "↔" + m.Dark.Hex()
}

// Frame is a Figma frame node we care about (typically mobile screen-sized).
type Frame struct {
	ID     string
	Name   string
	Bg     Color
	Width  int
	Height int
	X      int
	Y      int
	Doc    map[string]any // raw Figma node payload
	Page   string         // name of the page this frame is on
	Parent string         // immediate parent name (often a SECTION)
}

// IsMobileSize reports whether this frame's dimensions match a phone screen.
// We accept common iOS sizes (375×812, 393×852, 390×844) and Android (412×892).
func (f Frame) IsMobileSize() bool {
	if f.Height < 600 {
		return false
	}
	switch f.Width {
	case 375, 390, 393, 412:
		return true
	}
	// Be lenient — anything 360-430 wide and 600-1000 tall counts as a phone.
	return f.Width >= 360 && f.Width <= 430 && f.Height >= 600 && f.Height <= 1100
}

// FramePair is two frames believed to render the same screen in light vs dark mode.
type FramePair struct {
	Light       Frame
	Dark        Frame
	PairScore   int    // higher = more confident this is a real pair
	PairReason  string // human-readable why we paired them
}

func clamp01(x float64) float64 {
	if x < 0 {
		return 0
	}
	if x > 1 {
		return 1
	}
	return x
}
