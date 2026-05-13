package projects

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// pipeline_promotion_candidates_test.go — U13 RebuildPromotionCandidates
// tests. The tests use lowered thresholds (K=1/2, N=1/2) so synthetic
// fixtures with 2-3 rows can exercise the SQL aggregation + the Go-side
// atom_reuse_rate computation. Production thresholds stay at K=3/N=2 via
// DefaultPromotionThresholds.

// promoFixture extends orgTestFixture with two extra (file_id, screen)
// pairs so file_count thresholds can be exercised meaningfully without
// re-coding the test seed.
type promoFixture struct {
	orgTestFixture
	screenIDsByFile map[string]string // file_id → screen_id
	versionIDByFile map[string]string // file_id → version_id (different versions can share screens in 1 project)
}

func seedPromoFixture(t *testing.T) promoFixture {
	t.Helper()
	base := seedOrgFixture(t)
	ctx := context.Background()

	screenIDsByFile := map[string]string{
		"file-1": base.screenID,
	}
	versionIDByFile := map[string]string{
		"file-1": base.versionID,
	}

	// Create additional flows under the same project, each attached to a
	// different Figma file. Screens under those flows are the "other-file"
	// rows for file_count aggregation.
	for _, fileID := range []string{"file-2", "file-3"} {
		flow, err := base.repo.UpsertFlow(ctx, Flow{
			ProjectID: base.projectID, FileID: fileID, Name: "Flow " + fileID,
		})
		if err != nil {
			t.Fatalf("upsert flow %s: %v", fileID, err)
		}
		v, err := base.repo.CreateVersion(ctx, base.projectID, base.userID)
		if err != nil {
			t.Fatalf("create version for %s: %v", fileID, err)
		}
		screens := []Screen{
			{VersionID: v.ID, FlowID: flow.ID, X: 0, Y: 0, Width: 375, Height: 812},
		}
		if err := base.repo.InsertScreens(ctx, screens); err != nil {
			t.Fatalf("insert screen for %s: %v", fileID, err)
		}
		screenIDsByFile[fileID] = screens[0].ID
		versionIDByFile[fileID] = v.ID
	}
	return promoFixture{
		orgTestFixture:  base,
		screenIDsByFile: screenIDsByFile,
		versionIDByFile: versionIDByFile,
	}
}

// seedMatches writes detected_organism_match rows for the given (fileID,
// fingerprint_hash) pairs. Each call writes one row. atom_signature defaults
// to ["a","b"] for tests that don't care; pass a custom atomSig via
// seedMatchWithAtoms for tests asserting cluster separation. match_kind
// defaults to 'novel' so the aggregator picks them up.
func (f promoFixture) seedMatch(t *testing.T, fileID, fingerprint, slotTopologyJSON string, kind string, confidence float64) {
	t.Helper()
	f.seedMatchWithAtoms(t, fileID, fingerprint, `["a","b"]`, slotTopologyJSON, kind, confidence)
}

// seedMatchWithAtoms is the explicit form. atomSig becomes the loose-clustering
// key (post-Bias-#3-fix: promotion candidates GROUP BY atom_signature_json,
// not fingerprint_hash). Pass distinct atomSig values when a test needs two
// separate clusters.
func (f promoFixture) seedMatchWithAtoms(t *testing.T, fileID, fingerprint, atomSig, slotTopologyJSON string, kind string, confidence float64) {
	t.Helper()
	screenID := f.screenIDsByFile[fileID]
	versionID := f.versionIDByFile[fileID]
	row := DetectedOrganismMatch{
		VersionID:         versionID,
		FrameID:           "frame-" + uuid.NewString(), // unique per call so UPSERTs don't collapse
		ScreenID:          screenID,
		MatchKind:         kind,
		FingerprintHash:   fingerprint,
		AtomSignatureJSON: atomSig,
		SlotTopologyJSON:  slotTopologyJSON,
		Confidence:        confidence,
		ManifestHash:      "mh1",
	}
	if err := f.repo.UpsertOrganismMatches(context.Background(), []DetectedOrganismMatch{row}); err != nil {
		t.Fatalf("seed match: %v", err)
	}
}

