/**
 * Thin proxy from the Next.js frontend to ds-service.
 *
 * Architecture: ds-service runs locally on chetan's Mac at localhost:8080
 * (or Cloudflare Tunnel hostname in production). The Vercel-deployed frontend
 * reaches it through this Edge route, forwarding the user's Bearer token.
 *
 * NO auth logic lives here — ds-service is the single source of truth.
 */

import { type NextRequest } from "next/server";
import { z } from "zod";
import type { SyncResponse } from "@/lib/api/sync-types";
import { BRANDS } from "@/lib/brand";

const DS_SERVICE_URL = process.env.DS_SERVICE_URL ?? "http://localhost:8080";

const RequestBody = z.object({
  brand: z.enum(BRANDS),
});

function json<T>(body: T, init?: ResponseInit) {
  return Response.json(body, init);
}

export async function POST(req: NextRequest) {
  const auth = req.headers.get("authorization");
  if (!auth?.startsWith("Bearer ")) {
    return json<SyncResponse>(
      { ok: false, error: "unauth", detail: "missing Bearer token" },
      { status: 401 },
    );
  }

  const traceId = req.headers.get("x-trace-id") ?? crypto.randomUUID();

  let body: unknown = {};
  try {
    body = await req.json();
  } catch {
    // empty body OK
  }
  const parsed = RequestBody.safeParse(body);
  if (!parsed.success) {
    return json<SyncResponse>(
      { ok: false, error: "validation", detail: parsed.error.issues.map((i) => i.message).join(", "), traceId },
      { status: 422 },
    );
  }

  try {
    const upstream = await fetch(`${DS_SERVICE_URL}/v1/sync/${parsed.data.brand}`, {
      method: "POST",
      headers: {
        Authorization: auth,
        "Content-Type": "application/json",
        "X-Trace-ID": traceId,
      },
      signal: AbortSignal.timeout(60_000),
      body: JSON.stringify({}),
    });
    const upstreamBody = await upstream.json().catch(() => ({}));

    if (!upstream.ok) {
      // ds-service rejected — surface the upstream error code
      return json<SyncResponse>(
        {
          ok: false,
          error: mapUpstreamStatus(upstream.status),
          detail: upstreamBody.error ?? upstream.statusText,
          traceId,
        },
        { status: upstream.status },
      );
    }
    // Success — pass through ds-service's structured result
    return json<SyncResponse>(
      {
        ok: true,
        dispatchedAt: new Date().toISOString(),
        traceId: upstreamBody.trace_id ?? traceId,
        jobId: upstreamBody.job_id,
        status: upstreamBody.status === "ok" ? "queued" : "noop",
      },
      { status: 202 },
    );
  } catch (err) {
    return json<SyncResponse>(
      {
        ok: false,
        error: "service_unreachable",
        detail: err instanceof Error ? err.message : String(err),
        traceId,
      },
      { status: 502 },
    );
  }
}

function mapUpstreamStatus(s: number): SyncResponse extends infer T
  ? T extends { ok: false; error: infer E }
    ? E
    : never
  : never {
  switch (s) {
    case 401:
      return "unauth";
    case 403:
      return "forbidden";
    case 404:
      return "bad_brand";
    case 422:
      return "validation";
    case 429:
      return "rate_limited";
    default:
      return "dispatch_failed";
  }
}
