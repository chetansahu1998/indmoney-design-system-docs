package projects

import (
	"context"
	"errors"
	"testing"
	"time"
)

// repository_figma_promote_test.go — covers the lookup helpers backing
// the Promote endpoint (Phase 2 U5) and the tree-linkage augmentation
// (U7).

func TestLookupFigmaFile_NotFound(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)

	_, err := repo.LookupFigmaFile(context.Background(), "missing", false)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestLookupFigmaFile_HappyPath(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()
	seenAt := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)

	// Seed a project + file via the same APIs the poller uses.
	if err := repo.UpsertFigmaProjects(ctx, "team-1",
		[]FigmaProjectRow{{ProjectID: "p-1", TeamID: "team-1", Name: "Networth"}},
		seenAt); err != nil {
		t.Fatalf("upsert projects: %v", err)
	}
	if err := repo.UpsertFigmaFilesShell(ctx, "p-1", "team-1",
		[]FigmaFileRow{{FileKey: "fk-1", Name: "Networth Mobile", LastModified: seenAt.Add(-time.Hour)}},
		seenAt); err != nil {
		t.Fatalf("upsert files: %v", err)
	}

	got, err := repo.LookupFigmaFile(ctx, "fk-1", false)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if got.FileKey != "fk-1" || got.Name != "Networth Mobile" || got.ProjectID != "p-1" {
		t.Fatalf("unexpected row: %+v", got)
	}
}

func TestLookupFigmaFile_DeletedExcludedByDefault(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()
	seenAt := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)

	if err := repo.UpsertFigmaProjects(ctx, "team-1",
		[]FigmaProjectRow{{ProjectID: "p-1", TeamID: "team-1", Name: "Networth"}},
		seenAt); err != nil {
		t.Fatalf("upsert projects: %v", err)
	}
	if err := repo.UpsertFigmaFilesShell(ctx, "p-1", "team-1",
		[]FigmaFileRow{{FileKey: "fk-1", Name: "n", LastModified: seenAt}},
		seenAt); err != nil {
		t.Fatalf("upsert files: %v", err)
	}
	// Sweep with a later timestamp marks the file as deleted.
	if _, err := repo.SweepFigmaFiles(ctx, "p-1", seenAt.Add(time.Hour)); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	if _, err := repo.LookupFigmaFile(ctx, "fk-1", false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for soft-deleted, got %v", err)
	}
	if _, err := repo.LookupFigmaFile(ctx, "fk-1", true); err != nil {
		t.Fatalf("includeDeleted=true should return row: %v", err)
	}
}