// TestRebuildPromotion_HappyPath — 3 novel matches of the same fingerprint
// across 2 files cluster into 1 promotion_candidate.
func TestRebuildPromotion_HappyPath(t *testing.T) {
	fx := seedPromoFixture(t)
	ctx := context.Background()
	slot := `[{"slot_kind":"LEFT_ICON","bbox_rank":0,"atom_slug":"left-icon-default"},{"slot_kind":"RIGHT_TEXT","bbox_rank":1,"atom_slug":"right-text"}]`

	fx.seedMatch(t, "file-1", "fp-hello", slot, "novel", 0.0)
	fx.seedMatch(t, "file-2", "fp-hello", slot, "novel", 0.0)
	fx.seedMatch(t, "file-2", "fp-hello", slot, "novel", 0.0)

	th := PromotionThresholds{
		MinFrequency: 2, MinFileCount: 2, IncludeNearLowConfidence: false,
	}
	if err := fx.repo.RebuildPromotionCandidates(ctx, th); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	got, err := fx.repo.ListPromotionCandidates(ctx, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 candidate; got %d", len(got))
	}
	c := got[0]
	wantHash := hashClusterKey(`["a","b"]`)
	if c.FingerprintHash != wantHash {
		t.Errorf("hash = %q; want %q (sha256[:16] of atom_signature_json)", c.FingerprintHash, wantHash)
	}
	if c.Frequency != 3 {
		t.Errorf("frequency = %d; want 3", c.Frequency)
	}
	if c.FileCount != 2 {
		t.Errorf("file_count = %d; want 2", c.FileCount)
	}
	// All three matches share the same slot_topology → DistinctTopologies=1
	// → StabilityScore=1.0.
	if c.StabilityScore != 1.0 {
		t.Errorf("stability = %v; want 1.0 (uniform topology)", c.StabilityScore)
	}
	// AtomReuseRate: both slots have non-empty atom_slug → 2/2 = 1.0
	if c.AtomReuseRate != 1.0 {
		t.Errorf("atom_reuse_rate = %v; want 1.0", c.AtomReuseRate)
	}
}

// TestRebuildPromotion_LooseKeyClustering — the Bias #3 case from the
// 2026-05-14 generalization audit. Same atom_set arranged into different
// slot_topologies across product files (Networth, INDstocks V4/V5, US
// Stocks) — the position-card pattern. Under strict-identity clustering
// (pre-fix) these would split into N candidates and fall below K/N
// thresholds; under loose clustering they collapse into one high-frequency
// candidate that DS team can act on.
func TestRebuildPromotion_LooseKeyClustering(t *testing.T) {
	fx := seedPromoFixture(t)
	ctx := context.Background()
	atomSig := `["position-name","price","pnl","quantity"]`

	// 4 frames, same atom set, three distinct layouts across two files —
	// emulates the same atoms being arranged differently in Networth's
	// holdings card vs INDstocks V4 position card vs V5.
	topoA := `[{"slot_kind":"LEFT_TEXT","bbox_rank":0,"atom_slug":"position-name"},{"slot_kind":"RIGHT_TEXT","bbox_rank":1,"atom_slug":"price"}]`
	topoB := `[{"slot_kind":"OVERLINE","bbox_rank":0,"atom_slug":"position-name"},{"slot_kind":"SUBTEXT","bbox_rank":1,"atom_slug":"price"}]`
	topoC := `[{"slot_kind":"LEFT_TEXT","bbox_rank":0,"atom_slug":"position-name"},{"slot_kind":"BADGE","bbox_rank":1,"atom_slug":"pnl"}]`

	fx.seedMatchWithAtoms(t, "file-1", "fp-strictA", atomSig, topoA, "novel", 0.0)
	fx.seedMatchWithAtoms(t, "file-1", "fp-strictA2", atomSig, topoA, "novel", 0.0)
	fx.seedMatchWithAtoms(t, "file-2", "fp-strictB", atomSig, topoB, "novel", 0.0)
	fx.seedMatchWithAtoms(t, "file-2", "fp-strictC", atomSig, topoC, "novel", 0.0)

	// Strict thresholds: K=3, N=2 — exactly the production gates from
	// DefaultPromotionThresholds. Under strict-identity clustering these
	// 4 rows would split into 3 clusters of {2, 1, 1} and none would meet
	// K=3. Loose clustering merges them into one cluster of 4 across 2
	// files, which meets the gates.
	if err := fx.repo.RebuildPromotionCandidates(ctx, PromotionThresholds{
		MinFrequency: 3, MinFileCount: 2,
	}); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	got, _ := fx.repo.ListPromotionCandidates(ctx, 0)
	if len(got) != 1 {
		t.Fatalf("expected 1 loose-cluster candidate (Bias #3 fix); got %d", len(got))
	}
	c := got[0]
	if c.Frequency != 4 {
		t.Errorf("frequency = %d; want 4 (all four wild copies in one cluster)", c.Frequency)
	}
	if c.FileCount != 2 {
		t.Errorf("file_count = %d; want 2", c.FileCount)
	}
	// 3 distinct topologies across 4 frames → stability = 1 - (3-1)/(4-1) = 0.333...
	wantStability := 1.0 - 2.0/3.0
	if diff := c.StabilityScore - wantStability; diff < -0.01 || diff > 0.01 {
		t.Errorf("stability = %v; want ~%v (3 distinct topologies / 4 frames)", c.StabilityScore, wantStability)
	}
	// Cluster id is hash of the atom_signature, not any of the per-row
	// fingerprint values.
	if c.FingerprintHash != hashClusterKey(atomSig) {
		t.Errorf("cluster hash = %q; want %q", c.FingerprintHash, hashClusterKey(atomSig))
	}
}

