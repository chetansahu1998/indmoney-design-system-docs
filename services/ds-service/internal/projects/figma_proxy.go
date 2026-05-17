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

// figmaCacheKeyWithFormat extends figmaCacheKey with format + scale axes for
// the asset-export proxy (U4). Asset exports under the same (tenant, file, node)
// tuple may render at multiple format/scale variants (PNG@1x for thumbnails,
// PNG@3x for retina, SVG for icons), each with its own pre-signed Figma URL —
// the cache must keep them distinct or different consumers stomp each other.
//
// The base figmaCacheKey is preserved for the legacy thumbnailed-frame proxy
// where format=png/scale=1 is implicit; this helper exists for callers that
// need format/scale-aware keys without forking the cache shape.
func figmaCacheKeyWithFormat(tenantID, fileKey, nodeID, format string, scale int) string {
	return fmt.Sprintf("%s:%s:%s:%s:%d", tenantID, fileKey, nodeID, format, scale)
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

// ─── U1 — frame-PNG byte proxy ──────────────────────────────────────────────
//
// HandleFigmaFramePNG (registered in cmd/server/main.go) accepts
// ?file_key=<k>&node_id=<id>&scale=1|2 and returns PNG bytes proxied from
// Figma's /v1/images endpoint. Used by the PRD viewer's Wall + FrameGrid so
// the corkboard renders real thumbnails instead of placeholder glyphs.
//
// We don't 302 to Figma's pre-signed S3 URL because (a) it would leak the
// signed URL into the browser's network panel and CDN logs, and (b) it
// would short-circuit our 5-min cache for repeat scrolls.
//
// Cache axes: (tenant_id, file_key, node_id, scale). Stored as raw bytes +
// content-type so a re-serve is a single map read + Write.

// figmaPNGCacheEntry holds the bytes for a single (file_key, node_id, scale)
// thumbnail. Pinned to the tenant via the cache key.
type figmaPNGCacheEntry struct {
	body        []byte
	contentType string
	at          time.Time
}

// figmaPNGCache mirrors figmaProxyCache's shape but keys on the bytes form.
// Same TTL; same simple flat-map + RWMutex layout. The two caches stay
// separate because the value types are different — combining them would
// force every read site through a type-switch for no gain.
type figmaPNGCache struct {
	mu  sync.RWMutex
	ttl time.Duration
	m   map[string]figmaPNGCacheEntry
}

func (c *figmaPNGCache) get(key string) (figmaPNGCacheEntry, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.m[key]
	if !ok {
		return figmaPNGCacheEntry{}, false
	}
	if time.Since(e.at) > c.ttl {
		return figmaPNGCacheEntry{}, false
	}
	return e, true
}

func (c *figmaPNGCache) set(key string, e figmaPNGCacheEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[key] = e
}

// figmaPNGProxy is the singleton cache for HandleFigmaFramePNG. 5-min TTL
// matches the figmaProxy metadata cache + Figma's documented S3 URL lifetime
// (~30 min, we stay well under).
var figmaPNGProxy = &figmaPNGCache{
	ttl: 5 * time.Minute,
	m:   map[string]figmaPNGCacheEntry{},
}

// figmaNodeIDRe matches the canonical Figma node id form ("X:Y") with
// reasonable upper bounds on each component. Figma node ids are always two
// non-negative integers separated by a colon; we accept up to 12 digits per
// side which exceeds any real ID by orders of magnitude. The query string
// also accepts the "X-Y" wire form (Figma's URL shape) — handlers normalise
// before this check.
var figmaNodeIDRe = regexp.MustCompile(`^[0-9]{1,12}:[0-9]{1,12}$`)

// figmaFileKeyRe matches the canonical Figma file key — alphanumeric only,
// no slashes / dots / brackets / etc. Figma's keys are ~22 chars but we
// accept up to 64 for headroom on future formats.
var figmaFileKeyRe = regexp.MustCompile(`^[a-zA-Z0-9]{1,64}$`)

// FigmaFramePNGAssetTokenTTL — short-lived token validity for the
// PRD viewer's thumbnail <img> tags. Matches the existing PNG-asset token
// TTL pattern (auth.AssetTokenTTL = 60s). The viewer re-mints when a page
// reloads; in-flight image loads finish well within 60s.
//
// 10 minutes here instead of 60s because the PRD viewer can sit open for a
// while and lazy-load thumbnails as the wall scrolls — a 60s window would
// force re-mints just to scroll an unchanged corkboard. The cache+TTL on
// the bytes side caps the actual Figma exposure at 5 minutes regardless.
const FigmaFramePNGAssetTokenTTL = 10 * time.Minute

// FigmaFramePNGAssetID returns the opaque resource id baked into the asset
// token's MAC. Re-uses auth.AssetTokenSigner (which is keyed (tenant_id,
// resource_id, expires)) without inventing a new signer type — the resource
// id slot was historically named "screen_id" but is content-agnostic.
//
// Format: "figma:<file_key>:<node_id>:<scale>". The scale is part of the
// MAC so a token minted for scale=1 can't be replayed against scale=2.
func FigmaFramePNGAssetID(fileKey, nodeID string, scale int) string {
	return fmt.Sprintf("figma:%s:%s:%d", fileKey, nodeID, scale)
}

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

// wait blocks until a token is available for tenantID under the provided
// refill cadence + burst, or ctx is cancelled. Mirrors the Go stdlib
// `golang.org/x/time/rate` Limiter.Wait semantics but stays in-house so we
// don't take a new dep — tokens are computed from `refillEvery`/`burst` so
// callers can run different limits per call site (URL-fetch vs bytes-download).
//
// Returns nil on success, ctx.Err() on cancel.
func (rl *figmaRateLimiter) wait(ctx context.Context, tenantID string, burst int, refillEvery time.Duration) error {
	if burst <= 0 {
		burst = 1
	}
	if refillEvery <= 0 {
		refillEvery = 200 * time.Millisecond
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		now := time.Now()
		rl.mu.Lock()
		b, ok := rl.buckets[tenantID]
		if !ok {
			rl.buckets[tenantID] = &figmaBucket{
				tokens:   float64(burst - 1),
				updated:  now,
				lastUsed: now,
			}
			rl.mu.Unlock()
			return nil
		}
		elapsed := now.Sub(b.updated)
		if elapsed > 0 {
			refill := float64(elapsed) / float64(refillEvery)
			b.tokens += refill
			if b.tokens > float64(burst) {
				b.tokens = float64(burst)
			}
			b.updated = now
		}
		if b.tokens >= 1 {
			b.tokens--
			b.lastUsed = now
			rl.mu.Unlock()
			return nil
		}
		// Compute time until next token.
		need := 1 - b.tokens
		sleep := time.Duration(need * float64(refillEvery))
		rl.mu.Unlock()
		// Cap wait slice to keep ctx-cancel responsive.
		if sleep > 50*time.Millisecond {
			sleep = 50 * time.Millisecond
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(sleep):
		}
	}
}
