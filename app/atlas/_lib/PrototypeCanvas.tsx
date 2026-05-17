"use client";

/**
 * PrototypeCanvas — Atlas center-pane mode for PM-authored HTML prototypes
 * (plan 005 U6 / KTD-4).
 *
 * Renders the sub_flow's attached prototype URL inside a sandboxed iframe
 * when the leaf's `canvasLifecycle` is `proto-only` or `proto-wip`. When
 * autosync fires `figma.design_shipped`, the live store refetches the
 * sub_flow; lifecycle flips to `design-shipped`; AtlasShellInner's render
 * branch swaps back to <LeafCanvas /> automatically — no page reload.
 *
 * Defence-in-depth:
 *   - URL must be HTTPS at render time. ds-service validates this at
 *     AttachPrototype (subflow_prototype.go U3b), but a stale row or a
 *     future code path could feed us http:// here; bail early.
 *   - `sandbox="allow-scripts allow-same-origin allow-forms"` matches plan
 *     005 KTD-4 (and plan 002 line 779). `allow-popups` is intentionally
 *     absent — we don't want the prototype opening tabs into the viewer's
 *     secure context.
 *   - `referrerpolicy="no-referrer"` keeps the docs-site origin out of the
 *     prototype's request logs.
 *
 * Class names follow Atlas's `lc-proto-*` convention so the styling lives
 * in `app/atlas/_styles/leafcanvas.css` alongside the other inspector
 * panels. The previous /prd-route copy at
 * app/prd/[subProduct]/[subFlow]/PrototypeCanvas.tsx used styled-jsx; here
 * we route through Atlas's stylesheet so the iframe inherits Atlas's
 * surface tokens.
 */

import React from "react";

interface Props {
  url: string;
  title?: string | null;
  banner: string | null;
}

export function PrototypeCanvas({ url, title, banner }: Props) {
  const safe = url.startsWith("https://");
  if (!safe) {
    return (
      <div className="lc-proto-error" role="alert">
        <div className="lc-proto-error-h">Prototype URL must be HTTPS</div>
        <div className="lc-proto-error-sub">Got: {url}</div>
        <a
          className="lc-proto-error-link"
          href={url}
          target="_blank"
          rel="noreferrer noopener"
        >
          Open in new tab ↗
        </a>
      </div>
    );
  }
  return (
    <div className="lc-proto">
      {banner && (
        <div className="lc-proto-banner" role="status">
          {banner}
        </div>
      )}
      <iframe
        src={url}
        title={title ?? "Prototype"}
        sandbox="allow-scripts allow-same-origin allow-forms"
        referrerPolicy="no-referrer"
        loading="lazy"
        className={
          banner
            ? "lc-proto-iframe lc-proto-iframe--with-banner"
            : "lc-proto-iframe"
        }
      />
      <div className="lc-proto-footer">
        <a href={url} target="_blank" rel="noreferrer noopener">
          Open prototype in new tab ↗
        </a>
      </div>
    </div>
  );
}
