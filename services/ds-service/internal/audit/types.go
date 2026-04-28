// Package audit implements the design-system audit core. It walks Figma
// node trees and emits structured findings about token coverage, component
// usage, and drift relative to the published INDmoney design system.
//
// Schema mirrors a subset of DesignBrain's FinalNodeUnderstanding_v1
// (~/DesignBrain-AI/internal/import/figma/final_node_understanding.go),
// trimmed to what the v1 plugin + Files tab + living-docs surfaces consume.
//
// The same shape is mirrored in TypeScript at lib/audit/types.ts; bumps to
// SchemaVersion must update both ends.
package audit

import "time"

// SchemaVersion is the audit-output contract version. Plugin + docs site
// readers tolerate unknown fields but reject mismatched majors.
const SchemaVersion = "1.0"

// Decision captures whether a component instance was matched to the DS,
// not matched, or matched ambiguously (multiple candidates within tolerance).
type Decision string

const (
	DecisionAccept    Decision = "accept"
	DecisionReject    Decision = "reject"
	DecisionAmbiguous Decision = "ambiguous"
)

// Priority on a fix recommendation. P1 fires on deprecated tokens / breaking
// changes / high-frequency drift; P2 on high-confidence drift; P3 on edge cases.
type Priority string

const (
	PriorityP1 Priority = "P1"
	PriorityP2 Priority = "P2"
	PriorityP3 Priority = "P3"
)

// FixCandidate is a single suggested action — bind this fill to that token,
// swap this custom rect for that DS component, etc.
type FixCandidate struct {
	NodeID       string   `json:"node_id"`
	NodeName     string   `json:"node_name"`
	Property     string   `json:"property"` // "fill" | "stroke" | "text" | "padding" | "radius" | "component"
	Observed     string   `json:"observed"` // "#6B7280", "20px", "Custom Rect"
	TokenPath    string   `json:"token_path"`             // "surface.surface-grey-separator-dark"
	TokenAlias   string   `json:"token_alias,omitempty"`  // e.g. CSS-var name "--surface-surface-grey-separator-dark"
	VariableID   string   `json:"variable_id,omitempty"`  // Figma variable id, when known
	// FigmaName is the original Glyph swatch label (e.g. "Spl/ Brown",
	// "Surface Grey BG"). Plugin uses it for team-library lookup; falls
	// back to TokenPath when absent on legacy audit JSON.
	FigmaName       string   `json:"figma_name,omitempty"`
	FigmaCollection string   `json:"figma_collection,omitempty"`
	Distance     float64  `json:"distance"`               // OKLCH for color, |a-b| for px
	UsageCount   int      `json:"usage_count"`            // how many times the observed value appears in this screen
	Priority     Priority `json:"priority"`
	Reason       string   `json:"reason"`                 // "drift" | "deprecated" | "unbound"
	Rationale    string   `json:"rationale,omitempty"`
	ReplacedBy   string   `json:"replaced_by,omitempty"`  // for deprecation chains
}

// MatchEvidence is the multi-signal scoring breakdown for a component match.
// Mirrors DesignBrain MatchResult.Evidence but with our 4-signal weighting.
type MatchEvidence struct {
	ComponentKey float64 `json:"component_key"` // 0.50
	NameLex      float64 `json:"name_lex"`      // 0.20
	StyleID      float64 `json:"style_id"`      // 0.20
	Color        float64 `json:"color"`         // 0.10
}

// ComponentMatch annotates an INSTANCE / COMPONENT node with the DS-match outcome.
//
// SetKey, MatchedName, MatchedDescription, and AxisCount are tooltip-grade
// metadata: present when an Accept/Ambiguous decision lands on a known DS
// candidate, omitted when the decision is Reject. They let the plugin and
// docs site show "Button · 6 axes" chips and hover-cards without having to
// re-resolve the DS manifest at render time.
type ComponentMatch struct {
	NodeID       string         `json:"node_id"`
	NodeName     string         `json:"node_name"`
	ComponentKey string         `json:"component_key,omitempty"`
	SetKey       string         `json:"set_key,omitempty"`            // durable COMPONENT_SET key
	Score        float64        `json:"score"`
	Decision     Decision       `json:"decision"`
	Evidence     MatchEvidence  `json:"evidence"`
	MatchedSlug  string         `json:"matched_slug,omitempty"`       // DS component slug, if accepted
	MatchedName  string         `json:"matched_name,omitempty"`       // DS component display name
	MatchedDescription string   `json:"matched_description,omitempty"` // DS component description (markdown)
	AxisCount    int            `json:"axis_count,omitempty"`         // number of VARIANT axes on the matched set
}

