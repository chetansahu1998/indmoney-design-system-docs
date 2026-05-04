package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// export.go — POST a NormalizedRow to /v1/projects/export.
//
// One ExportRequest per row. Sub-sheet name → synthetic file_id
// (sheet:<tab-slug>) so multiple sheet rows referencing different real
// Figma files all live under one project on the brain.

// ExportRequest mirrors the ds-service contract exactly. Field names
// match server-side json tags so json.Marshal produces a payload the
// existing HandleExport accepts without changes.
type ExportRequest struct {
	IdempotencyKey string         `json:"idempotency_key"`
	FileID         string         `json:"file_id"`
	FileName       string         `json:"file_name"`
	Flows          []FlowPayload  `json:"flows"`
}

type FlowPayload struct {
	SectionID   *string         `json:"section_id"`
	FrameIDs    []string        `json:"frame_ids"`
	Frames      []FramePayload  `json:"frames"`
	Platform    string          `json:"platform"`
	Product     string          `json:"product"`
	Path        string          `json:"path"`
	PersonaName string          `json:"persona_name"`
	Name        string          `json:"name"`
	ModeGroups  []any           `json:"mode_groups"`
}

type FramePayload struct {
	FrameID                   string  `json:"frame_id"`
	X                         float64 `json:"x"`
	Y                         float64 `json:"y"`
	Width                     float64 `json:"width"`
	Height                    float64 `json:"height"`
	Name                      string  `json:"name,omitempty"`
	VariableCollectionID      string  `json:"variable_collection_id,omitempty"`
	ModeID                    string  `json:"mode_id,omitempty"`
	ModeLabel                 string  `json:"mode_label,omitempty"`
	ExplicitVariableModesJSON string  `json:"explicit_variable_modes_json,omitempty"`
}

// ExportResponse is the success shape from /v1/projects/export.
type ExportResponse struct {
	ProjectID string `json:"project_id"`
	VersionID string `json:"version_id"`
	Deeplink  string `json:"deeplink"`
	TraceID   string `json:"trace_id"`
}

// Exporter wraps the POST + Tier-2 GDoc fetch + the per-cycle GDoc cache.
type Exporter struct {
	dsURL      string
	bearer     string
	hc         *http.Client
	gdocClient *GDocFetcher
	dryRun     bool
}

func NewExporter(dsURL, bearer string, gdoc *GDocFetcher, dryRun bool) *Exporter {
	return &Exporter{
		dsURL:      dsURL,
		bearer:     bearer,
		hc:         &http.Client{Timeout: 60 * time.Second},
		gdocClient: gdoc,
		dryRun:     dryRun,
	}
}

// ExportRow builds + POSTs the ExportRequest for a NormalizedRow with its
// resolved Figma frames. Returns the new project_id + version_id, or an
// error.
func (e *Exporter) ExportRow(ctx context.Context, row NormalizedRow, screens []Screen) (*ExportResponse, error) {
	subSlug := slugify(row.Mapping.Product)
	syntheticFileID := "sheet:" + subSlug

	frames := make([]FramePayload, 0, len(screens))
	frameIDs := make([]string, 0, len(screens))
	for _, s := range screens {
		frames = append(frames, FramePayload{
			FrameID:   s.ID,
			X:         s.X,
			Y:         s.Y,
			Width:     s.Width,
			Height:    s.Height,
			Name:      s.Name,
			ModeLabel: "default", // see "blank PNG" diagnosis — mode pair handling out of scope here
		})
		frameIDs = append(frameIDs, s.ID)
	}

	sectionID := row.NodeID // reuse node-id as the section identifier
	flowName := row.Project
	if flowName == "" {
		flowName = "(untitled)"
	}
	platform := inferPlatformFromScreens(screens) // mobile|web|""

	req := ExportRequest{
		IdempotencyKey: idempotencyKey(row),
		FileID:         row.FileID,            // real Figma file (per-flow)
		FileName:       row.Mapping.Product,   // brain-level node name (the sub-sheet's product)
		Flows: []FlowPayload{{
			SectionID:   &sectionID,
			FrameIDs:    frameIDs,
			Frames:      frames,
			Platform:    platform,
			Product:     row.Mapping.Product,
			Path:        "",
			PersonaName: "",
			Name:        flowName,
			ModeGroups:  nil,
		}},
	}
	// The synthetic file_id ensures we have a stable project per
	// sub-sheet — even though the FlowPayload's Frames live in the real
	// Figma file. Note: the export endpoint dedupes projects on
	// (tenant_id, slug) which derives from FileID — so all rows under
	// the same sub-sheet land under the same project.
	if subSlug != "" {
		// Tag the file_id with the synthetic slug for the project-level
		// uniqueness; ds-service writes flow.file_id from the FlowPayload's
		// fileID separately.
		_ = syntheticFileID // currently unused — TODO U9: pipe through to a SubSheet field on ExportRequest if the server adds support
	}

	if e.dryRun {
		out, _ := json.MarshalIndent(req, "", "  ")
		fmt.Printf("[DRY RUN] %s/%s: %d frames\n%s\n", row.Tab, row.Project, len(frames), out)
		return &ExportResponse{ProjectID: "(dry-run)", VersionID: "(dry-run)"}, nil
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("export: marshal: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		e.dsURL+"/v1/projects/export", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("export: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+e.bearer)
	resp, err := e.hc.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("export: do: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusConflict {
		// Idempotency hit — server already has this version. Treat as success.
		var existing ExportResponse
		_ = json.Unmarshal(respBody, &existing)
		return &existing, nil
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("export: HTTP %d: %s", resp.StatusCode, respBody[:min(len(respBody), 200)])
	}
	var out ExportResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, fmt.Errorf("export: decode response: %w", err)
	}
	return &out, nil
}

