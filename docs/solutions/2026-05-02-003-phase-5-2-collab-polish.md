---
title: "Phase 5.2 — DRD collab polish closure"
date: 2026-05-02
status: shipped
phase: 5.2
---

# Phase 5.2 — DRD collab polish: what we shipped

## Four polish units that close the Phase 5.1 carry-forward

| Unit | Outcome | Notes |
|------|---------|-------|
| P1 | RecentDecisions deep-link + admin reactivate | DashboardDecision shape extended with status/flow_id/slug |
| P2 | Slash menu friendly verbs | /decision, /figma-link, /violation-ref via SuggestionMenuController |
| P3 | SSE-driven live updates in custom blocks | decisionRef + violationRef refresh on inbox channel events |
| P4 | Figma frame thumbnail proxy | GET /v1/figma/frame-metadata with 5min cache |

## P1 — admin moderation

DashboardDecision shape gains status + flow_id + slug. The dashboard
query JOINs decisions → project_versions → projects to populate them
in one round-trip.

New repo method `AdminReactivateDecision(ctx, decisionID)` flips a
superseded decision back to accepted + clears its superseded_by_id.
Cross-tenant write; gated by `requireSuperAdmin` at the handler.
Idempotent on a non-superseded row (returns updated=0).

POST /v1/atlas/admin/decisions/{id}/reactivate is the new endpoint.
Audit-log row written under decision.admin_reactivate so the activity
rail surfaces the moderation.

RecentDecisions panel rows are now Links to /projects/<slug>?decision=<id>;
the Phase 5.1 P3 deep-link handler scrolls + flashes the matching card
on mount. Superseded rows render a "Reactivate" button that POSTs to
the new endpoint + optimistically flips the row's tint.

## P2 — slash menu verbs

`buildDRDSlashItems(editor)` returns the three DRD entries with
friendly titles ("Decision" / "Figma link" / "Violation ref") + a
"DRD" badge that visually groups them in the menu. Aliases cover both
the verb form and the schema name so power users typing the schema
name (decisionRef etc.) still hit the same item.

DRDTabCollab disables BlockNote's built-in slash menu via
`slashMenu={false}` on `<BlockNoteView>` and mounts a
`<SuggestionMenuController triggerCharacter="/" getItems={...}>` that
merges DRD items ahead of `getDefaultReactSlashMenuItems`. Default
BlockNote items still flow (paragraph, heading, list, etc.).

`filterDRDSlashItems(query, items)` is the predicate; matches on title
or aliases case-insensitively. The default items pass through because
their `title` field already covers the user query semantics.

## P3 — block-level SSE live updates

New SSE event `project.decision_changed` carries
`{slug, flow_id, decision_id, status, action, actor_user_id}`.
Published from:

  - `HandleDecisionCreate` — `action: "created"`. When the new
    decision supersedes another, a second event with
    `action: "superseded"` fires for the predecessor.
  - `HandleAdminReactivateDecision` — `action: "admin_reactivated"`.

Both publish on the existing tenant inbox channel
(`inbox:<tenant_id>`) so we don't introduce a new channel pattern.

Frontend gains module-level listener sets in lib/inbox/client.ts:
`subscribeDecisionChanges(cb)` + `subscribeViolationLifecycle(cb)`.
The single EventSource opened by `<InboxShell>` fans out to all
listeners; per-block subscribers piggyback so the DRD never opens N
parallel sockets.

DecisionRefRenderer + ViolationRefRenderer subscribe on mount + bump
a refreshKey when a matching event lands. The fetch effect re-runs;
status pill + body update without remounting the embedded card.

## P4 — Figma frame thumbnail proxy

GET /v1/figma/frame-metadata?url=<figma_url> parses the URL → calls
Figma /v1/images with the tenant's PAT → returns the signed CDN PNG
URL + the frame title. 5-minute in-process cache keyed by
(tenant_id, file_key, node_id).

`ParseFigmaURL(raw)` is the pure parser; handles both
/file/<key>/<title>?node-id=… and /design/<key> shapes; normalises
node-id from "1-23" to "1:23" (Figma's two ID formats).

`ServerDeps.FigmaPATResolver func(ctx, tenantID) (string, error)` is
the new injection point; main.go wires it via the existing
`dbConn.GetFigmaToken` + `cfg.EncryptionKey.Decrypt` pair. Tenants
without a configured PAT get an empty string + nil error so the
proxy falls back to URL-only metadata gracefully.

FigmaLinkRenderer in customBlocks.tsx now fetches the metadata on URL
change + renders the thumbnail when the response carries one;
gradient placeholder remains for tenants without a PAT.

Eight new Go tests cover the parser (file/design shapes, rejection),
cache TTL, errFigmaNotFound sentinel, key collision avoidance, and
the http client behaviour against a 404.

## What's left (Phase 5.3 polish, optional)

- Admin reactivate endpoint should publish a Phase 6-friendly SSE
  event so the mind graph re-renders the decision node when an
  admin re-flips it.
- Figma proxy: per-tenant rate-limit (5 req/sec) so a malicious tab
  can't drain the Figma PAT quota.
- BlockKit Slack message rendering for the digest worker (richer
  formatting than text-only).

These don't block any Phase 6 input contract; the schema + APIs are
stable.
