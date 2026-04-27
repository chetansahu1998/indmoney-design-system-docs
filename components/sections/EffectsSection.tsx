"use client";
import { motion } from "framer-motion";
import SectionHeading from "@/components/ui/SectionHeading";
import DataGapPreview from "@/components/ui/DataGapPreview";
import { fadeUp } from "@/lib/motion-variants";
import { shadowTokens, shadowsProvenance, toCSS, type ShadowToken } from "@/lib/tokens/effects";
import { showToast } from "@/components/ui/Toast";

/* Sample preview — shows what an "elevation 1 / 2 / 3" ramp looks like,
   so the empty state previews the SHAPE of the data instead of being blank. */
const SAMPLE_RAMP = [
  { name: "elevation/1", layers: "0 1px 2px rgba(0,0,0,0.06), 0 1px 3px rgba(0,0,0,0.10)" },
  { name: "elevation/2", layers: "0 4px 6px rgba(0,0,0,0.05), 0 10px 15px rgba(0,0,0,0.10)" },
  { name: "elevation/3", layers: "0 10px 20px rgba(0,0,0,0.06), 0 24px 40px rgba(0,0,0,0.12)" },
];

function SamplePreview() {
  return (
    <div style={{ display: "flex", gap: 24, alignItems: "center", flexWrap: "wrap", justifyContent: "center" }}>
      {SAMPLE_RAMP.map((s, i) => (
        <motion.div
          key={s.name}
          initial={{ opacity: 0, y: 14 }}
          animate={{ opacity: 1, y: 0 }}
          transition={{ delay: 0.15 + i * 0.12, duration: 0.45, ease: [0.33, 1, 0.68, 1] }}
          style={{ display: "flex", flexDirection: "column", alignItems: "center", gap: 10 }}
        >
          <motion.div
            animate={{ y: [0, -2, 0] }}
            transition={{ delay: 0.5 + i * 0.12, duration: 2.2, repeat: Infinity, repeatType: "mirror", ease: "easeInOut" }}
            style={{
              width: 72,
              height: 48,
              background: "var(--bg-surface)",
              border: "1px solid var(--border)",
              borderRadius: 8,
              boxShadow: s.layers,
            }}
          />
          <span style={{ fontSize: 10, fontFamily: "var(--font-mono)", color: "var(--text-3)" }}>
            {s.name}
          </span>
        </motion.div>
      ))}
    </div>
  );
}

function ShadowCard({ token }: { token: ShadowToken }) {
  const css = toCSS(token);

  const copy = () => {
    navigator.clipboard.writeText(css).catch(() => {});
    showToast({
      message: `Copied shadow CSS`,
      detail: token.path,
      tone: "success",
    });
  };

  return (
    <button
      onClick={copy}
      data-token={token.path}
      style={{
        display: "flex",
        flexDirection: "column",
        gap: 14,
        padding: 20,
        background: "var(--bg-surface)",
        border: "1px solid var(--border)",
        borderRadius: 10,
        textAlign: "left",
        cursor: "pointer",
      }}
    >
      <div style={{ display: "flex", alignItems: "center", justifyContent: "center", height: 96 }}>
        <div
          style={{
            width: 80,
            height: 56,
            background: "var(--bg-surface-2)",
            border: "1px solid var(--border)",
            borderRadius: 8,
            boxShadow: css,
          }}
        />
      </div>
      <div>
        <div style={{ fontSize: 13, fontWeight: 600, color: "var(--text-1)", letterSpacing: "-0.1px" }}>
          {token.name}
        </div>
        <div
          style={{
            fontSize: 10,
            fontFamily: "var(--font-mono)",
            color: "var(--text-3)",
            marginTop: 4,
            wordBreak: "break-all",
          }}
        >
          {token.path}
        </div>
      </div>
    </button>
  );
}

export default function EffectsSection() {
  const tokens = shadowTokens();
  const provenance = shadowsProvenance();
  const isEmpty = tokens.length === 0;

  return (
    <section
      id="effects"
      style={{ marginBottom: 80, scrollMarginTop: "calc(var(--header-h) + 32px)" }}
    >
      <SectionHeading id="effects" title="Effects" />

      <motion.p
        variants={fadeUp}
        initial="hidden"
        whileInView="visible"
        viewport={{ once: true }}
        style={{ fontSize: 16, color: "var(--text-2)", lineHeight: 1.65, maxWidth: 640, marginBottom: 12 }}
      >
        Shadow tokens are extracted from Glyph — first via published EFFECT styles, falling
        back to a node-tree scan that finds inline shadows on FRAME / COMPONENT layers.
      </motion.p>
      <p style={{ fontSize: 12, color: "var(--text-3)", fontFamily: "var(--font-mono)", marginBottom: 28 }}>
        {tokens.length} shadow tokens · source: {provenance}
      </p>

      {isEmpty ? (
        <DataGapPreview
          diagnosis={
            <>
              Glyph publishes <strong style={{ color: "var(--text-1)" }}>0 EFFECT styles</strong>,
              and the node-tree scanner found <strong style={{ color: "var(--text-1)" }}>0 inline
              shadows</strong> on the design-system page. Either Glyph hasn&apos;t formalized an
              elevation ramp yet, or shadows live on a different page.
            </>
          }
          unlock={
            <>
              Set <code style={{ fontFamily: "var(--font-mono)" }}>FIGMA_NODE_ID_INDMONEY_GLYPH</code>{" "}
              to a Glyph page that contains shadow components (e.g. cards, sheets, modals), then
              re-run the pipeline.
            </>
          }
          command="go run ./services/ds-service/cmd/effects --brand indmoney"
          preview={<SamplePreview />}
        />
      ) : (
        <div
          style={{
            display: "grid",
            gridTemplateColumns: "repeat(auto-fill, minmax(220px, 1fr))",
            gap: 14,
          }}
        >
          {tokens.map((t) => (
            <ShadowCard key={t.path} token={t} />
          ))}
        </div>
      )}
    </section>
  );
}
