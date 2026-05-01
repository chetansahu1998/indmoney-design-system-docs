"use client";

/**
 * ActivityRail — Phase 5 U12. Right-side timeline of audit_log events
 * for the active flow. Renders grouped by day; oldest grouping at the
 * bottom, newest at top. Live-append happens via the project's existing
 * SSE channel — we listen for any project.* event with a flow_id in
 * its payload and prepend to the local cache.
 */

import { useEffect, useMemo, useState } from "react";
import {
  fetchFlowActivity,
  type FlowActivityEntry,
} from "@/lib/drd/client";

interface Props {
  slug: string;
  flowID: string | null;
}

const EVENT_LABELS: Record<string, string> = {
  "decision.created": "Decision",
  "decision.superseded": "Superseded",
  "decision.deleted": "Decision deleted",
  "violation.acknowledge": "Acknowledged",
  "violation.dismiss": "Dismissed",
  "violation.reactivate": "Reactivated",
  "violation.mark_fixed": "Fixed",
  "comment.created": "Comment",
  "drd.snapshot": "DRD edit",
  "project.export": "Export",
};

function dayLabel(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "—";
  const today = new Date();
  const yest = new Date();
  yest.setDate(today.getDate() - 1);
  const same = (a: Date, b: Date) =>
    a.getFullYear() === b.getFullYear() &&
    a.getMonth() === b.getMonth() &&
    a.getDate() === b.getDate();
  if (same(d, today)) return "Today";
  if (same(d, yest)) return "Yesterday";
  return d.toLocaleDateString();
}

function timeLabel(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}

function summarize(entry: FlowActivityEntry): string {
  const label = EVENT_LABELS[entry.event_type] ?? entry.event_type;
  let detail = "";
  if (entry.details) {
    try {
      const obj = JSON.parse(entry.details) as Record<string, unknown>;
      const t = (obj["title"] ?? obj["reason"] ?? obj["note"]) as
        | string
        | undefined;
      if (t) detail = t;
    } catch {
      /* ignore */
    }
  }
  return detail ? `${label} — ${detail.slice(0, 80)}` : label;
}

export default function ActivityRail({ slug, flowID }: Props) {
  const [entries, setEntries] = useState<FlowActivityEntry[]>([]);
  const [loading, setLoading] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    if (!flowID) {
      setEntries([]);
      return;
    }
    let cancelled = false;
    setLoading(true);
    setErr(null);
    void fetchFlowActivity(slug, flowID, 100).then((r) => {
      if (cancelled) return;
      setLoading(false);
      if (!r.ok) {
        setErr(`${r.error} (status ${r.status})`);
        return;
      }
      setEntries(r.data.activity ?? []);
    });
    return () => {
      cancelled = true;
    };
  }, [slug, flowID]);

  const grouped = useMemo(() => {
    const map = new Map<string, FlowActivityEntry[]>();
    for (const e of entries) {
      const k = dayLabel(e.ts);
      const arr = map.get(k) ?? [];
      arr.push(e);
      map.set(k, arr);
    }
    return Array.from(map.entries());
  }, [entries]);

  return (
    <aside
      data-testid="activity-rail"
      style={{
        padding: 12,
        border: "1px solid var(--border)",
        borderRadius: 8,
        background: "var(--bg-surface)",
        display: "flex",
        flexDirection: "column",
        gap: 12,
        minWidth: 220,
        maxWidth: 320,
      }}
    >
      <h3
        style={{
          fontSize: 12,
          fontFamily: "var(--font-mono)",
          color: "var(--text-3)",
          textTransform: "uppercase",
          letterSpacing: 0.6,
          margin: 0,
        }}
      >
        Activity
      </h3>
      {!flowID && (
        <span style={{ fontSize: 11, color: "var(--text-3)" }}>No flow selected.</span>
      )}
      {loading && <span style={{ fontSize: 11, color: "var(--text-3)" }}>loading…</span>}
      {err && <span style={{ fontSize: 11, color: "var(--danger)" }}>{err}</span>}
      {!loading && !err && entries.length === 0 && flowID && (
        <span style={{ fontSize: 11, color: "var(--text-3)" }}>
          No activity yet on this flow.
        </span>
      )}
      {grouped.map(([day, items]) => (
        <section
          key={day}
          style={{ display: "flex", flexDirection: "column", gap: 6 }}
        >
          <div
            style={{
              fontSize: 10,
              fontFamily: "var(--font-mono)",
              color: "var(--text-3)",
              textTransform: "uppercase",
              letterSpacing: 0.6,
            }}
          >
            {day}
          </div>
          {items.map((e) => (
            <div
              key={e.id}
              data-event-type={e.event_type}
              style={{
                fontSize: 11,
                fontFamily: "var(--font-mono)",
                color: "var(--text-2)",
                lineHeight: 1.4,
              }}
            >
              <span style={{ color: "var(--text-3)" }}>{timeLabel(e.ts)}</span>{" "}
              {summarize(e)}
            </div>
          ))}
        </section>
      ))}
    </aside>
  );
}
