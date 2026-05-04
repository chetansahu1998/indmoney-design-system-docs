// @ts-nocheck
"use client";
// Ported verbatim from INDmoney Docs/atlas.jsx; preserves pixel-identical
// visuals. Type tightening tracked in Phase 8 cleanup.
import React, { useEffect, useRef, useState, useMemo, useCallback } from "react";

// ============================================================
// DATA — flows organized by "lobes" of the brain silhouette.
//
// Phase 4 wiring: AtlasShell hydrates lib/atlas/live-store.ts and writes
// the resolved DOMAINS / FLOWS / SYNAPSES into window.__ATLAS_* globals
// BEFORE this module is imported. We read those globals here when present
// so the brain renders real production data on first paint. The mock
// arrays below remain as the fallback for two cases:
//   1. Standalone preview of `INDmoney Docs/The Atlas.html` (no shell).
//   2. ds-service unreachable / first-launch with no projects.
// ============================================================

const __ATLAS_DOMAINS_MOCK = [
  { id: "markets",            label: "Markets",            sub: "Trade & invest",    lobe: "frontalL" },
  { id: "money_matters",      label: "Money Matters",      sub: "Pay, save, plan",   lobe: "frontalR" },
  { id: "platform",           label: "Platform",           sub: "Identity & system", lobe: "parietalL" },
  { id: "lending",            label: "Lending",            sub: "Borrow & repay",    lobe: "parietalR" },
  { id: "recurring_payments", label: "Recurring Payments", sub: "Bills & cards",     lobe: "temporal"  },
  { id: "web_platform",       label: "Web Platform",       sub: "Web touch-points",  lobe: "occipital" },
];

const __ATLAS_FLOWS_MOCK = [
  { id: "stocks",    label: "Indian Stocks",      domain: "markets",   count: 142, primary: true },
  { id: "us",        label: "US Stocks",          domain: "markets",   count: 96,  primary: true },
  { id: "mf",        label: "Mutual Funds",       domain: "markets",   count: 168, primary: true },
  { id: "fno",       label: "F&O",                domain: "markets",   count: 88 },
  { id: "smallcase", label: "Smallcase",          domain: "markets",   count: 64 },
  { id: "ipo",       label: "IPO",                domain: "markets",   count: 42 },
  { id: "etf",       label: "ETF",                domain: "markets",   count: 58 },
  { id: "bonds",     label: "Bonds",              domain: "markets",   count: 46 },

  { id: "portfolio", label: "Portfolio",          domain: "investing", count: 124, primary: true },
  { id: "research",  label: "Research",           domain: "investing", count: 110, primary: true },
  { id: "orders",    label: "Orders",             domain: "investing", count: 86 },
  { id: "watchlist", label: "Watchlists",         domain: "investing", count: 38 },
  { id: "tax",       label: "Tax & Reports",      domain: "investing", count: 44 },

  { id: "wallet",    label: "Wallet",             domain: "money",     count: 78,  primary: true },
  { id: "plutus",    label: "Plutus Card",        domain: "money",     count: 54 },
  { id: "deposit",   label: "Deposit & Withdraw", domain: "money",     count: 52 },

  { id: "kyc",       label: "Onboarding · KYC",   domain: "platform",  count: 92,  primary: true },
  { id: "auth",      label: "Auth & Security",    domain: "platform",  count: 64 },
  { id: "settings",  label: "Settings",           domain: "platform",  count: 58 },
  { id: "notif",     label: "Notifications",      domain: "platform",  count: 32 },
];

const __ATLAS_SYNAPSES_MOCK = [
  ["stocks", "portfolio"], ["stocks", "orders"], ["stocks", "research"],
  ["us", "portfolio"], ["mf", "portfolio"], ["mf", "tax"],
  ["fno", "orders"], ["smallcase", "portfolio"],
  ["ipo", "kyc"], ["etf", "portfolio"], ["bonds", "tax"],
  ["wallet", "deposit"], ["plutus", "wallet"],
  ["deposit", "kyc"], ["kyc", "auth"], ["auth", "settings"],
  ["portfolio", "tax"], ["research", "watchlist"],
  ["orders", "notif"], ["settings", "notif"],
  ["research", "stocks"], ["research", "us"],
];

const __W: any = typeof window !== "undefined" ? (window as any) : {};
// `__ATLAS_DATA_READY` is set by AtlasShell when the live store has finished
// hydrating — even when the result is an empty production DB. Without this
// flag we'd fall through to the mocks for empty tenants and show a fake
// brain. Standalone HTML preview never sets the flag, so it keeps the
// hardcoded showcase data.
//
// Critical: ESM modules are cached, so this top-level code runs ONCE.
// We need DOMAINS/FLOWS/SYNAPSES to *re-read* window each access so SSE-
// driven additions (new projects exported from Figma) appear in the brain
// without a hard reload. Proxy makes DOMAINS look like a normal array but
// each `.find`/`.map`/`.length` access pulls live window state.
function __resolveLive(globalKey: string, mock: any[]): any[] {
  const w: any = typeof window !== "undefined" ? (window as any) : {};
  return w.__ATLAS_DATA_READY && Array.isArray(w[globalKey]) ? w[globalKey] : mock;
}
function __liveArrayProxy(globalKey: string, mock: any[]): any[] {
  return new Proxy([] as any[], {
    get(_t, prop) {
      const real = __resolveLive(globalKey, mock);
      const v = (real as any)[prop];
      return typeof v === "function" ? v.bind(real) : v;
    },
    has(_t, prop) {
      return prop in __resolveLive(globalKey, mock);
    },
    ownKeys() {
      return Reflect.ownKeys(__resolveLive(globalKey, mock));
    },
    getOwnPropertyDescriptor(_t, prop) {
      return Object.getOwnPropertyDescriptor(__resolveLive(globalKey, mock), prop);
    },
  });
}
const DOMAINS = __liveArrayProxy("__ATLAS_DOMAINS", __ATLAS_DOMAINS_MOCK);
const FLOWS = __liveArrayProxy("__ATLAS_FLOWS", __ATLAS_FLOWS_MOCK);
const SYNAPSES = __liveArrayProxy("__ATLAS_SYNAPSES", __ATLAS_SYNAPSES_MOCK);

// FLOWS_BY_ID is also a live-getter so the leaf canvas's parent-flow
// lookup picks up new projects without a page reload. AtlasShell may
// also overwrite this directly; either path keeps it fresh.
if (typeof window !== "undefined") {
  Object.defineProperty(window, "FLOWS_BY_ID", {
    configurable: true,
    get() {
      const list = __resolveLive("__ATLAS_FLOWS", __ATLAS_FLOWS_MOCK);
      return Object.fromEntries(list.map((f: any) => [f.id, f]));
    },
  });
}

const SCREEN_VOCAB = {
  stocks:["Stock detail","Order ticket","Quote","Charts","News","Peers","Financials","About","Holdings"],
  us:["US detail","Tradeplus","Order","Charts","Earnings","Filings","FX","LRS"],
  mf:["Fund detail","SIP","Lumpsum","Switch","STP","SWP","Holdings","Returns","Risk"],
  fno:["Option chain","Strikes","Strategy","Greeks","Margin","Payoff"],
  smallcase:["Discover","Detail","Plan","Rebalance"],
  ipo:["Live","Apply","Mandate","Allotment","Listing"],
  etf:["Detail","Order","Tracking","Holdings"],
  bonds:["Detail","Yield","T-Bills","Order"],
  portfolio:["Overview","Allocation","Returns","XIRR","Holdings","Transactions","Capital gains","Insights"],
  research:["Ideas","Sectors","Macro","Reports","Authors"],
  orders:["Order book","Trade book","Open","Modify","GTT","AMO"],
  watchlist:["My list","Add","Reorder","Alerts"],
  tax:["Capital gains","P&L","TDS","Annual","Harvest"],
  wallet:["Balance","Add money","Withdraw","Transactions","Banks"],
  plutus:["Card hub","Activate","Spend","Rewards","Statement"],
  deposit:["UPI","NEFT","Net banking","Bank picker","Mandate"],
  kyc:["PAN","Aadhaar","Selfie","Bank proof","Signature","Nominee","FATCA","Risk","Status"],
  auth:["Login","MPIN","Biometric","2FA","Devices","Sessions"],
  settings:["Profile","Banks","Nominees","Address","App","Privacy"],
  notif:["Inbox","Alert","Preferences","Snooze"],
};

