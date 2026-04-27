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
const send = (msg) => figma.ui.postMessage(msg);
/* ── State ──────────────────────────────────────────────────────────── */
let auditServerURL = "http://localhost:7474";
let docsURL = "https://indmoney-design-system-docs.vercel.app";
let healthTimer = null;
let knownVariables = {};
figma.showUI(__html__, { width: 420, height: 680, themeColors: true });
figma.ui.onmessage = async (msg) => {
    var _a;
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
                return runAudit(msg.payload.scope);
            case "apply-fix":
                return applyFix(msg.payload);
            case "inject":
                return runInject(msg.payload.kind);
            case "open-docs":
                figma.openExternal(docsURL);
                return;
            case "request-selection-summary":
                return emitSelectionSummary();
        }
    }
    catch (e) {
        toast(`Error: ${(_a = e.message) !== null && _a !== void 0 ? _a : "unknown"}`, "error");
    }
};
// Selection-change watcher — emits a live summary so the UI can update without
// the user clicking anything.
figma.on("selectionchange", () => {
    emitSelectionSummary();
});
// Restore stored URL on boot.
(async () => {
    const stored = (await figma.clientStorage.getAsync("audit_server_url"));
    if (stored)
        auditServerURL = stored;
})();
// Direct menu commands.
if (figma.command === "auditFile")
    runAudit("file").catch(() => { });
if (figma.command === "auditSelection")
    runAudit("selection").catch(() => { });
if (figma.command === "publishSelection")
    runPublish().catch(() => { });
