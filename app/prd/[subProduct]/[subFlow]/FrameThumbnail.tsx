"use client";

/**
 * FrameThumbnail — renders a real Figma frame PNG inside the PRD viewer's
 * Wall + FrameGrid (U1 of plan 2026-05-17-004).
 *
 * Replaces the placeholder glyph rendering (▣ / □) that v1 of the viewer
 * shipped with. The bytes hit the new /v1/figma/frame-png endpoint via the
 * same-origin Next.js proxy at /api/figma/frame-png, which forwards an
 * asset token minted server-side by the PRDShell on mount.
 *
 * States:
 *   - loading  → grey-pulse skeleton (matches Wall.tsx's surface-1 background)
 *   - errored  → fallback glyph + alt text, preserving the v1 visual contract
 *   - loaded   → fade-in PNG via opacity transition
 *
 * The wrapper preserves the parent's box (height/width) so layout doesn't
 * jitter between load states.
 */

import { useState } from "react";

interface Props {
  fileKey: string;
  figmaNodeID: string;
  alt: string;
  /** Asset token + tenant query string, minted server-side. Both included
   *  verbatim — the helper that produces them owns the URL shape. */
  assetTokenQS: string;
  scale?: 1 | 2;
  width?: number | string;
  height?: number | string;
  className?: string;
}

export function FrameThumbnail({
  fileKey,
  figmaNodeID,
  alt,
  assetTokenQS,
  scale = 1,
  width,
  height,
  className,
}: Props) {
  const [loaded, setLoaded] = useState(false);
  const [errored, setErrored] = useState(false);

  // The asset token already carries the tenant + file/node binding in its
  // MAC; we still re-attach the same parameters to the URL so the server
  // can route + verify. assetTokenQS is opaque to this component — comes
  // from /api/figma/frame-png-token's response.url field.
  const src =
    `/api/figma/frame-png?file_key=${encodeURIComponent(fileKey)}` +
    `&node_id=${encodeURIComponent(figmaNodeID)}` +
    `&scale=${scale}` +
    `&${assetTokenQS}`;

  if (errored || !assetTokenQS) {
    // Fallback — preserve the placeholder-glyph aesthetic the v1 viewer
    // used so the wall doesn't look broken when a frame fails to render.
    return (
      <div
        className={`frame-thumb frame-thumb--errored ${className ?? ""}`}
        style={{ width, height }}
        aria-label={alt}
        role="img"
      >
        <span aria-hidden>□</span>
        <style jsx>{`
          .frame-thumb--errored {
            display: flex;
            align-items: center;
            justify-content: center;
            color: var(--text-3);
            font-size: 28px;
            background: linear-gradient(
              135deg,
              rgba(255, 255, 255, 0.03),
              rgba(255, 255, 255, 0.08)
            );
            border-radius: 4px;
          }
        `}</style>
      </div>
    );
  }

  return (
    <div
      className={`frame-thumb ${className ?? ""}`}
      style={{ width, height, position: "relative" }}
    >
      {!loaded && (
        <div className="frame-thumb__skeleton" aria-hidden>
          <style jsx>{`
            .frame-thumb__skeleton {
              position: absolute;
              inset: 0;
              background: var(--surface-1, rgba(255, 255, 255, 0.04));
              border-radius: 4px;
              animation: frame-thumb-pulse 1.6s ease-in-out infinite;
            }
            @keyframes frame-thumb-pulse {
              0%,
              100% {
                opacity: 0.6;
              }
              50% {
                opacity: 0.9;
              }
            }
          `}</style>
        </div>
      )}
      {/* eslint-disable-next-line @next/next/no-img-element */}
      <img
        src={src}
        alt={alt}
        loading="lazy"
        decoding="async"
        onLoad={() => setLoaded(true)}
        onError={() => setErrored(true)}
        style={{
          width: "100%",
          height: "100%",
          objectFit: "cover",
          borderRadius: 4,
          opacity: loaded ? 1 : 0,
          transition: "opacity 180ms ease-out",
          display: "block",
        }}
      />
    </div>
  );
}
