"use client";
import { motion } from "framer-motion";
import { spacingScale, paddingScale, radiusScale, spacingProvenance } from "@/lib/tokens/spacing";
import SectionHeading from "@/components/ui/SectionHeading";
import DSTable from "@/components/ui/DSTable";
import { fadeUp, stagger, itemFadeUp } from "@/lib/motion-variants";
import { useIsMobile } from "@/lib/use-mobile";
import UsageChip from "@/components/audit/UsageChip";

export default function SpacingSection() {
  const isMobile = useIsMobile();
  const scale = spacingScale();
  const padding = paddingScale();
  const radius = radiusScale();
  const provenance = spacingProvenance();
  // Bar scale auto-adapts so the largest token still fits in a sensible width.
  const MAX_BAR = Math.max(72, ...scale.map((s) => s.px));
  const isFigmaScan = provenance === "figma-layout-scan";
  return (
    <section id="spacing" style={{ marginBottom: 80, scrollMarginTop: "calc(var(--header-h) + 32px)" }}>
      <SectionHeading id="spacing" title="Spacing" />

      <motion.p
        variants={fadeUp} initial="hidden" whileInView="visible" viewport={{ once: true }}
        style={{ fontSize: 16, color: "var(--text-2)", lineHeight: 1.65, maxWidth: 640, marginBottom: 12 }}
      >
        Spacing tokens use literal pixel values rather than a multiplier grid. Token names match the value directly —{" "}
        <code style={{ fontFamily: "var(--font-mono)", fontSize: 12, background: "var(--bg-surface)", padding: "1px 5px", borderRadius: 3, color: "var(--accent)" }}>
          space.16
        </code>{" "}
        equals 16px. Always use tokens — never raw pixel values in components.
      </motion.p>

      <p style={{ fontSize: 12, color: "var(--text-3)", fontFamily: "var(--font-mono)", marginBottom: 16 }}>
        {scale.length} space · {padding.length} padding · {radius.length} radius · source: {provenance}
      </p>

      {isFigmaScan && (
        <div style={{
          padding: "10px 14px", marginBottom: 28,
          background: "var(--bg-surface)",
          border: "1px solid var(--border)", borderLeft: "3px solid var(--accent)",
          borderRadius: 6, fontSize: 12, color: "var(--text-2)", lineHeight: 1.6,
        }}>
          <strong style={{ color: "var(--text-1)" }}>Discovered via layout-pattern scan.</strong>{" "}
          Each value below is a real number used by Glyph + Atoms component frames.
          The <code style={{ fontFamily: "var(--font-mono)" }}>×N</code> count shows how many
          frames use it — high counts are de-facto tokens, low counts are candidates for
          consolidation.
        </div>
      )}
      {provenance === "hand-curated" && (
        <div style={{
          padding: "10px 14px", marginBottom: 28,
          background: "var(--bg-surface)",
          border: "1px solid var(--border)", borderLeft: "3px solid var(--accent)",
          borderRadius: 6, fontSize: 12, color: "var(--text-2)", lineHeight: 1.6,
        }}>
          <strong style={{ color: "var(--text-1)" }}>Live preview, hand-curated values.</strong>{" "}
          Run{" "}
          <code style={{ fontFamily: "var(--font-mono)", color: "var(--accent)" }}>
            go run ./services/ds-service/cmd/variables --brand indmoney
          </code>{" "}
          to replace these with values discovered from Glyph + Atoms components.
        </div>
      )}

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
          {scale.map((s, i) => (
            <motion.div
              key={s.token}
              initial={{ opacity: 0, x: -8 }}
              whileInView={{ opacity: 1, x: 0 }}
              viewport={{ once: true, margin: "-10px" }}
              transition={{ delay: i * 0.025, duration: 0.3, ease: [0.33, 1, 0.68, 1] }}
              whileHover={{ backgroundColor: "var(--bg-surface-2)" }}
              style={{
                display: "grid",
                // Bar column is fluid (1fr) instead of fixed 200px — narrow
                // viewports and wide ones both render the bar at the right
                // proportion. The mobile layout adds the usage count next to
                // px so the audit signal isn't lost (it used to be desktop-only).
                gridTemplateColumns: isMobile
                  ? (isFigmaScan ? "84px 1fr 56px 48px" : "84px 1fr 56px")
                  : (isFigmaScan ? "120px 1fr 64px 76px" : "120px 1fr 64px"),
                alignItems: "center", gap: isMobile ? 8 : 16,
                padding: "10px 16px",
                background: "var(--bg-surface)",
                border: "1px solid var(--border)",
                borderRadius: 6,
                transition: "background 0.15s",
              }}
            >
              <span style={{ fontFamily: "var(--font-mono)", fontSize: 12, color: "var(--text-3)", display: "inline-flex", alignItems: "center", gap: 6 }}>
                {s.token}
                <UsageChip tokenPath={s.token} size="sm" />
              </span>
              <div style={{ height: 16, display: "flex", alignItems: "center", minWidth: 0 }}>
                {s.px > 0 && (
                  <motion.div
                    initial={{ width: 0, opacity: 0 }}
                    whileInView={{
                      // Width as a percentage of the column so the bar
                      // scales with viewport instead of being clamped
                      // to a 200px ceiling that overflowed mobile.
                      width: `${Math.max(2, Math.min(100, (s.px / MAX_BAR) * 100))}%`,
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
              {isFigmaScan && (
                <span
                  title={`Used by ${s.usageCount ?? 0} frames`}
                  style={{
                    fontFamily: "var(--font-mono)",
                    fontSize: isMobile ? 10 : 11,
                    color: "var(--text-3)",
                    textAlign: "right",
                  }}
                >
                  ×{s.usageCount ?? 0}
                </span>
              )}
            </motion.div>
          ))}
        </div>
      </motion.div>

      {/* ── Padding (auto-layout) ── */}
      {padding.length > 0 && (
        <motion.div
          id="spacing-padding"
          variants={fadeUp} initial="hidden" whileInView="visible" viewport={{ once: true, margin: "-40px" }}
          style={{ marginBottom: 48, scrollMarginTop: "calc(var(--header-h) + 32px)" }}
        >
          <h3 style={{ fontSize: 18, fontWeight: 600, letterSpacing: "-0.3px", color: "var(--text-1)", marginBottom: 8 }}>
            Padding scale
          </h3>
          <p style={{ fontSize: 14, color: "var(--text-2)", marginBottom: 16 }}>
            {isFigmaScan
              ? "Auto-layout padding values discovered across Glyph + Atoms component frames. Sorted by usage."
              : "Padding values used in interactive containers."}
          </p>
          <div
            style={{
              display: "grid",
              gridTemplateColumns: `repeat(auto-fill, minmax(${isMobile ? 110 : 130}px, 1fr))`,
              gap: 8,
            }}
          >
            {padding.map((p) => (
              <div
                key={p.token}
                style={{
                  display: "flex",
                  alignItems: "center",
                  gap: 10,
                  padding: "10px 12px",
                  background: "var(--bg-surface)",
                  border: "1px solid var(--border)",
                  borderRadius: 6,
                }}
              >
                <span
                  style={{
                    width: 22,
                    height: 22,
                    borderRadius: 3,
                    border: "1.5px dashed var(--accent)",
                    flexShrink: 0,
                    position: "relative",
                  }}
                >
                  <span
                    style={{
                      position: "absolute",
                      inset: Math.min(p.px / 4, 8),
                      background: "var(--accent)",
                      opacity: 0.55,
                      borderRadius: 2,
                    }}
                  />
                </span>
                <div style={{ display: "flex", flexDirection: "column", minWidth: 0, lineHeight: 1.2 }}>
                  <span style={{ fontFamily: "var(--font-mono)", fontSize: 12, fontWeight: 600, color: "var(--text-1)" }}>
                    {p.px}px
                  </span>
                  <span style={{ fontFamily: "var(--font-mono)", fontSize: 10, color: "var(--text-3)" }}>
                    {isFigmaScan ? `×${p.usageCount ?? 0}` : p.token}
                  </span>
                </div>
              </div>
            ))}
          </div>
        </motion.div>
      )}

      {/* ── Border radius ── */}
      <motion.div
        id="spacing-radius"
        variants={fadeUp} initial="hidden" whileInView="visible" viewport={{ once: true, margin: "-40px" }}
        style={{ scrollMarginTop: "calc(var(--header-h) + 32px)" }}
      >
        <h3 style={{ fontSize: 18, fontWeight: 600, letterSpacing: "-0.3px", color: "var(--text-1)", marginBottom: 8 }}>Border radius</h3>
        <p style={{ fontSize: 14, color: "var(--text-2)", marginBottom: 24 }}>
          {isFigmaScan
            ? "Corner-radius values discovered on Glyph + Atoms components. Sorted by px."
            : "Corner radius tokens applied consistently across all interactive and container components."}
        </p>
        <div className="ds-table-scroll">
        <DSTable
          headers={isFigmaScan ? ["Token", "Value", "Preview", "Usage"] : ["Token", "Value", "Preview"]}
          rows={radius.map((r) => {
            const cells = [
              <span key="tok" style={{ fontFamily: "var(--font-mono)", fontSize: 12, color: "var(--text-3)" }}>{r.token}</span>,
              <span key="val" style={{ fontFamily: "var(--font-mono)", fontSize: 12, color: "var(--text-2)" }}>
                {r.px >= 999 ? "pill" : `${r.px}px`}
              </span>,
              <motion.div
                key="prev"
                whileHover={{ scale: 1.1 }}
                transition={{ type: "spring", stiffness: 300, damping: 22 }}
                style={{
                  width: 56, height: 24,
                  background: "var(--accent)", opacity: 0.55,
                  borderRadius: Math.min(r.px, 28),
                }}
              />,
            ];
            if (isFigmaScan) {
              cells.push(
                <span key="usage" style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--text-3)" }}>
                  ×{r.usageCount ?? 0}
                </span>,
              );
            }
            return cells;
          })}
        />
        </div>
      </motion.div>
    </section>
  );
}
