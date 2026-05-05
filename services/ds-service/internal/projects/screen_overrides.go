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

// ─── screen_text_overrides repo ─────────────────────────────────────────────
//
// U2 of plan 2026-05-05-002: CRUD surface for in-place text overrides on the
// leaf canvas. Mirrors flow_drd's optimistic-concurrency PUT shape so the
// frontend save state machine stays identical (idle | saving | saved |
// conflict | error).
//
// Cross-tenant rows are invisible to the caller (404 at the handler layer);
// every read joins screens → project_versions → projects on tenant_id.

// ScreenTextOverride is the wire shape for GET responses + repo Upsert input.
type ScreenTextOverride struct {
	ID                   string `json:"id"`
	ScreenID             string `json:"screen_id"`
	FigmaNodeID          string `json:"figma_node_id"`
	CanonicalPath        string `json:"canonical_path"`
	LastSeenOriginalText string `json:"last_seen_original_text"`
	Value                string `json:"value"`
	Revision             int    `json:"revision"`
	Status               string `json:"status"` // active | orphaned
	UpdatedByUserID      string `json:"updated_by_user_id"`
	UpdatedAt            string `json:"updated_at"`
}

// ListOverridesByScreen returns every override row pinned to one screen,
// regardless of status. Caller filters client-side as needed.
//
// Cross-tenant screen IDs return ([], nil) — no oracle.
func (t *TenantRepo) ListOverridesByScreen(ctx context.Context, projectSlug, screenID string) ([]ScreenTextOverride, error) {
	if t.tenantID == "" {
		return nil, errors.New("projects: tenant_id required")
	}
	rows, err := t.handle().QueryContext(ctx,
		`SELECT o.id, o.screen_id, o.figma_node_id, o.canonical_path,
		        o.last_seen_original_text, o.value, o.revision, o.status,
		        o.updated_by_user_id, o.updated_at
		   FROM screen_text_overrides o
		   JOIN screens s ON s.id = o.screen_id
		   JOIN project_versions v ON v.id = s.version_id
		   JOIN projects p ON p.id = v.project_id
		  WHERE p.slug = ? AND p.tenant_id = ? AND p.deleted_at IS NULL
		    AND o.screen_id = ? AND o.tenant_id = ?
		  ORDER BY o.updated_at DESC`,
		projectSlug, t.tenantID, screenID, t.tenantID,
	)
	if err != nil {
		return nil, fmt.Errorf("list overrides by screen: %w", err)
	}
	defer rows.Close()
	return scanOverrides(rows)
}

// ListOverridesByLeaf returns every override row pinned to any screen owned
// by one flow ("leaf"). Joins through screens → flows scoped to the slug.
func (t *TenantRepo) ListOverridesByLeaf(ctx context.Context, projectSlug, flowID string) ([]ScreenTextOverride, error) {
	if t.tenantID == "" {
		return nil, errors.New("projects: tenant_id required")
	}
	rows, err := t.handle().QueryContext(ctx,
		`SELECT o.id, o.screen_id, o.figma_node_id, o.canonical_path,
		        o.last_seen_original_text, o.value, o.revision, o.status,
		        o.updated_by_user_id, o.updated_at
		   FROM screen_text_overrides o
		   JOIN screens s ON s.id = o.screen_id
		   JOIN flows f ON f.id = s.flow_id
		   JOIN projects p ON p.id = f.project_id
		  WHERE p.slug = ? AND p.tenant_id = ? AND p.deleted_at IS NULL
		    AND f.id = ? AND o.tenant_id = ?
		  ORDER BY o.updated_at DESC`,
		projectSlug, t.tenantID, flowID, t.tenantID,
	)
	if err != nil {
		return nil, fmt.Errorf("list overrides by leaf: %w", err)
	}
	defer rows.Close()
	return scanOverrides(rows)
}

