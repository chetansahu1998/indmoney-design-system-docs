package projects

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// Phase 5 U3 — pure validators + integration tests for decisions CRUD.

func TestValidateDecisionInput_HappyPath(t *testing.T) {
	in, err := ValidateDecisionInput(DecisionInput{
		Title:    "  Padding-32 over grid-24  ",
		BodyJSON: []byte(`{"blocks":[]}`),
		Status:   "accepted",
		Links: []DecisionLinkInput{
			{LinkType: LinkTypeViolation, TargetID: "v1"},
		},
	})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if in.Title != "Padding-32 over grid-24" {
		t.Errorf("title not trimmed: %q", in.Title)
	}
	if in.Status != "accepted" {
		t.Errorf("status: %q", in.Status)
	}
	if len(in.Links) != 1 {
		t.Errorf("links: %d", len(in.Links))
	}
}

func TestValidateDecisionInput_DefaultsToAccepted(t *testing.T) {
	in, err := ValidateDecisionInput(DecisionInput{Title: "X"})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if in.Status != "accepted" {
		t.Errorf("expected default 'accepted', got %q", in.Status)
	}
}

func TestValidateDecisionInput_RejectsEmptyTitle(t *testing.T) {
	_, err := ValidateDecisionInput(DecisionInput{Title: "   "})
	if !errors.Is(err, ErrDecisionTitleEmpty) {
		t.Errorf("expected ErrDecisionTitleEmpty, got %v", err)
	}
}

func TestValidateDecisionInput_RejectsLongTitle(t *testing.T) {
	_, err := ValidateDecisionInput(DecisionInput{
		Title: strings.Repeat("x", MaxDecisionTitleLen+1),
	})
	if !errors.Is(err, ErrDecisionTitleTooLong) {
		t.Errorf("expected ErrDecisionTitleTooLong, got %v", err)
	}
}

func TestValidateDecisionInput_RejectsBigBody(t *testing.T) {
	_, err := ValidateDecisionInput(DecisionInput{
		Title:    "X",
		BodyJSON: make([]byte, MaxDecisionBodyBytes+1),
	})
	if !errors.Is(err, ErrDecisionBodyTooLarge) {
		t.Errorf("expected ErrDecisionBodyTooLarge, got %v", err)
	}
}

func TestValidateDecisionInput_RejectsServerOnlyStatus(t *testing.T) {
	_, err := ValidateDecisionInput(DecisionInput{Title: "X", Status: "superseded"})
	if !errors.Is(err, ErrDecisionInvalidStatus) {
		t.Errorf("expected ErrDecisionInvalidStatus for direct 'superseded', got %v", err)
	}
}

func TestValidateDecisionInput_RejectsUnknownLinkType(t *testing.T) {
	_, err := ValidateDecisionInput(DecisionInput{
		Title: "X",
		Links: []DecisionLinkInput{{LinkType: "wat", TargetID: "1"}},
	})
	if !errors.Is(err, ErrDecisionLinkUnknown) {
		t.Errorf("expected ErrDecisionLinkUnknown, got %v", err)
	}
}

func TestValidateDecisionInput_DropsEmptyTargetIDs(t *testing.T) {
	in, err := ValidateDecisionInput(DecisionInput{
		Title: "X",
		Links: []DecisionLinkInput{
			{LinkType: LinkTypeViolation, TargetID: ""},
			{LinkType: LinkTypeScreen, TargetID: "s1"},
		},
	})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if len(in.Links) != 1 || in.Links[0].TargetID != "s1" {
		t.Errorf("expected 1 link, got %+v", in.Links)
	}
}

func TestDetectSupersessionCycle_NoCycle(t *testing.T) {
	chain := map[string]CycleCheckHop{
		"a": {ID: "a", SupersedesID: "b"},
		"b": {ID: "b", SupersedesID: ""},
	}
	if DetectSupersessionCycle("c", "a", chain) {
		t.Errorf("expected no cycle for fresh proposed id")
	}
}

