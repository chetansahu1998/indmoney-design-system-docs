package projects

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Phase 7 U1 — per-flow ACL grants.
//
// Resolution rule:
//   effective_role = MAX(product_default_role, flow_grants.role)
// where MAX honours the precedence ladder
//   viewer < commenter < editor < owner < admin.
//
// Admins always win; their role comes from the JWT claim, not from
// flow_grants. The grant table only records role bumps for users whose
// product-default role is below what they need on a specific flow.

// FlowRole values in precedence order.
type FlowRole string

const (
	FlowRoleViewer    FlowRole = "viewer"
	FlowRoleCommenter FlowRole = "commenter"
	FlowRoleEditor    FlowRole = "editor"
	FlowRoleOwner     FlowRole = "owner"
	FlowRoleAdmin     FlowRole = "admin"
)

// flowRoleRank maps roles to their precedence index. Higher = more powerful.
// Unknown roles return -1 so MAX(role, "") never wins.
func flowRoleRank(r FlowRole) int {
	switch r {
	case FlowRoleViewer:
		return 1
	case FlowRoleCommenter:
		return 2
	case FlowRoleEditor:
		return 3
	case FlowRoleOwner:
		return 4
	case FlowRoleAdmin:
		return 5
	}
	return 0
}

// MaxFlowRole returns the higher-precedence of two roles. Empty / unknown
// roles are treated as the lowest. Used by ResolveFlowRole to combine the
// product-default role with the per-flow grant.
func MaxFlowRole(a, b FlowRole) FlowRole {
	if flowRoleRank(b) > flowRoleRank(a) {
		return b
	}
	return a
}

// ─── Public API ──────────────────────────────────────────────────────────

// FlowGrant is one row of the flow_grants table, fetched via GetFlowGrants.
type FlowGrant struct {
	FlowID    string
	UserID    string
	Role      FlowRole
	GrantedBy string
	GrantedAt time.Time
}

// ResolveFlowRole returns the effective role of (user, flow) per the
// precedence rule above. The product-default role is determined by the
// caller (typically derived from auth.Claims.Role); we only handle the
// flow_grants override and the MAX combine.
//
// Cross-tenant lookups return FlowRoleViewer effectively zero — the row
// won't be found because flow_grants is tenant-scoped via the flow's
// own tenant_id.
func (t *TenantRepo) ResolveFlowRole(ctx context.Context, userID, flowID string, productDefault FlowRole) (FlowRole, error) {
	if t.tenantID == "" {
		return "", errors.New("acl: tenant_id required")
	}
	if userID == "" || flowID == "" {
		return productDefault, nil
	}
	var roleStr string
	err := t.r.db.QueryRowContext(ctx,
		`SELECT g.role
		   FROM flow_grants g
		   JOIN flows f ON f.id = g.flow_id
		  WHERE g.flow_id = ?
		    AND g.user_id = ?
		    AND g.revoked_at IS NULL
		    AND f.tenant_id = ?
		    AND f.deleted_at IS NULL`,
		flowID, userID, t.tenantID,
	).Scan(&roleStr)
	if errors.Is(err, sql.ErrNoRows) {
		return productDefault, nil
	}
	if err != nil {
		return "", fmt.Errorf("acl resolve: %w", err)
	}
	return MaxFlowRole(productDefault, FlowRole(roleStr)), nil
}

// GrantFlowRole creates or refreshes a grant. If a row already exists for
// (flow, user) it's updated in place + an audit_log entry is written
// regardless. Idempotent: granting the same role to the same user is a
// no-op write that still produces an audit row (per Phase 4 lifecycle
// pattern — every state-touching call leaves a trail).
func (t *TenantRepo) GrantFlowRole(ctx context.Context, flowID, userID, granterUserID string, role FlowRole) error {
	if t.tenantID == "" {
		return errors.New("acl: tenant_id required")
	}
	if flowRoleRank(role) == 0 {
		return fmt.Errorf("acl: invalid role %q", role)
	}
	tx, err := t.r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := t.now().UTC()

	// Validate flow belongs to tenant before touching the grant table.
	var flowTenant string
	err = tx.QueryRowContext(ctx,
		`SELECT tenant_id FROM flows WHERE id = ? AND deleted_at IS NULL`,
		flowID,
	).Scan(&flowTenant)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("acl grant lookup flow: %w", err)
	}
	if flowTenant != t.tenantID {
		return ErrNotFound // cross-tenant 404
	}

	// Upsert: SQLite ON CONFLICT clause keys on the composite PK.
	_, err = tx.ExecContext(ctx,
		`INSERT INTO flow_grants (flow_id, user_id, tenant_id, role, granted_by, granted_at, revoked_at)
		 VALUES (?, ?, ?, ?, ?, ?, NULL)
		 ON CONFLICT(flow_id, user_id) DO UPDATE
		   SET role        = excluded.role,
		       granted_by  = excluded.granted_by,
		       granted_at  = excluded.granted_at,
		       revoked_at  = NULL`,
		flowID, userID, t.tenantID, string(role), granterUserID, rfc3339(now),
	)
	if err != nil {
		return fmt.Errorf("acl grant upsert: %w", err)
	}

	// Audit-log entry — Phase 4 pattern. Action=grant, target=flow_id, actor=granterUserID.
	if err := writeACLAuditLog(ctx, tx, t.tenantID, granterUserID, flowID, "grant", string(role), now); err != nil {
		return err
	}
	return tx.Commit()
}

