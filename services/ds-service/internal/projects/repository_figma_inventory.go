package projects

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// repository_figma_inventory.go — TenantRepo methods for the FIGMA DB
// (migration 0025). Backs the inventory poller in internal/figma/inventory.
//
// Mirrors repository_organism.go conventions:
//   - tenant_id is captured from TenantRepo and injected into every query.
//   - Batch upserts use prepared-statement-inside-tx.
//   - Soft delete via deleted_at: when a project/file/page/section drops
//     out of a crawl response, the row's deleted_at is set to the crawl
//     timestamp. Subsequent appearances clear deleted_at (resurrection).
//
// The poller calls UpsertProjects → SweepProjects, UpsertFiles → SweepFiles,
// etc. — sweep methods mark anything not seen in the current crawl as
// deleted while preserving its row for audit + historical dashboard reads.

// ─── seed list ───────────────────────────────────────────────────────────────

// FigmaTeamSeed is a single admin-added entry in figma_team_seed.
type FigmaTeamSeed struct {
	TenantID         string
	TeamID           string
	TeamName         string
	AddedByUserID    string
	AddedAt          time.Time
	Enabled          bool
	LastCrawlAt      time.Time
	LastCrawlStatus  string
	LastCrawlError   string
}

// UpsertFigmaTeamSeed inserts or updates a team seed. Idempotent on
// (tenant_id, team_id). Enabled defaults to true on insert; on update the
// existing enabled flag is preserved (admin re-uploading the same team
// without explicitly flipping the toggle should not flip enabled).
func (t *TenantRepo) UpsertFigmaTeamSeed(ctx context.Context, seed FigmaTeamSeed) error {
	if t.tenantID == "" {
		return errors.New("projects: tenant_id required")
	}
	if seed.TeamID == "" || strings.TrimSpace(seed.TeamName) == "" {
		return errors.New("projects: team_id and team_name required")
	}
	addedAt := rfc3339(seed.AddedAt.UTC())
	if seed.AddedAt.IsZero() {
		addedAt = rfc3339(t.now().UTC())
	}
	enabled := 1
	if !seed.Enabled && !seed.AddedAt.IsZero() {
		enabled = 0
	}
	_, err := t.handle().ExecContext(ctx, `
		INSERT INTO figma_team_seed (
			tenant_id, team_id, team_name, added_by_user_id, added_at, enabled
		) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(tenant_id, team_id) DO UPDATE SET
			team_name        = excluded.team_name,
			added_by_user_id = excluded.added_by_user_id
	`, t.tenantID, seed.TeamID, seed.TeamName, seed.AddedByUserID, addedAt, enabled)
	if err != nil {
		return fmt.Errorf("upsert figma_team_seed: %w", err)
	}
	return nil
}

// SetFigmaTeamSeedEnabled flips the enabled flag for a team. Used by the
// DELETE admin endpoint (soft-disable; preserves crawled data).
func (t *TenantRepo) SetFigmaTeamSeedEnabled(ctx context.Context, teamID string, enabled bool) error {
	if t.tenantID == "" {
		return errors.New("projects: tenant_id required")
	}
	v := 0
	if enabled {
		v = 1
	}
	_, err := t.handle().ExecContext(ctx,
		`UPDATE figma_team_seed SET enabled = ? WHERE tenant_id = ? AND team_id = ?`,
		v, t.tenantID, teamID)
	return err
}

// MarkFigmaTeamSeedCrawl writes the post-crawl status fields. status is one
// of "ok", "forbidden", "error". errMsg is truncated to 4000 chars.
func (t *TenantRepo) MarkFigmaTeamSeedCrawl(ctx context.Context, teamID, status, errMsg string) error {
	if t.tenantID == "" {
		return errors.New("projects: tenant_id required")
	}
	if len(errMsg) > 4000 {
		errMsg = errMsg[:4000] + "...(truncated)"
	}
	var errArg any
	if errMsg == "" {
		errArg = nil
	} else {
		errArg = errMsg
	}
	_, err := t.handle().ExecContext(ctx, `
		UPDATE figma_team_seed
		   SET last_crawl_at = ?, last_crawl_status = ?, last_crawl_error = ?
		 WHERE tenant_id = ? AND team_id = ?
	`, rfc3339(t.now().UTC()), status, errArg, t.tenantID, teamID)
	return err
}

// ListFigmaTeamSeeds returns every seed for the tenant, enabled or not.
// Ordered by added_at ASC so the admin UI shows oldest-first.
func (t *TenantRepo) ListFigmaTeamSeeds(ctx context.Context) ([]FigmaTeamSeed, error) {
	if t.tenantID == "" {
		return nil, errors.New("projects: tenant_id required")
	}
	rows, err := t.r.db.QueryContext(ctx, `
		SELECT tenant_id, team_id, team_name, added_by_user_id, added_at,
		       enabled,
		       COALESCE(last_crawl_at, ''), COALESCE(last_crawl_status, ''),
		       COALESCE(last_crawl_error, '')
		  FROM figma_team_seed
		 WHERE tenant_id = ?
		 ORDER BY added_at ASC
	`, t.tenantID)
	if err != nil {
		return nil, fmt.Errorf("list figma_team_seed: %w", err)
	}
	defer rows.Close()

	out := make([]FigmaTeamSeed, 0)
	for rows.Next() {
		var s FigmaTeamSeed
		var enabled int
		var addedAt, lastCrawlAt string
		if err := rows.Scan(
			&s.TenantID, &s.TeamID, &s.TeamName, &s.AddedByUserID, &addedAt,
			&enabled, &lastCrawlAt, &s.LastCrawlStatus, &s.LastCrawlError,
		); err != nil {
			return nil, fmt.Errorf("scan figma_team_seed: %w", err)
		}
		s.Enabled = enabled != 0
		s.AddedAt = parseTime(addedAt)
		s.LastCrawlAt = parseTime(lastCrawlAt)
		out = append(out, s)
	}
	return out, rows.Err()
}

// ListEnabledFigmaTeamSeeds returns only enabled seeds. Used by the poller.
// Returns rows across all tenants when t.tenantID is empty — the poller
// loops over tenant IDs and calls this with a tenant-scoped repo per tenant.
func (t *TenantRepo) ListEnabledFigmaTeamSeeds(ctx context.Context) ([]FigmaTeamSeed, error) {
	if t.tenantID == "" {
		return nil, errors.New("projects: tenant_id required")
	}
	rows, err := t.r.db.QueryContext(ctx, `
		SELECT tenant_id, team_id, team_name
		  FROM figma_team_seed
		 WHERE tenant_id = ? AND enabled = 1
		 ORDER BY team_id ASC
	`, t.tenantID)
	if err != nil {
		return nil, fmt.Errorf("list enabled figma_team_seed: %w", err)
	}
	defer rows.Close()
	out := make([]FigmaTeamSeed, 0)
	for rows.Next() {
		var s FigmaTeamSeed
		if err := rows.Scan(&s.TenantID, &s.TeamID, &s.TeamName); err != nil {
			return nil, err
		}
		s.Enabled = true
		out = append(out, s)
	}
	return out, rows.Err()
}

// ─── observed team ───────────────────────────────────────────────────────────

