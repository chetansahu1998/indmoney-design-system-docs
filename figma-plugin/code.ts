/**
 * INDmoney DS Sync — Figma plugin main thread.
 *
 * The plugin is a designer-side publishing + audit tool, not a download
 * surface. Three flows, in priority order:
 *
 *   1. Publish — designer multi-selects in Figma, plugin auto-recognises
 *      sets / standalone components / variants / instances / custom, shows
 *      a live preview, then POSTs to ds-service /v1/publish which persists
 *      to lib/contributions/<file>.json. The DS catches new components
 *      from real designs without anyone hand-editing manifests.
 *
 *   2. Audit — selection / page / file → ds-service /v1/audit/run → fix
 *      cards with click-to-apply via Figma variable APIs.
 *
 *   3. Inject — pull existing icons/components from the docs site into the
 *      file. Kept for the rare "scaffold a new design" flow but it's the
 *      tertiary tab now.
 *
 * Lifecycle:
 *   - On boot: connect to local audit-server, start health polling.
 *   - On selectionchange: classify + emit live summary to UI.
 *   - On user action (publish / audit / inject): show progress, POST,
 *     surface result via toast queue.
 */

/* ── Shared message types ──────────────────────────────────────────── */

interface MessageFromUI {
  type:
    | "ready"
    | "set-server-url"
    | "publish"
    | "audit"
    | "apply-fix"
    | "apply-group"
    | "apply-many"
    | "inject"
    | "open-docs"
    | "request-selection-summary"
    | "projects.set-active"
    | "projects.refresh-detection"
    | "projects.send"
    /** Phase 3 U9 — UI signals user dismissed the first-run modal.
     *  Code sets the durable clientStorage flag so the modal stays
     *  hidden across future plugin runs. */
    | "projects.firstrun-dismiss"
    /** Phase 4 U11 — UI requests the auto-fix flow for a violation_id.
     *  Payload: { slug, violation_id, target?: { token_path?: string;
     *  text_style_id?: string; observed_number?: number } }. Plugin
     *  fetches the violation, builds the fix preview, applies on the
     *  Figma node, then POSTs /fix-applied. */
    | "projects.autofix";
  payload?: unknown;
}

interface MessageToUI {
  type:
    | "selection-summary"
    | "health"
    | "publish-result"
    | "audit-summary"
    | "audit-fixes"
    | "audit-matches"
    | "audit-applied"
    | "group-applied"
    | "many-applied"
    | "inject-progress"
    | "toast"
    | "projects.detected-groups"
    | "projects.send"
    | "projects.send-result"
    | "projects.send-progress"
    /** Phase 3 U9 — Code reports first-run state to the UI on boot
     *  ({ seen: boolean }). UI shows the welcome modal only when seen
     *  is false. */
    | "projects.firstrun-status";
  payload?: unknown;
}

const send = (msg: MessageToUI) => figma.ui.postMessage(msg);

/* ── State ──────────────────────────────────────────────────────────── */

let auditServerURL = "http://localhost:7474";
let docsURL = "https://indmoney-design-system-docs.vercel.app";
let healthTimer: ReturnType<typeof setInterval> | null = null;
let knownVariables: Record<string, Variable> = {};

// Projects mode (U3) — when the UI is on the Projects tab, selectionchange
// re-runs detection. The flag is toggled from the UI ("projects.set-active").
// Detection itself is debounced to coalesce rapid marquee-drag selectionchange
// bursts.
let projectsModeActive = false;
let projectsDetectTimer: ReturnType<typeof setTimeout> | null = null;
let allPagesLoaded = false;

// 440 × 720 — comfortable for the redesigned three-mode shell. Mode picker
// + status + scrollable pane + toaster all fit without crowding at this
// size, and the plugin is still narrow enough to dock alongside the canvas.
figma.showUI(__html__, { width: 440, height: 720, themeColors: true });

figma.ui.onmessage = async (msg: MessageFromUI) => {
  try {
    switch (msg.type) {
      case "ready":
        startHealthPolling();
        emitSelectionSummary();
        // Phase 3 U9: first-run check. Read the durable flag from
        // clientStorage and emit the result so the UI can show its
        // welcome modal exactly once per Figma profile. New profiles
        // (or clientStorage cleared) → seen: false → modal renders.
        {
          const seen =
            (await figma.clientStorage.getAsync("projects.firstrun-seen")) === true;
          send({ type: "projects.firstrun-status", payload: { seen } });
        }
        return;
      case "projects.firstrun-dismiss":
        // Persists across plugin re-runs. Cleared via Figma's clientStorage
        // reset (Plugins → Manage plugins → reinstall) or by deleting the
        // key manually for QA.
        await figma.clientStorage.setAsync("projects.firstrun-seen", true);
        return;
      case "set-server-url":
        if (typeof msg.payload === "string" && msg.payload.startsWith("http")) {
          const v = msg.payload.replace(/\/$/, "");
          if (v.includes("localhost") || v.includes("127.0.0.1") || v.startsWith("http://localhost") || v.startsWith("https://")) {
            auditServerURL = v;
            await figma.clientStorage.setAsync("audit_server_url", v);
            void pollHealth();
          }
        }
        return;
      case "publish":
        return runPublish();
      case "audit":
        return runAudit((msg.payload as { scope: AuditScope }).scope);
      case "apply-fix":
        return applyFix(msg.payload as ApplyFixPayload);
      case "apply-group":
        return applyGroup(msg.payload as ApplyGroupPayload);
      case "apply-many":
        return applyMany(msg.payload as ApplyManyPayload);
      case "inject":
        return runInject((msg.payload as { kind: "icon" | "component" }).kind);
      case "open-docs":
        figma.openExternal(docsURL);
        return;
      case "request-selection-summary":
        return emitSelectionSummary();
      case "projects.set-active":
        projectsModeActive = !!(msg.payload && (msg.payload as any).active);
        if (projectsModeActive) await runProjectsDetection();
        return;
      case "projects.refresh-detection":
        return runProjectsDetection();
      case "projects.send":
        // U3: backend wiring lands in U4. For now we only echo a "result" so
        // the UI re-enables its Send button cleanly. Plugin code never
        // serializes the payload here — UI logs it to the JS console as a
        // placeholder. We just confirm receipt.
        send({
          type: "projects.send-result",
          payload: {
            ok: true,
            phase: "u3-placeholder",
            note: "Send wired to console.log only; backend lands in U4.",
          },
        });
        toast("Projects payload logged to console (U3 placeholder).", "info");
        return;
      case "projects.autofix":
        return runAutoFix(msg.payload as AutoFixPayload);
    }
  } catch (e: unknown) {
    toast(`Error: ${(e as Error).message ?? "unknown"}`, "error");
  }
};

