package projects

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/auth"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/sse"
)

// ─── Pure-function tests ────────────────────────────────────────────────────

func TestAssetStreamChannel_ParseRoundTrip(t *testing.T) {
	cases := []struct {
		tenantID, leafID string
	}{
		{"tenant-1", "leaf-abc"},
		{"e090530f-2698-489d-934a-c821cb925c8a", "8eda5e4a-ad26-46a7-b118-9428b43111b4"},
		{"t", "l"}, // single-char minimums
	}
	for _, c := range cases {
		channel := assetStreamChannel(c.tenantID, c.leafID)
		gotTenant, gotLeaf, ok := parseAssetStreamChannel(channel)
		if !ok {
			t.Errorf("parse(%q) ok=false; want true", channel)
			continue
		}
		if gotTenant != c.tenantID {
			t.Errorf("parse(%q) tenant=%q; want %q", channel, gotTenant, c.tenantID)
		}
		if gotLeaf != c.leafID {
			t.Errorf("parse(%q) leaf=%q; want %q", channel, gotLeaf, c.leafID)
		}
	}
}

func TestParseAssetStreamChannel_Malformed(t *testing.T) {
	bad := []string{
		"",                                     // empty
		"graph:t:l",                            // wrong prefix
		"assets:",                              // missing both
		"assets:tenant",                        // missing colon between tenant and leaf
		"assets::leaf",                         // empty tenant (idx <= 0 path)
		"assets:tenant:",                       // empty leaf (idx == len-1 path)
		"someprefix-assets:tenant:leaf-suffix", // prefix substring, not at start
	}
	for _, s := range bad {
		if _, _, ok := parseAssetStreamChannel(s); ok {
			t.Errorf("parse(%q) returned ok=true; want false", s)
		}
	}
}

func TestIsAssetStreamChannel_PrefixCheck(t *testing.T) {
	want := map[string]bool{
		"assets:t:l":  true,
		"assets:":     true, // valid prefix even if rest empty (parse separately rejects)
		"graph:t:l":   false,
		"":            false,
		"asset:t:l":   false, // singular, off-by-one
		"AssetS:t:l":  false, // case-sensitive
		"xassets:t:l": false,
	}
	for s, expected := range want {
		if got := isAssetStreamChannel(s); got != expected {
			t.Errorf("isAssetStreamChannel(%q) = %v; want %v", s, got, expected)
		}
	}
}

func TestDedupeClusterIDs_DeduplicatesAcrossScreens(t *testing.T) {
	// Two screens, overlapping cluster IDs, mixed name/structural-cluster
	// patterns from node-classifier. shouldRasterize → ExtractClusterIDs's
	// real walker decides; here we feed canonical_tree shapes that the
	// existing extractor accepts (vector-only subtrees + icon-named paths).
	tree1 := `{"document":{"id":"root","type":"FRAME","children":[
		{"id":"icon-A","type":"INSTANCE","name":"Icons/Home","absoluteBoundingBox":{"x":0,"y":0,"width":24,"height":24},"children":[
			{"id":"v1","type":"VECTOR","absoluteBoundingBox":{"x":0,"y":0,"width":24,"height":24}}
		]},
		{"id":"icon-B","type":"INSTANCE","name":"Icons/Search","absoluteBoundingBox":{"x":30,"y":0,"width":24,"height":24},"children":[
			{"id":"v2","type":"VECTOR","absoluteBoundingBox":{"x":0,"y":0,"width":24,"height":24}}
		]}
	]}}`
	tree2 := `{"document":{"id":"root2","type":"FRAME","children":[
		{"id":"icon-A","type":"INSTANCE","name":"Icons/Home","absoluteBoundingBox":{"x":0,"y":0,"width":24,"height":24},"children":[
			{"id":"v1","type":"VECTOR","absoluteBoundingBox":{"x":0,"y":0,"width":24,"height":24}}
		]},
		{"id":"icon-C","type":"INSTANCE","name":"Icons/Settings","absoluteBoundingBox":{"x":60,"y":0,"width":24,"height":24},"children":[
			{"id":"v3","type":"VECTOR","absoluteBoundingBox":{"x":0,"y":0,"width":24,"height":24}}
		]}
	]}}`

	got := dedupeClusterIDs([]CanonicalTreeResult{
		{ScreenID: "s1", Tree: tree1},
		{ScreenID: "s2", Tree: tree2},
	})

	// Build a set of unique IDs without order assumptions for the dedup
	// check, then separately assert first-seen ordering for icon-A.
	seen := map[string]int{}
	for i, id := range got {
		if existing, dup := seen[id]; dup {
			t.Errorf("duplicate cluster id %q at positions %d and %d", id, existing, i)
		}
		seen[id] = i
	}
	for _, want := range []string{"icon-A", "icon-B", "icon-C"} {
		if _, ok := seen[want]; !ok {
			t.Errorf("missing expected cluster id %q in result %v", want, got)
		}
	}
	// icon-A appears in tree1 first, so its index in the deduped slice
	// should precede icon-C (which only shows up in tree2).
	if seen["icon-A"] >= seen["icon-C"] {
		t.Errorf("first-seen ordering violated: icon-A at %d, icon-C at %d",
			seen["icon-A"], seen["icon-C"])
	}
}

