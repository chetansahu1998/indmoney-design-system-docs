import { defineConfig } from "vitest/config";
import { fileURLToPath } from "node:url";

// vitest.config.ts — runner config wired up in plan 2026-05-06-003 U7
// follow-up. Picks up *.test.ts(x) under app/, lib/, and any colocated
// __tests__/ folder. Uses happy-dom because the canvas-v2 unit tests
// live close to React but the suite as written does not actually mount
// React components — it tests singleton modules (gesture-tracker,
// leaf-zoom-signal, node-classifier). happy-dom is faster than jsdom
// for that profile.
//
// Path alias `@/*` mirrors tsconfig.json so test files can use the
// same imports as production code.
export default defineConfig({
  test: {
    // Vitest-converted tests use .vitest.ts(x) suffix.
    //
    // Pre-existing *.test.ts files in this repo follow a self-rolling
    // assertion pattern (export runAll(): void) and are not vitest-compatible
    // — they were placeholder files written before any runner was wired.
    // Converting them is a separate scope; until that lands, we explicitly
    // segregate by suffix so vitest doesn't try to load them as test suites
    // (it errors with "No test suite found"). When a *.test.ts file is
    // converted, rename it to *.vitest.ts to bring it into this glob.
    include: [
      "app/**/*.vitest.ts",
      "app/**/*.vitest.tsx",
      "lib/**/*.vitest.ts",
      "lib/**/*.vitest.tsx",
      "components/**/*.vitest.ts",
      "components/**/*.vitest.tsx",
    ],
    // Skip the entire services/ tree — that's Go.
    exclude: [
      "node_modules/**",
      ".next/**",
      "services/**",
      "test-results/**",
      "playwright-report/**",
    ],
    environment: "happy-dom",
    globals: false, // explicit imports — keeps the test files runnable under tsc --noEmit
    // Fast test files don't need long timeouts; give them 5s headroom
    // so a slow CI doesn't false-fail.
    testTimeout: 5_000,
    // Each test file runs in its own worker — module-level singletons
    // (canvasGestureTracker, leaf-zoom-signal) reset cleanly between
    // files without cross-file ordering dependencies.
    isolate: true,
  },
  resolve: {
    alias: {
      "@": fileURLToPath(new URL("./", import.meta.url)),
    },
  },
});
