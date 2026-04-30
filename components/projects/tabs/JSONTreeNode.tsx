"use client";

/**
 * JSONTreeNode — recursive node renderer for the U8 JSON tab.
 *
 * Default-collapsed at depth ≥2 (per Phase 1 plan H1: avoid 1-2s freeze on
 * 1000-node trees). Click a row to expand/collapse. Bound-variable bindings
 * render with a "🎯 bound" chip + the resolved hex/value from the active mode.
 */

import { useMemo, useState, type CSSProperties } from "react";
import {
  extractBoundVariables,
  type ModeResolver,
  type ResolvedValue,
} from "@/lib/projects/resolveTreeForMode";

const DEFAULT_COLLAPSE_DEPTH = 2;

interface NodeProps {
  node: unknown;
  /** Key/index from parent — used to label the row. */
  label: string;
  depth: number;
  resolver: ModeResolver | null;
  /** When true, the search filter forces this branch open. */
  forceOpen: boolean;
  /** Lower-cased substring to highlight; empty string disables filtering. */
  filter: string;
}

export default function JSONTreeNode(props: NodeProps) {
  const { node, label, depth, resolver, forceOpen, filter } = props;
  const [openOverride, setOpenOverride] = useState<boolean | null>(null);

  const defaultOpen = depth < DEFAULT_COLLAPSE_DEPTH;
  const isOpen = openOverride ?? (forceOpen || defaultOpen);

  // Match check — `filter` is already lower-cased. We match on the label and
  // any string-typed scalar within the node's top-level fields. Children
  // recurse and force-open through their own forceOpen prop derivation.
  const matched = useMemo(() => {
    if (!filter) return false;
    if (label.toLowerCase().includes(filter)) return true;
    if (typeof node === "string" && node.toLowerCase().includes(filter)) return true;
    if (node && typeof node === "object") {
      const t = (node as Record<string, unknown>).type;
      const n = (node as Record<string, unknown>).name;
      if (typeof t === "string" && t.toLowerCase().includes(filter)) return true;
      if (typeof n === "string" && n.toLowerCase().includes(filter)) return true;
    }
    return false;
  }, [filter, label, node]);

  const childForceOpen = forceOpen || (filter !== "" && matched);

  // Scalar leaf rendering.
  if (node === null || node === undefined) {
    return <Row depth={depth} label={label} value={<Atom>{String(node)}</Atom>} matched={matched} />;
  }
  if (typeof node !== "object") {
    return (
      <Row
        depth={depth}
        label={label}
        value={<Atom>{typeof node === "string" ? `"${node}"` : String(node)}</Atom>}
        matched={matched}
      />
    );
  }

  // Object / array node.
  const isArray = Array.isArray(node);
  const entries = isArray
    ? (node as unknown[]).map((v, i) => [String(i), v] as const)
    : Object.entries(node as Record<string, unknown>);
  const count = entries.length;

  const bindings = extractBoundVariables(node);
  const summary = nodeSummary(node);

  return (
    <div
      style={{
        marginLeft: depth === 0 ? 0 : 12,
        background: matched ? "color-mix(in srgb, var(--accent) 8%, transparent)" : undefined,
        borderRadius: 4,
      }}
    >
      <button
        onClick={() => setOpenOverride(!isOpen)}
        style={{
          all: "unset",
          cursor: "pointer",
          display: "flex",
          alignItems: "center",
          gap: 6,
          padding: "2px 4px",
          width: "100%",
        }}
      >
        <span
          style={{
            display: "inline-block",
            width: 12,
            color: "var(--text-3)",
            fontFamily: "var(--font-mono)",
            fontSize: 11,
          }}
        >
          {isOpen ? "▾" : "▸"}
        </span>
        <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--text-2)" }}>
          {label}
        </span>
        <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--text-3)" }}>
          {summary || (isArray ? `[${count}]` : `{${count}}`)}
        </span>
        {bindings && bindings.length > 0 && (
          <BoundChips bindings={bindings} resolver={resolver} />
        )}
      </button>
      {isOpen &&
        entries.map(([k, v]) => (
          <JSONTreeNode
            key={k}
            node={v}
            label={k}
            depth={depth + 1}
            resolver={resolver}
            forceOpen={childForceOpen}
            filter={filter}
          />
        ))}
    </div>
  );
}

function Row(props: { depth: number; label: string; value: React.ReactNode; matched: boolean }) {
  const { depth, label, value, matched } = props;
  return (
    <div
      style={{
        marginLeft: depth === 0 ? 0 : 12,
        padding: "2px 4px",
        display: "flex",
        gap: 8,
        background: matched ? "color-mix(in srgb, var(--accent) 8%, transparent)" : undefined,
        borderRadius: 4,
      }}
    >
      <span style={{ width: 12 }} />
      <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--text-2)" }}>
        {label}:
      </span>
      {value}
    </div>
  );
}

function Atom({ children }: { children: React.ReactNode }) {
  return (
    <span style={{ fontFamily: "var(--font-mono)", fontSize: 11, color: "var(--text-1)" }}>
      {children}
    </span>
  );
}

function BoundChips(props: {
  bindings: ReturnType<typeof extractBoundVariables>;
  resolver: ModeResolver | null;
}) {
  const { bindings, resolver } = props;
  if (!bindings) return null;
  return (
    <span style={{ display: "inline-flex", gap: 4, marginLeft: 4 }}>
      {bindings.map((b) => {
        const resolved = resolver?.resolve(b.binding) ?? null;
        return (
          <span
            key={b.field + b.binding.id}
            title={`${b.field} → ${b.binding.id}${resolved ? ` (${describeResolved(resolved)})` : " (not in active mode)"}`}
            style={chipStyle(resolved)}
          >
            🎯 {b.field}
            {resolved && resolved.kind === "color" && (
              <span
                aria-hidden
                style={{
                  display: "inline-block",
                  width: 8,
                  height: 8,
                  borderRadius: 2,
                  marginLeft: 4,
                  background: resolved.hex,
                  verticalAlign: "middle",
                  border: "1px solid var(--border)",
                }}
              />
            )}
          </span>
        );
      })}
    </span>
  );
}

function chipStyle(resolved: ResolvedValue | null): CSSProperties {
  return {
    display: "inline-flex",
    alignItems: "center",
    gap: 2,
    padding: "1px 6px",
    borderRadius: 999,
    fontSize: 10,
    fontFamily: "var(--font-mono)",
    background: resolved
      ? "color-mix(in srgb, var(--accent) 14%, transparent)"
      : "color-mix(in srgb, var(--border) 50%, transparent)",
    color: resolved ? "var(--accent)" : "var(--text-3)",
  };
}

function describeResolved(r: ResolvedValue): string {
  switch (r.kind) {
    case "color":
      return r.hex;
    case "number":
      return String(r.value);
    case "string":
      return `"${r.value}"`;
    default:
      return "unknown";
  }
}

/**
 * nodeSummary returns a one-line preview shown next to a collapsed node so
 * the user can scan the tree without expanding everything. Picks the most
 * informative field (type, name, characters for TEXT nodes).
 */
function nodeSummary(node: unknown): string | null {
  if (!node || typeof node !== "object") return null;
  const o = node as Record<string, unknown>;
  const parts: string[] = [];
  if (typeof o.type === "string") parts.push(o.type);
  if (typeof o.name === "string") parts.push(`"${o.name}"`);
  if (typeof o.characters === "string" && o.characters.length < 40)
    parts.push(`"${o.characters}"`);
  if (parts.length === 0) return null;
  return parts.join(" · ");
}
