package projects

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/sse"
)

// repository_figma_autosync_subflow_test.go — U2 of the MCP + PM authoring
// workflow plan. Verifies UpsertSubFlowFromSection respects precedence
// (admin override > ParseSectionName), is idempotent on re-runs, handles
// the "(unassigned)" bucket, rebinds across renames, and stays
// tenant-scoped.

// seedSectionForSubFlow writes the figma_section row a U2 lookup needs.
// Wraps seedSection (which already lives in repository_figma_autosync_test.go)
// with a per-call file/page/section trio so each test gets its own context.
//
// Default classification: PageClassFinal — the historical U2 tests
// (written before U3b's gate) all expect figma_section_id to be linked.
// Tests that exercise the WIP path use seedSectionForSubFlowOnPage with
// an explicit non-final classification.
func seedSectionForSubFlow(t *testing.T, repo *TenantRepo, fileKey, pageID, sectionID, name string) {
	t.Helper()
	seedSectionForSubFlowOnPage(t, repo, fileKey, pageID, sectionID, name, PageClassFinal)
}

// seedSectionForSubFlowOnPage is the explicit-classification variant.
// Used by U3b's design-shipped-gate tests to exercise the proto-wip
// path (page_classification != 'final' → autosync leaves
// figma_section_id NULL).
func seedSectionForSubFlowOnPage(t *testing.T, repo *TenantRepo, fileKey, pageID, sectionID, name string, classification PageClassification) {
	t.Helper()
	ctx := context.Background()
	pages := []FigmaPageRow{{
		FileKey: fileKey, PageID: pageID, Name: "Page", OrderIndex: 0,
		Classification: classification,
	}}
	sections := []FigmaSectionRow{{FileKey: fileKey, PageID: pageID, SectionID: sectionID, Name: name, OrderIndex: 0}}
	if _, _, err := repo.UpsertFigmaPagesAndSections(ctx, fileKey, pages, sections, nil, repo.now().UTC()); err != nil {
		t.Fatalf("seed section %s: %v", sectionID, err)
	}
}

func TestUpsertSubFlowFromSection_HappyPath(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	const (
		fileKey   = "fk-1"
		pageID    = "0:1"
		sectionID = "10:1"
		name      = "Wallet/M2M Settlement"
	)
	seedSectionForSubFlow(t, repo, fileKey, pageID, sectionID, name)

	sf, err := repo.UpsertSubFlowFromSection(ctx, fileKey, pageID, sectionID, name, nil)
	if err != nil {
		t.Fatalf("upsert from section: %v", err)
	}
	if sf.Name != "M2M Settlement" {
		t.Errorf("sub_flow.Name: got %q, want %q", sf.Name, "M2M Settlement")
	}
	if sf.FigmaSectionID == nil || *sf.FigmaSectionID != sectionID {
		t.Errorf("figma_section_id not bound: %v", sf.FigmaSectionID)
	}

	sp, err := repo.GetSubProductByName(ctx, "Wallet")
	if err != nil {
		t.Fatalf("get sub_product: %v", err)
	}
	if sp.ID != sf.SubProductID {
		t.Errorf("sub_product_id mismatch: %s vs %s", sp.ID, sf.SubProductID)
	}
}

func TestUpsertSubFlowFromSection_Idempotent(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	const (
		fileKey   = "fk-1"
		pageID    = "0:1"
		sectionID = "10:1"
		name      = "Wallet/M2M Settlement"
	)
	seedSectionForSubFlow(t, repo, fileKey, pageID, sectionID, name)

	first, err := repo.UpsertSubFlowFromSection(ctx, fileKey, pageID, sectionID, name, nil)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := repo.UpsertSubFlowFromSection(ctx, fileKey, pageID, sectionID, name, nil)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if first.ID != second.ID || first.SubProductID != second.SubProductID {
		t.Errorf("non-idempotent: %+v vs %+v", first, second)
	}

	// No duplicate sub_product / sub_flow rows.
	flows, err := repo.ListSubFlows(ctx, "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(flows) != 1 {
		t.Errorf("expected 1 sub_flow row, got %d", len(flows))
	}
}

