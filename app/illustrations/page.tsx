import AssetGallery from "@/components/AssetGallery";
import FilesShell from "@/components/files/FilesShell";
import { iconsByKind, iconsByCategory, slugifyCategory } from "@/lib/icons/manifest";
import type { NavGroup } from "@/components/Sidebar";

export default function IllustrationsPage() {
  const entries = iconsByKind("illustration");
  const grouped = iconsByCategory("illustration");

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
    <FilesShell nav={nav} title="Illustrations" sectionIds={sectionIds}>
      <AssetGallery
        title="Illustrations"
        subtitle="2D + 3D illustrations from Glyph. Asset names match Figma. Click to copy SVG."
        entries={entries}
        layout="square"
      />
    </FilesShell>
  );
}
