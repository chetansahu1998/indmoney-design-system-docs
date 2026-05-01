package projects

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/db"
)

// Phase 5 U12 — flow-activity query smoke. Validates that audit_log rows
// written with json_extract-able details.flow_id surface in the per-flow
// timeline + cross-tenant rows are filtered.

func writeAuditWithFlow(t *testing.T, d *db.DB, tenantID, userID, flowID, eventType string) {
	t.Helper()
	details, _ := json.Marshal(map[string]any{"flow_id": flowID})
	if err := d.WriteAudit(context.Background(), db.AuditEntry{
		ID:         uuid.NewString(),
		TS:         time.Now().UTC(),
		EventType:  eventType,
		TenantID:   tenantID,
		UserID:     userID,
		Method:     "POST",
		Endpoint:   "/v1/test",
		StatusCode: 200,
		Details:    string(details),
	}); err != nil {
		t.Fatalf("write audit: %v", err)
	}
}

func TestFlowActivity_FiltersByFlowID(t *testing.T) {
	d, tA, tB, uA := newTestDB(t)
	repoA := NewTenantRepo(d.DB, tA)
	versionID, _ := seedFlowAndScreens(t, repoA, uA)
	var flowAID string
	if err := d.DB.QueryRow(`SELECT flow_id FROM screens WHERE version_id = ? LIMIT 1`, versionID).Scan(&flowAID); err != nil {
		t.Fatalf("flow_id: %v", err)
	}
	writeAuditWithFlow(t, d, tA, uA, flowAID, "decision.created")
	writeAuditWithFlow(t, d, tA, uA, "other-flow", "decision.created")

	// Tenant B's audit row referencing flowAID — must not leak.
	writeAuditWithFlow(t, d, tB, uA, flowAID, "decision.created")

	// Direct DB query mirroring HandleFlowActivity's path.
	rows, err := d.DB.QueryContext(context.Background(),
		`SELECT id FROM audit_log
		  WHERE tenant_id = ?
		    AND json_valid(details)
		    AND json_extract(details, '$.flow_id') = ?`,
		tA, flowAID,
	)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		count++
	}
	if count != 1 {
		t.Errorf("expected 1 row for tenant A + flowAID, got %d", count)
	}
}
