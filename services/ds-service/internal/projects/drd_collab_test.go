package projects

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

// Phase 5 U1 — DRD collab repo helpers.

func TestRepo_LoadYDocState_NoSnapshotReturnsNil(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	versionID, _ := seedFlowAndScreens(t, repo, uA)
	var flowID string
	if err := d.DB.QueryRow(`SELECT flow_id FROM screens WHERE version_id = ? LIMIT 1`, versionID).Scan(&flowID); err != nil {
		t.Fatalf("flow_id: %v", err)
	}
	state, err := repo.LoadYDocState(context.Background(), flowID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if state != nil {
		t.Errorf("expected nil for never-snapshotted flow, got %d bytes", len(state))
	}
}

func TestRepo_PersistYDocSnapshot_RoundTrip(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	versionID, _ := seedFlowAndScreens(t, repo, uA)
	var flowID string
	if err := d.DB.QueryRow(`SELECT flow_id FROM screens WHERE version_id = ? LIMIT 1`, versionID).Scan(&flowID); err != nil {
		t.Fatalf("flow_id: %v", err)
	}
	payload := []byte{0xff, 0xfe, 0xfd, 0x01, 0x02, 0x03}

	rev, err := repo.PersistYDocSnapshot(context.Background(), flowID, uA, payload)
	if err != nil {
		t.Fatalf("persist: %v", err)
	}
	if rev != 1 {
		t.Errorf("expected rev=1 on first persist, got %d", rev)
	}

	got, err := repo.LoadYDocState(context.Background(), flowID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != len(payload) {
		t.Fatalf("expected %d bytes, got %d", len(payload), len(got))
	}
	for i, b := range payload {
		if got[i] != b {
			t.Errorf("byte[%d] mismatch: %x vs %x", i, got[i], b)
		}
	}

	// Second persist bumps revision.
	rev2, err := repo.PersistYDocSnapshot(context.Background(), flowID, uA, payload)
	if err != nil {
		t.Fatalf("persist 2: %v", err)
	}
	if rev2 != 2 {
		t.Errorf("expected rev=2 on second persist, got %d", rev2)
	}
}

func TestRepo_PersistYDocSnapshot_RejectsOversize(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	versionID, _ := seedFlowAndScreens(t, repo, uA)
	var flowID string
	if err := d.DB.QueryRow(`SELECT flow_id FROM screens WHERE version_id = ? LIMIT 1`, versionID).Scan(&flowID); err != nil {
		t.Fatalf("flow_id: %v", err)
	}
	huge := make([]byte, MaxYDocBytes+1)
	if _, err := repo.PersistYDocSnapshot(context.Background(), flowID, uA, huge); err == nil {
		t.Errorf("expected error for oversize ydoc")
	}
}

func TestRepo_PersistYDocSnapshot_CrossTenantNotFound(t *testing.T) {
	d, tA, tB, uA := newTestDB(t)
	repoA := NewTenantRepo(d.DB, tA)
	versionID, _ := seedFlowAndScreens(t, repoA, uA)
	var flowID string
	if err := d.DB.QueryRow(`SELECT flow_id FROM screens WHERE version_id = ? LIMIT 1`, versionID).Scan(&flowID); err != nil {
		t.Fatalf("flow_id: %v", err)
	}
	repoB := NewTenantRepo(d.DB, tB)
	_, err := repoB.PersistYDocSnapshot(context.Background(), flowID, uA, []byte{0x01})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// ─── Phase MCP/U3 — characterization tests + sub_flow_id–keyed access ───────

// TestPersistYDocSnapshot_Characterization locks in the current contract for
// PersistYDocSnapshot before U3 adds the nullable sub_flow_id column. Any
// silent regression in YDoc serialization (bytes change, revision skips,
// timestamps stop bumping) surfaces here first.
//
// Observed contract today (mig 0001 + 0008 + 0015):
//   - PersistYDocSnapshot is an upsert (INSERT ... ON CONFLICT DO UPDATE),
//     so calling it on a flow with no existing flow_drd row CREATES the row
//     with revision = 1. There is no ErrFlowDRDNotFound — the only "not
//     found" surface is the assertFlowVisibleByID precheck (cross-tenant
//     or missing flow → ErrNotFound).
//   - Revision starts at 1 on first persist (the literal in the INSERT path)
//     and increments by 1 on each subsequent persist (the ON CONFLICT path).
//   - y_doc_state is overwritten verbatim each call. No diffing.
//   - updated_at and last_snapshot_at are both set to the same now() value
//     on every call. updated_by_user_id is overwritten with the latest
//     caller's user id.
//   - Sizes above MaxYDocBytes (5MB) return an error before any tx opens.
func TestPersistYDocSnapshot_Characterization(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	versionID, _ := seedFlowAndScreens(t, repo, uA)
	var flowID string
	if err := d.DB.QueryRow(`SELECT flow_id FROM screens WHERE version_id = ? LIMIT 1`, versionID).Scan(&flowID); err != nil {
		t.Fatalf("flow_id: %v", err)
	}
	ctx := context.Background()

	// 1. First persist on a flow with no flow_drd row → creates the row,
	//    returns revision 1.
	state1 := make([]byte, 100)
	for i := range state1 {
		state1[i] = byte(i)
	}
	rev1, err := repo.PersistYDocSnapshot(ctx, flowID, uA, state1)
	if err != nil {
		t.Fatalf("persist 1: %v", err)
	}
	if rev1 != 1 {
		t.Errorf("expected revision=1 on first persist, got %d", rev1)
	}

	// Read back via raw SQL to verify all the side-effects we care about.
	var (
		gotState        []byte
		gotRevision     int64
		gotUpdatedAt    string
		gotLastSnapAt   sql.NullString
		gotUpdatedBy    sql.NullString
		gotContentJSON  []byte
	)
	if err := d.DB.QueryRow(
		`SELECT y_doc_state, revision, updated_at, last_snapshot_at, updated_by_user_id, content_json
		   FROM flow_drd WHERE flow_id = ? AND tenant_id = ?`,
		flowID, tA,
	).Scan(&gotState, &gotRevision, &gotUpdatedAt, &gotLastSnapAt, &gotUpdatedBy, &gotContentJSON); err != nil {
		t.Fatalf("readback 1: %v", err)
	}
	if len(gotState) != len(state1) {
		t.Fatalf("y_doc_state length mismatch: got %d want %d", len(gotState), len(state1))
	}
	for i, b := range state1 {
		if gotState[i] != b {
			t.Fatalf("y_doc_state byte[%d]: got %x want %x", i, gotState[i], b)
		}
	}
	if gotRevision != 1 {
		t.Errorf("DB revision=%d, want 1", gotRevision)
	}
	if gotUpdatedAt == "" {
		t.Error("updated_at not set")
	}
	if !gotLastSnapAt.Valid || gotLastSnapAt.String == "" {
		t.Error("last_snapshot_at not set on first persist")
	}
	if !gotUpdatedBy.Valid || gotUpdatedBy.String != uA {
		t.Errorf("updated_by_user_id=%v, want %s", gotUpdatedBy, uA)
	}
	if len(gotContentJSON) == 0 {
		t.Error("content_json should be seeded to empty BlockNote (`{}`)")
	}

	// 2. Second persist with a different payload → revision bumps, state
	//    replaced verbatim.
	state2 := make([]byte, 200)
	for i := range state2 {
		state2[i] = byte(255 - (i % 256))
	}
	rev2, err := repo.PersistYDocSnapshot(ctx, flowID, uA, state2)
	if err != nil {
		t.Fatalf("persist 2: %v", err)
	}
	if rev2 != 2 {
		t.Errorf("expected revision=2 on second persist, got %d", rev2)
	}
	if err := d.DB.QueryRow(
		`SELECT y_doc_state, revision FROM flow_drd WHERE flow_id = ? AND tenant_id = ?`,
		flowID, tA,
	).Scan(&gotState, &gotRevision); err != nil {
		t.Fatalf("readback 2: %v", err)
	}
	if len(gotState) != len(state2) {
		t.Fatalf("y_doc_state length on second persist: got %d want %d", len(gotState), len(state2))
	}
	for i, b := range state2 {
		if gotState[i] != b {
			t.Fatalf("y_doc_state byte[%d] on second persist: got %x want %x", i, gotState[i], b)
		}
	}
	if gotRevision != 2 {
		t.Errorf("DB revision=%d after second persist, want 2", gotRevision)
	}

	// 3. Oversize payload → error.
	huge := make([]byte, MaxYDocBytes+1)
	if _, err := repo.PersistYDocSnapshot(ctx, flowID, uA, huge); err == nil {
		t.Error("expected error for oversize ydoc")
	}

	// 4. Unknown flow_id → ErrNotFound (assertFlowVisibleByID gate).
	if _, err := repo.PersistYDocSnapshot(ctx, "nonexistent-flow-id", uA, []byte{0x01}); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound for unknown flow_id, got %v", err)
	}
}

// seedSubFlowForDRD creates a sub_product + sub_flow pair, returning the
// sub_flow row. Local to this file — the parallel seed helpers in subflow
// tests are typed for that file's harness. Keeping a thin wrapper here
// keeps these U3 tests self-contained.
func seedSubFlowForDRD(t *testing.T, repo *TenantRepo, productName, flowName string) SubFlow {
	t.Helper()
	ctx := context.Background()
	sp, err := repo.UpsertSubProduct(ctx, productName)
	if err != nil {
		t.Fatalf("upsert sub_product: %v", err)
	}
	sf, err := repo.UpsertSubFlow(ctx, sp.ID, flowName)
	if err != nil {
		t.Fatalf("upsert sub_flow: %v", err)
	}
	return sf
}

// flowIDFromSeed extracts the underlying flow_id from a seedFlowAndScreens
// fixture so tests can pass it as the new flow_id argument to
// CreateDRDForSubFlow.
func flowIDFromSeed(t *testing.T, d *sql.DB, versionID string) string {
	t.Helper()
	var flowID string
	if err := d.QueryRow(`SELECT flow_id FROM screens WHERE version_id = ? LIMIT 1`, versionID).Scan(&flowID); err != nil {
		t.Fatalf("flow_id: %v", err)
	}
	return flowID
}

func TestCreateDRDForSubFlow_HappyPath(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	versionID, _ := seedFlowAndScreens(t, repo, uA)
	flowID := flowIDFromSeed(t, d.DB, versionID)
	sf := seedSubFlowForDRD(t, repo, "Wallet", "Cold State")
	ctx := context.Background()

	got, err := repo.CreateDRDForSubFlow(ctx, sf.ID, flowID, uA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if got != flowID {
		t.Errorf("expected flow_id=%s, got %s", flowID, got)
	}

	// Idempotent: second call returns the same flow_id without erroring.
	got2, err := repo.CreateDRDForSubFlow(ctx, sf.ID, flowID, uA)
	if err != nil {
		t.Fatalf("create 2: %v", err)
	}
	if got2 != flowID {
		t.Errorf("expected idempotent flow_id=%s on second call, got %s", flowID, got2)
	}

	// And idempotent with a different (would-be conflicting) flow_id arg:
	// the row already exists, so the second arg is ignored.
	got3, err := repo.CreateDRDForSubFlow(ctx, sf.ID, "some-other-flow-id-that-doesnt-exist", uA)
	if err != nil {
		t.Fatalf("create 3 (idempotent with different flow_id arg): %v", err)
	}
	if got3 != flowID {
		t.Errorf("expected idempotent flow_id=%s, got %s", flowID, got3)
	}
}

func TestLoadYDocStateBySubFlow_HappyPath(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	versionID, _ := seedFlowAndScreens(t, repo, uA)
	flowID := flowIDFromSeed(t, d.DB, versionID)
	sf := seedSubFlowForDRD(t, repo, "INDstocks", "Watchlist")
	ctx := context.Background()

	if _, err := repo.CreateDRDForSubFlow(ctx, sf.ID, flowID, uA); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Before any snapshot, the row exists with NULL y_doc_state → load
	// returns nil + nil error.
	state, err := repo.LoadYDocStateBySubFlow(ctx, sf.ID)
	if err != nil {
		t.Fatalf("load (pre-snapshot): %v", err)
	}
	if state != nil {
		t.Errorf("expected nil state pre-snapshot, got %d bytes", len(state))
	}

	payload := []byte{0xde, 0xad, 0xbe, 0xef, 0x00, 0x42}
	if _, err := repo.PersistYDocSnapshotBySubFlow(ctx, sf.ID, uA, payload); err != nil {
		t.Fatalf("persist: %v", err)
	}

	got, err := repo.LoadYDocStateBySubFlow(ctx, sf.ID)
	if err != nil {
		t.Fatalf("load (post-snapshot): %v", err)
	}
	if len(got) != len(payload) {
		t.Fatalf("len mismatch: got %d want %d", len(got), len(payload))
	}
	for i, b := range payload {
		if got[i] != b {
			t.Errorf("byte[%d] mismatch: %x vs %x", i, got[i], b)
		}
	}
}

func TestPersistYDocSnapshotBySubFlow_RevisionIncrement(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	versionID, _ := seedFlowAndScreens(t, repo, uA)
	flowID := flowIDFromSeed(t, d.DB, versionID)
	sf := seedSubFlowForDRD(t, repo, "Mutual Funds", "SIP")
	ctx := context.Background()

	if _, err := repo.CreateDRDForSubFlow(ctx, sf.ID, flowID, uA); err != nil {
		t.Fatalf("create: %v", err)
	}

	rev1, err := repo.PersistYDocSnapshotBySubFlow(ctx, sf.ID, uA, []byte{0x01, 0x02})
	if err != nil {
		t.Fatalf("persist 1: %v", err)
	}
	if rev1 != 1 {
		t.Errorf("expected rev=1, got %d", rev1)
	}

	rev2, err := repo.PersistYDocSnapshotBySubFlow(ctx, sf.ID, uA, []byte{0x03, 0x04, 0x05})
	if err != nil {
		t.Fatalf("persist 2: %v", err)
	}
	if rev2 != 2 {
		t.Errorf("expected rev=2, got %d", rev2)
	}

	got, err := repo.LoadYDocStateBySubFlow(ctx, sf.ID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 3 || got[0] != 0x03 || got[1] != 0x04 || got[2] != 0x05 {
		t.Errorf("expected second payload, got % x", got)
	}
}

func TestLoadYDocStateBySubFlow_NotFound(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	if _, err := repo.LoadYDocStateBySubFlow(ctx, "nonexistent-sub-flow-id"); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound for unknown sub_flow_id, got %v", err)
	}

	// Persist with no row also surfaces ErrNotFound.
	if _, err := repo.PersistYDocSnapshotBySubFlow(ctx, "nonexistent-sub-flow-id", "u", []byte{0x01}); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound for persist with no row, got %v", err)
	}
}

func TestCreateDRDForSubFlow_PartialUniqueConstraint(t *testing.T) {
	// Two distinct sub_flows both trying to bind to the same flow_id should
	// fail on the second attempt — flow_id is still the PK.
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	versionID, _ := seedFlowAndScreens(t, repo, uA)
	flowID := flowIDFromSeed(t, d.DB, versionID)
	sfA := seedSubFlowForDRD(t, repo, "Wallet", "Activation")
	sfB := seedSubFlowForDRD(t, repo, "Wallet", "Recovery")
	ctx := context.Background()

	if _, err := repo.CreateDRDForSubFlow(ctx, sfA.ID, flowID, uA); err != nil {
		t.Fatalf("first create: %v", err)
	}

	// Second create with a DIFFERENT sub_flow_id but the SAME flow_id —
	// PK collision on flow_id. Caller sees a wrapped SQL error (no
	// idempotent fast-path matches because the sub_flow_id is different).
	_, err := repo.CreateDRDForSubFlow(ctx, sfB.ID, flowID, uA)
	if err == nil {
		t.Fatal("expected error when binding a second sub_flow to the same flow_id")
	}
}

func TestSubFlowID_PartialUniqueIndex(t *testing.T) {
	// Direct raw-SQL test: two flow_drd rows cannot share the same
	// sub_flow_id within a tenant (partial unique index); but multiple
	// rows with NULL sub_flow_id are fine.
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	versionID, _ := seedFlowAndScreens(t, repo, uA)
	flowID1 := flowIDFromSeed(t, d.DB, versionID)
	sf := seedSubFlowForDRD(t, repo, "Tax", "Filing")
	ctx := context.Background()

	if _, err := repo.CreateDRDForSubFlow(ctx, sf.ID, flowID1, uA); err != nil {
		t.Fatalf("first create: %v", err)
	}

	// Insert a second flow row so we can try to bind another flow_drd row.
	p, err := repo.UpsertProject(ctx, Project{
		Name: "P2", Platform: "mobile", Product: "Plutus", Path: "Y", OwnerUserID: uA,
	})
	if err != nil {
		t.Fatalf("upsert project: %v", err)
	}
	v, err := repo.CreateVersion(ctx, p.ID, uA)
	if err != nil {
		t.Fatalf("create version: %v", err)
	}
	f2, err := repo.UpsertFlow(ctx, Flow{ProjectID: p.ID, FileID: "F2", Name: "Flow2"})
	if err != nil {
		t.Fatalf("upsert flow: %v", err)
	}
	if err := repo.InsertScreens(ctx, []Screen{{VersionID: v.ID, FlowID: f2.ID, X: 0, Y: 0, Width: 1, Height: 1}}); err != nil {
		t.Fatalf("insert screens: %v", err)
	}

	// Raw INSERT trying to bind a second flow_drd row to the SAME
	// sub_flow_id → should hit the partial unique index.
	_, err = d.DB.Exec(
		`INSERT INTO flow_drd
		   (flow_id, tenant_id, content_json, revision, schema_version,
		    updated_at, updated_by_user_id, sub_flow_id)
		 VALUES (?, ?, ?, 0, 'test', ?, ?, ?)`,
		f2.ID, tA, []byte(`{}`), "2026-05-17T00:00:00Z", uA, sf.ID,
	)
	if err == nil {
		t.Error("expected UNIQUE constraint error on duplicate sub_flow_id")
	}

	// Two rows with NULL sub_flow_id are allowed by the partial index —
	// the first such row was created by the legacy PersistYDocSnapshot
	// test path, but here we already have one row with sub_flow_id=sf.ID,
	// so insert two NULL-sub_flow_id rows on different flows.
	p3, _ := repo.UpsertProject(ctx, Project{Name: "P3", Platform: "mobile", Product: "Plutus", Path: "Z1", OwnerUserID: uA})
	v3, _ := repo.CreateVersion(ctx, p3.ID, uA)
	f3, _ := repo.UpsertFlow(ctx, Flow{ProjectID: p3.ID, FileID: "F3", Name: "Flow3"})
	_ = repo.InsertScreens(ctx, []Screen{{VersionID: v3.ID, FlowID: f3.ID, X: 0, Y: 0, Width: 1, Height: 1}})
	if _, err := repo.PersistYDocSnapshot(ctx, f3.ID, uA, []byte{0x01}); err != nil {
		t.Fatalf("legacy persist on f3: %v", err)
	}

	p4, _ := repo.UpsertProject(ctx, Project{Name: "P4", Platform: "mobile", Product: "Plutus", Path: "Z2", OwnerUserID: uA})
	v4, _ := repo.CreateVersion(ctx, p4.ID, uA)
	f4, _ := repo.UpsertFlow(ctx, Flow{ProjectID: p4.ID, FileID: "F4", Name: "Flow4"})
	_ = repo.InsertScreens(ctx, []Screen{{VersionID: v4.ID, FlowID: f4.ID, X: 0, Y: 0, Width: 1, Height: 1}})
	if _, err := repo.PersistYDocSnapshot(ctx, f4.ID, uA, []byte{0x02}); err != nil {
		t.Fatalf("legacy persist on f4: %v", err)
	}
	// Both f3 and f4 now have flow_drd rows with NULL sub_flow_id — partial
	// unique index permits this. Spot-check via raw count.
	var n int
	if err := d.DB.QueryRow(
		`SELECT COUNT(*) FROM flow_drd WHERE tenant_id = ? AND sub_flow_id IS NULL`,
		tA,
	).Scan(&n); err != nil {
		t.Fatalf("count NULL rows: %v", err)
	}
	if n < 2 {
		t.Errorf("expected >=2 rows with NULL sub_flow_id, got %d", n)
	}
}

func TestLegacyFlowIDPath_StillWorks(t *testing.T) {
	// Belt-and-suspenders: after adding the sub_flow_id column + methods,
	// every legacy code path must still work unchanged.
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	versionID, _ := seedFlowAndScreens(t, repo, uA)
	flowID := flowIDFromSeed(t, d.DB, versionID)
	ctx := context.Background()

	// Load on a never-snapshotted flow → nil, nil.
	state, err := repo.LoadYDocState(ctx, flowID)
	if err != nil {
		t.Fatalf("legacy load (empty): %v", err)
	}
	if state != nil {
		t.Errorf("expected nil pre-snapshot, got %d bytes", len(state))
	}

	// Legacy persist → revision 1.
	rev, err := repo.PersistYDocSnapshot(ctx, flowID, uA, []byte{0xaa, 0xbb})
	if err != nil {
		t.Fatalf("legacy persist: %v", err)
	}
	if rev != 1 {
		t.Errorf("expected legacy rev=1, got %d", rev)
	}

	// Confirm sub_flow_id is NULL on the legacy row (no autosync touched it).
	var subFlowID sql.NullString
	if err := d.DB.QueryRow(
		`SELECT sub_flow_id FROM flow_drd WHERE flow_id = ? AND tenant_id = ?`,
		flowID, tA,
	).Scan(&subFlowID); err != nil {
		t.Fatalf("readback sub_flow_id: %v", err)
	}
	if subFlowID.Valid {
		t.Errorf("expected NULL sub_flow_id on legacy row, got %q", subFlowID.String)
	}

	// Legacy load → bytes match.
	got, err := repo.LoadYDocState(ctx, flowID)
	if err != nil {
		t.Fatalf("legacy load: %v", err)
	}
	if len(got) != 2 || got[0] != 0xaa || got[1] != 0xbb {
		t.Errorf("legacy load round-trip mismatch: % x", got)
	}
}

// ─── ResolveFlowIDForSubFlow (U3 follow-up) ────────────────────────────────

// TestResolveFlowIDForSubFlow_Existing returns the bound flow_id without
// touching the DRD chain when a flow_drd row already exists.
func TestResolveFlowIDForSubFlow_Existing(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	versionID, _ := seedFlowAndScreens(t, repo, uA)
	flowID := flowIDFromSeed(t, d.DB, versionID)
	sf := seedSubFlowForDRD(t, repo, "Wallet", "Activation")
	ctx := context.Background()

	if _, err := repo.CreateDRDForSubFlow(ctx, sf.ID, flowID, uA); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := repo.ResolveFlowIDForSubFlow(ctx, sf.ID, uA)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != flowID {
		t.Errorf("expected existing flow_id=%s, got %s", flowID, got)
	}
}

// TestResolveFlowIDForSubFlow_BootstrapsOnFirstCall creates the synthetic
// chain when no flow_drd row exists yet for the sub_flow.
func TestResolveFlowIDForSubFlow_BootstrapsOnFirstCall(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	sf := seedSubFlowForDRD(t, repo, "Plutus", "Cold State")
	ctx := context.Background()

	// No flow_drd row yet — bootstrap path.
	flowID, err := repo.ResolveFlowIDForSubFlow(ctx, sf.ID, uA)
	if err != nil {
		t.Fatalf("resolve (bootstrap): %v", err)
	}
	if flowID == "" {
		t.Fatal("expected non-empty flow_id from bootstrap path")
	}

	// Confirm a flow_drd row now exists bound to this sub_flow.
	var (
		gotFlowID   string
		gotSubFlow  sql.NullString
		gotRevision int64
	)
	if err := d.DB.QueryRow(
		`SELECT flow_id, sub_flow_id, revision FROM flow_drd WHERE tenant_id = ? AND sub_flow_id = ?`,
		tA, sf.ID,
	).Scan(&gotFlowID, &gotSubFlow, &gotRevision); err != nil {
		t.Fatalf("readback flow_drd: %v", err)
	}
	if gotFlowID != flowID {
		t.Errorf("flow_id mismatch: row=%s resolver=%s", gotFlowID, flowID)
	}
	if !gotSubFlow.Valid || gotSubFlow.String != sf.ID {
		t.Errorf("sub_flow_id mismatch: %v", gotSubFlow)
	}
	if gotRevision != 0 {
		t.Errorf("expected revision=0 (no snapshot yet), got %d", gotRevision)
	}
}

// TestResolveFlowIDForSubFlow_Idempotent calling twice produces the same
// flow_id and does NOT create duplicate flow_drd rows.
func TestResolveFlowIDForSubFlow_Idempotent(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	sf := seedSubFlowForDRD(t, repo, "Mutual Funds", "SIP")
	ctx := context.Background()

	flowID1, err := repo.ResolveFlowIDForSubFlow(ctx, sf.ID, uA)
	if err != nil {
		t.Fatalf("resolve 1: %v", err)
	}
	flowID2, err := repo.ResolveFlowIDForSubFlow(ctx, sf.ID, uA)
	if err != nil {
		t.Fatalf("resolve 2: %v", err)
	}
	if flowID1 != flowID2 {
		t.Errorf("not idempotent: first=%s second=%s", flowID1, flowID2)
	}

	var rowCount int
	if err := d.DB.QueryRow(
		`SELECT COUNT(*) FROM flow_drd WHERE tenant_id = ? AND sub_flow_id = ?`,
		tA, sf.ID,
	).Scan(&rowCount); err != nil {
		t.Fatalf("count: %v", err)
	}
	if rowCount != 1 {
		t.Errorf("expected exactly 1 flow_drd row, got %d", rowCount)
	}
}

// TestResolveFlowIDForSubFlow_UnknownSubFlowID returns the bootstrap-path
// outcome (a new chain) — but only when userID is supplied. With empty
// userID and no existing row, surfaces a clear error rather than silently
// creating a chain with empty ownership.
func TestResolveFlowIDForSubFlow_RequiresUserIDOnBootstrap(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	_, err := repo.ResolveFlowIDForSubFlow(ctx, "nonexistent-sub-flow-id", "")
	if err == nil {
		t.Error("expected error when bootstrapping with empty user_id")
	}
}

func TestTenantIsolation_BySubFlow(t *testing.T) {
	d, tA, tB, uA := newTestDB(t)
	repoA := NewTenantRepo(d.DB, tA)
	versionID, _ := seedFlowAndScreens(t, repoA, uA)
	flowID := flowIDFromSeed(t, d.DB, versionID)
	sfA := seedSubFlowForDRD(t, repoA, "Wallet", "Activation")
	ctx := context.Background()

	if _, err := repoA.CreateDRDForSubFlow(ctx, sfA.ID, flowID, uA); err != nil {
		t.Fatalf("create on tenantA: %v", err)
	}
	if _, err := repoA.PersistYDocSnapshotBySubFlow(ctx, sfA.ID, uA, []byte{0xff}); err != nil {
		t.Fatalf("persist on tenantA: %v", err)
	}

	// TenantB cannot see TenantA's sub_flow-keyed DRD.
	repoB := NewTenantRepo(d.DB, tB)
	if _, err := repoB.LoadYDocStateBySubFlow(ctx, sfA.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound for cross-tenant load, got %v", err)
	}
	if _, err := repoB.PersistYDocSnapshotBySubFlow(ctx, sfA.ID, uA, []byte{0x01}); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound for cross-tenant persist, got %v", err)
	}

	// And CreateDRDForSubFlow on tenantB doesn't see tenantA's row — so
	// it would try to INSERT (and fail because the flow_id PK collides
	// with tenantA's row — both tenants share the underlying flow_drd
	// PK space, which is acceptable: tenants own flow_id ranges via the
	// flows table).
	// We confirm at minimum that the call doesn't silently return tenantA's
	// flow_id (which would be a tenant-leak bug). The flow itself doesn't
	// exist for tenantB so assertFlowVisibleByID fires first.
	if _, err := repoB.CreateDRDForSubFlow(ctx, sfA.ID, flowID, uA); err == nil {
		t.Error("expected error binding tenantA's flow_id under tenantB")
	}
}
