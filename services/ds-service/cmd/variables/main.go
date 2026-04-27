// Command variables extracts Figma NUMBER Variables for a brand and writes
// them as W3C-DTCG dimension tokens to lib/tokens/<brand>/spacing.tokens.json
// (and any other dimension files we add later).
//
// Pattern recognition:
//   - Collection name OR variable name containing "space"/"spacing" → space.*
//   - "radius"/"corner"                                              → radius.*
//   - "padding"                                                      → padding.*
//   - "size"/"width"/"height"                                        → size.*
//
// On failure (Free-plan 403, missing scope), exits 0 and leaves the existing
// hand-curated spacing.tokens.json untouched — extractor never destroys data.
//
// Usage:
//
//	go run ./services/ds-service/cmd/variables --brand indmoney
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/audit"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/figma/client"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/figma/extractor"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/figma/repo"
)

func main() {
	var (
		brand    = flag.String("brand", "indmoney", "Brand slug")
		outDir   = flag.String("out", "", "Output dir (default: lib/tokens/<brand>)")
		dryRun   = flag.Bool("dry-run", false, "Print planned writes without touching files")
		verbose  = flag.Bool("v", false, "Verbose logs")
		source   = flag.String("source", "glyph", "Where to scan: 'glyph' (Glyph + Atoms pages, single-file) or 'manifest' (every file in lib/audit-files.json)")
		manifest = flag.String("manifest", "", "Path to audit-files.json (manifest source only; default: <repo>/lib/audit-files.json)")
	)
	flag.Parse()

	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	loadDotEnv(log)

	pat := os.Getenv("FIGMA_PAT")
	if pat == "" {
		log.Error("FIGMA_PAT not set in env or .env.local")
		os.Exit(1)
	}

	bUp := strings.ToUpper(*brand)
	fileKey := firstEnv("FIGMA_FILE_KEY_"+bUp+"_GLYPH", "FIGMA_FILE_KEY_"+bUp)

	if *outDir == "" {
		*outDir = filepath.Join(repo.Root(), "lib/tokens", *brand)
	}

	// Manifest mode runs longer than the single-file glyph scan because each
	// file is a separate REST round-trip; bump the timeout proportionally.
	ctxTimeout := 5 * time.Minute
	if *source == "manifest" {
		ctxTimeout = 30 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()
	c := client.New(pat)

	// Source: manifest — walk every file in lib/audit-files.json, aggregate
	// histograms across files. The Variables API path is single-file by
	// design, so manifest mode skips it and goes straight to the layout scan.
	if *source == "manifest" {
		runManifestMode(ctx, c, *brand, *manifest, *outDir, *dryRun, log)
		return
	}

	if fileKey == "" {
		log.Error("no Figma file key — set FIGMA_FILE_KEY_" + bUp + "_GLYPH")
		os.Exit(1)
	}

	res, err := extractor.RunVariables(ctx, c, fileKey, log)
	useScan := false
	if err != nil {
		if errors.Is(err, extractor.ErrVariablesUnavailable) {
			log.Info("Variables API gated — falling back to layout-pattern scan of design pages")
			useScan = true
			res = &extractor.VariablesResult{}
		} else {
			log.Error("variables extraction failed", "err", err)
			os.Exit(2)
		}
	} else if len(res.Variables) == 0 {
		log.Info("Variables API returned 0 NUMBER vars — running layout-pattern scan")
		useScan = true
	}

	var scan *extractor.LayoutPatterns
	if useScan {
		// Component-bearing pages. Env vars override the corresponding
		// default; the Icons page is intentionally excluded (its values are
		// sub-pixel SVG geometry, not component layout).
		defaults := map[string]string{
			"FIGMA_NODE_ID_" + bUp + "_GLYPH":       "1631:49817",
			"FIGMA_NODE_ID_" + bUp + "_GLYPH_ATOMS": "1583:36915",
		}
		seen := map[string]bool{}
		pages := []string{}
		for envKey, def := range defaults {
			v := os.Getenv(envKey)
			if v == "" {
				v = def
			}
			if !seen[v] {
				seen[v] = true
				pages = append(pages, v)
			}
		}
		s, err := extractor.RunLayoutPatterns(ctx, c, fileKey, pages, log)
		if err != nil {
			log.Error("layout pattern scan failed", "err", err)
			os.Exit(2)
		}
		scan = s
	}

	log.Info("Figma collections seen", "names", strings.Join(res.CollectionNames, ", "))
	log.Info("classified variables",
		"total", len(res.Variables),
		"spacing", count(res.Variables, "spacing"),
		"radius", count(res.Variables, "radius"),
		"padding", count(res.Variables, "padding"),
		"size", count(res.Variables, "size"),
		"other", count(res.Variables, "other"),
	)

	var doc map[string]any
	if scan != nil {
		doc = buildDTCGFromScan(*brand, scan)
	} else {
		doc = buildDTCG(*brand, res)
	}
	bytes, _ := json.MarshalIndent(doc, "", "  ")
	out := filepath.Join(*outDir, "spacing.tokens.json")

	if *dryRun {
		fmt.Println(string(bytes))
		return
	}
	emptyVars := scan == nil && len(res.Variables) == 0
	emptyScan := scan != nil && len(scan.Spacing) == 0 && len(scan.Padding) == 0 && len(scan.Radius) == 0
	if emptyVars || emptyScan {
		log.Info("no values discovered — leaving existing tokens file untouched", "path", out)
		return
	}
	if err := os.WriteFile(out, append(bytes, '\n'), 0o644); err != nil {
		log.Error("write failed", "err", err)
		os.Exit(2)
	}

	// Single-file scan also writes the observed-values sidecar so the
	// audit + docs site can surface drift candidates ("18px used 47×
	// → snap to 20") without having to re-walk Figma. Manifest mode
	// already does this; mirroring here keeps the two paths consistent.
	if scan != nil {
		side := buildSpacingObservedFromScan(*brand, scan)
		sbytes, _ := json.MarshalIndent(side, "", "  ")
		sidecar := filepath.Join(repo.Root(), "lib/audit/spacing-observed.json")
		if err := os.MkdirAll(filepath.Dir(sidecar), 0o755); err == nil {
			if err := os.WriteFile(sidecar, append(sbytes, '\n'), 0o644); err != nil {
				log.Warn("write sidecar failed", "err", err)
			} else {
				log.Info("wrote spacing-observed sidecar", "path", sidecar)
			}
		}
	}

	if scan != nil {
		log.Info("DONE",
			"out", out,
			"spacing_values", len(scan.Spacing),
			"padding_values", len(scan.Padding),
			"radius_values", len(scan.Radius),
			"frames_seen", scan.FramesSeen,
		)
	} else {
		log.Info("DONE", "out", out, "variables", len(res.Variables))
	}
}

// buildDTCGFromScan converts a histogram-based scan result into DTCG
// dimension tokens. Each value becomes a token named by its px size, with
// usage count surfaced via $extensions.
func buildDTCGFromScan(brand string, s *extractor.LayoutPatterns) map[string]any {
	root := map[string]any{
		"$description": "Spacing + radius tokens discovered by walking Figma autolayout / cornerRadius properties on Glyph component pages. Counts indicate how many nodes use each value.",
		"$extensions": map[string]any{
			"com.indmoney.provenance":  "figma-layout-scan",
			"com.indmoney.brand":       brand,
			"com.indmoney.extractedAt": time.Now().UTC().Format(time.RFC3339),
			"com.indmoney.pages":       s.Pages,
			"com.indmoney.frames-seen": s.FramesSeen,
		},
	}
	// Noise filters: minPx culls sub-pixel icon-vector rounding; minCount
	// filters one-offs. spacing/padding then go through the U18 4-pt
	// grid-snap filter (only on-grid values become tokens; off-grid
	// observations land in the spacing-observed sidecar for drift fixes).
	// Radius keeps the legacy half-px filter — U19 radius classification
	// runs at audit-time, not extraction-time.
	root["space"] = histogramToBucket(s.Spacing, "space", 1, 3, true)
	root["padding"] = histogramToBucket(s.Padding, "padding", 1, 3, true)
	root["radius"] = histogramRadiusBucket(s.Radius, 4)
	return root
}

// histogramRadiusBucket emits one DTCG entry per observed radius that
// matches the U19 allowed set {0, 2, 4, 6, 8, 12, 16}. Off-allowed
// observations (and the icon-vector micro-rounding noise like 0.5, 1000,
// 19.5) are dropped from the token file. The full radius histogram still
// ships in the spacing-observed sidecar so audit fixes can flag cases
// like "23px radius on a 40-tall button → use Pill rule (height/2)".
func histogramRadiusBucket(h extractor.LayoutHistogram, minCount int) map[string]any {
	bucket := map[string]any{}
	allowed := map[float64]bool{}
	for _, v := range audit.AllowedRadiusValues {
		allowed[v] = true
	}
	for _, vc := range h.Sorted() {
		if vc.Count < minCount {
			continue
		}
		if !allowed[vc.Value] {
			continue
		}
		key := numKey(vc.Value)
		bucket[key] = map[string]any{
			"$type":  "dimension",
			"$value": map[string]any{"value": vc.Value, "unit": "px"},
			"$extensions": map[string]any{
				"com.indmoney.usage-count": vc.Count,
				"com.indmoney.token-path":  "radius." + key,
				"com.indmoney.on-grid":     true,
			},
		}
	}
	return bucket
}

// histogramToBucket emits one DTCG entry per distinct value, after filtering
// noise: minPx culls sub-pixel icon-vector rounding, minCount filters one-offs.
// When applyGridSnap is true (spacing + padding), only values on the
// canonical 4-pt grid (audit.AllowedGridSpacing) are kept. Off-grid
// observations are intentionally dropped from the token set — designers
// should bind to the snapped value, not the literal "11px" they happened
// to type. The full observed histogram still ships in
// lib/audit/spacing-observed.json so the audit can flag the drift.
func histogramToBucket(h extractor.LayoutHistogram, prefix string, minPx float64, minCount int, applyGridSnap bool) map[string]any {
	bucket := map[string]any{}
	for _, vc := range h.Sorted() {
		if vc.Value < minPx || vc.Count < minCount {
			continue
		}
		if applyGridSnap {
			if !audit.IsOnSpacingGrid(vc.Value) {
				continue
			}
		} else {
			// Radius path: keep whole + half-pixel values only.
			frac := vc.Value - float64(int64(vc.Value))
			if frac != 0 && frac != 0.5 {
				continue
			}
		}
		key := numKey(vc.Value)
		bucket[key] = map[string]any{
			"$type":  "dimension",
			"$value": map[string]any{"value": vc.Value, "unit": "px"},
			"$extensions": map[string]any{
				"com.indmoney.usage-count": vc.Count,
				"com.indmoney.token-path":  prefix + "." + key,
			},
		}
	}
	return bucket
}

// numKey turns 100 → "100", 0.5 → "0.5", 16 → "16". Doesn't strip integer zeros.
func numKey(v float64) string {
	if v == float64(int64(v)) {
		return fmt.Sprintf("%d", int64(v))
	}
	// Trim only trailing zeros AFTER the decimal: 0.50 → "0.5", but never strips
	// trailing zeros from integers.
	s := fmt.Sprintf("%g", v)
	return s
}

func count(vs []extractor.Variable, bucket string) int {
	n := 0
	for _, v := range vs {
		if v.Bucket == bucket {
			n++
		}
	}
	return n
}

// buildDTCG converts classified variables → a DTCG document mirroring the
// shape of the hand-curated spacing.tokens.json:
//
//	{
//	  "space":  { "<key>":  { "$type": "dimension", "$value": { "value": N, "unit": "px" } } },
//	  "radius": { "<key>":  ... }
//	}
func buildDTCG(brand string, res *extractor.VariablesResult) map[string]any {
	root := map[string]any{
		"$description": "Spacing + radius + padding tokens extracted from Figma Variables.",
		"$extensions": map[string]any{
			"com.indmoney.provenance":  "figma-variables",
			"com.indmoney.brand":       brand,
			"com.indmoney.extractedAt": time.Now().UTC().Format(time.RFC3339),
			"com.indmoney.collections": res.CollectionNames,
		},
	}
	buckets := map[string]map[string]any{
		"space":   {},
		"radius":  {},
		"padding": {},
		"size":    {},
		"other":   {},
	}
	// Sort variables for deterministic output
	sorted := make([]extractor.Variable, len(res.Variables))
	copy(sorted, res.Variables)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Bucket != sorted[j].Bucket {
			return sorted[i].Bucket < sorted[j].Bucket
		}
		if sorted[i].Px != sorted[j].Px {
			return sorted[i].Px < sorted[j].Px
		}
		return sorted[i].Name < sorted[j].Name
	})
	for _, v := range sorted {
		key := slugify(v.Name)
		bucketKey := v.Bucket
		if bucketKey == "spacing" {
			bucketKey = "space"
		}
		entry := map[string]any{
			"$type":  "dimension",
			"$value": map[string]any{"value": v.Px, "unit": "px"},
			"$extensions": map[string]any{
				"com.indmoney.figma-id":    v.ID,
				"com.indmoney.collection":  v.Collection,
				"com.indmoney.figma-name":  v.Name,
			},
		}
		if v.Description != "" {
			entry["$description"] = v.Description
		}
		buckets[bucketKey][key] = entry
	}
	for k, b := range buckets {
		if len(b) > 0 {
			root[k] = b
		}
	}
	return root
}

