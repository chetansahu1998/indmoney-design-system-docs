package projects

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/auth"
)

// U12 — CSV bulk export / import for translators.
//
// Each test seeds a canonical_tree on the fixture's screen so the export path
// has TEXT nodes to walk. The tree uses the same JSON shape ResolveCanonical-
// Tree handles (no Figma envelope, since the seed plays the part of the
// dereferenced node we'd otherwise pull from /v1/files/.../nodes).

const csvSeedTree = `{
  "id": "FRAME_1",
  "type": "FRAME",
  "children": [
    {"id": "node-a", "type": "TEXT", "characters": "Hello"},
    {"id": "node-b", "type": "TEXT", "characters": "World"},
    {"id": "node-c", "type": "RECTANGLE"},
    {"id": "node-d", "type": "TEXT", "characters": "Welcome"}
  ]
}`

func seedCanonicalTree(t *testing.T, fx *overrideFixture, screenID, tree string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := fx.dbHandle.ExecContext(context.Background(),
		`INSERT INTO screen_canonical_trees (screen_id, canonical_tree, hash, updated_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(screen_id) DO UPDATE SET canonical_tree=excluded.canonical_tree, updated_at=excluded.updated_at`,
		screenID, tree, "hash-x", now,
	)
	if err != nil {
		t.Fatalf("seed canonical tree: %v", err)
	}
}

func csvExport(t *testing.T, srv *Server, slug, leafID, tenantID, userID string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/v1/projects/%s/leaves/%s/text-overrides/csv", slug, leafID), nil)
	r.SetPathValue("slug", slug)
	r.SetPathValue("leaf_id", leafID)
	r = r.WithContext(WithClaims(context.Background(), &auth.Claims{Sub: userID, Tenants: []string{tenantID}}))
	w := httptest.NewRecorder()
	srv.HandleCSVExport(w, r)
	return w
}

func csvImport(t *testing.T, srv *Server, slug, leafID, tenantID, userID, body string, force bool) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", "overrides.csv")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := fw.Write([]byte(body)); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if force {
		_ = mw.WriteField("force", "true")
	}
	_ = mw.Close()

	r := httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/v1/projects/%s/leaves/%s/text-overrides/csv", slug, leafID), &buf)
	r.SetPathValue("slug", slug)
	r.SetPathValue("leaf_id", leafID)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	r = r.WithContext(WithClaims(context.Background(), &auth.Claims{Sub: userID, Tenants: []string{tenantID}}))
	w := httptest.NewRecorder()
	srv.HandleCSVImport(w, r)
	return w
}

// ───────────────────────── Export schema ───────────────────────────────────

func TestHandleCSVExport_Schema(t *testing.T) {
	fx := newOverrideFixture(t)
	seedCanonicalTree(t, fx, fx.screenA, csvSeedTree)

	w := csvExport(t, fx.server, fx.slugA, fx.flowA, fx.tenantA, fx.userA)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/csv") {
		t.Fatalf("expected text/csv; got %q", got)
	}
	rdr := csv.NewReader(bytes.NewReader(w.Body.Bytes()))
	header, err := rdr.Read()
	if err != nil {
		t.Fatalf("read header: %v", err)
	}
	want := []string{"screen", "screen_id", "node_path", "figma_node_id", "original", "current", "last_edited_by", "last_edited_at"}
	if len(header) != len(want) {
		t.Fatalf("header columns: got %v want %v", header, want)
	}
	for i, c := range want {
		if header[i] != c {
			t.Fatalf("header[%d]: got %q want %q", i, header[i], c)
		}
	}
	rows, err := rdr.ReadAll()
	if err != nil {
		t.Fatalf("read rows: %v", err)
	}
	// Three TEXT nodes: node-a, node-b, node-d (RECTANGLE is excluded).
	if len(rows) != 3 {
		t.Fatalf("expected 3 TEXT rows; got %d (rows=%v)", len(rows), rows)
	}
	got := map[string]string{}
	for _, r := range rows {
		got[r[3]] = r[4] // figma_node_id -> original
	}
	if got["node-a"] != "Hello" || got["node-b"] != "World" || got["node-d"] != "Welcome" {
		t.Fatalf("unexpected text mapping: %v", got)
	}
	// `current` defaults to `original` when no override exists.
	for _, r := range rows {
		if r[5] != r[4] {
			t.Fatalf("row %s: expected current=original; got current=%q original=%q", r[3], r[5], r[4])
		}
	}
}

// ───────────────────────── Import: 14 rows happy path ──────────────────────

