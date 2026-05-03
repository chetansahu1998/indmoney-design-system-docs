"use client";
import { Fragment, useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { motion, AnimatePresence, LayoutGroup } from "framer-motion";
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
import { useIsMobile } from "@/lib/use-mobile";
import DataGapPreview from "@/components/ui/DataGapPreview";

/**
 * Component gallery + Zeplin-style inspector panel.
 *
 * Layout:
 *   [grid taking remaining width] [sticky right-side inspector, 380px]
 *
 * The inspector docks to the right when a component is selected. The grid
 * compresses to fit the remaining space — tiles don't move, they just rewrap
 * because the parent flex shrinks. Click X or another tile to swap target;
 * Esc closes.
 *
 * Mobile (≤900px): the inspector becomes a bottom sheet instead. Real estate
 * is too tight for a side dock.
 */
export default function ComponentInspector({
  entries,
}: {
  entries: IconEntry[];
}) {
  const [openSlug, setOpenSlug] = useState<string | null>(null);
  const [query, setQuery] = useState("");
  const isMobile = useIsMobile();

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

  // Esc closes the inspector — keyboard-first UX.
  useEffect(() => {
    if (!openSlug) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpenSlug(null);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [openSlug]);

  return (
    <div>
      <SearchBar value={query} onChange={setQuery} total={entries.length} matches={total} />

      {/* Two-column layout when inspector is open on desktop; single column
       *  on mobile (inspector becomes bottom sheet). */}
      <div
        style={{
          display: "flex",
          alignItems: "flex-start",
          gap: openEntry && !isMobile ? 24 : 0,
        }}
      >
        {/* Grid column — flex:1 so it absorbs remaining width when the
         *  inspector is closed (single-column) and shrinks when open. */}
        <div style={{ flex: 1, minWidth: 0 }}>
          <LayoutGroup id="component-grid">
            {Array.from(grouped.entries()).map(([cat, list]) => (
              <div
                key={cat}
                id={`cat-${slugifyCategory(cat)}`}
                style={{ marginBottom: 36, scrollMarginTop: "calc(var(--header-h) + 32px)" }}
              >
                <CategoryHeader label={cat} count={list.length} />
                <div
                  style={{
                    display: "grid",
                    // Tile minmax adapts to whether the inspector is docked.
                    // 240px when docked (more density), 280px otherwise.
                    gridTemplateColumns: `repeat(auto-fill, minmax(${
                      isMobile ? 200 : openEntry ? 240 : 280
                    }px, 1fr))`,
                    gap: 12,
                  }}
                >
                  {list.map((e) => (
                    <ComponentTile
                      key={e.slug + e.variant_id}
                      entry={e}
                      open={openSlug === e.slug}
                      onToggle={() =>
                        setOpenSlug((v) => (v === e.slug ? null : e.slug))
                      }
                    />
                  ))}
                </div>
              </div>
            ))}
          </LayoutGroup>

          {total === 0 && (
            <div
              style={{
                padding: "48px 0",
                textAlign: "center",
                color: "var(--text-3)",
                fontSize: 14,
              }}
            >
              No components match &ldquo;{query}&rdquo;
            </div>
          )}
        </div>

        {/* Desktop: sticky inspector docks to the right of the grid. Width
         *  fixed at 380px; sticky so it stays in view as user scrolls the
         *  grid below.
         *  Mobile: switches to a bottom sheet (handled below). */}
        {!isMobile && (
          <AnimatePresence>
            {openEntry && (
              <motion.aside
                key={openEntry.slug}
                initial={{ opacity: 0, x: 16 }}
                animate={{ opacity: 1, x: 0 }}
                exit={{ opacity: 0, x: 16 }}
                transition={{ type: "spring", stiffness: 320, damping: 28 }}
                style={{
                  flex: "0 0 380px",
                  position: "sticky",
                  top: "calc(var(--header-h) + 24px)",
                  maxHeight: "calc(100vh - var(--header-h) - 48px)",
                  overflow: "hidden",
                  background: "var(--bg-surface)",
                  border: "1px solid var(--border)",
                  borderRadius: 12,
                  display: "flex",
                  flexDirection: "column",
                  boxShadow: "var(--elev-shadow-2)",
                }}
              >
                <InspectorBody entry={openEntry} onClose={() => setOpenSlug(null)} />
              </motion.aside>
            )}
          </AnimatePresence>
        )}
      </div>

      {/* Mobile bottom sheet for the inspector — same body, different shell. */}
      {isMobile && (
        <AnimatePresence>
          {openEntry && (
            <motion.div
              key="mobile-sheet-backdrop"
              initial={{ opacity: 0 }}
              animate={{ opacity: 1 }}
              exit={{ opacity: 0 }}
              transition={{ duration: 0.18 }}
              onClick={() => setOpenSlug(null)}
              style={{
                position: "fixed",
                inset: 0,
                background: "var(--scrim)",
                zIndex: 80,
              }}
            >
              <motion.div
                initial={{ y: "100%" }}
                animate={{ y: 0 }}
                exit={{ y: "100%" }}
                transition={{ type: "spring", stiffness: 320, damping: 30 }}
                onClick={(e) => e.stopPropagation()}
                style={{
                  position: "absolute",
                  bottom: 0,
                  left: 0,
                  right: 0,
                  maxHeight: "85vh",
                  background: "var(--bg-page)",
                  borderTopLeftRadius: 16,
                  borderTopRightRadius: 16,
                  border: "1px solid var(--border)",
                  borderBottom: "none",
                  display: "flex",
                  flexDirection: "column",
                  overflow: "hidden",
                }}
              >
                {/* Drag handle */}
                <div style={{ display: "flex", justifyContent: "center", padding: "10px 0 0" }}>
                  <div
                    style={{
                      width: 40,
                      height: 4,
                      background: "var(--border-strong)",
                      borderRadius: 2,
                    }}
                  />
                </div>
                <InspectorBody entry={openEntry} onClose={() => setOpenSlug(null)} />
              </motion.div>
            </motion.div>
          )}
        </AnimatePresence>
      )}
    </div>
  );
}

/* ── Tile ───────────────────────────────────────────────────────────────── */

function ComponentTile({
  entry,
  open,
  onToggle,
}: {
  entry: IconEntry;
  open: boolean;
  onToggle: () => void;
}) {
  const variantCount = entry.variants?.length ?? 0;
  return (
    <motion.button
      layout
      onClick={onToggle}
      whileHover={{ y: -2 }}
      transition={{ type: "spring", stiffness: 320, damping: 26 }}
      data-component={entry.slug}
      data-open={open}
      style={{
        display: "flex",
        flexDirection: "column",
        gap: 10,
        padding: 14,
        background: open ? "var(--bg-surface-2)" : "var(--bg-surface)",
        border: `1px solid ${open ? "var(--accent)" : "var(--border)"}`,
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
        <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", gap: 8 }}>
          <span
            style={{
              fontSize: 13,
              fontWeight: 600,
              color: "var(--text-1)",
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
          {variantCount === 0 && " · single"}
        </span>
      </div>
    </motion.button>
  );
}

/* ── Inspector Body — shared between desktop dock + mobile sheet ────────── */

function InspectorBody({ entry, onClose }: { entry: IconEntry; onClose: () => void }) {
  const variants = entry.variants ?? [];
  const propertyKeys = useMemo(() => {
    const set = new Set<string>();
    for (const v of variants) for (const p of v.properties) set.add(p.name);
    return Array.from(set);
  }, [variants]);
  const matrix = useMemo(() => axisMatrix(entry), [entry]);
  const defaultVariant = useMemo(() => defaultVariantOf(entry), [entry]);
  const hasAxes = matrix.axes.length > 0 || matrix.scalars.length > 0;
  const hasLayout = !!defaultVariant?.layout?.mode && defaultVariant.layout.mode !== "NONE";

  return (
    <>
      {/* Sticky header inside the inspector — name, slug/meta, close. The
       *  sticky positioning keeps the title in view while the body scrolls
       *  through Description → Axes → Layout → Variants. */}
      <div
        style={{
          padding: "16px 18px",
          borderBottom: "1px solid var(--border)",
          display: "flex",
          alignItems: "flex-start",
          gap: 12,
          flexShrink: 0,
          position: "sticky",
          top: 0,
          background: "var(--bg-surface)",
          zIndex: 1,
        }}
      >
        <div style={{ minWidth: 0, flex: 1 }}>
          <div
            style={{
              fontSize: 15,
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
              wordBreak: "break-all",
            }}
          >
            {entry.slug}
          </div>
          <div
            style={{
              fontSize: 11,
              color: "var(--text-2)",
              marginTop: 8,
              display: "flex",
              flexWrap: "wrap",
              gap: 6,
            }}
          >
            <Pill>{entry.category}</Pill>
            <Pill>
              {variants.length} variant{variants.length === 1 ? "" : "s"}
            </Pill>
            {propertyKeys.length > 0 && (
              <Pill>{propertyKeys.join(" · ")}</Pill>
            )}
          </div>
          {/* Detail-page deep link — promoted into the inspector header
           *  so designers always have a path from quick-browse → full
           *  spec without going back to the top nav. */}
          <Link
            href={`/components/${entry.slug}`}
            style={{
              display: "inline-flex",
              alignItems: "center",
              gap: 4,
              marginTop: 10,
              fontSize: 11,
              fontWeight: 600,
              color: "var(--accent)",
              textDecoration: "none",
              fontFamily: "var(--font-mono)",
            }}
          >
            Open detail →
          </Link>
        </div>
        <button
          onClick={onClose}
          aria-label="Close inspector (Esc)"
          title="Close (Esc)"
          style={{
            background: "var(--bg-surface-2)",
            border: "1px solid var(--border)",
            cursor: "pointer",
            color: "var(--text-2)",
            padding: 6,
            borderRadius: 6,
            display: "flex",
            flexShrink: 0,
          }}
        >
          <svg width="14" height="14" viewBox="0 0 16 16" fill="none">
            <path d="M4 4l8 8M12 4l-8 8" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" />
          </svg>
        </button>
      </div>

      {/* Body — Description, Variant Axes, Layout, then the variants list.
       *  Order matches what a designer asks first: "what is this", "what
       *  knobs does it have", "how is it laid out", "show me each one".
       *  Sections are present-only — empty data renders nothing rather
       *  than a "no description yet" placeholder, keeping the panel terse. */}
      <div style={{ overflowY: "auto", flex: 1 }}>
        {entry.description && (
          <DescriptionSection
            description={entry.description}
            docLinks={entry.doc_links ?? []}
          />
        )}
        {hasAxes && <AxesSection matrix={matrix} />}
        {hasLayout && defaultVariant?.layout && (
          <LayoutSection layout={defaultVariant.layout} />
        )}
        {variants.length === 0 ? (
          <EmptyVariants slug={entry.slug} />
        ) : (
          <>
            <SectionHeader label="Variants" count={variants.length} />
            <div style={{ padding: "0 14px 18px", display: "flex", flexDirection: "column", gap: 10 }}>
              {variants.map((v) => (
                <VariantRow key={v.variant_id} variant={v} />
              ))}
            </div>
          </>
        )}
      </div>
    </>
  );
}

function SectionHeader({ label, count }: { label: string; count?: number }) {
  return (
    <div
      style={{
        display: "flex",
        alignItems: "center",
        gap: 8,
        padding: "16px 14px 8px",
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
      <div style={{ flex: 1, height: 1, background: "var(--border)" }} />
    </div>
  );
}

function DescriptionSection({
  description,
  docLinks,
}: {
  description: string;
  docLinks: string[];
}) {
  return (
    <div>
      <SectionHeader label="Description" />
      <div
        style={{
          padding: "0 14px 4px",
          fontSize: 13,
          lineHeight: 1.5,
          color: "var(--text-2)",
          whiteSpace: "pre-wrap",
        }}
      >
        {description}
      </div>
      {docLinks.length > 0 && (
        <div style={{ padding: "8px 14px 4px", display: "flex", flexWrap: "wrap", gap: 6 }}>
          {docLinks.map((href, i) => (
            <a
              key={href + i}
              href={href}
              target="_blank"
              rel="noreferrer"
              style={{
                fontSize: 11,
                fontFamily: "var(--font-mono)",
                color: "var(--accent)",
                textDecoration: "none",
                background: "color-mix(in srgb, var(--accent) 10%, transparent)",
                padding: "2px 8px",
                borderRadius: 4,
              }}
            >
              docs ↗
            </a>
          ))}
        </div>
      )}
    </div>
  );
}

function AxesSection({ matrix }: { matrix: ReturnType<typeof axisMatrix> }) {
  return (
    <div>
      <SectionHeader label="Variant axes" count={matrix.axes.length + matrix.scalars.length} />
      <div style={{ padding: "0 14px", display: "flex", flexDirection: "column", gap: 8 }}>
        {matrix.axes.map((axis) => (
          <div
            key={axis.name}
            style={{
              display: "flex",
              alignItems: "flex-start",
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
                color: "var(--text-1)",
                fontWeight: 600,
                minWidth: 60,
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
                  title={v === axis.default ? "Default value" : undefined}
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
          color: "var(--text-1)",
          fontWeight: 600,
          flex: 1,
          minWidth: 0,
          overflow: "hidden",
          textOverflow: "ellipsis",
          whiteSpace: "nowrap",
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
      {prop.default_value !== undefined && prop.default_value !== null && (
        <span
          style={{
            fontFamily: "var(--font-mono)",
            fontSize: 10,
            color: "var(--text-2)",
            maxWidth: 100,
            overflow: "hidden",
            textOverflow: "ellipsis",
            whiteSpace: "nowrap",
          }}
          title={String(prop.default_value)}
        >
          {String(prop.default_value)}
        </span>
      )}
    </div>
  );
}

function LayoutSection({ layout }: { layout: LayoutInfo }) {
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
    <div>
      <SectionHeader label="Layout" />
      <div
        style={{
          margin: "0 14px 4px",
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
    </div>
  );
}

function Pill({ children }: { children: React.ReactNode }) {
  return (
    <span
      style={{
        fontFamily: "var(--font-mono)",
        fontSize: 10,
        padding: "2px 7px",
        background: "var(--bg-surface-2)",
        border: "1px solid var(--border)",
        borderRadius: 4,
        color: "var(--text-2)",
        whiteSpace: "nowrap",
      }}
    >
      {children}
    </span>
  );
}

function EmptyVariants({ slug }: { slug: string }) {
  return (
    <div style={{ padding: "16px 14px" }}>
      <DataGapPreview
        diagnosis={
          <>
            No variants extracted for{" "}
            <strong style={{ color: "var(--text-1)", fontFamily: "var(--font-mono)" }}>
              {slug}
            </strong>{" "}
            yet. Variants are each property combination of a Glyph component
            set (state × size × intent).
          </>
        }
        unlock={
          <>
            Run the variants extractor against the Glyph file. Output writes to{" "}
            <code style={{ fontFamily: "var(--font-mono)" }}>
              public/icons/glyph/variants/
            </code>{" "}
            and the manifest auto-updates.
          </>
        }
        command="go run ./services/ds-service/cmd/variants"
        preview={
          <div style={{ display: "flex", gap: 6, opacity: 0.6 }}>
            {[0, 1, 2].map((i) => (
              <motion.div
                key={i}
                initial={{ opacity: 0, scale: 0.94 }}
                animate={{ opacity: 1, scale: 1 }}
                transition={{ delay: i * 0.08, duration: 0.4 }}
                style={{
                  width: 50,
                  height: 50,
                  borderRadius: 8,
                  background: "var(--bg-surface-2)",
                  border: "1px dashed var(--border-strong)",
                }}
              />
            ))}
          </div>
        }
      />
    </div>
  );
}

/* ── Variant Row (vertical, one per panel row) ──────────────────────────── */

function VariantRow({ variant }: { variant: VariantEntry }) {
  const url = `/icons/glyph/${variant.file.replace(/^variants\//, "variants/")}`;
  const boundCount =
    Object.keys(variant.bound_variables ?? {}).length +
    (variant.fills ?? []).filter((f) => f.bound_variable_id).length +
    (variant.effects ?? []).filter((e) => e.bound_variable_id).length;
  const axisEntries = variant.axis_values
    ? Object.entries(variant.axis_values)
    : variant.properties.map((p) => [p.name, p.value] as [string, string]);
  return (
    <div
      // C17 — anchor the DEFAULT badge to the outer variant card's top-right
      // corner rather than the inner image preview frame. Previously the badge
      // floated inside the image well, which made it look attached to the
      // *image* (and clip behind tall renderings) instead of being a marker
      // on the variant card as a whole.
      style={{
        display: "flex",
        flexDirection: "column",
        gap: 8,
        padding: 10,
        background: "var(--bg-surface-2)",
        border: `1px solid ${variant.is_default ? "var(--accent)" : "var(--border)"}`,
        borderRadius: 8,
        position: "relative",
      }}
    >
      {variant.is_default && (
        <span
          title="Default variant"
          style={{
            position: "absolute",
            top: 6,
            right: 6,
            fontSize: 9,
            fontWeight: 700,
            color: "var(--accent)",
            background: "color-mix(in srgb, var(--accent) 14%, transparent)",
            padding: "2px 6px",
            borderRadius: 4,
            fontFamily: "var(--font-mono)",
            letterSpacing: "0.04em",
            zIndex: 1,
          }}
        >
          ★ DEFAULT
        </span>
      )}
      <div
        style={{
          minHeight: 80,
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          background: "var(--bg-surface)",
          border: "1px solid var(--border)",
          borderRadius: 6,
          padding: 8,
        }}
      >
        <img
          src={url}
          alt={variant.name}
          loading="lazy"
          style={{ maxWidth: "100%", maxHeight: 110, objectFit: "contain" }}
        />
      </div>
      <div style={{ display: "flex", flexWrap: "wrap", gap: 4 }}>
        {axisEntries.length === 0 ? (
          <PropChip k="default" v="" />
        ) : (
          axisEntries.map(([k, v]) => <PropChip key={k} k={k} v={v} />)
        )}
      </div>
      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          gap: 8,
          fontSize: 10,
          fontFamily: "var(--font-mono)",
          color: "var(--text-3)",
        }}
      >
        <span>
          {variant.width} × {variant.height}
        </span>
        {boundCount > 0 && (
          <span
            title={`${boundCount} property${boundCount === 1 ? "" : "ies"} bound to a Figma Variable`}
            style={{
              background: "color-mix(in srgb, var(--success) 14%, transparent)",
              color: "var(--success)",
              padding: "2px 6px",
              borderRadius: 4,
              fontWeight: 600,
            }}
          >
            🎯 {boundCount} bound
          </span>
        )}
      </div>
    </div>
  );
}

function PropChip({ k, v }: { k: string; v: string }) {
  return (
    <span
      style={{
        fontFamily: "var(--font-mono)",
        fontSize: 10,
        padding: "2px 7px",
        background: "var(--bg-surface)",
        border: "1px solid var(--border)",
        borderRadius: 4,
        color: "var(--text-2)",
      }}
    >
      <span style={{ color: "var(--text-3)" }}>{k}</span>
      {v && <span style={{ color: "var(--text-1)", marginLeft: 4 }}>{v}</span>}
    </span>
  );
}

/* ── Search bar ─────────────────────────────────────────────────────────── */

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
        padding: "10px 14px",
        marginBottom: 32,
      }}
    >
      <svg width="14" height="14" viewBox="0 0 16 16" fill="none" style={{ color: "var(--text-3)" }}>
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
          fontSize: 14,
          color: "var(--text-1)",
          fontFamily: "var(--font-sans)",
        }}
      />
      <span style={{ fontSize: 11, color: "var(--text-3)", fontFamily: "var(--font-mono)" }}>
        {value ? `${matches} of ${total}` : `${total} components`}
      </span>
    </div>
  );
}

function CategoryHeader({ label, count }: { label: string; count: number }) {
  return (
    <div style={{ display: "flex", alignItems: "center", gap: 10, marginBottom: 14 }}>
      <span
        style={{
          fontSize: 12,
          fontWeight: 600,
          color: "var(--text-2)",
          textTransform: "uppercase",
          letterSpacing: "0.07em",
        }}
      >
        {label}
      </span>
      <span
        style={{
          fontSize: 10,
          fontWeight: 600,
          color: "var(--text-3)",
          background: "var(--bg-surface-2)",
          padding: "1px 6px",
          borderRadius: 4,
          fontFamily: "var(--font-mono)",
        }}
      >
        {count}
      </span>
      <div style={{ flex: 1, height: 1, background: "var(--border)" }} />
    </div>
  );
}
