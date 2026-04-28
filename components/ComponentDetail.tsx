"use client";

import { motion } from "framer-motion";
import Link from "next/link";
import { fadeUp, stagger, itemFadeUp } from "@/lib/motion-variants";
import {
  iconURL,
  defaultVariantOf,
  type IconEntry,
  type VariantEntry,
  type ComponentProperty,
  type LayoutInfo,
  type FillInfo,
  type EffectInfo,
  type CornerInfo,
  type ChildSummary,
} from "@/lib/icons/manifest";
import { showToast } from "@/components/ui/Toast";

/**
 * ComponentDetail — the single-page spec sheet for one component. This is
 * what a designer screenshots into Slack, what an engineer reads before
 * implementing, what a DS lead reviews for completeness.
 *
 * Sections, in scroll order:
 *   1. Overview      — hero render of default variant + name + description
 *   2. Variants      — N-axis matrix (table form); thumbnails per variant
 *   3. Props         — every component property, all 4 types
 *   4. Layout        — autolayout config table (mode, padding, gap, ...)
 *   5. Appearance    — fills, strokes, effects, corner radius
 *                      with bound-variable badges
 *   6. Structure     — first-level children with property cascade refs
 *   7. Code          — placeholder for snippet generation (Tier B)
 *
 * Information hierarchy: hero is loud (large render + name + axis pills),
 * each section header is small and consistent (uppercase tracked label).
 * No section renders if its data is missing — empty states only appear
 * when explicitly meaningful (Variants/Props always render; Layout +
 * Appearance only when the default variant has them).
 */
export default function ComponentDetail({ entry }: { entry: IconEntry }) {
  const def = defaultVariantOf(entry);

  return (
    <motion.div variants={stagger} initial="hidden" animate="visible" style={{ display: "flex", flexDirection: "column", gap: 48 }}>
      <Hero entry={entry} def={def} />
      {entry.variants && entry.variants.length > 0 && (
        <VariantsSection entry={entry} />
      )}
      {entry.prop_defs && entry.prop_defs.length > 0 && (
        <PropsSection props={entry.prop_defs} />
      )}
      {def?.layout && (
        <LayoutSection layout={def.layout} />
      )}
      {def && (def.fills?.length || def.strokes?.length || def.effects?.length || def.corner) && (
        <AppearanceSection variant={def} />
      )}
      {def?.children && def.children.length > 0 && (
        <StructureSection variant={def} />
      )}
      <CodeSection entry={entry} def={def} />
    </motion.div>
  );
}

/* ── Hero ──────────────────────────────────────────────────────────────── */

