/**
 * Tests for the U8 mode resolver. Pure functions — no DOM, no network. Run via
 * Playwright/Vitest infrastructure if available; otherwise these read as
 * documentation and the JSONTab Playwright test exercises the same paths
 * end-to-end.
 *
 * NOTE: this repo uses Playwright (not Vitest/Jest) for component tests, so
 * these are kept in `*.test.ts` form as a future-proofing stub. The Playwright
 * `tests/projects/canvas-render.spec.ts` from U6 already covers JSON tab
 * tab-switch behavior; mode resolution end-to-end is exercised there once
 * U8 wires the tab.
 *
 * Format mirrors a Vitest/Node-test contract so future migration is trivial:
 * each `_test_<name>` function is independent and asserts via thrown errors.
 */

import {
  extractBoundVariables,
  makeResolver,
  type BoundVariableRef,
} from "./resolveTreeForMode";

function _test_resolveColor_lightVsDark() {
  const lightValues = {
    "VariableID:a/1:0": { r: 1, g: 1, b: 1, a: 1 }, // white
  };
  const darkValues = {
    "VariableID:a/1:0": { r: 0, g: 0, b: 0, a: 1 }, // black
  };
  const lightResolver = makeResolver("light", [
    { label: "light", values: lightValues },
    { label: "dark", values: darkValues },
  ]);
  const darkResolver = makeResolver("dark", [
    { label: "light", values: lightValues },
    { label: "dark", values: darkValues },
  ]);

  const ref: BoundVariableRef = { id: "VariableID:a/1:0" };
  const lightResult = lightResolver.resolve(ref);
  const darkResult = darkResolver.resolve(ref);
  if (!lightResult || lightResult.kind !== "color")
    throw new Error("light resolution missing");
  if (lightResult.hex.toLowerCase() !== "#ffffff")
    throw new Error(`light hex expected #ffffff, got ${lightResult.hex}`);
  if (!darkResult || darkResult.kind !== "color")
    throw new Error("dark resolution missing");
  if (darkResult.hex.toLowerCase() !== "#000000")
    throw new Error(`dark hex expected #000000, got ${darkResult.hex}`);
}

function _test_resolveMissingVariable_returnsNull() {
  const resolver = makeResolver("light", [
    { label: "light", values: { "VariableID:a/1:0": { r: 1, g: 1, b: 1, a: 1 } } },
  ]);
  const result = resolver.resolve({ id: "VariableID:does-not-exist" });
  if (result !== null) throw new Error("expected null for missing variable");
}

function _test_resolveActiveModeNotInBindings_returnsNull() {
  const resolver = makeResolver("sepia", [
    { label: "light", values: { "VariableID:a/1:0": { r: 1, g: 1, b: 1, a: 1 } } },
  ]);
  const result = resolver.resolve({ id: "VariableID:a/1:0" });
  if (result !== null) throw new Error("active mode not in bindings should return null");
}

function _test_resolveNumber() {
  const resolver = makeResolver("light", [
    { label: "light", values: { "VariableID:a/1:0": 16 } },
  ]);
  const result = resolver.resolve({ id: "VariableID:a/1:0" });
  if (!result || result.kind !== "number" || result.value !== 16)
    throw new Error("expected number 16");
}

function _test_resolveString() {
  const resolver = makeResolver("light", [
    { label: "light", values: { "VariableID:a/1:0": "Inter" } },
  ]);
  const result = resolver.resolve({ id: "VariableID:a/1:0" });
  if (!result || result.kind !== "string" || result.value !== "Inter")
    throw new Error("expected string Inter");
}

function _test_resolverCachesPerBinding() {
  let lookups = 0;
  const handler = {
    get(target: Record<string, unknown>, prop: string) {
      lookups += 1;
      return target[prop];
    },
  };
  const proxiedValues = new Proxy({ "VariableID:a/1:0": 42 }, handler);
  const resolver = makeResolver("light", [
    { label: "light", values: proxiedValues },
  ]);
  resolver.resolve({ id: "VariableID:a/1:0" });
  resolver.resolve({ id: "VariableID:a/1:0" });
  resolver.resolve({ id: "VariableID:a/1:0" });
  if (lookups !== 1)
    throw new Error(`expected 1 lookup with cache; got ${lookups}`);
}

function _test_extractBoundVariables_singleBinding() {
  const node = {
    type: "RECTANGLE",
    boundVariables: { fills: { id: "VariableID:a/1:0" } },
  };
  const out = extractBoundVariables(node);
  if (!out || out.length !== 1) throw new Error("expected 1 binding");
  if (out[0].field !== "fills") throw new Error("field mismatch");
  if (out[0].binding.id !== "VariableID:a/1:0") throw new Error("id mismatch");
}

function _test_extractBoundVariables_arrayBinding() {
  const node = {
    type: "RECTANGLE",
    boundVariables: {
      fills: [
        { id: "VariableID:a/1:0" },
        { id: "VariableID:a/2:0" },
      ],
    },
  };
  const out = extractBoundVariables(node);
  if (!out || out.length !== 2) throw new Error("expected 2 bindings");
  if (out[0].field !== "fills[0]") throw new Error("field[0] mismatch");
  if (out[1].field !== "fills[1]") throw new Error("field[1] mismatch");
}

function _test_extractBoundVariables_emptyNode_returnsNull() {
  if (extractBoundVariables({ type: "RECTANGLE" }) !== null)
    throw new Error("no boundVariables → null expected");
  if (extractBoundVariables(null) !== null)
    throw new Error("null node → null expected");
  if (extractBoundVariables("string") !== null)
    throw new Error("primitive → null expected");
}

// ─── Test runner ─────────────────────────────────────────────────────────────
//
// Self-running on import via `process.env.NODE_ENV === "test"` would be
// invasive. Export the suite for explicit invocation.
export const _suite = {
  resolveColor_lightVsDark: _test_resolveColor_lightVsDark,
  resolveMissingVariable_returnsNull: _test_resolveMissingVariable_returnsNull,
  resolveActiveModeNotInBindings_returnsNull:
    _test_resolveActiveModeNotInBindings_returnsNull,
  resolveNumber: _test_resolveNumber,
  resolveString: _test_resolveString,
  resolverCachesPerBinding: _test_resolverCachesPerBinding,
  extractBoundVariables_singleBinding: _test_extractBoundVariables_singleBinding,
  extractBoundVariables_arrayBinding: _test_extractBoundVariables_arrayBinding,
  extractBoundVariables_emptyNode_returnsNull:
    _test_extractBoundVariables_emptyNode_returnsNull,
};

// When invoked directly (e.g. `tsx lib/projects/resolveTreeForMode.test.ts`),
// run all tests and report.
declare const require: { main: unknown } | undefined;
declare const module: { id: string } | undefined;
if (typeof require !== "undefined" && typeof module !== "undefined" && require.main === module) {
  let pass = 0;
  let fail = 0;
  for (const [name, fn] of Object.entries(_suite)) {
    try {
      fn();
      pass++;
      console.log(`✓ ${name}`);
    } catch (e) {
      fail++;
      console.error(`✗ ${name}: ${(e as Error).message}`);
    }
  }
  console.log(`\n${pass} passed, ${fail} failed`);
  if (fail > 0 && typeof process !== "undefined") {
    (process as { exit?: (code: number) => void }).exit?.(1);
  }
}
