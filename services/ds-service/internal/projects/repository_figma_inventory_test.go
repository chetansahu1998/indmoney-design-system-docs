package projects

import (
	"context"
	"testing"
	"time"
)

// repository_figma_inventory_test.go — migration 0025 + repository sanity.
// Covers the round-trip path the inventory poller uses:
//
//   seed   → upsert team_seed
//   team   → upsert team metadata
//   tier-A → upsert projects + files (cheap shell)
//   tier-B → upsert pages + sections
//   sweep  → soft-delete projects/files/pages/sections not seen this cycle
//   tree   → assemble the admin-UI tree
//   runs   → start/finish a run row

func TestFigmaInventory_FullRoundTrip(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()
	seenAt := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)

	// 1. Add a team seed
	if err := repo.UpsertFigmaTeamSeed(ctx, FigmaTeamSeed{
		TeamID:        "team-1",
		TeamName:      "Design",
		AddedByUserID: uA,
		Enabled:       true,
	}); err != nil {
		t.Fatalf("upsert seed: %v", err)
	}
	seeds, err := repo.ListEnabledFigmaTeamSeeds(ctx)
	if err != nil || len(seeds) != 1 || seeds[0].TeamID != "team-1" {
		t.Fatalf("list seeds: %v %+v", err, seeds)
	}

	// 2. Team metadata
	if err := repo.UpsertFigmaTeam(ctx, "team-1", "Design Team"); err != nil {
		t.Fatalf("upsert team: %v", err)
	}

	// 3. Projects (tier-A)
	if err := repo.UpsertFigmaProjects(ctx, "team-1", []FigmaProjectRow{
		{ProjectID: "p-1", TeamID: "team-1", Name: "Networth"},
		{ProjectID: "p-2", TeamID: "team-1", Name: "INDstocks"},
	}, seenAt); err != nil {
		t.Fatalf("upsert projects: %v", err)
	}

	// 4. Files (tier-A shell)
	if err := repo.UpsertFigmaFilesShell(ctx, "p-1", "team-1", []FigmaFileRow{
		{FileKey: "fk-A", Name: "Networth Mobile", LastModified: seenAt.Add(-time.Hour)},
		{FileKey: "fk-B", Name: "Networth Web", LastModified: seenAt.Add(-2 * time.Hour)},
	}, seenAt); err != nil {
		t.Fatalf("upsert files: %v", err)
	}

	// 5. FilesNeedingPagesSync should return both (never synced)
	needSync, err := repo.FilesNeedingPagesSync(ctx, 50)
	if err != nil || len(needSync) != 2 {
		t.Fatalf("files needing sync: %v len=%d", err, len(needSync))
	}

	// 6. Pages + sections (tier-B) for fk-A
	pages := []FigmaPageRow{
		{FileKey: "fk-A", PageID: "0:1", Name: "Home", OrderIndex: 0, BackgroundColorHex: "#ffffff"},
		{FileKey: "fk-A", PageID: "0:2", Name: "Discover", OrderIndex: 1},
	}
	sections := []FigmaSectionRow{
		{FileKey: "fk-A", PageID: "0:1", SectionID: "3:7", Name: "Hero",
			X: 100, Y: 200, Width: 1440, Height: 720, OrderIndex: 0},
		{FileKey: "fk-A", PageID: "0:1", SectionID: "3:8", Name: "Cards",
			X: 0, Y: 1000, Width: 1440, Height: 500, OrderIndex: 1},
	}
	pn, sn, err := repo.UpsertFigmaPagesAndSections(ctx, "fk-A", pages, sections, seenAt)
	if err != nil || pn != 2 || sn != 2 {
		t.Fatalf("upsert pages+sections: %v p=%d s=%d", err, pn, sn)
	}

	if err := repo.UpdateFigmaFilePagesSynced(ctx, FigmaFileRow{
		FileKey:           "fk-A",
		Version:           "v123",
		PagesLastSyncedAt: seenAt,
		PagesSyncVersion:  "v123",
	}); err != nil {
		t.Fatalf("mark synced: %v", err)
	}

	// fk-A should drop out of FilesNeedingPagesSync; fk-B still needs it
	needSync, err = repo.FilesNeedingPagesSync(ctx, 50)
	if err != nil || len(needSync) != 1 || needSync[0].FileKey != "fk-B" {
		t.Fatalf("after sync, needSync = %v %+v", err, needSync)
	}

	// 7. Tree assembly
	tree, err := repo.GetFigmaInventoryTree(ctx, "team-1", false)
	if err != nil {
		t.Fatalf("tree: %v", err)
	}
	if tree.Kind != "team" || tree.Name != "Design Team" {
		t.Fatalf("tree root: %+v", tree)
	}
	if len(tree.Children) != 2 {
		t.Fatalf("expected 2 projects, got %d", len(tree.Children))
	}
	// Find Networth → fk-A → page 0:1 → 2 sections
	var fkAFile *FigmaInventoryTreeNode
	for _, p := range tree.Children {
		if p.ID == "p-1" {
			for _, f := range p.Children {
				if f.ID == "fk-A" {
					fkAFile = f
				}
			}
		}
	}
	if fkAFile == nil || len(fkAFile.Children) != 2 {
		t.Fatalf("fk-A file or pages missing: %+v", fkAFile)
	}
	var homePage *FigmaInventoryTreeNode
	for _, pg := range fkAFile.Children {
		if pg.ID == "0:1" {
			homePage = pg
		}
	}
	if homePage == nil || len(homePage.Children) != 2 {
		t.Fatalf("home page or sections missing: %+v", homePage)
	}
	if homePage.Children[0].Kind != "section" {
		t.Fatalf("section kind mismatch: %s", homePage.Children[0].Kind)
	}
	if homePage.Children[0].X == nil || *homePage.Children[0].X != 100 {
		t.Fatalf("section x: %+v", homePage.Children[0].X)
	}

	// 8. Sweep: simulate next crawl where p-2 disappears
	laterAt := seenAt.Add(1 * time.Hour)
	if err := repo.UpsertFigmaProjects(ctx, "team-1", []FigmaProjectRow{
		{ProjectID: "p-1", TeamID: "team-1", Name: "Networth"},
		// p-2 omitted
	}, laterAt); err != nil {
		t.Fatalf("re-upsert projects: %v", err)
	}
	n, err := repo.SweepFigmaProjects(ctx, "team-1", laterAt)
	if err != nil || n != 1 {
		t.Fatalf("sweep projects: %v n=%d", err, n)
	}
	treeAfter, err := repo.GetFigmaInventoryTree(ctx, "team-1", false)
	if err != nil {
		t.Fatalf("tree after sweep: %v", err)
	}
	if len(treeAfter.Children) != 1 {
		t.Fatalf("expected 1 live project after sweep, got %d", len(treeAfter.Children))
	}

	// 9. Run rows
	runID, err := repo.StartFigmaInventoryRun(ctx, seenAt)
	if err != nil || runID == 0 {
		t.Fatalf("start run: %v id=%d", err, runID)
	}
	if err := repo.FinishFigmaInventoryRun(ctx, runID, FigmaInventoryRunRow{
		TeamsCrawled:    1,
		ProjectsSeen:    1,
		FilesSeen:       2,
		FilesRefetched:  1,
		PagesUpserted:   2,
		SectionsUpserted: 2,
		ErrorCount:      0,
	}, nil); err != nil {
		t.Fatalf("finish run: %v", err)
	}
	runs, err := repo.ListFigmaInventoryRuns(ctx, 10)
	if err != nil || len(runs) != 1 || runs[0].FilesSeen != 2 {
		t.Fatalf("list runs: %v %+v", err, runs)
	}
}

