package projects

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// prd.go — U4 of docs/plans/2026-05-17-002-feat-mcp-ds-service-pm-workflow-plan.md.
//
// Persistent storage for the PRD as typed stems (KTD-3). The PRD is a
// parent row hung off sub_flow, with one prd_tab → many prd_state →
// many typed-stem child rows (acceptance criteria, edge cases, copy
// strings, Mixpanel events, a11y notes, frame tags).
//
// Shape mirrored from decisions.go:
//   - Validate*Input pure functions normalize + length-cap callers' data.
//   - Sentinel Err* exports map to HTTP statuses in the handler layer.
//   - Length caps live as package constants.
//   - Writes that touch multiple tables open a tx via beginOrJoin so the
//     caller can compose them with a parent tx (HandleExport-style).
//
// Tenant scoping discipline (per Execution Notes §B.7):
//   - Every SQL query carries an explicit WHERE tenant_id = ? predicate.
//   - Read-before-tx: lookups against the read pool happen BEFORE
//     beginOrJoin opens a write tx. The single-writer pool means a long
//     read on the write handle would serialize other writers.
//
// Binding contract (per Execution Notes §A clarification 2):
//   - The designer's frame NAME is canonical. No @role component property.
//   - prd_state.frame_name carries the designer-canonical label.
//   - frame_tag.figma_node_id is the concrete Figma node id.
//
// Mixpanel validation (per Execution Notes §A clarification 4):
//   - prd_state_event.name has no validation. Verb taxonomy is deferred to
//     the analytics team. See TODO(mixpanel) on AddEvent.

// ─── Length caps + identifier limits ────────────────────────────────────────

const (
	MaxPRDTitleLen               = 200
	MaxPRDSummaryLen             = 8000
	MaxPRDDesignNotesLen         = 8000
	MaxPRDTabNameLen             = 200
	MaxPRDTabOverviewLen         = 4000
	MaxPRDStateLabelLen          = 200
	MaxPRDStateFrameNameLen      = 400
	MaxPRDStateMarkdownLen       = 4000
	MaxAcceptanceCriterionLen    = 1000
	MaxEdgeCaseLen               = 1000
	MaxCopyStringKeyLen          = 200
	MaxCopyStringValueLen        = 4000
	MaxCopyStringLocaleLen       = 32
	MaxEventNameLen              = 200
	MaxEventPropertiesSchemaLen  = 8000
	MaxEventFiresOnLen           = 200
	MaxA11yNoteLen               = 1000
	MaxFrameTagVariantLen        = 64
	MaxPRDStateFrameTagsPerState = 100
)

// ─── Sentinel errors ────────────────────────────────────────────────────────

var (
	ErrPRDInvalidInput       = errors.New("prd: invalid input")
	ErrPRDNotFound           = errors.New("prd: not found")
	ErrPRDStateNotFound      = errors.New("prd: state not found")
	ErrPRDFrameTagNotFound   = errors.New("prd: frame tag not found")
	ErrPRDFrameTagDuplicate  = errors.New("prd: frame tag already attached")
	ErrPRDStateLabelEmpty    = errors.New("prd: state label required")
	ErrPRDStateLabelTooLong  = errors.New("prd: state label exceeds cap")
	ErrPRDEventNameEmpty     = errors.New("prd: event name required")
	ErrPRDCopyKeyEmpty       = errors.New("prd: copy_string key required")
	ErrPRDCriterionEmpty     = errors.New("prd: acceptance criterion required")
	ErrPRDEdgeCaseEmpty      = errors.New("prd: edge case required")
	ErrPRDA11yNoteEmpty      = errors.New("prd: a11y note required")
	ErrPRDTabNameEmpty       = errors.New("prd: tab name required")
)

// ─── Input structs ──────────────────────────────────────────────────────────

// PRDInput is the validated upsert payload for the parent `prd` row.
// SubFlowID is required and immutable; subsequent UpsertPRD calls with the
// same SubFlowID update title / summary / design_notes_md in place.
type PRDInput struct {
	SubFlowID     string
	Title         string
	SummaryMD     string
	DesignNotesMD string
}

// PRDTabInput — one logical tab in the PRD.
type PRDTabInput struct {
	PRDID       string
	Name        string
	Position    int
	OverviewMD  string
}

// PRDStateInput — one row in the PRD's "Possible States" table.
// FrameName is optional; U2b sets it when auto-skeleton matches a designer
// frame. Re-upserting on (PRDTabID, Label) is idempotent: it clears
// deleted_at on a soft-deleted row (restore semantics).
type PRDStateInput struct {
	PRDTabID         string
	Label            string
	Position         int
	FrameName        string // optional
	ConditionMD      string
	DesignHandlingMD string
	FEHandlingMD     string
}

// AcceptanceCriterionInput — one row of acceptance criteria.
type AcceptanceCriterionInput struct {
	PRDStateID string
	Position   int
	Criterion  string
}

// EdgeCaseInput — one edge case for a state.
type EdgeCaseInput struct {
	PRDStateID string
	Position   int
	EdgeCase   string
}

// CopyStringInput — i18n key/value/locale tuple. Idempotent on
// (PRDStateID, Key, Locale).
type CopyStringInput struct {
	PRDStateID string
	Key        string
	Value      string
	Locale     string // empty → "en"
}

// EventInput — Mixpanel tracking-plan row. Idempotent on
// (PRDStateID, Name); same name updates properties_schema + fires_on in
// place. PropertiesSchema is opaque JSON; the repo does not parse it.
type EventInput struct {
	PRDStateID        string
	Position          int
	Name              string
	PropertiesSchema  string
	FiresOn           string
}

// A11yNoteInput — one accessibility annotation.
type A11yNoteInput struct {
	PRDStateID string
	Position   int
	Note       string
}

// FrameTagInput — attach a Figma node to a PRD state.
// Variant lets the same node attach twice under different platform variants
// (android / ios / desktop). The unique index uses COALESCE(variant, '').
type FrameTagInput struct {
	PRDStateID  string
	FigmaNodeID string
	Variant     string // optional
	Position    int
}

// ─── Persisted record types ─────────────────────────────────────────────────

