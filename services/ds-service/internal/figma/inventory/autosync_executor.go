// autosync_executor — U10 + U18 of the autosync bridge.
//
// Plan: docs/plans/2026-05-14-001-feat-figma-db-autosync-bridge-plan.md
//
// The Planner is read-only — it emits a FilePlan describing what it would
// do but never mutates state. The Executor reads that FilePlan and runs
// it: for each PlannedSync with Action=full_export it builds a synthetic
// ExportRequest from figma_node data and calls the in-process runExport
// seam; for ActionCheapUpdate it updates flows.name without firing the
// pipeline; skip actions are no-ops (the planner already explained why).
//
// Idempotency comes from figma_auto_sync_state: every Execute writes a
// state row keyed on (tenant, file_key, page_id, section_id). On failure
// the prior LastSyncedFlowID/VersionID are preserved by UpsertAutoSyncState
// so the next attempt still knows which flow to update.
//
// READ-YOUR-WRITE (executor → planner) — the autosync retry loop runs
// Planner.PlanTenant + Executor.Execute sequentially in one goroutine
// per cycle. After Executor.Execute commits an UpsertAutoSyncState row,
// the next cycle's PlanTenant must observe that commit so the planner
// doesn't re-emit FullExport for an already-synced section. Under WAL
// this is naturally consistent (reads started after a commit see it),
// but the call-site contract: the planner reads run AFTER the executor's
// commit returns, both through the same TenantRepo instance. Don't
// reorder so reads start before the commit returns. Plan 2026-05-16-001
// R6 + U5.
package inventory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/projects"
)

// RunExportFunc is the function pointer the executor uses to invoke the
// in-process export pipeline. Wired by the caller (admin endpoint or CLI)
// with (*projects.Server).runExport — defined here as an interface to
// avoid a circular import.
type RunExportFunc func(ctx context.Context, p projects.RunExportParams) (projects.RunExportResult, error)

// ExecutorDB is the slice of TenantRepo behavior the executor needs
// beyond what AutoSyncDB already gives the planner.
type ExecutorDB interface {
	AutoSyncDB
}

// Executor turns a FilePlan into real writes.
type Executor struct {
	db        ExecutorDB
	runExport RunExportFunc

	// ServiceUserID is what runExport stamps as the actor on audit_log
	// entries created by autosync. Defaults to "autosync" if empty.
	ServiceUserID string

	now func() time.Time
}

// NewExecutor wires the executor. runExport MUST be non-nil — without it
// every full_export becomes a no-op and the integration test would pass
// despite producing no flows/screens rows.
func NewExecutor(db ExecutorDB, runExport RunExportFunc) *Executor {
	return &Executor{
		db:            db,
		runExport:     runExport,
		ServiceUserID: "autosync",
		now:           time.Now,
	}
}

// ExecuteResult summarises a single file's run.
type ExecuteResult struct {
	FileKey       string
	FileName      string
	Sections      int
	FullExported  int
	CheapUpdated  int
	SkippedAlready int
	SkippedQuar   int
	Errors        []string // human-readable per-section failure summaries
}

