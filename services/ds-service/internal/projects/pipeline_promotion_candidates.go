package projects

import (
	"context"
	"database/sql"
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
// in lockstep with each import. Cheap to run — the heavy lift is a single
// SQL group-by + a single Go-side topology parse per cluster.
//
// Clustering key: `fingerprint_hash` (strict identity over
// (atom_set, slot_topology)). Two frames cluster together iff their
// canonical structure is bit-identical.

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
	FingerprintHash string
	Frequency       int
	FileCount       int
	// RepTopologyJSON is the slot_topology_json of one representative row
	// from the cluster. By construction (fingerprint_hash strict identity)
	// every member shares this value, so picking the MAX or MIN is
	// arbitrary — both produce the same result.
	RepTopologyJSON string
	FirstSeen       time.Time
	LastSeen        time.Time
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

	// HAVING applies the K/N thresholds. The composite ranking happens at
	// read time (ListPromotionCandidates ORDER BY frequency * stability *
	// atom_reuse), so we don't need it here.
	query := `
		SELECT
		  m.fingerprint_hash,
		  COUNT(*) AS frequency,
		  COUNT(DISTINCT f.file_id) AS file_count,
		  MAX(m.slot_topology_json) AS rep_topology,
		  MIN(m.detected_at) AS first_seen,
		  MAX(m.detected_at) AS last_seen
		FROM detected_organism_match m
		JOIN screens s ON s.id = m.screen_id
		JOIN flows  f ON f.id = s.flow_id
		WHERE m.tenant_id = ? AND ` + whereKind + `
		GROUP BY m.fingerprint_hash
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
		if err := rows.Scan(&c.FingerprintHash, &c.Frequency, &c.FileCount,
			&c.RepTopologyJSON, &firstSeen, &lastSeen); err != nil {
			return nil, fmt.Errorf("scan promotion cluster: %w", err)
		}
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
		FingerprintHash: c.FingerprintHash,
		Frequency:       c.Frequency,
		FileCount:       c.FileCount,
		// Stability score is structurally 1.0 under strict-identity
		// clustering — every member has the same slot_topology by
		// construction, so variance is always 0. Future evolution
		// (loosen clustering to atom-set-only hash with topology drift
		// scoring) would let this go below 1.0.
		StabilityScore: 1.0,
		AtomReuseRate:  atomReuse,
		FirstSeen:      firstSeen,
		LastSeen:       lastSeen,
	}, nil
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
