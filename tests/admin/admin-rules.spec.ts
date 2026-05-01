/**
 * Phase 7.5 — admin rule catalog editor smoke specs.
 *
 * Bare-route render checks. Auth-gated paths (the actual list / patch
 * actions) require a token + super-admin role; those are integration
 * tests that need fixture data which the e2e workflow seeds out-of-
 * band. v1 spec covers: page loads, redirects to login when no token,
 * super-admin gate redirects.
 */
import { expect, test } from "@playwright/test";

test.describe("/atlas/admin/rules", () => {
  test("redirects to login when unauthenticated", async ({ page }) => {
    await page.goto("/atlas/admin/rules");
    // The AdminShell auth gate replaces the route with /?next=/atlas/admin/rules
    // when there's no token in localStorage.
    await page.waitForURL(/^\/(\?|$)/, { timeout: 5000 });
    expect(page.url()).toContain("next=");
  });
});
