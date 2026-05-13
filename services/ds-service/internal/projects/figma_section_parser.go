package projects

import (
	"regexp"
	"strings"
)

// figma_section_parser.go — U3 of the autosync bridge plan (2026-05-14).
//
// Parses a Figma section name into (sub_product, sub_flow). The design
// convention the team is adopting:
//
//   Section name           Sub-product   Sub-flow
//   ─────────────────────  ────────────  ─────────────────
//   Wallet/Main Flow       Wallet        Main Flow
//   Wallet/MTM handling    Wallet        MTM handling
//   Hero                   (unassigned)  Hero
//   Wallet/MTM/Settlement  Wallet        MTM/Settlement
//
// First "/" splits sub-product from sub-flow; deeper slashes stay in
// sub_flow. Leading/trailing whitespace + emoji are stripped from both
// halves. Slash-less names land in the "(unassigned)" sub-product
// bucket so they still flow downstream — admins rename in Figma to
// move them out.

// UnassignedSubProduct is the fallback bucket for slash-less section names.
const UnassignedSubProduct = "(unassigned)"

// leadingFringe strips leading emoji / punctuation / whitespace so the
// parser sees the human-readable core. Shared by both halves.
var sectionLeadingFringe = regexp.MustCompile(`^[^\p{L}\p{N}(]+`)

// ParseSectionName splits a Figma section name on the first "/" into
// (sub_product, sub_flow). Slash-less names use the "(unassigned)"
// sub-product bucket.
//
// First-slash-wins is deliberate: a name like "Wallet/MTM/Settlement"
// maps cleanly to sub_product="Wallet" with the rest of the hierarchy
// preserved in sub_flow. The audit pipeline's `flows.path` then carries
// the full chain.
func ParseSectionName(raw string) (subProduct, subFlow string) {
	trimmed := strings.TrimSpace(raw)
	trimmed = sectionLeadingFringe.ReplaceAllString(trimmed, "")
	trimmed = strings.TrimSpace(trimmed)

	idx := strings.Index(trimmed, "/")
	if idx < 0 {
		return UnassignedSubProduct, trimmed
	}
	sub := strings.TrimSpace(trimmed[:idx])
	flow := strings.TrimSpace(trimmed[idx+1:])
	if sub == "" {
		// Edge case: "/Main Flow" — leading slash. Treat the leading-empty
		// part as unassigned and keep the rest as sub_flow.
		return UnassignedSubProduct, flow
	}
	return sub, flow
}
