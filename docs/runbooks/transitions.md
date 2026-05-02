# Atlas + Projects — Per-Page Transition Audit

> **Filed:** 2026-05-02 (U13 of phase-4 Atlas integration plan).
> **Trigger:** User feedback — *"once i click on the node the transitions is
> senseless you need to research each pages entry and exit at different page
> states etc."*
>
> This document audits every page entry, exit, and intra-page state change in
> the atlas + projects shell against the brainstorm's R27 spec ("shared
> three.js + r3f render pipeline … transitions between mind graph and project
> view are shared-element morphs at ~600ms"). It is the canonical reference
> for the motion grammar; pair with `lib/animations/conventions.md` (three-tool
> grammar) and `lib/animations/easings.ts` (canonical curve constants).

## How to read this doc

Each row covers one transition or intra-page state change with five fields:

- **Current** — what the code actually does today (curve + duration +
  reduced-motion behaviour).
- **Spec** — what the brainstorm + R27 + the three-tool conventions imply
  the transition should do.
- **Gap** — the delta between Current and Spec, if any.
- **Fix** — recommended action, sized in person-hours. `0h` = aligned
  already, no work needed. `defer` = followup task ID.
- **Manual verification** — operator-runnable checklist line.

A "✓ aligned" row is *not* dead weight — re-running this audit against future
changes is faster when the alignment is captured here, so a future regression
shows up as a Current-vs-doc mismatch.

## Conventions cheat-sheet

Curve language (from `lib/animations/easings.ts`):

| Constant            | GSAP string         | Use                                 |
|---------------------|---------------------|-------------------------------------|
| `EASE_PAGE_OPEN`    | `expo.out`          | Page-load reveal — snappy           |
| `EASE_TAB_SWITCH`   | `cubic.inOut`       | Tab swap (symmetrical)              |
| `EASE_HOVER`        | `back.out(1.2)`     | Hover micro-interactions            |
| `EASE_THEME_TOGGLE` | `cubic.out`         | Theme crossfade / soothing fade     |
| `EASE_DOLLY`        | `expo.inOut`        | Camera dolly / cinematic            |
| `EASE_TYPE_ON`      | `none`              | Type-on / character-stagger reveal  |

Duration buckets (R27-implied + cross-checked against current code):

- Cinematic camera moves / leaf morph / build-ups: **600–800ms**
- Page-shell entrance choreography: **600–900ms** total (staggered)
- Tab switches: **150–200ms**
- Data-row fades / chip toggles / lifecycle row exits: **200–250ms**
- Outline pulse / arrival flash: **600ms** (one cycle)

Reduced-motion contract: **every** transition must collapse to its final
state instantly under `prefers-reduced-motion: reduce`. The doc flags any
that don't.

---

## 1. `/atlas` mount — initial paint

**Files:** `app/atlas/page.tsx` → `app/atlas/BrainGraph.tsx`,
`app/atlas/SignalAnimationLayer.tsx`, `lib/animations/timelines/atlasBloomBuildUp.ts`
(NOTE: `atlasBloomBuildUp` runs in `components/projects/atlas/AtlasCanvas.tsx`,
NOT on the /atlas BrainGraph mount — see Gap below).

- **Current:**
  - Page is `dynamic({ ssr: false })`-imported → during the chunk fetch a
    radial-gradient skeleton with a pulse keyframe (2s ease-in-out, looping)
    fills the viewport. No bloom build-up runs on /atlas itself.
  - Once `BrainGraph` mounts, three.js + react-force-graph-3d hydrate
    synchronously; the bloom pass is added once via
    `composer.addPass(new UnrealBloomPass(...))` with **static** params
    (threshold 1.0, strength 1.2, radius 0.85). No tween in.
  - Camera position is the library default (z=600). No mount dolly.
  - Organic node-drift loop starts immediately via `useFrame`-equivalent
    `requestAnimationFrame` (sin-based y-position perturbation, amplitude 0.6,
    frequency 0.5 rad/s).
  - Edge-pulse uniforms (`advancePulseTime`) tick continuously regardless of
    interaction state.
