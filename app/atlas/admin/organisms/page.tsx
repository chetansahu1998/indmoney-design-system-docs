"use client";

/**
 * /atlas/admin/organisms — Part C of the organism-pattern-detection plan
 * (docs/plans/2026-05-13-001-feat-organism-pattern-detection-plan.md, U11
 * + U14). Two surfaces on one page:
 *
 *   1. Adoption table (U11) — per-slug counts of exact / near / novel
 *      matches across the tenant's imported corpus. The "drift signal"
 *      cell flags slugs where the ratio of (near + novel) to total is
 *      high — those are the published organisms that designers are
 *      forking aggressively instead of using as INSTANCEs.
 *
 *   2. Promotion candidates panel (U14) — the top-N fingerprints that
 *      recurred K+ times across N+ files without matching any published
 *      organism. Ranked by composite score
 *      (frequency × stability_score × atom_reuse_rate).
 *
 * Both surfaces are read-only views over the corpus written by Stage 6.7
 * (U5) and the Part D rebuild (U13). No live recomputation here — the
 * dashboard reflects whatever the last import + aggregation produced.
 */

import { useCallback, useEffect, useState } from "react";

import { AdminShell } from "@/app/atlas/admin/_lib/AdminShell";
import { adminFetchJSON } from "@/app/atlas/admin/_lib/adminFetch";
import { useAuth } from "@/lib/auth-client";

// ─── Response shapes (mirror server_organism_admin.go DTOs) ──────────────────

interface AdoptionRow {
  slug: string;
  name?: string;
  category?: string;
  instance_count: number;
  exact: number;
  near: number;
  novel: number;
}

interface AdoptionResponse {
  rows: AdoptionRow[];
  total_matches: number;
  signature_catalog_empty: boolean;
}

interface PromotionCandidate {
  fingerprint_hash: string;
  frequency: number;
  file_count: number;
  stability_score: number;
  atom_reuse_rate: number;
  composite_score: number;
  proposed_name?: string;
  first_seen: string;
  last_seen: string;
}

interface PromotionResponse {
  candidates: PromotionCandidate[];
  count: number;
}

// ─── Drift signal classification ─────────────────────────────────────────────

type DriftLevel = "green" | "yellow" | "red";

/**
 * Classify a published slug's adoption status by the (near + novel) /
 * (total) ratio. The "(unmatched-novel)" bucket has no published slug to
 * drift against — it's classified separately as "novel" (info, not warning).
 *
 *   green   — designers mostly use published INSTANCEs / exact matches
 *   yellow  — meaningful drift; some near-matches or novel siblings
 *   red     — heavy drift; designers are forking aggressively
 */
function driftLevel(row: AdoptionRow): DriftLevel | "novel" {
  if (row.slug === "") return "novel";
  const total = row.instance_count + row.exact + row.near + row.novel;
  if (total === 0) return "green";
  const driftRatio = (row.near + row.novel) / total;
  if (driftRatio > 0.5) return "red";
  if (driftRatio > 0.2) return "yellow";
  return "green";
}

function driftDot(level: DriftLevel | "novel"): string {
  switch (level) {
    case "red":
      return "🔴";
    case "yellow":
      return "🟡";
    case "green":
      return "🟢";
    case "novel":
      return "✨";
  }
}

function driftLabel(level: DriftLevel | "novel"): string {
  switch (level) {
    case "red":
      return "Heavy drift";
    case "yellow":
      return "Some drift";
    case "green":
      return "Healthy";
    case "novel":
      return "Unmatched (novel)";
  }
}

// ─── Page component ──────────────────────────────────────────────────────────

