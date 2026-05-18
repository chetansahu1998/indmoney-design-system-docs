package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/db"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/projects"
)

// ─── Test harness ──────────────────────────────────────────────────────────

// testHarness bundles the DB, two tenants, a user, and a wired registry
// so each test can do `h := newTestHarness(t)` and reach for `h.deps` /
// `h.registry` / `h.invoke`.
type testHarness struct {
	t        *testing.T
	d        *db.DB
	tenantA  string
	tenantB  string
	userA    string
	registry *Registry
	deps     Deps
}

func newTestHarness(t *testing.T) *testHarness {
	t.Helper()
	dir := t.TempDir()
	d, err := db.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("db open: %v", err)
	}
	t.Cleanup(func() { d.Close() })

	ctx := context.Background()
	userA := uuid.NewString()
	userB := uuid.NewString()
	if err := d.CreateUser(ctx, db.User{
		ID: userA, Email: "a@example.com",
		PasswordHash: "x", Role: "user", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("create userA: %v", err)
	}
	if err := d.CreateUser(ctx, db.User{
		ID: userB, Email: "b@example.com",
		PasswordHash: "x", Role: "user", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("create userB: %v", err)
	}
	tenantA := uuid.NewString()
	tenantB := uuid.NewString()
	if err := d.CreateTenant(ctx, db.Tenant{
		ID: tenantA, Slug: "tenant-a", Name: "A", Status: "active",
		PlanType: "free", CreatedAt: time.Now(), CreatedBy: userA,
	}); err != nil {
		t.Fatalf("create tenantA: %v", err)
	}
	if err := d.CreateTenant(ctx, db.Tenant{
		ID: tenantB, Slug: "tenant-b", Name: "B", Status: "active",
		PlanType: "free", CreatedAt: time.Now(), CreatedBy: userB,
	}); err != nil {
		t.Fatalf("create tenantB: %v", err)
	}

	repo := projects.NewTenantRepo(d.DB, tenantA)
	h := &testHarness{
		t:        t,
		d:        d,
		tenantA:  tenantA,
		tenantB:  tenantB,
		userA:    userA,
		registry: NewDefaultRegistry(),
		deps: Deps{
			Repo:   repo,
			UserID: userA,
		},
	}
	return h
}

// invoke is a convenience wrapper that marshals + dispatches.
func (h *testHarness) invoke(name string, args any) (Result, error) {
	h.t.Helper()
	var raw json.RawMessage
	if args != nil {
		b, err := json.Marshal(args)
		if err != nil {
			h.t.Fatalf("marshal args for %s: %v", name, err)
		}
		raw = b
	}
	return h.registry.Invoke(context.Background(), name, h.deps, raw)
}

func (h *testHarness) seedSubFlow(productName, flowName string) projects.SubFlow {
	h.t.Helper()
	ctx := context.Background()
	sp, err := h.deps.Repo.UpsertSubProduct(ctx, productName)
	if err != nil {
		h.t.Fatalf("upsert sub_product: %v", err)
	}
	sf, err := h.deps.Repo.UpsertSubFlow(ctx, sp.ID, flowName)
	if err != nil {
		h.t.Fatalf("upsert sub_flow: %v", err)
	}
	return sf
}

// seedSubFlowForTenantB writes a sub_product + sub_flow under tenantB
// (not the harness's default tenantA). Used by cross-tenant assertion
// tests (/ce-code-review #14) so the assertion "tenantA can't see this"
// has actual data on the other side — otherwise a not-found result
// would pass even if tenant scoping were removed.
func (h *testHarness) seedSubFlowForTenantB(productName, flowName string) projects.SubFlow {
	h.t.Helper()
	ctx := context.Background()
	repoB := projects.NewTenantRepo(h.d.DB, h.tenantB)
	sp, err := repoB.UpsertSubProduct(ctx, productName)
	if err != nil {
		h.t.Fatalf("upsert sub_product (tenantB): %v", err)
	}
	sf, err := repoB.UpsertSubFlow(ctx, sp.ID, flowName)
	if err != nil {
		h.t.Fatalf("upsert sub_flow (tenantB): %v", err)
	}
	return sf
}

// ─── Registry-level tests ──────────────────────────────────────────────────

// TestRegistry_ColdCatalog_ReturnsExpectedVisibleTools — U6 promoted 11
// prd.author sub-ops to Visible (3 meta-verbs + 11 = 14). /ce-code-review
// #23 added drd.list_anchors to Visible (hot-path read for Atlas) = 15.
// `prd.author` stays Visible as a deprecated alias.
func TestRegistry_ColdCatalog_ReturnsExpectedVisibleTools(t *testing.T) {
	r := NewDefaultRegistry()
	visible := r.ListVisible()
	wantNames := map[string]bool{
		// Original meta-verbs
		"drd.read":        false,
		"prd.author":      false,
		"section.inspect": false,
		// U6 promotions — the 11 PRD sub-ops
		"prd.get":                      false,
		"prd.upsert_tab":               false,
		"prd.add_state":                false,
		"prd.add_event":                false,
		"prd.add_acceptance_criterion": false,
		"prd.add_edge_case":            false,
		"prd.upsert_copy_string":       false,
		"prd.add_a11y_note":            false,
		"prd.attach_frame":             false,
		"prd.detach_frame":             false,
		"prd.export":                   false,
		// /ce-code-review #23 — hot-path read consumed on every leaf-open.
		"drd.list_anchors": false,
		// /ce-code-review #4 + #5 — Atlas-parity capability gaps closed.
		"comment.create": false,
		"comment.list":   false,
		"activity.list":  false,
	}
	if got, want := len(visible), len(wantNames); got != want {
		names := []string{}
		for _, v := range visible {
			names = append(names, v.Name())
		}
		t.Fatalf("expected %d visible tools, got %d: %v", want, got, names)
	}
	for _, t2 := range visible {
		if _, ok := wantNames[t2.Name()]; !ok {
			t.Errorf("unexpected visible tool: %q", t2.Name())
		}
		wantNames[t2.Name()] = true
	}
	for n, seen := range wantNames {
		if !seen {
			t.Errorf("expected visible tool not present: %q", n)
		}
	}
}

func TestRegistry_InvokeUnknownTool_ReturnsErrToolNotFound(t *testing.T) {
	h := newTestHarness(t)
	_, err := h.invoke("does.not.exist", map[string]any{})
	if !errors.Is(err, ErrToolNotFound) {
		t.Fatalf("expected ErrToolNotFound, got %v", err)
	}
}

func TestRegistry_ColdCatalogTokenBudget_UnderPostU6Ceiling(t *testing.T) {
	r := NewDefaultRegistry()
	visible := r.ListVisible()
	type catEntry struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		InputSchema json.RawMessage `json:"input_schema"`
	}
	entries := make([]catEntry, 0, len(visible))
	for _, v := range visible {
		entries = append(entries, catEntry{
			Name:        v.Name(),
			Description: v.Description(),
			InputSchema: v.InputSchema(),
		})
	}
	bytes, err := json.Marshal(entries)
	if err != nil {
		t.Fatalf("marshal catalog: %v", err)
	}
	// Cold-catalog budget evolution:
	//   - Pre-U5: 3 visible tools, terse descriptions, ~1500 bytes.
	//   - Post-U5: 3 visible tools, boundary-aware descriptions +
	//     per-prop schemas, ~2200 bytes (budget 4000 with margin).
	//   - Post-U6: 14 visible tools, ~14 KB (budget 16 KB).
	//   - /ce-code-review (current): 18 visible tools — added
	//     drd.list_anchors (#23 hot-path promotion) + comment.create /
	//     comment.list (#4) + activity.list (#5), ~17.5 KB. KTD-5's
	//     "stay well under 30 simultaneously-loaded tools" still holds
	//     (18 ≪ 30); absolute size stays below any practical system-
	//     prompt budget. Budget bumped to 20 KB for headroom on the
	//     next promotion.
	const budget = 20000
	if len(bytes) > budget {
		t.Errorf("cold catalog %d bytes > budget %d — KTD-5 budget broken", len(bytes), budget)
		t.Logf("catalog JSON:\n%s", string(bytes))
	}
}