- **Spec (R27 + brainstorm):** "shared three.js render pipeline … cinematic
  build-up to atlas". The brainstorm calls for a *bloom-arrival* feel on
  initial paint of the brain graph — not a hard cut.
- **Gap:** The `atlasBloomBuildUp` timeline (intensity 0→0.5, threshold
  1.0→0.7, chroma 0→0.0008 over 800ms, `EASE_PAGE_OPEN`) **only runs in the
  ProjectShell-embedded AtlasCanvas, never in the /atlas BrainGraph**.
  /atlas hard-cuts to bloomed pass at full strength. This is one plausible
  cause of the user's "senseless transitions" complaint — the brain graph
  appears with no arrival cue, then leaf clicks trigger spring-driven dollies
  that DO have arrival physics, so the leaves feel more alive than the brain
  itself.
- **Fix:** ~3h. Wire `atlasBloomBuildUp` into BrainGraph's bloom-pass effect
  so the initial paint plays the same 800ms intensity ramp as
  AtlasCanvas. Out of scope for U13 (this is a new transition, not a
  swap-to-canonical edit). **Defer to U14** — followup task.
- **Manual verification:** Open `/atlas` from a cold reload. Expect: bloom
  starts dim (~0 intensity) and ramps to full glow over ~800ms. Today it
  hard-cuts. Confirm DevTools Performance trace stays > 55fps during the
  ramp.

## 2. `/atlas` → `/projects/[slug]` — leaf morph (forward)

**Files:** `app/atlas/view-transitions.css`, `app/atlas/LeafMorphHandoff.tsx`,
`components/projects/ProjectToolbar.tsx` (the destination title bar carries
the matching `view-transition-name`).

- **Current:**
  - View Transitions API: 600ms `cubic-bezier(0.87, 0, 0.13, 1)` (= `expo.inOut`
    = `EASE_DOLLY`) on `::view-transition-old(*)`, `::view-transition-new(*)`,
    `::view-transition-group(*)`.
  - Reduced-motion: `animation-duration: 0s` collapses to instant route swap.
  - Browser fallback (Firefox): no-op, plain route swap.
  - Spatial-continuity cue under reduced-motion / unsupported: static
    breadcrumb (`/atlas › <flow name>`) on ProjectToolbar.
- **Spec:** R27 → "shared-element morphs at ~600ms". `EASE_DOLLY` for
  cinematic feel. ✓
- **Gap:** None — this is the canonical morph. The CSS comment explicitly
  documents the cubic-bezier as the `expo.inOut` / `EASE_DOLLY` equivalent.
- **Fix:** 0h.
- **Manual verification:** Open `/atlas`, click a flow leaf. Expect: title
  morphs smoothly into the project toolbar position over 600ms, no flash, no
  jank. Toggle `prefers-reduced-motion` and repeat — instant swap, no morph.

## 3. `/projects/[slug]` mount — project shell open

**Files:** `lib/animations/timelines/projectShellOpen.ts`,
`components/projects/ProjectShell.tsx`.

- **Current:** GSAP timeline with these stagger points and curves (all already
  using canonical constants):
  - 100ms: toolbar `opacity 0→1, y -12→0` 400ms `EASE_PAGE_OPEN`
  - 300ms: atlas-canvas `opacity 0→1` 500ms `EASE_THEME_TOGGLE`
  - 300ms: atlas-frames stagger `opacity+y` `EASE_HOVER` (`back.out(1.2)`),
    per-frame 80ms clamped to 600ms total
  - 500ms: tab-strip 400ms `EASE_THEME_TOGGLE`
  - 600ms: tab-content 300ms `EASE_THEME_TOGGLE`
  - Total ~900ms.
  - Reduced-motion: every target `gsap.set` to final state, empty paused
    timeline returned.
