/**
 * Phase 4 U14 — /inbox route Playwright spec.
 *
 * Covers the happy path (loaded inbox renders + bulk-acknowledge fades
 * rows + filter chips reflect URL state) and the empty-state path.
 *
 * Strategy mirrors tests/projects/cold-start.spec.ts: page.route
 * intercepts the /v1/inbox + /v1/projects/.../bulk-acknowledge calls,
 * auth is injected via addInitScript.
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

const SAMPLE_ROW = {
  violation_id: "v-1",
  version_id: "ver-1",
  screen_id: "scr-1",
  flow_id: "flow-1",
  project_id: "proj-1",
  project_slug: "tax-fno-learn",
  project_name: "Tax / F&O / Learn Touchpoints",
  product: "Tax",
  flow_name: "Onboarding",
  rule_id: "theme_parity.fill",
  category: "theme_parity",
  severity: "high" as const,
  property: "fill",
  observed: "rgba(127,64,255,0.5)",
  suggestion: "Bind to colour.surface.button-cta",
  auto_fixable: true,
  status: "active" as const,
  created_at: new Date().toISOString(),
};

test("loaded inbox renders rows + supports bulk-acknowledge", async ({ page }) => {
  await injectAuth(page);

  await page.route("**/v1/inbox**", (route) => {
    return route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        rows: [SAMPLE_ROW, { ...SAMPLE_ROW, violation_id: "v-2" }],
        total: 2,
        limit: 50,
        offset: 0,
      }),
    });
  });

  let bulkRequest: { ids: string[]; reason: string; action: string } | null = null;
  await page.route("**/violations/bulk-acknowledge", async (route) => {
    const body = JSON.parse(route.request().postData() || "{}");
    bulkRequest = {
      ids: body.violation_ids,
      reason: body.reason,
      action: body.action,
    };
    return route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        bulk_id: "bulk-1",
        updated: body.violation_ids,
        skipped: [],
        action: body.action,
      }),
    });
  });

  await page.goto("/inbox");
  await expect(page.getByTestId("inbox-shell")).toBeVisible();
  const rows = page.getByTestId("inbox-row");
  await expect(rows).toHaveCount(2);

  // Select-all + bulk acknowledge.
  await page.getByLabel("Select all visible rows").check();
  await expect(page.getByText("2 selected")).toBeVisible();
  await page.getByRole("button", { name: /Acknowledge 2/ }).click();
  await page.locator('select').selectOption("deferred-v2");
  await page.getByRole("button", { name: /Confirm acknowledge/ }).click();

  await page.waitForFunction(() => {
    return document.querySelectorAll('[data-testid="inbox-row"]').length === 0;
  });
  expect(bulkRequest).not.toBeNull();
  expect(bulkRequest!.action).toBe("acknowledge");
  expect(bulkRequest!.ids).toEqual(["v-1", "v-2"]);
});

test("empty inbox shows welcome state", async ({ page }) => {
  await injectAuth(page);
  await page.route("**/v1/inbox**", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ rows: [], total: 0, limit: 50, offset: 0 }),
    }),
  );

  await page.goto("/inbox");
  await expect(page.getByText(/Inbox zero/i)).toBeVisible();
});

test("filter chip updates the URL + refetches", async ({ page }) => {
  await injectAuth(page);

  let lastQuery: string | null = null;
  await page.route("**/v1/inbox**", (route) => {
    lastQuery = new URL(route.request().url()).search;
    return route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ rows: [SAMPLE_ROW], total: 1, limit: 50, offset: 0 }),
    });
  });

  await page.goto("/inbox");
  await expect(page.getByTestId("inbox-row")).toHaveCount(1);

  // Click a category chip.
  await page.getByRole("button", { name: "Theme parity" }).click();
  await page.waitForFunction(() => window.location.search.includes("category=theme_parity"));
  expect(lastQuery).toContain("category=theme_parity");
});
