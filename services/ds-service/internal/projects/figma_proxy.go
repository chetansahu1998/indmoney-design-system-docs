package projects

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Phase 5.2 P4 — Figma frame-metadata proxy.
//
// The DRD's figmaLink custom block renders a thumbnailed card for any
// Figma URL a designer pastes. To get a real thumbnail (vs. the
// gradient placeholder) we need to call Figma's /v1/images endpoint,
// which requires a Personal Access Token. PATs are stored per-tenant
// (figma_tokens table); this proxy decrypts the PAT, calls Figma, and
// returns just the public-CDN PNG URL Figma responds with.
//
// Cache is in-memory + 5min TTL keyed by (tenant_id, file_key, node_id).
// The Figma image URLs are pre-signed S3 with their own expiry, so the
// 5min cache stays well under that.

// FigmaFrameMetadata is the response shape served by HandleFigmaFrameMetadata.
type FigmaFrameMetadata struct {
	FileKey      string `json:"file_key"`
	NodeID       string `json:"node_id"`
	Title        string `json:"title"`
	ThumbnailURL string `json:"thumbnail_url,omitempty"`
	Source       string `json:"source"` // "figma" | "url-parse"
}

// figmaURLPattern matches both file/<key> and design/<key> URL shapes.
// `<key>` is capture group 1; the title segment (used as fallback when
// Figma's API isn't reachable) is group 2.
var figmaURLPattern = regexp.MustCompile(
	`https?://(?:www\.)?figma\.com/(?:file|design|proto)/([a-zA-Z0-9]+)/?([^/?#]*)?`,
)

// ParseFigmaURL extracts the file key + a friendly title + a node_id
// when the URL has a node-id query param. Pure function — no network.
func ParseFigmaURL(raw string) (FigmaFrameMetadata, error) {
	m := figmaURLPattern.FindStringSubmatch(raw)
	if m == nil {
		return FigmaFrameMetadata{}, fmt.Errorf("not a figma url: %q", raw)
	}
	out := FigmaFrameMetadata{FileKey: m[1], Source: "url-parse"}
	if len(m) > 2 && m[2] != "" {
		out.Title = strings.ReplaceAll(m[2], "-", " ")
	}
	u, err := url.Parse(raw)
	if err == nil {
		// node-id is the canonical Figma frame reference. Figma writes
		// it in two formats: "1:23" and "1-23"; normalise to colon.
		if id := u.Query().Get("node-id"); id != "" {
			out.NodeID = strings.ReplaceAll(id, "-", ":")
		}
	}
	return out, nil
}

// figmaProxyCache is a tiny in-process LRU-ish map. We use a flat map
// + a 5min sweep ticker for simplicity; the working set fits in <100
// entries because a flow rarely embeds more than a handful of figmaLinks.
type figmaProxyCache struct {
	mu  sync.RWMutex
	ttl time.Duration
	m   map[string]figmaProxyCacheEntry
}

type figmaProxyCacheEntry struct {
	val FigmaFrameMetadata
	at  time.Time
}

// figmaProxy holds the singleton cache. Re-used across requests; the
// http handler reaches in via package-level helpers.
var figmaProxy = &figmaProxyCache{
	ttl: 5 * time.Minute,
	m:   map[string]figmaProxyCacheEntry{},
}

func figmaCacheKey(tenantID, fileKey, nodeID string) string {
	return tenantID + ":" + fileKey + ":" + nodeID
}

func (c *figmaProxyCache) get(key string) (FigmaFrameMetadata, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.m[key]
	if !ok {
		return FigmaFrameMetadata{}, false
	}
	if time.Since(entry.at) > c.ttl {
		return FigmaFrameMetadata{}, false
	}
	return entry.val, true
}

func (c *figmaProxyCache) set(key string, val FigmaFrameMetadata) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[key] = figmaProxyCacheEntry{val: val, at: time.Now()}
}

// fetchFigmaThumbnailURL calls Figma's /v1/images endpoint to get a
// signed PNG URL for the given node_id. Returns the URL or an error
// (4xx mapped to "not_found", 5xx surfaces as a transient error so
// callers can retry).
func fetchFigmaThumbnailURL(ctx context.Context, pat, fileKey, nodeID string) (string, error) {
	if pat == "" {
		return "", errors.New("missing figma PAT")
	}
	endpoint := fmt.Sprintf(
		"https://api.figma.com/v1/images/%s?ids=%s&format=png&scale=1",
		url.PathEscape(fileKey),
		url.QueryEscape(nodeID),
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("X-Figma-Token", pat)
	resp, err := figmaHTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusForbidden {
		return "", errFigmaNotFound
	}
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", fmt.Errorf("figma images: HTTP %d — %s", resp.StatusCode, string(body))
	}
	var body struct {
		Images map[string]string `json:"images"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("figma images decode: %w", err)
	}
	thumb, ok := body.Images[nodeID]
	if !ok || thumb == "" {
		return "", errFigmaNotFound
	}
	return thumb, nil
}

// errFigmaNotFound is a sentinel returned by fetchFigmaThumbnailURL when
// the file or node id resolves to 4xx. Handler maps to 404 with a hint.
var errFigmaNotFound = errors.New("figma: file or node not found")

// figmaHTTPClient is a single http.Client with a strict 8s timeout. All
// figma proxy calls share it so connection reuse + timeouts apply.
var figmaHTTPClient = &http.Client{Timeout: 8 * time.Second}

// ─── Phase 5.3 P2 — per-tenant rate limit ───────────────────────────────────
//
// A malicious tab embedding lots of Figma URLs could otherwise drain a
// tenant's Figma PAT quota. A simple token bucket keyed by tenant_id
// caps requests at FigmaProxyBurstSize tokens with one token refilling
// every FigmaProxyRefillEvery interval.
//
// The 5min server-side cache (figmaProxyCache above) absorbs the common
// case — repeat fetches of the same URL hit memory, not Figma. The rate
// limit only kicks in when a tab is requesting many *distinct* URLs in
// rapid succession.

const (
	// 5 burst, refilled at 1 token per 200ms ≈ 5 req/sec sustained.
	FigmaProxyBurstSize   = 5
	FigmaProxyRefillEvery = 200 * time.Millisecond
)

type figmaRateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*figmaBucket
}

type figmaBucket struct {
	tokens   float64
	updated  time.Time
	lastUsed time.Time
}

var figmaProxyLimiter = &figmaRateLimiter{
	buckets: map[string]*figmaBucket{},
}

// allow returns true and decrements when the tenant has at least one
// token; false otherwise. Tokens refill at 1 per FigmaProxyRefillEvery,
// capped at FigmaProxyBurstSize.
func (rl *figmaRateLimiter) allow(tenantID string, now time.Time) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	b, ok := rl.buckets[tenantID]
	if !ok {
		// Brand-new tenant — start with a full bucket and consume one.
		rl.buckets[tenantID] = &figmaBucket{
			tokens:   float64(FigmaProxyBurstSize - 1),
			updated:  now,
			lastUsed: now,
		}
		return true
	}

	elapsed := now.Sub(b.updated)
	if elapsed > 0 {
		refill := float64(elapsed) / float64(FigmaProxyRefillEvery)
		b.tokens += refill
		if b.tokens > float64(FigmaProxyBurstSize) {
			b.tokens = float64(FigmaProxyBurstSize)
		}
		b.updated = now
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	b.lastUsed = now
	return true
}
