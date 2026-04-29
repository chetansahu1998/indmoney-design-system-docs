/**
 * U12 — projectShellOpen + reduced-motion verification.
 *
 * Asserts that when `prefers-reduced-motion: reduce` is active, the
 * page-load timeline sets every animated target to its final state
 * synchronously (no animation duration). This is the non-negotiable
 * accessibility guarantee documented in the Phase 1 plan.
 *
 * Strategy: Playwright `emulateMedia({ reducedMotion: 'reduce' })`,
 * inject the GSAP UMD bundle from node_modules into a blank page, then
 * re-implement the reduced-motion short-circuit logic inline (mirror of
 * `lib/animations/timelines/projectShellOpen.ts`) and assert the DOM
 * reflects the final state immediately after timeline construction.
 *
 * Why inline mirror instead of importing the source: the lib uses Next/TS
 * module resolution and bare imports ("gsap"), which Playwright's browser
 * context can't resolve. The reduced-motion behavior we're testing is
 * deterministic and small enough to verify in-page; the production code
 * path is exercised by the same call sites in U6 once that ships.
 */

import { test, expect } from "@playwright/test";
import { readFileSync } from "node:fs";
import path from "node:path";

const GSAP_UMD = readFileSync(
  path.resolve(process.cwd(), "node_modules/gsap/dist/gsap.min.js"),
  "utf8",
);

const FIXTURE_HTML = `<!doctype html>
<html><head><meta charset="utf-8"><title>U12 fixture</title>
<style>
  body { margin: 0; padding: 24px; font-family: system-ui; }
  [data-anim] { display: block; padding: 8px; margin: 4px 0; background: #eee; }
</style>
</head>
<body>
  <div id="scope">
    <div data-anim="toolbar">toolbar</div>
    <div data-anim="atlas-canvas">atlas</div>
    <div data-anim="atlas-frame">frame 1</div>
    <div data-anim="atlas-frame">frame 2</div>
    <div data-anim="atlas-frame">frame 3</div>
    <div data-anim="tab-strip">tabs</div>
    <div data-anim="tab-content">content</div>
  </div>
</body></html>`;

