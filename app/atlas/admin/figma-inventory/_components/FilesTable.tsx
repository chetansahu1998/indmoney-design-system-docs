"use client";

/**
 * FilesTable — primary view for /atlas/admin/figma-inventory.
 *
 * Flattens the inventory tree (team → project → file → page → section)
 * into a sortable file-level table with filter chips for project, recency
 * window, and status. Each row can be expanded to show its pages and
 * sections inline — that's the "tree" experience surfaced only when the
 * admin actually wants to drill in.
 *
 * Default sort: last_modified DESC (newest first) — matches the explicit
 * design ask: "by date recent". Click any column header to re-sort.
 *
 * Backed by GET /v1/admin/figma-inventory/tree?team_id=...
 */

import { useCallback, useEffect, useMemo, useState } from "react";

import { adminFetchJSON } from "@/app/atlas/admin/_lib/adminFetch";
import { useAuth } from "@/lib/auth-client";

import {
  EmptyBox,
  GhostBtn,
  Pill,
  SortableTh,
  StatusBadge,
  Td,
  Th,
  formatAgo,
  type SortDirection,
} from "../_lib/Table";
import type { InventoryTreeNode } from "../types";

interface Props {
  teamID: string;
}

type SortField = "name" | "project" | "last_modified" | "pages" | "sections";

interface FileRow {
  fileKey: string;
  fileName: string;
  projectID: string;
  projectName: string;
  lastModified: string; // RFC3339 or ""
  pages: InventoryTreeNode[]; // for expand-row view
  pageCount: number;
  sectionCount: number;
  thumbnailURL?: string;
  deletedAt?: string;
}

type StatusFilter = "all" | "synced" | "pending" | "deleted";
type RecencyFilter = "any" | "24h" | "7d" | "30d";

const RECENCY_LABEL: Record<RecencyFilter, string> = {
  any: "Any time",
  "24h": "24h",
  "7d": "7d",
  "30d": "30d",
};

const STATUS_LABEL: Record<StatusFilter, string> = {
  all: "All",
  synced: "Pages synced",
  pending: "Pending sync",
  deleted: "Deleted",
};

