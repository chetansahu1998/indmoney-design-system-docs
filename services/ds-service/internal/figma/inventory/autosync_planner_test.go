package inventory

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/db"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/projects"
)

// autosync_planner_test.go — U7 unit + integration coverage.
// Stands up a real *db.DB so migrations apply (including 0028), then
// drives the planner with seeded inventory + state rows.

func newPlannerTestDB(t *testing.T) (*db.DB, string, string) {
	t.Helper()
	dir := t.TempDir()
	d, err := db.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("db open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	ctx := context.Background()
	userID := uuid.NewString()
	if err := d.CreateUser(ctx, db.User{ID: userID, Email: "p@example.com", PasswordHash: "x", Role: "user", CreatedAt: time.Now()}); err != nil {
		t.Fatalf("user: %v", err)
	}
	tenantID := uuid.NewString()
	if err := d.CreateTenant(ctx, db.Tenant{ID: tenantID, Slug: "t1", Name: "T", Status: "active", PlanType: "free", CreatedAt: time.Now(), CreatedBy: userID}); err != nil {
		t.Fatalf("tenant: %v", err)
	}
	return d, tenantID, userID
}

type plannerDBAdapter struct{ d *db.DB }

func (a plannerDBAdapter) NewTenantRepo(tenantID string) *projects.TenantRepo {
	return projects.NewTenantRepo(a.d.DB, tenantID)
}

func newPlanner(d *db.DB, now time.Time) *Planner {
	return NewPlanner(plannerDBAdapter{d: d}, PlannerConfig{
		Now: func() time.Time { return now },
	})
}

func seedFile(t *testing.T, repo *projects.TenantRepo, file projects.FigmaFileRow,
	mapping projects.FigmaProjectMapping, pages []projects.FigmaPageRow, sections []projects.FigmaSectionRow,
	now time.Time) {
	t.Helper()
	ctx := context.Background()
	if err := repo.UpsertFigmaTeam(ctx, file.TeamID, "Team"); err != nil {
		t.Fatalf("seed team: %v", err)
	}
	if err := repo.UpsertFigmaProjects(ctx, file.TeamID, []projects.FigmaProjectRow{
		{ProjectID: file.ProjectID, TeamID: file.TeamID, Name: "Test Project"},
	}, now); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if mapping.ProjectID != "" {
		if err := repo.UpsertFigmaProjectMapping(ctx, mapping); err != nil {
			t.Fatalf("seed mapping: %v", err)
		}
	}
	if err := repo.UpsertFigmaFilesShell(ctx, file.ProjectID, file.TeamID, []projects.FigmaFileRow{file}, now); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	if len(pages) > 0 || len(sections) > 0 {
		if _, _, err := repo.UpsertFigmaPagesAndSections(ctx, file.FileKey, pages, sections, now); err != nil {
			t.Fatalf("seed pages+sections: %v", err)
		}
	}
}

func TestPlanner_FileOutOfWindowSkips(t *testing.T) {
	d, tenantID, userID := newPlannerTestDB(t)
	repo := projects.NewTenantRepo(d.DB, tenantID)
	now := time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC)
	seedFile(t, repo,
		projects.FigmaFileRow{FileKey: "fk-old", Name: "Old", TeamID: "t", ProjectID: "p1", LastModified: now.AddDate(0, -7, 0)},
		projects.FigmaProjectMapping{ProjectID: "p1", Domain: "Markets", Product: "X", EnabledForAutosync: true, MappedByUserID: userID},
		nil, nil, now,
	)
	plan, err := newPlanner(d, now).Plan(context.Background(), tenantID, "fk-old")
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if plan.FileSkip == nil || plan.FileSkip.Code != SkipOutOfWindow {
		t.Fatalf("expected out_of_window, got %+v", plan.FileSkip)
	}
}

