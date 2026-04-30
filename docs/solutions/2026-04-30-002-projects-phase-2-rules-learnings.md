---
date: 2026-04-30
phase: 2
captured_after: feat/projects-flow-atlas-phase-2
type: learnings
---

# Phase 2 — Audit engine extensions — institutional learnings

Captured at the close of Phase 2 (12 implementation units + prod-wire +
sidecar backfill scaffold shipped on `feat/projects-flow-atlas-phase-2`).

## What worked

### RuleRunner interface as a stable plug-in slot

Phase 1's `RuleRunner` interface was a single-method abstraction:

```go
type RuleRunner interface {
    Run(ctx context.Context, v *ProjectVersion) ([]Violation, error)
}
```

Phase 2 added 5 new rule classes (theme parity, cross-persona, a11y
contrast + touch target, flow-graph, component governance) without
touching the worker. The composite runner (prod-wire) wraps them all
into one Runner; the worker doesn't know N rules ran. This is the
right shape — preserve it for Phase 4+ rule additions.

### Tenant-aware composite runner (per-Run vs per-boot)

The clean refactor in prod-wire was making the composite tenant-aware
on each `Run()` call instead of building a per-tenant composite at
boot. Worker holds one Runner pointer; `tenantAwareRunner.Run()` reads
`v.TenantID` and builds a fresh per-tenant slice. This let Phase 1's
worker.go integration ship without any worker-lifecycle changes.

### Migration backfill UPDATE for new violations.category column

Migration 0002 added `violations.category TEXT NOT NULL DEFAULT
'token_drift'` then ran `UPDATE` statements to remap known Phase 1
rule_ids to the right category. ~30 rule_id prefixes covered. Existing
violations got categorized in-place; new violations from the Phase 2
runners set Category explicitly. No data migration script needed
post-deploy.

### Channel-notification + 100ms throttle for SSE progress

Phase 3 U6's audit-progress events reused Phase 1's broker without a
single new abstraction. Token-bucket throttle at 100ms (always emit
the LAST tick at completed==total) keeps the channel from saturating
on fast runs while preserving the 100% completion signal.

## What we'd do differently

### Per-rule progress, not per-(rule, screen)

The Phase 2 plan U6 envisioned ~140 events per typical job
(rule × screen). The shipped implementation emits per-rule (5-7
events) because the RuleRunner interface doesn't expose per-screen
iteration to the worker — adding that would require extending the
interface with a callback parameter, churning every existing rule.

Lesson: when an interface is the wrong shape for new requirements,
it's cheaper to ship the lower-fidelity version that matches the
existing shape than to refactor the interface mid-phase. Per-screen
is a future polish unit if dogfood feedback demands it.

### lib/audit/*.json sidecar backfill was speculative

The Phase 2 plan U9 spec assumed ~800 sidecars to migrate. The repo
today has 0 (only `index.json` + `spacing-observed.json`, both
explicitly skipped by the CLI). The migration tool ships in working
form so the workflow exists when designers DO start running per-file
audits, but the backfill itself was busy-work this phase.

Lesson: confirm the dataset exists before scoping a migration unit.
The plan should have called out the empty-state explicitly + framed
U9 as "build the migration scaffold for future per-file audits"
rather than "backfill 800 sidecars."

### Prod-wire degraded mode — Variable resolver deferred

The 5 production loaders (NewDBResolvedTreeLoader,
NewDBScreenModeLoader, etc.) all return the SAME canonical_tree for
every mode of a screen because Phase 1 stores ONE tree per screen +
a `modes[]` sidecar with explicitVariableModes IDs. Per-mode
resolved trees with Variables resolved to concrete RGBA require a
Go-side mirror of `lib/projects/resolveTreeForMode.ts` — that's the
deferred TODO.

Today's behavior:
- theme_parity: catches structural drift between mode trees
  (type/name/layout mismatches + properties hand-painted into the
  stored tree without bindings). Does NOT catch
  "structurally-identical, only resolved Variable values differ" —
  needs the resolver.
- a11y_contrast: emits `a11y_unverifiable` when fills bind via
  Variables; computes when raw colors are on the node directly.
- cross_persona, a11y_touch_target, flow_graph,
  component_governance: unaffected (work on the canonical_tree
  shape directly).

Lesson: the headline AE-2 case ("hand-painted dark mode fill") fires
when the painted value is captured in the stored tree's mode-
specific node — i.e., when Phase 1's pipeline persisted dark's tree
for that screen instead of light's. For the cleanest AE-2 catch,
the resolver upgrade is owed. Schedule a Phase 3.5 / Phase 4 polish
unit.

## Quirks worth knowing

### `audit_jobs.priority` defaults to 50

Routine exports get priority=50 via the column DEFAULT. Recently-
edited flow exports could get 100 for premium ordering, but Phase 2
doesn't surface a way to set priority from the export pipeline.
Phase 4's auto-fix flow (and Phase 7's admin curation) are likely
places where 100-priority jobs make sense.

### Fan-out grouping is per-project, NOT per-flow

`fanout.go` `loadActiveFlowsLatestVersions` SQL groups by
`project_id` (one job per project's latest version), NOT by
`(project_id, flow_id)`. The Phase 2 plan U8 anticipated 25 jobs
for "5 projects × 5 flows = 25"; current code emits 5. The U12
Playwright spec asserts `>= 5 && <= 25` to pass against today's
code AND a future per-flow refactor.

### DS_AUDIT_LEGACY_SIDECARS env flag

Default OFF in Phase 2 — audit pipeline writes to SQLite via the
projects worker. Set to "1" for one-release rollback to JSON
sidecars. **Phase 3 should remove the flag entirely** since the
read-path cutover (U10) is shipped + verified.

### Phase 1 auditCoreRunner severity mapping

`internal/projects/runner.go:MapPriorityToSeverity` translates
audit core P1/P2/P3 → 5-tier:
  P1 + reason="deprecated"|"theme_break" → critical
  P1 (other reasons)                     → high
  P2                                     → medium
  P3 + has token_path/replaced_by/reason="custom" → low
  P3 (rationale-only)                    → info

Phase 2 new rules emit severity directly (no Priority indirection).
Phase 7 admin curation will override per-rule.
