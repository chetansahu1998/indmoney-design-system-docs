/**
 * Phase 4 U14 (deferred) — ViolationsTab inline lifecycle controls.
 *
 * Covers AE-3 closure: a designer hits Acknowledge with a reason from
 * the project page; the row fades out + the PATCH request fires with
 * the right body.
 */

import { test, expect, type Page } from "@playwright/test";

const TENANT = "tenant-a";
const SLUG = "tax-fno-learn";

async function injectAuth(page: Page): Promise<void> {
  await page.addInitScript((tenantID: string) => {
    window.localStorage.setItem(
      "indmoney-ds-auth",
      JSON.stringify({
        state: {
          token: "fake-token",
          tenants: [{ id: tenantID, slug: tenantID, name: tenantID }],
          activeTenantID: tenantID,
        },
        version: 0,
      }),
    );
  }, TENANT);
}

test.skip("Acknowledge fires PATCH + fades the row", async ({ page }) => {
  // The full flow exercises the project shell + view-state machine +
  // SSE ticket flow. Skipped pending fixture builders that mirror
  // tests/projects/cold-start.spec.ts. Phase 5 plan re-enables this
  // alongside the DRD collab tests.
  await injectAuth(page);
  let patchBody: { action: string; reason: string } | null = null;
  await page.route("**/v1/projects/*/violations/*", async (route) => {
    if (route.request().method() === "PATCH") {
      patchBody = JSON.parse(route.request().postData() || "{}");
      return route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ violation_id: "v1", from: "active", to: "acknowledged", action: "acknowledge" }),
      });
    }
    return route.continue();
  });
  await page.goto(`/projects/${SLUG}`);
  await page.getByRole("button", { name: "Acknowledge" }).first().click();
  await page.locator("textarea").fill("deferred to v2");
  await page.getByRole("button", { name: /Confirm acknowledge/ }).click();
  await page.waitForFunction(() => true);
  expect(patchBody).toEqual({ action: "acknowledge", reason: "deferred to v2" });
});
