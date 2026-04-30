package projects

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/sse"
)

// ─── Tunables ────────────────────────────────────────────────────────────────

// WorkerLeaseDuration is how long a claim holds before it can be re-leased by
// another worker. 60s is comfortably more than the worst-case audit-engine run
// against a 50-frame project (Phase 1 budget: 10s p95).
const WorkerLeaseDuration = 60 * time.Second

// WorkerHeartbeatInterval is how often a running worker pushes lease_expires_at
// forward. 30s = lease/2 — three missed ticks before a stale lease becomes
// claimable again, which keeps a transient pause from triggering double-claim.
const WorkerHeartbeatInterval = 30 * time.Second

// WorkerSafetyNetInterval is the fall-back poll period. Channel notifications
// from the pipeline are the fast path; this is the safety net for jobs queued
// before the worker started (e.g. crash recovery), or for notifications dropped
// because the channel was full.
const WorkerSafetyNetInterval = 30 * time.Second

// WorkerStackTraceLimit caps the stack trace length stored on a panicked job's
// audit_jobs.error column. 8KB is plenty to find the bug without bloating the
// table.
const WorkerStackTraceLimit = 8 * 1024

// ─── Interfaces (worker logic depends on these, not concrete types) ──────────

// WorkerRepo is the subset of TenantRepo + DB the WorkerPool uses. Defined as
// an interface so worker_test.go can supply a fake (or pass a real *TenantRepo)
// without dragging in unrelated repository surface.
//
// Phase 1 implementation uses the existing TenantRepo plus the *sql.DB exposed
// via TenantRepo.DB() — see workerRepoAdapter below.
type WorkerRepo interface {
	// ClaimNextJob atomically marks the next queued (or expired-lease) job as
	// running, leases it to workerID for WorkerLeaseDuration, and returns its
	// metadata. On no-rows-available, returns (nil, nil) — NOT an error.
	ClaimNextJob(ctx context.Context, workerID string) (*ClaimedJob, error)

	// HeartbeatJob pushes lease_expires_at forward by WorkerLeaseDuration on
	// the row leased to workerID. If the lease has been stolen, returns
	// ErrLeaseStolen so the running goroutine can abort gracefully.
	HeartbeatJob(ctx context.Context, jobID, workerID string) error

	// LoadVersion reads the project_versions row for the worker's audit run.
	// Tenant-scoped; returns ErrNotFound for cross-tenant reads.
	LoadVersion(ctx context.Context, tenantID, versionID string) (*ProjectVersion, error)

	// LoadProjectSlug looks up the slug for SSE event payloads. Phase 1's SSE
	// events carry the slug so clients don't have to round-trip.
	LoadProjectSlug(ctx context.Context, tenantID, versionID string) (string, error)

	// PersistRunIdempotent writes the violations + flips audit_jobs.status in a
	// single transaction. DELETE-then-INSERT keyed on version_id; safe to call
	// multiple times for the same job.
	PersistRunIdempotent(ctx context.Context, jobID, versionID string, violations []Violation) error

	// MarkJobFailed transitions the job to failed with errMsg (truncated by
	// caller) and clears the lease. Used by the panic recover path.
	MarkJobFailed(ctx context.Context, jobID, errMsg string) error

	// ResetStaleRunningJobs resets `running` rows whose lease_expires_at has
	// passed back to `queued` so they can be re-claimed. Called at startup
	// (crash recovery) AND on the safety-net tick (handles workers that died
	// without releasing their lease).
	ResetStaleRunningJobs(ctx context.Context) (int, error)
}

// ClaimedJob is what ClaimNextJob returns. Carries everything the worker needs
// to run an audit + emit the SSE event without re-reading the row.
type ClaimedJob struct {
	JobID     string
	VersionID string
	TenantID  string
	TraceID   string
}

// ErrLeaseStolen indicates another worker has taken over the job (lease
// expired before this heartbeat). The running goroutine should abort cleanly
// without writing violations or emitting SSE.
var ErrLeaseStolen = errors.New("worker: lease stolen")

// ─── WorkerPool ──────────────────────────────────────────────────────────────

