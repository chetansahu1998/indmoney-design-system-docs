package rules

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/projects"
)

// fakeResolvedLoader is an in-memory ResolvedTreeLoader for the test suite.
// It returns one ResolvedScreen per (screenID, modeLabel) pair the test seeded.
type fakeResolvedLoader struct {
	rows []ResolvedScreen
	err  error
}

func (l *fakeResolvedLoader) LoadResolvedScreens(_ context.Context, _ string) ([]ResolvedScreen, error) {
	if l.err != nil {
		return nil, l.err
	}
	return l.rows, nil
}

// mustJSON marshals v to a string or fatals the test. Used so test fixtures
// can stay in literal map[string]any form and we serialize them once at the
// loader boundary, mirroring the production wire shape.
func mustJSON(t *testing.T, v map[string]any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	return string(b)
}

// versionFixture builds a ProjectVersion with the bare minimum the runner
// reads — ID and TenantID only.
func versionFixture() *projects.ProjectVersion {
	return &projects.ProjectVersion{ID: "v1", TenantID: "t1"}
}

// ─── Test 1: Happy path — matching modes ─────────────────────────────────────

func TestThemeParity_MatchingModes_ZeroViolations(t *testing.T) {
	tree := map[string]any{
		"type": "FRAME",
		"name": "Card",
		"children": []any{
			map[string]any{"type": "TEXT", "name": "Title"},
		},
	}
	loader := &fakeResolvedLoader{rows: []ResolvedScreen{
		{ScreenID: "s1", ModeLabel: "light", ResolvedTree: mustJSON(t, tree)},
		{ScreenID: "s1", ModeLabel: "dark", ResolvedTree: mustJSON(t, tree)},
	}}
	r := NewThemeParityRunner(ThemeParityConfig{Loader: loader})
	got, err := r.Run(context.Background(), versionFixture())
	if err != nil {
		t.Fatalf("Run returned err: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 violations, got %d: %+v", len(got), got)
	}
}

// ─── Test 2: Happy path — bound-only divergence ──────────────────────────────

func TestThemeParity_BoundOnlyDivergence_ZeroViolations(t *testing.T) {
	// Both modes carry boundVariables.fills pointing to the same Variable.
	// The resolved fills value differs across modes — this is legitimate
	// theme behavior. Because the runner ignores boundVariables AND the
	// keys it points to (fills here), no violation should surface.
	light := map[string]any{
		"type": "RECTANGLE",
		"boundVariables": map[string]any{
			"fills": "var.surface.bg",
		},
		"fills": map[string]any{"r": 1.0, "g": 1.0, "b": 1.0, "a": 1.0},
	}
	dark := map[string]any{
		"type": "RECTANGLE",
		"boundVariables": map[string]any{
			"fills": "var.surface.bg",
		},
		"fills": map[string]any{"r": 0.05, "g": 0.05, "b": 0.07, "a": 1.0},
	}
	loader := &fakeResolvedLoader{rows: []ResolvedScreen{
		{ScreenID: "s1", ModeLabel: "light", ResolvedTree: mustJSON(t, light)},
		{ScreenID: "s1", ModeLabel: "dark", ResolvedTree: mustJSON(t, dark)},
	}}
	r := NewThemeParityRunner(ThemeParityConfig{Loader: loader})
	got, err := r.Run(context.Background(), versionFixture())
	if err != nil {
		t.Fatalf("Run returned err: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 violations (bound divergence is legitimate), got %d: %+v", len(got), got)
	}
}

// ─── Test 3: Single mode — zero violations ───────────────────────────────────

func TestThemeParity_SingleMode_ZeroViolations(t *testing.T) {
	loader := &fakeResolvedLoader{rows: []ResolvedScreen{
		{ScreenID: "s1", ModeLabel: "default", ResolvedTree: mustJSON(t, map[string]any{"type": "FRAME"})},
	}}
	r := NewThemeParityRunner(ThemeParityConfig{Loader: loader})
	got, err := r.Run(context.Background(), versionFixture())
	if err != nil {
		t.Fatalf("Run returned err: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 violations for single-mode screen, got %d", len(got))
	}
}

// ─── Test 4: Three-mode pair-wise — N(N-1)/2 violations ──────────────────────

