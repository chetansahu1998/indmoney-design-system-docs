"use client";

/**
 * ComponentsPanel — Phase 2C cross-file component usage view.
 *
 * Lists master components in the inventory ranked by total INSTANCE
 * count across every crawled file. Each row drills into a per-component
 * detail view showing the file/project context for every instance.
 *
 * Backed by:
 *   GET /v1/admin/figma-inventory/components
 *   GET /v1/admin/figma-inventory/components/{component_key}/usage
 *
 * The crawl populates the data — until enough files are deep-synced
 * AND library master files have been crawled, this panel may render
 * "no components yet" or show partial counts. That's expected during
 * the backfill window.
 */

import { useCallback, useEffect, useMemo, useState } from "react";

import { adminFetchJSON } from "@/app/atlas/_lib/adminFetch";
import { useAuth } from "@/lib/auth-client";

import {
  EmptyBox,
  GhostBtn,
  SortableTh,
  Td,
  Th,
  formatAgo,
  type SortDirection,
} from "../_lib/Table";

interface ComponentUsageRow {
  component_key: string;
  master_name?: string;
  master_file_key?: string;
  master_node_id?: string;
  files_using: number;
  total_instances: number;
}

interface UsageResp {
  components: ComponentUsageRow[];
  count: number;
}

interface ComponentInstance {
  file_key: string;
  file_name: string;
  project_name: string;
  instance_node_id: string;
  instance_name: string;
  parent_id?: string;
  depth: number;
}

interface DetailResp {
  component_key: string;
  instances: ComponentInstance[];
  count: number;
}

type SortField = "name" | "files" | "instances";

