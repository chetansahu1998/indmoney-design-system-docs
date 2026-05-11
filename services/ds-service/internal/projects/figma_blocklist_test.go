package projects

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

// figma_blocklist_test.go — verify the persistent-failure skip list
// (2026-05-12). Three behaviors to pin:
//
//   1. Threshold logic — 1 or 2 failures don't suppress (active=false).
//      3rd failure flips to active=true with cooldown_until = now+24h.
//   2. Reset logic — a failure with last_failure_at older than 1h AND
//      below the threshold resets consecutive_failures to 1. Above the
//      threshold, the counter accumulates regardless.
//   3. Clear paths — explicit ClearFigmaRenderFailure removes the row;
//      ClearFigmaRenderFailuresForHashChange removes rows whose
//      stored clear_hash differs from the current hash.

func TestFigmaBlocklist_BelowThresholdNotActive(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	// 2 failures (one short of the threshold)
	for i := 0; i < BlocklistFailureThreshold-1; i++ {
		entry, err := repo.MarkFigmaRenderFailure(ctx, "f1", "n1", "figma 425", "hash-1")
		if err != nil {
			t.Fatalf("mark[%d]: %v", i, err)
		}
		if entry != nil {
			t.Fatalf("threshold not yet crossed; expected nil entry on mark[%d], got %+v", i, entry)
		}
	}
	// IsFigmaRenderBlocked should return found but active=false
	entry, active, err := repo.IsFigmaRenderBlocked(ctx, "f1", "n1")
	if err != nil {
		t.Fatalf("isblocked: %v", err)
	}
	if entry == nil {
		t.Fatal("expected entry row to exist even below threshold")
	}
	if active {
		t.Fatal("expected active=false below threshold")
	}
	if entry.ConsecutiveFailures != BlocklistFailureThreshold-1 {
		t.Errorf("ConsecutiveFailures=%d, want %d", entry.ConsecutiveFailures, BlocklistFailureThreshold-1)
	}
}

func TestFigmaBlocklist_AtThresholdFlipsActive(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	// Hit the threshold exactly.
	var finalEntry *FigmaRenderBlockEntry
	var err error
	for i := 0; i < BlocklistFailureThreshold; i++ {
		finalEntry, err = repo.MarkFigmaRenderFailure(ctx, "f1", "n1", "figma 425", "hash-1")
		if err != nil {
			t.Fatalf("mark[%d]: %v", i, err)
		}
	}
	if finalEntry == nil {
		t.Fatal("expected non-nil entry at threshold crossing")
	}
	if finalEntry.ConsecutiveFailures != BlocklistFailureThreshold {
		t.Errorf("ConsecutiveFailures=%d, want %d", finalEntry.ConsecutiveFailures, BlocklistFailureThreshold)
	}
	if !finalEntry.CooldownUntil.After(time.Now()) {
		t.Errorf("CooldownUntil=%v not in the future", finalEntry.CooldownUntil)
	}

	_, active, err := repo.IsFigmaRenderBlocked(ctx, "f1", "n1")
	if err != nil {
		t.Fatalf("isblocked: %v", err)
	}
	if !active {
		t.Fatal("expected active=true at threshold")
	}
}

func TestFigmaBlocklist_ClearOnSuccess(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	// Pre-populate to the threshold.
	for i := 0; i < BlocklistFailureThreshold; i++ {
		if _, err := repo.MarkFigmaRenderFailure(ctx, "f1", "n1", "x", "hash-1"); err != nil {
			t.Fatalf("mark: %v", err)
		}
	}
	if _, active, _ := repo.IsFigmaRenderBlocked(ctx, "f1", "n1"); !active {
		t.Fatal("setup: expected active")
	}

	// A success clears the row.
	if err := repo.ClearFigmaRenderFailure(ctx, "f1", "n1"); err != nil {
		t.Fatalf("clear: %v", err)
	}
	entry, active, err := repo.IsFigmaRenderBlocked(ctx, "f1", "n1")
	if err != nil {
		t.Fatalf("isblocked after clear: %v", err)
	}
	if entry != nil || active {
		t.Errorf("expected row cleared, got entry=%+v active=%v", entry, active)
	}
}

func TestFigmaBlocklist_HashChangeInvalidates(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	// Pre-populate to threshold with clear_hash = "old".
	for i := 0; i < BlocklistFailureThreshold; i++ {
		if _, err := repo.MarkFigmaRenderFailure(ctx, "f1", "n1", "x", "old-hash"); err != nil {
			t.Fatalf("mark: %v", err)
		}
	}
	// Designer edits the file — new canonical_tree hash arrives.
	cleared, err := repo.ClearFigmaRenderFailuresForHashChange(ctx, "f1", "new-hash")
	if err != nil {
		t.Fatalf("hash clear: %v", err)
	}
	if cleared != 1 {
		t.Errorf("expected 1 row cleared, got %d", cleared)
	}
	_, active, _ := repo.IsFigmaRenderBlocked(ctx, "f1", "n1")
	if active {
		t.Fatal("expected entry cleared after hash change")
	}
}

