/**
 * UI store — single source of truth for non-data UI state.
 *
 * Why Zustand vs useState: persistent state (theme, density, sidebar collapse)
 * + cross-component subscriptions (Header reads theme, ColorSection reads density)
 * with no provider wiring. Persists to localStorage for cross-session memory.
 */

import { create } from "zustand";
import { persist } from "zustand/middleware";

export type Density = "compact" | "default" | "comfortable";

interface UIState {
  /** Active section id for scroll-spy highlighting in Sidebar. */
  activeSection: string;
  setActiveSection: (id: string) => void;

  /** Sidebar groups currently collapsed (group ids). */
  collapsedGroups: Set<string>;
  toggleGroup: (id: string) => void;

  /** Density scaling — affects padding/leading via --density-scale CSS var. */
  density: Density;
  setDensity: (d: Density) => void;

  /** Are the search modal / mobile menu / download dialog open? */
  searchOpen: boolean;
  setSearchOpen: (open: boolean) => void;

  mobileMenuOpen: boolean;
  setMobileMenuOpen: (open: boolean) => void;

  exportOpen: boolean;
  setExportOpen: (open: boolean) => void;

  syncOpen: boolean;
  setSyncOpen: (open: boolean) => void;

  /** Recent search hits — surface in cmdk as the empty-state list. */
  recents: string[];
  pushRecent: (id: string) => void;
}

const DENSITY_SCALES: Record<Density, number> = {
  compact: 0.875,
  default: 1,
  comfortable: 1.125,
};

export const useUIStore = create<UIState>()(
  persist(
    (set, get) => ({
      activeSection: "color",
      setActiveSection: (id) => set({ activeSection: id }),

      collapsedGroups: new Set(),
      toggleGroup: (id) =>
        set((state) => {
          const next = new Set(state.collapsedGroups);
          if (next.has(id)) next.delete(id);
          else next.add(id);
          return { collapsedGroups: next };
        }),

      density: "default",
      setDensity: (density) => {
        if (typeof document !== "undefined") {
          document.documentElement.style.setProperty(
            "--density-scale",
            String(DENSITY_SCALES[density]),
          );
        }
        set({ density });
      },

      searchOpen: false,
      setSearchOpen: (open) => set({ searchOpen: open }),

      mobileMenuOpen: false,
      setMobileMenuOpen: (open) => set({ mobileMenuOpen: open }),

      exportOpen: false,
      setExportOpen: (open) => set({ exportOpen: open }),

      syncOpen: false,
      setSyncOpen: (open) => set({ syncOpen: open }),

      recents: [],
      pushRecent: (id) =>
        set((state) => {
          const next = [id, ...state.recents.filter((r) => r !== id)].slice(0, 8);
          return { recents: next };
        }),
    }),
    {
      name: "indmoney-ds-ui",
      // Only persist non-ephemeral state; serialize the Set as an Array.
      partialize: (state) => ({
        density: state.density,
        recents: state.recents,
        collapsedGroups: Array.from(state.collapsedGroups),
      }),
      // Rehydrate Set after loading from localStorage.
      merge: (persisted, current) => {
        const p = persisted as { collapsedGroups?: string[] } & Partial<UIState>;
        return {
          ...current,
          ...p,
          collapsedGroups: new Set(p.collapsedGroups ?? []),
        };
      },
    },
  ),
);

/** Apply persisted density on first client render. */
export function applyDensityFromStore() {
  if (typeof document === "undefined") return;
  const d = useUIStore.getState().density;
  document.documentElement.style.setProperty(
    "--density-scale",
    String(DENSITY_SCALES[d]),
  );
}
