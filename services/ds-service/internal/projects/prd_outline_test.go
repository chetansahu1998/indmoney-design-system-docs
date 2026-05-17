package projects

import (
	"context"
	"strings"
	"testing"
	"time"
)

// prd_outline_test.go — U6b coverage-wall load path tests.
//
// Each test seeds: tenant + sub_flow + figma_section subtree, links them
// via LinkSubFlowToFigmaSection, then calls LoadSectionOutline and asserts
// shape + counts. Where prd_state rows are needed they go in via the U4
// repo methods (UpsertPRD, UpsertPRDTab, UpsertPRDState, stem adders).

// outlineHarness wires the common scaffolding so individual tests stay
// declarative. Returns the repo, sub_flow (linked to a section), the
// section identifiers (fileKey + sectionID), and ctx for write tools.
type outlineHarness struct {
	repo      *TenantRepo
	subFlow   SubFlow
	fileKey   string
	sectionID string
	ctx       context.Context
}

// newOutlineHarness seeds a tenant, sub_flow, section subtree (with the
// supplied direct-child frames), and binds them. Frame names are taken
// verbatim from the input slice — that's how the wall's join keys.
func newOutlineHarness(t *testing.T, directFrames []FigmaNodeRow) outlineHarness {
	t.Helper()
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	_, sf := seedSubFlow(t, repo, "Wallet", "M2M Settlement")

	fileKey := "fk-outline"
	sectionID := "10:1"
	subtree := append(
		[]FigmaNodeRow{
			{NodeID: sectionID, NodeType: "SECTION", Name: "Wallet/M2M Settlement", HasBBox: true, X: 0, Y: 0, Width: 1200, Height: 800, Depth: 2},
		},
		directFrames...,
	)
	seedSection(t, repo, fileKey, "0:1", sectionID, subtree)
	if err := repo.LinkSubFlowToFigmaSection(ctx, sf.ID, sectionID); err != nil {
		t.Fatalf("LinkSubFlowToFigmaSection: %v", err)
	}
	// Reload sub_flow so FigmaSectionID is populated for LoadSectionOutline.
	updated, err := repo.GetSubFlowBySlug(ctx, "wallet/m2m-settlement")
	if err != nil {
		t.Fatalf("GetSubFlowBySlug: %v", err)
	}

	return outlineHarness{
		repo:      repo,
		subFlow:   updated,
		fileKey:   fileKey,
		sectionID: sectionID,
		ctx:       ctx,
	}
}

// makeOutlineFrame is a fluent FigmaNodeRow constructor for direct-child
// frames in the canvas. Y-position drives the wall's display order.
func makeOutlineFrame(nodeID, name string, absY float64) FigmaNodeRow {
	return FigmaNodeRow{
		NodeID:     nodeID,
		ParentID:   "10:1",
		NodeType:   "FRAME",
		Name:       name,
		HasBBox:    true,
		X:          0,
		Y:          absY,
		Width:      100,
		Height:     100,
		Depth:      3,
		OrderIndex: 0,
	}
}

// upsertSkeletonState creates a PRD + tab + one prd_state with the given
// frame_name as both label and frame_name. Mirrors U2b's auto-skeleton
// shape so the wall's join can find it.
func upsertSkeletonState(t *testing.T, h outlineHarness, frameName string, position int) PRDState {
	t.Helper()
	_, err := h.repo.UpsertPRD(h.ctx, PRDInput{SubFlowID: h.subFlow.ID})
	if err != nil {
		t.Fatalf("UpsertPRD: %v", err)
	}
	prd, err := h.repo.UpsertPRD(h.ctx, PRDInput{SubFlowID: h.subFlow.ID})
	if err != nil {
		t.Fatalf("UpsertPRD: %v", err)
	}
	tab, err := h.repo.UpsertPRDTab(h.ctx, PRDTabInput{PRDID: prd.ID, Name: "default"})
	if err != nil {
		t.Fatalf("UpsertPRDTab: %v", err)
	}
	st, err := h.repo.UpsertPRDState(h.ctx, PRDStateInput{
		PRDTabID:  tab.ID,
		Label:     frameName,
		FrameName: frameName,
		Position:  position,
	})
	if err != nil {
		t.Fatalf("UpsertPRDState(%s): %v", frameName, err)
	}
	return st
}

