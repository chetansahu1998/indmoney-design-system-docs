package audit

import (
	"math"
	"testing"
)

func TestLooksLikeScreen(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"Trade Screen", true},
		{"Watchlist Page", true},
		{"Login Modal", true},
		{"Confirm Dialog", true},
		{"Bottom Sheet", true},
		{"WIP — sketches", false},
		{"Component playground", false},
		{"", false},
		{"a", false},
		{"TRADE SCREEN", true}, // case-insensitive
	}
	for _, c := range cases {
		if got := LooksLikeScreen(c.name); got != c.want {
			t.Errorf("LooksLikeScreen(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestClassifyKind(t *testing.T) {
	cases := []struct {
		fType, fName string
		want         Kind
	}{
		{"FRAME", "Trade Screen", KindScreen},
		{"FRAME", "card-inner", KindFrame},
		{"INSTANCE", "Button/primary", KindComponent},
		{"COMPONENT_SET", "Button", KindComponent},
		{"TEXT", "Buy now", KindText},
		{"VECTOR", "icon-arrow", KindIcon},
		{"BOOL_OPERATION", "minus", KindIcon},
		{"RECTANGLE", "bg", KindShape},
		{"GROUP", "g14", KindContainer},
		{"SECTION", "Buttons", KindSection},
		{"WHATEVER", "x", KindOther},
	}
	for _, c := range cases {
		if got := ClassifyKind(c.fType, c.fName); got != c.want {
			t.Errorf("ClassifyKind(%q,%q) = %v, want %v", c.fType, c.fName, got, c.want)
		}
	}
}

func TestHexToRGBRoundTrip(t *testing.T) {
	cases := []string{"#000000", "#FFFFFF", "#6F7686", "#017AFE", "#FE6060"}
	for _, in := range cases {
		r, g, b, ok := HexToRGB(in)
		if !ok {
			t.Errorf("HexToRGB(%q) ok=false", in)
			continue
		}
		out := RGBToHex(r, g, b)
		if out != in {
			t.Errorf("HexToRGB(%q) → RGBToHex = %q", in, out)
		}
	}
}

func TestHexToRGBInvalid(t *testing.T) {
	cases := []string{"", "#", "#FFF", "not-a-color", "#ZZZZZZ"}
	for _, in := range cases {
		if _, _, _, ok := HexToRGB(in); ok {
			t.Errorf("HexToRGB(%q) ok=true want false", in)
		}
	}
}

func TestOKLCHDistance(t *testing.T) {
	// identical → 0
	if d := OKLCHDistance("#6F7686", "#6F7686"); d != 0 {
		t.Errorf("identical distance = %v, want 0", d)
	}
	// near-neighbor (#6F7686 vs #6B7280) ~0.011 by Lab math
	d := OKLCHDistance("#6F7686", "#6B7280")
	if d < 0.005 || d > 0.025 {
		t.Errorf("near-neighbor distance = %v, want in [0.005, 0.025]", d)
	}
	// invalid → ∞
	if d := OKLCHDistance("not-a-color", "#6F7686"); !math.IsInf(d, 1) {
		t.Errorf("invalid distance = %v, want +Inf", d)
	}
	// far apart (white vs black) > 0.5
	if d := OKLCHDistance("#000000", "#FFFFFF"); d < 0.5 {
		t.Errorf("black/white distance = %v, want > 0.5", d)
	}
}

func TestCanonicalHashStripsVolatile(t *testing.T) {
	// Two nodes that differ only in id + bbox position should hash the same.
	nodeA := map[string]any{
		"id":   "1:111",
		"type": "RECTANGLE",
		"absoluteBoundingBox": map[string]any{
			"x": 10.0, "y": 20.0, "width": 100.0, "height": 50.0,
		},
		"styles": map[string]any{"fill": "S:abc"},
	}
	nodeB := map[string]any{
		"id":   "9:999",
		"type": "RECTANGLE",
		"absoluteBoundingBox": map[string]any{
			"x": 999.0, "y": 999.0, "width": 100.0, "height": 50.0,
		},
		"styles": map[string]any{"fill": "S:abc"},
	}
	if CanonicalHash(nodeA) != CanonicalHash(nodeB) {
		t.Errorf("identical-shape nodes hashed differently:\n  A=%s\n  B=%s",
			CanonicalHash(nodeA), CanonicalHash(nodeB))
	}
}

func TestCanonicalHashDistinguishesShape(t *testing.T) {
	nodeA := map[string]any{
		"type":                "RECTANGLE",
		"absoluteBoundingBox": map[string]any{"width": 100.0, "height": 50.0},
		"styles":              map[string]any{"fill": "S:abc"},
	}
	nodeB := map[string]any{
		"type":                "RECTANGLE",
		"absoluteBoundingBox": map[string]any{"width": 100.0, "height": 50.0},
		"styles":              map[string]any{"fill": "S:DIFFERENT"},
	}
	if CanonicalHash(nodeA) == CanonicalHash(nodeB) {
		t.Errorf("nodes with different fillStyle hashed the same: %s", CanonicalHash(nodeA))
	}
}

func TestCanonicalHashEmpty(t *testing.T) {
	if got := CanonicalHash(nil); got != "" {
		t.Errorf("CanonicalHash(nil) = %q, want empty", got)
	}
}

func TestMatchAcceptOnComponentKey(t *testing.T) {
	cands := []DSCandidate{
		{Slug: "card-elevated", Name: "Card / elevated", ComponentKey: "1:42"},
	}
	in := MatchInput{NodeID: "n1", Name: "Card", ComponentKey: "1:42"}
	got := Match(in, cands, DefaultMatchingWeights())
	if got.Decision != DecisionAccept {
		t.Errorf("componentKey hit decision = %v, want accept", got.Decision)
	}
	if got.MatchedSlug != "card-elevated" {
		t.Errorf("matched slug = %q, want card-elevated", got.MatchedSlug)
	}
}

func TestMatchAmbiguousOnNameOnly(t *testing.T) {
	cands := []DSCandidate{
		{Slug: "card-elevated", Name: "Card / elevated", ComponentKey: "1:42"},
	}
	in := MatchInput{NodeID: "n1", Name: "Card / elevated"}
	got := Match(in, cands, DefaultMatchingWeights())
	if got.Decision != DecisionAmbiguous {
		t.Errorf("name-only decision = %v, want ambiguous", got.Decision)
	}
}

func TestMatchRejectsWhenNothingFires(t *testing.T) {
	cands := []DSCandidate{
		{Slug: "card-elevated", Name: "Card / elevated", ComponentKey: "1:42"},
	}
	in := MatchInput{NodeID: "n1", Name: "Random rectangle"}
	got := Match(in, cands, DefaultMatchingWeights())
	if got.Decision != DecisionReject {
		t.Errorf("decision = %v, want reject", got.Decision)
	}
	if got.MatchedSlug != "" {
		t.Errorf("rejected match has slug %q; expected empty", got.MatchedSlug)
	}
}

func TestFindClosestColor(t *testing.T) {
	tokens := []DSToken{
		// FigmaName populated — only tokens with a real published Variable
		// are bindable. Empty FigmaName tokens (base primitives) are skipped.
		{Path: "text-1", Hex: "#0E1117", Kind: "color", FigmaName: "Text-1"},
		{Path: "surface-grey-separator-dark", Hex: "#6F7686", Kind: "color", FigmaName: "Surface Grey Separator Dark"},
		{Path: "danger", Hex: "#FE6060", Kind: "color", FigmaName: "Danger"},
	}
	tok, d := FindClosestColor("#6B7280", tokens, 0.03)
	if tok == nil {
		t.Fatalf("expected a match within threshold, got nil")
	}
	if tok.Path != "surface-grey-separator-dark" {
		t.Errorf("closest = %q, want surface-grey-separator-dark", tok.Path)
	}
	if d > 0.03 {
		t.Errorf("distance %v should be ≤ threshold 0.03", d)
	}

	// Far-apart color: nothing within threshold.
	if tok2, _ := FindClosestColor("#FF00FF", tokens, 0.01); tok2 != nil {
		t.Errorf("far color matched %q; expected nil", tok2.Path)
	}
}

func TestPriorityForFix(t *testing.T) {
	if got := PriorityForFix("deprecated", 0.001, 0, 5); got != PriorityP1 {
		t.Errorf("deprecated → %v, want P1", got)
	}
	if got := PriorityForFix("drift", 0.005, 7, 5); got != PriorityP1 {
		t.Errorf("high-usage drift → %v, want P1", got)
	}
	if got := PriorityForFix("drift", 0.01, 1, 5); got != PriorityP2 {
		t.Errorf("close drift → %v, want P2", got)
	}
	if got := PriorityForFix("drift", 0.025, 1, 5); got != PriorityP3 {
		t.Errorf("borderline drift → %v, want P3", got)
	}
}
