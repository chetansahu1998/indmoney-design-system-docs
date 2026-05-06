/**
 * node-classifier-parity.test.ts — TS-side fixture-driven parity check
 * against the Go isCluster/walkClusters implementation. Vitest test,
 * picked up by vitest.config.ts.
 *
 * The cross-language fixture (services/ds-service/internal/projects/
 * testdata/cluster_classifier_fixture.json) is the contract between Go
 * isCluster (pipeline_cluster_prerender.go) and TS shouldRasterize
 * (../node-classifier.ts). Drift on either side fails one of the two
 * test runs simultaneously.
 *
 * walkLikeGo mirrors Go walkClusters semantics (visibility/removed
 * pruning before classification, prune subtree on cluster). Production
 * useIconClusterURLs.collectClusterIDs ALSO has the same visibility
 * pruning since the U7-followup commit.
 */

import * as fs from "fs";
import * as path from "path";

import { describe, expect, it } from "vitest";

import type { CanonicalNode } from "../types";
import { shouldRasterize } from "../node-classifier";

function walkLikeGo(node: CanonicalNode | null, acc: string[]): void {
  if (!node) return;
  // Visibility pruning — matches Go walkClusters lines 121-129.
  const visibleField = (node as unknown as { visible?: unknown }).visible;
  if (typeof visibleField === "boolean" && !visibleField) return;
  const removedField = (node as unknown as { removed?: unknown }).removed;
  if (typeof removedField === "boolean" && removedField) return;

  if (shouldRasterize(node) && typeof node.id === "string") {
    acc.push(node.id);
    return; // cluster encompasses subtree — do not descend
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
  // testdata directory. The Go test reads the same path; if the relative
  // pathing breaks (file moved), this test fails loudly.
  const fixturePath = path.resolve(
    __dirname,
    "../../../../../services/ds-service/internal/projects/testdata/cluster_classifier_fixture.json",
  );
  const raw = fs.readFileSync(fixturePath, "utf8");
  return JSON.parse(raw) as Fixture;
}

describe("node-classifier parity (vs Go isCluster fixture)", () => {
  const fix = loadFixture();
  it("fixture has cases", () => {
    expect(fix.cases.length).toBeGreaterThan(0);
  });

  for (const c of fix.cases) {
    it(`parity: ${c.name}`, () => {
      const got: string[] = [];
      walkLikeGo(c.tree as CanonicalNode, got);
      expect([...got].sort()).toEqual([...c.expected_cluster_ids].sort());
    });
  }
});
