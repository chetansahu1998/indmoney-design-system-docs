---
title: "feat: Projects · Flow Atlas — Phase 2 of 8 (audit engine extensions + fan-out + sidecar migration)"
type: feat
status: active
date: 2026-04-30
origin: docs/brainstorms/2026-04-29-projects-flow-atlas-requirements.md
predecessor: docs/plans/2026-04-29-001-feat-projects-flow-atlas-phase-1-plan.md
---

# feat: Projects · Flow Atlas — Phase 2 of 8 (audit engine extensions)

> **This is Phase 2 of an 8-phase product build.** Phase 1 shipped the round-trip foundation: schema, plugin Projects mode, fast-preview pipeline, project-view shell, audit-job worker pool of size 1 with `RuleRunner` interface, and existing audit core surfacing through the Violations tab. Phase 2 plugs the new rule classes that the brainstorm calls out (R7: theme parity, cross-persona, a11y, flow-graph, component governance) into the same `RuleRunner` slot, scales the worker pool, ships the audit fan-out trigger that re-audits every active flow when a rule or token catalog changes, and migrates the legacy `lib/audit/*.json` sidecars into the SQLite store. Estimate: 3-4 weeks across ~10 implementation units.

## Overview

Phase 1 shipped the spine. Phase 2 puts the muscle on it. The brainstorm's R7 enumerates five new rule classes that Phase 1 deliberately stubbed:

1. **Theme parity** — for each mode pair, structural diff between the two canonical_trees outside `explicitVariableModes` must be zero. Any delta = **Critical** violation. Catches cases where a designer hand-painted a button background in dark mode instead of binding to the token (origin AE-2).
2. **Cross-persona consistency** — components / token bindings / screen counts must be coherent across personas of the same flow. A `Toast` used in `Default` but missing in `Logged-out` = **High** violation (origin AE-3).
3. **WCAG AA accessibility** — text contrast ≥ 4.5:1 (3:1 for large text), interactive touch targets ≥ 44×44pt. Surface-by-surface against the active theme.
4. **Flow-graph** — dead-end screens, orphan screens, cycles without exit, missing required state coverage (Loading / Empty / Error). Built on Figma prototype connections, falling back to name-pattern inference where prototype links are absent.
5. **Component governance** — detached instances, override sprawl (instance with too many overrides → likely should be a new component), component sprawl (a flow using 80+ distinct components → information architecture concern).

Phase 2 also ships:

- **Audit fan-out trigger** (origin F9, R10, AE-7): when DS lead publishes a token catalog or curates a rule, every active flow's latest version re-audits. Worker pool grows from 1 to 6 with heartbeat-refresh-and-takeover lease semantics. Priority queue (recently-edited flows first); rate-limited so a 47-flow token publish doesn't melt the system.
- **`lib/audit/*.json` → SQLite migration**: Phase 1 left the existing JSON sidecar pipeline (consumed by `/files/[slug]`) untouched. Phase 2 backfills those sidecars into the new `violations` + `screens` tables and migrates the read path. Single source of truth for violations across the docs site.
- **Default-severity catalog**: rules carry a default severity table seeded into a new `audit_rules` table (per-tenant overrides land in Phase 7 when the DS lead curation editor ships). This unblocks Phase 7 from a schema migration during admin work.

**Phase 2 deliberately does NOT ship:**

- DS lead rule curation editor (Phase 7 admin).
- Auto-fix in plugin (Phase 4 — Phase 2 fixes are advisory only via the `suggestion` field on Violation).
- Inbox-side bulk-acknowledge UX (Phase 4).
- Comment threads on violations (Phase 5).
- Decision-violation linking (Phase 5).
- CEL rule expression DSL (Phase 7 — Phase 2 rules are compiled Go code).

The vertical slice: a designer re-exports a flow with a hand-painted dark-mode fill → Phase 2's theme-parity rule catches it as Critical → backend records the violation in SQLite → Violations tab shows it with the new rule_id and category badge → DS lead pushes a renamed token via the new admin endpoint → 47 flows re-audit in under 5 minutes → designer inboxes refresh.

---

## Predecessor & Continuity

- **Predecessor plan:** [`docs/plans/2026-04-29-001-feat-projects-flow-atlas-phase-1-plan.md`](2026-04-29-001-feat-projects-flow-atlas-phase-1-plan.md). Phase 1 delivered the schema (10 tables), `RuleRunner` interface, `WorkerPool{ size: 1 }` with lease columns, channel-notification queue, screen_modes + screen_canonical_trees split, and the projects API surface.
- **Surfaces inherited from Phase 1:**
  - `services/ds-service/internal/projects/runner.go` — `RuleRunner` interface (`Run(ctx, version) ([]Violation, error)`) is the plug-in point. Phase 2 adds 5 sibling impls and registers them in a slice the worker iterates.
  - `services/ds-service/internal/projects/worker.go` — `WorkerPool{ size: 1 }` with `leased_by` / `lease_expires_at` columns. Phase 2 grows `size` to 6 (env-tunable) and adds the heartbeat-refresh goroutine.
  - `services/ds-service/internal/projects/repository.go` — `TenantRepo` is mandatory. All new rule code reads via this layer; no raw SQL.
  - `lib/projects/types.ts` + `components/projects/tabs/ViolationsTab.tsx` — Phase 2's new `category` field surfaces in filter chips; `severity` is unchanged.
- **Continuity invariants Phase 2 must not break:**
  - `internal/audit/types.SchemaVersion = "1.0"` stays.
  - `internal/projects/types.ProjectsSchemaVersion` bumps to `"1.1"` (additive: new `category` enum on Violation, new `triggered_by` enum on AuditJob).
  - `lib/audit/<slug>.json` sidecar **read** path remains live until U10 ships the cutover; **writes** stop at U7 (sidecar generator deprecated, all writes go through SQLite).
  - All Phase 1 routes registered in `cmd/server/main.go` are unchanged.

---

## Phased Delivery Roadmap (reference)

This is **Phase 2 of 8** — see Phase 1's roadmap table for the full arc. Phase 2 advances:
- **R7** (audit rule classes — full implementation of all 5 classes)
- **R10** (audit re-runs on rule/token publish)
- **R8** in part (Active lifecycle preserved; Acknowledged/Dismissed lifecycle UX is Phase 4)

---

## Problem Frame

Phase 1's Violations tab today shows the *existing* audit core's output: token-color drift, text-style drift, padding/gap drift, radius drift, component-matching findings. That's a fraction of what the brainstorm promises. The five missing rule classes are the high-value findings that *only* this product can produce — Figma cannot tell a designer that a Toast component is missing from the Logged-out persona, or that a button is hand-painted in dark mode but bound in light, or that a screen is unreachable from the start node. Without these, Projects is a pretty viewer over the existing audit; with them, it's the system the brainstorm describes.

The fan-out story is equally load-bearing. Origin AE-7: "DS lead publishes a renamed token; ~5 minutes later, 47 flows are flagged with the rename suggestion." Phase 1 has the worker; Phase 2 has the trigger. Without it, rule changes are stranded — designers wouldn't see the consequences until they re-export individually, which destroys the "make the right things visible" governance posture the product depends on.

---

## Animation Philosophy

Phase 2 inherits Phase 1's animation library (GSAP + Lenis + reduced-motion) unchanged. New surfaces:

| Surface | Animation treatment |
|---------|---------------------|
| Violations tab category filter chips | GSAP fade-in on chip change (~200ms); chip selection state via Framer Motion `layoutId` underline morph. |
| New severity arrival (live SSE update) | Newly-arrived violation row: brief background flash (`opacity 0.6 → 0` over 600ms) + chip pulse. mhdyousuf-style snappy attention without being distracting. |
| Fan-out progress (DS lead admin endpoint response) | Full-screen toast with progress bar driven by SSE `audit.fanout_progress` events (`{enqueued, completed, total}`); toast collapses to a corner pill at first scroll. |
| "Audit re-running" badge on a project view receiving a fan-out re-audit | Pulsing dot next to the version selector; severity counts go grey + animated until completion. |

All respect `prefers-reduced-motion: reduce` per Phase 1's `useReducedMotion` hook.

---

## Requirements Trace

This plan advances:

- **R7** — Audit engine extends with all five rule classes (theme parity, cross-persona, a11y, flow-graph, component governance). Existing rules retained.
- **R8 (partial)** — Violations carry `category` and `severity`; Active lifecycle is the only state Phase 2 enforces. Acknowledged/Dismissed transitions ship in Phase 4.
- **R9** — Severity stays 5-tier (Critical → Info). Phase 2 seeds a default severity table (`audit_rules`); per-tenant overrides land in Phase 7.
- **R10** — Audit re-runs on (a) new export (already Phase 1), (b) DS-lead rule curation change (Phase 2 endpoint, full UI in Phase 7), (c) DS-lead token catalog publish (Phase 2 endpoint + CLI, full UI in Phase 7).
- **R14 (extension)** — Violations tab gains category filters in addition to existing severity grouping.

**Origin actors:** A1 (Designer), A3 (DS lead, via admin endpoint + CLI for now), A6 (ds-service), A8 (Docs site).

**Origin flows:** F2 audit phase (full implementation now lives in Phase 2 — Phase 1 stubbed it with existing audit.Audit only), F9 (Audit fan-out on rule/token change — full).

**Origin acceptance examples:**
- **AE-2** (Theme parity catches a manual paint) — Full Phase 2.
- **AE-3** (Cross-persona consistency) — Full Phase 2.
- **AE-7** (Token publish fans out) — Full Phase 2.
- AE-1 / AE-6 — already Phase 1.
- AE-4 / AE-5 / AE-8 — Phase 5 / 6.

---

## Scope Boundaries

### Deferred for later
*(Carried verbatim from origin — product/version sequencing.)*

- Branch + merge review workflow.
- Comprehensive auto-fix beyond token + style class.
- Cross-platform side-by-side comparison surfaces.
- Live mid-file audit (in-Figma "linter while you design" mode).
- PRD / Linear / Jira integration.
- AI-suggested decisions / DRD drafts.
- Mobile designer app / iPad viewer.
- Public read-only sharing.

### Outside this product's identity
*(Carried verbatim from origin — positioning rejection.)*

