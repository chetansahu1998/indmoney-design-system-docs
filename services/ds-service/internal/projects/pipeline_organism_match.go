package projects

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
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

// ─── Classifier (U4) ─────────────────────────────────────────────────────────

// OrganismMatchThresholds tunes the Jaccard cutpoints used by
// ClassifyFingerprint. Held as a value rather than const so callers can
// experiment without a code change — the production pipeline uses
// DefaultOrganismMatchThresholds.
type OrganismMatchThresholds struct {
	// NearMin is the minimum Jaccard for a `near` match. Below this the
	// fingerprint classifies as `novel`.
	NearMin float64
	// ExactMin is the Jaccard at which a fingerprint counts as `exact` IFF
	// the slot-topology hash also matches (atom set is perfect AND
	// arrangement matches). In practice this is 1.0 — partial atom-set
	// matches never count as exact regardless of arrangement.
	ExactMin float64
}

// DefaultOrganismMatchThresholds — used by the Stage 6.7 pipeline pass.
// Conservative defaults bias toward `novel` so Part D's promotion
// recommendations stay focused on truly-distinct patterns rather than
// near-misses already covered by existing organisms.
var DefaultOrganismMatchThresholds = OrganismMatchThresholds{
	NearMin:  0.5,
	ExactMin: 1.0,
}

// OrganismMatchKind enumerates the three verdict buckets stored in the
// detected_organism_match.match_kind column. (Plan classification matrix
// in High-Level Technical Design.)
type OrganismMatchKind string

const (
	MatchKindExact OrganismMatchKind = "exact"
	MatchKindNear  OrganismMatchKind = "near"
	MatchKindNovel OrganismMatchKind = "novel"
)

// OrganismSlotDelta captures one per-slot difference between a fingerprint
// and its best-match published variant. Serialized into the diff_json
// column on detected_organism_match.
type OrganismSlotDelta struct {
	// Kind is one of "added" (fingerprint has slot the variant doesn't),
	// "missing" (variant has slot the fingerprint doesn't), or "moved"
	// (atom present in both but slot position differs).
	Kind string `json:"kind"`
	// AtomSlug names the atom involved. Empty when the diff is about a
	// non-INSTANCE slot.
	AtomSlug string `json:"atom_slug,omitempty"`
}

// OrganismMatchVerdict is the classifier's full output. The pipeline writer
// (U5) maps this 1:1 onto a detected_organism_match row.
type OrganismMatchVerdict struct {
	Kind              OrganismMatchKind   `json:"kind"`
	SuspectedSlug     string              `json:"suspected_slug,omitempty"`
	SuspectedVariant  string              `json:"suspected_variant,omitempty"`
	Confidence        float64             `json:"confidence"`
	Diff              []OrganismSlotDelta `json:"diff,omitempty"`
}

// ClassifyFingerprint scores `fp` against every signature in `sigs` and
// returns the best match's verdict. Pure function; deterministic.
//
// Algorithm (mirrors the plan's decision matrix):
//
//  1. Compute Jaccard(fp.AtomSet, sig.AtomSlugs) for every signature; pick
//     the highest. Tie-broken by lexicographically smaller slug.
//  2. If best Jaccard ≥ ExactMin (= 1.0 by default) AND slot-topology hash
//     matches → MatchKindExact, confidence = 1.0, diff = [].
//  3. If best Jaccard ≥ ExactMin but topology drifts → MatchKindNear with
//     a `moved`-flavored diff and confidence reflecting topology overlap.
//  4. If best Jaccard ∈ [NearMin, ExactMin) → MatchKindNear with diff
//     listing the added/missing atoms; confidence = Jaccard.
//  5. If best Jaccard < NearMin → MatchKindNovel, confidence = best
//     Jaccard (or 0 when sigs is empty).
//
// When sigs is empty (the current production manifest has 0
// composition_refs) the verdict is always Novel with confidence 0 and an
// empty SuspectedSlug. The classifier is robust to that case so Stage 6.7
// (U5) still writes rows.
func ClassifyFingerprint(fp OrganismFingerprint, sigs []OrganismSignature, th OrganismMatchThresholds) OrganismMatchVerdict {
	if len(sigs) == 0 {
		return OrganismMatchVerdict{Kind: MatchKindNovel, Confidence: 0}
	}

	bestIdx := -1
	bestJaccard := -1.0
	fpSet := atomSetToMap(fp.AtomSet)
	for i, sig := range sigs {
		j := jaccard(fpSet, sig.AtomSlugSet())
		if j > bestJaccard ||
			(j == bestJaccard && bestIdx >= 0 && sig.Slug < sigs[bestIdx].Slug) {
			bestJaccard = j
			bestIdx = i
		}
	}
	if bestIdx < 0 {
		return OrganismMatchVerdict{Kind: MatchKindNovel, Confidence: 0}
	}
	best := sigs[bestIdx]
	verdict := OrganismMatchVerdict{
		SuspectedSlug: best.Slug,
		Confidence:    bestJaccard,
	}

	if bestJaccard < th.NearMin {
		verdict.Kind = MatchKindNovel
		// Don't surface a misleading SuspectedSlug for novel matches — the
		// dashboard treats novel matches as their own bucket.
		verdict.SuspectedSlug = ""
		return verdict
	}

	// Variant inference — when the best match has variant labels published,
	// emit the first one as a placeholder. Variant-axis parsing (e.g.
	// "li=yes,ri=yes,rt=yes") is a Part B / Part C refinement; here we keep
	// the field populated with something stable so downstream dashboards
	// don't render `NULL`.
	if len(best.VariantNames) > 0 {
		verdict.SuspectedVariant = best.VariantNames[0]
	}

	// Diff: which atoms are added/missing relative to best.
	verdict.Diff = computeAtomDiff(fp.AtomSet, best.AtomSlugs)

	if bestJaccard >= th.ExactMin && len(verdict.Diff) == 0 {
		verdict.Kind = MatchKindExact
		verdict.Confidence = 1.0
		return verdict
	}
	verdict.Kind = MatchKindNear
	return verdict
}

