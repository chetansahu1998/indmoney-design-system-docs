package projects

import (
	"context"
	"sort"
	"testing"
)

// repository_figma_autosync_skeleton_test.go — U2b tests.
//
// Verifies AutoSkeletonPRDStates:
//   - Creates one prd_state per non-default-named direct-child frame.
//   - Preserves canvas-Y order via Position.
//   - Is idempotent on re-runs (no duplicate rows).
//   - Skips Figma default names.
//   - Soft-deletes prior auto-skeleton states whose frame disappeared.
//   - Restores soft-deleted states on frame re-add.
//   - Tolerates empty frame lists.
//   - Stays tenant-scoped.

// makeFrame is a fluent FrameRow constructor with sensible defaults so
// tests stay readable.
func makeFrame(nodeID, name string, absY float64) FrameRow {
	return FrameRow{
		NodeID:       nodeID,
		Name:         name,
		ParentNodeID: "section-1",
		Depth:        2,
		AbsX:         0,
		AbsY:         absY,
		Width:        100,
		Height:       100,
		HasRender:    false,
	}
}

// liveStateLabels returns the labels of all live prd_state rows on a
// sub_flow's PRD, ordered by position. Useful for asserting both
// content + order in one pass.
func liveStateLabels(t *testing.T, repo *TenantRepo, subFlowID string) []string {
	t.Helper()
	full, err := repo.LoadPRD(context.Background(), subFlowID)
	if err != nil {
		t.Fatalf("LoadPRD: %v", err)
	}
	if len(full.Tabs) == 0 {
		return nil
	}
	// v1 single-tab — collect from the first tab.
	tab := full.Tabs[0]
	labels := make([]string, len(tab.States))
	for i, st := range tab.States {
		labels[i] = st.Label
	}
	return labels
}

// allStatesIncludingDeleted is a tiny direct-SQL helper for assertions
// that need to see soft-deleted rows (LoadPRD excludes them).
func allStatesIncludingDeleted(t *testing.T, repo *TenantRepo, tabID string) []PRDState {
	t.Helper()
	rows, err := repo.handle().QueryContext(context.Background(), `
		SELECT id, tenant_id, prd_tab_id, label, position, frame_name,
		       condition_md, design_handling_md, fe_handling_md,
		       deleted_at, created_at, updated_at
		  FROM prd_state
		 WHERE tenant_id = ? AND prd_tab_id = ?
		 ORDER BY position ASC, created_at ASC
	`, repo.tenantID, tabID)
	if err != nil {
		t.Fatalf("query prd_state: %v", err)
	}
	defer rows.Close()
	var out []PRDState
	for rows.Next() {
		st, scanErr := scanPRDState(rows)
		if scanErr != nil {
			t.Fatalf("scan prd_state: %v", scanErr)
		}
		out = append(out, st)
	}
	return out
}

// seedSubFlowForSkeleton returns the sub_flow id that AutoSkeletonPRDStates
// will operate against.
func seedSubFlowForSkeleton(t *testing.T, repo *TenantRepo) SubFlow {
	t.Helper()
	_, sf := seedSubFlow(t, repo, "Wallet", "M2M Settlement")
	return sf
}

// firstTabID locates the single default tab AutoSkeletonPRDStates creates,
// so tests can poke at prd_state rows directly.
func firstTabID(t *testing.T, repo *TenantRepo, subFlowID string) string {
	t.Helper()
	full, err := repo.LoadPRD(context.Background(), subFlowID)
	if err != nil {
		t.Fatalf("LoadPRD: %v", err)
	}
	if len(full.Tabs) == 0 {
		t.Fatalf("no tabs on PRD for sub_flow %s", subFlowID)
	}
	return full.Tabs[0].ID
}

// ─── Scenario 1: Happy path ─────────────────────────────────────────────────

