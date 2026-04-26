"use client";
import { useState } from "react";
import { motion, AnimatePresence } from "framer-motion";
import { base, semantic } from "@/lib/tokens";
import SectionHeading from "@/components/ui/SectionHeading";
import DSTable from "@/components/ui/DSTable";
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip";
import { fadeUp, stagger, itemFadeUp } from "@/lib/motion-variants";
import { useIsMobile } from "@/lib/use-mobile";

// ── Primitive palette groups ──────────────────────────────────────────────────
type Swatch = { name: string; token: string; hex: string; outline?: boolean };

const primitiveGroups: { group: string; items: Swatch[] }[] = [
  {
    group: "Grey",
    items: Object.entries(base.colour.grey).map(([k, v]) => ({
      name: `Grey ${k}`, token: `base.colour.grey.${k}`, hex: v,
      outline: Number(k) < 200,
    })),
  },
  {
    group: "Brand Blue",
    items: Object.entries(base.colour.brandBlue).map(([k, v]) => ({
      name: `Brand Blue ${k}`, token: `base.colour.brand-blue.${k}`, hex: v,
    })),
  },
  {
    group: "Noon Yellow",
    items: Object.entries(base.colour.noon).map(([k, v]) => ({
      name: `Noon ${k}`, token: `base.colour.noon.${k}`, hex: v,
      outline: Number(k) < 300,
    })),
  },
  {
    group: "Supermall",
    items: Object.entries(base.colour.supermall).map(([k, v]) => ({
      name: `Supermall ${k}`, token: `base.colour.supermall.${k}`, hex: v,
    })),
  },
  {
    group: "Red",
    items: Object.entries(base.colour.red).map(([k, v]) => ({
      name: `Red ${k}`, token: `base.colour.red.${k}`, hex: v,
      outline: Number(k) < 200,
    })),
  },
  {
    group: "Green",
    items: Object.entries(base.colour.green).map(([k, v]) => ({
      name: `Green ${k}`, token: `base.colour.green.${k}`, hex: v,
      outline: Number(k) < 200,
    })),
  },
  {
    group: "Orange",
    items: Object.entries(base.colour.orange).map(([k, v]) => ({
      name: `Orange ${k}`, token: `base.colour.orange.${k}`, hex: v,
      outline: Number(k) < 200,
    })),
  },
];

// ── Semantic token rows ───────────────────────────────────────────────────────
type SemanticRow = { token: string; primitive: string; hex: string; usage: string };

const textTokens: SemanticRow[] = [
  { token: "colour.text-n-icon.primary",         primitive: "grey.900",        hex: semantic.colour.textNIcon.primary,         usage: "Primary text, headings, labels" },
  { token: "colour.text-n-icon.secondary",       primitive: "grey.700",        hex: semantic.colour.textNIcon.secondary,       usage: "Body copy, descriptions" },
  { token: "colour.text-n-icon.tertiary",        primitive: "grey.600",        hex: semantic.colour.textNIcon.tertiary,        usage: "Subtext, supporting info" },
  { token: "colour.text-n-icon.muted",           primitive: "grey.500",        hex: semantic.colour.textNIcon.muted,           usage: "Placeholders, disabled text" },
  { token: "colour.text-n-icon.on-surface-bold", primitive: "neutral.white",   hex: semantic.colour.textNIcon.onSurfaceBold,   usage: "Text on dark/filled surfaces" },
  { token: "colour.text-n-icon.action",          primitive: "brand-blue.700",  hex: semantic.colour.textNIcon.action,          usage: "Links, CTAs, interactive labels" },
  { token: "colour.text-n-icon.error",           primitive: "red.700",         hex: semantic.colour.textNIcon.error,           usage: "Error messages" },
  { token: "colour.text-n-icon.warning",         primitive: "orange.700",      hex: semantic.colour.textNIcon.warning,         usage: "Warning states" },
  { token: "colour.text-n-icon.success",         primitive: "green.700",       hex: semantic.colour.textNIcon.success,         usage: "Success states" },
  { token: "colour.text-n-icon.yellow-light",    primitive: "noon.400",        hex: semantic.colour.textNIcon.yellowLight,     usage: "Brand yellow on dark surfaces" },
  { token: "colour.text-n-icon.supermall",       primitive: "supermall.800",   hex: semantic.colour.textNIcon.supermall,       usage: "Supermall brand text" },
];

