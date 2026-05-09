package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/figma/client"
)

// figma_resolve.go — Figma REST resolve + screen-frame extraction.
//
// Given (file_id, node_id), fetch the node from Figma, walk its tree,
// return top-level frames ≥ 280×400px (screen-sized). Mirrors the
// validated logic from /tmp/figma_test.py (2026-05-04).
//
// Caching: 1-hour LRU keyed on (file_id, node_id). The 2026-05-05 sheet
// inspection found one Figma file referenced 45 times — without the
// cache, a row-index shift in that tab would trigger 45 redundant
// node-fetches per cycle. Cache cuts to 1 fetch per (file_id, node_id)
// per hour.

// Screen is one top-level frame inside a section, ready to ship as a
// FramePayload to the export endpoint.
type Screen struct {
	ID    string  // Figma node ID (e.g. "30341" or "12940:595737")
	Name  string  // human-readable frame name
	Type  string  // FRAME / COMPONENT / INSTANCE
	X     float64 // absolute Figma coords
	Y     float64
	Width float64
	Height float64
}

// FigmaClient wraps the REST API + cache. One instance per cycle is fine.
//
// Rate limiting + retries: every fetchAndWalk acquires a tier-1 token via
// client.WaitTier1, sharing the per-PAT bucket with internal/figma/client.Client
// instances elsewhere in the process. fetchWithRetry adds backoff for 429s
// (honoring Retry-After) and for transient transport errors (connection-reset,
// EOF, decode-EOF). Without these, the sheets-sync resolve path saw 64 errors
// in a 78-min cycle (28 × HTTP 429 burst, 63 × decode-EOF, 14 × transport).
type FigmaClient struct {
	pat   string
	hc    *http.Client
	cache *figmaCache
	// baseURL allows tests to point fetchAndWalk at httptest.Server. Empty
	// string means production Figma. Tests SHOULD set this; production code
	// SHOULD NOT (we don't proxy Figma in any environment).
	baseURL string
	// sleep is injectable so tests don't actually wait Retry-After seconds.
	// nil → time.Sleep.
	sleep func(time.Duration)
}

func NewFigmaClient(pat string) *FigmaClient {
	return &FigmaClient{
		pat: pat,
		// 90s timeout: production /v1/files/<id>/nodes for INDmoney's larger
		// Figma files (Dashboard v5, INDstocks V4) routinely takes 30-60s.
		// The previous 30s ceiling was aborting otherwise-successful fetches
		// and surfacing them as "do request: context deadline exceeded".
		hc:    &http.Client{Timeout: 90 * time.Second},
		cache: newFigmaCache(200, time.Hour),
	}
}

// ResolveSection fetches the node + walks it to top-level screen frames.
// Empty result + nil error means "the node resolved but contained no
// frames meeting the size filter" (e.g. a CANVAS or empty SECTION).
func (f *FigmaClient) ResolveSection(ctx context.Context, fileID, nodeID string) ([]Screen, error) {
	if fileID == "" || nodeID == "" {
		return nil, fmt.Errorf("figma: empty fileID or nodeID")
	}
	if cached, ok := f.cache.get(fileID, nodeID); ok {
		return cached, nil
	}

	// First attempt — with retry-on-transient. Render-timeout surfaces
	// to the outer fallback (single retry at lower scale, preserved from
	// the original plan even though the URL today doesn't take a scale arg).
	screens, err := f.fetchWithRetry(ctx, fileID, nodeID, 2)
	if err != nil && isRenderTimeout(err) {
		screens, err = f.fetchWithRetry(ctx, fileID, nodeID, 1)
	}
	if err == nil {
		f.cache.put(fileID, nodeID, screens)
		return screens, nil
	}
	return nil, err
}

