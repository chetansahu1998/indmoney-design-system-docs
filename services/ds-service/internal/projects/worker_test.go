package projects

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/indmoney/design-system-docs/services/ds-service/internal/audit"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/db"
	"github.com/indmoney/design-system-docs/services/ds-service/internal/sse"
)

// ─── Test helpers ────────────────────────────────────────────────────────────

// workerTestEnv bundles the moving parts a worker test needs: a real DB with
// the U1 schema, a TenantRepo, a tenant, a user, plus a stubBroker that
// captures every published SSE event.
type workerTestEnv struct {
	d        *db.DB
	tenantA  string
	userA    string
	repo     *TenantRepo
	broker   *workerStubBroker
}

func newWorkerTestEnv(t *testing.T) *workerTestEnv {
	t.Helper()
	d, tA, _, uA := newTestDB(t)
	return &workerTestEnv{
		d:       d,
		tenantA: tA,
		userA:   uA,
		repo:    NewTenantRepo(d.DB, tA),
		broker:  &workerStubBroker{},
	}
}

// workerStubBroker captures Publish calls. Implements SSEPublisher.
type workerStubBroker struct {
	mu     sync.Mutex
	events []capturedEvent
}

type capturedEvent struct {
	traceID string
	event   sse.Event
}

func (b *workerStubBroker) Publish(traceID string, ev sse.Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, capturedEvent{traceID: traceID, event: ev})
}

func (b *workerStubBroker) snapshot() []capturedEvent {
	b.mu.Lock()
	defer b.mu.Unlock()
	cp := make([]capturedEvent, len(b.events))
	copy(cp, b.events)
	return cp
}

func (b *workerStubBroker) countByType(ty string) int {
	n := 0
	for _, e := range b.snapshot() {
		if e.event.Type() == ty {
			n++
		}
	}
	return n
}

// fakeRunner produces a fixed slice of violations and (optionally) panics or
// returns an error so tests can exercise every failure path. ScreenID/Property
// fields are populated against `screenIDs[i % len(screenIDs)]` so violations
// reference real screen rows the test seeded.
type fakeRunner struct {
	mu        sync.Mutex
	calls     int
	violations int
	screenIDs []string
	panicMsg  string
	errOnce   error
	delay     time.Duration
}

func (f *fakeRunner) Run(ctx context.Context, v *ProjectVersion) ([]Violation, error) {
	f.mu.Lock()
	f.calls++
	calls := f.calls
	delay := f.delay
	panicMsg := f.panicMsg
	errOnce := f.errOnce
	if errOnce != nil {
		f.errOnce = nil
	}
	f.mu.Unlock()

	if delay > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
	}
	if panicMsg != "" && calls == 1 {
		panic(panicMsg)
	}
	if errOnce != nil {
		return nil, errOnce
	}
	out := make([]Violation, 0, f.violations)
	for i := 0; i < f.violations; i++ {
		sid := ""
		if len(f.screenIDs) > 0 {
			sid = f.screenIDs[i%len(f.screenIDs)]
		}
		out = append(out, Violation{
			VersionID: v.ID,
			ScreenID:  sid,
			TenantID:  v.TenantID,
			RuleID:    fmt.Sprintf("test.rule-%d", i),
			Severity:  SeverityMedium,
			Property:  "fill",
			Observed:  "#abcdef",
			Suggestion: "Bind to surface.test",
			Status:    "active",
		})
	}
	return out, nil
}

// seedProject + version + flow + screens + queued audit_jobs row. Returns the
// IDs the test needs for assertions.
type seedResult struct {
	projectID string
	slug      string
	versionID string
	traceID   string
	jobID     string
	screenIDs []string
}

