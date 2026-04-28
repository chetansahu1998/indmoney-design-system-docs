"use client";

import { useEffect, useMemo, useState, useCallback } from "react";
import { motion, AnimatePresence } from "framer-motion";
import ComponentDetail from "@/components/ComponentDetail";
import {
  iconURL,
  slugifyCategory,
  type IconEntry,
} from "@/lib/icons/manifest";
import { useUIStore } from "@/lib/ui-store";
import { useIsMobile } from "@/lib/use-mobile";

/**
 * /components — three-section workspace.
 *
 * Section 1: Primary page navigation — the FilesShell sidebar with
 * categories (lives outside this component, same pattern as every other
 * tab).
 *
 * Section 2: Full-width content area. Shows every component for the
 * currently-active category as a clean grid. When nothing is selected,
 * the grid takes the entire main column.
 *
 * Section 3: Detail panel — opens on demand. Clicking a component
 * shrinks the grid into a narrower lane and docks the detail beside it
 * with the full ComponentDetail spec. Closing the detail expands the
 * grid back to full width. The grid keeps its scroll position so a
 * designer who's deep in a long category doesn't lose their place.
 *
 * Selection persists in `?c=<slug>` so refresh and deep-links work.
 */
export default function ComponentBrowser({
  entries,
  orderedCategories,
}: {
  entries: IconEntry[];
  /** Same order the sidebar uses, so left nav and content agree. */
  orderedCategories: string[];
}) {
  const activeSection = useUIStore((s) => s.activeSection);
  const setActiveSection = useUIStore((s) => s.setActiveSection);
  const isMobile = useIsMobile();
  const [slug, setSlug] = useState<string | null>(null);

  const bySlug = useMemo(() => {
    const m = new Map<string, IconEntry>();
    for (const e of entries) m.set(e.slug, e);
    return m;
  }, [entries]);

  const grouped = useMemo(() => {
    const map = new Map<string, IconEntry[]>();
    for (const cat of orderedCategories) map.set(cat, []);
    for (const e of entries) {
      const c = e.category || "uncategorized";
      if (!map.has(c)) map.set(c, []);
      map.get(c)!.push(e);
    }
    for (const list of map.values()) list.sort((a, b) => a.name.localeCompare(b.name));
    return map;
  }, [entries, orderedCategories]);

  const activeCat = useMemo(() => {
    const fromSection = activeSection?.startsWith("cat-")
      ? activeSection.replace(/^cat-/, "")
      : null;
    if (fromSection) {
      const match = orderedCategories.find((c) => slugifyCategory(c) === fromSection);
      if (match) return match;
    }
    return orderedCategories[0] ?? null;
  }, [activeSection, orderedCategories]);

  // Sync ?c=<slug> with state.
  useEffect(() => {
    const apply = () => {
      const url = new URL(window.location.href);
      const c = url.searchParams.get("c");
      setSlug(c && bySlug.has(c) ? c : null);
    };
    apply();
    window.addEventListener("popstate", apply);
    return () => window.removeEventListener("popstate", apply);
  }, [bySlug]);

  // Sidebar links are #cat-<slug>. The vertical scroll-spy that normally
  // syncs hash → activeSection is disabled on this route (no in-page
  // anchors), so wire the hash directly into the store here. This keeps
  // sidebar pill, header chip, and grid filter perfectly in lockstep
  // without needing IntersectionObserver targets in the DOM.
  useEffect(() => {
    const sync = () => {
      const h = window.location.hash.replace(/^#/, "");
      if (h.startsWith("cat-")) setActiveSection(h);
    };
    sync();
    window.addEventListener("hashchange", sync);
    return () => window.removeEventListener("hashchange", sync);
  }, [setActiveSection]);

  const selectComponent = useCallback(
    (next: string | null) => {
      const url = new URL(window.location.href);
      if (next) url.searchParams.set("c", next);
      else url.searchParams.delete("c");
      window.history.pushState(null, "", url.toString());
      setSlug(next);
      // Keep sidebar highlight honest with current selection's category.
      if (next) {
        const e = bySlug.get(next);
        if (e?.category) setActiveSection(`cat-${slugifyCategory(e.category)}`);
      }
    },
    [bySlug, setActiveSection],
  );

  const list = activeCat ? grouped.get(activeCat) ?? [] : [];
  const selected = slug ? bySlug.get(slug) ?? null : null;
  const detailOpen = !!selected;

  // Esc closes detail.
  useEffect(() => {
    if (!detailOpen) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") selectComponent(null);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [detailOpen, selectComponent]);

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 24 }}>
      <Header
        totalEntries={entries.length}
        totalVariants={entries.reduce((n, e) => n + (e.variants?.length ?? 0), 0)}
        catCount={orderedCategories.length}
        activeCat={activeCat}
        activeCatCount={list.length}
      />

      <div
        className="cb-grid"
        data-detail-open={detailOpen}
        data-mobile={isMobile}
      >
        <style>{`
          /* Section 2 (grid) takes 100% by default. When section 3 opens,
             it shrinks to the left lane and section 3 docks beside it.
             Mobile collapses to a single column and section 3 becomes a
             bottom sheet handled by AnimatePresence below. */
          .cb-grid {
            display: grid;
            grid-template-columns: 1fr;
            gap: 0;
            transition: grid-template-columns 220ms cubic-bezier(.2,.7,.2,1);
            align-items: start;
          }
          .cb-grid[data-detail-open="true"][data-mobile="false"] {
            grid-template-columns: minmax(0, 1fr) minmax(420px, 540px);
            gap: 24px;
          }
          .cb-grid > .cb-content { min-width: 0; }
          .cb-grid > .cb-detail {
            position: sticky;
            top: calc(var(--header-h) + 24px);
            max-height: calc(100vh - var(--header-h) - 48px);
            overflow-y: auto;
            background: var(--bg-surface);
            border: 1px solid var(--border);
            border-radius: 12px;
            padding: 28px 28px 36px;
            box-shadow: var(--elev-shadow-2);
          }
          .cb-grid[data-mobile="true"] > .cb-detail {
            position: fixed;
            top: var(--header-h);
            left: 0; right: 0; bottom: 0;
            max-height: none;
            border-radius: 16px 16px 0 0;
            border: 1px solid var(--border);
            border-bottom: none;
            padding: 18px 18px 32px;
            z-index: 60;
            background: var(--bg-page);
          }
        `}</style>

        <div className="cb-content">
          <ComponentGrid
            category={activeCat}
            list={list}
            selectedSlug={slug}
            detailOpen={detailOpen}
            onPick={selectComponent}
          />
        </div>

        {/* Section 3: detail panel. AnimatePresence so it slides in/out. */}
        <AnimatePresence mode="popLayout">
          {selected && (
            <motion.div
              key={selected.slug}
              className="cb-detail"
              initial={isMobile ? { y: "100%" } : { opacity: 0, x: 24 }}
              animate={isMobile ? { y: 0 } : { opacity: 1, x: 0 }}
              exit={isMobile ? { y: "100%" } : { opacity: 0, x: 24 }}
              transition={{ type: "spring", stiffness: 320, damping: 30 }}
            >
              <DetailHeader
                entry={selected}
                onClose={() => selectComponent(null)}
              />
              <ComponentDetail entry={selected} />
            </motion.div>
          )}
        </AnimatePresence>

        {/* Mobile scrim under the bottom-sheet detail. */}
        {isMobile && selected && (
          <motion.div
            initial={{ opacity: 0 }}
            animate={{ opacity: 1 }}
            exit={{ opacity: 0 }}
            transition={{ duration: 0.18 }}
            onClick={() => selectComponent(null)}
            style={{
              position: "fixed",
              top: "var(--header-h)",
              left: 0,
              right: 0,
              bottom: 0,
              background: "var(--scrim)",
              zIndex: 55,
            }}
          />
        )}
      </div>
    </div>
  );
}

