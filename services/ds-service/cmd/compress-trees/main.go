// Command compress-trees backfills the screen_canonical_trees compression
// columns. Two modes:
//
//	--to=gz   (default; T8 era — migration 0016 backfill)
//	   moves canonical_tree (TEXT) → canonical_tree_gz (BLOB),
//	   nulls the legacy column. ResolveCanonicalTree picks the gz blob.
//
//	--to=zstd (Phase 1 — migration 0022 backfill)
//	   moves canonical_tree_gz (BLOB) → canonical_tree_zstd (BLOB),
//	   nulls the gz column. ResolveCanonicalTree picks the zstd blob.
//	   Measured on the 5,647-tree corpus: 765.8 MB total vs 1.17 GB
//	   gzip — 432.9 MB / 36.1% saved with 3.2× faster decompress.
//
// Both modes share the same batched-tx + per-row idempotent UPDATE
// shape so re-running after a partial failure picks up where it
// stopped. Rows already migrated for the chosen target are filtered
// out by the WHERE clause; --dry-run reports the count without
// writing.
//
// Usage on Fly:
//
//	fly ssh console -C "/usr/local/bin/compress-trees --to=zstd"
//	fly ssh console -C "/usr/local/bin/compress-trees --to=zstd --dry-run"
//	fly ssh console -C "/usr/local/bin/compress-trees --to=zstd --vacuum"
//
// Local dev:
//
//	cd services/ds-service && go run ./cmd/compress-trees --to=zstd --dry-run

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/db"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/projects"
)

func main() {
	target := flag.String("to", "gz", "target compression: gz|zstd")
	dryRun := flag.Bool("dry-run", false, "report row count and uncompressed bytes without writing")
	batch := flag.Int("batch", 200, "screens compressed per transaction; tune down on tiny VMs")
	vacuum := flag.Bool("vacuum", false, "run VACUUM after the backfill to reclaim disk")
	flag.Parse()

	if *batch < 1 {
		fmt.Fprintln(os.Stderr, "--batch must be >= 1")
		os.Exit(2)
	}
	if *target != "gz" && *target != "zstd" {
		fmt.Fprintf(os.Stderr, "--to must be gz or zstd, got %q\n", *target)
		os.Exit(2)
	}

	dbPath := os.Getenv("SQLITE_PATH")
	if dbPath == "" {
		dbPath = filepath.Join("services", "ds-service", "data", "ds.db")
	}
	conn, err := db.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open db %s: %v\n", dbPath, err)
		os.Exit(1)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	switch *target {
	case "gz":
		runBackfillToGz(ctx, conn, *batch, *dryRun, *vacuum)
	case "zstd":
		runBackfillToZstd(ctx, conn, *batch, *dryRun, *vacuum)
	}
}

// ─── --to=gz (T8 backfill, original behavior) ───────────────────────────────

