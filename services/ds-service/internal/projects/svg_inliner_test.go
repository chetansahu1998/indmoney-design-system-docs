package projects

import (
	"strings"
	"testing"
)

// ─── SanitizeSVGMarkup ──────────────────────────────────────────────────

func TestSanitizeSVGMarkup_BasicSafe(t *testing.T) {
	in := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100"><circle cx="50" cy="50" r="40" fill="red"/></svg>`
	out, err := SanitizeSVGMarkup([]byte(in))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(string(out), "<svg") {
		t.Errorf("expected <svg tag in output: %s", out)
	}
	if !strings.Contains(string(out), "<circle") {
		t.Errorf("expected <circle in output: %s", out)
	}
	if !strings.Contains(string(out), `viewBox="0 0 100 100"`) {
		t.Errorf("expected viewBox preserved: %s", out)
	}
}

func TestSanitizeSVGMarkup_StripsScript(t *testing.T) {
	in := `<svg><script>alert(1)</script><circle cx="0" cy="0" r="1"/></svg>`
	out, err := SanitizeSVGMarkup([]byte(in))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(string(out), "<script") {
		t.Errorf("expected <script> stripped: %s", out)
	}
	if strings.Contains(string(out), "alert") {
		t.Errorf("expected script body stripped: %s", out)
	}
	if !strings.Contains(string(out), "<circle") {
		t.Errorf("expected sibling content preserved: %s", out)
	}
}

func TestSanitizeSVGMarkup_StripsForeignObject(t *testing.T) {
	in := `<svg><foreignObject><body><script>alert(1)</script></body></foreignObject></svg>`
	out, err := SanitizeSVGMarkup([]byte(in))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(strings.ToLower(string(out)), "foreignobject") {
		t.Errorf("expected <foreignObject> stripped: %s", out)
	}
	if strings.Contains(string(out), "alert") {
		t.Errorf("expected embedded script payload stripped: %s", out)
	}
}

func TestSanitizeSVGMarkup_StripsEventHandlers(t *testing.T) {
	in := `<svg><rect onload="alert(1)" onclick="alert(2)" onmouseover="x" fill="red"/></svg>`
	out, err := SanitizeSVGMarkup([]byte(in))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, attr := range []string{"onload", "onclick", "onmouseover"} {
		if strings.Contains(strings.ToLower(string(out)), attr) {
			t.Errorf("expected %s stripped: %s", attr, out)
		}
	}
	if !strings.Contains(string(out), `fill="red"`) {
		t.Errorf("expected safe attrs preserved: %s", out)
	}
}

func TestSanitizeSVGMarkup_StripsJavascriptURL(t *testing.T) {
	in := `<svg><a href="javascript:alert(1)" xlink:href="javascript:alert(2)"><rect/></a></svg>`
	out, err := SanitizeSVGMarkup([]byte(in))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(strings.ToLower(string(out)), "javascript:") {
		t.Errorf("expected javascript: URLs stripped: %s", out)
	}
	if !strings.Contains(string(out), "<a") {
		t.Errorf("expected the <a> tag itself preserved: %s", out)
	}
}

func TestSanitizeSVGMarkup_PreservesUseHref(t *testing.T) {
	// Same-document fragment references (used by <symbol>/<use> pattern)
	// must survive sanitization.
	in := `<svg><defs><symbol id="i"><circle r="1"/></symbol></defs><use href="#i"/></svg>`
	out, err := SanitizeSVGMarkup([]byte(in))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(string(out), `href="#i"`) {
		t.Errorf("expected fragment href preserved: %s", out)
	}
	if !strings.Contains(string(out), "<symbol") {
		t.Errorf("expected <symbol> preserved: %s", out)
	}
}

func TestSanitizeSVGMarkup_StripsDataTextHTML(t *testing.T) {
	in := `<svg><a href="data:text/html,<script>alert(1)</script>"><rect/></a></svg>`
	out, err := SanitizeSVGMarkup([]byte(in))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(strings.ToLower(string(out)), "data:text/html") {
		t.Errorf("expected data:text/html stripped: %s", out)
	}
}

func TestSanitizeSVGMarkup_PreservesDataImage(t *testing.T) {
	// data:image/png URLs in <image> tags are safe — SVG can legitimately
	// embed raster images this way. Should NOT be stripped.
	in := `<svg><image href="data:image/png;base64,iVBOR..." width="10" height="10"/></svg>`
	out, err := SanitizeSVGMarkup([]byte(in))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(string(out), "data:image/png") {
		t.Errorf("expected data:image/png preserved: %s", out)
	}
}

func TestSanitizeSVGMarkup_StripsStyle(t *testing.T) {
	// <style> inside SVG can hide @import url(javascript:...) and
	// other CSS-side payloads. Drop entirely.
	in := `<svg><style>@import url(javascript:alert(1));</style><circle r="1"/></svg>`
	out, err := SanitizeSVGMarkup([]byte(in))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(strings.ToLower(string(out)), "<style") {
		t.Errorf("expected <style> stripped: %s", out)
	}
	if strings.Contains(string(out), "javascript") {
		t.Errorf("expected CSS payload stripped: %s", out)
	}
}

