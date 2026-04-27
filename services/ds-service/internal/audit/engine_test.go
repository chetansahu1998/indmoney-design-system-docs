package audit

import (
	"testing"
)

func TestAudit_DetectsScreenAndComputesCoverage(t *testing.T) {
	// Build a minimal Figma tree: page → final-design FRAME (screen) → 2 nodes.
	tree := map[string]any{
		"type": "DOCUMENT",
		"children": []any{
			map[string]any{
				"type": "CANVAS",
				"name": "Page 1",
				"children": []any{
					map[string]any{
						"id":   "1:1",
						"type": "FRAME",
						"name": "Trade Screen",
						"children": []any{
							// Bound fill — counted as bound.
							map[string]any{
								"id":   "1:2",
								"type": "RECTANGLE",
								"name": "bg",
								"fills": []any{map[string]any{
									"type":           "SOLID",
									"color":          map[string]any{"r": 0.06, "g": 0.07, "b": 0.09},
									"boundVariables": map[string]any{"color": map[string]any{"id": "var:1"}},
								}},
							},
							// Unbound fill close to a token — should produce a P2/P3 fix.
							map[string]any{
								"id":   "1:3",
								"type": "RECTANGLE",
								"name": "PriceLabel",
								"fills": []any{map[string]any{
									"type":  "SOLID",
									"color": map[string]any{"r": 0.42, "g": 0.45, "b": 0.5}, // ~#6B7280
								}},
							},
						},
					},
					// A WIP page that should be skipped.
					map[string]any{
						"id":   "9:9",
						"type": "FRAME",
						"name": "WIP — sketches",
					},
				},
			},
		},
	}

	tokens := []DSToken{
		{Path: "surface.surface-grey-separator-dark", Hex: "#6F7686", Kind: "color", FigmaName: "Surface Grey Separator Dark"},
		{Path: "text-1", Hex: "#0E1117", Kind: "color", FigmaName: "Text-1"},
	}

	res := Audit(tree, tokens, nil, Options{
		FileKey: "demo", FileName: "Demo", FileSlug: "demo", Brand: "indmoney",
	})

	if len(res.Screens) != 1 {
		t.Fatalf("got %d screens, want 1 (WIP page should be skipped)", len(res.Screens))
	}
	s := res.Screens[0]
	if s.Name != "Trade Screen" {
		t.Errorf("screen name = %q, want Trade Screen", s.Name)
	}
	if s.Coverage.Fills.Total != 2 {
		t.Errorf("fills total = %d, want 2", s.Coverage.Fills.Total)
	}
	if s.Coverage.Fills.Bound != 1 {
		t.Errorf("fills bound = %d, want 1", s.Coverage.Fills.Bound)
	}
	if len(s.Fixes) == 0 {
		t.Errorf("expected at least one fix candidate for the unbound fill")
	}
	// Find the fill fix
	hasFillFix := false
	for _, f := range s.Fixes {
		if f.Property == "fill" {
			hasFillFix = true
			if f.TokenPath == "" {
				t.Errorf("fill fix has no token_path: %+v", f)
			}
		}
	}
	if !hasFillFix {
		t.Errorf("no fill fix found in %d fixes", len(s.Fixes))
	}
	if res.OverallCoverage <= 0 || res.OverallCoverage > 1 {
		t.Errorf("overall coverage = %v, expected (0, 1]", res.OverallCoverage)
	}
}

func TestAudit_EmptyTree(t *testing.T) {
	res := Audit(nil, nil, nil, Options{FileSlug: "x"})
	if len(res.Screens) != 0 {
		t.Errorf("nil tree got %d screens, want 0", len(res.Screens))
	}
	if res.SchemaVersion != SchemaVersion {
		t.Errorf("schema version = %q", res.SchemaVersion)
	}
}

func TestBuildIndex_CrossFilePattern(t *testing.T) {
	// Two files share one canonical hash → emits a CrossFilePattern.
	r1 := AuditResult{FileSlug: "indstocks-v4", Screens: []AuditScreen{}}
	r2 := AuditResult{FileSlug: "indmoney-app", Screens: []AuditScreen{}}
	hashes := map[string][]HashedNode{
		"indstocks-v4": {{Hash: "sha256:abc", NodeID: "1:2"}, {Hash: "sha256:other", NodeID: "1:3"}},
		"indmoney-app": {{Hash: "sha256:abc", NodeID: "5:5"}},
	}
	idx := BuildIndex([]AuditResult{r1, r2}, hashes, "ds-rev-test")
	if len(idx.CrossFilePatterns) != 1 {
		t.Fatalf("expected 1 cross-file pattern, got %d: %+v", len(idx.CrossFilePatterns), idx.CrossFilePatterns)
	}
	p := idx.CrossFilePatterns[0]
	if p.CanonicalHash != "sha256:abc" {
		t.Errorf("hash = %q", p.CanonicalHash)
	}
	if len(p.Files) != 2 {
		t.Errorf("file count in pattern = %d", len(p.Files))
	}
}

func TestSlugify(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Trade Screen", "trade-screen"},
		{"WIP — sketches", "wip-sketches"},
		{"   ", ""},
	}
	for _, c := range cases {
		got := slugify(c.in)
		if c.want == "" {
			// Allow hash fallback.
			if got == "" {
				t.Errorf("slugify(%q) returned empty; want hash fallback", c.in)
			}
			continue
		}
		if got != c.want {
			t.Errorf("slugify(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
