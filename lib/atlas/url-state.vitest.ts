/**
 * url-state.vitest.ts — covers the AtlasURLState round-trip, with focus on
 * the new `subFlow` field added by plan 005 U1. Pure functions, no DOM.
 */

import { describe, expect, test } from "vitest";

import {
  buildAtlasURL,
  DEFAULT_ATLAS_URL_STATE,
  parseAtlasURL,
  shouldReplaceHistory,
  type AtlasURLState,
} from "./url-state";

describe("parseAtlasURL — subFlow field", () => {
  test("?subFlow=wallet/m2m-settlement parses verbatim", () => {
    const params = new URLSearchParams("subFlow=wallet/m2m-settlement");
    const state = parseAtlasURL(params);
    expect(state.subFlow).toBe("wallet/m2m-settlement");
  });

  test("missing subFlow param defaults to null", () => {
    const params = new URLSearchParams("project=plutus&leaf=abc");
    const state = parseAtlasURL(params);
    expect(state.subFlow).toBeNull();
  });

  test("empty subFlow param coerces to null", () => {
    const params = new URLSearchParams("subFlow=");
    const state = parseAtlasURL(params);
    expect(state.subFlow).toBeNull();
  });

  test("null params returns default state (subFlow=null)", () => {
    const state = parseAtlasURL(null);
    expect(state.subFlow).toBeNull();
    expect(state).toEqual(DEFAULT_ATLAS_URL_STATE);
  });

  test("subFlow coexists with other params", () => {
    const params = new URLSearchParams(
      "project=plutus&leaf=abc&subFlow=wallet/m2m-settlement&platform=web",
    );
    const state = parseAtlasURL(params);
    expect(state).toMatchObject({
      platform: "web",
      project: "plutus",
      leaf: "abc",
      subFlow: "wallet/m2m-settlement",
    });
  });
});

describe("buildAtlasURL — subFlow field", () => {
  test("subFlow round-trips through build → parse", () => {
    const initial: AtlasURLState = {
      ...DEFAULT_ATLAS_URL_STATE,
      subFlow: "wallet/m2m-settlement",
    };
    const url = buildAtlasURL(initial);
    expect(url).toContain("subFlow=wallet%2Fm2m-settlement");
    const params = new URLSearchParams(url.split("?")[1] ?? "");
    const parsed = parseAtlasURL(params);
    expect(parsed.subFlow).toBe("wallet/m2m-settlement");
  });

  test("null subFlow is omitted from URL", () => {
    const state: AtlasURLState = { ...DEFAULT_ATLAS_URL_STATE };
    const url = buildAtlasURL(state);
    expect(url).not.toContain("subFlow=");
  });

  test("subFlow + project + leaf round-trip preserves all fields", () => {
    const start: AtlasURLState = {
      ...DEFAULT_ATLAS_URL_STATE,
      project: "plutus",
      leaf: "leaf-uuid",
      subFlow: "wallet/m2m-settlement",
    };
    const url = buildAtlasURL(start);
    const qs = url.split("?")[1] ?? "";
    const parsed = parseAtlasURL(new URLSearchParams(qs));
    expect(parsed.project).toBe("plutus");
    expect(parsed.leaf).toBe("leaf-uuid");
    expect(parsed.subFlow).toBe("wallet/m2m-settlement");
  });
});

describe("shouldReplaceHistory — subFlow drives push", () => {
  test("changing subFlow triggers history push (not replace)", () => {
    const prev: AtlasURLState = { ...DEFAULT_ATLAS_URL_STATE };
    const next: AtlasURLState = {
      ...DEFAULT_ATLAS_URL_STATE,
      subFlow: "wallet/m2m-settlement",
    };
    expect(shouldReplaceHistory(prev, next)).toBe(false);
  });

  test("identical subFlow with only frame change is replace", () => {
    const prev: AtlasURLState = {
      ...DEFAULT_ATLAS_URL_STATE,
      subFlow: "wallet/m2m-settlement",
      frame: "old-frame",
    };
    const next: AtlasURLState = {
      ...DEFAULT_ATLAS_URL_STATE,
      subFlow: "wallet/m2m-settlement",
      frame: "new-frame",
    };
    expect(shouldReplaceHistory(prev, next)).toBe(true);
  });
});
