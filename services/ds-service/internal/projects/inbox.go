package projects

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Phase 4 U4 — designer personal inbox.
//
// /v1/inbox returns the requesting user's Active violations across every
// flow they own or are editor on. Phase 4 scope is tenant + role-based;
// Phase 7's per-resource ACL grants extend this without changing the
// query shape (an extra WHERE clause is appended to the same composite
// index).

// InboxRow is one violation enriched with the flow + project + screen
// context the inbox UI needs to render the row without a second fetch.
//
// Fields are flat (rather than nested) so the JSON wire shape matches
// the row layout the docs site renders directly. The frontend types map
// 1:1 to this struct.
type InboxRow struct {
	ViolationID string `json:"violation_id"`
	VersionID   string `json:"version_id"`
	ScreenID    string `json:"screen_id"`
	FlowID      string `json:"flow_id"`
	ProjectID   string `json:"project_id"`
	ProjectSlug string `json:"project_slug"`
	ProjectName string `json:"project_name"`
	Product     string `json:"product"`
	FlowName    string `json:"flow_name"`
	RuleID      string `json:"rule_id"`
	Category    string `json:"category"`
	Severity    string `json:"severity"`
	Property    string `json:"property"`
	Observed    string `json:"observed,omitempty"`
	Suggestion  string `json:"suggestion,omitempty"`
	PersonaID   string `json:"persona_id,omitempty"`
	ModeLabel   string `json:"mode_label,omitempty"`
	AutoFixable bool   `json:"auto_fixable"`
	Status      string `json:"status"`
	CreatedAt   string `json:"created_at"` // RFC3339
}

// InboxFilters carries the optional query-string filters the inbox endpoint
// honors. Empty fields skip that predicate. DateFrom / DateTo are RFC3339;
// invalid timestamps are rejected at the HTTP layer before this struct is
// built.
type InboxFilters struct {
	RuleID     string
	Persona    string // matches violations.persona_id (a UUID) or persona name (resolved later)
	ModeLabel  string
	ProjectID  string
	Category   string
	DateFrom   time.Time
	DateTo     time.Time
	Limit      int // capped at MaxInboxLimit; default DefaultInboxLimit
	Offset     int // pagination cursor — Phase 8 search replaces with proper keyset
	Severities []string
}

// MaxInboxLimit caps how many rows a single inbox request may return.
// Matches the plan's "Load more at 100 rows" pagination.
const (
	DefaultInboxLimit = 50
	MaxInboxLimit     = 100
)

