"use client";
import { useMemo, useState } from "react";
import { motion, AnimatePresence } from "framer-motion";
import SectionHeading from "@/components/ui/SectionHeading";
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip";
import { fadeUp } from "@/lib/motion-variants";
import { useIsMobile } from "@/lib/use-mobile";
import {
  iconURL,
  iconsByKind,
  iconsByCategory,
  type IconEntry,
} from "@/lib/icons/manifest";

// Category labels (lowercase keys → display titles).
const CATEGORY_LABELS: Record<string, string> = {
  ui: "UI",
  "2D": "2D illustrations",
  "3D": "3D illustrations",
  cold: "Cold",
  "filled icons": "Filled",
  icon: "Generic",
  logo: "Logos",
  logos: "Logos",
  profilecard: "Profile card",
  wallet: "Wallet",
  bank: "Bank",
  nvidia: "NVIDIA",
  uncategorized: "Other",
};

// Default page size; "Load more" extends by INCREMENT.
const INITIAL_PAGE = 120;
const INCREMENT = 240;

/* ── Icon tile (CSS-mask renderer) ──────────────────────────────────────────
 * Uses CSS mask-image so the browser fetches+caches each SVG once and recolors
 * via background-color. Avoids 800+ inline-SVG reconciliations on theme flips.
 */
function IconTile({ icon, onSelect }: { icon: IconEntry; onSelect: () => void }) {
  return (
    <TooltipProvider delayDuration={120}>
      <Tooltip>
        <TooltipTrigger asChild>
          <button
            onClick={onSelect}
            data-icon={icon.slug}
            data-icon-source={icon.source ?? "glyph"}
            style={{
              display: "flex",
              flexDirection: "column",
              alignItems: "center",
              gap: 8,
              padding: "14px 8px 10px",
              background: "var(--bg-surface)",
              border: "1px solid var(--border)",
              borderRadius: 8,
              cursor: "pointer",
              width: "100%",
              transition: "transform 140ms ease, box-shadow 140ms ease, background 140ms ease",
            }}
            onMouseEnter={(e) => {
              (e.currentTarget as HTMLButtonElement).style.transform = "translateY(-2px)";
              (e.currentTarget as HTMLButtonElement).style.boxShadow = "0 6px 18px rgba(0,0,0,0.10)";
            }}
            onMouseLeave={(e) => {
              (e.currentTarget as HTMLButtonElement).style.transform = "";
              (e.currentTarget as HTMLButtonElement).style.boxShadow = "";
            }}
          >
            <span
              aria-hidden
              style={{
                width: 24,
                height: 24,
                display: "block",
                backgroundColor: "var(--text-1)",
                WebkitMask: `url(${iconURL(icon)}) center / contain no-repeat`,
                mask: `url(${iconURL(icon)}) center / contain no-repeat`,
              }}
            />
            <span
              style={{
                fontSize: 9,
                color: "var(--text-3)",
                overflow: "hidden",
                textOverflow: "ellipsis",
                whiteSpace: "nowrap",
                maxWidth: "100%",
                textAlign: "center",
                fontFamily: "var(--font-mono)",
              }}
            >
              {icon.slug}
            </span>
          </button>
        </TooltipTrigger>
        <TooltipContent
          style={{
            background: "var(--bg-surface-2)",
            border: "1px solid var(--border)",
            color: "var(--text-1)",
            fontSize: 11,
            fontFamily: "var(--font-mono)",
            borderRadius: 6,
            padding: "4px 8px",
          }}
        >
          {icon.name} · click to copy SVG
        </TooltipContent>
      </Tooltip>
    </TooltipProvider>
  );
}

