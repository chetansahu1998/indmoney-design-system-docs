#!/usr/bin/env tsx
/**
 * Unified Figma extraction pipeline.
 *
 * Runs every Go extractor in order and writes DTCG JSON into
 * lib/tokens/<brand>/. Each step is independent — a failure in one extractor
 * (e.g. Variables API gated behind a Pro plan) does not block the rest.
 *
 * Pipeline steps:
 *   1. colors      → base.tokens.json + semantic.tokens.json + semantic-dark.tokens.json + text-styles.tokens.json
 *   2. variables   → spacing.tokens.json (spacing/radius/padding from Figma Variables, if available)
 *   3. effects     → effects.tokens.json (drop/inner shadows from Figma EFFECT styles)
 *   4. icons       → public/icons/glyph/manifest.json + svg files (864 icons)
 *
 * Operator workflow:
 *   npm run sync:tokens
 *   git diff lib/tokens public/icons
 *   git add ... && git commit && git push
 */

import { spawn } from "node:child_process";
import path from "node:path";

const ROOT = path.resolve(__dirname, "..");
const BRAND = process.env.NEXT_PUBLIC_BRAND ?? "indmoney";

if (!process.env.FIGMA_PAT) {
  // Try loading .env.local manually — tsx by default doesn't.
  try {
    const envPath = path.join(ROOT, ".env.local");
    const fs = require("node:fs");
    if (fs.existsSync(envPath)) {
      const txt: string = fs.readFileSync(envPath, "utf8");
      for (const line of txt.split("\n")) {
        const m = line.match(/^\s*([A-Z0-9_]+)\s*=\s*"?([^"\n]+)"?\s*$/);
        if (m && !process.env[m[1]]) process.env[m[1]] = m[2];
      }
    }
  } catch {}
}

if (!process.env.FIGMA_PAT) {
  console.error("FATAL: FIGMA_PAT not set. Add it to .env.local.");
  process.exit(78);
}

interface Step {
  name: string;
  cmd: string;          // go cmd path relative to GO_MOD
  args?: string[];      // overrides default --brand <BRAND>
  optional?: boolean;   // non-zero exit warns but pipeline continues
}

// All Go cmds live in services/ds-service. Run go from there so it picks up
// go.mod; the cmds themselves resolve repo-root for outputs via internal/figma/repo.
const GO_MOD = path.join(ROOT, "services/ds-service");

const STEPS: Step[] = [
  { name: "colors + typography", cmd: "./cmd/extractor", args: ["--brand", BRAND] },
  { name: "variables (spacing/radius)", cmd: "./cmd/variables", args: ["--brand", BRAND], optional: true },
  { name: "effects (shadows)",   cmd: "./cmd/effects",   args: ["--brand", BRAND], optional: true },
  // The icons cmd doesn't take --brand; it reads FIGMA_FILE_KEY_INDMONEY_GLYPH
  // from env directly and writes to <repo>/public/icons/glyph/.
  { name: "icons",               cmd: "./cmd/icons",     args: [], optional: true },
];

function run(step: Step): Promise<number> {
  return new Promise((resolve) => {
    console.log(`\n[sync] ▶ ${step.name}`);
    const child = spawn(
      "go",
      ["run", step.cmd, ...(step.args ?? [])],
      { cwd: GO_MOD, stdio: "inherit", env: process.env },
    );
    child.on("close", (code) => resolve(code ?? 0));
  });
}

(async () => {
  console.log(`[sync] brand=${BRAND}`);
  const failures: string[] = [];

  for (const step of STEPS) {
    const code = await run(step);
    if (code !== 0) {
      const msg = `${step.name} exited ${code}`;
      if (step.optional) {
        console.warn(`[sync] ⚠ optional step failed: ${msg} — continuing`);
      } else {
        console.error(`[sync] ✕ required step failed: ${msg}`);
        failures.push(msg);
      }
    } else {
      console.log(`[sync] ✓ ${step.name}`);
    }
  }

  if (failures.length > 0) {
    console.error(`\n[sync] ${failures.length} required step(s) failed`);
    process.exit(1);
  }

  console.log("\n[sync] OK — review changes:");
  console.log("        git diff lib/tokens public/icons");
})();
