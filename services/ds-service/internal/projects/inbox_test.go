package projects

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Phase 4 U4 — inbox endpoint integration tests.

// seedFlowAndScreensWithPath is the parameter-bearing variant of
// seedFlowAndScreens. Tests that need multiple distinct projects in the
// same DB call this with unique paths so UpsertProject doesn't collapse
// them into a single row.
func seedFlowAndScreensWithPath(t *testing.T, repo *TenantRepo, userID, product, path string) (string, []string) {
	t.Helper()
	ctx := context.Background()
	p, err := repo.UpsertProject(ctx, Project{
		Name: "P", Platform: "mobile", Product: product, Path: path, OwnerUserID: userID,
	})
	if err != nil {
		t.Fatalf("upsert project: %v", err)
	}
	v, err := repo.CreateVersion(ctx, p.ID, userID)
	if err != nil {
		t.Fatalf("create version: %v", err)
	}
	f, err := repo.UpsertFlow(ctx, Flow{ProjectID: p.ID, FileID: "F-" + path, Name: "Flow"})
	if err != nil {
		t.Fatalf("upsert flow: %v", err)
	}
	screens := []Screen{
		{VersionID: v.ID, FlowID: f.ID, X: 0, Y: 0, Width: 375, Height: 812},
		{VersionID: v.ID, FlowID: f.ID, X: 0, Y: 1000, Width: 375, Height: 812},
	}
	if err := repo.InsertScreens(ctx, screens); err != nil {
		t.Fatalf("insert screens: %v", err)
	}
	return v.ID, []string{screens[0].ID, screens[1].ID}
}

// seedProjectAndViolations creates a tenant-scoped project + flow + screens
// + N active violations and returns the project id and the violation ids.
//
// Each call uses a fresh project path (random suffix) so multiple invocations
// in the same test don't collapse into the same UpsertProject identity.
func seedProjectAndViolations(t *testing.T, repo *TenantRepo, userID string, n int, severity string) (string, []string) {
	t.Helper()
	versionID, screens := seedFlowAndScreensWithPath(t, repo, userID, "Plutus", "X-"+uuid.NewString()[:8])
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		id := uuid.NewString()
		_, err := repo.DB().ExecContext(context.Background(),
			`INSERT INTO violations (id, version_id, screen_id, tenant_id, rule_id, severity, category, property, status, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'active', ?)`,
			id, versionID, screens[i%len(screens)], repo.tenantID,
			"theme_parity.fill", severity, "theme_parity", "fill",
			time.Now().UTC().Format(time.RFC3339),
		)
		if err != nil {
			t.Fatalf("seed violation: %v", err)
		}
		ids = append(ids, id)
	}

	// Resolve project id via the version we just created.
	var projectID string
	if err := repo.DB().QueryRow(
		`SELECT project_id FROM project_versions WHERE id = ?`, versionID,
	).Scan(&projectID); err != nil {
		t.Fatalf("project_id lookup: %v", err)
	}
	return projectID, ids
}

func TestRepo_GetInbox_EditorSeesAllActive(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	_, ids := seedProjectAndViolations(t, repo, uA, 5, "high")

	rows, total, err := repo.GetInbox(context.Background(), uA, true, InboxFilters{})
	if err != nil {
		t.Fatalf("inbox: %v", err)
	}
	if total != 5 {
		t.Errorf("expected total=5, got %d", total)
	}
	if len(rows) != 5 {
		t.Errorf("expected 5 rows, got %d", len(rows))
	}
	if len(rows) > 0 && rows[0].ViolationID != ids[len(ids)-1] {
		// Most recent first (created_at DESC after severity tier — all
		// share same severity here so created_at ordering applies).
		// We don't assert exact order beyond "in the seeded set".
		seen := map[string]bool{}
		for _, r := range rows {
			seen[r.ViolationID] = true
		}
		for _, id := range ids {
			if !seen[id] {
				t.Errorf("inbox missing violation %s", id)
			}
		}
	}
}

