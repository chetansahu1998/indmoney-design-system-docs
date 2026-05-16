// Package db provides the SQLite layer for ds-service.
//
// Single-file local DB at services/ds-service/data/ds.db. Schema migrations
// are bundled at compile time and run on every startup (idempotent).
//
// Connection pooling (plan 2026-05-16-001-fix-sqlite-pool-split-plan.md):
//
// The package exposes two connection pools backed by the same file:
//
//   - Write pool — single connection (MaxOpenConns=1). Preserves the
//     single-writer invariant the codebase depends on (autosync
//     idempotency, sync orchestrator's no-per-tenant-lock posture,
//     worker lease semantics). Writes serialize through this conn.
//     The embedded *sql.DB IS the write pool, so every existing
//     *DB.ExecContext / *DB.QueryContext / *DB.BeginTx call routes
//     through the write pool. Helper methods (CreateUser, WriteAudit,
//     etc.) implicitly use the write pool.
//
//   - Read pool — multiple connections (MaxOpenConns=8) opened with
//     mode=ro. Concurrent readers under WAL — the parallelism that
//     SetMaxOpenConns(1) silently defeated for years. Access via
//     d.Read() *sql.DB. mode=ro is a hard guard: accidental writes
//     against d.Read() return "attempt to write a readonly database"
//     rather than silently landing on the wrong handle.
//
// Read-before-tx convention: code that opens a write transaction must
// finish all reads it needs BEFORE BeginTx. A write tx holds the only
// write connection; issuing a fresh read inside the tx deadlocks the
// process. See:
//   - docs/solutions/2026-05-01-003-phase-7-8-closure.md
//   - docs/solutions/2026-05-05-001-zeplin-canvas-learnings.md
//   - docs/plans/2026-05-13-002-feat-figma-db-phase-2-plan.md
//
// Read-your-write paths: heartbeat → recovery, executor → planner, and
// worker lease-renew MUST use the write pool (not d.Read()) so they
// observe their own commits without ms-staleness. d.Read() is for
// "best-effort fresh, ms-staleness acceptable" paths only.
//
// Tables:
//   users            — operators with login credentials (bcrypt password_hash)
//   tenants          — brand tenants (one per docs site brand)
//   tenant_users     — many-to-many user↔tenant with role
//   figma_tokens     — per-tenant encrypted Figma PATs (AES-GCM)
//   audit_log        — append-only operator action log (sync, login, etc.)
//   sync_state       — per-tenant last-sync metadata + canonical hash for skip-no-change
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// DB wraps the SQLite pools and exposes app-specific helpers.
//
// The embedded *sql.DB is the WRITE pool (single connection). Methods
// inherited via the embed — ExecContext, QueryContext, QueryRowContext,
// BeginTx, etc. — therefore route to the write pool. This is the safe
// default: writes obviously belong on the write pool, and reads done
// outside a deliberate read-only context don't risk read-your-write
// staleness.
//
// To explicitly opt into the read pool for concurrent-read paths, call
// d.Read() *sql.DB and issue queries through it. See the package-level
// doc-comment for the read-before-tx and read-your-write conventions.
//
// Write() is an alias for the embedded *sql.DB exposed as a named
// accessor; new code should prefer d.Write() over reaching d.DB for
// intent-clarity.
type DB struct {
	*sql.DB                 // write pool — single conn
	read           *sql.DB  // read pool — multi conn, mode=ro
	readPoolClosed bool     // set after Close() so accessors error cleanly
}

