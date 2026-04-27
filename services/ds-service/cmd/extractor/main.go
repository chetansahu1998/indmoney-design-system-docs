// Command extractor runs the Figma → W3C-DTCG token pipeline locally.
//
// Usage:
//
//	go run ./services/ds-service/cmd/extractor \
//	    --brand indmoney \
//	    --out lib/tokens/indmoney
//
// Reads FIGMA_PAT and FIGMA_FILE_KEY_<BRAND> from env (.env.local at repo root).
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
	"github.com/indmoney/design-system-docs/services/ds-service/internal/figma/dtcg"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/figma/extractor"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/figma/repo"
)

// stringSlice is a flag.Value that collects repeated --source flags into a list.
type stringSlice []string

func (s *stringSlice) String() string     { return strings.Join(*s, ",") }
func (s *stringSlice) Set(v string) error { *s = append(*s, v); return nil }

func main() {
	var (
		brand   = flag.String("brand", "indmoney", "Brand slug (indmoney|tickertape)")
		outDir  = flag.String("out", "", "Output directory (default: lib/tokens/<brand>)")
		verbose = flag.Bool("v", false, "Verbose logging")
		sources stringSlice
	)
	flag.Var(&sources, "source", "Repeatable source spec 'kind:file_key[:node_id]' where kind ∈ {design-system, product}. If absent, sources are read from FIGMA_SOURCES env (comma-separated) OR a default mix derived from FIGMA_FILE_KEY_<BRAND>* env vars.")
	flag.Parse()

	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	loadDotEnv(log)

	pat := os.Getenv("FIGMA_PAT")
	if pat == "" {
		fatalf(log, "FIGMA_PAT not set. Copy .env.example to .env.local and fill in.")
	}

	if *outDir == "" {
		*outDir = filepath.Join(repo.Root(), "lib/tokens", *brand)
	}
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		fatalf(log, "mkdir %s: %v", *outDir, err)
	}

	srcs, err := resolveSources(sources, *brand)
	if err != nil {
		fatalf(log, "%v", err)
	}
	if len(srcs) == 0 {
		fatalf(log, "no sources configured. Pass --source flags, set FIGMA_SOURCES env, or set FIGMA_FILE_KEY_<BRAND>* envs.")
	}
	log.Info("sources resolved", "count", len(srcs))
	for _, s := range srcs {
		log.Info("  source", "kind", s.Kind, "file_key", s.FileKey, "node_id", s.NodeID)
	}

	ctx := context.Background()
	c := client.New(pat)

	// Sanity check identity
	me, err := c.Identity(ctx)
	if err != nil {
		fatalf(log, "/v1/me failed: %v", err)
	}
	log.Info("authenticated", "email", me["email"], "handle", me["handle"])

	// Run extraction
	result, err := extractor.Run(ctx, c, *brand, srcs, log)
	if err != nil {
		fatalf(log, "extraction failed: %v", err)
	}

	// Convert to DTCG
	files, err := dtcg.Adapt(result)
	if err != nil {
		fatalf(log, "DTCG adapt failed: %v", err)
	}

	// Write outputs
	must(log, writeFile(filepath.Join(*outDir, "base.tokens.json"), files.Base))
	must(log, writeFile(filepath.Join(*outDir, "semantic.tokens.json"), files.Semantic))
	must(log, writeFile(filepath.Join(*outDir, "semantic-dark.tokens.json"), files.SemanticDark))
	// Only overwrite text-styles if the extractor actually produced any (>2 bytes = more than "{}")
	// Otherwise preserve whatever's checked in (may be a previous extraction or hand-curated).
	if len(files.TextStyles) > 4 {
		must(log, writeFile(filepath.Join(*outDir, "text-styles.tokens.json"), files.TextStyles))
	} else {
		log.Info("text-styles preserved (extractor produced none)", "path", filepath.Join(*outDir, "text-styles.tokens.json"))
	}
	must(log, writeFile(filepath.Join(*outDir, "_extraction-meta.json"), files.ContractMeta))

	log.Info("DONE",
		"out", *outDir,
		"sources", len(result.Sources),
		"frames", result.CandidateCount(),
		"pairs", result.PairCount(),
		"obs", len(result.Observations),
		"roles", len(result.Roles),
		"base_colors", len(result.BasePalette),
		"text_styles", len(result.TextStyles),
	)
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Top 10 roles by usage:")
	for i, r := range result.Roles {
		if i >= 10 {
			break
		}
		fmt.Fprintf(os.Stderr, "  %2d×%-3d  %s   names=[%s]\n",
			r.InstanceCount, len(r.Names), r.Key, joinShortN(r.Names, 4))
	}
}

