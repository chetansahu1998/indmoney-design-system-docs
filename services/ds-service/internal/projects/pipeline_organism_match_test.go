package projects

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// pipeline_organism_match_test.go — U3 walker tests drive against real
// canonical-tree fixtures pulled from the 2026-05-13 session probe
// (testdata/organism_fixtures/). The fixtures cover three sub-classes:
//
//   - wild-tax-1/2/3:           Tax Centre list-row hand-builds. Expected
//                               to match List on Surface skeleton (small
//                               atom set: Left Icon + Right Text + Right
//                               Icon).
//   - wild-dash-1..5:           Dashboard v5 list-row hand-builds. Range
//                               from 2 atoms (eq-1) to 9 atoms (sav, which
//                               includes Overline + Subtext + Badges).
//   - wild-sav, wild-eq-1/2:    Adjacent products' list-row hand-builds.
//   - ds-1/2/3:                 Published DS COMPONENT_SETs and SECTION.
//                               Walker should NOT emit candidates from
//                               COMPONENT_SET roots (those aren't FRAMEs).
//   - us1/us2, v4a/b/c, v5:     Position-card hand-builds (separate organism
//                               that has no published parent — Part D
//                               candidates).
//
// Walker correctness assertions:
//   1. Each wild-* fixture emits ≥ 1 candidate when wrapped in a synthetic
//      outer FRAME (the fixture root IS the candidate).
//   2. The candidate's AtomSet contains the expected atom slugs (resolved
//      via nameToAtomSlug heuristic).
//   3. Hash is deterministic — running the walker twice yields identical
//      hashes per fixture.
//   4. Hash is content-invariant — fingerprint hash for "Reliance Industries"
//      copy = fingerprint hash for the same shape with different copy
//      (R7: ignore copy-only diffs).

func loadFixture(t *testing.T, name string) map[string]any {
	t.Helper()
	path := filepath.Join("testdata", "organism_fixtures", name+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("parse fixture %s: %v", name, err)
	}
	// Figma /v1/files/<key>/nodes response shape: {nodes: {<id>: {document: ...}}}
	nodes, _ := raw["nodes"].(map[string]any)
	for _, v := range nodes {
		entry, _ := v.(map[string]any)
		if entry == nil {
			continue
		}
		doc, _ := entry["document"].(map[string]any)
		if doc != nil {
			return doc
		}
	}
	t.Fatalf("fixture %s missing nodes[*].document", name)
	return nil
}

// TestWalker_WildTax1_BasicCandidate confirms the simplest single-organism
// fixture: Reliance Industries list row should surface as 1+ candidate when
// the root is wrapped (it IS the organism, not a containing screen).
func TestWalker_WildTax1_BasicCandidate(t *testing.T) {
	root := loadFixture(t, "wild-tax-1")
	wrapped := WrapForOrganismWalk(root)
	out := WalkOrganismCandidates(wrapped)
	if len(out) == 0 {
		t.Fatalf("expected ≥1 candidate from wild-tax-1; got 0")
	}
	// The outer fixture FRAME should be the first candidate emitted.
	top := out[0]
	if top.FrameID != root["id"] {
		t.Errorf("expected top candidate FrameID = fixture root id (%v); got %q", root["id"], top.FrameID)
	}
	// At minimum: left-icon-default + right-text + right-icon resolve.
	atoms := map[string]bool{}
	for _, a := range top.AtomSet {
		atoms[a] = true
	}
	for _, must := range []string{"left-icon-default", "right-text", "right-icon"} {
		if !atoms[must] {
			t.Errorf("AtomSet missing %q (got %v)", must, top.AtomSet)
		}
	}
}

