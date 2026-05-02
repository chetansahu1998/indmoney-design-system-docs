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
 *
 * U2c — Static `/atlas › <flow name>` breadcrumb. Always-on (NOT gated by
 * reduced-motion). For motion users it's standard wayfinding; for reduced-
 * motion + Firefox-default-flag users it serves as the *primary* spatial-
 * continuity substitute for the cross-route morph (per the corrected
 * Reduced-Motion + Unsupported Browser Strategy in the Phase plan's Key
 * Technical Decisions section). Clicking `/atlas` navigates to the atlas
 * route — no animation hook here; the reverse-morph (U3) is wired by
 * the atlas side via View Transitions if the API is available.
 */

import Link from "next/link";
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
  /** Project slug. Required for the U2b View-Transitions morph: the title
   *  bar's `view-transition-name` must match the source flow-leaf label
   *  in /atlas (`flow-${slug}-label`). */
  slug: string;
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
  slug,
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

  const resolvedFlowName = flowName ?? project.Name;

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
      {/* U2c — Static `/atlas › <flow name>` breadcrumb. Always rendered;
          provides spatial-continuity context for both motion and no-motion
          users. Inherits the toolbar's mono font via inline styles. The
          `›` separator + flow-name span are both rendered as plain text
          so flow names containing slashes (e.g. "F&O / Learn") don't
          break the breadcrumb structure — the slash is just a glyph in
          the flowName text node. */}
      <nav
        data-tour="atlas-breadcrumb"
        aria-label="Atlas breadcrumb"
        style={{
          display: "flex",
          alignItems: "center",
          gap: 6,
          fontFamily: "var(--font-mono)",
          fontSize: 12,
          color: "var(--text-3)",
          width: "100%",
          minWidth: 0,
        }}
      >
        <Link
          href="/atlas"
          data-testid="atlas-breadcrumb-link"
          style={{
            color: "var(--text-2)",
            textDecoration: "none",
          }}
        >
          /atlas
        </Link>
        <span aria-hidden>›</span>
        <span
          data-testid="atlas-breadcrumb-flow"
          style={{
            color: "var(--text-1)",
            overflow: "hidden",
            textOverflow: "ellipsis",
            whiteSpace: "nowrap",
            minWidth: 0,
          }}
        >
          {resolvedFlowName}
        </span>
      </nav>

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
        {/* U2b — View-Transitions destination. The browser-native View
            Transitions API matches this element to the source flow-leaf
            label in /atlas (which carries the same name); during the
            cross-route morph the bounding rect + opacity animate
            between them. The CSS keyframes (duration + easing) live in
            `app/atlas/view-transitions.css`. */}
        <span
          data-tour="project-title"
          style={{
            color: "var(--text-1)",
            viewTransitionName: `flow-${slug}-label`,
          }}
        >
          {resolvedFlowName}
        </span>
      </nav>

      {/* Theme toggle — segmented Light / Dark / Auto. */}
      <div
        role="radiogroup"
        aria-label="Theme"
        data-tour="theme-toggle"
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
        data-tour="persona-toggle"
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

      {/* Phase 3 U8: version selector — surfaces only when >1 versions
          exist. Single-version projects don't waste toolbar space on a
          dropdown that can't toggle. */}
      {versions.length > 1 ? (
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
            {versions.map((v) => (
              <option key={v.ID} value={v.ID}>
                v{v.VersionIndex}
                {" · "}
                {formatVersionAge(v.CreatedAt)}
                {v.Status !== "view_ready" ? ` · ${v.Status}` : ""}
              </option>
            ))}
          </select>
        </label>
      ) : null}
    </div>
  );
}

/**
 * Phase 3 U8: short-form age label for the version dropdown ("today",
 * "2d ago", "3w ago"). Keeps the option text scannable without a date
 * picker. Falls back to ISO date when parsing fails.
 */
function formatVersionAge(createdAt: string): string {
  const created = new Date(createdAt);
  if (Number.isNaN(created.getTime())) return createdAt.slice(0, 10);
  const now = Date.now();
  const diffMs = now - created.getTime();
  const day = 24 * 60 * 60 * 1000;
  if (diffMs < day) return "today";
  const days = Math.round(diffMs / day);
  if (days < 7) return `${days}d ago`;
  const weeks = Math.round(days / 7);
  if (weeks < 5) return `${weeks}w ago`;
  const months = Math.round(days / 30);
  return `${months}mo ago`;
}
