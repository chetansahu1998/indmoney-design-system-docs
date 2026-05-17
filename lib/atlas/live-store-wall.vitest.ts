/**
 * live-store-wall.vitest.ts — plan 005 U7. Covers loadLeafWall + setLeafMode.
 *
 * Scope:
 *   - Happy path: server returns {wall: WallResult} → wallByLeaf populated.
 *   - Empty / 5xx → stamps null instead of throwing.
 *   - Leaf without sub_flow → null without a fetch.
 *   - setLeafMode round-trips through the store and is idempotent.
 */

import { beforeEach, afterEach, describe, expect, test, vi } from "vitest";

import { useAtlas } from "./live-store";
import type { Leaf, SubFlowSummary, WallResult } from "./types";

const SUB_FLOW: SubFlowSummary = {
  id: "sf-1",
  fullSlug: "wallet/m2m-settlement",
  name: "M2M Settlement",
  canvasLifecycle: "design-shipped",
  figmaSectionID: "sec-1",
  figmaFileKey: "FK-1",
};

const LEAF_BOUND: Leaf = {
  id: "leaf-bound",
  flow: "wallet",
  label: "Wallet M2M",
  frames: 5,
  violations: 0,
  subFlow: SUB_FLOW,
};

const LEAF_UNBOUND: Leaf = {
  id: "leaf-legacy",
  flow: "wallet",
  label: "Wallet legacy",
  frames: 5,
  violations: 0,
};

const SAMPLE_WALL: WallResult = {
  frames: [
    {
      figma_node_id: "1:23",
      frame_name: "Cold state",
      binding_status: "bound",
      criteria_count: 3,
      events_count: 1,
      copy_count: 2,
      edge_cases_count: 0,
      a11y_count: 1,
      total_word_count: 22,
      has_render: true,
    },
  ],
  counts: { total: 1, bound: 1, untagged: 0, orphaned: 0, coverage_percent: 100 },
};

vi.mock("../auth-client", () => ({
  getToken: () => "test-token",
  useAuth: { getState: () => ({ token: "test-token" }) },
}));

function seedStore(leaves: Leaf[]) {
  useAtlas.setState({
    leavesByFlow: { wallet: leaves },
    wallByLeaf: {},
    leafMode: {},
  });
}

let fetchMock: ReturnType<typeof vi.fn>;

beforeEach(() => {
  fetchMock = vi.fn();
  globalThis.fetch = fetchMock as unknown as typeof fetch;
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("loadLeafWall", () => {
  test("happy path — stamps WallResult under wallByLeaf[leafID]", async () => {
    seedStore([LEAF_BOUND]);
    fetchMock.mockResolvedValueOnce({
      ok: true,
      status: 200,
      json: async () => ({ wall: SAMPLE_WALL }),
    } as Response);

    await useAtlas.getState().loadLeafWall(LEAF_BOUND.id);

    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [url] = fetchMock.mock.calls[0]!;
    expect(url).toBe("/api/prd/wallet/m2m-settlement");

    const stored = useAtlas.getState().wallByLeaf[LEAF_BOUND.id];
    expect(stored).not.toBeNull();
    expect(stored?.counts.coverage_percent).toBe(100);
    expect(stored?.frames[0]?.frame_name).toBe("Cold state");
  });

  test("missing wall key in response stamps null without throwing", async () => {
    seedStore([LEAF_BOUND]);
    fetchMock.mockResolvedValueOnce({
      ok: true,
      status: 200,
      json: async () => ({ sub_flow: { id: "sf-1" } }),
    } as Response);

    await useAtlas.getState().loadLeafWall(LEAF_BOUND.id);

    const map = useAtlas.getState().wallByLeaf;
    expect(LEAF_BOUND.id in map).toBe(true);
    expect(map[LEAF_BOUND.id]).toBeNull();
  });

  test("leaf without sub_flow short-circuits to null", async () => {
    seedStore([LEAF_UNBOUND]);

    await useAtlas.getState().loadLeafWall(LEAF_UNBOUND.id);

    expect(fetchMock).not.toHaveBeenCalled();
    expect(useAtlas.getState().wallByLeaf[LEAF_UNBOUND.id]).toBeNull();
  });

  test("server 5xx stamps null instead of throwing", async () => {
    seedStore([LEAF_BOUND]);
    fetchMock.mockResolvedValueOnce({
      ok: false,
      status: 500,
      json: async () => ({}),
    } as Response);

    await useAtlas.getState().loadLeafWall(LEAF_BOUND.id);

    expect(useAtlas.getState().wallByLeaf[LEAF_BOUND.id]).toBeNull();
  });

  test("idempotent: second call with cached value skips refetch", async () => {
    seedStore([LEAF_BOUND]);
    fetchMock.mockResolvedValueOnce({
      ok: true,
      status: 200,
      json: async () => ({ wall: SAMPLE_WALL }),
    } as Response);

    await useAtlas.getState().loadLeafWall(LEAF_BOUND.id);
    await useAtlas.getState().loadLeafWall(LEAF_BOUND.id);

    expect(fetchMock).toHaveBeenCalledTimes(1);
  });
});

describe("setLeafMode", () => {
  test("writes the new mode under leafMode[leafID]", () => {
    useAtlas.setState({ leafMode: {} });

    useAtlas.getState().setLeafMode("leaf-1", "wall");

    expect(useAtlas.getState().leafMode["leaf-1"]).toBe("wall");
  });

  test("idempotent — no state churn when mode is unchanged", () => {
    useAtlas.setState({ leafMode: { "leaf-1": "wall" } });
    const before = useAtlas.getState().leafMode;

    useAtlas.getState().setLeafMode("leaf-1", "wall");

    expect(useAtlas.getState().leafMode).toBe(before);
  });

  test("toggles back to canvas", () => {
    useAtlas.setState({ leafMode: { "leaf-1": "wall" } });

    useAtlas.getState().setLeafMode("leaf-1", "canvas");

    expect(useAtlas.getState().leafMode["leaf-1"]).toBe("canvas");
  });
});
