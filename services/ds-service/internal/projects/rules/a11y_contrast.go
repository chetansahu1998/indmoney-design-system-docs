package rules

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/projects"
)

// Public rule constants.
const (
	// A11yContrastRuleID — emitted on the violations row when text-on-bg
	// contrast falls below the WCAG 2.1 AA threshold.
	A11yContrastRuleID = "a11y_contrast_aa"

	// A11yUnverifiableRuleID — emitted when the background can't be
	// resolved (e.g. image-fill ancestor). Severity Info, not High.
	A11yUnverifiableRuleID = "a11y_unverifiable"

	// A11yContrastCategory — violations.category value for the contrast
	// rule. Drives the Violations tab filter chip.
	A11yContrastCategory = "a11y_contrast"
)

// WCAG 2.1 AA thresholds. Hardcoded constants per plan U4 — Phase 7 wires
// these through audit_rules config.
const (
	thresholdNormal     = 4.5
	thresholdLarge      = 3.0
	largeTextMinSize    = 18.0
	largeBoldMinSize    = 14.0
	largeBoldMinWeight  = 700
	defaultBackground   = "#FFFFFF" // when no opaque ancestor is found
)

// ScreenModeLoader supplies per-mode canonical-tree resolution to the
// contrast runner. One row per (screen × screen_mode) — light and dark mode
// each get their own resolved tree so contrast is checked under the values
// that actually ship.
//
// Defined inline (not shared with U2/U3) per the plan's "no shared utilities"
// rule for U4. Production wiring will pass an adapter that joins
// screen_canonical_trees × screen_modes and re-resolves variable bindings
// for each mode — see TODO(U4-prod-wire) below.
type ScreenModeLoader interface {
	// LoadScreenModesForVersion returns one row per (screen, mode) pair.
	// `CanonicalTree` is the mode-resolved canonical tree (variable
	// bindings already collapsed to concrete RGBA fills).
	LoadScreenModesForVersion(ctx context.Context, versionID string) ([]ScreenModeTree, error)
}

// ScreenModeTree is a single (screen, mode) resolution.
type ScreenModeTree struct {
	ScreenID      string
	ModeLabel     string
	CanonicalTree map[string]any
}

// A11yContrastConfig collects the runner's dependencies.
type A11yContrastConfig struct {
	Loader ScreenModeLoader
}

// A11yContrastRunner walks each (screen, mode) tree, resolves text→bg
// contrast for every TEXT node, and emits High violations for ratios below
// the WCAG 2.1 AA threshold.
type A11yContrastRunner struct {
	loader ScreenModeLoader
}

// NewA11yContrast constructs the runner.
func NewA11yContrast(cfg A11yContrastConfig) *A11yContrastRunner {
	return &A11yContrastRunner{loader: cfg.Loader}
}

// Run implements projects.RuleRunner.
//
// TODO(U4-prod-wire): the production loader must join
// screen_canonical_trees × screen_modes and re-resolve bound-variable fills
// per mode. For U4 we ship the runner against synthetic in-memory fixtures;
// the in-memory loader supplies pre-resolved trees so the runner stays pure.
func (r *A11yContrastRunner) Run(ctx context.Context, v *projects.ProjectVersion) ([]projects.Violation, error) {
	if v == nil {
		return nil, fmt.Errorf("a11y_contrast: nil version")
	}
	if r.loader == nil {
		return nil, fmt.Errorf("a11y_contrast: loader not configured")
	}
	rows, err := r.loader.LoadScreenModesForVersion(ctx, v.ID)
	if err != nil {
		return nil, fmt.Errorf("a11y_contrast: load screen-modes: %w", err)
	}
	out := make([]projects.Violation, 0)
	for _, row := range rows {
		modeLabel := row.ModeLabel
		// Walk while tracking the parent chain so we can resolve the
		// nearest opaque ancestor for each TEXT node.
		var visit func(node any, ancestors []map[string]any)
		visit = func(node any, ancestors []map[string]any) {
			m, ok := node.(map[string]any)
			if !ok {
				return
			}
			if t, _ := m["type"].(string); t == "TEXT" {
				if vio := evaluateTextContrast(m, ancestors, v, row.ScreenID, modeLabel); vio != nil {
					out = append(out, *vio)
				}
			}
			if children, ok := m["children"].([]any); ok {
				next := append(ancestors, m)
				for _, c := range children {
					visit(c, next)
				}
			}
		}
		visit(row.CanonicalTree, nil)
	}
	return out, nil
}

