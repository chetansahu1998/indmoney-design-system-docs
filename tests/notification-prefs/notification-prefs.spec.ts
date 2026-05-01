/**
 * Phase 7.5 — notification preference center smoke specs.
 *
 * Verifies the page renders without authentication redirect (the page
 * itself isn't admin-gated; it just needs a valid token to load prefs).
 */
import { expect, test } from "@playwright/test";

test.describe("/settings/notifications", () => {
  test("renders the sign-in card when unauthenticated", async ({ page }) => {
    await page.goto("/settings/notifications");
    await expect(page.locator("text=Sign in to manage notifications")).toBeVisible({
      timeout: 5000,
    });
  });
});
