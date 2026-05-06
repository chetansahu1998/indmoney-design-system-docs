/**
 * node-classifier-parity.test.ts — TS-side fixture-driven parity check
 * for U7 of plan 2026-05-06-003. Loads the same JSON fixture the Go
 * test consumes (services/ds-service/internal/projects/testdata/
 * cluster_classifier_fixture.json) and asserts that walking the tree
 * with TS shouldRasterize collects the expected node IDs.
 *
 * The cross-language fixture is the contract between Go isCluster
 * (services/ds-service/internal/projects/pipeline_cluster_prerender.go)
 * and TS shouldRasterize (../node-classifier.ts). Drift on either
 * side fails one of the two test runs.
 *
 * Test shape mirrors gesture-tracker.test.ts: self-rolling assertions,
 * runAll() driver for whatever test runner picks the file up. The
 * project does not currently wire a JS unit-test runner (only Playwright
 * test:parity); when one is added, this file just works without
 * modification.
 *
 * KNOWN DRIFT — production collectClusterIDs (useIconClusterURLs.ts:40)
 * does NOT prune hidden / removed nodes during walk; the Go walkClusters
 * does. To exercise predicate-level parity (the actual contract) without
 * being defeated by this difference, this test uses a local walker
 * (walkLikeGo) that mirrors Go walkClusters' visibility pruning. The
 * production gap is tracked separately — reconciliation belongs in the
 * useIconClusterURLs walker, not in node-classifier.ts.
 */

import * as fs from "fs";
import * as path from "path";

import type { CanonicalNode } from "../types";
import { shouldRasterize } from "../node-classifier";

function assert(cond: unknown, msg: string): void {
  if (!cond) throw new Error(`assertion failed: ${msg}`);
}

// Test-side walker that mirrors Go walkClusters semantics:
// - Pre-prune invisible / removed nodes (Go behavior)
// - Apply shouldRasterize at each node
// - When classified as cluster, push id and DO NOT descend
// - Otherwise recurse into children
function walkLikeGo(node: CanonicalNode | null, acc: string[]): void {
  if (!node) return;
  // Visibility pruning — matches Go walkClusters lines 121-129.
  // CanonicalNode types `visible?: boolean` and `removed?: boolean`;
  // both default to "visible" when absent.
  const visibleField = (node as unknown as { visible?: unknown }).visible;
  if (typeof visibleField === "boolean" && !visibleField) return;
  const removedField = (node as unknown as { removed?: unknown }).removed;
  if (typeof removedField === "boolean" && removedField) return;

  if (shouldRasterize(node) && typeof node.id === "string") {
    acc.push(node.id);
    return; // Cluster encompasses subtree — do not descend.
  }
  if (Array.isArray(node.children)) {
    for (const c of node.children) walkLikeGo(c as CanonicalNode, acc);
  }
}

interface FixtureCase {
  name: string;
  tree: unknown;
  expected_cluster_ids: string[];
  comparison_notes?: string;
}
interface Fixture {
  cases: FixtureCase[];
}

function loadFixture(): Fixture {
  // Walk up from this test file to the repo root, then down into the Go
  // testdata directory. Matches the path Go test reads from. If the
  // relative pathing breaks (e.g., file moves), CI fails loudly.
  const fixturePath = path.resolve(
    __dirname,
    "../../../../../services/ds-service/internal/projects/testdata/cluster_classifier_fixture.json",
  );
  const raw = fs.readFileSync(fixturePath, "utf8");
  return JSON.parse(raw) as Fixture;
}

function arraysEqualSet(a: string[], b: string[]): boolean {
  if (a.length !== b.length) return false;
  const sa = [...a].sort();
  const sb = [...b].sort();
  for (let i = 0; i < sa.length; i++) if (sa[i] !== sb[i]) return false;
  return true;
}

export function runAll(): void {
  const fix = loadFixture();
  assert(fix.cases.length > 0, "fixture has no cases");

  let failed = 0;
  for (const c of fix.cases) {
    try {
      const got: string[] = [];
      walkLikeGo(c.tree as CanonicalNode, got);
      assert(
        arraysEqualSet(got, c.expected_cluster_ids),
        `parity drift for case "${c.name}"\n  want: ${JSON.stringify(c.expected_cluster_ids.sort())}\n  got:  ${JSON.stringify(got.sort())}\n  notes: ${c.comparison_notes ?? ""}`,
      );
      // eslint-disable-next-line no-console
      console.log(`ok  parity ${c.name}`);
    } catch (err) {
      failed++;
      // eslint-disable-next-line no-console
      console.error(
        `fail parity ${c.name}: ${err instanceof Error ? err.message : String(err)}`,
      );
    }
  }
  if (failed > 0) throw new Error(`${failed} node-classifier parity case(s) failed`);
}
