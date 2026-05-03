import ComponentCanvas from "@/components/ComponentCanvas";
import FilesShell from "@/components/files/FilesShell";
import { iconsByKind, slugifyCategory } from "@/lib/icons/manifest";
import type { NavGroup } from "@/components/Sidebar";

// Audit C27: per-route metadata.
export const metadata = { title: "Components · INDmoney DS" };

/**
 * /components — horizontal-canvas component browser (restored 2026-05-02).
 *
 * Designers think in canvas, not in page-of-text. The previous three-section
 * sidebar+grid+detail layout (commit 7ef994e) reverted the horizontal direction
 * the user originally shipped (b10b765); this page restores it on top of the
 * atomic-design tier hierarchy added later (430fdd7) — `parentComponents()`
 * filters to organism-tier components only, so /components stays the
 * organism gallery while atoms get their own surface in a future iteration.
 *
 * The canvas owns the entire viewport below the global header. Categories
 * flow left-to-right as section bands. Inside each band, components stack
 * vertically with their default variant at Figma's own dimensions and the
 * full variant matrix strung out next to it. Pan via trackpad / wheel /
 * space-drag / arrow keys. Click a component → inspector overlay slides in.
 *
 * Sidebar stays — category sub-anchors trigger horizontal pan via in-canvas
 * listeners (the canvas reads its `data-cat` lookup from the URL hash).
 *
 * Phase 4 reverse-view integration (commit 463692b — `WhereThisBreaks.tsx`
 * consumes `/v1/components/violations`) lives inside the inspector overlay
 * and is independent of this page-level layout choice.
 */
export default function ComponentsPage() {
  // Show every kind=component entry from Glyph regardless of atomic-design
  // tier (atom / molecule / parent). The previous parent-only filter dropped
  // 105 atoms + 11 molecules — i.e. the actual building blocks (Buttons,
  // Input Field, Bottom Nav, Bottom Sheet, etc.) — leaving only 2 entries
  // on screen. We still drop "Design System 🌟" since those are token-sheet
  // master frames, not composable components.
  const entries = iconsByKind("component").filter(
    (e) => (e.category ?? "").trim() !== "Design System 🌟",
  );

  const grouped = new Map<string, typeof entries>();
  for (const e of entries) {
    const cat = e.category || "uncategorized";
    if (!grouped.has(cat)) grouped.set(cat, []);
    grouped.get(cat)!.push(e);
  }
  // Audit C15: order bands by documented atomic-design tiers first
  // (Atoms → Molecules → Organisms → Templates → Pages), then alphabetical
  // for any category that doesn't slot into the canonical tier list.
  // The previous "alphabetical-by-cat-size" order was implicit and meant
  // a designer browsing the canvas saw "Buttons" (largest set) before
  // "Atoms" — fights how the team actually frames the system.
  const TIER_ORDER = ["Atoms", "Molecules", "Organisms", "Templates", "Pages"];
  const tierIndex = (cat: string) => {
    const i = TIER_ORDER.findIndex((t) => t.toLowerCase() === cat.toLowerCase());
    return i === -1 ? Number.MAX_SAFE_INTEGER : i;
  };
  const cats = Array.from(grouped.entries())
    .sort((a, b) => {
      const ai = tierIndex(a[0]);
      const bi = tierIndex(b[0]);
      if (ai !== bi) return ai - bi;
      return a[0].localeCompare(b[0]);
    })
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