/* ── Header (counts + active category banner) ─────────────────────────── */

function Header({
  totalEntries,
  totalVariants,
  catCount,
  activeCat,
  activeCatCount,
}: {
  totalEntries: number;
  totalVariants: number;
  catCount: number;
  activeCat: string | null;
  activeCatCount: number;
}) {
  return (
    <div style={{ borderBottom: "1px solid var(--border)", paddingBottom: 24 }}>
      <h1
        style={{
          fontSize: 36,
          fontWeight: 700,
          letterSpacing: "-1px",
          color: "var(--text-1)",
          margin: 0,
          lineHeight: 1.05,
        }}
      >
        Components
      </h1>
      <p
        style={{
          fontSize: 14,
          color: "var(--text-2)",
          lineHeight: 1.6,
          margin: "10px 0 0",
          maxWidth: 640,
        }}
      >
        Component primitives extracted from Glyph&apos;s Atoms page. Pick a category
        on the left, click any component to open its full spec on the right.
      </p>
      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: 14,
          marginTop: 10,
          flexWrap: "wrap",
          fontSize: 11,
          color: "var(--text-3)",
          fontFamily: "var(--font-mono)",
        }}
      >
        <span>
          {totalEntries} components · {totalVariants} variants · {catCount} categories
        </span>
        {activeCat && (
          <span
            style={{
              padding: "2px 8px",
              border: "1px solid var(--border)",
              borderRadius: 4,
              color: "var(--text-2)",
            }}
          >
            viewing: {activeCat}{" "}
            <span style={{ color: "var(--text-3)" }}>· {activeCatCount}</span>
          </span>
        )}
      </div>
    </div>
  );
}

