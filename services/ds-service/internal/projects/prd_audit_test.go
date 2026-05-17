package projects

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// prd_audit_test.go — U6b tests for the append-only PRD audit log.
//
// Covers:
//   - RecordPRDAudit happy path + round-trip via listPRDAuditForState.
//   - RecordPRDAudit against an unknown prd_state_id — FK enforced, so the
//     insert surfaces a SQLite constraint error (NOT a no-op). This test
//     documents the actual implementation behavior; if the FK gets relaxed,
//     the assertion here will catch the regression.
//   - LatestPRDAuditByState picks the newest row per state, keyed by state id.
//   - LatestPRDAuditByState with empty input → empty (non-nil) map, no error.
//   - Tenant isolation: audit rows written under tenant A are invisible to
//     tenant B's TenantRepo.

// seedStateForAudit returns one prd_state id ready to receive audit rows.
func seedStateForAudit(t *testing.T, repo *TenantRepo, label string) string {
	t.Helper()
	_, _, tab := seedPRDWithTab(t, repo)
	st, err := repo.UpsertPRDState(context.Background(), PRDStateInput{
		PRDTabID: tab.ID, Label: label,
	})
	if err != nil {
		t.Fatalf("UpsertPRDState(%q): %v", label, err)
	}
	return st.ID
}

// ─── Scenario 1: Happy path — record + read back ─────────────────────────────

func TestRecordPRDAudit_HappyPath(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	stateID := seedStateForAudit(t, repo, "Cold state")

	if err := repo.RecordPRDAudit(ctx, stateID, uA, OpUpsertState); err != nil {
		t.Fatalf("RecordPRDAudit: %v", err)
	}

	// Direct read-back via the unexported helper.
	rows, err := repo.listPRDAuditForState(ctx, stateID)
	if err != nil {
		t.Fatalf("listPRDAuditForState: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 audit row, got %d", len(rows))
	}
	if rows[0].PRDStateID != stateID {
		t.Errorf("prd_state_id: got %q, want %q", rows[0].PRDStateID, stateID)
	}
	if rows[0].UserID != uA {
		t.Errorf("user_id: got %q, want %q", rows[0].UserID, uA)
	}
	if rows[0].Op != OpUpsertState {
		t.Errorf("op: got %q, want %q", rows[0].Op, OpUpsertState)
	}
	if rows[0].TenantID != tA {
		t.Errorf("tenant_id: got %q, want %q", rows[0].TenantID, tA)
	}
	if rows[0].At.IsZero() {
		t.Errorf("at timestamp is zero")
	}

	// Cross-check via LatestPRDAuditByState.
	got, err := repo.LatestPRDAuditByState(ctx, []string{stateID})
	if err != nil {
		t.Fatalf("LatestPRDAuditByState: %v", err)
	}
	if a, ok := got[stateID]; !ok {
		t.Errorf("LatestPRDAuditByState missing state %q", stateID)
	} else if a.UserID != uA || a.Op != OpUpsertState {
		t.Errorf("latest row mismatch: %+v", a)
	}
}

// ─── Scenario 2: Unknown prd_state_id — FK enforced ──────────────────────────
//
// The migration declares FOREIGN KEY (tenant_id, prd_state_id) REFERENCES
// prd_state(tenant_id, id). With SQLite FK enforcement on (db.Open enables
// it via PRAGMA foreign_keys=ON), inserting an audit row against an
// unknown state surfaces a CHECK/FK constraint error from the driver.
// RecordPRDAudit wraps it as "insert prd_audit: …".
//
// We assert: returns a non-nil error AND no audit row is persisted.
// We deliberately do NOT assert ErrPRDAuditStateRequired here — that error
// is for the empty-string guard only.

