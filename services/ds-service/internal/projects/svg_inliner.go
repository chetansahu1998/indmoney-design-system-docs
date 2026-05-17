// svg_inliner.go — Stage 9 post-pass that splices server-rendered SVG
// bytes from asset_cache into the canonical_tree blob as `svg_markup`
// fields on matching nodes (U8 of the Figma-Dev-Mode-parity plan).
//
// Why this lives in a post-pass rather than at canonical_tree extraction
// time (Stage 2/3): the SVG bytes don't exist until Stage 9 renders
// them via `renderSVGClustersForVersion`. Hoisting eligibility earlier
// would require fetching SVG before knowing which clusters need it
// (vs. structural cluster classification which is shape-based and
// works on the raw tree). Doing the splice after Stage 9 keeps the
// canonical_tree write path linear:
//
//   Stage 2/3: extract canonical_tree, persist (no svg_markup yet)
//   Stage 9.1: render SVG bytes for SVG-eligible clusters → asset_cache
//   Stage 9.2: InlineSVGMarkup loads each screen's tree, splices the
//              SVG bytes into matching nodes, UPDATEs the row.
//
// The client renderer (`nodeToHTML.renderClusterPlaceholder`, U7)
// branches on `node.svg_markup` to emit `<svg>…</svg>` inline rather
// than `<img src=PNG>`. Without this server pass, U7's branch is
// dead code.
//
// Trust contract: SVG bytes from Figma's /v1/images?format=svg API are
// sanitized here via `SanitizeSVGMarkup` before being persisted into
// the canonical_tree. Per doc-review P1 #1, regex-only stripping of
// `<script>` is insufficient — SVG supports `<foreignObject>` with
// embedded HTML, event handlers (`onload`, `onclick`, …), `javascript:`
// URLs in href/xlink:href, and other attack surfaces that a regex
// would miss. We parse via `golang.org/x/net/html` (already a dep)
// and apply a parse-tree-based allowlist.
//
// Failure handling: any per-screen failure (DB read, JSON parse, SVG
// fetch, sanitization) is logged and the screen skipped. Other
// screens continue. A failed inline screen falls through to the PNG
// path on the client (U7 has the R5 silent-fallback branch). One
// poison-pill SVG does not block the whole version.
//
// Idempotency: by default, nodes that already carry a non-empty
// `svg_markup` field are SKIPPED — re-runs are cheap and won't
// re-sanitize unchanged bytes. When the on-disk SVG bytes have
// actually changed (typical case: a Figma re-export wrote fresh
// `<sanitized-node>.svg` to `data/assets/<tenant>/<file>/v<vi>/`),
// callers must pass `SVGInlineDeps.ForceRefresh = true` to bypass
// the skip and overwrite stale markup. The backfill CLI's `--force`
// flag flips this on. Stage 9 leaves it off because the same version
// re-rendering the same node id with the same bytes is the common
// case.
//
// Concurrency: the UPDATE is optimistic-locked on `updated_at` so a
// parallel writer (live Stage 6 commit, parallel Stage 9 re-run, ops
// fix script) cannot be silently stomped — if another transaction
// changed the row between our load and our UPDATE, rows-affected is
// 0 and we surrender the splice; a future pass will re-attempt.

package projects

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// SVGInlineDeps is the minimal surface InlineSVGMarkup pulls from the
// Pipeline. Defined as a struct (not method on Pipeline) so the unit
// can be tested with stub dependencies.
type SVGInlineDeps struct {
	DB      *sql.DB
	DataDir string
	Log     *slog.Logger
	// OversizeScreens (optional out-counter): when non-nil, InlineSVGMarkup
	// increments it once per screen that exceeded the 32MB post-splice cap
	// (`maxScreenInlinedBytesUncompressed`). Distinguishes "skipped — too
	// big" from "skipped — no matching nodes" so operator-facing summaries
	// can flag screens needing attention. Audit reviewer #4.
	OversizeScreens *int
	// SkippedConcurrent (optional out-counter): when non-nil, incremented
	// once per screen where the optimistic-lock UPDATE matched zero rows
	// — i.e. a concurrent writer modified the canonical_tree between the
	// inliner's load and its UPDATE. Surfaces the race in operator-visible
	// summaries instead of swallowing it silently. Audit reviewer #1.
	SkippedConcurrent *int
	// ForceRefresh, when true, bypasses the "already has non-empty
	// svg_markup" skip so the inliner re-reads the on-disk SVG bytes
	// and overwrites stale markup with fresh sanitized output. Set by
	// the backfill CLI's `--force` flag when an operator wants to pick
	// up post-re-export changes. Defaults to false so live Stage 9
	// re-runs stay cheap (the common case writes the same bytes). Audit
	// reviewer #2.
	ForceRefresh bool
}

