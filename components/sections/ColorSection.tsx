"use client";
import { useState } from "react";
import { motion, AnimatePresence } from "framer-motion";
import SectionHeading from "@/components/ui/SectionHeading";
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip";
import { fadeUp, stagger, itemFadeUp } from "@/lib/motion-variants";
import { useIsMobile } from "@/lib/use-mobile";
import {
  buildBasePalette,
  buildSemanticPairs,
  getExtractionMeta,
} from "@/lib/tokens/loader";

// Source data — resolved at module load. The lib/tokens/loader walks the
// extractor's DTCG JSON; values are derived from real Figma observations.
const semanticPairs = buildSemanticPairs();
const basePalette = buildBasePalette();
const meta = getExtractionMeta();

const BUCKET_ORDER = [
  "surface",
  "surface-elevated",
  "text-n-icon",
  "border",
  "success",
  "danger",
  "warning",
  "info",
  "other",
];

const BUCKET_LABELS: Record<string, string> = {
  "surface": "Surface",
  "surface-elevated": "Surface · elevated",
  "text-n-icon": "Text & icon",
  "border": "Border",
  "success": "Success",
  "danger": "Danger",
  "warning": "Warning",
  "info": "Info",
  "other": "Other",
};

const BASE_BUCKET_ORDER = [
  "neutral-light",
  "neutral-dark",
  "grey",
  "green",
  "red",
  "orange",
  "blue",
  "other",
];

/* ── Single tile (one mode) ── */
function ModeTile({
  hex,
  mode,
  token,
}: {
  hex: string;
  mode: "light" | "dark";
  token: string;
}) {
  const [copied, setCopied] = useState(false);
  const copy = () => {
    navigator.clipboard.writeText(hex).catch(() => {});
    setCopied(true);
    setTimeout(() => setCopied(false), 1100);
  };
  return (
    <button
      onClick={copy}
      data-token={token}
      data-mode={mode}
      style={{
        flex: 1,
        height: 56,
        background: hex,
        border: "1px solid var(--border)",
        borderRadius: 6,
        position: "relative",
        cursor: "pointer",
        padding: 0,
        overflow: "hidden",
      }}
    >
      <div
        style={{
          position: "absolute",
          inset: 0,
          display: "flex",
          alignItems: "flex-end",
          justifyContent: "space-between",
          padding: "0 8px 6px 8px",
          fontFamily: "var(--font-mono)",
          fontSize: 10,
          color: mode === "light" ? "rgba(0,0,0,0.55)" : "rgba(255,255,255,0.7)",
          letterSpacing: "0.04em",
        }}
      >
        <span>{mode}</span>
        <span>{hex}</span>
      </div>
      <AnimatePresence>
        {copied && (
          <motion.div
            initial={{ opacity: 0 }}
            animate={{ opacity: 1 }}
            exit={{ opacity: 0 }}
            transition={{ duration: 0.12 }}
            style={{
              position: "absolute",
              inset: 0,
              display: "flex",
              alignItems: "center",
              justifyContent: "center",
              background: "rgba(0,0,0,0.45)",
              fontSize: 11,
              color: "#fff",
              letterSpacing: "0.05em",
              fontWeight: 600,
            }}
          >
            COPIED
          </motion.div>
        )}
      </AnimatePresence>
    </button>
  );
}