// Open creates / opens the SQLite database at path and runs migrations.
//
// Two pools are constructed against the same file:
//   - Write pool (the embedded *sql.DB): full RW DSN, MaxOpenConns=1.
//   - Read pool (d.Read()): mode=ro DSN, MaxOpenConns=8.
//
// The write pool is opened first so migrations can create the file;
// then the read pool opens against the now-existing file.
//
// Tests use a file under t.TempDir() — the same path works for both
// pools (mode=ro requires the file to exist, which migrations ensure).
func Open(path string) (*DB, error) {
	writeDSN := fmt.Sprintf(
		"file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)",
		path,
	)
	write, err := sql.Open("sqlite", writeDSN)
	if err != nil {
		return nil, fmt.Errorf("sqlite open (write): %w", err)
	}
	// Write pool: single connection, preserves the single-writer
	// invariant. Do NOT raise this above 1 without a deliberate audit
	// of every codebase pattern that assumes serial writes (autosync
	// idempotency, sync orchestrator, worker leases, read-before-tx
	// call sites). See plan 2026-05-16-001 Decision 2.
	write.SetMaxOpenConns(1)
	if err := write.PingContext(context.Background()); err != nil {
		_ = write.Close()
		return nil, fmt.Errorf("sqlite ping (write): %w", err)
	}

	d := &DB{DB: write}
	if err := d.migrate(context.Background()); err != nil {
		_ = write.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	// Read pool: opens against the now-migrated file with mode=ro.
	// mode=ro lets multiple connections proceed concurrently under
	// WAL without contending for the write lock, AND fails fast when
	// callers accidentally try to write through Read().
	readDSN := fmt.Sprintf(
		"file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&mode=ro",
		path,
	)
	read, err := sql.Open("sqlite", readDSN)
	if err != nil {
		_ = write.Close()
		return nil, fmt.Errorf("sqlite open (read): %w", err)
	}
	read.SetMaxOpenConns(8)
	if err := read.PingContext(context.Background()); err != nil {
		_ = read.Close()
		_ = write.Close()
		return nil, fmt.Errorf("sqlite ping (read): %w", err)
	}
	d.read = read
	return d, nil
}

// Write returns the single-connection write pool. New code should
// prefer this over reaching d.DB directly so the intent is explicit.
//
// Read-before-tx: code opening a write tx via d.Write().BeginTx (or
// the embedded d.BeginTx) MUST complete all reads it needs BEFORE
// the tx begins. The write conn is held by the tx; a fresh read
// inside the tx deadlocks. See package doc-comment for case studies.
func (d *DB) Write() *sql.DB { return d.DB }

// Read returns the multi-connection read pool (mode=ro). Use for
// concurrent-read paths where ms-staleness is acceptable: list/get
// HTTP endpoints, dashboard queries, audit log queries, inventory
// poller's per-file lookups, SSE subscription resolution.
//
// Do NOT use for read-your-write paths — those must use Write():
//   - pipeline heartbeat → recovery sweeper
//   - worker HeartbeatJob lease check
//   - autosync executor → planner sequence
//   - Stage 6 tx commit → audit worker
//
// Calling Read() after Close() returns nil. Calls against a nil
// *sql.DB panic — that's a programming error, not a runtime
// condition. Don't reach for Read() during shutdown.
func (d *DB) Read() *sql.DB { return d.read }

// Close shuts down both pools. Writes finish first (in-flight tx
// either commits or rolls back via *sql.DB.Close semantics), then
// reads close. Safe to call multiple times — second call is a no-op
// because both *sql.DB handles return ErrConnDone after the first
// close.
func (d *DB) Close() error {
	// Close write first so any in-flight write tx is allowed to finish
	// (or be rolled back) before the read pool tears down. Either order
	// is safe in practice — *sql.DB.Close() blocks until in-flight
	// statements drain — but write-first matches "writes are
	// authoritative, reads can be torn down freely" semantics.
	var firstErr error
	if d.DB != nil {
		if err := d.DB.Close(); err != nil {
			firstErr = fmt.Errorf("close write pool: %w", err)
		}
	}
	if d.read != nil && !d.readPoolClosed {
		if err := d.read.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("close read pool: %w", err)
		}
		d.readPoolClosed = true
	}
	return firstErr
}

func (d *DB) migrate(ctx context.Context) error {
	// Phase A — legacy inline migrations (Phase 0 schema: users, tenants, …).
	// Idempotent CREATE TABLE IF NOT EXISTS; safe to run on every startup.
	for i, stmt := range migrations {
		if _, err := d.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
	}
	// Phase B — versioned migrations from services/ds-service/migrations/.
	// Tracked in schema_migrations table; each file runs once.
	return d.applyVersionedMigrations(ctx)
}