/* ── Section 2: components grid ────────────────────────────────────────── */

function ComponentGrid({
  category,
  list,
  selectedSlug,
  detailOpen,
  onPick,
}: {
  category: string | null;
  list: IconEntry[];
  selectedSlug: string | null;
  detailOpen: boolean;
  onPick: (slug: string) => void;
}) {
  const [filter, setFilter] = useState("");
  const filtered = useMemo(() => {
    const q = filter.trim().toLowerCase();
    if (!q) return list;
    return list.filter((e) => e.name.toLowerCase().includes(q) || e.slug.includes(q));
  }, [list, filter]);

  // Tile sizing follows the available width — denser when detail is open.
  const minTile = detailOpen ? 200 : 240;

  return (
    <div>
      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: 12,
          marginBottom: 18,
          flexWrap: "wrap",
        }}
      >
        <div
          style={{
            display: "flex",
            alignItems: "center",
            gap: 8,
            background: "var(--bg-surface)",
            border: "1px solid var(--border)",
            borderRadius: 8,
            padding: "8px 12px",
            flex: "1 1 280px",
            minWidth: 240,
          }}
        >
          <svg width="13" height="13" viewBox="0 0 16 16" fill="none" style={{ color: "var(--text-3)" }}>
            <circle cx="7" cy="7" r="4.5" stroke="currentColor" strokeWidth="1.4" />
            <path d="M10.5 10.5L13 13" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
          </svg>
          <input
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            placeholder={`Filter ${category ?? "components"}…`}
            style={{
              flex: 1,
              background: "none",
              border: "none",
              outline: "none",
              fontSize: 13,
              color: "var(--text-1)",
              fontFamily: "var(--font-sans)",
            }}
          />
          <span style={{ fontSize: 10, color: "var(--text-3)", fontFamily: "var(--font-mono)" }}>
            {filter ? `${filtered.length} of ${list.length}` : `${list.length}`}
          </span>
        </div>
      </div>

      {filtered.length === 0 ? (
        <div
          style={{
            padding: "60px 24px",
            textAlign: "center",
            color: "var(--text-3)",
            fontSize: 14,
          }}
        >
          No components match.
        </div>
      ) : (
        <div
          style={{
            display: "grid",
            gridTemplateColumns: `repeat(auto-fill, minmax(${minTile}px, 1fr))`,
            gap: 12,
          }}
        >
          {filtered.map((e) => (
            <ComponentCard
              key={e.slug}
              entry={e}
              selected={selectedSlug === e.slug}
              onPick={() => onPick(e.slug)}
            />
          ))}
        </div>
      )}
    </div>
  );
}

