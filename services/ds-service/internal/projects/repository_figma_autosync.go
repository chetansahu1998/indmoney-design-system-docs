package projects

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// repository_figma_autosync.go — U6 of the autosync bridge plan
// (docs/plans/2026-05-14-001-feat-figma-db-autosync-bridge-plan.md).
//
// TenantRepo methods for the two tables migration 0028 introduced:
//   - figma_auto_sync_state (per-section sync history)
//   - figma_project_mapping (admin (domain, product) per Figma project)
//
// Same conventions as repository_figma_inventory.go: tenant_id captured
// from TenantRepo and force-injected on every row; prepared-statement-
// inside-tx for batch writes; ErrNotFound on row-miss.

// ─── figma_auto_sync_state ───────────────────────────────────────────────────

// AutoSyncState mirrors one figma_auto_sync_state row.
type AutoSyncState struct {
	TenantID            string
	FileKey             string
	PageID              string
	SectionID           string
	ContentHash         string
	PositionHash        string
	LastSyncedFlowID    string
	LastSyncedVersionID string
	LastSyncedAt        time.Time
	LastAttemptAt       time.Time
	LastAttemptStatus   string // 'ok' | 'skipped' | 'error' | 'quarantined'
	SkipReason          string
	ErrorMessage        string
	FirstSeenAt         time.Time
}

// UpsertAutoSyncState writes/refreshes one state row for a section.
// Idempotent on the 4-tuple PK (tenant_id, file_key, page_id, section_id).
// The caller is responsible for setting LastAttemptStatus + SkipReason +
// ErrorMessage to whatever shape applies; this method just persists.
//
// Behavior:
//   - last_attempt_at is ALWAYS bumped to now.
//   - last_synced_at moves only when LastAttemptStatus == 'ok'.
//   - last_synced_flow_id / last_synced_version_id move only when the
//     caller supplies non-empty values (preserves prior known-good on
//     transient errors).
//   - first_seen_at is preserved across upserts (DEFAULT-NULL safe).
func (t *TenantRepo) UpsertAutoSyncState(ctx context.Context, s AutoSyncState) error {
	if t.tenantID == "" {
		return errors.New("projects: tenant_id required")
	}
	if s.FileKey == "" || s.PageID == "" || s.SectionID == "" {
		return errors.New("projects: file_key, page_id, section_id required")
	}
	now := rfc3339(t.now().UTC())
	firstSeen := now
	if !s.FirstSeenAt.IsZero() {
		firstSeen = rfc3339(s.FirstSeenAt.UTC())
	}

	var lastSyncedAt any
	if s.LastAttemptStatus == "ok" {
		lastSyncedAt = now
	}
	var flowID, versionID any
	if s.LastSyncedFlowID != "" {
		flowID = s.LastSyncedFlowID
	}
	if s.LastSyncedVersionID != "" {
		versionID = s.LastSyncedVersionID
	}

	_, err := t.handle().ExecContext(ctx, `
		INSERT INTO figma_auto_sync_state (
			tenant_id, file_key, page_id, section_id,
			content_hash, position_hash,
			last_synced_flow_id, last_synced_version_id, last_synced_at,
			last_attempt_at, last_attempt_status, skip_reason, error_message,
			first_seen_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(tenant_id, file_key, page_id, section_id) DO UPDATE SET
			content_hash           = excluded.content_hash,
			position_hash          = excluded.position_hash,
			last_synced_flow_id    = COALESCE(excluded.last_synced_flow_id,    figma_auto_sync_state.last_synced_flow_id),
			last_synced_version_id = COALESCE(excluded.last_synced_version_id, figma_auto_sync_state.last_synced_version_id),
			last_synced_at         = CASE
			    WHEN excluded.last_attempt_status = 'ok' THEN excluded.last_synced_at
			    ELSE figma_auto_sync_state.last_synced_at
			END,
			last_attempt_at        = excluded.last_attempt_at,
			last_attempt_status    = excluded.last_attempt_status,
			skip_reason            = excluded.skip_reason,
			error_message          = excluded.error_message
	`,
		t.tenantID, s.FileKey, s.PageID, s.SectionID,
		nullableStr(s.ContentHash), nullableStr(s.PositionHash),
		flowID, versionID, lastSyncedAt,
		now, nullableStr(s.LastAttemptStatus),
		nullableStr(s.SkipReason), nullableStr(s.ErrorMessage),
		firstSeen,
	)
	if err != nil {
		return fmt.Errorf("upsert figma_auto_sync_state: %w", err)
	}
	return nil
}

