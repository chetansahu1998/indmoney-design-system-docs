// Command effects extracts Figma EFFECT styles for a brand and writes them
// as W3C-DTCG shadow tokens to lib/tokens/<brand>/effects.tokens.json.
//
// Usage:
//
//	go run ./services/ds-service/cmd/effects --brand indmoney
//
// Reads FIGMA_PAT and FIGMA_FILE_KEY_<BRAND>_GLYPH from .env.local.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
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
		log.Error("FIGMA_PAT not set")
		os.Exit(1)
	}

	bUp := strings.ToUpper(*brand)
	fileKey := firstEnv("FIGMA_FILE_KEY_"+bUp+"_GLYPH", "FIGMA_FILE_KEY_"+bUp)
	if fileKey == "" {
		log.Error("no Figma file key — set FIGMA_FILE_KEY_" + bUp + "_GLYPH or FIGMA_FILE_KEY_" + bUp)
		os.Exit(1)
	}

	if *outDir == "" {
		*outDir = filepath.Join(repo.Root(), "lib/tokens", *brand)
	}
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		log.Error("mkdir failed", "err", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	c := client.New(pat)

	res, err := extractor.RunEffects(ctx, c, fileKey, log)
	if err != nil {
		log.Error("effects extraction failed", "err", err)
		os.Exit(2)
	}

	// Fallback: if no published EFFECT styles, scan the design-system page tree
	// for inline shadows on FRAME/COMPONENT nodes (Glyph's actual pattern).
	if len(res.Styles) == 0 {
		pageNode := firstEnv("FIGMA_NODE_ID_"+bUp+"_GLYPH", "FIGMA_NODE_ID_"+bUp)
		if pageNode != "" {
			log.Info("no EFFECT styles published — scanning page tree for inline shadows", "page", pageNode)
			scan, err := extractor.ScanEffects(ctx, c, fileKey, pageNode, log)
			if err != nil {
				log.Warn("scan fallback failed", "err", err)
			} else {
				res.Styles = scan.Styles
			}
		} else {
			log.Info("no FIGMA_NODE_ID_" + bUp + "_GLYPH set — skipping inline-shadow scan")
		}
	}

	dtcg := buildDTCG(*brand, res)
	out := filepath.Join(*outDir, "effects.tokens.json")
	bytes, _ := json.MarshalIndent(dtcg, "", "  ")
	if err := os.WriteFile(out, append(bytes, '\n'), 0o644); err != nil {
		log.Error("write failed", "err", err)
		os.Exit(2)
	}

	log.Info("DONE",
		"out", out,
		"styles", len(res.Styles),
	)
}

// buildDTCG converts an EffectsResult into a W3C-DTCG shadow tree:
//
//	{
//	  "shadow": {
//	    "card.elevation-1": {
//	      "$type": "shadow",
//	      "$value": [{ "color": "#…", "offsetX": "0px", "offsetY": "2px", "blur": "8px", "spread": "0px", "inset": false }]
//	    }
//	  }
//	}
func buildDTCG(brand string, res *extractor.EffectsResult) map[string]any {
	root := map[string]any{
		"$description": "Figma EFFECT styles dereferenced to W3C-DTCG shadow tokens.",
		"$extensions": map[string]any{
			"com.indmoney.provenance":  "figma-effect-styles",
			"com.indmoney.brand":       brand,
			"com.indmoney.extractedAt": time.Now().UTC().Format(time.RFC3339),
		},
	}
	bucket := map[string]any{}
	for _, s := range res.Styles {
		shadows := []map[string]any{}
		for _, e := range s.Effects {
			if e.Type != "DROP_SHADOW" && e.Type != "INNER_SHADOW" {
				continue // skip blur effects — DTCG shadow type only covers shadows
			}
			shadows = append(shadows, map[string]any{
				"color":   e.Color,
				"offsetX": fmt.Sprintf("%gpx", e.OffsetX),
				"offsetY": fmt.Sprintf("%gpx", e.OffsetY),
				"blur":    fmt.Sprintf("%gpx", e.Radius),
				"spread":  fmt.Sprintf("%gpx", e.Spread),
				"inset":   e.Inset,
			})
		}
		if len(shadows) == 0 {
			continue
		}
		key := slugify(s.Name)
		entry := map[string]any{
			"$type": "shadow",
		}
		if len(shadows) == 1 {
			entry["$value"] = shadows[0]
		} else {
			entry["$value"] = shadows
		}
		if s.Description != "" {
			entry["$description"] = s.Description
		}
		bucket[key] = entry
	}
	root["shadow"] = bucket
	return root
}

func slugify(name string) string {
	s := strings.ToLower(name)
	s = strings.ReplaceAll(s, " ", "-")
	s = strings.ReplaceAll(s, "/", "-")
	s = strings.ReplaceAll(s, "_", "-")
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

// loadDotEnv reads .env.local from cwd or any ancestor and applies KEY=VALUE.
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