// TestWalker_WildSav_RichComposition asserts the richest wild fixture
// surfaces with its full 9-atom set including Overline/Subtext/Badges.
func TestWalker_WildSav_RichComposition(t *testing.T) {
	root := loadFixture(t, "wild-sav")
	wrapped := WrapForOrganismWalk(root)
	out := WalkOrganismCandidates(wrapped)
	if len(out) == 0 {
		t.Fatalf("expected ≥1 candidate from wild-sav; got 0")
	}
	top := out[0]
	atoms := map[string]bool{}
	for _, a := range top.AtomSet {
		atoms[a] = true
	}
	// The "rich" signal: Badges, Overline, Subtext atoms appear.
	for _, must := range []string{"badges", "overline", "subtext", "left-icon-default"} {
		if !atoms[must] {
			t.Errorf("AtomSet missing %q (got %v)", must, top.AtomSet)
		}
	}
}

// TestWalker_ScreenRootSkipped checks that the production-style call (no
// wrapping) on a screen-shaped root (375×812) does NOT emit a candidate for
// the root itself. We synthesize a phone-screen root containing two
// list-row children and confirm:
//   - The root is NOT emitted.
//   - Each inner list-row IS evaluated.
func TestWalker_ScreenRootSkipped(t *testing.T) {
	wildTax := loadFixture(t, "wild-tax-1")
	wildEq := loadFixture(t, "wild-eq-2")
	phoneScreen := map[string]any{
		"id":   "phone-root",
		"name": "Phone screen",
		"type": "FRAME",
		"absoluteBoundingBox": map[string]any{
			"x": 0.0, "y": 0.0, "width": 375.0, "height": 812.0,
		},
		"children": []any{wildTax, wildEq},
	}
	out := WalkOrganismCandidates(phoneScreen)
	for _, fp := range out {
		if fp.FrameID == "phone-root" {
			t.Errorf("screen root must not be emitted as a candidate")
		}
	}
	// We expect at least one candidate per inner fixture (the inner FRAMEs
	// themselves qualify).
	sawTax := false
	sawEq := false
	for _, fp := range out {
		if fp.FrameID == wildTax["id"] {
			sawTax = true
		}
		if fp.FrameID == wildEq["id"] {
			sawEq = true
		}
	}
	if !sawTax {
		t.Errorf("expected wild-tax-1 root frame in candidates")
	}
	if !sawEq {
		t.Errorf("expected wild-eq-2 root frame in candidates")
	}
}

// TestWalker_Deterministic — same fixture, two runs, identical hashes per
// frame_id. R3 idempotency requirement.
func TestWalker_Deterministic(t *testing.T) {
	for _, name := range []string{"wild-tax-1", "wild-sav", "wild-dash-4", "wild-eq-2"} {
		t.Run(name, func(t *testing.T) {
			root := loadFixture(t, name)
			a := WalkOrganismCandidates(WrapForOrganismWalk(root))
			b := WalkOrganismCandidates(WrapForOrganismWalk(root))
			if len(a) != len(b) {
				t.Fatalf("candidate count drift: %d vs %d", len(a), len(b))
			}
			for i := range a {
				if a[i].FrameID != b[i].FrameID {
					t.Errorf("order drift @ %d: %q vs %q", i, a[i].FrameID, b[i].FrameID)
				}
				if a[i].Hash != b[i].Hash {
					t.Errorf("hash drift for %q: %q vs %q", a[i].FrameID, a[i].Hash, b[i].Hash)
				}
			}
		})
	}
}