// LookupAutoSyncState returns one state row by the 4-tuple PK. Returns
// ErrNotFound when the row doesn't exist — the planner uses this to
// distinguish "first sync ever" from "compare hash against prior state."
func (t *TenantRepo) LookupAutoSyncState(ctx context.Context, fileKey, pageID, sectionID string) (AutoSyncState, error) {
	if t.tenantID == "" {
		return AutoSyncState{}, errors.New("projects: tenant_id required")
	}
	row := t.r.db.QueryRowContext(ctx, `
		SELECT tenant_id, file_key, page_id, section_id,
		       COALESCE(content_hash, ''), COALESCE(position_hash, ''),
		       COALESCE(last_synced_flow_id, ''), COALESCE(last_synced_version_id, ''),
		       COALESCE(last_synced_at, ''),
		       COALESCE(last_attempt_at, ''), COALESCE(last_attempt_status, ''),
		       COALESCE(skip_reason, ''), COALESCE(error_message, ''),
		       first_seen_at
		  FROM figma_auto_sync_state
		 WHERE tenant_id = ? AND file_key = ? AND page_id = ? AND section_id = ?
	`, t.tenantID, fileKey, pageID, sectionID)

	var s AutoSyncState
	var lastSynced, lastAttempt, firstSeen string
	err := row.Scan(
		&s.TenantID, &s.FileKey, &s.PageID, &s.SectionID,
		&s.ContentHash, &s.PositionHash,
		&s.LastSyncedFlowID, &s.LastSyncedVersionID, &lastSynced,
		&lastAttempt, &s.LastAttemptStatus,
		&s.SkipReason, &s.ErrorMessage,
		&firstSeen,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return AutoSyncState{}, ErrNotFound
	}
	if err != nil {
		return AutoSyncState{}, fmt.Errorf("scan figma_auto_sync_state: %w", err)
	}
	s.LastSyncedAt = parseTime(lastSynced)
	s.LastAttemptAt = parseTime(lastAttempt)
	s.FirstSeenAt = parseTime(firstSeen)
	return s, nil
}

// AutoSyncStateFilter narrows ListAutoSyncState results. Fields left zero
// (empty string for strings, 0 for limit/offset) mean "no filter on this
// dimension". Limit defaults to 100, offset defaults to 0.
type AutoSyncStateFilter struct {
	FileKey    string
	Status     string // 'ok' | 'skipped' | 'error' | 'quarantined' — exact match
	SkipReason string // exact match
	Limit      int
	Offset     int
}

// ListAutoSyncState returns paginated state rows matching the filter,
// ordered by last_attempt_at DESC (most-recent-activity first).
func (t *TenantRepo) ListAutoSyncState(ctx context.Context, f AutoSyncStateFilter) ([]AutoSyncState, error) {
	if t.tenantID == "" {
		return nil, errors.New("projects: tenant_id required")
	}
	if f.Limit <= 0 {
		f.Limit = 100
	}

	sqlStr := `
		SELECT tenant_id, file_key, page_id, section_id,
		       COALESCE(content_hash, ''), COALESCE(position_hash, ''),
		       COALESCE(last_synced_flow_id, ''), COALESCE(last_synced_version_id, ''),
		       COALESCE(last_synced_at, ''),
		       COALESCE(last_attempt_at, ''), COALESCE(last_attempt_status, ''),
		       COALESCE(skip_reason, ''), COALESCE(error_message, ''),
		       first_seen_at
		  FROM figma_auto_sync_state
		 WHERE tenant_id = ?`
	args := []any{t.tenantID}
	if f.FileKey != "" {
		sqlStr += ` AND file_key = ?`
		args = append(args, f.FileKey)
	}
	if f.Status != "" {
		sqlStr += ` AND last_attempt_status = ?`
		args = append(args, f.Status)
	}
	if f.SkipReason != "" {
		sqlStr += ` AND skip_reason = ?`
		args = append(args, f.SkipReason)
	}
	sqlStr += ` ORDER BY last_attempt_at DESC LIMIT ? OFFSET ?`
	args = append(args, f.Limit, f.Offset)

	rows, err := t.r.db.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, fmt.Errorf("list figma_auto_sync_state: %w", err)
	}
	defer rows.Close()

	out := make([]AutoSyncState, 0)
	for rows.Next() {
		var s AutoSyncState
		var lastSynced, lastAttempt, firstSeen string
		if err := rows.Scan(
			&s.TenantID, &s.FileKey, &s.PageID, &s.SectionID,
			&s.ContentHash, &s.PositionHash,
			&s.LastSyncedFlowID, &s.LastSyncedVersionID, &lastSynced,
			&lastAttempt, &s.LastAttemptStatus,
			&s.SkipReason, &s.ErrorMessage,
			&firstSeen,
		); err != nil {
			return nil, fmt.Errorf("scan figma_auto_sync_state: %w", err)
		}
		s.LastSyncedAt = parseTime(lastSynced)
		s.LastAttemptAt = parseTime(lastAttempt)
		s.FirstSeenAt = parseTime(firstSeen)
		out = append(out, s)
	}
	return out, rows.Err()
}

