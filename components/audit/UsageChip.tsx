"use client";

import { useState } from "react";
import { motion, AnimatePresence } from "framer-motion";
import { tokenUsage, componentUsage, hasAuditData } from "@/lib/audit";

/**
 * UsageChip — surfaces audit-derived usage signal next to a token / component
 * tile on Foundations + Components. Three visual states:
 *
 *   audited + used    "47 uses · 3 files"  — full opacity
 *   audited + zero    "0 uses"             — 50% opacity, faint border
 *   not audited       "?"                  — 50% opacity, dashed border
 *
 * The last state is critical: zero-usage means the audit ran AND the token is
 * unused; not-audited means the token couldn't be checked. Conflating them
 * would have the docs lie about what's actually in production.
 *
 * Hover popover lists the top-5 use sites (file → screen → node) with optional
 * Figma deep-links. Reuses `swatchHover` for spring; respects
 * `prefers-reduced-motion` via Framer's automatic detection.
 */

type Variant =
  | { kind: "not-audited" }
  | { kind: "zero" }
  | { kind: "used"; count: number; files: number; useSites: Array<{ file: string; screen: string; node: string }> };

interface Props {
  /** DTCG token path, e.g. "surface.surface-grey-separator-dark" */
  tokenPath?: string;
  /** Or component slug — exactly one of tokenPath / componentSlug */
  componentSlug?: string;
  /** Visual size — fits next to a swatch (sm) or alongside a card title (md) */
  size?: "sm" | "md";
  /** Optional class for layout siblings */
  className?: string;
}

export default function UsageChip({ tokenPath, componentSlug, size = "sm", className }: Props) {
  const variant = resolveVariant(tokenPath, componentSlug);
  const [hovered, setHovered] = useState(false);

  // Don't render anything if audit hasn't been wired and we're asked about a
  // generic surface. Surfaces that want explicit "not audited" pass-through
  // can opt in via tokenPath/componentSlug.
  if (!hasAuditData() && variant.kind === "not-audited" && !tokenPath && !componentSlug) {
    return null;
  }

  const styles = chipStyles(variant, size);

  return (
    <span
      className={className}
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
      style={{
        position: "relative",
        display: "inline-flex",
        alignItems: "center",
        gap: 4,
        cursor: variant.kind === "used" ? "default" : "help",
      }}
    >
      <motion.span
        whileHover={{ scale: 1.05 }}
        transition={{ type: "spring", stiffness: 300, damping: 22 }}
        style={styles.chip}
        aria-label={chipAriaLabel(variant)}
      >
        {chipBody(variant, size)}
      </motion.span>
      <AnimatePresence>
        {hovered && variant.kind === "used" && variant.useSites.length > 0 && (
          <UsePopover useSites={variant.useSites} />
        )}
        {hovered && variant.kind === "not-audited" && (
          <NotAuditedPopover />
        )}
      </AnimatePresence>
    </span>
  );
}

/* ── Variant resolution ─────────────────────────────────────────────────── */

function resolveVariant(tokenPath?: string, componentSlug?: string): Variant {
  if (tokenPath) {
    const u = tokenUsage(tokenPath);
    if (!u) return { kind: "not-audited" };
    if (u.usage_count === 0) return { kind: "zero" };
    return {
      kind: "used",
      count: u.usage_count,
      files: u.file_count,
      useSites: (u.use_sites ?? []).map((s) => ({
        file: s.file_slug,
        screen: s.screen_slug,
        node: s.node_name ?? s.node_id,
      })),
    };
  }
  if (componentSlug) {
    const u = componentUsage(componentSlug);
    if (!u) return { kind: "not-audited" };
    if (u.usage_count === 0) return { kind: "zero" };
    return { kind: "used", count: u.usage_count, files: u.file_count, useSites: [] };
  }
  return { kind: "not-audited" };
}

/* ── Visual ─────────────────────────────────────────────────────────────── */

