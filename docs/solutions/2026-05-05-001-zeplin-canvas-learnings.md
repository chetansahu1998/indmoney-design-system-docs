---
type: learnings
date: 2026-05-05
plan: docs/plans/2026-05-05-002-feat-zeplin-grade-leaf-canvas-plan.md
brainstorm: docs/brainstorms/2026-05-05-zeplin-grade-leaf-canvas-requirements.md
---

# Zeplin-grade leaf canvas — implementation learnings

Captured after shipping U1–U15 of the Zeplin canvas plan over a single execution session. Worth preserving for the next major canvas / override / asset-pipeline work.

## Architecture decisions that held up

### D1 dual-path renderer (autolayout flex + absolute fallback) — validated

The brainstorm spike against `2882:56419` proved the dual-path strategy mapped 1:1 to browser flexbox for autolayout subtrees and `position:absolute` for hand-positioned ones. Confirmed in production via `app/atlas/_lib/leafcanvas-v2/nodeToHTML.ts`. The 18% autolayout / 82% absolute split inside a real frame meant **both paths had to coexist from day one** — the early single-path approximation would have shipped a renderer that broke on every status-bar / loader / floating-element layout.

**Forward-applicable**: any future Figma → HTML mapping work should default to dual-path. Don't trust the brainstorm phase's assumed-mode percentages without walking the actual `canonical_tree`.

### D3a — autolayout reflow on text edit cascades for free via flexbox

Validated on real data: edited "▲+1.2 Market" → "Buy now extended copy" produced an exact +11.4 px text height growth, +1.58 px parent height growth, and ~0.8 px sibling-icon Y reflow. Browser layout did the cascade end-to-end with zero JS layout code.

**Forward-applicable**: keep canvas v1 contenteditable + debounced PUT. Resist BlockNote / Yjs on the canvas layer — D3a only works because we mount HTML elements that participate in the parent's flex container. A canvas / WebGL / SVG variant would have required reimplementing autolayout in JS.

### Override re-attachment in the same Stage 6 transaction

The 3-tier match (`figma_node_id` → `canonical_path` → `last_seen_original_text`) runs inside the canonical-tree write tx, not as a follow-up worker. This avoids the read-after-write race that bit Phase 6's graph rebuild. Captured in `services/ds-service/internal/projects/screen_overrides_reattach.go` + `pipeline.go` Stage 6.

**Forward-applicable**: any per-row "anchor to upstream re-imported data" pattern should mirror this. Read source rows BEFORE the write tx (Phase 7+8 deadlock learning), then mutate inside the tx.

## Gotchas

### Subagent worktrees branch from `main`, not the orchestrator's current branch

Every subagent dispatched in this session via `Agent({ isolation: "worktree" })` got a worktree branched from `main`. The orchestrator's `feat/zeplin-leaf-canvas` HEAD was invisible to them. Result: each subagent recreated files the prior unit had already added (`screen_overrides.go`, `0018_*.up.sql`).

Workarounds:
- **Explicit prompt context**: list the files prior units already created so the subagent knows to ADD rather than CREATE.
- **First-line `git reset --hard feat/zeplin-leaf-canvas`** in the subagent's prompt — this puts the worktree on the right base. Most subagents in batches 2+ adopted this.

**Forward-applicable**: when dispatching N subagents serially against an iterative branch, include the reset instruction in every prompt. Don't rely on the harness to do it.

### Migration numbering races between concurrent subagents

When U1, U2, U3, U4 each picked the next unused migration number, they all picked `0018` because each saw the same `main` base (which ended at `0017`). Dropping the duplicates at merge time was clean for `IF NOT EXISTS` migrations but would have been ugly for ALTER TABLE ones.

**Forward-applicable**: assign migration numbers up front in the plan (`U1 owns 0018, U2 owns 0019, ...`). The plan did this; subagents created their own anyway because they didn't see U1's commit.

### `pointer-events: none` on full-screen wrappers

