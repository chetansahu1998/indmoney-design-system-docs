package audit

import "testing"

func TestSnapSpacing_OnGrid(t *testing.T) {
	for _, v := range AllowedGridSpacing {
		got := SnapSpacing(v)
		if !got.OnGrid {
			t.Errorf("SnapSpacing(%v).OnGrid = false; want true", v)
		}
		if got.Snapped != v {
			t.Errorf("SnapSpacing(%v).Snapped = %v; want %v", v, got.Snapped, v)
		}
		if got.Distance != 0 {
			t.Errorf("SnapSpacing(%v).Distance = %v; want 0", v, got.Distance)
		}
	}
}

func TestSnapSpacing_NearestSingle(t *testing.T) {
	cases := []struct {
		in   float64
		want float64
	}{
		{11, 12},
		{13, 12},
		{17, 16},  // 17 is 1 from 16, 3 from 20
		{19, 20},  // 19 is 3 from 16, 1 from 20
		{25, 24},  // 25 is 1 from 24, 3 from 28
		{27, 28},  // 27 is 3 from 24, 1 from 28
		{33, 32},  // 33 is 1 from 32, 3 from 36
		{14, 12},  // 14 → 12 (|2| vs |2| from 16 — tie, but 12-then-16; 14 is equidistant: tie → 16
		// keeping 14 as a tie case in tie test below; remove from here.
	}
	for _, c := range cases {
		if c.in == 14 {
			continue
		}
		got := SnapSpacing(c.in)
		if got.Snapped != c.want {
			t.Errorf("SnapSpacing(%v).Snapped = %v; want %v", c.in, got.Snapped, c.want)
		}
		if got.OnGrid {
			t.Errorf("SnapSpacing(%v).OnGrid = true; want false", c.in)
		}
		if len(got.Candidates) != 1 {
			t.Errorf("SnapSpacing(%v).Candidates = %v; want 1 candidate", c.in, got.Candidates)
		}
	}
}

func TestSnapSpacing_TiesRoundUp(t *testing.T) {
	// Designer's rule: 18 → 20 (not 16); 15 → 16 (not 12); 22 → 24 (not 20).
	// "Closer multiple wins; ties round up."
	cases := []struct {
		in       float64
		want     float64
		tieCount int
	}{
		{18, 20, 2}, // equidistant between 16 and 20
		{14, 16, 2}, // equidistant between 12 and 16
		{22, 24, 2}, // equidistant between 20 and 24
		{26, 28, 2}, // equidistant between 24 and 28
	}
	for _, c := range cases {
		got := SnapSpacing(c.in)
		if got.Snapped != c.want {
			t.Errorf("SnapSpacing(%v).Snapped = %v; want %v (tie should round UP)", c.in, got.Snapped, c.want)
		}
		if got.OnGrid {
			t.Errorf("SnapSpacing(%v).OnGrid = true; want false", c.in)
		}
		if len(got.Candidates) != c.tieCount {
			t.Errorf("SnapSpacing(%v).Candidates = %v; want %d entries (tie)", c.in, got.Candidates, c.tieCount)
		}
	}
}

func TestSnapSpacing_DesignerRules(t *testing.T) {
	// User's verbatim rule:
	//   18 → 16 or 20 (tie, round up to 20)
	//   15 → 16 (closer than 12)
	//   11 → 12
	rules := map[float64]float64{
		18: 20,
		15: 16,
		11: 12,
	}
	for in, want := range rules {
		got := SnapSpacing(in).Snapped
		if got != want {
			t.Errorf("designer rule SnapSpacing(%v) = %v; want %v", in, got, want)
		}
	}
}

func TestSnapSpacing_OutOfRange(t *testing.T) {
	got := SnapSpacing(200)
	if got.Snapped != 120 {
		t.Errorf("oversized value snapped to %v; want 120 (max grid)", got.Snapped)
	}
	if got.OnGrid {
		t.Error("oversized value reported as on-grid")
	}
}

func TestSnapSpacing_Zero(t *testing.T) {
	got := SnapSpacing(0)
	if got.Snapped != 0 {
		t.Errorf("zero observed snapped to %v; want 0", got.Snapped)
	}
}

func TestIsOnSpacingGrid(t *testing.T) {
	for _, v := range AllowedGridSpacing {
		if !IsOnSpacingGrid(v) {
			t.Errorf("IsOnSpacingGrid(%v) = false; want true", v)
		}
	}
	for _, v := range []float64{1, 3, 5, 7, 9, 10, 11, 13, 14, 15, 17, 18, 19, 21, 22, 23} {
		if IsOnSpacingGrid(v) {
			t.Errorf("IsOnSpacingGrid(%v) = true; want false", v)
		}
	}
}
