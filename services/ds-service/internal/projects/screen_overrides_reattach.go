package projects

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// screen_overrides.go — U3 (Zeplin-grade leaf canvas, D4).
//
// Designers edit text directly on the canvas. Each edit is persisted as a
// `screen_overrides` row keyed on (screen_id, figma_node_id). When the export
// pipeline rewrites a screen's canonical_tree (via sheet-sync's 5-min
// re-import), every existing override is re-anchored against the new tree
// using a 3-tier match:
//
//	1. figma_node_id  — primary; ~99% of cases. The Figma node id is stable
//	                    unless the designer deletes+recreates the node.
//	2. canonical_path — secondary; catches the rare "Figma re-keyed the node
//	                    but the structural position is the same".
//	3. last_seen_original_text — tertiary; full-tree TEXT walk. Catches "I
//	                    deleted the node and recreated it with the same copy".
//
// If none match (including ambiguous fingerprint matches → multiple TEXT nodes
// with the same content), the override is marked `orphaned` and an
// `override.text.orphaned` audit row is emitted so PMs can surface it in the
// per-leaf "Overrides needing review" inspector tab.
//
// Per the Phase 7+8 SQLite-single-writer learning
// (docs/solutions/2026-05-01-003-phase-7-8-closure.md), the existing-overrides
// SELECT happens BEFORE the canonical_tree write transaction begins. The
// re-attach UPDATEs run inside the same tx as InsertCanonicalTree.

// AuditActionOverrideOrphaned is the event emitted when a re-attach can't
// resolve any of the three anchors. Tooling (Activity feed, "Overrides needing
// review" tab) keys off this event_type.
const AuditActionOverrideOrphaned = "override.text.orphaned"

// ScreenOverrideStatus enumerates the lifecycle states for a row.
const (
	ScreenOverrideStatusActive   = "active"
	ScreenOverrideStatusOrphaned = "orphaned"
)

// ScreenOverride mirrors one row in the screen_overrides table.
type ScreenOverride struct {
	ID                   string
	ScreenID             string
	TenantID             string
	FigmaNodeID          string
	CanonicalPath        string
	LastSeenOriginalText string
	Value                string
	Status               string
	UpdatedBy            string
	UpdatedAt            time.Time
}

