// Package extractor implements the pair-walker that locates light/dark frame
// pairs and recursively walks both in lockstep to derive ModePair tokens.
//
// Algorithm overview (see project memory `project_indmoney_ds_token_source`):
//
//  1. Find all top-level FRAME nodes that are mobile-screen-sized AND have a
//     classifiable solid background (light OR dark).
//  2. Cluster frames by parent (typically a SECTION) — designers place pairs
//     in the same parent for side-by-side review.
//  3. Within each cluster, pair light↔dark frames by name match, dimension match,
//     and reading-order proximity.
//  4. For each pair, walk both frame trees in parallel by structural position
//     (componentId for INSTANCE; name+type for FRAME/RECTANGLE).
//  5. At each matched pair of leaf nodes, capture (light_fill, dark_fill) keyed
//     by node name and accumulate into the observation table.
//  6. Cluster identical color-pair tuples to identify shared semantic roles.
package extractor

import (
	"sort"
	"strings"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/figma/types"
)

// Observation is one (name, role label nearby, light fill, dark fill, where).
type Observation struct {
	Name        string
	NearbyLabel string // closest TEXT sibling — usually the human role description
	Light       types.Color
	Dark        types.Color
	HasLight    bool
	HasDark     bool
	NodeType    string
	PairID      string // which FramePair this came from
	Path        string // dotted name path from frame root
}

// FindCandidateFrames walks the file's pages and returns all top-level
// mobile-sized frames with classifiable solid backgrounds.
func FindCandidateFrames(file map[string]any) []types.Frame {
	doc := mapKey(file, "document")
	pages := arrayKey(doc, "children")
	var out []types.Frame
	for _, p := range pages {
		pageNode := asMap(p)
		pageName := stringKey(pageNode, "name")
		// Walk top-level frames AND one level into sections (since a SECTION can
		// contain mobile frames as direct children — that's exactly the INDstocks "Phase 1" pattern).
		for _, child := range arrayKey(pageNode, "children") {
			collectMobileFrames(asMap(child), pageName, "", &out)
		}
	}
	return out
}

func collectMobileFrames(node map[string]any, page, parent string, out *[]types.Frame) {
	if node == nil {
		return
	}
	t := stringKey(node, "type")
	name := stringKey(node, "name")
	if t == "FRAME" {
		bg := primaryFill(node)
		bbox := mapKey(node, "absoluteBoundingBox")
		w := intKey(bbox, "width")
		h := intKey(bbox, "height")
		f := types.Frame{
			ID:     stringKey(node, "id"),
			Name:   name,
			Bg:     bg,
			Width:  w,
			Height: h,
			X:      intKey(bbox, "x"),
			Y:      intKey(bbox, "y"),
			Doc:    node,
			Page:   page,
			Parent: parent,
		}
		if f.IsMobileSize() && (f.Bg.IsLightMode() || f.Bg.IsDarkMode()) {
			*out = append(*out, f)
			return // don't recurse into a classified frame
		}
	}
	// SECTION or unclassified FRAME — recurse one level
	if t == "SECTION" || t == "FRAME" || t == "GROUP" {
		nextParent := name
		if t == "SECTION" {
			nextParent = name
		}
		for _, child := range arrayKey(node, "children") {
			collectMobileFrames(asMap(child), page, nextParent, out)
		}
	}
}

