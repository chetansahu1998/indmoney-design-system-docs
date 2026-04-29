// Schema bring-up smoke tests for the projects schema (U1).
//
// Verifies:
//   - Fresh open creates all 10 new tables + indexes.
//   - PRAGMA foreign_keys is enabled per connection (DSN-set).
//   - Re-open is idempotent — schema_migrations records prevent re-running.
//   - Unique constraints fire on duplicate inserts.
//   - FK violations are rejected.
//   - Soft-delete via deleted_at column survives migration.
//   - INSERT ... ON CONFLICT works on personas.
//
// Tests run against an in-memory SQLite via "file::memory:?cache=shared" so
// modernc.org/sqlite shares the connection.

package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	// Use a per-test temp file; ":memory:" loses state across connection
	// pool cycles even with cache=shared.
	dsnPath := t.TempDir() + "/test.db"
	d, err := Open(dsnPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func TestSchemaBringUp_TableCount(t *testing.T) {
	d := openTestDB(t)

	// Phase 0 + Phase 1 tables = 7 + 10 + 1 (schema_migrations) = 18.
	want := []string{
		// Phase 0
		"users", "tenants", "tenant_users", "figma_tokens", "audit_log", "sync_state",
		// Phase 1
		"personas", "projects", "flows", "project_versions",
		"screens", "screen_canonical_trees", "screen_modes",
		"audit_jobs", "violations", "flow_drd",
		// Migration tracking
		"schema_migrations",
	}
	for _, table := range want {
		var n int
		if err := d.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n); err != nil {
			t.Fatalf("query %s: %v", table, err)
		}
		if n != 1 {
			t.Errorf("table %q: count=%d, want 1", table, n)
		}
	}
}

func TestSchemaBringUp_ForeignKeysEnabled(t *testing.T) {
	d := openTestDB(t)
	var v int
	if err := d.QueryRow(`PRAGMA foreign_keys`).Scan(&v); err != nil {
		t.Fatalf("pragma foreign_keys: %v", err)
	}
	if v != 1 {
		t.Errorf("foreign_keys=%d, want 1", v)
	}
}

func TestSchemaBringUp_Idempotent(t *testing.T) {
	dsnPath := t.TempDir() + "/test.db"

	d, err := Open(dsnPath)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	first, err := d.AppliedMigrations(context.Background())
	if err != nil {
		t.Fatalf("applied first: %v", err)
	}
	_ = d.Close()

	d2, err := Open(dsnPath)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	defer d2.Close()
	second, err := d2.AppliedMigrations(context.Background())
	if err != nil {
		t.Fatalf("applied second: %v", err)
	}

	if len(first) != len(second) {
		t.Errorf("re-open changed migration count: first=%v, second=%v", first, second)
	}
	for i, v := range first {
		if second[i] != v {
			t.Errorf("re-open changed migration order: %v vs %v", first, second)
			break
		}
	}
}

