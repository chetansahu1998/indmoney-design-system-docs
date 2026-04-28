"use client";

/**
 * ComponentCanvas — horizontal infinite-canvas component browser.
 *
 * The previous /components surface was a vertical document: categories
 * stacked, components inside grids, variants hidden behind a click. That
 * fights the way designers actually work — they think in canvas, not in
 * page-of-text.
 *
 * This surface is a single horizontally-scrolling canvas. Categories flow
 * left-to-right as section bands. Inside each band, components stack
 * vertically and each component shows its default variant at *Figma's
 * own dimensions* (proportionally clamped) with the rest of the variant
 * matrix strung out next to it. A designer dragging through this view
 * sees the same thing they'd see panning Figma's Atoms page — just
 * filtered to "shipped components" and searchable.
 *
 * Interactions:
 *   - Native horizontal scroll: trackpad two-finger swipe, scrollbar drag
 *   - Wheel-redirect: vertical mouse wheel scrolls the canvas horizontally
 *   - Drag-to-pan: middle-click drag OR space-bar+drag, à la Figma
 *   - Keyboard: ←/→ pan one viewport-width, Home jumps to start
 *   - Click a component: inspector overlay slides in from the right (does
 *     not reflow the canvas — overlay so the canvas position stays stable)
 *   - Esc: closes overlay
 *   - Search filters bands in place; empty bands disappear
 *
 * Performance:
 *   - Components render eagerly (89 entries × ~6 variants = ~600 image
 *     tags worst case, well within native lazy-load budget)
 *   - Each variant <img> uses loading="lazy" so off-screen variants don't
 *     hit the network until panned into view
 */

