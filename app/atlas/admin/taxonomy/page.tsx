"use client";

/**
 * Phase 7.5 / U3 — taxonomy curator.
 *
 * The DS-lead's authoritative tree of Product → folder paths. Designer
 * exports land in `projects` with arbitrary `projects.path` strings;
 * paths not yet in canonical_taxonomy show as "designer-extended" and
 * can be promoted to canonical or left informal.
 *
 * UI shape: a real hierarchical tree view, not a flat list. Each Product
 * is a top-level group; under it, the path segments split into a tree.
 * Per-row actions: Promote, Archive, Unarchive, Add child.
 *
 * Backend calls:
 *   GET  /v1/atlas/admin/taxonomy
 *   POST /v1/atlas/admin/taxonomy/promote        body {product, path}
 *   POST /v1/atlas/admin/taxonomy/archive        body {product, path}
 *
 * Drag-to-reorder is deferred — taxonomy ordering is alphabetical for
 * v1 (folders display sorted), and reorder requires an order_index
 * column that didn't ship in migration 0010.
 */

import { AnimatePresence, motion, Reorder } from "framer-motion";
import { useEffect, useMemo, useState } from "react";

import { AdminShell } from "../_lib/AdminShell";
import { adminFetchJSON } from "../_lib/adminFetch";

interface Entry {
  product: string;
  path: string;
  canonical: boolean;
  archived_at?: string;
  flow_count?: number;
  order_index: number;
}

/**
 * TreeNode is a frontend-only structure built from the flat Entry list.
 * Each node owns its segment label + the rebuilt full path so an action
 * fires against the correct row in canonical_taxonomy. orderIndex drives
 * the visual ordering within a sibling group; extended (non-canonical)
 * rows always sort to the bottom.
 */
interface TreeNode {
  product: string;
  /** Path from product root, "/"-joined. Empty for the product itself. */
  path: string;
  /** Last segment — the display label for this row. */
  label: string;
  canonical: boolean;
  archived: boolean;
  flowCount: number;
  orderIndex: number;
  children: Map<string, TreeNode>;
}

function buildTree(entries: Entry[]): Map<string, TreeNode> {
  // Top-level keyed by product. Each product's tree built by walking its
  // entries' path segments.
  const products = new Map<string, TreeNode>();

  // Ensure every product appears as a root, even if it only has extended
  // entries with no canonical row yet.
  for (const e of entries) {
    if (!products.has(e.product)) {
      products.set(e.product, {
        product: e.product,
        path: "",
        label: e.product,
        canonical: true, // products themselves are implicit roots
        archived: false,
        flowCount: 0,
        orderIndex: 0,
        children: new Map(),
      });
    }
  }

  for (const e of entries) {
    if (!e.path) continue; // product-root row
    const root = products.get(e.product)!;
    let cursor = root;
    const segments = e.path.split("/").filter(Boolean);
    for (let i = 0; i < segments.length; i++) {
      const seg = segments[i];
      const fullPath = segments.slice(0, i + 1).join("/");
      const isLeaf = i === segments.length - 1;
      let next = cursor.children.get(seg);
      if (!next) {
        next = {
          product: e.product,
          path: fullPath,
          label: seg,
          canonical: false,
          archived: false,
          flowCount: 0,
          orderIndex: 0,
          children: new Map(),
        };
        cursor.children.set(seg, next);
      }
      if (isLeaf) {
        if (e.canonical) next.canonical = true;
        if (e.archived_at) next.archived = true;
        if (e.flow_count) next.flowCount = e.flow_count;
        next.orderIndex = e.order_index;
      }
      cursor = next;
    }
  }
  return products;
}

/** Stable key used by Framer Reorder + by the sibling reorder save call. */
function nodeKey(n: TreeNode): string {
  return `${n.product}\x00${n.path}`;
}

/** Sort siblings: canonical first by order_index then label, extended after. */
function sortSiblings(nodes: TreeNode[]): TreeNode[] {
  return [...nodes].sort((a, b) => {
    if (a.canonical !== b.canonical) return a.canonical ? -1 : 1;
    if (a.canonical && b.canonical) {
      if (a.orderIndex !== b.orderIndex) return a.orderIndex - b.orderIndex;
    }
    return a.label.localeCompare(b.label);
  });
}

