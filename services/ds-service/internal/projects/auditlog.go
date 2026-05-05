package projects

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/db"
)

// AuditAction enumerates the project.* events U4 must emit. Both success AND
// failure paths must write a row — every export attempt is auditable.
const (
	AuditActionExport       = "project.export"
	AuditActionExportFailed = "project.export.failed"
)

// Override audit event types — emitted by U10's WriteOverrideEvent helper.
// Frontend `auditLogToActivity` keys off these strings; the helper centralises
// the JSON details shape so the PUT/DELETE/bulk/orphan call sites stay in sync.
const (
	AuditActionOverrideTextSet     = "override.text.set"
	AuditActionOverrideTextReset   = "override.text.reset"
	AuditActionOverrideTextBulkSet = "override.text.bulk_set"
	// AuditActionOverrideOrphaned is also referenced from
	// screen_overrides_reattach.go (kept there for backward compatibility).
)

// OverrideEvent is the input shape for WriteOverrideEvent. Optional fields
// (BulkID, OldValue, NewValue, Reason) are zero-valued when not relevant for
// the event type. Endpoint + Method default per event type so callers don't
// have to repeat them.
type OverrideEvent struct {
	EventType   string // override.text.set | .reset | .orphaned | .bulk_set
	TenantID    string
	UserID      string // empty for system-emitted events (e.g. orphan from re-import)
	FlowID      string // optional — empty when re-attach can't resolve a flow
	ScreenID    string
	FigmaNodeID string
	OldValue    string // prior override value (or original Figma text on first edit)
	NewValue    string // new override value (empty for reset/orphaned)
	BulkID      string // optional — populated for bulk_set rows
	Reason      string // optional — populated for orphaned rows
	IPAddress   string
	Endpoint    string // defaults to a synthetic path per event type when empty
	Method      string // defaults to a per-event-type method when empty
	StatusCode  int
}

