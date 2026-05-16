package projects

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// prd_test.go — U4 integration tests for PRD typed-stems repo.
// Mirrors decisions_test.go's table-test style + setup helpers.

// seedSubFlow creates a (sub_product, sub_flow) pair so PRD tests can hang
// off a real sub_flow row (the FK constraint requires this).
func seedSubFlow(t *testing.T, repo *TenantRepo, productName, flowName string) (SubProduct, SubFlow) {
	t.Helper()
	ctx := context.Background()
	sp, err := repo.UpsertSubProduct(ctx, productName)
	if err != nil {
		t.Fatalf("UpsertSubProduct: %v", err)
	}
	sf, err := repo.UpsertSubFlow(ctx, sp.ID, flowName)
	if err != nil {
		t.Fatalf("UpsertSubFlow: %v", err)
	}
	return sp, sf
}

// seedPRDWithTab is the common scaffold for tests that need a working PRD.
func seedPRDWithTab(t *testing.T, repo *TenantRepo) (SubFlow, PRD, PRDTab) {
	t.Helper()
	ctx := context.Background()
	_, sf := seedSubFlow(t, repo, "Wallet", "M2M Settlement")
	prd, err := repo.UpsertPRD(ctx, PRDInput{
		SubFlowID: sf.ID,
		Title:     "Wallet — M2M Settlement",
		SummaryMD: "PRD scaffold for tests.",
	})
	if err != nil {
		t.Fatalf("UpsertPRD: %v", err)
	}
	tab, err := repo.UpsertPRDTab(ctx, PRDTabInput{
		PRDID:    prd.ID,
		Name:     "Investment",
		Position: 0,
	})
	if err != nil {
		t.Fatalf("UpsertPRDTab: %v", err)
	}
	return sf, prd, tab
}

// ─── Scenario 1: Happy-path round-trip ───────────────────────────────────────

func TestPRD_RoundTrip_HappyPath(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	sf, _, tab := seedPRDWithTab(t, repo)

	// 3 states.
	states := make([]PRDState, 0, 3)
	for i, label := range []string{"Cold state", "Hot state", "Empty state"} {
		st, err := repo.UpsertPRDState(ctx, PRDStateInput{
			PRDTabID: tab.ID,
			Label:    label,
			Position: i,
		})
		if err != nil {
			t.Fatalf("UpsertPRDState %s: %v", label, err)
		}
		states = append(states, st)
	}

	// 6 acceptance criteria — 2 per state.
	for _, st := range states {
		for i := 0; i < 2; i++ {
			if _, err := repo.AddAcceptanceCriterion(ctx, AcceptanceCriterionInput{
				PRDStateID: st.ID,
				Position:   i,
				Criterion:  "Criterion " + st.Label + " " + string(rune('A'+i)),
			}); err != nil {
				t.Fatalf("AddAcceptanceCriterion: %v", err)
			}
		}
	}

	// 4 events spread across the first state.
	for i, name := range []string{
		"wallet.m2m_settlement.cold_state_viewed",
		"wallet.m2m_settlement.cta_clicked",
		"wallet.m2m_settlement.help_opened",
		"wallet.m2m_settlement.dismissed",
	} {
		if _, err := repo.AddEvent(ctx, EventInput{
			PRDStateID:       states[0].ID,
			Position:         i,
			Name:             name,
			PropertiesSchema: `{"reason":"string"}`,
			FiresOn:          "screen_viewed",
		}); err != nil {
			t.Fatalf("AddEvent %s: %v", name, err)
		}
	}

	// 2 frame tags on the hot state, one per platform variant.
	for _, variant := range []string{"android", "ios"} {
		if _, err := repo.AttachFrameTag(ctx, FrameTagInput{
			PRDStateID:  states[1].ID,
			FigmaNodeID: "1:42",
			Variant:     variant,
		}); err != nil {
			t.Fatalf("AttachFrameTag %s: %v", variant, err)
		}
	}

	// LoadPRD returns full nested structure with stems in position order.
	full, err := repo.LoadPRD(ctx, sf.ID)
	if err != nil {
		t.Fatalf("LoadPRD: %v", err)
	}
	if full.Title != "Wallet — M2M Settlement" {
		t.Errorf("title round-trip: %q", full.Title)
	}
	if len(full.Tabs) != 1 {
		t.Fatalf("expected 1 tab, got %d", len(full.Tabs))
	}
	gotTab := full.Tabs[0]
	if len(gotTab.States) != 3 {
		t.Fatalf("expected 3 states, got %d", len(gotTab.States))
	}
	// States in position order.
	for i, want := range []string{"Cold state", "Hot state", "Empty state"} {
		if gotTab.States[i].Label != want {
			t.Errorf("state[%d] label = %q, want %q", i, gotTab.States[i].Label, want)
		}
	}
	if len(gotTab.States[0].AcceptanceCriteria) != 2 {
		t.Errorf("cold state criteria = %d, want 2", len(gotTab.States[0].AcceptanceCriteria))
	}
	if len(gotTab.States[0].Events) != 4 {
		t.Errorf("cold state events = %d, want 4", len(gotTab.States[0].Events))
	}
	if len(gotTab.States[1].FrameTags) != 2 {
		t.Errorf("hot state frame tags = %d, want 2", len(gotTab.States[1].FrameTags))
	}
}

