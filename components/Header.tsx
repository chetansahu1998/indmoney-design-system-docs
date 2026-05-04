"use client";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { useEffect, useState } from "react";
import { motion, LayoutGroup } from "framer-motion";
import { brandLabel, currentBrand, BRANDS } from "@/lib/brand";
import { useUIStore, type Density } from "@/lib/ui-store";
import { useIsMobile } from "@/lib/use-mobile";
import { getExtractionMeta } from "@/lib/tokens/loader";
import { fetchInbox } from "@/lib/inbox/client";
import { getToken } from "@/lib/auth-client";

const DENSITY_LABEL: Record<Density, string> = {
  compact: "S",
  default: "M",
  comfortable: "L",
};
const DENSITY_TOOLTIP: Record<Density, string> = {
  compact: "Compact density (D)",
  default: "Default density (D)",
  comfortable: "Comfortable density (D)",
};

export default function Header({
  onSearchOpen,
  onThemeToggle,
  onMenuOpen,
  theme,
}: {
  onSearchOpen: () => void;
  onThemeToggle: () => void;
  onMenuOpen: () => void;
  theme: "dark" | "light";
}) {
  const brand = currentBrand();
  const setExportOpen = useUIStore((s) => s.setExportOpen);
  const setSyncOpen = useUIStore((s) => s.setSyncOpen);
  const density = useUIStore((s) => s.density);
  const setDensity = useUIStore((s) => s.setDensity);
  const meta = getExtractionMeta() as {
    roles?: number;
    base_colors?: number;
    observations?: number;
    glyph_colors?: number;
  };
  const tokenCount = meta.glyph_colors ?? meta.roles ?? 0;
  const baseColors = meta.base_colors ?? 0;

  const cycleDensity = () => {
    const order: Density[] = ["compact", "default", "comfortable"];
    const i = order.indexOf(density);
    setDensity(order[(i + 1) % order.length]);
  };

  // Mobile auto-hide: hide on scroll-down, show on scroll-up. Desktop
  // keeps a sticky header — sidebar is fixed there too, no real estate
  // pressure. On mobile the header is 72px of precious vertical space so
  // it earns its keep by getting out of the way when reading.
  const isMobile = useIsMobile();
  const [hidden, setHidden] = useState(false);
  useEffect(() => {
    if (!isMobile || typeof window === "undefined") return;
    let lastY = window.scrollY;
    const onScroll = () => {
      const y = window.scrollY;
      // Don't auto-hide near the top — feels twitchy.
      if (y < 80) {
        setHidden(false);
      } else if (y > lastY + 4) {
        setHidden(true);
      } else if (y < lastY - 4) {
        setHidden(false);
      }
      lastY = y;
    };
    window.addEventListener("scroll", onScroll, { passive: true });
    return () => window.removeEventListener("scroll", onScroll);
  }, [isMobile]);

  return (
    <motion.header
      initial={{ opacity: 0, y: -8 }}
      animate={{ opacity: 1, y: hidden ? "calc(-1 * var(--header-h))" : 0 }}
      transition={{ duration: 0.25, ease: [0.33, 1, 0.68, 1] }}
      className="site-header"
      style={{
        position: "fixed",
        top: 0,
        left: 0,
        right: 0,
        zIndex: 100,
        height: "var(--header-h)",
        background: "var(--bg-page)",
        borderBottom: "1px solid var(--border)",
        display: "flex",
        alignItems: "center",
        backdropFilter: "saturate(180%) blur(12px)",
      }}
    >
      {/* Hamburger — mobile-only */}
      <motion.button
        onClick={onMenuOpen}
        whileTap={{ scale: 0.9 }}
        transition={{ type: "spring", stiffness: 300, damping: 22 }}
        className="header-hamburger"
        aria-label="Open navigation"
        style={{
          width: 36,
          height: 36,
          borderRadius: 8,
          background: "var(--bg-surface)",
          border: "1px solid var(--border)",
          cursor: "pointer",
          alignItems: "center",
          justifyContent: "center",
          color: "var(--text-2)",
          flexShrink: 0,
        }}
      >
        <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
          <path
            d="M2 4h12M2 8h12M2 12h12"
            stroke="currentColor"
            strokeWidth="1.4"
            strokeLinecap="round"
          />
        </svg>
      </motion.button>

      {/* Brand identity + sync chip — natural width, no flex grow.
       *  Was previously flex:1 which caused PageNav (rendered inside this
       *  wrapper) to overflow into the BrandSwitcher rendered as a sibling
       *  to the right, producing a visible overlap at 1440px. PageNav is
       *  now a sibling instead, with its own flex-shrink rules. */}
      <motion.div
        initial={{ opacity: 0 }}
        animate={{ opacity: 1 }}
        transition={{ delay: 0.1, duration: 0.4 }}
        className="header-brand"
        style={{
          display: "flex",
          alignItems: "center",
          gap: 14,
          minWidth: 0,
          flexShrink: 0,
        }}
      >
        <span
          style={{
            fontWeight: 700,
            letterSpacing: "-0.5px",
            color: "var(--text-1)",
            fontSize: 15,
            whiteSpace: "nowrap",
          }}
        >
          {brandLabel(brand)} <span style={{ color: "var(--text-3)", fontWeight: 500 }}>DS</span>
        </span>
        <SyncChip tokens={tokenCount} baseColors={baseColors} />
      </motion.div>

      {/* Page-level navigation — separate flex item so its own width
       *  doesn't collide with the brand cluster. flex:1 absorbs the
       *  remaining horizontal space; PageNav itself wraps via the
       *  CSS class so narrow viewports hide individual links. */}
      <div
        className="header-pagenav-slot"
        style={{ flex: 1, minWidth: 0, display: "flex", alignItems: "center", overflow: "hidden" }}
      >
        <PageNav />
      </div>

      {/* BrandSwitcher hidden until v1.1 — Tickertape's deploy isn't wired
       *  yet, so the inline switcher links to a domain that 404s. The
       *  component code below stays for re-enable later. */}
      {/* {BRANDS.length > 1 && <BrandSwitcher brand={brand} />} */}

      {/* Search trigger */}
      <motion.button
        onClick={onSearchOpen}
        whileHover={{ scale: 1.02 }}
        whileTap={{ scale: 0.97 }}
        transition={{ type: "spring", stiffness: 300, damping: 22 }}
        className="search-btn"
        aria-label="Open search (cmd+k)"
        style={{
          display: "flex",
          alignItems: "center",
          gap: 8,
          height: 36,
          padding: "0 12px 0 10px",
          background: "var(--bg-surface)",
          border: "1px solid var(--border)",
          borderRadius: 8,
          cursor: "pointer",
          color: "var(--text-3)",
          fontSize: 13,
          fontFamily: "var(--font-sans)",
        }}
      >
        <svg width="14" height="14" viewBox="0 0 16 16" fill="none">
          <circle cx="7" cy="7" r="4.5" stroke="currentColor" strokeWidth="1.4" />
          <path
            d="M10.5 10.5L13 13"
            stroke="currentColor"
            strokeWidth="1.4"
            strokeLinecap="round"
          />
        </svg>
        <span className="search-text-label" style={{ color: "var(--text-3)" }}>
          {/* Audit C28: search indexes more than tokens (flows, decisions,
           *  DRDs, components). "Search docs…" communicates broader scope
           *  without overpromising "tokens". */}
          Search docs…
        </span>
        <kbd
          className="search-kbd"
          style={{
            marginLeft: 4,
            fontSize: 11,
            background: "var(--bg-surface-2)",
            border: "1px solid var(--border-strong)",
            borderRadius: 4,
            padding: "1px 5px",
            fontFamily: "var(--font-mono)",
            color: "var(--text-3)",
          }}
        >
          ⌘K
        </kbd>
      </motion.button>

      {/* Sync now */}
      <motion.button
        onClick={() => setSyncOpen(true)}
        whileHover={{ scale: 1.04 }}
        whileTap={{ scale: 0.94 }}
        transition={{ type: "spring", stiffness: 300, damping: 22 }}
        title="Sync tokens from Figma"
        aria-label="Sync tokens from Figma"
        style={{
          marginLeft: 8,
          display: "inline-flex",
          alignItems: "center",
          gap: 6,
          height: 36,
          padding: "0 12px",
          background: "var(--accent)",
          border: "1px solid var(--accent)",
          borderRadius: 8,
          cursor: "pointer",
          color: "#fff",
          fontSize: 13,
          fontWeight: 600,
        }}
        className="sync-btn"
      >
        <svg width="14" height="14" viewBox="0 0 16 16" fill="none">
          <path
            d="M2.5 5.5a5.5 5.5 0 019.49-3.79M13.5 10.5a5.5 5.5 0 01-9.49 3.79M14 2v3.5h-3.5M2 14v-3.5h3.5"
            stroke="currentColor"
            strokeWidth="1.4"
            strokeLinecap="round"
            strokeLinejoin="round"
          />
        </svg>
        <span className="sync-text-label">Sync</span>
      </motion.button>

      {/* Download tokens */}
      <motion.button
        onClick={() => setExportOpen(true)}
        whileHover={{ scale: 1.04 }}
        whileTap={{ scale: 0.94 }}
        transition={{ type: "spring", stiffness: 300, damping: 22 }}
        title="Download tokens (CSS · JSON · Swift · Android · Kotlin)"
        aria-label="Download tokens"
        style={{
          marginLeft: 8,
          display: "inline-flex",
          alignItems: "center",
          gap: 6,
          height: 36,
          padding: "0 12px",
          background: "var(--bg-surface)",
          border: "1px solid var(--border)",
          borderRadius: 8,
          cursor: "pointer",
          color: "var(--text-2)",
          fontSize: 13,
          fontWeight: 500,
        }}
        className="download-btn"
      >
        <svg width="14" height="14" viewBox="0 0 16 16" fill="none">
          <path
            d="M8 2v9m0 0l3-3m-3 3l-3-3M3 13h10"
            stroke="currentColor"
            strokeWidth="1.4"
            strokeLinecap="round"
            strokeLinejoin="round"
          />
        </svg>
        <span className="download-text-label">Download</span>
      </motion.button>

      {/* Density toggle */}
      <motion.button
        onClick={cycleDensity}
        whileHover={{ scale: 1.06 }}
        whileTap={{ scale: 0.92 }}
        transition={{ type: "spring", stiffness: 300, damping: 22 }}
        title={DENSITY_TOOLTIP[density]}
        aria-label={DENSITY_TOOLTIP[density]}
        className="header-density"
        style={{
          marginLeft: 8,
          width: 36,
          height: 36,
          borderRadius: 8,
          background: "var(--bg-surface)",
          border: "1px solid var(--border)",
          cursor: "pointer",
          color: "var(--text-2)",
          fontSize: 13,
          fontWeight: 600,
          fontFamily: "var(--font-mono)",
        }}
      >
        {DENSITY_LABEL[density]}
      </motion.button>

      {/* Theme toggle */}
      <motion.button
        title={`${theme === "dark" ? "Switch to light mode" : "Switch to dark mode"} (T)`}
        aria-label={theme === "dark" ? "Switch to light mode" : "Switch to dark mode"}
        onClick={onThemeToggle}
        whileHover={{ scale: 1.08 }}
        whileTap={{ scale: 0.9, rotate: 20 }}
        transition={{ type: "spring", stiffness: 300, damping: 22 }}
        style={{
          marginLeft: 8,
          width: 36,
          height: 36,
          borderRadius: "50%",
          background: "var(--bg-surface)",
          border: "1px solid var(--border)",
          cursor: "pointer",
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          color: "var(--text-2)",
        }}
      >
        <motion.div
          key={theme}
          initial={{ opacity: 0, rotate: -30, scale: 0.7 }}
          animate={{ opacity: 1, rotate: 0, scale: 1 }}
          exit={{ opacity: 0, rotate: 30, scale: 0.7 }}
          transition={{ duration: 0.2, ease: "easeOut" }}
        >
          {theme === "dark" ? (
            <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
              <circle cx="8" cy="8" r="3" stroke="currentColor" strokeWidth="1.4" />
              <path
                d="M8 2V1M8 15v-1M2 8H1M15 8h-1M3.5 3.5l-.7-.7M13.2 13.2l-.7-.7M3.5 12.5l-.7.7M13.2 2.8l-.7.7"
                stroke="currentColor"
                strokeWidth="1.4"
                strokeLinecap="round"
              />
            </svg>
          ) : (
            <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
              <path
                d="M13.5 10.5A6 6 0 0 1 5.5 2.5a6 6 0 1 0 8 8z"
                stroke="currentColor"
                strokeWidth="1.4"
                strokeLinecap="round"
                strokeLinejoin="round"
              />
            </svg>
          )}
        </motion.div>
      </motion.button>
    </motion.header>
  );
}

