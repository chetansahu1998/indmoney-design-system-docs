// Command variants enumerates every variant of every kind=component set in
// the icons manifest, downloads each variant SVG, parses the Figma "Prop=Val"
// name format into structured properties, and writes the result back into
// public/icons/glyph/manifest.json under each entry's `variants` field.
//
// Pipeline:
//   1. Load existing manifest.json.
//   2. Filter to entries whose category indicates kind=component (today: "ui").
//   3. Fetch each set's children via /v1/files/:key/nodes?ids=...&depth=1.
//   4. Collect variant ids + parsed names.
//   5. Batch-resolve SVG URLs via /v1/images/:key.
//   6. Download each SVG into public/icons/glyph/variants/.
//   7. Re-write manifest.json with the variants array per entry.
//
// Usage:
//   go run ./cmd/variants
//   go run ./cmd/variants --kind component
//   go run ./cmd/variants --max 5     # debug: limit sets processed
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/figma/client"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/figma/repo"
)

// Legacy fallback for manifests that lack the kind field on each entry.
// New manifests carry `kind` directly; this set is only consulted when the
// kind field is absent.
var legacyComponentCategories = map[string]bool{"ui": true}

type Variant struct {
	// — legacy fields (kept verbatim for backwards compat with manifest.ts) —
	Name       string              `json:"name"`
	Properties []map[string]string `json:"properties"`
	VariantID  string              `json:"variant_id"`
	File       string              `json:"file"`
	Width      int                 `json:"width"`
	Height     int                 `json:"height"`

	// — rich fields — populated when the deep-fetch path runs.
	// Key is the durable Variable.key analogue for components — stable
	// across publishes, the only safe identifier for cross-file refs.
	Key string `json:"key,omitempty"`
	// AxisValues is the parsed VARIANT axis tuple, e.g.
	// {"State":"Default","Size":"Large"}. Strict subset of Properties
	// but typed as a map for direct lookup.
	AxisValues map[string]string `json:"axis_values,omitempty"`
	// IsDefault marks the variant Figma considers the default — the
	// spatially top-left one (REST doesn't expose Plugin's
	// `defaultVariant`, so we reconstruct).
	IsDefault bool `json:"is_default,omitempty"`
	// Layout, Fills, Strokes, Effects, Corner, Opacity, BoundVariables,
	// Children describe the variant's root frame — what makes it look
	// the way it does. All optional; populated by the deep-fetch path.
	Layout         *LayoutInfo       `json:"layout,omitempty"`
	Fills          []FillInfo        `json:"fills,omitempty"`
	Strokes        []FillInfo        `json:"strokes,omitempty"`
	StrokeWeight   float64           `json:"stroke_weight,omitempty"`
	Effects        []EffectInfo      `json:"effects,omitempty"`
	Corner         *CornerInfo       `json:"corner,omitempty"`
	Opacity        float64           `json:"opacity,omitempty"`
	BoundVariables map[string]string `json:"bound_variables,omitempty"`
	Children       []ChildSummary    `json:"children,omitempty"`
	// Composes is the list of every INSTANCE found while walking this
	// variant's node tree. Populated only when the parent entry's tier
	// is "parent" (atom variants have no INSTANCEs by definition;
	// molecules sometimes do but we don't surface that yet). Resolved
	// AtomSlug is filled in a post-pass that cross-references against
	// the atoms table.
	Composes []CompositionRef `json:"composes,omitempty"`
}