// maxSingleSVGBytes caps the size of any one SVG markup payload before
// inlining. Average Figma-exported illustration markup is 2-20KB; even
// the largest stylized illustration we've seen is ~80KB. A 1MB ceiling
// catches accidentally-pasted bitmap-traced SVGs that would balloon
// the recompressed canonical_tree past the 64MB decompression cap.
// When this triggers, the node is skipped and falls through to the
// PNG path on the client.
const maxSingleSVGBytes = 1024 * 1024 // 1MB

// maxScreenInlinedBytesUncompressed caps the total uncompressed JSON
// size after splicing. If a single screen accumulates >32MB of inlined
// SVG markup we skip the UPDATE entirely (warning logged) so the
// 64MB decompression cap in canonical_tree.go stays safe. Average
// screen with 5-10 illustrations sits well under 200KB; this guard
// is for adversarial / accidentally-huge-SVG cases.
const maxScreenInlinedBytesUncompressed = 32 * 1024 * 1024 // 32MB

// InlineSVGMarkup loads each screen's canonical_tree, splices the
// SVG bytes from asset_cache into matching nodes as `svg_markup`
// fields, and UPDATEs the screen_canonical_trees row.
//
// Returns the count of screens whose tree was updated (one screen
// may inline N nodes; the return is screen-count, not node-count).
// A nil error means the pass completed; per-screen failures are
// logged but never surface as the function's error.
func InlineSVGMarkup(
	ctx context.Context,
	deps SVGInlineDeps,
	in PipelineInputs,
	svgClusterIDs []string,
) (int, error) {
	if len(svgClusterIDs) == 0 {
		return 0, nil
	}
	if deps.DB == nil {
		return 0, errors.New("svg inline: nil DB")
	}
	if deps.DataDir == "" {
		return 0, errors.New("svg inline: empty DataDir")
	}
	if in.TenantID == "" {
		return 0, errors.New("svg inline: empty TenantID")
	}

	svgSet := make(map[string]struct{}, len(svgClusterIDs))
	for _, id := range svgClusterIDs {
		if id != "" {
			svgSet[id] = struct{}{}
		}
	}
	if len(svgSet) == 0 {
		return 0, nil
	}

	screenIDs := make([]string, 0, len(in.Frames))
	for _, f := range in.Frames {
		if f.ScreenID != "" {
			screenIDs = append(screenIDs, f.ScreenID)
		}
	}

	versionIndex, err := lookupVersionIndex(ctx, deps.DB, in.VersionID)
	if err != nil {
		return 0, fmt.Errorf("svg inline: lookup version index: %w", err)
	}

	updatedScreens := 0
	for _, screenID := range screenIDs {
		if ctx.Err() != nil {
			return updatedScreens, ctx.Err()
		}
		ok, err := inlineForScreen(ctx, deps, in, versionIndex, screenID, svgSet)
		if err != nil {
			if deps.Log != nil {
				deps.Log.Warn("svg inline: screen failed",
					"screen_id", screenID,
					"err", err.Error())
			}
			continue
		}
		if ok {
			updatedScreens++
		}
	}
	return updatedScreens, nil
}

func lookupVersionIndex(ctx context.Context, db *sql.DB, versionID string) (int, error) {
	var vIdx int
	err := db.QueryRowContext(ctx,
		`SELECT version_index FROM project_versions WHERE id = ?`,
		versionID,
	).Scan(&vIdx)
	if err != nil {
		return 0, err
	}
	return vIdx, nil
}