func scanOverrides(rows *sql.Rows) ([]ScreenTextOverride, error) {
	out := make([]ScreenTextOverride, 0)
	for rows.Next() {
		var o ScreenTextOverride
		if err := rows.Scan(&o.ID, &o.ScreenID, &o.FigmaNodeID, &o.CanonicalPath,
			&o.LastSeenOriginalText, &o.Value, &o.Revision, &o.Status,
			&o.UpdatedByUserID, &o.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan override: %w", err)
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// OverrideUpsertInput is what UpsertOverride consumes. Mirrors the PUT body.
type OverrideUpsertInput struct {
	ProjectSlug          string
	ScreenID             string
	FigmaNodeID          string
	Value                string
	CanonicalPath        string
	LastSeenOriginalText string
	ExpectedRevision     int
	UpdatedByUserID      string
}

// OverrideUpsertResult is what UpsertOverride returns on success.
type OverrideUpsertResult struct {
	Revision  int
	UpdatedAt time.Time
	FlowID    string // resolved from screen for audit_log details
}

// UpsertOverride writes one override with optimistic-concurrency. The whole
// operation runs inside the caller's transaction (when t.tx is non-nil) or
// inside a fresh transaction owned by this method. Audit-log INSERT happens
// in the same tx via the auditFn callback so writes never split.
//
//   - expectedRevision == 0 means "create" — INSERT path. Conflicting unique
//     index → ErrRevisionConflict.
//   - expectedRevision > 0 must match the row's current revision. UPDATE
//     affecting 0 rows → ErrRevisionConflict.
//
// auditFn is called AFTER the override write succeeds, BEFORE the caller's
// commit. It receives the same *sql.Tx so the audit row + override are atomic.
// It also receives the flow_id resolved from the screen and the prior override
// value (empty string on first-write paths) so the caller can embed an
// old → new diff in `details` without a separate query.
func (t *TenantRepo) UpsertOverride(
	ctx context.Context,
	in OverrideUpsertInput,
	auditFn func(tx *sql.Tx, flowID string, oldValue string, newRevision int) error,
) (OverrideUpsertResult, error) {
	if t.tenantID == "" {
		return OverrideUpsertResult{}, errors.New("projects: tenant_id required")
	}
	if in.ExpectedRevision < 0 {
		return OverrideUpsertResult{}, errors.New("projects: expected_revision must be >= 0")
	}

	owned, tx, err := t.beginOrJoin(ctx)
	if err != nil {
		return OverrideUpsertResult{}, fmt.Errorf("begin override tx: %w", err)
	}
	if owned {
		defer func() { _ = tx.Rollback() }()
	}

	// Resolve flow_id and verify the screen is visible to this tenant.
	var flowID string
	err = tx.QueryRowContext(ctx,
		`SELECT s.flow_id FROM screens s
		   JOIN project_versions v ON v.id = s.version_id
		   JOIN projects p ON p.id = v.project_id
		  WHERE s.id = ? AND s.tenant_id = ?
		    AND p.slug = ? AND p.tenant_id = ? AND p.deleted_at IS NULL
		  LIMIT 1`,
		in.ScreenID, t.tenantID, in.ProjectSlug, t.tenantID,
	).Scan(&flowID)
	if errors.Is(err, sql.ErrNoRows) {
		return OverrideUpsertResult{}, ErrNotFound
	}
	if err != nil {
		return OverrideUpsertResult{}, fmt.Errorf("resolve screen: %w", err)
	}

	now := t.now().UTC()
	nowStr := rfc3339(now)

	// Capture the prior override value (if any) BEFORE we mutate the row so
	// the audit hook can record a true old → new diff. On first writes the
	// SELECT returns sql.ErrNoRows; we fall back to LastSeenOriginalText so
	// the activity feed still shows what the user replaced.
	var oldValue string
	{
		var v sql.NullString
		err := tx.QueryRowContext(ctx,
			`SELECT value FROM screen_text_overrides
			   WHERE screen_id = ? AND figma_node_id = ? AND tenant_id = ?
			   LIMIT 1`,
			in.ScreenID, in.FigmaNodeID, t.tenantID,
		).Scan(&v)
		if err == nil && v.Valid {
			oldValue = v.String
		}
	}
	if oldValue == "" {
		oldValue = in.LastSeenOriginalText
	}

	var newRev int
	if in.ExpectedRevision == 0 {
		// First-write path. Unique-constraint hit → another writer raced us;
		// surface as ErrRevisionConflict.
		_, err := tx.ExecContext(ctx,
			`INSERT INTO screen_text_overrides
			    (id, tenant_id, screen_id, figma_node_id, canonical_path,
			     last_seen_original_text, value, revision, status,
			     updated_by_user_id, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, 1, 'active', ?, ?)`,
			uuid.NewString(), t.tenantID, in.ScreenID, in.FigmaNodeID,
			in.CanonicalPath, in.LastSeenOriginalText, in.Value,
			in.UpdatedByUserID, nowStr,
		)
		if err != nil {
			low := strings.ToLower(err.Error())
			if strings.Contains(low, "unique") || strings.Contains(low, "constraint") {
				return OverrideUpsertResult{}, ErrRevisionConflict
			}
			return OverrideUpsertResult{}, fmt.Errorf("override insert: %w", err)
		}
		newRev = 1
	} else {
		res, err := tx.ExecContext(ctx,
			`UPDATE screen_text_overrides
			    SET value = ?, revision = revision + 1, status = 'active',
			        canonical_path = ?, last_seen_original_text = ?,
			        updated_by_user_id = ?, updated_at = ?
			  WHERE screen_id = ? AND figma_node_id = ?
			    AND tenant_id = ? AND revision = ?`,
			in.Value, in.CanonicalPath, in.LastSeenOriginalText,
			in.UpdatedByUserID, nowStr,
			in.ScreenID, in.FigmaNodeID, t.tenantID, in.ExpectedRevision,
		)
		if err != nil {
			return OverrideUpsertResult{}, fmt.Errorf("override update: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return OverrideUpsertResult{}, fmt.Errorf("rows affected: %w", err)
		}
		if n != 1 {
			return OverrideUpsertResult{}, ErrRevisionConflict
		}
		newRev = in.ExpectedRevision + 1
	}

	if auditFn != nil {
		if err := auditFn(tx, flowID, oldValue, newRev); err != nil {
			return OverrideUpsertResult{}, fmt.Errorf("override audit: %w", err)
		}
	}

	if owned {
		if err := tx.Commit(); err != nil {
			return OverrideUpsertResult{}, fmt.Errorf("commit override: %w", err)
		}
	}

	return OverrideUpsertResult{
		Revision:  newRev,
		UpdatedAt: now,
		FlowID:    flowID,
	}, nil
}

// DeleteOverride removes one override row. Idempotent: missing row → (false,
// nil) so the handler can return 204 without distinguishing.
//
// Like UpsertOverride, the operation runs in a tx that also encloses the
// audit-log INSERT; auditFn receives the resolved flow_id and the prior
// override value so the activity feed can surface "{user} reset \"{old}\"".
//
// Returns (deleted bool, flowID string, err error). flowID is "" when the
// screen itself isn't visible — matched against ErrNotFound.
func (t *TenantRepo) DeleteOverride(
	ctx context.Context,
	projectSlug, screenID, figmaNodeID string,
	auditFn func(tx *sql.Tx, flowID string, oldValue string) error,
) (bool, string, error) {
	if t.tenantID == "" {
		return false, "", errors.New("projects: tenant_id required")
	}

	owned, tx, err := t.beginOrJoin(ctx)
	if err != nil {
		return false, "", fmt.Errorf("begin delete tx: %w", err)
	}
	if owned {
		defer func() { _ = tx.Rollback() }()
	}

	var flowID string
	err = tx.QueryRowContext(ctx,
		`SELECT s.flow_id FROM screens s
		   JOIN project_versions v ON v.id = s.version_id
		   JOIN projects p ON p.id = v.project_id
		  WHERE s.id = ? AND s.tenant_id = ?
		    AND p.slug = ? AND p.tenant_id = ? AND p.deleted_at IS NULL
		  LIMIT 1`,
		screenID, t.tenantID, projectSlug, t.tenantID,
	).Scan(&flowID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, "", ErrNotFound
	}
	if err != nil {
		return false, "", fmt.Errorf("resolve screen: %w", err)
	}

	// Capture the prior value before deleting so the audit hook can include
	// it in the activity-feed event. SELECT inside the same tx so the read
	// reflects any in-flight write.
	var oldValue string
	{
		var v sql.NullString
		_ = tx.QueryRowContext(ctx,
			`SELECT value FROM screen_text_overrides
			   WHERE screen_id = ? AND figma_node_id = ? AND tenant_id = ?
			   LIMIT 1`,
			screenID, figmaNodeID, t.tenantID,
		).Scan(&v)
		if v.Valid {
			oldValue = v.String
		}
	}

	res, err := tx.ExecContext(ctx,
		`DELETE FROM screen_text_overrides
		   WHERE screen_id = ? AND figma_node_id = ? AND tenant_id = ?`,
		screenID, figmaNodeID, t.tenantID,
	)
	if err != nil {
		return false, flowID, fmt.Errorf("override delete: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, flowID, fmt.Errorf("rows affected: %w", err)
	}
	deleted := n == 1

	// Always invoke the audit hook even when deleted=false: the handler
	// may still want to record the idempotent-no-op call (or it can no-op
	// itself). Per the plan we only emit the audit row for actual deletes.
	if deleted && auditFn != nil {
		if err := auditFn(tx, flowID, oldValue); err != nil {
			return false, flowID, fmt.Errorf("override delete audit: %w", err)
		}
	}

	if owned {
		if err := tx.Commit(); err != nil {
			return false, flowID, fmt.Errorf("commit delete: %w", err)
		}
	}

	return deleted, flowID, nil
}

// MarkOverridesOrphaned flips one override's status to 'orphaned'. Used by
// U3 (canonical_tree re-import) when the 3-tier match fails. Idempotent.
func (t *TenantRepo) MarkOverridesOrphaned(ctx context.Context, overrideIDs []string) error {
	if t.tenantID == "" {
		return errors.New("projects: tenant_id required")
	}
	if len(overrideIDs) == 0 {
		return nil
	}
	placeholders := strings.Repeat("?,", len(overrideIDs))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, 0, len(overrideIDs)+1)
	for _, id := range overrideIDs {
		args = append(args, id)
	}
	args = append(args, t.tenantID)
	_, err := t.handle().ExecContext(ctx,
		`UPDATE screen_text_overrides
		    SET status = 'orphaned'
		  WHERE id IN (`+placeholders+`) AND tenant_id = ?`,
		args...,
	)
	if err != nil {
		return fmt.Errorf("mark overrides orphaned: %w", err)
	}
	return nil
}

// BulkOverrideRow is one input row for BulkUpsertOverrides. Mirrors a single
// PUT body without expected_revision (bulk runs are last-write-wins per row;
// we do NOT enforce optimistic concurrency in bulk mode).
type BulkOverrideRow struct {
	ScreenID             string
	FigmaNodeID          string
	Value                string
	CanonicalPath        string
	LastSeenOriginalText string
	// Set after the upsert succeeds. New row -> 1; existing row -> revision+1.
	Revision int
	// FlowID is resolved per row so the audit hook can include it.
	FlowID string
	// PerRowAudit is invoked inside the same tx as this row's upsert. Mirrors
	// HandleBulkAcknowledge's BulkLifecycleRow pattern. Receives `oldValue`
	// (the prior value of the row, or LastSeenOriginalText on first writes)
	// so the activity feed can render an old → new diff per bulk row.
	PerRowAudit func(tx *sql.Tx, flowID string, oldValue string, newRevision int) error
}

// BulkOverrideSummary reports per-row outcomes after a bulk upsert.
type BulkOverrideSummary struct {
	Updated []string // figma_node_id list (success)
	Skipped []string // figma_node_id list (cross-tenant or unresolvable)
}

// BulkUpsertOverrides applies many override writes inside a single
// transaction. Pattern mirrors BulkUpdateViolationStatus (HandleBulkAcknowledge):
// per-row audit_log writes happen in the same tx; any audit error rolls back
// the entire batch.
//
// Last-write-wins semantics — bulk callers (CSV import, U12) don't supply
// expected_revision per row.
func (t *TenantRepo) BulkUpsertOverrides(
	ctx context.Context,
	projectSlug string,
	rows []*BulkOverrideRow,
	updatedByUserID string,
) (BulkOverrideSummary, error) {
	if t.tenantID == "" {
		return BulkOverrideSummary{}, errors.New("projects: tenant_id required")
	}
	if len(rows) == 0 {
		return BulkOverrideSummary{}, nil
	}

	tx, err := t.r.db.BeginTx(ctx, nil)
	if err != nil {
		return BulkOverrideSummary{}, fmt.Errorf("begin bulk override tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	resolveStmt, err := tx.PrepareContext(ctx,
		`SELECT s.flow_id FROM screens s
		   JOIN project_versions v ON v.id = s.version_id
		   JOIN projects p ON p.id = v.project_id
		  WHERE s.id = ? AND s.tenant_id = ?
		    AND p.slug = ? AND p.tenant_id = ? AND p.deleted_at IS NULL
		  LIMIT 1`)
	if err != nil {
		return BulkOverrideSummary{}, fmt.Errorf("prepare resolve: %w", err)
	}
	defer resolveStmt.Close()

	priorStmt, err := tx.PrepareContext(ctx,
		`SELECT value FROM screen_text_overrides
		   WHERE screen_id = ? AND figma_node_id = ? AND tenant_id = ?
		   LIMIT 1`)
	if err != nil {
		return BulkOverrideSummary{}, fmt.Errorf("prepare prior: %w", err)
	}
	defer priorStmt.Close()

	upsertStmt, err := tx.PrepareContext(ctx,
		`INSERT INTO screen_text_overrides
		    (id, tenant_id, screen_id, figma_node_id, canonical_path,
		     last_seen_original_text, value, revision, status,
		     updated_by_user_id, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, 1, 'active', ?, ?)
		 ON CONFLICT(screen_id, figma_node_id) DO UPDATE SET
		    value = excluded.value,
		    canonical_path = excluded.canonical_path,
		    last_seen_original_text = excluded.last_seen_original_text,
		    revision = screen_text_overrides.revision + 1,
		    status = 'active',
		    updated_by_user_id = excluded.updated_by_user_id,
		    updated_at = excluded.updated_at
		 RETURNING revision`)
	if err != nil {
		return BulkOverrideSummary{}, fmt.Errorf("prepare upsert: %w", err)
	}
	defer upsertStmt.Close()

	now := rfc3339(t.now().UTC())
	summary := BulkOverrideSummary{}
	for _, row := range rows {
		var flowID string
		err := resolveStmt.QueryRowContext(ctx,
			row.ScreenID, t.tenantID, projectSlug, t.tenantID,
		).Scan(&flowID)
		if errors.Is(err, sql.ErrNoRows) {
			summary.Skipped = append(summary.Skipped, row.FigmaNodeID)
			continue
		}
		if err != nil {
			return BulkOverrideSummary{}, fmt.Errorf("bulk resolve %s: %w", row.ScreenID, err)
		}
		row.FlowID = flowID

		// Capture the prior override value (if any) before the upsert so
		// PerRowAudit can include an old → new diff. First-write rows fall
		// back to LastSeenOriginalText so the activity feed still has a
		// meaningful "what was replaced" string.
		var oldValue string
		{
			var v sql.NullString
			err := priorStmt.QueryRowContext(ctx, row.ScreenID, row.FigmaNodeID, t.tenantID).Scan(&v)
			if err == nil && v.Valid {
				oldValue = v.String
			}
		}
		if oldValue == "" {
			oldValue = row.LastSeenOriginalText
		}

		var newRev int
		err = upsertStmt.QueryRowContext(ctx,
			uuid.NewString(), t.tenantID, row.ScreenID, row.FigmaNodeID,
			row.CanonicalPath, row.LastSeenOriginalText, row.Value,
			updatedByUserID, now,
		).Scan(&newRev)
		if err != nil {
			return BulkOverrideSummary{}, fmt.Errorf("bulk upsert row %s: %w", row.FigmaNodeID, err)
		}
		row.Revision = newRev

		if row.PerRowAudit != nil {
			if err := row.PerRowAudit(tx, flowID, oldValue, newRev); err != nil {
				return BulkOverrideSummary{}, fmt.Errorf("bulk audit row %s: %w", row.FigmaNodeID, err)
			}
		}
		summary.Updated = append(summary.Updated, row.FigmaNodeID)
	}

	if err := tx.Commit(); err != nil {
		return BulkOverrideSummary{}, fmt.Errorf("commit bulk override: %w", err)
	}
	return summary, nil
}
