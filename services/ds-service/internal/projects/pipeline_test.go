package projects

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/sse"
)

// stubRenderer is a fake FigmaImageRenderer that returns a canned URL → bytes
// map. The optional fail429 flag injects a rate-limit error on the first N
// attempts so tests can exercise the retry path.
type stubRenderer struct {
	urls       map[string]string
	pngs       map[string][]byte
	fail429    int          // remaining 429s to return
	failOnce   error        // single non-retryable error to return on first call
	mu         sync.Mutex
	calls      int
	downloads  int
}

func (s *stubRenderer) RenderPNGs(ctx context.Context, fileKey string, ids []string) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.fail429 > 0 {
		s.fail429--
		return nil, errors.New("figma images: 429 rate limit")
	}
	if s.failOnce != nil {
		err := s.failOnce
		s.failOnce = nil
		return nil, err
	}
	out := make(map[string]string, len(ids))
	for _, id := range ids {
		if u, ok := s.urls[id]; ok {
			out[id] = u
		}
	}
	return out, nil
}

func (s *stubRenderer) DownloadPNG(ctx context.Context, url string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.downloads++
	bs, ok := s.pngs[url]
	if !ok {
		return nil, errors.New("stubRenderer: unknown url " + url)
	}
	return bs, nil
}

// stubNodeFetcher returns a canned /v1/files/.../nodes response.
type stubNodeFetcher struct {
	frames map[string]any
	calls  int
}

func (s *stubNodeFetcher) GetFileNodes(ctx context.Context, fileKey string, ids []string, depth int) (map[string]any, error) {
	s.calls++
	inner := map[string]any{}
	for _, id := range ids {
		if v, ok := s.frames[id]; ok {
			inner[id] = v
		}
	}
	return map[string]any{"nodes": inner}, nil
}

// stubBroker captures published events.
type stubBroker struct {
	mu       sync.Mutex
	events   []sse.Event
}

func (b *stubBroker) Publish(traceID string, ev sse.Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, ev)
}

func (b *stubBroker) snapshot() []sse.Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	cp := make([]sse.Event, len(b.events))
	copy(cp, b.events)
	return cp
}

// setupPipelineTest builds a fresh DB + repo + project + version + flow + screens
// ready to run RunFastPreview against. Returns (pipeline, inputs, broker, cleanup).
func setupPipelineTest(t *testing.T, renderer FigmaImageRenderer, fetcher FigmaNodeFetcher) (*Pipeline, PipelineInputs, *stubBroker) {
	t.Helper()
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	p, err := repo.UpsertProject(ctx, Project{
		Name: "Test", Platform: "mobile", Product: "Plutus", Path: "Onboarding", OwnerUserID: uA,
	})
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	v, err := repo.CreateVersion(ctx, p.ID, uA)
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	flow, err := repo.UpsertFlow(ctx, Flow{
		ProjectID: p.ID, FileID: "FILE-K", Name: "FlowA",
	})
	if err != nil {
		t.Fatalf("flow: %v", err)
	}

	// Two-frame mode pair.
	screens := []Screen{
		{VersionID: v.ID, FlowID: flow.ID, X: 0, Y: 0, Width: 375, Height: 812},
		{VersionID: v.ID, FlowID: flow.ID, X: 0, Y: 1500, Width: 375, Height: 812},
	}
	if err := repo.InsertScreens(ctx, screens); err != nil {
		t.Fatalf("insert screens: %v", err)
	}

	frames := []PipelineFrame{
		{ScreenID: screens[0].ID, FigmaFrameID: "fig-1", X: 0, Y: 0, Width: 375, Height: 812,
			VariableCollectionID: "VC", ModeID: "light", ModeLabel: "light"},
		{ScreenID: screens[1].ID, FigmaFrameID: "fig-2", X: 0, Y: 1500, Width: 375, Height: 812,
			VariableCollectionID: "VC", ModeID: "dark", ModeLabel: "dark"},
	}

	broker := &stubBroker{}
	pipeline := &Pipeline{
		Repo:          repo,
		Renderer:      renderer,
		NodeFetcher:   fetcher,
		SSE:           broker,
		AuditEnqueuer: NewAuditEnqueuer(),
		AuditLogger:   &AuditLogger{DB: d},
		DataDir:       t.TempDir(),
		Log:           slog.New(slog.NewTextHandler(os.Stderr, nil)),
	}

	in := PipelineInputs{
		VersionID:      v.ID,
		ProjectID:      p.ID,
		ProjectSlug:    p.Slug,
		TenantID:       tA,
		UserID:         uA,
		FileID:         "FILE-K",
		IdempotencyKey: uuid.NewString(),
		TraceID:        uuid.NewString(),
		Frames:         frames,
	}
	return pipeline, in, broker
}

