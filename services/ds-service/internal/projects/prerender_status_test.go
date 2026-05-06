package projects

// prerender_status_test.go — coverage for U8 of plan 2026-05-06-003.
// Pin the ring buffer's wrap behavior, snapshot ordering, and concurrent
// Append safety.

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func makeRun(versionID string, t time.Time) PrerenderRun {
	return PrerenderRun{
		VersionID:     versionID,
		FileID:        "f1",
		TenantID:      "t1",
		StartedAt:     t,
		FinishedAt:    t.Add(5 * time.Second),
		DurationMs:    5000,
		TotalClusters: 100,
		Rendered:      90,
		Failed:        10,
		Outcome:       "partial",
	}
}

func TestPrerenderStatusBuffer_NewWithDefault(t *testing.T) {
	b := NewPrerenderStatusBuffer(0)
	if b == nil {
		t.Fatal("NewPrerenderStatusBuffer(0) returned nil")
	}
	if got := b.Snapshot(""); len(got) != 0 {
		t.Fatalf("empty buffer should snapshot to empty, got len=%d", len(got))
	}
}

func TestPrerenderStatusBuffer_AppendBelowCapacity(t *testing.T) {
	b := NewPrerenderStatusBuffer(5)
	now := time.Now()
	for i := 0; i < 3; i++ {
		b.Append(makeRun("v"+string(rune('A'+i)), now.Add(time.Duration(i)*time.Second)))
	}
	got := b.Snapshot("")
	if len(got) != 3 {
		t.Fatalf("want 3 runs, got %d", len(got))
	}
	// Oldest first.
	if got[0].VersionID != "vA" || got[1].VersionID != "vB" || got[2].VersionID != "vC" {
		t.Fatalf("want chronological order vA,vB,vC, got %s,%s,%s",
			got[0].VersionID, got[1].VersionID, got[2].VersionID)
	}
}

func TestPrerenderStatusBuffer_WrapsAtCapacity(t *testing.T) {
	b := NewPrerenderStatusBuffer(3)
	now := time.Now()
	// Append 5 runs into a buffer with capacity 3 — first 2 should be
	// overwritten; snapshot should return last 3 in chronological order.
	for i := 0; i < 5; i++ {
		b.Append(makeRun("v"+string(rune('A'+i)), now.Add(time.Duration(i)*time.Second)))
	}
	got := b.Snapshot("")
	if len(got) != 3 {
		t.Fatalf("want 3 runs after wrap, got %d", len(got))
	}
	if got[0].VersionID != "vC" || got[1].VersionID != "vD" || got[2].VersionID != "vE" {
		t.Fatalf("want chronological vC,vD,vE after wrap, got %s,%s,%s",
			got[0].VersionID, got[1].VersionID, got[2].VersionID)
	}
}

func TestPrerenderStatusBuffer_FilterByVersionID(t *testing.T) {
	b := NewPrerenderStatusBuffer(10)
	now := time.Now()
	b.Append(makeRun("v1", now))
	b.Append(makeRun("v2", now.Add(1*time.Second)))
	b.Append(makeRun("v1", now.Add(2*time.Second))) // re-run of v1
	b.Append(makeRun("v3", now.Add(3*time.Second)))

	got := b.Snapshot("v1")
	if len(got) != 2 {
		t.Fatalf("want 2 runs filtered to v1, got %d", len(got))
	}
	for _, r := range got {
		if r.VersionID != "v1" {
			t.Fatalf("filter leaked: got versionID=%s in v1 filter", r.VersionID)
		}
	}
}

func TestPrerenderStatusBuffer_NilSafety(t *testing.T) {
	var b *PrerenderStatusBuffer // intentionally nil
	b.Append(makeRun("v1", time.Now()))
	if got := b.Snapshot(""); got != nil {
		t.Fatalf("nil buffer Snapshot should return nil, got %v", got)
	}
}

func TestPrerenderStatusBuffer_ConcurrentAppendSafe(t *testing.T) {
	b := NewPrerenderStatusBuffer(100)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			b.Append(makeRun("v", time.Unix(int64(i), 0)))
		}()
	}
	wg.Wait()
	got := b.Snapshot("")
	if len(got) != 50 {
		t.Fatalf("want 50 runs from concurrent appends, got %d", len(got))
	}
}

func TestClassifyOutcome(t *testing.T) {
	cases := []struct {
		name                            string
		total, rendered, failed         int
		setupErr                        error
		want                            string
	}{
		{"ok", 10, 10, 0, nil, "ok"},
		{"partial", 10, 7, 3, nil, "partial"},
		{"all_failed", 10, 0, 10, nil, "all_failed"},
		{"no_clusters", 0, 0, 0, nil, "no_clusters"},
		{"setup_error trumps everything", 10, 5, 5, errors.New("nil deps"), "setup_error"},
	}
	for _, tc := range cases {
		got := ClassifyOutcome(tc.total, tc.rendered, tc.failed, tc.setupErr)
		if got != tc.want {
			t.Errorf("%s: ClassifyOutcome(%d,%d,%d,err=%v) = %q, want %q",
				tc.name, tc.total, tc.rendered, tc.failed, tc.setupErr, got, tc.want)
		}
	}
}

func TestHandlePrerenderStatus_NilBufferReturns503(t *testing.T) {
	h := HandlePrerenderStatus(nil)
	r := httptest.NewRequest(http.MethodGet, "/v1/admin/prerender/status", nil)
	w := httptest.NewRecorder()
	h(w, r)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil buffer should 503, got %d", w.Code)
	}
}

func TestHandlePrerenderStatus_HappyPath(t *testing.T) {
	b := NewPrerenderStatusBuffer(10)
	b.Append(makeRun("v1", time.Unix(1700000000, 0)))
	b.Append(makeRun("v2", time.Unix(1700000060, 0)))

	h := HandlePrerenderStatus(b)
	r := httptest.NewRequest(http.MethodGet, "/v1/admin/prerender/status", nil)
	w := httptest.NewRecorder()
	h(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	var resp struct {
		Runs     []PrerenderRun `json:"runs"`
		Count    int            `json:"count"`
		Capacity int            `json:"capacity"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if resp.Count != 2 {
		t.Fatalf("want count=2, got %d", resp.Count)
	}
	if resp.Capacity != 10 {
		t.Fatalf("want capacity=10, got %d", resp.Capacity)
	}
	if resp.Runs[0].VersionID != "v1" || resp.Runs[1].VersionID != "v2" {
		t.Fatalf("want v1,v2 in order, got %s,%s",
			resp.Runs[0].VersionID, resp.Runs[1].VersionID)
	}
}

func TestHandlePrerenderStatus_FilterByQueryParam(t *testing.T) {
	b := NewPrerenderStatusBuffer(10)
	b.Append(makeRun("v1", time.Unix(1700000000, 0)))
	b.Append(makeRun("v2", time.Unix(1700000060, 0)))
	b.Append(makeRun("v1", time.Unix(1700000120, 0)))

	h := HandlePrerenderStatus(b)
	r := httptest.NewRequest(http.MethodGet, "/v1/admin/prerender/status?version_id=v1", nil)
	w := httptest.NewRecorder()
	h(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var resp struct {
		Runs  []PrerenderRun `json:"runs"`
		Count int            `json:"count"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Count != 2 {
		t.Fatalf("filter to v1: want count=2, got %d", resp.Count)
	}
	for _, run := range resp.Runs {
		if run.VersionID != "v1" {
			t.Fatalf("filter leaked: got versionID=%s", run.VersionID)
		}
	}
}
