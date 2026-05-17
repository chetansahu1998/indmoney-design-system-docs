package projects

// drd_anchor.go — plan 005 Phase B.
//
// Anchors that bind a DRD BlockNote block to a prototype screen for
// Atlas's PrototypeAnchorBridge. Three persistence APIs:
//
//   - AttachDRDAnchor(sub_flow_id, block_id, screen_id, created_by)
//     Idempotent: re-attaching an existing (block_id, screen_id) pair
//     is a no-op (UNIQUE index swallows the conflict via INSERT OR
//     IGNORE).
//
//   - DetachDRDAnchor(sub_flow_id, block_id, screen_id)
//     Deletes the row. No-op when the anchor doesn't exist.
//
//   - ListDRDAnchorsForSubFlow(sub_flow_id) → []DRDAnchor
//     The bridge's primary lookup path. Returned newest-first so a
//     future "show latest anchor" feature can use [0] without sorting.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// ErrDRDAnchorFieldRequired is returned when AttachDRDAnchor or
// DetachDRDAnchor is called with an empty sub_flow_id, block_id, or
// screen_id. Surfaced as a real error rather than a no-op so the MCP
// caller can log the bug instead of silently swallowing it.
var ErrDRDAnchorFieldRequired = errors.New("drd_anchor: sub_flow_id, block_id, and screen_id required")

// DRDAnchor is one row of the drd_anchor table.
type DRDAnchor struct {
	ID         string `json:"id"`
	TenantID   string `json:"tenant_id"`
	SubFlowID  string `json:"sub_flow_id"`
	BlockID    string `json:"block_id"`
	ScreenID   string `json:"screen_id"`
	CreatedAt  string `json:"created_at"`
	CreatedBy  string `json:"created_by,omitempty"`
}

// AttachDRDAnchor inserts (or refreshes) a (sub_flow_id, block_id,
// screen_id) anchor row. Returns the created row's id; when the anchor
// already exists, returns the existing row's id so callers can rely on
// a stable identifier.
func (t *TenantRepo) AttachDRDAnchor(ctx context.Context, subFlowID, blockID, screenID, createdBy string) (string, error) {
	if t.tenantID == "" {
		return "", errors.New("drd_anchor: tenant_id required")
	}
	subFlowID = strings.TrimSpace(subFlowID)
	blockID = strings.TrimSpace(blockID)
	screenID = strings.TrimSpace(screenID)
	if subFlowID == "" || blockID == "" || screenID == "" {
		return "", ErrDRDAnchorFieldRequired
	}
	id := uuid.NewString()
	_, err := t.handle().ExecContext(ctx,
		`INSERT INTO drd_anchor (id, tenant_id, sub_flow_id, block_id, screen_id, created_by)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT (tenant_id, sub_flow_id, block_id, screen_id) DO NOTHING`,
		id, t.tenantID, subFlowID, blockID, screenID, strings.TrimSpace(createdBy),
	)
	if err != nil {
		return "", fmt.Errorf("insert drd_anchor: %w", err)
	}
	// On conflict we kept the existing row — return its id.
	var existingID string
	if scanErr := t.readHandle().QueryRowContext(ctx,
		`SELECT id FROM drd_anchor
		  WHERE tenant_id = ? AND sub_flow_id = ? AND block_id = ? AND screen_id = ?`,
		t.tenantID, subFlowID, blockID, screenID,
	).Scan(&existingID); scanErr == nil {
		return existingID, nil
	}
	return id, nil
}

// DetachDRDAnchor removes one anchor row. No error when the row
// doesn't exist — the caller's intent ("there should be no anchor
// here") is satisfied either way.
func (t *TenantRepo) DetachDRDAnchor(ctx context.Context, subFlowID, blockID, screenID string) error {
	if t.tenantID == "" {
		return errors.New("drd_anchor: tenant_id required")
	}
	subFlowID = strings.TrimSpace(subFlowID)
	blockID = strings.TrimSpace(blockID)
	screenID = strings.TrimSpace(screenID)
	if subFlowID == "" || blockID == "" || screenID == "" {
		return ErrDRDAnchorFieldRequired
	}
	_, err := t.handle().ExecContext(ctx,
		`DELETE FROM drd_anchor
		  WHERE tenant_id = ? AND sub_flow_id = ? AND block_id = ? AND screen_id = ?`,
		t.tenantID, subFlowID, blockID, screenID,
	)
	if err != nil {
		return fmt.Errorf("delete drd_anchor: %w", err)
	}
	return nil
}

// ListDRDAnchorsForSubFlow returns every anchor under a sub_flow,
// newest-first. The bridge consumes this on leaf-open so the screen-id
// → block-id lookup is a synchronous Map access.
func (t *TenantRepo) ListDRDAnchorsForSubFlow(ctx context.Context, subFlowID string) ([]DRDAnchor, error) {
	if t.tenantID == "" {
		return nil, errors.New("drd_anchor: tenant_id required")
	}
	subFlowID = strings.TrimSpace(subFlowID)
	if subFlowID == "" {
		return nil, nil
	}
	rows, err := t.readHandle().QueryContext(ctx,
		`SELECT id, tenant_id, sub_flow_id, block_id, screen_id, created_at, COALESCE(created_by,'')
		   FROM drd_anchor
		  WHERE tenant_id = ? AND sub_flow_id = ?
		  ORDER BY created_at DESC, id DESC`,
		t.tenantID, subFlowID,
	)
	if err != nil {
		return nil, fmt.Errorf("list drd_anchor: %w", err)
	}
	defer rows.Close()
	out := make([]DRDAnchor, 0, 16)
	for rows.Next() {
		var a DRDAnchor
		if err := rows.Scan(&a.ID, &a.TenantID, &a.SubFlowID, &a.BlockID, &a.ScreenID, &a.CreatedAt, &a.CreatedBy); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