function Hero({ entry, def }: { entry: IconEntry; def: VariantEntry | null }) {
  const previewURL = def
    ? `/icons/glyph/${def.file.replace(/^variants\//, "variants/")}`
    : iconURL(entry);
  return (
    <motion.section
      variants={fadeUp}
      id="overview"
      style={{
        scrollMarginTop: "calc(var(--header-h) + 32px)",
      }}
    >
      <Link
        href="/components"
        style={{
          display: "inline-flex",
          alignItems: "center",
          gap: 6,
          marginBottom: 16,
          fontSize: 12,
          fontFamily: "var(--font-mono)",
          color: "var(--text-3)",
          textDecoration: "none",
        }}
      >
        <svg width="11" height="11" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
          <path d="M10 4l-4 4 4 4" />
        </svg>
        Components / {entry.category}
      </Link>
      <h1
        style={{
          fontSize: 48,
          fontWeight: 700,
          letterSpacing: "-1.5px",
          color: "var(--text-1)",
          marginBottom: 12,
          lineHeight: 1.05,
        }}
      >
        {entry.name}
      </h1>
      {entry.description && (
        <p style={{ fontSize: 15, color: "var(--text-2)", lineHeight: 1.65, maxWidth: 640, marginBottom: 16 }}>
          {entry.description}
        </p>
      )}
      <div
        style={{
          display: "flex",
          gap: 8,
          flexWrap: "wrap",
          fontSize: 11,
          fontFamily: "var(--font-mono)",
          color: "var(--text-3)",
          marginBottom: 24,
        }}
      >
        <Pill>{entry.category}</Pill>
        <Pill>
          {entry.variants?.length ?? 0} variant
          {entry.variants?.length === 1 ? "" : "s"}
        </Pill>
        {entry.prop_defs && (
          <Pill>
            {entry.prop_defs.length} prop{entry.prop_defs.length === 1 ? "" : "s"}
          </Pill>
        )}
        {entry.single_variant_set && <Pill>single-variant</Pill>}
        <Pill>source: {entry.source ?? "glyph"}</Pill>
      </div>
      {/* Hero preview frame */}
      <div
        style={{
          padding: 36,
          background: "var(--bg-surface)",
          border: "1px solid var(--border)",
          borderRadius: 12,
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          minHeight: 220,
          backgroundImage:
            "repeating-linear-gradient(45deg, transparent 0 8px, color-mix(in srgb, var(--text-3) 4%, transparent) 8px 10px)",
        }}
      >
        <img
          src={previewURL}
          alt={entry.name}
          style={{ maxWidth: "100%", maxHeight: 320, objectFit: "contain" }}
        />
      </div>
      {entry.doc_links && entry.doc_links.length > 0 && (
        <div style={{ marginTop: 12, display: "flex", gap: 8, flexWrap: "wrap" }}>
          {entry.doc_links.map((url) => (
            <a
              key={url}
              href={url}
              target="_blank"
              rel="noreferrer"
              style={{
                fontSize: 11,
                fontFamily: "var(--font-mono)",
                color: "var(--accent)",
                textDecoration: "none",
                padding: "4px 8px",
                background: "var(--bg-surface)",
                border: "1px solid var(--border)",
                borderRadius: 6,
              }}
            >
              ↗ {labelFromUrl(url)}
            </a>
          ))}
        </div>
      )}
    </motion.section>
  );
}

/* ── Section helpers ───────────────────────────────────────────────────── */

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

function Pill({ children }: { children: React.ReactNode }) {
  return (
    <span
      style={{
        display: "inline-flex",
        alignItems: "center",
        padding: "3px 8px",
        background: "var(--bg-surface)",
        border: "1px solid var(--border)",
        borderRadius: 999,
        whiteSpace: "nowrap",
      }}
    >
      {children}
    </span>
  );
}

/* ── Variants ──────────────────────────────────────────────────────────── */

function VariantsSection({ entry }: { entry: IconEntry }) {
  const variants = entry.variants ?? [];
  return (
    <motion.section
      variants={fadeUp}
      id="variants"
      style={{ scrollMarginTop: "calc(var(--header-h) + 32px)" }}
    >
      <SectionHeader id="variants" eyebrow="02" title="Variants" count={variants.length} />
      <div
        style={{
          display: "grid",
          gridTemplateColumns: "repeat(auto-fill, minmax(220px, 1fr))",
          gap: 12,
        }}
      >
        {variants.map((v) => (
          <VariantCard key={v.variant_id} variant={v} entrySlug={entry.slug} />
        ))}
      </div>
    </motion.section>
  );
}

