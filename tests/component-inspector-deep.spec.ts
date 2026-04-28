import { test, expect } from "@playwright/test";

/**
 * Horizontal-canvas /components walkthrough (P6 of plan-002).
 *
 * Asserts the rich data shipped from the Go extractor reaches the canvas
 * surface and the overlay drawer:
 *   - Canvas mounts with category bands and at least one inspect button
 *   - Clicking inspect opens an overlay aside with the deep sections
 *   - The Variant axes section renders at least one axis
 *   - At least one variant carries a default-marker (★ corner badge)
 */

const BASE = "http://localhost:3001";

test("canvas renders category bands with inspect-able components", async ({ page }) => {
  await page.goto(BASE + "/components", { waitUntil: "networkidle" });
  // Canvas surface mounts and contains at least one band.
  await expect(page.locator("[data-component-canvas]")).toBeVisible();
  await expect(page.locator("[data-cat]").first()).toBeVisible();
  // At least one component card has an inspect button.
  const inspect = page.getByRole("button", { name: /Inspect/i }).first();
  await expect(inspect).toBeVisible();
});

test("clicking inspect opens overlay with Variant axes section", async ({ page }) => {
  await page.goto(BASE + "/components", { waitUntil: "networkidle" });
  await page.getByRole("button", { name: /Inspect/i }).first().click();

  // Overlay aside slides in.
  const overlay = page.locator("aside").last();
  await expect(overlay).toBeVisible({ timeout: 3000 });

  // Variant axes section shows up with a count > 0.
  const axesHeader = overlay.locator("text=Variant axes").first();
  await expect(axesHeader).toBeVisible();
  const countBadge = axesHeader.locator("xpath=following-sibling::span[1]").first();
  const countText = (await countBadge.textContent()) || "";
  expect(parseInt(countText.trim(), 10)).toBeGreaterThan(0);
});

test("overlay marks at least one variant as default with ★", async ({ page }) => {
  await page.goto(BASE + "/components", { waitUntil: "networkidle" });
  await page.getByRole("button", { name: /Inspect/i }).first().click();
  const overlay = page.locator("aside").last();
  await expect(overlay).toBeVisible();
  // The default marker is the ★ glyph inside the All Variants tile.
  const star = overlay.getByText("★", { exact: false }).first();
  await expect(star).toBeVisible({ timeout: 3000 });
});
