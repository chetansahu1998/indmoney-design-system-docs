"use client";
import { motion } from "framer-motion";
import { typography } from "@/lib/tokens";
import SectionHeading from "@/components/ui/SectionHeading";
import DSTable from "@/components/ui/DSTable";
import { Badge } from "@/components/ui/badge";
import { fadeUp, stagger, itemFadeUp } from "@/lib/motion-variants";
import { useIsMobile } from "@/lib/use-mobile";

const Mono = ({ children }: { children: React.ReactNode }) => (
  <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--text-3)" }}>{children}</span>
);

const Pill = ({ children }: { children: React.ReactNode }) => (
  <span style={{
    display: "inline-block", fontFamily: "var(--font-mono)", fontSize: 10, fontWeight: 600,
    padding: "2px 7px", borderRadius: 4,
    background: "var(--bg-surface-2)", color: "var(--text-3)",
  }}>
    {children}
  </span>
);

type RampEntry = (typeof typography.heading)[number] | (typeof typography.body)[number] | (typeof typography.action)[number];

const previewStyle = (entry: RampEntry): React.CSSProperties => ({
  fontSize: entry.size,
  fontWeight: "heading" in entry ? 700 : 400,
  lineHeight: `${entry.lineHeight}px`,
  letterSpacing: entry.letterSpacing === 0 ? 0 : `${entry.letterSpacing}px`,
  color: "var(--text-1)",
  overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap",
  maxWidth: 280,
});

const CATEGORY_COLORS: Record<string, string> = {
  Heading: "#0f61ff",
  Body:    "#0f8857",
  Action:  "#e5641a",
};