func TestFigmaInventory_TenantIsolation(t *testing.T) {
	d, tA, tB, uA := newTestDB(t)
	ctx := context.Background()
	seenAt := time.Now().UTC()

	repoA := NewTenantRepo(d.DB, tA)
	repoB := NewTenantRepo(d.DB, tB)

	if err := repoA.UpsertFigmaTeamSeed(ctx, FigmaTeamSeed{
		TeamID: "shared-id", TeamName: "A's team", AddedByUserID: uA, Enabled: true,
	}); err != nil {
		t.Fatalf("seed A: %v", err)
	}
	if err := repoA.UpsertFigmaTeam(ctx, "shared-id", "A view"); err != nil {
		t.Fatalf("team A: %v", err)
	}
	if err := repoA.UpsertFigmaProjects(ctx, "shared-id", []FigmaProjectRow{
		{ProjectID: "p-x", TeamID: "shared-id", Name: "A proj"},
	}, seenAt); err != nil {
		t.Fatalf("upsert A projects: %v", err)
	}

	// B has no rows yet — tree should 404
	if _, err := repoB.GetFigmaInventoryTree(ctx, "shared-id", false); err != ErrNotFound {
		t.Fatalf("B should see no team, got err=%v", err)
	}
	seedsB, _ := repoB.ListFigmaTeamSeeds(ctx)
	if len(seedsB) != 0 {
		t.Fatalf("B leaked %d seeds from A", len(seedsB))
	}
}
