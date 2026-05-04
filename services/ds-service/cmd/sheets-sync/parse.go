package main

import (
	"regexp"
	"strings"
)

// parse.go — pure functions: Figma URL parser + sub-sheet → product
// resolver. Go mirror of lib/atlas/taxonomy.ts (subSheetToLobe +
// LEGACY_PRODUCT_PATTERNS). Keeping them in lockstep is a maintenance
// concern — see U4 plan unit + parse_test.go for the parity test.

// ─── Figma URL parsing ──────────────────────────────────────────────────────

// figmaURLRe captures file_id (group 1) and node-id (group 2 — optional).
// Handles all three Figma URL forms: /file/, /design/, /proto/.
//
// Examples:
//   https://www.figma.com/design/Ql47.../INDmoney?node-id=12940-595737  → file=Ql47..., node=12940-595737
//   https://www.figma.com/file/Abc/Foo                                  → file=Abc, node=""
//   https://www.figma.com/design/X/Y?node-id=1-2&t=ABC                  → file=X, node=1-2
var figmaURLRe = regexp.MustCompile(
	`figma\.com/(?:file|design|proto)/([A-Za-z0-9]+)` +
		`(?:/[^?]*)?` +
		`(?:\?[^#]*?node-id=([0-9A-Za-z\-_]+))?`)

// URLKind classifies a Figma URL the sync sees in the sheet.
type URLKind string

const (
	URLValid      URLKind = "valid"       // file_id + node_id present → fetch frames
	URLCanvasOnly URLKind = "canvas-only" // file_id only → skip per R3 rule
	URLEmpty      URLKind = "empty"       // empty cell → ghost flow
	URLMalformed  URLKind = "malformed"   // didn't match figma.com regex → log + skip
)

// ParseFigmaURL extracts file_id and node_id from a Figma URL string.
// Returns:
//   - kind: classification (see URLKind constants)
//   - fileID: empty for URLEmpty / URLMalformed
//   - nodeID: empty unless URLValid; format converted from URL form (1-2)
//             to API form (1:2) since Figma's REST API expects colons
func ParseFigmaURL(url string) (kind URLKind, fileID, nodeID string) {
	url = strings.TrimSpace(url)
	if url == "" {
		return URLEmpty, "", ""
	}
	m := figmaURLRe.FindStringSubmatch(url)
	if m == nil {
		return URLMalformed, "", ""
	}
	fileID = m[1]
	rawNode := m[2]
	if rawNode == "" {
		return URLCanvasOnly, fileID, ""
	}
	// Convert "12940-595737" → "12940:595737" (API form).
	// Only the FIRST hyphen is the page/node separator; subsequent
	// hyphens (rare but possible in node IDs) stay as-is.
	nodeID = strings.Replace(rawNode, "-", ":", 1)
	return URLValid, fileID, nodeID
}

// ─── Sub-sheet → product/lobe mapping ───────────────────────────────────────

// SubSheetMapping is the result of resolving a sheet tab name.
type SubSheetMapping struct {
	Skip    bool   // true → don't sync this tab at all (Product Design / Lotties / Illustrations)
	Lobe    string // markets / money_matters / lending / recurring_payments / platform / web_platform
	Product string // canonical product name shown on the brain
	Source  string // how we resolved: "explicit", "default", "skip"
}

// SubSheetToProduct mirrors lib/atlas/taxonomy.ts subSheetToLobe.
// Hand-authored regex set; unknown tabs default to the Platform lobe so
// new sub-sheets surface immediately rather than silently filtering out.
//
// IMPORTANT: keep in sync with lib/atlas/taxonomy.ts. parse_test.go has a
// parity test that loads the JS source and verifies the Go side produces
// identical mappings for every known tab name in the production sheet.
func SubSheetToProduct(tabName string) SubSheetMapping {
	cleaned := strings.TrimSpace(tabName)
	for _, skip := range skipTabs {
		if skip.MatchString(cleaned) {
			return SubSheetMapping{Skip: true, Lobe: "platform", Product: cleaned, Source: "skip"}
		}
	}
	for _, rule := range subSheetRules {
		if rule.pattern.MatchString(cleaned) {
			product := rule.product
			if product == "" {
				product = cleaned
			}
			return SubSheetMapping{Skip: false, Lobe: rule.lobe, Product: product, Source: "explicit"}
		}
	}
	return SubSheetMapping{Skip: false, Lobe: "platform", Product: cleaned, Source: "default"}
}

type subSheetRule struct {
	pattern *regexp.Regexp
	lobe    string
	product string // canonical name; empty → use the raw tab name
}