// TestAllToolsHaveBoundaryDescriptions enforces plan 002 U5 — every
// registered tool must:
//   - state both "Use when" and "Don't use when" in its Description();
//   - keep the description ≤1200 chars (≈150 words; arxiv 2602.14878 cap);
//   - annotate every leaf JSON-Schema property with a non-empty
//     "description" field. Recurses through `properties` and
//     `items.properties`. Enum-bearing properties are accepted as long
//     as the property itself has a description.
//
// Walker shape mirrors the existing cold-catalog snapshot pattern above.
func TestAllToolsHaveBoundaryDescriptions(t *testing.T) {
	r := NewDefaultRegistry()
	all := r.ListAll()
	if len(all) == 0 {
		t.Fatal("expected NewDefaultRegistry to register at least one tool")
	}
	const descMaxChars = 1200
	for _, tool := range all {
		name := tool.Name()
		desc := tool.Description()
		if desc == "" {
			t.Errorf("%s: Description() is empty", name)
			continue
		}
		if !strings.Contains(desc, "Use when") {
			t.Errorf("%s: Description() missing \"Use when\" trigger: %q", name, desc)
		}
		if !strings.Contains(desc, "Don't use when") {
			t.Errorf("%s: Description() missing \"Don't use when\" boundary: %q", name, desc)
		}
		if len(desc) > descMaxChars {
			t.Errorf("%s: Description() exceeds %d chars (%d): %q",
				name, descMaxChars, len(desc), desc)
		}

		raw := tool.InputSchema()
		var schema map[string]any
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Errorf("%s: InputSchema() not valid JSON: %v", name, err)
			continue
		}
		walkSchemaProperties(t, name, "", schema)
	}
}

