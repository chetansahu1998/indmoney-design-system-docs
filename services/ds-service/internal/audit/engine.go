package audit

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Audit walks a Figma node tree (the "document" payload from a /v1/files/<key>
// or /v1/files/<key>/nodes response), classifies every screen, and returns
// the structured result that the plugin + Files tab + living-docs surfaces consume.
//
// Inputs:
//   - tree:        the Figma document map (top-level node with children = pages).
//   - tokens:      the published DS tokens to compare against (color + dimension).
//   - candidates:  the published DS components for matching (componentKey + name + styles).
//   - opts:        runtime tuning (drift thresholds, file metadata, manifest overrides).
//
// Output: AuditResult with one AuditScreen per final-design page.
//
// The engine is pure (no I/O). Callers (cmd/audit, cmd/server) wrap it with
// the Figma client + filesystem writer.
func Audit(tree map[string]any, tokens []DSToken, candidates []DSCandidate, opts Options) AuditResult {
	now := time.Now().UTC()
	result := AuditResult{
		SchemaVersion:   SchemaVersion,
		FileKey:         opts.FileKey,
		FileName:        opts.FileName,
		FileSlug:        opts.FileSlug,
		Brand:           opts.Brand,
		Owner:           opts.Owner,
		ExtractedAt:     now,
		FileRev:         opts.FileRev,
		DesignSystemRev: opts.DesignSystemRev,
		Extensions: map[string]any{
			"com.indmoney.provenance":  "figma-audit",
			"com.indmoney.extractedAt": now.Format(time.RFC3339),
			"com.indmoney.sweepRun":    opts.SweepRun,
		},
	}

	if tree == nil {
		return result
	}

	// Find candidate screens. If the manifest pins explicit final pages by id,
	// honour that. Otherwise apply the LooksLikeScreen heuristic.
	allowed := opts.AllowedFinalPageIDs
	screens := make([]map[string]any, 0)
	walkScreens(tree, allowed, &screens)

	for _, s := range screens {
		auditScreen := auditOneScreen(s, tokens, candidates, opts)
		result.Screens = append(result.Screens, auditScreen)
		result.Extensions["com.indmoney.last_node_count"] = auditScreen.NodeCount
	}

	// Roll up file-level metrics.
	tBound, tTotal := 0, 0
	fromDS, totalComps := 0, 0
	hexFreq := map[string]int{}
	for _, s := range result.Screens {
		c := s.Coverage
		tBound += c.Fills.Bound + c.Text.Bound + c.Spacing.Bound + c.Radius.Bound
		tTotal += c.Fills.Total + c.Text.Total + c.Spacing.Total + c.Radius.Total
		fromDS += s.ComponentSummary.FromDS
		totalComps += s.ComponentSummary.FromDS + s.ComponentSummary.Ambiguous + s.ComponentSummary.Custom
		for _, fix := range s.Fixes {
			if fix.Property == "fill" {
				hexFreq[fix.Observed]++
			}
		}
	}
	if tTotal > 0 {
		result.OverallCoverage = float64(tBound) / float64(tTotal)
	}
	if totalComps > 0 {
		result.OverallFromDS = float64(fromDS) / float64(totalComps)
	}
	result.HeadlineDriftHex = mostFrequent(hexFreq)
	return result
}

// Options is the runtime configuration knob set passed to Audit().
type Options struct {
	FileKey             string
	FileName            string
	FileSlug            string
	Brand               string
	Owner               string
	FileRev             string
	DesignSystemRev     string
	SweepRun            string
	AllowedFinalPageIDs []string // when set, only these page nodes are audited
	ColorDriftThreshold float64  // 0 = use default
	PxDriftThreshold    float64  // 0 = use default
	HeatThreshold       int      // usage count above which a drift is escalated to P1
}

func walkScreens(node map[string]any, allowed []string, out *[]map[string]any) {
	if node == nil {
		return
	}
	t, _ := node["type"].(string)
	id, _ := node["id"].(string)
	name, _ := node["name"].(string)

	if t == "FRAME" {
		isAllowed := false
		if len(allowed) > 0 {
			for _, a := range allowed {
				if a == id {
					isAllowed = true
					break
				}
			}
		} else {
			isAllowed = LooksLikeScreen(name)
		}
		if isAllowed {
			*out = append(*out, node)
			return // don't recurse into a screen — its descendants are audit subjects, not more screens
		}
	}
	children, _ := node["children"].([]any)
	for _, ch := range children {
		if cm, ok := ch.(map[string]any); ok {
			walkScreens(cm, allowed, out)
		}
	}
}

func auditOneScreen(screen map[string]any, tokens []DSToken, candidates []DSCandidate, opts Options) AuditScreen {
	id, _ := screen["id"].(string)
	name, _ := screen["name"].(string)

	heat := opts.HeatThreshold
	if heat <= 0 {
		heat = 5
	}

	cov := TokenCoverage{}
	summary := ComponentSummary{}
	fixes := []FixCandidate{}
	matches := []ComponentMatch{}
	hexFreq := map[string]int{}

	nodeCount := 0
	walk(screen, func(n map[string]any) {
		nodeCount++
		t, _ := n["type"].(string)
		nodeID, _ := n["id"].(string)
		nodeName, _ := n["name"].(string)

		switch t {
		case "INSTANCE", "COMPONENT":
			match := matchComponent(n, candidates, nodeID, nodeName)
			matches = append(matches, match)
			switch match.Decision {
			case DecisionAccept:
				summary.FromDS++
			case DecisionAmbiguous:
				summary.Ambiguous++
			case DecisionReject:
				summary.Custom++
				fixes = append(fixes, FixCandidate{
					NodeID:   nodeID,
					NodeName: nodeName,
					Property: "component",
					Observed: nodeName,
					Reason:   "custom",
					Priority: PriorityP3,
				})
			}
		}

		// Token coverage: walk fills, strokes, text-style refs, padding, radius.
		auditFills(n, &cov, &fixes, hexFreq, tokens, opts, nodeID, nodeName)
		auditTextStyle(n, &cov, &fixes, tokens, nodeID, nodeName)
		auditDimension(n, &cov, &fixes, tokens, opts, nodeID, nodeName, heat)
	})

	// Hex frequency feeds usage_count back into fixes.
	for i := range fixes {
		if fixes[i].Property == "fill" {
			fixes[i].UsageCount = hexFreq[fixes[i].Observed]
			fixes[i].Priority = PriorityForFix(fixes[i].Reason, fixes[i].Distance, fixes[i].UsageCount, heat)
		}
	}
	SortFixes(fixes)

	return AuditScreen{
		NodeID:           id,
		Name:             name,
		Slug:             slugify(name),
		Coverage:         cov,
		ComponentSummary: summary,
		Fixes:            fixes,
		ComponentMatches: matches,
		NodeCount:        nodeCount,
	}
}

// walk DFS-traverses node and calls fn on every visited node.
func walk(node map[string]any, fn func(map[string]any)) {
	if node == nil {
		return
	}
	fn(node)
	children, _ := node["children"].([]any)
	for _, ch := range children {
		if cm, ok := ch.(map[string]any); ok {
			walk(cm, fn)
		}
	}
}

func matchComponent(n map[string]any, candidates []DSCandidate, nodeID, nodeName string) ComponentMatch {
	componentID, _ := n["componentId"].(string)
	if componentID == "" {
		componentID, _ = n["componentSetId"].(string)
	}
	in := MatchInput{
		NodeID:       nodeID,
		Name:         nodeName,
		ComponentKey: componentID,
		StyleIDs:     extractStyleIDs(n),
		Colors:       extractFillHexes(n),
	}
	return Match(in, candidates, DefaultMatchingWeights())
}

func auditFills(n map[string]any, cov *TokenCoverage, fixes *[]FixCandidate, hexFreq map[string]int, tokens []DSToken, opts Options, nodeID, nodeName string) {
	fills, _ := n["fills"].([]any)
	for _, f := range fills {
		fm, _ := f.(map[string]any)
		if fm == nil {
			continue
		}
		visible, hasVisible := fm["visible"].(bool)
		if hasVisible && !visible {
			continue
		}
		if t, _ := fm["type"].(string); t != "SOLID" {
			continue
		}
		cov.Fills.Total++

		if isBoundToVariable(fm) {
			cov.Fills.Bound++
			continue
		}
		hex := readSolidFillHex(fm)
		if hex == "" {
			continue
		}
		hexFreq[hex]++
		closest, dist := FindClosestColor(hex, tokens, opts.ColorDriftThreshold)
		// Defensive filter: if a candidate slipped through without a real
		// Figma Variable identity (FigmaName == ""), treat the fill as
		// unbound. The plugin can't bind to a DTCG slug — only to a
		// published variable — so suggesting "base.colour.grey.4a4f52"
		// would just produce an apply-time error toast. Better to surface
		// it as unbound so the designer hand-picks a semantic token.
		if closest != nil && closest.FigmaName == "" {
			closest = nil
		}
		if closest == nil {
			*fixes = append(*fixes, FixCandidate{
				NodeID:   nodeID,
				NodeName: nodeName,
				Property: "fill",
				Observed: hex,
				Reason:   "unbound",
				Distance: 0,
				Priority: PriorityP3,
			})
			continue
		}
		reason := "drift"
		if closest.Deprecated {
			reason = "deprecated"
		}
		*fixes = append(*fixes, FixCandidate{
			NodeID:          nodeID,
			NodeName:        nodeName,
			Property:        "fill",
			Observed:        hex,
			TokenPath:       closest.Path,
			VariableID:      closest.VariableID,
			FigmaName:       closest.FigmaName,
			FigmaCollection: closest.FigmaCollection,
			Distance:        dist,
			Reason:          reason,
			Priority:        PriorityP3, // resolved later with usage_count
			ReplacedBy:      closest.ReplacedBy,
		})
	}
}

func auditTextStyle(n map[string]any, cov *TokenCoverage, fixes *[]FixCandidate, tokens []DSToken, nodeID, nodeName string) {
	if t, _ := n["type"].(string); t != "TEXT" {
		return
	}
	cov.Text.Total++
	styles, _ := n["styles"].(map[string]any)
	if _, hasText := styles["text"]; hasText {
		cov.Text.Bound++
		return
	}
	*fixes = append(*fixes, FixCandidate{
		NodeID:   nodeID,
		NodeName: nodeName,
		Property: "text",
		Observed: "freehand",
		Reason:   "unbound",
		Priority: PriorityP3,
	})
}

func auditDimension(n map[string]any, cov *TokenCoverage, fixes *[]FixCandidate, tokens []DSToken, opts Options, nodeID, nodeName string, heat int) {
	// Spacing — itemSpacing and the four padding fields when autolayout is on.
	if mode, _ := n["layoutMode"].(string); mode != "" && mode != "NONE" {
		if v, ok := n["itemSpacing"].(float64); ok && v > 0 {
			emitSpacingFix(v, "spacing", cov, fixes, tokens, opts, nodeID, nodeName, heat)
		}
		for _, key := range []string{"paddingLeft", "paddingRight", "paddingTop", "paddingBottom"} {
			if v, ok := n[key].(float64); ok && v > 0 {
				emitSpacingFix(v, "padding", cov, fixes, tokens, opts, nodeID, nodeName, heat)
			}
		}
	}
	// Corner radius — top-level cornerRadius only (per-corner overrides v1.1+).
	if cr, ok := n["cornerRadius"].(float64); ok && cr > 0 {
		cov.Radius.Total++
		height := readHeight(n)
		cls := ClassifyRadius(cr, height)
		switch cls.Kind {
		case RadiusOnGrid, RadiusPill:
			cov.Radius.Bound++
		case RadiusOffGrid:
			tokenPath := "radius." + radiusKey(cls.Snapped)
			rationale := cls.Suggestion
			*fixes = append(*fixes, FixCandidate{
				NodeID: nodeID, NodeName: nodeName,
				Property:  "radius",
				Observed:  fmt.Sprintf("%gpx", cr),
				TokenPath: tokenPath,
				Distance:  cls.Distance,
				Reason:    "drift",
				Rationale: rationale,
				Priority:  PriorityForFix("drift", cls.Distance, 1, heat),
			})
		}
	}
}

// readHeight pulls the node's height in px from absoluteBoundingBox or size.
// Returns 0 when neither is available — the radius classifier disables pill
// detection in that case so we don't false-positive.
func readHeight(n map[string]any) float64 {
	if bb, ok := n["absoluteBoundingBox"].(map[string]any); ok {
		if h, ok := bb["height"].(float64); ok {
			return h
		}
	}
	if sz, ok := n["size"].(map[string]any); ok {
		if h, ok := sz["y"].(float64); ok {
			return h
		}
	}
	return 0
}

// emitSpacingFix evaluates one observed spacing/padding value against both
// the published token set (for binding-level coverage) and the 4-pt grid
// (for drift). An off-grid value is drift even when it happens to match
// a token — but the published token set is curated from the grid by U17,
// so in practice the two checks agree.
func emitSpacingFix(v float64, kind string, cov *TokenCoverage, fixes *[]FixCandidate, tokens []DSToken, opts Options, nodeID, nodeName string, heat int) {
	cov.Spacing.Total++
	if IsOnSpacingGrid(v) {
		// On-grid: counts as bound for the coverage roll-up. Token binding
		// (variable id) is the harder ask we still want, but landing on
		// the grid is the strict prerequisite.
		cov.Spacing.Bound++
		return
	}
	snap := SnapSpacing(v)
	rationale := fmt.Sprintf("Off the 4-pt grid — snap to %gpx", snap.Snapped)
	if len(snap.Candidates) > 1 {
		rationale = fmt.Sprintf("Off the 4-pt grid — sits between %v; round up to %gpx", snap.Candidates, snap.Snapped)
	}
	tokenPath := ""
	if tok, _ := FindClosestPx(snap.Snapped, tokens, kind, 0); tok != nil {
		tokenPath = tok.Path
	}
	*fixes = append(*fixes, FixCandidate{
		NodeID: nodeID, NodeName: nodeName,
		Property:  kind,
		Observed:  fmt.Sprintf("%gpx", v),
		TokenPath: tokenPath,
		Distance:  snap.Distance,
		Reason:    "drift",
		Rationale: rationale,
		Priority:  PriorityForFix("drift", snap.Distance, 1, heat),
	})
}

func isBoundToVariable(fm map[string]any) bool {
	bv, _ := fm["boundVariables"].(map[string]any)
	if bv == nil {
		return false
	}
	if _, hasColor := bv["color"]; hasColor {
		return true
	}
	return false
}

func readSolidFillHex(fm map[string]any) string {
	col, _ := fm["color"].(map[string]any)
	if col == nil {
		return ""
	}
	r, _ := col["r"].(float64)
	g, _ := col["g"].(float64)
	b, _ := col["b"].(float64)
	return RGBToHex(r, g, b)
}

func extractStyleIDs(n map[string]any) []string {
	styles, _ := n["styles"].(map[string]any)
	if styles == nil {
		return nil
	}
	ids := make([]string, 0, len(styles))
	for _, v := range styles {
		if s, ok := v.(string); ok && s != "" {
			ids = append(ids, s)
		}
	}
	sort.Strings(ids)
	return ids
}

func extractFillHexes(n map[string]any) []string {
	fills, _ := n["fills"].([]any)
	out := []string{}
	for _, f := range fills {
		fm, _ := f.(map[string]any)
		if fm == nil {
			continue
		}
		if t, _ := fm["type"].(string); t != "SOLID" {
			continue
		}
		if h := readSolidFillHex(fm); h != "" {
			out = append(out, h)
		}
	}
	return out
}

func mostFrequent(m map[string]int) string {
	best := ""
	bestCount := 0
	for k, v := range m {
		if v > bestCount {
			best = k
			bestCount = v
		}
	}
	return best
}

func slugify(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash && b.Len() > 0 {
				b.WriteRune('-')
				prevDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		// Hash fallback so two screens with the same un-slug-able name don't collide.
		sum := sha256.Sum256([]byte(name))
		return "screen-" + hex.EncodeToString(sum[:3])
	}
	return out
}