export function ComponentsPanel() {
  const token = useAuth((s) => s.token);
  const [rows, setRows] = useState<ComponentUsageRow[]>([]);
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState("");
  const [search, setSearch] = useState("");
  const [sortField, setSortField] = useState<SortField>("instances");
  const [sortDir, setSortDir] = useState<SortDirection>("desc");
  const [selected, setSelected] = useState<ComponentUsageRow | null>(null);

  const load = useCallback(async () => {
    if (!token) return;
    setLoading(true);
    setErr("");
    try {
      const res = await adminFetchJSON<UsageResp>("/v1/admin/figma-inventory/components?limit=200");
      setRows(res.components || []);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }, [token]);

  useEffect(() => {
    void load();
  }, [load]);

  const filtered = useMemo(() => {
    const needle = search.trim().toLowerCase();
    if (!needle) return rows;
    return rows.filter((r) =>
      (r.master_name || "").toLowerCase().includes(needle) ||
      r.component_key.toLowerCase().includes(needle),
    );
  }, [rows, search]);

  const sorted = useMemo(() => {
    const arr = [...filtered];
    const mul = sortDir === "asc" ? 1 : -1;
    arr.sort((a, b) => {
      switch (sortField) {
        case "name":
          return mul * (a.master_name || a.component_key).localeCompare(b.master_name || b.component_key);
        case "files":
          return mul * (a.files_using - b.files_using);
        case "instances":
          return mul * (a.total_instances - b.total_instances);
      }
    });
    return arr;
  }, [filtered, sortField, sortDir]);

  function toggleSort(f: SortField) {
    if (sortField === f) {
      setSortDir((d) => (d === "asc" ? "desc" : "asc"));
    } else {
      setSortField(f);
      setSortDir(f === "name" ? "asc" : "desc");
    }
  }

  return (
    <section style={{ marginTop: 32 }}>
      <div style={{ display: "flex", alignItems: "baseline", gap: 12, marginBottom: 12 }}>
        <h2 style={{ fontSize: 14, fontWeight: 600, textTransform: "uppercase", letterSpacing: "0.08em", color: "var(--text-2, #aaa)", margin: 0 }}>
          Components — cross-file usage
        </h2>
        <span style={{ fontSize: 11, color: "var(--text-3, #777)" }}>
          Each row = one library master. Click to see every instance across all files.
        </span>
        <div style={{ marginLeft: "auto", display: "flex", gap: 8, alignItems: "center" }}>
          <input
            type="search"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="Search component name or key…"
            style={{
              background: "var(--bg, #0b0b0c)",
              border: "1px solid var(--border, rgba(255,255,255,0.12))",
              color: "var(--text-1, #f7f7f7)",
              borderRadius: 6,
              padding: "6px 10px",
              font: "inherit",
              fontSize: 13,
              minWidth: 220,
            }}
          />
          <GhostBtn onClick={() => void load()} disabled={loading}>
            {loading ? "Refreshing…" : "Refresh"}
          </GhostBtn>
        </div>
      </div>

      {err && (
        <p style={{ color: "var(--danger, #ef4444)", fontSize: 12, margin: 0 }}>Failed: {err}</p>
      )}
      {!loading && rows.length === 0 && !err && (
        <EmptyBox>
          <strong>No components instanced yet.</strong>
          <br />
          Components surface here once the deep crawl resolves their durable keys. Watch the runs strip for progress.
        </EmptyBox>
      )}

      {sorted.length > 0 && (
        <div style={{ display: "grid", gridTemplateColumns: selected ? "1fr 360px" : "1fr", gap: 16, alignItems: "start" }}>
          <table style={{ width: "100%", borderCollapse: "collapse", fontSize: 13 }}>
            <thead>
              <tr style={{ borderBottom: "1px solid var(--border, #333)", textAlign: "left" }}>
                <SortableTh field="name" current={sortField} direction={sortDir} onClick={toggleSort}>
                  Component
                </SortableTh>
                <Th>Library key</Th>
                <SortableTh field="files" current={sortField} direction={sortDir} onClick={toggleSort} align="right">
                  In files
                </SortableTh>
                <SortableTh field="instances" current={sortField} direction={sortDir} onClick={toggleSort} align="right">
                  Instances
                </SortableTh>
                <Th>Master</Th>
              </tr>
            </thead>
            <tbody>
              {sorted.map((r) => {
                const isActive = selected?.component_key === r.component_key;
                return (
                  <tr
                    key={r.component_key}
                    onClick={() => setSelected(isActive ? null : r)}
                    style={{
                      borderBottom: "1px solid var(--border, #222)",
                      cursor: "pointer",
                      background: isActive ? "var(--accent-soft, rgba(80,180,255,0.12))" : undefined,
                    }}
                  >
                    <Td>{r.master_name || <span style={{ color: "var(--text-3, #888)" }}>(no name)</span>}</Td>
                    <Td mono muted>
                      {r.component_key.slice(0, 24)}
                      {r.component_key.length > 24 ? "…" : ""}
                    </Td>
                    <Td align="right">
                      <span style={{ fontVariantNumeric: "tabular-nums" }}>{r.files_using}</span>
                    </Td>
                    <Td align="right">
                      <span style={{ fontVariantNumeric: "tabular-nums", fontWeight: 600 }}>
                        {r.total_instances.toLocaleString()}
                      </span>
                    </Td>
                    <Td muted>
                      {r.master_file_key ? (
                        <span title={`${r.master_file_key} · ${r.master_node_id}`}>in inventory</span>
                      ) : (
                        <span style={{ color: "var(--text-3, #888)" }} title="Master is in a library file we haven't crawled (or is published-only)">
                          remote
                        </span>
                      )}
                    </Td>
                  </tr>
                );
              })}
            </tbody>
          </table>

          {selected && <UsageDetail component={selected} onClose={() => setSelected(null)} />}
        </div>
      )}
    </section>
  );
}

function UsageDetail({ component, onClose }: { component: ComponentUsageRow; onClose: () => void }) {
  const [data, setData] = useState<ComponentInstance[]>([]);
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState("");

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setErr("");
    void adminFetchJSON<DetailResp>(
      `/v1/admin/figma-inventory/components/${encodeURIComponent(component.component_key)}/usage?limit=500`,
    )
      .then((res) => {
        if (cancelled) return;
        setData(res.instances || []);
      })
      .catch((e) => {
        if (cancelled) return;
        setErr(e instanceof Error ? e.message : String(e));
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [component.component_key]);

  // Group by (project, file).
  const grouped = useMemo(() => {
    const out: Record<string, { project: string; file: string; fileKey: string; instances: ComponentInstance[] }> = {};
    for (const inst of data) {
      const k = `${inst.project_name}::${inst.file_key}`;
      if (!out[k]) {
        out[k] = { project: inst.project_name, file: inst.file_name, fileKey: inst.file_key, instances: [] };
      }
      out[k].instances.push(inst);
    }
    return Object.values(out).sort((a, b) =>
      a.project.localeCompare(b.project) || a.file.localeCompare(b.file),
    );
  }, [data]);

  return (
    <aside
      style={{
        position: "sticky",
        top: 16,
        padding: 12,
        background: "var(--bg-surface, rgba(255,255,255,0.03))",
        border: "1px solid var(--border, rgba(255,255,255,0.12))",
        borderRadius: 8,
        maxHeight: "75vh",
        overflowY: "auto",
        display: "flex",
        flexDirection: "column",
        gap: 8,
      }}
    >
      <div style={{ display: "flex", alignItems: "baseline", justifyContent: "space-between" }}>
        <h3 style={{ fontSize: 13, margin: 0 }}>
          {component.master_name || "(no name)"}{" "}
          <span style={{ color: "var(--text-3, #888)", fontWeight: 400 }}>
            {component.total_instances.toLocaleString()} instances · {component.files_using} files
          </span>
        </h3>
        <button
          type="button"
          onClick={onClose}
          aria-label="Close"
          style={{
            background: "transparent",
            border: "none",
            color: "var(--text-3, #888)",
            cursor: "pointer",
            fontSize: 14,
          }}
        >
          ×
        </button>
      </div>
      <code style={{ fontSize: 10, color: "var(--text-3, #777)" }}>{component.component_key}</code>
      {loading && <p style={{ fontSize: 12, color: "var(--text-3, #888)", margin: 0 }}>Loading instances…</p>}
      {err && <p style={{ fontSize: 12, color: "var(--danger, #ef4444)", margin: 0 }}>Failed: {err}</p>}
      {!loading && grouped.length === 0 && !err && (
        <p style={{ fontSize: 12, color: "var(--text-3, #888)", margin: 0 }}>No instances yet (crawl in progress).</p>
      )}
      {grouped.map((g) => (
        <div key={g.fileKey} style={{ borderTop: "1px solid var(--border, rgba(255,255,255,0.06))", paddingTop: 8, marginTop: 4 }}>
          <div style={{ display: "flex", alignItems: "baseline", gap: 6, marginBottom: 4 }}>
            <span style={{ fontSize: 11, color: "var(--text-3, #888)" }}>{g.project} ›</span>
            <a
              href={`https://www.figma.com/design/${g.fileKey}`}
              target="_blank"
              rel="noopener noreferrer"
              style={{ fontSize: 12, color: "var(--accent, #7b9fff)", textDecoration: "none" }}
            >
              {g.file}
            </a>
            <span style={{ marginLeft: "auto", fontSize: 11, color: "var(--text-3, #888)", fontVariantNumeric: "tabular-nums" }}>
              {g.instances.length}
            </span>
          </div>
          {g.instances.slice(0, 8).map((inst) => (
            <a
              key={inst.instance_node_id}
              href={`https://www.figma.com/design/${g.fileKey}?node-id=${inst.instance_node_id.replace(":", "-")}`}
              target="_blank"
              rel="noopener noreferrer"
              style={{
                display: "block",
                paddingLeft: 12,
                fontSize: 11,
                color: "var(--text-2, #aaa)",
                textDecoration: "none",
              }}
              title={`depth ${inst.depth}`}
            >
              · {inst.instance_name || inst.instance_node_id}
            </a>
          ))}
          {g.instances.length > 8 && (
            <p style={{ paddingLeft: 12, fontSize: 11, color: "var(--text-3, #777)", margin: 0 }}>
              + {g.instances.length - 8} more
            </p>
          )}
        </div>
      ))}
    </aside>
  );
}

// formatAgo is exported from _lib/Table; keep this import here so the
// component compiles even when nothing in this file currently uses it
// (placeholder for a "last seen" column once we track instance recency).
void formatAgo;
