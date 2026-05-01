/**
 * Phase 4 U14 — /atlas/admin dashboard Playwright spec.
 *
 * Covers loaded + empty states. Recharts panels are heavy + lazy-loaded;
 * the spec asserts that the panel headings render without forcing a
 * full chart paint (Recharts' canvas-via-SVG path is fragile under
 * Playwright; we assert structural presence).
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

test("loaded dashboard renders aggregate panels", async ({ page }) => {
  await injectAuth(page);
  await page.route("**/v1/atlas/admin/summary**", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        weeks_window: 8,
        by_product: [
          { product: "Tax", active: 47 },
          { product: "Plutus", active: 23 },
        ],
        by_severity: { critical: 4, high: 12, medium: 30, low: 24 },
        trend: [
          { week_start: "2026-W17", active: 47, fixed: 12 },
          { week_start: "2026-W18", active: 53, fixed: 18 },
        ],
        top_violators: [
          {
            rule_id: "theme_parity.fill",
            category: "theme_parity",
            active_count: 25,
            highest_severity: "critical",
          },
        ],
        recent_decisions: [],
        total_active: 70,
        generated_at: new Date().toISOString(),
      }),
    }),
  );

  await page.goto("/atlas/admin");
  await expect(page.getByTestId("dashboard-shell")).toBeVisible();
  await expect(page.getByText(/70 active violations/i)).toBeVisible();
  await expect(page.getByText("Active violations by product")).toBeVisible();
  await expect(page.getByText("Trend (active vs fixed)")).toBeVisible();
  await expect(page.getByText("Top violators")).toBeVisible();
  await expect(page.getByText("Recent decisions")).toBeVisible();
});

test("non-admin gets 403 surfaced as an error EmptyState", async ({ page }) => {
  await injectAuth(page);
  await page.route("**/v1/atlas/admin/summary**", (route) =>
    route.fulfill({
      status: 403,
      contentType: "application/json",
      body: JSON.stringify({ error: "forbidden", detail: "super_admin required" }),
    }),
  );

  await page.goto("/atlas/admin");
  await expect(page.getByText(/Couldn't load dashboard/i)).toBeVisible();
});

test("changing weeks window re-fetches with new param", async ({ page }) => {
  await injectAuth(page);
  let lastWeeks: string | null = null;
  await page.route("**/v1/atlas/admin/summary**", (route) => {
    lastWeeks = new URL(route.request().url()).searchParams.get("weeks");
    return route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        weeks_window: Number(lastWeeks) || 8,
        by_product: [],
        by_severity: {},
        trend: [],
        top_violators: [],
        recent_decisions: [],
        total_active: 0,
        generated_at: new Date().toISOString(),
      }),
    });
  });

  await page.goto("/atlas/admin");
  await page.getByRole("button", { name: "24w" }).click();
  await page.waitForFunction(() => true);
  expect(lastWeeks).toBe("24");
});
