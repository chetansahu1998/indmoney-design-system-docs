import { test, expect } from "@playwright/test";

/**
 * Deep-component-extraction walkthrough (P6 of plan-002).
 *
 * Asserts the rich data shipped from the Go extractor reaches the
 * inspector UI:
 *   - Click a tile → inspector opens with sticky header
 *   - "Variant axes" section renders at least one axis row
 *   - "Variants" section renders default ★ badge on at least one row
 */

const BASE = "http://localhost:3001";

test("inspector renders Variant axes table after clicking a component tile", async ({ page }) => {
  await page.goto(BASE + "/components", { waitUntil: "networkidle" });

  // Click the first component tile.
  const firstTile = page.locator("[data-component]").first();
  await firstTile.click();

  // Inspector body must show "Variant axes" section header.
  const axesHeader = page.locator("aside, [class*='view'] >> text=Variant axes").first();
  await expect(axesHeader).toBeVisible({ timeout: 3000 });

  // At least one axis row carries an axis name (e.g. "Type", "Size", "State").
  // The axis row sets the axis name as its first label — assert at least one
  // such row exists by counting children of the axes container indirectly:
  // the section's count badge reads the total prop+axis count.
  // (We don't bind to specific axis names — different components have
  // different axes; presence is the contract.)
  const sectionCountBadge = axesHeader.locator("xpath=following-sibling::span[1]").first();
  await expect(sectionCountBadge).toBeVisible();
  const countText = (await sectionCountBadge.textContent()) || "";
  expect(parseInt(countText.trim(), 10)).toBeGreaterThan(0);
});

test("inspector marks the default variant with ★ DEFAULT badge", async ({ page }) => {
  await page.goto(BASE + "/components", { waitUntil: "networkidle" });
  await page.locator("[data-component]").first().click();

  // The default-variant badge is the literal string "★ DEFAULT" inside
  // a span on exactly one variant row. This is fragile if the visual
  // copy changes — accept that as the price of an end-to-end check.
  const defaultBadge = page.getByText("★ DEFAULT", { exact: false }).first();
  await expect(defaultBadge).toBeVisible({ timeout: 3000 });
});
