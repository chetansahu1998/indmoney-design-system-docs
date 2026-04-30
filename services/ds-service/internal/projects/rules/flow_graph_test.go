package rules

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/projects"
)

// ─── Test fixtures ──────────────────────────────────────────────────────────

type stubFlowGraphLoader struct {
	screens     []ScreenForFlowGraph
	startNodeID string
	loadErr     error
	startErr    error
}

func (s *stubFlowGraphLoader) LoadScreensForFlowGraph(_ context.Context, _ string) ([]ScreenForFlowGraph, error) {
	return s.screens, s.loadErr
}
func (s *stubFlowGraphLoader) LoadStartNodeID(_ context.Context, _ string) (string, error) {
	return s.startNodeID, s.startErr
}

type stubLinkStore struct {
	cached       []projects.PrototypeLink
	getErr       error
	upsertErr    error
	upsertCalls  int
	upsertedRows []projects.PrototypeLink
}

func (s *stubLinkStore) GetPrototypeLinks(_ context.Context, _ string) ([]projects.PrototypeLink, error) {
	return s.cached, s.getErr
}
func (s *stubLinkStore) UpsertPrototypeLinks(_ context.Context, links []projects.PrototypeLink) error {
	if s.upsertErr != nil {
		return s.upsertErr
	}
	s.upsertCalls++
	s.upsertedRows = append(s.upsertedRows, links...)
	return nil
}

type stubFetcher struct {
	links     []projects.PrototypeLink
	err       error
	callCount int
}

func (f *stubFetcher) FetchLinks(_ context.Context, _ string, _ map[string]string) ([]projects.PrototypeLink, error) {
	f.callCount++
	return f.links, f.err
}

// mkTreeNode builds a canonical_tree JSON blob with the given root id + name.
func mkTreeNode(id, name string) string {
	b, _ := json.Marshal(map[string]any{"id": id, "name": name, "type": "FRAME"})
	return string(b)
}

func mkScreen(screenID, flowID, name string) ScreenForFlowGraph {
	return ScreenForFlowGraph{
		ScreenID:      screenID,
		FlowID:        flowID,
		FileID:        "file-A",
		CanonicalTree: mkTreeNode("node-"+screenID, name),
	}
}

func mkLink(screenID, dst string, action string) projects.PrototypeLink {
	d := dst
	return projects.PrototypeLink{
		ID:                  "link-" + screenID + "-" + dst,
		ScreenID:            screenID,
		TenantID:            "tenant-1",
		SourceNodeID:        "btn-" + screenID,
		DestinationScreenID: &d,
		Trigger:             "ON_CLICK",
		Action:              action,
	}
}

func runRule(t *testing.T, screens []ScreenForFlowGraph, links []projects.PrototypeLink) []projects.Violation {
	t.Helper()
	loader := &stubFlowGraphLoader{screens: screens}
	store := &stubLinkStore{cached: links}
	r := NewFlowGraphRunner(loader, store, &stubFetcher{})
	v := &projects.ProjectVersion{ID: "v-1", TenantID: "tenant-1"}
	out, err := r.Run(context.Background(), v)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return out
}

func ruleIDs(viols []projects.Violation) []string {
	out := make([]string, len(viols))
	for i, v := range viols {
		out[i] = v.RuleID
	}
	return out
}

func countByRuleID(viols []projects.Violation, ruleID string) int {
	c := 0
	for _, v := range viols {
		if v.RuleID == ruleID {
			c++
		}
	}
	return c
}

// ─── Happy paths ────────────────────────────────────────────────────────────

func TestFlowGraph_LinearFlow_NoViolations(t *testing.T) {
	// A → B → C → D → E. 4 links, 5 screens. Density >= 0.5 → full run.
	screens := []ScreenForFlowGraph{
		mkScreen("s-A", "f-1", "Start"),
		mkScreen("s-B", "f-1", "Middle 1"),
		mkScreen("s-C", "f-1", "Middle 2"),
		mkScreen("s-D", "f-1", "Middle 3"),
		mkScreen("s-E", "f-1", "Success"),
	}
	links := []projects.PrototypeLink{
		mkLink("s-A", "s-B", "NAVIGATE"),
		mkLink("s-B", "s-C", "NAVIGATE"),
		mkLink("s-C", "s-D", "NAVIGATE"),
		mkLink("s-D", "s-E", "NAVIGATE"),
	}
	out := runRule(t, screens, links)
	if len(out) != 0 {
		t.Errorf("linear flow: expected 0 violations, got %d (%v)", len(out), ruleIDs(out))
	}
}

