package rules

import (
	"context"
	"strings"
	"testing"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/projects"
)

// fakeContrastLoader is the in-memory test fixture for ScreenModeLoader.
type fakeContrastLoader struct {
	rows []ScreenModeTree
}

func (f *fakeContrastLoader) LoadScreenModesForVersion(_ context.Context, _ string) ([]ScreenModeTree, error) {
	return f.rows, nil
}

// hexFill builds a SOLID fill payload from a hex color. Alpha defaults to 1
// (opaque) — pass alpha < 1 explicitly via solidFillAlpha when testing the
// transparent-bg fallback.
func hexFill(hex string) map[string]any {
	r, g, b, err := HexToRGB(hex)
	if err != nil {
		panic("hexFill: " + err.Error())
	}
	return map[string]any{
		"type": "SOLID",
		"color": map[string]any{
			"r": r,
			"g": g,
			"b": b,
			"a": 1.0,
		},
	}
}

// hexFillAlpha builds a SOLID fill at the given alpha.
func hexFillAlpha(hex string, alpha float64) map[string]any {
	f := hexFill(hex)
	f["color"].(map[string]any)["a"] = alpha
	return f
}

// gradientFill builds a GRADIENT_LINEAR fill from a list of hex stops.
func gradientFill(stops ...string) map[string]any {
	out := make([]any, 0, len(stops))
	for _, hex := range stops {
		r, g, b, _ := HexToRGB(hex)
		out = append(out, map[string]any{
			"color": map[string]any{
				"r": r,
				"g": g,
				"b": b,
				"a": 1.0,
			},
		})
	}
	return map[string]any{
		"type":          "GRADIENT_LINEAR",
		"gradientStops": out,
	}
}

// imageFill builds an IMAGE fill payload (no color → caller handles).
func imageFill() map[string]any {
	return map[string]any{"type": "IMAGE"}
}

// makeText builds a TEXT node with the given foreground hex + typography.
func makeText(fgHex string, fontSize float64, fontWeight int) map[string]any {
	return map[string]any{
		"type":  "TEXT",
		"name":  "label",
		"fills": []any{hexFill(fgHex)},
		"style": map[string]any{
			"fontSize":   fontSize,
			"fontWeight": fontWeight,
		},
	}
}

// makeFrame wraps children in a FRAME with the given fills (variadic so
// callers can pass nil for "no fill").
func makeFrame(fill map[string]any, children ...any) map[string]any {
	frame := map[string]any{
		"type":     "FRAME",
		"name":     "Frame",
		"children": children,
	}
	if fill != nil {
		frame["fills"] = []any{fill}
	}
	return frame
}

// runContrast runs the rule against a list of (modeLabel, root) pairs and
// returns the violations.
func runContrast(t *testing.T, rows []ScreenModeTree) []projects.Violation {
	t.Helper()
	loader := &fakeContrastLoader{rows: rows}
	r := NewA11yContrast(A11yContrastConfig{Loader: loader})
	v := &projects.ProjectVersion{ID: "v1", TenantID: "t1"}
	out, err := r.Run(context.Background(), v)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return out
}

// Happy path: white on black → 21:1 → no violation.
func TestA11yContrast_Happy_WhiteOnBlack(t *testing.T) {
	root := makeFrame(hexFill("#000000"),
		makeText("#FFFFFF", 16, 400),
	)
	out := runContrast(t, []ScreenModeTree{{ScreenID: "s1", ModeLabel: "light", CanonicalTree: root}})
	if len(out) != 0 {
		t.Fatalf("white-on-black must pass; got %+v", out)
	}
}

// Happy path: dark grey #595959 on white at 14pt regular → 7.0:1 → passes
// the 4.5 normal-text threshold.
func TestA11yContrast_Happy_DarkGreyOnWhite(t *testing.T) {
	root := makeFrame(hexFill("#FFFFFF"),
		makeText("#595959", 14, 400),
	)
	out := runContrast(t, []ScreenModeTree{{ScreenID: "s1", ModeLabel: "light", CanonicalTree: root}})
	if len(out) != 0 {
		t.Fatalf("#595959 on white must pass at 14pt regular; got %+v", out)
	}
}

// Edge: transparent / no-fill ancestor falls back to white.
//
// Layout: text inside a transparent frame, no opaque ancestor — bg should
// resolve to white. Foreground white → contrast 1.0 → violation.
func TestA11yContrast_TransparentBackground_FallsBackToWhite(t *testing.T) {
	transparentFrame := makeFrame(nil, makeText("#FFFFFF", 16, 400))
	out := runContrast(t, []ScreenModeTree{{ScreenID: "s1", ModeLabel: "light", CanonicalTree: transparentFrame}})
	if len(out) != 1 {
		t.Fatalf("white text on missing-fill (white-default) must violate; got %+v", out)
	}
	if !strings.Contains(out[0].Observed, "#FFFFFF") {
		t.Errorf("Observed should mention white bg; got %q", out[0].Observed)
	}
}

