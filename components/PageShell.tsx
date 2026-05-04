"use client";

/**
 * Slim shell used by /onboarding, /settings, /inbox — pages that don't
 * have a per-section left sidebar but still need the canonical top nav
 * so users can switch tabs without the bar appearing/disappearing.
 *
 * Mounts the same `<Header />` component that DocsShell + FilesShell
 * use, plus the SearchModal so ⌘K works on every page.
 */

import { AnimatePresence } from "framer-motion";
import { useEffect, useState } from "react";

import Header from "@/components/Header";
import SearchModal from "@/components/SearchModal";
import { useUIStore } from "@/lib/ui-store";

export default function PageShell({ children }: { children: React.ReactNode }) {
  const [theme, setTheme] = useState<"dark" | "light">("dark");
  const searchOpen = useUIStore((s) => s.searchOpen);
  const setSearchOpen = useUIStore((s) => s.setSearchOpen);
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
      {children}
    </>
  );
}