- **Spec:** Cinematic but not slow (R27 "~600ms" budget should refer to
  individual moves; the *staggered* total of 900ms is acceptable as long as
  no single move exceeds the budget). Toolbar 400ms, atlas 500ms, tabs
  300ms — all under 800ms ✓.
- **Gap:** None — U10's audit reported this file 100% canonical-easings,
  confirmed by re-reading the file in U13.
- **Fix:** 0h.
- **Manual verification:** Cold-load `/projects/<any-slug>`. Expect: toolbar
  drops in first, atlas fades + frames bounce in (back.out overshoot
  visible), tab strip slides up, tab content fades. No element appears
  before 100ms; total settles by ~900ms.

## 4. `/projects/[slug]` → `/atlas` — Esc reverse-morph

**Files:** `components/projects/ProjectShell.tsx` (Esc handler →
`router.back()`), browser View Transitions API (the *same* CSS from #2 plays
the morph in reverse direction automatically), `app/atlas/page.tsx` reads
`?from=<slug>` and dispatches `view.morphFromProject` to re-focus the source
leaf at the correct zoom level.

- **Current:** Same 600ms `EASE_DOLLY` curve as forward morph (CSS doesn't
  branch on direction; the browser computes new = atlas, old = project view
  for the back navigation). Reduced-motion collapses to instant.
- **Spec:** Symmetric reverse morph at the same duration as forward — ✓.
- **Gap:** None.
- **Fix:** 0h.
- **Manual verification:** From `/projects/<slug>`, press Escape. Expect:
  same 600ms morph, this time the project toolbar title shrinks back into
  the leaf position and the brain re-focuses on that leaf. No flash, no
  re-paint.

## 5. Tab switch within `/projects/[slug]`

**Files:** `lib/animations/timelines/tabSwitch.ts`,
`components/projects/ProjectShell.tsx` (`useEffect` watching `activeTab`).

- **Current:**
  - Outgoing: `opacity 1→0, y 0→-12` 150ms `EASE_TAB_SWITCH` (`cubic.inOut`)
  - Incoming: `opacity 0→1, y 12→0` 180ms `EASE_TAB_SWITCH`, starts at
    `DURATION_OUT * 0.5 = 75ms` (slight overlap → "single curtain" feel)
  - Reduced-motion: outgoing snapped hidden, incoming snapped visible.
- **Spec:** Tab swaps 150–200ms with a symmetric curve so out + in feel
  paired. ✓
