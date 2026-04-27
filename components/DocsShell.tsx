"use client";
import { useEffect, useState } from "react";
import { AnimatePresence, motion } from "framer-motion";
import Header from "@/components/Header";
import Sidebar from "@/components/Sidebar";
import Footer from "@/components/Footer";
import SearchModal from "@/components/SearchModal";
import TokenExportDialog from "@/components/extensions/TokenExportDialog";
import SyncModal from "@/components/SyncModal";
import TypographySection from "@/components/sections/TypographySection";
import ColorSection from "@/components/sections/ColorSection";
import SpacingSection from "@/components/sections/SpacingSection";
import MotionSection from "@/components/sections/MotionSection";
import IconographySection from "@/components/sections/IconographySection";
import EffectsSection from "@/components/sections/EffectsSection";
import { useIsMobile } from "@/lib/use-mobile";
import { useUIStore, applyDensityFromStore } from "@/lib/ui-store";
import { brandLabel, currentBrand } from "@/lib/brand";

// Scroll-spy candidates. Color and Type sub-buckets are dynamic (driven by
// Glyph data), so we keep their parent IDs here and add the known sub-buckets
// that exist today. Sub-IDs that don't render are simply ignored by the
// IntersectionObserver — no error, just no scroll-spy until they're added.
const SECTIONS = [
  "color",
  "color-surface",
  "color-text-n-icon",
  "color-tertiary",
  "color-surface-market-ticker",
  "color-special",
  "color-base",
  "typography",
  "type-heading",
  "type-subtitle",
  "type-body",
  "type-caption",
  "type-overline",
  "type-small",
  "spacing",
  "spacing-scale",
  "spacing-padding",
  "spacing-radius",
  "motion",
  "motion-spring",
  "motion-opacity",
  "motion-scale",
  "iconography",
  "effects",
];