// buildSpacingObservedFromScan emits the same sidecar shape as the multi-
// file walker, with one synthetic "file" entry representing the Glyph +
// Atoms scan. Lets downstream consumers treat single-file and multi-file
// outputs uniformly.
func buildSpacingObservedFromScan(brand string, s *extractor.LayoutPatterns) map[string]any {
	emit := func(h extractor.LayoutHistogram, dim string) []map[string]any {
		out := make([]map[string]any, 0, len(h))
		for _, vc := range h.Sorted() {
			row := map[string]any{
				"value":   vc.Value,
				"count":   vc.Count,
				"by_file": map[string]int{"glyph-atoms-scan": vc.Count},
			}
			if dim == "spacing" || dim == "padding" {
				snap := audit.SnapSpacing(vc.Value)
				row["on_grid"] = snap.OnGrid
				row["snap_to"] = snap.Snapped
				row["snap_distance"] = snap.Distance
				if len(snap.Candidates) > 1 {
					row["snap_candidates"] = snap.Candidates
				}
			}
			out = append(out, row)
		}
		return out
	}
	return map[string]any{
		"$description": "Raw observed dimension values from Glyph + Atoms layout scan. U18 (grid-snap) reads this to compute drift suggestions.",
		"$extensions": map[string]any{
			"com.indmoney.brand":       brand,
			"com.indmoney.extractedAt": time.Now().UTC().Format(time.RFC3339),
			"com.indmoney.files": []map[string]any{
				{
					"file_slug":   "glyph-atoms-scan",
					"name":        "Glyph + Atoms (single-file scan)",
					"frames_seen": s.FramesSeen,
				},
			},
		},
		"spacing": emit(s.Spacing, "spacing"),
		"padding": emit(s.Padding, "padding"),
		"radius":  emit(s.Radius, "radius"),
	}
}

