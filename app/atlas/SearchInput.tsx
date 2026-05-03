"use client";

/**
 * Phase 8 U11 — in-graph search input.
 *
 * Top-left of the canvas. Slides down on focus; queries
 * /v1/search?scope=mind-graph; matching node IDs are passed up to
 * BrainGraph which dims non-matching nodes to opacity 0.3.
 *
 * The query state lives here; the matching-id set + clear handler are
 * passed to the parent via callbacks. Reduced-motion: no slide; immediate.
 */

import { motion } from "framer-motion";
import { useEffect, useRef, useState } from "react";

import { useSearch } from "@/components/search/useSearch";

interface Props {
  /** When non-empty, BrainGraph dims non-matching nodes. Empty = full graph. */
  onMatchChange: (nodeIDs: Set<string> | null) => void;
  reducedMotion: boolean;
}

export function SearchInput({ onMatchChange, reducedMotion }: Props) {
  const [query, setQuery] = useState("");
  const [focused, setFocused] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);

  const { results, status } = useSearch(query, { scope: "mind-graph", limit: 50 });

  // Push the matching-set up whenever results change.
  useEffect(() => {
    if (query.trim() === "") {
      onMatchChange(null);
      return;
    }
    if (status === "ready") {
      const set = new Set<string>();
      for (const r of results) {
        // The node IDs in graph_index are typed: "<kind>:<id>". The search
        // result's (kind, id) maps directly. flow:flow_abc → node id
        // "flow:flow_abc". Same for component / decision / persona.
        set.add(`${r.kind}:${r.id}`);
      }
      onMatchChange(set);
    }
  }, [results, status, query, onMatchChange]);

  // Esc clears + blurs.
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") {
        setQuery("");
        inputRef.current?.blur();
      }
      // ⌘F-style focus: "/" while not typing, mirrors GitHub
      if (e.key === "/" && document.activeElement === document.body) {
        e.preventDefault();
        inputRef.current?.focus();
      }
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  return (
    <motion.div
      className="search-input"
      initial={false}
      animate={
        reducedMotion
          ? { opacity: 1, y: 0 }
          : focused || query !== ""
            ? { opacity: 1, y: 0 }
            : { opacity: 0.6, y: -2 }
      }
      transition={{ duration: 0.18 }}
    >
      <input
        ref={inputRef}
        type="search"
        placeholder="Search the graph…  /"
        value={query}
        onChange={(e) => setQuery(e.target.value)}
        onFocus={() => setFocused(true)}
        onBlur={() => {
          setFocused(false);
          // Defensive: if the user emptied the input via backspace and then
          // blurred, force-clear the dim state. The keyed effect already
          // does this when `query` flips to "" but if useSearch is mid-
          // debounce the dim can persist. This guarantees blur with empty
          // query always lifts the dim.
          if (query.trim() === "") onMatchChange(null);
        }}
        aria-label="Search the mind graph"
      />
      {query !== "" && status === "ready" && (
        <span className="count" aria-live="polite">
          {results.length} match{results.length === 1 ? "" : "es"}
        </span>
      )}
      <style jsx>{`
        .search-input {
          position: fixed;
          top: 24px;
          left: 24px;
          display: flex;
          align-items: center;
          gap: 8px;
          padding: 4px 12px;
          background: var(--bg-overlay);
          border: 1px solid var(--border-subtle);
          border-radius: 999px;
          backdrop-filter: blur(12px);
          z-index: 10;
          font-family: var(--font-sans, "Inter Variable", sans-serif);
        }
        .search-input input {
          width: 240px;
          padding: 6px 4px;
          background: transparent;
          border: none;
          color: var(--text-1);
          font-size: 12px;
          letter-spacing: 0.01em;
        }
        .search-input input::placeholder {
          color: var(--text-3);
        }
        .search-input input:focus-visible {
          outline: none;
        }
        .search-input input:focus + .count,
        .search-input input:focus {
          /* keep the box width stable when count appears */
        }
        .count {
          font-size: 10px;
          color: var(--text-3);
          font-variant-numeric: tabular-nums;
        }
      `}</style>
    </motion.div>
  );
}
