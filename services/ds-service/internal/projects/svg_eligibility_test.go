package projects

import (
	"strings"
	"testing"
)

// reasonsContain reports whether any reason in res starts with prefix.
// Reasons may carry typed suffixes ("unsupported_type:RECTANGLE_PHOTO");
// using a prefix match keeps the assertion robust to those.
func reasonsContain(res SVGEligibility, prefix string) bool {
	for _, r := range res.Reasons {
		if strings.HasPrefix(r, prefix) {
			return true
		}
	}
	return false
}

func TestIsSVGEligible_VectorOnlyTreeAccepted(t *testing.T) {
	tree := []byte(`{"document":{"id":"root","type":"FRAME","children":[
		{"id":"v1","type":"VECTOR","fills":[{"type":"SOLID","color":{"r":0,"g":0,"b":0}}]},
		{"id":"e1","type":"ELLIPSE"},
		{"id":"r1","type":"RECTANGLE","fills":[{"type":"SOLID"}]}
	]}}`)
	got := IsSVGEligible(tree)
	if !got.OK {
		t.Errorf("expected eligible; got reasons=%v", got.Reasons)
	}
}

func TestIsSVGEligible_ImageFillRejected(t *testing.T) {
	// Image fill at the root.
	tree := []byte(`{"document":{"id":"root","type":"RECTANGLE","fills":[{"type":"IMAGE","imageRef":"abc"}]}}`)
	got := IsSVGEligible(tree)
	if got.OK {
		t.Errorf("expected NOT eligible for IMAGE fill")
	}
	if !reasonsContain(got, "image_fill") {
		t.Errorf("missing image_fill reason; got %v", got.Reasons)
	}
}

func TestIsSVGEligible_NestedImageFillRejected(t *testing.T) {
	// Image fill buried 3 levels deep — walker must descend past wrappers.
	tree := []byte(`{"document":{"id":"root","type":"FRAME","children":[
		{"id":"g1","type":"GROUP","children":[
			{"id":"f1","type":"FRAME","children":[
				{"id":"r1","type":"RECTANGLE","fills":[{"type":"IMAGE","imageRef":"buried"}]}
			]}
		]}
	]}}`)
	got := IsSVGEligible(tree)
	if got.OK {
		t.Errorf("expected NOT eligible for nested IMAGE fill")
	}
}

func TestIsSVGEligible_LayerBlurRejected(t *testing.T) {
	tree := []byte(`{"document":{"id":"root","type":"FRAME","effects":[{"type":"LAYER_BLUR","radius":4,"visible":true}],"children":[]}}`)
	got := IsSVGEligible(tree)
	if got.OK {
		t.Errorf("expected NOT eligible for LAYER_BLUR")
	}
	if !reasonsContain(got, "blur_effect") {
		t.Errorf("missing blur_effect reason; got %v", got.Reasons)
	}
}

func TestIsSVGEligible_BackgroundBlurRejected(t *testing.T) {
	tree := []byte(`{"document":{"id":"root","type":"FRAME","effects":[{"type":"BACKGROUND_BLUR","radius":8}],"children":[]}}`)
	got := IsSVGEligible(tree)
	if got.OK {
		t.Errorf("expected NOT eligible for BACKGROUND_BLUR")
	}
}

func TestIsSVGEligible_HiddenBlurAllowed(t *testing.T) {
	// A blur effect that is toggled invisible doesn't render — must not
	// disqualify the subtree.
	tree := []byte(`{"document":{"id":"root","type":"FRAME","effects":[{"type":"LAYER_BLUR","radius":4,"visible":false}],"children":[
		{"id":"v1","type":"VECTOR"}
	]}}`)
	got := IsSVGEligible(tree)
	if !got.OK {
		t.Errorf("hidden blur should not disqualify; got reasons=%v", got.Reasons)
	}
}

