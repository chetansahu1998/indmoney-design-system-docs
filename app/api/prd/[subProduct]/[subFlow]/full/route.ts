/**
 * GET /api/prd/{subProduct}/{subFlow}/full
 *
 * Document-view companion to /api/projects/.../prd. Calls prd.author with
 * `op=get` (which dispatches to the deep prd.get tool) and returns the
 * full PRD shape (tabs → states → typed stems → frame tags).
 *
 * Lives separately from /prd because the wall path is cheap (section.inspect
 * returns it inline) and most viewer visits sit on the Wall tab. The
 * Document tab pays the deeper read only when the user clicks across.
 *
 * Response shape (on 2xx):
 *   {data: PRDFull} — when a PRD row exists.
 *   {data: {sub_flow_id, prd: null, note}} — pre-skeleton sub_flow.
 *
 * The client `isPRDFull()` discriminates between the two.
 */

import { type NextRequest } from "next/server";

const DS_SERVICE_URL = process.env.DS_SERVICE_URL ?? "http://localhost:8080";

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
      `${DS_SERVICE_URL}/v1/mcp/invoke/prd.author`,
      {
        method: "POST",
        headers: {
          Authorization: auth,
          "Content-Type": "application/json",
          "X-Trace-ID": traceId,
        },
        body: JSON.stringify({
          op: "get",
          args: { sub_flow_slug: fullSlug },
        }),
        signal: AbortSignal.timeout(15_000),
      },
    );

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
