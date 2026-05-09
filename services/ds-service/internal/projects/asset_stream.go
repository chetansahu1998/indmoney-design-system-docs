package projects

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/auth"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/sse"
)

// asset_stream.go — per-leaf SSE asset hydration.
//
// Why this exists: when a designer opens a leaf with cold cache, the frontend
// fires N parallel `<img src=…/assets/<node>>` GETs. Each cache miss runs a
// synchronous Figma render under a 5-req/sec per-tenant limiter, with a 30s
// budget per asset. With 30+ misses on a fresh leaf, browsers stall on the
// HTTP/1.1 6-conn cap, most renders surface as 425, and the canvas shows
// dashed placeholders for minutes (see docs/2026-05-09 sub-leaf perf notes).
//
// This endpoint flips the pattern: ONE long-lived SSE stream per leaf-open
// drives all cluster renders server-side and streams `asset-ready` events as
// each one lands. Cache hits are emitted instantly with a freshly-minted
// signed URL; misses go through the existing PreviewPyramid path (one Figma
// render → all 4 tiers persisted). The frontend swaps each placeholder for
// the real image as events arrive — no per-cluster mint round-trips, no
// 425-retry storms, no dependency on Stage 9 having finished.
//
// Auth pattern mirrors HandleGraphEvents: a POST issues a 60s single-use
// ticket bound to a synthetic traceID; the GET stream redeems the ticket
// and verifies the channel shape. Native EventSource can't send custom
// headers, so the ticket-in-query pattern is the established workaround.

// assetStreamChannel returns the synthetic SSE channel/traceID name for a
// per-leaf asset stream. Mirrors graphBroadcastChannel naming so future
// reads of broker activity can grep across channel families.
func assetStreamChannel(tenantID, leafID string) string {
	return "assets:" + tenantID + ":" + leafID
}

// isAssetStreamChannel reports whether the ticket-bound trace ID matches
// the assets:<tenant>:<leafID> shape. The redeem path uses this to defend
// against a ticket bound to a different SSE channel being replayed here.
func isAssetStreamChannel(traceID string) bool {
	return strings.HasPrefix(traceID, "assets:")
}

// parseAssetStreamChannel extracts (tenantID, leafID) from a synthetic
// asset-stream traceID. Returns ok=false when the shape doesn't match —
// callers MUST nil/empty-check before trusting the return values.
func parseAssetStreamChannel(traceID string) (tenantID, leafID string, ok bool) {
	if !isAssetStreamChannel(traceID) {
		return "", "", false
	}
	rest := traceID[len("assets:"):]
	idx := strings.IndexByte(rest, ':')
	if idx <= 0 || idx == len(rest)-1 {
		return "", "", false
	}
	return rest[:idx], rest[idx+1:], true
}

// AssetStreamConcurrency caps the number of in-flight renders per leaf
// stream. Each render waits on figmaProxyLimiter (5 req/sec per tenant) so
// values much higher than 4 just queue inside the limiter. Kept at 4 to
// match DefaultClusterPrerenderConfig.Concurrency for parity with Stage 9.
const AssetStreamConcurrency = 4

// AssetStreamPerNodeBudget bounds the wall-clock spent on a single
// cluster's render. Mirrors SingleAssetSyncRenderBudget — a per-node
// timeout protects the rest of the stream from one stuck Figma render.
const AssetStreamPerNodeBudget = 30 * time.Second

// AssetStreamTotalBudget bounds the entire stream's lifetime. Bigger than
// per-node budget × concurrency because cache hits emit instantly and
// real-world leafs have a long-tail of clusters; we'd rather keep streaming
// for 90s than truncate before slow renders land. Beyond this, emit
// `complete` and close so the frontend's onComplete handler runs and
// stragglers fall through to the existing on-demand path.
const AssetStreamTotalBudget = 90 * time.Second

