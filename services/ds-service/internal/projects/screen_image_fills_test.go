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