// Edge: gradient background uses the worst-case stop. Black text on a
// gradient that runs #000000 → #FFFFFF — worst-case stop for black text is
// black (ratio 1:1) → violation.
func TestA11yContrast_GradientBackground_WorstCaseStop(t *testing.T) {
	root := makeFrame(gradientFill("#000000", "#FFFFFF"),
		makeText("#000000", 16, 400),
	)
	out := runContrast(t, []ScreenModeTree{{ScreenID: "s1", ModeLabel: "light", CanonicalTree: root}})
	if len(out) != 1 {
		t.Fatalf("black-on-(black-white-gradient) must violate (worst stop = black); got %+v", out)
	}
	if !strings.Contains(out[0].Observed, "worst-case stop") {
		t.Errorf("Observed should annotate worst-case stop; got %q", out[0].Observed)
	}
	// And gradient that's solidly contrast-safe should pass: dark text on a
	// gradient that runs #FFFFFF → #F0F0F0 — worst stop is #F0F0F0,
	// contrast ~17:1 against black → passes.
	rootSafe := makeFrame(gradientFill("#FFFFFF", "#F0F0F0"),
		makeText("#000000", 16, 400),
	)
	outSafe := runContrast(t, []ScreenModeTree{{ScreenID: "s1", ModeLabel: "light", CanonicalTree: rootSafe}})
	if len(outSafe) != 0 {
		t.Fatalf("black on light gradient must pass; got %+v", outSafe)
	}
}

// Edge: image background → Info `a11y_unverifiable`, NOT High.
func TestA11yContrast_ImageBackground_Unverifiable(t *testing.T) {
	root := makeFrame(imageFill(), makeText("#FFFFFF", 16, 400))
	out := runContrast(t, []ScreenModeTree{{ScreenID: "s1", ModeLabel: "light", CanonicalTree: root}})
	if len(out) != 1 {
		t.Fatalf("image bg must emit one Info violation; got %+v", out)
	}
	v := out[0]
	if v.RuleID != A11yUnverifiableRuleID {
		t.Errorf("RuleID: want %q, got %q", A11yUnverifiableRuleID, v.RuleID)
	}
	if v.Severity != projects.SeverityInfo {
		t.Errorf("Severity: want info, got %q", v.Severity)
	}
	if !strings.Contains(v.Suggestion, "image") {
		t.Errorf("Suggestion should reference image; got %q", v.Suggestion)
	}
}

// Error path: low contrast normal text — #A0A0A0 on white, 16pt regular.
// Computed ratio ~2.6:1, well below 4.5 → High violation.
func TestA11yContrast_LowContrast_NormalText(t *testing.T) {
	root := makeFrame(hexFill("#FFFFFF"),
		makeText("#A0A0A0", 16, 400),
	)
	out := runContrast(t, []ScreenModeTree{{ScreenID: "s1", ModeLabel: "light", CanonicalTree: root}})
	if len(out) != 1 {
		t.Fatalf("#A0A0A0 on white at 16pt must violate; got %+v", out)
	}
	v := out[0]
	if v.RuleID != A11yContrastRuleID {
		t.Errorf("RuleID: want %q, got %q", A11yContrastRuleID, v.RuleID)
	}
	if v.Severity != projects.SeverityHigh {
		t.Errorf("Severity: want high, got %q", v.Severity)
	}
	if v.Category != A11yContrastCategory {
		t.Errorf("Category: want %q, got %q", A11yContrastCategory, v.Category)
	}
	if v.Property != "contrast" {
		t.Errorf("Property: want contrast, got %q", v.Property)
	}
	if !strings.Contains(v.Observed, "ratio") || !strings.Contains(v.Observed, "#A0A0A0") || !strings.Contains(v.Observed, "#FFFFFF") {
		t.Errorf("Observed should mention ratio + fg + bg; got %q", v.Observed)
	}
	if !strings.Contains(v.Suggestion, "4.5:1") {
		t.Errorf("Suggestion should reference 4.5:1 threshold; got %q", v.Suggestion)
	}
	if v.ModeLabel == nil || *v.ModeLabel != "light" {
		t.Errorf("ModeLabel: want light, got %v", v.ModeLabel)
	}
}

// Error path: low contrast large text — #A0A0A0 on white at 24pt. Ratio
// ~2.6:1, still below the 3.0 large-text threshold → High violation.
func TestA11yContrast_LowContrast_LargeText(t *testing.T) {
	root := makeFrame(hexFill("#FFFFFF"),
		makeText("#A0A0A0", 24, 400),
	)
	out := runContrast(t, []ScreenModeTree{{ScreenID: "s1", ModeLabel: "light", CanonicalTree: root}})
	if len(out) != 1 {
		t.Fatalf("#A0A0A0 on white at 24pt must violate; got %+v", out)
	}
	if !strings.Contains(out[0].Suggestion, "3:1") {
		t.Errorf("large-text suggestion should reference 3:1; got %q", out[0].Suggestion)
	}
}

// Threshold edge: #767676 on white at 16pt regular → 4.54:1, just above the
// normal threshold → no violation.
func TestA11yContrast_ThresholdEdge_NormalText_Passes(t *testing.T) {
	root := makeFrame(hexFill("#FFFFFF"),
		makeText("#767676", 16, 400),
	)
	out := runContrast(t, []ScreenModeTree{{ScreenID: "s1", ModeLabel: "light", CanonicalTree: root}})
	if len(out) != 0 {
		t.Fatalf("#767676 on white at 16pt regular should pass (4.54:1 ≥ 4.5); got %+v", out)
	}
}