func TestUpsertSubFlowFromSection_PreexistingSubFlowFromDRDPath(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	// Simulate U3: PM created the sub_flow via the DRD path before the
	// section appeared in Figma. figma_section_id is NULL at this point.
	wallet, err := repo.UpsertSubProduct(ctx, "Wallet")
	if err != nil {
		t.Fatalf("seed sub_product: %v", err)
	}
	preexisting, err := repo.UpsertSubFlow(ctx, wallet.ID, "M2M Settlement")
	if err != nil {
		t.Fatalf("seed sub_flow: %v", err)
	}
	if preexisting.FigmaSectionID != nil {
		t.Fatalf("precondition: pre-existing sub_flow should have NULL figma_section_id, got %v", preexisting.FigmaSectionID)
	}

	const (
		fileKey   = "fk-1"
		pageID    = "0:1"
		sectionID = "10:1"
		name      = "Wallet/M2M Settlement"
	)
	seedSectionForSubFlow(t, repo, fileKey, pageID, sectionID, name)

	sf, err := repo.UpsertSubFlowFromSection(ctx, fileKey, pageID, sectionID, name, nil)
	if err != nil {
		t.Fatalf("upsert from section: %v", err)
	}
	if sf.ID != preexisting.ID {
		t.Errorf("expected to reuse pre-existing sub_flow %s, got new %s", preexisting.ID, sf.ID)
	}
	if sf.FigmaSectionID == nil || *sf.FigmaSectionID != sectionID {
		t.Errorf("figma_section_id not back-filled: %v", sf.FigmaSectionID)
	}

	// No duplicate row.
	flows, _ := repo.ListSubFlows(ctx, wallet.ID)
	if len(flows) != 1 {
		t.Errorf("expected 1 sub_flow, got %d", len(flows))
	}
}

func TestUpsertSubFlowFromSection_AdminOverrideWins(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	const (
		fileKey   = "fk-1"
		pageID    = "0:1"
		sectionID = "10:1"
		name      = "Wallet/M2M Settlement"
	)
	seedSectionForSubFlow(t, repo, fileKey, pageID, sectionID, name)

	// Admin override BEFORE autosync runs.
	if err := repo.UpsertSectionClassification(ctx, SectionClassification{
		FileKey: fileKey, PageID: pageID, SectionID: sectionID,
		SubProduct: "Custom Wallet", SubFlow: "Custom Flow",
		Source: "admin_override",
	}); err != nil {
		t.Fatalf("seed override: %v", err)
	}

	sf, err := repo.UpsertSubFlowFromSection(ctx, fileKey, pageID, sectionID, name, nil)
	if err != nil {
		t.Fatalf("upsert from section: %v", err)
	}
	if sf.Name != "Custom Flow" {
		t.Errorf("override sub_flow name: got %q, want %q", sf.Name, "Custom Flow")
	}
	sp, _ := repo.GetSubProductByName(ctx, "Custom Wallet")
	if sp.ID == "" || sp.ID != sf.SubProductID {
		t.Errorf("override sub_product not used; sf.sub_product_id=%q sp=%+v", sf.SubProductID, sp)
	}

	// The parser's "Wallet" / "M2M Settlement" should NOT have been
	// materialised.
	if _, err := repo.GetSubProductByName(ctx, "Wallet"); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected Wallet not materialised when override won; got %v", err)
	}
}

func TestUpsertSubFlowFromSection_EmptyOverridesIgnored(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	const (
		fileKey   = "fk-1"
		pageID    = "0:1"
		sectionID = "10:1"
		name      = "Wallet/M2M Settlement"
	)
	seedSectionForSubFlow(t, repo, fileKey, pageID, sectionID, name)

	// Write blank override values directly — UpsertSectionClassification
	// rejects empty inputs, so we side-step it via raw SQL to exercise the
	// "empty-string column" case the picker must defend against.
	if _, err := d.DB.ExecContext(ctx, `
		UPDATE figma_section
		   SET sub_product_override = '   ', sub_flow_override = ''
		 WHERE tenant_id = ? AND file_key = ? AND page_id = ? AND section_id = ?
	`, tA, fileKey, pageID, sectionID); err != nil {
		t.Fatalf("seed blank overrides: %v", err)
	}

	sf, err := repo.UpsertSubFlowFromSection(ctx, fileKey, pageID, sectionID, name, nil)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if sf.Name != "M2M Settlement" {
		t.Errorf("blank overrides should NOT win; got sub_flow %q", sf.Name)
	}
	if _, err := repo.GetSubProductByName(ctx, "Wallet"); err != nil {
		t.Errorf("expected ParseSectionName output materialised when overrides blank; got %v", err)
	}
}