// WorkerPool drains audit_jobs serially (Phase 1: size=1; Phase 2: size=6).
// Each goroutine claims a job via ClaimNextJob, runs the configured RuleRunner,
// persists violations idempotently, and emits SSE events.
//
// Channel notification (Notifications <-chan AuditJobNotification) is the
// fast path: U4's pipeline writes a tuple after committing the audit_jobs row,
// the worker wakes immediately and claims. The 30s safety-net ticker handles
// the boot-time backlog (jobs queued before the worker started) and
// notifications dropped because the channel was full.
type WorkerPool struct {
	Size          int
	Repo          WorkerRepo
	Runner        RuleRunner
	// Broker is the SSE publisher; the worker only calls Publish. Defined as
	// the narrow SSEPublisher interface (already exported by pipeline.go) so
	// tests can supply a fake without implementing Subscribe/Close.
	Broker        SSEPublisher
	Notifications <-chan AuditJobNotification
	Log           *slog.Logger

	// LeaseDuration / HeartbeatInterval / SafetyNet are exposed for tests so
	// the lease-stolen scenario can play out in milliseconds rather than
	// minutes. Zero values fall back to the package constants.
	LeaseDuration     time.Duration
	HeartbeatInterval time.Duration
	SafetyNet         time.Duration

	// now is injectable for time-sensitive tests; nil → time.Now.
	now func() time.Time

	// Track every started worker so Stop can wait for them.
	wg sync.WaitGroup
}

// Start spawns Size goroutines and returns immediately. They run until ctx is
// canceled. Call Wait() if you need to block on shutdown.
//
// Each goroutine has a unique worker_id (uuid) used as the leased_by value so
// concurrent claim attempts don't step on each other in Phase 2.
func (p *WorkerPool) Start(ctx context.Context) error {
	if p.Size <= 0 {
		p.Size = 1
	}
	if p.Repo == nil {
		return errors.New("worker: Repo required")
	}
	if p.Runner == nil {
		return errors.New("worker: Runner required")
	}
	if p.LeaseDuration == 0 {
		p.LeaseDuration = WorkerLeaseDuration
	}
	if p.HeartbeatInterval == 0 {
		p.HeartbeatInterval = WorkerHeartbeatInterval
	}
	if p.SafetyNet == 0 {
		p.SafetyNet = WorkerSafetyNetInterval
	}
	if p.now == nil {
		p.now = time.Now
	}
	if p.Log == nil {
		p.Log = slog.Default()
	}

	// Crash recovery: any `running` row with stale lease was orphaned by a
	// previous process. Reset to queued so this generation can re-claim.
	if n, err := p.Repo.ResetStaleRunningJobs(ctx); err != nil {
		p.Log.Warn("worker: startup stale-lease sweep failed", "err", err.Error())
	} else if n > 0 {
		p.Log.Info("worker: reset stale running jobs", "count", n)
	}

	for i := 0; i < p.Size; i++ {
		workerID := uuid.NewString()
		p.wg.Add(1)
		go p.runWorker(ctx, workerID)
	}
	return nil
}

// Wait blocks until every worker goroutine has returned. Useful for tests.
func (p *WorkerPool) Wait() { p.wg.Wait() }

// runWorker is the per-goroutine main loop: wait for a notification, the
// safety-net tick, or ctx-done; then drain everything currently claimable.
func (p *WorkerPool) runWorker(ctx context.Context, workerID string) {
	defer p.wg.Done()
	tk := time.NewTicker(p.SafetyNet)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.Notifications:
		case <-tk.C:
		}
		// Drain — the channel may have buffered multiple notifications; one
		// claim attempt covers them all because ClaimNextJob picks oldest first
		// and we loop until none claimable.
		for {
			if ctx.Err() != nil {
				return
			}
			done, err := p.claimAndProcess(ctx, workerID)
			if err != nil {
				p.Log.Warn("worker: claim error", "err", err.Error())
				break
			}
			if !done {
				break
			}
		}
	}
}

