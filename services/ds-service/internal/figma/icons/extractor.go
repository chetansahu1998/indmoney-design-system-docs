// Package icons extracts icon SVGs from Glyph's Icons Fresh page.
//
// Each icon is a Figma COMPONENT_SET with 14 variants (7 sizes × 2 BG modes).
// We extract the BG=No, Size=24px variant per set as the canonical icon SVG,
// then post-process to replace fixed fill/stroke colors with `currentColor`
// so designers can recolor through CSS without re-exporting.
package icons

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/figma/client"
)

// Icon is one icon's metadata in the manifest.
type Icon struct {
	Slug      string `json:"slug"`       // "download-cloud"
	Name      string `json:"name"`       // "Download Cloud"
	Category  string `json:"category"`   // "2D"
	Sets      string `json:"set_id"`     // Figma component_set node id
	VariantID string `json:"variant_id"` // The 24px BG=No variant id we exported
	File      string `json:"file"`       // "download-cloud.svg" (relative to /icons/glyph/)
	WidthPx   int    `json:"width"`
	HeightPx  int    `json:"height"`
}

// variantInfo is the chosen export variant per icon set.
type variantInfo struct {
	variantID string
	width     int
	height    int
}

// Manifest is the JSON written to public/icons/glyph/manifest.json.
type Manifest struct {
	GeneratedAt time.Time `json:"generated_at"`
	FileKey     string    `json:"file_key"`
	PageNodeID  string    `json:"page_node_id"`
	Icons       []Icon    `json:"icons"`
	Categories  []string  `json:"categories"`
}

