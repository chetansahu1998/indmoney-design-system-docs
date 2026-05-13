package projects

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/auth"
)

// server_organism_admin_test.go — U10 endpoint tests. Drives the three
// HTTP handlers through httptest with seeded organism + promotion data,
// confirming auth, response shape, ranking, and tenant isolation.

func seedAdminFixture(t *testing.T) (*Server, *auth.Claims, orgTestFixture) {
	t.Helper()
	fx := seedOrgFixture(t)
	rows := []DetectedOrganismMatch{
		{VersionID: fx.versionID, FrameID: "f1", ScreenID: fx.screenID,
			SuspectedSlug: "list-on-surface", MatchKind: "exact",
			FingerprintHash: "h1", AtomSignatureJSON: `["a","b"]`, SlotTopologyJSON: `[]`,
			Confidence: 1.0, ManifestHash: "mh"},
		{VersionID: fx.versionID, FrameID: "f2", ScreenID: fx.screenID,
			SuspectedSlug: "list-on-surface", MatchKind: "near",
			FingerprintHash: "h2", AtomSignatureJSON: `[]`, SlotTopologyJSON: `[]`,
			Confidence: 0.7, ManifestHash: "mh"},
		{VersionID: fx.versionID, FrameID: "f3", ScreenID: fx.screenID,
			SuspectedSlug: "list-on-surface", MatchKind: "near",
			FingerprintHash: "h3", AtomSignatureJSON: `[]`, SlotTopologyJSON: `[]`,
			Confidence: 0.6, ManifestHash: "mh"},
		{VersionID: fx.versionID, FrameID: "f4", ScreenID: fx.screenID,
			MatchKind: "novel", FingerprintHash: "h4",
			AtomSignatureJSON: `[]`, SlotTopologyJSON: `[]`,
			Confidence: 0.0, ManifestHash: "mh"},
		{VersionID: fx.versionID, FrameID: "f5", ScreenID: fx.screenID,
			MatchKind: "novel", FingerprintHash: "h5",
			AtomSignatureJSON: `[]`, SlotTopologyJSON: `[]`,
			Confidence: 0.0, ManifestHash: "mh"},
		{VersionID: fx.versionID, FrameID: "f6", ScreenID: fx.screenID,
			MatchKind: "novel", FingerprintHash: "h6",
			AtomSignatureJSON: `[]`, SlotTopologyJSON: `[]`,
			Confidence: 0.0, ManifestHash: "mh"},
	}
	if err := fx.repo.UpsertOrganismMatches(context.Background(), rows); err != nil {
		t.Fatalf("seed organism matches: %v", err)
	}

	srv := &Server{deps: ServerDeps{DB: fx.db}}
	claims := &auth.Claims{Sub: fx.userID, Email: "t@x", Tenants: []string{fx.tenantA}}
	return srv, claims, fx
}

func callHandler(t *testing.T, h http.HandlerFunc, method, target string, claims *auth.Claims) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	if claims != nil {
		req = req.WithContext(context.WithValue(req.Context(), ctxKeyClaims, claims))
	}
	w := httptest.NewRecorder()
	h(w, req)
	return w
}

// ─── HandleOrganismAdoption ──────────────────────────────────────────────────

