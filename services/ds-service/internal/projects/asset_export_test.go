package projects

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// U4 — Asset export proxy + cache tests.
//
// Test scenarios per plan:
//   - Happy path cache miss → fetch → store → cache hit on repeat.
//   - Figma 429 with Retry-After: backoff once, succeed.
//   - SVG-not-supported for a node: error with node_id.
//   - version_index part of cache key: re-export under new version invalidates.
//   - Figma 5xx persistent: error; cache not poisoned.
//   - Rate-limit context cancel: context error; no goroutines stuck.

// fakeURLFetcher implements FigmaImageURLFetcher with scriptable responses.
type fakeURLFetcher struct {
	mu         sync.Mutex
	calls      int
	responses  []map[string]string // pop one per call
	errors     []error             // pop one per call
	missing    map[string]bool     // node_ids that never appear in responses
}

func (f *fakeURLFetcher) GetImages(ctx context.Context, fileKey string, nodeIDs []string, format string, scale int) (map[string]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	idx := f.calls - 1
	if idx < len(f.errors) && f.errors[idx] != nil {
		return nil, f.errors[idx]
	}
	out := map[string]string{}
	for _, nid := range nodeIDs {
		if f.missing[nid] {
			continue
		}
		out[nid] = "https://cdn.figma.test/" + fileKey + "/" + strings.ReplaceAll(nid, ":", "_") + "." + format
	}
	if idx < len(f.responses) && f.responses[idx] != nil {
		out = f.responses[idx]
	}
	return out, nil
}

// fakeByteFetcher returns canned bytes for any URL.
type fakeByteFetcher struct {
	mu        sync.Mutex
	calls     int32
	payload   []byte
	failTimes int
	err       error
}

func (f *fakeByteFetcher) Fetch(ctx context.Context, url string) ([]byte, error) {
	atomic.AddInt32(&f.calls, 1)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failTimes > 0 {
		f.failTimes--
		if f.err != nil {
			return nil, f.err
		}
		return nil, errors.New("synthetic byte fetch failure")
	}
	if len(f.payload) == 0 {
		return []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}, nil
	}
	return f.payload, nil
}

// rateLimitErr mimics the figma client's APIError shape (string contains "rate limit"
// or "429" so isRateLimitErr matches).
type rateLimitErr struct {
	retryAfter time.Duration
}

func (e *rateLimitErr) Error() string { return "figma API: 429 rate limit" }

// Implement RetryAfter() so retryAfterFromErr can pick it up.
func (e *rateLimitErr) RetryAfter() time.Duration { return e.retryAfter }

// ─── Setup helpers ──────────────────────────────────────────────────────────

// seedFlowAndVersion creates a project + flow + project_version row so
// LookupLeafFigmaContext returns sane values. Returns (flowID, fileID,
// versionIndex).
func seedFlowAndVersion(t *testing.T, repo *TenantRepo, userID, fileID string) (string, string, int) {
	t.Helper()
	ctx := context.Background()
	p, err := repo.UpsertProject(ctx, Project{
		Name: "TestProj-" + fileID, Platform: "mobile", Product: "Plutus",
		Path: "OB", OwnerUserID: userID, FileID: fileID,
	})
	if err != nil {
		t.Fatalf("upsert project: %v", err)
	}
	flow, err := repo.UpsertFlow(ctx, Flow{
		ProjectID: p.ID,
		FileID:    fileID,
		Name:      "Flow",
	})
	if err != nil {
		t.Fatalf("upsert flow: %v", err)
	}
	v, err := repo.CreateVersion(ctx, p.ID, userID)
	if err != nil {
		t.Fatalf("create version: %v", err)
	}
	return flow.ID, fileID, v.VersionIndex
}

// newAssetExporter wires an AssetExporter with the given fakes against a
// freshly-seeded test DB. Returns (exporter, tenantID, leafID, dataDir).
func newAssetExporter(t *testing.T, urls FigmaImageURLFetcher, bytes AssetByteFetcher) (*AssetExporter, string, string, string) {
	t.Helper()
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	flowID, _, _ := seedFlowAndVersion(t, repo, uA, "FILE-K-"+t.Name())
	dataDir := t.TempDir()
	return &AssetExporter{
		Repo:    repo,
		URLs:    urls,
		Bytes:   bytes,
		DataDir: dataDir,
		Limiter: &figmaRateLimiter{buckets: map[string]*figmaBucket{}},
		Now:     time.Now,
	}, tA, flowID, dataDir
}

