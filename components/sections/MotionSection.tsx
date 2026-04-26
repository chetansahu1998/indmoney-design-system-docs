"use client";
import { useState } from "react";
import { motion } from "framer-motion";
import { motion as motionTokens } from "@/lib/tokens";
import SectionHeading from "@/components/ui/SectionHeading";
import DSTable from "@/components/ui/DSTable";
import { fadeUp, stagger, itemFadeUp } from "@/lib/motion-variants";
import { useIsMobile } from "@/lib/use-mobile";

const Code = ({ children }: { children: React.ReactNode }) => (
  <code style={{
    display: "block",
    fontFamily: "var(--font-mono)", fontSize: 11,
    background: "var(--bg-surface-2)", color: "var(--accent)",
    padding: "10px 14px", borderRadius: 6,
    border: "1px solid var(--border)",
    whiteSpace: "pre", overflowX: "auto",
    lineHeight: 1.7,
  }}>{children}</code>
);

const Tag = ({ children, color }: { children: React.ReactNode; color: string }) => (
  <span style={{
    display: "inline-block", fontSize: 9, fontWeight: 700,
    textTransform: "uppercase", letterSpacing: "0.06em",
    padding: "1px 6px", borderRadius: 3,
    background: color + "22", color,
  }}>{children}</span>
);

function SpringCurve({ damping }: { damping: 24 | 26 | 28 }) {
  const paths: Record<number, string> = {
    24: "M0 60 C10 60 15 4 30 6 S50 14 60 10 70 8 80 9 90 9.5 100 10",
    26: "M0 60 C10 60 18 6 32 8 S55 13 70 11 85 10 100 10",
    28: "M0 60 C12 60 22 8 38 10 S65 12 80 11 90 10.5 100 10",
  };
  return (
    <svg width={100} height={70} viewBox="0 0 100 70" style={{ display: "block" }}>
      <line x1="0" y1="10" x2="100" y2="10" stroke="var(--border)" strokeWidth="0.5" strokeDasharray="2 2" />
      <motion.path
        d={paths[damping]}
        fill="none"
        stroke="var(--accent)"
        strokeWidth="1.5"
        strokeLinecap="round"
        initial={{ pathLength: 0, opacity: 0 }}
        whileInView={{ pathLength: 1, opacity: 1 }}
        viewport={{ once: true }}
        transition={{ duration: 0.8, ease: "easeOut", delay: 0.1 }}
      />
      <circle cx="0" cy="60" r="2" fill="var(--accent)" />
      <motion.circle
        cx="0" cy="60" r="3"
        fill="var(--accent)"
        initial={{ cx: 0, cy: 60 }}
        whileInView={{ cx: 100, cy: 10 }}
        viewport={{ once: true }}
        transition={{ duration: 0.8, ease: "easeOut", delay: 0.1 }}
      />
    </svg>
  );
}

function EasingCurve({ type }: { type: "ease-out" | "ease-in-out" }) {
  const path = type === "ease-out"
    ? "M0 60 C10 60 20 10 100 10"
    : "M0 60 C30 60 70 10 100 10";
  const color = type === "ease-out" ? "#0f8857" : "var(--accent)";
  return (
    <svg width={100} height={70} viewBox="0 0 100 70" style={{ display: "block" }}>
      <line x1="0" y1="10" x2="100" y2="10" stroke="var(--border)" strokeWidth="0.5" strokeDasharray="2 2" />
      <motion.path
        d={path}
        fill="none"
        stroke={color}
        strokeWidth="1.5"
        strokeLinecap="round"
        initial={{ pathLength: 0 }}
        whileInView={{ pathLength: 1 }}
        viewport={{ once: true }}
        transition={{ duration: 0.7, ease: "easeOut" }}
      />
      <circle cx="0" cy="60" r="2" fill={color} />
      <circle cx="100" cy="10" r="2" fill={color} />
    </svg>
  );
}

