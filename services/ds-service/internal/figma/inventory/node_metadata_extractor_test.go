// node_metadata_extractor_test.go — U5 of plan 2026-05-17-004.
//
// Covers ExtractForFile end-to-end (Figma response → DB rows), the
// pure buildNodeMetadataRows pipe in isolation, and the filter +
// batching + idempotency contracts the integration depends on.
package inventory

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/db"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/projects"
)

// fakeNodesFetcher records every call and returns a canned response per
// (file_key, csv(ids)) key. Tests register expectations up-front; an
// unregistered call falls through to an empty-children response so
// missing-section paths can be exercised without per-test plumbing.
type fakeNodesFetcher struct {
	responses map[string]map[string]any
	calls     int32
	wantErr   error
}

func (f *fakeNodesFetcher) GetFileNodes(_ context.Context, fileKey string, nodeIDs []string, _ int) (map[string]any, error) {
	atomic.AddInt32(&f.calls, 1)
	if f.wantErr != nil {
		return nil, f.wantErr
	}
	key := fileKey
	for _, id := range nodeIDs {
		key += "|" + id
	}
	if resp, ok := f.responses[key]; ok {
		return resp, nil
	}
	// Default: every section has empty children.
	nodes := map[string]any{}
	for _, id := range nodeIDs {
		nodes[id] = map[string]any{"document": map[string]any{"children": []any{}}}
	}
	return map[string]any{"nodes": nodes}, nil
}

// node builds one child entry inside a section's document.children.
func node(id, typ, name string, x, y, w, h float64) map[string]any {
	return map[string]any{
		"id": id, "type": typ, "name": name,
		"absoluteBoundingBox": map[string]any{
			"x": x, "y": y, "width": w, "height": h,
		},
	}
}

// sectionResponse builds the {document: {children: [...]}} entry for one section.
func sectionResponse(children ...map[string]any) map[string]any {
	ch := make([]any, 0, len(children))
	for _, c := range children {
		ch = append(ch, c)
	}
	return map[string]any{
		"document": map[string]any{
			"id":       "section-doc",
			"type":     "SECTION",
			"children": ch,
		},
	}
}

// extractorFixture bundles the test DB, the seeded tenant id, the repo
// wired at that tenant, and the extractor.
type extractorFixture struct {
	d        *db.DB
	tenantID string
	repo     *projects.TenantRepo
	ext      *NodeMetadataExtractor
}

// setupExtractor stands up a test DB and wires an extractor against the
// supplied fake fetcher.
func setupExtractor(t *testing.T, fc *fakeNodesFetcher) extractorFixture {
	t.Helper()
	d, tenantID, _ := newPlannerTestDB(t)
	repo := projects.NewTenantRepo(d.DB, tenantID)
	ext := &NodeMetadataExtractor{
		ResolvePAT: func(_ context.Context, _ string) (string, error) { return "pat-fake", nil },
		NewClient:  func(_ string) FigmaNodesFetcher { return fc },
		Repo:       func(_ string) *projects.TenantRepo { return repo },
	}
	return extractorFixture{d: d, tenantID: tenantID, repo: repo, ext: ext}
}

// countMetadataRows queries figma_node_metadata directly via the test DB.
func countMetadataRows(t *testing.T, d *db.DB, tenantID, fileKey string) int {
	t.Helper()
	var n int
	err := d.DB.QueryRow(
		`SELECT COUNT(*) FROM figma_node_metadata WHERE tenant_id = ? AND file_key = ?`,
		tenantID, fileKey,
	).Scan(&n)
	if err != nil {
		t.Fatalf("count rows: %v", err)
	}
	return n
}

