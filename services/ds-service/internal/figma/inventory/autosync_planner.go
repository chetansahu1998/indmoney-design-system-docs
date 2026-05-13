// Package inventory's AutoSyncPlanner — U7 of the autosync bridge plan
// (docs/plans/2026-05-14-001-feat-figma-db-autosync-bridge-plan.md).
//
// Phase B ships only the read-only Plan() side: given a file_key, return
// a []PlannedSync describing what would be synced, what would be skipped
// (and why), and what would get a cheap name/position update. No DB
// writes. No HandleExport calls. Pure read so admins can dry-run the
// planner against the real corpus before Phase C enables writes.
package inventory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/projects"
)

// PlanAction is what the planner intends to do for a given section.
type PlanAction string

const (
	// ActionFullExport — section content changed (or this is the first
	// time we've seen this section). Phase C calls runExport.
	ActionFullExport PlanAction = "full_export"
	// ActionCheapUpdate — section's name or x/y moved but its subtree
	// content is unchanged. Phase C runs a direct UPDATE on flows.name;
	// no audit pipeline involvement.
	ActionCheapUpdate PlanAction = "cheap_update"
	// ActionSkipUnchanged — content_hash + position_hash both match
	// the prior state row. No-op.
	ActionSkipUnchanged PlanAction = "skip_unchanged"
	// ActionSkipQuarantined — file or project gates the section. Reason
	// in PlannedSync.SkipReason.
	ActionSkipQuarantined PlanAction = "skip_quarantined"
)

// SkipReason — set on PlannedSync.SkipReason when Action ∈ {
// skip_quarantined, skip_unchanged }.
const (
	SkipOutOfWindow     = "out_of_window"     // file's last_modified older than 6 months
	SkipProjectUnmapped = "project_unmapped"  // figma_project_mapping missing
	SkipMappingDisabled = "mapping_disabled"  // mapping exists, enabled_for_autosync=0
	SkipNoSourcePage    = "no_source_page"    // no Final + no version pages
	SkipHashNotReady    = "hash_not_ready"    // deep_synced_at hasn't populated content_hash yet
	SkipAlreadySynced   = "already_synced"    // content_hash matches prior state
	SkipNewSection      = "new_section"       // FullExport reason: never synced before
	SkipContentChanged  = "content_changed"   // FullExport reason: subtree hash flipped
	SkipPositionOnly    = "position_or_name_changed" // CheapUpdate reason
)

// PlanReason carries the FILE-level skip reason (when no section-level plan
// rows are emitted). This is what the planner returns at the top level so
// admins can see "this file is in quarantine because X" without having to
// enumerate every section.
type PlanReason struct {
	Code    string // matches SkipOutOfWindow / SkipProjectUnmapped / etc.
	Message string
}

// PlannedSync is one row of the planner's output. Per-section.
type PlannedSync struct {
	TenantID  string `json:"tenant_id"`
	FileKey   string `json:"file_key"`
	FileName  string `json:"file_name"`
	PageID    string `json:"page_id"`
	PageName  string `json:"page_name"`
	SectionID string `json:"section_id"`
	Section   string `json:"section_name"`

	// Action + reason — the planner's verdict for this section.
	Action     PlanAction `json:"action"`
	Reason     string     `json:"reason,omitempty"`
	SkipReason string     `json:"skip_reason,omitempty"`

	// Parsed nomenclature from the section name.
	SubProduct string `json:"sub_product"`
	SubFlow    string `json:"sub_flow"`

	// Carried forward from the page classifier.
	PersonaHint string `json:"persona_hint,omitempty"`

	// Mapping inputs (admin-managed taxonomy).
	Domain  string `json:"domain,omitempty"`
	Product string `json:"product,omitempty"`

	// Live hashes from figma_section (compared against PriorContentHash).
	LiveContentHash  string `json:"live_content_hash"`
	LivePositionHash string `json:"live_position_hash"`

	// Hashes from the last successful sync (figma_auto_sync_state).
	// Empty when this is a new section.
	PriorContentHash    string `json:"prior_content_hash,omitempty"`
	PriorPositionHash   string `json:"prior_position_hash,omitempty"`
	PriorFlowID         string `json:"prior_flow_id,omitempty"`
	PriorLastSyncedAt   string `json:"prior_last_synced_at,omitempty"`
}