export default function AdminTaxonomyPage() {
  const [entries, setEntries] = useState<Entry[]>([]);
  const [status, setStatus] = useState<"loading" | "ready" | "error">("loading");
  const [error, setError] = useState<string | null>(null);
  const [actingKey, setActingKey] = useState<string | null>(null);
  const [expanded, setExpanded] = useState<Set<string>>(new Set());

  async function load() {
    setStatus("loading");
    try {
      const body = await adminFetchJSON<{ taxonomy: Entry[] }>("/v1/atlas/admin/taxonomy");
      setEntries(body.taxonomy ?? []);
      setStatus("ready");
      // Default-expand every product node.
      const nextExpanded = new Set<string>();
      for (const e of body.taxonomy ?? []) {
        nextExpanded.add(`${e.product}\x00`);
      }
      setExpanded(nextExpanded);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
      setStatus("error");
    }
  }

  useEffect(() => {
    void load();
  }, []);

  const tree = useMemo(() => buildTree(entries), [entries]);

  async function act(key: string, product: string, path: string, action: "promote" | "archive") {
    setActingKey(key);
    try {
      await adminFetchJSON(`/v1/atlas/admin/taxonomy/${action}`, {
        method: "POST",
        body: { product, path },
      });
      await load();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setActingKey(null);
    }
  }

  // Phase 7.6 — persist a sibling reorder. Only canonical rows are
  // included in the payload; extended rows can't be reordered (they have
  // no order_index row to update). Optimistic UI: we don't reload after,
  // the user-side state already reflects the new order.
  async function saveReorder(siblings: TreeNode[]) {
    const canonicalOnly = siblings.filter((s) => s.canonical && !s.archived);
    if (canonicalOnly.length === 0) return;
    try {
      await adminFetchJSON("/v1/atlas/admin/taxonomy/reorder", {
        method: "POST",
        body: {
          entries: canonicalOnly.map((s) => ({ product: s.product, path: s.path })),
        },
      });
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
      // Reload to revert local optimistic state.
      void load();
    }
  }

  function toggleExpand(key: string) {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
  }

  return (
    <AdminShell
      title="Taxonomy curator"
      description="The authoritative Product → folder tree. Promote a designer-extended path to make it canonical (it appears in the plugin's autocomplete + the mind-graph hierarchy edges). Archive a canonical path to retire it — flows under it stay readable, but it's marked stale."
    >
      {status === "loading" && <div className="msg">Loading taxonomy…</div>}
      {status === "error" && (
        <div className="msg err">
          Couldn&apos;t load: {error}.{" "}
          <button onClick={() => void load()}>Retry</button>
        </div>
      )}
      {status === "ready" && tree.size === 0 && (
        <div className="msg empty">
          <strong>No projects yet.</strong> The taxonomy populates as
          designers export flows from the Figma plugin.
        </div>
      )}
      {status === "ready" && tree.size > 0 && (
        <div className="tree" role="tree" aria-label="Product taxonomy">
          {Array.from(tree.values())
            .sort((a, b) => a.product.localeCompare(b.product))
            .map((root) => (
              <ProductBranch
                key={root.product}
                node={root}
                depth={0}
                expanded={expanded}
                onToggle={toggleExpand}
                actingKey={actingKey}
                onAction={act}
                onReorder={saveReorder}
              />
            ))}
        </div>
      )}
      <Legend />
      <style jsx>{`
        .msg {
          padding: 16px;
          color: var(--text-3);
        }
        .msg.empty {
          padding: 32px;
          text-align: center;
          background: var(--surface-1, rgba(255, 255, 255, 0.02));
          border: 1px dashed var(--border);
          border-radius: 12px;
        }
        .msg.empty strong {
          color: var(--text-1);
          display: block;
          margin-bottom: 4px;
        }
        .msg.err {
          color: #ffb347;
        }
        .msg.err button {
          margin-left: 8px;
          padding: 4px 10px;
          border: 1px solid var(--border);
          border-radius: 6px;
          background: transparent;
          color: inherit;
          cursor: pointer;
        }
        .tree {
          background: var(--surface-1, rgba(255, 255, 255, 0.02));
          border: 1px solid var(--border, rgba(255, 255, 255, 0.08));
          border-radius: 12px;
          padding: 8px 0;
        }
      `}</style>
    </AdminShell>
  );
}

interface BranchProps {
  node: TreeNode;
  depth: number;
  expanded: Set<string>;
  onToggle: (key: string) => void;
  actingKey: string | null;
  onAction: (key: string, product: string, path: string, action: "promote" | "archive") => void;
  /** Phase 7.6 — fires when this node's siblings have been reordered. */
  onReorder: (siblings: TreeNode[]) => void;
}

