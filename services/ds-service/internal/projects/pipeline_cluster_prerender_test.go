package projects

// pipeline_cluster_prerender_test.go — unit tests for U1+U2+U3 of plan
// 2026-05-06-003. Locks in:
//   • LookupAsset gate inversion (DB error skips to on-demand)
//   • Per-node panic recovery (process must not crash on malformed source)
//   • Phase 2 ctx-cancel bail (no thousands of context-cancelled spawns)
//   • Per-version dedup (Acquire/Release helpers)
//   • walkClusters depth + accumulator bounds (adversarial canonical_tree)
//   • ExtractClusterIDs visibility / removed pruning
//   • PrerenderRepo compile-time satisfaction (covered by `var _` decl;
//     tests would fail to compile if the interface drifted from the fake)
//
// Phase 1 (AssetExporter chunked render) is exercised with deps.AssetExporter
// nil — the chunk loop is bypassed cleanly. Phase 1 chunk behavior is
// covered by asset_export_test.go's existing fetchImagesWithRetry tests
// plus the integration smoke in /tmp/full_pipeline_test.mjs cited in
// commit b9b4377.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ─── Helpers ───────────────────────────────────────────────────────────────

// fakePrerenderRepo implements PrerenderRepo for table-driven tests.
// Per-method behavior is configured via function fields so tests can
// inject canned responses without standing up a real *TenantRepo.
type fakePrerenderRepo struct {
	mu sync.Mutex

	getAnyLeafFn      func(ctx context.Context, versionID string) (string, error)
	getVersionIndexFn func(ctx context.Context, versionID string) (int, error)
	storeAssetFn      func(ctx context.Context, row AssetCacheRow) error
	lookupAssetFn     func(ctx context.Context, tenantID, fileID, nodeID, format string, scale, versionIndex int) (AssetCacheRow, bool, error)

	storedRows []AssetCacheRow // captured by storeAssetFn default impl
}

func (f *fakePrerenderRepo) GetAnyLeafIDForVersion(ctx context.Context, versionID string) (string, error) {
	if f.getAnyLeafFn != nil {
		return f.getAnyLeafFn(ctx, versionID)
	}
	return "leaf-" + versionID, nil
}

func (f *fakePrerenderRepo) GetVersionIndex(ctx context.Context, versionID string) (int, error) {
	if f.getVersionIndexFn != nil {
		return f.getVersionIndexFn(ctx, versionID)
	}
	return 1, nil
}

func (f *fakePrerenderRepo) StoreAsset(ctx context.Context, row AssetCacheRow) error {
	if f.storeAssetFn != nil {
		return f.storeAssetFn(ctx, row)
	}
	f.mu.Lock()
	f.storedRows = append(f.storedRows, row)
	f.mu.Unlock()
	return nil
}

func (f *fakePrerenderRepo) LookupAsset(ctx context.Context, tenantID, fileID, nodeID, format string, scale, versionIndex int) (AssetCacheRow, bool, error) {
	if f.lookupAssetFn != nil {
		return f.lookupAssetFn(ctx, tenantID, fileID, nodeID, format, scale, versionIndex)
	}
	// Default: report cached so the gate lets the goroutine proceed to
	// RenderPreviewPyramid. Tests that want the skip-path override.
	return AssetCacheRow{}, true, nil
}

// stubPreviewSource implements PreviewSourceFetcher with configurable
// behavior. Mirrors the stubSource pattern from asset_preview_pyramid_test.go
// but lives here so adding new behaviors (panicOnNode, errorOnNode) doesn't
// pollute the pyramid test file.
type stubPreviewSource struct {
	mu sync.Mutex

	pngBytes        []byte
	err             error
	panicOnNodeID   string             // if nodeID matches, panic
	errorPerNode    map[string]error   // per-node error override
	delayPerNode    map[string]time.Duration
	calls           []string
	callCount       atomic.Int64
}

func (s *stubPreviewSource) FetchPreviewSource(ctx context.Context, _, _, nodeID string) ([]byte, error) {
	s.callCount.Add(1)
	s.mu.Lock()
	s.calls = append(s.calls, nodeID)
	s.mu.Unlock()
	if s.panicOnNodeID != "" && nodeID == s.panicOnNodeID {
		panic("stubPreviewSource: forced panic for " + nodeID)
	}
	if d, ok := s.delayPerNode[nodeID]; ok {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(d):
		}
	}
	if e, ok := s.errorPerNode[nodeID]; ok {
		return nil, e
	}
	if s.err != nil {
		return nil, s.err
	}
	return s.pngBytes, nil
}

