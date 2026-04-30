// Package rules — Phase 2 U5 — Flow-graph rule.
//
// Catches reachability and state-coverage problems that emerge from the prototype
// graph of a flow:
//   - orphan        — screen unreachable from the start node (Medium)
//   - dead_end      — out-degree zero AND name not a recognized terminus (Medium)
//   - cycle         — strongly-connected component with no exit edge (High)
//   - missing_state — Loading screen without an Empty/Error sibling (Low)
//
// Sparse-prototype fallback: if the link/screen ratio is below 0.5, the rule
// emits a single Info `flow_graph_skipped` and runs ONLY missing-state-coverage
// (the other three would produce noise on flows where prototype links were
// never wired up in Figma — common in early design exploration).
//
// Production data path:
//
//	repo.GetPrototypeLinks(versionID)            // cache hit?
//	  └─ empty → fetcher.FetchLinks(...)         // Figma REST round-trip
//	             └─ repo.UpsertPrototypeLinks    // populate cache
//	  └─ non-empty → use directly
//
// The runner is stateless w.r.t. Figma — the fetcher is the only IO surface
// and it can be swapped for tests via the FlowGraphFetcher interface.

package rules

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/projects"
)

// ─── Loaders + dependencies ─────────────────────────────────────────────────

// FlowGraphLoader is the data-access surface the runner needs. The production
// impl wraps TenantRepo (TODO(U5-prod-wire)); tests use stubLoader.
type FlowGraphLoader interface {
	// LoadScreensForFlowGraph returns one row per screen in the version with the
	// canonical_tree blob included so the rule can extract the screen's name +
	// scan for Loading/Empty/Error markers without a second query.
	LoadScreensForFlowGraph(ctx context.Context, versionID string) ([]ScreenForFlowGraph, error)

	// LoadStartNodeID returns the prototype start node for a Figma file. Empty
	// string + nil error means "unknown" — the runner falls back to the first
	// screen in created_at order.
	LoadStartNodeID(ctx context.Context, fileID string) (string, error)
}

// ScreenForFlowGraph collects the per-screen inputs the rule needs.
type ScreenForFlowGraph struct {
	ScreenID      string
	FlowID        string
	FileID        string // file_id of the parent flow; needed by LoadStartNodeID
	CanonicalTree string // JSON; the rule extracts root.name from here
}

// FlowGraphLinkStore is the cache surface the runner needs. The production
// impl is *TenantRepo (which already exposes GetPrototypeLinks +
// UpsertPrototypeLinks); tests use a stub.
type FlowGraphLinkStore interface {
	GetPrototypeLinks(ctx context.Context, versionID string) ([]projects.PrototypeLink, error)
	UpsertPrototypeLinks(ctx context.Context, links []projects.PrototypeLink) error
}

// ─── Runner ─────────────────────────────────────────────────────────────────

// FlowGraphRunner implements projects.RuleRunner.
type FlowGraphRunner struct {
	loader  FlowGraphLoader
	links   FlowGraphLinkStore
	fetcher PrototypeFetcher
}

// NewFlowGraphRunner builds the runner. Pass NoopPrototypeFetcher() if the real
// Figma fetcher isn't wired yet — the runner will fall into sparse-prototype
// mode and only emit state-coverage findings.
func NewFlowGraphRunner(loader FlowGraphLoader, links FlowGraphLinkStore, fetcher PrototypeFetcher) *FlowGraphRunner {
	if fetcher == nil {
		fetcher = NoopPrototypeFetcher()
	}
	return &FlowGraphRunner{loader: loader, links: links, fetcher: fetcher}
}

