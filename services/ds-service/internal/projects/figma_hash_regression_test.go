package projects

import (
	"testing"
)

// figma_hash_regression_test.go — U3 of plan 002 (characterization-first).
// Locks in ComputeContentHash + ComputePositionHash output bytes for a
// representative subtree BEFORE U4's poller rewrite ships. Any accidental
// drift in hash inputs (e.g. someone "improves" HashableNode serialization,
// changes sort order, or adds/removes a hashed field) flips these
// constants and surfaces as a CI failure rather than as silent corruption
// of every figma_section.content_hash in production.
//
// Plan: docs/plans/2026-05-14-002-feat-figma-section-subtree-blob-plan.md
//
// To regenerate the "want" constants intentionally (after a deliberate
// hash-input change), run the test once, copy the actual values from the
// failure message, paste them back here, and document the change in the
// PR description.

// regressionFixture is a synthetic section subtree — one SECTION root, two
// FRAMEs underneath, each with INSTANCE + TEXT + VECTOR children. Mixed
// types, varied depths, varied bboxes, one componentId reference. Mirrors
// the shape of a real list-row hand-build.
func regressionFixture() (rootID string, nodes []HashableNode) {
	rootID = "10:1"
	nodes = []HashableNode{
		{NodeID: "10:1", ParentID: "0:1", NodeType: "SECTION", Name: "Wallet/Main", HasBBox: true, X: 0, Y: 0, Width: 1200, Height: 800, Depth: 2, OrderIndex: 0},
		{NodeID: "10:2", ParentID: "10:1", NodeType: "FRAME", Name: "Hero Row", HasBBox: true, X: 16, Y: 16, Width: 343, Height: 56, Depth: 3, OrderIndex: 0},
		{NodeID: "10:3", ParentID: "10:2", NodeType: "INSTANCE", Name: "Left Icon/Default", HasBBox: true, X: 16, Y: 16, Width: 24, Height: 24, Depth: 4, OrderIndex: 0},
		{NodeID: "10:4", ParentID: "10:2", NodeType: "TEXT", Name: "Reliance", HasBBox: true, X: 48, Y: 16, Width: 200, Height: 24, Depth: 4, OrderIndex: 1},
		{NodeID: "10:5", ParentID: "10:2", NodeType: "VECTOR", Name: "Chevron", HasBBox: true, X: 311, Y: 22, Width: 12, Height: 12, Depth: 4, OrderIndex: 2},
		{NodeID: "10:6", ParentID: "10:1", NodeType: "FRAME", Name: "Position Row", HasBBox: true, X: 16, Y: 80, Width: 343, Height: 64, Depth: 3, OrderIndex: 1},
		{NodeID: "10:7", ParentID: "10:6", NodeType: "INSTANCE", Name: "Left Icon/Default", HasBBox: true, X: 16, Y: 96, Width: 24, Height: 24, Depth: 4, OrderIndex: 0},
		{NodeID: "10:8", ParentID: "10:6", NodeType: "TEXT", Name: "Position Name", HasBBox: true, X: 48, Y: 88, Width: 180, Height: 20, Depth: 4, OrderIndex: 1},
		{NodeID: "10:9", ParentID: "10:6", NodeType: "TEXT", Name: "Position Value", HasBBox: true, X: 48, Y: 112, Width: 180, Height: 20, Depth: 4, OrderIndex: 2},
		{NodeID: "10:10", ParentID: "10:6", NodeType: "RECTANGLE", Name: "Divider", HasBBox: true, X: 48, Y: 140, Width: 280, Height: 1, Depth: 4, OrderIndex: 3},
	}
	return
}

// Snapshot bytes locked-in 2026-05-14 against the figma_hash.go
// implementation as it exists on commit d82cf65 (the U1+U2 commit of plan
// 002, post-rename to migrations 0030/0031). If U4's poller rewrite or
// any future change inadvertently alters hash inputs, these literals
// flip and the test fails. That's the canary.
//
// To intentionally update: run the test, copy actuals from the failure
// message, paste back here, document why in the PR.
const (
	regressionContentHashWant  = "abfeb9fb74a9f0a452472265d9019941cf2126514a39cb919704547bb2b5dbc5"
	regressionPositionHashWant = "01fd1a56e52d26144050f036d5609024f6ecc85e567cd64d8c36cc65db5af9e1"
)

func TestFigmaHash_Regression_ContentHash(t *testing.T) {
	root, nodes := regressionFixture()
	got := ComputeContentHash(root, nodes)
	if regressionContentHashWant == "__SET_FROM_FIRST_RUN__" {
		// Initial bootstrap mode: print the value so the implementer can
		// paste it back into the const. This branch is removed after the
		// first run of the test on the baseline build.
		t.Logf("regressionContentHashWant = %q  (paste into the const above)", got)
		t.Skip("bootstrap: regressionContentHashWant placeholder; see Logf")
	}
	if got != regressionContentHashWant {
		t.Errorf("ComputeContentHash drift:\n  got:  %q\n  want: %q\n  (if intentional, update the const and document why)", got, regressionContentHashWant)
	}
}

func TestFigmaHash_Regression_PositionHash(t *testing.T) {
	root, nodes := regressionFixture()
	got := ComputePositionHash(root, nodes)
	if regressionPositionHashWant == "__SET_FROM_FIRST_RUN__" {
		t.Logf("regressionPositionHashWant = %q  (paste into the const above)", got)
		t.Skip("bootstrap: regressionPositionHashWant placeholder; see Logf")
	}
	if got != regressionPositionHashWant {
		t.Errorf("ComputePositionHash drift:\n  got:  %q\n  want: %q\n  (if intentional, update the const and document why)", got, regressionPositionHashWant)
	}
}

func TestFigmaHash_Regression_ContentHashResponds_NodeAdded(t *testing.T) {
	// Sanity: adding a node to the subtree should change content_hash.
	// Guards against the failure mode "function silently returns a
	// constant" — if that ever happened, the snapshot test above wouldn't
	// catch it because both calls return the same constant.
	root, nodes := regressionFixture()
	base := ComputeContentHash(root, nodes)
	added := append(nodes, HashableNode{
		NodeID:     "10:99",
		ParentID:   "10:6",
		NodeType:   "TEXT",
		Name:       "Extra Annotation",
		HasBBox:    true,
		X:          48, Y: 160, Width: 200, Height: 16,
		Depth:      4,
		OrderIndex: 4,
	})
	withExtra := ComputeContentHash(root, added)
	if base == withExtra {
		t.Errorf("ComputeContentHash did not respond to a new descendant node: both = %q", base)
	}
}

func TestFigmaHash_Regression_ContentHashIgnores_RootBBox(t *testing.T) {
	// Sanity: changing only the root's own bbox should NOT change
	// content_hash (the U4 contract excludes the root's own bbox/name).
	// Position hash should flip.
	root, nodes := regressionFixture()
	baseContent := ComputeContentHash(root, nodes)
	basePos := ComputePositionHash(root, nodes)
	moved := make([]HashableNode, len(nodes))
	copy(moved, nodes)
	for i := range moved {
		if moved[i].NodeID == root {
			moved[i].X += 100
			moved[i].Y += 100
		}
	}
	movedContent := ComputeContentHash(root, moved)
	movedPos := ComputePositionHash(root, moved)
	if movedContent != baseContent {
		t.Errorf("ComputeContentHash flipped when only the root's bbox changed (it should not):\n  base:  %q\n  moved: %q", baseContent, movedContent)
	}
	if movedPos == basePos {
		t.Errorf("ComputePositionHash did NOT change when the root's bbox moved (it should): both = %q", basePos)
	}
}
