---
title: "feat: Google Sheet → ds-service sync pipeline"
type: feat
status: active
date: 2026-05-05
deepened: 2026-05-05
---

# feat: Google Sheet → ds-service sync pipeline

## Overview

Replace ad-hoc Figma plugin exports with an automatic sync from the team's working Google Sheet (`Design <> InD`, id `1sS-fBxhRnGBDU5ISZJEfpZ7-ZxExNh-YUF-izh5yYE0`) into ds-service `projects` / `flows` / `screens`. The atlas brain at `/atlas` repopulates without anyone clicking "Send to Projects" in the plugin — the sheet is the source of truth, the sync polls it on a 5-minute cron, and changes default-accept.

This unblocks visualizing every active design effort across the org (~20 sub-sheet products, ~700 sheet rows) without forcing each designer through the plugin export step.

---

## Deepening Findings (2026-05-05) — Real Sheet Inspection

After writing the v1 plan we pulled the entire production sheet via SA + tried fetching 3 Figma sections + 1 Confluence DRD + 1 Google Doc DRD. Replaces every estimate in the original plan with actual numbers.

### Real volumes

| Metric | Original estimate | Actual |
|---|---|---|
| Total non-blank rows (after skip set) | ~876 | **444** |
| Importable rows (valid Figma URL with node-id) | ~353 (assumed = total minus a few) | **353** ✓ |
| Empty Figma URL → ghost flow | unknown | **84 (19% of brain!)** |
| Malformed URL | unknown | **7** |
| Canvas-only URL | unknown | **0** (designers are disciplined) |
| Distinct Figma file_ids | ~30 estimated | **43** |
| Importable tabs (after skipping Product Design / Lotties / Illustrations) | 21 | **21** ✓ |

### Top Figma files by sheet-row count (matters for API quota)

| file_id | rows referencing it |
|---|---|
| `4fTtWb03uIZSESRUd1e9wC` | **45** ← heaviest |
| `zEiJVGdOKf6TeI5ahwwFCE` | 30 |
| `IspdtPgpzse0Gft6NR6Z9N` | 28 |
| `b6xbko2EQRqy08kgl0b4Pg` | 28 |
| `vwbPMm28N5uaZq5jrFBo6W` | 21 |
| `66GfoHEG2KbgvAV3HNkSXx` | 20 |
| `OMTcwfED2sEcWZFu2wBNYK` | 18 |
| `SDI9MfdEUbvr0cknLpjGjW` | 17 |

A row-index shift in a heavy file's tab triggers up to 45 Figma node-fetches in one cycle. **This drives a new design decision**: cache `(file_id, node_id) → frame list` for 1 hour to cut redundant Figma calls when row content didn't actually change. Adds ~50 LoC to U6 (in-memory LRU keyed on (file_id, node_id, version)).

### DRD link distribution (444 rows)

| Type | Count | Fetchable? |
|---|---|---|
| Empty | 387 | n/a |
| Google Doc | 22 | **YES** when shared with SA — verified 2026-05-05 against `1l8ATtw…` (Instant Exit V2). Docs API v1 works without Drive API being enabled. |
| Confluence (`finzoom.atlassian.net`) | 13 | **No** — anonymous GET returns 200 but it's a login wall HTML. Real fetch needs Atlassian PAT. |
| "No DRD" / "No DRD Shared" / "N/A" markers | 15 | n/a (treat as empty) |
| Other URL | 6 | unclear; treat as empty |
| Notion | 1 | **No** — needs Notion integration token. |

**Two-tier strategy** (replaces earlier "don't fetch anything" decision):

**Tier 1 — URL link** (always works): Store the DRD URL on `flows.external_drd_url`. Render as a clickable "📄 External DRD" button in the DRD-tab header alongside the BlockNote editor. Designer clicks → opens the source doc in a new tab. This is the universal fallback.

**Tier 2 — content fetch + preview** (Google Docs only, when accessible): When the URL parses as a Google Doc *and* the SA can read it, fetch the content via Docs API v1 and store:
- `flows.external_drd_title` — the doc's `title` field (e.g. "Instant Exit (V2): Making exiting a position even faster")
- `flows.external_drd_snippet` — first ~500 chars of plain text body, for inline preview in the inspector
- `flows.external_drd_fetched_at` — when we last pulled it

The DRD inspector then shows the title + snippet + a "Read full DRD ↗" button as well as the editable BlockNote area. If the fetch fails (doc not shared with SA), fall back to Tier 1 silently.

**Confluence + Notion stay at Tier 1 only.** Adding Atlassian PAT and Notion integration is meaningful scope; defer to a follow-up plan if the team asks.

### Validating Tier 2 — successful Doc fetch evidence

```
Doc:  https://docs.google.com/document/d/1l8ATtwqabMkVcamtm7uh3VVDZDN-vFSpHOy7ht7H-p8/edit
SA:   indmoneydocs@gen-lang-client-0386349723.iam.gserviceaccount.com
Probe results (2026-05-05):
  [1] Anonymous export                → HTTP 401 (doc is private)
  [2] Drive API v3 (drive.metadata)   → HTTP 403 (Drive API not enabled in GCP project)
  [3] Drive API v3 (drive.readonly)   → HTTP 403 (same)
  [4] Docs API v1 (documents.readonly)→ HTTP 200, 614 KB JSON ✓
  [5] Export endpoint (SA bearer)     → HTTP 200, 5,365 bytes plain text ✓

  → title: "Copy of Instant Exit (V2): Making exiting a position even faster"
  → first 200 chars of body: "Challenges with current implementation of Instant Exit:
    If I have an open order against a position, and I click instant exit on that position,
    a bottom sheet comes asking me to cancel the open order first and then only I can…"
```

Two scopes work for Tier 2: `documents.readonly` returns structured JSON (good for parsing), `drive.readonly` works for the export endpoint (simpler — returns plain text directly). U6/U8 will use the **export endpoint** path because it's smaller, simpler, and the snippet is what the inspector renders anyway.

### Operational requirement for Tier 2

For the team to get Doc previews in the inspector, each DRD Doc must be shared with the SA email. Two options:

- **Per-doc share** — designers add the SA email when creating each new DRD. Burden but precise.
- **Domain-wide share rule** — Workspace admin sets a default share rule for the team's "Design DRDs" folder so anything in there is auto-shared with the SA. One-time setup, scales forever.

Plan recommends the second; runbook entry below covers both. Until either is set up, Tier 1 (URL only) is the visible behavior — no regression vs today.

### Designer POC roster (real names from sheet vs. mint-tokens roster)

| Sheet name | Count | In `mint-tokens` roster (May 3)? |
|---|---|---|
| Sahaj | 283 | ✓ Sahaj Tyagi |
| Omeshwari | 99 | ✓ Omeshwari Dharpure |
| Saksham | 45 | ✓ Saksham Jamwal |
| Laxmi | 8 | ✓ Laxmi Mishra |
| Tarushi | 4 | **✗ Missing** |
| Chetan | 1 | ✓ Chetan Sahu |
| Devang | several (in Illustrations tab) | **✗ Missing** |

**Action**: when shipping U10's deploy, also re-mint tokens for **Tarushi** and **Devang** so Design POC mapping covers everyone. The mapping file in `cmd/sheets-sync/parse.go` adds these two with their freshly-minted user_ids.

### PM (Product POC) roster

35 distinct product POCs — not pre-mintable as users. Treat as free-text: store on `flows.product_poc_text TEXT`, no user-id mapping. Top 10:

| PM | Count |
|---|---|
| Shruti | 30 |
| Vipul | 25 |
| Mayank | 23 |
| Shubham | 20 |
| Vaibhav Beriwal | 17 |
| Yash | 17 |
| Udit | 16 |
| Chetan | 15 |
| Sourab | 13 |
| Sparsh | 12 |

Multi-author cells confirmed real: "Drishti & Ritwik", "Mayank & Sanchit", "Tejas, Aditya". Stored as-is.

### Status field (mostly empty but signal exists)

| Value | Count |
|---|---|
| Done | 26 |
| Complete | 12 |
| tbd / TBD | 3 |
| WIP | 1 |
| In dev | 1 |
| In progress | 1 |
| In review / in review | 2 |
| To be picked | 1 |

**New decision: surface Status in the brain inspector header** as a small badge ("Done" / "WIP" / "In review" / "TBD"). Designers can sort/filter by status if a "show only WIP" filter ships later. Normalize at sync time to a canonical set: `done|wip|in_review|tbd|backlog|empty`. Add `flows.sheet_status TEXT` column to the migration (U1).

### End-to-end Figma fetch validation (3 sample rows)

| Sheet row | Figma node type | Top-level screens extracted |
|---|---|---|
| Performant Trade Screen-Equity (`IspdtPgpzse0Gft6NR6Z9N` → `4483:265999`) | SECTION | 34 |
| MTF Statement Page Redesign (`5529:185426`) | SECTION | 13 |
| Multiple demat account compliance (`5529:184325`) | SECTION | 2 |

All resolve correctly. Frame walker logic from `/tmp/figma_test.py` is sound — the Go port in U6 is a mechanical translation.

### Surprises that did NOT happen

- **Cross-tab dedup hits**: 0 in this snapshot. The dedup pass (U5) is still needed as a safety net but the production sheet is already clean. No load-bearing collisions to worry about today.
- **Canvas-only URLs**: 0. Designers always include node-id. The skip path in U4 is still required for robustness.

### Rollout impact

When U10 ships and the first cycle runs, the brain at `/atlas` goes from **1 project → 21 projects** (one per non-skipped sub-sheet) with **353 sub-flow nodes** spanning ~20 product areas across 6 lobes. This is the moment the brain stops looking sparse.

---

## Problem Frame

Today the Atlas brain has 1 project (the only file someone exported via the plugin). The team's actual design pipeline lives in a Google Sheet with 24 tabs covering every product (INDstocks, Plutus, Insta Cash, Onboarding KYC, …). Each row is a design effort with a Figma URL, owner POC, designer POC, and status. The sheet is updated continuously; the brain is not.

A sync pipeline gets the brain to mirror what the team is *actually working on*. The shape we want:

- Each sub-sheet (tab name) → one project node on the brain (e.g. "INDstocks" → big white node on the Markets lobe)
- Each sheet row → one sub-flow node orbiting its parent project
- Each Figma file's section → screens inside the leaf canvas
- New rows appear automatically (default-accept)
- Removed rows soft-archive instead of disappearing silently
- The sync is cheap when nothing changed and thorough when something did

---

## Requirements Trace

- R1. Initial bulk pull: every visible tab via Sheets API v4 service-account auth (already wired — `.secrets/google-sheets-sa.json`)
- R2. Cleanup + de-duplication so the same row in `Product Design` master and `<Product>` tab does not produce two flows
- R3. Filter rules: only rows with valid Figma URL containing a `node-id`; canvas-only URLs and malformed URLs land in the sync log but produce no DB write; empty-URL rows produce a *ghost* flow (visible on brain, no leaf canvas) per prior decision
- R4. Sub-sheet → product mapping driven by `lib/atlas/taxonomy.ts` (single source of truth shared with the frontend)
- R5. Cron schedule: every 5 minutes
- R6. Drive API `modifiedTime` gate: skip the heavy pull when the sheet hasn't changed since the last sync run; only do the work when timestamp advanced
- R7. Default-accept: no admin queue, no dry-run gate in production — every sheet edit applies on the next cycle
- R8. Designer mapping: `Design POC` text column resolves to a row in the `users` table (or stays unresolved + logged when not in the roster)
- R9. Run on Fly cron, with the SA JSON as a Fly secret
- R10. State table `sheet_sync_state` keyed on `(spreadsheet_id, tab, row_index)` for hash-diff change detection
- R11. `--dry-run` mode that prints planned imports without POSTing to `/v1/projects/export`
- R12. Failure modes documented + observable via the existing `/v1/telemetry/event` pipeline

---

## Scope Boundaries

- Not building a sheet-side approval queue — every change is default-accepted (R7)
- Not building a UI to trigger sync manually — the cron is the trigger; admins can `fly machine restart` to force an immediate run
- Not building bidirectional sync — the sheet is read-only from our perspective; no writes to columns G/H ("Status" / "Last updated status")
- Not building a custom approval UI for new personas auto-discovered from the sheet — they go to the existing pending-personas queue (Phase 7 U4 already shipped)
- Not migrating the existing `indian-stocks-research` plugin-exported project — it stays as-is; sync creates new projects keyed by sub-sheet name with synthetic `file_id` values prefixed `sheet:`. The Figma file `Ql47G1l4xLYOW7V2MkK0Qx` may be referenced by a sub-sheet row separately and create a separate project; both can coexist
- Not exporting Illustrations / Lotties / Product Design tabs to the brain (Product Design = master roll-up that would double-count; Illustrations stays for a future asset surface; Lotties skipped entirely)

### Deferred to Follow-Up Work

