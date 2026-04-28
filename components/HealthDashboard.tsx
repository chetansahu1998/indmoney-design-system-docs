"use client";

import Link from "next/link";
import { motion } from "framer-motion";
import { fadeUp, stagger, itemFadeUp } from "@/lib/motion-variants";
import {
  systemStats,
  bindingCoverage,
  propTypeBreakdown,
  componentsWithRichData,
} from "@/lib/icons/manifest";
import {
  buildSemanticPairs,
  buildBasePalette,
  getExtractionMeta,
} from "@/lib/tokens/loader";
import { hasAuditData, auditedFiles, provenanceLine } from "@/lib/audit";

/**
 * HealthDashboard — design-system vital signs.
 *
 * Six sections, scroll-down:
 *   1. Overview      — six big stat cards: tokens, components, variants,
 *                      audited files, latest extraction, binding %
 *   2. Tokens        — semantic vs base counts, sources
 *   3. Components    — total, with prop defs, with description, top
 *                      categories, biggest variant matrices
 *   4. Drift         — top off-grid spacing values from audit sidecar
 *                      (when present)
 *   5. Audits        — list of audited files with coverage + drift
 *   6. Extraction    — provenance log: when each pipeline last ran
 *
 * Empty states everywhere data is missing — no silent zeros.
 */
