package sse

import (
	"net/http"
	"time"
)

// ClearWriteDeadline opts the current SSE response out of the
// server-wide http.Server.WriteTimeout. Without this, the 5-minute
// WriteTimeout configured in cmd/server/main.go force-closes every SSE
// stream at the 5-minute mark — including the new POST /mcp GET /mcp
// stream Claude Connectors subscribe to for tools/list_changed
// notifications. /ce-code-review pre-existing-finding: WriteTimeout
// applies to all routes process-wide; SSE handlers must opt out
// individually.
//
// Safe to call once per SSE handler, right after WriteHeader. Errors
// are returned but typically ignored — older Go runtimes or
// non-conforming http.ResponseWriter wrappers may not implement the
// deadline interface; in that case the handler still works under the
// 5-min cap (degraded but not broken).
//
// Implementation: http.NewResponseController exposes the per-request
// deadline knobs introduced in Go 1.20. SetWriteDeadline(time.Time{})
// clears the deadline so writes block as long as the client reads.
func ClearWriteDeadline(w http.ResponseWriter) error {
	return http.NewResponseController(w).SetWriteDeadline(time.Time{})
}
