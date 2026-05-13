package projects

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"sort"
	"strings"
)

// pipeline_organism_match.go — Phase 1 of the organism-pattern-detection plan
// (docs/plans/2026-05-13-001-feat-organism-pattern-detection-plan.md, U3/U4).
//
// Two concerns live in this file:
//   - WalkOrganismCandidates: depth-first traversal of a canonical_tree that
//     emits one OrganismFingerprint per FRAME whose subtree contains ≥ 2
//     atom INSTANCEs. Pure function; no I/O. (U3)
//   - ClassifyFingerprint: Jaccard-based comparison of a fingerprint against
//     the published OrganismSignature catalog, producing the verdict that
//     gets persisted to detected_organism_match. (U4, added in a follow-up
//     commit.)
//
// Why both in one file: the two functions share the helper set (slot-kind
// regex map, name-to-atom-slug normalization, canonical-JSON hashing). Co-
// locating them keeps the helpers private to this concern and lets the
// classifier and walker evolve together without cross-file dance.

// ─── Constants + helper regexes ──────────────────────────────────────────────

// organismMinAtomInstances is the minimum atom-INSTANCE count for a FRAME to
// qualify as an organism candidate. Below this we're inside a tiny wrapper
// (status-bar icon, single chevron) that isn't meaningfully composable.
const organismMinAtomInstances = 2

// fingerprintHashHexLen is the hex-encoded length of the sha256_16 prefix
// we use as the fingerprint hash. Mirrors the table column width and the
// ManifestHash format from BuildOrganismSignatures.
const fingerprintHashHexLen = 32

// Slot-kind inference patterns. Mirrors app/atlas/_lib/leafcanvas-v2/node-
// classifier.ts so the Go walker and the TS classifier classify the same
// canonical_tree the same way (parity is load-bearing per the 2026-05-13
// Vector-pollution incident in the plan's Institutional Learnings).
//
// Order matters — first match wins. More specific patterns (e.g. "Right Icon")
// must come before generic ones (e.g. "Icon").
var slotKindPatterns = []struct {
	kind string
	re   *regexp.Regexp
}{
	{kind: "LEFT_ICON", re: regexp.MustCompile(`(?i)^\s*left\s*icon(\b|/)`)},
	{kind: "RIGHT_ICON", re: regexp.MustCompile(`(?i)^\s*right\s*icon(\b|/)`)},
	{kind: "LEFT_TEXT", re: regexp.MustCompile(`(?i)^\s*left\s*text(\b|/)`)},
	{kind: "RIGHT_TEXT", re: regexp.MustCompile(`(?i)^\s*right\s*text(\b|/)`)},
	{kind: "OVERLINE", re: regexp.MustCompile(`(?i)^\s*overline(\b|/)`)},
	{kind: "SUBTEXT", re: regexp.MustCompile(`(?i)^\s*subtext(\b|/)`)},
	{kind: "BADGE", re: regexp.MustCompile(`(?i)^\s*badge(s)?(\b|/)`)},
	{kind: "SEPARATOR", re: regexp.MustCompile(`(?i)^\s*separator(s)?(\b|/)`)},
	{kind: "ICON", re: regexp.MustCompile(`(?i)^\s*icons?(/|\b)`)},
}

// slotKindFromName returns one of the SLOT_KIND constants given a Figma node
// name. Falls back to "UNKNOWN" so the classifier has a stable enumeration
// to compare. Unknown is intentional — it lets `near` matches still pair on
// atom_set even when slot-naming drifts.
func slotKindFromName(name string) string {
	for _, p := range slotKindPatterns {
		if p.re.MatchString(name) {
			return p.kind
		}
	}
	return "UNKNOWN"
}

// nameToAtomSlug maps a Figma INSTANCE name to a manifest atom slug heuristic.
// The manifest currently lacks a componentId → slug index, so we infer the
// slug by normalizing the INSTANCE name. This is a deliberate heuristic with
// known limits:
//
//   - "Left Icon/Default"        → "left-icon-default"
//   - "Right Text"               → "right-text"
//   - "Icons/2D/Chevron right"   → "icons-2d-chevron-right"
//   - "Icons/Logo/reliance"      → "icons-logo-reliance"
//
// When cmd/variants writes a per-componentId index into a future manifest
// version we can swap this heuristic for a direct lookup without changing
// callers.
var nameSlugCleanup = regexp.MustCompile(`[^a-z0-9]+`)

