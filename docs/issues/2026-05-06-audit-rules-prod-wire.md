---
title: Wire production-mode data loaders for the 7 audit-rule engines
status: open
created: 2026-05-06
priority: medium
area: ds-service / audit
---

## What

`services/ds-service/internal/projects/rules/*.go` defines seven audit rules whose **rule logic is real and tested** but whose **production data loaders are stubbed behind interfaces**. Today the worker pool runs them against synthetic in-memory fixtures (loaders.go); production needs DB-backed implementations that join the live SQLite tables.

Each file carries a clearly scoped `TODO(U*-prod-wire)` comment that already lists what the production loader must do — no design work needed, just implementation.

## The seven loader implementations

| File | Interface | Loader needs |
|---|---|---|
| `rules/theme_parity.go` | `ResolvedTreeLoader` | Join `screen_canonical_trees × screen_modes`, resolve `boundVariables` against per-mode token catalog (`lib/tokens/indmoney/semantic-{light,dark}.tokens.json`), substitute concrete hex per mode |
| `rules/a11y_contrast.go` | `ScreenModeLoader` | Join `screen_canonical_trees × screen_modes`, return one tree per (screen, mode) pair with bound-variable fills already resolved |
| `rules/cross_persona.go` | `FlowsByProjectLoader` | Read flows + flow-persona links across all projects in a tenant; for each flow walk text content + figma_node ids; cross-reference duplicate flows wearing different persona hats |
| `rules/component_governance.go` | (per file comment) | Read component_sets + components from canonical_tree envelopes; cross-check against the published library |
| `rules/a11y_touch_target.go` | (sql.DB-backed loader) | Walk canonical_tree for INTERACTIVE atomic types (buttons, links, inputs), check absoluteBoundingBox against 44x44 touch-target rule |
| `rules/prototype.go` | `PrototypeFetcher` | `/v1/files/<key>/prototypes` (or canonical_tree's `prototypeStartNodeID` per page) + walk `interactions` arrays |
| `rules/loaders.go` | (umbrella registry) | Wire each rule's loader into the worker pool's compositeRunner |

## Why this is deferred, not a bug

These rules **don't silently fail in production**. The worker registers a composite runner via `rules.NewTenantAwareRunner` (cmd/server/main.go:247). When a rule's production loader isn't wired, that rule simply isn't invoked — the audit job still completes, other rules still run, and the missing rule's findings are absent rather than wrong.

The hard work for each rule is **already in the runner code** and unit-tested with in-memory fixtures. Production wiring is the remaining 30–60% per rule: SQL joins, token-catalog reads, optional Figma round-trips. Each rule independently shippable.

## Trigger to ship

- A specific rule starts coming up in design review and we want it auto-flagged on every export — implement that rule's loader.
- Or the design-systems team explicitly asks for "every rule we wrote should be running" — ship all seven loaders together.

Until then, the worker keeps running with whichever loaders are wired and skips the rest. No correctness regression, just gaps in audit coverage.

## Effort estimate

- ~1–2 days per rule for the loader + tests (3-7 days total spread out)
- Minimum-viable first pass: theme_parity + a11y_contrast (the two most-asked-about), ~3 days

## How to verify when shipping

For each rule, after wiring its loader: run `go test ./services/ds-service/internal/projects/rules/... -run <RuleName>`, then trigger a real export and inspect the audit_jobs row's `findings_count` against an expected baseline.
