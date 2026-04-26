"use client";
import { useEffect, useState } from "react";
import { AnimatePresence, motion } from "framer-motion";
import Header from "@/components/Header";
import Sidebar from "@/components/Sidebar";
import Footer from "@/components/Footer";
import SearchModal from "@/components/SearchModal";
import TypographySection from "@/components/sections/TypographySection";
import ColorSection from "@/components/sections/ColorSection";
import SpacingSection from "@/components/sections/SpacingSection";
import MotionSection from "@/components/sections/MotionSection";
import IconographySection from "@/components/sections/IconographySection";
import { useIsMobile } from "@/lib/use-mobile";

const SECTIONS = [
  "type-ramp","type-hierarchy","type-styles",
  "color","color-text","color-surface","color-border",
  "spacing","spacing-scale","spacing-radius",
  "motion","motion-spring","motion-opacity","motion-scale",
  "iconography",
];

export default function DocsShell() {
  const [active, setActive] = useState("type-ramp");
  const [theme, setTheme] = useState<"dark" | "light">("dark");
  const [searchOpen, setSearchOpen] = useState(false);
  const [mobileMenuOpen, setMobileMenuOpen] = useState(false);
  const isMobile = useIsMobile();

  useEffect(() => {
    document.documentElement.setAttribute("data-theme", theme);
  }, [theme]);

  // ⌘K / Ctrl+K
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === "k") {
        e.preventDefault();
        setSearchOpen(true);
      }
    };
    window.addEventListener("keydown", handler);
    return () => window.removeEventListener("keydown", handler);
  }, []);

  // Scroll-spy
  useEffect(() => {
    const els = SECTIONS.map((id) => document.getElementById(id)).filter(Boolean) as HTMLElement[];
    const obs = new IntersectionObserver(
      (entries) => {
        entries.forEach((e) => { if (e.isIntersecting) setActive(e.target.id); });
      },
      { rootMargin: "-10% 0px -80% 0px" }
    );
    els.forEach((el) => obs.observe(el));
    return () => obs.disconnect();
  }, []);

  const mainPadding = isMobile
    ? "32px 16px 80px"
    : "72px 80px 120px";

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

      <Sidebar
        activeSection={active}
        mobileOpen={mobileMenuOpen}
        onMobileClose={() => setMobileMenuOpen(false)}
      />

      <div className="main-with-sidebar" style={{
        display: "flex",
        marginTop: "var(--header-h)",
        minHeight: "calc(100vh - var(--header-h))",
      }}>
        <main style={{
          flex: 1, minWidth: 0,
          padding: mainPadding,
          maxWidth: isMobile ? "100%" : 1100,
        }}>
          {/* Hero */}
          <motion.div
            initial={{ opacity: 0, y: 20 }}
            animate={{ opacity: 1, y: 0 }}
            transition={{ duration: 0.5, ease: [0.33, 1, 0.68, 1] }}
            style={{ borderBottom: "1px solid var(--border)", paddingBottom: isMobile ? 32 : 48, marginBottom: isMobile ? 40 : 64 }}
          >
            <h1 style={{
              fontSize: isMobile ? 40 : 64,
              fontWeight: 700,
              letterSpacing: isMobile ? "-1px" : "-2px",
              lineHeight: 1.05, color: "var(--text-1)",
              marginBottom: 16,
            }}>
              Foundations
            </h1>
            <p style={{ fontSize: isMobile ? 14 : 16, color: "var(--text-2)", maxWidth: 640, lineHeight: 1.65 }}>
              Typography, color, and spacing are the core building blocks of the Field Design System. These foundational decisions create visual harmony and consistency across every product surface.
            </p>
          </motion.div>

          <TypographySection />
          <ColorSection />
          <SpacingSection />
          <MotionSection />
          <IconographySection />

          {/* Bottom nav */}
          <nav style={{
            display: "grid",
            gridTemplateColumns: isMobile ? "1fr" : "1fr 1fr",
            gap: 16,
            marginTop: 80, borderTop: "1px solid var(--border)", paddingTop: 48,
          }}>
            {[
              { dir: "← Previous", label: "Logo", href: "#", right: false },
              { dir: "Next →",     label: "Components", href: "#", right: true },
            ].map((item) => (
              <motion.a
                key={item.label}
                href={item.href}
                whileHover={{ y: -2, boxShadow: "0 8px 24px rgba(0,0,0,0.12)" }}
                transition={{ type: "spring", stiffness: 300, damping: 22 }}
                style={{
                  display: "flex", flexDirection: "column", gap: 4,
                  padding: 24,
                  background: "var(--bg-surface)", border: "1px solid var(--border)",
                  borderRadius: 8, textDecoration: "none",
                  textAlign: item.right && !isMobile ? "right" : "left",
                }}
              >
                <span style={{ fontSize: 12, color: "var(--text-3)" }}>{item.dir}</span>
                <span style={{ fontSize: 16, fontWeight: 600, color: "var(--text-1)" }}>{item.label}</span>
              </motion.a>
            ))}
          </nav>
        </main>
      </div>
      <Footer />
    </>
  );
}
