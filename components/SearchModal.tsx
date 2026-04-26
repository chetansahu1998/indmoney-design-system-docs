"use client";
import { useEffect, useMemo, useState } from "react";
import { motion, AnimatePresence } from "framer-motion";
import { Command, CommandList, CommandEmpty, CommandGroup, CommandItem } from "@/components/ui/command";
import { Command as CommandPrimitive } from "cmdk";
import { overlayVariants, panelVariants } from "@/lib/motion-variants";
import { buildSemanticPairs, buildBasePalette } from "@/lib/tokens/loader";
import { useUIStore } from "@/lib/ui-store";

type Item = {
  id: string;
  title: string;
  desc: string;
  section: string;
  href: string;
  /** Action keys this item supports (copy CSS var, copy hex, etc) */
  copyOptions?: { label: string; value: string }[];
};

const SECTION_COLORS: Record<string, string> = {
  Color: "#feee00",
  Surface: "#a78bfa",
  Text: "#4d93fc",
  Border: "#888",
  Success: "#1FD896",
  Danger: "#FF5050",
  Warning: "#FF8D4D",
  Info: "#3D99FF",
  Constant: "#666",
  Page: "#aaa",
  Other: "#999",
  Base: "#feee00",
  Section: "#aaa",
};

/** Top-level navigation entries (always shown when query is empty AND no recents). */
const TOP_LEVEL: Item[] = [
  { id: "color",       title: "Color",       desc: "Light/dark token pairs by usage cluster", section: "Section", href: "#color" },
  { id: "typography",  title: "Typography",  desc: "Type ramp from Glyph",                    section: "Section", href: "#typography" },
  { id: "spacing",     title: "Spacing",     desc: "Space scale + radius (Field defaults)",   section: "Section", href: "#spacing" },
  { id: "motion",      title: "Motion",      desc: "Spring presets",                          section: "Section", href: "#motion" },
  { id: "iconography", title: "Iconography", desc: "Icon library (Field defaults)",            section: "Section", href: "#iconography" },
];

/** Compute the live token index from the extracted JSON — runs at module load. */
function computeIndex(): Item[] {
  const out: Item[] = [];
  for (const p of buildSemanticPairs()) {
    const sectionLabel =
      p.bucket === "text-n-icon" ? "Text"
      : p.bucket === "surface-elevated" ? "Surface"
      : p.bucket === "constant-light" ? "Constant"
      : p.bucket === "constant-dark" ? "Constant"
      : capitalize(p.bucket);
    out.push({
      id: `s-${p.path}`,
      title: p.path,
      desc: `${p.light} → ${p.dark}${p.description ? ` · ${p.description.slice(0, 60)}…` : ""}`,
      section: sectionLabel,
      href: `#color-${p.bucket}`,
      copyOptions: [
        { label: "Copy CSS var", value: `var(--${p.path.replace(/[^a-z0-9-]+/gi, "-").toLowerCase()})` },
        { label: "Copy light hex", value: p.light },
        { label: "Copy dark hex", value: p.dark },
        { label: "Copy token path", value: p.path },
      ],
    });
  }
  for (const p of buildBasePalette()) {
    out.push({
      id: `b-${p.path}`,
      title: p.path,
      desc: p.hex,
      section: "Base",
      href: "#color-base",
      copyOptions: [
        { label: "Copy hex", value: p.hex },
        { label: "Copy CSS var", value: `var(--${p.path.replace(/[^a-z0-9-]+/gi, "-").toLowerCase()})` },
      ],
    });
  }
  return out;
}

const TOKEN_INDEX = computeIndex();

