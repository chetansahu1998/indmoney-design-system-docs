package projects

import (
	"context"
	"errors"
	"testing"
	"time"
)

// repository_organism_test.go — exercises the TenantRepo organism-detection
// methods added in U6 against a real SQLite DB seeded with the 0024
// migration. Uses the newTestDB helper from repository_test.go.

// orgTestFixture wraps the boilerplate of seeding a project + flow + version
// + screen so each test can write/read organism rows against real FK targets.
type orgTestFixture struct {
	repo      *TenantRepo
	otherRepo *TenantRepo // a second tenant for isolation checks
	projectID string      // for tests that need to create additional flows/versions
	versionID string
	screenID  string
	tenantA   string
	tenantB   string
	userID    string // captured from newTestDB so additional CreateVersion calls satisfy FK
}

func seedOrgFixture(t *testing.T) orgTestFixture {
	t.Helper()
	d, tA, tB, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	other := NewTenantRepo(d.DB, tB)

	p, err := repo.UpsertProject(context.Background(), Project{
		Name: "OrgTest", Platform: "mobile", Product: "Indian Stocks",
		Path: "Tests", OwnerUserID: uA,
	})
	if err != nil {
		t.Fatalf("upsert project: %v", err)
	}
	v, err := repo.CreateVersion(context.Background(), p.ID, uA)
	if err != nil {
		t.Fatalf("create version: %v", err)
	}
	flow, err := repo.UpsertFlow(context.Background(), Flow{
		ProjectID: p.ID, FileID: "file-1", Name: "Test Flow",
	})
	if err != nil {
		t.Fatalf("upsert flow: %v", err)
	}
	screens := []Screen{
		{VersionID: v.ID, FlowID: flow.ID, X: 0, Y: 0, Width: 375, Height: 812},
	}
	if err := repo.InsertScreens(context.Background(), screens); err != nil {
		t.Fatalf("insert screens: %v", err)
	}
	return orgTestFixture{
		repo: repo, otherRepo: other,
		projectID: p.ID,
		versionID: v.ID, screenID: screens[0].ID,
		tenantA: tA, tenantB: tB,
		userID: uA,
	}
}

// TestUpsertOrganismMatches_RoundTrip — write 3 rows, list them back, all
// fields preserved including JSON columns and the nullable ParentFrameID.
func TestUpsertOrganismMatches_RoundTrip(t *testing.T) {
	fx := seedOrgFixture(t)
	ctx := context.Background()
	rows := []DetectedOrganismMatch{
		{
			VersionID: fx.versionID, FrameID: "1454:194509", ScreenID: fx.screenID,
			SuspectedSlug: "list-on-surface", SuspectedVariantKey: "li=yes,ri=yes,rt=yes",
			MatchKind: "near", FingerprintHash: "abc111",
			AtomSignatureJSON: `["left-icon-default","right-icon","right-text"]`,
			SlotTopologyJSON:  `[]`, DiffJSON: `[{"kind":"added","atom_slug":"overline"}]`,
			Confidence: 0.85, ManifestHash: "mh1", DetectedAt: time.Now().UTC().Truncate(time.Second),
		},
		{
			VersionID: fx.versionID, FrameID: "1454:194541", ScreenID: fx.screenID,
			MatchKind: "novel", FingerprintHash: "abc222",
			AtomSignatureJSON: `["position-card-header"]`, SlotTopologyJSON: `[]`,
			Confidence: 0.3, ManifestHash: "mh1", DetectedAt: time.Now().UTC().Truncate(time.Second),
		},
		{
			VersionID: fx.versionID, FrameID: "1454:194550", ScreenID: fx.screenID,
			SuspectedSlug: "list-on-surface", MatchKind: "exact",
			FingerprintHash:   "abc333",
			AtomSignatureJSON: `["left-icon-default","right-icon","right-text"]`,
			SlotTopologyJSON:  `[]`,
			Confidence:        1.0, ManifestHash: "mh1",
			ParentFrameID: "1454:194509", DetectedAt: time.Now().UTC().Truncate(time.Second),
		},
	}
	if err := fx.repo.UpsertOrganismMatches(ctx, rows); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := fx.repo.ListOrganismMatchesForVersion(ctx, fx.versionID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 rows; got %d", len(got))
	}

	// Verify the nested one carries ParentFrameID.
	for _, r := range got {
		if r.FrameID == "1454:194550" && r.ParentFrameID != "1454:194509" {
			t.Errorf("nested row missing ParentFrameID: %+v", r)
		}
		if r.TenantID != fx.tenantA {
			t.Errorf("TenantID not enforced; got %q want %q", r.TenantID, fx.tenantA)
		}
	}
}

