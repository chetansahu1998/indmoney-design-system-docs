//go:build bench

// Phase 1 perf budget bench: cold fast-preview pipeline must finish
// ≤ 15 s p95 on a synthetic 5-flow input. Run via:
//
//	cd services/ds-service && go test -bench=. -tags=bench -benchtime=3x \
//	  ./internal/projects/...
//
// The `bench` build tag excludes this file from the default
// `go test ./...` run so CI's normal test job stays fast — only the
// dedicated perf-budgets job (or a developer running `-tags=bench`
// explicitly) executes it.
//
// Why ≤ 15 s on synthetic input
// ─────────────────────────────
// The Phase 1 plan budgets ≤ 15 s p95 for the cold fast-preview pipeline
// against real Figma traffic. Real traffic is 90% network — Figma REST
// node fetch + PNG render. We can't benchmark those in CI without a real
// Figma file (and the rate limits would burn). The bench therefore runs
// the WHOLE pipeline against an in-process stub renderer / fetcher; the
// budget here is intentionally generous so that infrastructure overhead
// (sqlite I/O, png downsampling, transaction commit) stays well below
// the network-dominated end-to-end budget. If this bench fails it means
// the local-only cost of the pipeline regressed badly enough that the
// 15 s real-traffic budget is at risk.
//
// Why we duplicate small helpers (benchTestDB, benchPNG) instead of using
// the ones from repository_test.go / png_test.go: those helpers take
// `*testing.T` only. Generic `testing.TB` adapters would mean editing
// shipped Phase 1 source which this PR cannot touch (constraint:
// pipeline_bench_test.go is the only Go file the worktree may add). We
// re-implement them here against `*testing.B` directly.
package projects

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/db"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/sse"
)

// benchStubRenderer is a minimal FigmaImageRenderer for benches.
type benchStubRenderer struct {
	urls map[string]string
	pngs map[string][]byte
	mu   sync.Mutex
}

func (s *benchStubRenderer) RenderPNGs(_ context.Context, _ string, ids []string) (map[string]string, error) {
	out := make(map[string]string, len(ids))
	for _, id := range ids {
		if u, ok := s.urls[id]; ok {
			out[id] = u
		}
	}
	return out, nil
}

func (s *benchStubRenderer) DownloadPNG(_ context.Context, url string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	bs, ok := s.pngs[url]
	if !ok {
		return nil, errors.New("benchStubRenderer: unknown url " + url)
	}
	return bs, nil
}

type benchStubNodeFetcher struct {
	frames map[string]any
}

func (s *benchStubNodeFetcher) GetFileNodes(_ context.Context, _ string, ids []string, _ int) (map[string]any, error) {
	inner := map[string]any{}
	for _, id := range ids {
		if v, ok := s.frames[id]; ok {
			inner[id] = v
		}
	}
	return map[string]any{"nodes": inner}, nil
}

type benchSink struct{}

func (b *benchSink) Publish(_ string, _ sse.Event) {}

// benchTestDB mirrors `newTestDB` from repository_test.go but takes a
// *testing.B. Returns (db, tenantA, userA).
func benchTestDB(b *testing.B) (*db.DB, string, string) {
	b.Helper()
	dir := b.TempDir()
	d, err := db.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		b.Fatalf("db open: %v", err)
	}
	b.Cleanup(func() { d.Close() })

	ctx := context.Background()
	userA := uuid.NewString()
	if err := d.CreateUser(ctx, db.User{
		ID: userA, Email: "bench-a@example.com",
		PasswordHash: "x", Role: "user", CreatedAt: time.Now(),
	}); err != nil {
		b.Fatalf("create userA: %v", err)
	}
	tenantA := uuid.NewString()
	if err := d.CreateTenant(ctx, db.Tenant{
		ID: tenantA, Slug: "bench-tenant-" + tenantA[:8], Name: "Bench",
		Status: "active", PlanType: "free", CreatedAt: time.Now(), CreatedBy: userA,
	}); err != nil {
		b.Fatalf("create tenantA: %v", err)
	}
	return d, tenantA, userA
}

// benchPNG returns a checker-pattern PNG of size (w,h). Equivalent to
// makeTestPNG but takes *testing.B.
func benchPNG(b *testing.B, w, h int) []byte {
	b.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if (x/16+y/16)%2 == 0 {
				img.Set(x, y, color.RGBA{R: 200, G: 100, B: 50, A: 255})
			} else {
				img.Set(x, y, color.RGBA{R: 50, G: 100, B: 200, A: 255})
			}
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		b.Fatalf("encode bench png: %v", err)
	}
	return buf.Bytes()
}

