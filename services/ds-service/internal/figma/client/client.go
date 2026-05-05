// Package client is a minimal Figma REST API client.
//
// Scoped to what the pair-walker needs: file fetch, node fetch, styles list.
// No external deps — stdlib only. Inspired by DesignBrain-AI's
// EnhancedRESTClient but pared down to ~100 LOC.
package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

const baseURL = "https://api.figma.com"

// Client wraps an authenticated Figma PAT.
type Client struct {
	token string
	http  *http.Client
}

// New constructs a Client. PAT must include file_content:read.
func New(pat string) *Client {
	return &Client{
		token: pat,
		http:  &http.Client{Timeout: 5 * time.Minute},
	}
}

// APIError is returned for any non-2xx response. Callers can switch on Status.
type APIError struct {
	Status     int
	Body       string
	RetryAfter time.Duration // populated on 429
	URL        string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("figma API %s: %d — %s", e.URL, e.Status, e.Body)
}

// IsRateLimit reports whether this error is a 429. Callers should backoff RetryAfter.
func (e *APIError) IsRateLimit() bool { return e.Status == http.StatusTooManyRequests }

// IsAuth reports whether this is a 401/403 (PAT problem).
func (e *APIError) IsAuth() bool {
	return e.Status == http.StatusUnauthorized || e.Status == http.StatusForbidden
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	url := baseURL + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("X-Figma-Token", c.token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("transport: %w", err)
	}
	defer resp.Body.Close()

	// Cap at 1 GB. Real INDmoney product files (INDstocks V4, Dashboard v5,
	// Help Center V2) routinely exceed 200 MB once their full node trees
	// + auto-layout + style refs serialize, so the previous 200 MB cap
	// silently truncated and the JSON decoder reported "unexpected end of
	// JSON input" instead of a useful error. 1 GB matches what the Figma
	// dashboard reports as the practical upper bound today.
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<30))

	if resp.StatusCode/100 != 2 {
		ae := &APIError{Status: resp.StatusCode, Body: truncate(string(body), 4000), URL: url}
		if resp.StatusCode == http.StatusTooManyRequests {
			if ra := resp.Header.Get("Retry-After"); ra != "" {
				if secs, err := strconv.Atoi(ra); err == nil {
					ae.RetryAfter = time.Duration(secs) * time.Second
				}
			}
		}
		return ae
	}

	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode: %w (first 200 bytes: %s)", err, truncate(string(body), 200))
	}
	return nil
}

