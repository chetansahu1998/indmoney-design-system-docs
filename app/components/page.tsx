import ComponentBrowser from "@/components/ComponentBrowser";
import FilesShell from "@/components/files/FilesShell";
import { parentComponents, slugifyCategory } from "@/lib/icons/manifest";
import type { NavGroup } from "@/components/Sidebar";

/**
 * /components — three-section workspace for PARENT (organism) components.
 *
 *   Section 1: FilesShell sidebar with categories.
 *   Section 2: Components grid for the active category, full main width
 *              by default.
 *   Section 3: Detail panel — opens on demand when a card is clicked,
 *              docks beside the grid (desktop) or as a bottom sheet
 *              (mobile). Closing returns the grid to full width.
 *
 * Atomic-design hierarchy: this surface shows tier="parent" only — the
 * 30 organisms designers actually consume (Toast Messages, Status Bar,
 * Footer CTA, Bottom Nav, Masthead/*, …). Atoms get their own surface
 * (`/atoms` planned) so designers can drill into primitives.
 *
 * Each parent's detail panel surfaces every variant with its own props,
 * layout, appearance, structure, and a "Built from" rail of the atoms
 * that variant composes — resolved via the manifest's composes[] graph.
 */
export default function ComponentsPage() {
  const entries = parentComponents();

  // Group by category. Categories on Design System page are derived
  // from the set name's "/"-prefix when present (e.g. "Masthead/Hot" →
  // "Masthead"); otherwise fall back to the page name.
  const grouped = new Map<string, typeof entries>();
  for (const e of entries) {
    const cat = e.category || "uncategorized";
    if (!grouped.has(cat)) grouped.set(cat, []);
    grouped.get(cat)!.push(e);
  }
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

  return (
    <FilesShell nav={nav} title="Components" sectionIds={[]}>
      <ComponentBrowser entries={entries} orderedCategories={orderedCategories} />
    </FilesShell>
  );
}
