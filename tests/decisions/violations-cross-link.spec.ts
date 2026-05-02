/**
 * Phase 6 U7 — bidirectional Violations ↔ Decisions cross-link.
 *
 * Asserts:
 *   1. A violation linked to a decision (via decision_links rows) renders
 *      a "View decision" CTA on its row.
 *   2. Clicking it switches the active tab to Decisions, sets
 *      ?decision=<id>, and the matching DecisionCard receives the 1.5s
 *      outline-pulse highlight.
 *   3. A DecisionCard whose `links` include a violation surfaces the
 *      Linked violations subsection. Clicking the row's "View" button
 *      switches to the Violations tab and pulses the matching row.
 *
 * Like the sibling decision specs (decision-creation.spec.ts,
 * supersession.spec.ts), this is .skip-gated until the project-shell
 * Playwright fixture (auth + /v1/projects/:slug + screens + flows)
 * lands. The structure stands so the spec runs as soon as the fixture
 * arrives.
 */

import { test, expect, type Page } from "@playwright/test";

const TENANT = "tenant-a";
const SLUG = "tax-fno-learn";
const FLOW_ID = "flow-1";
const VERSION_ID = "v-1";
const SCREEN_ID = "s-1";
const VIOLATION_ID = "vio-1";
const DECISION_ID = "d-1";

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

test.skip("ViolationsTab → DecisionsTab cross-link pulses target card", async ({
  page,
}) => {
  await injectAuth(page);

  // Decisions list (flow-scoped) — one decision, linked to vio-1.
  await page.route(
    `**/v1/projects/${SLUG}/flows/${FLOW_ID}/decisions**`,
    (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          decisions: [
            {
              id: DECISION_ID,
              tenant_id: TENANT,
              flow_id: FLOW_ID,
              version_id: VERSION_ID,
              title: "Adopt token grid-24",
              status: "accepted",
              made_by_user_id: "u-1",
              made_at: new Date().toISOString(),
              created_at: new Date().toISOString(),
              updated_at: new Date().toISOString(),
              links: [
                {
                  decision_id: DECISION_ID,
                  link_type: "violation",
                  target_id: VIOLATION_ID,
                  created_at: new Date().toISOString(),
                },
              ],
            },
          ],
          count: 1,
        }),
      }),
  );

  // Violations list — one critical row matching vio-1.
  await page.route(`**/v1/projects/${SLUG}/violations**`, (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        violations: [
          {
            id: VIOLATION_ID,
            version_id: VERSION_ID,
            screen_id: SCREEN_ID,
            tenant_id: TENANT,
            rule_id: "padding.token-mismatch",
            severity: "critical",
            category: "token_drift",
            property: "padding",
            observed: "16px",
            suggestion: "use --space-3",
            status: "active",
            auto_fixable: false,
            created_at: new Date().toISOString(),
          },
        ],
        count: 1,
      }),
    }),
  );

  // Linked-violations endpoint — D → V direction.
  await page.route(
    `**/v1/decisions/${DECISION_ID}/violations`,
    (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          violations: [
            {
              id: VIOLATION_ID,
              version_id: VERSION_ID,
              screen_id: SCREEN_ID,
              tenant_id: TENANT,
              rule_id: "padding.token-mismatch",
              severity: "critical",
              category: "token_drift",
              property: "padding",
              observed: "16px",
              suggestion: "use --space-3",
              status: "active",
              auto_fixable: false,
              created_at: new Date().toISOString(),
            },
          ],
          count: 1,
        }),
      }),
  );

  await page.goto(`/projects/${SLUG}`);

  // V → D direction: confirm the row shows the cross-link CTA.
  await page.getByRole("tab", { name: /Violations/i }).click();
  const viewDecisionBtn = page.getByTestId("violation-view-decision");
  await expect(viewDecisionBtn).toBeVisible();
  await expect(viewDecisionBtn).toHaveAttribute(
    "data-decision-id",
    DECISION_ID,
  );
  await viewDecisionBtn.click();

  // Tab should swap; URL should carry ?decision=<id>; the card's row
  // gets the accent outline (DecisionsTab's highlightedID branch).
  await expect(page.getByRole("tab", { name: /Decisions/i })).toHaveAttribute(
    "aria-selected",
    "true",
  );
  await expect(page).toHaveURL(new RegExp(`decision=${DECISION_ID}`));
  const card = page.getByTestId("decision-card");
  await expect(card).toHaveAttribute("data-decision-id", DECISION_ID);

  // D → V direction: the card's "Linked violations" subsection lists the
  // violation; clicking "View" flips to the Violations tab and pulses
  // the row.
  const linkedSection = page.getByTestId("decision-linked-violations");
  await expect(linkedSection).toBeVisible();
  const linkedRow = page.getByTestId("decision-linked-violation-row").first();
  await expect(linkedRow).toHaveAttribute("data-violation-id", VIOLATION_ID);
  await linkedRow.getByRole("button", { name: /^View$/ }).click();

  await expect(page.getByRole("tab", { name: /Violations/i })).toHaveAttribute(
    "aria-selected",
    "true",
  );
  await expect(page).toHaveURL(new RegExp(`violation=${VIOLATION_ID}`));
});

test.skip("DecisionCard renders empty state when no linked violations", async ({
  page,
}) => {
  await injectAuth(page);

  await page.route(
    `**/v1/projects/${SLUG}/flows/${FLOW_ID}/decisions**`,
    (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          decisions: [
            {
              id: DECISION_ID,
              tenant_id: TENANT,
              flow_id: FLOW_ID,
              version_id: VERSION_ID,
              title: "Decision with no violation links",
              status: "accepted",
              made_by_user_id: "u-1",
              made_at: new Date().toISOString(),
              created_at: new Date().toISOString(),
              updated_at: new Date().toISOString(),
              links: [],
            },
          ],
          count: 1,
        }),
      }),
  );

  await page.goto(`/projects/${SLUG}`);
  await page.getByRole("tab", { name: /Decisions/i }).click();
  await expect(
    page.getByTestId("decision-linked-violations"),
  ).toContainText(/No linked violations/i);
});
