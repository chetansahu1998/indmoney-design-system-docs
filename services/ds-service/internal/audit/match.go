package audit

import (
	"strings"
)

// MatchingService subset ported from ~/DesignBrain-AI/internal/import/figma/matching_service.go.
//
// We use 4 of DesignBrain's 8 signals (vector / lex / shared-style / autolayout
// / geom are dropped for v1; embeddings require a host we don't have):
//
//   componentKey   — 0.50  (the strongest signal — Figma's own component id)
//   nameLex        — 0.20  (token-stripped, lowercased substring match)
//   styleID        — 0.20  (shared paintStyleId / textStyleId)
//   color          — 0.10  (OKLCH within drift threshold of any DS color)
//
// Decisions:
//   accept      ≥ 0.50 (componentKey alone is enough)
//   ambiguous   ≥ 0.20 < 0.50
//   reject      < 0.20

// DefaultMatchingWeights mirrors DesignBrain's DefaultMatchingWeights but
// with the dropped signals' weights redistributed proportionally.
type MatchingWeights struct {
	ComponentKey float64
	NameLex      float64
	StyleID      float64
	Color        float64
}

// DefaultMatchingWeights returns the v1 default weighting.
func DefaultMatchingWeights() MatchingWeights {
	return MatchingWeights{
		ComponentKey: 0.50,
		NameLex:      0.20,
		StyleID:      0.20,
		Color:        0.10,
	}
}

// DSCandidate is a published-DS component the matcher can consider.
type DSCandidate struct {
	Slug         string
	Name         string
	ComponentKey string // Figma component id
	StyleIDs     []string
	Colors       []string // hex strings of fills the DS uses
}

// MatchInput is the test node we're scoring against the DS library.
type MatchInput struct {
	NodeID       string
	Name         string
	ComponentKey string
	StyleIDs     []string
	Colors       []string // observed fill hexes
}

// Match scores the input against every candidate; returns the highest scorer
// or DecisionReject if no candidate clears the threshold. Pure function —
// no I/O, no allocations beyond the result.
func Match(in MatchInput, candidates []DSCandidate, w MatchingWeights) ComponentMatch {
	best := ComponentMatch{
		NodeID:   in.NodeID,
		NodeName: in.Name,
		Decision: DecisionReject,
	}
	for _, c := range candidates {
		s, ev := scoreOne(in, c, w)
		if s > best.Score {
			best.Score = s
			best.Evidence = ev
			best.ComponentKey = c.ComponentKey
			best.MatchedSlug = c.Slug
		}
	}
	switch {
	case best.Score >= 0.50:
		best.Decision = DecisionAccept
	case best.Score >= 0.20:
		best.Decision = DecisionAmbiguous
	default:
		best.Decision = DecisionReject
		// Clear matched slug — we're rejecting.
		best.MatchedSlug = ""
		best.ComponentKey = ""
	}
	return best
}

func scoreOne(in MatchInput, c DSCandidate, w MatchingWeights) (float64, MatchEvidence) {
	ev := MatchEvidence{}
	score := 0.0

	if in.ComponentKey != "" && in.ComponentKey == c.ComponentKey {
		ev.ComponentKey = 1.0
		score += w.ComponentKey
	}

	if lexScore := nameSimilarity(in.Name, c.Name); lexScore > 0 {
		ev.NameLex = lexScore
		score += w.NameLex * lexScore
	}

	if styleScore := overlap(in.StyleIDs, c.StyleIDs); styleScore > 0 {
		ev.StyleID = styleScore
		score += w.StyleID * styleScore
	}

	if colorScore := bestColorMatch(in.Colors, c.Colors); colorScore > 0 {
		ev.Color = colorScore
		score += w.Color * colorScore
	}

	return score, ev
}

// nameSimilarity normalizes both names (lowercase, strip non-alphanumerics)
// and returns the longer-shared-substring proportion. Returns 0 if either is
// empty. 1.0 means full match; 0.0 means no shared sequence.
func nameSimilarity(a, b string) float64 {
	na := normalize(a)
	nb := normalize(b)
	if na == "" || nb == "" {
		return 0
	}
	if na == nb {
		return 1.0
	}
	// Substring containment scores 0.5–1.0.
	if strings.Contains(na, nb) || strings.Contains(nb, na) {
		shorter := len(nb)
		if len(na) < shorter {
			shorter = len(na)
		}
		longer := len(na)
		if len(nb) > longer {
			longer = len(nb)
		}
		return float64(shorter) / float64(longer)
	}
	return 0
}

func normalize(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// overlap returns the Jaccard-ish ratio: |A ∩ B| / max(|A|,|B|), in [0,1].
func overlap(a, b []string) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	set := make(map[string]bool, len(a))
	for _, s := range a {
		set[s] = true
	}
	hit := 0
	for _, s := range b {
		if set[s] {
			hit++
		}
	}
	denom := len(a)
	if len(b) > denom {
		denom = len(b)
	}
	return float64(hit) / float64(denom)
}

// bestColorMatch returns 1 - normalized OKLCH distance for the closest
// pair across the two color lists. Anything below the drift threshold scores 0.
func bestColorMatch(a, b []string) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	best := 1e9
	for _, ha := range a {
		for _, hb := range b {
			d := OKLCHDistance(ha, hb)
			if d < best {
				best = d
			}
		}
	}
	if best > DefaultColorDriftThreshold {
		return 0
	}
	// Normalize: distance 0 → 1.0, threshold → 0.
	return 1.0 - (best / DefaultColorDriftThreshold)
}
