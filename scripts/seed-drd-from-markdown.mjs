#!/usr/bin/env node
/**
 * scripts/seed-drd-from-markdown.mjs
 *
 * Convert a markdown file into a BlockNote-shaped Y.Doc and push it into
 * ds-service via the MCP `drd.append` tool. Used to seed an existing
 * Design Requirement Doc — including rich tables, headings, lists,
 * blockquotes — into the Atlas DRD editor for a sub_flow.
 *
 * Usage:
 *   node scripts/seed-drd-from-markdown.mjs <sub_flow_slug> <markdown-path> [user_id]
 *
 * Example:
 *   node scripts/seed-drd-from-markdown.mjs \
 *     indstocks/unified-watchlist-screener \
 *     "$HOME/INDmoney/INDstocks/Outputs/Unified Watchlist Screener/DRD-Unified Watchlist Screener-V1-3rd May'26.md"
 *
 * Requires DS_SERVICE_URL (defaults to http://localhost:8080) and an
 * authenticated context — uses DEV_AUTH_BYPASS-compatible user_id when
 * the bypass is active. Pass an explicit user_id arg otherwise.
 */

import { JSDOM } from "jsdom";
import fs from "node:fs";
import path from "node:path";
import * as Y from "yjs";

// jsdom shim — BlockNote's core editor needs window/document at import time.
const dom = new JSDOM("<!DOCTYPE html><html><body></body></html>", {
  url: "http://localhost/",
  pretendToBeVisual: true,
});
// Bind only properties that are writable on globalThis. Newer Node
// runtimes expose `navigator` as a read-only getter — skip it; BlockNote
// reads navigator off `window` directly which we set below.
function safeAssign(key, value) {
  try {
    Object.defineProperty(globalThis, key, { value, writable: true, configurable: true });
  } catch {
    // Ignore non-configurable slots (e.g. Node's built-in navigator).
  }
}
safeAssign("window", dom.window);
safeAssign("document", dom.window.document);
safeAssign("HTMLElement", dom.window.HTMLElement);
safeAssign("Node", dom.window.Node);
safeAssign("Element", dom.window.Element);
safeAssign("DocumentFragment", dom.window.DocumentFragment);
safeAssign("getComputedStyle", dom.window.getComputedStyle);

const { ServerBlockNoteEditor } = await import("@blocknote/server-util");

const [, , slug, mdPath, userIDArg] = process.argv;
if (!slug || !mdPath) {
  console.error("usage: seed-drd-from-markdown.mjs <sub_flow_slug> <markdown-path> [user_id]");
  process.exit(2);
}
if (!slug.includes("/")) {
  console.error("sub_flow_slug must be in {sub_product}/{sub_flow} format");
  process.exit(2);
}
if (!fs.existsSync(mdPath)) {
  console.error("markdown file not found:", mdPath);
  process.exit(2);
}

const DS = process.env.DS_SERVICE_URL ?? "http://localhost:8080";
const USER_ID = userIDArg ?? process.env.SEED_USER_ID ?? "dev-bypass";

const markdown = fs.readFileSync(mdPath, "utf8");
console.log(`Markdown: ${markdown.length} bytes from ${path.basename(mdPath)}`);

// 1. Parse markdown → BlockNote blocks via the ServerBlockNoteEditor.
const editor = ServerBlockNoteEditor.create();
const blocks = await editor.tryParseMarkdownToBlocks(markdown);
console.log(`Parsed ${blocks.length} blocks (${countTypes(blocks)})`);

// 2. Use the ServerBlockNoteEditor's blocksToYDoc utility — BlockNote
//    persists into a Y.Doc whose top-level fragment matches the binding
//    the in-browser editor reads via @hocuspocus/provider + y-prosemirror.
const ydoc = editor.blocksToYDoc(blocks, "document");
const update = Y.encodeStateAsUpdate(ydoc);
const b64 = Buffer.from(update).toString("base64");
console.log(`YDoc encoded: ${update.length} bytes raw, ${b64.length} chars base64`);

// 3. Push to ds-service via MCP drd.append.
const authHeader = process.env.DS_AUTH_TOKEN
  ? { Authorization: `Bearer ${process.env.DS_AUTH_TOKEN}` }
  : {};
const res = await fetch(`${DS}/v1/mcp/invoke/drd.append`, {
  method: "POST",
  headers: { "Content-Type": "application/json", ...authHeader },
  body: JSON.stringify({
    sub_flow_slug: slug,
    content_bytes_base64: b64,
    user_id: USER_ID,
  }),
});
const body = await res.json();
if (!res.ok || body.error) {
  console.error("MCP drd.append failed:", body);
  process.exit(1);
}
console.log(`✓ Seeded DRD for ${slug} → flow_id=${body.data.flow_id}, revision=${body.data.revision}, ${body.data.bytes_persisted} bytes persisted`);

function countTypes(blocks) {
  const counts = {};
  for (const b of blocks) {
    counts[b.type] = (counts[b.type] ?? 0) + 1;
  }
  return Object.entries(counts).map(([t, n]) => `${n} ${t}`).join(", ");
}