// FilePlan is the top-level result: a file's per-section plan rows plus
// any file-level skip reason if the planner short-circuited.
type FilePlan struct {
	TenantID    string         `json:"tenant_id"`
	FileKey     string         `json:"file_key"`
	FileName    string         `json:"file_name"`
	ProjectID   string         `json:"project_id"`
	ProjectName string         `json:"project_name,omitempty"`
	FileSkip    *PlanReason    `json:"file_skip,omitempty"`
	Sections    []PlannedSync  `json:"sections,omitempty"`
}

// Planner is the read-only AutoSync planner. Construct via NewPlanner.
type Planner struct {
	db      AutoSyncDB
	now     func() time.Time
	window  time.Duration
}

// AutoSyncDB is the slice of TenantRepo behavior the planner needs.
// Defining the interface here means tests can pass a fake without a real DB.
type AutoSyncDB interface {
	NewTenantRepo(tenantID string) *projects.TenantRepo
}

// PlannerConfig is optional. Pass zero values for defaults.
type PlannerConfig struct {
	Now    func() time.Time
	Window time.Duration // 6-month default
}

// NewPlanner constructs a Planner. The DB injector should return a
// TenantRepo for the given tenant_id — typically a closure over the
// shared *sql.DB.
func NewPlanner(db AutoSyncDB, cfg PlannerConfig) *Planner {
	p := &Planner{db: db}
	p.now = cfg.Now
	if p.now == nil {
		p.now = time.Now
	}
	p.window = cfg.Window
	if p.window == 0 {
		p.window = 6 * 30 * 24 * time.Hour // ~6 months
	}
	return p
}