export default function HealthDashboard() {
  const stats = systemStats();
  const binding = bindingCoverage();
  const props = propTypeBreakdown();
  const semanticPairs = buildSemanticPairs();
  const basePalette = buildBasePalette();
  const meta = getExtractionMeta() as {
    extracted_at?: string;
    glyph_colors?: number;
    base_colors?: number;
    sources?: { kind: string; file_name?: string }[];
  };
  const audited = hasAuditData() ? auditedFiles() : [];
  const richComponents = componentsWithRichData();

  return (
    <motion.div variants={stagger} initial="hidden" animate="visible" style={{ display: "flex", flexDirection: "column", gap: 56 }}>
      {/* ─── Hero ─── */}
      <motion.section variants={fadeUp} id="overview" style={{ scrollMarginTop: "calc(var(--header-h) + 32px)" }}>
        <div style={{ marginBottom: 8, fontSize: 11, fontWeight: 600, color: "var(--text-3)", textTransform: "uppercase", letterSpacing: "0.08em" }}>
          Design system
        </div>
        <h1 style={{ fontSize: 48, fontWeight: 700, letterSpacing: "-1.5px", color: "var(--text-1)", marginBottom: 12, lineHeight: 1.05 }}>
          Health
        </h1>
        <p style={{ fontSize: 15, color: "var(--text-2)", lineHeight: 1.65, maxWidth: 640, marginBottom: 24 }}>
          Vital signs of INDmoney's Glyph. Everything below is real data extracted from the
          live system — no synthetic numbers, no placeholders. If a section is empty,
          that data hasn't been captured yet.
        </p>

        <div
          style={{
            display: "grid",
            gridTemplateColumns: "repeat(auto-fill, minmax(180px, 1fr))",
            gap: 12,
          }}
        >
          <StatCard label="Components" value={stats.components} hint={`${stats.components_with_props} with props`} tone="accent" />
          <StatCard label="Variants" value={stats.variants} hint="across all sets" tone="accent" />
          <StatCard label="Tokens" value={(meta.glyph_colors ?? 0) + (meta.base_colors ?? 0)} hint={`${semanticPairs.length} semantic · ${basePalette.length} base`} tone="accent" />
          <StatCard label="Icons" value={stats.icons} hint="system glyphs" />
          <StatCard label="Illustrations" value={stats.illustrations} hint="2D + 3D" />
          <StatCard label="Logos" value={stats.logos} hint="brand + partner" />
          <StatCard
            label="Audits"
            value={audited.length}
            hint={audited.length > 0 ? "files audited" : "none yet"}
            tone={audited.length > 0 ? "success" : "muted"}
          />
          <StatCard
            label="Bound fills"
            value={binding.fillsTotal > 0 ? `${Math.round((binding.fillsBound / binding.fillsTotal) * 100)}%` : "—"}
            hint={`${binding.fillsBound}/${binding.fillsTotal}`}
            tone="success"
          />
        </div>
      </motion.section>

      {/* ─── Tokens ─── */}
      <motion.section variants={fadeUp} id="tokens" style={{ scrollMarginTop: "calc(var(--header-h) + 32px)" }}>
        <SectionHeader id="tokens" eyebrow="01" title="Tokens" />
        <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 12 }}>
          <Card title="Color">
            <KV label="Semantic pairs" value={String(semanticPairs.length)} />
            <KV label="Base palette" value={String(basePalette.length)} />
            <KV
              label="Source"
              value={(meta.sources ?? []).map((s) => `${s.kind}:${s.file_name ?? "?"}`).join(", ") || "—"}
              mono
            />
            <KV label="Last sync" value={meta.extracted_at ? new Date(meta.extracted_at).toLocaleString() : "—"} mono />
            <Action href="/#color">Open Foundations →</Action>
          </Card>
          <Card title="Bindings">
            <KV
              label="Fills bound"
              value={`${binding.fillsBound} / ${binding.fillsTotal}`}
              bar={binding.fillsTotal > 0 ? binding.fillsBound / binding.fillsTotal : 0}
            />
            <KV
              label="Effects bound"
              value={`${binding.effectsBound} / ${binding.effectsTotal}`}
              bar={binding.effectsTotal > 0 ? binding.effectsBound / binding.effectsTotal : 0}
            />
            <div style={{ fontSize: 11, color: "var(--text-3)", marginTop: 8, lineHeight: 1.55 }}>
              Bound = fill or effect references a Figma Variable. Raw fills are
              candidates for token migration. The audit can apply these in
              one click via the plugin.
            </div>
          </Card>
        </div>
      </motion.section>

      {/* ─── Components ─── */}
      <motion.section variants={fadeUp} id="components" style={{ scrollMarginTop: "calc(var(--header-h) + 32px)" }}>
        <SectionHeader id="components" eyebrow="02" title="Components" count={stats.components} />
        <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 12, marginBottom: 12 }}>
          <Card title="Property types">
            <KV label="VARIANT" value={String(props.variant)} />
            <KV label="BOOLEAN" value={String(props.boolean)} />
            <KV label="TEXT" value={String(props.text)} />
            <KV label="INSTANCE_SWAP" value={String(props.instance_swap)} />
          </Card>
          <Card title="Documentation">
            <KV
              label="With description"
              value={`${stats.components_with_description} / ${stats.components}`}
              bar={stats.components > 0 ? stats.components_with_description / stats.components : 0}
            />
            <KV
              label="With prop defs"
              value={`${stats.components_with_props} / ${stats.components}`}
              bar={stats.components > 0 ? stats.components_with_props / stats.components : 0}
            />
            <Action href="/components">Open gallery →</Action>
          </Card>
        </div>

        {/* Top components by variant count */}
        <div style={{ background: "var(--bg-surface)", border: "1px solid var(--border)", borderRadius: 10, overflow: "hidden" }}>
          <div style={{ padding: "12px 14px", background: "var(--bg-surface-2)", borderBottom: "1px solid var(--border)" }}>
            <span style={{ fontSize: 11, fontWeight: 600, color: "var(--text-2)", textTransform: "uppercase", letterSpacing: "0.06em" }}>
              Largest variant matrices
            </span>
          </div>
          {[...richComponents]
            .sort((a, b) => (b.variants?.length ?? 0) - (a.variants?.length ?? 0))
            .slice(0, 8)
            .map((c) => (
              <Link
                key={c.slug}
                href={`/components/${c.slug}`}
                style={{
                  display: "grid",
                  gridTemplateColumns: "minmax(120px, 1.5fr) 1fr 80px",
                  gap: 12,
                  padding: "10px 14px",
                  borderBottom: "1px solid var(--border)",
                  textDecoration: "none",
                  alignItems: "center",
                }}
              >
                <span style={{ color: "var(--text-1)", fontSize: 13, fontWeight: 500 }}>{c.name}</span>
                <span style={{ color: "var(--text-3)", fontSize: 11, fontFamily: "var(--font-mono)" }}>
                  {(c.variant_axes ?? []).map((a) => a.name).join(" × ") || "(no axes)"}
                </span>
                <span style={{ color: "var(--accent)", fontSize: 12, fontFamily: "var(--font-mono)", fontWeight: 600, textAlign: "right" }}>
                  {c.variants?.length ?? 0}
                </span>
              </Link>
            ))}
        </div>
      </motion.section>

      {/* ─── Drift ─── */}
      <motion.section variants={fadeUp} id="drift" style={{ scrollMarginTop: "calc(var(--header-h) + 32px)" }}>
        <SectionHeader id="drift" eyebrow="03" title="Drift" />
        <DriftBlock />
      </motion.section>

      {/* ─── Audits ─── */}
      <motion.section variants={fadeUp} id="audits" style={{ scrollMarginTop: "calc(var(--header-h) + 32px)" }}>
        <SectionHeader id="audits" eyebrow="04" title="Audits" count={audited.length} />
        {audited.length === 0 ? (
          <div
            style={{
              padding: 22,
              border: "1px dashed var(--border-default, var(--border))",
              borderRadius: 10,
              textAlign: "center",
              color: "var(--text-3)",
              background:
                "repeating-linear-gradient(135deg, transparent 0 8px, color-mix(in srgb, var(--text-3) 4%, transparent) 8px 10px)",
              fontSize: 13,
            }}
          >
            <div style={{ color: "var(--text-1)", fontWeight: 600, marginBottom: 4 }}>
              No audits yet
            </div>
            <div style={{ lineHeight: 1.55 }}>
              Designers run the plugin's <em>Audit file</em> command on their working
              files — the resulting JSON lands in <code style={{ fontFamily: "var(--font-mono)" }}>lib/audit/</code>.
              When even one audit runs, this section turns into a live coverage table.
            </div>
          </div>
        ) : (
          <div style={{ background: "var(--bg-surface)", border: "1px solid var(--border)", borderRadius: 10, overflow: "hidden" }}>
            {audited.map((f) => (
              <Link
                key={f.file_slug}
                href={`/files/${f.file_slug}`}
                style={{
                  display: "grid",
                  gridTemplateColumns: "minmax(140px, 1.5fr) 80px 80px",
                  gap: 12,
                  padding: "10px 14px",
                  borderBottom: "1px solid var(--border)",
                  textDecoration: "none",
                  alignItems: "center",
                }}
              >
                <span style={{ color: "var(--text-1)", fontSize: 13, fontWeight: 500, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                  {f.file_name}
                </span>
                <span style={{ fontFamily: "var(--font-mono)", fontSize: 12, color: "var(--accent)", fontWeight: 600, textAlign: "right" }}>
                  {Math.round(f.overall_coverage * 1000) / 10}%
                </span>
                <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--text-3)", textAlign: "right" }}>
                  {f.screen_count} screens
                </span>
              </Link>
            ))}
            <div style={{ padding: "10px 14px", fontSize: 11, fontFamily: "var(--font-mono)", color: "var(--text-3)" }}>
              {provenanceLine()}
            </div>
          </div>
        )}
      </motion.section>

      {/* ─── Extraction provenance ─── */}
      <motion.section variants={fadeUp} id="extraction" style={{ scrollMarginTop: "calc(var(--header-h) + 32px)" }}>
        <SectionHeader id="extraction" eyebrow="05" title="Extraction" />
        <div style={{ background: "var(--bg-surface)", border: "1px solid var(--border)", borderRadius: 10, overflow: "hidden" }}>
          <PipelineRow name="Color extractor" cmd="go run ./cmd/extractor" lastRun={meta.extracted_at} />
          <PipelineRow name="Variants extractor" cmd="go run ./cmd/variants" lastRun={meta.extracted_at} />
          <PipelineRow name="Spacing/radius scan" cmd="go run ./cmd/variables" lastRun={meta.extracted_at} />
          <PipelineRow name="Effects extractor" cmd="go run ./cmd/effects" lastRun={null} />
        </div>
      </motion.section>
    </motion.div>
  );
}

