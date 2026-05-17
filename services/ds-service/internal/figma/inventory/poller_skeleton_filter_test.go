package inventory

import (
	"testing"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/projects"
)

// poller_skeleton_filter_test.go — verifies the in-memory frame filter
// the poller hands to TenantRepo.AutoSkeletonPRDStates (plan
// 2026-05-17-002 U2b). Mirrors the contract on
// projects.ListSectionFrames but runs against the in-memory subtree slice
// rather than the read-pool blob.

func TestFilterSectionFrames_HappyPath(t *testing.T) {
	const sectionID = "10:1"
	nodes := []projects.FigmaNodeRow{
		{NodeID: sectionID, NodeType: "SECTION", Name: "Wallet/M2M", Depth: 1},
		{NodeID: "11:1", ParentID: sectionID, NodeType: "FRAME", Name: "Cold state", Depth: 2, X: 0, Y: 0},
		{NodeID: "11:2", ParentID: sectionID, NodeType: "INSTANCE", Name: "Hot state", Depth: 2, X: 0, Y: 100},
		{NodeID: "11:3", ParentID: sectionID, NodeType: "COMPONENT", Name: "Empty", Depth: 2, X: 0, Y: 200},
		// Excluded — wrong types at the same depth.
		{NodeID: "11:4", ParentID: sectionID, NodeType: "TEXT", Name: "label", Depth: 2, X: 0, Y: 50},
		{NodeID: "11:5", ParentID: sectionID, NodeType: "RECTANGLE", Name: "bg", Depth: 2, X: 0, Y: 150},
		{NodeID: "11:6", ParentID: sectionID, NodeType: "GROUP", Name: "grp", Depth: 2, X: 0, Y: 250},
		// Excluded — too deep (grandchildren of the section).
		{NodeID: "12:1", ParentID: "11:1", NodeType: "FRAME", Name: "nested", Depth: 3, X: 0, Y: 0},
	}

	got := filterSectionFrames(sectionID, nodes)
	if len(got) != 3 {
		t.Fatalf("got %d frames, want 3: %+v", len(got), got)
	}
	if got[0].Name != "Cold state" || got[1].Name != "Hot state" || got[2].Name != "Empty" {
		t.Errorf("order: got [%s, %s, %s], want [Cold state, Hot state, Empty]",
			got[0].Name, got[1].Name, got[2].Name)
	}
}

func TestFilterSectionFrames_SortsByYThenX(t *testing.T) {
	const sectionID = "10:1"
	nodes := []projects.FigmaNodeRow{
		{NodeID: sectionID, NodeType: "SECTION", Depth: 1},
		{NodeID: "a", ParentID: sectionID, NodeType: "FRAME", Name: "B-row1", Depth: 2, X: 100, Y: 0},
		{NodeID: "b", ParentID: sectionID, NodeType: "FRAME", Name: "A-row1", Depth: 2, X: 0, Y: 0},
		{NodeID: "c", ParentID: sectionID, NodeType: "FRAME", Name: "row2", Depth: 2, X: 0, Y: 100},
	}
	got := filterSectionFrames(sectionID, nodes)
	want := []string{"A-row1", "B-row1", "row2"}
	for i, w := range want {
		if got[i].Name != w {
			t.Errorf("position %d: got %q, want %q", i, got[i].Name, w)
		}
	}
}

func TestFilterSectionFrames_EmptyCases(t *testing.T) {
	// Nil input.
	if out := filterSectionFrames("10:1", nil); len(out) != 0 {
		t.Errorf("nil nodes: got %d frames, want 0", len(out))
	}
	// Empty section id.
	nodes := []projects.FigmaNodeRow{
		{NodeID: "10:1", NodeType: "SECTION", Depth: 1},
		{NodeID: "11:1", ParentID: "10:1", NodeType: "FRAME", Name: "X", Depth: 2},
	}
	if out := filterSectionFrames("", nodes); len(out) != 0 {
		t.Errorf("empty section id: got %d frames, want 0", len(out))
	}
	// Section absent from subtree (data drift).
	if out := filterSectionFrames("missing:1", nodes); len(out) != 0 {
		t.Errorf("missing section: got %d frames, want 0", len(out))
	}
}