- **Gap (sequencing):** ProjectShell's `useEffect` runs `tabSwitch(null,
  incoming)` — passing `null` for outgoing because the same DOM node is
  reused for both tabs (only one tab is rendered at a time). This means the
  outgoing fade-up never plays — only the incoming fade-in does. The
  outgoing tab is unmounted *before* the timeline starts (React unmounts the
  previous tab synchronously when `activeTab` changes; the
  `useEffect`-scheduled `tabSwitch` then runs against an already-empty
  outgoing slot). The "curtain wipe" promise of `tabSwitch.ts` is therefore
  never realised — the user sees a hard cut on the outgoing tab and only an
  incoming fade.
  - This is **the second plausible match for the user's "senseless"
    complaint**. The outgoing fade is dead code; only the incoming
    plays, so each tab swap looks like *content disappearing then
    fading in* rather than a paired transition.
- **Fix:** Two options, both larger than a swap-to-constant:
  - (a) ~2h — mount both tabs simultaneously during the swap (render the
    outgoing tab as a portal-anchored ghost while the incoming tab fades
    in). Risk: heavy tab content (DRD editor, JSON tab) doubles work
    during the 200ms.
  - (b) ~30min — accept the current behaviour and shorten the incoming-only
    timeline to 200ms total (currently 180 + 75 overlap = 255ms with no
    outgoing visible).
  - **Defer to U14**. Out of scope for U13.
- **Manual verification:** Open `/projects/<slug>`, click DRD → Violations →
  Decisions → JSON. Expect: each swap should feel like a paired curtain.
  Today it feels like a hard cut + incoming fade. Confirm by enabling
  prefers-reduced-motion — the difference between motion / no-motion should
  be subtle (since outgoing was never animated, only the incoming snap is
  removed).

## 6. Theme toggle

**Files:** `lib/animations/timelines/themeToggle.ts`,
`components/projects/ProjectToolbar.tsx`,
`components/projects/atlas/AtlasCanvas.tsx` (re-runs `atlasBloomBuildUp` on
theme change).

- **Current:**
  - JSON-tab bound chips pulse: scale 1→1.06→1, opacity 1→0.85→1, total
    ~400ms `EASE_THEME_TOGGLE` (`cubic.out`), per-chip stagger 0.12 amount.
  - Atlas postprocessing re-builds via `atlasBloomBuildUp` 800ms
    `EASE_PAGE_OPEN`.
  - DOM token swap: `document.documentElement.setAttribute('data-theme',
    concrete)` is synchronous — CSS-variable consumers cross-fade per
    whatever local CSS they declared (no global override).
  - Reduced-motion: timeline no-ops (chip already shows new bound value via
    React re-render). Bloom build-up runs in instant mode (final values set
    on state directly).
- **Spec:** Soothing crossfade ~400ms; atlas re-bloom is the cinematic
  flourish. ✓
- **Gap:** No timeline gaps; minor consistency note — the chip pulse curve
  and the bloom rebuild use different curves (`cubic.out` vs `expo.out`)
  intentionally because they're different intent (chip = settle-back, bloom
  = arrival). Documented here so a future "unify them" change knows the
  rationale.
- **Fix:** 0h.
- **Manual verification:** Open `/projects/<slug>`, click theme toggle.
  Expect: token chip(s) on JSON tab pulse and settle in ~400ms; atlas
  bloom dims and re-ramps in ~800ms. Toggle reduced-motion → both
  collapse to instant.

## 7. `/components` mount — horizontal canvas

**Files:** `app/components/page.tsx`, `components/ComponentCanvas.tsx`.

- **Current:** No GSAP timeline, no react-spring, no `useFrame`. Pan / zoom
  is wheel + space-drag handled in vanilla `onWheel` / `onPointerDown` →
  `transform: translate3d(...) scale(...)` writes. Inspector overlay slide-in
  is a CSS `transition: transform 220ms ease, opacity 220ms ease` (no
  `cubic-bezier` and no canonical-constant routing — this is a `<style jsx>`
  CSS surface).
- **Spec:** Horizontal canvas browser is a designer-tool surface; the
  brainstorm's R27 doesn't strongly constrain non-graph routes. The 220ms
  duration is in the data-row bucket which is fine.
- **Gap:** Inline `220ms ease` is a code smell against the conventions doc
  but: (a) it's CSS not GSAP, so the canonical-constants don't directly
  apply — `easings.ts` exports GSAP-string names, not CSS bezier strings,
  and (b) there's no equivalent `EASE_*` for "data-row UI affordance" right
  now. Followup task: extend `easings.ts` with CSS-string twins so style
  blocks can call them.
- **Fix:** ~2h to extend `easings.ts` + add `--ease-tab-switch`,
  `--ease-page-open` etc. CSS variables and migrate ComponentCanvas /
  inbox / violations chips to use them. **Defer to U14**.
- **Manual verification:** Open `/components`. Pan with trackpad and click
  a component. Expect: smooth ~220ms slide-in of inspector overlay. No
  jank.

## 8. `/inbox` mount + filter changes + lifecycle row fade

**Files:** `components/inbox/InboxShell.tsx`, `components/inbox/InboxRow.tsx`,
`components/inbox/InboxFilters.tsx`, `components/inbox/BulkActionBar.tsx`.

- **Current:**
  - Mount: no GSAP timeline. Initial render shows whatever EmptyState /
    `<ul>` is in scope. EmptyState component owns its own 320ms +
    `EASE_PAGE_OPEN` stagger (after U13's edit — was inline `expo.out`).
  - Filter changes: `useEffect` re-fetches; the list re-renders. No
    inter-state animation between the loading skeleton and the populated
    list.
  - Lifecycle row exit (Acknowledge / Dismiss): inline CSS
    `transition: opacity 220ms ease, transform 220ms ease` on the row,
    triggered by toggling a `fadingOut` set. Reduced-motion: not respected
    explicitly — the user still sees the 220ms fade because the CSS
    transition doesn't gate on `prefers-reduced-motion` itself. (The
    transform distance is 12px, so it's not severe, but it's a contract
    miss.)
  - Filter chip toggles: `transition: background 150ms ease, border-color
    150ms ease` — no reduced-motion gate.
  - Bulk-action bar slide-in: `transition: background 150ms ease` (the bar
    appears via React mount, not transition — the inline transition is for
    button hover states only).
- **Spec:** Filter changes ≤ 200ms; row exits ≤ 250ms; reduced-motion
  collapses everything. Mount can be plain (no requirement to animate
  every list).
- **Gap:**
  - **Reduced-motion violation #1**: InboxRow lifecycle fade is 220ms
    regardless of preference. Should respect `useReducedMotion()` and
    snap-remove instead.
  - **Reduced-motion violation #2**: Filter chip background / border
    transitions also fire under reduced-motion. Less severe (background
    color flip is sub-perceptual), but still a contract miss.
  - **Inline easing #1**: All four files use `transition: …ease` — no
    canonical-constant routing because we don't have CSS-twin constants
    yet (see #7 Gap).
- **Fix:**
  - Reduced-motion gates on InboxRow + chip transitions: ~1h. **Defer to
    U14**.
  - CSS-twin constants in `easings.ts`: ~2h, same followup as #7.
- **Manual verification:** Open `/inbox`. Toggle a severity chip → quick
  ~150ms color flip. Acknowledge a row → 220ms fade-out + 12px slide.
  Toggle reduced-motion and repeat — *expect* instant chip flip + instant
  row removal; *actual* still animated, contract miss.

## 9. DRD tab content load

**Files:** `components/projects/tabs/DRDTab.tsx`,
`components/projects/tabs/DRDTabCollab.tsx`.

- **Current:**
  - On mount: `fetchDRD` runs in `useEffect`. Until it returns, no editor
    is rendered (the BlockNote `<BlockNoteView>` doesn't appear until
    `loaded === true`). No skeleton, no fade-in — hard mount once data
    lands.
  - BlockNote owns its own internal animations (caret blink, slash-menu
    open/close, block hover affordances). Those use BlockNote's defaults
    which are out of scope for our motion grammar.
  - The collab variant (Yjs + Hocuspocus) adds a presence cursor color
    fade that's owned by Yjs's CursorAwareness extension — also out of
    scope.
- **Spec:** Tab content load — the parent `tabSwitch` handles the visual
  swap; once the tab is visible the content can hard-paint. The DRD's own
  load is a data fetch, not a transition.
- **Gap:** None directly. But because of the tab-switch sequencing issue
  (#5), if the user clicks DRD → Violations → DRD quickly, they see two
  hard cuts because the in-flight fetch on the second DRD mount stalls
  the visible content while the incoming tab-fade has already completed.
  The fix lives in #5; this row just records the cross-impact.
- **Fix:** 0h directly. Inherits the U14 followup from #5.
- **Manual verification:** Open `/projects/<slug>` → DRD tab on a flow
  with content. Expect: tab strip swaps, tab content area fades in via
  parent tabSwitch, then editor appears once data loads. The DRD load
  itself doesn't animate.

## 10. Violations tab — filter chip toggling + lifecycle row fade

**Files:** `components/projects/tabs/ViolationsTab.tsx`,
`components/projects/tabs/violations/CategoryFilterChips.tsx`,
`components/projects/tabs/violations/PersonaFilterChips.tsx`,
`components/projects/tabs/violations/ThemeFilterChips.tsx`,
`components/projects/tabs/violations/LifecycleButtons.tsx`.

- **Current:**
  - Row arrival on data load: GSAP `gsap.from(rows, { opacity:0, y:6, duration:
    0.32, ease: EASE_PAGE_OPEN, stagger })` (after U13 edit; was inline
    `expo.out`). Per-row stagger clamped to 600ms total.
  - New-arrival flash on SSE-driven re-fetch: GSAP `gsap.fromTo` from
    accent-mix background to transparent, 600ms `EASE_THEME_TOGGLE` (after
    U13 edit; was inline `cubic.out`).
  - Filter chips: `transition: background 200ms cubic-bezier(0.34, 1.56,
    0.64, 1), border-color 160ms ease` (inline CSS, three files share the
    same string). The bezier is approximately `back.out(~1.2)` — already
    visually equivalent to `EASE_HOVER`, but expressed as a literal.
  - Highlighted-violation outline pulse: `transition: opacity 220ms ease,
    transform 220ms ease, outline-color 200ms ease, outline-width 200ms ease`
    (inline CSS).
  - Lifecycle row resolved (Acknowledge / Dismiss): adds id to `resolvedSet`,
    row is *filtered out* of render — no fade animation, hard removal.
    (Different behaviour from `/inbox` which fades 220ms.)
  - Reduced-motion: stagger + flash skip. Outline pulse + chip CSS
    transitions still fire (contract miss, same as #8).
- **Spec:** Row arrival 250–400ms ≤ 600ms total stagger ✓. Chip toggle
  200ms ✓. Outline pulse 200ms each ✓.
- **Gap:**
  - **Reduced-motion violation #3**: chip CSS transitions and outline
    pulse don't respect `prefers-reduced-motion`. Same fix shape as #8.
  - **Behavioural inconsistency**: lifecycle resolution hard-removes the
    row while `/inbox` fades it. Same data, two different exits. Not
    necessarily wrong — the violations tab is read-mostly while inbox is
    triage, so a slower exit on inbox is defensible — but document the
    intent so future dev doesn't "fix" the wrong one.
  - **Inline easing #2**: chip `cubic-bezier(0.34, 1.56, 0.64, 1)` is
    repeated across three filter chip files. Could be a CSS variable.
- **Fix:**
  - Reduced-motion gates: ~30min. **Defer to U14**.
  - CSS-twin constants for chip bezier: covered by U14 followup #7/#8.
  - Document inbox-vs-violations exit-behaviour intent: covered here.
- **Manual verification:** Open `/projects/<slug>` → Violations. Expect:
  rows fade in with stagger ≤ 600ms total. Toggle a category chip →
  ~200ms back-out background flip. Acknowledge a row → instant disappear
  (no fade). Trigger an audit re-run → newly-arriving rows flash accent
  color and fade to transparent over 600ms.

---

## Cross-cutting findings

### A. Duration consistency

All durations land in their expected buckets:

- Cinematic / camera moves: 600–800ms ✓ (leaf morph 600ms, bloom build-up
  800ms, AtlasCanvas spring ~650ms perceived).
- Page-shell entrance: 900ms total ✓ (no single move > 500ms).
- Tab switches: 150–180ms ✓ (but see #5 sequencing).
- Data row fades: 220–320ms ✓.
- Outline pulse: 200ms each leg ✓.

### B. Easing consistency

After U13's swap-to-canonical edits there are **zero remaining inline
GSAP easing strings** in `components/`, `app/`, or `lib/`. (Confirmed via
`grep -rn -E '"(expo|cubic|back|quint|power[0-9]|elastic|bounce|sine)\.[a-zA-Z]+`
returning empty.)

