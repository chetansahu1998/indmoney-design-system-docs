"use client";

/**
 * Atlas empty-state for "no flows extracted on this platform yet".
 *
 * Plan 2026-05-03-001 / T9. Until the Figma plugin grows a web extraction
 * target, `?platform=web` against any tenant returns zero flows — the brain
 * canvas would otherwise paint as a black void with no signal that the
 * data isn't there yet. This panel makes the empty case explicit and
 * routes the user back to the populated platform.
 */

import type { Platform } from "../../lib/atlas/types";

export interface NoPlatformFlowsProps {
  platform: Platform;
  /** Switch the live store + URL to the opposite platform. */
  onSwitchPlatform: (next: Platform) => void;
}

export default function NoPlatformFlows({
  platform,
  onSwitchPlatform,
}: NoPlatformFlowsProps) {
  const other: Platform = platform === "web" ? "mobile" : "web";
  return (
    <div className="atlas-root atlas-root--empty" role="status" aria-live="polite">
      <div className="atlas-empty-card">
        <h2 className="atlas-empty-title">No {platform} flows extracted yet</h2>
        <p className="atlas-empty-body">
          The Figma plugin hasn&rsquo;t exported any {platform} flows for this
          tenant. Toggle to <strong>{other}</strong> to see the existing atlas, or
          run the plugin against a {platform} Figma section to populate it.
        </p>
        <div className="atlas-empty-actions">
          <button
            type="button"
            className="atlas-empty-btn atlas-empty-btn--primary"
            onClick={() => onSwitchPlatform(other)}
          >
            Switch to {other}
          </button>
          <a
            className="atlas-empty-btn"
            href="https://www.figma.com/community"
            target="_blank"
            rel="noopener noreferrer"
          >
            Open plugin
          </a>
        </div>
      </div>
    </div>
  );
}
