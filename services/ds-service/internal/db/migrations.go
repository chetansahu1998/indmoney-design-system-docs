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
	// Migrations whose filename contains `.no_tx.` (e.g.
	// `0015_tenant_fk_constraints.no_tx.up.sql`) opt out of the wrapping
	// transaction so they can issue PRAGMA statements that SQLite forbids
	// inside a tx — most importantly `PRAGMA foreign_keys = OFF`, required
	// for the SQLite "rebuild table" technique used to add FK constraints
	// to existing tables (T7 / plan 2026-05-03-001). The migration body
	// is responsible for its own atomicity (typically with an explicit
	// BEGIN/COMMIT inside the SQL).
	if strings.Contains(f.Name, ".no_tx.") {
		return d.applyOneNoTx(ctx, f)
	}

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

// applyOneNoTx runs a migration body without wrapping it in a transaction.
// The body must contain its own BEGIN/COMMIT if it wants atomicity. Used
// for migrations that need PRAGMA statements that are no-ops or errors
// inside a tx (foreign_keys, journal_mode).
//
// Critical: pins to a single sql.Conn for the whole body. The DSN sets
// foreign_keys=ON per connection, so without pinning a `PRAGMA foreign_
// keys = OFF` issued on one pool connection wouldn't carry over to the
// next ExecContext, which might land on a different connection with
// foreign_keys=ON re-applied. The test suite reproducer hit exactly this
// failure mode (error code 1811 mid-migration).
func (d *DB) applyOneNoTx(ctx context.Context, f migrationFile) error {
	conn, err := d.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire conn (no-tx): %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, f.SQL); err != nil {
		return fmt.Errorf("exec migration body (no-tx): %w", err)
	}
	if _, err := conn.ExecContext(ctx,
		`INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)`,
		f.Version, f.Name, time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		return fmt.Errorf("record applied (no-tx): %w", err)
	}
	return nil
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