import { Fragment, useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { motion, AnimatePresence } from "framer-motion";
import {
  iconURL,
  slugifyCategory,
  axisMatrix,
  defaultVariantOf,
  type IconEntry,
  type VariantEntry,
  type ComponentProperty,
  type LayoutInfo,
} from "@/lib/icons/manifest";

/** Cap component preview width so 800-wide status bars don't blow out the lane. */
const PREVIEW_MAX_W = 360;
/** Cap component preview height — keeps tall logos from forcing the lane tall. */
const PREVIEW_MAX_H = 240;
/** Variant strip thumbnail size — small enough to fit ~10 next to a preview. */
const VAR_THUMB = 64;

export default function ComponentCanvas({ entries }: { entries: IconEntry[] }) {
  const [openSlug, setOpenSlug] = useState<string | null>(null);
  const [query, setQuery] = useState("");
  const canvasRef = useRef<HTMLDivElement>(null);

  // Group entries by category, preserve insertion order from manifest sort.
  const grouped = useMemo(() => {
    const map = new Map<string, IconEntry[]>();
    const q = query.trim().toLowerCase();
    for (const e of entries) {
      if (q && !e.name.toLowerCase().includes(q) && !e.slug.includes(q)) continue;
      const cat = e.category || "uncategorized";
      if (!map.has(cat)) map.set(cat, []);
      map.get(cat)!.push(e);
    }
    for (const list of map.values()) list.sort((a, b) => a.name.localeCompare(b.name));
    return map;
  }, [entries, query]);

  const total = useMemo(
    () => Array.from(grouped.values()).reduce((n, list) => n + list.length, 0),
    [grouped],
  );
  const openEntry = useMemo(
    () => entries.find((e) => e.slug === openSlug) ?? null,
    [entries, openSlug],
  );

  // — Canvas pan controls —
  // Wheel-redirect: vertical wheel deltas become horizontal scroll. Without
  // this, mice (no shift modifier, no horizontal wheel) can't browse the
  // canvas. Trackpad horizontal swipes already work natively, so they pass
  // through unchanged.
  useEffect(() => {
    const el = canvasRef.current;
    if (!el) return;
    const onWheel = (e: WheelEvent) => {
      // Only redirect when the user is scrolling vertically with no
      // horizontal intent. shift+wheel = native horizontal scroll → leave
      // alone. trackpad horizontal swipe has deltaX → leave alone.
      if (e.deltaX !== 0) return;
      if (Math.abs(e.deltaY) < 1) return;
      // Don't hijack vertical scroll when the cursor is over a band's
      // internal scroll body — designers need to scroll long category
      // bands (Buttons has 13 components) without their wheel jumping
      // sideways. The redirect kicks in only on the empty canvas gutters
      // and on band headers, where vertical wheel is otherwise dead input.
      const t = e.target as Node | null;
      if (t instanceof Element) {
        const inBandBody = t.closest("[data-band-body]");
        if (inBandBody) return;
      }
      e.preventDefault();
      el.scrollLeft += e.deltaY;
    };
    el.addEventListener("wheel", onWheel, { passive: false });
    return () => el.removeEventListener("wheel", onWheel);
  }, []);

  // Drag-to-pan: middle-mouse-button drag OR space-bar held + left-drag.
  // Figma uses space+drag as the canonical pan gesture; mirror it here so
  // muscle memory carries over.
  useEffect(() => {
    const el = canvasRef.current;
    if (!el) return;
    let panning = false;
    let spaceHeld = false;
    let startX = 0;
    let startScroll = 0;
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.code === "Space" && !isTextInput(e.target)) {
        spaceHeld = true;
        el.style.cursor = "grab";
      }
      // Arrow-key pan — one viewport width per press.
      if (e.code === "ArrowRight" && !isTextInput(e.target)) {
        e.preventDefault();
        el.scrollBy({ left: el.clientWidth * 0.85, behavior: "smooth" });
      }
      if (e.code === "ArrowLeft" && !isTextInput(e.target)) {
        e.preventDefault();
        el.scrollBy({ left: -el.clientWidth * 0.85, behavior: "smooth" });
      }
      if (e.code === "Home" && !isTextInput(e.target)) {
        e.preventDefault();
        el.scrollTo({ left: 0, behavior: "smooth" });
      }
    };
    const onKeyUp = (e: KeyboardEvent) => {
      if (e.code === "Space") {
        spaceHeld = false;
        el.style.cursor = "";
      }
    };
    const onMouseDown = (e: MouseEvent) => {
      // Middle-button = always pan. Left-button = pan only if space held.
      if (e.button === 1 || (e.button === 0 && spaceHeld)) {
        panning = true;
        startX = e.clientX;
        startScroll = el.scrollLeft;
        el.style.cursor = "grabbing";
        e.preventDefault();
      }
    };
    const onMouseMove = (e: MouseEvent) => {
      if (!panning) return;
      el.scrollLeft = startScroll - (e.clientX - startX);
    };
    const onMouseUp = () => {
      if (panning) {
        panning = false;
        el.style.cursor = spaceHeld ? "grab" : "";
      }
    };
    window.addEventListener("keydown", onKeyDown);
    window.addEventListener("keyup", onKeyUp);
    el.addEventListener("mousedown", onMouseDown);
    window.addEventListener("mousemove", onMouseMove);
    window.addEventListener("mouseup", onMouseUp);
    return () => {
      window.removeEventListener("keydown", onKeyDown);
      window.removeEventListener("keyup", onKeyUp);
      el.removeEventListener("mousedown", onMouseDown);
      window.removeEventListener("mousemove", onMouseMove);
      window.removeEventListener("mouseup", onMouseUp);
    };
  }, []);

  // Esc closes the overlay.
  useEffect(() => {
    if (!openSlug) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpenSlug(null);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [openSlug]);

  // Hash routing — sidebar links use #cat-<slug>. The canvas owns its own
  // horizontal scroll, so the browser's default vertical-anchor jump
  // doesn't apply. Listen for the hash and pan the canvas instead.
  useEffect(() => {
    const el = canvasRef.current;
    if (!el) return;
    const apply = () => {
      const hash = window.location.hash.replace(/^#cat-/, "");
      if (!hash) return;
      const target = el.querySelector(`[data-cat="${hash}"]`) as HTMLElement | null;
      if (!target) return;
      el.scrollTo({ left: target.offsetLeft - 24, behavior: "smooth" });
    };
    apply();
    window.addEventListener("hashchange", apply);
    return () => window.removeEventListener("hashchange", apply);
  }, []);

  // Pan to a category by id — used by the category jump-bar.
  const jumpTo = (cat: string) => {
    const el = canvasRef.current;
    if (!el) return;
    const target = el.querySelector(`[data-cat="${slugifyCategory(cat)}"]`) as HTMLElement | null;
    if (!target) return;
    const left = target.offsetLeft - 24;
    el.scrollTo({ left, behavior: "smooth" });
  };

  return (
    <div
      style={{
        display: "flex",
        flexDirection: "column",
        height: "calc(100vh - var(--header-h))",
        overflow: "hidden",
      }}
    >
      {/* ── Frozen toolbar ─────────────────────────────────────────── */}
      <div
        style={{
          flexShrink: 0,
          padding: "20px 28px 12px",
          borderBottom: "1px solid var(--border)",
          background: "var(--bg-page)",
        }}
      >
        <div
          style={{
            display: "flex",
            alignItems: "baseline",
            justifyContent: "space-between",
            gap: 16,
            flexWrap: "wrap",
            marginBottom: 14,
          }}
        >
          <div>
            <h1
              style={{
                fontSize: 28,
                fontWeight: 700,
                letterSpacing: "-0.6px",
                color: "var(--text-1)",
                lineHeight: 1.1,
                margin: 0,
              }}
            >
              Components
            </h1>
            <div
              style={{
                fontSize: 12,
                color: "var(--text-3)",
                fontFamily: "var(--font-mono)",
                marginTop: 4,
              }}
            >
              {entries.length} components ·{" "}
              {entries.reduce((n, e) => n + (e.variants?.length ?? 0), 0)} variants ·{" "}
              {grouped.size} categories
            </div>
          </div>
          <CanvasHelp />
        </div>
        <div style={{ display: "flex", alignItems: "center", gap: 12, flexWrap: "wrap" }}>
          <SearchBar value={query} onChange={setQuery} total={entries.length} matches={total} />
          <CategoryJump grouped={grouped} onJump={jumpTo} />
        </div>
      </div>

      {/* ── Canvas surface — horizontal scroll, full remaining height ── */}
      <div
        ref={canvasRef}
        data-component-canvas
        style={{
          flex: 1,
          minHeight: 0,
          overflowX: "auto",
          overflowY: "hidden",
          background:
            "radial-gradient(circle at 1px 1px, color-mix(in srgb, var(--text-3) 12%, transparent) 1px, transparent 0)",
          backgroundSize: "24px 24px",
          backgroundPosition: "0 0",
        }}
      >
        {total === 0 ? (
          <EmptyCanvas query={query} />
        ) : (
          <div
            style={{
              display: "flex",
              alignItems: "stretch",
              gap: 24,
              padding: "28px 32px",
              minHeight: "100%",
              width: "max-content",
            }}
          >
            {Array.from(grouped.entries()).map(([cat, list]) => (
              <CategoryBand
                key={cat}
                cat={cat}
                entries={list}
                openSlug={openSlug}
                onOpen={(slug) => setOpenSlug((s) => (s === slug ? null : slug))}
              />
            ))}
            <div style={{ width: 64, flexShrink: 0 }} aria-hidden />
          </div>
        )}
      </div>

      {/* ── Inspector overlay ──────────────────────────────────────── */}
      <AnimatePresence>
        {openEntry && (
          <InspectorOverlay
            key={openEntry.slug}
            entry={openEntry}
            onClose={() => setOpenSlug(null)}
          />
        )}
      </AnimatePresence>
    </div>
  );
}

/* ── Category band ─────────────────────────────────────────────────────── */

function CategoryBand({
  cat,
  entries,
  openSlug,
  onOpen,
}: {
  cat: string;
  entries: IconEntry[];
  openSlug: string | null;
  onOpen: (slug: string) => void;
}) {
  return (
    <section
      data-cat={slugifyCategory(cat)}
      style={{
        flexShrink: 0,
        alignSelf: "flex-start",
        maxHeight: "100%",
        background: "var(--bg-surface)",
        border: "1px solid var(--border)",
        borderRadius: 14,
        display: "flex",
        flexDirection: "column",
        boxShadow: "var(--elev-shadow-1)",
      }}
    >
      <header
        style={{
          display: "flex",
          alignItems: "center",
          gap: 10,
          padding: "16px 18px 12px",
          borderBottom: "1px solid var(--border)",
          flexShrink: 0,
          background: "var(--bg-surface)",
          borderTopLeftRadius: 14,
          borderTopRightRadius: 14,
        }}
      >
        <div
          style={{
            width: 6,
            height: 6,
            borderRadius: "50%",
            background: "var(--accent)",
          }}
        />
        <span
          style={{
            fontSize: 12,
            fontWeight: 700,
            color: "var(--text-1)",
            textTransform: "uppercase",
            letterSpacing: "0.06em",
          }}
        >
          {cat}
        </span>
        <span
          style={{
            fontFamily: "var(--font-mono)",
            fontSize: 10,
            color: "var(--text-3)",
            background: "var(--bg-surface-2)",
            border: "1px solid var(--border)",
            padding: "1px 6px",
            borderRadius: 4,
          }}
        >
          {entries.length}
        </span>
      </header>
      <div
        data-band-body
        style={{
          display: "flex",
          flexDirection: "column",
          gap: 12,
          padding: 18,
          paddingTop: 14,
          overflowY: "auto",
          flex: 1,
          minHeight: 0,
        }}
      >
        {entries.map((entry) => (
          <ComponentCard
            key={entry.slug + entry.variant_id}
            entry={entry}
            open={openSlug === entry.slug}
            onOpen={() => onOpen(entry.slug)}
          />
        ))}
      </div>
    </section>
  );
}

/* ── Component card — default preview + variant strip ─────────────────── */

function ComponentCard({
  entry,
  open,
  onOpen,
}: {
  entry: IconEntry;
  open: boolean;
  onOpen: () => void;
}) {
  const variants = entry.variants ?? [];
  const defaultV = useMemo(() => defaultVariantOf(entry), [entry]);
  const otherVariants = useMemo(
    () => variants.filter((v) => v.variant_id !== defaultV?.variant_id).slice(0, 12),
    [variants, defaultV],
  );

  // Real-size scaling. Take the default variant's actual width + height, scale
  // proportionally to fit inside the cap, but never up-scale (don't make a
  // 24px icon look 240px). Tiny components keep their tiny look — that's
  // what designers want to see when they're checking.
  const naturalW = defaultV?.width ?? entry.width;
  const naturalH = defaultV?.height ?? entry.height;
  const fitScale = Math.min(
    1,
    PREVIEW_MAX_W / Math.max(naturalW, 1),
    PREVIEW_MAX_H / Math.max(naturalH, 1),
  );
  const previewW = Math.max(120, Math.round(naturalW * fitScale));
  const previewH = Math.max(80, Math.round(naturalH * fitScale));

  const previewURL = defaultV
    ? `/icons/glyph/${defaultV.file}`
    : iconURL(entry);

  return (
    <motion.div
      layout
      initial={false}
      animate={{ scale: open ? 1.005 : 1 }}
      transition={{ type: "spring", stiffness: 320, damping: 26 }}
      style={{
        background: "var(--bg-page)",
        border: `1px solid ${open ? "var(--accent)" : "var(--border)"}`,
        borderRadius: 10,
        padding: 14,
        display: "flex",
        flexDirection: "column",
        gap: 10,
      }}
    >
      <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", gap: 8 }}>
        <div style={{ display: "flex", flexDirection: "column", gap: 2, minWidth: 0 }}>
          <span
            style={{
              fontSize: 13,
              fontWeight: 600,
              color: "var(--text-1)",
              overflow: "hidden",
              textOverflow: "ellipsis",
              whiteSpace: "nowrap",
              maxWidth: 280,
            }}
          >
            {entry.name}
          </span>
          <span
            style={{
              fontSize: 10,
              color: "var(--text-3)",
              fontFamily: "var(--font-mono)",
            }}
          >
            {naturalW} × {naturalH}
            {variants.length > 0 && ` · ${variants.length} variant${variants.length === 1 ? "" : "s"}`}
            {entry.variant_axes && entry.variant_axes.length > 0 && (
              <> · {entry.variant_axes.map((a) => a.name).join(" × ")}</>
            )}
          </span>
        </div>
        <button
          onClick={onOpen}
          aria-label={`Inspect ${entry.name}`}
          style={{
            background: open ? "var(--accent)" : "var(--bg-surface-2)",
            color: open ? "#fff" : "var(--text-2)",
            border: `1px solid ${open ? "var(--accent)" : "var(--border)"}`,
            borderRadius: 6,
            padding: "5px 10px",
            fontSize: 11,
            fontWeight: 600,
            cursor: "pointer",
            fontFamily: "var(--font-mono)",
            flexShrink: 0,
          }}
        >
          {open ? "open" : "inspect"}
        </button>
      </div>

      {/* Preview at native dimensions, on a checker pattern so transparent
       *  glyphs stay readable. */}
      <div
        style={{
          minWidth: previewW,
          width: previewW,
          height: previewH,
          background:
            "repeating-linear-gradient(45deg, transparent 0 6px, color-mix(in srgb, var(--text-3) 5%, transparent) 6px 7px)",
          border: "1px solid var(--border)",
          borderRadius: 8,
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          padding: 6,
        }}
      >
        <img
          src={previewURL}
          alt={entry.name}
          loading="lazy"
          draggable={false}
          style={{
            maxWidth: "100%",
            maxHeight: "100%",
            objectFit: "contain",
          }}
        />
      </div>

      {/* Variant strip — small thumbs of every other variant, scrollable
       *  inside the card if there are too many. */}
      {otherVariants.length > 0 && (
        <div
          style={{
            display: "flex",
            gap: 6,
            overflowX: "auto",
            paddingBottom: 4,
            scrollbarWidth: "thin",
          }}
        >
          {otherVariants.map((v) => (
            <VariantThumb key={v.variant_id} variant={v} />
          ))}
          {variants.length - 1 > 12 && (
            <div
              style={{
                width: VAR_THUMB,
                height: VAR_THUMB,
                background: "var(--bg-surface-2)",
                border: "1px dashed var(--border)",
                borderRadius: 6,
                display: "flex",
                alignItems: "center",
                justifyContent: "center",
                fontFamily: "var(--font-mono)",
                fontSize: 11,
                color: "var(--text-3)",
                flexShrink: 0,
              }}
              title={`${variants.length - 1 - 12} more — open inspector`}
            >
              +{variants.length - 1 - 12}
            </div>
          )}
        </div>
      )}
    </motion.div>
  );
}

function VariantThumb({ variant }: { variant: VariantEntry }) {
  return (
    <div
      title={variant.name}
      style={{
        width: VAR_THUMB,
        height: VAR_THUMB,
        background: "var(--bg-surface-2)",
        border: `1px solid ${variant.is_default ? "var(--accent)" : "var(--border)"}`,
        borderRadius: 6,
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        padding: 4,
        flexShrink: 0,
        position: "relative",
      }}
    >
      <img
        src={`/icons/glyph/${variant.file}`}
        alt={variant.name}
        loading="lazy"
        draggable={false}
        style={{ maxWidth: "100%", maxHeight: "100%", objectFit: "contain" }}
      />
      {variant.is_default && (
        <span
          style={{
            position: "absolute",
            top: 2,
            right: 2,
            width: 6,
            height: 6,
            background: "var(--accent)",
            borderRadius: "50%",
          }}
          aria-label="default variant"
        />
      )}
    </div>
  );
}

/* ── Inspector overlay drawer ──────────────────────────────────────────── */

function InspectorOverlay({ entry, onClose }: { entry: IconEntry; onClose: () => void }) {
  const variants = entry.variants ?? [];
  const matrix = useMemo(() => axisMatrix(entry), [entry]);
  const defaultV = useMemo(() => defaultVariantOf(entry), [entry]);
  const hasAxes = matrix.axes.length > 0 || matrix.scalars.length > 0;
  const hasLayout = !!defaultV?.layout?.mode && defaultV.layout.mode !== "NONE";

  return (
    <>
      {/* Scrim — fixed, covers everything below the global header (top nav
       *  stays usable while the drawer is open). */}
      <motion.div
        initial={{ opacity: 0 }}
        animate={{ opacity: 1 }}
        exit={{ opacity: 0 }}
        transition={{ duration: 0.18 }}
        onClick={onClose}
        style={{
          position: "fixed",
          top: "var(--header-h)",
          left: 0,
          right: 0,
          bottom: 0,
          background: "var(--scrim)",
          zIndex: 90,
        }}
      />
      <motion.aside
        initial={{ x: "100%" }}
        animate={{ x: 0 }}
        exit={{ x: "100%" }}
        transition={{ type: "spring", stiffness: 320, damping: 32 }}
        style={{
          position: "fixed",
          top: "var(--header-h)",
          right: 0,
          height: "calc(100vh - var(--header-h))",
          width: "min(460px, 100vw)",
          background: "var(--bg-surface)",
          borderLeft: "1px solid var(--border)",
          zIndex: 95,
          display: "flex",
          flexDirection: "column",
          boxShadow: "var(--elev-shadow-3)",
        }}
      >
        <div
          style={{
            padding: "18px 20px",
            borderBottom: "1px solid var(--border)",
            display: "flex",
            alignItems: "flex-start",
            gap: 12,
            flexShrink: 0,
          }}
        >
          <div style={{ minWidth: 0, flex: 1 }}>
            <div
              style={{
                fontSize: 17,
                fontWeight: 600,
                letterSpacing: "-0.2px",
                color: "var(--text-1)",
                wordBreak: "break-word",
              }}
            >
              {entry.name}
            </div>
            <div
              style={{
                fontSize: 11,
                color: "var(--text-3)",
                fontFamily: "var(--font-mono)",
                marginTop: 4,
              }}
            >
              {entry.slug}
            </div>
            <Link
              href={`/components/${entry.slug}`}
              style={{
                display: "inline-flex",
                marginTop: 10,
                fontSize: 11,
                fontWeight: 600,
                color: "var(--accent)",
                textDecoration: "none",
                fontFamily: "var(--font-mono)",
              }}
            >
              Open detail page →
            </Link>
          </div>
          <button
            onClick={onClose}
            aria-label="Close (Esc)"
            style={{
              background: "var(--bg-surface-2)",
              border: "1px solid var(--border)",
              cursor: "pointer",
              color: "var(--text-2)",
              padding: 6,
              borderRadius: 6,
              flexShrink: 0,
            }}
          >
            <svg width="14" height="14" viewBox="0 0 16 16" fill="none">
              <path d="M4 4l8 8M12 4l-8 8" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" />
            </svg>
          </button>
        </div>

        <div style={{ overflowY: "auto", flex: 1 }}>
          {entry.description && (
            <Section label="Description">
              <div
                style={{
                  fontSize: 13,
                  lineHeight: 1.55,
                  color: "var(--text-2)",
                  whiteSpace: "pre-wrap",
                  padding: "0 18px",
                }}
              >
                {entry.description}
              </div>
            </Section>
          )}
          {hasAxes && (
            <Section label="Variant axes" count={matrix.axes.length + matrix.scalars.length}>
              <div style={{ padding: "0 18px", display: "flex", flexDirection: "column", gap: 8 }}>
                {matrix.axes.map((axis) => (
                  <div
                    key={axis.name}
                    style={{
                      display: "flex",
                      gap: 10,
                      padding: "8px 10px",
                      background: "var(--bg-surface-2)",
                      border: "1px solid var(--border)",
                      borderRadius: 8,
                    }}
                  >
                    <div
                      style={{
                        fontFamily: "var(--font-mono)",
                        fontSize: 11,
                        fontWeight: 600,
                        color: "var(--text-1)",
                        minWidth: 70,
                        paddingTop: 2,
                      }}
                    >
                      {axis.name}
                    </div>
                    <div style={{ display: "flex", flexWrap: "wrap", gap: 4, flex: 1 }}>
                      {axis.values.map((v) => (
                        <span
                          key={v}
                          style={{
                            fontFamily: "var(--font-mono)",
                            fontSize: 10,
                            padding: "2px 7px",
                            background:
                              v === axis.default
                                ? "color-mix(in srgb, var(--accent) 14%, transparent)"
                                : "var(--bg-surface)",
                            border: `1px solid ${v === axis.default ? "var(--accent)" : "var(--border)"}`,
                            color: v === axis.default ? "var(--accent)" : "var(--text-2)",
                            borderRadius: 4,
                            fontWeight: v === axis.default ? 600 : 500,
                          }}
                        >
                          {v}
                          {v === axis.default && " ★"}
                        </span>
                      ))}
                    </div>
                  </div>
                ))}
                {matrix.scalars.map((p) => (
                  <ScalarPropRow key={p.name} prop={p} />
                ))}
              </div>
            </Section>
          )}
          {hasLayout && defaultV?.layout && (
            <Section label="Layout">
              <LayoutGrid layout={defaultV.layout} />
            </Section>
          )}
          {variants.length > 0 && (
            <Section label="All variants" count={variants.length}>
              <div
                style={{
                  padding: "0 18px 18px",
                  display: "grid",
                  gridTemplateColumns: "repeat(2, 1fr)",
                  gap: 8,
                }}
              >
                {variants.map((v) => (
                  <OverlayVariantTile key={v.variant_id} variant={v} />
                ))}
              </div>
            </Section>
          )}
        </div>
      </motion.aside>
    </>
  );
}

function Section({
  label,
  count,
  children,
}: {
  label: string;
  count?: number;
  children: React.ReactNode;
}) {
  return (
    <div style={{ paddingTop: 16 }}>
      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: 8,
          padding: "0 18px 8px",
          fontSize: 11,
          fontWeight: 600,
          color: "var(--text-3)",
          textTransform: "uppercase",
          letterSpacing: "0.07em",
        }}
      >
        <span>{label}</span>
        {count !== undefined && (
          <span
            style={{
              fontFamily: "var(--font-mono)",
              fontSize: 10,
              background: "var(--bg-surface-2)",
              border: "1px solid var(--border)",
              padding: "1px 6px",
              borderRadius: 4,
            }}
          >
            {count}
          </span>
        )}
      </div>
      {children}
    </div>
  );
}