// Selection-change watcher — emits a live summary so the UI can update without
// the user clicking anything. When Projects mode is active, also re-runs the
// smart-grouping detection (debounced 150ms to coalesce marquee bursts).
figma.on("selectionchange", () => {
  emitSelectionSummary();
  if (projectsModeActive) {
    if (projectsDetectTimer) clearTimeout(projectsDetectTimer);
    projectsDetectTimer = setTimeout(() => {
      void runProjectsDetection();
    }, 150);
  }
});

// Restore stored URL on boot.
(async () => {
  const stored = (await figma.clientStorage.getAsync("audit_server_url")) as string | undefined;
  if (stored) auditServerURL = stored;
})();

// Direct menu commands.
if (figma.command === "auditFile") runAudit("file").catch(() => {});
if (figma.command === "auditSelection") runAudit("selection").catch(() => {});
if (figma.command === "publishSelection") runPublish().catch(() => {});
if (figma.command === "openProjects") {
  // The "projects.open-via-command" hint tells the UI to switch to the
  // Projects tab on its first message-pump tick. UI handles the toggle —
  // we just flag intent here.
  send({ type: "toast", payload: { message: "Switching to Projects mode…", level: "info", ts: Date.now() } });
  // Defer so the UI has its message handlers in place. The UI listens for
  // a synthetic toast and additionally for a dedicated payload on
  // projects.send-progress, but the simplest contract is: UI clicks its
  // own mode-projects button after seeing the menu intent. We piggyback on
  // a special `projects.send-progress` shape.
  setTimeout(() => {
    send({ type: "projects.send-progress", payload: { phase: "open-via-command" } });
  }, 100);
}
if (figma.command === "openDocsSite") {
  figma.openExternal(docsURL);
  figma.closePlugin();
}

/* ── Health polling ─────────────────────────────────────────────────── */

function startHealthPolling() {
  if (healthTimer) clearInterval(healthTimer);
  void pollHealth();
  healthTimer = setInterval(() => void pollHealth(), 5000);
}

async function pollHealth() {
  try {
    const r = await fetch(`${auditServerURL}/__health`, { method: "GET" });
    if (r.ok) {
      const body = (await r.json()) as { ok: boolean; schema_version: string; repo: string };
      send({
        type: "health",
        payload: {
          connected: true,
          server_url: auditServerURL,
          schema_version: body.schema_version,
          repo: body.repo,
          file_key: (figma as any).fileKey || null,
          file_name: figma.root.name,
        },
      });
      return;
    }
    send({
      type: "health",
      payload: {
        connected: false,
        server_url: auditServerURL,
        error: `HTTP ${r.status}`,
        diagnosis: `Server reached at ${auditServerURL} but returned ${r.status}. Restart the audit-server.`,
      },
    });
  } catch (e) {
    const msg = (e as Error).message ?? "unknown";
    let diagnosis = `Cannot reach ${auditServerURL}. Run \`npm run audit:serve\` in the docs repo.`;
    if (msg.toLowerCase().includes("cors") || msg.toLowerCase().includes("blocked")) {
      diagnosis = `CORS blocked the request. The audit-server allows any origin by default — double-check it's the version from this repo.`;
    } else if (msg.toLowerCase().includes("failed to fetch") || msg.toLowerCase().includes("network")) {
      diagnosis = `Network error. Is \`npm run audit:serve\` actually running? Try opening ${auditServerURL}/__health in a browser tab to confirm.`;
    }
    send({
      type: "health",
      payload: { connected: false, server_url: auditServerURL, error: msg, diagnosis },
    });
  }
}

/* ── Selection classification ───────────────────────────────────────── */

type SelectionKind =
  | "component_set"
  | "component_standalone"
  | "component_variant"
  | "instance"
  | "frame"
  | "other";

interface SelectionItem {
  figma_id: string;
  name: string;
  kind: SelectionKind;
  width: number;
  height: number;
  // Only populated for kind=component_set
  variants?: Array<{
    variant_id: string;
    name: string;
    properties: Array<{ name: string; value: string }>;
    width: number;
    height: number;
  }>;
  // For kind=component_variant
  parent_set_id?: string;
  parent_set_name?: string;
  properties?: Array<{ name: string; value: string }>;
  // For kind=instance
  instance_of_id?: string;
  instance_of_name?: string;
}

interface SelectionSummary {
  total: number;
  byKind: Record<SelectionKind, number>;
  items: SelectionItem[]; // capped to 50 for UI; full list captured at publish time
  variantTotal: number;   // sum of variants across all selected sets
  publishable: number;     // count we'd actually push (sets + standalone + variants only)
}

function classifyNode(node: SceneNode): SelectionItem {
  const base: SelectionItem = {
    figma_id: node.id,
    name: node.name,
    kind: "other",
    width: "width" in node ? Math.round(node.width) : 0,
    height: "height" in node ? Math.round(node.height) : 0,
  };

  switch (node.type) {
    case "COMPONENT_SET": {
      base.kind = "component_set";
      base.variants = (node.children as readonly SceneNode[])
        .filter((c) => c.type === "COMPONENT")
        .map((c) => ({
          variant_id: c.id,
          name: c.name,
          properties: parseVariantProps(c.name),
          width: "width" in c ? Math.round(c.width) : 0,
          height: "height" in c ? Math.round(c.height) : 0,
        }));
      return base;
    }
    case "COMPONENT": {
      const parent = node.parent;
      if (parent && parent.type === "COMPONENT_SET") {
        base.kind = "component_variant";
        base.parent_set_id = parent.id;
        base.parent_set_name = parent.name;
        base.properties = parseVariantProps(node.name);
      } else {
        base.kind = "component_standalone";
      }
      return base;
    }
    case "INSTANCE": {
      base.kind = "instance";
      const inst = node as InstanceNode;
      // Async getMainComponentAsync would be ideal but we're in sync classify;
      // mainComponent is available synchronously when not loaded from a library.
      try {
        const main = inst.mainComponent;
        if (main) {
          base.instance_of_id = main.id;
          base.instance_of_name = main.name;
        }
      } catch {}
      return base;
    }
    case "FRAME":
    case "GROUP":
      base.kind = "frame";
      return base;
    default:
      return base;
  }
}

function parseVariantProps(name: string): Array<{ name: string; value: string }> {
  return name
    .split(",")
    .map((part) => part.trim())
    .filter(Boolean)
    .map((part) => {
      const eq = part.indexOf("=");
      if (eq < 0) return null;
      return {
        name: part.slice(0, eq).trim(),
        value: part.slice(eq + 1).trim(),
      };
    })
    .filter(Boolean) as Array<{ name: string; value: string }>;
}