type PRD struct {
	ID            string    `json:"id"`
	TenantID      string    `json:"tenant_id"`
	SubFlowID     string    `json:"sub_flow_id"`
	Title         string    `json:"title"`
	SummaryMD     string    `json:"summary_md"`
	DesignNotesMD string    `json:"design_notes_md"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type PRDTab struct {
	ID         string    `json:"id"`
	TenantID   string    `json:"tenant_id"`
	PRDID      string    `json:"prd_id"`
	Name       string    `json:"name"`
	Position   int       `json:"position"`
	OverviewMD string    `json:"overview_md"`
	CreatedAt  time.Time `json:"created_at"`
}

type PRDState struct {
	ID               string     `json:"id"`
	TenantID         string     `json:"tenant_id"`
	PRDTabID         string     `json:"prd_tab_id"`
	Label            string     `json:"label"`
	Position         int        `json:"position"`
	FrameName        *string    `json:"frame_name,omitempty"`
	ConditionMD      string     `json:"condition_md"`
	DesignHandlingMD string     `json:"design_handling_md"`
	FEHandlingMD     string     `json:"fe_handling_md"`
	DeletedAt        *time.Time `json:"deleted_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

type AcceptanceCriterion struct {
	ID         string    `json:"id"`
	TenantID   string    `json:"tenant_id"`
	PRDStateID string    `json:"prd_state_id"`
	Position   int       `json:"position"`
	Criterion  string    `json:"criterion"`
	CreatedAt  time.Time `json:"created_at"`
}

type EdgeCase struct {
	ID         string    `json:"id"`
	TenantID   string    `json:"tenant_id"`
	PRDStateID string    `json:"prd_state_id"`
	Position   int       `json:"position"`
	EdgeCase   string    `json:"edge_case"`
	CreatedAt  time.Time `json:"created_at"`
}

type CopyString struct {
	ID         string    `json:"id"`
	TenantID   string    `json:"tenant_id"`
	PRDStateID string    `json:"prd_state_id"`
	Key        string    `json:"key"`
	Value      string    `json:"value"`
	Locale     string    `json:"locale"`
	CreatedAt  time.Time `json:"created_at"`
}

type Event struct {
	ID                string    `json:"id"`
	TenantID          string    `json:"tenant_id"`
	PRDStateID        string    `json:"prd_state_id"`
	Position          int       `json:"position"`
	Name              string    `json:"name"`
	PropertiesSchema  string    `json:"properties_schema"`
	FiresOn           string    `json:"fires_on"`
	CreatedAt         time.Time `json:"created_at"`
}

type A11yNote struct {
	ID         string    `json:"id"`
	TenantID   string    `json:"tenant_id"`
	PRDStateID string    `json:"prd_state_id"`
	Position   int       `json:"position"`
	Note       string    `json:"note"`
	CreatedAt  time.Time `json:"created_at"`
}

type FrameTag struct {
	ID          string    `json:"id"`
	TenantID    string    `json:"tenant_id"`
	PRDStateID  string    `json:"prd_state_id"`
	FigmaNodeID string    `json:"figma_node_id"`
	Variant     *string   `json:"variant,omitempty"`
	Position    int       `json:"position"`
	CreatedAt   time.Time `json:"created_at"`
}

// PRDStateFull is one prd_state with all of its typed stems and frame tags
// pre-loaded. Returned by LoadPRD inside PRDTabFull.
type PRDStateFull struct {
	PRDState
	AcceptanceCriteria []AcceptanceCriterion `json:"acceptance_criteria,omitempty"`
	EdgeCases          []EdgeCase            `json:"edge_cases,omitempty"`
	CopyStrings        []CopyString          `json:"copy_strings,omitempty"`
	Events             []Event               `json:"events,omitempty"`
	A11yNotes          []A11yNote            `json:"a11y_notes,omitempty"`
	FrameTags          []FrameTag            `json:"frame_tags,omitempty"`
}

// PRDTabFull bundles a tab with its (live) states ordered by position.
type PRDTabFull struct {
	PRDTab
	States []PRDStateFull `json:"states,omitempty"`
}

// PRDFull is the deeply-nested read shape returned by LoadPRD. Soft-deleted
// states are excluded; tabs are returned in (position, created_at) order.
type PRDFull struct {
	PRD
	Tabs []PRDTabFull `json:"tabs,omitempty"`
}

// ─── Validators ─────────────────────────────────────────────────────────────

func validatePRDInput(in PRDInput) (PRDInput, error) {
	out := PRDInput{
		SubFlowID:     strings.TrimSpace(in.SubFlowID),
		Title:         strings.TrimSpace(in.Title),
		SummaryMD:     in.SummaryMD,
		DesignNotesMD: in.DesignNotesMD,
	}
	if out.SubFlowID == "" {
		return PRDInput{}, fmt.Errorf("%w: sub_flow_id required", ErrPRDInvalidInput)
	}
	if len(out.Title) > MaxPRDTitleLen {
		return PRDInput{}, fmt.Errorf("%w: title exceeds %d chars", ErrPRDInvalidInput, MaxPRDTitleLen)
	}
	if len(out.SummaryMD) > MaxPRDSummaryLen {
		return PRDInput{}, fmt.Errorf("%w: summary_md exceeds %d chars", ErrPRDInvalidInput, MaxPRDSummaryLen)
	}
	if len(out.DesignNotesMD) > MaxPRDDesignNotesLen {
		return PRDInput{}, fmt.Errorf("%w: design_notes_md exceeds %d chars", ErrPRDInvalidInput, MaxPRDDesignNotesLen)
	}
	return out, nil
}

func validatePRDTabInput(in PRDTabInput) (PRDTabInput, error) {
	out := PRDTabInput{
		PRDID:      strings.TrimSpace(in.PRDID),
		Name:       strings.TrimSpace(in.Name),
		Position:   in.Position,
		OverviewMD: in.OverviewMD,
	}
	if out.PRDID == "" {
		return PRDTabInput{}, fmt.Errorf("%w: prd_id required", ErrPRDInvalidInput)
	}
	if out.Name == "" {
		return PRDTabInput{}, ErrPRDTabNameEmpty
	}
	if len(out.Name) > MaxPRDTabNameLen {
		return PRDTabInput{}, fmt.Errorf("%w: tab name exceeds %d chars", ErrPRDInvalidInput, MaxPRDTabNameLen)
	}
	if len(out.OverviewMD) > MaxPRDTabOverviewLen {
		return PRDTabInput{}, fmt.Errorf("%w: overview_md exceeds %d chars", ErrPRDInvalidInput, MaxPRDTabOverviewLen)
	}
	return out, nil
}

func validatePRDStateInput(in PRDStateInput) (PRDStateInput, error) {
	out := PRDStateInput{
		PRDTabID:         strings.TrimSpace(in.PRDTabID),
		Label:            strings.TrimSpace(in.Label),
		Position:         in.Position,
		FrameName:        strings.TrimSpace(in.FrameName),
		ConditionMD:      in.ConditionMD,
		DesignHandlingMD: in.DesignHandlingMD,
		FEHandlingMD:     in.FEHandlingMD,
	}
	if out.PRDTabID == "" {
		return PRDStateInput{}, fmt.Errorf("%w: prd_tab_id required", ErrPRDInvalidInput)
	}
	if out.Label == "" {
		return PRDStateInput{}, ErrPRDStateLabelEmpty
	}
	if len(out.Label) > MaxPRDStateLabelLen {
		return PRDStateInput{}, ErrPRDStateLabelTooLong
	}
	if len(out.FrameName) > MaxPRDStateFrameNameLen {
		return PRDStateInput{}, fmt.Errorf("%w: frame_name exceeds %d chars", ErrPRDInvalidInput, MaxPRDStateFrameNameLen)
	}
	if len(out.ConditionMD) > MaxPRDStateMarkdownLen {
		return PRDStateInput{}, fmt.Errorf("%w: condition_md exceeds %d chars", ErrPRDInvalidInput, MaxPRDStateMarkdownLen)
	}
	if len(out.DesignHandlingMD) > MaxPRDStateMarkdownLen {
		return PRDStateInput{}, fmt.Errorf("%w: design_handling_md exceeds %d chars", ErrPRDInvalidInput, MaxPRDStateMarkdownLen)
	}
	if len(out.FEHandlingMD) > MaxPRDStateMarkdownLen {
		return PRDStateInput{}, fmt.Errorf("%w: fe_handling_md exceeds %d chars", ErrPRDInvalidInput, MaxPRDStateMarkdownLen)
	}
	return out, nil
}

func validateAcceptanceCriterionInput(in AcceptanceCriterionInput) (AcceptanceCriterionInput, error) {
	out := AcceptanceCriterionInput{
		PRDStateID: strings.TrimSpace(in.PRDStateID),
		Position:   in.Position,
		Criterion:  strings.TrimSpace(in.Criterion),
	}
	if out.PRDStateID == "" {
		return AcceptanceCriterionInput{}, fmt.Errorf("%w: prd_state_id required", ErrPRDInvalidInput)
	}
	if out.Criterion == "" {
		return AcceptanceCriterionInput{}, ErrPRDCriterionEmpty
	}
	if len(out.Criterion) > MaxAcceptanceCriterionLen {
		return AcceptanceCriterionInput{}, fmt.Errorf("%w: criterion exceeds %d chars", ErrPRDInvalidInput, MaxAcceptanceCriterionLen)
	}
	return out, nil
}

func validateEdgeCaseInput(in EdgeCaseInput) (EdgeCaseInput, error) {
	out := EdgeCaseInput{
		PRDStateID: strings.TrimSpace(in.PRDStateID),
		Position:   in.Position,
		EdgeCase:   strings.TrimSpace(in.EdgeCase),
	}
	if out.PRDStateID == "" {
		return EdgeCaseInput{}, fmt.Errorf("%w: prd_state_id required", ErrPRDInvalidInput)
	}
	if out.EdgeCase == "" {
		return EdgeCaseInput{}, ErrPRDEdgeCaseEmpty
	}
	if len(out.EdgeCase) > MaxEdgeCaseLen {
		return EdgeCaseInput{}, fmt.Errorf("%w: edge_case exceeds %d chars", ErrPRDInvalidInput, MaxEdgeCaseLen)
	}
	return out, nil
}

func validateCopyStringInput(in CopyStringInput) (CopyStringInput, error) {
	out := CopyStringInput{
		PRDStateID: strings.TrimSpace(in.PRDStateID),
		Key:        strings.TrimSpace(in.Key),
		Value:      in.Value,
		Locale:     strings.TrimSpace(in.Locale),
	}
	if out.PRDStateID == "" {
		return CopyStringInput{}, fmt.Errorf("%w: prd_state_id required", ErrPRDInvalidInput)
	}
	if out.Key == "" {
		return CopyStringInput{}, ErrPRDCopyKeyEmpty
	}
	if len(out.Key) > MaxCopyStringKeyLen {
		return CopyStringInput{}, fmt.Errorf("%w: copy key exceeds %d chars", ErrPRDInvalidInput, MaxCopyStringKeyLen)
	}
	if len(out.Value) > MaxCopyStringValueLen {
		return CopyStringInput{}, fmt.Errorf("%w: copy value exceeds %d chars", ErrPRDInvalidInput, MaxCopyStringValueLen)
	}
	if out.Locale == "" {
		out.Locale = "en"
	}
	if len(out.Locale) > MaxCopyStringLocaleLen {
		return CopyStringInput{}, fmt.Errorf("%w: locale exceeds %d chars", ErrPRDInvalidInput, MaxCopyStringLocaleLen)
	}
	return out, nil
}

func validateEventInput(in EventInput) (EventInput, error) {
	// TODO(mixpanel): validate name shape once analytics team defines the
	// verb taxonomy. Per Execution Notes §A clarification 4, no validation
	// today beyond non-empty + length cap.
	out := EventInput{
		PRDStateID:       strings.TrimSpace(in.PRDStateID),
		Position:         in.Position,
		Name:             strings.TrimSpace(in.Name),
		PropertiesSchema: in.PropertiesSchema,
		FiresOn:          strings.TrimSpace(in.FiresOn),
	}
	if out.PRDStateID == "" {
		return EventInput{}, fmt.Errorf("%w: prd_state_id required", ErrPRDInvalidInput)
	}
	if out.Name == "" {
		return EventInput{}, ErrPRDEventNameEmpty
	}
	if len(out.Name) > MaxEventNameLen {
		return EventInput{}, fmt.Errorf("%w: event name exceeds %d chars", ErrPRDInvalidInput, MaxEventNameLen)
	}
	if out.PropertiesSchema == "" {
		out.PropertiesSchema = "{}"
	}
	if len(out.PropertiesSchema) > MaxEventPropertiesSchemaLen {
		return EventInput{}, fmt.Errorf("%w: properties_schema exceeds %d chars", ErrPRDInvalidInput, MaxEventPropertiesSchemaLen)
	}
	if len(out.FiresOn) > MaxEventFiresOnLen {
		return EventInput{}, fmt.Errorf("%w: fires_on exceeds %d chars", ErrPRDInvalidInput, MaxEventFiresOnLen)
	}
	return out, nil
}

func validateA11yNoteInput(in A11yNoteInput) (A11yNoteInput, error) {
	out := A11yNoteInput{
		PRDStateID: strings.TrimSpace(in.PRDStateID),
		Position:   in.Position,
		Note:       strings.TrimSpace(in.Note),
	}
	if out.PRDStateID == "" {
		return A11yNoteInput{}, fmt.Errorf("%w: prd_state_id required", ErrPRDInvalidInput)
	}
	if out.Note == "" {
		return A11yNoteInput{}, ErrPRDA11yNoteEmpty
	}
	if len(out.Note) > MaxA11yNoteLen {
		return A11yNoteInput{}, fmt.Errorf("%w: a11y note exceeds %d chars", ErrPRDInvalidInput, MaxA11yNoteLen)
	}
	return out, nil
}

func validateFrameTagInput(in FrameTagInput) (FrameTagInput, error) {
	out := FrameTagInput{
		PRDStateID:  strings.TrimSpace(in.PRDStateID),
		FigmaNodeID: strings.TrimSpace(in.FigmaNodeID),
		Variant:     strings.TrimSpace(in.Variant),
		Position:    in.Position,
	}
	if out.PRDStateID == "" {
		return FrameTagInput{}, fmt.Errorf("%w: prd_state_id required", ErrPRDInvalidInput)
	}
	if out.FigmaNodeID == "" {
		return FrameTagInput{}, fmt.Errorf("%w: figma_node_id required", ErrPRDInvalidInput)
	}
	if len(out.Variant) > MaxFrameTagVariantLen {
		return FrameTagInput{}, fmt.Errorf("%w: variant exceeds %d chars", ErrPRDInvalidInput, MaxFrameTagVariantLen)
	}
	return out, nil
}

// ─── Repo methods ───────────────────────────────────────────────────────────

// UpsertPRD creates or updates the parent prd row keyed by sub_flow_id.
// Idempotent: a second call with the same SubFlowID returns the same id
// and updates title / summary / design_notes in place.
func (t *TenantRepo) UpsertPRD(ctx context.Context, in PRDInput) (PRD, error) {
	if t.tenantID == "" {
		return PRD{}, errors.New("prd: tenant_id required")
	}
	v, err := validatePRDInput(in)
	if err != nil {
		return PRD{}, err
	}

	// Read-before-tx: look up the existing row against the read handle.
	existing, err := t.getPRDBySubFlow(ctx, v.SubFlowID, t.readHandle())
	if err != nil && !errors.Is(err, ErrPRDNotFound) {
		return PRD{}, err
	}

	now := t.now().UTC()
	if err == nil {
		// Update in place.
		if _, uerr := t.handle().ExecContext(ctx,
			`UPDATE prd
			    SET title = ?, summary_md = ?, design_notes_md = ?, updated_at = ?
			  WHERE tenant_id = ? AND id = ?`,
			v.Title, v.SummaryMD, v.DesignNotesMD, rfc3339(now),
			t.tenantID, existing.ID,
		); uerr != nil {
			return PRD{}, fmt.Errorf("update prd: %w", uerr)
		}
		existing.Title = v.Title
		existing.SummaryMD = v.SummaryMD
		existing.DesignNotesMD = v.DesignNotesMD
		existing.UpdatedAt = now
		return existing, nil
	}

	// Insert.
	id := uuid.NewString()
	if _, ierr := t.handle().ExecContext(ctx,
		`INSERT INTO prd (id, tenant_id, sub_flow_id, title, summary_md, design_notes_md, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, t.tenantID, v.SubFlowID, v.Title, v.SummaryMD, v.DesignNotesMD, rfc3339(now), rfc3339(now),
	); ierr != nil {
		if strings.Contains(ierr.Error(), "UNIQUE") {
			// Raced with a concurrent writer; re-read via write handle.
			return t.getPRDBySubFlow(ctx, v.SubFlowID, t.handle())
		}
		return PRD{}, fmt.Errorf("insert prd: %w", ierr)
	}
	return PRD{
		ID:            id,
		TenantID:      t.tenantID,
		SubFlowID:     v.SubFlowID,
		Title:         v.Title,
		SummaryMD:     v.SummaryMD,
		DesignNotesMD: v.DesignNotesMD,
		CreatedAt:     now,
		UpdatedAt:     now,
	}, nil
}

// UpsertPRDTab creates or updates a tab keyed by (prd_id, LOWER(TRIM(name))).
// Re-running with the same name returns the same id; position +
// overview_md update in place.
func (t *TenantRepo) UpsertPRDTab(ctx context.Context, in PRDTabInput) (PRDTab, error) {
	if t.tenantID == "" {
		return PRDTab{}, errors.New("prd_tab: tenant_id required")
	}
	v, err := validatePRDTabInput(in)
	if err != nil {
		return PRDTab{}, err
	}
	key := normalizeName(v.Name)

	existing, err := t.getPRDTabByName(ctx, v.PRDID, key, t.readHandle())
	if err != nil && !errors.Is(err, ErrNotFound) {
		return PRDTab{}, err
	}

	now := t.now().UTC()
	if err == nil {
		if _, uerr := t.handle().ExecContext(ctx,
			`UPDATE prd_tab
			    SET name = ?, position = ?, overview_md = ?
			  WHERE tenant_id = ? AND id = ?`,
			v.Name, v.Position, v.OverviewMD,
			t.tenantID, existing.ID,
		); uerr != nil {
			return PRDTab{}, fmt.Errorf("update prd_tab: %w", uerr)
		}
		existing.Name = v.Name
		existing.Position = v.Position
		existing.OverviewMD = v.OverviewMD
		return existing, nil
	}

	id := uuid.NewString()
	if _, ierr := t.handle().ExecContext(ctx,
		`INSERT INTO prd_tab (id, tenant_id, prd_id, name, position, overview_md, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, t.tenantID, v.PRDID, v.Name, v.Position, v.OverviewMD, rfc3339(now),
	); ierr != nil {
		if strings.Contains(ierr.Error(), "UNIQUE") {
			return t.getPRDTabByName(ctx, v.PRDID, key, t.handle())
		}
		return PRDTab{}, fmt.Errorf("insert prd_tab: %w", ierr)
	}
	return PRDTab{
		ID:         id,
		TenantID:   t.tenantID,
		PRDID:      v.PRDID,
		Name:       v.Name,
		Position:   v.Position,
		OverviewMD: v.OverviewMD,
		CreatedAt:  now,
	}, nil
}

// UpsertPRDState creates or updates a state keyed by (prd_tab_id,
// LOWER(TRIM(label))). Idempotent restore: if the matching row is soft-
// deleted (deleted_at IS NOT NULL), this call clears deleted_at and
// updates the rest in place — authored stems (criteria, events, copy)
// are preserved.
//
// NOTE for U2b: idempotency key is (prd_tab_id, label). U2b can call this
// in a loop per Figma frame with the frame's name as `Label` and rely on
// the same id surviving rename-then-restore cycles.
func (t *TenantRepo) UpsertPRDState(ctx context.Context, in PRDStateInput) (PRDState, error) {
	if t.tenantID == "" {
		return PRDState{}, errors.New("prd_state: tenant_id required")
	}
	v, err := validatePRDStateInput(in)
	if err != nil {
		return PRDState{}, err
	}
	key := normalizeName(v.Label)

	// Look for live AND soft-deleted rows; restoration is in scope.
	existing, err := t.getPRDStateByLabelIncludingDeleted(ctx, v.PRDTabID, key, t.readHandle())
	if err != nil && !errors.Is(err, ErrNotFound) {
		return PRDState{}, err
	}

	now := t.now().UTC()
	if err == nil {
		// Update + clear deleted_at (idempotent restore).
		if _, uerr := t.handle().ExecContext(ctx,
			`UPDATE prd_state
			    SET label = ?, position = ?, frame_name = ?,
			        condition_md = ?, design_handling_md = ?, fe_handling_md = ?,
			        deleted_at = NULL, updated_at = ?
			  WHERE tenant_id = ? AND id = ?`,
			v.Label, v.Position, nullIfEmpty(v.FrameName),
			v.ConditionMD, v.DesignHandlingMD, v.FEHandlingMD,
			rfc3339(now),
			t.tenantID, existing.ID,
		); uerr != nil {
			return PRDState{}, fmt.Errorf("update prd_state: %w", uerr)
		}
		existing.Label = v.Label
		existing.Position = v.Position
		if v.FrameName == "" {
			existing.FrameName = nil
		} else {
			fn := v.FrameName
			existing.FrameName = &fn
		}
		existing.ConditionMD = v.ConditionMD
		existing.DesignHandlingMD = v.DesignHandlingMD
		existing.FEHandlingMD = v.FEHandlingMD
		existing.DeletedAt = nil
		existing.UpdatedAt = now
		return existing, nil
	}

	id := uuid.NewString()
	if _, ierr := t.handle().ExecContext(ctx,
		`INSERT INTO prd_state
		 (id, tenant_id, prd_tab_id, label, position, frame_name,
		  condition_md, design_handling_md, fe_handling_md,
		  created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, t.tenantID, v.PRDTabID, v.Label, v.Position, nullIfEmpty(v.FrameName),
		v.ConditionMD, v.DesignHandlingMD, v.FEHandlingMD,
		rfc3339(now), rfc3339(now),
	); ierr != nil {
		if strings.Contains(ierr.Error(), "UNIQUE") {
			return t.getPRDStateByLabelIncludingDeleted(ctx, v.PRDTabID, key, t.handle())
		}
		return PRDState{}, fmt.Errorf("insert prd_state: %w", ierr)
	}
	state := PRDState{
		ID:               id,
		TenantID:         t.tenantID,
		PRDTabID:         v.PRDTabID,
		Label:            v.Label,
		Position:         v.Position,
		ConditionMD:      v.ConditionMD,
		DesignHandlingMD: v.DesignHandlingMD,
		FEHandlingMD:     v.FEHandlingMD,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if v.FrameName != "" {
		fn := v.FrameName
		state.FrameName = &fn
	}
	return state, nil
}

// SoftDeletePRDState marks the row as deleted. Authored stems remain in
// place; LoadPRD excludes the row from its read shape.
func (t *TenantRepo) SoftDeletePRDState(ctx context.Context, stateID string) error {
	if t.tenantID == "" {
		return errors.New("prd_state: tenant_id required")
	}
	if stateID == "" {
		return ErrPRDStateNotFound
	}
	now := t.now().UTC()
	res, err := t.handle().ExecContext(ctx,
		`UPDATE prd_state
		    SET deleted_at = ?, updated_at = ?
		  WHERE tenant_id = ? AND id = ? AND deleted_at IS NULL`,
		rfc3339(now), rfc3339(now), t.tenantID, stateID,
	)
	if err != nil {
		return fmt.Errorf("soft-delete prd_state: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrPRDStateNotFound
	}
	return nil
}

// RestorePRDState clears deleted_at on a soft-deleted state row.
// Equivalent to UpsertPRDState with the same label, but lets callers
// avoid recomputing input fields.
func (t *TenantRepo) RestorePRDState(ctx context.Context, stateID string) error {
	if t.tenantID == "" {
		return errors.New("prd_state: tenant_id required")
	}
	if stateID == "" {
		return ErrPRDStateNotFound
	}
	now := t.now().UTC()
	res, err := t.handle().ExecContext(ctx,
		`UPDATE prd_state
		    SET deleted_at = NULL, updated_at = ?
		  WHERE tenant_id = ? AND id = ? AND deleted_at IS NOT NULL`,
		rfc3339(now), t.tenantID, stateID,
	)
	if err != nil {
		return fmt.Errorf("restore prd_state: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrPRDStateNotFound
	}
	return nil
}

// AddAcceptanceCriterion inserts a new acceptance criterion row.
// Always creates a new row — no idempotency key (multiple criteria per
// state are the common case). Tests verify position ordering survives
// round-trip.
func (t *TenantRepo) AddAcceptanceCriterion(ctx context.Context, in AcceptanceCriterionInput) (AcceptanceCriterion, error) {
	if t.tenantID == "" {
		return AcceptanceCriterion{}, errors.New("prd_state_acceptance_criterion: tenant_id required")
	}
	v, err := validateAcceptanceCriterionInput(in)
	if err != nil {
		return AcceptanceCriterion{}, err
	}
	id := uuid.NewString()
	now := t.now().UTC()
	if _, ierr := t.handle().ExecContext(ctx,
		`INSERT INTO prd_state_acceptance_criterion
		 (id, tenant_id, prd_state_id, position, criterion, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		id, t.tenantID, v.PRDStateID, v.Position, v.Criterion, rfc3339(now),
	); ierr != nil {
		return AcceptanceCriterion{}, fmt.Errorf("insert acceptance criterion: %w", ierr)
	}
	return AcceptanceCriterion{
		ID:         id,
		TenantID:   t.tenantID,
		PRDStateID: v.PRDStateID,
		Position:   v.Position,
		Criterion:  v.Criterion,
		CreatedAt:  now,
	}, nil
}

// AddEdgeCase inserts a new edge_case row.
func (t *TenantRepo) AddEdgeCase(ctx context.Context, in EdgeCaseInput) (EdgeCase, error) {
	if t.tenantID == "" {
		return EdgeCase{}, errors.New("prd_state_edge_case: tenant_id required")
	}
	v, err := validateEdgeCaseInput(in)
	if err != nil {
		return EdgeCase{}, err
	}
	id := uuid.NewString()
	now := t.now().UTC()
	if _, ierr := t.handle().ExecContext(ctx,
		`INSERT INTO prd_state_edge_case
		 (id, tenant_id, prd_state_id, position, edge_case, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		id, t.tenantID, v.PRDStateID, v.Position, v.EdgeCase, rfc3339(now),
	); ierr != nil {
		return EdgeCase{}, fmt.Errorf("insert edge_case: %w", ierr)
	}
	return EdgeCase{
		ID:         id,
		TenantID:   t.tenantID,
		PRDStateID: v.PRDStateID,
		Position:   v.Position,
		EdgeCase:   v.EdgeCase,
		CreatedAt:  now,
	}, nil
}

// UpsertCopyString writes a copy_string row keyed by
// (prd_state_id, key, locale). Re-running with the same key+locale
// updates value in place.
func (t *TenantRepo) UpsertCopyString(ctx context.Context, in CopyStringInput) (CopyString, error) {
	if t.tenantID == "" {
		return CopyString{}, errors.New("prd_state_copy_string: tenant_id required")
	}
	v, err := validateCopyStringInput(in)
	if err != nil {
		return CopyString{}, err
	}

	existing, err := t.getCopyStringByKey(ctx, v.PRDStateID, v.Key, v.Locale, t.readHandle())
	if err != nil && !errors.Is(err, ErrNotFound) {
		return CopyString{}, err
	}
	now := t.now().UTC()
	if err == nil {
		if _, uerr := t.handle().ExecContext(ctx,
			`UPDATE prd_state_copy_string
			    SET value = ?
			  WHERE tenant_id = ? AND id = ?`,
			v.Value, t.tenantID, existing.ID,
		); uerr != nil {
			return CopyString{}, fmt.Errorf("update copy_string: %w", uerr)
		}
		existing.Value = v.Value
		return existing, nil
	}

	id := uuid.NewString()
	if _, ierr := t.handle().ExecContext(ctx,
		`INSERT INTO prd_state_copy_string
		 (id, tenant_id, prd_state_id, key, value, locale, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, t.tenantID, v.PRDStateID, v.Key, v.Value, v.Locale, rfc3339(now),
	); ierr != nil {
		if strings.Contains(ierr.Error(), "UNIQUE") {
			return t.getCopyStringByKey(ctx, v.PRDStateID, v.Key, v.Locale, t.handle())
		}
		return CopyString{}, fmt.Errorf("insert copy_string: %w", ierr)
	}
	return CopyString{
		ID:         id,
		TenantID:   t.tenantID,
		PRDStateID: v.PRDStateID,
		Key:        v.Key,
		Value:      v.Value,
		Locale:     v.Locale,
		CreatedAt:  now,
	}, nil
}

// AddEvent writes a prd_state_event row. Idempotent on
// (prd_state_id, name): same name updates properties_schema +
// fires_on + position in place (no duplicate-event surprise).
//
// TODO(mixpanel): validate name shape once analytics team defines the
// verb taxonomy. Today any non-empty string up to MaxEventNameLen is
// accepted.
func (t *TenantRepo) AddEvent(ctx context.Context, in EventInput) (Event, error) {
	if t.tenantID == "" {
		return Event{}, errors.New("prd_state_event: tenant_id required")
	}
	v, err := validateEventInput(in)
	if err != nil {
		return Event{}, err
	}

	existing, err := t.getEventByName(ctx, v.PRDStateID, v.Name, t.readHandle())
	if err != nil && !errors.Is(err, ErrNotFound) {
		return Event{}, err
	}
	now := t.now().UTC()
	if err == nil {
		if _, uerr := t.handle().ExecContext(ctx,
			`UPDATE prd_state_event
			    SET position = ?, properties_schema = ?, fires_on = ?
			  WHERE tenant_id = ? AND id = ?`,
			v.Position, v.PropertiesSchema, v.FiresOn,
			t.tenantID, existing.ID,
		); uerr != nil {
			return Event{}, fmt.Errorf("update event: %w", uerr)
		}
		existing.Position = v.Position
		existing.PropertiesSchema = v.PropertiesSchema
		existing.FiresOn = v.FiresOn
		return existing, nil
	}

	id := uuid.NewString()
	if _, ierr := t.handle().ExecContext(ctx,
		`INSERT INTO prd_state_event
		 (id, tenant_id, prd_state_id, position, name, properties_schema, fires_on, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, t.tenantID, v.PRDStateID, v.Position, v.Name, v.PropertiesSchema, v.FiresOn, rfc3339(now),
	); ierr != nil {
		if strings.Contains(ierr.Error(), "UNIQUE") {
			return t.getEventByName(ctx, v.PRDStateID, v.Name, t.handle())
		}
		return Event{}, fmt.Errorf("insert event: %w", ierr)
	}
	return Event{
		ID:               id,
		TenantID:         t.tenantID,
		PRDStateID:       v.PRDStateID,
		Position:         v.Position,
		Name:             v.Name,
		PropertiesSchema: v.PropertiesSchema,
		FiresOn:          v.FiresOn,
		CreatedAt:        now,
	}, nil
}

// AddA11yNote inserts a new a11y note row.
func (t *TenantRepo) AddA11yNote(ctx context.Context, in A11yNoteInput) (A11yNote, error) {
	if t.tenantID == "" {
		return A11yNote{}, errors.New("prd_state_a11y_note: tenant_id required")
	}
	v, err := validateA11yNoteInput(in)
	if err != nil {
		return A11yNote{}, err
	}
	id := uuid.NewString()
	now := t.now().UTC()
	if _, ierr := t.handle().ExecContext(ctx,
		`INSERT INTO prd_state_a11y_note
		 (id, tenant_id, prd_state_id, position, note, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		id, t.tenantID, v.PRDStateID, v.Position, v.Note, rfc3339(now),
	); ierr != nil {
		return A11yNote{}, fmt.Errorf("insert a11y_note: %w", ierr)
	}
	return A11yNote{
		ID:         id,
		TenantID:   t.tenantID,
		PRDStateID: v.PRDStateID,
		Position:   v.Position,
		Note:       v.Note,
		CreatedAt:  now,
	}, nil
}

// AttachFrameTag binds a Figma node to a PRD state. The unique index uses
// COALESCE(variant, '') so the same node can attach to the same state
// twice under different platform variants (android / ios / desktop).
//
// Returns ErrPRDFrameTagDuplicate when (state, node, variant) already
// exists — letting the caller distinguish "wrong call" from real DB errors.
func (t *TenantRepo) AttachFrameTag(ctx context.Context, in FrameTagInput) (FrameTag, error) {
	if t.tenantID == "" {
		return FrameTag{}, errors.New("frame_tag: tenant_id required")
	}
	v, err := validateFrameTagInput(in)
	if err != nil {
		return FrameTag{}, err
	}
	id := uuid.NewString()
	now := t.now().UTC()
	if _, ierr := t.handle().ExecContext(ctx,
		`INSERT INTO frame_tag
		 (id, tenant_id, prd_state_id, figma_node_id, variant, position, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, t.tenantID, v.PRDStateID, v.FigmaNodeID, nullIfEmpty(v.Variant), v.Position, rfc3339(now),
	); ierr != nil {
		if strings.Contains(ierr.Error(), "UNIQUE") {
			return FrameTag{}, ErrPRDFrameTagDuplicate
		}
		return FrameTag{}, fmt.Errorf("insert frame_tag: %w", ierr)
	}
	tag := FrameTag{
		ID:          id,
		TenantID:    t.tenantID,
		PRDStateID:  v.PRDStateID,
		FigmaNodeID: v.FigmaNodeID,
		Position:    v.Position,
		CreatedAt:   now,
	}
	if v.Variant != "" {
		vv := v.Variant
		tag.Variant = &vv
	}
	return tag, nil
}

// DetachFrameTag removes a frame_tag row by id.
func (t *TenantRepo) DetachFrameTag(ctx context.Context, tagID string) error {
	if t.tenantID == "" {
		return errors.New("frame_tag: tenant_id required")
	}
	if tagID == "" {
		return ErrPRDFrameTagNotFound
	}
	res, err := t.handle().ExecContext(ctx,
		`DELETE FROM frame_tag WHERE tenant_id = ? AND id = ?`,
		t.tenantID, tagID,
	)
	if err != nil {
		return fmt.Errorf("delete frame_tag: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrPRDFrameTagNotFound
	}
	return nil
}

// LoadPRD returns the deeply-nested read shape for one PRD, keyed by
// sub_flow_id. Soft-deleted states are excluded. Tabs and states are
// ordered by (position, created_at). Typed stems are ordered by
// (position, created_at) inside each state.
//
// All queries use the read pool; the load is a single read-only flow with
// no writes.
func (t *TenantRepo) LoadPRD(ctx context.Context, subFlowID string) (PRDFull, error) {
	if t.tenantID == "" {
		return PRDFull{}, errors.New("prd: tenant_id required")
	}
	subFlowID = strings.TrimSpace(subFlowID)
	if subFlowID == "" {
		return PRDFull{}, ErrPRDNotFound
	}

	prd, err := t.getPRDBySubFlow(ctx, subFlowID, t.readHandle())
	if err != nil {
		return PRDFull{}, err
	}
	full := PRDFull{PRD: prd}

	// Tabs.
	tabRows, err := t.readHandle().QueryContext(ctx,
		`SELECT id, tenant_id, prd_id, name, position, overview_md, created_at
		   FROM prd_tab
		  WHERE tenant_id = ? AND prd_id = ?
		  ORDER BY position ASC, created_at ASC`,
		t.tenantID, prd.ID,
	)
	if err != nil {
		return PRDFull{}, fmt.Errorf("load prd_tab: %w", err)
	}
	defer tabRows.Close()
	tabsByID := make(map[string]*PRDTabFull)
	tabOrder := make([]string, 0)
	for tabRows.Next() {
		tab, scanErr := scanPRDTab(tabRows)
		if scanErr != nil {
			return PRDFull{}, scanErr
		}
		full.Tabs = append(full.Tabs, PRDTabFull{PRDTab: tab})
		tabsByID[tab.ID] = &full.Tabs[len(full.Tabs)-1]
		tabOrder = append(tabOrder, tab.ID)
	}
	if err := tabRows.Err(); err != nil {
		return PRDFull{}, fmt.Errorf("iterate prd_tab: %w", err)
	}

	if len(full.Tabs) == 0 {
		return full, nil
	}

	// Collect tab ids for the per-stem fan-out queries.
	tabIDs := tabOrder

	// States — exclude soft-deleted.
	states, err := t.loadStatesForTabs(ctx, tabIDs)
	if err != nil {
		return PRDFull{}, err
	}
	if len(states) == 0 {
		return full, nil
	}

	// Build state holders FIRST, off-tree, so we can attach stems via stable
	// pointers. The PRDStateFull pointers stay valid for the duration of the
	// load — only at the end do we copy values into the tab.States slices.
	// (Appending into a slice invalidates element pointers once the backing
	// array grows, so we cannot use tab.States[len-1] as a long-lived ptr.)
	stateByID := make(map[string]*PRDStateFull, len(states))
	stateIDs := make([]string, 0, len(states))
	statesByTab := make(map[string][]*PRDStateFull, len(tabIDs))
	for _, s := range states {
		holder := &PRDStateFull{PRDState: s}
		stateByID[s.ID] = holder
		stateIDs = append(stateIDs, s.ID)
		statesByTab[s.PRDTabID] = append(statesByTab[s.PRDTabID], holder)
	}

	// Stems: one fan-out query per stem table. Each appends into the
	// PRDStateFull holder via stateByID — pointers stay stable.
	if err := t.loadAcceptanceCriteria(ctx, stateIDs, stateByID); err != nil {
		return PRDFull{}, err
	}
	if err := t.loadEdgeCases(ctx, stateIDs, stateByID); err != nil {
		return PRDFull{}, err
	}
	if err := t.loadCopyStrings(ctx, stateIDs, stateByID); err != nil {
		return PRDFull{}, err
	}
	if err := t.loadEvents(ctx, stateIDs, stateByID); err != nil {
		return PRDFull{}, err
	}
	if err := t.loadA11yNotes(ctx, stateIDs, stateByID); err != nil {
		return PRDFull{}, err
	}
	if err := t.loadFrameTags(ctx, stateIDs, stateByID); err != nil {
		return PRDFull{}, err
	}

	// Materialize: copy the assembled holders into each tab's States slice in
	// the order the state load returned them (already sorted by tab+position).
	for tabID, holders := range statesByTab {
		tab := tabsByID[tabID]
		if tab == nil {
			continue
		}
		tab.States = make([]PRDStateFull, len(holders))
		for i, h := range holders {
			tab.States[i] = *h
		}
	}

	return full, nil
}

// PRDExport is the result of an export — markdown for human reading,
// sidecar for downstream tools (Storybook / Playwright / Mixpanel /
// JIRA generators) that want structured data without re-parsing
// markdown.
//
// The sidecar IS PRDFull serialized — no separate type, no
// transformation. The snake_case JSON tags on PRDFull (and the typed
// stems it embeds) are the canonical wire shape. Schema changes to
// PRDFull flow through to the sidecar automatically (plan KTD-4).
type PRDExport struct {
	Markdown string  `json:"markdown"`
	Sidecar  PRDFull `json:"sidecar"`
}

// RenderPRDExport returns the rendered PRD as both markdown and a
// typed sidecar. Loads the PRD once via LoadPRD, then renders markdown
// from the loaded tree — no second query, no schema drift between the
// two outputs.
func (t *TenantRepo) RenderPRDExport(ctx context.Context, subFlowID string) (PRDExport, error) {
	full, err := t.LoadPRD(ctx, subFlowID)
	if err != nil {
		return PRDExport{}, err
	}
	return PRDExport{
		Markdown: renderPRDFullToMarkdown(full),
		Sidecar:  full,
	}, nil
}

// RenderPRDMarkdown produces a deterministic markdown rendering of the PRD.
// Pure function over the LoadPRD result; no I/O beyond the single LoadPRD
// call. Walk order is (tab.position, state.position, stem.position) so
// the same PRD round-trips to the same string twice in a row.
//
// Thin wrapper around RenderPRDExport for callers that don't need the
// sidecar (kept for backward compatibility — prefer RenderPRDExport in
// new code).
func (t *TenantRepo) RenderPRDMarkdown(ctx context.Context, subFlowID string) (string, error) {
	export, err := t.RenderPRDExport(ctx, subFlowID)
	if err != nil {
		return "", err
	}
	return export.Markdown, nil
}

// renderPRDFullToMarkdown walks a loaded PRDFull and produces the
// deterministic markdown rendering. Shared by RenderPRDExport and (via
// it) RenderPRDMarkdown so both surfaces stay byte-identical.
func renderPRDFullToMarkdown(full PRDFull) string {
	var b strings.Builder
	if full.Title != "" {
		fmt.Fprintf(&b, "# %s\n\n", full.Title)
	}
	if full.SummaryMD != "" {
		fmt.Fprintf(&b, "%s\n\n", strings.TrimRight(full.SummaryMD, "\n"))
	}

	for _, tab := range full.Tabs {
		fmt.Fprintf(&b, "## %s\n\n", tab.Name)
		if tab.OverviewMD != "" {
			fmt.Fprintf(&b, "%s\n\n", strings.TrimRight(tab.OverviewMD, "\n"))
		}
		if len(tab.States) > 0 {
			b.WriteString("### Possible States\n\n")
		}
		for _, st := range tab.States {
			fmt.Fprintf(&b, "#### %s\n\n", st.Label)
			if st.FrameName != nil && *st.FrameName != "" {
				fmt.Fprintf(&b, "_Frame:_ `%s`\n\n", *st.FrameName)
			}
			if st.ConditionMD != "" {
				fmt.Fprintf(&b, "**Condition**\n\n%s\n\n", strings.TrimRight(st.ConditionMD, "\n"))
			}
			if st.DesignHandlingMD != "" {
				fmt.Fprintf(&b, "**Design handling**\n\n%s\n\n", strings.TrimRight(st.DesignHandlingMD, "\n"))
			}
			if st.FEHandlingMD != "" {
				fmt.Fprintf(&b, "**FE handling**\n\n%s\n\n", strings.TrimRight(st.FEHandlingMD, "\n"))
			}
			if len(st.AcceptanceCriteria) > 0 {
				b.WriteString("**Acceptance criteria**\n\n")
				for _, c := range st.AcceptanceCriteria {
					fmt.Fprintf(&b, "- %s\n", c.Criterion)
				}
				b.WriteString("\n")
			}
			if len(st.EdgeCases) > 0 {
				b.WriteString("**Edge cases**\n\n")
				for _, c := range st.EdgeCases {
					fmt.Fprintf(&b, "- %s\n", c.EdgeCase)
				}
				b.WriteString("\n")
			}
			if len(st.CopyStrings) > 0 {
				b.WriteString("**Copy**\n\n")
				for _, c := range st.CopyStrings {
					fmt.Fprintf(&b, "- `%s` (%s): %s\n", c.Key, c.Locale, c.Value)
				}
				b.WriteString("\n")
			}
			if len(st.Events) > 0 {
				b.WriteString("**Events**\n\n")
				for _, e := range st.Events {
					if e.FiresOn != "" {
						fmt.Fprintf(&b, "- `%s` — fires on `%s`\n", e.Name, e.FiresOn)
					} else {
						fmt.Fprintf(&b, "- `%s`\n", e.Name)
					}
				}
				b.WriteString("\n")
			}
			if len(st.A11yNotes) > 0 {
				b.WriteString("**Accessibility**\n\n")
				for _, n := range st.A11yNotes {
					fmt.Fprintf(&b, "- %s\n", n.Note)
				}
				b.WriteString("\n")
			}
			if len(st.FrameTags) > 0 {
				b.WriteString("**Frames**\n\n")
				for _, ft := range st.FrameTags {
					if ft.Variant != nil && *ft.Variant != "" {
						fmt.Fprintf(&b, "- `%s` (%s)\n", ft.FigmaNodeID, *ft.Variant)
					} else {
						fmt.Fprintf(&b, "- `%s`\n", ft.FigmaNodeID)
					}
				}
				b.WriteString("\n")
			}
		}
	}

	if full.DesignNotesMD != "" {
		fmt.Fprintf(&b, "## Design notes\n\n%s\n", strings.TrimRight(full.DesignNotesMD, "\n"))
	}

	return b.String()
}

// ─── Internal scanners + lookup helpers ─────────────────────────────────────

func scanPRD(row interface {
	Scan(dest ...any) error
}) (PRD, error) {
	var p PRD
	var createdAt, updatedAt string
	if err := row.Scan(&p.ID, &p.TenantID, &p.SubFlowID, &p.Title, &p.SummaryMD, &p.DesignNotesMD, &createdAt, &updatedAt); err != nil {
		return PRD{}, err
	}
	p.CreatedAt = parseTime(createdAt)
	p.UpdatedAt = parseTime(updatedAt)
	return p, nil
}

func scanPRDTab(row interface {
	Scan(dest ...any) error
}) (PRDTab, error) {
	var tab PRDTab
	var createdAt string
	if err := row.Scan(&tab.ID, &tab.TenantID, &tab.PRDID, &tab.Name, &tab.Position, &tab.OverviewMD, &createdAt); err != nil {
		return PRDTab{}, err
	}
	tab.CreatedAt = parseTime(createdAt)
	return tab, nil
}

func scanPRDState(row interface {
	Scan(dest ...any) error
}) (PRDState, error) {
	var st PRDState
	var frameName, deletedAt sql.NullString
	var createdAt, updatedAt string
	if err := row.Scan(
		&st.ID, &st.TenantID, &st.PRDTabID, &st.Label, &st.Position, &frameName,
		&st.ConditionMD, &st.DesignHandlingMD, &st.FEHandlingMD,
		&deletedAt, &createdAt, &updatedAt,
	); err != nil {
		return PRDState{}, err
	}
	if frameName.Valid {
		v := frameName.String
		st.FrameName = &v
	}
	if deletedAt.Valid {
		d := parseTime(deletedAt.String)
		st.DeletedAt = &d
	}
	st.CreatedAt = parseTime(createdAt)
	st.UpdatedAt = parseTime(updatedAt)
	return st, nil
}

func (t *TenantRepo) getPRDBySubFlow(ctx context.Context, subFlowID string, h dbtx) (PRD, error) {
	row := h.QueryRowContext(ctx,
		`SELECT id, tenant_id, sub_flow_id, title, summary_md, design_notes_md, created_at, updated_at
		   FROM prd
		  WHERE tenant_id = ? AND sub_flow_id = ?`,
		t.tenantID, subFlowID,
	)
	p, err := scanPRD(row)
	if errors.Is(err, sql.ErrNoRows) {
		return PRD{}, ErrPRDNotFound
	}
	if err != nil {
		return PRD{}, fmt.Errorf("lookup prd: %w", err)
	}
	return p, nil
}

func (t *TenantRepo) getPRDTabByName(ctx context.Context, prdID, lowerName string, h dbtx) (PRDTab, error) {
	row := h.QueryRowContext(ctx,
		`SELECT id, tenant_id, prd_id, name, position, overview_md, created_at
		   FROM prd_tab
		  WHERE tenant_id = ? AND prd_id = ? AND LOWER(TRIM(name)) = ?`,
		t.tenantID, prdID, lowerName,
	)
	tab, err := scanPRDTab(row)
	if errors.Is(err, sql.ErrNoRows) {
		return PRDTab{}, ErrNotFound
	}
	if err != nil {
		return PRDTab{}, fmt.Errorf("lookup prd_tab: %w", err)
	}
	return tab, nil
}

// getPRDStateByLabelIncludingDeleted matches live OR soft-deleted rows.
// UpsertPRDState uses this so restoring a soft-deleted state preserves
// the original id (and its authored stems).
func (t *TenantRepo) getPRDStateByLabelIncludingDeleted(ctx context.Context, tabID, lowerLabel string, h dbtx) (PRDState, error) {
	row := h.QueryRowContext(ctx,
		`SELECT id, tenant_id, prd_tab_id, label, position, frame_name,
		        condition_md, design_handling_md, fe_handling_md,
		        deleted_at, created_at, updated_at
		   FROM prd_state
		  WHERE tenant_id = ? AND prd_tab_id = ? AND LOWER(TRIM(label)) = ?
		  ORDER BY (deleted_at IS NULL) DESC
		  LIMIT 1`,
		t.tenantID, tabID, lowerLabel,
	)
	st, err := scanPRDState(row)
	if errors.Is(err, sql.ErrNoRows) {
		return PRDState{}, ErrNotFound
	}
	if err != nil {
		return PRDState{}, fmt.Errorf("lookup prd_state: %w", err)
	}
	return st, nil
}

func (t *TenantRepo) getCopyStringByKey(ctx context.Context, stateID, key, locale string, h dbtx) (CopyString, error) {
	row := h.QueryRowContext(ctx,
		`SELECT id, tenant_id, prd_state_id, key, value, locale, created_at
		   FROM prd_state_copy_string
		  WHERE tenant_id = ? AND prd_state_id = ? AND key = ? AND locale = ?`,
		t.tenantID, stateID, key, locale,
	)
	var c CopyString
	var createdAt string
	if err := row.Scan(&c.ID, &c.TenantID, &c.PRDStateID, &c.Key, &c.Value, &c.Locale, &createdAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return CopyString{}, ErrNotFound
		}
		return CopyString{}, fmt.Errorf("lookup copy_string: %w", err)
	}
	c.CreatedAt = parseTime(createdAt)
	return c, nil
}

func (t *TenantRepo) getEventByName(ctx context.Context, stateID, name string, h dbtx) (Event, error) {
	row := h.QueryRowContext(ctx,
		`SELECT id, tenant_id, prd_state_id, position, name, properties_schema, fires_on, created_at
		   FROM prd_state_event
		  WHERE tenant_id = ? AND prd_state_id = ? AND name = ?`,
		t.tenantID, stateID, name,
	)
	var e Event
	var createdAt string
	if err := row.Scan(&e.ID, &e.TenantID, &e.PRDStateID, &e.Position, &e.Name, &e.PropertiesSchema, &e.FiresOn, &createdAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Event{}, ErrNotFound
		}
		return Event{}, fmt.Errorf("lookup event: %w", err)
	}
	e.CreatedAt = parseTime(createdAt)
	return e, nil
}

// loadStatesForTabs returns every live prd_state row for the given tab ids,
// ordered by (prd_tab_id, position, created_at) so the per-tab fan-out is
// already deterministic.
func (t *TenantRepo) loadStatesForTabs(ctx context.Context, tabIDs []string) ([]PRDState, error) {
	if len(tabIDs) == 0 {
		return nil, nil
	}
	q, args := buildInQuery(
		`SELECT id, tenant_id, prd_tab_id, label, position, frame_name,
		        condition_md, design_handling_md, fe_handling_md,
		        deleted_at, created_at, updated_at
		   FROM prd_state
		  WHERE tenant_id = ? AND deleted_at IS NULL AND prd_tab_id IN `,
		` ORDER BY prd_tab_id, position ASC, created_at ASC`,
		t.tenantID, tabIDs,
	)
	rows, err := t.readHandle().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("load prd_state: %w", err)
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

func (t *TenantRepo) loadAcceptanceCriteria(ctx context.Context, stateIDs []string, stateByID map[string]*PRDStateFull) error {
	q, args := buildInQuery(
		`SELECT id, tenant_id, prd_state_id, position, criterion, created_at
		   FROM prd_state_acceptance_criterion
		  WHERE tenant_id = ? AND prd_state_id IN `,
		` ORDER BY prd_state_id, position ASC, created_at ASC`,
		t.tenantID, stateIDs,
	)
	rows, err := t.readHandle().QueryContext(ctx, q, args...)
	if err != nil {
		return fmt.Errorf("load acceptance_criteria: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var c AcceptanceCriterion
		var createdAt string
		if err := rows.Scan(&c.ID, &c.TenantID, &c.PRDStateID, &c.Position, &c.Criterion, &createdAt); err != nil {
			return err
		}
		c.CreatedAt = parseTime(createdAt)
		if st := stateByID[c.PRDStateID]; st != nil {
			st.AcceptanceCriteria = append(st.AcceptanceCriteria, c)
		}
	}
	return rows.Err()
}

func (t *TenantRepo) loadEdgeCases(ctx context.Context, stateIDs []string, stateByID map[string]*PRDStateFull) error {
	q, args := buildInQuery(
		`SELECT id, tenant_id, prd_state_id, position, edge_case, created_at
		   FROM prd_state_edge_case
		  WHERE tenant_id = ? AND prd_state_id IN `,
		` ORDER BY prd_state_id, position ASC, created_at ASC`,
		t.tenantID, stateIDs,
	)
	rows, err := t.readHandle().QueryContext(ctx, q, args...)
	if err != nil {
		return fmt.Errorf("load edge_cases: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var c EdgeCase
		var createdAt string
		if err := rows.Scan(&c.ID, &c.TenantID, &c.PRDStateID, &c.Position, &c.EdgeCase, &createdAt); err != nil {
			return err
		}
		c.CreatedAt = parseTime(createdAt)
		if st := stateByID[c.PRDStateID]; st != nil {
			st.EdgeCases = append(st.EdgeCases, c)
		}
	}
	return rows.Err()
}

func (t *TenantRepo) loadCopyStrings(ctx context.Context, stateIDs []string, stateByID map[string]*PRDStateFull) error {
	q, args := buildInQuery(
		`SELECT id, tenant_id, prd_state_id, key, value, locale, created_at
		   FROM prd_state_copy_string
		  WHERE tenant_id = ? AND prd_state_id IN `,
		` ORDER BY prd_state_id, key ASC, locale ASC, created_at ASC`,
		t.tenantID, stateIDs,
	)
	rows, err := t.readHandle().QueryContext(ctx, q, args...)
	if err != nil {
		return fmt.Errorf("load copy_strings: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var c CopyString
		var createdAt string
		if err := rows.Scan(&c.ID, &c.TenantID, &c.PRDStateID, &c.Key, &c.Value, &c.Locale, &createdAt); err != nil {
			return err
		}
		c.CreatedAt = parseTime(createdAt)
		if st := stateByID[c.PRDStateID]; st != nil {
			st.CopyStrings = append(st.CopyStrings, c)
		}
	}
	return rows.Err()
}

func (t *TenantRepo) loadEvents(ctx context.Context, stateIDs []string, stateByID map[string]*PRDStateFull) error {
	q, args := buildInQuery(
		`SELECT id, tenant_id, prd_state_id, position, name, properties_schema, fires_on, created_at
		   FROM prd_state_event
		  WHERE tenant_id = ? AND prd_state_id IN `,
		` ORDER BY prd_state_id, position ASC, name ASC, created_at ASC`,
		t.tenantID, stateIDs,
	)
	rows, err := t.readHandle().QueryContext(ctx, q, args...)
	if err != nil {
		return fmt.Errorf("load events: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var e Event
		var createdAt string
		if err := rows.Scan(&e.ID, &e.TenantID, &e.PRDStateID, &e.Position, &e.Name, &e.PropertiesSchema, &e.FiresOn, &createdAt); err != nil {
			return err
		}
		e.CreatedAt = parseTime(createdAt)
		if st := stateByID[e.PRDStateID]; st != nil {
			st.Events = append(st.Events, e)
		}
	}
	return rows.Err()
}

func (t *TenantRepo) loadA11yNotes(ctx context.Context, stateIDs []string, stateByID map[string]*PRDStateFull) error {
	q, args := buildInQuery(
		`SELECT id, tenant_id, prd_state_id, position, note, created_at
		   FROM prd_state_a11y_note
		  WHERE tenant_id = ? AND prd_state_id IN `,
		` ORDER BY prd_state_id, position ASC, created_at ASC`,
		t.tenantID, stateIDs,
	)
	rows, err := t.readHandle().QueryContext(ctx, q, args...)
	if err != nil {
		return fmt.Errorf("load a11y_notes: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var n A11yNote
		var createdAt string
		if err := rows.Scan(&n.ID, &n.TenantID, &n.PRDStateID, &n.Position, &n.Note, &createdAt); err != nil {
			return err
		}
		n.CreatedAt = parseTime(createdAt)
		if st := stateByID[n.PRDStateID]; st != nil {
			st.A11yNotes = append(st.A11yNotes, n)
		}
	}
	return rows.Err()
}

func (t *TenantRepo) loadFrameTags(ctx context.Context, stateIDs []string, stateByID map[string]*PRDStateFull) error {
	q, args := buildInQuery(
		`SELECT id, tenant_id, prd_state_id, figma_node_id, variant, position, created_at
		   FROM frame_tag
		  WHERE tenant_id = ? AND prd_state_id IN `,
		` ORDER BY prd_state_id, position ASC, figma_node_id ASC, created_at ASC`,
		t.tenantID, stateIDs,
	)
	rows, err := t.readHandle().QueryContext(ctx, q, args...)
	if err != nil {
		return fmt.Errorf("load frame_tags: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var ft FrameTag
		var variant sql.NullString
		var createdAt string
		if err := rows.Scan(&ft.ID, &ft.TenantID, &ft.PRDStateID, &ft.FigmaNodeID, &variant, &ft.Position, &createdAt); err != nil {
			return err
		}
		if variant.Valid {
			v := variant.String
			ft.Variant = &v
		}
		ft.CreatedAt = parseTime(createdAt)
		if st := stateByID[ft.PRDStateID]; st != nil {
			st.FrameTags = append(st.FrameTags, ft)
		}
	}
	return rows.Err()
}

// buildInQuery composes `... IN (?, ?, ?) <suffix>` and returns the
// flattened args slice. Args are prepended with `tenantID` because every
// caller follows the same `WHERE tenant_id = ? AND col IN (...)` shape.
//
// Kept local to prd.go to avoid coupling with unrelated callers. The id
// list is sorted before binding so the SQL plan cache (whatever SQLite has)
// sees a stable query shape — not load-bearing for correctness, just hygiene.
func buildInQuery(prefix, suffix, tenantID string, ids []string) (string, []any) {
	sorted := append([]string(nil), ids...)
	sort.Strings(sorted)
	placeholders := make([]string, len(sorted))
	args := make([]any, 0, 1+len(sorted))
	args = append(args, tenantID)
	for i, id := range sorted {
		placeholders[i] = "?"
		args = append(args, id)
	}
	q := prefix + "(" + strings.Join(placeholders, ", ") + ")" + suffix
	return q, args
}