// claimAndProcess is the worker's per-job unit of work. Returns (true, nil)
// when a job was successfully attempted (success OR failure path — either way
// the job no longer needs draining), (false, nil) when no job was claimable,
// or (false, err) on infrastructure failure (DB down, etc.).
//
// The function never returns a panic — defer/recover wraps the runner call
// and routes panics into the failed-job path.
func (p *WorkerPool) claimAndProcess(ctx context.Context, workerID string) (bool, error) {
	claim, err := p.Repo.ClaimNextJob(ctx, workerID)
	if err != nil {
		return false, fmt.Errorf("claim: %w", err)
	}
	if claim == nil {
		return false, nil
	}

	// Heartbeat goroutine refreshes the lease while the runner runs.
	hbCtx, hbCancel := context.WithCancel(ctx)
	var hbWG sync.WaitGroup
	hbWG.Add(1)
	leaseStolen := false
	var leaseStolenMu sync.Mutex
	go func() {
		defer hbWG.Done()
		t := time.NewTicker(p.HeartbeatInterval)
		defer t.Stop()
		for {
			select {
			case <-hbCtx.Done():
				return
			case <-t.C:
				err := p.Repo.HeartbeatJob(hbCtx, claim.JobID, workerID)
				if err != nil {
					if errors.Is(err, ErrLeaseStolen) {
						leaseStolenMu.Lock()
						leaseStolen = true
						leaseStolenMu.Unlock()
						return
					}
					p.Log.Warn("worker: heartbeat error",
						"job_id", claim.JobID, "err", err.Error())
				}
			}
		}
	}()

	// Run the rule pipeline. Wrapped in defer/recover so a panicking RuleRunner
	// can't bring the pool down — we mark the job failed and continue.
	violations, runErr := p.runWithRecovery(ctx, claim)

	hbCancel()
	hbWG.Wait()

	leaseStolenMu.Lock()
	stolen := leaseStolen
	leaseStolenMu.Unlock()
	if stolen {
		p.Log.Warn("worker: lease was stolen mid-run; abandoning",
			"job_id", claim.JobID, "worker_id", workerID)
		return true, nil
	}

	slug, _ := p.Repo.LoadProjectSlug(ctx, claim.TenantID, claim.VersionID)

	if runErr != nil {
		errMsg := truncateErr(runErr.Error(), WorkerStackTraceLimit)
		if err := p.Repo.MarkJobFailed(ctx, claim.JobID, errMsg); err != nil {
			p.Log.Error("worker: mark failed", "job_id", claim.JobID, "err", err.Error())
		}
		if p.Broker != nil {
			p.Broker.Publish(claim.TraceID, sse.ProjectAuditFailed{
				ProjectSlug: slug,
				VersionID:   claim.VersionID,
				Tenant:      claim.TenantID,
				Error:       runErr.Error(),
			})
		}
		return true, nil
	}

	// Persist idempotently: DELETE existing violations for this version and
	// INSERT the new set in a single transaction, then flip status to done.
	if err := p.Repo.PersistRunIdempotent(ctx, claim.JobID, claim.VersionID, violations); err != nil {
		errMsg := truncateErr(err.Error(), WorkerStackTraceLimit)
		_ = p.Repo.MarkJobFailed(ctx, claim.JobID, errMsg)
		if p.Broker != nil {
			p.Broker.Publish(claim.TraceID, sse.ProjectAuditFailed{
				ProjectSlug: slug,
				VersionID:   claim.VersionID,
				Tenant:      claim.TenantID,
				Error:       err.Error(),
			})
		}
		return true, nil
	}

	if p.Broker != nil {
		p.Broker.Publish(claim.TraceID, sse.ProjectAuditComplete{
			ProjectSlug:    slug,
			VersionID:      claim.VersionID,
			Tenant:         claim.TenantID,
			ViolationCount: len(violations),
		})
	}
	p.Log.Info("worker: job done",
		"job_id", claim.JobID, "version_id", claim.VersionID,
		"violations", len(violations))
	return true, nil
}

// runWithRecovery invokes the RuleRunner with a panic recover. A panic is
// converted into an error that includes the truncated stack trace.
func (p *WorkerPool) runWithRecovery(ctx context.Context, claim *ClaimedJob) (violations []Violation, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			stack := truncateErr(string(debug.Stack()), WorkerStackTraceLimit)
			err = fmt.Errorf("rule runner panic: %v\n%s", rec, stack)
			violations = nil
		}
	}()
	v, lerr := p.Repo.LoadVersion(ctx, claim.TenantID, claim.VersionID)
	if lerr != nil {
		return nil, fmt.Errorf("load version: %w", lerr)
	}
	return p.Runner.Run(ctx, v)
}

// truncateErr clips s to limit bytes (with an ellipsis suffix if truncated).
func truncateErr(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	if limit < 4 {
		return s[:limit]
	}
	return s[:limit-3] + "..."
}