/* ── Detail modal: fetches and shows the actual SVG source for copy. ─────── */
function IconDetail({ icon, onClose }: { icon: IconEntry; onClose: () => void }) {
  const [svg, setSvg] = useState<string>("");
  const [copied, setCopied] = useState(false);
  const url = iconURL(icon);

  useMemo(() => {
    fetch(url)
      .then((r) => r.text())
      .then(setSvg)
      .catch(() => setSvg(""));
  }, [url]);

  const copy = (value: string) => {
    navigator.clipboard.writeText(value).catch(() => {});
    setCopied(true);
    setTimeout(() => setCopied(false), 1300);
  };

  return (
    <motion.div
      initial={{ opacity: 0 }}
      animate={{ opacity: 1 }}
      exit={{ opacity: 0 }}
      transition={{ duration: 0.15 }}
      onClick={onClose}
      style={{
        position: "fixed",
        inset: 0,
        background: "rgba(0,0,0,0.45)",
        zIndex: 100,
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        padding: 24,
      }}
    >
      <motion.div
        initial={{ y: 12, opacity: 0 }}
        animate={{ y: 0, opacity: 1 }}
        exit={{ y: 6, opacity: 0 }}
        transition={{ type: "spring", stiffness: 320, damping: 26 }}
        onClick={(e) => e.stopPropagation()}
        style={{
          width: "min(520px, 100%)",
          background: "var(--bg-surface)",
          border: "1px solid var(--border)",
          borderRadius: 12,
          padding: 24,
          color: "var(--text-1)",
          display: "flex",
          flexDirection: "column",
          gap: 18,
        }}
      >
        <div style={{ display: "flex", alignItems: "center", gap: 16 }}>
          <span
            style={{
              width: 56,
              height: 56,
              display: "block",
              backgroundColor: "var(--text-1)",
              WebkitMask: `url(${url}) center / contain no-repeat`,
              mask: `url(${url}) center / contain no-repeat`,
              flexShrink: 0,
            }}
          />
          <div style={{ minWidth: 0, flex: 1 }}>
            <div style={{ fontSize: 16, fontWeight: 600, letterSpacing: "-0.2px" }}>
              {icon.name}
            </div>
            <div style={{ fontSize: 11, color: "var(--text-3)", fontFamily: "var(--font-mono)", marginTop: 2 }}>
              {icon.slug} · {icon.category} · {icon.source ?? "glyph"}
            </div>
          </div>
          <button
            onClick={onClose}
            style={{ background: "none", border: "none", color: "var(--text-3)", cursor: "pointer", padding: 4 }}
            aria-label="Close"
          >
            <svg width="18" height="18" viewBox="0 0 16 16" fill="none">
              <path d="M4 4l8 8M12 4l-8 8" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
            </svg>
          </button>
        </div>

        <div
          style={{
            background: "var(--bg-surface-2)",
            border: "1px solid var(--border)",
            borderRadius: 8,
            padding: 12,
            fontFamily: "var(--font-mono)",
            fontSize: 11,
            color: "var(--text-2)",
            maxHeight: 220,
            overflow: "auto",
            whiteSpace: "pre-wrap",
            wordBreak: "break-all",
          }}
        >
          {svg || "Loading…"}
        </div>

        <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
          <CopyButton label="Copy SVG" onClick={() => copy(svg)} disabled={!svg} highlight={copied} />
          <CopyButton label="Copy slug" onClick={() => copy(icon.slug)} />
          <CopyButton label="Copy URL" onClick={() => copy(url)} />
        </div>
      </motion.div>
    </motion.div>
  );
}

function CopyButton({
  label,
  onClick,
  disabled,
  highlight,
}: {
  label: string;
  onClick: () => void;
  disabled?: boolean;
  highlight?: boolean;
}) {
  return (
    <button
      onClick={onClick}
      disabled={disabled}
      style={{
        padding: "8px 12px",
        background: highlight ? "var(--accent)" : "var(--bg-surface-2)",
        color: highlight ? "#fff" : "var(--text-1)",
        border: "1px solid var(--border)",
        borderRadius: 6,
        fontSize: 12,
        fontFamily: "var(--font-mono)",
        cursor: disabled ? "not-allowed" : "pointer",
        opacity: disabled ? 0.5 : 1,
        transition: "background 120ms ease",
      }}
    >
      {highlight ? "Copied!" : label}
    </button>
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
      <svg width="14" height="14" viewBox="0 0 16 16" fill="none" style={{ color: "var(--text-3)", flexShrink: 0 }}>
        <circle cx="7" cy="7" r="4.5" stroke="currentColor" strokeWidth="1.4" />
        <path d="M10.5 10.5L13 13" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
      </svg>
      <input
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder="Filter Glyph icons by name or slug…"
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
            initial={{ opacity: 0, scale: 0.85 }}
            animate={{ opacity: 1, scale: 1 }}
            exit={{ opacity: 0, scale: 0.85 }}
            onClick={() => onChange("")}
            style={{
              background: "none",
              border: "none",
              cursor: "pointer",
              color: "var(--text-3)",
              display: "flex",
              alignItems: "center",
              padding: 0,
            }}
            aria-label="Clear"
          >
            <svg width="14" height="14" viewBox="0 0 16 16" fill="none">
              <path d="M4 4l8 8M12 4l-8 8" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
            </svg>
          </motion.button>
        )}
      </AnimatePresence>
      <span
        style={{
          fontSize: 11,
          color: "var(--text-3)",
          fontFamily: "var(--font-mono)",
          flexShrink: 0,
        }}
      >
        {value ? `${matches} of ${total}` : `${total} icons`}
      </span>
    </div>
  );
}

