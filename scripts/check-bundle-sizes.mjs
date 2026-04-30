#!/usr/bin/env node
/**
 * scripts/check-bundle-sizes.mjs — Phase 1 perf budget enforcer.
 *
 * Asserts the gzipped sizes of the `/projects/[slug]` route bundles stay
 * under the Phase 1 budgets:
 *
 *   - Initial route shell (rootMain + page chunks)        ≤ 200 KB gz
 *   - Atlas chunk (r3f + drei)                            ≤ 350 KB gz
 *   - DRD chunk (BlockNote)                               ≤ 400 KB gz
 *   - Animations chunk (GSAP + Lenis)                     ≤  50 KB gz
 *
 * Runs after `npm run build`. Reads `.next/build-manifest.json` for the
 * root-shared bundle and `.next/server/app/projects/[slug]/page/
 * react-loadable-manifest.json` for the page chunks. Identifies the
 * dynamic-imported atlas / drd / animations chunks by their content
 * fingerprint (string match against well-known imports).
 *
 * Why content fingerprint vs explicit Webpack chunk names: this app uses
 * Turbopack (Next 16). Turbopack emits hashed filenames; there's no
 * `chunks/atlas.js` to look up by name. We instead:
 *   1. Enumerate every JS chunk under .next/static/chunks/.
 *   2. Read each chunk's bytes (only first ~256KB — the marker strings live
 *      near the top of webpack module wrappers).
 *   3. Match against fingerprint strings characteristic of each library:
 *        atlas       — "@react-three/fiber", "three"
 *        drd         — "@blocknote", "prosemirror"
 *        animations  — "gsap", "lenis"
 *   4. Sum gzipped size per category. Fail when > budget.
 *
 * Exit 1 on first violation. Prints a summary table on success.
 *
 * Run via:  npm run check:bundles  (after npm run build).
 */

import { promises as fs } from "node:fs";
import { gzipSync } from "node:zlib";
import path from "node:path";
import { fileURLToPath } from "node:url";

// Project root: this file lives at scripts/, so root is one level up.
const __dirname = path.dirname(fileURLToPath(import.meta.url));
const ROOT = path.resolve(__dirname, "..");
const NEXT_DIR = path.join(ROOT, ".next");
const STATIC_CHUNKS = path.join(NEXT_DIR, "static", "chunks");

// Phase 1 budgets in bytes (gzipped).
const KB = 1024;
const BUDGETS = {
  routeShell: 200 * KB,
  atlas: 350 * KB,
  drd: 400 * KB,
  animations: 50 * KB,
};

// Library fingerprints. We match against the chunk's first 256KB so the
// marker string check stays fast for big chunks.
// Marker strings that survive minification. Picked by inspecting the
// compiled chunks of an actual `npm run build` — see the script header for
// the fingerprint methodology. If a future Next/Turbopack release tree-
// shakes a marker out, swap to a different recognizable string from the
// same package and update the unit-fingerprint comment.
const FINGERPRINTS = {
  // r3f's three.js bundling carries the well-known WebGL shader chunks.
  atlas: ["WEBGL_compressed_texture_etc1", "BoxGeometry", "PerspectiveCamera"],
  // BlockNote's editor wraps prosemirror; minified output keeps the
  // prosemirror-* package markers as embedded literal strings.
  drd: ["prosemirror-model", "prosemirror-view"],
  // GSAP and Lenis both retain their package keyword as an internal token.
  animations: ["GSAP", "lenis"],
};

// Read up to `bytes` of a file as a string for fingerprint matching. We
// default to 4 MB which covers the largest chunks in the current build
// (the BlockNote chunk weighs ~1 MB raw); tune up if a chunk grows past
// this limit.
async function readHead(file, bytes = 4 * 1024 * 1024) {
  const fd = await fs.open(file, "r");
  try {
    const stat = await fd.stat();
    const len = Math.min(bytes, stat.size);
    const buf = Buffer.alloc(len);
    const { bytesRead } = await fd.read(buf, 0, len, 0);
    return buf.subarray(0, bytesRead).toString("utf8");
  } finally {
    await fd.close();
  }
}

async function gzippedSize(file) {
  const bytes = await fs.readFile(file);
  return gzipSync(bytes).length;
}

async function exists(p) {
  try {
    await fs.access(p);
    return true;
  } catch {
    return false;
  }
}

function categorize(headText) {
  for (const [name, markers] of Object.entries(FINGERPRINTS)) {
    if (markers.some((m) => headText.includes(m))) return name;
  }
  return null;
}

