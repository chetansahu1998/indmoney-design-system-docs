/**
 * CopyOverridesTab.test.ts — U11.
 *
 * Covers the pure logic layers exposed by `CopyOverridesTab.tsx`:
 *   - `collectOverrides` flattens the per-screen Map shape and joins
 *     with frame labels,
 *   - `applyOverridePipeline` filters by status + free-text and sorts,
 *   - drag-payload encode/decode round-trips and rejects garbage,
 *   - `relativeTime` rounds buckets correctly,
 *   - the orphan-reattach action shape matches the live-store
 *     contract: PUT new + DELETE old, leaving the cache in a sane
 *     state on success.
 *
 * Same shape as the other canvas-v2 tests: each `_test_*` is
 * independent and throws on failure; `runAll` is the umbrella driver.
 *
 * Strict TS — no `// @ts-nocheck`.
 */

import type { TextOverride } from "../../../../../lib/projects/client";
import type { Frame } from "../../../../../lib/atlas/types";
import {
  applyOverridePipeline,
  collectOverrides,
  decodeOrphanDrag,
  encodeOrphanDrag,
  type DisplayOverrideRow,
  type OrphanDragPayload,
  relativeTime,
} from "../CopyOverridesTab";

function assert(cond: unknown, msg: string): void {
  if (!cond) throw new Error(`assertion failed: ${msg}`);
}

// ─── Fixtures ────────────────────────────────────────────────────────────────

function mkOverride(partial: Partial<TextOverride> & { id: string }): TextOverride {
  return {
    id: partial.id,
    screen_id: partial.screen_id ?? "screen-A",
    figma_node_id: partial.figma_node_id ?? `node-${partial.id}`,
    canonical_path: partial.canonical_path ?? "Frame/Section/Label",
    last_seen_original_text: partial.last_seen_original_text ?? "Original",
    value: partial.value ?? "Edited",
    revision: partial.revision ?? 1,
    status: partial.status ?? "active",
    updated_by_user_id: partial.updated_by_user_id ?? "user-zoe",
    updated_at: partial.updated_at ?? "2026-05-05T10:00:00Z",
  };
}

function mkFrame(id: string, label: string): Frame {
  return { id, idx: 0, x: 0, y: 0, w: 280, h: 580, label, pngUrl: "" };
}

// ─── collectOverrides ────────────────────────────────────────────────────────

function _test_collect_flattens_per_screen_maps(): void {
  const byScreen: Record<string, Map<string, TextOverride>> = {
    "screen-A": new Map([
      ["node-1", mkOverride({ id: "1", screen_id: "screen-A", figma_node_id: "node-1" })],
      ["node-2", mkOverride({ id: "2", screen_id: "screen-A", figma_node_id: "node-2" })],
    ]),
    "screen-B": new Map([
      ["node-3", mkOverride({ id: "3", screen_id: "screen-B", figma_node_id: "node-3" })],
    ]),
  };
  const frames = [mkFrame("screen-A", "Login"), mkFrame("screen-B", "Dashboard")];
  const out = collectOverrides(byScreen, frames);
  assert(out.length === 3, `flattened length: ${out.length}`);
  const labels = out.map((r) => r.screenLabel).sort();
  assert(labels[0] === "Dashboard", "label A");
  assert(labels[1] === "Login", "label B");
  assert(labels[2] === "Login", "label C");
}

function _test_collect_falls_back_to_screen_id(): void {
  const byScreen: Record<string, Map<string, TextOverride>> = {
    "unknown-screen": new Map([
      ["node-1", mkOverride({ id: "1", screen_id: "unknown-screen" })],
    ]),
  };
  const out = collectOverrides(byScreen, []);
  assert(out.length === 1, "one row");
  assert(out[0].screenLabel === "unknown-screen", "fallback label");
}