func runBackfillToGz(ctx context.Context, conn *db.DB, batch int, dryRun, vacuum bool) {
	var pendingCount int
	var pendingBytes int64
	if err := conn.DB.QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(SUM(LENGTH(canonical_tree)), 0)
		   FROM screen_canonical_trees
		  WHERE canonical_tree <> '' AND canonical_tree_gz IS NULL`,
	).Scan(&pendingCount, &pendingBytes); err != nil {
		fatalf("count pending: %v", err)
	}
	fmt.Printf("[to=gz] pending rows=%d uncompressed_bytes=%d\n", pendingCount, pendingBytes)
	if pendingCount == 0 {
		fmt.Println("nothing to do")
		return
	}
	if dryRun {
		return
	}

	processed, written, errs := 0, int64(0), 0
	for {
		ids, legacies, err := loadGzBatch(ctx, conn, batch)
		if err != nil {
			fatalf("load batch: %v", err)
		}
		if len(ids) == 0 {
			break
		}
		tx, err := conn.DB.BeginTx(ctx, nil)
		if err != nil {
			fatalf("begin: %v", err)
		}
		for i, screenID := range ids {
			gz, err := projects.CompressTree(legacies[i])
			if err != nil {
				_ = tx.Rollback()
				fmt.Fprintf(os.Stderr, "compress %s: %v\n", screenID, err)
				errs++
				goto NextBatch
			}
			res, err := tx.ExecContext(ctx,
				`UPDATE screen_canonical_trees
				    SET canonical_tree    = '',
				        canonical_tree_gz = ?
				  WHERE screen_id = ?
				    AND canonical_tree_gz IS NULL`,
				gz, screenID)
			if err != nil {
				_ = tx.Rollback()
				fmt.Fprintf(os.Stderr, "update %s: %v\n", screenID, err)
				errs++
				goto NextBatch
			}
			if rows, _ := res.RowsAffected(); rows == 1 {
				processed++
				written += int64(len(gz))
			}
		}
		if err := tx.Commit(); err != nil {
			fatalf("commit: %v", err)
		}
	NextBatch:
		fmt.Printf("[to=gz] progress processed=%d errors=%d written_gz_bytes=%d\n",
			processed, errs, written)
	}
	if vacuum {
		runVacuum(ctx, conn)
	}
	fmt.Printf("[to=gz] ok processed=%d errors=%d written_gz_bytes=%d vacuumed=%v\n",
		processed, errs, written, vacuum)
}

func loadGzBatch(ctx context.Context, conn *db.DB, n int) ([]string, []string, error) {
	rows, err := conn.DB.QueryContext(ctx,
		`SELECT screen_id, canonical_tree
		   FROM screen_canonical_trees
		  WHERE canonical_tree <> '' AND canonical_tree_gz IS NULL
		  LIMIT ?`, n)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var ids, legacies []string
	for rows.Next() {
		var id, legacy string
		if err := rows.Scan(&id, &legacy); err != nil {
			return nil, nil, err
		}
		ids = append(ids, id)
		legacies = append(legacies, legacy)
	}
	return ids, legacies, rows.Err()
}

// ─── --to=zstd (Phase 1 backfill) ───────────────────────────────────────────

func runBackfillToZstd(ctx context.Context, conn *db.DB, batch int, dryRun, vacuum bool) {
	// Pending = rows with gz populated but no zstd yet.
	// Pre-T8 rows (legacy still set, gz NULL) are also picked up via the
	// LegacyOR clause so a stale unmigrated row gets the full legacy → zstd
	// jump in one pass without needing to run --to=gz first.
	var pendingCount int
	var pendingGzBytes int64
	if err := conn.DB.QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(SUM(LENGTH(canonical_tree_gz)), 0)
		   FROM screen_canonical_trees
		  WHERE canonical_tree_zstd IS NULL
		    AND (canonical_tree_gz IS NOT NULL OR canonical_tree <> '')`,
	).Scan(&pendingCount, &pendingGzBytes); err != nil {
		fatalf("count pending: %v", err)
	}
	fmt.Printf("[to=zstd] pending rows=%d gz_bytes=%d\n", pendingCount, pendingGzBytes)
	if pendingCount == 0 {
		fmt.Println("nothing to do")
		return
	}
	if dryRun {
		return
	}

	// skipped is the per-run exclusion list. A row that fails decompress
	// (e.g., a >64 MB tree that trips the safety cap) gets added here so
	// the next loadZstdBatch query excludes it — without this guard, the
	// failing row repeatedly resurfaces, gets re-attempted, and the loop
	// runs forever instead of finishing the rest of the corpus. The row
	// stays in gz form; ResolveCanonicalTree's gz fallback continues to
	// serve it correctly at read time. A follow-up audit can decide
	// whether to manually re-encode (raise the cap explicitly) or drop
	// the offending screen entirely.
	skipped := map[string]struct{}{}
	processed, writtenZstd, savedBytes, errs := 0, int64(0), int64(0), 0
	for {
		ids, gzs, legacies, err := loadZstdBatch(ctx, conn, batch, skipped)
		if err != nil {
			fatalf("load batch: %v", err)
		}
		if len(ids) == 0 {
			break
		}
		tx, err := conn.DB.BeginTx(ctx, nil)
		if err != nil {
			fatalf("begin: %v", err)
		}
		batchProcessed, batchWritten, batchSaved := 0, int64(0), int64(0)
		for i, screenID := range ids {
			// Source-of-truth tree: prefer gz (decompress) over legacy text.
			// Mirrors ResolveCanonicalTree's gz>legacy priority for rows that
			// haven't been migrated to gz yet.
			tree, derr := projects.ResolveCanonicalTree(legacies[i], gzs[i], nil)
			if derr != nil {
				fmt.Fprintf(os.Stderr, "skip %s (decompress): %v\n", screenID, derr)
				errs++
				skipped[screenID] = struct{}{}
				continue
			}
			zstdBlob, err := projects.CompressTreeZstd(tree)
			if err != nil {
				fmt.Fprintf(os.Stderr, "skip %s (compress zstd): %v\n", screenID, err)
				errs++
				skipped[screenID] = struct{}{}
				continue
			}
			res, err := tx.ExecContext(ctx,
				`UPDATE screen_canonical_trees
				    SET canonical_tree      = '',
				        canonical_tree_gz   = NULL,
				        canonical_tree_zstd = ?
				  WHERE screen_id = ?
				    AND canonical_tree_zstd IS NULL`,
				zstdBlob, screenID)
			if err != nil {
				// UPDATE-level error inside an active SQLite tx leaves the
				// tx in an aborted state on most error classes; the safe
				// path is to roll back the whole batch and let the next
				// iteration retry the rows we did NOT yet skip. The
				// failed screenID joins the skip set so it doesn't loop.
				_ = tx.Rollback()
				fmt.Fprintf(os.Stderr, "skip %s (update): %v\n", screenID, err)
				errs++
				skipped[screenID] = struct{}{}
				goto AfterRollback
			}
			if rows, _ := res.RowsAffected(); rows == 1 {
				batchProcessed++
				batchWritten += int64(len(zstdBlob))
				batchSaved += int64(len(gzs[i])) - int64(len(zstdBlob))
			}
		}
		if err := tx.Commit(); err != nil {
			fatalf("commit: %v", err)
		}
		processed += batchProcessed
		writtenZstd += batchWritten
		savedBytes += batchSaved
	AfterRollback:
		fmt.Printf("[to=zstd] progress processed=%d errors=%d skipped=%d zstd_bytes=%d saved_vs_gz=%d\n",
			processed, errs, len(skipped), writtenZstd, savedBytes)
	}
	if vacuum {
		runVacuum(ctx, conn)
	}
	fmt.Printf("[to=zstd] ok processed=%d errors=%d zstd_bytes=%d saved_vs_gz=%d vacuumed=%v\n",
		processed, errs, writtenZstd, savedBytes, vacuum)
}