// ─── Scenario 2: UpsertPRD idempotent ────────────────────────────────────────

func TestPRD_UpsertIdempotent(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	_, sf := seedSubFlow(t, repo, "Wallet", "M2M")
	prd1, err := repo.UpsertPRD(ctx, PRDInput{SubFlowID: sf.ID, Title: "First"})
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	prd2, err := repo.UpsertPRD(ctx, PRDInput{SubFlowID: sf.ID, Title: "Second"})
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if prd1.ID != prd2.ID {
		t.Errorf("expected idempotent id; got %s vs %s", prd1.ID, prd2.ID)
	}
	if prd2.Title != "Second" {
		t.Errorf("title not updated: %q", prd2.Title)
	}
}

// ─── Scenario 3: UpsertPRDTab idempotent on (prd_id, name) ──────────────────

func TestPRDTab_UpsertIdempotent(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	_, prd, _ := seedPRDWithTab(t, repo)

	tab1, err := repo.UpsertPRDTab(ctx, PRDTabInput{
		PRDID: prd.ID, Name: "Banks", Position: 1, OverviewMD: "first overview",
	})
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	// Case-insensitive idempotency.
	tab2, err := repo.UpsertPRDTab(ctx, PRDTabInput{
		PRDID: prd.ID, Name: "  BANKS  ", Position: 7, OverviewMD: "second overview",
	})
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if tab1.ID != tab2.ID {
		t.Errorf("expected idempotent id; got %s vs %s", tab1.ID, tab2.ID)
	}
	if tab2.Position != 7 || tab2.OverviewMD != "second overview" {
		t.Errorf("fields not updated: %+v", tab2)
	}
}

// ─── Scenario 4: UpsertPRDState idempotent on (prd_tab_id, label) ───────────

func TestPRDState_UpsertIdempotent(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	_, _, tab := seedPRDWithTab(t, repo)

	st1, err := repo.UpsertPRDState(ctx, PRDStateInput{
		PRDTabID: tab.ID, Label: "Cold state", Position: 0, ConditionMD: "v1",
	})
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	st2, err := repo.UpsertPRDState(ctx, PRDStateInput{
		PRDTabID: tab.ID, Label: "cold state", Position: 3, ConditionMD: "v2",
	})
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if st1.ID != st2.ID {
		t.Errorf("expected idempotent id; got %s vs %s", st1.ID, st2.ID)
	}
	if st2.Position != 3 || st2.ConditionMD != "v2" {
		t.Errorf("fields not updated: %+v", st2)
	}
}

// ─── Scenario 5: SoftDelete + Restore preserve authored content ──────────────

