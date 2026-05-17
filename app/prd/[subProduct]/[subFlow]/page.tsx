/**
 * /prd/{subProduct}/{subFlow} — DEPRECATED. Plan 005 U8.
 *
 * The PM-authoring surfaces (PRD doc, prototype canvas, coverage wall,
 * activity feed, comments) now live inside Atlas's existing right rail
 * and center pane. This route used to mount its own PRDShell — that
 * shell has been deleted; the matching client components were either
 * relocated under app/atlas/_lib/ (Wall, PrototypeCanvas, FrameThumbnail)
 * or folded into LeafInspector tabs (DocumentView → PRDTab,
 * StateCard → PRDStateCard, DRDPane → AtlasDRDEditor).
 *
 * This file stays only to keep any external link / MCP-resolved URL /
 * historical bookmark from 404-ing. It server-redirects to the Atlas
 * URL that surfaces the same sub_flow. One HTTP hop, no client bundle.
 *
 * Segment validation is preserved so a malformed slug renders the same
 * 4xx-style page instead of bouncing into Atlas with garbage params.
 */

import { redirect } from "next/navigation";

const SEGMENT_RE = /^[a-z0-9][a-z0-9-]*$/;
const MAX_SEGMENT_LEN = 80;

interface Props {
  params: Promise<{ subProduct: string; subFlow: string }>;
}

export default async function PRDRedirectPage({ params }: Props) {
  const { subProduct, subFlow } = await params;
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
  // Atlas's `?subFlow=<sp>/<sf>` URL state (plan 005 U1) is what AtlasShell
  // resolves on mount to derive the leaf binding. We don't carry the
  // `project` param here because Atlas's brain hydration already picks
  // the right leaf from the sub_flow.
  redirect(`/atlas?subFlow=${encodeURIComponent(`${subProduct}/${subFlow}`)}`);
}
