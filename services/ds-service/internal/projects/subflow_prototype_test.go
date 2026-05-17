package projects

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/sse"
)

// subflow_prototype_test.go — U3b of the MCP + PM authoring workflow plan.
// Verifies AttachPrototype / DetachPrototype / CanvasLifecycle correctness
// + the U2 autosync gate's interaction with the prototype lifecycle.

// protoStubBroker captures every Publish call so tests can assert SSE event
// fan-out without spinning up a real *sse.MemoryBroker.
type protoStubBroker struct {
	events []brokerEvent
}

type brokerEvent struct {
	channel string
	event   sse.Event
}

func (s *protoStubBroker) Publish(channel string, event sse.Event) {
	s.events = append(s.events, brokerEvent{channel: channel, event: event})
}

// seedSubFlowForProto creates a fresh sub_product + sub_flow pair the
// prototype tests can attach to. Returns the sub_flow row.
func seedSubFlowForProto(t *testing.T, repo *TenantRepo) SubFlow {
	t.Helper()
	ctx := context.Background()
	sp, err := repo.UpsertSubProduct(ctx, "Wallet")
	if err != nil {
		t.Fatalf("seed sub_product: %v", err)
	}
	sf, err := repo.UpsertSubFlow(ctx, sp.ID, "M2M Settlement")
	if err != nil {
		t.Fatalf("seed sub_flow: %v", err)
	}
	return sf
}

// ─── AttachPrototype ──────────────────────────────────────────────────────

func TestAttachPrototype_HappyPath(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	sf := seedSubFlowForProto(t, repo)
	broker := &protoStubBroker{}

	const url = "https://www.figma.com/proto/abc123/Cold-State"
	if err := repo.AttachPrototype(ctx, sf.ID, url, "Cold State v3", broker); err != nil {
		t.Fatalf("attach: %v", err)
	}

	got, err := repo.loadSubFlowByID(ctx, sf.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.PrototypeURL == nil || *got.PrototypeURL != url {
		t.Errorf("prototype_url: got %v, want %q", got.PrototypeURL, url)
	}
	if got.PrototypeTitle == nil || *got.PrototypeTitle != "Cold State v3" {
		t.Errorf("prototype_title: got %v", got.PrototypeTitle)
	}
	if got.PrototypeAttachedAt == nil {
		t.Error("prototype_attached_at should be stamped")
	}

	if len(broker.events) != 1 {
		t.Fatalf("expected 1 published event, got %d", len(broker.events))
	}
	ev, ok := broker.events[0].event.(sse.DRDPrototypeAttached)
	if !ok {
		t.Fatalf("unexpected event type: %T", broker.events[0].event)
	}
	if ev.SubFlowID != sf.ID || ev.PrototypeURL != url || ev.Title != "Cold State v3" {
		t.Errorf("event payload mismatch: %+v", ev)
	}
	if ev.SubFlowSlug != "wallet/m2m-settlement" {
		t.Errorf("expected slug %q, got %q", "wallet/m2m-settlement", ev.SubFlowSlug)
	}
	if broker.events[0].channel != "inbox:"+tA {
		t.Errorf("expected inbox channel, got %q", broker.events[0].channel)
	}
}

func TestAttachPrototype_RejectsNonHTTPS(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()
	sf := seedSubFlowForProto(t, repo)

	cases := []struct {
		name string
		url  string
	}{
		{"empty", ""},
		{"http scheme", "http://example.com/proto"},
		{"file scheme", "file:///tmp/proto.html"},
		{"no scheme", "example.com/proto"},
		{"javascript", "javascript:alert(1)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := repo.AttachPrototype(ctx, sf.ID, c.url, "", nil)
			if !errors.Is(err, ErrInvalidPrototypeURL) {
				t.Errorf("got %v, want ErrInvalidPrototypeURL", err)
			}
		})
	}
}