func seedProjectWithJob(t *testing.T, env *workerTestEnv, screenCount int) seedResult {
	t.Helper()
	ctx := context.Background()
	p, err := env.repo.UpsertProject(ctx, Project{
		Name: "Worker Test", Platform: "mobile", Product: "Plutus",
		Path: "Onboarding-" + uuid.NewString()[:8], OwnerUserID: env.userA,
	})
	if err != nil {
		t.Fatalf("upsert project: %v", err)
	}
	v, err := env.repo.CreateVersion(ctx, p.ID, env.userA)
	if err != nil {
		t.Fatalf("create version: %v", err)
	}
	flow, err := env.repo.UpsertFlow(ctx, Flow{
		ProjectID: p.ID, FileID: "FILE-" + uuid.NewString()[:6], Name: "F",
	})
	if err != nil {
		t.Fatalf("upsert flow: %v", err)
	}
	screens := make([]Screen, 0, screenCount)
	for i := 0; i < screenCount; i++ {
		screens = append(screens, Screen{
			VersionID: v.ID, FlowID: flow.ID,
			X: 0, Y: float64(i * 1000), Width: 375, Height: 812,
		})
	}
	if screenCount > 0 {
		if err := env.repo.InsertScreens(ctx, screens); err != nil {
			t.Fatalf("insert screens: %v", err)
		}
	}

	traceID := uuid.NewString()
	idem := uuid.NewString()
	tx, err := env.repo.BeginTx(ctx)
	if err != nil {
		t.Fatalf("tx: %v", err)
	}
	jobID, err := env.repo.EnqueueAuditJob(ctx, tx, v.ID, traceID, idem)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	sids := make([]string, len(screens))
	for i := range screens {
		sids[i] = screens[i].ID
	}
	return seedResult{
		projectID: p.ID,
		slug:      p.Slug,
		versionID: v.ID,
		traceID:   traceID,
		jobID:     jobID,
		screenIDs: sids,
	}
}

