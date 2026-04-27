"use client";

import { motion } from "framer-motion";
import FilesShell from "@/components/files/FilesShell";
import AuditLensCards from "@/components/files/AuditLensCards";
import { fadeUp } from "@/lib/motion-variants";
import { auditedFiles } from "@/lib/audit";
import type { AuditResult } from "@/lib/audit/types";
import type { NavGroup } from "@/components/Sidebar";

/**
 * FileDetail renders one file's audit using FilesShell. The sidebar lists
 * every audited file (cross-file navigation) plus this file's screens
 * (in-file scroll-spy anchors).
 */
export default function FileDetail({ result }: { result: AuditResult }) {
  const screens = result.screens ?? [];
  const allFiles = auditedFiles();

  // Sidebar nav: this file's screens (with section ids that match the
  // anchored panels below), then a "Other files" group linking siblings.
  const screenGroup: NavGroup = {
    label: result.file_name,
    defaultOpen: true,
    sub: screens.map((s) => ({
      label: s.name,
      href: `#screen-${s.slug}`,
    })),
  };

  const otherFiles: NavGroup = {
    label: "Other files",
    defaultOpen: false,
    sub: allFiles
      .filter((f) => f.file_slug !== result.file_slug)
      .slice(0, 12)
      .map((f) => ({
        label: f.file_name,
        href: `/files/${f.file_slug}`,
      })),
  };

  const nav = otherFiles.sub.length > 0 ? [screenGroup, otherFiles] : [screenGroup];
  const sectionIds = screens.map((s) => `screen-${s.slug}`);

  const overallCoverage = Math.round(result.overall_coverage * 1000) / 10;
  const overallFromDS = Math.round(result.overall_from_ds * 1000) / 10;

  return (
    <FilesShell nav={nav} title="Files" sectionIds={sectionIds}>
      <Hero
        name={result.file_name}
        screenCount={screens.length}
        coverage={overallCoverage}
        fromDS={overallFromDS}
        extractedAt={result.extracted_at}
        fileSlug={result.file_slug}
      />

      {screens.length === 0 ? (
        <p
          style={{
            padding: "48px 0",
            textAlign: "center",
            color: "var(--text-3)",
            fontFamily: "var(--font-mono)",
            fontSize: 13,
          }}
        >
          No final-design screens found in this file. Run the plugin&apos;s{" "}
          <em>Audit file</em> command from inside the Figma file, or pin{" "}
          <code style={{ fontFamily: "var(--font-mono)" }}>final_pages</code> overrides
          in <code style={{ fontFamily: "var(--font-mono)" }}>lib/audit-files.json</code>.
        </p>
      ) : (
        screens.map((s) => (
          <section
            key={s.node_id}
            id={`screen-${s.slug}`}
            style={{
              marginBottom: 56,
              scrollMarginTop: "calc(var(--header-h) + 32px)",
            }}
          >
            <motion.h2
              variants={fadeUp}
              initial="hidden"
              whileInView="visible"
              viewport={{ once: true }}
              style={{
                fontSize: 22,
                fontWeight: 600,
                letterSpacing: "-0.4px",
                color: "var(--text-1)",
                marginBottom: 4,
              }}
            >
              {s.name}
            </motion.h2>
            <p
              style={{
                fontSize: 11,
                fontFamily: "var(--font-mono)",
                color: "var(--text-3)",
                marginBottom: 18,
              }}
            >
              {s.node_count} nodes · {s.fixes.length} fix{s.fixes.length === 1 ? "" : "es"} ·{" "}
              {s.component_summary.from_ds}/{s.component_summary.ambiguous}/
              {s.component_summary.custom} DS/Amb/Cust
            </p>
            <AuditLensCards screen={s} />
          </section>
        ))
      )}
    </FilesShell>
  );
}

function Hero({
  name,
  screenCount,
  coverage,
  fromDS,
  extractedAt,
  fileSlug,
}: {
  name: string;
  screenCount: number;
  coverage: number;
  fromDS: number;
  extractedAt: string;
  fileSlug: string;
}) {
  return (
    <motion.div
      initial={{ opacity: 0, y: 16 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.45, ease: [0.33, 1, 0.68, 1] }}
      style={{
        borderBottom: "1px solid var(--border)",
        paddingBottom: 32,
        marginBottom: 32,
      }}
    >
      <p
        style={{
          fontSize: 11,
          fontFamily: "var(--font-mono)",
          color: "var(--text-3)",
          margin: "0 0 6px",
          textTransform: "uppercase",
          letterSpacing: "0.07em",
        }}
      >
        Files / {fileSlug}
      </p>
      <h1
        style={{
          fontSize: 40,
          fontWeight: 700,
          letterSpacing: "-1.2px",
          color: "var(--text-1)",
          margin: "0 0 12px",
          lineHeight: 1.05,
        }}
      >
        {name}
      </h1>
      <div style={{ display: "flex", gap: 24, alignItems: "baseline", flexWrap: "wrap" }}>
        <Stat label="Coverage" value={`${coverage}%`} accent />
        <Stat label="DS comps" value={`${fromDS}%`} />
        <Stat label="Screens" value={String(screenCount)} />
      </div>
      <p
        style={{
          fontSize: 12,
          fontFamily: "var(--font-mono)",
          color: "var(--text-3)",
          margin: "12px 0 0",
        }}
      >
        source: lib/audit/{fileSlug}.json · audited {timeAgo(extractedAt)}
      </p>
    </motion.div>
  );
}

function Stat({
  label,
  value,
  accent,
}: {
  label: string;
  value: string;
  accent?: boolean;
}) {
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 2 }}>
      <span
        style={{
          fontSize: 26,
          fontWeight: 700,
          color: accent ? "var(--accent)" : "var(--text-1)",
          letterSpacing: "-0.4px",
        }}
      >
        {value}
      </span>
      <span
        style={{
          fontSize: 10,
          color: "var(--text-3)",
          textTransform: "uppercase",
          letterSpacing: "0.07em",
          fontWeight: 600,
        }}
      >
        {label}
      </span>
    </div>
  );
}

function timeAgo(iso: string): string {
  const t = new Date(iso);
  if (t.getUTCFullYear() < 2000) return "—";
  const ms = Date.now() - t.getTime();
  const m = Math.floor(ms / 60000);
  if (m < 1) return "just now";
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  const d = Math.floor(h / 24);
  return `${d}d ago`;
}