/* ── Pair card: light + dark side by side ── */
function PairCard({
  path,
  light,
  dark,
  description,
}: {
  path: string;
  light: string;
  dark: string;
  description?: string;
}) {
  const isMobile = useIsMobile();
  const segments = path.split(".");
  const leaf = segments.slice(2).join(".") || segments.at(-1) || path;

  return (
    <TooltipProvider delayDuration={80}>
      <Tooltip>
        <TooltipTrigger asChild>
          <motion.div
            variants={itemFadeUp}
            style={{
              padding: 12,
              background: "var(--bg-surface)",
              border: "1px solid var(--border)",
              borderRadius: 10,
              display: "flex",
              flexDirection: "column",
              gap: 8,
            }}
          >
            <div style={{ display: "flex", gap: 6 }}>
              <ModeTile hex={light} mode="light" token={path} />
              <ModeTile hex={dark} mode="dark" token={path} />
            </div>
            <div>
              <div
                style={{
                  fontSize: 12,
                  fontWeight: 600,
                  color: "var(--text-1)",
                  letterSpacing: "-0.1px",
                }}
              >
                {leaf}
              </div>
              <div
                style={{
                  fontSize: 10,
                  fontFamily: "var(--font-mono)",
                  color: "var(--text-3)",
                  marginTop: 2,
                  overflow: "hidden",
                  textOverflow: "ellipsis",
                  whiteSpace: "nowrap",
                }}
              >
                {path}
              </div>
            </div>
          </motion.div>
        </TooltipTrigger>
        {description && !isMobile && (
          <TooltipContent
            style={{
              background: "var(--bg-surface-2)",
              border: "1px solid var(--border)",
              color: "var(--text-1)",
              fontSize: 11,
              borderRadius: 6,
              padding: "8px 10px",
              maxWidth: 360,
              lineHeight: 1.5,
            }}
          >
            {description}
          </TooltipContent>
        )}
      </Tooltip>
    </TooltipProvider>
  );
}

/* ── Base palette tile (single mode-agnostic primitive) ── */
function PaletteTile({ hex, path }: { hex: string; path: string }) {
  const [copied, setCopied] = useState(false);
  const copy = () => {
    navigator.clipboard.writeText(hex).catch(() => {});
    setCopied(true);
    setTimeout(() => setCopied(false), 1100);
  };
  return (
    <button
      onClick={copy}
      data-token-scope="base"
      data-token={path}
      style={{
        flex: 1,
        minWidth: 0,
        display: "flex",
        flexDirection: "column",
        cursor: "pointer",
        background: "none",
        border: "none",
        padding: 0,
        position: "relative",
      }}
    >
      <div
        style={{
          height: 44,
          background: hex,
          borderRight: "1px solid rgba(0,0,0,0.06)",
          position: "relative",
          overflow: "hidden",
        }}
      >
        <AnimatePresence>
          {copied && (
            <motion.div
              initial={{ opacity: 0 }}
              animate={{ opacity: 1 }}
              exit={{ opacity: 0 }}
              transition={{ duration: 0.1 }}
              style={{
                position: "absolute",
                inset: 0,
                display: "flex",
                alignItems: "center",
                justifyContent: "center",
                background: "rgba(0,0,0,0.4)",
                color: "#fff",
                fontSize: 9,
                letterSpacing: "0.06em",
                fontWeight: 600,
              }}
            >
              COPIED
            </motion.div>
          )}
        </AnimatePresence>
      </div>
      <div
        style={{
          padding: "5px 6px",
          background: "var(--bg-surface)",
          borderRight: "1px solid var(--border)",
          borderTop: "1px solid var(--border)",
          fontFamily: "var(--font-mono)",
          fontSize: 9,
          color: "var(--text-3)",
          textAlign: "left",
        }}
      >
        {hex}
      </div>
    </button>
  );
}

/* ── Bucket section ── */
function PairsBucket({
  bucket,
  pairs,
}: {
  bucket: string;
  pairs: Array<{ path: string; leaf: string; light: string; dark: string; description?: string }>;
}) {
  if (pairs.length === 0) return null;
  return (
    <motion.div
      id={`color-${bucket}`}
      variants={fadeUp}
      initial="hidden"
      whileInView="visible"
      viewport={{ once: true, margin: "-40px" }}
      style={{ marginBottom: 40, scrollMarginTop: "calc(var(--header-h) + 32px)" }}
    >
      <h3 style={{ fontSize: 18, fontWeight: 600, letterSpacing: "-0.3px", color: "var(--text-1)", marginBottom: 6 }}>
        {BUCKET_LABELS[bucket] ?? bucket}
      </h3>
      <p style={{ fontSize: 13, color: "var(--text-2)", marginBottom: 20, maxWidth: 640 }}>
        {pairs.length} token{pairs.length === 1 ? "" : "s"} · light & dark mode pairs extracted from production usage.
      </p>
      <motion.div
        variants={stagger}
        initial="hidden"
        whileInView="visible"
        viewport={{ once: true, margin: "-30px" }}
        style={{
          display: "grid",
          gridTemplateColumns: "repeat(auto-fill, minmax(220px, 1fr))",
          gap: 12,
        }}
      >
        {pairs.map((p) => (
          <PairCard key={p.path} {...p} />
        ))}
      </motion.div>
    </motion.div>
  );
}

