/**
 * U12 — sidecar-to-SQLite backfill CLI parity.
 *
 * Exercises Phase 2 U9: `services/ds-service/cmd/migrate-sidecars`.
 *
 * Two test arcs:
 *   1. "zero-work" run against the real lib/audit/ — confirms the CLI sees
 *      only index.json + spacing-observed.json (both skipped) and reports
 *      "found 0 sidecar(s)".  This is the ground truth observation that
 *      backfill.go's header comment documents.
 *   2. Synthetic-fixture run against a tmp dir — writes 3 fake sidecar JSON
 *      files matching the audit.AuditResult shape, runs the CLI with
 *      --dir, and asserts:
 *        - 3 synthetic projects committed to SQLite,
 *        - violations rows = sum of FixCandidates across fixtures,
 *        - re-running yields zero new rows (idempotent),
 *        - bumping fixture mtime → re-run creates a new project_versions
 *          row (Version index increments).
 *
 * No HTTP, no auth, no running ds-service. Pure CLI + sqlite3 on disk.
 */

import { test, expect } from "@playwright/test";
import { execSync, spawnSync } from "node:child_process";
import { mkdtempSync, rmSync, writeFileSync, statSync, utimesSync } from "node:fs";
import path from "node:path";
import os from "node:os";

const REPO_ROOT = process.env.GITHUB_WORKSPACE ?? process.cwd();
const REAL_AUDIT_DIR = path.join(REPO_ROOT, "lib/audit");

// We use a side DB so a parallel /files-tab spec running against the dev DB
// doesn't fight us. Each test uses its OWN tmp DB initialized by the CLI.
function tmpDB(): string {
  return path.join(
    mkdtempSync(path.join(os.tmpdir(), "u12-migrate-")),
    "ds.db",
  );
}

/**
 * Run the migrate-sidecars CLI. Returns combined stdout+stderr.
 * `go` must be on PATH; the caller skips when it isn't.
 */
function runMigrate(args: { dir: string; db: string; dryRun?: boolean; slug?: string }): {
  stdout: string;
  stderr: string;
  status: number | null;
} {
  const argv = [
    "run",
    "./services/ds-service/cmd/migrate-sidecars",
    `--dir=${args.dir}`,
    `--db=${args.db}`,
  ];
  if (args.dryRun) argv.push("--dry-run");
  if (args.slug) argv.push(`--slug=${args.slug}`);
  const out = spawnSync("go", argv, {
    cwd: REPO_ROOT,
    encoding: "utf8",
    timeout: 120_000,
    env: {
      ...process.env,
      DS_SYSTEM_TENANT_ID: "system",
      DS_SYSTEM_USER_ID: "system",
    },
  });
  return {
    stdout: out.stdout ?? "",
    stderr: out.stderr ?? "",
    status: out.status,
  };
}

/** Run a sqlite3 query against the given DB. */
function sqlite(db: string, stmt: string): string {
  return execSync(`sqlite3 ${JSON.stringify(db)} ${JSON.stringify(stmt)}`, {
    encoding: "utf8",
  }).trim();
}

/** Convenience: integer COUNT(*) via sqlite. */
function count(db: string, stmt: string): number {
  return parseInt(sqlite(db, stmt), 10);
}

const Q = {
  proj: (slug: string) =>
    `SELECT COUNT(*) FROM projects WHERE slug='${slug}';`,
  projLike: (pattern: string) =>
    `SELECT COUNT(*) FROM projects WHERE slug LIKE '${pattern}';`,
  versions: (slugCond: string) =>
    `SELECT COUNT(*) FROM project_versions v JOIN projects p ON p.id=v.project_id WHERE ${slugCond};`,
  violations: (slugCond: string) =>
    `SELECT COUNT(*) FROM violations v JOIN project_versions pv ON pv.id=v.version_id JOIN projects p ON p.id=pv.project_id WHERE ${slugCond};`,
  markers: (slugCond: string) =>
    `SELECT COUNT(*) FROM backfill_markers m JOIN projects p ON p.id=m.project_id WHERE ${slugCond};`,
  maxVersionIdx: (slug: string) =>
    `SELECT MAX(version_index) FROM project_versions v JOIN projects p ON p.id=v.project_id WHERE p.slug='${slug}';`,
};

/** True when `go` is on PATH. The CLI build needs it. */
function hasGo(): boolean {
  try {
    execSync("go version", { stdio: "ignore" });
    return true;
  } catch {
    return false;
  }
}

/**
 * Synthetic AuditResult JSON. Mirrors services/ds-service/internal/audit/types.go
 * AuditResult → at minimum schema_version + screens[].fixes[]; backfill.go
 * tolerates missing optional fields.
 */
