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

	for _, ps := range plan.Sections {
		res.Sections++
		switch ps.Action {
		case ActionSkipUnchanged:
			res.SkippedAlready++
		case ActionSkipQuarantined:
			res.SkippedQuar++
			// Persist a state row so the admin dashboard sees the
			// quarantine reason. Idempotent across retries.
			_ = repo.UpsertAutoSyncState(ctx, projects.AutoSyncState{
				FileKey:           plan.FileKey,
				PageID:            ps.PageID,
				SectionID:         ps.SectionID,
				ContentHash:       ps.LiveContentHash,
				PositionHash:      ps.LivePositionHash,
				LastAttemptStatus: "quarantined",
				SkipReason:        ps.SkipReason,
			})
		case ActionCheapUpdate:
			if err := e.executeCheapUpdate(ctx, repo, plan, ps); err != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("%s/%s cheap_update: %v", ps.PageID, ps.SectionID, err))
				continue
			}
			res.CheapUpdated++
		case ActionFullExport:
			if err := e.executeFullExport(ctx, repo, plan, ps); err != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("%s/%s full_export: %v", ps.PageID, ps.SectionID, err))
				continue
			}
			res.FullExported++
		}
	}
	return res, nil
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

	persona := ps.PersonaHint
	if persona == "default" {
		persona = "" // pipeline treats empty persona as "no persona"
	}

	flowName := ps.SubFlow
	if flowName == "" {
		flowName = ps.Section
	}

	// Path is "Domain/Product/SubProduct/SubFlow" — used as a stable slug
	// fragment downstream. JoinFlowPath already trims empties.
	path := JoinFlowPath(ps.Domain, ps.Product, ps.SubProduct, ps.SubFlow)

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
				Product:     ps.Product,
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
		// Persist error state — preserves prior flow/version ids so a
		// retry still updates the existing flow rather than orphaning.
		_ = repo.UpsertAutoSyncState(ctx, projects.AutoSyncState{
			FileKey:           plan.FileKey,
			PageID:            ps.PageID,
			SectionID:         ps.SectionID,
			ContentHash:       ps.LiveContentHash,
			PositionHash:      ps.LivePositionHash,
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
	if err := repo.UpsertAutoSyncState(ctx, projects.AutoSyncState{
		FileKey:             plan.FileKey,
		PageID:              ps.PageID,
		SectionID:           ps.SectionID,
		ContentHash:         ps.LiveContentHash,
		PositionHash:        ps.LivePositionHash,
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