// ─── planner helpers — page + section reads with classifier output ──────────

// FigmaPageRowFull is figma_page extended with classifier + hash fields.
// Distinct from FigmaPageRow (which is the upsert input) so SELECT
// columns are explicit and stable.
type FigmaPageRowFull struct {
	TenantID            string
	FileKey             string
	PageID              string
	Name                string
	OrderIndex          int
	BackgroundColorHex  string
	ContentHash         string
	PositionHash        string
	DerivedLastModified time.Time
	PageClassification  string
	VersionBase         string
	VersionN            int
	PersonaHint         string
}

// ListFigmaPagesForFile returns every live page row in a file with the
// classifier output columns the AutoSyncPlanner needs to pick source pages.
func (t *TenantRepo) ListFigmaPagesForFile(ctx context.Context, fileKey string) ([]FigmaPageRowFull, error) {
	if t.tenantID == "" {
		return nil, errors.New("projects: tenant_id required")
	}
	if fileKey == "" {
		return nil, errors.New("projects: file_key required")
	}
	rows, err := t.r.db.QueryContext(ctx, `
		SELECT page_id, name, order_index, COALESCE(background_color, ''),
		       COALESCE(content_hash, ''), COALESCE(position_hash, ''),
		       COALESCE(derived_last_modified, ''),
		       COALESCE(page_classification, ''),
		       COALESCE(version_base, ''),
		       COALESCE(version_n, 0),
		       COALESCE(persona_hint, '')
		  FROM figma_page
		 WHERE tenant_id = ? AND file_key = ? AND deleted_at IS NULL
		 ORDER BY order_index ASC, page_id ASC
	`, t.tenantID, fileKey)
	if err != nil {
		return nil, fmt.Errorf("list figma_page: %w", err)
	}
	defer rows.Close()
	out := make([]FigmaPageRowFull, 0)
	for rows.Next() {
		var r FigmaPageRowFull
		var derivedLastModified string
		if err := rows.Scan(
			&r.PageID, &r.Name, &r.OrderIndex, &r.BackgroundColorHex,
			&r.ContentHash, &r.PositionHash, &derivedLastModified,
			&r.PageClassification, &r.VersionBase, &r.VersionN, &r.PersonaHint,
		); err != nil {
			return nil, fmt.Errorf("scan figma_page: %w", err)
		}
		r.TenantID = t.tenantID
		r.FileKey = fileKey
		r.DerivedLastModified = parseTime(derivedLastModified)
		out = append(out, r)
	}
	return out, rows.Err()
}

// FigmaSectionRowFull is figma_section with hash columns. Distinct from
// FigmaSectionRow (upsert input) for the same reason as FigmaPageRowFull.
type FigmaSectionRowFull struct {
	TenantID     string
	FileKey      string
	PageID       string
	SectionID    string
	Name         string
	X            float64
	Y            float64
	Width        float64
	Height       float64
	OrderIndex   int
	ContentHash  string
	PositionHash string
	// Migration 0029 — Claude/admin-supplied taxonomy overrides. When set,
	// the planner uses these in preference to ParseSectionName(name).
	SubProductOverride string
	SubFlowOverride    string
	ClassifiedSource   string // 'section_name' | 'claude_heuristic' | 'admin_override' | ''
}