/* Interactive press-feedback demo */
function PressDemo() {
  const [pressing, setPressing] = useState(false);
  return (
    <div style={{ display: "flex", alignItems: "center", gap: 20, flexWrap: "wrap" }}>
      <div>
        <div style={{ fontSize: 10, color: "var(--text-3)", marginBottom: 8 }}>Hover / press me</div>
        <motion.div
          onHoverStart={() => {}}
          onTapStart={() => setPressing(true)}
          onTap={() => setPressing(false)}
          onTapCancel={() => setPressing(false)}
          animate={{ scale: pressing ? 0.96 : 1 }}
          transition={{ type: "spring", stiffness: 300, damping: 26 }}
          whileHover={{ boxShadow: "0 4px 16px rgba(77,147,252,0.35)" }}
          style={{
            width: 100, height: 40, borderRadius: 8,
            background: "var(--accent)", display: "flex", alignItems: "center", justifyContent: "center",
            fontSize: 13, fontWeight: 600, color: "#fff", cursor: "pointer",
            userSelect: "none",
          }}
        >
          Buy now
        </motion.div>
      </div>

      <div style={{ display: "flex", flexDirection: "column", gap: 4 }}>
        <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
          <motion.div
            animate={{ scale: pressing ? 0.96 : 1, background: pressing ? "var(--accent)" : "var(--bg-surface-2)" }}
            style={{ width: 8, height: 8, borderRadius: "50%", border: "1px solid var(--border)" }}
          />
          <span style={{ fontSize: 11, color: pressing ? "var(--text-1)" : "var(--text-3)" }}>
            {pressing ? "scale(0.96) — pressed" : "scale(1.0) — rest"}
          </span>
        </div>
        <div style={{ fontSize: 11, color: "var(--text-3)", fontFamily: "var(--font-mono)" }}>
          spring · stiffness 300 · damping 26
        </div>
      </div>
    </div>
  );
}

const principles = [
  { icon: "⚡", title: "Fast",     desc: "Animations never feel slow or heavy. Spring presets keep interactions under ~0.55s." },
  { icon: "🌿", title: "Natural",  desc: "Spring physics mirror real-world momentum — elements decelerate the way objects do." },
  { icon: "🔗", title: "Cohesive", desc: "All surfaces share the same three spring presets and one opacity curve." },
];

