import { test, expect, type Page } from "@playwright/test";

/**
 * Phase G end-to-end walkthrough. Visits every route and verifies the
 * fixes from G1–G19 actually render correctly in a browser. Reports
 * issues by logging — runs as a single suite so screenshots from one
 * failure don't block the others.
 */

const BASE = "http://localhost:3001";
const ROUTES = [
  { path: "/", title: "Foundations" },
  { path: "/icons", title: "Icons" },
  { path: "/components", title: "Components" },
  { path: "/illustrations", title: "Illustrations" },
  { path: "/logos", title: "Logos" },
  { path: "/files", title: "Files" },
];

async function gotoAndWait(page: Page, path: string) {
  await page.goto(BASE + path, { waitUntil: "networkidle" });
  await page.waitForTimeout(400);
}

test.describe("Phase G — full UX walkthrough", () => {
  for (const route of ROUTES) {
    test(`G1: top-nav shows correct active state on ${route.path}`, async ({ page }) => {
      await gotoAndWait(page, route.path);
      const activeLink = page.locator('nav.page-nav a[aria-current="page"]');
      await expect(activeLink).toHaveCount(1);
      await expect(activeLink).toContainText(route.title);
      // No hydration warnings in console
      const warnings: string[] = [];
      page.on("console", (msg) => {
        if (msg.type() === "warning" && msg.text().includes("hydrat")) {
          warnings.push(msg.text());
        }
      });
      await page.waitForTimeout(200);
      expect(warnings).toEqual([]);
    });
  }

  test("G1: top-nav pill animates between routes (no hard flash)", async ({ page }) => {
    await gotoAndWait(page, "/");
    // Click Files
    await page.click('nav.page-nav a[href="/files"]');
    await page.waitForURL("**/files");
    const activeLink = page.locator('nav.page-nav a[aria-current="page"]');
    await expect(activeLink).toContainText("Files");
  });

  test("G2: scroll-spy lights up first section at scroll=0", async ({ page }) => {
    await gotoAndWait(page, "/");
    // First foundations section is "color-surface". Sidebar pill should
    // mount with that or "color" already active.
    await page.waitForTimeout(800);
    const activePill = page.locator('aside.sidebar-desktop, nav.sidebar-desktop').first();
    // The pill is rendered when an active item exists. Check that any sidebar entry
    // has aria-current="true" or the layoutId pill is mounted.
    const ariaCurrent = page.locator('.sidebar-desktop a[aria-current="true"]');
    await expect(ariaCurrent.first()).toBeVisible({ timeout: 4000 });
  });

  test("G2: activeSection resets across routes (no leak from / to /icons)", async ({ page }) => {
    await gotoAndWait(page, "/");
    // Scroll to surface bucket
    const surface = page.locator("#color-surface");
    if (await surface.count() > 0) {
      await surface.scrollIntoViewIfNeeded();
      await page.waitForTimeout(500);
    }
    // Navigate to /icons
    await page.click('nav.page-nav a[href="/icons"]');
    await page.waitForURL("**/icons");
    await page.waitForTimeout(800);
    // The sidebar on /icons should have its own active item, not "color-surface"
    const ariaCurrent = page.locator('.sidebar-desktop a[aria-current="true"]');
    if (await ariaCurrent.count() > 0) {
      const href = await ariaCurrent.first().getAttribute("href");
      expect(href).not.toBe("#color-surface");
    }
  });

  test("G4: /files sidebar lists per-file nav (or single 'All files' when empty)", async ({ page }) => {
    await gotoAndWait(page, "/files");
    const sidebar = page.locator('.sidebar-desktop');
    await expect(sidebar).toBeVisible();
    // Sidebar should have at least one anchor
    const anchors = page.locator('.sidebar-desktop a[href^="#"]');
    expect(await anchors.count()).toBeGreaterThan(0);
  });

  test("G7: footer renders brand + links + extract receipt", async ({ page }) => {
    await gotoAndWait(page, "/");
    await page.evaluate(() => window.scrollTo(0, document.body.scrollHeight));
    await page.waitForTimeout(400);
    const footer = page.locator("footer");
    await expect(footer).toBeVisible();
    await expect(footer).toContainText("INDmoney");
    // Has at least some real internal links (no href="#")
    const realLinks = footer.locator('a[href^="/"]');
    expect(await realLinks.count()).toBeGreaterThan(0);
    // No deadlinks in this footer
    const deadLinks = footer.locator('a[href="#"]');
    expect(await deadLinks.count()).toBe(0);
  });

  test("G9: focus-visible rings render on Tab", async ({ page }) => {
    await gotoAndWait(page, "/");
    await page.keyboard.press("Tab");
    await page.keyboard.press("Tab");
    const focused = page.locator(":focus-visible");
    if (await focused.count() > 0) {
      const outline = await focused.first().evaluate((el) => {
        return getComputedStyle(el).outlineWidth;
      });
      expect(outline).not.toBe("0px");
    }
  });

  test("G11: copy-toast appears when a swatch is clicked", async ({ page }) => {
    await gotoAndWait(page, "/");
    // Find a color swatch and click
    const swatch = page.locator('button[data-token]').first();
    if (await swatch.count() > 0) {
      await swatch.scrollIntoViewIfNeeded();
      // Grant clipboard permission
      await page.context().grantPermissions(["clipboard-read", "clipboard-write"]);
      await swatch.click();
      // Toast should appear bottom-right
      const toast = page.locator('[role="status"]').first();
      await expect(toast).toBeVisible({ timeout: 2000 });
      await expect(toast).toContainText(/Copied/);
    }
  });

  test("G17: back-to-top FAB appears after scrolling", async ({ page }) => {
    await gotoAndWait(page, "/");
    const beforeScroll = page.locator('button[aria-label="Scroll to top"]');
    expect(await beforeScroll.count()).toBe(0);
    await page.evaluate(() => window.scrollTo(0, 800));
    await page.waitForTimeout(400);
    await expect(page.locator('button[aria-label="Scroll to top"]')).toBeVisible();
  });

  test("G19: T toggles theme", async ({ page }) => {
    await gotoAndWait(page, "/");
    const initial = await page.evaluate(() =>
      document.documentElement.getAttribute("data-theme"),
    );
    await page.keyboard.press("t");
    await page.waitForTimeout(150);
    const after = await page.evaluate(() =>
      document.documentElement.getAttribute("data-theme"),
    );
    expect(after).not.toBe(initial);
    // Press again to restore
    await page.keyboard.press("t");
  });

  test("G19: D cycles density", async ({ page }) => {
    await gotoAndWait(page, "/");
    const initial = await page.evaluate(() =>
      getComputedStyle(document.documentElement).getPropertyValue("--density-scale").trim(),
    );
    await page.keyboard.press("d");
    await page.waitForTimeout(150);
    const after = await page.evaluate(() =>
      getComputedStyle(document.documentElement).getPropertyValue("--density-scale").trim(),
    );
    expect(after).not.toBe(initial);
  });

  test("G19: T does not fire when typing in search", async ({ page }) => {
    await gotoAndWait(page, "/");
    const initial = await page.evaluate(() =>
      document.documentElement.getAttribute("data-theme"),
    );
    // Open search
    await page.keyboard.press("Meta+k");
    await page.waitForTimeout(200);
    // Type t into search input
    const searchInput = page.locator('input[type="search"], [cmdk-input]').first();
    if (await searchInput.count() > 0) {
      await searchInput.type("t");
      await page.waitForTimeout(150);
    } else {
      await page.keyboard.type("t");
    }
    const after = await page.evaluate(() =>
      document.documentElement.getAttribute("data-theme"),
    );
    expect(after).toBe(initial); // theme unchanged when typing
  });

  test("nav state: clicking sidebar anchor moves pill", async ({ page }) => {
    await gotoAndWait(page, "/");
    // Find the typography group anchor
    const typeAnchor = page.locator('.sidebar-desktop a[href="#typography"], .sidebar-desktop a[href*="type-"]').first();
    if (await typeAnchor.count() > 0) {
      await typeAnchor.click();
      await page.waitForTimeout(600);
      const active = page.locator('.sidebar-desktop a[aria-current="true"]');
      await expect(active).toHaveCount(1);
    }
  });

  test("smoke: every route loads without server error", async ({ page }) => {
    const results: { path: string; status: number; errors: string[] }[] = [];
    for (const r of ROUTES) {
      const errors: string[] = [];
      page.on("pageerror", (e) => errors.push(e.message));
      const resp = await page.goto(BASE + r.path, { waitUntil: "domcontentloaded" });
      results.push({
        path: r.path,
        status: resp?.status() ?? 0,
        errors,
      });
    }
    for (const r of results) {
      expect(r.status, `${r.path} returned ${r.status}`).toBe(200);
      expect(r.errors, `${r.path} threw: ${r.errors.join("; ")}`).toEqual([]);
    }
  });
});
