package projects

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Phase 8 U9 — smoke tests for the FTS5 search index + UpsertSearchIndexRows.
//
// We test the data layer directly. The HTTP handler's auth + ACL paths
// belong in server_test.go alongside the rest; these tests verify the
// indexing + query mechanics.

func TestSearchIndex_UpsertAndQuery(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	ctx := context.Background()

	// Seed some content to index.
	repo := NewTenantRepo(d.DB, tA)
	proj, err := repo.UpsertProject(ctx, Project{
		Name: "Onboarding", Platform: GraphPlatformMobile,
		Product: "Indian Stocks", Path: "F&O/Onboarding", OwnerUserID: uA,
	})
	if err != nil {
		t.Fatal(err)
	}
	flowID := uuid.NewString()
	if _, err := d.ExecContext(ctx,
		`INSERT INTO flows (id, project_id, tenant_id, file_id, section_id, name, persona_id, created_at, updated_at)
		 VALUES (?, ?, ?, ?, NULL, ?, NULL, ?, ?)`,
		flowID, proj.ID, tA, "f1", "Learn Touchpoints",
		time.Now().UTC().Format(time.RFC3339), time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		t.Fatal(err)
	}

	// Build + upsert search rows in one tx.
	rows, err := BuildSearchRowsForTenant(ctx, d.DB, tA)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	tx, err := d.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := UpsertSearchIndexRows(ctx, tx, tA, rows); err != nil {
		_ = tx.Rollback()
		t.Fatalf("upsert: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// FTS5 query — match against "touchpoints"; should hit the flow row.
	hit := mustQueryOne(t, d.DB, tA, "touchpoints")
	if hit.kind != "flow" || hit.id != flowID {
		t.Errorf("got hit %+v; want kind=flow id=%s", hit, flowID)
	}
}

func TestSearchIndex_TenantIsolation(t *testing.T) {
	d, tA, tB, uA := newTestDB(t)
	ctx := context.Background()

	repo := NewTenantRepo(d.DB, tA)
	proj, err := repo.UpsertProject(ctx, Project{
		Name: "TenantA-only", Platform: GraphPlatformMobile,
		Product: "Tax", Path: "Returns/A", OwnerUserID: uA,
	})
	if err != nil {
		t.Fatal(err)
	}
	flowID := uuid.NewString()
	if _, err := d.ExecContext(ctx,
		`INSERT INTO flows (id, project_id, tenant_id, file_id, section_id, name, persona_id, created_at, updated_at)
		 VALUES (?, ?, ?, ?, NULL, ?, NULL, ?, ?)`,
		flowID, proj.ID, tA, "fa", "Alpha-only-Flow-Title",
		time.Now().UTC().Format(time.RFC3339), time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		t.Fatal(err)
	}

	rowsA, _ := BuildSearchRowsForTenant(ctx, d.DB, tA)
	tx, _ := d.DB.BeginTx(ctx, nil)
	_ = UpsertSearchIndexRows(ctx, tx, tA, rowsA)
	_ = tx.Commit()

	// Tenant B has no projects + no search rows. Query as tenant B should
	// return nothing for tenant A's content.
	rows, err := d.DB.QueryContext(ctx,
		`SELECT entity_id FROM search_index_fts
		   WHERE tenant_id = ? AND search_index_fts MATCH ?`,
		tB, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var found []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		found = append(found, id)
	}
	if len(found) != 0 {
		t.Errorf("tenant B leaked tenant A rows: %v", found)
	}
}

func TestExtractPlainText_BlockNote(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"empty", "", ""},
		{"malformed", "not json", ""},
		{"text leaf", `{"content":[{"type":"text","text":"hello"}]}`, "hello"},
		{
			"nested blocks",
			`{"blocks":[{"content":[{"type":"text","text":"alpha"},{"type":"text","text":"beta"}]}]}`,
			"alpha beta",
		},
	}
	for _, c := range cases {
		got := extractPlainText([]byte(c.raw))
		if got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

type queryHit struct {
	kind, id string
}

func mustQueryOne(t *testing.T, db *sql.DB, tenantID, q string) queryHit {
	t.Helper()
	rows, err := db.QueryContext(context.Background(),
		`SELECT entity_kind, entity_id FROM search_index_fts
		   WHERE tenant_id = ? AND search_index_fts MATCH ?
		   ORDER BY rank LIMIT 1`,
		tenantID, q,
	)
	if err != nil {
		t.Fatalf("fts query: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatalf("no fts hit for %q", q)
	}
	var h queryHit
	if err := rows.Scan(&h.kind, &h.id); err != nil {
		t.Fatal(err)
	}
	return h
}
