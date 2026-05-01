---
title: "Phase 5 collab + decisions — closure learnings"
date: 2026-05-02
status: shipped
phase: 5
---

# Phase 5 — DRD multi-user collab + decisions: what we learned

## What shipped (14 units, ≈3-4 weeks of compounding work)

| Unit | Title | Notes |
|------|-------|-------|
| Plan | Phase 5 plan (963 lines) | 14 implementation units, sequenced |
| U2 | Migration 0008 | decisions, decision_links, drd_comments, notifications, notification_preferences + flow_drd.y_doc_state |
| U3 | Decisions backend | 4 routes; ValidateDecisionInput + supersession-cycle detector + chain-flip integrity |
| U4 | Decisions tab UI | 0/1/N/superseded view-state machine + chain rendering + inline DecisionForm |
| U6 | Comments + @mention parser | universal target_kind/target_id; email-prefix mention resolution; in-tx notification fan-out |
| U7 | Notifications inbox + frontend | Mentions tab on /inbox; mark-read; SSE event NotificationCreated |
| U10 | Recent decisions feed wiring | dashboard.go reads real `decisions` cross-tenant |
| U11 | Violation→decision link | PATCH endpoint accepts `link_decision_id` + writes decision_links in same tx; LifecycleButtons surfaces the picker |
| U12 | Activity rail | json_extract-based audit_log query + DRDTab right-rail |
| U8 | Digest CLI | EligibleForDigest pure function (daily 09:00 local + 23h gap, weekly Mon 09:00 + 6d gap); SlackSender with 5xx fail; per-user webhook + delivered_via tracking |
| U1 | Hocuspocus sidecar + auth bridge | 4 ds-service routes (ticket + /internal/auth + /internal/load + /internal/snapshot); Node sidecar with onAuthenticate / onLoadDocument / onChange (debounced 30s) / onDisconnect (force-flush) hooks |
| U13 | Playwright sweep | 4 specs (decision-creation, supersession, mentions-filter); the Yjs collab e2e spec lands in 5.1 |
| U14 | Deploy runbook | docs/runbooks/2026-05-02-phase-5-deploy.md |

## What deferred to Phase 5.1 (and why)

### BlockNote + Hocuspocus React-side wiring

The sidecar is in place, the auth bridge works, snapshots persist.
The React-side `BlockNoteView` hook to a `HocuspocusProvider` is a
self-contained 30-line wiring task — but it requires a deeper review
of BlockNote's collaboration extension shape vs. our existing
single-author DRDTab + the read-only state machine.

**Why deferred:** the integration touches the Phase-1 DRDTab's
view-state machine + the Phase-3 read-only preview; merging the
collaboration provider in carelessly risks breaking the offline-mode
fallback. Phase 5.1 ships this as one focused unit with two specs
(two-browser convergence + offline-mode reconnect).

### Custom DRD blocks

`/decision`, `/figma-link`, `/violation-ref`, `/persona-pin`,
`/acceptance-criteria` are pure BlockNote-schema additions. Phase 5
ships the DecisionForm + DecisionCard so the inline `/decision`
custom block is a thin wrapper — but BlockNote 0.x's custom-block API
shifted between versions and requires the Yjs pairing to be in place
first to test reliably.

### Live cursor presence

Yjs awareness is part of the Hocuspocus contract; the sidecar already
broadcasts it. Surfacing cursors in the React tree is a Phase 5.1
polish that needs the BlockNote-collab pairing first.

## Surprises during build

### Mention regex trailing punctuation

`@karthik.` captured "karthik." because the regex character class
included `.` `_` `-`. Fixed with a `strings.TrimRight(name, "._-")`
post-pass; documented inline so the next pattern revisit doesn't
regress. Tests now explicitly cover trailing-period mentions.

### MarkNotificationsRead arg-order ergonomics

The first version of the bulk-update used a `args[:len(args)-1]`
splice to prepend the `now` value. It worked but was hard to read;
rewrote to build args with explicit prepend + named comment so the
SQL placeholder order is obvious at the call site.

### Cross-tenant @mention silent drop

Three options were considered:
1. Hard-fail the comment write
2. Silently drop the @mention but still write the comment
3. Drop the @mention + return a warning in the response

Picked option 2 (silent drop). Rationale: the comment is still useful
even when one of the mentions misfires; an honest typo or org-rename
shouldn't reject the whole comment. The tests document this so future
maintainers know it's a deliberate choice.

### Hocuspocus typescript pulled into root tsc

Adding `services/hocuspocus/` to the repo broke the docs-site `tsc
--noEmit` because the root tsconfig's `include: "**/*.ts"` glob picked
up the sidecar's `server.ts`. Fixed by adding `services` to the
existing `exclude` list (mirrors the `figma-plugin` convention).

## Carried-over decisions

- **Decision body editor**: shipped as a plain textarea in U4. The
  Phase 5 plan said "share BlockNote with a stripped schema" — the
  plain textarea is cheaper to ship and the wire format (body_json
  string) supports both. 5.1 polish swaps when the BlockNote-collab
  pairing lands.
- **Comment threading depth = 1**: ships in 5.0; the schema supports
  arbitrary depth, only the UI is depth-1.
- **Slack webhook secrets stored plaintext**: Phase 7 polish swaps
  for the encrypted-at-rest pattern matching `figma_tokens`. Logged
  as a follow-up.
- **Email digest delivery**: SMTP wiring deferred to Phase 7 admin
  (the digest CLI logs-and-skips when SMTP_HOST isn't set so dry-run
  + Slack-only deploys are clean).