func TestAutoSkeleton_HappyPath(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	sf := seedSubFlowForSkeleton(t, repo)

	frames := []FrameRow{
		makeFrame("1:1", "Cold state", 0),
		makeFrame("1:2", "Hot state", 100),
		makeFrame("1:3", "Empty state", 200),
		makeFrame("1:4", "Error", 300),
		makeFrame("1:5", "Loading", 400),
		makeFrame("1:6", "Success", 500),
	}

	if err := repo.AutoSkeletonPRDStates(ctx, sf.ID, frames); err != nil {
		t.Fatalf("AutoSkeletonPRDStates: %v", err)
	}

	got := liveStateLabels(t, repo, sf.ID)
	want := []string{"Cold state", "Hot state", "Empty state", "Error", "Loading", "Success"}
	if len(got) != len(want) {
		t.Fatalf("state count: got %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("position %d: got %q, want %q", i, got[i], want[i])
		}
	}

	// frame_name should be set on every auto-created state (it's the gate
	// the soft-delete sweep uses).
	full, _ := repo.LoadPRD(ctx, sf.ID)
	for _, st := range full.Tabs[0].States {
		if st.FrameName == nil || *st.FrameName != st.Label {
			t.Errorf("state %q: frame_name %v, want %q", st.Label, st.FrameName, st.Label)
		}
	}
}

// ─── Scenario 2: Idempotent re-run ──────────────────────────────────────────

func TestAutoSkeleton_Idempotent(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	sf := seedSubFlowForSkeleton(t, repo)

	frames := []FrameRow{
		makeFrame("1:1", "Cold state", 0),
		makeFrame("1:2", "Hot state", 100),
	}
	if err := repo.AutoSkeletonPRDStates(ctx, sf.ID, frames); err != nil {
		t.Fatalf("first pass: %v", err)
	}
	full1, _ := repo.LoadPRD(ctx, sf.ID)
	idsBefore := make(map[string]string, len(full1.Tabs[0].States))
	for _, st := range full1.Tabs[0].States {
		idsBefore[st.Label] = st.ID
	}

	// Second pass — exact same input.
	if err := repo.AutoSkeletonPRDStates(ctx, sf.ID, frames); err != nil {
		t.Fatalf("second pass: %v", err)
	}
	full2, _ := repo.LoadPRD(ctx, sf.ID)
	if full1.ID != full2.ID {
		t.Fatalf("PRD id changed across passes: %s vs %s", full1.ID, full2.ID)
	}
	if len(full2.Tabs) != 1 {
		t.Errorf("tab count: got %d, want 1", len(full2.Tabs))
	}
	if len(full2.Tabs[0].States) != 2 {
		t.Errorf("state count: got %d, want 2", len(full2.Tabs[0].States))
	}
	for _, st := range full2.Tabs[0].States {
		if idsBefore[st.Label] != st.ID {
			t.Errorf("state id for %q changed: %s -> %s", st.Label, idsBefore[st.Label], st.ID)
		}
	}
}

// ─── Scenario 3: Skip Figma default-named frames ─────────────────────────────