// GetFile fetches `/v1/files/<key>`. depth limits node tree expansion (1=pages only).
// Pass depth=0 for full file (large — multi-MB for production app files).
func (c *Client) GetFile(ctx context.Context, fileKey string, depth int) (map[string]any, error) {
	path := "/v1/files/" + fileKey
	if depth > 0 {
		path += "?depth=" + strconv.Itoa(depth)
	}
	var out map[string]any
	if err := c.get(ctx, path, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetFileNodes fetches `/v1/files/<key>/nodes?ids=<csv>`.
// nodeIDs MUST be in canonical "X:Y" form (NOT "X-Y").
func (c *Client) GetFileNodes(ctx context.Context, fileKey string, nodeIDs []string, depth int) (map[string]any, error) {
	if len(nodeIDs) == 0 {
		return nil, errors.New("nodeIDs is empty")
	}
	csv := nodeIDs[0]
	for _, id := range nodeIDs[1:] {
		csv += "," + id
	}
	path := fmt.Sprintf("/v1/files/%s/nodes?ids=%s", fileKey, csv)
	if depth > 0 {
		path += "&depth=" + strconv.Itoa(depth)
	}
	var out map[string]any
	if err := c.get(ctx, path, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetFileComponents fetches `/v1/files/<key>/components` — the file's published
// components with their durable Component.Key (stable across edits/publishes).
// Each entry carries node_id + key + name + description, which lets us
// cross-reference Figma node trees back to the durable identifier the
// Plugin API uses for `importComponentByKeyAsync`.
func (c *Client) GetFileComponents(ctx context.Context, fileKey string) (map[string]any, error) {
	var out map[string]any
	if err := c.get(ctx, "/v1/files/"+fileKey+"/components", &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetFileComponentSets fetches `/v1/files/<key>/component_sets` — the file's
// published component sets (variant matrices). Same shape as GetFileComponents
// but for COMPONENT_SET nodes. Sets are what we treat as "the component" in
// the docs site and the audit; their key is what survives publish-cycles.
func (c *Client) GetFileComponentSets(ctx context.Context, fileKey string) (map[string]any, error) {
	var out map[string]any
	if err := c.get(ctx, "/v1/files/"+fileKey+"/component_sets", &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetStyles fetches the published-styles list for the file.
// Used to extract typography (TEXT styles) which Glyph DOES expose.
func (c *Client) GetStyles(ctx context.Context, fileKey string) (map[string]any, error) {
	var out map[string]any
	if err := c.get(ctx, "/v1/files/"+fileKey+"/styles", &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetLocalVariables fetches `/v1/files/<key>/variables/local`.
// Returns variables + collections defined in the file. Requires the PAT to
// include `file_variables:read` AND the file owner to be on a Pro/Org plan.
// Returns a 403 with helpful message on Free plans — caller can degrade gracefully.
func (c *Client) GetLocalVariables(ctx context.Context, fileKey string) (map[string]any, error) {
	var out map[string]any
	if err := c.get(ctx, "/v1/files/"+fileKey+"/variables/local", &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetPublishedVariables fetches `/v1/files/<key>/variables/published` — the
// subset of variables the file has explicitly published as a library. Requires
// `file_variables:read`. Useful when consuming a downstream design-system file.
func (c *Client) GetPublishedVariables(ctx context.Context, fileKey string) (map[string]any, error) {
	var out map[string]any
	if err := c.get(ctx, "/v1/files/"+fileKey+"/variables/published", &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetImages fetches `/v1/images/<key>?ids=<csv>&format={png|svg}&scale={1|2|3}`.
// Returns a node-id → signed CDN URL map. The URLs are short-lived; callers
// must download the bytes promptly (the asset-export proxy in projects.U4
// downloads inline under a separate per-tenant rate-limit bucket).
//
// nodeIDs MUST be in canonical "X:Y" form. format must be "png" or "svg".
// scale must be 1, 2, or 3 (Figma rejects other values; for SVG it's silently
// ignored but we still pass it for symmetry).
//
// Reuses Client.get's transport, 1 GB body cap, and APIError surface. 429s
// surface as *APIError with RetryAfter populated; callers should backoff
// (mirrors pipeline.go renderChunk's 3-attempt pattern).
func (c *Client) GetImages(ctx context.Context, fileKey string, nodeIDs []string, format string, scale int) (map[string]string, error) {
	if len(nodeIDs) == 0 {
		return nil, errors.New("nodeIDs is empty")
	}
	if format != "png" && format != "svg" {
		return nil, fmt.Errorf("unsupported format %q (want png|svg)", format)
	}
	if scale < 1 || scale > 3 {
		return nil, fmt.Errorf("unsupported scale %d (want 1|2|3)", scale)
	}
	csv := nodeIDs[0]
	for _, id := range nodeIDs[1:] {
		csv += "," + id
	}
	path := fmt.Sprintf("/v1/images/%s?ids=%s&format=%s&scale=%d",
		fileKey, csv, format, scale)
	var raw struct {
		Err    any               `json:"err"`
		Images map[string]string `json:"images"`
	}
	if err := c.get(ctx, path, &raw); err != nil {
		return nil, err
	}
	if raw.Err != nil {
		return nil, fmt.Errorf("figma images api err: %v", raw.Err)
	}
	return raw.Images, nil
}

// GetFileImageFills fetches `/v1/files/<file_key>/images` — the imageRef
// resolver, distinct from /v1/images which renders arbitrary nodes.
//
// Figma stores raster fills (photos, embedded illustrations, raster icons)
// as content-addressed S3 blobs keyed by a sha1-derived `imageRef`. Every
// IMAGE-type Paint in canonical_tree carries that `imageRef`, but the URL
// is NOT included — the renderer must call this endpoint once per file to
// get a `{imageRef → s3-url}` map.
//
// The S3 URLs returned here expire (~24h, undocumented but observed).
// Callers MUST cache the bytes, not the URLs.
//
// Response shape (Figma API):
//
//	{ "error": false, "status": 200, "meta": { "images": { "<imageRef>": "<s3-url>", ... } } }
func (c *Client) GetFileImageFills(ctx context.Context, fileKey string) (map[string]string, error) {
	if fileKey == "" {
		return nil, errors.New("fileKey is empty")
	}
	path := fmt.Sprintf("/v1/files/%s/images", fileKey)
	var raw struct {
		Error  any `json:"error"`
		Status int `json:"status"`
		Meta   struct {
			Images map[string]string `json:"images"`
		} `json:"meta"`
	}
	if err := c.get(ctx, path, &raw); err != nil {
		return nil, err
	}
	// Figma returns `error: false` (boolean) on success. Treat any non-false,
	// non-nil error field as a failure.
	if raw.Error != nil && raw.Error != false {
		return nil, fmt.Errorf("figma file-images api err: %v", raw.Error)
	}
	return raw.Meta.Images, nil
}

// Identity returns `/v1/me` — used for preflight smoke tests.
func (c *Client) Identity(ctx context.Context) (map[string]any, error) {
	var out map[string]any
	if err := c.get(ctx, "/v1/me", &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Token returns the bearer token (used by helper packages that make their own
// HTTP requests against /v1/images, etc.).
func (c *Client) Token() string { return c.token }

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
}
