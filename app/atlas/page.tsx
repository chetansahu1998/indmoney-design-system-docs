/**
 * /atlas — brain-graph entry route.
 *
 * Renders the ported AtlasShell (Canvas2D brain + leaf overlay) from
 * `INDmoney Docs/`. The original force-graph-3d BrainGraph is preserved at
 * `page.tsx.legacy.bak` for reference until Phase 8 deletes it.
 *
 * Reads URL state via lib/atlas/url-state.ts and threads it into the shell
 * so deeplinks (Figma plugin → /projects/:slug → /atlas?project=…&leaf=…)
 * land users on the right leaf/frame on first paint.
 */

"use client";

import dynamic from "next/dynamic";
import { useSearchParams } from "next/navigation";
import { Suspense, useMemo } from "react";

import PageShell from "../../components/PageShell";
import { parseAtlasURL } from "../../lib/atlas/url-state";

import "./_styles/atlas.css";
import "./_styles/leafcanvas.css";

const AtlasShell = dynamic(() => import("./_lib/AtlasShell"), {
  ssr: false,
  loading: () => <div className="atlas-root atlas-root--booting" />,
});

export default function AtlasPage() {
  return (
    <PageShell withSidebar={false}>
      <Suspense fallback={<div className="atlas-root atlas-root--booting" />}>
        <AtlasInner />
      </Suspense>
    </PageShell>
  );
}

function AtlasInner() {
  const search = useSearchParams();
  const initialURL = useMemo(() => parseAtlasURL(search), [search]);
  return <AtlasShell initialURL={initialURL} />;
}
