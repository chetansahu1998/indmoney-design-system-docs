/**
 * Phase 8 — global search smoke specs.
 *
 * Verifies the cmdk palette opens, accepts input, and renders without
 * errors. Authed search-result tests need seeded FTS5 rows; tracked
 * for the e2e fixture phase.
 */
import { expect, test } from "@playwright/test";

test.describe("Search palette (cmdk)", () => {
  test("opens via ⌘K and accepts a query", async ({ page }) => {
    await page.goto("/");
    // The palette is mounted globally. Cmd+K (or Ctrl+K on Linux CI
    // headless Chromium).
    await page.keyboard.press("ControlOrMeta+k");
    const input = page.locator('input[placeholder*="Search" i], input[placeholder*="search" i]').first();
    await expect(input).toBeVisible({ timeout: 5000 });
    await input.fill("onboarding");
    // Don't assert on results — those depend on seeded data. Just
    // confirm the field is interactive + the palette stays open.
    await expect(input).toHaveValue("onboarding");
  });
});
