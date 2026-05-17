"use client";

/**
 * CanvasShell — picks the renderer for the center canvas based on the
 * sub_flow's KTD-8 lifecycle.
 *
 *   empty           → "attach prototype or wait for design" empty state
 *   proto-only      → PrototypeCanvas, no banner
 *   proto-wip       → PrototypeCanvas + "designer is working on this section" banner
 *   design-shipped  → FrameGrid (rendered frame thumbnails / placeholders)
 *
 * The auto-swap requirement (live transition from proto-* to design-shipped
 * on figma.design_shipped SSE) is implemented one level up in PRDShell:
 * the SSE handler bumps a refetch key → section.inspect re-runs → this
 * component re-renders with the new lifecycle. No state lives here.
 */

import type { Lifecycle, WallRow } from "./types";
import { PrototypeCanvas } from "./PrototypeCanvas";
import { FrameThumbnail } from "./FrameThumbnail";
import { useFrameThumbTokens } from "./useFrameThumbToken";

interface Props {
  lifecycle: Lifecycle;
  prototypeUrl: string | null;
  prototypeTitle: string | null;
  // Wall rows minus orphans — the frame-shipped canvas only renders
  // live frames. Caller filters; we just render.
  frames: WallRow[];
  slug: string;
  /** Figma file key for the bound section (U1 of plan 2026-05-17-004).
   *  Threaded through to <FrameGrid> for real-PNG rendering. */
  fileKey?: string;
}

export function CanvasShell({
  lifecycle,
  prototypeUrl,
  prototypeTitle,
  frames,
  slug,
  fileKey,
}: Props) {
  switch (lifecycle) {
    case "empty":
      return (
        <div className="canvas-empty">
          <strong>No canvas yet</strong>
          <span>
            Attach an HTML prototype with{" "}
            <code>/ind-prd {slug} attach-prototype &lt;https://…&gt;</code>,
            or wait for the designer to ship a Figma section.
          </span>
          <style jsx>{`
            .canvas-empty {
              padding: 48px 16px;
              text-align: center;
              border: 1px dashed var(--border, rgba(255, 255, 255, 0.12));
              border-radius: 8px;
              color: var(--text-3);
              font-size: 13px;
              display: flex;
              flex-direction: column;
              gap: 8px;
              align-items: center;
            }
            .canvas-empty strong {
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
              color: var(--text-2);
            }
          `}</style>
        </div>
      );
    case "proto-only":
      return (
        <PrototypeCanvas
          url={prototypeUrl ?? ""}
          title={prototypeTitle}
          banner={null}
        />
      );
    case "proto-wip":
      return (
        <PrototypeCanvas
          url={prototypeUrl ?? ""}
          title={prototypeTitle}
          banner="Designer is working on this section — final designs not yet shipped."
        />
      );
    case "design-shipped":
      return <FrameGrid frames={frames} fileKey={fileKey} />;
    default:
      // Defensive — surface the unknown lifecycle so future enum additions
      // are loud rather than silently rendering nothing.
      return (
        <div className="canvas-empty">
          Unknown canvas lifecycle: <code>{lifecycle}</code>
        </div>
      );
  }
}

// ─── FrameGrid (design-shipped) ──────────────────────────────────────────
//
// Renders the wall frames as a compact strip across the canvas slot. The
// full wall (with counts + binding badges) lives in Wall.tsx; this is the
// canvas-y view: just the frames, in Figma section order.
//
// U1 (plan 2026-05-17-004): real PNG thumbnails via /api/figma/frame-png.
// FrameGrid mints one asset token per (file_key, node_id, scale=1) via
// useFrameThumbTokens; <FrameThumbnail> falls back to the v1 placeholder
// glyph until its token resolves so the canvas never renders blank.

function FrameGrid({
  frames,
  fileKey,
}: {
  frames: WallRow[];
  fileKey?: string;
}) {
  const nodeIDs = frames
    .map((f) => f.figma_node_id)
    .filter((id): id is string => !!id);
  const { tokenQSFor } = useFrameThumbTokens(fileKey, nodeIDs);

  if (frames.length === 0) {
    return (
      <div className="frame-grid frame-grid--empty">
        Section bound but no frames detected yet. Re-sync Figma to refresh.
        <style jsx>{`
          .frame-grid--empty {
            padding: 32px;
            text-align: center;
            color: var(--text-3);
            font-size: 13px;
            border: 1px dashed var(--border, rgba(255, 255, 255, 0.12));
            border-radius: 8px;
          }
        `}</style>
      </div>
    );
  }
  return (
    <div className="frame-grid">
      {frames.map((f) => {
        const tokenQS = tokenQSFor(f.figma_node_id);
        const canRenderRealThumb = !!fileKey && !!f.figma_node_id && !!tokenQS;
        return (
          <article
            key={`${f.figma_node_id || f.frame_name}`}
            className={`frame-grid__card frame-grid__card--${f.binding_status}`}
          >
            <div className="frame-grid__thumb">
              {canRenderRealThumb ? (
                <FrameThumbnail
                  fileKey={fileKey!}
                  figmaNodeID={f.figma_node_id}
                  alt={f.frame_name || "frame"}
                  assetTokenQS={tokenQS}
                  width="100%"
                  height="100%"
                />
              ) : (
                <span aria-hidden>{f.has_render ? "▣" : "□"}</span>
              )}
            </div>
            <div className="frame-grid__name" title={f.frame_name}>
              {f.frame_name}
            </div>
            <div className="frame-grid__badge">
              {f.binding_status === "bound" ? "bound" : "untagged"}
            </div>
          </article>
        );
      })}
      <style jsx>{`
        .frame-grid {
          display: grid;
          grid-template-columns: repeat(auto-fill, minmax(180px, 1fr));
          gap: 12px;
        }
        .frame-grid__card {
          border: 1px solid var(--border, rgba(255, 255, 255, 0.08));
          border-radius: 8px;
          padding: 10px;
          background: var(--surface-1, rgba(255, 255, 255, 0.02));
          display: flex;
          flex-direction: column;
          gap: 6px;
          min-height: 140px;
        }
        .frame-grid__card--bound {
          border-color: rgba(80, 200, 120, 0.25);
        }
        .frame-grid__card--untagged {
          border-color: rgba(255, 179, 71, 0.25);
        }
        .frame-grid__thumb {
          height: 96px;
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
        .frame-grid__name {
          font-size: 12px;
          color: var(--text-1);
          white-space: nowrap;
          overflow: hidden;
          text-overflow: ellipsis;
        }
        .frame-grid__badge {
          font-size: 10px;
          letter-spacing: 0.04em;
          text-transform: uppercase;
          color: var(--text-3);
        }
      `}</style>
    </div>
  );
}