func TestFlowGraph_BranchingWithSuccessTerminus_NoViolations(t *testing.T) {
	// A → B (Success). B has zero out-edges but its name is in the terminus
	// allowlist — no dead-end violation.
	screens := []ScreenForFlowGraph{
		mkScreen("s-A", "f-1", "Form"),
		mkScreen("s-B", "f-1", "Success"),
	}
	links := []projects.PrototypeLink{mkLink("s-A", "s-B", "NAVIGATE")}
	out := runRule(t, screens, links)
	if countByRuleID(out, "flow_graph_dead_end") != 0 {
		t.Errorf("Success terminus should not produce dead-end: %v", ruleIDs(out))
	}
}

func TestFlowGraph_CaseInsensitiveTerminusMatch(t *testing.T) {
	// "success" lowercase still matches.
	screens := []ScreenForFlowGraph{
		mkScreen("s-A", "f-1", "Form"),
		mkScreen("s-B", "f-1", "success"),
	}
	links := []projects.PrototypeLink{mkLink("s-A", "s-B", "NAVIGATE")}
	out := runRule(t, screens, links)
	if countByRuleID(out, "flow_graph_dead_end") != 0 {
		t.Errorf("case-insensitive terminus should not produce dead-end: %v", ruleIDs(out))
	}
}

// ─── Error paths ────────────────────────────────────────────────────────────

func TestFlowGraph_OrphanScreen(t *testing.T) {
	// s-Orphan has no inbound edges and is not the start.
	screens := []ScreenForFlowGraph{
		mkScreen("s-A", "f-1", "Start"),
		mkScreen("s-B", "f-1", "Middle"),
		mkScreen("s-C", "f-1", "Success"),
		mkScreen("s-Orphan", "f-1", "Unreachable"),
	}
	links := []projects.PrototypeLink{
		mkLink("s-A", "s-B", "NAVIGATE"),
		mkLink("s-B", "s-C", "NAVIGATE"),
	}
	out := runRule(t, screens, links)
	if countByRuleID(out, "flow_graph_orphan") != 1 {
		t.Errorf("expected 1 orphan, got: %v", ruleIDs(out))
	}
	for _, v := range out {
		if v.RuleID == "flow_graph_orphan" {
			if v.ScreenID != "s-Orphan" {
				t.Errorf("orphan attribution: got screen %q, want s-Orphan", v.ScreenID)
			}
			if v.Severity != projects.SeverityMedium {
				t.Errorf("orphan severity: got %q, want medium", v.Severity)
			}
			if v.Category != "flow_graph" {
				t.Errorf("orphan category: got %q, want flow_graph", v.Category)
			}
		}
	}
}

func TestFlowGraph_DeadEnd(t *testing.T) {
	// "Tax Form" has zero outbound and is NOT a recognized terminus.
	screens := []ScreenForFlowGraph{
		mkScreen("s-A", "f-1", "Start"),
		mkScreen("s-B", "f-1", "Tax Form"),
	}
	links := []projects.PrototypeLink{mkLink("s-A", "s-B", "NAVIGATE")}
	out := runRule(t, screens, links)
	if countByRuleID(out, "flow_graph_dead_end") != 1 {
		t.Errorf("expected 1 dead-end, got: %v", ruleIDs(out))
	}
	for _, v := range out {
		if v.RuleID == "flow_graph_dead_end" {
			if v.Severity != projects.SeverityMedium {
				t.Errorf("dead-end severity: got %q, want medium", v.Severity)
			}
		}
	}
}

func TestFlowGraph_CycleWithoutExit(t *testing.T) {
	// A → B → A loop, no edges out of the cycle.
	screens := []ScreenForFlowGraph{
		mkScreen("s-A", "f-1", "A"),
		mkScreen("s-B", "f-1", "B"),
	}
	links := []projects.PrototypeLink{
		mkLink("s-A", "s-B", "NAVIGATE"),
		mkLink("s-B", "s-A", "NAVIGATE"),
	}
	out := runRule(t, screens, links)
	if countByRuleID(out, "flow_graph_cycle") != 1 {
		t.Errorf("expected 1 cycle violation, got: %v", ruleIDs(out))
	}
	for _, v := range out {
		if v.RuleID == "flow_graph_cycle" {
			if v.Severity != projects.SeverityHigh {
				t.Errorf("cycle severity: got %q, want high", v.Severity)
			}
		}
	}
}

