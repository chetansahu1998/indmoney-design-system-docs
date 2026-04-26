"use client";
import { useEffect, useState } from "react";
import { motion, AnimatePresence } from "framer-motion";
import { Command, CommandList, CommandEmpty, CommandGroup, CommandItem } from "@/components/ui/command";
import { Command as CommandPrimitive } from "cmdk";
import { overlayVariants, panelVariants } from "@/lib/motion-variants";

type Item = { id: string; title: string; desc: string; section: string; href: string };

const INDEX: Item[] = [
  // Typography
  { id: "type-ramp",      title: "Type ramp",       desc: "H40–H16, B16–B11, A17–A12 named styles",    section: "Typography", href: "#type-ramp" },
  { id: "type-hierarchy", title: "Type hierarchy",   desc: "Heading, Body, Action categories",           section: "Typography", href: "#type-hierarchy" },
  { id: "type-styles",    title: "Type styles",      desc: "Style cards with specs per category",        section: "Typography", href: "#type-styles" },
  { id: "H40",  title: "H40",  desc: "40px / 48px line-height / −0.25px tracking", section: "Typography", href: "#type-ramp" },
  { id: "H32",  title: "H32",  desc: "32px / 40px line-height / −0.25px tracking", section: "Typography", href: "#type-ramp" },
  { id: "H28",  title: "H28",  desc: "28px / 36px line-height / −0.25px tracking", section: "Typography", href: "#type-ramp" },
  { id: "H24",  title: "H24",  desc: "24px / 32px line-height / −0.25px tracking", section: "Typography", href: "#type-ramp" },
  { id: "H20",  title: "H20",  desc: "20px / 28px line-height / −0.25px tracking", section: "Typography", href: "#type-ramp" },
  { id: "H16",  title: "H16",  desc: "16px / 20px line-height / −0.15px tracking", section: "Typography", href: "#type-ramp" },
  { id: "B16",  title: "B16",  desc: "16px / 20px line-height / −0.15px tracking", section: "Typography", href: "#type-ramp" },
  { id: "B14",  title: "B14",  desc: "14px / 18px line-height / −0.1px tracking",  section: "Typography", href: "#type-ramp" },
  { id: "B12",  title: "B12",  desc: "12px / 16px line-height / −0.1px tracking",  section: "Typography", href: "#type-ramp" },
  { id: "B11",  title: "B11",  desc: "11px / 14px line-height / −0.1px tracking",  section: "Typography", href: "#type-ramp" },
  { id: "A17",  title: "A17",  desc: "17px / 24px line-height / −0.25px tracking", section: "Typography", href: "#type-ramp" },
  { id: "A16",  title: "A16",  desc: "16px / 24px line-height / 0px tracking",     section: "Typography", href: "#type-ramp" },
  { id: "A14",  title: "A14",  desc: "14px / 20px line-height / 0px tracking",     section: "Typography", href: "#type-ramp" },
  { id: "A12",  title: "A12",  desc: "12px / 16px line-height / 0px tracking",     section: "Typography", href: "#type-ramp" },
  { id: "noontree", title: "Noontree", desc: "Primary typeface across all surfaces", section: "Typography", href: "#typography" },

  // Color
  { id: "color",         title: "Color",            desc: "Base palette and semantic tokens",            section: "Color", href: "#color" },
  { id: "color-text",    title: "Text & icon tokens", desc: "primary, secondary, tertiary, action, error", section: "Color", href: "#color-text" },
  { id: "color-surface", title: "Surface tokens",   desc: "primary, secondary, brand, action-bold…",    section: "Color", href: "#color-surface" },
  { id: "color-border",  title: "Border tokens",    desc: "primary, subtle, bold, action, error…",      section: "Color", href: "#color-border" },
  { id: "grey",          title: "Grey palette",     desc: "grey.50 → grey.1000",                        section: "Color", href: "#color" },
  { id: "brand-blue",    title: "Brand Blue",       desc: "brand-blue.50 → brand-blue.1000",            section: "Color", href: "#color" },
  { id: "noon",          title: "Noon Yellow",      desc: "noon.100 → noon.1000",                       section: "Color", href: "#color" },
  { id: "text-primary",  title: "colour.text-n-icon.primary",   desc: "#1d2539 — grey.900", section: "Color", href: "#color-text" },
  { id: "text-action",   title: "colour.text-n-icon.action",    desc: "#0f61ff — brand-blue.700", section: "Color", href: "#color-text" },
  { id: "surface-brand", title: "colour.surface.brand-primary", desc: "#feee00 — noon.400", section: "Color", href: "#color-surface" },

  // Spacing
  { id: "spacing",        title: "Spacing",         desc: "Literal-value space tokens: space.0–space.72", section: "Spacing", href: "#spacing" },
  { id: "spacing-scale",  title: "Spacing scale",   desc: "space.0, space.4, space.8 … space.72",         section: "Spacing", href: "#spacing-scale" },
  { id: "spacing-radius", title: "Border radius",   desc: "radius.2 → radius.40, radius.rounded (9999px)", section: "Spacing", href: "#spacing-radius" },
  { id: "space-4",  title: "space.4",   desc: "4px",  section: "Spacing", href: "#spacing-scale" },
  { id: "space-8",  title: "space.8",   desc: "8px",  section: "Spacing", href: "#spacing-scale" },
  { id: "space-16", title: "space.16",  desc: "16px", section: "Spacing", href: "#spacing-scale" },
  { id: "space-24", title: "space.24",  desc: "24px", section: "Spacing", href: "#spacing-scale" },
  { id: "radius-8",       title: "radius.8",        desc: "8px — default inputs, buttons, cards",  section: "Spacing", href: "#spacing-radius" },
  { id: "radius-rounded", title: "radius.rounded",  desc: "9999px — chips, icon buttons, avatars", section: "Spacing", href: "#spacing-radius" },

  // Motion
  { id: "motion",         title: "Motion",           desc: "Spring, opacity, and scale animation specs", section: "Motion", href: "#motion" },
  { id: "motion-spring",  title: "Spring presets",   desc: "motion.spring.fast / standard / heavy",      section: "Motion", href: "#motion-spring" },
  { id: "motion-opacity", title: "Opacity",          desc: "200ms ease-out — motion.opacity.standard",   section: "Motion", href: "#motion-opacity" },
  { id: "motion-scale",   title: "Scale / Press",    desc: "96% scale on press — motion.scale.press",    section: "Motion", href: "#motion-scale" },
  { id: "spring-fast",    title: "motion.spring.fast",    desc: "stiffness 300 / damping 24 — snappy",   section: "Motion", href: "#motion-spring" },
  { id: "spring-std",     title: "motion.spring.standard",desc: "stiffness 300 / damping 26 — balanced", section: "Motion", href: "#motion-spring" },
  { id: "spring-heavy",   title: "motion.spring.heavy",   desc: "stiffness 300 / damping 28 — controlled", section: "Motion", href: "#motion-spring" },
];

