/**
 * POST /api/projects/{subProduct}/{subFlow}/drd/ticket
 *
 * U3 follow-up — Next.js proxy onto ds-service's slug-keyed DRD ticket
 * endpoint. The PRD viewer's DRDPane mints a Hocuspocus ticket here, then
 * opens a Y.Doc against the resolved flow_id.
 *
 * Upstream contract:
 *   POST {DS_SERVICE_URL}/v1/projects/{sub_product_slug}/{sub_flow_slug}/drd/ticket
 *   → 200 { ticket, trace_id, flow_id, tenant_id, user_id, role, expires_in }
 *
 * The upstream resolves the sub_flow → flow_id binding (bootstrapping the
 * synthetic project → flow → flow_drd chain on first call) and mints a
 * 60s single-use ticket. We don't peek at the response — pure pass-through.
 *
 * Mirrors the pattern in app/api/projects/[slug]/figma-image-refs/route.ts:
 *   - Bearer token forwarded verbatim; ds-service enforces tenant scoping.
 *   - Status passed through.
 *   - Network failures wrapped as 502 with a traceId so the DRDPane can
 *     fall back to the "Could not load" state cleanly.
 */

import { type NextRequest } from "next/server";

const DS_SERVICE_URL = process.env.DS_SERVICE_URL ?? "http://localhost:8080";

const SEGMENT_RE = /^[a-z0-9][a-z0-9-]*$/;
const MAX_SEGMENT_LEN = 80;

type RouteParams = {
  params: Promise<{ subProduct: string; subFlow: string }>;
};

export async function POST(req: NextRequest, { params }: RouteParams) {
  const { subProduct, subFlow } = await params;

  for (const [name, seg] of Object.entries({ subProduct, subFlow })) {
    if (
      typeof seg !== "string" ||
      seg.length === 0 ||
      seg.length > MAX_SEGMENT_LEN ||
      !SEGMENT_RE.test(seg)
    ) {
      return Response.json(
        { ok: false, error: "bad_slug_segment", detail: `${name}=${seg}` },
        { status: 400 },
      );
    }
  }

  const auth = req.headers.get("authorization");
  if (!auth || !auth.startsWith("Bearer ")) {
    return Response.json(
      { ok: false, error: "unauth", detail: "missing Bearer token" },
      { status: 401 },
    );
  }

  const traceId = req.headers.get("x-trace-id") ?? crypto.randomUUID();

  const upstreamURL =
    `${DS_SERVICE_URL}/v1/projects/${encodeURIComponent(subProduct)}/${encodeURIComponent(subFlow)}/drd/ticket`;

  try {
    const upstream = await fetch(upstreamURL, {
      method: "POST",
      headers: {
        Authorization: auth,
        Accept: "application/json",
        "Content-Type": "application/json",
        "X-Trace-ID": traceId,
      },
      body: "{}",
      // Ticket mint is cheap (one DB lookup + one in-memory issue) — short
      // timeout is fine. First-time-bootstrap path may do a few inserts;
      // 10s is generous headroom.
      signal: AbortSignal.timeout(10_000),
    });
    const text = await upstream.text();
    return new Response(text, {
      status: upstream.status,
      headers: {
        "Content-Type":
          upstream.headers.get("content-type") ?? "application/json",
        "X-Trace-ID": traceId,
      },
    });
  } catch (err) {
    return Response.json(
      {
        ok: false,
        error: "upstream_unreachable",
        detail: (err as Error).message,
        traceId,
      },
      { status: 502 },
    );
  }
}