// ─── Tests ──────────────────────────────────────────────────────────────────

func TestRenderAssets_HappyPath_CacheMissThenHit(t *testing.T) {
	urls := &fakeURLFetcher{}
	bs := &fakeByteFetcher{payload: []byte("PNG-bytes")}
	exp, tA, leafID, dataDir := newAssetExporter(t, urls, bs)

	ctx := context.Background()
	res, err := exp.RenderAssetsForLeaf(ctx, tA, leafID, []string{"1:2", "3:4"}, "png", 2)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("len = %d, want 2", len(res))
	}
	if res[0].NodeID != "1:2" || res[1].NodeID != "3:4" {
		t.Errorf("order not preserved: %+v", res)
	}
	for _, r := range res {
		if r.Mime != "image/png" {
			t.Errorf("mime %q, want image/png", r.Mime)
		}
		abs := filepath.Join(dataDir, r.StorageKey)
		if _, err := os.Stat(abs); err != nil {
			t.Errorf("file not on disk: %v", err)
		}
	}

	// Second call: should be all cache hits — fakeURLFetcher.calls stays at 1.
	calls0 := urls.calls
	res2, err := exp.RenderAssetsForLeaf(ctx, tA, leafID, []string{"1:2", "3:4"}, "png", 2)
	if err != nil {
		t.Fatalf("render second: %v", err)
	}
	if urls.calls != calls0 {
		t.Errorf("expected zero new URL fetches on cache hit, got %d (was %d)", urls.calls, calls0)
	}
	if res2[0].StorageKey != res[0].StorageKey {
		t.Errorf("storage_key changed across calls: %q vs %q", res[0].StorageKey, res2[0].StorageKey)
	}
}

func TestRenderAssets_429RetryWithRetryAfter(t *testing.T) {
	urls := &fakeURLFetcher{
		errors: []error{
			&rateLimitErr{retryAfter: 10 * time.Millisecond},
			nil, // succeeds on retry
		},
	}
	bs := &fakeByteFetcher{payload: []byte("ok")}
	exp, tA, leafID, _ := newAssetExporter(t, urls, bs)

	res, err := exp.RenderAssetsForLeaf(context.Background(), tA, leafID, []string{"1:2"}, "png", 1)
	if err != nil {
		t.Fatalf("expected success after retry: %v", err)
	}
	if len(res) != 1 || res[0].NodeID != "1:2" {
		t.Errorf("unexpected result: %+v", res)
	}
	if urls.calls != 2 {
		t.Errorf("expected exactly 2 fetch calls (1 fail + 1 success), got %d", urls.calls)
	}
}

func TestRenderAssets_SVGNotSupportedForNode(t *testing.T) {
	urls := &fakeURLFetcher{
		missing: map[string]bool{"99:99": true},
	}
	bs := &fakeByteFetcher{}
	exp, tA, leafID, _ := newAssetExporter(t, urls, bs)

	_, err := exp.RenderAssetsForLeaf(context.Background(), tA, leafID, []string{"99:99"}, "svg", 1)
	if err == nil {
		t.Fatalf("expected error for unrenderable SVG node")
	}
	if !IsAssetExportNodeMissing(err) {
		t.Errorf("expected IsAssetExportNodeMissing, got %v", err)
	}
	// node_id should be in the message.
	if !strings.Contains(err.Error(), "99:99") {
		t.Errorf("error %q missing node_id", err.Error())
	}
}