func TestFlowGraph_CycleWithExit_NoViolation(t *testing.T) {
	// A → B → C → A but C also has C → D (exit). Density: 4 links / 4 screens
	// = 1.0 → not sparse. No cycle violation expected.
	screens := []ScreenForFlowGraph{
		mkScreen("s-A", "f-1", "A"),
		mkScreen("s-B", "f-1", "B"),
		mkScreen("s-C", "f-1", "C"),
		mkScreen("s-D", "f-1", "Success"),
	}
	links := []projects.PrototypeLink{
		mkLink("s-A", "s-B", "NAVIGATE"),
		mkLink("s-B", "s-C", "NAVIGATE"),
		mkLink("s-C", "s-A", "NAVIGATE"),
		mkLink("s-C", "s-D", "NAVIGATE"),
	}
	out := runRule(t, screens, links)
	if countByRuleID(out, "flow_graph_cycle") != 0 {
		t.Errorf("cycle-with-exit should not violate: %v", ruleIDs(out))
	}
}

func TestFlowGraph_MissingStateCoverage(t *testing.T) {
	// Loading screen present, no Empty/Error siblings. Density: 2/3 → not sparse.
	screens := []ScreenForFlowGraph{
		mkScreen("s-A", "f-1", "Profile"),
		mkScreen("s-L", "f-1", "Loading"),
		mkScreen("s-B", "f-1", "Success"),
	}
	links := []projects.PrototypeLink{
		mkLink("s-A", "s-L", "NAVIGATE"),
		mkLink("s-L", "s-B", "NAVIGATE"),
	}
	out := runRule(t, screens, links)
	if countByRuleID(out, "flow_graph_missing_state_coverage") != 1 {
		t.Errorf("expected 1 missing_state_coverage, got: %v", ruleIDs(out))
	}
	for _, v := range out {
		if v.RuleID == "flow_graph_missing_state_coverage" {
			if v.Severity != projects.SeverityLow {
				t.Errorf("state-coverage severity: got %q, want low", v.Severity)
			}
		}
	}
}

func TestFlowGraph_StateCoverage_PresentEmpty_NoViolation(t *testing.T) {
	// Loading + EmptyState peer present → no violation.
	screens := []ScreenForFlowGraph{
		mkScreen("s-A", "f-1", "Profile"),
		mkScreen("s-L", "f-1", "Loading"),
		mkScreen("s-E", "f-1", "EmptyState"),
		mkScreen("s-D", "f-1", "Success"),
	}
	links := []projects.PrototypeLink{
		mkLink("s-A", "s-L", "NAVIGATE"),
		mkLink("s-L", "s-E", "NAVIGATE"),
		mkLink("s-E", "s-D", "NAVIGATE"),
	}
	out := runRule(t, screens, links)
	if countByRuleID(out, "flow_graph_missing_state_coverage") != 0 {
		t.Errorf("Empty/Error present should not violate: %v", ruleIDs(out))
	}
}

// ─── Sparse fallback ────────────────────────────────────────────────────────

func TestFlowGraph_SparseProtoData_OnlyStateCoverageRuns(t *testing.T) {
	// 5 screens, 1 link → density 0.2 < 0.5 → sparse. Only state-coverage runs;
	// no orphan/dead-end/cycle. Plus one Info `flow_graph_skipped`.
	screens := []ScreenForFlowGraph{
		mkScreen("s-A", "f-1", "Start"),
		mkScreen("s-B", "f-1", "Middle"),
		mkScreen("s-C", "f-1", "Loading"),
		mkScreen("s-D", "f-1", "End"),
		mkScreen("s-E", "f-1", "Other"),
	}
	links := []projects.PrototypeLink{mkLink("s-A", "s-B", "NAVIGATE")}
	out := runRule(t, screens, links)

	if countByRuleID(out, "flow_graph_skipped") != 1 {
		t.Errorf("expected exactly 1 flow_graph_skipped Info, got: %v", ruleIDs(out))
	}
	if countByRuleID(out, "flow_graph_orphan") != 0 {
		t.Errorf("sparse: orphan rule must not run: %v", ruleIDs(out))
	}
	if countByRuleID(out, "flow_graph_dead_end") != 0 {
		t.Errorf("sparse: dead-end rule must not run: %v", ruleIDs(out))
	}
	if countByRuleID(out, "flow_graph_cycle") != 0 {
		t.Errorf("sparse: cycle rule must not run: %v", ruleIDs(out))
	}
	if countByRuleID(out, "flow_graph_missing_state_coverage") != 1 {
		t.Errorf("sparse: state-coverage must still run, got: %v", ruleIDs(out))
	}
	for _, v := range out {
		if v.RuleID == "flow_graph_skipped" && v.Severity != projects.SeverityInfo {
			t.Errorf("skipped severity: got %q, want info", v.Severity)
		}
	}
}

// ─── Cache-hit / fetcher ────────────────────────────────────────────────────

