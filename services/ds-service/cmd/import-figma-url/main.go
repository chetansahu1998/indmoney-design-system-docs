// import-figma-url — one-shot Figma URL importer.
//
// Bypasses sheets-sync for Figma URLs that aren't (yet) in the
// source-of-truth spreadsheet. Resolves a section node to its screen
// frames the same way cmd/sheets-sync's figma_resolve.go does, then
// POSTs an ExportRequest to /v1/projects/export — same contract the
// figma plugin and sheets-sync use, so the result lands in the same
// pipeline (Stages 2-9, blocklist, cluster prerender).
//
// Usage:
//   go run ./cmd/import-figma-url \
//     -file-id 2m7ouydXKfxYk7hhjQxrt7 \
//     -node-id 8695:78076 \
//     -name "INDstocks V4 sub-flow"
//
// Env:
//   FIGMA_PERSONAL_ACCESS_TOKEN  required — the PAT the rest of the
//                                stack uses for Figma /v1/files reads.
//   DS_SERVICE_BEARER            required — super-admin JWT for the
//                                /v1/projects/export POST.
//   DS_SERVICE_URL               optional, default http://localhost:8080
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	figmaclient "github.com/indmoney/design-system-docs/services/ds-service/internal/figma/client"
)

const (
	minScreenWidth  = 280
	minScreenHeight = 80
)