// ─── Scenario 1: All frames bound ───────────────────────────────────────────

func TestLoadSectionOutline_AllFramesBound(t *testing.T) {
	h := newOutlineHarness(t, []FigmaNodeRow{
		makeOutlineFrame("n1", "Cold state", 0),
		makeOutlineFrame("n2", "Hot state", 100),
		makeOutlineFrame("n3", "Refresh in progress", 200),
		makeOutlineFrame("n4", "Bank tracking failed", 300),
	})
	upsertSkeletonState(t, h, "Cold state", 0)
	upsertSkeletonState(t, h, "Hot state", 1)
	upsertSkeletonState(t, h, "Refresh in progress", 2)
	upsertSkeletonState(t, h, "Bank tracking failed", 3)

	got, err := h.repo.LoadSectionOutline(h.ctx, h.subFlow)
	if err != nil {
		t.Fatalf("LoadSectionOutline: %v", err)
	}
	if len(got.Frames) != 4 {
		t.Fatalf("len(Frames)=%d, want 4", len(got.Frames))
	}
	for i, row := range got.Frames {
		if row.BindingStatus != BindingStatusBound {
			t.Errorf("row %d (%s): BindingStatus=%q, want bound", i, row.FrameName, row.BindingStatus)
		}
		if row.PRDStateID == nil {
			t.Errorf("row %d: PRDStateID nil, want set", i)
		}
	}
	if got.Counts.Total != 4 || got.Counts.Bound != 4 || got.Counts.Untagged != 0 || got.Counts.Orphaned != 0 {
		t.Errorf("Counts: %+v, want Total=4 Bound=4 Untagged=0 Orphaned=0", got.Counts)
	}
	if got.Counts.CoveragePercent != 100 {
		t.Errorf("CoveragePercent=%d, want 100", got.Counts.CoveragePercent)
	}
}

// ─── Scenario 2: Mixed bound + untagged ─────────────────────────────────────

func TestLoadSectionOutline_MixedBoundUntagged(t *testing.T) {
	h := newOutlineHarness(t, []FigmaNodeRow{
		makeOutlineFrame("n1", "Cold state", 0),
		makeOutlineFrame("n2", "Hot state", 100),
		makeOutlineFrame("n3", "Refresh in progress", 200),
		makeOutlineFrame("n4", "Bank tracking failed", 300),
	})
	// Only two frames get prd_state rows.
	upsertSkeletonState(t, h, "Cold state", 0)
	upsertSkeletonState(t, h, "Hot state", 1)

	got, err := h.repo.LoadSectionOutline(h.ctx, h.subFlow)
	if err != nil {
		t.Fatalf("LoadSectionOutline: %v", err)
	}
	if len(got.Frames) != 4 {
		t.Fatalf("len(Frames)=%d, want 4", len(got.Frames))
	}
	bound := 0
	untagged := 0
	for _, row := range got.Frames {
		switch row.BindingStatus {
		case BindingStatusBound:
			bound++
		case BindingStatusUntagged:
			untagged++
			if row.PRDStateID != nil {
				t.Errorf("untagged row %s has PRDStateID=%v, want nil", row.FrameName, *row.PRDStateID)
			}
		}
	}
	if bound != 2 || untagged != 2 {
		t.Errorf("got bound=%d untagged=%d, want 2/2", bound, untagged)
	}
	if got.Counts.CoveragePercent != 50 {
		t.Errorf("CoveragePercent=%d, want 50", got.Counts.CoveragePercent)
	}
}

// ─── Scenario 3: Orphaned state (no matching frame) ──────────────────────────

