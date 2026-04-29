package projects

import (
	"context"
	"database/sql"
	"log/slog"
	"time"
)

// HeartbeatStaleThreshold is how long a `pending` version's heartbeat may lag
// before the recovery sweeper marks it failed. The pipeline writes a heartbeat
// every 5s (RunFastPreview); 30s gives 6 missed ticks of slack so transient
// process pauses don't kill a live pipeline.
const HeartbeatStaleThreshold = 30 * time.Second

// RecoverySweepInterval is how often RunRecoveryLoop polls the DB. 60s is
// sufficient because the sweep is a safety net — the heartbeat does the
// real-time tracking.
const RecoverySweepInterval = 60 * time.Second

// RecoverStuckVersions is the one-shot sweep: scan project_versions where
// status='pending' AND pipeline_heartbeat_at < now-30s, and mark them failed.
// Tenant-agnostic (the sweeper is a system-level recovery, not a per-tenant
// operation), so it touches the *sql.DB directly rather than going through
// TenantRepo.
//
// Returns the number of versions marked failed, plus the first error
// encountered (best-effort — continues sweeping past per-row failures).
func RecoverStuckVersions(ctx context.Context, db *sql.DB, log *slog.Logger) (int, error) {
	if db == nil {
		return 0, nil
	}
	cutoff := time.Now().UTC().Add(-HeartbeatStaleThreshold).Format(time.RFC3339)
	rows, err := db.QueryContext(ctx,
		`SELECT id, tenant_id FROM project_versions
		  WHERE status = 'pending'
		    AND pipeline_heartbeat_at IS NOT NULL
		    AND pipeline_heartbeat_at < ?`,
		cutoff,
	)
	if err != nil {
		return 0, err
	}
	type stuck struct{ id, tenant string }
	var todo []stuck
	for rows.Next() {
		var id, tenant string
		if err := rows.Scan(&id, &tenant); err != nil {
			rows.Close()
			return 0, err
		}
		todo = append(todo, stuck{id, tenant})
	}
	rows.Close()

	count := 0
	var firstErr error
	for _, s := range todo {
		_, err := db.ExecContext(ctx,
			`UPDATE project_versions
			    SET status = 'failed',
			        pipeline_heartbeat_at = NULL,
			        error = 'orphaned by server restart'
			  WHERE id = ? AND tenant_id = ? AND status = 'pending'`,
			s.id, s.tenant,
		)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			if log != nil {
				log.Warn("recovery: failed to mark version", "version_id", s.id, "err", err)
			}
			continue
		}
		count++
		if log != nil {
			log.Info("recovery: marked orphaned version failed", "version_id", s.id, "tenant_id", s.tenant)
		}
	}
	return count, firstErr
}

// RunRecoveryLoop runs an immediate sweep, then sweeps every RecoverySweepInterval
// until ctx is canceled. Intended to be `go RunRecoveryLoop(ctx, db, log)` from
// cmd/server's main.
func RunRecoveryLoop(ctx context.Context, db *sql.DB, log *slog.Logger) {
	// Immediate sweep at startup catches versions left pending by a server
	// restart that happened mid-pipeline.
	if _, err := RecoverStuckVersions(ctx, db, log); err != nil && log != nil {
		log.Warn("recovery: startup sweep error", "err", err)
	}
	tk := time.NewTicker(RecoverySweepInterval)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
			if _, err := RecoverStuckVersions(ctx, db, log); err != nil && log != nil {
				log.Warn("recovery: sweep error", "err", err)
			}
		}
	}
}