// PairFrames pairs light frames with dark frames using same-parent + name match
// + reading-order proximity. Returns at most one Dark per Light.
func PairFrames(frames []types.Frame) []types.FramePair {
	// Sort: parent, then x, then y — gives reading order within each section.
	sort.SliceStable(frames, func(i, j int) bool {
		if frames[i].Parent != frames[j].Parent {
			return frames[i].Parent < frames[j].Parent
		}
		if frames[i].Y != frames[j].Y {
			return frames[i].Y < frames[j].Y
		}
		return frames[i].X < frames[j].X
	})

	// Group by parent.
	groups := map[string][]types.Frame{}
	for _, f := range frames {
		groups[f.Parent] = append(groups[f.Parent], f)
	}

	var pairs []types.FramePair
	for _, group := range groups {
		// Within group, pair lights and darks by name match first; fall back to nearest neighbor.
		used := make(map[string]bool)
		for i := range group {
			lf := group[i]
			if !lf.Bg.IsLightMode() || used[lf.ID] {
				continue
			}
			best := -1
			bestScore := 0
			bestReason := ""
			for j := range group {
				df := group[j]
				if !df.Bg.IsDarkMode() || used[df.ID] {
					continue
				}
				score, reason := pairScore(lf, df)
				if score > bestScore {
					bestScore = score
					best = j
					bestReason = reason
				}
			}
			if best >= 0 {
				pairs = append(pairs, types.FramePair{
					Light:      lf,
					Dark:       group[best],
					PairScore:  bestScore,
					PairReason: bestReason,
				})
				used[lf.ID] = true
				used[group[best].ID] = true
			}
		}
	}
	return pairs
}

func pairScore(l, d types.Frame) (int, string) {
	score := 0
	reasons := []string{}
	if normalize(l.Name) == normalize(d.Name) {
		score += 100
		reasons = append(reasons, "same-name")
	}
	if l.Width == d.Width && l.Height == d.Height {
		score += 30
		reasons = append(reasons, "same-size")
	}
	// Adjacent frames score higher than far-apart ones.
	dx := abs(l.X - d.X)
	dy := abs(l.Y - d.Y)
	if dx < l.Width*3 && dy < l.Height*2 {
		score += 10
		reasons = append(reasons, "adjacent")
	}
	return score, strings.Join(reasons, "+")
}

// WalkPair recursively walks both frames in lockstep and records observations.
//
// Matching strategy:
//   - INSTANCE nodes: match by `componentId` (strongest — same logical component)
//   - All others: match by (type, normalized name) at same child index, with
//     fallback to scanning sibling list for the same (type, name) pair.
func WalkPair(p types.FramePair, observations *[]Observation) {
	walkParallel(p.Light.Doc, p.Dark.Doc, p.Light.ID+"↔"+p.Dark.ID, "", observations)
}

func walkParallel(l, d map[string]any, pairID, path string, out *[]Observation) {
	if l == nil || d == nil {
		return
	}
	lType := stringKey(l, "type")
	dType := stringKey(d, "type")
	if lType != dType {
		return // structural mismatch — the pair walk fails gracefully here
	}
	name := stringKey(l, "name")
	if name == "" {
		name = stringKey(d, "name")
	}

	lFill := primaryFill(l)
	dFill := primaryFill(d)
	if hasFill(l) || hasFill(d) {
		obs := Observation{
			Name:     name,
			Light:    lFill,
			Dark:     dFill,
			HasLight: hasFill(l),
			HasDark:  hasFill(d),
			NodeType: lType,
			PairID:   pairID,
			Path:     path + "/" + name,
		}
		*out = append(*out, obs)
	}

	// Recurse children. Try to align by index; if names diverge, attempt re-alignment.
	lChildren := arrayKey(l, "children")
	dChildren := arrayKey(d, "children")
	pairs := alignChildren(lChildren, dChildren)
	nextPath := path + "/" + name
	for _, pp := range pairs {
		walkParallel(asMap(pp.l), asMap(pp.d), pairID, nextPath, out)
	}
}

type childPair struct{ l, d any }