// TestExtractor_HappyPath — 3 sections, 4 FRAME children each → 12 rows.
func TestExtractor_HappyPath(t *testing.T) {
	const fk = "fkH"
	fc := &fakeNodesFetcher{responses: map[string]map[string]any{}}
	fc.responses[fk+"|sec1|sec2|sec3"] = map[string]any{
		"nodes": map[string]any{
			"sec1": sectionResponse(
				node("n1a", "FRAME", "A1", 0, 0, 100, 50),
				node("n1b", "FRAME", "A2", 0, 60, 100, 50),
				node("n1c", "FRAME", "A3", 0, 120, 100, 50),
				node("n1d", "FRAME", "A4", 0, 180, 100, 50),
			),
			"sec2": sectionResponse(
				node("n2a", "FRAME", "B1", 0, 0, 50, 50),
				node("n2b", "FRAME", "B2", 0, 60, 50, 50),
				node("n2c", "FRAME", "B3", 0, 120, 50, 50),
				node("n2d", "FRAME", "B4", 0, 180, 50, 50),
			),
			"sec3": sectionResponse(
				node("n3a", "FRAME", "C1", 0, 0, 50, 50),
				node("n3b", "FRAME", "C2", 0, 60, 50, 50),
				node("n3c", "FRAME", "C3", 0, 120, 50, 50),
				node("n3d", "FRAME", "C4", 0, 180, 50, 50),
			),
		},
	}
	fx := setupExtractor(t, fc)
	pageMap := map[string]string{"sec1": "p1", "sec2": "p1", "sec3": "p1"}

	written, err := fx.ext.ExtractForFile(context.Background(), fx.tenantID, fk,
		[]string{"sec1", "sec2", "sec3"}, pageMap)
	if err != nil {
		t.Fatalf("ExtractForFile: %v", err)
	}
	if written != 12 {
		t.Fatalf("written: got %d want 12", written)
	}
	if got := atomic.LoadInt32(&fc.calls); got != 1 {
		t.Errorf("api calls: got %d want 1", got)
	}
	if got := countMetadataRows(t, fx.d, fx.tenantID, fk); got != 12 {
		t.Errorf("db rows: got %d want 12", got)
	}
}

// TestExtractor_FiltersNonFrameTypes — only FRAME/INSTANCE/COMPONENT
// children persist; TEXT, VECTOR, RECTANGLE, GROUP drop out.
func TestExtractor_FiltersNonFrameTypes(t *testing.T) {
	const fk = "fkF"
	fc := &fakeNodesFetcher{responses: map[string]map[string]any{}}
	mixed := sectionResponse(
		node("k1", "FRAME", "Frame", 0, 0, 10, 10),
		node("k2", "TEXT", "Label", 0, 0, 10, 10),
		node("k3", "VECTOR", "Vec", 0, 0, 10, 10),
		node("k4", "RECTANGLE", "Rect", 0, 0, 10, 10),
		node("k5", "GROUP", "Grp", 0, 0, 10, 10),
		node("k6", "INSTANCE", "Inst", 0, 0, 10, 10),
		node("k7", "COMPONENT", "Comp", 0, 0, 10, 10),
	)
	fc.responses[fk+"|sec1"] = map[string]any{"nodes": map[string]any{"sec1": mixed}}
	fx := setupExtractor(t, fc)
	written, err := fx.ext.ExtractForFile(context.Background(), fx.tenantID, fk,
		[]string{"sec1"}, map[string]string{"sec1": "p1"})
	if err != nil {
		t.Fatalf("ExtractForFile: %v", err)
	}
	if written != 3 {
		t.Fatalf("written: got %d want 3 (FRAME+INSTANCE+COMPONENT only)", written)
	}
	if got := countMetadataRows(t, fx.d, fx.tenantID, fk); got != 3 {
		t.Errorf("db rows: got %d want 3", got)
	}
}

// TestExtractor_Idempotent — second run on same file = same row count, no dupes.
func TestExtractor_Idempotent(t *testing.T) {
	const fk = "fkI"
	fc := &fakeNodesFetcher{responses: map[string]map[string]any{}}
	fc.responses[fk+"|sec1"] = map[string]any{"nodes": map[string]any{
		"sec1": sectionResponse(
			node("n1", "FRAME", "F1", 0, 0, 10, 10),
			node("n2", "FRAME", "F2", 0, 20, 10, 10),
		),
	}}
	fx := setupExtractor(t, fc)
	ctx := context.Background()
	pages := map[string]string{"sec1": "p1"}
	if _, err := fx.ext.ExtractForFile(ctx, fx.tenantID, fk, []string{"sec1"}, pages); err != nil {
		t.Fatalf("run1: %v", err)
	}
	if got := countMetadataRows(t, fx.d, fx.tenantID, fk); got != 2 {
		t.Fatalf("after run1: got %d want 2", got)
	}
	// Re-run — should UPSERT, not insert duplicates.
	if _, err := fx.ext.ExtractForFile(ctx, fx.tenantID, fk, []string{"sec1"}, pages); err != nil {
		t.Fatalf("run2: %v", err)
	}
	if got := countMetadataRows(t, fx.d, fx.tenantID, fk); got != 2 {
		t.Errorf("after run2: got %d want 2 (idempotent)", got)
	}
}