type IconEntry struct {
	// — legacy fields —
	Slug      string    `json:"slug"`
	Name      string    `json:"name"`
	Kind      string    `json:"kind,omitempty"`
	Category  string    `json:"category"`
	Source    string    `json:"source,omitempty"`
	SetID     string    `json:"set_id"`
	VariantID string    `json:"variant_id"`
	File      string    `json:"file"`
	Width     int       `json:"width"`
	Height    int       `json:"height"`
	Variants  []Variant `json:"variants,omitempty"`

	// Tier / Page / PageID echo the icons.Icon fields. cmd/variants only
	// reads them — the values are decided once during cmd/icons. They're
	// repeated here so the manifest survives a roundtrip through
	// cmd/variants without dropping fields.
	Tier   string `json:"tier,omitempty"`
	Page   string `json:"page,omitempty"`
	PageID string `json:"page_id,omitempty"`

	// — rich fields, populated by the deep-fetch path —
	// Key is the COMPONENT_SET's durable identifier from
	// /v1/files/:key/components — stays stable across publishes.
	Key string `json:"key,omitempty"`
	// Description is the markdown documentation entered in Figma's
	// "Description" field on the component set. Often empty for
	// internal components.
	Description string `json:"description,omitempty"`
	// DocLinks are the Figma `documentationLinks` URLs (e.g. Storybook,
	// internal wiki). Order preserved from Figma.
	DocLinks []string `json:"doc_links,omitempty"`
	// PropDefs is the full componentPropertyDefinitions map flattened
	// to a sorted slice (sorted by Name for stable JSON output). Carries
	// all 4 property types — VARIANT/BOOLEAN/TEXT/INSTANCE_SWAP — with
	// the `#suffix` preserved on non-VARIANT names.
	PropDefs []ComponentProperty `json:"prop_defs,omitempty"`
	// VariantAxes is the matrix view of the VARIANT properties: per-axis
	// values + default. For consumers that want "show me the axes" UI
	// without parsing the verbose PropDefs.
	VariantAxes []VariantAxis `json:"variant_axes,omitempty"`
	// SingleVariantSet flags COMPONENT_SETs with exactly one child
	// COMPONENT — they look like standalone components in the UI but
	// the API treats them differently (the property defs live on the
	// parent set). Useful for the docs site to render them as
	// "single-variant" rather than "Variant 1 of 1".
	SingleVariantSet bool `json:"single_variant_set,omitempty"`
}

type Manifest struct {
	GeneratedAt string      `json:"generated_at"`
	FileKey     string      `json:"file_key"`
	PageNodeID  string      `json:"page_node_id"`
	Icons       []IconEntry `json:"icons"`
	Categories  []string    `json:"categories"`
}