func TestRepo_GetInbox_NonEditorOnlySeesOwnProjects(t *testing.T) {
	d, tA, _, uA := newTestDB(t)

	// User uA owns the seeded project. A second user uB exists but owns no projects.
	uB := uuid.NewString()
	if _, err := d.DB.Exec(
		`INSERT INTO users (id, email, password_hash, role, created_at) VALUES (?, ?, 'x', 'user', ?)`,
		uB, "non-editor-"+uB[:8]+"@example.com", time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		t.Fatalf("seed userB: %v", err)
	}

	repo := NewTenantRepo(d.DB, tA)
	_, _ = seedProjectAndViolations(t, repo, uA, 3, "medium")

	// uB requesting inbox with isEditor=false should see 0 rows.
	rows, total, err := repo.GetInbox(context.Background(), uB, false, InboxFilters{})
	if err != nil {
		t.Fatalf("inbox: %v", err)
	}
	if total != 0 || len(rows) != 0 {
		t.Errorf("uB should see 0 rows, got total=%d rows=%d", total, len(rows))
	}

	// uA requesting inbox with isEditor=false should see all 3.
	rows, total, err = repo.GetInbox(context.Background(), uA, false, InboxFilters{})
	if err != nil {
		t.Fatalf("inbox: %v", err)
	}
	if total != 3 {
		t.Errorf("uA should see 3 rows, got total=%d", total)
	}
}

func TestRepo_GetInbox_TenantIsolation(t *testing.T) {
	d, tA, tB, uA := newTestDB(t)

	repoA := NewTenantRepo(d.DB, tA)
	_, _ = seedProjectAndViolations(t, repoA, uA, 4, "high")

	// Tenant B sees zero of tenant A's violations even with editor scope.
	repoB := NewTenantRepo(d.DB, tB)
	rows, total, err := repoB.GetInbox(context.Background(), uA, true, InboxFilters{})
	if err != nil {
		t.Fatalf("inbox: %v", err)
	}
	if total != 0 || len(rows) != 0 {
		t.Errorf("tenant B leak: total=%d rows=%d", total, len(rows))
	}
}