// TestWalker_HashIgnoresCopy — two fingerprints with the same atom_set + slot
// topology but different TEXT character content must hash identically.
// Mirrors R7 (copy-only diffs are not structural).
func TestWalker_HashIgnoresCopy(t *testing.T) {
	// Build two synthetic FRAMEs with identical structure but different
	// TEXT character content. Both have 2 atom INSTANCEs (left-icon-default,
	// right-text) so they pass the organismMinAtomInstances threshold.
	mkFrame := func(textA string) map[string]any {
		return map[string]any{
			"id":   "frame-x",
			"name": "List Row",
			"type": "FRAME",
			"absoluteBoundingBox": map[string]any{"x": 0.0, "y": 0.0, "width": 343.0, "height": 56.0},
			"children": []any{
				map[string]any{
					"id":          "li1",
					"type":        "INSTANCE",
					"name":        "Left Icon/Default",
					"componentId": "229:4715",
					"absoluteBoundingBox": map[string]any{"x": 0.0, "y": 0.0, "width": 24.0, "height": 24.0},
					"children":    []any{map[string]any{"id": "li1-t", "type": "TEXT", "characters": textA, "name": "Reliance"}},
				},
				map[string]any{
					"id":          "rt1",
					"type":        "INSTANCE",
					"name":        "Right Text",
					"componentId": "228:5960",
					"absoluteBoundingBox": map[string]any{"x": 100.0, "y": 0.0, "width": 100.0, "height": 24.0},
					"children":    []any{map[string]any{"id": "rt1-t", "type": "TEXT", "characters": "+₹1,000"}},
				},
			},
		}
	}
	a := mkFrame("Reliance Industries")
	b := mkFrame("MRF Tyres")
	outA := WalkOrganismCandidates(WrapForOrganismWalk(a))
	outB := WalkOrganismCandidates(WrapForOrganismWalk(b))
	if len(outA) == 0 || len(outB) == 0 {
		t.Fatalf("expected candidates from both synthetic frames")
	}
	if outA[0].Hash != outB[0].Hash {
		t.Errorf("hash should be content-invariant for identical structure: %q vs %q", outA[0].Hash, outB[0].Hash)
	}
}

// TestWalker_NestedOrganisms — when one organism candidate sits inside
// another, both surface and the inner one carries parent_frame_id = outer.
func TestWalker_NestedOrganisms(t *testing.T) {
	// Outer FRAME (e.g. List on Card) contains an inner FRAME (a list row).
	// Both subtree-counts pass the min-instances threshold.
	makeRow := func(id string) map[string]any {
		return map[string]any{
			"id":   id,
			"name": "List/Full width",
			"type": "FRAME",
			"absoluteBoundingBox": map[string]any{"x": 0.0, "y": 0.0, "width": 343.0, "height": 56.0},
			"children": []any{
				map[string]any{"id": id + "-a", "type": "INSTANCE", "name": "Left Icon/Default", "componentId": "229:4715",
					"absoluteBoundingBox": map[string]any{"x": 0.0, "y": 0.0, "width": 24.0, "height": 24.0}},
				map[string]any{"id": id + "-b", "type": "INSTANCE", "name": "Right Text", "componentId": "228:5960",
					"absoluteBoundingBox": map[string]any{"x": 100.0, "y": 0.0, "width": 100.0, "height": 24.0}},
			},
		}
	}
	outer := map[string]any{
		"id":   "outer-card",
		"name": "List on Card",
		"type": "FRAME",
		"absoluteBoundingBox": map[string]any{"x": 0.0, "y": 0.0, "width": 343.0, "height": 200.0},
		"children": []any{makeRow("row-1"), makeRow("row-2"), makeRow("row-3")},
	}
	out := WalkOrganismCandidates(WrapForOrganismWalk(outer))
	// Expect: outer (3 inner rows × 2 INSTANCEs each = 6) + 3 rows = 4 candidates.
	if len(out) != 4 {
		t.Fatalf("expected 4 candidates (outer + 3 rows); got %d", len(out))
	}
	// Outer first (DFS).
	if out[0].FrameID != "outer-card" {
		t.Errorf("first candidate must be outer; got %q", out[0].FrameID)
	}
	if out[0].ParentFrameID != "" {
		t.Errorf("outer must have empty ParentFrameID; got %q", out[0].ParentFrameID)
	}
	for _, fp := range out[1:] {
		if fp.ParentFrameID != "outer-card" {
			t.Errorf("inner %q expected ParentFrameID=outer-card; got %q", fp.FrameID, fp.ParentFrameID)
		}
	}
}

