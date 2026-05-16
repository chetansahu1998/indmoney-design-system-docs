// cmd/figma-autosync-dryrun — U8 of the autosync bridge plan
// (docs/plans/2026-05-14-001-feat-figma-db-autosync-bridge-plan.md).
//
// One-shot CLI that runs Planner.Plan() against the local DB and prints
// a JSON/text plan. Read-only — never writes to figma_auto_sync_state,
// never calls runExport. Lets admins eyeball what the planner WOULD do
// before Phase C enables writes.
//
// Usage:
//
//	go run ./cmd/figma-autosync-dryrun \
//	    -tenant e090530f-2698-489d-934a-c821cb925c8a \
//	    -file-key 2m7ouydXKfxYk7hhjQxrt7
//
//	go run ./cmd/figma-autosync-dryrun \
//	    -tenant e090530f-2698-489d-934a-c821cb925c8a \
//	    -skip-empty
//
// Omitting -file-key runs PlanTenant — every in-window mapped file.
// -skip-empty drops sections whose action=skip_unchanged for readability.
// -format=text|json (default text).
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/db"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/figma/inventory"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/projects"
)

func main() {
	var (
		tenantID  = flag.String("tenant", "", "tenant_id (required)")
		fileKey   = flag.String("file-key", "", "single file_key (optional; default: run PlanTenant)")
		dbPath    = flag.String("db", "", "path to ds.db (default: services/ds-service/data/ds.db found upward)")
		format    = flag.String("format", "text", "output format: text|json")
		skipEmpty = flag.Bool("skip-empty", false, "drop sections with action=skip_unchanged in text output")
		limit     = flag.Int("limit", 0, "in PlanTenant mode, cap files inspected (0 = all)")
	)
	flag.Parse()

	if *tenantID == "" {
		fmt.Fprintln(os.Stderr, "usage: figma-autosync-dryrun -tenant <id> [-file-key <key>] [-format text|json] [-skip-empty] [-limit N]")
		os.Exit(2)
	}

	loadDotEnv() // optional; lets the user reuse FIGMA_PAT shape from the inventory CLI

	dbFile := *dbPath
	if dbFile == "" {
		dbFile = findDB()
	}
	if dbFile == "" {
		fmt.Fprintln(os.Stderr, "cannot locate ds.db — pass -db <path>")
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "opening db: %s\n", dbFile)

	d, err := db.Open(dbFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "db open:", err)
		os.Exit(1)
	}
	defer d.Close()

	planner := inventory.NewPlanner(adapter{d: d}, inventory.PlannerConfig{Now: time.Now})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	var plans []inventory.FilePlan
	if *fileKey != "" {
		fp, err := planner.Plan(ctx, *tenantID, *fileKey)
		if err != nil {
			fmt.Fprintln(os.Stderr, "plan:", err)
			os.Exit(1)
		}
		plans = []inventory.FilePlan{fp}
	} else {
		plans, err = planner.PlanTenant(ctx, *tenantID)
		if err != nil {
			fmt.Fprintln(os.Stderr, "plan tenant:", err)
			os.Exit(1)
		}
		if *limit > 0 && len(plans) > *limit {
			plans = plans[:*limit]
		}
	}

	if *format == "json" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(plans); err != nil {
			fmt.Fprintln(os.Stderr, "json encode:", err)
			os.Exit(1)
		}
		return
	}

	// Text format.
	renderText(plans, *skipEmpty)
}

