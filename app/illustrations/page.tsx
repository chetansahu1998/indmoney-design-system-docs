import AssetGallery from "@/components/AssetGallery";
import PageShell from "@/components/PageShell";
import { iconsByKind } from "@/lib/icons/manifest";

export default function IllustrationsPage() {
  const entries = iconsByKind("illustration");
  return (
    <PageShell>
      <AssetGallery
        title="Illustrations"
        subtitle="2D + 3D illustrations from Glyph. Asset names match Figma. Click to copy SVG."
        entries={entries}
        layout="square"
      />
    </PageShell>
  );
}
