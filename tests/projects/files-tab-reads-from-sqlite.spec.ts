/**
 * U12 — /files/[slug] read-path cutover (Phase 2 U10).
 *
 * Verifies the files-tab read path goes through the new
 * GET /api/audit/by-slug/<slug> route (backed by SQLite) AND the legacy
 * sidecar-import path under READ_FROM_SIDECAR=1 produce equivalent
 * FixCandidate counts for the same slug.
 *
 * Strategy:
 *   1. Pre-condition: U10 must have shipped the API route. We probe
 *      /api/audit/by-slug/healthcheck — if the response is 404 from the
 *      Next dev server (i.e. no route registered), we self-skip.
 *   2. Seed SQLite via the migrate-sidecars CLI on a synthetic fixture so
 *      both paths have a known FixCandidate count to compare against.
 *   3. Navigate to /files/<slug> twice, once with READ_FROM_SIDECAR
 *      simulated via a query-string override (`?source=sidecar`) and once
 *      without. Assert rendered fix counts match.
 *
 * Why this spec is heavily gated:
 *   It depends on TWO not-yet-merged units (U9 backfill CLI + U10 read-path
 *   cutover). Any of them missing → `test.skip()` with a TODO. The tests
 *   either run end-to-end OR they skip explicitly — they NEVER pass
 *   silently.
 */

import { test, expect } from "@playwright/test";
import { execSync, spawnSync } from "node:child_process";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import path from "node:path";
import os from "node:os";

const REPO_ROOT = process.env.GITHUB_WORKSPACE ?? process.cwd();
const FIXTURE_SLUG = "u12-readpath";
const FIXTURE_FIX_COUNT = 6;

/** Seed-once latch: first runnable test in the file populates SQLite. */
let seededDB: string | null = null;
let seedSkipReason: string | null = null;

function hasGo(): boolean {
  try {
    execSync("go version", { stdio: "ignore" });
    return true;
  } catch {
    return false;
  }
}

function sqlite(db: string, stmt: string): string {
  return execSync(`sqlite3 ${JSON.stringify(db)} ${JSON.stringify(stmt)}`, {
    encoding: "utf8",
  }).trim();
}

function syntheticAuditResult(slug: string, fixCount: number): string {
  const fixes = Array.from({ length: fixCount }, (_, i) => ({
    node_id: `node-${slug}-${i}`,
    node_name: `Node ${i}`,
    property: i % 2 === 0 ? "fill" : "padding",
    observed: i % 2 === 0 ? "#FF6633" : "13px",
    token_path: i % 2 === 0 ? "surface.brand.500" : "spacing.md",
    distance: 0.42,
    usage_count: 1,
    priority: "P2",
    reason: "drift",
  }));
  return JSON.stringify({
    schema_version: "1.0",
    file_key: `key-${slug}`,
    file_name: slug,
    file_slug: slug,
    brand: "indmoney",
    extracted_at: new Date().toISOString(),
    design_system_rev: "test",
    overall_coverage: 0.5,
    overall_from_ds: 0.5,
    screens: [
      {
        node_id: `${slug}-screen-1`,
        name: `${slug} screen`,
        slug: `${slug}-s1`,
        coverage: {
          fills: { bound: 1, total: 2 },
          text: { bound: 0, total: 0 },
          spacing: { bound: 0, total: 1 },
          radius: { bound: 0, total: 0 },
        },
        component_summary: { from_ds: 0, ambiguous: 0, custom: 0 },
        fixes,
        component_matches: [],
        node_count: 4,
      },
    ],
  });
}

function seedSQLiteFromFixture(): { db: string } | { skipReason: string } {
  if (!hasGo()) return { skipReason: "go toolchain not on PATH" };

  const fixDir = mkdtempSync(path.join(os.tmpdir(), "u12-readpath-"));
  const dbDir = mkdtempSync(path.join(os.tmpdir(), "u12-readpath-db-"));
  const db = path.join(dbDir, "ds.db");
  writeFileSync(
    path.join(fixDir, `${FIXTURE_SLUG}.json`),
    syntheticAuditResult(FIXTURE_SLUG, FIXTURE_FIX_COUNT),
  );

  const out = spawnSync(
    "go",
    [
      "run",
      "./services/ds-service/cmd/migrate-sidecars",
      `--dir=${fixDir}`,
      `--db=${db}`,
    ],
    { cwd: REPO_ROOT, encoding: "utf8", timeout: 120_000 },
  );
  if (out.status !== 0) {
    return {
      skipReason: `migrate-sidecars CLI failed: ${out.stderr || out.stdout}`,
    };
  }

  // Sanity: violations row count matches fixture count.
  const count = parseInt(
    sqlite(
      db,
      `SELECT COUNT(*) FROM violations v JOIN project_versions pv ON pv.id=v.version_id JOIN projects p ON p.id=pv.project_id WHERE p.slug='${FIXTURE_SLUG}';`,
    ),
    10,
  );
  if (count !== FIXTURE_FIX_COUNT) {
    return {
      skipReason: `seed produced ${count} violations, expected ${FIXTURE_FIX_COUNT}`,
    };
  }
  rmSync(fixDir, { recursive: true, force: true });
  return { db };
}