func writeFile(path string, content []byte) error {
	if len(content) == 0 {
		return nil
	}
	return os.WriteFile(path, content, 0o644)
}

func must(log *slog.Logger, err error) {
	if err != nil {
		log.Error("write failed", "err", err)
		os.Exit(2)
	}
}

func fatalf(log *slog.Logger, format string, args ...any) {
	log.Error(fmt.Sprintf(format, args...))
	os.Exit(1)
}

// resolveSources figures out the extraction sources from (in priority order):
//   1. --source CLI flags (repeatable)
//   2. FIGMA_SOURCES env (comma-separated source specs)
//   3. Auto-derived from FIGMA_FILE_KEY_<BRAND>* env vars:
//        FIGMA_FILE_KEY_<BRAND>_GLYPH       → design-system source
//        FIGMA_FILE_KEY_<BRAND>             → product source
//        FIGMA_NODE_ID_<BRAND>              → optional node_id for product
func resolveSources(cliSources stringSlice, brand string) ([]extractor.Source, error) {
	parse := func(specs []string) ([]extractor.Source, error) {
		out := make([]extractor.Source, 0, len(specs))
		for _, raw := range specs {
			raw = strings.TrimSpace(raw)
			if raw == "" {
				continue
			}
			s, err := extractor.ParseSource(raw)
			if err != nil {
				return nil, err
			}
			out = append(out, s)
		}
		return out, nil
	}

	if len(cliSources) > 0 {
		return parse(cliSources)
	}
	if env := os.Getenv("FIGMA_SOURCES"); env != "" {
		return parse(strings.Split(env, ","))
	}

	// Auto-derive
	bUp := strings.ToUpper(brand)
	var auto []extractor.Source
	if v := os.Getenv("FIGMA_FILE_KEY_" + bUp + "_GLYPH"); v != "" {
		// Design-system file — fetch styles + optionally a token-display node
		spec := string(extractor.SourceDesignSystem) + ":" + v
		if nid := os.Getenv("FIGMA_NODE_ID_" + bUp + "_GLYPH"); nid != "" {
			spec += ":" + nid
		}
		s, err := extractor.ParseSource(spec)
		if err != nil {
			return nil, err
		}
		auto = append(auto, s)
	}
	if v := os.Getenv("FIGMA_FILE_KEY_" + bUp); v != "" {
		spec := string(extractor.SourceProduct) + ":" + v
		if nid := os.Getenv("FIGMA_NODE_ID_" + bUp); nid != "" {
			spec += ":" + nid
		}
		s, err := extractor.ParseSource(spec)
		if err != nil {
			return nil, err
		}
		auto = append(auto, s)
	}
	return auto, nil
}

// loadDotEnv reads .env.local from cwd (or any ancestor) and applies KEY=VALUE
// pairs into os.Environ unless already set. Tiny implementation; no quoting.
func loadDotEnv(log *slog.Logger) {
	candidates := []string{".env.local", "../.env.local", "../../.env.local", "../../../.env.local"}
	for _, c := range candidates {
		f, err := os.Open(c)
		if err != nil {
			continue
		}
		defer f.Close()
		applyDotEnv(f, log, c)
		return
	}
}

func applyDotEnv(f *os.File, log *slog.Logger, path string) {
	buf := make([]byte, 1<<20)
	n, _ := f.Read(buf)
	loaded := 0
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
			loaded++
		}
	}
	log.Debug("dotenv loaded", "path", path, "vars", loaded)
}

func joinShortN(ss []string, n int) string {
	if len(ss) <= n {
		return strings.Join(ss, ", ")
	}
	return strings.Join(ss[:n], ", ") + fmt.Sprintf(" +%d", len(ss)-n)
}
