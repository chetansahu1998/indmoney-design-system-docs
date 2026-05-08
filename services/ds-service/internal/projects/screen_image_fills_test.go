package projects

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

type stubFillURLFetcher struct {
	mu    sync.Mutex
	urls  map[string]string
	err   error
	calls int
}

func (s *stubFillURLFetcher) GetFileImageFills(ctx context.Context, fileKey string) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	out := make(map[string]string, len(s.urls))
	for k, v := range s.urls {
		out[k] = v
	}
	return out, nil
}

type stubByteFetcher struct {
	mu       sync.Mutex
	contents map[string][]byte
	calls    int
}

func (s *stubByteFetcher) Fetch(ctx context.Context, url string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	bs, ok := s.contents[url]
	if !ok {
		return nil, errors.New("stubByteFetcher: unknown url " + url)
	}
	return bs, nil
}

// 1×1 PNG header — http.DetectContentType only needs the first 512 bytes.
var minimalPNG = []byte{
	0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
	0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1F, 0x15, 0xC4,
	0x89, 0x00, 0x00, 0x00, 0x0D, 0x49, 0x44, 0x41,
	0x54, 0x78, 0x9C, 0x62, 0x00, 0x01, 0x00, 0x00,
	0x05, 0x00, 0x01, 0x0D, 0x0A, 0x2D, 0xB4, 0x00,
	0x00, 0x00, 0x00, 0x49, 0x45, 0x4E, 0x44, 0xAE,
	0x42, 0x60, 0x82,
}

func TestWarmImageFillsForVersion_PopulatesCache(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	proj, err := repo.UpsertProject(ctx, Project{
		Name: "T", Platform: "mobile", Product: "Plutus", Path: "Onboarding", OwnerUserID: uA,
	})
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	v, err := repo.CreateVersion(ctx, proj.ID, uA)
	if err != nil {
		t.Fatalf("version: %v", err)
	}

	fileID := "FILE-K"
	urls := &stubFillURLFetcher{urls: map[string]string{
		"refA": "https://cdn.example/A",
		"refB": "https://cdn.example/B",
	}}
	bytes := &stubByteFetcher{contents: map[string][]byte{
		"https://cdn.example/A": minimalPNG,
		"https://cdn.example/B": minimalPNG,
	}}

	dataDir := t.TempDir()
	resolver := &ImageFillResolver{
		DB:      d.DB,
		URLs:    urls,
		Bytes:   bytes,
		DataDir: dataDir,
		Now:     time.Now,
	}

	tree := `{"document":{"fills":[{"type":"IMAGE","imageRef":"refA"}],"children":[{"fills":[{"type":"IMAGE","imageRef":"refB"}]}]}}`

	if err := resolver.WarmImageFillsForVersion(ctx, tA, fileID, v.ID, []string{tree}); err != nil {
		t.Fatalf("warm: %v", err)
	}

	if urls.calls != 1 {
		t.Errorf("expected 1 GetFileImageFills call, got %d", urls.calls)
	}
	if bytes.calls != 2 {
		t.Errorf("expected 2 byte fetches (refA, refB), got %d", bytes.calls)
	}

	versionIndex, err := repo.GetVersionIndex(ctx, v.ID)
	if err != nil {
		t.Fatalf("version index: %v", err)
	}
	for _, ref := range []string{"refA", "refB"} {
		row, hit, lerr := repo.LookupAsset(ctx, tA, fileID, ref, ImageFillFormat, ImageFillScale, versionIndex)
		if lerr != nil {
			t.Fatalf("lookup %s: %v", ref, lerr)
		}
		if !hit {
			t.Errorf("expected asset_cache hit for %s after warm", ref)
			continue
		}
		if _, serr := os.Stat(filepath.Join(dataDir, row.StorageKey)); serr != nil {
			t.Errorf("expected file persisted for %s: %v", ref, serr)
		}
	}

	// Second warm should be a no-op (every ref already cached on disk).
	if err := resolver.WarmImageFillsForVersion(ctx, tA, fileID, v.ID, []string{tree}); err != nil {
		t.Fatalf("warm rerun: %v", err)
	}
	if urls.calls != 1 {
		t.Errorf("second warm should skip GetFileImageFills (all hits); got total %d calls", urls.calls)
	}
}