// AssetStreamDefaultTier is the preview tier emitted by default. preview-128
// fits a fully zoomed-out leaf canvas (each frame ~150px on screen), so a
// successful render here is enough for first-paint. Higher tiers come from
// the existing on-demand path when the user zooms in — and because
// PreviewPyramidGenerator materializes ALL tiers in one render, those
// zoomed-in fetches hit cache.
const AssetStreamDefaultTier = "preview-128"

// HandleAssetStreamTicket serves
//
//	POST /v1/projects/{slug}/leaves/{leafID}/asset-stream/ticket
//
// Issues a single-use 60s ticket bound to assets:<tenant>:<leafID>. The
// frontend calls this once per leaf-open then opens an EventSource on
// /asset-stream?ticket=…
//
// Tenant-scoping happens via the JWT-derived tenantID; a leafID belonging to
// a different tenant produces a 404 (no existence oracle).
func (s *Server) HandleAssetStreamTicket(w http.ResponseWriter, r *http.Request) {
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
	slug := r.PathValue("slug")
	leafID := r.PathValue("leaf_id")
	if slug == "" || leafID == "" {
		writeJSONErr(w, http.StatusBadRequest, "missing_params", "slug,leafID required")
		return
	}

	// Verify the leaf belongs to the project. After the brain-products
	// migration the frontend passes the project slug as leaf_id (see
	// screen_image_fills.go:135 for the same pattern); resolve to a real
	// flow UUID before LookupLeafFigmaContext, which expects a flow id.
	repo := NewTenantRepo(s.deps.DB.DB, tenantID)
	resolvedLeafID := leafID
	if leafID == slug {
		flowID, ferr := repo.PrimaryFlowIDForSlug(r.Context(), slug)
		if ferr != nil {
			if errors.Is(ferr, ErrNotFound) {
				http.NotFound(w, r)
				return
			}
			writeJSONErr(w, http.StatusInternalServerError, "primary_flow", ferr.Error())
			return
		}
		resolvedLeafID = flowID
	}
	if _, _, err := repo.LookupLeafFigmaContext(r.Context(), resolvedLeafID); err != nil {
		if errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		writeJSONErr(w, http.StatusInternalServerError, "leaf_lookup", err.Error())
		return
	}

	traceID := assetStreamChannel(tenantID, leafID)
	ticket := s.deps.Tickets.IssueTicket(claims.Sub, tenantID, traceID, sse.DefaultTicketTTL)
	writeJSON(w, http.StatusOK, map[string]any{
		"ticket":     ticket,
		"trace_id":   traceID,
		"expires_in": int(sse.DefaultTicketTTL.Seconds()),
	})
}

