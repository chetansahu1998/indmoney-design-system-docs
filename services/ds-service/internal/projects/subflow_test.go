package projects

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// U1 — sub_product + sub_flow repo tests.
// Uses newTestDB from repository_test.go for the tenants/users bootstrap.

func TestSubFlowSlugify(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"Wallet", "wallet"},
		{"M2M Settlement", "m2m-settlement"},
		{"  Wallet  ", "wallet"},
		{"INDstocks F&O", "indstocks-f-o"},
		{"Wallet/Main Flow", "wallet-main-flow"},
		{"---weird---", "weird"},
		{"", ""},
		{"123 Hello", "123-hello"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := subFlowSlugify(tc.in); got != tc.want {
				t.Errorf("subFlowSlugify(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestSubProduct_UpsertHappyPath(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	sp1, err := repo.UpsertSubProduct(ctx, "Wallet")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if sp1.ID == "" {
		t.Fatal("expected id")
	}
	if sp1.Name != "Wallet" {
		t.Fatalf("name = %q, want %q", sp1.Name, "Wallet")
	}
	if sp1.Slug != "wallet" {
		t.Fatalf("slug = %q, want %q", sp1.Slug, "wallet")
	}
	if sp1.CreatedAt.IsZero() {
		t.Fatal("expected created_at to be set")
	}

	// Second call returns the same id.
	sp2, err := repo.UpsertSubProduct(ctx, "Wallet")
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if sp1.ID != sp2.ID {
		t.Fatalf("expected idempotent id; got %s vs %s", sp1.ID, sp2.ID)
	}
}

func TestSubProduct_UpsertCaseInsensitive(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	sp1, err := repo.UpsertSubProduct(ctx, "Wallet")
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	sp2, err := repo.UpsertSubProduct(ctx, "WALLET")
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if sp1.ID != sp2.ID {
		t.Fatalf("expected same id for case-insensitive name; got %s vs %s", sp1.ID, sp2.ID)
	}
	// First writer's casing wins.
	if sp2.Name != "Wallet" {
		t.Fatalf("expected name preserved as %q, got %q", "Wallet", sp2.Name)
	}
}

func TestSubProduct_UpsertTrimsWhitespace(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	sp1, err := repo.UpsertSubProduct(ctx, "Wallet")
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	sp2, err := repo.UpsertSubProduct(ctx, "  Wallet  ")
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if sp1.ID != sp2.ID {
		t.Fatalf("expected same id after trim; got %s vs %s", sp1.ID, sp2.ID)
	}
}

func TestSubProduct_SlugFromName(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	cases := []struct {
		name, wantSlug string
	}{
		{"M2M Settlement", "m2m-settlement"},
		{"INDstocks F&O", "indstocks-f-o"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sp, err := repo.UpsertSubProduct(ctx, tc.name)
			if err != nil {
				t.Fatalf("upsert: %v", err)
			}
			if sp.Slug != tc.wantSlug {
				t.Fatalf("slug = %q, want %q", sp.Slug, tc.wantSlug)
			}
		})
	}
}

func TestSubProduct_EmptyNameRejected(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	for _, in := range []string{"", "   "} {
		if _, err := repo.UpsertSubProduct(ctx, in); err == nil {
			t.Fatalf("expected error for empty name %q", in)
		}
	}
}

func TestSubFlow_UpsertHappyPath(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	sp, err := repo.UpsertSubProduct(ctx, "Wallet")
	if err != nil {
		t.Fatalf("upsert sub_product: %v", err)
	}

	sf1, err := repo.UpsertSubFlow(ctx, sp.ID, "M2M Settlement")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if sf1.ID == "" {
		t.Fatal("expected id")
	}
	if sf1.Slug != "m2m-settlement" {
		t.Fatalf("slug = %q, want %q", sf1.Slug, "m2m-settlement")
	}
	if sf1.DRDID != nil || sf1.FigmaSectionID != nil {
		t.Fatalf("expected nullable cols to start NULL; got drd=%v fig=%v", sf1.DRDID, sf1.FigmaSectionID)
	}

	sf2, err := repo.UpsertSubFlow(ctx, sp.ID, "M2M Settlement")
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if sf1.ID != sf2.ID {
		t.Fatalf("expected idempotent id; got %s vs %s", sf1.ID, sf2.ID)
	}
}