function ScalarPropRow({ prop }: { prop: ComponentProperty }) {
  const cleanName = prop.name.split("#")[0];
  const tone =
    prop.type === "BOOLEAN"
      ? "color-mix(in srgb, var(--success) 14%, transparent)"
      : prop.type === "TEXT"
        ? "color-mix(in srgb, var(--accent) 14%, transparent)"
        : "color-mix(in srgb, var(--warn) 14%, transparent)";
  const toneColor =
    prop.type === "BOOLEAN" ? "var(--success)" : prop.type === "TEXT" ? "var(--accent)" : "var(--warn)";
  return (
    <div
      style={{
        display: "flex",
        alignItems: "center",
        gap: 10,
        padding: "8px 10px",
        background: "var(--bg-surface-2)",
        border: "1px solid var(--border)",
        borderRadius: 8,
      }}
    >
      <div
        style={{
          fontFamily: "var(--font-mono)",
          fontSize: 11,
          fontWeight: 600,
          color: "var(--text-1)",
          flex: 1,
        }}
      >
        {cleanName}
      </div>
      <span
        style={{
          fontFamily: "var(--font-mono)",
          fontSize: 9,
          padding: "2px 6px",
          background: tone,
          color: toneColor,
          borderRadius: 3,
          fontWeight: 600,
        }}
      >
        {prop.type}
      </span>
    </div>
  );
}