func TestIsSVGEligible_UnsafeBlendModeRejected(t *testing.T) {
	tree := []byte(`{"document":{"id":"root","type":"FRAME","blendMode":"LUMINOSITY","children":[]}}`)
	got := IsSVGEligible(tree)
	if got.OK {
		t.Errorf("expected NOT eligible for LUMINOSITY blendMode")
	}
	if !reasonsContain(got, "blend_mode:") {
		t.Errorf("missing blend_mode reason; got %v", got.Reasons)
	}
}

func TestIsSVGEligible_NormalAndPassThroughBlendModesAllowed(t *testing.T) {
	for _, mode := range []string{"NORMAL", "PASS_THROUGH", ""} {
		tree := []byte(`{"document":{"id":"r","type":"FRAME","blendMode":"` + mode + `","children":[{"id":"v","type":"VECTOR"}]}}`)
		got := IsSVGEligible(tree)
		if !got.OK {
			t.Errorf("blendMode=%q should be allowed; got reasons=%v", mode, got.Reasons)
		}
	}
}

func TestIsSVGEligible_UnsupportedTypeRejected(t *testing.T) {
	// SLICE is a Figma annotation-only node type — no SVG equivalent.
	tree := []byte(`{"document":{"id":"root","type":"FRAME","children":[
		{"id":"s1","type":"SLICE"}
	]}}`)
	got := IsSVGEligible(tree)
	if got.OK {
		t.Errorf("expected NOT eligible for SLICE child")
	}
	if !reasonsContain(got, "unsupported_type:SLICE") {
		t.Errorf("missing unsupported_type:SLICE reason; got %v", got.Reasons)
	}
}

func TestIsSVGEligible_InvisibleNodeIgnored(t *testing.T) {
	// An invisible IMAGE fill child must not disqualify — it doesn't render.
	tree := []byte(`{"document":{"id":"root","type":"FRAME","children":[
		{"id":"hidden","type":"RECTANGLE","visible":false,"fills":[{"type":"IMAGE","imageRef":"x"}]},
		{"id":"v1","type":"VECTOR"}
	]}}`)
	got := IsSVGEligible(tree)
	if !got.OK {
		t.Errorf("invisible IMAGE-fill node must not disqualify; got reasons=%v", got.Reasons)
	}
}

func TestIsSVGEligible_RemovedNodeIgnored(t *testing.T) {
	// `removed: true` is Figma's tombstone marker post-edit; same skip rule.
	tree := []byte(`{"document":{"id":"root","type":"FRAME","children":[
		{"id":"gone","type":"RECTANGLE","removed":true,"fills":[{"type":"IMAGE","imageRef":"x"}]},
		{"id":"v1","type":"VECTOR"}
	]}}`)
	got := IsSVGEligible(tree)
	if !got.OK {
		t.Errorf("removed node must not disqualify; got reasons=%v", got.Reasons)
	}
}

func TestIsSVGEligible_ClipWithCardinalRotationAllowed(t *testing.T) {
	// 90° rotation has off-diagonal=±1 — cardinal, safe.
	tree := []byte(`{"document":{"id":"root","type":"FRAME","clipsContent":true,"children":[
		{"id":"r","type":"RECTANGLE","relativeTransform":[[0,1,10],[-1,0,20]]}
	]}}`)
	got := IsSVGEligible(tree)
	if !got.OK {
		t.Errorf("clip + 90° rotation must be allowed; got reasons=%v", got.Reasons)
	}
}

func TestIsSVGEligible_ClipWithSkewRotationRejected(t *testing.T) {
	// Off-diagonal of 0.5 → 30° rotation, non-cardinal.
	tree := []byte(`{"document":{"id":"root","type":"FRAME","clipsContent":true,"children":[
		{"id":"r","type":"RECTANGLE","relativeTransform":[[0.866,0.5,10],[-0.5,0.866,20]]}
	]}}`)
	got := IsSVGEligible(tree)
	if got.OK {
		t.Errorf("expected NOT eligible for clip + skew rotation")
	}
	if !reasonsContain(got, "clip_with_skew_rotation") {
		t.Errorf("missing clip_with_skew_rotation reason; got %v", got.Reasons)
	}
}