// TestExtractor_EmptySection — section with no direct-child frames =
// zero rows, no error.
func TestExtractor_EmptySection(t *testing.T) {
	const fk = "fkE"
	fc := &fakeNodesFetcher{responses: map[string]map[string]any{}}
	fc.responses[fk+"|sec1"] = map[string]any{"nodes": map[string]any{
		"sec1": sectionResponse(),
	}}
	fx := setupExtractor(t, fc)
	written, err := fx.ext.ExtractForFile(context.Background(), fx.tenantID, fk,
		[]string{"sec1"}, map[string]string{"sec1": "p1"})
	if err != nil {
		t.Fatalf("ExtractForFile: %v", err)
	}
	if written != 0 {
		t.Fatalf("written: got %d want 0", written)
	}
	if got := countMetadataRows(t, fx.d, fx.tenantID, fk); got != 0 {
		t.Errorf("db rows: got %d want 0", got)
	}
}

// TestExtractor_BatchBoundary — 120 section ids → 3 API calls (50 each
// for the first two, 20 for the last) given nodeMetadataBatchSize=50.
func TestExtractor_BatchBoundary(t *testing.T) {
	const fk = "fkB"
	const total = 120
	sectionIDs := make([]string, 0, total)
	pageMap := map[string]string{}
	fc := &fakeNodesFetcher{responses: map[string]map[string]any{}}
	for i := 0; i < total; i++ {
		sid := fmt.Sprintf("sec-%d", i)
		sectionIDs = append(sectionIDs, sid)
		pageMap[sid] = "p1"
	}
	// Pre-register the 3 expected batches.
	for start := 0; start < total; start += nodeMetadataBatchSize {
		end := start + nodeMetadataBatchSize
		if end > total {
			end = total
		}
		key := fk
		batchNodes := map[string]any{}
		for i := start; i < end; i++ {
			sid := sectionIDs[i]
			key += "|" + sid
			batchNodes[sid] = sectionResponse(
				node("child-"+sid, "FRAME", "F", 0, 0, 10, 10),
			)
		}
		fc.responses[key] = map[string]any{"nodes": batchNodes}
	}
	fx := setupExtractor(t, fc)
	written, err := fx.ext.ExtractForFile(context.Background(), fx.tenantID, fk, sectionIDs, pageMap)
	if err != nil {
		t.Fatalf("ExtractForFile: %v", err)
	}
	if written != total {
		t.Fatalf("written: got %d want %d", written, total)
	}
	if got := atomic.LoadInt32(&fc.calls); got != 3 {
		t.Errorf("api calls: got %d want 3 (120/50 batched)", got)
	}
	if got := countMetadataRows(t, fx.d, fx.tenantID, fk); got != total {
		t.Errorf("db rows: got %d want %d", got, total)
	}
}

// TestExtractor_FigmaErrorReturned — a Figma error on the only batch
// surfaces, no rows persisted.
func TestExtractor_FigmaErrorReturned(t *testing.T) {
	const fk = "fkErr"
	fc := &fakeNodesFetcher{wantErr: errors.New("HTTP 429")}
	fx := setupExtractor(t, fc)
	written, err := fx.ext.ExtractForFile(context.Background(), fx.tenantID, fk,
		[]string{"sec1"}, map[string]string{"sec1": "p1"})
	if err == nil {
		t.Fatalf("expected error from 429, got nil")
	}
	if written != 0 {
		t.Fatalf("written: got %d want 0 (Figma failed)", written)
	}
	if got := countMetadataRows(t, fx.d, fx.tenantID, fk); got != 0 {
		t.Errorf("db rows: got %d want 0", got)
	}
}

// TestExtractor_EmptyInputs — no sections = no-op (no API call).
func TestExtractor_EmptyInputs(t *testing.T) {
	fc := &fakeNodesFetcher{}
	fx := setupExtractor(t, fc)
	written, err := fx.ext.ExtractForFile(context.Background(), fx.tenantID, "fk", nil, nil)
	if err != nil {
		t.Fatalf("ExtractForFile: %v", err)
	}
	if written != 0 {
		t.Fatalf("written: got %d want 0", written)
	}
	if got := atomic.LoadInt32(&fc.calls); got != 0 {
		t.Errorf("api calls: got %d want 0", got)
	}
}

