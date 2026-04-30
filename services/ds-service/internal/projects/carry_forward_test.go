package projects

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Phase 4 U3 — carry-forward integration tests.
//
// All four scenarios from the plan:
//   1. Re-audit with previously-Dismissed violation → new violation lands
//      with status='dismissed', reason matches the marker.
//   2. DS-lead override (dismissed → active) deletes the marker.
//   3. After override, subsequent re-audit → new violation is Active again
//      (override sticks).
//   4. Multi-tenant: tenant A's carry-forward never bleeds into tenant B.

// fixtureScreenLogicalID grabs the screen.screen_logical_id for a screen_id.
// Used by tests to build carry-forward markers without re-deriving the
// stable identity from scratch.
func fixtureScreenLogicalID(t *testing.T, repo *TenantRepo, screenID string) string {
	t.Helper()
	var logical string
	err := repo.DB().QueryRow(
		`SELECT screen_logical_id FROM screens WHERE id = ?`, screenID,
	).Scan(&logical)
	if err != nil {
		t.Fatalf("logical_id lookup: %v", err)
	}
	return logical
}

// seedDismissedMarker inserts a dismissed_carry_forwards row directly so
// the carry-forward apply step has something to match against.
func seedDismissedMarker(t *testing.T, repo *TenantRepo, tenantID, screenID, ruleID, property, reason string) {
	t.Helper()
	logical := fixtureScreenLogicalID(t, repo, screenID)
	tx, err := repo.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()
	if err := WriteCarryForwardMarker(context.Background(), tx, CarryForwardMarker{
		TenantID:            tenantID,
		ScreenLogicalID:     logical,
		RuleID:              ruleID,
		Property:            property,
		Reason:              reason,
		DismissedByUserID:   "user-x",
		DismissedAt:         time.Now().UTC(),
		OriginalViolationID: uuid.NewString(),
	}); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

func TestApplyCarryForwardInTx_FlipsMatchingViolation(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	versionID, screens := seedFlowAndScreens(t, repo, uA)

	// Mark (screens[0], theme_parity.fill, fill) as previously dismissed.
	seedDismissedMarker(t, repo, tA, screens[0], "theme_parity.fill", "fill", "deferred to v2")

	// Build two candidate violations: one matches, one does not.
	violations := []Violation{
		{
			ID:        uuid.NewString(),
			VersionID: versionID,
			ScreenID:  screens[0],
			TenantID:  tA,
			RuleID:    "theme_parity.fill",
			Property:  "fill",
			Severity:  "high",
			Status:    "active",
		},
		{
			ID:        uuid.NewString(),
			VersionID: versionID,
			ScreenID:  screens[1],
			TenantID:  tA,
			RuleID:    "theme_parity.fill",
			Property:  "fill",
			Severity:  "high",
			Status:    "active",
		},
	}

	tx, err := d.DB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	flipped, err := ApplyCarryForwardInTx(context.Background(), tx, tA, violations)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if flipped != 1 {
		t.Errorf("expected 1 flipped, got %d", flipped)
	}
	if violations[0].Status != "dismissed" {
		t.Errorf("expected violations[0]=dismissed, got %q", violations[0].Status)
	}
	if violations[1].Status != "active" {
		t.Errorf("expected violations[1]=active, got %q", violations[1].Status)
	}
}

func TestApplyCarryForwardInTx_NoMarkers_NoOp(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	versionID, screens := seedFlowAndScreens(t, repo, uA)

	violations := []Violation{
		{
			ID:        uuid.NewString(),
			VersionID: versionID,
			ScreenID:  screens[0],
			TenantID:  tA,
			RuleID:    "theme_parity.fill",
			Property:  "fill",
			Severity:  "high",
			Status:    "active",
		},
	}
	tx, err := d.DB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	flipped, err := ApplyCarryForwardInTx(context.Background(), tx, tA, violations)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if flipped != 0 {
		t.Errorf("expected 0 flipped, got %d", flipped)
	}
	if violations[0].Status != "active" {
		t.Errorf("expected active (no marker), got %q", violations[0].Status)
	}
}

func TestApplyCarryForwardInTx_TenantIsolation(t *testing.T) {
	d, tA, tB, uA := newTestDB(t)
	repoA := NewTenantRepo(d.DB, tA)
	versionID, screens := seedFlowAndScreens(t, repoA, uA)

	// Tenant A dismissed (screens[0], theme_parity.fill, fill).
	seedDismissedMarker(t, repoA, tA, screens[0], "theme_parity.fill", "fill", "tenant A reason")

	// Build a candidate violation FROM TENANT B's perspective targeting the
	// same screen_logical_id (e.g., a logical_id collision under cross-tenant
	// shared screens, hypothetically). Apply with tB as the scope.
	logical := fixtureScreenLogicalID(t, repoA, screens[0])
	if logical == "" {
		t.Fatalf("missing logical_id")
	}

	// Insert a screen owned by tenant B with the same logical_id (forced
	// collision — not realistic but surfaces the safeguard).
	repoB := NewTenantRepo(d.DB, tB)
	versionB, screensB := seedFlowAndScreens(t, repoB, uA)
	if _, err := d.DB.Exec(
		`UPDATE screens SET screen_logical_id = ? WHERE id = ?`,
		logical, screensB[0],
	); err != nil {
		t.Fatalf("collide logical: %v", err)
	}

	violations := []Violation{
		{
			ID:        uuid.NewString(),
			VersionID: versionB,
			ScreenID:  screensB[0],
			TenantID:  tB,
			RuleID:    "theme_parity.fill",
			Property:  "fill",
			Severity:  "high",
			Status:    "active",
		},
	}
	tx, err := d.DB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	flipped, err := ApplyCarryForwardInTx(context.Background(), tx, tB, violations)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if flipped != 0 {
		t.Errorf("tenant B should not see tenant A markers; flipped=%d", flipped)
	}
	if violations[0].Status != "active" {
		t.Errorf("expected active, got %q", violations[0].Status)
	}
	_ = versionID // silence unused
}

func TestDeleteCarryForwardMarker_RemovesRow(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	_, screens := seedFlowAndScreens(t, repo, uA)
	seedDismissedMarker(t, repo, tA, screens[0], "rule.x", "fill", "reason")

	logical := fixtureScreenLogicalID(t, repo, screens[0])

	tx, err := d.DB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := DeleteCarryForwardMarker(context.Background(), tx, tA, logical, "rule.x", "fill"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	var count int
	if err := d.DB.QueryRow(
		`SELECT COUNT(*) FROM dismissed_carry_forwards WHERE tenant_id = ? AND screen_logical_id = ?`,
		tA, logical,
	).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 markers after delete, got %d", count)
	}
}

func TestDeleteCarryForwardMarker_NoRow_NoError(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	tx, err := d.DB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()
	if err := DeleteCarryForwardMarker(context.Background(), tx, tA, "nonexistent", "rule", "prop"); err != nil {
		t.Errorf("expected nil error for missing marker, got %v", err)
	}
}

// End-to-end: simulate two re-audit runs through the worker's
// PersistRunIdempotent. First run produces an Active violation; we dismiss
// it via WriteCarryForwardMarker; second run should land it as Dismissed.
// Then DS-lead override deletes the marker; third run lands it as Active
// again.
func TestCarryForward_ReauditScenario_DismissReactivateRoundTrip(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	versionID, screens := seedFlowAndScreens(t, repo, uA)
	logical := fixtureScreenLogicalID(t, repo, screens[0])

	// Run 1 — fresh audit lands a single Active violation. We persist it via
	// the workerRepoAdapter so the carry-forward hook has a chance to fire
	// (it won't this time because no marker exists yet).
	wr := &workerRepoAdapter{db: d.DB, now: time.Now}
	jobID := uuid.NewString()
	if _, err := d.DB.Exec(
		`INSERT INTO audit_jobs (id, version_id, tenant_id, status, trace_id, idempotency_key, created_at)
		 VALUES (?, ?, ?, 'running', 'trace-1', 'idemp-1', ?)`,
		jobID, versionID, tA, time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		t.Fatalf("seed job: %v", err)
	}

	v1 := Violation{
		ID: uuid.NewString(), VersionID: versionID, ScreenID: screens[0], TenantID: tA,
		RuleID: "theme_parity.fill", Property: "fill", Severity: "high", Status: "active",
	}
	if err := wr.PersistRunIdempotent(context.Background(), jobID, versionID, []Violation{v1}); err != nil {
		t.Fatalf("run 1: %v", err)
	}
	var status1 string
	if err := d.DB.QueryRow(`SELECT status FROM violations WHERE version_id = ?`, versionID).Scan(&status1); err != nil {
		t.Fatalf("readback 1: %v", err)
	}
	if status1 != "active" {
		t.Errorf("run 1 expected active, got %q", status1)
	}

	// Designer dismisses it — write the marker directly (lifecycle endpoint
	// does this in production, U1 wires the path).
	tx, err := d.DB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := WriteCarryForwardMarker(context.Background(), tx, CarryForwardMarker{
		TenantID: tA, ScreenLogicalID: logical, RuleID: "theme_parity.fill", Property: "fill",
		Reason: "logged-out persona", DismissedByUserID: uA, DismissedAt: time.Now().UTC(),
		OriginalViolationID: v1.ID,
	}); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// Run 2 — re-audit emits the same logical violation. Carry-forward should
	// flip its Status to "dismissed".
	job2 := uuid.NewString()
	if _, err := d.DB.Exec(
		`INSERT INTO audit_jobs (id, version_id, tenant_id, status, trace_id, idempotency_key, created_at)
		 VALUES (?, ?, ?, 'running', 'trace-2', 'idemp-2', ?)`,
		job2, versionID, tA, time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		t.Fatalf("seed job 2: %v", err)
	}
	v2 := Violation{
		ID: uuid.NewString(), VersionID: versionID, ScreenID: screens[0], TenantID: tA,
		RuleID: "theme_parity.fill", Property: "fill", Severity: "high", Status: "active",
	}
	if err := wr.PersistRunIdempotent(context.Background(), job2, versionID, []Violation{v2}); err != nil {
		t.Fatalf("run 2: %v", err)
	}
	var status2 string
	if err := d.DB.QueryRow(`SELECT status FROM violations WHERE version_id = ?`, versionID).Scan(&status2); err != nil {
		t.Fatalf("readback 2: %v", err)
	}
	if status2 != "dismissed" {
		t.Errorf("run 2 expected dismissed via carry-forward, got %q", status2)
	}

	// DS-lead override: delete the marker.
	tx2, err := d.DB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin override: %v", err)
	}
	if err := DeleteCarryForwardMarker(context.Background(), tx2, tA, logical, "theme_parity.fill", "fill"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := tx2.Commit(); err != nil {
		t.Fatalf("commit override: %v", err)
	}

	// Run 3 — same violation should now land Active because the marker is gone.
	job3 := uuid.NewString()
	if _, err := d.DB.Exec(
		`INSERT INTO audit_jobs (id, version_id, tenant_id, status, trace_id, idempotency_key, created_at)
		 VALUES (?, ?, ?, 'running', 'trace-3', 'idemp-3', ?)`,
		job3, versionID, tA, time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		t.Fatalf("seed job 3: %v", err)
	}
	v3 := Violation{
		ID: uuid.NewString(), VersionID: versionID, ScreenID: screens[0], TenantID: tA,
		RuleID: "theme_parity.fill", Property: "fill", Severity: "high", Status: "active",
	}
	if err := wr.PersistRunIdempotent(context.Background(), job3, versionID, []Violation{v3}); err != nil {
		t.Fatalf("run 3: %v", err)
	}
	var status3 string
	if err := d.DB.QueryRow(`SELECT status FROM violations WHERE version_id = ?`, versionID).Scan(&status3); err != nil {
		t.Fatalf("readback 3: %v", err)
	}
	if status3 != "active" {
		t.Errorf("run 3 expected active after override, got %q", status3)
	}
}

func TestResolveCarryForwardKey_TenantScoped(t *testing.T) {
	d, tA, tB, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	versionID, screens := seedFlowAndScreens(t, repo, uA)
	vID := seedViolation(t, repo, versionID, screens[0], tA)

	logical, ruleID, property, err := ResolveCarryForwardKey(context.Background(), d.DB, tA, vID)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if logical == "" || ruleID == "" || property == "" {
		t.Errorf("missing fields: logical=%q rule=%q prop=%q", logical, ruleID, property)
	}

	// Cross-tenant returns ErrNotFound.
	if _, _, _, err := ResolveCarryForwardKey(context.Background(), d.DB, tB, vID); err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}
