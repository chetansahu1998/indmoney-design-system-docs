package projects

import "testing"

// figma_page_classifier_test.go — U2 unit coverage.
// Anchored on real page names from the 145-file 6-month window discovery.

func TestClassifyPages_FinalVariants(t *testing.T) {
	// Every one of these was observed in the live corpus.
	finalNames := []string{
		"Final",
		"Final Design",
		"FInal Design",
		"Final designs",
		"🏁 Final Designs",
		"🏁 Final",
		"Final 🚀",
		"Trader FINAL DESIGN",
		"Investor FINAL DESIGN",
		"Stories FINAL DESIGN",
		"Shareability FINAL DESIGN",
		"Final Design (Flutter)",
		"Final Design'24",
		"Final design",
		"finals",
		"dollar rupee final",
		"Insta Cash- Final",
		"Final Designs - Mobile/mWeb",
		"Final Designs dWeb",
		"Final Flow & Structure",
	}
	rows := make([]FigmaPageInput, len(finalNames))
	for i, n := range finalNames {
		rows[i] = FigmaPageInput{PageID: "p" + n, Name: n}
	}
	got := ClassifyPages(rows, nil)
	for i, g := range got {
		if g.Classification != PageClassFinal {
			t.Errorf("name=%q: got %s, want final", finalNames[i], g.Classification)
		}
	}
}

func TestClassifyPages_VersionVariants(t *testing.T) {
	cases := []struct {
		name     string
		wantBase string
		wantN    int
	}{
		{"V1", "", 1},
		{"V2", "", 2},
		{"v3", "", 3},
		{"Onboarding v1", "Onboarding", 1},
		{"Onboarding v2", "Onboarding", 2},
		{"Mini App v4 (Q2, 2023)", "Mini App", 4},
		{"Mini APP V3 (Q1, 2023)", "Mini APP", 3},
		{"Mini Save V2", "Mini Save", 2},
		{"SBM Flow v3", "SBM Flow", 3},
		{"US stocks V2", "US stocks", 2},
		{"Refer & Earn Credit Card V2", "Refer & Earn Credit Card", 2},
		{"Equity Tracking V2", "Equity Tracking", 2},
		{"appstore new v4", "appstore new", 4},
		{"v4 Q2 2023", "", 4},
		{"Homepage V2 Q2' 2023", "Homepage", 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyPages([]FigmaPageInput{{PageID: "p", Name: tc.name}}, nil)
			if got[0].Classification != PageClassVersion {
				t.Fatalf("got %s, want version", got[0].Classification)
			}
			if got[0].VersionBase != tc.wantBase {
				t.Errorf("base: got %q want %q", got[0].VersionBase, tc.wantBase)
			}
			if got[0].VersionN != tc.wantN {
				t.Errorf("n: got %d want %d", got[0].VersionN, tc.wantN)
			}
		})
	}
}

func TestClassifyPages_NoiseVariants(t *testing.T) {
	noiseNames := []string{
		"Cover",
		"Cover Page",
		"📔 Cover",
		"Dump",
		"WIP",
		"WIP Designs",
		"wip designs",
		"WIP - 2025",
		"Iterations",
		"🗄️ archive",
		"Old reference",
		"Old flow",
		"Draft",
		"Don't open",
		"😕 exploration",
		"😕 Design Exploration",
		"Backup old",
		"Current design",
	}
	rows := make([]FigmaPageInput, len(noiseNames))
	for i, n := range noiseNames {
		rows[i] = FigmaPageInput{PageID: "p" + n, Name: n}
	}
	got := ClassifyPages(rows, nil)
	for i, g := range got {
		if g.Classification != PageClassNoise {
			t.Errorf("name=%q: got %s, want noise", noiseNames[i], g.Classification)
		}
	}
}

func TestClassifyPages_UnknownVariants(t *testing.T) {
	// Pages we don't auto-classify — neither Final nor version nor noise.
	unknownNames := []string{
		"Twitter",
		"Insta withdrawal",
		"facebook carousel",
		"Goals linked investments",
		"Federal Bank",
		"Backup old reference",
	}
	for _, n := range unknownNames {
		got := ClassifyPages([]FigmaPageInput{{PageID: "p", Name: n}}, nil)
		// "Backup old reference" matches noise via "\bold\b" — fine.
		// The point is no false positives for Final or Version on these.
		if got[0].Classification == PageClassFinal || got[0].Classification == PageClassVersion {
			t.Errorf("name=%q: misclassified as %s", n, got[0].Classification)
		}
	}
}

func TestClassifyPages_PersonaDerivation(t *testing.T) {
	cases := []struct {
		name    string
		persona string
	}{
		{"Trader FINAL DESIGN", "trader"},
		{"Investor FINAL DESIGN", "investor"},
		{"Stories FINAL DESIGN", "stories"},
		{"Shareability FINAL DESIGN", "shareability"},
		{"Dasbboard FINAL DESIGN", "dasbboard"},
		{"Final Design (Flutter)", "flutter"},
		{"Final Design", "default"},
		{"Final", "default"},
		{"🏁 Final Designs", "default"},
		{"finals", "default"},
		{"Insta Cash- Final", "insta cash"},
		{"dollar rupee final", "dollar rupee"},
		// Mobile/mWeb variant — the downstream parser handles the slash;
		// classifier just emits the platform string lowercased.
		{"Final Designs - Mobile/mWeb", "mobile/mweb"},
		{"Final Designs dWeb", "dweb"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyPages([]FigmaPageInput{{PageID: "p", Name: tc.name}}, nil)
			if got[0].Classification != PageClassFinal {
				t.Fatalf("must classify as final, got %s", got[0].Classification)
			}
			if got[0].PersonaHint != tc.persona {
				t.Errorf("persona: got %q want %q", got[0].PersonaHint, tc.persona)
			}
		})
	}
}

