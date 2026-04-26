/**
 * Discriminated union for `/api/sync` response.
 *
 * The Next.js `/api/sync` route is a thin proxy to `ds-service`'s
 * `POST /v1/sync/:tenant`. This type is the over-the-wire contract.
 *
 * Adding a new error variant:
 *   1. Append to the `error` literal union below.
 *   2. Update server emit in `services/ds-service/internal/sync/orchestrator.go`.
 *   3. Update `components/SyncModal.tsx` to render a useful message for it.
 */

import { z } from "zod";
import { BRANDS } from "@/lib/brand";

export const SyncRequest = z.object({
  brand: z.enum(BRANDS),
});
export type SyncRequest = z.infer<typeof SyncRequest>;

export type SyncResponse =
  | {
      ok: true;
      dispatchedAt: string; // ISO-8601 UTC
      traceId: string;
      jobId: string;
      status: "queued" | "noop";
    }
  | {
      ok: false;
      error:
        | "unauth"
        | "forbidden"
        | "bad_brand"
        | "rate_limited"
        | "dispatch_failed"
        | "service_unreachable"
        | "validation";
      traceId?: string;
      detail?: string;
    };

export const SyncMeta = z.object({
  lastSyncedAt: z.string(),
  fileKey: z.string(),
  modes: z.array(z.string()),
  lastCommitSha: z.string().optional(),
  status: z.enum(["ok", "skipped_nochange", "failed"]).optional(),
});
export type SyncMeta = z.infer<typeof SyncMeta>;