// inlineForScreen loads one screen's canonical_tree, walks for matching
// node IDs, reads + sanitizes their SVG bytes, splices them in, and
// UPDATEs. Returns (true, nil) when a write happened; (false, nil) when
// the screen had no matching nodes.
func inlineForScreen(
	ctx context.Context,
	deps SVGInlineDeps,
	in PipelineInputs,
	versionIndex int,
	screenID string,
	svgSet map[string]struct{},
) (bool, error) {
	treeJSON, loadedAt, err := loadCanonicalTreeRaw(ctx, deps.DB, screenID)
	if err != nil {
		return false, fmt.Errorf("load tree: %w", err)
	}
	if treeJSON == "" {
		return false, nil
	}

	var tree map[string]any
	if err := json.Unmarshal([]byte(treeJSON), &tree); err != nil {
		return false, fmt.Errorf("unmarshal tree: %w", err)
	}

	// Descend into the `document` envelope if present. The canonical_tree
	// JSON is the raw `/v1/files/<id>` response shape, where the actual
	// node tree lives under `tree.document.children`, and the top-level
	// keys are `styles componentSets components document schemaVersion`.
	// ExtractClustersWithSVGFlag (pipeline_cluster_prerender.go:260)
	// already does this descent; the inliner missed it on initial U8
	// ship, which made *every* inline pass a no-op (QA Bug 8 root cause).
	// `walkRoot` is a reference to the same in-memory tree map; mutations
	// to descendant nodes propagate back to `tree` for the post-loop
	// json.Marshal.
	walkRoot := tree
	if doc, hasDoc := tree["document"].(map[string]any); hasDoc {
		walkRoot = doc
	}

	inlinedCount := 0
	walkAndMutate(walkRoot, func(node map[string]any) {
		idVal, ok := node["id"].(string)
		if !ok || idVal == "" {
			return
		}
		if _, want := svgSet[idVal]; !want {
			return
		}
		// Idempotency skip: nodes that already carry a non-empty
		// `svg_markup` are NOT re-read from disk by default. This is the
		// cheap behaviour — re-sanitizing an unchanged byte string is
		// pointless.
		//
		// When `ForceRefresh` is set on the deps, the skip is bypassed
		// so callers triggering a refresh after a Figma re-export (which
		// rewrites the on-disk SVG bytes for the same node id) can pick
		// up the fresher payload. The CLI `--force` flag flips this on.
		if !deps.ForceRefresh {
			if existing, has := node["svg_markup"].(string); has && existing != "" {
				return
			}
		}
		raw, err := readSVGBytes(deps.DataDir, in.TenantID, in.FileID, versionIndex, idVal)
		if err != nil {
			// The render may have failed silently; fall through to PNG.
			if deps.Log != nil {
				deps.Log.Debug("svg inline: read failed",
					"screen_id", screenID, "node_id", idVal, "err", err.Error())
			}
			return
		}
		if len(raw) > maxSingleSVGBytes {
			if deps.Log != nil {
				deps.Log.Warn("svg inline: oversize single payload — falling through to PNG",
					"screen_id", screenID, "node_id", idVal, "bytes", len(raw))
			}
			return
		}
		sanitized, err := SanitizeSVGMarkup(raw)
		if err != nil {
			if deps.Log != nil {
				deps.Log.Warn("svg inline: sanitize failed — falling through to PNG",
					"screen_id", screenID, "node_id", idVal, "err", err.Error())
			}
			return
		}
		node["svg_markup"] = string(sanitized)
		inlinedCount++
	})

	if inlinedCount == 0 {
		return false, nil
	}

	mutated, err := json.Marshal(tree)
	if err != nil {
		return false, fmt.Errorf("marshal tree: %w", err)
	}
	if len(mutated) > maxScreenInlinedBytesUncompressed {
		if deps.Log != nil {
			deps.Log.Warn("svg inline: screen exceeds 32MB cap — skipping UPDATE",
				"screen_id", screenID, "bytes", len(mutated))
		}
		if deps.OversizeScreens != nil {
			*deps.OversizeScreens++
		}
		return false, nil
	}

	zstdBlob, err := CompressTreeZstd(string(mutated))
	if err != nil {
		return false, fmt.Errorf("compress: %w", err)
	}

	// Tenant predicate is defense-in-depth (audit reviewer #7). The CLI
	// + Stage 9 caller already pre-scope screen IDs by tenant via the
	// outer iteration, but joining through `screens` here means any
	// future caller (a new HTTP handler, a one-shot fix script) can't
	// accidentally cross-tenant UPDATE.
	//
	// Optimistic-lock guard (audit reviewer #1): `AND updated_at = ?`
	// uses the timestamp we observed at load time. If any other writer
	// (live Stage 6 commit, parallel Stage 9 re-run, ops-issued fix
	// script) bumped `updated_at` between our load and this UPDATE,
	// rows-affected is 0 and we surrender the splice for this screen.
	// The on-disk SVG bytes are persistent and a future inliner pass
	// will re-attempt against the fresher tree.
	res, err := deps.DB.ExecContext(ctx,
		`UPDATE screen_canonical_trees
		    SET canonical_tree      = '',
		        canonical_tree_gz   = NULL,
		        canonical_tree_zstd = ?,
		        updated_at          = ?
		  WHERE screen_id = ?
		    AND updated_at = ?
		    AND screen_id IN (SELECT id FROM screens WHERE tenant_id = ?)`,
		zstdBlob, rfc3339(time.Now().UTC()), screenID, loadedAt, in.TenantID,
	)
	if err != nil {
		return false, fmt.Errorf("UPDATE screen_canonical_trees: %w", err)
	}
	rows, raErr := res.RowsAffected()
	if raErr != nil {
		// SQLite always supports RowsAffected; treat error as fatal so the
		// caller can decide whether to retry.
		return false, fmt.Errorf("rows affected: %w", raErr)
	}
	if rows == 0 {
		// Concurrent writer won. Log + skip gracefully — the next
		// inliner pass will see the fresher tree and re-attempt.
		if deps.Log != nil {
			deps.Log.Info("svg inline: concurrent writer; skipping screen",
				"screen_id", screenID, "loaded_at", loadedAt)
		}
		if deps.SkippedConcurrent != nil {
			*deps.SkippedConcurrent++
		}
		return false, nil
	}
	return true, nil
}

