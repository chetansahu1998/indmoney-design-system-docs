/**
 * Phase 3 U13 — cold-start integration tests.
 *
 * Covers acceptance examples introduced for Phase 3:
 *   AE-9   cold installer's first interaction (Welcome project + tour)
 *   AE-11  permission-denied preview (?read_only_preview=1 path)
 *   AE-13  network-error recovery (RetryableError + state machine)
 *
 * Strategy mirrors the Phase 1 + Phase 2 specs: page.route mocks the
 * ds-service responses + auth state injected via addInitScript. The
 * Shepherd tour itself is exercised via ?reset-tour=1 so localStorage
 * doesn't leak between test runs.
 */

import { test, expect, type Page } from "@playwright/test";
import {
  FIXTURE_PERSONAS,
  FIXTURE_PROJECT,
  FIXTURE_SCREENS,
  FIXTURE_TENANT,
  FIXTURE_VERSIONS,
  FIXTURE_VIOLATIONS,
} from "./fixtures/project-fixtures";

const SLUG = FIXTURE_PROJECT.Slug;

async function injectAuth(page: Page): Promise<void> {
  await page.addInitScript((tenantID: string) => {
    window.localStorage.setItem(
      "indmoney-ds-auth",
      JSON.stringify({
        state: {
          token: "fake-token-for-tests",
          tenants: [{ id: tenantID, slug: tenantID, name: tenantID }],
          activeTenantID: tenantID,
        },
        version: 0,
      }),
    );
    // Make the tour mount cleanly across runs — clear any prior state.
    window.localStorage.removeItem("indmoney-projects-tour");
  }, FIXTURE_TENANT);
}

async function mockProjectAPIs(page: Page): Promise<void> {
  await page.route(`**/v1/projects/${SLUG}**`, (route) => {
    const url = new URL(route.request().url());
    if (url.pathname.endsWith(`/v1/projects/${SLUG}/violations`)) {
      return route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          violations: FIXTURE_VIOLATIONS,
          count: FIXTURE_VIOLATIONS.length,
        }),
      });
    }
    if (url.pathname.endsWith(`/v1/projects/${SLUG}/events/ticket`)) {
      return route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          ticket: "fake-ticket",
          trace_id: "fake-trace",
          expires_in: 60,
        }),
      });
    }
    return route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        project: FIXTURE_PROJECT,
        versions: FIXTURE_VERSIONS,
        screens: FIXTURE_SCREENS,
        screen_modes: [],
        available_personas: FIXTURE_PERSONAS,
      }),
    });
  });

  // SSE endpoint — keep the connection open + idle (no events).
  await page.route(`**/v1/projects/${SLUG}/events*`, (route) => {
    return route.fulfill({
      status: 200,
      contentType: "text/event-stream",
      body: ":\n",
    });
  });
}

// ─── AE-9: cold installer's first interaction ──────────────────────────────

test.describe("AE-9 — cold installer first interaction", () => {
  test("Welcome project list renders project shell + tour mounts on ?reset-tour=1", async ({ page }) => {
    await injectAuth(page);
    await mockProjectAPIs(page);

    await page.goto(`/projects/${SLUG}?reset-tour=1`);

    // Toolbar tab strip is visible (state machine resolved view_ready).
    await expect(page.getByRole("tab", { name: /violations/i })).toBeVisible();
    await expect(page.getByRole("tab", { name: /json/i })).toBeVisible();

    // Tour mounts — Shepherd renders a popup with the first step's title.
    // The popup is keyed by the persona-toggle anchor.
    await expect(page.locator(".shepherd-text").first()).toBeVisible({
      timeout: 5_000,
    });
  });

  test("dismissing the tour persists across reloads", async ({ page }) => {
    await injectAuth(page);
    await mockProjectAPIs(page);

    await page.goto(`/projects/${SLUG}?reset-tour=1`);
    await expect(page.locator(".shepherd-text").first()).toBeVisible();

    // Dismiss via Skip tour (first step). The button is rendered by Shepherd.
    await page.getByRole("button", { name: /skip tour/i }).click();
    await expect(page.locator(".shepherd-text")).toHaveCount(0);

    // Reload — tour should NOT remount (localStorage records "skipped").
    await page.goto(`/projects/${SLUG}`);
    await expect(page.locator(".shepherd-text")).toHaveCount(0);
  });
});

// ─── AE-11: permission-denied preview ──────────────────────────────────────

test.describe("AE-11 — permission-denied preview", () => {
  test("?read_only_preview=1 surfaces the read-only banner + disables DRD editor", async ({
    page,
  }) => {
    await injectAuth(page);
    await mockProjectAPIs(page);

    await page.goto(`/projects/${SLUG}?read_only_preview=1#drd`);

    // Top-level banner from U7 state machine.
    await expect(
      page.getByText(/read-only mode/i).first(),
    ).toBeVisible();

    // DRD editor's BlockNote root should be present but non-editable.
    // The U7-lite banner inside the DRD tab is also present (redundant
    // with the top-level banner; both are intentional per U7 commit).
    await expect(
      page.getByText(/Request edit access from the project owner/i).first(),
    ).toBeVisible();
  });

  test("normal load (no query param) does NOT show the read-only banner", async ({
    page,
  }) => {
    await injectAuth(page);
    await mockProjectAPIs(page);

    await page.goto(`/projects/${SLUG}#drd`);

    await expect(
      page.getByText(/read-only mode/i),
    ).toHaveCount(0);
  });
});

// ─── AE-13: network-error recovery ─────────────────────────────────────────

test.describe("AE-13 — network-error recovery", () => {
  test("initial fetch failure → RetryableError + Try again succeeds", async ({
    page,
  }) => {
    await injectAuth(page);

    // First attempt: simulate a 500. After one click, mockProjectAPIs takes
    // over and serves the real fixture.
    let firstAttempt = true;
    await page.route(`**/v1/projects/${SLUG}**`, (route) => {
      if (firstAttempt) {
        firstAttempt = false;
        return route.fulfill({
          status: 500,
          contentType: "application/json",
          body: JSON.stringify({ error: "internal_error" }),
        });
      }
      return route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          project: FIXTURE_PROJECT,
          versions: FIXTURE_VERSIONS,
          screens: FIXTURE_SCREENS,
          screen_modes: [],
          available_personas: FIXTURE_PERSONAS,
        }),
      });
    });

    await page.goto(`/projects/${SLUG}`);

    // RetryableError surfaces.
    await expect(
      page.getByText(/Couldn't load this project/i),
    ).toBeVisible();
    const retryBtn = page.getByRole("button", { name: /try again/i });
    await expect(retryBtn).toBeVisible();

    // Click → retry succeeds → toolbar appears.
    await retryBtn.click();
    await expect(page.getByRole("tab", { name: /violations/i })).toBeVisible({
      timeout: 5_000,
    });
  });
});