func nameToAtomSlug(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = nameSlugCleanup.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	return s
}

// ─── Slot + Fingerprint types ────────────────────────────────────────────────

// OrganismSlot describes one direct-child position inside an organism-shaped
// FRAME. The classifier (U4) compares slot lists; the dashboard (U12) renders
// the topology visually.
type OrganismSlot struct {
	// SlotKind is the inferred slot semantic ("LEFT_ICON", "RIGHT_TEXT", ...)
	// or "UNKNOWN" when no name pattern matched.
	SlotKind string `json:"slot_kind"`
	// BBoxRank is the 0-based ordering within the parent FRAME under
	// canonical bbox sort (top-to-bottom, left-to-right ties).
	BBoxRank int `json:"bbox_rank"`
	// AtomSlug is the heuristic-resolved atom slug ("left-icon-default",
	// "right-text", …). Empty when the slot's node isn't an INSTANCE — those
	// slots survive in the topology so the classifier can spot drift like
	// "wild adds a TEXT in slot 2 that the published variant doesn't have."
	AtomSlug string `json:"atom_slug,omitempty"`
}

// OrganismFingerprint is one detection candidate emitted by the walker.
// Pure data — no IO, no manifest reference. The classifier (U4) and the
// pipeline writer (U5) consume it.
type OrganismFingerprint struct {
	// FrameID is the Figma node id of the candidate FRAME.
	FrameID string `json:"frame_id"`
	// FrameName is the FRAME's name (e.g. "List/Full width", "Frame 1321..."),
	// preserved verbatim for downstream display + diff rendering.
	FrameName string `json:"frame_name"`
	// ParentFrameID is the enclosing organism candidate's frame id, if any.
	// Empty for top-level matches. Set when a candidate FRAME sits inside
	// another candidate FRAME (e.g. a List on Surface row inside a List on
	// Card outer container).
	ParentFrameID string `json:"parent_frame_id,omitempty"`
	// AtomSet is the sorted unique set of atom slugs the FRAME's INSTANCE
	// descendants resolve to via nameToAtomSlug. Drives Jaccard comparison.
	AtomSet []string `json:"atom_set"`
	// SlotTopology is the bbox-ordered list of direct-child slots, each
	// tagged by inferred SlotKind. Drives slot-arrangement comparison.
	SlotTopology []OrganismSlot `json:"slot_topology"`
	// Hash is the sha256_16 of canonical-JSON(AtomSet, SlotTopology). The
	// same canonical structure across two frames produces the same hash —
	// the join key for Part D's promotion-candidate clustering.
	Hash string `json:"hash"`
	// AtomInstanceCount is the raw count of INSTANCE nodes (any componentId)
	// in the frame's subtree. Used in Part D's atom_reuse_rate score.
	AtomInstanceCount int `json:"atom_instance_count"`
	// TotalDescendantCount is the raw count of every node in the frame's
	// subtree (including the frame itself). Used in Part D's atom_reuse_rate
	// denominator.
	TotalDescendantCount int `json:"total_descendant_count"`
}

// ─── Walker ──────────────────────────────────────────────────────────────────

// WalkOrganismCandidates traverses a canonical_tree root and returns one
// OrganismFingerprint per organism-shaped FRAME (≥ organismMinAtomInstances
// atom-INSTANCE descendants). The screen-root FRAME itself is skipped — it's
// always a phone-screen container in production canonical_trees, never an
// organism candidate. Tests that want to evaluate a fixture's root directly
// should wrap it in a synthetic outer FRAME via WrapForOrganismWalk.
//
// `treeRoot` is the decoded canonical_tree (typically `{document: {...}}`
// unwrapped to `document`). Pass map[string]any directly — mirrors
// ExtractClusterIDs's input convention.
func WalkOrganismCandidates(treeRoot map[string]any) []OrganismFingerprint {
	if treeRoot == nil {
		return nil
	}
	// Production canonical_trees come wrapped as `{document: <node>}`.
	// Unwrap if present so callers don't have to.
	if doc, ok := treeRoot["document"].(map[string]any); ok {
		treeRoot = doc
	}
	out := make([]OrganismFingerprint, 0)
	// Skip the root — descend from children. Same pattern as
	// ExtractClusterIDs in pipeline_cluster_prerender.go.
	if children, ok := treeRoot["children"].([]any); ok {
		for _, c := range children {
			cm, _ := c.(map[string]any)
			if cm == nil {
				continue
			}
			walkOrganismFromNode(cm, nil, &out)
		}
	}
	return out
}

