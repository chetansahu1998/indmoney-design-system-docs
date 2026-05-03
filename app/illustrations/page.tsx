import Link from "next/link";
import AssetGallery from "@/components/AssetGallery";
import FilesShell from "@/components/files/FilesShell";
import { iconsByKind, iconsByCategory, slugifyCategory } from "@/lib/icons/manifest";
import type { NavGroup } from "@/components/Sidebar";

// Audit C27: per-route metadata so each Sweep-2 surface has a distinct
// browser-tab title instead of inheriting the layout-level default
// "INDmoney DS · Foundations".
export const metadata = { title: "Illustrations · INDmoney DS" };

export default function IllustrationsPage() {
  const entries = iconsByKind("illustration");
  const grouped = iconsByCategory("illustration");

  const cats = Array.from(grouped.entries()).sort(
    (a, b) => b[1].length - a[1].length,
  );
  // Audit C24: surface a count chip on the "Categories" header so the
  // sidebar matches the convention used elsewhere (Components page bands,
  // Health card counts). The number is the rendered category count, which
  // is more useful here than total assets — it tells the designer how
  // wide the navigation is at a glance.
  const nav: NavGroup[] = [
    {
      label: `Categories (${cats.length})`,
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
        searchPlaceholder="Search illustrations by name or slug…"
        footerHint={
          <>
            Looking for color or spacing tokens?{" "}
            <Link href="/" style={{ color: "var(--accent)", textDecoration: "none" }}>
              See Foundations
            </Link>
            .
          </>
        }
      />
    </FilesShell>
  );
}
