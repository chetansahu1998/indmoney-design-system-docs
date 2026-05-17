"use client";

/**
 * PrototypeCanvas — sandboxed iframe slot for an HTML prototype (KTD-8).
 *
 * Defence-in-depth:
 *   - URL must be HTTPS at render time. ds-service already validates this
 *     at AttachPrototype (subflow_prototype.go), but a stale row or future
 *     code path could feed us http:// here; bail early.
 *   - `sandbox="allow-scripts allow-same-origin allow-forms"` matches the
 *     plan body (line 779). `allow-popups` is intentionally absent — we
 *     don't want the prototype opening tabs into the viewer origin.
 *   - `referrerpolicy="no-referrer"` keeps the docs-site origin out of
 *     the prototype's request logs.
 *
 * The banner row carries the lifecycle hint ("designer is working on this
 * section" for proto-wip). When null, the iframe fills the whole slot.
 */

interface Props {
  url: string;
  title?: string | null;
  banner: string | null;
}

export function PrototypeCanvas({ url, title, banner }: Props) {
  const safe = url.startsWith("https://");
  if (!safe) {
    return (
      <div className="proto-canvas proto-canvas--error">
        Prototype URL must be HTTPS.{" "}
        <a href={url} target="_blank" rel="noreferrer noopener">
          Open in new tab
        </a>
        <style jsx>{`
          .proto-canvas {
            padding: 24px;
            color: var(--text-2);
            font-size: 14px;
            border: 1px solid var(--border, rgba(255, 255, 255, 0.08));
            border-radius: 8px;
          }
          .proto-canvas a {
            color: var(--accent);
          }
        `}</style>
      </div>
    );
  }
  return (
    <div className="proto-canvas">
      {banner && (
        <div className="proto-canvas__banner" role="status">
          {banner}
        </div>
      )}
      <iframe
        src={url}
        title={title ?? "Prototype"}
        sandbox="allow-scripts allow-same-origin allow-forms"
        referrerPolicy="no-referrer"
        loading="lazy"
        className={banner ? "proto-canvas__iframe with-banner" : "proto-canvas__iframe"}
      />
      <div className="proto-canvas__footer">
        <a href={url} target="_blank" rel="noreferrer noopener">
          Open prototype in new tab ↗
        </a>
      </div>
      <style jsx>{`
        .proto-canvas {
          position: relative;
          width: 100%;
          height: 60vh;
          min-height: 420px;
          border: 1px solid var(--border, rgba(255, 255, 255, 0.08));
          border-radius: 8px;
          overflow: hidden;
          background: var(--surface-1, rgba(255, 255, 255, 0.02));
          display: flex;
          flex-direction: column;
        }
        .proto-canvas__banner {
          padding: 8px 14px;
          font-size: 12px;
          color: var(--warning-fg, #1a1300);
          background: var(--warning-soft, #ffe9a8);
          border-bottom: 1px solid var(--border, rgba(0, 0, 0, 0.1));
          letter-spacing: 0.01em;
        }
        .proto-canvas__iframe {
          flex: 1;
          width: 100%;
          border: 0;
          background: #fff;
        }
        .proto-canvas__footer {
          padding: 6px 12px;
          font-size: 11px;
          background: var(--surface-1, rgba(255, 255, 255, 0.02));
          border-top: 1px solid var(--border, rgba(255, 255, 255, 0.08));
          text-align: right;
        }
        .proto-canvas__footer a {
          color: var(--text-3);
          text-decoration: none;
        }
        .proto-canvas__footer a:hover {
          color: var(--accent);
        }
      `}</style>
    </div>
  );
}
