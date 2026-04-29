// Migration runner backed by numbered SQL files at services/ds-service/migrations/.
//
// Discipline (per docs/plans/2026-04-29-001 U1):
//   - Each migration is a numbered file `NNNN_description.up.sql`.
//   - schema_migrations(version, name, applied_at) tracks what's been run.
//   - Forward-only: never DROP COLUMN within the release that stops writing
//     it; renames are 3-release dual-write + cutover + drop-old.
//   - Each migration runs in its own transaction; partial-failure leaves the
//     DB in the prior state.
//
// Why not the prior `[]string{...}` inline pattern: moving to numbered files
// makes the schema readable as a unit, supports replay testing in CI, and
// gives Phase 2+ a clean place to land additive migrations without bloating
// `db.go`.

package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"

	migrationsPkg "github.com/indmoney/design-system-docs/services/ds-service/migrations"
)

// migrationsFS is the embed.FS exported by the sibling migrations package.
// embed.FS can't traverse upward via "../../", so the embed declaration
// lives next to the .sql files in services/ds-service/migrations/embed.go.
// Aliased to migrationsPkg here because db.go already declares a `migrations`
// slice for the legacy Phase 0 inline schema.
var migrationsFS = migrationsPkg.FS

// applyVersionedMigrations creates the tracking table if missing, then runs
// every NNNN_*.up.sql file whose version is not yet recorded. Each file runs
// in a single transaction.
func (d *DB) applyVersionedMigrations(ctx context.Context) error {
	if _, err := d.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    INTEGER PRIMARY KEY,
			name       TEXT NOT NULL,
			applied_at TEXT NOT NULL
		)
	`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	files, err := listMigrationFiles()
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}

	applied, err := d.loadAppliedVersions(ctx)
	if err != nil {
		return fmt.Errorf("load applied: %w", err)
	}

	for _, f := range files {
		if applied[f.Version] {
			continue
		}
		if err := d.applyOne(ctx, f); err != nil {
			return fmt.Errorf("apply %s: %w", f.Name, err)
		}
	}
	return nil
}

type migrationFile struct {
	Version int
	Name    string
	SQL     string
}

func listMigrationFiles() ([]migrationFile, error) {
	entries, err := fs.ReadDir(migrationsFS, ".")
	if err != nil {
		return nil, err
	}
	var files []migrationFile
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		ver, err := parseMigrationVersion(name)
		if err != nil {
			return nil, fmt.Errorf("malformed migration filename %q: %w", name, err)
		}
		body, err := fs.ReadFile(migrationsFS, name)
		if err != nil {
			return nil, err
		}
		files = append(files, migrationFile{Version: ver, Name: name, SQL: string(body)})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Version < files[j].Version })
	return files, nil
}

// parseMigrationVersion extracts the leading integer from "0001_foo.up.sql".
func parseMigrationVersion(filename string) (int, error) {
	idx := strings.Index(filename, "_")
	if idx <= 0 {
		return 0, errors.New("missing underscore separator")
	}
	return strconv.Atoi(filename[:idx])
}

func (d *DB) loadAppliedVersions(ctx context.Context) (map[int]bool, error) {
	rows, err := d.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int]bool{}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out[v] = true
	}
	return out, rows.Err()
}

func (d *DB) applyOne(ctx context.Context, f migrationFile) error {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Execute the migration SQL. SQLite's exec accepts multiple statements
	// separated by semicolons in a single call when the driver supports it;
	// modernc.org/sqlite does support multi-statement Exec.
	if _, err := tx.ExecContext(ctx, f.SQL); err != nil {
		return fmt.Errorf("exec migration body: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)`,
		f.Version, f.Name, time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		return fmt.Errorf("record applied: %w", err)
	}
	return tx.Commit()
}

// AppliedMigrations returns the list of versions recorded in schema_migrations,
// sorted ascending. Used by tests + the /__health endpoint.
func (d *DB) AppliedMigrations(ctx context.Context) ([]int, error) {
	rows, err := d.QueryContext(ctx, `SELECT version FROM schema_migrations ORDER BY version ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// underscore-only used to make the linter happy about the unused import in
// some build configurations.
var _ = sql.ErrNoRows