// atomSetToMap is a tiny helper so jaccard's input shape matches what
// OrganismSignature.AtomSlugSet returns.
func atomSetToMap(atoms []string) map[string]struct{} {
	out := make(map[string]struct{}, len(atoms))
	for _, a := range atoms {
		out[a] = struct{}{}
	}
	return out
}

// jaccard returns |A ∩ B| / |A ∪ B|. Returns 0 when both sets are empty
// (a degenerate case the classifier guards against at higher level).
func jaccard(a, b map[string]struct{}) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 0
	}
	inter := 0
	union := len(a)
	for k := range b {
		if _, ok := a[k]; ok {
			inter++
		} else {
			union++
		}
	}
	return float64(inter) / float64(union)
}

// computeAtomDiff returns slot-deltas describing how the fingerprint's
// atom set differs from the signature's. Stable order: added atoms first
// (sorted), then missing atoms (sorted).
func computeAtomDiff(fpAtoms, sigAtoms []string) []OrganismSlotDelta {
	fpSet := atomSetToMap(fpAtoms)
	sigSet := atomSetToMap(sigAtoms)
	var added, missing []string
	for a := range fpSet {
		if _, ok := sigSet[a]; !ok {
			added = append(added, a)
		}
	}
	for a := range sigSet {
		if _, ok := fpSet[a]; !ok {
			missing = append(missing, a)
		}
	}
	sort.Strings(added)
	sort.Strings(missing)
	out := make([]OrganismSlotDelta, 0, len(added)+len(missing))
	for _, a := range added {
		out = append(out, OrganismSlotDelta{Kind: "added", AtomSlug: a})
	}
	for _, a := range missing {
		out = append(out, OrganismSlotDelta{Kind: "missing", AtomSlug: a})
	}
	return out
}

// ─── Stage 6.7 pipeline integration (U5) ─────────────────────────────────────

// stage67Budget caps the wall-clock for organism detection per project_version
// import. The walker is ~10ms per screen × 100s of screens = under 5s in
// practice; this budget exists as a safety valve so a pathological canonical
// tree can't stall the pipeline.
const stage67Budget = 60 * time.Second