/**
 * Top-level page navigation — Foundations, Components, Illustrations, Logos.
 *
 * Reads the active route via Next's usePathname() so the SSR HTML renders
 * the correct active state (no hydration flash). Each entry is a Next Link
 * for SPA transitions + automatic prefetch.
 *
 * The active state is rendered as a layoutId="topnav-active" pill so the
 * highlight animates between routes instead of hard-cutting.
 */
function PageNav() {
  const pathname = usePathname() ?? "/";
  const inboxUnread = useInboxUnreadBadge();
  const items: { href: string; label: string; badge?: number }[] = [
    { href: "/",              label: "Foundations" },
    { href: "/atlas",         label: "Atlas" },
    { href: "/components",    label: "Components" },
    { href: "/icons",         label: "Icons" },
    { href: "/illustrations", label: "Illustrations" },
    { href: "/logos",         label: "Logos" },
    { href: "/inbox",         label: "Inbox", badge: inboxUnread },
    { href: "/onboarding",    label: "Onboarding" },
    { href: "/files",         label: "Files" },
    { href: "/health",        label: "Health" },
    { href: "/settings/notifications", label: "Settings" },
  ];
  return (
    <LayoutGroup id="topnav">
      <nav
        className="page-nav"
        aria-label="Site sections"
        style={{ display: "inline-flex", gap: 4, marginLeft: 6 }}
      >
        {items.map((item) => {
          const active =
            item.href === "/" ? pathname === "/" : pathname.startsWith(item.href);
          return (
            <Link
              key={item.href}
              href={item.href}
              prefetch
              aria-current={active ? "page" : undefined}
              style={{
                position: "relative",
                padding: "4px 10px",
                borderRadius: 6,
                fontSize: 12,
                fontWeight: active ? 600 : 500,
                color: active ? "var(--text-1)" : "var(--text-3)",
                textDecoration: "none",
                whiteSpace: "nowrap",
                transition: "color 0.15s",
              }}
            >
              {active && (
                <motion.span
                  layoutId="topnav-active"
                  style={{
                    position: "absolute",
                    inset: 0,
                    background: "var(--bg-surface-2)",
                    borderRadius: 6,
                    zIndex: -1,
                  }}
                  transition={{ type: "spring", stiffness: 380, damping: 30 }}
                />
              )}
              <span style={{ position: "relative", display: "inline-flex", alignItems: "center", gap: 5 }}>
                {item.label}
                {item.badge && item.badge > 0 ? (
                  <span
                    aria-label={`${item.badge} unread`}
                    style={{
                      display: "inline-flex",
                      alignItems: "center",
                      justifyContent: "center",
                      minWidth: 16,
                      height: 16,
                      padding: "0 5px",
                      background: "var(--accent)",
                      color: "#fff",
                      borderRadius: 999,
                      fontSize: 10,
                      fontWeight: 700,
                      lineHeight: 1,
                      fontVariantNumeric: "tabular-nums",
                    }}
                  >
                    {item.badge > 99 ? "99+" : item.badge}
                  </span>
                ) : null}
              </span>
            </Link>
          );
        })}
      </nav>
    </LayoutGroup>
  );
}

