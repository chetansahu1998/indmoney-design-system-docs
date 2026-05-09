package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

// frame is a tiny constructor sugar so the walker fixtures stay readable.
func frame(id, kind, name string, w, h float64) figmaNode {
	return figmaNode{
		ID:                  id,
		Name:                name,
		Type:                kind,
		AbsoluteBoundingBox: &figmaBoundingBox{X: 0, Y: 0, Width: w, Height: h},
	}
}

func rect(id, name string, w, h float64, fills ...figmaFill) figmaNode {
	return figmaNode{
		ID:                  id,
		Name:                name,
		Type:                "RECTANGLE",
		AbsoluteBoundingBox: &figmaBoundingBox{Width: w, Height: h},
		Fills:               fills,
	}
}

func TestWalkScreens_FrameAcceptedAtScreenSize(t *testing.T) {
	root := figmaNode{Type: "SECTION", Children: []figmaNode{
		frame("a", "FRAME", "Home", 375, 812),
	}}
	got := walkScreens(root)
	if len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("expected one frame 'a', got %#v", got)
	}
}

func TestWalkScreens_HeightFloorAt80NotAt400(t *testing.T) {
	// Pre-fix: 375x146 popup frames were silently dropped (height < 400).
	// New floor is 80, so anything ≥ 80 tall passes.
	root := figmaNode{Type: "SECTION", Children: []figmaNode{
		frame("popup", "FRAME", "Inv. price card", 375, 146),
		frame("tooltip", "FRAME", "Tooltip", 375, 192),
		frame("too-short", "FRAME", "label-strip", 375, 60),
	}}
	got := walkScreens(root)
	ids := map[string]bool{}
	for _, s := range got {
		ids[s.ID] = true
	}
	if !ids["popup"] || !ids["tooltip"] {
		t.Fatalf("expected popup + tooltip to be accepted, got %#v", got)
	}
	if ids["too-short"] {
		t.Fatalf("expected too-short (h=60) to be rejected, got %#v", got)
	}
}

func TestWalkScreens_WidthFloorStillExcludesIconDebris(t *testing.T) {
	root := figmaNode{Type: "SECTION", Children: []figmaNode{
		frame("icon", "FRAME", "icon/24x24", 24, 24),
		frame("narrow", "FRAME", "200x600", 200, 600),
	}}
	got := walkScreens(root)
	if len(got) != 0 {
		t.Fatalf("expected no screens (both under 280px wide), got %#v", got)
	}
}

func TestWalkScreens_RectangleAcceptedOnlyWithImageFill(t *testing.T) {
	root := figmaNode{Type: "SECTION", Children: []figmaNode{
		rect("paste", "Screenshot_20250805", 375, 834, figmaFill{Type: "IMAGE"}),
		rect("solid", "background-shape", 375, 800, figmaFill{Type: "SOLID"}),
		rect("nofill", "no-fill-rect", 375, 800),
	}}
	got := walkScreens(root)
	if len(got) != 1 || got[0].ID != "paste" {
		t.Fatalf("expected only image-filled rectangle 'paste', got %#v", got)
	}
}

func TestWalkScreens_RecursesIntoSectionAndGroupOnly(t *testing.T) {
	// SECTION/GROUP recurse; FRAME does not — its children are sub-elements.
	root := figmaNode{Type: "SECTION", Children: []figmaNode{
		{
			Type: "GROUP",
			Children: []figmaNode{
				frame("inside-group", "FRAME", "Nested", 375, 812),
			},
		},
		// Frame containing its own sub-frame — only the OUTER counts.
		{
			Type:                "FRAME",
			ID:                  "outer",
			Name:                "Outer",
			AbsoluteBoundingBox: &figmaBoundingBox{Width: 375, Height: 812},
			Children: []figmaNode{
				frame("inner", "FRAME", "Inner button-ish wrapper", 375, 600),
			},
		},
	}}
	got := walkScreens(root)
	gotIDs := map[string]bool{}
	for _, s := range got {
		gotIDs[s.ID] = true
	}
	if !gotIDs["inside-group"] || !gotIDs["outer"] {
		t.Fatalf("expected inside-group and outer, got %#v", got)
	}
	if gotIDs["inner"] {
		t.Fatalf("did not expect frame children to be collected, got %#v", got)
	}
}

