// Grid-snap rules for spacing / padding tokens. INDmoney design uses a 4-pt
// grid with 2 and 6 as escape hatches for component-internal fine spacing.
// Anything else is drift, surfaced via audit fixes — never minted as a token.
//
// Rules:
//   - Allowed values: {2, 4, 6, 8, 12, 16, 20, 24, 28, 32, 36, 40, 48, 56,
//     64, 72, 80, 96, 120}
//   - Off-grid observations snap to the nearest allowed value
//   - Ties round UP (15 → 16, not 12; 22 → 24, not 20)
//   - Distance from the snapped value drives priority (closer = lower-pri
//     since the designer was almost on grid)
//
// Radius rules live in radius.go because they're property-derived, not
// just a snap.
package audit

import "math"

// AllowedGridSpacing is the canonical 4-pt INDmoney grid plus the 2/6 escape
// hatches. Sorted ascending so binary search on the snap path is trivial.
var AllowedGridSpacing = []float64{2, 4, 6, 8, 12, 16, 20, 24, 28, 32, 36, 40, 48, 56, 64, 72, 80, 96, 120}

// SnapResult is the outcome of one grid-snap evaluation.
type SnapResult struct {
	Observed   float64
	Snapped    float64
	OnGrid     bool      // true when Observed ∈ AllowedGridSpacing exactly
	Distance   float64   // |Observed - Snapped|, in px
	Candidates []float64 // typically [Snapped]; for ties (e.g. 18 → both 16 and 20), holds both
}

// SnapSpacing snaps an observed px value to the nearest entry in
// AllowedGridSpacing. Returns OnGrid=true when the input is already exactly
// on the grid. For tie cases (18 sits exactly between 16 and 20), both
// candidates are returned and Snapped is set to the larger — designers
// should round UP on ties because tighter UI is rarely what the spec called
// for; small pad-bumps almost always reflect intent that didn't survive a
// hand-tweak.
func SnapSpacing(observed float64) SnapResult {
	if observed <= 0 {
		return SnapResult{Observed: observed, Snapped: 0, OnGrid: false, Distance: 0}
	}
	for _, v := range AllowedGridSpacing {
		if math.Abs(v-observed) < 0.001 {
			return SnapResult{Observed: observed, Snapped: v, OnGrid: true, Distance: 0, Candidates: []float64{v}}
		}
	}
	// Find nearest two entries for tie detection.
	bestDist := math.Inf(1)
	var best float64
	for _, v := range AllowedGridSpacing {
		d := math.Abs(v - observed)
		if d < bestDist {
			bestDist = d
			best = v
		}
	}
	// Out-of-range above the largest allowed: snap to the max and report
	// as drift; never invent a new tier silently.
	if observed > AllowedGridSpacing[len(AllowedGridSpacing)-1] {
		max := AllowedGridSpacing[len(AllowedGridSpacing)-1]
		return SnapResult{
			Observed:   observed,
			Snapped:    max,
			OnGrid:     false,
			Distance:   observed - max,
			Candidates: []float64{max},
		}
	}
	// Tie: equidistant from two grid entries. Prefer the larger.
	candidates := []float64{best}
	for _, v := range AllowedGridSpacing {
		if v == best {
			continue
		}
		if math.Abs(math.Abs(v-observed)-bestDist) < 0.001 {
			candidates = append(candidates, v)
		}
	}
	if len(candidates) > 1 {
		// Pick the larger — round-up semantics.
		largest := candidates[0]
		for _, v := range candidates {
			if v > largest {
				largest = v
			}
		}
		return SnapResult{
			Observed:   observed,
			Snapped:    largest,
			OnGrid:     false,
			Distance:   bestDist,
			Candidates: candidates,
		}
	}
	return SnapResult{
		Observed:   observed,
		Snapped:    best,
		OnGrid:     false,
		Distance:   bestDist,
		Candidates: candidates,
	}
}

// IsOnSpacingGrid reports whether observed exists in AllowedGridSpacing
// exactly (within float epsilon). Cheap precheck before SnapSpacing.
func IsOnSpacingGrid(observed float64) bool {
	for _, v := range AllowedGridSpacing {
		if math.Abs(v-observed) < 0.001 {
			return true
		}
	}
	return false
}