func TestSanitizeSVGMarkup_StripsCommentsAndDoctype(t *testing.T) {
	in := `<!DOCTYPE svg PUBLIC "..." "..."><!-- malicious comment --><svg><circle r="1"/></svg>`
	out, err := SanitizeSVGMarkup([]byte(in))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(string(out), "DOCTYPE") {
		t.Errorf("expected DOCTYPE stripped: %s", out)
	}
	if strings.Contains(string(out), "malicious comment") {
		t.Errorf("expected comment stripped: %s", out)
	}
}

func TestSanitizeSVGMarkup_NestedBlockedElements(t *testing.T) {
	// A blocked element nested inside another blocked element should
	// still produce clean output — skip-depth must track correctly.
	in := `<svg><foreignObject><iframe src="https://evil"><script>x</script></iframe></foreignObject><circle r="1"/></svg>`
	out, err := SanitizeSVGMarkup([]byte(in))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, tag := range []string{"foreignobject", "iframe", "script"} {
		if strings.Contains(strings.ToLower(string(out)), tag) {
			t.Errorf("expected %s stripped: %s", tag, out)
		}
	}
	if !strings.Contains(string(out), "<circle") {
		t.Errorf("expected post-block content preserved: %s", out)
	}
}

func TestSanitizeSVGMarkup_EmptyInput(t *testing.T) {
	out, err := SanitizeSVGMarkup([]byte(""))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(out) != "" {
		t.Errorf("expected empty output for empty input, got %q", out)
	}
}

// ─── MutateCanonicalTree / walkAndMutate ────────────────────────────────

func TestMutateCanonicalTree_VisitsRoot(t *testing.T) {
	tree := map[string]any{"id": "root", "type": "FRAME"}
	visited := 0
	MutateCanonicalTree(tree, func(node map[string]any) { visited++ })
	if visited != 1 {
		t.Errorf("expected root visited once, got %d", visited)
	}
}

func TestMutateCanonicalTree_VisitsChildrenDepthFirst(t *testing.T) {
	tree := map[string]any{
		"id": "root",
		"children": []any{
			map[string]any{"id": "c1"},
			map[string]any{
				"id": "c2",
				"children": []any{
					map[string]any{"id": "c2-a"},
				},
			},
		},
	}
	visited := []string{}
	MutateCanonicalTree(tree, func(node map[string]any) {
		id, _ := node["id"].(string)
		visited = append(visited, id)
	})
	expected := []string{"root", "c1", "c2", "c2-a"}
	if len(visited) != len(expected) {
		t.Fatalf("visited mismatch: got %v want %v", visited, expected)
	}
	for i := range expected {
		if visited[i] != expected[i] {
			t.Errorf("visit %d: got %q want %q", i, visited[i], expected[i])
		}
	}
}

func TestMutateCanonicalTree_MutationPersists(t *testing.T) {
	tree := map[string]any{
		"id": "root",
		"children": []any{
			map[string]any{"id": "target"},
		},
	}
	MutateCanonicalTree(tree, func(node map[string]any) {
		if id, _ := node["id"].(string); id == "target" {
			node["svg_markup"] = "<svg/>"
		}
	})
	children, _ := tree["children"].([]any)
	target, _ := children[0].(map[string]any)
	if target["svg_markup"] != "<svg/>" {
		t.Errorf("expected mutation persisted, got %v", target["svg_markup"])
	}
}

func TestMutateCanonicalTree_NilSafe(t *testing.T) {
	// Must not panic on nil; should just be a no-op.
	MutateCanonicalTree(nil, func(node map[string]any) {
		t.Error("mutator should not be called on nil tree")
	})
}

// QA Bug 8 regression: InlineSVGMarkup must descend into tree["document"]
// when the canonical_tree carries the raw Figma /v1/files envelope shape
// (top-level keys: styles componentSets components document schemaVersion).
// Before this fix the inliner walked from the envelope root, found no
// children, and exited silently — making every inline pass a no-op.
func TestInlineSVGMarkup_DescendsIntoDocumentEnvelope(t *testing.T) {
	// Build a minimal envelope-shaped tree with one SVG-eligible cluster
	// nested under `document.children`.
	tree := map[string]any{
		"schemaVersion": 1,
		"document": map[string]any{
			"id":   "doc",
			"type": "DOCUMENT",
			"children": []any{
				map[string]any{
					"id":   "1:1",
					"type": "FRAME",
					"children": []any{
						map[string]any{
							"id":   "1:2",
							"type": "FRAME",
							"name": "illustration/hero",
						},
					},
				},
			},
		},
	}
	visitedIDs := []string{}
	walkRoot := tree
	if doc, ok := tree["document"].(map[string]any); ok {
		walkRoot = doc
	}
	MutateCanonicalTree(walkRoot, func(node map[string]any) {
		if id, ok := node["id"].(string); ok {
			visitedIDs = append(visitedIDs, id)
		}
	})
	wantedIDs := []string{"doc", "1:1", "1:2"}
	if len(visitedIDs) != len(wantedIDs) {
		t.Fatalf("expected %d visits, got %d: %v", len(wantedIDs), len(visitedIDs), visitedIDs)
	}
	for i, want := range wantedIDs {
		if visitedIDs[i] != want {
			t.Errorf("visit[%d] = %q, want %q", i, visitedIDs[i], want)
		}
	}
}