func TestPlanner_UnmappedProjectQuarantines(t *testing.T) {
	d, tenantID, _ := newPlannerTestDB(t)
	repo := projects.NewTenantRepo(d.DB, tenantID)
	now := time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC)
	seedFile(t, repo,
		projects.FigmaFileRow{FileKey: "fk-1", Name: "F", TeamID: "t", ProjectID: "p-noMap", LastModified: now.AddDate(0, -1, 0)},
		projects.FigmaProjectMapping{}, nil, nil, now,
	)
	plan, _ := newPlanner(d, now).Plan(context.Background(), tenantID, "fk-1")
	if plan.FileSkip == nil || plan.FileSkip.Code != SkipProjectUnmapped {
		t.Fatalf("expected project_unmapped, got %+v", plan.FileSkip)
	}
}

func TestPlanner_NoSourcePageSkips(t *testing.T) {
	d, tenantID, userID := newPlannerTestDB(t)
	repo := projects.NewTenantRepo(d.DB, tenantID)
	now := time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC)
	seedFile(t, repo,
		projects.FigmaFileRow{FileKey: "fk-1", Name: "F", TeamID: "t", ProjectID: "p1", LastModified: now.AddDate(0, -1, 0)},
		projects.FigmaProjectMapping{ProjectID: "p1", Domain: "M", Product: "X", EnabledForAutosync: true, MappedByUserID: userID},
		[]projects.FigmaPageRow{
			{FileKey: "fk-1", PageID: "0:1", Name: "Cover", OrderIndex: 0, ContentHash: "h1", Classification: projects.PageClassNoise},
			{FileKey: "fk-1", PageID: "0:2", Name: "Twitter", OrderIndex: 1, ContentHash: "h2", Classification: projects.PageClassUnknown},
		}, nil, now,
	)
	plan, _ := newPlanner(d, now).Plan(context.Background(), tenantID, "fk-1")
	if plan.FileSkip == nil || plan.FileSkip.Code != SkipNoSourcePage {
		t.Fatalf("expected no_source_page, got %+v", plan.FileSkip)
	}
}

func TestPlanner_FinalPageFullExportNewSection(t *testing.T) {
	d, tenantID, userID := newPlannerTestDB(t)
	repo := projects.NewTenantRepo(d.DB, tenantID)
	now := time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC)
	seedFile(t, repo,
		projects.FigmaFileRow{FileKey: "fk-1", Name: "INDstocks", TeamID: "t", ProjectID: "p1", LastModified: now.AddDate(0, -1, 0)},
		projects.FigmaProjectMapping{ProjectID: "p1", Domain: "Markets", Product: "Indian Stocks", PlatformDefault: "mobile", EnabledForAutosync: true, MappedByUserID: userID},
		[]projects.FigmaPageRow{
			{FileKey: "fk-1", PageID: "0:1", Name: "Final Design", OrderIndex: 0, ContentHash: "ph1", PositionHash: "pp1", Classification: projects.PageClassFinal, PersonaHint: "default"},
		},
		[]projects.FigmaSectionRow{
			{FileKey: "fk-1", PageID: "0:1", SectionID: "3:7", Name: "Wallet/Main Flow", X: 100, Y: 200, Width: 1440, Height: 720, OrderIndex: 0, ContentHash: "sh1", PositionHash: "sp1"},
		},
		now,
	)
	plan, _ := newPlanner(d, now).Plan(context.Background(), tenantID, "fk-1")
	if plan.FileSkip != nil {
		t.Fatalf("unexpected file skip: %+v", plan.FileSkip)
	}
	if len(plan.Sections) != 1 {
		t.Fatalf("expected 1 section, got %d", len(plan.Sections))
	}
	ps := plan.Sections[0]
	if ps.Action != ActionFullExport || ps.Reason != SkipNewSection {
		t.Errorf("action/reason: %s/%s", ps.Action, ps.Reason)
	}
	if ps.SubProduct != "Wallet" || ps.SubFlow != "Main Flow" {
		t.Errorf("section parse: %q/%q", ps.SubProduct, ps.SubFlow)
	}
	if ps.Domain != "Markets" || ps.Product != "Indian Stocks" {
		t.Errorf("mapping: %q/%q", ps.Domain, ps.Product)
	}
}

