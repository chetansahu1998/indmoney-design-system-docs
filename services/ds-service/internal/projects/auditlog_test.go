package projects

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/auth"
)

// bulkUpsertHelper invokes HandleBulkUpsertOverrides with the given items.
// Mirrors the inline pattern in screen_overrides_test.go without adding a
// shared helper there.
func bulkUpsertHelper(t *testing.T, fx *overrideFixture, tenantID, userID, slug string, items []bulkOverrideItem) *httptest.ResponseRecorder {
	t.Helper()
	bs, _ := json.Marshal(bulkUpsertOverridesRequest{Items: items})
	r := httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/v1/projects/%s/text-overrides/bulk", slug),
		bytes.NewReader(bs))
	r.SetPathValue("slug", slug)
	r = r.WithContext(WithClaims(context.Background(), &auth.Claims{Sub: userID, Tenants: []string{tenantID}}))
	w := httptest.NewRecorder()
	fx.server.HandleBulkUpsertOverrides(w, r)
	return w
}

// U10 — WriteOverrideEvent contract tests.
//
// The function centralises the audit_log details JSON shape across PUT,
// DELETE, bulk and orphan call sites; the activity-feed adapter
// (lib/atlas/data-adapters.ts) reads `details.old`, `.new`, `.flow_id`,
// `.bulk_id`, `.reason`. These tests pin those keys so a future refactor
// can't silently drop a field the frontend expects.

// loadDetails returns the parsed details JSON of the most recent audit_log
// row matching event_type. Mirrors auditDetailString in screen_overrides_test
// but parses the JSON for direct field assertions.
func loadDetails(t *testing.T, fx *overrideFixture, eventType string) map[string]any {
	t.Helper()
	var raw string
	if err := fx.dbHandle.QueryRowContext(context.Background(),
		`SELECT details FROM audit_log WHERE event_type = ? ORDER BY ts DESC LIMIT 1`,
		eventType,
	).Scan(&raw); err != nil {
		t.Fatalf("expected audit row for %s: %v", eventType, err)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("parse details %s: %v", eventType, err)
	}
	return out
}

// TestWriteOverrideEvent_SetEmitsOldNew covers the activity-feed contract:
// after a PUT the audit_log row carries `old` (= prior value or original
// Figma text) and `new` (= the just-saved value).
func TestWriteOverrideEvent_SetEmitsOldNew(t *testing.T) {
	fx := newOverrideFixture(t)

	if w := putOverride(t, fx.server, fx.slugA, fx.screenA, "node-aud-1", fx.tenantA, fx.userA,
		putOverrideRequest{
			Value:                "First",
			ExpectedRevision:     0,
			LastSeenOriginalText: "OriginalCopy",
		}); w.Code != 200 {
		t.Fatalf("first PUT: %d", w.Code)
	}
	d := loadDetails(t, fx, "override.text.set")
	if got := d["old"]; got != "OriginalCopy" {
		t.Errorf("first write: expected old=OriginalCopy, got %v", got)
	}
	if got := d["new"]; got != "First" {
		t.Errorf("first write: expected new=First, got %v", got)
	}
	if got := d["flow_id"]; got != fx.flowA {
		t.Errorf("expected flow_id=%s, got %v", fx.flowA, got)
	}

	// Update the same row — old should now reflect the prior value, not
	// the original Figma text.
	if w := putOverride(t, fx.server, fx.slugA, fx.screenA, "node-aud-1", fx.tenantA, fx.userA,
		putOverrideRequest{
			Value:            "Second",
			ExpectedRevision: 1,
		}); w.Code != 200 {
		t.Fatalf("second PUT: %d", w.Code)
	}
	d2 := loadDetails(t, fx, "override.text.set")
	if got := d2["old"]; got != "First" {
		t.Errorf("update: expected old=First, got %v", got)
	}
	if got := d2["new"]; got != "Second" {
		t.Errorf("update: expected new=Second, got %v", got)
	}
}

