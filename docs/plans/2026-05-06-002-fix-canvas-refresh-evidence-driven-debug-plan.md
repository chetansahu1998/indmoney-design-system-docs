---
title: "fix: canvas-v2 refresh symptom — evidence-driven debug + targeted fix"
type: fix
status: active
date: 2026-05-06
deepened: 2026-05-06
---

# fix: canvas-v2 refresh symptom — evidence-driven debug + targeted fix

## Overview

User reports the leaf canvas (`app/atlas/_lib/leafcanvas.tsx`) "is getting refreshed every time" — frames stuck on shimmer, camera snapping back to the auto-fit landing zone after panning, and a felt sense of repeated reload during a session. The user's prior frustration is with **speculative fixes that don't first prove the cause**. This plan is therefore characterization-first: instrument the live pipeline, capture evidence of what is actually mutating, classify the symptom against four candidate causes, and only then ship a targeted fix.

A 50-second headless audit (Playwright + `window.__CANVAS_LOG=true`) has already established that the literal "LeafCanvas remount loop" hypothesis is **false** — `__LC_MOUNTS` reaches 2 (StrictMode dev artifact) then stays stable. So the symptom is a *felt* refresh, not a literal one. External research (see Sources) identifies four 2025/2026-canonical candidate root causes for that exact class of bug: Zustand selector-identity churn, IntersectionObserver-on-transform-parent (a known browser footgun directly relevant to our pan/zoom architecture), camera state coupled to data-store updates, and Turbopack Fast-Refresh module-singleton re-init. The plan instruments for all four, decides from evidence, then fixes the one we actually find.

---

## Problem Frame

**Symptom (user-reported, live browser, real interaction):**
- "Canvas is getting refreshed every time."
- Frames render PNGs initially, then re-shimmer.
- Camera resets to auto-fit landing zone after the user pans away.
- Net effect: rendering feels broken / slow / unstable.

**What is NOT the cause** (already disproved):
- LeafCanvas literal remount loop. Headless audit shows `mounts=2 unmounts=1` (StrictMode initial), stable for 47s afterwards.
- `slotVersion` keying on `loadedAt` cycling. `loadedAt` is only set by `openLeaf` (`lib/atlas/live-store.ts:449`) and not by `applyEvent`/override mutations.
- `flowsSignature` cycling faster than 60s (`AtlasShell.tsx:208` interval).

**What we have already changed in the last 12 hours** (must NOT be reverted unless evidence points there):
- `gesture-tracker` singleton + 150 ms debounce (`app/atlas/_lib/leafcanvas-v2/gesture-tracker.ts`).
- `leaf-zoom-signal` split into `live` + `settled` (`app/atlas/_lib/leafcanvas-v2/leaf-zoom-signal.ts`).
- Camera lives in `camRef`, RAF-flushed to `worldRef.current.style.transform`, no per-wheel React state (`app/atlas/_lib/leafcanvas.tsx`).
- Auto-fit moved to `useLayoutEffect` and gated by `fitDoneForLeafRef` (per-leaf-id once-only).
- LeafFrameRenderer IO callbacks defer mount during `getIsGesturing()`, drain on settle.
- `cached !== undefined` short-circuit moved BEFORE the `warm` gate so cache hits hydrate without IO.
- Diagnostic `clog()` namespaces: `camera`, `gesture`, `io`, `tree`. Off unless `window.__CANVAS_LOG=true`.

**Strongest active hypothesis from external research:** IntersectionObserver does **not** recompute when an ancestor's `transform` changes — only when scroll, resize, or the target element's own bbox changes. Our pan/zoom architecture transforms the `.lc-world` ancestor; IO observers on `.leafcv2-frame` descendants therefore stop firing reliably after pan. Frames panned into viewport stay `isIntersecting=false` from IO's perspective even though they're visibly on-screen. This matches the "stuck on shimmer" symptom exactly and is documented in Mozilla bug 1419339 and WebKit bug 209264. **This is the highest-prior-probability cause.** Plan still verifies before fixing — but if the instrumentation in U2 confirms it, U6 ships the canonical tldraw-pattern fix (decouple culling from IO, drive it from the camera signal directly).

---

## Requirements Trace

- R1. Reproduce the symptom in an instrumented session and capture telemetry that classifies it against the four candidate causes (selector churn, IO/transform footgun, camera coupling, HMR).
- R2. Prove or disprove each candidate cause with evidence — not speculation, not "this looks like it could".
- R3. Ship one targeted fix matched to the diagnosed cause; do not stack speculative fixes from the candidate list.
- R4. Preserve the gestures/camera/lazy-load work shipped in the last 12 hours unless evidence specifically implicates a unit there.
- R5. Leave the diagnostic surface (`clog`, render-trace, store-trace, prod build option) in place behind a flag so the next regression is faster to triage.

---

## Scope Boundaries

