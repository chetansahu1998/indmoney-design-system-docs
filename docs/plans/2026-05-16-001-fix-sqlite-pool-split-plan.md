---
title: "fix: split ds-service SQLite pool into read pool + single-writer pool"
status: active
created: 2026-05-16
deepened: 2026-05-16
type: fix
---

# fix: split ds-service SQLite pool into read pool + single-writer pool

## Problem Frame

`services/ds-service/internal/db/db.go:38` calls `conn.SetMaxOpenConns(1)`. The DSN enables WAL (`_pragma=journal_mode(WAL)`) but the pool config funnels every `QueryContext`, `QueryRowContext`, `ExecContext`, and `BeginTx` across the entire service through one `*sql.Conn`. WAL's concurrent-reader capability is never delivered.

This is the root cause of multiple unrelated-looking findings from the post-merge audit:

- **PC-13:** heartbeat starvation under large Stage 6 transactions — the pipeline writer holds the only connection, the heartbeat goroutine cannot refresh `pipeline_heartbeat_at`, and the recovery sweeper steals the audit lease mid-export.
- **PERF-1:** `handleAutosyncExecute` synchronous over the full corpus holds the connection for 15+ minutes, blocking every other HTTP endpoint and every background worker.
- **REL-B7:** every new background worker added to the service (audit pool, graph rebuild, inventory poller, autosync ticker, recovery sweep, blocklist sweep, SSE subscriptions) tightens the squeeze on the shared connection.
- **PERF-3:** the process-wide `autosyncMu` mutex compounds with connection contention — operators wait for both.

The audit's `learnings-researcher` surfaced three prior case studies of the same single-writer deadlock (`docs/solutions/2026-05-01-003-phase-7-8-closure.md` "graph rebuild worker deadlock", `docs/solutions/2026-05-05-001-zeplin-canvas-learnings.md` "Stage 6 re-attach", `docs/plans/2026-05-13-002-feat-figma-db-phase-2-plan.md` "figma webhook handler"). The team has independently re-derived the "read-before-tx" convention three times and has not consolidated it. Several recent designs (autosync idempotency, sync orchestrator's no-per-tenant-lock posture) **depend** on the implicit serial-writer property.

## Scope

**In scope:**
- Restructure `internal/db/db.go` so the public surface exposes two `*sql.DB` handles (read + write) instead of one.
- Asymmetric pool sizing: write pool stays `MaxOpenConns=1` (preserves the single-writer invariant the codebase depends on); read pool opens with `mode=ro` and `MaxOpenConns=8` (concurrent reads).
- Update every `*sql.DB` consumer to choose `Read()` or `Write()` per call-site intent, with **`Write()` as the safe default for any ambiguous case**.
- Preserve all existing PRAGMA settings (`foreign_keys=ON`, `journal_mode=WAL`, `busy_timeout=5000`) on both pools.
- Preserve migration runner semantics including the `.no_tx.` connection-pinning pattern.
- Preserve graceful shutdown: both pools close cleanly on SIGTERM.
- Document `read-before-tx` as a permanent convention for the write pool.

**Out of scope (deferred to follow-up work):**
- Per-tenant advisory locks (`docs/runbooks/sync.md:77` known gap). Single-writer invariant masks the gap for now; making it explicit belongs in its own slice.
- Per-tenant sharding of the read pool. User explicitly deferred multi-tenant work.
- `BeginReadTx` API for explicit read-only transactions. The codebase has zero `sql.TxOptions{ReadOnly: true}` usage; not worth adding the API until a caller needs it.
- Tuning `synchronous` PRAGMA (currently default `FULL`). Performance work, not pool-split work.
- Tuning `cache_size` / `mmap_size`. Same.
- Adding observability hooks (per-pool query counts, latency histograms). Slot for later; the wrapper makes the surface point obvious.
- Replacing the `*db.DB` god-object's method surface (CreateUser, GetTenantBySlug, etc.) with a thinner repository pattern. Larger maintainability refactor.

**Outside this fix's identity:**
- Migrating to PostgreSQL, replacing modernc.org/sqlite, or any cross-process database.
- Distributed write coordination (the service is single-process today).

### Deferred to Follow-Up Work

- Per-tenant advisory lock to make the no-tenant-lock gap explicit (`docs/runbooks/sync.md:77`).
- Observability: per-pool query count / latency histograms / connection-pool stats endpoint.
- Audit + capture this work via `/ce-compound` afterward (per learnings-researcher recommendation) — three case studies of the same deadlock pattern deserve consolidated guidance.

---

## Requirements

**R1 — WAL concurrent readers work.** Multiple goroutines can issue reads simultaneously while a write transaction is in flight, without queueing on a single connection. Verified by an integration test that proves a read returns while a long-running write tx is held open.

**R2 — Single-writer invariant preserved.** Writes still serialize via `MaxOpenConns(1)` on the write pool. Existing `read-before-tx` call sites, the autosync idempotency design, the sync orchestrator's no-tenant-lock posture, and the worker-lease/heartbeat semantics all remain correct without modification.

