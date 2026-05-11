package projects

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// figma_blocklist.go — known-bad-frame skip list (2026-05-12).
//
// Persistent memory of Figma render failures so we stop re-attempting
// frames Figma deterministically can't render. Pre-fix, every sync
// cycle + Stage 9 pre-render + on-demand fetch re-tried the same broken
// (file_id, node_id) pairs, burning the per-PAT rate-limit budget and
// still failing. With this layer:
//
//   1. After N consecutive failures, callers MarkFigmaRenderFailure
//      which inserts/updates a row with cooldown_until = now + 24h.
//   2. Before calling Figma, callers IsFigmaRenderBlocked which checks
//      both presence and cooldown freshness.
//   3. On a successful render (cache hit or fresh upstream OK),
//      callers ClearFigmaRenderFailure removes the row — the
//      underlying upstream bug fixed itself OR the designer touched
//      the frame.
//   4. On canonical_tree hash change for the screen containing the
//      blocked node, the next sync auto-invalidates the row (the
//      designer's edit MAY have resolved the render bug).
//
// Storage: migration 0023 creates the figma_render_blocklist table.
// All access is tenant-scoped — Figma render bugs are per-(file, node)
// but the tenant scope mirrors the rest of the repo and keeps the
// blast radius contained if a node ID is somehow reused across tenants.

// BlocklistFailureThreshold is the number of consecutive same-error
// failures before we insert into the blocklist. Set conservatively:
// genuine transient errors (429, network blips) clear inside 2-3
// attempts; the threshold catches deterministic upstream failures
// without poisoning the entry on flaky-network noise.
const BlocklistFailureThreshold = 3

// BlocklistCooldown is the default skip window after insertion. After
// this window callers will re-attempt the frame once; success clears
// the row, failure replaces it. 24h balances "give Figma a chance to
// recover" against "don't burn budget every cycle".
const BlocklistCooldown = 24 * time.Hour

// FigmaRenderBlockEntry is the surfaced row shape for read APIs.
type FigmaRenderBlockEntry struct {
	TenantID            string
	FileID              string
	NodeID              string
	FirstFailureAt      time.Time
	LastFailureAt       time.Time
	ConsecutiveFailures int
	LastError           string
	CooldownUntil       time.Time
	ClearHash           string // canonical_tree hash at time of insert; empty if none recorded
}

// IsFigmaRenderBlocked reports whether (file_id, node_id) is currently
// in the blocklist AND inside its cooldown window. A stale row (past
// cooldown_until) returns false here so the caller re-attempts; a
// subsequent failure either clears or refreshes the row.
//
// Returns the entry on a true result so callers can include the
// last_error in their own log/telemetry for triage.
func (t *TenantRepo) IsFigmaRenderBlocked(ctx context.Context, fileID, nodeID string) (*FigmaRenderBlockEntry, bool, error) {
	if t.tenantID == "" {
		return nil, false, errors.New("projects: tenant_id required")
	}
	if fileID == "" || nodeID == "" {
		return nil, false, nil
	}
	row := t.r.db.QueryRowContext(ctx,
		`SELECT first_failure_at, last_failure_at, consecutive_failures, last_error, cooldown_until, COALESCE(clear_hash, '')
		   FROM figma_render_blocklist
		  WHERE tenant_id = ? AND file_id = ? AND node_id = ?`,
		t.tenantID, fileID, nodeID,
	)
	var entry FigmaRenderBlockEntry
	var firstStr, lastStr, cooldownStr string
	if err := row.Scan(&firstStr, &lastStr, &entry.ConsecutiveFailures, &entry.LastError, &cooldownStr, &entry.ClearHash); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("blocklist lookup: %w", err)
	}
	entry.TenantID = t.tenantID
	entry.FileID = fileID
	entry.NodeID = nodeID
	entry.FirstFailureAt = parseTime(firstStr)
	entry.LastFailureAt = parseTime(lastStr)
	entry.CooldownUntil = parseTime(cooldownStr)

	// If we're still inside the cooldown window, the entry is active.
	if t.now().UTC().Before(entry.CooldownUntil) {
		return &entry, true, nil
	}
	// Past cooldown — entry is stale. Return it so the caller can
	// decide what to do (typically: re-attempt). Active=false.
	return &entry, false, nil
}

