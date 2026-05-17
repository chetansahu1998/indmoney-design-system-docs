/**
 * POST /api/figma/frame-png-token
 *
 * Mints a short-lived asset token for the new /v1/figma/frame-png endpoint
 * (U1 of plan 2026-05-17-004). Called by the PRD viewer on mount; the
 * returned URL is what <FrameThumbnail> attaches to <img src=> for inline
 * thumbnail rendering.
 *
 * Request body: { file_key, node_id, scale }
 * Response:     { url, expires_in }
 *
 * Forwards the user's Bearer token to ds-service — ds-service enforces
 * tenant scoping and bakes the resolved tenant_id into the asset token's
 * MAC.
 */

import { type NextRequest } from "next/server";

const DS_SERVICE_URL = process.env.DS_SERVICE_URL ?? "http://localhost:8080";

export async function POST(req: NextRequest) {
  const auth = req.headers.get("authorization");
  if (!auth || !auth.startsWith("Bearer ")) {
    return Response.json(
      { error: "unauthorized", detail: "missing Bearer token" },
      { status: 401 },
    );
  }
  const traceId = req.headers.get("x-trace-id") ?? crypto.randomUUID();

  let body: unknown;
  try {
    body = await req.json();
  } catch (err) {
    return Response.json(
      {
        error: "invalid_body",
        detail: err instanceof Error ? err.message : String(err),
      },
      { status: 400 },
    );
  }

  try {
    const upstream = await fetch(`${DS_SERVICE_URL}/v1/figma/frame-png-token`, {
      method: "POST",
      headers: {
        Authorization: auth,
        Accept: "application/json",
        "Content-Type": "application/json",
        "X-Trace-ID": traceId,
      },
      body: JSON.stringify(body),
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
        error: "service_unreachable",
        detail: err instanceof Error ? err.message : String(err),
        traceId,
      },
      { status: 502 },
    );
  }
}
