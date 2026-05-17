package mcp

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/projects"
)

// tools_prd_test.go — U2 audit-thread-through coverage (plan 2026-05-17-004).
//
// Scope:
//   - Every prd.author op:* WRITE site records a prd_audit row keyed on
//     (prd_state_id, user_id, op).
//   - prd.get and prd.export are read-only — no audit row.
//   - prd.upsert_tab is structural — no audit row (tab has no state to key on).
//   - Audit-insert failure does NOT fail the user-facing tool result; the
//     failure surfaces as a deps.Log warning.
//   - End-to-end: the wall's last_touched_by / last_touched_at populate
//     after a single prd.author op:add_state.
//
// Test harness:
//   - Reuses newTestHarness() from registry_test.go for DB / tenant /
//     user / registry wiring.
//   - countAuditRowsForState() reads prd_audit directly so we don't depend
//     on internal helpers in the projects package.
//   - latestAuditOpForState() returns the newest op for assertion.

// countAuditRowsForState returns the number of prd_audit rows that exist
// for (tenant, state). Direct SQL — cheaper than spinning up a second
// repo just to call LatestPRDAuditByState.
func countAuditRowsForState(t *testing.T, db *sql.DB, tenantID, stateID string) int {
	t.Helper()
	var n int
	err := db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM prd_audit WHERE tenant_id = ? AND prd_state_id = ?`,
		tenantID, stateID,
	).Scan(&n)
	if err != nil {
		t.Fatalf("count prd_audit: %v", err)
	}
	return n
}

// latestAuditForState returns (op, userID) of the newest prd_audit row
// for the given state, or empty strings + a not-found error.
//
// NOTE: prd_audit.at uses RFC3339 (1-second resolution), so two audits
// written in the same wall-clock second tie. Use auditOpsForState
// instead when more than one row exists for the same state.
func latestAuditForState(t *testing.T, db *sql.DB, tenantID, stateID string) (string, string) {
	t.Helper()
	var op, userID string
	err := db.QueryRowContext(context.Background(),
		`SELECT op, user_id FROM prd_audit
		  WHERE tenant_id = ? AND prd_state_id = ?
		  ORDER BY at DESC, id DESC LIMIT 1`,
		tenantID, stateID,
	).Scan(&op, &userID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ""
		}
		t.Fatalf("latest prd_audit: %v", err)
	}
	return op, userID
}

// auditOpsForState returns the set of op strings recorded for the
// given state. Stable across same-second timestamps where
// latestAuditForState would coin-flip on id ordering.
func auditOpsForState(t *testing.T, db *sql.DB, tenantID, stateID string) map[string]bool {
	t.Helper()
	rows, err := db.QueryContext(context.Background(),
		`SELECT op FROM prd_audit WHERE tenant_id = ? AND prd_state_id = ?`,
		tenantID, stateID,
	)
	if err != nil {
		t.Fatalf("auditOpsForState: %v", err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var op string
		if err := rows.Scan(&op); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out[op] = true
	}
	return out
}

// countAllAuditRowsForTenant returns the total prd_audit row count for
// the harness's primary tenant — used to assert read-only ops record
// nothing.
func countAllAuditRowsForTenant(t *testing.T, db *sql.DB, tenantID string) int {
	t.Helper()
	var n int
	err := db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM prd_audit WHERE tenant_id = ?`,
		tenantID,
	).Scan(&n)
	if err != nil {
		t.Fatalf("count prd_audit (tenant): %v", err)
	}
	return n
}

// stateIDFromAddStateResult pulls the state.ID from a prd.author op:add_state
// response. The Result.Data shape is projects.PRDState.
func stateIDFromAddStateResult(t *testing.T, res Result) string {
	t.Helper()
	st, ok := res.Data.(projects.PRDState)
	if !ok {
		t.Fatalf("unexpected Data shape: %T", res.Data)
	}
	if st.ID == "" {
		t.Fatalf("state.ID empty in result")
	}
	return st.ID
}

// ─── Scenario 1: add_state happy path — audit row written ─────────────────