func TestPlanner_SkipUnchangedWhenHashesMatch(t *testing.T) {
	d, tenantID, userID := newPlannerTestDB(t)
	repo := projects.NewTenantRepo(d.DB, tenantID)
	now := time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC)
	seedFile(t, repo,
		projects.FigmaFileRow{FileKey: "fk-1", Name: "F", TeamID: "t", ProjectID: "p1", LastModified: now.AddDate(0, -1, 0)},
		projects.FigmaProjectMapping{ProjectID: "p1", Domain: "D", Product: "P", EnabledForAutosync: true, MappedByUserID: userID},
		[]projects.FigmaPageRow{{FileKey: "fk-1", PageID: "0:1", Name: "Final", OrderIndex: 0, ContentHash: "ph", Classification: projects.PageClassFinal, PersonaHint: "default"}},
		[]projects.FigmaSectionRow{{FileKey: "fk-1", PageID: "0:1", SectionID: "3:7", Name: "Hero", ContentHash: "sh", PositionHash: "sp"}},
		now,
	)
	if err := repo.UpsertAutoSyncState(context.Background(), projects.AutoSyncState{
		FileKey: "fk-1", PageID: "0:1", SectionID: "3:7", ContentHash: "sh", PositionHash: "sp",
		LastSyncedFlowID: "flow-1", LastSyncedVersionID: "ver-1", LastAttemptStatus: "ok",
	}); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	plan, _ := newPlanner(d, now).Plan(context.Background(), tenantID, "fk-1")
	if len(plan.Sections) != 1 || plan.Sections[0].Action != ActionSkipUnchanged {
		t.Fatalf("expected skip_unchanged, got %+v", plan.Sections)
	}
}

func TestPlanner_CheapUpdateWhenOnlyPositionMoves(t *testing.T) {
	d, tenantID, userID := newPlannerTestDB(t)
	repo := projects.NewTenantRepo(d.DB, tenantID)
	now := time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC)
	seedFile(t, repo,
		projects.FigmaFileRow{FileKey: "fk-1", Name: "F", TeamID: "t", ProjectID: "p1", LastModified: now.AddDate(0, -1, 0)},
		projects.FigmaProjectMapping{ProjectID: "p1", Domain: "D", Product: "P", EnabledForAutosync: true, MappedByUserID: userID},
		[]projects.FigmaPageRow{{FileKey: "fk-1", PageID: "0:1", Name: "Final", OrderIndex: 0, ContentHash: "ph", Classification: projects.PageClassFinal, PersonaHint: "default"}},
		[]projects.FigmaSectionRow{{FileKey: "fk-1", PageID: "0:1", SectionID: "3:7", Name: "Hero", ContentHash: "sh", PositionHash: "sp-NEW"}},
		now,
	)
	if err := repo.UpsertAutoSyncState(context.Background(), projects.AutoSyncState{
		FileKey: "fk-1", PageID: "0:1", SectionID: "3:7", ContentHash: "sh", PositionHash: "sp-OLD",
		LastSyncedFlowID: "flow-1", LastAttemptStatus: "ok",
	}); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	plan, _ := newPlanner(d, now).Plan(context.Background(), tenantID, "fk-1")
	if len(plan.Sections) != 1 || plan.Sections[0].Action != ActionCheapUpdate {
		t.Fatalf("expected cheap_update, got %+v", plan.Sections)
	}
}

