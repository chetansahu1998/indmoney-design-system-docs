"use client";

/**
 * JSON tab — U8.
 *
 * On click of a frame in the atlas (U7 emits to view-store via
 * `setActiveScreenID`), this tab lazy-fetches the screen's canonical_tree
 * from the U4 backend (GET /v1/projects/:slug/screens/:id/canonical-tree)
 * and renders it via JSONTreeNode default-collapsed at depth ≥2.
 *
 * Mode resolution (light/dark) happens at chip-render time via
 * `lib/projects/resolveTreeForMode.ts:makeResolver(activeMode, modeBindings)`.
 * The resolver is memoized per (mode × screen) so theme toggles don't
 * re-walk the tree.
 *
 * Per-screen canonical_tree blobs are cached in a Map keyed by screen_id
 * for the session — second click on the same screen is instant.
 */

import { useEffect, useMemo, useState } from "react";
import { lazyFetchCanonicalTree } from "@/lib/projects/client";
import {
  makeResolver,
  type ModeResolver,
} from "@/lib/projects/resolveTreeForMode";
import type { Screen, ScreenMode } from "@/lib/projects/types";
import { resolveTheme, useProjectView } from "@/lib/projects/view-store";
import EmptyTab from "./EmptyTab";
import EmptyState from "@/components/empty-state/EmptyState";
import JSONTreeNode from "./JSONTreeNode";

interface JSONTabProps {
  slug: string;
  screens: Screen[];
  screenModes: ScreenMode[];
}

// Session-scoped cache keyed by screen_id. Cleared when the user navigates
// away from /projects/[slug] (component unmount → no useRef survives), but
// preserved across tab switches within the same project view.
const treeCache = new Map<string, unknown>();

export default function JSONTab({ slug, screens, screenModes }: JSONTabProps) {
  const selectedScreenID = useProjectView((s) => s.selectedScreenID);
  const theme = useProjectView((s) => s.theme);
  const activeMode = resolveTheme(theme); // "light" | "dark"
  const [filter, setFilter] = useState("");
  const [tree, setTree] = useState<unknown>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Modes-by-label, parsed once per props change. The resolver consumes the
  // pre-parsed shape for O(1) lookups.
  const modeBindings = useMemo(
    () =>
      selectedScreenID
        ? screenModes
            .filter((m) => m.ScreenID === selectedScreenID)
            .map((m) => {
              try {
                const values = JSON.parse(m.ExplicitVariableModesJSON || "{}") as Record<string, unknown>;
                return { label: m.ModeLabel, values };
              } catch {
                return { label: m.ModeLabel, values: {} as Record<string, unknown> };
              }
            })
        : [],
    [selectedScreenID, screenModes],
  );

  // Resolver re-derived per (active mode × screen). React's useMemo is the
  // memoization layer; the resolver itself caches per-binding internally.
  const resolver: ModeResolver | null = useMemo(() => {
    if (!selectedScreenID || modeBindings.length === 0) return null;
    return makeResolver(activeMode, modeBindings);
  }, [selectedScreenID, activeMode, modeBindings]);

  // Lazy fetch. Triggered on screen-ID change; cached by screen.
  useEffect(() => {
    if (!selectedScreenID) {
      setTree(null);
      setError(null);
      return;
    }
    const cached = treeCache.get(selectedScreenID);
    if (cached !== undefined) {
      setTree(cached);
      setError(null);
      return;
    }
    let cancelled = false;
    setLoading(true);
    setError(null);
    lazyFetchCanonicalTree(slug, selectedScreenID)
      .then((res) => {
        if (cancelled) return;
        if (!res.ok) {
          setError(res.error || "Failed to fetch canonical_tree");
          return;
        }
        treeCache.set(selectedScreenID, res.data.canonical_tree);
        setTree(res.data.canonical_tree);
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [selectedScreenID, slug]);

  if (!selectedScreenID) {
    return (
      <EmptyTab
        title="Pick a screen in the atlas"
        description="Click any frame above to inspect its canonical_tree. Light/dark theme resolves bound variables in place."
      />
    );
  }

  const activeScreen = screens.find((s) => s.ID === selectedScreenID);

  return (
    <div data-anim="tab-content" style={{ display: "flex", flexDirection: "column", gap: 8 }}>
      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: 8,
          padding: "8px 12px",
          borderBottom: "1px solid var(--border)",
        }}
      >
        <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--text-3)" }}>
          screen:
        </span>
        <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--text-1)" }}>
          {activeScreen?.ScreenLogicalID ?? selectedScreenID}
        </span>
        <span style={{ flex: 1 }} />
        <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--text-3)" }}>
          mode:
        </span>
        <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--accent)" }}>
          {activeMode}
        </span>
      </div>
      <div style={{ padding: "0 12px" }}>
        <input
          type="search"
          placeholder="Filter by name / type / property"
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
          style={{
            width: "100%",
            padding: "6px 10px",
            border: "1px solid var(--border)",
            background: "var(--surface-1)",
            borderRadius: 6,
            fontFamily: "var(--font-mono)",
            fontSize: 12,
            color: "var(--text-1)",
          }}
        />
      </div>
      <div
        style={{
          padding: "8px 12px",
          maxHeight: "calc(100vh - 420px)",
          overflowY: "auto",
        }}
      >
        {loading && <EmptyState variant="loading" compact />}
        {error && (
          // 404 here means "this screen has no canonical_tree" — the
          // re-export-needed variant explains the action; other errors fall
          // back to the generic error variant with the detail attached.
          /404|not found|not yet captured/i.test(error) ? (
            <EmptyState variant="re-export-needed" description={error} compact />
          ) : (
            <EmptyState variant="error" description={error} compact />
          )
        )}
        {!loading && !error && tree !== null && (
          <JSONTreeNode
            node={tree}
            label="root"
            depth={0}
            resolver={resolver}
            forceOpen={false}
            filter={filter.trim().toLowerCase()}
          />
        )}
      </div>
    </div>
  );
}
