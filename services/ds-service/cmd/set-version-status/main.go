// Command set-version-status flips a project_versions row's status (and
// optional error message) directly via TenantRepo.SetVersionStatus. Used
// by ops to:
//
//   - Synthesize a `failed` version for testing T4's retry UX without a
//     real Figma render error.
//   - Recover a `pending` version that's stuck because its pipeline
//     goroutine crashed before flipping the row to view_ready / failed
//     (the recovery sweeper handles this automatically, but operators
//     may want to force the transition).
//
// Usage on Fly:
//
//	fly ssh console -C "/usr/local/bin/set-version-status \
//	    --tenant <tenant-id> \
//	    --version <version-id> \
//	    --status failed \
//	    --error 'synthetic failure for retry testing'"
//
// Local: go run ./cmd/set-version-status from the ds-service module root,
// reads SQLITE_PATH (default services/ds-service/data/ds.db).
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/db"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/projects"
)

func main() {
	tenant := flag.String("tenant", "", "tenant_id that owns the version")
	versionID := flag.String("version", "", "project_versions.id to mutate")
	status := flag.String("status", "", "new status: pending | view_ready | failed")
	errMsg := flag.String("error", "", "error message (only persisted when --status=failed)")
	flag.Parse()

	if *tenant == "" || *versionID == "" || *status == "" {
		fmt.Fprintln(os.Stderr, "usage: set-version-status --tenant <id> --version <id> --status pending|view_ready|failed [--error <msg>]")
		os.Exit(2)
	}

	dbPath := os.Getenv("SQLITE_PATH")
	if dbPath == "" {
		dbPath = filepath.Join("services", "ds-service", "data", "ds.db")
	}
	conn, err := db.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open db %s: %v\n", dbPath, err)
		os.Exit(1)
	}
	defer conn.Close()

	repo := projects.NewTenantRepo(conn.DB, *tenant)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := repo.SetVersionStatus(ctx, *versionID, *status, *errMsg); err != nil {
		fmt.Fprintf(os.Stderr, "set: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("ok version=%s status=%s err=%q\n", *versionID, *status, *errMsg)
}