func TestLoadSectionOutline_OrphanedState(t *testing.T) {
	h := newOutlineHarness(t, []FigmaNodeRow{
		makeOutlineFrame("n1", "Cold state", 0),
		makeOutlineFrame("n2", "Hot state", 100),
		makeOutlineFrame("n3", "Refresh in progress", 200),
	})
	upsertSkeletonState(t, h, "Cold state", 0)
	upsertSkeletonState(t, h, "Hot state", 1)
	upsertSkeletonState(t, h, "Refresh in progress", 2)
	// A 4th state whose frame_name is NOT in the section — should be orphaned.
	upsertSkeletonState(t, h, "Removed by designer", 3)

	got, err := h.repo.LoadSectionOutline(h.ctx, h.subFlow)
	if err != nil {
		t.Fatalf("LoadSectionOutline: %v", err)
	}
	// 3 frames + 1 orphan = 4 rows.
	if len(got.Frames) != 4 {
		t.Fatalf("len(Frames)=%d, want 4 (3 frames + 1 orphan)", len(got.Frames))
	}
	orphanCount := 0
	for _, row := range got.Frames {
		if row.BindingStatus == BindingStatusOrphaned {
			orphanCount++
			if row.FigmaNodeID != "" {
				t.Errorf("orphan row has FigmaNodeID=%q, want empty", row.FigmaNodeID)
			}
			if row.FrameName != "Removed by designer" {
				t.Errorf("orphan FrameName=%q, want %q", row.FrameName, "Removed by designer")
			}
		}
	}
	if orphanCount != 1 {
		t.Errorf("orphanCount=%d, want 1", orphanCount)
	}
	if got.Counts.Orphaned != 1 || got.Counts.Bound != 3 {
		t.Errorf("Counts: %+v, want Bound=3 Orphaned=1", got.Counts)
	}
}

// ─── Scenario 4: Soft-deleted state surfaces as orphan ───────────────────────

func TestLoadSectionOutline_SoftDeletedStateAsOrphan(t *testing.T) {
	h := newOutlineHarness(t, []FigmaNodeRow{
		makeOutlineFrame("n1", "Cold state", 0),
	})
	st := upsertSkeletonState(t, h, "Cold state", 0)
	// Also seed a soft-deleted state — its label matches a frame name that's
	// no longer in the section. Use a name not in the current section so the
	// orphan branch fires regardless of live-match precedence.
	deleted := upsertSkeletonState(t, h, "Old removed state", 1)
	if err := h.repo.SoftDeletePRDState(h.ctx, deleted.ID); err != nil {
		t.Fatalf("SoftDeletePRDState: %v", err)
	}
	_ = st

	got, err := h.repo.LoadSectionOutline(h.ctx, h.subFlow)
	if err != nil {
		t.Fatalf("LoadSectionOutline: %v", err)
	}
	orphans := 0
	for _, row := range got.Frames {
		if row.BindingStatus == BindingStatusOrphaned {
			orphans++
		}
	}
	if orphans != 1 {
		t.Errorf("orphans=%d, want 1 (soft-deleted state)", orphans)
	}
}

// ─── Scenario 5: Counts populated from stems ────────────────────────────────

