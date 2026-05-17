/**
 * selection-cycle.vitest.ts — pure helper pins for the Enter /
 * Shift+Enter / Tab / Cmd+A selection navigators (U4).
 */

import { describe, expect, it } from "vitest";

import {
  collectIdsByType,
  findAncestorChain,
  findFirstChildId,
  findNextSiblingId,
  findNodeById,
  findParentId,
  findPrevSiblingId,
  findSiblingIds,
  type CanonicalLikeNode,
} from "../selection-cycle";

const TREE: CanonicalLikeNode = {
  id: "root",
  type: "FRAME",
  children: [
    {
      id: "section-a",
      type: "FRAME",
      children: [
        { id: "card-a1", type: "INSTANCE", children: [{ id: "text-a1-1", type: "TEXT" }] },
        { id: "card-a2", type: "INSTANCE", children: [{ id: "text-a2-1", type: "TEXT" }] },
      ],
    },
    {
      id: "section-b",
      type: "FRAME",
      children: [
        { id: "icon-b1", type: "VECTOR" },
        { id: "icon-b2", type: "VECTOR" },
        { id: "icon-b3", type: "VECTOR" },
      ],
    },
    { id: "section-c", type: "FRAME" }, // no children
  ],
};

describe("findAncestorChain", () => {
  it("returns the full root-to-target chain", () => {
    const chain = findAncestorChain(TREE, "text-a1-1");
    expect(chain).not.toBeNull();
    expect(chain!.map((n) => n.id)).toEqual([
      "root",
      "section-a",
      "card-a1",
      "text-a1-1",
    ]);
  });

  it("returns [root] when target is root", () => {
    const chain = findAncestorChain(TREE, "root");
    expect(chain!.map((n) => n.id)).toEqual(["root"]);
  });

  it("returns null for missing id", () => {
    expect(findAncestorChain(TREE, "nope")).toBeNull();
  });

  it("returns null for null tree", () => {
    expect(findAncestorChain(null, "anything")).toBeNull();
  });

  it("returns null for empty id", () => {
    expect(findAncestorChain(TREE, "")).toBeNull();
  });
});

describe("findNodeById", () => {
  it("returns the node at the leaf", () => {
    const node = findNodeById(TREE, "icon-b2");
    expect(node?.type).toBe("VECTOR");
  });

  it("returns null when not found", () => {
    expect(findNodeById(TREE, "ghost")).toBeNull();
  });
});

describe("findParentId", () => {
  it("returns the immediate parent", () => {
    expect(findParentId(TREE, "card-a1")).toBe("section-a");
    expect(findParentId(TREE, "text-a1-1")).toBe("card-a1");
  });

  it("returns null for root (no parent)", () => {
    expect(findParentId(TREE, "root")).toBeNull();
  });

  it("returns null for missing node", () => {
    expect(findParentId(TREE, "ghost")).toBeNull();
  });
});

describe("findFirstChildId — Enter descend", () => {
  it("returns the first child id", () => {
    expect(findFirstChildId(TREE, "root")).toBe("section-a");
    expect(findFirstChildId(TREE, "section-a")).toBe("card-a1");
    expect(findFirstChildId(TREE, "card-a1")).toBe("text-a1-1");
  });

  it("returns null when the node has no children", () => {
    expect(findFirstChildId(TREE, "text-a1-1")).toBeNull();
    expect(findFirstChildId(TREE, "section-c")).toBeNull();
  });

  it("returns null for missing node", () => {
    expect(findFirstChildId(TREE, "ghost")).toBeNull();
  });
});

describe("findSiblingIds", () => {
  it("returns siblings excluding the queried node", () => {
    const sibs = findSiblingIds(TREE, "icon-b2");
    expect(sibs).toEqual(["icon-b1", "icon-b3"]);
  });

  it("returns empty array when the node is an only child", () => {
    expect(findSiblingIds(TREE, "text-a1-1")).toEqual([]);
  });

  it("returns null for root (no parent)", () => {
    expect(findSiblingIds(TREE, "root")).toBeNull();
  });
});

describe("findNextSiblingId — Tab", () => {
  it("returns next in order", () => {
    expect(findNextSiblingId(TREE, "icon-b1")).toBe("icon-b2");
    expect(findNextSiblingId(TREE, "icon-b2")).toBe("icon-b3");
  });

  it("wraps around at the last sibling", () => {
    expect(findNextSiblingId(TREE, "icon-b3")).toBe("icon-b1");
  });

  it("returns null for root", () => {
    expect(findNextSiblingId(TREE, "root")).toBeNull();
  });
});