// TokenCoverage is per-property bound/total counts.
type TokenCoverage struct {
	Fills    Coverage `json:"fills"`
	Text     Coverage `json:"text"`
	Spacing  Coverage `json:"spacing"`
	Radius   Coverage `json:"radius"`
}

type Coverage struct {
	Bound int `json:"bound"`
	Total int `json:"total"`
}

// AuditScreen is one final-design page's findings.
type AuditScreen struct {
	NodeID            string           `json:"node_id"`
	Name              string           `json:"name"` // frame name, e.g. "Trade Screen"
	Slug              string           `json:"slug"` // kebab-case, used for URL hash anchors
	Coverage          TokenCoverage    `json:"coverage"`
	ComponentSummary  ComponentSummary `json:"component_summary"`
	Fixes             []FixCandidate   `json:"fixes"`
	ComponentMatches  []ComponentMatch `json:"component_matches"`
	NodeCount         int              `json:"node_count"`
}

// ComponentSummary is the headline component-usage triple per screen.
type ComponentSummary struct {
	FromDS    int `json:"from_ds"`
	Ambiguous int `json:"ambiguous"`
	Custom    int `json:"custom"`
}

// AuditResult is one file's audit output. Written to lib/audit/<slug>.json.
type AuditResult struct {
	SchemaVersion    string                  `json:"schema_version"`
	FileKey          string                  `json:"file_key"`
	FileName         string                  `json:"file_name"`
	FileSlug         string                  `json:"file_slug"`
	Brand            string                  `json:"brand"`
	Owner            string                  `json:"owner,omitempty"`
	ExtractedAt      time.Time               `json:"extracted_at"`
	FileRev          string                  `json:"file_rev,omitempty"`
	DesignSystemRev  string                  `json:"design_system_rev"`
	OverallCoverage  float64                 `json:"overall_coverage"`
	OverallFromDS    float64                 `json:"overall_from_ds"`
	HeadlineDriftHex string                  `json:"headline_drift_hex,omitempty"`
	Screens          []AuditScreen           `json:"screens"`
	Extensions       map[string]any          `json:"$extensions,omitempty"`
}

// IndexEntry is the per-file row in lib/audit/index.json.
type IndexEntry struct {
	FileKey          string    `json:"file_key"`
	FileName         string    `json:"file_name"`
	FileSlug         string    `json:"file_slug"`
	Brand            string    `json:"brand"`
	ExtractedAt      time.Time `json:"extracted_at"`
	OverallCoverage  float64   `json:"overall_coverage"`
	OverallFromDS    float64   `json:"overall_from_ds"`
	ScreenCount      int       `json:"screen_count"`
	HeadlineDriftHex string    `json:"headline_drift_hex,omitempty"`
}

// CrossFilePattern is a canonical-hash bucket that appears across ≥ 2 audited
// files — surfaced as a "promote to DS" candidate.
type CrossFilePattern struct {
	CanonicalHash string   `json:"canonical_hash"`
	NodeCount     int      `json:"node_count"`
	Files         []string `json:"files"` // file_slug list
	SuggestedName string   `json:"suggested_name,omitempty"`
}

// TokenUsage is per-token usage roll-up across all audited files.
type TokenUsage struct {
	TokenPath  string                 `json:"token_path"`
	UsageCount int                    `json:"usage_count"`
	FileCount  int                    `json:"file_count"`
	UseSites   []TokenUseSite         `json:"use_sites,omitempty"` // top N for hover popover
}

type TokenUseSite struct {
	FileSlug   string `json:"file_slug"`
	ScreenSlug string `json:"screen_slug"`
	NodeID     string `json:"node_id"`
	NodeName   string `json:"node_name,omitempty"`
}

// ComponentUsage is per-DS-component usage roll-up.
type ComponentUsage struct {
	Slug       string `json:"slug"`
	UsageCount int    `json:"usage_count"`
	FileCount  int    `json:"file_count"`
}

// Index is the roll-up across all audited files. Written to lib/audit/index.json.
type Index struct {
	SchemaVersion     string             `json:"schema_version"`
	GeneratedAt       time.Time          `json:"generated_at"`
	DesignSystemRev   string             `json:"design_system_rev"`
	Files             []IndexEntry       `json:"files"`
	TokenUsage        []TokenUsage       `json:"token_usage"`
	ComponentUsage    []ComponentUsage   `json:"component_usage"`
	CrossFilePatterns []CrossFilePattern `json:"cross_file_patterns"`
	Extensions        map[string]any     `json:"$extensions,omitempty"`
}