// Execute walks every section in the plan and runs the appropriate
// action. Returns aggregate counts plus any per-section errors —
// individual section failures do NOT abort the file (one bad section
// shouldn't block its siblings from syncing).
func (e *Executor) Execute(ctx context.Context, plan FilePlan) (ExecuteResult, error) {
	if e.runExport == nil {
		return ExecuteResult{}, errors.New("executor: runExport is nil")
	}
	res := ExecuteResult{FileKey: plan.FileKey, FileName: plan.FileName}
	if plan.FileSkip != nil {
		// File-level skip (out-of-window, project_unmapped) — nothing to
		// do. Caller already has plan.FileSkip for reporting.
		return res, nil
	}
	repo := e.db.NewTenantRepo(plan.TenantID)
	if repo == nil {
		return ExecuteResult{}, errors.New("executor: nil tenant repo")
	}

	// Two-pass approach (May 18, 2026 fix).
	// Pass 1: handle skip/cheap-update in-place AND collect the FullExport
	// PlannedSyncs into a slice. Pass 2 batches all FullExports into ONE
	// RunExport call so the resulting project_version contains every
	// section's screens together — not one version per section, which
	// hides every-section-but-the-last on the frontend (UpsertProject
	// keys on file_id alone, so per-section RunExport calls collide on
	// the same project row and only the latest "wins").
	var fullExports []PlannedSync
	for _, ps := range plan.Sections {
		res.Sections++
		switch ps.Action {
		case ActionSkipUnchanged:
			res.SkippedAlready++
		case ActionSkipQuarantined:
			res.SkippedQuar++
			// #13 audit fix: only upsert when the planner saw
			// SkipHashNotReady, where no prior row may exist yet and a
			// placeholder is needed for the admin dashboard. For
			// SkipMaxRetriesExceeded (the genuine F4 quarantine path)
			// the row already says status='quarantined' with its
			// content_hash frozen at the time of failure — re-upserting
			// here would overwrite content_hash with the *current* live
			// hash. That masked drift: after an operator cleared
			// quarantine, the planner saw state.content_hash ==
			// live.content_hash and emitted skip_unchanged, so the fix
			// never got re-exported.
			if ps.SkipReason == SkipHashNotReady {
				_ = repo.UpsertAutoSyncState(ctx, projects.AutoSyncState{
					FileKey:           plan.FileKey,
					PageID:            ps.PageID,
					SectionID:         ps.SectionID,
					LastAttemptStatus: "quarantined",
					SkipReason:        ps.SkipReason,
				})
			}
		case ActionCheapUpdate:
			if err := e.executeCheapUpdate(ctx, repo, plan, ps); err != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("%s/%s cheap_update: %v", ps.PageID, ps.SectionID, err))
				continue
			}
			res.CheapUpdated++
		case ActionFullExport:
			fullExports = append(fullExports, ps)
		}
	}

	// Pass 2 — batch all FullExports into a single RunExport per file.
	if len(fullExports) > 0 {
		batchErrs, batchOK := e.executeFullExportBatch(ctx, repo, plan, fullExports)
		res.FullExported += batchOK
		res.Errors = append(res.Errors, batchErrs...)
	}
	return res, nil
}