// TestUpsertOrganismMatches_Idempotent — running upsert twice on the same
// rows produces identical row count (no duplicates, no FK errors).
func TestUpsertOrganismMatches_Idempotent(t *testing.T) {
	fx := seedOrgFixture(t)
	ctx := context.Background()
	row := DetectedOrganismMatch{
		VersionID: fx.versionID, FrameID: "abc", ScreenID: fx.screenID,
		MatchKind: "near", FingerprintHash: "h1",
		AtomSignatureJSON: `[]`, SlotTopologyJSON: `[]`,
		Confidence: 0.7, ManifestHash: "mh", DetectedAt: time.Now(),
	}
	if err := fx.repo.UpsertOrganismMatches(ctx, []DetectedOrganismMatch{row}); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	row.MatchKind = "exact" // mutate one field to verify UPSERT actually writes
	row.Confidence = 1.0
	if err := fx.repo.UpsertOrganismMatches(ctx, []DetectedOrganismMatch{row}); err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	got, err := fx.repo.ListOrganismMatchesForVersion(ctx, fx.versionID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 row after re-upsert; got %d", len(got))
	}
	if got[0].MatchKind != "exact" || got[0].Confidence != 1.0 {
		t.Errorf("expected updated fields; got kind=%q conf=%v", got[0].MatchKind, got[0].Confidence)
	}
}

// TestUpsertOrganismMatches_TenantIsolation — rows written by tenant A are
// invisible to tenant B. R8 requirement.
func TestUpsertOrganismMatches_TenantIsolation(t *testing.T) {
	fx := seedOrgFixture(t)
	ctx := context.Background()
	rowA := DetectedOrganismMatch{
		VersionID: fx.versionID, FrameID: "shared-frame-id", ScreenID: fx.screenID,
		MatchKind: "near", FingerprintHash: "h1",
		AtomSignatureJSON: `[]`, SlotTopologyJSON: `[]`,
		Confidence: 0.7, ManifestHash: "mh",
	}
	if err := fx.repo.UpsertOrganismMatches(ctx, []DetectedOrganismMatch{rowA}); err != nil {
		t.Fatalf("tenant A upsert: %v", err)
	}

	// Tenant B looks for the same frame id — must not see tenant A's row.
	_, err := fx.otherRepo.LookupOrganismMatchByFrame(ctx, "shared-frame-id")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("tenant B leaked tenant A's row; got err=%v", err)
	}

	// Tenant A still sees the row.
	rec, err := fx.repo.LookupOrganismMatchByFrame(ctx, "shared-frame-id")
	if err != nil {
		t.Fatalf("tenant A lost its own row: %v", err)
	}
	if rec.FrameID != "shared-frame-id" {
		t.Errorf("wrong row returned: %+v", rec)
	}
}

// TestLookupOrganismMatchByFrame_NotFound — empty result returns ErrNotFound,
// not nil/nil. Mirrors repository.go convention.
func TestLookupOrganismMatchByFrame_NotFound(t *testing.T) {
	fx := seedOrgFixture(t)
	_, err := fx.repo.LookupOrganismMatchByFrame(context.Background(), "no-such-frame")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound; got %v", err)
	}
}

