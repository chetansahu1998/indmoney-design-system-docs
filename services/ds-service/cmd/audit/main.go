// Command audit walks the curated file manifest, runs the audit core
// against each Figma file, and writes per-file + index JSON into lib/audit/.
//
// Usage:
//
//	go run ./services/ds-service/cmd/audit
//	go run ./services/ds-service/cmd/audit --files lib/audit-files.json --out lib/audit
//	go run ./services/ds-service/cmd/audit --dry-run
//
// Reads FIGMA_PAT from .env.local. Tokens come from
// lib/tokens/<brand>/{semantic,base}.tokens.json — the same files Foundations renders.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/audit"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/figma/client"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/figma/repo"
)

type ManifestEntry struct {
	FileKey    string   `json:"file_key"`
	Name       string   `json:"name"`
	Brand      string   `json:"brand"`
	Owner      string   `json:"owner,omitempty"`
	FinalPages []string `json:"final_pages,omitempty"`
}

type Manifest struct {
	Files []ManifestEntry `json:"files"`
}

func main() {
	var (
		manifestPath = flag.String("files", "", "Path to lib/audit-files.json (default: <repo>/lib/audit-files.json)")
		outDir       = flag.String("out", "", "Output dir (default: <repo>/lib/audit)")
		dryRun       = flag.Bool("dry-run", false, "Print summary; don't write JSON")
		verbose      = flag.Bool("v", false, "Verbose logs")
	)
	flag.Parse()

	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	loadDotEnv()

	root := repo.Root()
	if *manifestPath == "" {
		*manifestPath = filepath.Join(root, "lib/audit-files.json")
	}
	if *outDir == "" {
		*outDir = filepath.Join(root, "lib/audit")
	}

	manifest, err := readManifest(*manifestPath)
	if err != nil {
		log.Error("read manifest", "err", err)
		os.Exit(1)
	}
	if len(manifest.Files) == 0 {
		log.Info("no files in manifest — nothing to audit", "path", *manifestPath)
		return
	}

	pat := os.Getenv("FIGMA_PAT")
	if pat == "" {
		log.Error("FIGMA_PAT not set in env or .env.local")
		os.Exit(1)
	}
	c := client.New(pat)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	dsRev, err := designSystemRev(root)
	if err != nil {
		log.Warn("could not compute DS rev — continuing without it", "err", err)
	}

	sweepRun := time.Now().UTC().Format("20060102T150405Z")
	results := make([]audit.AuditResult, 0, len(manifest.Files))
	hashesByFile := map[string][]audit.HashedNode{}
	failures := 0

	for _, entry := range manifest.Files {
		if entry.FileKey == "" || strings.HasPrefix(entry.FileKey, "REPLACE_WITH") {
			log.Warn("skipping placeholder entry", "name", entry.Name)
			continue
		}
		log.Info("auditing file", "name", entry.Name, "file_key", entry.FileKey)

		raw, err := c.GetFile(ctx, entry.FileKey, 0)
		if err != nil {
			log.Error("get file failed", "name", entry.Name, "err", err)
			failures++
			continue
		}
		doc, _ := raw["document"].(map[string]any)
		fileRev, _ := raw["version"].(string)

		opts := audit.Options{
			FileKey:             entry.FileKey,
			FileName:            entry.Name,
			FileSlug:            slugifyFileName(entry.Name),
			Brand:               entry.Brand,
			Owner:               entry.Owner,
			FileRev:             fileRev,
			DesignSystemRev:     dsRev,
			SweepRun:            sweepRun,
			AllowedFinalPageIDs: entry.FinalPages,
		}

		tokens, err := loadDSTokens(root, entry.Brand)
		if err != nil {
			log.Warn("could not load DS tokens for brand — proceeding with empty token set", "brand", entry.Brand, "err", err)
		}
		// Component candidates v1: derive from the existing icons manifest
		// (kind=component entries) so cmd/audit doesn't re-enumerate.
		candidates, _ := loadDSComponents(root)

		result := audit.Audit(doc, tokens, candidates, opts)
		results = append(results, result)
		hashesByFile[opts.FileSlug] = audit.CollectHashes(doc)

		log.Info("file audited",
			"name", entry.Name,
			"screens", len(result.Screens),
			"coverage", fmt.Sprintf("%.1f%%", result.OverallCoverage*100),
			"from_ds", fmt.Sprintf("%.1f%%", result.OverallFromDS*100),
		)
	}

	if *dryRun {
		log.Info("dry-run — skipping disk writes")
		return
	}

	for _, r := range results {
		path, err := audit.WritePerFile(*outDir, r)
		if err != nil {
			log.Error("write per-file", "file", r.FileSlug, "err", err)
			failures++
			continue
		}
		log.Info("wrote per-file audit", "path", path)
	}

	idx := audit.BuildIndex(results, hashesByFile, dsRev)
	idxPath, err := audit.WriteIndex(*outDir, idx)
	if err != nil {
		log.Error("write index", "err", err)
		os.Exit(2)
	}
	log.Info("DONE",
		"index", idxPath,
		"files", len(results),
		"failures", failures,
		"cross_file_patterns", len(idx.CrossFilePatterns),
	)
}