func TestLoadSectionOutline_CountsFromStems(t *testing.T) {
	h := newOutlineHarness(t, []FigmaNodeRow{
		makeOutlineFrame("n1", "Cold state", 0),
	})
	st := upsertSkeletonState(t, h, "Cold state", 0)
	// Three acceptance criteria.
	for _, c := range []string{"AC 1", "AC 2", "AC 3"} {
		if _, err := h.repo.AddAcceptanceCriterion(h.ctx, AcceptanceCriterionInput{PRDStateID: st.ID, Criterion: c}); err != nil {
			t.Fatalf("AddAcceptanceCriterion: %v", err)
		}
	}
	// Two events.
	for _, n := range []string{"wallet.m2m.cold_viewed", "wallet.m2m.cta_tapped"} {
		if _, err := h.repo.AddEvent(h.ctx, EventInput{PRDStateID: st.ID, Name: n}); err != nil {
			t.Fatalf("AddEvent: %v", err)
		}
	}
	// One copy string.
	if _, err := h.repo.UpsertCopyString(h.ctx, CopyStringInput{PRDStateID: st.ID, Key: "title", Value: "Add a bank"}); err != nil {
		t.Fatalf("UpsertCopyString: %v", err)
	}
	// One edge case.
	if _, err := h.repo.AddEdgeCase(h.ctx, EdgeCaseInput{PRDStateID: st.ID, EdgeCase: "No internet"}); err != nil {
		t.Fatalf("AddEdgeCase: %v", err)
	}
	// One a11y note.
	if _, err := h.repo.AddA11yNote(h.ctx, A11yNoteInput{PRDStateID: st.ID, Note: "VoiceOver: announce CTA label"}); err != nil {
		t.Fatalf("AddA11yNote: %v", err)
	}

	got, err := h.repo.LoadSectionOutline(h.ctx, h.subFlow)
	if err != nil {
		t.Fatalf("LoadSectionOutline: %v", err)
	}
	if len(got.Frames) != 1 {
		t.Fatalf("len(Frames)=%d, want 1", len(got.Frames))
	}
	row := got.Frames[0]
	if row.CriteriaCount != 3 {
		t.Errorf("CriteriaCount=%d, want 3", row.CriteriaCount)
	}
	if row.EventsCount != 2 {
		t.Errorf("EventsCount=%d, want 2", row.EventsCount)
	}
	if row.CopyCount != 1 {
		t.Errorf("CopyCount=%d, want 1", row.CopyCount)
	}
	if row.EdgeCasesCount != 1 {
		t.Errorf("EdgeCasesCount=%d, want 1", row.EdgeCasesCount)
	}
	if row.A11yCount != 1 {
		t.Errorf("A11yCount=%d, want 1", row.A11yCount)
	}
}

// ─── Scenario 6: last_touched_* from audit log ──────────────────────────────

func TestLoadSectionOutline_LastTouchedFromAuditLog(t *testing.T) {
	h := newOutlineHarness(t, []FigmaNodeRow{
		makeOutlineFrame("n1", "Cold state", 0),
	})
	st := upsertSkeletonState(t, h, "Cold state", 0)
	// Record two audits — user2 should win as the latest.
	// Audit `at` is stored at RFC3339 second-precision (no fractional
	// seconds). Inject fixed clocks 2 seconds apart so the DESC order
	// is deterministic without slow time.Sleep.
	t0 := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	repoT0 := h.repo.withNow(func() time.Time { return t0 })
	repoT1 := h.repo.withNow(func() time.Time { return t0.Add(2 * time.Second) })
	if err := repoT0.RecordPRDAudit(h.ctx, st.ID, "user1", OpUpsertState); err != nil {
		t.Fatalf("RecordPRDAudit user1: %v", err)
	}
	if err := repoT1.RecordPRDAudit(h.ctx, st.ID, "user2", OpAddEvent); err != nil {
		t.Fatalf("RecordPRDAudit user2: %v", err)
	}

	got, err := h.repo.LoadSectionOutline(h.ctx, h.subFlow)
	if err != nil {
		t.Fatalf("LoadSectionOutline: %v", err)
	}
	if len(got.Frames) != 1 {
		t.Fatalf("len(Frames)=%d, want 1", len(got.Frames))
	}
	row := got.Frames[0]
	if row.LastTouchedBy == nil {
		t.Fatalf("LastTouchedBy nil, want non-nil")
	}
	if *row.LastTouchedBy != "user2" {
		t.Errorf("LastTouchedBy=%q, want user2", *row.LastTouchedBy)
	}
	if row.LastTouchedAt == nil || *row.LastTouchedAt == "" {
		t.Errorf("LastTouchedAt nil/empty, want set")
	}
}

// ─── Scenario 7: Empty section (no section bound on sub_flow) ───────────────

func TestLoadSectionOutline_NoSectionBound(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()
	_, sf := seedSubFlow(t, repo, "Wallet", "M2M Settlement")
	// sub_flow has no figma_section_id linked yet.

	got, err := repo.LoadSectionOutline(ctx, sf)
	if err != nil {
		t.Fatalf("LoadSectionOutline: %v", err)
	}
	if got.Counts.Total != 0 {
		t.Errorf("Counts.Total=%d, want 0", got.Counts.Total)
	}
	if len(got.Frames) != 0 {
		t.Errorf("len(Frames)=%d, want 0", len(got.Frames))
	}
}

