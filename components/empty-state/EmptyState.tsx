"use client";

/**
 * EmptyState — Phase 3 U5 — primitive for every empty/loading/error/
 * progress/permission-denied state across the product.
 *
 * Phase 1's `components/projects/tabs/EmptyTab.tsx` was the baseline:
 * single-style card with title + description + action slot. EmptyState
 * supersedes it with 8 explicit variants the consumer chooses by name.
 * Each variant ships sensible default copy, icon, and accent color so the
 * caller can render a polished state with a single `<EmptyState
 * variant="welcome" />` and only override what's product-specific.
 *
 * Variants (see `EmptyStateVariant` below):
 *   welcome           — first-time-visitor "you have no projects yet" hero
 *   loading           — request in flight, spinner-style sigil
 *   audit-running     — pipeline running; renders progress UI when consumer
 *                       passes a `progress` prop, otherwise looks like
 *                       loading with a different headline
 *   error             — generic 5xx / network failure; consumer can wrap
 *                       this in `<RetryableError>` for backoff retry
 *   permission-denied — read-only banner; suggests the grant-request CTA
 *   re-export-needed  — "no canonical_tree for this screen yet" hint
 *   zero-violations   — celebratory "no findings on the active version"
 *   offline           — full-network failure (different from `error`'s
 *                       transient-API failure — this means VPN dropped)
 *
 * Animation: arrival is a 500ms stagger of icon → title → description →
 * cta via GSAP, matching Phase 3's "empty-state arrival" Animation
 * Philosophy row. Reduced-motion: items render in their final state on
 * first paint.
 *
 * Bundle: this file is the entire chunks/empty-states surface (~3.5KB
 * gz uncompressed before Tailwind). Phase 3 plan budgets ≤30KB gz for
 * the chunk; we have plenty of headroom for SVG illustrations as a
 * follow-up polish.
 */

import { useEffect, useRef, type ReactNode } from "react";
import gsap from "gsap";
import { useGSAPContext } from "@/lib/animations/hooks/useGSAPContext";
import { useReducedMotion } from "@/lib/animations/context";
import { EASE_PAGE_OPEN } from "@/lib/animations/easings";

export type EmptyStateVariant =
  | "welcome"
  | "loading"
  | "audit-running"
  | "error"
  | "permission-denied"
  | "re-export-needed"
  | "zero-violations"
  | "offline";

interface EmptyStateProps {
  variant: EmptyStateVariant;
  /** Overrides the variant's default headline. */
  title?: string;
  /** Overrides the variant's default body copy. */
  description?: string;
  /** Slot for a primary CTA (button / link). */
  action?: ReactNode;
  /** Slot for a secondary action / link, rendered below `action`. */
  secondary?: ReactNode;
  /** When variant=audit-running, render a progress bar with these counts. */
  progress?: { completed: number; total: number };
  /** Compact form for inline use (smaller padding, no minHeight). */
  compact?: boolean;
}

interface VariantConfig {
  /** Unicode sigil rendered in the icon disc. SVG illustrations are a
   *  Phase 3.5 polish — sigils ship today for bundle weight. */
  sigil: string;
  /** Accent color CSS variable. */
  accent: string;
  /** Default title. */
  defaultTitle: string;
  /** Default body copy. */
  defaultDescription: string;
  /** ARIA role hint for assistive tech. */
  role: "status" | "alert";
}

const VARIANTS: Record<EmptyStateVariant, VariantConfig> = {
  welcome: {
    sigil: "✦",
    accent: "var(--accent)",
    defaultTitle: "Welcome to Projects · Flow Atlas",
    defaultDescription:
      "Install the Figma plugin and run Projects mode in any file to ship your first flow.",
    role: "status",
  },
  loading: {
    sigil: "◐",
    accent: "var(--text-2)",
    defaultTitle: "Loading…",
    defaultDescription: "",
    role: "status",
  },
  "audit-running": {
    sigil: "◑",
    accent: "var(--info, #3b82f6)",
    defaultTitle: "Audit running…",
    defaultDescription: "Findings appear here as workers complete each screen.",
    role: "status",
  },
  error: {
    sigil: "!",
    accent: "var(--danger, #c00)",
    defaultTitle: "Something went wrong",
    defaultDescription: "Try again in a moment, or refresh if the problem persists.",
    role: "alert",
  },
  "permission-denied": {
    sigil: "🔒",
    accent: "var(--warning, #c80)",
    defaultTitle: "Read-only access",
    defaultDescription:
      "You're viewing this in read-only mode. Request edit access from the project owner.",
    role: "status",
  },
  "re-export-needed": {
    sigil: "↻",
    accent: "var(--text-2)",
    defaultTitle: "Canonical tree not yet captured",
    defaultDescription:
      "Re-export the flow from Figma to populate this screen's JSON view.",
    role: "status",
  },
  "zero-violations": {
    sigil: "✓",
    accent: "var(--success, #16a34a)",
    defaultTitle: "All clear",
    defaultDescription:
      "No violations on the active version. Re-export when a token publishes to keep this fresh.",
    role: "status",
  },
  offline: {
    sigil: "⚠",
    accent: "var(--danger, #c00)",
    defaultTitle: "Couldn't reach the backend",
    defaultDescription:
      "Check your VPN and click Retry. The page will reconnect automatically when the network recovers.",
    role: "alert",
  },
};