// TestCountOrganismMatchesByKind — group-by counts the rows correctly.
func TestCountOrganismMatchesByKind(t *testing.T) {
	fx := seedOrgFixture(t)
	ctx := context.Background()
	rows := []DetectedOrganismMatch{
		{VersionID: fx.versionID, FrameID: "f1", ScreenID: fx.screenID, MatchKind: "exact",
			FingerprintHash: "h1", AtomSignatureJSON: "[]", SlotTopologyJSON: "[]",
			Confidence: 1.0, ManifestHash: "mh"},
		{VersionID: fx.versionID, FrameID: "f2", ScreenID: fx.screenID, MatchKind: "near",
			FingerprintHash: "h2", AtomSignatureJSON: "[]", SlotTopologyJSON: "[]",
			Confidence: 0.7, ManifestHash: "mh"},
		{VersionID: fx.versionID, FrameID: "f3", ScreenID: fx.screenID, MatchKind: "near",
			FingerprintHash: "h3", AtomSignatureJSON: "[]", SlotTopologyJSON: "[]",
			Confidence: 0.6, ManifestHash: "mh"},
		{VersionID: fx.versionID, FrameID: "f4", ScreenID: fx.screenID, MatchKind: "novel",
			FingerprintHash: "h4", AtomSignatureJSON: "[]", SlotTopologyJSON: "[]",
			Confidence: 0.3, ManifestHash: "mh"},
	}
	if err := fx.repo.UpsertOrganismMatches(ctx, rows); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	counts, err := fx.repo.CountOrganismMatchesByKind(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if counts["exact"] != 1 || counts["near"] != 2 || counts["novel"] != 1 {
		t.Errorf("unexpected counts: %+v", counts)
	}
}

// TestListOrganismMatchesBySlug — filter by suspected_slug + kind, paginated.
func TestListOrganismMatchesBySlug(t *testing.T) {
	fx := seedOrgFixture(t)
	ctx := context.Background()
	rows := []DetectedOrganismMatch{
		{VersionID: fx.versionID, FrameID: "f1", ScreenID: fx.screenID,
			SuspectedSlug: "list-on-surface", MatchKind: "near",
			FingerprintHash: "h1", AtomSignatureJSON: "[]", SlotTopologyJSON: "[]",
			Confidence: 0.7, ManifestHash: "mh"},
		{VersionID: fx.versionID, FrameID: "f2", ScreenID: fx.screenID,
			SuspectedSlug: "list-on-card", MatchKind: "near",
			FingerprintHash: "h2", AtomSignatureJSON: "[]", SlotTopologyJSON: "[]",
			Confidence: 0.6, ManifestHash: "mh"},
		{VersionID: fx.versionID, FrameID: "f3", ScreenID: fx.screenID,
			SuspectedSlug: "list-on-surface", MatchKind: "exact",
			FingerprintHash: "h3", AtomSignatureJSON: "[]", SlotTopologyJSON: "[]",
			Confidence: 1.0, ManifestHash: "mh"},
	}
	if err := fx.repo.UpsertOrganismMatches(ctx, rows); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// All rows for list-on-surface (any kind).
	all, err := fx.repo.ListOrganismMatchesBySlug(ctx, "list-on-surface", "", 100, 0)
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("expected 2 list-on-surface rows; got %d", len(all))
	}

	// Filter by kind=exact.
	exact, err := fx.repo.ListOrganismMatchesBySlug(ctx, "list-on-surface", "exact", 100, 0)
	if err != nil {
		t.Fatalf("list exact: %v", err)
	}
	if len(exact) != 1 || exact[0].FrameID != "f3" {
		t.Errorf("expected only f3; got %+v", exact)
	}

	// Pagination
	page1, err := fx.repo.ListOrganismMatchesBySlug(ctx, "list-on-surface", "", 1, 0)
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	page2, err := fx.repo.ListOrganismMatchesBySlug(ctx, "list-on-surface", "", 1, 1)
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}
	if len(page1) != 1 || len(page2) != 1 {
		t.Errorf("pagination broken: page1=%d page2=%d", len(page1), len(page2))
	}
	if page1[0].FrameID == page2[0].FrameID {
		t.Errorf("pages must differ; got duplicate %q", page1[0].FrameID)
	}
}

// TestUpsertPromotionCandidates_RoundTrip — write + list-back, ordering by
// composite score honored.
func TestUpsertPromotionCandidates_RoundTrip(t *testing.T) {
	fx := seedOrgFixture(t)
	ctx := context.Background()
	candidates := []PromotionCandidate{
		{FingerprintHash: "low", Frequency: 3, FileCount: 2, StabilityScore: 0.5, AtomReuseRate: 0.5},
		{FingerprintHash: "high", Frequency: 10, FileCount: 5, StabilityScore: 0.9, AtomReuseRate: 0.95},
		{FingerprintHash: "mid", Frequency: 5, FileCount: 3, StabilityScore: 0.7, AtomReuseRate: 0.7},
	}
	if err := fx.repo.UpsertPromotionCandidates(ctx, candidates); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err := fx.repo.ListPromotionCandidates(ctx, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 candidates; got %d", len(got))
	}
	// Order: high, mid, low (descending composite score).
	wantOrder := []string{"high", "mid", "low"}
	for i, w := range wantOrder {
		if got[i].FingerprintHash != w {
			t.Errorf("position %d: got %q want %q", i, got[i].FingerprintHash, w)
		}
	}
}

