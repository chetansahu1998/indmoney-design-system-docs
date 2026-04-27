/**
 * INDmoney DS Sync — Figma plugin main thread.
 *
 * Surfaces:
 *   • Inject icons / components from the published manifest.
 *   • Audit selection / page / file: POST node tree → ds-service /v1/audit/run,
 *     render fix cards in the panel, click-to-apply via Figma variable APIs.
 *   • Staleness banner when DS revision has moved since last visit.
 *
 * Build: `cd figma-plugin && npx tsc` (uses tsconfig in same dir).
 */

interface MessageFromUI {
  type:
    | "request-selection"
    | "inject-icons"
    | "inject-components"
    | "audit-selection"
    | "audit-page"
    | "audit-file"
    | "apply-fix"
    | "ping-docs"
    | "ping-audit-server"
    | "set-base-url"
    | "set-audit-server-url"
    | "ready";
  payload?: unknown;
}

interface MessageToUI {
  type:
    | "selection"
    | "injection-progress"
    | "injection-done"
    | "audit-summary"
    | "audit-fixes"
    | "audit-applied"
    | "staleness-banner"
    | "error"
    | "info";
  payload?: unknown;
}

let baseURL = "https://indmoney-design-system-docs.vercel.app";
let auditServerURL = "http://localhost:7474";

// Cached so the apply handler can find it without re-fetching variables.
let knownVariables: { [variableId: string]: Variable } = {};

const send = (msg: MessageToUI) => figma.ui.postMessage(msg);

figma.showUI(__html__, { width: 380, height: 620, themeColors: true });

figma.ui.onmessage = async (msg: MessageFromUI) => {
  try {
    switch (msg.type) {
      case "ready":
        return checkStalenessOnLoad();
      case "set-base-url":
        if (typeof msg.payload === "string" && msg.payload.startsWith("http")) {
          baseURL = msg.payload.replace(/\/$/, "");
          send({ type: "info", payload: `Docs URL set to ${baseURL}` });
        }
        return;
      case "set-audit-server-url":
        if (typeof msg.payload === "string" && msg.payload.startsWith("http")) {
          auditServerURL = msg.payload.replace(/\/$/, "");
          send({ type: "info", payload: `Audit server set to ${auditServerURL}` });
        }
        return;
      case "request-selection":
        return reportSelection();
      case "inject-icons":
        return injectFromManifest("icon");
      case "inject-components":
        return injectFromManifest("component");
      case "audit-selection":
        return runAudit("selection");
      case "audit-page":
        return runAudit("page");
      case "audit-file":
        return runAudit("file");
      case "apply-fix":
        return applyFix(msg.payload as ApplyFixPayload);
      case "ping-docs":
        return pingDocs();
      case "ping-audit-server":
        return pingAuditServer();
    }
  } catch (e: unknown) {
    send({ type: "error", payload: (e as Error).message });
  }
};

// Direct menu-command shortcuts.
if (figma.command === "syncSelection") reportSelection();
if (figma.command === "injectIcons") injectFromManifest("icon");
if (figma.command === "injectComponents") injectFromManifest("component");
if (figma.command === "auditSelection") runAudit("selection");
if (figma.command === "auditFile") runAudit("file");
if (figma.command === "openDocsSite") {
  figma.openExternal(baseURL);
  figma.closePlugin();
}

/* ── Selection report ─────────────────────────────────────────────────── */

function reportSelection() {
  const sel = figma.currentPage.selection;
  const summary = sel.map((node) => ({
    id: node.id,
    name: node.name,
    type: node.type,
    width: "width" in node ? node.width : null,
    height: "height" in node ? node.height : null,
    childCount: "children" in node ? node.children.length : 0,
  }));
  send({ type: "selection", payload: summary });
}

/* ── Audit ───────────────────────────────────────────────────────────── */

interface AuditFix {
  node_id: string;
  node_name: string;
  property: string;
  observed: string;
  token_path: string;
  token_alias?: string;
  variable_id?: string;
  distance: number;
  usage_count: number;
  priority: "P1" | "P2" | "P3";
  reason: string;
  replaced_by?: string;
}

