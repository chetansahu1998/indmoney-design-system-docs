package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/projects"
)

// tools_resolve_test.go — U9b coverage. Mirrors the test harness shape
// from registry_test.go (newTestHarness, seedSubFlow, h.invoke).

// ─── Slug-shape validation ─────────────────────────────────────────────────

func TestResolve_MalformedSlug_OneSegment(t *testing.T) {
	h := newTestHarness(t)
	_, err := h.invoke("resolve", map[string]any{"slug": "just-one-segment"})
	if err == nil {
		t.Fatalf("expected error for 1-segment slug, got nil")
	}
	if !errors.Is(err, ErrInvalidArgs) {
		t.Errorf("expected ErrInvalidArgs, got %v", err)
	}
	if !strings.Contains(err.Error(), "segments") {
		t.Errorf("error should mention segments, got %v", err)
	}
}

func TestResolve_MalformedSlug_FourSegments(t *testing.T) {
	h := newTestHarness(t)
	_, err := h.invoke("resolve", map[string]any{"slug": "a/b/c/d"})
	if err == nil {
		t.Fatalf("expected error for 4-segment slug, got nil")
	}
	if !errors.Is(err, ErrInvalidArgs) {
		t.Errorf("expected ErrInvalidArgs, got %v", err)
	}
}

func TestResolve_MalformedSlug_Empty(t *testing.T) {
	h := newTestHarness(t)
	_, err := h.invoke("resolve", map[string]any{"slug": ""})
	if err == nil {
		t.Fatalf("expected error for empty slug")
	}
	if !errors.Is(err, ErrInvalidArgs) {
		t.Errorf("expected ErrInvalidArgs, got %v", err)
	}
}

func TestResolve_MalformedSlug_EmptySegment(t *testing.T) {
	h := newTestHarness(t)
	_, err := h.invoke("resolve", map[string]any{"slug": "wallet/"})
	if err == nil {
		t.Fatalf("expected error when a segment is empty")
	}
	if !errors.Is(err, ErrInvalidArgs) {
		t.Errorf("expected ErrInvalidArgs, got %v", err)
	}
}

// ─── Unknown slug ──────────────────────────────────────────────────────────

func TestResolve_UnknownSlug_ReturnsNotFound(t *testing.T) {
	h := newTestHarness(t)
	_, err := h.invoke("resolve", map[string]any{"slug": "ghost/sub"})
	if err == nil {
		t.Fatalf("expected error for unknown slug")
	}
	if !errors.Is(err, projects.ErrNotFound) {
		t.Errorf("expected projects.ErrNotFound, got %v", err)
	}
}

// ─── Two-segment happy path ────────────────────────────────────────────────

func TestResolve_TwoSegmentSlug_HappyPath(t *testing.T) {
	h := newTestHarness(t)
	h.seedSubFlow("Wallet", "M2M Settlement")

	res, err := h.invoke("resolve", map[string]any{"slug": "wallet/m2m-settlement"})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	out, ok := res.Data.(ResolveResult)
	if !ok {
		t.Fatalf("unexpected Data shape: %T", res.Data)
	}

	if out.Slug != "wallet/m2m-settlement" {
		t.Errorf("slug echo: got %q", out.Slug)
	}
	if out.Level != ResolveLevelSubFlow {
		t.Errorf("level: got %q, want %q", out.Level, ResolveLevelSubFlow)
	}
	if out.State != nil {
		t.Errorf("expected State==nil for sub_flow level, got %+v", out.State)
	}
	if out.SubFlow.FullSlug != "wallet/m2m-settlement" {
		t.Errorf("full_slug: got %q", out.SubFlow.FullSlug)
	}
	if out.SubFlow.SubProduct != "Wallet" {
		t.Errorf("sub_product display name: got %q", out.SubFlow.SubProduct)
	}
	if out.PRDExists {
		t.Errorf("PRDExists should be false on a fresh sub_flow")
	}
	if out.CanvasLifecycle != string(projects.LifecycleEmpty) {
		t.Errorf("lifecycle: got %q, want empty", out.CanvasLifecycle)
	}
	if out.Links.ConventionsDocURL == "" {
		t.Errorf("ConventionsDocURL should be populated")
	}
	// Plan 005 U8 — PM viewer is now Atlas's right rail + center pane;
	// the standalone /prd/<sp>/<sf> page redirects here. Slash inside the
	// slug is URL-encoded so it round-trips as a single query value.
	if out.Links.PRDViewerURL != "/atlas?subFlow=wallet%2Fm2m-settlement" {
		t.Errorf("PRDViewerURL: got %q", out.Links.PRDViewerURL)
	}
	if len(res.NextActions) == 0 {
		t.Errorf("expected next-action hints, got none")
	}
}

