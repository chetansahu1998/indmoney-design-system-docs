package projects

import (
	"context"
	"errors"
	"testing"
)

// drd_anchor_test.go — plan 005 Phase B.
//
// Covers AttachDRDAnchor / DetachDRDAnchor / ListDRDAnchorsForSubFlow,
// plus the idempotency + tenant-isolation guarantees the docs promise.

func TestAttachDRDAnchor_HappyPath(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	sf, _, _ := seedPRDWithTab(t, repo)

	id, err := repo.AttachDRDAnchor(ctx, sf.ID, "block-1", "S3", uA)
	if err != nil {
		t.Fatalf("AttachDRDAnchor: %v", err)
	}
	if id == "" {
		t.Errorf("expected non-empty id")
	}
	got, err := repo.ListDRDAnchorsForSubFlow(ctx, sf.ID)
	if err != nil {
		t.Fatalf("ListDRDAnchorsForSubFlow: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 anchor, got %d", len(got))
	}
	if got[0].BlockID != "block-1" || got[0].ScreenID != "S3" {
		t.Errorf("round-trip mismatch: %+v", got[0])
	}
	if got[0].CreatedBy != uA {
		t.Errorf("created_by mismatch: got %q want %q", got[0].CreatedBy, uA)
	}
}

func TestAttachDRDAnchor_Idempotent(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	sf, _, _ := seedPRDWithTab(t, repo)

	id1, err := repo.AttachDRDAnchor(ctx, sf.ID, "block-1", "S3", uA)
	if err != nil {
		t.Fatalf("first attach: %v", err)
	}
	id2, err := repo.AttachDRDAnchor(ctx, sf.ID, "block-1", "S3", uA)
	if err != nil {
		t.Fatalf("second attach: %v", err)
	}
	if id1 != id2 {
		t.Errorf("idempotency violated: %q vs %q", id1, id2)
	}
	got, _ := repo.ListDRDAnchorsForSubFlow(ctx, sf.ID)
	if len(got) != 1 {
		t.Errorf("expected 1 row after double-attach, got %d", len(got))
	}
}

func TestAttachDRDAnchor_MultipleScreensPerBlock(t *testing.T) {
	// "Trader Mode" heading anchors both S3 (Trader Mode ON) and S7
	// (Order sheet) — both rows should land in the table.
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	sf, _, _ := seedPRDWithTab(t, repo)

	if _, err := repo.AttachDRDAnchor(ctx, sf.ID, "block-1", "S3", uA); err != nil {
		t.Fatalf("S3: %v", err)
	}
	if _, err := repo.AttachDRDAnchor(ctx, sf.ID, "block-1", "S7", uA); err != nil {
		t.Fatalf("S7: %v", err)
	}
	got, _ := repo.ListDRDAnchorsForSubFlow(ctx, sf.ID)
	if len(got) != 2 {
		t.Errorf("expected 2 anchors on same block, got %d", len(got))
	}
}

func TestAttachDRDAnchor_EmptyFieldsRejected(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	sf, _, _ := seedPRDWithTab(t, repo)

	for _, tc := range []struct {
		name           string
		subFlowID      string
		blockID        string
		screenID       string
	}{
		{"empty sub_flow", "", "block-1", "S3"},
		{"empty block",    sf.ID, "", "S3"},
		{"empty screen",   sf.ID, "block-1", ""},
		{"whitespace block", sf.ID, "   ", "S3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := repo.AttachDRDAnchor(ctx, tc.subFlowID, tc.blockID, tc.screenID, "u")
			if !errors.Is(err, ErrDRDAnchorFieldRequired) {
				t.Errorf("expected ErrDRDAnchorFieldRequired, got %v", err)
			}
		})
	}
}

func TestDetachDRDAnchor(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	sf, _, _ := seedPRDWithTab(t, repo)

	if _, err := repo.AttachDRDAnchor(ctx, sf.ID, "block-1", "S3", uA); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if err := repo.DetachDRDAnchor(ctx, sf.ID, "block-1", "S3"); err != nil {
		t.Fatalf("detach: %v", err)
	}
	got, _ := repo.ListDRDAnchorsForSubFlow(ctx, sf.ID)
	if len(got) != 0 {
		t.Errorf("expected 0 anchors after detach, got %d", len(got))
	}
	// Re-detach (idempotent — no-op).
	if err := repo.DetachDRDAnchor(ctx, sf.ID, "block-1", "S3"); err != nil {
		t.Errorf("second detach should be no-op, got %v", err)
	}
}

func TestListDRDAnchorsForSubFlow_TenantIsolation(t *testing.T) {
	d, tA, tB, uA := newTestDB(t)
	repoA := NewTenantRepo(d.DB, tA)
	repoB := NewTenantRepo(d.DB, tB)
	ctx := context.Background()

	sf, _, _ := seedPRDWithTab(t, repoA)

	if _, err := repoA.AttachDRDAnchor(ctx, sf.ID, "block-1", "S3", uA); err != nil {
		t.Fatalf("repoA attach: %v", err)
	}
	got, err := repoB.ListDRDAnchorsForSubFlow(ctx, sf.ID)
	if err != nil {
		t.Fatalf("repoB list: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("tenant B leaked %d anchors from tenant A", len(got))
	}
}

func TestListDRDAnchorsForSubFlow_EmptySubFlow(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	got, err := repo.ListDRDAnchorsForSubFlow(context.Background(), "")
	if err != nil {
		t.Fatalf("empty sub_flow: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil result for empty sub_flow_id, got %d rows", len(got))
	}
}