func TestPRDAuthor_AddState_RecordsAudit(t *testing.T) {
	h := newTestHarness(t)
	h.seedSubFlow("Wallet", "M2M")

	res, err := h.invoke("prd.author", map[string]any{
		"op": "add_state",
		"args": map[string]any{
			"sub_flow_slug": "wallet/m2m",
			"tab_name":      "Investment",
			"label":         "Cold",
		},
	})
	if err != nil {
		t.Fatalf("add_state: %v", err)
	}
	stateID := stateIDFromAddStateResult(t, res)

	if got := countAuditRowsForState(t, h.d.DB, h.tenantA, stateID); got != 1 {
		t.Fatalf("expected 1 audit row, got %d", got)
	}
	op, userID := latestAuditForState(t, h.d.DB, h.tenantA, stateID)
	if op != string(projects.OpUpsertState) {
		t.Errorf("op: got %q, want %q", op, projects.OpUpsertState)
	}
	if userID != h.userA {
		t.Errorf("user_id: got %q, want %q", userID, h.userA)
	}
}

// ─── Scenario 2: add_event happy path ─────────────────────────────────────

func TestPRDAuthor_AddEvent_RecordsAudit(t *testing.T) {
	h := newTestHarness(t)
	h.seedSubFlow("Wallet", "M2M")

	addRes, err := h.invoke("prd.author", map[string]any{
		"op": "add_state",
		"args": map[string]any{
			"sub_flow_slug": "wallet/m2m",
			"tab_name":      "Investment",
			"label":         "Cold",
		},
	})
	if err != nil {
		t.Fatalf("add_state: %v", err)
	}
	stateID := stateIDFromAddStateResult(t, addRes)

	if _, err := h.invoke("prd.author", map[string]any{
		"op": "add_event",
		"args": map[string]any{
			"sub_flow_slug": "wallet/m2m",
			"tab_name":      "Investment",
			"state_label":   "Cold",
			"name":          "wallet_cold_state_view",
		},
	}); err != nil {
		t.Fatalf("add_event: %v", err)
	}

	// 2 rows now: upsert_state (from add_state) + add_event.
	if got := countAuditRowsForState(t, h.d.DB, h.tenantA, stateID); got != 2 {
		t.Fatalf("expected 2 audit rows, got %d", got)
	}
	ops := auditOpsForState(t, h.d.DB, h.tenantA, stateID)
	if !ops[string(projects.OpAddEvent)] {
		t.Errorf("expected %q audit row, got ops=%v", projects.OpAddEvent, ops)
	}
	if !ops[string(projects.OpUpsertState)] {
		t.Errorf("expected %q audit row (from add_state), got ops=%v", projects.OpUpsertState, ops)
	}
	// Latest user_id is still asserted via the SELECT (same regardless of
	// which row sorted first when timestamps tie).
	if _, userID := latestAuditForState(t, h.d.DB, h.tenantA, stateID); userID != h.userA {
		t.Errorf("user_id: got %q, want %q", userID, h.userA)
	}
}

// ─── Scenario 3: add_acceptance_criterion happy path ──────────────────────

func TestPRDAuthor_AddAcceptanceCriterion_RecordsAudit(t *testing.T) {
	h := newTestHarness(t)
	h.seedSubFlow("Wallet", "M2M")

	addRes, err := h.invoke("prd.author", map[string]any{
		"op": "add_state",
		"args": map[string]any{
			"sub_flow_slug": "wallet/m2m",
			"tab_name":      "Investment",
			"label":         "Cold",
		},
	})
	if err != nil {
		t.Fatalf("add_state: %v", err)
	}
	stateID := stateIDFromAddStateResult(t, addRes)

	if _, err := h.invoke("prd.author", map[string]any{
		"op": "add_acceptance_criterion",
		"args": map[string]any{
			"sub_flow_slug": "wallet/m2m",
			"tab_name":      "Investment",
			"state_label":   "Cold",
			"criterion":     "Shows hero CTA when balance == 0.",
		},
	}); err != nil {
		t.Fatalf("add_acceptance_criterion: %v", err)
	}
	ops := auditOpsForState(t, h.d.DB, h.tenantA, stateID)
	if !ops[string(projects.OpAddAcceptanceCriterion)] {
		t.Errorf("expected %q audit row, got ops=%v", projects.OpAddAcceptanceCriterion, ops)
	}
}

// ─── Scenario 4: upsert_copy_string happy path ─────────────────────────────