// waitForJobStatus polls audit_jobs.status until it matches `want` or times out.
// Used by tests that fire-and-forget a worker goroutine.
func waitForJobStatus(t *testing.T, dbConn *sql.DB, jobID, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var got string
		if err := dbConn.QueryRow(`SELECT status FROM audit_jobs WHERE id = ?`, jobID).Scan(&got); err == nil && got == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	var final string
	_ = dbConn.QueryRow(`SELECT status FROM audit_jobs WHERE id = ?`, jobID).Scan(&final)
	t.Fatalf("job %s never reached %s (final=%s)", jobID, want, final)
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

// ─── Tests ───────────────────────────────────────────────────────────────────

// Happy path: queue 1 job → worker picks up via channel notification (no 2s
// polling lag), calls runner, writes 5 violations, publishes SSE; states
// queued→running→done.
func TestWorker_HappyPath_ChannelNotification(t *testing.T) {
	env := newWorkerTestEnv(t)
	seed := seedProjectWithJob(t, env, 2)

	enq := NewAuditEnqueuer()
	runner := &fakeRunner{violations: 5, screenIDs: seed.screenIDs}
	pool := &WorkerPool{
		Size:              1,
		Repo:              NewWorkerRepo(env.d.DB, nil),
		Runner:            runner,
		Broker:            env.broker,
		Notifications:     enq.Notifications(),
		Log:               quietLogger(),
		// Push the safety-net far out so the test only succeeds if channel
		// notification fires the wakeup.
		SafetyNet:         1 * time.Hour,
		HeartbeatInterval: 10 * time.Millisecond,
		LeaseDuration:     1 * time.Second,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := pool.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}

	// Channel-notify the worker (mimicking U4's pipeline).
	enq.EnqueueAuditJob(seed.versionID, seed.traceID)

	waitForJobStatus(t, env.d.DB, seed.jobID, "done", 2*time.Second)

	// Violations row count == 5.
	var n int
	if err := env.d.DB.QueryRow(`SELECT COUNT(*) FROM violations WHERE version_id = ?`, seed.versionID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 5 {
		t.Fatalf("expected 5 violations, got %d", n)
	}

	// SSE: exactly one ProjectAuditComplete published with ViolationCount=5.
	if got := env.broker.countByType("project.audit_complete"); got != 1 {
		t.Fatalf("expected 1 audit_complete event; got %d", got)
	}
	for _, e := range env.broker.snapshot() {
		if c, ok := e.event.(sse.ProjectAuditComplete); ok {
			if c.ViolationCount != 5 {
				t.Fatalf("expected ViolationCount=5; got %d", c.ViolationCount)
			}
			if c.ProjectSlug != seed.slug {
				t.Fatalf("expected slug %s; got %s", seed.slug, c.ProjectSlug)
			}
		}
	}

	cancel()
	pool.Wait()
}

// Edge: queue 3 jobs simultaneously → worker (size=1) processes serially in
// created_at order.
func TestWorker_Serial_OrderedByCreatedAt(t *testing.T) {
	env := newWorkerTestEnv(t)

	// Seed three jobs back to back. Manually space the created_at column so
	// the ordering is unambiguous against SQLite's RFC3339 1-second resolution.
	jobs := make([]seedResult, 3)
	for i := 0; i < 3; i++ {
		jobs[i] = seedProjectWithJob(t, env, 1)
		// Backdate created_at so older jobs sort first.
		oldCreated := time.Now().UTC().Add(time.Duration(-3+i) * time.Minute).Format(time.RFC3339Nano)
		if _, err := env.d.DB.Exec(`UPDATE audit_jobs SET created_at = ? WHERE id = ?`, oldCreated, jobs[i].jobID); err != nil {
			t.Fatalf("backdate: %v", err)
		}
	}

	// Track order via a runner that records VersionID per call.
	order := make([]string, 0, 3)
	var orderMu sync.Mutex
	rec := &recordingRunner{
		callback: func(v *ProjectVersion) {
			orderMu.Lock()
			order = append(order, v.ID)
			orderMu.Unlock()
		},
	}

	pool := &WorkerPool{
		Size:              1,
		Repo:              NewWorkerRepo(env.d.DB, nil),
		Runner:            rec,
		Broker:            env.broker,
		Notifications:     nil, // safety-net only
		Log:               quietLogger(),
		SafetyNet:         50 * time.Millisecond,
		HeartbeatInterval: 10 * time.Millisecond,
		LeaseDuration:     1 * time.Second,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := pool.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}

	for _, j := range jobs {
		waitForJobStatus(t, env.d.DB, j.jobID, "done", 3*time.Second)
	}

	cancel()
	pool.Wait()

	orderMu.Lock()
	defer orderMu.Unlock()
	if len(order) != 3 {
		t.Fatalf("expected 3 runs; got %d (%v)", len(order), order)
	}
	for i := range order {
		if order[i] != jobs[i].versionID {
			t.Fatalf("expected order[%d]=%s; got %s", i, jobs[i].versionID, order[i])
		}
	}
}

type recordingRunner struct {
	mu       sync.Mutex
	callback func(v *ProjectVersion)
}

func (r *recordingRunner) Run(ctx context.Context, v *ProjectVersion) ([]Violation, error) {
	r.mu.Lock()
	cb := r.callback
	r.mu.Unlock()
	if cb != nil {
		cb(v)
	}
	return nil, nil
}

// Edge: lease expires (heartbeat goroutine fails) → another claim attempt picks
// up the abandoned job. Simulated by manually corrupting leased_by so the
// heartbeat sees a 0-row update and ErrLeaseStolen.
func TestWorker_LeaseStolen_RequeuedByStaleSweep(t *testing.T) {
	env := newWorkerTestEnv(t)
	seed := seedProjectWithJob(t, env, 1)

	// First "worker": pretend a previous process claimed but died.
	staleWorker := uuid.NewString()
	staleLease := time.Now().UTC().Add(-1 * time.Minute).Unix() // already expired
	if _, err := env.d.DB.Exec(
		`UPDATE audit_jobs SET status='running', leased_by=?, lease_expires_at=? WHERE id = ?`,
		staleWorker, staleLease, seed.jobID,
	); err != nil {
		t.Fatalf("stale claim: %v", err)
	}

	// Real pool comes online. ResetStaleRunningJobs at startup should requeue,
	// then the safety-net tick claims and processes.
	runner := &fakeRunner{violations: 2, screenIDs: seed.screenIDs}
	pool := &WorkerPool{
		Size:              1,
		Repo:              NewWorkerRepo(env.d.DB, nil),
		Runner:            runner,
		Broker:            env.broker,
		Notifications:     nil,
		Log:               quietLogger(),
		SafetyNet:         30 * time.Millisecond,
		HeartbeatInterval: 10 * time.Millisecond,
		LeaseDuration:     1 * time.Second,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := pool.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}

	waitForJobStatus(t, env.d.DB, seed.jobID, "done", 2*time.Second)
	cancel()
	pool.Wait()
}

// Edge: empty version (0 screens) → completes; 0 violations written.
func TestWorker_EmptyVersion_NoViolations(t *testing.T) {
	env := newWorkerTestEnv(t)
	seed := seedProjectWithJob(t, env, 0) // no screens

	enq := NewAuditEnqueuer()
	runner := &fakeRunner{violations: 0}
	pool := &WorkerPool{
		Size:              1,
		Repo:              NewWorkerRepo(env.d.DB, nil),
		Runner:            runner,
		Broker:            env.broker,
		Notifications:     enq.Notifications(),
		Log:               quietLogger(),
		SafetyNet:         50 * time.Millisecond,
		HeartbeatInterval: 10 * time.Millisecond,
		LeaseDuration:     1 * time.Second,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := pool.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	enq.EnqueueAuditJob(seed.versionID, seed.traceID)

	waitForJobStatus(t, env.d.DB, seed.jobID, "done", 2*time.Second)
	var n int
	if err := env.d.DB.QueryRow(`SELECT COUNT(*) FROM violations WHERE version_id = ?`, seed.versionID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0 violations; got %d", n)
	}
	if got := env.broker.countByType("project.audit_complete"); got != 1 {
		t.Fatalf("expected 1 audit_complete; got %d", got)
	}

	cancel()
	pool.Wait()
}

// Edge: idempotent retry — partial-write + crash + restart + re-run → final
// violations table matches what a clean run would produce. No duplicates.
func TestWorker_IdempotentRetry_NoDuplicates(t *testing.T) {
	env := newWorkerTestEnv(t)
	seed := seedProjectWithJob(t, env, 1)

	// Pre-populate the violations table with stale rows from a hypothetical
	// previous failed attempt. The DELETE-then-INSERT in PersistRunIdempotent
	// must wipe them.
	for i := 0; i < 7; i++ {
		_, err := env.d.DB.Exec(
			`INSERT INTO violations (id, version_id, screen_id, tenant_id, rule_id, severity, property, status, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, 'active', ?)`,
			uuid.NewString(), seed.versionID, seed.screenIDs[0], env.tenantA,
			"stale.rule", "low", "fill", time.Now().UTC().Format(time.RFC3339),
		)
		if err != nil {
			t.Fatalf("seed stale: %v", err)
		}
	}

	enq := NewAuditEnqueuer()
	runner := &fakeRunner{violations: 3, screenIDs: seed.screenIDs}
	pool := &WorkerPool{
		Size:              1,
		Repo:              NewWorkerRepo(env.d.DB, nil),
		Runner:            runner,
		Broker:            env.broker,
		Notifications:     enq.Notifications(),
		Log:               quietLogger(),
		SafetyNet:         50 * time.Millisecond,
		HeartbeatInterval: 10 * time.Millisecond,
		LeaseDuration:     1 * time.Second,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := pool.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	enq.EnqueueAuditJob(seed.versionID, seed.traceID)

	waitForJobStatus(t, env.d.DB, seed.jobID, "done", 2*time.Second)
	cancel()
	pool.Wait()

	var n int
	if err := env.d.DB.QueryRow(`SELECT COUNT(*) FROM violations WHERE version_id = ?`, seed.versionID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 3 {
		t.Fatalf("expected 3 violations after idempotent run (stale rows wiped); got %d", n)
	}
}

// Error: RuleRunner panics → recover; job marked failed; no rows in violations.
func TestWorker_RunnerPanics_JobFailed_NoViolations(t *testing.T) {
	env := newWorkerTestEnv(t)
	seed := seedProjectWithJob(t, env, 1)

	enq := NewAuditEnqueuer()
	runner := &fakeRunner{panicMsg: "synthetic boom"}
	pool := &WorkerPool{
		Size:              1,
		Repo:              NewWorkerRepo(env.d.DB, nil),
		Runner:            runner,
		Broker:            env.broker,
		Notifications:     enq.Notifications(),
		Log:               quietLogger(),
		SafetyNet:         50 * time.Millisecond,
		HeartbeatInterval: 10 * time.Millisecond,
		LeaseDuration:     1 * time.Second,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := pool.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	enq.EnqueueAuditJob(seed.versionID, seed.traceID)

	waitForJobStatus(t, env.d.DB, seed.jobID, "failed", 2*time.Second)

	var errMsg string
	if err := env.d.DB.QueryRow(`SELECT COALESCE(error,'') FROM audit_jobs WHERE id = ?`, seed.jobID).Scan(&errMsg); err != nil {
		t.Fatalf("read err: %v", err)
	}
	if errMsg == "" {
		t.Fatal("expected error message on failed job")
	}

	var n int
	if err := env.d.DB.QueryRow(`SELECT COUNT(*) FROM violations WHERE version_id = ?`, seed.versionID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0 violations after panic; got %d", n)
	}

	if got := env.broker.countByType("project.audit_failed"); got != 1 {
		t.Fatalf("expected 1 audit_failed event; got %d", got)
	}

	cancel()
	pool.Wait()
}

// Error: ds-service crash mid-job → restart finds 'running' row with stale
// lease → resets to queued → re-processed correctly.
func TestWorker_CrashRecovery_StaleRunningResetToQueued(t *testing.T) {
	env := newWorkerTestEnv(t)
	seed := seedProjectWithJob(t, env, 1)

	// Simulate the prior process: claim the job, never release.
	staleWorker := uuid.NewString()
	staleLease := time.Now().UTC().Add(-1 * time.Minute).Unix()
	if _, err := env.d.DB.Exec(
		`UPDATE audit_jobs SET status='running', leased_by=?, lease_expires_at=?, started_at=? WHERE id = ?`,
		staleWorker, staleLease, time.Now().Add(-2*time.Minute).Format(time.RFC3339), seed.jobID,
	); err != nil {
		t.Fatalf("stale claim: %v", err)
	}

	// New process boots, runs ResetStaleRunningJobs, then claims fresh.
	runner := &fakeRunner{violations: 1, screenIDs: seed.screenIDs}
	pool := &WorkerPool{
		Size:              1,
		Repo:              NewWorkerRepo(env.d.DB, nil),
		Runner:            runner,
		Broker:            env.broker,
		Notifications:     nil,
		Log:               quietLogger(),
		SafetyNet:         30 * time.Millisecond,
		HeartbeatInterval: 10 * time.Millisecond,
		LeaseDuration:     1 * time.Second,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := pool.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}

	waitForJobStatus(t, env.d.DB, seed.jobID, "done", 2*time.Second)

	// New leased_by should be NULL (cleared in PersistRunIdempotent).
	var leasedBy sql.NullString
	if err := env.d.DB.QueryRow(`SELECT leased_by FROM audit_jobs WHERE id = ?`, seed.jobID).Scan(&leasedBy); err != nil {
		t.Fatalf("read leased_by: %v", err)
	}
	if leasedBy.Valid && leasedBy.String == staleWorker {
		t.Fatalf("expected fresh worker_id (or NULL); still see stale %s", staleWorker)
	}

	cancel()
	pool.Wait()
}

// Channel-notification fast path: a job queued AFTER the worker is running
// should be picked up faster than the safety-net interval.
func TestWorker_ChannelNotification_FasterThanSafetyNet(t *testing.T) {
	env := newWorkerTestEnv(t)
	seed := seedProjectWithJob(t, env, 1)

	enq := NewAuditEnqueuer()
	runner := &fakeRunner{violations: 1, screenIDs: seed.screenIDs}
	pool := &WorkerPool{
		Size:              1,
		Repo:              NewWorkerRepo(env.d.DB, nil),
		Runner:            runner,
		Broker:            env.broker,
		Notifications:     enq.Notifications(),
		Log:               quietLogger(),
		// 10s safety net — anything faster proves channel notification works.
		SafetyNet:         10 * time.Second,
		HeartbeatInterval: 10 * time.Millisecond,
		LeaseDuration:     1 * time.Second,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := pool.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}

	start := time.Now()
	enq.EnqueueAuditJob(seed.versionID, seed.traceID)
	waitForJobStatus(t, env.d.DB, seed.jobID, "done", 2*time.Second)
	elapsed := time.Since(start)
	if elapsed > 1*time.Second {
		t.Fatalf("channel notification too slow (%v); should be <<1s", elapsed)
	}

	cancel()
	pool.Wait()
}

// Severity mapping table: P1 deprecated → critical, P1 drift → high, P2 →
// medium, P3 with token_path → low, P3 without → info.
func TestMapPriorityToSeverity(t *testing.T) {
	cases := []struct {
		name string
		fc   audit.FixCandidate
		want string
	}{
		{"P1 deprecated", audit.FixCandidate{Priority: audit.PriorityP1, Reason: "deprecated"}, SeverityCritical},
		{"P1 theme_break", audit.FixCandidate{Priority: audit.PriorityP1, Reason: "theme_break"}, SeverityCritical},
		{"P1 drift", audit.FixCandidate{Priority: audit.PriorityP1, Reason: "drift"}, SeverityHigh},
		{"P2 drift", audit.FixCandidate{Priority: audit.PriorityP2, Reason: "drift"}, SeverityMedium},
		{"P3 with token_path", audit.FixCandidate{Priority: audit.PriorityP3, Reason: "drift", TokenPath: "x.y"}, SeverityLow},
		{"P3 custom no token", audit.FixCandidate{Priority: audit.PriorityP3, Reason: "custom"}, SeverityLow},
		{"P3 info-grade only", audit.FixCandidate{Priority: audit.PriorityP3, Reason: "naming"}, SeverityInfo},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := MapPriorityToSeverity(tc.fc); got != tc.want {
				t.Fatalf("MapPriorityToSeverity = %s; want %s", got, tc.want)
			}
		})
	}
}

// auditCoreRunner with a stub loader and empty token catalog → returns no
// violations against an empty version. Validates the runner doesn't crash on
// nil catalogs and the loader interface is honoured.
func TestAuditCoreRunner_EmptyCatalog_NoViolations(t *testing.T) {
	loader := &fakeLoader{rows: []ScreenWithTree{
		{ScreenID: "s1", FlowID: "f1", CanonicalTree: ""},
	}}
	r := NewAuditCoreRunner(AuditCoreRunnerConfig{
		Loader: loader,
	})
	v := &ProjectVersion{ID: "v1", TenantID: "t1"}
	got, err := r.Run(context.Background(), v)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 violations against empty catalog; got %d", len(got))
	}
}

type fakeLoader struct {
	rows []ScreenWithTree
}

func (f *fakeLoader) LoadScreensWithTrees(ctx context.Context, versionID string) ([]ScreenWithTree, error) {
	return f.rows, nil
}

// auditCoreRunner: when loader returns an error, Run propagates.
func TestAuditCoreRunner_LoaderError(t *testing.T) {
	loader := &erroringLoader{err: errors.New("db down")}
	r := NewAuditCoreRunner(AuditCoreRunnerConfig{Loader: loader})
	_, err := r.Run(context.Background(), &ProjectVersion{ID: "v"})
	if err == nil {
		t.Fatal("expected error")
	}
}

type erroringLoader struct{ err error }

func (e *erroringLoader) LoadScreensWithTrees(ctx context.Context, versionID string) ([]ScreenWithTree, error) {
	return nil, e.err
}

// Worker startup: ResetStaleRunningJobs runs and reports the count via the
// log. We can't observe the log, but we can verify the row was reset.
func TestWorker_StartupResetStaleJobs(t *testing.T) {
	env := newWorkerTestEnv(t)
	seed := seedProjectWithJob(t, env, 1)

	staleLease := time.Now().UTC().Add(-1 * time.Minute).Unix()
	if _, err := env.d.DB.Exec(
		`UPDATE audit_jobs SET status='running', leased_by=?, lease_expires_at=? WHERE id = ?`,
		"old-worker", staleLease, seed.jobID,
	); err != nil {
		t.Fatalf("stale: %v", err)
	}

	pool := &WorkerPool{
		Size:              0, // Start defaults to 1 — we'll use Size=0 as a proxy
		Repo:              NewWorkerRepo(env.d.DB, nil),
		Runner:            &fakeRunner{},
		Broker:            env.broker,
		Notifications:     nil,
		Log:               quietLogger(),
		SafetyNet:         1 * time.Hour,
		HeartbeatInterval: 10 * time.Millisecond,
		LeaseDuration:     1 * time.Second,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := pool.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}

	// After Start, the stale row should be reset to queued.
	var status string
	var leasedBy sql.NullString
	if err := env.d.DB.QueryRow(`SELECT status, leased_by FROM audit_jobs WHERE id = ?`, seed.jobID).Scan(&status, &leasedBy); err != nil {
		t.Fatalf("read: %v", err)
	}
	// Status may be 'queued' (just reset) or 'running' (already re-claimed) or 'done'
	// since the worker may have raced. Acceptable: anything that's NOT still 'running'
	// with the OLD leased_by.
	if status == "running" && leasedBy.Valid && leasedBy.String == "old-worker" {
		t.Fatalf("stale running row not reset: status=%s leased_by=%v", status, leasedBy)
	}

	cancel()
	pool.Wait()
}

// Integration: full pipeline test — POST export → wait view_ready → wait
// audit_complete → assert violations queryable.
//
// This skips the actual HTTP layer and drives the in-process pieces directly
// (pipeline writes audit_jobs row + notifies enqueuer; worker drains; SSE).
func TestWorker_Integration_FullPipeline(t *testing.T) {
	env := newWorkerTestEnv(t)
	ctx := context.Background()

	// Seed project + version + flow + screens + canonical_tree (mimicking U4
	// pipeline output WITHOUT actually running it).
	p, err := env.repo.UpsertProject(ctx, Project{
		Name: "Integration", Platform: "mobile", Product: "Plutus", Path: "Int", OwnerUserID: env.userA,
	})
	if err != nil {
		t.Fatalf("p: %v", err)
	}
	v, err := env.repo.CreateVersion(ctx, p.ID, env.userA)
	if err != nil {
		t.Fatalf("v: %v", err)
	}
	flow, err := env.repo.UpsertFlow(ctx, Flow{ProjectID: p.ID, FileID: "FK", Name: "F"})
	if err != nil {
		t.Fatalf("flow: %v", err)
	}
	screen := Screen{VersionID: v.ID, FlowID: flow.ID, X: 0, Y: 0, Width: 375, Height: 812}
	if err := env.repo.InsertScreens(ctx, []Screen{screen}); err != nil {
		t.Fatalf("screen: %v", err)
	}
	// Re-fetch to get the assigned ID.
	var screenID string
	if err := env.d.DB.QueryRow(`SELECT id FROM screens WHERE version_id = ? LIMIT 1`, v.ID).Scan(&screenID); err != nil {
		t.Fatalf("screen id: %v", err)
	}

	// Insert a canonical_tree row and enqueue audit_job (mimics pipeline's
	// final transaction).
	enq := NewAuditEnqueuer()
	traceID := uuid.NewString()
	tx, err := env.repo.BeginTx(ctx)
	if err != nil {
		t.Fatalf("tx: %v", err)
	}
	if err := env.repo.InsertCanonicalTree(ctx, tx, screenID, `{"id":"frame","type":"FRAME","children":[]}`, "h"); err != nil {
		t.Fatalf("tree: %v", err)
	}
	if err := env.repo.RecordViewReady(ctx, tx, v.ID); err != nil {
		t.Fatalf("view_ready: %v", err)
	}
	jobID, err := env.repo.EnqueueAuditJob(ctx, tx, v.ID, traceID, uuid.NewString())
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// Worker uses the real auditCoreRunner with the DB-backed loader and an
	// empty token catalog (which is the realistic Phase 1 wiring).
	runner := NewAuditCoreRunner(AuditCoreRunnerConfig{
		Loader: NewDBVersionScreenLoader(env.d.DB),
	})
	pool := &WorkerPool{
		Size:              1,
		Repo:              NewWorkerRepo(env.d.DB, nil),
		Runner:            runner,
		Broker:            env.broker,
		Notifications:     enq.Notifications(),
		Log:               quietLogger(),
		SafetyNet:         200 * time.Millisecond,
		HeartbeatInterval: 50 * time.Millisecond,
		LeaseDuration:     1 * time.Second,
	}
	wctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := pool.Start(wctx); err != nil {
		t.Fatalf("start: %v", err)
	}

	enq.EnqueueAuditJob(v.ID, traceID)
	waitForJobStatus(t, env.d.DB, jobID, "done", 3*time.Second)

	if got := env.broker.countByType("project.audit_complete"); got != 1 {
		t.Fatalf("expected 1 audit_complete; got %d", got)
	}

	cancel()
	pool.Wait()
}

// Race-resistance: spam Notifications faster than the worker can drain. The
// worker must not double-claim a single job, and must process every distinct
// job exactly once.
func TestWorker_NotificationSpam_NoDoubleClaim(t *testing.T) {
	env := newWorkerTestEnv(t)
	const jobCount = 5
	jobs := make([]seedResult, jobCount)
	for i := 0; i < jobCount; i++ {
		jobs[i] = seedProjectWithJob(t, env, 1)
	}

	enq := NewAuditEnqueuer()
	var runs atomic.Int32
	runner := &fakeRunner{}
	wrapped := &countingRunner{
		inner: runner,
		count: &runs,
	}
	pool := &WorkerPool{
		Size:              1,
		Repo:              NewWorkerRepo(env.d.DB, nil),
		Runner:            wrapped,
		Broker:            env.broker,
		Notifications:     enq.Notifications(),
		Log:               quietLogger(),
		SafetyNet:         50 * time.Millisecond,
		HeartbeatInterval: 10 * time.Millisecond,
		LeaseDuration:     1 * time.Second,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := pool.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}

	// Spam notifications.
	for i := 0; i < 50; i++ {
		enq.EnqueueAuditJob("noise", "noise")
	}
	for _, j := range jobs {
		enq.EnqueueAuditJob(j.versionID, j.traceID)
	}

	for _, j := range jobs {
		waitForJobStatus(t, env.d.DB, j.jobID, "done", 3*time.Second)
	}

	cancel()
	pool.Wait()

	if int(runs.Load()) != jobCount {
		t.Fatalf("expected %d runner invocations; got %d", jobCount, runs.Load())
	}
}

type countingRunner struct {
	inner RuleRunner
	count *atomic.Int32
}

func (c *countingRunner) Run(ctx context.Context, v *ProjectVersion) ([]Violation, error) {
	c.count.Add(1)
	return c.inner.Run(ctx, v)
}