var migrations = []string{
	`CREATE TABLE IF NOT EXISTS users (
		id              TEXT PRIMARY KEY,
		email           TEXT UNIQUE NOT NULL,
		password_hash   TEXT NOT NULL,
		role            TEXT NOT NULL DEFAULT 'user',  -- super_admin | user
		created_at      TEXT NOT NULL,
		last_login_at   TEXT
	)`,
	`CREATE TABLE IF NOT EXISTS tenants (
		id                TEXT PRIMARY KEY,
		slug              TEXT UNIQUE NOT NULL,
		name              TEXT NOT NULL,
		status            TEXT NOT NULL DEFAULT 'active',  -- active | suspended | archived
		plan_type         TEXT NOT NULL DEFAULT 'free',
		created_at        TEXT NOT NULL,
		created_by        TEXT NOT NULL REFERENCES users(id)
	)`,
	`CREATE TABLE IF NOT EXISTS tenant_users (
		tenant_id   TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
		user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		role        TEXT NOT NULL,  -- tenant_admin | designer | engineer | viewer
		status      TEXT NOT NULL DEFAULT 'active',
		created_at  TEXT NOT NULL,
		PRIMARY KEY (tenant_id, user_id)
	)`,
	`CREATE TABLE IF NOT EXISTS figma_tokens (
		tenant_id        TEXT PRIMARY KEY REFERENCES tenants(id) ON DELETE CASCADE,
		encrypted_token  BLOB NOT NULL,
		key_version      INTEGER NOT NULL DEFAULT 1,
		figma_user_email TEXT,
		figma_user_handle TEXT,
		last_validated_at TEXT,
		created_at       TEXT NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS audit_log (
		id          TEXT PRIMARY KEY,
		ts          TEXT NOT NULL,
		event_type  TEXT NOT NULL,
		tenant_id   TEXT,
		user_id     TEXT,
		method      TEXT,
		endpoint    TEXT,
		status_code INTEGER,
		duration_ms INTEGER,
		ip_address  TEXT,
		details     TEXT  -- JSON
	)`,
	`CREATE INDEX IF NOT EXISTS idx_audit_tenant_ts  ON audit_log(tenant_id, ts DESC)`,
	`CREATE INDEX IF NOT EXISTS idx_audit_user_ts    ON audit_log(user_id, ts DESC)`,
	`CREATE TABLE IF NOT EXISTS sync_state (
		tenant_id          TEXT PRIMARY KEY REFERENCES tenants(id) ON DELETE CASCADE,
		canonical_hash     TEXT NOT NULL,
		last_synced_at     TEXT NOT NULL,
		last_committed_sha TEXT,
		status             TEXT NOT NULL,  -- ok | skipped_nochange | failed
		failure_message    TEXT,
		modes              TEXT,  -- JSON array
		updated_at         TEXT NOT NULL
	)`,
}

// ─── User operations ─────────────────────────────────────────────────────────

type User struct {
	ID           string
	Email        string
	PasswordHash string
	Role         string
	CreatedAt    time.Time
	LastLoginAt  *time.Time
}

func (d *DB) CreateUser(ctx context.Context, u User) error {
	if u.ID == "" {
		return errors.New("user id is empty")
	}
	_, err := d.ExecContext(ctx,
		`INSERT INTO users (id, email, password_hash, role, created_at) VALUES (?, ?, ?, ?, ?)`,
		u.ID, u.Email, u.PasswordHash, u.Role, u.CreatedAt.UTC().Format(time.RFC3339),
	)
	return err
}

func (d *DB) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	row := d.QueryRowContext(ctx,
		`SELECT id, email, password_hash, role, created_at, last_login_at FROM users WHERE email = ?`,
		email,
	)
	u, err := scanUser(row)
	if err != nil {
		return nil, err
	}
	return u, nil
}

func (d *DB) GetUserByID(ctx context.Context, id string) (*User, error) {
	row := d.QueryRowContext(ctx,
		`SELECT id, email, password_hash, role, created_at, last_login_at FROM users WHERE id = ?`,
		id,
	)
	return scanUser(row)
}

func (d *DB) UpdateUserLastLogin(ctx context.Context, id string) error {
	_, err := d.ExecContext(ctx,
		`UPDATE users SET last_login_at = ? WHERE id = ?`,
		time.Now().UTC().Format(time.RFC3339), id,
	)
	return err
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanUser(row rowScanner) (*User, error) {
	var u User
	var createdAt string
	var lastLogin sql.NullString
	if err := row.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Role, &createdAt, &lastLogin); err != nil {
		return nil, err
	}
	t, _ := time.Parse(time.RFC3339, createdAt)
	u.CreatedAt = t
	if lastLogin.Valid {
		t2, _ := time.Parse(time.RFC3339, lastLogin.String)
		u.LastLoginAt = &t2
	}
	return &u, nil
}

// ─── Tenant operations ───────────────────────────────────────────────────────

type Tenant struct {
	ID        string
	Slug      string
	Name      string
	Status    string
	PlanType  string
	CreatedAt time.Time
	CreatedBy string
}

