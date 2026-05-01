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
var _a;
const send = (msg) => figma.ui.postMessage(msg);
/* ── State ──────────────────────────────────────────────────────────── */
let auditServerURL = "http://localhost:7474";
let docsURL = "https://indmoney-design-system-docs.vercel.app";
// Phase 7.8 — docs-site auth token used by the projects.send POST. The
// user pastes their JWT into the plugin's "Settings" once; we persist
// it via figma.clientStorage so it survives plugin reloads. Stays null
// until set; projects.send rejects with a clear error in that case.
let docsAuthToken = null;
let healthTimer = null;
let knownVariables = {};
// Projects mode (U3) — when the UI is on the Projects tab, selectionchange
// re-runs detection. The flag is toggled from the UI ("projects.set-active").
// Detection itself is debounced to coalesce rapid marquee-drag selectionchange
// bursts.
let projectsModeActive = false;
let projectsDetectTimer = null;
let allPagesLoaded = false;
// 440 × 720 — comfortable for the redesigned three-mode shell. Mode picker
// + status + scrollable pane + toaster all fit without crowding at this
// size, and the plugin is still narrow enough to dock alongside the canvas.
figma.showUI(__html__, { width: 440, height: 720, themeColors: true });
figma.ui.onmessage = async (msg) => {
    var _a, _b, _c, _d, _e, _f, _g, _h;
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
                    const seen = (await figma.clientStorage.getAsync("projects.firstrun-seen")) === true;
                    send({ type: "projects.firstrun-status", payload: { seen } });
                }
                return;
            case "projects.firstrun-dismiss":
                // Persists across plugin re-runs. Cleared via Figma's clientStorage
                // reset (Plugins → Manage plugins → reinstall) or by deleting the
                // key manually for QA.
                await figma.clientStorage.setAsync("projects.firstrun-seen", true);
                return;
            case "set-docs-token": {
                // Phase 7.8 — paste-once docs-site JWT for the projects.send POST.
                // Empty string clears the stored token (logout flow). The token
                // never leaves the plugin's clientStorage; it's only sent as the
                // Authorization header on /api/projects/export calls.
                const raw = (_a = msg.payload) !== null && _a !== void 0 ? _a : "";
                // Be forgiving about how the user pasted the token. Common
                // mistakes (observed during operator setup): wrapping quotes
                // because they copied the console output literally, a stray
                // `Bearer ` prefix copied from a curl example, or — worst —
                // pasting the entire localStorage JSON blob and expecting us
                // to dig out the field. Normalize all of these before the
                // shape check fires, so the user only sees an error when the
                // value is fundamentally not a JWT.
                let v = raw.trim();
                // If they pasted the whole zustand-persist blob, try to pull
                // .state.token out of it.
                if (v.startsWith("{")) {
                    try {
                        const parsed = JSON.parse(v);
                        const candidate = (_e = (_d = (_c = (_b = parsed === null || parsed === void 0 ? void 0 : parsed.state) === null || _b === void 0 ? void 0 : _b.token) !== null && _c !== void 0 ? _c : parsed === null || parsed === void 0 ? void 0 : parsed.token) !== null && _d !== void 0 ? _d : parsed === null || parsed === void 0 ? void 0 : parsed.access_token) !== null && _e !== void 0 ? _e : null;
                        if (typeof candidate === "string")
                            v = candidate.trim();
                    }
                    catch (_j) {
                        /* fall through to the shape check below */
                    }
                }
                // Strip a leading "Bearer " (curl/Authorization-header copy).
                v = v.replace(/^Bearer\s+/i, "");
                // Strip surrounding quotes (single, double, or smart). Console
                // output often comes wrapped in quotes when copied as a string.
                v = v.replace(/^['"“‘]+|['"”’]+$/g, "");
                // Strip any internal whitespace — JWTs never contain spaces or
                // newlines, but copy-paste sometimes inserts them.
                v = v.replace(/\s+/g, "");
                if (v === "") {
                    docsAuthToken = null;
                    await figma.clientStorage.deleteAsync("docs_auth_token");
                    send({
                        type: "projects.send-result",
                        payload: { ok: true, info: "Token cleared." },
                    });
                    send({ type: "docs-token-state", payload: { hasToken: false } });
                    return;
                }
                // Minimal sanity check — JWTs are three base64url segments
                // separated by dots. Bcrypt hashes / passwords don't match.
                if (!/^[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$/.test(v)) {
                    // Surface a more actionable detail than the bare format
                    // requirement so the user can self-diagnose. The first 12
                    // chars are safe to echo back — they're the base64-encoded
                    // JWT header which is public anyway.
                    const dots = (v.match(/\./g) || []).length;
                    const preview = v.length > 0 ? v.slice(0, 16) + (v.length > 16 ? "…" : "") : "(empty)";
                    send({
                        type: "projects.send-result",
                        payload: {
                            ok: false,
                            error: "invalid_token_format",
                            detail: `Not a JWT — got ${v.length} chars, ${dots} dot(s). Preview: ${preview}. Expected eyJ…aaa.bbb.ccc from JSON.parse(localStorage['indmoney-ds-auth']).state.token`,
                        },
                    });
                    return;
                }
                docsAuthToken = v;
                await figma.clientStorage.setAsync("docs_auth_token", v);
                send({
                    type: "projects.send-result",
                    payload: { ok: true, info: "Token saved." },
                });
                send({ type: "docs-token-state", payload: { hasToken: true } });
                return;
            }
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
            case "apply-group":
                return applyGroup(msg.payload);
            case "apply-many":
                return applyMany(msg.payload);
            case "inject":
                return runInject(msg.payload.kind);
            case "open-docs":
                figma.openExternal(docsURL);
                return;
            case "request-selection-summary":
                return emitSelectionSummary();
            case "projects.set-active":
                projectsModeActive = !!(msg.payload && msg.payload.active);
                if (projectsModeActive)
                    await runProjectsDetection();
                return;
            case "projects.refresh-detection":
                return runProjectsDetection();
            case "projects.send":
                // Phase 7.8 — real export wiring. POSTs to the docs-site proxy at
                // /api/projects/export which forwards to ds-service's HandleExport.
                // The plugin's manifest already allowlists the Vercel origin so
                // we don't need to add the ephemeral tunnel URL here.
                //
                // The boot-time clientStorage read happens in a parallel async
                // IIFE — if the user clicks Send before that resolves,
                // docsAuthToken is null even though a saved token exists. Lazy
                // re-read here closes that race.
                if (!docsAuthToken) {
                    const tok = (await figma.clientStorage.getAsync("docs_auth_token"));
                    if (tok)
                        docsAuthToken = tok;
                }
                if (!docsAuthToken) {
                    send({
                        type: "projects.send-result",
                        payload: {
                            ok: false,
                            error: "auth_required",
                            detail: "Set your docs-site token in plugin Settings first (paste the JWT from localStorage['indmoney-ds-auth']).",
                        },
                    });
                    // Don't double-fire a figma.notify here — the UI's
                    // projects.send-result handler now opens Settings + focuses
                    // the JWT input, which is far more discoverable than a
                    // floating string mentioning "plugin Settings" while the
                    // operator is busy hunting for the cog icon.
                    return;
                }
                try {
                    const res = await fetch(`${docsURL}/api/projects/export`, {
                        method: "POST",
                        headers: {
                            "Content-Type": "application/json",
                            "Authorization": `Bearer ${docsAuthToken}`,
                            // Lets the operator correlate plugin → docs → ds-service
                            // logs when something goes wrong; ds-service threads this
                            // ID through SSE events too.
                            "X-Trace-ID": (typeof crypto !== "undefined" && crypto.randomUUID)
                                ? crypto.randomUUID() : String(Date.now()),
                        },
                        body: JSON.stringify(msg.payload),
                    });
                    const bodyText = await res.text();
                    let body = {};
                    try {
                        body = JSON.parse(bodyText);
                    }
                    catch ( /* keep text */_k) { /* keep text */ }
                    if (!res.ok) {
                        send({
                            type: "projects.send-result",
                            payload: {
                                ok: false,
                                status: res.status,
                                error: body.error || "http_error",
                                detail: body.detail || bodyText.slice(0, 200),
                            },
                        });
                        toast(`Export failed (HTTP ${res.status}): ${body.detail || body.error || "unknown"}`, "error");
                        return;
                    }
                    send({
                        type: "projects.send-result",
                        payload: {
                            ok: true,
                            project_id: body.project_id,
                            version_id: body.version_id,
                            deeplink: body.deeplink,
                            trace_id: body.trace_id,
                        },
                    });
                    toast(`Exported — project ${(_g = (_f = body.project_id) === null || _f === void 0 ? void 0 : _f.slice(0, 8)) !== null && _g !== void 0 ? _g : "?"}…`, "success");
                }
                catch (err) {
                    // "Failed to fetch" is browser-speak for "request never made
                    // it onto the wire" — could be CORS, manifest-domain gate,
                    // DNS, or the tunnel being down. Probe a known-good Vercel
                    // path to disambiguate before reporting back.
                    const e = err;
                    let probe = "skipped";
                    try {
                        const p = await fetch(`${docsURL}/favicon.ico`, { method: "GET" });
                        probe = `favicon=${p.status}`;
                    }
                    catch (probeErr) {
                        probe = `favicon-failed: ${probeErr.message}`;
                    }
                    const detail = [
                        `${e.name || "Error"}: ${e.message}`,
                        `probe(${probe})`,
                        e.cause ? `cause=${JSON.stringify(e.cause).slice(0, 120)}` : null,
                    ].filter(Boolean).join(" · ");
                    send({
                        type: "projects.send-result",
                        payload: {
                            ok: false,
                            error: "network",
                            detail,
                        },
                    });
                    toast(`Couldn't reach ${docsURL}: ${detail}`, "error");
                    // Mirror to plugin console so the user can grab a fuller
                    // stack from Figma's plugin DevTools if needed.
                    console.error("[projects.send] fetch failed", { url: `${docsURL}/api/projects/export`, err, probe });
                }
                return;
            case "projects.autofix":
                return runAutoFix(msg.payload);
        }
    }
    catch (e) {
        toast(`Error: ${(_h = e.message) !== null && _h !== void 0 ? _h : "unknown"}`, "error");
    }
};
// Selection-change watcher — emits a live summary so the UI can update without
// the user clicking anything. When Projects mode is active, also re-runs the
// smart-grouping detection (debounced 150ms to coalesce marquee bursts).
figma.on("selectionchange", () => {
    emitSelectionSummary();
    if (projectsModeActive) {
        if (projectsDetectTimer)
            clearTimeout(projectsDetectTimer);
        projectsDetectTimer = setTimeout(() => {
            void runProjectsDetection();
        }, 150);
    }
});
// Restore stored URL + token on boot.
(async () => {
    const stored = (await figma.clientStorage.getAsync("audit_server_url"));
    if (stored)
        auditServerURL = stored;
    const tok = (await figma.clientStorage.getAsync("docs_auth_token"));
    if (tok)
        docsAuthToken = tok;
    // Tell the UI whether we've already restored a token, so the
    // Settings entry-point dot reflects reality on first paint.
    send({ type: "docs-token-state", payload: { hasToken: !!docsAuthToken } });
})();
// Direct menu commands.
if (figma.command === "auditFile")
    runAudit("file").catch(() => { });
