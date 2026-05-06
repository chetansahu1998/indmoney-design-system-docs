// pipeline_cluster_prerender.go — Stage 6.5 of the leaf-import pipeline.
//
// After view_ready ships, walk every screen's canonical_tree, identify icon /
// illustration / shape clusters using the same predicate as
// app/atlas/_lib/leafcanvas-v2/node-classifier.ts, dedupe to unique
// (file_id, node_id) pairs, and render the preview pyramid for each so the
// frontend gets cache-only GETs at leaf-open.
//
// Why this exists: pre-fix, the frontend's useIconClusterURLs minted a
// signed URL per cluster on every leaf-open and the browser raced ~1500-2000
// concurrent /v1/projects/<slug>/assets/<node> GETs. With browser HTTP/1.1's
// 6-conn-per-origin throttle and Figma /v1/images' own concurrency budget,
// most renders timed out or 502'd, leaving illustrations blank. Doing the
// work once at pipeline time, sequenced under our existing
// `figmaProxyLimiter` token bucket, lets the canvas render every cluster
// from cache. Subsequent leaf-opens are O(N_clusters × 5ms cache lookups).
//
// Runs in a goroutine spawned after Stage 6 commits — view_ready does NOT
// wait on this. Failures are logged but never fail the pipeline; missing
// clusters degrade to the existing on-demand render path (HandleAssetDownload).

package projects

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"sync"
	"sync/atomic"
	"time"
)

// ClusterPrerenderConfig tunes the background render. Concurrency caps
// the goroutine fan-out; the rate-limit lives further down inside
// AssetExporter via figmaProxyLimiter.
type ClusterPrerenderConfig struct {
	// Concurrency is the number of pyramid renders running in parallel.
	// Each pyramid does one Figma /v1/images call; figmaProxyLimiter
	// already caps Figma traffic at ~5 req/sec, so concurrency above that
	// just queues internally. We default to 4 so a flaky render doesn't
	// stall the others.
	Concurrency int
	// Timeout is the per-cluster budget (one Figma render + 4-tier
	// downsample + persist). Bigger than the synchronous request budget
	// because we have no user waiting.
	Timeout time.Duration
	// Phase1MaxConsecutiveFailures bounds the Phase 1 chunk loop's
	// tolerance for sustained Figma 429/5xx. RenderAssetsForLeaf already
	// retries internally 3× per chunk; this catches the case where Figma
	// is genuinely degraded across multiple chunks and we'd otherwise
	// burn the rest of TotalBudget churning through ~50 doomed chunks.
	// Sporadic single-chunk failures don't trip this; only N in a row do.
	Phase1MaxConsecutiveFailures int
	// SampleErrorCap bounds the number of distinct per-cluster Warn
	// lines emitted per Phase 2 run. Beyond this, errors are counted in
	// the failed counter but not individually logged. Final aggregate
	// log includes the captured samples for triage.
	SampleErrorCap int
}

// DefaultClusterPrerenderConfig — sensible defaults for first-class import.
var DefaultClusterPrerenderConfig = ClusterPrerenderConfig{
	Concurrency:                  4,
	Timeout:                      45 * time.Second,
	Phase1MaxConsecutiveFailures: 3,
	SampleErrorCap:               5,
}

// prerenderInFlight gates concurrent Stage 9 invocations on the same
// version_id. Mirrors the audit_jobs idempotency pattern: HandleVersionRetry
// + a quick-fire double-export should not double-spend Figma quota or
// double-write asset_cache rows. Process-global (not Pipeline-scoped)
// because Pipeline is constructed per-tenant per-request via
// pipelineFactory; we need a single source of truth across all pipeline
// instances. Process restart drops the map — that's fine, the on-demand
// path is the recovery mechanism.
//
// Held only for the lifetime of the Stage 9 goroutine (~30 min worst case);
// memory cost is negligible (one struct{} per concurrent prerender).
var prerenderInFlight sync.Map // map[string]struct{} keyed on versionID

// AcquirePrerenderSlot is exposed for tests + the Stage 9 spawn site.
// Returns true if the slot was acquired (caller proceeds and MUST call
// ReleasePrerenderSlot on completion); false if another goroutine is
// already prerendering this version.
func AcquirePrerenderSlot(versionID string) bool {
	_, loaded := prerenderInFlight.LoadOrStore(versionID, struct{}{})
	return !loaded
}