func TestWalkScreens_NoBboxIsSkipped(t *testing.T) {
	root := figmaNode{Type: "SECTION", Children: []figmaNode{
		{Type: "FRAME", ID: "no-bbox", Name: "missing"},
	}}
	if got := walkScreens(root); len(got) != 0 {
		t.Fatalf("expected no screens for nil bbox, got %#v", got)
	}
}

// successBody is a minimal /v1/files/<id>/nodes response that decodes
// cleanly and yields one screen via walkScreens. Only the fields the
// walker reads are populated.
const successBody = `{
  "nodes": {
    "1:1": {
      "document": {
        "id": "1:1",
        "type": "SECTION",
        "children": [
          {"id":"1:2","type":"FRAME","name":"Home","absoluteBoundingBox":{"x":0,"y":0,"width":375,"height":812}}
        ]
      }
    }
  }
}`

func newTestClient(t *testing.T, ts *httptest.Server) *FigmaClient {
	t.Helper()
	c := NewFigmaClient("test-pat-" + t.Name())
	c.baseURL = ts.URL
	c.sleep = func(time.Duration) {} // never block in tests
	return c
}

// TestResolveSection_RetriesOn429HonoringRetryAfter — when Figma returns
// 429 with Retry-After: 1, fetchWithRetry waits (sleep is stubbed so the
// test stays fast) and retries up to 3 times. After two 429s and a 200,
// the resolve succeeds.
func TestResolveSection_RetriesOn429HonoringRetryAfter(t *testing.T) {
	var attempts atomic.Int32
	var observedSleeps []time.Duration

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n <= 2 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"status":429,"err":"Rate limit exceeded"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(successBody))
	}))
	defer ts.Close()

	c := newTestClient(t, ts)
	c.sleep = func(d time.Duration) { observedSleeps = append(observedSleeps, d) }

	screens, err := c.ResolveSection(context.Background(), "f1", "1:1")
	if err != nil {
		t.Fatalf("expected success after 2 retries; got %v", err)
	}
	if len(screens) != 1 || screens[0].Name != "Home" {
		t.Fatalf("expected 1 screen named Home, got %#v", screens)
	}
	if got := attempts.Load(); got != 3 {
		t.Errorf("expected 3 server hits, got %d", got)
	}
	// First attempt is direct; attempts 2 and 3 are preceded by sleeps.
	if len(observedSleeps) != 2 {
		t.Fatalf("expected 2 backoff sleeps, got %d (%v)", len(observedSleeps), observedSleeps)
	}
	// Retry-After: 1s is below the 500ms floor → clamped UP to 1s. Confirm
	// both sleeps reflect the Retry-After value, not the exponential default.
	for i, d := range observedSleeps {
		if d != time.Second {
			t.Errorf("sleep[%d]: expected 1s (from Retry-After), got %v", i, d)
		}
	}
}

// TestResolveSection_RetriesOnTransientTransport — connection-reset /
// truncated body (server closes mid-response producing a transport-level
// error or "unexpected end of JSON input") gets retried with exponential
// backoff. After one transient failure and a clean 200, the call succeeds.
func TestResolveSection_RetriesOnTransientTransport(t *testing.T) {
	var attempts atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n == 1 {
			// Truncated JSON body with 200 status → decoder hits
			// "unexpected end of JSON input" — same wire shape Figma exhibits
			// under load (63 of 107 errors in the May 9 cycle).
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"nodes":{"1:1":{"docu`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(successBody))
	}))
	defer ts.Close()

	c := newTestClient(t, ts)
	screens, err := c.ResolveSection(context.Background(), "f1", "1:1")
	if err != nil {
		t.Fatalf("expected success after one transient failure; got %v", err)
	}
	if len(screens) != 1 {
		t.Fatalf("expected 1 screen, got %d", len(screens))
	}
	if got := attempts.Load(); got != 2 {
		t.Errorf("expected 2 server hits (1 transient + 1 success), got %d", got)
	}
}

