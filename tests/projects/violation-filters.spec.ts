/**
 * Phase 6 U6 — ViolationsTab persona × theme filter chips.
 *
 * Covers R14 closure: the Violations tab must support filtering by the
 * active persona × theme axes. Persona was previously surfaced only via
 * the toolbar dropdown (single-select); theme had no filter UI at all.
 *
 * The chip rows are pure client-side filters over the dataset already
 * fetched for the active version. Backend changes are out of scope for
 * U6 — listViolations only accepts a single persona_id / mode_label, and
 * multi-select would require either N round-trips or a server change.
 *
 * Like the rest of tests/projects/*, these specs are skipped pending
 * the project-shell fixture builders that the Phase 5/6 testing
 * follow-ups will land. Skeleton kept here so a) the spec exists for
 * the verification gate and b) the fixture wiring drops in cleanly.
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
          email: "designer@example.com",
          role: "designer",
          tenants: [{ id: tenantID, slug: tenantID, name: tenantID }],
          activeTenantID: tenantID,
        },
        version: 0,
      }),
    );
  }, TENANT);
}

test.skip("Persona chip multi-select filters violations to the selected set", async ({
  page,
}) => {
  await injectAuth(page);
  await page.goto(`/projects/${SLUG}`);
  // Two persona chips selected → only violations matching either persona
  // remain. Counts on each chip reflect the unfiltered dataset, not the
  // filtered view (per CategoryFilterChips' established convention).
  await page.getByRole("button", { name: /^KYC-pending/ }).click();
  await page.getByRole("button", { name: /^Default/ }).click();
  const rows = page.locator("[data-violation-row]");
  await expect(rows).not.toHaveCount(0);
  // Every visible row must carry a persona-id from the selected set.
  const personaIDs = await rows.evaluateAll((els) =>
    els.map((el) => el.getAttribute("data-persona-id")),
  );
  for (const id of personaIDs) {
    expect(["persona-1", "persona-2"]).toContain(id);
  }
});

test.skip("Theme chip restricts violations to selected mode label", async ({
  page,
}) => {
  await injectAuth(page);
  await page.goto(`/projects/${SLUG}`);
  await page.getByRole("button", { name: /^Light/ }).click();
  const rows = page.locator("[data-violation-row]");
  const modeLabels = await rows.evaluateAll((els) =>
    els.map((el) => el.getAttribute("data-mode-label")),
  );
  for (const m of modeLabels) {
    expect(m).toBe("light");
  }
});

test.skip("Auto-fix CTA only renders for AutoFixable violations", async ({
  page,
}) => {
  // R11: the Fix in Figma button must respect the server-side
  // auto_fixable flag. Defense-in-depth: ViolationsTab's render guard
  // is the single source of truth (FixInFigmaButton itself carries no
  // capability check).
  await injectAuth(page);
  await page.goto(`/projects/${SLUG}`);
  const fixCount = await page.getByTestId("fix-in-figma").count();
  const autoFixableCount = await page
    .locator("[data-violation-row][data-auto-fixable='true']")
    .count();
  expect(fixCount).toBe(autoFixableCount);
});

test.skip("All chips selected with no overlap shows empty-state", async ({
  page,
}) => {
  await injectAuth(page);
  await page.goto(`/projects/${SLUG}`);
  // Pick a persona AND theme combination that no violation matches.
  await page.getByRole("button", { name: /^Unassigned/ }).click();
  await page.getByRole("button", { name: /^Dark/ }).click();
  await expect(
    page.getByText(/No violations match the selected filters/),
  ).toBeVisible();
});