function LayoutGrid({ layout }: { layout: LayoutInfo }) {
  const padding =
    layout.padding_top !== undefined ||
    layout.padding_right !== undefined ||
    layout.padding_bottom !== undefined ||
    layout.padding_left !== undefined
      ? `${layout.padding_top ?? 0} ${layout.padding_right ?? 0} ${layout.padding_bottom ?? 0} ${layout.padding_left ?? 0}`
      : null;
  const rows: Array<[string, string | null | undefined]> = [
    ["mode", layout.mode],
    ["wrap", layout.wrap === "WRAP" ? "wrap" : layout.wrap === "NO_WRAP" ? "no wrap" : null],
    ["padding", padding],
    ["gap", layout.item_spacing != null ? `${layout.item_spacing}px` : null],
    [
      "align",
      layout.primary_align && layout.counter_align
        ? `${layout.primary_align} · ${layout.counter_align}`
        : null,
    ],
    [
      "sizing",
      layout.primary_sizing || layout.counter_sizing
        ? `${layout.primary_sizing ?? "—"} · ${layout.counter_sizing ?? "—"}`
        : null,
    ],
  ];
  const filled = rows.filter(([, v]) => v != null && v !== "");
  if (filled.length === 0) return null;
  return (
    <div
      style={{
        margin: "0 18px",
        padding: "10px 12px",
        background: "var(--bg-surface-2)",
        border: "1px solid var(--border)",
        borderRadius: 8,
        display: "grid",
        gridTemplateColumns: "max-content 1fr",
        rowGap: 6,
        columnGap: 14,
        fontFamily: "var(--font-mono)",
        fontSize: 11,
      }}
    >
      {filled.map(([k, v]) => (
        <Fragment key={k}>
          <span style={{ color: "var(--text-3)" }}>{k}</span>
          <span style={{ color: "var(--text-1)" }}>{v}</span>
        </Fragment>
      ))}
    </div>
  );
}

