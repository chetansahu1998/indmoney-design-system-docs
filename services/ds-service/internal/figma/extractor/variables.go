// Variables extractor — pulls Figma Variables (NUMBER scope) from a file
// and classifies them into spacing / radius / padding / size buckets.
//
// Pipeline:
//   1. GET /v1/files/:fileKey/variables/local → variableCollections + variables.
//   2. Filter to NUMBER variables (resolvedType=="FLOAT").
//   3. Classify by collection name OR variable name:
//        - "space", "spacing"            → spacing
//        - "radius", "corner"            → radius
//        - "padding"                     → padding
//        - "size", "width", "height"     → size
//        - everything else               → other
//   4. For each variable, resolve its default mode value (numeric).
//
// On Free plans the Variables API returns 403; the caller should treat that as
// "no variables exposed" and fall back to hand-curated values.
package extractor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/figma/client"
)

// Variable is a single classified Figma Variable.
type Variable struct {
	ID         string  // "VariableID:1:23"
	Name       string  // "space.16"
	Collection string  // "Space" / "Radius" / etc.
	Bucket     string  // spacing | radius | padding | size | other
	Px         float64 // resolved default-mode value
	Description string
}

// VariablesResult is the output of variable extraction for a file.
type VariablesResult struct {
	Variables []Variable
	// CollectionNames is every collection seen — surfaced in logs to help
	// the maintainer pick which to map.
	CollectionNames []string
}

// RunVariables fetches local variables and classifies the NUMBER ones.
// If the API returns 403 (typically Free-plan files), it returns an empty
// result and a sentinel error so callers can degrade gracefully.
var ErrVariablesUnavailable = errors.New("variables API not available (likely Free plan or missing file_variables:read scope)")

func RunVariables(ctx context.Context, c *client.Client, fileKey string, log *slog.Logger) (*VariablesResult, error) {
	resp, err := c.GetLocalVariables(ctx, fileKey)
	if err != nil {
		var ae *client.APIError
		if errors.As(err, &ae) && (ae.Status == 403 || ae.Status == 404) {
			log.Warn("variables endpoint unavailable", "status", ae.Status, "body", truncate(ae.Body, 240))
			return &VariablesResult{}, ErrVariablesUnavailable
		}
		return nil, fmt.Errorf("get local variables: %w", err)
	}

	meta, _ := resp["meta"].(map[string]any)
	if meta == nil {
		return &VariablesResult{}, nil
	}
	collections, _ := meta["variableCollections"].(map[string]any)
	variables, _ := meta["variables"].(map[string]any)

	colInfo := map[string]struct {
		name          string
		defaultModeID string
	}{}
	colNames := make([]string, 0, len(collections))
	for id, raw := range collections {
		c, _ := raw.(map[string]any)
		name := stringKey(c, "name")
		defMode := stringKey(c, "defaultModeId")
		colInfo[id] = struct {
			name          string
			defaultModeID string
		}{name, defMode}
		colNames = append(colNames, name)
	}

	out := &VariablesResult{CollectionNames: colNames}
	for _, raw := range variables {
		v, _ := raw.(map[string]any)
		if stringKey(v, "resolvedType") != "FLOAT" {
			continue
		}
		colID := stringKey(v, "variableCollectionId")
		col := colInfo[colID]
		valuesByMode, _ := v["valuesByMode"].(map[string]any)
		val, ok := valuesByMode[col.defaultModeID].(float64)
		if !ok {
			// Some variables alias another variable — skip aliases for now
			continue
		}
		name := stringKey(v, "name")
		bucket := classifyBucket(col.name, name)
		out.Variables = append(out.Variables, Variable{
			ID:          stringKey(v, "id"),
			Name:        name,
			Collection:  col.name,
			Bucket:      bucket,
			Px:          val,
			Description: stringKey(v, "description"),
		})
	}
	return out, nil
}

func classifyBucket(collection, name string) string {
	c := strings.ToLower(collection)
	n := strings.ToLower(name)
	has := func(haystack, needle string) bool { return strings.Contains(haystack, needle) }
	switch {
	case has(c, "space") || has(n, "space") || has(c, "spacing") || has(n, "spacing"):
		return "spacing"
	case has(c, "radius") || has(c, "corner") || has(n, "radius") || has(n, "corner"):
		return "radius"
	case has(c, "padding") || has(n, "padding"):
		return "padding"
	case has(c, "size") || has(n, "size") || has(n, "width") || has(n, "height"):
		return "size"
	}
	return "other"
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