func TestPipeline_HappyPath(t *testing.T) {
	png := makeTestPNG(t, 400, 600)
	renderer := &stubRenderer{
		urls: map[string]string{"fig-1": "u1", "fig-2": "u2"},
		pngs: map[string][]byte{"u1": png, "u2": png},
	}
	fetcher := &stubNodeFetcher{
		frames: map[string]any{
			"fig-1": map[string]any{"document": map[string]any{"id": "fig-1", "type": "FRAME"}},
			"fig-2": map[string]any{"document": map[string]any{"id": "fig-2", "type": "FRAME"}},
		},
	}
	p, in, broker := setupPipelineTest(t, renderer, fetcher)

	if err := p.RunFastPreview(context.Background(), in); err != nil {
		t.Fatalf("pipeline: %v", err)
	}

	// Version must be view_ready.
	v, err := p.Repo.GetVersion(context.Background(), in.VersionID)
	if err != nil {
		t.Fatalf("get version: %v", err)
	}
	if v.Status != "view_ready" {
		t.Fatalf("expected view_ready, got %s (err=%s)", v.Status, v.Error)
	}

	// PNG files must exist.
	for _, f := range in.Frames {
		path := filepath.Join(p.DataDir, "screens", in.TenantID, in.VersionID, f.ScreenID+"@2x.png")
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("png not persisted at %s: %v", path, err)
		}
	}

	// SSE event published.
	events := broker.snapshot()
	found := false
	for _, e := range events {
		if e.Type() == "project.view_ready" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected project.view_ready SSE event; got %d events", len(events))
	}

	// audit_jobs row enqueued.
	var jobCount int
	if err := p.Repo.DB().QueryRow(`SELECT COUNT(*) FROM audit_jobs WHERE version_id = ?`, in.VersionID).Scan(&jobCount); err != nil {
		t.Fatalf("count jobs: %v", err)
	}
	if jobCount != 1 {
		t.Fatalf("expected 1 audit job, got %d", jobCount)
	}
}

func TestPipeline_RetriesOn429(t *testing.T) {
	png := makeTestPNG(t, 200, 200)
	renderer := &stubRenderer{
		urls:    map[string]string{"fig-1": "u1", "fig-2": "u2"},
		pngs:    map[string][]byte{"u1": png, "u2": png},
		fail429: 2, // first two RenderPNGs calls return 429
	}
	fetcher := &stubNodeFetcher{
		frames: map[string]any{
			"fig-1": map[string]any{"id": "fig-1"},
			"fig-2": map[string]any{"id": "fig-2"},
		},
	}
	p, in, _ := setupPipelineTest(t, renderer, fetcher)

	if err := p.RunFastPreview(context.Background(), in); err != nil {
		t.Fatalf("pipeline: %v", err)
	}
	if renderer.calls != 3 {
		t.Fatalf("expected 3 RenderPNGs calls (2 fail + 1 success); got %d", renderer.calls)
	}
}

