package projects

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// pipeline_promotion_candidates.go — Part D U13 of the organism-pattern-
// detection plan. Aggregates the `detected_organism_match` corpus into
// `promotion_candidate` rows: clusters of fingerprints that recur across
// multiple frames and multiple files within a tenant. The DS team reads
// these as a ranked list of "patterns we should consider publishing as
// new components" or "novel compositions worth adopting upstream."
//
// Triggered from Stage 6.7 (U5) immediately after a fresh
// detected_organism_match batch lands, so the dashboard surface refreshes
// in lockstep with each import.
//
// Clustering key: **atom_signature_json** (loose identity over atom_set
// only — slot_topology is ignored). Two frames cluster together iff their
// sorted atom-INSTANCE set is identical, regardless of how those atoms are
// arranged in the wrapper. This is the Bias #3 fix from the 2026-05-14
// generalization audit: strict-identity (atom_set + slot_topology)
// clustering blocked cross-product patterns like the position-card
// recurring across Networth / INDstocks V4 / V5 / US Stocks, where the
// same atom mix gets nudged into slightly different layouts per surface.
//
// Cluster identifier persisted in `promotion_candidate.fingerprint_hash`
// is sha256(atom_signature_json)[:16]. The column name is a misnomer
// post-shift but kept stable to avoid a schema migration; the stored value
// is the cluster key hash, which is the contract callers actually care
// about.

// PromotionThresholds tunes RebuildPromotionCandidates's inclusion gates.
// Held as a value rather than const so tests can drop K/N for synthetic
// fixtures (Networth has 1 file in the local DB; production tenants run
// with K≥3, N≥2 in DefaultPromotionThresholds).
type PromotionThresholds struct {
	// MinFrequency excludes clusters with fewer occurrences than this.
	// A pattern that appears once isn't a promotion candidate — it's just
	// a one-off composition.
	MinFrequency int
	// MinFileCount excludes clusters confined to a single Figma file. The
	// promotion thesis ("this pattern recurs across the org") needs ≥ 2
	// files for evidence.
	MinFileCount int
	// IncludeNearLowConfidence — when true, low-confidence `near` matches
	// (confidence < NearConfidenceCeiling) are aggregated alongside `novel`
	// matches. Captures the "wild variant" case where designers extended a
	// published organism beyond its variant axis. Default true.
	IncludeNearLowConfidence bool
	// NearConfidenceCeiling is the upper bound for low-confidence near
	// matches. Rows with confidence ≥ this value are presumed close
	// enough to the published variant that consolidation (Part B) is the
	// right action, not promotion.
	NearConfidenceCeiling float64
}

// DefaultPromotionThresholds — the production gates.
var DefaultPromotionThresholds = PromotionThresholds{
	MinFrequency:             3,
	MinFileCount:             2,
	IncludeNearLowConfidence: true,
	NearConfidenceCeiling:    0.7,
}

// RebuildPromotionCandidates aggregates the tenant's organism-match corpus
// into promotion_candidate rows. Replace-set semantics — the entire tenant
// row set is rewritten on each run (UpsertPromotionCandidates already
// implements this via DELETE-then-INSERT in one tx).
//
// Called from Stage 6.7 immediately after a fresh batch of detected_organism_match
// rows is written. Detection failure in the upstream stage doesn't trigger
// this; this method failing logs + returns but doesn't propagate (the corpus
// remains correct, just the aggregation lags by one import).
func (t *TenantRepo) RebuildPromotionCandidates(ctx context.Context, th PromotionThresholds) error {
	if t.tenantID == "" {
		return errors.New("projects: tenant_id required")
	}

	clusters, err := t.collectPromotionClusters(ctx, th)
	if err != nil {
		return fmt.Errorf("collect promotion clusters: %w", err)
	}

	now := t.now().UTC()
	rows := make([]PromotionCandidate, 0, len(clusters))
	for _, c := range clusters {
		row, err := buildPromotionRow(c, now)
		if err != nil {
			// Skip the cluster on parse failure rather than failing the whole
			// rebuild — bad slot_topology_json in one cluster shouldn't block
			// every other cluster.
			continue
		}
		rows = append(rows, row)
	}

	return t.UpsertPromotionCandidates(ctx, rows)
}

// promotionCluster is the intermediate per-cluster aggregate harvested by
// the SQL pass. Go-side scoring + topology parsing happen on this.
type promotionCluster struct {
	// ClusterKey is the canonical clustering input — atom_signature_json,
	// already a sorted JSON array string written by the walker so byte-
	// equality is the right grouping predicate. Hashed to ClusterHash for
	// persistence as `promotion_candidate.fingerprint_hash`.
	ClusterKey  string
	ClusterHash string
	Frequency   int
	FileCount   int
	// RepTopologyJSON is one representative slot_topology_json from the
	// cluster. Under loose clustering members may have differing topologies;
	// MAX gives a stable-but-arbitrary representative for atom-reuse
	// computation. Topology variance is captured separately via
	// DistinctTopologies.
	RepTopologyJSON string
	// DistinctTopologies counts unique slot_topology_json values within
	// this cluster. 1 = every member arranged the atoms identically;
	// higher = looser variance. Feeds the stability score.
	DistinctTopologies int
	FirstSeen          time.Time
	LastSeen           time.Time
}