// Tabs to skip entirely. Same set as lib/atlas/taxonomy.ts SKIP_TABS.
var skipTabs = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^product\s*design$`), // master roll-up — would double-count
	regexp.MustCompile(`(?i)^lotties$`),          // animation assets
	regexp.MustCompile(`(?i)^illustrations$`),    // off-brain (asset surface)
}

// Sub-sheet rules — order matters, first match wins. Patterns mirror
// lib/atlas/taxonomy.ts SUB_SHEET_LOBES exactly.
var subSheetRules = []subSheetRule{
	// ── Markets
	{regexp.MustCompile(`(?i)^indstocks?$`), "markets", "INDstocks"},
	{regexp.MustCompile(`(?i)^mutual\s*funds?$`), "markets", "Mutual Funds"},
	{regexp.MustCompile(`(?i)^us\s*stocks?$`), "markets", "US Stocks"},
	{regexp.MustCompile(`(?i)^fixed\s*deposits?$`), "markets", "Fixed Deposit"},
	{regexp.MustCompile(`(?i)^f&?o$`), "markets", "F&O"},

	// ── Recurring Payments
	{regexp.MustCompile(`(?i)^bbps\s*&?\s*tpap$`), "recurring_payments", "BBPS & TPAP"},
	{regexp.MustCompile(`(?i)^credit\s*cards?$`), "recurring_payments", "Credit Card"},

	// ── Lending
	{regexp.MustCompile(`(?i)^insta\s*cash$`), "lending", "Insta Cash"},
	{regexp.MustCompile(`(?i)^insta\s*plus$`), "lending", "Insta Plus"},
	{regexp.MustCompile(`(?i)^nbfc$`), "lending", "NBFC"},

	// ── Money Matters
	{regexp.MustCompile(`(?i)^plutus$`), "money_matters", "Plutus"},
	{regexp.MustCompile(`(?i)^neo\s*banking?$`), "money_matters", "Neobanking"},
	{regexp.MustCompile(`(?i)^insurance$`), "money_matters", "Insurance"},
	{regexp.MustCompile(`(?i)^goals?$`), "money_matters", "Goals"},

	// ── Platform — patterns flexible enough to absorb the sheet's typos
	// ("Onbording KYC", "Platfrom & global serach"). Test cases pin the
	// exact strings we know about today; new typos go in via a future PR.
	{regexp.MustCompile(`(?i)^on[bp][a-z]*ding\s*kyc$`), "platform", "Onboarding KYC"},
	{regexp.MustCompile(`(?i)^pl[a-z]+\s*&?\s*global\s*s[a-z]+ch$`), "platform", "Platform & Global Search"},
	{regexp.MustCompile(`(?i)^referral$`), "platform", "Referral"},
	{regexp.MustCompile(`(?i)^chatbot$`), "platform", "Chatbot"},
	{regexp.MustCompile(`(?i)^social\s*project$`), "platform", "Social Project"},
	{regexp.MustCompile(`(?i)^ind\s*learn$`), "platform", "INDlearn"},

	// ── Web Platform
	{regexp.MustCompile(`(?i)^ind\s*web$`), "web_platform", "INDWeb"},
}

// ─── Status normalization ──────────────────────────────────────────────────

// NormalizeStatus collapses the messy free-text values from the sheet's
// Status column into a small canonical set the inspector can render
// consistently. See plan §"Deepening Findings" for the real-data values.
func NormalizeStatus(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	switch s {
	case "":
		return ""
	case "done", "complete":
		return "done"
	case "wip", "in dev", "in progress":
		return "wip"
	case "in review":
		return "in_review"
	case "tbd", "to be picked":
		return "tbd"
	case "backlog":
		return "backlog"
	default:
		// Unknown statuses pass through lowercased so the inspector can
		// at least show them; admins can decide later whether to add
		// canonical mappings.
		return s
	}
}

// ─── DRD URL classification (Tier-1/Tier-2 routing) ────────────────────────

type DRDKind string

const (
	DRDEmpty      DRDKind = "empty"
	DRDGoogleDoc  DRDKind = "googledoc"  // Tier 2 — fetchable via Docs API + SA
	DRDConfluence DRDKind = "confluence" // Tier 1 — URL link only
	DRDNotion     DRDKind = "notion"     // Tier 1 — URL link only
	DRDOtherURL   DRDKind = "other-url"  // Tier 1 — URL link only
	DRDNoneMarker DRDKind = "none"       // "No DRD", "N/A", etc. — treat as empty
)

// gdocIDRe extracts the document ID from a Google Docs URL.
//   /document/d/<DOCID>/edit?...
//   /document/u/0/d/<DOCID>/...
var gdocIDRe = regexp.MustCompile(`/document/(?:u/\d+/)?d/([A-Za-z0-9_\-]+)`)

// ClassifyDRD resolves the DRD column value (sheet column D) and, when
// present, extracts the Google Doc ID for Tier-2 fetch.
func ClassifyDRD(raw string) (kind DRDKind, gdocID string) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return DRDEmpty, ""
	}
	lower := strings.ToLower(s)
	switch lower {
	case "no drd", "no drd shared", "n/a", "na", "-":
		return DRDNoneMarker, ""
	}
	if !strings.HasPrefix(lower, "http") {
		// Non-URL inline text — treat as empty for the link-button surface
		return DRDEmpty, ""
	}
	switch {
	case strings.Contains(lower, "atlassian.net") || strings.Contains(lower, "confluence"):
		return DRDConfluence, ""
	case strings.Contains(lower, "docs.google.com"):
		if m := gdocIDRe.FindStringSubmatch(s); m != nil {
			return DRDGoogleDoc, m[1]
		}
		return DRDGoogleDoc, ""
	case strings.Contains(lower, "notion.so"):
		return DRDNotion, ""
	default:
		return DRDOtherURL, ""
	}
}
