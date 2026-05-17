"use client";

/**
 * app/atlas/_lib/FrameThumbnail.tsx — Atlas-side shared thumbnail component.
 *
 * Plan 005 U2 + U7 — both the new PRDTab (inside leafcanvas.tsx) and the
 * future Wall mode need to render real Figma frame PNGs via the
 * /api/figma/frame-png proxy. Centralising here keeps token-mint usage
 * and loading-state behavior consistent.
 *
 * Behaviour mirrors the original viewer-side FrameThumbnail (which is
 * deleted by U8) but uses Atlas's `lc-prd-thumb*` className tokens so the
 * visuals slot into the LeafInspector tab body without conflicting with
 * the legacy /prd viewer CSS.
 *
 * States:
 *   - no fileKey OR no nodeID → static placeholder glyph
 *   - token not yet minted    → skeleton box (pulse animation in CSS)
 *   - PNG loading             → skeleton box; image fades in onLoad
 *   - errored                 → placeholder glyph + alt
 */

import { useState } from "react";

import { useFrameThumbToken } from "@/lib/atlas/figma-frame-tokens";

interface Props {
  fileKey?: string;
  figmaNodeID?: string;
  alt: string;
  scale?: 1 | 2;
  width?: number | string;
  height?: number | string;
  /**
   * Plan 005 U7 — optional pre-minted token. When the caller already
   * batched a token mint for many node IDs (e.g. Wall.tsx using
   * useFrameThumbTokens), they pass the per-frame query string here so
   * we don't fire an extra mint per card. Falls back to the singular
   * self-mint hook when absent.
   */
  assetTokenQS?: string;
}

export function FrameThumbnail({
  fileKey,
  figmaNodeID,
  alt,
  scale = 1,
  width,
  height,
  assetTokenQS: pre,
}: Props) {
  const selfMinted = useFrameThumbToken(fileKey, figmaNodeID, scale);
  const assetTokenQS = pre ?? selfMinted;
  const [loaded, setLoaded] = useState(false);
  const [errored, setErrored] = useState(false);

  // No binding metadata — render the static placeholder. Same affordance
  // as the legacy viewer used for "no frame to fetch".
  if (!fileKey || !figmaNodeID || errored) {
    return (
      <div
        className="lc-prd-thumb lc-prd-thumb-fallback"
        style={{ width, height }}
        aria-label={alt}
        role="img"
      >
        <span aria-hidden>▣</span>
      </div>
    );
  }

  // Token not minted yet (or empty after a server fail). Show the
  // skeleton; the hook will populate the token on next tick.
  if (!assetTokenQS) {
    return (
      <div
        className="lc-prd-thumb lc-prd-thumb-skel"
        style={{ width, height }}
        aria-label={alt}
        role="img"
      />
    );
  }

  const src =
    `/api/figma/frame-png?file_key=${encodeURIComponent(fileKey)}` +
    `&node_id=${encodeURIComponent(figmaNodeID)}` +
    `&scale=${scale}` +
    `&${assetTokenQS}`;

  return (
    <div
      className="lc-prd-thumb"
      style={{ width, height, position: "relative" }}
    >
      {!loaded && <div className="lc-prd-thumb-skel" aria-hidden />}
      {/* eslint-disable-next-line @next/next/no-img-element */}
      <img
        src={src}
        alt={alt}
        loading="lazy"
        decoding="async"
        onLoad={() => setLoaded(true)}
        onError={() => setErrored(true)}
        className="lc-prd-thumb-img"
        style={{ opacity: loaded ? 1 : 0 }}
      />
    </div>
  );
}
