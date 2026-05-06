// prerender_status.go — operational observability for Stage 9 cluster
// prerender. U8 of plan 2026-05-06-003.
//
// Pre-fix, the only signal an operator had for "did Stage 9 succeed for
// version X" was log grep. Logs are durable but search-hostile: with
// the U2 sampled-Warn cap (~50 lines/run) finding the relevant version
// in production log volume took minutes. The aggregate Info line at
// end of Phase 2 carries rendered/failed counts but is buried among
// other pipeline log lines.
//
// This module adds a process-wide ring buffer of the last N prerender
// runs and an admin-gated HTTP endpoint that returns the buffer as
// JSON. In-memory only — process restart drops the history. Acceptable
// because the on-demand HandleAssetDownload path is the actual recovery
// mechanism for any Stage 9 miss; the status buffer is for triage, not
// for reconciliation.
package projects

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// PrerenderRun captures one Stage 9 invocation's outcome.
type PrerenderRun struct {
	VersionID     string    `json:"version_id"`
	FileID        string    `json:"file_id"`
	TenantID      string    `json:"tenant_id"`
	StartedAt     time.Time `json:"started_at"`
	FinishedAt    time.Time `json:"finished_at"`
	DurationMs    int64     `json:"duration_ms"`
	TotalClusters int       `json:"total_clusters"`
	Rendered      int       `json:"rendered"`
	Failed        int       `json:"failed"`
	// Skipped counts nodes where Phase 2's gate short-circuited (Phase 1
	// didn't cache them). Not currently populated by PrerenderClusters
	// — reserved for future enrichment.
	Skipped int `json:"skipped"`
	// Phase1Aborted is true when the consecutive-failure circuit-breaker
	// tripped during Phase 1 (Figma sustained-degraded for this run).
	Phase1Aborted bool `json:"phase1_aborted"`
	// SampleErrors carries up to SampleErrorCap error strings from
	// Phase 2 (mix of render and cache-write failures). The full list
	// is not retained — readers wanting more depth grep the run logs by
	// version_id.
	SampleErrors []string `json:"sample_errors,omitempty"`
	// Outcome is a coarse classification for dashboard charting:
	//   "ok"           — rendered > 0 and failed == 0
	//   "partial"      — rendered > 0 and failed > 0
	//   "all_failed"   — rendered == 0 and failed > 0
	//   "no_clusters"  — total_clusters == 0 (degenerate case, no work)
	//   "setup_error"  — PrerenderClusters returned a setup-level error
	//                    (nil deps, type-assert fail, leaf lookup fail)
	Outcome string `json:"outcome"`
	// SetupError is the error string when Outcome=="setup_error", empty
	// otherwise. Preserved separately so dashboards can filter on
	// outcome without parsing freeform error text.
	SetupError string `json:"setup_error,omitempty"`
}

// PrerenderStatusBuffer is a thread-safe ring buffer of the last N
// prerender runs. Zero value is NOT usable — construct via
// NewPrerenderStatusBuffer.
type PrerenderStatusBuffer struct {
	mu       sync.Mutex
	capacity int
	runs     []PrerenderRun // ring; head at idx (next write position)
	idx      int
	full     bool // whether we've wrapped at least once
}

// PrerenderStatusBufferCapacity is the default ring depth. 256 covers
// ~a day of imports at one tenant's typical cadence (~1 import per
// minute peak), which is enough for triage without unbounded memory.
const PrerenderStatusBufferCapacity = 256

// NewPrerenderStatusBuffer constructs a buffer with the given capacity.
// capacity <= 0 falls back to the default.
func NewPrerenderStatusBuffer(capacity int) *PrerenderStatusBuffer {
	if capacity <= 0 {
		capacity = PrerenderStatusBufferCapacity
	}
	return &PrerenderStatusBuffer{
		capacity: capacity,
		runs:     make([]PrerenderRun, capacity),
	}
}

// Append records a completed run. Safe for concurrent use.
func (b *PrerenderStatusBuffer) Append(run PrerenderRun) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.runs[b.idx] = run
	b.idx++
	if b.idx >= b.capacity {
		b.idx = 0
		b.full = true
	}
}

// Snapshot returns the buffer's contents in chronological order
// (oldest first). Optional filterVersionID restricts the result to
// runs matching that version_id; empty string returns everything.
func (b *PrerenderStatusBuffer) Snapshot(filterVersionID string) []PrerenderRun {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	// Determine the chronological start index. If we've wrapped,
	// b.idx points to the OLDEST entry; otherwise the buffer is filling
	// from index 0 and the oldest is also 0.
	var size int
	var start int
	if b.full {
		size = b.capacity
		start = b.idx
	} else {
		size = b.idx
		start = 0
	}
	out := make([]PrerenderRun, 0, size)
	for i := 0; i < size; i++ {
		r := b.runs[(start+i)%b.capacity]
		if filterVersionID != "" && r.VersionID != filterVersionID {
			continue
		}
		out = append(out, r)
	}
	return out
}

// ClassifyOutcome derives the Outcome field from counters + setup error.
// Exported so pipeline.go can stamp the field at spawn-site error paths
// without duplicating the enum logic.
func ClassifyOutcome(totalClusters, rendered, failed int, setupErr error) string {
	if setupErr != nil {
		return "setup_error"
	}
	if totalClusters == 0 {
		return "no_clusters"
	}
	if rendered > 0 && failed == 0 {
		return "ok"
	}
	if rendered > 0 && failed > 0 {
		return "partial"
	}
	return "all_failed"
}

// HandlePrerenderStatus returns an http.HandlerFunc that serves the
// buffer as JSON. Wire under requireSuperAdmin in main.go — the
// runs include tenant_id and version_id which are operator-internal.
//
// Query params:
//
//	?version_id=X — filter to a single version
//
// Response:
//
//	{ "runs": [...], "count": N, "capacity": M }
func HandlePrerenderStatus(b *PrerenderStatusBuffer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if b == nil {
			http.Error(w, `{"error":"prerender status buffer not configured"}`, http.StatusServiceUnavailable)
			return
		}
		filter := r.URL.Query().Get("version_id")
		runs := b.Snapshot(filter)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"runs":     runs,
			"count":    len(runs),
			"capacity": b.capacity,
		})
	}
}
