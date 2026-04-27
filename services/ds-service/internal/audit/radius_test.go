package audit

import "testing"

func TestClassifyRadius_OnGrid(t *testing.T) {
	for _, v := range AllowedRadiusValues {
		got := ClassifyRadius(v, 100) // tall node, but r is small so pill check shouldn't fire
		if v >= 50 {
			// Tall + r=height/2 case — handled by pill test below.
			continue
		}
		if got.Kind != RadiusOnGrid {
			t.Errorf("ClassifyRadius(%v, 100) Kind = %v; want RadiusOnGrid", v, got.Kind)
		}
		if got.Snapped != v {
			t.Errorf("ClassifyRadius(%v, 100) Snapped = %v; want %v", v, got.Snapped, v)
		}
	}
}

func TestClassifyRadius_Pill(t *testing.T) {
	cases := []struct {
		observed float64
		height   float64
	}{
		{20, 40},  // exact half
		{20, 41},  // slack within 1px (40.5 → 20 still pill)
		{19, 40},  // slack within 1px (20 - 19 = 1)
		{50, 100}, // larger pill
	}
	for _, c := range cases {
		got := ClassifyRadius(c.observed, c.height)
		if got.Kind != RadiusPill {
			t.Errorf("ClassifyRadius(%v, %v) Kind = %v; want RadiusPill", c.observed, c.height, got.Kind)
		}
		if got.Suggestion == "" {
			t.Errorf("pill case missing suggestion: %+v", got)
		}
	}
}

func TestClassifyRadius_NotPillWhenShortNode(t *testing.T) {
	// Very small node (height < 16) — even r=height/2 shouldn't be flagged
	// as a pill rule; treat as on-grid value if it lands.
	got := ClassifyRadius(8, 14)
	if got.Kind == RadiusPill {
		t.Errorf("short node shouldn't classify as pill: %+v", got)
	}
}

func TestClassifyRadius_OffGrid(t *testing.T) {
	cases := []struct {
		observed float64
		height   float64
		want     float64 // expected snap
	}{
		{7, 30, 8},   // halfway between 6 and 8 → round up
		{12.5, 40, 12}, // pill check: 40/2=20, observed 12.5 < 19 — not pill; closer to 12
		{19.5, 30, 16}, // not pill (height=30, r/h=0.65 OK pill?), wait: 30/2=15, r=19.5 > 14 (15-1) → pill
	}
	// Adjust: 19.5 with height 30 IS pill. Replace with non-pill setup.
	for _, c := range cases {
		_ = c
	}
	// Simpler scenarios with no host height (height=0 disables pill check):
	noHeight := []struct {
		observed float64
		want     float64
		ties     bool
	}{
		{1, 2, false},
		{3, 4, true},   // halfway 2,4 → 4
		{5, 6, true},   // halfway 4,6 → 6
		{7, 8, true},   // halfway 6,8 → 8
		{10, 12, true}, // halfway 8,12 → 12
		{13, 12, false},
		{15, 16, false},
		{19, 16, false}, // closer to 16 (3 vs +∞ — 16 is largest)
		{23, 16, false}, // out of range → max
	}
	for _, c := range noHeight {
		got := ClassifyRadius(c.observed, 0)
		if got.Kind != RadiusOffGrid {
			t.Errorf("ClassifyRadius(%v, 0) Kind = %v; want RadiusOffGrid", c.observed, got.Kind)
		}
		if got.Snapped != c.want {
			t.Errorf("ClassifyRadius(%v, 0) Snapped = %v; want %v", c.observed, got.Snapped, c.want)
		}
		if got.Suggestion == "" {
			t.Errorf("off-grid suggestion empty for %v", c.observed)
		}
	}
}

func TestClassifyRadius_PillTakesPrecedence(t *testing.T) {
	// A 23px radius on a 40-tall button is pill (40/2 = 20, ±1px slack
	// covers up to 21; 23 is outside). Adjust: 23 on height=46 → height/2=23,
	// pill matches.
	got := ClassifyRadius(23, 46)
	if got.Kind != RadiusPill {
		t.Errorf("pill case missed: r=23 h=46 Kind = %v", got.Kind)
	}
}