// TestWalker_AtomSetDeduped — a FRAME with 5 INSTANCEs all named "Vector"
// resolves to 1 atom slug. organismMinAtomInstances=2 on raw count would
// pass, but the buildFingerprint check requires ≥ 2 atoms in AtomSet (else
// the candidate is an icon cluster, not an organism).
func TestWalker_AtomSetDeduped(t *testing.T) {
	frame := map[string]any{
		"id":   "icon-stack",
		"name": "Icons/Logo/HDFC",
		"type": "FRAME",
		"absoluteBoundingBox": map[string]any{"x": 0.0, "y": 0.0, "width": 32.0, "height": 32.0},
		"children": []any{},
	}
	// 5 same-named vectors → 1 slug after dedupe
	for i := 0; i < 5; i++ {
		frame["children"] = append(frame["children"].([]any), map[string]any{
			"id":          "v" + string(rune('0'+i)),
			"type":        "INSTANCE",
			"name":        "Vector",
			"componentId": "1:9",
			"absoluteBoundingBox": map[string]any{"x": float64(i), "y": 0.0, "width": 16.0, "height": 16.0},
		})
	}
	out := WalkOrganismCandidates(WrapForOrganismWalk(frame))
	if len(out) != 0 {
		t.Errorf("expected 0 candidates (atom set collapses to 1); got %d", len(out))
	}
}

// TestWalker_BelowThreshold — a FRAME with 1 INSTANCE descendant doesn't
// qualify regardless of atom uniqueness.
func TestWalker_BelowThreshold(t *testing.T) {
	frame := map[string]any{
		"id":   "tiny",
		"type": "FRAME",
		"absoluteBoundingBox": map[string]any{"x": 0.0, "y": 0.0, "width": 32.0, "height": 32.0},
		"children": []any{
			map[string]any{
				"id":          "i1",
				"type":        "INSTANCE",
				"name":        "Right Icon",
				"componentId": "228:6123",
				"absoluteBoundingBox": map[string]any{"x": 0.0, "y": 0.0, "width": 16.0, "height": 16.0},
			},
		},
	}
	out := WalkOrganismCandidates(WrapForOrganismWalk(frame))
	if len(out) != 0 {
		t.Errorf("expected 0 candidates from 1-INSTANCE frame; got %d", len(out))
	}
}

// TestWalker_VisibleFalseSkipped — frames marked visible:false are pruned
// (we don't want detection firing on invisible authored states).
func TestWalker_VisibleFalseSkipped(t *testing.T) {
	frame := map[string]any{
		"id":      "hidden-row",
		"name":    "List/Full width",
		"type":    "FRAME",
		"visible": false,
		"absoluteBoundingBox": map[string]any{"x": 0.0, "y": 0.0, "width": 343.0, "height": 56.0},
		"children": []any{
			map[string]any{"id": "a", "type": "INSTANCE", "name": "Left Icon/Default", "componentId": "229:4715",
				"absoluteBoundingBox": map[string]any{"x": 0.0, "y": 0.0, "width": 24.0, "height": 24.0}},
			map[string]any{"id": "b", "type": "INSTANCE", "name": "Right Text", "componentId": "228:5960",
				"absoluteBoundingBox": map[string]any{"x": 100.0, "y": 0.0, "width": 100.0, "height": 24.0}},
		},
	}
	out := WalkOrganismCandidates(WrapForOrganismWalk(frame))
	if len(out) != 0 {
		t.Errorf("expected hidden frame to be skipped; got %d candidates", len(out))
	}
}

// TestWalker_NameToAtomSlug — confirm the heuristic produces expected slugs.
func TestWalker_NameToAtomSlug(t *testing.T) {
	cases := map[string]string{
		"Left Icon/Default":          "left-icon-default",
		"Right Text":                 "right-text",
		"Icons/2D/Chevron right":     "icons-2d-chevron-right",
		"Icons/Logo/reliance":        "icons-logo-reliance",
		"Overline":                   "overline",
		"  Subtext  ":                "subtext",
		"Badges":                     "badges",
		"":                           "",
		"Vector 2776":                "vector-2776",
	}
	for in, want := range cases {
		got := nameToAtomSlug(in)
		if got != want {
			t.Errorf("nameToAtomSlug(%q) = %q; want %q", in, got, want)
		}
	}
}