// executeFullExportBatch turns every PlannedSync with Action=FullExport on
// `plan` into ONE ExportRequest containing one FlowPayload per section,
// then calls runExport once. The resulting project_version owns every
// section's screens, so the project page's default render shows the whole
// file at once.
//
// Per-section bookkeeping (frame lookup, "no frame children" skip, state
// row upsert) is preserved — the loop just batches the actual RunExport
// instead of issuing N of them.
//
// Sections whose ListFrameChildrenOfSection returns 0 are dropped from
// the batch with a "no_frame_children" state row written directly; they
// don't pollute the ExportRequest.
//
// Sections whose frame count exceeds MaxFramesPerFlow are also dropped
// here (with an error recorded) — validating big sections is the caller's
// responsibility upstream; the executor refuses to send a malformed
// request. We could chunk a single section into multiple flows but that
// silently changes how the frontend sees the section, so failing loud is
// the better default.
//
// On runExport error: every section in the batch gets an 'error' state
// row (with shared error message). That's correct — they shared the
// transaction; if it failed, all of them failed together. The next
// planner cycle will re-pick them up via SkipRetryFailedPipeline.
//
// Returns (per-section error strings, number of sections that completed
// successfully). Errors are descriptive enough for ExecuteResult.
func (e *Executor) executeFullExportBatch(
	ctx context.Context,
	repo *projects.TenantRepo,
	plan FilePlan,
	syncs []PlannedSync,
) (errs []string, okCount int) {
	type ready struct {
		ps     PlannedSync
		frames []projects.FigmaSectionFrameChild
	}
	prepared := make([]ready, 0, len(syncs))
	for _, ps := range syncs {
		frames, err := repo.ListFrameChildrenOfSection(ctx, plan.FileKey, ps.SectionID)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s/%s list_frames: %v", ps.PageID, ps.SectionID, err))
			continue
		}
		if len(frames) == 0 {
			_ = repo.UpsertAutoSyncState(ctx, projects.AutoSyncState{
				FileKey:           plan.FileKey,
				PageID:            ps.PageID,
				SectionID:         ps.SectionID,
				ContentHash:       ps.LiveContentHash,
				PositionHash:      ps.LivePositionHash,
				LastAttemptStatus: "skipped",
				SkipReason:        "no_frame_children",
			})
			continue
		}
		// Refuse oversized sections — see method header.
		if len(frames) > projects.MaxFramesPerFlow {
			errs = append(errs, fmt.Sprintf("%s/%s frames_exceeded: %d > %d",
				ps.PageID, ps.SectionID, len(frames), projects.MaxFramesPerFlow))
			_ = repo.UpsertAutoSyncState(ctx, projects.AutoSyncState{
				FileKey:           plan.FileKey,
				PageID:            ps.PageID,
				SectionID:         ps.SectionID,
				LiveContentHash:   ps.LiveContentHash,
				LastAttemptStatus: "error",
				ErrorMessage: truncate(fmt.Sprintf("section has %d frames, MaxFramesPerFlow=%d",
					len(frames), projects.MaxFramesPerFlow), 400),
			})
			continue
		}
		prepared = append(prepared, ready{ps: ps, frames: frames})
	}
	if len(prepared) == 0 {
		return errs, 0
	}

	flowPayloads := make([]projects.FlowPayload, 0, len(prepared))
	for _, p := range prepared {
		ps := p.ps
		framePayloads := make([]projects.FramePayload, 0, len(p.frames))
		frameIDs := make([]string, 0, len(p.frames))
		for _, fr := range p.frames {
			frameIDs = append(frameIDs, fr.NodeID)
			framePayloads = append(framePayloads, projects.FramePayload{
				FrameID: fr.NodeID, Name: fr.Name,
				X: fr.X, Y: fr.Y, Width: fr.Width, Height: fr.Height,
			})
		}
		persona := sanitizeForExport(ps.PersonaHint)
		if persona == "default" {
			persona = ""
		}
		flowName := sanitizeForExport(ps.SubFlow)
		if flowName == "" {
			flowName = sanitizeForExport(ps.Section)
		}
		if flowName == "" {
			flowName = "Untitled"
		}
		path := sanitizeForExport(JoinFlowPath(ps.Domain, ps.Product, ps.SubProduct, ps.SubFlow))
		product := sanitizeForExport(ps.Product)
		sectionID := ps.SectionID
		flowPayloads = append(flowPayloads, projects.FlowPayload{
			SectionID:   &sectionID,
			FrameIDs:    frameIDs,
			Frames:      framePayloads,
			Platform:    derivePlatform(plan, ps),
			Product:     product,
			Path:        path,
			PersonaName: persona,
			Name:        flowName,
		})
	}

	req := projects.ExportRequest{
		IdempotencyKey: uuid.NewString(),
		FileID:         plan.FileKey,
		FileName:       plan.FileName,
		Flows:          flowPayloads,
	}
	result, runErr := e.runExport(ctx, projects.RunExportParams{
		TenantID:  plan.TenantID,
		UserID:    e.ServiceUserID,
		Source:    "autosync",
		ClientIP:  "autosync",
		UserAgent: "ds-service/autosync",
		Req:       req,
	})
	if runErr != nil {
		errMsg := truncate(runErr.Error(), 400)
		for _, p := range prepared {
			_ = repo.UpsertAutoSyncState(ctx, projects.AutoSyncState{
				FileKey:           plan.FileKey,
				PageID:            p.ps.PageID,
				SectionID:         p.ps.SectionID,
				LiveContentHash:   p.ps.LiveContentHash,
				LastAttemptStatus: "error",
				ErrorMessage:      errMsg,
			})
			errs = append(errs, fmt.Sprintf("%s/%s full_export_batch: %v", p.ps.PageID, p.ps.SectionID, runErr))
		}
		return errs, 0
	}

	// Success — record state for every section in the batch. They all
	// share the same project_version.
	for _, p := range prepared {
		ps := p.ps
		if err := repo.UpsertAutoSyncState(ctx, projects.AutoSyncState{
			FileKey:             plan.FileKey,
			PageID:              ps.PageID,
			SectionID:           ps.SectionID,
			ContentHash:         ps.LiveContentHash,
			PositionHash:        ps.LivePositionHash,
			NodeMetadataHash:    ps.LiveNodeMetadataHash,
			LiveContentHash:     ps.LiveContentHash,
			LastSyncedVersionID: result.VersionID,
			LastAttemptStatus:   "ok",
		}); err != nil {
			errs = append(errs, fmt.Sprintf("%s/%s persist_state: %v", ps.PageID, ps.SectionID, err))
			continue
		}
		okCount++
	}
	return errs, okCount
}