// auditManifestEntry mirrors the schema in lib/audit-files.json (a subset —
// only the fields the multi-file walker needs).
type auditManifestEntry struct {
	FileKey    string   `json:"file_key"`
	Name       string   `json:"name"`
	Brand      string   `json:"brand"`
	FinalPages []string `json:"final_pages,omitempty"`
}

type auditManifest struct {
	Files []auditManifestEntry `json:"files"`
}

// runManifestMode walks every file in lib/audit-files.json, aggregates
// histograms, and emits spacing.tokens.json with per-file usage breakdown
// in $extensions. A drift sidecar at lib/audit/spacing-observed.json
// preserves the raw observed values (including off-grid noise) so U18 can
// later turn them into drift fixes without re-walking Figma.
func runManifestMode(ctx context.Context, c *client.Client, brand, manifestPath, outDir string, dryRun bool, log *slog.Logger) {
	if manifestPath == "" {
		manifestPath = filepath.Join(repo.Root(), "lib/audit-files.json")
	}
	bs, err := os.ReadFile(manifestPath)
	if err != nil {
		log.Error("read audit-files manifest", "path", manifestPath, "err", err)
		os.Exit(1)
	}
	var m auditManifest
	if err := json.Unmarshal(bs, &m); err != nil {
		log.Error("parse audit-files manifest", "err", err)
		os.Exit(1)
	}
	if len(m.Files) == 0 {
		log.Warn("manifest has zero files — populate lib/audit-files.json (designer runs the Figma plugin once per product file, or hand-edit) and rerun")
		return
	}

	inputs := make([]extractor.FileWalkInput, 0, len(m.Files))
	for _, e := range m.Files {
		if e.FileKey == "" || strings.HasPrefix(e.FileKey, "REPLACE_WITH") {
			log.Warn("skipping placeholder manifest entry", "name", e.Name)
			continue
		}
		if e.Brand != "" && e.Brand != brand {
			continue
		}
		inputs = append(inputs, extractor.FileWalkInput{
			FileKey:  e.FileKey,
			FileSlug: slugifyFileName(e.Name),
			Name:     e.Name,
			Pages:    e.FinalPages,
		})
	}
	if len(inputs) == 0 {
		log.Warn("no usable manifest entries for brand", "brand", brand)
		return
	}

	multi, err := extractor.RunLayoutPatternsMultiFile(ctx, c, inputs, log)
	if err != nil {
		log.Error("multi-file walk failed", "err", err)
		os.Exit(2)
	}

	doc := buildDTCGFromMultiFile(brand, multi)
	bytes, _ := json.MarshalIndent(doc, "", "  ")
	out := filepath.Join(outDir, "spacing.tokens.json")
	sidecar := filepath.Join(repo.Root(), "lib/audit/spacing-observed.json")

	if dryRun {
		fmt.Println(string(bytes))
		return
	}
	agg := multi.Aggregate
	if len(agg.Spacing) == 0 && len(agg.Padding) == 0 && len(agg.Radius) == 0 {
		log.Info("no values discovered across manifest — leaving existing tokens untouched", "out", out)
		return
	}
	if err := os.WriteFile(out, append(bytes, '\n'), 0o644); err != nil {
		log.Error("write tokens", "err", err)
		os.Exit(2)
	}
	// Sidecar: every observed value (no min-px, no min-count filter) keyed
	// by spacing/padding/radius with per-file rollup. U18 reads this to
	// decide which observations are off-grid drift.
	side := buildSpacingObservedSidecar(brand, multi)
	sbytes, _ := json.MarshalIndent(side, "", "  ")
	if err := os.MkdirAll(filepath.Dir(sidecar), 0o755); err != nil {
		log.Warn("mkdir sidecar dir failed", "err", err)
	}
	if err := os.WriteFile(sidecar, append(sbytes, '\n'), 0o644); err != nil {
		log.Warn("write sidecar failed", "err", err)
	}

	log.Info("manifest mode DONE",
		"out", out,
		"sidecar", sidecar,
		"files_attempted", len(inputs),
		"files_succeeded", len(multi.PerFile),
		"frames_seen", agg.FramesSeen,
		"spacing_values", len(agg.Spacing),
		"padding_values", len(agg.Padding),
		"radius_values", len(agg.Radius),
	)
}

