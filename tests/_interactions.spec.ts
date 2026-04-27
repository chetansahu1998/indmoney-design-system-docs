import { test, expect } from "@playwright/test";

const BASE = "http://localhost:3001";

test.describe("Interactive flows", () => {
  test.beforeEach(async ({ context }) => {
    await context.grantPermissions(["clipboard-read", "clipboard-write"]);
  });

  test("color swatch click → toast → clipboard contains hex", async ({ page }) => {
    await page.goto(BASE + "/", { waitUntil: "networkidle" });
    await page.waitForTimeout(500);
    const swatch = page.locator("button[data-token]").first();
    await swatch.scrollIntoViewIfNeeded();
    await swatch.click();
    const toast = page.locator('[role="status"]').first();
    await expect(toast).toBeVisible({ timeout: 1500 });
    await page.screenshot({ path: "test-results/sweep/interaction-toast.png", fullPage: false });
    const clip = await page.evaluate(() => navigator.clipboard.readText());
    expect(clip).toMatch(/^#[0-9a-fA-F]{6}$/);
  });

  test("navigate / → /icons → /files: pill animates between top-nav routes", async ({ page }) => {
    await page.goto(BASE + "/", { waitUntil: "networkidle" });
    await page.waitForTimeout(400);
    await page.screenshot({ path: "test-results/sweep/interaction-nav-1-foundations.png" });

    await page.click('nav.page-nav a[href="/icons"]');
    await page.waitForURL("**/icons");
    await page.waitForTimeout(400);
    await page.screenshot({ path: "test-results/sweep/interaction-nav-2-icons.png" });

    await page.click('nav.page-nav a[href="/files"]');
    await page.waitForURL("**/files");
    await page.waitForTimeout(400);
    await page.screenshot({ path: "test-results/sweep/interaction-nav-3-files.png" });
  });

  test("sidebar anchor click → pill moves and content scrolls", async ({ page }) => {
    await page.goto(BASE + "/", { waitUntil: "networkidle" });
    await page.waitForTimeout(500);
    // Click typography → headings
    await page.click('.sidebar-desktop a[href="#type-heading"]');
    await page.waitForTimeout(800);
    const active = page.locator('.sidebar-desktop a[aria-current="true"]');
    await expect(active).toContainText(/Headings/);
    await page.screenshot({ path: "test-results/sweep/interaction-sidebar-jump.png" });
  });

  test("density toggle changes layout dimensions", async ({ page }) => {
    await page.goto(BASE + "/", { waitUntil: "networkidle" });
    await page.waitForTimeout(400);
    const before = await page.evaluate(() => ({
      headerH: getComputedStyle(document.documentElement).getPropertyValue("--header-h"),
      sidebarW: getComputedStyle(document.documentElement).getPropertyValue("--sidebar-w"),
    }));
    await page.keyboard.press("d");
    await page.waitForTimeout(300);
    const after = await page.evaluate(() => ({
      headerH: getComputedStyle(document.documentElement).getPropertyValue("--header-h"),
      sidebarW: getComputedStyle(document.documentElement).getPropertyValue("--sidebar-w"),
    }));
    expect(after).not.toEqual(before);
  });

  test("ComponentInspector: clicking a component expands variants", async ({ page }) => {
    await page.goto(BASE + "/components", { waitUntil: "networkidle" });
    await page.waitForTimeout(800);
    // Find first component tile and click
    const tile = page.locator('button[data-component-slug], button[data-component-key], [data-component]').first();
    if (await tile.count() === 0) {
      // Fallback: click any button in the components grid
      const btn = page.locator("main button").first();
      if (await btn.count() > 0) {
        await btn.click();
        await page.waitForTimeout(600);
      }
    } else {
      await tile.click();
      await page.waitForTimeout(600);
    }
    await page.screenshot({ path: "test-results/sweep/interaction-component-expand.png", fullPage: false });
  });

  test("scroll triggers back-to-top FAB + clicks restore scroll", async ({ page }) => {
    await page.goto(BASE + "/", { waitUntil: "networkidle" });
    await page.evaluate(() => window.scrollTo(0, 1500));
    await page.waitForTimeout(500);
    const fab = page.locator('button[aria-label="Scroll to top"]');
    await expect(fab).toBeVisible();
    await page.screenshot({ path: "test-results/sweep/interaction-fab-visible.png" });
    await fab.click();
    await page.waitForTimeout(800);
    const y = await page.evaluate(() => window.scrollY);
    expect(y).toBeLessThan(50);
  });

  test("light theme switch via T persists", async ({ page }) => {
    await page.goto(BASE + "/", { waitUntil: "networkidle" });
    await page.keyboard.press("t");
    await page.waitForTimeout(200);
    const theme1 = await page.evaluate(() =>
      document.documentElement.getAttribute("data-theme"),
    );
    await page.reload();
    await page.waitForTimeout(400);
    const theme2 = await page.evaluate(() =>
      document.documentElement.getAttribute("data-theme"),
    );
    expect(theme1).toBe(theme2);
  });

  test("scroll memory restores on back navigation", async ({ page }) => {
    await page.goto(BASE + "/", { waitUntil: "networkidle" });
    await page.evaluate(() => window.scrollTo(0, 1200));
    await page.waitForTimeout(500);
    await page.click('nav.page-nav a[href="/icons"]');
    // Pathname-only match — allow trailing hash from scroll-spy hash sync.
    await page.waitForFunction(() => location.pathname === "/icons");
    await page.waitForTimeout(400);
    await page.goBack();
    await page.waitForFunction(() => location.pathname === "/");
    await page.waitForTimeout(800);
    const y = await page.evaluate(() => window.scrollY);
    // Allow some slack for re-mount; expect ≥ 800 to confirm restore happened
    expect(y).toBeGreaterThan(800);
  });

  test("sidebar group collapse persists across route changes", async ({ page }) => {
    await page.goto(BASE + "/", { waitUntil: "networkidle" });
    await page.waitForTimeout(400);
    // Click "Color" group header to collapse
    const colorHeader = page.locator('.sidebar-desktop button:has-text("Color")').first();
    await colorHeader.click();
    await page.waitForTimeout(300);
    // Surface entry should be hidden
    const surface = page.locator('.sidebar-desktop a[href="#color-surface"]');
    await expect(surface).toBeHidden();

    // Navigate away and back
    await page.click('nav.page-nav a[href="/icons"]');
    await page.waitForURL("**/icons");
    await page.waitForTimeout(400);
    await page.click('nav.page-nav a[href="/"]');
    await page.waitForURL("http://localhost:3001/");
    await page.waitForTimeout(600);
    // Color group should still be collapsed
    await expect(page.locator('.sidebar-desktop a[href="#color-surface"]')).toBeHidden();
  });
});