// fetchWithRetry calls fetchAndWalk up to 3 times, retrying on
// transient errors only:
//   - 429 → sleep Retry-After (clamped to [500ms, 30s])
//   - connection-reset / EOF / decode-EOF → exponential backoff (1s, 2s, 4s)
//
// Render-timeouts and 4xx (non-429) and "node not in response" are NOT
// retried here — they surface to ResolveSection which decides whether
// to swap scale (render-timeout) or fail (everything else).
func (f *FigmaClient) fetchWithRetry(ctx context.Context, fileID, nodeID string, scale int) ([]Screen, error) {
	const maxAttempts = 3
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			wait := backoffFor(attempt, lastErr)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			default:
			}
			f.doSleep(wait)
		}
		screens, err := f.fetchAndWalk(ctx, fileID, nodeID, scale)
		if err == nil {
			return screens, nil
		}
		lastErr = err
		if !isTransientFigmaErr(err) {
			return nil, err
		}
	}
	return nil, lastErr
}

func (f *FigmaClient) doSleep(d time.Duration) {
	if f.sleep != nil {
		f.sleep(d)
		return
	}
	time.Sleep(d)
}

// backoffFor computes how long to wait before retry attempt N (1-indexed,
// where attempt 2 is the first retry). For *rateLimitError carrying a
// Retry-After hint, honor it (clamped to [500ms, 30s]). Otherwise back
// off exponentially: 1s, 2s, 4s, capped at 8s.
func backoffFor(attempt int, err error) time.Duration {
	if rle, ok := err.(*rateLimitError); ok && rle.retryAfter > 0 {
		d := rle.retryAfter
		if d < 500*time.Millisecond {
			d = 500 * time.Millisecond
		}
		if d > 30*time.Second {
			d = 30 * time.Second
		}
		return d
	}
	d := time.Duration(1<<(attempt-2)) * time.Second
	if d > 8*time.Second {
		d = 8 * time.Second
	}
	return d
}

// isTransientFigmaErr reports whether err is worth retrying. Covers
// *rateLimitError plus the transport / decode failure modes Figma exhibits
// under load: connection reset, unexpected EOF mid-stream, and decode
// errors that surface as "unexpected end of JSON input" when the body
// arrived truncated. Render-timeouts are NOT transient here — they have
// their own scale-fallback path in ResolveSection.
func isTransientFigmaErr(err error) bool {
	if _, ok := err.(*rateLimitError); ok {
		return true
	}
	s := err.Error()
	switch {
	case strings.Contains(s, "do request:"):
		return true // transport-level: connection reset, timeout, DNS, etc.
	case strings.Contains(s, "read body:"):
		return true // ReadAll mid-stream failure (typically EOF / reset)
	case strings.Contains(s, "unexpected end of JSON input"):
		return true // truncated/empty body returned with a 200 status
	case strings.Contains(s, "unexpected EOF"):
		return true
	}
	return false
}

// rateLimitError signals Figma returned 429. The retryAfter field carries
// the parsed Retry-After header (zero when absent), for fetchWithRetry to
// honor. The Error string preserves the historical "figma: HTTP 429: ..."
// shape so existing log filters / regex matchers continue to match.
type rateLimitError struct {
	retryAfter time.Duration
	body       string
}

func (e *rateLimitError) Error() string {
	return fmt.Sprintf("figma: HTTP 429: %s", e.body)
}

// RetryAfter exposes the parsed Retry-After value. Mirrors the method-shaped
// probe in internal/projects/asset_export.go's retryAfterFromErr so the same
// duck-typed handling works there.
func (e *rateLimitError) RetryAfter() time.Duration { return e.retryAfter }

