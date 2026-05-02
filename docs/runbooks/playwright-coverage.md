# Playwright AE Coverage Matrix (U11)

Operator reference. Maps every brainstorm acceptance example (AE-1 → AE-8 in
`docs/brainstorms/2026-04-29-projects-flow-atlas-requirements.md`) to the
Playwright spec(s) that exercise the user-visible end-to-end story, plus
notes on which assertions are gated behind unshipped fixtures.

This is the load-bearing artifact for U11: every shipped unit (U1-U10, U12)
has tested wiring, but until U11 nobody had audited that the brainstorm's
canonical user stories were covered. This doc is that audit.

## Convention

- **Status:**
  - **Live** — assertions run unconditionally in CI.
  - **Skipped (fixture)** — `test.skip()` until the project-shell Playwright
    fixture builder lands. The mock surface in the body is the contract;
    when the fixture lands, the spec runs unchanged.
  - **Skipped (DS_E2E)** — needs a live ds-service + JWT. Gated on
    `DS_E2E=1` + `DS_AUTH_TOKEN`. Not run in PR CI; opt-in for nightly /
    integration runs.
- **Gap:** asserted-but-not-verified items (a spec exists and is reachable,
  but a specific assertion the AE calls for is not exercised). Tracked here
  rather than by silently editing existing specs (U11 scope is test + doc
  only; production / spec changes that aren't U11's gap-fill tests are
  flagged for follow-up).

## Matrix

| AE  | Story (one-line)                                         | Spec(s)                                                                                                                                | Status              | Notes / gaps                                                                                                                                                                                              |
| --- | -------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------- | ------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| AE-1 | Designer exports a flow → atlas paints + audit completes | `tests/projects/plugin-export-flow.spec.ts`                                                                                            | Skipped (DS_E2E)    | Asserts the full export → SSE → atlas-paints contract. Only runs when a real ds-service is reachable. The brainstorm's "violation count badge with 4 High and 2 Medium" is NOT pinned (see Gaps below).   |
| AE-2 | Theme parity catches a manual paint                      | `tests/projects/auto-fix-roundtrip.spec.ts`                                                                                            | Skipped (fixture)   | Covers the "Fix in Figma" deeplink contract. The actual Figma plugin write + re-export → Fixed transition is unit-tested in Go (`violation_get_test.go` + `lifecycle_test.go`); the docs-site half is here. |
| AE-3 | Cross-persona consistency (Toast missing in Logged-out)  | `tests/projects/cross-persona-consistency.spec.ts` *(new in U11)*                                                                      | Skipped (fixture)   | Asserts the High row + Acknowledge with reason path. Mock-only; no live audit engine dependency.                                                                                                          |
| AE-4 | DRD + Decision flow with violation linkage               | `tests/decisions/decision-creation.spec.ts`, `tests/decisions/supersession.spec.ts`, `tests/decisions/violations-cross-link.spec.ts`   | Skipped (fixture)   | Three specs cover the three sub-stories: create via `/decision` slash, supersession chain, and bidirectional V↔D cross-link. The slash-menu friendly-verb refactor (Phase 5.2 P2) is exercised separately in DRD specs. |
| AE-5 | Mind-graph reverse lookup (hover Toast → 23 flows card)  | `tests/atlas-mind-graph.spec.ts`                                                                                                       | Live                | Mount + chips + reduced-motion fallback are all live. The hover signal-card content ("23 flows, 6 products, 4 critical, 7 high") is a specific assertion not currently pinned (see Gaps below).            |
| AE-6 | Re-export preserves DRD, refreshes audit                 | `tests/projects/re-export-preserves-drd.spec.ts` *(new in U11)*                                                                        | Skipped (fixture)   | Four sub-tests: version selector lists v1 + v2; DRD unchanged across versions; v1 decisions stay on v1; v2 violations reflect carry-forward + new shape (1 active, 1 ack, 3 new — 2 v1 fixes hidden by default Active filter). |
| AE-7 | Token publish fans out to all flows                      | `tests/projects/audit-fanout.spec.ts`                                                                                                  | Skipped (DS_E2E)    | Asserts `audit_jobs` row count + drain-to-done invariant. The brainstorm's "47 flows had drift … rename suggestion" specific count is fixture-dependent and not pinned.                                  |
| AE-8 | Mind graph → flow morph (and reverse via Esc)            | `tests/projects/atlas-leaf-morph.spec.ts`, `tests/projects/atlas-reverse-morph.spec.ts`                                                | Live (skip-on-empty)| Both specs are live but `test.skip` when the live fixture has no flow nodes with project URLs. Asserts the navigation + view-transition-name contract; the actual animation pseudo-elements are explicitly out of scope (browser-internal). |

## Supporting / closure specs

These don't map 1:1 to an AE but cover R-level requirements that AE stories
touch:

