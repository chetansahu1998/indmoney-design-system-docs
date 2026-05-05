/**
 * GET /api/projects/{slug}/figma-image-refs?screen_ids=ID1,ID2,…
 *
 * U13 server-side skeleton — resolves IMAGE-fill `imageRef` values for
 * the canvas-v2 surface. The heavy lifting (token plumbing, Figma file
 * discovery, 5-min figma_proxy cache) lives in ds-service per the
 * U7-already-existing infra; this route is a thin proxy so the docs-site
 * client can hit a same-origin URL and dodge the CORS/cookie story.
 *
 * Upstream contract:
 *   GET {DS_SERVICE_URL}/v1/projects/{slug}/figma-image-refs?screen_ids=…
 *   → 200 { image_refs: { [imageRef]: url } }
 *
 * The upstream resolves via Figma's `/v1/files/{file}/images` endpoint
 * (cached 5 min on the figma_proxy ring). Anything else stays here:
 *   - Auth: forward the user's Bearer token verbatim. ds-service
 *     enforces tenant scoping.
 *   - Errors: surface upstream status + a small JSON envelope so the
 *     v2 renderer can fall back to the `imagePlaceholderStyle` path.
 *   - Caching: deliberately none on the docs-site hop; ds-service owns
 *     the cache contract so we don't double-cache stale entries.
 *
 * Strict TS: no `// @ts-nocheck`.
 */

import { type NextRequest } from "next/server";

const DS_SERVICE_URL = process.env.DS_SERVICE_URL ?? "http://localhost:8080";

type Params = { params: Promise<{ slug: string }> };

export async function GET(req: NextRequest, ctx: Params) {
  const { slug } = await ctx.params;
  if (!slug) {
    return Response.json(
      { ok: false, error: "missing_slug" },
      { status: 400 },
    );
  }

  const screenIDs = req.nextUrl.searchParams.get("screen_ids") ?? "";
  // Accept comma-separated; normalise + drop empties so we don't send
  // `screen_ids=,,`.
  const ids = screenIDs
    .split(",")
    .map((s) => s.trim())
    .filter((s) => s.length > 0);

  // Auth: forward the inbound Authorization header. ds-service
  // validates against the tenant boundary; we don't peek at the JWT.
  const auth = req.headers.get("authorization");
  if (!auth || !auth.startsWith("Bearer ")) {
    return Response.json(
      { ok: false, error: "unauth", detail: "missing Bearer token" },
      { status: 401 },
    );
  }

  const traceId = req.headers.get("x-trace-id") ?? crypto.randomUUID();

  const qs = ids.length > 0
    ? `?screen_ids=${encodeURIComponent(ids.join(","))}`
    : "";
  const upstreamURL =
    `${DS_SERVICE_URL}/v1/projects/${encodeURIComponent(slug)}/figma-image-refs${qs}`;

  try {
    const upstream = await fetch(upstreamURL, {
      method: "GET",
      headers: {
        Authorization: auth,
        Accept: "application/json",
        "X-Trace-ID": traceId,
      },
      // Figma `/v1/files/.../images` can take a few seconds when the
      // file has many image fills; the 5-min cache absorbs steady-state
      // latency but a cold call needs headroom.
      signal: AbortSignal.timeout(20_000),
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
