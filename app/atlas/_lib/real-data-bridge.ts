// @ts-nocheck
"use client";

/**
 * real-data-bridge.ts — Phase 5 wiring layer.
 *
 * The ported reference UI calls window.buildLeafCanvas / buildViolations /
 * buildDecisions / buildActivity / buildComments / PhoneFrame. Originally
 * those returned mock data. This bridge:
 *
 *   1. Saves the originals for fallback (standalone preview, sparse data).
 *   2. Replaces each one with a delegating wrapper that reads the live
 *      atlas store for real data when present, otherwise calls the mock.
 *   3. Patches window.PhoneFrame to render the real screen PNG when the
 *      frame object carries a `pngUrl`; falls through to the original
 *      mock phone-screen dispatcher for standalone preview.
 *
 * Imported once from AtlasShellInner — order matters, must come AFTER the
 * leaves/frames modules but BEFORE the AtlasApp/LeafCanvas first render.
 */

import React from "react";

import { useAtlas } from "../../../lib/atlas/live-store";
import { LeafFrameRenderer } from "./leafcanvas-v2/LeafFrameRenderer";

// ─── Originals captured for fallback ─────────────────────────────────────────

const W: any = typeof window !== "undefined" ? (window as any) : {};

const originalBuildLeafCanvas = W.buildLeafCanvas;
const originalBuildViolations = W.buildViolations;
const originalBuildDecisions = W.buildDecisions;
const originalBuildActivity = W.buildActivity;
const originalBuildComments = W.buildComments;
const originalPhoneFrame = W.PhoneFrame;

// Snapshot helper — read live-store outside React (these wrappers are
// called from inside the ported components' render functions, where
// hooks aren't available).
function snapshot() {
  return useAtlas.getState();
}

// Real-data-only mode: when the live store is hydrated, we never return
// mock seeded data. This guarantees the right inspector reflects what's
// actually in ds-service — even during the brief window before a leaf's
// overlay slot finishes loading. Falling back to mocks before would
// render fake "Aanya P. edited DRD 2h ago" rows that look real but are
// invented; users couldn't tell the difference.
function inLiveMode(): boolean {
  return typeof window !== "undefined" && !!(window as any).__ATLAS_DATA_READY;
}

// ─── Bridge: builders ────────────────────────────────────────────────────────

function bridgeBuildLeafCanvas(leaf: any) {
  if (!leaf) return inLiveMode() ? { frames: [], edges: [] } : (originalBuildLeafCanvas?.(leaf) ?? { frames: [], edges: [] });
  const slot = snapshot().leafSlots[leaf.id];
  if (slot && slot.frames.length > 0) {
    return {
      frames: slot.frames.map((f) => ({
        id: f.id,
        idx: f.idx,
        x: f.x,
        y: f.y,
        w: f.w,
        h: f.h,
        kind: "real",
        label: f.label,
        pngUrl: f.pngUrl,
      })),
      edges: slot.edges,
    };
  }
  // Live mode + slot still loading → return empty (renderer shows skeleton).
  // Standalone preview → keep showing the mock so the HTML stays demo-able.
  if (inLiveMode()) return { frames: [], edges: [] };
  return originalBuildLeafCanvas?.(leaf) ?? { frames: [], edges: [] };
}

function bridgeBuildViolations(leaf: any) {
  if (!leaf || inLiveMode()) {
    const slot = leaf ? snapshot().leafSlots[leaf.id] : null;
    if (slot && slot.loadedAt > 0) {
      return slot.overlays.violations.map((v) => ({
        id: v.id, severity: v.severity, rule: v.rule, ruleId: v.ruleId,
        layer: v.layer, frameIdx: v.frameIdx, status: v.status,
        detail: v.detail, ago: v.ago,
      }));
    }
    return []; // live mode never returns mocks
  }
  return originalBuildViolations?.(leaf) ?? [];
}

function bridgeBuildDecisions(leaf: any) {
  if (!leaf || inLiveMode()) {
    const slot = leaf ? snapshot().leafSlots[leaf.id] : null;
    if (slot && slot.loadedAt > 0) {
      return slot.overlays.decisions.map((d) => ({
        id: d.id, title: d.title, body: d.body, author: d.author,
        ago: d.ago, linksTo: d.linksTo,
      }));
    }
    return [];
  }
  return originalBuildDecisions?.(leaf) ?? [];
}

function bridgeBuildActivity(leaf: any) {
  if (!leaf || inLiveMode()) {
    const slot = leaf ? snapshot().leafSlots[leaf.id] : null;
    if (slot && slot.loadedAt > 0) {
      return slot.overlays.activity.map((a) => ({
        who: a.who, what: a.what, ago: a.ago, kind: a.kind,
      }));
    }
    return [];
  }
  return originalBuildActivity?.(leaf) ?? [];
}

function bridgeBuildComments(leaf: any) {
  if (!leaf || inLiveMode()) {
    const slot = leaf ? snapshot().leafSlots[leaf.id] : null;
    if (slot && slot.loadedAt > 0) {
      return slot.overlays.comments.map((c) => ({
        who: c.who, body: c.body, ago: c.ago, reactions: c.reactions,
      }));
    }
    return [];
  }
  return originalBuildComments?.(leaf) ?? [];
}

