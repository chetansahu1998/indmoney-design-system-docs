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
// (rasterized as a single PNG) when seen at the top of isCluster.
// Mirrors node-classifier.ts kind="shape". RECTANGLE is intentionally
// NOT in this set: a standalone RECTANGLE renders as a themable <div>
// (button background, status bar, surface tile) so live text overlay
// + theme tinting + hover states keep working.
var clusterShapeTypes = map[string]struct{}{
	"VECTOR":            {},
	"ELLIPSE":           {},
	"LINE":              {},
	"BOOLEAN_OPERATION": {},
	"STAR":              {},
	"POLYGON":           {},
	"REGULAR_POLYGON":   {},
}

// vectorLeafShapeTypes — superset used ONLY inside vectorLeafSummary's
// recursive walk. Adds RECTANGLE (and ELLIPSE — already in
// clusterShapeTypes but listed here for explicit intent) so that an
// illustration frame containing a mix of RECTANGLE backgrounds plus
// VECTOR/GROUP candle shapes qualifies as a vector-only subtree and
// the WHOLE FRAME clusters as one PNG. Without RECTANGLE here, a
// chart-Frame with one image-fill RECTANGLE behind 200 vectors fails
// the predicate, walker descends, and each inner GROUP becomes its
// own /v1/images call — driving Figma into 429 cascade.
//
// Real example from canonical_tree of indstocks-performant-trade-screen-equity:
//
//   FRAME 'Frame 1321320970' [375x236]   <- the chart container
//     RECTANGLE 'ChatGPT Image…' [628x628]   image-fill backdrop
//     ELLIPSE  …                              decorative
//     RECTANGLE × 3                          decorative tiles
//     GROUP    …
//       GROUP 'Group 1321319495' kids=90    90 individual candle vectors
//       GROUP 'Group 1321319491' kids=90    90 individual candle vectors
//       …
//
// With RECTANGLE in vectorLeafShapeTypes, the chart Frame qualifies as
// a single cluster — one Figma render instead of ~200.
var vectorLeafShapeTypes = map[string]struct{}{
	"VECTOR":            {},
	"ELLIPSE":           {},
	"LINE":              {},
	"BOOLEAN_OPERATION": {},
	"STAR":              {},
	"POLYGON":           {},
	"REGULAR_POLYGON":   {},
	"RECTANGLE":         {},
}

// Name patterns from node-classifier.ts. Anchored so a cluster hidden in
// a deeper path (e.g. "Some Container/Icons/...") still matches when the
// inner segment leads. Use \s* to tolerate Figma-style spacing in path
// separators ("Icons/ 2D/ Help" — note the spaces after "/").
var (
	iconNamePattern         = regexp.MustCompile(`(?i)(?:^|/)\s*(?:icons?|illustrations?)\s*/`)
	yesNoVariantPattern     = regexp.MustCompile(`(?i)/(?:yes|no|on|off)/\d+\s*px\s*$`)
	pureSizeVariantPattern  = regexp.MustCompile(`(?i)^[\w\s\-]+_\d+px$`)
	// chartNamePattern matches wrappers whose name signals a data-viz
	// region: stock charts, sparklines, trend lines, candlestick views.
	// Used as a fast-path: when matched AND the wrapper is chart-sized
	// (see clusterMaxWidth/Height), the wrapper rasterizes as a single
	// PNG instead of fragmenting into per-vector placeholders.
	chartNamePattern = regexp.MustCompile(`(?i)(?:^|[\s/:_-])(?:chart|graph|trend|sparkline|candlesticks?|plot)(?:[\s/:_-]|$)`)
	containerWrapperTypes   = map[string]struct{}{
		"INSTANCE":  {},
		"FRAME":     {},
		"COMPONENT": {},
		"GROUP":     {},
	}
)

