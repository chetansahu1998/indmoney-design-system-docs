package audit

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Publish endpoint — receives multi-selection uploads from the Figma plugin
// after auto-recognition. The plugin classifies each selected node into one
// of the kinds below and POSTs the result here; the server persists into
// lib/contributions/<file_slug>.json with dedup by figma_id.
//
// Contributions are advisory: an operator reviews the JSON in git and
// decides whether to merge the items into the canonical icons manifest.
// We never mutate the canonical manifest from a designer action.

type PublishKind string

const (
	PublishKindComponentSet  PublishKind = "component_set"
	PublishKindComponentMain PublishKind = "component_standalone"
	PublishKindComponentVar  PublishKind = "component_variant"
	PublishKindInstance      PublishKind = "instance"
	PublishKindOther         PublishKind = "other"
)

type PublishProperty struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type PublishVariant struct {
	VariantID  string            `json:"variant_id"`
	Name       string            `json:"name"`
	Properties []PublishProperty `json:"properties,omitempty"`
	Width      int               `json:"width"`
	Height     int               `json:"height"`
}

type PublishItem struct {
	Kind            PublishKind       `json:"kind"`
	FigmaID         string            `json:"figma_id"`
	Name            string            `json:"name"`
	ComponentSetID  string            `json:"component_set_id,omitempty"`
	ComponentKey    string            `json:"component_key,omitempty"`
	ParentSetName   string            `json:"parent_set_name,omitempty"`
	ParentSetID     string            `json:"parent_set_id,omitempty"`
	Variants        []PublishVariant  `json:"variants,omitempty"`
	Properties      []PublishProperty `json:"properties,omitempty"`
	Width           int               `json:"width"`
	Height          int               `json:"height"`
	Description     string            `json:"description,omitempty"`
	StyleIDs        []string          `json:"style_ids,omitempty"`
	CapturedAt      time.Time         `json:"captured_at"`
}

type PublishRequest struct {
	FileKey   string        `json:"file_key"`
	FileName  string        `json:"file_name"`
	Brand     string        `json:"brand"`
	Selections []PublishItem `json:"selections"`
}

type PublishResponse struct {
	OK          bool   `json:"ok"`
	WrittenPath string `json:"written_path"`
	NewCount    int    `json:"new_count"`
	UpdatedCount int   `json:"updated_count"`
	TotalCount  int    `json:"total_count"`
}

type contributionsDoc struct {
	SchemaVersion string         `json:"schema_version"`
	FileKey       string         `json:"file_key"`
	FileName      string         `json:"file_name"`
	Brand         string         `json:"brand"`
	UpdatedAt     time.Time      `json:"updated_at"`
	Selections    []PublishItem  `json:"selections"`
	Extensions    map[string]any `json:"$extensions,omitempty"`
}

var publishMu sync.Mutex

// HandlePublish persists incoming selection metadata to
// lib/contributions/<file_slug>.json with dedup-by-figma_id.
func HandlePublish(cfg HandlerConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 50<<20)
		var req PublishRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf("parse body: %s", err), http.StatusBadRequest)
			return
		}
		if req.FileKey == "" {
			http.Error(w, "file_key required", http.StatusBadRequest)
			return
		}
		if len(req.Selections) == 0 {
			http.Error(w, "no selections", http.StatusBadRequest)
			return
		}

		publishMu.Lock()
		defer publishMu.Unlock()

		dir := filepath.Join(cfg.RepoRoot, "lib/contributions")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			http.Error(w, fmt.Sprintf("mkdir: %s", err), http.StatusInternalServerError)
			return
		}

		slug := slugifyForFileKey(req.FileKey, req.FileName)
		path := filepath.Join(dir, slug+".json")

		var doc contributionsDoc
		if bs, err := os.ReadFile(path); err == nil {
			_ = json.Unmarshal(bs, &doc)
		}
		doc.SchemaVersion = SchemaVersion
		doc.FileKey = req.FileKey
		doc.FileName = req.FileName
		doc.Brand = req.Brand
		doc.UpdatedAt = time.Now().UTC()
		if doc.Extensions == nil {
			doc.Extensions = map[string]any{}
		}
		doc.Extensions["com.indmoney.provenance"] = "figma-plugin-publish"

		newCount, updatedCount := mergeSelections(&doc, req.Selections)

		bs, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			http.Error(w, fmt.Sprintf("marshal: %s", err), http.StatusInternalServerError)
			return
		}
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, append(bs, '\n'), 0o644); err != nil {
			http.Error(w, fmt.Sprintf("write: %s", err), http.StatusInternalServerError)
			return
		}
		if err := os.Rename(tmp, path); err != nil {
			http.Error(w, fmt.Sprintf("rename: %s", err), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(PublishResponse{
			OK:           true,
			WrittenPath:  path,
			NewCount:     newCount,
			UpdatedCount: updatedCount,
			TotalCount:   len(doc.Selections),
		})
	}
}

// mergeSelections dedups by figma_id. Existing entries are updated in-place
// (preserves the earliest captured_at if present); new entries are appended.
// Returns (newCount, updatedCount).
func mergeSelections(doc *contributionsDoc, incoming []PublishItem) (int, int) {
	idx := map[string]int{}
	for i, s := range doc.Selections {
		idx[s.FigmaID] = i
	}
	now := time.Now().UTC()
	newCount, updatedCount := 0, 0
	for _, s := range incoming {
		if s.FigmaID == "" {
			continue
		}
		if s.CapturedAt.IsZero() {
			s.CapturedAt = now
		}
		if i, ok := idx[s.FigmaID]; ok {
			// Preserve earliest captured_at; update everything else.
			if !doc.Selections[i].CapturedAt.IsZero() &&
				doc.Selections[i].CapturedAt.Before(s.CapturedAt) {
				s.CapturedAt = doc.Selections[i].CapturedAt
			}
			doc.Selections[i] = s
			updatedCount++
		} else {
			doc.Selections = append(doc.Selections, s)
			idx[s.FigmaID] = len(doc.Selections) - 1
			newCount++
		}
	}
	// Stable order: kind, then name.
	sort.SliceStable(doc.Selections, func(i, j int) bool {
		if doc.Selections[i].Kind != doc.Selections[j].Kind {
			return rankKind(doc.Selections[i].Kind) < rankKind(doc.Selections[j].Kind)
		}
		return strings.ToLower(doc.Selections[i].Name) < strings.ToLower(doc.Selections[j].Name)
	})
	return newCount, updatedCount
}

func rankKind(k PublishKind) int {
	switch k {
	case PublishKindComponentSet:
		return 0
	case PublishKindComponentMain:
		return 1
	case PublishKindComponentVar:
		return 2
	case PublishKindInstance:
		return 3
	}
	return 4
}