// LoadCanonicalTreeForBackfill is an exported entry point that mirrors
// loadCanonicalTreeRaw for use by the `backfill-svg-markup` command.
// Bypasses the TenantRepo's per-screen tenant join (cross-tenant safety
// is the operator's responsibility for one-shot CLI runs) so the CLI
// can iterate every screen across every tenant in one DB connection.
// Not exposed via HTTP.
func LoadCanonicalTreeForBackfill(ctx context.Context, db *sql.DB, screenID string) (string, error) {
	tree, _, err := loadCanonicalTreeRaw(ctx, db, screenID)
	return tree, err
}

// loadCanonicalTreeRaw decompresses the canonical_tree from the storage
// triple (legacy TEXT / gzip blob / zstd blob — picks the first one set)
// and also returns the row's `updated_at` so callers can implement
// optimistic-locking on the subsequent UPDATE. Returns ("", "", nil)
// when the row doesn't exist.
func loadCanonicalTreeRaw(ctx context.Context, db *sql.DB, screenID string) (string, string, error) {
	var legacy, updatedAt string
	var gz, zstdBlob []byte
	err := db.QueryRowContext(ctx,
		`SELECT canonical_tree, canonical_tree_gz, canonical_tree_zstd, updated_at
		   FROM screen_canonical_trees
		  WHERE screen_id = ?`,
		screenID,
	).Scan(&legacy, &gz, &zstdBlob, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	if err != nil {
		return "", "", err
	}
	tree, terr := ResolveCanonicalTree(legacy, gz, zstdBlob)
	if terr != nil {
		return "", "", terr
	}
	return tree, updatedAt, nil
}

// readSVGBytes reads the rendered SVG file written by
// `persistAssetBytes` during `renderSVGClustersForVersion`. Path matches
// the layout in `asset_export.go:393`: data/assets/<tenant>/<file>/v<vi>/<sanitized-node>.svg
func readSVGBytes(dataDir, tenantID, fileID string, versionIndex int, nodeID string) ([]byte, error) {
	// Defense-in-depth nodeID validation. Figma's observed node ID
	// format is `<int>:<int>` (sometimes with an I-prefix for instances:
	// "I1234:5"). Reject anything containing path-traversal characters
	// or that doesn't look remotely like a Figma node id — this
	// prevents an attacker-controlled tree from making us read
	// /etc/passwd via a crafted node id. Mirrors the validator pattern
	// in screen_image_fills_handler.go:269.
	if !isValidFigmaNodeID(nodeID) {
		return nil, fmt.Errorf("invalid node id: %q", nodeID)
	}
	rel := filepath.Join("assets", tenantID, fileID,
		fmt.Sprintf("v%d", versionIndex),
		sanitizeNodeIDForFS(nodeID)+".svg")
	path := filepath.Join(dataDir, rel)
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	// Cap the read at maxSingleSVGBytes + 1 so we can detect over-cap
	// without loading the whole pathologically-huge file.
	return io.ReadAll(io.LimitReader(f, maxSingleSVGBytes+1))
}

func isValidFigmaNodeID(id string) bool {
	if id == "" || len(id) > 256 {
		return false
	}
	// Allow ASCII digits, ':', ';', and the leading 'I' Figma uses for
	// instance node ids. The semicolon separates instance-override
	// chain segments — Figma emits ids like
	// "I20013:239603;1625:49634;1625:46951;1434:24120;1742:27214" for
	// deeply nested overrides. Without ';' in the allowlist the
	// inliner silently rejects ~28% of cluster nodes on real leaves
	// (QA Bug 8). Reject anything else.
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch {
		case c >= '0' && c <= '9':
		case c == ':':
		case c == ';':
		case c == 'I' && i == 0:
		default:
			return false
		}
	}
	// Must contain at least one ':' (real node ids look like "1:2",
	// "I123:45"; a bare integer is not a Figma node id).
	return strings.Contains(id, ":")
}

