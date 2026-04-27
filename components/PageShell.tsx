"use client";
import { brandLabel, currentBrand } from "@/lib/brand";

/**
 * Slim shell used by /components, /illustrations, /logos. Provides the brand
 * mark and top-level page navigation without dragging in DocsShell's sidebar,
 * search modal, or sync UI (those are tied to the foundations page).
 */
export default function PageShell({ children }: { children: React.ReactNode }) {
  const brand = currentBrand();
  return (
    <>
      <header
        style={{
          position: "sticky",
          top: 0,
          zIndex: 40,
          background: "var(--bg-page)",
          borderBottom: "1px solid var(--border)",
          height: 56,
          display: "flex",
          alignItems: "center",
          padding: "0 20px",
          gap: 16,
          backdropFilter: "saturate(180%) blur(8px)",
        }}
      >
        <a
          href="/"
          style={{
            fontWeight: 700,
            letterSpacing: "-0.5px",
            color: "var(--text-1)",
            fontSize: 15,
            textDecoration: "none",
            whiteSpace: "nowrap",
          }}
        >
          {brandLabel(brand)} <span style={{ color: "var(--text-3)", fontWeight: 500 }}>DS</span>
        </a>
        <PageNav />
      </header>
      {children}
    </>
  );
}

function PageNav() {
  const pathname = typeof window !== "undefined" ? window.location.pathname : "/";
  const items = [
    { href: "/",              label: "Foundations" },
    { href: "/components",    label: "Components" },
    { href: "/illustrations", label: "Illustrations" },
    { href: "/logos",         label: "Logos" },
    { href: "/files",         label: "Files" },
  ];
  return (
    <nav aria-label="Site sections" style={{ display: "inline-flex", gap: 4 }}>
      {items.map((item) => {
        const active = item.href === "/" ? pathname === "/" : pathname.startsWith(item.href);
        return (
          <a
            key={item.href}
            href={item.href}
            style={{
              padding: "4px 10px",
              borderRadius: 6,
              fontSize: 12,
              fontWeight: active ? 600 : 500,
              color: active ? "var(--text-1)" : "var(--text-3)",
              background: active ? "var(--bg-surface-2)" : "transparent",
              textDecoration: "none",
              whiteSpace: "nowrap",
            }}
          >
            {item.label}
          </a>
        );
      })}
    </nav>
  );
}
