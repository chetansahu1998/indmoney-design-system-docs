/**
 * Phase 7.8 — Figma plugin → ds-service proxy.
 *
 * The plugin's manifest networkAccess.allowedDomains lists the Vercel
 * docs origin but NOT ds-service's host (which is the ephemeral
 * trycloudflare URL today). So the plugin POSTs the export payload
 * here, and this route forwards it to ds-service over the same
 * server-side env (DS_SERVICE_URL) the rest of the proxy routes use.
 *
 * Auth: Bearer token from the plugin (the user's docs-site JWT) is
 * passed through verbatim. ds-service validates and returns the
 * ExportResponse {project_id, version_id, deeplink, trace_id}.
 *
 * No business logic lives here — this is purely a transport hop. If
 * we wanted to add per-plugin rate-limiting or audit beyond what
 * ds-service does, here's where it would land.
 */

import { type NextRequest } from "next/server";

const DS_SERVICE_URL = process.env.DS_SERVICE_URL ?? "http://localhost:8080";

export async function POST(req: NextRequest) {
  const auth = req.headers.get("authorization");
  if (!auth?.startsWith("Bearer ")) {
    return Response.json(
      { ok: false, error: "unauth", detail: "missing Bearer token" },
      { status: 401 },
    );
  }
  let body: Record<string, unknown> = {};
  try {
    body = (await req.json()) as Record<string, unknown>;
  } catch (err) {
    return Response.json(
      { ok: false, error: "invalid_json", detail: (err as Error).message },
      { status: 400 },
    );
  }
  // Trace ID can come either from the X-Trace-ID header (curl, future
  // clients) or from the body (Figma plugin — see code.ts comment about
  // why custom headers were dropped).
  const traceId =
    req.headers.get("x-trace-id") ??
    (typeof body.trace_id === "string" ? body.trace_id : null) ??
    crypto.randomUUID();

  try {
    const upstream = await fetch(`${DS_SERVICE_URL}/v1/projects/export`, {
      method: "POST",
      headers: {
        Authorization: auth,
        "Content-Type": "application/json",
        "X-Trace-ID": traceId,
      },
      body: JSON.stringify(body),
      // Plugin export pipeline + audit can take ~30s on a 50-frame
      // flow; give the upstream room before we 504.
      signal: AbortSignal.timeout(60_000),
    });
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
        ok: false,
        error: "upstream_unreachable",
        detail: (err as Error).message,
        traceId,
      },
      { status: 502 },
    );
  }
}

// CORS preflight — the Figma plugin runs in a different origin
// (figma.com sandbox, Origin: null) so the browser sends OPTIONS
// before the POST. We deliberately echo the requesting Origin and
// keep the Allow-Headers list as small as possible, since some
// sandboxed-fetch implementations (notably Figma desktop) treat any
// Allow-Headers mismatch as a generic "Failed to fetch."
export async function OPTIONS(req: NextRequest) {
  const origin = req.headers.get("origin") ?? "";
  // Reflect what the client asked for — covers Authorization,
  // Content-Type, and any future header we add without us having to
  // chase the allow-list each time.
  const reqHeaders = req.headers.get("access-control-request-headers")
    ?? "Authorization, Content-Type";
  return new Response(null, {
    status: 204,
    headers: {
      "Access-Control-Allow-Origin": origin || "*",
      "Access-Control-Allow-Methods": "POST, OPTIONS",
      "Access-Control-Allow-Headers": reqHeaders,
      "Access-Control-Max-Age": "600",
      "Vary": "Origin, Access-Control-Request-Headers",
    },
  });
}