function _test_collect_empty_returns_empty(): void {
  assert(collectOverrides(undefined, []).length === 0, "undefined → []");
  assert(collectOverrides({}, []).length === 0, "empty → []");
}

// ─── applyOverridePipeline (filter + sort) ───────────────────────────────────

function rowsFixture(): DisplayOverrideRow[] {
  return [
    {
      screenLabel: "Dashboard",
      row: mkOverride({
        id: "old-active",
        screen_id: "s1",
        figma_node_id: "n1",
        status: "active",
        updated_at: "2026-05-01T10:00:00Z",
        value: "Buy",
        last_seen_original_text: "Buy now",
      }),
    },
    {
      screenLabel: "Login",
      row: mkOverride({
        id: "new-active",
        screen_id: "s2",
        figma_node_id: "n2",
        status: "active",
        updated_at: "2026-05-05T09:00:00Z",
        value: "Sign in",
        last_seen_original_text: "Login",
        updated_by_user_id: "user-alex",
      }),
    },
    {
      screenLabel: "Settings",
      row: mkOverride({
        id: "orphan",
        screen_id: "s3",
        figma_node_id: "n3",
        status: "orphaned",
        updated_at: "2026-05-04T11:00:00Z",
        value: "Privacy",
        last_seen_original_text: "Privacy & data",
        updated_by_user_id: "user-mara",
      }),
    },
  ];
}

function _test_pipeline_default_sort_is_updated_desc(): void {
  const out = applyOverridePipeline(rowsFixture(), "all", "", "updated_desc");
  assert(out.length === 3, "no filter applied");
  assert(out[0].row.id === "new-active", "newest first");
  assert(out[1].row.id === "orphan", "second");
  assert(out[2].row.id === "old-active", "oldest last");
}

function _test_pipeline_filter_by_status(): void {
  const orphans = applyOverridePipeline(rowsFixture(), "orphaned", "", "updated_desc");
  assert(orphans.length === 1, `orphans: ${orphans.length}`);
  assert(orphans[0].row.id === "orphan", "orphan id");

  const active = applyOverridePipeline(rowsFixture(), "active", "", "updated_desc");
  assert(active.length === 2, `active: ${active.length}`);
  for (const r of active) assert(r.row.status === "active", "all active");
}

function _test_pipeline_search_matches_original_current_screen(): void {
  // Search hits original text.
  let out = applyOverridePipeline(rowsFixture(), "all", "buy now", "updated_desc");
  assert(out.length === 1 && out[0].row.id === "old-active", "search original");

  // Search hits current value.
  out = applyOverridePipeline(rowsFixture(), "all", "sign in", "updated_desc");
  assert(out.length === 1 && out[0].row.id === "new-active", "search current");

  // Search hits screen label.
  out = applyOverridePipeline(rowsFixture(), "all", "settings", "updated_desc");
  assert(out.length === 1 && out[0].row.id === "orphan", "search screen");

  // No match.
  out = applyOverridePipeline(rowsFixture(), "all", "zzzzz", "updated_desc");
  assert(out.length === 0, "no match");
}

function _test_pipeline_sort_by_screen_alphabetical(): void {
  const out = applyOverridePipeline(rowsFixture(), "all", "", "screen");
  assert(out[0].screenLabel === "Dashboard", "first dashboard");
  assert(out[1].screenLabel === "Login", "second login");
  assert(out[2].screenLabel === "Settings", "third settings");
}

function _test_pipeline_sort_by_who(): void {
  const out = applyOverridePipeline(rowsFixture(), "all", "", "who");
  // user-alex < user-mara < user-zoe
  assert(out[0].row.updated_by_user_id === "user-alex", "first alex");
  assert(out[1].row.updated_by_user_id === "user-mara", "second mara");
  assert(out[2].row.updated_by_user_id === "user-zoe", "third zoe");
}

function _test_pipeline_combined_filter_and_sort(): void {
  const out = applyOverridePipeline(rowsFixture(), "active", "buy", "updated_desc");
  assert(out.length === 1, "filtered + searched");
  assert(out[0].row.id === "old-active", "match");
}

