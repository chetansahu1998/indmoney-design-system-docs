package projects

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"
)

// blocklist_sweep.go — periodic stale-row cleanup for figma_render_blocklist
// (2026-05-12).
//
// The blocklist's 24h cooldown is the primary memory mechanism. After
// cooldown, IsFigmaRenderBlocked returns active=false and callers re-attempt.
// A successful re-attempt deletes the row. A re-failure refreshes the row.
//
// So when do rows accumulate forever? Two cases:
//   1. A frame goes broken, hits threshold, then the leaf is NEVER
//      re-rendered (no Stage 9 prerender, no on-demand fetch, no
//      pipeline retry). The row sits past cooldown indefinitely.
//   2. A tenant is deleted (CASCADE handles rows for active tenants,
//      but we have no other invariant — and the schema's FK should
//      catch this, so this is theoretical).
//
// This sweep deletes rows where last_failure_at < now - 7 days. Past
// that window, either the frame stopped being asked about (no harm in
// dropping the row — the next failure will reinsert at consecutive=1
// with a fresh first_failure_at) OR the frame was being asked about
// regularly and ClearFigmaRenderFailure/refresh would have kept
// last_failure_at recent.
//
// 7 days is conservative: long enough that designer-edit workflows
// (test → fix → re-sync) complete inside the window, short enough
// that the table stays bounded under cumulative file churn.

// BlocklistStaleThreshold is the age past last_failure_at after which
// a row is considered abandoned and can be GC'd.
const BlocklistStaleThreshold = 7 * 24 * time.Hour

// BlocklistSweepInterval is the gap between sweep runs in the
// background loop. 1 hour is plenty — the threshold is days, so we're
// not race-sensitive.
const BlocklistSweepInterval = 1 * time.Hour

// SweepStaleBlocklistRows deletes blocklist rows older than
// BlocklistStaleThreshold. Tenant-agnostic — runs at the system level
// so the loop doesn't have to enumerate tenants. Returns the count of
// rows deleted plus the first error (best-effort, continues past
// per-row issues).
func SweepStaleBlocklistRows(ctx context.Context, db *sql.DB, log *slog.Logger) (int, error) {
	if db == nil {
		return 0, nil
	}
	cutoff := time.Now().UTC().Add(-BlocklistStaleThreshold).Format(time.RFC3339)
	res, err := db.ExecContext(ctx,
		`DELETE FROM figma_render_blocklist
		  WHERE last_failure_at < ?`,
		cutoff,
	)
	if err != nil {
		return 0, fmt.Errorf("blocklist sweep: %w", err)
	}
	n, _ := res.RowsAffected()
	if n > 0 && log != nil {
		log.Info("blocklist: swept stale rows",
			"deleted", n,
			"threshold_days", int(BlocklistStaleThreshold/(24*time.Hour)),
			"cutoff", cutoff,
		)
	}
	return int(n), nil
}

// RunBlocklistSweepLoop runs an immediate sweep, then sweeps every
// BlocklistSweepInterval until ctx is canceled. Designed to be invoked
// as `go RunBlocklistSweepLoop(ctx, db, log)` from cmd/server's main —
// mirrors RunRecoveryLoop's wiring exactly.
func RunBlocklistSweepLoop(ctx context.Context, db *sql.DB, log *slog.Logger) {
	if _, err := SweepStaleBlocklistRows(ctx, db, log); err != nil && log != nil {
		log.Warn("blocklist: startup sweep error", "err", err)
	}
	tk := time.NewTicker(BlocklistSweepInterval)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
			if _, err := SweepStaleBlocklistRows(ctx, db, log); err != nil && log != nil {
				log.Warn("blocklist: sweep error", "err", err)
			}
		}
	}
}
