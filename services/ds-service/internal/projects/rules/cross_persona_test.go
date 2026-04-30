package rules

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/projects"
)

// ─── In-memory loader ────────────────────────────────────────────────────────

// stubLoader is a test-only FlowsByProjectLoader that returns a pre-built
// slice of FlowWithPersona. The test-fixtures synthesize canonical_tree JSON
// strings directly so we don't need a database round-trip.
type stubLoader struct {
	flows []FlowWithPersona
	err   error
}

func (s *stubLoader) LoadFlowsForProjectVersion(_ context.Context, _, _ string) ([]FlowWithPersona, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.flows, nil
}

// ─── Tree-builder helpers ────────────────────────────────────────────────────

// instanceNode renders an INSTANCE node fragment with the given component-set
// key + name. Either or both of these may be empty to model degraded fixtures.
type instanceNode struct {
	name             string
	componentSetKey  string
	componentSetID   string
	componentID      string
	mainComponentKey string
}

func (n instanceNode) toMap() map[string]any {
	m := map[string]any{
		"type": "INSTANCE",
		"name": n.name,
	}
	if n.mainComponentKey != "" {
		m["mainComponent"] = map[string]any{
			"componentSetKey": n.mainComponentKey,
			"name":            n.name,
		}
	} else if n.componentSetKey != "" {
		m["componentSetKey"] = n.componentSetKey
	}
	if n.componentSetID != "" {
		m["componentSetId"] = n.componentSetID
	}
	if n.componentID != "" {
		m["componentId"] = n.componentID
	}
	return m
}

// frameTree wraps the given children in a top-level FRAME node, encoding to
// the JSON string ScreenWithTree.CanonicalTree expects.
func frameTree(children ...map[string]any) string {
	doc := map[string]any{
		"type":     "FRAME",
		"name":     "Root",
		"children": children,
	}
	b, _ := json.Marshal(doc)
	return string(b)
}

// instanceTree builds a one-screen tree containing instance nodes, each
// identified by a single componentSetKey. Helper for the common test shape.
func instanceTree(setKeys ...string) string {
	children := make([]map[string]any, 0, len(setKeys))
	for _, k := range setKeys {
		children = append(children, instanceNode{
			name:             k, // simple convention: name == key in tests
			mainComponentKey: k,
		}.toMap())
	}
	return frameTree(children...)
}

// makeFlow builds a FlowWithPersona with a single screen carrying the given
// canonical tree. Empty personaName indicates a single-persona flow.
func makeFlow(flowID, personaID, personaName, canonicalTree string) FlowWithPersona {
	var personaPtr *string
	if personaID != "" {
		p := personaID
		personaPtr = &p
	}
	return FlowWithPersona{
		FlowID:      flowID,
		PersonaID:   personaPtr,
		PersonaName: personaName,
		Screens: []projects.ScreenWithTree{
			{
				ScreenID:      flowID + "-screen-1",
				FlowID:        flowID,
				CanonicalTree: canonicalTree,
			},
		},
	}
}

// ─── Test scenarios ──────────────────────────────────────────────────────────

func TestCrossPersona_HappyPath_IdenticalSets(t *testing.T) {
	loader := &stubLoader{flows: []FlowWithPersona{
		makeFlow("flow-default", "persona-default", "Default", instanceTree("compA", "compB")),
		makeFlow("flow-loggedout", "persona-loggedout", "Logged-out", instanceTree("compA", "compB")),
	}}
	r := NewCrossPersonaRunner(loader)

	v := &projects.ProjectVersion{ID: "v1", ProjectID: "p1", TenantID: "t1"}
	got, err := r.Run(context.Background(), v)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 violations for identical sets, got %d: %+v", len(got), got)
	}
}

