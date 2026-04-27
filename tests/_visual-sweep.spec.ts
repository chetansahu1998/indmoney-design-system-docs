import { test } from "@playwright/test";
import * as fs from "fs";

/**
 * Visual sweep — visits every route, takes a desktop + mobile screenshot,
 * captures console errors. Output goes to test-results/sweep/. Designed to
 * be read by humans, not assertions.
 */

const BASE = "http://localhost:3001";
const ROUTES = [
  { slug: "foundations", path: "/" },
  { slug: "icons", path: "/icons" },
  { slug: "components", path: "/components" },
  { slug: "illustrations", path: "/illustrations" },
  { slug: "logos", path: "/logos" },
  { slug: "files", path: "/files" },
];

const OUT_DIR = "test-results/sweep";

test.beforeAll(() => {
  if (!fs.existsSync(OUT_DIR)) fs.mkdirSync(OUT_DIR, { recursive: true });
});

const consoleLog: Record<string, string[]> = {};

for (const route of ROUTES) {
  test(`sweep ${route.slug}`, async ({ page }) => {
    const errors: string[] = [];
    const warnings: string[] = [];
    page.on("pageerror", (e) => errors.push(`pageerror: ${e.message}`));
    page.on("console", (m) => {
      if (m.type() === "error") errors.push(`console.error: ${m.text()}`);
      if (m.type() === "warning") warnings.push(`console.warn: ${m.text()}`);
    });

    // Desktop, dark
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.goto(BASE + route.path, { waitUntil: "networkidle" });
    await page.waitForTimeout(800);
    await page.screenshot({
      path: `${OUT_DIR}/${route.slug}-desktop-dark.png`,
      fullPage: false,
    });
    await page.screenshot({
      path: `${OUT_DIR}/${route.slug}-desktop-dark-fullpage.png`,
      fullPage: true,
    });

    // Toggle to light
    await page.keyboard.press("t");
    await page.waitForTimeout(300);
    await page.screenshot({
      path: `${OUT_DIR}/${route.slug}-desktop-light.png`,
      fullPage: false,
    });

    // Reset to dark for mobile shot
    await page.keyboard.press("t");
    await page.waitForTimeout(200);

    // Mobile
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto(BASE + route.path, { waitUntil: "networkidle" });
    await page.waitForTimeout(600);
    await page.screenshot({
      path: `${OUT_DIR}/${route.slug}-mobile-dark.png`,
      fullPage: false,
    });

    consoleLog[route.slug] = [
      `--- ${route.path} ---`,
      `errors: ${errors.length}`,
      ...errors,
      `warnings: ${warnings.length}`,
      ...warnings.slice(0, 5),
    ];
  });
}

test.afterAll(() => {
  const summary = Object.values(consoleLog).flat().join("\n");
  fs.writeFileSync(`${OUT_DIR}/console.txt`, summary);
  console.log(summary);
});