if (figma.command === "auditSelection")
    runAudit("selection").catch(() => { });
if (figma.command === "publishSelection")
    runPublish().catch(() => { });
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
if (figma.command === "autofix") {
    // Phase 4.1 — deeplink → UI handoff. Figma resolved the parameters
    // declared in manifest.json (?slug=…&violation_id=…) and exposes them
    // on figma.parameters. We pump them into the UI which renders the
    // confirm-step panel; the UI then echoes back projects.autofix once
    // the user clicks Apply.
    const params = (_a = figma.parameters) === null || _a === void 0 ? void 0 : _a.values;
    const slug = params === null || params === void 0 ? void 0 : params.slug;
    const violationID = params === null || params === void 0 ? void 0 : params.violation_id;
    if (!slug || !violationID) {
        toast("Auto-fix: missing slug or violation_id parameter.", "error");
    }
    else {
        setTimeout(() => {
            send({
                type: "projects.autofix-prompt",
                payload: { slug, violation_id: violationID },
            });
        }, 100);
    }
}
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
    var _a;
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
                    file_key: figma.fileKey || null,
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
    }
    catch (e) {
        const msg = (_a = e.message) !== null && _a !== void 0 ? _a : "unknown";
        let diagnosis = `Cannot reach ${auditServerURL}. Run \`npm run audit:serve\` in the docs repo.`;
        if (msg.toLowerCase().includes("cors") || msg.toLowerCase().includes("blocked")) {
            diagnosis = `CORS blocked the request. The audit-server allows any origin by default — double-check it's the version from this repo.`;
        }
        else if (msg.toLowerCase().includes("failed to fetch") || msg.toLowerCase().includes("network")) {
            diagnosis = `Network error. Is \`npm run audit:serve\` actually running? Try opening ${auditServerURL}/__health in a browser tab to confirm.`;
        }
        send({
            type: "health",
            payload: { connected: false, server_url: auditServerURL, error: msg, diagnosis },
        });
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
    // Forward component matches that have a matched_slug — these power the
    // "Component matches" section. Reject decisions are dropped to keep the
    // panel signal-rich; ambiguous + accept both surface so designers can
    // verify ambiguous cases manually.
    const matches = [];
    for (const s of data.result.screens) {
        if (!s.component_matches)
            continue;
        for (const m of s.component_matches) {
            if (m.decision === "reject" || !m.matched_slug)
                continue;
            matches.push(m);
        }
    }
    send({ type: "audit-matches", payload: matches.slice(0, 60) });
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
    // We don't preload library variables — they require N+1 API calls and
    // designers may have many libraries. resolveVariable does an on-demand
    // library walk only when the local-by-name match fails.
}
/* libraryByName caches the most recent successful library lookups so
 * "Apply to all 47" doesn't re-walk every collection per node. Keyed by
 * the normalized name returned from `normalizeTokenName`. */
