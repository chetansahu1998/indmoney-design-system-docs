"use client";
import { useState, useCallback } from "react";
import { motion, AnimatePresence } from "framer-motion";
import { iconGroups, allIcons, type Icon } from "@/lib/icons";
import SectionHeading from "@/components/ui/SectionHeading";
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip";
import { stagger, itemFadeUp, fadeUp } from "@/lib/motion-variants";
import { useIsMobile } from "@/lib/use-mobile";

/* ── Icon tile ── */
function IconTile({ icon }: { icon: Icon }) {
  const [copied, setCopied] = useState(false);

  const copy = useCallback(() => {
    const svg = `<svg width="24" height="24" viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg">${icon.inner}</svg>`;
    navigator.clipboard.writeText(svg).catch(() => {});
    setCopied(true);
    setTimeout(() => setCopied(false), 1400);
  }, [icon.inner]);

  return (
    <TooltipProvider delayDuration={80}>
      <Tooltip>
        <TooltipTrigger asChild>
          <motion.button
            onClick={copy}
            variants={itemFadeUp}
            whileHover={{ y: -2, scale: 1.04, boxShadow: "0 6px 20px rgba(0,0,0,0.12)" }}
            whileTap={{ scale: 0.94 }}
            transition={{ type: "spring", stiffness: 300, damping: 22 }}
            style={{
              display: "flex", flexDirection: "column", alignItems: "center", gap: 8,
              padding: "14px 8px 12px",
              background: "var(--bg-surface)", border: "1px solid var(--border)",
              borderRadius: 8, cursor: "pointer", width: "100%",
              position: "relative", overflow: "hidden",
            }}
          >
            {/* Icon */}
            <span
              style={{ color: "var(--text-1)", display: "flex", alignItems: "center", justifyContent: "center" }}
              dangerouslySetInnerHTML={{
                __html: `<svg width="20" height="20" viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg">${icon.inner}</svg>`,
              }}
            />

            {/* Name */}
            <span style={{
              fontSize: 9, color: "var(--text-3)",
              overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap",
              maxWidth: "100%", textAlign: "center",
              fontFamily: "var(--font-mono)",
            }}>
              {icon.name}
            </span>

            {/* Copied flash */}
            <AnimatePresence>
              {copied && (
                <motion.div
                  initial={{ opacity: 0, scale: 0.85 }}
                  animate={{ opacity: 1, scale: 1 }}
                  exit={{ opacity: 0 }}
                  transition={{ duration: 0.15 }}
                  style={{
                    position: "absolute", inset: 0,
                    background: "var(--accent)", borderRadius: 8,
                    display: "flex", alignItems: "center", justifyContent: "center",
                    flexDirection: "column", gap: 4,
                  }}
                >
                  <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
                    <path d="M3 8l3.5 3.5L13 4.5" stroke="#fff" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" />
                  </svg>
                  <span style={{ fontSize: 9, color: "#fff", fontFamily: "var(--font-mono)" }}>copied</span>
                </motion.div>
              )}
            </AnimatePresence>
          </motion.button>
        </TooltipTrigger>
        <TooltipContent
          style={{
            background: "var(--bg-surface-2)", border: "1px solid var(--border)",
            color: "var(--text-1)", fontSize: 11, fontFamily: "var(--font-mono)",
            borderRadius: 6, padding: "4px 8px",
          }}
        >
          {icon.name} · click to copy SVG
        </TooltipContent>
      </Tooltip>
    </TooltipProvider>
  );
}