// Cluster size ceiling. Wrappers larger than this are full-screen
// containers (status bars, phone screens, dashboards) — clustering them
// as one PNG would lose autolayout structure and prevent atomic-child
// inspection. The ceiling deliberately exceeds typical chart bounds
// (~375×400) but stays under common screen heights (≥667).
const (
	clusterMaxWidth  = 400
	clusterMaxHeight = 600
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
	// Skip the screen-root and start walking from its children. Mirrors
	// TS-side `collectClusterIDs` in
	// app/atlas/_lib/leafcanvas-v2/useIconClusterURLs.ts, which
	// intentionally never returns the root as a cluster — the renderer
	// treats it as a container (LeafFrameRenderer passes keyHint="root"
	// which the cluster check in nodeToHTML.ts skips).
	//
	// Pre-2026-05-10 walkClusters was called on root directly with depth=0.
	// For chart-named screens at chart-bounded size (e.g. "Quick Buy:
	// indstock chart" at 375×429), the chart-name fast path matched on
	// the root → entire screen clustered as ONE PNG. Stage 9 wrote that
	// PNG. The renderer then descended past the root looking for INNER
	// clusters (Battery, Wifi, icons, etc.) — none of which existed in
	// asset_cache because Stage 9 stopped at the root. Result: a wall
	// of broken `<img>` placeholders with alt-text labels across every
	// Gold/Silver index, Quick Buy chart, and similar chart-named screen.
	//
	// Walking from children matches what the canvas actually asks for.
	out := make([]string, 0, 32)
	if m, ok := root.(map[string]any); ok {
		children, _ := m["children"].([]any)
		for _, c := range children {
			walkClusters(c, &out, 1)
		}
	}
	return out
}

// ClusterCandidate is the enriched output of ExtractClustersWithSVGFlag —
// the cluster's node ID plus an SVG-eligibility verdict for routing into
// the SVG vs raster render branch in Stage 9. SkipReasons echoes the
// IsSVGEligible blocklist hits so the operator log + asset_cache row can
// cite WHY a cluster fell back to raster (image fill, blur, blend mode,
// etc).
type ClusterCandidate struct {
	ID           string
	SVGEligible  bool
	SkipReasons []string
}

// ExtractClustersWithSVGFlag walks the canonical_tree blob exactly like
// ExtractClusterIDs but, on each cluster discovery, also evaluates the
// SVG eligibility blocklist against the cluster's subtree. The split-by-
// flag result lets Stage 9 dispatch SVG-eligible clusters to a single
// /v1/images?format=svg call — vector-faithful at any zoom — and route
// the remainder through the existing pyramid path.
func ExtractClustersWithSVGFlag(canonicalTreeJSON []byte) []ClusterCandidate {
	if len(canonicalTreeJSON) == 0 {
		return nil
	}
	var root any
	if err := json.Unmarshal(canonicalTreeJSON, &root); err != nil {
		return nil
	}
	if m, ok := root.(map[string]any); ok {
		if doc, hasDoc := m["document"]; hasDoc {
			root = doc
		}
	}
	// Skip the screen-root — see comment on ExtractClusterIDs for why.
	// TS-side `collectClusterIDs` never returns the root; mirroring that
	// here keeps Stage 9's cluster set aligned with what the canvas
	// actually requests.
	out := make([]ClusterCandidate, 0, 32)
	if m, ok := root.(map[string]any); ok {
		children, _ := m["children"].([]any)
		for _, c := range children {
			walkClustersWithSVGFlag(c, &out, 1)
		}
	}
	return out
}

