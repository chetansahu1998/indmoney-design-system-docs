/**
 * Playwright config for INDmoney DS docs.
 *
 * Two test categories live under tests/:
 *   - token-parity/  — assert rendered DOM colors match source JSON (the
 *                      load-bearing fidelity test)
 *   - visual/        — toHaveScreenshot regressions per brand × mode
 *                      (deferred to v1.1 — bootstrap baselines first)
 *
 * For v1 we run a single project (indmoney-light) since Tickertape isn't ready
 * and dark-mode toggle is page-level, not per-test.
 */
import { defineConfig, devices } from "@playwright/test";

const BASE_URL = process.env.PLAYWRIGHT_BASE_URL ?? "http://localhost:3001";

export default defineConfig({
  testDir: "./tests",
  testMatch: /.*\.spec\.ts$/,
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 0,
  workers: process.env.CI ? 2 : undefined,
  reporter: process.env.CI ? [["github"], ["html", { open: "never" }]] : "list",

  expect: {
    toHaveScreenshot: {
      maxDiffPixelRatio: 0.01,
      threshold: 0.2,
      animations: "disabled",
    },
  },

  use: {
    baseURL: BASE_URL,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    video: "off",
  },

  projects: [
    {
      name: "indmoney-light",
      use: { ...devices["Desktop Chrome"], colorScheme: "light" },
    },
  ],

  webServer: process.env.CI
    ? {
        command: "npm run start",
        url: BASE_URL,
        reuseExistingServer: false,
        timeout: 60_000,
      }
    : undefined,
});
