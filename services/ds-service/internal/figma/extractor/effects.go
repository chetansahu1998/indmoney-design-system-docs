// Effects extractor — pulls EFFECT styles (drop-shadow, inner-shadow, blur)
// from a Figma file and converts them to W3C-DTCG shadow tokens.
//
// Pipeline:
//   1. GET /v1/files/:fileKey/styles → list of all published styles (filter by style_type=EFFECT).
//   2. For each EFFECT style, GET /v1/files/:fileKey/nodes?ids=<node_id> to dereference
//      the node's `effects[]` array (Figma's actual effect data lives on the source node).
//   3. Convert each effect to a DTCG shadow value (multi-shadow when a style stacks several).
//
// The output structure is intentionally simple — one bucket "shadow" with named
// entries. UI loaders flatten this the same way the color loader does.
package extractor

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/figma/client"
)

// Effect is a single Figma effect (drop-shadow / inner-shadow / blur).
type Effect struct {
	Type      string  // "DROP_SHADOW" | "INNER_SHADOW" | "LAYER_BLUR" | "BACKGROUND_BLUR"
	Color     string  // "#RRGGBBAA" — empty for blur effects
	OffsetX   float64 // px
	OffsetY   float64 // px
	Radius    float64 // px (blur radius)
	Spread    float64 // px
	Inset     bool    // true for INNER_SHADOW
}

// EffectStyle is one published EFFECT style from Figma plus its dereferenced effects.
type EffectStyle struct {
	NodeID      string
	Name        string   // "Glyph/Card/Elevation 1"
	Description string
	Effects     []Effect
}

// EffectsResult is what the effects extractor returns.
type EffectsResult struct {
	Styles []EffectStyle
}

// RunEffects fetches all EFFECT styles for a file and dereferences them.
func RunEffects(ctx context.Context, c *client.Client, fileKey string, log *slog.Logger) (*EffectsResult, error) {
	stylesResp, err := c.GetStyles(ctx, fileKey)
	if err != nil {
		return nil, fmt.Errorf("list styles: %w", err)
	}

	meta, ok := stylesResp["meta"].(map[string]any)
	if !ok {
		return &EffectsResult{}, nil
	}
	rawStyles, _ := meta["styles"].([]any)

	type pending struct {
		nodeID, name, desc string
	}
	var nodes []pending
	for _, raw := range rawStyles {
		s, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if stringKey(s, "style_type") != "EFFECT" {
			continue
		}
		nid := stringKey(s, "node_id")
		if nid == "" {
			continue
		}
		nodes = append(nodes, pending{
			nodeID: nid,
			name:   stringKey(s, "name"),
			desc:   stringKey(s, "description"),
		})
	}

	log.Info("effect styles discovered", "count", len(nodes))
	if len(nodes) == 0 {
		return &EffectsResult{}, nil
	}

	// Batch dereferences: Figma allows comma-separated ids on /nodes.
	const batchSize = 25
	out := &EffectsResult{Styles: make([]EffectStyle, 0, len(nodes))}
	for i := 0; i < len(nodes); i += batchSize {
		end := i + batchSize
		if end > len(nodes) {
			end = len(nodes)
		}
		ids := make([]string, 0, end-i)
		index := map[string]pending{}
		for _, n := range nodes[i:end] {
			ids = append(ids, n.nodeID)
			index[n.nodeID] = n
		}
		resp, err := c.GetFileNodes(ctx, fileKey, ids, 1)
		if err != nil {
			return nil, fmt.Errorf("deref effects batch: %w", err)
		}
		nodesMap, _ := resp["nodes"].(map[string]any)
		for nid, raw := range nodesMap {
			wrap, _ := raw.(map[string]any)
			doc, _ := wrap["document"].(map[string]any)
			if doc == nil {
				continue
			}
			rawEffects, _ := doc["effects"].([]any)
			effects := parseEffects(rawEffects)
			meta := index[nid]
			out.Styles = append(out.Styles, EffectStyle{
				NodeID:      nid,
				Name:        meta.name,
				Description: meta.desc,
				Effects:     effects,
			})
		}
	}
	return out, nil
}

func parseEffects(raw []any) []Effect {
	out := make([]Effect, 0, len(raw))
	for _, e := range raw {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		visible, hasVisible := m["visible"].(bool)
		if hasVisible && !visible {
			continue
		}
		typ := stringKey(m, "type")
		ef := Effect{
			Type:   typ,
			Inset:  typ == "INNER_SHADOW",
			Radius: floatField(m, "radius"),
			Spread: floatField(m, "spread"),
		}
		if off, ok := m["offset"].(map[string]any); ok {
			ef.OffsetX = floatField(off, "x")
			ef.OffsetY = floatField(off, "y")
		}
		if col, ok := m["color"].(map[string]any); ok {
			r := floatField(col, "r")
			g := floatField(col, "g")
			b := floatField(col, "b")
			a := floatField(col, "a")
			if a == 0 && col["a"] == nil {
				a = 1
			}
			ef.Color = colorHexA(r, g, b, a)
		}
		out = append(out, ef)
	}
	return out
}

func floatField(m map[string]any, k string) float64 {
	if v, ok := m[k].(float64); ok {
		return v
	}
	return 0
}

func colorHexA(r, g, b, a float64) string {
	clamp := func(x float64) int {
		v := int(x*255 + 0.5)
		if v < 0 {
			return 0
		}
		if v > 255 {
			return 255
		}
		return v
	}
	hex := fmt.Sprintf("#%02X%02X%02X", clamp(r), clamp(g), clamp(b))
	if a < 0.999 {
		hex += strings.ToUpper(fmt.Sprintf("%02x", clamp(a)))
	}
	return hex
}
