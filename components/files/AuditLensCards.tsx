"use client";

import { motion } from "framer-motion";
import { fadeUp, stagger, itemFadeUp } from "@/lib/motion-variants";
import type { AuditScreen, FixCandidate } from "@/lib/audit/types";

/**
 * AuditLensCards renders the three-lens triple per screen:
 *   1. Token coverage — bound/total per property.
 *   2. Component usage — DS / ambiguous / custom split.
 *   3. Drift suggestions — top fix candidates with priority pills.
 *
 * Pure presentational component; consumes the AuditScreen shape directly.
 * Theme-aware via CSS variables, motion via lib/motion-variants only.
 */
export default function AuditLensCards({ screen }: { screen: AuditScreen }) {
  return (
    <motion.div
      variants={stagger}
      initial="hidden"
      whileInView="visible"
      viewport={{ once: true, margin: "-30px" }}
      style={{
        display: "grid",
        gridTemplateColumns: "repeat(auto-fit, minmax(280px, 1fr))",
        gap: 14,
      }}
    >
      <CoverageCard screen={screen} />
      <ComponentsCard screen={screen} />
      <DriftCard screen={screen} />
    </motion.div>
  );
}

/* ── Lens 1: Token coverage ─────────────────────────────────────────── */

function CoverageCard({ screen }: { screen: AuditScreen }) {
  const c = screen.coverage;
  const rows: Array<[string, { bound: number; total: number }]> = [
    ["fills", c.fills],
    ["text", c.text],
    ["spacing", c.spacing],
    ["radius", c.radius],
  ];
  return (
    <Card title="Token coverage" hint="Properties bound to a DS token vs. total observed.">
      <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
        {rows.map(([label, r]) => {
          const pct = r.total === 0 ? null : (r.bound / r.total) * 100;
          return (
            <CoverageRow key={label} label={label} bound={r.bound} total={r.total} pct={pct} />
          );
        })}
      </div>
    </Card>
  );
}

function CoverageRow({
  label,
  bound,
  total,
  pct,
}: {
  label: string;
  bound: number;
  total: number;
  pct: number | null;
}) {
  const accentColor =
    pct === null ? "var(--text-3)"
      : pct >= 90 ? "var(--accent)"
      : pct >= 70 ? "color-mix(in srgb, var(--accent) 60%, var(--warn, #f6a738))"
      : "var(--danger)";
  return (
    <motion.div
      variants={itemFadeUp}
      style={{
        display: "grid",
        gridTemplateColumns: "70px 1fr 90px",
        alignItems: "center",
        gap: 10,
      }}
    >
      <span style={{ fontSize: 11, color: "var(--text-3)", fontFamily: "var(--font-mono)" }}>
        {label}
      </span>
      <div style={{ height: 6, borderRadius: 3, background: "var(--bg-surface-2)", overflow: "hidden" }}>
        {pct !== null && (
          <motion.div
            initial={{ width: 0 }}
            whileInView={{ width: `${Math.max(2, pct)}%` }}
            viewport={{ once: true }}
            transition={{ duration: 0.5, ease: [0.33, 1, 0.68, 1] }}
            style={{ height: "100%", background: accentColor }}
          />
        )}
      </div>
      <span style={{ fontSize: 11, fontFamily: "var(--font-mono)", color: "var(--text-2)", textAlign: "right" }}>
        {pct === null ? "—" : `${bound}/${total} · ${Math.round(pct)}%`}
      </span>
    </motion.div>
  );
}

/* ── Lens 2: Component usage ────────────────────────────────────────── */

function ComponentsCard({ screen }: { screen: AuditScreen }) {
  const s = screen.component_summary;
  const total = s.from_ds + s.ambiguous + s.custom;
  return (
    <Card title="Component usage" hint="Instances matched to the DS, ambiguous, or custom.">
      <div style={{ display: "flex", flexDirection: "column", gap: 12, marginTop: 4 }}>
        <CompStat
          label="From DS"
          count={s.from_ds}
          total={total}
          color="var(--accent)"
          tone="positive"
        />
        <CompStat
          label="Ambiguous"
          count={s.ambiguous}
          total={total}
          color="color-mix(in srgb, var(--accent) 50%, transparent)"
          tone="neutral"
        />
        <CompStat
          label="Custom"
          count={s.custom}
          total={total}
          color="var(--danger)"
          tone="warning"
        />
      </div>
    </Card>
  );
}

