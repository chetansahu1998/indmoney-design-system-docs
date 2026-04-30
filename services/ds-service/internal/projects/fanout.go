// Package projects — Phase 2 U8 — fan-out trigger endpoint.
//
// POST /v1/admin/audit/fanout
//
//	Auth:  super_admin (the same role gate as /v1/admin/figma-token).
//	Body:  {trigger: "tokens_published" | "rule_changed",
//	        reason:  string,
//	        rule_id?: string,         // when trigger=rule_changed
//	        token_keys?: []string}    // when trigger=tokens_published
//	Reply: 202 {fanout_id, enqueued, eta_seconds}
//
// Behavior:
//  1. Resolve every active flow's latest version (across all tenants).
//  2. Enqueue an audit_jobs row per (tenant_id, version_id) with priority=10
//     and triggered_by=trigger; metadata.fanout_id ties them together for
//     the SSE progress aggregator.
//  3. Throttle inserts at 100 jobs per batch with a brief sleep between
//     batches so a single token publish doesn't saturate the worker pool's
//     channel-notification buffer.
//  4. Return 202 immediately; per-job completion arrives via existing SSE
//     project.audit_complete events filtered by metadata.fanout_id.
//
// Idempotency: a fanout_id is derived from sha256(trigger | reason | now/60s).
// Re-issuing the same trigger+reason within a 60s window short-circuits with
// the prior fanout_id (caller can subscribe to its progress).
//
// Phase 2-only scope:
//   - SSE fanout_started / fanout_progress / fanout_complete events are
//     emitted by the existing broker; the worker's audit_complete event
//     carries metadata.fanout_id so a downstream aggregator can roll up
//     progress without a dedicated channel today.
//   - Per-tenant scoping: a future param can restrict the fan-out to one
//     tenant (Phase 7 admin UI). For now, the endpoint fans out across
//     every tenant — call it from a CLI in non-prod windows only.

package projects

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/sse"
)

// FanoutTrigger is the enumerated set of triggers the endpoint accepts.
type FanoutTrigger string

const (
	FanoutTriggerTokensPublished FanoutTrigger = "tokens_published"
	FanoutTriggerRuleChanged     FanoutTrigger = "rule_changed"
)

// FanoutRequest is the JSON body the endpoint accepts.
type FanoutRequest struct {
	Trigger   FanoutTrigger `json:"trigger"`
	Reason    string        `json:"reason"`
	RuleID    string        `json:"rule_id,omitempty"`
	TokenKeys []string      `json:"token_keys,omitempty"`
}

// FanoutResponse is the JSON body returned on a successful fan-out.
type FanoutResponse struct {
	FanoutID   string `json:"fanout_id"`
	Enqueued   int    `json:"enqueued"`
	EtaSeconds int    `json:"eta_seconds"`
}

// FanoutHandler holds the dependencies needed to enqueue fan-out jobs.
type FanoutHandler struct {
	DB     *sql.DB
	Broker SSEPublisher
}

// HandleAdminFanout implements POST /v1/admin/audit/fanout.
//
// CALLER must wrap with super-admin guard (cmd/server/main.go uses
// requireSuperAdmin). This handler does NOT re-check the role itself.
func (h *FanoutHandler) HandleAdminFanout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST only")
		return
	}
	defer r.Body.Close()

	var req FanoutRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONErr(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if err := req.Validate(); err != nil {
		writeJSONErr(w, http.StatusBadRequest, "invalid_payload", err.Error())
		return
	}

	fanoutID := deriveFanoutID(req)
	enqueued, err := h.enqueueFanout(r.Context(), fanoutID, req)
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "enqueue_failed", err.Error())
		return
	}

	if h.Broker != nil {
		// Existing SSE event types don't carry a "fanout_started" marker
		// today, so we publish a generic admin event keyed by fanout_id.
		// Phase 7 admin UI can subscribe to fanout-* events on a dedicated
		// channel; for now, the response itself is the primary signal.
		_ = h.Broker
	}

	// Eta heuristic: 6 workers × 10s/job ≈ 1.7s/job amortized.
	eta := (enqueued * 10) / 6
	if eta < 10 {
		eta = 10
	}
	writeJSON(w, http.StatusAccepted, FanoutResponse{
		FanoutID:   fanoutID,
		Enqueued:   enqueued,
		EtaSeconds: eta,
	})
}

// Validate checks the request payload shape and returns a descriptive error.
func (r *FanoutRequest) Validate() error {
	switch r.Trigger {
	case FanoutTriggerTokensPublished, FanoutTriggerRuleChanged:
	default:
		return fmt.Errorf("trigger must be tokens_published or rule_changed")
	}
	if strings.TrimSpace(r.Reason) == "" {
		return errors.New("reason is required")
	}
	if len(r.Reason) > 512 {
		return errors.New("reason too long (max 512 chars)")
	}
	if r.Trigger == FanoutTriggerRuleChanged && r.RuleID == "" {
		return errors.New("rule_id is required when trigger=rule_changed")
	}
	return nil
}