if (figma.command === "openDocsSite") {
    figma.openExternal(docsURL);
    figma.closePlugin();
}
/* ── Health polling ─────────────────────────────────────────────────── */
function startHealthPolling() {
    if (healthTimer)
        clearInterval(healthTimer);
    void pollHealth();
    healthTimer = setInterval(() => void pollHealth(), 5000);
}
async function pollHealth() {
    try {
        const r = await fetch(`${auditServerURL}/__health`, { method: "GET" });
        if (r.ok) {
            const body = (await r.json());
            send({
                type: "health",
                payload: {
                    connected: true,
                    server_url: auditServerURL,
                    schema_version: body.schema_version,
                    repo: body.repo,
                },
            });
            return;
        }
        send({ type: "health", payload: { connected: false, server_url: auditServerURL, error: `HTTP ${r.status}` } });
    }
    catch (e) {
        send({ type: "health", payload: { connected: false, server_url: auditServerURL, error: e.message } });
    }
}
function classifyNode(node) {
    const base = {
        figma_id: node.id,
        name: node.name,
        kind: "other",
        width: "width" in node ? Math.round(node.width) : 0,
        height: "height" in node ? Math.round(node.height) : 0,
    };
    switch (node.type) {
        case "COMPONENT_SET": {
            base.kind = "component_set";
            base.variants = node.children
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
            }
            else {
                base.kind = "component_standalone";
            }
            return base;
        }
        case "INSTANCE": {
            base.kind = "instance";
            const inst = node;
            // Async getMainComponentAsync would be ideal but we're in sync classify;
            // mainComponent is available synchronously when not loaded from a library.
            try {
                const main = inst.mainComponent;
                if (main) {
                    base.instance_of_id = main.id;
                    base.instance_of_name = main.name;
                }
            }
            catch (_a) { }
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
function parseVariantProps(name) {
    return name
        .split(",")
        .map((part) => part.trim())
        .filter(Boolean)
        .map((part) => {
        const eq = part.indexOf("=");
        if (eq < 0)
            return null;
        return {
            name: part.slice(0, eq).trim(),
            value: part.slice(eq + 1).trim(),
        };
    })
        .filter(Boolean);
}
function emitSelectionSummary() {
    const sel = figma.currentPage.selection;
    const items = sel.map(classifyNode);
    const byKind = {
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
        if (i.variants)
            variantTotal += i.variants.length;
        if (i.kind === "component_set" || i.kind === "component_standalone" || i.kind === "component_variant") {
            publishable++;
        }
    }
    const summary = {
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
    const fileKey = figma.fileKey || "unknown";
    const fileName = figma.root.name;
    const captured = items.map((i) => {
        var _a;
        return ({
            kind: kindToServerKind(i.kind),
            figma_id: i.figma_id,
            name: i.name,
            component_set_id: i.parent_set_id,
            parent_set_name: i.parent_set_name,
            parent_set_id: i.parent_set_id,
            variants: (_a = i.variants) === null || _a === void 0 ? void 0 : _a.map((v) => ({
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
        });
    });
    let r;
    try {
        r = await fetch(`${auditServerURL}/v1/publish`, {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ file_key: fileKey, file_name: fileName, brand: "indmoney", selections: captured }),
        });
    }
    catch (e) {
        toast(`Audit server unreachable. Run \`npm run audit:serve\`.`, "error");
        return;
    }
    if (!r.ok) {
        const body = await r.text().catch(() => "");
        toast(`Publish failed (${r.status}): ${body || "unknown"}`, "error");
        return;
    }
    const data = (await r.json());
    send({ type: "publish-result", payload: data });
    const adds = data.new_count;
    const updates = data.updated_count;
    toast(`${adds} added, ${updates} updated · committed at lib/contributions/`, "success");
}
function kindToServerKind(k) {
    switch (k) {
        case "component_set": return "component_set";
        case "component_standalone": return "component_standalone";
        case "component_variant": return "component_variant";
        case "instance": return "instance";
        default: return "other";
    }
}
async function runAudit(scope) {
    const tree = collectNodeTreeForAudit(scope);
    if (tree === null) {
        toast(scope === "selection" ? "Select at least one layer first." : `${scope} is empty.`, "error");
        return;
    }
    const fileKey = figma.fileKey || "unknown";
    const fileName = figma.root.name;
    toast(`Auditing ${scope}…`, "info");
    let r;
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
    }
    catch (e) {
        toast("Audit server unreachable. Run `npm run audit:serve`.", "error");
        return;
    }
    if (!r.ok) {
        const body = await r.text().catch(() => "");
        toast(`Audit failed (${r.status}): ${body || "unknown"}`, "error");
        return;
    }
    const data = (await r.json());
    await figma.clientStorage.setAsync(`audit:${fileKey}`, {
        cache_key: data.cache_key,
        audited_at: Date.now(),
    });
    if (data.registered) {
        toast(`✓ ${fileName} registered — commit + push to deploy`, "success");
    }
    send({ type: "audit-summary", payload: aggregateAuditTotals(data.result) });
    const flat = [];
    for (const s of data.result.screens)
        for (const f of s.fixes)
            flat.push(f);
    send({ type: "audit-fixes", payload: flat.slice(0, 100) });
    await prefetchVariables();
}
function aggregateAuditTotals(result) {
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
            if (f.priority === "P1")
                p1++;
            else if (f.priority === "P2")
                p2++;
            else
                p3++;
        }
    }
    return {
        coverage: total > 0 ? Math.round((bound / total) * 1000) / 10 : 0,
        fromDS: ds, amb, cust, p1, p2, p3,
        screens: result.screens.length,
    };
}
function collectNodeTreeForAudit(scope) {
    const minimal = (n) => {
        const base = { id: n.id, type: n.type, name: n.name };
        if ("absoluteBoundingBox" in n && n.absoluteBoundingBox) {
            const bb = n.absoluteBoundingBox;
            base.absoluteBoundingBox = { x: bb.x, y: bb.y, width: bb.width, height: bb.height };
        }
        if ("fills" in n && n.fills && n.fills.length) {
            base.fills = n.fills.map(serializePaint);
        }
        if ("strokes" in n && n.strokes && n.strokes.length) {
            base.strokes = n.strokes.map(serializePaint);
        }
        if ("layoutMode" in n) {
            base.layoutMode = n.layoutMode;
            base.itemSpacing = n.itemSpacing;
            base.paddingLeft = n.paddingLeft;
            base.paddingRight = n.paddingRight;
            base.paddingTop = n.paddingTop;
            base.paddingBottom = n.paddingBottom;
        }
        if ("cornerRadius" in n) {
            const cr = n.cornerRadius;
            if (typeof cr === "number")
                base.cornerRadius = cr;
        }
        if ("componentId" in n)
            base.componentId = n.componentId;
        if ("styles" in n)
            base.styles = n.styles;
        if ("children" in n && n.children) {
            base.children = n.children.map(minimal);
        }
        return base;
    };
    if (scope === "selection") {
        const sel = figma.currentPage.selection;
        if (sel.length === 0)
            return null;
        return { type: "DOCUMENT", children: [{ type: "CANVAS", name: "Selection", children: sel.map(minimal) }] };
    }
    if (scope === "page") {
        return { type: "DOCUMENT", children: [minimal(figma.currentPage)] };
    }
    return { type: "DOCUMENT", children: figma.root.children.map(minimal) };
}
function serializePaint(p) {
    var _a, _b;
    if (p.type === "SOLID") {
        return {
            type: "SOLID",
            color: { r: p.color.r, g: p.color.g, b: p.color.b },
            opacity: (_a = p.opacity) !== null && _a !== void 0 ? _a : 1,
            visible: p.visible !== false,
            boundVariables: (_b = p.boundVariables) !== null && _b !== void 0 ? _b : undefined,
        };
    }
    return { type: p.type, visible: p.visible !== false };
}
async function prefetchVariables() {
    const vars = await figma.variables.getLocalVariablesAsync();
    knownVariables = {};
    for (const v of vars)
        knownVariables[v.id] = v;
}
async function applyFix(p) {
    var _a, _b;
    const node = await figma.getNodeByIdAsync(p.node_id);
    if (!node || node.removed) {
        toast(`Node ${p.node_id} not found (deleted?)`, "error");
        return;
    }
    let variable = null;
    if (p.variable_id && knownVariables[p.variable_id]) {
        variable = knownVariables[p.variable_id];
    }
    else {
        const all = await figma.variables.getLocalVariablesAsync();
        variable = (_a = all.find((v) => v.name.toLowerCase() === p.token_path.toLowerCase())) !== null && _a !== void 0 ? _a : null;
    }
    if (!variable) {
        toast(`Token "${p.token_path}" isn't a local variable. Copy path manually.`, "error");
        return;
    }
    try {
        if (p.property === "fill" && "fills" in node) {
            const fills = node.fills;
            if (fills && fills.length > 0 && fills[0].type === "SOLID") {
                const updated = fills.map((paint, i) => {
                    if (i !== 0)
                        return paint;
                    return figma.variables.setBoundVariableForPaint(paint, "color", variable);
                });
                node.fills = updated;
                send({ type: "audit-applied", payload: { node_id: p.node_id, token_path: p.token_path } });
                toast(`Applied ${p.token_path}`, "success");
                return;
            }
        }
        toast(`Apply for "${p.property}" not yet supported in v1.`, "error");
    }
    catch (e) {
        toast(`Apply failed: ${(_b = e === null || e === void 0 ? void 0 : e.message) !== null && _b !== void 0 ? _b : "unknown"}. Plan tier?`, "error");
    }
}
async function runInject(kind) {
    toast(`Fetching manifest…`, "info");
    let resp;
    try {
        resp = await fetch(`${docsURL}/icons/glyph/manifest.json`);
    }
    catch (e) {
        toast(`Cannot reach ${docsURL}`, "error");
        return;
    }
    if (!resp.ok) {
        toast(`Manifest fetch failed: ${resp.status}`, "error");
        return;
    }
    const m = (await resp.json());
    const targets = m.icons.filter((e) => { var _a; return ((_a = e.kind) !== null && _a !== void 0 ? _a : "") === kind; });
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
            if (!r.ok)
                continue;
            const svg = await r.text();
            const node = figma.createNodeFromSvg(svg);
            node.name = entry.slug;
            host.appendChild(node);
            done++;
            if (done % 20 === 0) {
                send({ type: "inject-progress", payload: { done, total: targets.length } });
            }
        }
        catch (_a) { }
    }
    figma.currentPage.selection = [host];
    figma.viewport.scrollAndZoomIntoView([host]);
    send({ type: "inject-progress", payload: { done, total: targets.length } });
    toast(`Injected ${done} ${kind}${done === 1 ? "" : "s"}`, "success");
}
/* ── Toast ──────────────────────────────────────────────────────────── */
function toast(message, level = "info") {
    send({ type: "toast", payload: { message, level, ts: Date.now() } });
}