function VariantCard({ variant, entrySlug }: { variant: VariantEntry; entrySlug: string }) {
  const url = `/icons/glyph/${variant.file.replace(/^variants\//, "variants/")}`;
  const boundCount = countBound(variant);
  return (
    <motion.div
      variants={itemFadeUp}
      style={{
        padding: 12,
        background: "var(--bg-surface)",
        border: variant.is_default ? "1px solid var(--accent)" : "1px solid var(--border)",
        borderRadius: 10,
        display: "flex",
        flexDirection: "column",
        gap: 10,
        position: "relative",
      }}
    >
      {variant.is_default && (
        <span
          style={{
            position: "absolute",
            top: 8,
            right: 8,
            fontSize: 9,
            padding: "2px 6px",
            background: "var(--accent)",
            color: "var(--text-on-accent, #fff)",
            borderRadius: 4,
            fontFamily: "var(--font-mono)",
            fontWeight: 600,
            letterSpacing: "0.04em",
          }}
        >
          DEFAULT
        </span>
      )}
      <div
        style={{
          minHeight: 100,
          padding: 8,
          background: "var(--bg-surface-2)",
          border: "1px solid var(--border)",
          borderRadius: 6,
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
        }}
      >
        <img src={url} alt={variant.name} loading="lazy" style={{ maxWidth: "100%", maxHeight: 100, objectFit: "contain" }} />
      </div>
      {variant.axis_values && (
        <div style={{ display: "flex", flexWrap: "wrap", gap: 4 }}>
          {Object.entries(variant.axis_values).map(([k, v]) => (
            <span
              key={k}
              style={{
                fontFamily: "var(--font-mono)",
                fontSize: 10,
                padding: "2px 7px",
                background: "var(--bg-surface-2)",
                border: "1px solid var(--border)",
                borderRadius: 4,
                color: "var(--text-2)",
              }}
            >
              <span style={{ color: "var(--text-3)" }}>{k}</span>{" "}
              <span style={{ color: "var(--text-1)" }}>{v}</span>
            </span>
          ))}
        </div>
      )}
      <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", fontSize: 10, fontFamily: "var(--font-mono)", color: "var(--text-3)" }}>
        <span>{variant.width} × {variant.height}</span>
        {boundCount > 0 && (
          <span style={{ color: "var(--accent)" }}>
            {boundCount} bound
          </span>
        )}
      </div>
    </motion.div>
  );
}

function countBound(v: VariantEntry): number {
  let n = 0;
  for (const f of v.fills ?? []) if (f.bound_variable_id) n++;
  for (const s of v.strokes ?? []) if (s.bound_variable_id) n++;
  for (const e of v.effects ?? []) if (e.bound_variable_id) n++;
  if (v.corner?.bound_variable_id) n++;
  for (const _id of Object.values(v.bound_variables ?? {})) n++;
  return n;
}

/* ── Props ─────────────────────────────────────────────────────────────── */

function PropsSection({ props }: { props: ComponentProperty[] }) {
  return (
    <motion.section
      variants={fadeUp}
      id="props"
      style={{ scrollMarginTop: "calc(var(--header-h) + 32px)" }}
    >
      <SectionHeader id="props" eyebrow="03" title="Properties" count={props.length} />
      <div
        style={{
          background: "var(--bg-surface)",
          border: "1px solid var(--border)",
          borderRadius: 10,
          overflow: "hidden",
        }}
      >
        <div
          style={{
            display: "grid",
            gridTemplateColumns: "minmax(160px, 1.2fr) 100px minmax(140px, 1fr) 1fr",
            padding: "10px 14px",
            borderBottom: "1px solid var(--border)",
            background: "var(--bg-surface-2)",
            fontSize: 10,
            fontWeight: 600,
            color: "var(--text-3)",
            textTransform: "uppercase",
            letterSpacing: "0.06em",
          }}
        >
          <span>Name</span>
          <span>Type</span>
          <span>Default</span>
          <span>Options</span>
        </div>
        {props.map((p) => (
          <PropRow key={p.name} prop={p} />
        ))}
      </div>
    </motion.section>
  );
}

