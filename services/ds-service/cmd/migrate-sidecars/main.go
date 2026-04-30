// Command migrate-sidecars — Phase 2 U9 — one-shot backfill of
// `lib/audit/*.json` per-file audit sidecars into SQLite.
//
// Usage (from repo root):
//
//	go run ./services/ds-service/cmd/migrate-sidecars             # process every sidecar
//	go run ./services/ds-service/cmd/migrate-sidecars --slug=foo  # one only
//	go run ./services/ds-service/cmd/migrate-sidecars --dry-run   # report without writing
//	go run ./services/ds-service/cmd/migrate-sidecars --dir=lib/audit
//
// Idempotent. Re-running on already-migrated sidecars is a no-op (compares
// sidecar mtime to `backfill_markers.sidecar_mtime`).
//
// Note (2026-04-30): the repository today has no real per-file audit sidecars
// in lib/audit/. Only `index.json` (manifest) and `spacing-observed.json`
// (cross-file aggregate) live there, and we explicitly skip both. The CLI
// runs cleanly and reports zero work; it becomes useful once designers start
// running per-file audits via the plugin or `npm run audit`.

package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/db"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/projects"
)

// skipFiles is the small set of well-known non-sidecar JSON files in
// lib/audit/. Anything else with a .json extension is treated as a sidecar.
var skipFiles = map[string]bool{
	"index.json":            true,
	"spacing-observed.json": true,
	"audit-files.json":      true,
}

func main() {
	dir := flag.String("dir", "lib/audit", "directory containing per-file audit JSON sidecars")
	dbPath := flag.String("db", "services/ds-service/data/ds.db", "SQLite path")
	slug := flag.String("slug", "", "process only this sidecar slug (filename without .json)")
	dryRun := flag.Bool("dry-run", false, "log what would happen without writing")
	flag.Parse()

	if err := run(*dir, *dbPath, *slug, *dryRun); err != nil {
		log.Fatalf("migrate-sidecars: %v", err)
	}
}

func run(dir, dbPath, onlySlug string, dryRun bool) error {
	systemTenant := envDefault("DS_SYSTEM_TENANT_ID", "system")
	systemUser := envDefault("DS_SYSTEM_USER_ID", "system")

	abs, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("abs %q: %w", dir, err)
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return fmt.Errorf("read dir %q: %w", abs, err)
	}

	var sidecars []os.DirEntry
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		if skipFiles[name] {
			continue
		}
		if onlySlug != "" && strings.TrimSuffix(name, ".json") != onlySlug {
			continue
		}
		sidecars = append(sidecars, e)
	}

	log.Printf("migrate-sidecars: found %d sidecar(s) in %s", len(sidecars), abs)
	if len(sidecars) == 0 {
		log.Printf("migrate-sidecars: nothing to backfill (the repo today has no per-file audit sidecars in this directory; this is expected)")
		return nil
	}

	if dryRun {
		for _, e := range sidecars {
			log.Printf("[dry-run] would process %s", e.Name())
		}
		return nil
	}

	d, err := db.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open db %q: %w", dbPath, err)
	}
	defer d.Close()

	ensureSystemUserTenant(d, systemUser, systemTenant)

	bf := &projects.Backfiller{
		DB:             d.DB,
		SystemTenantID: systemTenant,
		SystemUserID:   systemUser,
	}

	created, updated, skipped := 0, 0, 0
	for i, e := range sidecars {
		if i%50 == 0 && i > 0 {
			log.Printf("migrate-sidecars: progress %d/%d", i, len(sidecars))
		}
		full := filepath.Join(abs, e.Name())
		bytes, err := os.ReadFile(full)
		if err != nil {
			log.Printf("read %s: %v", e.Name(), err)
			continue
		}
		info, err := os.Stat(full)
		if err != nil {
			log.Printf("stat %s: %v", e.Name(), err)
			continue
		}
		slug := strings.TrimSuffix(e.Name(), ".json")
		res, err := bf.BackfillSidecar(context.Background(), full, slug, bytes, info.ModTime())
		if err != nil {
			log.Printf("backfill %s: %v", slug, err)
			continue
		}
		switch res.Action {
		case "created":
			created++
		case "updated":
			updated++
		case "skip_unchanged":
			skipped++
		}
	}

	log.Printf("migrate-sidecars: done — created=%d updated=%d skipped=%d", created, updated, skipped)
	return nil
}

func envDefault(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// ensureSystemUserTenant creates the system user + tenant rows on first run if
// they don't exist. Otherwise the FKs on synthetic projects fail.
func ensureSystemUserTenant(d *db.DB, userID, tenantID string) {
	now := time.Now().UTC().Format(time.RFC3339)

	_, err := d.DB.ExecContext(context.Background(),
		`INSERT OR IGNORE INTO users (id, email, password_hash, role, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		userID, "system@indmoney.local", "x", "user", now,
	)
	if err != nil {
		log.Printf("ensure system user: %v", err)
	}

	_, err = d.DB.ExecContext(context.Background(),
		`INSERT OR IGNORE INTO tenants (id, slug, name, status, plan_type, created_at, created_by)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		tenantID, "system", "System", "active", "free", now, userID,
	)
	if err != nil {
		log.Printf("ensure system tenant: %v", err)
	}
}
