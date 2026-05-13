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
