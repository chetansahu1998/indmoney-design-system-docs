/**
 * Phase 7.5 + 7.6 — taxonomy curator smoke specs.
 *
 * Auth + seeded data needed for the drag-to-reorder + promote/archive
 * paths. v1 spec covers the unauthenticated redirect.
 */
import { expect, test } from "@playwright/test";

test.describe("/atlas/admin/taxonomy", () => {
  test("redirects to login when unauthenticated", async ({ page }) => {
    await page.goto("/atlas/admin/taxonomy");
    await page.waitForURL(/^\/(\?|$)/, { timeout: 5000 });
    expect(page.url()).toContain("next=");
  });
});