func TestAttachPrototype_RejectsOverlongURL(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()
	sf := seedSubFlowForProto(t, repo)

	long := "https://" + strings.Repeat("a", PrototypeURLMaxLength)
	err := repo.AttachPrototype(ctx, sf.ID, long, "", nil)
	if !errors.Is(err, ErrInvalidPrototypeURL) {
		t.Errorf("got %v, want ErrInvalidPrototypeURL", err)
	}
}

func TestAttachPrototype_Idempotent(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()
	sf := seedSubFlowForProto(t, repo)

	const url = "https://www.figma.com/proto/abc123/Cold-State"
	broker := &protoStubBroker{}
	if err := repo.AttachPrototype(ctx, sf.ID, url, "X", broker); err != nil {
		t.Fatalf("first: %v", err)
	}
	first, _ := repo.loadSubFlowByID(ctx, sf.ID)

	// Re-attach with same URL + title — no row change, no SSE re-publish.
	if err := repo.AttachPrototype(ctx, sf.ID, url, "X", broker); err != nil {
		t.Fatalf("second: %v", err)
	}
	second, _ := repo.loadSubFlowByID(ctx, sf.ID)

	if first.PrototypeAttachedAt == nil || second.PrototypeAttachedAt == nil {
		t.Fatal("attached_at should be set")
	}
	if !first.PrototypeAttachedAt.Equal(*second.PrototypeAttachedAt) {
		t.Errorf("attached_at should be preserved across idempotent re-attach")
	}
	if len(broker.events) != 1 {
		t.Errorf("expected 1 published event after idempotent re-attach, got %d", len(broker.events))
	}
}

func TestAttachPrototype_SubFlowNotFound(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	err := repo.AttachPrototype(ctx, "does-not-exist", "https://example.com/proto", "", nil)
	if !errors.Is(err, ErrSubFlowNotFound) {
		t.Errorf("got %v, want ErrSubFlowNotFound", err)
	}
}

// ─── DetachPrototype ──────────────────────────────────────────────────────

func TestDetachPrototype_ClearsURL(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()
	sf := seedSubFlowForProto(t, repo)

	if err := repo.AttachPrototype(ctx, sf.ID, "https://example.com/proto", "X", nil); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if err := repo.DetachPrototype(ctx, sf.ID); err != nil {
		t.Fatalf("detach: %v", err)
	}
	got, _ := repo.loadSubFlowByID(ctx, sf.ID)
	if got.PrototypeURL != nil {
		t.Errorf("expected nil prototype_url after detach, got %v", *got.PrototypeURL)
	}
	if got.PrototypeTitle != nil {
		t.Errorf("expected nil prototype_title after detach, got %v", *got.PrototypeTitle)
	}

	// Lifecycle is "empty" after detach (no section + no URL).
	lc, err := repo.CanvasLifecycle(ctx, sf.ID)
	if err != nil {
		t.Fatalf("lifecycle: %v", err)
	}
	if lc != LifecycleEmpty {
		t.Errorf("got lifecycle %q, want %q", lc, LifecycleEmpty)
	}
}

// ─── CanvasLifecycle ──────────────────────────────────────────────────────

func TestCanvasLifecycle_Empty(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()
	sf := seedSubFlowForProto(t, repo)

	lc, err := repo.CanvasLifecycle(ctx, sf.ID)
	if err != nil {
		t.Fatalf("lifecycle: %v", err)
	}
	if lc != LifecycleEmpty {
		t.Errorf("got %q, want %q", lc, LifecycleEmpty)
	}
}

func TestCanvasLifecycle_ProtoOnly(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()
	sf := seedSubFlowForProto(t, repo)

	if err := repo.AttachPrototype(ctx, sf.ID, "https://example.com/proto", "", nil); err != nil {
		t.Fatalf("attach: %v", err)
	}
	lc, err := repo.CanvasLifecycle(ctx, sf.ID)
	if err != nil {
		t.Fatalf("lifecycle: %v", err)
	}
	if lc != LifecycleProtoOnly {
		t.Errorf("got %q, want %q", lc, LifecycleProtoOnly)
	}
}

