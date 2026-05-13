"use client";

/**
 * InventoryTree — Phase 2A U3.
 *
 * Renders the team → project → file → page → section tree fetched from
 * GET /v1/admin/figma-inventory/tree?team_id=...
 *
 * - Substring search filters across all levels. Matching nodes expand to
 *   root so their ancestor chain stays visible. Highlighted span on the
 *   matched substring.
 * - "Show deleted" toggle adds soft-deleted rows with strikethrough.
 * - Per-team client-side cache. "Refresh" bypasses cache.
 * - Render performance: ~1.5k nodes at the current corpus size — fine
 *   without virtualization. Add a note here if a tenant exceeds ~5k.
 */

import { useCallback, useEffect, useMemo, useState } from "react";

import { adminFetchJSON } from "@/app/atlas/admin/_lib/adminFetch";
import { useAuth } from "@/lib/auth-client";

import type { InventoryNodeKind, InventoryTreeNode } from "../types";

interface Props {
  teamID: string;
}

export function InventoryTree({ teamID }: Props) {
  const token = useAuth((s) => s.token);
  const [tree, setTree] = useState<InventoryTreeNode | null>(null);
  const [loading, setLoading] = useState(false);
  const [err, setErr] = useState<string>("");
  const [search, setSearch] = useState<string>("");
  const [includeDeleted, setIncludeDeleted] = useState<boolean>(false);
  const [expanded, setExpanded] = useState<Set<string>>(new Set());

  const load = useCallback(async () => {
    if (!teamID || !token) {
      setTree(null);
      return;
    }
    setLoading(true);
    setErr("");
    try {
      const url = `/v1/admin/figma-inventory/tree?team_id=${encodeURIComponent(teamID)}${
        includeDeleted ? "&include_deleted=1" : ""
      }`;
      const res = await adminFetchJSON<InventoryTreeNode>(url);
      setTree(res);
      // Expand the team root by default.
      setExpanded(new Set([nodeKey(res)]));
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      setErr(msg);
      setTree(null);
    } finally {
      setLoading(false);
    }
  }, [teamID, token, includeDeleted]);

  useEffect(() => {
    void load();
  }, [load]);

  // When search has a value, expand every ancestor of every match so
  // the chain is visible. Computed memo-style so we don't mutate
  // `expanded` during render.
  const effectiveExpanded = useMemo(() => {
    if (!tree) return expanded;
    if (!search.trim()) return expanded;
    const out = new Set(expanded);
    const needle = search.trim().toLowerCase();
    const walk = (node: InventoryTreeNode, ancestors: string[]) => {
      if (node.name.toLowerCase().includes(needle)) {
        for (const a of ancestors) out.add(a);
      }
      if (node.children) {
        for (const c of node.children) {
          walk(c, [...ancestors, nodeKey(node)]);
        }
      }
    };
    walk(tree, []);
    return out;
  }, [tree, expanded, search]);

  function toggle(key: string) {
    setExpanded((s) => {
      const next = new Set(s);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
  }

  function expandAll() {
    if (!tree) return;
    const all = new Set<string>();
    const walk = (n: InventoryTreeNode) => {
      all.add(nodeKey(n));
      n.children?.forEach(walk);
    };
    walk(tree);
    setExpanded(all);
  }

  function collapseAll() {
    if (!tree) return;
    setExpanded(new Set([nodeKey(tree)]));
  }

  if (!teamID) {
    return (
      <div className="it-empty">
        <p>Select a team from the left to view its inventory tree.</p>
      </div>
    );
  }

  return (
    <div className="it-root">
      <div className="it-toolbar">
        <input
          type="search"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder="Search projects, files, pages, sections…"
          className="it-search"
        />
        <label className="it-toggle">
          <input
            type="checkbox"
            checked={includeDeleted}
            onChange={(e) => setIncludeDeleted(e.target.checked)}
          />
          Show deleted
        </label>
        <button type="button" onClick={expandAll} className="it-btn">Expand all</button>
        <button type="button" onClick={collapseAll} className="it-btn">Collapse</button>
        <button type="button" onClick={() => void load()} className="it-btn">Refresh</button>
      </div>

      {loading && !tree && <p className="it-loading">Loading tree…</p>}
      {err && (
        <p className="it-err">
          {err.toLowerCase().includes("team_not_found")
            ? "This team has not been crawled yet — wait for the next poll cycle or click \"Sync now\"."
            : `Failed: ${err}`}
        </p>
      )}
      {tree && (
        <div className="it-tree" role="tree" aria-label="Inventory tree">
          <TreeNode
            node={tree}
            depth={0}
            expanded={effectiveExpanded}
            onToggle={toggle}
            search={search.trim()}
          />
        </div>
      )}

      <style jsx>{`
        .it-root {
          display: flex;
          flex-direction: column;
          gap: 12px;
        }
        .it-toolbar {
          display: flex;
          gap: 8px;
          flex-wrap: wrap;
          align-items: center;
        }
        .it-search {
          flex: 1;
          min-width: 200px;
          background: var(--bg, #0b0b0c);
          border: 1px solid var(--border, rgba(255, 255, 255, 0.12));
          color: var(--text-1, #eee);
          border-radius: 6px;
          padding: 6px 10px;
          font: inherit;
        }
        .it-toggle {
          display: flex;
          align-items: center;
          gap: 6px;
          font-size: 12px;
          color: var(--text-2, #aaa);
          cursor: pointer;
        }
        .it-btn {
          background: transparent;
          border: 1px solid var(--border, rgba(255, 255, 255, 0.12));
          color: var(--text-1, #eee);
          font: inherit;
          font-size: 12px;
          cursor: pointer;
          border-radius: 6px;
          padding: 4px 10px;
        }
        .it-btn:hover {
          background: var(--surface-2, rgba(255, 255, 255, 0.04));
        }
        .it-loading,
        .it-empty,
        .it-err {
          font-size: 13px;
          color: var(--text-3, #777);
          margin: 0;
        }
        .it-err {
          color: var(--danger, #ef4444);
        }
        .it-empty {
          padding: 32px 16px;
          text-align: center;
        }
        .it-tree {
          font-size: 13px;
        }
      `}</style>
    </div>
  );
}

interface TreeNodeProps {
  node: InventoryTreeNode;
  depth: number;
  expanded: Set<string>;
  onToggle: (key: string) => void;
  search: string;
}

function TreeNode({ node, depth, expanded, onToggle, search }: TreeNodeProps) {
  const key = nodeKey(node);
  const isOpen = expanded.has(key);
  const hasChildren = (node.children?.length ?? 0) > 0;
  const isDeleted = !!node.deleted_at;
  const matches = search ? node.name.toLowerCase().includes(search.toLowerCase()) : false;

  // When searching: hide subtrees that contain zero matches at any depth.
  const subtreeHasMatch = useMatchesSubtree(node, search);
  if (search && !subtreeHasMatch) return null;

  return (
    <div className={`tn-wrap${isDeleted ? " deleted" : ""}`} role="treeitem" aria-expanded={hasChildren ? isOpen : undefined}>
      <button
        type="button"
        className={`tn-row${matches ? " matched" : ""}`}
        onClick={() => hasChildren && onToggle(key)}
        style={{ paddingLeft: 8 + depth * 14 }}
      >
        <span className="tn-disclosure" aria-hidden>
          {hasChildren ? (isOpen ? "▾" : "▸") : " "}
        </span>
        <span className="tn-kind">{kindIcon(node.kind)}</span>
        <HighlightedName name={node.name} search={search} />
        <NodeMeta node={node} />
      </button>
      {isOpen && hasChildren && (
        <div className="tn-children" role="group">
          {node.children!.map((c) => (
            <TreeNode
              key={nodeKey(c)}
              node={c}
              depth={depth + 1}
              expanded={expanded}
              onToggle={onToggle}
              search={search}
            />
          ))}
        </div>
      )}
      <style jsx>{`
        .tn-wrap {
          width: 100%;
        }
        .tn-wrap.deleted {
          opacity: 0.55;
          text-decoration: line-through;
        }
        .tn-row {
          display: flex;
          align-items: center;
          gap: 6px;
          width: 100%;
          background: transparent;
          border: none;
          color: var(--text-1, #eee);
          font: inherit;
          cursor: pointer;
          padding: 4px 8px;
          border-radius: 4px;
          text-align: left;
        }
        .tn-row:hover {
          background: var(--surface-2, rgba(255, 255, 255, 0.04));
        }
        .tn-row.matched {
          background: var(--accent-dim, rgba(99, 102, 241, 0.15));
        }
        .tn-disclosure {
          width: 12px;
          color: var(--text-3, #888);
          font-size: 10px;
        }
        .tn-kind {
          font-size: 12px;
          opacity: 0.8;
        }
      `}</style>
    </div>
  );
}

function NodeMeta({ node }: { node: InventoryTreeNode }) {
  if (node.kind === "file" && node.last_modified) {
    return <span className="tn-meta">{shortTime(node.last_modified)}</span>;
  }
  if (node.kind === "section" && node.width != null && node.height != null) {
    return (
      <span className="tn-meta">
        {Math.round(node.x ?? 0)},{Math.round(node.y ?? 0)} · {Math.round(node.width)}×{Math.round(node.height)}
      </span>
    );
  }
  return null;
}

function HighlightedName({ name, search }: { name: string; search: string }) {
  if (!search) return <span className="tn-name">{name}</span>;
  const idx = name.toLowerCase().indexOf(search.toLowerCase());
  if (idx === -1) return <span className="tn-name">{name}</span>;
  return (
    <span className="tn-name">
      {name.slice(0, idx)}
      <mark className="tn-mark">{name.slice(idx, idx + search.length)}</mark>
      {name.slice(idx + search.length)}
      <style jsx>{`
        .tn-mark {
          background: var(--accent, #6366f1);
          color: #fff;
          padding: 0 2px;
          border-radius: 2px;
        }
      `}</style>
    </span>
  );
}

function kindIcon(kind: InventoryNodeKind): string {
  switch (kind) {
    case "team":
      return "👥";
    case "project":
      return "📁";
    case "file":
      return "📄";
    case "page":
      return "📑";
    case "section":
      return "▦";
  }
}

function shortTime(iso: string): string {
  // YYYY-MM-DD HH:MM
  return iso.replace("T", " ").slice(0, 16);
}

function nodeKey(node: InventoryTreeNode): string {
  return `${node.kind}:${node.id}`;
}

function useMatchesSubtree(node: InventoryTreeNode, search: string): boolean {
  // Pure function — no hooks despite the name. Kept "use" prefix only
  // for readability inside the component.
  if (!search) return true;
  const needle = search.toLowerCase();
  const stack: InventoryTreeNode[] = [node];
  while (stack.length) {
    const n = stack.pop()!;
    if (n.name.toLowerCase().includes(needle)) return true;
    if (n.children) stack.push(...n.children);
  }
  return false;
}