func TestPickSourcePages_FinalWins(t *testing.T) {
	pages := []ClassifiedPage{
		{PageID: "p1", Name: "Cover", Classification: PageClassNoise},
		{PageID: "p2", Name: "Final", Classification: PageClassFinal, PersonaHint: "default"},
		{PageID: "p3", Name: "Onboarding v2", Classification: PageClassVersion, VersionBase: "Onboarding", VersionN: 2},
		{PageID: "p4", Name: "Onboarding v1", Classification: PageClassVersion, VersionBase: "Onboarding", VersionN: 1},
	}
	got := PickSourcePages(pages)
	if len(got) != 1 || got[0].PageID != "p2" {
		t.Fatalf("got %+v, want one final page p2", got)
	}
}

func TestPickSourcePages_MultiFinal(t *testing.T) {
	// Design Summit 2023-style: 5 final pages by persona, all surface.
	pages := []ClassifiedPage{
		{PageID: "p1", Name: "Trader FINAL DESIGN", Classification: PageClassFinal, PersonaHint: "trader"},
		{PageID: "p2", Name: "Investor FINAL DESIGN", Classification: PageClassFinal, PersonaHint: "investor"},
		{PageID: "p3", Name: "Stories FINAL DESIGN", Classification: PageClassFinal, PersonaHint: "stories"},
		{PageID: "p4", Name: "WIP", Classification: PageClassNoise},
	}
	got := PickSourcePages(pages)
	if len(got) != 3 {
		t.Fatalf("expected 3 final pages, got %d: %+v", len(got), got)
	}
}

func TestPickSourcePages_VersionGrouping(t *testing.T) {
	// Two base groups: Onboarding (pick v2) + SBM Flow (pick v3).
	// Mini App has 4 versions all on one base; pick v4.
	pages := []ClassifiedPage{
		{PageID: "p1", Name: "Onboarding v1", Classification: PageClassVersion, VersionBase: "Onboarding", VersionN: 1},
		{PageID: "p2", Name: "Onboarding v2", Classification: PageClassVersion, VersionBase: "Onboarding", VersionN: 2},
		{PageID: "p3", Name: "SBM Flow v3", Classification: PageClassVersion, VersionBase: "SBM Flow", VersionN: 3},
		{PageID: "p4", Name: "Mini App v1", Classification: PageClassVersion, VersionBase: "Mini App", VersionN: 1},
		{PageID: "p5", Name: "Mini App v2", Classification: PageClassVersion, VersionBase: "Mini App", VersionN: 2},
		{PageID: "p6", Name: "Mini App v3", Classification: PageClassVersion, VersionBase: "Mini App", VersionN: 3},
		{PageID: "p7", Name: "Mini App v4", Classification: PageClassVersion, VersionBase: "Mini App", VersionN: 4},
	}
	got := PickSourcePages(pages)
	if len(got) != 3 {
		t.Fatalf("expected 3 winners, got %d: %+v", len(got), got)
	}
	// Validate winners by base.
	winners := map[string]int{}
	for _, p := range got {
		winners[p.VersionBase] = p.VersionN
	}
	if winners["Onboarding"] != 2 {
		t.Errorf("Onboarding winner: got %d want 2", winners["Onboarding"])
	}
	if winners["SBM Flow"] != 3 {
		t.Errorf("SBM Flow winner: got %d want 3", winners["SBM Flow"])
	}
	if winners["Mini App"] != 4 {
		t.Errorf("Mini App winner: got %d want 4", winners["Mini App"])
	}
}

func TestPickSourcePages_EmptyWhenNeitherFinalNorVersion(t *testing.T) {
	pages := []ClassifiedPage{
		{PageID: "p1", Name: "Cover", Classification: PageClassNoise},
		{PageID: "p2", Name: "Twitter", Classification: PageClassUnknown},
	}
	got := PickSourcePages(pages)
	if len(got) != 0 {
		t.Fatalf("expected zero, got %d: %+v", len(got), got)
	}
}

func TestClassifyPages_Rule_OverridesDefault(t *testing.T) {
	// Forward-compat: an admin rule classifying "Production" as final
	// makes it pick up despite no default match.
	pages := []FigmaPageInput{{PageID: "p1", Name: "Production"}}
	rules := []PagePickerRule{
		{MatchKind: "exact", MatchPattern: "Production", Classification: PageClassFinal, Priority: 10},
	}
	got := ClassifyPages(pages, rules)
	if got[0].Classification != PageClassFinal {
		t.Errorf("rule override failed: got %s", got[0].Classification)
	}
}

func TestClassifyPages_EmptyInputReturnsEmpty(t *testing.T) {
	got := ClassifyPages(nil, nil)
	if len(got) != 0 {
		t.Errorf("expected empty, got %+v", got)
	}
}