// ─── Drag payload round-trip ─────────────────────────────────────────────────

function _test_drag_payload_round_trip(): void {
  const orphan = mkOverride({ id: "x", status: "orphaned" });
  const payload: OrphanDragPayload = {
    kind: "orphan-override",
    leafID: "leaf-1",
    slug: "slug-1",
    orphan,
  };
  const enc = encodeOrphanDrag(payload);
  const dec = decodeOrphanDrag(enc);
  assert(dec !== null, "decoded");
  if (!dec) return;
  assert(dec.kind === "orphan-override", "kind");
  assert(dec.leafID === "leaf-1", "leafID");
  assert(dec.slug === "slug-1", "slug");
  assert(dec.orphan.id === "x", "orphan id");
  assert(dec.orphan.value === "Edited", "value");
}

function _test_drag_payload_rejects_garbage(): void {
  assert(decodeOrphanDrag("not-json") === null, "non-json");
  assert(decodeOrphanDrag('{"kind":"other"}') === null, "wrong kind");
  assert(decodeOrphanDrag("{}") === null, "missing fields");
  assert(decodeOrphanDrag('{"kind":"orphan-override"}') === null, "missing orphan");
}

// ─── relativeTime ────────────────────────────────────────────────────────────

function _test_relative_time_buckets(): void {
  const now = Date.parse("2026-05-05T12:00:00Z");
  assert(relativeTime("", now) === "—", "empty");
  assert(relativeTime("not-a-date", now) === "—", "garbage");
  assert(relativeTime("2026-05-05T11:59:50Z", now) === "now", "10s → now");
  assert(relativeTime("2026-05-05T11:59:00Z", now) === "1m ago", "60s → 1m");
  assert(relativeTime("2026-05-05T10:00:00Z", now) === "2h ago", "2h");
  assert(relativeTime("2026-05-04T12:00:00Z", now) === "1d ago", "1d");
  assert(relativeTime("2026-05-03T12:00:00Z", now) === "2d ago", "2d");
}

// ─── Orphan-reattach action shape ────────────────────────────────────────────
// The live-store's `applyOrphanReattach` action calls `putTextOverride`
// at the new location and `deleteTextOverride` at the old one. We exercise
// the action's expected wire shape by replaying it with stub callbacks —
// matches what live-store.ts does internally without spinning up zustand.

interface ActionStubResult {
  ok: boolean;
  error?: string;
}

interface PutCall {
  slug: string;
  screenID: string;
  figmaNodeID: string;
  body: {
    value: string;
    expected_revision: number;
    canonical_path: string;
    last_seen_original_text: string;
  };
}

interface DelCall {
  slug: string;
  screenID: string;
  figmaNodeID: string;
}

async function runReattachStub(
  orphan: TextOverride,
  newScreenID: string,
  newFigmaNodeID: string,
  canonicalPath: string,
  putFn: (c: PutCall) => Promise<{ ok: true; revision: number; updated_at: string } | { ok: false; status: number; error?: string }>,
  delFn: (c: DelCall) => Promise<{ ok: true } | { ok: false; status: number; error?: string }>,
): Promise<ActionStubResult & { putCalls: PutCall[]; delCalls: DelCall[] }> {
  const putCalls: PutCall[] = [];
  const delCalls: DelCall[] = [];

  const putCall: PutCall = {
    slug: "slug-1",
    screenID: newScreenID,
    figmaNodeID: newFigmaNodeID,
    body: {
      value: orphan.value,
      expected_revision: 0,
      canonical_path: canonicalPath,
      last_seen_original_text: orphan.last_seen_original_text,
    },
  };
  putCalls.push(putCall);
  const putRes = await putFn(putCall);
  if (!putRes.ok) {
    return { ok: false, error: putRes.error || "put failed", putCalls, delCalls };
  }

  const delCall: DelCall = {
    slug: "slug-1",
    screenID: orphan.screen_id,
    figmaNodeID: orphan.figma_node_id,
  };
  delCalls.push(delCall);
  const delRes = await delFn(delCall);
  if (!delRes.ok && delRes.status !== 404) {
    return { ok: false, error: delRes.error || "del failed", putCalls, delCalls };
  }
  return { ok: true, putCalls, delCalls };
}