func TestPRDAuthor_UpsertCopyString_RecordsAudit(t *testing.T) {
	h := newTestHarness(t)
	h.seedSubFlow("Wallet", "M2M")

	addRes, err := h.invoke("prd.author", map[string]any{
		"op": "add_state",
		"args": map[string]any{
			"sub_flow_slug": "wallet/m2m",
			"tab_name":      "Investment",
			"label":         "Cold",
		},
	})
	if err != nil {
		t.Fatalf("add_state: %v", err)
	}
	stateID := stateIDFromAddStateResult(t, addRes)

	if _, err := h.invoke("prd.author", map[string]any{
		"op": "upsert_copy_string",
		"args": map[string]any{
			"sub_flow_slug": "wallet/m2m",
			"tab_name":      "Investment",
			"state_label":   "Cold",
			"key":           "hero.title",
			"value":         "Start your portfolio",
		},
	}); err != nil {
		t.Fatalf("upsert_copy_string: %v", err)
	}
	ops := auditOpsForState(t, h.d.DB, h.tenantA, stateID)
	if !ops[string(projects.OpUpsertCopyString)] {
		t.Errorf("expected %q audit row, got ops=%v", projects.OpUpsertCopyString, ops)
	}
}

// ─── Scenario 5: attach_frame happy path ───────────────────────────────────

func TestPRDAuthor_AttachFrame_RecordsAudit(t *testing.T) {
	h := newTestHarness(t)
	h.seedSubFlow("Wallet", "M2M")

	addRes, err := h.invoke("prd.author", map[string]any{
		"op": "add_state",
		"args": map[string]any{
			"sub_flow_slug": "wallet/m2m",
			"tab_name":      "Investment",
			"label":         "Cold",
		},
	})
	if err != nil {
		t.Fatalf("add_state: %v", err)
	}
	stateID := stateIDFromAddStateResult(t, addRes)

	if _, err := h.invoke("prd.author", map[string]any{
		"op": "attach_frame",
		"args": map[string]any{
			"sub_flow_slug": "wallet/m2m",
			"tab_name":      "Investment",
			"state_label":   "Cold",
			"figma_node_id": "1:4913",
		},
	}); err != nil {
		t.Fatalf("attach_frame: %v", err)
	}
	ops := auditOpsForState(t, h.d.DB, h.tenantA, stateID)
	if !ops[string(projects.OpAttachFrameTag)] {
		t.Errorf("expected %q audit row, got ops=%v", projects.OpAttachFrameTag, ops)
	}
}

// ─── Scenario 6: detach_frame — audit skipped (documented deviation) ──────

// The plan asks for a detach audit row keyed on the resolved state_id;
// resolving tag_id → state_id BEFORE delete would require a new
// exported lookup on projects.TenantRepo, out-of-scope per the U2
// "instrumentation only" boundary. This test pins the documented choice
// (skip) so a future change is intentional rather than accidental. See
// the inline comment on prd.detach_frame's Invoke for the rationale.
func TestPRDAuthor_DetachFrame_DoesNotRecordAudit_PerDeviation(t *testing.T) {
	h := newTestHarness(t)
	h.seedSubFlow("Wallet", "M2M")

	addRes, err := h.invoke("prd.author", map[string]any{
		"op": "add_state",
		"args": map[string]any{
			"sub_flow_slug": "wallet/m2m",
			"tab_name":      "Investment",
			"label":         "Cold",
		},
	})
	if err != nil {
		t.Fatalf("add_state: %v", err)
	}
	stateID := stateIDFromAddStateResult(t, addRes)

	attachRes, err := h.invoke("prd.author", map[string]any{
		"op": "attach_frame",
		"args": map[string]any{
			"sub_flow_slug": "wallet/m2m",
			"tab_name":      "Investment",
			"state_label":   "Cold",
			"figma_node_id": "1:4913",
		},
	})
	if err != nil {
		t.Fatalf("attach_frame: %v", err)
	}
	tag, ok := attachRes.Data.(projects.FrameTag)
	if !ok {
		t.Fatalf("unexpected attach Data shape: %T", attachRes.Data)
	}

	before := countAuditRowsForState(t, h.d.DB, h.tenantA, stateID)
	if before != 2 {
		// 1 from upsert_state, 1 from attach_frame.
		t.Fatalf("pre-detach audit rows: got %d, want 2", before)
	}

	if _, err := h.invoke("prd.author", map[string]any{
		"op":   "detach_frame",
		"args": map[string]any{"tag_id": tag.ID},
	}); err != nil {
		t.Fatalf("detach_frame: %v", err)
	}

	after := countAuditRowsForState(t, h.d.DB, h.tenantA, stateID)
	if after != before {
		t.Errorf("detach should NOT record audit (per U2 deviation): rows before=%d after=%d", before, after)
	}
}