// buildDTCGFromMultiFile mirrors buildDTCGFromScan but adds per-file
// $extensions provenance. U18 will re-emit this with grid-snap filtering;
// for now it surfaces the raw aggregate so we can verify multi-file walk
// works before changing the schema.
func buildDTCGFromMultiFile(brand string, m *extractor.MultiFileLayoutPatterns) map[string]any {
	files := make([]map[string]any, 0, len(m.Files))
	for _, f := range m.Files {
		row := map[string]any{
			"file_key":    f.FileKey,
			"file_slug":   f.FileSlug,
			"name":        f.Name,
			"frames_seen": f.FramesSeen,
			"failed":      f.Failed,
		}
		if f.Failed {
			row["error"] = f.FailErr
		}
		files = append(files, row)
	}
	root := map[string]any{
		"$description": "Spacing + radius tokens aggregated across every product file in lib/audit-files.json. Counts indicate aggregate node usage; per-file rollup lives in lib/audit/spacing-observed.json.",
		"$extensions": map[string]any{
			"com.indmoney.provenance":  "figma-layout-scan-multi-file",
			"com.indmoney.brand":       brand,
			"com.indmoney.extractedAt": time.Now().UTC().Format(time.RFC3339),
			"com.indmoney.files":       files,
			"com.indmoney.frames-seen": m.Aggregate.FramesSeen,
		},
	}
	root["space"] = histogramToBucketMulti(m, "spacing", "space", 1, 3)
	root["padding"] = histogramToBucketMulti(m, "padding", "padding", 1, 3)
	root["radius"] = histogramToBucketMulti(m, "radius", "radius", 2, 4)
	return root
}

