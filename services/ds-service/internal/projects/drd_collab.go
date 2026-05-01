package projects

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Phase 5 U1 — Yjs collaboration auth bridge + snapshot persistence.
//
// The Hocuspocus sidecar runs on a private interface (loopback in dev,
// internal docker network in prod). Three endpoints connect it back to
// ds-service:
//
//   POST /v1/projects/:slug/flows/:flow_id/drd/ticket
//     Public; auth-gated. Issues a single-use ticket bound to
//     (user_id, tenant_id, flow_id) using the existing TicketStore.
//     Hocuspocus client redeems via WebSocket query param.
//
//   POST /internal/drd/auth
//     Loopback-only; shared-secret-gated. Hocuspocus calls this on each
//     handshake with the ticket; ds-service redeems + returns
//     {user_id, tenant_id, flow_id, role}.
//
//   GET  /internal/drd/load?flow_id=...
//     Loopback-only; shared-secret-gated. Returns the binary y_doc_state
//     for Hocuspocus to bootstrap a Y.Doc on first peer connect.
//
//   POST /internal/drd/snapshot
//     Loopback-only; shared-secret-gated. Hocuspocus posts the binary
//     Y.Doc state on debounced change + last-disconnect; ds-service
//     writes flow_drd.y_doc_state + bumps revision + writes audit_log.
//
// The HOCUSPOCUS_DRD_CHANNEL prefix is reserved on the SSE broker if we
// ever want to broadcast Y.Doc events out of band (Phase 7+).

// HocuspocusSharedSecret env var. Set in both ds-service and the
// Hocuspocus sidecar. The auth bridge rejects requests without this
// header value to keep the loopback endpoints from being abused.
const HocuspocusSharedSecretEnv = "DS_HOCUSPOCUS_SHARED_SECRET"

// MaxYDocBytes caps the binary state Hocuspocus may snapshot. Phase 5's
// budget is 5MB; flows that exceed it trigger an admin alert (Phase 7
// wires the alerting). 413 is returned to Hocuspocus.
const MaxYDocBytes = 5 << 20 // 5MB

// DRDAuthResult is what the auth bridge returns on a redeemed ticket.
type DRDAuthResult struct {
	UserID    string
	TenantID  string
	FlowID    string
	Role      string
	ProjectSlug string
}

// resolveDRDFlowID returns the flow's id if the user (claims) can edit
// or read it. The Phase 4 + Phase 5 trust boundary: tenant + role gate.
// Phase 7 ACL grants extend without changing this signature.
func (t *TenantRepo) resolveDRDFlowID(ctx context.Context, slug, flowID string) (projectSlug string, err error) {
	row := t.r.db.QueryRowContext(ctx,
		`SELECT p.slug
		   FROM flows f
		   JOIN projects p ON p.id = f.project_id
		  WHERE f.id = ? AND f.tenant_id = ? AND f.deleted_at IS NULL
		    AND p.slug = ?`,
		flowID, t.tenantID, slug,
	)
	if err := row.Scan(&projectSlug); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("resolve drd flow: %w", err)
	}
	return projectSlug, nil
}

// LoadYDocState returns the persisted binary Y.Doc for a flow inside a
// tenant. Returns nil + nil error when the flow has never had a snapshot
// (first-edit case — Hocuspocus initialises an empty Y.Doc).
func (t *TenantRepo) LoadYDocState(ctx context.Context, flowID string) ([]byte, error) {
	if err := t.assertFlowVisibleByID(ctx, flowID); err != nil {
		return nil, err
	}
	var state []byte
	err := t.r.db.QueryRowContext(ctx,
		`SELECT y_doc_state FROM flow_drd WHERE flow_id = ? AND tenant_id = ?`,
		flowID, t.tenantID,
	).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil // no snapshot yet — first-edit case
	}
	if err != nil {
		return nil, fmt.Errorf("load ydoc: %w", err)
	}
	return state, nil
}

// PersistYDocSnapshot writes the new Y.Doc binary state + bumps revision.
// Updates last_snapshot_at + updated_at. Caller is expected to have
// already validated MaxYDocBytes.
//
// content_json is intentionally NOT updated here — Hocuspocus is the
// source of truth for the live state; the legacy REST snapshot in
// content_json is rebuilt on demand by the existing fetchDRD path
// (Phase 5.1 wires the BlockNote-rendered JSON refresh). Phase 5 ships
// the binary path first; programmatic readers continue to see the
// pre-collab content_json until the next REST PUT replaces it.
func (t *TenantRepo) PersistYDocSnapshot(ctx context.Context, flowID, userID string, state []byte) (revision int64, err error) {
	if err := t.assertFlowVisibleByID(ctx, flowID); err != nil {
		return 0, err
	}
	if len(state) > MaxYDocBytes {
		return 0, fmt.Errorf("ydoc state %d bytes exceeds %d cap", len(state), MaxYDocBytes)
	}
	now := t.now().UTC()
	tx, err := t.r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Upsert: flow_drd may not have a row yet (first edit). content_json
	// is required NOT NULL in the schema; we seed with an empty BlockNote
	// document so the row satisfies the constraint.
	res, err := tx.ExecContext(ctx,
		`INSERT INTO flow_drd
		   (flow_id, tenant_id, content_json, revision, schema_version,
		    updated_at, updated_by_user_id, y_doc_state, last_snapshot_at)
		 VALUES (?, ?, ?, 1, 'phase5', ?, ?, ?, ?)
		 ON CONFLICT(flow_id) DO UPDATE SET
		   y_doc_state      = excluded.y_doc_state,
		   last_snapshot_at = excluded.last_snapshot_at,
		   revision         = flow_drd.revision + 1,
		   updated_at       = excluded.updated_at,
		   updated_by_user_id = excluded.updated_by_user_id`,
		flowID, t.tenantID,
		[]byte(`{}`), // empty BlockNote JSON for the seed row
		rfc3339(now), userID, state, rfc3339(now),
	)
	if err != nil {
		return 0, fmt.Errorf("persist ydoc: %w", err)
	}
	_ = res

	// Read back the bumped revision so the audit_log + caller see the
	// actual value.
	var rev int64
	if err := tx.QueryRowContext(ctx,
		`SELECT revision FROM flow_drd WHERE flow_id = ? AND tenant_id = ?`,
		flowID, t.tenantID,
	).Scan(&rev); err != nil {
		return 0, fmt.Errorf("read rev: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit ydoc: %w", err)
	}
	return rev, nil
}

// drdAuditEntry returns a typed audit_log row for a DRD snapshot event.
// Caller writes via DB.WriteAudit post-commit (best-effort).
type DRDAuditEntry struct {
	TenantID  string
	UserID    string
	FlowID    string
	Bytes     int
	Revision  int64
	TS        time.Time
}

// MakeDRDAuditEvent returns the audit_log.event_type slug for a DRD action.
func MakeDRDAuditEvent(action string) string {
	return "drd." + action
}