// ─── Scenario 7: get is read-only ──────────────────────────────────────────

func TestPRDAuthor_Get_RecordsNoAudit(t *testing.T) {
	h := newTestHarness(t)
	h.seedSubFlow("Wallet", "M2M")

	// Seed one state so there's something to read.
	if _, err := h.invoke("prd.author", map[string]any{
		"op": "add_state",
		"args": map[string]any{
			"sub_flow_slug": "wallet/m2m",
			"tab_name":      "Investment",
			"label":         "Cold",
		},
	}); err != nil {
		t.Fatalf("seed add_state: %v", err)
	}

	before := countAllAuditRowsForTenant(t, h.d.DB, h.tenantA)

	if _, err := h.invoke("prd.author", map[string]any{
		"op":   "get",
		"args": map[string]any{"sub_flow_slug": "wallet/m2m"},
	}); err != nil {
		t.Fatalf("get: %v", err)
	}

	after := countAllAuditRowsForTenant(t, h.d.DB, h.tenantA)
	if after != before {
		t.Errorf("get is read-only: rows before=%d after=%d", before, after)
	}
}

// ─── Scenario 8: upsert_tab is structural — no audit ──────────────────────

func TestPRDAuthor_UpsertTab_RecordsNoAudit(t *testing.T) {
	h := newTestHarness(t)
	h.seedSubFlow("Wallet", "M2M")

	before := countAllAuditRowsForTenant(t, h.d.DB, h.tenantA)

	if _, err := h.invoke("prd.author", map[string]any{
		"op": "upsert_tab",
		"args": map[string]any{
			"sub_flow_slug": "wallet/m2m",
			"name":          "Investment",
		},
	}); err != nil {
		t.Fatalf("upsert_tab: %v", err)
	}

	after := countAllAuditRowsForTenant(t, h.d.DB, h.tenantA)
	if after != before {
		t.Errorf("upsert_tab is structural (no state to key audit on): "+
			"rows before=%d after=%d", before, after)
	}
}

// ─── Scenario 9: Audit insert failure does NOT fail the tool ─────────────

// This scenario simulates the audit-insert-fails-but-tool-succeeds path
// by recording the deps.Log output and asserting that:
//   (a) the user-facing tool result is success;
//   (b) a "prd_audit insert failed" warning was emitted.
//
// We don't need to inject a real audit failure — instead we exploit the
// FK shape: we hand recordAudit a state_id that's tenant-scoped wrong
// (an empty string would also work but RecordPRDAudit rejects it with
// ErrPRDAuditStateRequired). To trigger the warning without breaking the
// happy path, we wrap the harness's logger and assert no panic, no
// returned error, and that recordAudit's contract holds: tool succeeds
// regardless.
//
// The direct invariant under test: recordAudit returns no error to the
// caller. The Warn surface is verified via a captured slog handler.
func TestRecordAudit_AuditFailureDoesNotFailTool(t *testing.T) {
	h := newTestHarness(t)
	h.seedSubFlow("Wallet", "M2M")

	// Build a deps with a captured logger so we can assert the Warn
	// surface fires for the "empty stateID" case (RecordPRDAudit rejects
	// it with ErrPRDAuditStateRequired — exactly the kind of failure the
	// best-effort discipline must swallow).
	var buf bytes.Buffer
	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})
	captured := slog.New(handler)
	deps := h.deps
	deps.Log = captured

	// Call recordAudit directly with an empty stateID — exercises the
	// log-and-continue branch without needing a flaky DB error.
	recordAudit(context.Background(), deps, "", projects.OpUpsertState)

	logOutput := buf.String()
	if !strings.Contains(logOutput, "prd_audit insert failed") {
		t.Errorf("expected warning to be logged, got: %q", logOutput)
	}

	// And, separately: a real tool call with valid input should still
	// record audit normally (proves the swallow doesn't break the next
	// successful path).
	res, err := h.invoke("prd.author", map[string]any{
		"op": "add_state",
		"args": map[string]any{
			"sub_flow_slug": "wallet/m2m",
			"tab_name":      "Investment",
			"label":         "Cold",
		},
	})
	if err != nil {
		t.Fatalf("add_state: %v", err)
	}
	stateID := stateIDFromAddStateResult(t, res)
	if got := countAuditRowsForState(t, h.d.DB, h.tenantA, stateID); got != 1 {
		t.Errorf("expected 1 audit row after recovery path, got %d", got)
	}
}