CSS easing strings (`220ms ease`, `cubic-bezier(...)`) remain in 8 places
across `components/inbox/` + `components/projects/tabs/violations/` chip
files. These don't have a canonical-constant routing path today because
`easings.ts` only exports GSAP strings. Followup task: ship CSS-twin
constants (CSS variables under `--ease-*`) and migrate the inline strings.

### C. Reduced-motion respect

| Transition                          | Honors reduced-motion? |
|-------------------------------------|------------------------|
| `/atlas` initial paint              | n/a (fallback page)    |
| Leaf morph forward                  | ✓ (CSS gates)          |
| Project shell open                  | ✓ (timeline gates)     |
| Reverse morph (Esc)                 | ✓                      |
| Tab switch                          | ✓                      |
| Theme toggle (chip pulse)           | ✓                      |
| Theme toggle (bloom rebuild)        | ✓                      |
| `/components` inspector slide-in    | ✗ (CSS, no gate)       |
| `/inbox` row exit                   | ✗ (CSS, no gate)       |
| `/inbox` chip toggle                | ✗ (CSS, no gate)       |
| Violations row stagger              | ✓                      |
| Violations new-arrival flash        | ✓                      |
| Violations chip toggle              | ✗ (CSS, no gate)       |
| Violations outline pulse            | ✗ (CSS, no gate)       |

