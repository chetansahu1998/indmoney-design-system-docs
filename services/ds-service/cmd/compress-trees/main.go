// Command compress-trees backfills screen_canonical_trees.canonical_tree_gz
// for every row whose legacy canonical_tree is still populated. Each row's
// existing TEXT JSON is gzipped into the new BLOB column and the legacy
// column is reset to '' in the same UPDATE so the read-side
// projects.ResolveCanonicalTree helper picks the gzipped representation
// transparently.
//
// Plan 2026-05-03-001 / T8 — pairs with migration 0016_canonical_tree_gz.
//
// Usage on Fly:
//
//	fly ssh console -C "/usr/local/bin/compress-trees"
//	fly ssh console -C "/usr/local/bin/compress-trees --dry-run"
//	fly ssh console -C "/usr/local/bin/compress-trees --vacuum"
//
// Local dev:
//
//	cd services/ds-service && go run ./cmd/compress-trees --dry-run
//
// The command is idempotent: rows already migrated (legacy='' and gz NOT
// NULL) are filtered out by the WHERE clause. Re-running adds zero work
// once the backfill is complete.

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
	dryRun := flag.Bool("dry-run", false, "report row count and uncompressed bytes without writing")
	batch := flag.Int("batch", 200, "screens compressed per transaction; tune down on tiny VMs")
	vacuum := flag.Bool("vacuum", false, "run VACUUM after the backfill to reclaim disk")
	flag.Parse()

	if *batch < 1 {
		fmt.Fprintln(os.Stderr, "--batch must be >= 1")
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

	var pendingCount int
	var pendingBytes int64
	err = conn.DB.QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(SUM(LENGTH(canonical_tree)), 0)
		   FROM screen_canonical_trees
		  WHERE canonical_tree <> '' AND canonical_tree_gz IS NULL`,
	).Scan(&pendingCount, &pendingBytes)
	if err != nil {
		fmt.Fprintf(os.Stderr, "count pending: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("pending rows=%d uncompressed_bytes=%d\n", pendingCount, pendingBytes)
	if pendingCount == 0 {
		fmt.Println("nothing to do")
		return
	}
	if *dryRun {
		return
	}

	processed, written, errs := 0, int64(0), 0
	for {
		ids, legacies, err := loadBatch(ctx, conn, *batch)
		if err != nil {
			fmt.Fprintf(os.Stderr, "load batch: %v\n", err)
			os.Exit(1)
		}
		if len(ids) == 0 {
			break
		}

		// One tx per batch — bounded WAL footprint on Fly's small volume.
		tx, err := conn.DB.BeginTx(ctx, nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "begin: %v\n", err)
			os.Exit(1)
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
			fmt.Fprintf(os.Stderr, "commit: %v\n", err)
			os.Exit(1)
		}
	NextBatch:
		fmt.Printf("progress processed=%d errors=%d written_gz_bytes=%d\n", processed, errs, written)
	}

	if *vacuum {
		fmt.Println("vacuuming…")
		if _, err := conn.DB.ExecContext(ctx, `VACUUM`); err != nil {
			fmt.Fprintf(os.Stderr, "vacuum: %v\n", err)
			os.Exit(1)
		}
	}

	fmt.Printf("ok processed=%d errors=%d written_gz_bytes=%d vacuumed=%v\n",
		processed, errs, written, *vacuum)
}

// loadBatch pulls the next chunk of un-migrated rows. Re-querying each
// iteration (instead of holding one big result set open) lets the per-batch
// commit release WAL frames before the next read.
func loadBatch(ctx context.Context, conn *db.DB, n int) ([]string, []string, error) {
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