func TestFigmaBlocklist_HashUnchanged_NoClear(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	// Pre-populate with clear_hash = "same".
	for i := 0; i < BlocklistFailureThreshold; i++ {
		if _, err := repo.MarkFigmaRenderFailure(ctx, "f1", "n1", "x", "same-hash"); err != nil {
			t.Fatalf("mark: %v", err)
		}
	}
	// Re-sync with the SAME hash — no clear.
	cleared, err := repo.ClearFigmaRenderFailuresForHashChange(ctx, "f1", "same-hash")
	if err != nil {
		t.Fatalf("hash clear: %v", err)
	}
	if cleared != 0 {
		t.Errorf("expected 0 rows cleared for matching hash, got %d", cleared)
	}
	if _, active, _ := repo.IsFigmaRenderBlocked(ctx, "f1", "n1"); !active {
		t.Fatal("expected entry to still be active")
	}
}

func TestFigmaBlocklist_ListReturnsAllRowsDescByLastFailure(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	// Two failed (file, node) pairs, separate mark cycles for clear timestamps.
	for i := 0; i < BlocklistFailureThreshold; i++ {
		if _, err := repo.MarkFigmaRenderFailure(ctx, "f1", "n1", "x", "h1"); err != nil {
			t.Fatalf("mark n1: %v", err)
		}
	}
	time.Sleep(10 * time.Millisecond)
	for i := 0; i < BlocklistFailureThreshold; i++ {
		if _, err := repo.MarkFigmaRenderFailure(ctx, "f1", "n2", "y", "h2"); err != nil {
			t.Fatalf("mark n2: %v", err)
		}
	}

	rows, err := repo.ListFigmaRenderBlocklist(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	// n2 was marked second → its last_failure_at is later → comes first in DESC order.
	if rows[0].NodeID != "n2" {
		t.Errorf("expected first row n2, got %s", rows[0].NodeID)
	}
}

func TestFigmaBlocklist_TenantScoped(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	tB := mintSecondTenantForTest(t, d.DB)
	repoA := NewTenantRepo(d.DB, tA)
	repoB := NewTenantRepo(d.DB, tB)
	ctx := context.Background()
	_ = sql.ErrNoRows // keep the sql import referenced via type below

	// Mark in tenant A only.
	for i := 0; i < BlocklistFailureThreshold; i++ {
		if _, err := repoA.MarkFigmaRenderFailure(ctx, "f1", "n1", "x", "h"); err != nil {
			t.Fatalf("mark A: %v", err)
		}
	}
	// Tenant B should NOT see the row.
	if _, active, _ := repoB.IsFigmaRenderBlocked(ctx, "f1", "n1"); active {
		t.Fatal("tenant B should not see tenant A's blocklist row")
	}
	listB, _ := repoB.ListFigmaRenderBlocklist(ctx)
	if len(listB) != 0 {
		t.Errorf("tenant B list: got %d rows, want 0", len(listB))
	}
}

func TestFigmaBlocklist_EmptyArgsAreSafe(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	// Empty fileID or nodeID — should no-op without DB writes.
	if _, _, err := repo.IsFigmaRenderBlocked(ctx, "", "n1"); err != nil {
		t.Errorf("IsBlocked(empty file): %v", err)
	}
	if _, err := repo.MarkFigmaRenderFailure(ctx, "", "n1", "x", "h"); err != nil {
		t.Errorf("Mark(empty file): %v", err)
	}
	if err := repo.ClearFigmaRenderFailure(ctx, "f", ""); err != nil {
		t.Errorf("Clear(empty node): %v", err)
	}
	list, err := repo.ListFigmaRenderBlocklist(ctx)
	if err != nil {
		t.Errorf("List: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("empty args should not populate blocklist; got %d rows", len(list))
	}
}

// mintSecondTenantForTest is a local helper — most tests use newTestDB's
// single tenant; we need a second one for the cross-tenant isolation test.
func mintSecondTenantForTest(t *testing.T, db *sql.DB) string {
	t.Helper()
	id := "tenant-blocklist-secondary"
	_, err := db.Exec(
		`INSERT OR IGNORE INTO tenants (id, slug, name, created_at)
		 VALUES (?, ?, ?, datetime('now'))`,
		id, id, "Blocklist Secondary",
	)
	if err != nil {
		t.Fatalf("seed second tenant: %v", err)
	}
	return id
}
