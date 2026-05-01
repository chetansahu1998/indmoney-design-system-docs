package projects

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"
	"sync"
	"time"
)

// Phase 6 — RebuildGraphIndex worker.
//
// The worker materialises graph_index from upstream sources (projects,
// flows, personas, decisions, manifest.json, tokens JSON, canonical-tree
// BLOBs). Two trigger modes:
//
//  1. Incremental — driven by SSE events. The pipeline / handlers call
//     EnqueueIncremental(tenant, platform, sourceKind, sourceRef) when a
//     row changes. Coalesced inside a 200ms debounce window so a burst of
//     decision events doesn't thrash the index.
//
//  2. Full — runs on cold start (boot) and on a 1-hour ticker. Walks every
//     (tenant, platform) slice and rebuilds from sources; the manifest +
//     tokens mtime check decides whether to re-derive component / token
//     rows or short-circuit.
//
// Worker pool size from env (`GRAPH_INDEX_REBUILD_WORKERS`, default 1) per
// Phase 1 learning #6.

// ─── Tunables ────────────────────────────────────────────────────────────────

// GraphRebuildDebounce is the coalesce window for incremental rebuilds. A
// burst of N events for the same (tenant, platform, source_kind, source_ref)
// inside this window flushes once.
const GraphRebuildDebounce = 200 * time.Millisecond

// GraphRebuildSafetyNet is the "did we miss an SSE event" catch-up cadence.
// 1 hour matches the SSE channel's worst-case redelivery + accommodates
// manifest + tokens mtime drift between extraction commits.
const GraphRebuildSafetyNet = 1 * time.Hour

// GraphRebuildStackTraceLimit caps the stack trace stored on a panicked
// flush in structured logs. Mirrors the audit worker's WorkerStackTraceLimit.
const GraphRebuildStackTraceLimit = 8 * 1024

// ─── Public surface ─────────────────────────────────────────────────────────

// GraphRebuildKey identifies a debounced flush. Composite of (tenant,
// platform, source_kind, source_ref); zero source_kind/source_ref values
// degrade to "rebuild the whole tenant slice".
type GraphRebuildKey struct {
	TenantID   string
	Platform   string
	SourceKind GraphSourceKind
	SourceRef  string
}

// GraphRebuildPublisher is the SSE-side surface the worker uses to announce
// that a tenant slice has been re-materialised. Defined as an interface so
// tests can supply a fake.
type GraphRebuildPublisher interface {
	PublishGraphIndexUpdated(tenantID, platform string, materializedAt time.Time)
}

// GraphRebuildSources bundles the file-system source paths the worker reads
// during full rebuilds. Resolved at boot from env / config.
type GraphRebuildSources struct {
	// ManifestPath points at public/icons/glyph/manifest.json relative to the
	// ds-service binary's cwd. Empty disables manifest-derived rows.
	ManifestPath string
	// TokensDir points at lib/tokens/indmoney/ relative to the ds-service
	// binary's cwd. Empty disables token-derived rows.
	TokensDir string
}

// GraphRebuildPool is the worker. Mirrors WorkerPool's lifecycle (Start,
// Wait) but with a debounced job queue instead of audit_jobs lease semantics.
type GraphRebuildPool struct {
	Size int
	DB   *sql.DB
	// TenantIDs is the list of tenants the full-rebuild ticker visits. The
	// pool is process-wide (one per ds-service binary) so it iterates every
	// tenant rather than being per-tenant. Empty → no full rebuilds (tests).
	TenantIDs []string
	Sources   GraphRebuildSources
	Publisher GraphRebuildPublisher
	Log       *slog.Logger

	// Tunables (zero values fall back to package constants).
	Debounce  time.Duration
	SafetyNet time.Duration

	// now is injectable for tests; nil → time.Now.
	now func() time.Time

	// ─── runtime state ──────────────────────────────────────────────────────

	mu      sync.Mutex
	pending map[GraphRebuildKey]time.Time // key → first-enqueued time
	queue   chan GraphRebuildKey
	wg      sync.WaitGroup
}

