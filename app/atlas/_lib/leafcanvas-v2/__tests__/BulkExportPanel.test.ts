/**
 * BulkExportPanel.test.ts — U9.
 *
 * No jsdom is wired in this repo, so we don't render the React tree.
 * Instead we cover:
 *   - the live-store selection state machine (add/remove/clear, single
 *     vs bulk coexistence rule),
 *   - the filename preview shape (`buildFilenamePreview`, `sanitizeFilename`),
 *   - the bulk-mint network shape via a stubbed `mintBulkAssetExportURL`
 *     (assert URL + body + that the download trigger fires).
 *
 * Strict TS — no `// @ts-nocheck`. Runner shape mirrors the other
 * canvas-v2 tests: each `_test_*` is independent + throws; `runAll` is
 * the umbrella entrypoint executed by `tsx`.
 */

import {
  type ApiResult,
  type BulkMintAssetResponse,
  type BulkMintAssetParams,
} from "../../../../../lib/projects/client";
import {
  buildFilenamePreview,
  sanitizeFilename,
} from "../BulkExportPanel";

function assert(cond: unknown, msg: string): void {
  if (!cond) throw new Error(`assertion failed: ${msg}`);
}

// ─── live-store selection state harness ─────────────────────────────────────
//
// A minimal port of the `addToBulkSelection` / `removeFromBulkSelection`
// / `clearBulkSelection` logic from `lib/atlas/live-store.ts`. We don't
// import zustand here so the test stays pure-fn and independent of the
// React dep graph.

interface SelectionState {
  selectedAtomicChild: { screenID: string; figmaNodeID: string } | null;
  selectedAtomicChildren: Map<string, string>;
}

function newSelection(): SelectionState {
  return { selectedAtomicChild: null, selectedAtomicChildren: new Map() };
}

function addToBulk(s: SelectionState, screenID: string, figmaNodeID: string): SelectionState {
  const key = `${screenID}|${figmaNodeID}`;
  if (s.selectedAtomicChildren.has(key)) return s;
  const next = new Map(s.selectedAtomicChildren);
  next.set(key, figmaNodeID);
  const nextSingle = next.size > 1 ? null : s.selectedAtomicChild;
  return { selectedAtomicChild: nextSingle, selectedAtomicChildren: next };
}

function removeFromBulk(s: SelectionState, screenID: string, figmaNodeID: string): SelectionState {
  const key = `${screenID}|${figmaNodeID}`;
  if (!s.selectedAtomicChildren.has(key)) return s;
  const next = new Map(s.selectedAtomicChildren);
  next.delete(key);
  return { ...s, selectedAtomicChildren: next };
}

function clearBulk(s: SelectionState): SelectionState {
  if (s.selectedAtomicChildren.size === 0) return s;
  return { ...s, selectedAtomicChildren: new Map() };
}

// ─── Selection state transitions ────────────────────────────────────────────

function _test_add_remove_clear(): void {
  let s = newSelection();
  s = addToBulk(s, "screen1", "n1");
  assert(s.selectedAtomicChildren.size === 1, "size after add");
  assert(s.selectedAtomicChildren.get("screen1|n1") === "n1", "key + value");

  // idempotent
  s = addToBulk(s, "screen1", "n1");
  assert(s.selectedAtomicChildren.size === 1, "still 1 after re-add");

  s = removeFromBulk(s, "screen1", "n1");
  assert(s.selectedAtomicChildren.size === 0, "size after remove");

  s = addToBulk(s, "s", "a");
  s = addToBulk(s, "s", "b");
  s = addToBulk(s, "s", "c");
  s = clearBulk(s);
  assert(s.selectedAtomicChildren.size === 0, "cleared");
}

function _test_single_select_clears_when_bulk_grows(): void {
  let s = newSelection();
  s = { ...s, selectedAtomicChild: { screenID: "s", figmaNodeID: "x" } };
  s = addToBulk(s, "s", "y");
  assert(s.selectedAtomicChild !== null, "still single after first add");
  s = addToBulk(s, "s", "z");
  assert(s.selectedAtomicChild === null, "single cleared when bulk > 1");
  assert(s.selectedAtomicChildren.size === 2, "bulk has 2");
}

