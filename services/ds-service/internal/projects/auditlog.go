package projects

import (
	"context"
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
	Action      string // AuditActionExport or AuditActionExportFailed
	UserID      string
	TenantID    string
	FileID      string
	ProjectID   string
	VersionID   string
	IP          string
	UserAgent   string
	TraceID     string
	Error       string // populated when Action == AuditActionExportFailed
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
		"file_id":     ev.FileID,
		"project_id":  ev.ProjectID,
		"version_id":  ev.VersionID,
		"trace_id":    ev.TraceID,
		"user_agent":  ev.UserAgent,
		"schema_ver":  ProjectsSchemaVersion,
	}
	if ev.Error != "" {
		details["error"] = ev.Error
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