// HandleAssetStream serves
//
//	GET /v1/projects/{slug}/leaves/{leafID}/asset-stream?ticket=…
//
// Redeems the ticket, opens a text/event-stream, walks every screen of the
// leaf's latest version, dedupes cluster IDs, and streams `asset-ready`
// events (one per cluster) as each one's preview pyramid lands. Cache hits
// emit instantly; misses route through PreviewPyramidGenerator + the
// per-tenant figmaProxyLimiter.
//
// Stream lifecycle:
//   - on every cluster persisted: `event: asset-ready\ndata: {node_id,format,url}\n\n`
//   - on per-cluster failure:     `event: asset-failed\ndata: {node_id,reason}\n\n`
//   - on final close:             `event: complete\ndata: {total,rendered,failed}\n\n`
//
// Heartbeats every 15s as a comment line so proxies (Cloudflare Tunnel,
// nginx) don't idle-close the connection. Mirrors HandleGraphEvents.
func (s *Server) HandleAssetStream(w http.ResponseWriter, r *http.Request) {
	if s.deps.Tickets == nil {
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
	_, ticketTenant, traceID, ok := s.deps.Tickets.RedeemTicket(ticket)
	if !ok {
		writeJSONErr(w, http.StatusUnauthorized, "invalid_ticket", "")
		return
	}
	if !isAssetStreamChannel(traceID) {
		writeJSONErr(w, http.StatusForbidden, "wrong_channel",
			"ticket bound to a non-asset-stream channel")
		return
	}
	tenantID, leafID, ok := parseAssetStreamChannel(traceID)
	if !ok || tenantID != ticketTenant {
		writeJSONErr(w, http.StatusForbidden, "wrong_channel", "ticket scope mismatch")
		return
	}

	// Verify path slug + leafID match the ticket. Defends against a ticket
	// minted for one (slug, leaf) being replayed against another. The ticket
	// already binds tenant+leaf via traceID; this adds the slug check.
	slug := r.PathValue("slug")
	pathLeafID := r.PathValue("leaf_id")
	if pathLeafID != leafID {
		writeJSONErr(w, http.StatusForbidden, "leaf_mismatch", "ticket bound to different leaf")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSONErr(w, http.StatusInternalServerError, "no_streaming", "")
		return
	}

	// Resolve leaf → file_id, version_index. Required for cache lookups +
	// pyramid persistence. A ticket already passed the LookupLeafFigmaContext
	// check at issuance time, but tenants can change repo state between
	// issuance and redemption (mid-import deletion); fail with 404 here too.
	//
	// Apply the same slug-as-leafID fallback as the ticket handler (and
	// screen_image_fills.go:135) so the post-brain-products frontend works.
	repo := NewTenantRepo(s.deps.DB.DB, tenantID)
	resolvedLeafID := leafID
	if leafID == slug {
		flowID, ferr := repo.PrimaryFlowIDForSlug(r.Context(), slug)
		if ferr != nil {
			if errors.Is(ferr, ErrNotFound) {
				http.NotFound(w, r)
				return
			}
			writeJSONErr(w, http.StatusInternalServerError, "primary_flow", ferr.Error())
			return
		}
		resolvedLeafID = flowID
	}
	fileID, versionIndex, err := repo.LookupLeafFigmaContext(r.Context(), resolvedLeafID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		writeJSONErr(w, http.StatusInternalServerError, "leaf_lookup", err.Error())
		return
	}

	// Collect cluster IDs across every screen of the leaf's latest version.
	// One SQL query, decompress per row, dedup IDs across screens. Empty
	// result is fine — no clusters means we close the stream immediately
	// after the headers; the frontend's onComplete fires and falls through.
	trees, err := repo.ListCanonicalTreesForFlow(r.Context(), resolvedLeafID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		writeJSONErr(w, http.StatusInternalServerError, "list_trees", err.Error())
		return
	}
	clusterIDs := dedupeClusterIDs(trees)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable nginx buffering
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// Empty-cluster path: nothing to stream, send `complete` and exit. The
	// frontend's onComplete handler closes the EventSource cleanly.
	if len(clusterIDs) == 0 {
		writeStreamEvent(w, flusher, "complete", assetStreamComplete{Total: 0})
		return
	}

	streamCtx, cancel := context.WithTimeout(r.Context(), AssetStreamTotalBudget)
	defer cancel()

	streamLeafAssets(streamCtx, s, w, flusher,
		tenantID, slug, resolvedLeafID, fileID, versionIndex, clusterIDs)
}

// dedupeClusterIDs walks every per-screen canonical_tree and returns the
// set of unique cluster IDs in deterministic order (first-seen wins).
// Mirrors pipeline.go Stage 9's per-version dedup.
func dedupeClusterIDs(trees []CanonicalTreeResult) []string {
	if len(trees) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(trees)*8)
	out := make([]string, 0, len(trees)*8)
	for _, t := range trees {
		for _, id := range ExtractClusterIDs([]byte(t.Tree)) {
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}
			out = append(out, id)
		}
	}
	return out
}