- Replacing Figma. Atlas remains read-only.
- Replacing Notion / Confluence org-wide.
- Replacing Linear / Jira. Violations are not tickets.
- Replacing Mobbin.
- Hard governance / blocking PRs.

### Deferred to Follow-Up Work
*(Plan-local — implementation work intentionally split into other phases.)*

- **Acknowledge / Dismiss / Fix lifecycle UX** — Phase 4 (designer surfaces). Phase 2 stores `status='active'` only; the lifecycle column already exists from Phase 1.
- **DS lead rule curation editor** (full UI for rule severity overrides, category renames, custom rules) — Phase 7. Phase 2 ships the `audit_rules` schema + admin endpoint hooks so Phase 7 doesn't migrate.
- **CEL DSL for rule expressions** — Phase 7. Phase 2 rules are native Go.
- **Auto-fix CTA in Violations tab + plugin Audit-mode handoff** — Phase 4. Phase 2 includes the `suggestion` and `auto_fixable` fields on every Violation but the UI is advisory only.
- **Decision-violation linking** (origin AE-4) — Phase 5 with DRD/decisions full implementation.
- **`/files/[slug]` route migrating off the JSON sidecar read path** — included in Phase 2 (U10) since stopping the writes would orphan the read otherwise.

---

## Context & Research

### Relevant Code and Patterns

- `services/ds-service/internal/projects/runner.go:50-150` — `RuleRunner` interface and Phase 1's `auditCoreRunner` impl. Phase 2's new rule classes are siblings: each implements `Run(ctx, version *Version) ([]Violation, error)`. The worker iterates over `[]RuleRunner`. Adding rules = appending to a slice in `cmd/server/main.go`.
- `services/ds-service/internal/projects/runner.go:236-268` — `MapPriorityToSeverity` is the Phase 1 baseline for translating audit core P1/P2/P3 → 5-tier. Phase 2's new rules emit severities directly (no Priority indirection); the `audit_rules` table holds the defaults. Existing rule paths still go through `MapPriorityToSeverity` for backward compatibility.
- `services/ds-service/internal/projects/worker.go` — `WorkerPool{ size: 1 }`. Phase 2 changes the constant to `size: 6` (env-overridable via `DS_AUDIT_WORKERS`), adds heartbeat refresh + takeover. Lease columns already exist.
- `services/ds-service/internal/projects/repository.go` — `TenantRepo` and `assertFlowVisible` are the trust boundary. New rules read canonical_trees + screen_modes via `TenantRepo.GetCanonicalTree(screenID)` and `TenantRepo.GetScreenModes(screenID)`. Both already exist from Phase 1.
- `services/ds-service/internal/audit/engine.go` — `Audit(tree, tokens, candidates, opts)` returns `AuditResult`. Phase 2's rule runners read its `FixCandidates` for *adjacent* findings (e.g., contrast checks need the same `tokens.Resolved` map the existing engine builds). New rules call `audit.NewMatcher(...)` for component-matching utilities.
- `lib/audit/<slug>.json` — existing sidecar format. Schema mirrored at `lib/audit/types.ts`. Phase 2 stops writing these (sidecar writer deprecated at U7) and migrates the read path at U10. Backfill script reads existing sidecars into SQLite for query parity (U9).
- `app/files/[slug]/page.tsx` — current consumer of the JSON sidecars. Phase 2 (U10) repoints this to the new `GET /v1/audit/by-slug/:slug` route which queries SQLite. Schema-compatible response shape — frontend changes nothing.
- `services/ds-service/internal/sse/broker.go` — Phase 1's broker. Phase 2 adds new event types: `audit.fanout_started`, `audit.fanout_progress`, `audit.fanout_complete`, `audit.rule_changed`, `audit.tokens_published`. Same publish/subscribe pattern.
- `lib/tokens/indmoney/{semantic-light,semantic-dark}.tokens.json` — DTCG token sources. The token-catalog-published trigger watches these (or is fired manually via CLI; CI-driven detection is Phase 7).
- `services/ds-service/cmd/icons/main.go` — pattern for CLI commands that touch the DB. Phase 2 adds `services/ds-service/cmd/admin/main.go` with `admin refan-out --reason=tokens-published` and `admin refan-out --rule-id=X` subcommands.
- `figma-plugin/code.ts` Audit mode — existing mode that the brainstorm's auto-fix story (Phase 4) builds on. Phase 2 doesn't touch the plugin.

### Institutional Learnings

`docs/solutions/` still does not exist in this repo. Phase 1 noted it should be initialized "once Phase 1 ships." Phase 2 honors that; first solution doc lands at `docs/solutions/2026-MM-DD-NNN-projects-phase-1-learnings.md` after the Phase 1 PR merges (out of scope for this plan, listed as a documentation follow-up).

### External References

- **WCAG 2.1 Contrast (Minimum) — Success Criterion 1.4.3** — relative luminance L = 0.2126·R_lin + 0.7152·G_lin + 0.0722·B_lin where each channel is sRGB-linearized; contrast = (L1+0.05)/(L2+0.05) where L1 is lighter. Threshold 4.5:1 normal, 3:1 for ≥18pt regular or ≥14pt bold. <https://www.w3.org/TR/WCAG21/#contrast-minimum>
- **Figma File API — `prototype_*` fields** — `prototypeStartNodeID` on file response; `transitions` and `interactions` on individual frame nodes carry the navigation graph (`source_node_id`, `destination_node_id`, `trigger`, `action`). For Phase 2's flow-graph rule we re-pull node JSON with `geometry=paths&plugin_data=shared` plus `branch_data=true` to get prototype connections. <https://www.figma.com/developers/api#files-endpoints>
- **Figma node `mainComponent` for detached-instance detection** — INSTANCE nodes have a `componentId` (or `mainComponent.id` in the new shape) pointing to a COMPONENT or COMPONENT_SET; if null while the node visually mimics a component pattern, it's detached. Phase 2 uses the existing `audit.NewMatcher` to score "looks like component X" before flagging.
- **DTCG Format Module W3C spec** — `lib/tokens/indmoney/*.tokens.json` already follows this. Token-publish trigger compares the latest mtime/hash against a stored marker.
- **APCA (Accessible Perceptual Contrast Algorithm)** — alternative to WCAG 2.1 contrast; better for non-black-on-white. **NOT used in Phase 2.** WCAG 2.1 stays the standard until WCAG 3.0 lands and the org commits. APCA can ship as a per-rule severity override in Phase 7.

### Cross-cutting tech context (carried from Phase 1)

- **Stack:** Next.js 16.2.1 + React 19.2.4. Tailwind v4. Pagefind for search. Framer Motion installed.
- **Backend:** Go (stdlib `net/http`, `modernc.org/sqlite`, JWT, no chi/echo).
- **Plugin:** Vanilla JS in `ui.html`. TypeScript `code.ts` compiled via `npx tsc -p .`.
- **Phase 1 conventions to honor:** denormalized `tenant_id` everywhere; `TenantRepo` mandatory; cross-tenant 404 (no existence oracle); idempotent worker transactions; channel-notification (no polling).

---

## Key Technical Decisions

### Rule classes & catalog

- **Rule registry pattern.** A new file `services/ds-service/internal/projects/rules/registry.go` exports `Registry()` returning a `[]RuleRunner` ordered by execution priority (theme parity first — fast and bounded, flow-graph last — needs prototype data). `cmd/server/main.go` calls `Registry()` at boot and passes the slice to the worker. Adding a 6th rule class = appending one entry.
- **Default-severity table seeded at first boot.** `audit_rules` table (`rule_id PK, name, description, category, default_severity, enabled, target_node_types, expression TEXT NULL`). Phase 2 ships a `seed_audit_rules.up.sql` migration that inserts ~30 rule rows. `expression` is null for all Phase 2 rules (compiled Go). Phase 7's CEL DSL writes into this column.
- **Severity is per-rule, not per-fix.** Phase 1's `MapPriorityToSeverity` (P1/P2/P3 → severity) stays for the existing audit core's FixCandidates. New rule classes set severity directly, looking up `audit_rules.default_severity` at runtime. Per-tenant overrides via Phase 7.
- **Rule categories.** New enum `category` on Violation: `theme_parity | cross_persona | a11y_contrast | a11y_touch_target | flow_graph | component_governance | token_drift | text_style_drift | spacing_drift | radius_drift | component_match`. The existing audit core's findings get categorized via a small mapping in `runner.go`. Frontend filters by category.
- **Rule expression DSL deferred.** No CEL in Phase 2. Rules are compiled Go. Reasoning: getting the rules right with deterministic Go semantics is more valuable than upfront DSL flexibility; once 5+ rules ship and DS leads start asking for tuning, Phase 7 introduces CEL with concrete known-good rules to translate.

### Theme parity

- **Algorithm: structural diff at canonical-tree depth.** For each mode pair `(light_tree, dark_tree)`, walk both in parallel; at each node compare `(type, name, hash_of_layout_props, hash_of_visual_props_excluding_explicitVariableModes_and_boundVariables)`. Any mismatch = Critical violation.
- **Why not raw deep-diff?** A naive deep-equal flags every Variable-bound property as different (since the resolved values legitimately differ between modes). Excluding `boundVariables` and `explicitVariableModes` from the comparison lets us catch only the cases where a designer hand-edited a property without binding to a variable.
- **Performance:** O(N) where N = node count per screen. ~200ms p95 for a 4886-tall mobile flow with ~500 nodes per mode. Single-pass; no nested compare needed.
- **Output:** one Violation per offending node. `property = "fill" | "stroke" | "effect" | "layout"`, `observed = "raw color #6B7280"` or similar, `suggestion = "Bind to colour.surface.<resolved-light>" | "Use the same node structure across modes"`. Severity always Critical (configurable in Phase 7).

### Cross-persona consistency

- **Algorithm: set diff on component instances per persona of same flow-base-path.** Group `flows` by `(project_id, path_excluding_persona)`; within each group, build a `Set<componentKey>` per persona; emit High violations for components in `A \ B` (with persona pair noted in observed).
- **What counts as "the same flow across personas":** the brainstorm's `path` field is canonical (e.g., `Indian Stocks/F&O/Learn Touchpoints`). Persona is one column on `flows`. The cross-persona check operates on flows with identical `(project_id, path)` but different `persona_id`.
- **Edge cases handled:**
  - Persona ships ALONE (no peer) → skip; not actionable.
  - Two personas, identical component sets → no violation.
  - Three+ personas → pair-wise compare; emit one violation per missing-component-per-persona to keep findings actionable.
