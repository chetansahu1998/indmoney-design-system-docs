"use client";

/**
 * lib/atlas/live-store.ts — single source of truth for the live atlas state.
 *
 * The store holds:
 *   - Brain-level data: domains, flows, synapses (+ their ETags)
 *   - Per-leaf data fetched on demand: leavesByFlow, framesByLeaf, overlaysByLeaf
 *   - The currently-open leaf (mirrors URL state)
 *   - The tweaks panel (persisted to localStorage)
 *
 * Two write paths:
 *   1. Bulk loaders (`hydrateInitial`, `loadLeaf`) — replace whole slices.
 *   2. SSE patches (`applyEvent`) — mutate in place, stamp `appearedAt` on
 *      newly-arriving entities so the bloom animation kicks in.
 *
 * Side effects (network, SSE) live outside this module; the store is a
 * pure cache + event reducer. UI components subscribe via the `useAtlas`
 * selectors at the bottom.
 */

import { create } from "zustand";
import { persist, createJSONStorage } from "zustand/middleware";
import {
  fetchInitialAtlasState,
  fetchLeafCanvas,
  fetchLeafOverlays,
  fetchLeavesForFlow,
  refetchBrainNodes,
} from "./data-adapters";
import {
  ATLAS_TWEAK_DEFAULTS,
  type ActivityEntry,
  type AtlasLiveEvent,
  type AtlasState,
  type AtlasTweaks,
  type DisplayComment,
  type DisplayDecision,
  type DisplayViolation,
  type DRDDocument,
  type Flow,
  type Frame,
  type Leaf,
  type LeafEdge,
  type Platform,
} from "./types";

// ─── Selection state (mirrors the URL) ───────────────────────────────────────

interface AtlasSelection {
  /** Brain-level node currently focused (project slug). */
  flowID: string | null;
  /** Open leaf (flow_id from our DB). null = leaf canvas closed. */
  leafID: string | null;
  /** Selected frame inside the open leaf. */
  frameID: string | null;
}

// ─── Per-leaf cache slot ─────────────────────────────────────────────────────

export interface LeafSlot {
  frames: Frame[];
  edges: LeafEdge[];
  overlays: {
    violations: DisplayViolation[];
    decisions: DisplayDecision[];
    activity: ActivityEntry[];
    comments: DisplayComment[];
    drd?: DRDDocument;
  };
  loadedAt: number;
  /**
   * Per-screen canonical_tree blobs lazy-loaded for the strict-TS
   * LeafFrameRenderer (canvas v2). Keyed by screens.id.
   *   - undefined entry  → not yet fetched (renderer triggers fetch).
   *   - null entry       → fetch completed but no tree available
   *                        (sheet-sync screens lack one until audit runs).
   *   - object entry     → ready-to-walk canonical tree.
   * Always present (initialized to `{}` in `loadLeaf`) so the renderer
   * doesn't have to null-check the map itself.
   */
  canonicalTreeByScreenID: Record<string, unknown>;
}

// ─── Store contract ──────────────────────────────────────────────────────────

interface AtlasStoreState {
  // Brain
  platform: Platform;
  domains: AtlasState["domains"];
  flows: Flow[];
  synapses: AtlasState["synapses"];
  brainNodesETag?: string;
  graphAggregateETag?: string;

  // Leaves
  leavesByFlow: Record<string, Leaf[]>;
  /** flow_id (= our DB flows.id) → loaded slot. */
  leafSlots: Record<string, LeafSlot>;

  // Selection (also mirrored in URL)
  selection: AtlasSelection;

  // Tweaks (persisted)
  tweaks: AtlasTweaks;

  // User directory cache (user_id → display name). Lazily populated by
  // adapters as needed; safe to leave empty (`displayNameFor` falls back).
  userDirectory: Record<string, string>;

  // First-load gate
  hydrated: boolean;
  loadingPlatform: Platform | null;

  // ─── Actions ───────────────────────────────────────────────────────────────
  setPlatform: (p: Platform) => Promise<void>;
  hydrateInitial: () => Promise<void>;
  refreshBrain: () => Promise<void>;
  loadLeavesForFlow: (slug: string, versionID?: string) => Promise<void>;
  openLeaf: (flowID: string | null) => Promise<void>;
  closeLeaf: () => void;
  selectFrame: (frameID: string | null) => void;
  selectFlow: (flowID: string | null) => void;
  setTweak: <K extends keyof AtlasTweaks>(key: K, value: AtlasTweaks[K]) => void;
  applyEvent: (evt: AtlasLiveEvent) => void;