// histogramToBucketMulti emits one DTCG entry per distinct value, filtered
// against the 4-pt grid (radius uses a softer rule — see U19). Off-grid
// observations are NOT minted as tokens; they appear in the spacing-observed
// sidecar where U18-driven audit fixes pick them up.
//
// The per-file usage breakdown attached so designers can see how the total
// decomposes across product files (e.g. "16 used 47× in Insta Plus, 38× in
// Dashboard, …").
func histogramToBucketMulti(m *extractor.MultiFileLayoutPatterns, dim, prefix string, minPx float64, minCount int) map[string]any {
	bucket := map[string]any{}
	hist := pickHist(m.Aggregate, dim)
	for _, vc := range hist.Sorted() {
		if vc.Value < minPx || vc.Count < minCount {
			continue
		}
		// Spacing + padding: only on-grid values become tokens. Radius is
		// property-derived (height/2 for pills) and handled separately
		// in U19 — keep the legacy filter (integer/half-pixel) for now
		// so radius emission isn't gated on a rule we haven't built yet.
		if dim == "spacing" || dim == "padding" {
			if !audit.IsOnSpacingGrid(vc.Value) {
				continue
			}
		} else {
			frac := vc.Value - float64(int64(vc.Value))
			if frac != 0 && frac != 0.5 {
				continue
			}
		}
		key := numKey(vc.Value)
		perFile := map[string]int{}
		for slug, p := range m.PerFile {
			h := pickHist(p, dim)
			if c := h[vc.Value]; c > 0 {
				perFile[slug] = c
			}
		}
		bucket[key] = map[string]any{
			"$type":  "dimension",
			"$value": map[string]any{"value": vc.Value, "unit": "px"},
			"$extensions": map[string]any{
				"com.indmoney.usage-count":    vc.Count,
				"com.indmoney.usage-by-file":  perFile,
				"com.indmoney.token-path":     prefix + "." + key,
				"com.indmoney.on-grid":        true,
			},
		}
	}
	return bucket
}