// walkSchemaProperties recursively asserts that every leaf property in a
// JSON-Schema object carries a non-empty "description". Nested objects
// recurse via `properties`; arrays recurse via `items.properties`. The
// pathPrefix string accumulates a dotted address for error messages so a
// missing description on a deeply nested arg points at the exact slot.
func walkSchemaProperties(t *testing.T, toolName, pathPrefix string, schema map[string]any) {
	t.Helper()
	propsAny, ok := schema["properties"]
	if !ok {
		return
	}
	props, ok := propsAny.(map[string]any)
	if !ok {
		return
	}
	for propName, propAny := range props {
		prop, ok := propAny.(map[string]any)
		if !ok {
			t.Errorf("%s: property %q%s is not an object", toolName, propName, pathPrefix)
			continue
		}
		fullPath := propName
		if pathPrefix != "" {
			fullPath = pathPrefix + "." + propName
		}
		descRaw, hasDesc := prop["description"]
		descStr, _ := descRaw.(string)
		if !hasDesc || strings.TrimSpace(descStr) == "" {
			t.Errorf("%s: property %q is missing a non-empty \"description\"", toolName, fullPath)
		}
		// Recurse into nested object properties.
		if typ, _ := prop["type"].(string); typ == "object" {
			walkSchemaProperties(t, toolName, fullPath, prop)
		}
		// Recurse into array item properties when items is itself an object schema.
		if itemsAny, ok := prop["items"]; ok {
			if items, ok := itemsAny.(map[string]any); ok {
				walkSchemaProperties(t, toolName, fullPath+"[]", items)
			}
		}
	}
}

// ─── subflow.* tests ────────────────────────────────────────────────────────