func TestThemeParity_ThreeMode_PairwiseViolations(t *testing.T) {
	// Each mode has a unique drift on the same property — paddingLeft.
	// Pair-wise diff: light×dark, light×sepia, dark×sepia → 3 violations.
	mk := func(pad float64) map[string]any {
		return map[string]any{"type": "FRAME", "paddingLeft": pad}
	}
	loader := &fakeResolvedLoader{rows: []ResolvedScreen{
		{ScreenID: "s1", ModeLabel: "light", ResolvedTree: mustJSON(t, mk(16))},
		{ScreenID: "s1", ModeLabel: "dark", ResolvedTree: mustJSON(t, mk(12))},
		{ScreenID: "s1", ModeLabel: "sepia", ResolvedTree: mustJSON(t, mk(20))},
	}}
	r := NewThemeParityRunner(ThemeParityConfig{Loader: loader})
	got, err := r.Run(context.Background(), versionFixture())
	if err != nil {
		t.Fatalf("Run returned err: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 violations (3 mode pairs), got %d: %+v", len(got), got)
	}
	for _, v := range got {
		if v.RuleID != "theme_parity_break" {
			t.Errorf("expected RuleID=theme_parity_break, got %q", v.RuleID)
		}
		if v.Category != "theme_parity" {
			t.Errorf("expected Category=theme_parity, got %q", v.Category)
		}
		if v.Severity != "critical" {
			t.Errorf("expected Severity=critical, got %q", v.Severity)
		}
		if v.Property != "paddingLeft" {
			t.Errorf("expected Property=paddingLeft, got %q", v.Property)
		}
	}
}

// ─── Test 5: Hand-painted dark — AE-2 critical violation ─────────────────────

func TestThemeParity_HandPaintedDark_CriticalViolation(t *testing.T) {
	// Light's fills is bound to a Variable; dark drops the binding and
	// hand-paints a raw color. This is a textbook theme-parity break.
	light := map[string]any{
		"type": "RECTANGLE",
		"boundVariables": map[string]any{
			"fills": "var.surface.bg",
		},
		"fills": map[string]any{"r": 1.0, "g": 1.0, "b": 1.0, "a": 1.0},
	}
	dark := map[string]any{
		"type": "RECTANGLE",
		// No boundVariables → fills is hand-painted in dark.
		"fills": map[string]any{"r": 0.42, "g": 0.45, "b": 0.5, "a": 1.0},
	}
	loader := &fakeResolvedLoader{rows: []ResolvedScreen{
		{ScreenID: "s1", ModeLabel: "light", ResolvedTree: mustJSON(t, light)},
		{ScreenID: "s1", ModeLabel: "dark", ResolvedTree: mustJSON(t, dark)},
	}}
	r := NewThemeParityRunner(ThemeParityConfig{Loader: loader})
	got, err := r.Run(context.Background(), versionFixture())
	if err != nil {
		t.Fatalf("Run returned err: %v", err)
	}
	if len(got) == 0 {
		t.Fatalf("expected at least 1 violation, got 0")
	}
	// At least one violation must reference fills + the hand-painted side.
	foundFill := false
	for _, v := range got {
		if v.RuleID != "theme_parity_break" {
			t.Errorf("expected RuleID=theme_parity_break, got %q", v.RuleID)
		}
		if v.Severity != "critical" {
			t.Errorf("expected Severity=critical, got %q", v.Severity)
		}
		if v.Category != "theme_parity" {
			t.Errorf("expected Category=theme_parity, got %q", v.Category)
		}
		if v.Property == "fills" || strings.HasPrefix(v.Property, "fills") || v.Property == "fill" {
			foundFill = true
		}
	}
	if !foundFill {
		t.Fatalf("expected a fills-related violation, got %+v", got)
	}
}

// ─── Test 6: Structural type mismatch — critical violation ───────────────────

