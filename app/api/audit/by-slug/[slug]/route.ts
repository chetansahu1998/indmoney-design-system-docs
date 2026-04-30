/**
 * Phase 2 U10 — proxy GET /api/audit/by-slug/<slug> to ds-service.
 *
 * Architecture mirrors app/api/sync/route.ts: the Next.js handler verifies
 * the bearer token is present, forwards it to ds-service which is the
 * single source of truth for tenant scoping + JWT verification. We do not
 * decode the JWT here.
 *
 * Response shape: identical to lib/audit/types.ts AuditResult — the upstream
 * Go handler (services/ds-service/internal/auditbyslug) returns the same JSON
 * the build-time sidecar import produced. Keep the shape stable; the FE
 * components in app/files/[slug]/page.tsx don't change types.
 *
 * Error shape: 4xx responses pass through ds-service's `{error, detail}`
 * envelope. 5xx-class network failures emit a structured envelope so the
 * page route can render a server-error state instead of crashing.
 */

import { type NextRequest } from "next/server";

const DS_SERVICE_URL = process.env.DS_SERVICE_URL ?? "http://localhost:8080";

const SLUG_RE = /^[a-z0-9-]+$/i;

type RouteParams = { params: Promise<{ slug: string }> };

export async function GET(req: NextRequest, { params }: RouteParams) {
  const { slug } = await params;

  if (!SLUG_RE.test(slug) || slug.length > 80) {
    return Response.json(
      { error: "invalid_slug", detail: "slug must be ^[a-z0-9-]+$ and ≤ 80 chars" },
      { status: 400 },
    );
  }

  const auth = req.headers.get("authorization");
  if (!auth?.startsWith("Bearer ")) {
    return Response.json(
      { error: "unauthorized", detail: "missing Bearer token" },
      { status: 401 },
    );
  }

  const traceId = req.headers.get("x-trace-id") ?? crypto.randomUUID();

  try {
    const upstream = await fetch(
      `${DS_SERVICE_URL}/v1/audit/by-slug/${encodeURIComponent(slug)}`,
      {
        method: "GET",
        headers: {
          Authorization: auth,
          "X-Trace-ID": traceId,
        },
        signal: AbortSignal.timeout(15_000),
      },
    );

    // Pass through the upstream body verbatim — the JSON shape on success is
    // already an AuditResult; on error it's `{error, detail}`. Either way the
    // FE handles it.
    const text = await upstream.text();
    return new Response(text, {
      status: upstream.status,
      headers: {
        "Content-Type": upstream.headers.get("content-type") ?? "application/json",
        "X-Trace-ID": traceId,
      },
    });
  } catch (err) {
    return Response.json(
      {
        error: "service_unreachable",
        detail: err instanceof Error ? err.message : String(err),
        traceId,
      },
      { status: 502 },
    );
  }
}
