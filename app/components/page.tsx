import ComponentInspector from "@/components/ComponentInspector";
import FilesShell from "@/components/files/FilesShell";
import { iconsByKind, iconsByCategory, slugifyCategory } from "@/lib/icons/manifest";
import type { NavGroup } from "@/components/Sidebar";

export default function ComponentsPage() {
  const entries = iconsByKind("component");
  const grouped = iconsByCategory("component");
  const totalVariants = entries.reduce((n, e) => n + (e.variants?.length ?? 0), 0);

  // Sidebar nav: each component category gets a sub-anchor that scrolls to
  // its in-page section. Sorted by entry count desc so the load-bearing
  // categories surface first.
  const cats = Array.from(grouped.entries())
    .sort((a, b) => b[1].length - a[1].length)
    .map(([cat, list]) => ({ cat, count: list.length }));

  const nav: NavGroup[] = [
    {
      label: "Categories",
      defaultOpen: true,
      sub: cats.map(({ cat }) => ({
        label: cat,
        href: `#cat-${slugifyCategory(cat)}`,
      })),
    },
  ];
  const sectionIds = cats.map(({ cat }) => `cat-${slugifyCategory(cat)}`);

  return (
    <FilesShell nav={nav} title="Components" sectionIds={sectionIds}>
      <div style={{ borderBottom: "1px solid var(--border)", paddingBottom: 32, marginBottom: 32 }}>
        <h1
          style={{
            fontSize: 48,
            fontWeight: 700,
            letterSpacing: "-1.5px",
            color: "var(--text-1)",
            marginBottom: 12,
            lineHeight: 1.05,
          }}
        >
          Components
        </h1>
        <p style={{ fontSize: 15, color: "var(--text-2)", lineHeight: 1.65, maxWidth: 640 }}>
          Component primitives extracted from Glyph&apos;s Atoms page — CTAs, progress bars,
          action bars, status bars, time pills. Click any component to expand its variants
          inline; left-nav jumps to each Atoms-page SECTION.
        </p>
        <p style={{ fontSize: 12, color: "var(--text-3)", fontFamily: "var(--font-mono)", marginTop: 8 }}>
          {entries.length} components · {totalVariants} variants · source: glyph
        </p>
      </div>

      <ComponentInspector entries={entries} />
    </FilesShell>
  );
}
