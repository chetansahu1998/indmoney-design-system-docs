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
	cryptoRand "crypto/rand"
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
	case "seed-fixtures":
		os.Exit(runSeedFixtures(os.Args[2:]))
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
  seed-fixtures     Insert demo personas, taxonomy, notifications, prefs for an empty tenant
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

// ─── seed-fixtures ──────────────────────────────────────────────────────────
//
// Inserts demo rows so the empty-tenant UI surfaces (atlas/admin/personas,
// /atlas/admin/taxonomy, /inbox, /settings/notifications) render content
// instead of empty states. Idempotent — uses INSERT OR IGNORE so re-runs
// don't duplicate rows. Tagged with `created_by_user_id = system@indmoney.local`
// so future cleanup can drop fixtures with one DELETE.
//
// Usage:
//
//	admin seed-fixtures --tenant=<tenant_id> [--user=<user_email>] [--db=<path>]
//
// Tenant required. Defaults: --user=chetan@indmoney.com, --db=services/ds-service/data/ds.db.
func runSeedFixtures(args []string) int {
	fs := flag.NewFlagSet("seed-fixtures", flag.ExitOnError)
	tenantID := fs.String("tenant", "", "Target tenant_id (required)")
	userEmail := fs.String("user", "chetan@indmoney.com", "User email used as created_by for personas + recipient for notifications")
	dbPath := fs.String("db", os.Getenv("DS_DB_PATH"), "Path to ds.db (env DS_DB_PATH)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *tenantID == "" {
		fmt.Fprintln(os.Stderr, "admin seed-fixtures: --tenant is required")
		return 2
	}
	if *dbPath == "" {
		*dbPath = "services/ds-service/data/ds.db"
	}

	d, err := db.Open(*dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "admin seed-fixtures: open db %q: %v\n", *dbPath, err)
		return 1
	}
	defer d.Close()

	ctx := context.Background()
	now := time.Now().UTC().Format(time.RFC3339)

	// Resolve recipient + system user IDs (system@indmoney.local is created
	// by migrations and used here as the audit attribution for fixtures).
	var userID string
	if err := d.QueryRowContext(ctx,
		`SELECT id FROM users WHERE email = ?`, *userEmail,
	).Scan(&userID); err != nil {
		fmt.Fprintf(os.Stderr, "admin seed-fixtures: lookup user %q: %v\n", *userEmail, err)
		return 1
	}
	var systemID string
	if err := d.QueryRowContext(ctx,
		`SELECT id FROM users WHERE email = 'system@indmoney.local'`,
	).Scan(&systemID); err != nil {
		// Fallback to user themselves so we don't hard-fail on tenants without
		// the system user. Future cleanup query becomes broader.
		systemID = userID
	}

	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "admin seed-fixtures: begin tx: %v\n", err)
		return 1
	}
	defer func() { _ = tx.Rollback() }()

	// 1. Personas — the four real design-system roles. All approved on
	// insert: pending personas only originate from the Figma plugin's
	// "suggest persona" flow when a designer tags a flow with a name
	// that's not in the canonical list. We never seed fake pending rows.
	personaSeeds := []string{
		"Designer (in-product)",
		"DS Lead",
		"PM",
		"Engineer",
	}
	personasIns := 0
	for _, name := range personaSeeds {
		id := uuidString()
		res, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO personas
			   (id, tenant_id, name, status, created_by_user_id, approved_by_user_id, approved_at, created_at)
			 VALUES (?, ?, ?, 'approved', ?, ?, ?, ?)`,
			id, *tenantID, name, systemID, systemID, now, now)
		if err != nil {
			fmt.Fprintf(os.Stderr, "admin seed-fixtures: insert persona %q: %v\n", name, err)
			return 1
		}
		if n, _ := res.RowsAffected(); n > 0 {
			personasIns++
		}
	}

	// 2. Canonical taxonomy — derive from existing projects' (product, path).
	rows, err := tx.QueryContext(ctx,
		`SELECT DISTINCT product, path FROM projects WHERE tenant_id = ? AND deleted_at IS NULL`,
		*tenantID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "admin seed-fixtures: list projects: %v\n", err)
		return 1
	}
	type pp struct{ product, path string }
	var paths []pp
	for rows.Next() {
		var p pp
		if err := rows.Scan(&p.product, &p.path); err != nil {
			rows.Close()
			fmt.Fprintf(os.Stderr, "admin seed-fixtures: scan project: %v\n", err)
			return 1
		}
		paths = append(paths, p)
	}
	rows.Close()
	taxonomyIns := 0
	for i, p := range paths {
		// Insert both the product root (path="") and the leaf path so the tree
		// renders with the product as a parent. Order index follows insertion.
		_, _ = tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO canonical_taxonomy
			   (tenant_id, product, path, promoted_by, promoted_at, order_index)
			 VALUES (?, ?, '', ?, ?, ?)`,
			*tenantID, p.product, systemID, now, i*10)
		res, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO canonical_taxonomy
			   (tenant_id, product, path, promoted_by, promoted_at, order_index)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			*tenantID, p.product, p.path, systemID, now, i*10+1)
		if err != nil {
			fmt.Fprintf(os.Stderr, "admin seed-fixtures: insert taxonomy %q/%q: %v\n", p.product, p.path, err)
			return 1
		}
		if n, _ := res.RowsAffected(); n > 0 {
			taxonomyIns++
		}
	}

	// 3. Notifications — NOT seeded. Real notifications come from real
	// activity (audit_jobs producing violations, DRD edits, persona
	// approvals). The /v1/inbox endpoint already streams audit-driven
	// content (violations) without a notifications-table seed; mention
	// notifications only fire when a real comment @-mentions a real user.
	notifIns := 0

	// 4. Notification preferences — defaults so /settings/notifications has
	// rows to render rather than spinning on the loading state.
	prefIns := 0
	for _, ch := range []string{"slack", "email"} {
		res, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO notification_preferences
			   (user_id, channel, cadence, slack_webhook_url, email_address, user_tz, updated_at)
			 VALUES (?, ?, 'off', '', ?, 'Asia/Kolkata', ?)`,
			userID, ch, *userEmail, now)
		if err != nil {
			fmt.Fprintf(os.Stderr, "admin seed-fixtures: insert pref %q: %v\n", ch, err)
			return 1
		}
		if r, _ := res.RowsAffected(); r > 0 {
			prefIns++
		}
	}

	if err := tx.Commit(); err != nil {
		fmt.Fprintf(os.Stderr, "admin seed-fixtures: commit: %v\n", err)
		return 1
	}

	fmt.Printf("admin seed-fixtures: tenant=%s user=%s — personas=+%d taxonomy=+%d notifications=+%d prefs=+%d\n",
		*tenantID, *userEmail, personasIns, taxonomyIns, notifIns, prefIns)
	return 0
}

// uuidString returns a random UUIDv4 as a hex-with-dashes string. Inlined
// here because the only other UUID dep in this binary is via db.Open's
// transitive imports; pulling github.com/google/uuid for one call site
// would bloat the cmd binary by ~80KB.
func uuidString() string {
	b := make([]byte, 16)
	if _, err := cryptoRand.Read(b); err != nil {
		// Last-resort fallback — time-based, not cryptographically random
		// but adequate for fixture seeding which never goes to prod auth.
		t := time.Now().UnixNano()
		for i := 0; i < 16; i++ {
			b[i] = byte(t >> (8 * (i % 8)))
		}
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