/* ── Components ────────────────────────────────────────────────────────── */

function SectionHeader({ id, title, eyebrow, count }: { id: string; title: string; eyebrow?: string; count?: number }) {
  return (
    <motion.div variants={itemFadeUp} style={{ marginBottom: 18 }}>
      {eyebrow && (
        <div
          style={{
            fontSize: 10,
            fontWeight: 600,
            color: "var(--text-3)",
            textTransform: "uppercase",
            letterSpacing: "0.08em",
            marginBottom: 6,
          }}
        >
          {eyebrow}
        </div>
      )}
      <div style={{ display: "flex", alignItems: "baseline", gap: 12 }}>
        <h2 id={id} style={{ fontSize: 24, fontWeight: 700, letterSpacing: "-0.5px", color: "var(--text-1)" }}>
          {title}
        </h2>
        {count != null && (
          <span style={{ fontSize: 13, color: "var(--text-3)", fontFamily: "var(--font-mono)" }}>
            {count}
          </span>
        )}
      </div>
    </motion.div>
  );
}

function StatCard({
  label,
  value,
  hint,
  tone,
}: {
  label: string;
  value: string | number;
  hint?: string;
  tone?: "accent" | "success" | "warning" | "danger" | "muted";
}) {
  const valueColor =
    tone === "accent" ? "var(--accent)" :
    tone === "success" ? "var(--success)" :
    tone === "warning" ? "var(--warning)" :
    tone === "danger" ? "var(--danger)" :
    tone === "muted" ? "var(--text-3)" :
    "var(--text-1)";
  return (
    <motion.div
      variants={itemFadeUp}
      whileHover={{ y: -2 }}
      transition={{ type: "spring", stiffness: 320, damping: 26 }}
      style={{
        padding: 16,
        background: "var(--bg-surface)",
        border: "1px solid var(--border)",
        borderRadius: 10,
        display: "flex",
        flexDirection: "column",
        gap: 4,
      }}
    >
      <span style={{ fontSize: 28, fontWeight: 700, letterSpacing: "-0.5px", color: valueColor, fontVariantNumeric: "tabular-nums", lineHeight: 1.1 }}>
        {value}
      </span>
      <span style={{ fontSize: 10, fontWeight: 600, color: "var(--text-3)", textTransform: "uppercase", letterSpacing: "0.06em" }}>
        {label}
      </span>
      {hint && (
        <span style={{ fontSize: 11, color: "var(--text-3)", fontFamily: "var(--font-mono)", marginTop: 2 }}>
          {hint}
        </span>
      )}
    </motion.div>
  );
}