func readManifest(path string) (*Manifest, error) {
	bs, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(bs, &m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	return &m, nil
}

// designSystemRev returns sha256(8) of the published manifest bytes — the
// same identifier the plugin uses to detect staleness.
func designSystemRev(root string) (string, error) {
	manifest := filepath.Join(root, "public/icons/glyph/manifest.json")
	bs, err := os.ReadFile(manifest)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(bs)
	return hex.EncodeToString(sum[:8]), nil
}

// loadDSTokens flattens semantic + base color tokens for the brand into the
// DSToken slice the audit core compares against.
func loadDSTokens(root, brand string) ([]audit.DSToken, error) {
	if brand == "" {
		brand = "indmoney"
	}
	tokens := []audit.DSToken{}

	semantic := filepath.Join(root, "lib/tokens", brand, "semantic.tokens.json")
	if bs, err := os.ReadFile(semantic); err == nil {
		tokens = append(tokens, flattenColorTokens(bs)...)
	}
	base := filepath.Join(root, "lib/tokens", brand, "base.tokens.json")
	if bs, err := os.ReadFile(base); err == nil {
		tokens = append(tokens, flattenColorTokens(bs)...)
	}
	spacing := filepath.Join(root, "lib/tokens", brand, "spacing.tokens.json")
	if bs, err := os.ReadFile(spacing); err == nil {
		tokens = append(tokens, flattenDimensionTokens(bs)...)
	}

	if len(tokens) == 0 {
		return nil, fmt.Errorf("no tokens loaded from lib/tokens/%s", brand)
	}
	return tokens, nil
}

// flattenColorTokens walks a DTCG JSON tree and emits one DSToken per leaf
// $value with $type=color. We include both deprecated metadata and replacedBy.
func flattenColorTokens(raw []byte) []audit.DSToken {
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil
	}
	out := []audit.DSToken{}
	walkDTCG(doc, "", func(path string, leaf map[string]any) {
		t, _ := leaf["$type"].(string)
		if t != "color" && t != "" {
			return
		}
		val := leaf["$value"]
		hexStr := dtcgColorToHex(val)
		if hexStr == "" {
			return
		}
		ext, _ := leaf["$extensions"].(map[string]any)
		ind, _ := ext["com.indmoney"].(map[string]any)
		dep, _ := ind["deprecated"].(bool)
		repl, _ := ind["replacedBy"].(string)
		out = append(out, audit.DSToken{
			Path:       path,
			Hex:        hexStr,
			Kind:       "color",
			Deprecated: dep,
			ReplacedBy: repl,
		})
	})
	return out
}

func flattenDimensionTokens(raw []byte) []audit.DSToken {
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil
	}
	out := []audit.DSToken{}
	walkDTCG(doc, "", func(path string, leaf map[string]any) {
		if t, _ := leaf["$type"].(string); t != "dimension" {
			return
		}
		val, _ := leaf["$value"].(map[string]any)
		v, _ := val["value"].(float64)
		kind := "spacing"
		if strings.HasPrefix(path, "radius.") {
			kind = "radius"
		} else if strings.HasPrefix(path, "padding.") {
			kind = "padding"
		}
		out = append(out, audit.DSToken{
			Path: path,
			Px:   v,
			Kind: kind,
		})
	})
	return out
}

func walkDTCG(node any, prefix string, fn func(path string, leaf map[string]any)) {
	m, ok := node.(map[string]any)
	if !ok {
		return
	}
	if _, hasValue := m["$value"]; hasValue {
		fn(prefix, m)
		return
	}
	for k, v := range m {
		if strings.HasPrefix(k, "$") {
			continue
		}
		next := k
		if prefix != "" {
			next = prefix + "." + k
		}
		walkDTCG(v, next, fn)
	}
}

// dtcgColorToHex handles both string-form ("#RRGGBB") and object-form
// ({"colorSpace":"srgb","components":[r,g,b]}) values.
func dtcgColorToHex(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	m, ok := v.(map[string]any)
	if !ok {
		return ""
	}
	comps, _ := m["components"].([]any)
	if len(comps) < 3 {
		return ""
	}
	r, _ := comps[0].(float64)
	g, _ := comps[1].(float64)
	b, _ := comps[2].(float64)
	return audit.RGBToHex(r, g, b)
}

// loadDSComponents reads the published icons manifest and returns kind=component
// entries as DSCandidates. v1 placeholder; richer DS-component metadata comes
// later when the cmd/extractor emits it explicitly.
func loadDSComponents(root string) ([]audit.DSCandidate, error) {
	bs, err := os.ReadFile(filepath.Join(root, "public/icons/glyph/manifest.json"))
	if err != nil {
		return nil, err
	}
	var raw struct {
		Icons []struct {
			Slug         string `json:"slug"`
			Name         string `json:"name"`
			Kind         string `json:"kind"`
			SetID        string `json:"set_id"`
			VariantID    string `json:"variant_id"`
		} `json:"icons"`
	}
	if err := json.Unmarshal(bs, &raw); err != nil {
		return nil, err
	}
	out := []audit.DSCandidate{}
	for _, i := range raw.Icons {
		if i.Kind != "component" {
			continue
		}
		out = append(out, audit.DSCandidate{
			Slug:         i.Slug,
			Name:         i.Name,
			ComponentKey: i.SetID, // Match against the COMPONENT_SET id
		})
	}
	return out, nil
}

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

func loadDotEnv() {
	candidates := []string{".env.local", "../.env.local", "../../.env.local", "../../../.env.local"}
	for _, c := range candidates {
		f, err := os.Open(c)
		if err != nil {
			continue
		}
		buf := make([]byte, 1<<20)
		n, _ := f.Read(buf)
		f.Close()
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
		return
	}
}
