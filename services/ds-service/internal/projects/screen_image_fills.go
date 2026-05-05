package projects

// screen_image_fills.go — resolves Figma `imageRef` hashes (raster fills
// embedded in canonical_tree IMAGE Paint values) to cached blobs on disk.
//
// Why this exists: canonical_tree carries `Paint{ type: "IMAGE", imageRef: "<hash>" }`
// for every photo, illustration, raster icon, or texture in a frame, but
// it does NOT carry the URL — Figma's API hides that behind a separate
// endpoint, `/v1/files/<file_key>/images`, which returns a per-file map
// of `imageRef -> s3-url`. Without resolving these the LeafFrameRenderer
// falls back to a grey-checker placeholder for every IMAGE fill, which
// is what users see today as "frames are mostly empty grey columns".
//
// Strategy:
//   1. Walk the canonical_trees of all screens in a leaf, collect the
//      unique imageRef hashes referenced.
//   2. Check asset_cache for already-cached entries (format = 'image-fill',
//      scale = 1, version_index = leaf's project version).
//   3. For misses, call /v1/files/<file_key>/images ONCE per leaf, get the
//      s3-url map, download each missing blob (capped concurrency), sniff
//      the MIME from the first bytes, store under
//      data/image-fills/<tenant>/<file_id>/<imageRef>.<ext>, and INSERT into
//      asset_cache.
//   4. Return `{ imageRef: <relative-storage-key> }` — the HTTP layer maps
//      this to a serve URL `/v1/projects/<slug>/assets/raw/<imageRef>`.
//
// The S3 URLs Figma returns expire (~24h, undocumented but observed). We
// proxy bytes through ds-service so the URL the browser sees never
// expires from its perspective; only the on-disk cache does, governed by
// the per-tenant LRU sweeper (tracked separately).

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ImageFillFormat is the asset_cache.format value for raster fills,
// distinct from the existing 'png'/'svg' used by node renders.
const ImageFillFormat = "image-fill"

// ImageFillScale is the asset_cache.scale slot used for fills. Image
// fills are stored at native resolution (Figma re-encodes to source
// dimensions on upload), so scale is always 1; the column is kept in
// the PK only because it's part of the existing asset_cache schema.
const ImageFillScale = 1

// MaxImageRefsPerLeaf bounds the unique imageRefs collected per leaf.
// A pathological frame with thousands of distinct rasters could starve
// the cache; 2000 is well above any real-world leaf and below the
// payload size where Figma's /v1/files/.../images call latency goes
// nonlinear (observed: ~150ms for ~500 refs, ~1.2s for ~2000).
const MaxImageRefsPerLeaf = 2000

// imageFillBlobMaxBytes — per-blob ceiling. Mirrors the asset bytes
// fetcher's 200 MB cap. A single hero illustration can be ~5–10 MB
// at native resolution; this leaves plenty of headroom while keeping
// the cap defensive against malformed responses.
const imageFillBlobMaxBytes = 200 << 20

// ImageFillResolver resolves canonical_tree imageRefs to cached blobs.
// Constructed once per server boot; ResolveImageRefsForLeaf is called
// per request.
type ImageFillResolver struct {
	DB      *sql.DB                  // raw handle; tenant scope is bound per-call via NewTenantRepo
	URLs    FigmaImageFillURLFetcher // /v1/files/<key>/images
	Bytes   AssetByteFetcher         // CDN GET (reused from asset_export)
	DataDir string                   // root data dir (e.g. ".../services/ds-service/data")
	Now     func() time.Time
}

// FigmaImageFillURLFetcher abstracts /v1/files/<key>/images so tests
// can stub it without hitting Figma. Mirrors FigmaImageURLFetcher's
// shape (used by AssetExporter for /v1/images node renders).
type FigmaImageFillURLFetcher interface {
	GetFileImageFills(ctx context.Context, fileKey string) (map[string]string, error)
}

// ImageFillRef is a single resolved imageRef -> on-disk cache entry.
type ImageFillRef struct {
	StorageKey string // relative path under DataDir, e.g. "image-fills/<tenant>/<file>/<hash>.png"
	Mime       string
	Bytes      int64
}