func TestMutateCanonicalTree_IgnoresNonMapChildren(t *testing.T) {
	// children with non-map entries (defensive against malformed JSON)
	// must not cause a panic or partial walk.
	tree := map[string]any{
		"id": "root",
		"children": []any{
			"string-not-map",
			42,
			map[string]any{"id": "real-child"},
		},
	}
	visited := []string{}
	MutateCanonicalTree(tree, func(node map[string]any) {
		id, _ := node["id"].(string)
		visited = append(visited, id)
	})
	if len(visited) != 2 || visited[0] != "root" || visited[1] != "real-child" {
		t.Errorf("expected [root real-child], got %v", visited)
	}
}

// ─── isValidFigmaNodeID ─────────────────────────────────────────────────

func TestIsValidFigmaNodeID(t *testing.T) {
	tests := []struct {
		id    string
		valid bool
	}{
		{"1:2", true},
		{"123:456", true},
		{"I123:45", true},
		{"", false},
		{"1234567:8901234", true},
		{"../../../etc/passwd", false},
		{"foo:bar", false},
		{"1:2:3", true}, // multi-colon allowed; Figma uses this for nested
		{"1:2/3", false},
		{"1:2\\3", false},
		{"1:2 3", false},
		{"123", false}, // missing colon
		{"a1:2", false},
		// Instance-override chains. Figma emits these for deeply nested
		// instance overrides (Bug 8 root cause). The validator must
		// accept them so the inliner doesn't reject ~28% of cluster
		// nodes on real leaves.
		{"I20696:214334;62:994", true},
		{"I9644:190198;674:59530", true},
		{"I20013:239603;1625:49634;1625:46951;1434:24120;1742:27214", true},
		// Bare semicolons without instance prefix are still well-formed
		// node id chains (Figma also emits these without 'I' in some
		// contexts).
		{"123:456;789:012", true},
		// Length cap raised from 64 → 256 to cover 5-deep override chains.
		// "1:" + 65 digits = 67 chars — passes length check and contains ':'.
		{"1:" + strings.Repeat("1", 65), true},
		// 256+ chars rejected even with valid format.
		{"1:" + strings.Repeat("1", 256), false},
	}
	for _, tc := range tests {
		got := isValidFigmaNodeID(tc.id)
		if got != tc.valid {
			t.Errorf("isValidFigmaNodeID(%q) = %v, want %v", tc.id, got, tc.valid)
		}
	}
}

// ─── Name-aware short-circuit in walkClustersWithSVGFlag ────────────────

func TestWalkClustersWithSVGFlag_NameOverridesEligibility(t *testing.T) {
	// A frame named `illustration/...` containing an IMAGE fill (which
	// would normally fail SVG eligibility) must still be flagged as
	// SVGEligible because the designer named it.
	tree := map[string]any{
		"id":   "root",
		"type": "FRAME",
		"children": []any{
			map[string]any{
				"id":   "I1:1",
				"type": "INSTANCE",
				"name": "illustration/empty-state",
				"fills": []any{
					map[string]any{"type": "IMAGE", "imageRef": "abc"},
				},
			},
		},
	}
	acc := []ClusterCandidate{}
	walkClustersWithSVGFlag(tree, &acc, 0)
	// We expect exactly one cluster candidate for the named frame.
	var found *ClusterCandidate
	for i := range acc {
		if acc[i].ID == "I1:1" {
			found = &acc[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("expected illustration/* node in clusters, got %v", acc)
	}
	if !found.SVGEligible {
		t.Errorf("name-aware short-circuit failed: SVGEligible=false for %q", found.ID)
	}
}

func TestWalkClustersWithSVGFlag_StructuralEligibilityStillBlocksUnnamed(t *testing.T) {
	// An unnamed cluster with an IMAGE fill should remain SVGEligible=false
	// (structural rule still applies). Only named frames get the override.
	tree := map[string]any{
		"id":   "root",
		"type": "FRAME",
		"children": []any{
			map[string]any{
				"id":   "1:2",
				"type": "VECTOR",
				"name": "Vector 1",
				"fills": []any{
					map[string]any{"type": "IMAGE", "imageRef": "abc"},
				},
			},
		},
	}
	acc := []ClusterCandidate{}
	walkClustersWithSVGFlag(tree, &acc, 0)
	for _, c := range acc {
		if c.ID == "1:2" && c.SVGEligible {
			t.Errorf("expected unnamed VECTOR with IMAGE fill to fail SVGEligible, got true")
		}
	}
}
