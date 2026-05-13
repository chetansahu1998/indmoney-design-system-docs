package projects

import "testing"

// figma_hash_test.go — U4 unit coverage.

func TestComputeContentHash_StableAcrossOrderJitter(t *testing.T) {
	// Two siblings under the same parent; reordering them shouldn't
	// flip the content hash (we sort by depth then node_id, not by
	// order_index).
	a := []HashableNode{
		{NodeID: "parent", ParentID: "", NodeType: "SECTION", Name: "p", Depth: 0},
		{NodeID: "child-a", ParentID: "parent", NodeType: "FRAME", Name: "A", Depth: 1, OrderIndex: 0, HasBBox: true, X: 0, Y: 0, Width: 100, Height: 100},
		{NodeID: "child-b", ParentID: "parent", NodeType: "FRAME", Name: "B", Depth: 1, OrderIndex: 1, HasBBox: true, X: 100, Y: 0, Width: 100, Height: 100},
	}
	b := []HashableNode{
		{NodeID: "parent", ParentID: "", NodeType: "SECTION", Name: "p", Depth: 0},
		// Same children, but with order_index swapped.
		{NodeID: "child-b", ParentID: "parent", NodeType: "FRAME", Name: "B", Depth: 1, OrderIndex: 0, HasBBox: true, X: 100, Y: 0, Width: 100, Height: 100},
		{NodeID: "child-a", ParentID: "parent", NodeType: "FRAME", Name: "A", Depth: 1, OrderIndex: 1, HasBBox: true, X: 0, Y: 0, Width: 100, Height: 100},
	}
	ha := ComputeContentHash("parent", a)
	hb := ComputeContentHash("parent", b)
	if ha != hb {
		t.Fatalf("hash differed across order_index jitter: a=%s b=%s", ha, hb)
	}
}

func TestComputeContentHash_RootBBoxDoesNotInfluence(t *testing.T) {
	// Moving a section (changing root x/y/w/h) without touching children
	// should NOT change content_hash. Only the position_hash should flip.
	atRest := []HashableNode{
		{NodeID: "section", ParentID: "", NodeType: "SECTION", Name: "s", Depth: 0, HasBBox: true, X: 0, Y: 0, Width: 100, Height: 100},
		{NodeID: "frame", ParentID: "section", NodeType: "FRAME", Name: "f", Depth: 1, OrderIndex: 0, HasBBox: true, X: 10, Y: 10, Width: 50, Height: 50},
	}
	moved := []HashableNode{
		{NodeID: "section", ParentID: "", NodeType: "SECTION", Name: "s", Depth: 0, HasBBox: true, X: 999, Y: 999, Width: 100, Height: 100},
		{NodeID: "frame", ParentID: "section", NodeType: "FRAME", Name: "f", Depth: 1, OrderIndex: 0, HasBBox: true, X: 10, Y: 10, Width: 50, Height: 50},
	}
	if ComputeContentHash("section", atRest) != ComputeContentHash("section", moved) {
		t.Errorf("content_hash flipped on root move — should be position-independent")
	}
	pa := ComputePositionHash("section", atRest)
	pm := ComputePositionHash("section", moved)
	if pa == pm {
		t.Errorf("position_hash should have flipped on root move")
	}
}

func TestComputeContentHash_FrameContentChangeFlips(t *testing.T) {
	// Add a new TEXT node inside the section → content_hash MUST differ.
	before := []HashableNode{
		{NodeID: "section", ParentID: "", NodeType: "SECTION", Name: "s", Depth: 0},
		{NodeID: "frame", ParentID: "section", NodeType: "FRAME", Name: "f", Depth: 1, OrderIndex: 0},
	}
	after := []HashableNode{
		{NodeID: "section", ParentID: "", NodeType: "SECTION", Name: "s", Depth: 0},
		{NodeID: "frame", ParentID: "section", NodeType: "FRAME", Name: "f", Depth: 1, OrderIndex: 0},
		{NodeID: "text", ParentID: "frame", NodeType: "TEXT", Name: "Hello", Depth: 2, OrderIndex: 0, HasBBox: true, X: 5, Y: 5, Width: 50, Height: 20},
	}
	if ComputeContentHash("section", before) == ComputeContentHash("section", after) {
		t.Errorf("content_hash unchanged after adding a child — expected flip")
	}
}