// walkAndMutate runs mutator on every map node in the tree (depth-first,
// children-second). Exported via `MutateCanonicalTree` for any future
// caller that needs to splice fields into a parsed canonical_tree.
func walkAndMutate(node map[string]any, mutator func(node map[string]any)) {
	if node == nil {
		return
	}
	mutator(node)
	children, ok := node["children"].([]any)
	if !ok {
		return
	}
	for _, c := range children {
		if childMap, ok := c.(map[string]any); ok {
			walkAndMutate(childMap, mutator)
		}
	}
}

// MutateCanonicalTree exposes walkAndMutate to other packages that
// need to splice fields into a parsed canonical_tree. Currently only
// used by InlineSVGMarkup; provided for future stages (e.g., a Dev
// Mode annotation pre-pass) that walk + mutate the tree similarly.
func MutateCanonicalTree(tree map[string]any, mutator func(node map[string]any)) {
	walkAndMutate(tree, mutator)
}

// ─── SVG sanitization ──────────────────────────────────────────────────

// blockedSVGElements are stripped entirely (with all their content).
// Each is a known XSS vector inside an inlined SVG:
//
//   script         executes JS directly
//   foreignObject  embeds HTML which can carry scripts / event handlers
//   iframe         loads arbitrary content
//   object,embed   load plugins / external content
//   style          @import url(javascript:…) and CSS-side XSS vectors
//   meta,link,base loads / link attacks
//
// `<a>` is allowed but its href is filtered (no javascript:).
var blockedSVGElements = map[string]struct{}{
	"script":        {},
	"foreignobject": {},
	"iframe":        {},
	"object":        {},
	"embed":         {},
	"style":         {},
	"meta":          {},
	"link":          {},
	"base":          {},
}

// urlAttributes carry values that must be filtered for javascript:
// / data:text-html: schemes. xlink:href is the SVG namespace cousin
// of href; both appear in <use>, <a>, <image>, <animate>.
var urlAttributes = map[string]struct{}{
	"href":       {},
	"xlink:href": {},
	"src":        {},
}