// Run implements projects.RuleRunner. Returns the violations the rule produced.
// Never persists — the worker owns the DELETE-then-INSERT transaction.
func (r *FlowGraphRunner) Run(ctx context.Context, v *projects.ProjectVersion) ([]projects.Violation, error) {
	if v == nil {
		return nil, errors.New("flow_graph: nil version")
	}
	if r.loader == nil {
		return nil, errors.New("flow_graph: nil loader")
	}
	if r.links == nil {
		return nil, errors.New("flow_graph: nil link store")
	}

	screens, err := r.loader.LoadScreensForFlowGraph(ctx, v.ID)
	if err != nil {
		return nil, fmt.Errorf("flow_graph: load screens: %w", err)
	}
	if len(screens) == 0 {
		return nil, nil
	}

	// Cache hit/miss: look in the prototype-link cache first; on miss, fetch
	// from Figma and upsert. We attribute fetched destinations back to local
	// screens via the figmaNodeID → screenID map below.
	cached, err := r.links.GetPrototypeLinks(ctx, v.ID)
	if err != nil {
		return nil, fmt.Errorf("flow_graph: get prototype_links: %w", err)
	}
	links := cached
	if len(cached) == 0 {
		// Fetch attempt. The map is keyed by Figma node_id (which is the
		// canonical_tree's root id) and maps to our local screen_id.
		fileID := ""
		nodeIDToScreen := map[string]string{}
		for _, s := range screens {
			if fileID == "" {
				fileID = s.FileID
			}
			if rootID, ok := rootNodeID(s.CanonicalTree); ok {
				nodeIDToScreen[rootID] = s.ScreenID
			}
		}
		fetched, ferr := r.fetcher.FetchLinks(ctx, fileID, nodeIDToScreen)
		if ferr != nil {
			return nil, fmt.Errorf("flow_graph: fetch prototype_links: %w", ferr)
		}
		if len(fetched) > 0 {
			if err := r.links.UpsertPrototypeLinks(ctx, fetched); err != nil {
				return nil, fmt.Errorf("flow_graph: upsert prototype_links: %w", err)
			}
		}
		links = fetched
	}

	// ─── Sparse-prototype fallback ───────────────────────────────────────────
	// Threshold: if average outgoing edges per screen < 0.5, prototype data is
	// too sparse for orphan/dead-end/cycle to be meaningful. Run state-coverage
	// only and emit one Info per flow so the UI can hint.
	sparse := false
	if float64(len(links))/float64(len(screens)) < 0.5 {
		sparse = true
	}

	var out []projects.Violation
	flows := groupScreensByFlow(screens)

	if sparse {
		for _, flow := range flows {
			out = append(out, missingStateCoverage(flow, v.ID, v.TenantID)...)
			out = append(out, sparseSkipMarker(flow, v.ID, v.TenantID))
		}
		return out, nil
	}

	// Full run.
	startID, err := r.loader.LoadStartNodeID(ctx, screens[0].FileID)
	if err != nil {
		return nil, fmt.Errorf("flow_graph: load start_node: %w", err)
	}
	for _, flow := range flows {
		out = append(out, runFullFlowGraph(flow, links, startID, v.ID, v.TenantID)...)
		out = append(out, missingStateCoverage(flow, v.ID, v.TenantID)...)
	}
	return out, nil
}

// ─── Per-flow logic ─────────────────────────────────────────────────────────

type flowScreens struct {
	flowID  string
	screens []ScreenForFlowGraph
}

func groupScreensByFlow(screens []ScreenForFlowGraph) []flowScreens {
	byFlow := map[string][]ScreenForFlowGraph{}
	order := []string{}
	for _, s := range screens {
		if _, ok := byFlow[s.FlowID]; !ok {
			order = append(order, s.FlowID)
		}
		byFlow[s.FlowID] = append(byFlow[s.FlowID], s)
	}
	out := make([]flowScreens, 0, len(order))
	for _, fid := range order {
		out = append(out, flowScreens{flowID: fid, screens: byFlow[fid]})
	}
	return out
}

// terminusPattern matches names that legitimately have zero out-edges (the user
// has reached an end-state). Case-insensitive whole-name match.
var terminusPattern = regexp.MustCompile(`(?i)^(success|confirmation|done|thank you|error)$`)

// loadingPattern matches Loading / Skeleton screen-name markers.
var loadingPattern = regexp.MustCompile(`(?i)\b(loading|skeleton)\b`)

// emptyOrErrorPattern matches Empty / Error / EmptyState / ErrorState markers.
var emptyOrErrorPattern = regexp.MustCompile(`(?i)\b(empty|empty[\s_-]?state|error[\s_-]?state)\b`)

