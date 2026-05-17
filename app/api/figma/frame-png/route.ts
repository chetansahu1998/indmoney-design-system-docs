/**
 * GET /api/figma/frame-png?file_key=…&node_id=…&scale=…&tenant=…&at=…
 *
 * Same-origin proxy onto ds-service's /v1/figma/frame-png (U1 of plan
 * 2026-05-17-004). The PRD viewer's <FrameThumbnail> component points
 * inline <img> tags at this URL so the browser can lazy-load PNGs without
 * a cross-origin auth dance.
 *
 * Auth model:
 *   - <img> tags can't carry an Authorization header, so this route does
 *     NOT require one. The ds-service endpoint accepts the ?at=<asset_token>
 *     query param (minted via POST /v1/figma/frame-png-token) and verifies
 *     it against (tenant_id, file_key, node_id, scale) via constant-time
 *     HMAC. A leaked log entry / browser-history line is therefore bounded
 *     to a single (tenant, file, node, scale) tuple for the token's TTL
 *     (10 minutes today).
 *   - This route preserves all four query params verbatim. ds-service
 *     owns the cache (5-min TTL) so we don't double-cache on the Next.js
 *     hop.
 *
 * Response: binary PNG body, content-type preserved, no transcoding.
 */

import { type NextRequest } from "next/server";

const DS_SERVICE_URL = process.env.DS_SERVICE_URL ?? "http://localhost:8080";

export async function GET(req: NextRequest) {
  const incoming = req.nextUrl.searchParams;
  const fileKey = incoming.get("file_key") ?? "";
  const nodeID = incoming.get("node_id") ?? "";
  const scale = incoming.get("scale") ?? "1";
  const tenant = incoming.get("tenant") ?? "";
  const at = incoming.get("at") ?? "";

  if (!fileKey || !nodeID || !at) {
    return Response.json(
      { error: "missing_params", detail: "file_key, node_id, and at are required" },
      { status: 400 },
    );
  }

  const traceId = req.headers.get("x-trace-id") ?? crypto.randomUUID();

  // Build the upstream URL — tenant is optional in ds-service (it falls
  // back to the JWT-claim path when absent), so we forward whatever was
  // supplied. Browser <img> tags reach us via the mint-issued URL which
  // always includes tenant + at.
  const qs = new URLSearchParams({
    file_key: fileKey,
    node_id: nodeID,
    scale,
    at,
  });
  if (tenant) qs.set("tenant", tenant);

  const upstreamURL = `${DS_SERVICE_URL}/v1/figma/frame-png?${qs.toString()}`;

  try {
    const upstream = await fetch(upstreamURL, {
      method: "GET",
      headers: {
        Accept: "image/png",
        "X-Trace-ID": traceId,
      },
      // Cold thumbnails involve a Figma /v1/images call + S3 download —
      // the 5-min cache absorbs the steady-state cost but the first
      // request needs headroom. 20s matches the existing figma-image-refs
      // proxy timeout.
      signal: AbortSignal.timeout(20_000),
    });

    // Surface upstream errors with their JSON bodies unchanged (they're
    // {error, detail}-shaped). Successful responses stream the bytes
    // through with the content-type and cache-control headers preserved.
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

    const buf = await upstream.arrayBuffer();
    return new Response(buf, {
      status: 200,
      headers: {
        "Content-Type":
          upstream.headers.get("content-type") ?? "image/png",
        "Cache-Control":
          upstream.headers.get("cache-control") ?? "private, max-age=300",
        "X-Content-Type-Options": "nosniff",
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
