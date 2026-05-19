// node_metadata_extractor — U5 of plan 2026-05-17-004 (PM follow-ups).
//
// Populates `figma_node_metadata` (mig 0034) with depth=1 direct-child
// frames for every section in a file. Replaces the manual
// `/tmp/run_step2_*.py` scripts: a fresh DB now backfills via the running
// Go server's autosync cycle, not by a human running Python on the side.
//
// Depth=1 only. The full subtree is already captured in
// `figma_section.subtree_json_zstd` (mig 0030); this stage exists to
// populate the row-shaped table that ListSectionFrames (PRD wall) and
// auto-skeleton (U2b of origin plan) consume.
//
// Idempotent: re-running on an unchanged file updates last_seen_at but
// leaves first_seen_at intact (UPSERT on PK).
//
// Best-effort: a single section's failure logs a warning and continues.
// File-level errors propagate so the poller's cycle stats reflect them.
//
// Rate-limiter aware: every Figma call routes through client.Client.get,
// which already respects the per-PAT tier-1 bucket shared with the rest
// of the inventory crawl.
package inventory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/figma/client"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/projects"
)

// nodeMetadataBatchSize bounds how many section IDs go into one
// /v1/files/<key>/nodes?ids=<csv> request. The Figma API tolerates fairly
// large URLs but rejects past ~8 KB; at ~13 chars per id + comma a batch
// of 50 stays comfortably under 1 KB and lets a 200-section file finish
// in four calls. 50 also matches the cap the original Python pipeline
// used during its later tuning passes.
const nodeMetadataBatchSize = 50

// nodeMetadataFrameTypes is the closed set of direct-child node types we
// persist. TEXT / VECTOR / RECTANGLE / GROUP and friends are filtered out
// — they are not state-level containers and PMs / the wall ignore them.
//
// COMPONENT_SET is intentionally NOT included: at the section level it's
// vanishingly rare (component-set frames typically live under PAGE, not
// SECTION), and the wall only renders FRAME/INSTANCE/COMPONENT.
var nodeMetadataFrameTypes = map[string]struct{}{
	"FRAME":     {},
	"INSTANCE":  {},
	"COMPONENT": {},
}

// FigmaNodesFetcher is the slice of *client.Client the extractor needs.
// Defining the interface here keeps tests honest — a fake can return any
// shape without spinning up an HTTP server, and the real implementation
// is the existing GetFileNodes method on client.Client.
type FigmaNodesFetcher interface {
	// GetFileNodes fetches /v1/files/<key>/nodes?ids=<csv>&depth=<n>.
	// Returns the raw decoded response (a map keyed at the top level by
	// "nodes" with section_id → {document: {...}} entries). nil + nil
	// would be a bug; the implementation MUST return an error on empty
	// id slices.
	GetFileNodes(ctx context.Context, fileKey string, nodeIDs []string, depth int) (map[string]any, error)
}

// NodeMetadataRepoFactory returns a TenantRepo for the given tenant_id.
// Wired by cmd/server/main.go to projects.NewTenantRepoFromPool so the
// extractor inherits the split read/write pools. Tests pass a closure
// over a plain *sql.DB via projects.NewTenantRepo.
//
// Per-tenant scoping happens once per ExtractForFile call — the
// extractor itself is tenant-agnostic so one process-wide instance can
// serve every tenant the poller crawls.
type NodeMetadataRepoFactory func(tenantID string) *projects.TenantRepo

// PATResolverFunc is the same signature already used by the poller (see
// PATResolver in poller.go). Duplicated here as a named type so the
// extractor's signature stays explicit at the call site; cmd/server/main
// passes the same closure to both.
type PATResolverFunc func(ctx context.Context, tenantID string) (string, error)

// NewFigmaNodesFetcherFunc lets cmd/server/main inject a real
// *client.Client per tenant (one client = one PAT = one rate-limit
// bucket). Tests pass a static FigmaNodesFetcher and ignore tenantID.
type NewFigmaNodesFetcherFunc func(pat string) FigmaNodesFetcher

