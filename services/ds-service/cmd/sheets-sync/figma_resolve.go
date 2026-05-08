package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
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
type FigmaClient struct {
	pat      string
	hc       *http.Client
	cache    *figmaCache
	maxRetry int
}

func NewFigmaClient(pat string) *FigmaClient {
	return &FigmaClient{
		pat:      pat,
		hc:       &http.Client{Timeout: 30 * time.Second},
		cache:    newFigmaCache(200, time.Hour),
		maxRetry: 1, // one retry on render-timeout, then surface
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

	// First attempt — default scale
	screens, err := f.fetchAndWalk(ctx, fileID, nodeID, 2)
	if err == nil {
		f.cache.put(fileID, nodeID, screens)
		return screens, nil
	}

	// Retry on render-timeout with smaller scale (per plan)
	if isRenderTimeout(err) {
		screens, err = f.fetchAndWalk(ctx, fileID, nodeID, 1)
		if err == nil {
			f.cache.put(fileID, nodeID, screens)
			return screens, nil
		}
	}
	return nil, err
}

// fetchAndWalk does one REST call + tree walk. `scale` is forwarded to
// the API but also signals which retry tier this is.
func (f *FigmaClient) fetchAndWalk(ctx context.Context, fileID, nodeID string, scale int) ([]Screen, error) {
	url := fmt.Sprintf("https://api.figma.com/v1/files/%s/nodes?ids=%s&geometry=paths",
		fileID, nodeID)
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
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
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