func TestSubflowCreate_HappyPath_CreatesSubProductAndSubFlow(t *testing.T) {
	h := newTestHarness(t)
	res, err := h.invoke("subflow.create", map[string]any{
		"sub_product": "Wallet",
		"sub_flow":    "M2M Settlement",
	})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	created, ok := res.Data.(subflowCreateResult)
	if !ok {
		t.Fatalf("unexpected Data shape: %T", res.Data)
	}
	if created.SubProduct.Slug != "wallet" {
		t.Errorf("sub_product slug: got %q, want wallet", created.SubProduct.Slug)
	}
	if created.SubFlow.Slug != "m2m-settlement" {
		t.Errorf("sub_flow slug: got %q, want m2m-settlement", created.SubFlow.Slug)
	}
	if created.SubFlow.FullSlug != "wallet/m2m-settlement" {
		t.Errorf("full_slug: got %q", created.SubFlow.FullSlug)
	}
	if len(res.NextActions) == 0 {
		t.Errorf("expected NextActions hints, got none")
	}
}

func TestSubflowCreate_WithPrototype_AttachesPrototype(t *testing.T) {
	h := newTestHarness(t)
	res, err := h.invoke("subflow.create", map[string]any{
		"sub_product":     "Wallet",
		"sub_flow":        "Cold State",
		"prototype_url":   "https://example.com/proto/cold-state",
		"prototype_title": "Cold state v1",
	})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	out := res.Data.(subflowCreateResult)
	if out.Prototype == nil || out.Prototype.URL != "https://example.com/proto/cold-state" {
		t.Errorf("prototype not attached: %+v", out.Prototype)
	}
	if out.SubFlow.PrototypeURL == nil || *out.SubFlow.PrototypeURL != "https://example.com/proto/cold-state" {
		t.Errorf("sub_flow.PrototypeURL not reloaded: %+v", out.SubFlow.PrototypeURL)
	}
}

func TestSubflowList_HappyPath_EmptyFilterReturnsAllForTenant(t *testing.T) {
	h := newTestHarness(t)
	h.seedSubFlow("Wallet", "M2M")
	h.seedSubFlow("Wallet", "Cold State")
	h.seedSubFlow("INDstocks", "F&O")

	res, err := h.invoke("subflow.list", map[string]any{})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	out := res.Data.([]subflowSummary)
	if len(out) != 3 {
		t.Fatalf("got %d sub_flows, want 3", len(out))
	}
}

func TestSubflowList_FilterBySubProductSlug(t *testing.T) {
	h := newTestHarness(t)
	h.seedSubFlow("Wallet", "M2M")
	h.seedSubFlow("Wallet", "Cold State")
	h.seedSubFlow("INDstocks", "F&O")

	res, err := h.invoke("subflow.list", map[string]any{"sub_product_filter": "wallet"})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	out := res.Data.([]subflowSummary)
	if len(out) != 2 {
		t.Fatalf("got %d sub_flows, want 2 (wallet only)", len(out))
	}
	for _, sf := range out {
		if sf.SubProductSlug != "wallet" {
			t.Errorf("expected sub_product wallet, got %q", sf.SubProductSlug)
		}
	}
}

func TestSubflowGet_HappyPath_RoundTrip(t *testing.T) {
	h := newTestHarness(t)
	h.seedSubFlow("Wallet", "M2M Settlement")

	res, err := h.invoke("subflow.get", map[string]any{"slug": "wallet/m2m-settlement"})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	out := res.Data.(subflowSummary)
	if out.Name != "M2M Settlement" {
		t.Errorf("name: got %q, want M2M Settlement", out.Name)
	}
}

// ─── drd.* tests ───────────────────────────────────────────────────────────