func TestPipeline_FailsAfter3xRateLimit(t *testing.T) {
	renderer := &stubRenderer{
		urls:    map[string]string{},
		fail429: 10, // always 429
	}
	fetcher := &stubNodeFetcher{frames: map[string]any{}}
	p, in, broker := setupPipelineTest(t, renderer, fetcher)

	err := p.RunFastPreview(context.Background(), in)
	if err == nil {
		t.Fatal("expected pipeline failure")
	}

	v, _ := p.Repo.GetVersion(context.Background(), in.VersionID)
	if v.Status != "failed" {
		t.Fatalf("expected failed status, got %s", v.Status)
	}

	// SSE export_failed published.
	found := false
	for _, e := range broker.snapshot() {
		if e.Type() == "project.export_failed" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected project.export_failed SSE")
	}
}

func TestPipeline_DownsamplesLargePNG(t *testing.T) {
	bigPNG := makeTestPNG(t, 1000, 9000) // 9000px tall
	renderer := &stubRenderer{
		urls: map[string]string{"fig-1": "u1", "fig-2": "u2"},
		pngs: map[string][]byte{"u1": bigPNG, "u2": bigPNG},
	}
	fetcher := &stubNodeFetcher{
		frames: map[string]any{"fig-1": map[string]any{}, "fig-2": map[string]any{}},
	}
	p, in, _ := setupPipelineTest(t, renderer, fetcher)

	if err := p.RunFastPreview(context.Background(), in); err != nil {
		t.Fatalf("pipeline: %v", err)
	}

	// Read the persisted PNG and verify long edge <= 4096.
	path := filepath.Join(p.DataDir, "screens", in.TenantID, in.VersionID, in.Frames[0].ScreenID+"@2x.png")
	bs, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	w, h, err := PNGDimensions(bs)
	if err != nil {
		t.Fatalf("dims: %v", err)
	}
	if w > 4096 || h > 4096 {
		t.Fatalf("expected long edge <= 4096; got %dx%d", w, h)
	}
}

func TestPipeline_FailsFastOn403(t *testing.T) {
	renderer := &stubRenderer{
		urls:     map[string]string{},
		failOnce: errors.New("figma images: 403 forbidden"),
	}
	fetcher := &stubNodeFetcher{frames: map[string]any{}}
	p, in, _ := setupPipelineTest(t, renderer, fetcher)

	err := p.RunFastPreview(context.Background(), in)
	if err == nil {
		t.Fatal("expected error")
	}
	if renderer.calls != 1 {
		t.Fatalf("expected single call (fail-fast), got %d", renderer.calls)
	}
}

func TestRecoverStuckVersions_MarksOrphanedFailed(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	p, _ := repo.UpsertProject(ctx, Project{
		Name: "P", Platform: "mobile", Product: "Plutus", Path: "X", OwnerUserID: uA,
	})
	v, _ := repo.CreateVersion(ctx, p.ID, uA)

	// Backdate heartbeat past staleness threshold.
	stale := time.Now().UTC().Add(-2 * time.Minute).Format(time.RFC3339)
	if _, err := d.ExecContext(ctx,
		`UPDATE project_versions SET pipeline_heartbeat_at = ? WHERE id = ?`,
		stale, v.ID); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	count, err := RecoverStuckVersions(ctx, d.DB, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 recovered, got %d", count)
	}

	got, _ := repo.GetVersion(ctx, v.ID)
	if got.Status != "failed" {
		t.Fatalf("expected failed; got %s", got.Status)
	}
	if got.Error != "orphaned by server restart" {
		t.Fatalf("expected orphaned message; got %q", got.Error)
	}
}

func TestRecoverStuckVersions_LeavesFreshAlone(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	p, _ := repo.UpsertProject(ctx, Project{
		Name: "P", Platform: "mobile", Product: "Plutus", Path: "X", OwnerUserID: uA,
	})
	v, _ := repo.CreateVersion(ctx, p.ID, uA)

	count, err := RecoverStuckVersions(ctx, d.DB, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 recovered (heartbeat fresh); got %d", count)
	}

	got, _ := repo.GetVersion(ctx, v.ID)
	if got.Status != "pending" {
		t.Fatalf("fresh version should still be pending; got %s", got.Status)
	}
}
