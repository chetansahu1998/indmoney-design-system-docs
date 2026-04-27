/**
 * Server-only loader for per-file audit JSON.
 *
 * Reads lib/audit/<slug>.json at build time (and on each server request
 * in dev). Used by app/files/[slug]/page.tsx — a Next.js server component
 * passes the parsed data to the client `FileDetail`.
 *
 * `import "server-only"` blocks accidental inclusion in a client bundle.
 */

import "server-only";
import { promises as fs } from "node:fs";
import path from "node:path";
import type { AuditResult } from "./types";

const AUDIT_DIR = path.join(process.cwd(), "lib/audit");

export async function loadFileAudit(slug: string): Promise<AuditResult | null> {
  if (!/^[a-z0-9-]+$/i.test(slug)) {
    return null;
  }
  try {
    const bs = await fs.readFile(path.join(AUDIT_DIR, `${slug}.json`), "utf8");
    const parsed = JSON.parse(bs) as AuditResult;
    if (!parsed.schema_version) return null;
    return parsed;
  } catch {
    return null;
  }
}

export async function listAuditedSlugs(): Promise<string[]> {
  try {
    const entries = await fs.readdir(AUDIT_DIR);
    return entries
      .filter((e) => e.endsWith(".json") && e !== "index.json")
      .map((e) => e.replace(/\.json$/, ""));
  } catch {
    return [];
  }
}