// TestExtractor_NoPAT — tenant without a PAT skips silently, no error.
func TestExtractor_NoPAT(t *testing.T) {
	fc := &fakeNodesFetcher{}
	d, tenantID, _ := newPlannerTestDB(t)
	repo := projects.NewTenantRepo(d.DB, tenantID)
	ext := &NodeMetadataExtractor{
		ResolvePAT: func(_ context.Context, _ string) (string, error) { return "", nil },
		NewClient:  func(_ string) FigmaNodesFetcher { return fc },
		Repo:       func(_ string) *projects.TenantRepo { return repo },
	}
	written, err := ext.ExtractForFile(context.Background(), tenantID, "fk",
		[]string{"sec1"}, nil)
	if err != nil {
		t.Fatalf("ExtractForFile: %v", err)
	}
	if written != 0 {
		t.Fatalf("written: got %d want 0", written)
	}
	if got := atomic.LoadInt32(&fc.calls); got != 0 {
		t.Errorf("api calls: got %d want 0 (no PAT)", got)
	}
}

// ─── buildNodeMetadataRows pure-function tests ────────────────────────────────

func TestBuildNodeMetadataRows_PreservesOrderAndCoords(t *testing.T) {
	resp := map[string]any{"nodes": map[string]any{
		"sec1": sectionResponse(
			node("a", "FRAME", "Alpha", 100, 200, 50, 60),
			node("b", "FRAME", "Beta", 300, 400, 70, 80),
		),
	}}
	rows := buildNodeMetadataRows(resp, []string{"sec1"}, "fk", map[string]string{"sec1": "p1"})
	if len(rows) != 2 {
		t.Fatalf("rows: got %d want 2", len(rows))
	}
	if rows[0].NodeID != "a" || rows[0].OrderIndex != 0 || rows[0].AbsX != 100 || rows[0].AbsY != 200 {
		t.Errorf("row[0] mismatch: %+v", rows[0])
	}
	if rows[1].NodeID != "b" || rows[1].OrderIndex != 1 || rows[1].Width != 70 || rows[1].Height != 80 {
		t.Errorf("row[1] mismatch: %+v", rows[1])
	}
	if rows[0].PageID != "p1" || rows[0].SectionID != "sec1" || rows[0].ParentID != "sec1" || rows[0].Depth != 1 {
		t.Errorf("row[0] page/section/parent/depth: %+v", rows[0])
	}
}

func TestBuildNodeMetadataRows_DropsMissingSection(t *testing.T) {
	resp := map[string]any{"nodes": map[string]any{
		"sec1": sectionResponse(node("a", "FRAME", "A", 0, 0, 10, 10)),
	}}
	rows := buildNodeMetadataRows(resp, []string{"sec1", "missing"}, "fk",
		map[string]string{"sec1": "p1"})
	if len(rows) != 1 {
		t.Errorf("rows: got %d want 1 (missing section silently dropped)", len(rows))
	}
}

func TestBuildNodeMetadataRows_NilResponse(t *testing.T) {
	if got := buildNodeMetadataRows(nil, []string{"sec1"}, "fk", nil); got != nil {
		t.Errorf("nil response should yield nil rows, got %d", len(got))
	}
}

func TestBuildNodeMetadataRows_InstanceComponentID(t *testing.T) {
	child := node("inst1", "INSTANCE", "I", 0, 0, 10, 10)
	child["componentId"] = "comp-master-99"
	resp := map[string]any{"nodes": map[string]any{
		"sec1": sectionResponse(child),
	}}
	rows := buildNodeMetadataRows(resp, []string{"sec1"}, "fk", map[string]string{"sec1": "p"})
	if len(rows) != 1 || rows[0].ComponentID != "comp-master-99" {
		t.Errorf("INSTANCE componentId not captured: %+v", rows)
	}
}

func TestBuildNodeMetadataRows_LayoutMode(t *testing.T) {
	child := node("a", "FRAME", "A", 0, 0, 10, 10)
	child["layoutMode"] = "VERTICAL"
	resp := map[string]any{"nodes": map[string]any{
		"sec1": sectionResponse(child),
	}}
	rows := buildNodeMetadataRows(resp, []string{"sec1"}, "fk", nil)
	if len(rows) != 1 || rows[0].LayoutMode != "VERTICAL" {
		t.Errorf("layout_mode not captured: %+v", rows)
	}
}
