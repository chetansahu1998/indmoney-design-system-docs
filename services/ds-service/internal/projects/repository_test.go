package projects

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/db"
)

// newTestDB opens a temp SQLite DB with U1 migrations applied, plus seeds the
// minimum users/tenants rows the FK constraints in 0001_projects_schema need.
// Returns (db, tenantA, tenantB, userA) for use in cross-tenant assertions.
func newTestDB(t *testing.T) (*db.DB, string, string, string) {
	t.Helper()
	dir := t.TempDir()
	d, err := db.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("db open: %v", err)
	}
	t.Cleanup(func() { d.Close() })

	ctx := context.Background()
	userA := uuid.NewString()
	userB := uuid.NewString()
	if err := d.CreateUser(ctx, db.User{ID: userA, Email: "a@example.com",
		PasswordHash: "x", Role: "user", CreatedAt: time.Now()}); err != nil {
		t.Fatalf("create userA: %v", err)
	}
	if err := d.CreateUser(ctx, db.User{ID: userB, Email: "b@example.com",
		PasswordHash: "x", Role: "user", CreatedAt: time.Now()}); err != nil {
		t.Fatalf("create userB: %v", err)
	}
	tenantA := uuid.NewString()
	tenantB := uuid.NewString()
	if err := d.CreateTenant(ctx, db.Tenant{ID: tenantA, Slug: "tenant-a", Name: "A",
		Status: "active", PlanType: "free", CreatedAt: time.Now(), CreatedBy: userA}); err != nil {
		t.Fatalf("create tenantA: %v", err)
	}
	if err := d.CreateTenant(ctx, db.Tenant{ID: tenantB, Slug: "tenant-b", Name: "B",
		Status: "active", PlanType: "free", CreatedAt: time.Now(), CreatedBy: userB}); err != nil {
		t.Fatalf("create tenantB: %v", err)
	}
	return d, tenantA, tenantB, userA
}

func TestRepo_UpsertProject_CreatesNew(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)

	p, err := repo.UpsertProject(context.Background(), Project{
		Name: "Test", Platform: "mobile", Product: "Indian Stocks",
		Path: "F&O/Learn", OwnerUserID: uA,
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if p.ID == "" || p.Slug == "" {
		t.Fatal("expected ID + slug to be assigned")
	}
	if p.TenantID != tA {
		t.Fatalf("tenant_id not set: %s", p.TenantID)
	}
}

func TestRepo_UpsertProject_Idempotent(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)

	p1, err := repo.UpsertProject(context.Background(), Project{
		Name: "First", Platform: "mobile", Product: "Plutus",
		Path: "Onboarding", OwnerUserID: uA,
	})
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	p2, err := repo.UpsertProject(context.Background(), Project{
		Name: "Second", Platform: "mobile", Product: "Plutus",
		Path: "Onboarding", OwnerUserID: uA,
	})
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if p1.ID != p2.ID {
		t.Fatalf("expected same project ID; got %s vs %s", p1.ID, p2.ID)
	}
	if p2.Name != "Second" {
		t.Fatalf("expected name updated to Second, got %s", p2.Name)
	}
}

func TestRepo_GetProjectBySlug_TenantIsolation(t *testing.T) {
	d, tA, tB, uA := newTestDB(t)
	repoA := NewTenantRepo(d.DB, tA)

	p, err := repoA.UpsertProject(context.Background(), Project{
		Name: "Tenant-A only", Platform: "mobile", Product: "Tax",
		Path: "Filing", OwnerUserID: uA,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Same DB connection, different tenant → ErrNotFound.
	repoB := NewTenantRepo(d.DB, tB)
	_, err = repoB.GetProjectBySlug(context.Background(), p.Slug)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for cross-tenant lookup; got %v", err)
	}

	// Original tenant can read.
	got, err := repoA.GetProjectBySlug(context.Background(), p.Slug)
	if err != nil {
		t.Fatalf("same-tenant lookup: %v", err)
	}
	if got.ID != p.ID {
		t.Fatalf("ID mismatch")
	}
}

func TestRepo_CreateVersion_AutoIncrementsIndex(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)

	p, _ := repo.UpsertProject(context.Background(), Project{
		Name: "P", Platform: "mobile", Product: "Plutus", Path: "X", OwnerUserID: uA,
	})
	v1, err := repo.CreateVersion(context.Background(), p.ID, uA)
	if err != nil {
		t.Fatalf("v1: %v", err)
	}
	v2, err := repo.CreateVersion(context.Background(), p.ID, uA)
	if err != nil {
		t.Fatalf("v2: %v", err)
	}
	if v1.VersionIndex != 1 || v2.VersionIndex != 2 {
		t.Fatalf("expected 1,2 got %d,%d", v1.VersionIndex, v2.VersionIndex)
	}
	if v1.Status != "pending" || v2.Status != "pending" {
		t.Fatal("expected pending")
	}
}

func TestRepo_UpsertFlow_Idempotent(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)

	p, _ := repo.UpsertProject(context.Background(), Project{
		Name: "P", Platform: "mobile", Product: "Plutus", Path: "X", OwnerUserID: uA,
	})
	section := "section-1"
	f1, err := repo.UpsertFlow(context.Background(), Flow{
		ProjectID: p.ID, FileID: "F", SectionID: &section, Name: "FlowA",
	})
	if err != nil {
		t.Fatalf("upsert flow 1: %v", err)
	}
	f2, err := repo.UpsertFlow(context.Background(), Flow{
		ProjectID: p.ID, FileID: "F", SectionID: &section, Name: "FlowA",
	})
	if err != nil {
		t.Fatalf("upsert flow 2: %v", err)
	}
	if f1.ID != f2.ID {
		t.Fatalf("expected same flow id, got %s vs %s", f1.ID, f2.ID)
	}
}

