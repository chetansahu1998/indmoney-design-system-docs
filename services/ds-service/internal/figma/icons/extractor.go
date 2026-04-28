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
	Kind      string `json:"kind"`       // "icon" | "component" | "logo" | "illustration"
	Category  string `json:"category"`   // For kind=component: parent SECTION name. For others: source group ("2D", "bank", etc.).
	Source    string `json:"source"`     // "icons-fresh" | "atoms" | "design-system" | "bottom-sheet" — which page it came from
	Sets      string `json:"set_id"`     // Figma component_set node id
	VariantID string `json:"variant_id"` // The variant id we exported
	File      string `json:"file"`       // "download-cloud.svg" (relative to /icons/glyph/)
	WidthPx   int    `json:"width"`
	HeightPx  int    `json:"height"`

	// — Atomic-design tier classification —
	// Tier sorts components into atomic-design strata so /components can
	// surface PARENT components (organisms) by default and let designers
	// drill into atoms separately. Empty for kind=icon|logo|illustration
	// (those aren't tiered). For kind=component:
	//   "atom"     — single primitive (Buttons page, Input Field, …)
	//   "molecule" — pattern composed of atoms (Bottom Sheet headers, list rows)
	//   "parent"   — final consumed component (Toast, Status Bar, Footer CTA, Masthead/*)
	// Source of truth is the page the COMPONENT_SET lives on, with a
	// name-pattern fallback for cross-shelved sets (e.g. Masthead/Hot
	// hosted under Atoms is still parent-tier).
	Tier   string `json:"tier,omitempty"`
	Page   string `json:"page,omitempty"`    // page name for human-readable provenance
	PageID string `json:"page_id,omitempty"` // page node id for cross-references
}