// streamLeafAssets is the per-stream worker. For each cluster ID:
//
//   - check cache: if every tier of the default tier exists, emit asset-ready
//     immediately with a freshly-minted signed URL.
//   - on miss: kick off PreviewPyramidGenerator.RenderPreviewPyramid (one
//     Figma render → 4 tiers persisted), then emit asset-ready on success.
//
// Concurrency = AssetStreamConcurrency goroutines (semaphore). All renders
// share the same per-tenant figmaProxyLimiter as Stage 9 + on-demand
// HandleAssetDownload, so we never exceed Figma's 5-req/sec budget.
//
// A serializeMu mutex guards writes to the http.ResponseWriter — concurrent
// goroutines must not interleave SSE frames.
func streamLeafAssets(
	ctx context.Context,
	s *Server,
	w http.ResponseWriter,
	flusher http.Flusher,
	tenantID, slug, leafID, fileID string,
	versionIndex int,
	clusterIDs []string,
) {
	tier, _ := ParsePreviewTierFormat(AssetStreamDefaultTier)
	tierFormat := tier.FormatString()

	repo := NewTenantRepo(s.deps.DB.DB, tenantID)

	var rendered, failed atomic.Int64
	var serializeMu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, AssetStreamConcurrency)

	// Heartbeat goroutine. Keeps the connection alive across proxies during
	// long Figma renders. Holds serializeMu while writing so it doesn't
	// interleave with event frames.
	hbCtx, hbCancel := context.WithCancel(ctx)
	defer hbCancel()
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-hbCtx.Done():
				return
			case <-ticker.C:
				serializeMu.Lock()
				_, _ = w.Write([]byte(": keepalive\n\n"))
				flusher.Flush()
				serializeMu.Unlock()
			}
		}
	}()

	emit := func(eventName string, payload any) {
		serializeMu.Lock()
		defer serializeMu.Unlock()
		writeStreamEvent(w, flusher, eventName, payload)
	}

dispatch:
	for _, nodeID := range clusterIDs {
		if ctx.Err() != nil {
			break
		}
		nodeID := nodeID
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			break dispatch
		}
		wg.Add(1)
		go func() {
			defer func() {
				if rec := recover(); rec != nil {
					failed.Add(1)
					emit("asset-failed", assetStreamFailed{
						NodeID: nodeID,
						Reason: fmt.Sprintf("panic: %v", rec),
					})
				}
			}()
			defer func() {
				<-sem
				wg.Done()
			}()

			nodeCtx, nodeCancel := context.WithTimeout(ctx, AssetStreamPerNodeBudget)
			defer nodeCancel()

			// Cache fast-path. LookupAsset for the default tier; if hit,
			// emit the signed URL directly — zero Figma traffic.
			row, hit, lerr := repo.LookupAsset(nodeCtx, tenantID, fileID, nodeID, tierFormat, 1, versionIndex)
			if lerr == nil && hit && row.StorageKey != "" {
				rendered.Add(1)
				emit("asset-ready", buildAssetReadyEvent(s, tenantID, slug, fileID, nodeID, tierFormat, 1))
				return
			}

			// Cache miss → render the pyramid. PreviewPyramid is wired in
			// production; tests that don't wire it surface as failed.
			if s.deps.PreviewPyramid == nil {
				failed.Add(1)
				emit("asset-failed", assetStreamFailed{
					NodeID: nodeID,
					Reason: "preview_pyramid_unavailable",
				})
				return
			}

			results, perr := s.deps.PreviewPyramid.RenderPreviewPyramid(
				nodeCtx, tenantID, leafID, fileID, nodeID, versionIndex,
			)
			if errors.Is(perr, context.DeadlineExceeded) || errors.Is(perr, context.Canceled) {
				failed.Add(1)
				emit("asset-failed", assetStreamFailed{NodeID: nodeID, Reason: "render_timeout"})
				return
			}
			if perr != nil && len(results) == 0 {
				failed.Add(1)
				emit("asset-failed", assetStreamFailed{NodeID: nodeID, Reason: perr.Error()})
				return
			}

			// Persist each successfully-rendered tier.
			now := s.deps.PreviewPyramid.now()
			persistedDefault := false
			for _, pr := range results {
				crow := AssetCacheRow{
					TenantID:     tenantID,
					FileID:       fileID,
					NodeID:       nodeID,
					Format:       pr.Tier.FormatString(),
					Scale:        1,
					VersionIndex: versionIndex,
					StorageKey:   pr.StorageKey,
					Bytes:        pr.Bytes,
					Mime:         pr.Mime,
					CreatedAt:    now,
				}
				if serr := repo.StoreAsset(nodeCtx, crow); serr != nil {
					// One tier failed to persist; keep going. Other tiers
					// may still be usable.
					continue
				}
				if pr.Tier.FormatString() == tierFormat {
					persistedDefault = true
				}
			}
			if !persistedDefault {
				failed.Add(1)
				emit("asset-failed", assetStreamFailed{
					NodeID: nodeID,
					Reason: "default_tier_not_persisted",
				})
				return
			}
			rendered.Add(1)
			emit("asset-ready", buildAssetReadyEvent(s, tenantID, slug, fileID, nodeID, tierFormat, 1))
		}()
	}
	wg.Wait()

	// Final summary event so the frontend's onComplete handler can close the
	// EventSource cleanly and run the residual mint pass for any IDs that
	// failed (so they hit the existing on-demand path with its own retry).
	emit("complete", assetStreamComplete{
		Total:    len(clusterIDs),
		Rendered: int(rendered.Load()),
		Failed:   int(failed.Load()),
	})
}

