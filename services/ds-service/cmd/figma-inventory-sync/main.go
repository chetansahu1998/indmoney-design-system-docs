// cmd/figma-inventory-sync — one-shot CLI that runs a single FIGMA DB
// inventory poll cycle against the local DB. Use it to seed a team and
// inspect the result without having to boot the full server.
//
// Usage:
//
//	go run ./cmd/figma-inventory-sync \
//	    -team-id 898419887480849435 \
//	    -team-name "INDmoney"
//
// Requires FIGMA_PAT in the environment (or .env.local in any ancestor of
// the cwd). The DB path defaults to services/ds-service/data/ds.db
// relative to repo root; override with -db.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/db"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/figma/inventory"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/projects"
)

func main() {
	var (
		teamID   = flag.String("team-id", "", "Figma team id (required)")
		teamName = flag.String("team-name", "", "Display name for the team (required)")
		dbPath   = flag.String("db", "", "Path to ds.db (default: services/ds-service/data/ds.db relative to cwd or ancestors)")
		tenantID = flag.String("tenant", "system", "tenant_id to crawl under")
		dump     = flag.Bool("dump", true, "Print the inventory tree as JSON after the cycle")
	)
	flag.Parse()

	if *teamID == "" || *teamName == "" {
		fmt.Fprintln(os.Stderr, "usage: figma-inventory-sync -team-id <id> -team-name <name>")
		os.Exit(2)
	}

	loadDotEnv()
	pat := os.Getenv("FIGMA_PAT")
	if pat == "" {
		fmt.Fprintln(os.Stderr, "FIGMA_PAT not set (checked env + .env.local ancestors)")
		os.Exit(1)
	}

	dbFile := *dbPath
	if dbFile == "" {
		dbFile = findDB()
	}
	if dbFile == "" {
		fmt.Fprintln(os.Stderr, "cannot locate ds.db — pass -db <path>")
		os.Exit(1)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	fmt.Printf("opening db: %s\n", dbFile)
	d, err := db.Open(dbFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "db open:", err)
		os.Exit(1)
	}
	defer d.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Insert the team seed.
	repo := projects.NewTenantRepo(d.DB, *tenantID)
	if err := repo.UpsertFigmaTeamSeed(ctx, projects.FigmaTeamSeed{
		TeamID:        *teamID,
		TeamName:      *teamName,
		AddedByUserID: "cli",
		Enabled:       true,
	}); err != nil {
		fmt.Fprintln(os.Stderr, "upsert seed:", err)
		os.Exit(1)
	}
	fmt.Printf("seeded team_id=%s team_name=%q under tenant=%s\n", *teamID, *teamName, *tenantID)

	// Build a poller that uses the env PAT for every tenant.
	poller, err := inventory.New(inventory.Config{
		DB: d.DB,
		ResolvePAT: func(ctx context.Context, _ string) (string, error) {
			return pat, nil
		},
		ListTenants:    func(ctx context.Context) []string { return []string{*tenantID} },
		Logger:         logger,
		PagesSyncBatch: 200, // first run: drain everything in one cycle
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "poller init:", err)
		os.Exit(1)
	}

	// We can't call the unexported runCycle directly, but TriggerSync is the
	// public entrypoint. Start the loop, trigger once, wait for the run row
	// to land, then cancel.
	runCtx, runCancel := context.WithCancel(ctx)
	poller.Start(runCtx)
	poller.TriggerSync()
	fmt.Println("sync triggered, waiting for cycle to complete...")

	// Poll figma_inventory_run for a finished row.
	deadline := time.Now().Add(4 * time.Minute)
	var lastRunID int64
	for time.Now().Before(deadline) {
		runs, err := repo.ListFigmaInventoryRuns(ctx, 1)
		if err == nil && len(runs) == 1 && !runs[0].FinishedAt.IsZero() && runs[0].ID != lastRunID {
			r := runs[0]
			fmt.Printf("\n=== cycle complete (run_id=%d, %dms) ===\n",
				r.ID, r.FinishedAt.Sub(r.StartedAt).Milliseconds())
			fmt.Printf("teams_crawled=%d projects=%d files=%d files_refetched=%d pages=%d sections=%d errors=%d\n",
				r.TeamsCrawled, r.ProjectsSeen, r.FilesSeen, r.FilesRefetched,
				r.PagesUpserted, r.SectionsUpserted, r.ErrorCount)
			if r.ErrorSampleJSON != "" {
				fmt.Println("errors:", r.ErrorSampleJSON)
			}
			lastRunID = r.ID
			break
		}
		time.Sleep(1 * time.Second)
	}
	runCancel()

	// Show the seed's last-crawl status.
	seeds, _ := repo.ListFigmaTeamSeeds(ctx)
	for _, s := range seeds {
		if s.TeamID == *teamID {
			fmt.Printf("\nseed: enabled=%v last_crawl_at=%s status=%s err=%q\n",
				s.Enabled, s.LastCrawlAt.Format(time.RFC3339), s.LastCrawlStatus, s.LastCrawlError)
		}
	}

	if *dump {
		tree, err := repo.GetFigmaInventoryTree(ctx, *teamID, false)
		if err != nil {
			fmt.Fprintln(os.Stderr, "tree:", err)
			os.Exit(1)
		}
		fmt.Println("\n=== inventory tree ===")
		summarize(tree, 0)
		fmt.Println("\n=== raw JSON ===")
		b, _ := json.MarshalIndent(tree, "", "  ")
		fmt.Println(string(b))
	}
}