// PageSpec describes one input page to extract icons from.
type PageSpec struct {
	NodeID     string // Figma page node id
	PageName   string // human-readable name written into Icon.Page (defaults to source)
	Source     string // friendly source name ("icons-fresh", "atoms", "design-system", "bottom-sheet")
	NamePrefix string // strip if present (e.g. "Icons/" for Icons Fresh)
	// Tier is the default tier stamped on every COMPONENT_SET found on
	// this page. Leave empty for icon/logo/illustration pages — they
	// aren't part of the atomic-design hierarchy.
	Tier string
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
		kind     string // "icon" | "component" | "logo" | "illustration"
		category string
		source   string
		slug     string
		display  string
		tier     string // "atom" | "molecule" | "parent" (empty for non-component kinds)
		pageName string
		pageID   string
	}

	var sets []setEntry

	// parentNameSet collects every COMPONENT_SET name found on a page with
	// Tier=parent during the first pass. After all pages are walked, any
	// atom-page set whose name matches a parent name (or matches a known
	// parent-prefix pattern like "Masthead/*") is lifted to parent tier.
	// This honors Figma's hierarchical naming convention — a/b/c paths
	// with "/" separators — without hardcoding which atoms get promoted.
	parentNameSet := map[string]bool{}
	parentPrefixes := []string{"Masthead/"} // path-prefix lift (configurable)

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

		// Walk children: COMPONENT_SETs at top-level OR nested inside SECTIONs.
		// We thread the enclosing SECTION name down: any COMPONENT_SET reached
		// while inside a SECTION is treated as a component, with the SECTION
		// name as its category. Top-level (no SECTION ancestor) sets are then
		// classified by name heuristics — bank, merchant, masthead, sub-brand
		// logo, or fall through to a generic "ui" bucket.
		//
		// kind is decided here, in one place, so the manifest carries it.
		var collectSets func(node map[string]any, sectionName string)
		collectSets = func(node map[string]any, sectionName string) {
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
					rest := strings.TrimPrefix(setName, page.NamePrefix)
					category, display = splitIconName(rest)
				}

				// Decide kind + category. Single source of truth, priority-ordered:
				//
				//   1. Page declares a Tier → kind=component (page.Tier wins
				//      regardless of SECTION wrapper). Design System and
				//      Bottom Sheet pages are component-only; their SECTION
				//      wrappers are mostly cosmetic and often unnamed.
				//   2. Inside a named SECTION → component (SECTION name is
				//      the category). Atoms page uses this — its SECTIONs
				//      ARE the categories ("Buttons", "Input Field", …).
				//   3. Category prefix from the set name says it's a logo/illustration.
				//   4. Name heuristics catch banks / merchants / mastheads / sub-brand wordmarks.
				//   5. icons-fresh source with Icon/Filled Icons category → icon.
				//   6. Default falls through to "icon" — the safest bucket.
				kind := ""
				if page.Tier != "" {
					kind = "component"
					// Category for parent/molecule pages: prefer the path-
					// prefix from the set name (Figma's "/" hierarchy),
					// then SECTION name if present, then "uncategorized".
					if cat, _ := splitIconName(setName); cat != "" && cat != "uncategorized" {
						category = cat
					} else if sectionName != "" {
						category = sectionName
					} else {
						category = page.PageName
						if category == "" {
							category = "uncategorized"
						}
					}
					display = setName
				} else if sectionName != "" {
					kind = "component"
					category = sectionName
					display = setName
				} else if isLogoNameLike(setName, category) {
					kind = "logo"
					if category == "uncategorized" || category == "" {
						category = "Logo"
					}
				} else if isBankLike(setName) {
					kind = "logo"
					category = "bank"
				} else if isMerchantLike(setName) {
					kind = "logo"
					category = "merchant"
				} else if isMastheadLike(setName) {
					kind = "illustration"
					category = "Masthead"
				} else if isIllustrationByDisplayName(display) {
					// PRIMARY illustration signal — the display name
					// itself carries "2D · …" / "3D · …" / "Profilecard …".
					// In the Glyph file this naming is reserved for the
					// genuinely illustrated assets; siblings in the same
					// 2D/3D folder without this prefix are stroke icons
					// (most are 24×24 line glyphs). Path-prefix awareness
					// — Figma's "node/node/property/…" convention — is
					// honored: we override category to the canonical
					// folder name so the illustration page groups them
					// cleanly.
					kind = "illustration"
					lowerDisplay := strings.ToLower(display)
					if strings.HasPrefix(lowerDisplay, "3d") {
						category = "3D"
					} else if strings.HasPrefix(lowerDisplay, "2d") {
						category = "2D"
					}
				} else if isIllustrationCategory(category) && category != "2D" && category != "3D" {
					// Keep Profilecard, Wallet, Cold etc. as illustrations
					// by category since their folder names ARE descriptive.
					// Drop 2D/3D from this rule because in this file those
					// folders contain 24×24 stroke icons mixed with the
					// occasional named illustration.
					kind = "illustration"
				} else if isIconCategory(category) || category == "2D" || category == "3D" {
					// Items in 2D / 3D folders without the "X · Name"
					// display marker are stroke icons grouped by visual
					// style. Preserve the folder name as their category
					// so the icon page can offer "2D · 256" / "3D · 135"
					// browse buckets.
					kind = "icon"
				} else if page.Source == "icons-fresh" {
					// Icons Fresh page items without a recognized category
					// (no Logo/2D/3D/Icon prefix) — true monochrome icons.
					kind = "icon"
				} else {
					// Atoms top-level item with no signal — most likely a
					// branded / partner asset. Bias to logo so colors are
					// preserved; can re-classify later if it surfaces wrong.
					kind = "logo"
					if category == "uncategorized" || category == "" {
						category = "Logo"
					}
				}

				if display == "" {
					return
				}
				// Tier is page-driven for the component kinds; non-component
				// assets (icons, logos, illustrations) carry no tier. This
				// keeps the manifest honest about which entries participate
				// in the atomic-design hierarchy.
				tier := ""
				if kind == "component" && page.Tier != "" {
					tier = page.Tier
				}
				if tier == "parent" {
					parentNameSet[setName] = true
				}
				pageName := page.PageName
				if pageName == "" {
					pageName = page.Source
				}
				sets = append(sets, setEntry{
					setID:    setID,
					setName:  setName,
					kind:     kind,
					category: category,
					source:   page.Source,
					slug:     slugify(display),
					display:  display,
					tier:     tier,
					pageName: pageName,
					pageID:   page.NodeID,
				})
				return
			}
			if t == "SECTION" {
				secName, _ := node["name"].(string)
				children, _ := node["children"].([]any)
				for _, ch := range children {
					if m, ok := ch.(map[string]any); ok {
						collectSets(m, secName)
					}
				}
				return
			}
			if t == "FRAME" || t == "GROUP" {
				children, _ := node["children"].([]any)
				for _, ch := range children {
					if m, ok := ch.(map[string]any); ok {
						collectSets(m, sectionName)
					}
				}
			}
		}
		topChildren, _ := doc["children"].([]any)
		for _, ch := range topChildren {
			if m, ok := ch.(map[string]any); ok {
				collectSets(m, "")
			}
		}
		log.Info("page enumerated", "source", page.Source, "sets_so_far", len(sets))
	}

	// Tier name-pattern fallback. Any kind=component set still tiered as
	// atom whose name (a) appears verbatim on a parent-tier page or
	// (b) starts with a known parent prefix gets lifted to parent. This
	// respects Figma's "/"-path hierarchy — `Masthead/Hot` placed on the
	// Atoms page is still authored as a parent piece even if the file is
	// laid out in an atom-shaped place. Done before dedupe so the lifted
	// tier wins when the same slug appears on both pages.
	for i := range sets {
		s := &sets[i]
		if s.kind != "component" || s.tier == "parent" {
			continue
		}
		if parentNameSet[s.setName] {
			s.tier = "parent"
			continue
		}
		for _, prefix := range parentPrefixes {
			if strings.HasPrefix(s.setName, prefix) {
				s.tier = "parent"
				break
			}
		}
	}

	// Dedupe by slug. When a parent and an atom share a slug (e.g. the
	// same name appears on Atoms and Design System pages), keep the
	// PARENT entry — that's the canonical published surface. Otherwise
	// first-seen wins.
	{
		seen := map[string]int{} // slug → index in uniq
		uniq := sets[:0]
		for _, s := range sets {
			if existingIdx, ok := seen[s.slug]; ok {
				if s.tier == "parent" && uniq[existingIdx].tier != "parent" {
					uniq[existingIdx] = s
				}
				continue
			}
			seen[s.slug] = len(uniq)
			uniq = append(uniq, s)
		}
		sets = uniq
	}
	{
		// Per-tier counts for log honesty.
		counts := map[string]int{}
		for _, s := range sets {
			if s.kind == "component" {
				counts[s.tier]++
			} else {
				counts[s.kind]++
			}
		}
		log.Info("found icon sets total", "count", len(sets), "breakdown", counts)
	}

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
		preserve := preserveColorsForKind(sets[i].kind, sets[i].display)
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
			Kind:      s.kind,
			Category:  s.category,
			Source:    s.source,
			Sets:      s.setID,
			VariantID: v.variantID,
			File:      s.slug + ".svg",
			WidthPx:   v.width,
			HeightPx:  v.height,
			Tier:      s.tier,
			Page:      s.pageName,
			PageID:    s.pageID,
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

