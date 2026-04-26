// Package extractor — Glyph-specific extraction.
//
// Glyph (the INDmoney design system file) doesn't use Figma Variables and
// doesn't have a clean published-styles list for colors. But the
// "Design System 🌟" page contains a "Colours" section where every token is
// rendered as a swatch tile with a TEXT label for its name and a TEXT label
// for its hex value.
//
// This extractor reads those tiles directly: walk the Light Mode + Dark Mode
// frames inside the Colours section, collect TEXT nodes in DFS order, pair
// each (name TEXT) with its following (hex TEXT). The first occurrence of
// each name wins per mode. Light-mode and dark-mode pairs match by name to
// produce mode-paired semantic tokens.
package extractor

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strings"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/figma/client"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/figma/types"
)

var hexRe = regexp.MustCompile(`^#[0-9A-Fa-f]{6,8}$`)

// glyphIgnoreNames are header/decorative TEXTs that aren't token names.
var glyphIgnoreNames = map[string]bool{
	"Definition": true, "Light Mode": true, "Dark Mode": true,
	"Colours": true, "Color": true, "Text & Icons": true,
	"Surface": true, "Surface Market Ticker": true, "Surface Status Bar": true,
	"Surface Tab Bar": true, "Surface Toggle": true, "Surface Notebox": true,
	"Tertiary": true, "Special": true, "Borders": true, "Shadows": true,
}

// glyphCategoryHeaders are TEXTs that mark the start of a category block.
// They're not tokens themselves, but become the bucket for following tokens.
var glyphCategoryHeaders = map[string]string{
	"Text & Icons":            "text-n-icon",
	"Surface":                 "surface",
	"Surface Market Ticker":   "surface-market-ticker",
	"Surface Status Bar":      "surface-status-bar",
	"Surface Tab Bar":         "surface-tab-bar",
	"Surface Toggle":          "surface-toggle",
	"Surface Notebox":         "surface-notebox",
	"Tertiary":                "tertiary",
	"Special":                 "special",
	"Borders":                 "border",
	"Shadows":                 "shadow",
}

// GlyphColor captures one extracted (light, dark) pair from Glyph.
type GlyphColor struct {
	Name     string // "Blue", "Surface Grey BG"
	Category string // "text-n-icon", "surface", etc.
	Light    string // "#017AFE"
	Dark     string // "#3D99FF" (empty if not paired)
}

// GlyphResult is what the Glyph-specific extractor returns.
type GlyphResult struct {
	Colors      []GlyphColor
	BasePalette map[string]types.Color // every distinct hex observed
}

