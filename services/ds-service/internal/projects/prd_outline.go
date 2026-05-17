package projects

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// prd_outline.go — coverage-wall load path (U6b of plan 002).
//
// LoadSectionOutline assembles the corkboard view for one sub_flow:
// every direct-child Figma frame in the bound section joined to its
// PRD state (matched on frame_name, case-insensitive + trim), plus
// per-stem count rollups and the latest prd_audit row for the state's
// `last_touched_by` / `last_touched_at` columns.
//
// Shape mirrors LoadPRD (prd.go): we run several read-only queries
// against the read pool and assemble the result in Go using stable
// pointer-holders, exactly as LoadPRD does for stems. Single-query
// joins were considered and rejected — the join shape (frames blob ⊕
// SQL state rows ⊕ five stem-count rollups ⊕ audit MAX) is heterogenous,
// and SQLite's planner doesn't optimize across heterogenous storage.
//
// Binding semantics (matches the spec in U6b of the plan):
//
//   bound     — frame exists in the section AND a live prd_state row
//               (deleted_at IS NULL) shares its case-insensitive +
//               trimmed frame_name.
//   untagged  — frame exists in the section AND no live prd_state row
//               matches its frame_name.
//   orphaned  — prd_state exists with deleted_at IS NOT NULL, OR
//               with a frame_name that does not match any current
//               section frame. Both are reported as orphan rows with
//               figma_node_id == "" so the viewer can render an
//               "orphan / removed from Figma" badge.
//
// Total word count is computed cheaply via strings.Fields — accuracy
// inside ±1 word per state is fine; the wall renders it as a "thinness"
// hint, not a billing input.

// WallRow is one row of the coverage-wall response — either a live
// frame in the section (possibly with a bound PRD state) or an orphaned
// PRD state whose frame is gone (figma_node_id == "").
//
// Field order + JSON tags MUST stay stable: the React Wall component
// (U9) parses this shape verbatim.
type WallRow struct {
	FigmaNodeID    string  `json:"figma_node_id"`
	FrameName      string  `json:"frame_name"`
	BindingStatus  string  `json:"binding_status"` // "bound" | "untagged" | "orphaned"
	PRDStateID     *string `json:"prd_state_id,omitempty"`
	PRDStateLabel  *string `json:"prd_state_label,omitempty"`
	CriteriaCount  int     `json:"criteria_count"`
	EventsCount    int     `json:"events_count"`
	CopyCount      int     `json:"copy_count"`
	EdgeCasesCount int     `json:"edge_cases_count"`
	A11yCount      int     `json:"a11y_count"`
	TotalWordCount int     `json:"total_word_count"`
	LastTouchedBy  *string `json:"last_touched_by,omitempty"`
	LastTouchedAt  *string `json:"last_touched_at,omitempty"`
	HasRender      bool    `json:"has_render"`
}

// WallCounts is the small aggregate the viewer renders as a header
// strip ("8 frames • 5 bound • 3 untagged • 62% covered").
type WallCounts struct {
	Total           int `json:"total"`
	Bound           int `json:"bound"`
	Untagged        int `json:"untagged"`
	Orphaned        int `json:"orphaned"`
	CoveragePercent int `json:"coverage_percent"`
}

// WallResult is the load-path return shape. JSON keys MUST stay stable —
// downstream consumers (U9 Wall component, MCP tool, future inbox
// rollups) all read it.
type WallResult struct {
	Frames []WallRow  `json:"frames"`
	Counts WallCounts `json:"counts"`
}

// Binding-status sentinels — exported so the MCP tool + tests refer to
// the same strings.
const (
	BindingStatusBound    = "bound"
	BindingStatusUntagged = "untagged"
	BindingStatusOrphaned = "orphaned"
)