// TestRebuildPromotion_DistinctAtomSets — atom-set differences DO produce
// separate clusters under loose-key. Guards against over-collapsing.
func TestRebuildPromotion_DistinctAtomSets(t *testing.T) {
	fx := seedPromoFixture(t)
	ctx := context.Background()
	slot := `[{"slot_kind":"LEFT_ICON","bbox_rank":0,"atom_slug":"x"}]`

	// Two clusters, each ≥2 frames across ≥2 files, with different
	// atom_signatures — they should NOT merge.
	fx.seedMatchWithAtoms(t, "file-1", "fp-1", `["a","b"]`, slot, "novel", 0.0)
	fx.seedMatchWithAtoms(t, "file-2", "fp-2", `["a","b"]`, slot, "novel", 0.0)
	fx.seedMatchWithAtoms(t, "file-1", "fp-3", `["c","d"]`, slot, "novel", 0.0)
	fx.seedMatchWithAtoms(t, "file-2", "fp-4", `["c","d"]`, slot, "novel", 0.0)

	if err := fx.repo.RebuildPromotionCandidates(ctx, PromotionThresholds{
		MinFrequency: 2, MinFileCount: 2,
	}); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	got, _ := fx.repo.ListPromotionCandidates(ctx, 0)
	if len(got) != 2 {
		t.Fatalf("expected 2 separate clusters (distinct atom_sets); got %d", len(got))
	}
}

// TestRebuildPromotion_FilterByFrequency — clusters below MinFrequency drop.
func TestRebuildPromotion_FilterByFrequency(t *testing.T) {
	fx := seedPromoFixture(t)
	ctx := context.Background()
	slot := `[{"slot_kind":"LEFT_ICON","bbox_rank":0,"atom_slug":"x"}]`

	fx.seedMatch(t, "file-1", "fp-low", slot, "novel", 0.0)  // frequency=1
	fx.seedMatch(t, "file-2", "fp-low", slot, "novel", 0.0)  // frequency=2 across 2 files

	th := PromotionThresholds{
		MinFrequency: 3, MinFileCount: 2, IncludeNearLowConfidence: false,
	}
	if err := fx.repo.RebuildPromotionCandidates(ctx, th); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	got, _ := fx.repo.ListPromotionCandidates(ctx, 0)
	if len(got) != 0 {
		t.Errorf("expected 0 candidates with MinFrequency=3 and only 2 matches; got %d", len(got))
	}
}

// TestRebuildPromotion_FilterByFileCount — clusters confined to one file drop.
func TestRebuildPromotion_FilterByFileCount(t *testing.T) {
	fx := seedPromoFixture(t)
	ctx := context.Background()
	slot := `[{"slot_kind":"LEFT_ICON","bbox_rank":0,"atom_slug":"x"}]`

	// 3 matches but all in file-1 → file_count = 1 < MinFileCount=2
	fx.seedMatch(t, "file-1", "fp-singlefile-1", slot, "novel", 0.0)
	fx.seedMatch(t, "file-1", "fp-singlefile-2", slot, "novel", 0.0)
	fx.seedMatch(t, "file-1", "fp-singlefile-3", slot, "novel", 0.0)

	th := PromotionThresholds{
		MinFrequency: 1, MinFileCount: 2, IncludeNearLowConfidence: false,
	}
	if err := fx.repo.RebuildPromotionCandidates(ctx, th); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	got, _ := fx.repo.ListPromotionCandidates(ctx, 0)
	if len(got) != 0 {
		t.Errorf("expected 0 single-file candidates; got %d", len(got))
	}
}

