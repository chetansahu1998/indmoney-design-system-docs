package projects

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/auth"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/sse"
)

// Phase 6 — HTTP handler + SSE channel for the mind-graph at /atlas.
//
// Read path: HandleGraphAggregate runs ONE indexed SELECT against
// graph_index, projects rows into the wire shape, returns JSON. No on-
// request aggregation, no source walks — the RebuildGraphIndex worker has
// already done that work.
//
// Live updates: HandleGraphEventsTicket mints a single-use ticket bound to
// the synthetic graph:<tenant>:<platform> trace ID; HandleGraphEvents
// upgrades to text/event-stream and forwards GraphIndexUpdated events.
// Subscribers re-fetch the aggregate on bust.

// graphBroadcastChannel returns the synthetic SSE channel name for a tenant
// + platform. Mirrors the inbox:<tenant_id> pattern from Phase 4.1; the
// platform suffix makes Mobile↔Web busts independent (R25).
func graphBroadcastChannel(tenantID, platform string) string {
	return "graph:" + tenantID + ":" + platform
}

// HandleGraphAggregate serves GET /v1/projects/graph?platform=mobile|web.
//
// Returns the full GraphAggregate JSON (nodes + edges + cache_key) for the
// authed tenant. ETag header is the cache_key so a re-fetch on an unchanged
// aggregate can short-circuit.
//
// Auth: requires a valid claims context (set by AdaptAuthMiddleware).
// Validation: platform query param must be "mobile" or "web"; 400 otherwise.
// Performance: ≤15ms p95 on a 1500-row tenant slice (single indexed SELECT).
func (s *Server) HandleGraphAggregate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET only")
		return
	}
	claims, _ := r.Context().Value(ctxKeyClaims).(*auth.Claims)
	if claims == nil {
		writeJSONErr(w, http.StatusUnauthorized, "unauthorized", "missing claims")
		return
	}
	tenantID := s.resolveTenantID(claims)
	if tenantID == "" {
		writeJSONErr(w, http.StatusForbidden, "no_tenant", "")
		return
	}
	platform := r.URL.Query().Get("platform")
	if platform != GraphPlatformMobile && platform != GraphPlatformWeb {
		writeJSONErr(w, http.StatusBadRequest, "invalid_platform",
			"platform must be 'mobile' or 'web'")
		return
	}
	repo := NewTenantRepo(s.deps.DB.DB, tenantID)
	rows, err := repo.LoadGraph(r.Context(), platform)
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "load_graph", err.Error())
		return
	}
	agg := BuildAggregate(rows, platform)

	// If-None-Match — short-circuit unchanged aggregates so re-fetches on a
	// SSE bust that arrives just-after the last fetch don't roundtrip the
	// full payload.
	if etag := r.Header.Get("If-None-Match"); etag != "" && etag == `"`+agg.CacheKey+`"` && agg.CacheKey != "" {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "private, max-age=30")
	if agg.CacheKey != "" {
		w.Header().Set("ETag", `"`+agg.CacheKey+`"`)
	}
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(agg)
}

