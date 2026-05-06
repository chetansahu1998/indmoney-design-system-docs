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
}

// DefaultClusterPrerenderConfig — sensible defaults for first-class import.
var DefaultClusterPrerenderConfig = ClusterPrerenderConfig{
	Concurrency: 4,
	Timeout:     45 * time.Second,
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
	walkClusters(root, &out)
	return out
}

func walkClusters(node any, acc *[]string) {
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

	if id != "" && isCluster(nodeType, name) {
		*acc = append(*acc, id)
		// Cluster encompasses its subtree — do not descend.
		return
	}

	if children, ok := m["children"].([]any); ok {
		for _, c := range children {
			walkClusters(c, acc)
		}
	}
}

// isCluster mirrors node-classifier.ts shouldRasterize: true for icon /
// illustration / shape kinds. Pure-shape types (VECTOR, ELLIPSE, etc.)
// always cluster. INSTANCE/FRAME/COMPONENT/GROUP cluster only if their
// name matches an icon or illustration taxonomy pattern. Layout-named
// containers ("Status Bar", "Rounded Rectangle", etc.) do NOT match any
// of these patterns and so correctly fall through to walk-children.
func isCluster(nodeType, name string) bool {
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
	return false
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
	leafID, err := deps.Repo.AnyLeafIDForVersion(ctx, in.VersionID)
	if err != nil {
		return 0, fmt.Errorf("any leaf for version %s: %w", in.VersionID, err)
	}
	if leafID == "" {
		return 0, errors.New("cluster prerender: no leaf found for version")
	}

	// Resolve version_index — needed for asset_cache primary key.
	versionIndex, err := deps.Repo.LookupVersionIndex(ctx, in.VersionID)
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
	if deps.AssetExporter != nil {
		exp := deps.AssetExporter
		// AssetExporter requires its Repo to be tenant-scoped (the LookupAsset
		// chain checks tenant_id row-by-row). The pipeline's Repo IS the
		// tenant-scoped TenantRepo, so use it directly via a clone.
		expCopy := *exp
		// `deps.Repo` is the prerender-specific narrow interface; the
		// pipeline-level p.Repo is the actual *TenantRepo. We need it
		// here. Since ClusterPrerenderDeps was set up with deps.Repo
		// AS the *TenantRepo (concrete type), type-assert.
		if tr, ok := deps.Repo.(*TenantRepo); ok {
			expCopy.Repo = tr
		}
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
		var ok, fail int
		for i := 0; i < len(clusterIDs); i += chunk {
			j := i + chunk
			if j > len(clusterIDs) {
				j = len(clusterIDs)
			}
			batch := clusterIDs[i:j]
			results, batchErr := expCopy.RenderAssetsForLeaf(ctx, in.TenantID, leafID, batch, "png", 2)
			ok += len(results)
			if batchErr != nil {
				fail++
				if log != nil {
					log.Warn("cluster prerender: phase 1 chunk failed",
						"version_id", in.VersionID,
						"chunk_start", i,
						"err", batchErr.Error(),
					)
				}
				// Continue to next chunk — don't abort the whole batch.
			}
			// Bail early if the parent context is dying (5-min budget).
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
	sem := make(chan struct{}, cfg.Concurrency)
	done := make(chan struct{}, len(clusterIDs))

	for _, nodeID := range clusterIDs {
		nodeID := nodeID // capture
		sem <- struct{}{}
		go func() {
			defer func() {
				<-sem
				done <- struct{}{}
			}()
			rctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
			defer cancel()

			// Skip nodes whose source PNG isn't cached. Phase 1 has already
			// run for the whole cluster set; if it didn't cache this node
			// (Figma 429, deadline, etc), running RenderPreviewPyramid here
			// would re-fetch via Figma per-node — exactly the 429 cascade we
			// were trying to avoid. Let HandleAssetDownload's on-demand
			// path handle these stragglers when a user opens the leaf.
			if _, cached, lerr := deps.Repo.LookupAsset(rctx, in.TenantID, in.FileID, nodeID, "png", 2, versionIndex); lerr == nil && !cached {
				return
			}

			pyramidResults, perr := deps.PreviewPyramid.RenderPreviewPyramid(
				rctx, in.TenantID, leafID, in.FileID, nodeID, versionIndex,
			)
			if perr != nil && len(pyramidResults) == 0 {
				failed.Add(1)
				if log != nil {
					log.Warn("cluster prerender: render failed",
						"node_id", nodeID,
						"err", perr.Error(),
					)
				}
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
					if log != nil {
						log.Warn("cluster prerender: cache row write failed",
							"node_id", nodeID,
							"tier", pr.Tier,
							"err", perr.Error(),
						)
					}
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
	for range clusterIDs {
		<-done
	}
	if log != nil {
		log.Info("cluster prerender: complete",
			"version_id", in.VersionID,
			"file_id", in.FileID,
			"total_clusters", len(clusterIDs),
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
	AnyLeafIDForVersion(ctx context.Context, versionID string) (string, error)
	LookupVersionIndex(ctx context.Context, versionID string) (int, error)
	StoreAsset(ctx context.Context, row AssetCacheRow) error
	// LookupAsset checks for an existing cache row. Used by Phase 2 to
	// gate per-node downsampling on whether Phase 1 successfully cached
	// the source PNG — if not, skip Phase 2 for that node and let the
	// on-demand path fill it later (avoids per-node Figma 429 cascade).
	LookupAsset(ctx context.Context, tenantID, fileID, nodeID, format string, scale, versionIndex int) (AssetCacheRow, bool, error)
}
