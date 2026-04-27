import FilesShell from "@/components/files/FilesShell";
import FilesIndex from "@/components/files/FilesIndex";

/**
 * /files — landing page listing every audited Figma file as a card.
 * Sidebar nav lists each file as a quick jump; main pane is the index.
 */
export default function FilesPage() {
  // Index uses a flat sidebar (one entry per file). The `nav` is built
  // from the audit index at build time so it auto-updates as designers
  // run the plugin.
  return (
    <FilesShell nav={[]} title="Files" sectionIds={[]}>
      <FilesIndex />
    </FilesShell>
  );
}