// TestWriteOverrideEvent_ResetEmitsOld verifies DELETE → reset event includes
// the value that was just removed, so the activity feed can render
// "{user} reset \"{old}\"".
func TestWriteOverrideEvent_ResetEmitsOld(t *testing.T) {
	fx := newOverrideFixture(t)

	if w := putOverride(t, fx.server, fx.slugA, fx.screenA, "node-reset-1", fx.tenantA, fx.userA,
		putOverrideRequest{Value: "ToBeReset", ExpectedRevision: 0}); w.Code != 200 {
		t.Fatalf("seed: %d", w.Code)
	}
	if w := deleteOverride(t, fx.server, fx.slugA, fx.screenA, "node-reset-1", fx.tenantA, fx.userA); w.Code != 204 {
		t.Fatalf("DELETE: %d", w.Code)
	}
	d := loadDetails(t, fx, "override.text.reset")
	if got := d["old"]; got != "ToBeReset" {
		t.Errorf("expected old=ToBeReset, got %v", got)
	}
	// `new` should be empty for resets.
	if got, _ := d["new"].(string); got != "" {
		t.Errorf("expected new to be empty for reset, got %q", got)
	}
}

// TestWriteOverrideEvent_BulkSetCarriesBulkID verifies bulk audit rows share
// a single `bulk_id` so the activity tab can group them into one user-facing
// "edited 14 strings" item.
func TestWriteOverrideEvent_BulkSetCarriesBulkID(t *testing.T) {
	fx := newOverrideFixture(t)

	// Issue a 2-row bulk PUT through the handler.
	items := []bulkOverrideItem{
		{ScreenID: fx.screenA, FigmaNodeID: "node-bulk-1", Value: "B1"},
		{ScreenID: fx.screenA, FigmaNodeID: "node-bulk-2", Value: "B2"},
	}
	w := bulkUpsertHelper(t, fx, fx.tenantA, fx.userA, fx.slugA, items)
	if w.Code != 200 {
		t.Fatalf("bulk PUT: %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		BulkID string `json:"bulk_id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.BulkID == "" {
		t.Fatal("expected bulk_id in response")
	}

	// Both rows must reference the same bulk_id.
	rows, err := fx.dbHandle.QueryContext(context.Background(),
		`SELECT details FROM audit_log WHERE event_type = ?`,
		"override.text.bulk_set",
	)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var n int
	for rows.Next() {
		n++
		var raw string
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var d map[string]any
		_ = json.Unmarshal([]byte(raw), &d)
		if got := d["bulk_id"]; got != resp.BulkID {
			t.Errorf("row %d: bulk_id mismatch: got %v want %s", n, got, resp.BulkID)
		}
	}
	if n != 2 {
		t.Fatalf("expected 2 bulk_set rows; got %d", n)
	}
}

// TestWriteOverrideEvent_OrphanedOmitsFlowID covers the edge case from §U10:
// re-import emits orphan events without flow context. The audit row must
// still appear (so tenant-wide queries see it) but `flow_id` is omitted —
// not "" — so JSON_EXTRACT(details, '$.flow_id') returns NULL, letting the
// per-flow activity tab silently filter it out.
func TestWriteOverrideEvent_OrphanedOmitsFlowID(t *testing.T) {
	fx := newOverrideFixture(t)
	ctx := context.Background()

	tx, err := fx.dbHandle.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := WriteOverrideEvent(ctx, tx, OverrideEvent{
		EventType:   AuditActionOverrideOrphaned,
		TenantID:    fx.tenantA,
		ScreenID:    fx.screenA,
		FigmaNodeID: "node-orphan-99",
		OldValue:    "stale copy",
		Reason:      "no node-id, path, or text match",
	}); err != nil {
		t.Fatalf("write event: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	d := loadDetails(t, fx, "override.text.orphaned")
	if _, present := d["flow_id"]; present {
		t.Errorf("flow_id should be omitted on orphan events, got %v", d["flow_id"])
	}
	if got := d["old"]; got != "stale copy" {
		t.Errorf("expected old=stale copy, got %v", got)
	}
	if got, _ := d["reason"].(string); !strings.Contains(got, "no node-id") {
		t.Errorf("expected reason to surface, got %q", got)
	}
}
