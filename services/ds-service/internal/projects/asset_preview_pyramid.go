package projects

// asset_preview_pyramid.go — U1 of plan docs/plans/2026-05-06-001-
// perf-canvas-v2-design-brain-borrow-plan.md.
//
// Generates 4 size-tiered PNG previews (128/512/1024/2048 px on the
// longest edge) per Figma node, derived from a single source render at
// scale=2. The frontend picks the smallest tier where
//
//	tierPx ≥ displayPx × zoom × DPR
//
// At zoom=0.25 a 80-frame leaf needs `preview-128` for everything,
// downloading ~5 MB total instead of ~80 MB at scale=2 — a 16× cut in
// both Figma render budget and downstream egress.
//
// Pattern source: DesignBrain-AI
//   - internal/canvas/image_tile_generator.go (Catmull-Rom downsample)
//   - web/src/engine/materials/ImageTileManager.ts:111-130 (tier picker)
//
// We don't tile within a frame (their pattern handles 4096×4096 source
// images by chopping into 512×512 tiles + col/row indexing). Our screens
// cap at ~2048×3000 so single-tile-per-tier is sufficient — one PNG per
// (node, tier) row in asset_cache. This both simplifies the schema
// (no col/row columns) and leaves room to add tiling later if needed.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/image/draw"
)

// PreviewTier is a logical size bucket — the longest edge of the rendered
// PNG, in source pixels. The 4-step ladder follows DesignBrain-AI; see
// `ImageTileManager.ts:111-130` for the math.
type PreviewTier int

// PreviewTiers in ascending order. The frontend's tier selector iterates
// in order and stops at the first tier ≥ required size, so the
// declaration order matters here — keep it sorted.
const (
	PreviewTier128  PreviewTier = 128
	PreviewTier512  PreviewTier = 512
	PreviewTier1024 PreviewTier = 1024
	PreviewTier2048 PreviewTier = 2048
)

// AllPreviewTiers — used by RenderPreviewPyramid to materialize every
// tier in one pass. Slice (not const) so callers can iterate.
var AllPreviewTiers = []PreviewTier{
	PreviewTier128,
	PreviewTier512,
	PreviewTier1024,
	PreviewTier2048,
}

// FormatString returns the asset_cache.format string for this tier:
// "preview-128", "preview-512", etc. Mirrors the migration 0021 CHECK.
func (t PreviewTier) FormatString() string {
	return fmt.Sprintf("preview-%d", int(t))
}

// ParsePreviewTierFormat returns the tier matching a "preview-N" format
// string, or 0 + ok=false if the string is not a known preview tier.
//
// HandleAssetDownload uses this to branch between the legacy node-render
// path (format == "png" | "svg") and the new pyramid-tier path.
func ParsePreviewTierFormat(format string) (PreviewTier, bool) {
	if !strings.HasPrefix(format, "preview-") {
		return 0, false
	}
	for _, t := range AllPreviewTiers {
		if format == t.FormatString() {
			return t, true
		}
	}
	return 0, false
}

// PreviewSourceFetcher abstracts "give me a PNG of this node at scale=2"
// so the pyramid generator can be tested without wiring through the full
// Figma render path. In production it shells out to AssetExporter.
// RenderAssetsForLeaf for a single nodeID.
type PreviewSourceFetcher interface {
	FetchPreviewSource(ctx context.Context, tenantID, leafID, nodeID string) ([]byte, error)
}

// PreviewPyramidGenerator is the long-lived component that renders
// pyramid tiers and persists them to disk. The HTTP handler that
// invokes this is responsible for the asset_cache row write — the
// generator stays storage-agnostic so tests can exercise the Go logic
// without a SQLite dependency.
type PreviewPyramidGenerator struct {
	Source  PreviewSourceFetcher // wraps AssetExporter.RenderAssetsForLeaf
	DataDir string               // mirrors AssetExporter.DataDir
	Now     func() time.Time
}

// Now() exposes the generator's clock so callers (HandleAssetDownload)
// can stamp asset_cache rows with the same timestamp the generator used.
func (g *PreviewPyramidGenerator) now() time.Time {
	if g.Now != nil {
		return g.Now().UTC()
	}
	return time.Now().UTC()
}

// AssetExporterPreviewSource adapts AssetExporter into a
// PreviewSourceFetcher: it renders the source PNG at scale=2 via the
// existing /v1/images render path (cache-aware, rate-limited), then
// reads the bytes from disk so the pyramid generator can downsample
// without keeping the source in memory across calls.
type AssetExporterPreviewSource struct {
	Exporter *AssetExporter
	// TenantBoundExporter returns a copy of Exporter with Repo bound to
	// the given tenantID. Mirrors Server.tenantExporter so the pyramid
	// path can call RenderAssetsForLeaf with a tenant-scoped repo. The
	// signature is a closure (not a Server pointer) to avoid an import
	// cycle with cmd/server.
	TenantBoundExporter func(tenantID string) *AssetExporter
	DataDir             string
}

// FetchPreviewSource renders + reads the PNG@scale=2 source for nodeID.
func (a *AssetExporterPreviewSource) FetchPreviewSource(ctx context.Context, tenantID, leafID, nodeID string) ([]byte, error) {
	if a == nil || a.Exporter == nil || a.TenantBoundExporter == nil {
		return nil, errors.New("preview source: AssetExporter not wired")
	}
	exp := a.TenantBoundExporter(tenantID)
	if exp == nil {
		return nil, fmt.Errorf("preview source: cannot bind tenant %s", tenantID)
	}
	results, err := exp.RenderAssetsForLeaf(ctx, tenantID, leafID, []string{nodeID}, "png", 2)
	if err != nil {
		return nil, fmt.Errorf("preview source render: %w", err)
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("preview source: no result for node %s", nodeID)
	}
	bs, err := os.ReadFile(filepath.Join(a.DataDir, results[0].StorageKey))
	if err != nil {
		return nil, fmt.Errorf("preview source read %s: %w", results[0].StorageKey, err)
	}
	return bs, nil
}

