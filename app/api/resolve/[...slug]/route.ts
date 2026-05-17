/**
 * U9b — proxy GET /api/resolve/[...slug] to ds-service.
 *
 * The slug is multi-segment ("wallet/m2m-settlement" or
 * "wallet/m2m-settlement/cold-state"), so the route uses Next's catch-all
 * `[...slug]` segment. Next decodes each segment, and we rejoin with "/"
 * to get the canonical full slug that the resolver expects.
 *
 * Architecture mirrors app/api/audit/by-slug/[slug]/route.ts:
 *   - Verify a Bearer token is present (don't decode it — ds-service
 *     enforces tenant scoping + JWT verification on every /v1/mcp/invoke
 *     call).
 *   - Forward the JWT verbatim.
 *   - POST `{ slug }` to /v1/mcp/invoke/resolve.
 *   - Pass through upstream status + body. 4xx envelopes from ds-service
 *     are already `{error, detail}`-shaped; 5xx network failures emit our
 *     own envelope so the caller renders a server-error state.
 *
 * The resolver itself is documented in docs/conventions/sub-product-slug.md
 * and the Go side lives at services/ds-service/internal/mcp/tools_resolve.go.
 */

import { type NextRequest } from "next/server";

const DS_SERVICE_URL = process.env.DS_SERVICE_URL ?? "http://localhost:8080";

// Segment-shape sanity: lowercase alphanumerics + hyphens. We accept
// underscores too because the existing slug normaliser (subFlowSlugify
// in services/ds-service/internal/projects/subflow.go) coalesces all
// non-[a-z0-9] runs to a single hyphen, so a stray underscore typed by
// a caller would never hit a real row anyway — but rejecting them at
// the edge gives the caller a clearer 400.
const SEGMENT_RE = /^[a-z0-9][a-z0-9-]*$/;

const MAX_SEGMENT_LEN = 80;

type RouteParams = { params: Promise<{ slug: string[] }> };

export async function GET(req: NextRequest, { params }: RouteParams) {
  const { slug } = await params;

  // Shape validation: 2 or 3 segments, each kebab-case ≤ 80 chars.
  if (!Array.isArray(slug) || (slug.length !== 2 && slug.length !== 3)) {
    return Response.json(
      {
        error: "invalid_slug",
        detail:
          "expected {sub_product}/{sub_flow} or {sub_product}/{sub_flow}/{state}",
      },
      { status: 400 },
    );
  }
  for (const seg of slug) {
    if (
      typeof seg !== "string" ||
      seg.length === 0 ||
      seg.length > MAX_SEGMENT_LEN ||
      !SEGMENT_RE.test(seg)
    ) {
      return Response.json(
        {
          error: "invalid_slug",
          detail: `segment ${JSON.stringify(seg)} must match ^[a-z0-9][a-z0-9-]*$ and be ≤ ${MAX_SEGMENT_LEN} chars`,
        },
        { status: 400 },
      );
    }
  }

  const fullSlug = slug.join("/");

  const auth = req.headers.get("authorization");
  if (!auth || !auth.startsWith("Bearer ")) {
    return Response.json(
      { error: "unauthorized", detail: "missing Bearer token" },
      { status: 401 },
    );
  }

  const traceId = req.headers.get("x-trace-id") ?? crypto.randomUUID();

  try {
    const upstream = await fetch(
      `${DS_SERVICE_URL}/v1/mcp/invoke/resolve`,
      {
        method: "POST",
        headers: {
          Authorization: auth,
          "Content-Type": "application/json",
          "X-Trace-ID": traceId,
        },
        body: JSON.stringify({ slug: fullSlug }),
        // Resolver does up to ~5 reads (sub_flow, sub_product, lifecycle,
        // PRD, frames). Single-digit ms in steady state; 15s is generous.
        signal: AbortSignal.timeout(15_000),
      },
    );

    // Pass through the upstream body verbatim. On success the MCP
    // envelope is `{data: ResolveResult, next_actions, schema_hint}`;
    // on error it's `{error, detail}`. The shape is part of the contract
    // — the caller (Next.js page or external system) reads either.
    const text = await upstream.text();
    return new Response(text, {
      status: upstream.status,
      headers: {
        "Content-Type":
          upstream.headers.get("content-type") ?? "application/json",
        // Short private cache so a fresh resolve after a PRD write
        // surfaces quickly. ds-service doesn't set Cache-Control on
        // /v1/mcp/invoke — keep ours conservative.
        "Cache-Control": "private, max-age=10",
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
