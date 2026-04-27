package audit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
)

// Ported (and simplified) from ~/DesignBrain-AI/internal/canonical/canonical_hash.go.
//
// CanonicalHash produces a content hash over the structurally-meaningful
// fields of a Figma node, stripping volatile fields (id, absoluteBoundingBox,
// effects, names) so that two genuinely-identical nodes — pasted from a
// component library at different times into different files — collide
// deterministically. Used for cross-file pattern detection.
//
// Granularity: type, normalized dimensions (rounded to nearest px),
// fillStyleId, textStyleId, autoLayout fingerprint, sorted child-type
// sequence. If false-positive rate proves > 5% on real data, narrow by
// re-adding stripped fields.

// CanonicalHash computes the hash of a Figma node tree. node is the JSON
// shape Figma returns from /v1/files/:key/nodes — a map[string]any with
// "type", "fills", "absoluteBoundingBox", "children", etc.
func CanonicalHash(node map[string]any) string {
	if node == nil {
		return ""
	}
	canonical := canonicalize(node)
	bytes, _ := json.Marshal(canonical)
	sum := sha256.Sum256(bytes)
	return "sha256:" + hex.EncodeToString(sum[:8]) // 8 bytes = 16 hex chars; readable + low collision for our scale
}

// canonicalize projects a node to its hashable shape.
func canonicalize(node map[string]any) map[string]any {
	out := map[string]any{}

	if t, ok := node["type"].(string); ok {
		out["type"] = t
	}

	// Normalized dimensions — round to nearest px, omit position.
	if bbox, ok := node["absoluteBoundingBox"].(map[string]any); ok {
		w, _ := bbox["width"].(float64)
		h, _ := bbox["height"].(float64)
		out["w"] = int(math.Round(w))
		out["h"] = int(math.Round(h))
	}

	// Style references — these IS the connection back to the DS.
	if v, ok := node["styles"].(map[string]any); ok {
		// Sort keys for deterministic JSON marshal.
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		styles := map[string]any{}
		for _, k := range keys {
			styles[k] = v[k]
		}
		out["styles"] = styles
	}

	// AutoLayout fingerprint — capture layout intent without being noisy.
	if mode, ok := node["layoutMode"].(string); ok && mode != "NONE" {
		out["layout"] = map[string]any{
			"mode":         mode,
			"itemSpacing":  intRound(node, "itemSpacing"),
			"paddingLeft":  intRound(node, "paddingLeft"),
			"paddingRight": intRound(node, "paddingRight"),
			"paddingTop":   intRound(node, "paddingTop"),
			"paddingBot":   intRound(node, "paddingBottom"),
			"counterAxis":  stringField(node, "counterAxisSizingMode"),
			"primaryAxis":  stringField(node, "primaryAxisSizingMode"),
		}
	}

	// Sorted child-type sequence: gives shape signal without recursing into
	// child content. Two cards with the same child types in the same order
	// hash similarly even if labels differ.
	if children, ok := node["children"].([]any); ok && len(children) > 0 {
		types := make([]string, 0, len(children))
		for _, ch := range children {
			cm, _ := ch.(map[string]any)
			if cm == nil {
				continue
			}
			t, _ := cm["type"].(string)
			types = append(types, t)
		}
		out["child_types"] = types
	}

	// Component reference — when set, matters for the hash because two nodes
	// referencing the same component are functionally identical.
	if k, ok := node["componentId"].(string); ok && k != "" {
		out["componentId"] = k
	}
	if k, ok := node["componentSetId"].(string); ok && k != "" {
		out["componentSetId"] = k
	}

	return out
}

func intRound(m map[string]any, key string) int {
	v, _ := m[key].(float64)
	return int(math.Round(v))
}

func stringField(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

// SuggestedNameFromHash returns a short stable label for a cross-file pattern
// when no human name is available. Uses the type + dims + first 6 hex chars.
func SuggestedNameFromHash(node map[string]any, hash string) string {
	t := stringField(node, "type")
	w := intRound(stringMap(node, "absoluteBoundingBox"), "width")
	h := intRound(stringMap(node, "absoluteBoundingBox"), "height")
	if t == "" || w == 0 {
		return hash
	}
	short := hash
	if len(short) > 16 {
		short = short[7:13]
	}
	return fmt.Sprintf("%s-%dx%d-%s", t, w, h, short)
}

func stringMap(m map[string]any, key string) map[string]any {
	v, _ := m[key].(map[string]any)
	return v
}