// TestWalker_SlotKindFromName — confirm slot inference matches the TS-side
// patterns referenced by the plan.
func TestWalker_SlotKindFromName(t *testing.T) {
	cases := map[string]string{
		"Left Icon/Default":          "LEFT_ICON",
		"Right Icon":                 "RIGHT_ICON",
		"Left Text":                  "LEFT_TEXT",
		"Right Text":                 "RIGHT_TEXT",
		"Overline":                   "OVERLINE",
		"Subtext":                    "SUBTEXT",
		"Badges":                     "BADGE",
		"Separators":                 "SEPARATOR",
		"Icons/2D/Help":              "ICON",
		"Random Frame 1234":          "UNKNOWN",
		"Wallet card":                "UNKNOWN",
	}
	for in, want := range cases {
		got := slotKindFromName(in)
		if got != want {
			t.Errorf("slotKindFromName(%q) = %q; want %q", in, got, want)
		}
	}
}

// TestWalker_DS_ComponentSetSkipped — the published DS catalog file's
// COMPONENT_SET roots should NOT surface candidates. (Walker only emits
// from FRAME nodes.)
func TestWalker_DS_ComponentSetSkipped(t *testing.T) {
	for _, name := range []string{"ds-1", "ds-2"} {
		t.Run(name, func(t *testing.T) {
			root := loadFixture(t, name)
			t2, _ := root["type"].(string)
			if t2 != "COMPONENT_SET" {
				t.Skipf("fixture %s is not COMPONENT_SET (type=%q) — guard not applicable", name, t2)
			}
			out := WalkOrganismCandidates(WrapForOrganismWalk(root))
			// The root itself is COMPONENT_SET, so it should not be a
			// candidate. Its children are COMPONENTs (also not FRAMEs).
			// Any inner FRAMEs with ≥2 instance descendants would surface.
			for _, fp := range out {
				if fp.FrameID == root["id"] {
					t.Errorf("DS COMPONENT_SET root %q must not be emitted", fp.FrameID)
				}
			}
		})
	}
}

// TestWalker_AllWildFixtures — every wild-* fixture surfaces ≥ 1 candidate.
// This is the breadth test that catches regressions in the threshold or the
// recursion logic.
func TestWalker_AllWildFixtures(t *testing.T) {
	fixtures := []string{
		"wild-tax-1", "wild-tax-2", "wild-tax-3",
		"wild-dash-1", "wild-dash-2", "wild-dash-3", "wild-dash-4", "wild-dash-5",
		"wild-eq-1", "wild-eq-2",
		"wild-sav",
	}
	for _, name := range fixtures {
		t.Run(name, func(t *testing.T) {
			root := loadFixture(t, name)
			out := WalkOrganismCandidates(WrapForOrganismWalk(root))
			if len(out) == 0 {
				t.Errorf("fixture %s: expected ≥1 candidate; got 0", name)
				return
			}
			// At least one candidate should have a non-empty fingerprint hash
			// and a non-empty AtomSet.
			top := out[0]
			if top.Hash == "" {
				t.Errorf("fixture %s: top candidate Hash is empty", name)
			}
			if len(top.Hash) != fingerprintHashHexLen {
				t.Errorf("fixture %s: Hash len = %d; want %d", name, len(top.Hash), fingerprintHashHexLen)
			}
			if len(top.AtomSet) < organismMinAtomInstances {
				t.Errorf("fixture %s: AtomSet len = %d; want ≥ %d", name, len(top.AtomSet), organismMinAtomInstances)
			}
		})
	}
}