export default function IconographySection() {
  const [query, setQuery] = useState("");
  const [visible, setVisible] = useState(INITIAL_PAGE);
  const [active, setActive] = useState<IconEntry | null>(null);
  const isMobile = useIsMobile();

  const all = useMemo(() => iconsByKind("icon"), []);
  const grouped = useMemo(() => iconsByCategory("icon"), []);
  const cats = useMemo(() => Array.from(grouped.keys()), [grouped]);

  const q = query.toLowerCase().trim();
  const matches = useMemo(() => {
    if (!q) return all;
    return all.filter((i) => i.name.toLowerCase().includes(q) || i.slug.includes(q));
  }, [q, all]);

  const isSearching = q.length > 0;
  const flat = isSearching ? matches.slice(0, visible) : null;

  return (
    <section
      id="iconography"
      style={{ marginBottom: 80, scrollMarginTop: "calc(var(--header-h) + 32px)" }}
    >
      <SectionHeading id="iconography" title="Iconography" />

      <motion.p
        variants={fadeUp}
        initial="hidden"
        whileInView="visible"
        viewport={{ once: true }}
        style={{ fontSize: 16, color: "var(--text-2)", lineHeight: 1.65, maxWidth: 640, marginBottom: 16 }}
      >
        <strong style={{ color: "var(--text-1)" }}>{all.length} system icons</strong> across{" "}
        {cats.length} categories, filtered from the Glyph manifest (logos and illustrations live
        on their own pages). All icons use{" "}
        <code style={{ fontFamily: "var(--font-mono)", fontSize: 12, background: "var(--bg-surface)", padding: "1px 5px", borderRadius: 3, color: "var(--accent)" }}>
          currentColor
        </code>{" "}
        — they recolor with the theme automatically. Click any icon to copy its SVG.
      </motion.p>

      {/* Meta strip */}
      <motion.div
        variants={fadeUp}
        initial="hidden"
        whileInView="visible"
        viewport={{ once: true }}
        style={{ display: "flex", gap: 12, marginBottom: 28, flexWrap: "wrap" }}
      >
        {[
          { label: "Total icons",  value: String(all.length) },
          { label: "Categories",   value: String(cats.length) },
          { label: "Default size", value: "24 × 24" },
          { label: "Format",       value: "SVG" },
          { label: "Renderer",     value: "CSS mask" },
        ].map((s) => (
          <div
            key={s.label}
            style={{
              display: "flex",
              flexDirection: "column",
              gap: 2,
              padding: "10px 16px",
              background: "var(--bg-surface)",
              border: "1px solid var(--border)",
              borderRadius: 8,
            }}
          >
            <span
              style={{
                fontSize: 10,
                color: "var(--text-3)",
                textTransform: "uppercase",
                letterSpacing: "0.07em",
                fontWeight: 600,
              }}
            >
              {s.label}
            </span>
            <span style={{ fontSize: 14, fontWeight: 600, color: "var(--text-1)", fontFamily: "var(--font-mono)" }}>
              {s.value}
            </span>
          </div>
        ))}
      </motion.div>

      <SearchBar
        value={query}
        onChange={(v) => {
          setQuery(v);
          setVisible(INITIAL_PAGE);
        }}
        total={all.length}
        matches={matches.length}
      />

      {/* Search results: flat grid */}
      {isSearching && flat && (
        <>
          {flat.length === 0 ? (
            <div style={{ padding: "48px 0", textAlign: "center", color: "var(--text-3)", fontSize: 14 }}>
              No icons match &ldquo;{query}&rdquo;
            </div>
          ) : (
            <>
              <div
                style={{
                  display: "grid",
                  gridTemplateColumns: `repeat(auto-fill, minmax(${isMobile ? 78 : 92}px, 1fr))`,
                  gap: 8,
                }}
              >
                {flat.map((icon) => (
                  <IconTile key={icon.slug + icon.variant_id} icon={icon} onSelect={() => setActive(icon)} />
                ))}
              </div>
              {matches.length > visible && (
                <LoadMore
                  remaining={matches.length - visible}
                  onClick={() => setVisible((v) => v + INCREMENT)}
                />
              )}
            </>
          )}
        </>
      )}

      {/* Browse by category */}
      {!isSearching && cats.map((cat) => {
        const list = grouped.get(cat) ?? [];
        if (list.length === 0) return null;
        const label = CATEGORY_LABELS[cat.toLowerCase()] ?? cat;
        return (
          <div key={cat} style={{ marginBottom: 36 }}>
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
                {list.length}
              </span>
              <div style={{ flex: 1, height: 1, background: "var(--border)" }} />
            </div>
            <div
              style={{
                display: "grid",
                gridTemplateColumns: `repeat(auto-fill, minmax(${isMobile ? 78 : 92}px, 1fr))`,
                gap: 8,
              }}
            >
              {list.map((icon) => (
                <IconTile key={icon.slug + icon.variant_id} icon={icon} onSelect={() => setActive(icon)} />
              ))}
            </div>
          </div>
        );
      })}

      <AnimatePresence>{active && <IconDetail icon={active} onClose={() => setActive(null)} />}</AnimatePresence>
    </section>
  );
}

function LoadMore({ remaining, onClick }: { remaining: number; onClick: () => void }) {
  return (
    <div style={{ display: "flex", justifyContent: "center", marginTop: 24 }}>
      <button
        onClick={onClick}
        style={{
          padding: "10px 20px",
          background: "var(--bg-surface)",
          border: "1px solid var(--border)",
          borderRadius: 8,
          color: "var(--text-1)",
          fontSize: 13,
          fontWeight: 500,
          cursor: "pointer",
          fontFamily: "var(--font-sans)",
        }}
      >
        Load {Math.min(INCREMENT, remaining)} more · {remaining} hidden
      </button>
    </div>
  );
}
