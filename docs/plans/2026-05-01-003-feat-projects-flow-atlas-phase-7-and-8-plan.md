---
title: "Projects · Flow Atlas — Phase 7 + Phase 8 (Admin / ACL / Notifications polish + Global Search)"
type: feat
status: active
created: 2026-05-01
origin: docs/brainstorms/2026-04-29-projects-flow-atlas-requirements.md
predecessor: docs/plans/2026-05-02-002-feat-projects-flow-atlas-phase-6-plan.md
---

# Phase 7 + Phase 8 — Admin / ACL polish + Global Search

> **Combined plan.** Phase 7 (admin curation surfaces, per-resource ACL grants, notification polish, mind-graph composition + saved views) and Phase 8 (global search across flows / DRDs / decisions / components) are bundled into one plan because they share three load-bearing pieces of infrastructure: (a) the role-based auth model + ACL grants determine *what* search can return; (b) the admin surfaces expose *which* indexed slices are visible to *whom*; (c) the notification subsystem carries deep-links into both. Building search before ACLs ships information leaks; building ACLs without search hides their value. Ship together.

## Overview

End-of-Phase-6 status: 6 of 8 planned phases shipped. The Projects · Flow Atlas surface (atlas + DRD + violations + mind graph) is complete as a navigation + authoring system; what remains is the connective tissue that makes the org's design knowledge reachable, governable, and curated.

**Phase 7** scope:
- Per-flow ACL grants on top of Phase 5's role model (R21 — "per-flow grant overrides supported")
- DS-lead admin surfaces: rule catalog editor, Product → folder taxonomy curator, persona library approval queue (R3, R4, R20, R26)
- DRD link aliasing on flow rename (Brainstorm Q3)
- Mind-graph polish carried over from Phase 6: real shader-based edge pulse, saved views (`/atlas?focus=…&filters=…` shareable URLs), component composition edges (`uses` from molecule → atom via `composition_refs`)
- Notification preference center (single page where users opt into / out of digest cadences per channel)

**Phase 8** scope:
- Global search across flows, DRD content, decisions (title + body), persona names, component refs (R23)
- Search backed by SQLite FTS5 virtual tables (no Elasticsearch sidecar in v1 — single-operator deployment)
- Result-type discrimination (each result tagged flow / drd / decision / persona / component)
- ACL filter on every result (Phase 7 grants are honored — nothing leaks)
- Deep-link entry into the appropriate surface (`/projects/<slug>`, `/atlas?focus=…`, `/components/<slug>`)
- In-graph free-text search inside the mind graph (R23 + AE-5 reverse-lookup polish)
- Existing cmdk `⌘K` palette becomes the global entry point

**Why these two together, not separately:**

| Without combining | With combining |
|---|---|
| Phase 7 ships ACLs but search returns everything → can't safely ship the admin surfaces that *show* search results to leads | One auth pass governs both surfaces |
| Phase 8 ships search but admins have no surface to manage what's indexable | The persona-approval queue lives next to the search index visibility toggle |
| Two CI cycles, two closure docs, two ramps for the same audience | One ramp, one closure |

---

## Animation Philosophy

Phase 7 + 8 inherit the existing primitives — no new motion library lands. New surfaces:

1. **Admin curation tables** — Phase 4 dashboard's `<DataGrid>` + the existing severity-bar primitives. New animations: row-shimmer on save (300ms), inline-edit field tween (150ms ease-out cubic). All gated behind `useReducedMotion()` in the standard pattern.
2. **Search-result-list reveal** — Framer Motion `AnimatePresence` on the cmdk popover; result rows enter staggered (40ms cascade, 80ms each). Cmdk's built-in keyboard navigation drives focus state.
3. **Persona approval bell** — when a persona enters the pending-pool, the admin's `/atlas/admin` header bell pulses (single 800ms pulse, fires on `NotificationCreated` SSE event with kind=`persona_pending`).
4. **Saved-view share-link toast** — confirms a copied URL with a 2s slide-up + checkmark.
5. **Mind-graph edge pulse (real shader)** — Phase 6 v1 shipped a "dim-non-incident" approximation; Phase 7 lands the proper sine-wave alpha modulation via a custom ShaderMaterial uniform. Skipped under reduced-motion.

**Reduced-motion compliance:** every new primitive falls back to instant state changes. No new media-query code — the existing `useReducedMotion()` hook covers all surfaces.

