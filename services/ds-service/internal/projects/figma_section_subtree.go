package projects

import (
	"encoding/json"
	"fmt"
)

// figma_section_subtree.go — per-section zstd-compressed subtree blob
// encoders for migration 0030's figma_section.subtree_json_zstd column.
// Plan: docs/plans/2026-05-14-002-feat-figma-section-subtree-blob-plan.md.
//
// Replaces the row-per-Figma-node figma_node table from migration 0027.
// The poller (U4) calls EncodeSubtreeBlob with the section's flat-node
// descendant list and writes the resulting bytes to the column. The
// autosync planner reader (U5 LoadSectionSubtree) calls DecodeSubtreeBlob
// to materialize the same []FigmaNodeRow back for ExportRequest building.
//
// Wire format: zstd-compressed UTF-8 JSON array of figmaNodeBlobEntry. The
// blob entry uses short JSON keys (id, type, w, h, etc.) because the same
// key strings repeat once per node — zstd's dictionary handles repetition
// well, but shorter keys mean a smaller pre-compression payload and
// modestly cheaper marshal/unmarshal. TenantID + FileKey are NOT carried
// in the blob — they live on the parent figma_section row and would be
// uniform across the entire blob, pure overhead. Callers of
// DecodeSubtreeBlob fill TenantID + FileKey from context.
//
// Compression reuses the package-shared CompressTreeZstd / DecompressTreeZstd
// helpers from canonical_tree.go (migration 0022 zstd primitives).

// figmaNodeBlobEntry is the wire shape for one descendant in the blob.
// Stable JSON tags; do not rename without an explicit migration story.
// `omitempty` is applied to zero-valued optional fields so the typical
// node (no componentId, no parent on roots, no bbox on slot-only nodes)
// serializes compactly.
type figmaNodeBlobEntry struct {
	NodeID       string  `json:"id"`
	ParentID     string  `json:"parent,omitempty"`
	NodeType     string  `json:"type"`
	Name         string  `json:"name"`
	HasBBox      bool    `json:"has_bbox,omitempty"`
	X            float64 `json:"x,omitempty"`
	Y            float64 `json:"y,omitempty"`
	Width        float64 `json:"w,omitempty"`
	Height       float64 `json:"h,omitempty"`
	Depth        int     `json:"depth"`
	OrderIndex   int     `json:"order_index"`
	ComponentID  string  `json:"component_id,omitempty"`
	ComponentKey string  `json:"component_key,omitempty"`
}

// EncodeSubtreeBlob serializes a section's descendant list into a
// zstd-compressed JSON array. Returns nil for an empty/nil input so the
// caller writes SQL NULL — mirrors CompressTreeZstd's empty-string
// convention. The TenantID + FileKey fields on each FigmaNodeRow are
// intentionally not carried in the blob; they live on the parent
// figma_section row.
func EncodeSubtreeBlob(nodes []FigmaNodeRow) ([]byte, error) {
	if len(nodes) == 0 {
		return nil, nil
	}
	entries := make([]figmaNodeBlobEntry, len(nodes))
	for i, n := range nodes {
		entries[i] = figmaNodeBlobEntry{
			NodeID:       n.NodeID,
			ParentID:     n.ParentID,
			NodeType:     n.NodeType,
			Name:         n.Name,
			HasBBox:      n.HasBBox,
			X:            n.X,
			Y:            n.Y,
			Width:        n.Width,
			Height:       n.Height,
			Depth:        n.Depth,
			OrderIndex:   n.OrderIndex,
			ComponentID:  n.ComponentID,
			ComponentKey: n.ComponentKey,
		}
	}
	raw, err := json.Marshal(entries)
	if err != nil {
		return nil, fmt.Errorf("marshal subtree entries: %w", err)
	}
	blob, err := CompressTreeZstd(string(raw))
	if err != nil {
		return nil, fmt.Errorf("compress subtree blob: %w", err)
	}
	return blob, nil
}

// DecodeSubtreeBlob is the inverse of EncodeSubtreeBlob. nil/empty input
// returns an empty slice with no error (a section that hasn't been
// deep-polled yet — the figma_section row exists but the blob column is
// SQL NULL). TenantID + FileKey on the returned rows are left empty;
// callers fill them from the query context that fetched the blob.
func DecodeSubtreeBlob(blob []byte) ([]FigmaNodeRow, error) {
	if len(blob) == 0 {
		return []FigmaNodeRow{}, nil
	}
	raw, err := DecompressTreeZstd(blob)
	if err != nil {
		return nil, fmt.Errorf("decompress subtree blob: %w", err)
	}
	if raw == "" {
		return []FigmaNodeRow{}, nil
	}
	var entries []figmaNodeBlobEntry
	if err := json.Unmarshal([]byte(raw), &entries); err != nil {
		return nil, fmt.Errorf("unmarshal subtree entries: %w", err)
	}
	out := make([]FigmaNodeRow, len(entries))
	for i, e := range entries {
		out[i] = FigmaNodeRow{
			NodeID:       e.NodeID,
			ParentID:     e.ParentID,
			NodeType:     e.NodeType,
			Name:         e.Name,
			HasBBox:      e.HasBBox,
			X:            e.X,
			Y:            e.Y,
			Width:        e.Width,
			Height:       e.Height,
			Depth:        e.Depth,
			OrderIndex:   e.OrderIndex,
			ComponentID:  e.ComponentID,
			ComponentKey: e.ComponentKey,
		}
	}
	return out, nil
}
