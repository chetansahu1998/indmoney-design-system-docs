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

// ─── Bridge: builders ────────────────────────────────────────────────────────

function bridgeBuildLeafCanvas(leaf: any) {
  if (!leaf) return originalBuildLeafCanvas?.(leaf) ?? { frames: [], edges: [] };
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
        // `kind` carries through to PhoneFrame for legacy compat — we
        // attach pngUrl so the PhoneFrame wrapper below renders the real
        // image regardless of what `kind` says.
        kind: "real",
        label: f.label,
        pngUrl: f.pngUrl,
      })),
      edges: slot.edges,
    };
  }
  return originalBuildLeafCanvas?.(leaf) ?? { frames: [], edges: [] };
}

function bridgeBuildViolations(leaf: any) {
  if (!leaf) return originalBuildViolations?.(leaf) ?? [];
  const slot = snapshot().leafSlots[leaf.id];
  if (slot && slot.overlays.violations.length >= 0 && slot.loadedAt > 0) {
    return slot.overlays.violations.map((v) => ({
      id: v.id,
      severity: v.severity,
      rule: v.rule,
      ruleId: v.ruleId,
      layer: v.layer,
      frameIdx: v.frameIdx,
      status: v.status,
      detail: v.detail,
      ago: v.ago,
    }));
  }
  return originalBuildViolations?.(leaf) ?? [];
}

function bridgeBuildDecisions(leaf: any) {
  if (!leaf) return originalBuildDecisions?.(leaf) ?? [];
  const slot = snapshot().leafSlots[leaf.id];
  if (slot && slot.loadedAt > 0) {
    return slot.overlays.decisions.map((d) => ({
      id: d.id,
      title: d.title,
      body: d.body,
      author: d.author,
      ago: d.ago,
      linksTo: d.linksTo,
    }));
  }
  return originalBuildDecisions?.(leaf) ?? [];
}

function bridgeBuildActivity(leaf: any) {
  if (!leaf) return originalBuildActivity?.(leaf) ?? [];
  const slot = snapshot().leafSlots[leaf.id];
  if (slot && slot.loadedAt > 0) {
    return slot.overlays.activity.map((a) => ({
      who: a.who,
      what: a.what,
      ago: a.ago,
      kind: a.kind,
    }));
  }
  return originalBuildActivity?.(leaf) ?? [];
}

function bridgeBuildComments(leaf: any) {
  if (!leaf) return originalBuildComments?.(leaf) ?? [];
  const slot = snapshot().leafSlots[leaf.id];
  if (slot && slot.loadedAt > 0) {
    return slot.overlays.comments.map((c) => ({
      who: c.who,
      body: c.body,
      ago: c.ago,
      reactions: c.reactions,
    }));
  }
  return originalBuildComments?.(leaf) ?? [];
}

// ─── Bridge: PhoneFrame ──────────────────────────────────────────────────────

function PhoneFrameWrapper(props: any) {
  const frame = props.frame;

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