// LoadSectionOutline returns the coverage wall for one sub_flow.
//
// Flow:
//  1. Resolve sub_flow → figma_section_id + file_key (handles the
//     "no section bound yet" case by returning an empty wall, not
//     ErrNotFound).
//  2. Pull the frame list via ListSectionFrames (canonical Go path;
//     reads the mig-0030 zstd blob, not the Python-populated
//     figma_node_metadata table).
//  3. Pull every prd_state for the sub_flow (including soft-deleted
//     rows — the wall surfaces them as orphans).
//  4. Match frames ↔ states by normalized frame_name.
//  5. Roll up per-stem counts (criteria / events / copy / edge / a11y)
//     and total word count.
//  6. Attach the latest prd_audit row per state.
//
// Tenant scoping is delegated to the TenantRepo binding on every read.
// All reads use the read pool; no writes.
//
// Empty section returns WallResult{Frames: []WallRow{}, Counts: {}}.
// Sub_flow without a bound section returns the same empty shape.
func (t *TenantRepo) LoadSectionOutline(ctx context.Context, subFlow SubFlow) (WallResult, error) {
	if t.tenantID == "" {
		return WallResult{}, errors.New("prd_outline: tenant_id required")
	}

	out := WallResult{Frames: []WallRow{}}

	// Step 1: load frame rows for the section (if bound). We tolerate
	// missing section binding — the wall is still useful pre-binding
	// (it just shows orphans, if any prd_states exist).
	var frames []FrameRow
	if subFlow.FigmaSectionID != nil && *subFlow.FigmaSectionID != "" {
		fileKey, err := t.LookupFigmaSectionFileKey(ctx, *subFlow.FigmaSectionID)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return WallResult{}, fmt.Errorf("prd_outline: lookup file_key: %w", err)
		}
		if err == nil {
			fs, ferr := t.ListSectionFrames(ctx, fileKey, *subFlow.FigmaSectionID)
			if ferr != nil {
				return WallResult{}, fmt.Errorf("prd_outline: list frames: %w", ferr)
			}
			frames = fs
		}
	}

	// Step 2: load every PRD state hung off this sub_flow (including
	// soft-deleted rows). LoadPRD skips soft-deleted rows, so we go
	// direct.
	states, err := t.loadAllPRDStatesForSubFlow(ctx, subFlow.ID)
	if err != nil {
		return WallResult{}, fmt.Errorf("prd_outline: load states: %w", err)
	}

	// Index live states by normalized frame_name for the match step.
	// Soft-deleted states are NEVER eligible for the live match — they
	// always surface as orphans, even when a live frame with the same
	// name exists (the deleted_at row is the orphan; the live frame
	// will be matched to a separate live row if one exists).
	stateByNormFrame := make(map[string]*PRDState, len(states))
	for i := range states {
		st := &states[i]
		if st.DeletedAt != nil {
			continue
		}
		if st.FrameName == nil || strings.TrimSpace(*st.FrameName) == "" {
			continue
		}
		stateByNormFrame[normalizeName(*st.FrameName)] = st
	}

	// Track which state ids end up matched so we can compute the
	// orphan set (every prd_state not consumed by a live frame match).
	matched := make(map[string]struct{}, len(states))

	// Step 3a: emit one WallRow per frame (canvas-y order is already
	// guaranteed by ListSectionFrames). We resolve binding here.
	for _, f := range frames {
		row := WallRow{
			FigmaNodeID:   f.NodeID,
			FrameName:     f.Name,
			BindingStatus: BindingStatusUntagged,
			HasRender:     f.HasRender,
		}
		if st, ok := stateByNormFrame[normalizeName(f.Name)]; ok {
			row.BindingStatus = BindingStatusBound
			id := st.ID
			label := st.Label
			row.PRDStateID = &id
			row.PRDStateLabel = &label
			matched[st.ID] = struct{}{}
		}
		out.Frames = append(out.Frames, row)
	}

	// Step 3b: emit orphan rows for every state that did not get
	// matched. Soft-deleted states are always orphans; live states
	// with a frame_name that no longer exists in the section are also
	// orphans. Pure live states with no frame_name (PM authored the
	// state before any frame existed) are also orphans — the wall
	// surfaces them so the PM can either rename or detach.
	for i := range states {
		st := &states[i]
		if _, ok := matched[st.ID]; ok {
			continue
		}
		id := st.ID
		label := st.Label
		frameName := ""
		if st.FrameName != nil {
			frameName = *st.FrameName
		}
		out.Frames = append(out.Frames, WallRow{
			FigmaNodeID:   "",
			FrameName:     frameName,
			BindingStatus: BindingStatusOrphaned,
			PRDStateID:    &id,
			PRDStateLabel: &label,
		})
	}

	// Step 4: stem-count rollups for every bound + orphan state that
	// is not soft-deleted (soft-deleted rows already report zeros, but
	// we still want their counts when the viewer wants to know how
	// much content would be lost on hard-delete).
	stateIDs := make([]string, 0, len(states))
	for i := range states {
		stateIDs = append(stateIDs, states[i].ID)
	}
	counts, err := t.loadOutlineStemCounts(ctx, stateIDs)
	if err != nil {
		return WallResult{}, fmt.Errorf("prd_outline: stem counts: %w", err)
	}
	wordCounts, err := t.loadOutlineWordCounts(ctx, stateIDs, states)
	if err != nil {
		return WallResult{}, fmt.Errorf("prd_outline: word counts: %w", err)
	}
	audits, err := t.LatestPRDAuditByState(ctx, stateIDs)
	if err != nil {
		return WallResult{}, fmt.Errorf("prd_outline: audits: %w", err)
	}

	// Step 5: fold the counts + audits into every row that carries a
	// PRDStateID.
	for i := range out.Frames {
		r := &out.Frames[i]
		if r.PRDStateID == nil {
			continue
		}
		sid := *r.PRDStateID
		if c, ok := counts[sid]; ok {
			r.CriteriaCount = c.Criteria
			r.EventsCount = c.Events
			r.CopyCount = c.Copy
			r.EdgeCasesCount = c.EdgeCases
			r.A11yCount = c.A11y
		}
		if w, ok := wordCounts[sid]; ok {
			r.TotalWordCount = w
		}
		if a, ok := audits[sid]; ok {
			user := a.UserID
			r.LastTouchedBy = &user
			ts := rfc3339(a.At)
			r.LastTouchedAt = &ts
		}
	}

	// Step 6: counts header.
	out.Counts.Total = len(out.Frames)
	for _, r := range out.Frames {
		switch r.BindingStatus {
		case BindingStatusBound:
			out.Counts.Bound++
		case BindingStatusUntagged:
			out.Counts.Untagged++
		case BindingStatusOrphaned:
			out.Counts.Orphaned++
		}
	}
	if out.Counts.Total > 0 {
		// Standard half-up rounding: coverage = round(bound / total * 100).
		// Empty section already short-circuited above.
		out.Counts.CoveragePercent = ((out.Counts.Bound * 200) / out.Counts.Total + 1) / 2
	}

	return out, nil
}

