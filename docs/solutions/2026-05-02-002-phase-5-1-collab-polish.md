---
title: "Phase 5.1 — DRD collab polish closure"
date: 2026-05-02
status: shipped
phase: 5.1
---

# Phase 5.1 — DRD collab polish: what we shipped

## Three small polish units that close the Phase 5 loop

| Unit | Outcome | Notes |
|------|---------|-------|
| P1 | BlockNote + HocuspocusProvider wired | Feature-flagged via NEXT_PUBLIC_DRD_COLLAB |
| P2 | Custom DRD blocks (decisionRef / figmaLink / violationRef) | Schema extension via createReactBlockSpec |
| P3 | Cursor presence + ?decision=<id> deep-link | Reduced-motion respect on cursor labels |

## P1 — Feature-flagged dispatch

The legacy single-author DRDTab stays the default; ProjectShell flips
to DRDTabCollab when `NEXT_PUBLIC_DRD_COLLAB === "1"` AND a flowID is
selected. That gives ops a per-deploy escape hatch — flip the flag
once the Hocuspocus sidecar is healthy, leave it off otherwise.

The collab editor flows through 4 connection states:
`minting → connecting → synced → (error | auth_failed)`. Editor only
mounts after first sync so BlockNote sees the full Y.Doc bootstrap.
PresenceBadge in the header lists the avatars of remote peers using
the awareness states the sidecar broadcasts.

## P2 — Custom blocks via createReactBlockSpec

Three custom blocks added to the BlockNote schema:

* **decisionRef** — stores `decisionID`; renders DecisionCard inline by
  fetching the live decision so the embed always reflects current
  status (proposed → accepted → superseded).
* **figmaLink** — stores `url` + derived `label`; renders an external-
  link card with a small gradient swatch placeholder. Frame-thumbnail
  proxy lands as Phase 5.2.
* **violationRef** — stores `violationID + slug`; renders a severity-
  tinted card with rule + status + suggestion fetched live from
  GET /v1/projects/:slug/violations/:id.

Schema is created at module scope via
`BlockNoteSchema.create({ blockSpecs: drdBlockSpecs })`. Each
`createReactBlockSpec` returns a creator function — invoke them at
module load so the resulting object is `Record<string, BlockSpec>`,
matching BlockNote's BlockSpecs type.

## P3 — Reduced-motion + deep-link

* `useReducedMotion()` controls `showCursorLabels`: switching from
  `"activity"` (default) to `"always"` when reduced-motion is on so
  labels don't flicker on remote keystrokes.
* `?decision=<id>` URL parameter scrolls the matching card into view
  + flashes a 1.5s outline pulse on first ok-state render. If the
  decision is superseded, the toggle auto-flips to include-superseded
  so the card is in the rendered set before scrolling.
* Tenant moderation deferred to Phase 5.2 (requires a backend schema
  change to attach `slug` to DashboardDecision so admins can deep-
  link from the recent-decisions feed without an extra fetch).

## What's queued for Phase 5.2

* Slash menu friendly verbs (`/decision`, `/figma-link`, `/violation-ref`)
  via slashMenuItems customization.
* SSE-driven live updates inside custom blocks (decision status change
  re-renders the embedded card without a remount).
* Figma-frame-thumbnail proxy endpoint for the figmaLink block.
* /atlas/admin tenant moderation: deep-link rows + reactivate-decision
  control on the recent-decisions panel (needs DashboardDecision schema
  bump first).

## Cross-deps frozen as Phase 6 inputs

Phase 6 (Mind graph) consumes the Decisions table + Activity rail
data Phase 5/5.1 ship. Both are stable; Phase 6 plan can be written
without Phase 5.2 polish landing first.