func runFullFlowGraph(flow flowScreens, links []projects.PrototypeLink, startNodeID, versionID, tenantID string) []projects.Violation {
	// Build screen_id → name + adjacency.
	screenNames := map[string]string{}
	allScreenIDs := map[string]struct{}{}
	for _, s := range flow.screens {
		allScreenIDs[s.ScreenID] = struct{}{}
		screenNames[s.ScreenID] = rootName(s.CanonicalTree)
	}

	// Adjacency: only edges where BOTH source and destination are in this flow.
	adjOut := map[string][]string{}
	adjIn := map[string]int{}
	for _, l := range links {
		if _, ok := allScreenIDs[l.ScreenID]; !ok {
			continue
		}
		if l.DestinationScreenID == nil {
			continue
		}
		dst := *l.DestinationScreenID
		if _, ok := allScreenIDs[dst]; !ok {
			continue
		}
		if dst == l.ScreenID {
			// Self-loop counts as an in-edge for cycle/exit purposes.
			adjOut[l.ScreenID] = append(adjOut[l.ScreenID], dst)
			adjIn[dst]++
			continue
		}
		adjOut[l.ScreenID] = append(adjOut[l.ScreenID], dst)
		adjIn[dst]++
	}

	// Pick start node — try the file's prototypeStartNodeID; if that maps to
	// one of our screens, use it. Otherwise fall back to the first screen.
	var startScreen string
	if startNodeID != "" {
		// startNodeID is a Figma node_id; match against root node ids.
		for _, s := range flow.screens {
			if rid, ok := rootNodeID(s.CanonicalTree); ok && rid == startNodeID {
				startScreen = s.ScreenID
				break
			}
		}
	}
	if startScreen == "" && len(flow.screens) > 0 {
		startScreen = flow.screens[0].ScreenID
	}

	var out []projects.Violation

	// Orphans: BFS from start; any unreached screen != start = orphan.
	reached := bfs(adjOut, startScreen)
	for _, s := range flow.screens {
		if s.ScreenID == startScreen {
			continue
		}
		if !reached[s.ScreenID] {
			out = append(out, mkViolation(versionID, tenantID, s.ScreenID,
				"flow_graph_orphan", projects.SeverityMedium,
				"reachability", "Screen has no inbound edges from start",
				"Add a prototype connection from another screen, or remove if unused.",
			))
		}
	}

	// Dead-ends: zero out-edges AND name not a recognized terminus.
	for _, s := range flow.screens {
		if len(adjOut[s.ScreenID]) > 0 {
			continue
		}
		if terminusPattern.MatchString(strings.TrimSpace(screenNames[s.ScreenID])) {
			continue
		}
		out = append(out, mkViolation(versionID, tenantID, s.ScreenID,
			"flow_graph_dead_end", projects.SeverityMedium,
			"out_edges", "Screen has zero outbound prototype connections",
			"Add a prototype connection to the next screen, or rename to a terminus (Success / Confirmation / Done / Error).",
		))
	}

	// Cycles without exit: SCCs of size >=2 OR self-loops where the node has no
	// out-edge leaving its SCC.
	sccs := tarjanSCC(adjOut, allScreenIDs)
	for _, scc := range sccs {
		if len(scc) < 2 {
			// Size-1 SCC: only flag if the only edge is a self-loop.
			node := scc[0]
			if !hasSelfLoop(adjOut, node) {
				continue
			}
			// self-loop with no other out-edge → cycle without exit
			if len(adjOut[node]) == 1 {
				out = append(out, mkViolation(versionID, tenantID, node,
					"flow_graph_cycle", projects.SeverityHigh,
					"reachability", "Screen self-loops with no exit",
					"Add an exit edge from the cycle.",
				))
			}
			continue
		}
		// Size >= 2 SCC. Check if any node has an edge leaving the SCC.
		sccSet := map[string]struct{}{}
		for _, n := range scc {
			sccSet[n] = struct{}{}
		}
		hasExit := false
		for _, n := range scc {
			for _, dst := range adjOut[n] {
				if _, in := sccSet[dst]; !in {
					hasExit = true
					break
				}
			}
			if hasExit {
				break
			}
		}
		if !hasExit {
			// One violation per SCC, attributed to the first node by ID.
			for _, n := range scc {
				out = append(out, mkViolation(versionID, tenantID, n,
					"flow_graph_cycle", projects.SeverityHigh,
					"reachability", "Screen is part of a cycle without exit",
					"Add an exit edge from the cycle so the user can leave.",
				))
				break
			}
		}
	}

	return out
}

// missingStateCoverage looks at screen names: if any name matches Loading|Skeleton
// AND no name in the flow matches Empty|EmptyState|Error|ErrorState, emit Low.
func missingStateCoverage(flow flowScreens, versionID, tenantID string) []projects.Violation {
	hasLoading := false
	hasEmptyOrError := false
	var loadingScreen string
	for _, s := range flow.screens {
		name := rootName(s.CanonicalTree)
		if loadingPattern.MatchString(name) {
			hasLoading = true
			if loadingScreen == "" {
				loadingScreen = s.ScreenID
			}
		}
		if emptyOrErrorPattern.MatchString(name) {
			hasEmptyOrError = true
		}
	}
	if !hasLoading || hasEmptyOrError {
		return nil
	}
	return []projects.Violation{
		mkViolation(versionID, tenantID, loadingScreen,
			"flow_graph_missing_state_coverage", projects.SeverityLow,
			"state_coverage", "Loading screen present without Empty or Error siblings",
			"Add screens for Empty and Error states alongside the Loading state.",
		),
	}
}