func main() {
	var (
		kind     = flag.String("kind", "component", "Asset kind to enumerate variants for")
		max      = flag.Int("max", 0, "Limit number of sets processed (0 = all)")
		manifest = flag.String("manifest", "", "Path to manifest.json (default: <repo>/public/icons/glyph/manifest.json)")
		variants = flag.String("variants-dir", "", "Output dir for variant SVGs (default: <manifest dir>/variants)")
		verbose  = flag.Bool("v", false, "Verbose logging")
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
	fileKey := firstEnv("FIGMA_FILE_KEY_INDMONEY_GLYPH", "FIGMA_FILE_KEY_INDMONEY")
	if fileKey == "" {
		log.Error("FIGMA_FILE_KEY_INDMONEY_GLYPH not set")
		os.Exit(1)
	}

	if *manifest == "" {
		*manifest = filepath.Join(repo.Root(), "public/icons/glyph/manifest.json")
	}
	if *variants == "" {
		*variants = filepath.Join(filepath.Dir(*manifest), "variants")
	}
	if err := os.MkdirAll(*variants, 0o755); err != nil {
		log.Error("mkdir variants", "err", err)
		os.Exit(1)
	}

	raw, err := os.ReadFile(*manifest)
	if err != nil {
		log.Error("read manifest", "err", err)
		os.Exit(1)
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		log.Error("parse manifest", "err", err)
		os.Exit(1)
	}

	// Filter targets
	var targets []int
	for i, e := range m.Icons {
		if !entryMatchesKind(e, *kind) {
			continue
		}
		targets = append(targets, i)
		if *max > 0 && len(targets) >= *max {
			break
		}
	}
	log.Info("variants run", "kind", *kind, "sets", len(targets))

	if len(targets) == 0 {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	c := client.New(pat)

	// 0. Fetch durable per-node metadata: /components + /component_sets
	// give us {node_id → {key, description}} for every published item in
	// this file. The /nodes/?ids=… path also has key/description but they
	// live nested inside `document` and miss when extraction edits aren't
	// yet published; the index endpoints are the source of truth here.
	keyByNodeID, descByNodeID := fetchPublishedIndex(ctx, c, fileKey, log)

	// 1. Resolve children of each COMPONENT_SET in batches of 25
	type variantKey struct {
		entryIdx int
		variant  Variant
	}
	pending := make([]variantKey, 0, len(targets)*4)

	const setBatch = 25
	for start := 0; start < len(targets); start += setBatch {
		end := start + setBatch
		if end > len(targets) {
			end = len(targets)
		}
		ids := make([]string, 0, end-start)
		for _, idx := range targets[start:end] {
			ids = append(ids, m.Icons[idx].SetID)
		}
		resp, err := getNodesWithRetry(ctx, c, fileKey, ids, log)
		if err != nil {
			log.Warn("batch failed; skipping", "start", start, "err", err)
			continue
		}
		nodes, _ := resp["nodes"].(map[string]any)
		for _, idx := range targets[start:end] {
			setID := m.Icons[idx].SetID
			wrap, _ := nodes[setID].(map[string]any)
			if wrap == nil {
				continue
			}
			doc, _ := wrap["document"].(map[string]any)
			if doc == nil {
				continue
			}

			// — Set-level rich data —
			// componentPropertyDefinitions lives on the COMPONENT_SET (or
			// on a standalone COMPONENT). All four property types
			// (VARIANT, BOOLEAN, TEXT, INSTANCE_SWAP) are extracted with
			// their `#suffix` preserved so plugin-runtime setProperties
			// keeps working across changes.
			if defs, ok := doc["componentPropertyDefinitions"].(map[string]any); ok {
				m.Icons[idx].PropDefs = parseComponentPropertyDefinitions(defs)
			}
			// Description: prefer the /component_sets index entry (richer,
			// survives node-document quirks); fall back to the document's
			// inline description field.
			if d, ok := descByNodeID[setID]; ok && d != "" {
				m.Icons[idx].Description = d
			} else if desc, ok := doc["description"].(string); ok && desc != "" {
				m.Icons[idx].Description = desc
			}
			// Durable set key — only present once the file is published.
			if k, ok := keyByNodeID[setID]; ok && k != "" {
				m.Icons[idx].Key = k
			}
			if links, ok := doc["documentationLinks"].([]any); ok {
				for _, l := range links {
					lm, _ := l.(map[string]any)
					if uri, _ := lm["uri"].(string); uri != "" {
						m.Icons[idx].DocLinks = append(m.Icons[idx].DocLinks, uri)
					}
				}
			}

			// — Variant enumeration with rich root-frame extraction —
			children, _ := doc["children"].([]any)
			variantNodes := []map[string]any{}
			for _, ch := range children {
				cm, _ := ch.(map[string]any)
				if cm == nil {
					continue
				}
				if t, _ := cm["type"].(string); t == "COMPONENT" {
					variantNodes = append(variantNodes, cm)
				}
			}
			if len(variantNodes) == 1 {
				m.Icons[idx].SingleVariantSet = true
			}
			defaultVariantID := computeDefaultVariantID(variantNodes)

			for _, cm := range variantNodes {
				vid, _ := cm["id"].(string)
				vname, _ := cm["name"].(string)
				if vid == "" || vname == "" {
					continue
				}
				bbox, _ := cm["absoluteBoundingBox"].(map[string]any)
				w, h := dim(bbox)
				v := Variant{
					Name:       vname,
					Properties: parseProps(vname),
					AxisValues: parseAxisValues(vname),
					VariantID:  vid,
					Key:        keyByNodeID[vid],
					IsDefault:  vid == defaultVariantID,
					File: filepath.Join(
						"variants",
						fmt.Sprintf("%s__%s.svg", m.Icons[idx].Slug, slugifyVariant(vname)),
					),
					Width:  w,
					Height: h,
				}
				// Per-variant rich extraction — the variant COMPONENT node
				// itself IS the root frame (Figma collapses them); pull
				// layout, fills, strokes, effects, corner, opacity, bound
				// vars + child summary directly off it.
				v.Layout = extractLayout(cm)
				v.Fills = extractPaints(cm["fills"])
				v.Strokes = extractPaints(cm["strokes"])
				if sw, ok := cm["strokeWeight"].(float64); ok {
					v.StrokeWeight = sw
				}
				v.Effects = extractEffects(cm["effects"])
				v.Corner = extractCorner(cm)
				if op, ok := cm["opacity"].(float64); ok && op < 1 {
					v.Opacity = op
				}
				v.BoundVariables = extractBoundVarIDs(cm["boundVariables"])
				v.Children = extractChildren(cm["children"])
				// Composition graph — only walked for tier=parent. Atoms
				// have no INSTANCEs and molecules aren't surfaced yet.
				// The walk is shallow (depth=3 fetch caps it anyway) and
				// stops at INSTANCE boundaries so we don't double-count
				// nested compositions.
				if m.Icons[idx].Tier == "parent" {
					v.Composes = walkVariantInstances(cm)
				}
				pending = append(pending, variantKey{idx, v})
			}

			// Build the matrix view from PropDefs + observed values
			m.Icons[idx].VariantAxes = buildVariantAxes(m.Icons[idx].PropDefs, variantNodes, defaultVariantID)
		}
		log.Info("variants enumerated", "progress", fmt.Sprintf("%d/%d sets", end, len(targets)), "total_variants", len(pending))
	}

	if len(pending) == 0 {
		log.Info("no variants found")
		return
	}

	// 2. Resolve SVG URLs in batches
	urlByID := map[string]string{}
	const urlBatch = 80
	for start := 0; start < len(pending); start += urlBatch {
		end := start + urlBatch
		if end > len(pending) {
			end = len(pending)
		}
		ids := make([]string, 0, end-start)
		for _, p := range pending[start:end] {
			ids = append(ids, p.variant.VariantID)
		}
		urls, err := imagesURLs(ctx, c, fileKey, ids)
		if err != nil {
			log.Warn("images batch failed", "err", err)
			continue
		}
		for k, v := range urls {
			urlByID[k] = v
		}
		log.Info("svg urls resolved", "progress", fmt.Sprintf("%d/%d", end, len(pending)))
	}

	// 3. Download SVGs concurrently
	manifestDir := filepath.Dir(*manifest)
	type result struct {
		i   int
		err error
	}
	resCh := make(chan result, len(pending))
	sema := make(chan struct{}, 10)
	var wg sync.WaitGroup
	for i, p := range pending {
		url := urlByID[p.variant.VariantID]
		if url == "" {
			continue
		}
		wg.Add(1)
		sema <- struct{}{}
		go func(i int, p variantKey, url string) {
			defer wg.Done()
			defer func() { <-sema }()
			dst := filepath.Join(manifestDir, p.variant.File)
			err := downloadSVG(ctx, url, dst)
			resCh <- result{i, err}
		}(i, p, url)
	}
	wg.Wait()
	close(resCh)
	ok, fail := 0, 0
	for r := range resCh {
		if r.err != nil {
			fail++
		} else {
			ok++
		}
	}
	log.Info("download summary", "ok", ok, "failed", fail)

	// 4. Stitch variants back into the manifest
	byEntry := map[int][]Variant{}
	for _, p := range pending {
		byEntry[p.entryIdx] = append(byEntry[p.entryIdx], p.variant)
	}
	for idx, vs := range byEntry {
		m.Icons[idx].Variants = vs
	}

	// 5. Resolve compositions — every parent CompositionRef gets its
	// AtomSlug filled by cross-referencing against the full manifest.
	// Done after stitching so byVariantID is populated for every entry.
	resolveCompositions(&m)

	out, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(*manifest, append(out, '\n'), 0o644); err != nil {
		log.Error("write manifest", "err", err)
		os.Exit(2)
	}
	log.Info("DONE", "manifest", *manifest, "variants", len(pending), "sets_with_variants", len(byEntry))
}

func entryMatchesKind(e IconEntry, kind string) bool {
	if e.Kind != "" {
		return e.Kind == kind
	}
	// Legacy manifest without a kind field — fall back to category.
	switch kind {
	case "component":
		return legacyComponentCategories[e.Category]
	}
	return false
}

func dim(bbox map[string]any) (int, int) {
	w, h := 0, 0
	if bbox != nil {
		if v, ok := bbox["width"].(float64); ok {
			w = int(v + 0.5)
		}
		if v, ok := bbox["height"].(float64); ok {
			h = int(v + 0.5)
		}
	}
	return w, h
}

// parseProps splits "State=Default, Size=Medium" → [{name:"State",value:"Default"},…]
func parseProps(name string) []map[string]string {
	out := []map[string]string{}
	for _, part := range strings.Split(name, ",") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			continue
		}
		out = append(out, map[string]string{
			"name":  strings.TrimSpace(kv[0]),
			"value": strings.TrimSpace(kv[1]),
		})
	}
	return out
}