// WrapForOrganismWalk wraps a single node in a synthetic outer FRAME so test
// fixtures whose top-level FRAME IS the organism candidate can be fed to the
// public walker. Production callers don't need this — they pass the screen
// root directly.
func WrapForOrganismWalk(node map[string]any) map[string]any {
	return map[string]any{
		"id":       "__test_wrapper",
		"name":     "__test_wrapper",
		"type":     "FRAME",
		"children": []any{node},
	}
}

// walkOrganismFromNode evaluates `node` as a candidate, then recurses into
// children. parentChain[len-1] is the nearest enclosing candidate; new
// candidates inherit it as parent_frame_id.
//
// Recursion descends through ALL container types so a candidate FRAME nested
// inside a GROUP / BOOLEAN_OPERATION / INSTANCE still surfaces. We do NOT
// stop at a candidate (unlike walkClusters which stops to avoid double-
// rasterizing); organism candidates can be legitimately nested.
func walkOrganismFromNode(node map[string]any, parentChain []*OrganismFingerprint, out *[]OrganismFingerprint) {
	if node == nil {
		return
	}
	if visible, ok := node["visible"].(bool); ok && !visible {
		return
	}
	if removed, ok := node["removed"].(bool); ok && removed {
		return
	}

	t, _ := node["type"].(string)
	candidateAdded := false
	if t == "FRAME" {
		if fp, ok := buildFingerprint(node); ok {
			if len(parentChain) > 0 {
				fp.ParentFrameID = parentChain[len(parentChain)-1].FrameID
			}
			*out = append(*out, fp)
			candidateAdded = true
			parentChain = append(parentChain, &(*out)[len(*out)-1])
		}
	}

	children, _ := node["children"].([]any)
	for _, c := range children {
		cm, _ := c.(map[string]any)
		if cm == nil {
			continue
		}
		walkOrganismFromNode(cm, parentChain, out)
	}
	_ = candidateAdded
}

// buildFingerprint walks `frame`'s subtree counting atom INSTANCEs +
// collecting slot topology. Returns ok=false when the FRAME doesn't meet
// the organismMinAtomInstances threshold or when its bbox is missing.
func buildFingerprint(frame map[string]any) (OrganismFingerprint, bool) {
	frameID, _ := frame["id"].(string)
	frameName, _ := frame["name"].(string)
	if frameID == "" {
		return OrganismFingerprint{}, false
	}

	atomInsts, totalDescendants := collectInstanceCounts(frame)
	if atomInsts < organismMinAtomInstances {
		return OrganismFingerprint{}, false
	}

	atomSet := collectAtomSlugs(frame)
	if len(atomSet) < organismMinAtomInstances {
		// The instance count was ≥ threshold but most resolve to the same
		// slug heuristic — collapsed-set candidates are weak signal. Skip.
		// E.g. 5 INSTANCE children all named "Vector" → 1 slug → not an
		// organism, just an icon-cluster.
		return OrganismFingerprint{}, false
	}

	slots := collectSlotTopology(frame)
	hash := computeFingerprintHash(atomSet, slots)

	return OrganismFingerprint{
		FrameID:              frameID,
		FrameName:            frameName,
		AtomSet:              atomSet,
		SlotTopology:         slots,
		Hash:                 hash,
		AtomInstanceCount:    atomInsts,
		TotalDescendantCount: totalDescendants,
	}, true
}

// collectInstanceCounts returns (atom-INSTANCE count, total-descendant count)
// for a frame's subtree (frame itself excluded). atom-INSTANCE counts only
// INSTANCEs with a non-empty componentId; raw drawn shapes don't count.
func collectInstanceCounts(frame map[string]any) (atomInsts, total int) {
	children, _ := frame["children"].([]any)
	for _, c := range children {
		cm, _ := c.(map[string]any)
		if cm == nil {
			continue
		}
		a, tot := countInstancesRec(cm)
		atomInsts += a
		total += tot
	}
	return
}