// ─── Bridge: PhoneFrame ──────────────────────────────────────────────────────

/**
 * Read the leaf-canvas v2 flag once per call. Env var is the public flag;
 * a window-level override exists so storybook / preview environments can
 * flip it without rebuilding the bundle.
 */
function leafCanvasV2Enabled(): boolean {
  if (typeof process !== "undefined" && process.env?.NEXT_PUBLIC_LEAFCANVAS_V2 === "1") return true;
  if (typeof window !== "undefined" && (window as any).__LEAFCANVAS_V2 === true) return true;
  return false;
}

/**
 * Find the active leaf slug for a frame so the v2 renderer can pull the
 * right canonical_tree. Walks the live store: leaf id == project slug
 * post brain-products migration, and frame.id == screens.id.
 */
function findSlugForFrame(frameID: string): string | null {
  const state = snapshot();
  const sel = state.selection;
  if (sel.leafID && state.leafSlots[sel.leafID]?.frames.some((f: any) => f.id === frameID)) {
    return sel.leafID;
  }
  for (const [leafID, slot] of Object.entries(state.leafSlots)) {
    if ((slot as any).frames?.some((f: any) => f.id === frameID)) return leafID;
  }
  return null;
}

function PhoneFrameWrapper(props: any) {
  const frame = props.frame;

  // Path 0 — Canvas v2 flag on AND we have a real frame: try the
  // strict-TS LeafFrameRenderer. The renderer falls back to a transparent
  // skeleton when canonical_tree is null, so we layer it ON TOP of the
  // PNG rather than replacing it. That way sheet-sync screens (where the
  // tree may not exist yet) keep their PNG underlay.
  if (leafCanvasV2Enabled() && frame && frame.kind === "real") {
    const slug = findSlugForFrame(frame.id);
    if (slug) {
      return React.createElement(
        "div",
        { className: "ph-screen ph-screen--v2", style: { position: "relative", width: "100%", height: "100%" } },
        // PNG underlay (only if available) so the user sees something
        // immediately while the canonical_tree resolves. The v2 renderer
        // sits on top and takes over once filtered/rendered.
        frame.pngUrl
          ? React.createElement("img", {
              src: frame.pngUrl,
              alt: frame.label || "Screen",
              loading: "lazy",
              decoding: "async",
              draggable: false,
              style: { position: "absolute", inset: 0, width: "100%", height: "100%", objectFit: "cover", display: "block" },
            })
          : null,
        React.createElement(LeafFrameRenderer, {
          slug,
          screenID: frame.id,
          label: frame.label,
          width: frame.w ?? 280,
          height: frame.h ?? 580,
        }),
      );
    }
    // No slug match → fall through to the legacy PNG path below.
  }

  // Path 1 — real-screen with rendered PNG: image-load.
  if (frame && typeof frame.pngUrl === "string" && frame.pngUrl) {
    return React.createElement(
      "div",
      { className: "ph-screen ph-screen--real" },
      React.createElement("img", {
        src: frame.pngUrl,
        alt: frame.label || "Screen",
        loading: "lazy",
        decoding: "async",
        draggable: false,
        style: { width: "100%", height: "100%", objectFit: "cover", display: "block" },
        onError: (e: any) => {
          // PNG 404s (race with the render pipeline). Replace with the
          // pending-render placeholder rather than leaving a broken image.
          const el = e?.currentTarget?.parentElement;
          if (el) {
            el.classList.remove("ph-screen--real");
            el.classList.add("ph-screen--pending");
            el.innerHTML = pendingMarkup(frame?.label);
          }
        },
      }),
    );
  }

  // Path 2 — real-data leaf but PNG not yet rendered. Show an honest
  // placeholder rather than a fake phone screen, so users can tell the
  // difference between "fake mock data" and "real flow, render in flight".
  if (frame && frame.kind === "real") {
    return React.createElement(
      "div",
      { className: "ph-screen ph-screen--pending", dangerouslySetInnerHTML: { __html: pendingMarkup(frame?.label) } },
    );
  }

  // Path 3 — fallback to the original mock dispatcher (used only by the
  // standalone HTML preview where no live data is wired).
  if (typeof originalPhoneFrame === "function") {
    return React.createElement(originalPhoneFrame, props);
  }
  return null;
}

function pendingMarkup(label?: string): string {
  const safe = (label ?? "Rendering").replace(/[<>&"']/g, (c) =>
    ({ "<": "&lt;", ">": "&gt;", "&": "&amp;", '"': "&quot;", "'": "&#39;" }[c] as string),
  );
  return `
    <div class="ph-pending-shell">
      <div class="ph-pending-spinner"></div>
      <div class="ph-pending-label">${safe}</div>
      <div class="ph-pending-sub">Rendering from Figma…</div>
    </div>
  `;
}

// ─── Install ────────────────────────────────────────────────────────────────

if (typeof window !== "undefined") {
  W.buildLeafCanvas = bridgeBuildLeafCanvas;
  W.buildViolations = bridgeBuildViolations;
  W.buildDecisions = bridgeBuildDecisions;
  W.buildActivity = bridgeBuildActivity;
  W.buildComments = bridgeBuildComments;
  W.PhoneFrame = PhoneFrameWrapper;
}

export {};
