package audit

import (
	"math"
	"sort"
)

// DSToken is one published-DS color/dimension entry the audit compares against.
type DSToken struct {
	Path       string  // "surface.surface-grey-separator-dark"
	Hex        string  // "#6F7686" (color tokens) — empty for dimension tokens
	Px         float64 // 16, 24 — color tokens leave 0
	VariableID string  // Figma variable id, when present in the token JSON
	// FigmaName is the original Glyph label (e.g. "Spl/ Brown", "Surface
	// Grey BG"). Plugin uses it for team-library lookup to bind the right
	// Figma Variable — the slugified Path is for docs, not for binding.
	FigmaName       string
	FigmaCollection string
	Deprecated bool
	ReplacedBy string // token path of replacement, when deprecation chain is set
	Kind       string // "color" | "spacing" | "radius" | "padding"
}

// FindClosestColor returns the closest DS color token within the drift
// threshold (or threshold * 1.5 to surface borderline candidates as ambiguous).
// Returns (nil, ∞) if no candidate is within the wider band.
//
// Bindable-only filter: base palette primitives (path prefix "base.colour.")
// are NOT published as Figma Variables and have no figma-name — recommending
// a designer bind to them would always fail in the plugin. The candidate
// pool is restricted to tokens that carry FigmaName, which in practice means
// semantic tokens only. Base tokens stay in the loaded set so distance math
// still observes them, but they're filtered before returning.
func FindClosestColor(observed string, tokens []DSToken, threshold float64) (*DSToken, float64) {
	if threshold <= 0 {
		threshold = DefaultColorDriftThreshold
	}
	wide := threshold * 1.5
	var bestTok *DSToken
	bestDist := math.Inf(1)
	for i := range tokens {
		t := &tokens[i]
		if t.Kind != "color" || t.Hex == "" {
			continue
		}
		// Skip tokens without a real Figma Variable identity. Base primitives
		// (4a4f52, ffffff, etc.) get no figma-name from the extractor and
		// can't be bound by the plugin.
		if t.FigmaName == "" {
			continue
		}
		d := OKLCHDistance(observed, t.Hex)
		if d < bestDist {
			bestDist = d
			bestTok = t
		}
	}
	if bestDist > wide {
		return nil, math.Inf(1)
	}
	return bestTok, bestDist
}

// FindClosestPx returns the closest dimension token within the px threshold.
func FindClosestPx(observed float64, tokens []DSToken, kind string, threshold float64) (*DSToken, float64) {
	if threshold <= 0 {
		threshold = DefaultPxDriftThreshold
	}
	var bestTok *DSToken
	bestDist := math.Inf(1)
	for i := range tokens {
		t := &tokens[i]
		if t.Kind != kind {
			continue
		}
		d := PxDistance(observed, t.Px)
		if d < bestDist {
			bestDist = d
			bestTok = t
		}
	}
	if bestDist > threshold {
		return nil, math.Inf(1)
	}
	return bestTok, bestDist
}

// PriorityForFix encapsulates the rule:
//
//	Deprecated source token       → P1
//	Drift × usage_count ≥ heat    → P1
//	Drift > threshold/2           → P2
//	otherwise                     → P3
//
// heat is the threshold above which "many people are wrong the same way"
// becomes a prioritized signal. Default = 5.
func PriorityForFix(reason string, distance float64, usageCount, heat int) Priority {
	if reason == "deprecated" {
		return PriorityP1
	}
	if usageCount >= heat {
		return PriorityP1
	}
	if distance < DefaultColorDriftThreshold/2 {
		return PriorityP2
	}
	return PriorityP3
}

// SortFixes orders fixes by priority then by distance × usage so the most
// impactful items surface first.
func SortFixes(fixes []FixCandidate) {
	rank := func(p Priority) int {
		switch p {
		case PriorityP1:
			return 0
		case PriorityP2:
			return 1
		}
		return 2
	}
	sort.SliceStable(fixes, func(i, j int) bool {
		ri, rj := rank(fixes[i].Priority), rank(fixes[j].Priority)
		if ri != rj {
			return ri < rj
		}
		hi := fixes[i].Distance * float64(fixes[i].UsageCount)
		hj := fixes[j].Distance * float64(fixes[j].UsageCount)
		return hi > hj
	})
}
