// Command migrate-graph-index — Phase 6 U1 cold-backfill helper.
//
// Idempotent: runs RebuildGraphIndex.RebuildFull synchronously for every
// (tenant, platform) pair. Used at first deploy of Phase 6 to populate the
// graph_index table from existing Phase 1–5 data, and as the operator
// escape hatch when an SSE event is missed and the safety-net ticker hasn't
// caught up yet.
//
// Usage:
//
//	migrate-graph-index --db=/path/to/ds.db [--tenant=<tenant_id>] [--platform=mobile|web]
//	  --db        Path to ds.db (env DS_DB_PATH).
//	  --tenant    Limit to a single tenant. Default: every tenant from `tenants`.
//	  --platform  Limit to mobile or web. Default: both.
//	  --manifest  Path to public/icons/glyph/manifest.json (env GRAPH_INDEX_MANIFEST_PATH).
//	  --tokens    Path to lib/tokens/indmoney/ (env GRAPH_INDEX_TOKENS_DIR).
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/db"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/projects"
)

func main() {
	dbPath := flag.String("db", getenv("DS_DB_PATH", ""), "Path to ds.db")
	onlyTenant := flag.String("tenant", "", "Limit to a single tenant ID")
	onlyPlatform := flag.String("platform", "", "Limit to mobile or web (default both)")
	manifest := flag.String("manifest",
		getenv("GRAPH_INDEX_MANIFEST_PATH", ""),
		"Path to public/icons/glyph/manifest.json")
	tokens := flag.String("tokens",
		getenv("GRAPH_INDEX_TOKENS_DIR", ""),
		"Path to lib/tokens/indmoney/ directory")
	flag.Parse()

	if *dbPath == "" {
		*dbPath = "services/ds-service/data/ds.db"
	}
	// Default the manifest + tokens paths relative to a typical local
	// checkout. In production these come from env.
	if *manifest == "" {
		*manifest, _ = filepath.Abs("../../public/icons/glyph/manifest.json")
	}
	if *tokens == "" {
		*tokens, _ = filepath.Abs("../../lib/tokens/indmoney")
	}

	d, err := db.Open(*dbPath)
	if err != nil {
		log.Fatalf("migrate-graph-index: open db %q: %v", *dbPath, err)
	}
	defer d.Close()

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	ctx := context.Background()

	tenants, err := listTenants(ctx, d.DB, *onlyTenant)
	if err != nil {
		log.Fatalf("migrate-graph-index: list tenants: %v", err)
	}
	if len(tenants) == 0 {
		log.Fatalf("migrate-graph-index: no tenants found (filter=%q)", *onlyTenant)
	}

	platforms := []string{projects.GraphPlatformMobile, projects.GraphPlatformWeb}
	if *onlyPlatform != "" {
		platforms = []string{*onlyPlatform}
	}

	pool := &projects.GraphRebuildPool{
		Size:      1, // synchronous; run in this process
		DB:        d.DB,
		TenantIDs: tenants,
		Sources: projects.GraphRebuildSources{
			ManifestPath: *manifest,
			TokensDir:    *tokens,
		},
		Log: logger,
	}

	totalRows := 0
	start := time.Now()
	for _, tenantID := range tenants {
		for _, platform := range platforms {
			tStart := time.Now()
			if err := pool.RebuildFull(ctx, tenantID, platform); err != nil {
				log.Fatalf("migrate-graph-index: rebuild tenant=%s platform=%s: %v", tenantID, platform, err)
			}
			n, _ := countRows(ctx, d.DB, tenantID, platform)
			totalRows += n
			fmt.Printf("rebuilt tenant=%s platform=%s rows=%d in %s\n",
				tenantID, platform, n, time.Since(tStart).Round(time.Millisecond))
		}
	}
	fmt.Printf("done. tenants=%d rows=%d wall=%s\n",
		len(tenants), totalRows, time.Since(start).Round(time.Millisecond))
}

// listTenants returns either the single requested tenant (if onlyTenant is
// non-empty) or every row from the `tenants` table. Returns the IDs in
// alphabetical order so successive runs touch tenants in the same sequence.
func listTenants(ctx context.Context, sqlDB *sql.DB, onlyTenant string) ([]string, error) {
	if onlyTenant != "" {
		var id string
		err := sqlDB.QueryRowContext(ctx, `SELECT id FROM tenants WHERE id = ?`, onlyTenant).Scan(&id)
		if err == sql.ErrNoRows {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		return []string{id}, nil
	}
	rows, err := sqlDB.QueryContext(ctx, `SELECT id FROM tenants ORDER BY id`)
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

// countRows returns the materialised row count for a (tenant, platform) so
// the operator sees per-slice numbers.
func countRows(ctx context.Context, sqlDB *sql.DB, tenantID, platform string) (int, error) {
	var n int
	err := sqlDB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM graph_index WHERE tenant_id = ? AND platform = ?`,
		tenantID, platform,
	).Scan(&n)
	return n, err
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
