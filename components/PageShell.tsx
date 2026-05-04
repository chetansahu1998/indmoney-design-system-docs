"use client";

/**
 * Slim shell used by /onboarding, /settings, /inbox, /atlas — pages that
 * don't own a per-section sub-nav but still need the canonical top nav
 * + sidebar so users can switch tabs without the chrome appearing or
 * disappearing.
 *
 * Mounts the same `<Header />` + `<Sidebar />` components DocsShell +
 * FilesShell use, plus the SearchModal so ⌘K works on every page.
 *
 * Atlas opts out of the sidebar with `withSidebar={false}`; the brain
 * canvas needs the full main-content area.
 */

import { AnimatePresence } from "framer-motion";
import { useEffect, useState } from "react";

import Header from "@/components/Header";
import SearchModal from "@/components/SearchModal";
import Sidebar from "@/components/Sidebar";
import { useUIStore } from "@/lib/ui-store";

export default function PageShell({
  children,
  withSidebar = true,
}: {
  children: React.ReactNode;
  withSidebar?: boolean;
}) {
  const [theme, setTheme] = useState<"dark" | "light">("dark");
  const searchOpen = useUIStore((s) => s.searchOpen);
  const setSearchOpen = useUIStore((s) => s.setSearchOpen);
  const mobileMenuOpen = useUIStore((s) => s.mobileMenuOpen);
  const setMobileMenuOpen = useUIStore((s) => s.setMobileMenuOpen);

  // Hydrate theme from the bootstrap script's localStorage value.
  useEffect(() => {
    const saved = (localStorage.getItem("indmoney-ds-theme") as "dark" | "light" | null) ?? "dark";
    setTheme(saved);
  }, []);

  // Persist theme + apply to <html data-theme>.
  useEffect(() => {
    document.documentElement.setAttribute("data-theme", theme);
    localStorage.setItem("indmoney-ds-theme", theme);
  }, [theme]);

  // ⌘K opens search.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setSearchOpen(!searchOpen);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [searchOpen, setSearchOpen]);

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

      {withSidebar ? (
        <>
          <Sidebar
            activeSection=""
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
            {children}
          </div>
        </>
      ) : (
        children
      )}
    </>
  );
}