function fixtureSidecar(slug: string, fixCount: number): string {
  const fixes = Array.from({ length: fixCount }, (_, i) => ({
    node_id: `node-${slug}-${i}`,
    node_name: `Node ${i}`,
    property: i % 2 === 0 ? "fill" : "padding",
    observed: i % 2 === 0 ? "#FF6633" : "13px",
    token_path:
      i % 2 === 0 ? "surface.brand.500" : "spacing.md",
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
        name: `${slug} screen 1`,
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

test.describe("U12 sidecar migration CLI", () => {
  test.skip(!hasGo(), "go toolchain not on PATH; cannot exercise migrate-sidecars CLI");

  test("real lib/audit/ → 'found 0 sidecar(s)' (only manifest + aggregate present)", async () => {
    const db = tmpDB();
    const out = runMigrate({ dir: REAL_AUDIT_DIR, db });
    expect(out.status, `stderr=${out.stderr}`).toBe(0);
    // The CLI logs to stderr via log.Printf; status code is the load-bearing
    // check. Output assertion is best-effort string match.
    const combined = `${out.stdout}\n${out.stderr}`;
    expect(combined).toMatch(/found 0 sidecar/);
  });

  test("synthetic fixtures → projects/violations rows match expectations", async () => {
    const fixDir = mkdtempSync(path.join(os.tmpdir(), "u12-sidecar-"));
    const db = tmpDB();

    const fixtures = [
      { slug: "u12-syn-alpha", fixes: 3 },
      { slug: "u12-syn-bravo", fixes: 5 },
      { slug: "u12-syn-charlie", fixes: 2 },
    ];
    let totalFixes = 0;
    for (const f of fixtures) {
      writeFileSync(
        path.join(fixDir, `${f.slug}.json`),
        fixtureSidecar(f.slug, f.fixes),
      );
      totalFixes += f.fixes;
    }

    const out = runMigrate({ dir: fixDir, db });
    expect(out.status, `stderr=${out.stderr}`).toBe(0);

    const cond = "p.slug LIKE 'u12-syn-%'";
    expect(count(db, Q.projLike("u12-syn-%"))).toBe(fixtures.length);
    expect(count(db, Q.versions(cond))).toBe(fixtures.length);
    expect(count(db, Q.violations(cond))).toBe(totalFixes);
    expect(count(db, Q.markers(cond))).toBe(fixtures.length);

    rmSync(fixDir, { recursive: true, force: true });
  });

  test("idempotency: re-running CLI on unchanged sidecars adds zero rows", async () => {
    const fixDir = mkdtempSync(path.join(os.tmpdir(), "u12-sidecar-idem-"));
    const db = tmpDB();
    writeFileSync(
      path.join(fixDir, "u12-idem.json"),
      fixtureSidecar("u12-idem", 4),
    );

    const cond = "p.slug='u12-idem'";
    const first = runMigrate({ dir: fixDir, db });
    expect(first.status).toBe(0);

    const projCount1 = count(db, Q.proj("u12-idem"));
    const verCount1 = count(db, Q.versions(cond));
    const violationCount1 = count(db, Q.violations(cond));
    expect(projCount1).toBe(1);
    expect(verCount1).toBe(1);
    expect(violationCount1).toBe(4);

    const second = runMigrate({ dir: fixDir, db });
    expect(second.status).toBe(0);
    expect(`${second.stdout}\n${second.stderr}`).toMatch(/skipped=1|created=0/);

    // Counts MUST be unchanged after re-run.
    expect(count(db, Q.proj("u12-idem"))).toBe(projCount1);
    expect(count(db, Q.versions(cond))).toBe(verCount1);
    expect(count(db, Q.violations(cond))).toBe(violationCount1);

    rmSync(fixDir, { recursive: true, force: true });
  });

  test("bumped mtime: re-run creates a new project_versions row", async () => {
    const fixDir = mkdtempSync(path.join(os.tmpdir(), "u12-sidecar-mtime-"));
    const db = tmpDB();
    const file = path.join(fixDir, "u12-mtime.json");
    writeFileSync(file, fixtureSidecar("u12-mtime", 2));

    // Backdate the original mtime so we can advance it deterministically.
    const past = new Date(Date.now() - 24 * 3_600_000);
    utimesSync(file, past, past);

    const cond = "p.slug='u12-mtime'";
    const first = runMigrate({ dir: fixDir, db });
    expect(first.status).toBe(0);
    expect(count(db, Q.versions(cond))).toBe(1);

    // Bump mtime to future + change content (extra fix); Backfiller updates.
    writeFileSync(file, fixtureSidecar("u12-mtime", 7));
    const future = new Date(statSync(file).mtimeMs + 5_000);
    utimesSync(file, future, future);

    const second = runMigrate({ dir: fixDir, db });
    expect(second.status).toBe(0);
    expect(`${second.stdout}\n${second.stderr}`).toMatch(
      /updated=1|created=0 updated=1/,
    );
    expect(count(db, Q.versions(cond))).toBe(2);
    expect(count(db, Q.maxVersionIdx("u12-mtime"))).toBe(2);

    rmSync(fixDir, { recursive: true, force: true });
  });
});
