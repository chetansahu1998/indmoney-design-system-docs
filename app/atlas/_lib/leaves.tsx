// @ts-nocheck
"use client";
// Ported verbatim from INDmoney Docs/leaves.jsx (mock seed data +
// per-leaf builder functions). Phase 4–5 replace consumers with
// adapter-driven values from lib/atlas/data-adapters.ts.
// ============================================================
// LEAVES — third-level nodes (sub-flows under each flow).
// Each leaf has a list of "frames" (Figma-frame mocks).
//
// Phase 4 wiring: AtlasShell sets `window.LEAVES = realLeavesFromAdapter`
// before this module imports. We only assign the mock when no real data is
// already present, so fallbacks still work for the standalone HTML preview.
// ============================================================

// Standalone HTML preview never sets __ATLAS_DATA_READY, so the showcase
// mock data ships through. Inside the Next.js shell, AtlasShell sets the
// flag + writes the real LEAVES array (possibly empty) before this module
// imports — we then skip the mock entirely so an empty real DB doesn't
// silently render the 95-leaf showcase.
if (!(window as any).__ATLAS_DATA_READY) {
window.LEAVES = [
  // ---- platform / kyc ----
  { id: "kyc-pan",        flow: "kyc",       label: "PAN verification",       frames: 6,  frameKind: "pan",     violations: 4 },
  { id: "kyc-aadhaar",    flow: "kyc",       label: "Aadhaar OTP",            frames: 5,  frameKind: "otp",     violations: 0 },
  { id: "kyc-selfie",     flow: "kyc",       label: "Liveness · Selfie",      frames: 4,  frameKind: "selfie",  violations: 2 },
  { id: "kyc-bank",       flow: "kyc",       label: "Bank verification",      frames: 7,  frameKind: "bank",    violations: 3 },
  { id: "kyc-nominee",    flow: "kyc",       label: "Nominee details",        frames: 3,  frameKind: "form",    violations: 1 },
  { id: "kyc-fatca",      flow: "kyc",       label: "FATCA declaration",      frames: 5,  frameKind: "form",    violations: 7 },
  { id: "kyc-risk",       flow: "kyc",       label: "Risk profiling",         frames: 9,  frameKind: "quiz",    violations: 0 },
  { id: "kyc-status",     flow: "kyc",       label: "Status & rejection",     frames: 4,  frameKind: "status",  violations: 5 },

  // ---- platform / auth ----
  { id: "auth-login",     flow: "auth",      label: "Login",                  frames: 5,  frameKind: "login",   violations: 1 },
  { id: "auth-mpin",      flow: "auth",      label: "MPIN setup",             frames: 4,  frameKind: "pin",     violations: 0 },
  { id: "auth-bio",       flow: "auth",      label: "Biometric",              frames: 3,  frameKind: "bio",     violations: 2 },
  { id: "auth-2fa",       flow: "auth",      label: "2FA",                    frames: 4,  frameKind: "otp",     violations: 0 },
  { id: "auth-devices",   flow: "auth",      label: "Trusted devices",        frames: 3,  frameKind: "list",    violations: 1 },

  { id: "settings-profile", flow: "settings", label: "Profile",               frames: 4,  frameKind: "profile", violations: 2 },
  { id: "settings-banks",   flow: "settings", label: "Linked banks",          frames: 6,  frameKind: "bank",    violations: 3 },
  { id: "settings-privacy", flow: "settings", label: "Privacy & data",        frames: 5,  frameKind: "privacy", violations: 0 },

  { id: "notif-inbox",    flow: "notif",     label: "Inbox",                  frames: 3,  frameKind: "inbox",   violations: 0 },
  { id: "notif-prefs",    flow: "notif",     label: "Preferences",            frames: 4,  frameKind: "prefs",   violations: 1 },

  // ---- markets ----
  { id: "stocks-detail",  flow: "stocks",    label: "Stock detail page",      frames: 12, frameKind: "stockd",  violations: 8 },
  { id: "stocks-buy",     flow: "stocks",    label: "Buy order ticket",       frames: 7,  frameKind: "order",   violations: 3 },
  { id: "stocks-sell",    flow: "stocks",    label: "Sell order ticket",      frames: 7,  frameKind: "order",   violations: 2 },
  { id: "stocks-charts",  flow: "stocks",    label: "Charts & technicals",    frames: 5,  frameKind: "chart",   violations: 6 },
  { id: "stocks-news",    flow: "stocks",    label: "News & filings",         frames: 4,  frameKind: "news",    violations: 0 },
  { id: "stocks-peers",   flow: "stocks",    label: "Peers & financials",    frames: 6,  frameKind: "peers",   violations: 1 },

  { id: "us-detail",      flow: "us",        label: "US stock detail",        frames: 9,  frameKind: "stockd",  violations: 4 },
  { id: "us-tradeplus",   flow: "us",        label: "Tradeplus order",        frames: 6,  frameKind: "order",   violations: 2 },
  { id: "us-fx",          flow: "us",        label: "FX & LRS",               frames: 5,  frameKind: "fx",      violations: 0 },

  { id: "mf-detail",      flow: "mf",        label: "Fund detail",            frames: 10, frameKind: "fund",    violations: 5 },
  { id: "mf-sip",         flow: "mf",        label: "Start SIP",              frames: 8,  frameKind: "sip",     violations: 1 },
  { id: "mf-lumpsum",     flow: "mf",        label: "Lumpsum invest",         frames: 5,  frameKind: "order",   violations: 0 },
  { id: "mf-switch",      flow: "mf",        label: "Switch fund",            frames: 4,  frameKind: "switch",  violations: 2 },

  { id: "fno-chain",      flow: "fno",       label: "Option chain",           frames: 6,  frameKind: "chain",   violations: 11 },
  { id: "fno-strategy",   flow: "fno",       label: "Strategy builder",       frames: 8,  frameKind: "strategy",violations: 3 },
  { id: "fno-margin",     flow: "fno",       label: "Margin calculator",      frames: 4,  frameKind: "calc",    violations: 0 },

  { id: "smallcase-disc", flow: "smallcase", label: "Discover",               frames: 4,  frameKind: "list",    violations: 1 },
  { id: "smallcase-detail",flow: "smallcase",label: "Smallcase detail",       frames: 5,  frameKind: "fund",    violations: 0 },

  { id: "ipo-live",       flow: "ipo",       label: "Live IPOs",              frames: 3,  frameKind: "list",    violations: 0 },
  { id: "ipo-apply",      flow: "ipo",       label: "Apply for IPO",          frames: 6,  frameKind: "ipo",     violations: 2 },
  { id: "ipo-mandate",    flow: "ipo",       label: "UPI mandate",            frames: 4,  frameKind: "mandate", violations: 0 },

  { id: "etf-detail",     flow: "etf",       label: "ETF detail",             frames: 5,  frameKind: "fund",    violations: 1 },
  { id: "bonds-detail",   flow: "bonds",     label: "Bond detail",            frames: 4,  frameKind: "fund",    violations: 0 },
  { id: "bonds-tbill",    flow: "bonds",     label: "T-Bills",                frames: 3,  frameKind: "fund",    violations: 0 },

  // ---- investing ----
  { id: "portfolio-overview", flow: "portfolio", label: "Overview",           frames: 6,  frameKind: "overview",violations: 3 },
  { id: "portfolio-allocation", flow: "portfolio", label: "Allocation",       frames: 4,  frameKind: "donut",   violations: 1 },
  { id: "portfolio-returns", flow: "portfolio", label: "Returns & XIRR",      frames: 5,  frameKind: "chart",   violations: 2 },
  { id: "portfolio-holdings",flow: "portfolio", label: "Holdings",            frames: 4,  frameKind: "list",    violations: 0 },
  { id: "portfolio-cg",   flow: "portfolio", label: "Capital gains",          frames: 5,  frameKind: "report",  violations: 4 },

  { id: "research-ideas", flow: "research",  label: "Investment ideas",       frames: 5,  frameKind: "ideas",   violations: 0 },
  { id: "research-sectors",flow: "research", label: "Sectors deep-dive",      frames: 4,  frameKind: "sectors", violations: 1 },
  { id: "research-reports",flow: "research", label: "Analyst reports",        frames: 3,  frameKind: "list",    violations: 0 },

  { id: "orders-book",    flow: "orders",    label: "Order book",             frames: 5,  frameKind: "list",    violations: 2 },
  { id: "orders-gtt",     flow: "orders",    label: "GTT orders",             frames: 4,  frameKind: "gtt",     violations: 0 },

  { id: "watchlist-list", flow: "watchlist", label: "My watchlist",           frames: 3,  frameKind: "list",    violations: 0 },
  { id: "watchlist-alerts",flow: "watchlist",label: "Price alerts",           frames: 4,  frameKind: "alert",   violations: 1 },

  { id: "tax-cg",         flow: "tax",       label: "Capital gains report",   frames: 6,  frameKind: "report",  violations: 3 },
  { id: "tax-harvest",    flow: "tax",       label: "Tax harvesting",         frames: 4,  frameKind: "harvest", violations: 0 },

  // ---- money ----
  { id: "wallet-balance", flow: "wallet",    label: "Wallet balance",         frames: 3,  frameKind: "wallet",  violations: 0 },
  { id: "wallet-add",     flow: "wallet",    label: "Add money",              frames: 5,  frameKind: "addmoney",violations: 1 },
  { id: "wallet-withdraw",flow: "wallet",    label: "Withdraw",               frames: 4,  frameKind: "withdraw",violations: 2 },

  { id: "plutus-hub",     flow: "plutus",    label: "Card hub",               frames: 4,  frameKind: "card",    violations: 0 },
  { id: "plutus-activate",flow: "plutus",    label: "Activate card",          frames: 6,  frameKind: "activate",violations: 1 },
  { id: "plutus-rewards", flow: "plutus",    label: "Rewards",                frames: 3,  frameKind: "rewards", violations: 0 },

  { id: "deposit-upi",    flow: "deposit",   label: "UPI deposit",            frames: 5,  frameKind: "upi",     violations: 0 },
  { id: "deposit-neft",   flow: "deposit",   label: "NEFT deposit",           frames: 4,  frameKind: "neft",    violations: 1 },
];
} // end of mock-fallback guard

