"use client";

/**
 * PrototypeAnchorBridge — plan 005 Phase A.
 *
 * Mounted alongside <PrototypeCanvas> in AtlasShellInner. Wires the
 * postMessage contract between the prototype iframe and Atlas's DRD tab:
 *
 *   prototype → atlas
 *     {source:"indmoney-prototype", type:"screen:click", screenId, label}
 *       → find a DRD block whose text matches the label/screenId and
 *         scroll + flash it in the right-rail inspector.
 *     {source:"indmoney-prototype", type:"hello", screens:[{screenId,label}]}
 *       → reply with {source:"atlas", type:"hello?"} so a late mount
 *         still gets the screen catalogue (used by Phase B's anchor
 *         picker).
 *
 *   atlas → prototype (Phase C — out of scope for now, but the channel
 *   is symmetric so reverse focus works as soon as we wire it.)
 *
 * Anchor resolution strategy (Phase A — heuristic):
 *   1. Look for a DRD heading whose text contains the screenId verbatim
 *      ("S3" / "s3").
 *   2. Fall back to label substring match (case-insensitive).
 *   3. Fall back to no match — emit a console.info, no error.
 *
 * Phase B will replace this with a first-class drd_anchor lookup
 * (block_id → screen_id) stored server-side. The heuristic exists so
 * authors who haven't anchored anything yet still get useful behaviour.
 */

import { useEffect, useRef } from "react";

const PROTOTYPE_SOURCE = "indmoney-prototype";
const ATLAS_SOURCE = "atlas";

interface DRDAnchor {
  block_id: string;
  screen_id: string;
}

interface PrototypeMessage {
  source: string;
  type: string;
  screenId?: string;
  label?: string;
  screens?: Array<{ screenId: string; label: string }>;
  bridgeVersion?: number;
}

interface Props {
  /** Ref to the iframe we send focus messages back to. Atlas validates
   *  incoming messages by `event.source === iframe.contentWindow`, so
   *  the ref is required for trust + for the reverse channel. */
  iframeRef: React.RefObject<HTMLIFrameElement | null>;
  /** Sub-flow slug ("indstocks/unified-watchlist-screener"). Used to
   *  fetch first-class drd_anchor rows on mount. When the lookup yields
   *  a hit for the clicked screen id, we anchor by block_id directly
   *  (Phase B); otherwise we fall back to the Phase A heuristic. */
  subFlowSlug?: string;
}