// ResolveImageRefsForLeaf collects every imageRef in the leaf's canonical
// trees and returns a map of imageRef -> ImageFillRef. Missing refs (the
// imageRef appears in canonical_tree but Figma's /v1/files/.../images
// doesn't list it) are silently skipped — the renderer falls back to its
// existing grey-checker placeholder for those.
//
// Cache strategy:
//   - Tenant-scoped (tenant id derived from the slug + claims at the HTTP layer).
//   - file_id scope so two projects sharing a file share the cache.
//   - version_index scope so a re-import correctly cache-misses on rasters
//     that may have been replaced.
//
// Cost:
//   - 1× /v1/files/<key>/images call per (file, version) combination, ever
//     (cache covers re-requests).
//   - N× CDN GET for first-fetch misses, where N = unique imageRefs in this
//     leaf that aren't already cached.
//   - 0 calls when warm.
func (r *ImageFillResolver) ResolveImageRefsForLeaf(
	ctx context.Context,
	tenantID, slug, leafID string,
) (map[string]ImageFillRef, error) {
	if r == nil {
		return nil, errors.New("nil ImageFillResolver")
	}
	if tenantID == "" {
		return nil, errors.New("tenantID required")
	}
	repo := NewTenantRepo(r.DB, tenantID)

	// 1) Resolve (file_id, version_index) for the leaf — same lookup the
	//    asset_export path uses.
	fileID, versionIndex, err := repo.LookupLeafFigmaContext(ctx, leafID)
	if err != nil {
		return nil, fmt.Errorf("lookup leaf figma context: %w", err)
	}

	// 2) Pull every screen's canonical_tree for this leaf (decompresses
	//    canonical_tree_gz on the fly via listLeafScreensForCSV which is
	//    already tenant + slug + leaf scoped).
	screens, err := repo.listLeafScreensForCSV(ctx, slug, leafID)
	if err != nil {
		return nil, fmt.Errorf("list leaf screens: %w", err)
	}

	// 3) Walk each tree, collect unique imageRefs.
	imageRefs := map[string]struct{}{}
	for _, sc := range screens {
		if sc.Tree == "" {
			continue
		}
		if err := collectImageRefsInto(sc.Tree, imageRefs); err != nil {
			// One bad tree shouldn't sink the leaf — skip it and continue.
			continue
		}
		if len(imageRefs) >= MaxImageRefsPerLeaf {
			break
		}
	}
	if len(imageRefs) == 0 {
		return map[string]ImageFillRef{}, nil
	}

	// 4) Cache lookup. Build the result with hits and a list of misses
	//    that need fetching.
	out := make(map[string]ImageFillRef, len(imageRefs))
	misses := make([]string, 0)
	for ref := range imageRefs {
		row, hit, lerr := repo.LookupAsset(ctx, tenantID, fileID, ref, ImageFillFormat, ImageFillScale, versionIndex)
		if lerr != nil {
			return nil, fmt.Errorf("cache lookup %s: %w", ref, lerr)
		}
		if hit {
			// Verify the bytes still exist on disk; a manual `rm -rf data/`
			// without a DB wipe would otherwise serve 404s forever.
			abs := filepath.Join(r.DataDir, row.StorageKey)
			if _, err := os.Stat(abs); err == nil {
				out[ref] = ImageFillRef{
					StorageKey: row.StorageKey,
					Mime:       row.Mime,
					Bytes:      row.Bytes,
				}
				continue
			}
			// Disk gone — fall through to refetch.
		}
		misses = append(misses, ref)
	}

	if len(misses) == 0 {
		return out, nil
	}

	// 5) One Figma call per leaf for the URL map. Caller is rate-limited
	//    upstream (HTTP layer enforces leaf-level concurrency).
	urls, err := r.URLs.GetFileImageFills(ctx, fileID)
	if err != nil {
		return nil, fmt.Errorf("figma file-images: %w", err)
	}

	// 6) Download bytes for misses with bounded concurrency.
	const concurrency = 8
	type fetchResult struct {
		ref  string
		row  AssetCacheRow
		err  error
		skip bool // ref absent from Figma's response
	}
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	results := make([]fetchResult, len(misses))
	for i, ref := range misses {
		i, ref := i, ref
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()

			s3url, ok := urls[ref]
			if !ok || s3url == "" {
				results[i] = fetchResult{ref: ref, skip: true}
				return
			}
			bs, err := r.Bytes.Fetch(ctx, s3url)
			if err != nil {
				results[i] = fetchResult{ref: ref, err: fmt.Errorf("download %s: %w", ref, err)}
				return
			}
			if int64(len(bs)) > imageFillBlobMaxBytes {
				results[i] = fetchResult{ref: ref, err: fmt.Errorf("blob too large: %d > %d", len(bs), imageFillBlobMaxBytes)}
				return
			}

			mime := http.DetectContentType(bs)
			ext := extForMime(mime)
			storageKey := filepath.Join("image-fills", tenantID, fileID, ref+ext)
			abs := filepath.Join(r.DataDir, storageKey)
			if mkdErr := os.MkdirAll(filepath.Dir(abs), 0o755); mkdErr != nil {
				results[i] = fetchResult{ref: ref, err: fmt.Errorf("mkdir: %w", mkdErr)}
				return
			}
			if wErr := os.WriteFile(abs, bs, 0o644); wErr != nil {
				results[i] = fetchResult{ref: ref, err: fmt.Errorf("write: %w", wErr)}
				return
			}
			results[i] = fetchResult{ref: ref, row: AssetCacheRow{
				TenantID:     tenantID,
				FileID:       fileID,
				NodeID:       ref,
				Format:       ImageFillFormat,
				Scale:        ImageFillScale,
				VersionIndex: versionIndex,
				StorageKey:   storageKey,
				Bytes:        int64(len(bs)),
				Mime:         mime,
				CreatedAt:    r.now(),
			}}
		}()
	}
	wg.Wait()

	// 7) Persist the rows + populate the result map.
	for _, res := range results {
		if res.skip {
			continue
		}
		if res.err != nil {
			// Log via context but don't sink the whole leaf for one bad blob.
			// Caller's logger isn't visible here; we silently drop the row.
			continue
		}
		if err := repo.StoreAsset(ctx, res.row); err != nil {
			return out, fmt.Errorf("store %s: %w", res.ref, err)
		}
		out[res.ref] = ImageFillRef{
			StorageKey: res.row.StorageKey,
			Mime:       res.row.Mime,
			Bytes:      res.row.Bytes,
		}
	}

	return out, nil
}