// ─── Stub array shape — never nil ──────────────────────────────────────────

func TestResolve_EmptyMixpanelEvents_StubsArrayShape(t *testing.T) {
	h := newTestHarness(t)
	h.seedSubFlow("Wallet", "Empty")

	res, err := h.invoke("resolve", map[string]any{"slug": "wallet/empty"})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	out := res.Data.(ResolveResult)

	if out.MixpanelEventNames == nil {
		t.Errorf("MixpanelEventNames is nil; want []")
	}
	if out.RecentEvents == nil {
		t.Errorf("RecentEvents is nil; want []")
	}
	if out.OpenSentryIssues == nil {
		t.Errorf("OpenSentryIssues is nil; want []")
	}
	if out.StorybookPaths == nil {
		t.Errorf("StorybookPaths is nil; want []")
	}
	if out.JIRAComponents == nil {
		t.Errorf("JIRAComponents is nil; want []")
	}

	// Round-trip via JSON to confirm `[]` not `null`.
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	js := string(b)
	for _, key := range []string{
		`"mixpanel_event_names":[]`,
		`"recent_events":[]`,
		`"open_sentry_issues":[]`,
		`"storybook_paths":[]`,
		`"jira_components":[]`,
	} {
		if !strings.Contains(js, key) {
			t.Errorf("missing stable empty-array shape %q in JSON:\n%s", key, js)
		}
	}
}

// ─── PRD-derived fields ────────────────────────────────────────────────────

func TestResolve_WithDeclaredEvents_NamesSortedAndDeduped(t *testing.T) {
	h := newTestHarness(t)
	h.seedSubFlow("Wallet", "M2M Settlement")
	ctx := context.Background()

	// Seed a PRD + tab + two states with three events (one duplicate).
	prd, err := h.deps.Repo.UpsertPRD(ctx, projects.PRDInput{
		SubFlowID: h.lastSeededSubFlowID(t, "wallet/m2m-settlement"),
		Title:     "M2M Settlement PRD",
	})
	if err != nil {
		t.Fatalf("UpsertPRD: %v", err)
	}
	tab, err := h.deps.Repo.UpsertPRDTab(ctx, projects.PRDTabInput{PRDID: prd.ID, Name: "default"})
	if err != nil {
		t.Fatalf("UpsertPRDTab: %v", err)
	}
	stCold, err := h.deps.Repo.UpsertPRDState(ctx, projects.PRDStateInput{
		PRDTabID: tab.ID,
		Label:    "Cold state",
		Position: 0,
	})
	if err != nil {
		t.Fatalf("UpsertPRDState: %v", err)
	}
	stHot, err := h.deps.Repo.UpsertPRDState(ctx, projects.PRDStateInput{
		PRDTabID: tab.ID,
		Label:    "Hot state",
		Position: 1,
	})
	if err != nil {
		t.Fatalf("UpsertPRDState (hot): %v", err)
	}
	for _, ev := range []struct {
		stateID string
		name    string
	}{
		{stCold.ID, "wallet.m2m_settlement.view"},
		{stCold.ID, "wallet.m2m_settlement.click_add_money"},
		{stHot.ID, "wallet.m2m_settlement.view"}, // duplicate name, different state — should dedupe
	} {
		if _, err := h.deps.Repo.AddEvent(ctx, projects.EventInput{
			PRDStateID: ev.stateID,
			Name:       ev.name,
		}); err != nil {
			t.Fatalf("AddEvent(%s): %v", ev.name, err)
		}
	}

	res, err := h.invoke("resolve", map[string]any{"slug": "wallet/m2m-settlement"})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	out := res.Data.(ResolveResult)
	if !out.PRDExists {
		t.Errorf("PRDExists should be true")
	}
	if out.StateCount != 2 {
		t.Errorf("StateCount: got %d, want 2", out.StateCount)
	}

	want := []string{"wallet.m2m_settlement.click_add_money", "wallet.m2m_settlement.view"}
	got := out.MixpanelEventNames
	if !sort.StringsAreSorted(got) {
		t.Errorf("MixpanelEventNames not sorted: %v", got)
	}
	if len(got) != len(want) {
		t.Fatalf("got %d events, want %d (deduped): %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("event[%d]: got %q, want %q", i, got[i], want[i])
		}
	}
}