function Card({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div
      style={{
        background: "var(--bg-surface)",
        border: "1px solid var(--border)",
        borderRadius: 10,
        overflow: "hidden",
        display: "flex",
        flexDirection: "column",
      }}
    >
      <div style={{ padding: "10px 14px", background: "var(--bg-surface-2)", borderBottom: "1px solid var(--border)" }}>
        <span style={{ fontSize: 11, fontWeight: 600, color: "var(--text-2)", textTransform: "uppercase", letterSpacing: "0.06em" }}>
          {title}
        </span>
      </div>
      <div style={{ padding: "12px 14px", display: "flex", flexDirection: "column", gap: 10 }}>{children}</div>
    </div>
  );
}

function KV({ label, value, mono, bar }: { label: string; value: string; mono?: boolean; bar?: number }) {
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 4 }}>
      <div style={{ display: "flex", justifyContent: "space-between", alignItems: "baseline", gap: 12 }}>
        <span style={{ fontSize: 12, color: "var(--text-3)" }}>{label}</span>
        <span
          style={{
            fontSize: 12,
            color: "var(--text-1)",
            fontFamily: mono ? "var(--font-mono)" : undefined,
            fontVariantNumeric: "tabular-nums",
            textAlign: "right",
            wordBreak: "break-all",
            maxWidth: "70%",
          }}
        >
          {value}
        </span>
      </div>
      {bar != null && (
        <div style={{ height: 4, background: "var(--bg-surface-2)", borderRadius: 2, overflow: "hidden" }}>
          <div
            style={{
              height: "100%",
              width: `${Math.max(0, Math.min(100, bar * 100))}%`,
              background: "var(--accent)",
              borderRadius: 2,
              transition: "width 240ms cubic-bezier(0.16, 1, 0.3, 1)",
            }}
          />
        </div>
      )}
    </div>
  );
}

function Action({ href, children }: { href: string; children: React.ReactNode }) {
  return (
    <Link
      href={href}
      style={{
        marginTop: 4,
        display: "inline-flex",
        alignItems: "center",
        gap: 6,
        fontSize: 12,
        fontWeight: 600,
        color: "var(--accent)",
        textDecoration: "none",
        fontFamily: "var(--font-mono)",
      }}
    >
      {children}
    </Link>
  );
}