/* ── Search bar ── */
function SearchBar({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  return (
    <div style={{
      display: "flex", alignItems: "center", gap: 10,
      background: "var(--bg-surface)", border: "1px solid var(--border)",
      borderRadius: 8, padding: "10px 14px", marginBottom: 36,
    }}>
      <svg width="14" height="14" viewBox="0 0 16 16" fill="none" style={{ color: "var(--text-3)", flexShrink: 0 }}>
        <circle cx="7" cy="7" r="4.5" stroke="currentColor" strokeWidth="1.4" />
        <path d="M10.5 10.5L13 13" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
      </svg>
      <input
        value={value}
        onChange={e => onChange(e.target.value)}
        placeholder="Filter icons…"
        style={{
          flex: 1, background: "none", border: "none", outline: "none",
          fontSize: 14, color: "var(--text-1)", fontFamily: "var(--font-sans)",
        }}
      />
      <AnimatePresence>
        {value && (
          <motion.button
            initial={{ opacity: 0, scale: 0.8 }}
            animate={{ opacity: 1, scale: 1 }}
            exit={{ opacity: 0, scale: 0.8 }}
            onClick={() => onChange("")}
            style={{
              background: "none", border: "none", cursor: "pointer",
              color: "var(--text-3)", display: "flex", alignItems: "center", padding: 0,
            }}
          >
            <svg width="14" height="14" viewBox="0 0 16 16" fill="none">
              <path d="M4 4l8 8M12 4l-8 8" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
            </svg>
          </motion.button>
        )}
      </AnimatePresence>
      <span style={{ fontSize: 11, color: "var(--text-3)", fontFamily: "var(--font-mono)", flexShrink: 0 }}>
        {value
          ? `${allIcons.filter(i => i.name.includes(value.toLowerCase())).length} results`
          : `${allIcons.length} icons`}
      </span>
    </div>
  );
}

export default function IconographySection() {
  const [query, setQuery] = useState("");
  const isMobile = useIsMobile();
  const q = query.toLowerCase().trim();

  // Filter groups or flatten to search results
  const filtered = q
    ? [{ group: `Results for "${query}"`, icons: allIcons.filter(i => i.name.includes(q)) }]
    : iconGroups;

  return (
    <section
      id="iconography"
      style={{ marginBottom: 80, scrollMarginTop: "calc(var(--header-h) + 32px)" }}
    >
      <SectionHeading id="iconography" title="Iconography" />

      <motion.p
        variants={fadeUp} initial="hidden" whileInView="visible" viewport={{ once: true }}
        style={{ fontSize: 16, color: "var(--text-2)", lineHeight: 1.65, maxWidth: 640, marginBottom: 16 }}
      >
        The M-Icon system — 111 system icons across 9 categories. All icons use{" "}
        <code style={{ fontFamily: "var(--font-mono)", fontSize: 12, background: "var(--bg-surface)", padding: "1px 5px", borderRadius: 3, color: "var(--accent)" }}>
          currentColor
        </code>{" "}
        so they automatically adapt to light and dark themes. Click any icon to copy its name.
      </motion.p>

      {/* Meta row */}
      <motion.div
        variants={stagger} initial="hidden" whileInView="visible" viewport={{ once: true }}
        style={{ display: "flex", gap: 12, marginBottom: 36, flexWrap: "wrap" }}
      >
        {[
          { label: "Total icons", value: "111" },
          { label: "Categories",  value: "9" },
          { label: "Size",        value: "24 × 24" },
          { label: "Format",      value: "SVG" },
          { label: "Fill",        value: "currentColor" },
        ].map((s) => (
          <motion.div
            key={s.label}
            variants={itemFadeUp}
            style={{
              display: "flex", flexDirection: "column", gap: 2,
              padding: "10px 16px",
              background: "var(--bg-surface)", border: "1px solid var(--border)", borderRadius: 8,
            }}
          >
            <span style={{ fontSize: 10, color: "var(--text-3)", textTransform: "uppercase", letterSpacing: "0.07em", fontWeight: 600 }}>{s.label}</span>
            <span style={{ fontSize: 14, fontWeight: 600, color: "var(--text-1)", fontFamily: "var(--font-mono)" }}>{s.value}</span>
          </motion.div>
        ))}
      </motion.div>

      {/* Theme demo strip */}
      <motion.div
        variants={fadeUp} initial="hidden" whileInView="visible" viewport={{ once: true }}
        style={{
          display: "flex", alignItems: "center", gap: 12,
          padding: isMobile ? "12px 14px" : "16px 20px", marginBottom: 36,
          background: "var(--bg-surface)", border: "1px solid var(--border)", borderRadius: 8,
          flexWrap: isMobile ? "wrap" : "nowrap",
        }}
      >
        <span style={{ fontSize: 12, color: "var(--text-3)", flexShrink: 0 }}>Theme preview</span>
        <div style={{ display: "flex", gap: 10, alignItems: "center", flex: 1, flexWrap: "wrap" }}>
          {["home-filled", "notification", "location-filled", "user-circle", "discount", "check-circle", "warning-triangle", "trend-up"].map(name => {
            const icon = allIcons.find(i => i.name === name);
            if (!icon) return null;
            return (
              <motion.span
                key={name}
                whileHover={{ scale: 1.2 }}
                transition={{ type: "spring", stiffness: 300, damping: 22 }}
                style={{ color: "var(--text-1)", display: "flex" }}
                dangerouslySetInnerHTML={{
                  __html: `<svg width="22" height="22" viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg">${icon.inner}</svg>`,
                }}
              />
            );
          })}
        </div>
        {!isMobile && (
          <span style={{ fontSize: 11, color: "var(--text-3)", fontFamily: "var(--font-mono)", flexShrink: 0 }}>
            Toggle theme ↗ to see inversion
          </span>
        )}
      </motion.div>

      {/* Search */}
      <SearchBar value={query} onChange={setQuery} />

      {/* Icon groups */}
      <AnimatePresence mode="wait">
        <motion.div
          key={q || "all"}
          initial={{ opacity: 0, y: 8 }}
          animate={{ opacity: 1, y: 0 }}
          exit={{ opacity: 0 }}
          transition={{ duration: 0.2 }}
        >
          {filtered.map(({ group, icons }) => (
            icons.length === 0 ? null : (
              <div key={group} style={{ marginBottom: 40 }}>
                <div style={{
                  display: "flex", alignItems: "center", gap: 10, marginBottom: 16,
                }}>
                  <span style={{ fontSize: 12, fontWeight: 600, color: "var(--text-2)", textTransform: "uppercase", letterSpacing: "0.07em" }}>
                    {group}
                  </span>
                  <span style={{
                    fontSize: 10, fontWeight: 600, color: "var(--text-3)",
                    background: "var(--bg-surface-2)", padding: "1px 6px", borderRadius: 4,
                    fontFamily: "var(--font-mono)",
                  }}>
                    {icons.length}
                  </span>
                  <div style={{ flex: 1, height: 1, background: "var(--border)" }} />
                </div>

                <motion.div
                  variants={stagger}
                  initial="hidden"
                  whileInView="visible"
                  viewport={{ once: true, margin: "-30px" }}
                  style={{
                    display: "grid",
                    gridTemplateColumns: `repeat(auto-fill, minmax(${isMobile ? 68 : 80}px, 1fr))`,
                    gap: 8,
                  }}
                >
                  {icons.map(icon => (
                    <IconTile key={icon.name} icon={icon} />
                  ))}
                </motion.div>
              </div>
            )
          ))}

          {q && filtered[0]?.icons.length === 0 && (
            <div style={{ padding: "48px 0", textAlign: "center", color: "var(--text-3)", fontSize: 14 }}>
              No icons match &ldquo;{query}&rdquo;
            </div>
          )}
        </motion.div>
      </AnimatePresence>
    </section>
  );
}
