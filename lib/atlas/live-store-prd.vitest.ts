/**
 * live-store-prd.vitest.ts — covers the loadLeafPRD action shipped in
 * plan 005 U2. The action fetches /api/prd/{sp}/{sf}/full, unwraps the
 * proxy response, and stamps `prdByLeaf[leafID]` with either the typed
 * PRDFull or `null` (for empty / unbound / error cases).
 *
 * Scope:
 *   - Happy path: full PRDFull payload lands in the store.
 *   - Empty branch: server returns `{sub_flow_id, prd: null, note}` → null.
 *   - Leaf has no SubFlowSummary → null without a fetch.
 *   - Server returns 5xx → null (no thrown error).
 */

import { beforeEach, afterEach, describe, expect, test, vi } from "vitest";

import { useAtlas } from "./live-store";
import type { Leaf, PRDFull, SubFlowSummary } from "./types";

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

const SAMPLE_PRD: PRDFull = {
  id: "prd-1",
  tenant_id: "tenant-a",
  sub_flow_id: SUB_FLOW.id,
  title: "Wallet M2M PRD",
  summary_md: "Wallet PRD summary.",
  design_notes_md: "",
  created_at: "2026-05-18T00:00:00Z",
  updated_at: "2026-05-18T00:00:00Z",
  tabs: [
    {
      id: "tab-1",
      tenant_id: "tenant-a",
      prd_id: "prd-1",
      name: "Default",
      position: 0,
      overview_md: "",
      created_at: "2026-05-18T00:00:00Z",
      states: [],
    },
  ],
};

// ---- Test-side auth stub ------------------------------------------------
//
// loadLeafPRD calls `getToken()` from lib/auth-client. The real impl reads
// a zustand auth store; for these unit tests we just need it to return a
// non-empty string so the code path falls through to fetch.

vi.mock("../auth-client", () => ({
  getToken: () => "test-token",
  useAuth: { getState: () => ({ token: "test-token" }) },
}));

function seedStore(leaves: Leaf[]) {
  useAtlas.setState({
    leavesByFlow: { wallet: leaves },
    prdByLeaf: {},
  });
}

let fetchMock: ReturnType<typeof vi.fn>;

beforeEach(() => {
  fetchMock = vi.fn();
  // happy-dom provides global.fetch; replace per-test.
  globalThis.fetch = fetchMock as unknown as typeof fetch;
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("loadLeafPRD", () => {
  test("happy path — stores PRDFull when proxy returns full payload", async () => {
    seedStore([LEAF_BOUND]);
    fetchMock.mockResolvedValueOnce({
      ok: true,
      status: 200,
      json: async () => SAMPLE_PRD,
    } as Response);

    await useAtlas.getState().loadLeafPRD(LEAF_BOUND.id);

    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [url] = fetchMock.mock.calls[0]!;
    expect(url).toBe("/api/prd/wallet/m2m-settlement/full");

    const stored = useAtlas.getState().prdByLeaf[LEAF_BOUND.id];
    expect(stored).not.toBeNull();
    expect(stored?.id).toBe(SAMPLE_PRD.id);
    expect(stored?.tabs?.[0]?.name).toBe("Default");
  });

  test("empty branch — {sub_flow_id, prd: null} collapses to null", async () => {
    seedStore([LEAF_BOUND]);
    fetchMock.mockResolvedValueOnce({
      ok: true,
      status: 200,
      json: async () => ({
        sub_flow_id: SUB_FLOW.id,
        prd: null,
        note: "no PRD yet",
      }),
    } as Response);

    await useAtlas.getState().loadLeafPRD(LEAF_BOUND.id);

    const stored = useAtlas.getState().prdByLeaf;
    expect(LEAF_BOUND.id in stored).toBe(true);
    expect(stored[LEAF_BOUND.id]).toBeNull();
  });

  test("leaf without sub_flow short-circuits to null without fetch", async () => {
    seedStore([LEAF_UNBOUND]);

    await useAtlas.getState().loadLeafPRD(LEAF_UNBOUND.id);

    expect(fetchMock).not.toHaveBeenCalled();
    expect(useAtlas.getState().prdByLeaf[LEAF_UNBOUND.id]).toBeNull();
  });

  test("server error stamps null instead of throwing", async () => {
    seedStore([LEAF_BOUND]);
    fetchMock.mockResolvedValueOnce({
      ok: false,
      status: 502,
      json: async () => ({}),
    } as Response);

    await expect(useAtlas.getState().loadLeafPRD(LEAF_BOUND.id)).resolves.toBeUndefined();
    expect(useAtlas.getState().prdByLeaf[LEAF_BOUND.id]).toBeNull();
  });

  test("idempotent — second call with cached value skips the fetch", async () => {
    seedStore([LEAF_BOUND]);
    fetchMock.mockResolvedValueOnce({
      ok: true,
      status: 200,
      json: async () => SAMPLE_PRD,
    } as Response);

    await useAtlas.getState().loadLeafPRD(LEAF_BOUND.id);
    await useAtlas.getState().loadLeafPRD(LEAF_BOUND.id);

    expect(fetchMock).toHaveBeenCalledTimes(1);
  });
});