function PipelineRow({ name, cmd, lastRun }: { name: string; cmd: string; lastRun: string | null | undefined }) {
  return (
    <div
      style={{
        padding: "10px 14px",
        borderBottom: "1px solid var(--border)",
        display: "grid",
        gridTemplateColumns: "1fr auto",
        alignItems: "baseline",
        gap: 12,
      }}
    >
      <div>
        <div style={{ fontSize: 13, color: "var(--text-1)", fontWeight: 500 }}>{name}</div>
        <code style={{ fontSize: 11, fontFamily: "var(--font-mono)", color: "var(--text-3)" }}>{cmd}</code>
      </div>
      <span style={{ fontSize: 11, fontFamily: "var(--font-mono)", color: lastRun ? "var(--text-2)" : "var(--text-3)", textAlign: "right" }}>
        {lastRun ? new Date(lastRun).toLocaleString() : "never"}
      </span>
    </div>
  );
}

/* ── Drift block — reads spacing-observed sidecar if present ─────────── */

function DriftBlock() {
  // The sidecar lives at lib/audit/spacing-observed.json. We can't import it
  // synchronously since it may not exist on first build; treat absence as
  // empty state. (Static import of an optional file is awkward in Next; we
  // rely on the build having a valid path or render the empty state.)
  let drift: Array<{ value: number; count: number; snap_to: number; on_grid: boolean }> = [];
  try {
    // eslint-disable-next-line @typescript-eslint/no-require-imports
    const sidecar = require("../lib/audit/spacing-observed.json");
    const all = [...(sidecar.spacing ?? []), ...(sidecar.padding ?? [])];
    drift = all
      .filter((d: { on_grid: boolean }) => d.on_grid === false)
      .sort((a: { count: number }, b: { count: number }) => b.count - a.count)
      .slice(0, 10);
  } catch {
    // Sidecar missing — empty state below.
  }

  if (drift.length === 0) {
    return (
      <div
        style={{
          padding: 22,
          border: "1px dashed var(--border)",
          borderRadius: 10,
          textAlign: "center",
          color: "var(--text-3)",
          fontSize: 13,
          background:
            "repeating-linear-gradient(135deg, transparent 0 8px, color-mix(in srgb, var(--text-3) 4%, transparent) 8px 10px)",
        }}
      >
        <div style={{ color: "var(--text-1)", fontWeight: 600, marginBottom: 4 }}>
          No drift recorded
        </div>
        <div style={{ lineHeight: 1.55 }}>
          Run <code style={{ fontFamily: "var(--font-mono)" }}>go run ./cmd/variables --source manifest</code> to
          aggregate off-grid spacing across product files. Until then, designers' raw values aren't surfaced here.
        </div>
      </div>
    );
  }

  return (
    <div style={{ background: "var(--bg-surface)", border: "1px solid var(--border)", borderRadius: 10, overflow: "hidden" }}>
      <div style={{ padding: "10px 14px", background: "var(--bg-surface-2)", borderBottom: "1px solid var(--border)" }}>
        <span style={{ fontSize: 11, fontWeight: 600, color: "var(--text-2)", textTransform: "uppercase", letterSpacing: "0.06em" }}>
          Top off-grid values
        </span>
      </div>
      {drift.map((d) => (
        <div
          key={d.value}
          style={{
            display: "grid",
            gridTemplateColumns: "60px 1fr 60px 80px",
            gap: 12,
            padding: "10px 14px",
            borderBottom: "1px solid var(--border)",
            alignItems: "center",
            fontSize: 12,
          }}
        >
          <span style={{ fontFamily: "var(--font-mono)", color: "var(--danger)", fontWeight: 600 }}>
            {d.value}px
          </span>
          <span style={{ color: "var(--text-2)" }}>
            used <strong style={{ color: "var(--text-1)", fontFamily: "var(--font-mono)" }}>{d.count}×</strong> across audited files
          </span>
          <span style={{ color: "var(--text-3)", fontFamily: "var(--font-mono)", fontSize: 11, textAlign: "center" }}>
            →
          </span>
          <span style={{ fontFamily: "var(--font-mono)", color: "var(--success)", fontWeight: 600 }}>
            snap {d.snap_to}px
          </span>
        </div>
      ))}
    </div>
  );
}