// Index by flow (always rebuild from whatever LEAVES ended up being —
// real data from the adapter or the mock fallback above).
window.LEAVES_BY_FLOW = window.LEAVES.reduce((acc, l) => {
  (acc[l.flow] ??= []).push(l);
  return acc;
}, {});

// ============================================================
// Generate plausible violation data for a leaf, deterministically.
// ============================================================
const RULES = [
  { id: "color-token", label: "Color must use design token", severity: "error" },
  { id: "spacing-grid", label: "Spacing must align to 4px grid", severity: "warning" },
  { id: "type-scale", label: "Font size must be from type scale", severity: "warning" },
  { id: "tap-target", label: "Tap target ≥ 44px", severity: "error" },
  { id: "contrast", label: "Text contrast ≥ 4.5:1 (AA)", severity: "error" },
  { id: "radius-token", label: "Border radius must use token", severity: "info" },
  { id: "elevation", label: "Shadow must be from elevation scale", severity: "warning" },
  { id: "icon-size", label: "Icon size must be 16/20/24", severity: "info" },
  { id: "naming", label: "Layer name must follow BEM", severity: "info" },
  { id: "component-instance", label: "Use main component, not detached", severity: "warning" },
  { id: "i18n", label: "String must be in translation table", severity: "warning" },
  { id: "a11y-label", label: "Interactive element needs aria-label", severity: "error" },
];

