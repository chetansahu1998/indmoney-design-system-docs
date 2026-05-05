package projects

// asset_preview_pyramid_test.go — unit tests for U1 of plan
// 2026-05-06-001. Exercises the downsample math + tier emission with a
// stub PreviewSourceFetcher so we don't depend on Figma.

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// stubSource implements PreviewSourceFetcher with a hardcoded RGBA PNG.
type stubSource struct {
	pngBytes []byte
	err      error
	calls    int
}

func (s *stubSource) FetchPreviewSource(_ context.Context, _, _, _ string) ([]byte, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return s.pngBytes, nil
}

// makePyramidTestPNG returns a w×h opaque-grey PNG with one diagonal line
// so downsamples are visually distinguishable across tiers. Renamed from
// `makePyramidTestPNG` to avoid clashing with the helper in png_test.go.
func makePyramidTestPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.NRGBA{R: 200, G: 200, B: 200, A: 255}
			if x == y || x+y == w-1 {
				c = color.NRGBA{R: 0, G: 0, B: 0, A: 255}
			}
			img.SetNRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode: %v", err)
	}
	return buf.Bytes()
}

// genWithStub builds a generator backed by stubSource against a temp dir.
func genWithStub(t *testing.T, srcPNG []byte) (*PreviewPyramidGenerator, *stubSource, string) {
	t.Helper()
	dir := t.TempDir()
	src := &stubSource{pngBytes: srcPNG}
	g := &PreviewPyramidGenerator{
		Source:  src,
		DataDir: dir,
		Now:     func() time.Time { return time.Date(2026, 5, 6, 0, 0, 0, 0, time.UTC) },
	}
	return g, src, dir
}

// ─── ParsePreviewTierFormat ────────────────────────────────────────────────

func TestParsePreviewTierFormat_ValidTiers(t *testing.T) {
	tests := []struct {
		input string
		want  PreviewTier
	}{
		{"preview-128", PreviewTier128},
		{"preview-512", PreviewTier512},
		{"preview-1024", PreviewTier1024},
		{"preview-2048", PreviewTier2048},
	}
	for _, tc := range tests {
		got, ok := ParsePreviewTierFormat(tc.input)
		if !ok {
			t.Errorf("input=%q expected ok=true", tc.input)
			continue
		}
		if got != tc.want {
			t.Errorf("input=%q got=%d want=%d", tc.input, got, tc.want)
		}
	}
}

func TestParsePreviewTierFormat_RejectsUnknown(t *testing.T) {
	for _, bad := range []string{"png", "svg", "preview-256", "preview-", "", "preview-2049", "preview"} {
		if _, ok := ParsePreviewTierFormat(bad); ok {
			t.Errorf("expected ok=false for %q", bad)
		}
	}
}

// ─── Pyramid generation: happy path ────────────────────────────────────────

func TestRenderPreviewPyramid_AllFourTiersEmitted(t *testing.T) {
	src := makePyramidTestPNG(t, 2048, 1024)
	g, _, dir := genWithStub(t, src)

	results, err := g.RenderPreviewPyramid(context.Background(), "tenant-A", "leaf-X", "fileK", "1234:5678", 7)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := len(results); got != 4 {
		t.Fatalf("expected 4 tier results, got %d", got)
	}
	wantTiers := map[PreviewTier]bool{
		PreviewTier128: true, PreviewTier512: true, PreviewTier1024: true, PreviewTier2048: true,
	}
	for _, r := range results {
		if !wantTiers[r.Tier] {
			t.Errorf("unexpected tier in result: %d", r.Tier)
		}
		if r.Mime != "image/png" {
			t.Errorf("tier %d mime=%q want image/png", r.Tier, r.Mime)
		}
		if r.Bytes == 0 {
			t.Errorf("tier %d zero bytes", r.Tier)
		}
		// Confirm file was written
		abs := filepath.Join(dir, r.StorageKey)
		st, err := os.Stat(abs)
		if err != nil {
			t.Errorf("tier %d storage_key=%q stat err: %v", r.Tier, r.StorageKey, err)
			continue
		}
		if st.Size() != r.Bytes {
			t.Errorf("tier %d on-disk size %d != reported %d", r.Tier, st.Size(), r.Bytes)
		}
		// Decode and verify dimensions
		f, _ := os.Open(abs)
		decoded, _ := png.Decode(f)
		f.Close()
		bounds := decoded.Bounds()
		longest := bounds.Dx()
		if bounds.Dy() > longest {
			longest = bounds.Dy()
		}
		if longest > int(r.Tier) {
			t.Errorf("tier %d: decoded longest=%d > target=%d", r.Tier, longest, int(r.Tier))
		}
	}
}