// sparseSkipMarker emits one Info violation per flow when the prototype data is
// too sparse for the structural rules to be meaningful. UI can render a hint.
func sparseSkipMarker(flow flowScreens, versionID, tenantID string) projects.Violation {
	screenID := ""
	if len(flow.screens) > 0 {
		screenID = flow.screens[0].ScreenID
	}
	return mkViolation(versionID, tenantID, screenID,
		"flow_graph_skipped", projects.SeverityInfo,
		"prototype_density", "Prototype connection density too low for structural rules (<0.5 edges/screen)",
		"Wire prototype connections in Figma so the orphan / dead-end / cycle checks can run.",
	)
}

// ─── Helpers ────────────────────────────────────────────────────────────────

func mkViolation(versionID, tenantID, screenID, ruleID, severity, property, observed, suggestion string) projects.Violation {
	return projects.Violation{
		VersionID:   versionID,
		TenantID:    tenantID,
		ScreenID:    screenID,
		RuleID:      ruleID,
		Severity:    severity,
		Category:    "flow_graph",
		Property:    property,
		Observed:    observed,
		Suggestion:  suggestion,
		Status:      "active",
		AutoFixable: false,
	}
}

func bfs(adj map[string][]string, start string) map[string]bool {
	reached := map[string]bool{}
	if start == "" {
		return reached
	}
	queue := []string{start}
	reached[start] = true
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		for _, dst := range adj[n] {
			if !reached[dst] {
				reached[dst] = true
				queue = append(queue, dst)
			}
		}
	}
	return reached
}

func hasSelfLoop(adj map[string][]string, node string) bool {
	for _, dst := range adj[node] {
		if dst == node {
			return true
		}
	}
	return false
}

// tarjanSCC computes strongly-connected components via Tarjan's algorithm.
// Returns SCCs in reverse-topological order (leaves first). nodes is the full
// set of vertices (we want SCCs even for nodes with zero out-edges so they
// appear as size-1 SCCs).
func tarjanSCC(adj map[string][]string, nodes map[string]struct{}) [][]string {
	type state struct {
		index, lowlink int
		onStack        bool
		visited        bool
	}
	st := map[string]*state{}
	for n := range nodes {
		st[n] = &state{}
	}
	var stack []string
	var sccs [][]string
	idx := 0

	var strongconnect func(v string)
	strongconnect = func(v string) {
		s := st[v]
		s.index = idx
		s.lowlink = idx
		s.visited = true
		idx++
		stack = append(stack, v)
		s.onStack = true

		for _, w := range adj[v] {
			ws, ok := st[w]
			if !ok {
				continue
			}
			if !ws.visited {
				strongconnect(w)
				if ws.lowlink < s.lowlink {
					s.lowlink = ws.lowlink
				}
			} else if ws.onStack {
				if ws.index < s.lowlink {
					s.lowlink = ws.index
				}
			}
		}

		if s.lowlink == s.index {
			var scc []string
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				st[w].onStack = false
				scc = append(scc, w)
				if w == v {
					break
				}
			}
			sccs = append(sccs, scc)
		}
	}

	// Deterministic order: visit nodes sorted by id so SCC-output is stable.
	ordered := make([]string, 0, len(nodes))
	for n := range nodes {
		ordered = append(ordered, n)
	}
	// simple sort to keep determinism
	for i := 1; i < len(ordered); i++ {
		for j := i; j > 0 && ordered[j-1] > ordered[j]; j-- {
			ordered[j-1], ordered[j] = ordered[j], ordered[j-1]
		}
	}
	for _, n := range ordered {
		if !st[n].visited {
			strongconnect(n)
		}
	}
	return sccs
}

// ─── Canonical-tree extraction ──────────────────────────────────────────────

// rootName returns the root node's `name` field from a canonical_tree JSON
// blob. Empty string on any decode/missing-field path (defensive).
func rootName(canonicalTree string) string {
	tree := decodeTree(canonicalTree)
	if tree == nil {
		return ""
	}
	if name, ok := tree["name"].(string); ok {
		return name
	}
	return ""
}

// rootNodeID returns the root node's `id` field. Used to attribute Figma
// node_ids back to local screen_ids when the prototype fetch returns
// destinations keyed by node_id.
func rootNodeID(canonicalTree string) (string, bool) {
	tree := decodeTree(canonicalTree)
	if tree == nil {
		return "", false
	}
	if id, ok := tree["id"].(string); ok && id != "" {
		return id, true
	}
	return "", false
}