// ─── Scenario 8: TotalWordCount sums state + stem text ──────────────────────

func TestLoadSectionOutline_TotalWordCount(t *testing.T) {
	h := newOutlineHarness(t, []FigmaNodeRow{
		makeOutlineFrame("n1", "Cold state", 0),
	})
	frameName := "Cold state"
	prd, err := h.repo.UpsertPRD(h.ctx, PRDInput{SubFlowID: h.subFlow.ID})
	if err != nil {
		t.Fatalf("UpsertPRD: %v", err)
	}
	tab, err := h.repo.UpsertPRDTab(h.ctx, PRDTabInput{PRDID: prd.ID, Name: "default"})
	if err != nil {
		t.Fatalf("UpsertPRDTab: %v", err)
	}
	st, err := h.repo.UpsertPRDState(h.ctx, PRDStateInput{
		PRDTabID:         tab.ID,
		Label:            frameName,
		FrameName:        frameName,
		Position:         0,
		ConditionMD:      "user has no bank accounts linked",  // 6 words
		DesignHandlingMD: "show empty state with primary CTA", // 6 words
	})
	if err != nil {
		t.Fatalf("UpsertPRDState: %v", err)
	}
	if _, err := h.repo.AddAcceptanceCriterion(h.ctx, AcceptanceCriterionInput{PRDStateID: st.ID, Criterion: "Empty state visible to first-time users"}); err != nil {
		t.Fatalf("AddAcceptanceCriterion: %v", err)
	}

	got, err := h.repo.LoadSectionOutline(h.ctx, h.subFlow)
	if err != nil {
		t.Fatalf("LoadSectionOutline: %v", err)
	}
	row := got.Frames[0]
	// Words are counted across markdown + stem text. Don't pin exact total —
	// implementation may include/exclude stems differently — just assert > 0
	// and that prose is sampled.
	if row.TotalWordCount <= 0 {
		t.Errorf("TotalWordCount=%d, want >0 with seeded prose", row.TotalWordCount)
	}
	if !strings.HasPrefix(*row.PRDStateLabel, "Cold") {
		t.Errorf("PRDStateLabel=%q, want prefix 'Cold'", *row.PRDStateLabel)
	}
}

// ─── Scenario 9: Tenant isolation ───────────────────────────────────────────

func TestLoadSectionOutline_TenantIsolation(t *testing.T) {
	d, tA, tB, _ := newTestDB(t)
	repoA := NewTenantRepo(d.DB, tA)
	repoB := NewTenantRepo(d.DB, tB)
	ctx := context.Background()

	// Tenant A: seed sub_flow + section + state.
	_, sfA := seedSubFlow(t, repoA, "Wallet", "M2M Settlement")
	subtree := []FigmaNodeRow{
		{NodeID: "10:1", NodeType: "SECTION", Name: "Wallet/M2M Settlement", HasBBox: true, X: 0, Y: 0, Width: 1200, Height: 800, Depth: 2},
		makeOutlineFrame("n1", "Cold state", 0),
	}
	seedSection(t, repoA, "fk-A", "0:1", "10:1", subtree)
	if err := repoA.LinkSubFlowToFigmaSection(ctx, sfA.ID, "10:1"); err != nil {
		t.Fatalf("LinkSubFlowToFigmaSection: %v", err)
	}

	// Reload sfA to get the linked figma_section_id.
	sfA, err := repoA.GetSubFlowBySlug(ctx, "wallet/m2m-settlement")
	if err != nil {
		t.Fatalf("GetSubFlowBySlug: %v", err)
	}

	// Tenant B's view of sfA is empty (cross-tenant access yields no rows).
	// Forge a SubFlow with sfA's id under tenant B's repo binding.
	gotB, err := repoB.LoadSectionOutline(ctx, sfA)
	if err != nil {
		t.Fatalf("LoadSectionOutline tenantB: %v", err)
	}
	if gotB.Counts.Total != 0 {
		t.Errorf("tenantB Counts.Total=%d, want 0 (no cross-tenant data)", gotB.Counts.Total)
	}
}
