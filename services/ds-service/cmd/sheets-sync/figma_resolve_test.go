package main

import (
	"testing"
)

// frame is a tiny constructor sugar so the walker fixtures stay readable.
func frame(id, kind, name string, w, h float64) figmaNode {
	return figmaNode{
		ID:                  id,
		Name:                name,
		Type:                kind,
		AbsoluteBoundingBox: &figmaBoundingBox{X: 0, Y: 0, Width: w, Height: h},
	}
}

func rect(id, name string, w, h float64, fills ...figmaFill) figmaNode {
	return figmaNode{
		ID:                  id,
		Name:                name,
		Type:                "RECTANGLE",
		AbsoluteBoundingBox: &figmaBoundingBox{Width: w, Height: h},
		Fills:               fills,
	}
}

func TestWalkScreens_FrameAcceptedAtScreenSize(t *testing.T) {
	root := figmaNode{Type: "SECTION", Children: []figmaNode{
		frame("a", "FRAME", "Home", 375, 812),
	}}
	got := walkScreens(root)
	if len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("expected one frame 'a', got %#v", got)
	}
}

func TestWalkScreens_HeightFloorAt80NotAt400(t *testing.T) {
	// Pre-fix: 375x146 popup frames were silently dropped (height < 400).
	// New floor is 80, so anything ≥ 80 tall passes.
	root := figmaNode{Type: "SECTION", Children: []figmaNode{
		frame("popup", "FRAME", "Inv. price card", 375, 146),
		frame("tooltip", "FRAME", "Tooltip", 375, 192),
		frame("too-short", "FRAME", "label-strip", 375, 60),
	}}
	got := walkScreens(root)
	ids := map[string]bool{}
	for _, s := range got {
		ids[s.ID] = true
	}
	if !ids["popup"] || !ids["tooltip"] {
		t.Fatalf("expected popup + tooltip to be accepted, got %#v", got)
	}
	if ids["too-short"] {
		t.Fatalf("expected too-short (h=60) to be rejected, got %#v", got)
	}
}

func TestWalkScreens_WidthFloorStillExcludesIconDebris(t *testing.T) {
	root := figmaNode{Type: "SECTION", Children: []figmaNode{
		frame("icon", "FRAME", "icon/24x24", 24, 24),
		frame("narrow", "FRAME", "200x600", 200, 600),
	}}
	got := walkScreens(root)
	if len(got) != 0 {
		t.Fatalf("expected no screens (both under 280px wide), got %#v", got)
	}
}

func TestWalkScreens_RectangleAcceptedOnlyWithImageFill(t *testing.T) {
	root := figmaNode{Type: "SECTION", Children: []figmaNode{
		rect("paste", "Screenshot_20250805", 375, 834, figmaFill{Type: "IMAGE"}),
		rect("solid", "background-shape", 375, 800, figmaFill{Type: "SOLID"}),
		rect("nofill", "no-fill-rect", 375, 800),
	}}
	got := walkScreens(root)
	if len(got) != 1 || got[0].ID != "paste" {
		t.Fatalf("expected only image-filled rectangle 'paste', got %#v", got)
	}
}

func TestWalkScreens_RecursesIntoSectionAndGroupOnly(t *testing.T) {
	// SECTION/GROUP recurse; FRAME does not — its children are sub-elements.
	root := figmaNode{Type: "SECTION", Children: []figmaNode{
		{
			Type: "GROUP",
			Children: []figmaNode{
				frame("inside-group", "FRAME", "Nested", 375, 812),
			},
		},
		// Frame containing its own sub-frame — only the OUTER counts.
		{
			Type:                "FRAME",
			ID:                  "outer",
			Name:                "Outer",
			AbsoluteBoundingBox: &figmaBoundingBox{Width: 375, Height: 812},
			Children: []figmaNode{
				frame("inner", "FRAME", "Inner button-ish wrapper", 375, 600),
			},
		},
	}}
	got := walkScreens(root)
	gotIDs := map[string]bool{}
	for _, s := range got {
		gotIDs[s.ID] = true
	}
	if !gotIDs["inside-group"] || !gotIDs["outer"] {
		t.Fatalf("expected inside-group and outer, got %#v", got)
	}
	if gotIDs["inner"] {
		t.Fatalf("did not expect frame children to be collected, got %#v", got)
	}
}

func TestWalkScreens_NoBboxIsSkipped(t *testing.T) {
	root := figmaNode{Type: "SECTION", Children: []figmaNode{
		{Type: "FRAME", ID: "no-bbox", Name: "missing"},
	}}
	if got := walkScreens(root); len(got) != 0 {
		t.Fatalf("expected no screens for nil bbox, got %#v", got)
	}
}
