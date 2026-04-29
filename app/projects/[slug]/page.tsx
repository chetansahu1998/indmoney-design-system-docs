/**
 * `/projects/[slug]` — server component shell.
 *
 * Phase 1 caveat: ds-service auth is JWT-in-localStorage (lib/auth-client.ts);
 * a server component cannot read it without a cookie, and Phase 1 doesn't
 * ship cookie auth. So the heavy lifting (project fetch, GSAP timelines, SSE)
 * lives in `<ProjectShellLoader>` — a client component that runs after the
 * `app/projects/layout.tsx` auth gate succeeds.
 *
 * The server component still owns the route-shape contract: it awaits the
 * dynamic params + searchParams Promises (Next 16 default) and forwards
 * stable string props to the client. This keeps the page indexed by Next's
 * router and lets later phases drop in cookie-auth without a refactor.
 */

import ProjectShellLoader from "./ProjectShellLoader";

export default async function ProjectDetailPage({
  params,
  searchParams,
}: {
  params: Promise<{ slug: string }>;
  searchParams: Promise<{ v?: string; trace?: string }>;
}) {
  const { slug } = await params;
  const { v: versionID, trace: traceID } = await searchParams;
  return (
    <ProjectShellLoader
      slug={slug}
      initialVersionID={versionID}
      initialTraceID={traceID}
    />
  );
}