// ─── Internal helpers ──────────────────────────────────────────────────────

// stemCountRow bundles per-state typed-stem counts for the wall load.
type stemCountRow struct {
	Criteria  int
	Events    int
	Copy      int
	EdgeCases int
	A11y      int
}

// loadAllPRDStatesForSubFlow returns every prd_state hung off the
// sub_flow's PRD — including soft-deleted rows (deleted_at IS NOT NULL).
// Returns an empty slice when no PRD row exists.
func (t *TenantRepo) loadAllPRDStatesForSubFlow(ctx context.Context, subFlowID string) ([]PRDState, error) {
	subFlowID = strings.TrimSpace(subFlowID)
	if subFlowID == "" {
		return nil, nil
	}
	rows, err := t.readHandle().QueryContext(ctx,
		`SELECT s.id, s.tenant_id, s.prd_tab_id, s.label, s.position, s.frame_name,
		        s.condition_md, s.design_handling_md, s.fe_handling_md,
		        s.deleted_at, s.created_at, s.updated_at
		   FROM prd_state s
		   JOIN prd_tab t ON t.tenant_id = s.tenant_id AND t.id = s.prd_tab_id
		   JOIN prd     p ON p.tenant_id = t.tenant_id AND p.id = t.prd_id
		  WHERE p.tenant_id = ? AND p.sub_flow_id = ?
		  ORDER BY s.position ASC, s.created_at ASC`,
		t.tenantID, subFlowID,
	)
	if err != nil {
		return nil, fmt.Errorf("load all states: %w", err)
	}
	defer rows.Close()
	var out []PRDState
	for rows.Next() {
		st, scanErr := scanPRDState(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// loadOutlineStemCounts returns per-state stem counts for the supplied
// state IDs. Five GROUP BY queries against the five stem tables; cheap
// because each table is small and already indexed by prd_state_id.
func (t *TenantRepo) loadOutlineStemCounts(ctx context.Context, stateIDs []string) (map[string]stemCountRow, error) {
	out := make(map[string]stemCountRow, len(stateIDs))
	if len(stateIDs) == 0 {
		return out, nil
	}

	type q struct {
		table string
		set   func(r *stemCountRow, n int)
	}
	queries := []q{
		{"prd_state_acceptance_criterion", func(r *stemCountRow, n int) { r.Criteria = n }},
		{"prd_state_event", func(r *stemCountRow, n int) { r.Events = n }},
		{"prd_state_copy_string", func(r *stemCountRow, n int) { r.Copy = n }},
		{"prd_state_edge_case", func(r *stemCountRow, n int) { r.EdgeCases = n }},
		{"prd_state_a11y_note", func(r *stemCountRow, n int) { r.A11y = n }},
	}

	for _, qq := range queries {
		query, args := buildInQuery(
			`SELECT prd_state_id, COUNT(*) FROM `+qq.table+
				` WHERE tenant_id = ? AND prd_state_id IN `,
			` GROUP BY prd_state_id`,
			t.tenantID, stateIDs,
		)
		rows, err := t.readHandle().QueryContext(ctx, query, args...)
		if err != nil {
			return nil, fmt.Errorf("count %s: %w", qq.table, err)
		}
		func() {
			defer rows.Close()
			for rows.Next() {
				var sid string
				var n int
				if err := rows.Scan(&sid, &n); err != nil {
					return
				}
				r := out[sid]
				qq.set(&r, n)
				out[sid] = r
			}
		}()
	}
	return out, nil
}

// loadOutlineWordCounts returns the total word count per state across
// the state's markdown columns + every typed-stem text column. Reads
// the same tables as LoadPRD's per-stem loaders, summing word counts
// directly so the load path doesn't materialize the full PRD.
func (t *TenantRepo) loadOutlineWordCounts(ctx context.Context, stateIDs []string, states []PRDState) (map[string]int, error) {
	out := make(map[string]int, len(stateIDs))
	if len(stateIDs) == 0 {
		return out, nil
	}

	// State-level markdown columns — already loaded; sum from the
	// PRDState slice the caller already has.
	for _, st := range states {
		out[st.ID] += wordCount(st.ConditionMD) + wordCount(st.DesignHandlingMD) + wordCount(st.FEHandlingMD)
	}

	// Stem text columns — one query per table, only the text column
	// returned (no FULL SELECT of the row).
	type q struct {
		query  string
		col    string
		filter string
	}
	queries := []q{
		{
			`SELECT prd_state_id, criterion FROM prd_state_acceptance_criterion`,
			"criterion", "",
		},
		{
			`SELECT prd_state_id, edge_case FROM prd_state_edge_case`,
			"edge_case", "",
		},
		{
			`SELECT prd_state_id, value FROM prd_state_copy_string`,
			"value", "",
		},
		{
			// Mixpanel events: count words in name + fires_on +
			// properties_schema. properties_schema is JSON so word-count
			// is approximate, but the wall just wants a thinness signal.
			`SELECT prd_state_id, name || ' ' || fires_on || ' ' || properties_schema FROM prd_state_event`,
			"events", "",
		},
		{
			`SELECT prd_state_id, note FROM prd_state_a11y_note`,
			"note", "",
		},
	}

	for _, qq := range queries {
		query, args := buildInQuery(
			qq.query+` WHERE tenant_id = ? AND prd_state_id IN `,
			``,
			t.tenantID, stateIDs,
		)
		rows, err := t.readHandle().QueryContext(ctx, query, args...)
		if err != nil {
			return nil, fmt.Errorf("words %s: %w", qq.col, err)
		}
		func() {
			defer rows.Close()
			for rows.Next() {
				var sid, text string
				if err := rows.Scan(&sid, &text); err != nil {
					return
				}
				out[sid] += wordCount(text)
			}
		}()
	}
	return out, nil
}

// wordCount is the cheapest reasonable word-count: counts runs of
// non-whitespace runes. UTF-8 safe; we use utf8.RuneCountInString only
// for ε(empty) detection. ±1 word per state is fine — the wall renders
// it as a "thinness" hint.
func wordCount(s string) int {
	if utf8.RuneCountInString(s) == 0 {
		return 0
	}
	return len(strings.Fields(s))
}
