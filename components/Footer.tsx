"use client";
import Link from "next/link";
import { brandLabel, currentBrand } from "@/lib/brand";
import { getExtractionMeta } from "@/lib/tokens/loader";

/**
 * Site footer. Reads brand from currentBrand() so the wordmark matches
 * whichever DS the docs site is rendering. Build provenance (last
 * extraction timestamp from the token meta) lands here as the closing
 * receipt — the design system is honest about when its data was last
 * refreshed.
 */
export default function Footer() {
  const brand = currentBrand();
  const meta = getExtractionMeta() as { extracted_at?: string };
  const extractedAt = meta.extracted_at ? new Date(meta.extracted_at) : null;
  const explore: { label: string; href: string }[] = [
    { label: "Foundations", href: "/" },
    { label: "Icons", href: "/icons" },
    { label: "Components", href: "/components" },
    { label: "Illustrations", href: "/illustrations" },
    { label: "Logos", href: "/logos" },
    { label: "Files audit", href: "/files" },
    { label: "Health", href: "/health" },
  ];
  const resources: { label: string; href: string }[] = [
    { label: "Sync from Figma", href: "/?sync=open" },
    { label: "Download tokens", href: "/?export=open" },
    { label: "Plugin guide", href: "/files" },
  ];
  return (
    <footer
      style={{
        background: "var(--bg-surface)",
        borderTop: "1px solid var(--border)",
        padding: "64px 40px 40px",
        display: "grid",
        gridTemplateColumns: "1fr auto auto",
        gap: 48,
        alignItems: "start",
      }}
    >
      <div>
        <div style={{ fontSize: 32, fontWeight: 700, letterSpacing: "-0.8px", color: "var(--text-1)" }}>
          {brandLabel(brand)} <span style={{ color: "var(--text-3)", fontWeight: 500 }}>DS</span>
        </div>
        <div style={{ fontSize: 13, color: "var(--text-3)", marginTop: 12 }}>
          © {new Date().getFullYear()} {brandLabel(brand)}. Tokens extracted from Figma.
        </div>
        {extractedAt && (
          <div
            style={{
              marginTop: 8,
              fontSize: 11,
              fontFamily: "var(--font-mono)",
              color: "var(--text-3)",
            }}
          >
            last sync: {extractedAt.toISOString().slice(0, 10)} ·{" "}
            {extractedAt.toISOString().slice(11, 16)}Z
          </div>
        )}
      </div>
      <FooterColumn title="Explore" items={explore} />
      <FooterColumn title="Resources" items={resources} />
    </footer>
  );
}

function FooterColumn({
  title,
  items,
}: {
  title: string;
  items: { label: string; href: string }[];
}) {
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
      <div
        style={{
          fontSize: 11,
          fontWeight: 600,
          color: "var(--text-3)",
          textTransform: "uppercase",
          letterSpacing: "0.06em",
          marginBottom: 4,
        }}
      >
        {title}
      </div>
      {items.map((it) => (
        <Link
          key={it.href}
          href={it.href}
          style={{ fontSize: 13, color: "var(--text-2)", textDecoration: "none" }}
        >
          {it.label}
        </Link>
      ))}
    </div>
  );
}
