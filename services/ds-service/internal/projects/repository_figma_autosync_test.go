package projects

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestUpsertAutoSyncState_NewRowThenIdempotent(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	state := AutoSyncState{
		FileKey: "fk-1", PageID: "0:1", SectionID: "3:7",
		ContentHash:       "abc123",
		PositionHash:      "pos456",
		LastSyncedFlowID:  "flow-uuid",
		LastSyncedVersionID: "version-uuid",
		LastAttemptStatus: "ok",
	}
	if err := repo.UpsertAutoSyncState(ctx, state); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := repo.LookupAutoSyncState(ctx, "fk-1", "0:1", "3:7")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if got.ContentHash != "abc123" || got.LastAttemptStatus != "ok" || got.LastSyncedFlowID != "flow-uuid" {
		t.Fatalf("unexpected: %+v", got)
	}
	if got.LastSyncedAt.IsZero() {
		t.Errorf("last_synced_at should be set on status='ok' upsert")
	}

	// Re-upsert with same content — should be idempotent.
	if err := repo.UpsertAutoSyncState(ctx, state); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	got2, err := repo.LookupAutoSyncState(ctx, "fk-1", "0:1", "3:7")
	if err != nil {
		t.Fatalf("lookup 2: %v", err)
	}
	if got2.LastSyncedFlowID != got.LastSyncedFlowID {
		t.Errorf("flow_id changed on re-upsert")
	}
}

func TestUpsertAutoSyncState_ErrorPreservesPriorFlowID(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	// First a successful sync.
	if err := repo.UpsertAutoSyncState(ctx, AutoSyncState{
		FileKey: "fk-1", PageID: "0:1", SectionID: "3:7",
		ContentHash: "hash-v1", LastSyncedFlowID: "flow-A",
		LastSyncedVersionID: "version-A", LastAttemptStatus: "ok",
	}); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	// Then an error attempt — no flow_id/version_id provided.
	if err := repo.UpsertAutoSyncState(ctx, AutoSyncState{
		FileKey: "fk-1", PageID: "0:1", SectionID: "3:7",
		ContentHash: "hash-v2", LastAttemptStatus: "error",
		ErrorMessage: "Figma 429",
	}); err != nil {
		t.Fatalf("error upsert: %v", err)
	}

	got, _ := repo.LookupAutoSyncState(ctx, "fk-1", "0:1", "3:7")
	if got.LastSyncedFlowID != "flow-A" {
		t.Errorf("flow_id should be preserved on error, got %q", got.LastSyncedFlowID)
	}
	if got.LastSyncedVersionID != "version-A" {
		t.Errorf("version_id should be preserved on error, got %q", got.LastSyncedVersionID)
	}
	if got.LastAttemptStatus != "error" {
		t.Errorf("status should reflect latest attempt, got %q", got.LastAttemptStatus)
	}
	if got.ErrorMessage != "Figma 429" {
		t.Errorf("error_message: got %q", got.ErrorMessage)
	}
}

