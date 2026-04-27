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