func TestHandleCSVImport_14Rows_Applied(t *testing.T) {
	fx := newOverrideFixture(t)
	// 14 TEXT children for the import flow.
	tree := `{"id":"F","type":"FRAME","children":[`
	for i := 0; i < 14; i++ {
		if i > 0 {
			tree += ","
		}
		tree += fmt.Sprintf(`{"id":"n-%d","type":"TEXT","characters":"orig %d"}`, i, i)
	}
	tree += `]}`
	seedCanonicalTree(t, fx, fx.screenA, tree)

	var buf bytes.Buffer
	cw := csv.NewWriter(&buf)
	_ = cw.Write([]string{"screen", "screen_id", "node_path", "figma_node_id", "original", "current", "last_edited_by", "last_edited_at"})
	for i := 0; i < 14; i++ {
		_ = cw.Write([]string{
			"FrameLabel",
			fx.screenA,
			fmt.Sprintf("%d", i),
			fmt.Sprintf("n-%d", i),
			fmt.Sprintf("orig %d", i),
			fmt.Sprintf("translated %d", i),
			"u-1",
			"",
		})
	}
	cw.Flush()

	w := csvImport(t, fx.server, fx.slugA, fx.flowA, fx.tenantA, fx.userA, buf.String(), false)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200; got %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Applied   int                 `json:"applied"`
		Skipped   int                 `json:"skipped"`
		BulkID    string              `json:"bulk_id"`
		Conflicts []csvImportConflict `json:"conflicts"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, w.Body.String())
	}
	if resp.Applied != 14 {
		t.Fatalf("expected 14 applied; got %d body=%s", resp.Applied, w.Body.String())
	}
	if resp.BulkID == "" {
		t.Fatalf("expected bulk_id in response")
	}

	// 14 audit rows all sharing the bulk_id.
	rows, err := fx.dbHandle.QueryContext(context.Background(),
		`SELECT details FROM audit_log WHERE event_type = 'override.text.bulk_set' AND tenant_id = ?`,
		fx.tenantA)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var detail string
		_ = rows.Scan(&detail)
		if !strings.Contains(detail, resp.BulkID) {
			t.Fatalf("audit row missing bulk_id: %s", detail)
		}
		count++
	}
	if count != 14 {
		t.Fatalf("expected 14 audit rows; got %d", count)
	}

	// 14 override rows persisted with the new values.
	var n int
	_ = fx.dbHandle.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM screen_text_overrides WHERE screen_id = ?`, fx.screenA,
	).Scan(&n)
	if n != 14 {
		t.Fatalf("expected 14 override rows; got %d", n)
	}
}

// ───────────────────────── Import: conflict surfaces in response ──────────

func TestHandleCSVImport_OneConflict_NotApplied(t *testing.T) {
	fx := newOverrideFixture(t)
	tree := `{"id":"F","type":"FRAME","children":[
		{"id":"n-0","type":"TEXT","characters":"orig"}
	]}`
	seedCanonicalTree(t, fx, fx.screenA, tree)

	// Seed a recent override to make the DB row newer than the CSV's
	// last_edited_at.
	if w := putOverride(t, fx.server, fx.slugA, fx.screenA, "n-0", fx.tenantA, fx.userA,
		putOverrideRequest{Value: "db-current", ExpectedRevision: 0}); w.Code != http.StatusOK {
		t.Fatalf("seed override: %d body=%s", w.Code, w.Body.String())
	}

	// CSV row claims a last_edited_at well in the past — should conflict.
	stale := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	body := strings.Join([]string{
		"screen,screen_id,node_path,figma_node_id,original,current,last_edited_by,last_edited_at",
		fmt.Sprintf("Frame,%s,0,n-0,orig,csv-translated,u-1,%s", fx.screenA, stale),
	}, "\n")

	w := csvImport(t, fx.server, fx.slugA, fx.flowA, fx.tenantA, fx.userA, body, false)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200; got %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Applied   int                 `json:"applied"`
		Conflicts []csvImportConflict `json:"conflicts"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Applied != 0 {
		t.Fatalf("expected applied=0 with conflicts; got %d", resp.Applied)
	}
	if len(resp.Conflicts) != 1 {
		t.Fatalf("expected 1 conflict; got %d", len(resp.Conflicts))
	}
	c := resp.Conflicts[0]
	if c.FigmaNodeID != "n-0" || c.CSVValue != "csv-translated" || c.CurrentValue != "db-current" {
		t.Fatalf("unexpected conflict: %+v", c)
	}

	// DB still has the original value — no write happened.
	var v string
	_ = fx.dbHandle.QueryRowContext(context.Background(),
		`SELECT value FROM screen_text_overrides WHERE screen_id = ? AND figma_node_id = ?`,
		fx.screenA, "n-0").Scan(&v)
	if v != "db-current" {
		t.Fatalf("expected db value untouched; got %q", v)
	}

	// Force=true on a re-import applies the row.
	w = csvImport(t, fx.server, fx.slugA, fx.flowA, fx.tenantA, fx.userA, body, true)
	if w.Code != http.StatusOK {
		t.Fatalf("force expected 200; got %d body=%s", w.Code, w.Body.String())
	}
	var forceResp struct {
		Applied int `json:"applied"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &forceResp)
	if forceResp.Applied != 1 {
		t.Fatalf("expected applied=1 with force; got %d", forceResp.Applied)
	}
	_ = fx.dbHandle.QueryRowContext(context.Background(),
		`SELECT value FROM screen_text_overrides WHERE screen_id = ? AND figma_node_id = ?`,
		fx.screenA, "n-0").Scan(&v)
	if v != "csv-translated" {
		t.Fatalf("expected force-applied value; got %q", v)
	}
}