func TestDedupeClusterIDs_EmptyAndMalformed(t *testing.T) {
	if got := dedupeClusterIDs(nil); got != nil {
		t.Errorf("nil input → nil; got %v", got)
	}
	if got := dedupeClusterIDs([]CanonicalTreeResult{}); got != nil {
		t.Errorf("empty input → nil; got %v", got)
	}
	// Malformed JSON falls through ExtractClusterIDs's nil return — should
	// produce an empty slice, NOT panic.
	got := dedupeClusterIDs([]CanonicalTreeResult{
		{ScreenID: "s1", Tree: "not valid json"},
		{ScreenID: "s2", Tree: ""},
	})
	if len(got) != 0 {
		t.Errorf("malformed trees → 0 ids; got %v", got)
	}
}

// ─── Test-server helper ─────────────────────────────────────────────────────

// newAssetStreamTestServer wires a *Server with the dependencies needed for
// asset_stream handlers: Tickets store, AssetSigner, AssetExporter, plus an
// in-memory test DB seeded with one project + flow + version. Returns
// (server, tenantID, userID, slug, fileID, leafID).
//
// Mirrors newAssetU5Server but adds the Tickets store the stream auth path
// requires. PreviewPyramid is wired with a stub source fetcher so the
// per-cluster render path can short-circuit when a test pre-populates the
// asset_cache (cache-hit path).
func newAssetStreamTestServer(t *testing.T) (srv *Server, tenantID, userID, slug, fileID, leafID string) {
	t.Helper()
	d, tA, _, uA := newTestDB(t)
	repo := NewTenantRepo(d.DB, tA)
	ctx := context.Background()
	fileID = "FILE-STREAM"
	p, err := repo.UpsertProject(ctx, Project{
		Name: "StreamProj", Platform: "mobile", Product: "Plutus",
		Path: "OB", OwnerUserID: uA, FileID: fileID,
	})
	if err != nil {
		t.Fatalf("upsert project: %v", err)
	}
	flow, err := repo.UpsertFlow(ctx, Flow{
		ProjectID: p.ID, FileID: fileID, Name: "Flow",
	})
	if err != nil {
		t.Fatalf("upsert flow: %v", err)
	}
	if _, err := repo.CreateVersion(ctx, p.ID, uA); err != nil {
		t.Fatalf("create version: %v", err)
	}

	dataDir := t.TempDir()
	signer, err := auth.NewAssetTokenSigner(bytes.Repeat([]byte("k"), 32))
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	tickets := sse.NewMemoryTicketStore(0)
	t.Cleanup(tickets.Close)

	exporter := &AssetExporter{
		Repo:    NewTenantRepo(d.DB, ""),
		URLs:    &fakeURLFetcher{},
		Bytes:   &fakeByteFetcher{payload: []byte("PNG")},
		DataDir: dataDir,
		Limiter: &figmaRateLimiter{buckets: map[string]*figmaBucket{}},
		Now:     time.Now,
	}

	srv = NewServer(ServerDeps{
		DB:            d,
		DataDir:       dataDir,
		AssetSigner:   signer,
		AssetExporter: exporter,
		Tickets:       tickets,
		AuditLogger:   &AuditLogger{DB: d},
		Log:           nil,
	})
	return srv, tA, uA, p.Slug, fileID, flow.ID
}

// ─── HandleAssetStreamTicket ────────────────────────────────────────────────

func TestHandleAssetStreamTicket_HappyPath(t *testing.T) {
	srv, tA, uA, slug, _, leafID := newAssetStreamTestServer(t)
	r := httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/v1/projects/%s/leaves/%s/asset-stream/ticket", slug, leafID),
		strings.NewReader(`{}`))
	r.SetPathValue("slug", slug)
	r.SetPathValue("leaf_id", leafID)
	r = r.WithContext(WithClaims(context.Background(),
		&auth.Claims{Sub: uA, Tenants: []string{tA}}))
	w := httptest.NewRecorder()

	srv.HandleAssetStreamTicket(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `"ticket":"`) {
		t.Errorf("response missing ticket field: %s", body)
	}
	wantChannel := assetStreamChannel(tA, leafID)
	if !strings.Contains(body, wantChannel) {
		t.Errorf("response missing trace_id %q: %s", wantChannel, body)
	}
}