// TestUpsertPromotionCandidates_ReplaceSet — UPSERT is replace-set semantics:
// re-running with a smaller list removes orphans.
func TestUpsertPromotionCandidates_ReplaceSet(t *testing.T) {
	fx := seedOrgFixture(t)
	ctx := context.Background()
	if err := fx.repo.UpsertPromotionCandidates(ctx, []PromotionCandidate{
		{FingerprintHash: "h1", Frequency: 5, FileCount: 2, StabilityScore: 0.8, AtomReuseRate: 0.7},
		{FingerprintHash: "h2", Frequency: 4, FileCount: 2, StabilityScore: 0.6, AtomReuseRate: 0.5},
		{FingerprintHash: "h3", Frequency: 3, FileCount: 2, StabilityScore: 0.5, AtomReuseRate: 0.4},
	}); err != nil {
		t.Fatalf("first: %v", err)
	}
	// Re-run with only h1 — h2 and h3 should be removed.
	if err := fx.repo.UpsertPromotionCandidates(ctx, []PromotionCandidate{
		{FingerprintHash: "h1", Frequency: 7, FileCount: 3, StabilityScore: 0.9, AtomReuseRate: 0.8},
	}); err != nil {
		t.Fatalf("second: %v", err)
	}
	got, err := fx.repo.ListPromotionCandidates(ctx, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 || got[0].FingerprintHash != "h1" || got[0].Frequency != 7 {
		t.Errorf("replace-set failed; got %+v", got)
	}
}

// TestUpsertPromotionCandidates_EmptyClears — passing zero-length slice
// removes all rows for the tenant.
func TestUpsertPromotionCandidates_EmptyClears(t *testing.T) {
	fx := seedOrgFixture(t)
	ctx := context.Background()
	if err := fx.repo.UpsertPromotionCandidates(ctx, []PromotionCandidate{
		{FingerprintHash: "h1", Frequency: 5, FileCount: 2, StabilityScore: 0.8, AtomReuseRate: 0.7},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := fx.repo.UpsertPromotionCandidates(ctx, []PromotionCandidate{}); err != nil {
		t.Fatalf("clear: %v", err)
	}
	got, err := fx.repo.ListPromotionCandidates(ctx, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty after clear; got %d rows", len(got))
	}
}

// TestPromotionCandidate_DismissedHidden — dismissed candidates don't appear
// in the default list.
func TestPromotionCandidate_DismissedHidden(t *testing.T) {
	fx := seedOrgFixture(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	if err := fx.repo.UpsertPromotionCandidates(ctx, []PromotionCandidate{
		{FingerprintHash: "active", Frequency: 5, FileCount: 2, StabilityScore: 0.8, AtomReuseRate: 0.7},
		{FingerprintHash: "hidden", Frequency: 5, FileCount: 2, StabilityScore: 0.8, AtomReuseRate: 0.7,
			DismissedAt: now},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	got, err := fx.repo.ListPromotionCandidates(ctx, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 || got[0].FingerprintHash != "active" {
		t.Errorf("expected only 'active'; got %+v", got)
	}
}

// TestPromotionCandidate_TenantIsolation — tenant B can't see tenant A's
// candidates.
func TestPromotionCandidate_TenantIsolation(t *testing.T) {
	fx := seedOrgFixture(t)
	ctx := context.Background()
	if err := fx.repo.UpsertPromotionCandidates(ctx, []PromotionCandidate{
		{FingerprintHash: "tenant-a-secret", Frequency: 5, FileCount: 2,
			StabilityScore: 0.8, AtomReuseRate: 0.7},
	}); err != nil {
		t.Fatalf("seed A: %v", err)
	}
	got, err := fx.otherRepo.ListPromotionCandidates(ctx, 0)
	if err != nil {
		t.Fatalf("list B: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("tenant B saw tenant A rows: %+v", got)
	}
}

// TestUpsertOrganismMatches_RejectsMissingRequiredFields — input validation.
func TestUpsertOrganismMatches_RejectsMissingRequiredFields(t *testing.T) {
	fx := seedOrgFixture(t)
	ctx := context.Background()
	bad := DetectedOrganismMatch{
		// VersionID intentionally empty
		FrameID: "f1", ScreenID: fx.screenID, MatchKind: "near",
		FingerprintHash: "h", AtomSignatureJSON: "[]", SlotTopologyJSON: "[]",
		Confidence: 0.5, ManifestHash: "mh",
	}
	err := fx.repo.UpsertOrganismMatches(ctx, []DetectedOrganismMatch{bad})
	if err == nil {
		t.Error("expected validation error for missing version_id")
	}
}