func TestLookupAutoSyncState_NotFound(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	_, err := repo.LookupAutoSyncState(context.Background(), "missing", "p", "s")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestListAutoSyncState_FilterByStatus(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	for i, st := range []AutoSyncState{
		{FileKey: "fk", PageID: "p", SectionID: "s1", LastAttemptStatus: "ok"},
		{FileKey: "fk", PageID: "p", SectionID: "s2", LastAttemptStatus: "error", ErrorMessage: "boom"},
		{FileKey: "fk", PageID: "p", SectionID: "s3", LastAttemptStatus: "quarantined", SkipReason: "project_unmapped"},
	} {
		if err := repo.UpsertAutoSyncState(ctx, st); err != nil {
			t.Fatalf("upsert %d: %v", i, err)
		}
	}

	errs, err := repo.ListAutoSyncState(ctx, AutoSyncStateFilter{Status: "error"})
	if err != nil || len(errs) != 1 || errs[0].SectionID != "s2" {
		t.Fatalf("error filter: %+v err=%v", errs, err)
	}
	quar, err := repo.ListAutoSyncState(ctx, AutoSyncStateFilter{SkipReason: "project_unmapped"})
	if err != nil || len(quar) != 1 || quar[0].SectionID != "s3" {
		t.Fatalf("skip_reason filter: %+v err=%v", quar, err)
	}
	all, err := repo.ListAutoSyncState(ctx, AutoSyncStateFilter{FileKey: "fk"})
	if err != nil || len(all) != 3 {
		t.Fatalf("file_key filter: got %d rows err=%v", len(all), err)
	}
}

func TestAutoSyncState_TenantIsolation(t *testing.T) {
	d, tA, tB, _ := newTestDB(t)
	repoA := NewTenantRepo(d.DB, tA)
	repoB := NewTenantRepo(d.DB, tB)
	ctx := context.Background()

	if err := repoA.UpsertAutoSyncState(ctx, AutoSyncState{
		FileKey: "shared-fk", PageID: "p", SectionID: "s",
		ContentHash: "A-hash", LastAttemptStatus: "ok",
	}); err != nil {
		t.Fatalf("A upsert: %v", err)
	}

	if _, err := repoB.LookupAutoSyncState(ctx, "shared-fk", "p", "s"); !errors.Is(err, ErrNotFound) {
		t.Errorf("B should not see A's state, got err=%v", err)
	}
	rowsB, _ := repoB.ListAutoSyncState(ctx, AutoSyncStateFilter{})
	if len(rowsB) != 0 {
		t.Errorf("B's list should be empty, got %d rows", len(rowsB))
	}
}

func TestClearAutoSyncQuarantine_ResetsQuarantinedRow(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	// Drive a section into auto-quarantine via 5 consecutive 'error' upserts
	// (matches the UPSERT's automatic threshold transition).
	const fk, pg, sec = "fk-cq", "p", "s"
	for i := 0; i < AutoSyncMaxRetries; i++ {
		if err := repo.UpsertAutoSyncState(ctx, AutoSyncState{
			FileKey: fk, PageID: pg, SectionID: sec,
			LastAttemptStatus: "error", ErrorMessage: "Figma 500",
		}); err != nil {
			t.Fatalf("upsert %d: %v", i, err)
		}
	}
	got, _ := repo.LookupAutoSyncState(ctx, fk, pg, sec)
	if got.LastAttemptStatus != "quarantined" {
		t.Fatalf("setup: row should be quarantined, got status=%q", got.LastAttemptStatus)
	}

	// Clear it.
	cleared, err := repo.ClearAutoSyncQuarantine(ctx, fk, pg, sec)
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if !cleared {
		t.Fatalf("cleared: got false, want true")
	}

	got, _ = repo.LookupAutoSyncState(ctx, fk, pg, sec)
	if got.LastAttemptStatus != "" {
		t.Errorf("last_attempt_status: got %q want empty", got.LastAttemptStatus)
	}
	if got.RetryCount != 0 {
		t.Errorf("retry_count: got %d want 0", got.RetryCount)
	}
	if !got.QuarantinedAt.IsZero() {
		t.Errorf("quarantined_at: got %v want zero", got.QuarantinedAt)
	}
	if got.SkipReason != "" {
		t.Errorf("skip_reason: got %q want empty", got.SkipReason)
	}
}

// #11 audit fix: clearing a non-quarantined row is a no-op and returns
// (false, nil) so the handler 404s. Prior behavior unconditionally
// wiped state of any matching row, which silently corrupted ok/error
// rows on operator misclick.
func TestClearAutoSyncQuarantine_NoOpOnHealthyRow(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	if err := repo.UpsertAutoSyncState(ctx, AutoSyncState{
		FileKey: "fk", PageID: "p", SectionID: "s",
		ContentHash: "h", LastSyncedVersionID: "v", LastAttemptStatus: "ok",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cleared, err := repo.ClearAutoSyncQuarantine(ctx, "fk", "p", "s")
	if err != nil {
		t.Fatalf("clear err: %v", err)
	}
	if cleared {
		t.Fatalf("cleared: got true, want false (row was ok, not quarantined)")
	}
	// Sanity: the row's content_hash + last_attempt_status survived intact.
	got, err := repo.LookupAutoSyncState(ctx, "fk", "p", "s")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if got.LastAttemptStatus != "ok" {
		t.Errorf("status corrupted: got %q want ok", got.LastAttemptStatus)
	}
	if got.ContentHash != "h" {
		t.Errorf("content_hash wiped: got %q want h", got.ContentHash)
	}
}

// Missing-row case: returns (false, nil) so the handler can 404.
func TestClearAutoSyncQuarantine_MissingRowReturnsFalse(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	cleared, err := repo.ClearAutoSyncQuarantine(context.Background(), "missing", "p", "s")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if cleared {
		t.Errorf("expected cleared=false on missing row, got true")
	}
}

func TestUpsertFigmaProjectMapping_Roundtrip(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	if err := repo.UpsertFigmaProjectMapping(ctx, FigmaProjectMapping{
		ProjectID:          "p-1",
		Domain:             "Markets",
		Product:            "Indian Stocks",
		PlatformDefault:    "mobile",
		EnabledForAutosync: true,
		MappedByUserID:     uA,
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := repo.LookupFigmaProjectMapping(ctx, "p-1")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if got.Domain != "Markets" || got.Product != "Indian Stocks" || got.PlatformDefault != "mobile" {
		t.Errorf("unexpected: %+v", got)
	}
	if !got.EnabledForAutosync {
		t.Errorf("should be enabled")
	}

	// Update — flip enabled flag.
	if err := repo.UpsertFigmaProjectMapping(ctx, FigmaProjectMapping{
		ProjectID:          "p-1",
		Domain:             "Markets",
		Product:            "Indian Stocks",
		PlatformDefault:    "mobile",
		EnabledForAutosync: false,
		MappedByUserID:     uA,
	}); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	got2, _ := repo.LookupFigmaProjectMapping(ctx, "p-1")
	if got2.EnabledForAutosync {
		t.Errorf("should be disabled after re-upsert with flag=false")
	}
}

func TestUpsertFigmaProjectMapping_DefaultsPlatformToUnspecified(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	if err := repo.UpsertFigmaProjectMapping(context.Background(), FigmaProjectMapping{
		ProjectID: "p-1", Domain: "X", Product: "Y", MappedByUserID: uA, EnabledForAutosync: true,
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, _ := repo.LookupFigmaProjectMapping(context.Background(), "p-1")
	if got.PlatformDefault != "unspecified" {
		t.Errorf("default platform: got %q want unspecified", got.PlatformDefault)
	}
}

func TestUpsertFigmaProjectMapping_Validation(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	cases := []FigmaProjectMapping{
		{Domain: "X", Product: "Y", MappedByUserID: uA},                                  // missing project_id
		{ProjectID: "p", Product: "Y", MappedByUserID: uA},                               // missing domain
		{ProjectID: "p", Domain: "X", MappedByUserID: uA},                                // missing product
		{ProjectID: "p", Domain: "X", Product: "Y"},                                      // missing user
	}
	for i, c := range cases {
		if err := repo.UpsertFigmaProjectMapping(ctx, c); err == nil {
			t.Errorf("case %d should have errored, got nil", i)
		}
	}
}

func TestListFigmaProjectMappings_OrderedByProductDomain(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	inputs := []FigmaProjectMapping{
		{ProjectID: "p-3", Domain: "Markets", Product: "US Stocks", MappedByUserID: uA, EnabledForAutosync: true},
		{ProjectID: "p-1", Domain: "Markets", Product: "Indian Stocks", MappedByUserID: uA, EnabledForAutosync: true},
		{ProjectID: "p-2", Domain: "Money matters", Product: "Bonds", MappedByUserID: uA, EnabledForAutosync: true},
	}
	for _, m := range inputs {
		if err := repo.UpsertFigmaProjectMapping(ctx, m); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}
	got, err := repo.ListFigmaProjectMappings(ctx)
	if err != nil || len(got) != 3 {
		t.Fatalf("list: %+v err=%v", got, err)
	}
	// Ordered by product ASC: Bonds < Indian Stocks < US Stocks.
	if got[0].Product != "Bonds" || got[1].Product != "Indian Stocks" || got[2].Product != "US Stocks" {
		t.Errorf("ordering: got %s, %s, %s", got[0].Product, got[1].Product, got[2].Product)
	}
}

// Suppress unused-package lints when only running a subset of tests.
var _ = time.Time{}

// ─── LoadSectionSubtree (plan 002 U5) ────────────────────────────────────────

// seedSection writes one figma_section row (no page row, no FKs from page).
// Returns nothing — caller uses LoadSectionSubtree to read back.
func seedSection(t *testing.T, repo *TenantRepo, fileKey, pageID, sectionID string, subtree []FigmaNodeRow) {
	t.Helper()
	ctx := context.Background()
	pages := []FigmaPageRow{{FileKey: fileKey, PageID: pageID, Name: "Page", OrderIndex: 0}}
	sections := []FigmaSectionRow{{FileKey: fileKey, PageID: pageID, SectionID: sectionID, Name: "Sec", OrderIndex: 0}}
	subtrees := map[string][]FigmaNodeRow{sectionID: subtree}
	if subtree == nil {
		subtrees = nil
	}
	if _, _, err := repo.UpsertFigmaPagesAndSections(ctx, fileKey, pages, sections, subtrees, time.Now().UTC()); err != nil {
		t.Fatalf("seed section: %v", err)
	}
}

func TestLoadSectionSubtree_HappyRoundTrip(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()
	subtree := []FigmaNodeRow{
		{NodeID: "10:1", NodeType: "SECTION", Name: "Wallet/Main", HasBBox: true, X: 0, Y: 0, Width: 1200, Height: 800, Depth: 2},
		{NodeID: "10:2", ParentID: "10:1", NodeType: "FRAME", Name: "Hero", HasBBox: true, X: 16, Y: 16, Width: 343, Height: 56, Depth: 3, OrderIndex: 0},
		{NodeID: "10:3", ParentID: "10:2", NodeType: "INSTANCE", Name: "Left Icon/Default", HasBBox: true, X: 16, Y: 16, Width: 24, Height: 24, Depth: 4, OrderIndex: 0, ComponentID: "229:4715"},
	}
	seedSection(t, repo, "fk-A", "0:1", "10:1", subtree)
	got, err := repo.LoadSectionSubtree(ctx, "fk-A", "10:1")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != len(subtree) {
		t.Fatalf("len: got %d, want %d", len(got), len(subtree))
	}
	// Each row should match input + carry tenant + file_key stamped by reader.
	for i := range subtree {
		want := subtree[i]
		want.TenantID = tA
		want.FileKey = "fk-A"
		if got[i] != want {
			t.Errorf("row %d:\n  got:  %+v\n  want: %+v", i, got[i], want)
		}
	}
}

func TestLoadSectionSubtree_NullBlobReturnsErrNotFound(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()
	// Seed section row WITHOUT a subtree (nil map) — blob is NULL.
	seedSection(t, repo, "fk-B", "0:1", "10:9", nil)
	_, err := repo.LoadSectionSubtree(ctx, "fk-B", "10:9")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound for NULL blob; got %v", err)
	}
}

func TestLoadSectionSubtree_MissingRowReturnsErrNotFound(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()
	_, err := repo.LoadSectionSubtree(ctx, "fk-unknown", "10:nope")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound for missing row; got %v", err)
	}
}

func TestLoadSectionSubtree_TenantIsolation(t *testing.T) {
	d, tA, tB, _ := newTestDB(t)
	repoA := NewTenantRepo(d.DB, tA)
	repoB := NewTenantRepo(d.DB, tB)
	ctx := context.Background()
	subtree := []FigmaNodeRow{
		{NodeID: "10:1", NodeType: "SECTION", Name: "Sec", Depth: 2},
		{NodeID: "10:2", ParentID: "10:1", NodeType: "FRAME", Name: "F", Depth: 3},
	}
	seedSection(t, repoA, "fk-X", "0:1", "10:1", subtree)
	_, err := repoB.LoadSectionSubtree(ctx, "fk-X", "10:1")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("tenant B should not see tenant A's subtree; got err=%v", err)
	}
}

func TestLoadSectionSubtree_EmptyTenantID(t *testing.T) {
	d, _, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, "") // explicit empty tenant
	_, err := repo.LoadSectionSubtree(context.Background(), "fk-A", "10:1")
	if err == nil {
		t.Errorf("expected error on empty tenant_id")
	}
}

// ─── UpsertFigmaNodeMetadata (mig 0034, plan 2026-05-17-004 U5) ──────────────

// fkmCountRows returns the row count in figma_node_metadata for the given
// tenant + file. Used by the U5 tests to verify upsert behavior.
func fkmCountRows(t *testing.T, repo *TenantRepo, fileKey string) int {
	t.Helper()
	var n int
	err := repo.r.db.QueryRow(
		`SELECT COUNT(*) FROM figma_node_metadata WHERE tenant_id = ? AND file_key = ?`,
		repo.tenantID, fileKey,
	).Scan(&n)
	if err != nil {
		t.Fatalf("count rows: %v", err)
	}
	return n
}

func TestUpsertFigmaNodeMetadata_HappyPath(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	rows := []FigmaNodeMetadataRow{
		{
			PageID: "0:1", SectionID: "10:1", NodeID: "100:1", ParentID: "10:1",
			Depth: 1, OrderIndex: 0, NodeType: "FRAME", Name: "Hero",
			HasBBox: true, AbsX: 0, AbsY: 0, Width: 320, Height: 240,
			LayoutMode: "VERTICAL",
		},
		{
			PageID: "0:1", SectionID: "10:1", NodeID: "100:2", ParentID: "10:1",
			Depth: 1, OrderIndex: 1, NodeType: "INSTANCE", Name: "Card",
			HasBBox: true, AbsX: 0, AbsY: 260, Width: 320, Height: 120,
			ComponentID: "comp-master-9",
		},
	}
	n, err := repo.UpsertFigmaNodeMetadata(ctx, "fk-1", rows)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if n != 2 {
		t.Errorf("written: got %d want 2", n)
	}
	if got := fkmCountRows(t, repo, "fk-1"); got != 2 {
		t.Errorf("db rows: got %d want 2", got)
	}
}

func TestUpsertFigmaNodeMetadata_IdempotentOnConflict(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	row := FigmaNodeMetadataRow{
		PageID: "0:1", SectionID: "10:1", NodeID: "100:1", ParentID: "10:1",
		Depth: 1, OrderIndex: 0, NodeType: "FRAME", Name: "Hero",
		HasBBox: true, AbsX: 0, AbsY: 0, Width: 320, Height: 240,
	}
	if _, err := repo.UpsertFigmaNodeMetadata(ctx, "fk-1", []FigmaNodeMetadataRow{row}); err != nil {
		t.Fatalf("upsert 1: %v", err)
	}
	// Re-upsert with a new name and new dimensions → should UPDATE, not INSERT.
	row.Name = "Hero v2"
	row.Width = 480
	if _, err := repo.UpsertFigmaNodeMetadata(ctx, "fk-1", []FigmaNodeMetadataRow{row}); err != nil {
		t.Fatalf("upsert 2: %v", err)
	}
	if got := fkmCountRows(t, repo, "fk-1"); got != 1 {
		t.Errorf("row count after re-upsert: got %d want 1 (UPSERT, not INSERT)", got)
	}
	// Confirm the update landed.
	var name string
	var width float64
	err := repo.r.db.QueryRow(
		`SELECT name, width FROM figma_node_metadata
		  WHERE tenant_id = ? AND file_key = ? AND node_id = ?`,
		tA, "fk-1", "100:1",
	).Scan(&name, &width)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if name != "Hero v2" || width != 480 {
		t.Errorf("update did not persist: name=%q width=%v", name, width)
	}
}

func TestUpsertFigmaNodeMetadata_TenantIsolation(t *testing.T) {
	d, tA, tB, _ := newTestDB(t)
	repoA := NewTenantRepo(d.DB, tA)
	repoB := NewTenantRepo(d.DB, tB)
	ctx := context.Background()

	row := FigmaNodeMetadataRow{
		PageID: "0:1", SectionID: "10:1", NodeID: "100:1", ParentID: "10:1",
		Depth: 1, OrderIndex: 0, NodeType: "FRAME", Name: "A's frame",
		HasBBox: true, AbsX: 0, AbsY: 0, Width: 100, Height: 100,
	}
	if _, err := repoA.UpsertFigmaNodeMetadata(ctx, "fk-shared", []FigmaNodeMetadataRow{row}); err != nil {
		t.Fatalf("upsert A: %v", err)
	}
	if got := fkmCountRows(t, repoA, "fk-shared"); got != 1 {
		t.Errorf("tenant A rows: got %d want 1", got)
	}
	if got := fkmCountRows(t, repoB, "fk-shared"); got != 0 {
		t.Errorf("tenant B should not see A's row; got %d", got)
	}
}

func TestUpsertFigmaNodeMetadata_NoBBoxWritesNulls(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()
	rows := []FigmaNodeMetadataRow{
		{
			PageID: "0:1", SectionID: "10:1", NodeID: "100:1", ParentID: "10:1",
			Depth: 1, OrderIndex: 0, NodeType: "INSTANCE", Name: "swap-stub",
			HasBBox: false, // Figma sometimes omits bbox on INSTANCE swap stubs
		},
	}
	if _, err := repo.UpsertFigmaNodeMetadata(ctx, "fk-1", rows); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	var hasBBox int
	var absX, absY, w, h *float64
	err := repo.r.db.QueryRow(
		`SELECT has_bbox, abs_x, abs_y, width, height FROM figma_node_metadata
		  WHERE tenant_id = ? AND node_id = ?`, tA, "100:1",
	).Scan(&hasBBox, &absX, &absY, &w, &h)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if hasBBox != 0 {
		t.Errorf("has_bbox: got %d want 0", hasBBox)
	}
	if absX != nil || absY != nil || w != nil || h != nil {
		t.Errorf("bbox coords should be NULL: x=%v y=%v w=%v h=%v", absX, absY, w, h)
	}
}

func TestUpsertFigmaNodeMetadata_EmptyRows(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	n, err := repo.UpsertFigmaNodeMetadata(context.Background(), "fk", nil)
	if err != nil {
		t.Errorf("empty rows should be no-op, got err: %v", err)
	}
	if n != 0 {
		t.Errorf("written: got %d want 0", n)
	}
}

func TestUpsertFigmaNodeMetadata_Validation(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	if _, err := repo.UpsertFigmaNodeMetadata(context.Background(), "", []FigmaNodeMetadataRow{{NodeID: "1"}}); err == nil {
		t.Errorf("empty file_key should error")
	}
	emptyRepo := NewTenantRepo(d.DB, "")
	if _, err := emptyRepo.UpsertFigmaNodeMetadata(context.Background(), "fk", []FigmaNodeMetadataRow{{NodeID: "1"}}); err == nil {
		t.Errorf("empty tenant should error")
	}
}
