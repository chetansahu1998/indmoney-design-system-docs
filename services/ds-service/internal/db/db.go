// Package db provides the SQLite layer for ds-service.
//
// Single-file local DB at services/ds-service/data/ds.db. Schema migrations
// are bundled at compile time and run on every startup (idempotent).
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

// DB wraps a *sql.DB with a few app-specific helpers.
type DB struct {
	*sql.DB
}

// Open creates / opens the SQLite database at path and runs migrations.
// Use ":memory:" for tests.
func Open(path string) (*DB, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)", path)
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite open: %w", err)
	}
	conn.SetMaxOpenConns(1) // SQLite single-writer; WAL allows concurrent readers
	if err := conn.PingContext(context.Background()); err != nil {
		return nil, fmt.Errorf("sqlite ping: %w", err)
	}
	d := &DB{conn}
	if err := d.migrate(context.Background()); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return d, nil
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
func (d *DB) QueryAudit(ctx context.Context, tenantID string, limit int) ([]AuditEntry, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := d.QueryContext(ctx,
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

func (d *DB) GetFigmaToken(ctx context.Context, tenantID string) (*FigmaTokenRecord, error) {
	row := d.QueryRowContext(ctx,
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

func (d *DB) GetSyncState(ctx context.Context, tenantID string) (*SyncStateRecord, error) {
	row := d.QueryRowContext(ctx,
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