function emitSelectionSummary() {
  const sel = figma.currentPage.selection;
  const items = sel.map(classifyNode);
  const byKind: Record<SelectionKind, number> = {
    component_set: 0,
    component_standalone: 0,
    component_variant: 0,
    instance: 0,
    frame: 0,
    other: 0,
  };
  let variantTotal = 0;
  let publishable = 0;
  for (const i of items) {
    byKind[i.kind]++;
    if (i.variants) variantTotal += i.variants.length;
    if (i.kind === "component_set" || i.kind === "component_standalone" || i.kind === "component_variant") {
      publishable++;
    }
  }
  const summary: SelectionSummary = {
    total: items.length,
    byKind,
    items: items.slice(0, 50),
    variantTotal,
    publishable,
  };
  send({ type: "selection-summary", payload: summary });
}

/* ── Publish ────────────────────────────────────────────────────────── */

async function runPublish() {
  const sel = figma.currentPage.selection;
  if (sel.length === 0) {
    toast("Select at least one component first.", "error");
    return;
  }
  // Filter out instances and frames at upload-time — the user picked them but
  // they're not publishable as DS entries; keep them in the metadata for log
  // visibility but mark `kind: "other"` server-side won't reject.
  const items = sel.map(classifyNode).filter((i) => i.kind !== "frame" && i.kind !== "other");
  if (items.length === 0) {
    toast("Nothing publishable in the selection — pick a Component Set, Component, or Variant.", "error");
    return;
  }

  toast(`Publishing ${items.length} item${items.length === 1 ? "" : "s"}…`, "info");
  const fileKey = (figma as any).fileKey || "unknown";
  const fileName = figma.root.name;
  const captured = items.map((i) => ({
    kind: kindToServerKind(i.kind),
    figma_id: i.figma_id,
    name: i.name,
    component_set_id: i.parent_set_id,
    parent_set_name: i.parent_set_name,
    parent_set_id: i.parent_set_id,
    variants: i.variants?.map((v) => ({
      variant_id: v.variant_id,
      name: v.name,
      properties: v.properties,
      width: v.width,
      height: v.height,
    })),
    properties: i.properties,
    width: i.width,
    height: i.height,
    captured_at: new Date().toISOString(),
  }));

  let r: Response;
  try {
    r = await fetch(`${auditServerURL}/v1/publish`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ file_key: fileKey, file_name: fileName, brand: "indmoney", selections: captured }),
    });
  } catch (e) {
    toast(`Audit server unreachable. Run \`npm run audit:serve\`.`, "error");
    return;
  }
  if (!r.ok) {
    const body = await r.text().catch(() => "");
    toast(`Publish failed (${r.status}): ${body || "unknown"}`, "error");
    return;
  }
  const data = (await r.json()) as {
    ok: boolean;
    written_path: string;
    new_count: number;
    updated_count: number;
    total_count: number;
  };
  send({ type: "publish-result", payload: data });
  const adds = data.new_count;
  const updates = data.updated_count;
  toast(
    `${adds} added, ${updates} updated · committed at lib/contributions/`,
    "success",
  );
}

function kindToServerKind(k: SelectionKind): string {
  switch (k) {
    case "component_set":      return "component_set";
    case "component_standalone": return "component_standalone";
    case "component_variant":  return "component_variant";
    case "instance":           return "instance";
    default:                   return "other";
  }
}

/* ── Audit ──────────────────────────────────────────────────────────── */

type AuditScope = "selection" | "page" | "file";

async function runAudit(scope: AuditScope) {
  const tree = collectNodeTreeForAudit(scope);
  if (tree === null) {
    toast(scope === "selection" ? "Select at least one layer first." : `${scope} is empty.`, "error");
    return;
  }
  const fileKey = (figma as any).fileKey || "unknown";
  const fileName = figma.root.name;

  toast(`Auditing ${scope}…`, "info");
  let r: Response;
  try {
    r = await fetch(`${auditServerURL}/v1/audit/run`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        node_tree: tree,
        scope,
        file_key: fileKey,
        file_name: fileName,
        brand: "indmoney",
      }),
    });
  } catch (e) {
    toast("Audit server unreachable. Run `npm run audit:serve`.", "error");
    return;
  }
  if (!r.ok) {
    const body = await r.text().catch(() => "");
    toast(`Audit failed (${r.status}): ${body || "unknown"}`, "error");
    return;
  }
  const data = (await r.json()) as AuditResponse;
  await figma.clientStorage.setAsync(`audit:${fileKey}`, {
    cache_key: data.cache_key,
    audited_at: Date.now(),
  });
  if (data.registered) {
    toast(`✓ ${fileName} registered — commit + push to deploy`, "success");
  }
  send({ type: "audit-summary", payload: aggregateAuditTotals(data.result) });

  const flat: AuditFix[] = [];
  for (const s of data.result.screens) for (const f of s.fixes) flat.push(f);
  send({ type: "audit-fixes", payload: flat.slice(0, 100) });

  // Forward component matches that have a matched_slug — these power the
  // "Component matches" section. Reject decisions are dropped to keep the
  // panel signal-rich; ambiguous + accept both surface so designers can
  // verify ambiguous cases manually.
  const matches: AuditComponentMatch[] = [];
  for (const s of data.result.screens) {
    if (!s.component_matches) continue;
    for (const m of s.component_matches) {
      if (m.decision === "reject" || !m.matched_slug) continue;
      matches.push(m);
    }
  }
  send({ type: "audit-matches", payload: matches.slice(0, 60) });

  await prefetchVariables();
}

interface AuditFix {
  node_id: string;
  node_name: string;
  property: string;
  observed: string;
  token_path: string;
  variable_id?: string;
  figma_name?: string;
  figma_collection?: string;
  distance: number;
  usage_count: number;
  priority: "P1" | "P2" | "P3";
  reason: string;
}

interface AuditComponentMatch {
  node_id: string;
  node_name: string;
  decision: "accept" | "reject" | "ambiguous";
  matched_slug?: string;
  matched_name?: string;
  matched_description?: string;
  axis_count?: number;
  set_key?: string;
  score: number;
}

interface AuditResult {
  screens: Array<{
    coverage: { fills: { bound: number; total: number }; text: { bound: number; total: number }; spacing: { bound: number; total: number }; radius: { bound: number; total: number } };
    component_summary: { from_ds: number; ambiguous: number; custom: number };
    fixes: AuditFix[];
    component_matches?: AuditComponentMatch[];
  }>;
}

interface AuditResponse {
  cache_key: string;
  result: AuditResult;
  registered?: boolean;
}