// WriteOverrideEvent inserts one audit_log row keyed off ev.EventType inside
// the supplied transaction. Centralises the `details` JSON shape so the
// frontend's activity feed (data-adapters.ts:auditLogToActivity) can rely on
// a single contract regardless of which call site emitted the row.
//
// `details` JSON contract:
//
//	{
//	  flow_id?:        string  // omitted when unknown (e.g. orphaned w/o flow context)
//	  screen_id:       string
//	  figma_node_id:   string
//	  old:             string  // prior value or original Figma text
//	  new:             string  // new value (set / bulk_set only)
//	  bulk_id?:        string  // bulk_set only
//	  reason?:         string  // orphaned only
//	  schema_ver:      int
//	}
//
// The function does not commit the tx; the caller's lifecycle owns the
// transaction so the audit row + override write commit-or-roll-back atomically.
func WriteOverrideEvent(ctx context.Context, tx *sql.Tx, ev OverrideEvent) error {
	if ev.EventType == "" {
		return nil
	}
	details := map[string]any{
		"screen_id":     ev.ScreenID,
		"figma_node_id": ev.FigmaNodeID,
		"old":           ev.OldValue,
		"new":           ev.NewValue,
		"schema_ver":    ProjectsSchemaVersion,
	}
	// Only include flow_id when populated — keeps `JSON_EXTRACT(details, '$.flow_id')`
	// queries clean (NULL vs empty-string ambiguity).
	if ev.FlowID != "" {
		details["flow_id"] = ev.FlowID
	}
	if ev.BulkID != "" {
		details["bulk_id"] = ev.BulkID
	}
	if ev.Reason != "" {
		details["reason"] = ev.Reason
	}
	bs, _ := json.Marshal(details)

	endpoint := ev.Endpoint
	method := ev.Method
	if endpoint == "" {
		switch ev.EventType {
		case AuditActionOverrideOrphaned:
			endpoint = "/internal/pipeline/reattach"
			if method == "" {
				method = "POST"
			}
		default:
			endpoint = "/v1/projects/text-overrides"
		}
	}
	if method == "" {
		switch ev.EventType {
		case AuditActionOverrideTextReset:
			method = "DELETE"
		case AuditActionOverrideTextBulkSet:
			method = "POST"
		default:
			method = "PUT"
		}
	}

	_, err := tx.ExecContext(ctx,
		`INSERT INTO audit_log
		    (id, ts, event_type, tenant_id, user_id, method, endpoint,
		     status_code, duration_ms, ip_address, details)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		uuid.NewString(),
		time.Now().UTC().Format(time.RFC3339Nano),
		ev.EventType,
		ev.TenantID,
		ev.UserID,
		method,
		endpoint,
		ev.StatusCode,
		0,
		ev.IPAddress,
		string(bs),
	)
	return err
}

// AuditLogger is a small wrapper around db.WriteAudit that builds the
// project-export-shaped row. Keeping it in this package lets the pipeline call
// a single function with named-args clarity, rather than repeating the JSON
// detail-blob construction at every call site.
type AuditLogger struct {
	DB *db.DB
}

// AuditExportEvent is the input for WriteExport — every required field from
// the U4 plan section. Optional fields are zero-valued when unknown.
type AuditExportEvent struct {
	Action    string // AuditActionExport or AuditActionExportFailed
	UserID    string
	TenantID  string
	FileID    string
	ProjectID string
	VersionID string
	IP        string
	UserAgent string
	TraceID   string
	Error     string // populated when Action == AuditActionExportFailed
	// Audit finding B5 — preserve the (frame_id, x, y, w, h) tuples so a
	// failed pipeline can be replayed without re-walking the Figma file.
	// Caller passes the slice from the export request body; nil/empty when
	// the caller doesn't have it (e.g. a re-audit fanout job).
	Frames []ExportAuditFrame
}

// ExportAuditFrame is the minimal per-frame snapshot the audit_log persists
// so a future recovery cmd can rebuild PipelineInputs.Frames without having
// to re-walk the full Figma file. Mirrors PipelineFrame's identity fields.
type ExportAuditFrame struct {
	ScreenID     string  `json:"screen_id"`
	FigmaFrameID string  `json:"figma_frame_id"`
	FlowID       string  `json:"flow_id"`
	X            float64 `json:"x"`
	Y            float64 `json:"y"`
	Width        float64 `json:"width"`
	Height       float64 `json:"height"`
}

// LatestExportEvent reads back the most-recent project.export row for the
// given version_id and decodes its persisted Frames slice. Used by T4's
// retry handler — when a designer asks to re-render a failed version, we
// rebuild the pipeline's input from the audit-log snapshot instead of
// asking them to re-walk Figma.
//
// Returns ErrNotFound when no successful export event exists for the
// version (e.g. the original export wrote project.export.failed only —
// rare; means the failure happened before frames were captured).
func (a *AuditLogger) LatestExportEvent(ctx context.Context, tenantID, versionID string) ([]ExportAuditFrame, string, error) {
	if a == nil || a.DB == nil {
		return nil, "", ErrNotFound
	}
	var detailsRaw string
	err := a.DB.QueryRowContext(ctx,
		`SELECT details FROM audit_log
		  WHERE tenant_id = ? AND event_type = ?
		    AND json_extract(details, '$.version_id') = ?
		  ORDER BY ts DESC LIMIT 1`,
		tenantID, AuditActionExport, versionID,
	).Scan(&detailsRaw)
	if err != nil {
		return nil, "", ErrNotFound
	}
	var parsed struct {
		FileID string             `json:"file_id"`
		Frames []ExportAuditFrame `json:"frames"`
	}
	if err := json.Unmarshal([]byte(detailsRaw), &parsed); err != nil {
		return nil, "", err
	}
	return parsed.Frames, parsed.FileID, nil
}

// WriteExport writes one audit_log row for a project.export event. Failures to
// persist are returned but should not block the pipeline — caller may log and
// move on. The details column is JSON for forward extensibility.
func (a *AuditLogger) WriteExport(ctx context.Context, ev AuditExportEvent) error {
	if a == nil || a.DB == nil {
		return nil
	}
	if ev.Action == "" {
		ev.Action = AuditActionExport
	}
	details := map[string]any{
		"file_id":    ev.FileID,
		"project_id": ev.ProjectID,
		"version_id": ev.VersionID,
		"trace_id":   ev.TraceID,
		"user_agent": ev.UserAgent,
		"schema_ver": ProjectsSchemaVersion,
	}
	if ev.Error != "" {
		details["error"] = ev.Error
	}
	if len(ev.Frames) > 0 {
		details["frames"] = ev.Frames
	}
	bs, _ := json.Marshal(details)
	entry := db.AuditEntry{
		ID:         uuid.NewString(),
		TS:         time.Now().UTC(),
		EventType:  ev.Action,
		TenantID:   ev.TenantID,
		UserID:     ev.UserID,
		Method:     "POST",
		Endpoint:   "/v1/projects/export",
		StatusCode: 0, // populated downstream when known
		IPAddress:  ev.IP,
		Details:    string(bs),
	}
	return a.DB.WriteAudit(ctx, entry)
}