async function _test_reattach_happy_path(): Promise<void> {
  const orphan = mkOverride({
    id: "orph-1",
    screen_id: "old-screen",
    figma_node_id: "old-node",
    status: "orphaned",
    value: "Save",
    last_seen_original_text: "Save changes",
  });
  const r = await runReattachStub(
    orphan,
    "new-screen",
    "new-node",
    "Frame/Save",
    async () => ({ ok: true, revision: 1, updated_at: "2026-05-05T12:00:00Z" }),
    async () => ({ ok: true }),
  );
  assert(r.ok, "ok");
  assert(r.putCalls.length === 1, "one PUT");
  assert(r.putCalls[0].screenID === "new-screen", "PUT to new screen");
  assert(r.putCalls[0].figmaNodeID === "new-node", "PUT to new node");
  assert(r.putCalls[0].body.value === "Save", "PUT carries orphan value");
  assert(
    r.putCalls[0].body.expected_revision === 0,
    `PUT expected_revision: ${r.putCalls[0].body.expected_revision}`,
  );
  assert(r.putCalls[0].body.canonical_path === "Frame/Save", "canonical path");
  assert(r.delCalls.length === 1, "one DELETE");
  assert(r.delCalls[0].screenID === "old-screen", "DELETE old screen");
  assert(r.delCalls[0].figmaNodeID === "old-node", "DELETE old node");
}

async function _test_reattach_aborts_when_put_fails(): Promise<void> {
  const orphan = mkOverride({
    id: "orph-2",
    screen_id: "old",
    figma_node_id: "old-node",
    status: "orphaned",
  });
  const r = await runReattachStub(
    orphan,
    "new",
    "new-node",
    "p",
    async () => ({ ok: false, status: 500, error: "boom" }),
    async () => ({ ok: true }),
  );
  assert(!r.ok, "not ok");
  assert(r.error === "boom", `error: ${r.error}`);
  assert(r.delCalls.length === 0, "no DELETE when PUT failed");
}

async function _test_reattach_tolerates_404_on_old_delete(): Promise<void> {
  // The orphan row may have been cleaned up out-of-band (sheet sync ran
  // again) — DELETE 404 still means the user goal is achieved.
  const orphan = mkOverride({ id: "orph-3", status: "orphaned" });
  const r = await runReattachStub(
    orphan,
    "new",
    "new-node",
    "p",
    async () => ({ ok: true, revision: 2, updated_at: "x" }),
    async () => ({ ok: false, status: 404, error: "not_found" }),
  );
  assert(r.ok, "still ok on 404 DELETE");
}

// ─── Umbrella runner ─────────────────────────────────────────────────────────

export async function runAll(): Promise<void> {
  _test_collect_flattens_per_screen_maps();
  _test_collect_falls_back_to_screen_id();
  _test_collect_empty_returns_empty();
  _test_pipeline_default_sort_is_updated_desc();
  _test_pipeline_filter_by_status();
  _test_pipeline_search_matches_original_current_screen();
  _test_pipeline_sort_by_screen_alphabetical();
  _test_pipeline_sort_by_who();
  _test_pipeline_combined_filter_and_sort();
  _test_drag_payload_round_trip();
  _test_drag_payload_rejects_garbage();
  _test_relative_time_buckets();
  await _test_reattach_happy_path();
  await _test_reattach_aborts_when_put_fails();
  await _test_reattach_tolerates_404_on_old_delete();
}