func TestRenderPreviewPyramid_OneFigmaCallPerNode(t *testing.T) {
	g, src, _ := genWithStub(t, makePyramidTestPNG(t, 1024, 768))
	if _, err := g.RenderPreviewPyramid(context.Background(), "t", "l", "f", "n", 1); err != nil {
		t.Fatal(err)
	}
	// Despite emitting 4 tiers, the source fetcher should be called once.
	if src.calls != 1 {
		t.Errorf("expected 1 source fetch, got %d", src.calls)
	}
}

// ─── Edge cases ────────────────────────────────────────────────────────────

func TestRenderPreviewPyramid_SourceSmallerThanLargestTier(t *testing.T) {
	// 800px source — only tier-128/512 should downsample; tier-1024/2048
	// should pass through verbatim (no upsample).
	src := makePyramidTestPNG(t, 800, 600)
	g, _, dir := genWithStub(t, src)

	results, err := g.RenderPreviewPyramid(context.Background(), "t", "l", "f", "n", 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		f, _ := os.Open(filepath.Join(dir, r.StorageKey))
		decoded, _ := png.Decode(f)
		f.Close()
		bounds := decoded.Bounds()
		longest := bounds.Dx()
		if bounds.Dy() > longest {
			longest = bounds.Dy()
		}
		switch r.Tier {
		case PreviewTier128:
			if longest > 128 {
				t.Errorf("tier-128 longest=%d > 128", longest)
			}
		case PreviewTier512:
			if longest > 512 {
				t.Errorf("tier-512 longest=%d > 512", longest)
			}
		case PreviewTier1024, PreviewTier2048:
			// Source was 800px; should pass through at 800.
			if longest != 800 {
				t.Errorf("tier-%d source-passthrough longest=%d want 800", r.Tier, longest)
			}
		}
	}
}

func TestRenderPreviewPyramid_StorageKeyShape(t *testing.T) {
	// Confirm relPath embeds tenant/file/version/node and tier.
	g, _, _ := genWithStub(t, makePyramidTestPNG(t, 256, 256))
	results, err := g.RenderPreviewPyramid(context.Background(), "tenant-X", "leaf", "F123", "11:22", 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		want := filepath.Join("preview-pyramid", "tenant-X", "F123", "v5")
		if got := filepath.Dir(r.StorageKey); got != want {
			t.Errorf("tier %d storage_key dir=%q want=%q", r.Tier, got, want)
		}
		// "11:22" -> "11_22" via sanitizeNodeIDForFS
		if !filepathContains(r.StorageKey, "11_22") {
			t.Errorf("tier %d storage_key=%q missing sanitized node id", r.Tier, r.StorageKey)
		}
	}
}

// ─── Error paths ───────────────────────────────────────────────────────────

func TestRenderPreviewPyramid_NilGenerator(t *testing.T) {
	var g *PreviewPyramidGenerator
	_, err := g.RenderPreviewPyramid(context.Background(), "t", "l", "f", "n", 1)
	if err == nil {
		t.Error("expected error from nil generator")
	}
}

func TestRenderPreviewPyramid_SourceError(t *testing.T) {
	g, src, _ := genWithStub(t, nil)
	src.err = errors.New("figma exploded")
	_, err := g.RenderPreviewPyramid(context.Background(), "t", "l", "f", "n", 1)
	if err == nil || !errors.Is(err, err) {
		// (errors.Is(err, err) is trivially true; we just want non-nil)
	}
	if err == nil {
		t.Error("expected error from source failure")
	}
}

func TestRenderPreviewPyramid_BadSourcePNG(t *testing.T) {
	g, _, _ := genWithStub(t, []byte("not a png"))
	_, err := g.RenderPreviewPyramid(context.Background(), "t", "l", "f", "n", 1)
	if err == nil {
		t.Error("expected decode error")
	}
}

// ─── helpers ───────────────────────────────────────────────────────────────

func filepathContains(p, needle string) bool {
	for i := 0; i+len(needle) <= len(p); i++ {
		if p[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
