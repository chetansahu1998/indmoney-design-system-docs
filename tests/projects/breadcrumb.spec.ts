/**
 * U2c — Static `/atlas › <flow name>` breadcrumb on `ProjectToolbar`.
 *
 * The breadcrumb is **always-on** (not gated by `useReducedMotion()`):
 *   - Motion users: standard wayfinding.
 *   - Reduced-motion + Firefox-default users: serves as the *primary*
 *     spatial-continuity substitute for the cross-route morph (per the
 *     corrected Reduced-Motion + Unsupported Browser Strategy in the
 *     Phase plan's Key Technical Decisions section).
 *
 * Coverage:
 *   1. Happy path: breadcrumb renders on a project page with the `/atlas`
 *      link visible and the flow-name span carrying the project's title.
 *   2. Click `/atlas`: navigates to /atlas. Until U3 lands, this is plain
 *      navigation (no morph); afterwards the View Transitions API may
 *      animate, but the *navigation contract* (URL change to `/atlas`)
 *      is what we assert here.
 *   3. Edge case: flow paths containing slashes (e.g. "F&O / Learn")
 *      render literally without breaking the breadcrumb's structure —
 *      the slash is part of the flow-name text node, not the separator.
 *
 * What we do NOT test:
 *   - Visual position of the breadcrumb (above vs alongside title) —
 *     designer's call; either layout satisfies the spec as long as both
 *     elements are present.
 *   - View Transitions pseudo-element animations — those are browser-
 *     internal and Playwright cannot reliably introspect them. Same
 *     boundary as `atlas-leaf-morph.spec.ts`.
 *
 * Strategy: piggyback on the same Playwright stub pattern as
 * `canvas-render.spec.ts` so we don't need a live ds-service. Auth is
 * injected via the persisted-zustand localStorage blob.
 */

import { test, expect, type Page } from "@playwright/test";
import {
  FIXTURE_PERSONAS,
  FIXTURE_PROJECT,
  FIXTURE_SCREENS,
  FIXTURE_VERSIONS,
  FIXTURE_VIOLATIONS,
} from "./fixtures/project-fixtures";

const DS_URL = "http://localhost:8080";

async function loginAs(page: Page, tenant: string): Promise<void> {
  await page.addInitScript(
    ({ tenantID }) => {
      const blob = {
        state: {
          token: `fake-jwt-${tenantID}`,
          email: "designer@example.com",
          role: "designer",
        },
        version: 0,
      };
      window.localStorage.setItem("indmoney-ds-auth", JSON.stringify(blob));
    },
    { tenantID: tenant },
  );
}

/**
 * Stubs the ds-service responses that ProjectShell needs to render. The
 * `projectName` parameter overrides FIXTURE_PROJECT.Name so individual
 * tests can exercise edge-case names (e.g. one with slashes) without
 * mutating the shared fixture.
 */
async function mockDSService(
  page: Page,
  opts: { projectName?: string } = {},
): Promise<void> {
  const project = opts.projectName
    ? { ...FIXTURE_PROJECT, Name: opts.projectName }
    : FIXTURE_PROJECT;

  await page.route(`${DS_URL}/v1/projects`, (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ projects: [project], count: 1 }),
    }),
  );

  await page.route(
    new RegExp(`^${DS_URL}/v1/projects/${FIXTURE_PROJECT.Slug}(\\?|$)`),
    (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          project,
          versions: FIXTURE_VERSIONS,
          screens: FIXTURE_SCREENS,
          available_personas: FIXTURE_PERSONAS,
        }),
      }),
  );

  await page.route(
    new RegExp(`^${DS_URL}/v1/projects/${FIXTURE_PROJECT.Slug}/violations`),
    (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          violations: FIXTURE_VIOLATIONS,
          count: FIXTURE_VIOLATIONS.length,
        }),
      }),
  );

  await page.route(
    new RegExp(
      `^${DS_URL}/v1/projects/${FIXTURE_PROJECT.Slug}/events/ticket`,
    ),
    (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          ticket: "tkt-fake",
          trace_id: "trace-fake",
          expires_in: 60,
        }),
      }),
  );

  await page.route(
    new RegExp(`^${DS_URL}/v1/projects/${FIXTURE_PROJECT.Slug}/events\\?`),
    (route) =>
      route.fulfill({
        status: 200,
        contentType: "text/event-stream",
        body: ": keepalive\n\n",
      }),
  );
}

