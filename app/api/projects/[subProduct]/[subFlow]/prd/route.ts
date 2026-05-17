/**
 * GET /api/projects/{subProduct}/{subFlow}/prd
 *
 * U9 viewer endpoint — thin proxy onto ds-service's MCP `section.inspect`.
 * Returns the sub_flow metadata + DRD/PRD existence summary + the frames
 * array + the U6b coverage wall. The Next.js PRDShell uses this for its
 * Wall tab and CanvasShell renderer pick.
 *
 * Architecture mirrors app/api/resolve/[...slug]/route.ts:
 *   - Verify a Bearer token is present (don't decode it — ds-service
 *     enforces tenant scoping + JWT verification on every /v1/mcp/invoke
 *     call).
 *   - POST `{ sub_flow_slug: "{subProduct}/{subFlow}" }` to
 *     /v1/mcp/invoke/section.inspect.
 *   - The ds-service Result envelope is `{data, next_actions, schema_hint}`.
 *     We unwrap `data` so the client doesn't have to peek through the
 *     wrapper for every field.
 *   - Pass through upstream status. 4xx envelopes from ds-service are
 *     already `{error, detail}`-shaped; we wrap 5xx network failures in
 *     our own envelope so the page can render a clean error state.
 *
 * Why this route exists alongside /api/resolve:
 *   /api/resolve returns the universal cross-system shape (frames + states
 *   + Mixpanel events). This route returns the viewer-specific shape
 *   (wall counts + binding status + per-stem counts + lifecycle). Different
 *   read paths because they serve different surfaces — the viewer needs
 *   the wall rollup; the resolver needs the cross-system fan-out.
 */

import { type NextRequest } from "next/server";

const DS_SERVICE_URL = process.env.DS_SERVICE_URL ?? "http://localhost:8080";

// Same segment-shape guard /api/resolve enforces. Cheap defence-in-depth
// against typos and adversarial inputs — ds-service rejects them too, but
// returning a 400 here saves a round-trip and gives a clearer error.
const SEGMENT_RE = /^[a-z0-9][a-z0-9-]*$/;
const MAX_SEGMENT_LEN = 80;

type RouteParams = {
  params: Promise<{ subProduct: string; subFlow: string }>;
};

export async function GET(req: NextRequest, { params }: RouteParams) {
  const { subProduct, subFlow } = await params;

  for (const [name, seg] of Object.entries({ subProduct, subFlow })) {
    if (
      typeof seg !== "string" ||
      seg.length === 0 ||
      seg.length > MAX_SEGMENT_LEN ||
      !SEGMENT_RE.test(seg)
    ) {
      return Response.json(
        {
          error: "invalid_slug",
          detail: `${name} ${JSON.stringify(seg)} must match ^[a-z0-9][a-z0-9-]*$ and be ≤ ${MAX_SEGMENT_LEN} chars`,
        },
        { status: 400 },
      );
    }
  }

  const fullSlug = `${subProduct}/${subFlow}`;

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
      `${DS_SERVICE_URL}/v1/mcp/invoke/section.inspect`,
      {
        method: "POST",
        headers: {
          Authorization: auth,
          "Content-Type": "application/json",
          "X-Trace-ID": traceId,
        },
        body: JSON.stringify({ sub_flow_slug: fullSlug }),
        // section.inspect does ~6 reads (sub_flow, lifecycle, DRD, PRD,
        // frames, wall). Single-digit ms in steady state; 15s is generous.
        signal: AbortSignal.timeout(15_000),
      },
    );

    // On success, unwrap the MCP `data` field so the client gets the
    // SectionInspect shape directly. On error, pass through the upstream
    // body verbatim (already `{error, detail}`-shaped).
    if (!upstream.ok) {
      const text = await upstream.text();
      return new Response(text, {
        status: upstream.status,
        headers: {
          "Content-Type":
            upstream.headers.get("content-type") ?? "application/json",
          "X-Trace-ID": traceId,
        },
      });
    }

    const envelope = (await upstream.json()) as { data?: unknown };
    return Response.json(envelope.data ?? null, {
      status: 200,
      headers: {
        // Short private cache so a fresh write surfaces quickly. The viewer
        // also auto-refreshes on SSE so this is just a debounce.
        "Cache-Control": "private, max-age=5",
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
