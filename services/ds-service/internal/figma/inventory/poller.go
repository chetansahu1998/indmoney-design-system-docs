// Package inventory polls Figma teams > projects > files > pages > sections
// every 5 minutes and mirrors the metadata into the FIGMA DB (migration
// 0025). It does not store node trees.
//
// Two-tier change detection:
//
//   Tier A — every 5 min: per tenant, for each enabled team seed:
//                         GET /v1/teams/<id>/projects     (tier-2, 40 RPM)
//                         GET /v1/projects/<id>/files     (tier-2, 40 RPM)
//                         Upsert project + file shells with the cheap
//                         `last_modified` from the file-list endpoint.
//
//   Tier B — same cycle: for files whose API `last_modified` is newer than
//                        the cached row, fetch
//                        GET /v1/files/<key>?depth=2     (tier-1, 12 RPM)
//                        and upsert pages + sections.
//
// All HTTP calls go through the shared per-PAT rate limiter built into
// client.Client, so the inventory poller cooperates politely with the
// audit pipeline's pre-existing Figma calls. The poller never aborts a
// cycle on error — per-team/per-file failures are logged + counted and
// the next cycle re-attempts.
//
// Wiring: cmd/server/main.go constructs one *Poller per process and calls
// Start(ctx) alongside the audit worker pool. Stops when ctx is cancelled.
package inventory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/figma/client"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/projects"
)

// DefaultInterval is the poll cadence. 5 minutes matches the user's
// requirement; can be lowered for tests via PollerConfig.Interval.
const DefaultInterval = 5 * time.Minute

// DefaultPagesSyncBatch is the per-cycle cap on Tier-B (depth=2) fetches.
// At 12 RPM tier-1 budget, 30 files in a 5-minute window leaves ~50%
// headroom for the audit pipeline's other tier-1 calls (GetFileNodes etc.).
const DefaultPagesSyncBatch = 30

// PATResolver returns the decrypted Figma PAT for one tenant, or
// (empty string, nil) when no token is configured. Mirrors the signature
// already used by cmd/server/main.go's figmaPATResolver closure so we
// can pass that closure straight in.
type PATResolver func(ctx context.Context, tenantID string) (string, error)

// TenantLister returns the set of tenant_ids the poller should crawl
// each cycle. Pass discoverTenantIDs from cmd/server/main.go (or a
// fixed list in tests).
type TenantLister func(ctx context.Context) []string

// Config bundles the dependencies. Required: DB, ResolvePAT, ListTenants.
// Optional: Interval (default 5m), PagesSyncBatch (default 30), Logger.
type Config struct {
	DB             *sql.DB
	ResolvePAT     PATResolver
	ListTenants    TenantLister
	Interval       time.Duration
	PagesSyncBatch int
	// Phase 2C — max depth the deep-tree fetch traverses. 0 → use the
	// client's default (14). Configurable so an operator can dial it
	// down on huge files where 14 levels of payload approaches the
	// 1 GB body cap.
	DeepFetchDepth int
	NewClient      func(pat string) FigmaInventoryClient // optional override for tests
	Logger         *slog.Logger
	Now            func() time.Time // injectable clock
}

// FigmaInventoryClient is the slice of *client.Client this poller calls.
// Defining the interface here means tests can pass a fake without spinning
// up an HTTP server.
type FigmaInventoryClient interface {
	GetTeamProjects(ctx context.Context, teamID string) (*client.TeamProjectsResponse, error)
	GetProjectFiles(ctx context.Context, projectID string) (*client.ProjectFilesResponse, error)
	// Phase 2C — deep fetch replaces the legacy depth=2 call. Pages,
	// sections, AND the full node tree all come back in one tier-1
	// response. depth=0 → client picks default (14).
	GetFileDeepTree(ctx context.Context, fileKey string, depth int) (*client.FileDeepTree, error)
}

// Poller is the long-lived crawler. Construct via New(), then call Start(ctx).
type Poller struct {
	cfg Config

	// triggerCh receives manual-sync requests from the admin "Sync now"
	// endpoint. Buffered so multiple admins clicking the button don't block
	// — extra notifications collapse into one in-progress cycle.
	triggerCh chan struct{}

	// onCycleDone, when set, is called at the end of every cycle with the
	// per-cycle stats. Only used by tests; production passes nil.
	onCycleDone func(stats RunStats)

	// readyCh closes after runCycle returns for the first time. Lets
	// downstream consumers (notably the autosync retry loop in
	// cmd/server) block on first-cycle completion instead of guessing
	// at a 90-second initial delay. Use Ready() to access — direct
	// access keeps the type's zero value invalid.
	readyCh   chan struct{}
	readyOnce sync.Once

	stoppedMu sync.Mutex
	stopped   bool
}

