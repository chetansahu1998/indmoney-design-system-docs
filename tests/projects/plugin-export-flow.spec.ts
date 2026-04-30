/**
 * Cross-layer plugin → backend → SSE → UI test (Phase 1 plan deliverable).
 *
 * The actual Figma plugin can't be driven from Playwright (it lives inside a
 * Figma iframe sandbox). We therefore exercise the same end-to-end contract
 * by:
 *   1. POSTing a multi-flow / multi-frame fixture payload directly to the
 *      ds-service `POST /v1/projects/export` endpoint.
 *   2. Subscribing to SSE via the per-trace `/v1/projects/:slug/events`
 *      stream, gated by a single-use ticket from
 *      `POST /v1/projects/:slug/events/ticket`.
 *   3. Asserting `project.view_ready` arrives before the audit completes.
 *   4. Asserting `project.audit_complete` arrives after the audit worker
 *      drains the queued audit_jobs row.
 *   5. Navigating to `/projects/<slug>?v=<id>` and confirming the atlas
 *      paints one frame per fixture screen.
 *
 * Why this test is `test.skip`'d by default
 * ─────────────────────────────────────────
 * The full round-trip requires:
 *   - A running ds-service binary on `DS_SERVICE_URL` (default
 *     http://localhost:8080) with U1 migrations applied + an authed user
 *     whose JWT we can capture.
 *   - A reachable Figma file_id (or a fake renderer wired in via
 *     `pipeline.SetFigmaImagesBase` to point at a local httptest server).
 *   - Write-access to `services/ds-service/data/screens/` for PNG persistence.
 *
 * None of those are available in CI today (the existing `build.yml` workflow
 * only `go build`s + runs token-parity Playwright). Until the Phase 2
 * `perf-budgets.yml` workflow stands up the full stack, this spec stays
 * skipped — we DO ship the assertions in the body so the day someone wires
 * the env it runs unchanged.
 *
 * To run locally:
 *   1. Boot ds-service with the fake Figma renderer:
 *        cd services/ds-service && go run ./cmd/audit-server
 *   2. Set env vars and run Playwright:
 *        DS_SERVICE_URL=http://localhost:8080 \
 *        DS_AUTH_TOKEN=<jwt> \
 *        PLAYWRIGHT_BASE_URL=http://localhost:3001 \
 *        npx playwright test tests/projects/plugin-export-flow.spec.ts
 *   3. The spec auto-detects the env vars (skip → run).
 */

import { test, expect, type Page } from "@playwright/test";

const DS_URL = process.env.DS_SERVICE_URL ?? "http://localhost:8080";
const DS_AUTH_TOKEN = process.env.DS_AUTH_TOKEN ?? "";

// Two boolean envs gate the test:
//   - DS_AUTH_TOKEN must be set (we need a real bearer to reach the server).
//   - DS_E2E=1 must be explicit (so a developer can never run this by accident
//     against a shared dev DB).
const E2E_ENABLED = process.env.DS_E2E === "1" && DS_AUTH_TOKEN !== "";

test.describe.configure({ mode: "serial" });

