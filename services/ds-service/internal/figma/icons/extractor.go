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
	Category  string `json:"category"`   // "2D" | "bank" | "merchant" | "stock" | "ui"
	Source    string `json:"source"`     // "icons-fresh" | "atoms" — which page it came from
	Sets      string `json:"set_id"`     // Figma component_set node id
	VariantID string `json:"variant_id"` // The variant id we exported
	File      string `json:"file"`       // "download-cloud.svg" (relative to /icons/glyph/)
	WidthPx   int    `json:"width"`
	HeightPx  int    `json:"height"`
}

// PageSpec describes one input page to extract icons from.
type PageSpec struct {
	NodeID  string // Figma page node id
	Source  string // friendly source name ("icons-fresh", "atoms")
	NamePrefix string // strip if present (e.g. "Icons/" for Icons Fresh)
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

// Extract downloads icons from one or more Figma pages.
//
// pages is a list of (node_id, source_name, name_prefix) tuples — typically
// the Icons Fresh page + the Atoms page (where banks/merchants/etc. live).
// Each set's category is derived from its name prefix or assigned by the
// source's namePrefix override.
func Extract(ctx context.Context, c *client.Client, fileKey string, pages []PageSpec, outDir string, log *slog.Logger) (*Manifest, error) {
	type setEntry struct {
		setID    string
		setName  string
		category string
		source   string
		slug     string
		display  string
	}

	var sets []setEntry

	// 1. Walk each page, enumerate COMPONENT_SETs at the top level + within SECTIONs
	for _, page := range pages {
		resp, err := c.GetFileNodes(ctx, fileKey, []string{page.NodeID}, 2)
		if err != nil {
			log.Warn("get page failed; skipping", "page", page.Source, "err", err)
			continue
		}
		nodes, _ := resp["nodes"].(map[string]any)
		var doc map[string]any
		for _, v := range nodes {
			if m, ok := v.(map[string]any); ok && m != nil {
				doc, _ = m["document"].(map[string]any)
				break
			}
		}
		if doc == nil {
			log.Warn("no document in page response", "page", page.Source)
			continue
		}

		// Walk children: COMPONENT_SETs at top-level OR inside SECTIONs
		var collectSets func(node map[string]any)
		collectSets = func(node map[string]any) {
			if node == nil {
				return
			}
			t, _ := node["type"].(string)
			if t == "COMPONENT_SET" {
				setID, _ := node["id"].(string)
				setName, _ := node["name"].(string)
				if setName == "" || setID == "" {
					return
				}
				category, display := splitIconName(setName)
				if page.NamePrefix != "" && strings.HasPrefix(setName, page.NamePrefix) {
					// Use the rest of the name after prefix as display
					rest := strings.TrimPrefix(setName, page.NamePrefix)
					category, display = splitIconName(rest)
				}
				if category == "uncategorized" || category == strings.ToLower(category) {
					// Inherit from source if no explicit category
					if page.Source == "atoms" {
						// Atoms page items default to "ui" except names matching banks
						if isBankLike(setName) {
							category = "bank"
						} else if isMerchantLike(setName) {
							category = "merchant"
						} else {
							category = "ui"
						}
					}
				}
				if display == "" {
					return
				}
				sets = append(sets, setEntry{
					setID:    setID,
					setName:  setName,
					category: category,
					source:   page.Source,
					slug:     slugify(display),
					display:  display,
				})
				return
			}
			if t == "SECTION" || t == "FRAME" || t == "GROUP" {
				children, _ := node["children"].([]any)
				for _, ch := range children {
					if m, ok := ch.(map[string]any); ok {
						collectSets(m)
					}
				}
			}
		}
		topChildren, _ := doc["children"].([]any)
		for _, ch := range topChildren {
			if m, ok := ch.(map[string]any); ok {
				collectSets(m)
			}
		}
		log.Info("page enumerated", "source", page.Source, "sets_so_far", len(sets))
	}

	// Dedupe by slug — multiple pages can have the same slug (e.g. "download-cloud" in both Icons Fresh and Atoms)
	{
		seen := map[string]bool{}
		uniq := sets[:0]
		for _, s := range sets {
			if seen[s.slug] {
				continue
			}
			seen[s.slug] = true
			uniq = append(uniq, s)
		}
		sets = uniq
	}
	log.Info("found icon sets total", "count", len(sets))

	// 2. For each set, fetch its variants at depth=2 and pick "BG=No, Size=24px"
	// We batch the variant lookups
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", outDir, err)
	}

	// 2. Fetch variants in batches of 25 sets at a time, with 429 retry (each set has ~14 variants;
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
		// Retry on 429 with exponential backoff
		var setsResp map[string]any
		for attempt := 0; attempt < 4; attempt++ {
			r, err := c.GetFileNodes(ctx, fileKey, ids, 2)
			if err == nil {
				setsResp = r
				break
			}
			if !is429Error(err) {
				log.Warn("get sets batch failed (non-429); skipping", "i", i, "err", err)
				break
			}
			wait := time.Duration(1<<attempt) * 5 * time.Second
			log.Info("rate limited, backing off", "i", i, "attempt", attempt+1, "wait", wait)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(wait):
			}
		}
		if setsResp == nil {
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
		preserve := preserveColorsForAsset(sets[i].category, sets[i].display)
		go func(i int, url string, preserve bool) {
			defer wg.Done()
			defer func() { <-sema }()
			err := downloadAndWrite(ctx, url, filepath.Join(outDir, sets[i].slug+".svg"), preserve)
			resultCh <- result{idx: i, err: err}
		}(i, url, preserve)
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
			Source:    s.source,
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

	pageIDs := make([]string, 0, len(pages))
	for _, p := range pages {
		pageIDs = append(pageIDs, p.NodeID)
	}
	manifest := &Manifest{
		GeneratedAt: time.Now().UTC(),
		FileKey:     fileKey,
		PageNodeID:  strings.Join(pageIDs, ","),
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

// downloadAndWrite fetches the SVG and writes it to dst. When preserveColors
// is true, the original fills are kept (illustrations, logos, multi-color
// components). When false, fixed fill+stroke colors are flattened to
// currentColor so the asset themes via CSS color.
func downloadAndWrite(ctx context.Context, url, dst string, preserveColors bool) error {
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
	if !preserveColors {
		svg = postProcessForCurrentColor(svg)
	}
	return os.WriteFile(dst, svg, 0o644)
}

// preserveColorsForAsset returns true when the SVG should keep its original
// Figma fills (illustrations, logos, multi-color components, and the "3D ·"
// named entries that live inside the Icon category but are full color art).
// Returns false for true monochrome system icons that benefit from
// currentColor recoloring via CSS.
func preserveColorsForAsset(category, displayName string) bool {
	switch category {
	case "Logo", "Logos", "bank", "nvidia", "merchant",
		"2D", "3D", "Profilecard", "Wallet", "Cold",
		"ui":
		return true
	}
	// Glyph stuffs "3D · Car - Family", "3D · Cash - New", etc. into the Icon
	// category. They're 24×24 but the source has real fills — preserve them.
	lower := strings.ToLower(displayName)
	if strings.HasPrefix(lower, "3d ") || strings.HasPrefix(lower, "3d·") || strings.HasPrefix(lower, "3d ·") {
		return true
	}
	return false
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

func is429Error(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "429") || strings.Contains(err.Error(), "rate limit")
}

// isBankLike heuristically detects bank logos from set names.
func isBankLike(name string) bool {
	lower := strings.ToLower(name)
	for _, kw := range []string{"bank", "icici", "hdfc", "axis", "kotak", "punjab", "canara", "indusind", "yes ", "idbi", "uco", "federal", "syndicate", "vijaya", "dena", "allahabad", "andhra", "corporation", "central", "oriental", "lakshmi", "karnataka", "karur", "saraswat", "south indian", "city union", "paytm payments", "fino", "rbl", "bandhan", "csb", "tamilnad", "deutsche", "hsbc", "citi", "stanchart", "standard chartered", "barclays", "scotia", "bnp"} {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// isMerchantLike detects payment app / merchant brand logos.
func isMerchantLike(name string) bool {
	lower := strings.ToLower(name)
	for _, kw := range []string{"phonepe", "google pay", "gpay", "paytm", "amazon pay", "bhim", "upi", "razorpay", "cred", "mobikwik", "freecharge"} {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}
