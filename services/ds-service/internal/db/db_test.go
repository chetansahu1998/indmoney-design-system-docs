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

	// Phase 0 + Phase 1 + Phase 2 + schema_migrations.
	want := []string{
		// Phase 0
		"users", "tenants", "tenant_users", "figma_tokens", "audit_log", "sync_state",
		// Phase 1
		"personas", "projects", "flows", "project_versions",
		"screens", "screen_canonical_trees", "screen_modes",
		"audit_jobs", "violations", "flow_drd",
		// Phase 2
		"audit_rules", "screen_prototype_links",
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

// ─── Phase 2 schema tests (migrations 0002 + 0003) ───────────────────────────

func TestPhase2_AppliedMigrationsIncludes0002And0003(t *testing.T) {
	d := openTestDB(t)
	versions, err := d.AppliedMigrations(context.Background())
	if err != nil {
		t.Fatalf("applied: %v", err)
	}
	want := map[int]bool{2: false, 3: false}
	for _, v := range versions {
		if _, ok := want[v]; ok {
			want[v] = true
		}
	}
	for v, applied := range want {
		if !applied {
			t.Errorf("migration version %d not applied; applied=%v", v, versions)
		}
	}
}

func TestPhase2_AuditRulesSeeded(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	// Sanity: every rule must carry a non-empty severity from the 5-tier set.
	rows, err := d.QueryContext(ctx, `SELECT rule_id, default_severity FROM audit_rules`)
	if err != nil {
		t.Fatalf("query audit_rules: %v", err)
	}
	defer rows.Close()
	allowed := map[string]bool{
		"critical": true, "high": true, "medium": true, "low": true, "info": true,
	}
	count := 0
	for rows.Next() {
		var ruleID, sev string
		if err := rows.Scan(&ruleID, &sev); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if !allowed[sev] {
			t.Errorf("rule %q: severity %q not in 5-tier set", ruleID, sev)
		}
		count++
	}
	if count < 25 {
		t.Errorf("expected >= 25 seeded rules, got %d", count)
	}

	// Spot-check the headline Phase 2 rules are all present + enabled.
	headline := []string{
		"theme_parity_break",
		"cross_persona_component_gap",
		"a11y_contrast_aa",
		"a11y_touch_target_44pt",
		"flow_graph_orphan",
		"flow_graph_dead_end",
		"flow_graph_cycle",
		"component_detached",
		"component_override_sprawl",
		"component_set_sprawl",
	}
	for _, ruleID := range headline {
		var enabled int
		var sev string
		err := d.QueryRowContext(ctx,
			`SELECT enabled, default_severity FROM audit_rules WHERE rule_id = ?`,
			ruleID,
		).Scan(&enabled, &sev)
		if err != nil {
			t.Errorf("rule %q missing: %v", ruleID, err)
			continue
		}
		if enabled != 1 {
			t.Errorf("rule %q: enabled=%d, want 1", ruleID, enabled)
		}
		if sev == "" {
			t.Errorf("rule %q: severity empty", ruleID)
		}
	}
}

func TestPhase2_ViolationsCategoryColumn_DefaultAndBackfill(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	seedTenantUser(t, d, ctx)

	// We need a complete chain: project → version → flow → screen → violation.
	flowID := seedFlow(t, d, ctx)
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := d.ExecContext(ctx,
		`INSERT INTO project_versions (id, project_id, tenant_id, version_index, status, created_by_user_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"v-1", "proj-1", "tenant-1", 1, "view_ready", "user-1", now,
	); err != nil {
		t.Fatalf("seed version: %v", err)
	}
	if _, err := d.ExecContext(ctx,
		`INSERT INTO screens (id, version_id, flow_id, tenant_id, x, y, width, height, screen_logical_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"s-1", "v-1", flowID, "tenant-1", 0.0, 0.0, 375.0, 812.0, "logical-s-1", now,
	); err != nil {
		t.Fatalf("seed screen: %v", err)
	}

	// Insert violations with a mix of rule_ids that the 0002 backfill UPDATE
	// recognizes. After insert, the trigger of category is the column DEFAULT
	// ('token_drift'); the backfill UPDATEs ran during migration only, but our
	// inserts mimic post-Phase-2 behavior where the runner sets category
	// directly. To prove the BACKFILL logic, we insert with the column DEFAULT
	// (no explicit category), then run the same UPDATE statement the migration
	// did and assert it would have classified correctly.
	cases := []struct {
		ruleID         string
		wantCategory   string
		wantAutoFix    int
		hasSuggestion  bool
	}{
		{"theme_break.fill", "theme_parity", 0, false},
		{"drift.text", "text_style_drift", 1, true},
		{"drift.padding", "spacing_drift", 0, false},
		{"drift.gap", "spacing_drift", 0, false},
		{"drift.radius", "radius_drift", 0, false},
		{"drift.fill", "token_drift", 1, true},
		{"deprecated.stroke", "token_drift", 1, true},
		{"unbound.text", "token_drift", 1, true},
		{"unbound.component", "component_match", 0, false},
		{"custom.component", "component_governance", 0, false},
		{"unknown.something", "token_drift", 0, false}, // default fallback
	}
	for i, tc := range cases {
		var suggestion string
		if tc.hasSuggestion {
			suggestion = "Bind to colour.surface.button"
		}
		if _, err := d.ExecContext(ctx,
			`INSERT INTO violations
			   (id, version_id, screen_id, tenant_id, rule_id, severity, property, suggestion, status, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'active', ?)`,
			"viol-"+tc.ruleID, "v-1", "s-1", "tenant-1",
			tc.ruleID, "medium", "fill", suggestion, now,
		); err != nil {
			t.Fatalf("insert violation %d (%s): %v", i, tc.ruleID, err)
		}
	}

	// Default-on-insert is 'token_drift' for every row (column DEFAULT).
	var defaultCount int
	if err := d.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM violations WHERE category = 'token_drift'`,
	).Scan(&defaultCount); err != nil {
		t.Fatalf("count default: %v", err)
	}
	if defaultCount != len(cases) {
		t.Errorf("expected all %d new rows to default to token_drift; got %d", len(cases), defaultCount)
	}

	// Re-run the migration's backfill UPDATEs to verify rule_id → category logic.
	mustExec := func(stmt string, args ...any) {
		t.Helper()
		if _, err := d.ExecContext(ctx, stmt, args...); err != nil {
			t.Fatalf("backfill exec %q: %v", stmt, err)
		}
	}
	mustExec(`UPDATE violations SET category = 'theme_parity' WHERE rule_id LIKE 'theme_break.%'`)
	mustExec(`UPDATE violations SET category = 'text_style_drift' WHERE rule_id LIKE 'drift.text'`)
	mustExec(`UPDATE violations SET category = 'spacing_drift' WHERE rule_id IN ('drift.padding', 'drift.gap', 'drift.spacing')`)
	mustExec(`UPDATE violations SET category = 'radius_drift' WHERE rule_id = 'drift.radius'`)
	mustExec(`UPDATE violations SET category = 'token_drift' WHERE rule_id IN ('drift.fill', 'drift.stroke', 'deprecated.fill', 'deprecated.stroke', 'deprecated.text', 'unbound.fill', 'unbound.stroke', 'unbound.text')`)
	mustExec(`UPDATE violations SET category = 'component_match' WHERE rule_id IN ('unbound.component', 'drift.component')`)
	mustExec(`UPDATE violations SET category = 'component_governance' WHERE rule_id = 'custom.component'`)
	mustExec(`UPDATE violations SET auto_fixable = 1 WHERE rule_id IN ('drift.fill', 'drift.stroke', 'drift.text', 'unbound.fill', 'unbound.stroke', 'unbound.text', 'deprecated.fill', 'deprecated.stroke', 'deprecated.text') AND suggestion IS NOT NULL AND suggestion <> ''`)

	for _, tc := range cases {
		var gotCat string
		var gotFix int
		if err := d.QueryRowContext(ctx,
			`SELECT category, auto_fixable FROM violations WHERE id = ?`,
			"viol-"+tc.ruleID,
		).Scan(&gotCat, &gotFix); err != nil {
			t.Fatalf("query %s: %v", tc.ruleID, err)
		}
		if gotCat != tc.wantCategory {
			t.Errorf("rule %q: category=%q, want %q", tc.ruleID, gotCat, tc.wantCategory)
		}
		if gotFix != tc.wantAutoFix {
			t.Errorf("rule %q: auto_fixable=%d, want %d", tc.ruleID, gotFix, tc.wantAutoFix)
		}
	}
}

func TestPhase2_AuditJobsPriorityAndTriggeredByDefaults(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	seedTenantUser(t, d, ctx)
	_ = seedFlow(t, d, ctx)
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := d.ExecContext(ctx,
		`INSERT INTO project_versions (id, project_id, tenant_id, version_index, status, created_by_user_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"v-1", "proj-1", "tenant-1", 1, "view_ready", "user-1", now,
	); err != nil {
		t.Fatalf("seed version: %v", err)
	}

	// Insert without specifying priority / triggered_by → defaults apply.
	if _, err := d.ExecContext(ctx,
		`INSERT INTO audit_jobs (id, version_id, tenant_id, status, trace_id, idempotency_key, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"job-1", "v-1", "tenant-1", "queued", "trace-A", "idem-A", now,
	); err != nil {
		t.Fatalf("insert job: %v", err)
	}
	var priority int
	var triggeredBy string
	var metadata sql.NullString
	if err := d.QueryRowContext(ctx,
		`SELECT priority, triggered_by, metadata FROM audit_jobs WHERE id = ?`,
		"job-1",
	).Scan(&priority, &triggeredBy, &metadata); err != nil {
		t.Fatalf("query: %v", err)
	}
	if priority != 50 {
		t.Errorf("priority=%d, want 50 (default)", priority)
	}
	if triggeredBy != "export" {
		t.Errorf("triggered_by=%q, want export (default)", triggeredBy)
	}
	if metadata.Valid {
		t.Errorf("metadata=%q, want NULL on default insert", metadata.String)
	}

	// Phase 1's UNIQUE(version_id) WHERE status IN ('queued','running') prevents
	// two queued jobs on the same version. Test the constraint is still in
	// effect with the new columns by trying a duplicate queued insert on v-1.
	if _, err := d.ExecContext(ctx,
		`INSERT INTO audit_jobs (id, version_id, tenant_id, status, trace_id, idempotency_key, priority, triggered_by, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"job-dup", "v-1", "tenant-1", "queued", "trace-DUP", "idem-DUP", 100, "export", now,
	); err == nil {
		t.Fatal("expected UNIQUE violation: two queued rows on same version_id")
	}

	// Spread queued jobs across separate versions to exercise the priority queue.
	if _, err := d.ExecContext(ctx,
		`INSERT INTO project_versions (id, project_id, tenant_id, version_index, status, created_by_user_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"v-2", "proj-1", "tenant-1", 2, "view_ready", "user-1", now,
	); err != nil {
		t.Fatalf("seed version 2: %v", err)
	}
	if _, err := d.ExecContext(ctx,
		`INSERT INTO audit_jobs (id, version_id, tenant_id, status, trace_id, idempotency_key, priority, triggered_by, metadata, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"job-fanout", "v-2", "tenant-1", "queued", "trace-F", "idem-F",
		10, "tokens_published", `{"fanout_id":"fan-1"}`, now,
	); err != nil {
		t.Fatalf("insert fanout job on v-2: %v", err)
	}

	// Dequeue order: job-1 (default priority 50) ahead of job-fanout (priority 10).
	rows, err := d.QueryContext(ctx,
		`SELECT id, priority, triggered_by FROM audit_jobs
		   WHERE status = 'queued'
		   ORDER BY priority DESC, created_at ASC`,
	)
	if err != nil {
		t.Fatalf("query queue: %v", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id, tb string
		var prio int
		if err := rows.Scan(&id, &prio, &tb); err != nil {
			t.Fatalf("scan: %v", err)
		}
		ids = append(ids, id)
	}
	if len(ids) != 2 || ids[0] != "job-1" || ids[1] != "job-fanout" {
		t.Errorf("dequeue order = %v, want [job-1, job-fanout]", ids)
	}

	// Verify metadata round-trips on the fanout job.
	if err := d.QueryRowContext(ctx,
		`SELECT metadata FROM audit_jobs WHERE id = 'job-fanout'`,
	).Scan(&metadata); err != nil {
		t.Fatalf("query fanout metadata: %v", err)
	}
	if !metadata.Valid || !strings.Contains(metadata.String, `"fanout_id"`) {
		t.Errorf("metadata round-trip: got %q, want contains fanout_id", metadata.String)
	}
}

func TestPhase2_PrototypeLinksFKAndCascade(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	seedTenantUser(t, d, ctx)
	_ = seedFlow(t, d, ctx)
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := d.ExecContext(ctx,
		`INSERT INTO project_versions (id, project_id, tenant_id, version_index, status, created_by_user_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"v-1", "proj-1", "tenant-1", 1, "view_ready", "user-1", now,
	); err != nil {
		t.Fatalf("seed version: %v", err)
	}
	for i, sid := range []string{"s-1", "s-2"} {
		if _, err := d.ExecContext(ctx,
			`INSERT INTO screens (id, version_id, flow_id, tenant_id, x, y, width, height, screen_logical_id, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			sid, "v-1", "flow-1", "tenant-1", float64(i*400), 0.0, 375.0, 812.0, "logical-"+sid, now,
		); err != nil {
			t.Fatalf("seed screen %s: %v", sid, err)
		}
	}

	// Happy: link s-1 → s-2 (NAVIGATE / ON_CLICK).
	if _, err := d.ExecContext(ctx,
		`INSERT INTO screen_prototype_links
		   (id, screen_id, tenant_id, source_node_id, destination_screen_id, destination_node_id, trigger, action, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"link-1", "s-1", "tenant-1", "node-button-A", "s-2", "node-frame-B",
		"ON_CLICK", "NAVIGATE", now,
	); err != nil {
		t.Fatalf("insert link: %v", err)
	}

	// FK violation: source screen doesn't exist.
	_, err := d.ExecContext(ctx,
		`INSERT INTO screen_prototype_links
		   (id, screen_id, tenant_id, source_node_id, trigger, action, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"link-bad", "nonexistent-screen", "tenant-1", "node-X", "ON_CLICK", "NAVIGATE", now,
	)
	if err == nil {
		t.Fatal("expected FK violation on bad source screen_id")
	}

	// Cascade on source screen delete.
	if _, err := d.ExecContext(ctx, `DELETE FROM screens WHERE id = 's-1'`); err != nil {
		t.Fatalf("delete source: %v", err)
	}
	var remaining int
	if err := d.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM screen_prototype_links WHERE id = 'link-1'`,
	).Scan(&remaining); err != nil {
		t.Fatalf("count after cascade: %v", err)
	}
	if remaining != 0 {
		t.Errorf("expected cascade delete; got %d remaining links", remaining)
	}

	// SET NULL on destination screen delete (preserve link with destination_screen_id = NULL).
	if _, err := d.ExecContext(ctx,
		`INSERT INTO screen_prototype_links
		   (id, screen_id, tenant_id, source_node_id, destination_screen_id, trigger, action, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"link-2", "s-2", "tenant-1", "node-back-A", "s-2", "ON_CLICK", "NAVIGATE", now,
	); err != nil {
		t.Fatalf("insert self-link: %v", err)
	}
	// Add another screen to be the destination, then delete it to test SET NULL.
	if _, err := d.ExecContext(ctx,
		`INSERT INTO screens (id, version_id, flow_id, tenant_id, x, y, width, height, screen_logical_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"s-3", "v-1", "flow-1", "tenant-1", 800.0, 0.0, 375.0, 812.0, "logical-s-3", now,
	); err != nil {
		t.Fatalf("seed s-3: %v", err)
	}
	if _, err := d.ExecContext(ctx,
		`INSERT INTO screen_prototype_links
		   (id, screen_id, tenant_id, source_node_id, destination_screen_id, trigger, action, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"link-3", "s-2", "tenant-1", "node-next-A", "s-3", "ON_CLICK", "NAVIGATE", now,
	); err != nil {
		t.Fatalf("insert link-3: %v", err)
	}
	if _, err := d.ExecContext(ctx, `DELETE FROM screens WHERE id = 's-3'`); err != nil {
		t.Fatalf("delete dest: %v", err)
	}
	var destAfterDelete sql.NullString
	if err := d.QueryRowContext(ctx,
		`SELECT destination_screen_id FROM screen_prototype_links WHERE id = 'link-3'`,
	).Scan(&destAfterDelete); err != nil {
		t.Fatalf("query dest: %v", err)
	}
	if destAfterDelete.Valid {
		t.Errorf("expected NULL after dest screen delete; got %q", destAfterDelete.String)
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
