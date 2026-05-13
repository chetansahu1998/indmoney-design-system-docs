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
	token    string
	http     *http.Client
	limiters *limiters
}

// New constructs a Client. PAT must include file_content:read. The Client
// carries process-local per-tier token buckets (tier1=12 RPM, tier2=40 RPM,
// tier3=80 RPM, all 80% of the documented Professional-plan caps). The
// buckets are SHARED across every Client constructed for the same PAT
// via limitersForPAT — production main.go builds a fresh client.New(pat)
// per request inside several closures, so a per-Client limiter would be
// no rate limit at all. Figma's documented cap is per PAT, so the shared
// key is the PAT itself.
func New(pat string) *Client {
	return &Client{
		token:    pat,
		http:     &http.Client{Timeout: 5 * time.Minute},
		limiters: limitersForPAT(pat),
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

func (c *Client) get(ctx context.Context, path string, out any, tier rateTier) error {
	if c.limiters != nil {
		if err := c.limiters.wait(ctx, tier); err != nil {
			return err
		}
	}
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
	if err := c.get(ctx, path, &out, tier1); err != nil {
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
	// `&geometry=paths` makes the API include `fillGeometry` and
	// `strokeGeometry` arrays on every shape node — SVG path strings the
	// canvas-v2 walker can emit as <svg><path/></svg> for real vector
	// rendering. Without it, VECTOR / ELLIPSE / LINE nodes return only
	// their bbox + fill colour and our renderer paints them as plain
	// coloured divs (icons render as solid rectangles). The geometry
	// payload roughly doubles the response size for icon-heavy frames
	// but is the only way to render true vector shapes in DOM.
	path := fmt.Sprintf("/v1/files/%s/nodes?ids=%s&geometry=paths", fileKey, csv)
	if depth > 0 {
		path += "&depth=" + strconv.Itoa(depth)
	}
	var out map[string]any
	if err := c.get(ctx, path, &out, tier1); err != nil {
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
	if err := c.get(ctx, "/v1/files/"+fileKey+"/components", &out, tier3); err != nil {
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
	if err := c.get(ctx, "/v1/files/"+fileKey+"/component_sets", &out, tier3); err != nil {
		return nil, err
	}
	return out, nil
}

// GetStyles fetches the published-styles list for the file.
// Used to extract typography (TEXT styles) which Glyph DOES expose.
func (c *Client) GetStyles(ctx context.Context, fileKey string) (map[string]any, error) {
	var out map[string]any
	if err := c.get(ctx, "/v1/files/"+fileKey+"/styles", &out, tier3); err != nil {
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
	if err := c.get(ctx, "/v1/files/"+fileKey+"/variables/local", &out, tier2); err != nil {
		return nil, err
	}
	return out, nil
}

// GetPublishedVariables fetches `/v1/files/<key>/variables/published` — the
// subset of variables the file has explicitly published as a library. Requires
// `file_variables:read`. Useful when consuming a downstream design-system file.
func (c *Client) GetPublishedVariables(ctx context.Context, fileKey string) (map[string]any, error) {
	var out map[string]any
	if err := c.get(ctx, "/v1/files/"+fileKey+"/variables/published", &out, tier2); err != nil {
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
	if err := c.get(ctx, path, &raw, tier1); err != nil {
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
	if err := c.get(ctx, path, &raw, tier2); err != nil {
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
	if err := c.get(ctx, "/v1/me", &out, tier3); err != nil {
		return nil, err
	}
	return out, nil
}

// Token returns the bearer token (used by helper packages that make their own
// HTTP requests against /v1/images, etc.).
func (c *Client) Token() string { return c.token }

// ─── Inventory endpoints (FIGMA DB — internal/figma/inventory) ──────────────
//
// The inventory poller mirrors team > project > file > page > section as
// metadata only. These three endpoints are the cheap fan-out path:
//
//   /v1/teams/<id>/projects        — list projects in a team    (tier 2)
//   /v1/projects/<id>/files        — list files in a project    (tier 2)
//   /v1/files/<key>?depth=2        — pages + their top-level    (tier 1)
//                                    SECTION children
//
// All three live behind the existing per-PAT rate limiter; tier 2 = 40 RPM
// (80% of 50), tier 1 = 12 RPM (80% of 15). The poller runs every 5 minutes
// and pages/sections are only refetched when /v1/projects/<id>/files
// reports a newer `last_modified` than the cached row.

// TeamProject is one row of GET /v1/teams/<id>/projects.
type TeamProject struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// TeamProjectsResponse is the full response shape.
type TeamProjectsResponse struct {
	Name     string        `json:"name"`     // team name (the only place the API surfaces it)
	Projects []TeamProject `json:"projects"`
}

// GetTeamProjects fetches `/v1/teams/<team_id>/projects`. Tier-2.
//
// Requires `projects:read` on the PAT. Returns 403 if the PAT's account
// can't see the team (handled by *APIError.IsAuth at the call site).
func (c *Client) GetTeamProjects(ctx context.Context, teamID string) (*TeamProjectsResponse, error) {
	if teamID == "" {
		return nil, errors.New("teamID is empty")
	}
	var out TeamProjectsResponse
	if err := c.get(ctx, "/v1/teams/"+teamID+"/projects", &out, tier2); err != nil {
		return nil, err
	}
	return &out, nil
}

// ProjectFile is one row of GET /v1/projects/<id>/files. The `last_modified`
// field is the cheap change-detection signal the inventory poller uses to
// decide whether to do the expensive depth=2 page/section refresh.
type ProjectFile struct {
	Key          string `json:"key"`
	Name         string `json:"name"`
	ThumbnailURL string `json:"thumbnail_url"`
	LastModified string `json:"last_modified"`
}

// ProjectFilesResponse wraps the file list.
type ProjectFilesResponse struct {
	Name  string        `json:"name"` // project name
	Files []ProjectFile `json:"files"`
}

// GetProjectFiles fetches `/v1/projects/<project_id>/files`. Tier-2.
func (c *Client) GetProjectFiles(ctx context.Context, projectID string) (*ProjectFilesResponse, error) {
	if projectID == "" {
		return nil, errors.New("projectID is empty")
	}
	var out ProjectFilesResponse
	if err := c.get(ctx, "/v1/projects/"+projectID+"/files", &out, tier2); err != nil {
		return nil, err
	}
	return &out, nil
}

// FilePagesAndSections is the trimmed shape of GET /v1/files/<key>?depth=2
// that the inventory walker needs: file-level metadata plus pages, plus
// each page's immediate SECTION children.
type FilePagesAndSections struct {
	Name         string         `json:"name"`
	Role         string         `json:"role"`
	LastModified string         `json:"lastModified"`
	EditorType   string         `json:"editorType"`
	ThumbnailURL string         `json:"thumbnailUrl"`
	Version      string         `json:"version"`
	LinkAccess   string         `json:"linkAccess"`
	MainFileKey  string         `json:"mainFileKey"`
	Document     filePagesDoc   `json:"document"`
}

// filePagesDoc is the document subtree. We only consume `children` (pages).
type filePagesDoc struct {
	Children []filePageNode `json:"children"`
}

// filePageNode is one CANVAS child (a page). We retain `backgroundColor`
// when present and walk `children` for SECTION nodes.
type filePageNode struct {
	ID              string             `json:"id"`
	Name            string             `json:"name"`
	Type            string             `json:"type"` // "CANVAS"
	BackgroundColor *figmaColor        `json:"backgroundColor,omitempty"`
	Children        []filePageChildRaw `json:"children"`
}

// filePageChildRaw is a top-level child of a page. We accept anything but
// surface only nodes whose Type == "SECTION" to the caller. `absoluteBoundingBox`
// is the bbox in canvas coordinates.
type filePageChildRaw struct {
	ID                  string       `json:"id"`
	Name                string       `json:"name"`
	Type                string       `json:"type"`
	AbsoluteBoundingBox *figmaBBox   `json:"absoluteBoundingBox,omitempty"`
}

type figmaBBox struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

type figmaColor struct {
	R float64 `json:"r"`
	G float64 `json:"g"`
	B float64 `json:"b"`
	A float64 `json:"a"`
}

// Pages returns the canvas children as a typed page slice (callers don't
// need to know about CANVAS vs other top-level types — the API guarantees
// document.children are pages).
func (f *FilePagesAndSections) Pages() []FilePage {
	out := make([]FilePage, 0, len(f.Document.Children))
	for _, p := range f.Document.Children {
		page := FilePage{
			ID:   p.ID,
			Name: p.Name,
		}
		if p.BackgroundColor != nil {
			page.BackgroundColorHex = colorToHex(*p.BackgroundColor)
		}
		for _, c := range p.Children {
			if c.Type != "SECTION" {
				continue
			}
			s := FileSection{ID: c.ID, Name: c.Name}
			if c.AbsoluteBoundingBox != nil {
				s.X = c.AbsoluteBoundingBox.X
				s.Y = c.AbsoluteBoundingBox.Y
				s.Width = c.AbsoluteBoundingBox.Width
				s.Height = c.AbsoluteBoundingBox.Height
			}
			page.Sections = append(page.Sections, s)
		}
		out = append(out, page)
	}
	return out
}

// FilePage is the inventory-poller-friendly representation of one CANVAS.
type FilePage struct {
	ID                 string
	Name               string
	BackgroundColorHex string
	Sections           []FileSection
}

// FileSection is one SECTION node directly under a page.
type FileSection struct {
	ID     string
	Name   string
	X      float64
	Y      float64
	Width  float64
	Height float64
}

func colorToHex(c figmaColor) string {
	clamp := func(v float64) int {
		i := int(v*255 + 0.5)
		if i < 0 {
			return 0
		}
		if i > 255 {
			return 255
		}
		return i
	}
	return fmt.Sprintf("#%02x%02x%02x", clamp(c.R), clamp(c.G), clamp(c.B))
}

// GetFilePagesAndSections fetches `/v1/files/<key>?depth=2`. Tier-1.
//
// depth=2 returns the file's document with each page's direct children
// included but NOT recursed — exactly what the inventory poller wants
// (pages + top-level SECTION nodes, nothing deeper). The response is
// typically <1 MB even for large product files because frame interiors
// are pruned.
//
// Deprecated by `GetFileDeepTree` in Phase 2C (2026-05-13). Kept here so
// any external caller still hitting depth=2 keeps working, but the
// inventory poller now uses GetFileDeepTree which returns this same
// pages/sections view *plus* the full descendant tree in one call.
func (c *Client) GetFilePagesAndSections(ctx context.Context, fileKey string) (*FilePagesAndSections, error) {
	if fileKey == "" {
		return nil, errors.New("fileKey is empty")
	}
	var out FilePagesAndSections
	if err := c.get(ctx, "/v1/files/"+fileKey+"?depth=2", &out, tier1); err != nil {
		return nil, err
	}
	return &out, nil
}

// ─── Deep tree fetch (Phase 2C — figma_node table) ───────────────────────────
//
// The inventory poller upgraded from depth=2 (pages + sections only) to a
// configurable depth (default 14) so we can mirror the full structural
// tree of every file as metadata-only rows in figma_node. Same single
// /v1/files/<key> call, same tier-1 rate budget — just a deeper response.

// DeepNode is the typed shape of one Figma node in the deep walk. Mirrors
// the API response, scoped to ONLY the fields the inventory needs:
// identity, type, name, bbox, component master reference. Everything else
// (fills, strokes, effects, characters, styles) is dropped at decode time
// to keep the in-memory tree compact.
type DeepNode struct {
	ID                  string      `json:"id"`
	Name                string      `json:"name"`
	Type                string      `json:"type"`
	AbsoluteBoundingBox *figmaBBox  `json:"absoluteBoundingBox,omitempty"`
	BackgroundColor     *figmaColor `json:"backgroundColor,omitempty"`
	// componentId — populated only on INSTANCE nodes; the node_id of the
	// master COMPONENT this instance was spawned from. The master may
	// live in this file OR in a remote library file. Either way, the
	// file-level `components` map (FileDeepTree.Components) carries the
	// durable key for whichever node_id this is, so the walker can
	// enrich the INSTANCE row with component_key at flatten time.
	ComponentID string     `json:"componentId,omitempty"`
	Children    []DeepNode `json:"children,omitempty"`
}

// componentMetadata is the per-master entry in the file-level
// `components` / `componentSets` maps. The map is keyed on the master's
// node_id (whether that master is local to this file or remote from a
// library). The `key` field is the durable library identifier that's
// stable across publish cycles — the join key for cross-file usage.
type componentMetadata struct {
	Key           string `json:"key"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	Remote        bool   `json:"remote"`
	DocumentationLinks []struct {
		URI string `json:"uri"`
	} `json:"documentationLinks,omitempty"`
}

// FileDeepTree is the file-level response. Reuses the same top-level shape
// as FilePagesAndSections so callers can lift pages/sections out of it
// without a second decode.
type FileDeepTree struct {
	Name         string   `json:"name"`
	Role         string   `json:"role"`
	LastModified string   `json:"lastModified"`
	EditorType   string   `json:"editorType"`
	ThumbnailURL string   `json:"thumbnailUrl"`
	Version      string   `json:"version"`
	LinkAccess   string   `json:"linkAccess"`
	MainFileKey  string   `json:"mainFileKey"`
	Document     DeepNode `json:"document"`
	// File-level metadata for component masters referenced anywhere in
	// this file's tree (either as local COMPONENT/COMPONENT_SET nodes
	// or as remote library masters reached via INSTANCE.componentId).
	// Keyed on the master's node_id (same id space as ComponentID).
	Components    map[string]componentMetadata `json:"components,omitempty"`
	ComponentSets map[string]componentMetadata `json:"componentSets,omitempty"`
}

// FlatNode is the shape the repository layer consumes — one row per node,
// with parent/depth/order_index resolved during the walk.
type FlatNode struct {
	NodeID       string
	ParentID     string  // empty on the document root
	NodeType     string
	Name         string
	HasBBox      bool    // true when AbsoluteBoundingBox was present
	X            float64
	Y            float64
	Width        float64
	Height       float64
	Depth        int     // 0 = document, 1 = page, 2 = top-level frame, ...
	OrderIndex   int     // sibling order within parent
	ComponentID  string  // for INSTANCE → master's same-file node_id
	ComponentKey string  // for COMPONENT / COMPONENT_SET → durable library key
}

// Flatten walks the deep tree depth-first and emits one FlatNode per
// visited node. Walk order is parent-before-children so a caller can
// stream rows into an INSERT without needing to defer parent_id resolution.
//
// While walking, each node gets enriched with `component_key` looked up
// from the file-level Components / ComponentSets maps:
//   - COMPONENT     → key from Components[node.id]
//   - COMPONENT_SET → key from ComponentSets[node.id]
//   - INSTANCE      → key from Components[node.componentId] (the master,
//                     local or remote — the file-level map carries the
//                     durable key either way)
//
// This is the join hinge for cross-file usage queries: every INSTANCE
// of "button-primary" across every file carries the same component_key
// regardless of which library file owns the master.
//
// The caller's responsibility:
//   - Pass the FlatNode list to a single batched UPSERT (the figma_node
//     PK enforces uniqueness on (tenant, file_key, node_id)).
//   - Walk stops naturally at the leaves Figma returned for the requested
//     depth — no extra capping needed here.
func (f *FileDeepTree) Flatten() []FlatNode {
	if f == nil {
		return nil
	}
	// Pre-allocate a reasonable starting size — most files land in the
	// 100-5000-node range; this avoids the first few growth allocations.
	out := make([]FlatNode, 0, 1024)

	// Helper: resolve durable key for a node, falling back across both
	// component maps. Returns "" when the node isn't a known master.
	lookupKey := func(nodeID string) string {
		if nodeID == "" {
			return ""
		}
		if md, ok := f.Components[nodeID]; ok && md.Key != "" {
			return md.Key
		}
		if md, ok := f.ComponentSets[nodeID]; ok && md.Key != "" {
			return md.Key
		}
		return ""
	}

	var walk func(node *DeepNode, parentID string, depth, orderIndex int)
	walk = func(node *DeepNode, parentID string, depth, orderIndex int) {
		fn := FlatNode{
			NodeID:      node.ID,
			ParentID:    parentID,
			NodeType:    node.Type,
			Name:        node.Name,
			Depth:       depth,
			OrderIndex:  orderIndex,
			ComponentID: node.ComponentID,
		}
		// Resolve component_key from the file-level maps. For
		// COMPONENT/COMPONENT_SET the lookup is on this node's own id;
		// for INSTANCE it's on the master's id (componentId).
		switch node.Type {
		case "COMPONENT", "COMPONENT_SET":
			fn.ComponentKey = lookupKey(node.ID)
		case "INSTANCE":
			fn.ComponentKey = lookupKey(node.ComponentID)
		}
		if node.AbsoluteBoundingBox != nil {
			fn.HasBBox = true
			fn.X = node.AbsoluteBoundingBox.X
			fn.Y = node.AbsoluteBoundingBox.Y
			fn.Width = node.AbsoluteBoundingBox.Width
			fn.Height = node.AbsoluteBoundingBox.Height
		}
		out = append(out, fn)
		for i := range node.Children {
			walk(&node.Children[i], node.ID, depth+1, i)
		}
	}
	walk(&f.Document, "", 0, 0)
	return out
}

// Pages returns the file's pages + their top-level SECTION children, in
// the same shape FilePagesAndSections.Pages produced. Lets the poller
// keep the existing figma_page / figma_section writes unchanged while
// adding the deep node-tree write on the side.
func (f *FileDeepTree) Pages() []FilePage {
	out := make([]FilePage, 0, len(f.Document.Children))
	for _, p := range f.Document.Children {
		page := FilePage{ID: p.ID, Name: p.Name}
		if p.BackgroundColor != nil {
			page.BackgroundColorHex = colorToHex(*p.BackgroundColor)
		}
		for _, c := range p.Children {
			if c.Type != "SECTION" {
				continue
			}
			s := FileSection{ID: c.ID, Name: c.Name}
			if c.AbsoluteBoundingBox != nil {
				s.X = c.AbsoluteBoundingBox.X
				s.Y = c.AbsoluteBoundingBox.Y
				s.Width = c.AbsoluteBoundingBox.Width
				s.Height = c.AbsoluteBoundingBox.Height
			}
			page.Sections = append(page.Sections, s)
		}
		out = append(out, page)
	}
	return out
}

// GetFileDeepTree fetches `/v1/files/<key>?depth=<n>`. Tier-1.
//
// depth=14 (Phase 2C default) returns the full document tree clamped at
// 14 levels deep. Figma doesn't document a max depth, but 14 covers
// every production INDmoney file we've inspected with margin to spare
// (real trees bottom out around depth 8-10 for the heaviest screens).
//
// Response size scales with tree breadth, not depth — a flat page with
// 2000 frames produces a bigger payload than a deeply-nested artboard
// with 200 nodes. The 1 GB body cap in Client.get covers both shapes.
//
// To minimize payload, the typed DeepNode struct drops every field the
// inventory doesn't need (fills, effects, characters, styles, …) at
// json.Unmarshal time — only the 9 fields listed on DeepNode survive.
func (c *Client) GetFileDeepTree(ctx context.Context, fileKey string, depth int) (*FileDeepTree, error) {
	if fileKey == "" {
		return nil, errors.New("fileKey is empty")
	}
	if depth <= 0 {
		depth = 14
	}
	path := fmt.Sprintf("/v1/files/%s?depth=%d", fileKey, depth)
	var out FileDeepTree
	if err := c.get(ctx, path, &out, tier1); err != nil {
		return nil, err
	}
	return &out, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
}
