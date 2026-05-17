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

// prd_audit.go — append-only audit log of writes to prd_state and its
// typed stems. Backs the coverage wall's `last_touched_by` /
// `last_touched_at` columns (U6b of plan 002).
//
// Threading contract (per Execution Notes §B.7 — tenant scoping):
//   Every prd.* write tool in internal/mcp/tools_prd.go calls
//   RecordPRDAudit(ctx, stateID, userID, op) after a successful write.
//   The call is best-effort: a failed audit is logged and swallowed at
//   the MCP layer, never bubbled to the user (a missing audit row is
//   strictly less bad than a refused edit).
//
// Auto-skeleton writes (U2b autosync) do NOT record audits — those
// originate from the autosync poller, not a PM. Threading them in would
// pollute `last_touched_by` with system-shaped values and obscure the
// "who actually authored this?" answer the wall needs.

// PRDAuditOp is the closed vocabulary of op strings stored in
// prd_audit.op. Kept as a typed alias of string so callers can pass the
// constant by reference and the compiler catches typos.
type PRDAuditOp string

const (
	OpUpsertState            PRDAuditOp = "upsert_state"
	OpAddAcceptanceCriterion PRDAuditOp = "add_acceptance_criterion"
	OpAddEdgeCase            PRDAuditOp = "add_edge_case"
	OpUpsertCopyString       PRDAuditOp = "upsert_copy_string"
	OpAddEvent               PRDAuditOp = "add_event"
	OpAddA11yNote            PRDAuditOp = "add_a11y_note"
	OpAttachFrameTag         PRDAuditOp = "attach_frame"
	OpDetachFrameTag         PRDAuditOp = "detach_frame"
)

// PRDAudit is one row of the prd_audit table.
type PRDAudit struct {
	ID         string     `json:"id"`
	TenantID   string     `json:"tenant_id"`
	PRDStateID string     `json:"prd_state_id"`
	UserID     string     `json:"user_id"`
	Op         PRDAuditOp `json:"op"`
	At         time.Time  `json:"at"`
}

// ErrPRDAuditStateRequired is returned when RecordPRDAudit is called
// with an empty stateID. We surface a real error rather than no-op so
// callers in tools_prd.go can log the bug instead of silently swallowing
// it.
var ErrPRDAuditStateRequired = errors.New("prd_audit: prd_state_id required")

// RecordPRDAudit inserts one row into prd_audit. Tenant-scoped via the
// repo binding. The MCP layer treats the return value as best-effort
// (log + continue) — a refused audit must not fail the user's edit.
//
// Returns ErrPRDAuditStateRequired when stateID is empty. The FK on
// (tenant_id, prd_state_id) means a stateID that does not exist in this
// tenant surfaces as a SQLite FK violation — also best-effort at the
// caller. We do not pre-check existence here because the read would
// double the write-path cost for a log that is itself best-effort.
func (t *TenantRepo) RecordPRDAudit(ctx context.Context, stateID, userID string, op PRDAuditOp) error {
	if t.tenantID == "" {
		return errors.New("prd_audit: tenant_id required")
	}
	stateID = strings.TrimSpace(stateID)
	if stateID == "" {
		return ErrPRDAuditStateRequired
	}
	userID = strings.TrimSpace(userID)
	if userID == "" {
		// Empty user id is allowed (system-emitted; rare on this path).
		// Stored as the empty string so a follow-up query can distinguish
		// "anonymous" from "user X" without a NULL/empty ambiguity.
		userID = ""
	}
	id := uuid.NewString()
	at := t.now().UTC()
	_, err := t.handle().ExecContext(ctx,
		`INSERT INTO prd_audit (id, tenant_id, prd_state_id, user_id, op, at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		id, t.tenantID, stateID, userID, string(op), rfc3339(at),
	)
	if err != nil {
		return fmt.Errorf("insert prd_audit: %w", err)
	}
	return nil
}

// LatestPRDAuditByState returns the most-recent prd_audit row for the
// supplied state IDs. Result is keyed by prd_state_id. States with no
// audit row are absent from the map.
//
// Single read against the read pool — used by the coverage wall load
// path. Uses a window-style query: pulls the top row per state ordered
// by (at DESC, id DESC).
func (t *TenantRepo) LatestPRDAuditByState(ctx context.Context, stateIDs []string) (map[string]PRDAudit, error) {
	if t.tenantID == "" {
		return nil, errors.New("prd_audit: tenant_id required")
	}
	if len(stateIDs) == 0 {
		return map[string]PRDAudit{}, nil
	}
	// Pull ALL audit rows for the state set, sorted newest-first, then
	// pick the first row per state in Go. SQLite supports DISTINCT-ON
	// poorly; a sorted scan + first-wins in Go is the cheapest cross-
	// engine pattern, and the audit table is bounded by author actions
	// (small per state).
	q, args := buildInQuery(
		`SELECT id, tenant_id, prd_state_id, user_id, op, at
		   FROM prd_audit
		  WHERE tenant_id = ? AND prd_state_id IN `,
		` ORDER BY prd_state_id, at DESC, id DESC`,
		t.tenantID, stateIDs,
	)
	rows, err := t.readHandle().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("load prd_audit: %w", err)
	}
	defer rows.Close()

	out := make(map[string]PRDAudit, len(stateIDs))
	for rows.Next() {
		var a PRDAudit
		var op, at string
		if err := rows.Scan(&a.ID, &a.TenantID, &a.PRDStateID, &a.UserID, &op, &at); err != nil {
			return nil, err
		}
		a.Op = PRDAuditOp(op)
		a.At = parseTime(at)
		// First row wins (we sorted newest-first within each state).
		if _, seen := out[a.PRDStateID]; !seen {
			out[a.PRDStateID] = a
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate prd_audit: %w", err)
	}
	return out, nil
}

// listPRDAuditForState is a small helper used by tests to read back the
// audit history of one state. Returned newest-first.
//
// NOT exported: callers that need a real history view should add a
// purpose-built method when the use case appears — this exists only so
// the test file can verify ordering without touching SQL directly.
func (t *TenantRepo) listPRDAuditForState(ctx context.Context, stateID string) ([]PRDAudit, error) {
	rows, err := t.readHandle().QueryContext(ctx,
		`SELECT id, tenant_id, prd_state_id, user_id, op, at
		   FROM prd_audit
		  WHERE tenant_id = ? AND prd_state_id = ?
		  ORDER BY at DESC, id DESC`,
		t.tenantID, stateID,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	defer rows.Close()
	var out []PRDAudit
	for rows.Next() {
		var a PRDAudit
		var op, at string
		if err := rows.Scan(&a.ID, &a.TenantID, &a.PRDStateID, &a.UserID, &op, &at); err != nil {
			return nil, err
		}
		a.Op = PRDAuditOp(op)
		a.At = parseTime(at)
		out = append(out, a)
	}
	return out, rows.Err()
}