function PropRow({ prop }: { prop: ComponentProperty }) {
  const typeColor: Record<string, string> = {
    VARIANT: "var(--accent)",
    BOOLEAN: "var(--success)",
    TEXT: "var(--warning)",
    INSTANCE_SWAP: "var(--text-2)",
  };
  return (
    <div
      style={{
        display: "grid",
        gridTemplateColumns: "minmax(160px, 1.2fr) 100px minmax(140px, 1fr) 1fr",
        padding: "12px 14px",
        borderBottom: "1px solid var(--border)",
        alignItems: "start",
        gap: 12,
      }}
    >
      <span style={{ fontFamily: "var(--font-mono)", fontSize: 12, color: "var(--text-1)", wordBreak: "break-all" }}>
        {prop.name}
      </span>
      <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: typeColor[prop.type] || "var(--text-2)", fontWeight: 600 }}>
        {prop.type}
      </span>
      <span style={{ fontFamily: "var(--font-mono)", fontSize: 12, color: "var(--text-2)" }}>
        {formatDefault(prop.default_value)}
      </span>
      <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--text-3)", lineHeight: 1.5 }}>
        {prop.variant_options?.join(" · ")}
        {prop.preferred_values && prop.preferred_values.length > 0 && (
          <span>
            {prop.preferred_values.length} preferred
          </span>
        )}
      </span>
    </div>
  );
}

function formatDefault(v: ComponentProperty["default_value"]): string {
  if (v == null) return "—";
  if (typeof v === "boolean") return v ? "true" : "false";
  return String(v);
}

/* ── Layout ────────────────────────────────────────────────────────────── */

function LayoutSection({ layout }: { layout: LayoutInfo }) {
  const rows: { label: string; value: string }[] = [];
  rows.push({ label: "Mode", value: layout.mode ?? "—" });
  if (layout.wrap) rows.push({ label: "Wrap", value: layout.wrap });
  rows.push({
    label: "Padding",
    value: `${layout.padding_top ?? 0} ${layout.padding_right ?? 0} ${layout.padding_bottom ?? 0} ${layout.padding_left ?? 0}`,
  });
  if (layout.item_spacing != null) rows.push({ label: "Gap", value: `${layout.item_spacing}` });
  if (layout.primary_align) rows.push({ label: "Primary align", value: layout.primary_align });
  if (layout.counter_align) rows.push({ label: "Counter align", value: layout.counter_align });
  if (layout.primary_sizing) rows.push({ label: "Primary sizing", value: layout.primary_sizing });
  if (layout.counter_sizing) rows.push({ label: "Counter sizing", value: layout.counter_sizing });
  if (layout.min_width) rows.push({ label: "Min width", value: `${layout.min_width}` });
  if (layout.max_width) rows.push({ label: "Max width", value: `${layout.max_width}` });
  return (
    <motion.section
      variants={fadeUp}
      id="layout"
      style={{ scrollMarginTop: "calc(var(--header-h) + 32px)" }}
    >
      <SectionHeader id="layout" eyebrow="04" title="Layout" />
      <div
        style={{
          display: "grid",
          gridTemplateColumns: "1fr 1fr",
          gap: 0,
          background: "var(--bg-surface)",
          border: "1px solid var(--border)",
          borderRadius: 10,
          overflow: "hidden",
        }}
      >
        {rows.map((r) => (
          <div
            key={r.label}
            style={{
              padding: "10px 14px",
              borderBottom: "1px solid var(--border)",
              borderRight: "1px solid var(--border)",
              display: "flex",
              alignItems: "center",
              justifyContent: "space-between",
              gap: 12,
              fontSize: 12,
            }}
          >
            <span style={{ color: "var(--text-3)" }}>{r.label}</span>
            <span style={{ fontFamily: "var(--font-mono)", color: "var(--text-1)" }}>{r.value}</span>
          </div>
        ))}
      </div>
    </motion.section>
  );
}

/* ── Appearance ────────────────────────────────────────────────────────── */

