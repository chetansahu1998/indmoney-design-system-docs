/**
 * Phase 4 U14 (deferred) — auto-fix round-trip closure for AE-2.
 *
 * The full deeplink → plugin → Figma write → /fix-applied → SSE → UI
 * round-trip can't be exercised in Playwright (Figma plugin runtime
 * isn't a browser tab). This spec covers the *docs-site half*: the
 * Fix-in-Figma button on the ViolationsTab opens the right deeplink,
 * and the SSE subscriber on the inbox channel reconciles the row when
 * the success ping arrives server-side.
 *
 * Backend half is unit-tested in Go (violation_get_test.go +
 * lifecycle_test.go cover the GET / POST + state transitions).
 */

import { test, expect } from "@playwright/test";

test.skip("Fix-in-Figma button opens the right deeplink", async ({ page }) => {
  // Skipped pending the project-shell fixture builder. Re-enabled
  // alongside violation-lifecycle.spec.ts in Phase 5.
  let popup: string | null = null;
  await page.exposeFunction("__capturePopup", (url: string) => {
    popup = url;
  });
  await page.goto("/projects/tax-fno-learn");
  await page.evaluate(() => {
    const orig = window.open;
    window.open = (url) => {
      (window as unknown as { __capturePopup: (u: string) => void }).__capturePopup(String(url));
      return orig?.call(window, url) || null;
    };
  });
  await page.getByTestId("fix-in-figma").first().click();
  expect(popup).toMatch(/^figma:\/\/plugin\/.*\/audit\?violation_id=/);
});
