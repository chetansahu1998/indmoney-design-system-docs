import AssetGallery from "@/components/AssetGallery";
import PageShell from "@/components/PageShell";
import { iconsByKind } from "@/lib/icons/manifest";

export default function ComponentsPage() {
  const entries = iconsByKind("component");
  return (
    <PageShell>
      <AssetGallery
        title="Components"
        subtitle="Component primitives extracted from Glyph's Atoms page — CTAs, progress bars, action bars, status bars, time pills. Visual reference only."
        entries={entries}
        layout="wide"
        emptyHint="No components found in the Atoms page."
      />
    </PageShell>
  );
}