// Extract downloads all 24px BG=No variants from the given Icons page.
//
// outDir is the public/icons/glyph/ directory in the repo.
// Concurrency is rate-limited at concurrencyLimit; Figma's image endpoint
// allows ~100 IDs per call but the S3 downloads can saturate the network.
func Extract(ctx context.Context, c *client.Client, fileKey, iconsPageID, outDir string, log *slog.Logger) (*Manifest, error) {
	// 1. Fetch the page at depth=2 to enumerate component sets
	resp, err := c.GetFileNodes(ctx, fileKey, []string{iconsPageID}, 2)
	if err != nil {
		return nil, fmt.Errorf("get icons page: %w", err)
	}
	page, _ := resp["nodes"].(map[string]any)
	if page == nil {
		return nil, fmt.Errorf("no nodes in response")
	}
	var doc map[string]any
	for _, v := range page {
		if m, ok := v.(map[string]any); ok && m != nil {
			doc, _ = m["document"].(map[string]any)
			break
		}
	}
	if doc == nil {
		return nil, fmt.Errorf("missing document")
	}

	type setEntry struct {
		setID    string
		setName  string
		category string
		slug     string
		display  string
	}
	children, _ := doc["children"].([]any)
	var sets []setEntry
	for _, ch := range children {
		m, _ := ch.(map[string]any)
		if m == nil || m["type"] != "COMPONENT_SET" {
			continue
		}
		setID, _ := m["id"].(string)
		setName, _ := m["name"].(string)
		// "Icons/ 2D/ Download Cloud" → category="2D", display="Download Cloud", slug="download-cloud"
		category, display := splitIconName(setName)
		if display == "" {
			continue
		}
		sets = append(sets, setEntry{
			setID:    setID,
			setName:  setName,
			category: category,
			slug:     slugify(display),
			display:  display,
		})
	}
	log.Info("found icon sets", "count", len(sets))

	// 2. For each set, fetch its variants at depth=2 and pick "BG=No, Size=24px"
	// We batch the variant lookups
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", outDir, err)
	}

	// Fetch variants in batches of 30 sets at a time (each set has ~14 variants;
	// fetching 30 sets = ~420 variants which is well within Figma's response size).
	variants := make([]variantInfo, len(sets))
	const batchSize = 25
	for i := 0; i < len(sets); i += batchSize {
		end := i + batchSize
		if end > len(sets) {
			end = len(sets)
		}
		ids := make([]string, end-i)
		for j := i; j < end; j++ {
			ids[j-i] = sets[j].setID
		}
		setsResp, err := c.GetFileNodes(ctx, fileKey, ids, 2)
		if err != nil {
			log.Warn("get sets batch failed; skipping", "i", i, "err", err)
			continue
		}
		setsNodes, _ := setsResp["nodes"].(map[string]any)
		for j := i; j < end; j++ {
			payload, ok := setsNodes[sets[j].setID].(map[string]any)
			if !ok || payload == nil {
				continue
			}
			setDoc, _ := payload["document"].(map[string]any)
			if setDoc == nil {
				continue
			}
			vchildren, _ := setDoc["children"].([]any)
			variants[j] = pickBestVariant(vchildren)
		}
		log.Info("variants resolved", "progress", fmt.Sprintf("%d/%d", end, len(sets)))
	}

	// 3. Get S3 SVG URLs in batches of 100
	svgURLs := make(map[string]string, len(sets))
	const imgBatch = 80
	pendingIDs := []string{}
	idxByID := map[string]int{}
	for i, v := range variants {
		if v.variantID == "" {
			continue
		}
		pendingIDs = append(pendingIDs, v.variantID)
		idxByID[v.variantID] = i
	}
	for i := 0; i < len(pendingIDs); i += imgBatch {
		end := i + imgBatch
		if end > len(pendingIDs) {
			end = len(pendingIDs)
		}
		urls, err := getImageURLs(ctx, c, fileKey, pendingIDs[i:end])
		if err != nil {
			log.Warn("image batch failed", "i", i, "err", err)
			continue
		}
		for k, v := range urls {
			svgURLs[k] = v
		}
		log.Info("svg urls resolved", "progress", fmt.Sprintf("%d/%d", end, len(pendingIDs)))
	}

	// 4. Download each SVG, post-process for currentColor, write to disk
	type result struct {
		idx int
		err error
	}
	resultCh := make(chan result, len(variants))
	sema := make(chan struct{}, 10) // max 10 concurrent downloads
	var wg sync.WaitGroup

	for i, v := range variants {
		if v.variantID == "" {
			continue
		}
		url := svgURLs[v.variantID]
		if url == "" {
			continue
		}
		wg.Add(1)
		sema <- struct{}{}
		go func(i int, url string) {
			defer wg.Done()
			defer func() { <-sema }()
			err := downloadAndWrite(ctx, url, filepath.Join(outDir, sets[i].slug+".svg"))
			resultCh <- result{idx: i, err: err}
		}(i, url)
	}
	wg.Wait()
	close(resultCh)

	successCount := 0
	failCount := 0
	for r := range resultCh {
		if r.err != nil {
			log.Warn("download failed", "slug", sets[r.idx].slug, "err", r.err)
			failCount++
		} else {
			successCount++
		}
	}
	log.Info("download summary", "ok", successCount, "failed", failCount)

	// 5. Build manifest
	categorySet := map[string]bool{}
	icons := make([]Icon, 0, len(sets))
	for i, s := range sets {
		v := variants[i]
		if v.variantID == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(outDir, s.slug+".svg")); err != nil {
			continue // download failed; skip from manifest
		}
		icons = append(icons, Icon{
			Slug:      s.slug,
			Name:      s.display,
			Category:  s.category,
			Sets:      s.setID,
			VariantID: v.variantID,
			File:      s.slug + ".svg",
			WidthPx:   v.width,
			HeightPx:  v.height,
		})
		categorySet[s.category] = true
	}
	sort.Slice(icons, func(i, j int) bool { return icons[i].Slug < icons[j].Slug })

	categories := make([]string, 0, len(categorySet))
	for c := range categorySet {
		categories = append(categories, c)
	}
	sort.Strings(categories)

	manifest := &Manifest{
		GeneratedAt: time.Now().UTC(),
		FileKey:     fileKey,
		PageNodeID:  iconsPageID,
		Icons:       icons,
		Categories:  categories,
	}

	manifestBytes, _ := json.MarshalIndent(manifest, "", "  ")
	if err := os.WriteFile(filepath.Join(outDir, "manifest.json"), manifestBytes, 0o644); err != nil {
		return nil, fmt.Errorf("write manifest: %w", err)
	}
	log.Info("manifest written", "icons", len(icons), "categories", len(categories), "path", filepath.Join(outDir, "manifest.json"))

	return manifest, nil
}