// RevokeFlowRole soft-revokes a grant by setting revoked_at. The row stays
// in place for audit. Re-granting the same user later does an UPDATE that
// clears revoked_at (see GrantFlowRole's ON CONFLICT clause).
func (t *TenantRepo) RevokeFlowRole(ctx context.Context, flowID, userID, revokerUserID string) error {
	if t.tenantID == "" {
		return errors.New("acl: tenant_id required")
	}
	tx, err := t.r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := t.now().UTC()

	res, err := tx.ExecContext(ctx,
		`UPDATE flow_grants
		    SET revoked_at = ?
		  WHERE flow_id = ? AND user_id = ? AND tenant_id = ? AND revoked_at IS NULL`,
		rfc3339(now), flowID, userID, t.tenantID,
	)
	if err != nil {
		return fmt.Errorf("acl revoke: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound // either no active grant, or cross-tenant
	}
	if err := writeACLAuditLog(ctx, tx, t.tenantID, revokerUserID, flowID, "revoke", "", now); err != nil {
		return err
	}
	return tx.Commit()
}

// ListFlowGrants returns every active grant on a flow. Used by the
// AccessPanel UI on the project view.
func (t *TenantRepo) ListFlowGrants(ctx context.Context, flowID string) ([]FlowGrant, error) {
	if t.tenantID == "" {
		return nil, errors.New("acl: tenant_id required")
	}
	rows, err := t.r.db.QueryContext(ctx,
		`SELECT g.flow_id, g.user_id, g.role, g.granted_by, g.granted_at
		   FROM flow_grants g
		   JOIN flows f ON f.id = g.flow_id
		  WHERE g.flow_id = ?
		    AND g.revoked_at IS NULL
		    AND f.tenant_id = ?
		    AND f.deleted_at IS NULL
		  ORDER BY g.granted_at DESC`,
		flowID, t.tenantID,
	)
	if err != nil {
		return nil, fmt.Errorf("acl list: %w", err)
	}
	defer rows.Close()
	var out []FlowGrant
	for rows.Next() {
		var g FlowGrant
		var grantedAt string
		var role string
		if err := rows.Scan(&g.FlowID, &g.UserID, &role, &g.GrantedBy, &grantedAt); err != nil {
			return nil, fmt.Errorf("acl scan: %w", err)
		}
		g.Role = FlowRole(role)
		g.GrantedAt = parseTime(grantedAt)
		out = append(out, g)
	}
	return out, rows.Err()
}

// writeACLAuditLog drops a single row in audit_log with the action verb
// (grant | revoke) and the target flow_id. Schema lives in
// internal/db/db.go; columns we populate: id, ts, event_type, tenant_id,
// user_id, details (JSON-encoded payload). HTTP-only columns (method,
// endpoint, status_code, etc.) stay NULL — this is a domain event, not
// an HTTP request log.
func writeACLAuditLog(ctx context.Context, tx *sql.Tx, tenantID, actorID, flowID, action, role string, at time.Time) error {
	details := fmt.Sprintf(`{"flow_id":%q,"role":%q,"action":%q}`, flowID, role, action)
	_, err := tx.ExecContext(ctx,
		`INSERT INTO audit_log (id, ts, event_type, tenant_id, user_id, details)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		uuid.NewString(), rfc3339(at), "flow.acl_"+action,
		tenantID, actorID, details,
	)
	if err != nil {
		return fmt.Errorf("acl audit log: %w", err)
	}
	return nil
}
