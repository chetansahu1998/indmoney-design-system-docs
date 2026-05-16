package projects

import (
	"context"
	"testing"
)

func TestSubtreeBlob_RoundTrip(t *testing.T) {
	in := []FigmaNodeRow{
		{NodeID: "0:1", ParentID: "", NodeType: "CANVAS", Name: "Page 1", Depth: 1, OrderIndex: 0},
		{NodeID: "10:1", ParentID: "0:1", NodeType: "SECTION", Name: "Wallet/Main", HasBBox: true, X: 0, Y: 0, Width: 1200, Height: 800, Depth: 2, OrderIndex: 0},
		{NodeID: "10:2", ParentID: "10:1", NodeType: "FRAME", Name: "Hero", HasBBox: true, X: 16, Y: 16, Width: 343, Height: 56, Depth: 3, OrderIndex: 0},
		{NodeID: "10:3", ParentID: "10:2", NodeType: "INSTANCE", Name: "Left Icon/Default", HasBBox: true, X: 16, Y: 16, Width: 24, Height: 24, Depth: 4, OrderIndex: 0, ComponentID: "229:4715", ComponentKey: "abc123"},
		{NodeID: "10:4", ParentID: "10:2", NodeType: "TEXT", Name: "Reliance", HasBBox: true, X: 48, Y: 16, Width: 200, Height: 24, Depth: 4, OrderIndex: 1},
		{NodeID: "10:5", ParentID: "10:2", NodeType: "VECTOR", Name: "Vector", Depth: 4, OrderIndex: 2},
	}
	blob, err := EncodeSubtreeBlob(in)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if len(blob) == 0 {
		t.Fatalf("encode: expected non-empty blob, got 0 bytes")
	}
	out, err := DecodeSubtreeBlob(blob)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != len(in) {
		t.Fatalf("round-trip length: got %d, want %d", len(out), len(in))
	}
	for i := range in {
		if out[i] != in[i] {
			// Field-by-field diff for clarity.
			t.Errorf("row %d mismatch:\n  in:  %+v\n  out: %+v", i, in[i], out[i])
		}
	}
}

func TestSubtreeBlob_DeterministicEncoding(t *testing.T) {
	// Same struct slice, two encode passes — must yield byte-identical blobs.
	// Go's encoding/json visits struct fields in declaration order, so a
	// stable []figmaNodeBlobEntry input produces deterministic output. zstd
	// is deterministic on identical input.
	nodes := []FigmaNodeRow{
		{NodeID: "1", NodeType: "FRAME", Name: "A", Depth: 1, OrderIndex: 0, HasBBox: true, Width: 100, Height: 100},
		{NodeID: "2", ParentID: "1", NodeType: "INSTANCE", Name: "B", Depth: 2, OrderIndex: 0, ComponentID: "c1"},
	}
	blob1, err := EncodeSubtreeBlob(nodes)
	if err != nil {
		t.Fatalf("encode 1: %v", err)
	}
	blob2, err := EncodeSubtreeBlob(nodes)
	if err != nil {
		t.Fatalf("encode 2: %v", err)
	}
	if len(blob1) != len(blob2) {
		t.Fatalf("non-deterministic length: %d vs %d", len(blob1), len(blob2))
	}
	for i := range blob1 {
		if blob1[i] != blob2[i] {
			t.Fatalf("non-deterministic at byte %d: %x vs %x", i, blob1[i], blob2[i])
		}
	}
}

func TestSubtreeBlob_EmptyInput(t *testing.T) {
	// Empty/nil input → nil blob (callers write SQL NULL).
	blob, err := EncodeSubtreeBlob(nil)
	if err != nil {
		t.Fatalf("encode nil: %v", err)
	}
	if blob != nil {
		t.Errorf("encode nil: expected nil blob, got %d bytes", len(blob))
	}
	blob, err = EncodeSubtreeBlob([]FigmaNodeRow{})
	if err != nil {
		t.Fatalf("encode empty: %v", err)
	}
	if blob != nil {
		t.Errorf("encode empty: expected nil blob, got %d bytes", len(blob))
	}
}

