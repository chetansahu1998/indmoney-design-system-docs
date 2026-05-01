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
  const traceId = req.headers.get("x-trace-id") ?? crypto.randomUUID();

  let body: unknown;
  try {
    body = await req.json();
  } catch (err) {
    return Response.json(
      { ok: false, error: "invalid_json", detail: (err as Error).message, traceId },
      { status: 400 },
    );
  }

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
// (figma.com sandbox) so the browser sends OPTIONS before the POST.
export async function OPTIONS(req: NextRequest) {
  const origin = req.headers.get("origin") ?? "";
  return new Response(null, {
    status: 204,
    headers: {
      "Access-Control-Allow-Origin": origin || "*",
      "Access-Control-Allow-Methods": "POST, OPTIONS",
      "Access-Control-Allow-Headers": "Authorization, Content-Type, X-Trace-ID",
      "Access-Control-Max-Age": "600",
    },
  });
}