  // Optimistic mutations
  patchViolationStatus: (violationID: string, status: DisplayViolation["status"]) => void;
  appendOptimisticComment: (leafID: string, who: string, body: string) => string;
  resolveOptimisticComment: (leafID: string, tempID: string, real: DisplayComment) => void;
}

// ─── Persisted slice (tweaks only) ───────────────────────────────────────────

interface PersistedSlice {
  tweaks: AtlasTweaks;
  platform: Platform;
}

// ─── Implementation ──────────────────────────────────────────────────────────

export const useAtlas = create<AtlasStoreState>()(
  persist<AtlasStoreState, [], [], PersistedSlice>(
    (set, get) => ({
      // Initial state
      platform: "mobile",
      domains: [],
      flows: [],
      synapses: [],
      leavesByFlow: {},
      leafSlots: {},
      selection: { flowID: null, leafID: null, frameID: null },
      tweaks: ATLAS_TWEAK_DEFAULTS,
      userDirectory: {},
      hydrated: false,
      loadingPlatform: null,

      // ─── Platform / hydration ────────────────────────────────────────────
      setPlatform: async (p) => {
        if (get().platform === p && get().hydrated) return;
        set({ platform: p, hydrated: false, leavesByFlow: {}, leafSlots: {} });
        await get().hydrateInitial();
      },

      hydrateInitial: async () => {
        const platform = get().platform;
        if (get().loadingPlatform === platform) return;
        set({ loadingPlatform: platform });
        const next = await fetchInitialAtlasState({
          platform,
          brainNodesETag: get().brainNodesETag,
          graphAggregateETag: get().graphAggregateETag,
        });
        // First paint stamps appearedAt=0 on every node so they skip the
        // entrance animation.
        const flows = next.flows.map((f) => ({ ...f, appearedAt: 0 }));
        set({
          domains: next.domains,
          flows,
          synapses: next.synapses,
          // brain-products carries the per-product leaves (one per
          // underlying project). Pre-populating short-circuits the
          // loadLeavesForFlow → fetchProject(<product-slug>) path that
          // would 404 because product slugs aren't real project rows.
          leavesByFlow: next.leavesByFlow,
          brainNodesETag: next.brainNodesETag,
          graphAggregateETag: next.graphAggregateETag,
          hydrated: true,
          loadingPlatform: null,
        });
      },

      refreshBrain: async () => {
        const platform = get().platform;
        const r = await refetchBrainNodes(platform, get().brainNodesETag);
        if ("notModified" in r) return;
        const now = Date.now();
        const prevByID = new Map(get().flows.map((f) => [f.id, f]));
        const merged: Flow[] = r.flows.map((next) => {
          const prev = prevByID.get(next.id);
          if (prev) {
            // Existing node — preserve appearedAt so we don't re-bloom on
            // every refresh; only freshly-arrived nodes get a stamp.
            return { ...next, appearedAt: prev.appearedAt };
          }
          return { ...next, appearedAt: now };
        });
        // Brain-products carries leaves alongside the flow rollup, so we
        // refresh leavesByFlow in the same pass — keeps the orbit dots in
        // sync with project add/delete without a per-flow round-trip.
        set({
          flows: merged,
          leavesByFlow: { ...get().leavesByFlow, ...r.leavesByFlow },
          brainNodesETag: r.etag,
        });
      },

      // ─── Leaf loading ────────────────────────────────────────────────────
      loadLeavesForFlow: async (slug, versionID) => {
        // Short-circuit when the brain-products hydrate already populated
        // leaves for this flow — even if the array is empty, presence in
        // the map signals "we know this product has no projects" and we
        // shouldn't fall through to the legacy per-project fetch.
        if (Object.prototype.hasOwnProperty.call(get().leavesByFlow, slug)) return;
        const leaves = await fetchLeavesForFlow(slug, versionID);
        // Mark all initially-loaded leaves with appearedAt=0 so they skip the
        // entrance animation; subsequent SSE-driven adds get stamped.
        const stamped = leaves.map((l) => ({ ...l, appearedAt: 0 }));
        set({ leavesByFlow: { ...get().leavesByFlow, [slug]: stamped } });
      },

      openLeaf: async (leafID) => {
        if (!leafID) {
          set({ selection: { ...get().selection, leafID: null, frameID: null } });
          return;
        }
        // After the brain-products migration: leafID is a ds-service project
        // slug; the parent flow on the brain is a taxonomy product. Walk the
        // map to find which product owns this leaf so the URL/state stay
        // consistent.
        const projects = Object.entries(get().leavesByFlow);
        let parentProductSlug: string | undefined;
        let leaf: Leaf | undefined;
        for (const [productSlug, leaves] of projects) {
          const found = leaves.find((l) => l.id === leafID);
          if (found) {
            parentProductSlug = productSlug;
            leaf = found;
            break;
          }
        }
        if (!parentProductSlug || !leaf) {
          // Caller hasn't loaded the parent yet; bail out softly and update
          // selection so the URL still reflects intent.
          set({ selection: { flowID: get().selection.flowID, leafID, frameID: null } });
          return;
        }
        set({ selection: { flowID: parentProductSlug, leafID, frameID: null } });

        // Fetch canvas + overlays from the LEAF's own project slug. flowID=""
        // tells fetchLeafCanvas/Overlays to pull the whole project (all flows
        // collapsed) rather than filter to one section.
        const projectSlug = leaf.id;
        const canvas = await fetchLeafCanvas(projectSlug, "", undefined);
        const framesByID = new Map(canvas.frames.map((f) => [f.id, f]));
        const overlays = await fetchLeafOverlays(
          projectSlug,
          "",
          undefined,
          framesByID,
          new Map(Object.entries(get().userDirectory)),
        );
        const slot: LeafSlot = {
          frames: canvas.frames.map((f) => ({ ...f, appearedAt: 0 })),
          edges: canvas.edges,
          overlays,
          loadedAt: Date.now(),
          // Seeded by fetchLeafCanvas: per-screen canonical_tree is
          // available for the first 20 screens (probe-walk for edge
          // inference); the remainder lazy-load as the v2 renderer scrolls.
          canonicalTreeByScreenID: canvas.canonicalTreeByScreenID ?? {},
        };
        set({ leafSlots: { ...get().leafSlots, [leafID]: slot } });
      },

      closeLeaf: () => {
        set({ selection: { ...get().selection, leafID: null, frameID: null } });
      },

      selectFrame: (frameID) => {
        set({ selection: { ...get().selection, frameID } });
      },

      selectFlow: (flowID) => {
        set({ selection: { ...get().selection, flowID } });
      },

      // ─── Tweaks ──────────────────────────────────────────────────────────
      setTweak: (key, value) => {
        set({ tweaks: { ...get().tweaks, [key]: value } });
      },

      // ─── SSE patches ─────────────────────────────────────────────────────
      applyEvent: (evt) => {
        switch (evt.type) {
          case "GraphIndexUpdated":
          case "view_ready": {
            // Both events imply the brain may have changed. Refetch with
            // ETag short-circuit so we don't roundtrip an unchanged payload.
            void get().refreshBrain();
            return;
          }

          case "audit_complete":
          case "audit_failed":
          case "audit_progress": {
            // The leaf overlays will reflect new violations. If the affected
            // project's leaf slot is open, refresh the violation slice in
            // the background. Post brain-products: sel.leafID is the
            // project slug; sel.flowID is the parent product slug. The SSE
            // event reports the project slug too, so match on leafID.
            const sel = get().selection;
            if (!sel.leafID) return;
            if (sel.leafID !== evt.slug) return;
            const slot = get().leafSlots[sel.leafID];
            if (!slot) return;
            const framesByID = new Map(slot.frames.map((f) => [f.id, f]));
            void fetchLeafOverlays(
              sel.leafID,
              "",
              undefined,
              framesByID,
              new Map(Object.entries(get().userDirectory)),
            ).then((next) => {
              const slots = get().leafSlots;
              const cur = slots[sel.leafID!];
              if (!cur) return;
              set({ leafSlots: { ...slots, [sel.leafID!]: { ...cur, overlays: next } } });
            });
            return;
          }

          case "violation_lifecycle_changed": {
            get().patchViolationStatus(evt.violationID, evt.status);
            return;
          }

          case "decision.created":
          case "decision.superseded":
          case "comment.created": {
            const sel = get().selection;
            if (!sel.leafID) return;
            // Match the SSE event's flowID against the parent ds-service
            // flow inside our open leaf project. Today we don't track which
            // flow within the project the user is viewing, so refresh the
            // whole project's overlays whenever any of its flows ping.
            const slot = get().leafSlots[sel.leafID];
            if (!slot) return;
            const framesByID = new Map(slot.frames.map((f) => [f.id, f]));
            void fetchLeafOverlays(
              sel.leafID,
              "",
              undefined,
              framesByID,
              new Map(Object.entries(get().userDirectory)),
            ).then((next) => {
              const slots = get().leafSlots;
              const cur = slots[sel.leafID!];
              if (!cur) return;
              set({ leafSlots: { ...slots, [sel.leafID!]: { ...cur, overlays: next } } });
            });
            return;
          }

          default:
            return;
        }
      },

      // ─── Optimistic mutations ────────────────────────────────────────────
      patchViolationStatus: (violationID, status) => {
        const slots = get().leafSlots;
        let touched = false;
        const next: Record<string, LeafSlot> = {};
        for (const [k, slot] of Object.entries(slots)) {
          let dirty = false;
          const violations = slot.overlays.violations.map((v) => {
            if (v.id !== violationID) return v;
            dirty = true;
            return { ...v, status, pending: false };
          });
          if (dirty) {
            touched = true;
            next[k] = { ...slot, overlays: { ...slot.overlays, violations } };
          } else {
            next[k] = slot;
          }
        }
        if (touched) set({ leafSlots: next });
      },

      appendOptimisticComment: (leafID, who, body) => {
        const tempID = `tmp-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 7)}`;
        const slot = get().leafSlots[leafID];
        if (!slot) return tempID;
        const optimistic: DisplayComment = {
          id: tempID,
          who,
          body,
          ago: "just now",
          createdAt: new Date().toISOString(),
          reactions: 0,
          pending: true,
        };
        set({
          leafSlots: {
            ...get().leafSlots,
            [leafID]: {
              ...slot,
              overlays: { ...slot.overlays, comments: [...slot.overlays.comments, optimistic] },
            },
          },
        });
        return tempID;
      },

      resolveOptimisticComment: (leafID, tempID, real) => {
        const slot = get().leafSlots[leafID];
        if (!slot) return;
        const comments = slot.overlays.comments.map((c) => (c.id === tempID ? real : c));
        set({
          leafSlots: {
            ...get().leafSlots,
            [leafID]: { ...slot, overlays: { ...slot.overlays, comments } },
          },
        });
      },
    }),
    {
      name: "atlas-tweaks",
      version: 1,
      storage: createJSONStorage(() => localStorage),
      partialize: (s) => ({ tweaks: s.tweaks, platform: s.platform }),
      merge: (persisted, current) => {
        const p = persisted as Partial<PersistedSlice> | undefined;
        return {
          ...current,
          tweaks: { ...ATLAS_TWEAK_DEFAULTS, ...(p?.tweaks ?? {}) },
          platform: p?.platform ?? current.platform,
        };
      },
    },
  ),
);

// ─── Selectors ───────────────────────────────────────────────────────────────

export const selectFlows = (s: AtlasStoreState) => s.flows;
export const selectDomains = (s: AtlasStoreState) => s.domains;
export const selectSynapses = (s: AtlasStoreState) => s.synapses;
export const selectSelection = (s: AtlasStoreState) => s.selection;
export const selectTweaks = (s: AtlasStoreState) => s.tweaks;

export const selectOpenLeaf = (s: AtlasStoreState): Leaf | null => {
  const id = s.selection.leafID;
  if (!id) return null;
  for (const leaves of Object.values(s.leavesByFlow)) {
    const found = leaves.find((l) => l.id === id);
    if (found) return found;
  }
  return null;
};

export const selectOpenLeafSlot = (s: AtlasStoreState): LeafSlot | null => {
  const id = s.selection.leafID;
  return id ? s.leafSlots[id] ?? null : null;
};

export const selectLeavesForFlow = (slug: string) =>
  (s: AtlasStoreState): Leaf[] => s.leavesByFlow[slug] ?? [];