// Threshold edge: large-text rule via fontWeight ≥ 700 + fontSize ≥ 14.
// #949494 on white = ~3.0:1 — passes the 3.0 large-text threshold for bold
// text, fails the 4.5 normal-text threshold otherwise.
func TestA11yContrast_LargeBoldThreshold(t *testing.T) {
	// Bold 14pt → large threshold (3.0). Use #949494 which sits roughly at
	// 3.04:1 — passes.
	rootBold := makeFrame(hexFill("#FFFFFF"),
		makeText("#949494", 14, 700),
	)
	outBold := runContrast(t, []ScreenModeTree{{ScreenID: "s1", ModeLabel: "light", CanonicalTree: rootBold}})
	if len(outBold) != 0 {
		t.Fatalf("#949494 on white at 14pt bold should pass under 3.0 threshold; got %+v", outBold)
	}
	// Same color, regular 14pt → normal threshold (4.5) — must fail.
	rootRegular := makeFrame(hexFill("#FFFFFF"),
		makeText("#949494", 14, 400),
	)
	outRegular := runContrast(t, []ScreenModeTree{{ScreenID: "s1", ModeLabel: "light", CanonicalTree: rootRegular}})
	if len(outRegular) != 1 {
		t.Fatalf("#949494 on white at 14pt regular should violate; got %+v", outRegular)
	}
}

// Per-mode: same logical screen passes light, fails dark. Light-mode tree
// has black text on white; dark-mode tree has black text on dark grey
// (ratio ~3.5:1 — fails 4.5 normal-text threshold).
func TestA11yContrast_PerMode_PassLightFailDark(t *testing.T) {
	light := makeFrame(hexFill("#FFFFFF"), makeText("#000000", 16, 400))
	dark := makeFrame(hexFill("#222222"), makeText("#444444", 16, 400))
	out := runContrast(t, []ScreenModeTree{
		{ScreenID: "s1", ModeLabel: "light", CanonicalTree: light},
		{ScreenID: "s1", ModeLabel: "dark", CanonicalTree: dark},
	})
	if len(out) != 1 {
		t.Fatalf("expected exactly one (dark-mode) violation; got %+v", out)
	}
	if out[0].ModeLabel == nil || *out[0].ModeLabel != "dark" {
		t.Errorf("violation should be tagged mode_label=dark; got %v", out[0].ModeLabel)
	}
	if out[0].ScreenID != "s1" {
		t.Errorf("ScreenID: want s1, got %q", out[0].ScreenID)
	}
}

// Edge: inner non-opaque frame falls through to outer opaque frame.
// Outer = white, inner = transparent overlay, text inside inner. Bg must
// resolve to white via the outer ancestor (not default-white-fallback —
// even with the same color the path matters for the gradient case).
func TestA11yContrast_TransparentInnerFallsToOuter(t *testing.T) {
	innerWithText := makeFrame(hexFillAlpha("#000000", 0), // transparent black overlay
		makeText("#A0A0A0", 16, 400),
	)
	root := makeFrame(hexFill("#FFFFFF"), innerWithText)
	out := runContrast(t, []ScreenModeTree{{ScreenID: "s1", ModeLabel: "light", CanonicalTree: root}})
	if len(out) != 1 {
		t.Fatalf("transparent inner + white outer: #A0A0A0 text must still violate; got %+v", out)
	}
	if !strings.Contains(out[0].Observed, "#FFFFFF") {
		t.Errorf("Observed should report white bg from outer ancestor; got %q", out[0].Observed)
	}
}

// Edge: TEXT node with no fills is skipped silently.
func TestA11yContrast_NoForeground_Skipped(t *testing.T) {
	textWithoutFill := map[string]any{
		"type":  "TEXT",
		"style": map[string]any{"fontSize": 16.0, "fontWeight": 400.0},
	}
	root := makeFrame(hexFill("#FFFFFF"), textWithoutFill)
	out := runContrast(t, []ScreenModeTree{{ScreenID: "s1", ModeLabel: "light", CanonicalTree: root}})
	if len(out) != 0 {
		t.Fatalf("missing fg fill must skip; got %+v", out)
	}
}

// Error path: nil version returns error.
func TestA11yContrast_NilVersion(t *testing.T) {
	r := NewA11yContrast(A11yContrastConfig{Loader: &fakeContrastLoader{}})
	if _, err := r.Run(context.Background(), nil); err == nil {
		t.Fatal("nil version: expected error, got nil")
	}
}

// Error path: nil loader returns error.
func TestA11yContrast_NilLoader(t *testing.T) {
	r := NewA11yContrast(A11yContrastConfig{Loader: nil})
	v := &projects.ProjectVersion{ID: "v1", TenantID: "t1"}
	if _, err := r.Run(context.Background(), v); err == nil {
		t.Fatal("nil loader: expected error, got nil")
	}
}