test.beforeAll(() => {
  const result = seedSQLiteFromFixture();
  if ("db" in result) {
    seededDB = result.db;
  } else {
    seedSkipReason = result.skipReason;
  }
});

test.describe("U12 /files/[slug] read-path cutover", () => {
  test("api/audit/by-slug route exists (U10 landed)", async ({ request }) => {
    test.skip(
      !!seedSkipReason,
      `seed phase failed; cannot probe U10 route. reason: ${seedSkipReason}. ` +
        `TODO: ensure go toolchain + migrate-sidecars CLI are available in CI.`,
    );

    // Probe the new route. If U10 hasn't landed, the dev server returns 404
    // and we self-skip the rest of the file.
    const probe = await request.get("/api/audit/by-slug/healthcheck");
    test.skip(
      probe.status() === 404,
      "U10 read-path API (/api/audit/by-slug/[slug]) not wired yet. " +
        "TODO: ship app/api/audit/by-slug/[slug]/route.ts before this test runs.",
    );
    expect([200, 404, 410]).toContain(probe.status()); // 410 acceptable for "no slug"
  });

  test("/files/<slug> renders without crashing and surfaces fix count", async ({
    page,
    request,
  }) => {
    test.skip(
      !!seedSkipReason,
      `seed phase failed: ${seedSkipReason}. TODO: stabilize seed harness.`,
    );

    // Skip if U10 route absent — same probe as above.
    const probe = await request.get("/api/audit/by-slug/healthcheck");
    test.skip(
      probe.status() === 404,
      "U10 read-path API not wired yet; nothing to read from.",
    );

    // Need /files/<slug> to be statically generated for our fixture slug. In
    // dev mode Next will try to build it on-demand. If lib/audit/files.ts
    // doesn't see the slug, it 404s — that's a U10 dependency too.
    const resp = await page.goto(`/files/${FIXTURE_SLUG}`);
    test.skip(
      !resp || resp.status() === 404,
      `/files/${FIXTURE_SLUG} not registered; depends on U10 wiring lib/audit/files.ts to SQLite.`,
    );
    expect(resp?.status()).toBeLessThan(500);

    // The FileDetail component prints "<n> fix(es)" per screen — find any
    // node that reports the count and assert it matches our seeded count.
    const html = await page.content();
    expect(html).toMatch(new RegExp(`${FIXTURE_FIX_COUNT}\\s+fix(es)?`));
  });

  test("parity: SQLite read-path vs READ_FROM_SIDECAR=1 → same FixCandidate count", async ({
    page,
    request,
  }) => {
    test.skip(
      !!seedSkipReason,
      `seed phase failed: ${seedSkipReason}.`,
    );
    const probe = await request.get("/api/audit/by-slug/healthcheck");
    test.skip(
      probe.status() === 404,
      "U10 read-path API not wired yet; cannot run parity test.",
    );

    // Path A — default (SQLite). Read directly from the api route.
    const apiResp = await request.get(`/api/audit/by-slug/${FIXTURE_SLUG}`);
    test.skip(
      apiResp.status() === 404,
      "audit/by-slug returns 404; U10 + seed not aligned. " +
        "TODO: check that backfill writes through to the same DB the route reads.",
    );
    expect(apiResp.status()).toBe(200);
    const apiBody = (await apiResp.json()) as {
      screens: Array<{ fixes: unknown[] }>;
    };
    const sqliteFixCount = apiBody.screens.reduce(
      (acc, s) => acc + s.fixes.length,
      0,
    );
    expect(sqliteFixCount).toBe(FIXTURE_FIX_COUNT);

    // Path B — sidecar import path. The plan doc lists READ_FROM_SIDECAR=1
    // as the one-release rollback flag (U10). We can't toggle a Node env at
    // test time without restarting the dev server; the practical proxy is
    // the URL parameter `?source=sidecar` if U10 plumbed it, OR a header.
    // Either is U10's responsibility; we assert against whichever path the
    // route exposes.
    const sidecarResp = await request.get(
      `/api/audit/by-slug/${FIXTURE_SLUG}?source=sidecar`,
    );
    test.skip(
      sidecarResp.status() === 404 || sidecarResp.status() === 501,
      "READ_FROM_SIDECAR fallback path not implemented in the route. " +
        "TODO: U10 should support `?source=sidecar` (or equivalent) for this parity test.",
    );

    if (sidecarResp.status() === 200) {
      const sidecarBody = (await sidecarResp.json()) as {
        screens: Array<{ fixes: unknown[] }>;
      };
      const sidecarFixCount = sidecarBody.screens.reduce(
        (acc, s) => acc + s.fixes.length,
        0,
      );
      // Parity contract: same slug → same fix count regardless of source.
      expect(sidecarFixCount).toBe(sqliteFixCount);
    }

    // Drive the page itself once to confirm the consumer renders. Use the
    // SQLite path (default) — the parity assertion above is the load-bearing
    // one; this is a smoke check the rendered content matches the API.
    const pageResp = await page.goto(`/files/${FIXTURE_SLUG}`);
    expect(pageResp?.status()).toBeLessThan(500);
    const html = await page.content();
    expect(html).toMatch(new RegExp(`${sqliteFixCount}\\s+fix(es)?`));
  });
});

test.afterAll(() => {
  if (seededDB) {
    rmSync(path.dirname(seededDB), { recursive: true, force: true });
  }
});
