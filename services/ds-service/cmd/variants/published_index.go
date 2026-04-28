// fetchPublishedIndex pulls the durable-key index for a file by hitting
// /v1/files/:key/components and /v1/files/:key/component_sets. These two
// endpoints return one record per published node with its stable
// Component.Key — the same identifier the Plugin API uses with
// importComponentByKeyAsync, and what survives publish/unpublish/rename
// cycles. The /nodes/ document tree only carries `key` when the file is
// currently in a published state, so the index endpoints are the source
// of truth for cross-file references.
//
// Returns two parallel maps:
//   - keyByNodeID:  node_id → durable Component.Key
//   - descByNodeID: node_id → description (when set in Figma's UI)
//
// On error (e.g. file not published, PAT lacks scope) returns empty maps
// and logs the failure. Callers should treat missing entries as benign.

package main

import (
	"context"
	"log/slog"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/figma/client"
)

func fetchPublishedIndex(ctx context.Context, c *client.Client, fileKey string, log *slog.Logger) (keyByNodeID, descByNodeID map[string]string) {
	keyByNodeID = map[string]string{}
	descByNodeID = map[string]string{}

	walk := func(label, listKey string, payload map[string]any) {
		meta, _ := payload["meta"].(map[string]any)
		if meta == nil {
			return
		}
		arr, _ := meta[listKey].([]any)
		count := 0
		for _, item := range arr {
			it, _ := item.(map[string]any)
			if it == nil {
				continue
			}
			nodeID, _ := it["node_id"].(string)
			key, _ := it["key"].(string)
			if nodeID == "" {
				continue
			}
			if key != "" {
				keyByNodeID[nodeID] = key
				count++
			}
			if d, _ := it["description"].(string); d != "" {
				descByNodeID[nodeID] = d
			}
		}
		log.Info("published index loaded", "endpoint", label, "count", count)
	}

	if resp, err := c.GetFileComponentSets(ctx, fileKey); err != nil {
		log.Warn("component_sets fetch failed; durable set keys unavailable", "err", err)
	} else {
		walk("/component_sets", "component_sets", resp)
	}
	if resp, err := c.GetFileComponents(ctx, fileKey); err != nil {
		log.Warn("components fetch failed; durable variant keys unavailable", "err", err)
	} else {
		walk("/components", "components", resp)
	}

	return keyByNodeID, descByNodeID
}