// ─── Canvas lifecycle ──────────────────────────────────────────────────────

func TestResolve_CanvasLifecycle_ProtoOnly(t *testing.T) {
	h := newTestHarness(t)
	h.seedSubFlow("Wallet", "Pre design")
	ctx := context.Background()
	sf, err := h.deps.Repo.GetSubFlowBySlug(ctx, "wallet/pre-design")
	if err != nil {
		t.Fatalf("GetSubFlowBySlug: %v", err)
	}
	if err := h.deps.Repo.AttachPrototype(ctx, sf.ID,
		"https://example.com/proto", "preview", nil); err != nil {
		t.Fatalf("AttachPrototype: %v", err)
	}

	res, err := h.invoke("resolve", map[string]any{"slug": "wallet/pre-design"})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	out := res.Data.(ResolveResult)
	if out.CanvasLifecycle != string(projects.LifecycleProtoOnly) {
		t.Errorf("lifecycle: got %q, want %q",
			out.CanvasLifecycle, projects.LifecycleProtoOnly)
	}
	if out.PrototypeURL != "https://example.com/proto" {
		t.Errorf("PrototypeURL: got %q", out.PrototypeURL)
	}
}

// ─── DRDExists ─────────────────────────────────────────────────────────────

func TestResolve_DRDExists_FalseByDefault(t *testing.T) {
	h := newTestHarness(t)
	h.seedSubFlow("Wallet", "Pristine")

	res, err := h.invoke("resolve", map[string]any{"slug": "wallet/pristine"})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	out := res.Data.(ResolveResult)
	if out.DRDExists {
		t.Errorf("DRDExists should be false before CreateDRDForSubFlow")
	}
}

func TestResolve_DRDExists_TrueAfterAppend(t *testing.T) {
	h := newTestHarness(t)
	h.seedSubFlow("Wallet", "With DRD")

	// drd.append seeds a flow_drd row + YDoc state — matches the
	// LoadYDocStateBySubFlow contract resolve uses to detect existence.
	if _, err := h.invoke("drd.append", map[string]any{
		"sub_flow_slug":        "wallet/with-drd",
		"content_bytes_base64": "AQID", // 3 arbitrary bytes
	}); err != nil {
		t.Fatalf("drd.append: %v", err)
	}

	res, err := h.invoke("resolve", map[string]any{"slug": "wallet/with-drd"})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	out := res.Data.(ResolveResult)
	if !out.DRDExists {
		t.Errorf("DRDExists should be true after drd.append")
	}
}

// ─── 3-segment slug — state narrowing ──────────────────────────────────────