function ProductBranch({
  node,
  depth,
  expanded,
  onToggle,
  actingKey,
  onAction,
  onReorder,
}: BranchProps) {
  const key = `${node.product}\x00${node.path}`;
  const hasChildren = node.children.size > 0;
  const isExpanded = expanded.has(key);
  const isProductRoot = depth === 0;
  const acting = actingKey === key;

  // Visual state — canonical / extended / archived. Products are always
  // implicit canonical so we don't show a chip for them.
  let stateChip: { label: string; color: string } | null = null;
  if (!isProductRoot) {
    if (node.archived) {
      stateChip = { label: "Archived", color: "#888" };
    } else if (node.canonical) {
      stateChip = { label: "Canonical", color: "#1FD896" };
    } else {
      stateChip = { label: "Extended", color: "#FFB347" };
    }
  }

  return (
    <>
      <div
        className={`row ${isProductRoot ? "product-row" : "folder-row"} ${acting ? "acting" : ""}`}
        style={{ paddingLeft: 16 + depth * 20 }}
        role="treeitem"
        aria-expanded={hasChildren ? isExpanded : undefined}
      >
        {hasChildren ? (
          <button
            type="button"
            className="caret"
            aria-label={isExpanded ? "Collapse" : "Expand"}
            onClick={() => onToggle(key)}
          >
            <span className={isExpanded ? "rot" : ""}>▶</span>
          </button>
        ) : (
          <span className="caret-spacer" aria-hidden />
        )}

        <div className="label">
          <span className={isProductRoot ? "product-name" : "folder-name"}>
            {node.label}
          </span>
          {!isProductRoot && (
            <code className="full-path">{node.product}/{node.path}</code>
          )}
        </div>

        {!isProductRoot && node.flowCount > 0 && (
          <span className="flow-count" title={`${node.flowCount} flow(s) under this path`}>
            {node.flowCount} flow{node.flowCount === 1 ? "" : "s"}
          </span>
        )}

        {stateChip && (
          <span
            className="chip"
            style={{
              color: stateChip.color,
              background: stateChip.color + "22",
              borderColor: stateChip.color + "55",
            }}
          >
            {stateChip.label}
          </span>
        )}

        {!isProductRoot && (
          <div className="actions">
            {!node.canonical && !node.archived && (
              <button
                className="primary"
                disabled={acting}
                onClick={() => onAction(key, node.product, node.path, "promote")}
              >
                Promote
              </button>
            )}
            {node.canonical && !node.archived && (
              <button
                className="secondary"
                disabled={acting}
                onClick={() => onAction(key, node.product, node.path, "archive")}
              >
                Archive
              </button>
            )}
            {node.archived && (
              <button
                className="primary"
                disabled={acting}
                onClick={() => onAction(key, node.product, node.path, "promote")}
              >
                Unarchive
              </button>
            )}
          </div>
        )}
      </div>

      <AnimatePresence initial={false}>
        {hasChildren && isExpanded && (
          <ChildrenList
            siblings={sortSiblings(Array.from(node.children.values()))}
            depth={depth + 1}
            expanded={expanded}
            onToggle={onToggle}
            actingKey={actingKey}
            onAction={onAction}
            onReorder={onReorder}
          />
        )}
      </AnimatePresence>

      <style jsx>{`
        .row {
          display: flex;
          align-items: center;
          gap: 12px;
          padding-top: 6px;
          padding-bottom: 6px;
          padding-right: 16px;
          border-bottom: 1px solid var(--border, rgba(255, 255, 255, 0.04));
          font-size: 13px;
        }
        .row.acting {
          opacity: 0.5;
        }
        .row.product-row {
          background: var(--surface-2, rgba(123, 159, 255, 0.06));
          padding-top: 10px;
          padding-bottom: 10px;
          font-weight: 600;
        }
        .product-row .product-name {
          font-size: 14px;
          color: var(--text-1);
        }
        .folder-row .folder-name {
          color: var(--text-2);
        }
        .caret {
          width: 18px;
          height: 18px;
          display: inline-flex;
          align-items: center;
          justify-content: center;
          background: transparent;
          border: none;
          color: var(--text-3);
          font-size: 9px;
          cursor: pointer;
        }
        .caret span {
          display: inline-block;
          transition: transform 0.15s ease;
        }
        .caret span.rot {
          transform: rotate(90deg);
        }
        .caret-spacer {
          width: 18px;
        }
        .label {
          flex: 1;
          min-width: 0;
          display: flex;
          flex-direction: column;
          gap: 2px;
        }
        .full-path {
          font-family: var(--font-mono, ui-monospace, monospace);
          font-size: 10px;
          color: var(--text-3);
        }
        .flow-count {
          font-size: 11px;
          color: var(--text-3);
          font-variant-numeric: tabular-nums;
          padding: 2px 6px;
          border-radius: 4px;
          background: var(--surface-1, rgba(255, 255, 255, 0.04));
        }
        .chip {
          padding: 2px 8px;
          border-radius: 999px;
          font-size: 10px;
          font-weight: 600;
          letter-spacing: 0.04em;
          text-transform: uppercase;
          border: 1px solid;
        }
        .actions {
          display: flex;
          gap: 6px;
        }
        .actions button {
          padding: 4px 12px;
          border-radius: 999px;
          font-size: 11px;
          font-weight: 600;
          cursor: pointer;
        }
        .actions button:disabled {
          opacity: 0.5;
          cursor: not-allowed;
        }
        .actions .primary {
          background: var(--accent, #7b9fff);
          color: var(--bg);
          border: none;
        }
        .actions .secondary {
          background: transparent;
          color: var(--text-2);
          border: 1px solid var(--border);
        }
      `}</style>
    </>
  );
}