func TestThemeParity_TypeMismatch_CriticalViolation(t *testing.T) {
	light := map[string]any{
		"type": "FRAME",
		"name": "Card",
		"children": []any{
			map[string]any{"type": "RECTANGLE", "name": "Bg"},
		},
	}
	dark := map[string]any{
		"type": "FRAME",
		"name": "Card",
		"children": []any{
			map[string]any{"type": "ELLIPSE", "name": "Bg"},
		},
	}
	loader := &fakeResolvedLoader{rows: []ResolvedScreen{
		{ScreenID: "s1", ModeLabel: "light", ResolvedTree: mustJSON(t, light)},
		{ScreenID: "s1", ModeLabel: "dark", ResolvedTree: mustJSON(t, dark)},
	}}
	r := NewThemeParityRunner(ThemeParityConfig{Loader: loader})
	got, err := r.Run(context.Background(), versionFixture())
	if err != nil {
		t.Fatalf("Run returned err: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 violation, got %d: %+v", len(got), got)
	}
	v := got[0]
	if v.Property != "type" {
		t.Errorf("expected Property=type, got %q", v.Property)
	}
	if v.Severity != "critical" {
		t.Errorf("expected Severity=critical, got %q", v.Severity)
	}
	if !strings.Contains(v.Suggestion, "node structure") {
		t.Errorf("expected suggestion to mention node structure, got %q", v.Suggestion)
	}
}

// ─── Test 7: Layout drift — critical violation ───────────────────────────────

func TestThemeParity_LayoutDrift_CriticalViolation(t *testing.T) {
	light := map[string]any{"type": "FRAME", "paddingLeft": 16.0}
	dark := map[string]any{"type": "FRAME", "paddingLeft": 12.0}
	loader := &fakeResolvedLoader{rows: []ResolvedScreen{
		{ScreenID: "s1", ModeLabel: "light", ResolvedTree: mustJSON(t, light)},
		{ScreenID: "s1", ModeLabel: "dark", ResolvedTree: mustJSON(t, dark)},
	}}
	r := NewThemeParityRunner(ThemeParityConfig{Loader: loader})
	got, err := r.Run(context.Background(), versionFixture())
	if err != nil {
		t.Fatalf("Run returned err: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 violation, got %d: %+v", len(got), got)
	}
	v := got[0]
	if v.Property != "paddingLeft" {
		t.Errorf("expected Property=paddingLeft, got %q", v.Property)
	}
	if v.Severity != "critical" {
		t.Errorf("expected Severity=critical, got %q", v.Severity)
	}
	if v.Category != "theme_parity" {
		t.Errorf("expected Category=theme_parity, got %q", v.Category)
	}
	if v.ModeLabel == nil || *v.ModeLabel == "" {
		t.Errorf("expected non-empty ModeLabel pointer, got %v", v.ModeLabel)
	}
	if v.ScreenID != "s1" {
		t.Errorf("expected ScreenID=s1, got %q", v.ScreenID)
	}
	if v.VersionID != "v1" || v.TenantID != "t1" {
		t.Errorf("expected version/tenant copied through, got %s/%s", v.VersionID, v.TenantID)
	}
}

// ─── Test 8: Multiple screens, only one with drift ───────────────────────────

func TestThemeParity_MultipleScreens_PerScreenIsolation(t *testing.T) {
	clean := map[string]any{"type": "FRAME"}
	driftLight := map[string]any{"type": "FRAME", "paddingLeft": 16.0}
	driftDark := map[string]any{"type": "FRAME", "paddingLeft": 12.0}
	loader := &fakeResolvedLoader{rows: []ResolvedScreen{
		{ScreenID: "s1", ModeLabel: "light", ResolvedTree: mustJSON(t, clean)},
		{ScreenID: "s1", ModeLabel: "dark", ResolvedTree: mustJSON(t, clean)},
		{ScreenID: "s2", ModeLabel: "light", ResolvedTree: mustJSON(t, driftLight)},
		{ScreenID: "s2", ModeLabel: "dark", ResolvedTree: mustJSON(t, driftDark)},
	}}
	r := NewThemeParityRunner(ThemeParityConfig{Loader: loader})
	got, err := r.Run(context.Background(), versionFixture())
	if err != nil {
		t.Fatalf("Run returned err: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 violation (only s2 drifts), got %d: %+v", len(got), got)
	}
	if got[0].ScreenID != "s2" {
		t.Fatalf("expected ScreenID=s2, got %q", got[0].ScreenID)
	}
}