// walkClustersWithSVGFlag mirrors walkClusters but emits a
// ClusterCandidate per cluster instead of just an ID. On cluster
// discovery the function evaluates IsSVGEligible-equivalent rules
// against the cluster's subtree (the same map[string]any node we
// already hold), so we don't pay for a second JSON unmarshal per
// cluster.
func walkClustersWithSVGFlag(node any, acc *[]ClusterCandidate, depth int) {
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
		// Evaluate SVG eligibility on the cluster's subtree by reusing
		// the in-memory map. We piggyback on a shared SVGEligibility
		// state walker (same blocklist as IsSVGEligible) without
		// re-parsing JSON.
		res := SVGEligibility{OK: true}
		walkSVGEligible(node, &res, 0)
		// U8 — name-aware short-circuit. When the designer named the
		// frame `illustration/...` or `icon/...` (iconNamePattern), they
		// are explicitly asserting "this is a vector group." Override
		// the structural eligibility blocklist and force SVGEligible=
		// true. Mirrors the doctrine threaded through node-classifier.ts
		// on the client and the MCP plan's KTD-4 ("designer naming is
		// canonical, server does not filter, infer, or guess"). The
		// SVG export at Stage 9.1 may still fail (the frame might
		// genuinely have an IMAGE fill), in which case U7's renderer
		// silently falls back to PNG via R5.
		if name != "" && iconNamePattern.MatchString(name) {
			res.OK = true
			res.Reasons = nil
		}
		*acc = append(*acc, ClusterCandidate{
			ID:          id,
			SVGEligible: res.OK,
			SkipReasons: res.Reasons,
		})
		// Cluster encompasses its subtree — do not descend further.
		return
	}
	if children, ok := m["children"].([]any); ok {
		for _, c := range children {
			walkClustersWithSVGFlag(c, acc, depth+1)
		}
	}
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
	// Chart-name fast path. A wrapper whose name signals a data-viz
	// region clusters as one PNG even when its text-to-shape ratio
	// would otherwise reject it (axis ticks, value labels, legends —
	// charts have lots of legitimate inline text). Gated on size to
	// prevent clustering full phone screens whose name happens to
	// contain "chart" (e.g., "Quick Buy: indstock chart" wraps the
	// whole 375×1687 screen — that must NOT cluster as one PNG, but
	// the inner ~375×400 chart frame inside it should). The fast
	// path requires a bbox: if one isn't present we cannot verify
	// chart-sizedness, so we fall through to the existing rules.
	if chartNamePattern.MatchString(name) && hasBBoxWithinClusterSize(node) {
		return true
	}
	// Autolayout-descendant guard. Mirror of TS-side
	// `hasAutolayoutDescendant` in icon-cluster-resolver.ts. When the
	// subtree contains FRAME/INSTANCE/COMPONENT children with non-NONE
	// `layoutMode`, that's a strong signal of "interactive UI containers
	// nested inside" — clustering past that point freezes structure
	// designers expect to be selectable into pixels.
	//
	// Production cases (2026-05-09):
	//   - Gold/Silver index screens: time-frame pills (1D/1W/1M/...) are
	//     an autolayout horizontal FRAME below the chart line. Pre-fix
	//     the whole 375×556 screen rasterized as one PNG.
	//   - Top-N ETF list cards: each row is an autolayout HORIZONTAL
	//     frame; the list container an autolayout VERTICAL. Pre-fix the
	//     entire list rasterized; designers couldn't click a row to
	//     inspect overrides.
	//
	// Named illustrations / charts still cluster — name regexes ran above
	// already. This guard only fires for the structural-heuristic
	// fallback that catches anonymous wrappers.
	if hasAutolayoutDescendant(node) {
		return false
	}
	// No-text fast path — parity with TS-side `isIconCluster` in
	// app/atlas/_lib/leafcanvas-v2/icon-cluster-resolver.ts. A wrapper
	// whose subtree has zero TEXT nodes and at least one shape leaf
	// clusters as one PNG/SVG even when shapeLeaves < 8.
	//
	// Pre-2026-05-09 audit (plutus-equity-tracking screen a89b7fb8): 64
	// new-heuristic clusters per screen, 0 in cache after Stage 9
	// because most were single-shape INSTANCE wrappers like "Federal
	// Bank" (1 RECTANGLE child). TS-side classified them as clusters and
	// requested /assets/I11784:81383;... URLs Go never wrote — the
	// canvas painted broken `<img>` placeholders with alt-text labels.
	//
	// Adding this fast path brings Go in lockstep with TS so every URL
	// the renderer asks for has a matching pre-rendered asset.
	//
	// FRAME-size guard: refuse the fast path for FRAMEs that exceed the
	// cluster-size ceiling. This mirrors TS-side's early
	// `if (t === "FRAME" && exceedsClusterSize(node)) return false`
	// guard at the top of isIconCluster, which keeps phone-sized
	// vector-illustration screens (e.g. NRI VKYC's 375×812 illustration
	// step) from rasterizing as one giant PNG when they should be
	// recursed into and rendered atomically.
	if !hasTextSubtree(node) && hasShapeSubtree(node) {
		if nodeType == "FRAME" && exceedsClusterSize(node) {
			return false
		}
		return true
	}
	// Illustration-subtree heuristic. Cluster the wrapper as a whole
	// if it's a chart-sized region whose shape/text mix looks like a
	// data-viz or illustration.
	//
	// Pre-fix the budget was max(2, shapeLeaves/20) (5% text). That's
	// calibrated for icons/illustrations where text is incidental
	// ("BUY"/"SELL" stamps). Real charts run 100%+ text-to-shape (every
	// vector candle has a numeric tick, every legend dot has a label),
	// so the old budget locked them out. Bumping to 1.5× shapes covers
	// chart cases without inviting login-form-style UI surfaces (those
	// run 200%+ text/shape).
	//
	// Three guards work together:
	//   - shapeLeaves >= 8: avoids clustering tiny icon sets.
	//   - !exceedsClusterSize: keeps phone-sized FRAMEs out (only when
	//     bbox is present — bbox-less wrappers fall through to the
	//     budget check, preserving pre-fix behaviour for canonical-tree
	//     fixtures that omit bbox).
	//   - textLeaves <= max(4, shapeLeaves*3/2): tolerates up to 150%
	//     text-per-shape (chart-shaped) but not 200%+ (form-shaped).
	if exceedsClusterSize(node) {
		return false
	}
	shapeLeaves, textLeaves, valid := illustrationSubtreeSummary(node)
	if !valid {
		return false
	}
	if shapeLeaves < 8 {
		return false
	}
	textBudget := shapeLeaves * 3 / 2
	if textBudget < 4 {
		textBudget = 4
	}
	return textLeaves <= textBudget
}

