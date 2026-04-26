"use client";
import { motion } from "framer-motion";
import { brandLabel, currentBrand, BRANDS } from "@/lib/brand";
import { useUIStore, type Density } from "@/lib/ui-store";
import { getExtractionMeta } from "@/lib/tokens/loader";

const DENSITY_LABEL: Record<Density, string> = {
  compact: "S",
  default: "M",
  comfortable: "L",
};
const DENSITY_TOOLTIP: Record<Density, string> = {
  compact: "Compact density",
  default: "Default density",
  comfortable: "Comfortable density",
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
  const density = useUIStore((s) => s.density);
  const setDensity = useUIStore((s) => s.setDensity);
  const meta = getExtractionMeta();

  const cycleDensity = () => {
    const order: Density[] = ["compact", "default", "comfortable"];
    const i = order.indexOf(density);
    setDensity(order[(i + 1) % order.length]);
  };

  return (
    <motion.header
      initial={{ opacity: 0, y: -8 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.35, ease: [0.33, 1, 0.68, 1] }}
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

      {/* Brand identity + sync chip */}
      <motion.div
        initial={{ opacity: 0 }}
        animate={{ opacity: 1 }}
        transition={{ delay: 0.1, duration: 0.4 }}
        className="header-brand"
        style={{
          flex: 1,
          display: "flex",
          alignItems: "center",
          gap: 14,
          minWidth: 0,
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
        <SyncChip
          observations={meta.observations}
          roles={meta.roles}
          baseColors={meta.base_colors}
        />
      </motion.div>

      {/* Brand switcher (only renders when ≥2 brands available) */}
      {BRANDS.length > 1 && <BrandSwitcher brand={brand} />}

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
          Search tokens…
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
        title={theme === "dark" ? "Switch to light mode" : "Switch to dark mode"}
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

function SyncChip({
  observations,
  roles,
  baseColors,
}: {
  observations: number;
  roles: number;
  baseColors: number;
}) {
  return (
    <div
      className="sync-chip"
      style={{
        display: "inline-flex",
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
      {roles} roles · {baseColors} primitives · {observations} obs
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
