# INDmoney Design-System MCP — Server Constitution v2

You are connected to the INDmoney design-system MCP server (`ds-service`). This document is the session primer — read once at handshake, refer back when in doubt. Anchor every tool call to it; do not improvise grammar, lifecycle, or schema rules.

## 1. Slug grammar (KTD-6)

Every artifact in this server is keyed by a universal slug:

- One segment: `^[a-z0-9][a-z0-9-]*$`, max 80 chars per segment, no consecutive dashes.
- `sub_product/sub_flow` — two segments, slash-joined (e.g. `indstocks/watchlist-screener`).
- `sub_product/sub_flow/state` — three segments (e.g. `indstocks/watchlist-screener/empty`).

Counterexamples: `INDstocks/watchlist` (uppercase), `--screener` (leading dash), `watchlist screener` (space), `wl/scr/` (trailing slash).

When in doubt, call `resolve` with the raw slug and let the server canonicalize it before authoring.

## 2. The four-table data graph

```
sub_product ── 1 ──┐
                   └─→ sub_flow ── 1 ──┐
                                       ├─→ prd ── 1 ──┬─→ prd_tab ── many ──→ prd_state
                                       │              ├─→ prd_event
                                       │              ├─→ prd_criterion
                                       │              ├─→ prd_copy_string
                                       │              └─→ prd_state_*  (typed-stem rows)
                                       │
                                       ├─→ flow_drd  (1 — BlockNote ydoc)
                                       │     └─→ drd_anchor  (many — block-id → prototype anchors)
                                       │
                                       └─→ projects ─→ project_versions ─→ flows
                                                                            (the Figma autosync graph)
```

Every read/write resolves a slug to a row in this graph. The MCP layer never bypasses tenant scoping — the JWT determines which sub_products you can see.

## 3. Lifecycle states

A `sub_flow` lives in one of four states (computed, not stored):

- `empty` — no DRD, no prototype, no Figma frames.
- `proto-only` — prototype URL attached, no DRD content yet.
- `proto-wip` — DRD has BlockNote content but no design-shipped flows yet.
- `design-shipped` — at least one `flows` row with a non-empty Figma frame set.

Transitions:
- `drd.append` / `drd.attach_prototype` can move `empty → proto-only → proto-wip`.
- Figma autosync (out-of-band) moves `proto-wip → design-shipped`.
- `*.detach_*` tools can move backwards but never delete the underlying sub_flow.

## 4. PRD typed-stems model

A PRD is NOT prose. It is a directed set of typed rows:

- `prd_state` — visual + behavioral states a UI surface enters (e.g. `loading`, `empty`, `error`).
- `prd_event` — user or system events that trigger transitions or analytics.
- `prd_criterion` — acceptance criteria (1-line, testable).
- `prd_copy_string` — exact strings shown to the user, keyed for i18n.
- `prd_tab` — grouping by tab/screen.
- `prd_state_a11y_note` — accessibility annotation pinned to a state (screen-reader text, focus order, contrast).
- `prd_state_edge_case` — boundary scenario the state must handle (timeout, zero-data, partial-load).
- `frame_tag` — binds a `prd_state` to a Figma frame node (`figma_node_id`, optional `variant`).

Each row carries its own ID, type, and (where relevant) a `frame_id` linking back to a Figma frame. Tools like `prd.add_state` and `prd.add_event` MUST be preferred over freeform `prd.author` payloads — typed rows are queryable; prose is not.

## 5. Anchor model

DRD content is BlockNote JSON. Prototype clicks land on `drd_anchor` rows, each mapping `(prototype_node_id, block_id)`. The prototype HTML carries `data-anchor-id` attributes; the DRD blocks carry stable `id` fields.

Contract: when a PM renames or reorders blocks, anchors must be reseeded (`drd.attach_anchor` with the new block IDs). Stale anchors fall through to a heuristic scroll-to-section bridge — degraded but never broken.

## 6. Error catalogue

| Sentinel | What it means | What to do |
|---|---|---|
| `tool_not_found` | Unknown tool name | Check spelling; call `tools/list` to see the catalogue. |
| `invalid_args` | JSON did not match `inputSchema` | Re-read the tool's `inputSchema`; coerce types. |
| `not_implemented` | Deep tool reserved for a follow-up unit | Wait or pick a sibling tool. |
| `not_found` | Slug or row missing in this tenant | Confirm tenant scope; call `resolve` to verify slug. |
| `invalid_input` | Slug grammar or prototype URL rejected | Re-read §1 (slug grammar) and try again. |

Tool-level errors return `isError: true` in the response envelope, NOT a JSON-RPC error — protocol errors are reserved for malformed JSON-RPC frames.

## 7. Common workflows

**Publish a sub_flow:**
1. `section.inspect` → confirm slug resolves and current lifecycle state.
2. `drd.append` → seed BlockNote content (or skip if already present).
3. `drd.attach_prototype` → set the prototype URL.
4. `drd.attach_anchor` (×N) → bind prototype anchors to DRD block IDs.

**Author a PRD:**
1. `prd.get` → see existing typed rows (do not overwrite blindly).
2. `prd.add_state` / `prd.add_event` / `prd.add_acceptance_criterion` / `prd.upsert_copy_string` — one row at a time, idempotent on `(prd_id, key)`.
3. `prd.export` → final wire-shape for downstream consumers (Atlas, ind-prd skill).

**Parse a DRD as a designer:**
1. `section.inspect` → grab the sub_flow + DRD + frames in one read.
2. `drd.read` → fetch BlockNote JSON.
3. `drd.list_anchors` → see which prototype clicks already wire to blocks.

Always prefer composite tools (`section.inspect`, `resolve`) for context-gathering, and granular `prd.*` / `drd.*` tools for mutations.
