package projects

import (
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