- Not changing the data pipeline (`ds-service`, canonical_tree fetching, asset preview pyramid). NRI VKYC refresh and pipeline are stable; logs show 200 OK.
- Not redesigning the brain graph (`app/atlas/BrainGraph.tsx`) or atlas shell architecture. The fix lives inside `leafcanvas.tsx`, `LeafFrameRenderer.tsx`, and the live-store selectors only.
- Not introducing a new state-management library. Zustand 5.x stays. Camera-store split (if needed by U6) uses Zustand's `subscribeWithSelector` middleware that's already available.
- Not reverting CZ1–CZ6 unless U3/U4 evidence specifically implicates one of them. The work that actually broke things, if any, is whatever U2 captures.
- Not introducing `react-scan` or WDYR as production-shipped libraries — they install as dev-only or are mounted under `process.env.NODE_ENV === 'development'` guards.

### Deferred to Follow-Up Work

- Camera persistence to `localStorage` per leaf (so users return to where they last left off): out of scope here; surface for a future plan once the active bug is fixed.
- Replacing IO with a fully camera-driven culling system (full tldraw / React-Flow pattern): only happens if U4 diagnoses the IO/transform footgun as the root cause; otherwise stays deferred.
- Migrating SSE writes to `startTransition` for input-jank improvement: useful regardless, but only land it here if it directly addresses the diagnosed symptom.

---

## Context & Research

### Relevant Code and Patterns

- `app/atlas/_lib/leafcanvas.tsx` — top-level canvas component, owns the camera ref, wheel/pan handlers, RAF flush, world transform target. Recently refactored (CZ3); `// @ts-nocheck`.
- `app/atlas/_lib/leafcanvas-v2/LeafFrameRenderer.tsx` — per-frame renderer, IO setup, tree-resolution effect, useImageRefs/useIconClusterURLs.
- `app/atlas/_lib/leafcanvas-v2/gesture-tracker.ts` — module-level singleton, 150 ms debounce, `useIsGesturing()` hook (CZ1).
- `app/atlas/_lib/leafcanvas-v2/idle-tracker.ts` — sister singleton, 500 ms idle window for IO detach.
- `app/atlas/_lib/leafcanvas-v2/leaf-zoom-signal.ts` — module-level live + settled zoom signals (CZ2).
- `app/atlas/_lib/leafcanvas-v2/canvas-log.ts` — `clog(ns, label, payload)` gated on `window.__CANVAS_LOG`.
- `app/atlas/_lib/AtlasShellInner.tsx:283` — `slotVersion = leafSlots[leafID]?.loadedAt`, used as remount key.
- `app/atlas/_lib/AtlasShell.tsx:128–133, 200–203` — `remountKey` bumped when `flowsSignature` changes; 60-second `refreshBrain` poll at line 208.
- `lib/atlas/live-store.ts` — Zustand 5 store. `set({ flows: merged })` rebuilds the array on every `refreshBrain()`. SSE handler `applyEvent` at line 763 dispatches `fetchLeafOverlays` for `audit_complete` etc., writing back to `leafSlots`.
- `app/atlas/_lib/real-data-bridge.ts:194` — wraps `LeafFrameRenderer` inside a `<div className="ph-screen ph-screen--v2">` that sits inside `.lc-frame`. The IO target lives several DOM levels under `.lc-world`'s `transform`.

### Institutional Learnings

(Searched `docs/solutions/` — no entries directly indexed under canvas-v2 / leafcanvas as of 2026-05-06; the existing perf plan is `docs/plans/2026-05-06-001-perf-canvas-v2-design-brain-borrow-plan.md`, which is a forward-looking perf plan that the CZ1–CZ6 work executed against.)

- Prior canvas-v2 self-cancelling-fetch bug (commit `2b7e572`, 2026-05-06 03:19): the tree-resolution effect had `state.status` in its deps and self-cancelled — frames stuck on shimmer. Pattern to remember: any setState inside an effect whose own state is in its deps will cancel itself on the very next render. The current effect deps `[warm, slug, screenID, cached]` are correct; do not re-introduce `intersected` or `state.status` here.
- Prior content-visibility experiment (CZ5, reverted): adding `content-visibility: auto` to `.lc-frame` caused IO to never fire on descendants because the host's children were skipped from layout. Reinstating it is only safe with concrete IO compatibility evidence.

### External References

(Curated from research dispatch 2026-05-06 — full citations in **Sources & References**.)