export default function DocsShell() {
  const [theme, setTheme] = useState<"dark" | "light">("dark");
  const activeSection = useUIStore((s) => s.activeSection);
  const setActiveSection = useUIStore((s) => s.setActiveSection);
  const searchOpen = useUIStore((s) => s.searchOpen);
  const setSearchOpen = useUIStore((s) => s.setSearchOpen);
  const mobileMenuOpen = useUIStore((s) => s.mobileMenuOpen);
  const setMobileMenuOpen = useUIStore((s) => s.setMobileMenuOpen);
  const isMobile = useIsMobile();
  const brand = currentBrand();

  // Apply persisted density on first client render
  useEffect(() => {
    applyDensityFromStore();
  }, []);

  // Theme effect
  useEffect(() => {
    document.documentElement.setAttribute("data-theme", theme);
  }, [theme]);

  // Read theme from localStorage on mount (next-themes-style)
  useEffect(() => {
    if (typeof window === "undefined") return;
    const saved = window.localStorage.getItem("indmoney-ds-theme") as "dark" | "light" | null;
    if (saved) setTheme(saved);
  }, []);

  useEffect(() => {
    if (typeof window === "undefined") return;
    window.localStorage.setItem("indmoney-ds-theme", theme);
  }, [theme]);

  // ⌘K / Ctrl+K
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === "k") {
        e.preventDefault();
        setSearchOpen(!searchOpen);
      }
    };
    window.addEventListener("keydown", handler);
    return () => window.removeEventListener("keydown", handler);
  }, [searchOpen, setSearchOpen]);

  // Scroll-spy + URL hash sync
  useEffect(() => {
    const els = SECTIONS.map((id) => document.getElementById(id)).filter(Boolean) as HTMLElement[];
    if (els.length === 0) return;

    const obs = new IntersectionObserver(
      (entries) => {
        const visible = entries.filter((e) => e.isIntersecting);
        if (visible.length === 0) return;
        // Pick the topmost visible
        visible.sort((a, b) => a.boundingClientRect.top - b.boundingClientRect.top);
        const id = visible[0].target.id;
        setActiveSection(id);
        // Update URL hash without scroll jump
        if (typeof window !== "undefined" && window.location.hash !== `#${id}`) {
          window.history.replaceState(null, "", `#${id}`);
        }
      },
      { rootMargin: "-15% 0px -75% 0px" },
    );
    els.forEach((el) => obs.observe(el));
    return () => obs.disconnect();
  }, [setActiveSection]);

  // Restore scroll position from URL hash on mount
  useEffect(() => {
    if (typeof window === "undefined") return;
    const hash = window.location.hash.replace("#", "");
    if (hash) {
      requestAnimationFrame(() => {
        const el = document.getElementById(hash);
        if (el) el.scrollIntoView({ behavior: "instant" as ScrollBehavior, block: "start" });
      });
    }
  }, []);

  const mainPadding = isMobile ? "32px 16px 80px" : "72px 80px 120px";

  return (
    <>
      <Header
        theme={theme}
        onThemeToggle={() => setTheme((t) => (t === "dark" ? "light" : "dark"))}
        onSearchOpen={() => setSearchOpen(true)}
        onMenuOpen={() => setMobileMenuOpen(true)}
      />

      <AnimatePresence>
        {searchOpen && <SearchModal key="search" onClose={() => setSearchOpen(false)} />}
      </AnimatePresence>

      <TokenExportDialog />

      <SyncModal
        open={useUIStore((s) => s.syncOpen)}
        onClose={() => useUIStore.getState().setSyncOpen(false)}
      />

      <Sidebar
        activeSection={activeSection}
        mobileOpen={mobileMenuOpen}
        onMobileClose={() => setMobileMenuOpen(false)}
      />

      <div
        className="main-with-sidebar"
        style={{
          display: "flex",
          marginTop: "var(--header-h)",
          minHeight: "calc(100vh - var(--header-h))",
        }}
      >
        <main
          style={{
            flex: 1,
            minWidth: 0,
            padding: mainPadding,
            maxWidth: isMobile ? "100%" : 1100,
          }}
        >
          {/* Hero */}
          <motion.div
            initial={{ opacity: 0, y: 20 }}
            animate={{ opacity: 1, y: 0 }}
            transition={{ duration: 0.5, ease: [0.33, 1, 0.68, 1] }}
            style={{
              borderBottom: "1px solid var(--border)",
              paddingBottom: isMobile ? 32 : 48,
              marginBottom: isMobile ? 40 : 64,
            }}
          >
            <h1
              style={{
                fontSize: isMobile ? 40 : 64,
                fontWeight: 700,
                letterSpacing: isMobile ? "-1px" : "-2px",
                lineHeight: 1.05,
                color: "var(--text-1)",
                marginBottom: 16,
              }}
            >
              Foundations
            </h1>
            <p
              style={{
                fontSize: isMobile ? 14 : 16,
                color: "var(--text-2)",
                maxWidth: 640,
                lineHeight: 1.65,
              }}
            >
              The {brandLabel(brand)} Design System foundations — color, typography, spacing,
              motion, iconography. Tokens are extracted from production Figma usage; light and
              dark modes are paired by design intent. Use ⌘K to find a token, click any swatch
              to copy.
            </p>
          </motion.div>

          <ColorSection />
          <TypographySection />
          <SpacingSection />
          <MotionSection />
          <IconographySection />
          <EffectsSection />

          {/* Bottom nav */}
          <nav
            style={{
              display: "grid",
              gridTemplateColumns: isMobile ? "1fr" : "1fr 1fr",
              gap: 16,
              marginTop: 80,
              borderTop: "1px solid var(--border)",
              paddingTop: 48,
            }}
          >
            {[
              { dir: "← Top", label: "Color", href: "#color", right: false },
              { dir: "Coming v1.1 →", label: "Components", href: "#", right: true },
            ].map((item) => (
              <motion.a
                key={item.label}
                href={item.href}
                whileHover={{ y: -2, boxShadow: "0 8px 24px rgba(0,0,0,0.12)" }}
                transition={{ type: "spring", stiffness: 300, damping: 22 }}
                style={{
                  display: "flex",
                  flexDirection: "column",
                  gap: 4,
                  padding: 24,
                  background: "var(--bg-surface)",
                  border: "1px solid var(--border)",
                  borderRadius: 8,
                  textDecoration: "none",
                  textAlign: item.right && !isMobile ? "right" : "left",
                }}
              >
                <span style={{ fontSize: 12, color: "var(--text-3)" }}>{item.dir}</span>
                <span style={{ fontSize: 16, fontWeight: 600, color: "var(--text-1)" }}>
                  {item.label}
                </span>
              </motion.a>
            ))}
          </nav>
        </main>
      </div>
      <Footer />
    </>
  );
}