// GetInbox returns Active violations visible to the requesting user inside
// the tenant the repo was built for. Visibility (Phase 4 scope):
//
//   - The user is project.owner_user_id, OR
//   - The user has an editor-or-higher tenant_users role
//     (tenant_admin / designer / engineer).
//
// The HTTP layer resolves the tenant role and passes it via the editor
// flag; this method does NOT call back into auth — keeps the repo
// boundary clean.
//
// Returns (rows, total_matching_count, error). total_matching_count is
// for the "Load more" affordance — the count IS subject to the same
// editor-scope predicate as the rows.
func (t *TenantRepo) GetInbox(ctx context.Context, userID string, isEditor bool, f InboxFilters) ([]InboxRow, int, error) {
	limit := f.Limit
	if limit <= 0 || limit > MaxInboxLimit {
		limit = DefaultInboxLimit
	}
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}

	// Build WHERE clauses incrementally so the SQL string mirrors the
	// filters the caller actually applied — easier to debug at SQL log
	// time than a static query with NULL-passthrough placeholders.
	clauses := []string{
		"v.tenant_id = ?",
		"v.status = 'active'",
		"p.deleted_at IS NULL",
	}
	args := []any{t.tenantID}

	// Visibility scope. Editors see everything in the tenant; non-editors
	// only see violations on projects they own.
	if !isEditor {
		clauses = append(clauses, "p.owner_user_id = ?")
		args = append(args, userID)
	}

	if f.RuleID != "" {
		clauses = append(clauses, "v.rule_id = ?")
		args = append(args, f.RuleID)
	}
	if f.Persona != "" {
		clauses = append(clauses, "v.persona_id = ?")
		args = append(args, f.Persona)
	}
	if f.ModeLabel != "" {
		clauses = append(clauses, "v.mode_label = ?")
		args = append(args, f.ModeLabel)
	}
	if f.ProjectID != "" {
		clauses = append(clauses, "p.id = ?")
		args = append(args, f.ProjectID)
	}
	if f.Category != "" {
		clauses = append(clauses, "v.category = ?")
		args = append(args, f.Category)
	}
	if !f.DateFrom.IsZero() {
		clauses = append(clauses, "v.created_at >= ?")
		args = append(args, f.DateFrom.UTC().Format(time.RFC3339))
	}
	if !f.DateTo.IsZero() {
		clauses = append(clauses, "v.created_at <= ?")
		args = append(args, f.DateTo.UTC().Format(time.RFC3339))
	}
	if len(f.Severities) > 0 {
		placeholders := make([]string, len(f.Severities))
		for i, sev := range f.Severities {
			placeholders[i] = "?"
			args = append(args, sev)
		}
		clauses = append(clauses, "v.severity IN ("+strings.Join(placeholders, ",")+")")
	}

	// Latest-version filter: a designer's inbox should only see violations
	// on the most recent (per project) version. Older versions' active
	// violations are noise — the next re-audit will re-emit (or not).
	clauses = append(clauses,
		`pv.id = (SELECT id FROM project_versions
		            WHERE project_id = pv.project_id
		            ORDER BY version_index DESC LIMIT 1)`,
	)

	whereSQL := strings.Join(clauses, " AND ")

	// Count first (same WHERE) so the UI can render "47 of 47 matches".
	countQuery := `SELECT COUNT(*)
	                 FROM violations v
	                 JOIN project_versions pv ON pv.id = v.version_id
	                 JOIN projects p ON p.id = pv.project_id
	                WHERE ` + whereSQL
	var total int
	if err := t.r.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("inbox count: %w", err)
	}
	if total == 0 {
		return nil, 0, nil
	}

	rowQuery := `SELECT v.id, v.version_id, v.screen_id,
	                    s.flow_id, p.id, p.slug, p.name, p.product,
	                    f.name,
	                    v.rule_id, v.category, v.severity, v.property,
	                    COALESCE(v.observed, ''), COALESCE(v.suggestion, ''),
	                    COALESCE(v.persona_id, ''), COALESCE(v.mode_label, ''),
	                    v.auto_fixable, v.status, v.created_at
	               FROM violations v
	               JOIN screens s ON s.id = v.screen_id
	               JOIN flows f ON f.id = s.flow_id
	               JOIN project_versions pv ON pv.id = v.version_id
	               JOIN projects p ON p.id = pv.project_id
	              WHERE ` + whereSQL + `
	              ORDER BY
	                CASE v.severity
	                  WHEN 'critical' THEN 1
	                  WHEN 'high'     THEN 2
	                  WHEN 'medium'   THEN 3
	                  WHEN 'low'      THEN 4
	                  ELSE 5
	                END,
	                v.created_at DESC
	              LIMIT ? OFFSET ?`
	args = append(args, limit, offset)

	rows, err := t.r.db.QueryContext(ctx, rowQuery, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("inbox query: %w", err)
	}
	defer rows.Close()

	out := make([]InboxRow, 0, limit)
	for rows.Next() {
		var r InboxRow
		var autoFix int
		if err := rows.Scan(
			&r.ViolationID, &r.VersionID, &r.ScreenID,
			&r.FlowID, &r.ProjectID, &r.ProjectSlug, &r.ProjectName, &r.Product,
			&r.FlowName,
			&r.RuleID, &r.Category, &r.Severity, &r.Property,
			&r.Observed, &r.Suggestion,
			&r.PersonaID, &r.ModeLabel,
			&autoFix, &r.Status, &r.CreatedAt,
		); err != nil {
			return nil, 0, err
		}
		r.AutoFixable = autoFix == 1
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return out, total, nil
}