// makeTinyPNG returns a 4×4 grey PNG suitable as a pyramid source.
// Any size ≥ the smallest tier (128) is fine — the generator downsamples
// from whatever it gets.
func makeTinyPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 256, 256))
	for y := 0; y < 256; y++ {
		for x := 0; x < 256; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: 128, G: 128, B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode: %v", err)
	}
	return buf.Bytes()
}

// newTestGenerator builds a PreviewPyramidGenerator backed by the given
// stub source against a temp dir. Used as a building block for
// PrerenderClusters tests that need a real PreviewPyramidGenerator (the
// type is concrete, not an interface, so we can't fake it directly).
func newTestGenerator(t *testing.T, src PreviewSourceFetcher) *PreviewPyramidGenerator {
	t.Helper()
	return &PreviewPyramidGenerator{
		Source:  src,
		DataDir: t.TempDir(),
		Now:     func() time.Time { return time.Date(2026, 5, 7, 0, 0, 0, 0, time.UTC) },
	}
}

// ─── ExtractClusterIDs / walkClusters ──────────────────────────────────────

func TestExtractClusterIDs_NilJSON_ReturnsNil(t *testing.T) {
	if got := ExtractClusterIDs(nil); got != nil {
		t.Fatalf("want nil, got %v", got)
	}
}

func TestExtractClusterIDs_MalformedJSON_ReturnsNil(t *testing.T) {
	if got := ExtractClusterIDs([]byte("{not json")); got != nil {
		t.Fatalf("want nil, got %v", got)
	}
}

func TestExtractClusterIDs_DocumentEnvelope_Unwraps(t *testing.T) {
	tree := `{"document":{"id":"root","type":"FRAME","children":[
		{"id":"icon-1","type":"VECTOR","name":"shape"}
	]}}`
	got := ExtractClusterIDs([]byte(tree))
	if len(got) != 1 || got[0] != "icon-1" {
		t.Fatalf("want [icon-1], got %v", got)
	}
}

func TestExtractClusterIDs_HiddenNode_Skipped(t *testing.T) {
	tree := `{"id":"root","type":"FRAME","children":[
		{"id":"hidden","type":"VECTOR","visible":false},
		{"id":"shown","type":"VECTOR"}
	]}`
	got := ExtractClusterIDs([]byte(tree))
	if len(got) != 1 || got[0] != "shown" {
		t.Fatalf("want [shown], got %v", got)
	}
}

func TestExtractClusterIDs_RemovedNode_Skipped(t *testing.T) {
	tree := `{"id":"root","type":"FRAME","children":[
		{"id":"gone","type":"VECTOR","removed":true},
		{"id":"alive","type":"VECTOR"}
	]}`
	got := ExtractClusterIDs([]byte(tree))
	if len(got) != 1 || got[0] != "alive" {
		t.Fatalf("want [alive], got %v", got)
	}
}

func TestExtractClusterIDs_DepthBound_StopsAtMaxDepth(t *testing.T) {
	// Build a pathological tree: walkClusterMaxDepth + 100 levels deep,
	// with a VECTOR cluster sitting at the very bottom. The walk should
	// early-return at the depth cap and never see the bottom cluster.
	type node struct {
		ID       string
		Type     string
		Children []node
	}
	leaf := node{ID: "deepest-cluster", Type: "VECTOR"}
	cur := leaf
	for i := 0; i < walkClusterMaxDepth+100; i++ {
		cur = node{ID: fmt.Sprintf("wrap-%d", i), Type: "FRAME", Children: []node{cur}}
	}
	bs, err := json.Marshal(cur)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := ExtractClusterIDs(bs)
	for _, id := range got {
		if id == "deepest-cluster" {
			t.Fatalf("walk descended past depth cap %d (returned %d clusters)", walkClusterMaxDepth, len(got))
		}
	}
}

