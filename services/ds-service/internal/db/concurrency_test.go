// Concurrency tests for the read/write pool split (plan
// 2026-05-16-001-fix-sqlite-pool-split-plan.md, U1 + U6).
//
// These tests assert the parallelism win the pool split is supposed to
// deliver: a long-running write transaction must not block concurrent
// reads. On the legacy single-conn pool (SetMaxOpenConns(1)), reads
// queue behind the writer — the test will time out or block. After the
// split (write pool MaxOpenConns=1, read pool mode=ro + MaxOpenConns=8),
// reads from d.Read() complete while the writer holds its tx.
//
// Once the split lands, the read calls migrate from the legacy
// embed (d.QueryContext) to the explicit accessor (d.Read().QueryContext).

package db

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestConcurrentRead_WhileWriteTxHeld is the canonical parallelism test.
//
// Pre-split: this test BLOCKS because reads queue on the single conn the
// write tx is holding. After the budget timeout, t.Fatal fires.
//
// Post-split: reads complete in <100ms each because they come from a
// separate connection pool (read pool, mode=ro, MaxOpenConns=8). The
// 2-second write tx and the 5 concurrent reads finish in well under the
// budget.
func TestConcurrentRead_WhileWriteTxHeld(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	seedTenantUser(t, d, ctx)

	// Start a write tx in a goroutine. Hold it for 2 seconds.
	writerStarted := make(chan struct{})
	writerDone := make(chan struct{})
	writeHold := 2 * time.Second
	go func() {
		defer close(writerDone)
		// Use d.Write() once the split lands; until then this uses
		// the embedded *sql.DB which IS the single-conn pool.
		tx, err := d.Write().BeginTx(ctx, nil)
		if err != nil {
			t.Errorf("begin write tx: %v", err)
			return
		}
		defer tx.Rollback() //nolint:errcheck — test scope
		// Do a real write so the tx actually acquires the write lock,
		// then signal readers can start.
		if _, err := tx.ExecContext(ctx,
			`UPDATE users SET last_login_at = ? WHERE id = 'user-1'`,
			time.Now().UTC().Format(time.RFC3339),
		); err != nil {
			t.Errorf("write inside tx: %v", err)
			return
		}
		close(writerStarted)
		time.Sleep(writeHold)
	}()

	// Wait for the writer to acquire the write lock before kicking off readers.
	<-writerStarted

	// Issue 5 concurrent reads. Each should return within ~100ms because
	// they come from the read pool (or fail on the legacy single-conn pool).
	// Budget the whole batch generously — 1 second — so a real failure
	// surfaces as a timeout, not a stuck test.
	const readerCount = 5
	readBudget := 1 * time.Second

	type readerResult struct {
		idx     int
		elapsed time.Duration
		err     error
	}
	results := make(chan readerResult, readerCount)
	var wg sync.WaitGroup
	for i := 0; i < readerCount; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			readCtx, cancel := context.WithTimeout(ctx, readBudget)
			defer cancel()
			start := time.Now()
			var n int
			err := d.Read().QueryRowContext(readCtx,
				`SELECT COUNT(*) FROM users WHERE id = ?`, "user-1",
			).Scan(&n)
			results <- readerResult{idx: idx, elapsed: time.Since(start), err: err}
		}(i)
	}

	wg.Wait()
	close(results)

	// Wait for the writer to finish so the test cleans up properly.
	<-writerDone

	for r := range results {
		if r.err != nil {
			t.Errorf("reader %d: %v (elapsed %s)", r.idx, r.err, r.elapsed)
			continue
		}
		// Tight bound: each read should complete much faster than the
		// write tx hold duration. 500ms leaves headroom for CI slowness
		// while still asserting the read isn't blocked by the writer.
		if r.elapsed > 500*time.Millisecond {
			t.Errorf("reader %d: took %s (write tx held for %s) — reads are blocked on the writer",
				r.idx, r.elapsed, writeHold)
		}
	}
}

// TestWritesSerialized asserts that MaxOpenConns=1 on the write pool
// causes concurrent writes to serialize. Critical for preserving the
// single-writer invariant the codebase depends on (autosync idempotency,
// sync orchestrator's no-per-tenant-lock posture, worker lease semantics).
//
// Five goroutines each open BeginTx + do an INSERT. Total elapsed time
// must be at least 5 * (per-write floor) — proving writes serialize.
// If a future change bumps MaxOpenConns above 1 on the write pool,
// this test fires.
func TestWritesSerialized(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	seedTenantUser(t, d, ctx)

	const writerCount = 5
	// Each writer holds the tx open for this long. With MaxOpenConns=1
	// the total time should be at least writerCount * perWrite.
	perWrite := 100 * time.Millisecond

	start := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < writerCount; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			tx, err := d.Write().BeginTx(ctx, nil)
			if err != nil {
				t.Errorf("writer %d begin: %v", idx, err)
				return
			}
			if _, err := tx.ExecContext(ctx,
				`UPDATE users SET last_login_at = ? WHERE id = 'user-1'`,
				time.Now().UTC().Format(time.RFC3339),
			); err != nil {
				_ = tx.Rollback()
				t.Errorf("writer %d update: %v", idx, err)
				return
			}
			time.Sleep(perWrite)
			if err := tx.Commit(); err != nil {
				t.Errorf("writer %d commit: %v", idx, err)
			}
		}(i)
	}
	wg.Wait()
	elapsed := time.Since(start)

	expectedMin := time.Duration(writerCount) * perWrite
	if elapsed < expectedMin {
		t.Errorf("writes did not serialize: %d writers × %s each took %s (expected >= %s)",
			writerCount, perWrite, elapsed, expectedMin)
	}
}

// TestRead_RejectsWrites asserts that the read pool refuses writes.
// mode=ro on the read DSN provides a runtime safety net — an accidental
// pool.Read().ExecContext("INSERT …") fails loudly rather than silently
// landing on the wrong handle.
func TestRead_RejectsWrites(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	seedTenantUser(t, d, ctx)

	_, err := d.Read().ExecContext(ctx,
		`UPDATE users SET last_login_at = ? WHERE id = 'user-1'`,
		time.Now().UTC().Format(time.RFC3339),
	)
	if err == nil {
		t.Fatal("expected read pool to reject writes; got nil error")
	}
	// modernc.org/sqlite returns "attempt to write a readonly database"
	// or similar. We don't pin the exact text; any non-nil error is the
	// contract.
}

// TestForeignKeys_OnBothPools asserts the FK pragma is honoured on the
// read pool as well as the write pool. The DSN sets foreign_keys(1);
// both pools must preserve it so FK constraints in queries (e.g.,
// JOINs through projects → versions → screens) behave consistently.
func TestForeignKeys_OnBothPools(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	var wv, rv int
	if err := d.Write().QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&wv); err != nil {
		t.Fatalf("write pool PRAGMA foreign_keys: %v", err)
	}
	if wv != 1 {
		t.Errorf("write pool foreign_keys=%d, want 1", wv)
	}
	if err := d.Read().QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&rv); err != nil {
		t.Fatalf("read pool PRAGMA foreign_keys: %v", err)
	}
	if rv != 1 {
		t.Errorf("read pool foreign_keys=%d, want 1", rv)
	}
}
