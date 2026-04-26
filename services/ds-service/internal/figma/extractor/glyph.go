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

// category attribute for textNode (so categorized inherits from text bucket)

// textNode is a flat record of one TEXT element's content + bbox.
type textNode struct {
	text     string
	x, y     float64
	width    float64
	parentID string // immediate-parent's id (for tile-grouping)
	categoryID string // closest non-generic ancestor name
}

// extractGlyphPairs walks a frame and pairs (name TEXT, hex TEXT) within the
// SAME parent group (the swatch tile). Tiles in Figma are typically:
//
//   GROUP "Token"            ← parent we group by
//     RECTANGLE swatch fill
//     TEXT "Blue"            ← name
//     TEXT "#017AFE"         ← hex
//
// Pass 1: group texts by parentID. Tiles with exactly one hex text + at least
// one name text get paired.
// Pass 2 (fallback): unmatched hex texts get paired by Euclidean distance to
// the nearest unmatched name text.
//
// Category is taken from the closest non-generic ancestor name OR from the
// most recent category-header TEXT seen in document order.
func extractGlyphPairs(frame map[string]any) []glyphPair {
	allTexts := collectTexts(frame, "", "")

	// Track running category from header TEXTs (top-down y order).
	type categorized struct {
		t        textNode
		category string
		isHex    bool
	}
	sortedTexts := make([]textNode, len(allTexts))
	copy(sortedTexts, allTexts)
	// Sort top-down for category pass
	sortByY := func(a, b textNode) bool {
		if absF(a.y-b.y) < 4 {
			return a.x < b.x
		}
		return a.y < b.y
	}
	for i := 1; i < len(sortedTexts); i++ {
		for j := i; j > 0 && sortByY(sortedTexts[j], sortedTexts[j-1]); j-- {
			sortedTexts[j-1], sortedTexts[j] = sortedTexts[j], sortedTexts[j-1]
		}
	}

	current := ""
	categorized_list := make([]categorized, 0, len(sortedTexts))
	for _, t := range sortedTexts {
		if cat, ok := glyphCategoryHeaders[t.text]; ok {
			current = cat
			continue
		}
		isHex := hexRe.MatchString(t.text)
		if !isHex && glyphIgnoreNames[t.text] {
			continue
		}
		categorized_list = append(categorized_list, categorized{t, current, isHex})
	}

	// Pass 1: group by parentID (the immediate Figma parent). For tiles with
	// 1 hex + 1+ names → pair them.
	byParent := map[string][]categorized{}
	for _, c := range categorized_list {
		byParent[c.t.parentID] = append(byParent[c.t.parentID], c)
	}

	pairs := make([]glyphPair, 0)
	seen := map[string]bool{}
	usedHex := map[string]bool{} // key: "x,y,text"
	usedName := map[string]bool{}

	keyOf := func(t textNode) string {
		return fmt.Sprintf("%.1f,%.1f,%s", t.x, t.y, t.text)
	}

	for _, group := range byParent {
		var hexes []categorized
		var names []categorized
		for _, g := range group {
			if g.isHex {
				hexes = append(hexes, g)
			} else if len(g.t.text) <= 60 {
				names = append(names, g)
			}
		}
		if len(hexes) == 0 || len(names) == 0 {
			continue
		}
		// One hex with one+ name candidates → pick the FIRST name (closest in y to the hex).
		for _, hx := range hexes {
			bestI := -1
			bestDist := 1e18
			for i, nm := range names {
				if usedName[keyOf(nm.t)] {
					continue
				}
				dy := absF(nm.t.y - hx.t.y)
				dx := absF(nm.t.x - hx.t.x)
				dist := dy*dy + dx*dx*0.25
				if dist < bestDist {
					bestDist = dist
					bestI = i
				}
			}
			if bestI < 0 {
				continue
			}
			n := names[bestI]
			if !seen[n.t.text] {
				cat := hx.category
				if cat == "" {
					cat = n.category
				}
				pairs = append(pairs, glyphPair{
					name:     n.t.text,
					hex:      strings.ToUpper(hx.t.text),
					category: cat,
				})
				seen[n.t.text] = true
			}
			usedHex[keyOf(hx.t)] = true
			usedName[keyOf(n.t)] = true
		}
	}

	// Pass 2: fallback for hexes whose parent didn't have a name TEXT.
	for _, hx := range categorized_list {
		if !hx.isHex || usedHex[keyOf(hx.t)] {
			continue
		}
		bestI := -1
		bestDist := 1e18
		for i, nm := range categorized_list {
			if nm.isHex || usedName[keyOf(nm.t)] || len(nm.t.text) > 60 {
				continue
			}
			dy := absF(nm.t.y - hx.t.y)
			dx := absF(nm.t.x - hx.t.x)
			dist := dx*dx*4 + dy*dy
			if absF(dy) < 30 && nm.t.x < hx.t.x {
				dist *= 0.25
			}
			if dist < bestDist {
				bestDist = dist
				bestI = i
			}
		}
		if bestI >= 0 {
			n := categorized_list[bestI]
			if !seen[n.t.text] {
				pairs = append(pairs, glyphPair{
					name:     n.t.text,
					hex:      strings.ToUpper(hx.t.text),
					category: hx.category,
				})
				seen[n.t.text] = true
			}
			usedHex[keyOf(hx.t)] = true
			usedName[keyOf(n.t)] = true
		}
	}

	return pairs
}

// collectTexts flattens all TEXT nodes in a frame in DFS order with their
// bboxes + IMMEDIATE parent id (for tile grouping) + closest non-generic
// ancestor name (for category attribution).
func collectTexts(node map[string]any, parentID, categoryID string) []textNode {
	var out []textNode
	var walk func(n map[string]any, pID, catID string)
	walk = func(n map[string]any, pID, catID string) {
		if n == nil {
			return
		}
		t := stringKey(n, "type")
		nm := stringKey(n, "name")
		nodeID := stringKey(n, "id")
		if t == "TEXT" {
			chars := strings.TrimSpace(stringKey(n, "characters"))
			if chars != "" {
				bbox := mapKey(n, "absoluteBoundingBox")
				out = append(out, textNode{
					text:       chars,
					x:          floatKey(bbox, "x"),
					y:          floatKey(bbox, "y"),
					width:      floatKey(bbox, "width"),
					parentID:   pID,
					categoryID: catID,
				})
			}
		}
		// Pass node id as next parent
		nextParent := nodeID
		nextCat := catID
		if nm != "" && !strings.HasPrefix(nm, "Frame ") && !strings.HasPrefix(nm, "Rectangle ") &&
			!strings.HasPrefix(nm, "Group ") && !strings.HasPrefix(nm, "Vector") &&
			!strings.HasPrefix(nm, "Ellipse ") && !strings.HasPrefix(nm, "Path") {
			nextCat = nm
		}
		for _, c := range arrayKey(n, "children") {
			walk(asMap(c), nextParent, nextCat)
		}
	}
	walk(node, parentID, categoryID)
	return out
}

func absF(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
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