// NodeMetadataExtractor is the per-file depth=1 writer. Wire one
// instance per process via cmd/server/main.go and pass it through
// inventory.Config.NodeMetadataExtractor.
type NodeMetadataExtractor struct {
	// ResolvePAT decrypts the per-tenant Figma PAT. Reuses the same
	// closure cmd/server/main.go builds for the rest of the inventory
	// poller — when empty, the extractor logs and skips (no point
	// fetching for a tenant that has no token).
	ResolvePAT PATResolverFunc

	// NewClient builds a FigmaNodesFetcher from a PAT. Lets tests
	// substitute a fake; production wires a closure that calls
	// client.New(pat). When nil the extractor falls back to
	// client.New (production default).
	NewClient NewFigmaNodesFetcherFunc

	// Repo returns a TenantRepo for the given tenant_id. Built per
	// ExtractForFile call so each tenant gets its own scoped repo. Tests
	// pass a closure over the test DB.
	Repo NodeMetadataRepoFactory

	// Log is used for per-section warnings + cycle summaries. Falls
	// back to slog.Default() when nil.
	Log *slog.Logger
}

// ExtractForFile fetches depth=1 metadata for every section in
// sectionIDs (batched), flattens the direct children, and upserts
// figma_node_metadata rows. Returns the number of rows written + the
// first non-nil error (subsequent errors are logged and surfaced via
// the count delta).
//
// Empty sectionIDs is a no-op (no Figma calls, no DB writes).
//
// Best-effort discipline: a single section returning a 4xx / malformed
// response logs a warning and continues; the call only fails on the
// PAT-resolve / repo-construction / DB transaction paths where there's
// nothing the next section's success could rescue.
//
// pageIDBySection maps each requested section_id to its parent
// page_id. Required because the mig 0034 schema stores page_id alongside
// section_id and the /v1/files/<key>/nodes response doesn't carry the
// page id of a section's grandparent canvas. The poller already has this
// mapping in hand (it just built the section rows); passing it through
// avoids a second DB round-trip.
func (e *NodeMetadataExtractor) ExtractForFile(
	ctx context.Context,
	tenantID, fileKey string,
	sectionIDs []string,
	pageIDBySection map[string]string,
) (int, error) {
	if tenantID == "" {
		return 0, errors.New("node_metadata: tenant_id required")
	}
	if fileKey == "" {
		return 0, errors.New("node_metadata: file_key required")
	}
	logger := e.logger().With("tenant", tenantID, "file_key", fileKey)
	if len(sectionIDs) == 0 {
		logger.Debug("node_metadata: no sections to extract")
		return 0, nil
	}
	if e.ResolvePAT == nil {
		return 0, errors.New("node_metadata: ResolvePAT required")
	}
	if e.Repo == nil {
		return 0, errors.New("node_metadata: Repo required")
	}

	pat, err := e.ResolvePAT(ctx, tenantID)
	if err != nil {
		return 0, fmt.Errorf("node_metadata: resolve pat: %w", err)
	}
	if pat == "" {
		logger.Debug("node_metadata: tenant has no figma pat, skipping")
		return 0, nil
	}

	fc := e.newClient(pat)
	repo := e.Repo(tenantID)
	if repo == nil {
		return 0, errors.New("node_metadata: repo factory returned nil")
	}

	totalRows := 0
	var firstErr error
	for start := 0; start < len(sectionIDs); start += nodeMetadataBatchSize {
		select {
		case <-ctx.Done():
			return totalRows, ctx.Err()
		default:
		}
		end := start + nodeMetadataBatchSize
		if end > len(sectionIDs) {
			end = len(sectionIDs)
		}
		batch := sectionIDs[start:end]
		// depth=1 → returns the section node itself + one level of
		// children. The children array is what we flatten.
		resp, err := fc.GetFileNodes(ctx, fileKey, batch, 1)
		if err != nil {
			logger.Warn("node_metadata: get_file_nodes failed",
				"batch_start", start, "batch_size", len(batch), "err", err.Error())
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		rows := buildNodeMetadataRows(resp, batch, fileKey, pageIDBySection)
		if len(rows) == 0 {
			continue
		}
		written, werr := repo.UpsertFigmaNodeMetadata(ctx, fileKey, rows)
		if werr != nil {
			logger.Warn("node_metadata: upsert failed",
				"batch_start", start, "rows", len(rows), "err", werr.Error())
			if firstErr == nil {
				firstErr = werr
			}
			continue
		}
		totalRows += written

		// Mig 0035 — compute per-section change-detection hash from the
		// rows we just wrote and stamp it on figma_section.node_metadata_hash.
		// The planner's diff branch consults this; a stale hash means
		// frame metadata changed (rename, type change, position shift)
		// → planner schedules a full_export on the next cycle.
		bySection := map[string][]projects.FigmaNodeMetadataRow{}
		for _, r := range rows {
			bySection[r.SectionID] = append(bySection[r.SectionID], r)
		}
		for sectionID, sectionRows := range bySection {
			hash := computeSectionNodeMetadataHash(sectionRows)
			if uerr := repo.UpdateFigmaSectionNodeMetadataHash(ctx, fileKey, sectionID, hash); uerr != nil {
				logger.Warn("node_metadata: section hash update failed",
					"section_id", sectionID, "err", uerr.Error())
				if firstErr == nil {
					firstErr = uerr
				}
			}
		}
	}
	logger.Info("node_metadata: extraction done",
		"sections", len(sectionIDs), "rows_written", totalRows,
		"errored", firstErr != nil)
	return totalRows, firstErr
}

// computeSectionNodeMetadataHash produces a stable SHA-256 over a section's
// direct-child rows. The hash changes if any of these change:
//   - a child's name (this is what catches frame renames)
//   - a child's type (FRAME ↔ INSTANCE ↔ COMPONENT promotion)
//   - a child's parent_id (re-parented)
//   - a child's bbox (position shift, resize)
//
// The hash does NOT include layout_mode or component_id because those
// signals are noisy across pipeline reads and the goal is "did the
// designer change something the autosync pipeline cares about?".
//
// Rows are sorted by node_id before hashing so a different fetch order
// (e.g. Figma reorders children in a re-fetch) doesn't move the hash.
//
// Empty input returns the empty string — caller treats that as "no
// metadata yet, force full_export".
func computeSectionNodeMetadataHash(rows []projects.FigmaNodeMetadataRow) string {
	if len(rows) == 0 {
		return ""
	}
	sorted := make([]projects.FigmaNodeMetadataRow, len(rows))
	copy(sorted, rows)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].NodeID < sorted[j].NodeID })
	h := sha256.New()
	for _, r := range sorted {
		// Each field separated by 0x1F (Unit Separator) so values can't
		// run together ambiguously (e.g. name="foo" + type="BAR" vs
		// name="fooBAR" + type="").
		h.Write([]byte(r.NodeID))
		h.Write([]byte{0x1F})
		h.Write([]byte(r.Name))
		h.Write([]byte{0x1F})
		h.Write([]byte(r.NodeType))
		h.Write([]byte{0x1F})
		h.Write([]byte(r.ParentID))
		h.Write([]byte{0x1F})
		h.Write([]byte(strconv.FormatFloat(r.AbsX, 'f', 2, 64)))
		h.Write([]byte{0x1F})
		h.Write([]byte(strconv.FormatFloat(r.AbsY, 'f', 2, 64)))
		h.Write([]byte{0x1F})
		h.Write([]byte(strconv.FormatFloat(r.Width, 'f', 2, 64)))
		h.Write([]byte{0x1F})
		h.Write([]byte(strconv.FormatFloat(r.Height, 'f', 2, 64)))
		h.Write([]byte{0x1E}) // record separator
	}
	return hex.EncodeToString(h.Sum(nil))
}

