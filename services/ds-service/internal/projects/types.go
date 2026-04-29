// Package projects implements the Phase 1 fast-preview pipeline behind
// /v1/projects/export. It owns:
//
//   - HTTP handlers (export, get, list, events, ticket)
//   - SQLite repository scoped by tenant_id (TenantRepo)
//   - Pipeline orchestration that pulls Figma metadata + renders PNGs
//   - Mode-pair detection re-run server-side to canonicalize plugin payload
//   - In-memory rate limiting + idempotency caches
//   - PNG long-edge downsample to keep texture memory bounded
//   - Recovery sweeper that marks orphaned versions failed
//
// Every struct mirrors a row in migrations/0001_projects_schema.up.sql.
// Every repository method on TenantRepo injects WHERE tenant_id = ? — cross-
// tenant attempts return 404 (no existence oracle).
package projects

import "time"

// ProjectsSchemaVersion is the canonical Phase 1 schema version. Used in audit
// log entries and the export response so callers can detect drift early.
const ProjectsSchemaVersion = "1.0"

// ─── DB row mirrors ──────────────────────────────────────────────────────────

// Project mirrors the projects table.
type Project struct {
	ID          string
	Slug        string
	Name        string
	Platform    string // mobile | web
	Product     string
	Path        string
	OwnerUserID string
	TenantID    string
	DeletedAt   *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// ProjectVersion mirrors the project_versions table.
type ProjectVersion struct {
	ID                  string
	ProjectID           string
	TenantID            string
	VersionIndex        int
	Status              string // pending | view_ready | failed
	PipelineStartedAt   *time.Time
	PipelineHeartbeatAt *time.Time
	Error               string
	CreatedByUserID     string
	CreatedAt           time.Time
}

// Flow mirrors the flows table.
type Flow struct {
	ID        string
	ProjectID string
	TenantID  string
	FileID    string
	SectionID *string
	Name      string
	PersonaID *string
	DeletedAt *time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Persona mirrors the personas table (org-wide library, tenant-scoped).
type Persona struct {
	ID                string
	TenantID          string
	Name              string
	Status            string // approved | pending
	CreatedByUserID   string
	ApprovedByUserID  *string
	ApprovedAt        *time.Time
	DeletedAt         *time.Time
	CreatedAt         time.Time
}

// Screen mirrors the screens table.
type Screen struct {
	ID              string
	VersionID       string
	FlowID          string
	TenantID        string
	X               float64
	Y               float64
	Width           float64
	Height          float64
	ScreenLogicalID string
	PNGStorageKey   *string
	CreatedAt       time.Time
}

// ScreenMode mirrors the screen_modes table.
type ScreenMode struct {
	ID                          string
	ScreenID                    string
	TenantID                    string
	ModeLabel                   string
	FigmaFrameID                string
	ExplicitVariableModesJSON   string
}

// AuditJob mirrors the audit_jobs table.
type AuditJob struct {
	ID              string
	VersionID       string
	TenantID        string
	Status          string
	TraceID         string
	IdempotencyKey  string
	LeasedBy        *string
	LeaseExpiresAt  *int64
	CreatedAt       time.Time
	StartedAt       *time.Time
	CompletedAt     *time.Time
	Error           string
}

// Violation mirrors the violations table.
type Violation struct {
	ID         string
	VersionID  string
	ScreenID   string
	TenantID   string
	RuleID     string
	Severity   string
	Property   string
	Observed   string
	Suggestion string
	PersonaID  *string
	ModeLabel  *string
	Status     string
	CreatedAt  time.Time
}

// CompositionRef is reserved for cross-version composition references introduced
// in Phase 4. Phase 1 doesn't read or write it but the type is defined here so
// downstream code can import it without the package shape changing twice.
type CompositionRef struct {
	ID                string
	VersionID         string
	TargetVersionID   string
	TenantID          string
	CompositionType   string
	CreatedAt         time.Time
}

// ─── Wire-shape helpers (request/response) ──────────────────────────────────

// ModeGroupPayload is the plugin's per-group declaration of which frames belong
// to which mode pair. Server re-runs DetectModePairs to canonicalize.
type ModeGroupPayload struct {
	VariableCollectionID string             `json:"variable_collection_id"`
	Frames               []FramePayloadMode `json:"frames"`
}

// FramePayloadMode is a single frame's mode declaration within a group.
type FramePayloadMode struct {
	FrameID                   string            `json:"frame_id"`
	ModeID                    string            `json:"mode_id"`
	ModeLabel                 string            `json:"mode_label"`
	ExplicitVariableModesJSON string            `json:"explicit_variable_modes_json,omitempty"`
}

// FramePayload is the plugin's per-frame info: ID, position, dimensions, and
// optional explicit variable modes for mode-pair detection.
type FramePayload struct {
	FrameID                   string  `json:"frame_id"`
	X                         float64 `json:"x"`
	Y                         float64 `json:"y"`
	Width                     float64 `json:"width"`
	Height                    float64 `json:"height"`
	Name                      string  `json:"name,omitempty"`
	VariableCollectionID      string  `json:"variable_collection_id,omitempty"`
	ModeID                    string  `json:"mode_id,omitempty"`
	ModeLabel                 string  `json:"mode_label,omitempty"`
	ExplicitVariableModesJSON string  `json:"explicit_variable_modes_json,omitempty"`
}

// FlowPayload is one flow's worth of plugin export data.
type FlowPayload struct {
	SectionID   *string            `json:"section_id"`
	FrameIDs    []string           `json:"frame_ids"`
	Frames      []FramePayload     `json:"frames"`
	Platform    string             `json:"platform"`
	Product     string             `json:"product"`
	Path        string             `json:"path"`
	PersonaName string             `json:"persona_name"`
	Name        string             `json:"name"`
	ModeGroups  []ModeGroupPayload `json:"mode_groups"`
}

// ExportRequest is the body of POST /v1/projects/export.
type ExportRequest struct {
	IdempotencyKey string        `json:"idempotency_key"`
	FileID         string        `json:"file_id"`
	FileName       string        `json:"file_name"`
	Flows          []FlowPayload `json:"flows"`
}

// ExportResponse is what the plugin gets back synchronously after the request
// has been validated and the project skeleton has been persisted. The pipeline
// runs in the background and emits SSE events on the trace_id channel.
type ExportResponse struct {
	ProjectID     string `json:"project_id"`
	VersionID     string `json:"version_id"`
	Deeplink      string `json:"deeplink"`
	TraceID       string `json:"trace_id"`
	SchemaVersion string `json:"schema_version"`
}

// FrameInfo is the input shape for DetectModePairs (mirror of FramePayload but
// stripped down to the fields the algorithm actually consumes).
type FrameInfo struct {
	FrameID              string
	X                    float64
	Y                    float64
	Width                float64
	Height               float64
	VariableCollectionID string
	ModeID               string
	ModeLabel            string
}

// ModeGroup is one mode-pair detection result. Frames inside it share a
// VariableCollectionId and live at the same x-column with different mode IDs.
type ModeGroup struct {
	VariableCollectionID string
	Frames               []FrameInfo
}