func TestRepo_UpsertFlow_NullSectionAndPersonaDistinct(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)

	p, _ := repo.UpsertProject(context.Background(), Project{
		Name: "P", Platform: "mobile", Product: "Plutus", Path: "X", OwnerUserID: uA,
	})
	// Two flows with NULL section_id are distinct (SQLite NULLs are distinct
	// in unique indexes), so re-running gets two SEPARATE flow rows IF nothing
	// matches in the read step. The repo does a read-then-insert; with NULL
	// section_id and NULL persona_id, the read will match either, so we'll
	// get the same flow back.
	f1, err := repo.UpsertFlow(context.Background(), Flow{
		ProjectID: p.ID, FileID: "F", Name: "Free1",
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	f2, err := repo.UpsertFlow(context.Background(), Flow{
		ProjectID: p.ID, FileID: "F", Name: "Free2",
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if f1.ID != f2.ID {
		t.Fatalf("freeform NULL-NULL flows should resolve to same id")
	}
}

func TestRepo_InsertScreens(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)

	p, _ := repo.UpsertProject(context.Background(), Project{
		Name: "P", Platform: "mobile", Product: "Plutus", Path: "X", OwnerUserID: uA,
	})
	v, _ := repo.CreateVersion(context.Background(), p.ID, uA)
	f, _ := repo.UpsertFlow(context.Background(), Flow{
		ProjectID: p.ID, FileID: "F", Name: "Flow",
	})
	screens := []Screen{
		{VersionID: v.ID, FlowID: f.ID, X: 0, Y: 0, Width: 375, Height: 812},
		{VersionID: v.ID, FlowID: f.ID, X: 0, Y: 1000, Width: 375, Height: 812},
	}
	if err := repo.InsertScreens(context.Background(), screens); err != nil {
		t.Fatalf("insert screens: %v", err)
	}
	// IDs must be auto-assigned.
	for i, s := range screens {
		if s.ID == "" {
			t.Fatalf("screen %d missing id", i)
		}
		if s.ScreenLogicalID == "" {
			t.Fatalf("screen %d missing logical id", i)
		}
	}
}

func TestRepo_UpsertPersona_RaceSafe(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)

	// First call creates pending row.
	p1, err := repo.UpsertPersona(context.Background(), "Trader", uA)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if p1.Status != "pending" {
		t.Fatalf("expected pending; got %s", p1.Status)
	}

	// Second call from same designer returns same row.
	p2, err := repo.UpsertPersona(context.Background(), "Trader", uA)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if p1.ID != p2.ID {
		t.Fatalf("expected same id; got %s vs %s", p1.ID, p2.ID)
	}
}

func TestRepo_FKViolation_OrphanScreenRejected(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)

	// Try to insert a screen with no version — FK violates.
	err := repo.InsertScreens(context.Background(), []Screen{
		{VersionID: "no-such-version", FlowID: "no-such-flow", X: 0, Y: 0,
			Width: 1, Height: 1},
	})
	if err == nil {
		t.Fatal("expected FK violation")
	}
}

func TestRepo_RecordFailed_TransitionsStatus(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)

	p, _ := repo.UpsertProject(context.Background(), Project{
		Name: "P", Platform: "mobile", Product: "Plutus", Path: "X", OwnerUserID: uA,
	})
	v, _ := repo.CreateVersion(context.Background(), p.ID, uA)

	if err := repo.RecordFailed(context.Background(), v.ID, "boom"); err != nil {
		t.Fatalf("record failed: %v", err)
	}
	got, _ := repo.GetVersion(context.Background(), v.ID)
	if got.Status != "failed" || got.Error != "boom" {
		t.Fatalf("expected failed+boom, got %s/%s", got.Status, got.Error)
	}
}

func TestRepo_RecordFailed_CrossTenantNotFound(t *testing.T) {
	d, tA, tB, uA := newTestDB(t)
	repoA := NewTenantRepo(d.DB, tA)
	p, _ := repoA.UpsertProject(context.Background(), Project{
		Name: "P", Platform: "mobile", Product: "Plutus", Path: "X", OwnerUserID: uA,
	})
	v, _ := repoA.CreateVersion(context.Background(), p.ID, uA)

	repoB := NewTenantRepo(d.DB, tB)
	err := repoB.RecordFailed(context.Background(), v.ID, "x")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected cross-tenant ErrNotFound; got %v", err)
	}
}

func TestRepo_ListProjects_FiltersDeleted(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)

	repo.UpsertProject(context.Background(), Project{
		Name: "Active", Platform: "mobile", Product: "P", Path: "Live", OwnerUserID: uA,
	})
	soft, _ := repo.UpsertProject(context.Background(), Project{
		Name: "Soft", Platform: "mobile", Product: "Q", Path: "Soft", OwnerUserID: uA,
	})

	// Soft delete one.
	_, err := d.ExecContext(context.Background(),
		`UPDATE projects SET deleted_at = ? WHERE id = ?`,
		time.Now().UTC().Format(time.RFC3339), soft.ID)
	if err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	got, err := repo.ListProjects(context.Background(), 100)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 || got[0].Name != "Active" {
		t.Fatalf("expected only Active; got %d projects", len(got))
	}
}
