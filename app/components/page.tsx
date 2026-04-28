import ComponentCanvas from "@/components/ComponentCanvas";
import FilesShell from "@/components/files/FilesShell";
import { iconsByKind, iconsByCategory, slugifyCategory } from "@/lib/icons/manifest";
import type { NavGroup } from "@/components/Sidebar";

/**
 * /components — horizontal-canvas component browser.
 *
 * The canvas owns the entire viewport below the global header. Its own
 * toolbar carries title + search + category jumps, so the page passes
 * `fullBleed` to FilesShell to drop the 1100px column constraint and
 * renders ComponentCanvas as the only child.
 *
 * Sidebar stays — it's how the docs site stitches navigation together —
 * and category sub-anchors trigger horizontal pan via in-canvas listeners
 * (the canvas reads its `data-cat` lookup from the hash).
 */
export default function ComponentsPage() {
  const entries = iconsByKind("component");
  const grouped = iconsByCategory("component");

  const cats = Array.from(grouped.entries())
    .sort((a, b) => b[1].length - a[1].length)
    .map(([cat, list]) => ({ cat, count: list.length }));

  const nav: NavGroup[] = [
    {
      label: "Categories",
      defaultOpen: true,
      sub: cats.map(({ cat, count }) => ({
        label: `${cat} · ${count}`,
        href: `#cat-${slugifyCategory(cat)}`,
      })),
    },
  ];
  const sectionIds = cats.map(({ cat }) => `cat-${slugifyCategory(cat)}`);

  return (
    <FilesShell nav={nav} title="Components" sectionIds={sectionIds} fullBleed>
      <ComponentCanvas entries={entries} />
    </FilesShell>
  );
}
