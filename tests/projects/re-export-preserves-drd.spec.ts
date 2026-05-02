/**
 * U11 — AE-6 re-export preserves DRD, refreshes audit Playwright spec.
 *
 * Covers brainstorm AE-6 from
 *   docs/brainstorms/2026-04-29-projects-flow-atlas-requirements.md
 *
 *   "Two weeks later, Aanya re-exports the same flow with new screens.
 *    New version v2 created. DRD content unchanged. Decisions from v1
 *    stay on v1; new decisions made on v2. Violations recomputed; 2 of
 *    4 v1 violations now Fixed (auto-detected), 1 still Active, 1
 *    Acknowledged-from-v1 carries forward, 3 new violations introduced."
 *
 * What this spec asserts (the user-visible end-to-end story):
 *   1. The version selector lists both v1 and v2 after re-export.
 *   2. Switching between v1 and v2 keeps the DRD body unchanged
 *      (DRD is per-flow, living, does NOT migrate per version — R5).
 *   3. Decisions made on v1 stay attached to v1; v2 has no decisions.
 *   4. Violations on v2 reflect the carry-forward / fixed / new shape:
 *      2 Fixed, 1 carry-forward Active, 1 carry-forward Acknowledged,
 *      3 brand-new — 4 still-listed total (the 2 Fixed aren't in the
 *      default Active filter), with the right status mix.
 *
 * Why this test is `test.skip()`'d
 * ────────────────────────────────
 * Same rationale as `tests/projects/violation-lifecycle.spec.ts` and
 * `tests/decisions/decision-creation.spec.ts`: the project-shell
 * Playwright fixture builder (auth + /v1/projects/:slug + flows +
 * screens) hasn't shipped yet. The mock route shape below is the
 * load-bearing contract; when the fixture lands the assertions run
 * unchanged. `docs/runbooks/playwright-coverage.md` tracks the gap.
 */

import { test, expect, type Page } from "@playwright/test";

const TENANT = "tenant-a";
const SLUG = "tax-fno-learn";
const FLOW_ID = "flow-1";
const VERSION_V1 = "v-1";
const VERSION_V2 = "v-2";

const DRD_BODY_TEXT = "v1 narrative — adopted padding-32 over grid-24";

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

/**
 * Wire all the route mocks AE-6 depends on. Kept as a helper so any
 * future variants (v3, v4, only-fixes-no-new, etc.) can branch from
 * the same baseline.
 */
async function mockRoutes(page: Page): Promise<void> {
  // Project shell + version list — both versions are visible.
  await page.route(`**/v1/projects/${SLUG}`, (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        slug: SLUG,
        name: "Tax F&O Learn Touchpoints",
        platform: "mobile",
        product: "Tax",
        path: "F&O/Learn",
        versions: [
          {
            id: VERSION_V1,
            project_slug: SLUG,
            version_index: 1,
            status: "complete",
            created_at: "2026-04-15T10:00:00Z",
          },
          {
            id: VERSION_V2,
            project_slug: SLUG,
            version_index: 2,
            status: "complete",
            created_at: "2026-04-29T10:00:00Z",
          },
        ],
        latest_version_id: VERSION_V2,
      }),
    }),
  );

  // DRD — per-flow, NOT per-version. The same body returns regardless
  // of which version is active. This is the AE-6 invariant.
  await page.route(
    `**/v1/projects/${SLUG}/flows/${FLOW_ID}/drd**`,
    (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          flow_id: FLOW_ID,
          body_json: {
            type: "doc",
            content: [
              {
                type: "paragraph",
                content: [{ type: "text", text: DRD_BODY_TEXT }],
              },
            ],
          },
          updated_at: "2026-04-15T10:00:00Z",
        }),
      }),
  );

  // Decisions — flow-scoped list. v1 has one decision (made_on_version_id
  // === v-1); v2 has none. The Decisions tab filters by current version
  // when the user switches; the API returns the union and the UI scopes.
  await page.route(
    `**/v1/projects/${SLUG}/flows/${FLOW_ID}/decisions**`,
    (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          decisions: [
            {
              id: "d-1",
              tenant_id: TENANT,
              flow_id: FLOW_ID,
              version_id: VERSION_V1,
              title: "Approved padding-32 over grid-24",
              status: "accepted",
              made_by_user_id: "u-1",
              made_at: "2026-04-15T11:00:00Z",
              created_at: "2026-04-15T11:00:00Z",
              updated_at: "2026-04-15T11:00:00Z",
              links: [],
            },
          ],
          count: 1,
        }),
      }),
  );

  // Violations — version-scoped. The route observes ?version_id and
  // returns the right shape per AE-6.
  //
  // v1 (4 violations, all the original audit findings):
  //   - 2 will be auto-resolved on re-audit (Fixed in v2)
  //   - 1 stays Active
  //   - 1 Acknowledged
  //
  // v2 (4 currently-listed violations after re-audit):
  //   - 1 carry-forward Active (the same offending node)
  //   - 1 carry-forward Acknowledged-from-v1
  //   - 3 net-new violations introduced by the new screens
  //   (the 2 Fixed-by-re-audit rows aren't in the default Active list;
  //   they're tagged status=fixed and shown only when the Fixed filter
  //   is on)
  await page.route(`**/v1/projects/${SLUG}/violations**`, (route) => {
    const url = new URL(route.request().url());
    const versionID = url.searchParams.get("version_id") ?? VERSION_V2;

    if (versionID === VERSION_V1) {
      return route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          violations: [
            v1Violation("v1-fixed-1", "active", "padding.token-mismatch"),
            v1Violation("v1-fixed-2", "active", "color.token-drift"),
            v1Violation("v1-active-1", "active", "radius.pill-rule"),
            v1Violation(
              "v1-ack-1",
              "acknowledged",
              "cross_persona.component_coverage_gap",
            ),
          ],
          count: 4,
        }),
      });
    }

    // version v2 — the carry-forward + new shape.
    return route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        violations: [
          // Carry-forward Active (same node, same rule, still tripping).
          v2Violation(
            "v2-carry-active",
            "active",
            "radius.pill-rule",
            "v1-active-1",
          ),
          // Carry-forward Acknowledged — keeps the v1 acknowledgement.
          v2Violation(
            "v2-carry-ack",
            "acknowledged",
            "cross_persona.component_coverage_gap",
            "v1-ack-1",
          ),
          // Three brand-new violations introduced by the v2 screens.
          v2Violation("v2-new-1", "active", "spacing.grid-drift"),
          v2Violation("v2-new-2", "active", "text.style-drift"),
          v2Violation("v2-new-3", "active", "a11y.contrast-aa"),
        ],
        count: 5,
      }),
    });
  });
}

