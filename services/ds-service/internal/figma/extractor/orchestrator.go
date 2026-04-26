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

// SourceKind identifies what role a Figma file plays in the extraction.
type SourceKind string

const (
	// SourceDesignSystem files contribute typography (TEXT styles via /styles endpoint)
	// and may optionally contribute named color frames. Example: Glyph.
	SourceDesignSystem SourceKind = "design-system"

	// SourceProduct files contribute pair-walker observations from production light/dark
	// screen renderings. Example: INDstocks V4.
	SourceProduct SourceKind = "product"
)

// Source declares one extraction input: (kind, file_key, node_id).
// node_id may be empty to fetch the whole file at depth=4.
// Multiple Sources can target the same file_key with different node_ids.
type Source struct {
	Kind    SourceKind
	FileKey string
	NodeID  string // optional; "X:Y" form
}

// String returns "kind:file_key:node_id" for logging/CLI parsing symmetry.
func (s Source) String() string {
	if s.NodeID == "" {
		return fmt.Sprintf("%s:%s", s.Kind, s.FileKey)
	}
	return fmt.Sprintf("%s:%s:%s", s.Kind, s.FileKey, s.NodeID)
}

// ParseSource accepts "kind:file_key[:node_id]" CLI form.
func ParseSource(raw string) (Source, error) {
	parts := strings.SplitN(raw, ":", 3)
	if len(parts) < 2 {
		return Source{}, fmt.Errorf("source must be 'kind:file_key[:node_id]', got %q", raw)
	}
	kind := SourceKind(parts[0])
	if kind != SourceDesignSystem && kind != SourceProduct {
		return Source{}, fmt.Errorf("unknown source kind %q (expected %q or %q)", kind, SourceDesignSystem, SourceProduct)
	}
	s := Source{Kind: kind, FileKey: parts[1]}
	if len(parts) == 3 {
		s.NodeID = parts[2]
	}
	return s, nil
}

// SourceResult holds the per-source contribution to the pooled output.
type SourceResult struct {
	Source         Source
	Name           string // human-readable file name from Figma
	CandidateCount int
	PairCount      int
	Pairs          []types.FramePair
	Observations   []Observation
	TextStyles     []TextStyle
}

// Result is the aggregate of one extraction run across all sources.
type Result struct {
	Brand        string
	Sources      []SourceResult // per-source breakdown
	Observations []Observation  // pooled across all SourceProduct sources
	Roles        []SemanticRole
	BasePalette  map[string]types.Color // unique colors observed across all sources
	TextStyles   []TextStyle            // pooled across all SourceDesignSystem sources
}