// UpsertFigmaTeam records the team's observed name. Sets first_seen_at on
// insert; only refreshes last_seen_at on conflict.
func (t *TenantRepo) UpsertFigmaTeam(ctx context.Context, teamID, name string) error {
	if t.tenantID == "" {
		return errors.New("projects: tenant_id required")
	}
	now := rfc3339(t.now().UTC())
	_, err := t.handle().ExecContext(ctx, `
		INSERT INTO figma_team (tenant_id, team_id, name, first_seen_at, last_seen_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(tenant_id, team_id) DO UPDATE SET
			name         = excluded.name,
			last_seen_at = excluded.last_seen_at
	`, t.tenantID, teamID, name, now, now)
	return err
}

// ─── projects ────────────────────────────────────────────────────────────────

// FigmaProjectRow is one figma_project row.
type FigmaProjectRow struct {
	TenantID    string
	ProjectID   string
	TeamID      string
	Name        string
	FirstSeenAt time.Time
	LastSeenAt  time.Time
	DeletedAt   time.Time
}

// UpsertFigmaProjects writes the project rows for a team. Idempotent.
// `seenAt` is the crawl timestamp; rows are stamped with last_seen_at = seenAt
// so a subsequent SweepFigmaProjects(teamID, seenAt) call can mark anything
// untouched as deleted in one statement.
func (t *TenantRepo) UpsertFigmaProjects(ctx context.Context, teamID string, projects []FigmaProjectRow, seenAt time.Time) error {
	if t.tenantID == "" {
		return errors.New("projects: tenant_id required")
	}
	if len(projects) == 0 {
		return nil
	}
	now := rfc3339(seenAt.UTC())
	tx, err := t.r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO figma_project (
			tenant_id, project_id, team_id, name, first_seen_at, last_seen_at
		) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(tenant_id, project_id) DO UPDATE SET
			team_id      = excluded.team_id,
			name         = excluded.name,
			last_seen_at = excluded.last_seen_at,
			deleted_at   = NULL
	`)
	if err != nil {
		return fmt.Errorf("prepare upsert figma_project: %w", err)
	}
	defer stmt.Close()

	for _, p := range projects {
		if p.ProjectID == "" {
			continue
		}
		if _, err := stmt.ExecContext(ctx,
			t.tenantID, p.ProjectID, teamID, p.Name, now, now,
		); err != nil {
			return fmt.Errorf("upsert figma_project %s: %w", p.ProjectID, err)
		}
	}
	return tx.Commit()
}

// SweepFigmaProjects soft-deletes any projects under teamID whose last_seen_at
// is older than seenAt (i.e. not touched by the current crawl). Already-deleted
// rows are skipped (no double-stamp).
func (t *TenantRepo) SweepFigmaProjects(ctx context.Context, teamID string, seenAt time.Time) (int64, error) {
	if t.tenantID == "" {
		return 0, errors.New("projects: tenant_id required")
	}
	res, err := t.handle().ExecContext(ctx, `
		UPDATE figma_project
		   SET deleted_at = ?
		 WHERE tenant_id = ? AND team_id = ?
		   AND deleted_at IS NULL
		   AND last_seen_at < ?
	`, rfc3339(seenAt.UTC()), t.tenantID, teamID, rfc3339(seenAt.UTC()))
	if err != nil {
		return 0, fmt.Errorf("sweep figma_project: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// ─── files ───────────────────────────────────────────────────────────────────

// FigmaFileRow mirrors figma_file. Only the fields the cheap /v1/projects
// list endpoint returns are populated by UpsertFigmaFilesShell; the rich
// fields (version, editor_type, etc.) come from a separate Update call
// during the pages-sync pass.
type FigmaFileRow struct {
	TenantID           string
	FileKey            string
	ProjectID          string
	TeamID             string
	Name               string
	ThumbnailURL       string
	LastModified       time.Time
	Version            string
	EditorType         string
	LinkAccess         string
	Role               string
	BranchOfFileKey    string
	PagesLastSyncedAt  time.Time
	PagesSyncVersion   string
	FirstSeenAt        time.Time
	LastSeenAt         time.Time
	DeletedAt          time.Time
}

// UpsertFigmaFilesShell writes/refreshes the cheap fields from
// /v1/projects/<id>/files: name, thumbnail_url, last_modified. Leaves the
// expensive page-sync fields untouched.
func (t *TenantRepo) UpsertFigmaFilesShell(ctx context.Context, projectID, teamID string, files []FigmaFileRow, seenAt time.Time) error {
	if t.tenantID == "" {
		return errors.New("projects: tenant_id required")
	}
	if len(files) == 0 {
		return nil
	}
	now := rfc3339(seenAt.UTC())
	tx, err := t.r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO figma_file (
			tenant_id, file_key, project_id, team_id, name,
			thumbnail_url, last_modified,
			first_seen_at, last_seen_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(tenant_id, file_key) DO UPDATE SET
			project_id    = excluded.project_id,
			team_id       = excluded.team_id,
			name          = excluded.name,
			thumbnail_url = excluded.thumbnail_url,
			last_modified = excluded.last_modified,
			last_seen_at  = excluded.last_seen_at,
			deleted_at    = NULL
	`)
	if err != nil {
		return fmt.Errorf("prepare upsert figma_file shell: %w", err)
	}
	defer stmt.Close()

	for _, f := range files {
		if f.FileKey == "" {
			continue
		}
		var lastMod any
		if !f.LastModified.IsZero() {
			lastMod = rfc3339(f.LastModified.UTC())
		}
		if _, err := stmt.ExecContext(ctx,
			t.tenantID, f.FileKey, projectID, teamID, f.Name,
			nullableStr(f.ThumbnailURL), lastMod,
			now, now,
		); err != nil {
			return fmt.Errorf("upsert figma_file %s: %w", f.FileKey, err)
		}
	}
	return tx.Commit()
}

