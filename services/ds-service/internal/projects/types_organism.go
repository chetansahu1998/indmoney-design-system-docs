package projects

import "time"

// types_organism.go — row shapes for the organism-pattern-detection corpus
// introduced in migration 0024 (U1). These are the wire/persistence shapes;
// the pure-computation shapes (OrganismFingerprint, OrganismMatchVerdict) live
// in pipeline_organism_match.go alongside their consumers.

// DetectedOrganismMatch mirrors the detected_organism_match table row 1:1.
// Stage 6.7 (U5) writes these; Part B's plugin endpoint (U7) and Part C's
// admin dashboard (U10) read them.
type DetectedOrganismMatch struct {
	VersionID            string
	FrameID              string
	ScreenID             string
	TenantID             string
	SuspectedSlug        string // empty when MatchKind = "novel"
	SuspectedVariantKey  string
	MatchKind            string  // 'exact' | 'near' | 'novel' — see CHECK constraint in 0024
	FingerprintHash      string
	AtomSignatureJSON    string  // serialized []string from OrganismFingerprint.AtomSet
	SlotTopologyJSON     string  // serialized []OrganismSlot from OrganismFingerprint.SlotTopology
	DiffJSON             string  // serialized []OrganismSlotDelta; empty for exact
	Confidence           float64 // 0.0–1.0
	ManifestHash         string
	ParentFrameID        string // empty for top-level matches
	DetectedAt           time.Time
}

// PromotionCandidate mirrors the promotion_candidate table row 1:1. Built by
// Part D's RebuildPromotionCandidates aggregation; consumed by Part C's
// admin dashboard panel (U14).
type PromotionCandidate struct {
	TenantID        string
	FingerprintHash string
	Frequency       int
	FileCount       int
	StabilityScore  float64
	AtomReuseRate   float64
	ProposedName    string    // nullable
	DismissedAt     time.Time // zero value when not dismissed
	FirstSeen       time.Time
	LastSeen        time.Time
}
