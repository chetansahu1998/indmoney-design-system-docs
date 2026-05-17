// Command backfill-svg-markup re-runs the post-Stage-9 SVG inline pass
// over existing project versions so leaves imported before U8 of the
// Figma-Dev-Mode parity initiative get `svg_markup` spliced into their
// canonical_tree blobs.
//
// Background: U7/U8 (commit 8bdcbf9) added a server-side step that reads
// each SVG-eligible cluster's bytes from disk (written earlier by the
// Stage 9.1 SVG cluster renderer) and splices them into the
// `canonical_tree.svg_markup` field. The client (LeafFrameRenderer +
// nodeToHTML.renderClusterPlaceholder) branches on `svg_markup` to
// emit inline <svg> instead of a raster <img src=PNG>.
//
// Existing leaves (imported before U8) already have the SVG bytes on
// disk via the older Stage 9 path but no `svg_markup` field on the
// canonical_tree. The result: a user installing the new build sees
// zero visible improvement until they manually re-import every file.
// This one-shot CLI walks the DB and runs the inline pass against
// every (tenant, project, version) tuple.
//
// Usage on Fly:
//
//	fly ssh console -C "/usr/local/bin/backfill-svg-markup"
//	fly ssh console -C "/usr/local/bin/backfill-svg-markup --tenant <id>"
//	fly ssh console -C "/usr/local/bin/backfill-svg-markup --dry-run"
//
// Local dev (from repo root):
//
//	cd services/ds-service && go run ./cmd/backfill-svg-markup
//
// Idempotent: InlineSVGMarkup checks for an existing non-empty
// `svg_markup` before re-sanitizing + re-writing. Re-running the CLI
// after a partial run is safe and skips already-inlined nodes.
//
// Failures are screen-local. A single bad SVG or missing file logs and
// continues; the version row is never failed.
//
// Plan: docs/plans/2026-05-17-003-feat-canvas-figma-dev-mode-parity-plan.md
// (post-ship QA Bug 7).
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/db"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/projects"
)