func TestSubtreeBlob_DecodeEmptyInput(t *testing.T) {
	// nil/empty input → empty slice, no error.
	out, err := DecodeSubtreeBlob(nil)
	if err != nil {
		t.Fatalf("decode nil: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("decode nil: expected empty slice, got %d", len(out))
	}
	out, err = DecodeSubtreeBlob([]byte{})
	if err != nil {
		t.Fatalf("decode []byte{}: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("decode []byte{}: expected empty slice, got %d", len(out))
	}
}

func TestSubtreeBlob_CorruptedBytes(t *testing.T) {
	// Random non-zstd bytes → decompress error surfaces.
	_, err := DecodeSubtreeBlob([]byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05})
	if err == nil {
		t.Errorf("expected decompression error on garbage bytes, got nil")
	}
}

func TestSubtreeBlob_ValidZstdInvalidJSON(t *testing.T) {
	// Valid zstd blob whose decompressed payload is not a JSON array → unmarshal error.
	bogus, err := CompressTreeZstd("not-a-json-array")
	if err != nil {
		t.Fatalf("setup: compress: %v", err)
	}
	_, err = DecodeSubtreeBlob(bogus)
	if err == nil {
		t.Errorf("expected unmarshal error on non-JSON-array payload, got nil")
	}
}

func TestSubtreeBlob_LargeSubtreeCompressionRatio(t *testing.T) {
	// Build a realistic ~100-node mixed-type subtree (mirrors a typical
	// list-row "Position card" hand-build). Verify the compressed blob is
	// well under the 256KB warn threshold from U4's approach.
	nodes := make([]FigmaNodeRow, 100)
	for i := range nodes {
		nodes[i] = FigmaNodeRow{
			NodeID:     "10:" + itoa(i),
			ParentID:   "10:" + itoa(i/3),
			NodeType:   pickType(i),
			Name:       pickName(i),
			HasBBox:    true,
			X:          float64(i % 12 * 28),
			Y:          float64(i / 12 * 24),
			Width:      24,
			Height:     24,
			Depth:      2 + i%4,
			OrderIndex: i,
		}
	}
	blob, err := EncodeSubtreeBlob(nodes)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if len(blob) > 32*1024 {
		t.Errorf("100-node blob compressed to %d bytes; expected well under 32 KB", len(blob))
	}
	// Round-trip still works.
	out, err := DecodeSubtreeBlob(blob)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != len(nodes) {
		t.Fatalf("round-trip length: got %d, want %d", len(out), len(nodes))
	}
}

// ─── test helpers ────────────────────────────────────────────────────────────

func itoa(i int) string {
	const digits = "0123456789"
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = digits[i%10]
		i /= 10
	}
	return string(buf[pos:])
}

func pickType(i int) string {
	switch i % 5 {
	case 0:
		return "FRAME"
	case 1:
		return "INSTANCE"
	case 2:
		return "TEXT"
	case 3:
		return "VECTOR"
	default:
		return "RECTANGLE"
	}
}

func pickName(i int) string {
	switch i % 4 {
	case 0:
		return "Left Icon/Default"
	case 1:
		return "Right Text"
	case 2:
		return "Subtext"
	default:
		return "Vector"
	}
}

// ─── ListSectionFrames (plan 002 U5) ─────────────────────────────────────────

// u5SeedSubtree is a section subtree fixture used by the U5 tests.
// Section sits at Depth=2 (matching the autosync poller's flattening
// convention — CANVAS=1, SECTION=2, direct-child FRAME=3); direct children
// are at Depth=3.
func u5SeedSubtree() []FigmaNodeRow {
	return []FigmaNodeRow{
		// Section (root of this subtree).
		{NodeID: "10:1", NodeType: "SECTION", Name: "Wallet/Main", HasBBox: true, X: 0, Y: 0, Width: 1200, Height: 800, Depth: 2, OrderIndex: 0},
	}
}