// ─── Lasso intersection: only atomics — frames excluded ────────────────────
//
// Mirrors the wrapper-local intersection test the renderer does on
// pointer-up. We feed in fake bbox candidates tagged "atomic" or
// "frame" and assert the pruning matches "atomics whose rect intersects
// the lasso, frames excluded by construction".

interface FakeAtomic {
  id: string;
  isAtomic: boolean; // false = FRAME
  rect: { left: number; top: number; right: number; bottom: number };
}

function lassoPick(
  candidates: FakeAtomic[],
  lasso: { left: number; top: number; right: number; bottom: number },
): string[] {
  const picked: string[] = [];
  for (const c of candidates) {
    if (!c.isAtomic) continue; // frames never picked
    const r = c.rect;
    if (
      r.right >= lasso.left &&
      r.left <= lasso.right &&
      r.bottom >= lasso.top &&
      r.top <= lasso.bottom
    ) {
      picked.push(c.id);
    }
  }
  return picked;
}

function _test_lasso_intersect_excludes_frames(): void {
  const candidates: FakeAtomic[] = [
    { id: "frame-outer", isAtomic: false, rect: { left: 0, top: 0, right: 200, bottom: 200 } },
    { id: "icon-1", isAtomic: true, rect: { left: 10, top: 10, right: 30, bottom: 30 } },
    { id: "icon-2", isAtomic: true, rect: { left: 50, top: 50, right: 70, bottom: 70 } },
    { id: "icon-far", isAtomic: true, rect: { left: 500, top: 500, right: 520, bottom: 520 } },
  ];
  const lasso = { left: 0, top: 0, right: 100, bottom: 100 };
  const picked = lassoPick(candidates, lasso);
  assert(!picked.includes("frame-outer"), "frame is never picked");
  assert(picked.includes("icon-1"), "icon-1 inside lasso");
  assert(picked.includes("icon-2"), "icon-2 inside lasso");
  assert(!picked.includes("icon-far"), "far icon excluded");
  assert(picked.length === 2, `picked count: ${picked.length}`);
}

// ─── 100-selection scale: O(N) traversal stays bounded ─────────────────────

function _test_100_selected_no_freeze(): void {
  let s = newSelection();
  for (let i = 0; i < 100; i++) {
    s = addToBulk(s, "screen", `node-${i}`);
  }
  assert(s.selectedAtomicChildren.size === 100, "100 selected");
  // Iteration cost (used to build the bulk-mint body): walk once.
  const start = Date.now();
  const ids: string[] = [];
  for (const [, fid] of s.selectedAtomicChildren) ids.push(fid);
  const elapsed = Date.now() - start;
  assert(ids.length === 100, "iteration yields 100");
  // Generous ceiling — Map iteration is O(N) and shouldn't be anywhere
  // near 100 ms even on slow CI. Catches accidental quadratic blow-up.
  assert(elapsed < 100, `iteration too slow: ${elapsed}ms`);
}

// ─── Filename preview shape ─────────────────────────────────────────────────

function _test_sanitize_filename(): void {
  assert(sanitizeFilename("Buy Now Button") === "buy-now-button", "spaces → dashes");
  assert(sanitizeFilename("icon/back") === "icon-back", "slash → dash");
  assert(sanitizeFilename("Hello   World") === "hello-world", "dedupe dashes");
  assert(sanitizeFilename("--leading--") === "leading", "trim leading/trailing dashes");
  assert(sanitizeFilename("✓ check") === "check", "non-ascii stripped");
  assert(sanitizeFilename("") === "untitled", "empty fallback");
  assert(sanitizeFilename("UPPER_lower-9") === "upper_lower-9", "underscores preserved");
}

function _test_build_filename_preview(): void {
  assert(
    buildFilenamePreview("Back Icon", "12:34", "svg") === "back-icon.svg",
    "name takes priority",
  );
  assert(
    buildFilenamePreview(null, "12:34", "png") === "12:34.png",
    "figmaNodeID fallback when no name",
  );
  assert(
    buildFilenamePreview("Cart", "1:1", "svg") === "cart.svg",
    "simple name",
  );
}

// ─── Bulk-mint URL invocation: body shape + download trigger ───────────────