---

## Data Model — additive, no rewrites

Three new tables, all migration `0010_admin_acl_search.up.sql`:

### `flow_grants` — per-flow ACL overrides (Phase 7 R21)

```sql
CREATE TABLE IF NOT EXISTS flow_grants (
    flow_id        TEXT NOT NULL REFERENCES flows(id) ON DELETE CASCADE,
    user_id        TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    tenant_id      TEXT NOT NULL,
    role           TEXT NOT NULL,               -- viewer | commenter | editor | owner
    granted_by     TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    granted_at     TEXT NOT NULL,
    revoked_at     TEXT,                        -- soft-revoke for audit trail
    PRIMARY KEY (flow_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_flow_grants_user ON flow_grants(user_id, revoked_at);
CREATE INDEX IF NOT EXISTS idx_flow_grants_flow ON flow_grants(flow_id, revoked_at);
```

Resolution rule: a user's effective role on a flow is `MAX(default-role-for-product, flow_grants.role)` where MAX honors the precedence `viewer < commenter < editor < owner < admin`. Admins always win.

### `notification_preferences` — already shipped in Phase 5

Phase 7 just adds a UI; no schema change.

### `taxonomy_proposals` — designer-suggested folder/persona moves (Phase 7 R4 + R26)

```sql
CREATE TABLE IF NOT EXISTS taxonomy_proposals (
    id             TEXT PRIMARY KEY,
    tenant_id      TEXT NOT NULL,
    kind           TEXT NOT NULL,               -- folder | persona
    proposed_by    TEXT NOT NULL REFERENCES users(id),
    proposed_at    TEXT NOT NULL,
    payload_json   TEXT NOT NULL,               -- shape varies by kind
    status         TEXT NOT NULL DEFAULT 'pending',  -- pending | approved | rejected
    reviewed_by    TEXT REFERENCES users(id),
    reviewed_at    TEXT,
    review_note    TEXT
);
CREATE INDEX IF NOT EXISTS idx_taxonomy_proposals_pending
    ON taxonomy_proposals(tenant_id, status) WHERE status = 'pending';
```

### `search_index_fts` — FTS5 virtual table (Phase 8)

```sql
CREATE VIRTUAL TABLE IF NOT EXISTS search_index_fts USING fts5(
    tenant_id UNINDEXED,
    entity_kind UNINDEXED,         -- flow | drd | decision | persona | component
    entity_id UNINDEXED,
    open_url UNINDEXED,
    title,
    body,
    tokenize = 'porter unicode61'
);
```

Populated by the same `RebuildGraphIndex` worker (Phase 6) — extended in U8 to upsert search rows alongside graph rows. Single rebuild pass produces both indexes.

---

## Requirements Trace

| Origin | Requirement | Phase 7 + 8 unit |
|---|---|---|
| **R3** | Audit-rule curation editor | U2 |
| **R4** | Product → folder taxonomy curator | U3 |
| **R20** | Severity overrides per-rule | U2 |
| **R21** | Per-flow grant overrides | U1 |
| **R22** | Comment threading depth >1 | U7 (deferred from Phase 5) |
| **R23** | Global search across all surfaces | U8 + U9 + U10 |
| **R23** | In-graph free-text search | U11 |
| **R24** | Notification preference center | U6 |
| **R26** | Persona library approval pending-pool | U4 |
| **AE-5** | Reverse-lookup polish via search | U10 + U11 |
| **Origin Q3** | DRD migration on flow rename | U5 |
| **Origin Q9** | Persona-pending visibility window | U4 |
| **Phase 6 deferred** | Component composition edges | U6 (mind-graph extension) |
| **Phase 6 deferred** | Real shader-based edge pulse | U7 (visual polish) |
| **Phase 6 deferred** | Saved views / shareable URLs | U7 (saved views) |

---

## Scope Boundaries

### In scope (Phase 7 + 8 combined)

**Phase 7:**
- `flow_grants` table + per-flow ACL UI on the project view
- Rule catalog editor at `/atlas/admin/rules`
- Taxonomy curator at `/atlas/admin/taxonomy` — tree editor for Product → folder
- Persona approval queue at `/atlas/admin/personas`
- Notification preference center at `/settings/notifications`
- DRD link aliasing: when a flow's path moves, prior URLs 301 → new URL via a new `flow_aliases` shadow table
- Component composition `uses` edges (molecule → atom via manifest `composition_refs`)
- Real shader-based edge pulse on click-and-hold (Phase 6 v1 polish)
- Saved views: `/atlas?focus=…&filters=…&platform=…` URLs work as expected today; Phase 7 adds a "Copy share link" button that copies the current canvas state as a URL