export default function SearchModal({ onClose }: { onClose: () => void }) {
  const [query, setQuery] = useState("");
  const [selected, setSelected] = useState<Item | null>(null);
  const recents = useUIStore((s) => s.recents);
  const pushRecent = useUIStore((s) => s.pushRecent);

  // Filter
  const results = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) {
      if (recents.length > 0) {
        const recentItems = recents
          .map((id) => TOKEN_INDEX.find((t) => t.id === id))
          .filter(Boolean) as Item[];
        return [...recentItems, ...TOP_LEVEL];
      }
      return [...TOP_LEVEL, ...TOKEN_INDEX.slice(0, 30)];
    }
    return TOKEN_INDEX.filter((item) =>
      `${item.title} ${item.desc} ${item.section}`.toLowerCase().includes(q),
    ).slice(0, 40);
  }, [query, recents]);

  function navigate(item: Item) {
    pushRecent(item.id);
    const id = item.href.replace("#", "");
    const el = document.getElementById(id);
    if (el) el.scrollIntoView({ behavior: "smooth", block: "start" });
    onClose();
  }

  function copyValue(value: string) {
    navigator.clipboard.writeText(value).catch(() => {});
    // Simple visual feedback via brief setSelected toggle
    setSelected(null);
    onClose();
  }

  // Esc — close subview first, then modal
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        if (selected) {
          setSelected(null);
        } else {
          onClose();
        }
      }
    };
    window.addEventListener("keydown", handler);
    return () => window.removeEventListener("keydown", handler);
  }, [onClose, selected]);

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
          position: "fixed",
          inset: 0,
          zIndex: 200,
          background: "rgba(0,0,0,0.55)",
          display: "flex",
          alignItems: "flex-start",
          justifyContent: "center",
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
          style={{ width: "min(620px, 92vw)", maxHeight: "70vh", display: "flex", flexDirection: "column" }}
        >
          <Command
            style={{
              background: "var(--bg-surface)",
              border: "1px solid var(--border-strong)",
              borderRadius: 12,
              overflow: "hidden",
              boxShadow: "0 32px 80px rgba(0,0,0,0.45), 0 0 0 1px rgba(255,255,255,0.04)",
            }}
            shouldFilter={false}
          >
            <div
              style={{
                display: "flex",
                alignItems: "center",
                gap: 10,
                padding: "14px 16px",
                borderBottom: "1px solid var(--border)",
              }}
            >
              <svg width="16" height="16" viewBox="0 0 16 16" fill="none" style={{ color: "var(--text-3)", flexShrink: 0 }}>
                <circle cx="7" cy="7" r="4.5" stroke="currentColor" strokeWidth="1.4" />
                <path d="M10.5 10.5L13 13" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
              </svg>
              <CommandPrimitive.Input
                autoFocus
                placeholder={selected ? `${selected.title} → choose action…` : "Search tokens (try: surface, primary, success, #FFFFFF)…"}
                value={query}
                onValueChange={setQuery}
                style={{
                  flex: 1,
                  background: "none",
                  border: "none",
                  outline: "none",
                  fontSize: 15,
                  color: "var(--text-1)",
                  fontFamily: "var(--font-sans)",
                }}
              />
              <kbd
                style={{
                  fontSize: 11,
                  color: "var(--text-3)",
                  background: "var(--bg-surface-2)",
                  border: "1px solid var(--border)",
                  borderRadius: 4,
                  padding: "2px 6px",
                  fontFamily: "var(--font-mono)",
                }}
              >
                esc
              </kbd>
            </div>

            <CommandList style={{ maxHeight: 440, overflowY: "auto" }}>
              <CommandEmpty
                style={{ padding: "32px 20px", textAlign: "center", fontSize: 14, color: "var(--text-3)" }}
              >
                No results for &ldquo;{query}&rdquo;
              </CommandEmpty>

              {selected ? (
                <CommandGroup heading={`Copy from ${selected.title}`}>
                  {selected.copyOptions?.map((o) => (
                    <CommandItem
                      key={o.label}
                      value={o.label}
                      onSelect={() => copyValue(o.value)}
                      style={commandItemStyle}
                    >
                      <div style={{ flex: 1, minWidth: 0 }}>
                        <div style={{ fontSize: 13, fontWeight: 600, color: "var(--text-1)" }}>{o.label}</div>
                        <div
                          style={{
                            fontSize: 11,
                            color: "var(--text-3)",
                            fontFamily: "var(--font-mono)",
                            overflow: "hidden",
                            textOverflow: "ellipsis",
                            whiteSpace: "nowrap",
                          }}
                        >
                          {o.value}
                        </div>
                      </div>
                    </CommandItem>
                  ))}
                </CommandGroup>
              ) : (
                <CommandGroup>
                  {results.map((item) => {
                    const color = SECTION_COLORS[item.section] ?? "#888";
                    return (
                      <CommandItem
                        key={item.id}
                        value={`${item.title} ${item.desc} ${item.section}`}
                        onSelect={() => {
                          if (item.copyOptions && item.copyOptions.length > 0) {
                            setSelected(item);
                            setQuery("");
                          } else {
                            navigate(item);
                          }
                        }}
                        style={commandItemStyle}
                      >
                        <div style={{ flex: 1, minWidth: 0 }}>
                          <div
                            style={{
                              fontSize: 13,
                              fontWeight: 600,
                              color: "var(--text-1)",
                              marginBottom: 2,
                              fontFamily: "var(--font-mono)",
                            }}
                          >
                            {item.title}
                          </div>
                          <div
                            style={{
                              fontSize: 11,
                              color: "var(--text-3)",
                              overflow: "hidden",
                              textOverflow: "ellipsis",
                              whiteSpace: "nowrap",
                            }}
                          >
                            {item.desc}
                          </div>
                        </div>
                        <span
                          style={{
                            flexShrink: 0,
                            fontSize: 9,
                            fontWeight: 700,
                            textTransform: "uppercase",
                            letterSpacing: "0.05em",
                            padding: "2px 7px",
                            borderRadius: 4,
                            background: color + "22",
                            color,
                          }}
                        >
                          {item.section}
                        </span>
                      </CommandItem>
                    );
                  })}
                </CommandGroup>
              )}
            </CommandList>

            <div
              style={{
                padding: "8px 16px",
                display: "flex",
                gap: 16,
                fontSize: 11,
                color: "var(--text-3)",
                borderTop: "1px solid var(--border)",
              }}
            >
              {[
                ["↑↓", "navigate"],
                ["↵", selected ? "copy" : "open"],
                ["esc", selected ? "back" : "close"],
              ].map(([k, v]) => (
                <span key={k} style={{ display: "flex", alignItems: "center", gap: 4 }}>
                  <kbd
                    style={{
                      fontFamily: "var(--font-mono)",
                      background: "var(--bg-surface-2)",
                      border: "1px solid var(--border)",
                      borderRadius: 3,
                      padding: "1px 5px",
                    }}
                  >
                    {k}
                  </kbd>
                  {v}
                </span>
              ))}
              <span style={{ marginLeft: "auto" }}>
                {TOKEN_INDEX.length} tokens indexed
              </span>
            </div>
          </Command>
        </motion.div>
      </motion.div>
    </AnimatePresence>
  );
}

const commandItemStyle: React.CSSProperties = {
  display: "flex",
  alignItems: "center",
  gap: 12,
  padding: "11px 16px",
  cursor: "pointer",
  borderBottom: "1px solid var(--border)",
  borderRadius: 0,
  background: "transparent",
  color: "var(--text-1)",
};

function capitalize(s: string) {
  return s.charAt(0).toUpperCase() + s.slice(1);
}