const LAYER_SAMPLES = [
  "Frame/Header/Title",
  "Frame/Body/Card/Heading",
  "Frame/Body/Card/Body",
  "Frame/Body/Form/Input/Label",
  "Frame/Body/Form/Input/Field",
  "Frame/Body/CTA/Primary",
  "Frame/Body/CTA/Secondary",
  "Frame/Footer/Disclaimer",
  "Frame/Body/List/Item/Avatar",
  "Frame/Body/List/Item/Title",
  "Frame/Body/Chart/Axis/Label",
  "Frame/Modal/Header/Close",
  "Frame/Body/Tabs/Active",
  "Frame/Body/EmptyState/Illustration",
  "Frame/Body/Banner/Icon",
];

function seededRand(seed) {
  let s = seed | 0;
  return () => {
    s = (s + 0x6D2B79F5) | 0;
    let t = Math.imul(s ^ (s >>> 15), 1 | s);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}
function hashString(str) {
  let h = 0;
  for (let i = 0; i < str.length; i++) h = ((h << 5) - h + str.charCodeAt(i)) | 0;
  return h;
}

window.buildViolations = function (leaf) {
  if (!leaf || !leaf.violations) return [];
  const r = seededRand(hashString(leaf.id));
  const out = [];
  const statuses = ["active", "active", "active", "acknowledged", "fixed"];
  for (let i = 0; i < leaf.violations; i++) {
    const rule = RULES[Math.floor(r() * RULES.length)];
    const layer = LAYER_SAMPLES[Math.floor(r() * LAYER_SAMPLES.length)];
    const frameIdx = Math.floor(r() * leaf.frames);
    const status = statuses[Math.floor(r() * statuses.length)];
    out.push({
      id: `${leaf.id}-v${i}`,
      severity: rule.severity,
      rule: rule.label,
      ruleId: rule.id,
      layer,
      frameIdx,
      status,
      detail: layer.split("/").pop() + " uses raw value, not token",
      ago: ["2h", "6h", "1d", "2d", "3d", "1w"][Math.floor(r() * 6)] + " ago",
    });
  }
  return out;
};

window.buildDecisions = function (leaf) {
  if (!leaf) return [];
  const r = seededRand(hashString(leaf.id) ^ 9999);
  const samples = [
    { title: "Pill buttons over rectangular CTAs", body: "We chose pill (radius 999) here because the screen sits in a card-heavy layout and we want tactile separation from rectangular containers.", author: "Aanya P.", ago: "3d ago", linksTo: 0 },
    { title: "Skip OTP on returning device", body: "If device is in trusted-devices list AND last login < 7 days, we skip OTP. Reduces drop-off ~14% in funnels A/B.", author: "Rohit M.", ago: "1w ago", linksTo: 1 },
    { title: "Defer FATCA to investment moment", body: "Showing FATCA at signup creates 22% drop. We've moved it to first-investment block.", author: "Sneha K.", ago: "2w ago" },
    { title: "Dark mode parity for charts only", body: "Charts get full dark variant; secondary surfaces still use light tokens. Engineering bandwidth limit.", author: "Kabir J.", ago: "3w ago" },
  ];
  const n = 1 + Math.floor(r() * Math.min(4, samples.length));
  return samples.slice(0, n).map((s, i) => ({ ...s, id: `${leaf.id}-d${i}` }));
};

window.buildActivity = function (leaf) {
  if (!leaf) return [];
  return [
    { who: "Aanya P.", what: "edited DRD",                    ago: "2h ago",  kind: "edit" },
    { who: "Rohit M.", what: "acknowledged 2 violations",      ago: "1d ago",  kind: "violation" },
    { who: "Audit",    what: "flagged 3 new violations",       ago: "1d ago",  kind: "audit" },
    { who: "Sneha K.", what: "added a decision",               ago: "3d ago",  kind: "decision" },
    { who: "Kabir J.", what: "synced from Figma (rev 47)",     ago: "5d ago",  kind: "sync" },
    { who: "Aanya P.", what: "renamed leaf",                   ago: "1w ago",  kind: "edit" },
  ];
};

window.buildComments = function (leaf) {
  if (!leaf) return [];
  return [
    { who: "Aanya P.", body: "Should the CTA copy match the brand tone guide on the next variant?", ago: "5h ago", reactions: 2 },
    { who: "Rohit M.", body: "+1 — and the disclaimer's height looks off vs. the rest of the flow.", ago: "4h ago", reactions: 0 },
    { who: "Sneha K.", body: "I'll log a decision here once the legal copy lands.", ago: "2h ago", reactions: 1 },
  ];
};

// ============================================================
// LAYOUT GENERATOR — frames on the canvas, with connections.
// Returns { frames: [{id,x,y,w,h,kind,label,idx}], edges: [{from,to,kind}] }
// Uses a horizontal "river" with branches, much like a Figma flow.
// ============================================================
window.buildLeafCanvas = function (leaf) {
  const n = leaf.frames;
  const r = seededRand(hashString(leaf.id) ^ 4242);
  // Frame dimensions: phone-aspect (375x812) scaled
  const FW = 280, FH = 580;
  const GAP = 90;
  const ROW_GAP = 140;

  const frames = [];
  const labels = labelsForKind(leaf.frameKind, n);

  // Layout strategy:
  //  - main row of n frames left to right
  //  - if n > 6, branch off into a second row at index 4
  let row = 0, col = 0;
  for (let i = 0; i < n; i++) {
    if (n > 6 && i === 4) { row = 1; col = 2; }
    const x = col * (FW + GAP);
    const y = row * (FH + ROW_GAP);
    frames.push({
      id: `${leaf.id}-fr${i}`,
      idx: i,
      x, y, w: FW, h: FH,
      kind: leaf.frameKind,
      label: labels[i] || `Frame ${i + 1}`,
    });
    col++;
  }

  // Edges: linear chain with one branch from frame 1 → branch frame
  const edges = [];
  for (let i = 0; i < frames.length - 1; i++) {
    if (n > 6 && i === 3) {
      edges.push({ from: frames[i].id, to: frames[i + 1].id, kind: "branch" });
    } else {
      edges.push({ from: frames[i].id, to: frames[i + 1].id, kind: "main" });
    }
  }
  // For long flows, add a back-edge ("error → retry") to make it feel real
  if (n >= 5) {
    edges.push({ from: frames[Math.min(2, n - 1)].id, to: frames[1].id, kind: "back" });
  }

  return { frames, edges };
};

function labelsForKind(kind, n) {
  const map = {
    pan:      ["Enter PAN", "Verify PAN", "Name match", "DOB match", "Confirm details", "Success"],
    otp:      ["Send OTP", "Enter OTP", "Verifying", "Success", "Resend"],
    selfie:   ["Instructions", "Camera", "Capture", "Verify"],
    bank:     ["Add bank", "IFSC", "Verify A/C", "₹1 test", "Confirm", "Success", "Failed retry"],
    form:     ["Form", "Validate", "Confirm", "Success", "Error"],
    quiz:     ["Q1 Goals", "Q2 Horizon", "Q3 Income", "Q4 Risk", "Q5 Loss", "Q6 Volatility", "Q7 Knowledge", "Result", "Confirm"],
    status:   ["Pending", "In review", "Action needed", "Rejected"],
    login:    ["Phone", "OTP", "MPIN", "Biometric prompt", "Home"],
    pin:      ["Set PIN", "Confirm PIN", "Success", "Error"],
    bio:      ["Setup biometric", "Verify", "Success"],
    list:     ["List", "Empty", "Filter", "Detail"],
    profile:  ["Profile", "Edit", "Save", "Success"],
    privacy:  ["Privacy hub", "Permissions", "Data export", "Delete account", "Confirm"],
    inbox:    ["Inbox", "Notification detail", "Empty"],
    prefs:    ["Categories", "Channels", "Quiet hours", "Save"],
    stockd:   ["Quote header", "Charts", "Order CTA", "About", "Financials", "Peers", "News", "Holdings", "Insights", "Filings", "Events", "Recos"],
    order:    ["Place order", "Quantity", "Price type", "Margin", "Confirm", "Placed", "Failed"],
    chart:    ["Chart", "Indicators", "Timeframe", "Compare", "Drawing tools"],
    news:     ["News list", "Article", "Filings", "Empty"],
    peers:    ["Peers list", "Compare", "Financials", "Ratios", "Empty", "Detail"],
    fx:       ["FX rate", "LRS form", "Confirm", "Success", "Failed"],
    fund:     ["Fund header", "Returns", "Risk", "Holdings", "Manager", "Documents", "Similar", "FAQs", "Reviews", "Insights"],
    sip:      ["Pick fund", "Amount", "Date", "Mandate", "Confirm", "Success", "Failed", "Edit"],
    switch:   ["Switch from", "Switch to", "Confirm", "Success"],
    chain:    ["Chain", "Strikes", "Greeks", "Order", "Margin", "Payoff"],
    strategy: ["Strategy list", "Builder", "Legs", "Margin", "Payoff", "Confirm", "Placed", "Edit"],
    calc:     ["Inputs", "Result", "Breakdown", "Reset"],
    ipo:      ["Live IPOs", "Detail", "Apply form", "Mandate", "Success", "Failed"],
    mandate:  ["Mandate", "App switch", "Approved", "Failed"],
    overview: ["Hero card", "Allocation donut", "Returns chart", "Top movers", "Insights", "Documents"],
    donut:    ["Allocation", "Asset class", "Sector", "Stock"],
    report:   ["Header", "Summary", "Breakdown", "Download CTA", "Email"],
    ideas:    ["Ideas list", "Idea detail", "Author", "Comments", "Save"],
    sectors:  ["Sector grid", "Detail", "Holdings", "Trends"],
    gtt:      ["List", "Create", "Confirm", "Triggered"],
    alert:    ["Create alert", "Active alerts", "Triggered", "History"],
    harvest:  ["Hero", "Suggested", "Preview", "Confirm"],
    wallet:   ["Balance", "Recent", "Limits"],
    addmoney: ["Amount", "Method", "UPI", "Confirm", "Success"],
    withdraw: ["Amount", "Bank", "Confirm", "Success"],
    card:     ["Hub", "Card front", "Limits", "Settings"],
    activate: ["Welcome", "Set PIN", "Confirm PIN", "Test txn", "Active", "Failed"],
    rewards:  ["Rewards", "Detail", "Redeem"],
    upi:      ["Amount", "App switch", "Approving", "Success", "Failed"],
    neft:     ["Amount", "Bank picker", "Verifying", "Success"],
  };
  return map[kind] || Array.from({ length: n }, (_, i) => `Frame ${i + 1}`);
}