function OverlayVariantTile({ variant }: { variant: VariantEntry }) {
  return (
    <div
      style={{
        background: "var(--bg-surface-2)",
        border: `1px solid ${variant.is_default ? "var(--accent)" : "var(--border)"}`,
        borderRadius: 8,
        padding: 8,
        display: "flex",
        flexDirection: "column",
        gap: 6,
        position: "relative",
      }}
    >
      <div
        style={{
          height: 90,
          background: "var(--bg-surface)",
          border: "1px solid var(--border)",
          borderRadius: 6,
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          padding: 6,
        }}
      >
        <img
          src={`/icons/glyph/${variant.file}`}
          alt={variant.name}
          loading="lazy"
          style={{ maxWidth: "100%", maxHeight: "100%", objectFit: "contain" }}
        />
      </div>
      <div
        style={{
          fontFamily: "var(--font-mono)",
          fontSize: 9,
          color: "var(--text-2)",
          overflow: "hidden",
          textOverflow: "ellipsis",
          whiteSpace: "nowrap",
        }}
        title={variant.name}
      >
        {variant.name}
      </div>
      {variant.is_default && (
        <span
          style={{
            position: "absolute",
            top: 4,
            right: 4,
            fontSize: 8,
            fontFamily: "var(--font-mono)",
            fontWeight: 700,
            color: "var(--accent)",
            background: "color-mix(in srgb, var(--accent) 14%, transparent)",
            padding: "1px 5px",
            borderRadius: 3,
          }}
        >
          ★
        </span>
      )}
    </div>
  );
}