// buildSpacingObservedSidecar dumps every observed value (no filters) so U18
// can compute drift without re-walking Figma. Schema is intentionally flat:
// {"spacing": [{value, count, by_file: {slug: count}}], ...}
func buildSpacingObservedSidecar(brand string, m *extractor.MultiFileLayoutPatterns) map[string]any {
	emit := func(dim string) []map[string]any {
		hist := pickHist(m.Aggregate, dim)
		out := make([]map[string]any, 0, len(hist))
		for _, vc := range hist.Sorted() {
			perFile := map[string]int{}
			for slug, p := range m.PerFile {
				h := pickHist(p, dim)
				if c := h[vc.Value]; c > 0 {
					perFile[slug] = c
				}
			}
			row := map[string]any{
				"value":   vc.Value,
				"count":   vc.Count,
				"by_file": perFile,
			}
			// Attach snap suggestion for spacing/padding so docs site +
			// audit fixes can read drift candidates straight from this
			// sidecar without re-running grid math.
			if dim == "spacing" || dim == "padding" {
				snap := audit.SnapSpacing(vc.Value)
				row["on_grid"] = snap.OnGrid
				row["snap_to"] = snap.Snapped
				row["snap_distance"] = snap.Distance
				if len(snap.Candidates) > 1 {
					row["snap_candidates"] = snap.Candidates
				}
			}
			out = append(out, row)
		}
		return out
	}
	files := make([]map[string]any, 0, len(m.Files))
	for _, f := range m.Files {
		files = append(files, map[string]any{
			"file_key":    f.FileKey,
			"file_slug":   f.FileSlug,
			"name":        f.Name,
			"frames_seen": f.FramesSeen,
			"failed":      f.Failed,
		})
	}
	return map[string]any{
		"$description": "Raw observed dimension values across every product file. U18 (grid-snap) reads this to compute drift suggestions.",
		"$extensions": map[string]any{
			"com.indmoney.brand":       brand,
			"com.indmoney.extractedAt": time.Now().UTC().Format(time.RFC3339),
			"com.indmoney.files":       files,
		},
		"spacing": emit("spacing"),
		"padding": emit("padding"),
		"radius":  emit("radius"),
	}
}