// alignChildren tries to match light & dark children by:
//  1. Same componentId for INSTANCE nodes (strongest signal)
//  2. Same (type, name) pair
//  3. Index-based fallback (same position, same type)
//
// Returns pairs in light-child order (preserves traversal stability).
func alignChildren(lc, dc []any) []childPair {
	matched := make(map[int]int, len(lc)) // lIdx → dIdx
	dUsed := make([]bool, len(dc))

	// Pass 1: componentId match (INSTANCE ↔ INSTANCE).
	for i, lAny := range lc {
		l := asMap(lAny)
		if stringKey(l, "type") != "INSTANCE" {
			continue
		}
		lcid := stringKey(l, "componentId")
		if lcid == "" {
			continue
		}
		for j, dAny := range dc {
			if dUsed[j] {
				continue
			}
			d := asMap(dAny)
			if stringKey(d, "type") == "INSTANCE" && stringKey(d, "componentId") == lcid {
				matched[i] = j
				dUsed[j] = true
				break
			}
		}
	}

	// Pass 2: (type, normalized name) match for everything still unmatched.
	for i, lAny := range lc {
		if _, done := matched[i]; done {
			continue
		}
		l := asMap(lAny)
		lt := stringKey(l, "type")
		ln := normalize(stringKey(l, "name"))
		for j, dAny := range dc {
			if dUsed[j] {
				continue
			}
			d := asMap(dAny)
			if stringKey(d, "type") == lt && normalize(stringKey(d, "name")) == ln {
				matched[i] = j
				dUsed[j] = true
				break
			}
		}
	}

	// Pass 3: positional fallback (same index, same type).
	for i, lAny := range lc {
		if _, done := matched[i]; done {
			continue
		}
		if i >= len(dc) || dUsed[i] {
			continue
		}
		l := asMap(lAny)
		d := asMap(dc[i])
		if stringKey(l, "type") == stringKey(d, "type") {
			matched[i] = i
			dUsed[i] = true
		}
	}

	// Build output preserving light-child order.
	out := make([]childPair, 0, len(matched))
	for i := range lc {
		if j, ok := matched[i]; ok {
			out = append(out, childPair{l: lc[i], d: dc[j]})
		}
	}
	return out
}

// ---- node accessors --------------------------------------------------------

func asMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

func mapKey(m map[string]any, k string) map[string]any {
	if m == nil {
		return nil
	}
	v, ok := m[k]
	if !ok {
		return nil
	}
	return asMap(v)
}

func arrayKey(m map[string]any, k string) []any {
	if m == nil {
		return nil
	}
	v, ok := m[k].([]any)
	if !ok {
		return nil
	}
	return v
}

func stringKey(m map[string]any, k string) string {
	if m == nil {
		return ""
	}
	if s, ok := m[k].(string); ok {
		return s
	}
	return ""
}

func intKey(m map[string]any, k string) int {
	if m == nil {
		return 0
	}
	if f, ok := m[k].(float64); ok {
		return int(f)
	}
	return 0
}

func floatKey(m map[string]any, k string) float64 {
	if m == nil {
		return 0
	}
	if f, ok := m[k].(float64); ok {
		return f
	}
	return 0
}

// primaryFill returns the first SOLID fill on a node, or zero Color if none.
func primaryFill(node map[string]any) types.Color {
	fills, ok := node["fills"].([]any)
	if !ok || len(fills) == 0 {
		return types.Color{}
	}
	first := asMap(fills[0])
	if stringKey(first, "type") != "SOLID" {
		return types.Color{}
	}
	cm := mapKey(first, "color")
	r := floatKey(cm, "r")
	g := floatKey(cm, "g")
	b := floatKey(cm, "b")
	var op *float64
	if v, ok := first["opacity"].(float64); ok {
		op = &v
	} else if v, ok := cm["a"].(float64); ok {
		op = &v
	}
	return types.FromFigma(r, g, b, op)
}

func hasFill(node map[string]any) bool {
	fills, ok := node["fills"].([]any)
	if !ok || len(fills) == 0 {
		return false
	}
	first := asMap(fills[0])
	return stringKey(first, "type") == "SOLID"
}

// normalize collapses whitespace + lowercases for name comparison.
func normalize(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
