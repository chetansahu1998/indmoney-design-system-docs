package projects

import (
	"context"
	"io"
	"log/slog"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
)

// testLogger returns a slog.Logger that discards output so test runs stay
// quiet. The worker's structured-log entries during a flush would otherwise
// flood `go test -v`.
func testLogger(t *testing.T) *slog.Logger {
	t.Helper()
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// Phase 6 U1 — smoke test for the RebuildGraphIndex worker.
//
// The test seeds a minimal (tenant, user, project, flow, decision) fixture,
// runs RebuildFull synchronously via a single-worker pool, then loads the
// resulting graph_index slice and asserts the row + edge shape.
//
// Manifest + tokens paths are left empty so component / token rows don't
// participate; that path has its own dedicated test once the fixtures are
// committed.

func TestGraphRebuild_HappyPath(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	ctx := context.Background()

	// ── Fixture: one project (Indian Stocks / F&O / Learn) with one flow.
	repo := NewTenantRepo(d.DB, tA)
	proj, err := repo.UpsertProject(ctx, Project{
		Name: "Learn Touchpoints", Platform: GraphPlatformMobile,
		Product: "Indian Stocks", Path: "F&O/Learn", OwnerUserID: uA,
	})
	if err != nil {
		t.Fatalf("upsert project: %v", err)
	}
	flowID := uuid.NewString()
	if _, err := d.ExecContext(ctx,
		`INSERT INTO flows (id, project_id, tenant_id, file_id, section_id, name, persona_id, created_at, updated_at)
		 VALUES (?, ?, ?, ?, NULL, ?, NULL, ?, ?)`,
		flowID, proj.ID, tA, "fileabc", "Onboarding",
		time.Now().UTC().Format(time.RFC3339), time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		t.Fatalf("insert flow: %v", err)
	}

	// ── Run: full rebuild for (tenantA, mobile)
	pool := &GraphRebuildPool{
		Size: 1,
		DB:   d.DB,
		Log:  testLogger(t),
	}
	if err := pool.RebuildFull(ctx, tA, GraphPlatformMobile); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	// ── Assert: rows materialised
	rows, err := repo.LoadGraph(ctx, GraphPlatformMobile)
	if err != nil {
		t.Fatalf("load graph: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("expected non-empty graph_index slice")
	}

	got := nodeIDsByType(rows)

	// Product node
	if len(got[GraphNodeProduct]) != 1 || got[GraphNodeProduct][0] != "product:indian-stocks" {
		t.Errorf("expected product:indian-stocks; got %v", got[GraphNodeProduct])
	}
	// Folder nodes — F&O and F&O/Learn
	wantFolders := []string{
		"folder:indian-stocks/F&O",
		"folder:indian-stocks/F&O/Learn",
	}
	sort.Strings(got[GraphNodeFolder])
	sort.Strings(wantFolders)
	if !equalStrings(got[GraphNodeFolder], wantFolders) {
		t.Errorf("folder node ids = %v; want %v", got[GraphNodeFolder], wantFolders)
	}
	// Flow node
	if len(got[GraphNodeFlow]) != 1 || got[GraphNodeFlow][0] != flowNodeID(flowID) {
		t.Errorf("expected flow node %s; got %v", flowNodeID(flowID), got[GraphNodeFlow])
	}

	// Hierarchy chain: flow → deepest folder → mid folder → product
	flowRow := mustFindRow(t, rows, flowNodeID(flowID))
	if flowRow.ParentID != "folder:indian-stocks/F&O/Learn" {
		t.Errorf("flow.parent_id = %q; want folder:indian-stocks/F&O/Learn", flowRow.ParentID)
	}
	deepFolder := mustFindRow(t, rows, "folder:indian-stocks/F&O/Learn")
	if deepFolder.ParentID != "folder:indian-stocks/F&O" {
		t.Errorf("deep folder.parent_id = %q; want folder:indian-stocks/F&O", deepFolder.ParentID)
	}
	midFolder := mustFindRow(t, rows, "folder:indian-stocks/F&O")
	if midFolder.ParentID != "product:indian-stocks" {
		t.Errorf("mid folder.parent_id = %q; want product:indian-stocks", midFolder.ParentID)
	}

	// Aggregate exposes the wire shape.
	agg := BuildAggregate(rows, GraphPlatformMobile)
	if agg.Platform != GraphPlatformMobile {
		t.Errorf("aggregate platform = %q; want mobile", agg.Platform)
	}
	if len(agg.Nodes) != len(rows) {
		t.Errorf("aggregate node count = %d; want %d", len(agg.Nodes), len(rows))
	}
	if len(agg.Edges) == 0 {
		t.Error("expected at least one hierarchy edge")
	}
}

func TestGraphRebuild_TenantIsolation(t *testing.T) {
	d, tA, tB, uA := newTestDB(t)
	ctx := context.Background()

	// Project + flow only in tenant A.
	repoA := NewTenantRepo(d.DB, tA)
	proj, err := repoA.UpsertProject(ctx, Project{
		Name: "A-only", Platform: GraphPlatformMobile,
		Product: "Indian Stocks", Path: "F&O", OwnerUserID: uA,
	})
	if err != nil {
		t.Fatalf("upsert A: %v", err)
	}
	if _, err := d.ExecContext(ctx,
		`INSERT INTO flows (id, project_id, tenant_id, file_id, section_id, name, persona_id, created_at, updated_at)
		 VALUES (?, ?, ?, ?, NULL, ?, NULL, ?, ?)`,
		uuid.NewString(), proj.ID, tA, "fileA", "FlowA",
		time.Now().UTC().Format(time.RFC3339), time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		t.Fatalf("insert flow A: %v", err)
	}

	pool := &GraphRebuildPool{Size: 1, DB: d.DB, Log: testLogger(t)}
	if err := pool.RebuildFull(ctx, tA, GraphPlatformMobile); err != nil {
		t.Fatalf("rebuild A: %v", err)
	}
	if err := pool.RebuildFull(ctx, tB, GraphPlatformMobile); err != nil {
		t.Fatalf("rebuild B: %v", err)
	}

	rowsA, _ := NewTenantRepo(d.DB, tA).LoadGraph(ctx, GraphPlatformMobile)
	rowsB, _ := NewTenantRepo(d.DB, tB).LoadGraph(ctx, GraphPlatformMobile)
	if len(rowsA) == 0 {
		t.Fatal("tenant A expected rows; got none")
	}
	if len(rowsB) != 0 {
		t.Fatalf("tenant B should have NO rows (no projects); got %d", len(rowsB))
	}
}

func TestGraphRebuild_PlatformPartition(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	ctx := context.Background()
	repo := NewTenantRepo(d.DB, tA)

	// Two projects sharing (tenant, product, path) on different platforms.
	// Pre-fix this would infinite-recurse on UpsertProject; the Phase 6
	// fix in repository.go disambiguates the slug with a platform suffix.
	if _, err := repo.UpsertProject(ctx, Project{
		Name: "Mobile", Platform: GraphPlatformMobile,
		Product: "Tax", Path: "Returns", OwnerUserID: uA,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.UpsertProject(ctx, Project{
		Name: "Web", Platform: GraphPlatformWeb,
		Product: "Tax", Path: "Returns", OwnerUserID: uA,
	}); err != nil {
		t.Fatal(err)
	}

	pool := &GraphRebuildPool{Size: 1, DB: d.DB, Log: testLogger(t)}
	if err := pool.RebuildFull(ctx, tA, GraphPlatformMobile); err != nil {
		t.Fatalf("rebuild mobile: %v", err)
	}
	if err := pool.RebuildFull(ctx, tA, GraphPlatformWeb); err != nil {
		t.Fatalf("rebuild web: %v", err)
	}

	mobile, _ := repo.LoadGraph(ctx, GraphPlatformMobile)
	web, _ := repo.LoadGraph(ctx, GraphPlatformWeb)
	if len(mobile) == 0 || len(web) == 0 {
		t.Fatalf("expected rows on both platforms; got mobile=%d web=%d", len(mobile), len(web))
	}
	for _, r := range mobile {
		if r.Platform != GraphPlatformMobile {
			t.Errorf("mobile slice contains row with platform=%q (id=%s)", r.Platform, r.ID)
		}
	}
	for _, r := range web {
		if r.Platform != GraphPlatformWeb {
			t.Errorf("web slice contains row with platform=%q (id=%s)", r.Platform, r.ID)
		}
	}
}

func TestBuildAggregate_HierarchyEdges(t *testing.T) {
	now := time.Now().UTC()
	rows := []GraphIndexRow{
		{ID: "product:p", Type: GraphNodeProduct, Label: "P", Platform: GraphPlatformMobile, MaterializedAt: now},
		{ID: "folder:p/x", Type: GraphNodeFolder, Label: "X", ParentID: "product:p", Platform: GraphPlatformMobile, MaterializedAt: now},
		{ID: "flow:f", Type: GraphNodeFlow, Label: "F", ParentID: "folder:p/x", Platform: GraphPlatformMobile, MaterializedAt: now,
			EdgesUses: []string{"component:c"}},
	}
	agg := BuildAggregate(rows, GraphPlatformMobile)
	if len(agg.Nodes) != 3 {
		t.Errorf("nodes = %d; want 3", len(agg.Nodes))
	}
	// Edges: 2 hierarchy + 1 uses = 3
	if len(agg.Edges) != 3 {
		t.Errorf("edges = %d; want 3", len(agg.Edges))
	}
	classCount := map[string]int{}
	for _, e := range agg.Edges {
		classCount[e.Class]++
	}
	if classCount[string(GraphEdgeHierarchy)] != 2 {
		t.Errorf("hierarchy edges = %d; want 2", classCount[string(GraphEdgeHierarchy)])
	}
	if classCount[string(GraphEdgeUses)] != 1 {
		t.Errorf("uses edges = %d; want 1", classCount[string(GraphEdgeUses)])
	}
}

// ─── Test helpers ───────────────────────────────────────────────────────────

func nodeIDsByType(rows []GraphIndexRow) map[GraphNodeKind][]string {
	out := map[GraphNodeKind][]string{}
	for _, r := range rows {
		out[r.Type] = append(out[r.Type], r.ID)
	}
	return out
}

func mustFindRow(t *testing.T, rows []GraphIndexRow, id string) GraphIndexRow {
	t.Helper()
	for _, r := range rows {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("row %q not found in slice of %d", id, len(rows))
	return GraphIndexRow{}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
