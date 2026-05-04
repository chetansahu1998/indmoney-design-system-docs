"use client";

import { useEffect, useState } from "react";
import Header from "@/components/Header";
import Sidebar, { type NavGroup } from "@/components/Sidebar";
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
  fullBleed = false,
}: {
  nav: NavGroup[];
  title: string;
  /** All anchor ids the sidebar references — used by scroll-spy. */
  sectionIds: string[];
  children: React.ReactNode;
  /**
   * When true, drops the 1100px max-width and the heavy padding so the
   * page can render a full-bleed canvas. Used by `/components` for the
   * horizontal-scroll designer canvas — that surface needs the whole
   * viewport width and is responsible for its own internal padding.
   */
  fullBleed?: boolean;
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

  // ⌘K — see DocsShell for the rationale of capture-phase + case-insensitive
  // matching + no input-focus bail. Audit C6: tile-focused / canvas-focused
  // states must still open the modal.
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        e.stopPropagation();
        setSearchOpen(!searchOpen);
      }
    };
    window.addEventListener("keydown", handler, { capture: true });
    return () => window.removeEventListener("keydown", handler, { capture: true });
  }, [searchOpen, setSearchOpen]);

  useActiveSection(sectionIds);

  const mainPadding = fullBleed
    ? 0
    : isMobile
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
            maxWidth: fullBleed ? "none" : isMobile ? "100%" : 1100,
          }}
        >
          {children}
        </main>
      </div>
    </>
  );
}
