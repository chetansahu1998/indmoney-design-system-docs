"use client";

import { useEffect, useState } from "react";
import Header from "@/components/Header";
import Sidebar, { type NavGroup } from "@/components/Sidebar";
import Footer from "@/components/Footer";
import SearchModal from "@/components/SearchModal";
import { AnimatePresence } from "framer-motion";
import { useUIStore, applyDensityFromStore } from "@/lib/ui-store";
import { useActiveSection } from "@/lib/use-active-section";
import { useIsMobile } from "@/lib/use-mobile";

/**
 * Layout shell for the Files routes. Mirrors DocsShell (Header + Sidebar +
 * main + Footer + scroll-spy) but accepts an arbitrary nav prop so a single
 * page can list every audited file in the sidebar and use sub-anchors per
 * screen.
 *
 * Foundations DocsShell stays unchanged — it owns its own section composition
 * and theme handling. FilesShell is the generalized wrapper for everything
 * else that wants the same chrome.
 */
export default function FilesShell({
  nav,
  title,
  sectionIds,
  children,
}: {
  nav: NavGroup[];
  title: string;
  /** All anchor ids the sidebar references — used by scroll-spy. */
  sectionIds: string[];
  children: React.ReactNode;
}) {
  const [theme, setTheme] = useState<"dark" | "light">("dark");
  const activeSection = useUIStore((s) => s.activeSection);
  const searchOpen = useUIStore((s) => s.searchOpen);
  const setSearchOpen = useUIStore((s) => s.setSearchOpen);
  const mobileMenuOpen = useUIStore((s) => s.mobileMenuOpen);
  const setMobileMenuOpen = useUIStore((s) => s.setMobileMenuOpen);
  const isMobile = useIsMobile();

  useEffect(() => {
    applyDensityFromStore();
  }, []);

  useEffect(() => {
    document.documentElement.setAttribute("data-theme", theme);
  }, [theme]);

  useEffect(() => {
    if (typeof window === "undefined") return;
    const saved = window.localStorage.getItem("indmoney-ds-theme") as "dark" | "light" | null;
    if (saved) setTheme(saved);
  }, []);

  useEffect(() => {
    if (typeof window === "undefined") return;
    window.localStorage.setItem("indmoney-ds-theme", theme);
  }, [theme]);

  // ⌘K
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

  useActiveSection(sectionIds);

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

      <Sidebar
        nav={nav}
        title={title}
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
          {children}
        </main>
      </div>
      <Footer />
    </>
  );
}