**R3 — Migration runner safety.** Versioned migrations (`internal/db/migrations.go::applyOne`) run on the write pool with the same tx-wrapping semantics. `.no_tx.` migrations (`applyOneNoTx`) preserve the single-connection pinning pattern so per-connection PRAGMA toggles work.

**R4 — Test compatibility.** Every `db.Open(path)` call site in tests + CLI tools continues to work with at most a one-line construction update. Test helpers (`newTestDB`, `newPlannerTestDB`, etc.) keep their current shape.

**R5 — Graceful shutdown.** Both pools close cleanly on SIGTERM. In-flight writes finish or roll back; in-flight reads cancel via `ctx`. The existing deferred `dbConn.Close()` at `cmd/server/main.go` continues to work as the single shutdown surface.

**R6 — Read-your-write critical paths use the write pool explicitly.** Specifically: the pipeline heartbeat goroutine (must read its own write), the recovery sweeper (must see fresh heartbeats), the audit worker lease-renew (must see its own lease takeover), the autosync executor → planner sequence (planner must observe executor's commit). Each gets a code comment naming the read-your-write requirement.

**R7 — PRAGMA preservation.** Both pools open with `_pragma=foreign_keys(1)`, `_pragma=journal_mode(WAL)`, `_pragma=busy_timeout(5000)`. Test assertion at `db_test.go:69` (`PRAGMA foreign_keys=1`) still passes on both handles.

**R8 — Read pool guards against writes.** Read pool opens with `mode=ro` in the URI. Accidental `pool.Read().ExecContext("INSERT …")` fails loudly at runtime rather than silently corrupting state.

**R9 — Convention documented.** The plan ships a code comment on `Write()` accessor that names `docs/solutions/2026-05-01-003-phase-7-8-closure.md` and `docs/solutions/2026-05-05-001-zeplin-canvas-learnings.md` as the rationale for `read-before-tx`. Future readers find the case studies without having to re-derive them.

---

## Key Technical Decisions

### Decision 1: Wrapper type, not raw `*sql.DB` pair

The new type `*db.DB` retains its current name (so the diff is minimized at every call site) but its internal shape becomes a pair of `*sql.DB` handles plus accessors:

```go
// directional only, not implementation specification
type DB struct {
    write *sql.DB  // single-conn, mutates data
    read  *sql.DB  // multi-conn, mode=ro
    closed atomic.Bool
}

func (d *DB) Write() *sql.DB { return d.write }
func (d *DB) Read()  *sql.DB { return d.read }
```

**Why a wrapper over two raw handles:**
- One type owns lifecycle (`Open`, `Close`) — neither handle escapes ownership ambiguity.
- One surface point for future evolution (observability, replication, runtime swap).
- Call sites pick intent via short method calls rather than carrying two field names everywhere.
- The shape leaves room for migration-runner helpers, `Conn()`-pinning helpers, and the eventual per-tenant advisory lock without re-shaping callers a second time.

**Why not embed `*sql.DB` (current pattern):** the entire point of the split is to force call sites to choose. Embedding silently exposes one of the handles and lets callers reach `dbConn.ExecContext` directly — the same bug we're trying to eliminate. Removing the embed forces every existing call site to recompile and pick.

### Decision 2: Asymmetric sizing — write=1, read=8

The learnings research found three case studies of code written under the assumption that the process has a single writer. Symmetric pool sizing (multiple write conns) would silently invalidate that assumption and reintroduce the same deadlock pattern in a new shape (BUSY errors instead of conn-pool starvation).

- **Write pool: `MaxOpenConns=1`.** Preserves serial-writer semantics. Autosync idempotency, sync orchestrator's lock-free posture, the worker-lease contract, and the read-before-tx convention all stay correct without modification.
- **Read pool: `MaxOpenConns=8`.** Concurrent readers serve the long tail of `QueryContext` traffic (HTTP list endpoints, dashboard, audit log, SSE subscribe lookups). 8 is comfortably above the worst-case concurrent-reader count today (3 HTTP handlers + 1 SSE + 1 inventory poller + 1 audit progress emit) with headroom for new background workers.

`busy_timeout=5000` is preserved on both pools. With concurrent readers, the read pool will never trigger BUSY (SQLite write locks don't block readers in WAL). With the write pool capped at 1, the write pool itself never triggers BUSY against itself. BUSY can only surface when an external process (e.g., ad-hoc `sqlite3` shell from the operator runbook) holds a write lock — `busy_timeout=5000` gives a 5-second window for that to clear.

### Decision 3: `read-before-tx` stays the convention; `BeginTx` always goes to write pool

The codebase has zero `sql.TxOptions{ReadOnly: true}` usage and ~31 `BeginTx` call sites. Adding a `kind` parameter to `BeginTx` would require touching 31 sites for a feature no caller currently needs.

Decision: **`BeginTx` always uses the write pool**, matching today's behavior. The `read-before-tx` convention (do all reads into Go locals before opening the tx) stays in force because the write pool is still single-conn — a tx that issues a fresh read inside itself still deadlocks. Code comment on `Write()` names the convention with case-study references.

If a future caller needs a read-only multi-row transaction (e.g., a consistent snapshot read), add `BeginReadTx` then. Not now.

### Decision 4: Read-your-write paths pin to write pool explicitly

Five paths require read-after-write consistency where ms-staleness would corrupt state:

1. Pipeline heartbeat → recovery sweep: the sweeper's `SELECT WHERE pipeline_heartbeat_at < cutoff` must see the heartbeat's `UPDATE` immediately. If the sweeper used the read pool, it could falsely mark a live pipeline failed and steal the lease.
2. Worker `HeartbeatJob` lease-renew: the conditional UPDATE returns `ErrLeaseStolen` based on its own prior write. Cross-pool staleness here = lost leases.
3. Worker `ClaimNextJob`: already wrapped in `BeginTx` (write pool by default 3). Safe.
4. Autosync executor → planner: executor's `UPDATE figma_auto_sync_state` and `INSERT project_versions` must be observable by the next planner cycle. Today these run synchronously in the same goroutine, but if the planner used the read pool it could miss the write.
5. Stage 6 pipeline tx → audit worker: tx commits `audit_jobs` rows; worker (different goroutine) picks them up. Worker reads must observe committed rows.

All five paths route reads via `pool.Write()` rather than `pool.Read()`. The `read pool` is reserved for "best-effort fresh, ms-staleness OK" paths: list endpoints, dashboard, audit log queries, inventory poller's `LookupFigmaFile` cycle (the poller writes to *different* rows than it reads, so staleness only delays a useful crawl, not corrupts state).

### Decision 5: Two-phase migration of call sites

Touching 200+ Query/Exec/BeginTx sites in one commit creates an unreviewable diff. Two-phase:

1. **Phase 1 (this plan's U1-U2):** Introduce the wrapper. **All existing call sites default to `Write()`.** Mechanical find-replace: `dbConn.DB` → `dbConn.Write()`, `t.r.db` → `t.r.db.Write()`. Compiles, all tests pass, but no parallelism win yet. The diff is large but boring — every change is the same shape.
2. **Phase 2 (this plan's U3-U6):** Audit method-by-method and migrate read-only methods to `Read()`. Each cluster (HTTP read endpoints, inventory poller reads, SSE Subscribe, audit log queries) is its own commit. Parallelism gains land incrementally.

If Phase 2 reveals a method that's not safely migratable (e.g., a multi-statement read that needs read-your-write consistency), it stays on `Write()` with a comment. Some methods stay on writer forever; that's correct.

---

## High-Level Technical Design

```
                  ┌─────────────────────────┐
                  │       db.Open(path)      │
                  └────────────┬─────────────┘
                               │
              ┌────────────────┼────────────────┐
              ▼                ▼                ▼
   ┌─────────────────┐  ┌───────────────┐  ┌──────────────┐
   │  write pool     │  │  run          │  │ read pool    │
   │  MaxOpenConns=1 │  │  migrations   │  │ mode=ro      │
   │  full DSN       │  │  on writer    │  │ MaxOpenConns │
   │                 │  │               │  │   =8         │
   └────────┬────────┘  └───────┬───────┘  └───────┬──────┘
            │                   │                  │
            ▼                   ▼                  ▼
        *sql.DB             (one-shot)          *sql.DB
            │                                      │
            └────────────┬─────────────────────────┘
                         ▼
                ┌────────────────────────┐
                │  type DB struct {       │
                │      write *sql.DB      │
                │      read  *sql.DB      │
                │      …                  │
                │  }                      │
                │                         │
                │  .Write()  → *sql.DB    │
                │  .Read()   → *sql.DB    │
                │  .Close()  → both       │
                └─────────────┬───────────┘
                              │
        ┌─────────────────────┼─────────────────────────────────┐
        ▼                     ▼                                 ▼
  HTTP handlers       TenantRepo (200+ methods)         CLI tools (17)
  pick Read/Write     - handle()    → Write             default Write
  per call            - readHandle()→ Read              (short-lived,
                      - BeginTx     → Write              read=write OK)
                      - WithTx      → unchanged
```

**Directional pseudo-code only. Implementer should treat as context, not code to reproduce.**

The wrapper type itself is small (~50 lines including doc comments). The bulk of the work is the call-site migration, which is mechanical at Phase 1 and curated at Phase 2.

### Bootstrap sequence (cmd/server/main.go)

```
1. db.Open(path) → opens write pool, runs migrations on it, opens read pool, returns *db.DB
2. dbConn used everywhere (replaces 11 dbConn.DB sites, all default to dbConn.Write())
3. defer dbConn.Close() (closes both pools in order: write first, then read)
```

### Migration runner

```
applyVersionedMigrations(ctx) — operates on d.write (write pool)
  ├── applyOne(tx-wrapped, write pool's BeginTx — single conn already)
  └── applyOneNoTx — calls d.write.Conn(ctx), pins to that conn, runs migration body
                     + version-record on the same Conn (preserves per-conn PRAGMA semantics)
```

`.no_tx.` migrations already use `d.Conn(ctx)` for connection pinning (driven by FK-toggle in migration 0015). That pattern transfers cleanly to the write pool — the pool has at most 1 conn so `Conn()` returns the only one. No behavior change.

---

## Output Structure

No new directories. The change is constrained to existing files:

```
services/ds-service/
├── internal/db/
│   ├── db.go                     ← rewritten (the wrapper)
│   └── migrations.go             ← small change: write pool only
├── internal/projects/
│   ├── repository.go             ← TenantRepo + handle() learn pool intent
│   ├── recovery.go               ← read-your-write: pin to Write()
│   ├── blocklist_sweep.go        ← same
│   ├── worker.go                 ← lease & heartbeat: Write()
│   └── pipeline.go               ← heartbeat goroutine: Write()
├── cmd/server/main.go            ← 11 .DB sites → .Write()
└── cmd/*/main.go (16 CLIs)       ← short-lived: default Write()
```

Test helpers in each test package recompile with one-line update.

---

## Implementation Units

### U1. Introduce DB wrapper with Read/Write accessors

**Goal:** Replace `*db.DB` (struct embedding `*sql.DB`) with a wrapper that exposes `Write() *sql.DB` and `Read() *sql.DB`. Open both pools in `db.Open()`. Run migrations on write pool. Close both on `Close()`. Read pool opens with `mode=ro`. Existing PRAGMA settings preserved on both DSNs.

**Requirements:** R1, R2, R5, R7, R8.

**Dependencies:** None.

**Files:**
- `services/ds-service/internal/db/db.go` (modify — rewrite type definition + Open + Close; keep all existing helper methods but route them via `Write()` internally)
- `services/ds-service/internal/db/db_test.go` (modify — add tests for the new shape)

**Approach:**
1. Replace the `DB struct { *sql.DB }` embed with `DB struct { write *sql.DB; read *sql.DB }`.
2. `Open()`:
   - Open write conn with current DSN.
   - `write.SetMaxOpenConns(1)` (single-writer invariant preserved).
   - Ping write conn.
   - Run migrations on write conn.
   - Open read conn with same DSN + `&mode=ro`.
   - `read.SetMaxOpenConns(8)`.
   - Ping read conn.
   - Return wrapper.
3. `Close()`: close write first, then read. Both errors collected; first non-nil returned.
4. Migrate existing helper methods (`CreateUser`, `GetUserByEmail`, `WriteAudit`, `QueryAudit`, `UpsertFigmaToken`, `GetFigmaToken`, etc.) to call `d.write.ExecContext` / `d.write.QueryContext` directly. Defer migrating the read-only ones (`GetUserByEmail`, `GetFigmaToken`, `QueryAudit`, etc.) to `d.read` — happens in U3.
5. Doc-comment on `Write()` names the case studies: `docs/solutions/2026-05-01-003-phase-7-8-closure.md`, `docs/solutions/2026-05-05-001-zeplin-canvas-learnings.md`, `docs/plans/2026-05-13-002-feat-figma-db-phase-2-plan.md`.

**Execution note:** Test-first. Before changing `db.Open`, write the concurrent-read-while-write-held test (reads succeed while a 2-second write tx is in flight). It will fail on the current single-conn pool and pass on the split pool.

**Patterns to follow:**
- The existing `Open()` shape — DSN, PRAGMA, ping, migrate.
- The existing close shape — single defer at `cmd/server/main.go:111`.
- The `.no_tx.` migration's `d.Conn(ctx)` pattern stays unchanged in `migrations.go`; it just operates on the write pool now.

**Test scenarios:**
- `TestOpen_OpensBothPools`: open succeeds, both `Write()` and `Read()` return non-nil. Each pings.
- `TestRead_RejectsWrites`: `pool.Read().ExecContext("INSERT INTO users …")` returns an error containing "read-only" or "readonly database".
- `TestWrite_AllowsWrites`: `pool.Write().ExecContext("INSERT INTO users …")` succeeds.
- `TestConcurrentRead_WhileWriteTxHeld`: open a write tx, hold it for 2 seconds in a goroutine, issue 5 concurrent reads from `pool.Read()` — all return within 100ms (i.e., not serialized on the write tx). Asserts the parallelism win.
- `TestClose_ClosesBothPools`: after `Close()`, both `Write()` and `Read()` return errors on use.
- `TestForeignKeys_OnBothPools`: `PRAGMA foreign_keys` returns 1 on both `Write()` and `Read()` connections.
- `TestMigrations_RunOnWritePool`: open a fresh DB, verify migrations applied (existing test pattern at `db_test.go`); after open, `pool.Write()` can write to migrated tables.
- `TestNoTxMigration_PinsConnection`: existing test for migration 0015's FK-toggle pattern still passes (it's already in `db_test.go`); confirm the connection-pinning still works through the write pool.

**Verification:** `go build ./...` clean, `go test ./internal/db/...` passes, all new tests above pass.

---

### U2. Migrate every `*sql.DB` consumer to default `Write()`

**Goal:** Mechanical find-replace migration. Every site that reaches `dbConn.DB`, `t.r.db`, `s.deps.DB.DB`, etc. picks `Write()` (the safe default). After this unit, the codebase compiles + all existing tests pass, but no parallelism is unlocked yet — every read still goes to the single-writer pool. This is the largest-line-count unit but the most boring; the diff is uniform.

**Requirements:** R1 (foundation), R2 (preserved by routing everything to writer), R5 (close path unchanged).

**Dependencies:** U1.

**Files (high-level — implementer will discover full list during execution):**
- `services/ds-service/cmd/server/main.go` (~11 sites: `dbConn.DB` → `dbConn.Write()`)
- `services/ds-service/cmd/figma-inventory-sync/main.go` and 15 other CLI mains (one-line each)
- `services/ds-service/internal/projects/repository.go` (TenantRepo struct + `handle()` accessor + `BeginTx` — all route to write pool by default)
- `services/ds-service/internal/projects/recovery.go`
- `services/ds-service/internal/projects/blocklist_sweep.go`
- `services/ds-service/internal/projects/worker.go`
- `services/ds-service/internal/projects/pipeline.go` (heartbeat goroutine)
- `services/ds-service/internal/projects/repository_*.go` (all sibling files use `t.r.db` — pattern is consistent)
- 4 test helpers (`internal/projects/repository_test.go`, `internal/figma/inventory/autosync_planner_test.go`, `internal/auditbyslug/handler_test.go`, `internal/projects/pipeline_bench_test.go`)
- `services/ds-service/internal/projects/server_figma_autosync_test.go` (3 sites reaching `srv.deps.DB.DB`)

**Approach:**
1. Audit `internal/projects/repository.go`. Change `TenantRepo.r` from `*db.DB` to `*db.DB` (same type, new shape internally). The `handle()` method becomes:
   - `t.tx != nil` → return `t.tx` (unchanged)
   - Otherwise → return `t.r.db.Write()` (was: `t.r.db.DB`)
2. `BeginTx`: `t.r.db.Write().BeginTx(ctx, nil)`.
3. Direct `*sql.DB` consumers in `internal/projects/` (recovery, blocklist_sweep, worker, pipeline-heartbeat) switch to `dbConn.Write()`.
4. `cmd/server/main.go`: 11 sites — mechanical replace.
5. CLI tools: mechanical replace.
6. Tests: mechanical replace.
7. **Do not migrate any read-only call to `Read()` in this unit** — that's U3 onward. Goal here is "compiles, works, single-writer behavior preserved."

**Execution note:** Pragmatic. No test-first. Existing test suite is the verification.

**Patterns to follow:**
- Find-replace is safe because the embed is gone; the compiler enforces every site.
- One commit. Boring, large, mechanical. Don't try to clean up other things in the same commit.

**Test scenarios:**
- Existing test suite passes end-to-end. No new tests in this unit.
- Concurrent-read parallelism test from U1 still passes (it doesn't depend on per-call-site migration).

**Verification:** `go build ./...` clean. `go test ./...` across the service passes. Diff is large but every change is the same shape (`dbConn.DB → dbConn.Write()`, `t.r.db → t.r.db.Write()`, etc.).

---

### U3. Migrate HTTP read endpoints + audit log queries to `Read()`

**Goal:** Audit and migrate the long tail of HTTP read endpoints (list, get, dashboard, search, audit log) to use `pool.Read()`. These are the highest-volume read paths and benefit most from concurrent reads.

**Requirements:** R1 (parallelism gain), R6 (preserves read-your-write — these paths don't have it).

**Dependencies:** U2 (defaults in place; safe to migrate one at a time).

**Files:**
- `services/ds-service/internal/projects/server.go` (HTTP handlers — list, get endpoints)
- `services/ds-service/internal/projects/repository.go` (audit log queries: `QueryAudit`)
- `services/ds-service/internal/projects/server_inbox.go` (if it exists — inbox list endpoint)
- `services/ds-service/internal/projects/repository_dashboard.go` (if it exists — dashboard queries)
- `services/ds-service/internal/projects/search.go` (search endpoint)
- `services/ds-service/internal/auditbyslug/handler.go` (audit-by-slug)
- `services/ds-service/internal/db/db.go` (migrate `GetUserByEmail`, `GetUserByID`, `GetFigmaToken`, `GetTenantBySlug`, `GetTenantRole`, `GetUserTenantIDs`, `QueryAudit`, `GetSyncState`, `loadAppliedVersions` → `d.read.QueryContext`)

**Approach:**
1. For each handler/method, ask: does this read need to see writes made in this same request flow? If yes → keep on `Write()`. If no → migrate to `Read()`.
2. Add a per-method `readHandle()` helper on `TenantRepo` that returns `t.tx` if a tx is in flight, else `t.r.db.Read()`. (Symmetric to `handle()` but routes to read pool.)
3. Migrate read-only `TenantRepo` methods (Lookup*, Get*, List*) to call `t.readHandle()` instead of `t.handle()`.
4. Migrate `*db.DB` read methods in `internal/db/db.go` to `d.read.QueryContext`.
5. Login flow caveat: `GetUserByEmail` followed by `UpdateUserLastLogin` is a read-then-write but the read result doesn't depend on its own write. Read can use `Read()`; the write uses `Write()`. Safe.

**Execution note:** Method-by-method. Each migration is a 1-2 line change with a clear "safe?" check.

**Patterns to follow:**
- The `handle()` / `readHandle()` symmetry mirrors the existing `dbtx` interface union pattern.
- Cite the case studies in the method docstring when a method is intentionally kept on `Write()` for read-your-write reasons.

**Test scenarios:**
- Existing test suite continues to pass.
- New integration test: concurrent HTTP `GET /v1/projects/list` calls succeed while a long-running write (e.g., audit-job tx) is in flight. Asserts the parallelism win surfaces at the HTTP layer.

**Verification:** `go test ./internal/projects/...` passes. Concurrent-list test passes.

---

### U4. Migrate inventory poller + autosync planner read paths to `Read()`

**Goal:** The inventory poller cycle (`internal/figma/inventory/poller.go::runCycle`) issues many reads per crawl. The autosync planner (`autosync_planner.go::PlanTenant`) issues ~5 reads per file × N files per cycle. Both run on a regular ticker and benefit from concurrent reads.

**Requirements:** R1, R6 (planner reads inside the autosync executor → planner sequence still pin to write).

**Dependencies:** U2.

**Files:**
- `services/ds-service/internal/figma/inventory/poller.go` (runCycle, crawlTenant)
- `services/ds-service/internal/figma/inventory/autosync_planner.go` (PlanTenant, Plan)
- `services/ds-service/internal/projects/repository_figma_inventory.go` (LookupFigmaFile, LookupFigmaProjectMapping, LookupFigmaProject, ListFigmaPagesForFile, ListFigmaSectionsForPage, ListFigmaFilesForAutoSync — all reads)
- `services/ds-service/internal/projects/repository_figma_autosync.go` (LookupAutoSyncState, ListAutoSyncState — reads)

**Approach:**
1. Inventory poller's read-then-write pattern: `FilesNeedingPagesSync` (read) → `syncFileDeep` (writes). The read can use `Read()`; the write goes through `TenantRepo` and uses `Write()`. Safe.
2. Autosync planner: `PlanTenant` is read-only. Every Lookup/List can use `Read()`.
3. **Critical: autosync executor → planner.** When the executor commits a full-export and the same goroutine immediately calls the planner on the next file, the planner's reads of `figma_auto_sync_state` and `project_versions` must see the executor's writes. **Solution**: the planner inherits the executor's `TenantRepo` instance, which uses the write pool for that single-goroutine sequence. The retry ticker's per-cycle `runOnce` already constructs a fresh planner+executor pair per cycle — they share a `TenantRepo`, so they share a pool. Verify by reading `cmd/server/main.go::startAutosyncRetryLoop::runOnce`.
4. Migrate `LookupAutoSyncState` (used by planner) to `readHandle()` — when called outside a write tx, uses Read pool; when called inside an executor-spawned tx, the tx handle is used (which is on the write pool).

**Execution note:** The executor → planner read-your-write boundary is the highest-risk part of this unit. Test it explicitly.

**Patterns to follow:**
- The autosync test fixture at `internal/figma/inventory/autosync_executor_test.go` already exercises executor → planner sequences. Add a test that uses the new pool split and asserts the planner sees the executor's commits.

**Test scenarios:**
- `TestAutosyncPlanner_SeesExecutorCommit`: executor commits a `figma_auto_sync_state` row with status='ok', planner immediately runs `PlanTenant`, planner reports the row as `skip_unchanged` (must see the commit). Asserts read-your-write held.
- `TestInventoryPoller_ConcurrentRead`: poller cycle runs concurrently with HTTP write traffic; poller cycle completes without blocking on writes.

**Verification:** Both tests pass. Existing autosync + inventory test suites pass.

---

### U5. Pin read-your-write critical paths to `Write()` with explicit comments

**Goal:** Add doc-comments + code-level pinning to the five read-your-write paths (Decision 4 above). These already use `Write()` after U2 — this unit is documentation + a small refactor that names the requirement at the code site so future readers don't migrate them to `Read()` and reintroduce a bug.

**Requirements:** R6, R9.

**Dependencies:** U2 (paths already on Write); U3/U4 (verify the dependent methods don't accidentally use Read).

**Files:**
- `services/ds-service/internal/projects/pipeline.go` (heartbeat goroutine)
- `services/ds-service/internal/projects/recovery.go` (recovery sweeper)
- `services/ds-service/internal/projects/worker.go` (HeartbeatJob, ClaimNextJob, ResetStaleRunningJobs)
- `services/ds-service/internal/figma/inventory/autosync_executor.go` (executor → planner sequence)
- `services/ds-service/internal/projects/repository.go` (HeartbeatVersion method)

**Approach:**
1. Each of the five paths gets a doc-comment block referencing R6 + the case studies + naming the read-your-write invariant.
2. Add a small helper `t.writeOnly()` (or similar — name TBD by implementer) that returns the write pool explicitly, distinct from `t.handle()` which routes via the read/write decision. Use it in the five paths. The helper exists primarily as a search anchor — future grep-finds of `writeOnly()` should turn up exactly these sites.
3. No new functional behavior; this unit is preventing regression.

**Execution note:** Pure documentation + naming refactor. Could be folded into U2 if the implementer prefers; kept separate here so the read-your-write invariant gets a deliberate review pass.

**Patterns to follow:**
- The doc-comment pattern at `docs/solutions/2026-05-01-003-phase-7-8-closure.md` ("Phase 7+8 closure deadlock") — use the same language so the case study and the code reference each other.

**Test scenarios:**
- No new tests. Verification is code review.

**Verification:** Grep for `writeOnly()` (or chosen helper name) returns exactly the five paths. Doc-comments cite case studies.

---

### U6. Concurrent read/write test coverage + close-path test

**Goal:** Lock the parallelism win into CI with explicit tests. Without these, a future regression (e.g., someone bumping the write pool to MaxOpenConns=4) won't surface as a test failure.

**Requirements:** R1 (parallelism), R2 (single-writer invariant), R5 (close).

**Dependencies:** U1-U5 complete.

**Files:**
- `services/ds-service/internal/db/concurrency_test.go` (new file)
- `services/ds-service/internal/projects/concurrency_test.go` (new file, integration-level)

**Approach:**
1. Pool-level concurrency tests:
   - `TestPool_ConcurrentReadsDuringWriteTx`: writer holds a 2-second tx; 10 readers issue queries concurrently; all return within 100ms.
   - `TestPool_WritesSerialized`: 5 goroutines each open `BeginTx(ctx, nil)` and `INSERT`. Total elapsed time ≥ 5 * (per-write duration) within tolerance — proves writes serialize.
   - `TestPool_BusyTimeoutOnExternalWriter`: open a second `*sql.DB` against the same file (simulating an `sqlite3` shell), hold a write tx for 6 seconds. Our pool's write attempt returns BUSY after the 5-second `busy_timeout`. (Optional — depends on whether modernc.org/sqlite + `mode=ro` interacts well with the test fixture; if too brittle, skip.)
   - `TestPool_CloseUnblocksWaiters`: open the pool, start a reader that blocks on a slow query, call `Close()`, assert the reader returns with a context-canceled or pool-closed error within 1 second.
2. Integration tests:
   - `TestExecutor_PlannerSeesCommit`: as described in U4. Replicated here as a regression test.
   - `TestRecoverySweeper_SeesHeartbeat`: heartbeat goroutine writes, recovery sweeper reads on the same tx-less call path — must see the heartbeat. Catches a regression where someone migrates recovery to `Read()`.

**Execution note:** Test-first for U1's parallelism test was already done. This unit consolidates and expands.

**Patterns to follow:**
- The existing concurrency tests in the repo (if any) — grep for `t.Parallel()` and `sync.WaitGroup` in `_test.go` files.

**Test scenarios:**
- Listed in Approach.

**Verification:** `go test ./internal/db/... -race` passes. `go test ./internal/projects/... -race` passes (no new race conditions surfaced by the split).

---

## Risk Analysis & Mitigation

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| Migrating a read-your-write path to `Read()` accidentally → silent staleness bug, lease theft, status-machine corruption | Medium | High | U5 pins the 5 critical paths with explicit `writeOnly()` helper. Doc-comments cite case studies. Test coverage in U6. Code review must check any new `Read()` call against the read-your-write invariant. |
| `mode=ro` URI interaction with modernc.org/sqlite — driver may not honor it cleanly | Low | Medium | Test it in U1's `TestRead_RejectsWrites`. If the driver doesn't honor `mode=ro`, fall back to documenting the convention without the runtime guard. |
| Concurrent reads surface latent bugs in test fixtures that assumed serialized ordering | Medium | Medium | Run `go test ./... -race` after each unit. Fix surfaced races as they appear (they're real bugs, masked today by single-conn). |
| Migration runner accidentally splits across pools | Low | Critical | Migrations explicitly call `d.write.ExecContext`, `d.write.Conn(ctx)`. Test 0015's FK-toggle behavior at `db_test.go`. |
| BUSY errors surface from the operator's ad-hoc `sqlite3` shell holding a write lock while the service tries to write | Medium | Low | `busy_timeout=5000` preserved. Document in `docs/runbooks/operator.md` that long-running `sqlite3` shell sessions can now block writers (today they can too, but rarely manifest because the service is single-conn). |
| Symmetric pool sizing accidentally chosen in a future PR, reintroducing the deadlock | Low | Critical | U6's `TestPool_WritesSerialized` asserts MaxOpenConns=1 behavior at the pool level. Doc-comment on `Open()` names the asymmetric sizing rationale. |
| Diff is too large to review (U2 touches 200+ sites) | High | Medium | U2 is intentionally mechanical and uniform. Reviewer can scan for "any change that isn't the literal `.DB → .Write()` shape" — that's the only thing worth careful attention. |
| Phase 2 (U3-U4) drift — implementer migrates a borderline read to `Read()` that should stay on `Write()` | Medium | Medium | Each migration in U3-U4 is one method at a time, accompanied by a one-line justification comment ("safe — no read-your-write requirement"). Code review catches misclassifications. |
| Test suite slows down because every test now opens 2 connections | Low | Low | Read pool's MaxOpenConns is configurable; tests can use a `db.OpenTest()` helper that opens both pools at MaxOpenConns=1 if needed. Default test config should still match production for fidelity. |

---

## Deferred to Implementation

These resolve during execution; not planning-time questions.

- **Exact helper name on TenantRepo for the read-pool path.** Options: `t.readHandle()`, `t.reader()`, `t.read()`. Implementer picks the name that reads best at call sites. (`t.handle()` already exists, so the new one must not collide.)
- **Exact helper name on TenantRepo for the write-only-explicitly-required path.** Options: `t.writeOnly()`, `t.writer()`, `t.mustWrite()`. Picked during U5.
- **Read pool MaxOpenConns final value.** Planned at 8; implementer may tune up/down based on observed connection-pool stats after Phase 1 ships. Should not exceed `ulimit -n` headroom.
- **Whether to fold U5's doc-comment work into U2's mechanical commit or keep separate.** Implementer's call based on review fatigue.
- **Exact list of `*db.DB` helper methods that move to `d.read`.** U3 lists candidates; final assignment per method depends on read-your-write audit.

---

## Patterns to Follow

- `services/ds-service/internal/projects/repository.go::handle()` for the dbtx-routing pattern that the new `readHandle()` mirrors.
- `services/ds-service/internal/db/migrations.go::applyOneNoTx` for the `Conn()` pinning pattern that transfers cleanly to the write pool.
- `docs/solutions/2026-05-01-003-phase-7-8-closure.md` ("graph rebuild worker deadlock") for the read-before-tx convention that gets named at the `Write()` doc-comment.
- `docs/solutions/2026-05-05-001-zeplin-canvas-learnings.md` ("Stage 6 re-attach") for the read-before-tx pattern in pipeline tx contexts.

---

## System-Wide Impact

**Affected subsystems:**
- HTTP handlers (~30 endpoint reads benefit from concurrent reads)
- Background workers (audit, graph rebuild, inventory poller, autosync ticker, recovery sweep, blocklist sweep) — no longer serialize on shared conn for their reads
- SSE Subscribe lookups — long-lived reads no longer block writers
- 17 CLI tools — short-lived, behavior unchanged
- 4 test packages — recompile, behavior unchanged

**Affected operator workflows:**
- `sqlite3` shell sessions against `ds.db` — can now hold the write lock long enough to cause BUSY in the service. `busy_timeout=5000` gives a 5-second window before BUSY surfaces. Worth a one-line addition to `docs/runbooks/operator.md`.

**Affected designs that depend on single-writer invariant (must be preserved):**
- Autosync idempotency (`docs/plans/2026-05-14-001-feat-figma-db-autosync-bridge-plan.md:556`) — preserved by `MaxOpenConns=1` on write pool.
- Sync orchestrator's no-per-tenant-lock posture (`docs/runbooks/sync.md:77`) — preserved by `MaxOpenConns=1` on write pool. Flagged for follow-up.
- Worker lease semantics (`worker.go::HeartbeatJob`) — preserved.

---

## Verification Strategy

Per-unit verification is in each unit's Verification field. End-to-end:

1. `go build ./...` clean.
2. `go test ./... -race -count=1` passes.
3. Run the service locally with a representative workload (one operator session + one HTTP load script doing concurrent list+get reads). Observe via slog that the HTTP requests no longer queue when the autosync retry loop is mid-cycle.
4. Open `sqlite3 services/ds-service/data/ds.db` while the service is running. Confirm reads from the service still work. Hold an open `BEGIN IMMEDIATE` in the shell for 3 seconds. Confirm the service either succeeds via `busy_timeout` retry or returns BUSY within the 5-second window — not a hang.
5. SIGTERM the service mid-load. Confirm both pools close cleanly via shutdown logs.

---

## Future Considerations

(Documented because they affect today's design; not in scope.)

- **Per-tenant advisory locks.** Once the write pool is exposed, the no-per-tenant-lock gap (sync.md:77, autosync executor) becomes addressable as a separate slice — likely a `figma_autosync_lease`-style row pattern, extended to other tenant-scoped writes.
- **Read replica.** The wrapper type makes future read replicas a 1-file change (point `read` at a different DSN). Out of scope today.
- **Observability.** Per-pool query count, latency histograms, connection-pool stats endpoint. The wrapper is the natural surface point.
- **Tuning `synchronous=NORMAL`.** WAL with `synchronous=FULL` (current default) is slower than necessary. NORMAL is safe in WAL mode per SQLite docs. Performance work, not pool-split work — slot for later.