- **IntersectionObserver + transform parent footgun** — Mozilla bug 1419339, WebKit bug 209264. IO does not recompute when an ancestor's `transform` changes. **Direct match for our pan/zoom architecture.** Standard fixes: re-observe on gesture-end, replace IO with manual viewport-rect math driven by camera signal, or animate `left/top` instead of `transform`.
- **react-scan** (https://github.com/aidenybai/react-scan) — 2025 canonical tool, zero-config, paints colored outlines on every component re-render. Replaces WDYR for React 19 work.
- **Zustand `useShallow`** (https://zustand.docs.pmnd.rs/learn/guides/prevent-rerenders-with-use-shallow) — canonical fix for selectors returning multi-key objects/arrays; returning a fresh `[a, b]` re-renders on every store emit.
- **`useSyncExternalStore` snapshot caching** (https://react.dev/reference/react/useSyncExternalStore) — `getSnapshot` MUST return `Object.is`-equal references when nothing changed, or React warns / loops.
- **tldraw camera pattern** (https://tldraw.dev/sdk-features/camera) — camera lives in a reactive signal outside React, RAF-synchronized; data updates do not invalidate camera consumers. We are halfway there (camera ref + RAF flush) but the live zoom signal still goes through `useSyncExternalStore`.
- **CSS `content-visibility: auto`** (MDN) — production canvases use this to skip layout/paint of off-viewport children. Only safe paired with compatible IO setup.
- **`startTransition` for SSE handlers** (https://react.dev/reference/react/useTransition) — wraps non-urgent store writes so input/pan stays responsive.
- **Turbopack Fast-Refresh module-singleton re-init** — Vercel issue #89530 (multiple BUILT cycles per save), #91768 (HMR regression). Module-level state (we have 4: `gesture-tracker`, `idle-tracker`, `fetch-queue`, `leaf-zoom-signal`) is re-initialized on Fast Refresh. Diagnostic: verify in `next build && next start`.

### Tech Stack (current, 2026-05-06)

- React 19.2.4 (StrictMode is on by default in dev). `react-scan` not installed.
- Next.js 16.2.1 with Turbopack dev. Known multiple-BUILT-per-save issues active.
- TypeScript 5, Zustand 5.0.12, Tailwind 4.

---

## Key Technical Decisions

- **Characterization-first execution.** No code change ships before U2 captures live evidence. Every prior turn-of-the-clock fix was speculative; this plan reverses that posture explicitly.
- **One targeted fix, not a stack of fixes.** Once U4 diagnoses the cause, U5/U6/U7/U8 deliver only the one that addresses it. The other diagnostic candidates remain instrumented and ready to ship as follow-up plans if a different regression surfaces later.
- **Diagnostic surface stays.** `window.__CANVAS_LOG`, `clog`, render-trace, and store-trace ship behind dev guards permanently. Future regressions become 5-minute triages, not 12-hour debugging sessions.
- **Production build verification is mandatory.** Per research item #13, the highest-leverage single test is `next build && next start`. If the symptom doesn't reproduce there, the bug is HMR/Fast-Refresh and not real React. Capture this signal in U2.
- **Preserve CZ1–CZ6 work unless evidence implicates it.** The user has explicitly said they don't want speculative reverts. The fitDoneForLeafRef + useLayoutEffect auto-fit + cache-before-warm + gesture-deferred IO are all evidence-supported by the existing audits.

---

## Open Questions

### Resolved During Planning

- **Is the LeafCanvas literally remounting?** No — headless audit shows mounts=2 (StrictMode), stable for 50 s. The symptom is a felt refresh, not a literal one.
- **Does `slotVersion` cycle?** No — `loadedAt` is only set by `openLeaf` and unchanged by `applyEvent` override mutations.
- **Which research candidate has the highest prior probability?** IO + transform parent footgun. Direct architectural match (we transform `.lc-world`, observe descendants), and the user's "stuck on shimmer after panning" precisely matches the symptom signature. But the plan still verifies before fixing — a Zustand selector or HMR cycle could also produce a "feels like refresh" experience.

### Deferred to Implementation

- **Exact selector(s) churning, if any.** Captured by the trace HOC in U2; not knowable until live data lands.
- **Whether the camera bounce-back is a real auto-fit re-fire or a perceived jump from frames mounting in a stale-camera location.** Captured by the camera-trace in U2.
- **Final shape of the IO replacement / fix.** Depends on U4's classification — a re-observe-on-gesture-end is one option, a tldraw-style camera-driven culler is another. U6 picks the one that matches.

---

## High-Level Technical Design

> *This illustrates the diagnostic flow and is directional guidance for review, not implementation specification.*

```
                           +---------------------------+
                           |  U1 install diag surface  |
                           |  (react-scan, traces)     |
                           +-------------+-------------+
                                         |
                           +-------------v-------------+
                           |  U2 capture live evidence |
                           |  with __CANVAS_LOG=true   |
                           |  + react-scan + DevTools  |
                           |  Profiler + prod-build    |
                           |  comparison               |
                           +-------------+-------------+
                                         |
                           +-------------v-------------+
                           |  U3 catalog mutation paths|
                           |  every place that writes  |
                           |  camera / IO / state /    |
                           |  store, with diff         |
                           +-------------+-------------+
                                         |
                           +-------------v-------------+
                           |  U4 classify symptom      |
                           |  (decision gate)          |
                           +------+------+------+------+
                                  |      |      |      |
                  +---------------+      |      |      +-----------------+
                  |                      |      |                        |
       Cause A:                Cause B:        Cause C:           Cause D:
       Zustand                 IO+transform    Camera-store        Turbopack
       selector churn          footgun         coupling             HMR / FR
                  |                      |      |                        |
       +----------v---+    +-------------v-+  +-v---------------+   +----v---+
       | U5 selector  |    | U6 IO refire / |  | U7 camera-store |   | U8 HMR |
       | useShallow + |    | camera-driven  |  | de-couple from  |   | guards |
       | trace            | culling pattern |  | leafSlots       |   | + prod |
       +--------------+    +----------------+  +-----------------+   | doc    |
                                                                     +--------+

Only ONE of U5–U8 ships, matched to U4's diagnosis. The others remain
instrumented for the next regression.
```

---

## Implementation Units

- U1. **Install diagnostic surface (react-scan + render trace + store trace)**

**Goal:** Stand up the live, in-browser diagnostic toolset that U2 will use to capture evidence. Zero behavioral change.

**Requirements:** R1, R5

**Dependencies:** None

**Files:**
- Modify: `package.json` (add `react-scan` to `devDependencies`)
- Create: `app/atlas/_lib/leafcanvas-v2/_diagnostics/use-render-trace.ts`
- Create: `app/atlas/_lib/leafcanvas-v2/_diagnostics/use-traced-selector.ts`
- Create: `app/atlas/_lib/leafcanvas-v2/_diagnostics/store-subscribe-trace.ts`
- Modify: `components/RootClient.tsx` (mount `react-scan` under a dev + flag guard)
- Modify: `app/atlas/_lib/leafcanvas.tsx` (call `useRenderTrace` on `LeafCanvas` props)
- Modify: `app/atlas/_lib/leafcanvas-v2/LeafFrameRenderer.tsx` (call `useRenderTrace` on its props; wrap the `cached` selector with `useTracedSelector`)
- Test: `app/atlas/_lib/leafcanvas-v2/_diagnostics/__tests__/use-render-trace.test.ts`

**Approach:**
- `useRenderTrace(name, props)` — increments a per-name counter, diff's prop identity vs prev render, logs the changed keys via `clog("render", ...)`. Off when `window.__CANVAS_LOG !== true`.
- `useTracedSelector(name, selectorFn, equalityFn?)` — wraps `useAtlas`; logs whenever the selected value's reference changes, including JSON snapshot of prev vs next so we can confirm whether content actually changed.
- `installStoreSubscribeTrace()` — module-level subscriber on `useAtlas` that logs the diffed keys of the store on every emit. Mounted once from `RootClient` behind the same dev flag.
- `react-scan` mounted by dynamic import inside `RootClient` only when `process.env.NODE_ENV === 'development' && window.__CANVAS_LOG === true`. Default-off so day-to-day dev isn't noisier.

**Patterns to follow:**
- Existing `clog` shape in `app/atlas/_lib/leafcanvas-v2/canvas-log.ts`.
- Existing IdleTracker injectable-clock pattern for testability (`app/atlas/_lib/leafcanvas-v2/idle-tracker.ts`).
- Zero deps inside the trace modules (test via FakeClock + ref objects).

**Test scenarios:**
- Happy path: `useRenderTrace("X", { a: 1, b: 2 })` re-rendered with same prop refs → logs `"no-prop-change"`.
- Edge case: same component re-rendered with new prop ref but same primitive value → logs `"changed: ['b']"` even when values match (this is the bug class we're catching).
- Error path: `__CANVAS_LOG === false` → no logs emit (fast-path).
- Integration: `useTracedSelector` wired against a fake Zustand store, store emits with same selected reference → no log; emits with new reference but identical content → logs.

**Verification:**
- With `__CANVAS_LOG=true`, opening a leaf prints render counts for each `LeafFrameRenderer` and the `LeafCanvas`. Selector traces show every store-emit-driven re-evaluation.
- `react-scan` paints highlights on the visible re-render edges during a synthetic SSE event in the browser.
- `pnpm build` succeeds; production bundle does not include the trace modules' log payloads (fast-path returns before any work).

---

- U2. **Capture live evidence (10-minute structured session)**

**Goal:** Reproduce the user's symptom in a real browser with U1's instrumentation on, capture the trace, and produce a written evidence record that pins the cause. **No code changes** in this unit — pure observation.

**Requirements:** R1, R2

**Dependencies:** U1

**Files:**
- Create: `docs/solutions/2026-05-06-canvas-refresh-evidence.md` (the evidence dossier — written from the live session)

**Approach:**
- Open `http://localhost:3001/atlas?platform=mobile` in Chrome with React DevTools (Profiler tab, Highlight Updates ON, "Record why each component rendered" ON).
- Set `window.__CANVAS_LOG = true` before opening a leaf.
- Open NRI VKYC (`onboarding-kyc-nri-vkyc-5f614a`) — capture the initial mount logs, the auto-fit camera write, and the first 30 seconds of activity.
- Pan slowly across the canvas, then quickly. Record the wheel-pan / camera-apply / IO logs.
- Wait 60+ seconds without interaction. Record any unsolicited logs (this catches SSE-driven cycles).
- **Production build comparison:** stop dev, run `pnpm build && pnpm start`, repeat the same scenario. **If the symptom does not reproduce in prod, the bug is Turbopack HMR — go to U8 (Cause D).**
- Write the evidence dossier with: every observed symptom, the matching log signature, the candidate cause it points to.

**Execution note:** Characterization-first. Goal is to build a fact log, not to fix anything. If the urge to "just try a fix" surfaces, write it in the dossier under Hypotheses Considered and move on.

**Patterns to follow:**
- Existing audit dossier shape: `docs/solutions/` Markdown files referencing source paths and timestamps.

**Test scenarios:**
- Test expectation: none — this unit is a manual evidence capture, not a code unit.

**Verification:**
- `docs/solutions/2026-05-06-canvas-refresh-evidence.md` exists, names the candidate cause selected, and references the supporting log lines / DevTools screenshots.
- The evidence record is specific enough that an implementer reading only the dossier can pick which of U5/U6/U7/U8 to ship without re-running the capture.

---

- U3. **Catalog every mutation path (camera / IO / store / remount key)**

**Goal:** Build a static, code-grounded inventory of every place state can change in the rendering pipeline, with rationale for whether each is necessary. This is the reference document U4 uses to classify the U2 evidence and rule out alternatives.

**Requirements:** R1, R2

**Dependencies:** None (can run in parallel with U2)

**Files:**
- Create: `docs/solutions/2026-05-06-canvas-mutation-catalog.md`

**Approach:**
- Walk every file under `app/atlas/_lib/leafcanvas.tsx`, `app/atlas/_lib/leafcanvas-v2/`, `app/atlas/_lib/AtlasShell.tsx`, `app/atlas/_lib/AtlasShellInner.tsx`, `lib/atlas/live-store.ts`.
- For each, list every site that mutates: `camRef`, `cam` JSX (now removed), `worldRef.current.style`, `IntersectionObserver` lifecycle, `setIntersected` / `setWarm` / `setState({status:...})`, `set({ leafSlots: ... })`, `loadedAt`, `flows` (signature), `remountKey`.
- For each site, record: file:line, trigger, write payload, justification or question mark.
- Include an "edges that should never fire on idle" table — if any line in U2's evidence shows one of these firing, that's the cause.

**Patterns to follow:**
- Existing audit-style dossiers in `docs/solutions/`.

**Test scenarios:**
- Test expectation: none — this is a documentation unit.

**Verification:**
- Document is complete enough that, given the U2 evidence, a reader can pick the offending mutation site by index.

---

- U4. **Classify the symptom and decide which fix unit to ship (decision gate)**

**Goal:** Read U2 evidence + U3 catalog, classify the cause as one of {A: Zustand selector churn, B: IO/transform footgun, C: Camera-store coupling, D: Turbopack HMR}, and select U5/U6/U7/U8 accordingly. **No fix code yet.**

**Requirements:** R2, R3

**Dependencies:** U2, U3

**Files:**
- Modify: `docs/solutions/2026-05-06-canvas-refresh-evidence.md` (add the classification + selected unit, with rationale)

**Approach:**
- Map U2's observed log signatures to the four candidate signatures below:
  - Cause A (selector): `[sel] X` logs fire on store emit, but JSON of prev vs next is identical → unwanted re-render. `useShallow` candidate.
  - Cause B (IO/transform): pan-into-viewport frames stay `idle` indefinitely; no `io: warm fired` even after gesture settle. Re-observe forces a fire. **High prior**.
  - Cause C (camera coupling): a camera-consuming component (zoom badge, etc.) re-renders every time `leafSlots` mutates, even when the camera value is unchanged.
  - Cause D (HMR): symptom does not reproduce in `pnpm build && pnpm start`.
- Note: the four causes are not strictly exclusive; if U2 captures evidence for multiple, this unit picks the one whose elimination would also resolve the others (typically B or C — they cascade).

**Test scenarios:**
- Test expectation: none — this is a decision/analysis unit.

**Verification:**
- The selected fix unit is named in the dossier with a one-paragraph rationale tying it to specific log lines from U2.
- Units that were not selected are explicitly marked "instrumented but not shipped this plan; re-evaluate if the symptom returns."

---

- U5. **Cause A fix — Zustand selector audit, `useShallow`, store-write discipline** *(only if U4 selects)*

**Goal:** Eliminate spurious re-renders by adding `useShallow` to multi-pick selectors, replacing any selector that returns a fresh object/array on every emit, and auditing `set()` calls for unnecessary spreads.

**Requirements:** R3

**Dependencies:** U4 selects A

**Files:**
- Modify: `lib/atlas/live-store.ts` (audit `set({...})` calls; ensure `flows` only reassigns when content meaningfully changed; consider `subscribeWithSelector` middleware)
- Modify: `app/atlas/_lib/AtlasShellInner.tsx` (selectors that pick multiple state keys → `useShallow`)
- Modify: any other consumer surfaced by U2's selector trace
- Test: `lib/atlas/__tests__/live-store-selectors.test.ts`

**Approach:**
- Replace any `useAtlas((s) => [s.a, s.b])` or `useAtlas((s) => ({ a: s.a, b: s.b }))` with `useAtlas(useShallow((s) => …))`.
- For `refreshBrain` and similar: if the merged array is content-equal to the previous, keep the previous reference. Use a small content-hash compare or explicit per-flow merge that preserves identity for unchanged rows.
- Run U1's selector trace with the fix applied to verify the offending log goes silent on idle.

**Patterns to follow:**
- xyflow's PR #5629 (`useShallow` rollout for similar canvas).
- Zustand 5 guidance: https://zustand.docs.pmnd.rs/learn/guides/prevent-rerenders-with-use-shallow

**Test scenarios:**
- Happy path: store update that doesn't change selected keys → consumer does not re-render.
- Edge case: store update that adds a new key not in the selector → consumer does not re-render.
- Integration: full store + selector + memoed selector identity audit harness; run a synthetic `applyEvent` cycle and assert subscriber fires only when truly relevant.

**Verification:**
- U1 trace shows zero "selector churn" lines during a 60 s idle session.
- U2's symptom no longer reproduces in dev or prod build.

---

- U6. **Cause B fix — IntersectionObserver re-fire on gesture-end + camera-driven culling fallback** *(only if U4 selects)*

**Goal:** Restore correct lazy-mount behavior under pan/zoom. Either force IO re-observe on gesture settle (low-cost) or replace IO with manual viewport-rect culling driven by the camera RAF (tldraw pattern, higher correctness ceiling).

**Requirements:** R3, R4

**Dependencies:** U4 selects B

**Files:**
- Modify: `app/atlas/_lib/leafcanvas-v2/LeafFrameRenderer.tsx` (IO setup effect)
- Modify: `app/atlas/_lib/leafcanvas-v2/gesture-tracker.ts` (no API change — a settle callback already exists; verify subscriber pattern is sufficient)
- Optionally create: `app/atlas/_lib/leafcanvas-v2/viewport-culler.ts` (camera-driven culler if Tier 2 is selected)
- Test: `app/atlas/_lib/leafcanvas-v2/__tests__/leaf-frame-renderer-io.test.ts`

**Approach:**
- **Tier 1 (minimal):** on `canvasGestureTracker.subscribe(g => !g)`, for every still-`!warm` LeafFrameRenderer, `observer.unobserve(el); observer.observe(el)`. Browser recomputes intersection against current layout (which now includes the post-pan transform). Frames in viewport get `isIntersecting=true` → existing code paths fire. The fix is ~10 lines inside the IO `attach()` closure, plus a settle subscriber.
- **Tier 2 (high-ceiling, only if Tier 1 doesn't fully resolve):** replace IO with a `viewport-culler.ts` module that subscribes to the camera RAF, computes screen-space rects from `(camera.x, camera.y, camera.z, frame.x, frame.y, frame.w, frame.h)`, and emits `inViewport[screenID]` to LeafFrameRenderer via a third module-level signal. tldraw / React-Flow architecture. Removes IO from the leaf canvas entirely.
- Document the choice in U4's dossier.

**Patterns to follow:**
- tldraw camera+culler: https://tldraw.dev/sdk-features/camera + https://tldraw.dev/sdk-features/performance
- Existing module-level signal pattern: `gesture-tracker.ts`, `leaf-zoom-signal.ts`.
- Existing IO closure in `LeafFrameRenderer.tsx` lines 250–340 (post-CZ4 with the `pendingWarm`/`pendingHot` defer).

**Test scenarios:**
- Happy path: frame initially off-viewport, IO not firing, user pans frame into view, gesture settles, IO re-observe fires `isIntersecting=true`, status transitions to `loading` → `ready`.
- Edge case: frame at exact rootMargin boundary, partial-intersect on settle, single transition to `warm` (no flicker).
- Error path: rapid pan-pan-pan sequence (gesture never fully settles) → no leaked IO observers; cleanup still runs on unmount.
- Integration: 79-frame leaf, pan from one side to the other end-to-end, all frames eventually transition to `ready` without manual scroll. Assert via Playwright + `data-status` attributes.

**Verification:**
- U1 trace shows `io: warm fired` after every pan-and-settle.
- Frame-status audit (`data-status="ready"`) reaches 79/79 within 10 s of a full-canvas pan, with no manual viewport jiggle required.
- Reproduction of the original symptom no longer fires.

---

- U7. **Cause C fix — decouple camera from leafSlots store** *(only if U4 selects)*

**Goal:** Move camera-consuming subscribers (zoom badge, fitAll button %, frame-strip selected state, etc.) off the same store slice that SSE updates write to, so a `leafSlots` mutation never re-renders any camera consumer.

**Requirements:** R3, R4

**Dependencies:** U4 selects C

**Files:**
- Modify: `app/atlas/_lib/leafcanvas-v2/leaf-zoom-signal.ts` (already split; verify settled signal isn't bridged through leafSlots)
- Create: `app/atlas/_lib/leafcanvas-v2/camera-store.ts` (Zustand `subscribeWithSelector` instance OR vanilla module-level signal; Tier 1 = vanilla signal; Tier 2 = Zustand vanilla store with `subscribeWithSelector` for ecosystem compat)
- Modify: `app/atlas/_lib/leafcanvas.tsx` (move `camRef` into the new camera-store; `applyCameraToDOM` reads from there)
- Modify: any consumer that previously read camera state via `useAtlas` selector (none expected today, but U2's audit will name them if they exist)

**Approach:**
- The existing camera lives in `camRef.current`, which is good; the gap is `useLeafZoomLive`/`useLeafZoomSettled` going through `useSyncExternalStore` from inside `LeafFrameRenderer`. If U2 shows that hook re-firing on `leafSlots` updates (it shouldn't, but trace will tell), we move the live signal into a fully separate store.
- Run U1's selector trace post-fix to confirm camera subscribers fire ONLY on real camera changes.

**Patterns to follow:**
- Zustand `subscribeWithSelector` middleware.
- tldraw signal/atom pattern.

**Test scenarios:**
- Happy path: SSE event fires `applyEvent` → `leafSlots` mutates → camera subscribers do NOT re-render.
- Edge case: zoom changes via wheel → camera subscribers DO re-render exactly once per `cameraSettled` event.
- Integration: test harness with synthetic SSE + zoom events; assert subscriber call counts exactly.

**Verification:**
- U1 selector trace shows zero camera-subscriber fires during a 60 s SSE-only session (no user gestures).
- U1 selector trace shows exactly one `cameraSettled` fire per pan/zoom gesture-end.

---

- U8. **Cause D fix — Turbopack HMR singleton guards + production-doc** *(only if U4 selects)*

**Goal:** If the symptom is dev-only HMR-induced, harden module-level singletons against Fast Refresh re-init and document the prod-build verification step so future regressions don't burn investigation hours.

**Requirements:** R3, R5

**Dependencies:** U4 selects D

**Files:**
- Modify: `app/atlas/_lib/leafcanvas-v2/gesture-tracker.ts` (cache `canvasGestureTracker` on `globalThis`)
- Modify: `app/atlas/_lib/leafcanvas-v2/idle-tracker.ts` (same pattern)
- Modify: `app/atlas/_lib/leafcanvas-v2/fetch-queue.ts` (same pattern)
- Modify: `app/atlas/_lib/leafcanvas-v2/leaf-zoom-signal.ts` (cache live + settled state on `globalThis`)
- Create: `docs/solutions/2026-05-06-turbopack-hmr-canvas.md` (operational runbook — repro `pnpm build && pnpm start`, expected behaviors, known Turbopack issues to track)

**Approach:**
- Idiom: `const _g = globalThis as unknown as { __canvasGestureTracker?: GestureTracker }; export const canvasGestureTracker = _g.__canvasGestureTracker ??= new GestureTracker();` — survives Fast Refresh because globals don't get re-initialized.
- Document the hot-reload sensitivity inside each singleton's header so future contributors understand why the indirection is there.

**Patterns to follow:**
- Vercel docs on persisting state across HMR.
- Existing `hmr-safe-singleton` patterns in popular React libs (xyflow, react-three-fiber).

**Test scenarios:**
- Happy path: in dev, edit `leafcanvas.tsx`, save → Fast Refresh — singleton state (e.g. `gesturingFlag`) is preserved.
- Edge case: in dev, edit `gesture-tracker.ts` itself (the singleton's own module), save → singleton IS re-initialized (the module changed). Verify no orphaned listeners.
- Integration: production-build run (`pnpm build && pnpm start`) — singleton instances do not change identity across renders, no globalThis pollution warnings.

**Verification:**
- The original symptom does not reproduce after editing other files in dev.
- `docs/solutions/2026-05-06-turbopack-hmr-canvas.md` exists with the prod-build repro recipe and a list of known Turbopack issues to monitor.

---

## System-Wide Impact

- **Interaction graph:** the diagnostic surface (U1) touches the React render tree of every `LeafCanvas` and `LeafFrameRenderer`; trace logs go to `console.log` only. No effect when `__CANVAS_LOG` is off (fast-path returns).
- **Error propagation:** selector traces and store-subscribe traces wrap their bodies in try/catch — a single bad consumer must not sink the trace.
- **State lifecycle risks:** if U6 (IO refire) ships, ensure the cleanup path doesn't double-disconnect observers when both the gesture-end subscriber and the unmount cleanup run. Existing `pendingWarm`/`pendingHot` closure pattern from CZ4 already handles this for the deferred case; mirror it for re-observe.
- **API surface parity:** none — diagnostic surface is internal; fix units modify existing components in place.
- **Integration coverage:** the fix unit's test scenarios (U5/U6/U7/U8) all include integration scenarios that exercise the full SSE → store → render path with real React + a fake EventSource.
- **Unchanged invariants:**
  - `canvasFetchQueue` priority semantics — not touched.
  - `unwrapCanonicalTree` envelope handling — not touched.
  - `useImageRefs` / `useIconClusterURLs` HTTP cycle — only the *trigger* (warm gate) is reconsidered, not the fetch itself.
  - The CZ1–CZ6 work is preserved unless U4 specifically diagnoses one of them as the cause.

---

## Risks & Dependencies

| Risk | Mitigation |
|------|------------|
| U2 captures inconclusive evidence (multiple candidate causes look possible) | U4 picks the one with the highest blast-radius reduction; remaining candidates remain instrumented and become follow-up plans only if symptom returns. |
| The IO re-observe (U6 Tier 1) doesn't fully resolve because the symptom is partially Cause C as well | Tier 2 of U6 (camera-driven culler) covers both: it removes IO entirely AND decouples viewport tracking from store updates. The plan permits escalating from Tier 1 to Tier 2 within U6 without re-planning. |
| `react-scan` adds noticeable dev cost | Mounted only behind `__CANVAS_LOG=true`; default-off. Production build never imports it. |
| `globalThis` singletons (U8) leak across tests | Tests reset via `beforeEach(() => { delete (globalThis as any).__canvasGestureTracker; ... })`; same pattern xyflow uses. |
| Production build differs from dev in ways that mask the bug elsewhere | U2 explicitly captures both. If they diverge, that's evidence by itself (Cause D). |

---

## Documentation / Operational Notes

- After U4 picks, update `docs/solutions/2026-05-06-canvas-refresh-evidence.md` so the next reader knows what was diagnosed AND what was instrumented but not shipped.
- After the fix lands, update `docs/plans/2026-05-06-001-perf-canvas-v2-design-brain-borrow-plan.md` if the diagnosed cause invalidates one of its prior assumptions.
- Add a short note in `AGENTS.md` under "Canvas debugging" pointing at `__CANVAS_LOG` and the production-build verification step. Future regressions become a 5-minute triage instead of a 12-hour debug.

---

## Sources & References

### Origin Capture (this session)

- Headless audit script: `/tmp/canvas_audit.mjs` (Playwright + `__CANVAS_LOG=true`).
- Audit log artifacts: `/tmp/audit_remount.log` (mounts=2, unmounts=1, stable for 50 s).
- 50-second observation timeline: `T+3s mounts=0`, `T+8s mounts=2 unmounts=1`, `T+15s/T+25s/T+35s/T+50s mounts=2 unmounts=1` — confirms no remount cycle.

### Code references

- `app/atlas/_lib/leafcanvas.tsx` (camera ref + RAF flush + auto-fit).
- `app/atlas/_lib/leafcanvas-v2/LeafFrameRenderer.tsx` (IO setup, tree-resolution, deferred-mount).
- `app/atlas/_lib/leafcanvas-v2/gesture-tracker.ts` (CZ1).
- `app/atlas/_lib/leafcanvas-v2/leaf-zoom-signal.ts` (CZ2).
- `app/atlas/_lib/leafcanvas-v2/canvas-log.ts` (CZ6 diagnostic surface).
- `app/atlas/_lib/AtlasShell.tsx` lines 128–133, 200–203, 208 (`remountKey`, `flowsSignature`, 60 s `refreshBrain` interval).
- `app/atlas/_lib/AtlasShellInner.tsx` line 283 (`slotVersion = leafSlots[leafID]?.loadedAt`).
- `lib/atlas/live-store.ts` lines 340–360 (`refreshBrain` rebuild), 449 (`loadedAt` set), 763–836 (`applyEvent`).

### External research (2026-05-06 dispatch)

- React Scan — https://github.com/aidenybai/react-scan
- Why Did You Render — https://github.com/welldone-software/why-did-you-render
- React Profiler API — https://react.dev/reference/react/Profiler
- React 19 Performance tracks — https://react.dev/reference/dev-tools/react-performance-tracks
- Zustand `useShallow` — https://zustand.docs.pmnd.rs/learn/guides/prevent-rerenders-with-use-shallow
- Zustand re-renders discussion #2642 — https://github.com/pmndrs/zustand/discussions/2642
- xyflow PR #5629 (real-world `useShallow` rollout) — https://github.com/xyflow/xyflow/pull/5629
- React `useSyncExternalStore` docs — https://react.dev/reference/react/useSyncExternalStore
- Zustand `getSnapshot` caching discussion #1936 — https://github.com/pmndrs/zustand/discussions/1936
- nico.fyi: Be careful with `useSyncExternalStore` — https://www.nico.fyi/blog/be-careful-with-usesyncexternalstore
- **Mozilla bug 1419339 — IO + transform animations** — https://bugzilla.mozilla.org/show_bug.cgi?id=1419339
- **WebKit bug 209264 — IO + zooming** — https://bugs.webkit.org/show_bug.cgi?id=209264
- tldraw camera system — https://tldraw.dev/sdk-features/camera
- tldraw performance — https://tldraw.dev/sdk-features/performance
- React Flow hooks API — https://reactflow.dev/api-reference/hooks
- MDN `content-visibility` — https://developer.mozilla.org/en-US/docs/Web/CSS/Reference/Properties/content-visibility
- DebugBear: `content-visibility` — https://www.debugbear.com/blog/content-visibility-api
- React `useTransition` — https://react.dev/reference/react/useTransition
- Turbopack Fast Refresh issue #89530 — https://github.com/vercel/next.js/issues/89530
- Turbopack 16.2 HMR regression #91768 — https://github.com/vercel/next.js/issues/91768
- Next.js 16 breaking changes — https://www.dharmsy.com/blog/nextjs-16-update-common-issues-and-fixes
