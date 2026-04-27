/**
 * INDmoney DS Sync — Figma plugin main thread.
 *
 * Runs in Figma's sandbox; talks to the docs-site / ds-service via
 * networkAccess.allowedDomains. Injects icons + components from the public
 * manifest, and posts the user's current selection back for analysis.
 *
 * Build: `tsc --target es2017 --module none code.ts && rm code.ts`
 *        (or run via the Figma plugin CLI).
 */

interface MessageFromUI {
  type:
    | "request-selection"
    | "inject-icons"
    | "inject-components"
    | "ping-docs"
    | "set-base-url";
  payload?: unknown;
}

interface MessageToUI {
  type: "selection" | "injection-progress" | "injection-done" | "error" | "info";
  payload?: unknown;
}

let baseURL = "https://indmoney-design-system-docs.vercel.app";

const send = (msg: MessageToUI) => figma.ui.postMessage(msg);

figma.showUI(__html__, { width: 360, height: 540, themeColors: true });

figma.ui.onmessage = async (msg: MessageFromUI) => {
  try {
    switch (msg.type) {
      case "set-base-url":
        if (typeof msg.payload === "string" && msg.payload.startsWith("http")) {
          baseURL = msg.payload.replace(/\/$/, "");
          send({ type: "info", payload: `Base URL set to ${baseURL}` });
        }
        return;
      case "request-selection":
        return reportSelection();
      case "inject-icons":
        return injectFromManifest("icon");
      case "inject-components":
        return injectFromManifest("component");
      case "ping-docs":
        return pingDocs();
    }
  } catch (e: unknown) {
    send({ type: "error", payload: (e as Error).message });
  }
};

// Menu commands map to UI messages so the user gets the panel for context.
if (figma.command === "syncSelection") {
  reportSelection();
}
if (figma.command === "injectIcons") {
  injectFromManifest("icon");
}
if (figma.command === "injectComponents") {
  injectFromManifest("component");
}
if (figma.command === "openDocsSite") {
  figma.openExternal(baseURL);
  figma.closePlugin();
}

/** Stream the current selection's structural digest back to the panel. */
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

interface ManifestEntry {
  slug: string;
  name: string;
  category: string;
  file: string;
  width: number;
  height: number;
  variants?: Array<{ name: string; file: string; width: number; height: number }>;
}

interface Manifest {
  icons: ManifestEntry[];
}

const ICON_CATEGORIES = new Set(["Icon", "Filled Icons"]);
const COMPONENT_CATEGORIES = new Set(["ui"]);

/**
 * Pull the published manifest from the docs site, filter to the selected
 * kind, fetch each SVG, and drop them into the current page as components.
 */
async function injectFromManifest(kind: "icon" | "component") {
  const manifestURL = `${baseURL}/icons/glyph/manifest.json`;
  send({ type: "info", payload: `Fetching ${manifestURL}` });
  const resp = await fetch(manifestURL);
  if (!resp.ok) throw new Error(`Manifest fetch failed: ${resp.status}`);
  const manifest = (await resp.json()) as Manifest;

  const filter = (e: ManifestEntry) =>
    kind === "icon"
      ? ICON_CATEGORIES.has(e.category)
      : COMPONENT_CATEGORIES.has(e.category);
  const targets = manifest.icons.filter(filter);
  send({
    type: "info",
    payload: `Injecting ${targets.length} ${kind}${targets.length === 1 ? "" : "s"}…`,
  });

  // Build a frame to host the imports
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
        send({
          type: "injection-progress",
          payload: { done, total: targets.length },
        });
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
    send({
      type: "info",
      payload: resp.ok
        ? `Reachable: ${baseURL} (${resp.status})`
        : `Reachable but ${resp.status}`,
    });
  } catch (e) {
    send({ type: "error", payload: `Cannot reach ${baseURL}: ${(e as Error).message}` });
  }
}