function buildScreens(flow) {
  const vocab = SCREEN_VOCAB[flow.id] || ["Screen"];
  const out = [];
  for (let i = 0; i < Math.min(flow.count, vocab.length * 3); i++) {
    out.push({ id: `${flow.id}-${i}`, label: vocab[i % vocab.length], idx: i, flow: flow.id });
  }
  return out;
}

// Seeded RNG for stable layouts
function mulberry32(seed) {
  return function () {
    seed |= 0; seed = (seed + 0x6D2B79F5) | 0;
    let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

// ============================================================
// BRAIN-SHAPED LAYOUT — SIDE PROFILE (left-facing)
// ============================================================
// Conventions (normalized space, x ∈ [-1.1, 1.1], y up positive):
//   - Front of brain at +x (right side of canvas? no — we'll put FRONT to the LEFT
//     for a classic "anatomy plate" side view. So +x = back of head, -x = forehead.)
//   - Top (+y) = crown, bottom (-y) = base.
// Lobes:
//   FRONTAL  : front-top bulge          (forehead)
//   PARIETAL : top                       (crown)
//   OCCIPITAL: back bulge                (back of head)
//   TEMPORAL : front-lower drop          (above ear)
//   CEREBELLUM: back-lower tucked dome
//   BRAINSTEM: small nub down from cerebellum
// ============================================================

// Returns true if (x, y) is inside the side-profile brain silhouette.
function inBrain(x, y) {
  // Main cerebrum: an asymmetric blob made of overlapping ellipses
  // 1) Big main mass — slightly egg-shaped, longer than tall
  const mainCx = 0.0, mainCy = 0.10;
  const mainA = 0.78, mainB = 0.55;
  const main = ((x - mainCx) ** 2) / (mainA * mainA) + ((y - mainCy) ** 2) / (mainB * mainB) <= 1;

  // 2) Frontal lobe bulge (front-top, x<0)
  const frontal = ((x + 0.55) ** 2) / (0.32 * 0.32) + ((y - 0.18) ** 2) / (0.42 * 0.42) <= 1;

  // 3) Occipital bulge (back, x>0, slightly down)
  const occipital = ((x - 0.62) ** 2) / (0.30 * 0.30) + ((y - 0.05) ** 2) / (0.36 * 0.36) <= 1;

  // 4) Temporal lobe (front-lower drop near -x, low y) — gives the "ear shelf"
  const temporal = ((x + 0.30) ** 2) / (0.32 * 0.32) + ((y + 0.32) ** 2) / (0.20 * 0.20) <= 1;

  const cerebrum = main || frontal || occipital || temporal;

  // 5) Cerebellum: dome tucked at back-bottom (slightly larger for visual presence)
  const cerebellum = ((x - 0.42) ** 2) / (0.26 * 0.26) + ((y + 0.45) ** 2) / (0.20 * 0.20) <= 1;

  // 6) Brainstem: small vertical nub between temporal & cerebellum
  const stem = ((x - 0.10) ** 2) / (0.13 * 0.13) + ((y + 0.58) ** 2) / (0.15 * 0.15) <= 1;

  // 7) Carve a hint of the central sulcus — a curved thin gap from top-front
  //    diving down-back. Subtle, just to give "creases".
  // sulcus path: a curve y = 0.55 - 0.7*(x+0.1)^2 over x in [-0.25, 0.4]
  const sulcusY = 0.55 - 0.7 * (x + 0.10) ** 2;
  const inSulcus = Math.abs(y - sulcusY) < 0.018 && x > -0.25 && x < 0.40;

  // 8) Carve the lateral fissure (between temporal & frontal/parietal)
  //    a roughly horizontal slit around y ≈ -0.10 from x=-0.55 to x=0.10
  const fissureY = -0.08 - 0.05 * Math.sin((x + 0.5) * 2.0);
  const inFissure = Math.abs(y - fissureY) < 0.022 && x > -0.55 && x < 0.12;

  return (cerebrum || cerebellum || stem) && !inSulcus && !inFissure;
}

// Distance to brain edge (positive inside) — rough estimate
function brainEdgeDistance(x, y) {
  if (!inBrain(x, y)) return -1;
  const dirs = [[1,0],[-1,0],[0,1],[0,-1],[0.7,0.7],[-0.7,0.7],[0.7,-0.7],[-0.7,-0.7]];
  let minSteps = 999;
  for (const [dx, dy] of dirs) {
    let steps = 0;
    while (steps < 50) {
      steps++;
      if (!inBrain(x + dx * 0.02 * steps, y + dy * 0.02 * steps)) break;
    }
    if (steps < minSteps) minSteps = steps;
  }
  return minSteps * 0.02;
}

// Domain-to-lobe anchor (side view). Six lobes — extended from the
// reference's original four to match the current taxonomy in
// lib/atlas/taxonomy.ts. Each anchor is a position in the brain's
// normalised coordinate space (x ∈ [-1.1, 1.1], y up positive).
//
//   Markets             → Frontal-left (front-top decisions)
//   Money Matters       → Frontal-right (front-mid pay/save)
//   Platform            → Parietal-left (top-mid identity/system)
//   Lending             → Parietal-right (top-back borrow/repay)
//   Recurring Payments  → Temporal (lower-front bills/cards)
//   Web Platform        → Occipital (back, web touch-points)
//
// Old IDs (markets/investing/money/platform) are kept as aliases so
// any stale data sources or legacy snapshots still resolve cleanly.
// Anchors tuned to actually land inside the brain silhouette (see
// inBrain() above). Coordinate space: x ∈ [-1.1, 1.1], y up positive.
// Front of brain at -x (forehead), back at +x.
//
// Layout reads as a brain in side-profile:
//
//                      MARKETS · MONEY MATTERS · PLATFORM
//                       (frontal arc — high decisions)
//                                                    LENDING
//                                                  (parietal —
//                                                    top-back)
//          RECURRING                              WEB PLATFORM
//          PAYMENTS                                 (occipital —
//          (temporal —                              back of head)
//           lower front)
//
const DOMAIN_ANCHORS = {
  // Active 6-lobe set — all six anchors confirmed inside inBrain().
  markets:            { x: -0.62, y:  0.30, name: "Frontal-L" },
  money_matters:      { x: -0.20, y:  0.42, name: "Frontal-Top" },
  platform:           { x:  0.22, y:  0.42, name: "Parietal-L" },
  lending:            { x:  0.55, y:  0.18, name: "Parietal-R" },
  recurring_payments: { x: -0.30, y: -0.18, name: "Temporal" },
  web_platform:       { x:  0.55, y: -0.08, name: "Occipital" },
  // Legacy aliases — kept so any stale snapshot still renders cleanly.
  investing:          { x:  0.22, y:  0.42, name: "Parietal-L" },
  money:              { x: -0.20, y:  0.42, name: "Frontal-Top" },
};

function domainAnchor(id) {
  return DOMAIN_ANCHORS[id] || { x: 0, y: 0, name: id || "Unknown" };
}

// Build node graph
function buildGraph() {
  const rand = mulberry32(42);
  const nodes = [];
  const links = [];

  // Add domain nodes (hub - large white).
  // Use the safe anchor lookup so any unknown lobe id (e.g. a new sub-sheet
  // mapped to a domain we haven't yet placed) renders at the origin
  // instead of crashing.
  DOMAINS.forEach(d => {
    const a = domainAnchor(d.id);
    nodes.push({
      id: "d:" + d.id,
      kind: "domain",
      label: d.label,
      domain: d.id,
      x: a.x,
      y: a.y,
      r: 7.5,
      seed: rand() * 1000,
      wiggle: 0.0035,
    });
  });

  // Add flow nodes — placed near their domain anchor inside the brain mask
  FLOWS.forEach(f => {
    const a = domainAnchor(f.domain);
    let pos;
    for (let attempts = 0; attempts < 200; attempts++) {
      const ang = rand() * Math.PI * 2;
      const radius = (f.primary ? 0.12 : 0.22) + rand() * 0.18;
      const x = a.x + Math.cos(ang) * radius;
      const y = a.y + Math.sin(ang) * radius;
      if (inBrain(x, y)) {
        pos = { x, y };
        break;
      }
    }
    if (!pos) pos = { x: a.x, y: a.y };
    nodes.push({
      id: "f:" + f.id,
      kind: "flow",
      label: f.label,
      domain: f.domain,
      flow: f.id,
      primary: !!f.primary,
      count: f.count,
      x: pos.x,
      y: pos.y,
      r: f.primary ? 5.2 : 3.6,
      seed: rand() * 1000,
      wiggle: f.primary ? 0.005 : 0.008,
    });
    // domain → flow link
    links.push({ a: "d:" + f.domain, b: "f:" + f.id, kind: "tree" });
  });

  // Add screen nodes — small dots filling the rest of the brain
  // ---- Sub-flow nodes (one per leaf in window.LEAVES) ----------------
  // We attach actual sub-flows from leaves.jsx to their parent flow node.
  // Each becomes a clickable child: hovering shows metadata in the inspector,
  // clicking opens the leaf canvas (split-screen Figma view).
  // Node id format: "s:<leafId>"
  //
  // Placement strategy: arrange sub-flows in a TIGHT ring around the parent.
  // We pick an orbit radius based on count and a starting angle, then walk
  // around. If a slot lands outside the brain or too close to another node,
  // we try a few perturbations before falling back to the parent's own xy
  // (so a sub-flow never floats far from its parent).
  const LEAVES_LIST = window.LEAVES || [];
  FLOWS.forEach(f => {
    const fNode = nodes.find(n => n.id === "f:" + f.id);
    if (!fNode) return;
    const subs = LEAVES_LIST.filter(l => l.flow === f.id);
    if (subs.length === 0) return;

    // Orbit radius — generous when there are few sub-flows so they're
    // clearly visible, tighter as count grows so 50+ subs stay clustered
    // around the parent. With 1–6 subs we want them clearly readable as
    // discrete nodes, not lost among the filler nebula.
    const fewSubs = subs.length <= 6;
    const baseR = fewSubs
      ? 0.10 + subs.length * 0.005   // 0.105 (1) → 0.130 (6) — generous
      : 0.055 + Math.min(subs.length, 8) * 0.004; // tighter for many
    const startAng = (fNode.x + fNode.y) * 7.3; // deterministic per parent

    subs.forEach((leaf, i) => {
      const ringIdx = i % 2;             // alternate near/far ring
      const ringR = baseR + ringIdx * 0.022;
      const ang = startAng + (i * (Math.PI * 2) / Math.max(subs.length, 4)) + ringIdx * 0.4;

      let pos = null;
      // Try the ideal position, then nudge inward if outside the brain.
      for (let k = 0; k < 8; k++) {
        const r = ringR * (1 - k * 0.12);
        const x = fNode.x + Math.cos(ang) * r;
        const y = fNode.y + Math.sin(ang) * r;
        if (inBrain(x, y)) {
          // ensure not crashing into the parent or another sub-flow
          let crowd = false;
          for (const n of nodes) {
            if (n.id === fNode.id) continue;
            if (n.kind !== "subflow" && n.kind !== "flow") continue;
            const dx = n.x - x, dy = n.y - y;
            if (dx * dx + dy * dy < 0.0009) { crowd = true; break; }
          }
          if (!crowd) { pos = { x, y }; break; }
        }
      }
      // Fallback: tiny offset from parent so it's never far.
      if (!pos) {
        pos = {
          x: fNode.x + Math.cos(ang) * 0.04,
          y: fNode.y + Math.sin(ang) * 0.04,
        };
      }

      nodes.push({
        id: "s:" + leaf.id,
        kind: "subflow",
        label: leaf.label,
        domain: f.domain,
        flow: f.id,
        leafId: leaf.id,
        violations: leaf.violations || 0,
        frames: leaf.frames || 0,
        x: pos.x,
        y: pos.y,
        // Sub-flow dot size scales with how much screen real estate is
        // available — when there are few siblings, render them larger so
        // they read as proper nodes (not filler). Cap at 3.5 to avoid
        // dominating the parent flow node.
        r: fewSubs ? 3.0 : 1.7,
        seed: rand() * 1000,
        wiggle: 0.010,
      });
      links.push({ a: "f:" + f.id, b: "s:" + leaf.id, kind: "leaf" });
    });
  });

  // Add some filler dots to densify the brain silhouette
  const fillerCount = 80;
  let placed = 0, attempts = 0;
  while (placed < fillerCount && attempts < 5000) {
    attempts++;
    const x = (rand() * 2 - 1);
    const y = (rand() * 2 - 1);
    if (!inBrain(x, y)) continue;
    // ensure not too close to existing
    let tooClose = false;
    for (const n of nodes) {
      const dx = n.x - x, dy = n.y - y;
      if (dx * dx + dy * dy < 0.018) { tooClose = true; break; }
    }
    if (tooClose) continue;
    nodes.push({
      id: `n:${placed}`,
      kind: "filler",
      label: "",
      domain: x < 0 ? (y > 0 ? "markets" : "money") : (y > 0 ? "investing" : "platform"),
      x, y,
      r: 1.6,
      seed: rand() * 1000,
      wiggle: 0.014,
    });
    placed++;
  }

  // Cross-domain synapses
  SYNAPSES.forEach(([a, b]) => {
    links.push({ a: "f:" + a, b: "f:" + b, kind: "synapse" });
  });

  // Filler-to-flow links — give some weight to fillers
  const flowNodes = nodes.filter(n => n.kind === "flow");
  nodes.filter(n => n.kind === "filler").forEach(n => {
    // attach to 1-2 nearest flow nodes in same domain
    const candidates = flowNodes.filter(fn => fn.domain === n.domain);
    candidates.sort((p, q) => {
      const da = (p.x - n.x) ** 2 + (p.y - n.y) ** 2;
      const db = (q.x - n.x) ** 2 + (q.y - n.y) ** 2;
      return da - db;
    });
    const k = 1 + (Math.floor(rand() * 2));
    for (let i = 0; i < Math.min(k, candidates.length); i++) {
      links.push({ a: candidates[i].id, b: n.id, kind: "filler" });
    }
  });

  // A few filler-to-filler cross links to add visual texture
  const fillers = nodes.filter(n => n.kind === "filler");
  for (let i = 0; i < 30; i++) {
    const a = fillers[Math.floor(rand() * fillers.length)];
    const b = fillers[Math.floor(rand() * fillers.length)];
    if (!a || !b || a.id === b.id) continue;
    const dx = a.x - b.x, dy = a.y - b.y;
    if (dx * dx + dy * dy > 0.04) continue;
    links.push({ a: a.a || a.id, b: b.id, kind: "filler" });
  }

  return { nodes, links };
}

// ============================================================
// CANVAS RENDERER (2D)
// ============================================================
function AtlasCanvas({
  selected, hovered, search, focusReq,
  onSelectNode, onHoverNode,
  tweaks,
}) {
  const canvasRef = useRef(null);
  const stateRef = useRef({});

  useEffect(() => {
    const canvas = canvasRef.current;
    const ctx = canvas.getContext("2d");
    let dpr = Math.min(window.devicePixelRatio || 1, 2);

    const { nodes, links } = buildGraph();
    const nodeById = new Map(nodes.map(n => [n.id, n]));
    // Build adjacency for highlighting
    const adj = new Map();
    nodes.forEach(n => adj.set(n.id, new Set()));
    links.forEach(l => {
      adj.get(l.a)?.add(l.b);
      adj.get(l.b)?.add(l.a);
    });

    // ---- camera (pan + zoom)
    let zoom = 1;       // 1.0 = fit-to-view (full brain), 4 = close-up
    let cx = 0, cy = 0; // center in normalized brain coords
    let targetZoom = 1, targetCx = 0, targetCy = 0;

    let W = 0, H = 0, scale = 1; // scale = px per normalized unit
    function resize() {
      const rect = canvas.parentElement.getBoundingClientRect();
      let w = rect.width, h = rect.height;
      // Fallback to viewport if parent hasn't laid out yet
      if (!w || !h) { w = window.innerWidth; h = window.innerHeight; }
      if (w === W && h === H) return;
      W = w; H = h;
      canvas.width = W * dpr;
      canvas.height = H * dpr;
      canvas.style.width = W + "px";
      canvas.style.height = H + "px";
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
      const padding = 60;
      const fitW = (W - padding * 2) / 2.2;
      const fitH = (H - padding * 2) / 2.0;
      scale = Math.min(fitW, fitH);
    }
    resize();
    requestAnimationFrame(() => resize());
    const ro = new ResizeObserver(resize);
    ro.observe(canvas.parentElement);
    window.addEventListener("resize", resize);

    function worldToScreen(x, y) {
      return [
        W / 2 + (x - cx) * scale * zoom,
        H / 2 + (y - cy) * scale * zoom,
      ];
    }
    function screenToWorld(sx, sy) {
      return [
        (sx - W / 2) / (scale * zoom) + cx,
        (sy - H / 2) / (scale * zoom) + cy,
      ];
    }

    // ---- interaction
    let dragging = false, lastX = 0, lastY = 0;
    let hoveredId = null;

    // Per-node hover progress, eased toward 1 when hovered and 0 when not.
    // Survives across frames so scale / ring / label / halo all share one
    // smoothly-animated value instead of snapping. Re-checked every frame so
    // un-hovering smoothly decays even when the cursor stops moving.
    const hoverP = new Map();
    const HOVER_LERP_IN = 0.22;   // snappy on enter
    const HOVER_LERP_OUT = 0.10;  // gentler on leave (avoids flicker on edges)
    canvas.addEventListener("pointerdown", (e) => {
      dragging = true; lastX = e.clientX; lastY = e.clientY;
      canvas.setPointerCapture?.(e.pointerId);
      canvas.style.cursor = "grabbing";
    });
    canvas.addEventListener("pointerup", (e) => {
      const moved = Math.hypot(e.clientX - (lastX0 || lastX), e.clientY - (lastY0 || lastY));
      // click vs drag
      if (!moved || moved < 4) {
        if (hoveredId) stateRef.current.onSelect?.(hoveredId);
        else stateRef.current.onSelect?.(null);
      }
      dragging = false;
      try { canvas.releasePointerCapture?.(e.pointerId); } catch {}
      canvas.style.cursor = hoveredId ? "pointer" : "grab";
    });
    let lastX0, lastY0;
    canvas.addEventListener("pointerdown", (e) => { lastX0 = e.clientX; lastY0 = e.clientY; });
    canvas.addEventListener("pointermove", (e) => {
      const rect = canvas.getBoundingClientRect();
      const sx = e.clientX - rect.left, sy = e.clientY - rect.top;
      if (dragging) {
        const dx = e.clientX - lastX, dy = e.clientY - lastY;
        lastX = e.clientX; lastY = e.clientY;
        cx -= dx / (scale * zoom);
        cy -= dy / (scale * zoom);
        targetCx = cx; targetCy = cy;
        return;
      }
      // hit-test under pointer
      const [wx, wy] = screenToWorld(sx, sy);
      let best = null, bestD = Infinity;
      for (const n of nodes) {
        if (n.kind === "filler") continue;
        const dx = n.x - wx, dy = n.y - wy;
        const d = dx * dx + dy * dy;
        // Subflow nodes are tiny (r≈1.7–3) and visually they live in the
        // halo/orbit of a much bigger flow node. With only a 4px buffer they
        // were nearly impossible to land the cursor on. Bump padding to 8px
        // for subflows so the visible-ring effectively becomes the hit area.
        const padding = n.kind === "subflow" ? 8 : 4;
        const hitR = (n.r + padding) / (scale * zoom);
        if (d < hitR * hitR && d < bestD) { bestD = d; best = n.id; }
      }
      // also consider fillers but with smaller hit box
      if (!best) {
        for (const n of nodes) {
          if (n.kind !== "filler") continue;
          const dx = n.x - wx, dy = n.y - wy;
          const d = dx * dx + dy * dy;
          const hitR = (n.r + 3) / (scale * zoom);
          if (d < hitR * hitR && d < bestD) { bestD = d; best = n.id; }
        }
      }
      if (best !== hoveredId) {
        hoveredId = best;
        stateRef.current.onHover?.(best);
        canvas.style.cursor = best ? "pointer" : "grab";
      }
    });
    canvas.addEventListener("wheel", (e) => {
      e.preventDefault();
      const rect = canvas.getBoundingClientRect();
      const sx = e.clientX - rect.left, sy = e.clientY - rect.top;

      // Trackpad-friendly routing (see leafcanvas.jsx for full notes):
      //   pinch (ctrlKey)         → zoom
      //   Cmd/Ctrl + scroll       → zoom
      //   mouse wheel (line mode  → zoom
      //     OR large integer Y, no X)
      //   everything else (2-finger scroll on trackpad) → pan
      const isPinch = e.ctrlKey;
      const isCmdZoom = e.metaKey;
      const looksLikeMouseWheel =
        e.deltaMode === 1 ||
        (e.deltaX === 0 && Math.abs(e.deltaY) >= 50 && Number.isInteger(e.deltaY));
      const shouldZoom = isPinch || isCmdZoom || looksLikeMouseWheel;

      if (shouldZoom) {
        const [wxBefore, wyBefore] = screenToWorld(sx, sy);
        const k = isPinch ? 0.01 : isCmdZoom ? 0.005 : 0.002;
        const factor = Math.exp(-e.deltaY * k);
        zoom = Math.max(0.55, Math.min(8, zoom * factor));
        const [wxAfter, wyAfter] = screenToWorld(sx, sy);
        cx += wxBefore - wxAfter;
        cy += wyBefore - wyAfter;
        targetZoom = zoom; targetCx = cx; targetCy = cy;
      } else {
        // Two-finger pan: translate camera. Divide by zoom so 1 trackpad px
        // moves the camera 1 screen px regardless of zoom level.
        cx += e.deltaX / zoom;
        cy += e.deltaY / zoom;
        targetCx = cx; targetCy = cy;
      }
    }, { passive: false });

    // ---- render loop
    let raf;
    function render() {
      // Defensive: re-check size each frame in case parent layout changed late
      if (!W || !H) resize();
      // smooth camera
      zoom += (targetZoom - zoom) * 0.18;
      cx += (targetCx - cx) * 0.18;
      cy += (targetCy - cy) * 0.18;

      ctx.clearRect(0, 0, W, H);

      const focused = stateRef.current.selected || stateRef.current.hovered;
      const focusedNeighbors = focused ? adj.get(focused) || new Set() : null;

      // Search filter
      const searchQ = (search || "").trim().toLowerCase();
      const searchMatches = new Set();
      if (searchQ) {
        nodes.forEach(n => {
          if (n.label && n.label.toLowerCase().includes(searchQ)) searchMatches.add(n.id);
        });
      }

      // Decide which nodes are "dimmed"
      function isDimmed(id) {
        if (searchQ) return !searchMatches.has(id);
        if (focused) return id !== focused && !focusedNeighbors.has(id);
        return false;
      }

      // ---- WIGGLE: each node has a tiny ambient drift driven by time + seed.
      // When a node is hovered or selected, ONLY that node + its 1-hop
      // neighbors freeze (smoothly eased per-node). The rest of the scene
      // keeps drifting, so the focused subtree is calmed without killing
      // the ambient life of the rest of the graph.
      const t = performance.now() * 0.001;
      const baseWig = (stateRef.current.tweaks?.wiggle ?? 1);
      const focusedId = stateRef.current.selected || stateRef.current.hovered;
      const frozenSet = focusedId
        ? new Set([focusedId, ...(adj.get(focusedId) || [])])
        : null;

      // ── Eased per-node hover progress. Each node moves toward 1 when it's
      // the active hover target and 0 otherwise. Used for scale, ring opacity,
      // halo, label opacity — single value drives every visual response so
      // they stay visually coherent.
      for (const n of nodes) {
        const target = n.id === hoveredId ? 1 : 0;
        const cur = hoverP.get(n.id) ?? 0;
        const lerp = target > cur ? HOVER_LERP_IN : HOVER_LERP_OUT;
        const next = cur + (target - cur) * lerp;
        hoverP.set(n.id, next);
      }
      // Parent ripple — when a flow node is hovered, every subflow whose
      // parent is that flow gets a 40% boost (a soft "the family glows"
      // effect). Uses the *eased* parent progress so the ripple breathes
      // alongside the parent's own pulse instead of snapping.
      const hoveredNode = hoveredId ? nodeById.get(hoveredId) : null;
      const rippleParentId = hoveredNode?.kind === "flow" ? hoveredNode.id : null;
      const rippleP = rippleParentId ? (hoverP.get(rippleParentId) ?? 0) : 0;
      // per-node eased multiplier (0 = frozen, 1 = full)
      if (!stateRef.current._wigByNode) stateRef.current._wigByNode = new Map();
      const wigByNode = stateRef.current._wigByNode;
      function pos(n) {
        const target = (frozenSet && frozenSet.has(n.id)) ? 0 : 1;
        let cur = wigByNode.get(n.id);
        if (cur == null) cur = target;
        cur += (target - cur) * 0.12;
        wigByNode.set(n.id, cur);

        const s = n.seed || 0;
        const a = (n.wiggle || 0) * baseWig * cur;
        const dx = Math.sin(t * 0.75 + s) * a + Math.cos(t * 0.43 + s * 1.7) * a * 0.55;
        const dy = Math.cos(t * 0.62 + s * 1.3) * a + Math.sin(t * 0.31 + s * 0.7) * a * 0.55;
        return [n.x + dx, n.y + dy];
      }
      const livePos = new Map();
      for (const n of nodes) livePos.set(n.id, pos(n));

      // breathing pulse (links + node radius)
      const breath = 0.5 + 0.5 * Math.sin(t * 0.9);

      // ---- LINKS
      ctx.lineWidth = 1;
      for (const l of links) {
        const A = nodeById.get(l.a), B = nodeById.get(l.b);
        if (!A || !B) continue;
        const [Ax, Ay] = livePos.get(A.id);
        const [Bx, By] = livePos.get(B.id);
        const [ax, ay] = worldToScreen(Ax, Ay);
        const [bx, by] = worldToScreen(Bx, By);
        // Frustum cull (simple)
        if ((ax < -50 && bx < -50) || (ax > W + 50 && bx > W + 50)) continue;
        if ((ay < -50 && by < -50) || (ay > H + 50 && by > H + 50)) continue;

        const aDim = isDimmed(A.id), bDim = isDimmed(B.id);
        const bothInvolved = focused && (A.id === focused || B.id === focused);

        let alpha;
        if (bothInvolved) alpha = 0.55;
        else if (aDim || bDim) alpha = 0.04;
        else alpha = l.kind === "synapse" ? 0.12 : 0.18;

        ctx.strokeStyle = `rgba(180, 195, 220, ${alpha})`;
        ctx.lineWidth = bothInvolved ? 1.2 : 0.6;
        ctx.beginPath();
        ctx.moveTo(ax, ay);
        ctx.lineTo(bx, by);
        ctx.stroke();
      }

      // ---- NODES
      // Sort: filler first (back), then screen, then flow, then domain (front)
      const order = [...nodes].sort((a, b) => {
        const w = { filler: 0, screen: 1, flow: 2, domain: 3 };
        return w[a.kind] - w[b.kind];
      });

      for (const n of order) {
        const [Lx, Ly] = livePos.get(n.id);
        const [sx, sy] = worldToScreen(Lx, Ly);
        if (sx < -30 || sx > W + 30 || sy < -30 || sy > H + 30) continue;

        const dim = isDimmed(n.id);
        const isHover = hoveredId === n.id;
        const isSel = stateRef.current.selected === n.id;
        const isMatch = searchQ && searchMatches.has(n.id);

        // Subtle breathing on radius for life — bigger nodes pulse more
        // Sub-flow nodes get a parent-active boost (slightly bigger when their parent
        // flow is selected) and a strong hover boost (when the node itself is hovered).
        const parentSelected =
          n.kind === "subflow" && stateRef.current.selected === "f:" + n.flow;
        const breathR = 1 + (isHover || isSel ? 0 : (n.kind === "domain" ? 0.06 : n.kind === "flow" ? 0.04 : 0.02) * (Math.sin(t * 1.4 + (n.seed || 0)) ));
        const hp = hoverP.get(n.id) ?? 0;
        // Subflow under a hovered flow gets a fraction of that parent's eased
        // hover (rippleP). Other kinds aren't part of a parent ripple.
        const familyP =
          n.kind === "subflow" && rippleParentId === ("f:" + n.flow)
            ? rippleP * 0.45
            : 0;
        let scale;
        if (n.kind === "subflow") {
          // base = breath/parentSelected/sel; hover smoothly adds up to +0.95
          // on top, ripple from parent adds up to +0.4. Result reads as a
          // gentle pop on its own and a light brighten when the family is hot.
          const base = isSel ? 1.25 : (parentSelected ? 1.25 : breathR);
          scale = base + hp * 0.95 + familyP * 0.55;
        } else {
          // Flow / domain: eased to 1.55 over hp, fallback to old behaviour.
          scale = 1 + hp * 0.55 + (isSel ? 0.25 : 0) + (hp || isSel ? 0 : (breathR - 1));
        }
        const r = n.r * scale;

        // Color: white for primaries/domains, gray for the rest, accent for matches
        let fill = "#c5cdda";
        if (n.kind === "domain") fill = "#ffffff";
        else if (n.kind === "flow" && n.primary) fill = "#ffffff";
        else if (n.kind === "filler") fill = "#7d8699";
        else if (n.kind === "subflow") {
          // Smoothly cross-fade between idle/parent-active/hovered using hp.
          // mixHex blends two hex colors at ratio 0..1.
          const idle = "#9aa3b6";
          const familyOn = "#d6dde9";
          const hot = "#ffffff";
          const familyMix = familyP > 0 ? mixHex(idle, familyOn, Math.min(1, familyP * 1.6)) : (parentSelected ? familyOn : idle);
          fill = mixHex(familyMix, hot, hp);
        }

        if (isMatch) fill = "#5fd28e";   // green accent like Obsidian search highlight
        if (isSel) fill = "#7eb8ff";     // blue accent for selection

        ctx.globalAlpha = dim ? 0.18 : 1;

        // Animated halo — expands outward as hp ramps to 1, fades on the way
        // back. Subflow gets a slightly tighter ring than flow/domain so it
        // doesn't visually swallow the parent.
        if (hp > 0.04) {
          const haloOffset = (n.kind === "subflow" ? 4 : 5) + hp * (n.kind === "subflow" ? 5 : 7);
          ctx.beginPath();
          ctx.arc(sx, sy, r + haloOffset, 0, Math.PI * 2);
          ctx.strokeStyle = isSel
            ? `rgba(126,184,255,${0.55 * hp})`
            : `rgba(255,255,255,${0.32 * hp})`;
          ctx.lineWidth = 1 + hp * 0.8;
          ctx.stroke();
        } else if (isSel) {
          // Persistent selection ring even when not hovered.
          ctx.beginPath();
          ctx.arc(sx, sy, r + 5, 0, Math.PI * 2);
          ctx.strokeStyle = "rgba(126,184,255,0.55)";
          ctx.lineWidth = 1;
          ctx.stroke();
        }

        ctx.beginPath();
        ctx.arc(sx, sy, r, 0, Math.PI * 2);
        ctx.fillStyle = fill;
        ctx.fill();
      }
      ctx.globalAlpha = 1;

      // ---- LABELS (semantic LOD)
      // - At zoom <= 1.05: only domain labels show
      // - At 1.05–2: domain + primary flow labels show
      // - At 2–4: all flow labels show
      // - At >4: flow + screen labels show (zoomed to focus)
      // - Hover/selected always show their own label and 1-hop neighbors
      ctx.font = "500 12px Inter, system-ui, sans-serif";
      ctx.textAlign = "center";
      ctx.textBaseline = "top";

      function drawLabel(n, sx, sy, opacity, weight = 500, size = 12) {
        if (opacity <= 0.01) return;
        const text = n.label;
        if (!text) return;
        ctx.font = `${weight} ${size}px Inter, system-ui, sans-serif`;
        // measure for backdrop pill (improves legibility)
        const m = ctx.measureText(text);
        const padX = 5, padY = 2;
        const tx = sx, ty = sy + n.r + 6;
        // soft backdrop
        ctx.fillStyle = `rgba(8, 11, 20, ${0.55 * opacity})`;
        ctx.beginPath();
        const rx = m.width / 2 + padX;
        const ry = size * 0.7 + padY;
        roundRect(ctx, tx - rx, ty - padY, rx * 2, ry * 2, 4);
        ctx.fill();
        // text
        ctx.fillStyle = `rgba(232, 238, 252, ${opacity})`;
        ctx.fillText(text, tx, ty);
      }

      for (const n of order) {
        if (!n.label) continue;
        const [Lx, Ly] = livePos.get(n.id);
        const [sx, sy] = worldToScreen(Lx, Ly);
        if (sx < -100 || sx > W + 100 || sy < -100 || sy > H + 100) continue;

        const isHover = hoveredId === n.id;
        const isSel = stateRef.current.selected === n.id;
        const isMatch = searchQ && searchMatches.has(n.id);
        const isNeighbor = focused && (focusedNeighbors?.has(n.id));

        let visible = false, op = 1, weight = 500, size = 12;

        if (n.kind === "domain") {
          // Always visible at any zoom
          visible = true; weight = 600; size = 13;
          op = focused ? (isSel || isHover || isNeighbor || n.id === focused ? 1 : 0.45) : 1;
        } else if (n.kind === "flow") {
          if (isSel || isHover || isMatch || isNeighbor) { visible = true; op = 1; size = 12.5; weight = 600; }
          else if (n.primary && zoom > 1.1) { visible = true; op = Math.min(1, (zoom - 1.1) / 0.6); }
          else if (zoom > 2.2) { visible = true; op = Math.min(1, (zoom - 2.2) / 0.6); }
        } else if (n.kind === "subflow") {
          // Always show sub-flow labels when their parent flow is selected.
          if (isSel || isHover) { visible = true; op = 1; size = 11.5; weight = 600; }
          else if (stateRef.current.selected === "f:" + n.flow) {
            visible = true; op = isNeighbor ? 0.95 : 0.85; size = 11; weight = 500;
          }
          else if (zoom > 3.5) { visible = true; op = Math.min(1, (zoom - 3.5) / 0.6); size = 11; }
        }
        // dimming
        if (isDimmed(n.id) && !isHover && !isSel) op *= 0.25;
        if (visible) drawLabel(n, sx, sy, op, weight, size);
      }

      raf = requestAnimationFrame(render);
    }
    render();

    // ---- imperative API
    stateRef.current = {
      ...stateRef.current,
      onSelect: () => {},
      onHover: () => {},
      flyTo(id) {
        if (!id) {
          targetCx = 0; targetCy = 0; targetZoom = 1;
          return;
        }
        const n = nodeById.get(id);
        if (!n) return;
        targetCx = n.x; targetCy = n.y;
        targetZoom = n.kind === "domain" ? 2.4 : n.kind === "flow" ? 4 : n.kind === "subflow" ? 5 : 5.5;
      },
      reset() {
        targetCx = 0; targetCy = 0; targetZoom = 1;
      },
      getNode(id) { return nodeById.get(id); },
      getNeighbors(id) { return Array.from(adj.get(id) || []); },
    };

    return () => {
      cancelAnimationFrame(raf);
      ro.disconnect();
      window.removeEventListener("resize", resize);
    };
  }, []);

  // wire callbacks
  useEffect(() => {
    if (!stateRef.current) return;
    stateRef.current.onSelect = onSelectNode;
    stateRef.current.onHover = onHoverNode;
    stateRef.current.selected = selected;
    stateRef.current.hovered = hovered;
    stateRef.current.tweaks = tweaks;
  });

  // focus requests
  useEffect(() => {
    if (!stateRef.current?.flyTo || !focusReq) return;
    if (focusReq.id === null) stateRef.current.reset();
    else stateRef.current.flyTo(focusReq.id);
  }, [focusReq]);

  return <canvas ref={canvasRef} className="atlas-canvas" />;
}

// Linear interpolation between two #rrggbb hex strings. Used by the node
// renderer so subflow color smoothly cross-fades between idle/family/hover
// states alongside the eased hover progress, avoiding the previous palette
// snap.
function mixHex(a, b, t) {
  const ai = parseInt(a.slice(1), 16);
  const bi = parseInt(b.slice(1), 16);
  const ar = (ai >> 16) & 255, ag = (ai >> 8) & 255, ab = ai & 255;
  const br = (bi >> 16) & 255, bg = (bi >> 8) & 255, bb = bi & 255;
  const tt = Math.max(0, Math.min(1, t));
  const r = Math.round(ar + (br - ar) * tt);
  const g = Math.round(ag + (bg - ag) * tt);
  const bl = Math.round(ab + (bb - ab) * tt);
  return "#" + ((r << 16) | (g << 8) | bl).toString(16).padStart(6, "0");
}

function roundRect(ctx, x, y, w, h, r) {
  ctx.beginPath();
  ctx.moveTo(x + r, y);
  ctx.arcTo(x + w, y, x + w, y + h, r);
  ctx.arcTo(x + w, y + h, x, y + h, r);
  ctx.arcTo(x, y + h, x, y, r);
  ctx.arcTo(x, y, x + w, y, r);
  ctx.closePath();
}

// ============================================================
// UI: top bar, sidebar, inspector, ⌘K
// ============================================================

function TopBar({ breadcrumb, onHome, onOpenSearch, totalScreens, totalFlows }) {
  return (
    <div className="topbar">
      <div className="tb-left">
        <button className="tb-logo" onClick={onHome} title="Recenter">
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5">
            <circle cx="6" cy="12" r="2.4"/>
            <circle cx="18" cy="6" r="1.8"/>
            <circle cx="18" cy="18" r="1.8"/>
            <circle cx="13" cy="12" r="1.4"/>
            <path d="M7.7 11l4-1M7.7 13l4 1M14 11l3-4M14 13l3 4" opacity=".55"/>
          </svg>
        </button>
        <div className="tb-brand">
          <div className="tb-title">The Atlas</div>
          <div className="tb-sub">INDmoney · knowledge graph</div>
        </div>
        {breadcrumb && (
          <div className="crumbs">
            {breadcrumb.map((c, i) => (
              <React.Fragment key={i}>
                <span className="crumb-sep">›</span>
                <span className={`crumb ${i === breadcrumb.length - 1 ? "is-last" : ""}`}>{c}</span>
              </React.Fragment>
            ))}
          </div>
        )}
      </div>
      <div className="tb-mid">
        <button className="searchpill" onClick={onOpenSearch}>
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><circle cx="11" cy="11" r="7"/><path d="m20 20-3.5-3.5"/></svg>
          <span>Search flows, screens…</span>
          <span className="kbd">⌘ K</span>
        </button>
      </div>
      <div className="tb-right">
        <div className="stat"><span className="stat-num">{totalFlows}</span><span className="stat-lbl">flows</span></div>
        <div className="stat"><span className="stat-num">{totalScreens.toLocaleString()}</span><span className="stat-lbl">screens</span></div>
      </div>
    </div>
  );
}

function Sidebar({ activeDomain, activeFlow, onPickDomain, onPickFlow, hoveredFlowId }) {
  const [openDomain, setOpenDomain] = useState(null);
  const effectiveOpen = activeDomain || openDomain;
  return (
    <div className="sb">
      <div className="sb-h">DOMAINS</div>
      <div className="sb-list">
        {DOMAINS.map(d => {
          const flows = FLOWS.filter(f => f.domain === d.id);
          const isOpen = effectiveOpen === d.id;
          return (
            <div key={d.id} className={`sb-domain ${activeDomain === d.id ? "is-active" : ""}`}>
              <button className="sb-row" onClick={() => {
                setOpenDomain(prev => prev === d.id ? null : d.id);
                onPickDomain(d.id);
              }}>
                <span className={`sb-caret ${isOpen ? "is-open" : ""}`}>›</span>
                <span className="sb-text">
                  <span className="sb-name">{d.label}</span>
                  <span className="sb-sub">{d.sub}</span>
                </span>
                <span className="sb-num">{flows.length}</span>
              </button>
              {isOpen && (
                <div className="sb-children">
                  {flows.map(f => (
                    <button
                      key={f.id}
                      className={`sb-flow ${activeFlow === f.id ? "is-active" : ""} ${hoveredFlowId === f.id ? "is-hover" : ""}`}
                      onClick={() => onPickFlow(f.id)}
                    >
                      <span className={`sb-flow-dot ${f.primary ? "is-primary" : ""}`} />
                      <span className="sb-flow-name">{f.label}</span>
                      <span className="sb-flow-num">{f.count}</span>
                    </button>
                  ))}
                </div>
              )}
            </div>
          );
        })}
      </div>
    </div>
  );
}

function Inspector({ kind, payload, hoveredLeafId, onClose, onPickFlow, onPickScreen }) {
  if (!kind) return null;
  if (kind === "domain") {
    const domain = payload;
    const flows = FLOWS.filter(f => f.domain === domain.id);
    const totalScreens = flows.reduce((a, f) => a + f.count, 0);
    return (
      <div className="ins">
        <div className="ins-head">
          <div>
            <div className="ins-eyebrow">Domain</div>
            <div className="ins-name">{domain.label}</div>
            <div className="ins-meta">{flows.length} flows · {totalScreens} screens</div>
          </div>
          <button className="ins-close" onClick={onClose} aria-label="Close">✕</button>
        </div>
        <div className="ins-section">
          <div className="ins-section-h">Flows</div>
          <div className="ins-list">
            {flows.map(f => (
              <button key={f.id} className="ins-row" onClick={() => onPickFlow(f.id)}>
                <span className={`ins-row-dot ${f.primary ? "is-primary" : ""}`} />
                <span className="ins-row-name">{f.label}{f.primary && <span className="badge">primary</span>}</span>
                <span className="ins-row-num">{f.count}</span>
              </button>
            ))}
          </div>
        </div>
      </div>
    );
  }
  if (kind === "flow") {
    const flow = payload;
    const domain = DOMAINS.find(d => d.id === flow.domain);
    const leaves = (window.LEAVES_BY_FLOW?.[flow.id]) || [];
    const linked = SYNAPSES.filter(([a, b]) => a === flow.id || b === flow.id).map(([a, b]) => a === flow.id ? b : a);
    const totalViolations = leaves.reduce((a, l) => a + (l.violations || 0), 0);
    return (
      <div className="ins">
        <div className="ins-head">
          <div>
            <div className="ins-eyebrow">{domain.label} · Flow</div>
            <div className="ins-name">{flow.label}</div>
            <div className="ins-meta">{flow.count} screens · {leaves.length} sub-flows{flow.primary && " · primary"}</div>
          </div>
          <button className="ins-close" onClick={onClose} aria-label="Close">✕</button>
        </div>
        <div className="ins-section">
          <div className="ins-section-h">Health</div>
          <Health label="Token coverage" v={92} />
          <Health label="A11y pass" v={84} />
          <Health label="Decision links" v={71} />
        </div>
        {leaves.length > 0 && (
          <div className="ins-section">
            <div className="ins-section-h">Sub-flows <span className="muted">({leaves.length}{totalViolations > 0 ? ` · ${totalViolations} violations` : ""})</span></div>
            <div className="leaves-list">
              {leaves.map(l => {
                const isHovered = hoveredLeafId === l.id;
                return (
                  <button
                    key={l.id}
                    className={`leaf-row ${isHovered ? "is-hover" : ""}`}
                    onClick={() => window.__openLeaf?.(l.id)}
                  >
                    <span className="leaf-row-name">{l.label}</span>
                    <span className="leaf-row-meta">
                      <span className="leaf-row-frames">{l.frames} frames</span>
                      {l.violations > 0 && (
                        <span className="leaf-row-vio">{l.violations}</span>
                      )}
                    </span>
                    <span className="leaf-row-arrow">→</span>
                    {isHovered && (
                      <span className="leaf-row-hint">Click node on graph to open</span>
                    )}
                  </button>
                );
              })}
            </div>
          </div>
        )}
        {linked.length > 0 && (
          <div className="ins-section">
            <div className="ins-section-h">Linked flows</div>
            <div className="ins-list">
              {linked.map(t => {
                const f = FLOWS.find(x => x.id === t);
                if (!f) return null;
                return (
                  <button key={t} className="ins-row" onClick={() => onPickFlow(t)}>
                    <span className={`ins-row-dot ${f.primary ? "is-primary" : ""}`} />
                    <span className="ins-row-name">{f.label}</span>
                    <span className="ins-row-num">{f.count}</span>
                  </button>
                );
              })}
            </div>
          </div>
        )}
      </div>
    );
  }
  return null;
}

function Health({ label, v }) {
  return (
    <div className="h-cell">
      <div className="h-top"><span>{label}</span><b>{v}%</b></div>
      <div className="h-bar"><div className="h-fill" style={{ width: `${v}%` }} /></div>
    </div>
  );
}

function CommandK({ open, onClose, onPick }) {
  const [q, setQ] = useState("");
  const [idx, setIdx] = useState(0);
  const inputRef = useRef(null);
  useEffect(() => { if (open) { setQ(""); setIdx(0); setTimeout(() => inputRef.current?.focus(), 30); } }, [open]);
  const all = useMemo(() => {
    const out = [];
    DOMAINS.forEach(d => out.push({ kind: "domain", id: d.id, label: d.label, sub: d.sub }));
    FLOWS.forEach(f => {
      const d = DOMAINS.find(x => x.id === f.domain);
      out.push({ kind: "flow", id: f.id, label: f.label, sub: `${d.label} · ${f.count} screens`, flow: f });
      ((window.LEAVES_BY_FLOW?.[f.id]) || []).forEach(l => {
        out.push({ kind: "subflow", id: l.id, label: l.label, sub: `${f.label} · ${l.frames} frames`, flow: f, leaf: l });
      });
    });
    return out;
  }, []);
  const results = useMemo(() => {
    if (!q.trim()) return all.slice(0, 14);
    const t = q.toLowerCase();
    return all.filter(r => r.label.toLowerCase().includes(t) || r.sub.toLowerCase().includes(t)).slice(0, 30);
  }, [q, all]);
  useEffect(() => { setIdx(0); }, [q]);
  if (!open) return null;
  const onKey = (e) => {
    if (e.key === "ArrowDown") { e.preventDefault(); setIdx(i => Math.min(results.length - 1, i + 1)); }
    else if (e.key === "ArrowUp") { e.preventDefault(); setIdx(i => Math.max(0, i - 1)); }
    else if (e.key === "Enter") { e.preventDefault(); const r = results[idx]; if (r) { onPick(r); onClose(); } }
  };
  return (
    <div className="cmdk-back" onClick={onClose}>
      <div className="cmdk" onClick={e => e.stopPropagation()}>
        <div className="cmdk-head">
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><circle cx="11" cy="11" r="7"/><path d="m20 20-3.5-3.5"/></svg>
          <input ref={inputRef} value={q} onChange={e => setQ(e.target.value)} onKeyDown={onKey} placeholder="Jump to a flow, sub-flow, domain…" />
          <span className="kbd">esc</span>
        </div>
        <div className="cmdk-body">
          {results.length === 0 && <div className="cmdk-empty">No matches.</div>}
          {results.map((r, i) => (
            <button key={r.kind + r.id} className={`cmdk-row ${i === idx ? "is-active" : ""}`} onMouseEnter={() => setIdx(i)} onClick={() => { onPick(r); onClose(); }}>
              <span className="cmdk-label">{r.label}</span>
              <span className="cmdk-kind">{r.kind === "subflow" ? "sub-flow" : r.kind}</span>
              <span className="cmdk-sub">{r.sub}</span>
            </button>
          ))}
        </div>
        <div className="cmdk-foot">
          <span><span className="kbd">↑↓</span> navigate</span>
          <span><span className="kbd">↵</span> open</span>
          <span><span className="kbd">esc</span> close</span>
        </div>
      </div>
    </div>
  );
}

function ZoomControls({ onZoomIn, onZoomOut, onFit }) {
  return (
    <div className="zoomctrl">
      <button onClick={onZoomIn} title="Zoom in"><span>+</span></button>
      <button onClick={onZoomOut} title="Zoom out"><span>−</span></button>
      <button onClick={onFit} title="Fit"><svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><path d="M4 9V4h5M20 9V4h-5M4 15v5h5M20 15v5h-5"/></svg></button>
    </div>
  );
}

function Hint({ visible }) {
  if (!visible) return null;
  return (
    <div className="hint">
      <div className="hint-row"><span className="kbd">drag</span> pan</div>
      <div className="hint-row"><span className="kbd">scroll</span> zoom</div>
      <div className="hint-row"><span className="kbd">click</span> open</div>
      <div className="hint-row"><span className="kbd">⌘K</span> search</div>
    </div>
  );
}

// ============================================================
// ROOT
// ============================================================
const TWEAK_DEFAULTS = /*EDITMODE-BEGIN*/{
  "showSidebar": true,
  "showHints": true,
  "wiggle": 0.8
}/*EDITMODE-END*/;

function App() {
  const [tweaks, setTweak] = window.useTweaks(TWEAK_DEFAULTS);

  const [hovered, setHovered] = useState(null);
  const [selected, setSelected] = useState(null);
  const [focusReq, setFocusReq] = useState(null);
  const [cmdOpen, setCmdOpen] = useState(false);
  const [showHint, setShowHint] = useState(true);

  useEffect(() => {
    const h = () => setShowHint(false);
    window.addEventListener("pointerdown", h, { once: true });
    return () => window.removeEventListener("pointerdown", h);
  }, []);

  useEffect(() => {
    const fn = (e) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") { e.preventDefault(); setCmdOpen(v => !v); }
      else if (e.key === "Escape") {
        // If the leaf canvas is open, the shell handles Escape — don't reset the Atlas.
        if (window.__leafOpen) return;
        if (cmdOpen) setCmdOpen(false);
        else if (selected) { setSelected(null); setFocusReq({ id: null, t: Date.now() }); }
      } else if (e.key === "0") {
        setSelected(null);
        setFocusReq({ id: null, t: Date.now() });
      }
    };
    window.addEventListener("keydown", fn);
    return () => window.removeEventListener("keydown", fn);
  }, [cmdOpen, selected]);

  // Decode selected
  const sel = useMemo(() => {
    if (!selected) return null;
    if (selected.startsWith("d:")) {
      const d = DOMAINS.find(x => x.id === selected.slice(2));
      return d ? { kind: "domain", node: selected, payload: d } : null;
    }
    if (selected.startsWith("f:")) {
      const f = FLOWS.find(x => x.id === selected.slice(2));
      return f ? { kind: "flow", node: selected, payload: f } : null;
    }
    if (selected.startsWith("s:")) {
      // Sub-flow id (s:<leafId>) — surface as the parent flow on the Atlas;
      // the leaf canvas itself handles the in-flow detail.
      const leafId = selected.slice(2);
      const leaf = (window.LEAVES || []).find(l => l.id === leafId);
      if (!leaf) return null;
      const f = FLOWS.find(x => x.id === leaf.flow);
      return f ? { kind: "flow", node: "f:" + f.id, payload: f, pickedLeafId: leafId } : null;
    }
    return null;
  }, [selected]);

  const handleSelect = useCallback((id) => {
    if (!id) {
      setSelected(null);
      setFocusReq({ id: null, t: Date.now() });
      return;
    }
    // Sub-flow node click → open leaf canvas immediately. We DON'T move
    // selection (the parent flow stays selected), so when the leaf closes
    // the user lands back on the same view.
    if (id.startsWith("s:")) {
      const leafId = id.slice(2);
      const leaf = (window.LEAVES || []).find(l => l.id === leafId);
      if (leaf) {
        // Make sure the parent flow is what's selected on the Atlas.
        setSelected("f:" + leaf.flow);
        window.__openLeaf?.(leafId);
        return;
      }
    }
    setSelected(id);
    setFocusReq({ id, t: Date.now() });
  }, []);

  const breadcrumb = useMemo(() => {
    if (!sel) return null;
    if (sel.kind === "domain") return [sel.payload.label];
    if (sel.kind === "flow") {
      const d = DOMAINS.find(x => x.id === sel.payload.domain);
      return [d.label, sel.payload.label];
    }
    return null;
  }, [sel]);

  const totalScreens = useMemo(() => FLOWS.reduce((a, f) => a + f.count, 0), []);

  return (
    <div className="atlas-root">
      <AtlasCanvas
        selected={selected}
        hovered={hovered}
        search={null}
        focusReq={focusReq}
        onSelectNode={handleSelect}
        onHoverNode={setHovered}
        tweaks={tweaks}
      />

      <TopBar
        breadcrumb={breadcrumb}
        onHome={() => { setSelected(null); setFocusReq({ id: null, t: Date.now() }); }}
        onOpenSearch={() => setCmdOpen(true)}
        totalScreens={totalScreens}
        totalFlows={FLOWS.length}
      />

      {tweaks.showSidebar && (
        <Sidebar
          activeDomain={sel?.kind === "domain" ? sel.payload.id : sel?.kind === "flow" ? sel.payload.domain : null}
          activeFlow={sel?.kind === "flow" ? sel.payload.id : null}
          hoveredFlowId={hovered && hovered.startsWith("f:") ? hovered.slice(2) : null}
          onPickDomain={(id) => handleSelect("d:" + id)}
          onPickFlow={(id) => handleSelect("f:" + id)}
        />
      )}

      <Inspector
        kind={sel?.kind || null}
        payload={sel?.payload || null}
        hoveredLeafId={hovered && hovered.startsWith("s:") ? hovered.slice(2) : null}
        onClose={() => { setSelected(null); setFocusReq({ id: null, t: Date.now() }); }}
        onPickFlow={(id) => handleSelect("f:" + id)}
        onPickScreen={(s) => handleSelect("s:" + s.id)}
      />

      <CommandK
        open={cmdOpen}
        onClose={() => setCmdOpen(false)}
        onPick={(r) => {
          if (r.kind === "domain") handleSelect("d:" + r.id);
          else if (r.kind === "flow") handleSelect("f:" + r.flow.id);
          else if (r.kind === "subflow") {
            // First select the parent flow on the Atlas, then open the leaf canvas.
            handleSelect("f:" + r.flow.id);
            window.__openLeaf?.(r.id);
          }
        }}
      />

      <ZoomControls
        onZoomIn={() => {
          if (selected) setFocusReq({ id: selected, t: Date.now() });
          else setFocusReq({ id: null, t: Date.now() });
        }}
        onZoomOut={() => { setSelected(null); setFocusReq({ id: null, t: Date.now() }); }}
        onFit={() => { setSelected(null); setFocusReq({ id: null, t: Date.now() }); }}
      />

      {tweaks.showHints && <Hint visible={showHint && !selected} />}

      <window.TweaksPanel title="Tweaks">
        <window.TweakSection title="Motion">
          <window.TweakSlider label="Wiggle" min={0} max={2.5} step={0.05} value={tweaks.wiggle} onChange={(v) => setTweak("wiggle", v)} />
        </window.TweakSection>
        <window.TweakSection title="Layout">
          <window.TweakToggle label="Sidebar" value={tweaks.showSidebar} onChange={(v) => setTweak("showSidebar", v)} />
          <window.TweakToggle label="Hints overlay" value={tweaks.showHints} onChange={(v) => setTweak("showHints", v)} />
        </window.TweakSection>
      </window.TweaksPanel>
    </div>
  );
}

window.AtlasApp = App;