func TestRenderAssets_VersionIndexInvalidatesCache(t *testing.T) {
	urls := &fakeURLFetcher{}
	bs := &fakeByteFetcher{payload: []byte("v1")}
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	flowID, _, _ := seedFlowAndVersion(t, repo, uA, "FILE-K")
	dataDir := t.TempDir()
	exp := &AssetExporter{
		Repo:    repo,
		URLs:    urls,
		Bytes:   bs,
		DataDir: dataDir,
		Limiter: &figmaRateLimiter{buckets: map[string]*figmaBucket{}},
		Now:     time.Now,
	}

	ctx := context.Background()
	if _, err := exp.RenderAssetsForLeaf(ctx, tA, flowID, []string{"1:2"}, "png", 2); err != nil {
		t.Fatalf("first render: %v", err)
	}
	calls1 := urls.calls

	// Repeat = cache hit.
	if _, err := exp.RenderAssetsForLeaf(ctx, tA, flowID, []string{"1:2"}, "png", 2); err != nil {
		t.Fatalf("cached render: %v", err)
	}
	if urls.calls != calls1 {
		t.Errorf("expected cache hit, got %d new fetches", urls.calls-calls1)
	}

	// New project_version → version_index increments → cache miss again.
	// Find the project_id for our flow and create a new version.
	var projectID string
	if err := d.DB.QueryRowContext(ctx, `SELECT project_id FROM flows WHERE id = ?`, flowID).Scan(&projectID); err != nil {
		t.Fatalf("lookup project_id: %v", err)
	}
	if _, err := repo.CreateVersion(ctx, projectID, uA); err != nil {
		t.Fatalf("create v2: %v", err)
	}
	bs.payload = []byte("v2")
	if _, err := exp.RenderAssetsForLeaf(ctx, tA, flowID, []string{"1:2"}, "png", 2); err != nil {
		t.Fatalf("v2 render: %v", err)
	}
	if urls.calls != calls1+1 {
		t.Errorf("expected one new fetch on version bump, got %d", urls.calls-calls1)
	}
}

func TestRenderAssets_5xxPersistent_NoCachePoison(t *testing.T) {
	urls := &fakeURLFetcher{
		errors: []error{
			errors.New("figma API: 500 internal"),
			errors.New("figma API: 500 internal"),
			errors.New("figma API: 500 internal"),
		},
	}
	bs := &fakeByteFetcher{}
	exp, tA, leafID, _ := newAssetExporter(t, urls, bs)

	ctx := context.Background()
	if _, err := exp.RenderAssetsForLeaf(ctx, tA, leafID, []string{"1:2"}, "png", 1); err == nil {
		t.Fatalf("expected error from 5xx")
	}
	// Cache should be empty — verify by calling LookupAsset directly.
	fileID, vi, err := exp.Repo.LookupLeafFigmaContext(ctx, leafID)
	if err != nil {
		t.Fatalf("lookup ctx: %v", err)
	}
	_, ok, err := exp.Repo.LookupAsset(ctx, tA, fileID, "1:2", "png", 1, vi)
	if err != nil {
		t.Fatalf("lookup asset: %v", err)
	}
	if ok {
		t.Errorf("cache poisoned with row after 5xx")
	}
}