func TestCanvasLifecycle_ProtoWIP(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()
	sf := seedSubFlowForProto(t, repo)

	// Attach proto.
	if err := repo.AttachPrototype(ctx, sf.ID, "https://example.com/proto", "", nil); err != nil {
		t.Fatalf("attach: %v", err)
	}

	// Seed a section on a NON-final page; run autosync. Gate will not
	// link figma_section_id but the figma_section row exists for the
	// WIP probe in CanvasLifecycle.
	seedSectionForSubFlowOnPage(t, repo, "fk-W", "0:1", "10:1",
		"Wallet/M2M Settlement", PageClassVersion)
	bound, err := repo.UpsertSubFlowFromSection(ctx, "fk-W", "0:1", "10:1",
		"Wallet/M2M Settlement", nil)
	if err != nil {
		t.Fatalf("autosync: %v", err)
	}
	if bound.FigmaSectionID != nil {
		t.Fatalf("non-final page should NOT bind figma_section_id; got %v", *bound.FigmaSectionID)
	}

	lc, err := repo.CanvasLifecycle(ctx, sf.ID)
	if err != nil {
		t.Fatalf("lifecycle: %v", err)
	}
	if lc != LifecycleProtoWIP {
		t.Errorf("got %q, want %q", lc, LifecycleProtoWIP)
	}
}

func TestCanvasLifecycle_DesignShipped(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	// Seed section on a final page; autosync binds it.
	seedSectionForSubFlowOnPage(t, repo, "fk-F", "0:1", "10:1",
		"Wallet/M2M Settlement", PageClassFinal)
	bound, err := repo.UpsertSubFlowFromSection(ctx, "fk-F", "0:1", "10:1",
		"Wallet/M2M Settlement", nil)
	if err != nil {
		t.Fatalf("autosync: %v", err)
	}
	if bound.FigmaSectionID == nil {
		t.Fatalf("final page should bind figma_section_id")
	}

	lc, err := repo.CanvasLifecycle(ctx, bound.ID)
	if err != nil {
		t.Fatalf("lifecycle: %v", err)
	}
	if lc != LifecycleDesignShipped {
		t.Errorf("got %q, want %q", lc, LifecycleDesignShipped)
	}
}

func TestCanvasLifecycle_DesignShipped_SupersedesProto(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	// PM attaches a prototype on a fresh sub_flow.
	sf := seedSubFlowForProto(t, repo)
	if err := repo.AttachPrototype(ctx, sf.ID, "https://example.com/proto", "", nil); err != nil {
		t.Fatalf("attach: %v", err)
	}

	// Designer ships a section on a final page that names the same
	// sub_flow. Autosync should bind the section AND stamp
	// prototype_superseded_at.
	seedSectionForSubFlowOnPage(t, repo, "fk-F", "0:1", "10:1",
		"Wallet/M2M Settlement", PageClassFinal)
	bound, err := repo.UpsertSubFlowFromSection(ctx, "fk-F", "0:1", "10:1",
		"Wallet/M2M Settlement", nil)
	if err != nil {
		t.Fatalf("autosync: %v", err)
	}
	if bound.ID != sf.ID {
		t.Fatalf("autosync should reuse pre-existing sub_flow %s; got %s", sf.ID, bound.ID)
	}
	if bound.FigmaSectionID == nil {
		t.Fatalf("figma_section_id not bound on final page")
	}
	if bound.PrototypeSupersededAt == nil {
		t.Errorf("prototype_superseded_at should be set when proto is in place at flip time")
	}
	if bound.PrototypeURL == nil {
		t.Errorf("prototype URL should be preserved for history")
	}

	lc, err := repo.CanvasLifecycle(ctx, sf.ID)
	if err != nil {
		t.Fatalf("lifecycle: %v", err)
	}
	if lc != LifecycleDesignShipped {
		t.Errorf("got %q, want %q", lc, LifecycleDesignShipped)
	}
}

func TestCanvasLifecycle_SubFlowNotFound(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	_, err := repo.CanvasLifecycle(ctx, "no-such-id")
	if !errors.Is(err, ErrSubFlowNotFound) {
		t.Errorf("got %v, want ErrSubFlowNotFound", err)
	}
}
