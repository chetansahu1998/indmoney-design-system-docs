"use client";

/**
 * View state for the `/projects/[slug]` shell.
 *
 * Why a sibling store and not an extension of `lib/auth-client.ts`:
 *   - Auth state has different durability needs (logout clears it; theme
 *     should survive logout).
 *   - The view-store is non-essential for unauthenticated routes; importing
 *     it in marketing pages (logged-out homepage in a future plan) shouldn't
 *     pull auth into that bundle.
 *
 * Persisted slice:
 *   `theme` is persisted to localStorage as `indmoney-projects-view`. Every-
 *   thing else (tab, persona, version) is intentionally session-only because
 *   they're URL-bound (hash + search params) — persisting them would fight
 *   the deeplink behaviour spec'd in U6.
 */

import { create } from "zustand";
import { persist, createJSONStorage } from "zustand/middleware";

export type ThemeMode = "light" | "dark" | "auto";
export type ProjectTab = "drd" | "violations" | "decisions" | "json";

interface ProjectViewState {
  /** Persisted. The Auto value defers to `prefers-color-scheme`. */
  theme: ThemeMode;
  setTheme: (theme: ThemeMode) => void;
}

export const useProjectView = create<ProjectViewState>()(
  persist(
    (set) => ({
      theme: "auto",
      setTheme: (theme) => set({ theme }),
    }),
    {
      name: "indmoney-projects-view",
      // Storage explicit so SSR doesn't try to touch localStorage.
      storage: createJSONStorage(() => {
        if (typeof window === "undefined") {
          // SSR no-op storage. zustand requires the methods exist.
          return {
            getItem: () => null,
            setItem: () => undefined,
            removeItem: () => undefined,
          };
        }
        return window.localStorage;
      }),
      // Theme is the only persisted field — partializer keeps the snapshot tight.
      partialize: (s) => ({ theme: s.theme }),
    },
  ),
);

/**
 * Resolves a `ThemeMode` to the concrete `light` | `dark` value the page
 * actually applies. SSR-safe (defaults to `light` when window is missing —
 * the client effect re-evaluates after hydration).
 */
export function resolveTheme(theme: ThemeMode): "light" | "dark" {
  if (theme === "light" || theme === "dark") return theme;
  if (typeof window === "undefined") return "light";
  if (typeof window.matchMedia !== "function") return "light";
  return window.matchMedia("(prefers-color-scheme: dark)").matches
    ? "dark"
    : "light";
}

// ─── URL hash helpers (active tab + active persona) ──────────────────────────

/**
 * Tab routing per U6: active tab in URL hash for deeplinks. `#drd`,
 * `#violations`, `#decisions`, `#json`. We also support `#persona=KYC-pending`
 * appended to the same hash (delimited by `&`) for persona deeplinks, e.g.
 * `#violations&persona=KYC-pending`.
 *
 * Keeping the parser tiny + dependency-free so this module stays SSR-safe.
 */
const VALID_TABS: readonly ProjectTab[] = [
  "drd",
  "violations",
  "decisions",
  "json",
] as const;

export function parseHash(hash: string): {
  tab: ProjectTab | null;
  persona: string | null;
} {
  // Strip the leading `#` and split on `&` for `tab&key=value` style.
  const trimmed = hash.startsWith("#") ? hash.slice(1) : hash;
  if (!trimmed) return { tab: null, persona: null };
  const parts = trimmed.split("&");
  let tab: ProjectTab | null = null;
  let persona: string | null = null;
  for (const part of parts) {
    if (!part) continue;
    if (!part.includes("=")) {
      if ((VALID_TABS as readonly string[]).includes(part)) {
        tab = part as ProjectTab;
      }
      continue;
    }
    const [k, v] = part.split("=", 2);
    if (k === "persona" && v) {
      persona = decodeURIComponent(v);
    }
  }
  return { tab, persona };
}

export function buildHash(tab: ProjectTab, persona: string | null): string {
  const parts: string[] = [tab];
  if (persona) parts.push(`persona=${encodeURIComponent(persona)}`);
  return `#${parts.join("&")}`;
}