func (r *ImageFillResolver) now() time.Time {
	if r.Now != nil {
		return r.Now().UTC()
	}
	return time.Now().UTC()
}

// LookupImageFillByRef finds a cached image-fill row across any
// (file_id, version_index) for the tenant, since the serve handler
// only has the imageRef hash to work with — the canonical_tree that
// referenced the imageRef carries the file context, but the browser
// loading the URL doesn't. Tenant scope is enforced; cross-tenant hits
// are impossible because the WHERE clause requires tenant_id match.
func (t *TenantRepo) LookupImageFillByRef(ctx context.Context, tenantID, imageRef string) (AssetCacheRow, bool, error) {
	if t == nil {
		return AssetCacheRow{}, false, errors.New("nil repo")
	}
	if t.tenantID != "" && tenantID != t.tenantID {
		return AssetCacheRow{}, false, fmt.Errorf("tenant mismatch: repo=%s arg=%s", t.tenantID, tenantID)
	}
	row := t.handle().QueryRowContext(ctx, `
		SELECT file_id, storage_key, bytes, mime, created_at
		  FROM asset_cache
		 WHERE tenant_id = ? AND node_id = ? AND format = ?
		 ORDER BY created_at DESC
		 LIMIT 1
	`, tenantID, imageRef, ImageFillFormat)
	var r AssetCacheRow
	var createdAt string
	if err := row.Scan(&r.FileID, &r.StorageKey, &r.Bytes, &r.Mime, &createdAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AssetCacheRow{}, false, nil
		}
		return AssetCacheRow{}, false, err
	}
	r.TenantID = tenantID
	r.NodeID = imageRef
	r.Format = ImageFillFormat
	r.Scale = ImageFillScale
	r.CreatedAt = parseTime(createdAt)
	return r, true, nil
}

// extForMime returns the file extension for a sniffed image MIME. Falls
// back to .bin for unknowns — we still serve the bytes with their
// recorded MIME, the extension is just for human-readable storage paths.
func extForMime(mime string) string {
	switch {
	case strings.HasPrefix(mime, "image/png"):
		return ".png"
	case strings.HasPrefix(mime, "image/jpeg"), strings.HasPrefix(mime, "image/jpg"):
		return ".jpg"
	case strings.HasPrefix(mime, "image/webp"):
		return ".webp"
	case strings.HasPrefix(mime, "image/gif"):
		return ".gif"
	case strings.HasPrefix(mime, "image/svg"):
		return ".svg"
	default:
		return ".bin"
	}
}

// collectImageRefsInto walks `treeJSON` (raw canonical_tree) recursively
// and inserts every IMAGE-Paint imageRef into `acc`.
//
// We use a streaming-ish decoder (json.Unmarshal into map[string]any) so
// callers don't need to maintain a parallel set of typed structs that
// drift with canonical_tree's schema. The walker only inspects two
// keys per node: "fills" (Paint[]) and "children" (Node[]).
func collectImageRefsInto(treeJSON string, acc map[string]struct{}) error {
	if treeJSON == "" {
		return nil
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(treeJSON), &root); err != nil {
		return err
	}
	// canonical_tree may be wrapped as {document, components, ...} or
	// be the bare node itself. Both shapes hit the same walker; the
	// document key is processed as a child if present.
	walkForImageRefs(root, acc)
	return nil
}

func walkForImageRefs(v any, acc map[string]struct{}) {
	switch n := v.(type) {
	case map[string]any:
		// fills: []Paint
		if fills, ok := n["fills"].([]any); ok {
			for _, f := range fills {
				p, ok := f.(map[string]any)
				if !ok {
					continue
				}
				if p["type"] != "IMAGE" {
					continue
				}
				ref, _ := p["imageRef"].(string)
				if ref != "" {
					acc[ref] = struct{}{}
				}
			}
		}
		// background: also a []Paint in some Figma schemas (older files).
		if bg, ok := n["background"].([]any); ok {
			for _, f := range bg {
				p, ok := f.(map[string]any)
				if !ok {
					continue
				}
				if p["type"] != "IMAGE" {
					continue
				}
				ref, _ := p["imageRef"].(string)
				if ref != "" {
					acc[ref] = struct{}{}
				}
			}
		}
		// children: []Node — recurse.
		if kids, ok := n["children"].([]any); ok {
			for _, c := range kids {
				walkForImageRefs(c, acc)
			}
		}
		// document: top-level wrapper for the envelope shape.
		if doc, ok := n["document"]; ok {
			walkForImageRefs(doc, acc)
		}
	case []any:
		for _, c := range n {
			walkForImageRefs(c, acc)
		}
	}
}