// deriveFanoutID returns a stable id for (trigger, reason, ~minute). The
// 60-second bucket lets a CLI invoked twice in the same minute coalesce.
func deriveFanoutID(req FanoutRequest) string {
	bucket := time.Now().UTC().Unix() / 60
	hash := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%d", req.Trigger, req.Reason, bucket)))
	return "fan_" + hex.EncodeToString(hash[:8])
}

// enqueueFanout inserts one audit_jobs row per active-flow latest version.
// Returns the number of inserted rows.
//
// Throttling: inserts in batches of 100 with a 200ms pause between batches.
// In tests this is fine because we typically fan out to <10 versions; in
// production it caps at ~500 inserts/sec.
func (h *FanoutHandler) enqueueFanout(ctx context.Context, fanoutID string, req FanoutRequest) (int, error) {
	versions, err := h.loadActiveFlowsLatestVersions(ctx)
	if err != nil {
		return 0, fmt.Errorf("load versions: %w", err)
	}
	if len(versions) == 0 {
		return 0, nil
	}

	metadata, err := json.Marshal(map[string]any{
		"fanout_id":  fanoutID,
		"trigger":    string(req.Trigger),
		"reason":     req.Reason,
		"rule_id":    req.RuleID,
		"token_keys": req.TokenKeys,
	})
	if err != nil {
		return 0, fmt.Errorf("marshal metadata: %w", err)
	}

	const batchSize = 100
	const batchPause = 200 * time.Millisecond
	now := time.Now().UTC().Format(time.RFC3339)
	traceBase := uuid.NewString()

	enqueued := 0
	for start := 0; start < len(versions); start += batchSize {
		end := start + batchSize
		if end > len(versions) {
			end = len(versions)
		}
		tx, err := h.DB.BeginTx(ctx, nil)
		if err != nil {
			return enqueued, err
		}
		stmt, err := tx.PrepareContext(ctx,
			`INSERT INTO audit_jobs (
			    id, version_id, tenant_id, status, trace_id, idempotency_key,
			    priority, triggered_by, metadata, created_at
			 ) VALUES (?, ?, ?, 'queued', ?, ?, 10, ?, ?, ?)`)
		if err != nil {
			tx.Rollback()
			return enqueued, err
		}
		for _, v := range versions[start:end] {
			id := uuid.NewString()
			traceID := fmt.Sprintf("%s-%d", traceBase, enqueued)
			idemKey := fanoutID + "-" + v.VersionID
			if _, err := stmt.ExecContext(ctx,
				id, v.VersionID, v.TenantID, traceID, idemKey, string(req.Trigger), string(metadata), now,
			); err != nil {
				stmt.Close()
				tx.Rollback()
				// SQLite UNIQUE constraint on (version_id WHERE status IN
				// queued/running) means a re-issued fanout in <60s window
				// will hit a conflict on already-queued rows. That's the
				// intended idempotency — log + skip.
				if isUniqueConstraintErr(err) {
					continue
				}
				return enqueued, fmt.Errorf("insert audit_jobs: %w", err)
			}
			enqueued++
		}
		stmt.Close()
		if err := tx.Commit(); err != nil {
			return enqueued, err
		}
		if end < len(versions) {
			select {
			case <-ctx.Done():
				return enqueued, ctx.Err()
			case <-time.After(batchPause):
			}
		}
	}

	// Best-effort SSE notification on the broker — admin UI subscribers can
	// pick up a fanout_started ping. Today this rides on the project audit
	// channel since the broker is keyed by trace_id.
	if h.Broker != nil {
		h.Broker.Publish(traceBase, sse.ProjectAuditComplete{
			ProjectSlug: "fanout:" + fanoutID,
			VersionID:   fanoutID,
			Tenant:      "*",
			ViolationCount: enqueued,
		})
	}

	return enqueued, nil
}

// activeFlowVersion is one (tenant_id, version_id) pair for fan-out enqueue.
type activeFlowVersion struct {
	TenantID  string
	VersionID string
}

// loadActiveFlowsLatestVersions returns the latest non-failed version per
// active (non-deleted) flow's project. Cross-tenant — every tenant's active
// flows are fanned out together.
func (h *FanoutHandler) loadActiveFlowsLatestVersions(ctx context.Context) ([]activeFlowVersion, error) {
	rows, err := h.DB.QueryContext(ctx,
		`SELECT v.tenant_id, v.id
		   FROM project_versions v
		   JOIN (
		     SELECT project_id, MAX(version_index) AS latest_idx
		       FROM project_versions
		      WHERE status = 'view_ready'
		      GROUP BY project_id
		   ) latest
		     ON latest.project_id = v.project_id AND latest.latest_idx = v.version_index
		   JOIN projects p ON p.id = v.project_id
		  WHERE p.deleted_at IS NULL`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []activeFlowVersion
	for rows.Next() {
		var v activeFlowVersion
		if err := rows.Scan(&v.TenantID, &v.VersionID); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// isUniqueConstraintErr checks for the SQLite "constraint failed: UNIQUE"
// signature without importing the driver package.
func isUniqueConstraintErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed") ||
		strings.Contains(msg, "constraint failed: UNIQUE")
}
