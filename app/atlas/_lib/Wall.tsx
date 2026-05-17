"use client";

/**
 * Wall — corkboard view of the coverage wall for an Atlas leaf's sub_flow.
 *
 * Plan 005 U7 — relocated from app/prd/[subProduct]/[subFlow]/Wall.tsx and
 * adapted so types + thumbnail hook resolve through lib/atlas. Visual layout
 * and counts strip are unchanged from the legacy viewer; only imports and
 * the WallCard click-to-canvas handler (new) are new.
 *
 *   ┌────────────────────────────────────────────────┐
 *   │ 8 frames · 5 bound · 3 untagged · 62% covered  │  ← counts strip
 *   ├────────────────────────────────────────────────┤
 *   │ [card] [card] [card] [card]                    │  ← bound + untagged
 *   ├────────────────────────────────────────────────┤
 *   │ Orphans                                        │
 *   │ [card] [card]                                  │
 *   └────────────────────────────────────────────────┘
 *
 * Clicking a card flips the leaf back to canvas mode and focuses the
 * corresponding frame — bidirectional navigation.
 */

import type {
  WallResult,
  WallRow,
  BindingStatus,
} from "../../../lib/atlas/types";
import { FrameThumbnail } from "./FrameThumbnail";
import { useFrameThumbTokens } from "../../../lib/atlas/figma-frame-tokens";

interface Props {
  data: WallResult;
  slug: string;
  /** Figma file key for the bound section; absent for pre-binding sub_flows. */
  fileKey?: string;
  /**
   * Called when a frame card is clicked. Atlas's center-pane swap uses
   * this to flip back to canvas mode and focus the frame.
   */
  onFrameClick?: (figmaNodeID: string) => void;
}

export function Wall({ data, slug, fileKey, onFrameClick }: Props) {
  const nodeIDs = data.frames
    .map((r) => r.figma_node_id)
    .filter((id): id is string => !!id);
  const { tokenQSFor } = useFrameThumbTokens(fileKey, nodeIDs);

  if (data.frames.length === 0) {
    return (
      <div className="lc-wall-empty">
        <strong>No frames yet</strong>
        <span>
          The bound Figma section has no direct-child frames the autosync
          could see, or no section is bound yet. Either attach a prototype
          via <code>/ind-prd {slug} attach-prototype …</code> or wait for the
          designer to ship the section.
        </span>
      </div>
    );
  }

  const live: WallRow[] = [];
  const orphans: WallRow[] = [];
  for (const r of data.frames) {
    if (r.binding_status === "orphaned") orphans.push(r);
    else live.push(r);
  }

  return (
    <div className="lc-wall">
      <header className="lc-wall-counts" aria-label="Coverage summary">
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
        <span className="lc-wall-pct">
          <strong>{data.counts.coverage_percent}%</strong> covered
        </span>
      </header>

      <section className="lc-wall-grid">
        {live.map((row) => (
          <WallCard
            key={cardKey(row)}
            row={row}
            slug={slug}
            fileKey={fileKey}
            tokenQS={tokenQSFor(row.figma_node_id)}
            onClick={onFrameClick}
          />
        ))}
      </section>

      {orphans.length > 0 && (
        <section className="lc-wall-orphans">
          <h3>Orphans</h3>
          <p>
            PRD states whose frame name no longer matches a frame in the
            bound section. Rename or detach.
          </p>
          <div className="lc-wall-grid">
            {orphans.map((row) => (
              <WallCard
                key={cardKey(row)}
                row={row}
                slug={slug}
                fileKey={fileKey}
                tokenQS={tokenQSFor(row.figma_node_id)}
                onClick={onFrameClick}
              />
            ))}
          </div>
        </section>
      )}
    </div>
  );
}

function cardKey(row: WallRow): string {
  if (row.figma_node_id) return `frame:${row.figma_node_id}`;
  if (row.prd_state_id) return `state:${row.prd_state_id}`;
  return `name:${row.frame_name}`;
}

function WallCard({
  row,
  slug,
  fileKey,
  tokenQS,
  onClick,
}: {
  row: WallRow;
  slug: string;
  fileKey?: string;
  tokenQS: string;
  onClick?: (figmaNodeID: string) => void;
}) {
  const tone: BindingStatus = row.binding_status;
  const canRenderRealThumb = !!fileKey && !!row.figma_node_id && !!tokenQS;
  const isClickable = !!onClick && !!row.figma_node_id;
  const Tag: any = isClickable ? "button" : "article";
  return (
    <Tag
      className={`lc-wall-card lc-wall-card--${tone}${isClickable ? " lc-wall-card--clickable" : ""}`}
      onClick={isClickable ? () => onClick!(row.figma_node_id) : undefined}
      type={isClickable ? "button" : undefined}
    >
      <div className="lc-wall-card-thumb">
        {canRenderRealThumb ? (
          <FrameThumbnail
            fileKey={fileKey!}
            figmaNodeID={row.figma_node_id}
            alt={row.frame_name || "frame"}
            assetTokenQS={tokenQS}
            width="100%"
            height="100%"
          />
        ) : (
          <span aria-hidden>{row.has_render ? "▣" : "□"}</span>
        )}
      </div>
      <div className="lc-wall-card-head">
        <div className="lc-wall-card-name" title={row.frame_name || "(no frame name)"}>
          {row.frame_name || <em>(orphan)</em>}
        </div>
        <span className={`lc-wall-card-badge lc-wall-card-badge--${tone}`}>{tone}</span>
      </div>
      {row.prd_state_label && (
        <div className="lc-wall-card-label">{row.prd_state_label}</div>
      )}
      <dl className="lc-wall-card-counts">
        <Stat label="criteria" value={row.criteria_count} />
        <Stat label="events" value={row.events_count} />
        <Stat label="copy" value={row.copy_count} />
        <Stat label="edge" value={row.edge_cases_count} />
        <Stat label="a11y" value={row.a11y_count} />
        <Stat label="words" value={row.total_word_count} />
      </dl>
      {row.last_touched_by && row.last_touched_at && (
        <div className="lc-wall-card-touched">
          {row.last_touched_by} · {timeAgo(row.last_touched_at)}
        </div>
      )}
      {tone === "untagged" && (
        <div className="lc-wall-card-cta">
          <code>/ind-prd {slug} add-state &quot;{row.frame_name}&quot;</code>
        </div>
      )}
    </Tag>
  );
}

function Stat({ label, value }: { label: string; value: number }) {
  return (
    <div className={`lc-wall-card-stat ${value === 0 ? "lc-wall-card-stat--zero" : ""}`}>
      <dt>{label}</dt>
      <dd>{value}</dd>
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
