/**
 * Audit manifest loader. Reads lib/audit/index.json + per-file JSON the Go
 * audit core wrote. Build-time only — Next.js inlines the JSON via import,
 * so usage chips are static at deploy time.
 *
 * Two return paths every caller has to handle:
 *   - audit ran AND token has uses        → { count > 0, files > 0 }
 *   - audit ran AND token has zero uses   → { count: 0,  files: 0 }
 *   - audit didn't run / file not in manifest → undefined
 *
 * "undefined" vs "0" matters. Zero-usage tokens are de-emphasized in the UI;
 * not-audited tokens render a `?` chip instead. Conflating the two would
 * make the docs lie about which tokens are actually unused.
 */

import indexData from "./index.json";
import type {
  AuditIndex,
  AuditResult,
  TokenUsage,
  ComponentUsage,
  IndexEntry,
} from "./types";
import { SCHEMA_VERSION } from "./types";

const RAW = indexData as unknown as AuditIndex;

// Major-version handshake. We tolerate minor bumps but log a warning when
// the major doesn't match this checkout.
const auditMajor = (RAW.schema_version ?? "0").split(".")[0];
const expectedMajor = SCHEMA_VERSION.split(".")[0];
if (auditMajor !== expectedMajor) {
  // eslint-disable-next-line no-console
  console.warn(
    `[audit] schema mismatch — expected major ${expectedMajor}, got ${auditMajor}. Re-run npm run audit.`,
  );
}

const TOKEN_INDEX: Map<string, TokenUsage> = new Map(
  (RAW.token_usage ?? []).map((t) => [t.token_path, t]),
);
const COMPONENT_INDEX: Map<string, ComponentUsage> = new Map(
  (RAW.component_usage ?? []).map((c) => [c.slug, c]),
);

/** Returns true when a sweep has actually populated the index. */
export function hasAuditData(): boolean {
  return (RAW.files?.length ?? 0) > 0;
}

/** Returns the placeholder/real generated_at timestamp for staleness checks. */
export function generatedAt(): Date | null {
  if (!RAW.generated_at) return null;
  const t = new Date(RAW.generated_at);
  // Placeholder uses 1970-01-01 — treat as null.
  if (t.getUTCFullYear() < 2000) return null;
  return t;
}

export function isStale(thresholdMs: number = 7 * 24 * 60 * 60 * 1000): boolean {
  const t = generatedAt();
  if (!t) return false; // never audited → not "stale", just absent
  return Date.now() - t.getTime() > thresholdMs;
}

/**
 * Look up usage for a specific token. Returns undefined when the audit
 * index has no record of the token (the audit hasn't run, or the token
 * exists in tokens.json but isn't in any audited file's drift list).
 */
export function tokenUsage(tokenPath: string): TokenUsage | undefined {
  return TOKEN_INDEX.get(tokenPath);
}

export function componentUsage(slug: string): ComponentUsage | undefined {
  return COMPONENT_INDEX.get(slug);
}

/** Files index for the /files top-level page. */
export function auditedFiles(): IndexEntry[] {
  return [...(RAW.files ?? [])].sort(
    (a, b) => a.overall_coverage - b.overall_coverage,
  );
}

/** Cross-file canonical-hash patterns; promotion candidates. */
export function crossFilePatterns() {
  return RAW.cross_file_patterns ?? [];
}

/** Full token-usage list — used by /components for SECTION reorder. */
export function allTokenUsage(): TokenUsage[] {
  return RAW.token_usage ?? [];
}

export function allComponentUsage(): ComponentUsage[] {
  return RAW.component_usage ?? [];
}

/* ── Per-file detail loader (server-side only) ────────────────────────── */

/**
 * loadFileAudit reads a per-file JSON at build time.
 *
 * Note: Next.js can't dynamically `import()` a JSON file by string at build,
 * so file-detail pages explicitly import their slug's JSON. This function is
 * a typed pass-through used by those pages.
 */
export function asFileAudit(raw: unknown): AuditResult | null {
  if (!raw || typeof raw !== "object") return null;
  const r = raw as AuditResult;
  if (!r.schema_version) return null;
  return r;
}

/* ── Provenance line shared across surfaces ──────────────────────────── */

export function provenanceLine(): string {
  if (!hasAuditData()) {
    return "Audit not yet run · `npm run audit`";
  }
  const t = generatedAt();
  if (!t) return "audited (timestamp missing)";
  const ago = Math.round((Date.now() - t.getTime()) / (60 * 60 * 1000));
  if (ago < 1) return `audited just now · ${RAW.files.length} files`;
  if (ago < 24) return `audited ${ago}h ago · ${RAW.files.length} files`;
  const days = Math.floor(ago / 24);
  return `audited ${days}d ago · ${RAW.files.length} files`;
}