// ListFigmaSectionsForPage returns every live section under a page.
func (t *TenantRepo) ListFigmaSectionsForPage(ctx context.Context, fileKey, pageID string) ([]FigmaSectionRowFull, error) {
	if t.tenantID == "" {
		return nil, errors.New("projects: tenant_id required")
	}
	if fileKey == "" || pageID == "" {
		return nil, errors.New("projects: file_key and page_id required")
	}
	rows, err := t.r.db.QueryContext(ctx, `
		SELECT section_id, name, x, y, width, height, order_index,
		       COALESCE(content_hash, ''), COALESCE(position_hash, ''),
		       COALESCE(sub_product_override, ''),
		       COALESCE(sub_flow_override, ''),
		       COALESCE(classified_source, '')
		  FROM figma_section
		 WHERE tenant_id = ? AND file_key = ? AND page_id = ? AND deleted_at IS NULL
		 ORDER BY order_index ASC, section_id ASC
	`, t.tenantID, fileKey, pageID)
	if err != nil {
		return nil, fmt.Errorf("list figma_section: %w", err)
	}
	defer rows.Close()
	out := make([]FigmaSectionRowFull, 0)
	for rows.Next() {
		var r FigmaSectionRowFull
		if err := rows.Scan(
			&r.SectionID, &r.Name, &r.X, &r.Y, &r.Width, &r.Height, &r.OrderIndex,
			&r.ContentHash, &r.PositionHash,
			&r.SubProductOverride, &r.SubFlowOverride, &r.ClassifiedSource,
		); err != nil {
			return nil, fmt.Errorf("scan figma_section: %w", err)
		}
		r.TenantID = t.tenantID
		r.FileKey = fileKey
		r.PageID = pageID
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListFigmaFilesForAutoSync returns every figma_file row that's
// eligible for the AutoSyncPlanner: in-window (last_modified >= cutoff)
// AND its figma_project_mapping has enabled_for_autosync=1
// AND — when the tenant has a non-empty figma_owner_allowlist — the
// file's last_editor_name appears on the allowlist.
//
// The allowlist join is conditional via "OR allowlist-is-empty" so a
// fresh install with no allowlist rows behaves as "allow all". A tenant
// that seeds even one name flips into allowlist-mode and unrecognised
// last-editor names are filtered out (or filtered out as "missing" if
// last_editor_name IS NULL — caller must run owner-fetch first).
//
// Soft-deleted files excluded. Ordered by last_modified DESC so recent
// files surface first.
func (t *TenantRepo) ListFigmaFilesForAutoSync(ctx context.Context, cutoff time.Time) ([]FigmaFileRow, error) {
	if t.tenantID == "" {
		return nil, errors.New("projects: tenant_id required")
	}
	rows, err := t.r.db.QueryContext(ctx, `
		SELECT f.file_key, f.project_id, f.team_id, f.name,
		       COALESCE(f.thumbnail_url, ''),
		       COALESCE(f.last_modified, ''),
		       COALESCE(f.version, ''),
		       COALESCE(f.editor_type, ''),
		       COALESCE(f.link_access, ''),
		       COALESCE(f.role, ''),
		       COALESCE(f.branch_of_file_key, ''),
		       COALESCE(f.pages_last_synced_at, ''),
		       COALESCE(f.pages_sync_version, ''),
		       f.first_seen_at, f.last_seen_at
		  FROM figma_file f
		  JOIN figma_project_mapping m
		       ON m.tenant_id = f.tenant_id
		      AND m.project_id = f.project_id
		      AND m.enabled_for_autosync = 1
		 WHERE f.tenant_id = ?
		   AND f.deleted_at IS NULL
		   AND f.last_modified >= ?
		   AND (
		         (SELECT COUNT(*) FROM figma_owner_allowlist WHERE tenant_id = ?) = 0
		         OR f.last_editor_name IN (SELECT full_name FROM figma_owner_allowlist WHERE tenant_id = ?)
		       )
		 ORDER BY f.last_modified DESC
	`, t.tenantID, rfc3339(cutoff.UTC()), t.tenantID, t.tenantID)
	if err != nil {
		return nil, fmt.Errorf("list figma_file for autosync: %w", err)
	}
	defer rows.Close()
	out := make([]FigmaFileRow, 0)
	for rows.Next() {
		var r FigmaFileRow
		var lastMod, pagesSyncedAt, firstSeen, lastSeen string
		if err := rows.Scan(
			&r.FileKey, &r.ProjectID, &r.TeamID, &r.Name,
			&r.ThumbnailURL, &lastMod, &r.Version, &r.EditorType,
			&r.LinkAccess, &r.Role, &r.BranchOfFileKey,
			&pagesSyncedAt, &r.PagesSyncVersion,
			&firstSeen, &lastSeen,
		); err != nil {
			return nil, fmt.Errorf("scan figma_file: %w", err)
		}
		r.TenantID = t.tenantID
		r.LastModified = parseTime(lastMod)
		r.PagesLastSyncedAt = parseTime(pagesSyncedAt)
		r.FirstSeenAt = parseTime(firstSeen)
		r.LastSeenAt = parseTime(lastSeen)
		out = append(out, r)
	}
	return out, rows.Err()
}

// ─── figma_project_mapping ───────────────────────────────────────────────────

// FigmaProjectMapping mirrors one figma_project_mapping row.
type FigmaProjectMapping struct {
	TenantID           string
	ProjectID          string
	Domain             string
	Product            string
	PlatformDefault    string // 'mobile' | 'web' | 'unspecified'
	EnabledForAutosync bool
	MappedByUserID     string
	MappedAt           time.Time
	UpdatedAt          time.Time
}

// UpsertFigmaProjectMapping creates or updates an admin-managed mapping
// from a Figma project id to (domain, product, platform_default). Caller
// supplies MappedByUserID; we stamp MappedAt + UpdatedAt server-side.
//
// PlatformDefault defaults to "unspecified" if empty. EnabledForAutosync
// is honored as-is — explicitly pass true on initial mapping, set false
// to pause without losing the (domain, product) values.
func (t *TenantRepo) UpsertFigmaProjectMapping(ctx context.Context, m FigmaProjectMapping) error {
	if t.tenantID == "" {
		return errors.New("projects: tenant_id required")
	}
	if m.ProjectID == "" {
		return errors.New("projects: project_id required")
	}
	if m.Domain == "" || m.Product == "" {
		return errors.New("projects: domain and product required")
	}
	if m.MappedByUserID == "" {
		return errors.New("projects: mapped_by_user_id required")
	}
	platform := m.PlatformDefault
	if platform == "" {
		platform = "unspecified"
	}
	enabled := 0
	if m.EnabledForAutosync {
		enabled = 1
	}
	now := rfc3339(t.now().UTC())
	mappedAt := now
	if !m.MappedAt.IsZero() {
		mappedAt = rfc3339(m.MappedAt.UTC())
	}

	_, err := t.handle().ExecContext(ctx, `
		INSERT INTO figma_project_mapping (
			tenant_id, project_id, domain, product, platform_default,
			enabled_for_autosync, mapped_by_user_id, mapped_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(tenant_id, project_id) DO UPDATE SET
			domain               = excluded.domain,
			product              = excluded.product,
			platform_default     = excluded.platform_default,
			enabled_for_autosync = excluded.enabled_for_autosync,
			mapped_by_user_id    = excluded.mapped_by_user_id,
			updated_at           = excluded.updated_at
	`,
		t.tenantID, m.ProjectID, m.Domain, m.Product, platform,
		enabled, m.MappedByUserID, mappedAt, now,
	)
	if err != nil {
		return fmt.Errorf("upsert figma_project_mapping: %w", err)
	}
	return nil
}

// LookupFigmaProjectMapping returns one mapping by Figma project_id.
// ErrNotFound when no mapping exists (planner treats this as
// "quarantine — project_unmapped").
func (t *TenantRepo) LookupFigmaProjectMapping(ctx context.Context, projectID string) (FigmaProjectMapping, error) {
	if t.tenantID == "" {
		return FigmaProjectMapping{}, errors.New("projects: tenant_id required")
	}
	row := t.r.db.QueryRowContext(ctx, `
		SELECT tenant_id, project_id, domain, product, platform_default,
		       enabled_for_autosync, mapped_by_user_id, mapped_at, updated_at
		  FROM figma_project_mapping
		 WHERE tenant_id = ? AND project_id = ?
	`, t.tenantID, projectID)
	var m FigmaProjectMapping
	var enabled int
	var mappedAt, updatedAt string
	err := row.Scan(
		&m.TenantID, &m.ProjectID, &m.Domain, &m.Product, &m.PlatformDefault,
		&enabled, &m.MappedByUserID, &mappedAt, &updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return FigmaProjectMapping{}, ErrNotFound
	}
	if err != nil {
		return FigmaProjectMapping{}, fmt.Errorf("scan figma_project_mapping: %w", err)
	}
	m.EnabledForAutosync = enabled != 0
	m.MappedAt = parseTime(mappedAt)
	m.UpdatedAt = parseTime(updatedAt)
	return m, nil
}

// ListFigmaProjectMappings returns every mapping for the tenant.
// Ordered by (product ASC, domain ASC) so the admin UI renders cleanly.
func (t *TenantRepo) ListFigmaProjectMappings(ctx context.Context) ([]FigmaProjectMapping, error) {
	if t.tenantID == "" {
		return nil, errors.New("projects: tenant_id required")
	}
	rows, err := t.r.db.QueryContext(ctx, `
		SELECT tenant_id, project_id, domain, product, platform_default,
		       enabled_for_autosync, mapped_by_user_id, mapped_at, updated_at
		  FROM figma_project_mapping
		 WHERE tenant_id = ?
		 ORDER BY product ASC, domain ASC
	`, t.tenantID)
	if err != nil {
		return nil, fmt.Errorf("list figma_project_mapping: %w", err)
	}
	defer rows.Close()

	out := make([]FigmaProjectMapping, 0)
	for rows.Next() {
		var m FigmaProjectMapping
		var enabled int
		var mappedAt, updatedAt string
		if err := rows.Scan(
			&m.TenantID, &m.ProjectID, &m.Domain, &m.Product, &m.PlatformDefault,
			&enabled, &m.MappedByUserID, &mappedAt, &updatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan figma_project_mapping: %w", err)
		}
		m.EnabledForAutosync = enabled != 0
		m.MappedAt = parseTime(mappedAt)
		m.UpdatedAt = parseTime(updatedAt)
		out = append(out, m)
	}
	return out, rows.Err()
}

// ─── section classification overrides (migration 0029) ──────────────────────

// SectionClassification is what the Claude classifier or admin override
// writes into figma_section.{sub_product,sub_flow}_override.
type SectionClassification struct {
	FileKey    string
	PageID     string
	SectionID  string
	SubProduct string
	SubFlow    string
	// Source distinguishes hand-entered designer naming ('section_name'),
	// Claude/heuristic pass ('claude_heuristic'), or admin override
	// ('admin_override'). The planner doesn't branch on source today but
	// the column lets the UI surface provenance.
	Source string
}

// UpsertSectionClassification writes the override columns for one section.
// Idempotent; tenant-scoped. Empty SubProduct + SubFlow are rejected — the
// caller should delete the row's override via DeleteSectionClassification
// instead of writing empty strings.
func (t *TenantRepo) UpsertSectionClassification(ctx context.Context, c SectionClassification) error {
	if t.tenantID == "" {
		return errors.New("projects: tenant_id required")
	}
	if c.FileKey == "" || c.PageID == "" || c.SectionID == "" {
		return errors.New("projects: file_key, page_id, section_id required")
	}
	if c.SubProduct == "" || c.SubFlow == "" {
		return errors.New("projects: sub_product and sub_flow required")
	}
	if c.Source == "" {
		c.Source = "claude_heuristic"
	}
	_, err := t.handle().ExecContext(ctx, `
		UPDATE figma_section
		   SET sub_product_override = ?,
		       sub_flow_override    = ?,
		       classified_source    = ?,
		       classified_at        = ?
		 WHERE tenant_id = ? AND file_key = ? AND page_id = ? AND section_id = ?
	`,
		c.SubProduct, c.SubFlow, c.Source, rfc3339(t.now().UTC()),
		t.tenantID, c.FileKey, c.PageID, c.SectionID,
	)
	if err != nil {
		return fmt.Errorf("upsert section classification: %w", err)
	}
	return nil
}
