"use client";

import { useEffect } from "react";
import { useScrollMemory } from "@/lib/use-scroll-memory";
import { useKeyboardShortcuts } from "@/lib/use-keyboard-shortcuts";
import { applyDensityFromStore, useUIStore } from "@/lib/ui-store";
import BackToTop from "@/components/ui/BackToTop";
import SearchModal from "@/components/SearchModal";
import { AnimatePresence } from "framer-motion";

/**
 * Client-only init shell. Mounted once in app/layout.tsx body so density
 * + scroll memory + back-to-top + any future global side effects run
 * regardless of which route is rendered.
 *
 * Keeping this in a dedicated component (rather than DocsShell or
 * FilesShell) means the behavior is uniform across every route — there's
 * no risk of one shell forgetting to apply density on first paint.
 *
 * S17 — also hosts a global SearchModal so the ⌘K hint in the home hero
 * (and the global ⌘K shortcut now in useKeyboardShortcuts) works on every
 * route, not just inside DocsShell/FilesShell. The shells continue to
 * mount their own SearchModal copies, but both use the same zustand
 * `searchOpen` flag — only one set of DOM nodes paints at a time because
 * the AnimatePresence is keyed and the modal portals to body.
 */
export default function RootClient() {
  useScrollMemory();
  useKeyboardShortcuts();
  const searchOpen = useUIStore((s) => s.searchOpen);
  const setSearchOpen = useUIStore((s) => s.setSearchOpen);
  useEffect(() => {
    applyDensityFromStore();
  }, []);

  // S22 — Footer ?sync=open / ?export=open query-string actions.
  // The footer links to `/?sync=open` and `/?export=open`; when the
  // homepage mounts (or any route receives those params), flip the
  // store flag so the matching modal opens. Strips the param from the
  // URL afterward so a refresh doesn't keep re-opening the modal.
  useEffect(() => {
    if (typeof window === "undefined") return;
    const url = new URL(window.location.href);
    const sync = url.searchParams.get("sync");
    const exp = url.searchParams.get("export");
    if (sync !== "open" && exp !== "open") return;
    if (sync === "open") {
      useUIStore.getState().setSyncOpen(true);
      url.searchParams.delete("sync");
    }
    if (exp === "open") {
      useUIStore.getState().setExportOpen(true);
      url.searchParams.delete("export");
    }
    window.history.replaceState({}, "", url.pathname + (url.search || "") + url.hash);
  }, []);
  return (
    <>
      <BackToTop />
      <AnimatePresence>
        {searchOpen && (
          <SearchModal key="root-search" onClose={() => setSearchOpen(false)} />
        )}
      </AnimatePresence>
    </>
  );
}