function AppearanceSection({ variant }: { variant: VariantEntry }) {
  const fills = variant.fills ?? [];
  const strokes = variant.strokes ?? [];
  const effects = variant.effects ?? [];
  const corner = variant.corner;

  return (
    <motion.section
      variants={fadeUp}
      id="appearance"
      style={{ scrollMarginTop: "calc(var(--header-h) + 32px)" }}
    >
      <SectionHeader id="appearance" eyebrow="05" title="Appearance" />
      <div style={{ display: "flex", flexDirection: "column", gap: 16 }}>
        {fills.length > 0 && (
          <PaintGroup title="Fills" paints={fills} />
        )}
        {strokes.length > 0 && (
          <PaintGroup title="Strokes" paints={strokes} extra={variant.stroke_weight != null ? `weight ${variant.stroke_weight}` : undefined} />
        )}
        {effects.length > 0 && (
          <EffectsGroup effects={effects} />
        )}
        {corner && (
          <CornerGroup corner={corner} />
        )}
      </div>
    </motion.section>
  );
}

function PaintGroup({ title, paints, extra }: { title: string; paints: FillInfo[]; extra?: string }) {
  return (
    <div style={{ background: "var(--bg-surface)", border: "1px solid var(--border)", borderRadius: 10, overflow: "hidden" }}>
      <div style={{ padding: "10px 14px", background: "var(--bg-surface-2)", borderBottom: "1px solid var(--border)", display: "flex", justifyContent: "space-between", alignItems: "baseline" }}>
        <span style={{ fontSize: 11, fontWeight: 600, color: "var(--text-2)", textTransform: "uppercase", letterSpacing: "0.06em" }}>
          {title}
        </span>
        {extra && <span style={{ fontSize: 10, fontFamily: "var(--font-mono)", color: "var(--text-3)" }}>{extra}</span>}
      </div>
      {paints.map((p, i) => (
        <div
          key={i}
          style={{
            display: "grid",
            gridTemplateColumns: "32px 80px 1fr auto",
            gap: 12,
            alignItems: "center",
            padding: "10px 14px",
            borderBottom: i < paints.length - 1 ? "1px solid var(--border)" : "none",
          }}
        >
          <div
            style={{
              width: 24,
              height: 24,
              borderRadius: 4,
              background: p.color || "transparent",
              border: "1px solid var(--border)",
              opacity: p.visible === false ? 0.35 : 1,
            }}
          />
          <span style={{ fontSize: 11, fontFamily: "var(--font-mono)", color: "var(--text-2)" }}>
            {p.type.replace("GRADIENT_", "GRAD ")}
          </span>
          <span style={{ fontSize: 12, fontFamily: "var(--font-mono)", color: "var(--text-1)" }}>
            {p.color || "—"}
            {p.opacity != null && p.opacity < 1 && (
              <span style={{ color: "var(--text-3)" }}> · {Math.round(p.opacity * 100)}%</span>
            )}
          </span>
          {p.bound_variable_id ? (
            <span
              style={{
                fontSize: 10,
                fontFamily: "var(--font-mono)",
                color: "var(--accent)",
                background: "var(--accent-soft, color-mix(in srgb, var(--accent) 14%, transparent))",
                padding: "2px 7px",
                borderRadius: 4,
                fontWeight: 600,
              }}
              title={p.bound_variable_id}
            >
              ◆ bound
            </span>
          ) : (
            <span style={{ fontSize: 10, fontFamily: "var(--font-mono)", color: "var(--text-3)" }}>raw</span>
          )}
        </div>
      ))}
    </div>
  );
}

function EffectsGroup({ effects }: { effects: EffectInfo[] }) {
  return (
    <div style={{ background: "var(--bg-surface)", border: "1px solid var(--border)", borderRadius: 10, overflow: "hidden" }}>
      <div style={{ padding: "10px 14px", background: "var(--bg-surface-2)", borderBottom: "1px solid var(--border)" }}>
        <span style={{ fontSize: 11, fontWeight: 600, color: "var(--text-2)", textTransform: "uppercase", letterSpacing: "0.06em" }}>
          Effects
        </span>
      </div>
      {effects.map((e, i) => (
        <div
          key={i}
          style={{
            padding: "10px 14px",
            borderBottom: i < effects.length - 1 ? "1px solid var(--border)" : "none",
            display: "flex",
            justifyContent: "space-between",
            alignItems: "center",
            gap: 12,
            fontSize: 11,
            fontFamily: "var(--font-mono)",
          }}
        >
          <span style={{ color: "var(--text-2)" }}>{e.type}</span>
          <span style={{ color: "var(--text-1)" }}>
            r{e.radius ?? 0} · ({e.offset_x ?? 0}, {e.offset_y ?? 0}) · {e.color || ""}
          </span>
          {e.bound_variable_id ? (
            <span style={{ color: "var(--accent)" }}>◆ bound</span>
          ) : (
            <span style={{ color: "var(--text-3)" }}>raw</span>
          )}
        </div>
      ))}
    </div>
  );
}