func TestDRDRead_NoDRDYet_DRDIsNil(t *testing.T) {
	h := newTestHarness(t)
	h.seedSubFlow("Wallet", "M2M")
	res, err := h.invoke("drd.read", map[string]any{"sub_flow_slug": "wallet/m2m"})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	out := res.Data.(drdReadResult)
	if out.DRD != nil {
		t.Errorf("expected DRD == nil when no snapshot; got %+v", out.DRD)
	}
	// Should suggest drd.append to seed.
	foundAppend := false
	for _, h := range res.NextActions {
		if h.Tool == "drd.append" {
			foundAppend = true
			break
		}
	}
	if !foundAppend {
		t.Errorf("expected drd.append next-action hint; got %+v", res.NextActions)
	}
}

func TestDRDAppend_HappyPath_CreatesFlowAndPersistsSnapshot(t *testing.T) {
	h := newTestHarness(t)
	h.seedSubFlow("Wallet", "M2M")

	payload := []byte{0x01, 0x02, 0x03, 0x04}
	b64 := base64.StdEncoding.EncodeToString(payload)
	res, err := h.invoke("drd.append", map[string]any{
		"sub_flow_slug":        "wallet/m2m",
		"content_bytes_base64": b64,
	})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	out := res.Data.(drdAppendResult)
	if out.FlowID == "" {
		t.Errorf("expected flow_id")
	}
	if out.Revision != 1 {
		t.Errorf("revision: got %d, want 1", out.Revision)
	}
	if out.Bytes != len(payload) {
		t.Errorf("bytes: got %d, want %d", out.Bytes, len(payload))
	}

	// Confirm DRD now reads back via drd.read.
	res2, err := h.invoke("drd.read", map[string]any{"sub_flow_slug": "wallet/m2m"})
	if err != nil {
		t.Fatalf("read invoke: %v", err)
	}
	rr := res2.Data.(drdReadResult)
	if rr.DRD == nil || rr.DRD.Bytes != len(payload) {
		t.Errorf("expected DRD round-trip; got %+v", rr.DRD)
	}
}

func TestDRDAttachPrototype_HappyPath(t *testing.T) {
	h := newTestHarness(t)
	h.seedSubFlow("Wallet", "Pre-design")

	_, err := h.invoke("drd.attach_prototype", map[string]any{
		"sub_flow_slug": "wallet/pre-design",
		"url":           "https://prototype.example.com/wallet",
		"title":         "Wallet preview",
	})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	// Confirm via subflow.get.
	res, err := h.invoke("subflow.get", map[string]any{"slug": "wallet/pre-design"})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	out := res.Data.(subflowSummary)
	if out.PrototypeURL == nil || *out.PrototypeURL != "https://prototype.example.com/wallet" {
		t.Errorf("prototype not attached: %+v", out.PrototypeURL)
	}
}

// ─── prd.* tests ────────────────────────────────────────────────────────────

func TestPRDGet_NoPRDYet_NoteOnly(t *testing.T) {
	h := newTestHarness(t)
	h.seedSubFlow("Wallet", "M2M")
	res, err := h.invoke("prd.get", map[string]any{"sub_flow_slug": "wallet/m2m"})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	m, ok := res.Data.(map[string]any)
	if !ok {
		t.Fatalf("unexpected Data shape: %T", res.Data)
	}
	if m["prd"] != nil {
		t.Errorf("expected prd:nil, got %+v", m["prd"])
	}
}

func TestPRDAuthor_AddState_HappyPath(t *testing.T) {
	h := newTestHarness(t)
	h.seedSubFlow("Wallet", "M2M")

	res, err := h.invoke("prd.author", map[string]any{
		"op": "add_state",
		"args": map[string]any{
			"sub_flow_slug":      "wallet/m2m",
			"tab_name":           "Investment",
			"label":              "Cold state",
			"position":           0,
			"condition_md":       "User has zero balance.",
			"design_handling_md": "Show empty hero.",
		},
	})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	state, ok := res.Data.(projects.PRDState)
	if !ok {
		t.Fatalf("unexpected Data shape: %T", res.Data)
	}
	if state.Label != "Cold state" {
		t.Errorf("label: got %q", state.Label)
	}

	// next_actions should include add_acceptance_criterion / add_event /
	// attach_frame. Plan 002 U6 promoted these to top-level prd.* tools;
	// hints now point at the promoted names rather than the prd.author
	// dispatch shim.
	wantTools := map[string]bool{
		"prd.add_acceptance_criterion": false,
		"prd.add_event":                false,
		"prd.attach_frame":             false,
	}
	for _, h := range res.NextActions {
		if _, ok := wantTools[h.Tool]; ok {
			wantTools[h.Tool] = true
		}
	}
	for tool, seen := range wantTools {
		if !seen {
			t.Errorf("expected next-action tool %q", tool)
		}
	}
}