export default function OrganismsAdminPage() {
  const token = useAuth((s) => s.token);
  const [adoption, setAdoption] = useState<AdoptionResponse | null>(null);
  const [promotions, setPromotions] = useState<PromotionResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  const refresh = useCallback(async () => {
    if (!token) return;
    setLoading(true);
    setError(null);
    try {
      const [adoptionRes, promosRes] = await Promise.all([
        adminFetchJSON<AdoptionResponse>("/v1/admin/organisms/adoption"),
        adminFetchJSON<PromotionResponse>("/v1/admin/organisms/promotion-candidates?limit=20"),
      ]);
      setAdoption(adoptionRes);
      setPromotions(promosRes);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  }, [token]);

  const renamePromotionCandidate = useCallback(
    async (hash: string, name: string) => {
      await adminFetchJSON<{ ok: boolean }>(
        `/v1/admin/organisms/promotion-candidates/${encodeURIComponent(hash)}`,
        { method: "PATCH", body: { proposed_name: name } },
      );
      // Optimistic update — write back into the local cache so the input
      // mirrors the server's truth without a full refresh.
      setPromotions((prev) =>
        prev
          ? {
              ...prev,
              candidates: prev.candidates.map((c) =>
                c.fingerprint_hash === hash ? { ...c, proposed_name: name } : c,
              ),
            }
          : prev,
      );
    },
    [],
  );

  useEffect(() => {
    void refresh();
  }, [refresh]);

  return (
    <AdminShell
      title="Organism patterns"
      description="Detected component-shaped FRAMEs across the imported corpus, grouped by their suspected DS organism. Surfaces which published organisms are getting reused vs hand-rebuilt, and ranks promotion candidates for the DS team."
    >
      {loading && !adoption && <p style={{ color: "#888" }}>Loading…</p>}
      {error && (
        <p style={{ color: "#c43", marginBottom: 16, fontFamily: "monospace" }}>
          Error: {error}
        </p>
      )}
      {adoption?.signature_catalog_empty && (
        <SignatureCatalogEmptyBanner total={adoption.total_matches} />
      )}

      {adoption && <AdoptionTable rows={adoption.rows} total={adoption.total_matches} />}

      {promotions && (
        <PromotionCandidatesPanel
          candidates={promotions.candidates}
          onRename={renamePromotionCandidate}
        />
      )}

      <button
        type="button"
        onClick={() => void refresh()}
        disabled={loading}
        style={{
          marginTop: 24,
          padding: "8px 16px",
          fontSize: 13,
          background: "transparent",
          border: "1px solid #444",
          borderRadius: 4,
          color: "#ccc",
          cursor: loading ? "not-allowed" : "pointer",
        }}
      >
        {loading ? "Refreshing…" : "Refresh"}
      </button>
    </AdminShell>
  );
}

// ─── Subcomponents ───────────────────────────────────────────────────────────

function SignatureCatalogEmptyBanner({ total }: { total: number }) {
  return (
    <div
      style={{
        marginBottom: 20,
        padding: "12px 16px",
        background: "rgba(94, 234, 212, 0.08)",
        border: "1px solid rgba(94, 234, 212, 0.3)",
        borderRadius: 6,
        fontSize: 13,
        lineHeight: 1.5,
      }}
    >
      <strong style={{ color: "#5eead4" }}>Signature catalog is empty.</strong>{" "}
      All {total} detected matches classified as <em>novel</em>. The
      published manifest{" "}
      <code style={{ background: "rgba(0,0,0,0.3)", padding: "1px 5px", borderRadius: 3 }}>
        public/icons/glyph/manifest.json
      </code>{" "}
      has no <code>composition_refs</code> populated. Re-run{" "}
      <code>cmd/variants</code> against the Glyph DS file to generate
      organism signatures; the next import will reclassify these as{" "}
      <em>exact</em> / <em>near</em> matches automatically.
    </div>
  );
}

function AdoptionTable({ rows, total }: { rows: AdoptionRow[]; total: number }) {
  return (
    <section style={{ marginBottom: 32 }}>
      <h2 style={{ fontSize: 18, marginBottom: 4 }}>Adoption ({total} matches)</h2>
      <p style={{ color: "#888", fontSize: 12, marginBottom: 12 }}>
        One row per suspected DS organism. <strong>Exact</strong> matches are
        atom-perfect; <strong>near</strong> matches drift on atom set or slot
        order; <strong>novel</strong> matches don&apos;t resemble any published
        organism. <strong>Instance</strong> counts come from{" "}
        <code>graph_index</code> (real Figma INSTANCEs of the organism), which
        today is zero until atom-level uses-edges land for organism wrappers.
      </p>
      <table
        style={{
          width: "100%",
          borderCollapse: "collapse",
          fontSize: 13,
        }}
      >
        <thead>
          <tr style={{ borderBottom: "1px solid #333", textAlign: "left" }}>
            <Th>Slug</Th>
            <Th align="right">Instance</Th>
            <Th align="right">Exact</Th>
            <Th align="right">Near</Th>
            <Th align="right">Novel</Th>
            <Th>Signal</Th>
          </tr>
        </thead>
        <tbody>
          {rows.length === 0 && (
            <tr>
              <Td colSpan={6} style={{ color: "#888", padding: "16px 0" }}>
                No organism matches detected yet. Import a project via the
                Figma plugin or <code>cmd/import-figma-url</code>.
              </Td>
            </tr>
          )}
          {rows.map((row, i) => {
            const level = driftLevel(row);
            const total = row.instance_count + row.exact + row.near + row.novel;
            return (
              <tr
                key={row.slug || `_unmatched_${i}`}
                style={{
                  borderBottom: "1px solid #222",
                }}
              >
                <Td>
                  <code style={{ fontFamily: "ui-monospace, monospace" }}>
                    {row.slug || "(unmatched-novel)"}
                  </code>
                </Td>
                <Td align="right">{row.instance_count}</Td>
                <Td align="right" muted={row.exact === 0}>
                  {row.exact}
                </Td>
                <Td align="right" muted={row.near === 0}>
                  {row.near}
                </Td>
                <Td align="right" muted={row.novel === 0}>
                  {row.novel}
                </Td>
                <Td>
                  <span title={driftLabel(level)}>
                    {driftDot(level)} <span style={{ color: "#888" }}>{driftLabel(level)}</span>
                    {total > 0 && level !== "novel" && (
                      <span style={{ color: "#666", marginLeft: 6 }}>
                        ({Math.round(((row.near + row.novel) / total) * 100)}% drift)
                      </span>
                    )}
                  </span>
                </Td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </section>
  );
}

function PromotionCandidatesPanel({
  candidates,
  onRename,
}: {
  candidates: PromotionCandidate[];
  onRename: (hash: string, name: string) => Promise<void>;
}) {
  return (
    <section>
      <h2 style={{ fontSize: 18, marginBottom: 4 }}>
        Promotion candidates ({candidates.length})
      </h2>
      <p style={{ color: "#888", fontSize: 12, marginBottom: 12 }}>
        Patterns that recur K+ times across N+ files in the tenant but
        don&apos;t match any published organism. Ranked by composite score
        (<code>frequency × stability × atom_reuse_rate</code>). Click any row&apos;s
        name cell to give a candidate a working title for the DS team.
      </p>
      {candidates.length === 0 && (
        <p
          style={{
            color: "#888",
            fontSize: 13,
            padding: "16px 0",
            borderTop: "1px solid #222",
            borderBottom: "1px solid #222",
          }}
        >
          No patterns recur enough across multiple files yet. Once 2+ product
          files share a structural pattern at least 3 times, candidates appear
          here automatically.
        </p>
      )}
      {candidates.length > 0 && (
        <table
          style={{
            width: "100%",
            borderCollapse: "collapse",
            fontSize: 13,
          }}
        >
          <thead>
            <tr style={{ borderBottom: "1px solid #333", textAlign: "left" }}>
              <Th>Fingerprint</Th>
              <Th>Proposed name</Th>
              <Th align="right">Frequency</Th>
              <Th align="right">Files</Th>
              <Th align="right">Stability</Th>
              <Th align="right">Atom reuse</Th>
              <Th align="right">Score</Th>
            </tr>
          </thead>
          <tbody>
            {candidates.map((c) => (
              <tr key={c.fingerprint_hash} style={{ borderBottom: "1px solid #222" }}>
                <Td>
                  <code style={{ fontFamily: "ui-monospace, monospace", fontSize: 11 }}>
                    {c.fingerprint_hash.slice(0, 12)}…
                  </code>
                </Td>
                <Td>
                  <NameEditor
                    initial={c.proposed_name ?? ""}
                    onCommit={(name) => onRename(c.fingerprint_hash, name)}
                  />
                </Td>
                <Td align="right">{c.frequency}×</Td>
                <Td align="right">{c.file_count}</Td>
                <Td align="right" muted>
                  {c.stability_score.toFixed(2)}
                </Td>
                <Td align="right" muted>
                  {(c.atom_reuse_rate * 100).toFixed(0)}%
                </Td>
                <Td align="right" style={{ fontWeight: 500 }}>
                  {c.composite_score.toFixed(1)}
                </Td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </section>
  );
}

function NameEditor({
  initial,
  onCommit,
}: {
  initial: string;
  onCommit: (name: string) => Promise<void>;
}) {
  const [value, setValue] = useState(initial);
  const [status, setStatus] = useState<"idle" | "saving" | "saved" | "error">("idle");
  // Mirror prop changes (refreshes after fetch) only when not actively editing.
  useEffect(() => {
    if (status === "idle") setValue(initial);
  }, [initial, status]);

  const commit = async () => {
    if (value === initial) return;
    setStatus("saving");
    try {
      await onCommit(value.trim());
      setStatus("saved");
      setTimeout(() => setStatus("idle"), 1200);
    } catch {
      setStatus("error");
    }
  };

  return (
    <span style={{ display: "inline-flex", alignItems: "center", gap: 6 }}>
      <input
        type="text"
        value={value}
        onChange={(e) => setValue(e.target.value)}
        onBlur={() => void commit()}
        onKeyDown={(e) => {
          if (e.key === "Enter") (e.target as HTMLInputElement).blur();
          if (e.key === "Escape") {
            setValue(initial);
            (e.target as HTMLInputElement).blur();
          }
        }}
        placeholder="Click to name…"
        style={{
          minWidth: 160,
          padding: "3px 6px",
          fontSize: 12,
          background: status === "error" ? "rgba(220, 38, 38, 0.08)" : "transparent",
          border: "1px solid transparent",
          borderRadius: 3,
          color: value ? "#5eead4" : "#666",
          fontFamily: "inherit",
          outline: "none",
        }}
        onFocus={(e) =>
          (e.target.style.borderColor = "#444")
        }
        onBlurCapture={(e) =>
          ((e.target as HTMLInputElement).style.borderColor = "transparent")
        }
      />
      {status === "saving" && <span style={{ fontSize: 10, color: "#888" }}>saving…</span>}
      {status === "saved" && <span style={{ fontSize: 10, color: "#16a34a" }}>✓</span>}
      {status === "error" && <span style={{ fontSize: 10, color: "#dc2626" }}>err</span>}
    </span>
  );
}

// ─── Tiny styled cells ───────────────────────────────────────────────────────

function Th({
  children,
  align,
}: {
  children: React.ReactNode;
  align?: "left" | "right" | "center";
}) {
  return (
    <th
      style={{
        textAlign: align ?? "left",
        padding: "8px 12px",
        fontWeight: 500,
        color: "#aaa",
        fontSize: 11,
        textTransform: "uppercase",
        letterSpacing: 0.5,
      }}
    >
      {children}
    </th>
  );
}

function Td({
  children,
  align,
  muted,
  colSpan,
  style,
}: {
  children: React.ReactNode;
  align?: "left" | "right" | "center";
  muted?: boolean;
  colSpan?: number;
  style?: React.CSSProperties;
}) {
  return (
    <td
      colSpan={colSpan}
      style={{
        padding: "10px 12px",
        textAlign: align ?? "left",
        color: muted ? "#666" : undefined,
        ...style,
      }}
    >
      {children}
    </td>
  );
}
