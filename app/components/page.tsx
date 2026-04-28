import ComponentBrowser from "@/components/ComponentBrowser";
import FilesShell from "@/components/files/FilesShell";
import { iconsByKind, iconsByCategory, slugifyCategory } from "@/lib/icons/manifest";
import type { NavGroup } from "@/components/Sidebar";

/**
 * /components — three-section workspace.
 *
 *   Section 1: FilesShell sidebar with categories (same nav pattern as
 *              every other tab).
 *   Section 2: Components grid for the active category, filling the
 *              full main column by default.
 *   Section 3: Detail panel — opens on demand when a card is clicked,
 *              docks beside the grid (desktop) or as a bottom sheet
 *              (mobile). Closing it returns the grid to full width.
 *
 * Sidebar order and grid order share `orderedCategories`, so clicking a
 * sidebar entry filters the grid in lockstep.
 */
export default function ComponentsPage() {
  const entries = iconsByKind("component");
  const grouped = iconsByCategory("component");

  const cats = Array.from(grouped.entries())
    .sort((a, b) => b[1].length - a[1].length)
    .map(([cat, list]) => ({ cat, count: list.length }));

  const orderedCategories = cats.map((c) => c.cat);

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

  // sectionIds intentionally empty — this page doesn't use vertical
  // scroll-spy; ComponentBrowser drives activeSection from clicks on
  // sidebar links via the global UI store.
  return (
    <FilesShell nav={nav} title="Components" sectionIds={[]}>
      <ComponentBrowser entries={entries} orderedCategories={orderedCategories} />
    </FilesShell>
  );
}
