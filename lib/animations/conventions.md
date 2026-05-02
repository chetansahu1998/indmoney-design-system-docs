# Atlas Motion Grammar — three tools, three jobs

> Codified in U10. If you're touching motion code, read this *first*. Picking
> the wrong tool is the difference between motion that feels alive and motion
> that feels mechanical.

The atlas + projects shell uses three motion libraries in production. Each
has a distinct feel and a distinct job. **Don't pick the same tool for two of
these jobs — each has a different feel.**

## 1. `@react-spring/three` — triggered 3D transitions

Use for any motion in the WebGL scene that's kicked off by a discrete event
and should feel *physical* (decelerate naturally, settle with a slight
overshoot, respond to interruption gracefully).

Examples in this codebase:

- Hover scale on a graph node.
- Click-to-zoom camera dolly (atlas canvas, brain graph).
- Particle-pool convergence on click-and-hold (`SignalAnimationLayer`).
- Route-triggered camera moves.

Canonical configs:

- Camera dolly: `{ tension: 170, friction: 26 }` (react-spring default —
  feels like a mass-spring system with realistic damping).
- Particle convergence: `{ tension: 200, friction: 30 }` (a touch stiffer
  + more damped so particles don't oscillate around the target).
- Hover micro-scale: `{ tension: 300, friction: 22 }` (snappier, slight
  overshoot — comparable to GSAP's `back.out(1.2)`).

### Imperative pattern (preferred for `react-force-graph-3d`)

The library owns the camera mount, so we *don't* wrap it in
`<animated.perspectiveCamera>`. Instead, use the imperative spring API:

```ts
const [, api] = useSpring(() => ({
  px: 0, py: 0, pz: 0, lx: 0, ly: 0, lz: 0,
  config: { tension: 170, friction: 26 },
  onChange: ({ value }) => {
    const cam = fgRef.current?.camera();
    if (!cam) return;
    cam.position.set(value.px, value.py, value.pz);
    cam.lookAt(value.lx, value.ly, value.lz);
  },
}));
api.start({ px: tx, py: ty, pz: tz, lx: lookX, ly: lookY, lz: lookZ });
```

Reduced-motion: skip the animation by calling `api.set(...)` instead of
`api.start({ ... })`. `useReducedMotion()` from `lib/animations/context.ts`
is the canonical hook.

## 2. GSAP — sequenced DOM choreography

Use for choreographed *DOM* timelines where multiple targets need to fire
in a deliberate rhythm and the developer wants timeline scrubbing,
labels, and overlap control.

Examples in this codebase:

- Project shell entrance (`projectShellOpen`).
- Tab switch out→in handoff (`tabSwitch`).
- Theme toggle crossfade (`themeToggle`).
- Atlas bloom build-up (`atlasBloomBuildUp`).

Reach for the named easing constants in `easings.ts` (`EASE_PAGE_OPEN`,
`EASE_TAB_SWITCH`, `EASE_HOVER`, `EASE_THEME_TOGGLE`, `EASE_DOLLY`).
Inline easing strings in timelines are a code smell — alias them via
the constants so the curve grammar stays uniform.

GSAP is *not* the right choice for a single 3D camera tween. Springs feel
better and don't require keeping a parallel transition contract with the
scene-graph library.

## 3. `useFrame` + `MathUtils.damp` — ambient / constant-velocity motion

Use for motion that has no clear start or end — drift, sway, ambient
pulses driven by uniform tick. Springs are wrong for this because they
want a target; ambient motion has no target, just a velocity field.

Examples in this codebase:

- Organic node drift (`BrainGraph` y-position sine wave).
- Edge-pulse shader uniform tick (`edgePulseShader.ts`).
- Camera idle bob (if/when added).

Pattern:

```ts
useFrame((state, delta) => {
  obj.position.y = THREE.MathUtils.damp(obj.position.y, target, 5, delta);
});
```

Or the existing pattern of advancing a time uniform:

```ts
const start = performance.now();
useEffect(() => {
  const tick = () => {
    advancePulseTime((performance.now() - start) / 1000);
    raf = requestAnimationFrame(tick);
  };
  raf = requestAnimationFrame(tick);
  return () => cancelAnimationFrame(raf);
}, []);
```

## Decision matrix

| Situation                                   | Tool                          |
|---------------------------------------------|-------------------------------|
| Click triggers a 3D camera move             | `@react-spring/three`         |
| Hover triggers scale/rotation on a mesh     | `@react-spring/three`         |
| Particle pool converges to a target         | `@react-spring/three`         |
| DOM elements stagger in on page mount       | GSAP                          |
| Tab content swaps with overlap              | GSAP                          |
| Theme tokens crossfade across the document  | GSAP                          |
| Node drift, idle sway, edge pulse uniform   | `useFrame` + `MathUtils.damp` |

If you find yourself reaching for GSAP to tween a three.js camera, or
springs to sustain a continuous drift loop, stop and re-read this doc.
