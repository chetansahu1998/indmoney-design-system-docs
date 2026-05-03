"use client";

/**
 * Phase 7.5 / U7 — saved-view share link.
 *
 * Reads the current useGraphView state (focused node, active filters,
 * platform) + builds a query-string URL that hydrates the same view on
 * paste. Click → copy to clipboard, show a 2s "Copied" toast.
 *
 * No persistence in v1 — saved views are URL-only. Future polish: a
 * server-side `saved_views` table the user can list/name views.
 *
 * Hydration: `app/atlas/page.tsx` already reads `?platform=` and
 * `?focus=`; this file extends the URL with `?filters=` (comma-joined).
 * BrainGraph + useGraphView need to wire `?filters=` parsing for full
 * round-trip — that's a one-liner in page.tsx but kept out of this file
 * to avoid touching the route entry until we know the format.
 */

import { motion, AnimatePresence } from "framer-motion";
import { useState } from "react";

import type { GraphFilters, GraphPlatform } from "./types";

interface Props {
  platform: GraphPlatform;
  focusedNodeID: string | null;
  filters: GraphFilters;
}

export function SavedViewShareButton({ platform, focusedNodeID, filters }: Props) {
  const [copied, setCopied] = useState(false);

  function buildShareURL(): string {
    if (typeof window === "undefined") return "";
    const url = new URL(window.location.href);
    url.pathname = "/atlas";
    url.searchParams.set("platform", platform);
    if (focusedNodeID) url.searchParams.set("focus", focusedNodeID);
    else url.searchParams.delete("focus");
    const onChips: string[] = [];
    if (filters.components) onChips.push("components");
    if (filters.tokens) onChips.push("tokens");
    if (filters.decisions) onChips.push("decisions");
    if (onChips.length > 0) url.searchParams.set("filters", onChips.join(","));
    else url.searchParams.delete("filters");
    return url.toString();
  }

  async function copy() {
    const u = buildShareURL();
    if (!u) return;
    try {
      await navigator.clipboard.writeText(u);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 2000);
    } catch {
      // Clipboard API can fail in non-HTTPS or sandboxed contexts; fall
      // back to a transient prompt the user can copy manually.
      window.prompt("Copy this URL:", u);
    }
  }

  return (
    <>
      <button
        type="button"
        onClick={() => void copy()}
        className="share-btn"
        aria-label="Copy share link for current view"
        title="Copy share link"
      >
        <svg
          aria-hidden
          width="14"
          height="14"
          viewBox="0 0 16 16"
          fill="none"
          stroke="currentColor"
          strokeWidth="1.5"
          strokeLinecap="round"
          strokeLinejoin="round"
        >
          <path d="M11 3.5h2.5V6" />
          <path d="M13.5 3.5l-5 5" />
          <path d="M11 9.5v3a1 1 0 01-1 1H4a1 1 0 01-1-1V6a1 1 0 011-1h3" />
        </svg>
        Share view
      </button>
      <AnimatePresence>
        {copied && (
          <motion.div
            className="toast"
            initial={{ y: 8, opacity: 0 }}
            animate={{ y: 0, opacity: 1 }}
            exit={{ opacity: 0 }}
            transition={{ duration: 0.15 }}
          >
            Link copied to clipboard
          </motion.div>
        )}
      </AnimatePresence>
      <style jsx>{`
        .share-btn {
          position: fixed;
          bottom: 24px;
          right: 24px;
          display: inline-flex;
          align-items: center;
          gap: 6px;
          padding: 8px 14px;
          border-radius: 999px;
          background: var(--bg-overlay);
          border: 1px solid var(--border);
          backdrop-filter: blur(12px);
          color: var(--text-1);
          font-family: var(--font-sans, "Inter Variable", sans-serif);
          font-size: 12px;
          font-weight: 500;
          cursor: pointer;
          z-index: 10;
        }
        .share-btn:hover {
          background: var(--accent-soft);
          color: var(--accent);
        }
        .share-btn:focus-visible {
          outline: 2px solid var(--accent);
          outline-offset: 2px;
        }
        .toast {
          position: fixed;
          bottom: 80px;
          right: 24px;
          padding: 8px 16px;
          background: var(--success);
          color: var(--bg-canvas);
          border-radius: 8px;
          font-family: var(--font-sans, "Inter Variable", sans-serif);
          font-size: 12px;
          font-weight: 600;
          z-index: 11;
          pointer-events: none;
        }
      `}</style>
    </>
  );
}
