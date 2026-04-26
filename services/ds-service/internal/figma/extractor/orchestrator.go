package extractor

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/figma/client"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/figma/types"
)

// FindCandidateFrames is exposed for testing. The orchestrator uses this internally.
// asMap is a small helper duplicated from pairwalker.go to keep this file self-contained.
func asMapFn(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

// Result is the aggregate of one extraction run.
type Result struct {
	Brand          string
	FileKey        string
	CandidateCount int
	PairCount      int
	Pairs          []types.FramePair
	Observations   []Observation
	Pairs_ByName   map[string][]Observation // name -> observations
	Roles          []SemanticRole
	BasePalette    map[string]types.Color // unique colors observed across all roles
	TextStyles     []TextStyle
}

// SemanticRole is a clustered token: every observation that resolved to the same
// (light, dark) color pair, regardless of original Figma node name.
type SemanticRole struct {
	Key             string // "#FFFFFF↔#171A1E"
	Light           types.Color
	Dark            types.Color
	HasLight        bool
	HasDark         bool
	Names           []string // all node names that mapped here
	NamesCanonical  string   // most descriptive name (longest non-default)
	InstanceCount   int      // total observations
	NearbyLabels    []string // labels found in adjacent TEXT nodes
}

// TextStyle is a typography token sourced from Figma's published styles.
type TextStyle struct {
	Name        string
	StyleID     string
	NodeID      string // a TEXT node we can sample for actual font/size
	FontFamily  string
	FontWeight  int
	FontSize    float64
	LineHeight  float64
	LetterSpace float64
	TextDecor   string
}

// Run executes the end-to-end pipeline against a single brand's Figma file.
//
// If targetNodeID is non-empty, only that single node (typically a SECTION)
// is fetched at depth=8 — much faster than full-file traversal.
// If empty, the full file is fetched at depth=4 and all candidate frames are scanned.
func Run(ctx context.Context, c *client.Client, brand, fileKey, targetNodeID string, log *slog.Logger) (*Result, error) {
	var rootNode map[string]any

	if targetNodeID != "" {
		log.Info("fetching single node (depth=8)", "brand", brand, "file_key", fileKey, "node_id", targetNodeID)
		resp, err := c.GetFileNodes(ctx, fileKey, []string{targetNodeID}, 8)
		if err != nil {
			return nil, fmt.Errorf("get nodes: %w", err)
		}
		nodes, _ := resp["nodes"].(map[string]any)
		if nodes == nil {
			return nil, fmt.Errorf("response missing nodes map")
		}
		// Find the requested node (Figma normalizes "X:Y" but the response key may use either).
		var payload map[string]any
		for _, v := range nodes {
			if m, ok := v.(map[string]any); ok && m != nil {
				payload = m
				break
			}
		}
		if payload == nil {
			return nil, fmt.Errorf("node %s not found in response", targetNodeID)
		}
		rootNode = map[string]any{
			"document": map[string]any{
				"id":       "synthetic-root",
				"type":     "DOCUMENT",
				"name":     "Single-node fetch",
				"children": []any{payload["document"]},
			},
		}
		log.Info("node fetched", "name", asMapFn(payload["document"])["name"])
	} else {
		log.Info("fetching file (depth=4)", "brand", brand, "file_key", fileKey)
		file, err := c.GetFile(ctx, fileKey, 4)
		if err != nil {
			return nil, fmt.Errorf("get file: %w", err)
		}
		log.Info("file fetched",
			"name", file["name"],
			"version", file["version"],
			"role", file["role"],
		)
		rootNode = file
	}

	frames := FindCandidateFrames(rootNode)
	log.Info("candidate frames", "count", len(frames))
	for _, f := range frames {
		log.Debug("frame", "name", f.Name, "page", f.Page, "parent", f.Parent,
			"size", fmt.Sprintf("%dx%d", f.Width, f.Height), "bg", f.Bg.Hex())
	}

	pairs := PairFrames(frames)
	log.Info("frame pairs", "count", len(pairs))

	var observations []Observation
	for _, p := range pairs {
		WalkPair(p, &observations)
	}
	log.Info("observations gathered", "count", len(observations))

	roles := clusterRoles(observations)
	log.Info("semantic roles after clustering", "count", len(roles))

	basePalette := buildBasePalette(observations)

	r := &Result{
		Brand:          brand,
		FileKey:        fileKey,
		CandidateCount: len(frames),
		PairCount:      len(pairs),
		Pairs:          pairs,
		Observations:   observations,
		Roles:          roles,
		BasePalette:    basePalette,
	}
	return r, nil
}

// clusterRoles groups observations by their (Light, Dark) color tuple.
//
// Rationale: "Same UI, different colors, same name" is operationalized as
// "same hex pair = same semantic token, regardless of which Figma node names hit it".
// Multiple Figma nodes (Action Card, Cash flow, Frame 2147228069 etc) can share
// the same role.
func clusterRoles(obs []Observation) []SemanticRole {
	groups := map[string]*SemanticRole{}
	for _, o := range obs {
		// Build a stable key
		var key string
		switch {
		case o.HasLight && o.HasDark:
			key = o.Light.Hex() + "↔" + o.Dark.Hex()
		case o.HasLight:
			key = o.Light.Hex() + "↔"
		case o.HasDark:
			key = "↔" + o.Dark.Hex()
		default:
			continue
		}
		role, ok := groups[key]
		if !ok {
			role = &SemanticRole{
				Key:      key,
				Light:    o.Light,
				Dark:     o.Dark,
				HasLight: o.HasLight,
				HasDark:  o.HasDark,
			}
			groups[key] = role
		}
		role.InstanceCount++
		if !contains(role.Names, o.Name) && o.Name != "" {
			role.Names = append(role.Names, o.Name)
		}
		if o.NearbyLabel != "" && !contains(role.NearbyLabels, o.NearbyLabel) {
			role.NearbyLabels = append(role.NearbyLabels, o.NearbyLabel)
		}
	}

	// Pick canonical name per role: longest non-generic name.
	for _, r := range groups {
		r.NamesCanonical = pickCanonicalName(r.Names)
	}

	// Sort by instance count descending (most-used roles first).
	out := make([]SemanticRole, 0, len(groups))
	for _, r := range groups {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].InstanceCount > out[j].InstanceCount
	})
	return out
}