// RunGlyphColours fetches the Colours section from Glyph's Design System page,
// extracts (Light Mode, Dark Mode) text-pair tokens, and returns paired colors.
//
// coloursNodeID must be the SECTION node id of the Colours block on the Design
// System page (located by name in the parent script).
func RunGlyphColours(ctx context.Context, c *client.Client, fileKey, coloursNodeID string, log *slog.Logger) (*GlyphResult, error) {
	resp, err := c.GetFileNodes(ctx, fileKey, []string{coloursNodeID}, 10)
	if err != nil {
		return nil, fmt.Errorf("get colours node: %w", err)
	}
	nodes, _ := resp["nodes"].(map[string]any)
	if nodes == nil {
		return nil, fmt.Errorf("no nodes in response")
	}
	var doc map[string]any
	for _, v := range nodes {
		if m, ok := v.(map[string]any); ok && m != nil {
			doc, _ = m["document"].(map[string]any)
			break
		}
	}
	if doc == nil {
		return nil, fmt.Errorf("missing document in node payload")
	}

	// Find the two "Light Mode" frames (named identically; first has white bg = light tokens,
	// second has black bg = dark tokens).
	var lightFrame, darkFrame map[string]any
	for _, child := range arrayKey(doc, "children") {
		ch := asMap(child)
		if stringKey(ch, "type") != "FRAME" || stringKey(ch, "name") != "Light Mode" {
			continue
		}
		bg := primaryFill(ch)
		if bg.IsLightMode() && lightFrame == nil {
			lightFrame = ch
		} else if bg.IsDarkMode() && darkFrame == nil {
			darkFrame = ch
		}
	}
	if lightFrame == nil {
		return nil, fmt.Errorf("no light-mode frame in Colours section")
	}
	log.Info("found Glyph Colours frames",
		"has_light", lightFrame != nil,
		"has_dark", darkFrame != nil,
	)

	lightPairs := extractGlyphPairs(lightFrame)
	darkPairs := map[string]glyphPair{}
	if darkFrame != nil {
		dp := extractGlyphPairs(darkFrame)
		for _, p := range dp {
			darkPairs[p.name] = p
		}
	}
	log.Info("Glyph pair extraction",
		"light_pairs", len(lightPairs),
		"dark_pairs", len(darkPairs),
	)

	out := &GlyphResult{
		BasePalette: map[string]types.Color{},
	}
	for _, lp := range lightPairs {
		gc := GlyphColor{
			Name:     lp.name,
			Category: lp.category,
			Light:    lp.hex,
		}
		if dp, ok := darkPairs[lp.name]; ok {
			gc.Dark = dp.hex
		}
		out.Colors = append(out.Colors, gc)

		out.BasePalette[lp.hex] = parseHex(lp.hex)
		if gc.Dark != "" {
			out.BasePalette[gc.Dark] = parseHex(gc.Dark)
		}
	}

	// Stable sort: by category then name
	sort.SliceStable(out.Colors, func(i, j int) bool {
		if out.Colors[i].Category != out.Colors[j].Category {
			return out.Colors[i].Category < out.Colors[j].Category
		}
		return out.Colors[i].Name < out.Colors[j].Name
	})

	return out, nil
}

type glyphPair struct {
	name     string
	hex      string
	category string
}

// extractGlyphPairs walks one frame in DFS order, pairs name TEXT with next hex TEXT,
// keeps the FIRST occurrence per name, and tags each pair with its current category.
func extractGlyphPairs(frame map[string]any) []glyphPair {
	var pairs []glyphPair
	seen := map[string]bool{}
	var lastName string
	currentCategory := ""

	var walk func(node map[string]any)
	walk = func(node map[string]any) {
		if node == nil {
			return
		}
		t := stringKey(node, "type")
		name := stringKey(node, "name")

		if t == "TEXT" {
			chars := strings.TrimSpace(stringKey(node, "characters"))
			if chars == "" {
				goto recurse
			}
			// Hex match → pair with last name
			if hexRe.MatchString(chars) {
				if lastName != "" && !seen[lastName] {
					pairs = append(pairs, glyphPair{
						name:     lastName,
						hex:      strings.ToUpper(chars),
						category: currentCategory,
					})
					seen[lastName] = true
				}
				lastName = ""
				goto recurse
			}
			// Category header
			if cat, ok := glyphCategoryHeaders[chars]; ok {
				currentCategory = cat
				lastName = ""
				goto recurse
			}
			// Skip ignored names + headers
			if glyphIgnoreNames[chars] {
				goto recurse
			}
			// Otherwise, it's a token-name candidate
			if len(chars) <= 60 {
				lastName = chars
			}
		}

	recurse:
		_ = name // suppress unused warning when we don't use name
		for _, c := range arrayKey(node, "children") {
			walk(asMap(c))
		}
	}

	walk(frame)
	return pairs
}

// parseHex converts "#RRGGBB[AA]" to types.Color.
func parseHex(hex string) types.Color {
	h := strings.TrimPrefix(strings.ToUpper(hex), "#")
	if len(h) < 6 {
		return types.Color{}
	}
	parseByte := func(s string) uint8 {
		var v uint8
		for _, c := range s {
			v <<= 4
			switch {
			case c >= '0' && c <= '9':
				v |= uint8(c - '0')
			case c >= 'A' && c <= 'F':
				v |= uint8(c - 'A' + 10)
			}
		}
		return v
	}
	c := types.Color{
		R: parseByte(h[0:2]),
		G: parseByte(h[2:4]),
		B: parseByte(h[4:6]),
		A: 1.0,
	}
	if len(h) >= 8 {
		c.A = float64(parseByte(h[6:8])) / 255
	}
	return c
}