// BenchmarkRunFastPreview_5Flows asserts the cold pipeline budget. Each
// invocation runs RunFastPreview against 5 flows × 4 frames = 20 screens,
// which mirrors the upper end of a typical export per the Phase 1 plan.
//
// Threshold: 15 s per single iteration. We use `-benchtime=3x` in the
// recommended invocation so the test runs 3 cold pipelines back-to-back,
// taking the slowest as our p95-ish proxy.
func BenchmarkRunFastPreview_5Flows(b *testing.B) {
	const (
		flows         = 5
		framesPerFlow = 4
		totalFrames   = flows * framesPerFlow
		budget        = 15 * time.Second
	)

	// Build the synthetic frame set once — outside the timer.
	pngBytes := benchPNG(b, 750, 1334) // ≈mobile retina; downsampler kicks in past 4096px.
	frameIDs := make([]string, totalFrames)
	urls := make(map[string]string, totalFrames)
	pngs := make(map[string][]byte, totalFrames)
	frameNodes := make(map[string]any, totalFrames)
	for i := 0; i < totalFrames; i++ {
		fid := uuid.NewString()
		frameIDs[i] = fid
		u := "u-" + fid
		urls[fid] = u
		pngs[u] = pngBytes
		frameNodes[fid] = map[string]any{
			"document": map[string]any{"id": fid, "type": "FRAME"},
		}
	}

	renderer := &benchStubRenderer{urls: urls, pngs: pngs}
	fetcher := &benchStubNodeFetcher{frames: frameNodes}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		// Fresh DB + fresh project per iteration — cold pipeline only.
		d, tA, uA := benchTestDB(b)
		repo := NewTenantRepo(d.DB, tA)
		ctx := context.Background()

		p, err := repo.UpsertProject(ctx, Project{
			Name: "Bench", Platform: "mobile", Product: "Plutus",
			Path: "Onboarding", OwnerUserID: uA,
		})
		if err != nil {
			b.Fatalf("upsert project: %v", err)
		}
		v, err := repo.CreateVersion(ctx, p.ID, uA)
		if err != nil {
			b.Fatalf("create version: %v", err)
		}

		var pipelineFrames []PipelineFrame
		idx := 0
		for fl := 0; fl < flows; fl++ {
			flow, err := repo.UpsertFlow(ctx, Flow{
				ProjectID: p.ID, FileID: "FILE-BENCH", Name: "Flow",
			})
			if err != nil {
				b.Fatalf("upsert flow: %v", err)
			}
			screens := make([]Screen, framesPerFlow)
			for fr := 0; fr < framesPerFlow; fr++ {
				screens[fr] = Screen{
					VersionID: v.ID, FlowID: flow.ID,
					X: float64(fr * 800), Y: float64(fl * 1500),
					Width: 750, Height: 1334,
				}
			}
			if err := repo.InsertScreens(ctx, screens); err != nil {
				b.Fatalf("insert screens: %v", err)
			}
			for fr := 0; fr < framesPerFlow; fr++ {
				pipelineFrames = append(pipelineFrames, PipelineFrame{
					ScreenID: screens[fr].ID, FigmaFrameID: frameIDs[idx],
					X: screens[fr].X, Y: screens[fr].Y,
					Width: 750, Height: 1334,
					VariableCollectionID: "VC", ModeID: "default", ModeLabel: "default",
				})
				idx++
			}
		}

		pipeline := &Pipeline{
			Repo:          repo,
			Renderer:      renderer,
			NodeFetcher:   fetcher,
			SSE:           &benchSink{},
			AuditEnqueuer: NewAuditEnqueuer(),
			AuditLogger:   &AuditLogger{DB: d},
			DataDir:       b.TempDir(),
			Log:           slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		}
		in := PipelineInputs{
			VersionID: v.ID, ProjectID: p.ID, ProjectSlug: p.Slug,
			TenantID: tA, UserID: uA, FileID: "FILE-BENCH",
			IdempotencyKey: uuid.NewString(), TraceID: uuid.NewString(),
			Frames: pipelineFrames,
		}

		b.StartTimer()
		start := time.Now()
		if err := pipeline.RunFastPreview(ctx, in); err != nil {
			b.Fatalf("pipeline: %v", err)
		}
		elapsed := time.Since(start)
		b.StopTimer()

		if elapsed > budget {
			b.Fatalf(
				"cold pipeline took %v, exceeds Phase 1 budget %v (%d flows × %d frames)",
				elapsed, budget, flows, framesPerFlow,
			)
		}
		b.ReportMetric(float64(elapsed.Milliseconds()), "ms/op")
	}
}
