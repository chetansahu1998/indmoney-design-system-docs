// Command sheets-sync — Google Sheet → ds-service projects/flows pipeline.
//
// Plan: docs/plans/2026-05-05-001-feat-google-sheet-sync-pipeline-plan.md
//
// Runs as a separate Fly app (indmoney-sheets-sync) on a 5-minute machine
// schedule. Each cycle:
//
//   1. Probe Drive API modifiedTime — short-circuit when unchanged
//   2. Read every visible sheet tab via Sheets API v4
//   3. Per-row normalize (parse Figma URL, resolve sub-sheet → product)
//   4. Cross-tab dedup on (file_id, node_id)
//   5. Diff against sheet_sync_state (new / changed / unchanged / gone)
//   6. For new+changed: Figma REST resolve → POST /v1/projects/export
//   7. Persist state + emit telemetry summary
//
// Usage:
//
//	sheets-sync [flags]
//
//	--once          Run a single cycle and exit (default; ideal for cron)
//	--loop          Loop forever with 5-minute sleeps (local dev / fallback)
//	--dry-run       Print planned imports without POSTing or writing state
//	--tab=<name>    Only sync this tab (debug)
//	--limit=<n>     Cap rows per tab to <n> (debug)
//	--db=<path>     SQLite path; defaults to env DS_DB_PATH
//	--verbose       Verbose logging
//
// Required env:
//
//	GOOGLE_APPLICATION_CREDENTIALS   path to SA JSON
//	SHEETS_SPREADSHEET_ID            sheet to sync
//	DS_SERVICE_URL                   target (e.g. https://indmoney-ds-service.fly.dev)
//	DS_SERVICE_BEARER                super-admin JWT for the export call
//	FIGMA_PAT                        Figma personal access token
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

const cycleInterval = 5 * time.Minute

func main() {
	var (
		runOnce  = flag.Bool("once", false, "Run a single cycle and exit (default if --loop not set)")
		runLoop  = flag.Bool("loop", false, "Loop forever with 5-minute sleeps")
		dryRun   = flag.Bool("dry-run", false, "Print planned imports without POSTing or writing state")
		tabOnly  = flag.String("tab", "", "Only sync this tab (debug)")
		limit    = flag.Int("limit", 0, "Cap rows per tab to N (debug; 0 = no cap)")
		dbPath   = flag.String("db", os.Getenv("DS_DB_PATH"), "Path to ds.db (env DS_DB_PATH)")
		verbose  = flag.Bool("verbose", false, "Verbose logging")
	)
	flag.Parse()

	loadDotEnv()

	logLevel := slog.LevelInfo
	if *verbose {
		logLevel = slog.LevelDebug
	}
	log := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel}))

	cfg, err := loadConfig()
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(2)
	}
	if *dbPath == "" {
		*dbPath = cfg.defaultDBPath()
	}
	cfg.DryRun = *dryRun
	cfg.TabOnly = *tabOnly
	cfg.RowLimit = *limit
	cfg.Logger = log

	log.Info("sheets-sync starting",
		"once", *runOnce, "loop", *runLoop, "dry_run", *dryRun,
		"db", *dbPath, "spreadsheet", cfg.SpreadsheetID,
		"ds_url", cfg.DSServiceURL,
	)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// One-shot path: --once flag, OR neither --once nor --loop given (cron friendly).
	if *runOnce || !*runLoop {
		if err := runCycle(ctx, cfg, *dbPath); err != nil {
			log.Error("cycle", "err", err)
			os.Exit(1)
		}
		return
	}

	// Loop mode — keep running until SIGINT/SIGTERM.
	for {
		if err := runCycle(ctx, cfg, *dbPath); err != nil {
			log.Error("cycle", "err", err)
		}
		select {
		case <-ctx.Done():
			log.Info("sheets-sync exiting")
			return
		case <-time.After(cycleInterval):
		}
	}
}

// runCycle is the top-level orchestration. Delegates to runCycleImpl in
// orchestrate.go (split for readability and unit testing).
func runCycle(ctx context.Context, cfg *config, dbPath string) error {
	return runCycleImpl(ctx, cfg, dbPath)
}

// config carries the resolved env + flag inputs into every step.
type config struct {
	SACredsPath    string
	SpreadsheetID  string
	DSServiceURL   string
	DSServiceJWT   string
	FigmaPAT       string

	DryRun   bool
	TabOnly  string
	RowLimit int
	Logger   *slog.Logger
}

func loadConfig() (*config, error) {
	c := &config{
		SACredsPath:   os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"),
		SpreadsheetID: os.Getenv("SHEETS_SPREADSHEET_ID"),
		DSServiceURL:  os.Getenv("DS_SERVICE_URL"),
		DSServiceJWT:  os.Getenv("DS_SERVICE_BEARER"),
		FigmaPAT:      os.Getenv("FIGMA_PAT"),
	}
	if c.SpreadsheetID == "" {
		return nil, fmt.Errorf("SHEETS_SPREADSHEET_ID env required")
	}
	if c.SACredsPath == "" {
		return nil, fmt.Errorf("GOOGLE_APPLICATION_CREDENTIALS env required (path to SA JSON)")
	}
	if c.DSServiceURL == "" {
		c.DSServiceURL = "https://indmoney-ds-service.fly.dev"
	}
	if c.DSServiceJWT == "" {
		return nil, fmt.Errorf("DS_SERVICE_BEARER env required (super-admin JWT)")
	}
	if c.FigmaPAT == "" {
		return nil, fmt.Errorf("FIGMA_PAT env required")
	}
	return c, nil
}

func (c *config) defaultDBPath() string {
	// Mirror cmd/digest's default: cwd-relative ds.db.
	if wd, err := os.Getwd(); err == nil {
		candidate := filepath.Join(wd, "services", "ds-service", "data", "ds.db")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return "services/ds-service/data/ds.db"
}

// loadDotEnv reads .env.local from cwd or any ancestor — matches cmd/server.
// We load BEFORE flag.Parse() resolves env defaults so the binary works
// the same in `go run ./cmd/sheets-sync` (cwd repo root) as in production
// (Fly machine where env is injected by `fly secrets`).
func loadDotEnv() {
	dir, err := os.Getwd()
	if err != nil {
		return
	}
	for {
		path := filepath.Join(dir, ".env.local")
		if data, err := os.ReadFile(path); err == nil {
			parseEnvFile(string(data))
			return
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return
		}
		dir = parent
	}
}

func parseEnvFile(s string) {
	for _, line := range splitLines(s) {
		line = trimSpace(line)
		if line == "" || line[0] == '#' {
			continue
		}
		eq := indexByte(line, '=')
		if eq < 0 {
			continue
		}
		k := trimSpace(line[:eq])
		v := trimSpace(line[eq+1:])
		// Strip optional surrounding quotes
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		}
		// Don't overwrite already-set env (Fly secrets win).
		if _, ok := os.LookupEnv(k); !ok {
			_ = os.Setenv(k, v)
		}
	}
}

// Tiny string helpers to avoid pulling strings package above main's
// already-imported set — keeps the binary's import surface tight.
func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

func trimSpace(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t' || s[0] == '\r') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}
