"use client";

/**
 * Project view toolbar — breadcrumb (Product → Path → Flow), theme toggle,
 * persona dropdown, version selector. Lives at the top of the project shell.
 *
 * Per U6:
 *   - Theme toggle persists via `lib/projects/view-store.ts:useProjectView`
 *   - Persona toggle is URL hash-bound (`#persona=KYC-pending`) for deeplink
 *   - Active tab indicator is owned by the parent shell's tab strip, not here
 *
 * Design intent (mhdyousuf/resn refs): controls feel snappy + monospace-
 * accented. No fancy chrome — the toolbar is functional, not decorative.
 */

import type { Persona, Project, ProjectVersion } from "@/lib/projects/types";
import {
  useProjectView,
  type ThemeMode,
} from "@/lib/projects/view-store";

const THEME_LABELS: Record<ThemeMode, string> = {
  light: "Light",
  dark: "Dark",
  auto: "Auto",
};

const THEME_ORDER: ThemeMode[] = ["light", "dark", "auto"];

interface ProjectToolbarProps {
  project: Project;
  /** All available versions; ordered newest first. May be empty if upstream hasn't shipped this list yet. */
  versions: ProjectVersion[];
  /** Currently-active version ID (or undefined when versions list is empty). */
  activeVersionID: string | undefined;
  onVersionChange: (id: string) => void;

  /** All personas with screens for the active version. May be empty. */
  personas: Persona[];
  /** Currently-active persona name (matches the URL `#persona=` deeplink). */
  activePersonaName: string | null;
  onPersonaChange: (name: string | null) => void;

  /** Optional flow segment for the breadcrumb tail; defaults to project.Name. */
  flowName?: string;
}

export default function ProjectToolbar({
  project,
  versions,
  activeVersionID,
  onVersionChange,
  personas,
  activePersonaName,
  onPersonaChange,
  flowName,
}: ProjectToolbarProps) {
  const theme = useProjectView((s) => s.theme);
  const setTheme = useProjectView((s) => s.setTheme);

  return (
    <div
      data-anim="toolbar"
      style={{
        display: "flex",
        alignItems: "center",
        gap: 16,
        padding: "10px 16px",
        borderBottom: "1px solid var(--border)",
        background: "var(--bg-surface)",
        flexWrap: "wrap",
      }}
    >
      {/* Breadcrumb — Product → Path → Flow. Mono so it reads as IDish. */}
      <nav
        aria-label="Project breadcrumb"
        style={{
          display: "flex",
          alignItems: "center",
          gap: 6,
          fontFamily: "var(--font-mono)",
          fontSize: 12,
          color: "var(--text-3)",
          flex: 1,
          minWidth: 0,
        }}
      >
        <span style={{ color: "var(--text-1)" }}>{project.Product}</span>
        <span aria-hidden>/</span>
        <span>{project.Path}</span>
        <span aria-hidden>/</span>
        <span style={{ color: "var(--text-1)" }}>
          {flowName ?? project.Name}
        </span>
      </nav>

      {/* Theme toggle — segmented Light / Dark / Auto. */}
      <div
        role="radiogroup"
        aria-label="Theme"
        style={{
          display: "inline-flex",
          border: "1px solid var(--border)",
          borderRadius: 6,
          overflow: "hidden",
        }}
      >
        {THEME_ORDER.map((m) => {
          const active = theme === m;
          return (
            <button
              key={m}
              type="button"
              role="radio"
              aria-checked={active}
              onClick={() => setTheme(m)}
              style={{
                padding: "6px 10px",
                fontSize: 11,
                fontFamily: "var(--font-mono)",
                background: active
                  ? "color-mix(in srgb, var(--text-1) 8%, transparent)"
                  : "transparent",
                color: active ? "var(--text-1)" : "var(--text-3)",
                border: "none",
                borderLeft: m !== "light" ? "1px solid var(--border)" : "none",
                cursor: "pointer",
              }}
            >
              {THEME_LABELS[m]}
            </button>
          );
        })}
      </div>

      {/* Persona dropdown. Empty list → disabled w/ helper label. */}
      <label
        style={{
          display: "inline-flex",
          alignItems: "center",
          gap: 6,
          fontSize: 11,
          fontFamily: "var(--font-mono)",
          color: "var(--text-3)",
        }}
      >
        Persona
        <select
          aria-label="Persona"
          value={activePersonaName ?? ""}
          disabled={personas.length === 0}
          onChange={(e) =>
            onPersonaChange(e.target.value === "" ? null : e.target.value)
          }
          style={{
            padding: "5px 8px",
            fontSize: 11,
            fontFamily: "var(--font-mono)",
            background: "var(--bg)",
            color: "var(--text-1)",
            border: "1px solid var(--border)",
            borderRadius: 6,
          }}
        >
          <option value="">All</option>
          {personas.map((p) => (
            <option key={p.ID} value={p.Name}>
              {p.Name}
              {p.Status === "pending" ? " (pending)" : ""}
            </option>
          ))}
        </select>
      </label>

      {/* Version selector. */}
      <label
        style={{
          display: "inline-flex",
          alignItems: "center",
          gap: 6,
          fontSize: 11,
          fontFamily: "var(--font-mono)",
          color: "var(--text-3)",
        }}
      >
        Version
        <select
          aria-label="Version"
          value={activeVersionID ?? ""}
          disabled={versions.length === 0}
          onChange={(e) => onVersionChange(e.target.value)}
          style={{
            padding: "5px 8px",
            fontSize: 11,
            fontFamily: "var(--font-mono)",
            background: "var(--bg)",
            color: "var(--text-1)",
            border: "1px solid var(--border)",
            borderRadius: 6,
          }}
        >
          {versions.length === 0 && <option value="">v—</option>}
          {versions.map((v) => (
            <option key={v.ID} value={v.ID}>
              v{v.VersionIndex}
              {v.Status !== "view_ready" ? ` · ${v.Status}` : ""}
            </option>
          ))}
        </select>
      </label>
    </div>
  );
}