func TestPRDState_SoftDeleteRestore_PreservesStems(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	sf, _, tab := seedPRDWithTab(t, repo)

	st, err := repo.UpsertPRDState(ctx, PRDStateInput{
		PRDTabID: tab.ID, Label: "Empty state",
	})
	if err != nil {
		t.Fatalf("UpsertPRDState: %v", err)
	}
	if _, err := repo.AddAcceptanceCriterion(ctx, AcceptanceCriterionInput{
		PRDStateID: st.ID, Criterion: "Show empty illustration",
	}); err != nil {
		t.Fatalf("AddAcceptanceCriterion: %v", err)
	}
	if _, err := repo.AddEvent(ctx, EventInput{
		PRDStateID: st.ID, Name: "wallet.empty.viewed",
	}); err != nil {
		t.Fatalf("AddEvent: %v", err)
	}

	// Soft-delete hides the state from LoadPRD.
	if err := repo.SoftDeletePRDState(ctx, st.ID); err != nil {
		t.Fatalf("SoftDeletePRDState: %v", err)
	}
	full, err := repo.LoadPRD(ctx, sf.ID)
	if err != nil {
		t.Fatalf("LoadPRD after soft-delete: %v", err)
	}
	if len(full.Tabs) != 1 || len(full.Tabs[0].States) != 0 {
		t.Fatalf("soft-deleted state still visible: %+v", full.Tabs)
	}

	// Restore via UpsertPRDState with the same label preserves id and stems.
	st2, err := repo.UpsertPRDState(ctx, PRDStateInput{
		PRDTabID: tab.ID, Label: "Empty state",
	})
	if err != nil {
		t.Fatalf("restore via Upsert: %v", err)
	}
	if st2.ID != st.ID {
		t.Errorf("restore returned new id %s, want %s", st2.ID, st.ID)
	}
	full, err = repo.LoadPRD(ctx, sf.ID)
	if err != nil {
		t.Fatalf("LoadPRD after restore: %v", err)
	}
	if len(full.Tabs[0].States) != 1 {
		t.Fatalf("expected restored state visible, got %d", len(full.Tabs[0].States))
	}
	rs := full.Tabs[0].States[0]
	if len(rs.AcceptanceCriteria) != 1 {
		t.Errorf("criteria lost across soft-delete cycle: %d", len(rs.AcceptanceCriteria))
	}
	if len(rs.Events) != 1 {
		t.Errorf("events lost across soft-delete cycle: %d", len(rs.Events))
	}
}

// ─── Scenario 6: AddAcceptanceCriterion preserves position order ────────────