func TestListSectionFrames_HappyPath_FiveFramesInYOrder(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	// Five FRAME direct children at scrambled Y values.
	subtree := append(u5SeedSubtree(),
		FigmaNodeRow{NodeID: "10:5", ParentID: "10:1", NodeType: "FRAME", Name: "E", HasBBox: true, X: 0, Y: 400, Width: 100, Height: 100, Depth: 3, OrderIndex: 4},
		FigmaNodeRow{NodeID: "10:2", ParentID: "10:1", NodeType: "FRAME", Name: "B", HasBBox: true, X: 0, Y: 100, Width: 100, Height: 100, Depth: 3, OrderIndex: 1},
		FigmaNodeRow{NodeID: "10:4", ParentID: "10:1", NodeType: "FRAME", Name: "D", HasBBox: true, X: 0, Y: 300, Width: 100, Height: 100, Depth: 3, OrderIndex: 3},
		FigmaNodeRow{NodeID: "10:1F", ParentID: "10:1", NodeType: "FRAME", Name: "A", HasBBox: true, X: 0, Y: 0, Width: 100, Height: 100, Depth: 3, OrderIndex: 0},
		FigmaNodeRow{NodeID: "10:3", ParentID: "10:1", NodeType: "FRAME", Name: "C", HasBBox: true, X: 0, Y: 200, Width: 100, Height: 100, Depth: 3, OrderIndex: 2},
	)
	seedSection(t, repo, "fk-A", "0:1", "10:1", subtree)

	got, err := repo.ListSectionFrames(ctx, "fk-A", "10:1")
	if err != nil {
		t.Fatalf("ListSectionFrames: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("len: got %d, want 5", len(got))
	}
	wantNames := []string{"A", "B", "C", "D", "E"}
	for i, want := range wantNames {
		if got[i].Name != want {
			t.Errorf("row %d: name got %q, want %q (full row: %+v)", i, got[i].Name, want, got[i])
		}
	}
	// Spot-check fields on row 0.
	if got[0].NodeID != "10:1F" || got[0].ParentNodeID != "10:1" || got[0].Depth != 3 {
		t.Errorf("row 0 fields wrong: %+v", got[0])
	}
	if got[0].AbsX != 0 || got[0].AbsY != 0 || got[0].Width != 100 || got[0].Height != 100 {
		t.Errorf("row 0 bbox wrong: %+v", got[0])
	}
	if got[0].HasRender {
		t.Errorf("HasRender should be false in v1; got true")
	}
}

func TestListSectionFrames_DirectChildOnly_NestedFiltered(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	// Section → FRAME (direct) → TEXT (grandchild). Expect 1 row.
	subtree := append(u5SeedSubtree(),
		FigmaNodeRow{NodeID: "10:2", ParentID: "10:1", NodeType: "FRAME", Name: "Hero", HasBBox: true, X: 0, Y: 0, Width: 100, Height: 100, Depth: 3, OrderIndex: 0},
		FigmaNodeRow{NodeID: "10:3", ParentID: "10:2", NodeType: "TEXT", Name: "Title", Depth: 4, OrderIndex: 0},
	)
	seedSection(t, repo, "fk-A", "0:1", "10:1", subtree)

	got, err := repo.ListSectionFrames(ctx, "fk-A", "10:1")
	if err != nil {
		t.Fatalf("ListSectionFrames: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len: got %d, want 1 (only direct-child FRAME); got=%+v", len(got), got)
	}
	if got[0].NodeID != "10:2" {
		t.Errorf("expected direct child 10:2, got %s", got[0].NodeID)
	}
}

func TestListSectionFrames_NodeTypeFilter(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	// Six direct children at depth 3, mixed types. Only FRAME/INSTANCE/COMPONENT
	// should survive.
	subtree := append(u5SeedSubtree(),
		FigmaNodeRow{NodeID: "10:f", ParentID: "10:1", NodeType: "FRAME", Name: "F", HasBBox: true, Y: 0, Depth: 3, OrderIndex: 0},
		FigmaNodeRow{NodeID: "10:i", ParentID: "10:1", NodeType: "INSTANCE", Name: "I", HasBBox: true, Y: 10, Depth: 3, OrderIndex: 1},
		FigmaNodeRow{NodeID: "10:c", ParentID: "10:1", NodeType: "COMPONENT", Name: "C", HasBBox: true, Y: 20, Depth: 3, OrderIndex: 2},
		FigmaNodeRow{NodeID: "10:t", ParentID: "10:1", NodeType: "TEXT", Name: "T", Depth: 3, OrderIndex: 3},
		FigmaNodeRow{NodeID: "10:g", ParentID: "10:1", NodeType: "GROUP", Name: "G", Depth: 3, OrderIndex: 4},
		FigmaNodeRow{NodeID: "10:r", ParentID: "10:1", NodeType: "RECTANGLE", Name: "R", Depth: 3, OrderIndex: 5},
	)
	seedSection(t, repo, "fk-A", "0:1", "10:1", subtree)

	got, err := repo.ListSectionFrames(ctx, "fk-A", "10:1")
	if err != nil {
		t.Fatalf("ListSectionFrames: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len: got %d, want 3 (FRAME+INSTANCE+COMPONENT only); got=%+v", len(got), got)
	}
	wantTypes := map[string]bool{"10:f": true, "10:i": true, "10:c": true}
	for _, r := range got {
		if !wantTypes[r.NodeID] {
			t.Errorf("unexpected NodeID in result: %s", r.NodeID)
		}
	}
}

func TestListSectionFrames_DesignerCanonicalNamesPreserved(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	// Names that the auto-skeleton blocklist would filter — but U5 must
	// pass them through verbatim. The caller is responsible for any
	// downstream filtering.
	subtree := append(u5SeedSubtree(),
		FigmaNodeRow{NodeID: "10:2", ParentID: "10:1", NodeType: "FRAME", Name: "Frame 21234", HasBBox: true, Y: 0, Depth: 3, OrderIndex: 0},
		FigmaNodeRow{NodeID: "10:3", ParentID: "10:1", NodeType: "FRAME", Name: "Rectangle 4324", HasBBox: true, Y: 10, Depth: 3, OrderIndex: 1},
		FigmaNodeRow{NodeID: "10:4", ParentID: "10:1", NodeType: "FRAME", Name: "Cold state", HasBBox: true, Y: 20, Depth: 3, OrderIndex: 2},
	)
	seedSection(t, repo, "fk-A", "0:1", "10:1", subtree)

	got, err := repo.ListSectionFrames(ctx, "fk-A", "10:1")
	if err != nil {
		t.Fatalf("ListSectionFrames: %v", err)
	}
	want := []string{"Frame 21234", "Rectangle 4324", "Cold state"}
	if len(got) != len(want) {
		t.Fatalf("len: got %d, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Name != w {
			t.Errorf("row %d: got %q, want %q", i, got[i].Name, w)
		}
	}
}

func TestListSectionFrames_SameNameFramesReturnedSeparately(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	// Two FRAMEs with identical names at different Y — both must come back.
	// Caller (frame_tag.variant time) disambiguates.
	subtree := append(u5SeedSubtree(),
		FigmaNodeRow{NodeID: "10:2", ParentID: "10:1", NodeType: "FRAME", Name: "Cold state", HasBBox: true, Y: 0, Depth: 3, OrderIndex: 0},
		FigmaNodeRow{NodeID: "10:3", ParentID: "10:1", NodeType: "FRAME", Name: "Cold state", HasBBox: true, Y: 200, Depth: 3, OrderIndex: 1},
	)
	seedSection(t, repo, "fk-A", "0:1", "10:1", subtree)

	got, err := repo.ListSectionFrames(ctx, "fk-A", "10:1")
	if err != nil {
		t.Fatalf("ListSectionFrames: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len: got %d, want 2 (no dedup by name)", len(got))
	}
	if got[0].NodeID != "10:2" || got[1].NodeID != "10:3" {
		t.Errorf("expected [10:2, 10:3] by Y; got [%s, %s]", got[0].NodeID, got[1].NodeID)
	}
	if got[0].Name != got[1].Name {
		t.Errorf("expected duplicate names preserved; got %q vs %q", got[0].Name, got[1].Name)
	}
}

func TestListSectionFrames_SortByYThenX_Stable(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	// Same Y, different X → X ascending.
	// Same Y, same X → original insertion order (SliceStable).
	subtree := append(u5SeedSubtree(),
		// Tier 1: Y=0, three at increasing X.
		FigmaNodeRow{NodeID: "10:y0x100", ParentID: "10:1", NodeType: "FRAME", Name: "y0x100", HasBBox: true, X: 100, Y: 0, Depth: 3, OrderIndex: 0},
		FigmaNodeRow{NodeID: "10:y0x0", ParentID: "10:1", NodeType: "FRAME", Name: "y0x0", HasBBox: true, X: 0, Y: 0, Depth: 3, OrderIndex: 1},
		FigmaNodeRow{NodeID: "10:y0x50", ParentID: "10:1", NodeType: "FRAME", Name: "y0x50", HasBBox: true, X: 50, Y: 0, Depth: 3, OrderIndex: 2},
		// Tier 2: Y=100, two at identical X — must preserve insertion order.
		FigmaNodeRow{NodeID: "10:y100x0-first", ParentID: "10:1", NodeType: "FRAME", Name: "tieFirst", HasBBox: true, X: 0, Y: 100, Depth: 3, OrderIndex: 3},
		FigmaNodeRow{NodeID: "10:y100x0-second", ParentID: "10:1", NodeType: "FRAME", Name: "tieSecond", HasBBox: true, X: 0, Y: 100, Depth: 3, OrderIndex: 4},
	)
	seedSection(t, repo, "fk-A", "0:1", "10:1", subtree)

	got, err := repo.ListSectionFrames(ctx, "fk-A", "10:1")
	if err != nil {
		t.Fatalf("ListSectionFrames: %v", err)
	}
	wantOrder := []string{"10:y0x0", "10:y0x50", "10:y0x100", "10:y100x0-first", "10:y100x0-second"}
	if len(got) != len(wantOrder) {
		t.Fatalf("len: got %d, want %d", len(got), len(wantOrder))
	}
	for i, w := range wantOrder {
		if got[i].NodeID != w {
			t.Errorf("row %d: got %s, want %s; full order=%v", i, got[i].NodeID, w, frameIDs(got))
		}
	}
}

func TestListSectionFrames_EmptySection_NoChildren(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	// Section row exists with subtree containing only the section node.
	seedSection(t, repo, "fk-A", "0:1", "10:1", u5SeedSubtree())

	got, err := repo.ListSectionFrames(ctx, "fk-A", "10:1")
	if err != nil {
		t.Fatalf("ListSectionFrames: %v (want nil)", err)
	}
	if got == nil {
		t.Errorf("expected non-nil empty slice, got nil")
	}
	if len(got) != 0 {
		t.Errorf("expected empty slice, got %d rows: %+v", len(got), got)
	}
}

func TestListSectionFrames_NullBlob_ReturnsEmpty(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	// Section row exists with NULL subtree blob (deep-poll pending).
	// LoadSectionSubtree returns ErrNotFound; ListSectionFrames must
	// normalize to []FrameRow{} per contract.
	seedSection(t, repo, "fk-A", "0:1", "10:9", nil)

	got, err := repo.ListSectionFrames(ctx, "fk-A", "10:9")
	if err != nil {
		t.Fatalf("expected nil err for NULL blob; got %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Errorf("expected empty non-nil slice; got %v", got)
	}
}

func TestListSectionFrames_MissingSection_ReturnsEmpty(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	// No figma_section row at all → ErrNotFound from LoadSectionSubtree
	// → normalized to []FrameRow{}.
	got, err := repo.ListSectionFrames(ctx, "fk-nonexistent", "10:nope")
	if err != nil {
		t.Fatalf("expected nil err for missing section; got %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Errorf("expected empty non-nil slice; got %v", got)
	}
}

func TestListSectionFrames_SectionNodeAbsentFromBlob_ReturnsEmpty(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	// Edge case: figma_section row exists with a non-empty subtree blob,
	// but the section node itself is missing from the blob (data drift).
	// We can't derive childDepth → contract is empty, not error.
	subtree := []FigmaNodeRow{
		// Note: NodeID "other" — NOT "10:1" — so the section lookup fails.
		{NodeID: "other", NodeType: "FRAME", Name: "orphan", HasBBox: true, Depth: 3, OrderIndex: 0},
	}
	seedSection(t, repo, "fk-A", "0:1", "10:1", subtree)

	got, err := repo.ListSectionFrames(ctx, "fk-A", "10:1")
	if err != nil {
		t.Fatalf("expected nil err for section-absent-from-blob; got %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Errorf("expected empty non-nil slice; got %v", got)
	}
}

func TestListSectionFrames_TenantIsolation(t *testing.T) {
	d, tA, tB, _ := newTestDB(t)
	repoA := NewTenantRepo(d.DB, tA)
	repoB := NewTenantRepo(d.DB, tB)
	ctx := context.Background()

	subtree := append(u5SeedSubtree(),
		FigmaNodeRow{NodeID: "10:2", ParentID: "10:1", NodeType: "FRAME", Name: "TenantAOnly", HasBBox: true, Depth: 3, OrderIndex: 0},
	)
	seedSection(t, repoA, "fk-X", "0:1", "10:1", subtree)

	// Tenant A sees the frame.
	gotA, err := repoA.ListSectionFrames(ctx, "fk-X", "10:1")
	if err != nil {
		t.Fatalf("tenant A list: %v", err)
	}
	if len(gotA) != 1 {
		t.Errorf("tenant A: expected 1 frame, got %d", len(gotA))
	}

	// Tenant B sees nothing — LoadSectionSubtree tenant scoping is honored.
	gotB, err := repoB.ListSectionFrames(ctx, "fk-X", "10:1")
	if err != nil {
		t.Fatalf("tenant B list: %v", err)
	}
	if len(gotB) != 0 {
		t.Errorf("tenant B: expected zero frames (isolation), got %d: %+v", len(gotB), gotB)
	}
}

func TestListSectionFrames_TwentyFramesMixedTypes(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	subtree := u5SeedSubtree()
	// 20 children at depth 3, mixed types — only FRAME/INSTANCE/COMPONENT
	// should survive. Distribute Y values to verify sort.
	typeCycle := []string{"FRAME", "INSTANCE", "COMPONENT", "TEXT", "GROUP", "RECTANGLE", "FRAME"}
	wantCount := 0
	for i := 0; i < 20; i++ {
		nt := typeCycle[i%len(typeCycle)]
		if nt == "FRAME" || nt == "INSTANCE" || nt == "COMPONENT" {
			wantCount++
		}
		subtree = append(subtree, FigmaNodeRow{
			NodeID:     "10:n" + itoa(i),
			ParentID:   "10:1",
			NodeType:   nt,
			Name:       "row-" + itoa(i),
			HasBBox:    true,
			X:          float64(i * 10),
			Y:          float64(i * 50), // unique Y values → unambiguous order
			Width:      100,
			Height:     40,
			Depth:      3,
			OrderIndex: i,
		})
	}
	// Also stuff a deeper grandchild — must be filtered by depth gate.
	subtree = append(subtree, FigmaNodeRow{
		NodeID: "10:deep", ParentID: "10:n0", NodeType: "FRAME", Name: "deep", HasBBox: true, Depth: 4, OrderIndex: 0,
	})
	seedSection(t, repo, "fk-A", "0:1", "10:1", subtree)

	got, err := repo.ListSectionFrames(ctx, "fk-A", "10:1")
	if err != nil {
		t.Fatalf("ListSectionFrames: %v", err)
	}
	if len(got) != wantCount {
		t.Fatalf("len: got %d, want %d", len(got), wantCount)
	}
	// Verify ascending Y order.
	for i := 1; i < len(got); i++ {
		if got[i-1].AbsY > got[i].AbsY {
			t.Errorf("not sorted at row %d: AbsY=%v vs prev=%v", i, got[i].AbsY, got[i-1].AbsY)
		}
	}
	// All survivors must be in the type allowlist.
	for _, r := range got {
		// We don't carry NodeType through; verify by NodeID prefix lookup.
		// Simpler: confirm none of the IDs we tagged as TEXT/GROUP/RECTANGLE leaked.
		for i := 0; i < 20; i++ {
			nt := typeCycle[i%len(typeCycle)]
			if nt != "FRAME" && nt != "INSTANCE" && nt != "COMPONENT" && r.NodeID == "10:n"+itoa(i) {
				t.Errorf("excluded-type row leaked: %s (type %s)", r.NodeID, nt)
			}
		}
		if r.HasRender {
			t.Errorf("v1: HasRender must be false; got true for %s", r.NodeID)
		}
		if r.Depth != 3 {
			t.Errorf("row %s: depth got %d, want 3", r.NodeID, r.Depth)
		}
	}
}

// frameIDs extracts NodeIDs from a FrameRow slice for compact failure messages.
func frameIDs(rows []FrameRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.NodeID
	}
	return out
}
