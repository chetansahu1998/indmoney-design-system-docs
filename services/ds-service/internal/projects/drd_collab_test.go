package projects

import (
	"context"
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