// evaluateTextContrast resolves fg/bg for a single TEXT node and returns a
// violation when the ratio falls below the applicable threshold. Returns nil
// when the text passes, when fg can't be resolved (skip silently), or when
// the bg is an image (returns the Info-grade `a11y_unverifiable` violation
// instead).
func evaluateTextContrast(textNode map[string]any, ancestors []map[string]any, v *projects.ProjectVersion, screenID, modeLabel string) *projects.Violation {
	fg, ok := resolveForegroundRGB(textNode)
	if !ok {
		// Degraded data — skip per plan U4.
		return nil
	}
	fontSize, fontWeight := readTypography(textNode)
	threshold := pickThreshold(fontSize, fontWeight)

	bgKind, bgFG, bgInfoStop := resolveBackground(ancestors, fg)

	switch bgKind {
	case backgroundImage:
		// Image background — emit Info `a11y_unverifiable`.
		return &projects.Violation{
			ID:         uuid.NewString(),
			VersionID:  v.ID,
			ScreenID:   screenID,
			TenantID:   v.TenantID,
			RuleID:     A11yUnverifiableRuleID,
			Severity:   projects.SeverityInfo,
			Category:   A11yContrastCategory,
			Property:   "contrast",
			Observed:   fmt.Sprintf("background contains an image (fg %s)", RGBToHex(fg.r, fg.g, fg.b)),
			Suggestion: "Background contains an image; verify contrast manually.",
			ModeLabel:  ptrIfNonEmpty(modeLabel),
			Status:     "active",
		}
	case backgroundSolid, backgroundGradient, backgroundDefault:
		// Continue to ratio compute below.
	default:
		return nil
	}

	lFG := RelativeLuminance(fg.r, fg.g, fg.b)
	lBG := RelativeLuminance(bgFG.r, bgFG.g, bgFG.b)
	ratio := ContrastRatio(lFG, lBG)

	if ratio >= threshold {
		return nil
	}

	// Below threshold → High violation.
	suggestion := "Foreground needs darker token to meet 4.5:1"
	if threshold == thresholdLarge {
		suggestion = "Foreground needs darker token to meet 3:1 (large text)"
	}
	observed := fmt.Sprintf("ratio %.2f:1 (fg %s on bg %s)", ratio, RGBToHex(fg.r, fg.g, fg.b), RGBToHex(bgFG.r, bgFG.g, bgFG.b))
	if bgKind == backgroundGradient && bgInfoStop != "" {
		observed += " [worst-case stop " + bgInfoStop + "]"
	}
	return &projects.Violation{
		ID:         uuid.NewString(),
		VersionID:  v.ID,
		ScreenID:   screenID,
		TenantID:   v.TenantID,
		RuleID:     A11yContrastRuleID,
		Severity:   projects.SeverityHigh,
		Category:   A11yContrastCategory,
		Property:   "contrast",
		Observed:   observed,
		Suggestion: suggestion,
		ModeLabel:  ptrIfNonEmpty(modeLabel),
		Status:     "active",
	}
}

// rgb is a tiny private color struct so the runner can pass colors around
// without leaking the (r,g,b) tuple shape into a public API.
type rgb struct {
	r, g, b float64
}

// pickThreshold applies the WCAG 2.1 AA large-text rule:
//
//	Large text = fontSize ≥ 18  OR  (fontSize ≥ 14 AND fontWeight ≥ 700)
//
// Returns 3.0 for large text, 4.5 otherwise.
func pickThreshold(fontSize float64, fontWeight int) float64 {
	if fontSize >= largeTextMinSize {
		return thresholdLarge
	}
	if fontSize >= largeBoldMinSize && fontWeight >= largeBoldMinWeight {
		return thresholdLarge
	}
	return thresholdNormal
}

// readTypography pulls fontSize + fontWeight off a TEXT node's `style` map,
// with sensible defaults so synthetic fixtures don't have to spell out every
// field. Figma REST returns these on `style.fontSize` / `style.fontWeight`;
// some plugin payloads put them at the node root, so we check both.
func readTypography(textNode map[string]any) (fontSize float64, fontWeight int) {
	fontSize = 16
	fontWeight = 400
	if style, ok := textNode["style"].(map[string]any); ok {
		if v, ok := readFloat(style["fontSize"]); ok {
			fontSize = v
		}
		if v, ok := readFloat(style["fontWeight"]); ok {
			fontWeight = int(v)
		}
	}
	if v, ok := readFloat(textNode["fontSize"]); ok {
		fontSize = v
	}
	if v, ok := readFloat(textNode["fontWeight"]); ok {
		fontWeight = int(v)
	}
	return fontSize, fontWeight
}