// buildAssetReadyEvent mints a signed URL for (nodeID, format, scale) and
// returns the wire-shaped event payload. Mirrors HandleMintAssetExportToken's
// composite-key signing so the GET /assets/<node> handler verifies cleanly.
//
// fileID is required: the AssetSigner composite is (tenant|file|node|format|
// scale), so a missing file_id would mint a token that fails verification at
// download time. Callers thread fileID through from the per-stream resolve.
func buildAssetReadyEvent(s *Server, tenantID, slug, fileID, nodeID, format string, scale int) assetStreamReady {
	composite := singleAssetTokenKey(fileID, nodeID, format, scale)
	token := ""
	if s.deps.AssetSigner != nil {
		token = s.deps.AssetSigner.Mint(tenantID, composite, AssetExportTokenTTL)
	}
	url := fmt.Sprintf("/v1/projects/%s/assets/%s?format=%s&scale=%d&at=%s",
		slug, nodeID, format, scale, token)
	return assetStreamReady{
		NodeID: nodeID,
		Format: format,
		URL:    url,
	}
}

// writeStreamEvent serializes a typed payload as JSON and writes one SSE
// `event: <name>\ndata: <json>\n\n` frame, then flushes. Caller must hold
// the serialization mutex when concurrent writers exist.
func writeStreamEvent(w http.ResponseWriter, flusher http.Flusher, eventName string, payload any) {
	body, err := json.Marshal(payload)
	if err != nil {
		body = []byte(`{}`)
	}
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventName, body)
	flusher.Flush()
}

// ─── Wire shapes ────────────────────────────────────────────────────────────

// assetStreamReady is the JSON body of an `asset-ready` event. Frontend
// reads `node_id` to key into its cluster-URL Map and swaps the placeholder
// for an `<img src=url>`.
type assetStreamReady struct {
	NodeID string `json:"node_id"`
	Format string `json:"format"`
	URL    string `json:"url"`
}

// assetStreamFailed is the JSON body of an `asset-failed` event. Reason is
// a short tag (`render_timeout`, `node_not_renderable`, etc) plus the
// error string for ops triage; the frontend logs the reason and leaves the
// placeholder dashed for fallback retry via the on-demand path.
type assetStreamFailed struct {
	NodeID string `json:"node_id"`
	Reason string `json:"reason"`
}

// assetStreamComplete fires once when every cluster has been processed (or
// the total budget elapses). Frontend uses this as the cue to close the
// EventSource and run the residual mint pass for any IDs not seen in
// asset-ready / asset-failed events (network blip / mid-stream disconnect).
type assetStreamComplete struct {
	Total    int `json:"total"`
	Rendered int `json:"rendered"`
	Failed   int `json:"failed"`
}