func TestUpsertSubFlowFromSection_SlashlessLandsInUnassigned(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	// Two slash-less sections under separate ids; both should reuse the
	// same "(unassigned)" sub_product row.
	cases := []struct {
		fileKey, pageID, sectionID, name, wantSubFlow string
	}{
		{"fk-1", "0:1", "10:1", "Onboarding", "Onboarding"},
		{"fk-1", "0:1", "10:2", "Hero", "Hero"},
	}
	for _, c := range cases {
		seedSectionForSubFlow(t, repo, c.fileKey, c.pageID, c.sectionID, c.name)
	}

	var subProductIDs []string
	for _, c := range cases {
		sf, err := repo.UpsertSubFlowFromSection(ctx, c.fileKey, c.pageID, c.sectionID, c.name, nil)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if sf.Name != c.wantSubFlow {
			t.Errorf("%s: sub_flow name got %q, want %q", c.name, sf.Name, c.wantSubFlow)
		}
		subProductIDs = append(subProductIDs, sf.SubProductID)
	}
	if subProductIDs[0] != subProductIDs[1] {
		t.Errorf("expected slash-less sections to share the (unassigned) sub_product; got %s vs %s",
			subProductIDs[0], subProductIDs[1])
	}

	sp, err := repo.GetSubProductByName(ctx, UnassignedSubProduct)
	if err != nil {
		t.Fatalf("(unassigned) sub_product missing: %v", err)
	}
	if sp.ID != subProductIDs[0] {
		t.Errorf("(unassigned) row id mismatch")
	}
}

func TestUpsertSubFlowFromSection_CaseInsensitiveIdempotency(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	const (
		fileKey   = "fk-1"
		pageID    = "0:1"
		sectionID = "10:1"
	)
	seedSectionForSubFlow(t, repo, fileKey, pageID, sectionID, "Wallet/M2M Settlement")
	first, err := repo.UpsertSubFlowFromSection(ctx, fileKey, pageID, sectionID, "Wallet/M2M Settlement", nil)
	if err != nil {
		t.Fatalf("first: %v", err)
	}

	// Simulate a designer renaming the section to a different casing in
	// Figma. Re-poll picks up the new name; the upsert path must NOT
	// create a duplicate row (case-insensitive LOWER(TRIM(name)) index
	// on sub_flow).
	seedSectionForSubFlow(t, repo, fileKey, pageID, sectionID, "WALLET/M2M Settlement")
	second, err := repo.UpsertSubFlowFromSection(ctx, fileKey, pageID, sectionID, "WALLET/M2M Settlement", nil)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if first.ID != second.ID {
		t.Errorf("case-insensitive rename should reuse same sub_flow; got %s vs %s", first.ID, second.ID)
	}
	flows, _ := repo.ListSubFlows(ctx, "")
	if len(flows) != 1 {
		t.Errorf("expected 1 sub_flow row after case-only rename, got %d", len(flows))
	}
}

func TestUpsertSubFlowFromSection_RenameToDifferentSubFlow(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	const (
		fileKey   = "fk-1"
		pageID    = "0:1"
		sectionID = "10:1"
	)
	seedSectionForSubFlow(t, repo, fileKey, pageID, sectionID, "Wallet/Foo")
	fooSF, err := repo.UpsertSubFlowFromSection(ctx, fileKey, pageID, sectionID, "Wallet/Foo", nil)
	if err != nil {
		t.Fatalf("foo: %v", err)
	}

	// Designer renames the section to a wholly different sub_flow under
	// the same sub_product. A NEW sub_flow row "Bar" is created. The old
	// "Foo" row stays (PM intent may still hold), but its figma_section_id
	// is cleared so the partial unique index can re-grant the binding.
	seedSectionForSubFlow(t, repo, fileKey, pageID, sectionID, "Wallet/Bar")
	barSF, err := repo.UpsertSubFlowFromSection(ctx, fileKey, pageID, sectionID, "Wallet/Bar", nil)
	if err != nil {
		t.Fatalf("bar: %v", err)
	}
	if barSF.ID == fooSF.ID {
		t.Fatalf("expected new sub_flow id after rename; got same id %s", barSF.ID)
	}
	if barSF.Name != "Bar" {
		t.Errorf("new sub_flow name: got %q, want %q", barSF.Name, "Bar")
	}
	if barSF.FigmaSectionID == nil || *barSF.FigmaSectionID != sectionID {
		t.Errorf("new sub_flow not linked to section: %v", barSF.FigmaSectionID)
	}

	// Old row still exists with figma_section_id cleared.
	flows, _ := repo.ListSubFlows(ctx, fooSF.SubProductID)
	if len(flows) != 2 {
		t.Fatalf("expected 2 sub_flow rows after rename, got %d", len(flows))
	}
	for _, sf := range flows {
		if sf.ID == fooSF.ID {
			if sf.FigmaSectionID != nil {
				t.Errorf("old sub_flow should have NULL figma_section_id; got %v", *sf.FigmaSectionID)
			}
		}
	}
}