func (d *DB) CreateTenant(ctx context.Context, t Tenant) error {
	_, err := d.ExecContext(ctx,
		`INSERT INTO tenants (id, slug, name, status, plan_type, created_at, created_by) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Slug, t.Name, t.Status, t.PlanType, t.CreatedAt.UTC().Format(time.RFC3339), t.CreatedBy,
	)
	return err
}

func (d *DB) GetTenantBySlug(ctx context.Context, slug string) (*Tenant, error) {
	row := d.QueryRowContext(ctx,
		`SELECT id, slug, name, status, plan_type, created_at, created_by FROM tenants WHERE slug = ?`,
		slug,
	)
	var t Tenant
	var createdAt string
	if err := row.Scan(&t.ID, &t.Slug, &t.Name, &t.Status, &t.PlanType, &createdAt, &t.CreatedBy); err != nil {
		return nil, err
	}
	t.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	return &t, nil
}

// AddTenantUser grants a user a role on a tenant.
func (d *DB) AddTenantUser(ctx context.Context, tenantID, userID, role string) error {
	_, err := d.ExecContext(ctx,
		`INSERT OR REPLACE INTO tenant_users (tenant_id, user_id, role, status, created_at) VALUES (?, ?, ?, 'active', ?)`,
		tenantID, userID, role, time.Now().UTC().Format(time.RFC3339),
	)
	return err
}

// GetUserTenantIDs returns the active tenant_ids the user belongs to.
// Used by the login handler so the JWT carries real tenant UUIDs (which
// downstream FK constraints reference) rather than slugs. Pre-fix, login
// hardcoded `tenants: ["indmoney"]` and projects.send hit FOREIGN KEY
// errors trying to insert with tenant_id = "indmoney".
func (d *DB) GetUserTenantIDs(ctx context.Context, userID string) ([]string, error) {
	rows, err := d.QueryContext(ctx,
		`SELECT tenant_id FROM tenant_users WHERE user_id = ? AND status = 'active' ORDER BY created_at`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// GetTenantRole returns the user's role on the tenant, or "" if no membership.
func (d *DB) GetTenantRole(ctx context.Context, tenantID, userID string) (string, error) {
	var role string
	err := d.QueryRowContext(ctx,
		`SELECT role FROM tenant_users WHERE tenant_id = ? AND user_id = ? AND status = 'active'`,
		tenantID, userID,
	).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return role, err
}

// ─── Audit log ───────────────────────────────────────────────────────────────

type AuditEntry struct {
	ID         string    `json:"id"`
	TS         time.Time `json:"ts"`
	EventType  string    `json:"event_type"`
	TenantID   string    `json:"tenant_id"`
	UserID     string    `json:"user_id"`
	Method     string    `json:"method"`
	Endpoint   string    `json:"endpoint"`
	StatusCode int       `json:"status_code"`
	DurationMs int       `json:"duration_ms"`
	IPAddress  string    `json:"ip_address"`
	Details    string    `json:"details"`
}

func (d *DB) WriteAudit(ctx context.Context, e AuditEntry) error {
	_, err := d.ExecContext(ctx,
		`INSERT INTO audit_log (id, ts, event_type, tenant_id, user_id, method, endpoint, status_code, duration_ms, ip_address, details)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.ID, e.TS.UTC().Format(time.RFC3339Nano), e.EventType, e.TenantID, e.UserID,
		e.Method, e.Endpoint, e.StatusCode, e.DurationMs, e.IPAddress, e.Details,
	)
	return err
}