// Plan computes the planner's verdict for a single file. Read-only:
// queries figma_file / figma_project / figma_project_mapping /
// figma_page / figma_section / figma_auto_sync_state, then emits
// per-section PlannedSync rows. No DB writes.
//
// Errors are returned only on plumbing failures (DB unreachable, bad
// inputs); logical skips (out-of-window, unmapped, no source page) come
// back as FilePlan.FileSkip so callers can render the reason without
// distinguishing error types.
func (p *Planner) Plan(ctx context.Context, tenantID, fileKey string) (FilePlan, error) {
	if tenantID == "" {
		return FilePlan{}, errors.New("inventory: tenant_id required")
	}
	if fileKey == "" {
		return FilePlan{}, errors.New("inventory: file_key required")
	}
	repo := p.db.NewTenantRepo(tenantID)

	// 1. Load the file row. ErrNotFound → file_skip.
	file, err := repo.LookupFigmaFile(ctx, fileKey, false)
	if errors.Is(err, projects.ErrNotFound) {
		return FilePlan{TenantID: tenantID, FileKey: fileKey, FileSkip: &PlanReason{
			Code: "file_not_found", Message: "file not in this tenant's inventory",
		}}, nil
	}
	if err != nil {
		return FilePlan{}, fmt.Errorf("lookup file: %w", err)
	}

	fp := FilePlan{
		TenantID:  tenantID,
		FileKey:   fileKey,
		FileName:  file.Name,
		ProjectID: file.ProjectID,
	}

	// 2. 6-month window check. file.LastModified zero is treated as
	// "we haven't crawled this file's last_modified yet" and we skip
	// with hash_not_ready (poll cycle hasn't completed).
	if file.LastModified.IsZero() {
		fp.FileSkip = &PlanReason{Code: SkipHashNotReady, Message: "file.last_modified not yet known"}
		return fp, nil
	}
	cutoff := p.now().Add(-p.window)
	if file.LastModified.Before(cutoff) {
		fp.FileSkip = &PlanReason{
			Code:    SkipOutOfWindow,
			Message: fmt.Sprintf("file modified %s, before cutoff %s", file.LastModified.Format(time.RFC3339), cutoff.Format(time.RFC3339)),
		}
		return fp, nil
	}

	// 3. Project mapping. Missing → quarantine.
	mapping, err := repo.LookupFigmaProjectMapping(ctx, file.ProjectID)
	if errors.Is(err, projects.ErrNotFound) {
		fp.FileSkip = &PlanReason{Code: SkipProjectUnmapped, Message: "no figma_project_mapping for this project"}
		return fp, nil
	}
	if err != nil {
		return FilePlan{}, fmt.Errorf("lookup mapping: %w", err)
	}
	if !mapping.EnabledForAutosync {
		fp.FileSkip = &PlanReason{Code: SkipMappingDisabled, Message: "mapping exists but enabled_for_autosync=0"}
		return fp, nil
	}
	// Look up project name for human-friendly rendering. Best-effort.
	if proj, perr := repo.LookupFigmaProject(ctx, file.ProjectID); perr == nil {
		fp.ProjectName = proj.Name
	}

	// 4. Pages + classifier output. Read from figma_page directly (the
	// classifier already ran during the deep-sync write, U4).
	pages, err := repo.ListFigmaPagesForFile(ctx, fileKey)
	if err != nil {
		return FilePlan{}, fmt.Errorf("list pages: %w", err)
	}
	classified := make([]projects.ClassifiedPage, 0, len(pages))
	for _, pg := range pages {
		if pg.PageClassification == "" {
			// Hash + classification not yet populated for this page.
			// Skip the whole file as not-ready; next poll cycle will fix.
			fp.FileSkip = &PlanReason{Code: SkipHashNotReady, Message: "page_classification not populated yet"}
			return fp, nil
		}
		classified = append(classified, projects.ClassifiedPage{
			PageID:         pg.PageID,
			Name:           pg.Name,
			Classification: projects.PageClassification(pg.PageClassification),
			VersionBase:    pg.VersionBase,
			VersionN:       pg.VersionN,
			PersonaHint:    pg.PersonaHint,
		})
	}
	picked := projects.PickSourcePages(classified)
	if len(picked) == 0 {
		fp.FileSkip = &PlanReason{Code: SkipNoSourcePage, Message: "no Final or version page eligible for sync"}
		return fp, nil
	}

	// 5. For each picked page, walk its sections and emit per-section plans.
	pickedByPageID := make(map[string]projects.ClassifiedPage, len(picked))
	for _, cp := range picked {
		pickedByPageID[cp.PageID] = cp
	}
	pageNameByID := make(map[string]string, len(pages))
	for _, pg := range pages {
		pageNameByID[pg.PageID] = pg.Name
	}

	for _, cp := range picked {
		sections, err := repo.ListFigmaSectionsForPage(ctx, fileKey, cp.PageID)
		if err != nil {
			return FilePlan{}, fmt.Errorf("list sections for page %s: %w", cp.PageID, err)
		}
		for _, sec := range sections {
			ps := PlannedSync{
				TenantID:         tenantID,
				FileKey:          fileKey,
				FileName:         file.Name,
				PageID:           cp.PageID,
				PageName:         cp.Name,
				SectionID:        sec.SectionID,
				Section:          sec.Name,
				PersonaHint:      cp.PersonaHint,
				Domain:           mapping.Domain,
				Product:          mapping.Product,
				LiveContentHash:  sec.ContentHash,
				LivePositionHash: sec.PositionHash,
			}
			ps.SubProduct, ps.SubFlow = projects.ParseSectionName(sec.Name)

			// Section's hashes not yet populated — same hash_not_ready treatment.
			if sec.ContentHash == "" {
				ps.Action = ActionSkipQuarantined
				ps.SkipReason = SkipHashNotReady
				fp.Sections = append(fp.Sections, ps)
				continue
			}

			// Compare to prior state row.
			prior, err := repo.LookupAutoSyncState(ctx, fileKey, cp.PageID, sec.SectionID)
			if errors.Is(err, projects.ErrNotFound) {
				ps.Action = ActionFullExport
				ps.Reason = SkipNewSection
				fp.Sections = append(fp.Sections, ps)
				continue
			}
			if err != nil {
				return FilePlan{}, fmt.Errorf("lookup state for %s/%s: %w", cp.PageID, sec.SectionID, err)
			}
			ps.PriorContentHash = prior.ContentHash
			ps.PriorPositionHash = prior.PositionHash
			ps.PriorFlowID = prior.LastSyncedFlowID
			if !prior.LastSyncedAt.IsZero() {
				ps.PriorLastSyncedAt = prior.LastSyncedAt.Format(time.RFC3339)
			}

			// Idempotent skip: prior was 'ok' AND hashes match.
			if prior.LastAttemptStatus == "ok" && prior.ContentHash == sec.ContentHash {
				if prior.PositionHash == sec.PositionHash {
					ps.Action = ActionSkipUnchanged
					ps.SkipReason = SkipAlreadySynced
				} else {
					ps.Action = ActionCheapUpdate
					ps.Reason = SkipPositionOnly
				}
				fp.Sections = append(fp.Sections, ps)
				continue
			}

			// Content changed (or prior wasn't 'ok' — retry).
			ps.Action = ActionFullExport
			if prior.ContentHash == "" {
				ps.Reason = SkipNewSection
			} else {
				ps.Reason = SkipContentChanged
			}
			fp.Sections = append(fp.Sections, ps)
		}
	}

	return fp, nil
}