func TestRenderAssets_ContextCancel_ReturnsCtxErr(t *testing.T) {
	// Limiter pre-drained so first call to wait() blocks.
	limiter := &figmaRateLimiter{buckets: map[string]*figmaBucket{}}
	now := time.Now()
	for i := 0; i < AssetExportURLBurst; i++ {
		_ = limiter.allow("tenantX", now)
	}
	// Force-update the bucket so refill is far away.
	limiter.mu.Lock()
	limiter.buckets["tenantX"].updated = now
	limiter.buckets["tenantX"].tokens = 0
	limiter.mu.Unlock()

	urls := &fakeURLFetcher{}
	bs := &fakeByteFetcher{}

	d, _, _, uA := newTestDB(t)
	// Use the synthetic tenant ID directly so the limiter's pre-drained bucket matches.
	tA := "tenantX"
	// Manually create the tenant row so seedFlowAndVersion's FK constraints pass.
	if _, err := d.DB.ExecContext(context.Background(),
		`INSERT INTO tenants (id, slug, name, status, plan_type, created_at, created_by)
		 VALUES (?, 'tenant-x', 'X', 'active', 'free', ?, ?)`,
		tA, time.Now().UTC().Format(time.RFC3339), uA); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	repo := NewTenantRepo(d.DB, tA)
	flowID, _, _ := seedFlowAndVersion(t, repo, uA, "FILE-K-cancel")

	exp := &AssetExporter{
		Repo:    repo,
		URLs:    urls,
		Bytes:   bs,
		DataDir: t.TempDir(),
		Limiter: limiter,
		Now:     time.Now,
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	done := make(chan error, 1)
	go func() {
		_, err := exp.RenderAssetsForLeaf(ctx, tA, flowID, []string{"1:2"}, "png", 1)
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil || !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("RenderAssetsForLeaf did not return after ctx cancel — likely stuck goroutine")
	}
}

// ─── Repo helper unit tests ─────────────────────────────────────────────────

func TestLookupAsset_Miss(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	_, ok, err := repo.LookupAsset(context.Background(), tA, "F", "1:2", "png", 1, 1)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if ok {
		t.Errorf("expected miss")
	}
}

func TestStoreAsset_Then_LookupAsset_Hit(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	now := time.Now().UTC().Truncate(time.Second)
	row := AssetCacheRow{
		TenantID: tA, FileID: "F", NodeID: "1:2",
		Format: "svg", Scale: 1, VersionIndex: 5,
		StorageKey: "assets/T/F/v5/1_2.svg", Bytes: 42, Mime: "image/svg+xml",
		CreatedAt: now,
	}
	if err := repo.StoreAsset(context.Background(), row); err != nil {
		t.Fatalf("store: %v", err)
	}
	got, ok, err := repo.LookupAsset(context.Background(), tA, "F", "1:2", "svg", 1, 5)
	if err != nil || !ok {
		t.Fatalf("expected hit; ok=%v err=%v", ok, err)
	}
	if got.StorageKey != row.StorageKey || got.Bytes != 42 || got.Mime != "image/svg+xml" {
		t.Errorf("row mismatch: %+v", got)
	}
}

func TestStoreAsset_TenantMismatch_Rejected(t *testing.T) {
	d, tA, tB, _ := newTestDB(t)
	repoA := NewTenantRepo(d.DB, tA)
	row := AssetCacheRow{
		TenantID: tB, // ← wrong tenant
		FileID: "F", NodeID: "1:2", Format: "png", Scale: 1, VersionIndex: 1,
		StorageKey: "x", Mime: "image/png",
	}
	if err := repoA.StoreAsset(context.Background(), row); err == nil {
		t.Errorf("expected tenant mismatch rejection")
	}
}

func TestLookupAsset_TenantIsolation(t *testing.T) {
	d, tA, tB, _ := newTestDB(t)
	repoA := NewTenantRepo(d.DB, tA)
	repoB := NewTenantRepo(d.DB, tB)
	now := time.Now().UTC().Truncate(time.Second)
	if err := repoA.StoreAsset(context.Background(), AssetCacheRow{
		TenantID: tA, FileID: "F", NodeID: "1:2",
		Format: "png", Scale: 1, VersionIndex: 1,
		StorageKey: "k", Mime: "image/png", CreatedAt: now,
	}); err != nil {
		t.Fatalf("store: %v", err)
	}
	// repoB should miss.
	_, ok, err := repoB.LookupAsset(context.Background(), tB, "F", "1:2", "png", 1, 1)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if ok {
		t.Errorf("cross-tenant read leaked")
	}
}

// ─── Mime mapping ───────────────────────────────────────────────────────────

func TestMimeForAssetFormat(t *testing.T) {
	if mimeForAssetFormat("png") != "image/png" {
		t.Error("png mime")
	}
	if mimeForAssetFormat("svg") != "image/svg+xml" {
		t.Error("svg mime")
	}
}

// ─── HTTP byte-fetcher integration (smoke) ──────────────────────────────────

func TestHTTPAssetByteFetcher_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("hello-bytes"))
	}))
	defer srv.Close()
	f := &HTTPAssetByteFetcher{}
	bs, err := f.Fetch(context.Background(), srv.URL+"/x.png")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if string(bs) != "hello-bytes" {
		t.Errorf("body: %q", string(bs))
	}
}