// TestRebuildPromotion_IncludeNearLowConfidence — low-confidence near matches
// are aggregated when the flag is on, ignored when it's off.
func TestRebuildPromotion_IncludeNearLowConfidence(t *testing.T) {
	fx := seedPromoFixture(t)
	ctx := context.Background()
	slot := `[{"slot_kind":"LEFT_ICON","bbox_rank":0,"atom_slug":"x"}]`

	// Two 'near' matches at confidence 0.5 across 2 files
	fx.seedMatch(t, "file-1", "fp-near", slot, "near", 0.5)
	fx.seedMatch(t, "file-2", "fp-near", slot, "near", 0.5)

	// With IncludeNearLowConfidence=true → 1 candidate
	if err := fx.repo.RebuildPromotionCandidates(ctx, PromotionThresholds{
		MinFrequency: 2, MinFileCount: 2, IncludeNearLowConfidence: true, NearConfidenceCeiling: 0.7,
	}); err != nil {
		t.Fatalf("rebuild on: %v", err)
	}
	got, _ := fx.repo.ListPromotionCandidates(ctx, 0)
	if len(got) != 1 {
		t.Errorf("expected 1 candidate with near-on; got %d", len(got))
	}

	// With IncludeNearLowConfidence=false → 0
	if err := fx.repo.RebuildPromotionCandidates(ctx, PromotionThresholds{
		MinFrequency: 2, MinFileCount: 2, IncludeNearLowConfidence: false,
	}); err != nil {
		t.Fatalf("rebuild off: %v", err)
	}
	got, _ = fx.repo.ListPromotionCandidates(ctx, 0)
	if len(got) != 0 {
		t.Errorf("expected 0 candidates with near-off; got %d", len(got))
	}
}

// TestRebuildPromotion_NearConfidenceCeiling — near matches above the
// ceiling are excluded (the assumption is they're consolidatable via the
// plugin rather than promotion candidates).
func TestRebuildPromotion_NearConfidenceCeiling(t *testing.T) {
	fx := seedPromoFixture(t)
	ctx := context.Background()
	slot := `[{"slot_kind":"LEFT_ICON","bbox_rank":0,"atom_slug":"x"}]`

	// Confidence 0.9 → above 0.7 ceiling → excluded
	fx.seedMatch(t, "file-1", "fp-high", slot, "near", 0.9)
	fx.seedMatch(t, "file-2", "fp-high", slot, "near", 0.9)

	if err := fx.repo.RebuildPromotionCandidates(ctx, PromotionThresholds{
		MinFrequency: 2, MinFileCount: 2, IncludeNearLowConfidence: true, NearConfidenceCeiling: 0.7,
	}); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	got, _ := fx.repo.ListPromotionCandidates(ctx, 0)
	if len(got) != 0 {
		t.Errorf("expected 0 high-conf-near candidates; got %d", len(got))
	}
}

// TestRebuildPromotion_AtomReuseRate — verify the per-slot atom-coverage
// proxy is correctly computed.
func TestRebuildPromotion_AtomReuseRate(t *testing.T) {
	fx := seedPromoFixture(t)
	ctx := context.Background()

	// 4 slots, 2 with atoms, 2 without → atom_reuse_rate = 0.5
	slot := `[
		{"slot_kind":"LEFT_ICON","bbox_rank":0,"atom_slug":"a"},
		{"slot_kind":"UNKNOWN","bbox_rank":1},
		{"slot_kind":"RIGHT_TEXT","bbox_rank":2,"atom_slug":"b"},
		{"slot_kind":"UNKNOWN","bbox_rank":3}
	]`
	fx.seedMatch(t, "file-1", "fp-half", slot, "novel", 0.0)
	fx.seedMatch(t, "file-2", "fp-half", slot, "novel", 0.0)

	if err := fx.repo.RebuildPromotionCandidates(ctx, PromotionThresholds{
		MinFrequency: 1, MinFileCount: 2,
	}); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	got, _ := fx.repo.ListPromotionCandidates(ctx, 0)
	if len(got) != 1 {
		t.Fatalf("expected 1 candidate; got %d", len(got))
	}
	if got[0].AtomReuseRate < 0.49 || got[0].AtomReuseRate > 0.51 {
		t.Errorf("atom_reuse_rate = %v; expected ~0.5", got[0].AtomReuseRate)
	}
}