// ReleasePrerenderSlot frees the dedup slot. Safe to call multiple times.
func ReleasePrerenderSlot(versionID string) {
	prerenderInFlight.Delete(versionID)
}

// clusterShapeTypes — Figma node types that are always shape-clusters
// (rasterized as a single PNG). Mirrors node-classifier.ts kind="shape".
var clusterShapeTypes = map[string]struct{}{
	"VECTOR":            {},
	"ELLIPSE":           {},
	"LINE":              {},
	"BOOLEAN_OPERATION": {},
	"STAR":              {},
	"POLYGON":           {},
	"REGULAR_POLYGON":   {},
}

// Name patterns from node-classifier.ts. Anchored so a cluster hidden in
// a deeper path (e.g. "Some Container/Icons/...") still matches when the
// inner segment leads. Use \s* to tolerate Figma-style spacing in path
// separators ("Icons/ 2D/ Help" — note the spaces after "/").
var (
	iconNamePattern         = regexp.MustCompile(`(?i)(?:^|/)\s*(?:icons?|illustrations?)\s*/`)
	yesNoVariantPattern     = regexp.MustCompile(`(?i)/(?:yes|no|on|off)/\d+\s*px\s*$`)
	pureSizeVariantPattern  = regexp.MustCompile(`(?i)^[\w\s\-]+_\d+px$`)
	containerWrapperTypes   = map[string]struct{}{
		"INSTANCE":  {},
		"FRAME":     {},
		"COMPONENT": {},
		"GROUP":     {},
	}
)

// ExtractClusterIDs walks the JSON-encoded canonical_tree blob and returns
// unique node IDs that should be pre-rendered as PNGs. The walk DOES NOT
// descend into a node once it has been classified as a cluster — the whole
// subtree renders as one PNG.
//
// canonicalTreeJSON is the raw bytes from extractCanonicalTree (returned
// alongside the hash before it gets handed to InsertCanonicalTree).
//
// Returns IDs in deterministic walk order so logs read cleanly. De-dup
// happens in the caller (across all screens).
func ExtractClusterIDs(canonicalTreeJSON []byte) []string {
	if len(canonicalTreeJSON) == 0 {
		return nil
	}
	var root any
	if err := json.Unmarshal(canonicalTreeJSON, &root); err != nil {
		return nil
	}
	// Trees come wrapped: { "document": <node> } from the Figma response
	// envelope. Unwrap if present.
	if m, ok := root.(map[string]any); ok {
		if doc, hasDoc := m["document"]; hasDoc {
			root = doc
		}
	}
	out := make([]string, 0, 32)
	walkClusters(root, &out, 0)
	return out
}

// walkClusterMaxDepth caps recursion in walkClusters. Real Figma files
// nest under ~10 levels typical (the UI doesn't surface deeper); 256
// is well above any legitimate input but small enough to bound the
// goroutine stack on adversarial / corrupted canonical_tree blobs.
// Hitting the cap is benign: walkClusters returns early, the affected
// subtree gets under-counted, and HandleAssetDownload's on-demand path
// renders any missed nodes when a user opens the leaf.
const walkClusterMaxDepth = 256

// walkClusterMaxAccLen caps the number of cluster IDs per call. A
// pathologically large tree (50K+ clusters in one screen) would push
// the JSON encoder for the prerender_runs status row past sensible
// limits and dwarf the 30-min budget. Cap and warn — the on-demand
// path is the safety net for over-cap nodes.
const walkClusterMaxAccLen = 50000