function aggregateAuditTotals(result: AuditResult) {
  let bound = 0, total = 0;
  let ds = 0, amb = 0, cust = 0;
  let p1 = 0, p2 = 0, p3 = 0;
  for (const s of result.screens) {
    bound += s.coverage.fills.bound + s.coverage.text.bound + s.coverage.spacing.bound + s.coverage.radius.bound;
    total += s.coverage.fills.total + s.coverage.text.total + s.coverage.spacing.total + s.coverage.radius.total;
    ds += s.component_summary.from_ds;
    amb += s.component_summary.ambiguous;
    cust += s.component_summary.custom;
    for (const f of s.fixes) {
      if (f.priority === "P1") p1++;
      else if (f.priority === "P2") p2++;
      else p3++;
    }
  }
  return {
    coverage: total > 0 ? Math.round((bound / total) * 1000) / 10 : 0,
    fromDS: ds, amb, cust, p1, p2, p3,
    screens: result.screens.length,
  };
}

function collectNodeTreeForAudit(scope: AuditScope): any | null {
  const minimal = (n: SceneNode | DocumentNode | PageNode): any => {
    const base: any = { id: n.id, type: n.type, name: n.name };
    if ("absoluteBoundingBox" in n && n.absoluteBoundingBox) {
      const bb = n.absoluteBoundingBox;
      base.absoluteBoundingBox = { x: bb.x, y: bb.y, width: bb.width, height: bb.height };
    }
    if ("fills" in n && n.fills && (n.fills as readonly Paint[]).length) {
      base.fills = (n.fills as readonly Paint[]).map(serializePaint);
    }
    if ("strokes" in n && n.strokes && (n.strokes as readonly Paint[]).length) {
      base.strokes = (n.strokes as readonly Paint[]).map(serializePaint);
    }
    if ("layoutMode" in n) {
      base.layoutMode = n.layoutMode;
      base.itemSpacing = (n as any).itemSpacing;
      base.paddingLeft = (n as any).paddingLeft;
      base.paddingRight = (n as any).paddingRight;
      base.paddingTop = (n as any).paddingTop;
      base.paddingBottom = (n as any).paddingBottom;
    }
    if ("cornerRadius" in n) {
      const cr = (n as any).cornerRadius;
      if (typeof cr === "number") base.cornerRadius = cr;
    }
    if ("componentId" in n) base.componentId = (n as any).componentId;
    if ("styles" in n) base.styles = (n as any).styles;
    if ("children" in n && (n as ChildrenMixin).children) {
      base.children = (n as ChildrenMixin).children.map(minimal);
    }
    return base;
  };

  if (scope === "selection") {
    const sel = figma.currentPage.selection;
    if (sel.length === 0) return null;
    return { type: "DOCUMENT", children: [{ type: "CANVAS", name: "Selection", children: sel.map(minimal) }] };
  }
  if (scope === "page") {
    return { type: "DOCUMENT", children: [minimal(figma.currentPage)] };
  }
  return { type: "DOCUMENT", children: figma.root.children.map(minimal) };
}

function serializePaint(p: Paint): any {
  if (p.type === "SOLID") {
    return {
      type: "SOLID",
      color: { r: p.color.r, g: p.color.g, b: p.color.b },
      opacity: p.opacity ?? 1,
      visible: p.visible !== false,
      boundVariables: (p as any).boundVariables ?? undefined,
    };
  }
  return { type: p.type, visible: p.visible !== false };
}

/* ── Click-to-apply ─────────────────────────────────────────────────── */

interface ApplyFixPayload {
  node_id: string;
  property: string;
  variable_id?: string;
  token_path: string;
  figma_name?: string;
  figma_collection?: string;
}

async function prefetchVariables() {
  const vars = await figma.variables.getLocalVariablesAsync();
  knownVariables = {};
  for (const v of vars) knownVariables[v.id] = v;
  // We don't preload library variables — they require N+1 API calls and
  // designers may have many libraries. resolveVariable does an on-demand
  // library walk only when the local-by-name match fails.
}

/* libraryByName caches the most recent successful library lookups so
 * "Apply to all 47" doesn't re-walk every collection per node. Keyed by
 * the normalized name returned from `normalizeTokenName`. */
const libraryByName: Record<string, Variable> = {};

/** Strip everything but alphanumerics + lowercase. So "colour.surface.
 *  surface-white", "Surface/Surface White", and "surface-white" all
 *  collapse to the same key, surviving DTCG path → Figma variable name
 *  formatting differences. */
function normalizeTokenName(name: string): string {
  return name.toLowerCase().replace(/[^a-z0-9]+/g, "");
}

async function applyFix(p: ApplyFixPayload) {
  const node = await figma.getNodeByIdAsync(p.node_id);
  if (!node || node.removed) {
    toast(`Node ${p.node_id} not found (deleted?)`, "error");
    return;
  }
  const variable = await resolveVariable(p);
  if (!variable) {
    const tried = p.figma_name || p.token_path;
    const looksLikeTokenPath = tried.startsWith("colour.") || tried.startsWith("base.");
    const hint = looksLikeTokenPath
      ? "This is a DTCG path, not a Figma Variable name — your audit ran before the figma-name fix landed. Re-audit to pick up the proper variable name, or reload the plugin (Plugins → Development → INDmoney DS Sync → Reload plugin)."
      : "Make sure the Glyph DS library is enabled in this file (Assets → Libraries → Glyph), then retry.";
    toast(`Couldn't bind "${tried}". ${hint}`, "error");
    return;
  }
  try {
    // Fill — bind a SolidPaint to a color variable.
    if (p.property === "fill" && "fills" in node) {
      const fills = (node as any).fills as readonly Paint[];
      if (fills && fills.length > 0 && fills[0].type === "SOLID") {
        const updated = (fills as Paint[]).map((paint, i) => {
          if (i !== 0) return paint;
          return figma.variables.setBoundVariableForPaint(paint as SolidPaint, "color", variable!);
        });
        (node as any).fills = updated;
        send({ type: "audit-applied", payload: { node_id: p.node_id, token_path: p.token_path } });
        toast(`Bound ${p.token_path}`, "success");
        return;
      }
    }
    // Stroke — same pattern as fill.
    if (p.property === "stroke" && "strokes" in node) {
      const strokes = (node as any).strokes as readonly Paint[];
      if (strokes && strokes.length > 0 && strokes[0].type === "SOLID") {
        const updated = (strokes as Paint[]).map((paint, i) => {
          if (i !== 0) return paint;
          return figma.variables.setBoundVariableForPaint(paint as SolidPaint, "color", variable!);
        });
        (node as any).strokes = updated;
        send({ type: "audit-applied", payload: { node_id: p.node_id, token_path: p.token_path } });
        toast(`Bound ${p.token_path}`, "success");
        return;
      }
    }
    // Spacing / padding / radius — bind via setBoundVariableForLayoutSizingAndSpacing
    // when the property exists on the node, otherwise fall through.
    if (p.property === "radius" && "cornerRadius" in node) {
      try {
        (node as any).setBoundVariable("topLeftRadius", variable);
        (node as any).setBoundVariable("topRightRadius", variable);
        (node as any).setBoundVariable("bottomLeftRadius", variable);
        (node as any).setBoundVariable("bottomRightRadius", variable);
        send({ type: "audit-applied", payload: { node_id: p.node_id, token_path: p.token_path } });
        toast(`Bound ${p.token_path}`, "success");
        return;
      } catch {}
    }
    if (p.property === "spacing" && "itemSpacing" in node) {
      try {
        (node as any).setBoundVariable("itemSpacing", variable);
        send({ type: "audit-applied", payload: { node_id: p.node_id, token_path: p.token_path } });
        toast(`Bound ${p.token_path}`, "success");
        return;
      } catch {}
    }
    toast(`Apply for "${p.property}" not yet supported.`, "error");
  } catch (e: any) {
    toast(`Apply failed: ${e?.message ?? "unknown"}. Plan tier?`, "error");
  }
}