// QueryAudit returns recent audit entries for a tenant, newest first.
//
// Read-only: routes through d.Read() (concurrent reads OK, ms-staleness
// acceptable for an audit log view). Plan 2026-05-16-001 U3.
func (d *DB) QueryAudit(ctx context.Context, tenantID string, limit int) ([]AuditEntry, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := d.read.QueryContext(ctx,
		`SELECT id, ts, event_type, tenant_id, user_id, method, endpoint, status_code, duration_ms, ip_address, details
		 FROM audit_log WHERE tenant_id = ? ORDER BY ts DESC LIMIT ?`,
		tenantID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AuditEntry
	for rows.Next() {
		var e AuditEntry
		var ts string
		if err := rows.Scan(&e.ID, &ts, &e.EventType, &e.TenantID, &e.UserID,
			&e.Method, &e.Endpoint, &e.StatusCode, &e.DurationMs, &e.IPAddress, &e.Details); err != nil {
			return nil, err
		}
		e.TS, _ = time.Parse(time.RFC3339Nano, ts)
		out = append(out, e)
	}
	return out, rows.Err()
}

// ─── Figma tokens ────────────────────────────────────────────────────────────

type FigmaTokenRecord struct {
	TenantID         string
	EncryptedToken   []byte
	KeyVersion       int
	FigmaUserEmail   string
	FigmaUserHandle  string
	LastValidatedAt  *time.Time
	CreatedAt        time.Time
}

func (d *DB) UpsertFigmaToken(ctx context.Context, r FigmaTokenRecord) error {
	_, err := d.ExecContext(ctx,
		`INSERT INTO figma_tokens (tenant_id, encrypted_token, key_version, figma_user_email, figma_user_handle, last_validated_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(tenant_id) DO UPDATE SET
		   encrypted_token = excluded.encrypted_token,
		   key_version = excluded.key_version,
		   figma_user_email = excluded.figma_user_email,
		   figma_user_handle = excluded.figma_user_handle,
		   last_validated_at = excluded.last_validated_at`,
		r.TenantID, r.EncryptedToken, r.KeyVersion, r.FigmaUserEmail, r.FigmaUserHandle,
		nullableTime(r.LastValidatedAt), r.CreatedAt.UTC().Format(time.RFC3339),
	)
	return err
}

// GetFigmaToken returns the encrypted Figma PAT for a tenant.
//
// Read-only: routes through d.Read(). The login + Figma proxy flows
// read this on every request; concurrent reads under WAL are the
// parallelism win. Plan 2026-05-16-001 U3.
func (d *DB) GetFigmaToken(ctx context.Context, tenantID string) (*FigmaTokenRecord, error) {
	row := d.read.QueryRowContext(ctx,
		`SELECT tenant_id, encrypted_token, key_version, figma_user_email, figma_user_handle, last_validated_at, created_at
		 FROM figma_tokens WHERE tenant_id = ?`,
		tenantID,
	)
	var r FigmaTokenRecord
	var lastValidated sql.NullString
	var createdAt string
	if err := row.Scan(&r.TenantID, &r.EncryptedToken, &r.KeyVersion, &r.FigmaUserEmail,
		&r.FigmaUserHandle, &lastValidated, &createdAt); err != nil {
		return nil, err
	}
	r.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	if lastValidated.Valid {
		t, _ := time.Parse(time.RFC3339, lastValidated.String)
		r.LastValidatedAt = &t
	}
	return &r, nil
}

// ─── Sync state ──────────────────────────────────────────────────────────────

type SyncStateRecord struct {
	TenantID         string
	CanonicalHash    string
	LastSyncedAt     time.Time
	LastCommittedSha string
	Status           string // ok | skipped_nochange | failed
	FailureMessage   string
	Modes            string // JSON array
	UpdatedAt        time.Time
}

func (d *DB) UpsertSyncState(ctx context.Context, s SyncStateRecord) error {
	_, err := d.ExecContext(ctx,
		`INSERT INTO sync_state (tenant_id, canonical_hash, last_synced_at, last_committed_sha, status, failure_message, modes, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(tenant_id) DO UPDATE SET
		   canonical_hash = excluded.canonical_hash,
		   last_synced_at = excluded.last_synced_at,
		   last_committed_sha = excluded.last_committed_sha,
		   status = excluded.status,
		   failure_message = excluded.failure_message,
		   modes = excluded.modes,
		   updated_at = excluded.updated_at`,
		s.TenantID, s.CanonicalHash, s.LastSyncedAt.UTC().Format(time.RFC3339),
		s.LastCommittedSha, s.Status, s.FailureMessage, s.Modes,
		s.UpdatedAt.UTC().Format(time.RFC3339),
	)
	return err
}

// GetSyncState returns the per-tenant sync metadata.
//
// Read-only: routes through d.Read(). Plan 2026-05-16-001 U3.
func (d *DB) GetSyncState(ctx context.Context, tenantID string) (*SyncStateRecord, error) {
	row := d.read.QueryRowContext(ctx,
		`SELECT tenant_id, canonical_hash, last_synced_at, last_committed_sha, status, failure_message, modes, updated_at
		 FROM sync_state WHERE tenant_id = ?`,
		tenantID,
	)
	var s SyncStateRecord
	var lastSync, updatedAt string
	if err := row.Scan(&s.TenantID, &s.CanonicalHash, &lastSync, &s.LastCommittedSha,
		&s.Status, &s.FailureMessage, &s.Modes, &updatedAt); err != nil {
		return nil, err
	}
	s.LastSyncedAt, _ = time.Parse(time.RFC3339, lastSync)
	s.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return &s, nil
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func nullableTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339)
}