// PyramidResult is one row of generator output — a single tier's
// AssetCacheRow shape so the caller can persist via TenantRepo.StoreAsset.
type PyramidResult struct {
	Tier       PreviewTier
	StorageKey string // relative path under DataDir
	Bytes      int64
	Mime       string // always "image/png" today
}

// RenderPreviewPyramid fetches the source render once, decodes it, and
// emits every tier in AllPreviewTiers via Catmull-Rom downsampling.
// The caller persists the returned rows.
//
// Concurrency: each tier downsamples independently; for 4 tiers on a
// 2048×3000 source the wall time is ~80ms on M-series. Not parallelized
// — the goroutine cost would exceed the savings on this workload.
//
// Error policy: a partial-failure (3 of 4 tiers render, 1 fails on
// encode) returns the successful results plus a non-nil error. The
// caller decides whether to persist the partial set or roll back. The
// HandleAssetDownload path persists partials so a subsequent request
// for a successfully-rendered tier hits cache.
func (g *PreviewPyramidGenerator) RenderPreviewPyramid(
	ctx context.Context,
	tenantID, leafID, fileID, nodeID string,
	versionIndex int,
) ([]PyramidResult, error) {
	if g == nil {
		return nil, errors.New("nil PreviewPyramidGenerator")
	}
	if g.Source == nil {
		return nil, errors.New("PreviewPyramidGenerator.Source is nil")
	}

	srcPNG, err := g.Source.FetchPreviewSource(ctx, tenantID, leafID, nodeID)
	if err != nil {
		return nil, fmt.Errorf("preview pyramid: fetch source: %w", err)
	}

	srcImage, err := png.Decode(bytes.NewReader(srcPNG))
	if err != nil {
		return nil, fmt.Errorf("preview pyramid: decode source: %w", err)
	}

	srcBounds := srcImage.Bounds()
	srcW := srcBounds.Dx()
	srcH := srcBounds.Dy()
	if srcW <= 0 || srcH <= 0 {
		return nil, fmt.Errorf("preview pyramid: degenerate source dimensions %dx%d", srcW, srcH)
	}

	now := g.now()
	results := make([]PyramidResult, 0, len(AllPreviewTiers))
	var firstErr error

	for _, tier := range AllPreviewTiers {
		dst, err := downsampleToTier(srcImage, srcW, srcH, int(tier))
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("tier %d downsample: %w", tier, err)
			}
			continue
		}
		var encoded bytes.Buffer
		if err := png.Encode(&encoded, dst); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("tier %d encode: %w", tier, err)
			}
			continue
		}
		key, err := persistPyramidTier(g.DataDir, tenantID, fileID, versionIndex, nodeID, tier, encoded.Bytes())
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("tier %d persist: %w", tier, err)
			}
			continue
		}
		results = append(results, PyramidResult{
			Tier:       tier,
			StorageKey: key,
			Bytes:      int64(encoded.Len()),
			Mime:       "image/png",
		})
		_ = now // hook for future cache-row metadata
	}

	return results, firstErr
}

// downsampleToTier fits the source image inside `tierPx` on its longest
// edge while preserving aspect ratio. Catmull-Rom kernel matches
// DesignBrain-AI's choice — better quality than bilinear, cheaper than
// Lanczos. If the source is already smaller than the target tier, the
// output is the source verbatim (no upsampling — would just add
// fuzziness without information).
func downsampleToTier(src image.Image, srcW, srcH, tierPx int) (image.Image, error) {
	longest := srcW
	if srcH > longest {
		longest = srcH
	}
	if longest <= tierPx {
		return src, nil
	}
	scale := float64(tierPx) / float64(longest)
	dstW := int(float64(srcW) * scale)
	dstH := int(float64(srcH) * scale)
	if dstW < 1 {
		dstW = 1
	}
	if dstH < 1 {
		dstH = 1
	}
	dst := image.NewNRGBA(image.Rect(0, 0, dstW, dstH))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Over, nil)
	return dst, nil
}

// persistPyramidTier writes a tier's PNG bytes to
//
//	<DataDir>/preview-pyramid/<tenant>/<file>/<version>/<node>__<tier>.png
//
// and returns the relative storage key. Mirrors the persistAssetBytes
// layout in asset_export.go but lives in its own subdir so a `du -sh`
// audit can attribute disk usage to the pyramid feature specifically.
func persistPyramidTier(dataDir, tenantID, fileID string, versionIndex int, nodeID string, tier PreviewTier, bs []byte) (string, error) {
	relDir := filepath.Join("preview-pyramid", tenantID, fileID, fmt.Sprintf("v%d", versionIndex))
	absDir := filepath.Join(dataDir, relDir)
	if err := os.MkdirAll(absDir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir: %w", err)
	}
	// Reuse the asset_export.go sanitizer so disk paths follow one
	// convention across asset types.
	safeNode := sanitizeNodeIDForFS(nodeID)
	relPath := filepath.Join(relDir, fmt.Sprintf("%s__%d.png", safeNode, int(tier)))
	absPath := filepath.Join(dataDir, relPath)
	tmp := absPath + ".tmp"
	if err := os.WriteFile(tmp, bs, 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, absPath); err != nil {
		return "", err
	}
	return relPath, nil
}