**Phase 8:**
- SQLite FTS5 `search_index_fts` virtual table
- `GET /v1/search?q=…` handler returning typed `{kind, id, title, snippet, open_url}` rows
- Search-side ACL filter using `flow_grants`
- Cmdk palette integration: `⌘K` opens the existing palette; results from `/v1/search` populate alongside the static navigation entries
- In-graph search input inside `/atlas` (top-left, slides down on focus, type-ahead)
- Search index incrementally updated by the same `RebuildGraphIndex` worker on flow / DRD / decision write events

### Deferred

- **Elasticsearch sidecar.** SQLite FTS5 is sufficient for the production tenant size (~400 flows × ~10 paragraphs of DRD ≈ 4k FTS rows). Crossing that threshold or wanting fuzzy / typo-tolerant search is Phase 9.
- **Cross-tenant admin** (org-wide superadmin browsing tenants). Single-operator deployment doesn't need it; Phase 10.
- **Notification webhook destinations.** Slack incoming webhook is shipped; Discord / Teams / generic webhooks are Phase 9.
- **Branch + merge review workflow** (carried from origin) — still deferred. Re-export-as-immutable-version remains the model.
- **AI-suggested decisions / AI-suggested mind-graph traversals.** The data is there (search index + graph index); the UX isn't designed. Phase 9.
- **iPad / touch gesture polish on mind graph.** Mouse-first stays.
- **Public read-only sharing.** Requires sharing-link infra + redaction; Phase 9 at earliest.

### Outside this product's identity

Same as Phase 1-6: not Notion / Confluence, not Linear / Jira, not Mobbin. Search is over INDmoney's design knowledge graph; nothing else.

---

## Context & Research

### Relevant code patterns to extend (Phase 1-6)

- `services/ds-service/internal/projects/server.go` — `s.requireAuth` + `s.requireSuperAdmin` middlewares are the auth gate. Phase 7 adds `s.requireFlowRole(role string)` for per-flow grants.
- `services/ds-service/internal/projects/dashboard.go` (Phase 4) — multi-aggregation handler pattern. Phase 7's admin surfaces follow the same `sync.WaitGroup` shape.
- `services/ds-service/internal/projects/graph_rebuild.go` (Phase 6) — the `RebuildGraphIndex` worker. Phase 8 extends this to also write `search_index_fts` rows during the same flush. One worker, two output indexes.
- `lib/inbox/` (Phase 4) + `lib/notifications/` (Phase 5) — preference center reuses the existing notification primitives.
- `components/cmdk/` — the existing `⌘K` palette. Phase 8 adds a new `<SearchResultsSection>` that lives below the static nav entries.
- `app/atlas/admin/page.tsx` (Phase 5.2 P1) — the admin shell. Phase 7's three new admin pages mount as siblings under `/atlas/admin/{rules,taxonomy,personas}`.

### External libraries

| Library | Version | Used for | Notes |
|---|---|---|---|
| SQLite FTS5 | builtin (modernc.org/sqlite v1.50+) | Search index | No new dep; Porter stemming + unicode61 tokenizer |
| `cmdk` | already in tree | `⌘K` palette | Phase 8 wires search results into existing component |

**No new top-level dependencies for Phase 7 + 8.**

### Institutional learnings to honor

- **Migration 0010 is sequential after Phase 6's 0009.** Forward-only column-add discipline (Phase 1 learning #2) applies.
- **ACL writes go through the audit log.** Every grant / revoke writes an `audit_log` row alongside the `flow_grants` mutation in the same transaction (Phase 4 pattern).
- **FTS5 sync is the worker's job, not the request's.** Phase 6 inverted "aggregate on read" to "materialise on write"; Phase 8 follows the same shape.
- **Reduced-motion gates every new primitive.** No exceptions.
- **Empty / loading / error states use the existing variant library** (Phase 3 EmptyState).

---

## Key Technical Decisions

### 1. ACL resolution at query time, not denormalised

**Rejected:** denormalise effective role onto each flow row.
**Shipped:** resolve at query time via a small helper:

```go
func (t *TenantRepo) ResolveFlowRole(ctx context.Context, userID, flowID string) (string, error) {
    // 1. SELECT default role from product membership (Phase 5 logic).
    // 2. SELECT flow_grants.role for (flow_id, user_id) WHERE revoked_at IS NULL.
    // 3. Return MAX(default_role, grant_role) per the precedence ladder.
}
```

Two SELECTs per request is fine (sub-millisecond on indexed lookups). Denormalising would force `flow_grants` writes to fan out across every dependent table.

### 2. Search index is materialised by the same worker as `graph_index`

The `RebuildGraphIndex` worker (Phase 6) gets a sibling output: each flush also writes / updates `search_index_fts` rows for the affected flows / DRDs / decisions. One write transaction covers both indexes — no two-phase commit, no consistency drift.

```go
// inside RebuildGraphIndex.RebuildFull
err := repo.UpsertGraphIndexRows(ctx, tx, graphRows)
err = repo.UpsertSearchIndexRows(ctx, tx, searchRows)
err = tx.Commit()
```

### 3. Search ACL filter at SQL level

```sql
SELECT s.entity_kind, s.entity_id, s.title, snippet(search_index_fts, 4, '<mark>', '</mark>', '…', 16) AS snip
  FROM search_index_fts s
 WHERE s.tenant_id = ?
   AND search_index_fts MATCH ?
   AND (
        s.entity_kind != 'flow'
        OR EXISTS (
            SELECT 1 FROM flow_grants g
             WHERE g.flow_id = s.entity_id
               AND g.user_id = ?
               AND g.revoked_at IS NULL
        )
        OR <user has product-default-role>
   )
 ORDER BY rank
 LIMIT 30
```

Single SELECT, ACL-aware, indexed on (tenant_id, MATCH, flow_grants).

### 4. In-graph search reuses the global handler

The `/atlas` search input calls the same `GET /v1/search` endpoint with a `?scope=mind-graph` query param. The handler filters to entities visible in the current mind graph (intersect with `graph_index` rows) and returns. No second backend; single source of truth.

### 5. Saved views are URL-only

No new persistence. Phase 7 adds a "Copy share link" button on `/atlas` that builds a URL from current state (`?focus=flow_abc&filters=components,decisions&platform=mobile`). Pasting the URL hydrates the same view. Future-Phase polish: persist named views server-side.

### 6. Component composition edges piggyback on existing manifest

The Figma extractor already writes `composition_refs` per component (Phase 5 deep-component-extraction). Phase 7 extends `BuildComponentRows` (Phase 6) to emit `edges_uses_json` populated with composition target slugs. No schema change, no extractor change.

---

## Output Structure

```
services/ds-service/migrations/
  0010_admin_acl_search.up.sql                 # NEW — flow_grants, taxonomy_proposals, search_index_fts
services/ds-service/internal/projects/
  acl.go                                       # NEW — flow_grants reads + writes
  acl_test.go                                  # NEW
  admin_rules.go                               # NEW — rule catalog editor handlers
  admin_taxonomy.go                            # NEW — folder + persona curation handlers
  admin_personas.go                            # NEW — pending-pool approval handlers
  search.go                                    # NEW — FTS5 query handler
  search_test.go                               # NEW
  graph_rebuild.go                             # MODIFY — also write search_index_fts on flush
  graph_sources.go                             # MODIFY — extend BuildComponentRows for composition edges
services/ds-service/internal/projects/
  flow_alias.go                                # NEW — DRD migration on rename
app/atlas/admin/
  rules/page.tsx                               # NEW
  taxonomy/page.tsx                            # NEW
  personas/page.tsx                            # NEW
app/settings/
  notifications/page.tsx                       # NEW — preference center
app/atlas/
  SearchInput.tsx                              # NEW — in-graph search top-left
  SavedViewShareButton.tsx                     # NEW — copy-link
components/search/
  SearchResultsSection.tsx                     # NEW — cmdk integration
  useSearch.ts                                 # NEW — debounced query hook
tests/
  admin-rules.spec.ts                          # NEW
  admin-taxonomy.spec.ts                       # NEW
  search.spec.ts                               # NEW
  flow-grants.spec.ts                          # NEW
docs/runbooks/
  phase-7-8-admin-search.md                    # NEW
```

---

## Implementation Units

### U1 — Phase 7: `flow_grants` schema + `ResolveFlowRole` + per-flow ACL UI