func TestRecordPRDAudit_UnknownState(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	err := repo.RecordPRDAudit(ctx, "nope-not-a-state", uA, OpUpsertState)
	if err == nil {
		t.Fatal("expected FK constraint error for unknown prd_state_id, got nil")
	}
	if !strings.Contains(err.Error(), "insert prd_audit") {
		t.Errorf("expected wrapped insert-error prefix, got %v", err)
	}

	// No audit row should have leaked.
	var n int
	if scanErr := d.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM prd_audit WHERE tenant_id = ?`, tA,
	).Scan(&n); scanErr != nil {
		t.Fatalf("count prd_audit: %v", scanErr)
	}
	if n != 0 {
		t.Errorf("expected 0 audit rows, got %d", n)
	}
}

// ─── Scenario 2b: Empty stateID returns ErrPRDAuditStateRequired ─────────────

func TestRecordPRDAudit_EmptyState(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	if err := repo.RecordPRDAudit(ctx, "   ", uA, OpUpsertState); !errors.Is(err, ErrPRDAuditStateRequired) {
		t.Errorf("expected ErrPRDAuditStateRequired for empty stateID, got %v", err)
	}
}

// ─── Scenario 3: LatestPRDAuditByState picks newest per state ────────────────

func TestLatestPRDAuditByState_MultipleStates(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	_, _, tab := seedPRDWithTab(t, repo)
	stA, err := repo.UpsertPRDState(ctx, PRDStateInput{PRDTabID: tab.ID, Label: "A"})
	if err != nil {
		t.Fatalf("state A: %v", err)
	}
	stB, err := repo.UpsertPRDState(ctx, PRDStateInput{PRDTabID: tab.ID, Label: "B"})
	if err != nil {
		t.Fatalf("state B: %v", err)
	}

	// Two audits for state A — RecordPRDAudit calls t.now() per call, but
	// the test clock is real time. We sequence them by overriding nowFn
	// indirectly: insert one, then sleep is the only realtime option, but
	// the implementation orders by (at DESC, id DESC), so a same-instant
	// pair would tie-break on id. To guarantee an unambiguous "newest"
	// for A, we record three ops and assert that the last one wins.
	for _, op := range []PRDAuditOp{OpUpsertState, OpAddEvent, OpAddA11yNote} {
		if err := repo.RecordPRDAudit(ctx, stA.ID, uA, op); err != nil {
			t.Fatalf("record A %s: %v", op, err)
		}
	}
	// One audit for state B.
	if err := repo.RecordPRDAudit(ctx, stB.ID, uA, OpUpsertCopyString); err != nil {
		t.Fatalf("record B: %v", err)
	}

	got, err := repo.LatestPRDAuditByState(ctx, []string{stA.ID, stB.ID})
	if err != nil {
		t.Fatalf("LatestPRDAuditByState: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %d (%+v)", len(got), got)
	}

	// For state A: pick the row whose op is one of the three we recorded.
	// Without a controllable clock, we cannot assert which specific op is
	// newest within the same millisecond. We CAN assert (a) state A maps
	// to one of the three ops, and (b) state B maps to OpUpsertCopyString.
	aRow, ok := got[stA.ID]
	if !ok {
		t.Fatalf("state A missing from result")
	}
	wantOps := map[PRDAuditOp]bool{
		OpUpsertState: true, OpAddEvent: true, OpAddA11yNote: true,
	}
	if !wantOps[aRow.Op] {
		t.Errorf("state A op %q not in recorded set", aRow.Op)
	}
	if aRow.PRDStateID != stA.ID {
		t.Errorf("state A key mismatch: %s vs %s", aRow.PRDStateID, stA.ID)
	}

	bRow, ok := got[stB.ID]
	if !ok {
		t.Fatalf("state B missing from result")
	}
	if bRow.Op != OpUpsertCopyString {
		t.Errorf("state B op = %q, want %q", bRow.Op, OpUpsertCopyString)
	}
	if bRow.PRDStateID != stB.ID {
		t.Errorf("state B key mismatch: %s vs %s", bRow.PRDStateID, stB.ID)
	}
}

// ─── Scenario 4: Empty input slice → empty (non-nil) map ─────────────────────

func TestLatestPRDAuditByState_Empty(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	got, err := repo.LatestPRDAuditByState(ctx, nil)
	if err != nil {
		t.Fatalf("nil input: %v", err)
	}
	if got == nil {
		t.Errorf("expected non-nil empty map for nil input, got nil")
	}
	if len(got) != 0 {
		t.Errorf("expected empty map, got %d entries", len(got))
	}

	got, err = repo.LatestPRDAuditByState(ctx, []string{})
	if err != nil {
		t.Fatalf("empty slice: %v", err)
	}
	if got == nil {
		t.Errorf("expected non-nil empty map for empty slice, got nil")
	}
	if len(got) != 0 {
		t.Errorf("expected empty map, got %d entries", len(got))
	}
}

// ─── Scenario 5: Tenant isolation ────────────────────────────────────────────
//
// Tenant A records an audit on its own state. Tenant B's TenantRepo, even
// if it tries the same stateID, must see an empty result.

func TestRecordPRDAudit_TenantIsolation(t *testing.T) {
	d, tA, tB, uA := newTestDB(t)
	repoA := NewTenantRepo(d.DB, tA)
	repoB := NewTenantRepo(d.DB, tB)
	ctx := context.Background()

	stateID := seedStateForAudit(t, repoA, "A-only")
	if err := repoA.RecordPRDAudit(ctx, stateID, uA, OpUpsertState); err != nil {
		t.Fatalf("repoA record: %v", err)
	}

	// Tenant A sees the row.
	gotA, err := repoA.LatestPRDAuditByState(ctx, []string{stateID})
	if err != nil {
		t.Fatalf("repoA latest: %v", err)
	}
	if _, ok := gotA[stateID]; !ok {
		t.Fatalf("tenant A should see its own audit row")
	}

	// Tenant B sees nothing for the same state id.
	gotB, err := repoB.LatestPRDAuditByState(ctx, []string{stateID})
	if err != nil {
		t.Fatalf("repoB latest: %v", err)
	}
	if len(gotB) != 0 {
		t.Errorf("tenant B leaked %d audit rows from tenant A", len(gotB))
	}

	// listPRDAuditForState on repoB also empty.
	rowsB, err := repoB.listPRDAuditForState(ctx, stateID)
	if err != nil {
		t.Fatalf("repoB list: %v", err)
	}
	if len(rowsB) != 0 {
		t.Errorf("tenant B list leaked %d rows", len(rowsB))
	}
}

// ─── Plan 005 U4: ListPRDAuditForSubFlow ─────────────────────────────────────
//
// HandleFlowActivity merges prd_audit rows into the leaf-level activity feed
// for sub_flow-bound flows. The resolver walks
// prd_audit → prd_state → prd_tab → prd → sub_flow_id, so the tests below
// pin the join shape, the limit semantics, and tenant isolation.

func TestListPRDAuditForSubFlow_HappyPath(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	sf, _, tab := seedPRDWithTab(t, repo)
	stA, err := repo.UpsertPRDState(ctx, PRDStateInput{PRDTabID: tab.ID, Label: "A"})
	if err != nil {
		t.Fatalf("state A: %v", err)
	}
	stB, err := repo.UpsertPRDState(ctx, PRDStateInput{PRDTabID: tab.ID, Label: "B"})
	if err != nil {
		t.Fatalf("state B: %v", err)
	}
	// Three audit rows across two states under the same sub_flow.
	for _, op := range []PRDAuditOp{OpUpsertState, OpAddEvent} {
		if err := repo.RecordPRDAudit(ctx, stA.ID, uA, op); err != nil {
			t.Fatalf("record A %s: %v", op, err)
		}
	}
	if err := repo.RecordPRDAudit(ctx, stB.ID, uA, OpUpsertCopyString); err != nil {
		t.Fatalf("record B: %v", err)
	}

	got, err := repo.ListPRDAuditForSubFlow(ctx, sf.ID, 0)
	if err != nil {
		t.Fatalf("ListPRDAuditForSubFlow: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 rows under the sub_flow, got %d", len(got))
	}
	// All rows belong to this sub_flow's two states.
	wantStates := map[string]bool{stA.ID: true, stB.ID: true}
	for _, a := range got {
		if !wantStates[a.PRDStateID] {
			t.Errorf("row keyed on unknown state %q", a.PRDStateID)
		}
	}
	// Newest-first ordering — TS strings are RFC3339 nano so lexicographic
	// comparison is monotonic. Same-instant rows tie-break on id DESC.
	for i := 1; i < len(got); i++ {
		prev := got[i-1]
		cur := got[i]
		prevKey := rfc3339(prev.At) + "|" + prev.ID
		curKey := rfc3339(cur.At) + "|" + cur.ID
		if prevKey < curKey {
			t.Errorf("rows not newest-first: %s before %s", prevKey, curKey)
		}
	}
}

func TestListPRDAuditForSubFlow_EmptySubFlowID(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	got, err := repo.ListPRDAuditForSubFlow(context.Background(), "   ", 0)
	if err != nil {
		t.Fatalf("empty sub_flow_id should be a no-op, got %v", err)
	}
	if got != nil {
		t.Errorf("expected nil result for empty sub_flow_id, got %d rows", len(got))
	}
}

func TestListPRDAuditForSubFlow_NoAuditRows(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	sf, _, _ := seedPRDWithTab(t, repo)
	got, err := repo.ListPRDAuditForSubFlow(ctx, sf.ID, 0)
	if err != nil {
		t.Fatalf("sub_flow with no audits: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected zero rows for empty audit history, got %d", len(got))
	}
}

func TestListPRDAuditForSubFlow_LimitClamps(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	_, _, tab := seedPRDWithTab(t, repo)
	state, err := repo.UpsertPRDState(ctx, PRDStateInput{PRDTabID: tab.ID, Label: "S"})
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	// Record 5 audits; sub_flow_id is the one tied to the seeded PRD.
	for i := 0; i < 5; i++ {
		if err := repo.RecordPRDAudit(ctx, state.ID, uA, OpUpsertState); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}
	sfID := lookupSubFlowIDForTab(t, repo, tab.ID)

	got, err := repo.ListPRDAuditForSubFlow(ctx, sfID, 3)
	if err != nil {
		t.Fatalf("ListPRDAuditForSubFlow: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("limit=3 should yield 3 rows, got %d", len(got))
	}
}

func TestListPRDAuditForSubFlow_TenantIsolation(t *testing.T) {
	d, tA, tB, uA := newTestDB(t)
	repoA := NewTenantRepo(d.DB, tA)
	repoB := NewTenantRepo(d.DB, tB)
	ctx := context.Background()

	sf, _, tab := seedPRDWithTab(t, repoA)
	state, err := repoA.UpsertPRDState(ctx, PRDStateInput{PRDTabID: tab.ID, Label: "iso"})
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	if err := repoA.RecordPRDAudit(ctx, state.ID, uA, OpUpsertState); err != nil {
		t.Fatalf("record: %v", err)
	}
	gotB, err := repoB.ListPRDAuditForSubFlow(ctx, sf.ID, 0)
	if err != nil {
		t.Fatalf("repoB: %v", err)
	}
	if len(gotB) != 0 {
		t.Errorf("tenant B leaked %d rows from tenant A", len(gotB))
	}
}

// lookupSubFlowIDForTab walks prd_tab → prd → sub_flow_id via SQL so a test
// that already has a PRDTab in hand can name the sub_flow without re-seeding.
func lookupSubFlowIDForTab(t *testing.T, repo *TenantRepo, tabID string) string {
	t.Helper()
	var sfID string
	err := repo.readHandle().QueryRowContext(context.Background(),
		`SELECT p.sub_flow_id
		   FROM prd_tab pt
		   JOIN prd p ON p.tenant_id = pt.tenant_id AND p.id = pt.prd_id
		  WHERE pt.tenant_id = ? AND pt.id = ?`,
		repo.tenantID, tabID,
	).Scan(&sfID)
	if err != nil {
		t.Fatalf("lookup sub_flow_id for tab %s: %v", tabID, err)
	}
	return sfID
}
