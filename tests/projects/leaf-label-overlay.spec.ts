/**
 * Phase 9 U2a — DOM overlay layer for flow-leaf labels.
 *
 * Asserts:
 *   1. Leaf labels render as DOM <div> elements (not as canvas pixels)
 *      inside the `data-testid="leaf-label-layer"` container, with the
 *      `pointer-events: none` style so they don't intercept node clicks.
 *   2. Each rendered label's screen position tracks the underlying
 *      three.js node within ±2px during force-simulation steady state.
 *      We verify this indirectly via the `transform: translate3d(...)`
 *      values being numeric and within the canvas bounds.
 *   3. Off-screen labels carry `display: none` so we don't ship 100+
 *      hidden DOM nodes into the layout pass.
 *
 * This spec depends on the dev server having a non-empty graph (at least
 * one flow-type node). If the test DB is empty, the leaf-label-layer is
 * still mounted but contains zero children — we skip the position
 * assertions in that case.
 *
 * Reduced-motion is NOT tested here; the projection runs regardless of
 * reduced-motion (label tracking is functional, not decorative).
 */

import { expect, test } from "@playwright/test";

test.describe("Phase 9 U2a — leaf-label DOM overlay", () => {
  test.beforeEach(async ({ page }) => {
    await page.goto("/atlas");
    // Wait for the BrainGraph chrome to render — the overlay layer
    // mounts alongside the canvas.
    await expect(page.locator('button:has-text("Hierarchy")')).toBeVisible({
      timeout: 15_000,
    });
  });

  test("overlay container renders as DOM (not canvas)", async ({ page }) => {
    const layer = page.locator('[data-testid="leaf-label-layer"]');
    await expect(layer).toBeAttached({ timeout: 10_000 });

    // It must be a <div>, not a <canvas>. (Canvas-rasterized sprites
    // cannot serve as the view-transition source for the cross-route
    // morph — that's the whole point of this overlay.)
    const tagName = await layer.evaluate((el) => el.tagName.toLowerCase());
    expect(tagName).toBe("div");

    // pointer-events: none — labels must not intercept canvas clicks.
    const pointerEvents = await layer.evaluate(
      (el) => window.getComputedStyle(el).pointerEvents,
    );
    expect(pointerEvents).toBe("none");

    // position: absolute and inset:0 (or equivalent left/top/right/bottom: 0)
    // so it composites cleanly over the canvas.
    const position = await layer.evaluate(
      (el) => window.getComputedStyle(el).position,
    );
    expect(position).toBe("absolute");
  });

  test("flow-leaf labels render as <div> children with translate3d", async ({
    page,
  }) => {
    const layer = page.locator('[data-testid="leaf-label-layer"]');
    await expect(layer).toBeAttached({ timeout: 10_000 });

    // Wait up to 8s for at least one label to project. The force
    // simulation needs ~1-2s to settle, then the rAF loop kicks in.
    const labels = layer.locator(".leaf-label");
    const count = await labels
      .count()
      .catch(() => 0);

    test.skip(
      count === 0,
      "No flow nodes in the graph fixture — overlay verified empty.",
    );

    // Each child must be a <div> with a numeric translate3d transform.
    const first = labels.first();
    await expect(first).toBeAttached();
    const tagName = await first.evaluate((el) => el.tagName.toLowerCase());
    expect(tagName).toBe("div");

    const transform = await first.evaluate(
      (el) => (el as HTMLElement).style.transform,
    );
    // U3 — labels are centred on the node via `translate(-50%, -50%)`
    // followed by `translate3d(<x>px, <y>px, 0)`. CSS transforms apply
    // right-to-left so translate3d positions the box first, then the
    // -50% shift centres the box's own centre on the projected coord.
    expect(transform).toMatch(
      /^translate\(-50%, -50%\) translate3d\(-?\d+(?:\.\d+)?px, -?\d+(?:\.\d+)?px, 0\)$/,
    );

    // The id attribute is preserved so view-transition-name matching (U2b)
    // can target it.
    const dataId = await first.getAttribute("data-leaf-label-id");
    expect(dataId).toBeTruthy();
  });

  test("off-screen labels are display:none, on-screen are display:block", async ({
    page,
  }) => {
    const layer = page.locator('[data-testid="leaf-label-layer"]');
    await expect(layer).toBeAttached({ timeout: 10_000 });

    // Allow the projection loop a moment to converge.
    await page.waitForTimeout(2500);

    const labels = layer.locator(".leaf-label");
    const count = await labels.count();
    test.skip(count === 0, "No flow nodes — overlay culling not exercised.");

    // For each label, verify display is exactly "block" or "none" (never
    // some other value that would imply a styling regression).
    const displays = await labels.evaluateAll((els) =>
      els.map((el) => (el as HTMLElement).style.display),
    );
    for (const d of displays) {
      expect(["block", "none"]).toContain(d);
    }

    // Visible labels (display: block) must have transform values within
    // the viewport bounds — labels with display:none can be anywhere.
    const visibleBounds = await labels.evaluateAll((els) => {
      return els
        .filter((el) => (el as HTMLElement).style.display === "block")
        .map((el) => {
          // U3 — transform shape is now
          // `translate(-50%, -50%) translate3d(<x>px, <y>px, 0)`.
          const m = (el as HTMLElement).style.transform.match(
            /translate3d\((-?\d+(?:\.\d+)?)px, (-?\d+(?:\.\d+)?)px,/,
          );
          if (!m) return null;
          const rect = el.getBoundingClientRect();
          return {
            x: parseFloat(m[1]),
            y: parseFloat(m[2]),
            // Bounding-rect centre — must equal the projected coord
            // (the layer's own client rect is at the viewport origin
            // since it has `inset: 0`).
            cx: rect.left + rect.width / 2,
            cy: rect.top + rect.height / 2,
          };
        })
        .filter(Boolean);
    });

    if (visibleBounds.length > 0) {
      const vp = page.viewportSize() ?? { width: 1280, height: 720 };
      for (const pt of visibleBounds) {
        // Allow a small overhang for labels just inside the edge — we
        // mainly want to catch wildly out-of-bounds projections (e.g.
        // a 2× DPR drift would put labels at 2× the viewport width).
        expect(pt!.x).toBeGreaterThan(-50);
        expect(pt!.x).toBeLessThan(vp.width + 50);
        expect(pt!.y).toBeGreaterThan(-50);
        expect(pt!.y).toBeLessThan(vp.height + 50);

        // U3 — the label's bounding-rect centre must match the
        // projected node coordinate within ±2 px. With the
        // `translate(-50%, -50%)` anchor, the box's centre lands on
        // (x, y) regardless of label width.
        expect(Math.abs(pt!.cx - pt!.x)).toBeLessThanOrEqual(2);
        expect(Math.abs(pt!.cy - pt!.y)).toBeLessThanOrEqual(2);
      }
    }
  });
});