// MarkFigmaRenderFailure records a single render failure for (file_id,
// node_id). Returns the resulting blocklist entry IF the threshold was
// crossed (consecutive_failures >= BlocklistFailureThreshold) — at
// which point future IsFigmaRenderBlocked calls will return active=true
// until the cooldown elapses.
//
// Behavior:
//   - No row exists → insert with consecutive_failures=1 (no cooldown yet).
//     Returns (nil, nil) because the threshold isn't met.
//   - Row exists, last failure inside cooldown or recent (<1h) → increment.
//   - Row exists, last failure OLDER than 1h but pre-threshold → reset to 1.
//     (a transient failure days apart isn't deterministic; reset the counter)
//   - consecutive_failures crosses threshold → set cooldown_until = now + 24h.
//     Returns (entry, nil) so callers can log "node X is now blocklisted".
func (t *TenantRepo) MarkFigmaRenderFailure(ctx context.Context, fileID, nodeID, errMsg, clearHash string) (*FigmaRenderBlockEntry, error) {
	if t.tenantID == "" {
		return nil, errors.New("projects: tenant_id required")
	}
	if fileID == "" || nodeID == "" {
		return nil, nil
	}
	now := t.now().UTC()
	// Look up existing row.
	existing, _, lookupErr := t.IsFigmaRenderBlocked(ctx, fileID, nodeID)
	if lookupErr != nil {
		return nil, lookupErr
	}

	nowStr := rfc3339(now)
	var consecutive int
	var firstFailureAt time.Time
	if existing == nil {
		consecutive = 1
		firstFailureAt = now
	} else {
		consecutive = existing.ConsecutiveFailures + 1
		firstFailureAt = existing.FirstFailureAt
		// If last failure is old (>1h ago) AND we haven't crossed the
		// threshold yet, this is unlikely to be deterministic — reset
		// to 1 so a flaky-network history doesn't poison new attempts.
		if consecutive < BlocklistFailureThreshold && now.Sub(existing.LastFailureAt) > time.Hour {
			consecutive = 1
			firstFailureAt = now
		}
	}

	// Cooldown only kicks in at the threshold; below that we record
	// the failure but allow immediate re-attempt (the caller's own
	// retry helper handles the in-cycle backoff).
	var cooldownUntil time.Time
	if consecutive >= BlocklistFailureThreshold {
		cooldownUntil = now.Add(BlocklistCooldown)
	} else {
		// Use a past timestamp so IsFigmaRenderBlocked correctly reports
		// active=false. We still record the row so the counter persists
		// across cycles.
		cooldownUntil = now
	}

	_, err := t.r.db.ExecContext(ctx,
		`INSERT INTO figma_render_blocklist (
			tenant_id, file_id, node_id,
			first_failure_at, last_failure_at, consecutive_failures,
			last_error, cooldown_until, clear_hash
		 ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(tenant_id, file_id, node_id) DO UPDATE SET
			last_failure_at      = excluded.last_failure_at,
			consecutive_failures = excluded.consecutive_failures,
			last_error           = excluded.last_error,
			cooldown_until       = excluded.cooldown_until,
			-- Preserve the original first_failure_at when consecutive
			-- failures span beyond the 1h reset window — keeps the
			-- "this has been broken since X" signal accurate.
			first_failure_at     = CASE
			                          WHEN excluded.consecutive_failures = 1 THEN excluded.first_failure_at
			                          ELSE figma_render_blocklist.first_failure_at
			                       END,
			-- Refresh clear_hash on every update so a designer edit that
			-- arrives mid-cooldown is reflected next cycle.
			clear_hash           = excluded.clear_hash`,
		t.tenantID, fileID, nodeID,
		rfc3339(firstFailureAt), nowStr, consecutive,
		errMsg, rfc3339(cooldownUntil), clearHash,
	)
	if err != nil {
		return nil, fmt.Errorf("blocklist upsert: %w", err)
	}

	if consecutive < BlocklistFailureThreshold {
		return nil, nil
	}
	return &FigmaRenderBlockEntry{
		TenantID:            t.tenantID,
		FileID:              fileID,
		NodeID:              nodeID,
		FirstFailureAt:      firstFailureAt,
		LastFailureAt:       now,
		ConsecutiveFailures: consecutive,
		LastError:           errMsg,
		CooldownUntil:       cooldownUntil,
		ClearHash:           clearHash,
	}, nil
}