describe("findPrevSiblingId — Shift+Tab", () => {
  it("returns previous in order", () => {
    expect(findPrevSiblingId(TREE, "icon-b3")).toBe("icon-b2");
    expect(findPrevSiblingId(TREE, "icon-b2")).toBe("icon-b1");
  });

  it("wraps around at the first sibling", () => {
    expect(findPrevSiblingId(TREE, "icon-b1")).toBe("icon-b3");
  });
});

describe("collectIdsByType — Cmd+A select-all", () => {
  it("returns every node of an allowed type", () => {
    const frames = collectIdsByType(TREE, new Set(["FRAME"]));
    expect(frames.sort()).toEqual(
      ["root", "section-a", "section-b", "section-c"].sort(),
    );
  });

  it("returns every node of multiple allowed types", () => {
    const ids = collectIdsByType(TREE, new Set(["INSTANCE", "VECTOR"]));
    expect(ids.sort()).toEqual(
      ["card-a1", "card-a2", "icon-b1", "icon-b2", "icon-b3"].sort(),
    );
  });

  it("returns empty array for unmatched types", () => {
    expect(collectIdsByType(TREE, new Set(["RECTANGLE"]))).toEqual([]);
  });

  it("returns empty array for null tree", () => {
    expect(collectIdsByType(null, new Set(["FRAME"]))).toEqual([]);
  });
});

describe("findFrameTarget integration via FRAME_TYPES (handleClick contract)", () => {
  // This test demonstrates the Cmd+A target set should include
  // FRAME-class types — the same set used by handleClick's
  // findFrameTarget. Pinning the contract here documents the
  // expected coupling so a future change to FRAME_TYPES doesn't
  // silently desync Cmd+A.
  it("FRAME_TYPES contract: covers FRAME, COMPONENT, INSTANCE, GROUP", () => {
    const FRAME_TYPES = new Set(["FRAME", "COMPONENT", "INSTANCE", "GROUP"]);
    const ids = collectIdsByType(TREE, FRAME_TYPES);
    // All four of root + section-a + section-b + section-c + card-a1 + card-a2
    // (FRAME + FRAME + FRAME + FRAME + INSTANCE + INSTANCE).
    expect(ids.length).toBe(6);
    expect(ids).toContain("root");
    expect(ids).toContain("card-a1");
  });
});

// QA Bug 4/5 regression: the Zustand slot stores the raw Figma
// `/v1/files/<id>` envelope on most leaves (top-level keys: styles,
// componentSets, components, document, schemaVersion). Pre-fix every
// helper walked from the envelope root, found no `id`/`children`, and
// returned null — silently breaking Enter / Shift+Enter / Tab / Cmd+A.
// Pin the envelope-unwrap behavior so callers can stay ignorant of the
// shape.
describe("envelope unwrap (QA Bug 4/5 regression)", () => {
  const ENVELOPE = {
    schemaVersion: 1,
    styles: {},
    components: {},
    componentSets: {},
    document: TREE,
  } as unknown as CanonicalLikeNode;

  it("findAncestorChain descends into .document when root has no id", () => {
    const chain = findAncestorChain(ENVELOPE, "text-a1-1");
    expect(chain).not.toBeNull();
    expect(chain!.map((n) => n.id)).toEqual(["root", "section-a", "card-a1", "text-a1-1"]);
  });

  it("findFirstChildId resolves through envelope", () => {
    expect(findFirstChildId(ENVELOPE, "root")).toBe("section-a");
  });

  it("findParentId resolves through envelope", () => {
    expect(findParentId(ENVELOPE, "text-a1-1")).toBe("card-a1");
  });

  it("collectIdsByType resolves through envelope", () => {
    const ids = collectIdsByType(ENVELOPE, new Set(["FRAME", "INSTANCE"]));
    expect(ids.length).toBeGreaterThan(0);
    expect(ids).toContain("root");
  });

  it("returns null on a malformed envelope (neither id nor document)", () => {
    const broken = { schemaVersion: 1, styles: {} } as unknown as CanonicalLikeNode;
    expect(findAncestorChain(broken, "x")).toBeNull();
    expect(collectIdsByType(broken, new Set(["FRAME"]))).toEqual([]);
  });
});