// Start spawns Size goroutines + a debounce flusher + the safety-net ticker.
// Returns immediately. Workers run until ctx is cancelled.
func (p *GraphRebuildPool) Start(ctx context.Context) error {
	if p.Size <= 0 {
		p.Size = 1
	}
	if p.DB == nil {
		return errors.New("graph_rebuild: DB required")
	}
	if p.Debounce == 0 {
		p.Debounce = GraphRebuildDebounce
	}
	if p.SafetyNet == 0 {
		p.SafetyNet = GraphRebuildSafetyNet
	}
	if p.now == nil {
		p.now = time.Now
	}
	if p.Log == nil {
		p.Log = slog.Default()
	}
	p.pending = map[GraphRebuildKey]time.Time{}
	// Buffer is generous so EnqueueIncremental never blocks under burst
	// (even if every active flow ships a decision in the same second). The
	// debounce queue is the primary backpressure mechanism.
	p.queue = make(chan GraphRebuildKey, 256)

	for i := 0; i < p.Size; i++ {
		p.wg.Add(1)
		go p.runWorker(ctx)
	}
	p.wg.Add(1)
	go p.runDebounceFlusher(ctx)
	p.wg.Add(1)
	go p.runSafetyNet(ctx)

	// Cold start: kick off a full rebuild for every (tenant, platform) so a
	// fresh boot reflects current data. Done synchronously-on-boot so the
	// service doesn't serve a stale index for the first hour.
	for _, tenantID := range p.TenantIDs {
		for _, platform := range []string{GraphPlatformMobile, GraphPlatformWeb} {
			p.EnqueueIncremental(tenantID, platform, "", "")
		}
	}
	return nil
}

// Wait blocks until every goroutine returns. Tests use this to confirm
// graceful shutdown.
func (p *GraphRebuildPool) Wait() { p.wg.Wait() }

// EnqueueIncremental records a (tenant, platform, source_kind, source_ref)
// rebuild request. Empty source_kind + source_ref means "rebuild the whole
// tenant slice" (used by cold start + safety-net ticker).
//
// Non-blocking: drops the enqueue if the pending map is already at the same
// key (the existing entry's timestamp is honored). This dedup is the heart of
// the debounce.
func (p *GraphRebuildPool) EnqueueIncremental(tenantID, platform string, sourceKind GraphSourceKind, sourceRef string) {
	if p == nil {
		return
	}
	if tenantID == "" || platform == "" {
		return
	}
	key := GraphRebuildKey{
		TenantID:   tenantID,
		Platform:   platform,
		SourceKind: sourceKind,
		SourceRef:  sourceRef,
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, exists := p.pending[key]; exists {
		return // already enqueued; debounce flusher will pick it up
	}
	p.pending[key] = p.now()
}

// runDebounceFlusher polls the pending map every 50ms; any entry whose
// first-enqueue timestamp is older than Debounce gets shifted into the
// worker queue. This is the trade-off for not running a per-key timer.
func (p *GraphRebuildPool) runDebounceFlusher(ctx context.Context) {
	defer p.wg.Done()
	tk := time.NewTicker(50 * time.Millisecond)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
		}
		now := p.now()
		p.mu.Lock()
		for key, enqueuedAt := range p.pending {
			if now.Sub(enqueuedAt) < p.Debounce {
				continue
			}
			// Try to write to queue without blocking; if the queue is full
			// we leave the key in pending and try again on the next tick.
			select {
			case p.queue <- key:
				delete(p.pending, key)
			default:
				// queue full; revisit
			}
		}
		p.mu.Unlock()
	}
}

// runSafetyNet periodically re-enqueues a full rebuild per (tenant,
// platform). Catches missed SSE events + manifest/tokens mtime drift.
func (p *GraphRebuildPool) runSafetyNet(ctx context.Context) {
	defer p.wg.Done()
	tk := time.NewTicker(p.SafetyNet)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
		}
		for _, tenantID := range p.TenantIDs {
			for _, platform := range []string{GraphPlatformMobile, GraphPlatformWeb} {
				p.EnqueueIncremental(tenantID, platform, "", "")
			}
		}
	}
}

