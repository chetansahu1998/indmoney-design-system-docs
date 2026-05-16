package projects

import (
	"regexp"
	"strconv"
	"strings"
)

// figma_page_classifier.go — U2 of the autosync bridge plan (2026-05-14).
//
// Classifies figma_page rows for one Figma file into 'final', 'version',
// 'noise', or 'unknown'. Pure function over the page-name string set;
// no I/O. The AutoSyncPlanner uses the classification to pick which
// page is the source-of-truth for a file (final preferred, then highest
// version, then nothing).
//
// Why a regex-driven classifier rather than admin-managed rules: 145
// files in the 6-month window today, dominated by ~30 spelling variants
// of "Final Design" + a long tail of version-suffixed pages. A pure
// function over the observed patterns handles 95% deterministically
// and is unit-testable. The ClassifyPages signature also accepts a
// `rules []PagePickerRule` slice for forward-compat (per-tenant
// overrides land as a follow-up; v1 passes empty).

// PageClassification is the enum stored on figma_page.page_classification.
type PageClassification string

const (
	PageClassFinal   PageClassification = "final"
	PageClassVersion PageClassification = "version"
	PageClassNoise   PageClassification = "noise"
	PageClassUnknown PageClassification = "unknown"
)

// ClassifiedPage is one classifier output row.
type ClassifiedPage struct {
	PageID         string
	Name           string
	Classification PageClassification
	// Populated when Classification == PageClassVersion. VersionBase is
	// the page name with the trailing " V?\d+(...)?" stripped — used to
	// group pages by base ("Onboarding v1" + "Onboarding v2" group on
	// VersionBase="Onboarding"). VersionN is the integer suffix.
	VersionBase string
	VersionN    int
	// Populated when Classification == PageClassFinal. The "persona" is
	// whatever remains after stripping "FINAL\s*DESIGN[S]?" / emoji /
	// punctuation from the page name. Empty remainder → "default".
	PersonaHint string
}

// PagePickerRule is the forward-compat override input. Empty slice in v1.
type PagePickerRule struct {
	MatchKind      string // 'exact' | 'glob' | 'regex'
	MatchPattern   string
	Classification PageClassification
	Priority       int
}

// FigmaPageInput is the minimal page identity the classifier needs.
type FigmaPageInput struct {
	PageID string
	Name   string
}

// ─── Regex catalog ──────────────────────────────────────────────────────────
//
// Compiled once at package init. The discovery pass against the real
// 145-file corpus surfaced these variants — see plan §"Discovery summary"
// for the raw observations.

