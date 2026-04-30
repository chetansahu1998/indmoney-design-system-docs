package projects

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Phase 4 U7 — per-component reverse view tests.

func seedComponentViolation(t *testing.T, repo *TenantRepo, versionID, screenID, ruleID, observed, severity string) string {
	t.Helper()
	id := uuid.NewString()
	_, err := repo.DB().Exec(
		`INSERT INTO violations (id, version_id, screen_id, tenant_id, rule_id, severity, category, property, observed, status, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, 'component_governance', 'instance', ?, 'active', ?)`,
		id, versionID, screenID, repo.tenantID,
		ruleID, severity, observed,
		time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		t.Fatalf("seed component violation: %v", err)
	}
	return id
}

func TestComponentViolations_AggregateAndPerFlow(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	versionID, screens := seedFlowAndScreens(t, repo, uA)

	seedComponentViolation(t, repo, versionID, screens[0], "component_detached", "Toast at Tax/F&O/learn", "high")
	seedComponentViolation(t, repo, versionID, screens[0], "component_override_sprawl", "Toast/Default at Tax", "medium")
	seedComponentViolation(t, repo, versionID, screens[1], "component_detached", "Button at Plutus/Onboarding", "high")

	agg, flows, err := ComponentViolations(context.Background(), d.DB, tA, true, uA, "Toast")
	if err != nil {
		t.Fatalf("component violations: %v", err)
	}
	if agg.TotalViolations != 2 {
		t.Errorf("aggregate total expected 2, got %d", agg.TotalViolations)
	}
	if agg.BySeverity["high"] != 1 || agg.BySeverity["medium"] != 1 {
		t.Errorf("severity tally wrong: %+v", agg.BySeverity)
	}
	if agg.BySetDetached != 1 || agg.BySetOverride != 1 {
		t.Errorf("rule tally wrong: detached=%d override=%d", agg.BySetDetached, agg.BySetOverride)
	}
	if agg.FlowCount != 1 {
		t.Errorf("flow count expected 1, got %d", agg.FlowCount)
	}
	if len(flows) != 1 {
		t.Fatalf("expected 1 flow row, got %d", len(flows))
	}
	if flows[0].ViolationCount != 2 {
		t.Errorf("flow violation count expected 2, got %d", flows[0].ViolationCount)
	}
	if flows[0].HighestSeverity != "high" {
		t.Errorf("highest severity expected high, got %q", flows[0].HighestSeverity)
	}
}

func TestComponentViolations_CrossTenantAggregateOnly(t *testing.T) {
	d, tA, tB, uA := newTestDB(t)
	repoA := NewTenantRepo(d.DB, tA)
	repoB := NewTenantRepo(d.DB, tB)

	versionA, screensA := seedFlowAndScreens(t, repoA, uA)
	versionB, screensB := seedFlowAndScreens(t, repoB, uA)

	seedComponentViolation(t, repoA, versionA, screensA[0], "component_detached", "Toast at A/screen", "high")
	seedComponentViolation(t, repoB, versionB, screensB[0], "component_detached", "Toast at B/screen", "critical")

	// Caller in tenant A: aggregate sees both, flow detail only sees their own.
	agg, flows, err := ComponentViolations(context.Background(), d.DB, tA, true, uA, "Toast")
	if err != nil {
		t.Fatalf("component violations: %v", err)
	}
	if agg.TotalViolations != 2 {
		t.Errorf("cross-tenant aggregate expected 2, got %d", agg.TotalViolations)
	}
	if len(flows) != 1 {
		t.Fatalf("tenant A should see 1 flow, got %d", len(flows))
	}

	// Tenant B perspective: same aggregate, different flow detail.
	_, flowsB, err := ComponentViolations(context.Background(), d.DB, tB, true, uA, "Toast")
	if err != nil {
		t.Fatalf("component violations B: %v", err)
	}
	if len(flowsB) != 1 {
		t.Fatalf("tenant B should see 1 flow, got %d", len(flowsB))
	}
	if flowsB[0].FlowID == flows[0].FlowID {
		t.Errorf("tenant A and B got the same flow_id — cross-tenant leak: %s", flows[0].FlowID)
	}
}

func TestComponentViolations_MissingNameRejected(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	_, _, err := ComponentViolations(context.Background(), d.DB, tA, true, uA, "")
	if err == nil {
		t.Errorf("expected error for empty name")
	}
}