// idempotencyKey is sha256(spreadsheet|tab|row_index|row_hash). Same row
// re-imported across cycles produces the same key, so the server-side
// idempotency cache short-circuits.
func idempotencyKey(r NormalizedRow) string {
	parts := strings.Join([]string{r.Tab, itoa(r.RowIndex), r.RowHash}, "|")
	h := sha256.Sum256([]byte(parts))
	return hex.EncodeToString(h[:16]) // first 16 bytes is plenty
}

// slugify turns a product name into a stable kebab-case slug for the
// synthetic file_id. "INDstocks" → "indstocks", "BBPS & TPAP" →
// "bbps-tpap", "Insta Cash" → "insta-cash".
func slugify(s string) string {
	var b strings.Builder
	prevDash := true // start as if previous was a separator so leading non-alphanums are stripped
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + 32)
			prevDash = false
		default:
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	out := b.String()
	if strings.HasSuffix(out, "-") {
		out = out[:len(out)-1]
	}
	return out
}

// inferPlatformFromScreens checks median frame width.
//   median ≤ 480 → mobile
//   median ≥ 1280 → web
//   mixed/empty → "" (let the server figure it out)
func inferPlatformFromScreens(screens []Screen) string {
	if len(screens) == 0 {
		return ""
	}
	widths := make([]float64, 0, len(screens))
	for _, s := range screens {
		widths = append(widths, s.Width)
	}
	// Quick median via inline simple sort (n is tiny).
	for i := 1; i < len(widths); i++ {
		for j := i; j > 0 && widths[j-1] > widths[j]; j-- {
			widths[j], widths[j-1] = widths[j-1], widths[j]
		}
	}
	median := widths[len(widths)/2]
	if median <= 480 {
		return "mobile"
	}
	if median >= 1280 {
		return "web"
	}
	return ""
}

// ─── Tier-2 Google Doc fetch ───────────────────────────────────────────────

// GDocFetcher pulls a Google Doc as plain text via the SA bearer + a
// 1-hour LRU. Used to populate flows.external_drd_title +
// external_drd_snippet on rows with a Google Doc DRD.
type GDocFetcher struct {
	saCredsPath string
	hc          *http.Client
	cache       map[string]gdocCacheEntry
	mu          sync.Mutex // sync imported in figma_resolve.go; keep cache local-here
	ttl         time.Duration
	tokenStore  *gdocTokenStore
}

type gdocCacheEntry struct {
	title   string
	snippet string
	addedAt time.Time
}

type gdocTokenStore struct {
	mu      sync.Mutex
	token   string
	expiry  time.Time
}