var (
	// Strip leading emoji + zero-width joiners + spaces so the rest of
	// the classifier sees the human-readable core of the name. Matches
	// any code point outside the basic letter/digit range at the start.
	leadingNonAlpha = regexp.MustCompile(`^[^\p{L}\p{N}]+`)
	// Trailing emoji / punctuation — strip the same way at the end.
	trailingNonAlpha = regexp.MustCompile(`[^\p{L}\p{N}]+$`)

	// "Noise" — pages we never treat as source of truth.
	// Each pattern is matched against the lowercase, emoji-stripped name.
	noisePatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)^cover( page)?$`),
		regexp.MustCompile(`(?i)^dump$`),
		regexp.MustCompile(`(?i)\bdump\b`),
		regexp.MustCompile(`(?i)^wip( designs?| - \d+)?$`),
		regexp.MustCompile(`(?i)\bwip\b`),
		regexp.MustCompile(`(?i)\biterations?\b`),
		regexp.MustCompile(`(?i)\barchive\b`),
		regexp.MustCompile(`(?i)\bold( reference| flow)?\b`),
		regexp.MustCompile(`(?i)\bdraft\b`),
		regexp.MustCompile(`(?i)don'?t\s*open`),
		regexp.MustCompile(`(?i)\bscratch\b`),
		regexp.MustCompile(`(?i)\bexplorations?\b`),
		regexp.MustCompile(`(?i)\bdesign\s+exploration\b`),
		regexp.MustCompile(`(?i)^backup\s+old$`),
		regexp.MustCompile(`(?i)^current\s+design$`),
	}

	// "Final" — pages that name themselves as the source of truth.
	// Matched on the lowercase, emoji-stripped name.
	finalPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)^final$`),
		regexp.MustCompile(`(?i)^finals?$`),
		regexp.MustCompile(`(?i)\bfinal\s*designs?\b`),
		regexp.MustCompile(`(?i)\bfinal\s+design\s*\(.*\)$`), // "Final Design (Flutter)"
		regexp.MustCompile(`(?i)^final\s+design\b`),
		regexp.MustCompile(`(?i)\bfinal\s+design[''']?\d{2}$`), // "Final Design'24"
		regexp.MustCompile(`(?i)\bfinal\s+flow\b`),
		regexp.MustCompile(`(?i)\w+\s+final\s+designs?\b`), // "Trader FINAL DESIGN"
		regexp.MustCompile(`(?i)\bfinal\s*🚀`),
		regexp.MustCompile(`(?i).*\s+final$`), // "Insta Cash- Final", "dollar rupee final"
	}

	// Version suffix: "V1", "v2", "V3 (Q1 2023)", "v4 Q2 2023".
	// Captures the base (group 1) and the integer (group 2).
	// The base may be empty (page name is just "v3").
	versionRE = regexp.MustCompile(`(?i)^(.*?)\s*[Vv](\d+)(?:[\s.(].*)?$`)
)

// ClassifyPages assigns a classification to every input row. Order of
// the returned slice matches the input order. Rules slice is honored
// when non-empty (currently unused in v1; planner always passes nil).
func ClassifyPages(rows []FigmaPageInput, rules []PagePickerRule) []ClassifiedPage {
	out := make([]ClassifiedPage, 0, len(rows))
	for _, r := range rows {
		out = append(out, classifyOne(r, rules))
	}
	return out
}

func classifyOne(r FigmaPageInput, rules []PagePickerRule) ClassifiedPage {
	cp := ClassifiedPage{
		PageID:         r.PageID,
		Name:           r.Name,
		Classification: PageClassUnknown,
	}

	// 1. Tenant-supplied rule overrides (forward-compat; empty in v1).
	if hit, ok := applyRules(r.Name, rules); ok {
		cp.Classification = hit
		// Versioned-by-rule pages still get version_base/n extraction.
		if hit == PageClassVersion {
			if base, n, ok := parseVersion(stripFringe(r.Name)); ok {
				cp.VersionBase = base
				cp.VersionN = n
			}
		}
		if hit == PageClassFinal {
			cp.PersonaHint = derivePersona(stripFringe(r.Name))
		}
		return cp
	}

	stripped := stripFringe(r.Name)
	lower := strings.ToLower(stripped)

	// 2. Noise — highest precedence after explicit rules. "Final Old
	// Reference" would otherwise match the Final regex; noise check
	// runs first so it short-circuits.
	for _, re := range noisePatterns {
		if re.MatchString(lower) {
			cp.Classification = PageClassNoise
			return cp
		}
	}

	// 3. Final — the dominant source-of-truth pattern.
	for _, re := range finalPatterns {
		if re.MatchString(lower) {
			cp.Classification = PageClassFinal
			cp.PersonaHint = derivePersona(stripped)
			return cp
		}
	}

	// 4. Version suffix — fallback when no Final page exists.
	if base, n, ok := parseVersion(stripped); ok {
		cp.Classification = PageClassVersion
		cp.VersionBase = base
		cp.VersionN = n
		return cp
	}

	// 5. Default — unknown. Planner skips files with only-unknown pages.
	return cp
}

// stripFringe removes leading + trailing emoji/punct so downstream regex
// patterns see the human-readable core. "🏁 Final 🚀" → "Final".
func stripFringe(name string) string {
	s := strings.TrimSpace(name)
	s = leadingNonAlpha.ReplaceAllString(s, "")
	s = trailingNonAlpha.ReplaceAllString(s, "")
	return strings.TrimSpace(s)
}

// parseVersion returns (base, n, ok). Base may be "" when the page name
// is literally "v3" or "V2". When the suffix has a trailing qualifier
// like "v4 Q2 2023", the base is everything before the V and the
// qualifier is dropped.
func parseVersion(name string) (string, int, bool) {
	m := versionRE.FindStringSubmatch(name)
	if m == nil {
		return "", 0, false
	}
	n, err := strconv.Atoi(m[2])
	if err != nil {
		return "", 0, false
	}
	base := strings.TrimSpace(m[1])
	return base, n, true
}

// derivePersona strips "FINAL\s*DESIGN[S]?" + emoji + punctuation from
// a Final page's name and returns the remainder lowercased. Empty
// remainder → "default".
//
// Examples:
//   "Trader FINAL DESIGN"        → "trader"
//   "Final Design (Flutter)"     → "flutter"
//   "🏁 Final Designs"           → "default"
//   "Final"                      → "default"
//   "Investor FINAL DESIGN"      → "investor"
//   "Final Designs - Mobile/mWeb"→ "mobile/mweb"  (downstream parses)
//   "Stories FINAL DESIGN"       → "stories"
var (
	personaStripFinalDesign = regexp.MustCompile(`(?i)\s*final\s*designs?\b`)
	personaStripFinalAlone  = regexp.MustCompile(`(?i)\s*\bfinals?\b\s*`)
	personaStripPunct       = regexp.MustCompile(`(?i)[''"\(\)\[\]🚀🏁🗄️😕📔]`)
)

func derivePersona(strippedName string) string {
	s := strippedName
	// Strip the rocket / flag / archive / etc. emoji explicitly — leading
	// + trailing only handled common cases; here we kill mid-string too.
	s = personaStripPunct.ReplaceAllString(s, " ")
	// Strip "FINAL DESIGN" / "Final Design" / "Final designs" first.
	s = personaStripFinalDesign.ReplaceAllString(s, " ")
	// Then strip a bare "Final" / "FINAL" (with word boundaries).
	s = personaStripFinalAlone.ReplaceAllString(s, " ")
	// Collapse whitespace + strip leading/trailing punct.
	s = strings.Join(strings.Fields(s), " ")
	s = strings.Trim(s, " -_/")
	s = strings.ToLower(s)
	if s == "" {
		return "default"
	}
	return s
}

// applyRules returns (classification, true) when a rule matches. v1
// passes an empty rules slice; this function is plumbed for the future
// admin-managed override table.
func applyRules(name string, rules []PagePickerRule) (PageClassification, bool) {
	if len(rules) == 0 {
		return "", false
	}
	// Higher priority wins. Stable sort by Priority desc then by index.
	// We don't sort in-place to keep ClassifyPages allocation-free.
	bestIdx := -1
	for i, rule := range rules {
		if !ruleMatches(name, rule) {
			continue
		}
		if bestIdx < 0 || rules[i].Priority > rules[bestIdx].Priority {
			bestIdx = i
		}
	}
	if bestIdx < 0 {
		return "", false
	}
	return rules[bestIdx].Classification, true
}

func ruleMatches(name string, rule PagePickerRule) bool {
	switch rule.MatchKind {
	case "exact":
		return name == rule.MatchPattern
	case "glob":
		// Cheap glob — only '*' supported. Convert to regex.
		pat := "^" + regexp.QuoteMeta(rule.MatchPattern) + "$"
		pat = strings.ReplaceAll(pat, `\*`, ".*")
		re, err := regexp.Compile(pat)
		if err != nil {
			return false
		}
		return re.MatchString(name)
	case "regex":
		re, err := regexp.Compile(rule.MatchPattern)
		if err != nil {
			return false
		}
		return re.MatchString(name)
	}
	return false
}

// PickSourcePages applies the page-picker policy to a classified set
// and returns the pages the AutoSyncPlanner should ingest.
//
// Policy:
//   1. If any final pages exist → return ALL final pages (multi-Final
//      files emit one flow per persona).
//   2. Else if any version pages exist → group by VersionBase; pick the
//      one with the highest VersionN per group.
//   3. Else → empty slice (planner records skip_reason='no_source_page').
//
// Noise + unknown pages are never returned.
func PickSourcePages(pages []ClassifiedPage) []ClassifiedPage {
	finals := make([]ClassifiedPage, 0)
	versions := make([]ClassifiedPage, 0)
	for _, p := range pages {
		switch p.Classification {
		case PageClassFinal:
			finals = append(finals, p)
		case PageClassVersion:
			versions = append(versions, p)
		}
	}
	if len(finals) > 0 {
		return finals
	}
	if len(versions) == 0 {
		return nil
	}
	// Group versions by base, pick the max version_n per base.
	// Multiple base groups (e.g. "Mini App vN" + "SBM Flow vN") all
	// surface; that's the right behavior — each is a different sub-flow.
	bestByBase := map[string]ClassifiedPage{}
	for _, v := range versions {
		prev, ok := bestByBase[v.VersionBase]
		if !ok || v.VersionN > prev.VersionN {
			bestByBase[v.VersionBase] = v
		}
	}
	out := make([]ClassifiedPage, 0, len(bestByBase))
	for _, v := range bestByBase {
		out = append(out, v)
	}
	return out
}