func TestPRDAuthor_UnknownOp_ReturnsInvalidArgsListingValidOps(t *testing.T) {
	h := newTestHarness(t)
	h.seedSubFlow("Wallet", "M2M")
	_, err := h.invoke("prd.author", map[string]any{
		"op":   "nope",
		"args": map[string]any{"sub_flow_slug": "wallet/m2m"},
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	if !errors.Is(err, ErrInvalidArgs) {
		t.Errorf("expected ErrInvalidArgs, got %v", err)
	}
	if !strings.Contains(err.Error(), "add_state") {
		t.Errorf("error should enumerate valid ops, got %v", err)
	}
}

func TestPRDExport_RoundTripMarkdown(t *testing.T) {
	h := newTestHarness(t)
	h.seedSubFlow("Wallet", "M2M")
	// Seed a state via prd.author.
	if _, err := h.invoke("prd.author", map[string]any{
		"op": "add_state",
		"args": map[string]any{
			"sub_flow_slug": "wallet/m2m",
			"tab_name":      "Investment",
			"label":         "Hot state",
		},
	}); err != nil {
		t.Fatalf("add_state: %v", err)
	}
	res, err := h.invoke("prd.author", map[string]any{
		"op":   "export",
		"args": map[string]any{"sub_flow_slug": "wallet/m2m"},
	})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	out := res.Data.(prdExportResult)
	if out.Markdown == "" {
		t.Errorf("expected markdown, got empty")
	}
	if !strings.Contains(out.Markdown, "Hot state") {
		t.Errorf("expected markdown to mention 'Hot state'; got %s", out.Markdown)
	}
}

// ─── section.* tests ────────────────────────────────────────────────────────

func TestSectionFrames_NoSectionBound_EmptyArrayWithNote(t *testing.T) {
	h := newTestHarness(t)
	h.seedSubFlow("Wallet", "Pre-design")
	res, err := h.invoke("section.frames", map[string]any{
		"sub_flow_slug": "wallet/pre-design",
	})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	out := res.Data.(sectionFramesResult)
	if len(out.Frames) != 0 {
		t.Errorf("expected empty frames, got %d", len(out.Frames))
	}
	if out.Note == "" {
		t.Errorf("expected explanatory note when no section bound")
	}
}

// U6b replaced the stub with the real wall implementation; the U6 stub
// assertion was deleted. Wall-shape coverage lives in
// internal/projects/prd_outline_test.go.

func TestSectionInspect_HappyPath_ReturnsSummary(t *testing.T) {
	h := newTestHarness(t)
	h.seedSubFlow("Wallet", "M2M")

	// Add a state so PRDSummary picks it up.
	if _, err := h.invoke("prd.author", map[string]any{
		"op": "add_state",
		"args": map[string]any{
			"sub_flow_slug": "wallet/m2m",
			"tab_name":      "Investment",
			"label":         "Cold",
		},
	}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	res, err := h.invoke("section.inspect", map[string]any{"sub_flow_slug": "wallet/m2m"})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	out := res.Data.(sectionInspectResult)
	if out.SubFlow.FullSlug != "wallet/m2m" {
		t.Errorf("full_slug: got %q", out.SubFlow.FullSlug)
	}
	if !out.PRDSummary.Exists {
		t.Errorf("expected PRD to exist after add_state")
	}
	if out.PRDSummary.StateCount != 1 {
		t.Errorf("expected 1 state, got %d", out.PRDSummary.StateCount)
	}
	if len(res.NextActions) == 0 {
		t.Errorf("expected next_actions, got none")
	}
	// next_actions must point at drd.read + the promoted top-level
	// prd.get / prd.add_state tools (plan 002 U6 — no more prd.author Op
	// envelope wrap).
	tools := map[string]bool{}
	for _, h := range res.NextActions {
		tools[h.Tool] = true
	}
	if !tools["drd.read"] {
		t.Errorf("missing drd.read hint")
	}
	if !tools["prd.get"] {
		t.Errorf("missing prd.get hint")
	}
}

// ─── Tenant isolation ──────────────────────────────────────────────────────

func TestTenantIsolation_SubflowsFromTenantBNotVisible(t *testing.T) {
	h := newTestHarness(t)

	// Seed sub_flow under tenantA via the registered repo.
	h.seedSubFlow("Wallet", "M2M")

	// Build a tenantB repo + invoke through the same registry.
	repoB := projects.NewTenantRepo(h.d.DB, h.tenantB)
	depsB := Deps{Repo: repoB, UserID: h.userA}
	resB, err := h.registry.Invoke(context.Background(), "subflow.list", depsB, nil)
	if err != nil {
		t.Fatalf("invoke under tenantB: %v", err)
	}
	out := resB.Data.([]subflowSummary)
	if len(out) != 0 {
		t.Errorf("tenant isolation broken: tenantB sees %d sub_flows", len(out))
	}
	// And subflow.get from tenantB should miss.
	_, err = h.registry.Invoke(context.Background(), "subflow.get",
		depsB, json.RawMessage(`{"slug": "wallet/m2m"}`))
	if err == nil {
		t.Fatalf("expected not-found error from tenantB lookup, got nil")
	}
}

// TestRegistry_AllToolsHaveCompleteMetadata enforces plan 002 U4 — every
// registered tool must implement the extended Tool interface with a
// non-empty Title, a valid SideEffects classification, and a sensible
// DeferLoading default (Visible→false, Deep→true).
func TestRegistry_AllToolsHaveCompleteMetadata(t *testing.T) {
	r := NewDefaultRegistry()
	all := r.ListAll()
	if len(all) == 0 {
		t.Fatal("expected NewDefaultRegistry to register at least one tool")
	}
	for _, tool := range all {
		name := tool.Name()
		titled, hasTitle := tool.(ToolTitled)
		if !hasTitle {
			t.Errorf("%s: missing ToolTitled (no Title() method)", name)
		} else if title := titled.Title(); title == "" {
			t.Errorf("%s: Title() is empty", name)
		} else if len(title) > 60 {
			t.Errorf("%s: Title() exceeds 60 chars (%d): %q", name, len(title), title)
		}
		sided, hasSide := tool.(ToolSideEffected)
		if !hasSide {
			t.Errorf("%s: missing ToolSideEffected (no SideEffects() method)", name)
		} else {
			side := sided.SideEffects()
			if side < ReadOnly || side > Destructive {
				t.Errorf("%s: SideEffects() out of valid range: %d", name, side)
			}
		}
		deferable, hasDefer := tool.(ToolDeferable)
		if !hasDefer {
			t.Errorf("%s: missing ToolDeferable (no DeferLoading() method)", name)
			continue
		}
		visible := tool.Visibility() == Visible
		d := deferable.DeferLoading()
		// Default convention: Visible→false, Deep→true. The interface
		// allows override, but flag anything unexpected for review.
		if visible && d {
			t.Errorf("%s: Visible tool with DeferLoading()==true — Claude eager-loads visible tools, this annotation is contradictory", name)
		}
		if !visible && !d {
			t.Logf("note: %s is Deep but DeferLoading()==false — verify this is intentional", name)
		}
	}
}