func TestWarmImageFillsForVersion_EmptyTreesNoCalls(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()
	proj, _ := repo.UpsertProject(ctx, Project{
		Name: "T", Platform: "mobile", Product: "Plutus", Path: "X", OwnerUserID: uA,
	})
	v, _ := repo.CreateVersion(ctx, proj.ID, uA)

	urls := &stubFillURLFetcher{}
	bytes := &stubByteFetcher{}
	resolver := &ImageFillResolver{
		DB: d.DB, URLs: urls, Bytes: bytes, DataDir: t.TempDir(), Now: time.Now,
	}

	if err := resolver.WarmImageFillsForVersion(ctx, tA, "F", v.ID, []string{}); err != nil {
		t.Fatalf("empty trees: %v", err)
	}
	if urls.calls != 0 || bytes.calls != 0 {
		t.Errorf("no refs → no fetches; got urls=%d bytes=%d", urls.calls, bytes.calls)
	}
}

func TestPipeline_Stage4_NilImageFillResolverNoop(t *testing.T) {
	png := makeTestPNG(t, 100, 100)
	renderer := &stubRenderer{
		urls: map[string]string{"fig-1": "u1", "fig-2": "u2"},
		pngs: map[string][]byte{"u1": png, "u2": png},
	}
	fetcher := &stubNodeFetcher{
		frames: map[string]any{
			"fig-1": map[string]any{"document": map[string]any{"id": "fig-1"}},
			"fig-2": map[string]any{"document": map[string]any{"id": "fig-2"}},
		},
	}
	p, in, _ := setupPipelineTest(t, renderer, fetcher)
	if p.ImageFillResolver != nil {
		t.Fatal("setupPipelineTest unexpectedly wired ImageFillResolver")
	}
	if err := p.RunFastPreview(context.Background(), in); err != nil {
		t.Fatalf("pipeline: %v", err)
	}
}

func TestPipeline_Stage4_TriggersImageFillWarm(t *testing.T) {
	png := makeTestPNG(t, 100, 100)
	const frameCount = 3

	urls := make(map[string]string, frameCount)
	pngs := make(map[string][]byte, frameCount)
	frames := make(map[string]any, frameCount)
	for i := 0; i < frameCount; i++ {
		fid := "fig-" + strconv.Itoa(i)
		u := "u-" + strconv.Itoa(i)
		urls[fid] = u
		pngs[u] = png
		// Each frame embeds an imageRef so the warmer has work.
		frames[fid] = map[string]any{
			"document": map[string]any{
				"id":    fid,
				"type":  "FRAME",
				"fills": []any{map[string]any{"type": "IMAGE", "imageRef": "ref-" + strconv.Itoa(i)}},
			},
		}
	}
	renderer := &stubRenderer{urls: urls, pngs: pngs}
	fetcher := &stubNodeFetcher{frames: frames}

	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	proj, _ := repo.UpsertProject(ctx, Project{
		Name: "T", Platform: "mobile", Product: "Plutus", Path: "X", OwnerUserID: uA,
	})
	v, _ := repo.CreateVersion(ctx, proj.ID, uA)
	flow, _ := repo.UpsertFlow(ctx, Flow{ProjectID: proj.ID, FileID: "FILE-K", Name: "F"})

	screens := make([]Screen, frameCount)
	for i := range screens {
		screens[i] = Screen{VersionID: v.ID, FlowID: flow.ID, X: 0, Y: float64(i * 1000), Width: 375, Height: 812}
	}
	if err := repo.InsertScreens(ctx, screens); err != nil {
		t.Fatalf("insert screens: %v", err)
	}

	pframes := make([]PipelineFrame, frameCount)
	cdn := map[string]string{}
	cdnBytes := map[string][]byte{}
	for i, s := range screens {
		pframes[i] = PipelineFrame{
			ScreenID: s.ID, FigmaFrameID: "fig-" + strconv.Itoa(i),
			X: 0, Y: float64(i * 1000), Width: 375, Height: 812,
			VariableCollectionID: "VC", ModeID: "light", ModeLabel: "light",
		}
		ref := "ref-" + strconv.Itoa(i)
		cdn[ref] = "https://cdn.example/" + ref
		cdnBytes[cdn[ref]] = minimalPNG
	}

	stubURLs := &stubFillURLFetcher{urls: cdn}
	stubBytes := &stubByteFetcher{contents: cdnBytes}
	resolver := &ImageFillResolver{
		DB: d.DB, URLs: stubURLs, Bytes: stubBytes, DataDir: t.TempDir(), Now: time.Now,
	}

	pipeline := &Pipeline{
		Repo:              repo,
		Renderer:          renderer,
		NodeFetcher:       fetcher,
		SSE:               &stubBroker{},
		AuditEnqueuer:     NewAuditEnqueuer(),
		AuditLogger:       &AuditLogger{DB: d},
		DataDir:           t.TempDir(),
		Log:               slog.New(slog.NewTextHandler(os.Stderr, nil)),
		ImageFillResolver: resolver,
	}
	in := PipelineInputs{
		VersionID: v.ID, ProjectID: proj.ID, ProjectSlug: proj.Slug,
		TenantID: tA, UserID: uA, FileID: "FILE-K",
		IdempotencyKey: uuid.NewString(),
		TraceID:        uuid.NewString(),
		Frames:         pframes,
	}

	if err := pipeline.RunFastPreview(ctx, in); err != nil {
		t.Fatalf("pipeline: %v", err)
	}

	if stubURLs.calls != 1 {
		t.Errorf("expected exactly 1 GetFileImageFills call (warm), got %d", stubURLs.calls)
	}
	if stubBytes.calls != frameCount {
		t.Errorf("expected %d byte fetches (one per imageRef), got %d", frameCount, stubBytes.calls)
	}

	versionIndex, _ := repo.GetVersionIndex(ctx, v.ID)
	for i := 0; i < frameCount; i++ {
		ref := "ref-" + strconv.Itoa(i)
		_, hit, lerr := repo.LookupAsset(ctx, tA, "FILE-K", ref, ImageFillFormat, ImageFillScale, versionIndex)
		if lerr != nil {
			t.Fatalf("lookup %s: %v", ref, lerr)
		}
		if !hit {
			t.Errorf("expected asset_cache hit for %s post-pipeline", ref)
		}
	}
}