/**
 * S10 — Inbox unread badge. Polls /v1/inbox every 60s for the active-row
 * count when the user is signed in. Lightweight (no SSE, no websocket);
 * the inbox itself owns the SSE subscription. Returns 0 when unauthed
 * or when the fetch fails — silent degradation rather than a noisy badge.
 */
function useInboxUnreadBadge(): number {
  const [count, setCount] = useState(0);
  useEffect(() => {
    let cancelled = false;
    let timer: ReturnType<typeof setInterval> | null = null;
    const poll = async () => {
      if (!getToken()) {
        if (!cancelled) setCount(0);
        return;
      }
      const r = await fetchInbox({ limit: 1 });
      if (cancelled) return;
      if (r.ok) setCount(r.data.total ?? 0);
    };
    void poll();
    timer = setInterval(() => void poll(), 60_000);
    return () => {
      cancelled = true;
      if (timer) clearInterval(timer);
    };
  }, []);
  return count;
}

function SyncChip({ tokens, baseColors }: { tokens: number; baseColors: number }) {
  return (
    <div
      className="sync-chip"
      style={{
        // display intentionally left to CSS so the responsive @media rule
        // in globals.css can hide the chip on narrow viewports without
        // having to fight inline-style precedence.
        alignItems: "center",
        gap: 8,
        padding: "4px 10px",
        background: "var(--bg-surface)",
        border: "1px solid var(--border)",
        borderRadius: 999,
        fontSize: 11,
        color: "var(--text-3)",
        fontFamily: "var(--font-mono)",
        whiteSpace: "nowrap",
      }}
    >
      <span
        style={{
          width: 6,
          height: 6,
          borderRadius: "50%",
          background: "var(--accent)",
          boxShadow: "0 0 0 3px rgba(77, 147, 252, 0.18)",
        }}
      />
      {tokens} tokens · {baseColors} primitives
    </div>
  );
}

function BrandSwitcher({ brand }: { brand: string }) {
  const others = BRANDS.filter((b) => b !== brand);
  if (others.length === 0) return null;
  return (
    <div
      className="brand-switcher"
      style={{
        display: "inline-flex",
        gap: 4,
        marginRight: 12,
        padding: 3,
        background: "var(--bg-surface)",
        border: "1px solid var(--border)",
        borderRadius: 8,
      }}
    >
      <span
        style={{
          padding: "5px 9px",
          fontSize: 12,
          fontWeight: 600,
          background: "var(--bg-surface-2)",
          color: "var(--text-1)",
          borderRadius: 5,
        }}
      >
        {brandLabel(brand as never)}
      </span>
      {others.map((b) => (
        <a
          key={b}
          href={`https://${b}.ds.indmoney.dev`}
          style={{
            padding: "5px 9px",
            fontSize: 12,
            color: "var(--text-3)",
            textDecoration: "none",
            borderRadius: 5,
            opacity: 0.6,
          }}
          title={`Switch to ${brandLabel(b)} (deferred to v1.1)`}
        >
          {brandLabel(b)}
        </a>
      ))}
    </div>
  );
}