func slugifyVariant(name string) string {
	s := strings.ToLower(name)
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, ",", "_")
	s = strings.ReplaceAll(s, "/", "-")
	s = strings.ReplaceAll(s, "=", "-")
	return s
}

func getNodesWithRetry(ctx context.Context, c *client.Client, fileKey string, ids []string, log *slog.Logger) (map[string]any, error) {
	// depth=3 captures: SET → variant COMPONENT → root frame → first-level
	// children. This is the sweet spot for the rich-extractor: deep enough
	// to see what makes a variant look the way it does (autolayout +
	// children + bound vars), shallow enough that fetch time stays under
	// a few minutes for ~89 sets.
	for attempt := 1; attempt <= 4; attempt++ {
		resp, err := c.GetFileNodes(ctx, fileKey, ids, 3)
		if err == nil {
			return resp, nil
		}
		var ae *client.APIError
		if asErr(err, &ae) && ae.IsRateLimit() {
			wait := ae.RetryAfter
			if wait == 0 {
				wait = time.Duration(5*attempt) * time.Second
			}
			log.Info("rate limited, backing off", "attempt", attempt, "wait", wait)
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			continue
		}
		return nil, err
	}
	return nil, fmt.Errorf("get nodes: 4 attempts exhausted")
}

func asErr(err error, target **client.APIError) bool {
	if e, ok := err.(*client.APIError); ok {
		*target = e
		return true
	}
	return false
}

func imagesURLs(ctx context.Context, c *client.Client, fileKey string, ids []string) (map[string]string, error) {
	idsCSV := strings.Join(ids, ",")
	url := fmt.Sprintf("https://api.figma.com/v1/images/%s?ids=%s&format=svg&svg_simplify_stroke=true&svg_outline_text=false", fileKey, idsCSV)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Figma-Token", c.Token())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("images %d: %s", resp.StatusCode, string(body))
	}
	var parsed struct {
		Err    any               `json:"err"`
		Images map[string]string `json:"images"`
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 5<<20))
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	if parsed.Err != nil {
		return nil, fmt.Errorf("images err: %v", parsed.Err)
	}
	return parsed.Images, nil
}

func downloadSVG(ctx context.Context, url, dst string) error {
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
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 5<<20))
	if err != nil {
		return err
	}
	return os.WriteFile(dst, body, 0o644)
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
		_ = log
		return
	}
}