func TestUpsertSubFlowFromSection_TenantIsolation(t *testing.T) {
	d, tA, tB, _ := newTestDB(t)
	repoA := NewTenantRepo(d.DB, tA)
	repoB := NewTenantRepo(d.DB, tB)
	ctx := context.Background()

	const (
		fileKey   = "fk-1"
		pageID    = "0:1"
		sectionID = "10:1"
		name      = "Wallet/M2M Settlement"
	)
	seedSectionForSubFlow(t, repoA, fileKey, pageID, sectionID, name)
	seedSectionForSubFlow(t, repoB, fileKey, pageID, sectionID, name)

	sfA, err := repoA.UpsertSubFlowFromSection(ctx, fileKey, pageID, sectionID, name, nil)
	if err != nil {
		t.Fatalf("tenantA: %v", err)
	}
	sfB, err := repoB.UpsertSubFlowFromSection(ctx, fileKey, pageID, sectionID, name, nil)
	if err != nil {
		t.Fatalf("tenantB: %v", err)
	}
	if sfA.ID == sfB.ID {
		t.Errorf("tenants must not share sub_flow rows; got id %s in both", sfA.ID)
	}
	if sfA.SubProductID == sfB.SubProductID {
		t.Errorf("tenants must not share sub_product rows; got %s in both", sfA.SubProductID)
	}

	// Each tenant has exactly 1 sub_flow.
	flowsA, _ := repoA.ListSubFlows(ctx, "")
	flowsB, _ := repoB.ListSubFlows(ctx, "")
	if len(flowsA) != 1 || len(flowsB) != 1 {
		t.Errorf("expected 1 row per tenant; A=%d B=%d", len(flowsA), len(flowsB))
	}
}

