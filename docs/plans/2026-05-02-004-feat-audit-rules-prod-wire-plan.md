---
title: "feat: Audit-rules production-wiring closure (TODO(U*-prod-wire) markers across services/ds-service/internal/projects/rules/)"
type: feat
status: draft
date: 2026-05-02
deepened:
origin:
---

# Audit-rules production-wiring closure

## Overview

The audit pipeline at `services/ds-service/internal/projects/rules/` ships with **8 production-wiring TODOs** that were deferred during Phase 2's rule rollout. Each rule has a working in-memory implementation + tests, but the production loaders that thread real DB rows / Figma data into the rule are stubbed. This plan closes those stubs.

**Why this is its own plan, not an item in `2026-05-02-003-feat-atlas-integration-seams-plan.md`:** the integration-seams plan covers the UI/UX rewrite (atlas mind graph + project view morph + visual hierarchy + tab UX). The audit-rules prod-wiring is a backend Go layer with its own dependency surface (Figma REST, SQLite joins, mode-resolved variable bindings) — different reviewer set, different test infrastructure, different deploy risk. Stuffing it into the seams plan would dilute both.

This plan is **drafted, not deepened.** Surface for ce-plan when ready.

---

## Problem Frame

When a designer exports a flow, the audit pipeline runs every rule against every screen. Today the pipeline emits *some* violations because the in-memory rule implementations work — but for several rule classes the production loaders that fetch the inputs the rules need are stubbed. Result: false negatives. A designer's flow may pass audit even when it has theme-parity, cross-persona, or accessibility-contrast violations the audit *would* catch if the loaders were wired.

---

## Requirements Trace

The brainstorm at `docs/brainstorms/2026-04-29-projects-flow-atlas-requirements.md` R7 ("Audit engine extends `services/ds-service/internal/audit/`. Adds rule classes: Theme parity, Cross-persona consistency, Component governance, Accessibility, Flow-level...") is **partially shipped**. This plan closes the gap for production data integration.

- R1. Each TODO(U*-prod-wire) marker resolved with a real loader implementation.
- R2. Per-rule production tests against seeded SQLite + a mocked Figma REST.
- R3. Existing rule-internal unit tests stay green throughout the rollout.

---

## TODO inventory

| File | TODO marker | What's stubbed | Effort |
|---|---|---|---|
| `services/ds-service/internal/projects/rules/theme_parity.go:65` | `TODO(U2-prod-wire)` | Production implementation needs to walk mode pairs + diff structural skeletons + emit Critical violation. The in-memory version assumes pre-resolved trees. | ~1.5d |
| `services/ds-service/internal/projects/rules/cross_persona.go:325` | `TODO(U3-prod-wire)` | `FlowsByProjectLoader` real implementation — currently a stub that returns an empty slice. Needs to query `flows` table grouped by (project_id, persona_id) and feed the rule. | ~1d |
| `services/ds-service/internal/projects/rules/a11y_contrast.go:80` | `TODO(U4-prod-wire)` | Production loader needs to join rendered PNGs + per-mode resolved variable bindings to compute WCAG contrast. Mode-resolution depends on the Go-side resolver mirroring `lib/projects/resolveTreeForMode.ts` — that resolver is itself a deferred dependency (see file header). | ~2d (resolver) + ~1d (loader) |
| `services/ds-service/internal/projects/rules/a11y_touch_target.go:83` | `TODO(U4-prod-wire)` | Production *sql.DB-backed loader for touch-target node iteration. | ~0.5d |
| `services/ds-service/internal/projects/rules/prototype.go:38` | `TODO(U5-prod-wire)` | Production `PrototypeFetcher` — calls Figma REST `/v1/files/<key>?depth=4` for the active version, parses prototype connections, feeds the flow-graph rule. Already used by `flow_graph.go` (next row). | ~1.5d |
| `services/ds-service/internal/projects/rules/flow_graph.go:40` | `TODO(U5-prod-wire)` | Wraps TenantRepo + the PrototypeFetcher above; tests use `stubLoader`. Replace stub. | ~0.5d (depends on prototype.go) |
| `services/ds-service/internal/projects/rules/component_governance.go:47` | `TODO(U6-prod-wire)` | Real implementation reads from `screen_components` join table + the manifest's composes graph. Detached-instance + override-sprawl + component-sprawl detection all depend on this. | ~1.5d |
| `services/ds-service/internal/projects/rules/loaders.go:2` | `TODO(U*-prod-wire)` | The blocking comment that lists all of the above; resolves once the other 7 land. | — |

**Total: ~9-10 person-days of focused backend work, with the resolver dependency on a11y_contrast adding 2 of those days as a critical-path prerequisite.**

---

## Sequencing

```
                   resolver (Go-side mode resolution, mirrors
                    lib/projects/resolveTreeForMode.ts) ~2d
                                  │
                  ┌───────────────┼────────────────────────┐
                  ▼               ▼                        ▼
          theme_parity     a11y_contrast        cross_persona
            ~1.5d              ~1d                  ~1d
                                                       │
                                                       └── component_governance ~1.5d
                                                              │
                                                              └── prototype + flow_graph ~2d
                                                                          │
                                                                          └── a11y_touch_target ~0.5d
```

Resolver is the choke point. Rest can parallelize once it ships.

---

## Out of scope

- **Rule severity tuning** — DS lead curates per-rule severity via the admin Rules page; not changed here.
- **New rule classes** — only the 8 stubs above are closed.
- **Plugin auto-fix expansion** — token + style auto-fix is shipped (`FixInFigmaButton` + audit-server forwarding); expanding to instance-override unwinding etc. is a separate plan.
- **Front-end UX changes** — violations rendering / filtering / lifecycle are all already shipped in the integration-seams plan (U6/U7) and unaffected.

---

## Dependencies

- Resolver implementation (Go-side mode-resolution) — required before theme_parity + a11y_contrast can ship.
- Figma REST quota — prototype + a11y_contrast loaders make per-version Figma calls; verify rate limits + caching strategy.
- Test data — need a seeded fixture project with intentional theme-parity / a11y / cross-persona issues to validate each rule end-to-end.

---

## Source pointers

- `services/ds-service/internal/projects/rules/loaders.go` — top-level header explaining the deferral.
- `services/ds-service/internal/projects/rules/resolver.go:9` — "Why the Go version exists at all (Phase 2 prod-wire deferred this)" comment.
- `lib/projects/resolveTreeForMode.ts` — the TS reference implementation the Go-side resolver must mirror.
- Each rule file's header comment — describes the stub vs production gap.

---

## When to execute

When integration-seams (`2026-05-02-003`) is fully shipped (U13 + U11 + remaining followups), and the team has bandwidth for ~2 weeks of focused backend work. Until then, the false-negative risk is documented + accepted; designers can manually flag missed violations through existing inbox + comment surfaces.