// walkClusters recursively walks the canonical_tree node graph and
// accumulates cluster IDs into acc. depth is the call-stack depth;
// returns early at walkClusterMaxDepth. Visibility / removed nodes are
// pruned. Accumulator size is capped at walkClusterMaxAccLen.
func walkClusters(node any, acc *[]string, depth int) {
	if depth > walkClusterMaxDepth {
		return
	}
	if len(*acc) >= walkClusterMaxAccLen {
		return
	}
	m, ok := node.(map[string]any)
	if !ok {
		return
	}
	visible := true
	if v, has := m["visible"].(bool); has && !v {
		visible = false
	}
	if removed, has := m["removed"].(bool); has && removed {
		visible = false
	}
	if !visible {
		return
	}
	id, _ := m["id"].(string)
	nodeType, _ := m["type"].(string)
	name, _ := m["name"].(string)

	if id != "" && isCluster(m, nodeType, name) {
		*acc = append(*acc, id)
		// Cluster encompasses its subtree — do not descend.
		return
	}

	if children, ok := m["children"].([]any); ok {
		for _, c := range children {
			walkClusters(c, acc, depth+1)
		}
	}
}

// isCluster mirrors node-classifier.ts shouldRasterize: true for icon /
// illustration / shape kinds. Pure-shape types (VECTOR, ELLIPSE, etc.)
// always cluster. INSTANCE/FRAME/COMPONENT/GROUP cluster only if their
// name matches an icon or illustration taxonomy pattern. Layout-named
// containers ("Status Bar", "Rounded Rectangle", etc.) do NOT match any
// of these patterns and so correctly fall through to walk-children.
//
// `node` is supplied so we can apply the vector-only-group heuristic:
// a wrapper (FRAME/GROUP/etc.) whose entire descendant subtree is
// vector shapes — like an illustration with 4 stacked vectors — should
// cluster as a single PNG, not get walked into and split into 4
// separate cluster nodes. Without this, an illustration of N vectors
// becomes N separate /v1/images calls AND the canvas renders the
// pieces individually (no shared composition), which the user reported
// as "the black background holds 4 vectors but it's all one group".
func isCluster(node map[string]any, nodeType, name string) bool {
	if nodeType == "" {
		return false
	}
	if _, isShape := clusterShapeTypes[nodeType]; isShape {
		return true
	}
	if _, isWrapper := containerWrapperTypes[nodeType]; !isWrapper {
		return false
	}
	if iconNamePattern.MatchString(name) {
		return true
	}
	if yesNoVariantPattern.MatchString(name) {
		return true
	}
	if pureSizeVariantPattern.MatchString(name) {
		return true
	}
	// Vector-only-subtree heuristic. Cluster the wrapper as a whole if
	// every leaf in its subtree is a vector shape AND there are at
	// least two vector leaves (a single-shape wrapper is just one
	// VECTOR — no need to cluster the wrapper, the inner shape is
	// already a cluster on its own).
	leafCount, allShape := vectorLeafSummary(node)
	if allShape && leafCount >= 2 {
		return true
	}
	return false
}

// vectorLeafSummary counts vector-shape leaves in a subtree and reports
// whether ALL leaves are vector shapes. A "leaf" is a node with no
// children. Returns (leafCount, allLeavesAreVectorShapes).
//
// Skips invisible / removed nodes — they don't contribute to the
// rendered output. Also skips TEXT nodes since they prove the subtree
// isn't pure-vector.
func vectorLeafSummary(node map[string]any) (int, bool) {
	if node == nil {
		return 0, false
	}
	// Apply visibility filter on the wrapper itself.
	if v, has := node["visible"].(bool); has && !v {
		return 0, true
	}
	if r, has := node["removed"].(bool); has && r {
		return 0, true
	}
	children, _ := node["children"].([]any)
	if len(children) == 0 {
		// This IS a leaf. Count it only if it's a vector shape.
		nt, _ := node["type"].(string)
		if _, isShape := clusterShapeTypes[nt]; isShape {
			return 1, true
		}
		// Empty wrappers (no children, not a shape) — degenerate case;
		// treat as zero leaves and false to prevent spurious clustering.
		return 0, false
	}
	total := 0
	allShape := true
	for _, c := range children {
		cm, ok := c.(map[string]any)
		if !ok {
			continue
		}
		// Skip invisible children.
		if v, has := cm["visible"].(bool); has && !v {
			continue
		}
		if r, has := cm["removed"].(bool); has && r {
			continue
		}
		ct, _ := cm["type"].(string)
		// TEXT children prove not-pure-vector immediately.
		if ct == "TEXT" {
			return 0, false
		}
		// Direct shape leaves count toward total.
		if _, isShape := clusterShapeTypes[ct]; isShape {
			total++
			continue
		}
		// Wrapper child — recurse.
		if _, isWrapper := containerWrapperTypes[ct]; isWrapper {
			cn, cAll := vectorLeafSummary(cm)
			if !cAll {
				return 0, false
			}
			total += cn
			continue
		}
		// Unknown type — be conservative, treat as not-pure-vector.
		return 0, false
	}
	return total, allShape
}