func pickHist(p *extractor.LayoutPatterns, dim string) extractor.LayoutHistogram {
	switch dim {
	case "spacing":
		return p.Spacing
	case "padding":
		return p.Padding
	case "radius":
		return p.Radius
	}
	return nil
}

// slugifyFileName mirrors cmd/audit/slugifyFileName so file slugs stay
// consistent across the CLIs.
func slugifyFileName(name string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash && b.Len() > 0 {
				b.WriteRune('-')
				prevDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

func slugify(name string) string {
	s := strings.ToLower(name)
	s = strings.ReplaceAll(s, " ", "-")
	s = strings.ReplaceAll(s, "/", "-")
	s = strings.ReplaceAll(s, "_", "-")
	s = strings.ReplaceAll(s, ".", "-")
	return strings.Trim(s, "-")
}

func firstEnv(keys ...string) string {
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

func loadDotEnv(log *slog.Logger) {
	candidates := []string{".env.local", "../.env.local", "../../.env.local", "../../../.env.local"}
	for _, c := range candidates {
		f, err := os.Open(c)
		if err != nil {
			continue
		}
		defer f.Close()
		buf := make([]byte, 1<<20)
		n, _ := f.Read(buf)
		for _, line := range strings.Split(string(buf[:n]), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			idx := strings.Index(line, "=")
			if idx < 0 {
				continue
			}
			k := strings.TrimSpace(line[:idx])
			v := strings.TrimSpace(line[idx+1:])
			v = strings.Trim(v, "\"'")
			if os.Getenv(k) == "" {
				os.Setenv(k, v)
			}
		}
		log.Debug("dotenv loaded", "path", c)
		return
	}
}
