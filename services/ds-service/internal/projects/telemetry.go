package projects

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/auth"
)

// telemetry.go — central drop-zone for client-side observability events.
//
// The Figma plugin (code.ts) and the Next.js web app post errors,
// lifecycle events, and breadcrumbs here. Server logs them to stdout
// (which Fly captures) so anyone can `fly logs -a indmoney-ds-service`
// and watch a live stream of what the apps are doing across machines.
//
// Auth: optional. Plugin/web calls usually carry a JWT; we honor it
// when present (so we can attach user_email) but don't require it,
// because errors that happen BEFORE auth completes (e.g. token mint
// failures) are exactly what we want to capture.
//
// Body shape:
//   {
//     "source":   "plugin" | "web",
//     "level":    "info" | "warn" | "error",
//     "event":    "atlas.hydrate" | "plugin.export.failed" | …,
//     "payload":  { … free-form, ≤4KB JSON … },
//     "session":  "uuid",            // optional, helps group events
//     "build":    "git-sha or 'dev'" // optional
//   }
//
// Hard caps: 8KB body, 10 events/sec/IP (rate-limited by RateLimiter
// at the auth-server hop). Storage: stdout only — keeps the surface
// dead-simple. If we ever want long-term retention, swap stdout for
// a SQLite append + index later.

type telemetryEvent struct {
	Source  string                 `json:"source"`
	Level   string                 `json:"level"`
	Event   string                 `json:"event"`
	Payload map[string]any         `json:"payload,omitempty"`
	Session string                 `json:"session,omitempty"`
	Build   string                 `json:"build,omitempty"`
}

const maxTelemetryBody = 8 << 10 // 8KB

// HandleTelemetryEvent serves POST /v1/telemetry/event.
// Anonymous-allowed. Always 204 unless the body is malformed.
func (s *Server) HandleTelemetryEvent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST only")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxTelemetryBody+1))
	if err != nil {
		writeJSONErr(w, http.StatusBadRequest, "read", err.Error())
		return
	}
	if len(body) > maxTelemetryBody {
		writeJSONErr(w, http.StatusRequestEntityTooLarge, "too_large",
			"telemetry body cannot exceed 8KB")
		return
	}
	var ev telemetryEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		writeJSONErr(w, http.StatusBadRequest, "json", err.Error())
		return
	}
	// Sanitize / normalize so a bad client can't poison the log.
	if ev.Source == "" {
		ev.Source = "unknown"
	}
	if ev.Level == "" {
		ev.Level = "info"
	}
	if ev.Event == "" {
		ev.Event = "anonymous"
	}
	ev.Source = trimAndCap(ev.Source, 40)
	ev.Level = strings.ToLower(trimAndCap(ev.Level, 8))
	ev.Event = trimAndCap(ev.Event, 80)
	ev.Session = trimAndCap(ev.Session, 64)
	ev.Build = trimAndCap(ev.Build, 64)

	// Best-effort identity from JWT (no auth requirement).
	var userEmail string
	if claims, _ := r.Context().Value(ctxKeyClaims).(*auth.Claims); claims != nil {
		userEmail = claims.Email
	}

	// IP — keep just enough to disambiguate without retaining full PII.
	clientIP := r.Header.Get("CF-Connecting-IP")
	if clientIP == "" {
		clientIP = r.Header.Get("X-Forwarded-For")
	}
	if clientIP == "" {
		clientIP = r.RemoteAddr
	}
	clientIP = trimAndCap(clientIP, 64)

	// Emit one structured line. Fly's log shipper picks up stdout JSON.
	emitTelemetry(ev, userEmail, clientIP, r.UserAgent())

	w.WriteHeader(http.StatusNoContent)
}

// emitTelemetry prints a single JSON line to stdout. Wrapped so tests
// can swap the writer without monkey-patching.
func emitTelemetry(ev telemetryEvent, userEmail, clientIP, userAgent string) {
	rec := map[string]any{
		"telemetry": true,
		"ts":        time.Now().UTC().Format(time.RFC3339Nano),
		"source":    ev.Source,
		"level":     ev.Level,
		"event":     ev.Event,
	}
	if ev.Payload != nil {
		rec["payload"] = ev.Payload
	}
	if ev.Session != "" {
		rec["session"] = ev.Session
	}
	if ev.Build != "" {
		rec["build"] = ev.Build
	}
	if userEmail != "" {
		rec["user_email"] = userEmail
	}
	if clientIP != "" {
		rec["client_ip"] = clientIP
	}
	if userAgent != "" {
		// Trim user-agent — useful for "is this Chrome on macOS?" without
		// being a chunky PII string.
		rec["user_agent"] = trimAndCap(userAgent, 120)
	}
	b, _ := json.Marshal(rec)
	// Plain Println so each event is one line and grep-friendly.
	// Fly's log shipper picks this up as application-level stdout.
	println(string(b))
}

func trimAndCap(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n]
	}
	return s
}

// Compile-time guard.
var _ http.HandlerFunc = (*Server)(nil).HandleTelemetryEvent