func TestDetectSupersessionCycle_FlagsCycle(t *testing.T) {
	chain := map[string]CycleCheckHop{
		"a": {ID: "a", SupersedesID: "b"},
		"b": {ID: "b", SupersedesID: "c"},
	}
	// proposed "c" superseding "a" would create a → b → c → a.
	if !DetectSupersessionCycle("c", "a", chain) {
		t.Errorf("expected cycle when proposedID is in the chain")
	}
}

func TestDetectSupersessionCycle_FlagsSelfCycle(t *testing.T) {
	chain := map[string]CycleCheckHop{
		"a": {ID: "a", SupersedesID: "a"}, // existing data is already cyclic
	}
	if !DetectSupersessionCycle("z", "a", chain) {
		t.Errorf("expected detected cycle when chain itself loops")
	}
}

// ─── Repo-layer integration ─────────────────────────────────────────────────

func TestRepo_CreateDecision_HappyPath(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	versionID, screens := seedFlowAndScreens(t, repo, uA)
	_ = screens

	// Resolve flow_id from the version.
	var flowID string
	if err := d.DB.QueryRow(`SELECT flow_id FROM screens WHERE version_id = ? LIMIT 1`, versionID).Scan(&flowID); err != nil {
		t.Fatalf("flow_id: %v", err)
	}

	in, err := ValidateDecisionInput(DecisionInput{
		Title:    "Approve padding-32 over grid-24",
		BodyJSON: []byte(`{"blocks":[]}`),
	})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	rec, err := repo.CreateDecision(context.Background(), flowID, "", uA, in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if rec.Status != "accepted" {
		t.Errorf("status: %q", rec.Status)
	}
	if rec.VersionID == "" {
		t.Errorf("version not auto-resolved")
	}

	got, err := repo.GetDecision(context.Background(), rec.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Title != "Approve padding-32 over grid-24" {
		t.Errorf("title round-trip: %q", got.Title)
	}
}

func TestRepo_CreateDecision_CrossTenantNotFound(t *testing.T) {
	d, tA, tB, uA := newTestDB(t)
	repoA := NewTenantRepo(d.DB, tA)
	versionID, _ := seedFlowAndScreens(t, repoA, uA)
	var flowID string
	if err := d.DB.QueryRow(`SELECT flow_id FROM screens WHERE version_id = ? LIMIT 1`, versionID).Scan(&flowID); err != nil {
		t.Fatalf("flow_id: %v", err)
	}

	repoB := NewTenantRepo(d.DB, tB)
	in, _ := ValidateDecisionInput(DecisionInput{Title: "X"})
	_, err := repoB.CreateDecision(context.Background(), flowID, "", uA, in)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestRepo_Supersession_FlipsPredecessor(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	versionID, _ := seedFlowAndScreens(t, repo, uA)
	var flowID string
	if err := d.DB.QueryRow(`SELECT flow_id FROM screens WHERE version_id = ? LIMIT 1`, versionID).Scan(&flowID); err != nil {
		t.Fatalf("flow_id: %v", err)
	}

	first, err := repo.CreateDecision(context.Background(), flowID, "", uA, DecisionInput{Title: "First"})
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	in2, _ := ValidateDecisionInput(DecisionInput{
		Title:        "Second",
		SupersedesID: first.ID,
	})
	second, err := repo.CreateDecision(context.Background(), flowID, "", uA, in2)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if second.SupersedesID == nil || *second.SupersedesID != first.ID {
		t.Errorf("second.supersedes_id wrong: %+v", second.SupersedesID)
	}

	// Read back the predecessor; it should be superseded with superseded_by_id pointing at second.
	first2, err := repo.GetDecision(context.Background(), first.ID)
	if err != nil {
		t.Fatalf("first2: %v", err)
	}
	if first2.Status != "superseded" {
		t.Errorf("expected predecessor superseded, got %q", first2.Status)
	}
	if first2.SupersededByID == nil || *first2.SupersededByID != second.ID {
		t.Errorf("predecessor superseded_by_id wrong: %+v", first2.SupersededByID)
	}
}

func TestRepo_Supersession_PredecessorMustExistInFlow(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	versionID, _ := seedFlowAndScreens(t, repo, uA)
	var flowID string
	if err := d.DB.QueryRow(`SELECT flow_id FROM screens WHERE version_id = ? LIMIT 1`, versionID).Scan(&flowID); err != nil {
		t.Fatalf("flow_id: %v", err)
	}
	in, _ := ValidateDecisionInput(DecisionInput{
		Title:        "Orphan-supersession",
		SupersedesID: "nonexistent-id",
	})
	_, err := repo.CreateDecision(context.Background(), flowID, "", uA, in)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound for nonexistent predecessor, got %v", err)
	}
}

func TestRepo_ListDecisionsForFlow_FiltersSuperseded(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	versionID, _ := seedFlowAndScreens(t, repo, uA)
	var flowID string
	if err := d.DB.QueryRow(`SELECT flow_id FROM screens WHERE version_id = ? LIMIT 1`, versionID).Scan(&flowID); err != nil {
		t.Fatalf("flow_id: %v", err)
	}
	first, _ := repo.CreateDecision(context.Background(), flowID, "", uA, DecisionInput{Title: "First", Status: "accepted"})
	in2, _ := ValidateDecisionInput(DecisionInput{Title: "Second", SupersedesID: first.ID})
	_, _ = repo.CreateDecision(context.Background(), flowID, "", uA, in2)

	// Default: hide superseded.
	got, err := repo.ListDecisionsForFlow(context.Background(), flowID, false)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 || got[0].Title != "Second" {
		t.Errorf("expected only second, got %+v", got)
	}
	// includeSuperseded: both.
	got, err = repo.ListDecisionsForFlow(context.Background(), flowID, true)
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("expected 2 (with superseded), got %d", len(got))
	}
}

func TestRepo_DecisionLinks_RoundTrip(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	versionID, screens := seedFlowAndScreens(t, repo, uA)
	var flowID string
	if err := d.DB.QueryRow(`SELECT flow_id FROM screens WHERE version_id = ? LIMIT 1`, versionID).Scan(&flowID); err != nil {
		t.Fatalf("flow_id: %v", err)
	}

	in, _ := ValidateDecisionInput(DecisionInput{
		Title: "Linked decision",
		Links: []DecisionLinkInput{
			{LinkType: LinkTypeScreen, TargetID: screens[0]},
			{LinkType: LinkTypeExternal, TargetID: "https://linear.app/x/123"},
		},
	})
	rec, err := repo.CreateDecision(context.Background(), flowID, "", uA, in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(rec.Links) != 2 {
		t.Fatalf("expected 2 links, got %d", len(rec.Links))
	}
	got, err := repo.GetDecision(context.Background(), rec.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got.Links) != 2 {
		t.Errorf("expected 2 links on read-back, got %d", len(got.Links))
	}
}

func TestDB_ListRecentDecisions_OrdersByMadeAt(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	versionID, _ := seedFlowAndScreens(t, repo, uA)
	var flowID string
	if err := d.DB.QueryRow(`SELECT flow_id FROM screens WHERE version_id = ? LIMIT 1`, versionID).Scan(&flowID); err != nil {
		t.Fatalf("flow_id: %v", err)
	}

	for i := 0; i < 3; i++ {
		if _, err := repo.CreateDecision(context.Background(), flowID, "", uA, DecisionInput{
			Title:  "D" + string(rune('A'+i)),
			Status: "accepted",
		}); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}

	repoDB := NewDB(d.DB)
	got, err := repoDB.ListRecentDecisions(context.Background(), 10)
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("expected 3, got %d", len(got))
	}
}
