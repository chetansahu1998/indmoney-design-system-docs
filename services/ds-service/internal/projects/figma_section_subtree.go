package projects

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
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

// ─── ListSectionFrames (plan 002 U5) ─────────────────────────────────────────

// FrameRow is the U5 output shape — one direct-child frame of a section, with
// the designer's name preserved verbatim. Returned by ListSectionFrames and
// consumed by both U2b (auto-skeleton seeding `prd_state`) and the U6 MCP
// `section.frames` tool.
//
// AbsX/AbsY mirror the absolute canvas coordinates already carried on
// FigmaNodeRow.X / FigmaNodeRow.Y (sourced from Figma's absoluteBoundingBox
// during inventory flattening in internal/figma/inventory/poller.go). They
// are renamed at this boundary to make the "absolute, not section-local"
// contract explicit to MCP-tool consumers.
//
// HasRender is a forward-looking flag for the render pipeline; v1 always
// returns false (the render-state join is a follow-up unit). Callers should
// treat HasRender as advisory, not authoritative.
type FrameRow struct {
	NodeID       string
	Name         string  // designer's name, verbatim — no filtering, no normalization
	ParentNodeID string  // section_id for direct children, or another container if relaxed in future
	Depth        int     // depth from file root (matches FigmaNodeRow.Depth)
	AbsX         float64 // absolute canvas X (from absoluteBoundingBox)
	AbsY         float64 // absolute canvas Y (from absoluteBoundingBox)
	Width        float64
	Height       float64
	HasRender    bool // v1: always false; future: derived from the render pipeline
}

// frameContainerTypes is the closed set of Figma node types treated as
// "frames" for U5/U2b/U6. Mirrors the autolayout-frame literal set used by
// pipeline_cluster_prerender.go:609 — INSTANCE and COMPONENT are included
// because designers regularly drop component instances directly inside a
// section as a state (e.g. a "Cold state" instance). TEXT/VECTOR/RECTANGLE/
// GROUP are intentionally excluded — they are not state-level containers.
var frameContainerTypes = map[string]struct{}{
	"FRAME":     {},
	"INSTANCE":  {},
	"COMPONENT": {},
}

// ListSectionFrames returns the direct-child frames of one figma section.
//
// "Direct child" means depth == section.Depth + 1 within the persisted
// subtree blob (mig 0030, written by the Go autosync poller — NOT the
// Python /tmp scripts that populate figma_node_metadata; that path is
// scaffolding tracked separately, see plan Execution Notes §B.2).
//
// Filter contract:
//   - Node type must be one of {FRAME, INSTANCE, COMPONENT}.
//   - Name is preserved verbatim — `Frame 21234`, `Rectangle 4324`,
//     duplicated `Cold state` names, etc. all flow through. Server does
//     not judge; the caller (U2b auto-skeleton, U6 MCP tool) decides
//     what to do with default-looking names or duplicates.
//   - Two frames with the same Name produce two rows; no dedup.
//
// Order: stable sort by (AbsY ascending, AbsX ascending) — the designer's
// canvas layout reads top-to-bottom, left-to-right.
//
// Empty cases — all return `[]FrameRow{}`, never nil, never error:
//   - figma_section row exists but subtree blob is NULL (not deep-polled).
//   - section row is missing from the subtree (data drift edge case).
//   - section has zero direct-child frames matching the type filter.
//
// Tenant scoping is delegated to LoadSectionSubtree (TenantRepo binding).
// Callable outside autosync hot paths; the read pool is fine.
func (t *TenantRepo) ListSectionFrames(ctx context.Context, fileKey, sectionID string) ([]FrameRow, error) {
	nodes, err := t.LoadSectionSubtree(ctx, fileKey, sectionID)
	if err != nil {
		// ErrNotFound (no row or NULL blob) → empty slice per contract.
		// Mirrors ListFrameChildrenOfSection's normalization.
		if errors.Is(err, ErrNotFound) {
			return []FrameRow{}, nil
		}
		return nil, err
	}

	// Locate the section row in the slice to derive the direct-child
	// depth. The blob carries the section node itself (the autosync
	// poller writes it as the root of the per-section subtree).
	sectionDepth := -1
	for _, n := range nodes {
		if n.NodeID == sectionID {
			sectionDepth = n.Depth
			break
		}
	}
	if sectionDepth < 0 {
		// Section row absent from its own subtree — data drift edge case.
		// Return empty, not error: the caller (MCP tool, auto-skeleton)
		// renders this as "no frames found", which is the right UX.
		return []FrameRow{}, nil
	}
	childDepth := sectionDepth + 1

	out := make([]FrameRow, 0, 16)
	for _, n := range nodes {
		if n.Depth != childDepth {
			continue
		}
		if _, ok := frameContainerTypes[n.NodeType]; !ok {
			continue
		}
		out = append(out, FrameRow{
			NodeID:       n.NodeID,
			Name:         n.Name,
			ParentNodeID: n.ParentID,
			Depth:        n.Depth,
			AbsX:         n.X,
			AbsY:         n.Y,
			Width:        n.Width,
			Height:       n.Height,
			HasRender:    false,
		})
	}

	// Stable sort by canvas Y then X — designer's visual reading order.
	// SliceStable preserves insertion order for ties (same AbsX+AbsY pair),
	// which keeps results deterministic across calls.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].AbsY != out[j].AbsY {
			return out[i].AbsY < out[j].AbsY
		}
		return out[i].AbsX < out[j].AbsX
	})
	return out, nil
}
