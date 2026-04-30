package projects

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ErrNotFound is returned by TenantRepo when a row exists for another tenant
// but isn't visible to the caller, AND when a row truly doesn't exist. The
// HTTP layer translates this to 404 in both cases (no existence oracle).
var ErrNotFound = errors.New("projects: not found")

// repo is the unexported plain handle. Every public method must go through
// TenantRepo so the tenant_id filter is impossible to forget at the call site.
type repo struct {
	db *sql.DB
}

// TenantRepo is the only public way to read or write project rows. It carries
// a tenant_id captured from auth.Claims and injects it into every query.
type TenantRepo struct {
	r        *repo
	tenantID string
	now      func() time.Time // injectable for tests
}

// NewTenantRepo builds a tenant-scoped repository. Pass *sql.DB (not *db.DB) so
// tests can substitute a bare connection without going through migrations.
func NewTenantRepo(db *sql.DB, tenantID string) *TenantRepo {
	return &TenantRepo{r: &repo{db: db}, tenantID: tenantID, now: time.Now}
}

// withNow returns a copy with the clock overridden — only tests reach for this.
func (t *TenantRepo) withNow(now func() time.Time) *TenantRepo {
	cp := *t
	cp.now = now
	return &cp
}

// rfc3339 formats a time the same way the Phase 0 db package does.
func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// parseTime is the inverse — accepts RFC3339Nano AND RFC3339 (mixed across
// existing rows). Returns zero on parse failure, matching db.scanUser's
// best-effort pattern.
func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t
	}
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

// ─── Projects ────────────────────────────────────────────────────────────────