func main() {
	var (
		fileID   = flag.String("file-id", "", "Figma file key (required)")
		nodeID   = flag.String("node-id", "", "Figma node id, e.g. 8695:78076 (required)")
		flowName = flag.String("name", "", "Display name for the imported flow")
		platform = flag.String("platform", "mobile", "Platform: mobile | web")
		product  = flag.String("product", "INDstocks", "Product name")
		path     = flag.String("path", "Imports/Ad-hoc", "Project taxonomy path")
		persona  = flag.String("persona-name", "default", "Persona name")
		verbose  = flag.Bool("verbose", false, "Verbose logging")
	)
	flag.Parse()

	if *fileID == "" || *nodeID == "" {
		fmt.Fprintln(os.Stderr, "ERROR: -file-id and -node-id are required")
		flag.Usage()
		os.Exit(2)
	}
	// Figma URLs use `X-Y` in node-id query param; the API expects `X:Y`.
	*nodeID = strings.ReplaceAll(*nodeID, "-", ":")

	pat := os.Getenv("FIGMA_PERSONAL_ACCESS_TOKEN")
	if pat == "" {
		fmt.Fprintln(os.Stderr, "ERROR: FIGMA_PERSONAL_ACCESS_TOKEN env var required")
		os.Exit(2)
	}
	bearer := os.Getenv("DS_SERVICE_BEARER")
	if bearer == "" {
		fmt.Fprintln(os.Stderr, "ERROR: DS_SERVICE_BEARER env var required (super-admin JWT)")
		os.Exit(2)
	}
	dsURL := os.Getenv("DS_SERVICE_URL")
	if dsURL == "" {
		dsURL = "http://localhost:8080"
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	if *verbose {
		log = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// ── Step 1: resolve the section node to its screen frames ──────────
	log.Info("resolving section", "file_id", *fileID, "node_id", *nodeID)
	client := figmaclient.New(pat)
	nodesResp, err := client.GetFileNodes(ctx, *fileID, []string{*nodeID}, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: GetFileNodes: %v\n", err)
		os.Exit(1)
	}

	// /v1/files/<key>/nodes shape: { nodes: { "<nodeID>": { document: {...} } } }
	inner, _ := nodesResp["nodes"].(map[string]any)
	if inner == nil {
		fmt.Fprintf(os.Stderr, "ERROR: response missing `nodes` envelope: %+v\n", nodesResp)
		os.Exit(1)
	}
	entry, _ := inner[*nodeID].(map[string]any)
	if entry == nil {
		fmt.Fprintf(os.Stderr, "ERROR: node %q not in response (Figma may have rejected it as inaccessible)\n", *nodeID)
		os.Exit(1)
	}
	document, _ := entry["document"].(map[string]any)
	if document == nil {
		fmt.Fprintf(os.Stderr, "ERROR: node %q has no document body\n", *nodeID)
		os.Exit(1)
	}
	fileName, _ := nodesResp["name"].(string)
	if fileName == "" {
		fileName = "Imported file " + *fileID
	}
	rootName, _ := document["name"].(string)
	flowDisplayName := *flowName
	if flowDisplayName == "" {
		flowDisplayName = rootName
	}
	if flowDisplayName == "" {
		flowDisplayName = "Imported flow"
	}

	// ── Step 2: walk for top-level screen frames ────────────────────────
	screens := walkScreens(document)
	if len(screens) == 0 {
		fmt.Fprintf(os.Stderr, "ERROR: node %q has no screen-shaped frames inside it (the section is empty, or it IS a screen — try its parent SECTION)\n", *nodeID)
		os.Exit(1)
	}
	log.Info("resolved section",
		"file_name", fileName,
		"root_name", rootName,
		"flow_name", flowDisplayName,
		"screen_count", len(screens),
	)
	if *verbose {
		for i, s := range screens {
			log.Debug("screen", "i", i, "id", s.ID, "name", s.Name,
				"w", s.Width, "h", s.Height)
		}
	}

	// ── Step 3: build ExportRequest payload ─────────────────────────────
	// Matches the shape ds-service's HandleExport expects (see
	// internal/projects/types.go ExportRequest / FlowPayload / FramePayload).
	type framePayload struct {
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
	type flowPayload struct {
		SectionID   *string        `json:"section_id"`
		FrameIDs    []string       `json:"frame_ids"`
		Frames      []framePayload `json:"frames"`
		Platform    string         `json:"platform"`
		Product     string         `json:"product"`
		Path        string         `json:"path"`
		PersonaName string         `json:"persona_name"`
		Name        string         `json:"name"`
	}
	type exportRequest struct {
		IdempotencyKey string        `json:"idempotency_key"`
		FileID         string        `json:"file_id"`
		FileName       string        `json:"file_name"`
		Flows          []flowPayload `json:"flows"`
	}

	frames := make([]framePayload, 0, len(screens))
	frameIDs := make([]string, 0, len(screens))
	for _, s := range screens {
		frames = append(frames, framePayload{
			FrameID: s.ID,
			X:       s.X, Y: s.Y,
			Width: s.Width, Height: s.Height,
			Name: s.Name,
		})
		frameIDs = append(frameIDs, s.ID)
	}
	sectionID := *nodeID
	payload := exportRequest{
		IdempotencyKey: uuid.NewString(),
		FileID:         *fileID,
		FileName:       fileName,
		Flows: []flowPayload{{
			SectionID:   &sectionID,
			FrameIDs:    frameIDs,
			Frames:      frames,
			Platform:    *platform,
			Product:     *product,
			Path:        *path,
			PersonaName: *persona,
			Name:        flowDisplayName,
		}},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: marshal payload: %v\n", err)
		os.Exit(1)
	}

	// ── Step 4: POST to /v1/projects/export ─────────────────────────────
	log.Info("posting export", "ds_url", dsURL, "bytes", len(body))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		dsURL+"/v1/projects/export", bytes.NewReader(body))
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: build request: %v\n", err)
		os.Exit(1)
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: POST: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode/100 != 2 {
		fmt.Fprintf(os.Stderr, "ERROR: HTTP %d: %s\n", resp.StatusCode, string(respBody))
		os.Exit(1)
	}

	var out map[string]any
	_ = json.Unmarshal(respBody, &out)
	fmt.Println()
	fmt.Println("✓ imported successfully")
	fmt.Printf("  project_id: %v\n", out["project_id"])
	fmt.Printf("  version_id: %v\n", out["version_id"])
	fmt.Printf("  trace_id:   %v\n", out["trace_id"])
	if dl, _ := out["deeplink"].(string); dl != "" {
		fmt.Printf("  deeplink:   %s\n", dl)
	}
	fmt.Println()
	fmt.Println("Pipeline runs in the background. Watch ds-server log for completion, or:")
	fmt.Printf("  sqlite3 services/ds-service/data/ds.db \"SELECT status FROM project_versions WHERE id='%v';\"\n", out["version_id"])
}

// screen mirrors sheets-sync's Screen — one renderable frame inside a section.
type screen struct {
	ID     string
	Name   string
	Type   string
	X      float64
	Y      float64
	Width  float64
	Height float64
}

// walkScreens — direct port of cmd/sheets-sync/figma_resolve.go's
// walkScreens. Walks SECTION / GROUP nodes and collects top-level
// FRAME / COMPONENT / INSTANCE / image-filled RECTANGLE children
// whose bbox is screen-shaped.
func walkScreens(node map[string]any) []screen {
	var out []screen
	walk(node, &out)
	return out
}

func walk(n map[string]any, out *[]screen) {
	children, _ := n["children"].([]any)
	for _, raw := range children {
		c, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		ctype, _ := c["type"].(string)
		switch ctype {
		case "FRAME", "COMPONENT", "INSTANCE":
			appendIfScreenSized(c, out)
			// DO NOT recurse — frame children are sub-elements
		case "RECTANGLE":
			if hasImageFill(c) {
				appendIfScreenSized(c, out)
			}
		case "SECTION", "GROUP":
			walk(c, out)
		}
	}
}

func appendIfScreenSized(c map[string]any, out *[]screen) bool {
	bb, _ := c["absoluteBoundingBox"].(map[string]any)
	if bb == nil {
		return false
	}
	w, _ := bb["width"].(float64)
	h, _ := bb["height"].(float64)
	if w < minScreenWidth || h < minScreenHeight {
		return false
	}
	x, _ := bb["x"].(float64)
	y, _ := bb["y"].(float64)
	id, _ := c["id"].(string)
	name, _ := c["name"].(string)
	ctype, _ := c["type"].(string)
	*out = append(*out, screen{
		ID: id, Name: name, Type: ctype,
		X: x, Y: y, Width: w, Height: h,
	})
	return true
}

func hasImageFill(c map[string]any) bool {
	fills, _ := c["fills"].([]any)
	for _, f := range fills {
		fm, ok := f.(map[string]any)
		if !ok {
			continue
		}
		if t, _ := fm["type"].(string); t == "IMAGE" {
			return true
		}
	}
	return false
}