/* ── Toolbar bits ──────────────────────────────────────────────────────── */

function SearchBar({
  value,
  onChange,
  total,
  matches,
}: {
  value: string;
  onChange: (v: string) => void;
  total: number;
  matches: number;
}) {
  return (
    <div
      style={{
        display: "flex",
        alignItems: "center",
        gap: 10,
        background: "var(--bg-surface)",
        border: "1px solid var(--border)",
        borderRadius: 8,
        padding: "8px 12px",
        minWidth: 280,
        flex: "0 1 360px",
      }}
    >
      <svg width="13" height="13" viewBox="0 0 16 16" fill="none" style={{ color: "var(--text-3)" }}>
        <circle cx="7" cy="7" r="4.5" stroke="currentColor" strokeWidth="1.4" />
        <path d="M10.5 10.5L13 13" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
      </svg>
      <input
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder="Search components…"
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
        {value ? `${matches} of ${total}` : `${total}`}
      </span>
    </div>
  );
}

function CategoryJump({
  grouped,
  onJump,
}: {
  grouped: Map<string, IconEntry[]>;
  onJump: (cat: string) => void;
}) {
  const cats = Array.from(grouped.entries()).sort((a, b) => b[1].length - a[1].length);
  return (
    <div
      style={{
        display: "flex",
        gap: 4,
        overflowX: "auto",
        flex: 1,
        scrollbarWidth: "thin",
      }}
    >
      {cats.map(([cat, list]) => (
        <button
          key={cat}
          onClick={() => onJump(cat)}
          style={{
            background: "var(--bg-surface)",
            border: "1px solid var(--border)",
            borderRadius: 6,
            padding: "4px 10px",
            fontSize: 11,
            color: "var(--text-2)",
            cursor: "pointer",
            whiteSpace: "nowrap",
            fontFamily: "var(--font-sans)",
          }}
        >
          {cat}{" "}
          <span style={{ fontFamily: "var(--font-mono)", color: "var(--text-3)", marginLeft: 4 }}>
            {list.length}
          </span>
        </button>
      ))}
    </div>
  );
}