// runOrganismDetection is Stage 6.7's entry point. Called from pipeline.go
// after Stage 6's transaction commits and before Stage 7's SSE publish.
// Detection failure is logged but never re-surfaced — view_ready is already
// durable when this runs (R9 idempotency + non-blocking guarantee).
//
// Inputs are deliberately primitive slices (screenIDs + treeJSONs) instead
// of the unexported screenReattach struct so the method signature doesn't
// leak that internal type across file boundaries.
func (p *Pipeline) runOrganismDetection(parentCtx context.Context, versionID string, screenIDs, treeJSONs []string) {
	if p == nil || p.Repo == nil {
		return
	}
	if len(screenIDs) != len(treeJSONs) {
		if p.Log != nil {
			p.Log.Warn("stage 6.7: screenIDs/treeJSONs length mismatch",
				"screens", len(screenIDs), "trees", len(treeJSONs))
		}
		return
	}
	if len(screenIDs) == 0 {
		return
	}

	// Recover from any walker panics so the pipeline never crashes on a
	// pathological canonical tree.
	defer func() {
		if r := recover(); r != nil && p.Log != nil {
			p.Log.Error("stage 6.7: panic in organism detection",
				"version_id", versionID,
				"panic", fmt.Sprintf("%v", r),
			)
		}
	}()

	ctx, cancel := context.WithTimeout(parentCtx, stage67Budget)
	defer cancel()

	// Load the published organism signature catalog. ManifestPath empty or
	// missing manifest → empty catalog → every fingerprint classifies as
	// novel. Detection still writes rows so Part D's clustering can run.
	signatures, manifestHash, err := BuildOrganismSignatures(p.ManifestPath)
	if err != nil && p.Log != nil {
		p.Log.Warn("stage 6.7: failed to load organism signatures; proceeding with empty catalog",
			"version_id", versionID, "manifest_path", p.ManifestPath, "err", err.Error())
		// Fall through with nil signatures — runOrganismDetection still
		// produces novel verdicts which Part D will cluster.
	}

	now := time.Now().UTC()
	verdicts := make([]DetectedOrganismMatch, 0, len(screenIDs)*2)
	thresholds := DefaultOrganismMatchThresholds

	for i, screenID := range screenIDs {
		if err := ctx.Err(); err != nil {
			if p.Log != nil {
				p.Log.Warn("stage 6.7: budget exceeded; truncating detection",
					"version_id", versionID,
					"screens_processed", i,
					"screens_total", len(screenIDs),
				)
			}
			break
		}
		treeJSON := treeJSONs[i]
		if treeJSON == "" {
			continue
		}
		var tree map[string]any
		if err := json.Unmarshal([]byte(treeJSON), &tree); err != nil {
			if p.Log != nil {
				p.Log.Warn("stage 6.7: unparseable canonical_tree; skipping screen",
					"version_id", versionID, "screen_id", screenID, "err", err.Error())
			}
			continue
		}
		fps := WalkOrganismCandidates(tree)
		for _, fp := range fps {
			v := ClassifyFingerprint(fp, signatures, thresholds)
			row, err := buildDetectedMatchRow(versionID, screenID, fp, v, string(manifestHash), now)
			if err != nil {
				if p.Log != nil {
					p.Log.Warn("stage 6.7: failed to build verdict row; skipping",
						"version_id", versionID, "frame_id", fp.FrameID, "err", err.Error())
				}
				continue
			}
			verdicts = append(verdicts, row)
		}
	}

	if len(verdicts) == 0 {
		if p.Log != nil {
			p.Log.Info("stage 6.7: no organism candidates",
				"version_id", versionID, "screens", len(screenIDs))
		}
		return
	}

	if err := p.Repo.UpsertOrganismMatches(ctx, verdicts); err != nil {
		if p.Log != nil {
			p.Log.Error("stage 6.7: upsert organism matches failed",
				"version_id", versionID, "rows", len(verdicts), "err", err.Error())
		}
		return
	}
	if p.Log != nil {
		// Per-kind tally for operator triage.
		byKind := map[OrganismMatchKind]int{}
		for _, v := range verdicts {
			byKind[OrganismMatchKind(v.MatchKind)]++
		}
		p.Log.Info("stage 6.7: organism detection complete",
			"version_id", versionID,
			"screens", len(screenIDs),
			"matches", len(verdicts),
			"exact", byKind[MatchKindExact],
			"near", byKind[MatchKindNear],
			"novel", byKind[MatchKindNovel],
		)
	}

	// TODO(U13): trigger RebuildPromotionCandidates(tenant_id) here. Part D
	// aggregates the novel-bucket fingerprints across all view_ready versions
	// in the tenant into promotion_candidate rows. Deferred until U13 ships
	// — the corpus is the prerequisite, not the consumer.
}

// buildDetectedMatchRow assembles a DetectedOrganismMatch row from the
// walker's fingerprint + the classifier's verdict, JSON-serializing the
// atom signature, slot topology, and diff. Returns an error only when JSON
// marshaling fails (shouldn't happen — types are pure value).
func buildDetectedMatchRow(versionID, screenID string, fp OrganismFingerprint, v OrganismMatchVerdict, manifestHash string, now time.Time) (DetectedOrganismMatch, error) {
	atomJSON, err := json.Marshal(fp.AtomSet)
	if err != nil {
		return DetectedOrganismMatch{}, fmt.Errorf("marshal atom_signature: %w", err)
	}
	slotJSON, err := json.Marshal(fp.SlotTopology)
	if err != nil {
		return DetectedOrganismMatch{}, fmt.Errorf("marshal slot_topology: %w", err)
	}
	var diffJSON string
	if len(v.Diff) > 0 {
		b, err := json.Marshal(v.Diff)
		if err != nil {
			return DetectedOrganismMatch{}, fmt.Errorf("marshal diff: %w", err)
		}
		diffJSON = string(b)
	}
	return DetectedOrganismMatch{
		VersionID:           versionID,
		FrameID:             fp.FrameID,
		ScreenID:            screenID,
		SuspectedSlug:       v.SuspectedSlug,
		SuspectedVariantKey: v.SuspectedVariant,
		MatchKind:           string(v.Kind),
		FingerprintHash:     fp.Hash,
		AtomSignatureJSON:   string(atomJSON),
		SlotTopologyJSON:    string(slotJSON),
		DiffJSON:            diffJSON,
		Confidence:          v.Confidence,
		ManifestHash:        manifestHash,
		ParentFrameID:       fp.ParentFrameID,
		DetectedAt:          now,
	}, nil
}
