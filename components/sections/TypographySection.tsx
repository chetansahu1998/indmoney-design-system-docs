"use client";
import { useMemo, useState } from "react";
import { motion } from "framer-motion";
import SectionHeading from "@/components/ui/SectionHeading";
import { fadeUp, stagger, itemFadeUp } from "@/lib/motion-variants";
import { useIsMobile } from "@/lib/use-mobile";
import { loadTypography, typographyByCategory, type DereferencedTypography } from "@/lib/tokens/typography";

const CATEGORY_ORDER = ["heading", "subtitle", "body", "caption", "overline", "small", "other"];
const CATEGORY_LABEL: Record<string, string> = {
  heading: "Headings",
  subtitle: "Subtitles",
  body: "Body",
  caption: "Caption",
  overline: "Overline",
  small: "Small",
  other: "Other",
};

const SAMPLE_TEXTS: Record<string, string> = {
  heading: "₹1,24,500.50",
  subtitle: "Today's Portfolio",
  body: "The quick brown fox jumps over the lazy dog.",
  caption: "Last updated 2 hours ago",
  overline: "MARKET STATUS",
  small: "T&C apply",
  other: "Sample text",
};

function fontMetricsLabel(t: DereferencedTypography): string {
  const lh = t.lineHeight ? `${Math.round(t.lineHeight)}px` : "—";
  const ls = t.letterSpacing ? ` · ${t.letterSpacing.toFixed(2)}px` : "";
  return `${t.fontSize}px / ${lh} · ${weightName(t.fontWeight)}${ls}`;
}

function weightName(w: number): string {
  switch (w) {
    case 100: return "Thin";
    case 200: return "ExtraLight";
    case 300: return "Light";
    case 400: return "Regular";
    case 500: return "Medium";
    case 600: return "SemiBold";
    case 700: return "Bold";
    case 800: return "ExtraBold";
    case 900: return "Black";
    default: return `${w}`;
  }
}

function TypographyRow({ token, sample }: { token: DereferencedTypography; sample: string }) {
  const isMobile = useIsMobile();
  const [copied, setCopied] = useState(false);
  const cssSnippet = useMemo(
    () =>
      `font-family: "${token.fontFamily}";\nfont-weight: ${token.fontWeight};\nfont-size: ${token.fontSize}px;\nline-height: ${token.lineHeight}px;\nletter-spacing: ${token.letterSpacing}px;`,
    [token],
  );

  const copy = () => {
    navigator.clipboard.writeText(cssSnippet).catch(() => {});
    setCopied(true);
    setTimeout(() => setCopied(false), 1200);
  };

  return (
    <motion.div
      variants={itemFadeUp}
      data-token={`text.${token.slug}`}
      onClick={copy}
      style={{
        display: "grid",
        gridTemplateColumns: isMobile ? "1fr" : "minmax(220px, 1fr) auto",
        gap: 16,
        alignItems: "center",
        padding: "20px 18px",
        background: "var(--bg-surface)",
        border: "1px solid var(--border)",
        borderRadius: 10,
        cursor: "pointer",
        marginBottom: 8,
      }}
    >
      <div style={{ minWidth: 0 }}>
        <div
          style={{
            fontFamily: `"${token.fontFamily}", -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif`,
            fontSize: token.fontSize,
            fontWeight: token.fontWeight,
            lineHeight: token.lineHeight ? `${token.lineHeight}px` : 1.3,
            letterSpacing: token.letterSpacing ? `${token.letterSpacing}px` : "normal",
            color: "var(--text-1)",
            marginBottom: 6,
            wordBreak: "break-word",
          }}
        >
          {sample}
        </div>
        <div
          style={{
            fontSize: 11,
            fontFamily: "var(--font-mono)",
            color: "var(--text-3)",
          }}
        >
          {token.slug} · {fontMetricsLabel(token)}
        </div>
      </div>

      <div
        style={{
          fontFamily: "var(--font-mono)",
          fontSize: 10,
          color: copied ? "var(--accent)" : "var(--text-3)",
          whiteSpace: "nowrap",
        }}
      >
        {copied ? "Copied CSS" : "Click to copy"}
      </div>
    </motion.div>
  );
}

export default function TypographySection() {
  const all = useMemo(() => loadTypography(), []);
  const grouped = useMemo(() => typographyByCategory(), []);
  const orderedCats = useMemo(
    () => [
      ...CATEGORY_ORDER.filter((c) => grouped.has(c)),
      ...Array.from(grouped.keys()).filter((c) => !CATEGORY_ORDER.includes(c)),
    ],
    [grouped],
  );

  const fontFamilies = useMemo(() => {
    const set = new Set(all.map((t) => t.fontFamily));
    return Array.from(set);
  }, [all]);

  return (
    <section
      id="typography"
      style={{ marginBottom: 80, scrollMarginTop: "calc(var(--header-h) + 32px)" }}
    >
      <SectionHeading id="typography" title="Typography" />

      <motion.p
        variants={fadeUp}
        initial="hidden"
        whileInView="visible"
        viewport={{ once: true }}
        style={{
          fontSize: 16,
          color: "var(--text-2)",
          lineHeight: 1.65,
          maxWidth: 720,
          marginBottom: 8,
        }}
      >
        {all.length} type styles extracted from Glyph's published TEXT styles
        (dereferenced for full font metadata). Click any sample to copy its CSS.
      </motion.p>
      <motion.p
        variants={fadeUp}
        initial="hidden"
        whileInView="visible"
        viewport={{ once: true }}
        style={{
          fontSize: 12,
          color: "var(--text-3)",
          fontFamily: "var(--font-mono)",
          marginBottom: 32,
        }}
      >
        Font family: {fontFamilies.join(", ")}
      </motion.p>

      {orderedCats.map((cat) => {
        const tokens = grouped.get(cat) ?? [];
        if (tokens.length === 0) return null;
        return (
          <motion.div
            key={cat}
            id={`type-${cat}`}
            variants={fadeUp}
            initial="hidden"
            whileInView="visible"
            viewport={{ once: true, margin: "-40px" }}
            style={{ marginBottom: 32, scrollMarginTop: "calc(var(--header-h) + 32px)" }}
          >
            <h3
              style={{
                fontSize: 16,
                fontWeight: 600,
                letterSpacing: "-0.2px",
                color: "var(--text-1)",
                marginBottom: 14,
              }}
            >
              {CATEGORY_LABEL[cat] ?? cat} · {tokens.length}
            </h3>
            <motion.div
              variants={stagger}
              initial="hidden"
              whileInView="visible"
              viewport={{ once: true, margin: "-30px" }}
            >
              {tokens.map((t) => (
                <TypographyRow key={t.slug} token={t} sample={SAMPLE_TEXTS[cat] ?? "Sample"} />
              ))}
            </motion.div>
          </motion.div>
        );
      })}
    </section>
  );
}