func TestSubFlow_ScopedBySubProduct(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	wallet, err := repo.UpsertSubProduct(ctx, "Wallet")
	if err != nil {
		t.Fatalf("wallet: %v", err)
	}
	stocks, err := repo.UpsertSubProduct(ctx, "INDstocks")
	if err != nil {
		t.Fatalf("stocks: %v", err)
	}

	sfWallet, err := repo.UpsertSubFlow(ctx, wallet.ID, "Complete Flow")
	if err != nil {
		t.Fatalf("wallet sf: %v", err)
	}
	sfStocks, err := repo.UpsertSubFlow(ctx, stocks.ID, "Complete Flow")
	if err != nil {
		t.Fatalf("stocks sf: %v", err)
	}
	if sfWallet.ID == sfStocks.ID {
		t.Fatal("expected distinct sub_flow ids across sub_products")
	}
	if sfWallet.SubProductID != wallet.ID || sfStocks.SubProductID != stocks.ID {
		t.Fatal("sub_product scoping wrong")
	}
}

func TestSubFlow_GetBySlugRoundTrip(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	wallet, err := repo.UpsertSubProduct(ctx, "Wallet")
	if err != nil {
		t.Fatalf("wallet: %v", err)
	}
	created, err := repo.UpsertSubFlow(ctx, wallet.ID, "M2M Settlement")
	if err != nil {
		t.Fatalf("sf: %v", err)
	}

	got, err := repo.GetSubFlowBySlug(ctx, "wallet/m2m-settlement")
	if err != nil {
		t.Fatalf("get by slug: %v", err)
	}
	if got.ID != created.ID {
		t.Fatalf("expected id %s, got %s", created.ID, got.ID)
	}
	if got.FullSlug(wallet) != "wallet/m2m-settlement" {
		t.Fatalf("FullSlug = %q", got.FullSlug(wallet))
	}

	// Malformed / missing slug → ErrNotFound.
	for _, bad := range []string{"", "wallet", "wallet/", "/m2m-settlement", "wallet/unknown"} {
		if _, err := repo.GetSubFlowBySlug(ctx, bad); !errors.Is(err, ErrNotFound) {
			t.Errorf("expected ErrNotFound for %q, got %v", bad, err)
		}
	}
}

func TestSubFlow_LinkToFigmaSection(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	wallet, _ := repo.UpsertSubProduct(ctx, "Wallet")
	sf, err := repo.UpsertSubFlow(ctx, wallet.ID, "M2M Settlement")
	if err != nil {
		t.Fatalf("sf: %v", err)
	}

	const sectionID = "12345:6789"
	if err := repo.LinkSubFlowToFigmaSection(ctx, sf.ID, sectionID); err != nil {
		t.Fatalf("link: %v", err)
	}

	got, err := repo.GetSubFlowByFigmaSection(ctx, sectionID)
	if err != nil {
		t.Fatalf("get by section: %v", err)
	}
	if got.ID != sf.ID {
		t.Fatalf("expected id %s, got %s", sf.ID, got.ID)
	}
	if got.FigmaSectionID == nil || *got.FigmaSectionID != sectionID {
		t.Fatalf("figma_section_id not round-tripped: %v", got.FigmaSectionID)
	}

	// Unknown section → ErrNotFound.
	if _, err := repo.GetSubFlowByFigmaSection(ctx, "nope:0"); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound; got %v", err)
	}
}

func TestSubFlow_LinkUniqueAcrossFlows(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	wallet, _ := repo.UpsertSubProduct(ctx, "Wallet")
	sf1, err := repo.UpsertSubFlow(ctx, wallet.ID, "M2M Settlement")
	if err != nil {
		t.Fatalf("sf1: %v", err)
	}
	sf2, err := repo.UpsertSubFlow(ctx, wallet.ID, "Periodic Settlement")
	if err != nil {
		t.Fatalf("sf2: %v", err)
	}

	const sectionID = "1:1"
	if err := repo.LinkSubFlowToFigmaSection(ctx, sf1.ID, sectionID); err != nil {
		t.Fatalf("first link: %v", err)
	}
	err = repo.LinkSubFlowToFigmaSection(ctx, sf2.ID, sectionID)
	if err == nil {
		t.Fatal("expected UNIQUE violation linking second sub_flow to same section")
	}
	if !strings.Contains(err.Error(), "UNIQUE") {
		t.Fatalf("expected UNIQUE error, got %v", err)
	}
}