// svgCanonicalNames restores SVG's case-sensitive element / attribute
// names after golang.org/x/net/html's tokenizer lowercases them.
// SVG specifies these identifiers with mixed case (viewBox not viewbox,
// clipPath not clippath); browsers honor only the canonical spelling.
// Keyed by the lowercase form the tokenizer emits.
var svgCanonicalNames = map[string]string{
	// Elements
	"clippath":            "clipPath",
	"lineargradient":      "linearGradient",
	"radialgradient":      "radialGradient",
	"feblend":             "feBlend",
	"fecolormatrix":       "feColorMatrix",
	"fecomponenttransfer": "feComponentTransfer",
	"fecomposite":         "feComposite",
	"feconvolvematrix":    "feConvolveMatrix",
	"fediffuselighting":   "feDiffuseLighting",
	"fedisplacementmap":   "feDisplacementMap",
	"fedistantlight":      "feDistantLight",
	"fedropshadow":        "feDropShadow",
	"feflood":             "feFlood",
	"fefunca":             "feFuncA",
	"fefuncb":             "feFuncB",
	"fefuncg":             "feFuncG",
	"fefuncr":             "feFuncR",
	"fegaussianblur":      "feGaussianBlur",
	"feimage":             "feImage",
	"femerge":             "feMerge",
	"femergenode":         "feMergeNode",
	"femorphology":        "feMorphology",
	"feoffset":            "feOffset",
	"fepointlight":        "fePointLight",
	"fespecularlighting":  "feSpecularLighting",
	"fespotlight":         "feSpotLight",
	"fetile":              "feTile",
	"feturbulence":        "feTurbulence",
	// Attributes
	"viewbox":              "viewBox",
	"preserveaspectratio":  "preserveAspectRatio",
	"clippathunits":        "clipPathUnits",
	"gradientunits":        "gradientUnits",
	"gradienttransform":    "gradientTransform",
	"patternunits":         "patternUnits",
	"patterntransform":     "patternTransform",
	"markerwidth":          "markerWidth",
	"markerheight":         "markerHeight",
	"markerunits":          "markerUnits",
	"refx":                 "refX",
	"refy":                 "refY",
	"basefrequency":        "baseFrequency",
	"numoctaves":           "numOctaves",
	"stitchtiles":          "stitchTiles",
	"surfacescale":         "surfaceScale",
	"lightingcolor":        "lightingColor",
	"diffuseconstant":      "diffuseConstant",
	"specularconstant":     "specularConstant",
	"specularexponent":     "specularExponent",
	"kernelmatrix":         "kernelMatrix",
	"kernelunitlength":     "kernelUnitLength",
	"edgemode":             "edgeMode",
	"colorinterpolation":   "color-interpolation",
	"floodcolor":           "flood-color",
	"floodopacity":         "flood-opacity",
	"stopcolor":            "stop-color",
	"stopopacity":          "stop-opacity",
	"strokewidth":          "stroke-width",
	"strokelinecap":        "stroke-linecap",
	"strokelinejoin":       "stroke-linejoin",
	"strokemiterlimit":     "stroke-miterlimit",
	"strokedasharray":      "stroke-dasharray",
	"strokedashoffset":     "stroke-dashoffset",
	"strokeopacity":        "stroke-opacity",
	"fillopacity":          "fill-opacity",
	"fillrule":             "fill-rule",
	"textanchor":           "text-anchor",
	"dominantbaseline":     "dominant-baseline",
	"fontsize":             "font-size",
	"fontfamily":           "font-family",
	"fontweight":           "font-weight",
	"fontstyle":            "font-style",
	"letterspacing":        "letter-spacing",
	"xlink:href":           "xlink:href",
	"tablevalues":          "tableValues",
	"primitiveunits":       "primitiveUnits",
	"filterunits":          "filterUnits",
	"maskunits":            "maskUnits",
	"maskcontentunits":     "maskContentUnits",
	"spreadmethod":         "spreadMethod",
}

// canonicalName returns the SVG-canonical spelling for a tokenizer-
// lowercased element or attribute name. Names not in the map (basic
// HTML/SVG attrs like `fill`, `x`, `cx`, etc.) pass through unchanged.
func canonicalName(lower string) string {
	if c, ok := svgCanonicalNames[lower]; ok {
		return c
	}
	return lower
}

