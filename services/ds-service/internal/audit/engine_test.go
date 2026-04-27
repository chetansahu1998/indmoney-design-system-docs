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

func TestAudit_OffGridSpacingProducesDriftFix(t *testing.T) {
	// Auto-layout frame with itemSpacing=18 (off-grid; expected snap to 20).
	tree := map[string]any{
		"type": "DOCUMENT",
		"children": []any{
			map[string]any{
				"type": "CANVAS", "name": "Page 1",
				"children": []any{
					map[string]any{
						"id": "1:1", "type": "FRAME", "name": "Trade Screen",
						"layoutMode":  "VERTICAL",
						"itemSpacing": 18.0,
						"paddingLeft": 11.0, // off-grid → expect snap to 12
					},
				},
			},
		},
	}
	res := Audit(tree, nil, nil, Options{
		FileSlug: "demo", Brand: "indmoney",
	})
	if len(res.Screens) != 1 {
		t.Fatalf("got %d screens, want 1", len(res.Screens))
	}
	s := res.Screens[0]
	gridFixes := []FixCandidate{}
	for _, f := range s.Fixes {
		if f.Property == "spacing" || f.Property == "padding" {
			gridFixes = append(gridFixes, f)
		}
	}
	if len(gridFixes) < 2 {
		t.Fatalf("expected >=2 grid-drift fixes (one per off-grid value), got %d: %+v", len(gridFixes), gridFixes)
	}
	// Find the 18→20 fix
	saw18 := false
	saw11 := false
	for _, f := range gridFixes {
		if f.Observed == "18px" {
			saw18 = true
			if f.Rationale == "" || f.Reason != "drift" {
				t.Errorf("18→20 fix missing rationale or wrong reason: %+v", f)
			}
		}
		if f.Observed == "11px" {
			saw11 = true
		}
	}
	if !saw18 {
		t.Errorf("expected drift fix for 18px (snap → 20)")
	}
	if !saw11 {
		t.Errorf("expected drift fix for 11px (snap → 12)")
	}
	// On-grid value should NOT produce a drift fix.
	tree2 := map[string]any{
		"type": "DOCUMENT",
		"children": []any{
			map[string]any{
				"type": "CANVAS", "name": "Page 1",
				"children": []any{
					map[string]any{
						"id": "2:1", "type": "FRAME", "name": "Watchlist Screen",
						"layoutMode":  "VERTICAL",
						"itemSpacing": 16.0, // on-grid
					},
				},
			},
		},
	}
	res2 := Audit(tree2, nil, nil, Options{FileSlug: "demo2", Brand: "indmoney"})
	for _, f := range res2.Screens[0].Fixes {
		if f.Property == "spacing" {
			t.Errorf("on-grid 16px should not produce drift fix; got %+v", f)
		}
	}
	if res2.Screens[0].Coverage.Spacing.Bound != 1 || res2.Screens[0].Coverage.Spacing.Total != 1 {
		t.Errorf("on-grid 16px should be Bound=1 Total=1; got %+v", res2.Screens[0].Coverage.Spacing)
	}
}

func TestAudit_RadiusPillCountsAsBound(t *testing.T) {
	tree := map[string]any{
		"type": "DOCUMENT",
		"children": []any{
			map[string]any{
				"type": "CANVAS", "name": "Page 1",
				"children": []any{
					map[string]any{
						"id": "1:1", "type": "FRAME", "name": "Trade Screen",
						"children": []any{
							map[string]any{
								"id": "1:2", "type": "FRAME", "name": "Pill Button",
								"absoluteBoundingBox": map[string]any{"height": 40.0},
								"cornerRadius":        20.0,
							},
							map[string]any{
								"id": "1:3", "type": "FRAME", "name": "Off-grid card",
								"absoluteBoundingBox": map[string]any{"height": 100.0},
								"cornerRadius":        7.0, // off-grid → snap to 8
							},
						},
					},
				},
			},
		},
	}
	res := Audit(tree, nil, nil, Options{FileSlug: "demo", Brand: "indmoney"})
	if len(res.Screens) != 1 {
		t.Fatalf("got %d screens", len(res.Screens))
	}
	s := res.Screens[0]
	if s.Coverage.Radius.Total < 2 {
		t.Errorf("radius Total = %d; want >=2 (one pill + one off-grid)", s.Coverage.Radius.Total)
	}
	if s.Coverage.Radius.Bound < 1 {
		t.Errorf("radius Bound = %d; want >=1 (pill counts as bound)", s.Coverage.Radius.Bound)
	}
	saw7 := false
	for _, f := range s.Fixes {
		if f.Property == "radius" && f.Observed == "7px" {
			saw7 = true
			if f.TokenPath != "radius.8" {
				t.Errorf("7→8 fix TokenPath = %q; want radius.8", f.TokenPath)
			}
			if f.Rationale == "" {
				t.Errorf("7→8 fix missing rationale: %+v", f)
			}
		}
	}
	if !saw7 {
		t.Errorf("expected drift fix for 7px radius (snap → 8)")
	}
}

func TestAudit_BaseTokensNeverAppearAsFixCandidates(t *testing.T) {
	// Regression test for designer-reported bug: base tokens (no Figma
	// Variable identity, empty FigmaName) were appearing as fix
	// suggestions like "base.colour.grey.4a4f52" — the plugin can't bind
	// to those because they're not published variables, so applying
	// errored. Both FindClosestColor and the engine itself filter empty
	// FigmaName tokens out; this test asserts a base-only token set
	// produces only "unbound" fixes (token_path empty), never base
	// suggestions.
	tree := map[string]any{
		"type": "DOCUMENT",
		"children": []any{
			map[string]any{
				"type": "CANVAS", "name": "Page 1",
				"children": []any{
					map[string]any{
						"id": "1:1", "type": "FRAME", "name": "Trade Screen",
						"children": []any{
							map[string]any{
								"id": "1:2", "type": "RECTANGLE", "name": "card",
								"fills": []any{map[string]any{
									"type":  "SOLID",
									"color": map[string]any{"r": 0.29, "g": 0.31, "b": 0.32}, // ~#4a4f52
								}},
							},
						},
					},
				},
			},
		},
	}
	// Token set contains only base primitives — no FigmaName, no
	// published Variable identity.
	baseOnlyTokens := []DSToken{
		{Path: "base.colour.grey.4a4f52", Hex: "#4A4F52", Kind: "color"},
		{Path: "base.colour.neutral-dark.1a1d20", Hex: "#1A1D20", Kind: "color"},
	}
	res := Audit(tree, baseOnlyTokens, nil, Options{FileSlug: "demo"})
	if len(res.Screens) != 1 {
		t.Fatalf("got %d screens", len(res.Screens))
	}
	for _, f := range res.Screens[0].Fixes {
		if f.Property != "fill" {
			continue
		}
		if f.TokenPath != "" {
			t.Errorf(
				"base-only token set produced fix with TokenPath=%q (Reason=%q) — base tokens must never be suggested",
				f.TokenPath, f.Reason,
			)
		}
		if f.Reason != "unbound" {
			t.Errorf("expected Reason=unbound, got %q", f.Reason)
		}
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