function chipStyles(variant: Variant, size: "sm" | "md") {
  const padY = size === "sm" ? 1 : 3;
  const padX = size === "sm" ? 6 : 9;
  const fontSize = size === "sm" ? 10 : 11;

  const base = {
    display: "inline-flex",
    alignItems: "center",
    gap: 4,
    padding: `${padY}px ${padX}px`,
    borderRadius: 999,
    fontSize,
    fontFamily: "var(--font-mono)",
    fontWeight: 600,
    letterSpacing: "0.02em",
    border: "1px solid var(--border)",
    background: "var(--bg-surface)",
    color: "var(--text-3)",
    whiteSpace: "nowrap" as const,
  };

  switch (variant.kind) {
    case "used":
      return {
        chip: {
          ...base,
          color: "var(--text-2)",
          borderColor: "color-mix(in srgb, var(--accent) 25%, var(--border))",
          background: "color-mix(in srgb, var(--accent) 6%, var(--bg-surface))",
        },
      };
    case "zero":
      return {
        chip: {
          ...base,
          opacity: 0.5,
        },
      };
    case "not-audited":
      return {
        chip: {
          ...base,
          opacity: 0.5,
          borderStyle: "dashed",
        },
      };
  }
}

function chipBody(variant: Variant, _size: "sm" | "md") {
  switch (variant.kind) {
    case "used": {
      const filesPart = variant.files > 1 ? ` · ${variant.files} files` : "";
      return (
        <>
          <span style={{ color: "var(--accent)" }}>{variant.count}</span>
          <span>uses{filesPart}</span>
        </>
      );
    }
    case "zero":
      return <span>0 uses</span>;
    case "not-audited":
      return <span aria-hidden>?</span>;
  }
}

function chipAriaLabel(variant: Variant): string {
  switch (variant.kind) {
    case "used":
      return `Used ${variant.count} times across ${variant.files} files`;
    case "zero":
      return "Zero uses across audited files";
    case "not-audited":
      return "Not audited";
  }
}

/* ── Popovers ───────────────────────────────────────────────────────────── */

function UsePopover({ useSites }: { useSites: Array<{ file: string; screen: string; node: string }> }) {
  return (
    <motion.span
      role="tooltip"
      initial={{ opacity: 0, y: 4 }}
      animate={{ opacity: 1, y: 0 }}
      exit={{ opacity: 0, y: 4 }}
      transition={{ duration: 0.16, ease: [0.33, 1, 0.68, 1] }}
      style={{
        position: "absolute",
        top: "100%",
        left: 0,
        marginTop: 6,
        zIndex: 10,
        minWidth: 220,
        padding: "8px 10px",
        background: "var(--bg-surface-2)",
        border: "1px solid var(--border)",
        borderRadius: 6,
        fontFamily: "var(--font-mono)",
        fontSize: 10,
        color: "var(--text-2)",
        boxShadow: "0 8px 24px rgba(0,0,0,0.18)",
      }}
    >
      <span style={{ display: "block", marginBottom: 4, color: "var(--text-3)" }}>Top use sites</span>
      {useSites.slice(0, 5).map((s, i) => (
        <span key={`${s.file}-${s.screen}-${s.node}-${i}`} style={{ display: "block", lineHeight: 1.6 }}>
          {s.file} / {s.screen} / {s.node}
        </span>
      ))}
    </motion.span>
  );
}

function NotAuditedPopover() {
  return (
    <motion.span
      role="tooltip"
      initial={{ opacity: 0, y: 4 }}
      animate={{ opacity: 1, y: 0 }}
      exit={{ opacity: 0, y: 4 }}
      transition={{ duration: 0.16, ease: [0.33, 1, 0.68, 1] }}
      style={{
        position: "absolute",
        top: "100%",
        left: 0,
        marginTop: 6,
        zIndex: 10,
        minWidth: 220,
        padding: "8px 10px",
        background: "var(--bg-surface-2)",
        border: "1px solid var(--border)",
        borderRadius: 6,
        fontSize: 11,
        color: "var(--text-2)",
        boxShadow: "0 8px 24px rgba(0,0,0,0.18)",
        lineHeight: 1.5,
      }}
    >
      Not audited yet. Run{" "}
      <code style={{ fontFamily: "var(--font-mono)", color: "var(--accent)" }}>npm run audit</code>{" "}
      to populate.
    </motion.span>
  );
}
