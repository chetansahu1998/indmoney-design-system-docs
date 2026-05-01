---
title: "Phase 5 deploy runbook — DRD multi-user collab + decisions"
date: 2026-05-02
status: active
phase: 5
---

# Phase 5 deploy runbook

Phase 5 introduces three durable surfaces (Decisions, Comments,
Notifications), one Yjs collaboration sidecar (Hocuspocus), one cron
worker (digest), an activity rail, and the violation→decision link.
Database additions are non-breaking; the rollout adds one new service
process (Hocuspocus) alongside ds-service.

## Pre-flight

1. **Confirm migrations.** `0008_decisions_comments_notifications.up.sql`
   ships with this branch:
     - `decisions`, `decision_links`, `drd_comments`, `notifications`,
       `notification_preferences`
     - `flow_drd.y_doc_state BLOB`, `flow_drd.last_snapshot_at TEXT`
   All idempotent (`CREATE TABLE IF NOT EXISTS` / `ADD COLUMN`).

2. **Generate the Hocuspocus shared secret.**

   ```sh
   openssl rand -hex 32
   ```
   Store as `DS_HOCUSPOCUS_SHARED_SECRET` for both ds-service AND the
   Hocuspocus sidecar. They MUST match.

3. **Install Node deps** in `services/hocuspocus/`:
   ```sh
   cd services/hocuspocus && npm install
   ```

4. **Pick a digest cadence**. The `digest` CLI is opt-in per user; ops
   sets up a cron entry hourly @ :00 UTC:
   ```cron
   0 * * * * cd /opt/ds-service && ./digest --db=/var/lib/ds-service/ds.db
   ```

## Deploy steps

```sh
# 1. ds-service
cd services/ds-service
go build -o /tmp/ds-service ./cmd/server
go build -o /tmp/digest ./cmd/digest

# 2. Hocuspocus sidecar (Node 22+)
cd services/hocuspocus
npm install --omit=dev
DS_HOCUSPOCUS_SHARED_SECRET=$DS_HOCUSPOCUS_SHARED_SECRET \
DS_SERVICE_URL=http://127.0.0.1:7475 \
HOCUSPOCUS_PORT=7676 \
node --import tsx server.ts

# 3. docs site
npm run build
```

Expected topology in production: ds-service + Hocuspocus run in the same
docker-compose pod (or k8s pod); the sidecar's `/internal/drd/*` calls
stay on the loopback / pod-local interface. The shared secret is the
only auth on those routes.

## Smoke checks (post-deploy)

- `/projects/<slug>` → Decisions tab loads. Click "+ New decision",
  fill title, submit; row appears in the list.
- Open Acknowledge on a violation → "Link to decision" dropdown lists
  the just-created decision. Submit; verify the row carries the link
  on /atlas/admin's Recent Decisions panel.
- `/inbox` → toggle Mentions tab; if the user has any unread mention
  notifications they render with the unread (left-bar tinted) treatment.
- DRD tab → ActivityRail on the right. After committing a few audit
  events (decision create, comment create, lifecycle PATCH) the rail
  shows them grouped by Today / Yesterday.
- Hocuspocus: open the same project in two browsers. Both editors load.
  (Phase 5.1 wires the BlockNote collaboration extension — until then
  the sidecar runs but the editor stays single-author.)

## Operational

- **Digest cron**: ensure `DS_DB_PATH` is reachable; run with
  `--dry-run` first to confirm preferences match the firing window.
- **Hocuspocus health**: WebSocket on `:7676` should accept connections
  with a valid ticket and reject without one. Watch logs for "snapshot
  ok flow=… bytes=… rev=…" lines once Phase 5.1 wires the editor.
- **Audit log retention**: continue the Phase 4 `admin retention` cron
  (90-day window). Phase 5 adds `decision.*`, `comment.*`, `drd.*` event
  types — they all live under the same retention window.

## Rollback

Migrations are additive. Roll ds-service to the previous binary; the new
tables stay populated but unused. Stop Hocuspocus to revert to single-
author DRD (Phase 1 PUT /v1/projects/.../drd still works for read-only
clients). The docs site's Decisions tab + Mentions chip 404 cleanly on
the older build.

## Closure summary

Phase 5 ships:

- AE-4 backend closed (decisions persist + recent feed populates)
- AE-3 strengthened (violation acknowledge/dismiss can link to a
  decision)
- F6 partial — collab cluster (U1, U5, U9) is wired to the sidecar
  but BlockNote's collaboration extension hookup is the Phase 5.1
  polish that unblocks the multi-author UX

## Phase 5.1 polish (carried forward)

- BlockNote collaboration extension wiring (Yjs + Hocuspocus provider
  in DRDTab) — sidecar is in place; the React-side hookup is the
  remaining piece.
- DRD custom blocks: `/decision`, `/figma-link`, `/violation-ref`,
  `/persona-pin`, `/acceptance-criteria` — UI-only work.
- Live cursor presence — depends on Yjs awareness wiring above.
- DigestPayload BlockKit rendering for richer Slack messages.
- Decisions admin moderation (override Dismissed → Active per row in
  /atlas/admin's recent feed).

The Phase 5 plan tracks all of these as Phase 5.1; the schema + APIs
in Phase 5 stay stable so the polish is purely additive.
