// Command admin — Phase 2 ds-service operator CLI.
//
// Subcommands:
//
//	fanout            POST /v1/admin/audit/fanout — re-audit every active flow's
//	                  latest version. Use after a token publish or rule curation
//	                  change. Requires a super-admin JWT (env DS_ADMIN_TOKEN
//	                  or --token flag).
//	migrate-sidecars  Backfill lib/audit/*.json sidecars into SQLite. ONE-SHOT
//	                  utility used during Phase 2 cutover; safe to re-run
//	                  (idempotent). Direct DB connection; does NOT go through
//	                  the HTTP API. (Phase 2 U9 ships the actual implementation;
//	                  this scaffold reserves the subcommand.)
//
// Usage:
//
//	admin fanout --trigger=tokens_published --reason="renamed colour.surface.bg"
//	admin fanout --trigger=rule_changed --rule-id=theme_parity_break --reason="..."
//	admin migrate-sidecars [--dry-run]
//
// Server URL via env DS_ADMIN_URL (default http://localhost:7475). Token via env
// DS_ADMIN_TOKEN or --token flag.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/db"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "fanout":
		os.Exit(runFanout(os.Args[2:]))
	case "retention":
		os.Exit(runRetention(os.Args[2:]))
	case "migrate-sidecars":
		fmt.Fprintln(os.Stderr, "admin: migrate-sidecars subcommand reserved; ships in Phase 2 U9.")
		fmt.Fprintln(os.Stderr, "       (Run go run ./cmd/migrate-sidecars when that lands.)")
		os.Exit(2)
	case "-h", "--help", "help":
		usage()
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "admin: unknown subcommand %q\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage: admin <subcommand> [flags]

subcommands:
  fanout            Re-audit every active flow's latest version (POST /v1/admin/audit/fanout)
  retention         Delete audit_log rows older than --days (Phase 4 U13)
  migrate-sidecars  Backfill lib/audit/*.json into SQLite (Phase 2 U9; reserved)

env:
  DS_ADMIN_URL     Server base URL (default http://localhost:7475)
  DS_ADMIN_TOKEN   Super-admin JWT (overridable via --token flag on each subcommand)
  DS_DB_PATH       Path to ds.db for direct-DB subcommands (default services/ds-service/data/ds.db)`)
}

// ─── fanout ─────────────────────────────────────────────────────────────────

func runFanout(args []string) int {
	fs := flag.NewFlagSet("fanout", flag.ExitOnError)
	trigger := fs.String("trigger", "", "tokens_published | rule_changed (required)")
	reason := fs.String("reason", "", "Free-text reason recorded with the fan-out (required, ≤512 chars)")
	ruleID := fs.String("rule-id", "", "rule_id when --trigger=rule_changed")
	tokenKeysCSV := fs.String("token-keys", "", "Comma-separated token keys when --trigger=tokens_published")
	url := fs.String("url", os.Getenv("DS_ADMIN_URL"), "Server base URL (env DS_ADMIN_URL)")
	token := fs.String("token", os.Getenv("DS_ADMIN_TOKEN"), "Super-admin JWT (env DS_ADMIN_TOKEN)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *trigger == "" || *reason == "" {
		fmt.Fprintln(os.Stderr, "admin fanout: --trigger and --reason are required")
		fs.Usage()
		return 2
	}
	if *url == "" {
		*url = "http://localhost:7475"
	}
	if *token == "" {
		fmt.Fprintln(os.Stderr, "admin fanout: --token or DS_ADMIN_TOKEN required (super-admin JWT)")
		return 2
	}

	body := map[string]any{
		"trigger": *trigger,
		"reason":  *reason,
	}
	if *ruleID != "" {
		body["rule_id"] = *ruleID
	}
	if *tokenKeysCSV != "" {
		var keys []string
		for _, k := range strings.Split(*tokenKeysCSV, ",") {
			if k = strings.TrimSpace(k); k != "" {
				keys = append(keys, k)
			}
		}
		body["token_keys"] = keys
	}

	bs, err := json.Marshal(body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "admin fanout: marshal body: %v\n", err)
		return 1
	}

	endpoint := strings.TrimRight(*url, "/") + "/v1/admin/audit/fanout"
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(bs))
	if err != nil {
		fmt.Fprintf(os.Stderr, "admin fanout: build request: %v\n", err)
		return 1
	}
	req.Header.Set("Authorization", "Bearer "+*token)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "admin fanout: %v\n", err)
		return 1
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		fmt.Fprintf(os.Stderr, "admin fanout: HTTP %d: %s\n", resp.StatusCode, string(respBody))
		return 1
	}

	var out map[string]any
	if err := json.Unmarshal(respBody, &out); err != nil {
		fmt.Println(string(respBody))
		return 0
	}
	pretty, _ := json.MarshalIndent(out, "", "  ")
	fmt.Println(string(pretty))
	return 0
}

// ─── retention ──────────────────────────────────────────────────────────────

// runRetention deletes audit_log rows older than --days. Phase 4 U13 — the
// Phase 0 audit_log table has no built-in retention policy; ops runs this
// weekly via cron to keep the DB bounded.
//
// Direct DB connection (no HTTP). Uses db.Open which applies migrations on
// startup; running this against a fresh / empty DB is safe (zero rows
// deleted). Always reports the row count + cutoff so cron logs the impact.
func runRetention(args []string) int {
	fs := flag.NewFlagSet("retention", flag.ExitOnError)
	days := fs.Int("days", 90, "Delete audit_log rows older than this many days")
	dbPath := fs.String("db", os.Getenv("DS_DB_PATH"), "Path to ds.db (env DS_DB_PATH)")
	dryRun := fs.Bool("dry-run", false, "Print what would be deleted without writing")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *days <= 0 {
		fmt.Fprintln(os.Stderr, "admin retention: --days must be > 0")
		return 2
	}
	if *dbPath == "" {
		*dbPath = "services/ds-service/data/ds.db"
	}

	d, err := db.Open(*dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "admin retention: open db %q: %v\n", *dbPath, err)
		return 1
	}
	defer d.Close()

	cutoff := time.Now().UTC().AddDate(0, 0, -*days).Format(time.RFC3339)
	ctx := context.Background()

	var preCount int
	if err := d.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM audit_log WHERE ts < ?`, cutoff).Scan(&preCount); err != nil {
		fmt.Fprintf(os.Stderr, "admin retention: count: %v\n", err)
		return 1
	}
	if *dryRun {
		fmt.Printf("[dry-run] would delete %d audit_log rows older than %s (cutoff %s)\n",
			preCount, fmt.Sprintf("%d days", *days), cutoff)
		return 0
	}

	res, err := d.ExecContext(ctx,
		`DELETE FROM audit_log WHERE ts < ?`, cutoff)
	if err != nil {
		fmt.Fprintf(os.Stderr, "admin retention: delete: %v\n", err)
		return 1
	}
	n, _ := res.RowsAffected()
	fmt.Printf("admin retention: deleted %d audit_log rows older than %d days (cutoff %s)\n",
		n, *days, cutoff)
	return 0
}
