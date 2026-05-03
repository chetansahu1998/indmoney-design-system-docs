"use client";
import { motion } from "framer-motion";
import SectionHeading from "@/components/ui/SectionHeading";
import { fadeUp } from "@/lib/motion-variants";
import { shadowTokens, shadowsProvenance, toCSS, type ShadowToken } from "@/lib/tokens/effects";
import { showToast } from "@/components/ui/Toast";

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

  if (tokens.length === 0) return null;

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
    </section>
  );
}
