/**
 * Phase 4 U14 (deferred) — ViolationsTab inline lifecycle controls.
 *
 * Covers AE-3 closure: a designer hits Acknowledge with a reason from
 * the project page; the row fades out + the PATCH request fires with
 * the right body. Phase 6 U6 extends this spec with the Reactivate
 * action exposed to admin users on dismissed rows (R8 closure).
 */

import { test, expect, type Page } from "@playwright/test";

const TENANT = "tenant-a";
const SLUG = "tax-fno-learn";

async function injectAuth(page: Page, role = "designer"): Promise<void> {
  await page.addInitScript(
    ({ tenantID, userRole }: { tenantID: string; userRole: string }) => {
      window.localStorage.setItem(
        "indmoney-ds-auth",
        JSON.stringify({
          state: {
            token: "fake-token",
            email: "designer@example.com",
            role: userRole,
            tenants: [{ id: tenantID, slug: tenantID, name: tenantID }],
            activeTenantID: tenantID,
          },
          version: 0,
        }),
      );
    },
    { tenantID: TENANT, userRole: role },
  );
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

test.skip("Admin reactivates a dismissed violation", async ({ page }) => {
  // Phase 6 U6 — closure for R8 (Active → Acknowledged → Fixed | Dismissed
  // PLUS DS-lead override Dismissed → Active). Admin role is gated client
  // side via useAuth().role; backend re-checks via isAdminRole() in
  // services/ds-service/internal/projects/lifecycle.go.
  //
  // Skipped on the same fixture-builder rationale as the Acknowledge
  // test above. Re-enables alongside the project-shell fixture work
  // tracked by Phase 5/6 testing follow-ups.
  await injectAuth(page, "tenant_admin");
  let patchBody: { action: string; reason: string } | null = null;
  await page.route("**/v1/projects/*/violations/*", async (route) => {
    if (route.request().method() === "PATCH") {
      patchBody = JSON.parse(route.request().postData() || "{}");
      return route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          violation_id: "v1",
          from: "dismissed",
          to: "active",
          action: "reactivate",
        }),
      });
    }
    return route.continue();
  });
  await page.goto(`/projects/${SLUG}`);
  await page
    .getByRole("button", { name: /Reactivate dismissed violation/ })
    .first()
    .click();
  await page.locator("textarea").fill("revisited in v3 retro");
  await page
    .getByRole("button", { name: /Confirm reactivate/ })
    .click();
  await page.waitForFunction(() => true);
  expect(patchBody).toEqual({
    action: "reactivate",
    reason: "revisited in v3 retro",
  });
});

test.skip("Non-admin cannot see Reactivate on dismissed rows", async ({ page }) => {
  // Defense-in-depth check: a designer (non-admin) viewing a dismissed
  // violation should see no inline action — the backend would 403 on
  // submit anyway, but we don't want the button to appear and create
  // a confusing UX.
  await injectAuth(page, "designer");
  await page.goto(`/projects/${SLUG}`);
  // Even if a dismissed row is rendered, the Reactivate button should
  // be absent for the designer role.
  await expect(
    page.getByRole("button", { name: /Reactivate dismissed violation/ }),
  ).toHaveCount(0);
});