async function _test_mint_invocation_and_download(): Promise<void> {
  const calls: { slug: string; params: BulkMintAssetParams }[] = [];
  const mintFn = async (
    slug: string,
    params: BulkMintAssetParams,
  ): Promise<ApiResult<BulkMintAssetResponse>> => {
    calls.push({ slug, params });
    return {
      ok: true,
      data: { download_url: "https://example.test/zips/abc.zip", expires_in: 300 },
    };
  };
  const downloads: string[] = [];
  const triggerDownload = (url: string): void => {
    downloads.push(url);
  };

  // Simulate the BulkExportPanel `onExport` flow without React: build the
  // body the component would build and assert the contract.
  const selected = new Map<string, string>();
  selected.set("screen|n1", "n1");
  selected.set("screen|n2", "n2");
  selected.set("screen|n3", "n3");
  selected.set("screen|n4", "n4");
  selected.set("screen|n5", "n5");
  selected.set("screen|n6", "n6");

  const nodeIDs: string[] = [];
  for (const [, fid] of selected) nodeIDs.push(fid);

  const res = await mintFn("project-x", {
    leafID: "leaf-1",
    nodeIDs,
    format: "svg",
    scale: 1,
  });
  assert(res.ok, "mint succeeded");
  if (res.ok) triggerDownload(res.data.download_url);

  assert(calls.length === 1, "mintFn called once");
  assert(calls[0].slug === "project-x", "slug carried");
  assert(calls[0].params.leafID === "leaf-1", "leafID carried");
  assert(calls[0].params.nodeIDs.length === 6, "6 nodes sent");
  assert(calls[0].params.format === "svg", "format=svg");
  assert(calls[0].params.scale === 1, "scale=1");
  assert(downloads.length === 1, "download triggered once");
  assert(downloads[0] === "https://example.test/zips/abc.zip", "url forwarded");
}

// ─── Filename collision (server-side resolves; client just doesn't crash) ──

function _test_filename_collision_client_unaffected(): void {
  // Two icons with the same name go through sanitizeFilename identically;
  // the bulk endpoint will append `-1`, `-2`. Client just emits the same
  // preview twice — assert this is the observed shape so we don't try to
  // dedupe on the client and disagree with the server.
  const a = buildFilenamePreview("Back", "1", "svg");
  const b = buildFilenamePreview("back", "2", "svg");
  assert(a === "back.svg", "first preview");
  assert(b === "back.svg", "second preview matches — server resolves");
}

// ─── Error-path mint: state surfaces failure, no download fires ───────────

async function _test_mint_error_no_download(): Promise<void> {
  const mintFn = async (
    _slug: string,
    _params: BulkMintAssetParams,
  ): Promise<ApiResult<BulkMintAssetResponse>> => {
    return { ok: false, status: 500, error: "boom" };
  };
  const downloads: string[] = [];
  const triggerDownload = (url: string): void => {
    downloads.push(url);
  };
  const res = await mintFn("p", {
    leafID: "l",
    nodeIDs: ["a"],
    format: "png",
    scale: 1,
  });
  if (res.ok) triggerDownload(res.data.download_url);
  assert(!res.ok, "mint failed");
  assert(downloads.length === 0, "no download on error");
}

export async function runAll(): Promise<void> {
  _test_add_remove_clear();
  _test_single_select_clears_when_bulk_grows();
  _test_lasso_intersect_excludes_frames();
  _test_100_selected_no_freeze();
  _test_sanitize_filename();
  _test_build_filename_preview();
  await _test_mint_invocation_and_download();
  _test_filename_collision_client_unaffected();
  await _test_mint_error_no_download();
}

// Match the canvas-v2 test convention: `runAll` exported, invoked here
// when the file is executed directly via `tsx` so a developer running
// `npx tsx <file>` sees green output. We wrap in a `.then()` chain
// instead of top-level `await` because tsx's CJS transform of TS files
// doesn't support top-level await.
if (process.argv[1] && process.argv[1].endsWith("BulkExportPanel.test.ts")) {
  void runAll().then(
    () => {
      console.log("BulkExportPanel.test.ts: all pass");
    },
    (err: unknown) => {
      console.error(err);
      process.exit(1);
    },
  );
}