interface AuditScreen {
  node_id: string;
  name: string;
  slug: string;
  coverage: {
    fills: { bound: number; total: number };
    text: { bound: number; total: number };
    spacing: { bound: number; total: number };
    radius: { bound: number; total: number };
  };
  component_summary: { from_ds: number; ambiguous: number; custom: number };
  fixes: AuditFix[];
  node_count: number;
}

interface AuditResult {
  schema_version: string;
  file_key: string;
  file_name: string;
  brand: string;
  overall_coverage: number;
  overall_from_ds: number;
  headline_drift_hex?: string;
  screens: AuditScreen[];
}

interface AuditResponse {
  schema_version: string;
  cache_key: string;
  result: AuditResult;
}

async function runAudit(scope: "selection" | "page" | "file") {
  const tree = collectNodeTree(scope);
  if (tree === null) {
    send({ type: "error", payload: scopeEmptyMessage(scope) });
    return;
  }

  send({ type: "info", payload: `Auditing ${scope}…` });

  const fileKey = (figma as any).fileKey || "unknown";
  const fileName = figma.root.name;
  const url = `${auditServerURL}/v1/audit/run`;

  let resp: Response;
  try {
    resp = await fetch(url, {
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
    send({
      type: "error",
      payload: `Audit server unreachable at ${auditServerURL}. Run \`npm run audit:serve\` on your laptop.`,
    });
    return;
  }
  if (!resp.ok) {
    const text = await resp.text().catch(() => "");
    send({ type: "error", payload: `Audit failed (${resp.status}): ${text}` });
    return;
  }

  const data = (await resp.json()) as AuditResponse;
  await figma.clientStorage.setAsync(`audit:${fileKey}`, {
    cache_key: data.cache_key,
    audited_at: Date.now(),
  });

  const totals = aggregateTotals(data.result);
  send({ type: "audit-summary", payload: totals });

  // Surface the fix list in priority order; cap to 50 in the panel.
  const flat: AuditFix[] = [];
  for (const s of data.result.screens) {
    for (const f of s.fixes) flat.push(f);
  }
  send({ type: "audit-fixes", payload: flat.slice(0, 50) });

  // Pre-resolve the variables we'll apply later so apply latency stays low.
  await prefetchVariables(flat);
}

function collectNodeTree(scope: "selection" | "page" | "file"): any | null {
  const minimal = (n: SceneNode | DocumentNode | PageNode): any => {
    const base: any = { id: n.id, type: n.type, name: n.name };
    if ("absoluteBoundingBox" in n && n.absoluteBoundingBox) {
      base.absoluteBoundingBox = {
        x: n.absoluteBoundingBox.x,
        y: n.absoluteBoundingBox.y,
        width: n.absoluteBoundingBox.width,
        height: n.absoluteBoundingBox.height,
      };
    }
    if ("fills" in n && n.fills && (n.fills as readonly Paint[]).length > 0) {
      base.fills = (n.fills as readonly Paint[]).map(serializePaint);
    }
    if ("strokes" in n && n.strokes && (n.strokes as readonly Paint[]).length > 0) {
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
    return {
      type: "DOCUMENT",
      children: [
        {
          type: "CANVAS",
          name: "Selection",
          children: sel.map(minimal),
        },
      ],
    };
  }
  if (scope === "page") {
    return {
      type: "DOCUMENT",
      children: [minimal(figma.currentPage)],
    };
  }
  // file
  return {
    type: "DOCUMENT",
    children: figma.root.children.map(minimal),
  };
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

function scopeEmptyMessage(scope: string): string {
  switch (scope) {
    case "selection":
      return "Select at least one layer first.";
    case "page":
      return "Current page is empty.";
    default:
      return "File is empty.";
  }
}

function aggregateTotals(result: AuditResult) {
  let bound = 0,
    total = 0;
  let fromDS = 0,
    ambig = 0,
    custom = 0;
  let p1 = 0,
    p2 = 0,
    p3 = 0;
  for (const s of result.screens) {
    const c = s.coverage;
    bound += c.fills.bound + c.text.bound + c.spacing.bound + c.radius.bound;
    total += c.fills.total + c.text.total + c.spacing.total + c.radius.total;
    fromDS += s.component_summary.from_ds;
    ambig += s.component_summary.ambiguous;
    custom += s.component_summary.custom;
    for (const f of s.fixes) {
      if (f.priority === "P1") p1++;
      else if (f.priority === "P2") p2++;
      else p3++;
    }
  }
  return {
    coverage: total > 0 ? Math.round((bound / total) * 1000) / 10 : 0,
    fromDS,
    ambig,
    custom,
    p1,
    p2,
    p3,
    screens: result.screens.length,
  };
}

/* ── Click-to-apply ─────────────────────────────────────────────────── */

interface ApplyFixPayload {
  node_id: string;
  property: string;
  variable_id?: string;
  token_path: string;
  observed: string;
}

async function prefetchVariables(fixes: AuditFix[]) {
  // The audit response gives us token_path + sometimes variable_id. For Apply
  // we need a real Variable object; resolve once here so apply is snappy.
  const vars = await figma.variables.getLocalVariablesAsync();
  knownVariables = {};
  for (const v of vars) {
    knownVariables[v.id] = v;
  }
  // Note: file-bound published variables would need
  // figma.variables.getVariableByIdAsync — left to U16 polish.
  void fixes;
}

async function applyFix(p: ApplyFixPayload) {
  const node = await figma.getNodeByIdAsync(p.node_id);
  if (!node || node.removed) {
    send({ type: "error", payload: `Node ${p.node_id} not found (deleted?)` });
    return;
  }

  let variable: Variable | null = null;
  if (p.variable_id && knownVariables[p.variable_id]) {
    variable = knownVariables[p.variable_id];
  } else {
    // Fallback: search by name matching token_path.
    const all = await figma.variables.getLocalVariablesAsync();
    variable =
      all.find((v) => v.name.toLowerCase() === p.token_path.toLowerCase()) ?? null;
  }
  if (!variable) {
    send({
      type: "error",
      payload: `Token "${p.token_path}" isn't a local variable on this Figma plan. Copy the path manually instead.`,
    });
    return;
  }

  try {
    if (p.property === "fill" && "fills" in node) {
      const current = (node as any).fills as readonly Paint[];
      if (current && current.length > 0 && current[0].type === "SOLID") {
        const updated = (current as Paint[]).map((paint, i) => {
          if (i !== 0) return paint;
          return figma.variables.setBoundVariableForPaint(
            paint as SolidPaint,
            "color",
            variable!,
          );
        });
        (node as any).fills = updated;
        send({
          type: "audit-applied",
          payload: { node_id: p.node_id, property: p.property, token_path: p.token_path },
        });
        return;
      }
    }
    send({
      type: "error",
      payload: `Apply for property "${p.property}" not yet implemented in v1.`,
    });
  } catch (e: any) {
    send({
      type: "error",
      payload: `Apply failed: ${e?.message ?? "unknown error"}. Your Figma plan may not allow plugin variable writes.`,
    });
  }
}

/* ── Staleness banner ────────────────────────────────────────────────── */

async function checkStalenessOnLoad() {
  const fileKey = (figma as any).fileKey || "unknown";
  const cached = (await figma.clientStorage.getAsync(`audit:${fileKey}`)) as
    | { cache_key: string; audited_at: number }
    | undefined;

  // Probe current DS rev via the manifest's content hash (cheap HEAD-equivalent).
  let currentRev = "";
  try {
    const r = await fetch(`${baseURL}/icons/glyph/manifest.json`, { method: "GET" });
    if (r.ok) {
      const text = await r.text();
      currentRev = await sha256Short(text);
    }
  } catch {
    // Network blip — skip; banner not critical.
    return;
  }

  if (!cached) {
    send({
      type: "staleness-banner",
      payload: {
        kind: "first-time",
        message: "First-time audit recommended for this file.",
      },
    });
    return;
  }
  const ageDays = (Date.now() - cached.audited_at) / (24 * 60 * 60 * 1000);
  const cachedRev = cached.cache_key.split(":")[1] ?? "";
  if (currentRev && cachedRev && currentRev !== cachedRev) {
    send({
      type: "staleness-banner",
      payload: {
        kind: "ds-changed",
        message: "DS updated since your last audit — re-run to refresh.",
      },
    });
    return;
  }
  if (ageDays > 7) {
    send({
      type: "staleness-banner",
      payload: {
        kind: "old",
        message: `Last audit ${Math.round(ageDays)} days ago — consider re-running.`,
      },
    });
  }
}

async function sha256Short(s: string): Promise<string> {
  // Browser SubtleCrypto is available in Figma plugin context.
  const buf = new TextEncoder().encode(s);
  const digest = await crypto.subtle.digest("SHA-256", buf);
  const arr = Array.from(new Uint8Array(digest));
  return arr.slice(0, 8).map((b) => b.toString(16).padStart(2, "0")).join("");
}

/* ── Manifest injection (existing) ────────────────────────────────────── */

interface ManifestEntry {
  slug: string;
  name: string;
  category: string;
  kind?: string;
  file: string;
  width: number;
  height: number;
}

interface Manifest {
  icons: ManifestEntry[];
}

async function injectFromManifest(kind: "icon" | "component") {
  const manifestURL = `${baseURL}/icons/glyph/manifest.json`;
  send({ type: "info", payload: `Fetching ${manifestURL}` });
  const resp = await fetch(manifestURL);
  if (!resp.ok) throw new Error(`Manifest fetch failed: ${resp.status}`);
  const manifest = (await resp.json()) as Manifest;
  const targets = manifest.icons.filter((e) => (e.kind ?? "") === kind);
  send({
    type: "info",
    payload: `Injecting ${targets.length} ${kind}${targets.length === 1 ? "" : "s"}…`,
  });

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
  figma.currentPage.selection = [host];
  figma.viewport.scrollAndZoomIntoView([host]);

  let done = 0;
  for (const entry of targets) {
    try {
      const svgURL = `${baseURL}/icons/glyph/${entry.file}`;
      const svgResp = await fetch(svgURL);
      if (!svgResp.ok) continue;
      const svg = await svgResp.text();
      const node = figma.createNodeFromSvg(svg);
      node.name = `${entry.slug}`;
      host.appendChild(node);
      done++;
      if (done % 20 === 0) {
        send({ type: "injection-progress", payload: { done, total: targets.length } });
      }
    } catch (err) {
      send({ type: "error", payload: `Failed: ${entry.slug}: ${(err as Error).message}` });
    }
  }
  send({ type: "injection-done", payload: { done, total: targets.length, hostId: host.id } });
  figma.notify(`Injected ${done} ${kind}${done === 1 ? "" : "s"} into "${host.name}"`);
}

async function pingDocs() {
  try {
    const resp = await fetch(`${baseURL}/icons/glyph/manifest.json`, { method: "HEAD" });
    send({ type: "info", payload: resp.ok ? `Reachable: ${baseURL}` : `${baseURL} → ${resp.status}` });
  } catch (e) {
    send({ type: "error", payload: `Cannot reach ${baseURL}: ${(e as Error).message}` });
  }
}

async function pingAuditServer() {
  try {
    const resp = await fetch(`${auditServerURL}/__health`);
    send({
      type: "info",
      payload: resp.ok ? `Audit server up at ${auditServerURL}` : `Audit server → ${resp.status}`,
    });
  } catch (e) {
    send({
      type: "error",
      payload: `Cannot reach audit server. Run \`npm run audit:serve\`. (${(e as Error).message})`,
    });
  }
}