export default function EmptyState({
  variant,
  title,
  description,
  action,
  secondary,
  progress,
  compact = false,
}: EmptyStateProps) {
  const cfg = VARIANTS[variant];
  const rootRef = useRef<HTMLDivElement>(null);
  const ctx = useGSAPContext(rootRef);
  const reduced = useReducedMotion();

  // Phase 3 U5 arrival animation: 500ms stagger of icon → title → description
  // → action. Skips under reduced-motion.
  useEffect(() => {
    if (!ctx || reduced) return;
    ctx.add(() => {
      const targets = rootRef.current?.querySelectorAll<HTMLElement>(
        "[data-empty-stagger]",
      );
      if (!targets || targets.length === 0) return;
      gsap.from(targets, {
        opacity: 0,
        y: 6,
        duration: 0.32,
        // U13 — was inline "expo.out"; aliased via canonical EASE_PAGE_OPEN
        // so the empty-state stagger matches projectShellOpen +
        // atlasBloomBuildUp + ViolationsTab row reveal.
        ease: EASE_PAGE_OPEN,
        stagger: 0.08,
      });
    });
  }, [ctx, reduced]);

  return (
    <div
      ref={rootRef}
      role={cfg.role}
      aria-live={cfg.role === "alert" ? "assertive" : "polite"}
      data-empty-state-variant={variant}
      style={{
        display: "flex",
        flexDirection: "column",
        alignItems: "center",
        justifyContent: "center",
        gap: 16,
        padding: compact ? "20px 16px" : "48px 32px",
        minHeight: compact ? undefined : 240,
        background: "var(--bg-surface)",
        border: "1px solid var(--border)",
        borderRadius: 12,
      }}
    >
      <div
        data-empty-stagger
        aria-hidden
        style={{
          width: 40,
          height: 40,
          borderRadius: 999,
          display: "grid",
          placeItems: "center",
          background: `color-mix(in srgb, ${cfg.accent} 12%, transparent)`,
          color: cfg.accent,
          fontSize: 20,
          fontFamily: "var(--font-mono)",
          ...(variant === "loading" || variant === "audit-running"
            ? { animation: "empty-state-spin 1.6s linear infinite" }
            : {}),
        }}
      >
        {cfg.sigil}
      </div>

      <div
        data-empty-stagger
        style={{
          fontSize: 14,
          fontWeight: 600,
          color: "var(--text-1)",
          textAlign: "center",
        }}
      >
        {title ?? cfg.defaultTitle}
      </div>

      {(description ?? cfg.defaultDescription) && (
        <div
          data-empty-stagger
          style={{
            fontSize: 12,
            color: "var(--text-3)",
            maxWidth: 420,
            textAlign: "center",
            lineHeight: 1.5,
            fontFamily: "var(--font-mono)",
          }}
        >
          {description ?? cfg.defaultDescription}
        </div>
      )}

      {variant === "audit-running" && progress ? (
        <div data-empty-stagger style={progressContainerStyle}>
          <div
            style={{
              ...progressBarStyle,
              width:
                progress.total > 0
                  ? `${Math.round((progress.completed / progress.total) * 100)}%`
                  : "0%",
              background: cfg.accent,
            }}
          />
          <div style={progressLabelStyle}>
            {progress.completed} / {progress.total} screens checked
          </div>
        </div>
      ) : null}

      {action ? <div data-empty-stagger>{action}</div> : null}
      {secondary ? <div data-empty-stagger>{secondary}</div> : null}

      {/* Spinner keyframes inline so the chunk doesn't rely on a global
          stylesheet entry. Reduced-motion overrides it via the global
          `@media (prefers-reduced-motion: reduce)` query in styles/. */}
      <style>{`
        @keyframes empty-state-spin {
          from { transform: rotate(0deg); }
          to { transform: rotate(360deg); }
        }
        @media (prefers-reduced-motion: reduce) {
          [data-empty-state-variant="loading"] [data-empty-stagger]:first-child,
          [data-empty-state-variant="audit-running"] [data-empty-stagger]:first-child {
            animation: none !important;
          }
        }
      `}</style>
    </div>
  );
}

const progressContainerStyle: React.CSSProperties = {
  display: "flex",
  flexDirection: "column",
  alignItems: "stretch",
  gap: 4,
  width: "min(360px, 100%)",
};
const progressBarStyle: React.CSSProperties = {
  height: 4,
  borderRadius: 999,
  transition: "width 200ms ease-out",
};
const progressLabelStyle: React.CSSProperties = {
  fontSize: 11,
  color: "var(--text-3)",
  fontFamily: "var(--font-mono)",
  textAlign: "right",
  fontVariantNumeric: "tabular-nums",
};