func loadZstdBatch(ctx context.Context, conn *db.DB, n int, skipped map[string]struct{}) ([]string, [][]byte, []string, error) {
	// Pull more than `n` raw candidates so post-filtering against the skip
	// set still yields a full batch. The skip set typically holds <10
	// entries (rare oversize blobs), so 2× headroom is plenty.
	probe := n
	if len(skipped) > 0 {
		probe = n + len(skipped) + 16
	}
	rows, err := conn.DB.QueryContext(ctx,
		`SELECT screen_id, canonical_tree_gz, COALESCE(canonical_tree, '')
		   FROM screen_canonical_trees
		  WHERE canonical_tree_zstd IS NULL
		    AND (canonical_tree_gz IS NOT NULL OR canonical_tree <> '')
		  LIMIT ?`, probe)
	if err != nil {
		return nil, nil, nil, err
	}
	defer rows.Close()
	var ids, legacies []string
	var gzs [][]byte
	for rows.Next() {
		var id, legacy string
		var gz []byte
		if err := rows.Scan(&id, &gz, &legacy); err != nil {
			return nil, nil, nil, err
		}
		if _, skip := skipped[id]; skip {
			continue
		}
		ids = append(ids, id)
		gzs = append(gzs, gz)
		legacies = append(legacies, legacy)
		if len(ids) >= n {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, nil, err
	}
	// If post-filter returned zero rows AND the un-filtered query
	// returned ANY rows (i.e., the only candidates are all skipped),
	// signal "done" so the caller's len==0 break condition fires.
	// Without this short-circuit the loop would request the next batch
	// at LIMIT and re-pull the same skipped rows endlessly.
	return ids, gzs, legacies, nil
}

// ─── shared helpers ─────────────────────────────────────────────────────────

func runVacuum(ctx context.Context, conn *db.DB) {
	fmt.Println("vacuuming…")
	if _, err := conn.DB.ExecContext(ctx, `VACUUM`); err != nil {
		fatalf("vacuum: %v", err)
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