// UpsertProject resolves an existing project by (tenant, product, platform, path)
// and returns its ID + slug, or creates one if absent. Uses a prepared statement;
// no string interpolation of user input.
func (t *TenantRepo) UpsertProject(ctx context.Context, p Project) (Project, error) {
	if t.tenantID == "" {
		return Project{}, errors.New("projects: tenant_id required")
	}
	// Look up existing first.
	row := t.r.db.QueryRowContext(ctx,
		`SELECT id, slug, name, platform, product, path, owner_user_id, created_at, updated_at
		   FROM projects
		  WHERE tenant_id = ? AND product = ? AND platform = ? AND path = ? AND deleted_at IS NULL`,
		t.tenantID, p.Product, p.Platform, p.Path,
	)
	var existing Project
	var createdAt, updatedAt string
	err := row.Scan(&existing.ID, &existing.Slug, &existing.Name, &existing.Platform,
		&existing.Product, &existing.Path, &existing.OwnerUserID, &createdAt, &updatedAt)
	if err == nil {
		existing.TenantID = t.tenantID
		existing.CreatedAt = parseTime(createdAt)
		existing.UpdatedAt = parseTime(updatedAt)
		// Bump updated_at + name (designers may rename)
		now := t.now().UTC()
		_, err := t.r.db.ExecContext(ctx,
			`UPDATE projects SET name = ?, updated_at = ? WHERE id = ? AND tenant_id = ?`,
			p.Name, rfc3339(now), existing.ID, t.tenantID)
		if err != nil {
			return Project{}, fmt.Errorf("update project: %w", err)
		}
		existing.Name = p.Name
		existing.UpdatedAt = now
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Project{}, fmt.Errorf("lookup project: %w", err)
	}

	// Create new.
	if p.ID == "" {
		p.ID = uuid.NewString()
	}
	if p.Slug == "" {
		p.Slug = makeSlug(p.Product, p.Path)
	}
	now := t.now().UTC()
	p.CreatedAt = now
	p.UpdatedAt = now
	p.TenantID = t.tenantID
	_, err = t.r.db.ExecContext(ctx,
		`INSERT INTO projects (id, slug, name, platform, product, path, owner_user_id, tenant_id, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Slug, p.Name, p.Platform, p.Product, p.Path, p.OwnerUserID, t.tenantID,
		rfc3339(now), rfc3339(now),
	)
	if err != nil {
		// Slug collisions can race two concurrent first-imports of the same
		// (product, path); on conflict, re-read.
		if strings.Contains(err.Error(), "UNIQUE") {
			return t.UpsertProject(ctx, p)
		}
		return Project{}, fmt.Errorf("insert project: %w", err)
	}
	return p, nil
}

// CreateVersion inserts a fresh version row with version_index = max+1. status is
// always "pending" at creation; the pipeline transitions it.
func (t *TenantRepo) CreateVersion(ctx context.Context, projectID, createdByUserID string) (ProjectVersion, error) {
	if t.tenantID == "" {
		return ProjectVersion{}, errors.New("projects: tenant_id required")
	}
	tx, err := t.r.db.BeginTx(ctx, nil)
	if err != nil {
		return ProjectVersion{}, err
	}
	defer tx.Rollback()

	// Compute next version_index under the same transaction so concurrent
	// inserts don't collide on UNIQUE(project_id, version_index).
	var maxIdx sql.NullInt64
	err = tx.QueryRowContext(ctx,
		`SELECT MAX(version_index) FROM project_versions WHERE project_id = ? AND tenant_id = ?`,
		projectID, t.tenantID,
	).Scan(&maxIdx)
	if err != nil {
		return ProjectVersion{}, fmt.Errorf("max version: %w", err)
	}
	next := 1
	if maxIdx.Valid {
		next = int(maxIdx.Int64) + 1
	}
	v := ProjectVersion{
		ID:                  uuid.NewString(),
		ProjectID:           projectID,
		TenantID:            t.tenantID,
		VersionIndex:        next,
		Status:              "pending",
		CreatedByUserID:     createdByUserID,
	}
	now := t.now().UTC()
	v.CreatedAt = now
	v.PipelineStartedAt = &now
	v.PipelineHeartbeatAt = &now
	_, err = tx.ExecContext(ctx,
		`INSERT INTO project_versions (id, project_id, tenant_id, version_index, status, pipeline_started_at, pipeline_heartbeat_at, created_by_user_id, created_at)
		 VALUES (?, ?, ?, ?, 'pending', ?, ?, ?, ?)`,
		v.ID, v.ProjectID, t.tenantID, v.VersionIndex,
		rfc3339(now), rfc3339(now), v.CreatedByUserID, rfc3339(now),
	)
	if err != nil {
		return ProjectVersion{}, fmt.Errorf("insert version: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return ProjectVersion{}, err
	}
	return v, nil
}

// UpsertFlow creates a flow if one doesn't exist for (tenant, file_id, section_id, persona_id),
// or returns the existing row's ID. The unique constraint in the schema is
// partial (deleted_at IS NULL) so soft-deleted flows don't block re-export.
//
// Implementation note: SQLite's ON CONFLICT clause requires the conflict target
// to match an actual UNIQUE index. The schema's idx_flows_unique is partial
// (WHERE deleted_at IS NULL), so we read-then-insert rather than relying on
// ON CONFLICT. Two concurrent inserts can race; the second one's INSERT will
// fail the UNIQUE check and we re-read.
func (t *TenantRepo) UpsertFlow(ctx context.Context, f Flow) (Flow, error) {
	if t.tenantID == "" {
		return Flow{}, errors.New("projects: tenant_id required")
	}
	// Read first.
	q := `SELECT id, project_id, file_id, section_id, name, persona_id, created_at, updated_at
	        FROM flows
	       WHERE tenant_id = ? AND file_id = ? AND deleted_at IS NULL`
	args := []any{t.tenantID, f.FileID}
	if f.SectionID == nil {
		q += ` AND section_id IS NULL`
	} else {
		q += ` AND section_id = ?`
		args = append(args, *f.SectionID)
	}
	if f.PersonaID == nil {
		q += ` AND persona_id IS NULL`
	} else {
		q += ` AND persona_id = ?`
		args = append(args, *f.PersonaID)
	}
	row := t.r.db.QueryRowContext(ctx, q, args...)
	var existing Flow
	var sectionID, personaID sql.NullString
	var createdAt, updatedAt string
	err := row.Scan(&existing.ID, &existing.ProjectID, &existing.FileID, &sectionID,
		&existing.Name, &personaID, &createdAt, &updatedAt)
	if err == nil {
		existing.TenantID = t.tenantID
		if sectionID.Valid {
			s := sectionID.String
			existing.SectionID = &s
		}
		if personaID.Valid {
			p := personaID.String
			existing.PersonaID = &p
		}
		existing.CreatedAt = parseTime(createdAt)
		existing.UpdatedAt = parseTime(updatedAt)
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Flow{}, fmt.Errorf("lookup flow: %w", err)
	}

	// Insert.
	if f.ID == "" {
		f.ID = uuid.NewString()
	}
	now := t.now().UTC()
	f.CreatedAt = now
	f.UpdatedAt = now
	f.TenantID = t.tenantID
	_, err = t.r.db.ExecContext(ctx,
		`INSERT INTO flows (id, project_id, tenant_id, file_id, section_id, name, persona_id, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		f.ID, f.ProjectID, t.tenantID, f.FileID, nullString(f.SectionID), f.Name,
		nullString(f.PersonaID), rfc3339(now), rfc3339(now),
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			// Concurrent insert won; re-read.
			return t.UpsertFlow(ctx, f)
		}
		return Flow{}, fmt.Errorf("insert flow: %w", err)
	}
	return f, nil
}

