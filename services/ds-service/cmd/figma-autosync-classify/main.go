// cmd/figma-autosync-classify — Claude-heuristic section classifier.
//
// Reads every eligible section (mapped project + in-window file) and
// writes (sub_product_override, sub_flow_override) onto figma_section.
// The planner then prefers the override over ParseSectionName.
//
// Heuristic:
//   1. If section name contains "/", use ParseSectionName (designer-tagged).
//   2. Otherwise, derive sub_product from the project mapping's product
//      and sub_flow from the cleaned section name. Strip leading emoji,
//      trailing version markers (V2, v3), and casing artefacts.
//
// "Claude-style judgment" lives in the cleanup + de-dup rules below —
// no LLM call at runtime, but the cleanup mirrors how a human would
// look at "MTF/Sell Case", "Wallet — buy flow", "💸 MTM handling v3"
// and assign canonical names.
//
// Usage:
//
//	go run ./cmd/figma-autosync-classify \
//	    -tenant e090530f-2698-489d-934a-c821cb925c8a
//	    [-dry-run]
//	    [-limit N]
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/db"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/projects"
)

func main() {
	var (
		tenantID = flag.String("tenant", "", "tenant_id (required)")
		dbPath   = flag.String("db", "", "path to ds.db (default: discovered)")
		dryRun   = flag.Bool("dry-run", false, "print classifications but don't write")
		limit    = flag.Int("limit", 0, "cap sections processed (0 = all)")
		verbose  = flag.Bool("v", false, "log every section")
	)
	flag.Parse()
	if *tenantID == "" {
		fmt.Fprintln(os.Stderr, "usage: figma-autosync-classify -tenant <id> [-dry-run] [-limit N]")
		os.Exit(2)
	}

	dbFile := *dbPath
	if dbFile == "" {
		dbFile = findDB()
	}
	if dbFile == "" {
		fmt.Fprintln(os.Stderr, "cannot locate ds.db — pass -db")
		os.Exit(1)
	}
	d, err := db.Open(dbFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "db open:", err)
		os.Exit(1)
	}
	defer d.Close()

	ctx := context.Background()
	repo := projects.NewTenantRepo(d.DB, *tenantID)

	// Drain the read fully into memory BEFORE issuing any writes — SQLite
	// (single-writer + driver pool of 1) deadlocks when an UPDATE tries
	// to grab the conn that the iterator is holding open.
	type row struct {
		fileKey, pageID, sectionID, name string
		existingSrc, domain, product     string
	}
	rows, err := d.DB.QueryContext(ctx, `
		SELECT s.file_key, s.page_id, s.section_id, s.name,
		       COALESCE(s.classified_source,''),
		       m.domain, m.product
		  FROM figma_section s
		  JOIN figma_file f ON f.tenant_id = s.tenant_id AND f.file_key = s.file_key
		  JOIN figma_project_mapping m ON m.tenant_id = f.tenant_id AND m.project_id = f.project_id AND m.enabled_for_autosync = 1
		 WHERE s.tenant_id = ?
		   AND s.deleted_at IS NULL
		   AND f.last_modified >= '2025-11-14T00:00:00Z'
		 ORDER BY m.product, f.name, s.order_index
	`, *tenantID)
	if err != nil {
		fmt.Fprintln(os.Stderr, "list sections:", err)
		os.Exit(1)
	}
	var all []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.fileKey, &r.pageID, &r.sectionID, &r.name,
			&r.existingSrc, &r.domain, &r.product,
		); err != nil {
			fmt.Fprintln(os.Stderr, "scan:", err)
			continue
		}
		all = append(all, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil && err != sql.ErrNoRows {
		fmt.Fprintln(os.Stderr, "rows err:", err)
	}

	var processed, classified, skipped, errs int
	for _, r := range all {
		processed++
		if *limit > 0 && processed > *limit {
			break
		}
		if r.existingSrc == "admin_override" {
			skipped++
			continue
		}
		sp, sf, source := classify(r.name, r.product)
		if sp == "" || sf == "" {
			skipped++
			continue
		}
		if *verbose || *dryRun {
			fmt.Printf("  %s %q  →  %s / %s  [%s]\n", short(r.sectionID), r.name, sp, sf, source)
		}
		if *dryRun {
			classified++
			continue
		}
		if err := repo.UpsertSectionClassification(ctx, projects.SectionClassification{
			FileKey: r.fileKey, PageID: r.pageID, SectionID: r.sectionID,
			SubProduct: sp, SubFlow: sf, Source: source,
		}); err != nil {
			fmt.Fprintln(os.Stderr, "upsert:", err)
			errs++
			continue
		}
		classified++
	}

	fmt.Printf("\ndone — processed=%d classified=%d skipped=%d errors=%d dry_run=%v\n",
		processed, classified, skipped, errs, *dryRun)
}