const surfaceTokens: SemanticRow[] = [
  { token: "colour.surface.primary",            primitive: "neutral.white",  hex: semantic.colour.surface.primary,           usage: "Default page/card background" },
  { token: "colour.surface.secondary",          primitive: "grey.100",       hex: semantic.colour.surface.secondary,         usage: "Subtle background, sidebar" },
  { token: "colour.surface.tertiary",           primitive: "grey.200",       hex: semantic.colour.surface.tertiary,          usage: "Hover states, table rows" },
  { token: "colour.surface.muted",              primitive: "grey.300",       hex: semantic.colour.surface.muted,             usage: "Dividers used as fills" },
  { token: "colour.surface.brand-primary",      primitive: "noon.400",       hex: semantic.colour.surface.brandPrimary,      usage: "Primary brand CTA background" },
  { token: "colour.surface.action-subtle",      primitive: "brand-blue.100", hex: semantic.colour.surface.actionSubtle,      usage: "Focused/selected input bg" },
  { token: "colour.surface.action-bold",        primitive: "brand-blue.600", hex: semantic.colour.surface.actionBold,        usage: "Primary button background" },
  { token: "colour.surface.error-subtle",       primitive: "red.100",        hex: semantic.colour.surface.errorSubtle,       usage: "Error field background" },
  { token: "colour.surface.warning-subtle",     primitive: "orange.100",     hex: semantic.colour.surface.warningSubtle,     usage: "Warning banner background" },
  { token: "colour.surface.success-subtle",     primitive: "green.100",      hex: semantic.colour.surface.successSubtle,     usage: "Success banner background" },
  { token: "colour.surface.supermall-subtle",   primitive: "supermall.100",  hex: semantic.colour.surface.supermallSubtle,   usage: "Supermall feature tinting" },
  { token: "colour.surface.secondary-inverted", primitive: "grey.900",       hex: semantic.colour.surface.secondaryInverted, usage: "Dark surface / inverted card" },
];

const borderTokens: SemanticRow[] = [
  { token: "colour.border.primary",   primitive: "grey.300",       hex: semantic.colour.border.primary,   usage: "Default card and input borders" },
  { token: "colour.border.subtle",    primitive: "grey.200",       hex: semantic.colour.border.subtle,    usage: "Subtle separators" },
  { token: "colour.border.bold",      primitive: "grey.400",       hex: semantic.colour.border.bold,      usage: "Emphasis borders" },
  { token: "colour.border.action",    primitive: "brand-blue.300", hex: semantic.colour.border.action,    usage: "Focused input border" },
  { token: "colour.border.error",     primitive: "red.300",        hex: semantic.colour.border.error,     usage: "Error state border" },
  { token: "colour.border.warning",   primitive: "orange.300",     hex: semantic.colour.border.warning,   usage: "Warning state border" },
  { token: "colour.border.success",   primitive: "green.300",      hex: semantic.colour.border.success,   usage: "Success state border" },
  { token: "colour.border.supermall", primitive: "supermall.300",  hex: semantic.colour.border.supermall, usage: "Supermall feature border" },
];

