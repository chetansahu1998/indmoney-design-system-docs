"use client";
import { motion } from "framer-motion";

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

  return (
    <motion.header
      initial={{ opacity: 0, y: -8 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.35, ease: [0.33, 1, 0.68, 1] }}
      className="site-header"
      style={{
        position: "fixed", top: 0, left: 0, right: 0, zIndex: 100,
        height: "var(--header-h)",
        background: "var(--bg-page)",
        borderBottom: "1px solid var(--border)",
        display: "flex", alignItems: "center",
        backdropFilter: "saturate(180%) blur(12px)",
      }}
    >
      {/* Hamburger — shown on mobile via CSS class */}
      <motion.button
        onClick={onMenuOpen}
        whileTap={{ scale: 0.9 }}
        transition={{ type: "spring", stiffness: 300, damping: 22 }}
        className="header-hamburger"
        style={{
          width: 36, height: 36, borderRadius: 8,
          background: "var(--bg-surface)", border: "1px solid var(--border)",
          cursor: "pointer", alignItems: "center", justifyContent: "center",
          color: "var(--text-2)", flexShrink: 0,
        }}
      >
        <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
          <path d="M2 4h12M2 8h12M2 12h12" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
        </svg>
      </motion.button>

      {/* Brand */}
      <motion.span
        initial={{ opacity: 0 }}
        animate={{ opacity: 1 }}
        transition={{ delay: 0.1, duration: 0.4 }}
        className="header-brand"
        style={{
          fontWeight: 700, letterSpacing: "-0.5px",
          color: "var(--text-1)", flex: 1,
        }}
      >
        Field DS
      </motion.span>

      {/* Search trigger */}
      <motion.button
        onClick={onSearchOpen}
        whileHover={{ scale: 1.02 }}
        whileTap={{ scale: 0.97 }}
        transition={{ type: "spring", stiffness: 300, damping: 22 }}
        className="search-btn"
        style={{
          display: "flex", alignItems: "center", gap: 8,
          height: 36,
          padding: "0 12px 0 10px",
          background: "var(--bg-surface)", border: "1px solid var(--border)",
          borderRadius: 8, cursor: "pointer", color: "var(--text-3)",
          fontSize: 13, fontFamily: "var(--font-sans)",
        }}
      >
        <svg width="14" height="14" viewBox="0 0 16 16" fill="none">
          <circle cx="7" cy="7" r="4.5" stroke="currentColor" strokeWidth="1.4" />
          <path d="M10.5 10.5L13 13" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
        </svg>
        <span className="search-text-label" style={{ color: "var(--text-3)" }}>Search…</span>
        <kbd className="search-kbd" style={{
          marginLeft: 4, fontSize: 11,
          background: "var(--bg-surface-2)", border: "1px solid var(--border-strong)",
          borderRadius: 4, padding: "1px 5px", fontFamily: "var(--font-mono)",
          color: "var(--text-3)",
        }}>⌘K</kbd>
      </motion.button>

      {/* Theme toggle */}
      <motion.button
        title={theme === "dark" ? "Switch to light mode" : "Switch to dark mode"}
        onClick={onThemeToggle}
        whileHover={{ scale: 1.08 }}
        whileTap={{ scale: 0.9, rotate: 20 }}
        transition={{ type: "spring", stiffness: 300, damping: 22 }}
        style={{
          width: 36, height: 36, borderRadius: "50%",
          background: "var(--bg-surface)", border: "1px solid var(--border)",
          cursor: "pointer", display: "flex", alignItems: "center", justifyContent: "center",
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
              <path d="M8 2V1M8 15v-1M2 8H1M15 8h-1M3.5 3.5l-.7-.7M13.2 13.2l-.7-.7M3.5 12.5l-.7.7M13.2 2.8l-.7.7"
                stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
            </svg>
          ) : (
            <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
              <path d="M13.5 10.5A6 6 0 0 1 5.5 2.5a6 6 0 1 0 8 8z" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" />
            </svg>
          )}
        </motion.div>
      </motion.button>
    </motion.header>
  );
}