func TestComputeContentHash_NameRenameOfDescendantFlips(t *testing.T) {
	// Renaming a non-root node IS a content change.
	a := []HashableNode{
		{NodeID: "section", ParentID: "", NodeType: "SECTION", Name: "s", Depth: 0},
		{NodeID: "frame", ParentID: "section", NodeType: "FRAME", Name: "Old Name", Depth: 1, OrderIndex: 0},
	}
	b := []HashableNode{
		{NodeID: "section", ParentID: "", NodeType: "SECTION", Name: "s", Depth: 0},
		{NodeID: "frame", ParentID: "section", NodeType: "FRAME", Name: "New Name", Depth: 1, OrderIndex: 0},
	}
	if ComputeContentHash("section", a) == ComputeContentHash("section", b) {
		t.Errorf("descendant rename should flip content_hash")
	}
}

func TestComputeContentHash_EmptySubtreeReturnsSentinel(t *testing.T) {
	// Section with no children.
	nodes := []HashableNode{
		{NodeID: "section", ParentID: "", NodeType: "SECTION", Name: "s", Depth: 0},
	}
	if ComputeContentHash("section", nodes) != EmptySubtreeHash {
		t.Errorf("empty subtree should return sentinel %q", EmptySubtreeHash)
	}
}

func TestComputeContentHash_FiltersOutSiblingsOfRoot(t *testing.T) {
	// A sibling section under the same page is in `all` but should not
	// participate in the hash for section A.
	all := []HashableNode{
		{NodeID: "page", ParentID: "", NodeType: "CANVAS", Name: "p", Depth: 0},
		{NodeID: "section-a", ParentID: "page", NodeType: "SECTION", Name: "A", Depth: 1, OrderIndex: 0},
		{NodeID: "section-b", ParentID: "page", NodeType: "SECTION", Name: "B", Depth: 1, OrderIndex: 1},
		{NodeID: "frame-a", ParentID: "section-a", NodeType: "FRAME", Name: "F", Depth: 2, OrderIndex: 0},
		{NodeID: "frame-b", ParentID: "section-b", NodeType: "FRAME", Name: "G", Depth: 2, OrderIndex: 0},
	}
	hA := ComputeContentHash("section-a", all)
	// Change section-b's child (sibling subtree). Hash of section-a must
	// be unaffected.
	all[4].Name = "Different"
	hA2 := ComputeContentHash("section-a", all)
	if hA != hA2 {
		t.Errorf("section-a hash changed when only a sibling subtree mutated")
	}
}

func TestComputePositionHash_FlipsOnNameRename(t *testing.T) {
	nodes := []HashableNode{
		{NodeID: "section", ParentID: "", NodeType: "SECTION", Name: "Old", Depth: 0, HasBBox: true, X: 0, Y: 0, Width: 100, Height: 100, OrderIndex: 0},
	}
	before := ComputePositionHash("section", nodes)
	nodes[0].Name = "New"
	after := ComputePositionHash("section", nodes)
	if before == after {
		t.Errorf("position_hash should flip on rename")
	}
}

func TestComputePositionHash_FlipsOnXYMove(t *testing.T) {
	nodes := []HashableNode{
		{NodeID: "section", ParentID: "", NodeType: "SECTION", Name: "s", Depth: 0, HasBBox: true, X: 0, Y: 0, Width: 100, Height: 100, OrderIndex: 0},
	}
	before := ComputePositionHash("section", nodes)
	nodes[0].X = 200
	after := ComputePositionHash("section", nodes)
	if before == after {
		t.Errorf("position_hash should flip on x change")
	}
}

func TestComputePositionHash_MissingNodeReturnsEmpty(t *testing.T) {
	nodes := []HashableNode{
		{NodeID: "other", ParentID: "", NodeType: "SECTION", Name: "x", Depth: 0},
	}
	if ComputePositionHash("section", nodes) != "" {
		t.Errorf("missing node should return empty string")
	}
}

func TestComputeContentHash_FloatFormattingStable(t *testing.T) {
	// 100.0 vs 100 should produce identical hashes (both rendered as "100").
	a := []HashableNode{
		{NodeID: "r", ParentID: "", NodeType: "SECTION", Depth: 0},
		{NodeID: "c", ParentID: "r", NodeType: "FRAME", Name: "f", Depth: 1, HasBBox: true, X: 100, Y: 100, Width: 50, Height: 50},
	}
	b := []HashableNode{
		{NodeID: "r", ParentID: "", NodeType: "SECTION", Depth: 0},
		{NodeID: "c", ParentID: "r", NodeType: "FRAME", Name: "f", Depth: 1, HasBBox: true, X: 100.0, Y: 100.0, Width: 50.0, Height: 50.0},
	}
	if ComputeContentHash("r", a) != ComputeContentHash("r", b) {
		t.Errorf("100 vs 100.0 produced different hashes")
	}
}
