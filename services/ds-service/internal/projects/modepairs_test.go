package projects

import "testing"

func TestDetectModePairs_SimplePair(t *testing.T) {
	frames := []FrameInfo{
		{FrameID: "1", X: 0, Y: 0, VariableCollectionID: "VC", ModeID: "light"},
		{FrameID: "2", X: 0, Y: 1500, VariableCollectionID: "VC", ModeID: "dark"},
	}
	groups := DetectModePairs(frames)
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	if len(groups[0].Frames) != 2 {
		t.Fatalf("expected 2 frames in pair, got %d", len(groups[0].Frames))
	}
}

func TestDetectModePairs_AdjacencyThreshold(t *testing.T) {
	frames := []FrameInfo{
		// Δx = 5 (within 10) → paired
		{FrameID: "a", X: 100, Y: 0, VariableCollectionID: "VC", ModeID: "light"},
		{FrameID: "b", X: 105, Y: 1500, VariableCollectionID: "VC", ModeID: "dark"},
		// Δx = 200 from {a,b} → its own column
		{FrameID: "c", X: 305, Y: 0, VariableCollectionID: "VC", ModeID: "light"},
	}
	groups := DetectModePairs(frames)
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}
	// First group sorted by min(x): a+b at x=100; c at x=305.
	if len(groups[0].Frames) != 2 {
		t.Fatalf("expected first group to be pair, got %d frames", len(groups[0].Frames))
	}
	if len(groups[1].Frames) != 1 {
		t.Fatalf("expected second group to be singleton, got %d", len(groups[1].Frames))
	}
}

func TestDetectModePairs_DifferentCollectionsDontPair(t *testing.T) {
	frames := []FrameInfo{
		{FrameID: "a", X: 0, Y: 0, VariableCollectionID: "VC1", ModeID: "light"},
		{FrameID: "b", X: 0, Y: 1500, VariableCollectionID: "VC2", ModeID: "dark"},
	}
	groups := DetectModePairs(frames)
	if len(groups) != 2 {
		t.Fatalf("expected 2 singletons (different collections), got %d groups", len(groups))
	}
}

func TestDetectModePairs_FramesWithoutCollectionAreSingletons(t *testing.T) {
	frames := []FrameInfo{
		{FrameID: "a", X: 0, Y: 0},
		{FrameID: "b", X: 0, Y: 1500},
	}
	groups := DetectModePairs(frames)
	if len(groups) != 2 {
		t.Fatalf("expected 2 singletons (no collection), got %d", len(groups))
	}
	for _, g := range groups {
		if len(g.Frames) != 1 {
			t.Fatalf("expected singleton, got %d", len(g.Frames))
		}
	}
}

func TestDetectModePairs_TripleMode(t *testing.T) {
	frames := []FrameInfo{
		{FrameID: "1", X: 0, Y: 0, VariableCollectionID: "VC", ModeID: "light"},
		{FrameID: "2", X: 5, Y: 1500, VariableCollectionID: "VC", ModeID: "dark"},
		{FrameID: "3", X: 8, Y: 3000, VariableCollectionID: "VC", ModeID: "sepia"},
	}
	groups := DetectModePairs(frames)
	if len(groups) != 1 {
		t.Fatalf("expected 1 triple group, got %d", len(groups))
	}
	if len(groups[0].Frames) != 3 {
		t.Fatalf("expected 3 frames in triple, got %d", len(groups[0].Frames))
	}
}

func TestDetectModePairs_SameCollectionSameMode(t *testing.T) {
	frames := []FrameInfo{
		{FrameID: "1", X: 0, Y: 0, VariableCollectionID: "VC", ModeID: "light"},
		{FrameID: "2", X: 0, Y: 1500, VariableCollectionID: "VC", ModeID: "light"},
	}
	groups := DetectModePairs(frames)
	if len(groups) != 2 {
		t.Fatalf("expected 2 singletons (same mode never pair), got %d", len(groups))
	}
}

func TestDetectModePairs_StableOrder(t *testing.T) {
	// Same input twice should yield identical output ordering.
	frames := []FrameInfo{
		{FrameID: "z", X: 200, Y: 0, VariableCollectionID: "VC", ModeID: "light"},
		{FrameID: "y", X: 200, Y: 1500, VariableCollectionID: "VC", ModeID: "dark"},
		{FrameID: "x", X: 0, Y: 0, VariableCollectionID: "VC", ModeID: "light"},
		{FrameID: "w", X: 0, Y: 1500, VariableCollectionID: "VC", ModeID: "dark"},
	}
	g1 := DetectModePairs(frames)
	g2 := DetectModePairs(frames)
	if len(g1) != len(g2) {
		t.Fatalf("group count drift")
	}
	for i := range g1 {
		if len(g1[i].Frames) != len(g2[i].Frames) {
			t.Fatalf("frame count drift at group %d", i)
		}
		for j := range g1[i].Frames {
			if g1[i].Frames[j].FrameID != g2[i].Frames[j].FrameID {
				t.Fatalf("frame order drift")
			}
		}
	}
}

func TestDetectModePairs_EmptyInput(t *testing.T) {
	if got := DetectModePairs(nil); got != nil {
		t.Fatalf("expected nil for nil input, got %v", got)
	}
	if got := DetectModePairs([]FrameInfo{}); got != nil {
		t.Fatalf("expected nil for empty input, got %v", got)
	}
}