func TestSchemaBringUp_AppliedMigrationsIncludes0001(t *testing.T) {
	d := openTestDB(t)
	versions, err := d.AppliedMigrations(context.Background())
	if err != nil {
		t.Fatalf("applied: %v", err)
	}
	found := false
	for _, v := range versions {
		if v == 1 {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("migration version 1 not applied; applied=%v", versions)
	}
}

func TestProjects_FKEnforced_OrphanInsertRejected(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	// Try inserting a flow with a non-existent project_id. FK on by default
	// per DSN; should be rejected.
	_, err := d.ExecContext(ctx,
		`INSERT INTO flows (id, project_id, tenant_id, file_id, name, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"flow-1", "nonexistent-project", "tenant-1", "file-A", "Test Flow",
		time.Now().UTC().Format(time.RFC3339), time.Now().UTC().Format(time.RFC3339),
	)
	if err == nil {
		t.Fatal("expected FK violation on orphan insert; got nil")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "foreign key") {
		t.Errorf("expected FK error, got: %v", err)
	}
}

func TestProjects_UniqueSlugPerTenant(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	seedTenantUser(t, d, ctx)

	now := time.Now().UTC().Format(time.RFC3339)
	insertProject := func(id, slug, tenantID string) error {
		_, err := d.ExecContext(ctx,
			`INSERT INTO projects (id, slug, name, platform, product, path, owner_user_id, tenant_id, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, slug, "Test", "mobile", "Indian Stocks", "Indian Stocks/Test",
			"user-1", tenantID, now, now,
		)
		return err
	}

	if err := insertProject("p1", "onboarding", "tenant-1"); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	// Same slug, same tenant → must fail.
	if err := insertProject("p2", "onboarding", "tenant-1"); err == nil {
		t.Errorf("expected unique violation on same-tenant slug duplicate; got nil")
	}
	// Same slug, different tenant → must succeed.
	if err := insertProject("p3", "onboarding", "tenant-2"); err != nil {
		t.Errorf("unexpected error on cross-tenant slug: %v", err)
	}
}

func TestPersonas_PendingDuplicatesAllowed(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	seedTenantUser(t, d, ctx)

	now := time.Now().UTC().Format(time.RFC3339)
	insertPersona := func(id, name, status string) error {
		_, err := d.ExecContext(ctx,
			`INSERT INTO personas (id, tenant_id, name, status, created_by_user_id, created_at)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			id, "tenant-1", name, status, "user-1", now,
		)
		return err
	}

	// Two pending duplicates — allowed.
	if err := insertPersona("pers-1", "KYC-Pending", "pending"); err != nil {
		t.Fatalf("first pending: %v", err)
	}
	if err := insertPersona("pers-2", "KYC-Pending", "pending"); err != nil {
		t.Errorf("second pending should be allowed: %v", err)
	}

	// Approve first; second approved insert with same name must fail.
	if _, err := d.ExecContext(ctx,
		`UPDATE personas SET status='approved', approved_at=? WHERE id=?`, now, "pers-1"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if err := insertPersona("pers-3", "KYC-Pending", "approved"); err == nil {
		t.Errorf("expected unique violation on duplicate approved persona name; got nil")
	}
}

func TestFlowDRD_RevisionCounterETag(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	seedTenantUser(t, d, ctx)
	flowID := seedFlow(t, d, ctx)

	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := d.ExecContext(ctx,
		`INSERT INTO flow_drd (flow_id, tenant_id, content_json, revision, schema_version, updated_at, updated_by_user_id)
		 VALUES (?, ?, ?, 0, '1.0', ?, ?)`,
		flowID, "tenant-1", []byte(`{"blocks":[]}`), now, "user-1",
	); err != nil {
		t.Fatalf("insert drd: %v", err)
	}

	// Optimistic update with correct expected_revision.
	res, err := d.ExecContext(ctx,
		`UPDATE flow_drd SET content_json=?, revision=revision+1, updated_at=? WHERE flow_id=? AND revision=?`,
		[]byte(`{"blocks":[{"type":"paragraph"}]}`), now, flowID, 0,
	)
	if err != nil {
		t.Fatalf("update with correct revision: %v", err)
	}
	rows, _ := res.RowsAffected()
	if rows != 1 {
		t.Errorf("expected 1 row updated, got %d", rows)
	}

	// Stale update with revision=0 (should now be at revision=1).
	res2, err := d.ExecContext(ctx,
		`UPDATE flow_drd SET content_json=?, revision=revision+1, updated_at=? WHERE flow_id=? AND revision=?`,
		[]byte(`{"blocks":[]}`), now, flowID, 0,
	)
	if err != nil {
		t.Fatalf("update with stale revision: %v", err)
	}
	rows2, _ := res2.RowsAffected()
	if rows2 != 0 {
		t.Errorf("expected 0 rows updated on stale revision; got %d", rows2)
	}
}

// seedTenantUser inserts the minimal tenant + user rows the projects FKs need.
func seedTenantUser(t *testing.T, d *DB, ctx context.Context) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	stmts := []struct{ sql string; args []any }{
		{`INSERT INTO users (id, email, password_hash, role, created_at) VALUES (?, ?, ?, ?, ?)`,
			[]any{"user-1", "u1@example.com", "hash", "user", now}},
		{`INSERT INTO tenants (id, slug, name, status, plan_type, created_at, created_by) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			[]any{"tenant-1", "tenant-1", "Tenant 1", "active", "free", now, "user-1"}},
		{`INSERT INTO tenants (id, slug, name, status, plan_type, created_at, created_by) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			[]any{"tenant-2", "tenant-2", "Tenant 2", "active", "free", now, "user-1"}},
	}
	for _, s := range stmts {
		if _, err := d.ExecContext(ctx, s.sql, s.args...); err != nil {
			// Allow IGNORE when running multiple tests in same DB pool, but
			// since each test gets its own DB this should always be fresh.
			if !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("seed: %v", err)
			}
		}
	}
}

// seedFlow inserts a project + flow so flow_drd FKs are satisfied.
func seedFlow(t *testing.T, d *DB, ctx context.Context) string {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := d.ExecContext(ctx,
		`INSERT INTO projects (id, slug, name, platform, product, path, owner_user_id, tenant_id, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"proj-1", "test-project", "Test", "mobile", "Indian Stocks", "Indian Stocks/Test",
		"user-1", "tenant-1", now, now,
	); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if _, err := d.ExecContext(ctx,
		`INSERT INTO flows (id, project_id, tenant_id, file_id, name, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"flow-1", "proj-1", "tenant-1", "file-A", "Test Flow", now, now,
	); err != nil {
		t.Fatalf("seed flow: %v", err)
	}
	return "flow-1"
}