// runWorker drains the queue. Each job runs through processJob with a
// defer/recover so a panic doesn't bring the pool down.
func (p *GraphRebuildPool) runWorker(ctx context.Context) {
	defer p.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case key := <-p.queue:
			p.processJob(ctx, key)
		}
	}
}

func (p *GraphRebuildPool) processJob(ctx context.Context, key GraphRebuildKey) {
	defer func() {
		if rec := recover(); rec != nil {
			stack := truncateErr(string(debug.Stack()), GraphRebuildStackTraceLimit)
			p.Log.Error("graph_rebuild: panic in processJob",
				"tenant_id", key.TenantID,
				"platform", key.Platform,
				"source_kind", key.SourceKind,
				"source_ref", key.SourceRef,
				"err", fmt.Sprintf("%v", rec),
				"stack", stack)
		}
	}()
	var err error
	if key.SourceKind == "" || key.SourceRef == "" {
		err = p.RebuildFull(ctx, key.TenantID, key.Platform)
	} else {
		err = p.rebuildIncremental(ctx, key)
	}
	if err != nil {
		p.Log.Warn("graph_rebuild: flush failed",
			"tenant_id", key.TenantID,
			"platform", key.Platform,
			"source_kind", key.SourceKind,
			"source_ref", key.SourceRef,
			"err", err.Error())
		return
	}
	if p.Publisher != nil {
		p.Publisher.PublishGraphIndexUpdated(key.TenantID, key.Platform, p.now().UTC())
	}
}