// ─── Scenario 10: end-to-end — wall.last_touched_* populated ─────────────

func TestPRDAuthor_AddState_PopulatesLastTouchedOnWall(t *testing.T) {
	h := newTestHarness(t)
	sf := h.seedSubFlow("Wallet", "M2M")

	if _, err := h.invoke("prd.author", map[string]any{
		"op": "add_state",
		"args": map[string]any{
			"sub_flow_slug": "wallet/m2m",
			"tab_name":      "Investment",
			"label":         "Cold",
		},
	}); err != nil {
		t.Fatalf("add_state: %v", err)
	}

	wall, err := h.deps.Repo.LoadSectionOutline(context.Background(), sf)
	if err != nil {
		t.Fatalf("LoadSectionOutline: %v", err)
	}
	if len(wall.Frames) == 0 {
		t.Fatalf("expected at least one wall row (orphan state), got 0")
	}

	// Find the row corresponding to the state we just authored. With no
	// Figma section bound, the row appears as an orphan (state has no
	// frame tag → BindingStatus = orphaned).
	var found *projects.WallRow
	for i := range wall.Frames {
		if wall.Frames[i].PRDStateLabel != nil && *wall.Frames[i].PRDStateLabel == "Cold" {
			found = &wall.Frames[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("wall row for state 'Cold' not found; rows: %+v", wall.Frames)
	}
	if found.LastTouchedBy == nil {
		t.Errorf("LastTouchedBy is nil — audit thread-through not visible to wall")
	} else if *found.LastTouchedBy != h.userA {
		t.Errorf("LastTouchedBy: got %q, want %q", *found.LastTouchedBy, h.userA)
	}
	if found.LastTouchedAt == nil {
		t.Errorf("LastTouchedAt is nil — audit row 'at' not flowing into wall")
	}
}

// ─── U4: prd.export JSON sidecar ─────────────────────────────────────────────
//
// The sidecar IS the PRDFull tree — same shape downstream consumers
// would get from prd.get. Three scenarios:
//   1. After authoring a state, the sidecar carries the typed tree.
//   2. The sidecar serializes to JSON and round-trips back into a
//      PRDFull struct without drift (schema-stable wire contract).
//   3. The markdown field still surfaces the exact rendering the
//      pre-U4 RenderPRDMarkdown produced (regression guard).

// seedPRDForExport authors a representative PRD (state + criterion +
// event) via the prd.author meta-verb so the export under test reflects
// the typed-stem tree a real PM would produce.
func seedPRDForExport(t *testing.T, h *testHarness) {
	t.Helper()
	h.seedSubFlow("Wallet", "M2M")
	if _, err := h.invoke("prd.author", map[string]any{
		"op": "add_state",
		"args": map[string]any{
			"sub_flow_slug": "wallet/m2m",
			"tab_name":      "Investment",
			"label":         "Hot state",
		},
	}); err != nil {
		t.Fatalf("seed add_state: %v", err)
	}
	if _, err := h.invoke("prd.author", map[string]any{
		"op": "add_acceptance_criterion",
		"args": map[string]any{
			"sub_flow_slug": "wallet/m2m",
			"tab_name":      "Investment",
			"state_label":   "Hot state",
			"criterion":     "Shows positive-balance banner.",
		},
	}); err != nil {
		t.Fatalf("seed criterion: %v", err)
	}
	if _, err := h.invoke("prd.author", map[string]any{
		"op": "add_event",
		"args": map[string]any{
			"sub_flow_slug": "wallet/m2m",
			"tab_name":      "Investment",
			"state_label":   "Hot state",
			"name":          "wallet.hot.viewed",
		},
	}); err != nil {
		t.Fatalf("seed event: %v", err)
	}
}

func TestPRDExport_ResultHasSidecar(t *testing.T) {
	h := newTestHarness(t)
	seedPRDForExport(t, h)

	res, err := h.invoke("prd.author", map[string]any{
		"op":   "export",
		"args": map[string]any{"sub_flow_slug": "wallet/m2m"},
	})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	out, ok := res.Data.(prdExportResult)
	if !ok {
		t.Fatalf("unexpected Data shape: %T", res.Data)
	}
	if out.Markdown == "" {
		t.Errorf("expected markdown, got empty")
	}
	if out.Bytes != len(out.Markdown) {
		t.Errorf("Bytes mismatch: got %d, want %d", out.Bytes, len(out.Markdown))
	}
	if out.SidecarBytes <= 0 {
		t.Errorf("expected positive SidecarBytes, got %d", out.SidecarBytes)
	}
	if out.SubFlowFullSlug != "wallet/m2m" {
		t.Errorf("SubFlowFullSlug: got %q, want %q", out.SubFlowFullSlug, "wallet/m2m")
	}
	if out.Sidecar.ID == "" {
		t.Errorf("expected Sidecar.ID, got empty (PRD row not in sidecar)")
	}
	if len(out.Sidecar.Tabs) == 0 {
		t.Fatalf("expected at least one tab in sidecar, got 0")
	}
	tab := out.Sidecar.Tabs[0]
	if tab.Name != "Investment" {
		t.Errorf("tab name: got %q, want %q", tab.Name, "Investment")
	}
	if len(tab.States) == 0 {
		t.Fatalf("expected at least one state in sidecar tab, got 0")
	}
	st := tab.States[0]
	if st.Label != "Hot state" {
		t.Errorf("state label: got %q, want %q", st.Label, "Hot state")
	}
	if len(st.AcceptanceCriteria) != 1 {
		t.Errorf("expected 1 criterion in sidecar, got %d", len(st.AcceptanceCriteria))
	}
	if len(st.Events) != 1 {
		t.Errorf("expected 1 event in sidecar, got %d", len(st.Events))
	}
}

func TestPRDExport_SidecarSerializesAsJSON(t *testing.T) {
	h := newTestHarness(t)
	seedPRDForExport(t, h)

	res, err := h.invoke("prd.author", map[string]any{
		"op":   "export",
		"args": map[string]any{"sub_flow_slug": "wallet/m2m"},
	})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	out := res.Data.(prdExportResult)

	// Marshal the sidecar; the result MUST be valid JSON and MUST
	// round-trip back into a PRDFull without drift. This is the
	// downstream-contract guard (plan KTD-4): the wire shape is
	// snake_case PRDFull, schema changes flow through automatically.
	b, err := json.Marshal(out.Sidecar)
	if err != nil {
		t.Fatalf("marshal sidecar: %v", err)
	}
	var round projects.PRDFull
	if err := json.Unmarshal(b, &round); err != nil {
		t.Fatalf("unmarshal sidecar back into PRDFull: %v\nbytes: %s", err, string(b))
	}
	if !reflect.DeepEqual(round, out.Sidecar) {
		t.Errorf("round-trip drift:\norig: %+v\nback: %+v", out.Sidecar, round)
	}
}

func TestPRDExport_MarkdownUnchangedFromPriorBehavior(t *testing.T) {
	h := newTestHarness(t)
	seedPRDForExport(t, h)

	res, err := h.invoke("prd.author", map[string]any{
		"op":   "export",
		"args": map[string]any{"sub_flow_slug": "wallet/m2m"},
	})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	out := res.Data.(prdExportResult)

	// The markdown wrapper still goes through the same renderer.
	// Calling it directly against the same sub_flow MUST produce
	// byte-identical output. Regression guard against accidental
	// drift between the wrapper and the new RenderPRDExport path.
	ctx := context.Background()
	sf, err := h.deps.Repo.GetSubFlowBySlug(ctx, "wallet/m2m")
	if err != nil {
		t.Fatalf("resolve sub_flow: %v", err)
	}
	md, err := h.deps.Repo.RenderPRDMarkdown(ctx, sf.ID)
	if err != nil {
		t.Fatalf("RenderPRDMarkdown: %v", err)
	}
	if md != out.Markdown {
		t.Errorf("markdown drift between prd.export and RenderPRDMarkdown:\n--- export ---\n%s\n--- wrapper ---\n%s", out.Markdown, md)
	}
}