/* ── Bulk apply (group + many) ──────────────────────────────────────── */

interface ApplyGroupPayload {
  property: string;
  token_path: string;
  observed: string;
  variable_id?: string;
  figma_name?: string;
  figma_collection?: string;
  node_ids: string[];
}

interface ApplyManyPayload {
  groups: ApplyGroupPayload[];
}

async function applyGroup(p: ApplyGroupPayload) {
  const variable = await resolveVariable(p);
  if (!variable) {
    const tried = p.figma_name || p.token_path;
    const looksLikeTokenPath = tried.startsWith("colour.") || tried.startsWith("base.");
    const hint = looksLikeTokenPath
      ? "This is a DTCG path, not a Figma Variable name — your audit ran before the figma-name fix landed. Re-audit to pick up the proper variable name, or reload the plugin (Plugins → Development → INDmoney DS Sync → Reload plugin)."
      : "Make sure the Glyph DS library is enabled in this file (Assets → Libraries → Glyph), then retry.";
    toast(`Couldn't bind "${tried}". ${hint}`, "error");
    return;
  }
  let applied = 0;
  for (const id of p.node_ids) {
    const node = await figma.getNodeByIdAsync(id);
    if (!node || node.removed) continue;
    if (await applyVariableToNode(node, p.property, variable)) applied++;
  }
  send({
    type: "group-applied",
    payload: {
      key: `${p.property}|${p.observed}|${p.token_path}`,
      applied,
      total: p.node_ids.length,
    },
  });
  toast(`Applied ${p.token_path} to ${applied}/${p.node_ids.length} node${p.node_ids.length === 1 ? "" : "s"}`, applied > 0 ? "success" : "error");
}

async function applyMany(p: ApplyManyPayload) {
  let totalApplied = 0;
  let totalTouched = 0;
  for (const g of p.groups) {
    const variable = await resolveVariable(g);
    if (!variable) continue;
    for (const id of g.node_ids) {
      totalTouched++;
      const node = await figma.getNodeByIdAsync(id);
      if (!node || node.removed) continue;
      if (await applyVariableToNode(node, g.property, variable)) totalApplied++;
    }
  }
  send({ type: "many-applied", payload: { applied: totalApplied, touched: totalTouched } });
  toast(`Applied ${totalApplied}/${totalTouched} bindings`, totalApplied > 0 ? "success" : "error");
}

/**
 * resolveVariable finds a Figma `Variable` for a fix.
 *
 * Order of attempts (each cheap fallback before the expensive one):
 *   1. variable_id direct hit in `knownVariables` (local file).
 *   2. Local variable name match using normalizeTokenName on either
 *      figma_name or token_path.
 *   3. Team library walk — Glyph publishes its color tokens via the
 *      DS library; consumer files don't have them as locals until
 *      first use. Walks every available collection, normalizes each
 *      library variable's name, and imports via importVariableByKeyAsync
 *      on a match. Cached in libraryByName to avoid re-walking on
 *      Apply-to-all-N.
 *
 * Returns null if no match in any source. Caller surfaces an actionable
 * error toast that names what we tried.
 */
async function resolveVariable(g: {
  variable_id?: string;
  token_path: string;
  figma_name?: string;
  figma_collection?: string;
}): Promise<Variable | null> {
  // 1. variable_id exact match (rare — only when audit ran against this file
  // and Figma returned the local id).
  if (g.variable_id && knownVariables[g.variable_id]) {
    return knownVariables[g.variable_id];
  }
  if (g.variable_id) {
    try {
      const v = await figma.variables.getVariableByIdAsync(g.variable_id);
      if (v) {
        knownVariables[v.id] = v;
        return v;
      }
    } catch {}
  }

  // The lookup target is whatever name the Figma variable actually has —
  // `figma_name` when the extractor captured it, `token_path` as a fallback
  // for older audit JSON.
  const targets = [g.figma_name, g.token_path].filter(Boolean) as string[];
  if (targets.length === 0) return null;
  const normTargets = targets.map(normalizeTokenName);

  // Cache hit?
  for (const t of normTargets) {
    if (libraryByName[t]) return libraryByName[t];
  }

  // 2. Local variables by normalized name.
  if (Object.keys(knownVariables).length === 0) await prefetchVariables();
  for (const v of Object.values(knownVariables)) {
    if (normTargets.includes(normalizeTokenName(v.name))) {
      return v;
    }
  }

  // 3. Team-library walk. teamLibrary may not be available depending on
  // plugin permissions / plan — wrap in try.
  try {
    const collections = await figma.teamLibrary.getAvailableLibraryVariableCollectionsAsync();
    for (const coll of collections) {
      // Optional bias: when figma_collection matches, walk that coll first
      // for speed, but we still walk all collections so a misnamed
      // category doesn't block resolution.
      const libVars = await figma.teamLibrary.getVariablesInLibraryCollectionAsync(coll.key);
      for (const lv of libVars) {
        const norm = normalizeTokenName(lv.name);
        if (normTargets.includes(norm)) {
          const imported = await figma.variables.importVariableByKeyAsync(lv.key);
          knownVariables[imported.id] = imported;
          for (const t of normTargets) libraryByName[t] = imported;
          return imported;
        }
      }
    }
  } catch (e) {
    // Surfaced as a more descriptive toast in the caller.
    figma.notify(`Library lookup failed: ${(e as Error).message ?? "unknown"}`, { error: true });
  }
  return null;
}

