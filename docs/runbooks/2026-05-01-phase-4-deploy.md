---
title: "Phase 4 deploy runbook — violation lifecycle + designer surfaces"
date: 2026-05-01
status: active
phase: 4
---

# Phase 4 deploy runbook

Phase 4 ships the violation-lifecycle UX (Acknowledge / Dismiss / Fix /
admin override), the designer personal inbox at `/inbox`, the per-component
reverse view ("Where this breaks"), the DS-lead dashboard at `/atlas/admin`,
and the plugin auto-fix integration. Database additions are non-breaking;
the rollout is a standard rolling deploy of ds-service + the docs site.

## Pre-flight

1. **Confirm migrations**. `0006_dismissed_carry_forwards.up.sql` and
   `0007_inbox_indexes.up.sql` ship with this branch. Both are idempotent
   (`CREATE TABLE IF NOT EXISTS` / `CREATE INDEX IF NOT EXISTS`); rerunning
   on already-migrated DBs is a no-op.
2. **Verify `recharts` dep**. `npm install` in the docs site root pulls in
   `recharts ^3.x`. The dashboard panels are lazy-loaded into a
   `chunks/dashboard` split; route shells (/projects, /inbox, /components)
   stay at their pre-Phase-4 budgets.
3. **Plugin manifest**. The `figma-plugin/auto-fix/` module ships behind
   the same plugin id (no manifest change required). Phase 4.1 polish
   adds the deeplink-parameter declaration.

## Deploy steps

```sh
# 1. ds-service
cd services/ds-service
go build -o /tmp/ds-service ./cmd/server
# … your deploy mechanism …

# 2. docs site
npm run build
# … your deploy mechanism …

# 3. plugin (if rolling out auto-fix today; manifest unchanged)
cd figma-plugin
npx tsc -p .
# upload code.js to Figma plugin admin
```

## Smoke checks (post-deploy)

- `/inbox` loads for an authenticated designer. Filter chips reflect URL
  state. Bulk-acknowledge fades rows + drops the count.
- `/atlas/admin` loads for super-admin. Five panels render. Switching the
  trend window (4w / 8w / 12w / 24w) refetches.
- `/components/<slug>` shows the "Where this breaks" section under the
  spec sections; non-zero violations render the per-flow row list.
- `PATCH /v1/projects/:slug/violations/:id` round-trip from the
  ViolationsTab inline buttons. Audit-log row is written. SSE subscriber
  in the project's Violations tab reconciles status in place.

## Rollback

The migrations are additive. Roll back ds-service to the previous binary
without touching the database; the new tables sit unused, which is
harmless. The docs site's `/inbox` and `/atlas/admin` routes 404 cleanly
on the old build.

## Operational hygiene

Schedule the audit-log retention CLI to run weekly:

```sh
DS_DB_PATH=/var/lib/ds-service/ds.db ./admin retention --days=90
```

Phase 0's `audit_log` table has no built-in retention; the CLI removes
rows older than the cutoff. Default cutoff is 90 days.

## Acceptance examples closed

- **AE-2** (theme-parity auto-fix round-trip) — closed via U11+U12. The
  plugin reads the deeplink, fetches the violation, applies the fix, and
  POSTs `/fix-applied` which flips the status to `fixed`.
- **AE-3** (cross-persona dismiss with rationale) — closed via U1+U6. The
  ViolationsTab inline Dismiss button posts the rationale; the audit_log
  row records actor + timestamp.
- **AE-7** (token-publish fan-out lands in designer inboxes) — closed via
  U4+U5. Fan-out lands violations under the latest version; the inbox
  surface shows them; designers triage; the dashboard count drops.

## Known follow-ups (Phase 4.1)

- Deeplink → UI handoff in the plugin (manifest parameters + ui.html
  panel for the auto-fix preview/Apply step). The dispatch path already
  ships in U11.
- Cross-tenant SSE for the inbox so other clients' lifecycle changes
  surface without a route remount.
- Variable-resolver UI for picking the auto-fix target token.