// PrerenderClusters drives the per-tenant pyramid render for every
// unique cluster ID across the version's screens. Persists asset_cache
// rows for each successful tier. Failures are logged and skipped — the
// frontend's existing on-demand render path is the safety net.
//
// Returns the number of clusters successfully pre-rendered (any tier
// persisted) so callers can log a summary.
func PrerenderClusters(
	ctx context.Context,
	log *slog.Logger,
	deps ClusterPrerenderDeps,
	in PipelineInputs,
	clusterIDs []string,
	cfg ClusterPrerenderConfig,
) (int, error) {
	if len(clusterIDs) == 0 {
		return 0, nil
	}
	if deps.PreviewPyramid == nil {
		return 0, errors.New("cluster prerender: PreviewPyramid not wired")
	}
	if deps.Repo == nil {
		return 0, errors.New("cluster prerender: Repo not wired")
	}
	if cfg.Concurrency <= 0 {
		cfg = DefaultClusterPrerenderConfig
	}

	// Resolve a leafID for this version. RenderAssetsForLeaf scopes its
	// PAT lookup by leaf, so any flow on this version works.
	leafID, err := deps.Repo.GetAnyLeafIDForVersion(ctx, in.VersionID)
	if err != nil {
		return 0, fmt.Errorf("any leaf for version %s: %w", in.VersionID, err)
	}
	if leafID == "" {
		return 0, errors.New("cluster prerender: no leaf found for version")
	}

	// Resolve version_index — needed for asset_cache primary key.
	versionIndex, err := deps.Repo.GetVersionIndex(ctx, in.VersionID)
	if err != nil {
		return 0, fmt.Errorf("version_index for %s: %w", in.VersionID, err)
	}

	// PHASE 1 — batched source-PNG render. Without this, the per-node
	// RenderPreviewPyramid below would hit Figma /v1/images one ID at a
	// time, immediately blowing through Figma's 5 req/sec rate limit
	// and 429-cascading the rest of the 5-minute budget. RenderAssetsForLeaf
	// chunks at 80 IDs per call (Figma's documented max), so 1000 unique
	// clusters becomes ~12 rate-limited calls. The persisted PNG@scale=2
	// rows then cache-hit during phase 2.
	//
	// phase1Cached: when Phase 1 runs, it captures the set of nodes that
	// successfully reached cache (newly rendered OR pre-cached). Phase 2
	// then gates per-node on this map instead of doing N LookupAsset
	// SQLite reads — saves ~2000 reads on a 4400-cluster import. nil
	// when AssetExporter is unwired (test/embedded path); Phase 2 falls
	// back to LookupAsset in that case.
	var phase1Cached map[string]struct{}
	if deps.AssetExporter != nil {
		exp := deps.AssetExporter
		// AssetExporter requires its Repo to be tenant-scoped (the LookupAsset
		// chain checks tenant_id row-by-row). The pipeline's Repo IS the
		// tenant-scoped TenantRepo, so use it directly via a clone.
		expCopy := *exp
		// `deps.Repo` is the prerender-specific narrow interface; the
		// pipeline-level p.Repo is the actual *TenantRepo. We need it
		// here for the AssetExporter's LookupAsset chain. Fail loud if
		// the concrete type isn't *TenantRepo: silent fallthrough would
		// leave expCopy.Repo pointing at the server-wide AssetExporter's
		// Repo (constructed at boot with tenantID="") and cross-tenant
		// asset_cache writes would become possible under any future
		// repo wrapper or fake.
		tr, isTenantRepo := deps.Repo.(*TenantRepo)
		if !isTenantRepo {
			return 0, errors.New("cluster prerender: deps.Repo must be *TenantRepo (got incompatible type)")
		}
		expCopy.Repo = tr
		if log != nil {
			log.Info("cluster prerender: phase 1 — batched source PNG render",
				"version_id", in.VersionID,
				"clusters", len(clusterIDs),
			)
		}
		// Manual chunking so a single 429 doesn't abort all 4000+ IDs.
		// RenderAssetsForLeaf chunks at 80 internally but if the FIRST
		// chunk's URL fetch errors, it returns before persisting any of
		// the LATER chunks' URLs (urlMap is built lazily). By calling
		// it once per chunk-of-80 here, a 429 only loses that chunk —
		// the remaining ~50 chunks still complete.
		const chunk = AssetExportChunkSize // 80
		// Circuit-breaker on consecutive chunk failures. RenderAssetsForLeaf
		// already does internal 3-attempt 429 backoff, so a chunk-level
		// error here means Figma was sustained-degraded across that retry
		// window. If multiple chunks in a row hit that wall, Figma is
		// genuinely down for this tenant — keep going just burns the
		// remaining 30-min budget on doomed calls. Bail early; on-demand
		// path covers the rest. Threshold is configurable; sporadic
		// single-chunk failures don't trip it.
		maxConsecFails := cfg.Phase1MaxConsecutiveFailures
		if maxConsecFails <= 0 {
			maxConsecFails = DefaultClusterPrerenderConfig.Phase1MaxConsecutiveFailures
		}
		var ok, fail, consecutiveFails int
		var phase1Aborted bool
		phase1Cached = make(map[string]struct{}, len(clusterIDs))
		for i := 0; i < len(clusterIDs); i += chunk {
			j := i + chunk
			if j > len(clusterIDs) {
				j = len(clusterIDs)
			}
			batch := clusterIDs[i:j]
			results, batchErr := expCopy.RenderAssetsForLeaf(ctx, in.TenantID, leafID, batch, "png", 2)
			ok += len(results)
			// Capture every successfully-cached node into phase1Cached so
			// Phase 2 doesn't need to LookupAsset per-node. RenderAssetsForLeaf
			// pre-allocates results to len(batch) and leaves zero-value slots
			// for nodes whose Figma URL was missing or whose byte fetch failed
			// (their NodeID stays empty). Filter on NodeID != "" to capture
			// only successes.
			for _, r := range results {
				if r.NodeID != "" {
					phase1Cached[r.NodeID] = struct{}{}
				}
			}
			if batchErr != nil {
				fail++
				consecutiveFails++
				if log != nil {
					log.Warn("cluster prerender: phase 1 chunk failed",
						"version_id", in.VersionID,
						"chunk_start", i,
						"consecutive_fails", consecutiveFails,
						"err", batchErr.Error(),
					)
				}
				if consecutiveFails >= maxConsecFails {
					if log != nil {
						log.Warn("cluster prerender: phase 1 circuit-breaker tripped — figma sustained-degraded",
							"version_id", in.VersionID,
							"consecutive_fails", consecutiveFails,
							"chunks_completed", i/chunk+1,
							"chunks_total", (len(clusterIDs)+chunk-1)/chunk,
						)
					}
					phase1Aborted = true
					break
				}
				// Continue to next chunk — don't abort the whole batch.
			} else {
				consecutiveFails = 0
			}
			// Bail early if the parent context is dying (30-min budget).
			if ctx.Err() != nil {
				if log != nil {
					log.Warn("cluster prerender: phase 1 ctx done",
						"chunks_completed", i/chunk+1,
						"chunks_total", (len(clusterIDs)+chunk-1)/chunk,
					)
				}
				break
			}
		}
		if log != nil {
			log.Info("cluster prerender: phase 1 done",
				"version_id", in.VersionID,
				"png_results", ok,
				"failed_chunks", fail,
				"total_chunks", (len(clusterIDs)+chunk-1)/chunk,
				"aborted", phase1Aborted,
			)
		}
	}

	// PHASE 2 — per-node downsample + persist. FetchPreviewSource hits
	// asset_cache for the PNG@scale=2 written in phase 1 (no Figma call),
	// then runs four-tier downsample + four asset_cache writes for tiers
	// 128/512/1024/2048. Concurrency cap is now safe to be higher because
	// no Figma calls happen here.
	var rendered atomic.Int64
	var failed atomic.Int64
	var wg sync.WaitGroup
	sem := make(chan struct{}, cfg.Concurrency)

	// Sampled error logging. ~4400 clusters × 3 Warn sites pre-fix could
	// emit up to ~17,600 Warn lines from a single failed import on
	// degraded Figma. Cap distinct samples per error class; keep all of
	// them for the aggregate log line at the end. Errors past the cap
	// still increment failed.Add(1), they just don't get a per-cluster
	// Warn — the aggregate covers the operator's diagnostic need.
	sampleCap := cfg.SampleErrorCap
	if sampleCap <= 0 {
		sampleCap = DefaultClusterPrerenderConfig.SampleErrorCap
	}
	var sampleMu sync.Mutex
	var renderErrSamples []string
	var cacheErrSamples []string
	var renderErrTotal, cacheErrTotal int64
	recordRenderErr := func(nodeID, errMsg string) {
		sampleMu.Lock()
		defer sampleMu.Unlock()
		renderErrTotal++
		if len(renderErrSamples) < sampleCap {
			renderErrSamples = append(renderErrSamples, fmt.Sprintf("%s: %s", nodeID, errMsg))
			if log != nil {
				log.Warn("cluster prerender: render failed (sampled)",
					"node_id", nodeID,
					"err", errMsg,
					"sample", fmt.Sprintf("%d/%d", len(renderErrSamples), sampleCap),
				)
			}
		}
	}
	recordCacheErr := func(nodeID, tier, errMsg string) {
		sampleMu.Lock()
		defer sampleMu.Unlock()
		cacheErrTotal++
		if len(cacheErrSamples) < sampleCap {
			cacheErrSamples = append(cacheErrSamples, fmt.Sprintf("%s/%s: %s", nodeID, tier, errMsg))
			if log != nil {
				log.Warn("cluster prerender: cache row write failed (sampled)",
					"node_id", nodeID,
					"tier", tier,
					"err", errMsg,
					"sample", fmt.Sprintf("%d/%d", len(cacheErrSamples), sampleCap),
				)
			}
		}
	}

	for _, nodeID := range clusterIDs {
		// Bail out of dispatch if the parent ctx has died. Mirror Phase 1's
		// pattern at the top of the loop so a cancelled parent doesn't queue
		// thousands of context-cancelled goroutines that immediately fail.
		if ctx.Err() != nil {
			if log != nil {
				log.Warn("cluster prerender: phase 2 ctx done before dispatch complete",
					"version_id", in.VersionID,
				)
			}
			break
		}
		nodeID := nodeID // capture
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			// Per-node panic recovery. Without this, any nil-deref or
			// out-of-bounds inside RenderPreviewPyramid (image decode,
			// slice indexing) crashes the entire ds-service process.
			// Recover here, count the cluster as failed, and let the
			// other goroutines continue.
			defer func() {
				if r := recover(); r != nil {
					failed.Add(1)
					if log != nil {
						log.Error("cluster prerender: panic in per-node goroutine",
							"node_id", nodeID,
							"panic", fmt.Sprintf("%v", r),
						)
					}
				}
			}()
			defer func() {
				<-sem
				wg.Done()
			}()
			rctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
			defer cancel()

			// Skip nodes whose source PNG isn't cached. Phase 1 has
			// already run for the whole cluster set; if Phase 1 didn't
			// cache this node (Figma 429, deadline, etc), running
			// RenderPreviewPyramid here would re-fetch via Figma per-node
			// — exactly the 429 cascade we were trying to avoid. Let
			// HandleAssetDownload's on-demand path handle these stragglers
			// when a user opens the leaf.
			//
			// When Phase 1 ran (deps.AssetExporter != nil), gate on the
			// in-memory phase1Cached map captured during Phase 1. Saves
			// a per-node LookupAsset SQLite read (~2000 reads on a
			// 4400-cluster import). When Phase 1 was unwired (test /
			// embedded path), fall back to LookupAsset so callers that
			// bypass Phase 1 but expect Phase 2 to render pyramids from
			// pre-cached source PNGs still work. The DB-error path
			// continues to skip (transient SQLite contention → defer to
			// the on-demand path).
			if phase1Cached != nil {
				if _, ok := phase1Cached[nodeID]; !ok {
					return
				}
			} else {
				if _, cached, lerr := deps.Repo.LookupAsset(rctx, in.TenantID, in.FileID, nodeID, "png", 2, versionIndex); lerr != nil || !cached {
					return
				}
			}

			pyramidResults, perr := deps.PreviewPyramid.RenderPreviewPyramid(
				rctx, in.TenantID, leafID, in.FileID, nodeID, versionIndex,
			)
			if perr != nil && len(pyramidResults) == 0 {
				failed.Add(1)
				recordRenderErr(nodeID, perr.Error())
				return
			}
			// Persist each successfully-rendered tier as an asset_cache row.
			now := deps.PreviewPyramid.now()
			persistedAny := false
			for _, pr := range pyramidResults {
				row := AssetCacheRow{
					TenantID:     in.TenantID,
					FileID:       in.FileID,
					NodeID:       nodeID,
					Format:       pr.Tier.FormatString(),
					Scale:        1,
					VersionIndex: versionIndex,
					StorageKey:   pr.StorageKey,
					Bytes:        pr.Bytes,
					Mime:         pr.Mime,
					CreatedAt:    now,
				}
				if perr := deps.Repo.StoreAsset(rctx, row); perr != nil {
					recordCacheErr(nodeID, fmt.Sprintf("%v", pr.Tier), perr.Error())
					continue
				}
				persistedAny = true
			}
			if persistedAny {
				rendered.Add(1)
			} else {
				failed.Add(1)
			}
		}()
	}
	wg.Wait()
	if log != nil {
		log.Info("cluster prerender: complete",
			"version_id", in.VersionID,
			"file_id", in.FileID,
			"total_clusters", len(clusterIDs),
			"render_err_total", renderErrTotal,
			"render_err_samples", renderErrSamples,
			"cache_err_total", cacheErrTotal,
			"cache_err_samples", cacheErrSamples,
			"rendered", rendered.Load(),
			"failed", failed.Load(),
		)
	}
	return int(rendered.Load()), nil
}