// ───────────────────────── Import: malformed CSV → 400 ─────────────────────

func TestHandleCSVImport_Malformed_400(t *testing.T) {
	fx := newOverrideFixture(t)

	// Missing required column "current".
	body := strings.Join([]string{
		"screen,screen_id,node_path,figma_node_id,original,last_edited_by,last_edited_at",
		fmt.Sprintf("Frame,%s,0,n-0,orig,u-1,", fx.screenA),
	}, "\n")
	w := csvImport(t, fx.server, fx.slugA, fx.flowA, fx.tenantA, fx.userA, body, false)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400; got %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Error  string           `json:"error"`
		Errors []csvImportError `json:"errors"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, w.Body.String())
	}
	if resp.Error != "invalid_csv" || len(resp.Errors) == 0 {
		t.Fatalf("expected invalid_csv with line-level errors; got %+v", resp)
	}
	if !strings.Contains(resp.Errors[0].Reason, "current") {
		t.Fatalf("expected error to mention missing column; got %q", resp.Errors[0].Reason)
	}
}

// ───────────────────────── Import: 1000 rows in 100-chunks ─────────────────

func TestHandleCSVImport_1000Rows_ChunkedBulkCalls(t *testing.T) {
	fx := newOverrideFixture(t)

	const N = 1000
	var sb strings.Builder
	sb.WriteString(`{"id":"F","type":"FRAME","children":[`)
	for i := 0; i < N; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		fmt.Fprintf(&sb, `{"id":"big-%d","type":"TEXT","characters":"orig %d"}`, i, i)
	}
	sb.WriteString("]}")
	seedCanonicalTree(t, fx, fx.screenA, sb.String())

	var buf bytes.Buffer
	cw := csv.NewWriter(&buf)
	_ = cw.Write([]string{"screen", "screen_id", "node_path", "figma_node_id", "original", "current", "last_edited_by", "last_edited_at"})
	for i := 0; i < N; i++ {
		_ = cw.Write([]string{
			"Frame", fx.screenA, fmt.Sprintf("%d", i),
			fmt.Sprintf("big-%d", i),
			fmt.Sprintf("orig %d", i),
			fmt.Sprintf("v-%d", i),
			"u-1", "",
		})
	}
	cw.Flush()

	start := time.Now()
	w := csvImport(t, fx.server, fx.slugA, fx.flowA, fx.tenantA, fx.userA, buf.String(), false)
	elapsed := time.Since(start)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200; got %d body=%s", w.Code, w.Body.String())
	}
	if elapsed > 30*time.Second {
		t.Fatalf("import took %s; budget is 30s", elapsed)
	}
	var resp struct {
		Applied int    `json:"applied"`
		BulkID  string `json:"bulk_id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Applied != N {
		t.Fatalf("expected %d applied; got %d", N, resp.Applied)
	}

	// All 1000 rows persisted under the same bulk_id.
	var n int
	_ = fx.dbHandle.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM screen_text_overrides WHERE screen_id = ?`, fx.screenA,
	).Scan(&n)
	if n != N {
		t.Fatalf("expected %d override rows; got %d", N, n)
	}
	var auditCount int
	_ = fx.dbHandle.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM audit_log WHERE event_type = 'override.text.bulk_set' AND tenant_id = ?`,
		fx.tenantA,
	).Scan(&auditCount)
	if auditCount != N {
		t.Fatalf("expected %d audit rows; got %d", N, auditCount)
	}
}