func TestHandleAssetStreamTicket_RejectsNonPost(t *testing.T) {
	srv, tA, uA, slug, _, leafID := newAssetStreamTestServer(t)
	r := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/v1/projects/%s/leaves/%s/asset-stream/ticket", slug, leafID), nil)
	r.SetPathValue("slug", slug)
	r.SetPathValue("leaf_id", leafID)
	r = r.WithContext(WithClaims(context.Background(),
		&auth.Claims{Sub: uA, Tenants: []string{tA}}))
	w := httptest.NewRecorder()

	srv.HandleAssetStreamTicket(w, r)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status=%d; want 405", w.Code)
	}
}

func TestHandleAssetStreamTicket_MissingClaims_401(t *testing.T) {
	srv, _, _, slug, _, leafID := newAssetStreamTestServer(t)
	r := httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/v1/projects/%s/leaves/%s/asset-stream/ticket", slug, leafID),
		strings.NewReader(`{}`))
	r.SetPathValue("slug", slug)
	r.SetPathValue("leaf_id", leafID)
	// No claims attached.
	w := httptest.NewRecorder()

	srv.HandleAssetStreamTicket(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status=%d; want 401", w.Code)
	}
}

func TestHandleAssetStreamTicket_LeafNotFound_404(t *testing.T) {
	srv, tA, uA, slug, _, _ := newAssetStreamTestServer(t)
	bogusLeaf := uuid.NewString()
	r := httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/v1/projects/%s/leaves/%s/asset-stream/ticket", slug, bogusLeaf),
		strings.NewReader(`{}`))
	r.SetPathValue("slug", slug)
	r.SetPathValue("leaf_id", bogusLeaf)
	r = r.WithContext(WithClaims(context.Background(),
		&auth.Claims{Sub: uA, Tenants: []string{tA}}))
	w := httptest.NewRecorder()

	srv.HandleAssetStreamTicket(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("status=%d; want 404 for unknown leaf", w.Code)
	}
}

// TestHandleAssetStreamTicket_SlugAsLeafID_OK pins the slug-as-leafID
// fallback that the post-brain-products frontend relies on. Pre-fix the
// ticket handler would 404 because LookupLeafFigmaContext rejected the
// slug. Post-fix the handler resolves slug → primary flow before lookup,
// matching ResolveImageRefsForLeaf's behaviour.
func TestHandleAssetStreamTicket_SlugAsLeafID_OK(t *testing.T) {
	srv, tA, uA, slug, _, _ := newAssetStreamTestServer(t)
	r := httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/v1/projects/%s/leaves/%s/asset-stream/ticket", slug, slug),
		strings.NewReader(`{}`))
	r.SetPathValue("slug", slug)
	r.SetPathValue("leaf_id", slug) // slug == leaf_id triggers fallback
	r = r.WithContext(WithClaims(context.Background(),
		&auth.Claims{Sub: uA, Tenants: []string{tA}}))
	w := httptest.NewRecorder()

	srv.HandleAssetStreamTicket(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	// Trace ID is the slug-keyed channel (since the path leaf_id passed
	// through unchanged on the channel-name level — the resolve happens
	// later, only when the GET stream actually walks canonical_trees).
	wantChannel := assetStreamChannel(tA, slug)
	if !strings.Contains(w.Body.String(), wantChannel) {
		t.Errorf("response missing slug-keyed trace_id %q: %s",
			wantChannel, w.Body.String())
	}
}

// ─── HandleAssetStream — auth + channel guards ──────────────────────────────

func TestHandleAssetStream_MissingTicket_401(t *testing.T) {
	srv, _, _, slug, _, leafID := newAssetStreamTestServer(t)
	r := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/v1/projects/%s/leaves/%s/asset-stream", slug, leafID), nil)
	r.SetPathValue("slug", slug)
	r.SetPathValue("leaf_id", leafID)
	w := httptest.NewRecorder()

	srv.HandleAssetStream(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status=%d; want 401 for missing ticket", w.Code)
	}
}

