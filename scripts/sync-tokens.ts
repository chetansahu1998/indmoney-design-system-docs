#!/usr/bin/env tsx
/**
 * Local sync-tokens runner.
 *
 * Invokes the Go extractor (services/ds-service/cmd/extractor) for the current
 * NEXT_PUBLIC_BRAND, writes JSON into lib/tokens/<brand>/, and prints a diff.
 *
 * Operator workflow:
 *   1. npm run sync:tokens
 *   2. git diff lib/tokens
 *   3. git add lib/tokens && git commit && git push   (Vercel auto-deploys)
 *
 * Phase 6 will replace this with a `Sync now` button calling the local
 * ds-service which wraps this same orchestration.
 */

import { spawn } from "node:child_process";
import { existsSync } from "node:fs";
import path from "node:path";

const ROOT = path.resolve(__dirname, "..");
const BRAND = process.env.NEXT_PUBLIC_BRAND ?? "indmoney";

if (!process.env.FIGMA_PAT) {
  console.error("FATAL: FIGMA_PAT not set. Copy .env.example to .env.local and fill it in.");
  process.exit(78);
}

console.log(`[sync] brand=${BRAND}`);

const extractorMain = path.join(ROOT, "services/ds-service/cmd/extractor");
if (!existsSync(extractorMain)) {
  console.error(`FATAL: extractor not found at ${extractorMain}. Phase 2 not yet built.`);
  process.exit(2);
}

const child = spawn(
  "go",
  [
    "run",
    "./services/ds-service/cmd/extractor",
    "--brand",
    BRAND,
    "--out",
    `lib/tokens/${BRAND}`,
  ],
  {
    cwd: ROOT,
    stdio: "inherit",
    env: process.env,
  },
);

child.on("close", (code) => {
  if (code !== 0) {
    console.error(`[sync] extractor exited ${code}`);
    process.exit(code ?? 1);
  }
  console.log("[sync] OK — review the diff:");
  console.log("        git diff lib/tokens/");
});
