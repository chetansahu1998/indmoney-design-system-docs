"use client";

/**
 * Wall — corkboard view of the U6b WallResult.
 *
 * Renders every frame in the bound Figma section, joined to the PRD state
 * that owns it (if any), with per-stem counts and a "last touched" stamp.
 * Orphans (deleted_at states, frame_name mismatches) surface at the bottom
 * so PMs can rename / detach in one place.
 *
 * Visual:
 *   ┌────────────────────────────────────────────────┐
 *   │ 8 frames · 5 bound · 3 untagged · 62% covered  │  ← counts strip
 *   ├────────────────────────────────────────────────┤
 *   │ [card] [card] [card] [card]                    │  ← bound + untagged
 *   │ [card] [card] [card] [card]                    │
 *   ├────────────────────────────────────────────────┤
 *   │ Orphans                                        │
 *   │ [card] [card]                                  │
 *   └────────────────────────────────────────────────┘
 *
 * Card colour reflects binding_status. Untagged cards get a CTA to open
 * Claude (skill invocation pre-fills sub_flow_slug + state_label hint).
 */

import type { WallResult, WallRow, BindingStatus } from "./types";

interface Props {
  data: WallResult;
  slug: string;
}

export function Wall({ data, slug }: Props) {
  if (data.frames.length === 0) {
    return (
      <div className="wall-empty">
        <strong>No frames yet</strong>
        <span>
          The bound Figma section has no direct-child frames the autosync
          could see, or no section is bound yet. Either attach a prototype
          via <code>/ind-prd {slug} attach-prototype …</code> or wait for the
          designer to ship the section.
        </span>
        <style jsx>{`
          .wall-empty {
            padding: 48px 16px;
            text-align: center;
            color: var(--text-3);
            font-size: 13px;
            display: flex;
            flex-direction: column;
            gap: 8px;
            align-items: center;
          }
          .wall-empty strong {
            color: var(--text-1);
            font-weight: 600;
            font-size: 14px;
          }
          code {
            font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
            background: var(--surface-1, rgba(255, 255, 255, 0.04));
            padding: 1px 6px;
            border-radius: 4px;
            font-size: 12px;
          }
        `}</style>
      </div>
    );
  }

  // Split frames into live (bound + untagged) vs orphaned for separate
  // rendering. WallResult already returns them in canvas-y order so we
  // don't re-sort — we just partition.
  const live: WallRow[] = [];
  const orphans: WallRow[] = [];
  for (const r of data.frames) {
    if (r.binding_status === "orphaned") orphans.push(r);
    else live.push(r);
  }

  return (
    <div className="wall">
      <header className="wall__counts" aria-label="Coverage summary">
        <span>
          <strong>{data.counts.total}</strong> frames
        </span>
        <span>
          <strong>{data.counts.bound}</strong> bound
        </span>
        <span>
          <strong>{data.counts.untagged}</strong> untagged
        </span>
        {data.counts.orphaned > 0 && (
          <span>
            <strong>{data.counts.orphaned}</strong> orphaned
          </span>
        )}
        <span className="wall__pct">
          <strong>{data.counts.coverage_percent}%</strong> covered
        </span>
      </header>

      <section className="wall__grid">
        {live.map((row) => (
          <WallCard key={cardKey(row)} row={row} slug={slug} />
        ))}
      </section>

      {orphans.length > 0 && (
        <section className="wall__orphans">
          <h3>Orphans</h3>
          <p>
            PRD states whose frame name no longer matches a frame in the
            bound section. Rename or detach.
          </p>
          <div className="wall__grid">
            {orphans.map((row) => (
              <WallCard key={cardKey(row)} row={row} slug={slug} />
            ))}
          </div>
        </section>
      )}

      <style jsx>{`
        .wall {
          display: flex;
          flex-direction: column;
          gap: 16px;
        }
        .wall__counts {
          display: flex;
          gap: 16px;
          padding: 10px 12px;
          background: var(--surface-1, rgba(255, 255, 255, 0.02));
          border: 1px solid var(--border, rgba(255, 255, 255, 0.08));
          border-radius: 8px;
          font-size: 12px;
          color: var(--text-3);
          font-variant-numeric: tabular-nums;
        }
        .wall__counts strong {
          color: var(--text-1);
          font-weight: 600;
          margin-right: 4px;
        }
        .wall__pct {
          margin-left: auto;
        }
        .wall__grid {
          display: grid;
          grid-template-columns: repeat(auto-fill, minmax(220px, 1fr));
          gap: 12px;
        }
        .wall__orphans h3 {
          margin: 16px 0 4px;
          font-size: 12px;
          letter-spacing: 0.06em;
          text-transform: uppercase;
          color: var(--text-2);
        }
        .wall__orphans p {
          margin: 0 0 10px;
          font-size: 12px;
          color: var(--text-3);
        }
      `}</style>
    </div>
  );
}

function cardKey(row: WallRow): string {
  // figma_node_id is unique within a section; orphans have it == "" so
  // fall back to (state_id || frame_name).
  if (row.figma_node_id) return `frame:${row.figma_node_id}`;
  if (row.prd_state_id) return `state:${row.prd_state_id}`;
  return `name:${row.frame_name}`;
}

// ─── WallCard ──────────────────────────────────────────────────────────────