func TestCrossPersona_HappyPath_SoloPersona(t *testing.T) {
	loader := &stubLoader{flows: []FlowWithPersona{
		makeFlow("flow-only", "persona-default", "Default", instanceTree("compA", "compB")),
	}}
	r := NewCrossPersonaRunner(loader)

	v := &projects.ProjectVersion{ID: "v1", ProjectID: "p1", TenantID: "t1"}
	got, err := r.Run(context.Background(), v)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 violations for solo persona, got %d", len(got))
	}
}

func TestCrossPersona_ErrorPath_MissingToastInLoggedOut(t *testing.T) {
	// Default persona has Toast; Logged-out doesn't.
	defaultTree := frameTree(
		instanceNode{name: "Button", mainComponentKey: "btn-key"}.toMap(),
		instanceNode{name: "Toast", mainComponentKey: "toast-key"}.toMap(),
	)
	loggedOutTree := frameTree(
		instanceNode{name: "Button", mainComponentKey: "btn-key"}.toMap(),
	)
	loader := &stubLoader{flows: []FlowWithPersona{
		makeFlow("flow-default", "persona-default", "Default", defaultTree),
		makeFlow("flow-loggedout", "persona-loggedout", "Logged-out", loggedOutTree),
	}}
	r := NewCrossPersonaRunner(loader)

	v := &projects.ProjectVersion{ID: "v1", ProjectID: "p1", TenantID: "t1"}
	got, err := r.Run(context.Background(), v)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 violation, got %d: %+v", len(got), got)
	}
	vio := got[0]
	if vio.RuleID != "cross_persona_component_gap" {
		t.Errorf("RuleID = %q, want %q", vio.RuleID, "cross_persona_component_gap")
	}
	if vio.Category != "cross_persona" {
		t.Errorf("Category = %q, want %q", vio.Category, "cross_persona")
	}
	if vio.Severity != "high" {
		t.Errorf("Severity = %q, want %q", vio.Severity, "high")
	}
	if vio.Property != "component_coverage" {
		t.Errorf("Property = %q, want %q", vio.Property, "component_coverage")
	}
	if vio.Observed != "Toast missing" {
		t.Errorf("Observed = %q, want %q", vio.Observed, "Toast missing")
	}
	wantSugg := "Add Toast to Logged-out or Acknowledge as deliberate"
	if vio.Suggestion != wantSugg {
		t.Errorf("Suggestion = %q, want %q", vio.Suggestion, wantSugg)
	}
	if vio.PersonaID == nil || *vio.PersonaID != "persona-loggedout" {
		t.Errorf("PersonaID = %v, want pointer to %q", vio.PersonaID, "persona-loggedout")
	}
	if vio.ScreenID != "flow-loggedout-screen-1" {
		t.Errorf("ScreenID = %q, want %q", vio.ScreenID, "flow-loggedout-screen-1")
	}
	if vio.VersionID != "v1" {
		t.Errorf("VersionID = %q, want %q", vio.VersionID, "v1")
	}
	if vio.TenantID != "t1" {
		t.Errorf("TenantID = %q, want %q", vio.TenantID, "t1")
	}
	if vio.Status != "active" {
		t.Errorf("Status = %q, want %q", vio.Status, "active")
	}
}

func TestCrossPersona_EdgeCase_ThreePersonasChainedGaps(t *testing.T) {
	// Default has [A,B,C], Logged-out has [A,B], KYC-pending has [A].
	// Pair-wise gaps:
	//   Logged-out missing C
	//   KYC-pending missing B
	//   KYC-pending missing C
	loader := &stubLoader{flows: []FlowWithPersona{
		makeFlow("flow-default", "persona-default", "Default", instanceTree("A", "B", "C")),
		makeFlow("flow-loggedout", "persona-loggedout", "Logged-out", instanceTree("A", "B")),
		makeFlow("flow-kyc", "persona-kyc", "KYC-pending", instanceTree("A")),
	}}
	r := NewCrossPersonaRunner(loader)

	v := &projects.ProjectVersion{ID: "v1", ProjectID: "p1", TenantID: "t1"}
	got, err := r.Run(context.Background(), v)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 violations, got %d: %+v", len(got), summaries(got))
	}
	// Sort for stable assertion (order is implementation-defined).
	keys := summaries(got)
	sort.Strings(keys)
	want := []string{
		"persona-kyc:B missing",
		"persona-kyc:C missing",
		"persona-loggedout:C missing",
	}
	for i, k := range want {
		if keys[i] != k {
			t.Errorf("violation[%d] = %q, want %q", i, keys[i], k)
		}
	}
	for _, vio := range got {
		if vio.RuleID != "cross_persona_component_gap" {
			t.Errorf("expected all violations to share rule_id, got %q", vio.RuleID)
		}
		if vio.Severity != "high" {
			t.Errorf("expected severity=high, got %q", vio.Severity)
		}
	}
}

