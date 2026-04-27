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

	"github.com/indmoney/design-system-docs/services/ds-service/internal/figma/client"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/figma/extractor"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/figma/repo"
)

func main() {
	var (
		brand   = flag.String("brand", "indmoney", "Brand slug")
		outDir  = flag.String("out", "", "Output dir (default: lib/tokens/<brand>)")
		dryRun  = flag.Bool("dry-run", false, "Print planned writes without touching files")
		verbose = flag.Bool("v", false, "Verbose logs")
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
	if fileKey == "" {
		log.Error("no Figma file key — set FIGMA_FILE_KEY_" + bUp + "_GLYPH")
		os.Exit(1)
	}

	if *outDir == "" {
		*outDir = filepath.Join(repo.Root(), "lib/tokens", *brand)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	c := client.New(pat)

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
	// Noise filters:
	//   spacing/padding: ≥1px is real (auto-layout never uses sub-pixel), ≥3
	//     uses keeps the long tail honest.
	//   radius:          ≥2px filters out icon-vector micro-rounding (0.1–0.9
	//     showed up 100+ times, all from vector shapes), ≥4 uses requires a
	//     real component to actually use it.
	root["space"] = histogramToBucket(s.Spacing, "space", 1, 3)
	root["padding"] = histogramToBucket(s.Padding, "padding", 1, 3)
	root["radius"] = histogramToBucket(s.Radius, "radius", 2, 4)
	return root
}

// histogramToBucket emits one DTCG entry per distinct value, after filtering
// noise: minPx culls sub-pixel icon-vector rounding, minCount filters one-offs.
// Sub-pixel non-half values are also dropped — real spacing tokens land on
// whole or half pixels.
func histogramToBucket(h extractor.LayoutHistogram, prefix string, minPx float64, minCount int) map[string]any {
	bucket := map[string]any{}
	for _, vc := range h.Sorted() {
		if vc.Value < minPx || vc.Count < minCount {
			continue
		}
		// Skip sub-pixel: keep N or N.5 only.
		frac := vc.Value - float64(int64(vc.Value))
		if frac != 0 && frac != 0.5 {
			continue
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
