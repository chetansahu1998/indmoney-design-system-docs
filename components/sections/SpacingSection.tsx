"use client";
import { motion } from "framer-motion";
import { spacing } from "@/lib/tokens";
import SectionHeading from "@/components/ui/SectionHeading";
import DSTable from "@/components/ui/DSTable";
import { fadeUp, stagger, itemFadeUp } from "@/lib/motion-variants";
import { useIsMobile } from "@/lib/use-mobile";

const MAX_BAR = 72;

export default function SpacingSection() {
  const isMobile = useIsMobile();
  return (
    <section id="spacing" style={{ marginBottom: 80, scrollMarginTop: "calc(var(--header-h) + 32px)" }}>
      <SectionHeading id="spacing" title="Spacing" />

      <motion.p
        variants={fadeUp} initial="hidden" whileInView="visible" viewport={{ once: true }}
        style={{ fontSize: 16, color: "var(--text-2)", lineHeight: 1.65, maxWidth: 640, marginBottom: 48 }}
      >
        Spacing tokens use literal pixel values rather than a multiplier grid. Token names match the value directly —{" "}
        <code style={{ fontFamily: "var(--font-mono)", fontSize: 12, background: "var(--bg-surface)", padding: "1px 5px", borderRadius: 3, color: "var(--accent)" }}>
          space.16
        </code>{" "}
        equals 16px. Always use tokens — never raw pixel values in components.
      </motion.p>

      {/* ── Spacing scale ── */}
      <motion.div
        id="spacing-scale"
        variants={stagger} initial="hidden" whileInView="visible" viewport={{ once: true, margin: "-40px" }}
        style={{ marginBottom: 48, scrollMarginTop: "calc(var(--header-h) + 32px)" }}
      >
        <motion.h3
          variants={itemFadeUp}
          style={{ fontSize: 18, fontWeight: 600, letterSpacing: "-0.3px", color: "var(--text-1)", marginBottom: 24 }}
        >
          Spacing scale
        </motion.h3>

        <div style={{ display: "flex", flexDirection: "column", gap: 4 }}>
          {spacing.scale.map((s, i) => (
            <motion.div
              key={s.token}
              initial={{ opacity: 0, x: -8 }}
              whileInView={{ opacity: 1, x: 0 }}
              viewport={{ once: true, margin: "-10px" }}
              transition={{ delay: i * 0.025, duration: 0.3, ease: [0.33, 1, 0.68, 1] }}
              whileHover={{ backgroundColor: "var(--bg-surface-2)" }}
              style={{
                display: "grid", gridTemplateColumns: isMobile ? "80px 1fr 48px" : "110px 1fr 60px",
                alignItems: "center", gap: isMobile ? 10 : 16,
                padding: "10px 16px",
                background: "var(--bg-surface)",
                border: "1px solid var(--border)",
                borderRadius: 6,
                transition: "background 0.15s",
              }}
            >
              <span style={{ fontFamily: "var(--font-mono)", fontSize: 12, color: "var(--text-3)" }}>{s.token}</span>
              <div style={{ height: 16, display: "flex", alignItems: "center" }}>
                {s.px > 0 && (
                  <motion.div
                    initial={{ width: 0, opacity: 0 }}
                    whileInView={{
                      width: Math.max(2, Math.round((s.px / MAX_BAR) * 200)),
                      opacity: 0.55,
                    }}
                    viewport={{ once: true }}
                    transition={{ delay: i * 0.025 + 0.1, duration: 0.5, ease: [0.33, 1, 0.68, 1] }}
                    style={{ height: 10, borderRadius: 2, background: "var(--accent)" }}
                  />
                )}
              </div>
              <span style={{ fontFamily: "var(--font-mono)", fontSize: 12, fontWeight: 600, color: "var(--text-2)", textAlign: "right" }}>
                {s.px}px
              </span>
            </motion.div>
          ))}
        </div>
      </motion.div>

      {/* ── Border radius ── */}
      <motion.div
        id="spacing-radius"
        variants={fadeUp} initial="hidden" whileInView="visible" viewport={{ once: true, margin: "-40px" }}
        style={{ scrollMarginTop: "calc(var(--header-h) + 32px)" }}
      >
        <h3 style={{ fontSize: 18, fontWeight: 600, letterSpacing: "-0.3px", color: "var(--text-1)", marginBottom: 8 }}>Border radius</h3>
        <p style={{ fontSize: 14, color: "var(--text-2)", marginBottom: 24 }}>
          Corner radius tokens applied consistently across all interactive and container components.
        </p>
        <div className="ds-table-scroll">
        <DSTable
          headers={["Token", "Value", "Preview"]}
          rows={spacing.radius.map((r) => [
            <span key="tok" style={{ fontFamily: "var(--font-mono)", fontSize: 12, color: "var(--text-3)" }}>{r.token}</span>,
            <span key="val" style={{ fontFamily: "var(--font-mono)", fontSize: 12, color: "var(--text-2)" }}>
              {r.px === 9999 ? "9999px" : `${r.px}px`}
            </span>,
            <motion.div
              key="prev"
              whileHover={{ scale: 1.1 }}
              transition={{ type: "spring", stiffness: 300, damping: 22 }}
              style={{
                width: 56, height: 24,
                background: "var(--accent)", opacity: 0.55,
                borderRadius: Math.min(r.px, 12),
              }}
            />,
          ])}
        />
        </div>
      </motion.div>
    </section>
  );
}
