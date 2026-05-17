package projects

import (
	"context"
	"errors"
	"fmt"
	"regexp"
)

// repository_figma_autosync_skeleton.go — U2b of the MCP + PM authoring
// workflow plan (docs/plans/2026-05-17-002-feat-mcp-ds-service-pm-workflow-plan.md).
//
// When autosync binds a Figma section to a sub_flow (U2), this method
// extends the wiring by pre-creating one prd_state row per PM-meaningful
// direct-child frame. The PM never re-encodes the state structure — they
// only fill content (acceptance criteria, copy strings, events, etc.) on
// the auto-skeletoned rows.
//
// Pipeline (per (sub_flow, frame-list) call):
//
//   1. UpsertPRD(sub_flow_id = subFlowID) — idempotent on (tenant, sub_flow).
//   2. UpsertPRDTab(prd_id, name="default", position=0) — single-tab v1.
//      Multi-tab structure is deferred; "default" works as a sentinel the
//      viewer suppresses when only one tab exists.
//   3. For each frame in `frames` (already sorted by canvas Y by caller):
//        - Skip Figma default-named frames (`Frame 12345`, `Rectangle 6789`,
//          `Group 1`, `Union`, etc.) — they're not state-worthy.
//        - UpsertPRDState(prd_tab_id, label=frame.Name, frame_name=frame.Name,
//          position=i). U4's contract clears deleted_at on a soft-deleted
//          row with the same (prd_tab_id, label) — that's the rename-then-
//          restore loop.
//   4. Soft-delete any prior auto-skeleton state in this tab whose label is
//      NOT in the current frame set AND which still has frame_name set
//      (the simple "pristine skeleton" proxy — see decision below).
//
// Designer-default name regex blocklist
//   `Frame \d+`, `Rectangle \d+`, `Ellipse \d+`, `Group \d+`, `Vector \d+`,
//   `Line \d+`, and bare `Union`. Mirrors the plan U2b regex. This filter
//   applies ONLY to skeleton creation. The MCP `section.frames` tool (U6)
//   still returns these — designer naming is canonical for *display*; the
//   filter only governs *what becomes a skeleton state*.
//
// Soft-delete simplification (per plan contract DECISION POINT)
//   Rule: a prd_state qualifies for soft-delete when its label isn't in the
//   current frame set AND `frame_name IS NOT NULL` (it was auto-created and
//   still carries the originating frame name). The PM editing the state
//   today does NOT clear frame_name (U4's UpsertPRDState preserves it), so
//   this proxy is imperfect for the MVP — but its failure mode is acceptable:
//   a PM-authored state whose frame was removed gets soft-deleted, and then
//   when the designer re-adds the frame, UpsertPRDState clears deleted_at
//   AND the authored stems (acceptance criteria, events, copy strings) are
//   preserved because soft-delete doesn't touch them.
//
//   Tightening this proxy (e.g. counting authored stems) is tracked for a
//   follow-up; v1 keeps it simple per the U2b plan's recommendation.
//
// Tenant scoping
//   All reads/writes go through the TenantRepo and carry tenant_id at the
//   SQL site via UpsertPRD/UpsertPRDTab/UpsertPRDState/SoftDeletePRDState.
//
// Read-before-tx
//   Step 4 reads prior states from the read pool BEFORE the soft-delete
//   writes fire. U4's upsert methods already serialize their own reads
//   against the read pool first (they pass t.readHandle()), so the auto-
//   skeleton flow inherits that discipline.
//
// Empty section / no frames
//   Returns nil. The PRD + default tab are still created so the PM can
//   start authoring against a section that the designer hasn't yet
//   populated.
//
// Idempotency
//   Re-running with the same frame list produces the same prd_state ids
//   (UpsertPRDState matches on (prd_tab_id, LOWER(TRIM(label))). Authored
//   stems are preserved across runs.

// figmaDefaultName matches the Figma auto-generated frame names that
// designers haven't renamed yet. See the plan U2b "Figma-default name
// regex blocklist" section. Order in the regex matters only for the
// scanner's compile cost; runtime cost is uniform per match.
var figmaDefaultName = regexp.MustCompile(`^(Frame|Rectangle|Ellipse|Group|Vector|Line) \d+$|^Union$`)

// defaultSkeletonTabName is the single-tab v1 sentinel name. The viewer
// suppresses tab headers when only this tab exists. Lower-cased + trimmed
// it remains "default" — U4's idempotency key for prd_tab is
// (prd_id, LOWER(TRIM(name))), so this is stable across re-runs.
const defaultSkeletonTabName = "default"

