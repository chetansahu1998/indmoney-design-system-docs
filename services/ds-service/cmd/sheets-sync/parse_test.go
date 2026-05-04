package main

import "testing"

func TestParseFigmaURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
		kind URLKind
		fid  string
		nid  string
	}{
		{
			"valid /design with node-id",
			"https://www.figma.com/design/Ql47G1l4xLYOW7V2MkK0Qx/Project-Name?node-id=12940-595737&t=ABC",
			URLValid, "Ql47G1l4xLYOW7V2MkK0Qx", "12940:595737",
		},
		{
			"valid /file with node-id (legacy)",
			"https://www.figma.com/file/Abc123/Foo?node-id=1-2",
			URLValid, "Abc123", "1:2",
		},
		{
			"valid /proto",
			"https://www.figma.com/proto/Xyz/Foo?node-id=99-100&starting-point-node-id=99-100",
			URLValid, "Xyz", "99:100",
		},
		{
			"no node-id (canvas-only)",
			"https://www.figma.com/design/Ql47.../Project-Name",
			URLCanvasOnly, "Ql47", "",
		},
		{
			"empty",
			"",
			URLEmpty, "", "",
		},
		{
			"whitespace-only",
			"   \t  ",
			URLEmpty, "", "",
		},
		{
			"malformed (not figma)",
			"https://example.com/foo",
			URLMalformed, "", "",
		},
		{
			"malformed (no host segment)",
			"random text",
			URLMalformed, "", "",
		},
		{
			"node-id with longer hyphen segments",
			"https://www.figma.com/design/X/Y?node-id=1-2-3",
			URLValid, "X", "1:2-3",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k, fid, nid := ParseFigmaURL(tt.url)
			if k != tt.kind {
				t.Errorf("kind = %q, want %q", k, tt.kind)
			}
			if fid != tt.fid {
				t.Errorf("fileID = %q, want %q", fid, tt.fid)
			}
			if nid != tt.nid {
				t.Errorf("nodeID = %q, want %q", nid, tt.nid)
			}
		})
	}
}

func TestSubSheetToProduct(t *testing.T) {
	cases := []struct {
		tab     string
		skip    bool
		lobe    string
		product string
		source  string
	}{
		// Markets
		{"INDstocks", false, "markets", "INDstocks", "explicit"},
		{"INDstock", false, "markets", "INDstocks", "explicit"},     // singular
		{"Mutual Funds", false, "markets", "Mutual Funds", "explicit"},
		{"US Stocks", false, "markets", "US Stocks", "explicit"},
		{"F&O", false, "markets", "F&O", "explicit"},
		{"Fixed Deposit", false, "markets", "Fixed Deposit", "explicit"},

		// Recurring Payments
		{"BBPS &TPAP", false, "recurring_payments", "BBPS & TPAP", "explicit"},
		{"BBPS & TPAP", false, "recurring_payments", "BBPS & TPAP", "explicit"},
		{"Credit Card", false, "recurring_payments", "Credit Card", "explicit"},

		// Lending
		{"Insta Cash", false, "lending", "Insta Cash", "explicit"},
		{"Insta Plus", false, "lending", "Insta Plus", "explicit"},
		{"NBFC", false, "lending", "NBFC", "explicit"},

		// Money Matters
		{"Plutus", false, "money_matters", "Plutus", "explicit"},
		{"Neobanking", false, "money_matters", "Neobanking", "explicit"},
		{"Insurance", false, "money_matters", "Insurance", "explicit"},
		{"Goals", false, "money_matters", "Goals", "explicit"},

		// Platform — including the sheet's actual typos
		{"Onbording KYC", false, "platform", "Onboarding KYC", "explicit"},  // sheet has this typo
		{"Onboarding KYC", false, "platform", "Onboarding KYC", "explicit"},
		{"Platfrom & global serach", false, "platform", "Platform & Global Search", "explicit"}, // 2 typos
		{"Referral", false, "platform", "Referral", "explicit"},
		{"Chatbot", false, "platform", "Chatbot", "explicit"},
		{"Social project", false, "platform", "Social Project", "explicit"},
		{"INDlearn", false, "platform", "INDlearn", "explicit"},

		// Web Platform
		{"INDWeb", false, "web_platform", "INDWeb", "explicit"},

		// Skip set
		{"Product Design", true, "platform", "Product Design", "skip"},
		{"Lotties", true, "platform", "Lotties", "skip"},
		{"Illustrations", true, "platform", "Illustrations", "skip"},

		// Default fallback for unknown tabs
		{"Made-up Tab", false, "platform", "Made-up Tab", "default"},
		{"", false, "platform", "", "default"},
	}
	for _, tc := range cases {
		t.Run(tc.tab, func(t *testing.T) {
			got := SubSheetToProduct(tc.tab)
			if got.Skip != tc.skip {
				t.Errorf("Skip = %v, want %v", got.Skip, tc.skip)
			}
			if got.Lobe != tc.lobe {
				t.Errorf("Lobe = %q, want %q", got.Lobe, tc.lobe)
			}
			if got.Product != tc.product {
				t.Errorf("Product = %q, want %q", got.Product, tc.product)
			}
			if got.Source != tc.source {
				t.Errorf("Source = %q, want %q", got.Source, tc.source)
			}
		})
	}
}

func TestNormalizeStatus(t *testing.T) {
	cases := map[string]string{
		"":             "",
		"Done":         "done",
		"done":         "done",
		"Complete":     "done",
		"complete":     "done",
		"WIP":          "wip",
		"wip":          "wip",
		"In dev":       "wip",
		"in progress":  "wip",
		"In progress":  "wip",
		"In review":    "in_review",
		"in review":    "in_review",
		"tbd":          "tbd",
		"TBD":          "tbd",
		"To be picked": "tbd",
		"Backlog":      "backlog",
		// Unknown values pass through (lowercased)
		"Custom-status": "custom-status",
	}
	for in, want := range cases {
		if got := NormalizeStatus(in); got != want {
			t.Errorf("NormalizeStatus(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestClassifyDRD(t *testing.T) {
	cases := []struct {
		raw    string
		kind   DRDKind
		gdocID string
	}{
		{"", DRDEmpty, ""},
		{"   ", DRDEmpty, ""},
		{"No DRD", DRDNoneMarker, ""},
		{"No DRD Shared", DRDNoneMarker, ""},
		{"n/a", DRDNoneMarker, ""},
		{"-", DRDNoneMarker, ""},
		{"plain inline text", DRDEmpty, ""},

		{"https://finzoom.atlassian.net/wiki/x/WYBqpQ", DRDConfluence, ""},
		{"https://confluence.example.com/foo", DRDConfluence, ""},

		{
			"https://docs.google.com/document/d/1l8ATtwqabMkVcamtm7uh3VVDZDN-vFSpHOy7ht7H-p8/edit",
			DRDGoogleDoc, "1l8ATtwqabMkVcamtm7uh3VVDZDN-vFSpHOy7ht7H-p8",
		},
		{
			"https://docs.google.com/document/u/0/d/1ABC/edit",
			DRDGoogleDoc, "1ABC",
		},
		{
			"https://www.notion.so/some-page-12345",
			DRDNotion, "",
		},
		{
			"https://example.com/random",
			DRDOtherURL, "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			k, id := ClassifyDRD(tc.raw)
			if k != tc.kind {
				t.Errorf("kind = %q, want %q", k, tc.kind)
			}
			if id != tc.gdocID {
				t.Errorf("gdocID = %q, want %q", id, tc.gdocID)
			}
		})
	}
}
