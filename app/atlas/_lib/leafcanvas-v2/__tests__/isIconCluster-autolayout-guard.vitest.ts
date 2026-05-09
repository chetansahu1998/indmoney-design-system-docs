/**
 * isIconCluster-autolayout-guard.vitest.ts — pin the 2026-05-09 fix that
 * refuses clustering when the subtree contains autolayout FRAME children.
 *
 * Production cases:
 *   - Gold/Silver index screens: time-frame pills (1D/1W/1M/3M/1Y/3Y/5Y)
 *     live inside an autolayout horizontal FRAME below the chart line.
 *     Pre-fix the entire 375×556 phone screen rasterized as one PNG;
 *     designers couldn't click pills or the price header.
 *   - Top-N ETF list cards: each row is an autolayout HORIZONTAL frame
 *     containing an icon + name + price. List container is autolayout
 *     VERTICAL. Pre-fix the whole card rasterized; designers couldn't
 *     click an individual row to inspect overrides.
 *
 * The guard fires AFTER the named-pattern fast paths (chart-name,
 * illustration-name, icon-name) so explicit illustrations/charts still
 * cluster. It only stops the structural-heuristic fallback that was
 * catching anonymous wrappers it shouldn't.
 */

import { describe, expect, it } from "vitest";

import { hasAutolayoutDescendant, isIconCluster } from "../icon-cluster-resolver";
import type { CanonicalNode } from "../types";

function vector(id: string): CanonicalNode {
  return { id, type: "VECTOR", name: "Vector" };
}

function autolayoutFrame(id: string, dir: "HORIZONTAL" | "VERTICAL", children: CanonicalNode[]): CanonicalNode {
  return {
    id,
    type: "FRAME",
    name: "Row",
    layoutMode: dir,
    children,
    absoluteBoundingBox: { x: 0, y: 0, width: 343, height: 60 },
  };
}

describe("hasAutolayoutDescendant", () => {
  it("returns false for a leaf vector", () => {
    expect(hasAutolayoutDescendant(vector("v"))).toBe(false);
  });

  it("returns false for nested wrappers without layoutMode", () => {
    const node: CanonicalNode = {
      id: "g",
      type: "GROUP",
      children: [
        { id: "g2", type: "GROUP", children: [vector("v")] },
      ],
    };
    expect(hasAutolayoutDescendant(node)).toBe(false);
  });

  it("returns true when a direct child is an autolayout FRAME", () => {
    const node: CanonicalNode = {
      id: "wrap",
      type: "FRAME",
      children: [autolayoutFrame("row", "HORIZONTAL", [vector("v")])],
    };
    expect(hasAutolayoutDescendant(node)).toBe(true);
  });

  it("returns true when an autolayout FRAME is deeply nested", () => {
    const node: CanonicalNode = {
      id: "wrap",
      type: "FRAME",
      children: [
        {
          id: "g",
          type: "GROUP",
          children: [
            {
              id: "g2",
              type: "GROUP",
              children: [autolayoutFrame("row", "VERTICAL", [vector("v")])],
            },
          ],
        },
      ],
    };
    expect(hasAutolayoutDescendant(node)).toBe(true);
  });

  it("excludes the wrapper itself — only DESCENDANTS count", () => {
    // The classified node IS autolayout but its children aren't. Some
    // illustration designs use auto-layout at the top level for a clean
    // bbox; we still want them to cluster (no inner UI containers to
    // freeze).
    const node: CanonicalNode = {
      id: "wrap",
      type: "FRAME",
      layoutMode: "HORIZONTAL",
      children: [vector("v1"), vector("v2"), vector("v3")],
    };
    expect(hasAutolayoutDescendant(node)).toBe(false);
  });

  it("treats INSTANCE and COMPONENT autolayout the same as FRAME", () => {
    const inst: CanonicalNode = {
      id: "wrap",
      type: "GROUP",
      children: [{
        id: "i",
        type: "INSTANCE",
        layoutMode: "HORIZONTAL",
        children: [vector("v")],
      }],
    };
    expect(hasAutolayoutDescendant(inst)).toBe(true);

    const comp: CanonicalNode = {
      id: "wrap",
      type: "GROUP",
      children: [{
        id: "c",
        type: "COMPONENT",
        layoutMode: "VERTICAL",
        children: [vector("v")],
      }],
    };
    expect(hasAutolayoutDescendant(comp)).toBe(true);
  });
});

describe("isIconCluster — autolayout guard", () => {
  it("refuses to cluster a wrapper whose subtree has autolayout FRAMEs (chart screen)", () => {
    // Chart screen with vector chart line + autolayout pills below. Has
    // 20 vectors (chart line) and 7 text labels (1D/1W/1M/3M/1Y/3Y/5Y),
    // which would PASS the leaf-count heuristic (shapes>=8, texts <=
    // 30) and cluster — but the autolayout pills mean it's a screen
    // with interactive UI, not a pure illustration.
    const chartShapes: CanonicalNode[] = Array.from({ length: 20 }, (_, i) => vector(`chart-${i}`));
    const pillTexts: CanonicalNode[] = ["1D", "1W", "1M", "3M", "1Y", "3Y", "5Y"].map(
      (label, i) => ({ id: `pill-${i}`, type: "TEXT", characters: label }),
    );
    const screen: CanonicalNode = {
      id: "screen",
      type: "FRAME",
      name: "Anonymous chart screen", // no chart-name fast-path match
      absoluteBoundingBox: { x: 0, y: 0, width: 375, height: 556 },
      children: [
        // Chart line subtree
        { id: "chart-line", type: "GROUP", children: chartShapes },
        // Pills row — autolayout HORIZONTAL
        autolayoutFrame("pills-row", "HORIZONTAL", pillTexts),
      ],
    };
    expect(isIconCluster(screen)).toBe(false);
  });

  it("refuses to cluster a list card with autolayout rows (Top-N ETFs)", () => {
    // 4-row list. Each row is autolayout horizontal with icon + name +
    // price. List container is autolayout vertical. Even though every
    // row has shape leaves (icon vectors), the autolayout structure
    // signals interactive UI.
    const row = (i: number): CanonicalNode => autolayoutFrame(`row-${i}`, "HORIZONTAL", [
      { id: `icon-${i}`, type: "GROUP", children: [vector(`vi-${i}-1`), vector(`vi-${i}-2`)] },
      { id: `name-${i}`, type: "TEXT", characters: `Fund ${i}` },
      { id: `price-${i}`, type: "TEXT", characters: "₹25.57" },
    ]);
    const list: CanonicalNode = {
      id: "list",
      type: "FRAME",
      name: "Top Gold ETFs",
      layoutMode: "VERTICAL",
      absoluteBoundingBox: { x: 0, y: 0, width: 343, height: 373 },
      children: [row(0), row(1), row(2), row(3)],
    };
    expect(isIconCluster(list)).toBe(false);
  });

  it("still clusters a pure illustration GROUP with no autolayout", () => {
    // Vault-with-coins style illustration: many vector paths, no autolayout.
    // Should still pass — this is the FD Upswing case from earlier today.
    const shapes = Array.from({ length: 20 }, (_, i) => vector(`v-${i}`));
    const wrap: CanonicalNode = {
      id: "wrap",
      type: "GROUP",
      name: "Group 1321319461",
      children: [{ id: "inner", type: "GROUP", children: shapes }],
    };
    expect(isIconCluster(wrap)).toBe(true);
  });
});