func TestAutoSkeleton_SkipsFigmaDefaults(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	sf := seedSubFlowForSkeleton(t, repo)

	frames := []FrameRow{
		makeFrame("1:1", "Cold state", 0),
		makeFrame("1:2", "Frame 12345", 50),        // skip
		makeFrame("1:3", "Hot state", 100),
		makeFrame("1:4", "Rectangle 6789", 150),    // skip
		makeFrame("1:5", "Empty state", 200),
		makeFrame("1:6", "Group 1", 250),           // skip
		makeFrame("1:7", "Union", 300),             // skip
		makeFrame("1:8", "Error", 350),
		makeFrame("1:9", "Ellipse 42", 400),        // skip
		makeFrame("1:10", "Vector 7", 450),         // skip
		makeFrame("1:11", "Line 1", 500),           // skip
	}

	if err := repo.AutoSkeletonPRDStates(ctx, sf.ID, frames); err != nil {
		t.Fatalf("AutoSkeletonPRDStates: %v", err)
	}

	got := liveStateLabels(t, repo, sf.ID)
	want := []string{"Cold state", "Hot state", "Empty state", "Error"}
	if len(got) != len(want) {
		t.Fatalf("state count: got %d (%v), want %d (%v)", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("position %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

// Negative cases — names that LOOK like defaults but shouldn't be skipped.
func TestAutoSkeleton_DefaultRegex_NegativeCases(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	sf := seedSubFlowForSkeleton(t, repo)
	frames := []FrameRow{
		makeFrame("1:1", "Frame", 0),               // no trailing digit → keep
		makeFrame("1:2", "Cold Frame 1", 100),      // not at start → keep
		makeFrame("1:3", "Frame Layout", 200),      // no digit → keep
		makeFrame("1:4", "Union State", 300),       // not bare "Union" → keep
		makeFrame("1:5", "Rectangle", 400),         // no digit → keep
	}
	if err := repo.AutoSkeletonPRDStates(ctx, sf.ID, frames); err != nil {
		t.Fatalf("AutoSkeletonPRDStates: %v", err)
	}
	got := liveStateLabels(t, repo, sf.ID)
	if len(got) != 5 {
		t.Errorf("expected all 5 borderline names to survive, got %d (%v)", len(got), got)
	}
}

// ─── Scenario 4: Frame added on re-run ──────────────────────────────────────

func TestAutoSkeleton_FrameAdded(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	sf := seedSubFlowForSkeleton(t, repo)

	first := []FrameRow{
		makeFrame("1:1", "Cold state", 0),
		makeFrame("1:2", "Hot state", 100),
		makeFrame("1:3", "Empty state", 200),
	}
	if err := repo.AutoSkeletonPRDStates(ctx, sf.ID, first); err != nil {
		t.Fatalf("first pass: %v", err)
	}
	full1, _ := repo.LoadPRD(ctx, sf.ID)
	hotID := ""
	for _, st := range full1.Tabs[0].States {
		if st.Label == "Hot state" {
			hotID = st.ID
		}
	}

	// Designer adds "Loading" between Hot and Empty (canvas Y 150).
	second := []FrameRow{
		makeFrame("1:1", "Cold state", 0),
		makeFrame("1:2", "Hot state", 100),
		makeFrame("1:4", "Loading", 150),
		makeFrame("1:3", "Empty state", 200),
	}
	if err := repo.AutoSkeletonPRDStates(ctx, sf.ID, second); err != nil {
		t.Fatalf("second pass: %v", err)
	}

	got := liveStateLabels(t, repo, sf.ID)
	want := []string{"Cold state", "Hot state", "Loading", "Empty state"}
	if len(got) != len(want) {
		t.Fatalf("state count: got %d (%v), want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("position %d: got %q, want %q", i, got[i], want[i])
		}
	}

	// Pre-existing Hot state retained its id.
	full2, _ := repo.LoadPRD(ctx, sf.ID)
	hotIDAfter := ""
	for _, st := range full2.Tabs[0].States {
		if st.Label == "Hot state" {
			hotIDAfter = st.ID
		}
	}
	if hotIDAfter != hotID {
		t.Errorf("Hot state id changed: %s -> %s", hotID, hotIDAfter)
	}
}

// ─── Scenario 5: Frame removed → soft-delete ────────────────────────────────

func TestAutoSkeleton_FrameRemoved_SoftDeletes(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	sf := seedSubFlowForSkeleton(t, repo)

	first := []FrameRow{
		makeFrame("1:1", "Cold state", 0),
		makeFrame("1:2", "Hot state", 100),
		makeFrame("1:3", "Empty state", 200),
	}
	if err := repo.AutoSkeletonPRDStates(ctx, sf.ID, first); err != nil {
		t.Fatalf("first pass: %v", err)
	}
	tabID := firstTabID(t, repo, sf.ID)

	// Designer removes Cold state.
	second := []FrameRow{
		makeFrame("1:2", "Hot state", 100),
		makeFrame("1:3", "Empty state", 200),
	}
	if err := repo.AutoSkeletonPRDStates(ctx, sf.ID, second); err != nil {
		t.Fatalf("second pass: %v", err)
	}

	got := liveStateLabels(t, repo, sf.ID)
	want := []string{"Hot state", "Empty state"}
	if len(got) != len(want) {
		t.Fatalf("live state count: got %d (%v), want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("position %d: got %q, want %q", i, got[i], want[i])
		}
	}

	// Cold state row still exists in the DB, just with deleted_at set.
	all := allStatesIncludingDeleted(t, repo, tabID)
	var coldFound bool
	for _, st := range all {
		if st.Label == "Cold state" {
			coldFound = true
			if st.DeletedAt == nil {
				t.Errorf("Cold state: expected deleted_at to be set")
			}
		}
	}
	if !coldFound {
		t.Errorf("Cold state row not present in DB at all — should be soft-deleted, not hard-deleted")
	}
}

// Soft-delete preserves authored stems — the PM-restoration loop in U6
// depends on this. Authored acceptance criteria on a now-orphaned state
// should still be there after the soft-delete pass.
func TestAutoSkeleton_FrameRemoved_PreservesAuthoredStems(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	sf := seedSubFlowForSkeleton(t, repo)

	first := []FrameRow{
		makeFrame("1:1", "Cold state", 0),
		makeFrame("1:2", "Hot state", 100),
	}
	if err := repo.AutoSkeletonPRDStates(ctx, sf.ID, first); err != nil {
		t.Fatalf("first pass: %v", err)
	}
	// Author a criterion on Cold state.
	full1, _ := repo.LoadPRD(ctx, sf.ID)
	var coldID string
	for _, st := range full1.Tabs[0].States {
		if st.Label == "Cold state" {
			coldID = st.ID
		}
	}
	if coldID == "" {
		t.Fatal("Cold state not found after first pass")
	}
	if _, err := repo.AddAcceptanceCriterion(ctx, AcceptanceCriterionInput{
		PRDStateID: coldID,
		Position:   0,
		Criterion:  "Cold state precondition",
	}); err != nil {
		t.Fatalf("AddAcceptanceCriterion: %v", err)
	}

	// Designer removes Cold state.
	if err := repo.AutoSkeletonPRDStates(ctx, sf.ID, []FrameRow{
		makeFrame("1:2", "Hot state", 100),
	}); err != nil {
		t.Fatalf("second pass: %v", err)
	}

	// The criterion row should still exist (foreign-keyed to the soft-deleted state).
	var count int
	row := repo.handle().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM prd_state_acceptance_criterion WHERE tenant_id = ? AND prd_state_id = ?`,
		tA, coldID,
	)
	if err := row.Scan(&count); err != nil {
		t.Fatalf("count criteria: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 acceptance criterion preserved on soft-deleted state, got %d", count)
	}
}

// Re-running autosync must NOT wipe PM-authored markdown columns
// (condition_md, design_handling_md, fe_handling_md) on existing skeleton
// rows. This is the workflow's whole premise: PMs author, autosync runs
// in the background, content survives.
//
// Without the ensureSkeletonRow helper this test fails — UpsertPRDState
// would overwrite the three markdown columns with the empty strings from
// the skeleton's PRDStateInput.
func TestAutoSkeleton_RerunPreservesAuthoredMarkdown(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	sf := seedSubFlowForSkeleton(t, repo)

	frames := []FrameRow{
		makeFrame("1:1", "Cold state", 0),
		makeFrame("1:2", "Hot state", 100),
	}
	if err := repo.AutoSkeletonPRDStates(ctx, sf.ID, frames); err != nil {
		t.Fatalf("first pass: %v", err)
	}

	// Locate the Cold state row and simulate the MCP authoring path:
	// non-empty markdown across all three columns, plus an acceptance
	// criterion (the typed-stem from U6's prd.author op:add_acceptance_criterion).
	full1, _ := repo.LoadPRD(ctx, sf.ID)
	var coldID string
	for _, st := range full1.Tabs[0].States {
		if st.Label == "Cold state" {
			coldID = st.ID
		}
	}
	if coldID == "" {
		t.Fatal("Cold state not found after first pass")
	}

	const (
		authoredCondition = "User has no holdings AND has never deposited."
		authoredDesign    = "Show the empty-state illustration with a CTA: 'Start investing'."
		authoredFE        = "GET /v1/portfolio/holdings returns []; render <EmptyHoldings />."
		authoredCriterion = "Tapping 'Start investing' opens the deposit sheet."
	)

	if _, err := repo.UpsertPRDState(ctx, PRDStateInput{
		PRDTabID:         full1.Tabs[0].ID,
		Label:            "Cold state",
		Position:         0,
		FrameName:        "Cold state",
		ConditionMD:      authoredCondition,
		DesignHandlingMD: authoredDesign,
		FEHandlingMD:     authoredFE,
	}); err != nil {
		t.Fatalf("simulate MCP authoring write: %v", err)
	}
	if _, err := repo.AddAcceptanceCriterion(ctx, AcceptanceCriterionInput{
		PRDStateID: coldID,
		Position:   0,
		Criterion:  authoredCriterion,
	}); err != nil {
		t.Fatalf("AddAcceptanceCriterion: %v", err)
	}

	// Re-run autosync with the same frames — no designer-visible change.
	if err := repo.AutoSkeletonPRDStates(ctx, sf.ID, frames); err != nil {
		t.Fatalf("second pass: %v", err)
	}

	// Reload and assert the authored markdown survives, with the criterion
	// still attached.
	full2, err := repo.LoadPRD(ctx, sf.ID)
	if err != nil {
		t.Fatalf("LoadPRD: %v", err)
	}
	var coldAfter *PRDStateFull
	for i := range full2.Tabs[0].States {
		if full2.Tabs[0].States[i].Label == "Cold state" {
			coldAfter = &full2.Tabs[0].States[i]
		}
	}
	if coldAfter == nil {
		t.Fatal("Cold state vanished after re-run")
	}
	if coldAfter.ID != coldID {
		t.Errorf("Cold state id changed across re-run: %s -> %s", coldID, coldAfter.ID)
	}
	if coldAfter.ConditionMD != authoredCondition {
		t.Errorf("condition_md wiped by re-run: got %q, want %q", coldAfter.ConditionMD, authoredCondition)
	}
	if coldAfter.DesignHandlingMD != authoredDesign {
		t.Errorf("design_handling_md wiped by re-run: got %q, want %q", coldAfter.DesignHandlingMD, authoredDesign)
	}
	if coldAfter.FEHandlingMD != authoredFE {
		t.Errorf("fe_handling_md wiped by re-run: got %q, want %q", coldAfter.FEHandlingMD, authoredFE)
	}
	if len(coldAfter.AcceptanceCriteria) != 1 {
		t.Fatalf("expected 1 acceptance criterion to survive re-run, got %d", len(coldAfter.AcceptanceCriteria))
	}
	if coldAfter.AcceptanceCriteria[0].Criterion != authoredCriterion {
		t.Errorf("acceptance criterion drifted: got %q, want %q", coldAfter.AcceptanceCriteria[0].Criterion, authoredCriterion)
	}

	// frame_name should still match the designer's name (structure update
	// from the skeleton is fine; that's not authored content).
	if coldAfter.FrameName == nil || *coldAfter.FrameName != "Cold state" {
		t.Errorf("frame_name lost: %v", coldAfter.FrameName)
	}
}

// ─── Scenario 6: Frame restored after soft-delete ───────────────────────────

func TestAutoSkeleton_FrameRestored_ReusesID(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	sf := seedSubFlowForSkeleton(t, repo)

	// Pass 1: 2 frames.
	if err := repo.AutoSkeletonPRDStates(ctx, sf.ID, []FrameRow{
		makeFrame("1:1", "Cold state", 0),
		makeFrame("1:2", "Hot state", 100),
	}); err != nil {
		t.Fatalf("pass 1: %v", err)
	}
	full1, _ := repo.LoadPRD(ctx, sf.ID)
	var coldID string
	for _, st := range full1.Tabs[0].States {
		if st.Label == "Cold state" {
			coldID = st.ID
		}
	}

	// Pass 2: Cold removed.
	if err := repo.AutoSkeletonPRDStates(ctx, sf.ID, []FrameRow{
		makeFrame("1:2", "Hot state", 100),
	}); err != nil {
		t.Fatalf("pass 2: %v", err)
	}

	// Pass 3: Cold restored.
	if err := repo.AutoSkeletonPRDStates(ctx, sf.ID, []FrameRow{
		makeFrame("1:1", "Cold state", 0),
		makeFrame("1:2", "Hot state", 100),
	}); err != nil {
		t.Fatalf("pass 3: %v", err)
	}

	full3, _ := repo.LoadPRD(ctx, sf.ID)
	var coldIDAfter string
	for _, st := range full3.Tabs[0].States {
		if st.Label == "Cold state" {
			coldIDAfter = st.ID
			if st.DeletedAt != nil {
				t.Errorf("Cold state should be restored (deleted_at NULL), got %v", st.DeletedAt)
			}
		}
	}
	if coldIDAfter == "" {
		t.Fatal("Cold state not present after restore")
	}
	if coldIDAfter != coldID {
		t.Errorf("Cold state id changed across delete-restore: %s -> %s", coldID, coldIDAfter)
	}
}

// ─── Scenario 7: Empty section ──────────────────────────────────────────────

func TestAutoSkeleton_EmptySection(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	sf := seedSubFlowForSkeleton(t, repo)

	if err := repo.AutoSkeletonPRDStates(ctx, sf.ID, nil); err != nil {
		t.Fatalf("nil frames: %v", err)
	}
	if err := repo.AutoSkeletonPRDStates(ctx, sf.ID, []FrameRow{}); err != nil {
		t.Fatalf("empty frames: %v", err)
	}

	// PRD + tab should exist, with zero live states.
	full, err := repo.LoadPRD(ctx, sf.ID)
	if err != nil {
		t.Fatalf("LoadPRD: %v", err)
	}
	if len(full.Tabs) != 1 {
		t.Errorf("tab count: got %d, want 1", len(full.Tabs))
	}
	if len(full.Tabs[0].States) != 0 {
		t.Errorf("state count: got %d, want 0", len(full.Tabs[0].States))
	}
}

// All-default-named frames → same outcome as empty (skeleton skips all).
func TestAutoSkeleton_AllDefaultNames(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	sf := seedSubFlowForSkeleton(t, repo)

	frames := []FrameRow{
		makeFrame("1:1", "Frame 1", 0),
		makeFrame("1:2", "Rectangle 2", 100),
		makeFrame("1:3", "Group 3", 200),
		makeFrame("1:4", "Union", 300),
	}
	if err := repo.AutoSkeletonPRDStates(ctx, sf.ID, frames); err != nil {
		t.Fatalf("AutoSkeletonPRDStates: %v", err)
	}
	full, _ := repo.LoadPRD(ctx, sf.ID)
	if len(full.Tabs) != 1 {
		t.Errorf("tab count: got %d, want 1", len(full.Tabs))
	}
	if len(full.Tabs[0].States) != 0 {
		t.Errorf("expected zero skeleton states from all-default-named frames, got %d", len(full.Tabs[0].States))
	}
}

// ─── Scenario 8: Validation guards ──────────────────────────────────────────

func TestAutoSkeleton_RequiresInputs(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	// No tenant.
	emptyRepo := NewTenantRepo(d.DB, "")
	if err := emptyRepo.AutoSkeletonPRDStates(ctx, "irrelevant", nil); err == nil {
		t.Error("expected error when tenant_id is empty")
	}
	// No sub_flow_id.
	if err := repo.AutoSkeletonPRDStates(ctx, "", nil); err == nil {
		t.Error("expected error when sub_flow_id is empty")
	}
}

// ─── Scenario 9: Tenant isolation ───────────────────────────────────────────

func TestAutoSkeleton_TenantIsolation(t *testing.T) {
	d, tA, tB, _ := newTestDB(t)
	repoA := NewTenantRepo(d.DB, tA)
	repoB := NewTenantRepo(d.DB, tB)
	ctx := context.Background()

	// Tenant A seeds a sub_flow and runs skeleton.
	_, sfA := seedSubFlow(t, repoA, "Wallet", "M2M Settlement")
	frames := []FrameRow{
		makeFrame("1:1", "Cold state", 0),
		makeFrame("1:2", "Hot state", 100),
	}
	if err := repoA.AutoSkeletonPRDStates(ctx, sfA.ID, frames); err != nil {
		t.Fatalf("tenant A skeleton: %v", err)
	}

	// Tenant B has its own (different) sub_flow with the same product name.
	_, sfB := seedSubFlow(t, repoB, "Wallet", "M2M Settlement")
	if sfA.ID == sfB.ID {
		t.Fatal("sub_flow ids collided across tenants — newTestDB invariant broken")
	}

	// Tenant B should NOT see tenant A's PRD or states.
	if _, err := repoB.LoadPRD(ctx, sfA.ID); err == nil {
		t.Error("tenant B leaked into tenant A's PRD via sub_flow_id")
	}

	// Tenant B's own sub_flow has no PRD yet.
	if _, err := repoB.LoadPRD(ctx, sfB.ID); err == nil {
		t.Error("tenant B unexpectedly already has a PRD before skeleton ran")
	}

	// Run skeleton for tenant B with different labels.
	framesB := []FrameRow{
		makeFrame("2:1", "Init", 0),
		makeFrame("2:2", "Done", 100),
	}
	if err := repoB.AutoSkeletonPRDStates(ctx, sfB.ID, framesB); err != nil {
		t.Fatalf("tenant B skeleton: %v", err)
	}

	// Each tenant sees only its own labels.
	gotA := liveStateLabels(t, repoA, sfA.ID)
	gotB := liveStateLabels(t, repoB, sfB.ID)

	wantA := []string{"Cold state", "Hot state"}
	wantB := []string{"Init", "Done"}

	sort.Strings(gotA)
	sort.Strings(gotB)
	sort.Strings(wantA)
	sort.Strings(wantB)

	if !stringSlicesEqual(gotA, wantA) {
		t.Errorf("tenant A states: got %v, want %v", gotA, wantA)
	}
	if !stringSlicesEqual(gotB, wantB) {
		t.Errorf("tenant B states: got %v, want %v", gotB, wantB)
	}
}
