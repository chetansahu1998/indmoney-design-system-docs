package projects

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/auth"
)

// ─── Pure transition-validator tests ─────────────────────────────────────────

func TestValidateTransition_Table(t *testing.T) {
	cases := []struct {
		name        string
		from        string
		action      LifecycleAction
		role        string
		reason      string
		systemActor bool
		wantTo      string
		wantErr     error
	}{
		{
			name:    "active→acknowledged with reason as designer",
			from:    "active", action: ActionAcknowledge, role: auth.RoleDesigner,
			reason: "deferred to v2", wantTo: "acknowledged",
		},
		{
			name:    "active→dismissed with reason as engineer",
			from:    "active", action: ActionDismiss, role: auth.RoleEngineer,
			reason: "logged-out persona doesn't apply", wantTo: "dismissed",
		},
		{
			name:    "active→acknowledged missing reason",
			from:    "active", action: ActionAcknowledge, role: auth.RoleDesigner,
			reason: "", wantErr: ErrReasonRequired,
		},
		{
			name:    "active→dismissed missing reason",
			from:    "active", action: ActionDismiss, role: auth.RoleDesigner,
			reason: "   ", wantErr: ErrReasonRequired,
		},
		{
			name:    "active→acknowledged reason too long",
			from:    "active", action: ActionAcknowledge, role: auth.RoleDesigner,
			reason: strings.Repeat("a", MaxReasonLen+1), wantErr: ErrReasonTooLong,
		},
		{
			name:    "acknowledged→active by designer is forbidden",
			from:    "acknowledged", action: ActionReactivate, role: auth.RoleDesigner,
			wantErr: ErrForbiddenRole,
		},
		{
			name:    "acknowledged→active by tenant admin succeeds",
			from:    "acknowledged", action: ActionReactivate, role: auth.RoleTenantAdmin,
			wantTo: "active",
		},
		{
			name:    "dismissed→active by super admin succeeds",
			from:    "dismissed", action: ActionReactivate, role: auth.RoleSuperAdmin,
			wantTo: "active",
		},
		{
			name:    "active→active via reactivate (admin) is rejected",
			from:    "active", action: ActionReactivate, role: auth.RoleTenantAdmin,
			wantErr: ErrInvalidTransition,
		},
		{
			name:    "fixed→acknowledged is rejected (terminal)",
			from:    "fixed", action: ActionAcknowledge, role: auth.RoleDesigner,
			reason: "anything", wantErr: ErrInvalidTransition,
		},
		{
			name:    "active→fixed by designer is forbidden (system-only)",
			from:    "active", action: ActionMarkFixed, role: auth.RoleDesigner,
			wantErr: ErrForbiddenRole,
		},
		{
			name:    "active→fixed by system actor succeeds",
			from:    "active", action: ActionMarkFixed, role: auth.RoleDesigner,
			systemActor: true, wantTo: "fixed",
		},
		{
			name:    "acknowledged→fixed by system actor succeeds",
			from:    "acknowledged", action: ActionMarkFixed, role: auth.RoleSuperAdmin,
			systemActor: true, wantTo: "fixed",
		},
		{
			name:    "fixed→fixed by system actor is rejected (idempotent guard)",
			from:    "fixed", action: ActionMarkFixed, role: auth.RoleSuperAdmin,
			systemActor: true, wantErr: ErrInvalidTransition,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tr, err := ValidateTransition(c.from, c.action, c.role, c.reason, c.systemActor)
			if c.wantErr != nil {
				if err == nil {
					t.Fatalf("expected error %v, got nil (transition=%+v)", c.wantErr, tr)
				}
				if !errors.Is(err, c.wantErr) {
					t.Fatalf("expected %v, got %v", c.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tr.To != c.wantTo {
				t.Errorf("expected to=%q, got %q", c.wantTo, tr.To)
			}
			if tr.From != strings.ToLower(strings.TrimSpace(c.from)) {
				t.Errorf("from not preserved: %q", tr.From)
			}
		})
	}
}

func TestParseLifecycleAction(t *testing.T) {
	cases := []struct {
		in   string
		want LifecycleAction
		err  bool
	}{
		{"acknowledge", ActionAcknowledge, false},
		{"  Dismiss  ", ActionDismiss, false},
		{"REACTIVATE", ActionReactivate, false},
		{"mark_fixed", ActionMarkFixed, false},
		{"approve", "", true},
		{"", "", true},
	}
	for _, c := range cases {
		got, err := ParseLifecycleAction(c.in)
		if c.err {
			if err == nil {
				t.Errorf("expected error for %q, got %v", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: unexpected error %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("%q: expected %q, got %q", c.in, c.want, got)
		}
	}
}

func TestAuditEventType_PerAction(t *testing.T) {
	cases := []struct {
		action LifecycleAction
		want   string
	}{
		{ActionAcknowledge, "violation.acknowledge"},
		{ActionDismiss, "violation.dismiss"},
		{ActionReactivate, "violation.reactivate"},
		{ActionMarkFixed, "violation.mark_fixed"},
	}
	for _, c := range cases {
		got := LifecycleTransition{Action: c.action}.AuditEventType()
		if got != c.want {
			t.Errorf("action=%s: expected %q, got %q", c.action, c.want, got)
		}
	}
}

// ─── Repository integration ─────────────────────────────────────────────────

// seedViolation inserts a single violation row directly so the lifecycle
// tests don't depend on the worker-pool seeding flow.
func seedViolation(t *testing.T, repo *TenantRepo, versionID, screenID, tenantID string) string {
	t.Helper()
	id := uuid.NewString()
	_, err := repo.DB().ExecContext(context.Background(),
		`INSERT INTO violations (id, version_id, screen_id, tenant_id, rule_id, severity, property, status, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, 'active', ?)`,
		id, versionID, screenID, tenantID,
		"theme_parity.fill", "high", "fill",
		time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		t.Fatalf("seed violation: %v", err)
	}
	return id
}

func TestRepo_UpdateViolationStatus_HappyPath(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	versionID, screens := seedFlowAndScreens(t, repo, uA)
	vID := seedViolation(t, repo, versionID, screens[0], tA)

	cur, err := repo.GetViolationForLifecycle(context.Background(), vID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cur.From != "active" {
		t.Fatalf("expected active, got %q", cur.From)
	}

	tr, err := ValidateTransition(cur.From, ActionAcknowledge, auth.RoleDesigner, "deferred", false)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}

	auditCalled := false
	err = repo.UpdateViolationStatus(context.Background(), vID, tr, func(tx *sql.Tx) error {
		auditCalled = true
		return nil
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if !auditCalled {
		t.Errorf("audit writer should have been called")
	}

	// Verify status flipped.
	var got string
	if err := d.DB.QueryRowContext(context.Background(),
		`SELECT status FROM violations WHERE id = ?`, vID).Scan(&got); err != nil {
		t.Fatalf("readback: %v", err)
	}
	if got != "acknowledged" {
		t.Errorf("expected acknowledged, got %q", got)
	}
}

func TestRepo_UpdateViolationStatus_AuditFailureRollsBack(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	versionID, screens := seedFlowAndScreens(t, repo, uA)
	vID := seedViolation(t, repo, versionID, screens[0], tA)

	tr := LifecycleTransition{From: "active", To: "acknowledged", Action: ActionAcknowledge, Reason: "x"}
	wantErr := errors.New("audit boom")
	err := repo.UpdateViolationStatus(context.Background(), vID, tr, func(tx *sql.Tx) error {
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected wrapped audit error, got %v", err)
	}

	// Status must remain active because the transaction was rolled back.
	var got string
	if err := d.DB.QueryRowContext(context.Background(),
		`SELECT status FROM violations WHERE id = ?`, vID).Scan(&got); err != nil {
		t.Fatalf("readback: %v", err)
	}
	if got != "active" {
		t.Errorf("expected active (rollback), got %q", got)
	}
}

func TestRepo_GetViolationForLifecycle_CrossTenantNotFound(t *testing.T) {
	d, tA, tB, uA := newTestDB(t)
	repoA := NewTenantRepo(d.DB, tA)
	versionID, screens := seedFlowAndScreens(t, repoA, uA)
	vID := seedViolation(t, repoA, versionID, screens[0], tA)

	// tenantB cannot see tenantA's violation.
	repoB := NewTenantRepo(d.DB, tB)
	_, err := repoB.GetViolationForLifecycle(context.Background(), vID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestRepo_UpdateViolationStatus_StaleFromReturnsNotFound(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	versionID, screens := seedFlowAndScreens(t, repo, uA)
	vID := seedViolation(t, repo, versionID, screens[0], tA)

	// Race: another transition won. Our `From=active` no longer matches.
	if _, err := d.DB.Exec(`UPDATE violations SET status = 'acknowledged' WHERE id = ?`, vID); err != nil {
		t.Fatalf("simulate race: %v", err)
	}

	tr := LifecycleTransition{From: "active", To: "acknowledged", Action: ActionAcknowledge, Reason: "x"}
	err := repo.UpdateViolationStatus(context.Background(), vID, tr, func(tx *sql.Tx) error { return nil })
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound on stale From, got %v", err)
	}
}