export function FilesTable({ teamID }: Props) {
  const token = useAuth((s) => s.token);
  const [tree, setTree] = useState<InventoryTreeNode | null>(null);
  const [loading, setLoading] = useState(false);
  const [err, setErr] = useState<string>("");

  // Filter + sort state.
  const [search, setSearch] = useState("");
  const [projectFilter, setProjectFilter] = useState<Set<string>>(new Set());
  const [statusFilter, setStatusFilter] = useState<StatusFilter>("all");
  const [recencyFilter, setRecencyFilter] = useState<RecencyFilter>("any");
  const [sortField, setSortField] = useState<SortField>("last_modified");
  const [sortDir, setSortDir] = useState<SortDirection>("desc");
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [includeDeleted, setIncludeDeleted] = useState(false);

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
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
      setTree(null);
    } finally {
      setLoading(false);
    }
  }, [teamID, token, includeDeleted]);

  useEffect(() => {
    void load();
  }, [load]);

  // Flatten to file rows.
  const allRows = useMemo<FileRow[]>(() => {
    if (!tree) return [];
    const out: FileRow[] = [];
    for (const project of tree.children ?? []) {
      if (project.kind !== "project") continue;
      for (const file of project.children ?? []) {
        if (file.kind !== "file") continue;
        const pages = (file.children ?? []).filter((c) => c.kind === "page");
        const sectionCount = pages.reduce(
          (acc, p) => acc + (p.children?.filter((c) => c.kind === "section").length ?? 0),
          0,
        );
        out.push({
          fileKey: file.id,
          fileName: file.name,
          projectID: project.id,
          projectName: project.name,
          lastModified: file.last_modified ?? "",
          pages,
          pageCount: pages.length,
          sectionCount,
          thumbnailURL: file.thumbnail_url,
          deletedAt: file.deleted_at,
        });
      }
    }
    return out;
  }, [tree]);

  // Project list for the multi-select chips.
  const projects = useMemo(() => {
    const map = new Map<string, { id: string; name: string; count: number }>();
    for (const r of allRows) {
      const cur = map.get(r.projectID);
      if (cur) cur.count += 1;
      else map.set(r.projectID, { id: r.projectID, name: r.projectName, count: 1 });
    }
    return [...map.values()].sort((a, b) => b.count - a.count || a.name.localeCompare(b.name));
  }, [allRows]);

  // Filter.
  const filtered = useMemo(() => {
    const now = Date.now();
    const windowMs =
      recencyFilter === "24h" ? 24 * 3600 * 1000
        : recencyFilter === "7d" ? 7 * 86400 * 1000
        : recencyFilter === "30d" ? 30 * 86400 * 1000
        : 0;
    const needle = search.trim().toLowerCase();
    return allRows.filter((r) => {
      if (projectFilter.size > 0 && !projectFilter.has(r.projectID)) return false;
      if (needle && !r.fileName.toLowerCase().includes(needle) && !r.projectName.toLowerCase().includes(needle)) return false;
      // Status filter
      const isDeleted = !!r.deletedAt;
      const isSynced = r.pageCount > 0;
      if (statusFilter === "deleted" && !isDeleted) return false;
      if (statusFilter !== "deleted" && isDeleted) return false; // hide deleted unless explicitly chosen
      if (statusFilter === "synced" && !isSynced) return false;
      if (statusFilter === "pending" && isSynced) return false;
      // Recency filter
      if (windowMs > 0) {
        const t = r.lastModified ? new Date(r.lastModified).getTime() : 0;
        if (!t || now - t > windowMs) return false;
      }
      return true;
    });
  }, [allRows, search, projectFilter, statusFilter, recencyFilter]);

  // Sort.
  const sorted = useMemo(() => {
    const arr = [...filtered];
    const mul = sortDir === "asc" ? 1 : -1;
    arr.sort((a, b) => {
      switch (sortField) {
        case "name":
          return mul * a.fileName.localeCompare(b.fileName);
        case "project":
          return (
            mul * a.projectName.localeCompare(b.projectName) ||
            a.fileName.localeCompare(b.fileName)
          );
        case "last_modified": {
          const ta = a.lastModified ? new Date(a.lastModified).getTime() : 0;
          const tb = b.lastModified ? new Date(b.lastModified).getTime() : 0;
          return mul * (ta - tb) || a.fileName.localeCompare(b.fileName);
        }
        case "pages":
          return mul * (a.pageCount - b.pageCount) || a.fileName.localeCompare(b.fileName);
        case "sections":
          return mul * (a.sectionCount - b.sectionCount) || a.fileName.localeCompare(b.fileName);
      }
    });
    return arr;
  }, [filtered, sortField, sortDir]);

  function toggleSort(field: SortField) {
    if (sortField === field) {
      setSortDir((d) => (d === "asc" ? "desc" : "asc"));
    } else {
      setSortField(field);
      // Sensible default direction per field.
      setSortDir(field === "last_modified" || field === "pages" || field === "sections" ? "desc" : "asc");
    }
  }

  function toggleProject(id: string) {
    setProjectFilter((s) => {
      const next = new Set(s);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }

  function clearFilters() {
    setSearch("");
    setProjectFilter(new Set());
    setStatusFilter("all");
    setRecencyFilter("any");
  }

  function toggleExpand(key: string) {
    setExpanded((s) => {
      const next = new Set(s);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
  }

  if (!teamID) {
    return <EmptyBox><strong>Select a team</strong> above to view its inventory.</EmptyBox>;
  }

  return (
    <section>
      {/* ── Filter row ─────────────────────────────────────────────────── */}
      <div
        style={{
          display: "flex",
          gap: 8,
          flexWrap: "wrap",
          alignItems: "center",
          marginBottom: 12,
        }}
      >
        <input
          type="search"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder="Search file or project…"
          style={{
            flex: "1 1 240px",
            minWidth: 200,
            maxWidth: 360,
            background: "var(--bg, #0b0b0c)",
            border: "1px solid var(--border, rgba(255,255,255,0.12))",
            color: "var(--text-1, #f7f7f7)",
            borderRadius: 6,
            padding: "6px 10px",
            font: "inherit",
            fontSize: 13,
          }}
        />
        {(["any", "24h", "7d", "30d"] as RecencyFilter[]).map((k) => (
          <Pill key={k} active={recencyFilter === k} onClick={() => setRecencyFilter(k)}>
            {RECENCY_LABEL[k]}
          </Pill>
        ))}
        <span style={{ width: 1, height: 20, background: "var(--border, rgba(255,255,255,0.12))" }} aria-hidden />
        {(["all", "synced", "pending", "deleted"] as StatusFilter[]).map((k) => (
          <Pill
            key={k}
            active={statusFilter === k}
            onClick={() => {
              setStatusFilter(k);
              setIncludeDeleted(k === "deleted");
            }}
          >
            {STATUS_LABEL[k]}
          </Pill>
        ))}
        <div style={{ marginLeft: "auto", display: "flex", gap: 8 }}>
          {(search || projectFilter.size > 0 || statusFilter !== "all" || recencyFilter !== "any") && (
            <GhostBtn onClick={clearFilters}>Reset filters</GhostBtn>
          )}
          <GhostBtn onClick={() => void load()} disabled={loading}>
            {loading ? "Refreshing…" : "Refresh"}
          </GhostBtn>
        </div>
      </div>

      {/* ── Project chip row ──────────────────────────────────────────── */}
      {projects.length > 1 && (
        <div
          style={{
            display: "flex",
            gap: 6,
            flexWrap: "wrap",
            alignItems: "center",
            marginBottom: 12,
            paddingBottom: 12,
            borderBottom: "1px solid var(--border, rgba(255,255,255,0.06))",
          }}
        >
          <span style={{ fontSize: 11, color: "var(--text-3, #888)", textTransform: "uppercase", letterSpacing: "0.06em", marginRight: 4 }}>
            Project
          </span>
          {projects.map((p) => (
            <Pill
              key={p.id}
              active={projectFilter.has(p.id)}
              onClick={() => toggleProject(p.id)}
              count={p.count}
              title={`project_id ${p.id}`}
            >
              {p.name}
            </Pill>
          ))}
          {projectFilter.size > 0 && (
            <GhostBtn onClick={() => setProjectFilter(new Set())}>Clear</GhostBtn>
          )}
        </div>
      )}

      {/* ── Result count + table ──────────────────────────────────────── */}
      <div style={{ marginBottom: 8, color: "var(--text-3, #888)", fontSize: 12 }}>
        Showing <strong style={{ color: "var(--text-1, #f7f7f7)" }}>{sorted.length}</strong>
        {" of "}
        <strong style={{ color: "var(--text-1, #f7f7f7)" }}>{allRows.length}</strong> files
        {sortField === "last_modified" && sortDir === "desc" && " · sorted by most recent"}
        {sortField !== "last_modified" && ` · sorted by ${sortField} (${sortDir})`}
      </div>

      {err && (
        <div
          role="alert"
          style={{
            padding: 12,
            marginBottom: 12,
            background: "rgba(255, 80, 80, 0.08)",
            border: "1px solid rgba(255, 80, 80, 0.3)",
            borderRadius: 6,
            color: "rgba(255, 150, 150, 0.95)",
            fontSize: 13,
          }}
        >
          {err.toLowerCase().includes("team_not_found")
            ? "This team has not been crawled yet — wait for the next poll cycle or click Sync now."
            : `Failed: ${err}`}
        </div>
      )}

      {loading && allRows.length === 0 && (
        <EmptyBox>Loading inventory…</EmptyBox>
      )}

      {!loading && sorted.length === 0 && !err && (
        <EmptyBox>
          {allRows.length === 0
            ? <><strong>No files in this team yet.</strong><br />Wait for the next poll cycle, or click Sync now.</>
            : <><strong>No files match the current filters.</strong><br />Reset filters or widen the recency window.</>}
        </EmptyBox>
      )}

      {sorted.length > 0 && (
        <table style={{ width: "100%", borderCollapse: "collapse", fontSize: 13 }}>
          <thead>
            <tr style={{ borderBottom: "1px solid var(--border, #333)", textAlign: "left" }}>
              <Th width={28}> </Th>
              <SortableTh field="name" current={sortField} direction={sortDir} onClick={toggleSort}>
                File
              </SortableTh>
              <SortableTh field="project" current={sortField} direction={sortDir} onClick={toggleSort}>
                Project
              </SortableTh>
              <SortableTh field="last_modified" current={sortField} direction={sortDir} onClick={toggleSort}>
                Modified
              </SortableTh>
              <SortableTh field="pages" current={sortField} direction={sortDir} onClick={toggleSort} align="right">
                Pages
              </SortableTh>
              <SortableTh field="sections" current={sortField} direction={sortDir} onClick={toggleSort} align="right">
                Sections
              </SortableTh>
              <Th>Status</Th>
              <Th align="right">Actions</Th>
            </tr>
          </thead>
          <tbody>
            {sorted.map((r) => {
              const isOpen = expanded.has(r.fileKey);
              const isDeleted = !!r.deletedAt;
              const isSynced = r.pageCount > 0;
              return (
                <FileRowView
                  key={r.fileKey}
                  row={r}
                  isOpen={isOpen}
                  onToggle={() => toggleExpand(r.fileKey)}
                  isDeleted={isDeleted}
                  isSynced={isSynced}
                />
              );
            })}
          </tbody>
        </table>
      )}
    </section>
  );
}

function FileRowView({
  row,
  isOpen,
  onToggle,
  isDeleted,
  isSynced,
}: {
  row: FileRow;
  isOpen: boolean;
  onToggle: () => void;
  isDeleted: boolean;
  isSynced: boolean;
}) {
  return (
    <>
      <tr
        style={{
          borderBottom: "1px solid var(--border, #222)",
          opacity: isDeleted ? 0.55 : 1,
        }}
      >
        <Td onClick={onToggle} style={{ cursor: "pointer", paddingRight: 0 }}>
          <span aria-hidden style={{ color: "var(--text-3, #888)" }}>
            {row.pageCount > 0 ? (isOpen ? "▾" : "▸") : "·"}
          </span>
        </Td>
        <Td onClick={onToggle} style={{ cursor: "pointer" }}>
          <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
            <span style={{ textDecoration: isDeleted ? "line-through" : undefined }}>
              {row.fileName}
            </span>
            <code style={{ fontSize: 10, color: "var(--text-3, #777)" }} title="file_key">
              {row.fileKey.slice(0, 8)}
            </code>
          </div>
        </Td>
        <Td muted>{row.projectName}</Td>
        <Td title={row.lastModified}>
          <span style={{ fontVariantNumeric: "tabular-nums" }}>{formatAgo(row.lastModified)}</span>
        </Td>
        <Td align="right" muted={row.pageCount === 0}>
          <span style={{ fontVariantNumeric: "tabular-nums" }}>{row.pageCount}</span>
        </Td>
        <Td align="right" muted={row.sectionCount === 0}>
          <span style={{ fontVariantNumeric: "tabular-nums" }}>{row.sectionCount}</span>
        </Td>
        <Td>
          {isDeleted ? (
            <StatusBadge kind="muted">deleted</StatusBadge>
          ) : isSynced ? (
            <StatusBadge kind="ok">synced</StatusBadge>
          ) : (
            <StatusBadge kind="pending">pending</StatusBadge>
          )}
        </Td>
        <Td align="right">
          <a
            href={`https://www.figma.com/design/${row.fileKey}`}
            target="_blank"
            rel="noopener noreferrer"
            style={{
              padding: "4px 10px",
              background: "var(--accent-soft, rgba(80, 180, 255, 0.1))",
              border: "1px solid var(--accent, rgba(80, 180, 255, 0.4))",
              color: "var(--accent, rgba(150, 200, 255, 1))",
              borderRadius: 6,
              textDecoration: "none",
              fontSize: 12,
              whiteSpace: "nowrap",
            }}
          >
            Open ↗
          </a>
        </Td>
      </tr>
      {isOpen && row.pageCount > 0 && (
        <tr style={{ borderBottom: "1px solid var(--border, #222)" }}>
          <Td colSpan={8} style={{ background: "var(--bg-surface-2, rgba(123, 159, 255, 0.04))", padding: "8px 12px 12px" }}>
            <ExpandedPages pages={row.pages} fileKey={row.fileKey} />
          </Td>
        </tr>
      )}
    </>
  );
}

function ExpandedPages({ pages, fileKey }: { pages: InventoryTreeNode[]; fileKey: string }) {
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
      {pages.map((page) => {
        const sections = (page.children ?? []).filter((c) => c.kind === "section");
        return (
          <div key={page.id} style={{ paddingLeft: 8 }}>
            <div style={{ display: "flex", alignItems: "baseline", gap: 8, marginBottom: 2 }}>
              <span style={{ fontSize: 13, fontWeight: 500 }}>📑 {page.name}</span>
              <span style={{ fontSize: 11, color: "var(--text-3, #888)" }}>
                {sections.length} section{sections.length === 1 ? "" : "s"}
              </span>
              <a
                href={`https://www.figma.com/design/${fileKey}?node-id=${page.id.replace(":", "-")}`}
                target="_blank"
                rel="noopener noreferrer"
                style={{ fontSize: 11, color: "var(--accent, #7b9fff)", marginLeft: "auto" }}
              >
                Open page ↗
              </a>
            </div>
            {sections.length > 0 ? (
              <ul style={{ listStyle: "none", padding: 0, margin: 0 }}>
                {sections.map((sec) => (
                  <li
                    key={sec.id}
                    style={{
                      display: "flex",
                      alignItems: "baseline",
                      gap: 8,
                      padding: "3px 0 3px 18px",
                      fontSize: 12,
                      color: "var(--text-2, #aaa)",
                    }}
                  >
                    <span>▦ {sec.name}</span>
                    {sec.width != null && sec.height != null && (
                      <span style={{ fontSize: 11, color: "var(--text-3, #777)", fontVariantNumeric: "tabular-nums" }}>
                        {Math.round(sec.x ?? 0)},{Math.round(sec.y ?? 0)} · {Math.round(sec.width)}×{Math.round(sec.height)}
                      </span>
                    )}
                  </li>
                ))}
              </ul>
            ) : (
              <p style={{ margin: "2px 0 0 18px", fontSize: 11, color: "var(--text-3, #777)" }}>
                No sections on this page.
              </p>
            )}
          </div>
        );
      })}
    </div>
  );
}
