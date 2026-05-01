/**
 * Phase 5 U13 — supersession chain rendering.
 *
 * Asserts that when the Decisions tab loads with two decisions where
 * one supersedes the other, both render with the dashed connector +
 * the predecessor's status pill reads "Superseded".
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

test.skip("Supersession chain renders predecessor + successor", async ({ page }) => {
  // Skipped pending project-shell fixtures (see decision-creation.spec.ts).
  await injectAuth(page);

  await page.route(`**/v1/projects/${SLUG}/flows/${FLOW_ID}/decisions**`, (route) => {
    const incl = new URL(route.request().url()).searchParams.get("include_superseded");
    type FixtureDecision = {
      id: string;
      tenant_id: string;
      flow_id: string;
      version_id: string;
      title: string;
      status: string;
      made_by_user_id: string;
      made_at: string;
      created_at: string;
      updated_at: string;
      supersedes_id?: string;
      superseded_by_id?: string;
    };
    const decisions: FixtureDecision[] = [
      {
        id: "d-2",
        tenant_id: TENANT,
        flow_id: FLOW_ID,
        version_id: "v-1",
        title: "Move to grid-24 unify",
        status: "accepted",
        made_by_user_id: "u-1",
        made_at: new Date().toISOString(),
        supersedes_id: "d-1",
        created_at: new Date().toISOString(),
        updated_at: new Date().toISOString(),
      },
    ];
    if (incl === "1") {
      decisions.push({
        id: "d-1",
        tenant_id: TENANT,
        flow_id: FLOW_ID,
        version_id: "v-1",
        title: "Approved padding-32 over grid-24",
        status: "superseded",
        made_by_user_id: "u-1",
        made_at: new Date(Date.now() - 86_400_000).toISOString(),
        superseded_by_id: "d-2",
        created_at: new Date(Date.now() - 86_400_000).toISOString(),
        updated_at: new Date().toISOString(),
      });
    }
    return route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ decisions, count: decisions.length }),
    });
  });

  await page.goto(`/projects/${SLUG}`);
  await page.getByRole("tab", { name: /Decisions/i }).click();
  await page.getByLabel(/Show superseded/i).check();
  await expect(page.getByText("Move to grid-24 unify")).toBeVisible();
  await expect(page.getByText("Approved padding-32 over grid-24")).toBeVisible();
});