// ─── Slug-as-leafID fallback (Bug B fix, 2026-05-08) ──────────────────────

// TestPrimaryFlowIDForSlug_ReturnsOldestFlow exercises the helper used by
// ResolveImageRefsForLeaf when the caller hands us a project slug instead
// of a flow UUID (post brain-products migration: useAtlas.selection.leafID
// is the project slug). Returns the oldest flow under the project.
func TestPrimaryFlowIDForSlug_ReturnsOldestFlow(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	proj, err := repo.UpsertProject(ctx, Project{
		Name: "Test", Platform: "mobile", Product: "P", Path: "Path", OwnerUserID: uA,
	})
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	first, err := repo.UpsertFlow(ctx, Flow{ProjectID: proj.ID, FileID: "F1", Name: "First"})
	if err != nil {
		t.Fatalf("flow1: %v", err)
	}
	// Tiny delay so created_at differs (SQLite RFC3339 millisecond resolution).
	time.Sleep(10 * time.Millisecond)
	if _, err := repo.UpsertFlow(ctx, Flow{ProjectID: proj.ID, FileID: "F1", Name: "Second"}); err != nil {
		t.Fatalf("flow2: %v", err)
	}

	got, err := repo.PrimaryFlowIDForSlug(ctx, proj.Slug)
	if err != nil {
		t.Fatalf("PrimaryFlowIDForSlug: %v", err)
	}
	if got != first.ID {
		t.Errorf("expected oldest flow %s, got %s", first.ID, got)
	}
}

func TestPrimaryFlowIDForSlug_UnknownSlug_ErrNotFound(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	if _, err := repo.PrimaryFlowIDForSlug(context.Background(), "no-such-slug"); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound for unknown slug, got %v", err)
	}
}