func TestIsSVGEligible_EmptyAndMalformed(t *testing.T) {
	cases := [][]byte{
		nil,
		[]byte(""),
		[]byte("not json"),
		[]byte(`{"document":null}`),
	}
	for i, in := range cases {
		got := IsSVGEligible(in)
		if got.OK && len(in) > 0 && string(in) != `{"document":null}` {
			t.Errorf("case %d: expected NOT eligible for malformed input", i)
		}
		if len(in) == 0 && got.OK {
			t.Errorf("case %d: expected NOT eligible for empty input", i)
		}
	}
}

func TestIsSVGEligible_DepthCap(t *testing.T) {
	// Build a chain 70 nodes deep; should trip max_depth_exceeded.
	var b strings.Builder
	b.WriteString(`{"document":`)
	for i := 0; i < 70; i++ {
		b.WriteString(`{"type":"FRAME","children":[`)
	}
	b.WriteString(`{"type":"VECTOR"}`)
	for i := 0; i < 70; i++ {
		b.WriteString(`]}`)
	}
	b.WriteString(`}`)
	got := IsSVGEligible([]byte(b.String()))
	if got.OK {
		t.Errorf("expected NOT eligible for >60 deep tree")
	}
	if !reasonsContain(got, "max_depth_exceeded") {
		t.Errorf("missing max_depth_exceeded reason; got %v", got.Reasons)
	}
}

// Realistic icon shape — the kind of cluster Phase 2 expects to flip to
// SVG. Mirrors the Figma export of "Icons/Home" in production.
func TestIsSVGEligible_RealisticIconAccepted(t *testing.T) {
	tree := []byte(`{"document":{
		"id":"icon-home","type":"INSTANCE","name":"Icons/Home",
		"absoluteBoundingBox":{"x":0,"y":0,"width":24,"height":24},
		"children":[{
			"id":"v1","type":"VECTOR",
			"fills":[{"type":"SOLID","color":{"r":0.1,"g":0.1,"b":0.1,"a":1}}],
			"strokes":[{"type":"SOLID"}]
		}]
	}}`)
	got := IsSVGEligible(tree)
	if !got.OK {
		t.Errorf("realistic icon must be eligible; got reasons=%v", got.Reasons)
	}
}

// Sanity guard: short-circuit on first failure. If a tree has BOTH an
// image fill AND a blur, we record one reason and stop walking — the
// caller doesn't need exhaustive diagnostics.
func TestIsSVGEligible_ShortCircuitsOnFirstFailure(t *testing.T) {
	tree := []byte(`{"document":{"id":"r","type":"FRAME",
		"effects":[{"type":"LAYER_BLUR","radius":4,"visible":true}],
		"fills":[{"type":"IMAGE","imageRef":"x"}],
		"children":[]
	}}`)
	got := IsSVGEligible(tree)
	if got.OK {
		t.Errorf("expected NOT eligible")
	}
	if len(got.Reasons) != 1 {
		t.Errorf("expected exactly 1 reason (short-circuit); got %v", got.Reasons)
	}
}

func TestSanitizeReasonValue(t *testing.T) {
	cases := map[string]string{
		"":                  "",
		"NORMAL":            "NORMAL",
		"BACKGROUND_BLUR":   "BACKGROUND_BLUR",
		"some/odd value":    "some_odd_value",
		"prefix:trailing":   "prefix_trailing",
		strings.Repeat("a", 50): strings.Repeat("a", 32), // 32-char cap
	}
	for in, want := range cases {
		got := sanitizeReasonValue(in)
		if got != want {
			t.Errorf("sanitize(%q) = %q; want %q", in, got, want)
		}
	}
}