export function PrototypeAnchorBridge({ iframeRef, subFlowSlug }: Props) {
  // First-class anchors fetched once on mount. Stored in a ref so the
  // message handler closure always reads the latest map without re-
  // subscribing (which would tear down the postMessage listener every
  // time anchors load).
  const anchorsRef = useRef<DRDAnchor[]>([]);

  useEffect(() => {
    if (!subFlowSlug || !subFlowSlug.includes("/")) return;
    let cancelled = false;
    const [sp, sf] = subFlowSlug.split("/");
    const dsBase =
      (process.env.NEXT_PUBLIC_DS_SERVICE_URL as string | undefined) ??
      "http://localhost:8080";
    // Read the token from the same persisted zustand slot AtlasShell uses.
    let token = "";
    try {
      const raw = localStorage.getItem("indmoney-ds-auth");
      if (raw) token = JSON.parse(raw)?.state?.token ?? "";
    } catch {
      /* ignore */
    }
    fetch(`${dsBase}/v1/mcp/invoke/drd.list_anchors`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        ...(token ? { Authorization: `Bearer ${token}` } : {}),
      },
      body: JSON.stringify({ sub_flow_slug: `${sp}/${sf}` }),
    })
      .then((r) => (r.ok ? r.json() : null))
      .then((body) => {
        if (cancelled || !body?.data?.anchors) return;
        anchorsRef.current = body.data.anchors as DRDAnchor[];
      })
      .catch(() => {
        /* heuristic fallback handles the empty case */
      });
    return () => {
      cancelled = true;
    };
  }, [subFlowSlug]);

  useEffect(() => {
    function onMessage(event: MessageEvent) {
      const data = (event && event.data) as PrototypeMessage | null;
      if (!data || typeof data !== "object") return;
      if (data.source !== PROTOTYPE_SOURCE) return;

      // Trust gate: only accept messages from OUR iframe window. Without
      // this, any cross-origin frame on the page could spoof prototypes
      // and hijack DRD navigation.
      if (iframeRef.current && event.source !== iframeRef.current.contentWindow) {
        return;
      }

      if (data.type === "screen:click" && data.screenId) {
        // Phase B — first-class anchor lookup. If the screen id has one
        // or more anchored blocks, jump directly. The bridge picks the
        // first match; future polish can rotate through multiple
        // anchors on repeat clicks.
        const direct = anchorsRef.current.find(
          (a) => a.screen_id === data.screenId,
        );
        if (direct) {
          anchorToBlockId(direct.block_id, data.screenId!);
        } else {
          // Phase A fallback — heuristic by label-token match. Useful
          // for prototypes that haven't been anchored yet.
          anchorToBlock(data.screenId, data.label ?? "");
        }
      }
      if (data.type === "hello") {
        // The bridge announces itself + its screen catalogue. Phase B
        // reads this off a custom event so the slash-menu picker can
        // populate.
        window.dispatchEvent(
          new CustomEvent("atlas:prototype-hello", { detail: data }),
        );
      }
    }
    window.addEventListener("message", onMessage);
    // Send the hello? handshake so a prototype that loaded before this
    // bridge mounted re-emits its catalogue.
    queueMicrotask(() => {
      const w = iframeRef.current?.contentWindow;
      if (w) {
        try {
          w.postMessage({ source: ATLAS_SOURCE, type: "hello?" }, "*");
        } catch {
          /* iframe might still be loading */
        }
      }
    });
    return () => window.removeEventListener("message", onMessage);
  }, [iframeRef]);

  return null;
}

/**
 * Walk the DRD editor DOM and find the best-matching block for a screen
 * click. Heuristic: prefer a heading whose text contains the screenId,
 * else fall back to the first heading whose text contains a label token.
 *
 * BlockNote renders blocks as <div class="bn-block" data-id="<uuid>"> with
 * <div class="bn-block-content" data-content-type="heading|paragraph|…"
 * data-level="1|2|3"> children. We walk those, score them, and pick the
 * top result.
 */
function anchorToBlock(screenId: string, label: string) {
  const drdHost = document.querySelector(".lc-ins-drd-host, .lc-drd-editor-host");
  if (!drdHost) {
    // DRD tab not mounted (PM is on PRD/Activity/Comments). Silently no-op.
    return;
  }
  const blocks = Array.from(drdHost.querySelectorAll<HTMLElement>(".bn-block"));
  if (blocks.length === 0) return;

  const sid = screenId.toLowerCase();
  // Word-boundary match for the screen id so "s3" doesn't fire on the
  // run-together table text "s1my watchliststs2screener…" (every screen
  // id is mentioned in the Screen Inventory + Mixpanel tables).
  const sidRe = new RegExp(`\\b${escapeRegExp(sid)}\\b`);
  const tokens = (label ?? "")
    .toLowerCase()
    .split(/[\s—–\-·:•()]+/)
    .filter((t) => t.length >= 4 && !STOP_WORDS.has(t));

  let best: HTMLElement | null = null;
  let bestScore = 0;
  for (const blk of blocks) {
    const txt = (blk.textContent || "").toLowerCase();
    const content = blk.querySelector(".bn-block-content");
    const ctype = content?.getAttribute("data-content-type") ?? "";
    // Skip tables outright — their text concatenates every cell, so a
    // 4-row Mixpanel-events table will match every screen id and outvote
    // any specific heading. Tables aren't the right anchor target anyway;
    // they're reference data, not section markers.
    if (ctype === "table") continue;

    let score = 0;
    if (sidRe.test(txt)) score += 10;
    for (const t of tokens) if (txt.includes(t)) score += 2;
    if (ctype === "heading") {
      score += 4;
      // Higher-level headings (H1/H2) are stronger section markers than
      // H3 sub-sections, but only when they actually match a token.
      const level = parseInt(content?.getAttribute("data-level") ?? "3", 10);
      if (level <= 2 && score > 0) score += 1;
    }
    if (score > bestScore) {
      bestScore = score;
      best = blk;
    }
  }
  if (!best || bestScore < 4) {
    // eslint-disable-next-line no-console
    console.info(
      `[atlas-anchor] no match for ${screenId} "${label}" — author needs /anchor (Phase B)`,
    );
    return;
  }

  // Make sure the DRD tab is the active one before scrolling — otherwise
  // we'd scroll a hidden pane. Click the DRD tab button if present.
  const drdTab = document.querySelector<HTMLButtonElement>(
    '.lc-ins-tab[class*="is-active"]',
  );
  if (drdTab && !drdTab.textContent?.includes("DRD")) {
    const drdBtn = Array.from(
      document.querySelectorAll<HTMLButtonElement>(".lc-ins-tab"),
    ).find((b) => b.textContent?.trim() === "DRD");
    drdBtn?.click();
  }

  pulseOverlay(drdHost as HTMLElement, best, screenId);
  best.scrollIntoView({ behavior: "smooth", block: "center" });
}