- Designer-cursor presence on the sheet itself (we only see edit timestamps, not who's editing live)
- Backfilling `_synced_at` per-row timestamps via Apps Script `onEdit` (kept for a later PR; current plan uses sheet-level `modifiedTime` only)
- Sheet → Hocuspocus collab presence integration

---

## Context & Research

### Relevant Code and Patterns

- `services/ds-service/internal/projects/types.go` — `ExportRequest` + `FlowPayload` + `FramePayload` shapes; the sync command builds these and POSTs to `/v1/projects/export`
- `services/ds-service/internal/projects/server.go:HandleExport` — destination endpoint already used by the Figma plugin; sync reuses the same contract
- `services/ds-service/internal/figma/client/client.go` — Figma REST API client; reuse for `GET /v1/files/{file_id}/nodes?ids=...` calls
- `services/ds-service/internal/projects/modepairs.go` — `DetectModePairs` helper, applied per group (mirrors plugin behaviour)
- `services/ds-service/cmd/extractor/` — existing cmd-style Go binary; sync follows the same package layout (one cmd dir with focused files)
- `services/ds-service/cmd/audit-server/` — runs as a separate Fly app today, same deploy pattern works for `cmd/sheets-sync/`
- `lib/atlas/taxonomy.ts` (`subSheetToLobe`, `LEGACY_PRODUCT_PATTERNS`, `productToDomain`) — UI side of the same mapping; sync command has its own Go mirror because the JS map cannot be imported into Go
- `services/ds-service/migrations/` — next available slot is `0017_sheet_sync_state.up.sql`
- `lib/telemetry.ts` + `services/ds-service/internal/projects/telemetry.go` — sync command uses the same `/v1/telemetry/event` endpoint to emit observability events
- `figma-plugin/code.ts:280–330` — reference implementation of the projects.send POST + audit-server proxy hop; the sync command bypasses this and POSTs directly to ds-service since it runs server-side

### Institutional Learnings

- **Figma render timeouts are real** (audit log captured one already on May 1). Pipeline retry with smaller batch + `?scale=1` should be planned, not assumed away.
- **Fly Dockerfile gotcha**: the `Dockerfile` lives at repo root, not in `services/ds-service/`. Deploy must be run from repo root with `fly deploy --config fly.sheets-sync.toml --remote-only`.
- **JSON `null` vs `[]` from Go**: ds-service serializes nil slices as `null`. Sync command's response readers must coerce with `?? []` everywhere — same lesson the atlas adapter learned.
- **Unique key constraint on flows**: `(tenant_id, file_id, section_id, persona_id) WHERE deleted_at IS NULL`. Two sheet rows pointing at the same `(file_id, node_id, persona)` would collide — dedupe must run *before* the export call, not after, or we get unique-violation 500s.
- **ENCRYPTION_KEY rotation**: the SA JSON is *not* tied to it (different secret), so a key rotation does not break the sync. Logged here so the runbook is correct.

### External References

- [Sheets API v4 spreadsheets.values.batchGet](https://developers.google.com/sheets/api/reference/rest/v4/spreadsheets.values/batchGet) — primary read path
- [Drive API v3 files.get with fields=modifiedTime](https://developers.google.com/drive/api/v3/reference/files/get) — the cheap probe for R6
- [Fly machine schedule directive](https://fly.io/docs/launch/cron/) — runs a machine on cron (we set `schedule = "*/5"` in fly.toml)
- [Figma REST API GET /v1/files/{key}/nodes](https://www.figma.com/developers/api#get-file-nodes-endpoint) — used to walk a section node-id and list its top-level frame children
- [google.golang.org/api/sheets/v4](https://pkg.go.dev/google.golang.org/api/sheets/v4) — Go SDK we add as a new dependency

---

## Key Technical Decisions

- **Separate Fly app `indmoney-sheets-sync`, not embedded in ds-service** — cleaner failure isolation, separate logs (`fly logs -a indmoney-sheets-sync`), independent restart, can swap to a beefier machine if Figma fan-out grows. Trade-off: one more Fly machine on the bill (~$2/mo) and one more deploy target. Worth it for the ops clarity.
- **Sub-sheet name IS the project**, with synthetic `file_id = sheet:<tab-name>` (sluggified) — preserves the existing flows-table FK shape while letting one project hold flows from many real Figma files. Avoids an `aggregate_projects` join table.
- **Dedup truth: `(real_file_id, real_node_id)` is the flow identity** — if a row appears in `Product Design` (master) AND a product-specific tab pointing at the same `(file_id, node_id)`, the product-specific tab wins. Product Design is treated as a master roll-up the team uses for triage, not as a separate project's contents.
- **Drive `modifiedTime` is the cheap gate** — one GET per cycle costs nothing; we only do the 24-tab batch read when the timestamp advanced. Stored in `sheet_sync_runs` table (separate from `sheet_sync_state`) so cycles are auditable.
- **State key is `(spreadsheet_id, tab, row_index)`** — survives row insertion (existing rows keep their indices when new rows append at the bottom) and detects deletion via "missing in current pull". Caveat: if someone *inserts* a row in the middle of a tab, every subsequent row index shifts by one and we re-import all of them. Mitigated by the row-hash gate inside the cycle (re-import is a no-op when the hash matches a known row regardless of index).
- **Default-accept means no per-row gate** — accepted, but logged. Every applied change emits a `sheets.sync.applied` telemetry event so we can audit-trail who/what changed even though we didn't ask permission.
- **Designer mapping pre-seeded from the existing 9-token roster (`mint-tokens` cmd)** — `Design POC = "Sahaj"` resolves to the user_id of the Sahaj JWT we minted on May 3. Unknown names fall back to a default service-user_id and emit `sheets.sync.unknown_designer` telemetry. The roster lives in code, not the DB, because the JWTs are the source of truth.
- **Figma render timeouts are retried once with `scale=1` then logged** — the existing pipeline already retries internally, but we want to surface a sync-level "this row's pipeline keeps timing out" signal in telemetry so the team can manually shrink the section.
- **`Product Design` tab is fully skipped at the source** (not deduped at the destination) — the dedup pass still runs as a safety net for cross-tab collisions in the *non-skipped* set, but the master tab never enters the pipeline at all.

---

## Open Questions

### Resolved During Planning

- **Q: Should we fetch DRD content from Confluence / Google Docs / Notion?**
  **Tier 1 (URL only) for all three; Tier 2 (content + snippet) for Google Docs only.** First probe (2026-05-05 morning) hit Drive-API-not-enabled errors and concluded "no fetching anywhere"; second probe (2026-05-05 afternoon, doc `1l8ATtw…`) showed the Docs API works without Drive API + when the SA has been shared on the doc, returning full title + body. Updating: GDocs upgrade to Tier 2 with snippet preview in the inspector; Confluence + Notion stay Tier 1 (URL link only) — the auth integration cost isn't justified for 13 + 1 rows. Operational requirement: Workspace admin sets a default share rule on the team's DRD folder so new DRDs auto-share with the SA email.
- **Q: Should empty-Figma-URL rows produce ghost flows on the brain (84 of them — 19% of the brain)?**
  Yes per R3. Ghost flows are visible nodes (small, dim) but have no leaf canvas — the inspector shows the metadata + the "Pending Figma link" empty state. The 84-rows-as-ghosts is a feature, not noise: they make in-flight design work visible org-wide.
- **Q: Should sheet rows that disappear cause flow soft-deletion?**
  Yes. State table tracks `last_seen_at`; rows missing from a current pull get `flows.deleted_at = NOW()`, atlas brain hides them, audit log records the soft-delete. Recovery (someone re-adds the row): the next pull resurrects via state-key match.
- **Q: How do we handle a row that points at a Figma file already in our DB (e.g. `Ql47G1l4xLYOW7V2MkK0Qx` from the plugin export)?**
  The sync creates a flow under the sub-sheet's project; it does NOT migrate the existing `indian-stocks-research` project. Two projects coexist. Future cleanup can soft-delete the orphan plugin project.
- **Q: How do we handle multi-author Product POC cells like "Drishti & Ritwik"?**
  Store as-is in flow metadata (audit_log details), do not try to split into multiple users. Designer POC similarly.
- **Q: What if a sub-sheet's name has a typo (e.g. `Onbording KYC`, `Platfrom & global serach`)?**
  Taxonomy regex already handles these. The Go-side mirror in this plan must too.
- **Q: How do we map Design POC strings to user_ids without a user-lookup endpoint?**
  Sync command embeds a static map (designer-name → user_id) mirroring `mint-tokens` output. Updating the map requires a sync redeploy, which is fine for a 9-person roster.

### Deferred to Implementation

- Exact column letters for `Project / Product POC / Design POC / DRD link / Figma link / Proto link / Status / Last updated status` — read by name from the header row to be resilient to column reorders, not by fixed letter
- The exact schema of frames returned from Figma when a node-id resolves to a `COMPONENT_SET` (variants) — implement and test against the live Figma file at `IspdtPgpzse0Gft6NR6Z9N` to confirm the screen-size filter handles variant frames correctly
- The retry policy specifics for transient Sheets API 5xx (default exponential backoff on `googleapi.Error.Code` ≥ 500 — leave the exact threshold to implementation)

---

## Output Structure

    services/ds-service/
      cmd/
        sheets-sync/                          (new directory)
          main.go                             — CLI entry: flags, env, top-level orchestration
          sheets.go                           — Sheets API v4 client wrapper (read-only)
          drive.go                            — Drive API v3 modifiedTime probe
          parse.go                            — Figma URL parser + sub-sheet → product resolver (Go mirror of taxonomy.ts)
          dedupe.go                           — cross-tab dedup algorithm
          figma_resolve.go                    — Figma REST API → top-level screen frames per node-id
          export.go                           — POST /v1/projects/export wrapper
          state.go                            — sheet_sync_state CRUD
          telemetry.go                        — emit /v1/telemetry/event
          *_test.go                           — table-driven tests per file
        sheets-sync.go                        (no — using cmd/sheets-sync/main.go above)
      migrations/
        0017_sheet_sync_state.up.sql          (new)
    fly.sheets-sync.toml                       (new — separate Fly app config)
    Dockerfile.sheets-sync                     (new — small Go-only image, no Figma extractor)

---

## High-Level Technical Design

> *This illustrates the intended approach and is directional guidance for review, not implementation specification. The implementing agent should treat it as context, not code to reproduce.*

### Sync cycle (every 5 min)

```
┌──────────────┐
│ cron tick    │
└──────┬───────┘
       │
       ▼
┌──────────────────────────────────────────┐
│ 1. Drive modifiedTime probe              │   GET drive/v3/files/{id}?fields=modifiedTime
│    Compare against state.last_synced_at  │   ── if same: emit sheets.sync.unchanged, exit ─→
└──────┬───────────────────────────────────┘
       │ (changed)
       ▼
┌──────────────────────────────────────────┐
│ 2. Sheets batchGet — every visible tab   │   spreadsheets.values.batchGet ranges='tab!A1:Z<lastRow>'
│    Skip Product Design / Lotties /       │
│    Illustrations at the source           │
└──────┬───────────────────────────────────┘
       │
       ▼
┌──────────────────────────────────────────┐
│ 3. Per-row normalize                     │
│    parse Figma URL → (file_id, node_id)  │
│    classify URL: canvas / valid / empty  │
│    resolve sub-sheet → product + lobe    │
│    resolve Design POC → user_id          │
│    compute row_hash                      │
└──────┬───────────────────────────────────┘
       │
       ▼
┌──────────────────────────────────────────┐
│ 4. Cross-tab dedup                       │
│    bucket by (file_id, node_id)          │
│    if duplicates: keep highest-priority  │
│      tab; drop lower-priority            │
└──────┬───────────────────────────────────┘
       │
       ▼
┌──────────────────────────────────────────┐
│ 5. State diff                            │
│    NEW: row not in state                 │   → POST /v1/projects/export (full)
│    CHANGED: row_hash differs             │   → POST /v1/projects/export (full, idempotency_key bumped)
│    UNCHANGED: skip                       │   → no-op
│    GONE: row in state, missing now       │   → DELETE /v1/flows/{id}  (or PATCH with deleted_at)
└──────┬───────────────────────────────────┘
       │
       ▼
┌──────────────────────────────────────────┐
│ 6. For each NEW/CHANGED row              │
│    Figma REST: GET /v1/files/{file_id}/  │
│      nodes?ids={node_id}                 │
│    Walk to top-level FRAMEs ≥ 280×400    │
│    Build FlowPayload + POST export       │
│    Update state.last_seen_at + hash +    │
│      version_id from response            │
└──────┬───────────────────────────────────┘
       │
       ▼
┌──────────────────────────────────────────┐
│ 7. Telemetry summary event               │   sheets.sync.cycle.done
│    counts: new, changed, unchanged,      │
│    gone, errors                          │
└──────────────────────────────────────────┘
```

### Dedup algorithm (step 4 above)

Tabs have a priority order — product-specific tabs win over the master roll-up. Product Design is already filtered at step 2, so this is a safety net for *cross-product-tab* collisions (rare but possible: a row about US Stocks accidentally pasted into the Mutual Funds tab pointing at the same Figma node).

```
priority: product-specific tab (= matches taxonomy explicit rule)
        > default-fallback tab (= unknown sub-sheet → Platform lobe)
        > Product Design (filtered at step 2; never reaches dedupe)
```

Pseudo-table for the (file_id, node_id) bucket:

| (file_id, node_id) | rows in pull | resolution |
|---|---|---|
| Same in 1 tab | 1 row | keep |
| Same in 2 tabs, both explicit | 2 rows | keep both as separate flows under their respective products (legitimate — same Figma section can be a flow under two products) |
| Same in 2 tabs, one explicit one fallback | 2 rows | drop the fallback row |
| Same key, same tab, multiple rows | N rows | keep first by row_index, log warning |

Note: the "keep both" branch is intentional — two products legitimately sharing a Figma section is meaningful, not a duplicate.

---

## Implementation Units

- U1. **Migration: `sheet_sync_state` + `sheet_sync_runs`**

**Goal:** Persist per-row state for hash-diff change detection + per-cycle audit log for the modifiedTime gate.

**Requirements:** R10

**Dependencies:** None

**Files:**
- Create: `services/ds-service/migrations/0017_sheet_sync_state.up.sql`

**Approach:**
- `sheet_sync_state` PK `(spreadsheet_id, tab, row_index)` plus columns: `tenant_id`, `file_id`, `node_id`, `row_hash`, `project_id` (FK to projects.id), `flow_id` (FK to flows.id, nullable for ghost rows), `last_seen_at`, `last_imported_at`, `last_error TEXT NULL`
- `sheet_sync_runs` PK `id` plus columns: `started_at`, `finished_at`, `drive_modified_time`, `sheet_modified_time`, `result` (`unchanged` / `applied` / `failed`), `summary_json` (`{new, changed, unchanged, gone, errors}`)
- Indexes on `sheet_sync_state.file_id` and `sheet_sync_state.last_imported_at`
- All foreign keys observe the existing `tenant_id` FK pattern (Phase T7 plan, May 3)
- **Added by deepening**: `flows` table also gains six new columns to surface sheet metadata in the inspector:
  - `external_drd_url TEXT NULL` (the Confluence/GDocs/Notion URL from column D — universal Tier 1)
  - `external_drd_title TEXT NULL` (Tier 2 — Google Doc title when fetchable)
  - `external_drd_snippet TEXT NULL` (Tier 2 — first ~500 chars of doc body for inline preview)
  - `external_drd_fetched_at TEXT NULL` (Tier 2 — RFC3339 last successful fetch)
  - `product_poc_text TEXT NULL` (free-text PM names from column B, multi-author preserved)
  - `sheet_status TEXT NULL` (normalized: `done|wip|in_review|tbd|backlog`)

  All nullable so existing flow rows aren't disturbed.

**Patterns to follow:**
- `services/ds-service/migrations/0009_graph_index.up.sql` (composite PK, JSON columns)
- `services/ds-service/migrations/0015_tenant_fk_constraints.no_tx.up.sql` (tenant_id FK shape)

**Test scenarios:**
- Happy path: migrate on a fresh DB, both tables exist with correct columns + indexes
- Edge case: re-running migration is a no-op (UP is idempotent in our convention via `CREATE TABLE IF NOT EXISTS`)
- Test expectation: SQL-level — verify via `PRAGMA table_info()` in `internal/db/migrations_test.go` (existing convention)

**Verification:**
- `sqlite3 ds.db "PRAGMA table_info(sheet_sync_state);"` returns the expected columns
- `sqlite3 ds.db "PRAGMA index_list(sheet_sync_state);"` shows both indexes

---

- U2. **CLI scaffold: `cmd/sheets-sync/main.go`**

**Goal:** CLI entry point with flags + env loading + top-level orchestration of the cycle phases (steps 1–7 in the diagram).

**Requirements:** R5, R7, R11

**Dependencies:** U1

**Files:**
- Create: `services/ds-service/cmd/sheets-sync/main.go`
- Create: `services/ds-service/cmd/sheets-sync/main_test.go` *(flag parsing + env presence sanity)*

**Approach:**
- Flags: `--once` (run one cycle then exit, default), `--loop` (loop forever with 5-min sleep — used by Fly cron container that survives across cycles), `--dry-run` (no POSTs), `--tab=<name>` (single-tab debug), `--limit=<n>` (cap rows per tab for testing)
- Env: `SHEETS_SPREADSHEET_ID`, `GOOGLE_APPLICATION_CREDENTIALS` (path to SA JSON), `DS_SERVICE_URL` (POST target), `DS_SERVICE_BEARER` (super-admin JWT for the export call)
- Composes the per-cycle pipeline: probe Drive → batch read → normalize → dedupe → state diff → per-row export → telemetry summary
- Each step is a function call out to its sibling file, so main.go stays orchestration-only

**Patterns to follow:**
- `services/ds-service/cmd/digest/main.go` (cmd-style flag parsing, env from .env.local at repo root)
- `services/ds-service/cmd/extractor/main.go` (long-running mode pattern for `--loop`)

**Test scenarios:**
- Happy path: `--dry-run` with a fixture sheet response prints planned imports without HTTP calls
- Edge case: missing required env produces a clear error before any API call
- Error path: passing `--tab=NonExistent` exits 0 with "tab not found" warning, not an unhandled error
- Test expectation: black-box test against fixture JSON files in `cmd/sheets-sync/testdata/`

**Verification:**
- `go build ./cmd/sheets-sync/` succeeds
- `./sheets-sync --dry-run --once` against the live sheet exits 0 and prints the planned import set

---

- U3. **Sheets + Drive API readers**

**Goal:** Two thin wrappers around `google.golang.org/api/sheets/v4` and `drive/v3` that produce the data shapes the rest of the pipeline consumes.

**Requirements:** R1, R6

**Dependencies:** U2

**Files:**
- Create: `services/ds-service/cmd/sheets-sync/sheets.go`
- Create: `services/ds-service/cmd/sheets-sync/drive.go`
- Create: `services/ds-service/cmd/sheets-sync/sheets_test.go`
- Create: `services/ds-service/cmd/sheets-sync/drive_test.go`

**Approach:**
- `sheets.go` exports `FetchAll(ctx, spreadsheetID) (Spreadsheet, error)`. Returns `Spreadsheet { Title, TimeZone, Tabs []Tab }` where `Tab { Name, GID, Header []string, Rows [][]string }`. Uses one `spreadsheets.get` for tab list + one `batchGet` for values.
- `drive.go` exports `ProbeModifiedTime(ctx, fileID) (time.Time, error)`. Uses `files.get?fields=modifiedTime`.
- Both use `option.WithCredentialsFile(env GOOGLE_APPLICATION_CREDENTIALS)` so they pick up the SA JSON automatically.
- Hard-cap each tab read at 5000 rows × 26 cols (matches the Apps Script we're replacing).

**Patterns to follow:**
- `services/ds-service/internal/figma/client/client.go` (HTTP-with-auth wrapper shape; per-method context.Context first arg)
- Go SDK examples in [google.golang.org/api/sheets/v4 README](https://pkg.go.dev/google.golang.org/api/sheets/v4)

**Test scenarios:**
- Happy path (sheets): mock the Sheets service via `httptest.Server` returning fixture JSON; assert FetchAll returns the expected number of tabs + rows
- Happy path (drive): mock returns a fixed RFC3339 time; assert it's parsed
- Edge case: empty tab returns Tab with empty Header/Rows, not an error
- Error path: 403 (sheet not shared) returns a typed error so main.go can print a useful message
- Error path: malformed RFC3339 from Drive API doesn't crash; logs warning + returns zero time

**Verification:**
- Live smoke against the production sheet: print tab count + total row count, must match the 24 tabs / ~876 rows we already saw
- `drive.ProbeModifiedTime` returns a recent timestamp

---

- U4. **Figma URL parser + sub-sheet → product resolver (Go mirror of taxonomy.ts)**

**Goal:** Two pure-functional pieces: parse Figma URLs into `(file_id, node_id, classification)`, and map a tab name to its product + lobe. Mirrors `lib/atlas/taxonomy.ts` so frontend and backend agree on the mapping.

**Requirements:** R3, R4

**Dependencies:** None (pure functions)

**Files:**
- Create: `services/ds-service/cmd/sheets-sync/parse.go`
- Create: `services/ds-service/cmd/sheets-sync/parse_test.go`

**Approach:**
- `ParseFigmaURL(url string) (fileID, nodeID, kind string)` where kind ∈ `{"valid", "canvas-only", "empty", "malformed"}`
- `SubSheetToProduct(tabName string) (product, lobe string, skip bool)` — Go regex mirror of the JS `subSheetToLobe` rules. Skip set: `Product Design`, `Lotties`. Illustrations is `skip=true` for the brain pipeline (kept for future asset surface).
- `LegacyProductToLobe(product string) string` for completeness (not used in sync directly but kept for the same parity with TS)
- All matches case-insensitive after stripping non-alphanumerics — same normalization as the TS file

**Patterns to follow:**
- `lib/atlas/taxonomy.ts` (the source of truth — the Go file is a mechanical port)
- `services/ds-service/internal/projects/rules/` (table-driven Go pattern matchers)

**Test scenarios:**
- Happy path: `https://www.figma.com/design/Ql47.../Project?node-id=12940-595737` → `("Ql47…", "12940:595737", "valid")`
- Happy path: `https://www.figma.com/file/Abc/Foo?node-id=1-2` → also valid (file/ vs design/ both supported)
- Edge case: empty string → `("", "", "empty")`
- Edge case: Figma URL with no `node-id` query → `("Abc", "", "canvas-only")`
- Error path: random URL `https://example.com/foo` → `("", "", "malformed")`
- Tab mapping happy path: `INDstocks` → `(product="INDstocks", lobe="markets", skip=false)`
- Tab mapping with typo: `Onbording KYC` → resolves correctly to Platform lobe
- Tab mapping skip set: `Product Design`, `Lotties`, `Illustrations` → `skip=true`
- Tab mapping unknown: `Made-up Tab` → `(product="Made-up Tab", lobe="platform", skip=false)` (default-fallback)
- **Parity test:** load `lib/atlas/taxonomy.ts` patterns from a fixture file (or a generated JSON dump) and verify the Go mapper produces the same lobe for each tab name we know about — guards against drift

**Verification:**
- `go test ./cmd/sheets-sync/` passes; parity test confirms Go and TS agree on all 24 known tab names

---

- U5. **Cross-tab dedup**

**Goal:** Bucket parsed rows by `(file_id, node_id)` and apply the priority resolution from the design.

**Requirements:** R2

**Dependencies:** U4

**Files:**
- Create: `services/ds-service/cmd/sheets-sync/dedupe.go`
- Create: `services/ds-service/cmd/sheets-sync/dedupe_test.go`

**Approach:**
- Input: `[]NormalizedRow` where each row carries `tabName`, `fileID`, `nodeID`, `productResolution` (`explicit` / `default` / `skip`)
- Output: `[]NormalizedRow` with duplicates collapsed per the rules in High-Level Technical Design
- Pure function. Stable sort: when collapsing, preserve the row with the lowest `row_index` for determinism.

**Patterns to follow:**
- `services/ds-service/internal/projects/modepairs.go` (similar bucket-then-merge shape)

**Test scenarios:**
- Happy path: 3 rows in 3 different tabs all pointing at distinct `(file_id, node_id)` → all 3 returned unchanged
- Edge case: 2 rows in same tab same `(file_id, node_id)` → 1 returned, lower row_index wins; warning logged
- Edge case: rows in 2 explicit tabs same key → both returned (legitimate cross-product reference)
- Edge case: 1 explicit + 1 fallback same key → fallback dropped
- Edge case: empty input → empty output, no panic
- Edge case: rows with empty file_id (canvas-only) skip dedup bucket and pass through

**Verification:**
- Test suite covers all 5 cases above
- Manual: run against the live sheet; print "deduped N → M rows" — M should be ≤ N (ideally equal in the production sheet today, since cross-tab collisions are rare)

---

- U6. **Figma REST resolve + screen-frame extraction**

**Goal:** Given `(file_id, node_id)`, fetch the node from Figma, walk it, return `[]Screen{ID, Name, X, Y, W, H, Type}` filtered to top-level frames ≥ 280×400.

**Requirements:** R3 (the "valid URL → frames" path)

**Dependencies:** U2

**Files:**
- Create: `services/ds-service/cmd/sheets-sync/figma_resolve.go`
- Create: `services/ds-service/cmd/sheets-sync/figma_resolve_test.go`

**Approach:**
- Reuse `services/ds-service/internal/figma/client/client.go` (Figma REST client)
- New function `ResolveSection(ctx, fileID, nodeID) ([]Screen, error)`
- Walk algorithm: stop at FRAME boundary; only collect children that are FRAME / COMPONENT / INSTANCE *and* width ≥ 280 *and* height ≥ 400. Exactly mirrors the `walk_frames` we validated in `/tmp/figma_test.py` on May 4.
- Retry on Figma timeout: one retry with `?scale=1` instead of `?scale=2`. After two attempts, return a typed `RenderTimeoutError` so main.go logs + emits telemetry.
- **Added by deepening**: in-memory LRU cache keyed on `(file_id, node_id)`, TTL 1 hour. Cache hit returns immediately, skipping the Figma roundtrip. Justified by the 45-row hot file `4fTtWb03uIZSESRUd1e9wC` — a row-index shift in that tab today triggers 45 redundant Figma fetches; the cache cuts it to 1 fetch per (file_id, node_id) per hour. Cache size cap: 200 entries (well under the 43 distinct file_ids × ~10 sections each upper bound).

**Patterns to follow:**
- `/tmp/figma_test.py` (the validated Python prototype — Go is a mechanical port)
- Existing client at `services/ds-service/internal/figma/client/`

**Test scenarios:**
- Happy path: SECTION node-id with 30 child FRAMEs at 375×812 → 30 screens returned
- Happy path: FRAME node-id (single-screen widget like BBPS) → 1 screen returned (the frame itself)
- Edge case: node-id resolves to CANVAS (page) → empty slice + warning ("rule says skip canvas")
- Edge case: nested FRAMEs and components inside a section → only TOP-LEVEL frames collected, components inside frames excluded
- Error path: 401 Figma (PAT bad/expired) → typed error, main.go logs and stops the cycle
- Error path: 429 rate limit → exponential backoff with jitter, ≤3 retries
- Error path: 400 "Render timeout" on first call → retry with scale=1 → success
- Error path: 400 "Render timeout" on both calls → return RenderTimeoutError

**Verification:**
- Live test: feed `(IspdtPgpzse0Gft6NR6Z9N, 4483:265999)` → returns ~34 screens (the count we validated May 4 already)
- Live test: feed `(EZOvD6jIwwcyna1RBtS6WI, 3381:32959)` → returns ~72 screens

---

- U7. **State CRUD + diff**

**Goal:** Read the sheet_sync_state table, compute the new/changed/gone partitions against the current pull, write back updated state at the end of the cycle.

**Requirements:** R10

**Dependencies:** U1

**Files:**
- Create: `services/ds-service/cmd/sheets-sync/state.go`
- Create: `services/ds-service/cmd/sheets-sync/state_test.go`

**Approach:**
- `LoadState(ctx, db) (map[StateKey]Row, error)` where StateKey is `{SpreadsheetID, Tab, RowIndex}`
- `Diff(currentNormalizedRows, state) (new, changed, unchanged, gone []NormalizedRow)`
- `Persist(ctx, db, current, runResult)` — single transaction: UPSERT seen rows, UPDATE last_seen_at, UPDATE row_hash for changed, set last_error for failed
- Row hash: SHA256 over `(file_id|node_id|design_poc|product_poc|status|drd_link|proto_link)` — every column the brain inspector renders. Format columns aren't included.
- "GONE" detection: any state row whose StateKey is not in the current pull's keyset

**Patterns to follow:**
- `services/ds-service/internal/projects/repository.go` — TenantRepo wrapper pattern, single-transaction writes
- `services/ds-service/internal/projects/idempotency.go` — similar in-memory diff pattern

**Test scenarios:**
- Happy path: empty state + 5 rows in pull → all 5 are NEW, none CHANGED/GONE
- Happy path: state has 5 rows, pull has same 5 rows with same hashes → all 5 are UNCHANGED
- Edge case: state has 5, pull has 6 (1 inserted) → 1 NEW, 5 UNCHANGED
- Edge case: state has 5, pull has 4 (1 deleted) → 0 NEW, 4 UNCHANGED, 1 GONE
- Edge case: state has 5, pull has 5 but 1 row's hash changed → 0 NEW, 4 UNCHANGED, 1 CHANGED
- Edge case: row_index shifts (insert in middle) → CHANGED detected for shifted rows; sync re-imports them. Verify this is acceptable (idempotency_key on POST means no duplicate flows in the DB)
- Persist test: failed export records last_error and does NOT bump last_imported_at, so the next cycle retries

**Verification:**
- Unit tests pass
- Live: run sync once → second consecutive run reports 0 new / 0 changed / 0 gone (idempotency)

---

- U8. **Export wrapper: sheet rows → ExportRequest → POST /v1/projects/export**

**Goal:** Given a NEW or CHANGED row + its resolved frames, build the ExportRequest and POST it, parse the response, return `(project_id, version_id, deeplink, trace_id)`.

**Requirements:** R3, R4

**Dependencies:** U6, U7

**Files:**
- Create: `services/ds-service/cmd/sheets-sync/export.go`
- Create: `services/ds-service/cmd/sheets-sync/export_test.go`

**Approach:**
- Synthetic file_id for sub-sheet projects: `sheet:<tab-slug>` (e.g. `sheet:indstocks`). Real Figma file_ids stay on the FlowPayload's per-flow file_id.
- Idempotency key: `sha256(spreadsheet_id|tab|row_index|row_hash)` so a re-import of the same row is a no-op server-side
- Designer mapping: pre-shipped map of 9+2 names → user_ids. Includes `Tarushi` and `Devang` who appear in the sheet but were missing from the May 3 `mint-tokens` roster — U10 deploy step re-mints tokens for both before flipping the cron on. Unknown names → default service user_id, telemetry warning emitted.
- Persona: derived from Design POC if it matches an approved persona by name; otherwise empty (the existing `flows.persona_id` is nullable)
- Default-accept: never blocks on unknown personas; just logs
- **Added by deepening**: ExportRequest now also carries `external_drd_url`, `external_drd_title`, `external_drd_snippet`, `external_drd_fetched_at`, `product_poc_text`, `sheet_status` so the new flows-table columns get populated in the same write. Status normalization map: `Done|done|complete|Complete → done`, `WIP|wip|in dev|In dev|In progress|in progress → wip`, `In review|in review → in_review`, `tbd|TBD|To be picked → tbd`, anything else → empty.
- **DRD content fetch (Tier 2)** — for each row whose `external_drd_url` matches `https://docs.google.com/document/d/<id>/`, attempt one fetch via the SA-bearer export endpoint:
  - URL: `https://docs.google.com/document/d/{id}/export?format=txt`
  - Header: `Authorization: Bearer <SA-access-token>` (mint with scope `drive.readonly`)
  - Body: plain text (first line is title, blank line, then content)
  - On 200: split title from body, store `title` + `snippet` (first 500 non-whitespace chars) + `fetched_at = NOW()`
  - On 403/404: leave the columns null + emit `sheets.sync.drd_not_shared` telemetry once per `(doc_id, cycle)` (avoid spam)
  - Cache: 1-hour LRU keyed on `doc_id`, same pattern as the Figma cache in U6 — a Doc fetched in this cycle won't refetch even if multiple sheet rows reference it
  - Failure does NOT block the row's flow import. The flow imports with Tier 1 (URL only); Tier 2 columns stay null until next cycle.

**Patterns to follow:**
- `figma-plugin/code.ts:280–350` (existing reference impl of the same POST)
- `services/ds-service/internal/projects/types.go:ExportRequest` (the contract)

**Test scenarios:**
- Happy path: 1 NEW row with 5 resolved frames → POST body has correct file_id, file_name, flows[0].section_id, flows[0].frames[0..4] with x/y/w/h
- Edge case: empty Figma URL row (ghost) → POST is skipped; flow row is still upserted via direct DB write with screens=[] (so the brain shows a ghost node)
- Error path: 5xx from /v1/projects/export → typed error, sync command logs + writes `last_error` + emits `sheets.sync.export_failed` telemetry
- Error path: 401 from /v1/projects/export → main.go aborts the cycle with "DS_SERVICE_BEARER expired"
- Error path: 409 idempotency violation → treat as success (server already has this version)
- `--dry-run` mode: print the constructed payload to stdout, never invoke fetch

**Verification:**
- Mock POST with `httptest.Server`; assert payload shape matches `ExportRequest`
- Live: run sync against 1 INDstocks row → see new project on the brain at `/atlas`

---

- U9. **Top-level orchestration glue + telemetry summary**

**Goal:** Wire U1–U8 into one cycle, emit per-cycle telemetry events, write the per-cycle audit row to `sheet_sync_runs`.

**Requirements:** R5, R6, R12

**Dependencies:** U2, U3, U4, U5, U6, U7, U8

**Files:**
- Modify: `services/ds-service/cmd/sheets-sync/main.go` (fill in the orchestration body sketched in U2)
- Create: `services/ds-service/cmd/sheets-sync/telemetry.go`

**Approach:**
- Cycle steps (matching the diagram):
  1. Read state.last_synced_at + drive.modifiedTime
  2. If equal: emit `sheets.sync.unchanged` and exit cycle
  3. Else: batch read sheets, normalize, dedupe, diff, per-row export
  4. Persist new state + sheet_sync_runs row
  5. Emit summary: `sheets.sync.cycle.done` with counts
- All telemetry POSTs to `/v1/telemetry/event` (anonymous-allowed endpoint that already exists)
- `--once` mode runs one cycle and exits 0; `--loop` mode does `for { runCycle(); time.Sleep(5*time.Minute) }` (Fly cron prefers `--once` invocations triggered by the schedule directive, but `--loop` is useful for local dev and as a fallback if cron config changes)

**Patterns to follow:**
- `services/ds-service/internal/projects/recovery.go` (loop-with-sleep pattern, signal-aware shutdown)

**Test scenarios:**
- Happy path: full cycle against fixture data — all functions wired, summary event has correct counts
- Edge case: drive.ProbeModifiedTime fails → cycle still runs (graceful degrade — read the sheet anyway, log a warning)
- Edge case: zero NEW/CHANGED/GONE → still emits `sheets.sync.cycle.done` with all-zero counts (proves the sync ran)
- Error path: any unhandled panic in a step → recovered + telemetry-logged with stack trace, cycle ends gracefully

**Verification:**
- `go build ./cmd/sheets-sync/` clean
- `./sheets-sync --once` against the production sheet exits 0 within 60 seconds
- `fly logs -a indmoney-ds-service | grep "sheets.sync"` shows the cycle summary

---

- U10. **Fly app config + Dockerfile + secret pushes + deploy**

**Goal:** Ship `cmd/sheets-sync` as a separate Fly app on a 5-minute machine schedule.

**Requirements:** R5, R9

**Dependencies:** U9

**Files:**
- Create: `fly.sheets-sync.toml`
- Create: `Dockerfile.sheets-sync`

**Approach:**
- Dockerfile: minimal alpine + Go binary, build-args parameterized like the existing audit-server Dockerfile
- fly.toml app name `indmoney-sheets-sync`, primary_region `bom`, machine schedule `*/5` (cron syntax), single auto-stopped machine that wakes per cycle
- Secrets: `fly secrets set GOOGLE_APPLICATION_CREDENTIALS_JSON="$(cat .secrets/google-sheets-sa.json)" -a indmoney-sheets-sync` plus `SHEETS_SPREADSHEET_ID`, `DS_SERVICE_URL=https://indmoney-ds-service.fly.dev`, `DS_SERVICE_BEARER=<super-admin JWT>`, `FIGMA_PAT=<the existing PAT>`
- Entrypoint runs `./sheets-sync --once` (Fly's schedule re-invokes the machine every 5 min; keeps one binary the whole time = warm-start friendly)
- Materialize the SA JSON at startup: tiny shim reads `GOOGLE_APPLICATION_CREDENTIALS_JSON` env var and writes to `/tmp/sa.json`, then sets `GOOGLE_APPLICATION_CREDENTIALS=/tmp/sa.json` for the SDK

**Patterns to follow:**
- `Dockerfile.audit-server` and `fly.audit-server.toml` — exact same shape
- `fly.toml` line 12 (the existing dockerfile path resolution from repo root — same gotcha applies, deploys from repo root)

**Test scenarios:**
- Happy path: `fly deploy --config fly.sheets-sync.toml --remote-only` from repo root succeeds
- Happy path: machine wakes on the 5-min boundary, runs one cycle, exits 0, machine auto-stops
- Edge case: missing secret produces a clear startup error in `fly logs`
- Test expectation: deploy = manual smoke; no automated test

**Verification:**
- `fly status -a indmoney-sheets-sync` shows the machine
- `fly logs -a indmoney-sheets-sync` shows a `sheets.sync.cycle.done` line within 5 minutes of deploy
- `curl https://indmoney-ds-service.fly.dev/v1/projects?limit=200` count rises from 1 to ~20 (one project per non-skipped sub-sheet)

---

## System-Wide Impact

- **Interaction graph:** sync command POSTs to `/v1/projects/export` → triggers the existing pipeline (Figma render + audit + graph_index rebuild) → emits `view_ready` + `GraphIndexUpdated` SSE → atlas brain at `/atlas` repaints with new flow nodes via the bloom animation
- **Error propagation:** Figma render timeouts surface as `last_error` in `sheet_sync_state` + telemetry events; do not block the rest of the cycle. Sheets API 5xx aborts the cycle with retry on next 5-min tick.
- **State lifecycle risks:** GONE rows soft-delete the corresponding `flows.deleted_at`; ATLAS brain hides them. Resurrection on re-add works because state-key match preserves the project_id/flow_id mapping (the row reappears, hash matches, no-op).
- **API surface parity:** Figma plugin and sheet-sync now both write to `/v1/projects/export`. They use disjoint `file_id` namespaces (real Figma file_ids vs. `sheet:` prefix) so they coexist without collisions.
- **Integration coverage:** end-to-end path that touches all of Drive API, Sheets API, Figma REST, ds-service POST, /v1/telemetry — exercised by the live cron, asserted by the cycle summary event having correct counts (not by a unit test, since mocking 4 upstream services would not prove the integration)
- **Unchanged invariants:** The existing `indian-stocks-research` project from the plugin export is not touched. The brain's URL state machine, leaf canvas, and DRD editor continue working as before. The atlas inspector does not need to know whether a flow came from the plugin or the sheet-sync.

---

## Risks & Dependencies

| Risk | Mitigation |
|---|---|
| Figma render timeouts on large sections (132+ frames in "Filters for Stock Screener") | Single retry with `scale=1`; persistent failures → `last_error` + telemetry event so the team can manually shrink the section. Sync continues with other rows. |
| Sheet typos changing tab names ("Onbording KYC" → "Onboarding KYC" if someone fixes it) | State key includes tab name → tab rename creates a new project; old project's flows soft-delete via GONE detection. Acceptable behavior; flag in runbook. |
| Sheet row insertion shifts row_indices, causing spurious "CHANGED" → re-imports of unchanged rows | Idempotency key on `/v1/projects/export` makes re-imports no-ops at the DB level. Cost: extra Figma fetches. Acceptable for a 5-min cron; revisit if Figma quota becomes a constraint. |
| Designer name "Drishti & Ritwik" doesn't map to a single user_id | Map to default service user_id + emit `sheets.sync.unknown_designer` event. Audit log records the raw text so the team can audit later. |
| SA JSON exposed via Fly secrets | Treated as a long-lived credential; rotated like Figma PAT. Stored only as Fly secret + local `.secrets/` (gitignored). Service account has Viewer-only on the sheet. |
| Sheets API quota: 300 reads / minute / project | One read per 5-min cycle is 0.07 reads/min. Quota irrelevant for normal operation. |
| Figma REST quota | More relevant: one node-fetch per NEW/CHANGED row, ~20-50/cycle in steady state. Well below their published limits; if hit, exponential backoff in U6 handles it. |
| Cycle exceeds 5-min window (next cron starts before previous ends) | Fly schedule directive defaults to "skip if previous still running" (verify this); add a `pid_file` or DB lock if needed. Probably overkill — typical cycle is <30s. |
| Manual sheet edits during a cycle race the read | Acceptable — next cycle (5 min later) catches up. Drive `modifiedTime` will reflect the in-flight edit. |
| **Heavy file `4fTtWb03uIZSESRUd1e9wC` (45 rows)** triggers 45 Figma fetches on a single row-index shift | LRU cache in U6 (`(file_id, node_id) → frames`, TTL 1h, 200 entries). First cycle does the fetches; subsequent cycles within an hour are no-ops at the Figma layer. |
| 84 ghost flows (rows with empty Figma URL) crowd the brain visually | Render ghosts smaller + dimmer than real flows; inspector header shows "Pending Figma link". Designers can still see who's working on what; not a noise problem. |
| `Tarushi` + `Devang` not in May 3 `mint-tokens` roster | U10 deploy step re-mints both before flipping cron on; designer mapping in `cmd/sheets-sync/parse.go` ships with all 11 names. |
| DRD content fetching is multi-system pain | Two-tier strategy. Tier 1 (URL link) ships universal. Tier 2 (title + snippet) ships for Google Docs only — verified working with the SA on 2026-05-05. Confluence + Notion remain Tier 1 until the team specifically asks. |
| GDoc DRDs not yet shared with the SA → Tier 2 silent on day one | Acceptable: Tier 1 (URL link) still renders; designers see a clickable button. As soon as a doc gets shared (per-doc or via folder default), Tier 2 backfills automatically on the next 5-min cycle. Telemetry event `sheets.sync.drd_not_shared` once per (doc_id, cycle) gives admins a list of "DRDs to share" without spamming logs. |

---

## Documentation / Operational Notes

### Runbook entries

**"sync didn't pick up a row I just added"**
1. `fly logs -a indmoney-sheets-sync | tail -100` — look for the cycle that should have caught it
2. If `sheets.sync.unchanged` events for the relevant time window: the Drive modifiedTime gate wrongly says no change. Check `drive_modified_time` in `sheet_sync_runs` table; if same as a previous run, Sheets cache may be stale — wait one more cycle.
3. If cycle ran but row is missing: query `sheet_sync_state WHERE tab = 'X' AND row_index = N`. If `last_error` is set, see column for reason.

**"row imported but Figma frames are missing"**
1. Row's `file_id` and `node_id` will be in `sheet_sync_state`
2. `figma render timeout` in `last_error` → wait for next cycle, automatic retry with smaller scale
3. Persistent timeout → manually split the Figma section into smaller pieces

**"new tab added to sheet, doesn't show up"**
1. Sync auto-discovers tabs (no allow-list). Check `parse.go` — does the tab name match the skip set (`Product Design`, `Lotties`, `Illustrations`)?
2. If not in skip set, expect a project node on the Platform lobe (default fallback). Add an explicit rule in `lib/atlas/taxonomy.ts` AND `cmd/sheets-sync/parse.go` to place it correctly.

**"forced re-sync"**
- `fly machine restart -a indmoney-sheets-sync` — runs one cycle immediately. State diff handles the cycle naturally; nothing weird happens from manual restarts.

**"completely reset state"**
- `sqlite3 ds.db "DELETE FROM sheet_sync_state WHERE spreadsheet_id = '1sS-...';"`
- Next cycle treats every row as NEW; full re-import. Idempotency keys prevent duplicate flows.

**"DRD inspector says 'External DRD' but no preview / title — why?"** (Tier 2 silent)
- Filter Fly logs for `sheets.sync.drd_not_shared` events to see which doc IDs were rejected
- Most likely cause: the GDoc is not shared with `indmoneydocs@gen-lang-client-0386349723.iam.gserviceaccount.com`
- Two fixes:
  - **Per-doc**: Designer opens the Doc → Share → adds the SA email as Viewer → next cycle (within 5 min) the snippet appears
  - **Bulk**: Workspace admin sets a default share rule on the team's "Design DRDs" folder so anything inside auto-shares with the SA email
- After either fix, no manual sync trigger needed — the next 5-min cycle picks it up

**"Tier 2 fetches a stale snippet — designer updated the Doc but the inspector hasn't caught up"**
- Tier 2 snippet refreshes once per cycle (every 5 min) when the row's hash changes
- If the row's other fields didn't change, the snippet won't refresh until either: the row hash changes, OR the 1-hour Tier-2 LRU cache expires
- Force refresh: bump any cell in the row (e.g. add a trailing space) → next cycle re-imports → snippet refreshes

### Monitoring

- Cycle summary events under `sheets.sync.cycle.done` — view in `fly logs` or feed to a dashboard
- Per-error events under `sheets.sync.export_failed`, `sheets.sync.figma_timeout`, `sheets.sync.unknown_designer`
- Alert thresholds (informal): >10 export failures in 1h, >3 consecutive cycles with `result='failed'`

---

## Sources & References

- Conversation thread on May 4, 2026: planning approval + the spec items R1–R12 above
- Validated Figma extraction logic: `/tmp/figma_test.py` (May 4) — Go port replicates this
- Live sheet inspector: `/tmp/sheet_inspect.py` (May 4) — Go port reads the same shape via `google.golang.org/api`
- Existing Fly app pattern: `fly.audit-server.toml` + `Dockerfile.audit-server`
- Atlas taxonomy source-of-truth: `lib/atlas/taxonomy.ts`
- Existing export endpoint: `services/ds-service/internal/projects/server.go:HandleExport`
- Existing telemetry endpoint: `services/ds-service/internal/projects/telemetry.go` (May 4, this conversation)
- Migration pattern reference: `services/ds-service/migrations/0009_graph_index.up.sql`, `0015_tenant_fk_constraints.no_tx.up.sql`