function CanvasHelp() {
  return (
    <div
      style={{
        display: "flex",
        gap: 10,
        fontFamily: "var(--font-mono)",
        fontSize: 10,
        color: "var(--text-3)",
        flexWrap: "wrap",
      }}
    >
      <Hint k="wheel" v="pan canvas" />
      <Hint k="space + drag" v="grab to pan" />
      <Hint k="←  →" v="step pan" />
      <Hint k="esc" v="close" />
    </div>
  );
}

function Hint({ k, v }: { k: string; v: string }) {
  return (
    <span style={{ display: "inline-flex", alignItems: "center", gap: 4 }}>
      <kbd
        style={{
          background: "var(--bg-surface-2)",
          border: "1px solid var(--border)",
          padding: "1px 6px",
          borderRadius: 3,
          color: "var(--text-2)",
          fontSize: 10,
        }}
      >
        {k}
      </kbd>
      <span>{v}</span>
    </span>
  );
}

function EmptyCanvas({ query }: { query: string }) {
  return (
    <div
      style={{
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        width: "100%",
        height: "100%",
        color: "var(--text-3)",
        fontSize: 14,
      }}
    >
      No components match &ldquo;{query}&rdquo;
    </div>
  );
}

function isTextInput(t: EventTarget | null): boolean {
  if (!t || !(t instanceof HTMLElement)) return false;
  const tag = t.tagName;
  return tag === "INPUT" || tag === "TEXTAREA" || t.isContentEditable;
}