// executeFullExport builds a synthetic ExportRequest from figma_node and
// calls runExport. On success persists last_synced_flow_id +
// last_synced_version_id so the next plan cycle can skip on hash match.
func (e *Executor) executeFullExport(
	ctx context.Context,
	repo *projects.TenantRepo,
	plan FilePlan,
	ps PlannedSync,
) error {
	frames, err := repo.ListFrameChildrenOfSection(ctx, plan.FileKey, ps.SectionID)
	if err != nil {
		return fmt.Errorf("list frames: %w", err)
	}
	if len(frames) == 0 {
		// Section has no FRAME children — common for header-only sections
		// or sections whose children are GROUP-wrapped. Record state so
		// the planner skips this section next time without a re-attempt.
		_ = repo.UpsertAutoSyncState(ctx, projects.AutoSyncState{
			FileKey:           plan.FileKey,
			PageID:            ps.PageID,
			SectionID:         ps.SectionID,
			ContentHash:       ps.LiveContentHash,
			PositionHash:      ps.LivePositionHash,
			LastAttemptStatus: "skipped",
			SkipReason:        "no_frame_children",
		})
		return nil
	}

	framePayloads := make([]projects.FramePayload, 0, len(frames))
	frameIDs := make([]string, 0, len(frames))
	for _, fr := range frames {
		frameIDs = append(frameIDs, fr.NodeID)
		framePayloads = append(framePayloads, projects.FramePayload{
			FrameID: fr.NodeID,
			Name:    fr.Name,
			X:       fr.X,
			Y:       fr.Y,
			Width:   fr.Width,
			Height:  fr.Height,
		})
	}

	persona := sanitizeForExport(ps.PersonaHint)
	if persona == "default" {
		persona = "" // pipeline treats empty persona as "no persona"
	}

	flowName := sanitizeForExport(ps.SubFlow)
	if flowName == "" {
		flowName = sanitizeForExport(ps.Section)
	}
	if flowName == "" {
		flowName = "Untitled"
	}

	// Path is "Domain/Product/SubProduct/SubFlow" — sanitised so it passes
	// projects.allowlistRegex (^[\w \-_/&·]+$). Strips emoji, parens,
	// punctuation that designers casually include in section names.
	path := sanitizeForExport(JoinFlowPath(ps.Domain, ps.Product, ps.SubProduct, ps.SubFlow))
	product := sanitizeForExport(ps.Product)

	sectionID := ps.SectionID
	req := projects.ExportRequest{
		IdempotencyKey: uuid.NewString(),
		FileID:         plan.FileKey,
		FileName:       plan.FileName,
		Flows: []projects.FlowPayload{
			{
				SectionID:   &sectionID,
				FrameIDs:    frameIDs,
				Frames:      framePayloads,
				Platform:    derivePlatform(plan, ps),
				Product:     product,
				Path:        path,
				PersonaName: persona,
				Name:        flowName,
			},
		},
	}

	result, runErr := e.runExport(ctx, projects.RunExportParams{
		TenantID:  plan.TenantID,
		UserID:    e.ServiceUserID,
		Source:    "autosync",
		ClientIP:  "autosync",
		UserAgent: "ds-service/autosync",
		Req:       req,
	})
	if runErr != nil {
		// Persist error state. Plan 2026-05-17-001 correctness-#13 fix:
		// only update live_content_hash on error — content_hash stays
		// at the last SYNCED value so the planner can detect designer
		// fixes during quarantine via LiveContentHash divergence.
		// Position is the same — only update position_hash on success.
		_ = repo.UpsertAutoSyncState(ctx, projects.AutoSyncState{
			FileKey:           plan.FileKey,
			PageID:            ps.PageID,
			SectionID:         ps.SectionID,
			LiveContentHash:   ps.LiveContentHash,
			LastAttemptStatus: "error",
			ErrorMessage:      truncate(runErr.Error(), 400),
		})
		return runErr
	}

	// Success — persist hashes + the new flow/version ids.
	// runExport currently returns one flow + one version per call
	// (we sent a single FlowPayload). Capture the first screen's flow_id
	// indirectly via result; the result object only has ProjectID +
	// VersionID. The flow_id lives in the txRepo's UpsertFlow output,
	// which runExport doesn't surface. For the test we record version_id
	// only — the planner's idempotency cares about content_hash, not
	// flow_id specifically.
	flowID := "" // TODO: surface flow_id via RunExportResult.FlowIDs if we ever need it
	// Success path: write both content_hash (the new synced state) and
	// live_content_hash (same value — what we just observed). On the
	// next cycle, the planner reads both and they match, so a designer
	// fix is detectable as soon as it diverges.
	if err := repo.UpsertAutoSyncState(ctx, projects.AutoSyncState{
		FileKey:             plan.FileKey,
		PageID:              ps.PageID,
		SectionID:           ps.SectionID,
		ContentHash:         ps.LiveContentHash,
		PositionHash:        ps.LivePositionHash,
		NodeMetadataHash:    ps.LiveNodeMetadataHash,
		LiveContentHash:     ps.LiveContentHash,
		LastSyncedFlowID:    flowID,
		LastSyncedVersionID: result.VersionID,
		LastAttemptStatus:   "ok",
	}); err != nil {
		return fmt.Errorf("persist state: %w", err)
	}
	return nil
}

