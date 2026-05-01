package projects

import (
	"context"
	"errors"
	"testing"
)

// Phase 4 U12 — GetViolation tests.

func TestRepo_GetViolation_HappyPath(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	versionID, screens := seedFlowAndScreens(t, repo, uA)
	vID := seedViolation(t, repo, versionID, screens[0], tA)

	// Resolve slug from the seeded project.
	var slug string
	if err := d.DB.QueryRow(
		`SELECT p.slug FROM project_versions pv JOIN projects p ON p.id = pv.project_id WHERE pv.id = ?`, versionID,
	).Scan(&slug); err != nil {
		t.Fatalf("slug lookup: %v", err)
	}

	d2, err := repo.GetViolation(context.Background(), slug, vID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if d2.ID != vID {
		t.Errorf("id mismatch: %s vs %s", d2.ID, vID)
	}
	if d2.ProjectSlug != slug {
		t.Errorf("slug mismatch: %s vs %s", d2.ProjectSlug, slug)
	}
	if d2.RuleID != "theme_parity.fill" {
		t.Errorf("rule_id mismatch: %s", d2.RuleID)
	}
}

func TestRepo_GetViolation_CrossTenantNotFound(t *testing.T) {
	d, tA, tB, uA := newTestDB(t)
	repoA := NewTenantRepo(d.DB, tA)
	versionID, screens := seedFlowAndScreens(t, repoA, uA)
	vID := seedViolation(t, repoA, versionID, screens[0], tA)

	var slug string
	if err := d.DB.QueryRow(
		`SELECT p.slug FROM project_versions pv JOIN projects p ON p.id = pv.project_id WHERE pv.id = ?`, versionID,
	).Scan(&slug); err != nil {
		t.Fatalf("slug lookup: %v", err)
	}

	repoB := NewTenantRepo(d.DB, tB)
	if _, err := repoB.GetViolation(context.Background(), slug, vID); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestRepo_GetViolation_SlugMismatchNotFound(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	versionID, screens := seedFlowAndScreens(t, repo, uA)
	vID := seedViolation(t, repo, versionID, screens[0], tA)

	if _, err := repo.GetViolation(context.Background(), "wrong-slug", vID); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound for wrong slug, got %v", err)
	}
}