func TestAcceptanceCriterion_PositionOrder(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	sf, _, tab := seedPRDWithTab(t, repo)
	st, err := repo.UpsertPRDState(ctx, PRDStateInput{PRDTabID: tab.ID, Label: "S"})
	if err != nil {
		t.Fatalf("state: %v", err)
	}

	// Insert in non-monotonic call order; LoadPRD returns by position.
	wants := []string{"first", "second", "third"}
	for i, txt := range []string{"second", "third", "first"} {
		pos := -1
		for p, w := range wants {
			if w == txt {
				pos = p
			}
		}
		_ = i
		if _, err := repo.AddAcceptanceCriterion(ctx, AcceptanceCriterionInput{
			PRDStateID: st.ID, Position: pos, Criterion: txt,
		}); err != nil {
			t.Fatalf("add %s: %v", txt, err)
		}
	}

	full, err := repo.LoadPRD(ctx, sf.ID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	got := full.Tabs[0].States[0].AcceptanceCriteria
	if len(got) != 3 {
		t.Fatalf("expected 3 criteria, got %d", len(got))
	}
	for i, w := range wants {
		if got[i].Criterion != w {
			t.Errorf("criteria[%d] = %q, want %q", i, got[i].Criterion, w)
		}
	}
}

// ─── Scenario 7: AddEvent accepts any non-empty name (no validation) ────────

func TestEvent_NoNameShapeValidation(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	_, _, tab := seedPRDWithTab(t, repo)
	st, err := repo.UpsertPRDState(ctx, PRDStateInput{PRDTabID: tab.ID, Label: "S"})
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	// All three accepted: dotted, snake, no-dots.
	for _, name := range []string{
		"wallet.m2m.cold_viewed",
		"random_string",
		"foo.bar.baz",
	} {
		if _, err := repo.AddEvent(ctx, EventInput{PRDStateID: st.ID, Name: name}); err != nil {
			t.Errorf("AddEvent(%q) failed: %v", name, err)
		}
	}
}

// ─── Scenario 8: AddEvent idempotent on (prd_state_id, name) ────────────────

func TestEvent_IdempotentOnName(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	_, _, tab := seedPRDWithTab(t, repo)
	st, _ := repo.UpsertPRDState(ctx, PRDStateInput{PRDTabID: tab.ID, Label: "S"})

	e1, err := repo.AddEvent(ctx, EventInput{
		PRDStateID: st.ID, Name: "wallet.x.viewed", FiresOn: "viewed",
	})
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	e2, err := repo.AddEvent(ctx, EventInput{
		PRDStateID: st.ID, Name: "wallet.x.viewed", FiresOn: "screen_loaded",
		PropertiesSchema: `{"x":"int"}`,
	})
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if e1.ID != e2.ID {
		t.Errorf("expected idempotent id; got %s vs %s", e1.ID, e2.ID)
	}
	if e2.FiresOn != "screen_loaded" || e2.PropertiesSchema != `{"x":"int"}` {
		t.Errorf("fields not updated: %+v", e2)
	}
}

// ─── Scenario 9: UpsertCopyString unique on (state, key, locale) ────────────

func TestCopyString_UniqueOnKeyLocale(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	_, _, tab := seedPRDWithTab(t, repo)
	st, _ := repo.UpsertPRDState(ctx, PRDStateInput{PRDTabID: tab.ID, Label: "S"})

	cs1, err := repo.UpsertCopyString(ctx, CopyStringInput{
		PRDStateID: st.ID, Key: "cta_label", Value: "Continue",
	})
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	cs2, err := repo.UpsertCopyString(ctx, CopyStringInput{
		PRDStateID: st.ID, Key: "cta_label", Value: "Get started",
	})
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if cs1.ID != cs2.ID {
		t.Errorf("expected idempotent id; got %s vs %s", cs1.ID, cs2.ID)
	}
	if cs2.Value != "Get started" {
		t.Errorf("value not updated: %q", cs2.Value)
	}

	// Different locale ⇒ new row.
	cs3, err := repo.UpsertCopyString(ctx, CopyStringInput{
		PRDStateID: st.ID, Key: "cta_label", Value: "Continuar", Locale: "es",
	})
	if err != nil {
		t.Fatalf("es: %v", err)
	}
	if cs3.ID == cs2.ID {
		t.Errorf("different locale should be a new row")
	}
}

// ─── Scenario 10: AttachFrameTag with variant ───────────────────────────────

func TestFrameTag_VariantUniqueness(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	_, _, tab := seedPRDWithTab(t, repo)
	st, _ := repo.UpsertPRDState(ctx, PRDStateInput{PRDTabID: tab.ID, Label: "S"})

	if _, err := repo.AttachFrameTag(ctx, FrameTagInput{
		PRDStateID: st.ID, FigmaNodeID: "1:42", Variant: "android",
	}); err != nil {
		t.Fatalf("android: %v", err)
	}
	if _, err := repo.AttachFrameTag(ctx, FrameTagInput{
		PRDStateID: st.ID, FigmaNodeID: "1:42", Variant: "ios",
	}); err != nil {
		t.Fatalf("ios: %v", err)
	}
	// Same variant ⇒ duplicate.
	if _, err := repo.AttachFrameTag(ctx, FrameTagInput{
		PRDStateID: st.ID, FigmaNodeID: "1:42", Variant: "android",
	}); !errors.Is(err, ErrPRDFrameTagDuplicate) {
		t.Errorf("expected ErrPRDFrameTagDuplicate, got %v", err)
	}

	// NULL variant + non-NULL variant coexist.
	if _, err := repo.AttachFrameTag(ctx, FrameTagInput{
		PRDStateID: st.ID, FigmaNodeID: "1:42",
	}); err != nil {
		t.Fatalf("NULL variant: %v", err)
	}
	// Re-running NULL variant duplicates.
	if _, err := repo.AttachFrameTag(ctx, FrameTagInput{
		PRDStateID: st.ID, FigmaNodeID: "1:42",
	}); !errors.Is(err, ErrPRDFrameTagDuplicate) {
		t.Errorf("expected duplicate on second NULL variant, got %v", err)
	}
}

// ─── Scenario 12: Cascade delete from sub_flow ──────────────────────────────

func TestPRD_CascadeDeleteFromSubFlow(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	sf, _, tab := seedPRDWithTab(t, repo)
	st, _ := repo.UpsertPRDState(ctx, PRDStateInput{PRDTabID: tab.ID, Label: "Cold"})
	if _, err := repo.AddAcceptanceCriterion(ctx, AcceptanceCriterionInput{
		PRDStateID: st.ID, Criterion: "C",
	}); err != nil {
		t.Fatalf("add criterion: %v", err)
	}
	if _, err := repo.AttachFrameTag(ctx, FrameTagInput{
		PRDStateID: st.ID, FigmaNodeID: "1:1",
	}); err != nil {
		t.Fatalf("attach: %v", err)
	}

	if _, err := d.DB.ExecContext(ctx, `DELETE FROM sub_flow WHERE tenant_id = ? AND id = ?`, tA, sf.ID); err != nil {
		t.Fatalf("delete sub_flow: %v", err)
	}

	// Every dependent row should be gone.
	for _, tbl := range []string{
		"prd", "prd_tab", "prd_state",
		"prd_state_acceptance_criterion",
		"frame_tag",
	} {
		var n int
		if err := d.DB.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM "+tbl+" WHERE tenant_id = ?", tA,
		).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", tbl, err)
		}
		if n != 0 {
			t.Errorf("%s not cascaded; %d rows remain", tbl, n)
		}
	}
}

