package projects

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Phase 7 U1 — ACL smoke tests. Covers the four invariants the plan calls
// out under "Test scenarios": product-default fallback, grant overrides
// default, revoke falls back to default, cross-tenant returns 404.

func TestResolveFlowRole_NoGrant_ReturnsProductDefault(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	ctx := context.Background()
	repo := NewTenantRepo(d.DB, tA)
	flowID := seedFlow(t, d.DB, tA, uA)

	role, err := repo.ResolveFlowRole(ctx, uA, flowID, FlowRoleEditor)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if role != FlowRoleEditor {
		t.Errorf("got %q, want editor (product default)", role)
	}
}

func TestResolveFlowRole_GrantBumpsRole(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	ctx := context.Background()
	repo := NewTenantRepo(d.DB, tA)
	flowID := seedFlow(t, d.DB, tA, uA)

	if err := repo.GrantFlowRole(ctx, flowID, uA, uA, FlowRoleOwner); err != nil {
		t.Fatalf("grant: %v", err)
	}
	role, err := repo.ResolveFlowRole(ctx, uA, flowID, FlowRoleViewer)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if role != FlowRoleOwner {
		t.Errorf("got %q, want owner (grant beats viewer default)", role)
	}
}

func TestResolveFlowRole_GrantDoesntDowngrade(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	ctx := context.Background()
	repo := NewTenantRepo(d.DB, tA)
	flowID := seedFlow(t, d.DB, tA, uA)

	if err := repo.GrantFlowRole(ctx, flowID, uA, uA, FlowRoleViewer); err != nil {
		t.Fatalf("grant: %v", err)
	}
	role, err := repo.ResolveFlowRole(ctx, uA, flowID, FlowRoleEditor)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if role != FlowRoleEditor {
		t.Errorf("got %q, want editor (default beats viewer grant)", role)
	}
}

func TestResolveFlowRole_RevokeFallsBack(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	ctx := context.Background()
	repo := NewTenantRepo(d.DB, tA)
	flowID := seedFlow(t, d.DB, tA, uA)

	if err := repo.GrantFlowRole(ctx, flowID, uA, uA, FlowRoleOwner); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if err := repo.RevokeFlowRole(ctx, flowID, uA, uA); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	role, err := repo.ResolveFlowRole(ctx, uA, flowID, FlowRoleViewer)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if role != FlowRoleViewer {
		t.Errorf("after revoke got %q, want viewer (default)", role)
	}
}

func TestResolveFlowRole_CrossTenant_404(t *testing.T) {
	d, tA, tB, uA := newTestDB(t)
	ctx := context.Background()
	flowID := seedFlow(t, d.DB, tA, uA)

	// Tenant B tries to grant on tenant A's flow — must be rejected.
	repoB := NewTenantRepo(d.DB, tB)
	err := repoB.GrantFlowRole(ctx, flowID, uA, uA, FlowRoleOwner)
	if err == nil {
		t.Fatal("expected ErrNotFound on cross-tenant grant; got nil")
	}
}

func TestMaxFlowRole(t *testing.T) {
	cases := []struct {
		a, b, want FlowRole
	}{
		{FlowRoleViewer, FlowRoleOwner, FlowRoleOwner},
		{FlowRoleEditor, FlowRoleEditor, FlowRoleEditor},
		{FlowRoleAdmin, FlowRoleViewer, FlowRoleAdmin},
		{"", FlowRoleEditor, FlowRoleEditor},
	}
	for _, c := range cases {
		if got := MaxFlowRole(c.a, c.b); got != c.want {
			t.Errorf("MaxFlowRole(%q, %q) = %q; want %q", c.a, c.b, got, c.want)
		}
	}
}

// seedFlow inserts a project + flow for the given tenant so ACL tests have
// something to grant against. Returns the flow id.
func seedFlow(t *testing.T, db *sql.DB, tenantID, ownerID string) string {
	t.Helper()
	repo := NewTenantRepo(db, tenantID)
	proj, err := repo.UpsertProject(context.Background(), Project{
		Name: "ACL Fixture", Platform: GraphPlatformMobile,
		Product: "Indian Stocks", Path: "F&O/ACL", OwnerUserID: ownerID,
	})
	if err != nil {
		t.Fatalf("seed project: %v", err)
	}
	flowID := uuid.NewString()
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO flows (id, project_id, tenant_id, file_id, section_id, name, persona_id, created_at, updated_at)
		 VALUES (?, ?, ?, ?, NULL, ?, NULL, ?, ?)`,
		flowID, proj.ID, tenantID, "fileFix", "Fixture",
		time.Now().UTC().Format(time.RFC3339), time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		t.Fatalf("seed flow: %v", err)
	}
	return flowID
}