The leaf-shell, leaf-canvas-wrap, and inspector-wrap all have `inset: 0` (full-screen). The default `pointer-events: auto` on each made them giant invisible event-eaters, swallowing wheel/pointer events meant for the brain canvas underneath. The fix (CSS): `pointer-events: none` on the wraps; `pointer-events: auto` on the actual visible children.

**Forward-applicable**: any full-screen wrapper that isn't itself an interaction target needs `pointer-events: none` from the start. The default is dangerous in absolute layouts.

### Worktree subagents' file edits leak into the parent repo

Two subagents (U5, U10) reported their initial Edit/Write tool calls landed in the parent repo path `/Users/.../indmoney-design-system-docs/services/...` instead of the worktree subdir, because the absolute-path tools don't auto-route by current branch. Both eventually compensated by re-applying via worktree-specific paths. The orchestrator caught uncommitted parent-repo changes during merge and reset cleanly.

**Forward-applicable**: dispatch instructions should explicitly say "use paths under `<worktree-path>/` for every Edit/Write tool call, not the parent repo path".

## Patterns worth replicating

### Optimistic-concurrency PUT with explicit `expected_revision`

DRD's `HandlePutDRD` shape was the precedent for `HandlePutOverride` (`screen_overrides_handler.go`). 200/409 with `current_revision + current_value` in the conflict body. Frontend save-state machine: `idle | saving | saved | conflict | error` with a refresh action that re-fetches and discards local edits. Last-write-wins; designers tolerate it because edits are typically scoped (one designer per leaf at a time).

### Asset cache key = `(tenant_id, file_id, node_id, format, scale, version_index)`

Re-export under a new project version naturally invalidates prior cached blobs because the version_index suffix changes. No separate eviction job. TTL sweeper can come later. Storage layout `data/assets/<tenant>/<version>/<node_id>.<format>` mirrors `screens.png_storage_key`.

### Render at the FRAME/GROUP wrapper level, not per-VECTOR

The brainstorm spike showed 614 individual VECTORs in one frame collapsed to ~280 icon clusters when wrapped FRAME/GROUP boundaries were used as the cluster unit. This is what Figma's plugin-level "export selection" does too. Cuts SVG/PNG fetches by ~55% and keeps icon visual identity intact.

## Things that took longer than planned

- **Hand-merging migration-numbering conflicts** across 4 subagents that each created their own `0018_*.up.sql`: ~30 min of merge-resolution time. The plan-pinned numbering would have prevented this.
- **Reconciling U2's `screen_overrides.go` with U3's parallel implementation**: U3's worktree was branched from main and didn't see U2's CRUD methods, so they wrote a parallel `ScreenOverride` struct + Insert/Get methods. Took a hand-merge into a new sibling file `screen_overrides_reattach.go` with schema renames (`screen_overrides` → `screen_text_overrides`, `updated_by` → `updated_by_user_id`).

## Things that worked first-try

- The brainstorm de-risk spike (4 HTML revisions) caught every fidelity gap before planning, so the plan's deferred-to-implementation list was honest from the start.
- The atomic-only click target with frame-pass-through (D5) was easy to ship — `findAtomicTarget` walks up from the click target until it hits a `data-figma-id` element, ignores frames.
- Single-click vs double-click discipline (D8) — designers got it on first interaction; no retraining needed.

## Open carry-forward items

- Pixel-perfect parity with Figma's renderer is iterative. v1 ships when 5 hand-picked complex frames pass designer review; future PRs handle gradients, shadows, masks, and blendMode edge cases.
- Basier Circle .ttf swap (currently Inter fallback per R10): a single-PR follow-up once licensing clears.
- Mixed-style text runs (`characterStyleOverrides`): not in v1; first follow-up after Inter→Basier.
- `TestRateLimit_PerTenantCap` is a flake unrelated to this plan; needs separate investigation (every U-subagent reported the same failure, so it was failing before this plan started).
- The brain-products endpoint's per-tenant rate limits at 120/min and 10000/day were bumped during the sheet-sync session; the test that asserts 9999 / 24h doesn't account for the burst-vs-refill interleave under load.