// ClusterPrerenderDeps captures the slice of Pipeline state needed for
// the prerender goroutine. Exposed so tests can build a deps struct
// without standing up the full Pipeline.
type ClusterPrerenderDeps struct {
	Repo           PrerenderRepo
	PreviewPyramid *PreviewPyramidGenerator
	// AssetExporter is used to do ONE big batched source-PNG render
	// up-front (Figma /v1/images accepts up to 80 IDs per call, so
	// 1000 unique clusters becomes ~12 rate-limited calls instead of
	// 1000 single-ID calls hitting Figma's 5 req/sec hard limit).
	// After this batch finishes, RenderPreviewPyramid per-node is a
	// cache-hit for the source PNG and just runs downsample + persist
	// locally. Without this, Stage 9 thrashes Figma with 429s for the
	// entire 5 min budget and only completes ~10% of clusters.
	AssetExporter *AssetExporter
}

// PrerenderRepo is the narrow repo interface the prerender path needs.
// Mirrors the methods on TenantRepo so production wiring just passes the
// repo straight in; tests can provide a fake.
type PrerenderRepo interface {
	GetAnyLeafIDForVersion(ctx context.Context, versionID string) (string, error)
	GetVersionIndex(ctx context.Context, versionID string) (int, error)
	StoreAsset(ctx context.Context, row AssetCacheRow) error
	// LookupAsset checks for an existing cache row. Used by Phase 2 to
	// gate per-node downsampling on whether Phase 1 successfully cached
	// the source PNG — if not, skip Phase 2 for that node and let the
	// on-demand path fill it later (avoids per-node Figma 429 cascade).
	LookupAsset(ctx context.Context, tenantID, fileID, nodeID, format string, scale, versionIndex int) (AssetCacheRow, bool, error)
}

// Compile-time guarantee that *TenantRepo satisfies PrerenderRepo.
// Without this, a future signature change on TenantRepo (renaming a
// method, changing a return type) would break the prerender wiring at
// distance — caught only when the build resolves the type assertion at
// PrerenderClusters:expCopy assignment, which is far from the source
// of the breaking change. With the assertion, the build fails at the
// declaration site of the offending method.
var _ PrerenderRepo = (*TenantRepo)(nil)