func TestLookupProjectByFileKey_NotLinked(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	_, err := repo.LookupProjectByFileKey(context.Background(), "fk-unlinked")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestPromoteRoundTrip_IdempotentAndTreeLinkage(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()
	seenAt := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)

	// Seed inventory: team + project + 2 files.
	if err := repo.UpsertFigmaTeam(ctx, "team-1", "Design"); err != nil {
		t.Fatalf("upsert team: %v", err)
	}
	if err := repo.UpsertFigmaProjects(ctx, "team-1",
		[]FigmaProjectRow{{ProjectID: "p-1", TeamID: "team-1", Name: "Networth"}},
		seenAt); err != nil {
		t.Fatalf("upsert projects: %v", err)
	}
	if err := repo.UpsertFigmaFilesShell(ctx, "p-1", "team-1",
		[]FigmaFileRow{
			{FileKey: "fk-A", Name: "Networth Mobile", LastModified: seenAt},
			{FileKey: "fk-B", Name: "Networth Web", LastModified: seenAt},
		},
		seenAt); err != nil {
		t.Fatalf("upsert files: %v", err)
	}

	// Promote fk-A by calling UpsertProject directly (the handler is
	// a thin wrapper around this). Two consecutive calls should be
	// idempotent on (tenant_id, file_id).
	p1, err := repo.UpsertProject(ctx, Project{
		Name: "Networth Mobile", Platform: "web", Product: "Networth",
		Path: "Networth Mobile", FileID: "fk-A", OwnerUserID: uA,
	})
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	p2, err := repo.UpsertProject(ctx, Project{
		Name: "Networth Mobile", Platform: "web", Product: "Networth",
		Path: "Networth Mobile", FileID: "fk-A", OwnerUserID: uA,
	})
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if p1.ID != p2.ID {
		t.Fatalf("expected same project id, got %s vs %s", p1.ID, p2.ID)
	}

	// LookupProjectByFileKey returns the linked row.
	got, err := repo.LookupProjectByFileKey(ctx, "fk-A")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if got.ID != p1.ID || got.Slug != p1.Slug {
		t.Fatalf("unexpected linked project: %+v vs %+v", got, p1)
	}

	// ProjectFileKeysForTenant: batch map keyed on file_key.
	linkedMap, err := repo.ProjectFileKeysForTenant(ctx)
	if err != nil {
		t.Fatalf("batch lookup: %v", err)
	}
	if _, ok := linkedMap["fk-A"]; !ok {
		t.Fatalf("expected fk-A in linked map, got %v", linkedMap)
	}
	if _, ok := linkedMap["fk-B"]; ok {
		t.Fatalf("fk-B should NOT be in linked map (unpromoted)")
	}

	// GetFigmaInventoryTree surfaces linked_project_id on the matched file.
	tree, err := repo.GetFigmaInventoryTree(ctx, "team-1", false)
	if err != nil {
		t.Fatalf("tree: %v", err)
	}
	var fkAFile, fkBFile *FigmaInventoryTreeNode
	for _, proj := range tree.Children {
		for _, file := range proj.Children {
			if file.ID == "fk-A" {
				fkAFile = file
			}
			if file.ID == "fk-B" {
				fkBFile = file
			}
		}
	}
	if fkAFile == nil || fkBFile == nil {
		t.Fatalf("missing files in tree: A=%v B=%v", fkAFile, fkBFile)
	}
	if fkAFile.LinkedProjectID != p1.ID {
		t.Fatalf("fk-A linkage: got %q, want %q", fkAFile.LinkedProjectID, p1.ID)
	}
	if fkAFile.LinkedProjectSlug != p1.Slug {
		t.Fatalf("fk-A slug: got %q, want %q", fkAFile.LinkedProjectSlug, p1.Slug)
	}
	if fkBFile.LinkedProjectID != "" {
		t.Fatalf("fk-B should have empty linkage, got %q", fkBFile.LinkedProjectID)
	}
}

func TestPromoteSlugCollision_AutoDisambiguates(t *testing.T) {
	// Promote two files whose generated slugs would collide and verify
	// the existing UpsertProject slug-disambiguation behaviour kicks in
	// (Phase 2 plan, U5 slug-collision scenario).
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()
	seenAt := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)

	if err := repo.UpsertFigmaProjects(ctx, "team-1",
		[]FigmaProjectRow{{ProjectID: "p-1", TeamID: "team-1", Name: "Networth"}},
		seenAt); err != nil {
		t.Fatalf("upsert projects: %v", err)
	}
	if err := repo.UpsertFigmaFilesShell(ctx, "p-1", "team-1",
		[]FigmaFileRow{
			{FileKey: "fk-X", Name: "Same Name", LastModified: seenAt},
			{FileKey: "fk-Y", Name: "Same Name", LastModified: seenAt},
		},
		seenAt); err != nil {
		t.Fatalf("upsert files: %v", err)
	}

	p1, err := repo.UpsertProject(ctx, Project{
		Name: "Same Name", Platform: "web", Product: "Networth",
		Path: "Same Name", FileID: "fk-X", OwnerUserID: uA,
	})
	if err != nil {
		t.Fatalf("upsert fk-X: %v", err)
	}
	p2, err := repo.UpsertProject(ctx, Project{
		Name: "Same Name", Platform: "web", Product: "Networth",
		Path: "Same Name", FileID: "fk-Y", OwnerUserID: uA,
	})
	if err != nil {
		t.Fatalf("upsert fk-Y: %v", err)
	}
	if p1.ID == p2.ID {
		t.Fatalf("two files with same name should produce two projects, got same id %s", p1.ID)
	}
	if p1.Slug == p2.Slug {
		t.Fatalf("slug should be disambiguated, got both %q", p1.Slug)
	}
}