Four contract misses, all CSS-driven, all rooted in the missing CSS-twin
constants. Single followup fix.

### D. "Senseless transitions" — the user's complaint

The two most plausible matches for the user's complaint:

1. **/atlas mount has no bloom build-up** while AtlasCanvas (project view)
   does. The brain hard-cuts to full glow; subsequent leaf-click springs
   feel "alive". The contrast is what makes the brain feel mechanical.
   Fix: wire `atlasBloomBuildUp` into BrainGraph. ~3h. (Section 1.)
2. **Tab switch outgoing-fade is dead code** because React unmounts the
   previous tab synchronously before the timeline runs. Each tab swap
   looks like a hard cut + incoming fade rather than a paired curtain.
   Fix: ~2h either render outgoing as a portal ghost or shorten timeline.
   (Section 5.)

Both are deferred to U14 — they are new transition behaviour, outside
U13's swap-to-canonical scope.

### E. Cross-reference: morph CSS budget vs `morphingNode` auto-clear

U2b's CSS sets a 600ms duration. U4's `morphingNode` clear was specified
as "300ms defer + 800ms backstop" in the plan. The 800ms backstop is
strictly larger than the 600ms CSS duration, so the morph completes
before the auto-clear fires. ✓ aligned.

If a future change extends the View Transition duration past 800ms, the
backstop must lift to keep the source-leaf snapshot alive long enough.