async function main() {
  if (!(await exists(NEXT_DIR))) {
    console.error("✘ .next/ not found — run `npm run build` first.");
    process.exit(2);
  }
  if (!(await exists(STATIC_CHUNKS))) {
    console.error(`✘ ${STATIC_CHUNKS} missing — build output incomplete.`);
    process.exit(2);
  }

  // Enumerate every JS chunk.
  const allEntries = await fs.readdir(STATIC_CHUNKS, { withFileTypes: true });
  const allJs = allEntries
    .filter((e) => e.isFile() && e.name.endsWith(".js"))
    .map((e) => path.join(STATIC_CHUNKS, e.name));

  // Build manifest: rootMainFiles makes up the initial document shell.
  const buildManifest = JSON.parse(
    await fs.readFile(path.join(NEXT_DIR, "build-manifest.json"), "utf8"),
  );
  const rootMain = buildManifest.rootMainFiles ?? [];
  const polyfills = buildManifest.polyfillFiles ?? [];
  const initialShellRel = [...rootMain, ...polyfills].filter((p) =>
    p.endsWith(".js"),
  );

  // /projects/[slug] page-specific manifest.
  const projectsPageManifest = path.join(
    NEXT_DIR,
    "server",
    "app",
    "projects",
    "[slug]",
    "page",
    "react-loadable-manifest.json",
  );
  let projectsPageChunks = [];
  if (await exists(projectsPageManifest)) {
    const m = JSON.parse(await fs.readFile(projectsPageManifest, "utf8"));
    projectsPageChunks = Object.values(m).flatMap((entry) =>
      (entry.files ?? []).filter((f) => f.endsWith(".js")),
    );
  }

  // Categorize every chunk by content fingerprint. Walking ALL chunks (not
  // just route-loaded ones) is correct — Next dynamic-import splitting may
  // emit a chunk that the route's manifest doesn't list inside its `files`
  // array (especially with Turbopack), but the chunk still ships when the
  // dynamic import resolves at runtime.
  const categorized = { atlas: [], drd: [], animations: [] };
  for (const file of allJs) {
    let head;
    try {
      head = await readHead(file);
    } catch {
      continue;
    }
    const cat = categorize(head);
    if (cat) categorized[cat].push(file);
  }

  // Sum sizes per category.
  const sums = {};
  for (const [cat, files] of Object.entries(categorized)) {
    let sum = 0;
    for (const f of files) sum += await gzippedSize(f);
    sums[cat] = sum;
  }

  // Initial route shell = rootMainFiles + polyfills + page-specific JS that
  // does NOT belong to a deferred dynamic-import category. We approximate
  // the deferred set as whatever was content-fingerprinted to atlas/drd/
  // animations.
  const deferred = new Set(
    Object.values(categorized)
      .flat()
      .map((f) => path.relative(NEXT_DIR, f)),
  );
  const initialShellAbs = initialShellRel
    .map((rel) => path.join(NEXT_DIR, rel))
    .filter((p) => !deferred.has(path.relative(NEXT_DIR, p)));
  const projectsPageAbs = projectsPageChunks
    .map((rel) => path.join(NEXT_DIR, rel))
    .filter((p) => !deferred.has(path.relative(NEXT_DIR, p)));
  let routeShellSum = 0;
  for (const f of [...initialShellAbs, ...projectsPageAbs]) {
    if (await exists(f)) routeShellSum += await gzippedSize(f);
  }
  sums.routeShell = routeShellSum;

  // Print + assert.
  const rows = [
    ["Initial /projects/[slug] route shell", sums.routeShell, BUDGETS.routeShell],
    ["chunks/atlas (r3f + drei)", sums.atlas, BUDGETS.atlas],
    ["chunks/drd (BlockNote)", sums.drd, BUDGETS.drd],
    ["chunks/animations (GSAP + Lenis)", sums.animations, BUDGETS.animations],
  ];

  console.log("\nPhase 1 bundle-size budgets — gzipped");
  console.log("─".repeat(72));
  let failed = 0;
  for (const [label, got, budget] of rows) {
    const status =
      got > budget ? "✘ FAIL" : got > budget * 0.85 ? "⚠ WARN" : "✓ ok";
    if (got > budget) failed++;
    const gotKB = (got / KB).toFixed(1).padStart(7);
    const budgetKB = (budget / KB).toFixed(0).padStart(4);
    const pct = ((got / budget) * 100).toFixed(0).padStart(3);
    console.log(
      `${status}  ${label.padEnd(40)} ${gotKB} KB / ${budgetKB} KB (${pct}%)`,
    );
  }
  console.log("─".repeat(72));

  if (failed > 0) {
    console.error(`\n${failed} bundle(s) exceeded their Phase 1 budget.`);
    process.exit(1);
  }
  console.log("\nAll bundles within Phase 1 budgets.");
}

main().catch((err) => {
  console.error(err);
  process.exit(2);
});
