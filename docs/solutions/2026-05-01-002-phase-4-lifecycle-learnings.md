---
title: Phase 4 closure — Violation lifecycle + designer surfaces
date: 2026-05-01
status: shipped
phase: 4
parent_plan: docs/plans/2026-05-01-002-feat-projects-flow-atlas-phase-4-plan.md
note: Closure doc reconstructed retroactively after Phase 6 ship audit found Phase 4 had no closure record. Code shipped between 2026-05-01 and 2026-05-02 commits; this doc captures what landed by reading the live code surface.
---

# Phase 4 closure — reconstructed

## What shipped

All 14 implementation units from the Phase 4 plan landed. Verified by file presence + handler signatures in `services/ds-service/internal/projects/server.go` and the migrations directory.

| Plan unit | Code surface |
|---|---|
| U1 — Lifecycle state machine + audit log | `lifecycle.go`, `auditlog.go`, `lifecycle_test.go` |
| U2 — `inbox:<tenant>` SSE channel + ticket | `inbox.go`, `HandleInboxEventsTicket` + `HandleInboxEvents` |
| U3 — Dismissed-violation carry-forward | `carry_forward.go`, `carry_forward_test.go`, migration `0006_dismissed_carry_forwards.up.sql` |
| U4 — Designer inbox + filter chips | `HandleInbox` + `inbox_test.go` |
| U5 — Bulk acknowledge | `HandleBulkAcknowledge` |
| U6 — Per-component reverse view | `HandleComponentViolations` + `components.go` |
| U7-U8 — Audit fan-out priority + worker | `fanout.go`, `worker.go` (Phase 1's pool extended) |
| U9 — DS-lead dashboard summary | `dashboard.go`, `dashboard_test.go`, `HandleDashboardSummary` |
| U10 — Violation lifecycle PATCH | `HandlePatchViolation` |
| U11-U12 — Plugin auto-fix round-trip | `HandleViolationGet` + `HandleViolationFixApplied` |
| U13 — Component reverse view UI | `app/components/[slug]/page.tsx` "Where this breaks" section |
| U14 — Backfill marker for old violations | `backfill.go`, migration `0004_backfill_markers.up.sql` |

## Decisions actually made vs. plan

Without contemporaneous notes the variance is hard to reconstruct, but the artefacts confirm:

1. **Carry-forward keyed on `(tenant_id, screen_logical_id, rule_id, property)`** — migration `0006` shows the composite PK. Plan section "Carry-forward semantics for Dismissed" called for this shape.
2. **Synchronous auto-fix round-trip** — `HandleViolationFixApplied` is request-scoped, not queued. Phase 5/6 closures cite this as "synchronous plugin → backend → plugin flow" so the design held.
3. **`inbox:<tenant>` is the channel pattern Phase 5 + 6 inherited.** Confirmed by Phase 6's reuse in the `graph:<tenant>:<platform>` channel (mirroring exactly the same ticket-redeem pattern).

## Pre-existing bugs surfaced after Phase 4

None known until Phase 6's `UpsertProject` infinite-recursion finding (Phase 1 origin, exposed by Phase 6 multi-platform tests). Already fixed in `repository.go` at the same time as this closure (Phase 6 follow-up).

## Performance

Not measured retroactively. Phase 4 plan listed budgets:

- Inbox list ≤80ms p95 cold — unverified
- Dashboard 5-aggregation parallel `sync.WaitGroup` — code present in `dashboard.go`, no perf trace recorded

These are observable through current production telemetry; if a regression surfaces, the budget table in the plan is the reference.

## Carried forward to Phase 5+

Per the plan's "Deferred to Follow-Up Work":
- DRD multi-user collab via Yjs → Phase 5 ✅ shipped
- Custom DRD blocks → Phase 5 ✅ shipped
- First-class Decision entities → Phase 5 ✅ shipped
- Mind graph → Phase 6 ✅ shipped
- Per-resource ACL grants → Phase 7 (still open)
- Notifications + digest → Phase 5 ✅ shipped
- Search → Phase 8 (still open)

## Closing note

The plan's frontmatter says `status: active` — that's stale. Phase 4 has been the foundation Phase 5 + 5.1 + 5.2 + 6 have built on for ~3 weeks now. Updating the frontmatter to `shipped` along with this closure.