function v1Violation(id: string, status: string, ruleID: string) {
  return {
    id,
    version_id: VERSION_V1,
    screen_id: "s-1",
    tenant_id: TENANT,
    rule_id: ruleID,
    severity: "high",
    category: "drift",
    property: "x",
    observed: "x",
    suggestion: "x",
    status,
    auto_fixable: false,
    created_at: "2026-04-15T11:00:00Z",
  };
}

function v2Violation(
  id: string,
  status: string,
  ruleID: string,
  carryFromID?: string,
) {
  return {
    id,
    version_id: VERSION_V2,
    screen_id: "s-1",
    tenant_id: TENANT,
    rule_id: ruleID,
    severity: "high",
    category: "drift",
    property: "x",
    observed: "x",
    suggestion: "x",
    status,
    auto_fixable: false,
    created_at: "2026-04-29T11:00:00Z",
    ...(carryFromID ? { carry_forward_from: carryFromID } : {}),
  };
}

test.skip("AE-6 — version selector lists v1 + v2 after re-export", async ({
  page,
}) => {
  await injectAuth(page);
  await mockRoutes(page);
  await page.goto(`/projects/${SLUG}`);

  const selector = page.getByTestId("project-version-selector");
  await expect(selector).toBeVisible();
  await expect(selector).toContainText(/v1/);
  await expect(selector).toContainText(/v2/);
});

test.skip("AE-6 — DRD body is unchanged when switching v1 ↔ v2", async ({
  page,
}) => {
  await injectAuth(page);
  await mockRoutes(page);

  await page.goto(`/projects/${SLUG}?v=${VERSION_V2}`);
  await page.getByRole("tab", { name: /DRD/i }).click();
  const editor = page.locator('[data-testid="drd-editor"]');
  await expect(editor).toContainText(DRD_BODY_TEXT);

  // Switch to v1 — same DRD body. R5 invariant: DRD does not migrate
  // per version. The editor must show the same content text.
  await page.getByTestId("project-version-selector").click();
  await page.getByRole("option", { name: /v1/ }).click();
  await expect(editor).toContainText(DRD_BODY_TEXT);
});

test.skip("AE-6 — v1 decisions stay on v1; v2 has none yet", async ({
  page,
}) => {
  await injectAuth(page);
  await mockRoutes(page);

  await page.goto(`/projects/${SLUG}?v=${VERSION_V1}`);
  await page.getByRole("tab", { name: /Decisions/i }).click();
  await expect(
    page.getByText("Approved padding-32 over grid-24"),
  ).toBeVisible();

  // Switch to v2 — same Decisions endpoint, but the UI scopes to the
  // active version. v2 has no decisions yet.
  await page.getByTestId("project-version-selector").click();
  await page.getByRole("option", { name: /v2/ }).click();
  await expect(page.getByText(/No decisions yet/i)).toBeVisible();
});

test.skip("AE-6 — v2 violations reflect carry-forward + new shape", async ({
  page,
}) => {
  await injectAuth(page);
  await mockRoutes(page);

  await page.goto(`/projects/${SLUG}?v=${VERSION_V2}`);
  await page.getByRole("tab", { name: /Violations/i }).click();

  // 5 listed rows: 1 carry-forward Active, 1 carry-forward Acknowledged,
  // 3 brand-new Active. The 2 Fixed v1 violations don't appear in the
  // default Active list.
  const rows = page.locator("[data-violation-row]");
  await expect(rows).toHaveCount(5);

  // Carry-forward Active.
  await expect(
    rows.filter({ hasText: /radius\.pill-rule/i }),
  ).toHaveAttribute("data-status", "active");

  // Carry-forward Acknowledged.
  await expect(
    rows.filter({ hasText: /cross_persona\.component_coverage_gap/i }),
  ).toHaveAttribute("data-status", "acknowledged");

  // Three brand-new rows — verify by rule_id.
  await expect(rows.filter({ hasText: /spacing\.grid-drift/i })).toHaveCount(
    1,
  );
  await expect(rows.filter({ hasText: /text\.style-drift/i })).toHaveCount(1);
  await expect(rows.filter({ hasText: /a11y\.contrast-aa/i })).toHaveCount(1);
});