// SanitizeSVGMarkup parses the SVG bytes with golang.org/x/net/html's
// tokenizer and emits a filtered re-serialization:
//
//   - blocked elements are dropped entirely (start tag, end tag, and
//     all content between)
//   - attributes whose name starts with "on" (case-insensitive) are
//     stripped from every element
//   - href / xlink:href / src whose value starts with javascript: or
//     data:text/html (case-insensitive, whitespace-trimmed) are
//     stripped
//
// Returns the sanitized markup. Returns an error only when the input
// is structurally unparseable (e.g., the tokenizer hits an I/O-level
// failure — never on malformed HTML, which the tokenizer is lenient
// about by design).
func SanitizeSVGMarkup(raw []byte) ([]byte, error) {
	z := html.NewTokenizer(strings.NewReader(string(raw)))
	var out strings.Builder
	out.Grow(len(raw))

	// Skip-element depth tracks how many nested blocked elements we are
	// currently inside. When > 0, we drop tokens until matching ends.
	skipDepth := 0
	skipStack := make([]string, 0, 4)

	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			if err := z.Err(); err != nil && !errors.Is(err, io.EOF) {
				return nil, err
			}
			return []byte(out.String()), nil

		case html.StartTagToken:
			name, hasAttr := z.TagName()
			nameStr := strings.ToLower(string(name))
			if _, blocked := blockedSVGElements[nameStr]; blocked {
				skipDepth++
				skipStack = append(skipStack, nameStr)
				continue
			}
			if skipDepth > 0 {
				continue
			}
			out.WriteByte('<')
			out.WriteString(canonicalName(nameStr))
			if hasAttr {
				writeFilteredAttrs(&out, z)
			}
			out.WriteByte('>')

		case html.SelfClosingTagToken:
			name, hasAttr := z.TagName()
			nameStr := strings.ToLower(string(name))
			if _, blocked := blockedSVGElements[nameStr]; blocked {
				continue
			}
			if skipDepth > 0 {
				continue
			}
			out.WriteByte('<')
			out.WriteString(canonicalName(nameStr))
			if hasAttr {
				writeFilteredAttrs(&out, z)
			}
			out.WriteString("/>")

		case html.EndTagToken:
			name, _ := z.TagName()
			nameStr := strings.ToLower(string(name))
			if skipDepth > 0 {
				// Pop the skip-stack only when this end-tag matches the
				// innermost skipped element. Cheap defense against
				// malformed input that ends a different tag mid-skip.
				if len(skipStack) > 0 && skipStack[len(skipStack)-1] == nameStr {
					skipStack = skipStack[:len(skipStack)-1]
					skipDepth--
				}
				continue
			}
			out.WriteString("</")
			out.WriteString(canonicalName(nameStr))
			out.WriteByte('>')

		case html.TextToken:
			if skipDepth > 0 {
				continue
			}
			out.Write(z.Text())

		case html.CommentToken:
			// Strip comments — they don't render and a clever <!-- ... -->
			// inside SVG can hide payload from naive regex scans. Easy to
			// drop; lossless for visual output.
			continue

		case html.DoctypeToken:
			// SVG doctypes are deprecated; we don't need to preserve them.
			continue
		}
	}
}

func writeFilteredAttrs(out *strings.Builder, z *html.Tokenizer) {
	for {
		k, v, more := z.TagAttr()
		name := strings.ToLower(string(k))
		val := string(v)
		if shouldDropAttr(name, val) {
			if !more {
				return
			}
			continue
		}
		out.WriteByte(' ')
		out.WriteString(canonicalName(name))
		out.WriteString(`="`)
		out.WriteString(html.EscapeString(val))
		out.WriteByte('"')
		if !more {
			return
		}
	}
}

func shouldDropAttr(name, val string) bool {
	// Event handlers — onload, onclick, onmouseover, onerror, etc.
	if strings.HasPrefix(name, "on") {
		return true
	}
	if _, isURL := urlAttributes[name]; isURL {
		trim := strings.TrimSpace(val)
		lower := strings.ToLower(trim)
		// javascript: URLs are the obvious vector. data:text/html
		// allows base64-encoded HTML with embedded scripts. Other
		// data: subtypes (data:image/png) are safe for SVG <image>.
		if strings.HasPrefix(lower, "javascript:") {
			return true
		}
		if strings.HasPrefix(lower, "data:text/html") {
			return true
		}
	}
	return false
}

