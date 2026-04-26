// Command icons extracts SVG icons from Glyph's Icons Fresh page.
//
//	go run ./cmd/icons --file-key <key> --page <node_id> --out <dir>
//
// Defaults read from .env.local: FIGMA_PAT, FIGMA_FILE_KEY_INDMONEY_GLYPH,
// FIGMA_NODE_ID_INDMONEY_GLYPH_ICONS. Output: <repo>/public/icons/glyph/.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/figma/client"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/figma/icons"
)

func main() {
	var (
		fileKey = flag.String("file-key", "", "Figma file key (default: $FIGMA_FILE_KEY_INDMONEY_GLYPH)")
		pageID  = flag.String("page", "", "Icons page node id (default: $FIGMA_NODE_ID_INDMONEY_GLYPH_ICONS)")
		outDir  = flag.String("out", "", "Output dir (default: <repo>/public/icons/glyph/)")
	)
	flag.Parse()

	loadDotEnv()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	pat := os.Getenv("FIGMA_PAT")
	if pat == "" {
		fmt.Fprintln(os.Stderr, "FIGMA_PAT not set")
		os.Exit(1)
	}
	if *fileKey == "" {
		*fileKey = os.Getenv("FIGMA_FILE_KEY_INDMONEY_GLYPH")
	}
	if *pageID == "" {
		*pageID = os.Getenv("FIGMA_NODE_ID_INDMONEY_GLYPH_ICONS")
	}
	if *fileKey == "" || *pageID == "" {
		fmt.Fprintln(os.Stderr, "file-key and page required")
		os.Exit(1)
	}
	if *outDir == "" {
		repoDir, _ := filepath.Abs("../..")
		*outDir = filepath.Join(repoDir, "public/icons/glyph")
	}

	c := client.New(pat)
	ctx := context.Background()

	manifest, err := icons.Extract(ctx, c, *fileKey, *pageID, *outDir, log)
	if err != nil {
		log.Error("extract failed", "err", err)
		os.Exit(1)
	}
	log.Info("DONE", "icons", len(manifest.Icons), "out", *outDir)
}

func loadDotEnv() {
	for _, path := range []string{".env.local", "../.env.local", "../../.env.local", "../../../.env.local"} {
		f, err := os.Open(path)
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
			if eq := strings.Index(line, "="); eq > 0 {
				k := strings.TrimSpace(line[:eq])
				v := strings.Trim(strings.TrimSpace(line[eq+1:]), "\"'")
				if os.Getenv(k) == "" {
					os.Setenv(k, v)
				}
			}
		}
		return
	}
}