// hasAutolayoutDescendant reports whether the subtree contains any
// FRAME/INSTANCE/COMPONENT child with a non-NONE `layoutMode`. Used as
// a guard against clustering wrappers that mix illustration shapes
// with interactive UI containers (chart screens with time-frame pills,
// list cards with autolayout rows). See isCluster's call site for the
// production cases that motivated the check, and TS-side parity in
// app/atlas/_lib/leafcanvas-v2/icon-cluster-resolver.ts.
//
// Excludes the wrapper being classified — only descendants count, so a
// wrapper that itself has layoutMode set but contains no inner
// autolayout subtrees (rare but possible for illustration-style auto-
// layout) still reaches the leaf-count heuristic.
func hasAutolayoutDescendant(node map[string]any) bool {
	if node == nil {
		return false
	}
	children, _ := node["children"].([]any)
	for _, c := range children {
		cm, ok := c.(map[string]any)
		if !ok {
			continue
		}
		if isAutolayoutFrame(cm) {
			return true
		}
		if hasAutolayoutDescendant(cm) {
			return true
		}
	}
	return false
}

// hasTextSubtree reports whether the subtree rooted at `node` (inclusive)
// contains a TEXT node. Used by the no-text fast path in isCluster to
// match TS-side `hasTextDescendant` in icon-cluster-resolver.ts.
func hasTextSubtree(node map[string]any) bool {
	if node == nil {
		return false
	}
	if t, _ := node["type"].(string); t == "TEXT" {
		return true
	}
	children, _ := node["children"].([]any)
	for _, c := range children {
		cm, ok := c.(map[string]any)
		if !ok {
			continue
		}
		if hasTextSubtree(cm) {
			return true
		}
	}
	return false
}

// hasShapeSubtree reports whether the subtree rooted at `node` (inclusive)
// contains any shape leaf using the same vectorLeafShapeTypes set the
// illustration-subtree summary uses. Mirrors TS-side hasShapeDescendant.
func hasShapeSubtree(node map[string]any) bool {
	if node == nil {
		return false
	}
	if t, _ := node["type"].(string); t != "" {
		if _, isShape := vectorLeafShapeTypes[t]; isShape {
			return true
		}
	}
	children, _ := node["children"].([]any)
	for _, c := range children {
		cm, ok := c.(map[string]any)
		if !ok {
			continue
		}
		if hasShapeSubtree(cm) {
			return true
		}
	}
	return false
}

func isAutolayoutFrame(n map[string]any) bool {
	if n == nil {
		return false
	}
	t, _ := n["type"].(string)
	if t != "FRAME" && t != "INSTANCE" && t != "COMPONENT" {
		return false
	}
	lm, _ := n["layoutMode"].(string)
	return lm == "HORIZONTAL" || lm == "VERTICAL"
}

// hasBBoxWithinClusterSize requires a present bbox AND that the bbox
// fits the cluster size ceiling. Used by the chart-name fast path —
// without a bbox we can't verify the wrapper is chart-sized so we must
// not blind-cluster.
func hasBBoxWithinClusterSize(node map[string]any) bool {
	w, h, ok := bboxDims(node)
	if !ok {
		return false
	}
	return w <= clusterMaxWidth && h <= clusterMaxHeight
}

// exceedsClusterSize is true ONLY when a bbox is present and it goes
// over the ceiling. Returns false for missing/zero bboxes — the budget
// path then decides on its own. This preserves pre-fix behaviour for
// canonical-tree nodes that lack absoluteBoundingBox.
func exceedsClusterSize(node map[string]any) bool {
	w, h, ok := bboxDims(node)
	if !ok {
		return false
	}
	return w > clusterMaxWidth || h > clusterMaxHeight
}