func TestHandleAssetStream_TokenInQueryString_400(t *testing.T) {
	srv, _, _, slug, _, leafID := newAssetStreamTestServer(t)
	r := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/v1/projects/%s/leaves/%s/asset-stream?token=eyJraWQ", slug, leafID), nil)
	r.SetPathValue("slug", slug)
	r.SetPathValue("leaf_id", leafID)
	w := httptest.NewRecorder()

	srv.HandleAssetStream(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status=%d; want 400 for ?token= passthrough", w.Code)
	}
}

func TestHandleAssetStream_WrongChannel_403(t *testing.T) {
	srv, tA, uA, slug, _, leafID := newAssetStreamTestServer(t)
	// Issue a ticket bound to a non-asset channel — e.g. the graph events
	// channel — and try to redeem it on the asset-stream endpoint.
	graphTrace := "graph:" + tA + ":mobile"
	ticket := srv.deps.Tickets.IssueTicket(uA, tA, graphTrace, sse.DefaultTicketTTL)

	r := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/v1/projects/%s/leaves/%s/asset-stream?ticket=%s", slug, leafID, ticket), nil)
	r.SetPathValue("slug", slug)
	r.SetPathValue("leaf_id", leafID)
	w := httptest.NewRecorder()

	srv.HandleAssetStream(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("status=%d; want 403 for wrong-channel ticket", w.Code)
	}
}

func TestHandleAssetStream_LeafMismatch_403(t *testing.T) {
	srv, tA, uA, slug, _, leafID := newAssetStreamTestServer(t)
	// Issue a ticket bound to leaf A, redeem on leaf B's path.
	ticket := srv.deps.Tickets.IssueTicket(uA, tA,
		assetStreamChannel(tA, leafID), sse.DefaultTicketTTL)

	otherLeaf := uuid.NewString()
	r := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/v1/projects/%s/leaves/%s/asset-stream?ticket=%s", slug, otherLeaf, ticket), nil)
	r.SetPathValue("slug", slug)
	r.SetPathValue("leaf_id", otherLeaf)
	w := httptest.NewRecorder()

	srv.HandleAssetStream(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("status=%d; want 403 for leaf mismatch", w.Code)
	}
}

// ─── HandleAssetStream — happy paths ────────────────────────────────────────

func TestHandleAssetStream_NoClusters_EmitsCompleteOnly(t *testing.T) {
	// A flow with no screens (and therefore no canonical_trees) produces
	// zero cluster IDs; the stream should emit `event: complete` and close
	// without firing any asset-ready events.
	srv, tA, uA, slug, _, leafID := newAssetStreamTestServer(t)
	ticket := srv.deps.Tickets.IssueTicket(uA, tA,
		assetStreamChannel(tA, leafID), sse.DefaultTicketTTL)

	r := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/v1/projects/%s/leaves/%s/asset-stream?ticket=%s", slug, leafID, ticket), nil)
	r.SetPathValue("slug", slug)
	r.SetPathValue("leaf_id", leafID)
	w := httptest.NewRecorder()

	srv.HandleAssetStream(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "event: complete") {
		t.Errorf("missing complete event: %s", body)
	}
	if strings.Contains(body, "event: asset-ready") {
		t.Errorf("unexpected asset-ready event for empty leaf: %s", body)
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type=%q; want text/event-stream", ct)
	}
}