func TestRepo_GetInbox_FiltersByRuleAndCategory(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	versionID, screens := seedFlowAndScreens(t, repo, uA)

	// Mix of rule_id + category
	mix := []struct {
		rule, category, severity string
	}{
		{"theme_parity.fill", "theme_parity", "high"},
		{"theme_parity.fill", "theme_parity", "high"},
		{"a11y.contrast", "a11y_contrast", "critical"},
		{"a11y.touch_target", "a11y_contrast", "low"},
	}
	for _, m := range mix {
		_, err := d.DB.Exec(
			`INSERT INTO violations (id, version_id, screen_id, tenant_id, rule_id, severity, category, property, status, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'active', ?)`,
			uuid.NewString(), versionID, screens[0], tA,
			m.rule, m.severity, m.category, "fill",
			time.Now().UTC().Format(time.RFC3339),
		)
		if err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	rows, total, err := repo.GetInbox(context.Background(), uA, true, InboxFilters{
		RuleID: "theme_parity.fill",
	})
	if err != nil {
		t.Fatalf("inbox: %v", err)
	}
	if total != 2 {
		t.Errorf("rule_id filter expected 2, got %d", total)
	}
	for _, r := range rows {
		if r.RuleID != "theme_parity.fill" {
			t.Errorf("unexpected rule in result: %s", r.RuleID)
		}
	}

	rows, total, err = repo.GetInbox(context.Background(), uA, true, InboxFilters{
		Category: "a11y_contrast",
	})
	if err != nil {
		t.Fatalf("inbox: %v", err)
	}
	if total != 2 {
		t.Errorf("category filter expected 2, got %d", total)
	}

	// Severity OR filter
	rows, total, err = repo.GetInbox(context.Background(), uA, true, InboxFilters{
		Severities: []string{"critical", "low"},
	})
	if err != nil {
		t.Fatalf("inbox: %v", err)
	}
	if total != 2 {
		t.Errorf("severity OR filter expected 2, got %d", total)
	}
	_ = rows
}

func TestRepo_GetInbox_ExcludesNonActiveStatus(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	versionID, screens := seedFlowAndScreens(t, repo, uA)

	for _, status := range []string{"active", "acknowledged", "dismissed", "fixed"} {
		_, err := d.DB.Exec(
			`INSERT INTO violations (id, version_id, screen_id, tenant_id, rule_id, severity, category, property, status, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			uuid.NewString(), versionID, screens[0], tA,
			"r", "high", "theme_parity", "fill", status,
			time.Now().UTC().Format(time.RFC3339),
		)
		if err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	_, total, err := repo.GetInbox(context.Background(), uA, true, InboxFilters{})
	if err != nil {
		t.Fatalf("inbox: %v", err)
	}
	if total != 1 {
		t.Errorf("expected only active = 1, got %d", total)
	}
}

func TestRepo_GetInbox_PaginationLimitOffset(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	_, _ = seedProjectAndViolations(t, repo, uA, 25, "high")

	rows, total, err := repo.GetInbox(context.Background(), uA, true, InboxFilters{Limit: 10, Offset: 0})
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	if total != 25 {
		t.Errorf("total expected 25, got %d", total)
	}
	if len(rows) != 10 {
		t.Errorf("page 1 expected 10, got %d", len(rows))
	}

	rows, _, err = repo.GetInbox(context.Background(), uA, true, InboxFilters{Limit: 10, Offset: 20})
	if err != nil {
		t.Fatalf("page 3: %v", err)
	}
	if len(rows) != 5 {
		t.Errorf("page 3 expected 5, got %d", len(rows))
	}

	// Limit cap.
	_, _, err = repo.GetInbox(context.Background(), uA, true, InboxFilters{Limit: 99999})
	if err != nil {
		t.Fatalf("oversize limit: %v", err)
	}
	// Should silently clamp to default; no assertion on exact cap behavior
	// beyond not erroring.
}

func TestRepo_GetInbox_OnlyLatestVersion(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	versionID1, screens := seedFlowAndScreens(t, repo, uA)

	// Insert a violation against version 1.
	id1 := uuid.NewString()
	if _, err := d.DB.Exec(
		`INSERT INTO violations (id, version_id, screen_id, tenant_id, rule_id, severity, category, property, status, created_at)
		 VALUES (?, ?, ?, ?, 'r', 'high', 'theme_parity', 'fill', 'active', ?)`,
		id1, versionID1, screens[0], tA, time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		t.Fatalf("v1 violation: %v", err)
	}

	// Create version 2 (newer) with its own active violation.
	var projectID string
	if err := d.DB.QueryRow(`SELECT project_id FROM project_versions WHERE id = ?`, versionID1).Scan(&projectID); err != nil {
		t.Fatalf("project lookup: %v", err)
	}
	v2, err := repo.CreateVersion(context.Background(), projectID, uA)
	if err != nil {
		t.Fatalf("v2: %v", err)
	}

	// Reuse a screen — but screens belong to v1. Insert a fresh screen for v2.
	var flowID string
	if err := d.DB.QueryRow(`SELECT flow_id FROM screens WHERE id = ?`, screens[0]).Scan(&flowID); err != nil {
		t.Fatalf("flow_id lookup: %v", err)
	}
	scr := []Screen{{VersionID: v2.ID, FlowID: flowID, X: 0, Y: 0, Width: 375, Height: 812}}
	if err := repo.InsertScreens(context.Background(), scr); err != nil {
		t.Fatalf("insert v2 screen: %v", err)
	}
	id2 := uuid.NewString()
	if _, err := d.DB.Exec(
		`INSERT INTO violations (id, version_id, screen_id, tenant_id, rule_id, severity, category, property, status, created_at)
		 VALUES (?, ?, ?, ?, 'r', 'high', 'theme_parity', 'fill', 'active', ?)`,
		id2, v2.ID, scr[0].ID, tA, time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		t.Fatalf("v2 violation: %v", err)
	}

	rows, total, err := repo.GetInbox(context.Background(), uA, true, InboxFilters{})
	if err != nil {
		t.Fatalf("inbox: %v", err)
	}
	if total != 1 {
		t.Errorf("expected only v2 violation, got total=%d", total)
	}
	if len(rows) != 1 || rows[0].ViolationID != id2 {
		t.Errorf("expected id2=%s in result, got %+v", id2, rows)
	}
}

func TestRepo_GetInbox_ProjectFilter(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)

	// Create two projects with violations.
	pA, _ := seedProjectAndViolations(t, repo, uA, 3, "high")
	pB, _ := seedProjectAndViolations(t, repo, uA, 2, "medium")

	// Filter to project A.
	_, total, err := repo.GetInbox(context.Background(), uA, true, InboxFilters{ProjectID: pA})
	if err != nil {
		t.Fatalf("inbox: %v", err)
	}
	if total != 3 {
		t.Errorf("expected 3 for project A, got %d", total)
	}

	_, total, err = repo.GetInbox(context.Background(), uA, true, InboxFilters{ProjectID: pB})
	if err != nil {
		t.Fatalf("inbox: %v", err)
	}
	if total != 2 {
		t.Errorf("expected 2 for project B, got %d", total)
	}
}