/**
 * ChildrenList wraps a sibling group in Framer's Reorder.Group so DS leads
 * can drag-to-reorder canonical folders. Extended (non-canonical) folders
 * still render in the list but their drag handle is disabled (you can't
 * reorder a row that has no order_index in canonical_taxonomy).
 *
 * onReorder fires the save call against the resulting sibling order; the
 * page-level handler filters to canonical-only entries before hitting the
 * backend.
 */
function ChildrenList({
  siblings,
  depth,
  expanded,
  onToggle,
  actingKey,
  onAction,
  onReorder,
}: {
  siblings: TreeNode[];
  depth: number;
  expanded: Set<string>;
  onToggle: (key: string) => void;
  actingKey: string | null;
  onAction: (key: string, product: string, path: string, action: "promote" | "archive") => void;
  onReorder: (siblings: TreeNode[]) => void;
}) {
  const [order, setOrder] = useState<TreeNode[]>(siblings);

  // Keep local order in sync when the parent's data refreshes.
  useEffect(() => {
    setOrder(siblings);
  }, [siblings]);

  return (
    <motion.div
      initial={{ height: 0, opacity: 0 }}
      animate={{ height: "auto", opacity: 1 }}
      exit={{ height: 0, opacity: 0 }}
      transition={{ duration: 0.2, ease: [0.22, 1, 0.36, 1] }}
      style={{ overflow: "hidden" }}
      role="group"
    >
      <Reorder.Group
        axis="y"
        values={order}
        onReorder={(next) => {
          setOrder(next);
          onReorder(next);
        }}
        as="div"
      >
        {order.map((child) => (
          <Reorder.Item
            key={nodeKey(child)}
            value={child}
            as="div"
            // Extended rows can't be reordered — drag is disabled.
            drag={child.canonical && !child.archived ? "y" : false}
            dragListener={child.canonical && !child.archived}
          >
            <ProductBranch
              node={child}
              depth={depth}
              expanded={expanded}
              onToggle={onToggle}
              actingKey={actingKey}
              onAction={onAction}
              onReorder={onReorder}
            />
          </Reorder.Item>
        ))}
      </Reorder.Group>
    </motion.div>
  );
}

function Legend() {
  return (
    <div className="legend">
      <span>
        <em style={{ color: "#1FD896" }}>●</em> Canonical — in the authoritative
        tree; appears in the plugin&apos;s autocomplete.
      </span>
      <span>
        <em style={{ color: "#FFB347" }}>●</em> Extended — designers have used
        this path; promote it to make it canonical.
      </span>
      <span>
        <em style={{ color: "#888" }}>●</em> Archived — retired but kept for
        audit. Existing flows under it stay readable.
      </span>
      <style jsx>{`
        .legend {
          margin-top: 16px;
          padding: 16px;
          font-size: 11px;
          color: var(--text-3);
          display: flex;
          flex-direction: column;
          gap: 6px;
        }
        em {
          font-style: normal;
          margin-right: 6px;
        }
      `}</style>
    </div>
  );
}