// pickBestVariant chooses the most representative COMPONENT child from a set:
//
//   1. BG=No + Size=24 (the canonical icon variant)
//   2. BG=No + any size in [20, 24, 32, 16, 40, 48, 64]
//   3. Any variant + Size=24
//   4. Any variant matching a known size
//   5. The first COMPONENT child as last resort
//
// This catches the ~574 sets that weren't strict-matched in the first pass.
func pickBestVariant(vchildren []any) variantInfo {
	type cand struct {
		id   string
		name string
		w, h int
	}
	var cands []cand
	for _, vc := range vchildren {
		vm, _ := vc.(map[string]any)
		if vm == nil || vm["type"] != "COMPONENT" {
			continue
		}
		vid, _ := vm["id"].(string)
		vname, _ := vm["name"].(string)
		bbox, _ := vm["absoluteBoundingBox"].(map[string]any)
		w := 24
		h := 24
		if bbox != nil {
			if wv, ok := bbox["width"].(float64); ok {
				w = int(wv)
			}
			if hv, ok := bbox["height"].(float64); ok {
				h = int(hv)
			}
		}
		cands = append(cands, cand{id: vid, name: vname, w: w, h: h})
	}
	if len(cands) == 0 {
		return variantInfo{}
	}

	// Score each candidate; higher is better
	bestScore := -1
	bestIdx := 0
	for i, c := range cands {
		score := 0
		nm := strings.ToLower(c.name)
		if strings.Contains(nm, "bg=no") {
			score += 100
		}
		if strings.Contains(nm, "size=24") {
			score += 50
		} else if strings.Contains(nm, "size=20") {
			score += 40
		} else if strings.Contains(nm, "size=32") {
			score += 35
		} else if strings.Contains(nm, "size=16") {
			score += 30
		} else if strings.Contains(nm, "size=40") {
			score += 25
		}
		// Prefer reasonable bbox sizes too
		if c.w >= 16 && c.w <= 32 {
			score += 5
		}
		if score > bestScore {
			bestScore = score
			bestIdx = i
		}
	}
	c := cands[bestIdx]
	return variantInfo{variantID: c.id, width: c.w, height: c.h}
}

// getImageURLs calls /v1/images for a batch of node IDs and returns id → S3 URL.
func getImageURLs(ctx context.Context, c *client.Client, fileKey string, ids []string) (map[string]string, error) {
	idsCSV := strings.Join(ids, ",")
	path := fmt.Sprintf("/v1/images/%s?ids=%s&format=svg&svg_simplify_stroke=true&svg_outline_text=false", fileKey, idsCSV)
	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.figma.com"+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Figma-Token", c.Token())
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 5<<20))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("images: %d %s", resp.StatusCode, string(body))
	}
	var parsed struct {
		Err    any               `json:"err"`
		Images map[string]string `json:"images"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	if parsed.Err != nil {
		return nil, fmt.Errorf("api error: %v", parsed.Err)
	}
	return parsed.Images, nil
}

// downloadAndWrite fetches the SVG from the S3 URL, post-processes for
// currentColor, and writes to dst.
func downloadAndWrite(ctx context.Context, url, dst string) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("download %d", resp.StatusCode)
	}
	svg, err := io.ReadAll(io.LimitReader(resp.Body, 5<<20))
	if err != nil {
		return err
	}
	// Post-process: every fill="#XXXXXX" or stroke="#XXXXXX" → currentColor
	// EXCEPT fill="none" / stroke="none" which we preserve.
	processed := postProcessForCurrentColor(svg)
	return os.WriteFile(dst, processed, 0o644)
}

var (
	fillRe   = regexp.MustCompile(`fill="(#[0-9A-Fa-f]{3,8}|rgb\([^)]+\)|black|white)"`)
	strokeRe = regexp.MustCompile(`stroke="(#[0-9A-Fa-f]{3,8}|rgb\([^)]+\)|black|white)"`)
)

// postProcessForCurrentColor replaces hex/rgb fill+stroke attributes with currentColor.
// fill="none" and stroke="none" are preserved.
func postProcessForCurrentColor(svg []byte) []byte {
	out := fillRe.ReplaceAll(svg, []byte(`fill="currentColor"`))
	out = strokeRe.ReplaceAll(out, []byte(`stroke="currentColor"`))
	return out
}

// splitIconName parses "Icons/ 2D/ Download Cloud" → ("2D", "Download Cloud").
// Falls back to ("uncategorized", whole-name) if pattern doesn't match.
func splitIconName(name string) (category, display string) {
	parts := strings.Split(name, "/")
	cleaned := make([]string, 0, len(parts))
	for _, p := range parts {
		s := strings.TrimSpace(p)
		if s != "" {
			cleaned = append(cleaned, s)
		}
	}
	if len(cleaned) >= 3 && strings.EqualFold(cleaned[0], "Icons") {
		return cleaned[1], strings.Join(cleaned[2:], " · ")
	}
	if len(cleaned) >= 2 {
		return cleaned[0], strings.Join(cleaned[1:], " · ")
	}
	return "uncategorized", name
}

func slugify(s string) string {
	s = strings.ToLower(s)
	s = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	return s
}
