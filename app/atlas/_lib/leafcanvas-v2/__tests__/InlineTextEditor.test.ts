/**
 * InlineTextEditor.test.ts — U8.
 *
 * Covers the save-state machine + commit semantics of the inline text
 * editor. We don't render the React tree (no jsdom is wired in this
 * repo); instead we extract the commit logic via a lightweight harness
 * that mirrors what the component does internally:
 *   - debounced PUT with `expected_revision` carrying the local rev,
 *   - chained edits bump the local rev so they don't 409 themselves,
 *   - blur with no change → no PUT,
 *   - 409 → state flips to `conflict` and surfaces current_revision +
 *     current_value to the host store.
 *
 * Strict TS — no `// @ts-nocheck`. Same shape as the other canvas-v2
 * tests: each `_test_*` is independent + throws; `runAll` is umbrella.
 */

import {
  type TextOverride,
  type TextOverridePutBody,
  type TextOverridePutResult,
} from "../../../../../lib/projects/client";
import {
  type InlineTextEditorSaveState,
} from "../InlineTextEditor";

function assert(cond: unknown, msg: string): void {
  if (!cond) throw new Error(`assertion failed: ${msg}`);
}

// ─── Test harness ────────────────────────────────────────────────────────────

/**
 * A minimal port of the InlineTextEditor's save loop. Independent of
 * React so we can step through state transitions deterministically. The
 * production component contains the same state machine, just wrapped in
 * useCallback / useRef.
 */
class Harness {
  state: InlineTextEditorSaveState = "idle";
  revision: number;
  lastSent: string;
  saved: TextOverride[] = [];
  conflicts: { current_revision: number; current_value: string }[] = [];
  putCalls: { body: TextOverridePutBody }[] = [];

  constructor(
    private readonly originalText: string,
    private readonly canonicalPath: string,
    private readonly slug: string,
    private readonly screenID: string,
    private readonly figmaNodeID: string,
    initialRevision: number,
    private readonly putFn: (
      slug: string,
      screenID: string,
      figmaNodeID: string,
      body: TextOverridePutBody,
    ) => Promise<TextOverridePutResult>,
  ) {
    this.revision = initialRevision;
    this.lastSent = originalText;
  }

  async commit(value: string): Promise<void> {
    if (value === this.lastSent) return; // no change → no PUT
    this.lastSent = value;
    this.state = "saving";
    const body: TextOverridePutBody = {
      value,
      expected_revision: this.revision,
      canonical_path: this.canonicalPath,
      last_seen_original_text: this.originalText,
    };
    this.putCalls.push({ body });
    const res = await this.putFn(this.slug, this.screenID, this.figmaNodeID, body);
    if (res.ok) {
      this.revision = res.data.revision;
      this.state = "saved";
      this.saved.push({
        id: "",
        screen_id: this.screenID,
        figma_node_id: this.figmaNodeID,
        canonical_path: this.canonicalPath,
        last_seen_original_text: this.originalText,
        value,
        revision: res.data.revision,
        status: "active",
        updated_by_user_id: "",
        updated_at: res.data.updated_at,
      });
    } else if (res.status === 409 && "conflict" in res) {
      this.revision = res.conflict.current_revision;
      this.state = "conflict";
      this.conflicts.push({
        current_revision: res.conflict.current_revision,
        current_value: res.conflict.current_value,
      });
    } else {
      this.state = "error";
    }
  }
}

// ─── Stub PUT implementations ────────────────────────────────────────────────

function makeOkPut(initialRevision: number) {
  let rev = initialRevision;
  return {
    fn: async (
      _slug: string,
      _screenID: string,
      _figmaNodeID: string,
      _body: TextOverridePutBody,
    ): Promise<TextOverridePutResult> => {
      rev += 1;
      return {
        ok: true,
        data: { revision: rev, updated_at: "2026-05-05T00:00:00Z" },
      };
    },
    revRef: () => rev,
  };
}

function makeConflictPut(currentRevision: number, currentValue: string) {
  return async (
    _slug: string,
    _screenID: string,
    _figmaNodeID: string,
    _body: TextOverridePutBody,
  ): Promise<TextOverridePutResult> => {
    return {
      ok: false,
      status: 409,
      conflict: {
        current_revision: currentRevision,
        current_value: currentValue,
      },
    };
  };
}

function makeErrorPut(status: number) {
  return async (
    _slug: string,
    _screenID: string,
    _figmaNodeID: string,
    _body: TextOverridePutBody,
  ): Promise<TextOverridePutResult> => {
    return { ok: false, status, error: `HTTP ${status}` };
  };
}

// ─── Happy path: type "Buy" → "Buy now"; PUT fires; state flashes ───────────

async function _test_happy_path_single_edit(): Promise<void> {
  const ok = makeOkPut(0);
  const h = new Harness("Buy", "Frame/Header/Title", "p1", "s1", "n1", 0, ok.fn);
  await h.commit("Buy now");
  assert(h.state === "saved", `state: ${h.state}`);
  assert(h.putCalls.length === 1, `put called once`);
  assert(h.putCalls[0].body.value === "Buy now", "body carries new value");
  assert(h.putCalls[0].body.expected_revision === 0, "rev=0 sent");
  assert(h.putCalls[0].body.last_seen_original_text === "Buy", "original sent");
  assert(h.saved.length === 1 && h.saved[0].value === "Buy now", "saved row");
  assert(h.revision === 1, `local rev bumped: ${h.revision}`);
}