// PlanTenant runs Plan() for every in-window mapped file in the tenant.
// Returns one FilePlan per file. Files that short-circuit (out_of_window,
// project_unmapped, no_source_page, file_not_found) are still in the
// result with FileSkip set — admins want to see them.
//
// Use with care: with 502 files, the inner SQL count is ~5 reads per
// file → ~2500 queries. Acceptable for an admin dry-run; not for
// per-request work.
func (p *Planner) PlanTenant(ctx context.Context, tenantID string) ([]FilePlan, error) {
	if tenantID == "" {
		return nil, errors.New("inventory: tenant_id required")
	}
	repo := p.db.NewTenantRepo(tenantID)
	files, err := repo.ListFigmaFilesForAutoSync(ctx, p.now().Add(-p.window))
	if err != nil {
		return nil, fmt.Errorf("list files: %w", err)
	}
	out := make([]FilePlan, 0, len(files))
	for _, f := range files {
		fp, err := p.Plan(ctx, tenantID, f.FileKey)
		if err != nil {
			// Plumbing failure — propagate. (Logical skips become FileSkip.)
			return nil, fmt.Errorf("plan %s: %w", f.FileKey, err)
		}
		out = append(out, fp)
	}
	return out, nil
}

// ─── Planner-only repository helpers (live on TenantRepo but kept here
//     as a forward-reference doc comment) ──────────────────────────────
//
// The planner needs three new TenantRepo methods that don't fit elsewhere:
//
//   ListFigmaPagesForFile(file_key) → []FigmaPageRowFull
//     Reads every figma_page row for a file with the classifier output
//     columns (page_classification, version_base, version_n,
//     persona_hint, content_hash, position_hash).
//
//   ListFigmaSectionsForPage(file_key, page_id) → []FigmaSectionRowFull
//     Reads every figma_section row for a page with content_hash +
//     position_hash. Live (non-deleted) only.
//
//   ListFigmaFilesForAutoSync(cutoff) → []FigmaFileRow
//     Returns figma_file rows whose project has a mapping with
//     enabled_for_autosync=1 AND last_modified >= cutoff. Used by
//     PlanTenant to enumerate candidate files in one query.
//
// All three are added in the same commit as this file (see
// repository_figma_inventory.go additions). Keeping the comment here so
// readers of the Planner can find the contract without grepping.

// joinPath helps build flows.path values when callers want the
// "Domain/Product/SubProduct/SubFlow" string for an ExportRequest. Not
// used by the Planner itself, but the CLI + Execute consumer (Phase C)
// will. Living here so it's near the parsed values it concatenates.
func JoinFlowPath(domain, product, subProduct, subFlow string) string {
	parts := []string{}
	for _, p := range []string{domain, product, subProduct, subFlow} {
		if strings.TrimSpace(p) != "" {
			parts = append(parts, strings.TrimSpace(p))
		}
	}
	return strings.Join(parts, "/")
}