func TestSubFlow_LinkUnknownID(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	err := repo.LinkSubFlowToFigmaSection(ctx, "nope-id", "1:1")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestSubFlow_EmptyNameRejected(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	wallet, _ := repo.UpsertSubProduct(ctx, "Wallet")
	for _, bad := range []string{"", "   "} {
		if _, err := repo.UpsertSubFlow(ctx, wallet.ID, bad); err == nil {
			t.Fatalf("expected error for empty name %q", bad)
		}
	}
	if _, err := repo.UpsertSubFlow(ctx, "", "Foo"); err == nil {
		t.Fatal("expected error for empty sub_product_id")
	}
}

func TestSubFlow_ListFiltered(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	wallet, _ := repo.UpsertSubProduct(ctx, "Wallet")
	stocks, _ := repo.UpsertSubProduct(ctx, "INDstocks")
	if _, err := repo.UpsertSubFlow(ctx, wallet.ID, "M2M Settlement"); err != nil {
		t.Fatalf("seed1: %v", err)
	}
	if _, err := repo.UpsertSubFlow(ctx, wallet.ID, "Periodic Settlement"); err != nil {
		t.Fatalf("seed2: %v", err)
	}
	if _, err := repo.UpsertSubFlow(ctx, stocks.ID, "F&O"); err != nil {
		t.Fatalf("seed3: %v", err)
	}

	all, err := repo.ListSubFlows(ctx, "")
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3, got %d", len(all))
	}

	onlyWallet, err := repo.ListSubFlows(ctx, wallet.ID)
	if err != nil {
		t.Fatalf("list wallet: %v", err)
	}
	if len(onlyWallet) != 2 {
		t.Fatalf("expected 2 wallet sub_flows, got %d", len(onlyWallet))
	}
	for _, sf := range onlyWallet {
		if sf.SubProductID != wallet.ID {
			t.Fatalf("expected wallet scoping; got %s", sf.SubProductID)
		}
	}

	none, err := repo.ListSubFlows(ctx, "unknown-id")
	if err != nil {
		t.Fatalf("list unknown: %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("expected 0, got %d", len(none))
	}
}

func TestSubFlow_GetSubProductByName(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	sp, err := repo.UpsertSubProduct(ctx, "Wallet")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err := repo.GetSubProductByName(ctx, "  WALLET  ")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != sp.ID {
		t.Fatalf("expected id %s, got %s", sp.ID, got.ID)
	}

	if _, err := repo.GetSubProductByName(ctx, "Missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound; got %v", err)
	}
}

func TestSubFlow_GetSubFlowByName(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	wallet, _ := repo.UpsertSubProduct(ctx, "Wallet")
	created, err := repo.UpsertSubFlow(ctx, wallet.ID, "M2M Settlement")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err := repo.GetSubFlowByName(ctx, wallet.ID, "m2m settlement")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != created.ID {
		t.Fatalf("expected id %s, got %s", created.ID, got.ID)
	}

	if _, err := repo.GetSubFlowByName(ctx, wallet.ID, "Missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound; got %v", err)
	}
}

// ─── GetSubFlowByFlowID (plan 005 U1) ────────────────────────────────────────

// seedFlowWithSection creates a project + flow with a given section_id under
// the caller's tenant. Returns the flow row's id. Used by the
// GetSubFlowByFlowID / HandleSubFlowForLeaf tests so they can exercise the
// flows.section_id → sub_flow.figma_section_id binding without re-implementing
// the autosync pipeline.
func seedFlowWithSection(t *testing.T, repo *TenantRepo, userID, sectionID string) string {
	t.Helper()
	ctx := context.Background()
	p, err := repo.UpsertProject(ctx, Project{
		Name: "P", Platform: "mobile", Product: "Plutus",
		Path: "X-" + sectionID, OwnerUserID: userID,
	})
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	f, err := repo.UpsertFlow(ctx, Flow{
		ProjectID: p.ID, FileID: "F-" + sectionID, SectionID: &sectionID,
		Name: "FlowA",
	})
	if err != nil {
		t.Fatalf("flow: %v", err)
	}
	return f.ID
}

func TestGetSubFlowByFlowID_HappyPath(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	wallet, _ := repo.UpsertSubProduct(ctx, "Wallet")
	sf, err := repo.UpsertSubFlow(ctx, wallet.ID, "M2M Settlement")
	if err != nil {
		t.Fatalf("upsert sf: %v", err)
	}
	const sectionID = "sec:1"
	if err := repo.LinkSubFlowToFigmaSection(ctx, sf.ID, sectionID); err != nil {
		t.Fatalf("link: %v", err)
	}
	flowID := seedFlowWithSection(t, repo, uA, sectionID)

	got, err := repo.GetSubFlowByFlowID(ctx, flowID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != sf.ID {
		t.Errorf("sub_flow.id = %s, want %s", got.ID, sf.ID)
	}
	if got.Name != "M2M Settlement" {
		t.Errorf("sub_flow.name = %s, want M2M Settlement", got.Name)
	}
	if got.FigmaSectionID == nil || *got.FigmaSectionID != sectionID {
		t.Errorf("figma_section_id = %v, want %s", got.FigmaSectionID, sectionID)
	}
}

func TestGetSubFlowByFlowID_NoSectionOnFlow(t *testing.T) {
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	// Flow without a section_id at all → freeform flow, no binding possible.
	p, err := repo.UpsertProject(ctx, Project{
		Name: "P", Platform: "mobile", Product: "Plutus", Path: "Free", OwnerUserID: uA,
	})
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	f, err := repo.UpsertFlow(ctx, Flow{
		ProjectID: p.ID, FileID: "F-free", Name: "Freeform",
	})
	if err != nil {
		t.Fatalf("flow: %v", err)
	}

	_, err = repo.GetSubFlowByFlowID(ctx, f.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound for freeform flow; got %v", err)
	}
}

func TestGetSubFlowByFlowID_SectionExistsButNoSubFlow(t *testing.T) {
	// Autosync race: a flow's section_id is stamped during extract, but
	// either no sub_flow row was upserted yet (PM hasn't authored a DRD),
	// or it was upserted without being LinkedToFigmaSection.
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()

	flowID := seedFlowWithSection(t, repo, uA, "sec:orphan")

	_, err := repo.GetSubFlowByFlowID(ctx, flowID)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound when no sub_flow bound to section; got %v", err)
	}

	// Now upsert a sub_flow but DO NOT call LinkSubFlowToFigmaSection —
	// the join should still miss because sub_flow.figma_section_id is NULL.
	wallet, _ := repo.UpsertSubProduct(ctx, "Wallet")
	if _, err := repo.UpsertSubFlow(ctx, wallet.ID, "Unbound"); err != nil {
		t.Fatalf("upsert sf: %v", err)
	}
	_, err = repo.GetSubFlowByFlowID(ctx, flowID)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound when sub_flow unlinked; got %v", err)
	}
}