// executeCheapUpdate handles the position-only / name-only case: the
// section's child hashes match but its OWN x/y/name moved. Updates
// flows.name without touching the audit pipeline, then persists the new
// position_hash so subsequent crawls land on skip_unchanged.
func (e *Executor) executeCheapUpdate(
	ctx context.Context,
	repo *projects.TenantRepo,
	plan FilePlan,
	ps PlannedSync,
) error {
	newName := ps.SubFlow
	if newName == "" {
		newName = ps.Section
	}
	if _, err := repo.UpdateFlowName(ctx, plan.FileKey, ps.SectionID, newName); err != nil {
		// Non-fatal: a cheap-update where the flow doesn't exist yet
		// shouldn't error — record state and continue.
		_ = repo.UpsertAutoSyncState(ctx, projects.AutoSyncState{
			FileKey:           plan.FileKey,
			PageID:            ps.PageID,
			SectionID:         ps.SectionID,
			ContentHash:       ps.LiveContentHash,
			PositionHash:      ps.LivePositionHash,
			LastAttemptStatus: "error",
			ErrorMessage:      truncate(err.Error(), 400),
		})
		return err
	}
	if err := repo.UpsertAutoSyncState(ctx, projects.AutoSyncState{
		FileKey:           plan.FileKey,
		PageID:            ps.PageID,
		SectionID:         ps.SectionID,
		ContentHash:       ps.LiveContentHash,
		PositionHash:      ps.LivePositionHash,
		NodeMetadataHash:  ps.LiveNodeMetadataHash,
		LastAttemptStatus: "ok",
	}); err != nil {
		return fmt.Errorf("persist state: %w", err)
	}
	return nil
}

// derivePlatform picks the platform string for an ExportRequest. Today
// the planner doesn't carry the mapping's platform_default into
// PlannedSync — so we fall back to "mobile" as the most common case.
// Future iteration could surface PlatformDefault on PlannedSync.
func derivePlatform(_ FilePlan, _ PlannedSync) string {
	return "mobile"
}

// sanitizeForExport collapses each input rune to either itself (when it
// passes projects.allowlistRegex ^[\w \-_/&·]+$) or a single space.
// Multiple spaces collapse to one. Result is trimmed. Used to scrub
// designer-supplied section names + paths before they hit validateExport.
func sanitizeForExport(s string) string {
	if s == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(s))
	lastSpace := false
	for _, r := range s {
		ok := r == ' ' ||
			r == '-' || r == '_' || r == '/' || r == '&' || r == '·' ||
			(r >= '0' && r <= '9') ||
			(r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z')
		if ok {
			b.WriteRune(r)
			lastSpace = r == ' '
		} else if !lastSpace {
			b.WriteByte(' ')
			lastSpace = true
		}
	}
	out := strings.TrimSpace(b.String())
	// Collapse "  " just in case the loop introduced doubles around drops.
	for strings.Contains(out, "  ") {
		out = strings.ReplaceAll(out, "  ", " ")
	}
	return out
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

// JoinSubflowOnly returns the subflow without leading "(unassigned)/".
// Useful for log lines.
func JoinSubflowOnly(subProduct, subFlow string) string {
	if subProduct == "" || subProduct == projects.UnassignedSubProduct {
		return subFlow
	}
	return strings.TrimSpace(subFlow)
}