**Goal:** add the per-flow grant table + resolution helper + a minimal UI on the project shell that shows current grants and lets editors invite users.

**Files:**
- Create: `services/ds-service/migrations/0010_admin_acl_search.up.sql` (only the `flow_grants` portion in this unit; the rest land in their own units)
- Create: `services/ds-service/internal/projects/acl.go` — `ResolveFlowRole`, `GrantFlowRole`, `RevokeFlowRole`
- Create: `services/ds-service/internal/projects/acl_test.go`
- Modify: `services/ds-service/internal/projects/server.go` — add `requireFlowRole(role string)` middleware
- Create: `app/projects/[slug]/AccessPanel.tsx` — list current grants + invite form
- Modify: every existing handler that writes flow data to call `requireFlowRole("editor")`

**Approach:**
- Migration adds `flow_grants` only (taxonomy_proposals + search_index_fts come later).
- `ResolveFlowRole` runs 2 SELECTs (default + grant); returns MAX role per the precedence ladder.
- New middleware `requireFlowRole(role)` reads flow_id from URL, calls `ResolveFlowRole`, 403s if insufficient.
- AccessPanel is a Phase 4-style table: list grants with role pill + "Revoke" button; invite form posts to `POST /v1/projects/:slug/grants`.
- Audit log: every grant + revoke writes an `audit_log` row in the same transaction.

**Test scenarios:**
- Designer A in Product X gets editor on flow F: `ResolveFlowRole(A, F) == "editor"` (default product role)
- Designer A is granted owner on flow F via `flow_grants`: `ResolveFlowRole(A, F) == "owner"` (grant wins)
- Grant revoked: lookup falls back to default
- Cross-tenant grant attempt: 403

---

### U2 — Phase 7: rule catalog editor at `/atlas/admin/rules`

**Goal:** DS lead can list every audit rule, toggle enabled, edit severity, write new rule expressions (CEL — already shipped via DesignBrain governance engine reuse).

**Files:**
- Create: `services/ds-service/internal/projects/admin_rules.go` — list / patch handlers
- Create: `app/atlas/admin/rules/page.tsx`
- Modify: `services/ds-service/cmd/server/main.go` — register routes (super-admin gated)

**Approach:**
- List handler returns `audit_rules` rows.
- Patch handler updates `enabled` + `default_severity`. Writing a new rule expression triggers a fan-out re-audit (Phase 2 worker).
- Page is a `<DataGrid>` with inline-edit cells. Save button writes patch; row shimmers on success.

---

### U3 — Phase 7: Product → folder taxonomy curator

**Goal:** DS lead can rename / archive / promote folders; designer-extended sub-folders sit below the canonical tree until promoted.

**Files:**
- Create: `services/ds-service/internal/projects/admin_taxonomy.go`
- Create: `app/atlas/admin/taxonomy/page.tsx`

**Approach:**
- Tree view with drag-to-reorder using Framer Motion `Reorder.Group`.
- Promote action moves a designer-extended path into the canonical taxonomy by writing it to a new `canonical_taxonomy` table (also added in migration 0010 — small, ~9 products × 30 folders = 270 rows max).
- Archive sets `deleted_at` on the folder rows; existing flows under the archived folder stay readable but new exports can't land there.

---

### U4 — Phase 7: persona library approval queue

**Goal:** designer-suggested personas (status='pending' from Phase 1) surface in the admin's queue for approval. Approval flips status to 'approved'; rejection deletes the row.

**Files:**
- Create: `services/ds-service/internal/projects/admin_personas.go`
- Create: `app/atlas/admin/personas/page.tsx`
- Modify: `services/ds-service/internal/sse/events.go` — add `PersonaPending` event for the admin bell

**Approach:**
- Pending-pool query: `SELECT * FROM personas WHERE status = 'pending'`.
- Admin's `/atlas/admin` header shows a bell badge with the pending count; clicking jumps to the queue.
- Approval writes status='approved' + approved_by + approved_at in a single tx.
- SSE event fires on the admin's `inbox:<tenant>` channel when a designer suggests a new persona.

---

### U5 — Phase 7: DRD link aliasing on flow rename (Origin Q3)

**Goal:** when a flow's path changes, prior URLs `/projects/<old-slug>` redirect to the new slug.