// renderText prints a human-readable plan summary. One file per block.
func renderText(plans []inventory.FilePlan, skipEmpty bool) {
	var totalFiles, totalSyncRequired, totalCheap, totalSkipUnchanged, totalSkipQuar, totalFileSkipped int

	for _, fp := range plans {
		totalFiles++
		fmt.Printf("\n══════ %s ", fp.FileName)
		if fp.ProjectName != "" {
			fmt.Printf("(project: %s) ", fp.ProjectName)
		}
		fmt.Printf("══════\n")
		fmt.Printf("  file_key: %s\n", fp.FileKey)

		if fp.FileSkip != nil {
			totalFileSkipped++
			fmt.Printf("  FILE SKIPPED: %s — %s\n", fp.FileSkip.Code, fp.FileSkip.Message)
			continue
		}

		// Bucket sections by action for the summary line.
		var fullCnt, cheapCnt, skipCnt, quarCnt int
		for _, s := range fp.Sections {
			switch s.Action {
			case inventory.ActionFullExport:
				fullCnt++
			case inventory.ActionCheapUpdate:
				cheapCnt++
			case inventory.ActionSkipUnchanged:
				skipCnt++
			case inventory.ActionSkipQuarantined:
				quarCnt++
			}
		}
		totalSyncRequired += fullCnt
		totalCheap += cheapCnt
		totalSkipUnchanged += skipCnt
		totalSkipQuar += quarCnt

		fmt.Printf("  sections: %d total | %d full_export | %d cheap_update | %d skip_unchanged | %d quarantined\n",
			len(fp.Sections), fullCnt, cheapCnt, skipCnt, quarCnt)

		for _, s := range fp.Sections {
			if skipEmpty && s.Action == inventory.ActionSkipUnchanged {
				continue
			}
			marker := actionMarker(s.Action)
			fmt.Printf("    %s [%s/%s] %s\n", marker, s.SubProduct, s.SubFlow, sectionLineDetail(s))
		}
	}

	fmt.Printf("\n──────── totals ────────\n")
	fmt.Printf("  files inspected:     %d\n", totalFiles)
	fmt.Printf("  files skipped:       %d\n", totalFileSkipped)
	fmt.Printf("  sections full_export:%d\n", totalSyncRequired)
	fmt.Printf("  sections cheap_update:%d\n", totalCheap)
	fmt.Printf("  sections skip_unchanged:%d\n", totalSkipUnchanged)
	fmt.Printf("  sections quarantined:%d\n", totalSkipQuar)
}

func actionMarker(a inventory.PlanAction) string {
	switch a {
	case inventory.ActionFullExport:
		return "✦"
	case inventory.ActionCheapUpdate:
		return "↺"
	case inventory.ActionSkipUnchanged:
		return "·"
	case inventory.ActionSkipQuarantined:
		return "!"
	}
	return "?"
}

func sectionLineDetail(s inventory.PlannedSync) string {
	var parts []string
	parts = append(parts, fmt.Sprintf("page=%s", s.PageName))
	if s.PersonaHint != "" && s.PersonaHint != "default" {
		parts = append(parts, fmt.Sprintf("persona=%s", s.PersonaHint))
	}
	if s.Reason != "" {
		parts = append(parts, fmt.Sprintf("reason=%s", s.Reason))
	}
	if s.SkipReason != "" {
		parts = append(parts, fmt.Sprintf("skip=%s", s.SkipReason))
	}
	if s.Action == inventory.ActionFullExport && s.PriorContentHash != "" {
		parts = append(parts,
			fmt.Sprintf("hash %s→%s", short(s.PriorContentHash), short(s.LiveContentHash)),
		)
	}
	return strings.Join(parts, " · ")
}

func short(h string) string {
	if len(h) > 8 {
		return h[:8]
	}
	return h
}

// adapter bridges *db.DB to the planner's AutoSyncDB interface.
type adapter struct{ d *db.DB }

func (a adapter) NewTenantRepo(tenantID string) *projects.TenantRepo {
	return projects.NewTenantRepo(a.d.DB, tenantID)
}

// ─── DB + env helpers (lifted from cmd/figma-inventory-sync — same shape) ────

func findDB() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	for d := cwd; d != "/" && d != ""; d = filepath.Dir(d) {
		alt := filepath.Join(d, "data", "ds.db")
		if _, err := os.Stat(alt); err == nil {
			return alt
		}
		candidate := filepath.Join(d, "services", "ds-service", "data", "ds.db")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

func loadDotEnv() {
	cwd, err := os.Getwd()
	if err != nil {
		return
	}
	for d := cwd; d != "/" && d != ""; d = filepath.Dir(d) {
		path := filepath.Join(d, ".env.local")
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			eq := strings.IndexByte(line, '=')
			if eq <= 0 {
				continue
			}
			k := strings.TrimSpace(line[:eq])
			v := strings.TrimSpace(line[eq+1:])
			v = strings.Trim(v, `"'`)
			if _, ok := os.LookupEnv(k); !ok {
				os.Setenv(k, v)
			}
		}
		return
	}
}
