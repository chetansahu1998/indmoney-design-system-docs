// Command cleanup-versions iterates every project across every tenant and
// prunes the on-disk PNG cache for versions older than the retention
// budget. Designed for two situations:
//
//   - Initial backfill after T6 lands. Existing projects accumulated one
//     directory per export with no cleanup; running the command once
//     reclaims everything older than the retention budget.
//   - Manual recovery when the in-process retention sweep has failed
//     repeatedly (transient I/O errors), since the sweeper logs and
//     continues but doesn't loudly fail.
//
// Usage on Fly:
//
//	fly ssh console -C "/usr/local/bin/cleanup-versions --retain 3"
//	fly ssh console -C "/usr/local/bin/cleanup-versions --retain 5 --dry-run"
//
// Local dev:
//
//	cd services/ds-service && go run ./cmd/cleanup-versions --retain 3
//
// Plan 2026-05-03-001 / T6.

package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/db"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/projects"
)

func main() {
	retain := flag.Int("retain", projects.DefaultVersionRetention, "keep N most-recent versions per project")
	dryRun := flag.Bool("dry-run", false, "list pruneable directories without removing them")
	dataDir := flag.String("data-dir", "", "screens root (default: services/ds-service/data, derived from REPO_DIR)")
	flag.Parse()

	if *retain < 1 {
		fmt.Fprintln(os.Stderr, "--retain must be >= 1")
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

	dataRoot := *dataDir
	if dataRoot == "" {
		repoDir := os.Getenv("REPO_DIR")
		if repoDir == "" {
			repoDir = "."
		}
		dataRoot = filepath.Join(repoDir, "services/ds-service/data")
	}

	log := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	tenantIDs, err := listTenantIDs(conn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "list tenants: %v\n", err)
		os.Exit(1)
	}
	if len(tenantIDs) == 0 {
		fmt.Println("no tenants found; nothing to do")
		return
	}

	totalPruned, totalKept := 0, 0
	for _, tid := range tenantIDs {
		repo := projects.NewTenantRepo(conn.DB, tid)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		projectIDs, err := listProjectIDsForTenant(ctx, conn, tid)
		cancel()
		if err != nil {
			fmt.Fprintf(os.Stderr, "tenant %s: %v\n", tid, err)
			continue
		}
		for _, pid := range projectIDs {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			if *dryRun {
				versions, err := repo.ListVersionsByProject(ctx, pid)
				cancel()
				if err != nil {
					fmt.Fprintf(os.Stderr, "list %s: %v\n", pid, err)
					continue
				}
				if len(versions) <= *retain {
					continue
				}
				for _, v := range versions[*retain:] {
					if v.PrunedAt != nil || v.Status == "pending" {
						continue
					}
					fmt.Printf("DRY tenant=%s project=%s version=%s status=%s\n",
						tid, pid, v.ID, v.Status)
				}
				continue
			}
			pruned, kept, err := projects.PruneOldVersionDirs(ctx, log, repo, dataRoot, pid, *retain)
			cancel()
			if err != nil {
				fmt.Fprintf(os.Stderr, "prune %s: %v\n", pid, err)
				continue
			}
			totalPruned += pruned
			totalKept += kept
		}
	}
	fmt.Printf("ok pruned=%d kept=%d retain=%d dry_run=%v\n",
		totalPruned, totalKept, *retain, *dryRun)
}

func listTenantIDs(d *db.DB) ([]string, error) {
	rows, err := d.QueryContext(context.Background(), `SELECT id FROM tenants WHERE status = 'active'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func listProjectIDsForTenant(ctx context.Context, d *db.DB, tenantID string) ([]string, error) {
	rows, err := d.QueryContext(ctx,
		`SELECT id FROM projects WHERE tenant_id = ? AND deleted_at IS NULL`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