func NewGDocFetcher(saCredsPath string) *GDocFetcher {
	return &GDocFetcher{
		saCredsPath: saCredsPath,
		hc:          &http.Client{Timeout: 30 * time.Second},
		cache:       map[string]gdocCacheEntry{},
		ttl:         time.Hour,
		tokenStore:  &gdocTokenStore{},
	}
}

// FetchSnippet returns (title, snippet) for a Google Doc. On any error
// (doc not shared with SA, API not enabled, network blip) returns empty
// strings + the error so the caller can decide whether to log + skip.
func (g *GDocFetcher) FetchSnippet(ctx context.Context, docID string) (title, snippet string, err error) {
	g.mu.Lock()
	if e, ok := g.cache[docID]; ok && time.Since(e.addedAt) < g.ttl {
		g.mu.Unlock()
		return e.title, e.snippet, nil
	}
	g.mu.Unlock()

	tok, err := g.minToken(ctx)
	if err != nil {
		return "", "", fmt.Errorf("gdoc: mint token: %w", err)
	}

	url := "https://docs.google.com/document/d/" + docID + "/export?format=txt"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := g.hc.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("gdoc: do: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound {
		// Doc not shared with SA. Telemetry signal at the call site.
		return "", "", &gdocNotSharedError{docID: docID, status: resp.StatusCode}
	}
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("gdoc: HTTP %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	title, snippet = splitTitleAndSnippet(string(body))
	g.mu.Lock()
	g.cache[docID] = gdocCacheEntry{title: title, snippet: snippet, addedAt: time.Now()}
	g.mu.Unlock()
	return title, snippet, nil
}

type gdocNotSharedError struct {
	docID  string
	status int
}

func (e *gdocNotSharedError) Error() string {
	return fmt.Sprintf("gdoc not shared with SA: doc=%s status=%d", e.docID, e.status)
}

// IsGDocNotShared reports whether err is a gdocNotSharedError.
func IsGDocNotShared(err error) bool {
	_, ok := err.(*gdocNotSharedError)
	return ok
}

// googleTokenSource builds an oauth2.TokenSource from a SA JSON file.
// Wraps Google's official `google.JWTConfigFromJSON` which knows how to
// sign + exchange the JWT correctly against any Google API.
func googleTokenSource(ctx context.Context, credsPath, scope string) (oauth2.TokenSource, error) {
	data, err := os.ReadFile(credsPath)
	if err != nil {
		return nil, err
	}
	cfg, err := google.JWTConfigFromJSON(data, scope)
	if err != nil {
		return nil, err
	}
	return cfg.TokenSource(ctx), nil
}

// splitTitleAndSnippet — first non-empty line is the title, remainder
// flattened to a single space-separated string capped at 500 chars.
// Matches the format returned by docs export?format=txt: title + blank
// line + body. The title sometimes has a UTF-8 BOM; strip it.
func splitTitleAndSnippet(text string) (title, snippet string) {
	text = strings.TrimPrefix(text, "\ufeff") // BOM
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		if strings.TrimSpace(l) != "" {
			title = strings.TrimSpace(l)
			rest := strings.Join(lines[i+1:], " ")
			snippet = strings.TrimSpace(strings.Join(strings.Fields(rest), " "))
			break
		}
	}
	if len(snippet) > 500 {
		snippet = snippet[:500] + "…"
	}
	return title, snippet
}

// minToken mints (or reuses) a Drive-readonly access token from the SA
// JSON. Uses Google's official google.golang.org/api/idtoken-style
// token source so we don't hand-roll JWT signing. Cached for ~55min.
func (g *GDocFetcher) minToken(ctx context.Context) (string, error) {
	g.tokenStore.mu.Lock()
	defer g.tokenStore.mu.Unlock()
	if g.tokenStore.token != "" && time.Until(g.tokenStore.expiry) > 5*time.Minute {
		return g.tokenStore.token, nil
	}

	ts, err := googleTokenSource(ctx, g.saCredsPath, "https://www.googleapis.com/auth/drive.readonly")
	if err != nil {
		return "", fmt.Errorf("gdoc: token source: %w", err)
	}
	tok, err := ts.Token()
	if err != nil {
		return "", fmt.Errorf("gdoc: get token: %w", err)
	}
	g.tokenStore.token = tok.AccessToken
	g.tokenStore.expiry = tok.Expiry
	return tok.AccessToken, nil
}
