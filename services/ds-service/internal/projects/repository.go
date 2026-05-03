package projects

import (
	"context"
	"database/sql"
	"encoding/json"
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
		if strings.Contains(err.Error(), "UNIQUE") {
			// Slug collision causes:
			//   (a) Race: two concurrent first-imports of the same
			//       (tenant, product, platform, path) — the other goroutine
			//       won; re-read by that 4-tuple and return the existing row.
			//   (b) Cross-platform collision: a row already exists for the
			//       same (tenant, product, path) on a *different* platform.
			//       The lookup-by-platform misses it, but the slug index is
			//       (tenant_id, slug) — so we hit UNIQUE. Regenerate the
			//       slug with a platform suffix and retry ONCE.
			//
			// Pre-Phase 6 this path recursed on the public method, which
			// could loop forever for case (b). The fix below is bounded:
			// at most one retry, and only for case (b).
			row := t.r.db.QueryRowContext(ctx,
				`SELECT id, slug, name, platform, product, path, owner_user_id, created_at, updated_at
				   FROM projects
				  WHERE tenant_id = ? AND product = ? AND platform = ? AND path = ? AND deleted_at IS NULL`,
				t.tenantID, p.Product, p.Platform, p.Path,
			)
			var existing Project
			var createdAt, updatedAt string
			scanErr := row.Scan(&existing.ID, &existing.Slug, &existing.Name, &existing.Platform,
				&existing.Product, &existing.Path, &existing.OwnerUserID, &createdAt, &updatedAt)
			if scanErr == nil {
				// Case (a): concurrent insert won — return existing row.
				existing.TenantID = t.tenantID
				existing.CreatedAt = parseTime(createdAt)
				existing.UpdatedAt = parseTime(updatedAt)
				return existing, nil
			}
			if !errors.Is(scanErr, sql.ErrNoRows) {
				return Project{}, fmt.Errorf("post-collision lookup: %w", scanErr)
			}
			// Case (b): same slug but different platform. Append the
			// platform to disambiguate, regenerate IDs, retry ONCE.
			p.Slug = makeSlug(p.Product, p.Path) + "-" + strings.ToLower(p.Platform)
			p.ID = uuid.NewString()
			retryErr := t.r.db.QueryRowContext(ctx,
				`INSERT INTO projects (id, slug, name, platform, product, path, owner_user_id, tenant_id, created_at, updated_at)
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
				 RETURNING id`,
				p.ID, p.Slug, p.Name, p.Platform, p.Product, p.Path, p.OwnerUserID, t.tenantID,
				rfc3339(now), rfc3339(now),
			).Scan(&p.ID)
			if retryErr != nil {
				return Project{}, fmt.Errorf("insert project (cross-platform retry): %w", retryErr)
			}
			return p, nil
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
		// If the designer renamed the Figma section between exports, the
		// (file_id, section_id, persona_id) tuple still matches this row,
		// but `f.Name` will differ from `existing.Name`. UPDATE so the UI
		// (project shell flow selector, atlas labels) reflects the rename.
		// Skipping this would silently freeze the original name forever.
		if f.Name != "" && f.Name != existing.Name {
			now := t.now().UTC()
			if _, uerr := t.r.db.ExecContext(ctx,
				`UPDATE flows SET name = ?, updated_at = ? WHERE id = ? AND tenant_id = ?`,
				f.Name, rfc3339(now), existing.ID, t.tenantID,
			); uerr != nil {
				return Flow{}, fmt.Errorf("rename flow: %w", uerr)
			}
			existing.Name = f.Name
			existing.UpdatedAt = now
		}
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

// ListFlowsByProject returns all active (non-deleted) flows for a project,
// scoped to the caller's tenant. Used by the project payload assembler so
// the frontend can render a flow selector instead of hardcoding screens[0].
func (t *TenantRepo) ListFlowsByProject(ctx context.Context, projectID string) ([]Flow, error) {
	if t.tenantID == "" {
		return nil, errors.New("projects: tenant_id required")
	}
	rows, err := t.r.db.QueryContext(ctx,
		`SELECT id, project_id, file_id, section_id, name, persona_id, created_at, updated_at
		   FROM flows
		  WHERE project_id = ? AND tenant_id = ? AND deleted_at IS NULL
		  ORDER BY created_at ASC`,
		projectID, t.tenantID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Flow
	for rows.Next() {
		var f Flow
		var sectionID, personaID sql.NullString
		var createdAt, updatedAt string
		if err := rows.Scan(&f.ID, &f.ProjectID, &f.FileID, &sectionID, &f.Name,
			&personaID, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		f.TenantID = t.tenantID
		if sectionID.Valid {
			s := sectionID.String
			f.SectionID = &s
		}
		if personaID.Valid {
			p := personaID.String
			f.PersonaID = &p
		}
		f.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		f.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		out = append(out, f)
	}
	return out, rows.Err()
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
//
// Wraps UpsertPersonaTracked, discarding the wasNew signal. Existing
// callers that don't care about the difference between "found" and
// "newly suggested" use this overload.
func (t *TenantRepo) UpsertPersona(ctx context.Context, name, createdByUserID string) (Persona, error) {
	p, _, err := t.UpsertPersonaTracked(ctx, name, createdByUserID)
	return p, err
}

// UpsertPersonaTracked is the same upsert but returns wasNew=true when
// the call resulted in a fresh pending row (vs. returning an existing
// approved/pending row). Phase 7.6 admin bell uses wasNew to fire a
// `persona.pending` SSE event only when there's actually new work for
// the DS lead.
func (t *TenantRepo) UpsertPersonaTracked(ctx context.Context, name, createdByUserID string) (Persona, bool, error) {
	if t.tenantID == "" {
		return Persona{}, false, errors.New("projects: tenant_id required")
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
		return p, false, nil
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
		return p, false, nil
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
			pp, _, rerr := t.UpsertPersonaTracked(ctx, name, createdByUserID)
			return pp, false, rerr
		}
		return Persona{}, false, fmt.Errorf("insert persona: %w", err)
	}
	// Re-read to get the row that won (might be ours or a concurrent
	// approved one). If we get back a pending row matching our inserted
	// id, the call was new — bubble that signal up so the call site can
	// publish PersonaPending on the SSE bus.
	pp, _, rerr := t.UpsertPersonaTracked(ctx, name, createdByUserID)
	if rerr != nil {
		return Persona{}, false, rerr
	}
	wasNew := pp.ID == id && pp.Status == "pending"
	return pp, wasNew, nil
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

// ─── DRD (U9) ────────────────────────────────────────────────────────────────
//
// One DRD per flow, monotonic revision counter for ETag-style optimistic
// concurrency. The plan flags this explicitly (DI C2): SQLite CURRENT_TIMESTAMP
// has 1-second resolution and silently overwrites within the window when used
// as an ETag, so a `revision INTEGER` counter is mandatory.

// ErrRevisionConflict is returned by UpsertDRD when the expected revision
// doesn't match the current row's revision. The handler turns this into 409.
var ErrRevisionConflict = errors.New("projects: drd revision conflict")

// DRDRecord is what the GET endpoint returns.
type DRDRecord struct {
	FlowID        string
	ContentJSON   []byte // raw BlockNote document (or `{}` for empty)
	Revision      int
	UpdatedAt     time.Time
	UpdatedByUser string
}

// GetDRD looks up a DRD for a flow, scoped by tenant. Returns ErrNotFound when
// the flow doesn't exist OR isn't visible to this tenant. Returns an empty
// record (revision=0, content=`{}`) when the flow exists but no DRD has been
// written yet — this lets the editor start blank without a separate "create".
func (t *TenantRepo) GetDRD(ctx context.Context, projectSlug, flowID string) (*DRDRecord, error) {
	if t.tenantID == "" {
		return nil, errors.New("projects: tenant_id required")
	}
	if err := t.assertFlowVisible(ctx, projectSlug, flowID); err != nil {
		return nil, err
	}
	rec := &DRDRecord{FlowID: flowID, Revision: 0, ContentJSON: []byte("{}")}
	var content []byte
	var rev int
	var updatedAt sql.NullString
	var updatedBy sql.NullString
	err := t.r.db.QueryRowContext(ctx,
		`SELECT content_json, revision, updated_at, updated_by_user_id
		 FROM flow_drd WHERE flow_id = ? AND tenant_id = ?`,
		flowID, t.tenantID,
	).Scan(&content, &rev, &updatedAt, &updatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return rec, nil
	}
	if err != nil {
		return nil, fmt.Errorf("drd select: %w", err)
	}
	rec.ContentJSON = content
	rec.Revision = rev
	if updatedAt.Valid {
		rec.UpdatedAt = parseTime(updatedAt.String)
	}
	if updatedBy.Valid {
		rec.UpdatedByUser = updatedBy.String
	}
	return rec, nil
}

// UpsertDRD writes a DRD with an expected-revision check. Returns the new
// revision on success. Returns ErrRevisionConflict when expectedRevision
// doesn't match the current row's revision (handler returns 409).
func (t *TenantRepo) UpsertDRD(ctx context.Context, projectSlug, flowID string, content []byte, expectedRevision int, updatedByUserID string) (int, error) {
	if t.tenantID == "" {
		return 0, errors.New("projects: tenant_id required")
	}
	if err := t.assertFlowVisible(ctx, projectSlug, flowID); err != nil {
		return 0, err
	}
	now := rfc3339(t.now().UTC())

	if expectedRevision == 0 {
		// First-write path. ON CONFLICT means a parallel writer beat us;
		// surface as ErrRevisionConflict so the client refreshes.
		_, err := t.r.db.ExecContext(ctx,
			`INSERT INTO flow_drd (flow_id, tenant_id, content_json, revision, schema_version, updated_at, updated_by_user_id)
			 VALUES (?, ?, ?, 1, '1.0', ?, ?)`,
			flowID, t.tenantID, content, now, updatedByUserID,
		)
		if err != nil {
			low := strings.ToLower(err.Error())
			if strings.Contains(low, "unique") || strings.Contains(low, "constraint") {
				return 0, ErrRevisionConflict
			}
			return 0, fmt.Errorf("drd insert: %w", err)
		}
		return 1, nil
	}

	res, err := t.r.db.ExecContext(ctx,
		`UPDATE flow_drd SET content_json = ?, revision = revision + 1, updated_at = ?, updated_by_user_id = ?
		 WHERE flow_id = ? AND tenant_id = ? AND revision = ?`,
		content, now, updatedByUserID, flowID, t.tenantID, expectedRevision,
	)
	if err != nil {
		return 0, fmt.Errorf("drd update: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("rows affected: %w", err)
	}
	if rows != 1 {
		return 0, ErrRevisionConflict
	}
	return expectedRevision + 1, nil
}

// assertFlowVisible returns ErrNotFound when the flow doesn't exist OR
// belongs to a project this tenant cannot see. Same 404 semantics as
// GetScreenForServe / GetCanonicalTree (no existence oracle).
func (t *TenantRepo) assertFlowVisible(ctx context.Context, projectSlug, flowID string) error {
	var ok int
	err := t.r.db.QueryRowContext(ctx,
		`SELECT 1 FROM flows f
		 JOIN projects p ON p.id = f.project_id
		 WHERE f.id = ? AND p.slug = ? AND p.tenant_id = ?
		   AND f.deleted_at IS NULL AND p.deleted_at IS NULL`,
		flowID, projectSlug, t.tenantID,
	).Scan(&ok)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
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

// ─── Phase 2: prototype-link cache (U5 flow-graph rule) ──────────────────────

// GetPrototypeLinks returns every prototype link cached for the screens belonging
// to a given version. Tenant-scoped via TenantRepo.tenantID — cross-tenant
// queries return zero rows (no existence oracle).
//
// Empty slice + nil error = cache miss (the runner should fetch from Figma and
// call UpsertPrototypeLinks). The runner distinguishes "no links exist for this
// flow" from "we never tried to populate" by checking whether the prototype
// fetch has run on this version (typically tracked via audit_jobs metadata).
func (t *TenantRepo) GetPrototypeLinks(ctx context.Context, versionID string) ([]PrototypeLink, error) {
	if t.tenantID == "" {
		return nil, errors.New("projects: tenant_id required")
	}
	rows, err := t.r.db.QueryContext(ctx,
		`SELECT pl.id, pl.screen_id, pl.tenant_id, pl.source_node_id,
		        pl.destination_screen_id, pl.destination_node_id,
		        pl.trigger, pl.action, pl.metadata, pl.created_at
		   FROM screen_prototype_links pl
		   JOIN screens s ON s.id = pl.screen_id
		  WHERE s.version_id = ? AND pl.tenant_id = ?`,
		versionID, t.tenantID,
	)
	if err != nil {
		return nil, fmt.Errorf("query prototype_links: %w", err)
	}
	defer rows.Close()
	var out []PrototypeLink
	for rows.Next() {
		var link PrototypeLink
		var dstScreen, dstNode, metadata sql.NullString
		var createdAt string
		if err := rows.Scan(
			&link.ID, &link.ScreenID, &link.TenantID, &link.SourceNodeID,
			&dstScreen, &dstNode,
			&link.Trigger, &link.Action, &metadata, &createdAt,
		); err != nil {
			return nil, fmt.Errorf("scan prototype_link: %w", err)
		}
		if dstScreen.Valid {
			s := dstScreen.String
			link.DestinationScreenID = &s
		}
		if dstNode.Valid {
			n := dstNode.String
			link.DestinationNodeID = &n
		}
		if metadata.Valid {
			m := metadata.String
			link.Metadata = &m
		}
		link.CreatedAt = parseTime(createdAt)
		out = append(out, link)
	}
	return out, rows.Err()
}

// UpsertPrototypeLinks replaces the prototype links for every screen mentioned
// in the input slice. Replace-set semantics in a single transaction:
//
//  1. DELETE links scoped by tenant_id + screen_ids.
//  2. INSERT every link in the input.
//
// Idempotent on re-audit: calling twice with the same input produces the same
// row set. Tenant-scoped — cross-tenant rows are never touched.
//
// Empty slice = no-op (returns nil); the caller may want to skip the call to
// avoid an empty transaction.
func (t *TenantRepo) UpsertPrototypeLinks(ctx context.Context, links []PrototypeLink) error {
	if t.tenantID == "" {
		return errors.New("projects: tenant_id required")
	}
	if len(links) == 0 {
		return nil
	}

	// Collect distinct screen_ids — those define the replace-set scope.
	screenSet := map[string]struct{}{}
	for _, l := range links {
		if l.ScreenID == "" {
			return errors.New("projects: prototype link missing screen_id")
		}
		screenSet[l.ScreenID] = struct{}{}
	}
	screenIDs := make([]string, 0, len(screenSet))
	for s := range screenSet {
		screenIDs = append(screenIDs, s)
	}

	tx, err := t.r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// DELETE prior links for these screens, scoped by tenant.
	placeholders := make([]string, len(screenIDs))
	args := make([]any, 0, len(screenIDs)+1)
	for i, sid := range screenIDs {
		placeholders[i] = "?"
		args = append(args, sid)
	}
	args = append(args, t.tenantID)
	deleteSQL := `DELETE FROM screen_prototype_links WHERE screen_id IN (` +
		strings.Join(placeholders, ",") + `) AND tenant_id = ?`
	if _, err := tx.ExecContext(ctx, deleteSQL, args...); err != nil {
		return fmt.Errorf("delete prior prototype_links: %w", err)
	}

	// INSERT the new set.
	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO screen_prototype_links
		   (id, screen_id, tenant_id, source_node_id, destination_screen_id,
		    destination_node_id, trigger, action, metadata, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	now := rfc3339(t.now().UTC())
	for i := range links {
		l := &links[i]
		if l.ID == "" {
			l.ID = uuid.NewString()
		}
		l.TenantID = t.tenantID
		var dstScreen, dstNode, metadata any
		if l.DestinationScreenID != nil {
			dstScreen = *l.DestinationScreenID
		}
		if l.DestinationNodeID != nil {
			dstNode = *l.DestinationNodeID
		}
		if l.Metadata != nil {
			metadata = *l.Metadata
		}
		if _, err := stmt.ExecContext(ctx,
			l.ID, l.ScreenID, t.tenantID, l.SourceNodeID,
			dstScreen, dstNode, l.Trigger, l.Action, metadata, now,
		); err != nil {
			return fmt.Errorf("insert prototype_link: %w", err)
		}
	}
	return tx.Commit()
}

// ─── Phase 4 U12: single-violation fetch (plugin auto-fix) ───────────────────

// ViolationDetail is the shape returned by GetViolation for the plugin's
// auto-fix flow. It bundles the violation row with enough screen + project
// context for the plugin to locate the offending node in Figma without a
// second round-trip.
type ViolationDetail struct {
	ID           string `json:"id"`
	VersionID    string `json:"version_id"`
	ScreenID     string `json:"screen_id"`
	NodeID       string `json:"node_id"` // figma node id from canonical_tree (best-effort lookup)
	RuleID       string `json:"rule_id"`
	Severity     string `json:"severity"`
	Category     string `json:"category"`
	Property     string `json:"property"`
	Observed     string `json:"observed"`
	Suggestion   string `json:"suggestion"`
	Status       string `json:"status"`
	AutoFixable  bool   `json:"auto_fixable"`
	ProjectSlug  string `json:"project_slug"`
	ProjectName  string `json:"project_name"`
	FlowID       string `json:"flow_id"`
	FlowName     string `json:"flow_name"`
	FileID       string `json:"file_id"`
}

// GetViolation returns one violation with project + flow context for the
// plugin's auto-fix flow. Tenant-scoped — cross-tenant returns ErrNotFound.
// Slug acts as a defense-in-depth check (a plugin holding only a
// violation_id couldn't probe other tenants' rows by enumerating slugs).
func (t *TenantRepo) GetViolation(ctx context.Context, slug, violationID string) (*ViolationDetail, error) {
	row := t.r.db.QueryRowContext(ctx,
		`SELECT v.id, v.version_id, v.screen_id, v.rule_id, v.severity, v.category,
		        v.property, COALESCE(v.observed, ''), COALESCE(v.suggestion, ''),
		        v.status, v.auto_fixable,
		        p.slug, p.name,
		        f.id, f.name, f.file_id
		   FROM violations v
		   JOIN screens s ON s.id = v.screen_id
		   JOIN flows f ON f.id = s.flow_id
		   JOIN project_versions pv ON pv.id = v.version_id
		   JOIN projects p ON p.id = pv.project_id
		  WHERE v.id = ? AND v.tenant_id = ? AND p.slug = ?`,
		violationID, t.tenantID, slug,
	)
	var d ViolationDetail
	var autoFix int
	if err := row.Scan(
		&d.ID, &d.VersionID, &d.ScreenID, &d.RuleID, &d.Severity, &d.Category,
		&d.Property, &d.Observed, &d.Suggestion,
		&d.Status, &autoFix,
		&d.ProjectSlug, &d.ProjectName,
		&d.FlowID, &d.FlowName, &d.FileID,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get violation: %w", err)
	}
	d.AutoFixable = autoFix == 1
	// node_id discovery: the plugin walks the canonical_tree to find the
	// node referenced by `property`. We don't synthesize it server-side
	// because the canonical_tree representation evolves with Phase 5
	// schema work — keeping the plugin authoritative for node-walk
	// avoids a serialization-format coupling.
	d.NodeID = ""
	return &d, nil
}

// ─── Phase 4: violation lifecycle ────────────────────────────────────────────

// ViolationLifecycleResult is what UpdateViolationStatus returns so the HTTP
// handler can build an SSE event without a second round-trip to the DB.
type ViolationLifecycleResult struct {
	ViolationID string
	VersionID   string
	ProjectSlug string
	TraceID     string // most recent audit_jobs.trace_id for the version (may be "" if none)
	From        string
	To          string
}

// GetViolationForLifecycle loads the minimum fields the lifecycle handler
// needs: the current status (for transition validation) plus version_id and
// project_slug + trace_id (for the SSE fan-out). Tenant-scoped: a row owned
// by another tenant returns ErrNotFound (no existence oracle).
//
// The trace_id comes from the latest audit_jobs row for the version. If no
// audit job has been recorded yet (theoretically impossible — violations are
// only ever inserted by the audit worker which writes the audit_jobs row
// first), the field is left empty and the SSE publish becomes a no-op.
func (t *TenantRepo) GetViolationForLifecycle(ctx context.Context, violationID string) (ViolationLifecycleResult, error) {
	row := t.r.db.QueryRowContext(ctx,
		`SELECT v.id, v.version_id, v.status, p.slug,
		        COALESCE((SELECT j.trace_id FROM audit_jobs j
		                  WHERE j.version_id = v.version_id AND j.tenant_id = v.tenant_id
		                  ORDER BY j.created_at DESC LIMIT 1), '')
		   FROM violations v
		   JOIN project_versions pv ON pv.id = v.version_id
		   JOIN projects p ON p.id = pv.project_id
		  WHERE v.id = ? AND v.tenant_id = ?`,
		violationID, t.tenantID,
	)
	var out ViolationLifecycleResult
	if err := row.Scan(&out.ViolationID, &out.VersionID, &out.From, &out.ProjectSlug, &out.TraceID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ViolationLifecycleResult{}, ErrNotFound
		}
		return ViolationLifecycleResult{}, fmt.Errorf("load violation: %w", err)
	}
	return out, nil
}

// BulkLifecycleRow is one input element for BulkUpdateViolationStatus.
//
// PerRowAudit runs inside the same transaction as the UPDATE for that row;
// caller uses it to write a per-violation audit_log entry that includes the
// shared bulk_id so log queries can re-aggregate the bulk operation later.
type BulkLifecycleRow struct {
	ViolationID string
	From        string // expected current status (used in WHERE for race safety)
	To          string // resulting status
	PerRowAudit func(tx *sql.Tx, violationID, fromStatus, toStatus string) error
}

// BulkLifecycleSummary reports per-id outcomes after a bulk run.
//
// Updated holds the violation_ids whose status flipped successfully. Skipped
// holds ids whose row did not match the expected From (already in target
// state, deleted, or cross-tenant); these are non-fatal — the caller surfaces
// them to the API consumer so they can decide whether to refetch + retry.
type BulkLifecycleSummary struct {
	Updated []string
	Skipped []string
}

// BulkUpdateViolationStatus applies many transitions inside a single
// transaction. The caller is expected to have already validated each (action,
// reason, role) tuple via ValidateTransition. Per-row audit writes happen in
// the same transaction so the (status, log) pair is always consistent.
//
// Failure semantics:
//   - A row whose UPDATE affects 0 rows (mismatched From, deleted, cross-
//     tenant) is recorded in Skipped — no rollback.
//   - A row whose audit-log writer returns an error rolls back the entire
//     transaction. Bulk operations are all-or-nothing on the audit-log
//     dimension; we never want a status flip without its log entry.
func (t *TenantRepo) BulkUpdateViolationStatus(ctx context.Context, rows []BulkLifecycleRow) (BulkLifecycleSummary, error) {
	if len(rows) == 0 {
		return BulkLifecycleSummary{}, nil
	}
	tx, err := t.r.db.BeginTx(ctx, nil)
	if err != nil {
		return BulkLifecycleSummary{}, fmt.Errorf("begin bulk tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	updateStmt, err := tx.PrepareContext(ctx,
		`UPDATE violations SET status = ?
		   WHERE id = ? AND tenant_id = ? AND status = ?`)
	if err != nil {
		return BulkLifecycleSummary{}, fmt.Errorf("prepare bulk update: %w", err)
	}
	defer updateStmt.Close()

	summary := BulkLifecycleSummary{}
	for _, r := range rows {
		res, err := updateStmt.ExecContext(ctx, r.To, r.ViolationID, t.tenantID, r.From)
		if err != nil {
			return BulkLifecycleSummary{}, fmt.Errorf("bulk update row %s: %w", r.ViolationID, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return BulkLifecycleSummary{}, fmt.Errorf("rows affected: %w", err)
		}
		if n == 0 {
			summary.Skipped = append(summary.Skipped, r.ViolationID)
			continue
		}
		if r.PerRowAudit != nil {
			if err := r.PerRowAudit(tx, r.ViolationID, r.From, r.To); err != nil {
				return BulkLifecycleSummary{}, fmt.Errorf("audit row %s: %w", r.ViolationID, err)
			}
		}
		summary.Updated = append(summary.Updated, r.ViolationID)
	}

	if err := tx.Commit(); err != nil {
		return BulkLifecycleSummary{}, fmt.Errorf("commit bulk: %w", err)
	}
	return summary, nil
}

// LoadViolationsForBulk fetches the current status + version_id + project_slug
// + trace_id for many violation IDs at once, tenant-scoped. IDs not visible
// to the tenant are silently dropped (no existence oracle) — caller treats
// missing IDs as "skipped".
func (t *TenantRepo) LoadViolationsForBulk(ctx context.Context, ids []string) ([]ViolationLifecycleResult, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	placeholders := strings.Repeat("?,", len(ids))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, 0, len(ids)+1)
	for _, id := range ids {
		args = append(args, id)
	}
	args = append(args, t.tenantID)

	q := `SELECT v.id, v.version_id, v.status, p.slug,
	             COALESCE((SELECT j.trace_id FROM audit_jobs j
	                       WHERE j.version_id = v.version_id AND j.tenant_id = v.tenant_id
	                       ORDER BY j.created_at DESC LIMIT 1), '')
	        FROM violations v
	        JOIN project_versions pv ON pv.id = v.version_id
	        JOIN projects p ON p.id = pv.project_id
	       WHERE v.id IN (` + placeholders + `) AND v.tenant_id = ?`

	rows, err := t.r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("bulk load: %w", err)
	}
	defer rows.Close()

	var out []ViolationLifecycleResult
	for rows.Next() {
		var v ViolationLifecycleResult
		if err := rows.Scan(&v.ViolationID, &v.VersionID, &v.From, &v.ProjectSlug, &v.TraceID); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// UpdateViolationStatus applies a validated lifecycle transition to a single
// violation row. Caller is expected to have already run ValidateTransition on
// the result of GetViolationForLifecycle.
//
// The repository updates the row inside a transaction; the audit_log write
// happens in the same transaction so a row that flips status without a log
// entry is impossible. The caller (server.go) supplies the audit_log payload.
//
// Returns ErrNotFound if the row was deleted between the GET and the UPDATE
// (race window the recovery sweeper can introduce).
func (t *TenantRepo) UpdateViolationStatus(ctx context.Context, violationID string, transition LifecycleTransition, auditEntry func(tx *sql.Tx) error) error {
	tx, err := t.r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin lifecycle tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx,
		`UPDATE violations SET status = ?
		   WHERE id = ? AND tenant_id = ? AND status = ?`,
		transition.To, violationID, t.tenantID, transition.From,
	)
	if err != nil {
		return fmt.Errorf("update violation status: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		// Either the row vanished, the tenant boundary rejected it, or another
		// transition raced ours and the From no longer matches. All three map
		// to 404 from the API's perspective (no existence oracle).
		return ErrNotFound
	}

	if auditEntry != nil {
		if err := auditEntry(tx); err != nil {
			return fmt.Errorf("write audit log: %w", err)
		}
	}

	return tx.Commit()
}

// ─── Phase 5 U3: decisions ──────────────────────────────────────────────────

// resolveLatestVersionID returns the most recent project_versions.id for a
// flow inside the caller's tenant. Used by CreateDecision when the caller
// doesn't pin a version explicitly — Phase 5 anchors decisions to whatever
// version is current at decision time.
func (t *TenantRepo) resolveLatestVersionID(ctx context.Context, flowID string) (string, error) {
	var versionID string
	err := t.r.db.QueryRowContext(ctx,
		`SELECT pv.id
		   FROM project_versions pv
		   JOIN flows f ON f.project_id = pv.project_id
		  WHERE f.id = ? AND f.tenant_id = ?
		  ORDER BY pv.version_index DESC
		  LIMIT 1`,
		flowID, t.tenantID,
	).Scan(&versionID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("latest version: %w", err)
	}
	return versionID, nil
}

// loadFlowSupersessionChain pre-loads every (id, supersedes_id) pair for a
// flow so DetectSupersessionCycle can walk the chain in-memory. One query
// per CreateDecision is acceptable — flows have <100 decisions in practice
// and the index on (tenant_id, flow_id, made_at DESC) covers the scan.
func (t *TenantRepo) loadFlowSupersessionChain(ctx context.Context, flowID string) (map[string]CycleCheckHop, error) {
	rows, err := t.r.db.QueryContext(ctx,
		`SELECT id, COALESCE(supersedes_id, '') FROM decisions
		  WHERE tenant_id = ? AND flow_id = ? AND deleted_at IS NULL`,
		t.tenantID, flowID,
	)
	if err != nil {
		return nil, fmt.Errorf("chain: %w", err)
	}
	defer rows.Close()
	chain := make(map[string]CycleCheckHop)
	for rows.Next() {
		var hop CycleCheckHop
		if err := rows.Scan(&hop.ID, &hop.SupersedesID); err != nil {
			return nil, err
		}
		chain[hop.ID] = hop
	}
	return chain, rows.Err()
}

// assertFlowVisibleAndScoped checks the flow exists in the caller's tenant +
// returns its project_id (used by Phase 1's flow read paths). Reused here
// to fail fast on cross-tenant create attempts.
func (t *TenantRepo) assertFlowVisibleByID(ctx context.Context, flowID string) error {
	var dummy string
	err := t.r.db.QueryRowContext(ctx,
		`SELECT id FROM flows WHERE id = ? AND tenant_id = ? AND deleted_at IS NULL`,
		flowID, t.tenantID,
	).Scan(&dummy)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// CreateDecision writes a decisions row + decision_links rows in one
// transaction. The supersession-cycle check happens in-process with the
// pre-loaded chain so the SQL stays simple. When supersedes_id is non-empty,
// the predecessor's status flips to 'superseded' + its superseded_by_id
// updates inside the same tx — the chain is always self-consistent on read.
//
// Caller is expected to have already run ValidateDecisionInput. versionID
// may be empty; when empty, the repo resolves the flow's latest version.
func (t *TenantRepo) CreateDecision(ctx context.Context, flowID, versionID, madeByUserID string, in DecisionInput) (*DecisionRecord, error) {
	if err := t.assertFlowVisibleByID(ctx, flowID); err != nil {
		return nil, err
	}
	if versionID == "" {
		v, err := t.resolveLatestVersionID(ctx, flowID)
		if err != nil {
			return nil, err
		}
		versionID = v
	}

	// Cycle prevention. Empty supersedes_id ⇒ no cycle possible.
	if in.SupersedesID != "" {
		chain, err := t.loadFlowSupersessionChain(ctx, flowID)
		if err != nil {
			return nil, err
		}
		if _, ok := chain[in.SupersedesID]; !ok {
			return nil, fmt.Errorf("%w: predecessor not found in flow", ErrNotFound)
		}
		// We're proposing a NEW id, so its successor chain is empty —
		// the only way it cycles is if startID transitively points to a
		// future "us". The detector treats proposedID as the would-be
		// new id; we use a placeholder until we have one (so the check
		// just verifies the chain itself isn't cyclic — which the schema
		// permits but our writes never produce).
		if DetectSupersessionCycle("__proposed__", in.SupersedesID, chain) {
			return nil, ErrDecisionCycle
		}
	}

	now := t.now()
	id := uuid.NewString()
	tx, err := t.r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO decisions
		 (id, tenant_id, flow_id, version_id, title, body_json, status,
		  made_by_user_id, made_at, supersedes_id,
		  created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, t.tenantID, flowID, versionID, in.Title, in.BodyJSON, in.Status,
		madeByUserID, rfc3339(now),
		nullIfEmpty(in.SupersedesID),
		rfc3339(now), rfc3339(now),
	); err != nil {
		return nil, fmt.Errorf("insert decision: %w", err)
	}

	// Flip the predecessor to superseded in the same tx so chain reads
	// are consistent. When the predecessor was already superseded
	// (multiple chains converging), we still mark `superseded_by_id`
	// to the most recent — admins can read the historical chain via
	// audit_log.
	if in.SupersedesID != "" {
		if _, err := tx.ExecContext(ctx,
			`UPDATE decisions
			    SET status = 'superseded',
			        superseded_by_id = ?,
			        updated_at = ?
			  WHERE id = ? AND tenant_id = ?`,
			id, rfc3339(now), in.SupersedesID, t.tenantID,
		); err != nil {
			return nil, fmt.Errorf("supersede predecessor: %w", err)
		}
	}

	for _, l := range in.Links {
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO decision_links
			 (decision_id, link_type, target_id, tenant_id, created_at)
			 VALUES (?, ?, ?, ?, ?)`,
			id, string(l.LinkType), l.TargetID, t.tenantID, rfc3339(now),
		); err != nil {
			return nil, fmt.Errorf("insert decision_link: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}

	return t.GetDecision(ctx, id)
}

// GetDecision returns one decision with its links pre-joined. Tenant-scoped.
func (t *TenantRepo) GetDecision(ctx context.Context, decisionID string) (*DecisionRecord, error) {
	row := t.r.db.QueryRowContext(ctx,
		`SELECT id, tenant_id, flow_id, version_id, title, COALESCE(body_json, X''),
		        status, made_by_user_id, made_at,
		        superseded_by_id, supersedes_id,
		        created_at, updated_at
		   FROM decisions
		  WHERE id = ? AND tenant_id = ? AND deleted_at IS NULL`,
		decisionID, t.tenantID,
	)
	rec := &DecisionRecord{}
	var madeAt, createdAt, updatedAt string
	var supersededBy, supersedes sql.NullString
	if err := row.Scan(
		&rec.ID, &rec.TenantID, &rec.FlowID, &rec.VersionID, &rec.Title, &rec.BodyJSON,
		&rec.Status, &rec.MadeByUserID, &madeAt,
		&supersededBy, &supersedes,
		&createdAt, &updatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scan decision: %w", err)
	}
	rec.MadeAt = parseTime(madeAt)
	rec.CreatedAt = parseTime(createdAt)
	rec.UpdatedAt = parseTime(updatedAt)
	if supersededBy.Valid {
		s := supersededBy.String
		rec.SupersededByID = &s
	}
	if supersedes.Valid {
		s := supersedes.String
		rec.SupersedesID = &s
	}
	if len(rec.BodyJSON) == 0 {
		rec.BodyJSON = nil
	}

	// Side-load links.
	links, err := t.r.db.QueryContext(ctx,
		`SELECT decision_id, link_type, target_id, created_at FROM decision_links
		  WHERE decision_id = ? AND tenant_id = ?
		  ORDER BY created_at ASC`,
		decisionID, t.tenantID,
	)
	if err != nil {
		return nil, fmt.Errorf("links: %w", err)
	}
	defer links.Close()
	for links.Next() {
		var l DecisionLink
		var ts string
		var lt string
		if err := links.Scan(&l.DecisionID, &lt, &l.TargetID, &ts); err != nil {
			return nil, err
		}
		l.LinkType = LinkType(lt)
		l.CreatedAt = parseTime(ts)
		rec.Links = append(rec.Links, l)
	}
	return rec, links.Err()
}

// ListDecisionsForFlow returns decisions for a flow, ordered by made_at DESC.
// includeSuperseded toggles whether the chain's predecessors are returned.
func (t *TenantRepo) ListDecisionsForFlow(ctx context.Context, flowID string, includeSuperseded bool) ([]DecisionRecord, error) {
	if err := t.assertFlowVisibleByID(ctx, flowID); err != nil {
		return nil, err
	}

	var statusClause string
	args := []any{t.tenantID, flowID}
	if !includeSuperseded {
		statusClause = " AND status IN ('proposed', 'accepted')"
	}

	rows, err := t.r.db.QueryContext(ctx,
		`SELECT id, tenant_id, flow_id, version_id, title, COALESCE(body_json, X''),
		        status, made_by_user_id, made_at,
		        superseded_by_id, supersedes_id,
		        created_at, updated_at
		   FROM decisions
		  WHERE tenant_id = ? AND flow_id = ? AND deleted_at IS NULL`+statusClause+`
		  ORDER BY made_at DESC`,
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("list decisions: %w", err)
	}
	defer rows.Close()

	var out []DecisionRecord
	ids := make([]string, 0)
	idIdx := make(map[string]int)
	for rows.Next() {
		var rec DecisionRecord
		var madeAt, createdAt, updatedAt string
		var supersededBy, supersedes sql.NullString
		if err := rows.Scan(
			&rec.ID, &rec.TenantID, &rec.FlowID, &rec.VersionID, &rec.Title, &rec.BodyJSON,
			&rec.Status, &rec.MadeByUserID, &madeAt,
			&supersededBy, &supersedes,
			&createdAt, &updatedAt,
		); err != nil {
			return nil, err
		}
		rec.MadeAt = parseTime(madeAt)
		rec.CreatedAt = parseTime(createdAt)
		rec.UpdatedAt = parseTime(updatedAt)
		if supersededBy.Valid {
			s := supersededBy.String
			rec.SupersededByID = &s
		}
		if supersedes.Valid {
			s := supersedes.String
			rec.SupersedesID = &s
		}
		if len(rec.BodyJSON) == 0 {
			rec.BodyJSON = nil
		}
		idIdx[rec.ID] = len(out)
		ids = append(ids, rec.ID)
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return out, nil
	}

	// Side-load all links for the listed decisions in one query.
	placeholders := strings.Repeat("?,", len(ids))
	placeholders = placeholders[:len(placeholders)-1]
	linkArgs := make([]any, 0, len(ids)+1)
	for _, id := range ids {
		linkArgs = append(linkArgs, id)
	}
	linkArgs = append(linkArgs, t.tenantID)
	linkRows, err := t.r.db.QueryContext(ctx,
		`SELECT decision_id, link_type, target_id, created_at FROM decision_links
		  WHERE decision_id IN (`+placeholders+`) AND tenant_id = ?`,
		linkArgs...,
	)
	if err != nil {
		return nil, fmt.Errorf("links bulk: %w", err)
	}
	defer linkRows.Close()
	for linkRows.Next() {
		var l DecisionLink
		var ts, lt string
		if err := linkRows.Scan(&l.DecisionID, &lt, &l.TargetID, &ts); err != nil {
			return nil, err
		}
		l.LinkType = LinkType(lt)
		l.CreatedAt = parseTime(ts)
		if idx, ok := idIdx[l.DecisionID]; ok {
			out[idx].Links = append(out[idx].Links, l)
		}
	}
	return out, linkRows.Err()
}

// AdminReactivateOutcome captures the chain delta for a successful
// admin reactivate. Phase 5.3 P1 — the mind graph (Phase 6) consumes
// this via the SSE event so it can erase the supersession edge from
// the predecessor to its former successor without a follow-up fetch.
type AdminReactivateOutcome struct {
	Updated                  int
	PreviousSupersededByID   string
}

// AdminReactivateDecision flips a superseded decision back to 'accepted'
// and clears its superseded_by_id. Cross-tenant write — only the
// super-admin handler should reach this method. Returns the count of
// rows affected (0 when the id doesn't exist or wasn't superseded) plus
// the prior superseded_by_id so the caller can publish the chain delta.
//
// Single transaction so the SELECT reads the prior state under the same
// lock as the UPDATE; otherwise a concurrent admin click could race.
//
// The reverse operation — moving Accepted → Superseded — happens through
// CreateDecision when a successor's supersedes_id points at this one;
// admins shouldn't manually mark a decision superseded outside that
// chain because doing so would orphan the chain.
func (db *DB) AdminReactivateDecision(ctx context.Context, decisionID string) (AdminReactivateOutcome, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		return AdminReactivateOutcome{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var prior sql.NullString
	if err := tx.QueryRowContext(ctx,
		`SELECT superseded_by_id FROM decisions
		  WHERE id = ? AND status = 'superseded' AND deleted_at IS NULL`,
		decisionID,
	).Scan(&prior); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AdminReactivateOutcome{Updated: 0}, nil
		}
		return AdminReactivateOutcome{}, fmt.Errorf("read prior: %w", err)
	}

	res, err := tx.ExecContext(ctx,
		`UPDATE decisions
		    SET status = 'accepted',
		        superseded_by_id = NULL,
		        updated_at = ?
		  WHERE id = ?
		    AND status = 'superseded'
		    AND deleted_at IS NULL`,
		now, decisionID,
	)
	if err != nil {
		return AdminReactivateOutcome{}, fmt.Errorf("admin reactivate: %w", err)
	}
	n, _ := res.RowsAffected()

	if err := tx.Commit(); err != nil {
		return AdminReactivateOutcome{}, fmt.Errorf("commit: %w", err)
	}
	out := AdminReactivateOutcome{Updated: int(n)}
	if prior.Valid {
		out.PreviousSupersededByID = prior.String
	}
	return out, nil
}

// ListRecentDecisions returns the most recent decisions across the entire
// database — used by /atlas/admin's Recent Decisions feed. Super-admin
// scope; the handler guards the call.
func (db *DB) ListRecentDecisions(ctx context.Context, limit int) ([]DecisionRecord, error) {
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	rows, err := db.db.QueryContext(ctx,
		`SELECT id, tenant_id, flow_id, version_id, title, COALESCE(body_json, X''),
		        status, made_by_user_id, made_at,
		        superseded_by_id, supersedes_id,
		        created_at, updated_at
		   FROM decisions
		  WHERE deleted_at IS NULL AND status IN ('proposed', 'accepted')
		  ORDER BY made_at DESC
		  LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("recent decisions: %w", err)
	}
	defer rows.Close()
	var out []DecisionRecord
	for rows.Next() {
		var rec DecisionRecord
		var madeAt, createdAt, updatedAt string
		var supersededBy, supersedes sql.NullString
		if err := rows.Scan(
			&rec.ID, &rec.TenantID, &rec.FlowID, &rec.VersionID, &rec.Title, &rec.BodyJSON,
			&rec.Status, &rec.MadeByUserID, &madeAt,
			&supersededBy, &supersedes,
			&createdAt, &updatedAt,
		); err != nil {
			return nil, err
		}
		rec.MadeAt = parseTime(madeAt)
		rec.CreatedAt = parseTime(createdAt)
		rec.UpdatedAt = parseTime(updatedAt)
		if supersededBy.Valid {
			s := supersededBy.String
			rec.SupersededByID = &s
		}
		if supersedes.Valid {
			s := supersedes.String
			rec.SupersedesID = &s
		}
		if len(rec.BodyJSON) == 0 {
			rec.BodyJSON = nil
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// DB is the unexported repo handle wrapper exposed for cross-tenant queries
// (currently just ListRecentDecisions). Uses the same *sql.DB the
// TenantRepo wraps; Phase 7 admin work may reuse this pattern.
type DB struct {
	db *sql.DB
}

// NewDB returns a DB-scoped handle. Caller is responsible for super-admin
// gating before invoking any cross-tenant method.
func NewDB(db *sql.DB) *DB {
	return &DB{db: db}
}

// nullIfEmpty returns nil for SQL when the string is empty (driver writes
// SQL NULL), else the string itself. Match the pattern in nullString().
func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// ─── Phase 5 U6: comments + mention resolution ──────────────────────────────

// resolveMentionEmails takes a slice of @names parsed from comment bodies
// and looks up their user_ids inside the caller's tenant. Names that
// don't match any tenant user are silently dropped — typo'd @mentions
// shouldn't reject the comment. Email is the username today: the part
// before "@" in the user's email maps to the @name. Phase 7 admin can
// configure a display-name field; until then, email-prefix is the
// pragmatic default.
func (t *TenantRepo) resolveMentionEmails(ctx context.Context, names []string) ([]string, error) {
	if len(names) == 0 {
		return nil, nil
	}
	// Build LIKE patterns: lower(SUBSTR(email, 1, INSTR(email,'@')-1)) = ?.
	// SQLite supports a parameter-driven IN clause; we go with one query
	// per call (small N, < MaxMentionsPerComment).
	args := make([]any, 0, len(names)+1)
	placeholders := make([]string, 0, len(names))
	for _, n := range names {
		placeholders = append(placeholders, "?")
		args = append(args, n)
	}
	args = append(args, t.tenantID)

	q := `SELECT u.id
	        FROM users u
	        JOIN tenant_users tu ON tu.user_id = u.id
	       WHERE LOWER(SUBSTR(u.email, 1, INSTR(u.email, '@') - 1)) IN (` +
		strings.Join(placeholders, ",") + `)
	         AND tu.tenant_id = ?
	         AND tu.status = 'active'`
	rows, err := t.r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("resolve mentions: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// CreateComment writes a comment row + emits notification rows for each
// resolved mention, all inside one transaction. Caller is expected to
// have already run ValidateCommentInput.
//
// Returns the persisted record + the recipient user_ids who got mention
// notifications (caller fans them out via SSE post-commit).
func (t *TenantRepo) CreateComment(ctx context.Context, authorUserID string, in CommentInput) (*CommentRecord, []string, error) {
	mentionUserIDs, err := t.resolveMentionEmails(ctx, in.MentionedNames)
	if err != nil {
		return nil, nil, err
	}
	// Drop self-mentions — the author getting their own notification is noise.
	filtered := mentionUserIDs[:0]
	for _, id := range mentionUserIDs {
		if id != authorUserID {
			filtered = append(filtered, id)
		}
	}
	mentionUserIDs = filtered

	mentionsJSON := []byte("null")
	if len(mentionUserIDs) > 0 {
		bs, err := json.Marshal(mentionUserIDs)
		if err != nil {
			return nil, nil, fmt.Errorf("marshal mentions: %w", err)
		}
		mentionsJSON = bs
	}

	now := t.now()
	id := uuid.NewString()
	tx, err := t.r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO drd_comments
		 (id, tenant_id, target_kind, target_id, flow_id, author_user_id,
		  body, parent_comment_id, mentions_user_ids, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, t.tenantID, string(in.TargetKind), in.TargetID, in.FlowID, authorUserID,
		in.Body, nullIfEmpty(in.ParentCommentID), nullIfNullJSON(mentionsJSON),
		rfc3339(now), rfc3339(now),
	); err != nil {
		return nil, nil, fmt.Errorf("insert comment: %w", err)
	}

	for _, recipient := range mentionUserIDs {
		payload, _ := json.Marshal(map[string]any{
			"flow_id":      in.FlowID,
			"target_kind":  string(in.TargetKind),
			"target_id":    in.TargetID,
			"comment_id":   id,
			"body_snippet": snippet(in.Body, 140),
			"author_user_id": authorUserID,
		})
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO notifications
			 (id, tenant_id, recipient_user_id, kind, target_kind, target_id,
			  flow_id, actor_user_id, payload_json, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			uuid.NewString(), t.tenantID, recipient,
			string(NotifMention), string(NotifTargetComment), id,
			nullIfEmpty(in.FlowID), authorUserID, string(payload), rfc3339(now),
		); err != nil {
			return nil, nil, fmt.Errorf("insert notification: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, nil, fmt.Errorf("commit: %w", err)
	}

	rec, err := t.GetComment(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	return rec, mentionUserIDs, nil
}

// nullIfNullJSON returns nil for SQL when the JSON literal is "null"
// (driver writes SQL NULL into mentions_user_ids), else the bytes.
func nullIfNullJSON(b []byte) any {
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	return string(b)
}

// snippet returns the first n runes of body for the notification preview.
func snippet(body string, n int) string {
	if len(body) <= n {
		return body
	}
	if n <= 1 {
		return body[:n]
	}
	return body[:n-1] + "…"
}

// GetComment returns one comment by id; tenant-scoped.
func (t *TenantRepo) GetComment(ctx context.Context, commentID string) (*CommentRecord, error) {
	row := t.r.db.QueryRowContext(ctx,
		`SELECT id, tenant_id, target_kind, target_id, flow_id, author_user_id,
		        body, parent_comment_id, COALESCE(mentions_user_ids, ''),
		        COALESCE(resolved_at, ''), COALESCE(resolved_by, ''),
		        created_at, updated_at
		   FROM drd_comments
		  WHERE id = ? AND tenant_id = ? AND deleted_at IS NULL`,
		commentID, t.tenantID,
	)
	rec := &CommentRecord{}
	var parent sql.NullString
	var mentions string
	if err := row.Scan(
		&rec.ID, &rec.TenantID, &rec.TargetKind, &rec.TargetID, &rec.FlowID,
		&rec.AuthorUserID, &rec.Body, &parent, &mentions,
		&rec.ResolvedAt, &rec.ResolvedBy, &rec.CreatedAt, &rec.UpdatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get comment: %w", err)
	}
	if parent.Valid {
		s := parent.String
		rec.ParentCommentID = &s
	}
	if mentions != "" {
		_ = json.Unmarshal([]byte(mentions), &rec.MentionsUserIDs)
	}
	return rec, nil
}

// ListCommentsForTarget returns every (non-deleted) comment for a target,
// oldest first, with linear depth=1 thread structure (replies are siblings
// of the root comment ordered by created_at ASC).
func (t *TenantRepo) ListCommentsForTarget(ctx context.Context, kind CommentTargetKind, targetID string) ([]CommentRecord, error) {
	rows, err := t.r.db.QueryContext(ctx,
		`SELECT id, tenant_id, target_kind, target_id, flow_id, author_user_id,
		        body, parent_comment_id, COALESCE(mentions_user_ids, ''),
		        COALESCE(resolved_at, ''), COALESCE(resolved_by, ''),
		        created_at, updated_at
		   FROM drd_comments
		  WHERE tenant_id = ? AND target_kind = ? AND target_id = ?
		    AND deleted_at IS NULL
		  ORDER BY created_at ASC`,
		t.tenantID, string(kind), targetID,
	)
	if err != nil {
		return nil, fmt.Errorf("list comments: %w", err)
	}
	defer rows.Close()
	var out []CommentRecord
	for rows.Next() {
		var rec CommentRecord
		var parent sql.NullString
		var mentions string
		if err := rows.Scan(
			&rec.ID, &rec.TenantID, &rec.TargetKind, &rec.TargetID, &rec.FlowID,
			&rec.AuthorUserID, &rec.Body, &parent, &mentions,
			&rec.ResolvedAt, &rec.ResolvedBy, &rec.CreatedAt, &rec.UpdatedAt,
		); err != nil {
			return nil, err
		}
		if parent.Valid {
			s := parent.String
			rec.ParentCommentID = &s
		}
		if mentions != "" {
			_ = json.Unmarshal([]byte(mentions), &rec.MentionsUserIDs)
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// ResolveComment marks a comment as resolved by the given user. Idempotent:
// already-resolved comments return without error (caller can re-run).
func (t *TenantRepo) ResolveComment(ctx context.Context, commentID, userID string) error {
	now := t.now()
	res, err := t.r.db.ExecContext(ctx,
		`UPDATE drd_comments
		    SET resolved_at = ?, resolved_by = ?, updated_at = ?
		  WHERE id = ? AND tenant_id = ? AND resolved_at IS NULL AND deleted_at IS NULL`,
		rfc3339(now), userID, rfc3339(now),
		commentID, t.tenantID,
	)
	if err != nil {
		return fmt.Errorf("resolve comment: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		// Either the row vanished, was already resolved, or cross-tenant.
		// Confirm-existence query distinguishes ErrNotFound from idempotent.
		var dummy string
		err := t.r.db.QueryRowContext(ctx,
			`SELECT id FROM drd_comments WHERE id = ? AND tenant_id = ? AND deleted_at IS NULL`,
			commentID, t.tenantID,
		).Scan(&dummy)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		// already resolved — idempotent no-op
	}
	return nil
}

// ─── Phase 5 U7: notifications ──────────────────────────────────────────────

// ListNotificationsForUser returns the requesting user's notifications,
// scoped to their tenant. unreadOnly limits to read_at IS NULL.
func (t *TenantRepo) ListNotificationsForUser(ctx context.Context, userID string, unreadOnly bool, limit int) ([]NotificationRecord, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := `SELECT id, tenant_id, recipient_user_id, kind,
	             COALESCE(target_kind, ''), COALESCE(target_id, ''),
	             COALESCE(flow_id, ''), COALESCE(actor_user_id, ''),
	             COALESCE(payload_json, ''), COALESCE(delivered_via, ''),
	             COALESCE(read_at, ''), created_at
	        FROM notifications
	       WHERE tenant_id = ? AND recipient_user_id = ?`
	if unreadOnly {
		q += ` AND read_at IS NULL`
	}
	q += ` ORDER BY created_at DESC LIMIT ?`

	rows, err := t.r.db.QueryContext(ctx, q, t.tenantID, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("list notifications: %w", err)
	}
	defer rows.Close()
	var out []NotificationRecord
	for rows.Next() {
		var rec NotificationRecord
		var payload, deliveredVia string
		if err := rows.Scan(
			&rec.ID, &rec.TenantID, &rec.RecipientUserID, &rec.Kind,
			&rec.TargetKind, &rec.TargetID, &rec.FlowID, &rec.ActorUserID,
			&payload, &deliveredVia, &rec.ReadAt, &rec.CreatedAt,
		); err != nil {
			return nil, err
		}
		if payload != "" {
			rec.PayloadJSON = []byte(payload)
		}
		if deliveredVia != "" {
			_ = json.Unmarshal([]byte(deliveredVia), &rec.DeliveredVia)
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// MarkNotificationsRead bulks set read_at on a set of ids, scoped to the
// caller's user. Returns the count of rows actually flipped (0 if all were
// already read).
func (t *TenantRepo) MarkNotificationsRead(ctx context.Context, userID string, ids []string) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	placeholders := strings.Repeat("?,", len(ids))
	placeholders = placeholders[:len(placeholders)-1]
	now := rfc3339(t.now())

	// Args order matches the query: SET read_at = ?, then IDs (IN clause),
	// then tenant_id, then recipient_user_id.
	args := make([]any, 0, len(ids)+3)
	args = append(args, now)
	for _, id := range ids {
		args = append(args, id)
	}
	args = append(args, t.tenantID, userID)

	res, err := t.r.db.ExecContext(ctx,
		`UPDATE notifications
		    SET read_at = ?
		  WHERE id IN (`+placeholders+`)
		    AND tenant_id = ?
		    AND recipient_user_id = ?
		    AND read_at IS NULL`,
		args...,
	)
	if err != nil {
		return 0, fmt.Errorf("mark read: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// ─── U12 bundle helpers (HandleProjectGet) ──────────────────────────────────
//
// The four list-by-* methods below back the bundled project-get response.
// Each is tenant-scoped via the WHERE clause so cross-tenant joins return
// empty rather than an existence oracle. The handler stitches them together
// after resolving the active version (?v=<id> or latest by version_index).

// ListVersionsByProject returns every version row for a project, newest first
// (highest version_index first). Tenant-scoped — cross-tenant project IDs
// silently return zero rows.
func (t *TenantRepo) ListVersionsByProject(ctx context.Context, projectID string) ([]ProjectVersion, error) {
	if t.tenantID == "" {
		return nil, errors.New("projects: tenant_id required")
	}
	rows, err := t.r.db.QueryContext(ctx,
		`SELECT id, project_id, version_index, status, pipeline_started_at, pipeline_heartbeat_at,
		        error, created_by_user_id, created_at
		   FROM project_versions
		  WHERE project_id = ? AND tenant_id = ?
		  ORDER BY version_index DESC`,
		projectID, t.tenantID,
	)
	if err != nil {
		return nil, fmt.Errorf("list versions: %w", err)
	}
	defer rows.Close()
	var out []ProjectVersion
	for rows.Next() {
		var v ProjectVersion
		var startedAt, heartbeatAt, errStr sql.NullString
		var createdAt string
		if err := rows.Scan(&v.ID, &v.ProjectID, &v.VersionIndex, &v.Status,
			&startedAt, &heartbeatAt, &errStr, &v.CreatedByUserID, &createdAt); err != nil {
			return nil, err
		}
		v.TenantID = t.tenantID
		v.CreatedAt = parseTime(createdAt)
		if startedAt.Valid {
			tt := parseTime(startedAt.String)
			v.PipelineStartedAt = &tt
		}
		if heartbeatAt.Valid {
			tt := parseTime(heartbeatAt.String)
			v.PipelineHeartbeatAt = &tt
		}
		if errStr.Valid {
			v.Error = errStr.String
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ListScreensByVersion returns every screen for a specific version, ordered
// by created_at to keep the atlas paint deterministic across re-fetches.
func (t *TenantRepo) ListScreensByVersion(ctx context.Context, versionID string) ([]Screen, error) {
	if t.tenantID == "" {
		return nil, errors.New("projects: tenant_id required")
	}
	rows, err := t.r.db.QueryContext(ctx,
		`SELECT id, version_id, flow_id, x, y, width, height, screen_logical_id,
		        png_storage_key, created_at
		   FROM screens
		  WHERE version_id = ? AND tenant_id = ?
		  ORDER BY created_at`,
		versionID, t.tenantID,
	)
	if err != nil {
		return nil, fmt.Errorf("list screens: %w", err)
	}
	defer rows.Close()
	var out []Screen
	for rows.Next() {
		var s Screen
		var pngKey sql.NullString
		var createdAt string
		if err := rows.Scan(&s.ID, &s.VersionID, &s.FlowID, &s.X, &s.Y,
			&s.Width, &s.Height, &s.ScreenLogicalID, &pngKey, &createdAt); err != nil {
			return nil, err
		}
		s.TenantID = t.tenantID
		s.CreatedAt = parseTime(createdAt)
		if pngKey.Valid {
			v := pngKey.String
			s.PNGStorageKey = &v
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ListScreenModesByVersion returns every screen_mode row tied to screens in
// the given version. Joined through screens so the version filter still
// applies — screen_modes itself doesn't carry version_id.
func (t *TenantRepo) ListScreenModesByVersion(ctx context.Context, versionID string) ([]ScreenMode, error) {
	if t.tenantID == "" {
		return nil, errors.New("projects: tenant_id required")
	}
	rows, err := t.r.db.QueryContext(ctx,
		`SELECT sm.id, sm.screen_id, sm.mode_label, sm.figma_frame_id,
		        COALESCE(sm.explicit_variable_modes_json, '')
		   FROM screen_modes sm
		   JOIN screens s ON s.id = sm.screen_id
		  WHERE s.version_id = ? AND sm.tenant_id = ? AND s.tenant_id = ?`,
		versionID, t.tenantID, t.tenantID,
	)
	if err != nil {
		return nil, fmt.Errorf("list screen_modes: %w", err)
	}
	defer rows.Close()
	var out []ScreenMode
	for rows.Next() {
		var m ScreenMode
		if err := rows.Scan(&m.ID, &m.ScreenID, &m.ModeLabel, &m.FigmaFrameID,
			&m.ExplicitVariableModesJSON); err != nil {
			return nil, err
		}
		m.TenantID = t.tenantID
		out = append(out, m)
	}
	return out, rows.Err()
}

// ListPersonasForTenant returns every non-deleted persona in the tenant,
// approved + pending alike. The frontend needs both so the persona switcher
// can show pending ones with a status badge.
func (t *TenantRepo) ListPersonasForTenant(ctx context.Context) ([]Persona, error) {
	if t.tenantID == "" {
		return nil, errors.New("projects: tenant_id required")
	}
	rows, err := t.r.db.QueryContext(ctx,
		`SELECT id, name, status, created_by_user_id, approved_by_user_id, approved_at, created_at
		   FROM personas
		  WHERE tenant_id = ? AND deleted_at IS NULL
		  ORDER BY status, name`,
		t.tenantID,
	)
	if err != nil {
		return nil, fmt.Errorf("list personas: %w", err)
	}
	defer rows.Close()
	var out []Persona
	for rows.Next() {
		var p Persona
		var approvedBy, approvedAt sql.NullString
		var createdAt string
		if err := rows.Scan(&p.ID, &p.Name, &p.Status, &p.CreatedByUserID,
			&approvedBy, &approvedAt, &createdAt); err != nil {
			return nil, err
		}
		p.TenantID = t.tenantID
		p.CreatedAt = parseTime(createdAt)
		if approvedBy.Valid {
			v := approvedBy.String
			p.ApprovedByUserID = &v
		}
		if approvedAt.Valid {
			tt := parseTime(approvedAt.String)
			p.ApprovedAt = &tt
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
