"use client";

import DataGapPreview from "@/components/ui/DataGapPreview";

/**
 * EmptyAuditState — wraps the existing DataGapPreview with audit-specific
 * copy + the canonical unlock command. Used by /files (when lib/audit/index.json
 * is the placeholder) and by per-section panels when no usage data is present.
 */
export default function EmptyAuditState({
  preview,
  scope = "site",
}: {
  preview: React.ReactNode;
  scope?: "site" | "section" | "file";
}) {
  return (
    <DataGapPreview
      diagnosis={
        <>
          The audit pipeline hasn&apos;t produced data yet for this {scope}. No file in{" "}
          <code style={{ fontFamily: "var(--font-mono)" }}>lib/audit-files.json</code>{" "}
          has been swept against the published INDmoney tokens, so usage counts +
          drift recommendations + cross-file patterns are all empty.
        </>
      }
      unlock={
        <>
          Add at least one Figma file_key to{" "}
          <code style={{ fontFamily: "var(--font-mono)" }}>lib/audit-files.json</code>{" "}
          and run the sweep. The Files tab + every Foundations / Components usage
          chip will populate on the next deploy.
        </>
      }
      command={`npm run audit`}
      preview={preview}
    />
  );
}