// ─── Scenario 13: Tenant isolation ──────────────────────────────────────────

func TestPRD_TenantIsolation(t *testing.T) {
	d, tA, tB, _ := newTestDB(t)
	repoA := NewTenantRepo(d.DB, tA)
	repoB := NewTenantRepo(d.DB, tB)
	ctx := context.Background()

	_, sfA := seedSubFlow(t, repoA, "Wallet", "M2M")
	if _, err := repoA.UpsertPRD(ctx, PRDInput{SubFlowID: sfA.ID, Title: "A-side"}); err != nil {
		t.Fatalf("upsert A: %v", err)
	}

	// Tenant B can't see A's PRD via LoadPRD (different sub_flow ids per tenant).
	if _, err := repoB.LoadPRD(ctx, sfA.ID); !errors.Is(err, ErrPRDNotFound) {
		t.Errorf("tenant B should not see tenant A's PRD, got %v", err)
	}
}

// ─── Scenario 14: Length-cap validation ─────────────────────────────────────

func TestAcceptanceCriterion_LengthCap(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()
	_, _, tab := seedPRDWithTab(t, repo)
	st, _ := repo.UpsertPRDState(ctx, PRDStateInput{PRDTabID: tab.ID, Label: "S"})

	_, err := repo.AddAcceptanceCriterion(ctx, AcceptanceCriterionInput{
		PRDStateID: st.ID,
		Criterion:  strings.Repeat("x", MaxAcceptanceCriterionLen+1),
	})
	if !errors.Is(err, ErrPRDInvalidInput) {
		t.Errorf("expected ErrPRDInvalidInput, got %v", err)
	}
}

// ─── Scenario 15: Empty inputs rejected ─────────────────────────────────────