func summarize(n *projects.FigmaInventoryTreeNode, depth int) {
	if n == nil {
		return
	}
	indent := strings.Repeat("  ", depth)
	tail := ""
	switch n.Kind {
	case "file":
		if n.LastModified != "" {
			tail = " [last_modified=" + n.LastModified + "]"
		}
	case "section":
		if n.X != nil && n.Y != nil {
			tail = fmt.Sprintf(" [x=%.0f y=%.0f w=%.0f h=%.0f]", *n.X, *n.Y, derefF(n.Width), derefF(n.Height))
		}
	}
	fmt.Printf("%s- %s %q%s\n", indent, n.Kind, n.Name, tail)
	for _, c := range n.Children {
		summarize(c, depth+1)
	}
}

func derefF(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

// findDB walks up from cwd looking for an existing ds.db. Checks both
// "<dir>/data/ds.db" (we're inside services/ds-service) and
// "<dir>/services/ds-service/data/ds.db" (we're at repo root), preferring
// whichever exists first as we ascend. Critically, the local check comes
// first so running from inside services/ds-service doesn't accidentally
// create a fresh nested DB under services/ds-service/services/ds-service.
func findDB() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	for d := cwd; d != "/" && d != ""; d = filepath.Dir(d) {
		// First: already inside a ds-service-shaped dir.
		alt := filepath.Join(d, "data", "ds.db")
		if _, err := os.Stat(alt); err == nil {
			return alt
		}
		// Second: at repo root.
		candidate := filepath.Join(d, "services", "ds-service", "data", "ds.db")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

// loadDotEnv mirrors cmd/server's helper: reads .env.local from cwd or
// any ancestor and sets the values into os.Environ if not already set.
func loadDotEnv() {
	cwd, err := os.Getwd()
	if err != nil {
		return
	}
	for d := cwd; d != "/" && d != ""; d = filepath.Dir(d) {
		path := filepath.Join(d, ".env.local")
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			eq := strings.IndexByte(line, '=')
			if eq <= 0 {
				continue
			}
			k := strings.TrimSpace(line[:eq])
			v := strings.TrimSpace(line[eq+1:])
			v = strings.Trim(v, `"'`)
			if _, ok := os.LookupEnv(k); !ok {
				os.Setenv(k, v)
			}
		}
		return
	}
}
