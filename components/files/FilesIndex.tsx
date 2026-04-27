"use client";

import Link from "next/link";
import { motion } from "framer-motion";
import { fadeUp, stagger, itemFadeUp } from "@/lib/motion-variants";
import { auditedFiles, hasAuditData, provenanceLine } from "@/lib/audit";
import EmptyAuditState from "@/components/audit/EmptyAuditState";

/**
 * /files index. Cards animate in via Foundations stagger; honest empty
 * state when no audit has run; theme-aware throughout.
 */
export default function FilesIndex() {
  const files = auditedFiles();

  if (!hasAuditData() || files.length === 0) {
    return <EmptyState />;
  }

  return (
    <>
      {/* Sidebar references this id; scroll-spy seeds the active pill on
       *  "All files" until the user scrolls past the hero. */}
      <div id="all-files">
        <Hero count={files.length} />
      </div>
      <motion.div
        variants={stagger}
        initial="hidden"
        animate="visible"
        style={{
          display: "grid",
          gridTemplateColumns: "repeat(auto-fill, minmax(300px, 1fr))",
          gap: 14,
        }}
      >
        {files.map((f) => (
          <FileCard key={f.file_slug} f={f} />
        ))}
      </motion.div>
    </>
  );
}

function Hero({ count }: { count: number }) {
  return (
    <motion.div
      variants={fadeUp}
      initial="hidden"
      animate="visible"
      style={{
        borderBottom: "1px solid var(--border)",
        paddingBottom: 32,
        marginBottom: 32,
      }}
    >
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
        Files
      </h1>
      <p style={{ fontSize: 15, color: "var(--text-2)", lineHeight: 1.65, maxWidth: 640 }}>
        Per-Figma-file audit rollups. Click any card to drill into per-screen token
        coverage, component usage, and drift suggestions. Files are auto-registered
        when designers run the plugin&apos;s <em>Audit file</em> command.
      </p>
      <p style={{ fontSize: 12, color: "var(--text-3)", fontFamily: "var(--font-mono)", marginTop: 8 }}>
        {count} files · {provenanceLine()}
      </p>
    </motion.div>
  );
}

function FileCard({
  f,
}: {
  f: ReturnType<typeof auditedFiles>[number];
}) {
  const coverage = Math.round(f.overall_coverage * 1000) / 10;
  const fromDS = Math.round(f.overall_from_ds * 1000) / 10;
  const tone =
    coverage >= 90 ? "good" : coverage >= 70 ? "neutral" : "warning";

  return (
    <motion.div variants={itemFadeUp} id={`file-${f.file_slug}`} style={{ scrollMarginTop: 88 }}>
      <Link
        href={`/files/${f.file_slug}`}
        style={{
          display: "block",
          textDecoration: "none",
          color: "inherit",
        }}
      >
        <motion.div
          whileHover={{ y: -2, boxShadow: "0 8px 24px rgba(0,0,0,0.10)" }}
          transition={{ type: "spring", stiffness: 300, damping: 22 }}
          style={{
            display: "flex",
            flexDirection: "column",
            gap: 12,
            padding: 20,
            background: "var(--bg-surface)",
            border: "1px solid var(--border)",
            borderRadius: 10,
          }}
        >
          <div>
            <h3
              style={{
                margin: 0,
                fontSize: 16,
                fontWeight: 600,
                color: "var(--text-1)",
                letterSpacing: "-0.2px",
                overflow: "hidden",
                textOverflow: "ellipsis",
                whiteSpace: "nowrap",
              }}
            >
              {f.file_name}
            </h3>
            <p
              style={{
                margin: "2px 0 0",
                fontSize: 11,
                color: "var(--text-3)",
                fontFamily: "var(--font-mono)",
              }}
            >
              {f.screen_count} screen{f.screen_count === 1 ? "" : "s"} · audited{" "}
              {timeAgo(f.extracted_at)}
            </p>
          </div>

          <div style={{ display: "flex", gap: 16, alignItems: "baseline" }}>
            <Stat
              label="Coverage"
              value={`${coverage}%`}
              tone={tone}
            />
            <Stat
              label="DS comps"
              value={`${fromDS}%`}
              tone={fromDS >= 70 ? "good" : "neutral"}
            />
          </div>

          {f.headline_drift_hex && (
            <div
              style={{
                display: "flex",
                alignItems: "center",
                gap: 8,
                padding: "6px 10px",
                background: "var(--bg-surface-2)",
                borderRadius: 6,
                fontSize: 11,
                fontFamily: "var(--font-mono)",
                color: "var(--text-2)",
              }}
            >
              <span
                style={{
                  width: 12,
                  height: 12,
                  background: f.headline_drift_hex,
                  borderRadius: 2,
                  border: "1px solid var(--border)",
                }}
              />
              <span>top drift: {f.headline_drift_hex}</span>
            </div>
          )}
        </motion.div>
      </Link>
    </motion.div>
  );
}

function Stat({
  label,
  value,
  tone,
}: {
  label: string;
  value: string;
  tone: "good" | "neutral" | "warning";
}) {
  const color =
    tone === "good" ? "var(--accent)"
      : tone === "warning" ? "var(--danger)"
      : "var(--text-2)";
  return (
    <div style={{ display: "flex", flexDirection: "column" }}>
      <span style={{ fontSize: 18, fontWeight: 700, color }}>{value}</span>
      <span style={{ fontSize: 10, color: "var(--text-3)", textTransform: "uppercase", letterSpacing: "0.06em", fontWeight: 600 }}>
        {label}
      </span>
    </div>
  );
}

function timeAgo(isoStr: string): string {
  const t = new Date(isoStr);
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

function EmptyState() {
  return (
    <>
      {/* id="all-files" stamped here too so the sidebar's "All files" anchor
       *  resolves in the empty state. Without this, useActiveSection warns
       *  about a missing DOM target. */}
      <div id="all-files" />
      <Hero count={0} />
      <EmptyAuditState
        scope="site"
        preview={
          <div style={{ display: "flex", gap: 14, alignItems: "center" }}>
            {[
              { name: "Sample file A", coverage: "94%" },
              { name: "Sample file B", coverage: "82%" },
              { name: "Sample file C", coverage: "67%" },
            ].map((s, i) => (
              <motion.div
                key={s.name}
                initial={{ opacity: 0, y: 8 }}
                animate={{ opacity: 1, y: 0 }}
                transition={{ delay: 0.15 + i * 0.1, duration: 0.4 }}
                style={{
                  width: 120,
                  padding: 12,
                  background: "var(--bg-surface)",
                  border: "1px solid var(--border)",
                  borderRadius: 8,
                  fontSize: 11,
                  fontFamily: "var(--font-mono)",
                }}
              >
                <div style={{ color: "var(--text-2)", marginBottom: 4 }}>{s.name}</div>
                <div style={{ fontSize: 16, fontWeight: 700, color: "var(--accent)" }}>{s.coverage}</div>
              </motion.div>
            ))}
          </div>
        }
      />
    </>
  );
}