func countInstancesRec(node map[string]any) (atomInsts, total int) {
	if node == nil {
		return 0, 0
	}
	total = 1
	if t, _ := node["type"].(string); t == "INSTANCE" {
		if cid, _ := node["componentId"].(string); cid != "" {
			atomInsts++
		}
	}
	children, _ := node["children"].([]any)
	for _, c := range children {
		cm, _ := c.(map[string]any)
		if cm == nil {
			continue
		}
		a, tot := countInstancesRec(cm)
		atomInsts += a
		total += tot
	}
	return
}

// collectAtomSlugs returns the sorted unique set of nameToAtomSlug(instance.name)
// for every INSTANCE descendant with a non-empty componentId. Stable across
// runs so the fingerprint hash is deterministic (R3 idempotency).
func collectAtomSlugs(frame map[string]any) []string {
	seen := map[string]struct{}{}
	var walk func(n map[string]any)
	walk = func(n map[string]any) {
		if n == nil {
			return
		}
		if t, _ := n["type"].(string); t == "INSTANCE" {
			if cid, _ := n["componentId"].(string); cid != "" {
				if name, _ := n["name"].(string); name != "" {
					slug := nameToAtomSlug(name)
					if slug != "" {
						seen[slug] = struct{}{}
					}
				}
			}
		}
		children, _ := n["children"].([]any)
		for _, c := range children {
			cm, _ := c.(map[string]any)
			walk(cm)
		}
	}
	children, _ := frame["children"].([]any)
	for _, c := range children {
		cm, _ := c.(map[string]any)
		walk(cm)
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// collectSlotTopology builds the bbox-ranked slot list from the frame's
// DIRECT children. Direct-children only — the topology cares about the
// outer composition, not deep descendants. Each child's slot kind is
// inferred from its name; its atom_slug is set when the child itself is an
// INSTANCE.
func collectSlotTopology(frame map[string]any) []OrganismSlot {
	type bboxChild struct {
		x, y    float64
		node    map[string]any
		atomSlug string
	}
	var bs []bboxChild
	children, _ := frame["children"].([]any)
	for _, c := range children {
		cm, _ := c.(map[string]any)
		if cm == nil {
			continue
		}
		if visible, ok := cm["visible"].(bool); ok && !visible {
			continue
		}
		bbox, _ := cm["absoluteBoundingBox"].(map[string]any)
		x, _ := bbox["x"].(float64)
		y, _ := bbox["y"].(float64)
		var atomSlug string
		if t, _ := cm["type"].(string); t == "INSTANCE" {
			if cid, _ := cm["componentId"].(string); cid != "" {
				name, _ := cm["name"].(string)
				atomSlug = nameToAtomSlug(name)
			}
		}
		bs = append(bs, bboxChild{x: x, y: y, node: cm, atomSlug: atomSlug})
	}
	// Sort: top-to-bottom (y asc), then left-to-right (x asc) on ties.
	// Round to 1 px to absorb float drift (mirrors visible-filter.ts::bboxKey).
	sort.SliceStable(bs, func(i, j int) bool {
		yi, yj := int(bs[i].y+0.5), int(bs[j].y+0.5)
		if yi != yj {
			return yi < yj
		}
		return int(bs[i].x+0.5) < int(bs[j].x+0.5)
	})
	out := make([]OrganismSlot, 0, len(bs))
	for i, b := range bs {
		name, _ := b.node["name"].(string)
		out = append(out, OrganismSlot{
			SlotKind: slotKindFromName(name),
			BBoxRank: i,
			AtomSlug: b.atomSlug,
		})
	}
	return out
}

// computeFingerprintHash produces a deterministic sha256_16 over a
// canonical-JSON encoding of (atom_set, slot_topology). Both inputs are
// already sorted/ordered upstream so the JSON encoding is stable.
func computeFingerprintHash(atomSet []string, slots []OrganismSlot) string {
	payload := struct {
		AtomSet      []string       `json:"atom_set"`
		SlotTopology []OrganismSlot `json:"slot_topology"`
	}{AtomSet: atomSet, SlotTopology: slots}
	data, _ := json.Marshal(payload) // struct marshaling order is deterministic per Go spec
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:16])
}