func (t *TenantRepo) collectPromotionClusters(ctx context.Context, th PromotionThresholds) ([]promotionCluster, error) {
	// Build the WHERE clause: always include 'novel', optionally include
	// low-confidence 'near'. The compound filter lives as a parameter list
	// so we can pass both kind strings + the confidence ceiling cleanly.
	args := []any{t.tenantID}
	whereKind := "(m.match_kind = 'novel'"
	if th.IncludeNearLowConfidence {
		whereKind += " OR (m.match_kind = 'near' AND m.confidence < ?)"
		args = append(args, th.NearConfidenceCeiling)
	}
	whereKind += ")"

	// GROUP BY atom_signature_json — the loose clustering key. The walker
	// emits atom_signature_json as a sorted JSON array string, so byte
	// equality is correct for grouping the same atom-set across topologies.
	// COUNT(DISTINCT slot_topology_json) captures intra-cluster layout
	// variance, which downstream maps to a stability score.
	query := `
		SELECT
		  m.atom_signature_json,
		  COUNT(*) AS frequency,
		  COUNT(DISTINCT f.file_id) AS file_count,
		  COUNT(DISTINCT m.slot_topology_json) AS distinct_topologies,
		  MAX(m.slot_topology_json) AS rep_topology,
		  MIN(m.detected_at) AS first_seen,
		  MAX(m.detected_at) AS last_seen
		FROM detected_organism_match m
		JOIN screens s ON s.id = m.screen_id
		JOIN flows  f ON f.id = s.flow_id
		WHERE m.tenant_id = ? AND ` + whereKind + `
		GROUP BY m.atom_signature_json
		HAVING COUNT(*) >= ? AND COUNT(DISTINCT f.file_id) >= ?
	`
	args = append(args, th.MinFrequency, th.MinFileCount)

	rows, err := t.r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]promotionCluster, 0)
	for rows.Next() {
		var c promotionCluster
		var firstSeen, lastSeen string
		if err := rows.Scan(&c.ClusterKey, &c.Frequency, &c.FileCount,
			&c.DistinctTopologies, &c.RepTopologyJSON, &firstSeen, &lastSeen); err != nil {
			return nil, fmt.Errorf("scan promotion cluster: %w", err)
		}
		c.ClusterHash = hashClusterKey(c.ClusterKey)
		if t, err := time.Parse(time.RFC3339, firstSeen); err == nil {
			c.FirstSeen = t
		}
		if t, err := time.Parse(time.RFC3339, lastSeen); err == nil {
			c.LastSeen = t
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// hashClusterKey produces the persisted cluster identifier from the loose
// clustering key (atom_signature_json). 16-byte hex prefix matches the
// width of fingerprint_hash values elsewhere in the corpus.
func hashClusterKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:16])
}

// buildPromotionRow turns one cluster's aggregate into the persistable
// PromotionCandidate shape. Goes through json.Unmarshal of the rep topology
// to compute atom_reuse_rate.
func buildPromotionRow(c promotionCluster, now time.Time) (PromotionCandidate, error) {
	atomReuse := computeAtomReuseRate(c.RepTopologyJSON)
	firstSeen := c.FirstSeen
	if firstSeen.IsZero() {
		firstSeen = now
	}
	lastSeen := c.LastSeen
	if lastSeen.IsZero() {
		lastSeen = now
	}
	return PromotionCandidate{
		FingerprintHash: c.ClusterHash,
		Frequency:       c.Frequency,
		FileCount:       c.FileCount,
		// Stability under loose clustering: 1.0 when every member arranged
		// the atoms into the same slot_topology (strict-identity case),
		// declining toward a 0.1 floor as designers diverge on layout. The
		// floor preserves the surface — a high-frequency cross-product
		// pattern with loose layouts is still a real candidate; it just
		// ranks below tight ones via the composite score.
		StabilityScore: stabilityFromTopologyVariance(c.Frequency, c.DistinctTopologies),
		AtomReuseRate:  atomReuse,
		FirstSeen:      firstSeen,
		LastSeen:       lastSeen,
	}, nil
}

// stabilityFromTopologyVariance maps (frequency, distinct_topologies) to
// a stability score in [0.1, 1.0].
//
//   - 1 distinct topology across N frames → 1.0 (strict-identity case)
//   - N distinct topologies across N frames → 0.1 (maximally loose)
//   - Linear interpolation between the two
func stabilityFromTopologyVariance(frequency, distinctTopologies int) float64 {
	if frequency <= 0 {
		return 0
	}
	if distinctTopologies <= 1 {
		return 1.0
	}
	denom := frequency - 1
	if denom < 1 {
		denom = 1
	}
	score := 1.0 - float64(distinctTopologies-1)/float64(denom)
	if score < 0.1 {
		return 0.1
	}
	if score > 1.0 {
		return 1.0
	}
	return score
}

// computeAtomReuseRate returns the fraction of slots in the topology that
// carry a non-empty atom_slug (i.e. are real INSTANCE references vs raw
// shape leaves / TEXT / UNKNOWN). Higher values indicate the cluster is
// composed mostly of published atoms, which is a strong signal that
// promoting it to a published component would actually consolidate work
// already done via atom-level reuse.
//
// Returns 0 on parse failure or empty topology — a degenerate cluster has
// nothing to promote, and surfacing it would be noise.
func computeAtomReuseRate(slotTopologyJSON string) float64 {
	if slotTopologyJSON == "" {
		return 0
	}
	var slots []OrganismSlot
	if err := json.Unmarshal([]byte(slotTopologyJSON), &slots); err != nil {
		return 0
	}
	if len(slots) == 0 {
		return 0
	}
	withAtom := 0
	for _, s := range slots {
		if s.AtomSlug != "" {
			withAtom++
		}
	}
	return float64(withAtom) / float64(len(slots))
}

// ─── Compile-time sanity ─────────────────────────────────────────────────────

// Guard so a future rename of UpsertPromotionCandidates is caught at compile
// time. RebuildPromotionCandidates is the only caller in the package.
var _ = (*TenantRepo).UpsertPromotionCandidates
var _ sql.Tx // silence unused-import in builds where the import is shadowed