// TestRebuildPromotion_ReplaceSet — re-running the rebuild on a tenant
// drops clusters that no longer qualify.
func TestRebuildPromotion_ReplaceSet(t *testing.T) {
	fx := seedPromoFixture(t)
	ctx := context.Background()
	slot := `[{"slot_kind":"LEFT_ICON","bbox_rank":0,"atom_slug":"x"}]`
	fx.seedMatch(t, "file-1", "fp-1", slot, "novel", 0.0)
	fx.seedMatch(t, "file-2", "fp-1", slot, "novel", 0.0)

	th := PromotionThresholds{MinFrequency: 2, MinFileCount: 2}
	if err := fx.repo.RebuildPromotionCandidates(ctx, th); err != nil {
		t.Fatalf("first rebuild: %v", err)
	}
	if got, _ := fx.repo.ListPromotionCandidates(ctx, 0); len(got) != 1 {
		t.Fatalf("expected 1 candidate after first rebuild; got %d", len(got))
	}

	// Tighten threshold so the existing cluster doesn't qualify. Re-run.
	th2 := PromotionThresholds{MinFrequency: 5, MinFileCount: 2}
	if err := fx.repo.RebuildPromotionCandidates(ctx, th2); err != nil {
		t.Fatalf("second rebuild: %v", err)
	}
	if got, _ := fx.repo.ListPromotionCandidates(ctx, 0); len(got) != 0 {
		t.Fatalf("expected 0 candidates after tightening; got %d", len(got))
	}
}

// TestRebuildPromotion_TenantIsolation — tenant B's rebuild doesn't pick up
// tenant A's matches.
func TestRebuildPromotion_TenantIsolation(t *testing.T) {
	fx := seedPromoFixture(t)
	ctx := context.Background()
	slot := `[{"slot_kind":"LEFT_ICON","bbox_rank":0,"atom_slug":"x"}]`
	fx.seedMatch(t, "file-1", "fp-A", slot, "novel", 0.0)
	fx.seedMatch(t, "file-2", "fp-A", slot, "novel", 0.0)

	if err := fx.repo.RebuildPromotionCandidates(ctx, PromotionThresholds{MinFrequency: 2, MinFileCount: 2}); err != nil {
		t.Fatalf("tenant A rebuild: %v", err)
	}
	if err := fx.otherRepo.RebuildPromotionCandidates(ctx, PromotionThresholds{MinFrequency: 2, MinFileCount: 2}); err != nil {
		t.Fatalf("tenant B rebuild: %v", err)
	}

	got, _ := fx.otherRepo.ListPromotionCandidates(ctx, 0)
	if len(got) != 0 {
		t.Errorf("tenant B leaked %d rows from tenant A's corpus", len(got))
	}
}

// TestStabilityFromTopologyVariance — direct unit test of the loose-cluster
// stability mapping. Bounds, edge cases, monotonic decline.
func TestStabilityFromTopologyVariance(t *testing.T) {
	cases := []struct {
		freq, distinct int
		want           float64
	}{
		{0, 0, 0.0},  // degenerate
		{1, 1, 1.0},  // single match, single topology
		{5, 1, 1.0},  // 5 matches, all same topology → maxed
		{4, 2, 1.0 - 1.0/3.0},
		{4, 3, 1.0 - 2.0/3.0},
		{4, 4, 0.1}, // every member has a different topology → floor
		{10, 10, 0.1},
		{1, 5, 0.1}, // pathological (more topologies than frames) → floor
	}
	for _, c := range cases {
		got := stabilityFromTopologyVariance(c.freq, c.distinct)
		if diff := got - c.want; diff < -0.01 || diff > 0.01 {
			t.Errorf("stabilityFromTopologyVariance(%d, %d) = %v; want ~%v",
				c.freq, c.distinct, got, c.want)
		}
	}
}

// TestComputeAtomReuseRate — direct unit test of the helper.
func TestComputeAtomReuseRate(t *testing.T) {
	cases := map[string]float64{
		``:                                 0,
		`[]`:                               0,
		`not-valid-json`:                   0,
		`[{"atom_slug":"a"}]`:              1.0,
		`[{"atom_slug":""}]`:               0.0,
		`[{"atom_slug":"a"},{"atom_slug":"b"}]`: 1.0,
		`[{"atom_slug":"a"},{}]`:           0.5,
		`[{},{},{}]`:                       0.0,
	}
	for in, want := range cases {
		if got := computeAtomReuseRate(in); got != want {
			t.Errorf("computeAtomReuseRate(%q) = %v; want %v", in, got, want)
		}
	}
}