// RebuildFull walks every source for the tenant slice and replaces every
// graph_index row. Idempotent: writes inside a transaction; on commit the
// new state is visible atomically.
//
// Safe to call directly without Start() — defaults are filled lazily so the
// cold-backfill command + tests can use this method without spinning up the
// debounce + safety-net goroutines.
func (p *GraphRebuildPool) RebuildFull(ctx context.Context, tenantID, platform string) error {
	if tenantID == "" || platform == "" {
		return errors.New("graph_rebuild: tenant + platform required")
	}
	if p.now == nil {
		p.now = time.Now
	}
	if p.Log == nil {
		p.Log = slog.Default()
	}
	now := p.now().UTC()
	repo := NewTenantRepo(p.DB, tenantID)

	// 1. Build all rows from sources. We collect per-source slices then
	//    concatenate so failures in one source don't taint the rest.
	var allRows []GraphIndexRow

	prodFolderRows, err := BuildProductFolderRows(ctx, p.DB, tenantID, platform, now)
	if err != nil {
		return fmt.Errorf("product/folder rows: %w", err)
	}
	allRows = append(allRows, prodFolderRows...)

	flowRows, err := BuildFlowRows(ctx, p.DB, tenantID, platform, now)
	if err != nil {
		return fmt.Errorf("flow rows: %w", err)
	}
	// Walk canonical_trees for flow → component edges. On failure the flow
	// rows still write; their edges_uses_json stays empty.
	if err := p.fillFlowComponentEdges(ctx, tenantID, platform, flowRows); err != nil {
		p.Log.Warn("graph_rebuild: flow→component edge derivation failed; flows will have empty edges_uses_json",
			"tenant_id", tenantID, "platform", platform, "err", err.Error())
	}
	allRows = append(allRows, flowRows...)

	personaRows, err := BuildPersonaRows(ctx, p.DB, tenantID, platform, now)
	if err != nil {
		return fmt.Errorf("persona rows: %w", err)
	}
	allRows = append(allRows, personaRows...)

	decisionRows, err := BuildDecisionRows(ctx, p.DB, tenantID, platform, now)
	if err != nil {
		return fmt.Errorf("decision rows: %w", err)
	}
	allRows = append(allRows, decisionRows...)

	// Tokens first — BuildComponentRows needs the variable_id → token_name map.
	tokenRows, varIDToName, err := BuildTokenRows(p.Sources.TokensDir, tenantID, platform, now)
	if err != nil {
		p.Log.Warn("graph_rebuild: tokens disabled",
			"tenant_id", tenantID, "platform", platform, "err", err.Error())
	}
	allRows = append(allRows, tokenRows...)

	componentRows, err := BuildComponentRows(p.Sources.ManifestPath, tenantID, platform, varIDToName, now)
	if err != nil {
		p.Log.Warn("graph_rebuild: components disabled",
			"tenant_id", tenantID, "platform", platform, "err", err.Error())
	}
	allRows = append(allRows, componentRows...)

	// Phase 8 — read search rows BEFORE opening the write tx, so the read
	// queries don't deadlock against SQLite's single-writer transaction
	// holding the only connection (db.go sets MaxOpenConns=1 because WAL
	// allows concurrent READERS but a single in-flight write).
	//
	// We only build search rows on the mobile slice (search is platform-
	// agnostic; entities live once per tenant). Doing it twice would just
	// delete and re-insert with the second platform's call.
	var searchRows []SearchIndexRow
	if platform == GraphPlatformMobile {
		s, sErr := BuildSearchRowsForTenant(ctx, p.DB, tenantID)
		if sErr != nil {
			p.Log.Warn("graph_rebuild: search index population failed",
				"tenant_id", tenantID, "err", sErr.Error())
		} else {
			searchRows = s
		}
	}

	// 2. Replace graph_index for (tenant, platform) atomically.
	tx, err := p.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	if err := repo.DeleteGraphIndexForPlatform(ctx, tx, platform); err != nil {
		return fmt.Errorf("delete prior rows: %w", err)
	}
	if err := repo.UpsertGraphIndexRows(ctx, tx, allRows); err != nil {
		return fmt.Errorf("upsert rows: %w", err)
	}
	if platform == GraphPlatformMobile && searchRows != nil {
		if err := DeleteSearchIndexForTenant(ctx, tx, tenantID); err != nil {
			return fmt.Errorf("delete search rows: %w", err)
		}
		if err := UpsertSearchIndexRows(ctx, tx, tenantID, searchRows); err != nil {
			return fmt.Errorf("upsert search rows: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	p.Log.Info("graph_rebuild: full rebuild complete",
		"tenant_id", tenantID,
		"platform", platform,
		"rows", len(allRows),
		"search_rows", len(searchRows))
	return nil
}

// rebuildIncremental processes a targeted job. For most source kinds we
// re-derive only the affected rows; for the heavyweight (manifest, tokens)
// we fall back to a full rebuild because the dependency graph is too
// tangled to update piecemeal cheaply.
func (p *GraphRebuildPool) rebuildIncremental(ctx context.Context, key GraphRebuildKey) error {
	switch key.SourceKind {
	case GraphSourceManifest, GraphSourceTokens:
		return p.RebuildFull(ctx, key.TenantID, key.Platform)
	}
	// For decisions / flows / projects / personas we re-derive the whole
	// tenant slice anyway — the per-row update is bounded but the join logic
	// (severity rollup, parent chain) requires the full dataset to recompute
	// correctly. This keeps the worker simple. If profiling shows hot SSE
	// bursts we can optimise per-source-kind later.
	return p.RebuildFull(ctx, key.TenantID, key.Platform)
}

// fillFlowComponentEdges walks each flow's canonical_trees and stamps the
// `EdgesUses` slice on the matching row. The variant-id → slug map comes
// from the manifest; missing manifest = no edges (logged but not fatal).
func (p *GraphRebuildPool) fillFlowComponentEdges(ctx context.Context, tenantID, platform string, flowRows []GraphIndexRow) error {
	if p.Sources.ManifestPath == "" {
		return nil
	}
	variantToSlug, err := BuildVariantIDToSlugMap(p.Sources.ManifestPath)
	if err != nil {
		return fmt.Errorf("build variant→slug map: %w", err)
	}
	if len(variantToSlug) == 0 {
		return nil
	}
	return FillFlowComponentEdges(ctx, p.DB, tenantID, platform, variantToSlug, flowRows)
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

// PlatformsAll lists every platform the worker iterates. Exposed for tests +
// the cold-backfill command.
var PlatformsAll = []string{GraphPlatformMobile, GraphPlatformWeb}

// SplitTenantList parses a comma-separated env-var into a tenant ID slice.
// Used by main.go boot wiring.
func SplitTenantList(raw string) []string {
	out := []string{}
	for _, s := range strings.Split(raw, ",") {
		s = strings.TrimSpace(s)
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}