/* ── Palette strip tile (big color bars) ── */
function PaletteTile({ c, i }: { c: Swatch; i: number }) {
  const [copied, setCopied] = useState(false);

  const copy = () => {
    navigator.clipboard.writeText(c.hex).catch(() => {});
    setCopied(true);
    setTimeout(() => setCopied(false), 1200);
  };

  return (
    <TooltipProvider key={c.token} delayDuration={80}>
      <Tooltip>
        <TooltipTrigger asChild>
          <motion.button
            onClick={copy}
            initial={{ opacity: 0 }}
            whileInView={{ opacity: 1 }}
            viewport={{ once: true }}
            whileHover={{ scaleY: 1.08, zIndex: 5 }}
            transition={{ delay: i * 0.02, duration: 0.3, type: "spring", stiffness: 300, damping: 22 }}
            style={{
              flex: 1, display: "flex", flexDirection: "column",
              cursor: "pointer", padding: 0, background: "none", border: "none",
              position: "relative",
            }}
          >
            <div style={{
              height: 48, background: c.hex,
              borderRight: "1px solid rgba(0,0,0,0.06)",
              position: "relative", overflow: "hidden",
            }}>
              <AnimatePresence>
                {copied && (
                  <motion.div
                    initial={{ opacity: 0 }}
                    animate={{ opacity: 1 }}
                    exit={{ opacity: 0 }}
                    transition={{ duration: 0.12 }}
                    style={{
                      position: "absolute", inset: 0,
                      display: "flex", alignItems: "center", justifyContent: "center",
                      background: "rgba(0,0,0,0.3)",
                    }}
                  >
                    <svg width="12" height="12" viewBox="0 0 12 12" fill="none">
                      <path d="M2 6l2.5 2.5L10 3.5" stroke="#fff" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
                    </svg>
                  </motion.div>
                )}
              </AnimatePresence>
            </div>
            <div style={{
              padding: "6px 8px", background: "var(--bg-surface)",
              borderRight: "1px solid var(--border)",
              borderTop: "1px solid var(--border)",
            }}>
              <div style={{ fontSize: 9, fontWeight: 600, color: "var(--text-2)", marginBottom: 1, whiteSpace: "nowrap" }}>
                {c.token.split(".").pop()}
              </div>
              <div style={{ fontSize: 9, fontFamily: "var(--font-mono)", color: "var(--text-3)" }}>{c.hex}</div>
            </div>
          </motion.button>
        </TooltipTrigger>
        <TooltipContent
          style={{
            background: "var(--bg-surface-2)", border: "1px solid var(--border)",
            color: "var(--text-1)", fontSize: 11, borderRadius: 6, padding: "6px 10px",
          }}
        >
          <div style={{ fontFamily: "var(--font-mono)", fontSize: 11 }}>{c.token}</div>
          <div style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: copied ? "var(--accent)" : "var(--text-3)" }}>
            {copied ? "Copied!" : `${c.hex} · click to copy`}
          </div>
        </TooltipContent>
      </Tooltip>
    </TooltipProvider>
  );
}

/* ── Semantic table swatch (small circle in table rows) ── */
function ColorSwatch({ hex, outline }: { hex: string; outline?: boolean }) {
  const [copied, setCopied] = useState(false);

  const copy = () => {
    navigator.clipboard.writeText(hex).catch(() => {});
    setCopied(true);
    setTimeout(() => setCopied(false), 1200);
  };

  return (
    <TooltipProvider delayDuration={80}>
      <Tooltip>
        <TooltipTrigger asChild>
          <motion.button
            onClick={copy}
            whileHover={{ scale: 1.2, zIndex: 10 }}
            whileTap={{ scale: 0.9 }}
            transition={{ type: "spring", stiffness: 300, damping: 22 }}
            style={{
              width: 24, height: 24, borderRadius: 4, background: hex,
              border: `1px solid ${outline ? "var(--border-strong)" : "rgba(0,0,0,0.08)"}`,
              flexShrink: 0, cursor: "pointer",
              position: "relative", overflow: "hidden",
              padding: 0,
            }}
          >
            <AnimatePresence>
              {copied && (
                <motion.div
                  initial={{ opacity: 0, scale: 0.6 }}
                  animate={{ opacity: 1, scale: 1 }}
                  exit={{ opacity: 0 }}
                  transition={{ duration: 0.12 }}
                  style={{
                    position: "absolute", inset: 0, borderRadius: 4,
                    display: "flex", alignItems: "center", justifyContent: "center",
                    background: "rgba(0,0,0,0.45)",
                  }}
                >
                  <svg width="10" height="10" viewBox="0 0 12 12" fill="none">
                    <path d="M2 6l2.5 2.5L10 3.5" stroke="#fff" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
                  </svg>
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
          {copied ? "Copied!" : `${hex} · click to copy`}
        </TooltipContent>
      </Tooltip>
    </TooltipProvider>
  );
}

function SemanticTable({ rows }: { rows: SemanticRow[] }) {
  const isMobile = useIsMobile();
  return (
    <div className="ds-table-scroll">
      <DSTable
        headers={isMobile ? ["Token", "Sw", "Usage"] : ["Token", "Swatch", "Primitive", "Usage"]}
        rows={rows.map((t) => {
          const base = [
            <span key="tok" style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--text-3)" }}>{t.token}</span>,
            <ColorSwatch key="sw" hex={t.hex} outline={t.hex === "#ffffff" || t.hex === "#fefdc8" || t.hex === "#fff7c2"} />,
            <span key="u" style={{ color: "var(--text-2)", fontSize: 13 }}>{t.usage}</span>,
          ];
          if (!isMobile) {
            base.splice(2, 0,
              <span key="prim" style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--text-3)" }}>{t.primitive}</span>
            );
          }
          return base;
        })}
      />
    </div>
  );
}