// AutoSkeletonPRDStates seeds (or refreshes) the PRD skeleton for a
// sub_flow. The caller (the inventory poller's syncFileDeep, after the
// autosync write tx commits) passes the section's direct-child FRAME /
// INSTANCE / COMPONENT children, already sorted by (AbsY, AbsX).
//
// Behaviour:
//   - Creates / updates exactly one PRD per sub_flow.
//   - Creates / updates exactly one "default" PRDTab on that PRD.
//   - For each non-default-named frame: upserts a prd_state row.
//   - Soft-deletes any prior auto-skeleton state whose label isn't in the
//     current frame set (frame_name IS NOT NULL gate). Authored stems
//     remain in place — they're recoverable on a frame-restore cycle.
//
// Empty `frames` list is supported (e.g. the section has only TEXT /
// VECTOR / GROUP / RECTANGLE children, or is brand-new and empty). The PRD
// + default tab are still materialised so the PM has somewhere to author
// even before the designer ships frames. Soft-delete then sweeps any
// prior skeleton.
//
// Errors are returned but the caller (poller) logs-and-continues per the
// U2 pattern — one bad section must not abort the rest of the file's
// autosync.
func (t *TenantRepo) AutoSkeletonPRDStates(ctx context.Context, subFlowID string, frames []FrameRow) error {
	if t.tenantID == "" {
		return errors.New("auto_skeleton: tenant_id required")
	}
	if subFlowID == "" {
		return errors.New("auto_skeleton: sub_flow_id required")
	}

	// 1. Upsert the parent PRD. Title is intentionally empty — the skeleton
	//    is structure only; the PM (or U6 MCP write) supplies the title.
	prd, err := t.UpsertPRD(ctx, PRDInput{SubFlowID: subFlowID})
	if err != nil {
		return fmt.Errorf("auto_skeleton: upsert prd: %w", err)
	}

	// 2. Upsert the single default tab. Position 0; overview blank. Name
	//    "default" doubles as a sentinel the viewer suppresses when only
	//    one tab exists (multi-tab is a deferred shape).
	tab, err := t.UpsertPRDTab(ctx, PRDTabInput{
		PRDID:    prd.ID,
		Name:     defaultSkeletonTabName,
		Position: 0,
	})
	if err != nil {
		return fmt.Errorf("auto_skeleton: upsert prd_tab: %w", err)
	}

	// 3. Build the set of labels we'll keep this pass — case-insensitive
	//    via normalizeName so the soft-delete sweep below matches the
	//    same (prd_tab_id, LOWER(TRIM(label))) key UpsertPRDState uses.
	keepLabels := make(map[string]struct{}, len(frames))
	position := 0
	for _, frame := range frames {
		if frame.Name == "" {
			continue // verbatim policy still requires a non-empty handle to write
		}
		if figmaDefaultName.MatchString(frame.Name) {
			// Designer default — skip skeleton creation. Per the plan U2b
			// contract, these names are still surfaced by section.frames
			// (U6) so PM tooling can prompt the designer to rename them;
			// they just don't become prd_state rows.
			continue
		}
		if ierr := t.ensureSkeletonRow(ctx, tab.ID, frame.Name, frame.Name, position); ierr != nil {
			return fmt.Errorf("auto_skeleton: upsert prd_state %q: %w", frame.Name, ierr)
		}
		keepLabels[normalizeName(frame.Name)] = struct{}{}
		position++
	}

	// 4. Soft-delete any prior auto-skeleton state whose label is NOT in
	//    the current frame set. The frame_name IS NOT NULL gate is the
	//    "still pristine skeleton" proxy — see the file header for the
	//    decision rationale and its failure mode. Authored stems are
	//    untouched (SoftDeletePRDState only writes prd_state.deleted_at).
	if err := t.sweepOrphanSkeletons(ctx, tab.ID, keepLabels); err != nil {
		return fmt.Errorf("auto_skeleton: sweep orphans: %w", err)
	}

	return nil
}

