/**
 * Single source of truth for severity → color mapping across the app.
 *
 * Replaces three drifting inline maps (HoverSignalCard, /atlas/admin/rules,
 * DashboardShell) per `docs/runbooks/atlas-ux-principles.md` §2.6: "no
 * hardcoded severity hex outside this module."
 *
 * Future tuning is a one-file edit; downstream callers consume the constant.
 */

export type Severity = "critical" | "high" | "medium" | "low" | "info";

/** Canonical palette — chosen for legibility on the atlas dark canvas and
 *  carried through to the project Violations / Rules surfaces so the same
 *  severity reads as the same color regardless of where the user encounters
 *  it. */
export const SEVERITY_COLORS: Record<Severity, string> = {
  critical: "#FF6B6B",
  high: "#FFB347",
  medium: "#FFD93D",
  low: "#9F8FFF",
  info: "#7B9FFF",
};

/** Lookup with a sensible fallback so unknown values (e.g. legacy 'unknown'
 *  rows) don't crash the renderer. */
export function severityColor(sev: string): string {
  return SEVERITY_COLORS[sev as Severity] ?? "#64748b";
}