const libraryByName = {};
/** Strip everything but alphanumerics + lowercase. So "colour.surface.
 *  surface-white", "Surface/Surface White", and "surface-white" all
 *  collapse to the same key, surviving DTCG path → Figma variable name
 *  formatting differences. */
function normalizeTokenName(name) {
    return name.toLowerCase().replace(/[^a-z0-9]+/g, "");
}
async function applyFix(p) {
    var _a;
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
            const fills = node.fills;
            if (fills && fills.length > 0 && fills[0].type === "SOLID") {
                const updated = fills.map((paint, i) => {
                    if (i !== 0)
                        return paint;
                    return figma.variables.setBoundVariableForPaint(paint, "color", variable);
                });
                node.fills = updated;
                send({ type: "audit-applied", payload: { node_id: p.node_id, token_path: p.token_path } });
                toast(`Bound ${p.token_path}`, "success");
                return;
            }
        }
        // Stroke — same pattern as fill.
        if (p.property === "stroke" && "strokes" in node) {
            const strokes = node.strokes;
            if (strokes && strokes.length > 0 && strokes[0].type === "SOLID") {
                const updated = strokes.map((paint, i) => {
                    if (i !== 0)
                        return paint;
                    return figma.variables.setBoundVariableForPaint(paint, "color", variable);
                });
                node.strokes = updated;
                send({ type: "audit-applied", payload: { node_id: p.node_id, token_path: p.token_path } });
                toast(`Bound ${p.token_path}`, "success");
                return;
            }
        }
        // Spacing / padding / radius — bind via setBoundVariableForLayoutSizingAndSpacing
        // when the property exists on the node, otherwise fall through.
        if (p.property === "radius" && "cornerRadius" in node) {
            try {
                node.setBoundVariable("topLeftRadius", variable);
                node.setBoundVariable("topRightRadius", variable);
                node.setBoundVariable("bottomLeftRadius", variable);
                node.setBoundVariable("bottomRightRadius", variable);
                send({ type: "audit-applied", payload: { node_id: p.node_id, token_path: p.token_path } });
                toast(`Bound ${p.token_path}`, "success");
                return;
            }
            catch (_b) { }
        }
        if (p.property === "spacing" && "itemSpacing" in node) {
            try {
                node.setBoundVariable("itemSpacing", variable);
                send({ type: "audit-applied", payload: { node_id: p.node_id, token_path: p.token_path } });
                toast(`Bound ${p.token_path}`, "success");
                return;
            }
            catch (_c) { }
        }
        toast(`Apply for "${p.property}" not yet supported.`, "error");
    }
    catch (e) {
        toast(`Apply failed: ${(_a = e === null || e === void 0 ? void 0 : e.message) !== null && _a !== void 0 ? _a : "unknown"}. Plan tier?`, "error");
    }
}
async function applyGroup(p) {
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
        if (!node || node.removed)
            continue;
        if (await applyVariableToNode(node, p.property, variable))
            applied++;
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
async function applyMany(p) {
    let totalApplied = 0;
    let totalTouched = 0;
    for (const g of p.groups) {
        const variable = await resolveVariable(g);
        if (!variable)
            continue;
        for (const id of g.node_ids) {
            totalTouched++;
            const node = await figma.getNodeByIdAsync(id);
            if (!node || node.removed)
                continue;
            if (await applyVariableToNode(node, g.property, variable))
                totalApplied++;
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
async function resolveVariable(g) {
    var _a;
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
        }
        catch (_b) { }
    }
    // The lookup target is whatever name the Figma variable actually has —
    // `figma_name` when the extractor captured it, `token_path` as a fallback
    // for older audit JSON.
    const targets = [g.figma_name, g.token_path].filter(Boolean);
    if (targets.length === 0)
        return null;
    const normTargets = targets.map(normalizeTokenName);
    // Cache hit?
    for (const t of normTargets) {
        if (libraryByName[t])
            return libraryByName[t];
    }
    // 2. Local variables by normalized name.
    if (Object.keys(knownVariables).length === 0)
        await prefetchVariables();
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
                    for (const t of normTargets)
                        libraryByName[t] = imported;
                    return imported;
                }
            }
        }
    }
    catch (e) {
        // Surfaced as a more descriptive toast in the caller.
        figma.notify(`Library lookup failed: ${(_a = e.message) !== null && _a !== void 0 ? _a : "unknown"}`, { error: true });
    }
    return null;
}
async function applyVariableToNode(node, property, variable) {
    try {
        if (property === "fill" && "fills" in node) {
            const fills = node.fills;
            if (!fills || fills.length === 0 || fills[0].type !== "SOLID")
                return false;
            node.fills = fills.map((paint, i) => i === 0 ? figma.variables.setBoundVariableForPaint(paint, "color", variable) : paint);
            return true;
        }
        if (property === "stroke" && "strokes" in node) {
            const strokes = node.strokes;
            if (!strokes || strokes.length === 0 || strokes[0].type !== "SOLID")
                return false;
            node.strokes = strokes.map((paint, i) => i === 0 ? figma.variables.setBoundVariableForPaint(paint, "color", variable) : paint);
            return true;
        }
        if (property === "radius" && "cornerRadius" in node) {
            const n = node;
            try {
                n.setBoundVariable("topLeftRadius", variable);
                n.setBoundVariable("topRightRadius", variable);
                n.setBoundVariable("bottomLeftRadius", variable);
                n.setBoundVariable("bottomRightRadius", variable);
                return true;
            }
            catch (_a) {
                return false;
            }
        }
        if (property === "spacing" && "itemSpacing" in node) {
            try {
                node.setBoundVariable("itemSpacing", variable);
                return true;
            }
            catch (_b) {
                return false;
            }
        }
    }
    catch (_c) { }
    return false;
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
async function runProjectsDetection() {
    var _a;
    // Per the 2026 dynamic-page manifest requirement, the plugin must call
    // loadAllPagesAsync before walking parent chains that may cross page
    // boundaries. We do it once per session (it's cheap-ish; cached in core).
    const warnings = [];
    if (!allPagesLoaded) {
        try {
            // `loadAllPagesAsync` is only present on dynamic-page manifests.
            const fn = figma.loadAllPagesAsync;
            if (typeof fn === "function") {
                await fn.call(figma);
                allPagesLoaded = true;
            }
            else {
                warnings.push("loadAllPagesAsync not available; falling back to current page only.");
            }
        }
        catch (e) {
            warnings.push(`loadAllPagesAsync failed: ${(_a = e.message) !== null && _a !== void 0 ? _a : "unknown"}`);
        }
    }
    const sel = figma.currentPage.selection;
    // Collect candidate "screen" frames — the user may have selected a
    // SECTION (we expand to its children), a FRAME (we keep it), or a mix.
    const frames = [];
    for (const n of sel) {
        if (n.type === "SECTION") {
            for (const c of n.children) {
                if (c.type === "FRAME")
                    frames.push(c);
            }
        }
        else if (n.type === "FRAME") {
            frames.push(n);
        }
        else if (n.type === "GROUP" || n.type === "COMPONENT" || n.type === "INSTANCE") {
            // Treat top-level container nodes as candidate screens too — designers
            // sometimes select a row of components rather than frames.
            frames.push(n);
        }
    }
    if (frames.length === 0) {
        send({
            type: "projects.detected-groups",
            payload: {
                file_key: figma.fileKey || "unknown",
                file_name: figma.root.name,
                total_frames: 0,
                groups: [],
                warnings: warnings.concat(["Select frames inside a section to start grouping."]),
            },
        });
        return;
    }
    // ── Step 1: group by enclosing SECTION ────────────────────────────
    // Walk parent chain; key = section.id, OR `freeform-<n>` if no section.
    const sectionGroups = new Map();
    let freeformIdx = 0;
    const freeformBucket = new Map();
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
        }
        else {
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
    const groups = [];
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
            file_key: figma.fileKey || "unknown",
            file_name: figma.root.name,
            total_frames: frames.length,
            groups,
            warnings,
        },
    });
}
function findEnclosingSection(node) {
    var _a;
    let p = node.parent;
    while (p) {
        if (p.type === "SECTION")
            return p;
        if (p.type === "PAGE" || p.type === "DOCUMENT")
            return null;
        p = (_a = p.parent) !== null && _a !== void 0 ? _a : null;
    }
    return null;
}
async function buildGroup(groupKey, section, rawFrames) {
    // Build frame info objects. `explicitVariableModes` returns a map of
    // VariableCollectionId → mode-id, whose mode-id we look up against the
    // collection to derive a human label.
    const frames = [];
    for (const f of rawFrames) {
        frames.push(await buildFrameInfo(f));
    }
    // Pair detection: for each frame, look at siblings sharing a column
    // (|Δx|<10) and a different y, where their explicitVariableModes share a
    // VariableCollectionId but a different mode id.
    const pairs = [];
    const used = new Set();
    for (let i = 0; i < frames.length; i++) {
        const a = frames[i];
        if (used.has(a.id))
            continue;
        const matches = [];
        for (let j = 0; j < frames.length; j++) {
            if (i === j)
                continue;
            const b = frames[j];
            if (used.has(b.id))
                continue;
            if (Math.abs(a.x - b.x) >= 10)
                continue;
            if (a.y === b.y)
                continue;
            // Variable-mode cross-check: must share a collection, different mode.
            if (!a.variable_collection_id || !b.variable_collection_id)
                continue;
            if (a.variable_collection_id !== b.variable_collection_id)
                continue;
            if (a.variable_mode_id === b.variable_mode_id)
                continue;
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
            for (const m of matches)
                used.add(m.id);
        }
    }
    const unpaired = frames.filter((f) => !used.has(f.id)).map((f) => f.id);
    // Platform auto-detect — use the widest frame in the group (most
    // representative of "the screen size" rather than a stray button).
    const widest = frames.reduce((acc, f) => (f.width > acc ? f.width : acc), 0);
    let platform_default = null;
    if (widest > 0 && widest < 500)
        platform_default = "mobile";
    else if (widest >= 1024)
        platform_default = "web";
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
async function buildFrameInfo(node) {
    const x = "x" in node ? node.x : 0;
    const y = "y" in node ? node.y : 0;
    const width = "width" in node ? Math.round(node.width) : 0;
    const height = "height" in node ? Math.round(node.height) : 0;
    // explicitVariableModes is { [VariableCollectionId]: mode_id }
    const explicit = {};
    let collectionId = null;
    let modeId = null;
    let modeLabel = null;
    try {
        const evm = node.explicitVariableModes;
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
    }
    catch (_a) { }
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
const modeLabelCache = {};
async function resolveModeLabel(collectionId, modeId) {
    if (modeLabelCache[collectionId] && modeLabelCache[collectionId][modeId]) {
        return modeLabelCache[collectionId][modeId];
    }
    try {
        const coll = await figma.variables.getVariableCollectionByIdAsync(collectionId);
        if (!coll)
            return null;
        const map = {};
        for (const m of coll.modes) {
            map[m.modeId] = m.name;
        }
        modeLabelCache[collectionId] = map;
        return map[modeId] || null;
    }
    catch (_a) {
        return null;
    }
}
/**
 * depth=2 structural skeleton — walks node + its direct children + grandchildren,
 * concatenating type/path tokens. Used to detect when two ostensibly-paired
 * frames diverge structurally beyond what bound-variable swaps explain.
 */
function depth2Skeleton(node) {
    const parts = [node.type];
    const children = "children" in node ? node.children : [];
    for (const c of children) {
        parts.push("[" + c.type);
        const grand = "children" in c ? c.children : [];
        for (const g of grand) {
            parts.push("(" + g.type + ")");
        }
        parts.push("]");
    }
    return parts.join("");
}
/* ── Toast ──────────────────────────────────────────────────────────── */
function toast(message, level = "info") {
    send({ type: "toast", payload: { message, level, ts: Date.now() } });
}
async function runAutoFix(payload) {
    var _a, _b, _c, _d, _e, _f, _g, _h;
    if (!payload || !payload.slug || !payload.violation_id) {
        toast("Auto-fix: missing slug or violation_id.", "error");
        return;
    }
    const headers = { Accept: "application/json" };
    // Plugin doesn't carry the JWT for the docs site (different host); the
    // /v1/projects/.../violations/:id endpoint is auth-gated, so the user
    // must have configured the plugin's auth token via Figma plugin
    // settings. Tokenisation polish lands separately.
    let v;
    try {
        const res = await fetch(`${auditServerURL}/v1/projects/${encodeURIComponent(payload.slug)}/violations/${encodeURIComponent(payload.violation_id)}`, { headers });
        if (!res.ok) {
            toast(`Auto-fix: GET violation failed (${res.status}).`, "error");
            return;
        }
        v = (await res.json());
    }
    catch (err) {
        toast(`Auto-fix: ${(_a = err.message) !== null && _a !== void 0 ? _a : "network"}.`, "error");
        return;
    }
    // Phase 4.1 — fetch-only mode: hand the violation back to the UI so
    // it can render the confirm panel. We also try to pre-resolve the
    // target variable from the violation's `suggestion` field — most rule
    // runners pre-fill that with the token path the auditor recommended.
    // When resolved, the UI receives a ready-to-apply payload; otherwise
    // the panel shows "manual target needed" and Phase 4.2's picker takes
    // over.
    if (!payload.target) {
        let resolvedVarID = null;
        let resolvedTokenPath = null;
        if (v.suggestion) {
            try {
                const variable = await resolveVariable({
                    token_path: v.suggestion,
                    figma_name: v.suggestion,
                });
                if (variable) {
                    resolvedVarID = variable.id;
                    resolvedTokenPath = v.suggestion;
                }
            }
            catch (_j) {
                // resolution best-effort — UI will surface the unresolved state.
            }
        }
        send({
            type: "projects.autofix-violation",
            payload: {
                slug: payload.slug,
                violation: v,
                resolved: resolvedVarID
                    ? { variable_id: resolvedVarID, token_path: resolvedTokenPath }
                    : null,
            },
        });
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
        targetTokenPath: (_b = payload.target) === null || _b === void 0 ? void 0 : _b.token_path,
        targetTextStyleId: (_c = payload.target) === null || _c === void 0 ? void 0 : _c.text_style_id,
        observedNumber: (_d = payload.target) === null || _d === void 0 ? void 0 : _d.observed_number,
    });
    if (!preview) {
        toast(`Auto-fix: insufficient target for ${v.rule_id}.`, "info");
        return;
    }
    // Dispatch.
    let result;
    switch (v.rule_id) {
        case "drift.fill":
        case "unbound.fill":
        case "deprecated.fill":
            if (!((_e = payload.target) === null || _e === void 0 ? void 0 : _e.variable_id) || !v.node_id) {
                toast("Auto-fix: missing variable_id or node_id.", "info");
                return;
            }
            result = await AutoFix.applyFillBinding(v.node_id, payload.target.variable_id);
            break;
        case "drift.text":
        case "unbound.text":
            if (!((_f = payload.target) === null || _f === void 0 ? void 0 : _f.text_style_id) || !v.node_id) {
                toast("Auto-fix: missing text_style_id or node_id.", "info");
                return;
            }
            result = await AutoFix.applyTextStyle(v.node_id, payload.target.text_style_id);
            break;
        case "drift.padding":
        case "drift.gap": {
            const obs = (_g = payload.target) === null || _g === void 0 ? void 0 : _g.observed_number;
            const prop = v.property;
            if (typeof obs !== "number" || !v.node_id) {
                toast("Auto-fix: missing observed_number or node_id.", "info");
                return;
            }
            result = await AutoFix.applySnapPadding(v.node_id, prop, obs);
            break;
        }
        case "drift.radius": {
            const obs = (_h = payload.target) === null || _h === void 0 ? void 0 : _h.observed_number;
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
        toast(`Auto-fix failed: ${result.error}`, "error");
        return;
    }
    // Success ping. Best-effort — even if this fails the Figma file is
    // already updated; the next re-audit will re-classify the violation
    // status correctly.
    try {
        await fetch(`${auditServerURL}/v1/projects/${encodeURIComponent(payload.slug)}/violations/${encodeURIComponent(payload.violation_id)}/fix-applied`, {
            method: "POST",
            headers: { "Content-Type": "application/json", Accept: "application/json" },
            body: JSON.stringify({ note: preview.hint }),
        });
    }
    catch (_k) {
        // swallow — file is already fixed
    }
    toast(`Auto-fix applied: ${preview.hint}`, "success");
}