export default function MotionSection() {
  const isMobile = useIsMobile();
  return (
    <section id="motion" style={{ marginBottom: 80, scrollMarginTop: "calc(var(--header-h) + 32px)" }}>
      <SectionHeading id="motion" title="Motion" />

      <motion.p
        variants={fadeUp} initial="hidden" whileInView="visible" viewport={{ once: true }}
        style={{ fontSize: 16, color: "var(--text-2)", lineHeight: 1.65, maxWidth: 640, marginBottom: 48 }}
      >
        The Field DS motion system defines animation behaviour across all platforms. Every interaction uses one of three spring presets, a single opacity curve, or a scale press spec — ensuring a consistent feel across product surfaces.
      </motion.p>

      {/* ── Principles ── */}
      <motion.div
        variants={stagger} initial="hidden" whileInView="visible" viewport={{ once: true, margin: "-40px" }}
        style={{ display: "grid", gridTemplateColumns: isMobile ? "1fr" : "repeat(3, 1fr)", gap: 16, marginBottom: 48 }}
      >
        {principles.map((p) => (
          <motion.div
            key={p.title}
            variants={itemFadeUp}
            whileHover={{ y: -3, boxShadow: "0 8px 24px rgba(0,0,0,0.1)" }}
            transition={{ type: "spring", stiffness: 300, damping: 22 }}
            style={{ background: "var(--bg-surface)", border: "1px solid var(--border)", borderRadius: 8, padding: 20 }}
          >
            <div style={{ fontSize: 20, marginBottom: 10 }}>{p.icon}</div>
            <div style={{ fontSize: 13, fontWeight: 600, color: "var(--text-1)", marginBottom: 4 }}>{p.title}</div>
            <div style={{ fontSize: 12, color: "var(--text-3)", lineHeight: 1.55 }}>{p.desc}</div>
          </motion.div>
        ))}
      </motion.div>

      {/* ── Spring presets ── */}
      <div id="motion-spring" style={{ marginBottom: 48, scrollMarginTop: "calc(var(--header-h) + 32px)" }}>
        <motion.h3
          variants={fadeUp} initial="hidden" whileInView="visible" viewport={{ once: true }}
          style={{ fontSize: 18, fontWeight: 600, letterSpacing: "-0.3px", color: "var(--text-1)", marginBottom: 8 }}
        >
          Spring presets
        </motion.h3>
        <motion.p
          variants={fadeUp} initial="hidden" whileInView="visible" viewport={{ once: true }}
          style={{ fontSize: 14, color: "var(--text-2)", marginBottom: 24, maxWidth: 640 }}
        >
          Used for all move / position animations. All presets share stiffness 300 — only damping varies to tune the feel.
        </motion.p>

        <motion.div
          variants={stagger} initial="hidden" whileInView="visible" viewport={{ once: true, margin: "-40px" }}
          style={{ display: "grid", gridTemplateColumns: isMobile ? "1fr" : "repeat(3, 1fr)", gap: 16, marginBottom: 32 }}
        >
          {motionTokens.spring.map((s) => (
            <motion.div
              key={s.token}
              variants={itemFadeUp}
              whileHover={{ y: -3, boxShadow: "0 8px 32px rgba(0,0,0,0.12)" }}
              transition={{ type: "spring", stiffness: 300, damping: 22 }}
              style={{ background: "var(--bg-surface)", border: "1px solid var(--border)", borderRadius: 8, padding: 20 }}
            >
              <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", marginBottom: 14 }}>
                <span style={{ fontSize: 13, fontWeight: 600, color: "var(--text-1)" }}>{s.name}</span>
                <Tag color="var(--accent)">{s.token.replace("motion.spring.", "")}</Tag>
              </div>
              <SpringCurve damping={s.damping as 24 | 26 | 28} />
              <div style={{
                display: "grid", gridTemplateColumns: "1fr 1fr 1fr", gap: 8,
                marginTop: 14, paddingTop: 14, borderTop: "1px solid var(--border)",
              }}>
                {[["Stiffness", String(s.stiffness)], ["Damping", String(s.damping)], ["Duration", s.duration]].map(([l, v]) => (
                  <div key={l}>
                    <div style={{ fontSize: 10, color: "var(--text-3)", marginBottom: 2 }}>{l}</div>
                    <div style={{ fontSize: 12, fontFamily: "var(--font-mono)", fontWeight: 600, color: "var(--text-2)" }}>{v}</div>
                  </div>
                ))}
              </div>
              <div style={{ fontSize: 11, color: "var(--text-3)", marginTop: 10, fontStyle: "italic" }}>{s.feel}</div>
            </motion.div>
          ))}
        </motion.div>

        <motion.div
          variants={fadeUp} initial="hidden" whileInView="visible" viewport={{ once: true }}
        >
          <div className="ds-table-scroll">
          <DSTable
            headers={isMobile ? ["Token", "K", "D", "Dur"] : ["Token", "Preset", "Stiffness", "Damping", "Duration", "Feel"]}
            rows={motionTokens.spring.map((s) => {
              const base = [
                <span key="tok" style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--text-3)" }}>{s.token}</span>,
                <span key="k" style={{ fontFamily: "var(--font-mono)", fontSize: 12 }}>{s.stiffness}</span>,
                <span key="d" style={{ fontFamily: "var(--font-mono)", fontSize: 12 }}>{s.damping}</span>,
                <span key="dur" style={{ fontFamily: "var(--font-mono)", fontSize: 12, color: "var(--text-2)" }}>{s.duration}</span>,
              ];
              if (!isMobile) {
                base.splice(1, 0, <span key="name" style={{ fontWeight: 600, color: "var(--text-1)", fontSize: 13 }}>{s.name}</span>);
                base.push(<span key="feel" style={{ fontSize: 12, color: "var(--text-3)" }}>{s.feel}</span>);
              }
              return base;
            })}
          />
          </div>
        </motion.div>

        {/* Code snippets */}
        <motion.div
          variants={stagger} initial="hidden" whileInView="visible" viewport={{ once: true }}
          style={{ marginTop: 24 }}
        >
          <motion.div variants={itemFadeUp} style={{ fontSize: 11, fontWeight: 600, color: "var(--text-3)", textTransform: "uppercase", letterSpacing: "0.07em", marginBottom: 12 }}>
            React Native (Reanimated)
          </motion.div>
          <motion.div variants={stagger} style={{ display: "grid", gridTemplateColumns: isMobile ? "1fr" : "repeat(3, 1fr)", gap: 12 }}>
            {motionTokens.spring.map((s) => (
              <motion.div key={s.token} variants={itemFadeUp}>
                <div style={{ fontSize: 11, color: "var(--text-3)", marginBottom: 6 }}>{s.name}</div>
                <Code>{s.rn}</Code>
              </motion.div>
            ))}
          </motion.div>
        </motion.div>

        <motion.div
          variants={stagger} initial="hidden" whileInView="visible" viewport={{ once: true }}
          style={{ marginTop: 20 }}
        >
          <motion.div variants={itemFadeUp} style={{ fontSize: 11, fontWeight: 600, color: "var(--text-3)", textTransform: "uppercase", letterSpacing: "0.07em", marginBottom: 12 }}>
            Android (Compose)
          </motion.div>
          <motion.div variants={stagger} style={{ display: "grid", gridTemplateColumns: isMobile ? "1fr" : "repeat(3, 1fr)", gap: 12 }}>
            {motionTokens.spring.map((s) => (
              <motion.div key={s.token} variants={itemFadeUp}>
                <div style={{ fontSize: 11, color: "var(--text-3)", marginBottom: 6 }}>{s.name}</div>
                <Code>{s.android}</Code>
              </motion.div>
            ))}
          </motion.div>
        </motion.div>
      </div>

      {/* ── Opacity ── */}
      <motion.div
        id="motion-opacity"
        variants={fadeUp} initial="hidden" whileInView="visible" viewport={{ once: true, margin: "-40px" }}
        style={{ marginBottom: 48, scrollMarginTop: "calc(var(--header-h) + 32px)" }}
      >
        <h3 style={{ fontSize: 18, fontWeight: 600, letterSpacing: "-0.3px", color: "var(--text-1)", marginBottom: 8 }}>Opacity</h3>
        <p style={{ fontSize: 14, color: "var(--text-2)", marginBottom: 24, maxWidth: 640 }}>
          One opacity preset covers all fade-in / fade-out animations. Used for revealing search results, overlays, and transparency transitions.
        </p>

        <div style={{ display: "grid", gridTemplateColumns: isMobile ? "1fr" : "240px 1fr", gap: 16, marginBottom: 24 }}>
          <motion.div
            whileHover={{ y: -2 }}
            transition={{ type: "spring", stiffness: 300, damping: 22 }}
            style={{ background: "var(--bg-surface)", border: "1px solid var(--border)", borderRadius: 8, padding: 20 }}
          >
            <div style={{ fontSize: 11, fontWeight: 600, color: "var(--text-3)", textTransform: "uppercase", letterSpacing: "0.07em", marginBottom: 14 }}>
              {motionTokens.opacity.token}
            </div>
            <EasingCurve type="ease-out" />
            <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 8, marginTop: 14, paddingTop: 14, borderTop: "1px solid var(--border)" }}>
              {[["Duration", `${motionTokens.opacity.duration}ms`], ["Easing", motionTokens.opacity.label]].map(([l, v]) => (
                <div key={l}>
                  <div style={{ fontSize: 10, color: "var(--text-3)", marginBottom: 2 }}>{l}</div>
                  <div style={{ fontSize: 11, fontFamily: "var(--font-mono)", fontWeight: 600, color: "var(--text-2)" }}>{v}</div>
                </div>
              ))}
            </div>
          </motion.div>

          <div style={{ display: "flex", flexDirection: "column", gap: 12 }}>
            <div>
              <div style={{ fontSize: 11, fontWeight: 600, color: "var(--text-3)", textTransform: "uppercase", letterSpacing: "0.07em", marginBottom: 6 }}>Cubic bezier</div>
              <Code>{motionTokens.opacity.easing}</Code>
            </div>
            <div>
              <div style={{ fontSize: 11, fontWeight: 600, color: "var(--text-3)", textTransform: "uppercase", letterSpacing: "0.07em", marginBottom: 6 }}>React Native (Reanimated)</div>
              <Code>{motionTokens.opacity.rnReanimated}</Code>
            </div>
            <div>
              <div style={{ fontSize: 11, fontWeight: 600, color: "var(--text-3)", textTransform: "uppercase", letterSpacing: "0.07em", marginBottom: 6 }}>React Native (Animated)</div>
              <Code>{motionTokens.opacity.rnAnimated}</Code>
            </div>
          </div>
        </div>
      </motion.div>

      {/* ── Scale / Press feedback ── */}
      <motion.div
        id="motion-scale"
        variants={fadeUp} initial="hidden" whileInView="visible" viewport={{ once: true, margin: "-40px" }}
        style={{ scrollMarginTop: "calc(var(--header-h) + 32px)" }}
      >
        <h3 style={{ fontSize: 18, fontWeight: 600, letterSpacing: "-0.3px", color: "var(--text-1)", marginBottom: 8 }}>Scale (press feedback)</h3>
        <p style={{ fontSize: 14, color: "var(--text-2)", marginBottom: 24, maxWidth: 640 }}>
          Applied on touch-down for all tappable surfaces — buttons, cards, tiles. Provides immediate tactile feedback without distracting from the interaction.
        </p>

        <div style={{ display: "grid", gridTemplateColumns: isMobile ? "1fr" : "240px 1fr", gap: 16 }}>
          <motion.div
            whileHover={{ y: -2 }}
            transition={{ type: "spring", stiffness: 300, damping: 22 }}
            style={{ background: "var(--bg-surface)", border: "1px solid var(--border)", borderRadius: 8, padding: 20 }}
          >
            <div style={{ fontSize: 11, fontWeight: 600, color: "var(--text-3)", textTransform: "uppercase", letterSpacing: "0.07em", marginBottom: 14 }}>
              {motionTokens.scale.token}
            </div>
            <EasingCurve type="ease-in-out" />
            <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 8, marginTop: 14, paddingTop: 14, borderTop: "1px solid var(--border)" }}>
              {[
                ["Scale to", `${motionTokens.scale.scaleTo * 100}%`],
                ["Duration", `${motionTokens.scale.duration}ms`],
                ["Easing", motionTokens.scale.easingLabel],
                ["Delay", `${motionTokens.scale.startDelay}ms`],
              ].map(([l, v]) => (
                <div key={l}>
                  <div style={{ fontSize: 10, color: "var(--text-3)", marginBottom: 2 }}>{l}</div>
                  <div style={{ fontSize: 11, fontFamily: "var(--font-mono)", fontWeight: 600, color: "var(--text-2)" }}>{v}</div>
                </div>
              ))}
            </div>
          </motion.div>

          <div style={{ display: "flex", flexDirection: "column", gap: 12 }}>
            {/* Interactive press demo */}
            <motion.div
              whileHover={{ y: -1 }}
              style={{
                background: "var(--bg-surface)", border: "1px solid var(--border)", borderRadius: 8, padding: 20,
              }}
            >
              <div style={{ fontSize: 11, fontWeight: 600, color: "var(--text-3)", textTransform: "uppercase", letterSpacing: "0.07em", marginBottom: 16 }}>
                Live demo
              </div>
              <PressDemo />
            </motion.div>

            <div>
              <div style={{ fontSize: 11, fontWeight: 600, color: "var(--text-3)", textTransform: "uppercase", letterSpacing: "0.07em", marginBottom: 6 }}>
                React Native (Reanimated)
              </div>
              <Code>{`// Touch down\nwithTiming(0.96, { duration: 200, easing: Easing.bezier(0.65, 0, 0.35, 1) })\n\n// Touch up\nwithTiming(1, { duration: 200, easing: Easing.bezier(0.65, 0, 0.35, 1) })`}</Code>
            </div>
          </div>
        </div>
      </motion.div>
    </section>
  );
}
