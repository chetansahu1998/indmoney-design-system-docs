"use client";

/**
 * Client wrapper around `<ProjectShell>` that performs the per-route fetch.
 *
 * Why a separate component: server components can't read the JWT in
 * localStorage; a client component must do the GET /v1/projects/:slug call.
 * This loader handles three states — loading, 404, error — and only mounts
 * `<ProjectShell>` once we have valid initial data.
 *
 * 401 handling: layout.tsx auth-gate prevents an unauthenticated render, so
 * a 401 here means the JWT expired mid-session. We bounce back to the same
 * login redirect.
 *
 * 404 handling: tenant isolation is enforced server-side — cross-tenant slug
 * lookups return 404 (no existence oracle). We surface the identical 404 UX
 * for missing slugs and forbidden slugs by design.
 */

import { useEffect, useState } from "react";
import { notFound, useRouter } from "next/navigation";
import ProjectShell from "@/components/projects/ProjectShell";
import { fetchProject } from "@/lib/projects/client";
import type {
  Flow,
  Persona,
  Project,
  ProjectVersion,
  Screen,
  ScreenMode,
} from "@/lib/projects/types";

interface ProjectShellLoaderProps {
  slug: string;
  initialVersionID?: string;
  initialTraceID?: string;
}

type LoadState =
  | { status: "loading" }
  | {
      status: "ok";
      project: Project;
      versions: ProjectVersion[];
      flows: Flow[];
      screens: Screen[];
      screenModes: ScreenMode[];
      personas: Persona[];
      activeVersionID?: string;
    }
  | { status: "not_found" }
  | { status: "unauthorized" }
  | { status: "error"; error: string; statusCode: number };

export default function ProjectShellLoader({
  slug,
  initialVersionID,
  initialTraceID,
}: ProjectShellLoaderProps) {
  const [state, setState] = useState<LoadState>({ status: "loading" });
  const router = useRouter();

  useEffect(() => {
    let cancelled = false;
    void fetchProject(slug, initialVersionID).then((r) => {
      if (cancelled) return;
      if (!r.ok) {
        if (r.status === 404) {
          setState({ status: "not_found" });
          return;
        }
        if (r.status === 401) {
          setState({ status: "unauthorized" });
          return;
        }
        setState({
          status: "error",
          error: r.error,
          statusCode: r.status,
        });
        return;
      }
      setState({
        status: "ok",
        project: r.data.project,
        versions: r.data.versions ?? [],
        flows: r.data.flows ?? [],
        screens: r.data.screens ?? [],
        screenModes: r.data.screen_modes ?? [],
        personas: r.data.available_personas ?? [],
        activeVersionID:
          initialVersionID ??
          r.data.versions?.[0]?.ID ??
          undefined,
      });
    });
    return () => {
      cancelled = true;
    };
  }, [slug, initialVersionID]);

  // Bounce on 401 — the JWT expired mid-session.
  useEffect(() => {
    if (state.status !== "unauthorized") return;
    router.replace(`/login?next=${encodeURIComponent(`/projects/${slug}`)}`);
  }, [state.status, router, slug]);

  // Render Next's notFound() UI for 404s — keeps the URL intact and surfaces
  // the standard 404 page instead of a custom in-shell error.
  if (state.status === "not_found") {
    notFound();
  }

  if (state.status === "loading" || state.status === "unauthorized") {
    return (
      <div
        aria-hidden
        style={{
          minHeight: "100vh",
          background: "var(--bg)",
        }}
      />
    );
  }

  if (state.status === "error") {
    return (
      <div
        role="alert"
        style={{
          padding: 32,
          maxWidth: 720,
          margin: "64px auto",
          fontFamily: "var(--font-mono)",
          fontSize: 13,
          color: "var(--text-1)",
          border: "1px solid var(--border)",
          borderRadius: 10,
          background: "var(--bg-surface)",
        }}
      >
        Couldn&apos;t load project: {state.error} (status{" "}
        {state.statusCode || "n/a"})
      </div>
    );
  }

  return (
    <ProjectShell
      slug={slug}
      initialProject={state.project}
      initialVersions={state.versions}
      initialActiveVersionID={state.activeVersionID}
      initialFlows={state.flows}
      initialScreens={state.screens}
      initialPersonas={state.personas}
      initialScreenModes={state.screenModes}
      initialTraceID={initialTraceID}
    />
  );
}
