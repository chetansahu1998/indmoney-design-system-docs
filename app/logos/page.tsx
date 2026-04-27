import AssetGallery from "@/components/AssetGallery";
import PageShell from "@/components/PageShell";
import { iconsByKind } from "@/lib/icons/manifest";

export default function LogosPage() {
  const entries = iconsByKind("logo");
  return (
    <PageShell>
      <AssetGallery
        title="Logos"
        subtitle="Brand and partner logos from Glyph — banks, fintech partners, exchanges. Multi-color, kept at native fills."
        entries={entries}
        layout="square"
      />
    </PageShell>
  );
}
