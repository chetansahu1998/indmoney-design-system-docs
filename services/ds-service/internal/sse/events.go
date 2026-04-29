// Package sse provides a stdlib-only Server-Sent Events broker, single-use
// ticket auth, and typed event payloads for the Projects · Flow Atlas pipeline.
//
// The broker is interface-first (BrokerService) so Phase 7 can swap in a
// Redis-backed implementation without changing call sites. The default
// MemoryBroker keeps everything in-process: a per-trace map of subscribers,
// non-blocking fan-out, and a 15s heartbeat.
package sse

// Event is the contract every payload pushed through a broker must satisfy.
//
// TenantID is read by the broker to filter delivery — only subscribers whose
// tenantID matches the event's tenant receive it. Type is the SSE event-type
// string (also used as the JSON discriminator). Payload is what gets marshaled
// into the `data:` field on the wire.
type Event interface {
	TenantID() string
	Type() string
	Payload() any
}

// ProjectViewReady fires when the Phase 1 fast-preview pipeline has produced a
// viewable project version (PNG renders, Figma metadata, mode pairs persisted).
type ProjectViewReady struct {
	ProjectSlug string `json:"project_slug"`
	VersionID   string `json:"version_id"`
	Tenant      string `json:"tenant_id"`
}

// TenantID implements Event.
func (e ProjectViewReady) TenantID() string { return e.Tenant }

// Type implements Event.
func (e ProjectViewReady) Type() string { return "project.view_ready" }

// Payload implements Event.
func (e ProjectViewReady) Payload() any { return e }

// ProjectAuditComplete fires when the (Phase 2) audit run finishes successfully.
// ViolationCount is the total number of audit findings against this version.
type ProjectAuditComplete struct {
	ProjectSlug    string `json:"project_slug"`
	VersionID      string `json:"version_id"`
	Tenant         string `json:"tenant_id"`
	ViolationCount int    `json:"violation_count"`
}

// TenantID implements Event.
func (e ProjectAuditComplete) TenantID() string { return e.Tenant }

// Type implements Event.
func (e ProjectAuditComplete) Type() string { return "project.audit_complete" }

// Payload implements Event.
func (e ProjectAuditComplete) Payload() any { return e }

// ProjectAuditFailed fires when an audit run errors out before completion.
type ProjectAuditFailed struct {
	ProjectSlug string `json:"project_slug"`
	VersionID   string `json:"version_id"`
	Tenant      string `json:"tenant_id"`
	Error       string `json:"error"`
}

// TenantID implements Event.
func (e ProjectAuditFailed) TenantID() string { return e.Tenant }

// Type implements Event.
func (e ProjectAuditFailed) Type() string { return "project.audit_failed" }

// Payload implements Event.
func (e ProjectAuditFailed) Payload() any { return e }

// ProjectExportFailed fires when the fast-preview pipeline aborts (Figma fetch
// failure, render timeout, size cap exceeded, etc.).
type ProjectExportFailed struct {
	ProjectSlug string `json:"project_slug"`
	VersionID   string `json:"version_id"`
	Tenant      string `json:"tenant_id"`
	Error       string `json:"error"`
}

// TenantID implements Event.
func (e ProjectExportFailed) TenantID() string { return e.Tenant }

// Type implements Event.
func (e ProjectExportFailed) Type() string { return "project.export_failed" }

// Payload implements Event.
func (e ProjectExportFailed) Payload() any { return e }
