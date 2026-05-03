import FilesShell from "@/components/files/FilesShell";
import FilesIndex from "@/components/files/FilesIndex";
import { auditedFiles, hasAuditData } from "@/lib/audit";
import type { NavGroup } from "@/components/Sidebar";

// Audit C27: per-route metadata.
export const metadata = { title: "Files · INDmoney DS" };

/**
 * /files — landing page listing every audited Figma file as a card.
 * The sidebar mirrors the cards: one entry per audited file plus a top
 * "All files" anchor. Section ids match the FileCard ids in FilesIndex
 * so scroll-spy lights up the correct sidebar entry as you scroll.
 *
 * When no audits exist, the sidebar reduces to the single "All files"
 * entry — the empty-state body below explains how to populate it.
 */
export default function FilesPage() {
  const files = hasAuditData() ? auditedFiles() : [];
  const sectionIds = ["all-files", ...files.map((f) => `file-${f.file_slug}`)];
  const nav: NavGroup[] = [
    {
      label: "Audited files",
      defaultOpen: true,
      sub: [
        { label: "All files", href: "#all-files" },
        ...files.map((f) => ({
          label: f.file_name,
          href: `#file-${f.file_slug}`,
        })),
      ],
    },
  ];
  return (
    <FilesShell nav={nav} title="Files" sectionIds={sectionIds}>
      <FilesIndex />
    </FilesShell>
  );
}