function WallCard({ row, slug }: { row: WallRow; slug: string }) {
  const tone: BindingStatus = row.binding_status;
  return (
    <article className={`card card--${tone}`}>
      <div className="card__thumb">
        <span aria-hidden>{row.has_render ? "▣" : "□"}</span>
      </div>
      <div className="card__head">
        <div className="card__name" title={row.frame_name || "(no frame name)"}>
          {row.frame_name || <em>(orphan)</em>}
        </div>
        <span className={`card__badge card__badge--${tone}`}>{tone}</span>
      </div>
      {row.prd_state_label && (
        <div className="card__label">{row.prd_state_label}</div>
      )}
      <dl className="card__counts">
        <Stat label="criteria" value={row.criteria_count} />
        <Stat label="events" value={row.events_count} />
        <Stat label="copy" value={row.copy_count} />
        <Stat label="edge" value={row.edge_cases_count} />
        <Stat label="a11y" value={row.a11y_count} />
        <Stat label="words" value={row.total_word_count} />
      </dl>
      {row.last_touched_by && row.last_touched_at && (
        <div className="card__touched">
          {row.last_touched_by} · {timeAgo(row.last_touched_at)}
        </div>
      )}
      {tone === "untagged" && (
        <div className="card__cta">
          <code>
            /ind-prd {slug} add-state &quot;{row.frame_name}&quot;
          </code>
        </div>
      )}
      <style jsx>{`
        .card {
          display: flex;
          flex-direction: column;
          gap: 8px;
          padding: 12px;
          background: var(--surface-1, rgba(255, 255, 255, 0.02));
          border: 1px solid var(--border, rgba(255, 255, 255, 0.08));
          border-radius: 8px;
          font-size: 12px;
        }
        .card--bound {
          border-color: rgba(80, 200, 120, 0.3);
        }
        .card--untagged {
          border-color: rgba(255, 179, 71, 0.3);
        }
        .card--orphaned {
          border-color: rgba(220, 80, 80, 0.3);
          background: rgba(220, 80, 80, 0.04);
        }
        .card__thumb {
          height: 92px;
          background: linear-gradient(
            135deg,
            rgba(255, 255, 255, 0.03),
            rgba(255, 255, 255, 0.08)
          );
          border-radius: 4px;
          display: flex;
          align-items: center;
          justify-content: center;
          color: var(--text-3);
          font-size: 28px;
        }
        .card__head {
          display: flex;
          justify-content: space-between;
          align-items: flex-start;
          gap: 8px;
        }
        .card__name {
          color: var(--text-1);
          font-weight: 500;
          font-size: 13px;
          min-width: 0;
          overflow: hidden;
          text-overflow: ellipsis;
          white-space: nowrap;
        }
        .card__name em {
          color: var(--text-3);
          font-style: italic;
        }
        .card__badge {
          font-size: 9px;
          font-weight: 700;
          letter-spacing: 0.06em;
          text-transform: uppercase;
          padding: 2px 6px;
          border-radius: 999px;
          background: var(--surface-1);
          color: var(--text-2);
        }
        .card__badge--bound {
          background: rgba(80, 200, 120, 0.15);
          color: rgb(80, 200, 120);
        }
        .card__badge--untagged {
          background: rgba(255, 179, 71, 0.15);
          color: rgb(255, 179, 71);
        }
        .card__badge--orphaned {
          background: rgba(220, 80, 80, 0.15);
          color: rgb(220, 80, 80);
        }
        .card__label {
          color: var(--text-2);
          font-size: 11px;
        }
        .card__counts {
          display: grid;
          grid-template-columns: repeat(3, 1fr);
          gap: 4px 6px;
          margin: 0;
          font-variant-numeric: tabular-nums;
        }
        .card__touched {
          font-size: 10px;
          color: var(--text-3);
        }
        .card__cta {
          padding-top: 4px;
          border-top: 1px dashed var(--border, rgba(255, 255, 255, 0.08));
        }
        .card__cta code {
          font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
          font-size: 10px;
          color: var(--text-3);
          white-space: nowrap;
          overflow: hidden;
          text-overflow: ellipsis;
          display: block;
        }
      `}</style>
    </article>
  );
}

function Stat({ label, value }: { label: string; value: number }) {
  return (
    <div className={`stat ${value === 0 ? "stat--zero" : ""}`}>
      <dt>{label}</dt>
      <dd>{value}</dd>
      <style jsx>{`
        .stat {
          display: flex;
          flex-direction: row;
          gap: 4px;
          align-items: baseline;
          font-size: 10px;
        }
        .stat dt {
          color: var(--text-3);
          letter-spacing: 0.02em;
          margin: 0;
        }
        .stat dd {
          margin: 0;
          color: var(--text-1);
          font-weight: 600;
        }
        .stat--zero dd {
          color: var(--text-3);
          font-weight: 400;
        }
      `}</style>
    </div>
  );
}

function timeAgo(iso: string): string {
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return iso;
  const sec = Math.max(0, (Date.now() - t) / 1000);
  if (sec < 60) return "just now";
  if (sec < 3600) return `${Math.floor(sec / 60)}m ago`;
  if (sec < 86400) return `${Math.floor(sec / 3600)}h ago`;
  return `${Math.floor(sec / 86400)}d ago`;
}