// HandleGraphEventsTicket serves POST /v1/projects/graph/events/ticket.
//
// Accepts a `platform` body field (or query param) and binds the ticket to
// the synthetic graph:<tenant>:<platform> trace ID. The /atlas mount calls
// this once to obtain a ticket then opens an EventSource on
// /v1/projects/graph/events?ticket=…
func (s *Server) HandleGraphEventsTicket(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST only")
		return
	}
	if s.deps.Tickets == nil {
		writeJSONErr(w, http.StatusInternalServerError, "tickets_not_configured", "")
		return
	}
	claims, _ := r.Context().Value(ctxKeyClaims).(*auth.Claims)
	if claims == nil {
		writeJSONErr(w, http.StatusUnauthorized, "unauthorized", "missing claims")
		return
	}
	tenantID := s.resolveTenantID(claims)
	if tenantID == "" {
		writeJSONErr(w, http.StatusForbidden, "no_tenant", "")
		return
	}
	platform := r.URL.Query().Get("platform")
	if platform == "" {
		// Allow body-based platform too (POST with JSON body).
		var body struct {
			Platform string `json:"platform"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
			platform = body.Platform
		}
	}
	if platform != GraphPlatformMobile && platform != GraphPlatformWeb {
		writeJSONErr(w, http.StatusBadRequest, "invalid_platform",
			"platform must be 'mobile' or 'web'")
		return
	}
	traceID := graphBroadcastChannel(tenantID, platform)
	ticket := s.deps.Tickets.IssueTicket(claims.Sub, tenantID, traceID, sse.DefaultTicketTTL)
	writeJSON(w, http.StatusOK, map[string]any{
		"ticket":     ticket,
		"trace_id":   traceID,
		"platform":   platform,
		"expires_in": int(sse.DefaultTicketTTL.Seconds()),
	})
}

// HandleGraphEvents serves GET /v1/projects/graph/events?ticket=…
//
// Long-poll EventSource that emits GraphIndexUpdated when the rebuild
// worker flushes for the (tenant, platform) the ticket was issued against.
// Heartbeats keep the connection alive across proxies (Cloudflare Tunnel
// is the production path).
func (s *Server) HandleGraphEvents(w http.ResponseWriter, r *http.Request) {
	if s.deps.Tickets == nil || s.deps.Broker == nil {
		writeJSONErr(w, http.StatusInternalServerError, "sse_not_configured", "")
		return
	}
	if r.URL.Query().Get("token") != "" || r.URL.Query().Get("authorization") != "" {
		writeJSONErr(w, http.StatusBadRequest, "no_jwt_in_query",
			"use ?ticket=... not ?token=...")
		return
	}
	ticket := r.URL.Query().Get("ticket")
	if ticket == "" {
		writeJSONErr(w, http.StatusUnauthorized, "missing_ticket", "")
		return
	}
	userID, tenantID, traceID, ok := s.deps.Tickets.RedeemTicket(ticket)
	if !ok {
		writeJSONErr(w, http.StatusUnauthorized, "invalid_ticket", "")
		return
	}
	// Accept either platform; the channel name encodes which one. We don't
	// require the URL to repeat the platform — the trace ID is the
	// authoritative scope.
	if !isGraphChannel(traceID) {
		writeJSONErr(w, http.StatusForbidden, "wrong_channel", "ticket bound to a non-graph channel")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSONErr(w, http.StatusInternalServerError, "no_streaming", "")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ch, unsub, err := s.deps.Broker.Subscribe(traceID, tenantID, userID)
	if err != nil {
		if errors.Is(err, sse.ErrSubscriberCapReached) {
			writeJSONErr(w, http.StatusServiceUnavailable, "subscribers_full", "")
			return
		}
		writeJSONErr(w, http.StatusInternalServerError, "subscribe", err.Error())
		return
	}
	defer unsub()

	clientGone := r.Context().Done()
	for {
		select {
		case <-clientGone:
			return
		case ev, alive := <-ch:
			if !alive {
				return
			}
			if sse.IsHeartbeat(ev) {
				_, _ = w.Write([]byte(": keepalive\n\n"))
				flusher.Flush()
				continue
			}
			payload, _ := json.Marshal(ev.Payload())
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type(), payload)
			flusher.Flush()
		}
	}
}

// isGraphChannel returns true when the ticket-bound trace ID matches the
// graph:<tenant>:<platform> shape. Cheaper than parsing the platform out;
// we just check the prefix.
func isGraphChannel(traceID string) bool {
	return len(traceID) > 6 && traceID[:6] == "graph:"
}

// ─── Worker → broker bridge ─────────────────────────────────────────────────

// SSEGraphPublisher implements GraphRebuildPublisher by forwarding flush
// events to the SSE broker on the graph:<tenant>:<platform> channel.
//
// The worker does not import sse directly; we pass a publisher into it so
// tests can supply a fake.
type SSEGraphPublisher struct {
	Broker sse.BrokerService
}

// PublishGraphIndexUpdated implements GraphRebuildPublisher.
func (p *SSEGraphPublisher) PublishGraphIndexUpdated(tenantID, platform string, materializedAt time.Time) {
	if p == nil || p.Broker == nil {
		return
	}
	p.Broker.Publish(graphBroadcastChannel(tenantID, platform), sse.GraphIndexUpdated{
		Tenant:         tenantID,
		Platform:       platform,
		MaterializedAt: materializedAt.UTC().Format(time.RFC3339),
	})
}
