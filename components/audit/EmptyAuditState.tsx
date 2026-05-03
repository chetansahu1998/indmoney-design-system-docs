"use client";

import DataGapPreview from "@/components/ui/DataGapPreview";

/**
 * EmptyAuditState — wraps the existing DataGapPreview with audit-specific
 * copy. Used by /files (when no audited files have landed yet) and by
 * per-section panels when no usage data is present.
 *
 * Audit C12: the previous copy hardcoded `npm run audit` while
 * `lib/audit-files.json.$description` claimed the plugin auto-registers.
 * Updated to reflect the canonical truth — files appear here once
 * designers export them via the Figma plugin's Project mode, no CLI
 * required.
 */
export default function EmptyAuditState({
  preview,
  scope = "site",
}: {
  /** Optional teaser visual; omit when there's nothing meaningful to show. */
  preview?: React.ReactNode;
  scope?: "site" | "section" | "file";
}) {
  return (
    <DataGapPreview
      diagnosis={
        <>
          No audit data has landed for this {scope} yet. Files appear here once
          designers export them via the Figma plugin&apos;s <em>Project mode</em>,
          which auto-registers each file and runs the sweep against the published
          INDmoney tokens.
        </>
      }
      unlock={
        <>
          Open the INDmoney Figma plugin, switch to <strong>Project mode</strong>,
          and run <em>Audit file</em>. The Files tab + every Foundations /
          Components usage chip will populate on the next deploy.
        </>
      }
      preview={preview}
    />
  );
}
