/**
 * Phase 4 U14 (deferred) — per-component reverse view rendering.
 *
 * /components/<slug> includes the WhereThisBreaks section. The
 * underlying endpoint is unit-tested in Go (components_test.go);
 * this spec asserts the section renders with the expected stat
 * cards + flow rows when the API returns non-zero data.
 */

import { test, expect, type Page } from "@playwright/test";

const TENANT = "tenant-a";

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

const FIXTURE = {
  name: "Toast",
  aggregate: {
    total_violations: 47,
    by_severity: { critical: 4, high: 12, medium: 31 },
    by_set_sprawl: 0,
    by_set_detached: 30,
    by_set_override: 17,
    flow_count: 23,
  },
  flows: [
    {
      project_id: "p1",
      project_slug: "tax-fno-learn",
      project_name: "Tax / F&O / Learn Touchpoints",
      product: "Tax",
      flow_id: "f1",
      flow_name: "Onboarding",
      violation_count: 12,
      highest_severity: "critical",
    },
  ],
};

test.skip("logged-in /components/<slug> renders Where this breaks", async ({ page }) => {
  // Skipped pending the static-page test fixture: /components/[slug]
  // is SSG'd via componentsWithRichData; the spec needs a deterministic
  // slug whose SSG output is in the build. Re-enables in Phase 5.
  await injectAuth(page);
  await page.route("**/v1/components/violations**", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(FIXTURE),
    }),
  );

  await page.goto("/components/toast");
  await expect(page.getByTestId("where-this-breaks")).toBeVisible();
  await expect(page.getByText("47")).toBeVisible(); // total_violations
  await expect(page.getByText("23")).toBeVisible(); // flow_count
  await expect(page.getByText("Tax / F&O / Learn Touchpoints")).toBeVisible();
});
