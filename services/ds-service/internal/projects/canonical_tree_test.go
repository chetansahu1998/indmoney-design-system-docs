package projects

import (
	"bytes"
	"strings"
	"testing"
)

// Round-trip identity: compress then decompress must return the same JSON
// byte-for-byte. The most important property — without it any read after
// migration 0022 is corrupt.
func TestCompressTreeZstd_RoundTrip(t *testing.T) {
	cases := []string{
		`{}`,
		`{"document":{"id":"x","children":[]}}`,
		`{"document":{"children":[{"id":"a","fills":[{"type":"IMAGE","imageRef":"abc"}]}]}}`,
		// Pathological / large repetitive payload to exercise the encoder's
		// dictionary stage and confirm the 64 MB cap doesn't fire on a 4 MB
		// realistic tree.
		strings.Repeat(`{"k":"v"},`, 200_000),
	}
	for _, raw := range cases {
		blob, err := CompressTreeZstd(raw)
		if err != nil {
			t.Fatalf("CompressTreeZstd: %v (input %d bytes)", err, len(raw))
		}
		got, err := DecompressTreeZstd(blob)
		if err != nil {
			t.Fatalf("DecompressTreeZstd: %v (compressed %d bytes)", err, len(blob))
		}
		if got != raw {
			t.Fatalf("round-trip mismatch (raw=%d bytes, got=%d bytes)", len(raw), len(got))
		}
	}
}

// Empty input is the dual-NULL signal: callers write a NULL to the zstd
// column and ResolveCanonicalTree falls through to gz / legacy. Both
// helpers must accept "" / nil cleanly.
func TestCompressTreeZstd_EmptyInput(t *testing.T) {
	blob, err := CompressTreeZstd("")
	if err != nil {
		t.Fatalf("CompressTreeZstd(\"\"): %v", err)
	}
	if blob != nil {
		t.Errorf("expected nil blob for empty input, got %d bytes", len(blob))
	}
}

func TestDecompressTreeZstd_EmptyInput(t *testing.T) {
	got, err := DecompressTreeZstd(nil)
	if err != nil {
		t.Fatalf("DecompressTreeZstd(nil): %v", err)
	}
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
	got, err = DecompressTreeZstd([]byte{})
	if err != nil {
		t.Fatalf("DecompressTreeZstd([]): %v", err)
	}
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

// ResolveCanonicalTree priority: zstd > gz > legacy.
//
// During the migration window rows can have any combination populated.
// The reader MUST prefer zstd because that's the freshest representation
// (Phase 1's write path always writes zstd + NULLs the others). If a row
// somehow has both gz AND zstd, picking zstd is correct because it
// reflects the most recent write.
func TestResolveCanonicalTree_Priority(t *testing.T) {
	const want = `{"id":"p","priority":"zstd"}`
	zstdBlob, err := CompressTreeZstd(want)
	if err != nil {
		t.Fatalf("compress zstd: %v", err)
	}
	gzBlob, err := CompressTree(`{"id":"p","priority":"gz"}`)
	if err != nil {
		t.Fatalf("compress gz: %v", err)
	}
	legacy := `{"id":"p","priority":"legacy"}`

	got, err := ResolveCanonicalTree(legacy, gzBlob, zstdBlob)
	if err != nil {
		t.Fatalf("ResolveCanonicalTree: %v", err)
	}
	if got != want {
		t.Errorf("priority mismatch: got %q; want %q (zstd should win over gz+legacy)", got, want)
	}
}

func TestResolveCanonicalTree_GzOnly(t *testing.T) {
	const want = `{"id":"gz-fallback"}`
	gzBlob, err := CompressTree(want)
	if err != nil {
		t.Fatalf("compress gz: %v", err)
	}
	got, err := ResolveCanonicalTree("", gzBlob, nil)
	if err != nil {
		t.Fatalf("ResolveCanonicalTree: %v", err)
	}
	if got != want {
		t.Errorf("gz-only path: got %q; want %q", got, want)
	}
}

func TestResolveCanonicalTree_LegacyOnly(t *testing.T) {
	const want = `{"id":"legacy-fallback"}`
	got, err := ResolveCanonicalTree(want, nil, nil)
	if err != nil {
		t.Fatalf("ResolveCanonicalTree: %v", err)
	}
	if got != want {
		t.Errorf("legacy-only path: got %q; want %q", got, want)
	}
}

func TestResolveCanonicalTree_AllEmpty(t *testing.T) {
	got, err := ResolveCanonicalTree("", nil, nil)
	if err != nil {
		t.Fatalf("ResolveCanonicalTree empty: %v", err)
	}
	if got != "" {
		t.Errorf("all-empty path: got %q; want empty string", got)
	}
}

// Backward compat: a row written under the T8 gz path must still decode
// correctly after Phase 1 ships. cmd/compress-trees --to=zstd will
// migrate them eventually, but during the window both representations
// coexist and the reader handles either.
func TestResolveCanonicalTree_PreservesGzipBackwardCompat(t *testing.T) {
	const tree = `{"document":{"id":"root","children":[{"id":"c1"}]}}`
	gzBlob, err := CompressTree(tree)
	if err != nil {
		t.Fatalf("compress gz: %v", err)
	}
	// Sanity: the gz blob is recognisably gzip (RFC 1952 magic 1f 8b).
	if len(gzBlob) < 2 || gzBlob[0] != 0x1f || gzBlob[1] != 0x8b {
		t.Errorf("expected gzip magic prefix; got %x", gzBlob[:min(2, len(gzBlob))])
	}
	got, err := ResolveCanonicalTree("", gzBlob, nil)
	if err != nil {
		t.Fatalf("ResolveCanonicalTree: %v", err)
	}
	if got != tree {
		t.Errorf("gz fallback didn't round-trip: got %q; want %q", got, tree)
	}
}

// zstd magic prefix sanity — guards against accidentally writing gzip
// bytes into the zstd column due to a future copy-paste error in the
// pipeline.
func TestCompressTreeZstd_ProducesZstdMagic(t *testing.T) {
	blob, err := CompressTreeZstd(`{"a":1}`)
	if err != nil {
		t.Fatalf("CompressTreeZstd: %v", err)
	}
	if len(blob) < 4 {
		t.Fatalf("blob too small: %d bytes", len(blob))
	}
	// RFC 8478 §3.1.1: zstd frame header magic = 28 b5 2f fd.
	want := []byte{0x28, 0xB5, 0x2F, 0xFD}
	if !bytes.Equal(blob[:4], want) {
		t.Errorf("expected zstd magic %x; got %x", want, blob[:4])
	}
}

// Compression actually saves space — pin the property so a future
// regression (wrong level / pass-through encoder / pretty-printing
// the JSON) shows up immediately.
func TestCompressTreeZstd_ActuallyCompresses(t *testing.T) {
	// Generate a repetitive JSON payload so even a small sample exhibits
	// dictionary gain. Real canonical_trees compress 9× on the production
	// corpus; this lower-bound assertion of >2× protects against
	// "compression silently disabled" bugs without coupling the test to
	// the exact level constant.
	raw := strings.Repeat(`{"id":"node","type":"FRAME","fills":[{"type":"SOLID","color":{"r":1,"g":1,"b":1,"a":1}}]},`, 500)
	blob, err := CompressTreeZstd(raw)
	if err != nil {
		t.Fatalf("CompressTreeZstd: %v", err)
	}
	ratio := float64(len(raw)) / float64(len(blob))
	if ratio < 2.0 {
		t.Errorf("zstd ratio too low: raw=%d compressed=%d ratio=%.2f×; want >=2×",
			len(raw), len(blob), ratio)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
