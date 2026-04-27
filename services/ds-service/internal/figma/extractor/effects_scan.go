// Effects scanner — walks a Figma node tree and collects all `effects[]`
// arrays, grouped by an inferred token name (parent frame/component name).
//
// This is the fallback path when a file has no published EFFECT styles but
// still uses shadows on its components. It's how Glyph defines elevation:
// shadows live inline on Card / Button / Sheet frames rather than as styles.
//
// Pipeline:
//   1. Fetch the target page via /v1/files/:fileKey/nodes?ids=<node>&depth=full.
//   2. DFS the document tree.
//   3. For every node with non-empty effects[], walk up the ancestor chain to
//      find the nearest "named" parent (FRAME / COMPONENT / COMPONENT_SET).
//   4. Group effects by ancestor name. Deduplicate identical effect lists.
//
// The grouping name becomes the shadow token slug, e.g. "card-elevation-1".
package extractor

import (
	"context"
	"fmt"
	"log/slog"
	"sort"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/figma/client"
)

// ScanEffectsResult is the output of the node-tree scanner.
type ScanEffectsResult struct {
	Styles []EffectStyle // reusing the same shape as the styles-API path
}

// ScanEffects fetches the page rooted at pageNodeID and walks for effects.
// log writes per-bucket counts when verbose.
func ScanEffects(ctx context.Context, c *client.Client, fileKey, pageNodeID string, log *slog.Logger) (*ScanEffectsResult, error) {
	resp, err := c.GetFileNodes(ctx, fileKey, []string{pageNodeID}, 0)
	if err != nil {
		return nil, fmt.Errorf("fetch page: %w", err)
	}
	nodes, _ := resp["nodes"].(map[string]any)
	var doc map[string]any
	for _, v := range nodes {
		if m, ok := v.(map[string]any); ok && m != nil {
			doc, _ = m["document"].(map[string]any)
			break
		}
	}
	if doc == nil {
		return nil, fmt.Errorf("page has no document")
	}

	groups := map[string][]Effect{}
	walk(doc, "", "", groups)

	out := &ScanEffectsResult{}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out.Styles = append(out.Styles, EffectStyle{
			Name:    k,
			Effects: groups[k],
		})
	}
	log.Info("effects scan", "groups", len(out.Styles))
	return out, nil
}

// walk DFS-traverses doc and collects effects bucketed by the nearest named
// FRAME/COMPONENT/COMPONENT_SET ancestor.
func walk(node map[string]any, parentName, parentType string, groups map[string][]Effect) {
	if node == nil {
		return
	}
	myName := stringKey(node, "name")
	myType := stringKey(node, "type")

	bucket := parentName
	if isBucketType(myType) && myName != "" {
		bucket = myName
	}

	if rawEffects, ok := node["effects"].([]any); ok && len(rawEffects) > 0 {
		effects := parseEffects(rawEffects)
		// Filter to shadow effects only
		var shadows []Effect
		for _, e := range effects {
			if e.Type == "DROP_SHADOW" || e.Type == "INNER_SHADOW" {
				shadows = append(shadows, e)
			}
		}
		if len(shadows) > 0 && bucket != "" {
			// Dedupe: only keep first occurrence of identical effect list per bucket
			if _, seen := groups[bucket]; !seen {
				groups[bucket] = shadows
			}
		}
	}

	if children, ok := node["children"].([]any); ok {
		for _, child := range children {
			if cm, ok := child.(map[string]any); ok {
				walk(cm, bucket, myType, groups)
			}
		}
	}
}

func isBucketType(t string) bool {
	return t == "FRAME" || t == "COMPONENT" || t == "COMPONENT_SET" || t == "INSTANCE"
}