// ─── Edge: blur with no change → no PUT ─────────────────────────────────────

async function _test_no_change_no_put(): Promise<void> {
  const ok = makeOkPut(3);
  const h = new Harness("Buy", "p", "s", "sid", "fid", 3, ok.fn);
  await h.commit("Buy");
  assert(h.putCalls.length === 0, "no PUT on identical value");
  assert(h.state === "idle", `state stays idle: ${h.state}`);
}

// ─── Chained edits: rev bumps so subsequent edits don't self-409 ────────────

async function _test_chained_edits_bump_revision(): Promise<void> {
  const ok = makeOkPut(0);
  const h = new Harness("Buy", "p", "s", "sid", "fid", 0, ok.fn);
  await h.commit("Buy 1");
  await h.commit("Buy 2");
  await h.commit("Buy 3");
  assert(h.putCalls.length === 3, `three PUTs: ${h.putCalls.length}`);
  assert(h.putCalls[0].body.expected_revision === 0, "first rev=0");
  assert(h.putCalls[1].body.expected_revision === 1, "second rev=1");
  assert(h.putCalls[2].body.expected_revision === 2, "third rev=2");
  assert(h.revision === 3, `final rev: ${h.revision}`);
}

// ─── 409 → conflict state surfaces server's current_revision/value ─────────

async function _test_conflict_409_state_and_payload(): Promise<void> {
  const conflict = makeConflictPut(7, "Server text");
  const h = new Harness("Buy", "p", "s", "sid", "fid", 0, conflict);
  await h.commit("Buy now");
  assert(h.state === "conflict", `state: ${h.state}`);
  assert(h.conflicts.length === 1, "conflict captured");
  assert(h.conflicts[0].current_revision === 7, "current_revision propagated");
  assert(h.conflicts[0].current_value === "Server text", "current_value propagated");
  // After 409 the local rev tracks server's so a subsequent edit doesn't
  // double-409 (last-write-wins per U8 plan).
  assert(h.revision === 7, `local rev tracks server: ${h.revision}`);
}

// ─── Error path: 5xx → state=error, no save row ────────────────────────────

async function _test_error_path_500(): Promise<void> {
  const err = makeErrorPut(500);
  const h = new Harness("Buy", "p", "s", "sid", "fid", 0, err);
  await h.commit("Buy now");
  assert(h.state === "error", `state: ${h.state}`);
  assert(h.saved.length === 0, "no saved rows on error");
  assert(h.conflicts.length === 0, "no conflicts on plain error");
}

// ─── Edge: post-409 retry with bumped rev succeeds ─────────────────────────

async function _test_retry_after_conflict(): Promise<void> {
  // First call → 409 with current_revision=4. Second call → 200 OK.
  let calls = 0;
  const fn = async (
    _slug: string,
    _screenID: string,
    _figmaNodeID: string,
    _body: TextOverridePutBody,
  ): Promise<TextOverridePutResult> => {
    calls += 1;
    if (calls === 1) {
      return {
        ok: false,
        status: 409,
        conflict: { current_revision: 4, current_value: "stale" },
      };
    }
    return { ok: true, data: { revision: 5, updated_at: "now" } };
  };
  const h = new Harness("Buy", "p", "s", "sid", "fid", 0, fn);
  await h.commit("v1");
  assert(h.state === "conflict", "first commit conflicted");
  // Local rev now points at server's. Retry the same edit (different
  // value to avoid the no-change short-circuit) — should pass.
  await h.commit("v2");
  assert(h.state === "saved", `retry state: ${h.state}`);
  assert(h.putCalls[1].body.expected_revision === 4, "retry uses bumped rev");
  assert(h.revision === 5, `final rev: ${h.revision}`);
}

// ─── PUT body shape carries canonical_path + last_seen_original_text ───────

async function _test_put_body_shape(): Promise<void> {
  const ok = makeOkPut(0);
  const h = new Harness(
    "Original Text",
    "Frame/Section/Label",
    "myslug",
    "screen-1",
    "figma-1",
    2,
    ok.fn,
  );
  await h.commit("New Text");
  const body = h.putCalls[0].body;
  assert(body.value === "New Text", "value");
  assert(body.expected_revision === 2, "expected_revision");
  assert(body.canonical_path === "Frame/Section/Label", "canonical_path passed through");
  assert(body.last_seen_original_text === "Original Text", "last_seen_original_text passed through");
}

export async function runAll(): Promise<void> {
  await _test_happy_path_single_edit();
  await _test_no_change_no_put();
  await _test_chained_edits_bump_revision();
  await _test_conflict_409_state_and_payload();
  await _test_error_path_500();
  await _test_retry_after_conflict();
  await _test_put_body_shape();
}