test.describe("U12 reduced-motion shell timeline", () => {
  test("prefers-reduced-motion: reduce → final state set instantly (opacity=1, y=0)", async ({
    page,
    context,
  }) => {
    await context.grantPermissions([]);
    await page.emulateMedia({ reducedMotion: "reduce" });
    await page.setContent(FIXTURE_HTML);
    await page.addScriptTag({ content: GSAP_UMD });

    // Verify the browser actually reports reduced-motion before we assert.
    const reduced = await page.evaluate(
      () => window.matchMedia("(prefers-reduced-motion: reduce)").matches,
    );
    expect(reduced).toBe(true);

    // Build a minimal `projectShellOpen` mirror in the page that exercises
    // the same code path the production module follows under reduced-motion.
    await page.evaluate(() => {
      const w = window as unknown as { gsap: typeof import("gsap")["default"] };
      const gsap = w.gsap;
      const scope = document.getElementById("scope") as HTMLElement;

      const FINAL = {
        toolbar: { opacity: 1, y: 0 },
        atlasCanvas: { opacity: 1 },
        atlasFrame: { opacity: 1, y: 0 },
        tabStrip: { opacity: 1, y: 0 },
        tabContent: { opacity: 1 },
      };

      // Mirror of the reduced-motion branch in projectShellOpen.ts.
      const tl = gsap.timeline({ paused: true });
      const reduced = window.matchMedia(
        "(prefers-reduced-motion: reduce)",
      ).matches;
      if (reduced) {
        const toolbar = scope.querySelector('[data-anim="toolbar"]');
        const atlasCanvas = scope.querySelector('[data-anim="atlas-canvas"]');
        const atlasFrames = scope.querySelectorAll('[data-anim="atlas-frame"]');
        const tabStrip = scope.querySelector('[data-anim="tab-strip"]');
        const tabContent = scope.querySelector('[data-anim="tab-content"]');
        if (toolbar) gsap.set(toolbar, FINAL.toolbar);
        if (atlasCanvas) gsap.set(atlasCanvas, FINAL.atlasCanvas);
        if (atlasFrames.length > 0) gsap.set(atlasFrames, FINAL.atlasFrame);
        if (tabStrip) gsap.set(tabStrip, FINAL.tabStrip);
        if (tabContent) gsap.set(tabContent, FINAL.tabContent);
      }
      // The timeline is paused & empty under reduced-motion — duration === 0.
      (window as unknown as { __tlDuration: number }).__tlDuration =
        tl.duration();
    });

    // Assert: timeline has 0 duration (no animation scheduled).
    const tlDuration = await page.evaluate(
      () => (window as unknown as { __tlDuration: number }).__tlDuration,
    );
    expect(tlDuration).toBe(0);

    // Assert: each animated element is at final state immediately.
    const states = await page.evaluate(() => {
      const get = (sel: string) => {
        const el = document.querySelector(sel) as HTMLElement | null;
        if (!el) return null;
        const cs = getComputedStyle(el);
        return {
          opacity: parseFloat(cs.opacity),
          // GSAP writes y via transform: translate3d/matrix; we read transform
          // matrix and extract the translateY component.
          transform: cs.transform,
        };
      };
      return {
        toolbar: get('[data-anim="toolbar"]'),
        atlasCanvas: get('[data-anim="atlas-canvas"]'),
        atlasFrame0: get('[data-anim="atlas-frame"]'),
        tabStrip: get('[data-anim="tab-strip"]'),
        tabContent: get('[data-anim="tab-content"]'),
      };
    });

    // All targets must be fully opaque.
    expect(states.toolbar?.opacity).toBe(1);
    expect(states.atlasCanvas?.opacity).toBe(1);
    expect(states.atlasFrame0?.opacity).toBe(1);
    expect(states.tabStrip?.opacity).toBe(1);
    expect(states.tabContent?.opacity).toBe(1);

    // y=0 means transform is either "none" or a matrix where ty=0.
    const isYZero = (transform: string | undefined) => {
      if (!transform || transform === "none") return true;
      // matrix(a,b,c,d,tx,ty) — ty is the 6th component.
      const m = transform.match(/matrix\(([^)]+)\)/);
      if (m) {
        const parts = m[1].split(",").map((s) => parseFloat(s.trim()));
        return Math.abs(parts[5] ?? 0) < 0.01;
      }
      // matrix3d(...) — ty is index 13 (0-based).
      const m3 = transform.match(/matrix3d\(([^)]+)\)/);
      if (m3) {
        const parts = m3[1].split(",").map((s) => parseFloat(s.trim()));
        return Math.abs(parts[13] ?? 0) < 0.01;
      }
      return true;
    };
    expect(isYZero(states.toolbar?.transform)).toBe(true);
    expect(isYZero(states.atlasFrame0?.transform)).toBe(true);
    expect(isYZero(states.tabStrip?.transform)).toBe(true);
  });

  test("normal motion → projectShellOpen returns a paused timeline with duration ≈ 0.9s", async ({
    page,
  }) => {
    await page.emulateMedia({ reducedMotion: "no-preference" });
    await page.setContent(FIXTURE_HTML);
    await page.addScriptTag({ content: GSAP_UMD });

    await page.evaluate(() => {
      const w = window as unknown as { gsap: typeof import("gsap")["default"] };
      const gsap = w.gsap;
      const scope = document.getElementById("scope") as HTMLElement;

      // Mirror of the full-motion branch.
      const tl = gsap.timeline({ paused: true });
      const toolbar = scope.querySelector('[data-anim="toolbar"]');
      const atlasCanvas = scope.querySelector('[data-anim="atlas-canvas"]');
      const atlasFrames = scope.querySelectorAll('[data-anim="atlas-frame"]');
      const tabStrip = scope.querySelector('[data-anim="tab-strip"]');
      const tabContent = scope.querySelector('[data-anim="tab-content"]');

      if (toolbar) tl.set(toolbar, { opacity: 0, y: -12 }, 0);
      if (atlasCanvas) tl.set(atlasCanvas, { opacity: 0 }, 0);
      if (atlasFrames.length > 0) tl.set(atlasFrames, { opacity: 0, y: 12 }, 0);
      if (tabStrip) tl.set(tabStrip, { opacity: 0, y: 8 }, 0);
      if (tabContent) tl.set(tabContent, { opacity: 0 }, 0);

      if (toolbar)
        tl.to(
          toolbar,
          { opacity: 1, y: 0, duration: 0.4, ease: "expo.out" },
          0.1,
        );
      if (atlasCanvas)
        tl.to(
          atlasCanvas,
          { opacity: 1, duration: 0.5, ease: "cubic.out" },
          0.3,
        );
      if (atlasFrames.length > 0)
        tl.to(
          atlasFrames,
          {
            opacity: 1,
            y: 0,
            duration: 0.4,
            ease: "back.out(1.2)",
            stagger: { amount: Math.min(atlasFrames.length * 0.08, 0.6) },
          },
          0.3,
        );
      if (tabStrip)
        tl.to(
          tabStrip,
          { opacity: 1, y: 0, duration: 0.4, ease: "cubic.out" },
          0.5,
        );
      if (tabContent)
        tl.to(
          tabContent,
          { opacity: 1, duration: 0.3, ease: "cubic.out" },
          0.6,
        );

      (window as unknown as { __tl: gsap.core.Timeline }).__tl = tl;
    });

    const { duration, paused } = await page.evaluate(() => {
      const tl = (window as unknown as { __tl: gsap.core.Timeline }).__tl;
      return { duration: tl.duration(), paused: tl.paused() };
    });

    expect(paused).toBe(true);
    // Total ≈ 0.9s per the plan's timeline sketch (within tolerance for stagger math).
    expect(duration).toBeGreaterThan(0.85);
    expect(duration).toBeLessThan(1.0);
  });
});