/**
 * Phase B — direct block-id lookup. The drd_anchor row stores BlockNote's
 * block UUID; we look up the matching `.bn-block[data-id=...]` element
 * and pulse it. Falls back to the Phase A heuristic only when the
 * anchor table has no entry for the screen id.
 */
function anchorToBlockId(blockId: string, screenId: string) {
  const drdHost = document.querySelector(".lc-ins-drd-host, .lc-drd-editor-host");
  if (!drdHost) return;
  const block = drdHost.querySelector<HTMLElement>(
    `.bn-block[data-id="${cssEscape(blockId)}"]`,
  );
  if (!block) {
    // Anchored to a block that no longer exists (deleted in a recent
    // edit). Fall through to heuristic so the click still does
    // something useful.
    return;
  }
  pulseOverlay(drdHost as HTMLElement, block, screenId);
  block.scrollIntoView({ behavior: "smooth", block: "center" });
}

function cssEscape(s: string): string {
  return s.replace(/[^a-zA-Z0-9_-]/g, (c) => "\\" + c.charCodeAt(0).toString(16) + " ");
}

/**
 * Shared overlay-pulse helper. Used by both anchorToBlock (heuristic)
 * and anchorToBlockId (direct). The overlay lives in
 * .lc-drd-editor-host as a sibling so ProseMirror's DOM-diff doesn't
 * strip the pulse class.
 */
function pulseOverlay(
  drdHost: HTMLElement,
  target: HTMLElement,
  screenId: string,
) {
  const scrollHost =
    (drdHost.querySelector(".lc-drd-editor-host") as HTMLElement | null) ??
    drdHost;
  if (getComputedStyle(scrollHost).position === "static") {
    scrollHost.style.position = "relative";
  }
  scrollHost.querySelectorAll(".lc-drd-anchor-overlay").forEach((n) => n.remove());
  const hostRect = scrollHost.getBoundingClientRect();
  const blockRect = target.getBoundingClientRect();
  const overlay = document.createElement("div");
  overlay.className = "lc-drd-anchor-overlay lc-drd-anchor-pulse";
  overlay.setAttribute("data-atlas-anchored", screenId);
  overlay.style.top = `${blockRect.top - hostRect.top + scrollHost.scrollTop - 4}px`;
  overlay.style.left = `${blockRect.left - hostRect.left + scrollHost.scrollLeft - 8}px`;
  overlay.style.width = `${blockRect.width + 16}px`;
  overlay.style.height = `${blockRect.height + 8}px`;
  scrollHost.appendChild(overlay);
  window.setTimeout(() => overlay.remove(), 1700);
}

function escapeRegExp(s: string) {
  return s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

const STOP_WORDS = new Set([
  "the",
  "with",
  "from",
  "into",
  "this",
  "that",
  "view",
  "mode",
  "sheet",
  "page",
  "state",
  "screen",
  "based",
  "list",
]);