// CandidateCount and PairCount aggregate across sources for top-level reporting.
func (r *Result) CandidateCount() int {
	n := 0
	for _, s := range r.Sources {
		n += s.CandidateCount
	}
	return n
}
func (r *Result) PairCount() int {
	n := 0
	for _, s := range r.Sources {
		n += s.PairCount
	}
	return n
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

// Run executes the end-to-end pipeline across one or more Sources.
//
// Each source contributes differently:
//   - SourceProduct → fetched at depth=8 (single node) or depth=4 (full file),
//     pair-walked, observations pooled.
//   - SourceDesignSystem → fetches /styles endpoint for TEXT styles AND optionally
//     pair-walks any node_id-targeted sections (some DS files have token frames).
//
// All observations and text styles are pooled before clustering.
func Run(ctx context.Context, c *client.Client, brand string, sources []Source, log *slog.Logger) (*Result, error) {
	if len(sources) == 0 {
		return nil, fmt.Errorf("at least one source required")
	}

	r := &Result{
		Brand:       brand,
		BasePalette: map[string]types.Color{},
	}

	for _, src := range sources {
		log.Info("starting source", "kind", src.Kind, "file_key", src.FileKey, "node_id", src.NodeID)
		sr, err := runSource(ctx, c, src, log)
		if err != nil {
			return nil, fmt.Errorf("source %s: %w", src.String(), err)
		}
		r.Sources = append(r.Sources, *sr)

		// Pool observations from product sources (and any DS source that ran a pair-walker).
		r.Observations = append(r.Observations, sr.Observations...)
		// Pool text styles from DS sources.
		r.TextStyles = append(r.TextStyles, sr.TextStyles...)

		log.Info("source done",
			"kind", sr.Source.Kind,
			"file", sr.Name,
			"frames", sr.CandidateCount,
			"pairs", sr.PairCount,
			"obs", len(sr.Observations),
			"text_styles", len(sr.TextStyles),
		)
	}

	r.Roles = clusterRoles(r.Observations)
	r.BasePalette = buildBasePalette(r.Observations)

	log.Info("aggregate",
		"sources", len(r.Sources),
		"frames", r.CandidateCount(),
		"pairs", r.PairCount(),
		"observations", len(r.Observations),
		"roles", len(r.Roles),
		"base_colors", len(r.BasePalette),
		"text_styles", len(r.TextStyles),
	)

	return r, nil
}

func runSource(ctx context.Context, c *client.Client, src Source, log *slog.Logger) (*SourceResult, error) {
	sr := &SourceResult{Source: src}

	// 1. Pair-walker pass (always run if NodeID set, OR if Kind=product and NodeID empty)
	if src.NodeID != "" || src.Kind == SourceProduct {
		rootNode, fileName, err := fetchRoot(ctx, c, src, log)
		if err != nil {
			return nil, err
		}
		sr.Name = fileName

		frames := FindCandidateFrames(rootNode)
		sr.CandidateCount = len(frames)
		log.Info("candidate frames", "source", src.String(), "count", len(frames))

		pairs := PairFrames(frames)
		sr.Pairs = pairs
		sr.PairCount = len(pairs)
		log.Info("frame pairs", "source", src.String(), "count", len(pairs))

		var obs []Observation
		for _, p := range pairs {
			WalkPair(p, &obs)
		}
		sr.Observations = obs
	}

	// 2. Text styles pass (only for DS sources)
	if src.Kind == SourceDesignSystem {
		stylesResp, err := c.GetStyles(ctx, src.FileKey)
		if err != nil {
			log.Warn("styles fetch failed (continuing without)", "err", err)
		} else {
			sr.TextStyles = extractTextStyles(stylesResp, log)
			log.Info("text styles fetched", "source", src.String(), "count", len(sr.TextStyles))
		}
	}

	return sr, nil
}

func fetchRoot(ctx context.Context, c *client.Client, src Source, log *slog.Logger) (map[string]any, string, error) {
	if src.NodeID != "" {
		resp, err := c.GetFileNodes(ctx, src.FileKey, []string{src.NodeID}, 8)
		if err != nil {
			return nil, "", fmt.Errorf("get nodes: %w", err)
		}
		nodes, _ := resp["nodes"].(map[string]any)
		var payload map[string]any
		for _, v := range nodes {
			if m, ok := v.(map[string]any); ok && m != nil {
				payload = m
				break
			}
		}
		if payload == nil {
			return nil, "", fmt.Errorf("node %s not found in response", src.NodeID)
		}
		fileName, _ := resp["name"].(string)
		root := map[string]any{
			"document": map[string]any{
				"id":       "synthetic-root",
				"type":     "DOCUMENT",
				"name":     fileName,
				"children": []any{payload["document"]},
			},
		}
		return root, fileName, nil
	}
	// Whole file at depth=4
	file, err := c.GetFile(ctx, src.FileKey, 4)
	if err != nil {
		return nil, "", fmt.Errorf("get file: %w", err)
	}
	name, _ := file["name"].(string)
	return file, name, nil
}

// extractTextStyles converts /v1/files/<key>/styles response into TextStyle entries.
//
// Only TEXT-typed styles are kept. Field/font metadata (size, weight, line height)
// is NOT in this endpoint — that requires a follow-up /v1/files/<key>/nodes call
// using the style's node_id. v1 captures just name + style_id; v1.1 will
// dereference the typography metadata.
func extractTextStyles(resp map[string]any, log *slog.Logger) []TextStyle {
	meta, _ := resp["meta"].(map[string]any)
	if meta == nil {
		return nil
	}
	styles, _ := meta["styles"].([]any)
	out := []TextStyle{}
	for _, s := range styles {
		m, _ := s.(map[string]any)
		if m == nil {
			continue
		}
		if t, _ := m["style_type"].(string); t != "TEXT" {
			continue
		}
		name, _ := m["name"].(string)
		styleID, _ := m["key"].(string)
		nodeID, _ := m["node_id"].(string)
		out = append(out, TextStyle{
			Name:    name,
			StyleID: styleID,
			NodeID:  nodeID,
		})
	}
	return out
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
