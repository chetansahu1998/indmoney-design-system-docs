package projects

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"
)

// blocklist_sweep_test.go — verify the stale-row GC (2026-05-12).
//
// Three behaviors to pin:
//   1. Fresh rows (last_failure_at within the threshold) survive sweep.
//   2. Stale rows (last_failure_at older than threshold) are deleted.
//   3. Sweep is idempotent — running twice on an empty table returns 0.

func TestSweepStaleBlocklistRows_FreshRowsSurvive(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	// Create a fresh row (just now).
	if _, err := repo.MarkFigmaRenderFailure(ctx, "f1", "n1", "x", "h"); err != nil {
		t.Fatalf("mark: %v", err)
	}

	deleted, err := SweepStaleBlocklistRows(ctx, d.DB, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if deleted != 0 {
		t.Errorf("expected 0 deleted (row is fresh), got %d", deleted)
	}
	// Row should still be present.
	list, _ := repo.ListFigmaRenderBlocklist(ctx)
	if len(list) != 1 {
		t.Errorf("expected 1 row after sweep, got %d", len(list))
	}
}

func TestSweepStaleBlocklistRows_StaleRowsDeleted(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	ctx := context.Background()

	// Direct INSERT with a backdated last_failure_at so we don't have to
	// time-travel the helper. The row's last_failure_at sits 30 days
	// in the past — well beyond the 7-day threshold.
	old := time.Now().UTC().Add(-30 * 24 * time.Hour).Format(time.RFC3339)
	cooldownEnd := time.Now().UTC().Add(-29 * 24 * time.Hour).Format(time.RFC3339)
	if _, err := d.DB.ExecContext(ctx,
		`INSERT INTO figma_render_blocklist (
			tenant_id, file_id, node_id, first_failure_at, last_failure_at,
			consecutive_failures, last_error, cooldown_until, clear_hash
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		tA, "f-old", "n-old", old, old, 3, "stale", cooldownEnd, "h",
	); err != nil {
		t.Fatalf("seed stale row: %v", err)
	}

	deleted, err := SweepStaleBlocklistRows(ctx, d.DB, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if deleted != 1 {
		t.Errorf("expected 1 deleted (stale row), got %d", deleted)
	}
}

func TestSweepStaleBlocklistRows_NilDBReturnsZero(t *testing.T) {
	n, err := SweepStaleBlocklistRows(context.Background(), nil, nil)
	if err != nil {
		t.Errorf("expected no error on nil DB, got %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 deleted with nil DB, got %d", n)
	}
}

func TestSweepStaleBlocklistRows_MixedRows_OnlyStaleDeleted(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	// One fresh row via the normal helper.
	if _, err := repo.MarkFigmaRenderFailure(ctx, "f1", "n-fresh", "fresh", "h"); err != nil {
		t.Fatalf("mark fresh: %v", err)
	}
	// Two stale rows seeded directly.
	old := time.Now().UTC().Add(-30 * 24 * time.Hour).Format(time.RFC3339)
	for _, nid := range []string{"n-stale-1", "n-stale-2"} {
		if _, err := d.DB.ExecContext(ctx,
			`INSERT INTO figma_render_blocklist (
				tenant_id, file_id, node_id, first_failure_at, last_failure_at,
				consecutive_failures, last_error, cooldown_until, clear_hash
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			tA, "f1", nid, old, old, 3, "stale", old, "h",
		); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	deleted, err := SweepStaleBlocklistRows(ctx, d.DB, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if deleted != 2 {
		t.Errorf("expected 2 stale deleted, got %d", deleted)
	}
	list, _ := repo.ListFigmaRenderBlocklist(ctx)
	if len(list) != 1 || list[0].NodeID != "n-fresh" {
		t.Errorf("expected only n-fresh to survive, got %+v", list)
	}
}
