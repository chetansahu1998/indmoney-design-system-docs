"use client";
import { motion } from "framer-motion";
import type { ReactNode } from "react";

/**
 * DataGapPreview — used when a section has no real data yet. Shows a clear
 * diagnostic, an animated preview of what the section will look like when
 * powered, and the exact command to unlock the data.
 */
export default function DataGapPreview({
  diagnosis,
  unlock,
  command,
  preview,
}: {
  diagnosis: ReactNode;     // What the extractor found / didn't find
  unlock: ReactNode;        // What needs to change to unlock real data
  command?: string;         // CLI command to (re-)run the pipeline
  preview: ReactNode;       // Animated preview rendered inside a "demo" frame
}) {
  return (
    <motion.div
      initial={{ opacity: 0, y: 8 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.35, ease: [0.33, 1, 0.68, 1] }}
      style={{
        position: "relative",
        background: "var(--bg-surface)",
        border: "1px solid var(--border)",
        borderRadius: 12,
        overflow: "hidden",
      }}
    >
      {/* Banner — diagnostic */}
      <div
        style={{
          padding: "12px 16px",
          background: "var(--bg-surface-2)",
          borderBottom: "1px solid var(--border)",
          display: "flex",
          gap: 10,
          alignItems: "flex-start",
        }}
      >
        <span
          aria-hidden
          style={{
            display: "inline-flex",
            alignItems: "center",
            justifyContent: "center",
            width: 20,
            height: 20,
            borderRadius: 4,
            background: "color-mix(in srgb, var(--accent) 20%, transparent)",
            color: "var(--accent)",
            fontSize: 12,
            fontWeight: 700,
            flexShrink: 0,
            marginTop: 1,
          }}
        >
          i
        </span>
        <div style={{ minWidth: 0, fontSize: 13, color: "var(--text-2)", lineHeight: 1.5 }}>
          <div style={{ color: "var(--text-1)", fontWeight: 600, marginBottom: 2 }}>
            Data gap — preview only
          </div>
          {diagnosis}
        </div>
      </div>

      {/* Preview area */}
      <div
        style={{
          padding: "32px 24px",
          background:
            "repeating-linear-gradient(135deg, transparent 0 8px, color-mix(in srgb, var(--text-3) 6%, transparent) 8px 10px)",
          minHeight: 180,
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          position: "relative",
        }}
      >
        <span
          style={{
            position: "absolute",
            top: 8,
            left: 12,
            fontSize: 9,
            fontFamily: "var(--font-mono)",
            color: "var(--text-3)",
            textTransform: "uppercase",
            letterSpacing: "0.08em",
          }}
        >
          Sample preview
        </span>
        {preview}
      </div>

      {/* Unlock command */}
      <div style={{ padding: "16px 16px 18px" }}>
        <div
          style={{
            fontSize: 11,
            fontWeight: 600,
            color: "var(--text-3)",
            textTransform: "uppercase",
            letterSpacing: "0.08em",
            marginBottom: 6,
          }}
        >
          To populate with real data
        </div>
        <div style={{ fontSize: 13, color: "var(--text-2)", lineHeight: 1.55, marginBottom: command ? 10 : 0 }}>
          {unlock}
        </div>
        {command && (
          <pre
            style={{
              margin: 0,
              padding: 10,
              background: "var(--bg-surface-2)",
              borderRadius: 6,
              fontFamily: "var(--font-mono)",
              fontSize: 12,
              color: "var(--text-1)",
              overflowX: "auto",
              border: "1px solid var(--border)",
            }}
          >
            {command}
          </pre>
        )}
      </div>
    </motion.div>
  );
}