func TestExtractClusterIDs_AccumulatorCap_StopsAtMaxLen(t *testing.T) {
	// Build a flat tree with walkClusterMaxAccLen + 100 cluster siblings.
	// Each child is an INSTANCE named like an icon-size-variant
	// ("icon_24px") so isCluster triggers via pureSizeVariantPattern at
	// the child level — bypassing the vector-only-subtree heuristic from
	// commit 7b7a40b that would otherwise collapse all-vector children
	// into one cluster at the wrapper level.
	children := make([]map[string]any, 0, walkClusterMaxAccLen+100)
	for i := 0; i < walkClusterMaxAccLen+100; i++ {
		children = append(children, map[string]any{
			"id":   fmt.Sprintf("ic%d", i),
			"type": "INSTANCE",
			"name": fmt.Sprintf("icon_%dpx", 24),
		})
	}
	tree := map[string]any{
		"id":       "root",
		"type":     "FRAME",
		"name":     "Container",
		"children": children,
	}
	bs, err := json.Marshal(tree)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := ExtractClusterIDs(bs)
	if len(got) > walkClusterMaxAccLen {
		t.Fatalf("acc exceeded cap: got %d, cap %d", len(got), walkClusterMaxAccLen)
	}
	// Each child checks the cap at the TOP of walkClusters. After the
	// cap is hit, subsequent children early-return without appending.
	// We should land at exactly walkClusterMaxAccLen.
	if len(got) != walkClusterMaxAccLen {
		t.Fatalf("acc did not land at cap: got %d, cap %d", len(got), walkClusterMaxAccLen)
	}
}

// ─── AcquirePrerenderSlot / ReleasePrerenderSlot ───────────────────────────

func TestAcquirePrerenderSlot_FirstCallTrue_SecondCallFalse(t *testing.T) {
	v := "test-version-acquire-1"
	defer ReleasePrerenderSlot(v) // cleanup even on test failure

	if !AcquirePrerenderSlot(v) {
		t.Fatal("first acquire should return true")
	}
	if AcquirePrerenderSlot(v) {
		t.Fatal("second acquire of same versionID should return false")
	}
}

func TestAcquirePrerenderSlot_AfterRelease_CanReacquire(t *testing.T) {
	v := "test-version-acquire-2"

	if !AcquirePrerenderSlot(v) {
		t.Fatal("first acquire should return true")
	}
	ReleasePrerenderSlot(v)
	if !AcquirePrerenderSlot(v) {
		t.Fatal("acquire after release should return true")
	}
	ReleasePrerenderSlot(v)
}

func TestAcquirePrerenderSlot_DistinctVersions_Independent(t *testing.T) {
	a, b := "test-version-acquire-3a", "test-version-acquire-3b"
	defer ReleasePrerenderSlot(a)
	defer ReleasePrerenderSlot(b)

	if !AcquirePrerenderSlot(a) {
		t.Fatal("acquire a should succeed")
	}
	if !AcquirePrerenderSlot(b) {
		t.Fatal("acquire b should succeed (distinct versionID)")
	}
}

// ─── PrerenderClusters validation ──────────────────────────────────────────

func TestPrerenderClusters_EmptyClusterIDs_ReturnsZero(t *testing.T) {
	deps := ClusterPrerenderDeps{
		Repo:           &fakePrerenderRepo{},
		PreviewPyramid: newTestGenerator(t, &stubPreviewSource{pngBytes: makeTinyPNG(t)}),
	}
	n, err := PrerenderClusters(context.Background(), nil, deps, PipelineInputs{VersionID: "v1"}, nil, DefaultClusterPrerenderConfig)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if n != 0 {
		t.Fatalf("want 0, got %d", n)
	}
}

func TestPrerenderClusters_NilPreviewPyramid_ReturnsError(t *testing.T) {
	deps := ClusterPrerenderDeps{
		Repo: &fakePrerenderRepo{},
		// PreviewPyramid intentionally nil
	}
	_, err := PrerenderClusters(context.Background(), nil, deps, PipelineInputs{VersionID: "v1"}, []string{"node-1"}, DefaultClusterPrerenderConfig)
	if err == nil || !strings.Contains(err.Error(), "PreviewPyramid") {
		t.Fatalf("want PreviewPyramid error, got %v", err)
	}
}

func TestPrerenderClusters_NilRepo_ReturnsError(t *testing.T) {
	deps := ClusterPrerenderDeps{
		PreviewPyramid: newTestGenerator(t, &stubPreviewSource{pngBytes: makeTinyPNG(t)}),
		// Repo intentionally nil
	}
	_, err := PrerenderClusters(context.Background(), nil, deps, PipelineInputs{VersionID: "v1"}, []string{"node-1"}, DefaultClusterPrerenderConfig)
	if err == nil || !strings.Contains(err.Error(), "Repo") {
		t.Fatalf("want Repo error, got %v", err)
	}
}

