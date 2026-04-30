package projects

import (
	"bytes"
	"errors"
	"image"
	"image/png"

	"golang.org/x/image/draw"
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
	// Phase 3.5 follow-up #2: upgraded from nearest-neighbor to
	// Catmull-Rom per the DesignBrain canvas tile-pyramid pattern. Phase 1
	// shipped nearest because golang.org/x/image wasn't yet a module dep;
	// we now depend on it for both DownsampleLongEdge + DownsampleByFraction
	// so atlas thumbnails don't show visible aliasing at the L2 (25%) tier.
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Over, nil)

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

// LODTier identifies a downsample tier. "full" is the 4096px cap from
// persistPNG; "l1" is 50% of that; "l2" is 25%. Frontend's
// `components/projects/atlas/lod/pickLOD.ts:pickLOD()` selects which
// tier URL to fetch based on screen-space pixel density.
type LODTier string

const (
	LODFull LODTier = "full"
	LODL1   LODTier = "l1"
	LODL2   LODTier = "l2"
)

// LODFractionFor returns the linear-dimension scale factor for a tier
// (e.g. l1 = 0.5 = both width and height halved). LODFull returns 1.0
// and is a no-op for the caller.
func LODFractionFor(tier LODTier) float64 {
	switch tier {
	case LODL1:
		return 0.5
	case LODL2:
		return 0.25
	default:
		return 1.0
	}
}

// LODSuffixFor returns the filename infix used to distinguish tiers.
// `<id>@2x.png` is full; `<id>@2x.l1.png` is 50%; `<id>@2x.l2.png` is
// 25%. The matching .ktx2 sidecars use the same infix
// (`<id>@2x.l1.ktx2`).
func LODSuffixFor(tier LODTier) string {
	switch tier {
	case LODL1:
		return ".l1"
	case LODL2:
		return ".l2"
	default:
		return ""
	}
}

// DownsampleByFraction decodes a PNG, scales both dimensions by frac
// (must be in (0, 1]), and re-encodes. frac=1.0 returns the input
// unchanged (no decode-encode round-trip). Returns an error for
// frac<=0 or frac>1.
//
// Sampling matches DownsampleLongEdge — nearest-neighbor — so the
// output character is consistent across the LOD chain. Phase 4 may
// upgrade both functions to bilinear via golang.org/x/image/draw once
// the dep lands.
func DownsampleByFraction(input []byte, frac float64) ([]byte, error) {
	if len(input) == 0 {
		return nil, errors.New("png: empty input")
	}
	if frac <= 0 || frac > 1 {
		return nil, errors.New("png: frac must be in (0, 1]")
	}
	if frac == 1.0 {
		return input, nil
	}

	cfg, err := png.DecodeConfig(bytes.NewReader(input))
	if err != nil {
		return nil, err
	}
	src, err := png.Decode(bytes.NewReader(input))
	if err != nil {
		return nil, err
	}

	dstW := int(float64(cfg.Width) * frac)
	dstH := int(float64(cfg.Height) * frac)
	if dstW < 1 {
		dstW = 1
	}
	if dstH < 1 {
		dstH = 1
	}

	dst := image.NewNRGBA(image.Rect(0, 0, dstW, dstH))
	// Catmull-Rom downsample matches DesignBrain's tile-pyramid pattern
	// (image_tile_generator.go in their canvas pipeline). Specifically
	// chosen for L2 (25%) tier where nearest-neighbor produces visible
	// aliasing on UI screenshots.
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Over, nil)

	var buf bytes.Buffer
	enc := &png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(&buf, dst); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