func TestGetSubFlowByFlowID_CrossTenant(t *testing.T) {
	d, tA, tB, uA := newTestDB(t)
	repoA := NewTenantRepo(d.DB, tA)
	repoB := NewTenantRepo(d.DB, tB)
	ctx := context.Background()

	wallet, _ := repoA.UpsertSubProduct(ctx, "Wallet")
	sf, _ := repoA.UpsertSubFlow(ctx, wallet.ID, "M2M Settlement")
	const sectionID = "sec:cross"
	if err := repoA.LinkSubFlowToFigmaSection(ctx, sf.ID, sectionID); err != nil {
		t.Fatalf("link: %v", err)
	}
	flowID := seedFlowWithSection(t, repoA, uA, sectionID)

	// Tenant A sees it.
	if _, err := repoA.GetSubFlowByFlowID(ctx, flowID); err != nil {
		t.Fatalf("tenant A should resolve: %v", err)
	}
	// Tenant B does not (no oracle).
	if _, err := repoB.GetSubFlowByFlowID(ctx, flowID); !errors.Is(err, ErrNotFound) {
		t.Errorf("tenant B should not see tenant A's flow: %v", err)
	}
}

func TestGetSubFlowByFlowID_UnknownFlowID(t *testing.T) {
	d, tA, _, _ := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	if _, err := repo.GetSubFlowByFlowID(context.Background(), "nope-flow-id"); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound for unknown flow_id; got %v", err)
	}
	if _, err := repo.GetSubFlowByFlowID(context.Background(), ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound for empty flow_id; got %v", err)
	}
}

func TestSubFlow_TenantIsolation(t *testing.T) {
	d, tA, tB, _ := newTestDB(t)
	repoA := NewTenantRepo(d.DB, tA)
	repoB := NewTenantRepo(d.DB, tB)
	ctx := context.Background()

	walletA, err := repoA.UpsertSubProduct(ctx, "Wallet")
	if err != nil {
		t.Fatalf("walletA: %v", err)
	}
	sfA, err := repoA.UpsertSubFlow(ctx, walletA.ID, "M2M Settlement")
	if err != nil {
		t.Fatalf("sfA: %v", err)
	}

	// Tenant B cannot see tenant A's rows.
	if _, err := repoB.GetSubProductByName(ctx, "Wallet"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound across tenant; got %v", err)
	}
	if _, err := repoB.GetSubFlowBySlug(ctx, "wallet/m2m-settlement"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound across tenant; got %v", err)
	}
	if all, err := repoB.ListSubFlows(ctx, ""); err != nil {
		t.Fatalf("list B: %v", err)
	} else if len(all) != 0 {
		t.Fatalf("tenant B sees %d rows; want 0", len(all))
	}

	// Tenant B can create independently with the same names.
	walletB, err := repoB.UpsertSubProduct(ctx, "Wallet")
	if err != nil {
		t.Fatalf("walletB: %v", err)
	}
	if walletB.ID == walletA.ID {
		t.Fatal("expected distinct ids across tenants for same name")
	}
	sfB, err := repoB.UpsertSubFlow(ctx, walletB.ID, "M2M Settlement")
	if err != nil {
		t.Fatalf("sfB: %v", err)
	}
	if sfB.ID == sfA.ID {
		t.Fatal("expected distinct sub_flow ids across tenants")
	}
}