func TestOrganismAdoption_Basic(t *testing.T) {
	srv, claims, _ := seedAdminFixture(t)
	w := callHandler(t, srv.HandleOrganismAdoption, http.MethodGet,
		"/v1/admin/organisms/adoption", claims)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Rows                  []OrganismAdoptionRow `json:"rows"`
		TotalMatches          int                   `json:"total_matches"`
		SignatureCatalogEmpty bool                  `json:"signature_catalog_empty"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if resp.TotalMatches != 6 {
		t.Errorf("total_matches = %d; want 6", resp.TotalMatches)
	}
	if resp.SignatureCatalogEmpty {
		t.Error("signature_catalog_empty should be false — list-on-surface slug present")
	}
	if len(resp.Rows) != 2 {
		t.Fatalf("expected 2 rows; got %d", len(resp.Rows))
	}
	if resp.Rows[0].Slug != "list-on-surface" {
		t.Errorf("first row slug = %q; want list-on-surface", resp.Rows[0].Slug)
	}
	if resp.Rows[0].Exact != 1 || resp.Rows[0].Near != 2 {
		t.Errorf("list-on-surface counts wrong: %+v", resp.Rows[0])
	}
	if resp.Rows[1].Slug != "" {
		t.Errorf("second row slug = %q; want empty (novel bucket)", resp.Rows[1].Slug)
	}
	if resp.Rows[1].Novel != 3 {
		t.Errorf("novel bucket count = %d; want 3", resp.Rows[1].Novel)
	}
}

func TestOrganismAdoption_EmptyCatalog(t *testing.T) {
	srv, claims, fx := seedAdminFixture(t)
	if _, err := fx.db.DB.ExecContext(context.Background(),
		`DELETE FROM detected_organism_match WHERE tenant_id = ?`, fx.tenantA); err != nil {
		t.Fatalf("wipe: %v", err)
	}
	if err := fx.repo.UpsertOrganismMatches(context.Background(), []DetectedOrganismMatch{
		{VersionID: fx.versionID, FrameID: "n1", ScreenID: fx.screenID, MatchKind: "novel",
			FingerprintHash: "h1", AtomSignatureJSON: "[]", SlotTopologyJSON: "[]",
			Confidence: 0, ManifestHash: "mh"},
	}); err != nil {
		t.Fatalf("reseed: %v", err)
	}
	w := callHandler(t, srv.HandleOrganismAdoption, http.MethodGet, "/v1/admin/organisms/adoption", claims)
	var resp struct {
		SignatureCatalogEmpty bool `json:"signature_catalog_empty"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if !resp.SignatureCatalogEmpty {
		t.Error("expected signature_catalog_empty=true with only novel rows")
	}
}

func TestOrganismAdoption_Unauthorized(t *testing.T) {
	srv, _, _ := seedAdminFixture(t)
	w := callHandler(t, srv.HandleOrganismAdoption, http.MethodGet, "/v1/admin/organisms/adoption", nil)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d; want 401", w.Code)
	}
}

func TestOrganismAdoption_MethodNotAllowed(t *testing.T) {
	srv, claims, _ := seedAdminFixture(t)
	w := callHandler(t, srv.HandleOrganismAdoption, http.MethodPost, "/v1/admin/organisms/adoption", claims)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d; want 405", w.Code)
	}
}

// ─── HandleOrganismMatchesBySlug ─────────────────────────────────────────────

type matchesResp struct {
	Matches []organismMatchDTO `json:"matches"`
	Slug    string             `json:"slug"`
	Count   int                `json:"count"`
}

func callMatchesBySlug(t *testing.T, srv *Server, claims *auth.Claims, slug, kind string) matchesResp {
	t.Helper()
	url := "/v1/admin/organisms/" + slug + "/matches"
	if kind != "" {
		url += "?kind=" + kind
	}
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.SetPathValue("slug", slug)
	req = req.WithContext(context.WithValue(req.Context(), ctxKeyClaims, claims))
	w := httptest.NewRecorder()
	srv.HandleOrganismMatchesBySlug(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", w.Code, w.Body.String())
	}
	var r matchesResp
	if err := json.Unmarshal(w.Body.Bytes(), &r); err != nil {
		t.Fatalf("parse: %v", err)
	}
	return r
}

func TestOrganismMatchesBySlug_All(t *testing.T) {
	srv, claims, _ := seedAdminFixture(t)
	r := callMatchesBySlug(t, srv, claims, "list-on-surface", "")
	if r.Count != 3 {
		t.Errorf("list-on-surface count = %d; want 3", r.Count)
	}
}

func TestOrganismMatchesBySlug_KindFilter(t *testing.T) {
	srv, claims, _ := seedAdminFixture(t)
	r := callMatchesBySlug(t, srv, claims, "list-on-surface", "exact")
	if r.Count != 1 || r.Matches[0].MatchKind != "exact" {
		t.Errorf("list-on-surface exact filter wrong: %+v", r)
	}
}