// sweepOrphanSkeletons soft-deletes any live prd_state on the given tab
// whose label isn't in `keep` AND whose frame_name is set (i.e. it was
// auto-skeletoned, not hand-authored from scratch via the MCP path).
//
// Read-before-tx: the SELECT runs against the read handle first; the per-
// row UPDATE goes through SoftDeletePRDState which uses the write handle.
// Single-writer serialisation makes this safe under concurrent autosync
// passes — the worst case is a redundant soft-delete that the next pass
// restores.
func (t *TenantRepo) sweepOrphanSkeletons(ctx context.Context, tabID string, keep map[string]struct{}) error {
	rows, err := t.readHandle().QueryContext(ctx, `
		SELECT id, label
		  FROM prd_state
		 WHERE tenant_id = ?
		   AND prd_tab_id = ?
		   AND deleted_at IS NULL
		   AND frame_name IS NOT NULL
	`, t.tenantID, tabID)
	if err != nil {
		return fmt.Errorf("list prior skeleton states: %w", err)
	}

	type orphan struct{ id, label string }
	var orphans []orphan
	for rows.Next() {
		var o orphan
		if scanErr := rows.Scan(&o.id, &o.label); scanErr != nil {
			rows.Close()
			return fmt.Errorf("scan prd_state: %w", scanErr)
		}
		if _, kept := keep[normalizeName(o.label)]; kept {
			continue
		}
		orphans = append(orphans, o)
	}
	if iterErr := rows.Err(); iterErr != nil {
		rows.Close()
		return fmt.Errorf("iterate prd_state: %w", iterErr)
	}
	rows.Close()

	for _, o := range orphans {
		if delErr := t.SoftDeletePRDState(ctx, o.id); delErr != nil {
			// If the row was concurrently deleted by another pass we get
			// ErrPRDStateNotFound. Tolerate that — it's still "gone".
			if errors.Is(delErr, ErrPRDStateNotFound) {
				continue
			}
			return fmt.Errorf("soft-delete orphan %s (%q): %w", o.id, o.label, delErr)
		}
	}
	return nil
}

// ensureSkeletonRow upserts the structural fields of a prd_state row
// (label, position, frame_name, deleted_at) WITHOUT touching the three
// PM-authored markdown columns (condition_md, design_handling_md,
// fe_handling_md).
//
// Why this exists: U4's UpsertPRDState is a full overwrite — every call
// rewrites condition_md / design_handling_md / fe_handling_md from the
// PRDStateInput, even when those input fields are blank. The autosync
// skeleton has no business carrying authored content; if it called
// UpsertPRDState with empty markdown it would wipe whatever the PM (or the
// U6 MCP write path) had authored on the previous pass.
//
// Contract:
//   - Live row exists at (prd_tab_id, LOWER(TRIM(label))):
//       UPDATE label / position / frame_name / updated_at only. Authored
//       markdown columns are left untouched.
//   - Soft-deleted row exists at (prd_tab_id, LOWER(TRIM(label))):
//       UPDATE same columns AND clear deleted_at (restore loop). Authored
//       markdown is still preserved — restoring a frame doesn't reset what
//       the PM wrote before the soft-delete.
//   - No row exists:
//       INSERT a brand-new row via UpsertPRDState. Authored markdown is
//       empty in PRDStateInput, which is correct for a freshly-discovered
//       frame — there's nothing to preserve.
//
// Read-before-tx: the lookup uses the read handle; the UPDATE / INSERT
// goes through the write handle (UpsertPRDState handles its own write).
// Each branch is its own autocommit transaction.
func (t *TenantRepo) ensureSkeletonRow(ctx context.Context, tabID, label, frameName string, position int) error {
	key := normalizeName(label)
	existing, err := t.getPRDStateByLabelIncludingDeleted(ctx, tabID, key, t.readHandle())
	if err != nil && !errors.Is(err, ErrNotFound) {
		return fmt.Errorf("lookup prd_state: %w", err)
	}
	if err == nil {
		// Row exists (live OR soft-deleted) — update structure only,
		// clear deleted_at, leave authored markdown columns untouched.
		now := t.now().UTC()
		if _, uerr := t.handle().ExecContext(ctx,
			`UPDATE prd_state
			    SET label = ?, position = ?, frame_name = ?,
			        deleted_at = NULL, updated_at = ?
			  WHERE tenant_id = ? AND id = ?`,
			label, position, nullIfEmpty(frameName),
			rfc3339(now),
			t.tenantID, existing.ID,
		); uerr != nil {
			return fmt.Errorf("update prd_state skeleton: %w", uerr)
		}
		return nil
	}
	// No row — fall through to UpsertPRDState (which will INSERT). The
	// markdown fields default to empty strings, which is the correct
	// starting state for a brand-new skeleton row.
	if _, ierr := t.UpsertPRDState(ctx, PRDStateInput{
		PRDTabID:  tabID,
		Label:     label,
		Position:  position,
		FrameName: frameName,
	}); ierr != nil {
		return ierr
	}
	return nil
}