export default function TypographySection() {
  const isMobile = useIsMobile();

  return (
    <section id="typography" style={{ marginBottom: 80, scrollMarginTop: "calc(var(--header-h) + 32px)" }}>
      <SectionHeading id="typography" title="Typography" />

      <motion.p
        variants={fadeUp} initial="hidden" whileInView="visible" viewport={{ once: true }}
        style={{ fontSize: 16, color: "var(--text-2)", lineHeight: 1.65, maxWidth: 640, marginBottom: 48 }}
      >
        Noontree is our primary typeface across all digital surfaces. The type system is organised into three categories — Heading, Body, and Action — each with a fixed set of sizes, line heights, and letter spacing values.
      </motion.p>

      {/* ── TYPE RAMP ── */}
      <motion.div
        id="type-ramp"
        variants={stagger} initial="hidden" whileInView="visible" viewport={{ once: true, margin: "-40px" }}
        style={{ marginBottom: 48, scrollMarginTop: "calc(var(--header-h) + 32px)" }}
      >
        <motion.h3 variants={itemFadeUp} style={{ fontSize: 18, fontWeight: 600, letterSpacing: "-0.3px", color: "var(--text-1)", marginBottom: 8 }}>
          Type ramp
        </motion.h3>
        <motion.p variants={itemFadeUp} style={{ fontSize: 14, color: "var(--text-2)", marginBottom: 24, maxWidth: 600 }}>
          15 named styles across Heading (H40–H16), Body (B16–B11), and Action (A17–A12).
        </motion.p>

        {[
          { label: "Heading", entries: typography.heading, preview: () => "Aa" },
          { label: "Body",    entries: typography.body,    preview: () => "The quick brown fox" },
          { label: "Action",  entries: typography.action,  preview: () => "Add to bag" },
        ].map(({ label, entries, preview }) => (
          <motion.div key={label} variants={itemFadeUp} style={{ marginBottom: 16 }}>
            <div style={{ fontSize: 11, fontWeight: 600, color: "var(--text-3)", textTransform: "uppercase", letterSpacing: "0.07em", marginBottom: 8 }}>
              {label}
            </div>
            {/* Horizontal scroll wrapper on mobile */}
            <div className="ds-table-scroll">
              <DSTable
                headers={isMobile
                  ? ["Name", "Size", "LH", "LS"]
                  : ["Name", "Preview", "Size", "Line height", "Letter spacing"]}
                rows={entries.map((entry) => {
                  const base = [
                    <span key="n" style={{ fontWeight: 600, color: "var(--text-1)", fontSize: 13 }}>{entry.name}</span>,
                    <Pill key="s">{entry.size}px</Pill>,
                    <Mono key="lh">{entry.lineHeight}px</Mono>,
                    <Mono key="ls">{entry.letterSpacing}px</Mono>,
                  ];
                  if (!isMobile) {
                    base.splice(1, 0,
                      <div key="p" style={{
                        ...previewStyle(entry),
                        fontWeight: label === "Heading" ? 700 : label === "Action" ? 600 : 400,
                        color: label === "Action" ? "var(--accent)" : "var(--text-1)",
                      }}>
                        {preview()}
                      </div>
                    );
                  }
                  return base;
                })}
              />
            </div>
          </motion.div>
        ))}
      </motion.div>

      {/* ── TYPE HIERARCHY ── */}
      <motion.div
        id="type-hierarchy"
        variants={stagger} initial="hidden" whileInView="visible" viewport={{ once: true, margin: "-40px" }}
        style={{ marginBottom: 48, scrollMarginTop: "calc(var(--header-h) + 32px)" }}
      >
        <motion.h3 variants={itemFadeUp} style={{ fontSize: 18, fontWeight: 600, letterSpacing: "-0.3px", color: "var(--text-1)", marginBottom: 8 }}>
          Type hierarchy
        </motion.h3>
        <motion.p variants={itemFadeUp} style={{ fontSize: 14, color: "var(--text-2)", marginBottom: 24, maxWidth: 640 }}>
          Three categories cover every content role across product, ads, and marketing surfaces.
        </motion.p>

        <motion.div
          variants={stagger}
          style={{ display: "grid", gridTemplateColumns: isMobile ? "1fr" : "repeat(3, 1fr)", gap: 12 }}
        >
          {[
            { letter: "H", name: "Heading", color: "#0f61ff", desc: "H16–H40. Bold weight, tight letter-spacing. Page titles, section headers, display text." },
            { letter: "B", name: "Body",    color: "#0f8857", desc: "B11–B16. Regular to bold. Paragraphs, descriptions, labels, and metadata." },
            { letter: "A", name: "Action",  color: "#e5641a", desc: "A12–A17. SemiBold or Bold. Buttons, links, CTAs, and interactive labels." },
          ].map((item) => (
            <motion.div
              key={item.name}
              variants={itemFadeUp}
              whileHover={{ y: -3, boxShadow: "0 8px 24px rgba(0,0,0,0.12)" }}
              transition={{ type: "spring", stiffness: 300, damping: 22 }}
              style={{ background: "var(--bg-surface)", border: "1px solid var(--border)", borderRadius: 8, padding: 20 }}
            >
              <div style={{
                display: "inline-flex", alignItems: "center", justifyContent: "center",
                width: 28, height: 28, borderRadius: 6,
                background: item.color + "22", color: item.color,
                fontSize: 11, fontWeight: 700, marginBottom: 10,
              }}>
                {item.letter}
              </div>
              <div style={{ fontSize: 13, fontWeight: 600, color: "var(--text-1)", marginBottom: 4 }}>{item.name}</div>
              <div style={{ fontSize: 12, color: "var(--text-3)", lineHeight: 1.5 }}>{item.desc}</div>
            </motion.div>
          ))}
        </motion.div>
      </motion.div>

      {/* ── TYPE STYLES ── */}
      <div id="type-styles" style={{ scrollMarginTop: "calc(var(--header-h) + 32px)" }}>
        <motion.h3
          variants={fadeUp} initial="hidden" whileInView="visible" viewport={{ once: true }}
          style={{ fontSize: 18, fontWeight: 600, letterSpacing: "-0.3px", color: "var(--text-1)", marginBottom: 8 }}
        >
          Type styles
        </motion.h3>
        <motion.p
          variants={fadeUp} initial="hidden" whileInView="visible" viewport={{ once: true }}
          style={{ fontSize: 14, color: "var(--text-2)", marginBottom: 24, maxWidth: 640 }}
        >
          Each style has multiple weight variants. Heading styles are always Bold or Extrabold.
        </motion.p>

        <motion.div
          variants={stagger} initial="hidden" whileInView="visible" viewport={{ once: true, margin: "-40px" }}
          style={{ display: "grid", gridTemplateColumns: isMobile ? "1fr" : "1fr 1fr", gap: 16 }}
        >
          {[
            {
              label: "H40 · Bold", category: "Heading",
              style: { fontSize: isMobile ? 32 : 40, fontWeight: 700, lineHeight: "48px", letterSpacing: "-0.25px" } as React.CSSProperties,
              preview: "Shop timeless style",
              specs: [["Size","40px"],["Line height","48px"],["Tracking","−0.25px"],["Weight","700"]],
            },
            {
              label: "H24 · Bold", category: "Heading",
              style: { fontSize: 24, fontWeight: 700, lineHeight: "32px", letterSpacing: "-0.25px" } as React.CSSProperties,
              preview: "Money Back Guarantee",
              specs: [["Size","24px"],["Line height","32px"],["Tracking","−0.25px"],["Weight","700"]],
            },
            {
              label: "B16 · Regular", category: "Body",
              style: { fontSize: 16, fontWeight: 400, lineHeight: "20px", letterSpacing: "-0.15px" } as React.CSSProperties,
              preview: "Get 5.9% APR with 12 or 24 easy payments on purchases.",
              specs: [["Size","16px"],["Line height","20px"],["Tracking","−0.15px"],["Weight","400"]],
            },
            {
              label: "B14 · Regular", category: "Body",
              style: { fontSize: 14, fontWeight: 400, lineHeight: "18px", letterSpacing: "-0.1px" } as React.CSSProperties,
              preview: "Body copy is always used for descriptive text in short strings.",
              specs: [["Size","14px"],["Line height","18px"],["Tracking","−0.1px"],["Weight","400"]],
            },
            {
              label: "A16 · SemiBold", category: "Action",
              style: { fontSize: 16, fontWeight: 600, lineHeight: "24px", letterSpacing: "0px", color: "#0f61ff" } as React.CSSProperties,
              preview: "Add to bag",
              specs: [["Size","16px"],["Line height","24px"],["Tracking","0px"],["Weight","600"]],
            },
            {
              label: "A14 · SemiBold", category: "Action",
              style: { fontSize: 14, fontWeight: 600, lineHeight: "20px", letterSpacing: "0px", color: "#0f61ff" } as React.CSSProperties,
              preview: "Buy now",
              specs: [["Size","14px"],["Line height","20px"],["Tracking","0px"],["Weight","600"]],
            },
          ].map((card) => {
            const catColor = CATEGORY_COLORS[card.category];
            return (
              <motion.div
                key={card.label}
                variants={itemFadeUp}
                whileHover={{ y: -2, boxShadow: "0 8px 32px rgba(0,0,0,0.1)" }}
                transition={{ type: "spring", stiffness: 300, damping: 22 }}
                style={{ background: "var(--bg-surface)", border: "1px solid var(--border)", borderRadius: 8, padding: 20 }}
              >
                <div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 16 }}>
                  <span style={{ fontSize: 11, fontWeight: 600, color: "var(--text-3)", textTransform: "uppercase", letterSpacing: "0.07em" }}>
                    {card.label}
                  </span>
                  <Badge style={{ fontSize: 9, fontWeight: 700, textTransform: "uppercase", letterSpacing: "0.05em", padding: "1px 6px", borderRadius: 3, background: catColor + "22", color: catColor, border: "none" }}>
                    {card.category}
                  </Badge>
                </div>
                <div style={{ ...card.style, marginBottom: 16, overflow: "hidden", color: (card.style as React.CSSProperties).color ?? "var(--text-1)" }}>
                  {card.preview}
                </div>
                <div style={{ display: "flex", gap: 12, flexWrap: "wrap", borderTop: "1px solid var(--border)", paddingTop: 14 }}>
                  {card.specs.map(([l, v]) => (
                    <div key={l}>
                      <div style={{ fontSize: 10, color: "var(--text-3)" }}>{l}</div>
                      <div style={{ fontSize: 11, fontFamily: "var(--font-mono)", color: "var(--text-2)" }}>{v}</div>
                    </div>
                  ))}
                </div>
              </motion.div>
            );
          })}
        </motion.div>
      </div>
    </section>
  );
}