export default function ColorSection() {
  const isMobile = useIsMobile();
  return (
    <section id="color" style={{ marginBottom: 80, scrollMarginTop: "calc(var(--header-h) + 32px)" }}>
      <SectionHeading id="color" title="Color" />

      <motion.p
        variants={fadeUp} initial="hidden" whileInView="visible" viewport={{ once: true }}
        style={{ fontSize: 16, color: "var(--text-2)", lineHeight: 1.65, maxWidth: 640, marginBottom: 48 }}
      >
        The Field DS colour system is organised into base primitives and semantic tokens. Always use semantic tokens in components — they carry intent and are theme-safe.
      </motion.p>

      {/* ── Primitives ── */}
      <motion.div
        variants={stagger} initial="hidden" whileInView="visible" viewport={{ once: true, margin: "-60px" }}
        style={{ marginBottom: 48 }}
      >
        <motion.h3 variants={itemFadeUp} style={{ fontSize: 18, fontWeight: 600, letterSpacing: "-0.3px", color: "var(--text-1)", marginBottom: 8 }}>
          Base palette
        </motion.h3>
        <motion.p variants={itemFadeUp} style={{ fontSize: 14, color: "var(--text-2)", marginBottom: 24, maxWidth: 600 }}>
          Raw colour values. Reference these only when defining semantic tokens — never use them directly in components.
        </motion.p>

        {primitiveGroups.map(({ group, items }) => (
          <motion.div
            key={group}
            variants={itemFadeUp}
            style={{ marginBottom: 24 }}
          >
            <div style={{ fontSize: 11, fontWeight: 600, color: "var(--text-3)", textTransform: "uppercase", letterSpacing: "0.07em", marginBottom: 10 }}>
              {group}
            </div>
            <div className={isMobile ? "ds-table-scroll" : ""}>
              <div style={{ display: "flex", gap: 0, borderRadius: 8, overflow: isMobile ? "visible" : "hidden", border: "1px solid var(--border)", minWidth: isMobile ? "max-content" : "auto" }}>
                {items.map((c, i) => (
                  <PaletteTile key={c.token} c={c} i={i} />
                ))}
              </div>
            </div>
          </motion.div>
        ))}
      </motion.div>

      {/* ── Semantic: Text & Icon ── */}
      <motion.div
        id="color-text"
        variants={fadeUp} initial="hidden" whileInView="visible" viewport={{ once: true, margin: "-40px" }}
        style={{ marginBottom: 40, scrollMarginTop: "calc(var(--header-h) + 32px)" }}
      >
        <h3 style={{ fontSize: 18, fontWeight: 600, letterSpacing: "-0.3px", color: "var(--text-1)", marginBottom: 8 }}>Text & icon tokens</h3>
        <p style={{ fontSize: 14, color: "var(--text-2)", marginBottom: 24, maxWidth: 640 }}>
          Used for text, icons, and any foreground element.
        </p>
        <SemanticTable rows={textTokens} />
      </motion.div>

      {/* ── Semantic: Surface ── */}
      <motion.div
        id="color-surface"
        variants={fadeUp} initial="hidden" whileInView="visible" viewport={{ once: true, margin: "-40px" }}
        style={{ marginBottom: 40, scrollMarginTop: "calc(var(--header-h) + 32px)" }}
      >
        <h3 style={{ fontSize: 18, fontWeight: 600, letterSpacing: "-0.3px", color: "var(--text-1)", marginBottom: 8 }}>Surface tokens</h3>
        <p style={{ fontSize: 14, color: "var(--text-2)", marginBottom: 24, maxWidth: 640 }}>
          Background fills for containers, cards, banners, and overlays.
        </p>
        <SemanticTable rows={surfaceTokens} />
      </motion.div>

      {/* ── Semantic: Border ── */}
      <motion.div
        id="color-border"
        variants={fadeUp} initial="hidden" whileInView="visible" viewport={{ once: true, margin: "-40px" }}
        style={{ scrollMarginTop: "calc(var(--header-h) + 32px)" }}
      >
        <h3 style={{ fontSize: 18, fontWeight: 600, letterSpacing: "-0.3px", color: "var(--text-1)", marginBottom: 8 }}>Border tokens</h3>
        <p style={{ fontSize: 14, color: "var(--text-2)", marginBottom: 24, maxWidth: 640 }}>
          Stroke colours for inputs, cards, and dividers.
        </p>
        <SemanticTable rows={borderTokens} />
      </motion.div>
    </section>
  );
}