async function applyVariableToNode(node: BaseNode, property: string, variable: Variable): Promise<boolean> {
  try {
    if (property === "fill" && "fills" in node) {
      const fills = (node as any).fills as readonly Paint[];
      if (!fills || fills.length === 0 || fills[0].type !== "SOLID") return false;
      (node as any).fills = (fills as Paint[]).map((paint, i) =>
        i === 0 ? figma.variables.setBoundVariableForPaint(paint as SolidPaint, "color", variable) : paint,
      );
      return true;
    }
    if (property === "stroke" && "strokes" in node) {
      const strokes = (node as any).strokes as readonly Paint[];
      if (!strokes || strokes.length === 0 || strokes[0].type !== "SOLID") return false;
      (node as any).strokes = (strokes as Paint[]).map((paint, i) =>
        i === 0 ? figma.variables.setBoundVariableForPaint(paint as SolidPaint, "color", variable) : paint,
      );
      return true;
    }
    if (property === "radius" && "cornerRadius" in node) {
      const n = node as any;
      try {
        n.setBoundVariable("topLeftRadius", variable);
        n.setBoundVariable("topRightRadius", variable);
        n.setBoundVariable("bottomLeftRadius", variable);
        n.setBoundVariable("bottomRightRadius", variable);
        return true;
      } catch {
        return false;
      }
    }
    if (property === "spacing" && "itemSpacing" in node) {
      try {
        (node as any).setBoundVariable("itemSpacing", variable);
        return true;
      } catch {
        return false;
      }
    }
  } catch {}
  return false;
}

/* ── Inject (kept, tertiary) ────────────────────────────────────────── */

interface ManifestEntry {
  slug: string;
  name: string;
  category: string;
  kind?: string;
  file: string;
}

async function runInject(kind: "icon" | "component") {
  toast(`Fetching manifest…`, "info");
  let resp: Response;
  try {
    resp = await fetch(`${docsURL}/icons/glyph/manifest.json`);
  } catch (e) {
    toast(`Cannot reach ${docsURL}`, "error");
    return;
  }
  if (!resp.ok) {
    toast(`Manifest fetch failed: ${resp.status}`, "error");
    return;
  }
  const m = (await resp.json()) as { icons: ManifestEntry[] };
  const targets = m.icons.filter((e) => (e.kind ?? "") === kind);
  send({ type: "inject-progress", payload: { done: 0, total: targets.length } });

  const host = figma.createFrame();
  host.name = `INDmoney ${kind === "icon" ? "Icons" : "Components"} (synced)`;
  host.layoutMode = "VERTICAL";
  host.itemSpacing = 12;
  host.paddingLeft = host.paddingRight = host.paddingTop = host.paddingBottom = 16;
  host.primaryAxisSizingMode = "AUTO";
  host.counterAxisSizingMode = "AUTO";
  host.x = figma.viewport.center.x;
  host.y = figma.viewport.center.y;
  figma.currentPage.appendChild(host);

  let done = 0;
  for (const entry of targets) {
    try {
      const r = await fetch(`${docsURL}/icons/glyph/${entry.file}`);
      if (!r.ok) continue;
      const svg = await r.text();
      const node = figma.createNodeFromSvg(svg);
      node.name = entry.slug;
      host.appendChild(node);
      done++;
      if (done % 20 === 0) {
        send({ type: "inject-progress", payload: { done, total: targets.length } });
      }
    } catch {}
  }
  figma.currentPage.selection = [host];
  figma.viewport.scrollAndZoomIntoView([host]);
  send({ type: "inject-progress", payload: { done, total: targets.length } });
  toast(`Injected ${done} ${kind}${done === 1 ? "" : "s"}`, "success");
}

/* ── Projects mode (U3) ─────────────────────────────────────────────── */

/**
 * Smart-grouping payload sent to the UI. The UI renders one card per group
 * with editable fields + per-pair toggles, and on Send-click constructs the
 * full ds-service /v1/projects/export payload (which is U4's wiring).
 *
 * Geometry primitives carry only the small set of properties the UI needs
 * to draw a preview row; full canonical_tree extraction is server-side.
 */
interface ProjectsFrameInfo {
  id: string;
  name: string;
  x: number;
  y: number;
  width: number;
  height: number;
  // Mode metadata — captured from `explicitVariableModes` and best-effort
  // resolved into a human label ("light"/"dark"/"default" or the mode's
  // raw name from the collection).
  mode_label: string | null;
  variable_collection_id: string | null;
  variable_mode_id: string | null;
  explicit_variable_modes: Record<string, string>;
  // Lightweight depth-2 structural skeleton — used by the cross-validation
  // step to flag pairs whose layouts diverge beyond their bound variables.
  skeleton: string;
}

interface ProjectsModePair {
  // The "primary" frame in the pair; for a 2-mode pair this is the lower-y
  // (typically light); the UI lets the designer override.
  primary_frame_id: string;
  paired_frame_ids: string[];
  modes: string[];
  theme_parity_warning: boolean;
}

interface ProjectsGroup {
  group_key: string; // section.id OR `freeform-<n>`
  section_id: string | null;
  section_name: string | null;
  default_name: string;
  // platform auto-detect: <500 → mobile, ≥1024 → web, else null (designer picks)
  platform_default: "mobile" | "web" | null;
  frames: ProjectsFrameInfo[];
  pairs: ProjectsModePair[];
  unpaired_frame_ids: string[];
}

interface ProjectsDetectedGroupsPayload {
  file_key: string;
  file_name: string;
  total_frames: number;
  groups: ProjectsGroup[];
  warnings: string[];
}