func TestHandleAssetStream_CacheHit_EmitsAssetReady(t *testing.T) {
	// Pre-populate one screen + canonical_tree containing a single icon
	// cluster, then warm the asset_cache for that cluster's preview-128
	// tier. The stream should emit one asset-ready event with a signed
	// URL pointing at the GET /assets/{node} download path.
	srv, tA, uA, slug, fileID, leafID := newAssetStreamTestServer(t)
	repo := NewTenantRepo(srv.deps.DB.DB, tA)
	ctx := context.Background()

	// Resolve version_id (CreateVersion was called in the helper) so we
	// can attach screens + canonical_trees to the same version the
	// stream will look up.
	versionID, err := repo.resolveLatestVersionID(ctx, leafID)
	if err != nil {
		t.Fatalf("resolve version: %v", err)
	}
	versionIndex, err := repo.GetVersionIndex(ctx, versionID)
	if err != nil {
		t.Fatalf("version index: %v", err)
	}

	scr := []Screen{{
		VersionID: versionID, FlowID: leafID,
		X: 0, Y: 0, Width: 375, Height: 812,
	}}
	if err := repo.InsertScreens(ctx, scr); err != nil {
		t.Fatalf("insert screens: %v", err)
	}
	clusterNodeID := "icon-1"
	tree := fmt.Sprintf(`{"document":{"id":"root","type":"FRAME","children":[
		{"id":%q,"type":"INSTANCE","name":"Icons/Home","absoluteBoundingBox":{"x":0,"y":0,"width":24,"height":24},"children":[
			{"id":"v1","type":"VECTOR","absoluteBoundingBox":{"x":0,"y":0,"width":24,"height":24}}
		]}
	]}}`, clusterNodeID)
	if _, err := srv.deps.DB.DB.ExecContext(ctx,
		`INSERT INTO screen_canonical_trees(screen_id, canonical_tree, hash, updated_at)
		 VALUES (?, ?, ?, datetime('now'))`,
		scr[0].ID, tree, "h"); err != nil {
		t.Fatalf("insert canonical tree: %v", err)
	}

	// Warm the asset_cache for preview-128 so the stream takes the
	// cache-hit fast path (zero Figma traffic).
	installFakeAsset(t, srv, tA, fileID, clusterNodeID,
		AssetStreamDefaultTier, 1, versionIndex, []byte("PNG"))

	ticket := srv.deps.Tickets.IssueTicket(uA, tA,
		assetStreamChannel(tA, leafID), sse.DefaultTicketTTL)

	r := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/v1/projects/%s/leaves/%s/asset-stream?ticket=%s", slug, leafID, ticket), nil)
	r.SetPathValue("slug", slug)
	r.SetPathValue("leaf_id", leafID)
	w := httptest.NewRecorder()

	srv.HandleAssetStream(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "event: asset-ready") {
		t.Errorf("missing asset-ready event for cached cluster: %s", body)
	}
	if !strings.Contains(body, fmt.Sprintf(`"node_id":%q`, clusterNodeID)) {
		t.Errorf("asset-ready missing expected node_id %q: %s", clusterNodeID, body)
	}
	wantPath := fmt.Sprintf("/v1/projects/%s/assets/%s", slug, clusterNodeID)
	if !strings.Contains(body, wantPath) {
		t.Errorf("asset-ready URL missing expected path %q: %s", wantPath, body)
	}
	if !strings.Contains(body, "format=preview-128") {
		t.Errorf("asset-ready URL missing default tier preview-128: %s", body)
	}
	if !strings.Contains(body, "at=") {
		t.Errorf("asset-ready URL missing signed token: %s", body)
	}
	if !strings.Contains(body, "event: complete") {
		t.Errorf("stream did not emit complete event: %s", body)
	}
}

// TestHandleAssetStream_TicketRedeemedOnce verifies the underlying
// MemoryTicketStore single-use semantics propagate through the handler:
// a redeemed ticket cannot be reused by a second GET. Defense-in-depth
// against ticket replay if a signed URL leaks via referrer / log scrape.
func TestHandleAssetStream_TicketRedeemedOnce(t *testing.T) {
	srv, tA, uA, slug, _, leafID := newAssetStreamTestServer(t)
	ticket := srv.deps.Tickets.IssueTicket(uA, tA,
		assetStreamChannel(tA, leafID), sse.DefaultTicketTTL)

	// First redemption succeeds.
	r1 := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/v1/projects/%s/leaves/%s/asset-stream?ticket=%s", slug, leafID, ticket), nil)
	r1.SetPathValue("slug", slug)
	r1.SetPathValue("leaf_id", leafID)
	w1 := httptest.NewRecorder()
	srv.HandleAssetStream(w1, r1)
	if w1.Code != http.StatusOK {
		t.Fatalf("first redeem status=%d body=%s", w1.Code, w1.Body.String())
	}

	// Second redemption with the same ticket must fail with 401.
	r2 := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/v1/projects/%s/leaves/%s/asset-stream?ticket=%s", slug, leafID, ticket), nil)
	r2.SetPathValue("slug", slug)
	r2.SetPathValue("leaf_id", leafID)
	w2 := httptest.NewRecorder()
	srv.HandleAssetStream(w2, r2)
	if w2.Code != http.StatusUnauthorized {
		t.Errorf("second redeem status=%d; want 401 (single-use)", w2.Code)
	}
}

// Sanity guard so future renames don't silently change the stream's
// default tier without updating the frontend's pickPreviewTier
// expectations.
func TestAssetStreamDefaultTier_IsPreview128(t *testing.T) {
	if AssetStreamDefaultTier != "preview-128" {
		t.Errorf("default tier drifted from preview-128 to %q — frontend "+
			"useIconClusterURLs assumes preview-128 for first-paint",
			AssetStreamDefaultTier)
	}
	if _, ok := ParsePreviewTierFormat(AssetStreamDefaultTier); !ok {
		t.Errorf("default tier %q not recognised by ParsePreviewTierFormat",
			AssetStreamDefaultTier)
	}
}