func TestPlanner_FullExportWhenContentChanged(t *testing.T) {
	d, tenantID, userID := newPlannerTestDB(t)
	repo := projects.NewTenantRepo(d.DB, tenantID)
	now := time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC)
	seedFile(t, repo,
		projects.FigmaFileRow{FileKey: "fk-1", Name: "F", TeamID: "t", ProjectID: "p1", LastModified: now.AddDate(0, -1, 0)},
		projects.FigmaProjectMapping{ProjectID: "p1", Domain: "D", Product: "P", EnabledForAutosync: true, MappedByUserID: userID},
		[]projects.FigmaPageRow{{FileKey: "fk-1", PageID: "0:1", Name: "Final", OrderIndex: 0, ContentHash: "ph", Classification: projects.PageClassFinal, PersonaHint: "default"}},
		[]projects.FigmaSectionRow{{FileKey: "fk-1", PageID: "0:1", SectionID: "3:7", Name: "Hero", ContentHash: "NEW", PositionHash: "sp"}},
		now,
	)
	if err := repo.UpsertAutoSyncState(context.Background(), projects.AutoSyncState{
		FileKey: "fk-1", PageID: "0:1", SectionID: "3:7", ContentHash: "OLD", PositionHash: "sp",
		LastSyncedFlowID: "flow-1", LastAttemptStatus: "ok",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	plan, _ := newPlanner(d, now).Plan(context.Background(), tenantID, "fk-1")
	if len(plan.Sections) != 1 || plan.Sections[0].Action != ActionFullExport || plan.Sections[0].Reason != SkipContentChanged {
		t.Fatalf("expected full_export/content_changed, got %+v", plan.Sections)
	}
}

func TestPlanner_HashNotReadyShortCircuits(t *testing.T) {
	d, tenantID, userID := newPlannerTestDB(t)
	repo := projects.NewTenantRepo(d.DB, tenantID)
	now := time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC)
	seedFile(t, repo,
		projects.FigmaFileRow{FileKey: "fk-1", Name: "F", TeamID: "t", ProjectID: "p1", LastModified: now.AddDate(0, -1, 0)},
		projects.FigmaProjectMapping{ProjectID: "p1", Domain: "D", Product: "P", EnabledForAutosync: true, MappedByUserID: userID},
		[]projects.FigmaPageRow{{FileKey: "fk-1", PageID: "0:1", Name: "Final Design", OrderIndex: 0}},
		nil, now,
	)
	plan, _ := newPlanner(d, now).Plan(context.Background(), tenantID, "fk-1")
	if plan.FileSkip == nil || plan.FileSkip.Code != SkipHashNotReady {
		t.Fatalf("expected hash_not_ready, got %+v", plan.FileSkip)
	}
}

func TestPlanner_MultiFinalEmitsAllSections(t *testing.T) {
	d, tenantID, userID := newPlannerTestDB(t)
	repo := projects.NewTenantRepo(d.DB, tenantID)
	now := time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC)
	seedFile(t, repo,
		projects.FigmaFileRow{FileKey: "fk-1", Name: "Design Summit", TeamID: "t", ProjectID: "p1", LastModified: now.AddDate(0, -1, 0)},
		projects.FigmaProjectMapping{ProjectID: "p1", Domain: "D", Product: "P", EnabledForAutosync: true, MappedByUserID: userID},
		[]projects.FigmaPageRow{
			{FileKey: "fk-1", PageID: "0:1", Name: "Trader FINAL DESIGN", OrderIndex: 0, ContentHash: "h1", Classification: projects.PageClassFinal, PersonaHint: "trader"},
			{FileKey: "fk-1", PageID: "0:2", Name: "Investor FINAL DESIGN", OrderIndex: 1, ContentHash: "h2", Classification: projects.PageClassFinal, PersonaHint: "investor"},
		},
		[]projects.FigmaSectionRow{
			{FileKey: "fk-1", PageID: "0:1", SectionID: "3:7", Name: "Hero/Trader", ContentHash: "s1", PositionHash: "p1"},
			{FileKey: "fk-1", PageID: "0:2", SectionID: "3:8", Name: "Hero/Investor", ContentHash: "s2", PositionHash: "p2"},
		},
		now,
	)
	plan, _ := newPlanner(d, now).Plan(context.Background(), tenantID, "fk-1")
	if len(plan.Sections) != 2 {
		t.Fatalf("expected 2, got %d", len(plan.Sections))
	}
	pmap := map[string]string{plan.Sections[0].PageID: plan.Sections[0].PersonaHint, plan.Sections[1].PageID: plan.Sections[1].PersonaHint}
	if pmap["0:1"] != "trader" || pmap["0:2"] != "investor" {
		t.Errorf("personas: %+v", pmap)
	}
}