async function runProjectsDetection() {
  // Per the 2026 dynamic-page manifest requirement, the plugin must call
  // loadAllPagesAsync before walking parent chains that may cross page
  // boundaries. We do it once per session (it's cheap-ish; cached in core).
  const warnings: string[] = [];
  if (!allPagesLoaded) {
    try {
      // `loadAllPagesAsync` is only present on dynamic-page manifests.
      const fn = (figma as any).loadAllPagesAsync as undefined | (() => Promise<void>);
      if (typeof fn === "function") {
        await fn.call(figma);
        allPagesLoaded = true;
      } else {
        warnings.push("loadAllPagesAsync not available; falling back to current page only.");
      }
    } catch (e) {
      warnings.push(`loadAllPagesAsync failed: ${(e as Error).message ?? "unknown"}`);
    }
  }

  const sel = figma.currentPage.selection;
  // Collect candidate "screen" frames — the user may have selected a
  // SECTION (we expand to its children), a FRAME (we keep it), or a mix.
  const frames: SceneNode[] = [];
  for (const n of sel) {
    if (n.type === "SECTION") {
      for (const c of (n as SectionNode).children) {
        if (c.type === "FRAME") frames.push(c);
      }
    } else if (n.type === "FRAME") {
      frames.push(n);
    } else if (n.type === "GROUP" || n.type === "COMPONENT" || n.type === "INSTANCE") {
      // Treat top-level container nodes as candidate screens too — designers
      // sometimes select a row of components rather than frames.
      frames.push(n);
    }
  }

  if (frames.length === 0) {
    send({
      type: "projects.detected-groups",
      payload: {
        file_key: (figma as any).fileKey || "unknown",
        file_name: figma.root.name,
        total_frames: 0,
        groups: [],
        warnings: warnings.concat(["Select frames inside a section to start grouping."]),
      } as ProjectsDetectedGroupsPayload,
    });
    return;
  }

  // ── Step 1: group by enclosing SECTION ────────────────────────────
  // Walk parent chain; key = section.id, OR `freeform-<n>` if no section.
  const sectionGroups = new Map<string, { section: SectionNode | null; frames: SceneNode[] }>();
  let freeformIdx = 0;
  const freeformBucket = new Map<string, { section: SectionNode | null; frames: SceneNode[] }>();

  for (const f of frames) {
    const section = findEnclosingSection(f);
    if (section) {
      const k = section.id;
      let g = sectionGroups.get(k);
      if (!g) {
        g = { section, frames: [] };
        sectionGroups.set(k, g);
      }
      g.frames.push(f);
    } else {
      // Bucket all freeform frames into a single freeform-1 group by default;
      // the designer can split via the UI. (If we wanted finer freeform
      // groupings we'd cluster by spatial proximity — left to a future unit.)
      const k = `freeform-${freeformIdx === 0 ? (freeformIdx = 1, 1) : freeformIdx}`;
      let g = freeformBucket.get(k);
      if (!g) {
        g = { section: null, frames: [] };
        freeformBucket.set(k, g);
      }
      g.frames.push(f);
    }
  }

  // ── Step 2: build ProjectsFrameInfo + detect mode pairs per group ──
  const groups: ProjectsGroup[] = [];

  for (const [groupKey, bucket] of sectionGroups) {
    groups.push(await buildGroup(groupKey, bucket.section, bucket.frames));
  }
  let ffN = 1;
  for (const [, bucket] of freeformBucket) {
    groups.push(await buildGroup(`freeform-${ffN}`, null, bucket.frames));
    ffN++;
  }

  send({
    type: "projects.detected-groups",
    payload: {
      file_key: (figma as any).fileKey || "unknown",
      file_name: figma.root.name,
      total_frames: frames.length,
      groups,
      warnings,
    } as ProjectsDetectedGroupsPayload,
  });
}

function findEnclosingSection(node: SceneNode): SectionNode | null {
  let p: BaseNode | null = node.parent;
  while (p) {
    if (p.type === "SECTION") return p as SectionNode;
    if (p.type === "PAGE" || p.type === "DOCUMENT") return null;
    p = (p as any).parent ?? null;
  }
  return null;
}

async function buildGroup(
  groupKey: string,
  section: SectionNode | null,
  rawFrames: SceneNode[],
): Promise<ProjectsGroup> {
  // Build frame info objects. `explicitVariableModes` returns a map of
  // VariableCollectionId → mode-id, whose mode-id we look up against the
  // collection to derive a human label.
  const frames: ProjectsFrameInfo[] = [];
  for (const f of rawFrames) {
    frames.push(await buildFrameInfo(f));
  }

  // Pair detection: for each frame, look at siblings sharing a column
  // (|Δx|<10) and a different y, where their explicitVariableModes share a
  // VariableCollectionId but a different mode id.
  const pairs: ProjectsModePair[] = [];
  const used = new Set<string>();
  for (let i = 0; i < frames.length; i++) {
    const a = frames[i];
    if (used.has(a.id)) continue;
    const matches: ProjectsFrameInfo[] = [];
    for (let j = 0; j < frames.length; j++) {
      if (i === j) continue;
      const b = frames[j];
      if (used.has(b.id)) continue;
      if (Math.abs(a.x - b.x) >= 10) continue;
      if (a.y === b.y) continue;
      // Variable-mode cross-check: must share a collection, different mode.
      if (!a.variable_collection_id || !b.variable_collection_id) continue;
      if (a.variable_collection_id !== b.variable_collection_id) continue;
      if (a.variable_mode_id === b.variable_mode_id) continue;
      matches.push(b);
    }
    if (matches.length > 0) {
      // Cross-validate skeleton; if any matched frame's skeleton diverges
      // from `a` (depth=2 path/type pairs), flag theme_parity_warning.
      let warn = false;
      for (const m of matches) {
        if (m.skeleton !== a.skeleton) {
          warn = true;
          break;
        }
      }
      pairs.push({
        primary_frame_id: a.id,
        paired_frame_ids: matches.map((m) => m.id),
        modes: [a.mode_label || "default", ...matches.map((m) => m.mode_label || "default")],
        theme_parity_warning: warn,
      });
      used.add(a.id);
      for (const m of matches) used.add(m.id);
    }
  }

  const unpaired = frames.filter((f) => !used.has(f.id)).map((f) => f.id);

  // Platform auto-detect — use the widest frame in the group (most
  // representative of "the screen size" rather than a stray button).
  const widest = frames.reduce((acc, f) => (f.width > acc ? f.width : acc), 0);
  let platform_default: "mobile" | "web" | null = null;
  if (widest > 0 && widest < 500) platform_default = "mobile";
  else if (widest >= 1024) platform_default = "web";

  const default_name = section
    ? section.name
    : groupKey.startsWith("freeform-")
      ? `Freeform ${groupKey.split("-")[1]}`
      : groupKey;

  return {
    group_key: groupKey,
    section_id: section ? section.id : null,
    section_name: section ? section.name : null,
    default_name,
    platform_default,
    frames,
    pairs,
    unpaired_frame_ids: unpaired,
  };
}

async function buildFrameInfo(node: SceneNode): Promise<ProjectsFrameInfo> {
  const x = "x" in node ? node.x : 0;
  const y = "y" in node ? node.y : 0;
  const width = "width" in node ? Math.round(node.width) : 0;
  const height = "height" in node ? Math.round(node.height) : 0;

  // explicitVariableModes is { [VariableCollectionId]: mode_id }
  const explicit: Record<string, string> = {};
  let collectionId: string | null = null;
  let modeId: string | null = null;
  let modeLabel: string | null = null;
  try {
    const evm = (node as any).explicitVariableModes as Record<string, string> | undefined;
    if (evm && typeof evm === "object") {
      for (const k of Object.keys(evm)) {
        explicit[k] = evm[k];
      }
      // Pick the first explicit collection — typical Glyph setup binds a
      // single mode collection per frame at the top level.
      const keys = Object.keys(explicit);
      if (keys.length > 0) {
        collectionId = keys[0];
        modeId = explicit[collectionId];
        modeLabel = await resolveModeLabel(collectionId, modeId);
      }
    }
  } catch {}

  return {
    id: node.id,
    name: node.name,
    x: Math.round(x),
    y: Math.round(y),
    width,
    height,
    mode_label: modeLabel,
    variable_collection_id: collectionId,
    variable_mode_id: modeId,
    explicit_variable_modes: explicit,
    skeleton: depth2Skeleton(node),
  };
}