test.describe("U2c — /atlas breadcrumb on project toolbar", () => {
  test("renders breadcrumb with /atlas link + flow name", async ({ page }) => {
    await loginAs(page, "tenant-alpha");
    await mockDSService(page);

    await page.goto(`/projects/${FIXTURE_PROJECT.Slug}`);

    const toolbar = page.locator('[data-anim="toolbar"]');
    await expect(toolbar).toBeVisible();

    const breadcrumb = page.locator('[data-tour="atlas-breadcrumb"]');
    await expect(breadcrumb).toBeVisible();

    // /atlas link is present and points at the atlas route.
    const atlasLink = page.locator('[data-testid="atlas-breadcrumb-link"]');
    await expect(atlasLink).toBeVisible();
    await expect(atlasLink).toHaveAttribute("href", "/atlas");
    await expect(atlasLink).toHaveText("/atlas");

    // Flow name renders and matches the project title (`flowName ??
    // project.Name` resolves to `project.Name` since ProjectShell passes
    // `flowName={initialProject.Name}`).
    const flowSpan = page.locator('[data-testid="atlas-breadcrumb-flow"]');
    await expect(flowSpan).toHaveText(FIXTURE_PROJECT.Name);

    // The toolbar's existing project-title carries the same string —
    // breadcrumb flow + title are wired to the same source.
    const titleEl = page.locator('[data-tour="project-title"]').first();
    await expect(titleEl).toHaveText(FIXTURE_PROJECT.Name);
  });

  test("clicking /atlas navigates to /atlas", async ({ page }) => {
    await loginAs(page, "tenant-alpha");
    await mockDSService(page);

    await page.goto(`/projects/${FIXTURE_PROJECT.Slug}`);
    await expect(page.locator('[data-anim="toolbar"]')).toBeVisible();

    const atlasLink = page.locator('[data-testid="atlas-breadcrumb-link"]');
    await expect(atlasLink).toBeVisible();

    await atlasLink.click();

    // Until U3 ships, this is a plain navigation; once U3 lands the
    // View Transitions API may animate the transition. Either way, the
    // URL contract is the same: we land on /atlas.
    await page.waitForURL(/\/atlas(?:[?#]|$)/, { timeout: 10_000 });
  });

  test("flow names with slashes render literally without breaking the breadcrumb", async ({
    page,
  }) => {
    await loginAs(page, "tenant-alpha");
    await mockDSService(page, { projectName: "F&O / Learn" });

    await page.goto(`/projects/${FIXTURE_PROJECT.Slug}`);
    await expect(page.locator('[data-anim="toolbar"]')).toBeVisible();

    const flowSpan = page.locator('[data-testid="atlas-breadcrumb-flow"]');
    await expect(flowSpan).toHaveText("F&O / Learn");

    // /atlas link is unaffected — the slash in the flow name does not
    // bleed into the link text or alter its href.
    const atlasLink = page.locator('[data-testid="atlas-breadcrumb-link"]');
    await expect(atlasLink).toHaveText("/atlas");
    await expect(atlasLink).toHaveAttribute("href", "/atlas");

    // The breadcrumb still has exactly one /atlas link (no second one
    // accidentally introduced by the slash in the flow name).
    const breadcrumb = page.locator('[data-tour="atlas-breadcrumb"]');
    await expect(breadcrumb.locator("a")).toHaveCount(1);
  });
});