// buildBasePalette gathers every distinct color observed (across all roles + sides)
// for emission as the "primitive" palette.
func buildBasePalette(obs []Observation) map[string]types.Color {
	out := map[string]types.Color{}
	for _, o := range obs {
		if o.HasLight {
			out[o.Light.Hex()] = o.Light
		}
		if o.HasDark {
			out[o.Dark.Hex()] = o.Dark
		}
	}
	return out
}

// pickCanonicalName chooses the most descriptive name from the observed names.
// Heuristics: prefer named over auto-generated ("Rectangle 12345" / "Frame 12345").
// Among the descriptive ones, prefer the longest (most specific).
func pickCanonicalName(names []string) string {
	if len(names) == 0 {
		return ""
	}
	type cand struct {
		s     string
		score int
	}
	cs := make([]cand, 0, len(names))
	for _, n := range names {
		score := len(n)
		// Penalize auto-generated names heavily.
		lower := strings.ToLower(n)
		if isAutoGenName(lower) {
			score -= 1000
		}
		// Prefer names with descriptive words.
		for _, kw := range []string{"surface", "text", "icon", "border", "card", "background", "primary", "secondary", "action", "muted", "elevated", "subtle", "bold", "success", "warning", "error", "danger"} {
			if strings.Contains(lower, kw) {
				score += 50
			}
		}
		cs = append(cs, cand{n, score})
	}
	sort.Slice(cs, func(i, j int) bool { return cs[i].score > cs[j].score })
	return cs[0].s
}

func isAutoGenName(lower string) bool {
	for _, p := range []string{"rectangle ", "frame ", "ellipse ", "vector", "path", "group ", "instance "} {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	return false
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
