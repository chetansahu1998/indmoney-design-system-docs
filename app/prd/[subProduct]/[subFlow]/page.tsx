/**
 * /prd/{subProduct}/{subFlow} — PM-facing PRD viewer (U9).
 *
 * Server component, but the bulk of work happens in the client shell
 * (PRDShell.tsx). Reason: the auth token lives in zustand-persist +
 * localStorage and only resolves on the client (see app/projects/layout.tsx
 * gate). Server-side fetching would require a cookie session we don't have
 * in Phase 1. So this file is a thin route-level entrypoint that:
 *
 *   - Receives the {subProduct, subFlow} segments
 *   - Validates segment shape (lowercase-kebab) before dispatching
 *   - Renders the client shell, which mounts the API fetch + SSE
 *
 * Lives under `[subProduct]/[subFlow]/prd/` (two dynamic segments + literal)
 * to mirror the universal slug shape committed by U9b: `{sub_product}/{sub_flow}`.
 * The plan prompt called for `[slug]/prd/` but a single Next dynamic segment
 * can't host a forward slash; this two-segment layout is the canonical
 * Next.js shape for the same identifier and is consistent with /api/resolve/[...slug].
 */

import { PRDShell } from "./PRDShell";

const SEGMENT_RE = /^[a-z0-9][a-z0-9-]*$/;
const MAX_SEGMENT_LEN = 80;

interface Props {
  params: Promise<{ subProduct: string; subFlow: string }>;
}

export default async function PRDPage({ params }: Props) {
  const { subProduct, subFlow } = await params;
  // Same segment guard the API route enforces — fail fast on garbage URLs
  // without spinning up the client bundle just to render an error.
  const segOK = (s: string) =>
    typeof s === "string" &&
    s.length > 0 &&
    s.length <= MAX_SEGMENT_LEN &&
    SEGMENT_RE.test(s);
  if (!segOK(subProduct) || !segOK(subFlow)) {
    return (
      <div style={{ padding: 48, color: "var(--text-2)" }}>
        <h1 style={{ fontSize: 18, marginBottom: 8 }}>Invalid sub_flow slug</h1>
        <p style={{ fontSize: 14, opacity: 0.7 }}>
          Slug segments must be lowercase-kebab and ≤ {MAX_SEGMENT_LEN} chars.
        </p>
      </div>
    );
  }
  return <PRDShell subProduct={subProduct} subFlow={subFlow} />;
}