func TestOrganismMatchesBySlug_UnmatchedAlias(t *testing.T) {
	srv, claims, _ := seedAdminFixture(t)
	r := callMatchesBySlug(t, srv, claims, "_unmatched", "")
	if r.Count != 3 {
		t.Errorf("_unmatched count = %d; want 3", r.Count)
	}
	for _, m := range r.Matches {
		if m.SuspectedSlug != "" {
			t.Errorf("_unmatched returned row with slug=%q", m.SuspectedSlug)
		}
	}
}

func TestOrganismMatchesBySlug_MissingSlug(t *testing.T) {
	srv, claims, _ := seedAdminFixture(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/organisms//matches", nil)
	req = req.WithContext(context.WithValue(req.Context(), ctxKeyClaims, claims))
	w := httptest.NewRecorder()
	srv.HandleOrganismMatchesBySlug(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d; want 400", w.Code)
	}
}

// ─── HandleOrganismPromotionCandidates ───────────────────────────────────────

func TestOrganismPromotionCandidates_Ranked(t *testing.T) {
	srv, claims, fx := seedAdminFixture(t)
	if err := fx.repo.UpsertPromotionCandidates(context.Background(), []PromotionCandidate{
		{FingerprintHash: "low", Frequency: 3, FileCount: 2, StabilityScore: 0.5, AtomReuseRate: 0.5},
		{FingerprintHash: "high", Frequency: 10, FileCount: 5, StabilityScore: 0.9, AtomReuseRate: 0.95},
		{FingerprintHash: "mid", Frequency: 5, FileCount: 3, StabilityScore: 0.7, AtomReuseRate: 0.7},
	}); err != nil {
		t.Fatalf("seed candidates: %v", err)
	}
	w := callHandler(t, srv.HandleOrganismPromotionCandidates, http.MethodGet,
		"/v1/admin/organisms/promotion-candidates", claims)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Candidates []promotionCandidateDTO `json:"candidates"`
		Count      int                     `json:"count"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if resp.Count != 3 {
		t.Errorf("count = %d; want 3", resp.Count)
	}
	want := []string{"high", "mid", "low"}
	for i, w := range want {
		if resp.Candidates[i].FingerprintHash != w {
			t.Errorf("position %d: got %q want %q", i, resp.Candidates[i].FingerprintHash, w)
		}
	}
}

func TestOrganismPromotionCandidates_EmptyOK(t *testing.T) {
	srv, claims, _ := seedAdminFixture(t)
	w := callHandler(t, srv.HandleOrganismPromotionCandidates, http.MethodGet,
		"/v1/admin/organisms/promotion-candidates", claims)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d", w.Code)
	}
	var resp struct {
		Count int `json:"count"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Count != 0 {
		t.Errorf("count = %d; want 0", resp.Count)
	}
}

// ─── HandleOrganismVerdictLookup (U7) ───────────────────────────────────────

func callVerdictLookup(t *testing.T, srv *Server, claims *auth.Claims, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/audit/organism-match",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if claims != nil {
		req = req.WithContext(context.WithValue(req.Context(), ctxKeyClaims, claims))
	}
	w := httptest.NewRecorder()
	srv.HandleOrganismVerdictLookup(w, req)
	return w
}