// ClearFigmaRenderFailure deletes the row for (file_id, node_id). Called
// when:
//   - A render attempt succeeds (Stage 9 cluster prerender or
//     HandleAssetDownload on-demand path).
//   - The canonical_tree hash for the screen containing this node has
//     changed since the row was inserted (designer touched the file —
//     their edit may have resolved the upstream bug; let the next
//     attempt re-test from scratch).
//
// Safe to call when no row exists; returns nil in that case.
func (t *TenantRepo) ClearFigmaRenderFailure(ctx context.Context, fileID, nodeID string) error {
	if t.tenantID == "" {
		return errors.New("projects: tenant_id required")
	}
	if fileID == "" || nodeID == "" {
		return nil
	}
	_, err := t.r.db.ExecContext(ctx,
		`DELETE FROM figma_render_blocklist
		  WHERE tenant_id = ? AND file_id = ? AND node_id = ?`,
		t.tenantID, fileID, nodeID,
	)
	if err != nil {
		return fmt.Errorf("blocklist clear: %w", err)
	}
	return nil
}

// ClearFigmaRenderFailuresForHashChange clears every blocklist row for
// (file_id, *) where the stored clear_hash != currentHash. Used at the
// top of Stage 9 prerender once the freshly-extracted canonical_tree is
// available: if the hash differs from the last failure-time snapshot,
// the designer edited the frame's enclosing screen and we should treat
// the underlying render bug as potentially resolved.
//
// fileID alone is the partition key here; we don't need to scope by
// node because the typical signal "designer edited THIS file" justifies
// reattempting every blocklisted node in the file. False-positive cost
// is low: one extra render attempt per cleared node, then either it
// succeeds (good) or it re-fails and the next mark inserts a fresh row.
func (t *TenantRepo) ClearFigmaRenderFailuresForHashChange(ctx context.Context, fileID, currentHash string) (int, error) {
	if t.tenantID == "" {
		return 0, errors.New("projects: tenant_id required")
	}
	if fileID == "" || currentHash == "" {
		return 0, nil
	}
	res, err := t.r.db.ExecContext(ctx,
		`DELETE FROM figma_render_blocklist
		  WHERE tenant_id = ? AND file_id = ?
		    AND COALESCE(clear_hash, '') != ?`,
		t.tenantID, fileID, currentHash,
	)
	if err != nil {
		return 0, fmt.Errorf("blocklist hash-clear: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// ListFigmaRenderBlocklist returns all active blocklist rows for the
// tenant. Used by the admin GET endpoint so operators can see which
// frames are currently suppressed and surface them to designers.
//
// Ordered by last_failure_at desc so freshest failures show first.
func (t *TenantRepo) ListFigmaRenderBlocklist(ctx context.Context) ([]FigmaRenderBlockEntry, error) {
	if t.tenantID == "" {
		return nil, errors.New("projects: tenant_id required")
	}
	rows, err := t.r.db.QueryContext(ctx,
		`SELECT file_id, node_id, first_failure_at, last_failure_at,
		        consecutive_failures, last_error, cooldown_until,
		        COALESCE(clear_hash, '')
		   FROM figma_render_blocklist
		  WHERE tenant_id = ?
		  ORDER BY last_failure_at DESC`,
		t.tenantID,
	)
	if err != nil {
		return nil, fmt.Errorf("blocklist list: %w", err)
	}
	defer rows.Close()
	var out []FigmaRenderBlockEntry
	for rows.Next() {
		var e FigmaRenderBlockEntry
		var firstStr, lastStr, cooldownStr string
		if err := rows.Scan(&e.FileID, &e.NodeID, &firstStr, &lastStr,
			&e.ConsecutiveFailures, &e.LastError, &cooldownStr, &e.ClearHash); err != nil {
			return nil, err
		}
		e.TenantID = t.tenantID
		e.FirstFailureAt = parseTime(firstStr)
		e.LastFailureAt = parseTime(lastStr)
		e.CooldownUntil = parseTime(cooldownStr)
		out = append(out, e)
	}
	return out, rows.Err()
}