| Requirement | Spec | Status | Notes |
| ----------- | ---- | ------ | ----- |
| F2 — two-phase pipeline UI progression | `tests/projects/two-phase-pipeline.spec.ts` | Live | Stub-EventSource drives the badge state machine; deterministic. |
| R8 — violation lifecycle (Acknowledge / Reactivate) | `tests/projects/violation-lifecycle.spec.ts` | Skipped (fixture) | Exercises designer Acknowledge + admin Reactivate. AE-3's acknowledge path is *also* covered by `cross-persona-consistency.spec.ts` (U11 doesn't dedup — both stand). |
| R14 — persona × theme filter chips | `tests/projects/violation-filters.spec.ts` | Skipped (fixture) | Multi-select persona, theme restriction, auto-fix-only-on-fixable, empty-state. |

## Known gaps (followups, NOT in U11 scope)

U11's discipline is **tests + docs only**. The following gaps were
identified during the audit and are recorded here for the next test-hardening
pass; they do NOT modify any existing spec:

1. **AE-1 specific severity counts unpinned.** The brainstorm calls out
   "4 High and 2 Medium" after a synthetic 6-frame export. The current
   `plugin-export-flow.spec.ts` asserts the SSE event order and that 3
   atlas frames render — it does not assert the violation-count breakdown.
   *Followup:* extend the same spec with a query to `/v1/projects/.../violations`
   after `audit_complete` and assert the severity histogram. Gated on the
   same DS_E2E env that already gates the spec.

2. **AE-5 hover signal-card content unpinned.** `atlas-mind-graph.spec.ts`
   asserts that filter chips render and the canvas mounts, but the
   "Used in 23 flows across 6 products, 4 critical violations, 7 high"
   card content is not asserted. The signal-card component itself has
   unit-test coverage; the e2e gap is pinning the populated content
   against a fixture. *Followup:* add a `tests/atlas-mind-graph-signal-card.spec.ts`
   once the dev-server graph fixture has predictable counts.

3. **AE-7 specific impact-count unpinned.** `audit-fanout.spec.ts` asserts
   the lower bound (`>= 5` enqueued jobs) and full drain. The brainstorm's
   "47 flows had drift" is fixture-dependent and not pinned. The current
   bound is the right invariant for the scaling contract; pinning a
   specific number would couple the spec to a synthetic-fixture row count.
   *Followup:* leave as-is unless the team explicitly wants a synthetic
   "47-flow drift fixture" to mirror the brainstorm number.

4. **AE-3 / AE-6 are skipped on the project-shell fixture builder.** Both
   new U11 specs are correctly authored and their mocks are the contract,
   but the surrounding shell — auth, `/v1/projects/:slug` shape, flows,
   screens — has to land before they run. Same blocker as
   `violation-lifecycle.spec.ts`, `violation-filters.spec.ts`,
   `decision-creation.spec.ts`, `supersession.spec.ts`,
   `violations-cross-link.spec.ts`. *Followup:* a single fixture-builder
   unit unblocks all eight specs at once.

5. **`auto-fix-roundtrip.spec.ts` only asserts the deeplink prefix.** The
   AE-2 closure ("re-export → violation Fixed") is covered by the Go
   unit tests cited in the spec header, but the docs-site UI half stops
   at the popup capture. *Followup:* extend with a second `test.skip()`
   block that mocks a follow-up `audit_complete` SSE event and asserts
   the row's `data-status` flips to `fixed`.

## Running the suite

```bash
# Lists all U11-impacted specs without parsing failures.
npx playwright test --list \
  tests/projects/cross-persona-consistency.spec.ts \
  tests/projects/re-export-preserves-drd.spec.ts \
  tests/projects/plugin-export-flow.spec.ts \
  tests/projects/auto-fix-roundtrip.spec.ts \
  tests/projects/atlas-leaf-morph.spec.ts \
  tests/projects/atlas-reverse-morph.spec.ts \
  tests/projects/two-phase-pipeline.spec.ts \
  tests/projects/audit-fanout.spec.ts \
  tests/projects/violation-lifecycle.spec.ts \
  tests/projects/violation-filters.spec.ts \
  tests/decisions/decision-creation.spec.ts \
  tests/decisions/supersession.spec.ts \
  tests/decisions/violations-cross-link.spec.ts \
  tests/atlas-mind-graph.spec.ts

# Run the live (un-skipped) subset against a dev server on :3001:
npm run dev &
npx playwright test \
  tests/projects/two-phase-pipeline.spec.ts \
  tests/projects/atlas-leaf-morph.spec.ts \
  tests/projects/atlas-reverse-morph.spec.ts \
  tests/atlas-mind-graph.spec.ts

# Run the DS_E2E subset (needs a real ds-service + super-admin JWT):
DS_E2E=1 DS_AUTH_TOKEN=<jwt> DS_ADMIN_URL=http://localhost:7475 \
  npx playwright test \
  tests/projects/plugin-export-flow.spec.ts \
  tests/projects/audit-fanout.spec.ts
```

## Glossary

- **Project shell fixture builder** — a not-yet-shipped helper that mocks
  `/v1/projects/:slug` + `/flows` + `/screens` + auth so the project page
  renders without a live ds-service. Tracked in the Phase 5/6 testing
  follow-ups; unblocks every `Skipped (fixture)` row above at once.
- **DS_E2E** — env-var gate for specs that need a real ds-service. Set
  `DS_E2E=1` plus `DS_AUTH_TOKEN=<jwt>` to opt in.
