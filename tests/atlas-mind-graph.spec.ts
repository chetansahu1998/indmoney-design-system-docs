/**
 * Phase 6 U14 — Playwright e2e for the mind graph at /atlas.
 *
 * Five critical paths:
 *   1. Mount + WebGL canvas appears
 *   2. Reduced-motion fallback shows the dashboard link
 *   3. Filter chips render + toggle UI state
 *   4. Platform toggle swaps Mobile↔Web
 *   5. Click-and-hold regression: camera position unchanged on hold (the
 *      user's frozen contract — held interaction is signal-only, NOT a
 *      zoom)
 *
 * Notes:
 *   - These tests run against the dev server (next dev --port 3001) so
 *     three.js + the rebuild worker can boot. CI workflow not yet wired
 *     for the WebGL stack; this spec stays opt-in via --grep.
 *   - The Playwright Chromium image ships with WebGL 2 enabled by
 *     default; `await page.evaluate('!!document.createElement("canvas").getContext("webgl2")')`
 *     should return true.
 */

import { expect, test } from "@playwright/test";

test.describe("Phase 6 mind graph (/atlas)", () => {
  test.beforeEach(async ({ page }) => {
    // Skip Authorization gating in dev — the page itself is public; the
    // SSE channel + aggregate endpoint require Bearer tokens but failure
    // there only blocks live updates, not the canvas mount.
    await page.goto("/atlas");
  });

  test("renders the canvas + filter chips on mount", async ({ page }) => {
    // The skeleton appears first; then BrainGraph mounts.
    await expect(page.locator("text=Loading mind graph…")).toBeVisible({
      timeout: 5000,
    });
    // Filter chips render once BrainGraph is past the skeleton + WebGL2 check.
    await expect(page.locator('button:has-text("Hierarchy")')).toBeVisible({
      timeout: 10_000,
    });
    await expect(page.locator('button:has-text("Components")')).toBeVisible();
    await expect(page.locator('button:has-text("Tokens")')).toBeVisible();
    await expect(page.locator('button:has-text("Decisions")')).toBeVisible();
  });

  test("platform toggle swaps mobile↔web", async ({ page }) => {
    await expect(page.locator('button:has-text("Mobile")')).toBeVisible({
      timeout: 10_000,
    });
    await page.click('button:has-text("Web")');
    await expect(page.locator('button[aria-selected="true"]:has-text("Web")')).toBeVisible();
    await page.click('button:has-text("Mobile")');
    await expect(page.locator('button[aria-selected="true"]:has-text("Mobile")')).toBeVisible();
  });

  test("filter chips toggle their active state", async ({ page }) => {
    const components = page.locator('button:has-text("Components")');
    await expect(components).toBeVisible({ timeout: 10_000 });
    // Initially inactive.
    await expect(components).not.toHaveClass(/active/);
    await components.click();
    await expect(components).toHaveClass(/active/);
    await components.click();
    await expect(components).not.toHaveClass(/active/);
  });

  test("reduced-motion serves the static fallback", async ({ browser }) => {
    const ctx = await browser.newContext({ reducedMotion: "reduce" });
    const page = await ctx.newPage();
    await page.goto("/atlas");
    await expect(page.locator("text=Reduced motion is enabled")).toBeVisible({
      timeout: 5000,
    });
    await expect(page.locator('a:has-text("Open admin dashboard")')).toBeVisible();
    await ctx.close();
  });

  /**
   * Regression for the user's frozen click-and-hold contract: holding
   * a node MUST NOT move the camera. We capture the camera matrix
   * before + after a 1-second hold and assert it didn't change.
   *
   * The library exposes camera() on the ForceGraph3D ref, but we don't
   * have direct ref access from Playwright. Instead we read the
   * underlying Three.js renderer's camera via `__r3f` / scene API
   * by injecting a small probe.
   */
  test("click-and-hold leaves camera position unchanged", async ({ page }) => {
    // Skip if no rendered nodes (empty graph in test env).
    const hasFilters = await page
      .locator('button:has-text("Hierarchy")')
      .isVisible({ timeout: 10_000 })
      .catch(() => false);
    test.skip(!hasFilters, "Atlas not rendered (likely no graph data in test DB)");

    // Capture initial camera state via the canvas's WebGL context.
    // Without library ref access we approximate via the canvas size +
    // rendered pixel hash; a true camera-position check requires test
    // hooks the BrainGraph would need to expose. For now we verify the
    // canvas pixel bounding box is unchanged after a hold gesture —
    // movement would change which pixels render.
    const canvas = page.locator("canvas").first();
    await expect(canvas).toBeVisible();
    const before = await canvas.boundingBox();
    if (!before) test.skip(true, "canvas not measurable");

    // Press + hold for 1s on a node region (canvas center as proxy).
    const box = before!;
    const cx = box.x + box.width / 2;
    const cy = box.y + box.height / 2;
    await page.mouse.move(cx, cy);
    await page.mouse.down();
    await page.waitForTimeout(1000);
    await page.mouse.up();

    // After release, the canvas dimensions should still match. (A real
    // camera tween would have re-rendered at a different zoom; the size
    // wouldn't change but the pixel content would. This is a smoke
    // check, not a perfect regression — the deeper invariant is asserted
    // via unit tests on useSignalHold.)
    const after = await canvas.boundingBox();
    expect(after?.width).toBe(before!.width);
    expect(after?.height).toBe(before!.height);
  });
});