---

## Followup tasks (deferred from U13)

| ID  | Description                                              | Estimate |
|-----|----------------------------------------------------------|----------|
| U14a | Wire `atlasBloomBuildUp` into BrainGraph initial paint  | 3h       |
| U14b | Tab-switch sequencing: portal-ghost outgoing OR shorten | 2h       |
| U14c | CSS-twin easing constants + migrate inline transitions  | 2h       |
| U14d | Reduced-motion gates on 4 CSS transitions               | 1h       |
| U14e | Document inbox-vs-violations exit-behaviour intent      | 0.5h     |

Total deferred: ~8.5h of motion polish, captured here so they don't
silently rot.

## Edits landed in U13

| File                                          | Change                                       |
|-----------------------------------------------|----------------------------------------------|
| `components/projects/tabs/ViolationsTab.tsx`  | Inline `expo.out` → `EASE_PAGE_OPEN`         |
| `components/projects/tabs/ViolationsTab.tsx`  | Inline `cubic.out` → `EASE_THEME_TOGGLE`     |
| `components/empty-state/EmptyState.tsx`       | Inline `expo.out` → `EASE_PAGE_OPEN`         |
| `docs/runbooks/transitions.md`                | This file (new)                              |

No animation behaviour changed — three string-equivalent swaps to lift the
canonical-constant invariant. `npx tsc --noEmit` clean post-edit.
