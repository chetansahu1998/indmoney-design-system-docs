"use client";

import { create } from "zustand";
import { AnimatePresence, motion } from "framer-motion";
import { useEffect } from "react";

/**
 * Tiny global toast primitive. Replaces the inconsistent ad-hoc copy
 * feedback that previously lived inside Color/Effects/Icons (overlay
 * boxes that disappeared on light theme, COPIED chips that varied in
 * size, etc.). One channel, one visual treatment, theme-aware.
 *
 * Usage:
 *   import { showToast } from "@/components/ui/Toast";
 *   showToast({ message: "Copied surface.text-1", tone: "success" });
 *
 * Mount <ToastHost /> once in the root layout body (already wired in
 * RootLayout). Multiple toasts stack; each auto-dismisses after 1800ms.
 */

export type ToastTone = "info" | "success" | "warning" | "danger";

interface ToastEntry {
  id: number;
  message: string;
  tone: ToastTone;
  /** Optional secondary line, shown smaller below the main message. */
  detail?: string;
}

interface ToastStore {
  queue: ToastEntry[];
  push: (t: Omit<ToastEntry, "id">) => void;
  dismiss: (id: number) => void;
}

let nextId = 1;

const useToastStore = create<ToastStore>((set) => ({
  queue: [],
  push: (t) => {
    const id = nextId++;
    set((s) => ({ queue: [...s.queue, { id, ...t }] }));
    if (typeof window !== "undefined") {
      window.setTimeout(() => {
        set((s) => ({ queue: s.queue.filter((q) => q.id !== id) }));
      }, 1800);
    }
  },
  dismiss: (id) => set((s) => ({ queue: s.queue.filter((q) => q.id !== id) })),
}));

export function showToast(t: Omit<ToastEntry, "id">) {
  useToastStore.getState().push(t);
}

const TONE_BG: Record<ToastTone, string> = {
  info: "var(--info)",
  success: "var(--success)",
  warning: "var(--warning)",
  danger: "var(--danger)",
};

export default function ToastHost() {
  const queue = useToastStore((s) => s.queue);
  const dismiss = useToastStore((s) => s.dismiss);

  // Reduced motion: skip the spring entrance for a straight fade.
  useEffect(() => {
    // No-op — placeholder for future per-mount behavior.
  }, []);

  return (
    <div
      aria-live="polite"
      aria-atomic="true"
      style={{
        position: "fixed",
        bottom: 24,
        right: 24,
        zIndex: 200,
        display: "flex",
        flexDirection: "column",
        gap: 8,
        pointerEvents: "none",
      }}
    >
      <AnimatePresence initial={false}>
        {queue.map((t) => (
          <motion.div
            key={t.id}
            layout
            initial={{ opacity: 0, y: 16, scale: 0.96 }}
            animate={{ opacity: 1, y: 0, scale: 1 }}
            exit={{ opacity: 0, y: 8, scale: 0.96 }}
            transition={{ type: "spring", stiffness: 380, damping: 28 }}
            onClick={() => dismiss(t.id)}
            role="status"
            style={{
              display: "flex",
              gap: 10,
              alignItems: "flex-start",
              padding: "10px 14px 10px 12px",
              minWidth: 220,
              maxWidth: 340,
              background: "var(--bg-surface)",
              border: "1px solid var(--border)",
              borderRadius: 10,
              boxShadow: "0 12px 32px rgba(0, 0, 0, 0.18)",
              pointerEvents: "auto",
              cursor: "pointer",
            }}
          >
            <span
              aria-hidden
              style={{
                marginTop: 6,
                width: 6,
                height: 6,
                borderRadius: 999,
                background: TONE_BG[t.tone],
                flexShrink: 0,
                boxShadow: `0 0 0 3px color-mix(in srgb, ${TONE_BG[t.tone]} 22%, transparent)`,
              }}
            />
            <div style={{ minWidth: 0 }}>
              <div
                style={{
                  fontSize: 13,
                  fontWeight: 600,
                  color: "var(--text-1)",
                  lineHeight: 1.3,
                }}
              >
                {t.message}
              </div>
              {t.detail && (
                <div
                  style={{
                    marginTop: 2,
                    fontSize: 11,
                    fontFamily: "var(--font-mono)",
                    color: "var(--text-3)",
                    lineHeight: 1.4,
                  }}
                >
                  {t.detail}
                </div>
              )}
            </div>
          </motion.div>
        ))}
      </AnimatePresence>
    </div>
  );
}