// buildNodeMetadataRows walks a /v1/files/<key>/nodes response and emits
// one FigmaNodeMetadataRow per direct-child FRAME/INSTANCE/COMPONENT of
// each requested section. Skips non-container types and sections that
// the response omitted (Figma silently drops unresolvable ids).
//
// Pure function — no IO, no logging. Tested in isolation.
func buildNodeMetadataRows(
	resp map[string]any,
	requestedSectionIDs []string,
	fileKey string,
	pageIDBySection map[string]string,
) []projects.FigmaNodeMetadataRow {
	if resp == nil {
		return nil
	}
	nodesMap, _ := resp["nodes"].(map[string]any)
	if nodesMap == nil {
		return nil
	}
	out := make([]projects.FigmaNodeMetadataRow, 0, len(requestedSectionIDs)*4)
	for _, sectionID := range requestedSectionIDs {
		entry, ok := nodesMap[sectionID].(map[string]any)
		if !ok || entry == nil {
			continue
		}
		doc, _ := entry["document"].(map[string]any)
		if doc == nil {
			continue
		}
		children, _ := doc["children"].([]any)
		pageID := pageIDBySection[sectionID]
		for i, raw := range children {
			child, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			nodeType, _ := child["type"].(string)
			if _, keep := nodeMetadataFrameTypes[nodeType]; !keep {
				continue
			}
			nodeID, _ := child["id"].(string)
			if nodeID == "" {
				continue
			}
			name, _ := child["name"].(string)

			row := projects.FigmaNodeMetadataRow{
				FileKey:    fileKey,
				PageID:     pageID,
				SectionID:  sectionID,
				NodeID:     nodeID,
				ParentID:   sectionID, // depth=1 → parent IS the section
				Depth:      1,
				OrderIndex: i,
				NodeType:   nodeType,
				Name:       name,
			}

			// absoluteBoundingBox → abs_x / abs_y / width / height.
			if bb, ok := child["absoluteBoundingBox"].(map[string]any); ok && bb != nil {
				row.HasBBox = true
				row.AbsX = floatFromAny(bb["x"])
				row.AbsY = floatFromAny(bb["y"])
				row.Width = floatFromAny(bb["width"])
				row.Height = floatFromAny(bb["height"])
			}

			// layoutMode is autolayout signal on FRAME/INSTANCE/COMPONENT.
			// The Figma API returns "" or "NONE" / "HORIZONTAL" /
			// "VERTICAL" / "GRID". Anything else → repo layer drops to NULL.
			if lm, ok := child["layoutMode"].(string); ok {
				row.LayoutMode = lm
			}

			// component_id: INSTANCE points at the master component;
			// COMPONENT/COMPONENT_SET expose their own id as the component
			// reference. Mirrors /tmp/run_step2_frames.py's logic exactly.
			switch nodeType {
			case "INSTANCE":
				if cid, ok := child["componentId"].(string); ok {
					row.ComponentID = cid
				}
			case "COMPONENT", "COMPONENT_SET":
				row.ComponentID = nodeID
			}
			// component_key isn't on the file-tree response (only on
			// /v1/files/<key>/components); the Python pipeline left it
			// NULL too. Leave blank.

			out = append(out, row)
		}
	}
	return out
}

// floatFromAny tolerates the JSON-decoded shape of a number (float64
// from encoding/json) plus the rare int return path some fakes use.
// Returns 0 on anything else — matches the Python pipeline's
// `float(bb.get('x', 0))` semantics.
func floatFromAny(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int64:
		return float64(n)
	}
	return 0
}

func (e *NodeMetadataExtractor) logger() *slog.Logger {
	if e.Log != nil {
		return e.Log
	}
	return slog.Default()
}

func (e *NodeMetadataExtractor) newClient(pat string) FigmaNodesFetcher {
	if e.NewClient != nil {
		return e.NewClient(pat)
	}
	return client.New(pat)
}