func TestPlanner_VersionPicksMaxN(t *testing.T) {
	d, tenantID, userID := newPlannerTestDB(t)
	repo := projects.NewTenantRepo(d.DB, tenantID)
	now := time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC)
	seedFile(t, repo,
		projects.FigmaFileRow{FileKey: "fk-1", Name: "IPO Center", TeamID: "t", ProjectID: "p1", LastModified: now.AddDate(0, -1, 0)},
		projects.FigmaProjectMapping{ProjectID: "p1", Domain: "D", Product: "P", EnabledForAutosync: true, MappedByUserID: userID},
		[]projects.FigmaPageRow{
			{FileKey: "fk-1", PageID: "0:1", Name: "V1", OrderIndex: 0, ContentHash: "h1", Classification: projects.PageClassVersion, VersionBase: "", VersionN: 1},
			{FileKey: "fk-1", PageID: "0:2", Name: "V2", OrderIndex: 1, ContentHash: "h2", Classification: projects.PageClassVersion, VersionBase: "", VersionN: 2},
			{FileKey: "fk-1", PageID: "0:3", Name: "V3", OrderIndex: 2, ContentHash: "h3", Classification: projects.PageClassVersion, VersionBase: "", VersionN: 3},
		},
		[]projects.FigmaSectionRow{{FileKey: "fk-1", PageID: "0:3", SectionID: "9:1", Name: "Newest Hero", ContentHash: "s3", PositionHash: "p3"}},
		now,
	)
	plan, _ := newPlanner(d, now).Plan(context.Background(), tenantID, "fk-1")
	if len(plan.Sections) != 1 || plan.Sections[0].PageID != "0:3" {
		t.Fatalf("expected only V3 page, got %+v", plan.Sections)
	}
}

func TestPlanner_MappingDisabledQuarantines(t *testing.T) {
	d, tenantID, userID := newPlannerTestDB(t)
	repo := projects.NewTenantRepo(d.DB, tenantID)
	now := time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC)
	seedFile(t, repo,
		projects.FigmaFileRow{FileKey: "fk-1", Name: "F", TeamID: "t", ProjectID: "p1", LastModified: now.AddDate(0, -1, 0)},
		projects.FigmaProjectMapping{ProjectID: "p1", Domain: "D", Product: "P", EnabledForAutosync: false, MappedByUserID: userID},
		[]projects.FigmaPageRow{{FileKey: "fk-1", PageID: "0:1", Name: "Final", OrderIndex: 0, ContentHash: "h", Classification: projects.PageClassFinal, PersonaHint: "default"}},
		nil, now,
	)
	plan, _ := newPlanner(d, now).Plan(context.Background(), tenantID, "fk-1")
	if plan.FileSkip == nil || plan.FileSkip.Code != SkipMappingDisabled {
		t.Fatalf("expected mapping_disabled, got %+v", plan.FileSkip)
	}
}

func TestJoinFlowPath_TrimsEmpty(t *testing.T) {
	cases := []struct{ d, p, sp, sf, want string }{
		{"Markets", "Indian Stocks", "Wallet", "Main Flow", "Markets/Indian Stocks/Wallet/Main Flow"},
		{"Markets", "X", "", "", "Markets/X"},
		{"", "", "Wallet", "Main Flow", "Wallet/Main Flow"},
	}
	for _, tc := range cases {
		got := JoinFlowPath(tc.d, tc.p, tc.sp, tc.sf)
		if got != tc.want {
			t.Errorf("got %q want %q", got, tc.want)
		}
	}
}
