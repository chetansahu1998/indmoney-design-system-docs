package projects

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Phase 4 U9 — dashboard aggregation tests.

// Baseline: the welcome seed migration (0005) inserts two demo violations
// (1 critical theme_parity, 1 high a11y_contrast) under the system tenant
// so a fresh installer's dashboard isn't empty. Tests using the dashboard
// account for this floor.
const welcomeSeedViolations = 2

func TestBuildDashboardSummary_OnlyWelcomeSeed(t *testing.T) {
	d, _, _, _ := newTestDB(t)
	out, err := BuildDashboardSummary(context.Background(), d.DB, 8)
	if err != nil {
		t.Fatalf("dashboard: %v", err)
	}
	if out.TotalActive != welcomeSeedViolations {
		t.Errorf("expected %d total (welcome seed only), got %d", welcomeSeedViolations, out.TotalActive)
	}
	if len(out.ByProduct) == 0 {
		t.Errorf("welcome seed should populate by_product")
	}
	if out.WeeksWindow != 8 {
		t.Errorf("expected weeks=8, got %d", out.WeeksWindow)
	}
}

func TestBuildDashboardSummary_WithFixture(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	versionID, screens := seedFlowAndScreens(t, repo, uA)

	for i, sev := range []string{"critical", "high", "medium", "high"} {
		_, err := d.DB.Exec(
			`INSERT INTO violations (id, version_id, screen_id, tenant_id, rule_id, severity, category, property, status, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'active', ?)`,
			uuid.NewString(), versionID, screens[i%len(screens)], tA,
			"theme_parity.fill", sev, "theme_parity", "fill",
			time.Now().UTC().Format(time.RFC3339),
		)
		if err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	out, err := BuildDashboardSummary(context.Background(), d.DB, 8)
	if err != nil {
		t.Fatalf("dashboard: %v", err)
	}
	// Welcome seed adds welcomeSeedViolations on top of our 4.
	if out.TotalActive != 4+welcomeSeedViolations {
		t.Errorf("total expected %d, got %d", 4+welcomeSeedViolations, out.TotalActive)
	}
	if out.BySeverity["high"] < 2 {
		t.Errorf("high expected ≥2 (we seeded 2), got %d", out.BySeverity["high"])
	}
	if out.BySeverity["critical"] < 1 {
		t.Errorf("critical expected ≥1 (we seeded 1), got %d", out.BySeverity["critical"])
	}
	if len(out.ByProduct) < 1 {
		t.Errorf("expected at least 1 product, got %d", len(out.ByProduct))
	}
	// Top-violator is the rule with the most rows. Our test seeded 4 of
	// theme_parity.fill (largest single-rule total), so it must lead.
	if len(out.TopViolators) == 0 || out.TopViolators[0].RuleID != "theme_parity.fill" {
		t.Errorf("expected theme_parity.fill at top, got %+v", out.TopViolators)
	}
}

func TestBuildDashboardSummary_WeeksClampedToValid(t *testing.T) {
	d, _, _, _ := newTestDB(t)
	out, err := BuildDashboardSummary(context.Background(), d.DB, 999)
	if err != nil {
		t.Fatalf("dashboard: %v", err)
	}
	if out.WeeksWindow != 8 {
		t.Errorf("expected fallback to 8, got %d", out.WeeksWindow)
	}
}