func TestPrerenderClusters_GetAnyLeafIDFails_ReturnsError(t *testing.T) {
	repo := &fakePrerenderRepo{
		getAnyLeafFn: func(_ context.Context, _ string) (string, error) {
			return "", errors.New("db blew up")
		},
	}
	deps := ClusterPrerenderDeps{
		Repo:           repo,
		PreviewPyramid: newTestGenerator(t, &stubPreviewSource{pngBytes: makeTinyPNG(t)}),
	}
	_, err := PrerenderClusters(context.Background(), nil, deps, PipelineInputs{VersionID: "v1"}, []string{"node-1"}, DefaultClusterPrerenderConfig)
	if err == nil || !strings.Contains(err.Error(), "any leaf for version") {
		t.Fatalf("want any-leaf error, got %v", err)
	}
}

func TestPrerenderClusters_NoLeafFound_ReturnsError(t *testing.T) {
	repo := &fakePrerenderRepo{
		getAnyLeafFn: func(_ context.Context, _ string) (string, error) {
			return "", nil // empty string + nil error
		},
	}
	deps := ClusterPrerenderDeps{
		Repo:           repo,
		PreviewPyramid: newTestGenerator(t, &stubPreviewSource{pngBytes: makeTinyPNG(t)}),
	}
	_, err := PrerenderClusters(context.Background(), nil, deps, PipelineInputs{VersionID: "v1"}, []string{"node-1"}, DefaultClusterPrerenderConfig)
	if err == nil || !strings.Contains(err.Error(), "no leaf found") {
		t.Fatalf("want no-leaf error, got %v", err)
	}
}

func TestPrerenderClusters_GetVersionIndexFails_ReturnsError(t *testing.T) {
	repo := &fakePrerenderRepo{
		getVersionIndexFn: func(_ context.Context, _ string) (int, error) {
			return 0, errors.New("idx blew up")
		},
	}
	deps := ClusterPrerenderDeps{
		Repo:           repo,
		PreviewPyramid: newTestGenerator(t, &stubPreviewSource{pngBytes: makeTinyPNG(t)}),
	}
	_, err := PrerenderClusters(context.Background(), nil, deps, PipelineInputs{VersionID: "v1"}, []string{"node-1"}, DefaultClusterPrerenderConfig)
	if err == nil || !strings.Contains(err.Error(), "version_index") {
		t.Fatalf("want version_index error, got %v", err)
	}
}

// ─── PrerenderClusters Phase 2 behavior ────────────────────────────────────

// Regression for U1 finding #3: the LookupAsset gate must skip on
// (cached=false, lerr=nil) AND on (cached=anything, lerr!=nil) — both
// indicate the source PNG is not safely cached, so the on-demand path
// should handle it. Pre-fix, lerr!=nil fell through to RenderPreviewPyramid,
// which is exactly the per-node Figma 429 cascade the comment promised
// to avoid.
func TestPrerenderClusters_LookupAssetMiss_SkipsRender(t *testing.T) {
	src := &stubPreviewSource{pngBytes: makeTinyPNG(t)}
	repo := &fakePrerenderRepo{
		lookupAssetFn: func(_ context.Context, _, _, _, _ string, _, _ int) (AssetCacheRow, bool, error) {
			return AssetCacheRow{}, false, nil // miss with no error
		},
	}
	deps := ClusterPrerenderDeps{
		Repo:           repo,
		PreviewPyramid: newTestGenerator(t, src),
	}
	n, err := PrerenderClusters(context.Background(), nil, deps, PipelineInputs{VersionID: "v1"}, []string{"node-1"}, DefaultClusterPrerenderConfig)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if n != 0 {
		t.Fatalf("want 0 rendered (gate skipped), got %d", n)
	}
	if got := src.callCount.Load(); got != 0 {
		t.Fatalf("FetchPreviewSource should not have been called (gate skipped); got %d calls", got)
	}
}