// fetchAndWalk does one REST call + tree walk. `scale` is preserved from
// the original plan as a hint for retry tiering, even though /v1/files/<id>/nodes
// has no scale parameter today.
func (f *FigmaClient) fetchAndWalk(ctx context.Context, fileID, nodeID string, scale int) ([]Screen, error) {
	// Cooperate with the per-PAT tier-1 bucket shared by every Client.New(pat)
	// caller in this process. /v1/files/<id>/nodes is documented as tier-1
	// (15 req/min on Pro, paced at 12 req/min by client.tier1RPM).
	if err := client.WaitTier1(ctx, f.pat); err != nil {
		return nil, fmt.Errorf("figma: rate-limit wait: %w", err)
	}

	base := f.baseURL
	if base == "" {
		base = "https://api.figma.com"
	}
	url := fmt.Sprintf("%s/v1/files/%s/nodes?ids=%s&geometry=paths",
		base, fileID, nodeID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("figma: build request: %w", err)
	}
	req.Header.Set("X-Figma-Token", f.pat)

	resp, err := f.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("figma: do request: %w", err)
	}
	defer resp.Body.Close()
	body, rerr := io.ReadAll(resp.Body)
	if rerr != nil {
		// Read failures (connection reset mid-body, transient EOF) used to
		// be silently dropped via `body, _ := io.ReadAll(...)` — the decoder
		// then reported "unexpected end of JSON input" with no upstream
		// signal that the network had truncated the response. Surface
		// explicitly so fetchWithRetry can decide to retry.
		return nil, fmt.Errorf("figma: read body: %w", rerr)
	}
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusTooManyRequests {
			return nil, &rateLimitError{
				retryAfter: parseRetryAfter(resp.Header.Get("Retry-After")),
				body:       string(body[:min(len(body), 200)]),
			}
		}
		// Detect Figma's render-timeout shape so the caller can retry.
		if resp.StatusCode == http.StatusBadRequest && bytesContain(body, "Render timeout") {
			return nil, &renderTimeoutError{status: resp.StatusCode, body: string(body[:min(len(body), 200)])}
		}
		return nil, fmt.Errorf("figma: HTTP %d: %s", resp.StatusCode, body[:min(len(body), 200)])
	}

	var resp2 figmaNodesResponse
	if err := json.Unmarshal(body, &resp2); err != nil {
		return nil, fmt.Errorf("figma: decode response: %w", err)
	}
	entry, ok := resp2.Nodes[nodeID]
	if !ok {
		return nil, fmt.Errorf("figma: node %q not in response", nodeID)
	}
	return walkScreens(entry.Document), nil
}

// parseRetryAfter handles the integer-seconds form of the Retry-After
// header. The HTTP-date form is allowed by RFC 7231 but Figma sends seconds
// in practice (and we have no precedent for the date form in the wild).
// Returns zero on absent / unparsable, which backoffFor maps to a 500ms floor.
func parseRetryAfter(h string) time.Duration {
	if h == "" {
		return 0
	}
	secs, err := strconv.Atoi(strings.TrimSpace(h))
	if err != nil || secs < 0 {
		return 0
	}
	return time.Duration(secs) * time.Second
}

// walkScreens recurses through SECTION / GROUP nodes and collects every
// FRAME / COMPONENT / INSTANCE / image-filled RECTANGLE child whose
// absolute bounding box is screen-shaped. Critically: STOPS at the FRAME
// boundary — we never recurse into a frame's children. This is what
// filters "every nested button/icon/sub-card" out of the screen list.
//
// Size gate: width ≥ minScreenWidth (excludes icon-tier debris); height
// ≥ minScreenHeight (low floor — designers also lay out short popup /
// info-card frames alongside fullscreens, e.g. 375×146 tooltips in the
// Gold-Silver flow, that pre-2026-05-08 were silently dropped).
//
// RECTANGLE handling: in flows like NRI VKYC the section contains
// screen-sized RECTANGLE nodes that are pasted screenshot references
// (Android/iOS captures, image fills) the team treats as part of the
// flow. We accept those by gating on hasImageFill — plain shape
// rectangles without an image fill are still ignored.
func walkScreens(node figmaNode) []Screen {
	var out []Screen
	walk(node, &out)
	return out
}

const (
	minScreenWidth  = 280
	minScreenHeight = 80
)

func walk(n figmaNode, out *[]Screen) {
	for _, c := range n.Children {
		switch c.Type {
		case "FRAME", "COMPONENT", "INSTANCE":
			if !appendIfScreenSized(c, out) {
				continue
			}
			// DO NOT recurse into FRAME — its children are sub-elements,
			// not screens.
		case "RECTANGLE":
			if !hasImageFill(c) {
				continue
			}
			appendIfScreenSized(c, out)
		case "SECTION", "GROUP":
			walk(c, out)
		}
	}
}