func TestCrossPersona_EdgeCase_BothMissingEachOthers(t *testing.T) {
	// Default has [A,B], Logged-out has [B,C].
	// Default missing C; Logged-out missing A.
	loader := &stubLoader{flows: []FlowWithPersona{
		makeFlow("flow-default", "persona-default", "Default", instanceTree("A", "B")),
		makeFlow("flow-loggedout", "persona-loggedout", "Logged-out", instanceTree("B", "C")),
	}}
	r := NewCrossPersonaRunner(loader)

	v := &projects.ProjectVersion{ID: "v1", ProjectID: "p1", TenantID: "t1"}
	got, err := r.Run(context.Background(), v)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 violations, got %d: %+v", len(got), summaries(got))
	}
	keys := summaries(got)
	sort.Strings(keys)
	want := []string{
		"persona-default:C missing",
		"persona-loggedout:A missing",
	}
	for i, k := range want {
		if keys[i] != k {
			t.Errorf("violation[%d] = %q, want %q", i, keys[i], k)
		}
	}
}

func TestCrossPersona_EdgeCase_NonInstanceNodesIgnored(t *testing.T) {
	// Default tree contains FRAME, GROUP, RECTANGLE, TEXT — no INSTANCE.
	// Logged-out also no INSTANCE. Net set difference is empty → 0 violations.
	noisyTree := func() string {
		return frameTree(
			map[string]any{"type": "FRAME", "name": "F", "children": []map[string]any{}},
			map[string]any{"type": "GROUP", "name": "G", "children": []map[string]any{}},
			map[string]any{"type": "RECTANGLE", "name": "R"},
			map[string]any{"type": "TEXT", "name": "T", "characters": "hello"},
		)
	}
	loader := &stubLoader{flows: []FlowWithPersona{
		makeFlow("flow-default", "persona-default", "Default", noisyTree()),
		makeFlow("flow-loggedout", "persona-loggedout", "Logged-out", noisyTree()),
	}}
	r := NewCrossPersonaRunner(loader)

	v := &projects.ProjectVersion{ID: "v1", ProjectID: "p1", TenantID: "t1"}
	got, err := r.Run(context.Background(), v)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 violations (no INSTANCE nodes), got %d: %+v", len(got), got)
	}
}

func TestCrossPersona_EdgeCase_InstanceWithoutComponentSetKeyIsSkipped(t *testing.T) {
	// Default has one valid INSTANCE plus one degraded INSTANCE (no key fields).
	// Logged-out only has the valid INSTANCE.
	// The degraded instance must be skipped — net diff = 0 violations.
	defaultTree := frameTree(
		instanceNode{name: "Valid", mainComponentKey: "valid-key"}.toMap(),
		// Degraded: no componentSetKey, no componentSetId, no componentId.
		map[string]any{"type": "INSTANCE", "name": "Degraded"},
	)
	loggedOutTree := frameTree(
		instanceNode{name: "Valid", mainComponentKey: "valid-key"}.toMap(),
	)
	loader := &stubLoader{flows: []FlowWithPersona{
		makeFlow("flow-default", "persona-default", "Default", defaultTree),
		makeFlow("flow-loggedout", "persona-loggedout", "Logged-out", loggedOutTree),
	}}
	r := NewCrossPersonaRunner(loader)

	v := &projects.ProjectVersion{ID: "v1", ProjectID: "p1", TenantID: "t1"}
	got, err := r.Run(context.Background(), v)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 violations (degraded instances skipped), got %d: %+v", len(got), got)
	}
}