func TestHTTPAssetByteFetcher_429(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
	}))
	defer srv.Close()
	f := &HTTPAssetByteFetcher{}
	_, err := f.Fetch(context.Background(), srv.URL)
	if err == nil || !strings.Contains(err.Error(), "429") {
		t.Errorf("expected 429 error, got %v", err)
	}
}

// ─── Cache key format awareness ─────────────────────────────────────────────

func TestFigmaCacheKeyWithFormat_DistinctAxes(t *testing.T) {
	a := figmaCacheKeyWithFormat("t", "f", "1:2", "png", 1)
	b := figmaCacheKeyWithFormat("t", "f", "1:2", "png", 2)
	c := figmaCacheKeyWithFormat("t", "f", "1:2", "svg", 1)
	if a == b {
		t.Errorf("scale axis collapsed: %q", a)
	}
	if a == c {
		t.Errorf("format axis collapsed: %q", a)
	}
}

// ─── Rate limiter wait() ────────────────────────────────────────────────────

func TestFigmaRateLimiter_Wait_RespectsCtxCancel(t *testing.T) {
	rl := &figmaRateLimiter{buckets: map[string]*figmaBucket{}}
	// Drain the bucket.
	now := time.Now()
	for i := 0; i < FigmaProxyBurstSize; i++ {
		_ = rl.allow("t1", now)
	}
	rl.mu.Lock()
	rl.buckets["t1"].tokens = 0
	rl.buckets["t1"].updated = time.Now()
	rl.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(15 * time.Millisecond)
		cancel()
	}()
	err := rl.wait(ctx, "t1", FigmaProxyBurstSize, 10*time.Second)
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestFigmaRateLimiter_Wait_BlocksThenAllows(t *testing.T) {
	rl := &figmaRateLimiter{buckets: map[string]*figmaBucket{}}
	// Burst tokens → exhaust → wait should refill on next tick.
	for i := 0; i < FigmaProxyBurstSize; i++ {
		_ = rl.allow("t1", time.Now())
	}
	start := time.Now()
	if err := rl.wait(context.Background(), "t1", FigmaProxyBurstSize, 50*time.Millisecond); err != nil {
		t.Fatalf("wait: %v", err)
	}
	elapsed := time.Since(start)
	if elapsed > 200*time.Millisecond {
		t.Errorf("wait took too long: %v (refill is 50ms)", elapsed)
	}
}

// Sanity: build a node-id with weird characters and ensure persistAssetBytes
// sanitizes correctly.
func TestPersistAssetBytes_SanitizesPath(t *testing.T) {
	dir := t.TempDir()
	key, err := persistAssetBytes(dir, "T", "F", 3, "../../etc/passwd:1", "png", []byte("x"))
	if err != nil {
		t.Fatalf("persist: %v", err)
	}
	if strings.Contains(key, "..") {
		t.Errorf("key contains '..': %q", key)
	}
	abs := filepath.Join(dir, key)
	if !strings.HasPrefix(abs, dir) {
		t.Errorf("key escapes data dir: %q", abs)
	}
}

// guard: AssetExportChunkSize stays reasonable so the test layer doesn't
// silently regress to single-node calls.
func TestAssetExportChunkSize_Reasonable(t *testing.T) {
	if AssetExportChunkSize < 25 || AssetExportChunkSize > 200 {
		t.Errorf("chunk size out of expected range: %d", AssetExportChunkSize)
	}
}

// Test that retry-after method-shaped errors are honoured.
func TestRetryAfterFromErr_MethodShape(t *testing.T) {
	d, ok := retryAfterFromErr(&rateLimitErr{retryAfter: 250 * time.Millisecond})
	if !ok || d != 250*time.Millisecond {
		t.Errorf("expected 250ms, got %v ok=%v", d, ok)
	}
	if _, ok := retryAfterFromErr(errors.New("plain")); ok {
		t.Errorf("plain error should not yield retry-after")
	}
	if _, ok := retryAfterFromErr(nil); ok {
		t.Errorf("nil error should not yield retry-after")
	}
}