function CornerGroup({ corner }: { corner: CornerInfo }) {
  return (
    <div style={{ background: "var(--bg-surface)", border: "1px solid var(--border)", borderRadius: 10, overflow: "hidden" }}>
      <div style={{ padding: "10px 14px", background: "var(--bg-surface-2)", borderBottom: "1px solid var(--border)" }}>
        <span style={{ fontSize: 11, fontWeight: 600, color: "var(--text-2)", textTransform: "uppercase", letterSpacing: "0.06em" }}>
          Corner radius
        </span>
      </div>
      <div style={{ padding: "10px 14px", display: "flex", justifyContent: "space-between", alignItems: "center", gap: 12, fontSize: 12, fontFamily: "var(--font-mono)" }}>
        <span style={{ color: "var(--text-2)" }}>
          {corner.individual ? `[${corner.individual.join(", ")}]` : `${corner.uniform ?? 0}`}
          {corner.smoothing != null && corner.smoothing > 0 && (
            <span style={{ color: "var(--text-3)" }}> · smoothing {corner.smoothing}</span>
          )}
        </span>
        {corner.bound_variable_id && (
          <span style={{ color: "var(--accent)" }}>◆ bound</span>
        )}
      </div>
    </div>
  );
}

/* ── Structure ─────────────────────────────────────────────────────────── */

function StructureSection({ variant }: { variant: VariantEntry }) {
  const children = variant.children ?? [];
  return (
    <motion.section
      variants={fadeUp}
      id="structure"
      style={{ scrollMarginTop: "calc(var(--header-h) + 32px)" }}
    >
      <SectionHeader id="structure" eyebrow="06" title="Structure" count={children.length} />
      <div style={{ background: "var(--bg-surface)", border: "1px solid var(--border)", borderRadius: 10, overflow: "hidden" }}>
        {children.map((c) => (
          <ChildRow key={c.id} child={c} />
        ))}
      </div>
    </motion.section>
  );
}

