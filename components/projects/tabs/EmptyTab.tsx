"use client";

/**
 * EmptyTab — thin compatibility wrapper.
 *
 * Phase 1 shipped this as the canonical empty-state primitive across all
 * tabs. Phase 3 U5 introduced the richer `<EmptyState variant="…">`
 * primitive at `components/empty-state/EmptyState.tsx`. EmptyTab now
 * delegates so existing callsites (DRDTab / ViolationsTab error fallback /
 * DecisionsTab placeholder) keep working while new code uses EmptyState
 * directly with the variant they need.
 *
 * Default variant is `loading` — matches the visual feel of the Phase 1
 * EmptyTab (subtle sigil disc) but the consumer can pass any variant.
 */

import type { ReactNode } from "react";
import EmptyState, { type EmptyStateVariant } from "@/components/empty-state/EmptyState";

interface EmptyTabProps {
  title: string;
  description?: string;
  action?: ReactNode;
  /** Phase 3 U5: variant override; defaults to `loading` for back-compat. */
  variant?: EmptyStateVariant;
}

export default function EmptyTab({
  title,
  description,
  action,
  variant = "loading",
}: EmptyTabProps) {
  return (
    <EmptyState
      variant={variant}
      title={title}
      description={description}
      action={action}
    />
  );
}