func TestOrganismVerdictLookup_Hit(t *testing.T) {
	srv, claims, _ := seedAdminFixture(t)
	w := callVerdictLookup(t, srv, claims, `{"node_id":"f1"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Verdict *organismMatchDTO `json:"verdict"`
		Reason  string            `json:"reason,omitempty"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if resp.Verdict == nil {
		t.Fatalf("expected verdict object; got nil (reason=%q)", resp.Reason)
	}
	if resp.Verdict.FrameID != "f1" {
		t.Errorf("frame_id = %q; want f1", resp.Verdict.FrameID)
	}
	if resp.Verdict.SuspectedSlug != "list-on-surface" {
		t.Errorf("suspected_slug = %q; want list-on-surface", resp.Verdict.SuspectedSlug)
	}
	if resp.Verdict.MatchKind != "exact" {
		t.Errorf("match_kind = %q; want exact", resp.Verdict.MatchKind)
	}
}

func TestOrganismVerdictLookup_Miss(t *testing.T) {
	srv, claims, _ := seedAdminFixture(t)
	w := callVerdictLookup(t, srv, claims, `{"node_id":"never-imported"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Verdict *organismMatchDTO `json:"verdict"`
		Reason  string            `json:"reason"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Verdict != nil {
		t.Errorf("expected verdict=null on miss; got %+v", resp.Verdict)
	}
	if resp.Reason != "no_import_covers_this_frame" {
		t.Errorf("reason = %q; want no_import_covers_this_frame", resp.Reason)
	}
}

func TestOrganismVerdictLookup_MissingNodeID(t *testing.T) {
	srv, claims, _ := seedAdminFixture(t)
	// Empty body
	w := callVerdictLookup(t, srv, claims, `{}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("empty: status = %d; want 400", w.Code)
	}
	// Whitespace-only
	w = callVerdictLookup(t, srv, claims, `{"node_id":"   "}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("whitespace: status = %d; want 400", w.Code)
	}
}

func TestOrganismVerdictLookup_BadJSON(t *testing.T) {
	srv, claims, _ := seedAdminFixture(t)
	w := callVerdictLookup(t, srv, claims, `{not json}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d; want 400", w.Code)
	}
}

func TestOrganismVerdictLookup_Unauthorized(t *testing.T) {
	srv, _, _ := seedAdminFixture(t)
	w := callVerdictLookup(t, srv, nil, `{"node_id":"f1"}`)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d; want 401", w.Code)
	}
}

func TestOrganismVerdictLookup_MethodNotAllowed(t *testing.T) {
	srv, claims, _ := seedAdminFixture(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/audit/organism-match", nil)
	req = req.WithContext(context.WithValue(req.Context(), ctxKeyClaims, claims))
	w := httptest.NewRecorder()
	srv.HandleOrganismVerdictLookup(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d; want 405", w.Code)
	}
}

func TestOrganismVerdictLookup_TenantIsolation(t *testing.T) {
	srv, _, fx := seedAdminFixture(t)
	// Tenant B asks about a frame_id seeded for tenant A → miss.
	bClaims := &auth.Claims{Sub: "userB", Email: "b@x", Tenants: []string{fx.tenantB}}
	w := callVerdictLookup(t, srv, bClaims, `{"node_id":"f1"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var resp struct {
		Verdict *organismMatchDTO `json:"verdict"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Verdict != nil {
		t.Errorf("tenant B saw tenant A's verdict: %+v", resp.Verdict)
	}
}

// ─── HandleOrganismForkMark + verdict surfacing (U9) ────────────────────────

func callForkMark(t *testing.T, srv *Server, claims *auth.Claims, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/audit/organism-match/fork",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if claims != nil {
		req = req.WithContext(context.WithValue(req.Context(), ctxKeyClaims, claims))
	}
	w := httptest.NewRecorder()
	srv.HandleOrganismForkMark(w, req)
	return w
}

func TestOrganismForkMark_HappyPath(t *testing.T) {
	srv, claims, fx := seedAdminFixture(t)
	w := callForkMark(t, srv, claims, `{"node_id":"f1","reason":"Custom color override for brand campaign"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", w.Code, w.Body.String())
	}
	// Verify it landed via direct DB read.
	mark, err := fx.repo.LookupOrganismForkMark(context.Background(), "f1")
	if err != nil {
		t.Fatalf("lookup after upsert: %v", err)
	}
	if mark.Reason != "Custom color override for brand campaign" {
		t.Errorf("reason = %q; want the seeded reason", mark.Reason)
	}
	if mark.MarkedByUserID != fx.userID {
		t.Errorf("marked_by_user_id = %q; want %q", mark.MarkedByUserID, fx.userID)
	}
}

func TestOrganismForkMark_VerdictSurfacesIntentionalFork(t *testing.T) {
	srv, claims, _ := seedAdminFixture(t)
	// First fork-mark.
	w := callForkMark(t, srv, claims, `{"node_id":"f1","reason":"approved by DS lead"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("fork-mark failed: %d %s", w.Code, w.Body.String())
	}
	// Now the verdict lookup should include is_intentional_fork.
	w = callVerdictLookup(t, srv, claims, `{"node_id":"f1"}`)
	var resp struct {
		Verdict           *organismMatchDTO `json:"verdict"`
		IsIntentionalFork bool              `json:"is_intentional_fork"`
		ForkReason        string            `json:"fork_reason"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if resp.Verdict == nil {
		t.Fatal("expected verdict object")
	}
	if !resp.IsIntentionalFork {
		t.Error("expected is_intentional_fork=true")
	}
	if resp.ForkReason != "approved by DS lead" {
		t.Errorf("fork_reason = %q; want 'approved by DS lead'", resp.ForkReason)
	}
}

func TestOrganismForkMark_Idempotent(t *testing.T) {
	srv, claims, fx := seedAdminFixture(t)
	// First mark.
	callForkMark(t, srv, claims, `{"node_id":"f1","reason":"first reason"}`)
	// Second mark — same frame, new reason — overwrites.
	w := callForkMark(t, srv, claims, `{"node_id":"f1","reason":"updated reason"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("second mark: %d", w.Code)
	}
	mark, err := fx.repo.LookupOrganismForkMark(context.Background(), "f1")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if mark.Reason != "updated reason" {
		t.Errorf("reason = %q; want 'updated reason' (overwrite)", mark.Reason)
	}
}

func TestOrganismForkMark_MissingNodeID(t *testing.T) {
	srv, claims, _ := seedAdminFixture(t)
	w := callForkMark(t, srv, claims, `{}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d; want 400", w.Code)
	}
}

func TestOrganismForkMark_Unauthorized(t *testing.T) {
	srv, _, _ := seedAdminFixture(t)
	w := callForkMark(t, srv, nil, `{"node_id":"f1"}`)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d; want 401", w.Code)
	}
}

func TestOrganismForkMark_TenantIsolation(t *testing.T) {
	srv, claims, fx := seedAdminFixture(t)
	// Tenant A marks frame f1.
	callForkMark(t, srv, claims, `{"node_id":"f1","reason":"tenant A note"}`)

	// Tenant B's verdict lookup for the same frame must NOT see fork-mark
	// (and won't see a verdict either since rows are tenant-scoped — but
	// we want the fork lookup specifically to also stay isolated).
	bClaims := &auth.Claims{Sub: "userB", Email: "b@x", Tenants: []string{fx.tenantB}}
	_, err := fx.otherRepo.LookupOrganismForkMark(context.Background(), "f1")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("tenant B leaked tenant A fork-mark; err=%v", err)
	}
	// Sanity — tenant B's verdict response also doesn't carry the flag.
	w := callVerdictLookup(t, srv, bClaims, `{"node_id":"f1"}`)
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if v, ok := resp["is_intentional_fork"]; ok && v == true {
		t.Errorf("tenant B saw is_intentional_fork=true; should be absent")
	}
}

// ─── Tenant isolation ────────────────────────────────────────────────────────

func TestOrganismAdmin_TenantIsolation(t *testing.T) {
	srv, _, fx := seedAdminFixture(t)
	bClaims := &auth.Claims{Sub: "userB", Email: "b@x", Tenants: []string{fx.tenantB}}
	w := callHandler(t, srv.HandleOrganismAdoption, http.MethodGet, "/v1/admin/organisms/adoption", bClaims)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var resp struct {
		TotalMatches int `json:"total_matches"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.TotalMatches != 0 {
		t.Errorf("tenant B leaked %d rows", resp.TotalMatches)
	}
}
