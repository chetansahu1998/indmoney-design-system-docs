/**
 * /projects/[slug] — Figma-plugin deeplink target.
 *
 * The plugin sends users here after a successful export. We forward to the
 * new spatial shell at /atlas, preserving every searchParam (?v, ?trace,
 * ?persona, …) so the inspector lands on the right state. The slug
 * becomes ?project=<slug>; if the URL targets a specific flow that route
 * lives entirely in /atlas's URL state machine.
 *
 * Server-side redirect — keeps the round-trip to one HTTP hop and avoids
 * a client-side flash of the old ProjectShell. The legacy implementation
 * is preserved at page.tsx.legacy.bak; Phase 8 deletes it once a release
 * with the new shell ships.
 */

import { redirect } from "next/navigation";

interface ProjectPageProps {
  params: Promise<{ slug: string }>;
  searchParams: Promise<Record<string, string | string[] | undefined>>;
}

export default async function ProjectPage(props: ProjectPageProps): Promise<never> {
  const { slug } = await props.params;
  const search = await props.searchParams;

  const qs = new URLSearchParams();
  qs.set("project", slug);
  for (const [k, v] of Object.entries(search)) {
    if (v == null) continue;
    const value = Array.isArray(v) ? v[0] : v;
    if (typeof value === "string" && value.length > 0) {
      qs.set(k, value);
    }
  }

  redirect(`/atlas?${qs.toString()}`);
}