// TestResolveImageRefsForLeaf_SlugAsLeafID_FallsBackToPrimaryFlow is the
// regression test for Bug B. Frontend useImageRefs passes
// useAtlas.selection.leafID — which post brain-products IS the project
// slug — to this resolver. Pre-fix the call returned ErrNotFound from
// LookupLeafFigmaContext (which expects a flow UUID); the renderer then
// painted every IMAGE-fill RECTANGLE as a grey placeholder. Symptom: the
// passport photo on NRI VKYC screen 28 (Figma node 1582:112247) showed
// flat grey instead of the cached PNG that ds-service had on disk.
//
// Post-fix the resolver detects leafID == slug and resolves to the
// project's primary flow before downstream lookups. The returned map
// matches what the flow-UUID call returns.
func TestResolveImageRefsForLeaf_SlugAsLeafID_FallsBackToPrimaryFlow(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	proj, _ := repo.UpsertProject(ctx, Project{
		Name: "T", Platform: "mobile", Product: "P", Path: "X", OwnerUserID: uA,
	})
	v, _ := repo.CreateVersion(ctx, proj.ID, uA)
	flow, _ := repo.UpsertFlow(ctx, Flow{ProjectID: proj.ID, FileID: "FILE-K", Name: "Only"})

	// Insert one screen with a canonical_tree carrying an IMAGE fill.
	scr := []Screen{
		{VersionID: v.ID, FlowID: flow.ID, X: 0, Y: 0, Width: 375, Height: 812},
	}
	if err := repo.InsertScreens(ctx, scr); err != nil {
		t.Fatalf("insert screens: %v", err)
	}
	tree := `{"document":{"fills":[{"type":"IMAGE","imageRef":"refPassport"}]}}`
	if _, err := d.DB.ExecContext(ctx,
		`INSERT INTO screen_canonical_trees(screen_id, canonical_tree, hash, updated_at)
		 VALUES (?, ?, ?, datetime('now'))`,
		scr[0].ID, tree, "h"); err != nil {
		t.Fatalf("insert canonical tree: %v", err)
	}

	urls := &stubFillURLFetcher{urls: map[string]string{
		"refPassport": "https://cdn.example/passport",
	}}
	bytes := &stubByteFetcher{contents: map[string][]byte{
		"https://cdn.example/passport": minimalPNG,
	}}
	resolver := &ImageFillResolver{
		DB: d.DB, URLs: urls, Bytes: bytes, DataDir: t.TempDir(), Now: time.Now,
	}

	// Sanity: with a real flow UUID, the resolver returns the imageRef.
	gotByUUID, err := resolver.ResolveImageRefsForLeaf(ctx, tA, proj.Slug, flow.ID)
	if err != nil {
		t.Fatalf("flow-UUID call: %v", err)
	}
	if _, ok := gotByUUID["refPassport"]; !ok {
		t.Fatalf("flow-UUID call missing refPassport, got keys=%v", keys(gotByUUID))
	}

	// Bug B repro: pre-fix this call would fail at LookupLeafFigmaContext.
	// Post-fix it resolves slug → primary flow internally and matches.
	gotBySlug, err := resolver.ResolveImageRefsForLeaf(ctx, tA, proj.Slug, proj.Slug)
	if err != nil {
		t.Fatalf("slug-as-leafID call: %v", err)
	}
	if _, ok := gotBySlug["refPassport"]; !ok {
		t.Fatalf("slug-as-leafID call missing refPassport, got keys=%v", keys(gotBySlug))
	}
	if len(gotByUUID) != len(gotBySlug) {
		t.Errorf("UUID and slug calls returned different sizes: uuid=%d slug=%d",
			len(gotByUUID), len(gotBySlug))
	}
}

func TestResolveImageRefsForLeaf_SlugWithNoFlows_ErrNotFound(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()
	// Project but NO flows.
	proj, _ := repo.UpsertProject(ctx, Project{
		Name: "T", Platform: "mobile", Product: "P", Path: "X", OwnerUserID: uA,
	})

	resolver := &ImageFillResolver{
		DB: d.DB, URLs: &stubFillURLFetcher{}, Bytes: &stubByteFetcher{},
		DataDir: t.TempDir(), Now: time.Now,
	}
	_, err := resolver.ResolveImageRefsForLeaf(ctx, tA, proj.Slug, proj.Slug)
	if err == nil || !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound for slug with no flows, got %v", err)
	}
}

func keys(m map[string]ImageFillRef) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