**Files:**
- Create: `services/ds-service/migrations/0010_admin_acl_search.up.sql` (extends with `flow_aliases` table)
- Create: `services/ds-service/internal/projects/flow_alias.go`
- Modify: `services/ds-service/internal/projects/server.go` — alias-aware lookup in HandleProjectGet

**Approach:**
- `flow_aliases (slug TEXT PRIMARY KEY, flow_id TEXT, redirected_to TEXT, created_at TEXT)` — written automatically when `UpdateProject` changes `slug`.
- HandleProjectGet checks `flow_aliases` if the requested slug doesn't match any active project; 301s to the live URL if found.
- Search results carry the live slug; aliases only matter for hand-typed / pasted URLs.

---

### U6 — Phase 7: notification preference center + composition edges in mind graph

**Goal:** ship the user-facing settings page for the existing Phase 5 `notification_preferences` table. Also extend Phase 6's BuildComponentRows to emit composition `uses` edges from the manifest.

**Files:**
- Create: `app/settings/notifications/page.tsx`
- Modify: `services/ds-service/internal/projects/graph_sources.go` — extend BuildComponentRows
- Add Playwright spec for the prefs page.

**Approach:**
- Prefs page is a form: per-channel cadence selector (off / daily / weekly), Slack webhook URL field, email field, time-zone picker.
- BuildComponentRows reads `composition_refs[]` from the manifest and emits edges to `component:<atom_slug>` for each. No schema change to graph_index — same `edges_uses_json` array.

---

### U7 — Phase 7: real shader-based edge pulse + saved-view share link

**Goal:** replace Phase 6's "dim non-incident" approximation with the real sine-wave shader-based pulse. Add the share-link button.

**Files:**
- Modify: `app/atlas/BrainGraph.tsx` — install custom ShaderMaterial on edges
- Modify: `app/atlas/SignalAnimationLayer.tsx` — drive the shader uniform from the rAF loop
- Create: `app/atlas/SavedViewShareButton.tsx`

**Approach:**
- ShaderMaterial vertex/fragment shaders accept a `uTime` + `uHeldNodeID` uniform pair. Fragment computes per-edge alpha as `base + 0.4 * sin(uTime * 6.28)` when the edge is incident to held; otherwise `base * 0.4`.
- Pulsing only edges incident to the held node — 1Hz sine, frame-time uniform updated by the existing rAF loop.
- Share button reads current `useGraphView` state, builds query string, copies to clipboard, shows toast.

---

### U8 — Phase 8: FTS5 search index + worker integration

**Goal:** add the `search_index_fts` virtual table; extend `RebuildGraphIndex` worker to write search rows on every flush.

**Files:**
- Modify: `services/ds-service/migrations/0010_admin_acl_search.up.sql` — add the FTS5 table
- Modify: `services/ds-service/internal/projects/graph_rebuild.go` — call new helper in RebuildFull
- Create: `services/ds-service/internal/projects/search_index.go` — UpsertSearchIndexRows + entity readers
- Create: `services/ds-service/internal/projects/search_index_test.go`

**Approach:**
- For each flush:
  - flows → search row with title=flow.name, body=concat(persona_name, project.product, project.path)
  - DRDs → search row with title=flow.name+" — DRD", body=plain-text-extract(flow_drd.content_json)
  - decisions → search row with title=decision.title, body=plain-text-extract(decision.body_json)
  - personas → search row with title=persona.name, body=""
  - components → search row with title=manifest.name, body=manifest.category+" "+manifest.description
- Helper: `extractPlainText(blocknote_json)` — recursively walks the BlockNote AST collecting text leaves.
- Idempotent: `INSERT OR REPLACE` keyed on `(tenant_id, entity_kind, entity_id)`.

---

### U9 — Phase 8: `GET /v1/search` handler with ACL filter

**Goal:** the search backend. Single SELECT against FTS5 with ACL-aware filtering.

**Files:**
- Create: `services/ds-service/internal/projects/search.go`
- Modify: `services/ds-service/cmd/server/main.go` — register route
- Create: `services/ds-service/internal/projects/search_test.go`

**Approach:**
- Handler reads `?q=`, `?limit=` (default 20, max 50), `?scope=` (mind-graph | all).
- Auth: requires Bearer token; tenant scoped via claims.
- Query: FTS5 MATCH with ACL JOIN as documented in Key Technical Decisions §3.
- Returns `{results: [{kind, id, title, snippet, open_url, score}]}`.
- Snippet uses `snippet()` FTS5 function with `<mark>` highlighting.

