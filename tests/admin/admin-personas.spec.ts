/**
 * Phase 7.5 — persona approval queue smoke specs.
 *
 * Same auth-gate shape as admin-rules. Bell-badge animation +
 * approve/reject actions need authed + seeded data; tracked separately.
 */
import { expect, test } from "@playwright/test";

test.describe("/atlas/admin/personas", () => {
  test("redirects to login when unauthenticated", async ({ page }) => {
    await page.goto("/atlas/admin/personas");
    await page.waitForURL(/^\/(\?|$)/, { timeout: 5000 });
    expect(page.url()).toContain("next=");
  });
});