func TestPrerenderClusters_LookupAssetError_SkipsRender(t *testing.T) {
	src := &stubPreviewSource{pngBytes: makeTinyPNG(t)}
	repo := &fakePrerenderRepo{
		lookupAssetFn: func(_ context.Context, _, _, _, _ string, _, _ int) (AssetCacheRow, bool, error) {
			return AssetCacheRow{}, true, errors.New("sqlite contention") // error path
		},
	}
	deps := ClusterPrerenderDeps{
		Repo:           repo,
		PreviewPyramid: newTestGenerator(t, src),
	}
	n, err := PrerenderClusters(context.Background(), nil, deps, PipelineInputs{VersionID: "v1"}, []string{"node-1"}, DefaultClusterPrerenderConfig)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if n != 0 {
		t.Fatalf("want 0 rendered (gate skipped on DB error), got %d", n)
	}
	if got := src.callCount.Load(); got != 0 {
		t.Fatalf("FetchPreviewSource should not have been called on DB error path; got %d calls", got)
	}
}

// Regression for U1 finding #1: a panic inside RenderPreviewPyramid
// (here triggered via the source) must be recovered by the per-node
// goroutine's defer. Without recovery, the panic crashes the entire
// ds-service process. Test passes if PrerenderClusters returns at all
// (no crash) and the panicked node is counted as failed.
func TestPrerenderClusters_PerNodePanic_RecoversAndCounts(t *testing.T) {
	src := &stubPreviewSource{
		pngBytes:      makeTinyPNG(t),
		panicOnNodeID: "node-bomb",
	}
	repo := &fakePrerenderRepo{}
	deps := ClusterPrerenderDeps{
		Repo:           repo,
		PreviewPyramid: newTestGenerator(t, src),
	}
	n, err := PrerenderClusters(
		context.Background(), nil, deps,
		PipelineInputs{TenantID: "t", FileID: "f", VersionID: "v1"},
		[]string{"node-bomb", "node-ok"},
		DefaultClusterPrerenderConfig,
	)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	// node-ok should render (non-panic path), node-bomb should be counted
	// as failed via the recover. Total successful renders = 1.
	if n != 1 {
		t.Fatalf("want 1 rendered (node-ok), got %d", n)
	}
}