- **Output:** `RuleID = "cross_persona_component_gap"`, `category = "cross_persona"`, severity High by default. `suggestion = "Add <ComponentName> to <Persona> or Acknowledge as deliberate"`.

### WCAG AA accessibility

- **Contrast rule.** For each TEXT node, compute foreground (text fill resolved against current mode) and background (the nearest opaque ancestor's resolved fill, or white if none). Compute relative-luminance contrast per WCAG 2.1. Threshold: 4.5:1 normal, 3:1 if `fontSize ≥ 18pt regular` or `fontSize ≥ 14pt + fontWeight ≥ 700`.
- **Touch target rule.** For each INSTANCE matched as a clickable atom (Button, IconButton, Link, Tab — known set from `public/icons/glyph/manifest.json` atoms), assert `width ≥ 44 AND height ≥ 44`. Severity High.
- **Edge cases:**
  - Transparent backgrounds → walk up parent chain until opaque or root; if no opaque background found, assume white at root.
  - Gradient backgrounds → use the brightest stop (worst-case for dark text, best-case for light text — choose the stop that minimizes contrast as the assertion baseline).
  - Image backgrounds → flag as `a11y_unverifiable` Info (designers can manually acknowledge).
- **Per-mode evaluation:** contrast rule runs once per `screen_modes` row. Theme-parity Critical surfaces structural problems; a11y surfaces resolved-value problems per mode.
- **Output:** `RuleID = "a11y_contrast_aa" | "a11y_touch_target_44pt"`, `category = "a11y_contrast" | "a11y_touch_target"`, severities High and High respectively. `suggestion` includes the computed ratio + threshold.

### Flow-graph

- **Source of edges.** Phase 2 re-fetches Figma node JSON with prototype data (`branch_data=true&plugin_data=shared`) per flow at audit time. New `screen_prototype_links` table caches the edges so re-audits don't refetch.
- **Algorithm:**
  - **Orphan**: screens with zero in-bound edges (and not the start node) → Medium.
  - **Dead-end**: screens with zero out-bound edges (and no terminal-state name like "Success" / "Confirmation") → Medium.
  - **Cycle without exit**: strongly-connected component with no exit edge → High.
  - **Missing required state coverage**: heuristic — if a flow has data-fetching screens (any screen with `// state:loading` annotation OR an instance of `LoadingState` / `Skeleton` atom), but no `EmptyState` / `ErrorState` siblings → Low. Detection list of required state names lives in `audit_rules.target_node_types` for Phase 7 tunability.
- **Fallback when prototype data is sparse.** If Figma file has fewer than 1 prototype link per 2 screens (rough heuristic), skip dead-end / orphan / cycle (would produce noise) but still run "missing state coverage". Persist `audit_jobs.metadata = {flow_graph_skipped: "insufficient prototype data"}` so the UI shows a hint.
- **Output:** `RuleID` per type, `category = "flow_graph"`, severities Medium/Medium/High/Low.

### Component governance

- **Detached instance rule.** Walk all INSTANCE nodes; flag any with null `componentId` AND name pattern matching a known component slug (use `audit.NewMatcher`'s scoring; threshold ≥0.5). Severity Medium.
- **Override sprawl rule.** For each INSTANCE, count overrides on `componentProperties`, `boundVariables`, and direct prop sets; flag if `>= 8` overrides on a single instance. Severity Low.
- **Component sprawl rule.** Per flow, count distinct `componentSetKey` values; flag if `>= 80`. Severity Info — high count isn't always wrong but is worth surfacing.
- **Output:** `RuleID = "component_detached" | "component_override_sprawl" | "component_set_sprawl"`, `category = "component_governance"`.

### Worker pool scaling

- **Pool size = 6, env-overridable.** `DS_AUDIT_WORKERS` env var; default 6. Phase 1's 1 was a deliberate choice (no contention to debug); Phase 2 grows to handle the fan-out at AE-7's "47 flows in <5min" target. Math: 47 flows × ~10s audit (5 rule classes × ~2s each) = 470s serial; 6 workers brings that to ~80s. Under target.
- **Heartbeat refresh + lease takeover.** Each running worker refreshes `lease_expires_at = now() + 60s` every 20s on its own job. `recoverStuckVersions` sweeper (Phase 1) extends to also reap `audit_jobs` with `lease_expires_at < now() - 30s`: marks them `queued` again so another worker claims (idempotent transaction makes this safe).
- **Priority queue.** New `audit_jobs.priority INTEGER NOT NULL DEFAULT 0` column; recently-edited flows get priority 100, default exports get 50, fan-out re-audits get 10. Workers `ORDER BY priority DESC, created_at ASC LIMIT 1`. Index on `(status, priority DESC, created_at)`.
- **Rate limit on enqueue.** `audit_jobs` insert rate capped at 100/min per tenant via in-memory token bucket in `internal/projects/ratelimit.go` (extends Phase 1's per-user export limit). Fan-out enqueue chunks: enqueue 100, sleep, enqueue 100, sleep — keeps a single token publish from saturating.

### Audit fan-out trigger

- **Two trigger types.** `tokens_published` (token catalog change) and `rule_changed` (rule curation change — Phase 7 hooks here, Phase 2 supports it via internal endpoint + CLI). Both produce the same fan-out: every active flow's latest version gets a re-audit job at `priority=10`.
- **Endpoint:** `POST /v1/admin/audit/fanout` (admin-role required, gated by JWT claim) with body `{trigger: "tokens_published" | "rule_changed", reason: string, rule_id?: string, token_keys?: string[]}`. Returns `{enqueued_count, fanout_id}` immediately; SSE channel `audit.fanout_progress` carries live progress.
- **CLI:** `services/ds-service/cmd/admin/main.go fanout --trigger=tokens_published --reason="renamed colour.surface.bg → colour.surface.surface-grey-bg"` for ops use until Phase 7 admin UI.
- **Token-watcher (optional, deferred to Phase 7):** Phase 2 does NOT auto-detect token file changes. Phase 7 wires this into a CI step on `lib/tokens/indmoney/*.tokens.json` mtime change. Phase 2 reasoning: silent automatic re-audit during dev would flood inboxes; explicit DS-lead trigger is the right model.

### Sidecar migration

- **One-shot backfill (`services/ds-service/cmd/migrate-sidecars/main.go`).** Reads every `lib/audit/*.json`, derives a synthetic project per slug (`platform=web`, `product=DesignSystem`, `path=docs/<slug>`, `persona=Default`), creates one Version per sidecar, persists screens + violations into the new tables. Idempotent — re-running on already-migrated slugs no-ops.
- **Sidecar writer deprecation (U7).** `services/ds-service/internal/audit/persist.go` `WriteSidecar` is gated by `DS_AUDIT_LEGACY_SIDECARS=1` (default off in Phase 2). All audit writes go to SQLite via the new `auditPersist.Save(...)` path which uses `TenantRepo.UpsertViolations`.
- **Read path cutover (U10).** `app/files/[slug]/page.tsx` switches from `import sidecar from "@/lib/audit/<slug>.json"` to `await fetch("/v1/audit/by-slug/<slug>")`. New backend handler queries SQLite and returns the same TypeScript shape `lib/audit/types.ts` defines. Single source of truth.
- **Rollback story.** If U10 surfaces UI regressions in production, `DS_AUDIT_LEGACY_SIDECARS=1` re-enables sidecar writes; sidecars stay on disk indefinitely (gitignored after Phase 2 ships); `app/files/[slug]/page.tsx` has a feature flag `READ_FROM_SIDECAR` that flips back to the import-time read. Both flags removed in Phase 3.

### Performance budgets

| Layer | Budget | Notes |
|-------|--------|-------|
| Theme parity rule per screen | ≤200ms p95 | O(N) tree walk; no extra fetches |
| Cross-persona rule per project | ≤500ms p95 | One cross-persona compare; in-memory set diff |
| A11y contrast rule per screen | ≤300ms p95 | Per-text-node compute; ~100 text nodes typical |
| A11y touch-target rule per screen | ≤100ms p95 | Per-instance compute; ~50 instances typical |
| Flow-graph rule per flow | ≤2s p95 | Includes optional Figma prototype refetch (cached after first) |
| Component governance rule per screen | ≤200ms p95 | Override-counting per instance |
| Per-flow audit total (5 new + existing) | ≤10s p95 | All rules in series |
| Fan-out 47 flows | ≤5min p95 | 6 workers × 47 flows × 10s = 78s; budget covers Figma REST round-trips |
| Backfill 800 sidecars | ≤10min one-shot | Run during off-hours window |

---

## Open Questions

### Resolved During Planning

- **Rule expression format.** Compiled Go in Phase 2; CEL DSL deferred to Phase 7. (Reasoning: get rules right deterministically before adding DSL flexibility.)
- **Severity table location.** New `audit_rules` table seeded with default severities; per-tenant overrides Phase 7.
- **Worker pool size.** 6, env-tunable via `DS_AUDIT_WORKERS`.
- **Token-publish trigger source.** Manual via admin endpoint + CLI in Phase 2; CI-driven auto-trigger Phase 7.
- **Flow-graph data source.** Figma prototype connections via REST (`branch_data=true`); cached in `screen_prototype_links`.
- **Contrast algorithm.** WCAG 2.1 relative luminance (4.5:1 / 3:1). APCA deferred until WCAG 3.0 + org adoption.
- **Sidecar read-path migration.** Included in Phase 2 (U10), guarded by feature flag for rollback.
- **Detached instance heuristic.** `audit.NewMatcher` score ≥0.5 against any known DS component slug.

### Deferred to Implementation

- Override-sprawl threshold (8 currently chosen; tune by observation in dogfood week).
- Component-sprawl threshold (80 currently chosen; same).
- Cycle detection algorithm (Tarjan vs Kosaraju) — pick at code time per stdlib availability and test harness.
- Exact backfill batch size for the sidecar migration (start with 50/transaction, tune by RAM observation).
- Whether to compute prototype-link cache on first audit or eagerly during U1 schema migration — decide by measuring first-audit p95.
- Whether "Loading / Empty / Error" state-coverage rule operates on screen names, layer names within screens, or component instance presence — decide after looking at INDstocks V4 conventions.

### Carried Open from origin (Phase 3+)

- **Origin Q1** Decision supersession UX — Phase 5.
- **Origin Q2** Inbox triage at scale — Phase 4.
- **Origin Q3** DRD migration on flow rename — Phase 4 / 5.
- **Origin Q5** Atlas zoom strategy — Phase 3.
- **Origin Q6** Mind graph performance ceiling — Phase 6.
- **Origin Q7** Comment portability — Phase 5.
- **Origin Q8** Permission inheritance — Phase 7.
- **Origin Q10** Slack/email digest content — Phase 7.

---

## Output Structure

```
services/ds-service/
├── cmd/
│   ├── admin/                                       ← NEW
│   │   └── main.go                                  ← admin fanout CLI
│   └── migrate-sidecars/                            ← NEW (one-shot backfill)
│       └── main.go
├── internal/
│   ├── projects/
│   │   ├── rules/                                   ← NEW package
│   │   │   ├── registry.go                          ← []RuleRunner construction
│   │   │   ├── theme_parity.go
│   │   │   ├── theme_parity_test.go
│   │   │   ├── cross_persona.go
│   │   │   ├── cross_persona_test.go
│   │   │   ├── a11y_contrast.go
│   │   │   ├── a11y_contrast_test.go
│   │   │   ├── a11y_touch_target.go
│   │   │   ├── a11y_touch_target_test.go
│   │   │   ├── flow_graph.go
│   │   │   ├── flow_graph_test.go
│   │   │   ├── component_governance.go
│   │   │   ├── component_governance_test.go
│   │   │   ├── treediff.go                          ← shared structural-diff helper
│   │   │   ├── contrast.go                          ← WCAG luminance utilities
│   │   │   └── prototype.go                         ← Figma prototype fetch + cache
│   │   ├── runner.go                                ← MODIFY: register []RuleRunner
│   │   ├── worker.go                                ← MODIFY: size=6 + heartbeat refresh
│   │   ├── repository.go                            ← MODIFY: GetActiveFlows, UpsertViolations, GetPrototypeLinks
│   │   ├── server.go                                ← MODIFY: fanout endpoint
│   │   ├── fanout.go                                ← NEW: fan-out enqueue logic
│   │   └── auditrules.go                            ← NEW: rule catalog seeded from migration
│   ├── audit/
│   │   └── persist.go                               ← MODIFY: gate sidecar write behind DS_AUDIT_LEGACY_SIDECARS
│   └── auditbyslug/                                ← NEW package (read path for /files/<slug>)
│       ├── handler.go                               ← GET /v1/audit/by-slug/:slug
│       └── handler_test.go
├── migrations/
│   ├── 0002_audit_rules_and_categories.up.sql       ← NEW: audit_rules table; violations.category column;
│   │                                                 audit_jobs.priority + triggered_by columns;
│   │                                                 screen_prototype_links table; index updates
│   └── 0003_seed_audit_rules.up.sql                 ← NEW: INSERTs ~30 rule rows with default severities

lib/projects/
├── types.ts                                         ← MODIFY: Violation.category enum + AuditJob.triggered_by + Fanout types
└── client.ts                                        ← MODIFY: triggerFanout, listFanouts, fetchAuditBySlug

components/projects/tabs/
├── ViolationsTab.tsx                                ← MODIFY: category filter chips; grouped-by-category mode
└── violations/
    └── CategoryFilterChips.tsx                      ← NEW

components/projects/admin/                            ← NEW (minimal Phase 2 surface; Phase 7 expands)
└── FanoutToast.tsx                                   ← live progress toast on fanout endpoint use

app/api/audit/
└── by-slug/[slug]/route.ts                          ← NEW (Next route handler proxying to ds-service)

app/files/
└── [slug]/page.tsx                                  ← MODIFY: switch from sidecar import to fetch + READ_FROM_SIDECAR flag

tests/projects/
├── theme-parity.spec.ts                             ← NEW Playwright
├── cross-persona.spec.ts                            ← NEW
├── a11y-contrast.spec.ts                            ← NEW
├── flow-graph.spec.ts                               ← NEW
├── component-governance.spec.ts                     ← NEW
├── audit-fanout.spec.ts                             ← NEW (admin endpoint + SSE progress)
├── sidecar-migration.spec.ts                        ← NEW (read-path parity)
└── files-tab-reads-from-sqlite.spec.ts              ← NEW

docs/security/
└── data-classification.md                            ← MODIFY: add `audit_rules.expression` (Internal),
                                                       `screen_prototype_links` (Internal)
```

---

## High-Level Technical Design

> *This illustrates the intended approach and is directional guidance for review, not implementation specification.*

### Phase 2 audit fan-out flow

```mermaid
sequenceDiagram
    participant L as DS lead (CLI / future admin UI)
    participant S as ds-service /v1/admin/audit/fanout
    participant DB as SQLite
    participant Q as Audit Worker Pool (size=6)
    participant U as Subscribed Project Views
    participant F as Figma REST API

    L->>S: POST /v1/admin/audit/fanout {trigger:"tokens_published", reason:"..."}
    S->>S: Auth: requireAdmin claim
    S->>DB: SELECT active flows × latest version (TenantRepo)
    S->>DB: BEGIN; INSERT 47× audit_jobs (priority=10, triggered_by="tokens_published"); COMMIT
    S->>U: SSE broker.Publish(fanout_id, audit.fanout_started, {total: 47})
    S-->>L: 202 {fanout_id, enqueued: 47}
    Note over Q: 6 workers consume in parallel
    loop for each job
        Q->>DB: BEGIN IMMEDIATE; UPDATE audit_jobs SET status=running, leased_by, lease_expires_at; COMMIT
        Q->>F: GET /v1/files/{file}/nodes (canonical_tree refresh — cached if hash matches)
        Q->>F: GET /v1/files/{file}?branch_data=true (prototype links — cached in screen_prototype_links)
        Q->>Q: Run RuleRunner registry: ThemeParity, CrossPersona, A11y, FlowGraph, ComponentGov, AuditCore
        Q->>DB: BEGIN; DELETE violations WHERE version_id; INSERT new violations; UPDATE audit_jobs SET status=done; COMMIT
        Q->>U: SSE broker.Publish(fanout_id, audit.fanout_progress, {completed, total})
        Q->>U: SSE broker.Publish(version.tenant, project.audit_complete, {version_id})
    end
    Q->>U: SSE broker.Publish(fanout_id, audit.fanout_complete, {total, duration_ms})
```

### Rule-runner registry sketch

```
// directional pseudo-code, not implementation:

type RuleRunner interface {
    Run(ctx context.Context, version *Version, deps Deps) ([]Violation, error)
    RuleIDs() []string  // for catalog reconciliation
}

func Registry(deps Deps) []RuleRunner {
    return []RuleRunner{
        rules.NewAuditCore(deps),                  // existing audit.Audit() — Phase 1
        rules.NewThemeParity(deps),                // U2
        rules.NewCrossPersona(deps),               // U3
        rules.NewA11yContrast(deps),               // U4
        rules.NewA11yTouchTarget(deps),            // U4
        rules.NewFlowGraph(deps),                  // U5
        rules.NewComponentGovernance(deps),        // U6
    }
}

// worker.go calls runners in order, accumulating violations.
// Each runner reads from deps (Repo, Figma client, audit_rules catalog,
// canonical_tree cache) — no shared mutable state.
```

The order matters: theme parity is fastest and most foundational; flow-graph is slowest (Figma refetch) and least essential to first paint. If runtime budget is exceeded, `worker.go` short-circuits remaining runners and emits an `audit_partial` violation with `category="meta"` so the UI shows a "Phase 2 audit incomplete" notice. Only the first 6 runners get this treatment; audit core is always run.

### Theme-parity diff sketch

```
// Walk both modes' canonical_trees in lockstep. At each node:

func diff(a, b *Node, path []string) []Violation {
    if a.Type != b.Type        → critical: structural type mismatch
    if a.Name != b.Name        → high: name divergence (warn, may be intentional)
    if hashLayout(a) != hashLayout(b)  → critical: layout drift outside Variables
    if hashVisual(a, excludeBoundVars=true) != hashVisual(b, excludeBoundVars=true)
                               → critical: hand-painted property in one mode
    return walk(a.Children, b.Children, append(path, a.Name))
}

// hashLayout: x, y, width, height, padding, gap, layoutMode, primaryAxisAlign, ...
// hashVisual: fills (raw color when not bound), strokes, effects, fontSize, ...
//             excluding `boundVariables` and `explicitVariableModes` keys
```

The diff returns one violation per offending node so a designer sees the specific property that broke parity, not "this screen's modes diverge."

---

## Implementation Units

- U1. **Schema additions: rule catalog + violation category + job priority + prototype cache**

**Goal:** Migration `0002_audit_rules_and_categories.up.sql` adds the new columns and tables; `0003_seed_audit_rules.up.sql` seeds the rule catalog with default severities.

**Requirements:** R7, R9, R10

**Dependencies:** Phase 1 schema (U1 of Phase 1 plan).

**Files:**
- Create: `services/ds-service/migrations/0002_audit_rules_and_categories.up.sql`
- Create: `services/ds-service/migrations/0003_seed_audit_rules.up.sql`
- Modify: `services/ds-service/internal/db/db_test.go` (new schema verification tests)

**Approach:**
- New table `audit_rules`: `rule_id TEXT PRIMARY KEY, name TEXT, description TEXT, category TEXT, default_severity TEXT, enabled INTEGER NOT NULL DEFAULT 1, target_node_types TEXT, expression TEXT NULL, created_at INTEGER`. Org-wide (no tenant_id — global catalog; per-tenant overrides Phase 7).
- New columns on `violations`: `category TEXT NOT NULL DEFAULT 'token_drift'` (backfilled from rule_id mapping); `auto_fixable INTEGER NOT NULL DEFAULT 0`.
- New columns on `audit_jobs`: `priority INTEGER NOT NULL DEFAULT 0`; `triggered_by TEXT NOT NULL DEFAULT 'export'` (`export | rule_change | tokens_published`); `metadata TEXT NULL` (JSON). Indexes: `(status, priority DESC, created_at)` for worker dequeue.
- New table `screen_prototype_links`: `id, screen_id, source_node_id, destination_screen_id NULL, destination_node_id, trigger TEXT, action TEXT, tenant_id, created_at`. FK `screen_id → screens(id) ON DELETE CASCADE`. Indexes on `(screen_id)` and `(tenant_id, destination_screen_id)`.
- Seed migration inserts ~30 rule rows; rule_ids come from the registry (theme_parity_break, cross_persona_component_gap, a11y_contrast_aa, a11y_touch_target_44pt, flow_graph_orphan, flow_graph_dead_end, flow_graph_cycle, flow_graph_missing_state_coverage, component_detached, component_override_sprawl, component_set_sprawl, plus existing audit core rule_ids re-asserted with categories).
- Forward-only column-add policy from Phase 1 honored throughout.

**Patterns to follow:**
- `services/ds-service/migrations/0001_projects_schema.up.sql` (Phase 1 numbered migration shape).
- `internal/db/migrations.go` (Phase 1 migration runner; no changes needed).

**Test scenarios:**
- *Happy path:* Migration applies on a fresh DB; `audit_rules` count == seed count; new columns exist with correct defaults.
- *Idempotency:* Re-running migration produces no errors; `schema_migrations` shows it applied once.
- *Backfill correctness:* Existing Phase 1 violations get `category` populated based on rule_id (e.g., `surface.bg.drift` → `token_drift`); no NULLs after migration.
- *FK integrity:* `screen_prototype_links` cascades on screen deletion; orphaned links produce zero rows after a cascade test.

**Verification:**
- `go test ./internal/db/...` passes including new schema tests.
- Sample query `SELECT category, COUNT(*) FROM violations GROUP BY category` returns non-zero rows for every existing rule_id mapped.

---

- U2. **Theme parity rule**

**Goal:** Implement `rules.ThemeParity` — for each `screen_modes` row pair within a `screens` group, compute structural diff between canonical_trees outside `explicitVariableModes` / `boundVariables`. Emit Critical violations.

**Requirements:** R7 (theme parity class), AE-2.

**Dependencies:** U1.

**Files:**
- Create: `services/ds-service/internal/projects/rules/theme_parity.go`
- Create: `services/ds-service/internal/projects/rules/theme_parity_test.go`
- Create: `services/ds-service/internal/projects/rules/treediff.go` (shared helper — also used by component-governance for instance comparison)
- Create: `services/ds-service/internal/projects/rules/treediff_test.go`

**Approach:**
- `treediff.go` exports `Diff(a, b *Node, opts DiffOpts) []Delta` — walks both trees in parallel, returns list of structural differences with path. `DiffOpts.IgnoreKeys` (`boundVariables`, `explicitVariableModes`, `componentPropertyReferences`) controls what's ignored.
- `theme_parity.go` reads each screen's canonical_tree per mode (via `repo.GetCanonicalTree`); for each mode pair, calls `treediff.Diff` with `IgnoreKeys` set; converts each Delta to a Violation with `RuleID="theme_parity_break"`, `category="theme_parity"`, severity from `audit_rules.default_severity` (default Critical).
- Skips screens with only one mode (`modes.length == 1`).
- Three-mode screens (light/dark/sepia): pair-wise compare, emit per-pair findings.

**Execution note:** Test-first. Write the failing tests with synthetic canonical_trees that exhibit each delta class, then implement the diff. Why: tree-diff correctness is load-bearing for theme parity's headline value (AE-2); easier to enumerate cases up front.

**Patterns to follow:**
- `services/ds-service/internal/projects/runner.go` `auditCoreRunner` shape — same `RuleRunner` interface.
- `services/ds-service/internal/audit/engine.go` for Node walking conventions.

**Test scenarios:**
- *Happy path: matching modes →* both modes produce identical canonical_trees outside `boundVariables`. Diff returns zero deltas. No violations emitted.
- *Happy path: bound-only divergence →* light has `fills: { boundVariables: { color: "var.surface.bg" }}`, dark has `fills: { boundVariables: { color: "var.surface.bg" }}` (same Variable, different resolved). Zero deltas (boundVariables ignored). No violations.
- *Edge case: single mode →* a screen with only "default" mode emits zero violations.
- *Edge case: three-mode pair-wise →* light + dark + sepia, each with one unique drift → emits N(N-1)/2 = 3 violations, one per pair.
- *Error path: hand-painted dark →* dark mode replaces `boundVariables.fills` with raw `{r:0.42,g:0.45,b:0.5,a:1}`. Critical violation: `RuleID="theme_parity_break"`, `property="fill"`, `observed="raw color rgb(107,115,128)"`, `suggestion` references the unbound state. Covers AE-2.
- *Error path: structural type mismatch →* light has `RECTANGLE` at path `Frame/Card/0`, dark has `ELLIPSE`. Critical violation with `property="type"`.
- *Error path: layout drift →* dark frame's `paddingLeft` is 12 vs light's 16 (no Variable bound). Critical violation `property="padding"`.
- *Integration:* end-to-end through worker — synthetic version, audit job runs, theme_parity violations land in `violations` table with correct `category`.

**Verification:**
- All test scenarios pass.
- Worker can run theme parity on the dogfood `INDstocks V4` flow without panics or timeouts.

---

- U3. **Cross-persona consistency rule**

**Goal:** Implement `rules.CrossPersona` — for each project's flows grouped by `(project_id, path_excluding_persona)`, set-diff component instances across personas; emit High violations for missing components per persona.

**Requirements:** R7 (cross-persona class), AE-3.

**Dependencies:** U1.

**Files:**
- Create: `services/ds-service/internal/projects/rules/cross_persona.go`
- Create: `services/ds-service/internal/projects/rules/cross_persona_test.go`

**Approach:**
- Reads all flows for the project at the active version. Groups by canonical path. Within each group, builds `Set<componentSetKey>` per persona (sourced from canonical_trees; if a tree contains an INSTANCE node with `mainComponent.componentSetKey == X`, X joins the set).
- Pair-wise compares across personas. For each missing component in persona B that exists in persona A: emit High violation on persona B's flow with `RuleID="cross_persona_component_gap"`, `category="cross_persona"`, `observed="<ComponentName> missing"`, `suggestion="Add <ComponentName> to <PersonaName> or Acknowledge"`.
- Skips solo personas (no peer).

**Patterns to follow:**
- `services/ds-service/internal/projects/runner.go` for the `RuleRunner` interface contract.

**Test scenarios:**
- *Happy path: identical persona sets →* two personas, identical component coverage. Zero violations.
- *Happy path: solo persona →* one flow with one persona for path P. Zero violations (no comparison).
- *Error path: missing Toast in Logged-out →* Default has Toast, Logged-out doesn't. One High violation on Logged-out flow. Covers AE-3.
- *Edge case: three personas with chained gaps →* Default has [A,B,C], Logged-out has [A,B], KYC-pending has [A]. Pair-wise emits: Logged-out missing C; KYC-pending missing B; KYC-pending missing C. Three violations.
- *Integration:* full run-through via worker; confirms violations are persisted with correct persona_id reference.

**Verification:** All test scenarios pass; sample dogfood project confirms no false-positives (manually reviewed).

---

- U4. **WCAG AA accessibility rules (contrast + touch target)**

**Goal:** Implement `rules.A11yContrast` and `rules.A11yTouchTarget` per WCAG 2.1 AA thresholds.

**Requirements:** R7 (a11y class).

**Dependencies:** U1.

**Files:**
- Create: `services/ds-service/internal/projects/rules/a11y_contrast.go`
- Create: `services/ds-service/internal/projects/rules/a11y_contrast_test.go`
- Create: `services/ds-service/internal/projects/rules/a11y_touch_target.go`
- Create: `services/ds-service/internal/projects/rules/a11y_touch_target_test.go`
- Create: `services/ds-service/internal/projects/rules/contrast.go` (relative-luminance utilities)
- Create: `services/ds-service/internal/projects/rules/contrast_test.go`

**Approach:**
- `contrast.go` — pure utilities: `RelativeLuminance(rgb)` (sRGB linearization + WCAG luma weighting), `ContrastRatio(fg, bg)` returning `(L1+0.05)/(L2+0.05)`. Test against WCAG-published reference pairs (white-on-black = 21:1, black-on-white = 21:1, #767676 on white = 4.54:1).
- `a11y_contrast.go` — for each TEXT node in canonical_tree:
  - Resolve foreground: text fill against active mode's Variable resolution (use `lib/projects/resolveTreeForMode` Go-side mirror — Phase 1 has the TS implementation; mirror in Go for backend rules in this unit).
  - Resolve background: walk parent chain to find first opaque fill; default white.
  - Compute ratio; threshold 4.5 (or 3.0 for `fontSize ≥ 18 || (fontSize ≥ 14 && fontWeight ≥ 700)`).
  - Below threshold → High violation. `observed="ratio 3.2:1 (fg #6B7280 on bg #FFFFFF)"`, `suggestion="Foreground needs darker token to meet 4.5:1"`.
- `a11y_touch_target.go` — for each INSTANCE matching a clickable atom (buttons, icon buttons, links — manifest list at `public/icons/glyph/manifest.json` with `kind: "atom"` and tag containing "interactive" — a small allowlist):
  - Assert `width >= 44 && height >= 44`. Below threshold → High violation.
- Both rules run per `screen_modes` row.
- Edge cases (transparent / gradient / image backgrounds) handled per Key Tech Decisions.

**Patterns to follow:**
- `services/ds-service/internal/audit/engine.go` Node walking.
- `lib/projects/resolveTreeForMode.ts` for the per-mode resolution semantics.

**Test scenarios:**
- *Happy path: white on black →* contrast 21:1, no violation.
- *Happy path: 14pt regular dark grey on white →* `#595959` on white = 7:1, no violation.
- *Edge case: transparent background falls back to white →* text has parent with `fillStyleId=null`; rule walks to root. Computes against white.
- *Edge case: gradient background uses worst-case stop →* text on a `[#222 → #FFF]` gradient checks against #FFF for dark text. Mid-gradient assertion.
- *Edge case: image background →* emits Info `a11y_unverifiable` rather than High.
- *Error path: low contrast normal text →* `#A0A0A0` on white = 2.84:1. High violation with `RuleID="a11y_contrast_aa"`, `observed="2.84:1 below 4.5:1"`.
- *Error path: low contrast large text →* `#A0A0A0` on white at fontSize 24 = 2.84:1 still below 3.0 threshold. Still violates.
- *Touch target happy path:* button 44×44, no violation.
- *Touch target error:* button 36×36 → High `a11y_touch_target_44pt`, `observed="36×36 below 44×44"`.
- *Per-mode:* same screen passes contrast in light, fails in dark (because light surface bg vs dark text contrast). Rule emits violation with `mode_label="dark"`.
- *Integration:* worker runs both rules; violations persist with `category="a11y_contrast"` / `"a11y_touch_target"`.

**Verification:** All test scenarios pass. Spot-check three real INDstocks screens manually against the contrast tool output.

---

- U5. **Flow-graph rule + Figma prototype connection cache**

**Goal:** Implement `rules.FlowGraph` — fetch Figma prototype connections, persist in `screen_prototype_links`, run dead-end / orphan / cycle / missing-state-coverage checks.

**Requirements:** R7 (flow-graph class).

**Dependencies:** U1.

**Files:**
- Create: `services/ds-service/internal/projects/rules/flow_graph.go`
- Create: `services/ds-service/internal/projects/rules/flow_graph_test.go`
- Create: `services/ds-service/internal/projects/rules/prototype.go` (Figma prototype fetch + cache)
- Create: `services/ds-service/internal/projects/rules/prototype_test.go`
- Modify: `services/ds-service/internal/projects/repository.go` — add `GetPrototypeLinks(versionID)`, `UpsertPrototypeLinks([]Link)`.

**Approach:**
- `prototype.go` — fetches `GET /v1/files/{file_id}?branch_data=true&geometry=paths&depth=3` once per audit run; parses `prototypeStartNodeID` and per-node `transitions[]`; persists into `screen_prototype_links`. Cache validity: link rows tied to `version_id`; on re-audit of same version they're reused.
- `flow_graph.go`:
  - Build adjacency list from `screen_prototype_links`.
  - **Orphan**: BFS from `prototypeStartNodeID`; screens unreached AND not the start = orphan. Emit Medium per orphan.
  - **Dead-end**: out-degree zero AND name doesn't match `^(Success|Confirmation|Done|Thank You|Error)$` (case-insensitive) → Medium.
  - **Cycle without exit**: Tarjan SCC; for each SCC of size ≥2, check if any node has an out-edge leaving the SCC. None → High.
  - **Missing state coverage**: scan flow's screens for any with name matching `Loading|Skeleton` OR containing a `LoadingState` instance → if found AND no peer matching `Empty|EmptyState|Error|ErrorState` → Low.
- Sparse-prototype fallback: if `link_count / screen_count < 0.5`, skip orphan/dead-end/cycle (would produce noise); only run missing-state-coverage. Emit Info `flow_graph_skipped` so UI can hint.

**Patterns to follow:**
- `services/ds-service/cmd/icons/main.go` for Figma REST client conventions.
- `services/ds-service/internal/projects/pipeline.go` for fetch + retry patterns.

**Test scenarios:**
- *Happy path: linear flow →* 5 screens, 4 prototype links forming a chain. Zero violations.
- *Happy path: branching flow with named terminus →* "Success" screen at end of one branch — no dead-end violation despite zero out-edges.
- *Error path: orphan screen →* 6 screens, 1 unreachable. Medium `flow_graph_orphan`.
- *Error path: dead-end →* screen named "Tax Form" with zero out-edges (not in named-terminus allowlist). Medium.
- *Error path: cycle without exit →* A → B → A loop, no edges out. High `flow_graph_cycle`.
- *Error path: missing state coverage →* flow has Loading screen, no Empty or Error. Low `flow_graph_missing_state_coverage`.
- *Edge case: sparse prototype data →* 5 screens, 1 link → skip dead-end/orphan/cycle, run missing-state-coverage only. One Info `flow_graph_skipped`.
- *Integration:* full run on a synthetic flow with Figma fixture; cache hit on second audit (no Figma re-fetch).

**Verification:** All scenarios pass. Manual spot-check on a known dogfood flow with prototype links — confirm orphan detection lands.

---

- U6. **Component governance rules**

**Goal:** Implement `rules.ComponentGovernance` — detached instances, override sprawl, component sprawl.

**Requirements:** R7 (component governance class).

**Dependencies:** U1, U2 (uses `treediff.go` for instance comparison).

**Files:**
- Create: `services/ds-service/internal/projects/rules/component_governance.go`
- Create: `services/ds-service/internal/projects/rules/component_governance_test.go`

**Approach:**
- **Detached instance:** for each INSTANCE in canonical_tree with null `componentId` (or `mainComponent.id`), compute `audit.NewMatcher` score against all known DS slugs in `public/icons/glyph/manifest.json`. If max score ≥ 0.5, flag with `RuleID="component_detached"`, suggestion includes the matched slug ("Likely meant to be `<Slug>` — convert to instance"), severity Medium.
- **Override sprawl:** count `componentProperties` overrides + `boundVariables` overrides + direct visual prop overrides per INSTANCE. If `≥ 8`, flag with `RuleID="component_override_sprawl"`, severity Low. Suggestion: "Heavy overrides — consider extracting a new component variant."
- **Component sprawl:** per-flow count of distinct `componentSetKey` values; if `≥ 80`, flag with `RuleID="component_set_sprawl"`, severity Info. One per flow.

**Patterns to follow:**
- `services/ds-service/internal/audit/engine.go` `NewMatcher` for component-similarity scoring.
- `services/ds-service/internal/projects/rules/treediff.go` (U2) for tree walking.

**Test scenarios:**
- *Happy path: clean instances →* all INSTANCEs have valid `componentId`; zero violations.
- *Error path: detached lookalike →* a RECTANGLE named "Button" with a single TEXT child — matcher score 0.6. Medium `component_detached`.
- *Error path: heavy override →* INSTANCE with 9 overrides. Low `component_override_sprawl`.
- *Error path: sprawling flow →* flow uses 84 distinct components. One Info `component_set_sprawl` per flow.
- *Edge case: 7 overrides →* below threshold; no violation.
- *Edge case: matcher score 0.45 →* below threshold; no violation. (Avoids false positives on truly novel custom shapes.)
- *Integration:* worker runs governance and cross-persona in same audit; both produce expected results without interference.

**Verification:** All scenarios pass.

---

- U7. **Worker pool scaling + heartbeat refresh + priority queue**

**Goal:** Grow `WorkerPool{ size: 1 }` to 6 (env-tunable). Add heartbeat refresh on running jobs. Switch dequeue ordering to priority-based.

**Requirements:** R10 (audit fan-out worker scaling).

**Dependencies:** U1 (priority column on audit_jobs).

**Files:**
- Modify: `services/ds-service/internal/projects/worker.go` — `size` from constant to env-driven; heartbeat refresh goroutine per running job.
- Modify: `services/ds-service/internal/projects/worker_test.go` — concurrency tests.
- Modify: `services/ds-service/internal/projects/recovery.go` — sweeper now also reaps `audit_jobs` with stale leases.

**Approach:**
- Env var `DS_AUDIT_WORKERS` parsed at boot; default 6. Min 1, max 32.
- Each running worker spawns a heartbeat goroutine: `for range time.Tick(20 * time.Second) { UPDATE audit_jobs SET lease_expires_at = now() + 60s WHERE id = ?, leased_by = ? }`. Stops when job completes.
- Sweeper extension: `recoverStuckVersions` already runs every 60s; add a sibling that runs `UPDATE audit_jobs SET status='queued', leased_by=NULL, lease_expires_at=NULL WHERE status='running' AND lease_expires_at < (now() - 30s)`. Reclaimed jobs re-enter the queue.
- Dequeue query: `SELECT ... FROM audit_jobs WHERE status='queued' ORDER BY priority DESC, created_at ASC LIMIT 1`. Atomic claim via `BEGIN IMMEDIATE; UPDATE ... WHERE id = ? AND status='queued'; COMMIT` checks `RowsAffected == 1`.
- Channel-notification (existing) unchanged — workers wake on insert; priority comparison happens at dequeue time.

**Patterns to follow:**
- Phase 1's existing `worker.go` claim transaction.
- Phase 1's `recovery.go` sweeper for the lease-takeover sibling.

**Test scenarios:**
- *Happy path: 6 workers consume 47 jobs in parallel →* total runtime under 5min in test environment with synthetic 1s rule runs.
- *Happy path: priority ordering →* enqueue 3 jobs at priorities 10, 100, 50; first to run is priority 100, then 50, then 10.
- *Edge case: heartbeat keeps lease alive →* running job lasting 90s never gets reclaimed.
- *Error path: worker crash mid-job →* simulate by killing heartbeat after 30s; sweeper marks job queued; another worker reclaims; result is one violation set (not duplicated).
- *Edge case: lease takeover is idempotent →* DELETE-then-INSERT transaction means the second worker's run produces same row count, no orphans.
- *Integration:* fan-out 47 jobs, observe SSE events, assert all jobs hit `status=done` exactly once each.

**Verification:** All scenarios pass; load test (47-flow synthetic project) completes inside 5min p95.

---

- U8. **Audit fan-out trigger endpoint + admin CLI + sidecar-writer deprecation flag**

**Goal:** Ship `POST /v1/admin/audit/fanout` endpoint and `services/ds-service/cmd/admin` CLI. Deprecate sidecar writes (gated behind env flag).

**Requirements:** R10, AE-7.

**Dependencies:** U1, U7.

**Files:**
- Create: `services/ds-service/internal/projects/fanout.go`
- Create: `services/ds-service/internal/projects/fanout_test.go`
- Modify: `services/ds-service/internal/projects/server.go` — register `HandleAdminFanout` behind admin-role guard.
- Modify: `services/ds-service/cmd/server/main.go` — wire route.
- Create: `services/ds-service/cmd/admin/main.go` — CLI subcommands: `fanout`, `migrate-sidecars`.
- Modify: `services/ds-service/internal/audit/persist.go` — `WriteSidecar` early-returns when `os.Getenv("DS_AUDIT_LEGACY_SIDECARS") != "1"`.
- Modify: `services/ds-service/internal/projects/auditrules.go` — new file, holds the rule catalog reader.

**Approach:**
- Endpoint: admin-only (JWT claim `role=admin` or `tenant_admin`); body `{trigger, reason, rule_id?, token_keys?}`; returns `202 {fanout_id, enqueued, eta_seconds}`.
- Fan-out logic:
  1. `repo.ListActiveFlowsLatestVersions()` returns `(flow_id, version_id, tenant_id)` triples — one row per active flow's latest version, scoped per tenant unless triggered globally.
  2. Insert `audit_jobs` rows in batches of 100 inside a transaction; `priority=10`, `triggered_by=trigger`, `metadata` includes `fanout_id`.
  3. Throttle: 100 jobs per batch, `time.Sleep(500ms)` between batches → max 200 jobs/sec. Inside per-tenant token bucket too (100/min).
  4. SSE `audit.fanout_started` immediately; per-job-completion (caught from existing `audit_complete` events filtered by `metadata.fanout_id`) → `audit.fanout_progress`; final `audit.fanout_complete` when all jobs done (or timeout 10min).
- CLI:
  - `admin fanout --trigger=tokens_published --reason="..."` — calls the endpoint via authed JWT loaded from `~/.ds-service/admin.token`.
  - `admin fanout --trigger=rule_changed --rule-id=theme_parity_break --reason="..."` — same.
  - `admin migrate-sidecars` — calls U9's backfill in-process.
- Sidecar deprecation flag: `WriteSidecar` early-returns no-op when `DS_AUDIT_LEGACY_SIDECARS != "1"`. Existing audit core continues to populate SQLite via the runner (existing path through `auditCoreRunner`).

**Patterns to follow:**
- `services/ds-service/internal/projects/server.go` for route registration + admin guard.
- `services/ds-service/internal/projects/ratelimit.go` for token bucket.

**Test scenarios:**
- *Happy path: token publish fan-out →* 47 active flows; endpoint returns 202 with `enqueued=47`; SSE channel receives started, 47 progress events, 1 complete. All jobs `status=done`. Covers AE-7.
- *Happy path: rule-change fan-out →* same shape; `triggered_by=rule_change`.
- *Error path: non-admin user →* 403.
- *Edge case: zero active flows →* returns 200 with `enqueued=0`; no SSE events.
- *Edge case: rate-limit hit mid-batch →* triggers throttle; total wall-time grows but all jobs eventually enqueue.
- *Idempotent rerun:* same fanout triggered twice in 60s — second call doesn't double-enqueue (idempotency check on `fanout_id` derived from trigger + reason hash).
- *CLI:* `admin fanout` invocation reaches endpoint, prints fanout_id, exits 0 on success.
- *Sidecar flag:* with flag unset, audit run produces zero `lib/audit/*.json` writes; with flag=1, sidecar produced as before.

**Verification:** All scenarios pass.

---

- U9. **Sidecar backfill: lib/audit/*.json → SQLite**

**Goal:** One-shot ingestion of existing JSON sidecars into `screens` + `violations` for query parity with new pipeline.

**Requirements:** Operational continuity (Phase 2 stops sidecar writes; reads need a single source).

**Dependencies:** U1, U8.

**Files:**
- Create: `services/ds-service/cmd/migrate-sidecars/main.go` (or as subcommand of `cmd/admin/main.go fanout`)
- Modify: `services/ds-service/internal/projects/repository.go` — add `BackfillSyntheticProject(slug)`, `BackfillViolations([]Violation)`.
- Create: `services/ds-service/internal/projects/backfill_test.go`

**Approach:**
- For each `lib/audit/*.json` file:
  - Parse with the existing `lib/audit/types.ts` Go-side mirror at `internal/audit/types.go`.
  - Synthesize project (`slug=<file-slug>`, `platform=web`, `product=DesignSystem`, `path=docs/<slug>`, `tenant_id=<system-tenant>`, `owner_user_id=<system-user>`).
  - One Version per sidecar (idempotent on slug — re-runs no-op).
  - One screen per `AuditScreen`; canonical_tree filled from sidecar's tree-data fields where present (or marked `null` with `metadata.source="sidecar-backfill"` for sidecars without trees).
  - Violations from `FixCandidates` mapped via `MapPriorityToSeverity` (existing).
- Idempotency: `BackfillSyntheticProject` uses `INSERT ... ON CONFLICT(tenant_id, slug) DO NOTHING RETURNING id`. Sidecar mtime stored on the project row; re-runs only re-ingest sidecars newer than last backfill.
- Run as a one-shot: `go run ./cmd/admin migrate-sidecars`. Logs progress every 50 files.

**Patterns to follow:**
- `services/ds-service/cmd/icons/main.go` for the CLI shape.

**Test scenarios:**
- *Happy path:* run on 5 fixture sidecars → 5 synthetic projects, N violations total. Manifest matches.
- *Idempotency:* second run produces zero new projects, zero new violations.
- *Updated sidecar:* one fixture sidecar mtime bumped → its project's version refreshed (new `Version`); old version preserved.
- *Edge case: malformed sidecar →* parse error logged, file skipped, exit code non-zero. Other files still processed.
- *Edge case: empty sidecar →* zero violations ingested; project created (so the read path doesn't 404 the slug).
- *Performance:* 800 sidecars complete inside 10min on dev hardware.

**Verification:** Backfill runs end-to-end; `SELECT COUNT(*) FROM projects WHERE platform='web' AND product='DesignSystem'` matches sidecar count; spot-check 3 slugs render identically through new vs old read path.

---

- U10. **Files-tab read-path migration: /files/[slug] reads from SQLite**

**Goal:** `app/files/[slug]/page.tsx` switches from build-time JSON sidecar import to runtime SQLite query via new `GET /v1/audit/by-slug/:slug` route. Rollback flag preserves old path for one release.

**Requirements:** Operational — single source of truth post Phase 2.

**Dependencies:** U9.

**Files:**
- Create: `services/ds-service/internal/auditbyslug/handler.go`
- Create: `services/ds-service/internal/auditbyslug/handler_test.go`
- Modify: `services/ds-service/cmd/server/main.go` — register route.
- Create: `app/api/audit/by-slug/[slug]/route.ts` (Next route handler proxying to ds-service)
- Modify: `app/files/[slug]/page.tsx` — fetch + flag.
- Modify: `lib/auth-client.ts` (if needed for proxy auth).

**Approach:**
- New handler: `GET /v1/audit/by-slug/:slug` returns the same JSON shape that `lib/audit/types.ts` defines (so the frontend doesn't change types). Reads from `screens` + `violations` joined back into `AuditOutput` shape via a query helper.
- Next route handler proxies (with auth) to ds-service, attaches X-Trace-ID header.
- `app/files/[slug]/page.tsx`:
  - New env-driven flag `READ_FROM_SIDECAR=1` (default unset). When set, falls back to `import sidecar from "@/lib/audit/<slug>.json"` (current behavior). When unset, fetches from new route.
  - This preserves a one-release rollback window.
- After one release of stable Phase 2 behavior in production, the flag is removed (Phase 3 cleanup unit).

**Patterns to follow:**
- Existing `app/api/sync/route.ts` for Next route handlers proxying to ds-service.
- `app/files/[slug]/page.tsx` for the data-loading pattern (currently build-time; becomes runtime).

**Test scenarios:**
- *Happy path:* slug X exists in SQLite; `/files/X` renders identical content to pre-migration. Manual visual diff on 5 slugs.
- *Edge case: slug not in SQLite →* 404 (matches current behavior for non-existent JSON).
- *Edge case: SQLite read failure →* page shows server-error state (rather than crashing). Error logged with trace_id.
- *Rollback path:* `READ_FROM_SIDECAR=1` reverts to import-time read; same content as pre-migration.
- *Integration: Playwright →* `tests/projects/files-tab-reads-from-sqlite.spec.ts` walks 5 slugs, asserts identical FixCandidate counts.

**Verification:** Production-equivalent staging shows identical /files/<slug> output for all migrated slugs; rollback flag works; Playwright passes.

---

- U11. **Frontend: ViolationsTab category filter chips + new severity-arrival animation**

**Goal:** Surface the new rule categories in the Violations tab as filter chips. Wire SSE-driven live arrival animation per Animation Philosophy.

**Requirements:** R14 extension.

**Dependencies:** U2-U6 backend rules; U1 schema.

**Files:**
- Modify: `lib/projects/types.ts` — `Violation.category` enum union; `AuditJob.triggered_by`.
- Modify: `components/projects/tabs/ViolationsTab.tsx` — category filter chips, grouped-by-category mode toggle.
- Create: `components/projects/tabs/violations/CategoryFilterChips.tsx`
- Modify: `components/projects/tabs/violations/RowAnimations.tsx` (extend if exists, else inline) — new-arrival flash on SSE update.
- Modify: `lib/projects/client.ts` — extend `listViolations` to accept `category` filter.

**Approach:**
- Category chips render above the existing severity-grouped list. Multi-select; chip click toggles inclusion. Toggling triggers re-fetch with `?category=X,Y`. Underline morph via Framer Motion `layoutId`.
- "Group by category" toggle (alternative to current group-by-severity); persists in URL search param.
- New-arrival animation: when SSE delivers `project.audit_complete` for current version, list re-fetches; new violation rows animate with a brief background flash (`opacity 0.6 → 0` over 600ms).
- Filter UX: deselecting all chips shows everything (default).

**Patterns to follow:**
- Phase 1's `ViolationsTab.tsx` structure (severity grouping, lifecycle button placement).
- `useGSAPContext` from Phase 1 animation library.

**Test scenarios:**
- *Happy path:* 5 categories present in dataset; chips render with counts; clicking one filters; deselecting all restores full list.
- *Happy path: severity grouping unchanged →* chip state doesn't break existing severity sort.
- *Edge case: zero categories →* chips hidden (regression: existing behavior preserved).
- *Animation:* on SSE-driven re-fetch, only newly-added rows flash; existing rows don't.
- *Reduced motion:* `prefers-reduced-motion: reduce` honored — flash short-circuits.
- *Playwright:* `tests/projects/category-filter-chips.spec.ts` — chip toggle, URL state, severity-grouping coexistence.

**Verification:** All scenarios pass; manual review of dogfood project's filter UX feels coherent.

---

- U12. **Playwright integration tests for fan-out + sidecar parity**

**Goal:** End-to-end Playwright coverage of the new fan-out path + sidecar-to-SQLite parity assertions.

**Requirements:** R10, AE-7; sidecar migration safety.

**Dependencies:** U1-U11.

**Files:**
- Create: `tests/projects/audit-fanout.spec.ts`
- Create: `tests/projects/sidecar-migration.spec.ts`
- Create: `tests/projects/files-tab-reads-from-sqlite.spec.ts`
- Modify: `tests/projects/fixtures/project-fixtures.ts` — multi-tenant fan-out fixtures.

**Approach:**
- `audit-fanout.spec.ts`:
  - Setup: 5 projects with 5 active flows × 2 personas × 2 modes = 100 screens of test data.
  - Hit `POST /v1/admin/audit/fanout` with admin token.
  - Subscribe to SSE; assert `started` arrives within 1s, all 25 `progress` events, `complete` within 60s.
  - Assert `audit_jobs` rows: 25 rows with `triggered_by=tokens_published`, all `status=done`.
- `sidecar-migration.spec.ts`:
  - Run `go run ./cmd/admin migrate-sidecars` against test fixtures (5 sidecars).
  - Assert `projects` table has 5 synthetic rows.
  - Assert `violations` count matches sum of FixCandidates across sidecars.
  - Re-run; assert no new rows (idempotency).
- `files-tab-reads-from-sqlite.spec.ts`:
  - For 3 known slugs, navigate to `/files/<slug>`, screenshot the violations area; compare against snapshot.
  - Run twice — once with `READ_FROM_SIDECAR=1`, once without; assert visual parity within tolerance.

**Patterns to follow:**
- Phase 1's `tests/projects/canvas-render.spec.ts` for Playwright structure.
- Phase 1's `tests/projects/fixtures/project-fixtures.ts` for fixture composition.

**Test scenarios:** all listed above.

**Verification:** Full `npx playwright test tests/projects/` passes including new specs.

---

## System-Wide Impact

- **Interaction graph:**
  - DS lead → admin endpoint / CLI (`POST /v1/admin/audit/fanout`).
  - Admin endpoint → repo (enqueue audit_jobs) → worker pool (consume) → repo (write violations) → SSE broker (publish progress).
  - Frontend Project view → SSE → re-fetch violations on `project.audit_complete`.
  - `/files/[slug]` route → new `GET /v1/audit/by-slug/:slug` → repo (read SQLite, no sidecar).
  - Rule registry → audit_rules table for default severity lookup at runtime.
- **Error propagation:**
  - Rule-class panic → caught by worker, recorded as `audit_jobs.error`, single rule's violations skipped, others continue.
  - Sidecar-write disabled but flag misconfigured → audit run still succeeds; backend logs warning.
  - Fan-out partial failure (some jobs fail) → SSE `audit.fanout_complete` includes `failed_count`; admin sees in CLI output.
- **State lifecycle risks:**
  - Worker crash mid-fan-out → lease takeover reclaims; idempotent transaction means no double-counted violations.
  - Sidecar-write flag flipped mid-run → audit run completes with current-flag value; no inconsistency.
  - Backfill on top of already-migrated sidecar → idempotency guards prevent dupes.
- **API surface parity:**
  - `internal/projects/types.ProjectsSchemaVersion` bumps `1.0` → `1.1` (additive: new `category` enum on Violation, `triggered_by` on AuditJob). Existing clients tolerate unknown fields.
  - `lib/audit/types.ts` unchanged — `/files/[slug]` reads return identical TS shape via the new endpoint.
  - Plugin `MessageFromUI` / `MessageToUI` unchanged (Phase 2 doesn't touch the plugin).
- **Integration coverage:**
  - Cross-layer: admin CLI → endpoint → enqueue → workers (size 6) → SSE → frontend live update. Single Playwright test `audit-fanout.spec.ts`.
  - Cross-version: backfill + new audit on same slug doesn't conflict (idempotency).
  - Cross-tenant: fan-out scoped per tenant by default; cross-tenant test in `tests/projects/tenant-isolation.spec.ts` (added in Phase 1) extends to the fanout endpoint.
- **Unchanged invariants:**
  - Phase 1 `RuleRunner` interface is the plug-in slot — not modified.
  - Phase 1 worker `BEGIN IMMEDIATE; DELETE; INSERT; COMMIT` transaction shape preserved.
  - `internal/audit/types.SchemaVersion = "1.0"` stays.
  - All Phase 1 routes unchanged.
  - Existing audit core's color/spacing/radius/component rules ship Phase 2 unchanged in behavior — new categories just classify their findings.

---

## Risks & Dependencies

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| Theme-parity tree-diff false positives (e.g., legitimate auto-layout differences across modes) | High | Medium | Treat first dogfood week as calibration; expose `audit_rules.enabled=0` as kill switch per rule. Phase 7 adds per-tenant override. |
| WCAG contrast computation off-by-rounding (various contrast tools differ in 4th decimal) | Medium | Low | Test vectors against W3C-published reference pairs. Document our luminance algorithm in `docs/security/data-classification.md` (already touches a11y context). |
| Figma prototype API rate limits when fan-out triggers re-fetch on 47 flows | Medium | High | Cache prototype links in `screen_prototype_links`; per-version invalidation only. Token bucket on Figma client (existing) caps simultaneous calls. |
| Worker pool of 6 still insufficient for org-wide fan-out as user count grows | Low (Phase 2) | Medium (later) | Env var allows scaling without code change. Phase 4 revisits if dashboards show queue lag. |
| Sidecar backfill creates synthetic projects that pollute search / mind graph | Medium | Low | `projects.metadata->source = "sidecar-backfill"` filter on those surfaces; mind graph and search exclude them by default in Phase 6 / Phase 8. |
| Read-path cutover breaks existing /files/[slug] consumers in production | Low | Critical | Feature flag `READ_FROM_SIDECAR=1` for one-release rollback; staging walk-through before flag-default flip; metrics on response times pre/post. |
| Rule registry order producing inconsistent per-tenant audit times if one rule dominates | Low | Low | Per-rule p95 tracking via metrics histogram; rebalance order in Phase 3 if needed. |
| Sidecar deprecation flag breaks dev workflows that read /files locally | Low | Low | Flag default OFF in Phase 2; documentation update in same PR; one-release window for teams to migrate. |
| Heartbeat goroutine leak if worker panics before goroutine exit | Medium | Low | `defer cancel()` on worker context; goroutine listens to ctx; tested in `worker_test.go` panic recovery scenario. |
| Audit_rules seed migration drift between code registry and DB rows | Medium | Medium | Boot-time reconciliation — `auditrules.go` compares registry to DB; logs warning on mismatch; never silently inserts. Phase 7 takes over true source-of-truth via DS lead curation. |
| Tarjan SCC implementation bug → false-positive cycle violations | Medium | Medium | Test against synthetic adjacency lists (chain, branch, cycle, multi-cycle, disconnected). Use stdlib `container/list` if available rather than rolling our own. |

---

## Documentation / Operational Notes

### Operational changes

- **New env vars:** `DS_AUDIT_WORKERS` (default 6), `DS_AUDIT_LEGACY_SIDECARS` (default unset), `READ_FROM_SIDECAR` (default unset). Document in `services/ds-service/README.md`.
- **New CLI commands:** `services/ds-service/cmd/admin/main.go` with `fanout` and `migrate-sidecars` subcommands. Document in `docs/runbooks/admin-cli.md` (new file).
- **Backfill runbook:** Step-by-step `migrate-sidecars` invocation, expected duration (≤10min), rollback approach. New file `docs/runbooks/2026-04-30-phase-2-backfill.md`.
- **SSE channels added:** `audit.fanout_started`, `audit.fanout_progress`, `audit.fanout_complete`. Document in `docs/api/sse-events.md`.

### Documentation

- `docs/security/data-classification.md` — add `audit_rules.expression` (Internal — may contain CEL referencing component names later), `screen_prototype_links` (Internal).
- `services/ds-service/README.md` — env var section, CLI section.
- New `docs/runbooks/` directory and 2 entries (admin CLI + backfill).
- Once Phase 2 ships, capture a `docs/solutions/2026-MM-DD-NNN-projects-phase-2-rules-learnings.md` with rule-tuning notes and dogfood findings (institutional learning we said we'd start in Phase 1 plan).

### Monitoring

- Per-rule p95 histogram (rule_id × duration_ms). Surface in DS lead dashboard (Phase 4) — Phase 2 just emits the metric.
- Fan-out queue depth gauge (`audit_jobs WHERE status='queued'`). Alert at >500 sustained for 5min.
- Worker pool utilization (`audit_jobs WHERE status='running'`). 6 sustained = overload.
- Sidecar-write disabled telemetry: count of audit runs that would have written but didn't (informational).

### Performance budgets (CI-asserted, from Phase 1; extended)

| Metric | Threshold | Source |
|--------|-----------|--------|
| Theme-parity rule p95 / screen | ≤200ms | Phase 2 |
| A11y contrast rule p95 / screen | ≤300ms | Phase 2 |
| Flow-graph rule p95 / flow (with cache hit) | ≤500ms | Phase 2 |
| Fan-out 47 flows total wall-time | ≤5min | Phase 2 |
| Bundle: existing chunks (atlas, drd, animations) | unchanged from Phase 1 | continuity |

CI extends `next build --analyze` parsing + adds Go benchmark `go test -bench=. ./internal/projects/rules/...` with thresholds.

---

## Sources & References

- **Origin document:** [`docs/brainstorms/2026-04-29-projects-flow-atlas-requirements.md`](../brainstorms/2026-04-29-projects-flow-atlas-requirements.md)
- **Predecessor plan:** [`docs/plans/2026-04-29-001-feat-projects-flow-atlas-phase-1-plan.md`](2026-04-29-001-feat-projects-flow-atlas-phase-1-plan.md)
- **WCAG 2.1 contrast spec:** <https://www.w3.org/TR/WCAG21/#contrast-minimum>
- **Figma file API (prototype):** <https://www.figma.com/developers/api#files-endpoints>
- **Phase 1 implementation surfaces:** `services/ds-service/internal/projects/{runner,worker,repository,recovery}.go`
- **Existing audit core:** `services/ds-service/internal/audit/{engine,types,persist}.go`
- **Existing sidecar consumer:** `app/files/[slug]/page.tsx`
- **DesignBrain CEL governance reference (Phase 7 prep, not Phase 2):** `~/DesignBrain-AI/internal/governance/`