func bboxDims(node map[string]any) (w, h float64, ok bool) {
	bbox, isMap := node["absoluteBoundingBox"].(map[string]any)
	if !isMap {
		return 0, 0, false
	}
	w, _ = bbox["width"].(float64)
	h, _ = bbox["height"].(float64)
	if w <= 0 || h <= 0 {
		return 0, 0, false
	}
	return w, h, true
}

// illustrationSubtreeSummary counts shape leaves (VECTOR/ELLIPSE/etc
// + RECTANGLE) and TEXT leaves separately within a subtree. A "leaf"
// is a node with no children. Returns (shapeLeaves, textLeaves, valid).
//
// Caller decides clustering policy based on the ratio. Currently:
// shapeLeaves >= 8 AND textLeaves <= 5% of shapeLeaves → cluster as
// whole illustration. This is permissive enough to absorb chart
// labels ("SELL", "BUY", "FLASH", "TRADING") embedded in candlestick
// illustrations, while still excluding interactive UI surfaces (phone
// screens with form fields, status bars with time text — those have
// text ratios well above 5%).
//
// `valid` is false when the subtree contains an unknown node type;
// caller should not cluster in that case.
//
// Skips invisible / removed nodes — they don't contribute to the
// rendered output.
func illustrationSubtreeSummary(node map[string]any) (shapes, texts int, valid bool) {
	if node == nil {
		return 0, 0, false
	}
	// Apply visibility filter on the wrapper itself.
	if v, has := node["visible"].(bool); has && !v {
		return 0, 0, true
	}
	if r, has := node["removed"].(bool); has && r {
		return 0, 0, true
	}
	children, _ := node["children"].([]any)
	if len(children) == 0 {
		nt, _ := node["type"].(string)
		// vectorLeafShapeTypes is the SUPERSET of clusterShapeTypes
		// that also includes RECTANGLE — a RECTANGLE-leaf inside an
		// illustration subtree counts as a vector for clustering
		// purposes (it's just a filled box, no live interactivity).
		// At the top of isCluster the standalone-RECTANGLE case is
		// excluded by clusterShapeTypes intentionally.
		if _, isShape := vectorLeafShapeTypes[nt]; isShape {
			return 1, 0, true
		}
		if nt == "TEXT" {
			return 0, 1, true
		}
		// Empty wrappers (no children, not a shape, not text) —
		// degenerate case; treat as zero leaves but valid=false to
		// keep the parent from clustering on empty content alone.
		return 0, 0, false
	}
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
		// Direct shape leaves count toward shapes.
		if _, isShape := vectorLeafShapeTypes[ct]; isShape {
			shapes++
			continue
		}
		// TEXT leaves count toward texts (no recurse — TEXT can't
		// have shape children that matter for this heuristic).
		if ct == "TEXT" {
			texts++
			continue
		}
		// Wrapper child — recurse and accumulate.
		if _, isWrapper := containerWrapperTypes[ct]; isWrapper {
			cs, ct2, cv := illustrationSubtreeSummary(cm)
			if !cv {
				return 0, 0, false
			}
			shapes += cs
			texts += ct2
			continue
		}
		// Unknown type — be conservative, treat as invalid.
		return 0, 0, false
	}
	return shapes, texts, true
}