function ChildRow({ child }: { child: ChildSummary }) {
  return (
    <div
      style={{
        padding: "12px 14px",
        borderBottom: "1px solid var(--border)",
        display: "grid",
        gridTemplateColumns: "minmax(140px, 1fr) 80px 1fr",
        gap: 12,
        fontSize: 12,
        alignItems: "start",
      }}
    >
      <div style={{ minWidth: 0 }}>
        <div style={{ fontWeight: 500, color: "var(--text-1)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
          {child.name}
        </div>
        {child.characters && (
          <div style={{ fontSize: 11, color: "var(--text-3)", fontFamily: "var(--font-mono)", marginTop: 2 }}>
            “{child.characters}”
          </div>
        )}
      </div>
      <span style={{ fontSize: 11, fontFamily: "var(--font-mono)", color: "var(--text-3)" }}>
        {child.type}
      </span>
      <div style={{ display: "flex", flexWrap: "wrap", gap: 4 }}>
        {child.property_refs &&
          Object.entries(child.property_refs).map(([k, v]) => (
            <span
              key={k}
              style={{
                fontSize: 10,
                fontFamily: "var(--font-mono)",
                padding: "2px 6px",
                background: "var(--accent-soft, color-mix(in srgb, var(--accent) 12%, transparent))",
                color: "var(--accent)",
                borderRadius: 4,
              }}
              title={`${k} cascades from prop ${v}`}
            >
              {k} ← {v}
            </span>
          ))}
        {child.bound_variables &&
          Object.entries(child.bound_variables).map(([k]) => (
            <span
              key={k}
              style={{
                fontSize: 10,
                fontFamily: "var(--font-mono)",
                padding: "2px 6px",
                background: "var(--bg-surface-2)",
                color: "var(--text-2)",
                borderRadius: 4,
              }}
            >
              ◆ {k}
            </span>
          ))}
      </div>
    </div>
  );
}

/* ── Code (placeholder) ────────────────────────────────────────────────── */

function CodeSection({ entry, def }: { entry: IconEntry; def: VariantEntry | null }) {
  const snippet = generateReactSnippet(entry, def);
  const copy = () => {
    navigator.clipboard?.writeText(snippet).catch(() => {});
    showToast({ message: "Snippet copied", detail: entry.slug, tone: "success" });
  };
  return (
    <motion.section
      variants={fadeUp}
      id="code"
      style={{ scrollMarginTop: "calc(var(--header-h) + 32px)" }}
    >
      <SectionHeader id="code" eyebrow="07" title="Code" />
      <div style={{ background: "var(--bg-surface)", border: "1px solid var(--border)", borderRadius: 10, overflow: "hidden" }}>
        <div
          style={{
            padding: "10px 14px",
            background: "var(--bg-surface-2)",
            borderBottom: "1px solid var(--border)",
            display: "flex",
            justifyContent: "space-between",
            alignItems: "center",
          }}
        >
          <span style={{ fontSize: 11, fontWeight: 600, color: "var(--text-2)", textTransform: "uppercase", letterSpacing: "0.06em" }}>
            React (preview)
          </span>
          <button
            onClick={copy}
            style={{
              padding: "4px 10px",
              fontSize: 11,
              fontWeight: 600,
              fontFamily: "var(--font-mono)",
              color: "var(--text-2)",
              background: "var(--bg-surface)",
              border: "1px solid var(--border)",
              borderRadius: 4,
              cursor: "pointer",
            }}
          >
            Copy
          </button>
        </div>
        <pre
          style={{
            margin: 0,
            padding: "14px 16px",
            fontFamily: "var(--font-mono)",
            fontSize: 12,
            color: "var(--text-1)",
            lineHeight: 1.6,
            overflow: "auto",
          }}
        >
          {snippet}
        </pre>
      </div>
      <div style={{ marginTop: 8, fontSize: 11, color: "var(--text-3)", fontFamily: "var(--font-mono)", lineHeight: 1.55 }}>
        Snippet is a directional preview generated from prop defs and the
        default variant. Wire to your component library's actual import path.
      </div>
    </motion.section>
  );
}

function generateReactSnippet(entry: IconEntry, def: VariantEntry | null): string {
  const componentName = entry.name.replace(/[^a-zA-Z0-9]/g, "");
  const props: string[] = [];
  if (def?.axis_values) {
    for (const [k, v] of Object.entries(def.axis_values)) {
      const camelKey = k.replace(/[^a-zA-Z0-9]/g, "");
      props.push(`  ${camelKey}="${v}"`);
    }
  }
  for (const p of entry.prop_defs ?? []) {
    if (p.type === "VARIANT") continue;
    if (p.default_value == null) continue;
    const cleanName = p.name.split("#")[0].replace(/[^a-zA-Z0-9]/g, "");
    if (typeof p.default_value === "boolean") {
      if (p.default_value) props.push(`  ${cleanName}`);
    } else {
      props.push(`  ${cleanName}="${p.default_value}"`);
    }
  }
  return `<${componentName}\n${props.join("\n")}\n/>`;
}

/* ── Misc ──────────────────────────────────────────────────────────────── */

function labelFromUrl(url: string): string {
  try {
    const u = new URL(url);
    return u.hostname.replace(/^www\./, "");
  } catch {
    return url;
  }
}
