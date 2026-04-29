package projects

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

// makeTestPNG returns a PNG of (w,h) filled with a checker pattern so the
// downsampler has actual content to chew through.
func makeTestPNG(t *testing.T, w, h int) []byte {
	t.Helper()
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
		t.Fatalf("encode test png: %v", err)
	}
	return buf.Bytes()
}

func TestDownsampleLongEdge_NoOpWhenWithinBounds(t *testing.T) {
	in := makeTestPNG(t, 800, 1200)
	out, err := DownsampleLongEdge(in, 4096)
	if err != nil {
		t.Fatalf("downsample: %v", err)
	}
	if !bytes.Equal(in, out) {
		t.Fatalf("expected byte-identical output for under-cap input")
	}
	w, h, err := PNGDimensions(out)
	if err != nil {
		t.Fatalf("dims: %v", err)
	}
	if w != 800 || h != 1200 {
		t.Fatalf("dims changed: got %dx%d", w, h)
	}
}

func TestDownsampleLongEdge_DownscalesTallImage(t *testing.T) {
	in := makeTestPNG(t, 1000, 9000)
	out, err := DownsampleLongEdge(in, 4096)
	if err != nil {
		t.Fatalf("downsample: %v", err)
	}
	w, h, err := PNGDimensions(out)
	if err != nil {
		t.Fatalf("dims: %v", err)
	}
	if h > 4096 || w > 4096 {
		t.Fatalf("expected long edge <= 4096; got %dx%d", w, h)
	}
	// Aspect ratio preserved (within rounding):  1000/9000 ≈ 0.111
	got := float64(w) / float64(h)
	want := 1000.0 / 9000.0
	if got < want*0.95 || got > want*1.05 {
		t.Fatalf("aspect ratio drift: got %.3f want %.3f", got, want)
	}
}

func TestDownsampleLongEdge_DownscalesWideImage(t *testing.T) {
	in := makeTestPNG(t, 8000, 600)
	out, err := DownsampleLongEdge(in, 4096)
	if err != nil {
		t.Fatalf("downsample: %v", err)
	}
	w, h, _ := PNGDimensions(out)
	if w > 4096 || h > 4096 {
		t.Fatalf("expected long edge <= 4096; got %dx%d", w, h)
	}
}

func TestDownsampleLongEdge_HonorsCustomCap(t *testing.T) {
	in := makeTestPNG(t, 1000, 1000)
	out, err := DownsampleLongEdge(in, 256)
	if err != nil {
		t.Fatalf("downsample: %v", err)
	}
	w, h, _ := PNGDimensions(out)
	if w != 256 || h != 256 {
		t.Fatalf("expected 256x256, got %dx%d", w, h)
	}
}

func TestDownsampleLongEdge_RoundtripPreservesValidPNG(t *testing.T) {
	in := makeTestPNG(t, 5000, 3000)
	out, err := DownsampleLongEdge(in, 4096)
	if err != nil {
		t.Fatalf("downsample: %v", err)
	}
	// Decode the output to confirm it's a valid PNG.
	if _, err := png.Decode(bytes.NewReader(out)); err != nil {
		t.Fatalf("output is not a valid PNG: %v", err)
	}
}

func TestDownsampleLongEdge_ErrorsOnEmptyInput(t *testing.T) {
	if _, err := DownsampleLongEdge(nil, 4096); err == nil {
		t.Fatal("expected error on nil input")
	}
}

func TestDownsampleLongEdge_ErrorsOnInvalidCap(t *testing.T) {
	in := makeTestPNG(t, 100, 100)
	if _, err := DownsampleLongEdge(in, 0); err == nil {
		t.Fatal("expected error on cap=0")
	}
}
