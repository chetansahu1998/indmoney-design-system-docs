"use client";

/**
 * DRDAnchorChips — plan 005 Phase B+ polish.
 *
 * Renders a small "[→ S3]" badge floating to the right of every DRD
 * block that has at least one drd_anchor row. Lives outside BlockNote's
 * editor tree so ProseMirror's DOM diff doesn't strip the chip on
 * re-render — same overlay technique used by the pulse animation.
 *
 * Re-fetches when the anchor table changes (via `atlas:anchors-changed`
 * window event the slash command fires after a successful attach), and
 * re-positions when the scroll host scrolls or the window resizes.
 */

import { useEffect, useRef, useState } from "react";

interface DRDAnchor {
  id: string;
  block_id: string;
  screen_id: string;
}

interface Props {
  /** Sub-flow slug ("indstocks/unified-watchlist-screener"). When
   *  missing or malformed, the chip layer renders nothing. */
  subFlowSlug?: string | null;
}

export function DRDAnchorChips({ subFlowSlug }: Props) {
  const [anchors, setAnchors] = useState<DRDAnchor[]>([]);
  const chipsLayerRef = useRef<HTMLDivElement | null>(null);
  const rafRef = useRef<number | null>(null);

  // 1. Load anchors from MCP. Re-runs when sub_flow changes or the
  //    `atlas:anchors-changed` event fires.
  useEffect(() => {
    if (!subFlowSlug || !subFlowSlug.includes("/")) {
      setAnchors([]);
      return;
    }
    let cancelled = false;
    function load() {
      const dsBase =
        (process.env.NEXT_PUBLIC_DS_SERVICE_URL as string | undefined) ??
        "http://localhost:8080";
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
        body: JSON.stringify({ sub_flow_slug: subFlowSlug }),
      })
        .then((r) => (r.ok ? r.json() : null))
        .then((body) => {
          if (cancelled) return;
          setAnchors((body?.data?.anchors as DRDAnchor[]) ?? []);
        })
        .catch(() => {
          if (!cancelled) setAnchors([]);
        });
    }
    load();
    const refresh = () => load();
    window.addEventListener("atlas:anchors-changed", refresh);
    return () => {
      cancelled = true;
      window.removeEventListener("atlas:anchors-changed", refresh);
    };
  }, [subFlowSlug]);

  // 2. On every anchor-set change, hook scroll + resize to re-paint
  //    chips. The chips themselves live in a single layer div that we
  //    mount under the DRD editor host. We don't render React children
  //    for the chips because we'd then have to thread React refs into
  //    sibling DOM (a portal would work but adds complexity); a tiny
  //    imperative renderer is enough.
  useEffect(() => {
    if (anchors.length === 0) return;
    const drdHost = document.querySelector(
      ".lc-ins-drd-host, .lc-drd-editor-host",
    ) as HTMLElement | null;
    if (!drdHost) return;
    const scrollHost =
      (drdHost.querySelector(".lc-drd-editor-host") as HTMLElement | null) ??
      drdHost;
    if (getComputedStyle(scrollHost).position === "static") {
      scrollHost.style.position = "relative";
    }
    // Single owned layer div the chips live in. Clears on each render.
    let layer = scrollHost.querySelector<HTMLDivElement>(
      ".lc-drd-anchor-chip-layer",
    );
    if (!layer) {
      layer = document.createElement("div");
      layer.className = "lc-drd-anchor-chip-layer";
      scrollHost.appendChild(layer);
    }
    chipsLayerRef.current = layer;

    function paint() {
      if (!layer) return;
      layer.innerHTML = "";
      // Group anchors by block_id so a block with multiple screens
      // renders a stacked chip ("→ S3, S7, S8") instead of overlapping
      // badges.
      const byBlock = new Map<string, string[]>();
      for (const a of anchors) {
        const arr = byBlock.get(a.block_id) ?? [];
        arr.push(a.screen_id);
        byBlock.set(a.block_id, arr);
      }
      const hostRect = scrollHost.getBoundingClientRect();
      for (const [blockId, screenIds] of byBlock) {
        const block = scrollHost.querySelector<HTMLElement>(
          `.bn-block[data-id="${cssEscape(blockId)}"]`,
        );
        if (!block) continue;
        const br = block.getBoundingClientRect();
        const chip = document.createElement("div");
        chip.className = "lc-drd-anchor-chip";
        chip.textContent =
          "→ " + screenIds.sort(naturalCmp).join(", ");
        chip.title = `Anchored to ${screenIds.length} prototype screen${screenIds.length === 1 ? "" : "s"}`;
        chip.style.top = `${br.top - hostRect.top + scrollHost.scrollTop + 4}px`;
        // Position chip flush to the right edge of the block, slightly
        // outside so it doesn't overlap text.
        chip.style.left = `${br.right - hostRect.left + scrollHost.scrollLeft + 8}px`;
        layer.appendChild(chip);
      }
    }

    function schedule() {
      if (rafRef.current != null) cancelAnimationFrame(rafRef.current);
      rafRef.current = requestAnimationFrame(() => {
        rafRef.current = null;
        paint();
      });
    }

    // Initial paint — give BlockNote a tick to lay out blocks first.
    schedule();
    scrollHost.addEventListener("scroll", schedule, { passive: true });
    window.addEventListener("resize", schedule);
    // BlockNote re-renders blocks on edit; observe the editor mutations
    // so chips reposition when blocks shift. Throttled via rAF above.
    const obs = new MutationObserver(schedule);
    obs.observe(scrollHost, { childList: true, subtree: true });

    return () => {
      scrollHost.removeEventListener("scroll", schedule);
      window.removeEventListener("resize", schedule);
      obs.disconnect();
      if (rafRef.current != null) cancelAnimationFrame(rafRef.current);
      layer?.remove();
      chipsLayerRef.current = null;
    };
  }, [anchors]);

  return null;
}

function cssEscape(s: string): string {
  return s.replace(/[^a-zA-Z0-9_-]/g, (c) =>
    "\\" + c.charCodeAt(0).toString(16) + " ",
  );
}

function naturalCmp(a: string, b: string): number {
  // Sort "S2" before "S10".
  const am = a.match(/^([A-Za-z]+)(\d+)$/);
  const bm = b.match(/^([A-Za-z]+)(\d+)$/);
  if (am && bm && am[1] === bm[1]) {
    return parseInt(am[2], 10) - parseInt(bm[2], 10);
  }
  return a.localeCompare(b);
}