const modeLabelCache: Record<string, Record<string, string>> = {};

async function resolveModeLabel(collectionId: string, modeId: string): Promise<string | null> {
  if (modeLabelCache[collectionId] && modeLabelCache[collectionId][modeId]) {
    return modeLabelCache[collectionId][modeId];
  }
  try {
    const coll = await figma.variables.getVariableCollectionByIdAsync(collectionId);
    if (!coll) return null;
    const map: Record<string, string> = {};
    for (const m of coll.modes) {
      map[m.modeId] = m.name;
    }
    modeLabelCache[collectionId] = map;
    return map[modeId] || null;
  } catch {
    return null;
  }
}

/**
 * depth=2 structural skeleton — walks node + its direct children + grandchildren,
 * concatenating type/path tokens. Used to detect when two ostensibly-paired
 * frames diverge structurally beyond what bound-variable swaps explain.
 */
function depth2Skeleton(node: SceneNode): string {
  const parts: string[] = [node.type];
  const children = "children" in node ? (node as ChildrenMixin).children : [];
  for (const c of children) {
    parts.push("[" + c.type);
    const grand = "children" in c ? (c as ChildrenMixin).children : [];
    for (const g of grand) {
      parts.push("(" + g.type + ")");
    }
    parts.push("]");
  }
  return parts.join("");
}

/* ── Toast ──────────────────────────────────────────────────────────── */

function toast(message: string, level: "info" | "success" | "error" = "info") {
  send({ type: "toast", payload: { message, level, ts: Date.now() } });
}

/* ── Phase 4 U11 — auto-fix flow ───────────────────────────────────── */

interface AutoFixPayload {
  /** Project slug from the deeplink. */
  slug: string;
  /** Violation id from the deeplink (?violation_id=<id>). */
  violation_id: string;
  /** Optional pre-resolved targets — UI may pass these after the user
   *  picks a token from the suggestion list. When absent, plugin can
   *  only render the preview (Phase 4 ships the dispatch shell; the
   *  variable-resolution UI lands as Phase 4.1 polish). */
  target?: {
    token_path?: string;
    variable_id?: string;
    text_style_id?: string;
    observed_number?: number;
  };
}

interface ViolationFromServer {
  id: string;
  rule_id: string;
  property: string;
  observed: string;
  suggestion: string;
  status: string;
  auto_fixable: boolean;
  project_slug: string;
  flow_id: string;
  flow_name: string;
  file_id: string;
  node_id: string;
}

async function runAutoFix(payload: AutoFixPayload): Promise<void> {
  if (!payload || !payload.slug || !payload.violation_id) {
    toast("Auto-fix: missing slug or violation_id.", "error");
    return;
  }
  const headers: Record<string, string> = { Accept: "application/json" };
  // Plugin doesn't carry the JWT for the docs site (different host); the
  // /v1/projects/.../violations/:id endpoint is auth-gated, so the user
  // must have configured the plugin's auth token via Figma plugin
  // settings. Phase 4 reuses the existing auditServerURL pattern;
  // tokenisation is a Phase 4.1 polish item.
  let v: ViolationFromServer;
  try {
    const res = await fetch(
      `${auditServerURL}/v1/projects/${encodeURIComponent(payload.slug)}/violations/${encodeURIComponent(payload.violation_id)}`,
      { headers },
    );
    if (!res.ok) {
      toast(`Auto-fix: GET violation failed (${res.status}).`, "error");
      return;
    }
    v = (await res.json()) as ViolationFromServer;
  } catch (err) {
    toast(`Auto-fix: ${(err as Error).message ?? "network"}.`, "error");
    return;
  }

  if (!AutoFix.isAutoFixable(v.rule_id)) {
    toast(`Auto-fix: ${v.rule_id} requires a manual fix.`, "info");
    return;
  }

  // Build the preview. UI shows it then the user clicks Apply (a follow-up
  // message). For now, this implementation auto-applies on receipt — the
  // confirm-step UI is Phase 4.1 polish; the preview-only path keeps the
  // designer in the loop via the explicit menu invocation.
  const preview = AutoFix.previewFix({
    ruleID: v.rule_id,
    property: v.property,
    observed: v.observed,
    targetTokenPath: payload.target?.token_path,
    targetTextStyleId: payload.target?.text_style_id,
    observedNumber: payload.target?.observed_number,
  });
  if (!preview) {
    toast(`Auto-fix: insufficient target for ${v.rule_id}.`, "info");
    return;
  }

  // Dispatch.
  let result: AutoFix.ApplyResult;
  switch (v.rule_id) {
    case "drift.fill":
    case "unbound.fill":
    case "deprecated.fill":
      if (!payload.target?.variable_id || !v.node_id) {
        toast("Auto-fix: missing variable_id or node_id.", "info");
        return;
      }
      result = await AutoFix.applyFillBinding(v.node_id, payload.target.variable_id);
      break;
    case "drift.text":
    case "unbound.text":
      if (!payload.target?.text_style_id || !v.node_id) {
        toast("Auto-fix: missing text_style_id or node_id.", "info");
        return;
      }
      result = await AutoFix.applyTextStyle(v.node_id, payload.target.text_style_id);
      break;
    case "drift.padding":
    case "drift.gap": {
      const obs = payload.target?.observed_number;
      const prop = (v.property as
        | "paddingLeft"
        | "paddingRight"
        | "paddingTop"
        | "paddingBottom"
        | "itemSpacing");
      if (typeof obs !== "number" || !v.node_id) {
        toast("Auto-fix: missing observed_number or node_id.", "info");
        return;
      }
      result = await AutoFix.applySnapPadding(v.node_id, prop, obs);
      break;
    }
    case "drift.radius": {
      const obs = payload.target?.observed_number;
      if (typeof obs !== "number" || !v.node_id) {
        toast("Auto-fix: missing observed_number or node_id.", "info");
        return;
      }
      result = await AutoFix.applySnapRadius(v.node_id, obs);
      break;
    }
    default:
      return;
  }

  if (result.ok !== true) {
    toast(`Auto-fix failed: ${(result as { ok: false; error: string }).error}`, "error");
    return;
  }

  // Success ping. Best-effort — even if this fails the Figma file is
  // already updated; the next re-audit will re-classify the violation
  // status correctly.
  try {
    await fetch(
      `${auditServerURL}/v1/projects/${encodeURIComponent(payload.slug)}/violations/${encodeURIComponent(payload.violation_id)}/fix-applied`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json", Accept: "application/json" },
        body: JSON.stringify({ note: preview.hint }),
      },
    );
  } catch {
    // swallow — file is already fixed
  }
  toast(`Auto-fix applied: ${preview.hint}`, "success");
}
