/**
 * Phase 5 U13 — Decisions tab + creation Playwright spec.
 *
 * Mocks /v1/projects/:slug/flows/:flow_id/decisions and POSTs through
 * to verify the form roundtrip + list re-render after submission.
 */

import { test, expect, type Page } from "@playwright/test";

const TENANT = "tenant-a";
const SLUG = "tax-fno-learn";
const FLOW_ID = "flow-1";

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

test.skip("Decisions tab — create + list re-render", async ({ page }) => {
  // Skipped pending the project-shell Playwright fixtures (mocks for
  // /v1/projects/:slug + /flows/:flow_id/screens etc). The Phase 5
  // backend is unit-tested in Go (decisions_test.go); when the project
  // fixture lands the same scenarios drive end-to-end here.
  await injectAuth(page);

  let listed: Array<Record<string, unknown>> = [];
  await page.route(`**/v1/projects/${SLUG}/flows/${FLOW_ID}/decisions**`, async (route) => {
    const req = route.request();
    if (req.method() === "POST") {
      const body = JSON.parse(req.postData() || "{}");
      const rec = {
        id: "d-1",
        tenant_id: TENANT,
        flow_id: FLOW_ID,
        version_id: "v-1",
        title: body.title,
        body_json: body.body_json,
        status: body.status ?? "accepted",
        made_by_user_id: "u-1",
        made_at: new Date().toISOString(),
        created_at: new Date().toISOString(),
        updated_at: new Date().toISOString(),
      };
      listed.push(rec);
      return route.fulfill({
        status: 201,
        contentType: "application/json",
        body: JSON.stringify(rec),
      });
    }
    return route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ decisions: listed, count: listed.length }),
    });
  });

  await page.goto(`/projects/${SLUG}`);
  await page.getByRole("tab", { name: /Decisions/i }).click();
  await expect(page.getByText(/No decisions yet/i)).toBeVisible();

  await page.getByRole("button", { name: /New decision/i }).click();
  await page.getByLabel("Title").fill("Approved padding-32 over grid-24");
  await page.getByRole("button", { name: /Save decision/i }).click();

  await expect(page.getByText("Approved padding-32 over grid-24")).toBeVisible();
});
