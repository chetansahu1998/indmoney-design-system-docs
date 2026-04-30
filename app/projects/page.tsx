"use client";

/**
 * `/projects` — project index, grouped by Product (Plutus / Tax / Indian
 * Stocks / etc).
 *
 * Per U6 the plan asks for a server component fetching via
 * `lib/projects/client.ts:listProjects`, but the JWT lives in localStorage
 * (lib/auth-client.ts) and is unavailable to RSC. We therefore render this
 * as a client page that fetches on mount. Auth gating already happened in
 * `app/projects/layout.tsx`, so by the time we mount here a token exists.
 *
 * Visual: minimal — Phase 1 ships the data anchor, not the chrome. A real
 * sidebar treatment (FilesShell-style nav with collapsible Product groups)
 * is appropriate when the dataset crosses ~20 projects; until then a flat
 * grouped list is enough.
 */

import { useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { listProjects } from "@/lib/projects/client";
import type { Project } from "@/lib/projects/types";
import EmptyState from "@/components/empty-state/EmptyState";

type LoadState =
  | { status: "loading" }
  | { status: "ok"; projects: Project[] }
  | { status: "error"; error: string; statusCode: number };

export default function ProjectsIndexPage() {
  const [state, setState] = useState<LoadState>({ status: "loading" });

  useEffect(() => {
    let cancelled = false;
    void listProjects().then((r) => {
      if (cancelled) return;
      if (!r.ok) {
        setState({
          status: "error",
          error: r.error,
          statusCode: r.status,
        });
        return;
      }
      setState({ status: "ok", projects: r.data.projects });
    });
    return () => {
      cancelled = true;
    };
  }, []);

  // Group by product even on the loading branch so the layout doesn't pop.
  const grouped = useMemo(() => {
    if (state.status !== "ok") return [] as Array<[string, Project[]]>;
    const map = new Map<string, Project[]>();
    for (const p of state.projects) {
      const key = p.Product || "Untitled";
      const existing = map.get(key);
      if (existing) existing.push(p);
      else map.set(key, [p]);
    }
    // Stable alphabetical order for deterministic rendering.
    return Array.from(map.entries()).sort(([a], [b]) => a.localeCompare(b));
  }, [state]);

  return (
    <main
      style={{
        padding: "48px 32px 96px",
        maxWidth: 1100,
        margin: "0 auto",
        minHeight: "100vh",
      }}
    >
      <header style={{ marginBottom: 32 }}>
        <h1 style={{ fontSize: 28, marginBottom: 8 }}>Projects</h1>
        <p
          style={{
            fontSize: 13,
            color: "var(--text-3)",
            fontFamily: "var(--font-mono)",
          }}
        >
          Designer-exported flows. Open one to see its atlas + DRD + audit.
        </p>
      </header>

      {state.status === "loading" && <EmptyState variant="loading" />}

      {state.status === "error" && (
        <EmptyState
          variant="error"
          title="Couldn't load projects"
          description={`${state.error} (status ${state.statusCode || "n/a"})`}
        />
      )}

      {state.status === "ok" && state.projects.length === 0 && (
        <EmptyState
          variant="welcome"
          action={
            <Link
              href="/onboarding"
              style={{
                display: "inline-block",
                padding: "8px 16px",
                fontSize: 12,
                fontFamily: "var(--font-mono)",
                background: "var(--accent)",
                color: "var(--bg-base, #fff)",
                border: "1px solid var(--border)",
                borderRadius: 6,
                textDecoration: "none",
              }}
            >
              See the day-1 walkthrough →
            </Link>
          }
          secondary={
            <span
              style={{
                fontSize: 11,
                color: "var(--text-3)",
                fontFamily: "var(--font-mono)",
              }}
            >
              Or run the plugin in Figma to export your first flow.
            </span>
          }
        />
      )}

      {state.status === "ok" &&
        grouped.map(([product, projects]) => (
          <section key={product} style={{ marginBottom: 40 }}>
            <h2
              style={{
                fontSize: 14,
                textTransform: "uppercase",
                letterSpacing: 0.6,
                color: "var(--text-3)",
                marginBottom: 12,
                fontFamily: "var(--font-mono)",
              }}
            >
              {product}
            </h2>
            <ul
              style={{
                listStyle: "none",
                margin: 0,
                padding: 0,
                display: "grid",
                gridTemplateColumns: "repeat(auto-fill, minmax(240px, 1fr))",
                gap: 12,
              }}
            >
              {projects.map((p) => (
                <li key={p.ID}>
                  <Link
                    href={`/projects/${p.Slug}`}
                    style={{
                      display: "block",
                      padding: 16,
                      border: "1px solid var(--border)",
                      borderRadius: 10,
                      background: "var(--bg-surface)",
                      textDecoration: "none",
                      color: "inherit",
                    }}
                  >
                    <div
                      style={{
                        fontSize: 14,
                        fontWeight: 600,
                        color: "var(--text-1)",
                        marginBottom: 4,
                      }}
                    >
                      {p.Name}
                    </div>
                    <div
                      style={{
                        fontSize: 11,
                        fontFamily: "var(--font-mono)",
                        color: "var(--text-3)",
                      }}
                    >
                      {p.Path} · {p.Platform}
                    </div>
                  </Link>
                </li>
              ))}
            </ul>
          </section>
        ))}
    </main>
  );
}
