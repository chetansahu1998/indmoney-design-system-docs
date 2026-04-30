---
date: 2026-04-30
phase: 1
captured_after: feat/projects-flow-atlas-phase-1
type: learnings
---

# Phase 1 — Projects · Flow Atlas — institutional learnings

Captured at the close of Phase 1 (12 implementation units shipped on
`feat/projects-flow-atlas-phase-1`). Phase 2 + Phase 3 are already in
flight on subsequent branches and are referenced where relevant.

## What worked

### Tenant scoping by denormalization + scoped repository

Phase 1 made `tenant_id` a column on every table except `personas`
(org-wide library). The `TenantRepo` constructor takes the tenant_id
once and silently injects `WHERE tenant_id = ?` into every query;
plain `Repo` is unexported. Cross-tenant 404 (no existence oracle)
fell out for free across `GET /v1/projects/:slug`,
`GET /screens/:id/png`, `GET /screens/:id/canonical-tree`,
`GET /flows/:id/drd`, and the SSE channel.

Phase 2 audit fan-out + Phase 3 view-state machine both inherit this
without any net-new tenant code. The pattern carries — extend it for
all subsequent phases.

### Migration discipline (numbered files + schema_migrations)

`services/ds-service/migrations/NNNN_description.up.sql` with a
`schema_migrations(version, name, applied_at)` tracking table was
boring infrastructure but paid off immediately:

- 0001 (Phase 1 schema) → 0002 (Phase 2 audit_rules + categories) →
  0003 (Phase 2 rule seed) → 0004 (Phase 2 backfill_markers) →
  0005 (Phase 3 Welcome demo project)
- Each migration is self-contained, idempotent (CREATE TABLE IF NOT
  EXISTS + INSERT OR IGNORE), and reviewed independently.

The `embed.go` sibling-package pattern (PR-002 quirk: `go:embed`
can't traverse upward via `../../`) survived every subsequent phase
unchanged.

### r3f + Next 16 Suspense + pathname keying

Phase 1's mitigation for pmndrs/react-three-fiber#3595 (Next 16's
componentCache holding references to disposed three.js objects) —
wrapping the `<Canvas>` in `<Suspense fallback={…}>` with
`key={usePathname()}` — held up cleanly through Phase 3 atlas
postprocessing additions. The remount-on-pathname pattern is the
correct shape for r3f + App Router; recommend it for the Phase 6
mind graph.

### SSE with single-use ticket auth

EventSource forbids custom Authorization headers + JWTs in query
strings leak. The Phase 1 single-use ticket model (`POST
/v1/projects/:slug/events/ticket` returns a 60s ticket; EventSource
attaches `?ticket=<id>`; broker invalidates on first use) carried
through Phase 2's fan-out + Phase 3's audit-progress events without
any ticket-flow churn.

## What we'd do differently

### Inline tab loading states pre-U5

Phase 1 + 2 each tab managed its own loading/error/empty UI as plain
divs ("Loading…" text in gray), creating visual inconsistency the
moment U5's EmptyState primitive landed in Phase 3. Should have
shipped the variant primitive in Phase 1 — the 8 variants are
universally needed.

### Worker pool size constant pre-U7

Shipping `WorkerPool{ Size: 1 }` as a hardcoded constant in Phase 1
made the Phase 2 U7 env-driven scaling work feel like a refactor
when it was really just a 5-line change. Should have shipped the env
read in Phase 1 with a default of 1.

### Plugin first-run UX pre-U9

Phase 1 plugin shipped without any onboarding, which meant the first
designer to install it had to read the source to understand what
Projects mode did. Phase 3 U9 added the modal; should have been
table-stakes from day 1.

### Animation library packaging

GSAP + Lenis + Framer Motion bundle as separate vendor chunks
correctly today, but the `lib/animations/timelines/` directory grew
faster than expected (4 timelines in Phase 1, 3 more in Phase 2 + 3).
A future refactor could cluster timelines by surface (atlas /
project-shell / tabs / onboarding) instead of by name.

## Quirks worth knowing

### SQLite UNIQUE constraint with NULL columns

`UNIQUE(tenant_id, file_id, section_id, persona_id)` on `flows`
treats `NULL` values as distinct (per SQLite spec — IEEE three-
valued logic). Freeform flows (no section ancestor) all set
`section_id=NULL` and never collide on the section axis. This was
intentional and remains correct, but Phase 2 worker code that
JOINs flows back to projects had to use `IS NOT NULL` guards
explicitly.

### audit_jobs UNIQUE on (version_id) WHERE status IN ('queued','running')

The partial unique index lets historical 'done'/'failed' rows
coexist with a new 'queued' row on the same version_id, but BLOCKS
two concurrent queued/running rows for the same version. Phase 2
fan-out's idempotency layer relies on this — it's the reason
`fanout_id`-based dedup succeeds even when two CLI invocations race.

### canonical_tree live in screen_canonical_trees, NOT screens

Phase 1's H5 finding split the JSON blob into a sibling table so
list queries don't pull MB of JSON the UI doesn't need. Phase 2's
rule loaders all JOIN through; Phase 3 U7's state machine never
touches canonical_tree directly. The split is load-bearing.

### Phase 1 PR is not yet open

The Phase 1 finishing fork (Playwright specs + bundle-size script +
CI workflow) committed `ea64c2e` and pushed to origin, but `gh pr
create` was sandbox-blocked at the time. Manually open the PR via
the GitHub UI when convenient + flip the Phase 1 plan frontmatter
to `status: completed`.