// appendIfScreenSized adds c to out when its bounding box passes the
// size gate. Returns false when skipped (and emits a debug log so we
// can audit dropped nodes when a leaf comes up short).
func appendIfScreenSized(c figmaNode, out *[]Screen) bool {
	b := c.AbsoluteBoundingBox
	if b == nil {
		slog.Debug("walk: skip (no bbox)", "id", c.ID, "name", c.Name, "type", c.Type)
		return false
	}
	if b.Width < minScreenWidth || b.Height < minScreenHeight {
		slog.Debug("walk: skip (under size floor)",
			"id", c.ID, "name", c.Name, "type", c.Type,
			"width", b.Width, "height", b.Height,
			"min_width", minScreenWidth, "min_height", minScreenHeight,
		)
		return false
	}
	*out = append(*out, Screen{
		ID:     c.ID,
		Name:   c.Name,
		Type:   c.Type,
		X:      b.X,
		Y:      b.Y,
		Width:  b.Width,
		Height: b.Height,
	})
	return true
}

func hasImageFill(c figmaNode) bool {
	for _, f := range c.Fills {
		if f.Type == "IMAGE" {
			return true
		}
	}
	return false
}

// ─── Wire shapes (Figma REST API) ──────────────────────────────────────────

type figmaNodesResponse struct {
	Nodes map[string]struct {
		Document figmaNode `json:"document"`
	} `json:"nodes"`
}

type figmaNode struct {
	ID                  string            `json:"id"`
	Name                string            `json:"name"`
	Type                string            `json:"type"`
	Children            []figmaNode       `json:"children"`
	AbsoluteBoundingBox *figmaBoundingBox `json:"absoluteBoundingBox"`
	Fills               []figmaFill       `json:"fills"`
}

type figmaBoundingBox struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// figmaFill is a minimal projection of the Figma fill object — we only
// need the type discriminator to gate RECTANGLE acceptance on IMAGE.
type figmaFill struct {
	Type string `json:"type"`
}

// ─── Errors ────────────────────────────────────────────────────────────────

type renderTimeoutError struct {
	status int
	body   string
}

func (e *renderTimeoutError) Error() string {
	return fmt.Sprintf("figma render timeout (HTTP %d): %s", e.status, e.body)
}

func isRenderTimeout(err error) bool {
	_, ok := err.(*renderTimeoutError)
	return ok
}

// ─── Cache ─────────────────────────────────────────────────────────────────

type figmaCache struct {
	mu      sync.Mutex
	entries map[string]figmaCacheEntry
	ttl     time.Duration
	cap     int
}

type figmaCacheEntry struct {
	screens []Screen
	addedAt time.Time
}

func newFigmaCache(cap int, ttl time.Duration) *figmaCache {
	return &figmaCache{
		entries: make(map[string]figmaCacheEntry, cap),
		ttl:     ttl,
		cap:     cap,
	}
}

func (c *figmaCache) get(fileID, nodeID string) ([]Screen, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := fileID + "|" + nodeID
	e, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	if time.Since(e.addedAt) > c.ttl {
		delete(c.entries, key)
		return nil, false
	}
	return e.screens, true
}

func (c *figmaCache) put(fileID, nodeID string, screens []Screen) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= c.cap {
		// Lazy eviction: drop one old entry. Not strict LRU but adequate
		// for a 200-entry cache that turns over once per cycle.
		var oldest string
		var oldestAt time.Time
		for k, v := range c.entries {
			if oldest == "" || v.addedAt.Before(oldestAt) {
				oldest, oldestAt = k, v.addedAt
			}
		}
		if oldest != "" {
			delete(c.entries, oldest)
		}
	}
	c.entries[fileID+"|"+nodeID] = figmaCacheEntry{screens: screens, addedAt: time.Now()}
}

// ─── Tiny helpers ──────────────────────────────────────────────────────────

func bytesContain(haystack []byte, needle string) bool {
	return strings.Contains(string(haystack), needle)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