// preserveColorsForKind decides whether an asset's SVG should keep its
// native Figma fills. The kind classification lives in one place — the
// extractor's collectSets — and this function reads from it.
func preserveColorsForKind(kind, displayName string) bool {
	switch kind {
	case "component", "logo", "illustration":
		return true
	}
	// "icon" kind — but Glyph stuffs "3D · Car - Family" into the Icon
	// category, and those have real fills. Preserve them too.
	lower := strings.ToLower(displayName)
	if strings.HasPrefix(lower, "3d ") || strings.HasPrefix(lower, "3d·") || strings.HasPrefix(lower, "3d ·") {
		return true
	}
	return false
}

// isIconCategory matches names produced by Figma styles that actually
// contain monochrome system icons (Glyph publishes them under "Icon" or
// "Filled Icons" inside the Icons Fresh page).
func isIconCategory(category string) bool {
	switch category {
	case "Icon", "Filled Icons":
		return true
	}
	return false
}

// isIllustrationCategory captures legacy categories that came from set-name
// prefixes ("2D · …", "3D · …", "Profilecard", "Wallet", "Cold").
func isIllustrationCategory(category string) bool {
	switch category {
	case "2D", "3D", "Profilecard", "Wallet", "Cold":
		return true
	}
	return false
}

// isIllustrationByDisplayName detects illustrations by their display name
// when the path-prefix category misses them. Figma stores 2D and 3D
// illustrations with display names like "3D · Car - Family" or
// "2D · Foreclose"; if these get parented under an "Icon/" group the
// category becomes "Icon" and the isIconCategory branch would
// mis-classify them. Detecting the literal "2D" / "3D" / "Profilecard"
// prefix on the *display* name is the honest signal — the asset's
// rendering tells us what tier of detail it carries.
func isIllustrationByDisplayName(display string) bool {
	d := strings.TrimSpace(display)
	if d == "" {
		return false
	}
	lower := strings.ToLower(d)
	// Allow "3d ·", "3d-", "3d ", and the same for "2d". Case-insensitive
	// because Figma's authoring is inconsistent — "3D · Car", "3d · sip100"
	// both appear in the same file.
	prefixes := []string{"3d ·", "3d·", "3d-", "3d ", "2d ·", "2d·", "2d-", "2d ", "profilecard"}
	for _, p := range prefixes {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	return false
}

// isLogoNameLike picks up sub-brand and partner-logo COMPONENT_SETs — names
// like "Icons/Logo/INDpay", "Visa", "Diners", "UPI" — that aren't covered
// by the bank or merchant heuristics but are clearly logos.
func isLogoNameLike(setName, category string) bool {
	if category == "Logo" || category == "Logos" {
		return true
	}
	if strings.HasPrefix(setName, "Icons/Logo/") || strings.HasPrefix(strings.ToLower(setName), "icons/logo/") {
		return true
	}
	for _, kw := range []string{"INDmoney", "INDstocks", "INDglobal", "INDcredit", "INDpay", "INDwheels", "INDprotect",
		"Visa", "Mastercard", "Rupay", "Amex", "American express", "Diners", "Discover", "Apple pay", "Gpay",
		"UPI", "NSE", "BSE", "Bharat Connect", "Bharat connect"} {
		if strings.EqualFold(setName, kw) {
			return true
		}
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

// isMastheadLike detects large page-template COMPONENT_SETs that aren't
// reusable components — Glyph's "Mini App", "Dashboard", "Cold Masthead",
// "Insta Masthead". They're heavy raster mocks that belong with illustrations.
func isMastheadLike(name string) bool {
	lower := strings.ToLower(name)
	for _, kw := range []string{"mini app", "dashboard", "masthead", "ind community", "iso "} {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}