func TestPRD_EmptyInputs(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()
	_, _, tab := seedPRDWithTab(t, repo)
	st, _ := repo.UpsertPRDState(ctx, PRDStateInput{PRDTabID: tab.ID, Label: "S"})

	cases := []struct {
		name string
		err  error
		fn   func() error
	}{
		{"tab name empty", ErrPRDTabNameEmpty, func() error {
			_, err := repo.UpsertPRDTab(ctx, PRDTabInput{PRDID: "x", Name: "   "})
			return err
		}},
		{"state label empty", ErrPRDStateLabelEmpty, func() error {
			_, err := repo.UpsertPRDState(ctx, PRDStateInput{PRDTabID: tab.ID, Label: ""})
			return err
		}},
		{"event name empty", ErrPRDEventNameEmpty, func() error {
			_, err := repo.AddEvent(ctx, EventInput{PRDStateID: st.ID, Name: "  "})
			return err
		}},
		{"criterion empty", ErrPRDCriterionEmpty, func() error {
			_, err := repo.AddAcceptanceCriterion(ctx, AcceptanceCriterionInput{PRDStateID: st.ID, Criterion: ""})
			return err
		}},
		{"edge case empty", ErrPRDEdgeCaseEmpty, func() error {
			_, err := repo.AddEdgeCase(ctx, EdgeCaseInput{PRDStateID: st.ID, EdgeCase: ""})
			return err
		}},
		{"a11y note empty", ErrPRDA11yNoteEmpty, func() error {
			_, err := repo.AddA11yNote(ctx, A11yNoteInput{PRDStateID: st.ID, Note: ""})
			return err
		}},
		{"copy key empty", ErrPRDCopyKeyEmpty, func() error {
			_, err := repo.UpsertCopyString(ctx, CopyStringInput{PRDStateID: st.ID, Key: "", Value: "x"})
			return err
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := c.fn(); !errors.Is(err, c.err) {
				t.Errorf("expected %v, got %v", c.err, err)
			}
		})
	}
}

// ─── Scenario 16: RenderPRDMarkdown deterministic ───────────────────────────

func TestRenderPRDMarkdown_Deterministic(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	sf, _, tab := seedPRDWithTab(t, repo)
	st, _ := repo.UpsertPRDState(ctx, PRDStateInput{
		PRDTabID: tab.ID, Label: "Cold", Position: 0, ConditionMD: "User has no balance",
	})
	if _, err := repo.AddAcceptanceCriterion(ctx, AcceptanceCriterionInput{
		PRDStateID: st.ID, Position: 0, Criterion: "Show empty illustration",
	}); err != nil {
		t.Fatalf("crit: %v", err)
	}
	if _, err := repo.AddEvent(ctx, EventInput{
		PRDStateID: st.ID, Name: "wallet.cold.viewed", FiresOn: "screen_viewed",
	}); err != nil {
		t.Fatalf("event: %v", err)
	}

	md1, err := repo.RenderPRDMarkdown(ctx, sf.ID)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	md2, err := repo.RenderPRDMarkdown(ctx, sf.ID)
	if err != nil {
		t.Fatalf("render2: %v", err)
	}
	if md1 != md2 {
		t.Errorf("render not deterministic\n--- run1 ---\n%s\n--- run2 ---\n%s", md1, md2)
	}
	// Sanity-check structure: top-level title, tab H2, state H4.
	for _, want := range []string{
		"# Wallet — M2M Settlement",
		"## Investment",
		"#### Cold",
		"- Show empty illustration",
		"`wallet.cold.viewed`",
	} {
		if !strings.Contains(md1, want) {
			t.Errorf("missing %q in render:\n%s", want, md1)
		}
	}
}

// ─── DetachFrameTag round-trip ──────────────────────────────────────────────

func TestFrameTag_Detach(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	_, _, tab := seedPRDWithTab(t, repo)
	st, _ := repo.UpsertPRDState(ctx, PRDStateInput{PRDTabID: tab.ID, Label: "S"})
	tag, err := repo.AttachFrameTag(ctx, FrameTagInput{
		PRDStateID: st.ID, FigmaNodeID: "1:1",
	})
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	if err := repo.DetachFrameTag(ctx, tag.ID); err != nil {
		t.Fatalf("detach: %v", err)
	}
	if err := repo.DetachFrameTag(ctx, tag.ID); !errors.Is(err, ErrPRDFrameTagNotFound) {
		t.Errorf("expected ErrPRDFrameTagNotFound on second detach, got %v", err)
	}
}
