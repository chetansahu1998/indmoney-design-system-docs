"use client";
import { useMemo, useState } from "react";
import { motion, AnimatePresence, LayoutGroup } from "framer-motion";
import { iconURL, type IconEntry, type VariantEntry } from "@/lib/icons/manifest";
import { useIsMobile } from "@/lib/use-mobile";

/**
 * Component gallery + inline detail inspector. Click a tile → expands in
 * place into a horizontal variant rail with property chips, mirroring
 * Zeplin's component inspector.
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

  return (
    <div>
      <SearchBar value={query} onChange={setQuery} total={entries.length} matches={total} />

      <LayoutGroup>
        {Array.from(grouped.entries()).map(([cat, list]) => (
          <div key={cat} style={{ marginBottom: 36 }}>
            <CategoryHeader label={cat} count={list.length} />
            <div
              style={{
                display: "grid",
                gridTemplateColumns: `repeat(auto-fill, minmax(${isMobile ? 220 : 280}px, 1fr))`,
                gap: 12,
              }}
            >
              {list.map((e) => (
                <ComponentTile
                  key={e.slug + e.variant_id}
                  entry={e}
                  open={openSlug === e.slug}
                  onToggle={() => setOpenSlug((v) => (v === e.slug ? null : e.slug))}
                />
              ))}
            </div>

            <AnimatePresence mode="wait">
              {(() => {
                const open = list.find((e) => e.slug === openSlug);
                if (!open) return null;
                return <DetailPanel key={open.slug} entry={open} onClose={() => setOpenSlug(null)} />;
              })()}
            </AnimatePresence>
          </div>
        ))}
      </LayoutGroup>

      {total === 0 && (
        <div style={{ padding: "48px 0", textAlign: "center", color: "var(--text-3)", fontSize: 14 }}>
          No components match &ldquo;{query}&rdquo;
        </div>
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
              {variantCount} variants
            </span>
          )}
        </div>
        <span style={{ fontSize: 11, color: "var(--text-3)", fontFamily: "var(--font-mono)" }}>
          {entry.width} × {entry.height}
          {variantCount === 0 && " · single variant"}
        </span>
      </div>
    </motion.button>
  );
}

/* ── Detail Panel (click-to-expand) ─────────────────────────────────────── */

function DetailPanel({ entry, onClose }: { entry: IconEntry; onClose: () => void }) {
  const variants = entry.variants ?? [];
  const propertyKeys = useMemo(() => {
    const set = new Set<string>();
    for (const v of variants) for (const p of v.properties) set.add(p.name);
    return Array.from(set);
  }, [variants]);

  return (
    <motion.div
      layout
      initial={{ opacity: 0, height: 0, marginTop: 0 }}
      animate={{ opacity: 1, height: "auto", marginTop: 16 }}
      exit={{ opacity: 0, height: 0, marginTop: 0 }}
      transition={{ duration: 0.3, ease: [0.33, 1, 0.68, 1] }}
      style={{
        overflow: "hidden",
        background: "var(--bg-surface)",
        border: "1px solid var(--border)",
        borderRadius: 12,
      }}
    >
      <div
        style={{
          padding: "16px 20px",
          borderBottom: "1px solid var(--border)",
          display: "flex",
          alignItems: "center",
          gap: 16,
          justifyContent: "space-between",
        }}
      >
        <div style={{ minWidth: 0 }}>
          <div style={{ fontSize: 16, fontWeight: 600, letterSpacing: "-0.2px", color: "var(--text-1)" }}>
            {entry.name}
          </div>
          <div style={{ fontSize: 11, color: "var(--text-3)", fontFamily: "var(--font-mono)", marginTop: 4 }}>
            {entry.slug} · {entry.category} · {variants.length} variant{variants.length === 1 ? "" : "s"}
            {propertyKeys.length > 0 && ` · ${propertyKeys.join(" · ")}`}
          </div>
        </div>
        <button
          onClick={onClose}
          aria-label="Close"
          style={{
            background: "none",
            border: "none",
            cursor: "pointer",
            color: "var(--text-3)",
            padding: 6,
            display: "flex",
          }}
        >
          <svg width="18" height="18" viewBox="0 0 16 16" fill="none">
            <path d="M4 4l8 8M12 4l-8 8" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" />
          </svg>
        </button>
      </div>

      {variants.length === 0 ? (
        <EmptyVariants slug={entry.slug} />
      ) : (
        <VariantRail variants={variants} />
      )}
    </motion.div>
  );
}

function EmptyVariants({ slug }: { slug: string }) {
  return (
    <div style={{ padding: "32px 24px", display: "flex", flexDirection: "column", gap: 14 }}>
      <p style={{ fontSize: 13, color: "var(--text-2)", lineHeight: 1.6, margin: 0 }}>
        Variants haven&apos;t been extracted for this component yet. Run the variants pipeline to
        populate the rail with each variable combination (state, size, intent, etc.).
      </p>
      <pre
        style={{
          margin: 0,
          padding: 12,
          background: "var(--bg-surface-2)",
          borderRadius: 6,
          fontFamily: "var(--font-mono)",
          fontSize: 12,
          color: "var(--text-1)",
          overflowX: "auto",
          border: "1px solid var(--border)",
        }}
      >{`go run ./services/ds-service/cmd/variants
# scoped to this component:
go run ./services/ds-service/cmd/variants --max 1`}</pre>
      <p style={{ fontSize: 11, color: "var(--text-3)", fontFamily: "var(--font-mono)", margin: 0 }}>
        Slug: {slug}
      </p>
    </div>
  );
}

function VariantRail({ variants }: { variants: VariantEntry[] }) {
  return (
    <div
      style={{
        padding: "20px 8px 24px",
        overflowX: "auto",
        WebkitOverflowScrolling: "touch",
      }}
    >
      <div style={{ display: "flex", gap: 14, padding: "0 12px" }}>
        {variants.map((v) => (
          <VariantCard key={v.variant_id} variant={v} />
        ))}
      </div>
    </div>
  );
}

function VariantCard({ variant }: { variant: VariantEntry }) {
  const url = `/icons/glyph/${variant.file.replace(/^variants\//, "variants/")}`;
  return (
    <div
      style={{
        flex: "0 0 auto",
        width: Math.min(280, Math.max(140, variant.width / 1.3)),
        display: "flex",
        flexDirection: "column",
        gap: 10,
        padding: 12,
        background: "var(--bg-surface-2)",
        border: "1px solid var(--border)",
        borderRadius: 8,
      }}
    >
      <div
        style={{
          minHeight: 96,
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
          style={{ maxWidth: "100%", maxHeight: 120, objectFit: "contain" }}
        />
      </div>
      <div style={{ display: "flex", flexWrap: "wrap", gap: 4 }}>
        {variant.properties.length === 0 ? (
          <PropChip k="default" v="" />
        ) : (
          variant.properties.map((p) => <PropChip key={p.name} k={p.name} v={p.value} />)
        )}
      </div>
      <div
        style={{
          fontSize: 10,
          fontFamily: "var(--font-mono)",
          color: "var(--text-3)",
          display: "flex",
          gap: 8,
        }}
      >
        <span>{variant.width} × {variant.height}</span>
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