// SweepFigmaFiles soft-deletes files under projectID not touched by the
// current crawl. Mirrors SweepFigmaProjects.
func (t *TenantRepo) SweepFigmaFiles(ctx context.Context, projectID string, seenAt time.Time) (int64, error) {
	if t.tenantID == "" {
		return 0, errors.New("projects: tenant_id required")
	}
	res, err := t.handle().ExecContext(ctx, `
		UPDATE figma_file
		   SET deleted_at = ?
		 WHERE tenant_id = ? AND project_id = ?
		   AND deleted_at IS NULL
		   AND last_seen_at < ?
	`, rfc3339(seenAt.UTC()), t.tenantID, projectID, rfc3339(seenAt.UTC()))
	if err != nil {
		return 0, fmt.Errorf("sweep figma_file: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// FilesNeedingPagesSync returns file_key + last_modified for every file
// whose pages haven't been synced or whose last_modified is newer than
// pages_sync_version. The poller drains this list through a worker pool.
//
// Excludes deleted files. Limit caps the per-cycle work to keep tier-1
// rate-limit budget bounded.
func (t *TenantRepo) FilesNeedingPagesSync(ctx context.Context, limit int) ([]FigmaFileRow, error) {
	if t.tenantID == "" {
		return nil, errors.New("projects: tenant_id required")
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := t.r.db.QueryContext(ctx, `
		SELECT file_key, project_id, team_id, name,
		       COALESCE(last_modified, ''),
		       COALESCE(version, ''),
		       COALESCE(pages_sync_version, '')
		  FROM figma_file
		 WHERE tenant_id = ?
		   AND deleted_at IS NULL
		   AND (
		         pages_last_synced_at IS NULL
		      OR last_modified IS NULL
		      OR pages_sync_version IS NULL
		      OR pages_sync_version <> COALESCE(version, last_modified)
		   )
		 ORDER BY pages_last_synced_at IS NULL DESC, pages_last_synced_at ASC
		 LIMIT ?
	`, t.tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("files needing pages sync: %w", err)
	}
	defer rows.Close()

	out := make([]FigmaFileRow, 0)
	for rows.Next() {
		var r FigmaFileRow
		var lastMod, version, syncVersion string
		if err := rows.Scan(
			&r.FileKey, &r.ProjectID, &r.TeamID, &r.Name,
			&lastMod, &version, &syncVersion,
		); err != nil {
			return nil, fmt.Errorf("scan files needing sync: %w", err)
		}
		r.LastModified = parseTime(lastMod)
		r.Version = version
		r.PagesSyncVersion = syncVersion
		out = append(out, r)
	}
	return out, rows.Err()
}

// UpdateFigmaFilePagesSynced writes the rich file metadata + marks pages
// as synced at the given timestamp. Called by the poller after a successful
// pages+sections crawl for one file.
func (t *TenantRepo) UpdateFigmaFilePagesSynced(ctx context.Context, f FigmaFileRow) error {
	if t.tenantID == "" {
		return errors.New("projects: tenant_id required")
	}
	if f.FileKey == "" {
		return errors.New("projects: file_key required")
	}
	var lastMod any
	if !f.LastModified.IsZero() {
		lastMod = rfc3339(f.LastModified.UTC())
	}
	syncedAt := rfc3339(f.PagesLastSyncedAt.UTC())
	if f.PagesLastSyncedAt.IsZero() {
		syncedAt = rfc3339(t.now().UTC())
	}
	_, err := t.handle().ExecContext(ctx, `
		UPDATE figma_file
		   SET name                  = COALESCE(NULLIF(?, ''), name),
		       thumbnail_url         = COALESCE(NULLIF(?, ''), thumbnail_url),
		       last_modified         = COALESCE(?, last_modified),
		       version               = NULLIF(?, ''),
		       editor_type           = NULLIF(?, ''),
		       link_access           = NULLIF(?, ''),
		       role                  = NULLIF(?, ''),
		       branch_of_file_key    = NULLIF(?, ''),
		       pages_last_synced_at  = ?,
		       pages_sync_version    = NULLIF(?, '')
		 WHERE tenant_id = ? AND file_key = ?
	`,
		f.Name, f.ThumbnailURL, lastMod,
		f.Version, f.EditorType, f.LinkAccess, f.Role, f.BranchOfFileKey,
		syncedAt, f.PagesSyncVersion,
		t.tenantID, f.FileKey,
	)
	if err != nil {
		return fmt.Errorf("update figma_file pages-synced: %w", err)
	}
	return nil
}

// ─── pages ───────────────────────────────────────────────────────────────────

// FigmaPageRow is one figma_page row.
type FigmaPageRow struct {
	TenantID           string
	FileKey            string
	PageID             string
	Name               string
	OrderIndex         int
	BackgroundColorHex string
}

// UpsertFigmaPagesAndSections is the page+section batch write for one file.
// It opens one tx that:
//   1. upserts every page with last_seen_at = seenAt
//   2. upserts every section with last_seen_at = seenAt
//   3. soft-deletes pages under (file_key) not touched by this crawl
//   4. soft-deletes sections under (file_key) not touched by this crawl
//
// One tx so the file-level state stays consistent — partial failures
// don't leave half-synced rows.
//
// Returns (pagesUpserted, sectionsUpserted, error).
func (t *TenantRepo) UpsertFigmaPagesAndSections(
	ctx context.Context,
	fileKey string,
	pages []FigmaPageRow,
	sections []FigmaSectionRow,
	seenAt time.Time,
) (int, int, error) {
	if t.tenantID == "" {
		return 0, 0, errors.New("projects: tenant_id required")
	}
	if fileKey == "" {
		return 0, 0, errors.New("projects: file_key required")
	}
	now := rfc3339(seenAt.UTC())
	tx, err := t.r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()

	// pages
	pageStmt, err := tx.PrepareContext(ctx, `
		INSERT INTO figma_page (
			tenant_id, file_key, page_id, name, order_index, background_color,
			first_seen_at, last_seen_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(tenant_id, file_key, page_id) DO UPDATE SET
			name             = excluded.name,
			order_index      = excluded.order_index,
			background_color = excluded.background_color,
			last_seen_at     = excluded.last_seen_at,
			deleted_at       = NULL
	`)
	if err != nil {
		return 0, 0, fmt.Errorf("prepare upsert figma_page: %w", err)
	}
	defer pageStmt.Close()

	for _, p := range pages {
		if p.PageID == "" {
			continue
		}
		if _, err := pageStmt.ExecContext(ctx,
			t.tenantID, fileKey, p.PageID, p.Name, p.OrderIndex,
			nullableStr(p.BackgroundColorHex), now, now,
		); err != nil {
			return 0, 0, fmt.Errorf("upsert figma_page %s: %w", p.PageID, err)
		}
	}

	// sections
	sectionStmt, err := tx.PrepareContext(ctx, `
		INSERT INTO figma_section (
			tenant_id, file_key, page_id, section_id, name,
			x, y, width, height, order_index,
			first_seen_at, last_seen_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(tenant_id, file_key, page_id, section_id) DO UPDATE SET
			name         = excluded.name,
			x            = excluded.x,
			y            = excluded.y,
			width        = excluded.width,
			height       = excluded.height,
			order_index  = excluded.order_index,
			last_seen_at = excluded.last_seen_at,
			deleted_at   = NULL
	`)
	if err != nil {
		return 0, 0, fmt.Errorf("prepare upsert figma_section: %w", err)
	}
	defer sectionStmt.Close()

	for _, s := range sections {
		if s.SectionID == "" {
			continue
		}
		if _, err := sectionStmt.ExecContext(ctx,
			t.tenantID, fileKey, s.PageID, s.SectionID, s.Name,
			s.X, s.Y, s.Width, s.Height, s.OrderIndex,
			now, now,
		); err != nil {
			return 0, 0, fmt.Errorf("upsert figma_section %s/%s: %w", s.PageID, s.SectionID, err)
		}
	}

	// sweep within this file
	if _, err := tx.ExecContext(ctx, `
		UPDATE figma_page
		   SET deleted_at = ?
		 WHERE tenant_id = ? AND file_key = ?
		   AND deleted_at IS NULL
		   AND last_seen_at < ?
	`, now, t.tenantID, fileKey, now); err != nil {
		return 0, 0, fmt.Errorf("sweep figma_page: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE figma_section
		   SET deleted_at = ?
		 WHERE tenant_id = ? AND file_key = ?
		   AND deleted_at IS NULL
		   AND last_seen_at < ?
	`, now, t.tenantID, fileKey, now); err != nil {
		return 0, 0, fmt.Errorf("sweep figma_section: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	return len(pages), len(sections), nil
}

// FigmaSectionRow is one figma_section row.
type FigmaSectionRow struct {
	TenantID   string
	FileKey    string
	PageID     string
	SectionID  string
	Name       string
	X          float64
	Y          float64
	Width      float64
	Height     float64
	OrderIndex int
}

// ─── single-file lookups (powers the Promote endpoint, U5) ──────────────────

// LookupFigmaFile returns one figma_file row keyed on file_key. Excludes
// soft-deleted rows by default — callers wanting to surface a deleted
// file should pass includeDeleted=true. Returns ErrNotFound when the
// file isn't in the tenant's inventory.
func (t *TenantRepo) LookupFigmaFile(ctx context.Context, fileKey string, includeDeleted bool) (FigmaFileRow, error) {
	if t.tenantID == "" {
		return FigmaFileRow{}, errors.New("projects: tenant_id required")
	}
	if fileKey == "" {
		return FigmaFileRow{}, errors.New("projects: file_key required")
	}
	deletedClause := " AND deleted_at IS NULL"
	if includeDeleted {
		deletedClause = ""
	}
	row := t.r.db.QueryRowContext(ctx, `
		SELECT file_key, project_id, team_id, name,
		       COALESCE(thumbnail_url, ''),
		       COALESCE(last_modified, ''),
		       COALESCE(version, ''),
		       COALESCE(editor_type, ''),
		       COALESCE(link_access, ''),
		       COALESCE(role, ''),
		       COALESCE(branch_of_file_key, ''),
		       COALESCE(pages_last_synced_at, ''),
		       COALESCE(pages_sync_version, ''),
		       first_seen_at, last_seen_at, COALESCE(deleted_at, '')
		  FROM figma_file
		 WHERE tenant_id = ? AND file_key = ?`+deletedClause, t.tenantID, fileKey)
	var r FigmaFileRow
	var lastMod, pagesSyncedAt, firstSeen, lastSeen, deletedAt string
	err := row.Scan(
		&r.FileKey, &r.ProjectID, &r.TeamID, &r.Name,
		&r.ThumbnailURL, &lastMod, &r.Version, &r.EditorType,
		&r.LinkAccess, &r.Role, &r.BranchOfFileKey,
		&pagesSyncedAt, &r.PagesSyncVersion,
		&firstSeen, &lastSeen, &deletedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return FigmaFileRow{}, ErrNotFound
	}
	if err != nil {
		return FigmaFileRow{}, fmt.Errorf("lookup figma_file: %w", err)
	}
	r.TenantID = t.tenantID
	r.LastModified = parseTime(lastMod)
	r.PagesLastSyncedAt = parseTime(pagesSyncedAt)
	r.FirstSeenAt = parseTime(firstSeen)
	r.LastSeenAt = parseTime(lastSeen)
	r.DeletedAt = parseTime(deletedAt)
	return r, nil
}

// LookupFigmaProject returns one figma_project row keyed on project_id.
// Used by the promote handler to derive a Product label from the Figma
// project name. Excludes soft-deleted rows.
func (t *TenantRepo) LookupFigmaProject(ctx context.Context, projectID string) (FigmaProjectRow, error) {
	if t.tenantID == "" {
		return FigmaProjectRow{}, errors.New("projects: tenant_id required")
	}
	if projectID == "" {
		return FigmaProjectRow{}, errors.New("projects: project_id required")
	}
	row := t.r.db.QueryRowContext(ctx, `
		SELECT project_id, team_id, name, first_seen_at, last_seen_at
		  FROM figma_project
		 WHERE tenant_id = ? AND project_id = ? AND deleted_at IS NULL
	`, t.tenantID, projectID)
	var r FigmaProjectRow
	var firstSeen, lastSeen string
	err := row.Scan(&r.ProjectID, &r.TeamID, &r.Name, &firstSeen, &lastSeen)
	if errors.Is(err, sql.ErrNoRows) {
		return FigmaProjectRow{}, ErrNotFound
	}
	if err != nil {
		return FigmaProjectRow{}, fmt.Errorf("lookup figma_project: %w", err)
	}
	r.TenantID = t.tenantID
	r.FirstSeenAt = parseTime(firstSeen)
	r.LastSeenAt = parseTime(lastSeen)
	return r, nil
}

// LookupProjectByFileKey returns the DS-internal projects row already
// linked to this file_key, if one exists. Powers the linkage badge on
// the inventory tree (U7) and the idempotency check inside the promote
// endpoint (U5). Returns ErrNotFound when no link exists yet.
//
// The lookup uses the existing partial unique index on
// `projects(tenant_id, file_id) WHERE deleted_at IS NULL` so the read
// stays index-scan cheap.
func (t *TenantRepo) LookupProjectByFileKey(ctx context.Context, fileKey string) (Project, error) {
	if t.tenantID == "" {
		return Project{}, errors.New("projects: tenant_id required")
	}
	if fileKey == "" {
		return Project{}, errors.New("projects: file_key required")
	}
	row := t.r.db.QueryRowContext(ctx, `
		SELECT id, slug, name, platform, product, path,
		       COALESCE(file_id, ''), owner_user_id, created_at, updated_at
		  FROM projects
		 WHERE tenant_id = ? AND file_id = ? AND deleted_at IS NULL
	`, t.tenantID, fileKey)
	var p Project
	var createdAt, updatedAt string
	err := row.Scan(&p.ID, &p.Slug, &p.Name, &p.Platform, &p.Product, &p.Path,
		&p.FileID, &p.OwnerUserID, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Project{}, ErrNotFound
	}
	if err != nil {
		return Project{}, fmt.Errorf("lookup project by file_key: %w", err)
	}
	p.TenantID = t.tenantID
	p.CreatedAt = parseTime(createdAt)
	p.UpdatedAt = parseTime(updatedAt)
	return p, nil
}

// ProjectFileKeysForTenant returns the set of file_keys currently linked
// to a DS-internal projects row for this tenant (deleted projects
// excluded). Used by GetFigmaInventoryTree (U7) to surface linkage
// state on file nodes in a single batch read rather than one query per
// file. Returns map[file_key]Project so the tree builder can stitch in
// both project_id and slug.
func (t *TenantRepo) ProjectFileKeysForTenant(ctx context.Context) (map[string]Project, error) {
	if t.tenantID == "" {
		return nil, errors.New("projects: tenant_id required")
	}
	rows, err := t.r.db.QueryContext(ctx, `
		SELECT id, slug, name, platform, product, path, file_id
		  FROM projects
		 WHERE tenant_id = ? AND file_id IS NOT NULL AND deleted_at IS NULL
	`, t.tenantID)
	if err != nil {
		return nil, fmt.Errorf("list linked projects: %w", err)
	}
	defer rows.Close()
	out := make(map[string]Project, 32)
	for rows.Next() {
		var p Project
		var fileID sql.NullString
		if err := rows.Scan(&p.ID, &p.Slug, &p.Name, &p.Platform, &p.Product, &p.Path, &fileID); err != nil {
			return nil, fmt.Errorf("scan linked project: %w", err)
		}
		if !fileID.Valid || fileID.String == "" {
			continue
		}
		p.TenantID = t.tenantID
		p.FileID = fileID.String
		out[fileID.String] = p
	}
	return out, rows.Err()
}

// ─── deep node tree (Phase 2C — figma_node) ──────────────────────────────────

// FigmaNodeRow mirrors one figma_node row. Built by the poller from a
// FlatNode (internal/figma/client) and passed to UpsertFigmaNodes in
// big batches.
type FigmaNodeRow struct {
	TenantID     string
	FileKey      string
	NodeID       string
	ParentID     string  // empty for the document root
	NodeType     string
	Name         string
	HasBBox      bool
	X            float64
	Y            float64
	Width        float64
	Height       float64
	Depth        int
	OrderIndex   int
	ComponentID  string
	ComponentKey string
}

// UpsertFigmaNodes batches the whole flat node list for one file into a
// single transaction: prepared INSERT with ON CONFLICT, then a sweep
// stamping deleted_at on rows the current crawl didn't touch.
//
// Returns the number of node rows upserted. Empty list is allowed —
// it'll just run the sweep (which marks every node as deleted if the
// file emptied out, e.g. after a FILE_DELETE webhook).
func (t *TenantRepo) UpsertFigmaNodes(ctx context.Context, fileKey string, nodes []FigmaNodeRow, seenAt time.Time) (int, error) {
	if t.tenantID == "" {
		return 0, errors.New("projects: tenant_id required")
	}
	if fileKey == "" {
		return 0, errors.New("projects: file_key required")
	}
	now := rfc3339(seenAt.UTC())
	tx, err := t.r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO figma_node (
			tenant_id, file_key, node_id, parent_id, node_type, name,
			x, y, width, height, depth, order_index,
			component_id, component_key,
			first_seen_at, last_seen_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(tenant_id, file_key, node_id) DO UPDATE SET
			parent_id     = excluded.parent_id,
			node_type     = excluded.node_type,
			name          = excluded.name,
			x             = excluded.x,
			y             = excluded.y,
			width         = excluded.width,
			height        = excluded.height,
			depth         = excluded.depth,
			order_index   = excluded.order_index,
			component_id  = excluded.component_id,
			component_key = excluded.component_key,
			last_seen_at  = excluded.last_seen_at,
			deleted_at    = NULL
	`)
	if err != nil {
		return 0, fmt.Errorf("prepare upsert figma_node: %w", err)
	}
	defer stmt.Close()

	upserted := 0
	for _, n := range nodes {
		if n.NodeID == "" {
			continue
		}
		// Nullable column args — empty string maps to SQL NULL so the
		// component_key partial index stays correct.
		var xArg, yArg, wArg, hArg any
		if n.HasBBox {
			xArg, yArg, wArg, hArg = n.X, n.Y, n.Width, n.Height
		}
		if _, err := stmt.ExecContext(ctx,
			t.tenantID, fileKey, n.NodeID,
			nullableStr(n.ParentID), n.NodeType, n.Name,
			xArg, yArg, wArg, hArg,
			n.Depth, n.OrderIndex,
			nullableStr(n.ComponentID), nullableStr(n.ComponentKey),
			now, now,
		); err != nil {
			return 0, fmt.Errorf("upsert figma_node %s: %w", n.NodeID, err)
		}
		upserted++
	}

	// sweep — anything in this file not touched this cycle gets
	// deleted_at stamped. Same single-tx pattern as
	// UpsertFigmaPagesAndSections so partial failures don't leave
	// half-synced state.
	if _, err := tx.ExecContext(ctx, `
		UPDATE figma_node
		   SET deleted_at = ?
		 WHERE tenant_id = ? AND file_key = ?
		   AND deleted_at IS NULL
		   AND last_seen_at < ?
	`, now, t.tenantID, fileKey, now); err != nil {
		return 0, fmt.Errorf("sweep figma_node: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return upserted, nil
}

// UpdateFigmaFileDeepSynced writes the deep-sync state onto figma_file
// (deep_synced_at, deep_sync_version, node_count). Called by the poller
// after a successful UpsertFigmaNodes.
func (t *TenantRepo) UpdateFigmaFileDeepSynced(ctx context.Context, fileKey, version string, nodeCount int, syncedAt time.Time) error {
	if t.tenantID == "" {
		return errors.New("projects: tenant_id required")
	}
	if fileKey == "" {
		return errors.New("projects: file_key required")
	}
	_, err := t.handle().ExecContext(ctx, `
		UPDATE figma_file
		   SET deep_synced_at    = ?,
		       deep_sync_version = NULLIF(?, ''),
		       node_count        = ?
		 WHERE tenant_id = ? AND file_key = ?
	`, rfc3339(syncedAt.UTC()), version, nodeCount, t.tenantID, fileKey)
	if err != nil {
		return fmt.Errorf("update figma_file deep-synced: %w", err)
	}
	return nil
}

// FilesNeedingDeepSync mirrors FilesNeedingPagesSync but uses the
// deep_synced_at / deep_sync_version columns. Returns file rows whose
// deep tree hasn't been fetched yet or whose Figma `version` moved
// since the last successful deep sync.
//
// limit caps per-cycle work so the tier-1 budget stays bounded.
func (t *TenantRepo) FilesNeedingDeepSync(ctx context.Context, limit int) ([]FigmaFileRow, error) {
	if t.tenantID == "" {
		return nil, errors.New("projects: tenant_id required")
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := t.r.db.QueryContext(ctx, `
		SELECT file_key, project_id, team_id, name,
		       COALESCE(last_modified, ''),
		       COALESCE(version, ''),
		       COALESCE(deep_sync_version, '')
		  FROM figma_file
		 WHERE tenant_id = ?
		   AND deleted_at IS NULL
		   AND (
		         deep_synced_at IS NULL
		      OR deep_sync_version IS NULL
		      OR deep_sync_version <> COALESCE(version, last_modified)
		   )
		 ORDER BY deep_synced_at IS NULL DESC, deep_synced_at ASC
		 LIMIT ?
	`, t.tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("files needing deep sync: %w", err)
	}
	defer rows.Close()

	out := make([]FigmaFileRow, 0)
	for rows.Next() {
		var r FigmaFileRow
		var lastMod, version, deepVersion string
		if err := rows.Scan(
			&r.FileKey, &r.ProjectID, &r.TeamID, &r.Name,
			&lastMod, &version, &deepVersion,
		); err != nil {
			return nil, fmt.Errorf("scan files needing deep sync: %w", err)
		}
		r.LastModified = parseTime(lastMod)
		r.Version = version
		out = append(out, r)
	}
	return out, rows.Err()
}

// FigmaNodeView is the trimmed row shape returned to the admin UI.
type FigmaNodeView struct {
	NodeID       string  `json:"node_id"`
	ParentID     string  `json:"parent_id,omitempty"`
	NodeType     string  `json:"node_type"`
	Name         string  `json:"name"`
	HasBBox      bool    `json:"has_bbox"`
	X            float64 `json:"x,omitempty"`
	Y            float64 `json:"y,omitempty"`
	Width        float64 `json:"width,omitempty"`
	Height       float64 `json:"height,omitempty"`
	Depth        int     `json:"depth"`
	OrderIndex   int     `json:"order_index"`
	ComponentID  string  `json:"component_id,omitempty"`
	ComponentKey string  `json:"component_key,omitempty"`
}

// ListFigmaNodesForFile returns every live node row in a file ordered
// depth-first (depth ASC, order_index ASC). Caps at limit rows so a
// 19k-node file doesn't blow up the admin UI. The UI can paginate or
// filter for cases with too many nodes.
func (t *TenantRepo) ListFigmaNodesForFile(ctx context.Context, fileKey string, limit int) ([]FigmaNodeView, error) {
	if t.tenantID == "" {
		return nil, errors.New("projects: tenant_id required")
	}
	if fileKey == "" {
		return nil, errors.New("projects: file_key required")
	}
	if limit <= 0 {
		limit = 2000
	}
	rows, err := t.r.db.QueryContext(ctx, `
		SELECT node_id, COALESCE(parent_id, ''), node_type, name,
		       x, y, width, height,
		       depth, order_index,
		       COALESCE(component_id, ''), COALESCE(component_key, '')
		  FROM figma_node
		 WHERE tenant_id = ? AND file_key = ? AND deleted_at IS NULL
		 ORDER BY depth ASC, order_index ASC, node_id ASC
		 LIMIT ?
	`, t.tenantID, fileKey, limit)
	if err != nil {
		return nil, fmt.Errorf("list figma_node: %w", err)
	}
	defer rows.Close()
	out := make([]FigmaNodeView, 0, 256)
	for rows.Next() {
		var v FigmaNodeView
		var x, y, w, h sql.NullFloat64
		if err := rows.Scan(&v.NodeID, &v.ParentID, &v.NodeType, &v.Name,
			&x, &y, &w, &h,
			&v.Depth, &v.OrderIndex,
			&v.ComponentID, &v.ComponentKey,
		); err != nil {
			return nil, fmt.Errorf("scan figma_node: %w", err)
		}
		if x.Valid {
			v.HasBBox = true
			v.X = x.Float64
			v.Y = y.Float64
			v.Width = w.Float64
			v.Height = h.Float64
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ─── cross-file component usage (Phase 2C analytic) ─────────────────────────

// ComponentUsageRow summarises one master component's reach across the
// tenant: how many files use it, how many total instances exist, and
// where the master itself lives if it's in our inventory. The (master_
// file_key, master_node_id) pair is empty when the master is in a
// library file we haven't crawled yet — the durable component_key on
// every INSTANCE is still enough to count usage.
type ComponentUsageRow struct {
	ComponentKey    string `json:"component_key"`
	MasterName      string `json:"master_name,omitempty"`
	MasterFileKey   string `json:"master_file_key,omitempty"`
	MasterNodeID    string `json:"master_node_id,omitempty"`
	FilesUsing      int    `json:"files_using"`
	TotalInstances  int    `json:"total_instances"`
}

// ListComponentUsage returns top-N master components ranked by total
// instance count. The query joins INSTANCE rows by component_key (the
// durable library identifier the walker stamped onto every INSTANCE
// at deep-fetch time) — works the same whether the master is local or
// remote.
//
// LEFT JOIN against figma_node masters surfaces the master's own
// location when it's in the inventory; library masters we haven't
// crawled still appear in the list with empty master_* fields.
func (t *TenantRepo) ListComponentUsage(ctx context.Context, limit int) ([]ComponentUsageRow, error) {
	if t.tenantID == "" {
		return nil, errors.New("projects: tenant_id required")
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := t.r.db.QueryContext(ctx, `
		SELECT
		    i.component_key,
		    COALESCE(MAX(m.name), ''),
		    COALESCE(MAX(m.file_key), ''),
		    COALESCE(MAX(m.node_id), ''),
		    COUNT(DISTINCT i.file_key) AS files_using,
		    COUNT(*) AS total_instances
		  FROM figma_node i
		  LEFT JOIN figma_node m
		         ON m.tenant_id = i.tenant_id
		        AND m.component_key = i.component_key
		        AND m.node_type IN ('COMPONENT','COMPONENT_SET')
		        AND m.deleted_at IS NULL
		 WHERE i.tenant_id = ?
		   AND i.node_type = 'INSTANCE'
		   AND i.component_key IS NOT NULL
		   AND i.deleted_at IS NULL
		 GROUP BY i.component_key
		 ORDER BY total_instances DESC
		 LIMIT ?
	`, t.tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("list component usage: %w", err)
	}
	defer rows.Close()
	out := make([]ComponentUsageRow, 0, limit)
	for rows.Next() {
		var r ComponentUsageRow
		if err := rows.Scan(
			&r.ComponentKey, &r.MasterName, &r.MasterFileKey, &r.MasterNodeID,
			&r.FilesUsing, &r.TotalInstances,
		); err != nil {
			return nil, fmt.Errorf("scan component usage: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ComponentUsageDetail is one row of "where is this component used".
type ComponentUsageDetail struct {
	FileKey      string `json:"file_key"`
	FileName     string `json:"file_name"`
	ProjectName  string `json:"project_name"`
	InstanceNodeID string `json:"instance_node_id"`
	InstanceName string `json:"instance_name"`
	ParentID     string `json:"parent_id,omitempty"`
	Depth        int    `json:"depth"`
}

// ListComponentUsageDetail returns every INSTANCE referencing the
// given component_key, grouped by (file, project). Powers the drill-in
// view: "show me the 1,347 places where button-primary lives."
func (t *TenantRepo) ListComponentUsageDetail(ctx context.Context, componentKey string, limit int) ([]ComponentUsageDetail, error) {
	if t.tenantID == "" {
		return nil, errors.New("projects: tenant_id required")
	}
	if componentKey == "" {
		return nil, errors.New("projects: component_key required")
	}
	if limit <= 0 {
		limit = 500
	}
	rows, err := t.r.db.QueryContext(ctx, `
		SELECT
		    i.file_key,
		    COALESCE(f.name, ''),
		    COALESCE(p.name, ''),
		    i.node_id, i.name, COALESCE(i.parent_id, ''), i.depth
		  FROM figma_node i
		  LEFT JOIN figma_file f
		         ON f.tenant_id = i.tenant_id AND f.file_key = i.file_key
		  LEFT JOIN figma_project p
		         ON p.tenant_id = f.tenant_id AND p.project_id = f.project_id
		 WHERE i.tenant_id = ?
		   AND i.node_type = 'INSTANCE'
		   AND i.component_key = ?
		   AND i.deleted_at IS NULL
		 ORDER BY p.name ASC, f.name ASC, i.depth ASC
		 LIMIT ?
	`, t.tenantID, componentKey, limit)
	if err != nil {
		return nil, fmt.Errorf("list component usage detail: %w", err)
	}
	defer rows.Close()
	out := make([]ComponentUsageDetail, 0, 64)
	for rows.Next() {
		var r ComponentUsageDetail
		if err := rows.Scan(
			&r.FileKey, &r.FileName, &r.ProjectName,
			&r.InstanceNodeID, &r.InstanceName, &r.ParentID, &r.Depth,
		); err != nil {
			return nil, fmt.Errorf("scan component usage detail: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CountFigmaNodesByFile returns a map[file_key]int of how many live
// (non-deleted) nodes each file currently has. Powers the inventory
// admin UI's per-file node-count badge without forcing the tree
// endpoint to load every node row.
func (t *TenantRepo) CountFigmaNodesByFile(ctx context.Context) (map[string]int, error) {
	if t.tenantID == "" {
		return nil, errors.New("projects: tenant_id required")
	}
	rows, err := t.r.db.QueryContext(ctx, `
		SELECT file_key, COUNT(*)
		  FROM figma_node
		 WHERE tenant_id = ? AND deleted_at IS NULL
		 GROUP BY file_key
	`, t.tenantID)
	if err != nil {
		return nil, fmt.Errorf("count figma_node by file: %w", err)
	}
	defer rows.Close()
	out := make(map[string]int, 64)
	for rows.Next() {
		var fk string
		var n int
		if err := rows.Scan(&fk, &n); err != nil {
			return nil, err
		}
		out[fk] = n
	}
	return out, rows.Err()
}

// ─── tree read for admin UI ──────────────────────────────────────────────────

// FigmaInventoryTreeNode is a generic tree node returned to the admin UI.
// One shape covers team/project/file/page/section to keep the JSON simple
// for the frontend to walk recursively.
type FigmaInventoryTreeNode struct {
	Kind         string                    `json:"kind"` // team|project|file|page|section
	ID           string                    `json:"id"`
	Name         string                    `json:"name"`
	X            *float64                  `json:"x,omitempty"`
	Y            *float64                  `json:"y,omitempty"`
	Width        *float64                  `json:"width,omitempty"`
	Height       *float64                  `json:"height,omitempty"`
	LastModified string                    `json:"last_modified,omitempty"`
	ThumbnailURL string                    `json:"thumbnail_url,omitempty"`
	DeletedAt    string                    `json:"deleted_at,omitempty"`
	// U7 — set on `file` nodes when this file_key has been promoted to a
	// DS-internal projects row. Empty on non-file nodes and on unlinked
	// files. The tree builder fetches all linked file_keys for the tenant
	// in one batch query before stitching, so per-file cost stays flat.
	LinkedProjectID   string                    `json:"linked_project_id,omitempty"`
	LinkedProjectSlug string                    `json:"linked_project_slug,omitempty"`
	// Phase 2C — deep node-tree mirror stats (set on `file` nodes only).
	NodeCount      int    `json:"node_count,omitempty"`
	DeepSyncedAt   string `json:"deep_synced_at,omitempty"`
	Children       []*FigmaInventoryTreeNode `json:"children,omitempty"`
}

// GetFigmaInventoryTree returns the full team>project>file>page>section
// tree for one team. Includes soft-deleted rows when includeDeleted is true.
//
// Reads in 5 queries (one per layer) and stitches in Go to keep SQL simple
// and the per-layer ordering deterministic. Tree sizes are bounded by the
// design-system corpus (single-digit teams, low-tens projects, ~100 files,
// ~5 pages/file, ~5 sections/page) so the all-in-memory shape is fine.
func (t *TenantRepo) GetFigmaInventoryTree(ctx context.Context, teamID string, includeDeleted bool) (*FigmaInventoryTreeNode, error) {
	if t.tenantID == "" {
		return nil, errors.New("projects: tenant_id required")
	}
	if teamID == "" {
		return nil, errors.New("projects: team_id required")
	}
	deletedClause := " AND deleted_at IS NULL"
	if includeDeleted {
		deletedClause = ""
	}

	// team
	teamRow := t.r.db.QueryRowContext(ctx, `
		SELECT name FROM figma_team WHERE tenant_id = ? AND team_id = ?
	`, t.tenantID, teamID)
	var teamName string
	if err := teamRow.Scan(&teamName); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scan figma_team: %w", err)
	}
	root := &FigmaInventoryTreeNode{Kind: "team", ID: teamID, Name: teamName}

	// projects
	projectsByID := map[string]*FigmaInventoryTreeNode{}
	pRows, err := t.r.db.QueryContext(ctx, `
		SELECT project_id, name, COALESCE(deleted_at, '')
		  FROM figma_project
		 WHERE tenant_id = ? AND team_id = ?`+deletedClause+`
		 ORDER BY name ASC
	`, t.tenantID, teamID)
	if err != nil {
		return nil, fmt.Errorf("list figma_project: %w", err)
	}
	for pRows.Next() {
		n := &FigmaInventoryTreeNode{Kind: "project"}
		if err := pRows.Scan(&n.ID, &n.Name, &n.DeletedAt); err != nil {
			pRows.Close()
			return nil, fmt.Errorf("scan figma_project: %w", err)
		}
		projectsByID[n.ID] = n
		root.Children = append(root.Children, n)
	}
	pRows.Close()
	if err := pRows.Err(); err != nil {
		return nil, err
	}

	// linked-project lookup (U7) — one batch query per tree fetch.
	// Excludes deleted projects via the existing partial unique index
	// `projects(tenant_id, file_id) WHERE deleted_at IS NULL`. Returns
	// map[file_key]Project for O(1) stitch in the file loop below.
	linkedByFileKey, err := t.ProjectFileKeysForTenant(ctx)
	if err != nil {
		// Non-fatal — surface tree without linkage rather than failing
		// the whole admin page on a linkage query hiccup.
		linkedByFileKey = map[string]Project{}
	}

	// files
	filesByKey := map[string]*FigmaInventoryTreeNode{}
	fileTeamScopeFilter := " AND team_id = ?"
	fRows, err := t.r.db.QueryContext(ctx, `
		SELECT file_key, project_id, name, COALESCE(thumbnail_url, ''),
		       COALESCE(last_modified, ''), COALESCE(deleted_at, ''),
		       COALESCE(node_count, 0), COALESCE(deep_synced_at, '')
		  FROM figma_file
		 WHERE tenant_id = ?`+fileTeamScopeFilter+deletedClause+`
		 ORDER BY name ASC
	`, t.tenantID, teamID)
	if err != nil {
		return nil, fmt.Errorf("list figma_file: %w", err)
	}
	for fRows.Next() {
		n := &FigmaInventoryTreeNode{Kind: "file"}
		var projectID string
		if err := fRows.Scan(&n.ID, &projectID, &n.Name, &n.ThumbnailURL,
			&n.LastModified, &n.DeletedAt,
			&n.NodeCount, &n.DeepSyncedAt); err != nil {
			fRows.Close()
			return nil, fmt.Errorf("scan figma_file: %w", err)
		}
		if linked, ok := linkedByFileKey[n.ID]; ok {
			n.LinkedProjectID = linked.ID
			n.LinkedProjectSlug = linked.Slug
		}
		filesByKey[n.ID] = n
		if parent, ok := projectsByID[projectID]; ok {
			parent.Children = append(parent.Children, n)
		}
	}
	fRows.Close()
	if err := fRows.Err(); err != nil {
		return nil, err
	}
	if len(filesByKey) == 0 {
		return root, nil
	}

	// pages — fetch every page under any file in filesByKey
	fileKeys := make([]string, 0, len(filesByKey))
	for k := range filesByKey {
		fileKeys = append(fileKeys, k)
	}
	pageInClause, pageArgs := inClause(fileKeys)
	pagesByFK := map[string]map[string]*FigmaInventoryTreeNode{}
	gRows, err := t.r.db.QueryContext(ctx, `
		SELECT file_key, page_id, name, order_index, COALESCE(deleted_at, '')
		  FROM figma_page
		 WHERE tenant_id = ? AND file_key IN (`+pageInClause+`)`+deletedClause+`
		 ORDER BY file_key ASC, order_index ASC, page_id ASC
	`, append([]any{t.tenantID}, pageArgs...)...)
	if err != nil {
		return nil, fmt.Errorf("list figma_page: %w", err)
	}
	for gRows.Next() {
		n := &FigmaInventoryTreeNode{Kind: "page"}
		var fileKey string
		var orderIndex int
		if err := gRows.Scan(&fileKey, &n.ID, &n.Name, &orderIndex, &n.DeletedAt); err != nil {
			gRows.Close()
			return nil, fmt.Errorf("scan figma_page: %w", err)
		}
		if fileNode, ok := filesByKey[fileKey]; ok {
			fileNode.Children = append(fileNode.Children, n)
		}
		if _, ok := pagesByFK[fileKey]; !ok {
			pagesByFK[fileKey] = map[string]*FigmaInventoryTreeNode{}
		}
		pagesByFK[fileKey][n.ID] = n
	}
	gRows.Close()
	if err := gRows.Err(); err != nil {
		return nil, err
	}

	// sections
	sRows, err := t.r.db.QueryContext(ctx, `
		SELECT file_key, page_id, section_id, name, x, y, width, height,
		       order_index, COALESCE(deleted_at, '')
		  FROM figma_section
		 WHERE tenant_id = ? AND file_key IN (`+pageInClause+`)`+deletedClause+`
		 ORDER BY file_key ASC, page_id ASC, order_index ASC, section_id ASC
	`, append([]any{t.tenantID}, pageArgs...)...)
	if err != nil {
		return nil, fmt.Errorf("list figma_section: %w", err)
	}
	for sRows.Next() {
		n := &FigmaInventoryTreeNode{Kind: "section"}
		var fileKey, pageID string
		var x, y, w, h float64
		var orderIndex int
		if err := sRows.Scan(&fileKey, &pageID, &n.ID, &n.Name,
			&x, &y, &w, &h, &orderIndex, &n.DeletedAt); err != nil {
			sRows.Close()
			return nil, fmt.Errorf("scan figma_section: %w", err)
		}
		n.X, n.Y, n.Width, n.Height = &x, &y, &w, &h
		if pages, ok := pagesByFK[fileKey]; ok {
			if parent, ok := pages[pageID]; ok {
				parent.Children = append(parent.Children, n)
			}
		}
	}
	sRows.Close()
	return root, sRows.Err()
}

// ─── inventory runs ──────────────────────────────────────────────────────────

// FigmaInventoryRunRow is one figma_inventory_run row.
type FigmaInventoryRunRow struct {
	ID              int64
	TenantID        string
	StartedAt       time.Time
	FinishedAt      time.Time
	TeamsCrawled    int
	ProjectsSeen    int
	FilesSeen       int
	FilesRefetched  int
	PagesUpserted   int
	SectionsUpserted int
	NodesUpserted   int // Phase 2C — figma_node rows written this cycle
	ErrorCount      int
	ErrorSampleJSON string
}

// StartFigmaInventoryRun inserts a "started" run row and returns its id.
func (t *TenantRepo) StartFigmaInventoryRun(ctx context.Context, startedAt time.Time) (int64, error) {
	if t.tenantID == "" {
		return 0, errors.New("projects: tenant_id required")
	}
	res, err := t.handle().ExecContext(ctx, `
		INSERT INTO figma_inventory_run (tenant_id, started_at) VALUES (?, ?)
	`, t.tenantID, rfc3339(startedAt.UTC()))
	if err != nil {
		return 0, fmt.Errorf("insert figma_inventory_run: %w", err)
	}
	return res.LastInsertId()
}

// FinishFigmaInventoryRun stamps the final counters on a started run row.
// errorSample is a slice of error strings — only the first 20 are stored
// to keep the row bounded.
func (t *TenantRepo) FinishFigmaInventoryRun(ctx context.Context, id int64, run FigmaInventoryRunRow, errorSample []string) error {
	if t.tenantID == "" {
		return errors.New("projects: tenant_id required")
	}
	var errJSON any
	if len(errorSample) > 0 {
		if len(errorSample) > 20 {
			errorSample = errorSample[:20]
		}
		b, _ := json.Marshal(errorSample)
		errJSON = string(b)
	}
	_, err := t.handle().ExecContext(ctx, `
		UPDATE figma_inventory_run
		   SET finished_at       = ?,
		       teams_crawled     = ?,
		       projects_seen     = ?,
		       files_seen        = ?,
		       files_refetched   = ?,
		       pages_upserted    = ?,
		       sections_upserted = ?,
		       nodes_upserted    = ?,
		       error_count       = ?,
		       error_sample_json = ?
		 WHERE id = ? AND tenant_id = ?
	`,
		rfc3339(t.now().UTC()),
		run.TeamsCrawled, run.ProjectsSeen, run.FilesSeen, run.FilesRefetched,
		run.PagesUpserted, run.SectionsUpserted, run.NodesUpserted,
		run.ErrorCount, errJSON,
		id, t.tenantID,
	)
	if err != nil {
		return fmt.Errorf("finish figma_inventory_run: %w", err)
	}
	return nil
}

// ListFigmaInventoryRuns returns the most-recent N runs for the tenant.
func (t *TenantRepo) ListFigmaInventoryRuns(ctx context.Context, limit int) ([]FigmaInventoryRunRow, error) {
	if t.tenantID == "" {
		return nil, errors.New("projects: tenant_id required")
	}
	if limit <= 0 {
		limit = 20
	}
	rows, err := t.r.db.QueryContext(ctx, `
		SELECT id, tenant_id, started_at, COALESCE(finished_at, ''),
		       teams_crawled, projects_seen, files_seen, files_refetched,
		       pages_upserted, sections_upserted,
		       COALESCE(nodes_upserted, 0),
		       error_count,
		       COALESCE(error_sample_json, '')
		  FROM figma_inventory_run
		 WHERE tenant_id = ?
		 ORDER BY started_at DESC
		 LIMIT ?
	`, t.tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("list figma_inventory_run: %w", err)
	}
	defer rows.Close()
	out := make([]FigmaInventoryRunRow, 0)
	for rows.Next() {
		var r FigmaInventoryRunRow
		var startedAt, finishedAt string
		if err := rows.Scan(
			&r.ID, &r.TenantID, &startedAt, &finishedAt,
			&r.TeamsCrawled, &r.ProjectsSeen, &r.FilesSeen, &r.FilesRefetched,
			&r.PagesUpserted, &r.SectionsUpserted,
			&r.NodesUpserted,
			&r.ErrorCount,
			&r.ErrorSampleJSON,
		); err != nil {
			return nil, fmt.Errorf("scan figma_inventory_run: %w", err)
		}
		r.StartedAt = parseTime(startedAt)
		r.FinishedAt = parseTime(finishedAt)
		out = append(out, r)
	}
	return out, rows.Err()
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func nullableStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// inClause builds a "?,?,?" placeholder list and its arg slice for an IN().
func inClause(vals []string) (string, []any) {
	if len(vals) == 0 {
		return "''", nil
	}
	ph := strings.Repeat("?,", len(vals))
	ph = ph[:len(ph)-1]
	args := make([]any, len(vals))
	for i, v := range vals {
		args[i] = v
	}
	return ph, args
}