func TestResolve_ThreeSegmentSlug_HappyPath(t *testing.T) {
	h := newTestHarness(t)
	h.seedSubFlow("Wallet", "M2M Settlement")
	ctx := context.Background()

	prd, err := h.deps.Repo.UpsertPRD(ctx, projects.PRDInput{
		SubFlowID: h.lastSeededSubFlowID(t, "wallet/m2m-settlement"),
	})
	if err != nil {
		t.Fatalf("UpsertPRD: %v", err)
	}
	tab, err := h.deps.Repo.UpsertPRDTab(ctx, projects.PRDTabInput{
		PRDID: prd.ID, Name: "default",
	})
	if err != nil {
		t.Fatalf("UpsertPRDTab: %v", err)
	}
	if _, err := h.deps.Repo.UpsertPRDState(ctx, projects.PRDStateInput{
		PRDTabID: tab.ID,
		Label:    "Cold state",
		Position: 0,
	}); err != nil {
		t.Fatalf("UpsertPRDState: %v", err)
	}

	res, err := h.invoke("resolve", map[string]any{
		"slug": "wallet/m2m-settlement/cold-state",
	})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	out := res.Data.(ResolveResult)
	if out.Level != ResolveLevelState {
		t.Errorf("level: got %q, want %q", out.Level, ResolveLevelState)
	}
	if out.State == nil {
		t.Fatalf("expected State to be populated for 3-segment slug")
	}
	if out.State.Label != "Cold state" {
		t.Errorf("State.Label: got %q, want \"Cold state\"", out.State.Label)
	}
}

func TestResolve_ThreeSegmentSlug_StateNotFound_SubFlowStillReturned(t *testing.T) {
	h := newTestHarness(t)
	h.seedSubFlow("Wallet", "M2M Settlement")

	res, err := h.invoke("resolve", map[string]any{
		"slug": "wallet/m2m-settlement/nonexistent",
	})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	out := res.Data.(ResolveResult)
	if out.Level != ResolveLevelState {
		t.Errorf("level: got %q, want %q (caller asked for state)", out.Level, ResolveLevelState)
	}
	if out.State != nil {
		t.Errorf("expected State==nil when state segment doesn't match; got %+v", out.State)
	}
	if out.SubFlow.FullSlug != "wallet/m2m-settlement" {
		t.Errorf("sub_flow header still present: got %q", out.SubFlow.FullSlug)
	}
}

// ─── Tenant isolation ──────────────────────────────────────────────────────

func TestResolve_TenantIsolation(t *testing.T) {
	h := newTestHarness(t)
	h.seedSubFlow("Wallet", "M2M Settlement")

	repoB := projects.NewTenantRepo(h.d.DB, h.tenantB)
	depsB := Deps{Repo: repoB, UserID: h.userA}
	_, err := h.registry.Invoke(context.Background(), "resolve",
		depsB, json.RawMessage(`{"slug": "wallet/m2m-settlement"}`))
	if err == nil {
		t.Fatalf("expected ErrNotFound from tenantB lookup; got nil")
	}
	if !errors.Is(err, projects.ErrNotFound) {
		t.Errorf("expected projects.ErrNotFound, got %v", err)
	}
}

// ─── Test helpers (file-local) ─────────────────────────────────────────────

// lastSeededSubFlowID is a small convenience wrapper that looks up a
// just-seeded sub_flow by its slug. Avoids returning the SubFlow from
// seedSubFlow (which the broader test harness inherited from
// registry_test.go) — the resolve tests need the id repeatedly across
// repo calls in the same test.
func (h *testHarness) lastSeededSubFlowID(t *testing.T, slug string) string {
	t.Helper()
	sf, err := h.deps.Repo.GetSubFlowBySlug(context.Background(), slug)
	if err != nil {
		t.Fatalf("GetSubFlowBySlug(%q): %v", slug, err)
	}
	return sf.ID
}