function ComponentCard({
  entry,
  selected,
  onPick,
}: {
  entry: IconEntry;
  selected: boolean;
  onPick: () => void;
}) {
  const variantCount = entry.variants?.length ?? 0;
  return (
    <motion.button
      onClick={onPick}
      whileHover={{ y: -2 }}
      transition={{ type: "spring", stiffness: 320, damping: 26 }}
      data-component={entry.slug}
      data-selected={selected}
      style={{
        display: "flex",
        flexDirection: "column",
        gap: 10,
        padding: 14,
        background: selected ? "color-mix(in srgb, var(--accent) 8%, var(--bg-surface))" : "var(--bg-surface)",
        border: `1px solid ${selected ? "var(--accent)" : "var(--border)"}`,
        borderRadius: 10,
        textAlign: "left",
        cursor: "pointer",
        transition: "background 140ms ease, border-color 140ms ease",
      }}
    >
      <div
        style={{
          height: 96,
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          background:
            "repeating-linear-gradient(45deg, transparent 0 6px, color-mix(in srgb, var(--text-3) 5%, transparent) 6px 7px)",
          borderRadius: 6,
          padding: 10,
        }}
      >
        <img
          src={iconURL(entry)}
          alt={entry.name}
          loading="lazy"
          style={{ maxWidth: "100%", maxHeight: 80, objectFit: "contain" }}
        />
      </div>
      <div style={{ display: "flex", flexDirection: "column", gap: 4, minWidth: 0 }}>
        <div
          style={{
            display: "flex",
            alignItems: "center",
            justifyContent: "space-between",
            gap: 8,
          }}
        >
          <span
            style={{
              fontSize: 13,
              fontWeight: 600,
              color: selected ? "var(--accent)" : "var(--text-1)",
              overflow: "hidden",
              textOverflow: "ellipsis",
              whiteSpace: "nowrap",
            }}
          >
            {entry.name}
          </span>
          {variantCount > 0 && (
            <span
              style={{
                fontSize: 10,
                fontWeight: 600,
                color: "var(--accent)",
                background: "color-mix(in srgb, var(--accent) 14%, transparent)",
                padding: "1px 6px",
                borderRadius: 4,
                fontFamily: "var(--font-mono)",
                flexShrink: 0,
              }}
            >
              {variantCount}
            </span>
          )}
        </div>
        <span style={{ fontSize: 11, color: "var(--text-3)", fontFamily: "var(--font-mono)" }}>
          {entry.width} × {entry.height}
        </span>
      </div>
    </motion.button>
  );
}

/* ── Section 3: detail header (close button + breadcrumb) ─────────────── */

function DetailHeader({
  entry,
  onClose,
}: {
  entry: IconEntry;
  onClose: () => void;
}) {
  return (
    <div
      style={{
        display: "flex",
        alignItems: "center",
        justifyContent: "space-between",
        gap: 12,
        marginBottom: 20,
        paddingBottom: 14,
        borderBottom: "1px solid var(--border)",
      }}
    >
      <div
        style={{
          fontFamily: "var(--font-mono)",
          fontSize: 11,
          color: "var(--text-3)",
        }}
      >
        {entry.category} · {entry.slug}
      </div>
      <button
        onClick={onClose}
        title="Close (Esc)"
        aria-label="Close detail"
        style={{
          display: "inline-flex",
          alignItems: "center",
          gap: 6,
          background: "var(--bg-surface-2)",
          border: "1px solid var(--border)",
          borderRadius: 6,
          padding: "5px 10px",
          fontSize: 11,
          color: "var(--text-2)",
          cursor: "pointer",
          fontFamily: "var(--font-mono)",
        }}
      >
        <svg width="11" height="11" viewBox="0 0 16 16" fill="none">
          <path d="M4 4l8 8M12 4l-8 8" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" />
        </svg>
        close
      </button>
    </div>
  );
}