func main() {
	tenantFilter := flag.String("tenant", "", "tenant ID (optional — restricts to one)")
	fileFilter := flag.String("file", "", "Figma file ID (optional — restricts to projects matching this file_id)")
	versionFilter := flag.String("version", "", "version ID (optional — restricts to one; requires --tenant)")
	dataDir := flag.String("data-dir", "", "ds-service data root (default: $REPO_DIR/services/ds-service/data or ./services/ds-service/data)")
	dryRun := flag.Bool("dry-run", false, "discover candidate versions + log per-version SVG cluster counts; skip writes")
	perVersionTimeout := flag.Duration("timeout", 10*time.Minute, "per-version wall-clock budget for the inline pass")
	flag.Parse()

	if *versionFilter != "" && *tenantFilter == "" {
		fmt.Fprintln(os.Stderr, "--version requires --tenant")
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

	// SIGTERM cancels the outer context so in-flight version work
	// finishes cleanly without partial writes leaking.
	rootCtx, cancelRoot := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancelRoot()

	tenantIDs, err := listTenantIDs(rootCtx, conn, *tenantFilter)
	if err != nil {
		fmt.Fprintf(os.Stderr, "list tenants: %v\n", err)
		os.Exit(1)
	}
	if len(tenantIDs) == 0 {
		fmt.Println("no tenants matched; nothing to do")
		return
	}

	var (
		totalVersions    int
		totalInlined     int
		totalScreens     int
		totalSVGClusters int
		totalOversize    int
		totalErrors      int
	)

	for _, tid := range tenantIDs {
		if rootCtx.Err() != nil {
			break
		}
		repo := projects.NewTenantRepo(conn.DB, tid)
		projectRows, err := listProjects(rootCtx, conn, tid, *fileFilter)
		if err != nil {
			fmt.Fprintf(os.Stderr, "tenant %s: list projects: %v\n", tid, err)
			totalErrors++
			continue
		}
		for _, pr := range projectRows {
			if rootCtx.Err() != nil {
				break
			}
			if pr.FileID == "" {
				// Legacy rows without file_id can't resolve the SVG
				// bytes path (data/assets/<tenant>/<file>/v<vi>/<id>.svg).
				// Skip with a warning so the operator knows the row
				// exists but is unreachable to this backfill.
				log.Warn("skip project without file_id",
					"tenant_id", tid, "project_id", pr.ID, "slug", pr.Slug)
				continue
			}
			versions, err := repo.ListVersionsByProject(rootCtx, pr.ID)
			if err != nil {
				fmt.Fprintf(os.Stderr, "tenant %s project %s: list versions: %v\n",
					tid, pr.ID, err)
				totalErrors++
				continue
			}
			for _, v := range versions {
				if *versionFilter != "" && v.ID != *versionFilter {
					continue
				}
				// Skip pending / failed versions — Stage 9 SVG bytes
				// may not exist for those, and the operator-visible
				// outcome is "no-op."
				if v.Status != "view_ready" && v.Status != "ready" {
					continue
				}
				// Pruned versions are still inlineable: cleanup.go:54 only
				// removes `<dataDir>/screens/<tenant>/<version>` (the PNG
				// cache), NOT `<dataDir>/assets/<tenant>/<file>/v<vi>`
				// (where Stage 9.1 wrote the SVG bytes). The CLI used to
				// skip pruned versions defensively, but that masked
				// legitimate work — re-inlining a pruned version still
				// makes the canvas serve inline SVG instead of falling
				// through to an absent PNG.
				_ = v.PrunedAt
				totalVersions++
				inlined, screens, svgCount, oversize, err := runVersion(rootCtx, log, conn, repo, tid, pr, v, dataRoot, *perVersionTimeout, *dryRun)
				if err != nil {
					fmt.Fprintf(os.Stderr, "tenant %s project %s version %s: %v\n",
						tid, pr.ID, v.ID, err)
					totalErrors++
					continue
				}
				totalInlined += inlined
				totalScreens += screens
				totalSVGClusters += svgCount
				totalOversize += oversize
			}
		}
	}

	fmt.Printf("ok versions=%d screens=%d svg_clusters=%d inlined_screens=%d oversize_skipped=%d errors=%d dry_run=%v\n",
		totalVersions, totalScreens, totalSVGClusters, totalInlined, totalOversize, totalErrors, *dryRun)
	// Reflect failures in the exit code so `fly ssh console -C` and CI
	// invocations don't silently swallow operator-visible problems.
	if totalErrors > 0 {
		os.Exit(1)
	}
}

// runVersion handles one (tenant, project, version) tuple. Returns
// (inlinedScreens, totalScreens, svgClusterCount, oversizeScreens, error).
func runVersion(
	parentCtx context.Context,
	log *slog.Logger,
	conn *db.DB,
	repo *projects.TenantRepo,
	tenantID string,
	project projectRow,
	version projects.ProjectVersion,
	dataDir string,
	timeout time.Duration,
	dryRun bool,
) (int, int, int, int, error) {
	ctx, cancel := context.WithTimeout(parentCtx, timeout)
	defer cancel()

	screens, err := repo.ListScreensByVersion(ctx, version.ID)
	if err != nil {
		return 0, 0, 0, 0, fmt.Errorf("list screens: %w", err)
	}
	if len(screens) == 0 {
		return 0, 0, 0, 0, nil
	}

	// Discover SVG cluster IDs by walking each screen's canonical_tree
	// with the same extractor Stage 9.1 uses. Aggregating across all
	// screens of the version mirrors how the live pipeline collects
	// svgIDs from `reattaches`.
	svgSet := make(map[string]struct{})
	frames := make([]projects.PipelineFrame, 0, len(screens))
	for _, s := range screens {
		frames = append(frames, projects.PipelineFrame{ScreenID: s.ID})
		treeJSON, err := projects.LoadCanonicalTreeForBackfill(ctx, conn.DB, s.ID)
		if err != nil {
			log.Warn("load tree failed",
				"tenant_id", tenantID, "version_id", version.ID,
				"screen_id", s.ID, "err", err.Error())
			continue
		}
		if treeJSON == "" {
			continue
		}
		for _, c := range projects.ExtractClustersWithSVGFlag([]byte(treeJSON)) {
			if c.SVGEligible && c.ID != "" {
				svgSet[c.ID] = struct{}{}
			}
		}
	}
	if len(svgSet) == 0 {
		// No SVG-eligible clusters on this version — InlineSVGMarkup
		// would short-circuit. Log + return so the totals stay honest.
		log.Info("no svg clusters",
			"tenant_id", tenantID, "version_id", version.ID,
			"screens", len(screens))
		return 0, len(screens), 0, 0, nil
	}
	svgIDs := make([]string, 0, len(svgSet))
	for id := range svgSet {
		svgIDs = append(svgIDs, id)
	}

	if dryRun {
		log.Info("DRY discover",
			"tenant_id", tenantID, "file_id", project.FileID,
			"version_id", version.ID, "version_index", version.VersionIndex,
			"screens", len(screens), "svg_clusters", len(svgIDs))
		return 0, len(screens), len(svgIDs), 0, nil
	}

	in := projects.PipelineInputs{
		VersionID: version.ID,
		ProjectID: project.ID,
		TenantID:  tenantID,
		FileID:    project.FileID,
		Frames:    frames,
	}
	var oversize int
	deps := projects.SVGInlineDeps{
		DB:              conn.DB,
		DataDir:         dataDir,
		Log:             log,
		OversizeScreens: &oversize,
	}
	inlined, err := projects.InlineSVGMarkup(ctx, deps, in, svgIDs)
	if err != nil {
		return 0, len(screens), len(svgIDs), oversize, err
	}
	log.Info("inlined",
		"tenant_id", tenantID, "file_id", project.FileID,
		"version_id", version.ID, "version_index", version.VersionIndex,
		"screens", len(screens), "svg_clusters", len(svgIDs),
		"updated_screens", inlined, "oversize_skipped", oversize)
	return inlined, len(screens), len(svgIDs), oversize, nil
}

// ─── DB helpers ─────────────────────────────────────────────────────

func listTenantIDs(ctx context.Context, d *db.DB, filter string) ([]string, error) {
	if filter != "" {
		return []string{filter}, nil
	}
	rows, err := d.QueryContext(ctx, `SELECT id FROM tenants WHERE status = 'active'`)
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

type projectRow struct {
	ID     string
	Slug   string
	FileID string
}

func listProjects(ctx context.Context, d *db.DB, tenantID string, fileFilter string) ([]projectRow, error) {
	q := `SELECT id, slug, COALESCE(file_id, '') FROM projects
	       WHERE tenant_id = ? AND deleted_at IS NULL`
	args := []any{tenantID}
	if fileFilter != "" {
		q += ` AND file_id = ?`
		args = append(args, fileFilter)
	}
	rows, err := d.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []projectRow
	for rows.Next() {
		var pr projectRow
		if err := rows.Scan(&pr.ID, &pr.Slug, &pr.FileID); err != nil {
			return nil, err
		}
		out = append(out, pr)
	}
	return out, rows.Err()
}