// InsertScreens persists a batch of empty screen rows (no canonical_tree, no
// png_storage_key yet). Pipeline will fill in PNG keys after render.
func (t *TenantRepo) InsertScreens(ctx context.Context, screens []Screen) error {
	if t.tenantID == "" {
		return errors.New("projects: tenant_id required")
	}
	if len(screens) == 0 {
		return nil
	}
	tx, err := t.r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO screens (id, version_id, flow_id, tenant_id, x, y, width, height, screen_logical_id, png_storage_key, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	now := t.now().UTC()
	for i := range screens {
		s := &screens[i]
		if s.ID == "" {
			s.ID = uuid.NewString()
		}
		if s.ScreenLogicalID == "" {
			s.ScreenLogicalID = uuid.NewString()
		}
		s.TenantID = t.tenantID
		s.CreatedAt = now
		_, err := stmt.ExecContext(ctx,
			s.ID, s.VersionID, s.FlowID, t.tenantID,
			s.X, s.Y, s.Width, s.Height, s.ScreenLogicalID,
			nullString(s.PNGStorageKey), rfc3339(now),
		)
		if err != nil {
			return fmt.Errorf("insert screen %s: %w", s.ID, err)
		}
	}
	return tx.Commit()
}

// UpsertPersona finds an approved persona by name in the tenant, or creates a
// pending one if absent. Race-safe: ON CONFLICT clause on the partial unique
// index resolves concurrent first-suggests.
func (t *TenantRepo) UpsertPersona(ctx context.Context, name, createdByUserID string) (Persona, error) {
	if t.tenantID == "" {
		return Persona{}, errors.New("projects: tenant_id required")
	}
	// Try approved first.
	row := t.r.db.QueryRowContext(ctx,
		`SELECT id, name, status, created_by_user_id, created_at
		   FROM personas
		  WHERE tenant_id = ? AND name = ? AND status = 'approved' AND deleted_at IS NULL`,
		t.tenantID, name,
	)
	var p Persona
	var createdAt string
	if err := row.Scan(&p.ID, &p.Name, &p.Status, &p.CreatedByUserID, &createdAt); err == nil {
		p.TenantID = t.tenantID
		p.CreatedAt = parseTime(createdAt)
		return p, nil
	}

	// Try pending — return existing pending row if the same designer suggested.
	row = t.r.db.QueryRowContext(ctx,
		`SELECT id, name, status, created_by_user_id, created_at
		   FROM personas
		  WHERE tenant_id = ? AND name = ? AND status = 'pending' AND deleted_at IS NULL AND created_by_user_id = ?
		  LIMIT 1`,
		t.tenantID, name, createdByUserID,
	)
	if err := row.Scan(&p.ID, &p.Name, &p.Status, &p.CreatedByUserID, &createdAt); err == nil {
		p.TenantID = t.tenantID
		p.CreatedAt = parseTime(createdAt)
		return p, nil
	}

	// Insert new pending; race-safe via partial unique index — if another
	// approved row appears between our SELECT and INSERT, the INSERT will
	// fail UNIQUE and we re-read.
	id := uuid.NewString()
	now := t.now().UTC()
	_, err := t.r.db.ExecContext(ctx,
		`INSERT INTO personas (id, tenant_id, name, status, created_by_user_id, created_at)
		 VALUES (?, ?, ?, 'pending', ?, ?)
		 ON CONFLICT(tenant_id, name) WHERE status = 'approved' AND deleted_at IS NULL DO NOTHING`,
		id, t.tenantID, name, createdByUserID, rfc3339(now),
	)
	if err != nil {
		// SQLite versions without ON CONFLICT clause-with-where support fall back
		// to plain UNIQUE error; recover by re-reading.
		if strings.Contains(err.Error(), "UNIQUE") {
			return t.UpsertPersona(ctx, name, createdByUserID)
		}
		return Persona{}, fmt.Errorf("insert persona: %w", err)
	}
	// Re-read to get the row that won (might be ours or a concurrent approved one).
	return t.UpsertPersona(ctx, name, createdByUserID)
}

// GetProjectBySlug returns the project for the given slug within the caller's
// tenant. ErrNotFound for cross-tenant or absent slugs — no existence oracle.
func (t *TenantRepo) GetProjectBySlug(ctx context.Context, slug string) (Project, error) {
	if t.tenantID == "" {
		return Project{}, errors.New("projects: tenant_id required")
	}
	row := t.r.db.QueryRowContext(ctx,
		`SELECT id, slug, name, platform, product, path, owner_user_id, created_at, updated_at
		   FROM projects
		  WHERE tenant_id = ? AND slug = ? AND deleted_at IS NULL`,
		t.tenantID, slug,
	)
	var p Project
	var createdAt, updatedAt string
	err := row.Scan(&p.ID, &p.Slug, &p.Name, &p.Platform, &p.Product, &p.Path,
		&p.OwnerUserID, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Project{}, ErrNotFound
	}
	if err != nil {
		return Project{}, err
	}
	p.TenantID = t.tenantID
	p.CreatedAt = parseTime(createdAt)
	p.UpdatedAt = parseTime(updatedAt)
	return p, nil
}

// GetVersion returns a specific version. Cross-tenant returns ErrNotFound.
func (t *TenantRepo) GetVersion(ctx context.Context, versionID string) (ProjectVersion, error) {
	if t.tenantID == "" {
		return ProjectVersion{}, errors.New("projects: tenant_id required")
	}
	row := t.r.db.QueryRowContext(ctx,
		`SELECT id, project_id, version_index, status, pipeline_started_at, pipeline_heartbeat_at,
		        error, created_by_user_id, created_at
		   FROM project_versions
		  WHERE id = ? AND tenant_id = ?`,
		versionID, t.tenantID,
	)
	var v ProjectVersion
	var startedAt, heartbeatAt sql.NullString
	var errStr sql.NullString
	var createdAt string
	err := row.Scan(&v.ID, &v.ProjectID, &v.VersionIndex, &v.Status,
		&startedAt, &heartbeatAt, &errStr, &v.CreatedByUserID, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ProjectVersion{}, ErrNotFound
	}
	if err != nil {
		return ProjectVersion{}, err
	}
	v.TenantID = t.tenantID
	v.CreatedAt = parseTime(createdAt)
	if startedAt.Valid {
		t := parseTime(startedAt.String)
		v.PipelineStartedAt = &t
	}
	if heartbeatAt.Valid {
		t := parseTime(heartbeatAt.String)
		v.PipelineHeartbeatAt = &t
	}
	if errStr.Valid {
		v.Error = errStr.String
	}
	return v, nil
}

// ListProjects returns active projects for the tenant ordered by updated_at DESC.
func (t *TenantRepo) ListProjects(ctx context.Context, limit int) ([]Project, error) {
	if t.tenantID == "" {
		return nil, errors.New("projects: tenant_id required")
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := t.r.db.QueryContext(ctx,
		`SELECT id, slug, name, platform, product, path, owner_user_id, created_at, updated_at
		   FROM projects
		  WHERE tenant_id = ? AND deleted_at IS NULL
		  ORDER BY updated_at DESC
		  LIMIT ?`,
		t.tenantID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Project
	for rows.Next() {
		var p Project
		var createdAt, updatedAt string
		if err := rows.Scan(&p.ID, &p.Slug, &p.Name, &p.Platform, &p.Product, &p.Path,
			&p.OwnerUserID, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		p.TenantID = t.tenantID
		p.CreatedAt = parseTime(createdAt)
		p.UpdatedAt = parseTime(updatedAt)
		out = append(out, p)
	}
	return out, rows.Err()
}

// RecordViewReady transitions a version to view_ready and clears heartbeat.
// Called from inside a pipeline transaction.
func (t *TenantRepo) RecordViewReady(ctx context.Context, tx *sql.Tx, versionID string) error {
	if t.tenantID == "" {
		return errors.New("projects: tenant_id required")
	}
	res, err := tx.ExecContext(ctx,
		`UPDATE project_versions
		    SET status = 'view_ready', pipeline_heartbeat_at = NULL, error = NULL
		  WHERE id = ? AND tenant_id = ?`,
		versionID, t.tenantID,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// RecordFailed marks a version failed with an error message and clears heartbeat.
// Used by both the pipeline (mid-run failure) and the recovery sweeper.
func (t *TenantRepo) RecordFailed(ctx context.Context, versionID, errMsg string) error {
	if t.tenantID == "" {
		return errors.New("projects: tenant_id required")
	}
	if len(errMsg) > 8000 {
		errMsg = errMsg[:8000]
	}
	res, err := t.r.db.ExecContext(ctx,
		`UPDATE project_versions
		    SET status = 'failed', pipeline_heartbeat_at = NULL, error = ?
		  WHERE id = ? AND tenant_id = ?`,
		errMsg, versionID, t.tenantID,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// HeartbeatVersion refreshes the pipeline_heartbeat_at column. Called by the
// heartbeat goroutine inside RunFastPreview every 5s.
func (t *TenantRepo) HeartbeatVersion(ctx context.Context, versionID string) error {
	if t.tenantID == "" {
		return errors.New("projects: tenant_id required")
	}
	_, err := t.r.db.ExecContext(ctx,
		`UPDATE project_versions SET pipeline_heartbeat_at = ? WHERE id = ? AND tenant_id = ?`,
		rfc3339(t.now().UTC()), versionID, t.tenantID,
	)
	return err
}

// GetCanonicalTree looks up the lazy canonical_tree blob for a screen, joined
// to its project so the tenant_id check applies. Returns ErrNotFound when the
// screen doesn't exist OR the project's tenant_id doesn't match the caller's
// claim — same 404 either way (no existence oracle).
//
// Used by the U8 JSON tab via GET /v1/projects/:slug/screens/:id/canonical-tree.
type CanonicalTreeResult struct {
	ScreenID string
	Tree     string // raw JSON; caller passes through unparsed
	Hash     string
}

func (t *TenantRepo) GetCanonicalTree(ctx context.Context, projectSlug, screenID string) (*CanonicalTreeResult, error) {
	if t.tenantID == "" {
		return nil, errors.New("projects: tenant_id required")
	}
	var res CanonicalTreeResult
	var hash sql.NullString
	err := t.r.db.QueryRowContext(ctx,
		`SELECT s.id, sct.canonical_tree, sct.hash
		 FROM screens s
		 JOIN project_versions v ON v.id = s.version_id
		 JOIN projects p ON p.id = v.project_id
		 JOIN screen_canonical_trees sct ON sct.screen_id = s.id
		 WHERE p.slug = ? AND p.tenant_id = ? AND p.deleted_at IS NULL AND s.id = ?`,
		projectSlug, t.tenantID, screenID,
	).Scan(&res.ScreenID, &res.Tree, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if hash.Valid {
		res.Hash = hash.String
	}
	return &res, nil
}

// GetScreenForServe looks up a screen joined to its project + version for the
// authed PNG route handler (U11). Returns ErrNotFound when the screen doesn't
// exist OR the project's tenant_id doesn't match the caller's claim — same
// 404 response either way (no existence oracle).
//
// Result fields are intentionally minimal — just what the handler needs to
// serve the file: the storage key, the version_id (for path construction),
// and the screen's own ID (echoed for log lines).
type ScreenServeInfo struct {
	ScreenID      string
	VersionID     string
	PngStorageKey string
}

func (t *TenantRepo) GetScreenForServe(ctx context.Context, projectSlug, screenID string) (*ScreenServeInfo, error) {
	if t.tenantID == "" {
		return nil, errors.New("projects: tenant_id required")
	}
	var info ScreenServeInfo
	var pngKey sql.NullString
	err := t.r.db.QueryRowContext(ctx,
		`SELECT s.id, s.version_id, s.png_storage_key
		 FROM screens s
		 JOIN project_versions v ON v.id = s.version_id
		 JOIN projects p ON p.id = v.project_id
		 WHERE p.slug = ? AND p.tenant_id = ? AND p.deleted_at IS NULL AND s.id = ?`,
		projectSlug, t.tenantID, screenID,
	).Scan(&info.ScreenID, &info.VersionID, &pngKey)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if pngKey.Valid {
		info.PngStorageKey = pngKey.String
	}
	return &info, nil
}

// SetScreenPNG records the persisted PNG storage key for a screen.
func (t *TenantRepo) SetScreenPNG(ctx context.Context, screenID, storageKey string) error {
	if t.tenantID == "" {
		return errors.New("projects: tenant_id required")
	}
	_, err := t.r.db.ExecContext(ctx,
		`UPDATE screens SET png_storage_key = ? WHERE id = ? AND tenant_id = ?`,
		storageKey, screenID, t.tenantID,
	)
	return err
}

// InsertScreenModes is called inside the pipeline transaction; takes a *sql.Tx.
func (t *TenantRepo) InsertScreenModes(ctx context.Context, tx *sql.Tx, modes []ScreenMode) error {
	if t.tenantID == "" {
		return errors.New("projects: tenant_id required")
	}
	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO screen_modes (id, screen_id, tenant_id, mode_label, figma_frame_id, explicit_variable_modes_json)
		 VALUES (?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for i := range modes {
		m := &modes[i]
		if m.ID == "" {
			m.ID = uuid.NewString()
		}
		m.TenantID = t.tenantID
		_, err := stmt.ExecContext(ctx, m.ID, m.ScreenID, t.tenantID, m.ModeLabel,
			m.FigmaFrameID, m.ExplicitVariableModesJSON)
		if err != nil {
			return fmt.Errorf("insert screen_mode: %w", err)
		}
	}
	return nil
}

// InsertCanonicalTree is called inside the pipeline transaction; takes a *sql.Tx.
func (t *TenantRepo) InsertCanonicalTree(ctx context.Context, tx *sql.Tx, screenID, tree, hash string) error {
	if t.tenantID == "" {
		return errors.New("projects: tenant_id required")
	}
	now := t.now().UTC()
	_, err := tx.ExecContext(ctx,
		`INSERT INTO screen_canonical_trees (screen_id, canonical_tree, hash, updated_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(screen_id) DO UPDATE SET canonical_tree = excluded.canonical_tree, hash = excluded.hash, updated_at = excluded.updated_at`,
		screenID, tree, hash, rfc3339(now),
	)
	return err
}

// EnqueueAuditJob is called inside the pipeline transaction; takes a *sql.Tx.
func (t *TenantRepo) EnqueueAuditJob(ctx context.Context, tx *sql.Tx, versionID, traceID, idempotencyKey string) (string, error) {
	if t.tenantID == "" {
		return "", errors.New("projects: tenant_id required")
	}
	id := uuid.NewString()
	now := t.now().UTC()
	_, err := tx.ExecContext(ctx,
		`INSERT INTO audit_jobs (id, version_id, tenant_id, status, trace_id, idempotency_key, created_at)
		 VALUES (?, ?, ?, 'queued', ?, ?, ?)`,
		id, versionID, t.tenantID, traceID, idempotencyKey, rfc3339(now),
	)
	if err != nil {
		return "", fmt.Errorf("insert audit_job: %w", err)
	}
	return id, nil
}

// BeginTx exposes a transaction handle for callers (pipeline) that need to
// stitch multiple repository writes together atomically.
func (t *TenantRepo) BeginTx(ctx context.Context) (*sql.Tx, error) {
	return t.r.db.BeginTx(ctx, nil)
}

// DB exposes the underlying *sql.DB. Used only by the recovery sweeper which
// works tenant-agnostically and the pipeline's heartbeat goroutine.
func (t *TenantRepo) DB() *sql.DB { return t.r.db }

// ─── Helpers ────────────────────────────────────────────────────────────────

func nullString(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

// makeSlug produces a deterministic slug from product + path. Lowercase, ascii,
// hyphenated. Mirrors the audit/server.go slugifyForFileKey approach so all
// tenant-scoped slug behavior is consistent across the codebase.
func makeSlug(product, path string) string {
	src := product + "-" + path
	var b []rune
	prevDash := false
	for _, r := range src {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b = append(b, r)
			prevDash = false
		case r >= 'A' && r <= 'Z':
			b = append(b, r+('a'-'A'))
			prevDash = false
		default:
			if !prevDash && len(b) > 0 {
				b = append(b, '-')
				prevDash = true
			}
		}
	}
	out := string(b)
	for len(out) > 0 && out[len(out)-1] == '-' {
		out = out[:len(out)-1]
	}
	if out == "" {
		out = uuid.NewString()
	}
	if len(out) > 80 {
		out = out[:80]
	}
	return out
}
