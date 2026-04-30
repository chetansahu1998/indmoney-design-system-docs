import { headers } from "next/headers";
import { notFound } from "next/navigation";
import FileDetail from "@/components/files/FileDetail";
import { loadFileAudit, listAuditedSlugs } from "@/lib/audit/files";
import type { AuditResult } from "@/lib/audit/types";

/**
 * /files/<slug> — per-file detail page.
 *
 * Phase 2 U10: read-path cutover from build-time JSON sidecar import to
 * runtime SQLite query via the new GET /api/audit/by-slug/:slug proxy.
 *
 * Rollback flag: when `READ_FROM_SIDECAR=1` is set in the environment,
 * this page falls back to the build-time `loadFileAudit` (which reads
 * `lib/audit/<slug>.json` from disk). This preserves a one-release rollback
 * window per the Phase 2 plan; the flag is removed in Phase 3 cleanup.
 *
 * Auth model caveat: ds-service auth is JWT-in-localStorage (Phase 1) and
 * RSCs cannot read localStorage. We forward the incoming Authorization
 * header (set by middleware/proxy) when present. Sidecar-backfilled slugs
 * live under the system tenant — the handler resolves them without a tenant
 * match when the IncludeSystem flag is on, so the docs site keeps rendering
 * even before per-user cookie auth lands.
 */

const READ_FROM_SIDECAR = process.env.READ_FROM_SIDECAR === "1";

export async function generateStaticParams() {
  // Static params are still derived from the on-disk sidecar list. After
  // Phase 2 ships and the sidecar generator is fully retired (U7), this
  // becomes a runtime list query against the projects table — handled in
  // the Phase 3 cleanup unit.
  const slugs = await listAuditedSlugs();
  return slugs.map((slug) => ({ slug }));
}

export default async function FilePage({
  params,
}: {
  params: Promise<{ slug: string }>;
}) {
  const { slug } = await params;

  let result: AuditResult | null = null;

  if (READ_FROM_SIDECAR) {
    // Rollback path — keep the original build-time JSON read.
    result = await loadFileAudit(slug);
  } else {
    result = await fetchAuditBySlug(slug);
    if (!result) {
      // Defense in depth: if the SQLite read path failed for any reason
      // (network, dev environment without ds-service running) we degrade to
      // the on-disk sidecar so the page still renders. Production logs
      // surface this via the proxy route's structured error envelope.
      result = await loadFileAudit(slug);
    }
  }

  if (!result) notFound();
  return <FileDetail result={result} />;
}

async function fetchAuditBySlug(slug: string): Promise<AuditResult | null> {
  if (!/^[a-z0-9-]+$/i.test(slug)) {
    return null;
  }

  // Resolve a base URL for the in-process API route. Vercel exposes
  // VERCEL_URL; local dev defaults to PORT=3001. NEXT_PUBLIC_SITE_URL is
  // the explicit override.
  const base =
    process.env.NEXT_PUBLIC_SITE_URL ??
    (process.env.VERCEL_URL ? `https://${process.env.VERCEL_URL}` : null) ??
    `http://localhost:${process.env.PORT ?? "3001"}`;

  const incoming = await headers();
  const auth = incoming.get("authorization");
  const traceId = incoming.get("x-trace-id") ?? crypto.randomUUID();

  const upstreamHeaders: Record<string, string> = {
    "X-Trace-ID": traceId,
  };
  if (auth) {
    upstreamHeaders.Authorization = auth;
  }

  try {
    const res = await fetch(`${base}/api/audit/by-slug/${encodeURIComponent(slug)}`, {
      method: "GET",
      headers: upstreamHeaders,
      // Don't cache at the Next layer — the proxy already sets a private 30s
      // Cache-Control. RSC-level fetch cache would key on URL only and miss
      // tenant scoping.
      cache: "no-store",
    });

    if (res.status === 404) return null;
    if (!res.ok) {
      console.error("audit by-slug fetch failed", {
        slug,
        status: res.status,
        traceId,
      });
      return null;
    }
    return (await res.json()) as AuditResult;
  } catch (err) {
    console.error("audit by-slug fetch error", { slug, err, traceId });
    return null;
  }
}