func TestUpsertSubFlowFromSection_RejectsEmptyArgs(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	_, err := repo.UpsertSubFlowFromSection(ctx, "", "", "", "Wallet/M2M", nil)
	if err == nil {
		t.Errorf("expected error for empty args")
	}
	if err != nil && !strings.Contains(err.Error(), "required") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// ─── U3b — design-shipped gate (page classification) ─────────────────────

// TestUpsertSubFlowFromSection_FinalPageLinksSection verifies the U3b gate
// flips figma_section_id when the hosting page is classified 'final'.
func TestUpsertSubFlowFromSection_FinalPageLinksSection(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	seedSectionForSubFlowOnPage(t, repo, "fk-1", "0:1", "10:1",
		"Wallet/M2M Settlement", PageClassFinal)

	sf, err := repo.UpsertSubFlowFromSection(ctx, "fk-1", "0:1", "10:1",
		"Wallet/M2M Settlement", nil)
	if err != nil {
		t.Fatalf("autosync: %v", err)
	}
	if sf.FigmaSectionID == nil || *sf.FigmaSectionID != "10:1" {
		t.Errorf("expected figma_section_id bound on final page, got %v", sf.FigmaSectionID)
	}
}

// TestUpsertSubFlowFromSection_NonFinalPageSkipsLink verifies the gate
// holds back the binding for WIP-class pages.
func TestUpsertSubFlowFromSection_NonFinalPageSkipsLink(t *testing.T) {
	cases := []PageClassification{PageClassVersion, PageClassNoise, PageClassUnknown}
	for _, class := range cases {
		t.Run(string(class), func(t *testing.T) {
			d, tA, _, _ := newTestDB(t)
			repo := NewTenantRepo(d.DB, tA)
			ctx := context.Background()

			seedSectionForSubFlowOnPage(t, repo, "fk-1", "0:1", "10:1",
				"Wallet/M2M Settlement", class)

			sf, err := repo.UpsertSubFlowFromSection(ctx, "fk-1", "0:1", "10:1",
				"Wallet/M2M Settlement", nil)
			if err != nil {
				t.Fatalf("autosync: %v", err)
			}
			// sub_flow row still created.
			if sf.ID == "" || sf.Name != "M2M Settlement" {
				t.Errorf("sub_flow row should still be created on WIP page: %+v", sf)
			}
			// But figma_section_id stays NULL.
			if sf.FigmaSectionID != nil {
				t.Errorf("expected NULL figma_section_id on %q page, got %v", class, *sf.FigmaSectionID)
			}
		})
	}
}

// TestUpsertSubFlowFromSection_DesignShippedPublishes verifies that a
// fresh bind on a final-classified page publishes the FigmaDesignShipped
// SSE event on the inbox:<tenant_id> channel.
func TestUpsertSubFlowFromSection_DesignShippedPublishes(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	seedSectionForSubFlowOnPage(t, repo, "fk-1", "0:1", "10:1",
		"Wallet/M2M Settlement", PageClassFinal)

	broker := &protoStubBroker{}
	bound, err := repo.UpsertSubFlowFromSection(ctx, "fk-1", "0:1", "10:1",
		"Wallet/M2M Settlement", broker)
	if err != nil {
		t.Fatalf("autosync: %v", err)
	}

	if len(broker.events) != 1 {
		t.Fatalf("expected 1 SSE event published, got %d", len(broker.events))
	}
	ev, ok := broker.events[0].event.(sse.FigmaDesignShipped)
	if !ok {
		t.Fatalf("unexpected event type: %T", broker.events[0].event)
	}
	if ev.SubFlowID != bound.ID {
		t.Errorf("event SubFlowID: got %q, want %q", ev.SubFlowID, bound.ID)
	}
	if ev.FigmaSectionID != "10:1" {
		t.Errorf("event FigmaSectionID: got %q, want %q", ev.FigmaSectionID, "10:1")
	}
	if ev.SubFlowSlug != "wallet/m2m-settlement" {
		t.Errorf("event slug: got %q, want %q", ev.SubFlowSlug, "wallet/m2m-settlement")
	}
	if broker.events[0].channel != "inbox:"+tA {
		t.Errorf("event channel: got %q, want %q", broker.events[0].channel, "inbox:"+tA)
	}
}

// TestUpsertSubFlowFromSection_DesignShippedIdempotent confirms re-running
// autosync against an already-bound sub_flow does NOT re-publish.
func TestUpsertSubFlowFromSection_DesignShippedIdempotent(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	seedSectionForSubFlowOnPage(t, repo, "fk-1", "0:1", "10:1",
		"Wallet/M2M Settlement", PageClassFinal)

	broker := &protoStubBroker{}
	if _, err := repo.UpsertSubFlowFromSection(ctx, "fk-1", "0:1", "10:1",
		"Wallet/M2M Settlement", broker); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := repo.UpsertSubFlowFromSection(ctx, "fk-1", "0:1", "10:1",
		"Wallet/M2M Settlement", broker); err != nil {
		t.Fatalf("second: %v", err)
	}
	if len(broker.events) != 1 {
		t.Errorf("expected 1 event total (first bind only), got %d", len(broker.events))
	}
}

// TestUpsertSubFlowFromSection_StampsPrototypeSupersededAt verifies the
// gate stamps prototype_superseded_at when a section binds while a
// prototype URL is in place.
func TestUpsertSubFlowFromSection_StampsPrototypeSupersededAt(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	// PM authors a DRD with a prototype URL BEFORE design exists.
	sp, err := repo.UpsertSubProduct(ctx, "Wallet")
	if err != nil {
		t.Fatalf("seed sub_product: %v", err)
	}
	sf, err := repo.UpsertSubFlow(ctx, sp.ID, "M2M Settlement")
	if err != nil {
		t.Fatalf("seed sub_flow: %v", err)
	}
	if err := repo.AttachPrototype(ctx, sf.ID, "https://example.com/proto", "", nil); err != nil {
		t.Fatalf("attach proto: %v", err)
	}

	// Designer ships the section on a final page; autosync binds + stamps.
	seedSectionForSubFlowOnPage(t, repo, "fk-1", "0:1", "10:1",
		"Wallet/M2M Settlement", PageClassFinal)
	bound, err := repo.UpsertSubFlowFromSection(ctx, "fk-1", "0:1", "10:1",
		"Wallet/M2M Settlement", nil)
	if err != nil {
		t.Fatalf("autosync: %v", err)
	}
	if bound.ID != sf.ID {
		t.Fatalf("expected reuse of pre-existing sub_flow")
	}
	if bound.PrototypeSupersededAt == nil {
		t.Errorf("prototype_superseded_at should be stamped when section binds while proto is attached")
	}
	if bound.PrototypeURL == nil || *bound.PrototypeURL != "https://example.com/proto" {
		t.Errorf("prototype_url should be preserved for history; got %v", bound.PrototypeURL)
	}
}