**Test scenarios:**
- Search "onboarding" returns flow titles matching
- Search "@karthik" — mention search across DRD content (treated as plaintext via FTS)
- Search with no ACL grant: flow that designer doesn't have access to is filtered out
- Empty query: 400
- ?scope=mind-graph: results filtered to entities present in the user's current `graph_index` slice

---

### U10 — Phase 8: cmdk palette integration

**Goal:** `⌘K` results section populated from `/v1/search`.

**Files:**
- Create: `components/search/SearchResultsSection.tsx`
- Create: `components/search/useSearch.ts`
- Modify: existing cmdk palette wiring to add the new section below the static nav entries

**Approach:**
- `useSearch` debounces input (250ms), fires `GET /v1/search?q=…`, returns typed results.
- Section renders results grouped by kind (Flows / Decisions / Components / Personas / DRD).
- Keyboard nav: cmdk's built-in arrow + enter; on enter, navigate to `result.open_url`.

---

### U11 — Phase 8: in-graph search input

**Goal:** type-to-find inside `/atlas`. Slides down from the top-left on focus; results filter visible nodes in real time.

**Files:**
- Create: `app/atlas/SearchInput.tsx`
- Modify: `app/atlas/BrainGraph.tsx` — accept a `searchQuery` prop and dim non-matching nodes

**Approach:**
- Input lives in the top-left corner. Focus triggers slide-down (Framer Motion).
- On change, calls `/v1/search?q=…&scope=mind-graph` (debounced).
- Returned `entity_id`s are joined against the current `visible.nodes` set; matching nodes glow brighter, non-matching dim to opacity 0.3.
- Press Esc clears the query + restores the full graph.

---

### U12 — Combined: Playwright e2e + closure runbook

**Goal:** ship the e2e + the deploy + monitor runbook + closure docs for both phases.

**Files:**
- Create: `tests/admin-rules.spec.ts`, `admin-taxonomy.spec.ts`, `search.spec.ts`, `flow-grants.spec.ts`, `notification-prefs.spec.ts`
- Create: `docs/runbooks/phase-7-8-admin-search.md`
- Create: `docs/solutions/2026-XX-XX-001-phase-7-8-closure.md` (date stamped at ship time)

**Approach:**
- Each Playwright spec covers the 3-5 critical paths for its surface (admin gate, edit save, smoke through preview).
- Runbook: deploy steps + the migration `0010` cold-apply + FTS5 index size budgets + ACL grant audit query.
- Closure: decisions made vs. plan, perf measured, deferred items.

---

## Performance Budgets

| Metric | Budget |
|---|---|
| `GET /v1/search?q=…` (FTS5 + ACL) | ≤30ms p95 on 4k indexed rows |
| Search ACL JOIN selectivity | ≤2× scan of unfiltered match set |
| FTS5 index size at production scale | ≤5MB on disk |
| RebuildGraphIndex full rebuild + search write | ≤8s p95 (was ≤5s for graph_index alone) |
| Admin rule patch round-trip | ≤80ms p95 |
| ACL grant write | ≤30ms p95 |
| Cmdk search-result render | ≤16ms/frame at p99 |
| In-graph search dim/glow | ≤16ms/frame at p99 |

---

## Risk Table

| Risk | Severity | Mitigation |
|---|---|---|
| FTS5 query slow at scale | Medium | Production size is well below threshold; tokenizer is `porter unicode61` (built-in). Profile at U9 + U10 against a 10k-row fixture |
| ACL JOIN explodes search latency | Medium | Index `flow_grants(user_id, flow_id, revoked_at)`; the EXISTS subquery short-circuits |
| Rule-edit-triggered fan-out floods worker | Medium | Phase 2 fan-out worker has priority queues; rule-change triggers run at priority=10 (lowest), letting designer exports through first |
| Flow rename breaks live SSE subscribers | Low | URL change doesn't break SSE channel (channel is keyed on tenant + flow_id, not slug). Only stale URLs need aliasing |
| Search index drifts from live data | Low | Single-worker shape (Phase 6) keeps both indexes synchronous |
| Composition-edge cycles | Low | Manifest's `composition_refs` is acyclic by Figma constraint (no component can be its own ancestor); detected anyway via simple visit-set in BuildComponentRows |
| ACL revoke vs. in-flight SSE event | Low | SSE filtering at broker level reads tenant_id only; per-flow filtering happens client-side. A revoked user might see one stale event before reconnecting; acceptable |