function CompStat({
  label,
  count,
  total,
  color,
  tone,
}: {
  label: string;
  count: number;
  total: number;
  color: string;
  tone: "positive" | "neutral" | "warning";
}) {
  const pct = total === 0 ? 0 : (count / total) * 100;
  return (
    <motion.div variants={itemFadeUp} style={{ display: "flex", flexDirection: "column", gap: 4 }}>
      <div style={{ display: "flex", justifyContent: "space-between", alignItems: "baseline" }}>
        <span style={{ fontSize: 11, color: "var(--text-3)" }}>{label}</span>
        <span style={{ fontSize: 12, fontFamily: "var(--font-mono)", color: tone === "warning" && count > 0 ? "var(--danger)" : "var(--text-2)" }}>
          {count}
        </span>
      </div>
      <div style={{ height: 4, borderRadius: 2, background: "var(--bg-surface-2)", overflow: "hidden" }}>
        <motion.div
          initial={{ width: 0 }}
          whileInView={{ width: `${pct}%` }}
          viewport={{ once: true }}
          transition={{ duration: 0.45, ease: [0.33, 1, 0.68, 1] }}
          style={{ height: "100%", background: color }}
        />
      </div>
    </motion.div>
  );
}

/* ── Lens 3: Drift suggestions ──────────────────────────────────────── */

function DriftCard({ screen }: { screen: AuditScreen }) {
  const fixes = screen.fixes ?? [];
  const top = fixes.slice(0, 8);
  return (
    <Card
      title="Drift & suggestions"
      hint={fixes.length > 0 ? `${fixes.length} fix${fixes.length === 1 ? "" : "es"} ranked by priority × usage.` : "No drift detected — every property is on token."}
    >
      {top.length === 0 ? (
        <div style={{ fontSize: 12, color: "var(--text-3)", padding: "12px 0", textAlign: "center" }}>
          ✓ Clean
        </div>
      ) : (
        <motion.ol
          variants={stagger}
          style={{ listStyle: "none", padding: 0, margin: 0, display: "flex", flexDirection: "column", gap: 8 }}
        >
          {top.map((f) => (
            <FixRow key={`${f.node_id}-${f.property}-${f.observed}`} fix={f} />
          ))}
        </motion.ol>
      )}
      {fixes.length > top.length && (
        <p style={{ fontSize: 10, color: "var(--text-3)", margin: "8px 0 0", fontFamily: "var(--font-mono)" }}>
          + {fixes.length - top.length} more — open in plugin to see all
        </p>
      )}
    </Card>
  );
}

function FixRow({ fix }: { fix: FixCandidate }) {
  return (
    <motion.li variants={itemFadeUp}>
      <div style={{ display: "flex", alignItems: "flex-start", gap: 8 }}>
        <PriorityPill priority={fix.priority} />
        <div style={{ minWidth: 0, flex: 1 }}>
          <div
            style={{
              fontSize: 12,
              color: "var(--text-1)",
              overflow: "hidden",
              textOverflow: "ellipsis",
              whiteSpace: "nowrap",
            }}
          >
            <span style={{ fontFamily: "var(--font-mono)", color: "var(--text-3)" }}>{fix.property}</span>{" "}
            <span style={{ fontFamily: "var(--font-mono)" }}>{fix.observed}</span>
            {fix.token_path && (
              <>
                <span style={{ color: "var(--text-3)" }}> → </span>
                <span style={{ color: "var(--accent)" }}>{fix.token_path}</span>
              </>
            )}
          </div>
          <div style={{ fontSize: 10, color: "var(--text-3)", fontFamily: "var(--font-mono)", marginTop: 2 }}>
            {fix.reason}
            {fix.distance > 0 && ` · d=${fix.distance.toFixed(3)}`}
            {fix.usage_count > 0 && ` · ×${fix.usage_count}`}
          </div>
        </div>
      </div>
    </motion.li>
  );
}

function PriorityPill({ priority }: { priority: "P1" | "P2" | "P3" }) {
  const styles =
    priority === "P1"
      ? { bg: "var(--danger)", fg: "#fff" }
      : priority === "P2"
      ? { bg: "var(--warn, #f6a738)", fg: "#1a1a1a" }
      : { bg: "var(--bg-surface-2)", fg: "var(--text-3)" };
  return (
    <span
      style={{
        flexShrink: 0,
        background: styles.bg,
        color: styles.fg,
        fontSize: 9,
        fontWeight: 700,
        padding: "1px 6px",
        borderRadius: 999,
        fontFamily: "var(--font-mono)",
        letterSpacing: "0.04em",
      }}
    >
      {priority}
    </span>
  );
}

/* ── Shared card chrome ─────────────────────────────────────────────── */

function Card({
  title,
  hint,
  children,
}: {
  title: string;
  hint: string;
  children: React.ReactNode;
}) {
  return (
    <motion.div
      variants={fadeUp}
      style={{
        background: "var(--bg-surface)",
        border: "1px solid var(--border)",
        borderRadius: 10,
        padding: "16px 18px",
        display: "flex",
        flexDirection: "column",
        gap: 6,
      }}
    >
      <div style={{ display: "flex", alignItems: "baseline", justifyContent: "space-between", gap: 12 }}>
        <h4 style={{ fontSize: 13, fontWeight: 600, letterSpacing: "-0.1px", color: "var(--text-1)", margin: 0 }}>
          {title}
        </h4>
      </div>
      <p style={{ fontSize: 11, color: "var(--text-3)", margin: 0 }}>{hint}</p>
      <div style={{ marginTop: 8 }}>{children}</div>
    </motion.div>
  );
}