const SECTION_COLORS: Record<string, string> = {
  Typography: "#4d93fc",
  Color:      "#feee00",
  Spacing:    "#3fcf60",
  Motion:     "#f5a623",
};

const TOP_LEVEL: Item[] = [
  { id: "typography", title: "Typography", desc: "Type ramp, hierarchy, and style specs",          section: "Typography", href: "#typography" },
  { id: "color-top",  title: "Color",      desc: "Base palette, text, surface, and border tokens", section: "Color",      href: "#color" },
  { id: "spacing-top",title: "Spacing",    desc: "Space scale and border radius tokens",           section: "Spacing",    href: "#spacing" },
  { id: "motion-top", title: "Motion",     desc: "Spring presets, opacity, and press feedback",    section: "Motion",     href: "#motion" },
];

export default function SearchModal({ onClose }: { onClose: () => void }) {
  const [query, setQuery] = useState("");

  const results = query.trim()
    ? INDEX.filter((item) =>
        `${item.title} ${item.desc} ${item.section}`.toLowerCase().includes(query.toLowerCase())
      ).slice(0, 8)
    : TOP_LEVEL;

  function navigate(href: string) {
    const id = href.replace("#", "");
    const el = document.getElementById(id);
    if (el) el.scrollIntoView({ behavior: "smooth", block: "start" });
    onClose();
  }

  // Esc key
  useEffect(() => {
    const handler = (e: KeyboardEvent) => { if (e.key === "Escape") onClose(); };
    window.addEventListener("keydown", handler);
    return () => window.removeEventListener("keydown", handler);
  }, [onClose]);

  return (
    <AnimatePresence>
      <motion.div
        key="overlay"
        variants={overlayVariants}
        initial="hidden"
        animate="visible"
        exit="exit"
        onClick={onClose}
        style={{
          position: "fixed", inset: 0, zIndex: 200,
          background: "rgba(0,0,0,0.55)",
          display: "flex", alignItems: "flex-start", justifyContent: "center",
          paddingTop: 100,
          backdropFilter: "blur(3px)",
        }}
      >
        <motion.div
          key="panel"
          variants={panelVariants}
          initial="hidden"
          animate="visible"
          exit="exit"
          onClick={(e) => e.stopPropagation()}
          style={{ width: 580, maxHeight: "70vh", display: "flex", flexDirection: "column" }}
        >
          <Command
            style={{
              background: "var(--bg-surface)",
              border: "1px solid var(--border-strong)",
              borderRadius: 12,
              overflow: "hidden",
              boxShadow: "0 32px 80px rgba(0,0,0,0.45), 0 0 0 1px rgba(255,255,255,0.04)",
            }}
          >
            <div style={{
              display: "flex", alignItems: "center", gap: 10,
              padding: "14px 16px",
              borderBottom: "1px solid var(--border)",
            }}>
              <svg width="16" height="16" viewBox="0 0 16 16" fill="none" style={{ color: "var(--text-3)", flexShrink: 0 }}>
                <circle cx="7" cy="7" r="4.5" stroke="currentColor" strokeWidth="1.4" />
                <path d="M10.5 10.5L13 13" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
              </svg>
              <CommandPrimitive.Input
                autoFocus
                placeholder="Search tokens, styles, sections…"
                value={query}
                onValueChange={setQuery}
                style={{
                  flex: 1, background: "none", border: "none", outline: "none",
                  fontSize: 15, color: "var(--text-1)",
                  fontFamily: "var(--font-sans)",
                }}
              />
              <kbd style={{
                fontSize: 11, color: "var(--text-3)",
                background: "var(--bg-surface-2)", border: "1px solid var(--border)",
                borderRadius: 4, padding: "2px 6px",
                fontFamily: "var(--font-mono)",
              }}>esc</kbd>
            </div>

            <CommandList style={{ maxHeight: 380, overflowY: "auto" }}>
              <CommandEmpty style={{ padding: "32px 20px", textAlign: "center", fontSize: 14, color: "var(--text-3)" }}>
                No results for &ldquo;{query}&rdquo;
              </CommandEmpty>

              <CommandGroup>
                {results.map((item) => {
                  const color = SECTION_COLORS[item.section] ?? "#888";
                  return (
                    <CommandItem
                      key={item.id}
                      value={`${item.title} ${item.desc} ${item.section}`}
                      onSelect={() => navigate(item.href)}
                      style={{
                        display: "flex", alignItems: "center", gap: 12,
                        padding: "11px 16px",
                        cursor: "pointer",
                        borderBottom: "1px solid var(--border)",
                        borderRadius: 0,
                        background: "transparent",
                        color: "var(--text-1)",
                      }}
                    >
                      <div style={{ flex: 1, minWidth: 0 }}>
                        <div style={{ fontSize: 13, fontWeight: 600, color: "var(--text-1)", marginBottom: 2 }}>{item.title}</div>
                        <div style={{ fontSize: 11, color: "var(--text-3)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{item.desc}</div>
                      </div>
                      <span style={{
                        flexShrink: 0,
                        fontSize: 9, fontWeight: 700, textTransform: "uppercase", letterSpacing: "0.05em",
                        padding: "2px 7px", borderRadius: 4,
                        background: color + "22",
                        color,
                      }}>
                        {item.section}
                      </span>
                    </CommandItem>
                  );
                })}
              </CommandGroup>
            </CommandList>

            {/* Footer */}
            <div style={{
              padding: "8px 16px",
              display: "flex", gap: 16,
              fontSize: 11, color: "var(--text-3)",
              borderTop: "1px solid var(--border)",
            }}>
              {[["↑↓", "navigate"], ["↵", "open"], ["esc", "close"]].map(([k, v]) => (
                <span key={k} style={{ display: "flex", alignItems: "center", gap: 4 }}>
                  <kbd style={{
                    fontFamily: "var(--font-mono)",
                    background: "var(--bg-surface-2)", border: "1px solid var(--border)",
                    borderRadius: 3, padding: "1px 5px",
                  }}>{k}</kbd>
                  {v}
                </span>
              ))}
            </div>
          </Command>
        </motion.div>
      </motion.div>
    </AnimatePresence>
  );
}
