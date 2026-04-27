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
    | "request-selection-summary";
  payload?: unknown;
}

interface MessageToUI {
  type:
    | "selection-summary"
    | "health"
    | "publish-result"
    | "audit-summary"
    | "audit-fixes"
    | "audit-applied"
    | "group-applied"
    | "many-applied"
    | "inject-progress"
    | "toast";
  payload?: unknown;
}

const send = (msg: MessageToUI) => figma.ui.postMessage(msg);

/* ── State ──────────────────────────────────────────────────────────── */

let auditServerURL = "http://localhost:7474";
let docsURL = "https://indmoney-design-system-docs.vercel.app";
let healthTimer: ReturnType<typeof setInterval> | null = null;
let knownVariables: Record<string, Variable> = {};

figma.showUI(__html__, { width: 420, height: 680, themeColors: true });

figma.ui.onmessage = async (msg: MessageFromUI) => {
  try {
    switch (msg.type) {
      case "ready":
        startHealthPolling();
        emitSelectionSummary();
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
    }
  } catch (e: unknown) {
    toast(`Error: ${(e as Error).message ?? "unknown"}`, "error");
  }
};

// Selection-change watcher — emits a live summary so the UI can update without
// the user clicking anything.
figma.on("selectionchange", () => {
  emitSelectionSummary();
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

interface AuditResult {
  screens: Array<{
    coverage: { fills: { bound: number; total: number }; text: { bound: number; total: number }; spacing: { bound: number; total: number }; radius: { bound: number; total: number } };
    component_summary: { from_ds: number; ambiguous: number; custom: number };
    fixes: AuditFix[];
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
    toast(
      `"${tried}" not found locally or in any team library. Make sure the Glyph DS library is enabled in this file (Assets → Libraries).`,
      "error",
    );
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
    toast(
      `"${tried}" not found locally or in any team library. Make sure the Glyph DS library is enabled in this file (Assets → Libraries).`,
      "error",
    );
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

/* ── Toast ──────────────────────────────────────────────────────────── */

function toast(message: string, level: "info" | "success" | "error" = "info") {
  send({ type: "toast", payload: { message, level, ts: Date.now() } });
}