func TestFlowGraph_CacheHit_NoFetcherCalls(t *testing.T) {
	screens := []ScreenForFlowGraph{
		mkScreen("s-A", "f-1", "A"),
		mkScreen("s-B", "f-1", "Success"),
	}
	cached := []projects.PrototypeLink{mkLink("s-A", "s-B", "NAVIGATE")}
	loader := &stubFlowGraphLoader{screens: screens}
	store := &stubLinkStore{cached: cached}
	fetcher := &stubFetcher{}
	r := NewFlowGraphRunner(loader, store, fetcher)
	if _, err := r.Run(context.Background(), &projects.ProjectVersion{ID: "v-1", TenantID: "tenant-1"}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if fetcher.callCount != 0 {
		t.Errorf("cache hit should not call fetcher: count=%d", fetcher.callCount)
	}
	if store.upsertCalls != 0 {
		t.Errorf("cache hit should not upsert: count=%d", store.upsertCalls)
	}
}

func TestFlowGraph_CacheMiss_FetcherCalled_LinksUpserted(t *testing.T) {
	screens := []ScreenForFlowGraph{
		mkScreen("s-A", "f-1", "A"),
		mkScreen("s-B", "f-1", "Success"),
	}
	loader := &stubFlowGraphLoader{screens: screens}
	store := &stubLinkStore{cached: nil} // miss
	fetcher := &stubFetcher{links: []projects.PrototypeLink{mkLink("s-A", "s-B", "NAVIGATE")}}
	r := NewFlowGraphRunner(loader, store, fetcher)
	if _, err := r.Run(context.Background(), &projects.ProjectVersion{ID: "v-1", TenantID: "tenant-1"}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if fetcher.callCount != 1 {
		t.Errorf("cache miss should call fetcher once: count=%d", fetcher.callCount)
	}
	if store.upsertCalls != 1 {
		t.Errorf("cache miss should upsert: count=%d", store.upsertCalls)
	}
}

func TestFlowGraph_FetcherError_Propagates(t *testing.T) {
	screens := []ScreenForFlowGraph{
		mkScreen("s-A", "f-1", "A"),
		mkScreen("s-B", "f-1", "Success"),
	}
	loader := &stubFlowGraphLoader{screens: screens}
	store := &stubLinkStore{cached: nil}
	fetcher := &stubFetcher{err: errors.New("figma 503")}
	r := NewFlowGraphRunner(loader, store, fetcher)
	_, err := r.Run(context.Background(), &projects.ProjectVersion{ID: "v-1", TenantID: "tenant-1"})
	if err == nil || !strings.Contains(err.Error(), "figma 503") {
		t.Errorf("expected fetcher error wrapped, got: %v", err)
	}
}

// ─── Guards ─────────────────────────────────────────────────────────────────

func TestFlowGraph_NilVersion(t *testing.T) {
	r := NewFlowGraphRunner(&stubFlowGraphLoader{}, &stubLinkStore{}, NoopPrototypeFetcher())
	_, err := r.Run(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error on nil version")
	}
}

func TestFlowGraph_EmptyScreens_NoViolations(t *testing.T) {
	loader := &stubFlowGraphLoader{screens: nil}
	store := &stubLinkStore{}
	r := NewFlowGraphRunner(loader, store, NoopPrototypeFetcher())
	out, err := r.Run(context.Background(), &projects.ProjectVersion{ID: "v-1", TenantID: "tenant-1"})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("zero screens: expected zero violations, got %d", len(out))
	}
}

// ─── Multi-flow per version ─────────────────────────────────────────────────

func TestFlowGraph_MultiFlow_PerFlowIsolation(t *testing.T) {
	// Two flows in the same version. Flow A is clean linear; Flow B has a
	// cycle. The cycle in B should not bleed into A.
	screens := []ScreenForFlowGraph{
		mkScreen("a1", "f-A", "Start"),
		mkScreen("a2", "f-A", "Success"),
		mkScreen("b1", "f-B", "X"),
		mkScreen("b2", "f-B", "Y"),
	}
	links := []projects.PrototypeLink{
		mkLink("a1", "a2", "NAVIGATE"),
		mkLink("b1", "b2", "NAVIGATE"),
		mkLink("b2", "b1", "NAVIGATE"),
	}
	out := runRule(t, screens, links)
	if countByRuleID(out, "flow_graph_cycle") != 1 {
		t.Errorf("expected exactly 1 cycle (in flow B), got: %v", ruleIDs(out))
	}
	// Flow A must not have any orphan/dead-end/cycle violations.
	for _, v := range out {
		if v.ScreenID == "a1" || v.ScreenID == "a2" {
			if strings.HasPrefix(v.RuleID, "flow_graph_") &&
				v.RuleID != "flow_graph_skipped" {
				t.Errorf("flow A should be clean; got %s on %s", v.RuleID, v.ScreenID)
			}
		}
	}
}

// ─── Compile-time RuleRunner conformance ────────────────────────────────────

var _ projects.RuleRunner = (*FlowGraphRunner)(nil)