// Ready returns a channel that is closed after the poller's first cycle
// finishes (whether it succeeded or accumulated errors — the contract is
// "deep_synced_at + content_hash columns are now safe to read", not "every
// file succeeded"). Returns the same channel on every call; safe for
// multiple receivers. Callers MUST also select on ctx.Done() to avoid
// deadlock if the PAT is misconfigured and the cycle never runs.
func (p *Poller) Ready() <-chan struct{} { return p.readyCh }

// RunStats is the per-cycle counters surfaced to the figma_inventory_run row.
type RunStats struct {
	TenantsCrawled   int
	TeamsCrawled     int
	ProjectsSeen     int
	FilesSeen        int
	FilesRefetched   int
	PagesUpserted    int
	SectionsUpserted int
	NodesUpserted    int
	Errors           []string
}

// New builds a Poller. Returns an error if required deps are missing.
func New(cfg Config) (*Poller, error) {
	if cfg.DB == nil {
		return nil, errors.New("inventory: DB required")
	}
	if cfg.ResolvePAT == nil {
		return nil, errors.New("inventory: ResolvePAT required")
	}
	if cfg.ListTenants == nil {
		return nil, errors.New("inventory: ListTenants required")
	}
	if cfg.Interval <= 0 {
		cfg.Interval = DefaultInterval
	}
	if cfg.PagesSyncBatch <= 0 {
		cfg.PagesSyncBatch = DefaultPagesSyncBatch
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.NewClient == nil {
		cfg.NewClient = func(pat string) FigmaInventoryClient {
			return client.New(pat)
		}
	}
	return &Poller{
		cfg:       cfg,
		triggerCh: make(chan struct{}, 1),
		readyCh:   make(chan struct{}),
	}, nil
}

// Start launches the polling loop. Non-blocking — returns immediately after
// spawning the goroutine. The loop exits when ctx is cancelled.
func (p *Poller) Start(ctx context.Context) {
	go p.loop(ctx)
}

// TriggerSync requests an out-of-band cycle. Non-blocking — collapses
// multiple back-to-back requests into a single follow-up cycle.
func (p *Poller) TriggerSync() {
	select {
	case p.triggerCh <- struct{}{}:
	default:
	}
}

func (p *Poller) loop(ctx context.Context) {
	t := time.NewTicker(p.cfg.Interval)
	defer t.Stop()

	// First cycle runs after a short delay so the server finishes booting
	// before we start hammering the Figma API. 30s mirrors the existing
	// WorkerLeaseDuration cadence already in projects/worker.go.
	first := time.NewTimer(30 * time.Second)
	defer first.Stop()

	for {
		select {
		case <-ctx.Done():
			p.cfg.Logger.Info("figma_inventory: poller stopping")
			return
		case <-first.C:
			p.runCycle(ctx)
		case <-t.C:
			p.runCycle(ctx)
		case <-p.triggerCh:
			p.runCycle(ctx)
		}
	}
}

// runCycle performs one full pass: enumerate tenants, then per-tenant
// enumerate teams, projects, files, and selectively refresh pages.
// Errors are accumulated, never thrown.
func (p *Poller) runCycle(ctx context.Context) {
	// REL-4 audit fix: signal Ready() as soon as a cycle attempt
	// completes — succeeded, errored, or had zero tenants. Doc
	// promises "first cycle attempted", not "first cycle succeeded".
	// Without this defer, the zero-tenants early-return below would
	// leave readyCh open forever, blocking startAutosyncRetryLoop.
	defer p.readyOnce.Do(func() { close(p.readyCh) })

	start := p.cfg.Now()
	tenants := p.cfg.ListTenants(ctx)
	if len(tenants) == 0 {
		p.cfg.Logger.Debug("figma_inventory: no tenants to crawl")
		return
	}

	totalStats := RunStats{TenantsCrawled: len(tenants)}
	for _, tenantID := range tenants {
		select {
		case <-ctx.Done():
			return
		default:
		}
		stats := p.crawlTenant(ctx, tenantID)
		totalStats.TeamsCrawled += stats.TeamsCrawled
		totalStats.ProjectsSeen += stats.ProjectsSeen
		totalStats.FilesSeen += stats.FilesSeen
		totalStats.FilesRefetched += stats.FilesRefetched
		totalStats.PagesUpserted += stats.PagesUpserted
		totalStats.SectionsUpserted += stats.SectionsUpserted
		totalStats.NodesUpserted += stats.NodesUpserted
		totalStats.Errors = append(totalStats.Errors, stats.Errors...)
	}

	p.cfg.Logger.Info("figma_inventory: cycle complete",
		"duration_ms", time.Since(start).Milliseconds(),
		"tenants", totalStats.TenantsCrawled,
		"teams", totalStats.TeamsCrawled,
		"projects", totalStats.ProjectsSeen,
		"files", totalStats.FilesSeen,
		"files_refetched", totalStats.FilesRefetched,
		"pages", totalStats.PagesUpserted,
		"sections", totalStats.SectionsUpserted,
		"nodes", totalStats.NodesUpserted,
		"errors", len(totalStats.Errors),
	)

	if p.onCycleDone != nil {
		p.onCycleDone(totalStats)
	}
}

// crawlTenant runs one tenant's full pass. PAT is resolved once; if it's
// missing we skip the tenant entirely (no point recording an empty run).
func (p *Poller) crawlTenant(ctx context.Context, tenantID string) RunStats {
	logger := p.cfg.Logger.With("tenant", tenantID)
	stats := RunStats{}

	pat, err := p.cfg.ResolvePAT(ctx, tenantID)
	if err != nil {
		logger.Warn("figma_inventory: pat resolve failed",
			"err", err.Error())
		stats.Errors = append(stats.Errors, fmt.Sprintf("pat_resolve: %s", err.Error()))
		return stats
	}
	if pat == "" {
		logger.Debug("figma_inventory: tenant has no PAT, skipping")
		return stats
	}

	repo := projects.NewTenantRepo(p.cfg.DB, tenantID)
	seeds, err := repo.ListEnabledFigmaTeamSeeds(ctx)
	if err != nil {
		logger.Warn("figma_inventory: list seeds failed",
			"err", err.Error())
		stats.Errors = append(stats.Errors, fmt.Sprintf("list_seeds: %s", err.Error()))
		return stats
	}
	if len(seeds) == 0 {
		return stats
	}

	runID, err := repo.StartFigmaInventoryRun(ctx, p.cfg.Now())
	if err != nil {
		logger.Warn("figma_inventory: start run row failed",
			"err", err.Error())
		// Continue anyway — we'd rather crawl without an audit row than skip the cycle.
		runID = 0
	}

	fc := p.cfg.NewClient(pat)
	seenAt := p.cfg.Now()

	for _, seed := range seeds {
		// #12 audit fix: `break` inside `select` only exits the select,
		// not the for-loop. Return on ctx-cancel so SIGTERM actually
		// stops the crawl mid-iteration instead of finishing every seed.
		if ctx.Err() != nil {
			return stats
		}
		teamStats := p.crawlTeam(ctx, fc, repo, seed, seenAt, logger)
		stats.TeamsCrawled++
		stats.ProjectsSeen += teamStats.ProjectsSeen
		stats.FilesSeen += teamStats.FilesSeen
		stats.Errors = append(stats.Errors, teamStats.Errors...)
	}

	// Tier-B: drain files needing pages-sync, bounded by PagesSyncBatch.
	files, err := repo.FilesNeedingPagesSync(ctx, p.cfg.PagesSyncBatch)
	if err != nil {
		logger.Warn("figma_inventory: list files needing sync failed",
			"err", err.Error())
		stats.Errors = append(stats.Errors, fmt.Sprintf("files_needing_sync: %s", err.Error()))
	}
	for _, f := range files {
		// #12 audit fix: same break-in-select issue as the seeds loop.
		if ctx.Err() != nil {
			return stats
		}
		pageCount, sectionCount, nodeCount, err := p.syncFileDeep(ctx, fc, repo, f, seenAt, logger)
		if err != nil {
			stats.Errors = append(stats.Errors,
				fmt.Sprintf("sync_deep %s: %s", f.FileKey, errSummary(err)))
			continue
		}
		stats.FilesRefetched++
		stats.PagesUpserted += pageCount
		stats.SectionsUpserted += sectionCount
		stats.NodesUpserted += nodeCount
	}

	if runID > 0 {
		finishStats := projects.FigmaInventoryRunRow{
			TeamsCrawled:    stats.TeamsCrawled,
			ProjectsSeen:    stats.ProjectsSeen,
			FilesSeen:       stats.FilesSeen,
			FilesRefetched:  stats.FilesRefetched,
			PagesUpserted:   stats.PagesUpserted,
			SectionsUpserted: stats.SectionsUpserted,
			NodesUpserted:    stats.NodesUpserted,
			ErrorCount:      len(stats.Errors),
		}
		if err := repo.FinishFigmaInventoryRun(ctx, runID, finishStats, stats.Errors); err != nil {
			logger.Warn("figma_inventory: finish run row failed",
				"err", err.Error(), "run_id", runID)
		}
	}

	return stats
}

// crawlTeam fetches one team's projects + per-project files (Tier-A only).
// Page/section fan-out is handled separately in Tier-B so the per-team
// loop can stay simple.
func (p *Poller) crawlTeam(
	ctx context.Context,
	fc FigmaInventoryClient,
	repo *projects.TenantRepo,
	seed projects.FigmaTeamSeed,
	seenAt time.Time,
	logger *slog.Logger,
) RunStats {
	stats := RunStats{}

	tpResp, err := fc.GetTeamProjects(ctx, seed.TeamID)
	if err != nil {
		status := "error"
		if apiErr, ok := err.(*client.APIError); ok && apiErr.IsAuth() {
			status = "forbidden"
		}
		_ = repo.MarkFigmaTeamSeedCrawl(ctx, seed.TeamID, status, errSummary(err))
		stats.Errors = append(stats.Errors,
			fmt.Sprintf("get_team_projects %s: %s", seed.TeamID, errSummary(err)))
		logger.Warn("figma_inventory: GetTeamProjects failed",
			"team", seed.TeamID, "status", status, "err", err.Error())
		return stats
	}

	// figma_team observed row — name comes from the projects response.
	teamName := tpResp.Name
	if teamName == "" {
		teamName = seed.TeamName
	}
	if err := repo.UpsertFigmaTeam(ctx, seed.TeamID, teamName); err != nil {
		stats.Errors = append(stats.Errors,
			fmt.Sprintf("upsert_team %s: %s", seed.TeamID, err.Error()))
	}

	// projects
	projRows := make([]projects.FigmaProjectRow, 0, len(tpResp.Projects))
	for _, pr := range tpResp.Projects {
		if pr.ID == "" {
			continue
		}
		projRows = append(projRows, projects.FigmaProjectRow{
			ProjectID: pr.ID,
			TeamID:    seed.TeamID,
			Name:      pr.Name,
		})
	}
	if err := repo.UpsertFigmaProjects(ctx, seed.TeamID, projRows, seenAt); err != nil {
		stats.Errors = append(stats.Errors,
			fmt.Sprintf("upsert_projects %s: %s", seed.TeamID, err.Error()))
	}
	if _, err := repo.SweepFigmaProjects(ctx, seed.TeamID, seenAt); err != nil {
		stats.Errors = append(stats.Errors,
			fmt.Sprintf("sweep_projects %s: %s", seed.TeamID, err.Error()))
	}
	stats.ProjectsSeen = len(projRows)

	// files per project
	for _, pr := range tpResp.Projects {
		select {
		case <-ctx.Done():
			return stats
		default:
		}
		pfResp, err := fc.GetProjectFiles(ctx, pr.ID)
		if err != nil {
			stats.Errors = append(stats.Errors,
				fmt.Sprintf("get_project_files %s: %s", pr.ID, errSummary(err)))
			logger.Warn("figma_inventory: GetProjectFiles failed",
				"project", pr.ID, "err", err.Error())
			continue
		}
		fileRows := make([]projects.FigmaFileRow, 0, len(pfResp.Files))
		for _, f := range pfResp.Files {
			if f.Key == "" {
				continue
			}
			fileRows = append(fileRows, projects.FigmaFileRow{
				FileKey:      f.Key,
				Name:         f.Name,
				ThumbnailURL: f.ThumbnailURL,
				LastModified: parseAPITime(f.LastModified),
			})
		}
		if err := repo.UpsertFigmaFilesShell(ctx, pr.ID, seed.TeamID, fileRows, seenAt); err != nil {
			stats.Errors = append(stats.Errors,
				fmt.Sprintf("upsert_files %s: %s", pr.ID, err.Error()))
		}
		if _, err := repo.SweepFigmaFiles(ctx, pr.ID, seenAt); err != nil {
			stats.Errors = append(stats.Errors,
				fmt.Sprintf("sweep_files %s: %s", pr.ID, err.Error()))
		}
		stats.FilesSeen += len(fileRows)
	}

	_ = repo.MarkFigmaTeamSeedCrawl(ctx, seed.TeamID, "ok", "")
	return stats
}

// syncFileDeep does the Tier-B deep fetch + upsert for one file. One
// call to /v1/files/<key>?depth=N populates THREE tables:
//   - figma_page         (depth-1 children)
//   - figma_section      (depth-2 SECTION children of pages)
//   - figma_node         (the full flat tree, Phase 2C)
//
// Returns the per-table counts so the cycle stats can roll them up.
func (p *Poller) syncFileDeep(
	ctx context.Context,
	fc FigmaInventoryClient,
	repo *projects.TenantRepo,
	f projects.FigmaFileRow,
	seenAt time.Time,
	logger *slog.Logger,
) (pageCount, sectionCount, nodeCount int, err error) {
	resp, ferr := fc.GetFileDeepTree(ctx, f.FileKey, p.cfg.DeepFetchDepth)
	if ferr != nil {
		logger.Warn("figma_inventory: GetFileDeepTree failed",
			"file", f.FileKey, "err", ferr.Error())
		return 0, 0, 0, ferr
	}

	// Flatten the deep tree once; reuse the result for both nodeRows and
	// the per-page / per-section hash computations (U4).
	flat := resp.Flatten()
	nodeRows := make([]projects.FigmaNodeRow, 0, len(flat))
	hashable := make([]projects.HashableNode, 0, len(flat))
	for _, n := range flat {
		nodeRows = append(nodeRows, projects.FigmaNodeRow{
			FileKey:      f.FileKey,
			NodeID:       n.NodeID,
			ParentID:     n.ParentID,
			NodeType:     n.NodeType,
			Name:         n.Name,
			HasBBox:      n.HasBBox,
			X:            n.X,
			Y:            n.Y,
			Width:        n.Width,
			Height:       n.Height,
			Depth:        n.Depth,
			OrderIndex:   n.OrderIndex,
			ComponentID:  n.ComponentID,
			ComponentKey: n.ComponentKey,
		})
		hashable = append(hashable, projects.HashableNode{
			NodeID:     n.NodeID,
			ParentID:   n.ParentID,
			NodeType:   n.NodeType,
			Name:       n.Name,
			HasBBox:    n.HasBBox,
			X:          n.X,
			Y:          n.Y,
			Width:      n.Width,
			Height:     n.Height,
			Depth:      n.Depth,
			OrderIndex: n.OrderIndex,
		})
	}

	// Build the page + section row sets, with hashes + classifier output (U2 + U4).
	pages := resp.Pages()
	pageRows := make([]projects.FigmaPageRow, 0, len(pages))
	sectionRows := make([]projects.FigmaSectionRow, 0)
	classifierInputs := make([]projects.FigmaPageInput, 0, len(pages))
	for _, pg := range pages {
		classifierInputs = append(classifierInputs, projects.FigmaPageInput{
			PageID: pg.ID, Name: pg.Name,
		})
	}
	classified := projects.ClassifyPages(classifierInputs, nil)
	classifiedByID := make(map[string]projects.ClassifiedPage, len(classified))
	for _, cp := range classified {
		classifiedByID[cp.PageID] = cp
	}
	for i, pg := range pages {
		cls := classifiedByID[pg.ID]
		pageRows = append(pageRows, projects.FigmaPageRow{
			FileKey:            f.FileKey,
			PageID:             pg.ID,
			Name:               pg.Name,
			OrderIndex:         i,
			BackgroundColorHex: pg.BackgroundColorHex,
			ContentHash:        projects.ComputeContentHash(pg.ID, hashable),
			PositionHash:       projects.ComputePositionHash(pg.ID, hashable),
			Classification:     cls.Classification,
			VersionBase:        cls.VersionBase,
			VersionN:           cls.VersionN,
			PersonaHint:        cls.PersonaHint,
		})
		for j, sec := range pg.Sections {
			sectionRows = append(sectionRows, projects.FigmaSectionRow{
				FileKey:      f.FileKey,
				PageID:       pg.ID,
				SectionID:    sec.ID,
				Name:         sec.Name,
				X:            sec.X,
				Y:            sec.Y,
				Width:        sec.Width,
				Height:       sec.Height,
				OrderIndex:   j,
				ContentHash:  projects.ComputeContentHash(sec.ID, hashable),
				PositionHash: projects.ComputePositionHash(sec.ID, hashable),
			})
		}
	}

	// Group the flat node list by nearest-SECTION ancestor (plan 002 U4).
	// The poller's FileDeepTree.Flatten() guarantees parents come before
	// children, so a single forward pass suffices: a node's section-ancestor
	// is either itself (if it's a SECTION) or its parent's section-ancestor
	// (looked up from the already-populated map). Nodes whose parent chain
	// never crosses a SECTION (page-level slot-only nodes) are dropped from
	// the map and not blob-stored — the autosync planner walks SECTION
	// subtrees only.
	subtreesBySection := make(map[string][]projects.FigmaNodeRow)
	sectionAncestorByID := make(map[string]string, len(nodeRows))
	for _, n := range nodeRows {
		var secID string
		if n.NodeType == "SECTION" {
			secID = n.NodeID
		} else if anc, ok := sectionAncestorByID[n.ParentID]; ok && anc != "" {
			secID = anc
		}
		if secID == "" {
			continue // page-level / above-section node, not blob-stored
		}
		sectionAncestorByID[n.NodeID] = secID
		subtreesBySection[secID] = append(subtreesBySection[secID], n)
	}

	// Pages + sections + per-section subtree blobs in one tx (plan 002 U4
	// folds the former separate figma_node tx into this one).
	pageCount, sectionCount, err = repo.UpsertFigmaPagesAndSections(ctx, f.FileKey, pageRows, sectionRows, subtreesBySection, seenAt)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("upsert pages+sections: %w", err)
	}

	// Plan 2026-05-17-002 U2 — bridge each freshly-upserted section into
	// the sub_product / sub_flow taxonomy (mig 0036). Idempotent on
	// re-runs: U1's upserts return the existing row on UNIQUE collision,
	// and LinkSubFlowToFigmaSection is a no-op when the binding is
	// already in place. Logged at WARN — a single bad section name must
	// not abort the rest of the file's autosync.
	for _, sec := range sectionRows {
		if _, sfErr := repo.UpsertSubFlowFromSection(ctx, f.FileKey, sec.PageID, sec.SectionID, sec.Name); sfErr != nil {
			logger.Warn("figma_inventory: sub_flow upsert failed",
				"file", f.FileKey, "page", sec.PageID, "section", sec.SectionID,
				"name", sec.Name, "err", sfErr.Error())
		}
	}

	// nodeCount carries the total number of descendants persisted to the
	// section blobs — UpdateFigmaFileDeepSynced still records it on
	// figma_file.node_count for poll-cycle telemetry.
	nodeCount = 0
	for _, sub := range subtreesBySection {
		nodeCount += len(sub)
	}

	// Mark the file synced for BOTH pages and deep trees in one update —
	// they came from the same response so they share the same version.
	if err := repo.UpdateFigmaFilePagesSynced(ctx, projects.FigmaFileRow{
		FileKey:           f.FileKey,
		Name:              resp.Name,
		ThumbnailURL:      resp.ThumbnailURL,
		LastModified:      parseAPITime(resp.LastModified),
		Version:           resp.Version,
		EditorType:        resp.EditorType,
		LinkAccess:        resp.LinkAccess,
		Role:              resp.Role,
		BranchOfFileKey:   resp.MainFileKey,
		PagesLastSyncedAt: seenAt,
		PagesSyncVersion:  resp.Version,
	}); err != nil {
		return pageCount, sectionCount, nodeCount, fmt.Errorf("mark pages-synced: %w", err)
	}
	if err := repo.UpdateFigmaFileDeepSynced(ctx, f.FileKey, resp.Version, nodeCount, seenAt); err != nil {
		return pageCount, sectionCount, nodeCount, fmt.Errorf("mark deep-synced: %w", err)
	}
	return pageCount, sectionCount, nodeCount, nil
}

// parseAPITime accepts the Figma API's lastModified format. The API uses
// RFC3339 with timezone "Z" (UTC). Returns zero time on parse failure
// rather than failing the row.
func parseAPITime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t
	}
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

// errSummary returns a short error string suitable for the last_crawl_error
// column. APIError bodies are sometimes multi-line JSON; collapse those.
func errSummary(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 1000 {
		s = s[:1000] + "...(truncated)"
	}
	return s
}
