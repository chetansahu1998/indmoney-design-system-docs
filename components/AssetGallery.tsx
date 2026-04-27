"use client";
import { useMemo, useState } from "react";
import { motion, AnimatePresence } from "framer-motion";
import { iconURL, slugifyCategory, type IconEntry } from "@/lib/icons/manifest";
import { useIsMobile } from "@/lib/use-mobile";

/**
 * Visual gallery used by /components, /illustrations, /logos. Renders each
 * entry at its native dimensions (capped) via <img>, preserving original
 * colors. No code samples, no theming — just a searchable preview wall.
 */
type Layout = "wide" | "square";

export default function AssetGallery({
  title,
  subtitle,
  entries,
  layout = "square",
  emptyHint,
}: {
  title: string;
  subtitle: string;
  entries: IconEntry[];
  layout?: Layout;
  emptyHint?: string;
}) {
  const [query, setQuery] = useState("");
  const isMobile = useIsMobile();

  const grouped = useMemo(() => {
    const map = new Map<string, IconEntry[]>();
    for (const e of entries) {
      const cat = e.category || "uncategorized";
      if (!map.has(cat)) map.set(cat, []);
      map.get(cat)!.push(e);
    }
    for (const list of map.values()) list.sort((a, b) => a.name.localeCompare(b.name));
    return map;
  }, [entries]);

  const q = query.toLowerCase().trim();
  const filtered = useMemo(() => {
    if (!q) return entries;
    return entries.filter(
      (e) => e.name.toLowerCase().includes(q) || e.slug.includes(q),
    );
  }, [entries, q]);

  const tileMin = layout === "wide"
    ? (isMobile ? 200 : 280)
    : (isMobile ? 78 : 104);

  return (
    <>
      <div style={{ borderBottom: "1px solid var(--border)", paddingBottom: 32, marginBottom: 32 }}>
        <h1
          style={{
            fontSize: isMobile ? 32 : 48,
            fontWeight: 700,
            letterSpacing: "-1.5px",
            color: "var(--text-1)",
            marginBottom: 12,
            lineHeight: 1.05,
          }}
        >
          {title}
        </h1>
        <p style={{ fontSize: 15, color: "var(--text-2)", lineHeight: 1.65, maxWidth: 640 }}>
          {subtitle}
        </p>
        <p style={{ fontSize: 12, color: "var(--text-3)", fontFamily: "var(--font-mono)", marginTop: 8 }}>
          {entries.length} entries · source: glyph
        </p>
      </div>

      <SearchBar value={query} onChange={setQuery} total={entries.length} matches={filtered.length} />

      {q ? (
        <Grid entries={filtered} layout={layout} tileMin={tileMin} emptyHint={emptyHint} />
      ) : (
        Array.from(grouped.entries()).map(([cat, list]) => (
          <div
            key={cat}
            id={`cat-${slugifyCategory(cat)}`}
            style={{ marginBottom: 36, scrollMarginTop: "calc(var(--header-h) + 32px)" }}
          >
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
                {cat}
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
                {list.length}
              </span>
              <div style={{ flex: 1, height: 1, background: "var(--border)" }} />
            </div>
            <Grid entries={list} layout={layout} tileMin={tileMin} />
          </div>
        ))
      )}
    </>
  );
}

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
      <svg width="14" height="14" viewBox="0 0 16 16" fill="none" style={{ color: "var(--text-3)", flexShrink: 0 }}>
        <circle cx="7" cy="7" r="4.5" stroke="currentColor" strokeWidth="1.4" />
        <path d="M10.5 10.5L13 13" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
      </svg>
      <input
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder="Search…"
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
      <AnimatePresence>
        {value && (
          <motion.button
            initial={{ opacity: 0 }}
            animate={{ opacity: 1 }}
            exit={{ opacity: 0 }}
            onClick={() => onChange("")}
            style={{
              background: "none",
              border: "none",
              cursor: "pointer",
              color: "var(--text-3)",
              padding: 0,
              display: "flex",
            }}
            aria-label="Clear"
          >
            <svg width="14" height="14" viewBox="0 0 16 16" fill="none">
              <path d="M4 4l8 8M12 4l-8 8" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
            </svg>
          </motion.button>
        )}
      </AnimatePresence>
      <span style={{ fontSize: 11, color: "var(--text-3)", fontFamily: "var(--font-mono)" }}>
        {value ? `${matches} of ${total}` : `${total} total`}
      </span>
    </div>
  );
}

function Grid({
  entries,
  layout,
  tileMin,
  emptyHint,
}: {
  entries: IconEntry[];
  layout: Layout;
  tileMin: number;
  emptyHint?: string;
}) {
  if (entries.length === 0) {
    return (
      <div style={{ padding: "48px 0", textAlign: "center", color: "var(--text-3)", fontSize: 14 }}>
        {emptyHint ?? "No matches"}
      </div>
    );
  }
  return (
    <div
      style={{
        display: "grid",
        gridTemplateColumns: `repeat(auto-fill, minmax(${tileMin}px, 1fr))`,
        gap: 10,
      }}
    >
      {entries.map((e) => (
        <Tile key={e.slug + e.variant_id} entry={e} layout={layout} />
      ))}
    </div>
  );
}

function Tile({ entry, layout }: { entry: IconEntry; layout: Layout }) {
  const aspect = layout === "wide" ? entry.height / Math.max(entry.width, 1) : 1;
  const previewHeight = layout === "wide" ? Math.min(120, Math.max(48, aspect * 280)) : 80;

  return (
    <div
      data-entry={entry.slug}
      title={`${entry.name}  ${entry.width}×${entry.height}`}
      style={{
        display: "flex",
        flexDirection: "column",
        gap: 8,
        padding: 12,
        background: "var(--bg-surface)",
        border: "1px solid var(--border)",
        borderRadius: 8,
        transition: "transform 140ms ease, box-shadow 140ms ease",
      }}
      onMouseEnter={(ev) => {
        const el = ev.currentTarget as HTMLElement;
        el.style.transform = "translateY(-2px)";
        el.style.boxShadow = "0 6px 18px rgba(0,0,0,0.10)";
      }}
      onMouseLeave={(ev) => {
        const el = ev.currentTarget as HTMLElement;
        el.style.transform = "";
        el.style.boxShadow = "";
      }}
    >
      <div
        style={{
          height: previewHeight,
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          background:
            "repeating-linear-gradient(45deg, transparent 0 6px, color-mix(in srgb, var(--text-3) 5%, transparent) 6px 7px)",
          borderRadius: 4,
          padding: 6,
        }}
      >
        <img
          src={iconURL(entry)}
          alt={entry.name}
          loading="lazy"
          style={{
            maxWidth: "100%",
            maxHeight: previewHeight - 12,
            objectFit: "contain",
            color: "var(--text-1)",
          }}
        />
      </div>
      <div style={{ display: "flex", flexDirection: "column", gap: 2, minWidth: 0 }}>
        <span
          style={{
            fontSize: 11,
            fontWeight: 600,
            color: "var(--text-1)",
            overflow: "hidden",
            textOverflow: "ellipsis",
            whiteSpace: "nowrap",
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
          {entry.width} × {entry.height}
        </span>
      </div>
    </div>
  );
}