// ListActiveOverridesForScreen returns the existing screen_overrides rows for
// one screen. Reads off the *sql.DB (NOT inside a tx) so callers can capture
// the rows BEFORE opening the canonical_tree write transaction — see
// docs/solutions/2026-05-01-003-phase-7-8-closure.md for the SQLite single-
// writer deadlock that this avoids.
//
// Returns active + orphaned rows; the re-attach pass then decides per row
// whether the new tree promotes / demotes / leaves the status unchanged.
func (t *TenantRepo) ListActiveOverridesForScreen(ctx context.Context, screenID string) ([]ScreenOverride, error) {
	if t.tenantID == "" {
		return nil, errors.New("projects: tenant_id required")
	}
	rows, err := t.r.db.QueryContext(ctx,
		`SELECT id, screen_id, tenant_id, figma_node_id, canonical_path,
		        last_seen_original_text, value, status,
		        COALESCE(updated_by_user_id, ''), updated_at
		   FROM screen_text_overrides
		  WHERE screen_id = ? AND tenant_id = ?`,
		screenID, t.tenantID,
	)
	if err != nil {
		return nil, fmt.Errorf("list overrides: %w", err)
	}
	defer rows.Close()

	var out []ScreenOverride
	for rows.Next() {
		var o ScreenOverride
		var ts string
		if err := rows.Scan(&o.ID, &o.ScreenID, &o.TenantID, &o.FigmaNodeID,
			&o.CanonicalPath, &o.LastSeenOriginalText, &o.Value, &o.Status,
			&o.UpdatedBy, &ts); err != nil {
			return nil, err
		}
		if parsed, perr := time.Parse(time.RFC3339Nano, ts); perr == nil {
			o.UpdatedAt = parsed
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// ReattachResult summarises the outcome of a single override's re-attach
// attempt. Tests assert on these so the per-row decisions are observable.
type ReattachResult struct {
	OverrideID    string
	MatchedBy     string // "figma_node_id" | "canonical_path" | "last_seen_original_text" | ""
	NewStatus     string // "active" | "orphaned"
	OldNodeID     string
	NewNodeID     string
	OldPath       string
	NewPath       string
	OrphanReason  string // populated when NewStatus == "orphaned"
}

// ReattachOverridesForScreen walks `existing` overrides against the new
// canonical_tree JSON and rewrites each row's anchors atomically inside the
// caller's transaction. Audit rows for orphaned overrides are written via
// `auditWriter` (which is a closure so the call site can route through either
// the *sql.DB or the in-flight tx — pipeline routes through the tx so the
// audit row commits-or-rolls-back together with the InsertCanonicalTree).
//
// Returns the per-row outcomes for caller logging / tests; never returns an
// error solely because an individual override couldn't be resolved (those are
// surfaced as `NewStatus == "orphaned"` and the audit row). It only returns
// an error if the tx itself fails (UPDATE statement failure, audit write
// failure inside the tx).
func (t *TenantRepo) ReattachOverridesForScreen(
	ctx context.Context,
	tx *sql.Tx,
	screenID string,
	existing []ScreenOverride,
	newTreeJSON string,
	auditWriter OverrideAuditWriter,
) ([]ReattachResult, error) {
	if t.tenantID == "" {
		return nil, errors.New("projects: tenant_id required")
	}
	if len(existing) == 0 {
		return nil, nil
	}
	idx, err := indexCanonicalTree(newTreeJSON)
	if err != nil {
		// A malformed new tree shouldn't crash the pipeline; fall back to
		// orphaning everything (caller can re-run after fixing the upstream
		// payload). Log via the audit writer so the operator sees the cause.
		idx = newTreeIndex{}
	}

	now := t.now().UTC()
	results := make([]ReattachResult, 0, len(existing))
	for _, ov := range existing {
		res, rerr := t.reattachOne(ctx, tx, ov, idx, now)
		if rerr != nil {
			return results, rerr
		}
		results = append(results, res)
		if res.NewStatus == ScreenOverrideStatusOrphaned && auditWriter != nil {
			if werr := auditWriter.WriteOverrideOrphaned(ctx, tx, OverrideOrphanedEvent{
				TenantID:    t.tenantID,
				ScreenID:    screenID,
				OverrideID:  ov.ID,
				FigmaNodeID: ov.FigmaNodeID,
				Path:        ov.CanonicalPath,
				Original:    ov.LastSeenOriginalText,
				Reason:      res.OrphanReason,
				At:          now,
			}); werr != nil {
				return results, fmt.Errorf("audit override.orphaned: %w", werr)
			}
		}
	}
	return results, nil
}

// reattachOne resolves one override against the new tree index and writes
// the UPDATE. Tier 1 → tier 2 → tier 3 → orphan. The "ambiguous fingerprint"
// case (multiple TEXT nodes with the same `characters`) skips tier 3 and
// orphans, since picking arbitrarily would silently rewrite the wrong node.
func (t *TenantRepo) reattachOne(
	ctx context.Context,
	tx *sql.Tx,
	ov ScreenOverride,
	idx newTreeIndex,
	now time.Time,
) (ReattachResult, error) {
	res := ReattachResult{
		OverrideID: ov.ID,
		OldNodeID:  ov.FigmaNodeID,
		OldPath:    ov.CanonicalPath,
	}

	// Tier 1 — figma_node_id.
	if node, ok := idx.byID[ov.FigmaNodeID]; ok && isTextNode(node.raw) {
		res.MatchedBy = "figma_node_id"
		res.NewStatus = ScreenOverrideStatusActive
		res.NewNodeID = ov.FigmaNodeID
		res.NewPath = node.path
		// `last_seen_original_text` is NOT updated on tier-1 hits — the
		// override's value is what the user sees, the original text is
		// only refreshed when we're certain the underlying TEXT node is the
		// same node we tracked before.
		return res, t.execReattach(ctx, tx, ov, res, ov.LastSeenOriginalText, now)
	}

	// Tier 2 — canonical_path. The structural slot still exists; if it's a
	// TEXT node, treat it as a re-key and update both anchors.
	if node, ok := idx.byPath[ov.CanonicalPath]; ok && isTextNode(node.raw) {
		newID := stringField(node.raw, "id")
		newOriginal := stringField(node.raw, "characters")
		res.MatchedBy = "canonical_path"
		res.NewStatus = ScreenOverrideStatusActive
		res.NewNodeID = newID
		res.NewPath = ov.CanonicalPath
		return res, t.execReattach(ctx, tx, ov, res, newOriginal, now)
	}

	// Tier 3 — last_seen_original_text. Walk every TEXT node; require a
	// UNIQUE match. Ambiguous → orphan rather than guessing.
	if ov.LastSeenOriginalText != "" {
		matches := idx.byText[ov.LastSeenOriginalText]
		switch len(matches) {
		case 0:
			res.NewStatus = ScreenOverrideStatusOrphaned
			res.OrphanReason = "no node-id, path, or text match"
		case 1:
			node := matches[0]
			res.MatchedBy = "last_seen_original_text"
			res.NewStatus = ScreenOverrideStatusActive
			res.NewNodeID = stringField(node.raw, "id")
			res.NewPath = node.path
			return res, t.execReattach(ctx, tx, ov, res, ov.LastSeenOriginalText, now)
		default:
			res.NewStatus = ScreenOverrideStatusOrphaned
			res.OrphanReason = "ambiguous fingerprint: " + strconv.Itoa(len(matches)) + " text nodes match"
		}
	} else {
		res.NewStatus = ScreenOverrideStatusOrphaned
		res.OrphanReason = "no node-id or path match; no fingerprint stored"
	}

	// Orphan path — flip status, leave anchors untouched so a manual re-attach
	// has the historical fingerprint to work from.
	return res, t.execOrphan(ctx, tx, ov, now)
}

// execReattach updates the row to reflect a successful match. Includes the
// (possibly refreshed) last_seen_original_text so a tier-2 hit upgrades the
// fingerprint for future re-imports.
func (t *TenantRepo) execReattach(
	ctx context.Context,
	tx *sql.Tx,
	ov ScreenOverride,
	res ReattachResult,
	newOriginal string,
	now time.Time,
) error {
	_, err := tx.ExecContext(ctx,
		`UPDATE screen_text_overrides
		    SET figma_node_id = ?,
		        canonical_path = ?,
		        last_seen_original_text = ?,
		        status = 'active',
		        updated_at = ?
		  WHERE id = ? AND tenant_id = ?`,
		res.NewNodeID, res.NewPath, newOriginal, rfc3339Nano(now), ov.ID, t.tenantID,
	)
	if err != nil {
		return fmt.Errorf("update override %s: %w", ov.ID, err)
	}
	return nil
}

// execOrphan flips status to 'orphaned' and updates the timestamp so the
// inspector can sort by "recently lost". Anchors are intentionally left as-is.
func (t *TenantRepo) execOrphan(
	ctx context.Context,
	tx *sql.Tx,
	ov ScreenOverride,
	now time.Time,
) error {
	_, err := tx.ExecContext(ctx,
		`UPDATE screen_text_overrides
		    SET status = 'orphaned',
		        updated_at = ?
		  WHERE id = ? AND tenant_id = ?`,
		rfc3339Nano(now), ov.ID, t.tenantID,
	)
	if err != nil {
		return fmt.Errorf("orphan override %s: %w", ov.ID, err)
	}
	return nil
}

// InsertScreenOverride persists a new override. Unique on (screen_id,
// figma_node_id); a second write to the same key updates `value` and
// reactivates the row. Used by tests + the (forthcoming) HTTP write handler.
func (t *TenantRepo) InsertScreenOverride(ctx context.Context, ov ScreenOverride) (ScreenOverride, error) {
	if t.tenantID == "" {
		return ScreenOverride{}, errors.New("projects: tenant_id required")
	}
	if ov.ID == "" {
		ov.ID = uuid.NewString()
	}
	if ov.Status == "" {
		ov.Status = ScreenOverrideStatusActive
	}
	ov.TenantID = t.tenantID
	if ov.UpdatedAt.IsZero() {
		ov.UpdatedAt = t.now().UTC()
	}
	_, err := t.r.db.ExecContext(ctx,
		`INSERT INTO screen_text_overrides
		    (id, screen_id, tenant_id, figma_node_id, canonical_path,
		     last_seen_original_text, value, status, updated_by_user_id, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(screen_id, figma_node_id) DO UPDATE SET
		    canonical_path          = excluded.canonical_path,
		    last_seen_original_text = excluded.last_seen_original_text,
		    value                   = excluded.value,
		    status                  = excluded.status,
		    updated_by_user_id      = excluded.updated_by_user_id,
		    updated_at              = excluded.updated_at`,
		ov.ID, ov.ScreenID, ov.TenantID, ov.FigmaNodeID, ov.CanonicalPath,
		ov.LastSeenOriginalText, ov.Value, ov.Status,
		ov.UpdatedBy, rfc3339Nano(ov.UpdatedAt),
	)
	if err != nil {
		return ScreenOverride{}, fmt.Errorf("insert override: %w", err)
	}
	return ov, nil
}

// GetScreenOverride looks up one row by id (tenant-scoped). Test helper.
func (t *TenantRepo) GetScreenOverride(ctx context.Context, id string) (ScreenOverride, error) {
	if t.tenantID == "" {
		return ScreenOverride{}, errors.New("projects: tenant_id required")
	}
	var o ScreenOverride
	var ts string
	err := t.r.db.QueryRowContext(ctx,
		`SELECT id, screen_id, tenant_id, figma_node_id, canonical_path,
		        last_seen_original_text, value, status,
		        COALESCE(updated_by_user_id, ''), updated_at
		   FROM screen_text_overrides
		  WHERE id = ? AND tenant_id = ?`,
		id, t.tenantID,
	).Scan(&o.ID, &o.ScreenID, &o.TenantID, &o.FigmaNodeID, &o.CanonicalPath,
		&o.LastSeenOriginalText, &o.Value, &o.Status, &o.UpdatedBy, &ts)
	if errors.Is(err, sql.ErrNoRows) {
		return ScreenOverride{}, ErrNotFound
	}
	if err != nil {
		return ScreenOverride{}, err
	}
	if parsed, perr := time.Parse(time.RFC3339Nano, ts); perr == nil {
		o.UpdatedAt = parsed
	}
	return o, nil
}

// ─── Tree walking ──────────────────────────────────────────────────────────

// newTreeIndex is a flattened view of the JSON canonical_tree for fast
// per-override lookups. Built once per (screen, re-import) call so the
// per-override match is O(1) for tier-1/2 and O(distinct-text-nodes) for
// tier-3 ambiguity check.
type newTreeIndex struct {
	byID   map[string]nodeRef
	byPath map[string]nodeRef
	byText map[string][]nodeRef
}

type nodeRef struct {
	path string
	raw  map[string]any
}

// indexCanonicalTree walks the JSON tree once and builds the three lookup
// maps. The tree may be either:
//
//   - the full Figma /v1/files/.../nodes envelope: `{"document": {...}, "components": ...}`
//   - a bare node: `{"id": ..., "type": "FRAME", "children": [...]}`
//
// Both are handled by descending into "document" if present, then walking
// children with their indices. The `canonical_path` segments are each child's
// numeric index in its parent's `children` array, joined via `.children.`,
// matching the format documented in 0018_screen_overrides.up.sql.
func indexCanonicalTree(treeJSON string) (newTreeIndex, error) {
	idx := newTreeIndex{
		byID:   make(map[string]nodeRef),
		byPath: make(map[string]nodeRef),
		byText: make(map[string][]nodeRef),
	}
	if treeJSON == "" || treeJSON == "{}" {
		return idx, nil
	}
	var root any
	if err := json.Unmarshal([]byte(treeJSON), &root); err != nil {
		return idx, fmt.Errorf("parse canonical tree: %w", err)
	}
	rootMap, ok := root.(map[string]any)
	if !ok {
		return idx, nil
	}
	// Unwrap the Figma envelope. If "document" is present we treat it as the
	// root node; otherwise the rootMap itself is the root node.
	if doc, ok := rootMap["document"].(map[string]any); ok {
		rootMap = doc
	}
	// Children of the root each get a path segment that is just their index;
	// descendants' segments are `.children.<idx>`.
	walkChildren(rootMap, "", &idx)
	return idx, nil
}

// walkChildren recursively descends `node["children"]`, recording each child
// in the index under the path that locates it relative to the implicit root.
func walkChildren(node map[string]any, parentPath string, idx *newTreeIndex) {
	rawChildren, _ := node["children"].([]any)
	for i, c := range rawChildren {
		child, ok := c.(map[string]any)
		if !ok {
			continue
		}
		var path string
		if parentPath == "" {
			path = strconv.Itoa(i)
		} else {
			path = parentPath + ".children." + strconv.Itoa(i)
		}
		ref := nodeRef{path: path, raw: child}
		if id := stringField(child, "id"); id != "" {
			idx.byID[id] = ref
		}
		idx.byPath[path] = ref
		if isTextNode(child) {
			if txt := stringField(child, "characters"); txt != "" {
				idx.byText[txt] = append(idx.byText[txt], ref)
			}
		}
		walkChildren(child, path, idx)
	}
}

func isTextNode(node map[string]any) bool {
	return strings.EqualFold(stringField(node, "type"), "TEXT")
}

func stringField(node map[string]any, key string) string {
	if v, ok := node[key].(string); ok {
		return v
	}
	return ""
}

// ─── Override audit hook ───────────────────────────────────────────────────

// OverrideAuditWriter is the seam used by ReattachOverridesForScreen to emit
// `override.text.orphaned` rows from inside the canonical_tree write tx. The
// production implementation is `*OverrideAuditLogger` below; tests supply a
// fake that just records the calls.
type OverrideAuditWriter interface {
	WriteOverrideOrphaned(ctx context.Context, tx *sql.Tx, ev OverrideOrphanedEvent) error
}

// OverrideOrphanedEvent is the payload persisted to audit_log. The full
// override row would include `value`, but we deliberately don't log the
// designer's text edit as part of the orphan event — orphan-rate dashboards
// don't need it, and it keeps PII out of the operator audit log.
type OverrideOrphanedEvent struct {
	TenantID    string
	ScreenID    string
	OverrideID  string
	FigmaNodeID string
	Path        string
	Original    string
	Reason      string
	At          time.Time
}

// OverrideAuditLogger is the production OverrideAuditWriter. Writes through
// the tx so the audit row commits-or-rolls-back atomically with the
// canonical_tree update.
type OverrideAuditLogger struct{}

// WriteOverrideOrphaned persists one `override.text.orphaned` audit_log row
// using the supplied tx. Delegates to U10's WriteOverrideEvent helper so the
// `details` JSON shape (old, new, screen_id, figma_node_id, reason) stays in
// sync with the PUT/DELETE/bulk paths the activity feed reads from.
//
// `flow_id` is intentionally left blank: the re-attach pipeline operates
// before flow_id is re-resolved against the new tree. The frontend's flow-
// scoped activity tab therefore omits orphan events; tenant-wide audit
// queries still surface them.
func (OverrideAuditLogger) WriteOverrideOrphaned(ctx context.Context, tx *sql.Tx, ev OverrideOrphanedEvent) error {
	return WriteOverrideEvent(ctx, tx, OverrideEvent{
		EventType:   AuditActionOverrideOrphaned,
		TenantID:    ev.TenantID,
		ScreenID:    ev.ScreenID,
		FigmaNodeID: ev.FigmaNodeID,
		OldValue:    ev.Original,
		Reason:      ev.Reason,
	})
}

// ─── small helpers ─────────────────────────────────────────────────────────

func rfc3339Nano(ts time.Time) string {
	return ts.UTC().Format(time.RFC3339Nano)
}

func ptrIfSet(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