// ─── workerRepoAdapter: TenantRepo + *sql.DB → WorkerRepo ────────────────────

// NewWorkerRepo constructs the production WorkerRepo backed by *sql.DB. The
// tenant_id filter is applied per-query at the SQL level, so a single adapter
// can serve every tenant — the pool spans tenants. ClaimNextJob picks the
// oldest queued row regardless of tenant; subsequent reads filter by the row's
// tenant_id (returned with the claim).
type workerRepoAdapter struct {
	db  *sql.DB
	now func() time.Time
}

// NewWorkerRepo returns the production *sql.DB-backed WorkerRepo. now is
// injectable for tests; nil → time.Now.
func NewWorkerRepo(db *sql.DB, now func() time.Time) WorkerRepo {
	if now == nil {
		now = time.Now
	}
	return &workerRepoAdapter{db: db, now: now}
}

// ClaimNextJob picks the oldest queued (or expired-lease) audit_jobs row,
// flips it to running, and returns its metadata. Uses a transaction so the
// claim is atomic against concurrent workers.
//
// SQLite's RETURNING clause (3.35+, which modernc.org/sqlite supports) lets
// us do the read+write in one statement, but we use a separate SELECT inside
// BEGIN IMMEDIATE for portability with older test-DB builds.
func (a *workerRepoAdapter) ClaimNextJob(ctx context.Context, workerID string) (*ClaimedJob, error) {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	nowUnix := a.now().UTC().Unix()
	var jobID, versionID, tenantID, traceID string
	// Phase 2 U7: dequeue ordering is priority-aware. Recently-edited flow
	// exports get priority=100 (default 50 for routine exports), fan-out
	// re-audits land at priority=10 so designer-driven work isn't starved
	// behind a token-publish fan-out. The compound index
	// idx_audit_jobs_status_priority_created (migration 0002) gives the
	// planner a clean path. Phase 1's idx_audit_jobs_status_created stays
	// available — the planner picks.
	row := tx.QueryRowContext(ctx,
		`SELECT id, version_id, tenant_id, trace_id
		   FROM audit_jobs
		  WHERE status = 'queued'
		     OR (status = 'running' AND (lease_expires_at IS NULL OR lease_expires_at < ?))
		  ORDER BY priority DESC, created_at ASC
		  LIMIT 1`,
		nowUnix,
	)
	if err := row.Scan(&jobID, &versionID, &tenantID, &traceID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	leaseExpires := a.now().UTC().Add(WorkerLeaseDuration).Unix()
	startedAt := a.now().UTC().Format(time.RFC3339)
	if _, err := tx.ExecContext(ctx,
		`UPDATE audit_jobs
		    SET status = 'running',
		        leased_by = ?,
		        lease_expires_at = ?,
		        started_at = COALESCE(started_at, ?)
		  WHERE id = ?`,
		workerID, leaseExpires, startedAt, jobID,
	); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &ClaimedJob{
		JobID:     jobID,
		VersionID: versionID,
		TenantID:  tenantID,
		TraceID:   traceID,
	}, nil
}

// HeartbeatJob refreshes lease_expires_at on the row only if leased_by still
// matches workerID. If the lease was stolen, RowsAffected is 0 and we return
// ErrLeaseStolen.
func (a *workerRepoAdapter) HeartbeatJob(ctx context.Context, jobID, workerID string) error {
	leaseExpires := a.now().UTC().Add(WorkerLeaseDuration).Unix()
	res, err := a.db.ExecContext(ctx,
		`UPDATE audit_jobs
		    SET lease_expires_at = ?
		  WHERE id = ? AND leased_by = ? AND status = 'running'`,
		leaseExpires, jobID, workerID,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrLeaseStolen
	}
	return nil
}

// LoadVersion fetches the project_versions row, tenant-scoped.
func (a *workerRepoAdapter) LoadVersion(ctx context.Context, tenantID, versionID string) (*ProjectVersion, error) {
	row := a.db.QueryRowContext(ctx,
		`SELECT id, project_id, version_index, status, created_by_user_id, created_at
		   FROM project_versions
		  WHERE id = ? AND tenant_id = ?`,
		versionID, tenantID,
	)
	var v ProjectVersion
	var createdAt string
	if err := row.Scan(&v.ID, &v.ProjectID, &v.VersionIndex, &v.Status, &v.CreatedByUserID, &createdAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	v.TenantID = tenantID
	v.CreatedAt = parseTime(createdAt)
	return &v, nil
}

// LoadProjectSlug joins project_versions → projects to fetch the slug for the
// SSE payload. Returns "" + nil error when not found (SSE works without slug —
// it's a UX nicety, not a correctness signal).
func (a *workerRepoAdapter) LoadProjectSlug(ctx context.Context, tenantID, versionID string) (string, error) {
	row := a.db.QueryRowContext(ctx,
		`SELECT p.slug
		   FROM project_versions v
		   JOIN projects p ON p.id = v.project_id
		  WHERE v.id = ? AND v.tenant_id = ?`,
		versionID, tenantID,
	)
	var slug string
	if err := row.Scan(&slug); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return slug, nil
}

// PersistRunIdempotent does the DELETE-then-INSERT in a single transaction and
// flips audit_jobs.status to done. Crash here leaves the row in 'running'; the
// next ClaimNextJob expires the lease and re-runs — DELETE is idempotent.
func (a *workerRepoAdapter) PersistRunIdempotent(ctx context.Context, jobID, versionID string, violations []Violation) error {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM violations WHERE version_id = ?`, versionID); err != nil {
		return fmt.Errorf("delete prior violations: %w", err)
	}

	if len(violations) > 0 {
		stmt, err := tx.PrepareContext(ctx,
			`INSERT INTO violations (
			    id, version_id, screen_id, tenant_id, rule_id, severity, category,
			    property, observed, suggestion, persona_id, mode_label, status,
			    auto_fixable, created_at
			 ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		now := a.now().UTC().Format(time.RFC3339)
		for i := range violations {
			v := &violations[i]
			if v.ID == "" {
				v.ID = uuid.NewString()
			}
			if v.Status == "" {
				v.Status = "active"
			}
			// Category defaults to 'token_drift' to match the column DEFAULT for
			// Phase 1 runners that don't set it explicitly. Phase 2 runners set
			// Category directly on every Violation they emit.
			category := v.Category
			if category == "" {
				category = "token_drift"
			}
			autoFix := 0
			if v.AutoFixable {
				autoFix = 1
			}
			if _, err := stmt.ExecContext(ctx,
				v.ID, v.VersionID, v.ScreenID, v.TenantID, v.RuleID, v.Severity, category,
				v.Property, v.Observed, v.Suggestion,
				nullString(v.PersonaID), nullString(v.ModeLabel), v.Status,
				autoFix, now,
			); err != nil {
				return fmt.Errorf("insert violation: %w", err)
			}
		}
	}

	completedAt := a.now().UTC().Format(time.RFC3339)
	if _, err := tx.ExecContext(ctx,
		`UPDATE audit_jobs
		    SET status = 'done',
		        leased_by = NULL,
		        lease_expires_at = NULL,
		        completed_at = ?,
		        error = NULL
		  WHERE id = ?`,
		completedAt, jobID,
	); err != nil {
		return fmt.Errorf("flip job done: %w", err)
	}
	return tx.Commit()
}

// MarkJobFailed updates the job's status without touching violations.
func (a *workerRepoAdapter) MarkJobFailed(ctx context.Context, jobID, errMsg string) error {
	completedAt := a.now().UTC().Format(time.RFC3339)
	_, err := a.db.ExecContext(ctx,
		`UPDATE audit_jobs
		    SET status = 'failed',
		        leased_by = NULL,
		        lease_expires_at = NULL,
		        completed_at = ?,
		        error = ?
		  WHERE id = ?`,
		completedAt, errMsg, jobID,
	)
	return err
}

// ResetStaleRunningJobs handles two cases: (a) crash recovery at startup —
// any 'running' row left over from a previous process reverts to 'queued'; and
// (b) workers that died without releasing a lease that has now expired.
func (a *workerRepoAdapter) ResetStaleRunningJobs(ctx context.Context) (int, error) {
	nowUnix := a.now().UTC().Unix()
	res, err := a.db.ExecContext(ctx,
		`UPDATE audit_jobs
		    SET status = 'queued',
		        leased_by = NULL,
		        lease_expires_at = NULL
		  WHERE status = 'running'
		    AND (lease_expires_at IS NULL OR lease_expires_at < ?)`,
		nowUnix,
	)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}