// renderSVGClustersForVersion calls AssetExporter.RenderAssetsForLeaf
// with format="svg" for the given cluster IDs, chunked at the same
// AssetExportChunkSize the raster path uses. Persists asset_cache rows
// with format="svg" so the asset-stream handler's cache-hit fast path
// finds them later. Returns (renderedCount, firstError) — partial
// failures are surfaced as renderedCount < len(ids) without aborting
// the rest.
//
// Phase 2.1 — see svg_eligibility.go for the per-cluster filter and
// pipeline.go Stage 9 for the call site.
func renderSVGClustersForVersion(
	ctx context.Context,
	exporter *AssetExporter,
	in PipelineInputs,
	svgIDs []string,
) (int, error) {
	if exporter == nil || len(svgIDs) == 0 {
		return 0, nil
	}
	// Tenant-scope a copy of the exporter — same trick PrerenderClusters
	// uses for Phase 1. The shared base AssetExporter is constructed at
	// boot with tenantID="" because Stage 9 is per-tenant per-version.
	exp := *exporter
	exp.Repo = NewTenantRepo(exporter.Repo.r.db, in.TenantID)

	// Resolve a leafID for this version. SVG render only needs a leaf
	// that belongs to the version (LookupLeafFigmaContext is leaf-scoped
	// for the file_id+version_index lookup).
	repo := exp.Repo
	leafID, err := repo.GetAnyLeafIDForVersion(ctx, in.VersionID)
	if err != nil {
		return 0, fmt.Errorf("svg render: get leaf for version: %w", err)
	}
	if leafID == "" {
		return 0, errors.New("svg render: no leaf found for version")
	}

	const chunk = AssetExportChunkSize // 80 IDs per Figma /v1/images call
	rendered := 0
	var firstErr error
	for i := 0; i < len(svgIDs); i += chunk {
		j := i + chunk
		if j > len(svgIDs) {
			j = len(svgIDs)
		}
		results, err := exp.RenderAssetsForLeaf(
			ctx, in.TenantID, leafID, svgIDs[i:j], "svg", 1)
		// Count actually-persisted node IDs (non-empty NodeID in result).
		for _, r := range results {
			if r.NodeID != "" {
				rendered++
			}
		}
		if err != nil && firstErr == nil {
			firstErr = err
		}
		if ctx.Err() != nil {
			break
		}
	}
	return rendered, firstErr
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

			// Blocklist consult (2026-05-12). If the cluster has hit the
			// failure threshold and is still inside its cooldown window,
			// skip Stage 9 for it — burning the per-PAT rate budget on a
			// known-deterministically-broken Figma node is pure waste.
			// Suppression is silent (doesn't increment failed counter) so
			// it doesn't pollute the prerender summary metrics.
			if _, blocked, berr := deps.Repo.IsFigmaRenderBlocked(rctx, in.FileID, nodeID); berr == nil && blocked {
				return
			}

			pyramidResults, perr := deps.PreviewPyramid.RenderPreviewPyramid(
				rctx, in.TenantID, leafID, in.FileID, nodeID, versionIndex,
			)
			if perr != nil && len(pyramidResults) == 0 {
				failed.Add(1)
				recordRenderErr(nodeID, perr.Error())
				// Blocklist mark — this is a fresh upstream failure for
				// (file, node). The clear_hash is empty here because Stage
				// 9 walks cluster IDs but doesn't have direct visibility
				// into which screen tree they belong to; the pipeline-
				// level integration captures the hash. If a designer
				// touches the file and the pipeline re-runs, the
				// pipeline's hash-clear pass will invalidate this row.
				if _, merr := deps.Repo.MarkFigmaRenderFailure(rctx, in.FileID, nodeID,
					"stage9: "+perr.Error(), ""); merr != nil && log != nil {
					log.Warn("stage9 blocklist mark failed; non-fatal",
						"file_id", in.FileID, "node_id", nodeID, "err", merr)
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
					recordCacheErr(nodeID, fmt.Sprintf("%v", pr.Tier), perr.Error())
					continue
				}
				persistedAny = true
			}
			if persistedAny {
				rendered.Add(1)
				// Blocklist clear — successful render means whatever
				// upstream issue we recorded is resolved. Safe to call
				// even when no row exists (no-op).
				if cerr := deps.Repo.ClearFigmaRenderFailure(rctx, in.FileID, nodeID); cerr != nil && log != nil {
					log.Warn("stage9 blocklist clear failed; non-fatal",
						"file_id", in.FileID, "node_id", nodeID, "err", cerr)
				}
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
	// figma_render_blocklist (2026-05-12) — consult+mark+clear so Stage 9
	// doesn't keep slamming Figma with cluster IDs that deterministically
	// return "no URL for frame X". The pipeline-level integration handles
	// SCREEN frames; Stage 9 walks INNER cluster IDs which often have the
	// same upstream brokenness (chart sparklines, illustration vector
	// groups). Without this, Stage 9 burns the per-PAT rate budget on
	// known-bad clusters every cycle.
	IsFigmaRenderBlocked(ctx context.Context, fileID, nodeID string) (*FigmaRenderBlockEntry, bool, error)
	MarkFigmaRenderFailure(ctx context.Context, fileID, nodeID, errMsg, clearHash string) (*FigmaRenderBlockEntry, error)
	ClearFigmaRenderFailure(ctx context.Context, fileID, nodeID string) error
}

// Compile-time guarantee that *TenantRepo satisfies PrerenderRepo.
// Without this, a future signature change on TenantRepo (renaming a
// method, changing a return type) would break the prerender wiring at
// distance — caught only when the build resolves the type assertion at
// PrerenderClusters:expCopy assignment, which is far from the source
// of the breaking change. With the assertion, the build fails at the
// declaration site of the offending method.
var _ PrerenderRepo = (*TenantRepo)(nil)