// Regression for the Phase 2 dispatch-loop ctx.Err() bail. With ctx
// already cancelled, the loop must break at the top and never spawn
// goroutines — so the source is never called.
func TestPrerenderClusters_CtxCanceledBeforeDispatch_NoSpawn(t *testing.T) {
	src := &stubPreviewSource{pngBytes: makeTinyPNG(t)}
	repo := &fakePrerenderRepo{}
	deps := ClusterPrerenderDeps{
		Repo:           repo,
		PreviewPyramid: newTestGenerator(t, src),
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel

	n, err := PrerenderClusters(ctx, nil, deps,
		PipelineInputs{VersionID: "v1"},
		[]string{"a", "b", "c", "d", "e"},
		DefaultClusterPrerenderConfig,
	)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if n != 0 {
		t.Fatalf("want 0 rendered (ctx cancelled before dispatch), got %d", n)
	}
	if got := src.callCount.Load(); got != 0 {
		t.Fatalf("FetchPreviewSource should not have been called; got %d", got)
	}
}

// Happy path: 3 distinct cluster IDs, all render, all 4 tiers persist.
// Validates the full goroutine choreography (sem, WaitGroup, atomic
// counters, StoreAsset write loop) end-to-end with an in-memory repo.
func TestPrerenderClusters_HappyPath_AllTiersPersisted(t *testing.T) {
	src := &stubPreviewSource{pngBytes: makeTinyPNG(t)}
	repo := &fakePrerenderRepo{}
	deps := ClusterPrerenderDeps{
		Repo:           repo,
		PreviewPyramid: newTestGenerator(t, src),
	}
	clusters := []string{"icon-1", "icon-2", "icon-3"}
	n, err := PrerenderClusters(
		context.Background(), nil, deps,
		PipelineInputs{TenantID: "t", FileID: "f", VersionID: "v1"},
		clusters,
		DefaultClusterPrerenderConfig,
	)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if n != len(clusters) {
		t.Fatalf("want %d rendered, got %d", len(clusters), n)
	}
	if got := src.callCount.Load(); got != int64(len(clusters)) {
		t.Fatalf("want %d FetchPreviewSource calls, got %d", len(clusters), got)
	}
	// 3 clusters × 4 tiers = 12 asset_cache rows.
	repo.mu.Lock()
	rowCount := len(repo.storedRows)
	repo.mu.Unlock()
	if rowCount != len(clusters)*len(AllPreviewTiers) {
		t.Fatalf("want %d rows (3 clusters × %d tiers), got %d",
			len(clusters)*len(AllPreviewTiers), len(AllPreviewTiers), rowCount)
	}
}

// Regression for U1: the type-assert at the AssetExporter clone site
// must fail loud when deps.Repo is not *TenantRepo. Pre-fix it silently
// fell through, leaving expCopy.Repo pointing at the server-wide
// AssetExporter's Repo (constructed at boot with tenantID="").
func TestPrerenderClusters_AssetExporterWithFakeRepo_FailsLoud(t *testing.T) {
	src := &stubPreviewSource{pngBytes: makeTinyPNG(t)}
	deps := ClusterPrerenderDeps{
		Repo:           &fakePrerenderRepo{}, // NOT *TenantRepo
		PreviewPyramid: newTestGenerator(t, src),
		AssetExporter:  &AssetExporter{}, // non-nil triggers the type-assert path
	}
	_, err := PrerenderClusters(
		context.Background(), nil, deps,
		PipelineInputs{TenantID: "t", FileID: "f", VersionID: "v1"},
		[]string{"node-1"},
		DefaultClusterPrerenderConfig,
	)
	if err == nil || !strings.Contains(err.Error(), "*TenantRepo") {
		t.Fatalf("want *TenantRepo error, got %v", err)
	}
}

// ─── isCluster: chart-name fast path + relaxed budget ─────────────────────
//
// Test fixture builders. Each "shape leaf" is a VECTOR child with no
// children; each "text leaf" is a TEXT child with no children. The
// outer FRAME carries the bbox and name we want to test.

func chartFixture(name string, w, h, shapes, texts int) string {
	var children []string
	for i := 0; i < shapes; i++ {
		children = append(children, fmt.Sprintf(`{"id":"s%d","type":"VECTOR"}`, i))
	}
	for i := 0; i < texts; i++ {
		children = append(children, fmt.Sprintf(`{"id":"t%d","type":"TEXT"}`, i))
	}
	return fmt.Sprintf(
		`{"id":"wrapper","type":"FRAME","name":%q,"absoluteBoundingBox":{"width":%d,"height":%d},"children":[%s]}`,
		name, w, h, strings.Join(children, ","),
	)
}

func TestIsCluster_ChartNameFastPath_AtChartSize_Clusters(t *testing.T) {
	// 19 shapes, 23 texts — pre-fix budget rejected this. Chart-named
	// wrapper at chart size should now pass via the fast path.
	tree := chartFixture("Stock Chart", 375, 403, 19, 23)
	ids := ExtractClusterIDs([]byte(tree))
	if len(ids) != 1 || ids[0] != "wrapper" {
		t.Fatalf("expected [wrapper], got %v", ids)
	}
}

func TestIsCluster_ChartNameFastPath_AtPhoneSize_DoesNotCluster(t *testing.T) {
	// "Quick Buy: indstock chart" matches CHART_NAME_RE but the wrapper
	// is the whole 375×1687 phone screen — must NOT cluster as one PNG.
	// Walker descends to children; with all leaf VECTORs as siblings,
	// each is a shape primitive and individually clusters.
	tree := chartFixture("Quick Buy: indstock chart", 375, 1687, 19, 23)
	ids := ExtractClusterIDs([]byte(tree))
	for _, id := range ids {
		if id == "wrapper" {
			t.Fatalf("did not expect phone-sized wrapper to cluster, got %v", ids)
		}
	}
}

func TestIsCluster_RelaxedBudget_ChartShape_Clusters(t *testing.T) {
	// Wrapper not chart-named, but shape/text ratio is chart-shaped
	// (19 shapes : 23 texts ≈ 121% text/shape) — within the 150% budget.
	tree := chartFixture("Frame 2147226503", 375, 403, 19, 23)
	ids := ExtractClusterIDs([]byte(tree))
	if len(ids) != 1 || ids[0] != "wrapper" {
		t.Fatalf("expected [wrapper] via relaxed budget, got %v", ids)
	}
}

func TestIsCluster_RelaxedBudget_FormShape_DoesNotCluster(t *testing.T) {
	// 5 shapes (input borders) : 30 texts (labels + placeholders + helper
	// copy) = 600% text/shape — way past the 150% budget. Login forms
	// must NOT cluster.
	tree := chartFixture("Login Form", 375, 500, 5, 30)
	ids := ExtractClusterIDs([]byte(tree))
	for _, id := range ids {
		if id == "wrapper" {
			t.Fatalf("did not expect form-shaped wrapper to cluster, got %v", ids)
		}
	}
}

func TestIsCluster_SizeCeiling_RejectsOversizedWrapper(t *testing.T) {
	// 19 shapes : 23 texts (would qualify by ratio) but height is 700,
	// over the 600 ceiling. Should NOT cluster as one PNG.
	tree := chartFixture("Big Chart-ish", 380, 700, 19, 23)
	ids := ExtractClusterIDs([]byte(tree))
	for _, id := range ids {
		if id == "wrapper" {
			t.Fatalf("did not expect oversize wrapper to cluster, got %v", ids)
		}
	}
}

func TestIsCluster_NoBBox_ChartNameFastPath_FallsThrough(t *testing.T) {
	// The chart-name fast path requires a bbox to verify chart-sizedness.
	// Without one we cannot blind-cluster (could be a phone-screen
	// labelled with "chart"). Single-VECTOR child without bbox doesn't
	// reach the >=8 shape budget either, so this wrapper does not
	// cluster and the walker recurses to the inner shape.
	tree := `{"id":"wrapper","type":"FRAME","name":"Stock Chart","children":[
		{"id":"v","type":"VECTOR"}
	]}`
	ids := ExtractClusterIDs([]byte(tree))
	for _, id := range ids {
		if id == "wrapper" {
			t.Fatalf("did not expect bbox-less wrapper to cluster via chart-name fast path, got %v", ids)
		}
	}
}

func TestIsCluster_NoBBox_BudgetPath_StillClustersOnRatio(t *testing.T) {
	// Pre-fix parity: a wrapper without bbox but with shape-heavy
	// children (8 vectors, 0 text) should still cluster. The size
	// guard only kicks in when bbox IS present and exceeds limits.
	// This protects canonical-tree fixtures that omit bbox.
	var children []string
	for i := 0; i < 8; i++ {
		children = append(children, fmt.Sprintf(`{"id":"v%d","type":"VECTOR"}`, i))
	}
	tree := fmt.Sprintf(`{"id":"wrapper","type":"INSTANCE","name":"Generic","children":[%s]}`,
		strings.Join(children, ","))
	ids := ExtractClusterIDs([]byte(tree))
	if len(ids) != 1 || ids[0] != "wrapper" {
		t.Fatalf("expected bbox-less 8-vector wrapper to cluster on ratio, got %v", ids)
	}
}

func TestIsCluster_ExistingIconPath_StillClusters(t *testing.T) {
	// Regression: the icon-name fast path (Icons/.../Help) must still
	// fire regardless of size and budget logic.
	tree := `{"id":"icn","type":"INSTANCE","name":"Icons/ 2D/ Help",
		"absoluteBoundingBox":{"width":24,"height":24},
		"children":[{"id":"v","type":"VECTOR"}]}`
	ids := ExtractClusterIDs([]byte(tree))
	if len(ids) != 1 || ids[0] != "icn" {
		t.Fatalf("expected icon to cluster via name pattern, got %v", ids)
	}
}

// ─── isCluster: autolayout-descendant guard (2026-05-09) ──────────────────
//
// Production cases from the May 9 fix:
//   - Gold/Silver index screens — chart line + autolayout pills row.
//     Pre-fix the entire 375×556 phone screen rasterized as one PNG;
//     designers couldn't click time-frame pills.
//   - Top-N ETF list cards — autolayout VERTICAL list of autolayout
//     HORIZONTAL rows. Pre-fix the whole list rasterized; designers
//     couldn't click an individual row.
//
// The TS-side mirror in app/atlas/_lib/leafcanvas-v2/icon-cluster-resolver.ts
// has parallel tests in __tests__/isIconCluster-autolayout-guard.vitest.ts.

func TestIsCluster_AutolayoutDescendant_ChartScreen_DoesNotCluster(t *testing.T) {
	// 20 chart-line vectors + a 7-pill autolayout-horizontal row. Without
	// the autolayout guard, the leaf-count heuristic (shapes=20, texts=7,
	// budget=max(4,30)=30, 7<=30) would cluster the whole screen.
	var chartShapes []string
	for i := 0; i < 20; i++ {
		chartShapes = append(chartShapes, fmt.Sprintf(`{"id":"c%d","type":"VECTOR"}`, i))
	}
	var pillTexts []string
	for i, label := range []string{"1D", "1W", "1M", "3M", "1Y", "3Y", "5Y"} {
		pillTexts = append(pillTexts, fmt.Sprintf(`{"id":"p%d","type":"TEXT","characters":%q}`, i, label))
	}
	// Use a name that does NOT trigger chartNamePattern — production
	// screens like Gold/Silver index ship as anonymous "Frame 2147228505"
	// numbered nodes (the user has no naming convention for them).
	tree := fmt.Sprintf(`{
		"id":"screen","type":"FRAME","name":"Frame 2147228505",
		"absoluteBoundingBox":{"width":375,"height":556},
		"children":[
			{"id":"chart-line","type":"GROUP","children":[%s]},
			{"id":"pills","type":"FRAME","layoutMode":"HORIZONTAL","children":[%s]}
		]
	}`, strings.Join(chartShapes, ","), strings.Join(pillTexts, ","))
	ids := ExtractClusterIDs([]byte(tree))
	for _, id := range ids {
		if id == "screen" {
			t.Fatalf("did not expect chart screen with autolayout pills to cluster, got %v", ids)
		}
	}
}

func TestIsCluster_AutolayoutDescendant_ListCard_DoesNotCluster(t *testing.T) {
	// 4-row list, each row autolayout-horizontal with icon + 2 texts.
	// Without the guard the budget would cluster (shapes=8 from the icons,
	// texts=8, budget=12, 8<=12).
	row := func(i int) string {
		return fmt.Sprintf(`{
			"id":"row-%d","type":"FRAME","layoutMode":"HORIZONTAL","children":[
				{"id":"icon-%d","type":"GROUP","children":[
					{"id":"vi-%d-1","type":"VECTOR"},
					{"id":"vi-%d-2","type":"VECTOR"}
				]},
				{"id":"name-%d","type":"TEXT","characters":"Fund"},
				{"id":"price-%d","type":"TEXT","characters":"₹25"}
			]}`, i, i, i, i, i, i)
	}
	rows := []string{row(0), row(1), row(2), row(3)}
	tree := fmt.Sprintf(`{
		"id":"list","type":"FRAME","name":"Top Gold ETFs","layoutMode":"VERTICAL",
		"absoluteBoundingBox":{"width":343,"height":373},
		"children":[%s]
	}`, strings.Join(rows, ","))
	ids := ExtractClusterIDs([]byte(tree))
	for _, id := range ids {
		if id == "list" {
			t.Fatalf("did not expect list card with autolayout rows to cluster as a whole, got %v", ids)
		}
	}
}

func TestIsCluster_AutolayoutDescendant_PureIllustration_StillClusters(t *testing.T) {
	// Pure vault-with-coins illustration GROUP — many vectors, no
	// autolayout anywhere. Should still cluster (regression guard for
	// FD Upswing-style content).
	var shapes []string
	for i := 0; i < 20; i++ {
		shapes = append(shapes, fmt.Sprintf(`{"id":"v%d","type":"VECTOR"}`, i))
	}
	tree := fmt.Sprintf(`{
		"id":"wrap","type":"GROUP","name":"Group 1321319461","children":[
			{"id":"inner","type":"GROUP","children":[%s]}
		]
	}`, strings.Join(shapes, ","))
	ids := ExtractClusterIDs([]byte(tree))
	if len(ids) != 1 || ids[0] != "wrap" {
		t.Fatalf("expected pure-illustration wrapper to still cluster, got %v", ids)
	}
}

func TestIsCluster_AutolayoutDescendant_NamedChart_StillClusters(t *testing.T) {
	// chartNamePattern fast-path runs BEFORE the autolayout guard, so an
	// explicitly chart-named wrapper at chart size still clusters even
	// if its subtree contains autolayout (pills inside a chart-named
	// frame). This preserves the explicit designer intent.
	tree := `{
		"id":"chart","type":"FRAME","name":"Stock Chart",
		"absoluteBoundingBox":{"width":375,"height":403},
		"children":[
			{"id":"line","type":"VECTOR"},
			{"id":"pills","type":"FRAME","layoutMode":"HORIZONTAL","children":[
				{"id":"p1","type":"TEXT","characters":"1D"}
			]}
		]
	}`
	ids := ExtractClusterIDs([]byte(tree))
	if len(ids) != 1 || ids[0] != "chart" {
		t.Fatalf("expected chart-named wrapper to cluster despite autolayout descendant, got %v", ids)
	}
}