func TestCrossPersona_OneEmptyPersonaGetsAllGaps(t *testing.T) {
	// Default has [A,B,C], Empty has []. Empty should get 3 violations
	// (A missing, B missing, C missing) — the value of cross-persona is
	// catching exactly this case.
	loader := &stubLoader{flows: []FlowWithPersona{
		makeFlow("flow-default", "persona-default", "Default", instanceTree("A", "B", "C")),
		makeFlow("flow-empty", "persona-empty", "Empty", frameTree()),
	}}
	r := NewCrossPersonaRunner(loader)

	v := &projects.ProjectVersion{ID: "v1", ProjectID: "p1", TenantID: "t1"}
	got, err := r.Run(context.Background(), v)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 violations on empty persona, got %d: %+v", len(got), summaries(got))
	}
	for _, vio := range got {
		if vio.PersonaID == nil || *vio.PersonaID != "persona-empty" {
			t.Errorf("expected violation on persona-empty, got %v", vio.PersonaID)
		}
	}
}

func TestCrossPersona_FallbackKeyFields(t *testing.T) {
	// Cover the componentSetId and componentId fallbacks.
	// Default: one instance with componentSetId only, one with componentId only.
	// Logged-out: empty.
	defaultTree := frameTree(
		instanceNode{name: "Alpha", componentSetID: "alpha-set-id"}.toMap(),
		instanceNode{name: "Beta", componentID: "beta-comp-id"}.toMap(),
	)
	loader := &stubLoader{flows: []FlowWithPersona{
		makeFlow("flow-default", "persona-default", "Default", defaultTree),
		makeFlow("flow-loggedout", "persona-loggedout", "Logged-out", frameTree()),
	}}
	r := NewCrossPersonaRunner(loader)

	v := &projects.ProjectVersion{ID: "v1", ProjectID: "p1", TenantID: "t1"}
	got, err := r.Run(context.Background(), v)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 violations across fallback keys, got %d: %+v", len(got), summaries(got))
	}
}

// summaries renders a "<personaID>:<observed>" string per violation for stable
// table-driven assertions.
func summaries(vs []projects.Violation) []string {
	out := make([]string, 0, len(vs))
	for _, v := range vs {
		pid := ""
		if v.PersonaID != nil {
			pid = *v.PersonaID
		}
		out = append(out, pid+":"+v.Observed)
	}
	return out
}

// Defensive: ensure the produced violations always reference the suggestion
// string format documented in the plan, regardless of which scenario produced
// them. Used as a smoke check rather than a separate test scenario.
func TestCrossPersona_SuggestionFormatStable(t *testing.T) {
	loader := &stubLoader{flows: []FlowWithPersona{
		makeFlow("flow-default", "persona-default", "Default", instanceTree("X")),
		makeFlow("flow-other", "persona-other", "Other Persona", frameTree()),
	}}
	r := NewCrossPersonaRunner(loader)

	v := &projects.ProjectVersion{ID: "v1", ProjectID: "p1", TenantID: "t1"}
	got, err := r.Run(context.Background(), v)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 violation, got %d", len(got))
	}
	vio := got[0]
	if !strings.HasPrefix(vio.Suggestion, "Add ") || !strings.Contains(vio.Suggestion, " to Other Persona") || !strings.HasSuffix(vio.Suggestion, " or Acknowledge as deliberate") {
		t.Errorf("Suggestion has unexpected shape: %q", vio.Suggestion)
	}
}