---

## Verification

Phase 7 + 8 ship when:

- All 12 implementation units have green CI
- `tests/admin-*.spec.ts` + `tests/search.spec.ts` + `tests/flow-grants.spec.ts` + `tests/notification-prefs.spec.ts` pass
- Migration `0010` applies cleanly + is embedded in `migrations.FS`
- `RebuildGraphIndex` worker writes both `graph_index` and `search_index_fts` rows on every flush
- `GET /v1/search?q=onboarding` returns matching flows + ACL-filters correctly
- Cmdk `⌘K` results render search hits below static nav
- `/atlas` SearchInput dims non-matching nodes
- Admin rule editor + taxonomy editor + persona approval queue all reachable from `/atlas/admin`
- `flow_grants` audit log entries land on every grant / revoke
- Notification preference center renders + persists prefs
- Real shader-based edge pulse on `/atlas` click-and-hold (replaces Phase 6 dim-non-incident approximation)
- Composition edges visible on the mind graph when Components chip toggled
- Saved-view share button copies a URL that hydrates the same view on paste
- Closure doc dropped at `docs/solutions/2026-XX-XX-001-phase-7-8-closure.md`

---

## Sequencing Plan

```
                    Phase 7 (Admin / ACL / polish)
       ┌────────────────────────────────────────────────────┐
       │  U1  flow_grants schema + ACL                      │
       │  U2  rule catalog editor                           │
       │  U3  taxonomy curator                              │
       │  U4  persona approval queue                        │
       │  U5  DRD link aliasing                             │
       │  U6  notification prefs + composition edges        │
       │  U7  shader edge pulse + share-link button         │
       └─────────────────────────┬──────────────────────────┘
                                 │
                                 ▼
                     Phase 8 (Global Search)
       ┌────────────────────────────────────────────────────┐
       │  U8  FTS5 schema + worker integration               │
       │  U9  /v1/search handler + ACL filter                │
       │  U10 cmdk palette integration                       │
       │  U11 in-graph search input                          │
       └─────────────────────────┬──────────────────────────┘
                                 │
                                 ▼
                  U12 Combined Playwright + runbook + closure
```

**Parallel-safe:** U2 / U3 / U4 are independent admin pages. U5, U6, U7 also independent. U8 must land before U9 + U10 + U11. U12 lands last.

**Estimated calendar:** 6-8 weeks. U1 is the load-bearing ACL piece; U8/U9 is the load-bearing search piece. The other units fan out from those.

---

## References

- Brainstorm: `docs/brainstorms/2026-04-29-projects-flow-atlas-requirements.md` — R3, R4, R20, R21, R22, R23, R24, R26, F7, F8, AE-5, Origin Q3, Q9
- Phase 1-6 plans + closures (all references): the predecessor lineage
- Phase 6 closure: `docs/solutions/2026-05-01-001-phase-6-closure.md` — the deferred-items section that fed Phase 7's polish units
- SQLite FTS5: https://sqlite.org/fts5.html
- cmdk docs: https://cmdk.paco.me

---

## Phase 1-6 Conventions Inherited

These constraints carry forward without re-litigation:

- **Tenant scoping by denormalised `tenant_id` + `TenantRepo` discipline** (Phase 1 learning #1). Every new query in Phase 7-8 follows this.
- **Migration discipline.** `0010` is sequential after Phase 6's `0009`; idempotent (`CREATE TABLE IF NOT EXISTS` / `CREATE VIRTUAL TABLE IF NOT EXISTS`); never DROP COLUMN within the release.
- **Single-worker materialisation.** Phase 6's `RebuildGraphIndex` worker is the materialiser for Phase 8's search index too. One write transaction covers both indexes.
- **SSE single-use ticket auth.** No new channels in Phase 7-8; existing `inbox:<tenant>` carries the new `persona_pending` event type.
- **Reduced-motion via `lib/animations/context.ts`** for every new motion primitive.
- **EmptyState variant library** for empty / loading / error states.
- **Animation-timeline clustering** under `lib/animations/timelines/admin/` (new) for Phase 7's curation UIs.
- **Worker pool size from env.** Search-write pool inherits `GRAPH_INDEX_REBUILD_WORKERS` (no new env var; same worker writes both).
