import ComponentInspector from "@/components/ComponentInspector";
import PageShell from "@/components/PageShell";
import { iconsByKind } from "@/lib/icons/manifest";

export default function ComponentsPage() {
  const entries = iconsByKind("component");
  const totalVariants = entries.reduce((n, e) => n + (e.variants?.length ?? 0), 0);
  return (
    <PageShell>
      <main
        style={{
          maxWidth: 1100,
          margin: "0 auto",
          padding: "72px 80px 120px",
        }}
      >
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
            inline; each tile shows the canonical variant with size on the rail below.
          </p>
          <p style={{ fontSize: 12, color: "var(--text-3)", fontFamily: "var(--font-mono)", marginTop: 8 }}>
            {entries.length} components · {totalVariants} variants · source: glyph
          </p>
        </div>

        <ComponentInspector entries={entries} />
      </main>
    </PageShell>
  );
}