// resolveForegroundRGB extracts fills[0].color from a TEXT node. Returns
// ok=false when the fill is missing, transparent, or non-RGBA — caller skips
// those silently per plan U4.
func resolveForegroundRGB(textNode map[string]any) (rgb, bool) {
	fills, ok := textNode["fills"].([]any)
	if !ok || len(fills) == 0 {
		return rgb{}, false
	}
	first, ok := fills[0].(map[string]any)
	if !ok {
		return rgb{}, false
	}
	if t, _ := first["type"].(string); t != "" && t != "SOLID" {
		// TEXT-with-gradient/image foreground — punt for U4.
		return rgb{}, false
	}
	color, ok := first["color"].(map[string]any)
	if !ok {
		return rgb{}, false
	}
	r, rOk := readFloat(color["r"])
	g, gOk := readFloat(color["g"])
	b, bOk := readFloat(color["b"])
	if !rOk || !gOk || !bOk {
		return rgb{}, false
	}
	if a, ok := readFloat(color["a"]); ok && a == 0 {
		return rgb{}, false
	}
	return rgb{r: r, g: g, b: b}, true
}

// backgroundKind is a tagged union for resolveBackground's return.
type backgroundKind int

const (
	backgroundUnknown backgroundKind = iota
	backgroundSolid
	backgroundGradient
	backgroundImage
	backgroundDefault // fell back to white
)

// resolveBackground walks the parent chain (innermost-first) looking for the
// first ancestor with a usable opaque fill.
//
//   - SOLID with alpha == 1 → returns (solid, color, "").
//   - GRADIENT_LINEAR / RADIAL / etc. → picks the worst-case stop, returns
//     (gradient, stopColor, "#RRGGBB"-of-stop).
//   - IMAGE → returns (image, fg, "") — caller emits Info violation.
//   - No opaque ancestor → returns (default, white, "").
//
// The `fg` argument is only consulted for the gradient case (worst-case
// stop selection compares each stop's luminance against the foreground's).
func resolveBackground(ancestors []map[string]any, fg rgb) (backgroundKind, rgb, string) {
	// Walk innermost-first so we land on the *closest* opaque container.
	for i := len(ancestors) - 1; i >= 0; i-- {
		node := ancestors[i]
		fills, ok := node["fills"].([]any)
		if !ok || len(fills) == 0 {
			continue
		}
		first, ok := fills[0].(map[string]any)
		if !ok {
			continue
		}
		fillType, _ := first["type"].(string)
		switch fillType {
		case "SOLID", "":
			color, ok := first["color"].(map[string]any)
			if !ok {
				continue
			}
			a, _ := readFloat(color["a"])
			// Treat missing alpha as opaque (matches Figma when the fill
			// payload omits it explicitly).
			if _, hasA := color["a"]; !hasA {
				a = 1
			}
			if a < 1 {
				// Not opaque — keep walking.
				continue
			}
			r, rOk := readFloat(color["r"])
			g, gOk := readFloat(color["g"])
			b, bOk := readFloat(color["b"])
			if !rOk || !gOk || !bOk {
				continue
			}
			return backgroundSolid, rgb{r: r, g: g, b: b}, ""
		case "GRADIENT_LINEAR", "GRADIENT_RADIAL", "GRADIENT_ANGULAR", "GRADIENT_DIAMOND":
			stops, ok := first["gradientStops"].([]any)
			if !ok || len(stops) == 0 {
				continue
			}
			worst, hex, ok := pickWorstGradientStop(stops, fg)
			if !ok {
				continue
			}
			return backgroundGradient, worst, hex
		case "IMAGE":
			return backgroundImage, rgb{}, ""
		}
	}
	// No opaque ancestor — default to white per plan U4.
	r, g, b, _ := HexToRGB(defaultBackground)
	return backgroundDefault, rgb{r: r, g: g, b: b}, ""
}

// pickWorstGradientStop selects the stop that minimizes contrast against the
// foreground (i.e., the stop whose luminance is closest to the foreground's
// luminance). Returns the stop's RGB, formatted hex, and ok=false when no
// stop carries a usable color.
func pickWorstGradientStop(stops []any, fg rgb) (rgb, string, bool) {
	lFG := RelativeLuminance(fg.r, fg.g, fg.b)
	var (
		best     rgb
		bestRatio float64
		found     bool
	)
	for _, s := range stops {
		stop, ok := s.(map[string]any)
		if !ok {
			continue
		}
		color, ok := stop["color"].(map[string]any)
		if !ok {
			continue
		}
		r, rOk := readFloat(color["r"])
		g, gOk := readFloat(color["g"])
		b, bOk := readFloat(color["b"])
		if !rOk || !gOk || !bOk {
			continue
		}
		stopRGB := rgb{r: r, g: g, b: b}
		ratio := ContrastRatio(lFG, RelativeLuminance(r, g, b))
		if !found || ratio < bestRatio {
			best, bestRatio, found = stopRGB, ratio, true
		}
	}
	if !found {
		return rgb{}, "", false
	}
	return best, RGBToHex(best.r, best.g, best.b), true
}

// ptrIfNonEmpty returns a pointer to s when non-empty, nil otherwise. Used
// for ModeLabel which is *string on the Violation row (nullable column).
func ptrIfNonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
