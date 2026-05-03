import Link from "next/link";
import AssetGallery from "@/components/AssetGallery";
import FilesShell from "@/components/files/FilesShell";
import { iconsByKind, iconsByCategory, slugifyCategory } from "@/lib/icons/manifest";
import type { NavGroup } from "@/components/Sidebar";

// Audit C27: per-route metadata.
export const metadata = { title: "Logos · INDmoney DS" };

export default function LogosPage() {
  const entries = iconsByKind("logo");
  const grouped = iconsByCategory("logo");

  const cats = Array.from(grouped.entries()).sort(
    (a, b) => b[1].length - a[1].length,
  );
  // Audit C24: surface a count chip on the "Categories" header so the
  // sidebar matches the convention used elsewhere. The number is the
  // rendered category count.
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
    <FilesShell nav={nav} title="Logos" sectionIds={sectionIds}>
      <AssetGallery
        title="Logos"
        subtitle="Brand and partner logos from Glyph — banks, fintech partners, exchanges. Multi-color, kept at native fills."
        entries={entries}
        layout="square"
        searchPlaceholder="Search logos by name or slug…"
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
