package projects

import (
	"bytes"
	"errors"
	"image"
	"image/png"
)

// MaxLongEdgePx is the Phase 1 cap for the longer side of any persisted PNG.
// Anything larger gets bilinear-resized down. The 4096px choice trades a small
// amount of detail on tablet-class designs for a >5x reduction in r3f texture
// memory in the typical case.
const MaxLongEdgePx = 4096

// DownsampleLongEdge decodes a PNG, computes whether either dimension exceeds
// maxLongEdge, and if so re-encodes a downsampled copy. If the input is already
// within bounds, the original bytes are returned unchanged (no decode-encode
// round-trip — preserves byte-identical output for unchanged assets).
//
// Sampling is nearest-neighbor (stdlib only — golang.org/x/image is not in
// the module). Phase 1 budget accepts the quality tradeoff; Phase 2 will swap
// in `golang.org/x/image/draw.BiLinear` once the dep is added.
func DownsampleLongEdge(input []byte, maxLongEdge int) ([]byte, error) {
	if len(input) == 0 {
		return nil, errors.New("png: empty input")
	}
	if maxLongEdge <= 0 {
		return nil, errors.New("png: maxLongEdge must be positive")
	}

	// Cheap bounds-only decode before committing to a full decode.
	cfg, err := png.DecodeConfig(bytes.NewReader(input))
	if err != nil {
		return nil, err
	}
	if cfg.Width <= maxLongEdge && cfg.Height <= maxLongEdge {
		return input, nil // no-op
	}

	src, err := png.Decode(bytes.NewReader(input))
	if err != nil {
		return nil, err
	}

	// Compute scale factor (<=1) so the long edge equals maxLongEdge.
	long := cfg.Width
	if cfg.Height > long {
		long = cfg.Height
	}
	scale := float64(maxLongEdge) / float64(long)
	dstW := int(float64(cfg.Width) * scale)
	dstH := int(float64(cfg.Height) * scale)
	if dstW < 1 {
		dstW = 1
	}
	if dstH < 1 {
		dstH = 1
	}

	dst := image.NewNRGBA(image.Rect(0, 0, dstW, dstH))
	srcBounds := src.Bounds()
	srcW := srcBounds.Dx()
	srcH := srcBounds.Dy()

	// Nearest-neighbor — fastest stdlib option. Acceptable for thumbnail-sized
	// frame textures; designers see the high-fidelity render in the PNG tab.
	for y := 0; y < dstH; y++ {
		sy := srcBounds.Min.Y + (y*srcH)/dstH
		for x := 0; x < dstW; x++ {
			sx := srcBounds.Min.X + (x*srcW)/dstW
			dst.Set(x, y, src.At(sx, sy))
		}
	}

	var buf bytes.Buffer
	enc := &png.Encoder{CompressionLevel: png.BestSpeed} // speed > size for these
	if err := enc.Encode(&buf, dst); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// PNGDimensions returns (width, height) of a PNG without fully decoding it.
// Useful for tests asserting "after downsample, long edge <= cap".
func PNGDimensions(b []byte) (int, int, error) {
	cfg, err := png.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		return 0, 0, err
	}
	return cfg.Width, cfg.Height, nil
}
