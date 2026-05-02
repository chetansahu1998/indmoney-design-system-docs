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
import { useReducedMotion } from "@/lib/animations/context";

/**
 * Phase 9 U5 — flattened audit state for the toolbar badge. ProjectShell
 * derives this from the view-machine's nested AuditStatus + top-level
 * pending kind so the toolbar doesn't have to know the full machine
 * shape. Four kinds:
 *   - pending: skeleton (canvas not yet visible / no audit info to show)
 *   - running: the spinner / dot indicator + completed/total tooltip
 *   - complete: the violation-count badge (final number once known)
 *   - failed: red badge with retry CTA
 */
export type ProjectToolbarAuditState =
  | { kind: "pending" }
  | { kind: "running"; completed: number; total: number }
  | { kind: "complete"; finalCount: number | undefined }
  | { kind: "failed"; error: string };

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

  /** Phase 9 U5 — audit-state discriminator. Drives the small badge
   *  rendered after the breadcrumb. Optional so consumers that don't
   *  care (e.g. tests, storybook stubs) can omit it; the badge falls
   *  back to the "pending" skeleton. */
  auditState?: ProjectToolbarAuditState;

  /** Phase 9 U5 — invoked when the user clicks the retry CTA on a
   *  failed-audit badge. Optional; when omitted the badge still
   *  renders the failure but the retry button is hidden. */
  onAuditRetry?: () => void;
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
  auditState,
  onAuditRetry,
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

      {/* Phase 9 U5 — audit-state badge. Renders next to the breadcrumb
          so the user has an at-a-glance answer to "is the audit still
          running, or did it finish?" right where they're already
          looking. The visual payload swaps with the auditState kind:
          spinner (running) → count (complete) → red retry (failed) →
          invisible (pending). */}
      <AuditStateBadge state={auditState} onRetry={onAuditRetry} />

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
 * Phase 9 U5 — audit-state badge.
 *
 * Renders one of four micro-UIs depending on the audit's state. The
 * component is intentionally small + self-contained so the four
 * branches stay readable in one place; we don't break it into
 * sub-components because each branch is ~10 lines and they share the
 * same outer chip styling.
 *
 * Reduced-motion: the running spinner is replaced with a static "•••"
 * dot indicator. Three dots is a stronger affordance than a single
 * static dot — it reads as "in progress" without movement, and
 * doesn't require a separate aria-label to communicate the same meaning
 * the spinner conveys to motion users.
 */
function AuditStateBadge({
  state,
  onRetry,
}: {
  state: ProjectToolbarAuditState | undefined;
  onRetry?: () => void;
}): React.ReactElement | null {
  const reducedMotion = useReducedMotion();

  if (!state || state.kind === "pending") {
    // Skeleton — invisible placeholder that holds layout space so the
    // toolbar doesn't shift when the badge resolves to running/complete.
    return (
      <span
        data-testid="audit-badge-pending"
        aria-hidden
        style={{
          display: "inline-block",
          width: 80,
          height: 22,
          borderRadius: 4,
          background: "transparent",
        }}
      />
    );
  }

  if (state.kind === "running") {
    const label =
      state.total > 0
        ? `Audit running — ${state.completed}/${state.total}`
        : "Audit running";
    return (
      <span
        data-testid="audit-badge-running"
        role="status"
        aria-live="polite"
        title={label}
        style={{
          display: "inline-flex",
          alignItems: "center",
          gap: 6,
          padding: "3px 8px",
          fontSize: 11,
          fontFamily: "var(--font-mono)",
          color: "var(--text-2)",
          border: "1px solid var(--border)",
          borderRadius: 4,
          background:
            "color-mix(in srgb, var(--bg) 92%, var(--text-1) 8%)",
        }}
      >
        {reducedMotion ? (
          <span
            data-testid="audit-badge-spinner-static"
            aria-hidden
            style={{
              fontFamily: "var(--font-mono)",
              fontSize: 11,
              letterSpacing: 1,
              color: "var(--text-3)",
            }}
          >
            •••
          </span>
        ) : (
          <span
            data-testid="audit-badge-spinner"
            aria-hidden
            style={{
              display: "inline-block",
              width: 10,
              height: 10,
              borderRadius: "50%",
              border: "1.5px solid var(--text-3)",
              borderTopColor: "var(--text-1)",
              animation: "audit-spinner-rotate 0.9s linear infinite",
            }}
          />
        )}
        <span>Audit running</span>
        {/* Inline keyframes — keeps the badge self-contained without
            polluting a global stylesheet. The keyframes are inert under
            reduced-motion because that branch never renders the
            spinner element above. */}
        {!reducedMotion ? (
          <style>{`@keyframes audit-spinner-rotate { to { transform: rotate(360deg); } }`}</style>
        ) : null}
      </span>
    );
  }

  if (state.kind === "complete") {
    const count = state.finalCount;
    const text =
      typeof count === "number"
        ? count === 1
          ? "1 violation"
          : `${count} violations`
        : "Audit complete";
    return (
      <span
        data-testid="audit-badge-complete"
        role="status"
        title={text}
        style={{
          display: "inline-flex",
          alignItems: "center",
          gap: 6,
          padding: "3px 8px",
          fontSize: 11,
          fontFamily: "var(--font-mono)",
          color: "var(--text-1)",
          border: "1px solid var(--border)",
          borderRadius: 4,
          background:
            "color-mix(in srgb, var(--bg) 86%, var(--success, #2a8) 14%)",
        }}
      >
        <span aria-hidden style={{ color: "var(--success, #2a8)" }}>
          ✓
        </span>
        <span>{text}</span>
      </span>
    );
  }

  // failed
  return (
    <span
      data-testid="audit-badge-failed"
      role="alert"
      title={state.error}
      style={{
        display: "inline-flex",
        alignItems: "center",
        gap: 6,
        padding: "3px 8px",
        fontSize: 11,
        fontFamily: "var(--font-mono)",
        color: "var(--text-1)",
        border: "1px solid var(--danger, #c33)",
        borderRadius: 4,
        background:
          "color-mix(in srgb, var(--bg) 80%, var(--danger, #c33) 20%)",
      }}
    >
      <span aria-hidden style={{ color: "var(--danger, #c33)" }}>
        !
      </span>
      <span>Audit failed</span>
      {onRetry ? (
        <button
          type="button"
          onClick={onRetry}
          data-testid="audit-badge-retry"
          style={{
            marginLeft: 4,
            padding: "0 6px",
            fontSize: 11,
            fontFamily: "var(--font-mono)",
            background: "transparent",
            color: "var(--danger, #c33)",
            border: "1px solid var(--danger, #c33)",
            borderRadius: 3,
            cursor: "pointer",
          }}
        >
          Retry
        </button>
      ) : null}
    </span>
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
