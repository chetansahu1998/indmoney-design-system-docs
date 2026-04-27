import AssetGallery from "@/components/AssetGallery";
import FilesShell from "@/components/files/FilesShell";
import { iconsByKind, iconsByCategory, slugifyCategory } from "@/lib/icons/manifest";
import type { NavGroup } from "@/components/Sidebar";

export default function LogosPage() {
  const entries = iconsByKind("logo");
  const grouped = iconsByCategory("logo");

  const cats = Array.from(grouped.entries()).sort(
    (a, b) => b[1].length - a[1].length,
  );
  const nav: NavGroup[] = [
    {
      label: "Categories",
      defaultOpen: true,
      sub: cats.map(([cat]) => ({
        label: cat,
        href: `#cat-${slugifyCategory(cat)}`,
      })),
    },
  ];
  const sectionIds = cats.map(([cat]) => `cat-${slugifyCategory(cat)}`);

  return (
    <FilesShell nav={nav} title="Logos" sectionIds={sectionIds}>
      <AssetGallery
        title="Logos"
        subtitle="Brand and partner logos from Glyph — banks, fintech partners, exchanges. Multi-color, kept at native fills."
        entries={entries}
        layout="square"
      />
    </FilesShell>
  );
}