test.describe("Phase 1 plugin → backend → SSE → UI (cross-layer)", () => {
  test.skip(
    !E2E_ENABLED,
    // TODO: needs DS_E2E=1 + a running ds-service with a valid JWT in
    // DS_AUTH_TOKEN. See the file header for setup instructions.
    "skipped — set DS_E2E=1 and DS_AUTH_TOKEN to run this end-to-end spec",
  );

  test("export → view_ready → audit_complete → atlas paints", async ({
    page,
  }: {
    page: Page;
  }) => {
    // Step 1: POST /v1/projects/export with a small multi-flow payload.
    const traceID = crypto.randomUUID();
    const idempotencyKey = crypto.randomUUID();
    const payload = {
      project_name: `pw-e2e ${new Date().toISOString()}`,
      product: "Plutus",
      path: "Onboarding",
      platform: "web",
      file_id: "FILE-PW-E2E",
      trace_id: traceID,
      idempotency_key: idempotencyKey,
      flows: [
        {
          name: "FlowA",
          frames: [
            {
              figma_frame_id: "fig-A1",
              x: 0,
              y: 0,
              width: 375,
              height: 812,
              variable_collection_id: "VC",
              mode_id: "light",
              mode_label: "light",
              explicit_variable_modes: {},
            },
            {
              figma_frame_id: "fig-A2",
              x: 0,
              y: 1000,
              width: 375,
              height: 812,
              variable_collection_id: "VC",
              mode_id: "dark",
              mode_label: "dark",
              explicit_variable_modes: {},
            },
          ],
        },
        {
          name: "FlowB",
          frames: [
            {
              figma_frame_id: "fig-B1",
              x: 500,
              y: 0,
              width: 375,
              height: 812,
              variable_collection_id: "VC",
              mode_id: "light",
              mode_label: "light",
              explicit_variable_modes: {},
            },
          ],
        },
      ],
    };

    const exportRes = await page.request.post(`${DS_URL}/v1/projects/export`, {
      headers: {
        Authorization: `Bearer ${DS_AUTH_TOKEN}`,
        "Content-Type": "application/json",
      },
      data: payload,
    });
    expect(exportRes.status()).toBeGreaterThanOrEqual(200);
    expect(exportRes.status()).toBeLessThan(300);
    const exportJSON = (await exportRes.json()) as {
      project_slug: string;
      version_id: string;
      trace_id: string;
    };
    expect(exportJSON.project_slug).toBeTruthy();
    expect(exportJSON.version_id).toBeTruthy();

    const slug = exportJSON.project_slug;
    const versionID = exportJSON.version_id;

    // Step 2: mint an SSE ticket scoped to our trace.
    const ticketRes = await page.request.post(
      `${DS_URL}/v1/projects/${encodeURIComponent(slug)}/events/ticket`,
      {
        headers: {
          Authorization: `Bearer ${DS_AUTH_TOKEN}`,
          "Content-Type": "application/json",
        },
        data: { trace_id: traceID },
      },
    );
    expect(ticketRes.ok()).toBe(true);
    const { ticket } = (await ticketRes.json()) as { ticket: string };
    expect(ticket).toBeTruthy();

    // Step 3 + 4: open the SSE stream from inside the page (EventSource only
    // exists in browser context) and collect events into window.__events.
    await page.goto("about:blank");
    const eventTypes = await page.evaluate(
      async ({ url, timeoutMs }) => {
        return await new Promise<string[]>((resolve) => {
          const types: string[] = [];
          const es = new EventSource(url);
          const done = () => {
            es.close();
            resolve(types);
          };
          for (const t of [
            "view_ready",
            "audit_complete",
            "audit_failed",
            "export_failed",
          ]) {
            es.addEventListener(t, () => {
              types.push(t);
              if (types.includes("view_ready") && types.includes("audit_complete")) {
                done();
              }
            });
          }
          setTimeout(done, timeoutMs);
        });
      },
      {
        url: `${DS_URL}/v1/projects/${encodeURIComponent(slug)}/events?ticket=${encodeURIComponent(ticket)}`,
        timeoutMs: 20_000,
      },
    );

    expect(eventTypes).toContain("view_ready");
    // audit_complete must follow view_ready — Phase 1's audit core runs the
    // existing color / spacing / radius checks against the synthetic frames.
    expect(eventTypes).toContain("audit_complete");
    const viewReadyIdx = eventTypes.indexOf("view_ready");
    const auditCompleteIdx = eventTypes.indexOf("audit_complete");
    expect(viewReadyIdx).toBeLessThan(auditCompleteIdx);

    // Step 5: render the project page and assert the atlas paints one frame
    // per fixture screen (3 total: A1 + A2 + B1).
    await page.addInitScript((token) => {
      const blob = {
        state: { token, email: "designer@example.com", role: "designer" },
        version: 0,
      };
      window.localStorage.setItem("indmoney-ds-auth", JSON.stringify(blob));
    }, DS_AUTH_TOKEN);

    await page.goto(
      `/projects/${encodeURIComponent(slug)}?v=${encodeURIComponent(versionID)}`,
    );
    await expect(page.locator('[data-anim="tab-strip"]')).toBeVisible({
      timeout: 10_000,
    });

    const canvas = page.locator('[data-anim="atlas-canvas"] canvas').first();
    await expect(canvas).toBeVisible({ timeout: 15_000 });

    // The atlas paints a single <canvas>; per-frame rendering happens inside
    // r3f. The shipped contract asserts on `[data-anim="atlas-frame"]`
    // overlays which the AtlasCanvas component emits one-per-screen for
    // animation targeting. Three frames in this fixture.
    const frames = page.locator('[data-anim="atlas-frame"]');
    await expect(frames).toHaveCount(3, { timeout: 10_000 });
  });
});
