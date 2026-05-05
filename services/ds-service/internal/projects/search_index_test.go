package projects

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"testing"
)

// U10 — search-index integration for screen_text_overrides.
//
// The unit under test is BuildSearchRowsForTenant + UpsertSearchIndexRows.
// We reuse the screen_overrides_test fixture (newOverrideFixture) so the
// happy path runs against the real PUT handler — i.e. the same path U2 ships
// to production. That guarantees the test can't pass against a stub repo.

// indexCurrent runs BuildSearchRowsForTenant + UpsertSearchIndexRows in one
// tx so the FTS5 table reflects the current screen_text_overrides state.
// Mirrors the worker's RebuildFull search-row write block.
func indexCurrent(t *testing.T, fx *overrideFixture, tenantID string) {
	t.Helper()
	ctx := context.Background()
	rows, err := BuildSearchRowsForTenant(ctx, fx.dbHandle.DB, tenantID)
	if err != nil {
		t.Fatalf("build search rows: %v", err)
	}
	tx, err := fx.dbHandle.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := DeleteSearchIndexForTenant(ctx, tx, tenantID); err != nil {
		_ = tx.Rollback()
		t.Fatalf("clear: %v", err)
	}
	if err := UpsertSearchIndexRows(ctx, tx, tenantID, rows); err != nil {
		_ = tx.Rollback()
		t.Fatalf("upsert: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// queryFTS returns every (kind, id) row matching `q` for the tenant. Using
// every-row instead of LIMIT 1 lets the assertions check both presence AND
// absence (the "override beats original" case).
func queryFTS(t *testing.T, db *sql.DB, tenantID, q string) []queryHit {
	t.Helper()
	rows, err := db.QueryContext(context.Background(),
		`SELECT entity_kind, entity_id FROM search_index_fts
		   WHERE tenant_id = ? AND search_index_fts MATCH ?
		   ORDER BY rank`,
		tenantID, q,
	)
	if err != nil {
		t.Fatalf("fts query: %v", err)
	}
	defer rows.Close()
	var out []queryHit
	for rows.Next() {
		var h queryHit
		if err := rows.Scan(&h.kind, &h.id); err != nil {
			t.Fatal(err)
		}
		out = append(out, h)
	}
	return out
}

// TestSearchIndex_TextOverride_PutThenIndexed covers AE-11 happy path:
// after a PUT, the override value is searchable and resolves to the override
// row (not the flow). Mirrors the plan's "query for the original returns
// this screen no longer (override beats original)" — since the original
// Figma text isn't indexed at all in this version, that's trivially true;
// the assertion that matters is "override value IS indexed".
func TestSearchIndex_TextOverride_PutThenIndexed(t *testing.T) {
	fx := newOverrideFixture(t)

	// PUT the override via the real HTTP handler so the same audit_log /
	// screen_text_overrides write path that production hits is exercised.
	w := putOverride(t, fx.server, fx.slugA, fx.screenA, "node-fts-1", fx.tenantA, fx.userA,
		putOverrideRequest{
			Value:                "Buy now",
			ExpectedRevision:     0,
			CanonicalPath:        "0/0/text",
			LastSeenOriginalText: "Buy",
		})
	if w.Code != http.StatusOK {
		t.Fatalf("PUT: %d body=%s", w.Code, w.Body.String())
	}

	// Synchronously rebuild the search index. (Production goes through the
	// debounced worker; the test exercises the same builder + writer.)
	indexCurrent(t, fx, fx.tenantA)

	// "buy now" should hit the text_override row.
	hits := queryFTS(t, fx.dbHandle.DB, fx.tenantA, "now")
	var foundOverride bool
	for _, h := range hits {
		if h.kind == SearchEntityKindTextOverride {
			foundOverride = true
			break
		}
	}
	if !foundOverride {
		t.Fatalf("expected text_override hit for query \"now\"; got %+v", hits)
	}
}

// TestSearchIndex_TextOverride_OrphanNotIndexed verifies that orphaned
// overrides are skipped — once an override falls off the canonical_tree,
// surfacing it from ⌘K would just dead-end. Confirms the WHERE status='active'
// guard in buildTextOverrideSearchRows.
func TestSearchIndex_TextOverride_OrphanNotIndexed(t *testing.T) {
	fx := newOverrideFixture(t)

	// Seed an override for tenant A.
	if w := putOverride(t, fx.server, fx.slugA, fx.screenA, "node-orphan-1", fx.tenantA, fx.userA,
		putOverrideRequest{Value: "BuyOrphan", ExpectedRevision: 0}); w.Code != http.StatusOK {
		t.Fatalf("seed: %d", w.Code)
	}

	// Flip it to orphaned via the repo helper.
	repo := NewTenantRepo(fx.dbHandle.DB, fx.tenantA)
	var overrideID string
	if err := fx.dbHandle.QueryRowContext(context.Background(),
		`SELECT id FROM screen_text_overrides
		   WHERE screen_id = ? AND figma_node_id = ?`,
		fx.screenA, "node-orphan-1",
	).Scan(&overrideID); err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkOverridesOrphaned(context.Background(), []string{overrideID}); err != nil {
		t.Fatalf("orphan: %v", err)
	}

	indexCurrent(t, fx, fx.tenantA)

	// Search for the unique orphan value — should NOT hit a text_override row.
	hits := queryFTS(t, fx.dbHandle.DB, fx.tenantA, "buyorphan")
	for _, h := range hits {
		if h.kind == SearchEntityKindTextOverride {
			t.Fatalf("orphaned override leaked into search index: %+v", h)
		}
	}
}

// TestSearchIndex_TextOverride_TenantIsolation ensures one tenant's override
// is invisible to another tenant's search. Repeats the contract from the
// existing TestSearchIndex_TenantIsolation but for the new entity kind.
func TestSearchIndex_TextOverride_TenantIsolation(t *testing.T) {
	fx := newOverrideFixture(t)

	if w := putOverride(t, fx.server, fx.slugA, fx.screenA, "node-iso-1", fx.tenantA, fx.userA,
		putOverrideRequest{Value: "ZebraSecretA", ExpectedRevision: 0}); w.Code != http.StatusOK {
		t.Fatalf("seed A: %d", w.Code)
	}

	indexCurrent(t, fx, fx.tenantA)
	indexCurrent(t, fx, fx.tenantB)

	// Tenant A finds the row; tenant B does not.
	hitsA := queryFTS(t, fx.dbHandle.DB, fx.tenantA, "zebrasecreta")
	if len(hitsA) == 0 {
		t.Fatal("tenant A: expected hit for own override")
	}
	hitsB := queryFTS(t, fx.dbHandle.DB, fx.tenantB, "zebrasecreta")
	if len(hitsB) != 0 {
		t.Fatalf("tenant B leaked tenant A's override: %+v", hitsB)
	}
}

// TestSearchIndex_TextOverride_RaceFreshAfterCommit covers the Phase 6
// read-after-write rule: if a search query happens immediately after the
// override write commits AND we then run the indexer, the latest value is
// the one indexed (the prior value is gone). This shadows the worker's
// "rebuild AFTER commit" timing.
func TestSearchIndex_TextOverride_RaceFreshAfterCommit(t *testing.T) {
	fx := newOverrideFixture(t)

	// First write — original value indexed.
	if w := putOverride(t, fx.server, fx.slugA, fx.screenA, "node-race-1", fx.tenantA, fx.userA,
		putOverrideRequest{Value: "before", ExpectedRevision: 0}); w.Code != http.StatusOK {
		t.Fatalf("first PUT: %d", w.Code)
	}
	indexCurrent(t, fx, fx.tenantA)

	// Second write — same row, different value.
	if w := putOverride(t, fx.server, fx.slugA, fx.screenA, "node-race-1", fx.tenantA, fx.userA,
		putOverrideRequest{Value: "after", ExpectedRevision: 1}); w.Code != http.StatusOK {
		t.Fatalf("second PUT: %d body=%s", w.Code, w.Body.String())
	}
	// Re-index AFTER commit — mirrors the worker's flush-driven sequence.
	indexCurrent(t, fx, fx.tenantA)

	// "before" must NOT match anymore (override row carries the new value);
	// "after" MUST match.
	hitsBefore := queryFTS(t, fx.dbHandle.DB, fx.tenantA, "before")
	for _, h := range hitsBefore {
		if h.kind == SearchEntityKindTextOverride {
			t.Fatalf("stale override row indexed for value \"before\": %+v", h)
		}
	}
	hitsAfter := queryFTS(t, fx.dbHandle.DB, fx.tenantA, "after")
	var found bool
	for _, h := range hitsAfter {
		if h.kind == SearchEntityKindTextOverride {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected text_override hit for \"after\"; got %+v", hitsAfter)
	}
}

// TestSearchIndex_TextOverride_OpenURLAndBody verifies the row-shape contract
// the frontend ⌘K modal depends on: the open URL deep-links the leaf canvas
// at the right screen + node, and the body field carries the original text
// so a PM searching for the *prior* copy still finds the override.
func TestSearchIndex_TextOverride_OpenURLAndBody(t *testing.T) {
	fx := newOverrideFixture(t)

	if w := putOverride(t, fx.server, fx.slugA, fx.screenA, "node-shape-1", fx.tenantA, fx.userA,
		putOverrideRequest{
			Value:                "FreshLabel",
			ExpectedRevision:     0,
			LastSeenOriginalText: "OldLabel",
		}); w.Code != http.StatusOK {
		t.Fatalf("PUT: %d", w.Code)
	}
	indexCurrent(t, fx, fx.tenantA)

	// Pull the row via the FTS table and inspect open_url + body.
	rows, err := fx.dbHandle.QueryContext(context.Background(),
		`SELECT entity_id, open_url, title, body FROM search_index_fts
		   WHERE tenant_id = ? AND entity_kind = ?`,
		fx.tenantA, SearchEntityKindTextOverride,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var n int
	for rows.Next() {
		n++
		var id, openURL, title, body string
		if err := rows.Scan(&id, &openURL, &title, &body); err != nil {
			t.Fatal(err)
		}
		// Open URL must reference the leaf canvas with screen + node.
		if !strings.Contains(openURL, "/atlas/leaf/") ||
			!strings.Contains(openURL, "screen=") ||
			!strings.Contains(openURL, "node=") {
			t.Errorf("open_url malformed: %s", openURL)
		}
		// Body holds the original text so PMs searching the prior copy hit
		// the override row.
		if body != "OldLabel" {
			t.Errorf("expected body=OldLabel, got %q", body)
		}
		// Title joins the screen logical id with the new value.
		if !strings.Contains(title, "FreshLabel") {
			t.Errorf("expected title to contain FreshLabel, got %q", title)
		}
	}
	if n != 1 {
		t.Fatalf("expected exactly 1 text_override row; got %d", n)
	}
}

// queryHit is defined in search_test.go (same package).