export default function ColorSection() {
  const grouped = new Map<string, typeof semanticPairs>();
  for (const p of semanticPairs) {
    if (!grouped.has(p.bucket)) grouped.set(p.bucket, []);
    grouped.get(p.bucket)!.push(p);
  }
  const orderedBuckets = [
    ...BUCKET_ORDER.filter((b) => grouped.has(b)),
    ...Array.from(grouped.keys()).filter((b) => !BUCKET_ORDER.includes(b)),
  ];

  const baseGrouped = new Map<string, typeof basePalette>();
  for (const p of basePalette) {
    if (!baseGrouped.has(p.bucket)) baseGrouped.set(p.bucket, []);
    baseGrouped.get(p.bucket)!.push(p);
  }
  const orderedBaseBuckets = [
    ...BASE_BUCKET_ORDER.filter((b) => baseGrouped.has(b)),
    ...Array.from(baseGrouped.keys()).filter((b) => !BASE_BUCKET_ORDER.includes(b)),
  ];

  return (
    <section
      id="color"
      style={{ marginBottom: 80, scrollMarginTop: "calc(var(--header-h) + 32px)" }}
    >
      <SectionHeading id="color" title="Color" />

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
          marginBottom: 16,
        }}
      >
        Tokens extracted from {meta.frames} mobile screen frames in the production app —
        {meta.pairs} light/dark pairs walked in lockstep, {meta.observations} fill observations
        clustered into <strong style={{ color: "var(--text-1)" }}>{meta.roles} semantic roles</strong>.
        Every swatch shows both modes; click to copy.
      </motion.p>

      <motion.p
        variants={fadeUp}
        initial="hidden"
        whileInView="visible"
        viewport={{ once: true }}
        style={{
          fontSize: 13,
          color: "var(--text-3)",
          lineHeight: 1.6,
          maxWidth: 720,
          marginBottom: 40,
          fontFamily: "var(--font-mono)",
        }}
      >
        sources: {meta.sources?.map((s: { kind: string; file_name?: string; file_key: string }) => `${s.kind}:${s.file_name || s.file_key.slice(0, 8)}`).join(" · ")}
      </motion.p>

      {/* ── Semantic tokens (light/dark pairs, the load-bearing ones) ── */}
      {orderedBuckets.map((bucket) => (
        <PairsBucket key={bucket} bucket={bucket} pairs={grouped.get(bucket)!} />
      ))}

      {/* ── Base palette (primitives) ── */}
      <motion.div
        id="color-base"
        variants={fadeUp}
        initial="hidden"
        whileInView="visible"
        viewport={{ once: true, margin: "-40px" }}
        style={{ marginTop: 64, scrollMarginTop: "calc(var(--header-h) + 32px)" }}
      >
        <h3
          style={{
            fontSize: 18,
            fontWeight: 600,
            letterSpacing: "-0.3px",
            color: "var(--text-1)",
            marginBottom: 6,
          }}
        >
          Base palette
        </h3>
        <p
          style={{
            fontSize: 13,
            color: "var(--text-2)",
            marginBottom: 24,
            maxWidth: 640,
          }}
        >
          {basePalette.length} distinct primitives observed across all sources.
          Reference these only when defining semantic tokens — never use them directly in components.
        </p>

        {orderedBaseBuckets.map((bucket) => {
          const items = baseGrouped.get(bucket)!;
          return (
            <div key={bucket} style={{ marginBottom: 18 }}>
              <div
                style={{
                  fontSize: 11,
                  fontWeight: 600,
                  color: "var(--text-3)",
                  textTransform: "uppercase",
                  letterSpacing: "0.07em",
                  marginBottom: 8,
                }}
              >
                {bucket} · {items.length}
              </div>
              <div
                style={{
                  display: "flex",
                  gap: 0,
                  borderRadius: 8,
                  overflow: "hidden",
                  border: "1px solid var(--border)",
                }}
              >
                {items.map((p) => (
                  <PaletteTile key={p.path} hex={p.hex} path={p.path} />
                ))}
              </div>
            </div>
          );
        })}
      </motion.div>
    </section>
  );
}