// TestResolveSection_FailsAfterMaxAttempts — sustained 429s exhaust the
// 3-attempt budget and the final error surfaces with the
// "figma: HTTP 429: ..." string downstream telemetry filters match on.
func TestResolveSection_FailsAfterMaxAttempts(t *testing.T) {
	var attempts atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"status":429,"err":"Rate limit exceeded"}`))
	}))
	defer ts.Close()

	c := newTestClient(t, ts)
	_, err := c.ResolveSection(context.Background(), "f1", "1:1")
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	if got := attempts.Load(); got != 3 {
		t.Errorf("expected exactly 3 attempts, got %d", got)
	}
	if msg := err.Error(); msg == "" || msg[:14] != "figma: HTTP 42" {
		t.Errorf("expected 429-shaped error preserved; got %q", msg)
	}
}

// TestResolveSection_NonRetryableSurfacesImmediately — decode errors on
// the malformed-but-syntactically-valid response shape (e.g. empty Nodes
// map, present but missing the requested ID) are NOT retried. They
// indicate a deterministic resolver mistake, not transient noise.
func TestResolveSection_NonRetryableSurfacesImmediately(t *testing.T) {
	var attempts atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusOK)
		// Valid JSON with empty nodes map → "node not in response" error.
		_, _ = w.Write([]byte(`{"nodes":{}}`))
	}))
	defer ts.Close()

	c := newTestClient(t, ts)
	_, err := c.ResolveSection(context.Background(), "f1", "1:1")
	if err == nil {
		t.Fatal("expected node-not-found error")
	}
	if got := attempts.Load(); got != 1 {
		t.Errorf("expected exactly 1 attempt for non-retryable error, got %d", got)
	}
}

// TestParseRetryAfter — header parsing edge cases. Figma sends seconds
// as a bare integer; we also tolerate trailing whitespace and reject
// negative / non-numeric values without panicking.
func TestParseRetryAfter(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"", 0},
		{"0", 0},
		{"1", time.Second},
		{"30", 30 * time.Second},
		{"  5 ", 5 * time.Second},
		{"-1", 0},
		{"abc", 0},
		{"1.5", 0}, // strict integer — fractional rejected
	}
	for _, tc := range cases {
		t.Run(strconv.Quote(tc.in), func(t *testing.T) {
			if got := parseRetryAfter(tc.in); got != tc.want {
				t.Errorf("parseRetryAfter(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestBackoffFor_HonorsRetryAfterWithClamps — small Retry-After values
// clamp UP to 500ms (avoid a hot loop); huge values clamp DOWN to 30s
// (avoid wedging the cycle on a single bad row).
func TestBackoffFor_HonorsRetryAfterWithClamps(t *testing.T) {
	rle := func(d time.Duration) *rateLimitError { return &rateLimitError{retryAfter: d} }
	cases := []struct {
		name    string
		err     error
		attempt int
		want    time.Duration
	}{
		{"retry-after honored within bounds", rle(2 * time.Second), 2, 2 * time.Second},
		{"retry-after clamped up to floor", rle(50 * time.Millisecond), 2, 500 * time.Millisecond},
		{"retry-after clamped down to ceiling", rle(120 * time.Second), 2, 30 * time.Second},
		{"missing retry-after falls through to exponential", rle(0), 2, time.Second},
		{"non-rate-limit err uses exponential — attempt 2", &renderTimeoutError{}, 2, time.Second},
		{"non-rate-limit err uses exponential — attempt 3", &renderTimeoutError{}, 3, 2 * time.Second},
		{"exponential capped at 8s", &renderTimeoutError{}, 10, 8 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := backoffFor(tc.attempt, tc.err); got != tc.want {
				t.Errorf("backoffFor(%d, %T) = %v, want %v", tc.attempt, tc.err, got, tc.want)
			}
		})
	}
}
