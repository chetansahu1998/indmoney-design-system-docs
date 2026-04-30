/**
 * Tenant isolation — cross-tenant routes return 404 with no existence
 * oracle (Phase 1 plan deliverable, Network/Auth section).
 *
 * The contract:
 *   Tenant A creates a project. Tenant B (separate JWT) tries every
 *   tenant-A-scoped endpoint. Each MUST respond 404 with the same body
 *   shape as a request for a slug that doesn't exist at all — no
 *   timing-distinguishable "you can't see this one" vs "this doesn't
 *   exist" leak. The Phase 1 plan calls this out as a load-bearing
 *   security property.
 *
 * Endpoints under test (all tenant-scoped):
 *   - GET  /v1/projects/:slug
 *   - GET  /v1/projects/:slug/screens/:id/png
 *   - GET  /v1/projects/:slug/screens/:id/canonical-tree
 *   - GET  /v1/projects/:slug/flows/:flow_id/drd
 *   - GET  /v1/projects/:slug/violations
 *   - POST /v1/projects/:slug/events/ticket
 *
 * Why this test is `test.skip`'d by default
 * ─────────────────────────────────────────
 * The full assertion needs:
 *   - A running ds-service (DS_SERVICE_URL).
 *   - Two valid JWTs scoped to two different tenants
 *     (DS_AUTH_TOKEN_A, DS_AUTH_TOKEN_B), each with a project owned
 *     by their tenant.
 *   - A pre-seeded slug owned by tenant A and a non-existent slug.
 *
 * Mocking either side via `page.route` would defeat the entire point of
 * this test — we'd be asserting on our own mock's behaviour, not on the
 * server's tenant scoping. Until the perf-budgets workflow stands up the
 * full stack (or a dedicated e2e workflow does), this spec stays skipped
 * with the assertions intact for the day someone wires DS_E2E=1.
 *
 * To run locally:
 *   1. Boot ds-service and seed two tenants with one project each.
 *   2. Capture both JWTs (e.g. via `cmd/audit-server`'s login route).
 *   3. Set the env vars:
 *        DS_E2E=1 \
 *        DS_SERVICE_URL=http://localhost:8080 \
 *        DS_AUTH_TOKEN_A=<jwt> DS_TENANT_A_SLUG=<slug-A> \
 *        DS_AUTH_TOKEN_B=<jwt> \
 *        DS_TENANT_A_SCREEN_ID=<screen-id-of-tenant-A> \
 *        DS_TENANT_A_FLOW_ID=<flow-id-of-tenant-A> \
 *        npx playwright test tests/projects/tenant-isolation.spec.ts
 */

import { test, expect } from "@playwright/test";

const DS_URL = process.env.DS_SERVICE_URL ?? "http://localhost:8080";
const DS_AUTH_TOKEN_A = process.env.DS_AUTH_TOKEN_A ?? "";
const DS_AUTH_TOKEN_B = process.env.DS_AUTH_TOKEN_B ?? "";
const TENANT_A_SLUG = process.env.DS_TENANT_A_SLUG ?? "";
const TENANT_A_SCREEN_ID = process.env.DS_TENANT_A_SCREEN_ID ?? "";
const TENANT_A_FLOW_ID = process.env.DS_TENANT_A_FLOW_ID ?? "";

const E2E_ENABLED =
  process.env.DS_E2E === "1" &&
  DS_AUTH_TOKEN_A !== "" &&
  DS_AUTH_TOKEN_B !== "" &&
  TENANT_A_SLUG !== "" &&
  TENANT_A_SCREEN_ID !== "" &&
  TENANT_A_FLOW_ID !== "";

const NONEXISTENT_SLUG = "definitely-not-a-real-slug-z9z9z9";
const NONEXISTENT_SCREEN_ID = "00000000-0000-0000-0000-000000000000";
const NONEXISTENT_FLOW_ID = "00000000-0000-0000-0000-000000000000";

test.describe.configure({ mode: "serial" });

