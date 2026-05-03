"use client";

import { useEffect } from "react";
import { useUIStore, type Density } from "@/lib/ui-store";

/**
 * Global keyboard shortcuts:
 *
 *   ⌘K / Ctrl+K  open search modal (S17 — lifted to root so it works
 *                site-wide, not just inside DocsShell/FilesShell)
 *   T       toggle theme (dark ↔ light)
 *   D       cycle density (compact → default → comfortable)
 *   [ / ]   prev / next sidebar sub-anchor
 *   ?       show help (no-op for v1; reserved)
 *
 * The shells (DocsShell / FilesShell) still register their own ⌘K
 * listeners — that's tolerated because both call `setSearchOpen(!open)`
 * via the same zustand store; the duplicate fire is idempotent at the
 * store level (open → closed → open is the same final state if both
 * fire). The hero copy ⌘K hint now works on `/inbox`, `/onboarding`,
 * `/settings/notifications`, `/health`, etc.
 *
 * Bindings only fire when no input/textarea/contenteditable is focused —
 * we don't want T to flip the theme while the designer is typing in the
 * search field. ⌘K is allowed even when an input is focused (standard
 * search-modal behavior).
 */
export function useKeyboardShortcuts() {
  const setActiveSection = useUIStore((s) => s.setActiveSection);
  const setSearchOpen = useUIStore((s) => s.setSearchOpen);

  useEffect(() => {
    if (typeof window === "undefined") return;

    const isInputFocused = () => {
      const a = document.activeElement;
      if (!a) return false;
      const tag = a.tagName;
      return (
        tag === "INPUT" ||
        tag === "TEXTAREA" ||
        tag === "SELECT" ||
        (a as HTMLElement).isContentEditable
      );
    };

    const onKey = (e: KeyboardEvent) => {
      // ⌘K / Ctrl+K — fire even when an input is focused.
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setSearchOpen(!useUIStore.getState().searchOpen);
        return;
      }

      if (e.metaKey || e.ctrlKey || e.altKey || e.shiftKey) return;
      if (isInputFocused()) return;

      const key = e.key.toLowerCase();

      if (key === "t") {
        e.preventDefault();
        toggleTheme();
        return;
      }

      if (key === "d") {
        e.preventDefault();
        cycleDensity();
        return;
      }

      if (key === "[" || key === "]") {
        e.preventDefault();
        jumpSection(key === "]" ? 1 : -1, setActiveSection);
        return;
      }
    };

    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [setActiveSection, setSearchOpen]);
}

/** Toggle theme via the same localStorage + data-attribute mechanism the
 *  shells use, so the in-memory state inside DocsShell.useState stays in
 *  sync on next render (DocsShell re-reads localStorage). */
function toggleTheme() {
  const cur = document.documentElement.getAttribute("data-theme") ?? "dark";
  const next = cur === "dark" ? "light" : "dark";
  document.documentElement.setAttribute("data-theme", next);
  try {
    localStorage.setItem("indmoney-ds-theme", next);
  } catch {
    // localStorage may be disabled in private mode — silent no-op is fine.
  }
}

function cycleDensity() {
  const order: Density[] = ["compact", "default", "comfortable"];
  const cur = useUIStore.getState().density;
  const i = order.indexOf(cur);
  useUIStore.getState().setDensity(order[(i + 1) % order.length]);
}

/** Jump to the prev/next sidebar sub-anchor by reading data-anchor-id
 *  attributes from the currently mounted sidebar. Falls back to no-op
 *  when no sidebar is mounted. */
function jumpSection(delta: 1 | -1, setActiveSection: (id: string) => void) {
  const desktop = document.querySelector(".sidebar-desktop");
  const root = desktop ?? document;
  const items = Array.from(
    root.querySelectorAll<HTMLElement>("[data-anchor-id]"),
  );
  if (items.length === 0) return;

  const current = useUIStore.getState().activeSection;
  const idx = items.findIndex((el) => el.getAttribute("data-anchor-id") === current);
  const nextIdx =
    idx < 0
      ? delta === 1
        ? 0
        : items.length - 1
      : Math.max(0, Math.min(items.length - 1, idx + delta));
  const nextId = items[nextIdx].getAttribute("data-anchor-id");
  if (!nextId) return;

  const target = document.getElementById(nextId);
  if (target) {
    target.scrollIntoView({ behavior: "smooth", block: "start" });
    setActiveSection(nextId);
  }
}