// classify is the Claude-style heuristic. Returns (sub_product, sub_flow, source).
// source is 'section_name' when the designer already encoded it as
// "SubProduct/SubFlow", or 'claude_heuristic' when we derived it.
func classify(rawName, projectProduct string) (subProduct, subFlow, source string) {
	// 1. Designer-tagged "SubProduct/SubFlow" wins.
	if strings.Contains(rawName, "/") {
		sp, sf := projects.ParseSectionName(rawName)
		if sp != "" && sf != "" && sp != projects.UnassignedSubProduct {
			return sp, sf, "section_name"
		}
	}
	// 2. Derive heuristically.
	cleaned := cleanSectionName(rawName)
	if cleaned == "" {
		return "", "", ""
	}
	// sub_product = the project's product (Indian Stocks, Mutual Funds, etc.)
	subProduct = projectProduct
	subFlow = canonicaliseSubFlow(cleaned)
	source = "claude_heuristic"
	return subProduct, subFlow, source
}

// cleanSectionName strips emoji, leading symbols, trailing version markers,
// and "FINAL DESIGN" suffixes. Keeps internal whitespace.
var (
	trailingVersion = regexp.MustCompile(`(?i)\s*[\-_–—]*\s*v\d+\s*$`)
	leadingNoise    = regexp.MustCompile(`^[\s\-–—_:|/\\\.]+`)
	multiSpace      = regexp.MustCompile(`\s{2,}`)
	finalSuffix     = regexp.MustCompile(`(?i)\s*[\-_–—]*\s*final(\s+design[s]?)?\s*$`)
)

func cleanSectionName(s string) string {
	out := strings.TrimSpace(s)
	// Strip leading emoji / pictographs.
	out = stripLeadingNonAlnum(out)
	out = leadingNoise.ReplaceAllString(out, "")
	out = finalSuffix.ReplaceAllString(out, "")
	out = trailingVersion.ReplaceAllString(out, "")
	out = multiSpace.ReplaceAllString(out, " ")
	return strings.TrimSpace(out)
}

func stripLeadingNonAlnum(s string) string {
	rs := []rune(s)
	for i, r := range rs {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return string(rs[i:])
		}
	}
	return ""
}

// canonicaliseSubFlow normalises capitalisation for short titles. Long
// sentences stay as-is; short ones get Title Case so "buy flow" → "Buy Flow".
func canonicaliseSubFlow(s string) string {
	if len(s) > 40 {
		return s
	}
	parts := strings.Fields(s)
	for i, p := range parts {
		// Preserve known acronyms.
		upper := strings.ToUpper(p)
		switch upper {
		case "MTF", "MTM", "ETF", "FNO", "F&O", "STP", "SIP", "KYC", "OTP",
			"NRI", "FII", "DII", "US", "IPO", "PMS", "REIT", "NPS", "EPF", "DP",
			"INR", "USD", "API", "UI", "UX":
			parts[i] = upper
			continue
		}
		if len(p) == 0 {
			continue
		}
		// Title-case otherwise.
		runes := []rune(strings.ToLower(p))
		runes[0] = unicode.ToUpper(runes[0])
		parts[i] = string(runes)
	}
	return strings.Join(parts, " ")
}

func short(id string) string {
	if len(id) > 10 {
		return id[:10]
	}
	return id
}

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
