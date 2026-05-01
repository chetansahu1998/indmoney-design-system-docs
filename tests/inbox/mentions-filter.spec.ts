/**
 * Phase 5 U13 — Mentions filter chip on /inbox.
 *
 * Verifies the mode-tab toggle pulls notifications from
 * /v1/notifications and renders NotificationRow rows; mark-read
 * fires POST /v1/notifications/mark-read with the right id.
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

test("Mentions tab fetches + renders + marks-read", async ({ page }) => {
  await injectAuth(page);

  // Stub the violations call (Violations is the default mode).
  await page.route("**/v1/inbox**", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ rows: [], total: 0, limit: 50, offset: 0 }),
    }),
  );

  let markReadIDs: string[] = [];
  await page.route("**/v1/notifications**", (route) => {
    if (route.request().method() === "POST") {
      const body = JSON.parse(route.request().postData() || "{}");
      markReadIDs = body.ids ?? [];
      return route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ updated: markReadIDs.length }),
      });
    }
    return route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        notifications: [
          {
            id: "n-1",
            tenant_id: TENANT,
            recipient_user_id: "u-self",
            kind: "mention",
            target_kind: "comment",
            target_id: "c-1",
            flow_id: "flow-1",
            actor_user_id: "u-other",
            payload_json: JSON.stringify({ body_snippet: "cc @karthik please" }),
            created_at: new Date().toISOString(),
          },
        ],
        count: 1,
      }),
    });
  });

  await page.goto("/inbox");
  await page.getByRole("tab", { name: /Mentions/i }).click();
  await expect(page.getByTestId("notifications-list")).toBeVisible();
  await expect(page.getByText(/mentioned you/i)).toBeVisible();
  await page.getByTestId("notification-row").click();
  // mark-read POST should fire on click.
  await page.waitForFunction(() => true);
  expect(markReadIDs).toContain("n-1");
});