test.describe("Phase 1 tenant isolation — cross-tenant 404 with no existence oracle", () => {
  test.skip(
    !E2E_ENABLED,
    // TODO: needs DS_E2E=1 + two tenant-scoped JWTs (DS_AUTH_TOKEN_A,
    // DS_AUTH_TOKEN_B) + a tenant-A slug (DS_TENANT_A_SLUG) +
    // DS_TENANT_A_SCREEN_ID + DS_TENANT_A_FLOW_ID. See file header.
    "skipped — set DS_E2E=1 + two tenant tokens to run cross-tenant assertions",
  );

  /**
   * Sanity: tenant A can read its own project. Without this the cross-tenant
   * assertion below could pass for the wrong reason (always returns 404).
   */
  test("tenant A can read its own project (sanity)", async ({ request }) => {
    const res = await request.get(
      `${DS_URL}/v1/projects/${encodeURIComponent(TENANT_A_SLUG)}`,
      { headers: { Authorization: `Bearer ${DS_AUTH_TOKEN_A}` } },
    );
    expect(res.status()).toBe(200);
  });

  test("GET /v1/projects/:slug — tenant B sees 404 for tenant A's slug, equal body to a non-existent slug", async ({
    request,
  }) => {
    const crossTenant = await request.get(
      `${DS_URL}/v1/projects/${encodeURIComponent(TENANT_A_SLUG)}`,
      { headers: { Authorization: `Bearer ${DS_AUTH_TOKEN_B}` } },
    );
    expect(crossTenant.status()).toBe(404);

    const nonexistent = await request.get(
      `${DS_URL}/v1/projects/${encodeURIComponent(NONEXISTENT_SLUG)}`,
      { headers: { Authorization: `Bearer ${DS_AUTH_TOKEN_B}` } },
    );
    expect(nonexistent.status()).toBe(404);

    const a = await crossTenant.json();
    const b = await nonexistent.json();
    expect(a).toEqual(b);
  });

  test("GET /screens/:id/png — same body for cross-tenant + non-existent", async ({
    request,
  }) => {
    const crossTenant = await request.get(
      `${DS_URL}/v1/projects/${encodeURIComponent(
        TENANT_A_SLUG,
      )}/screens/${encodeURIComponent(TENANT_A_SCREEN_ID)}/png`,
      { headers: { Authorization: `Bearer ${DS_AUTH_TOKEN_B}` } },
    );
    expect(crossTenant.status()).toBe(404);

    const nonexistent = await request.get(
      `${DS_URL}/v1/projects/${encodeURIComponent(
        NONEXISTENT_SLUG,
      )}/screens/${encodeURIComponent(NONEXISTENT_SCREEN_ID)}/png`,
      { headers: { Authorization: `Bearer ${DS_AUTH_TOKEN_B}` } },
    );
    expect(nonexistent.status()).toBe(404);

    // PNG endpoint returns plain bodies on 404; assert lengths match (no
    // existence-oracle leak via response size). If the server returns JSON,
    // compare structurally; otherwise compare byte length.
    const aBytes = (await crossTenant.body()).length;
    const bBytes = (await nonexistent.body()).length;
    expect(aBytes).toBe(bBytes);
  });

  test("GET /screens/:id/canonical-tree — same body for cross-tenant + non-existent", async ({
    request,
  }) => {
    const crossTenant = await request.get(
      `${DS_URL}/v1/projects/${encodeURIComponent(
        TENANT_A_SLUG,
      )}/screens/${encodeURIComponent(TENANT_A_SCREEN_ID)}/canonical-tree`,
      { headers: { Authorization: `Bearer ${DS_AUTH_TOKEN_B}` } },
    );
    expect(crossTenant.status()).toBe(404);

    const nonexistent = await request.get(
      `${DS_URL}/v1/projects/${encodeURIComponent(
        NONEXISTENT_SLUG,
      )}/screens/${encodeURIComponent(NONEXISTENT_SCREEN_ID)}/canonical-tree`,
      { headers: { Authorization: `Bearer ${DS_AUTH_TOKEN_B}` } },
    );
    expect(nonexistent.status()).toBe(404);
    expect(await crossTenant.json()).toEqual(await nonexistent.json());
  });

  test("GET /flows/:flow_id/drd — same body for cross-tenant + non-existent", async ({
    request,
  }) => {
    const crossTenant = await request.get(
      `${DS_URL}/v1/projects/${encodeURIComponent(
        TENANT_A_SLUG,
      )}/flows/${encodeURIComponent(TENANT_A_FLOW_ID)}/drd`,
      { headers: { Authorization: `Bearer ${DS_AUTH_TOKEN_B}` } },
    );
    expect(crossTenant.status()).toBe(404);

    const nonexistent = await request.get(
      `${DS_URL}/v1/projects/${encodeURIComponent(
        NONEXISTENT_SLUG,
      )}/flows/${encodeURIComponent(NONEXISTENT_FLOW_ID)}/drd`,
      { headers: { Authorization: `Bearer ${DS_AUTH_TOKEN_B}` } },
    );
    expect(nonexistent.status()).toBe(404);
    expect(await crossTenant.json()).toEqual(await nonexistent.json());
  });

  test("GET /violations — same body for cross-tenant + non-existent", async ({
    request,
  }) => {
    const crossTenant = await request.get(
      `${DS_URL}/v1/projects/${encodeURIComponent(TENANT_A_SLUG)}/violations`,
      { headers: { Authorization: `Bearer ${DS_AUTH_TOKEN_B}` } },
    );
    expect(crossTenant.status()).toBe(404);

    const nonexistent = await request.get(
      `${DS_URL}/v1/projects/${encodeURIComponent(NONEXISTENT_SLUG)}/violations`,
      { headers: { Authorization: `Bearer ${DS_AUTH_TOKEN_B}` } },
    );
    expect(nonexistent.status()).toBe(404);
    expect(await crossTenant.json()).toEqual(await nonexistent.json());
  });

  test("POST /events/ticket — same body for cross-tenant + non-existent", async ({
    request,
  }) => {
    const crossTenant = await request.post(
      `${DS_URL}/v1/projects/${encodeURIComponent(TENANT_A_SLUG)}/events/ticket`,
      {
        headers: {
          Authorization: `Bearer ${DS_AUTH_TOKEN_B}`,
          "Content-Type": "application/json",
        },
        data: { trace_id: "trace-x" },
      },
    );
    expect(crossTenant.status()).toBe(404);

    const nonexistent = await request.post(
      `${DS_URL}/v1/projects/${encodeURIComponent(NONEXISTENT_SLUG)}/events/ticket`,
      {
        headers: {
          Authorization: `Bearer ${DS_AUTH_TOKEN_B}`,
          "Content-Type": "application/json",
        },
        data: { trace_id: "trace-x" },
      },
    );
    expect(nonexistent.status()).toBe(404);
    expect(await crossTenant.json()).toEqual(await nonexistent.json());
  });
});
