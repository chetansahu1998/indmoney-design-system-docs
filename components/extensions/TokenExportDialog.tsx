"use client";
import { useEffect, useMemo, useState } from "react";
import { motion, AnimatePresence } from "framer-motion";
import { useUIStore } from "@/lib/ui-store";
import { brandLabel, currentBrand } from "@/lib/brand";
import {
  exportFormats,
  formatLabel,
  formatDescription,
  exportTokens,
  downloadTokens,
  type ExportFormat,
} from "@/lib/tokens/exporters";

/**
 * Download tokens dialog.
 *
 * Opens via the Header "Download" button (Zustand-driven). User picks a format,
 * sees a live preview of the first ~600 chars, and clicks Download — browser
 * triggers a file save.
 *
 * Real-designer win Field DS doesn't have. The TokenExportDialog from
 * DesignBrain-AI inspired the UI; we ported the concept and re-implemented
 * with INDmoney's actual extracted tokens.
 */
export default function TokenExportDialog() {
  const open = useUIStore((s) => s.exportOpen);
  const setOpen = useUIStore((s) => s.setExportOpen);
  const [format, setFormat] = useState<ExportFormat>("css");

  const formats = useMemo(() => exportFormats(), []);
  const brand = currentBrand();

  const preview = useMemo(() => {
    if (!open) return "";
    return exportTokens(format, brand).content.slice(0, 1400);
  }, [open, format, brand]);

  // Esc close
  useEffect(() => {
    if (!open) return;
    const handler = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    window.addEventListener("keydown", handler);
    return () => window.removeEventListener("keydown", handler);
  }, [open, setOpen]);

  return (
    <AnimatePresence>
      {open && (
        <motion.div
          key="overlay"
          initial={{ opacity: 0 }}
          animate={{ opacity: 1 }}
          exit={{ opacity: 0 }}
          transition={{ duration: 0.18 }}
          onClick={() => setOpen(false)}
          style={{
            position: "fixed",
            inset: 0,
            zIndex: 200,
            background: "var(--scrim)",
            display: "flex",
            alignItems: "flex-start",
            justifyContent: "center",
            paddingTop: 80,
            backdropFilter: "blur(4px)",
          }}
        >
          <motion.div
            key="panel"
            initial={{ y: -16, opacity: 0, scale: 0.97 }}
            animate={{ y: 0, opacity: 1, scale: 1 }}
            exit={{ y: -16, opacity: 0, scale: 0.97 }}
            transition={{ type: "spring", stiffness: 320, damping: 28 }}
            onClick={(e) => e.stopPropagation()}
            style={{
              width: "min(720px, 92vw)",
              background: "var(--bg-surface)",
              border: "1px solid var(--border-strong)",
              borderRadius: 14,
              overflow: "hidden",
              boxShadow:
                "var(--elev-shadow-3), 0 0 0 1px var(--border)",
              display: "flex",
              flexDirection: "column",
              maxHeight: "82vh",
            }}
          >
            {/* Header */}
            <div
              style={{
                padding: "16px 20px",
                borderBottom: "1px solid var(--border)",
                display: "flex",
                alignItems: "center",
                justifyContent: "space-between",
              }}
            >
              <div>
                <div style={{ fontSize: 15, fontWeight: 600, color: "var(--text-1)" }}>
                  Download {brandLabel(brand)} tokens
                </div>
                <div style={{ fontSize: 12, color: "var(--text-3)", marginTop: 2 }}>
                  Pick a format · the preview shows the first ~1400 characters
                </div>
              </div>
              <button
                onClick={() => setOpen(false)}
                style={{
                  background: "var(--bg-surface-2)",
                  border: "1px solid var(--border)",
                  color: "var(--text-2)",
                  borderRadius: 6,
                  padding: "4px 8px",
                  fontFamily: "var(--font-mono)",
                  fontSize: 11,
                  cursor: "pointer",
                }}
              >
                esc
              </button>
            </div>

            {/* Format picker */}
            <div
              style={{
                display: "flex",
                gap: 6,
                padding: "12px 20px",
                borderBottom: "1px solid var(--border)",
                overflowX: "auto",
              }}
            >
              {formats.map((f) => (
                <button
                  key={f}
                  onClick={() => setFormat(f)}
                  style={{
                    padding: "6px 12px",
                    background: format === f ? "var(--accent)" : "var(--bg-surface-2)",
                    color: format === f ? "#fff" : "var(--text-2)",
                    border: "1px solid",
                    borderColor: format === f ? "var(--accent)" : "var(--border)",
                    borderRadius: 6,
                    fontSize: 12,
                    fontWeight: 500,
                    cursor: "pointer",
                    whiteSpace: "nowrap",
                  }}
                >
                  {formatLabel(f)}
                </button>
              ))}
            </div>

            {/* Description */}
            <div
              style={{
                padding: "10px 20px",
                fontSize: 12,
                color: "var(--text-3)",
                borderBottom: "1px solid var(--border)",
                lineHeight: 1.5,
              }}
            >
              {formatDescription(format)}
            </div>

            {/* Preview */}
            <div
              style={{
                flex: 1,
                minHeight: 0,
                overflow: "auto",
                padding: 16,
                background: "var(--bg-page)",
              }}
            >
              <pre
                style={{
                  margin: 0,
                  fontSize: 11,
                  fontFamily: "var(--font-mono)",
                  color: "var(--text-2)",
                  lineHeight: 1.55,
                  whiteSpace: "pre",
                  overflow: "auto",
                }}
              >
                {preview}
              </pre>
            </div>

            {/* Actions */}
            <div
              style={{
                padding: "14px 20px",
                borderTop: "1px solid var(--border)",
                display: "flex",
                gap: 10,
                justifyContent: "flex-end",
              }}
            >
              <button
                onClick={() => {
                  navigator.clipboard
                    .writeText(exportTokens(format, brand).content)
                    .catch(() => {});
                }}
                style={{
                  padding: "9px 14px",
                  background: "var(--bg-surface-2)",
                  border: "1px solid var(--border)",
                  color: "var(--text-1)",
                  borderRadius: 7,
                  fontSize: 13,
                  fontWeight: 500,
                  cursor: "pointer",
                }}
              >
                Copy to clipboard
              </button>
              <button
                onClick={() => downloadTokens(format, brand)}
                style={{
                  padding: "9px 16px",
                  background: "var(--accent)",
                  border: "1px solid var(--accent)",
                  color: "#fff",
                  borderRadius: 7,
                  fontSize: 13,
                  fontWeight: 600,
                  cursor: "pointer",
                }}
              >
                Download {format.toUpperCase()}
              </button>
            </div>
          </motion.div>
        </motion.div>
      )}
    </AnimatePresence>
  );
}
