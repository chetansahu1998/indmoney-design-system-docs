/**
 * AtomicChildInspector.vitest.ts — unit coverage for findByFigmaID, the
 * tree-search helper that maps a Figma node id to the canonical-tree
 * node + parent the inspector renders.
 *
 * Regression scope (2026-05-08): pre-fix the helper assumed callers
 * passed the bare document node, but the live store's
 * `canonicalTreeByScreenID` slot stores the Figma `/v1/files/.../nodes`
 * envelope shape `{ document, styles, components, ... }`. Walking the
 * envelope returned null because the envelope itself has no id and no
 * children, which manifested as the inspector showing "No layer
 * selected" on every atomic click. The fix is in
 * AtomicChildInspector.tsx::findByFigmaID — it now unwraps the
 * envelope before walking. These tests pin both the unwrap and the
 * underlying tree walk.
 */

import { describe, expect, it } from "vitest";

import { findByFigmaID } from "../AtomicChildInspector";

describe("findByFigmaID", () => {
  it("returns the bare-tree root when the id matches the root", () => {
    const tree = { id: "root", type: "FRAME", children: [] };
    const got = findByFigmaID(tree, "root");
    expect(got?.node.id).toBe("root");
    expect(got?.parent).toBeNull();
  });

  it("walks children to find a deep match in a bare tree", () => {
    const tree = {
      id: "root",
      type: "FRAME",
      children: [
        {
          id: "row",
          type: "FRAME",
          children: [{ id: "btn", type: "TEXT", characters: "Buy" }],
        },
      ],
    };
    const got = findByFigmaID(tree, "btn");
    expect(got?.node.id).toBe("btn");
    expect(got?.parent?.id).toBe("row");
  });

  it("unwraps the Figma envelope and walks .document", () => {
    // This is the real-store shape: { document, styles, components, ... }.
    // Pre-fix the walker stopped at the envelope and returned null.
    const envelope = {
      document: {
        id: "doc",
        type: "FRAME",
        children: [{ id: "leaf", type: "TEXT", characters: "Hello" }],
      },
      styles: {},
      components: {},
      schemaVersion: 0,
    };
    const got = findByFigmaID(envelope, "leaf");
    expect(got?.node.id).toBe("leaf");
    expect(got?.parent?.id).toBe("doc");
  });

  it("returns null when no node matches", () => {
    const tree = { id: "root", type: "FRAME", children: [] };
    expect(findByFigmaID(tree, "missing")).toBeNull();
  });

  it("returns null on null/undefined/non-object input", () => {
    expect(findByFigmaID(null, "x")).toBeNull();
    expect(findByFigmaID(undefined, "x")).toBeNull();
    expect(findByFigmaID("string", "x")).toBeNull();
    expect(findByFigmaID(42, "x")).toBeNull();
  });

  it("returns null on an envelope without a document field", () => {
    const bogus = { styles: {}, components: {} };
    expect(findByFigmaID(bogus, "x")).toBeNull();
  });

  it("does not re-enter children that aren't arrays", () => {
    const tree = {
      id: "root",
      type: "FRAME",
      // children: not-an-array shouldn't crash the walk
      children: "oops" as unknown as never,
    };
    expect(findByFigmaID(tree, "root")).toMatchObject({ node: { id: "root" } });
    expect(findByFigmaID(tree, "absent")).toBeNull();
  });

  it("matches the first occurrence when duplicate ids exist (depth-first)", () => {
    // Real Figma trees should never contain duplicate ids, but we want
    // deterministic behaviour if they ever do.
    const tree = {
      id: "root",
      type: "FRAME",
      children: [
        { id: "dup", type: "VECTOR", children: [{ id: "deep", type: "VECTOR" }] },
        { id: "dup", type: "VECTOR" },
      ],
    };
    const got = findByFigmaID(tree, "dup");
    expect(got?.node.id).toBe("dup");
    expect(got?.parent?.id).toBe("root");
  });
});
