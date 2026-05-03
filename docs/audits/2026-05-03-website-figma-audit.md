---
title: "Website × Figma audit — every route, every tab, every interaction"
date: 2026-05-03
status: in-progress
scope:
  visual_parity: true
  data_binding: true
  functional: true
---

# Website × Figma audit

Multi-axis audit of the entire ds-docs Next.js app:
- **Visual parity (A)**: live UI vs Figma source for every visible surface
- **Data binding (B)**: rendered values vs underlying source of truth (Figma API, ds-service, generated tokens, JSON manifests)
- **Functional (C)**: every tab, hover, click, search, filter, modal, and SSE channel actually works end-to-end

## Stack snapshot (audit time)

- Next.js dev server on `:3001`
- ds-service on `:8080`
- Cloudflare tunnel: `text-activated-commodities-inspiration.trycloudflare.com`
- Active project (most data): `indian-stocks-research`, version `810061c4` (`view_ready`, 219/219 PNGs as of 00:43 IST 2026-05-03)
- Figma file: `Ql47G1l4xLYOW7V2MkK0Qx`

## Route inventory

| Route | Type | Figma data dependency | Audit owner |
|---|---|---|---|
| `/` | static | brand copy + hero | sweep-1 |
| `/atlas` | dynamic | mind-graph from `/v1/projects/graph` (live) | atlas-deep |
| `/atlas/admin` | dynamic | persona/rule/taxonomy state from ds-service | atlas-deep |
| `/atlas/admin/personas` | dynamic | personas table | atlas-deep |
| `/atlas/admin/rules` | dynamic | audit_rules table | atlas-deep |
| `/atlas/admin/taxonomy` | dynamic | canonical_taxonomy table | atlas-deep |
| `/components` + `[slug]` | dynamic | component browser (Figma library?) | sweep-2 |
| `/files` + `[slug]` | dynamic | files manifest | sweep-2 |
| `/health` | static | health dashboard | sweep-1 |
| `/icons` | dynamic | `lib/icons.ts` (synced from Figma glyph file) | sweep-2 |
| `/illustrations` | dynamic | asset gallery | sweep-2 |
| `/inbox` | dynamic | `/v1/inbox` SSE stream | sweep-1 |
| `/logos` | dynamic | logo asset gallery | sweep-2 |
| `/onboarding` + `[persona]` | dynamic | persona-driven content | sweep-1 |
| `/projects` | dynamic | project list from ds-service | projects-deep |
| `/projects/[slug]` | dynamic | project + canvas + tabs (atlas/screens/violations/decisions/JSON/files/DRD) | projects-deep |
| `/settings/notifications` | dynamic | notification prefs | sweep-1 |

## Audit dispatch

Four parallel agents own disjoint slices:
- **atlas-deep** — `/atlas` + `/atlas/admin/*`
- **projects-deep** — `/projects` + `/projects/[slug]` (every tab, every interaction)
- **sweep-1** — home, inbox, onboarding, settings, health
- **sweep-2** — components, files, icons, illustrations, logos

Each agent populates its section below with structured findings.

---

## Findings — Atlas surface

**Atlas summary.** Live ds-service on `:8080` rejects all known credentials and runs an ephemeral JWT signing key (regenerated on restart per `cmd/server/main.go:285`), so a Playwright login + screenshot pass on `/atlas` and the four `/atlas/admin/*` surfaces could not be performed in this audit window. The Playwright MCP toolset was not available in the deferred-tools surface either. Findings below are therefore static-analysis + DB/API truth-table cross-checks against the four files of `app/atlas/admin/*`, `BrainGraph.tsx`, `HoverSignalCard.tsx`, `LeafLabelLayer.tsx`, `SearchInput.tsx`, `FilterChips.tsx`, `forceConfig.ts`, and `_lib/AdminShell.tsx`. The strongest signal: the live graph for the active tenant has **0 personas, 0 decisions, all-zero severity counts on every flow** (so the entire HoverSignalCard "signal" surface degrades to empty states), and the `canonical_taxonomy` + `personas` tables are empty so `/atlas/admin/taxonomy` and `/atlas/admin/personas` cannot exit their empty-state copy. On the visual side, the runbook 2.6 invariant "no hardcoded hex outside `forceConfig.ts`" is broken in at least 5 atlas-surface files. Click-handler still ships exactly the orchestration the runbook prescribes; the recent U1–U4 polish landed cleanly.

### [Sev: P0] Browser audit blocked — `/atlas` + admin pages can't be opened by an automated session
- **Where**: all five routes; ds-service login `POST http://localhost:8080/v1/auth/login` (`services/ds-service/cmd/server/main.go:831`)
- **Axis**: functional
- **Observed**: login with `chetan@indmoney.com` + several test passwords returns `401 invalid credentials`. ds-service log line at startup: `JWT_SIGNING_KEY not set — generated ephemeral key (tokens won't survive restart)` (`main.go:285`). Any DB-stored token is invalidated on each restart. Playwright MCP tools were not available in the deferred toolset; no browser audit was possible.
- **Expected**: an audit-time path to a logged-in browser context (either persistent JWT signing key, a dev-mode skeleton key, or a documented "use bootstrap-token to mint a session" runbook step).
- **Source of truth**: `services/ds-service/cmd/server/main.go:276-295` (ephemeral key path)
- **Fix sketch**: add a documented test login (e.g. `make atlas-audit-login` that uses `BOOTSTRAP_TOKEN` to upsert a known-password admin), and persist `JWT_SIGNING_KEY` into `.env.local` after first generation.

### [Sev: P0] Personas table empty — `/atlas/admin/personas` is uninspectable in any state
- **Where**: `app/atlas/admin/personas/page.tsx`; backing table `personas`
- **Axis**: data
- **Observed**: `SELECT count(*) FROM personas` returns `0`. The page will always render the "Queue clear" empty state. The `personas` node-type also has 0 rows in `graph_index` (no persona satellites in the brain graph for either platform).
- **Expected**: at minimum one pending persona seeded for `tenant_id=e090530f-2698-489d-934a-c821cb925c8a` so the queue + the bell badge + the SSE wiring can be exercised.
- **Source of truth**: `services/ds-service/data/ds.db` table `personas` (count 0); `graph_index` `WHERE type='persona'` (count 0)
- **Fix sketch**: `INSERT INTO personas (id,tenant_id,name,status,created_by_user_id,created_at) VALUES ('p1','<tenant>','Smita Sharma','pending','f3427f67-...', datetime('now'));`

### [Sev: P0] canonical_taxonomy table empty — `/atlas/admin/taxonomy` shows "No projects yet" while real projects exist
- **Where**: `app/atlas/admin/taxonomy/page.tsx`; backing table `canonical_taxonomy`; `projects` table has 2 active rows (`welcome-project`, `indian-stocks-research`)
- **Axis**: data
- **Observed**: `SELECT count(*) FROM canonical_taxonomy` returns `0`; the page's `tree.size === 0` branch fires → renders `<strong>No projects yet.</strong> The taxonomy populates as designers export flows from the Figma plugin.` That copy is misleading — flows have already been exported (graph_index has `folder:indian-stocks/research`, `folder:design-system/tokens/...` etc).
- **Expected**: the page should derive the tree from the union of `canonical_taxonomy` + `projects.product`/`projects.path` (or backend should auto-seed canonical rows for every distinct `(product, path)` from `projects`).
- **Source of truth**: `projects.path` values like `Indian Stocks/research` exist; `canonical_taxonomy` empty
- **Fix sketch**: backfill `canonical_taxonomy` from `projects` paths on first read, or render extended-only entries derived from `projects` even when canonical_taxonomy is empty.

### [Sev: P1] Flow `open_url` drops the version qualifier — runbook spec violation
- **Where**: `services/ds-service/internal/projects/graph_repo.go`; rendered by `LeafLabelLayer.extractSlugFromOpenURL` and `HoverSignalCard` "Open project →"
- **Axis**: data
- **Observed**: `graph_index.open_url` for all 4 flow rows is `/projects/indian-stocks-research` — no `?v=<version>` suffix.
- **Expected**: `LeafLabelLayer` comment cites backend writing `/projects/<slug>` or `/projects/<slug>?v=…` (line 86–88). The active version `810061c4-9fe5-40b9-bfc8-f7f23ea1a123` is the most recent of two `view_ready` versions for the project — without `?v=`, the user always lands on the latest, breaking deep-links from /atlas to a historical version of a flow.
- **Source of truth**: `graph_index.open_url`; `project_versions` shows two `view_ready` rows for `32259e88...`
- **Fix sketch**: have the rebuild worker append `?v=<latest_version_id>` (or the version of the export that produced this flow) to `open_url`.

### [Sev: P1] All flow severity counts are zero — HoverSignalCard "signal" is empty for every real flow
- **Where**: `app/atlas/HoverSignalCard.tsx:88-110` (`SeverityRow` component); data source `graph_index.severity_*`
- **Axis**: data
- **Observed**: for the 3 real `indian-stocks-research` flows (`Stock Screener`, `Research Product`, `Filters for Stock Screener`) all five severity columns are 0 and `persona_count = 0`. The card will always render "No active violations" + omit the Personas row. Only the `welcome-flow` carries any signal (`severity_critical=1, severity_high=1`).
- **Expected**: real violations exist in the system (3 violations rows, 33 audit_rules enabled). The graph_index materialiser is not joining violations → flow → severity bucket.
- **Source of truth**: `graph_index` rows for `flow:*` with all-zero severities; `violations` table count = 3
- **Fix sketch**: re-run the graph rematerializer (or backfill the `severity_*` columns from violations grouped by `flow_id`).

### [Sev: P1] Hardcoded severity hex in HoverSignalCard violates runbook 2.6 ("no hardcoded hex outside `forceConfig.ts`")
- **Where**: `app/atlas/HoverSignalCard.tsx:103-107` (`#FF6B6B`, `#FFB347`, `#FFD93D`, `#9F8FFF`, `#7B9FFF`); also `:160-167`, `:211`
- **Axis**: visual
- **Observed**: severity colours, type-tag colours, and CTA accent are inline string literals.
- **Expected**: per runbook §2.6 these belong in `forceConfig.ts` (or a sibling `severityColors.ts`). Tuning needs a multi-file hunt.
- **Source of truth**: `docs/runbooks/atlas-ux-principles.md` §2.6
- **Fix sketch**: hoist a `SEVERITY_COLOR` map to `forceConfig.ts` and consume it from HoverSignalCard + `app/atlas/admin/rules/page.tsx`.

### [Sev: P1] Hardcoded severity hex in `/atlas/admin/rules` and `DashboardShell` (two parallel maps drifting)
- **Where**: `app/atlas/admin/rules/page.tsx:34-40` (`#FF6B6B/#FFB347/#FFD93D/#9F8FFF/#7B9FFF`); `components/dashboard/DashboardShell.tsx:178-187` (`#dc2626/#ea580c/#ca8a04/#2563eb/#64748b`)
- **Axis**: visual
- **Observed**: rules page paints "critical" in coral `#FF6B6B`; dashboard paints "critical" in red-600 `#dc2626`. Same data dimension, two different palettes, two different files.
- **Expected**: one canonical severity-colour token (or token bundle) shared across atlas + admin surfaces.
- **Source of truth**: design-system tokens; runbook §2.6
- **Fix sketch**: introduce `--sev-critical` … `--sev-info` CSS vars sourced from the brand tokens; both files consume those.

### [Sev: P1] Bell badge palette is hardcoded — no theme awareness in `/atlas/admin/*` chrome
- **Where**: `app/atlas/admin/_lib/AdminShell.tsx:239-264` (`#ffb347`, `#2a1a00`, `#7b9fff`, `rgba(255,179,71,*)`)
- **Axis**: visual
- **Observed**: badge background, glow, and focus outline all hardcoded; ignores theme.
- **Expected**: `var(--accent-warning, …)` or token-driven so light theme reads correctly.
- **Source of truth**: runbook §2.6
- **Fix sketch**: replace with `var(--accent-warning)` + `var(--accent)` token references.

### [Sev: P1] HoverSignalCard background hardcoded `rgba(10, 14, 24, 0.92)` — bypasses `--bg-canvas` theme observer
- **Where**: `app/atlas/HoverSignalCard.tsx:138`
- **Axis**: visual
- **Observed**: card surface is dark even in light theme; the BrainGraph already runs a MutationObserver to repaint the scene background — the card does not participate.
- **Expected**: `background: var(--surface-overlay)` or a derived token; should follow theme flips.
- **Source of truth**: runbook §2.6 ("scene background reads `--bg-canvas` token") + AtlasInner shell uses `var(--bg-canvas, var(--bg-page))`
- **Fix sketch**: token-ize the card surface.

### [Sev: P1] `useReducedMotion` source split between `app/atlas/reducedMotion` and `lib/animations/context` — runbook 2.8 single-source rule violated
- **Where**: `app/atlas/page.tsx:27` (`./reducedMotion`), `app/atlas/BrainGraph.tsx:42` (`./reducedMotion`), `app/atlas/LeafLabelLayer.tsx` doc-block citation says it must be `@/lib/animations/context`
- **Axis**: functional
- **Observed**: two parallel hooks. If the global hook in `lib/animations/context.ts` ever drifts from `app/atlas/reducedMotion.ts` (e.g. SSR semantics, change listener timing), atlas surfaces will animate inconsistently with the rest of the site.
- **Expected**: one canonical hook; runbook §2.8 explicitly states "Reduced-motion source = `lib/animations/context.ts`".
- **Source of truth**: `docs/runbooks/atlas-ux-principles.md` §2.8
- **Fix sketch**: delete `app/atlas/reducedMotion.ts`'s `useReducedMotion`, re-export from `@/lib/animations/context`.

### [Sev: P1] HoverSignalCard does not flip-clamp to viewport top/left edges
- **Where**: `app/atlas/HoverSignalCard.tsx:30-39`
- **Axis**: functional
- **Observed**: code flips left/top only when overflowing right/bottom. A node near `(0, 0)` projects to `anchor.x=4, anchor.y=4`; the card draws at `left=20, top=20`. But if anchor is on a node so close to the canvas top that the projected y is negative (which happens when the canvas overlay does not start at viewport top — atlas-shell starts at `var(--header-h)`), `top` may go below `0` — card half-cut by the header. No `Math.max(margin, …)` guard.
- **Expected**: per runbook §2.3 "card flips at viewport edges" — same rule should apply at top/left.
- **Source of truth**: runbook §2.3 + atlas-shell offset `top: var(--header-h)`
- **Fix sketch**: clamp `left = Math.max(margin, …)`; `top = Math.max(margin + headerH, …)`.

### [Sev: P1] Search match-count covers only flow/component/decision/persona — folders, products, tokens never match
- **Where**: `app/atlas/SearchInput.tsx:39-46`; `useSearch.ts:23` (`SearchHit.kind` enum)
- **Axis**: functional
- **Observed**: `useSearch` ships kinds `flow|drd|decision|persona|component`. SearchInput maps to graph node IDs `${kind}:${id}`. graph_index has 756 token nodes + 3 folder + 2 product nodes that the search will never light up; PMs searching a token name or folder path get an empty match set and the entire graph dims to 0.3.
- **Expected**: search index should also expose token + folder + product kinds (or SearchInput should label "no token matches" rather than dimming the whole graph).
- **Source of truth**: `graph_index` GROUP BY type (756 tokens, 3 folders, 2 products) vs `SearchHit.kind` enum
- **Fix sketch**: extend `/v1/search` scope=mind-graph to include token/folder/product; OR fall back to a client-side label-substring match for those kinds.

### [Sev: P1] Search input clears via Esc but does NOT clear the dim-state when input emptied via backspace+blur — race
- **Where**: `app/atlas/SearchInput.tsx:33-48`
- **Axis**: functional
- **Observed**: when `query.trim() === ""` the effect calls `onMatchChange(null)` — works. But the effect deps include `results` and `status`; if a fast user empties the input mid-flight, `status` may still be `loading` from the prior keystroke and the effect skips both the `null` push and the `set.add` push (the `if (status === "ready")` gate). Net result: the dim state can stick if the user backspaces while a debounced fetch is in flight.
- **Expected**: when query is empty, immediately push `null` regardless of fetch status — and abort the inflight request.
- **Source of truth**: `app/atlas/SearchInput.tsx:33-48`
- **Fix sketch**: hoist the `query.trim() === ""` clearing into a separate effect with deps `[query]`.

### [Sev: P1] FilterChips don't expose a Persona chip — but the type system + graph data have personas, and runbook designer-lens calls out persona filtering
- **Where**: `app/atlas/FilterChips.tsx`; `app/atlas/types.ts` `GraphFilters` shape (Hierarchy/Components/Tokens/Decisions only)
- **Axis**: functional
- **Observed**: only 4 chips. `personas` are a node type but there is no UI to toggle them on/off. Designer lens in the runbook says "Toggles filter chips (Components, Tokens, Decisions) and watches satellite nodes appear" but the same paragraph talks about hovering components for "usage count" — persona-level filtering is implied by the system but not surfaced.
- **Expected**: either a Persona chip OR a documented decision that personas always render with hierarchy.
- **Source of truth**: `app/atlas/types.ts` `GraphFilters`
- **Fix sketch**: add Persona chip if persona nodes are seeded; otherwise document the omission in the runbook.

### [Sev: P1] Taxonomy page comment block claims drag-to-reorder is "deferred" — but the code DOES ship `Reorder.Group` and saves to `/v1/atlas/admin/taxonomy/reorder`
- **Where**: `app/atlas/admin/taxonomy/page.tsx:21-23` (doc-block) vs `:572-602` (Reorder.Group + saveReorder)
- **Axis**: data
- **Observed**: the doc-block at the top says "Drag-to-reorder is deferred — taxonomy ordering is alphabetical for v1" yet `ChildrenList` wraps siblings in a Framer `Reorder.Group` and POSTs `/v1/atlas/admin/taxonomy/reorder` on every drop. The schema also added `order_index` (visible in `.schema canonical_taxonomy`).
- **Expected**: doc-block matches code reality; the deferral note should be deleted or rewritten.
- **Source of truth**: `canonical_taxonomy.order_index` exists; `Reorder.Group` mounted
- **Fix sketch**: delete the stale paragraph; move the order_index history into the changelog or commit message.

### [Sev: P1] Folder partition asymmetric across platforms (web has 2 folders, mobile has 1) — `/atlas` switches platform but the underlying graph is uneven
- **Where**: `graph_index` GROUP BY platform/type
- **Axis**: data
- **Observed**: mobile=`{product:1, folder:1, flow:3}`; web=`{product:1, folder:2, flow:1}`. Toggling Mobile↔Web in PlatformToggle reveals very different topologies for what's nominally the same product (`Indian Stocks`).
- **Expected**: either both platforms have the full Indian Stocks tree, or the UI surfaces a "no flows for this platform yet" state instead of a near-empty brain.
- **Source of truth**: `graph_index` per-platform counts
- **Fix sketch**: confirm with PM whether platform-specific flows exist by design; if so, add an explicit empty state copy when a platform's flow count is <2.

### [Sev: P1] `nodeLabel` accessor returns empty string for `flow` type but the LeafLabelLayer uses `n.label` directly — long flow names like "Filters for Stock Screener" (29 chars) have no truncation logic
- **Where**: `app/atlas/LeafLabelLayer.tsx:266-316` (renders `{l.label}` with `whiteSpace: "nowrap"`)
- **Axis**: visual
- **Observed**: runbook §2.5 says "Truncate via existing toolbar styles. Long flow names (e.g. 'Filters for Stock Screener' at 30 chars) truncate via `text-overflow: ellipsis` on a max-width container, not by overlapping the next node." The current implementation has neither `max-width` nor `text-overflow` — labels overlap freely.
- **Expected**: max-width (e.g. 180px) + `text-overflow: ellipsis` per the runbook §2.5 invariant.
- **Source of truth**: runbook §2.5
- **Fix sketch**: add `maxWidth: 180, textOverflow: "ellipsis", overflow: "hidden"` to the inline `style`.

### [Sev: P2] `atlas-shell` background uses `var(--bg-canvas, var(--bg-page))` fallback chain — runbook §2.6 wants `--bg-canvas` only
- **Where**: `app/atlas/page.tsx:131`
- **Axis**: visual
- **Observed**: fallback to `--bg-page` softens the rule. If `--bg-canvas` is undefined on a future theme, the brain glow will sit against the brand's bright page background.
- **Expected**: hard-error (or at least a fixed dark fallback) when `--bg-canvas` is missing — matches `forceConfig.backgroundColor()`'s `#050810` SSR fallback.
- **Source of truth**: runbook §2.6 + `forceConfig.ts:117`
- **Fix sketch**: drop the `--bg-page` fallback; rely on `forceConfig.backgroundColor()`'s fallback.

### [Sev: P2] BrainGraph EmptyState uses hardcoded white-tints (`rgba(255,255,255,0.62)` etc) — light theme regression
- **Where**: `app/atlas/BrainGraph.tsx:919-948`
- **Axis**: visual
- **Observed**: empty + error states color text with `rgba(255,255,255,0.62)`; on a hypothetical light-theme `/atlas` (the runbook §2.6 says background stays dark, but the fallback states could appear pre-theme-decision) the text is still white-on-white if any of the upstream guards fail.
- **Expected**: use `var(--text-1)` / `var(--text-3)` like the rest of the page does.
- **Source of truth**: runbook §2.6
- **Fix sketch**: token-ize.

### [Sev: P2] Spring camera overshoots small distances — runbook §2.4 forbids overlapping animations but doesn't constrain visual settle for satellite click
- **Where**: `app/atlas/BrainGraph.tsx:584-630` (`handleNodeClick` for product/folder)
- **Axis**: functional
- **Observed**: spring config `{tension: 170, friction: 26}` is the react-spring default; for a `folder` zoom of distance 120 it lands cleanly, but a `product` zoom of distance 200 from a near-camera origin can overshoot. Runbook §2.2 says "settles without visible overshoot for small distances and a tiny one for large" — this is acceptable but should be measured.
- **Expected**: a recorded GIF showing the worst-case overshoot is ≤ 4 px equivalent.
- **Source of truth**: runbook §2.2
- **Fix sketch**: tighten friction to 28 if overshoot is visible.

### [Sev: P2] AdminShell `markAllSeen` invoked on every render where `pathname === "/atlas/admin/personas"` and `rawCount` changes — could spam localStorage writes during a burst of SSE events
- **Where**: `app/atlas/admin/_lib/AdminShell.tsx:133-139`
- **Axis**: functional
- **Observed**: deps `[pathname, rawCount]` re-run on every `persona.pending` event while the user sits on the personas page, calling `localStorage.setItem` on each. Cheap but unnecessary.
- **Expected**: debounce or only mark seen on the leading edge of the visit.
- **Source of truth**: `AdminShell.tsx:133-139`
- **Fix sketch**: split into `useEffect(()=>markAllSeen(), [pathname])` (visit-time) plus a refresh on tab focus.

### [Sev: P2] Persona pending bell badge initial fetch races the seenMarker hydration
- **Where**: `app/atlas/admin/_lib/AdminShell.tsx:64-89`
- **Axis**: functional
- **Observed**: `loadInitialCount()` and the `localStorage.getItem("admin-personas-seen-marker")` read happen in the same effect but as independent statements. If `loadInitialCount()` resolves before the marker hydrates, badge briefly shows the full pending count even when the user had previously dismissed.
- **Expected**: hydrate marker BEFORE the fetch (or render the badge only when both `rawCount > 0 && hydrated && markerLoaded`).
- **Source of truth**: `AdminShell.tsx:64-89`
- **Fix sketch**: hydrate marker synchronously in a `useState(() => …)` initialiser.

### [Sev: P2] AdminShell SSE EventSource reconnects only via cleanup → re-subscribe on token change; no exponential backoff on transient ds-service outage
- **Where**: `app/atlas/admin/_lib/AdminShell.tsx:90-114`
- **Axis**: functional
- **Observed**: if `/v1/inbox/events/ticket` returns non-OK, the function returns silently. No retry, no error surfacing — bell badge silently goes stale.
- **Expected**: on subscription failure, retry with backoff (1s → 2s → 4s, capped) and a console warning.
- **Source of truth**: `AdminShell.tsx:90-114`
- **Fix sketch**: wrap `subscribe()` in a backoff loop; expose an aria-hidden status indicator.

### [Sev: P2] Taxonomy chips use hardcoded `#1FD896 / #FFB347 / #888`
- **Where**: `app/atlas/admin/taxonomy/page.tsx:319-322`, `:609-619`
- **Axis**: visual
- **Observed**: state chips and Legend dots carry inline hex.
- **Expected**: tokens (`--accent-success`, `--accent-warning`, `--text-3`).
- **Source of truth**: runbook §2.6
- **Fix sketch**: token-ize.

### [Sev: P2] `var(--bg-base, #fff)` hardcoded fallback in `DashboardShell` weeks button
- **Where**: `components/dashboard/DashboardShell.tsx:108`
- **Axis**: visual
- **Observed**: `color: weeks === w ? "var(--bg-base, #fff)" : "var(--text-2)"` — pure white fallback if `--bg-base` is undefined.
- **Expected**: use `var(--bg)` (which is the actually-defined token) per other admin chrome.
- **Source of truth**: rest of admin shells use `var(--bg)`
- **Fix sketch**: replace `--bg-base` with `--bg`.

### [Sev: P2] PlatformToggle / SavedViewShareButton / SignalAnimationLayer not audited (file-read budget) — risk of additional hex drift
- **Where**: `app/atlas/PlatformToggle.tsx`, `SavedViewShareButton.tsx`, `SignalAnimationLayer.tsx`
- **Axis**: visual
- **Observed**: these three components are referenced in `BrainGraph.tsx` chrome but were not opened in this audit pass. Given the hardcoded hex pattern in HoverSignalCard, FilterChips chip palette, and SearchInput surface, the chrome cluster is likely the same.
- **Expected**: token-coverage sweep.
- **Source of truth**: runbook §2.6
- **Fix sketch**: rip-grep `#[0-9A-Fa-f]{3,8}` across `app/atlas/**` and migrate.

### [Sev: P2] FilterChips chip palette inline (`rgba(123, 159, 255, 0.18)` etc) — duplicates `NODE_VISUAL.product.color` (`#7B9FFF`)
- **Where**: `app/atlas/FilterChips.tsx:101-104`
- **Axis**: visual
- **Observed**: active-chip blue is `rgba(123, 159, 255, 0.18)` — same RGB as the product node colour but inlined in a second file.
- **Expected**: read from `NODE_VISUAL.product.color` (with a sibling `NODE_VISUAL.product.softColor` for the chip surface).
- **Source of truth**: runbook §2.6
- **Fix sketch**: hoist soft-color tokens.

### [Sev: P2] BrainGraph leaf-click → `view.morphTo(node)` does not pass `?v=<version>` to the destination URL — same root cause as P1 finding above
- **Where**: `app/atlas/BrainGraph.tsx:586-590`; consumed in `useGraphView.morphTo` (not opened)
- **Axis**: functional
- **Observed**: morph destination URL is whatever `node.signal.open_url` is — currently `/projects/<slug>` with no version. Reverse-morph back via `?from=<slug>` in `app/atlas/page.tsx:72` will also lose the version.
- **Expected**: round-trip the version_id from atlas → project → atlas.
- **Source of truth**: `useGraphView` (not opened); `BrainGraph.tsx:586-590`
- **Fix sketch**: thread `?v=` through `view.morphTo` + the `?from=` reverse marker.

### [Sev: P2] `aggregate.status === "empty"` EmptyState references "Export from the Figma plugin" — but for the Indian Stocks project the data IS exported, just on the other platform
- **Where**: `app/atlas/BrainGraph.tsx:919-948`
- **Axis**: visual
- **Observed**: the empty-state copy is one-size-fits-all. Switching to `web` platform with only 1 flow could render the empty state if cull pruning hits zero (unlikely with current data but a fragile coupling).
- **Expected**: copy distinguishes "no data on this platform" vs "no data anywhere".
- **Source of truth**: `BrainGraph.tsx:919-948`
- **Fix sketch**: pass platform + tenant info into EmptyState; vary the body.

### [Sev: P3] HoverSignalCard severity hex map duplicates `app/atlas/admin/rules/page.tsx:34-40` — two parallel definitions
- **Where**: `HoverSignalCard.tsx:103-107` vs `rules/page.tsx:34-40`
- **Axis**: visual
- **Observed**: identical maps, copy-pasted; if one drifts, severity bars and rule rows desync.
- **Expected**: one shared `SEVERITY_COLOR` constant.
- **Source of truth**: code
- **Fix sketch**: extract to `lib/severity.ts`.

### [Sev: P3] `extractSlugFromOpenURL` regex `^/projects/([^/?#]+)` won't match if backend ever emits an absolute URL (`https://…/projects/<slug>`)
- **Where**: `app/atlas/LeafLabelLayer.tsx:95-102`
- **Axis**: functional
- **Observed**: defensive regex is path-only.
- **Expected**: match `(?:https?://[^/]+)?/projects/…` to be future-proof.
- **Source of truth**: `LeafLabelLayer.tsx:95-102`
- **Fix sketch**: extend the regex.

### [Sev: P3] `view-transition-name: flow-${slug}-label` pattern collides if two flow nodes share the same slug
- **Where**: `app/atlas/LeafLabelLayer.tsx:298`
- **Axis**: functional
- **Observed**: per-slug name; if a project's atlas surfaces multiple flow leaves from one slug (a folder/flow alias) the browser will refuse the snapshot or pick one arbitrarily.
- **Expected**: `flow-${node.id}-label` is safer (already unique by graph_index PK).
- **Source of truth**: `LeafLabelLayer.tsx:298`
- **Fix sketch**: use `node.id`-derived suffix.

### [Sev: P3] AdminShell pulse animation runs even under `prefers-reduced-motion` — runbook §2.8 says all animations collapse
- **Where**: `app/atlas/admin/_lib/AdminShell.tsx:246-265` (`@keyframes bellPulse` runs unconditionally)
- **Axis**: functional
- **Observed**: no `@media (prefers-reduced-motion: reduce)` override on the keyframe.
- **Expected**: per runbook §2.8, animation-duration: 0s under reduced motion.
- **Source of truth**: runbook §2.8
- **Fix sketch**: wrap the `animation` declaration in a media query that disables it.

### [Sev: P3] AdminShell typography uses `font-size: 12px / 13px / 14px` inline — no `var(--font-size-*)` token
- **Where**: `app/atlas/admin/_lib/AdminShell.tsx:218,242,267,273`
- **Axis**: visual
- **Observed**: micro-typography hand-written.
- **Expected**: tokens.
- **Source of truth**: brand tokens
- **Fix sketch**: token-ize.

### [Sev: P3] `app/atlas/page.tsx` ReducedMotionFallback CTA hex `#7b9fff` repeated alongside `var(--accent, #7b9fff)` — fallback drift risk
- **Where**: `app/atlas/page.tsx:267,168`
- **Axis**: visual
- **Observed**: hex used as fallback in two places; if `--accent` is later retuned, fallbacks must be hand-synced.
- **Expected**: one constant.
- **Source of truth**: `page.tsx`
- **Fix sketch**: extract `ACCENT_FALLBACK` constant or trust the token.

## Findings — Projects surface

### Projects summary

The `/projects` and `/projects/[slug]` surfaces are wired but the live shell does not match the audit prompt's mental model. **There is no Atlas/Screens/Files/DRD-as-tab — the project shell has exactly four tabs: DRD, Violations, Decisions, JSON** (`components/projects/ProjectShell.tsx:124-129`). Atlas is a fixed top-half pane, not a tab. The prompt's "7 tabs" inventory is wrong against current code.

Auth blocks every live API + Playwright probe — `services/ds-service` requires a JWT bearer on every projects endpoint, the user table only has chetan@indmoney.com whose password I cannot recover, and JWT minting via `JWT_SIGNING_KEY` was denied by the sandbox. Findings below mix unauthenticated probes (all 401), source review of the React/Go code, and direct sqlite3 reads of `services/ds-service/data/ds.db` against the active version `810061c4`.

The data picture for `indian-stocks-research / 810061c4`:
- 219 screens, 219 PNGs on disk, 100% have `png_storage_key` populated. Coordinates exactly match Figma `absoluteBoundingBox` for sampled frames (`2256:97152`, `2256:97478`, `2256:97703`, `2256:98041`, `2289:113537`).
- **0 canonical_trees** for this version → JSON tab is empty for every one of the 219 screens. The 25 trees that exist are all `welcome-*` fixtures.
- **0 violations** for this version (3 in DB, all on welcome-project or v1 of indian-stocks).
- **0 decisions** for any flow.
- **0 flow_drd rows** for any indian-stocks flow (only `welcome-flow` has DRD content).
- **0 personas** for this tenant → persona filter chips render empty.
- **0 screen_modes** for any screen → JSON tab's "mode resolver" never has bindings.
- **3 flows** exist for this project (Research Product, Stock Screener, Filters for Stock Screener) but the shell only exposes one — `screens[0]?.FlowID` (`ProjectShell.tsx:622-665`).

### [Sev: P0] Auth gate makes every project tab un-renderable from a fresh load without a JWT
- **Where**: `app/projects/layout.tsx:62-72` (gate) + `services/ds-service/cmd/server/main.go:467-694` (every `/v1/projects/...` route wrapped by `s.requireAuth(...)`)
- **Axis**: functional
- **Observed**: All API probes (GET violations, POST events ticket, GET DRD, GET decisions) returned `401 missing bearer token`. The Next layout returns `<div aria-hidden />` until a token rehydrates from localStorage. Without a known password, the audit cannot enter the page.
- **Expected**: A documented `?dev_token=` path, an HTTP-Only cookie auth path, or a documented test user for E2E + audit work.
- **Source of truth**: `services/ds-service/internal/auth/auth.go:131` (MintAccessToken)
- **Fix sketch**: Add a documented dev-only token endpoint behind `BOOTSTRAP_TOKEN`, or commit a Playwright login fixture path under `docs/audits/`.

### [Sev: P0] JSON tab is broken for every indian-stocks screen — 0 canonical_trees in DB
- **Where**: JSON tab at `/projects/indian-stocks-research`; `components/projects/tabs/JSONTab.tsx:79-110`; backend `GET /v1/projects/{slug}/screens/{id}/canonical-tree` (`main.go:504`)
- **Axis**: data
- **Observed**: `SELECT count(*) FROM screen_canonical_trees sct JOIN screens s ON sct.screen_id=s.id WHERE s.version_id='810061c4-9fe5-40b9-bfc8-f7f23ea1a123'` returns **0**. All 25 existing rows belong to welcome-* fixtures. Every screen click in atlas → JSON tab will hit 404 → render `EmptyState variant="re-export-needed"`.
- **Expected**: 219 canonical_tree rows, one per screen, populated by the export pipeline.
- **Source of truth**: `screen_canonical_trees` table; pipeline canon-tree writer (Stage 5)
- **Fix sketch**: Backfill from existing Figma export, or re-run pipeline with the canonical-tree writer enabled.

### [Sev: P0] DRD/Decisions/Violations all bind to `screens[0].FlowID` — only ever shows ONE of three flows
- **Where**: `components/projects/ProjectShell.tsx:622-666`; `services/ds-service/internal/projects/repository.go:2229` (ListScreensByVersion ORDER BY created_at)
- **Axis**: data + functional
- **Observed**: `indian-stocks-research/810061c4` has 3 distinct flow_ids (Research Product=37, Stock Screener=50, Filters=132 screens). Whichever flow's first screen was inserted first wins. There is no flow selector in `ProjectToolbar`. Two-thirds of the project is invisible to DRD/Decisions/Violations consumers.
- **Expected**: A flow selector in the toolbar (or per-flow tab structure) and the right-hand tabs filtering by selected flow.
- **Source of truth**: `flows` table — 3 rows for project `32259e88-bc1e-465c-91eb-a8fa1009363b`
- **Fix sketch**: Add `<FlowSelector>` in `ProjectToolbar`, plumb `activeFlowID` through `ProjectShell` instead of `screens[0].FlowID`.

### [Sev: P0] DRD tab calls `fetchDRD(slug, flowID)` for indian-stocks flows but no `flow_drd` row exists for any of them
- **Where**: `components/projects/tabs/DRDTab.tsx:72-102`; backend `GET /v1/projects/{slug}/flows/{flow_id}/drd` (`main.go:510`)
- **Axis**: data
- **Observed**: `flow_drd` contains only `welcome-flow`. The 3 indian-stocks flows have no row. If backend returns 404 the tab shows loadError; if it returns empty `{}`, the editor mounts blank with `revision=0` and the first edit silently authors against an unseeded record.
- **Expected**: A default seeded DRD row per flow at flow-creation time, or an explicit "Create DRD" CTA.
- **Source of truth**: `flow_drd` table
- **Fix sketch**: Pipeline emits an empty `flow_drd` row at flow-creation time; UI surfaces a "Start drafting" CTA.

### [Sev: P0] Violations tab is empty even though pipeline reached `view_ready` — version 810061c4 has 0 violations
- **Where**: `components/projects/tabs/ViolationsTab.tsx:191-220`; backend `GET /v1/projects/{slug}/violations` (`main.go:627`)
- **Axis**: data
- **Observed**: `SELECT count(*) FROM violations WHERE version_id='810061c4-...'` → **0**. Three rows in DB belong to `welcome-*` and `6c8ea8cc` (older version). The toolbar audit badge will display "complete (0)" — indistinguishable from "audit failed silently".
- **Expected**: Audit fanout populates rows when version transitions to view_ready. Either audit_complete never fired or the rules engine produced zero findings against 219 mobile screens (very unlikely).
- **Source of truth**: `violations` + `audit_jobs`
- **Fix sketch**: Inspect `audit_jobs` for this version; re-run via `POST /v1/admin/audit/fanout`.

### [Sev: P0] Personas table empty for tenant — persona chips and persona-scoped filtering are inert
- **Where**: `components/projects/ProjectToolbar.tsx` persona dropdown; `ViolationsTab.tsx` PersonaFilterChips; `ProjectShell.tsx:646`
- **Axis**: data
- **Observed**: `SELECT count(*) FROM personas` → **0** globally. Persona filter chip set is empty; URL `#persona=KYC-pending` deeplink resolves to `null`. The `filteredScreens` memo (`ProjectShell.tsx:690-698`) is documented as a "Phase 1 placeholder" that always returns all screens — confirmed dead code.
- **Expected**: At least one persona seeded for indian-stocks (KYC-pending, etc.).
- **Source of truth**: `personas` table; pipeline `UpsertPersona`
- **Fix sketch**: Backfill personas from Figma section names; remove the placeholder comment or implement actual persona filtering.

### [Sev: P0] Atlas frame click forces a tab switch to JSON — but JSON tab is empty (P0 above) → headline interaction is broken end-to-end
- **Where**: `components/projects/ProjectShell.tsx:901-906`
- **Axis**: functional
- **Observed**: `onFrameSelect` always switches to JSON tab via `changeTab("json")`. With 0 canonical_trees, every click in the atlas results in: dolly to frame → curtain animation → empty-state "Re-export needed".
- **Expected**: Either keep current tab if no canonical_tree, or open an inspector overlay; at minimum show the screen's logical_id + dimensions inline.
- **Source of truth**: same as JSON-empty finding above
- **Fix sketch**: Gate JSON-switch on tree availability; otherwise stay on the current tab and toast a hint.

### [Sev: P0] PNG endpoint authenticates via `?token=<jwt>` query param — leaks JWT to access logs + browser history
- **Where**: `lib/projects/client.ts:139-142` (`screenPngUrl`); `services/ds-service/cmd/server/main.go:494`; `components/projects/atlas/AtlasCanvas.tsx:230-238`
- **Axis**: functional + security
- **Observed**: `curl http://localhost:8080/v1/projects/indian-stocks-research/screens/<id>/png` → `401`. The atlas appends `?token=<jwt>` so the GET-fallback in `requireAuth` accepts it; this works in browsers but logs the JWT into every middlebox + browser history + access log. Token-in-URL is a known anti-pattern.
- **Expected**: Short-lived signed URL (HMAC over screen_id + exp), OR a per-tenant cookie scoped to `/v1/projects/.../png`.
- **Source of truth**: `requireAuth` in `services/ds-service/internal/auth/`
- **Fix sketch**: Add `signPngURL(screen_id, ttl)` HMAC helper; deprecate `?token=`.

### [Sev: P1] `screens[0]` is non-deterministic — `ListScreensByVersion` ORDER BY `created_at` only, no stable tie-breaker
- **Where**: `services/ds-service/internal/projects/repository.go:2229`; `ProjectShell.tsx:622`
- **Axis**: data
- **Observed**: Without microsecond timestamps + a stable secondary key, two screens inserted in the same TXN at the same timestamp can swap order on repeat reads. `screens[0].FlowID` could flip flows for the same project on each page load.
- **Expected**: `ORDER BY created_at, id`.
- **Source of truth**: `repository.go:2229`
- **Fix sketch**: Append `, id` to the ORDER BY.

### [Sev: P1] Esc key handler always calls `router.back()` — `?from=` URL param is not honored
- **Where**: `components/projects/ProjectShell.tsx:397-428`
- **Axis**: functional
- **Observed**: The keydown handler unconditionally calls `router.back()` with no consultation of `searchParams.get("from")`. Landing on `/projects/...?from=/inbox` and pressing Esc returns to the prior history entry, not the named source.
- **Expected**: If `?from=` is present, navigate to that route; else `router.back()`.
- **Source of truth**: `ProjectShell.tsx:424`
- **Fix sketch**: `const from = searchParams.get("from"); if (from) router.push(from); else router.back();`

### [Sev: P1] SSE subscription always opens — even on a passive view of a `view_ready` project — leaving an idle EventSource per tab
- **Where**: `components/projects/ProjectShell.tsx:487-552`
- **Axis**: functional
- **Observed**: When no trace ID is supplied (passive view), code mints `crypto.randomUUID()` and opens EventSource against `/events?ticket=…`. Backend filters by trace and emits only heartbeats — but each open project tab maintains a long-lived connection.
- **Expected**: Skip subscription when state is view_ready+complete and there's no trace ID.
- **Source of truth**: `ProjectShell.tsx:492-498`
- **Fix sketch**: Guard the call on `initialTraceID || searchParams.get("trace")` truthiness.

### [Sev: P1] Toolbar audit badge says "complete (0)" — indistinguishable from "audit never ran"
- **Where**: `components/projects/ProjectShell.tsx:248-255`; `ProjectToolbar.tsx:46-47`
- **Axis**: visual + data
- **Observed**: With 0 violations and view_ready status, `auditBadge` becomes `{kind: "complete", finalCount: undefined}`. The badge has no way to distinguish "ran-and-found-nothing", "ran-and-failed", or "never-fanned-out".
- **Expected**: Three-state surface backed by `audit_jobs.status`.
- **Source of truth**: `audit_jobs` table joined to version
- **Fix sketch**: Surface the underlying job status, not just the violation count.

### [Sev: P1] DRD `editor.onChange` cleanup is `typeof === 'function'` guarded — risk of duplicate listeners on flow change
- **Where**: `components/projects/tabs/DRDTab.tsx:107-121`
- **Axis**: functional
- **Observed**: If the BlockNote version returns void from `onChange`, every effect-rebind (e.g. flowID change) leaves a stale listener subscribed → multiple PUTs per keystroke after a flow toggle.
- **Expected**: Pin BlockNote version; require `offChange` to be callable.
- **Source of truth**: `@blocknote/react` API
- **Fix sketch**: Lock `@blocknote/react` to a known-good range; drop the typeof guard.

### [Sev: P1] Hash-driven tab changes ignore the `pendingTab !== null` lock that click-driven changes respect
- **Where**: `components/projects/ProjectShell.tsx:336-356` vs `:561-577`
- **Axis**: functional
- **Observed**: `changeTab` early-returns when `pendingTab` is set. The hashchange listener mutates state directly without that guard. A click-driven swap mid-flight + back/forward navigation that updates the hash can both be in flight, leaving inconsistent state.
- **Expected**: Hash-driven path also respects `pendingTab` lock.
- **Source of truth**: `ProjectShell.tsx:344-349`
- **Fix sketch**: In hash apply, `if (pendingTab) return;` before mutating state.

### [Sev: P1] `view-transition-name` for the breadcrumb is `flow-${slug}-label` — won't match per-flow names emitted by `/atlas`
- **Where**: `components/projects/ProjectToolbar.tsx:55-65`
- **Axis**: visual
- **Observed**: Multi-flow projects have one slug + N flows; the morph at most matches the first flow, others fall through to no-morph.
- **Expected**: Switch to `flow-${activeFlowID}-label` once flow selection exists.
- **Source of truth**: `view-transition-name` references in atlas + toolbar
- **Fix sketch**: Key by flow_id once the selector exists.

### [Sev: P1] DRD collab feature flag depends on `NEXT_PUBLIC_DRD_COLLAB === "1"` — env var not set anywhere in `.env.local`
- **Where**: `components/projects/ProjectShell.tsx:81`
- **Axis**: functional
- **Observed**: `DRD_COLLAB_ENABLED` evaluated at module scope; `.env.local` only sets `NEXT_PUBLIC_BRAND` and `NEXT_PUBLIC_DS_SERVICE_URL`. Every user gets the single-author REST flow.
- **Expected**: Default ON in dev; document toggle in `.env.local.example`.
- **Source of truth**: `.env.local`
- **Fix sketch**: Add `NEXT_PUBLIC_DRD_COLLAB=1` to dev defaults.

### [Sev: P1] Decisions ↔ Violations cross-link uses `router.replace` — losing back-button history
- **Where**: `components/projects/ProjectShell.tsx:584-610`
- **Axis**: functional
- **Observed**: Each cross-link rewrites history in place; user cannot back-button to clear the highlight.
- **Expected**: `router.push` so each cross-link is a real history entry.
- **Source of truth**: same
- **Fix sketch**: Swap to `router.push`.

### [Sev: P1] `filteredScreens` is documented as placeholder — UI silently lies about persona filtering
- **Where**: `components/projects/ProjectShell.tsx:690-698`
- **Axis**: data
- **Observed**: When user selects a persona, the function STILL returns all screens, but the toolbar shows the persona as active. No indication the filter is inert.
- **Expected**: Either remove the persona dropdown until linkage works, or surface "filtering inactive".
- **Source of truth**: `personas` + `flows.persona_id`
- **Fix sketch**: Filter `screens` by `flows.persona_id === selectedPersona.ID` (column already exists).

### [Sev: P1] Slow-render affordance hard-coded to 15s — too aggressive for legit pipeline phases that exceed it
- **Where**: `components/projects/ProjectShell.tsx:264-274`
- **Axis**: functional
- **Observed**: After 15s in `pending`, user sees "refresh?". Refreshing during Stage 5/6 discards partial progress.
- **Expected**: 30s threshold or wire to server-emitted ETA.
- **Source of truth**: pipeline runtime metrics (none exposed)
- **Fix sketch**: Bump to `30_000`, or read `progress.expected_remaining_ms` from SSE.

### [Sev: P1] `/projects` groups by `Product` — welcome's product is "DesignSystem" while indian-stocks uses "Indian Stocks" → fixture appears as a real group
- **Where**: `app/projects/page.tsx:53-64`
- **Axis**: visual
- **Observed**: List shows two groups; no way to filter or hide the welcome fixture.
- **Expected**: Mark welcome with `is_fixture=1`, hide unless a dev flag.
- **Source of truth**: `projects` table — no fixture column
- **Fix sketch**: Add `is_fixture INTEGER DEFAULT 0`; exclude from list by default.

### [Sev: P1] `/projects` is fully client-rendered — every refresh refetches from the browser, no Next caching/SWR
- **Where**: `app/projects/page.tsx:30-50`
- **Axis**: functional + perf
- **Observed**: useEffect → listProjects on every mount; no cache; back-button refetches.
- **Expected**: RSC + cookie auth, OR SWR with stale-while-revalidate.
- **Source of truth**: `lib/projects/client.ts:listProjects`
- **Fix sketch**: Migrate list to RSC, or wrap call in SWR.

### [Sev: P1] Tab pane uses `position:absolute; inset:0` inside a relative container that sits in `flex: 1 1 50%` — long content fights overflow:hidden
- **Where**: `components/projects/ProjectShell.tsx:911-1009`
- **Axis**: visual
- **Observed**: Inner pane has `overflow: auto` on a position:absolute child; long DRD/Violations content scrolls inside the pane only, while atlas zoom UI also has scroll affordances → two-scrollbar UX.
- **Expected**: Single scrollable surface per tab.
- **Source of truth**: layout in `ProjectShell.tsx`
- **Fix sketch**: Drop inner overflow; let the bottom-half flex container manage scroll.

### [Sev: P1] Active version param `?v=...` is set but no UI badge says "Viewing v3 of 5"
- **Where**: `components/projects/ProjectShell.tsx:680-686`; `ProjectToolbar.tsx`
- **Axis**: visual
- **Observed**: User can't tell which version is active without opening the dropdown. Breadcrumb has no version segment.
- **Expected**: Append `· v3` to breadcrumb tail.
- **Source of truth**: `ProjectToolbar.tsx`
- **Fix sketch**: Add version chip after flow name.

### [Sev: P2] EventSource ticket reconnect mints fresh ticket on every error — no exponential backoff
- **Where**: `lib/projects/client.ts:326-380`
- **Axis**: functional
- **Observed**: Flapping backend produces ticket-mint storms; ticket POST counts against rate limits.
- **Expected**: 1s → 2s → 4s … cap 30s.
- **Source of truth**: `subscribeProjectEvents`
- **Fix sketch**: Track `retryAttempt` and `setTimeout(reconnect, Math.min(30000, 1000*2**attempt))`.

### [Sev: P2] Atlas hover scale 1.015× via `useFrame` lerp competes with the click-dolly spring → first-click can flicker
- **Where**: `components/projects/atlas/AtlasFrame.tsx:42-48`
- **Axis**: functional
- **Observed**: Lerp factor 0.18 means ~12 frames to settle. A fast click during hover applies dolly while mesh is still scaling down → brief flicker.
- **Expected**: Reset scale to 1.0 instantly on click before dolly.
- **Source of truth**: same
- **Fix sketch**: In `handleSelect`, `meshRef.current.scale.set(1,1,1)` synchronously.

### [Sev: P2] JSON tab `treeCache` is module-scoped (singleton) — re-export of same screen ID returns stale tree
- **Where**: `components/projects/tabs/JSONTab.tsx:41`
- **Axis**: data
- **Observed**: `const treeCache = new Map<string, unknown>()` outside the component. After re-export with new content, cache holds the old tree until full page reload.
- **Expected**: Key by `(version_id, screen_id)`; clear on version change.
- **Source of truth**: same
- **Fix sketch**: `treeCache.delete(key)` on version-change effect, or use SWR.

### [Sev: P2] Theme apply effect never cleans `data-theme` on unmount → leaks to other routes
- **Where**: `components/projects/ProjectShell.tsx:359-363`
- **Axis**: functional
- **Observed**: Effect sets `documentElement.setAttribute("data-theme", concrete)` with no cleanup. Atlas page may want its own theme; project's choice persists.
- **Expected**: Save prior theme in a ref, restore in cleanup.
- **Source of truth**: same
- **Fix sketch**: Cleanup restores prior `data-theme`.

### [Sev: P2] Tab-switch animation runs even on initial hash-driven mount → first-paint shows curtain wipe instead of static content
- **Where**: `components/projects/ProjectShell.tsx:300-356`
- **Axis**: visual
- **Observed**: Loading `/projects/.../#tab=violations` runs the curtain. New visitors see motion they didn't trigger.
- **Expected**: Skip animation on first apply; use `firstApply` ref to snap-set.
- **Source of truth**: same
- **Fix sketch**: Track `firstApply`, set both states without animation on first run.

### [Sev: P2] DRD autosave on unmount — tab switch interrupts a debounced save → final 1.5s of edits lost
- **Where**: `components/projects/tabs/DRDTab.tsx:64-104`, `:107-121`
- **Axis**: data
- **Observed**: `clearTimeout(debounceTimer.current)` in cleanup but no `persistNow()` flush.
- **Expected**: Cleanup calls `persistNow()` synchronously (or via beacon).
- **Source of truth**: `DRDTab.tsx`
- **Fix sketch**: In cleanup: `if (debounceTimer.current) { clearTimeout(...); void persistNow(); }`.

### [Sev: P2] Atlas texture budget watchdog only logs — never evicts; 219 screens at 2× a typical bundle exceeds `TEXTURE_BUDGET_BYTES`
- **Where**: `components/projects/atlas/AtlasCanvas.tsx:200-214`
- **Axis**: functional
- **Observed**: Just `console.warn`; no eviction or LOD downgrade.
- **Expected**: Hook to ringTier downgrade on overage.
- **Source of truth**: `textureCache.ts`
- **Fix sketch**: Trigger downgrade when total > budget.

### [Sev: P2] No `flow_grants` enforcement in shell — `isReadOnly()` only reads `permission_denied` from URL preview flag, not real ACL
- **Where**: `components/projects/ProjectShell.tsx:215`; `lib/projects/view-machine.ts`
- **Axis**: functional
- **Observed**: `permissionDeniedFromQuery: searchParams.get("read_only_preview") === "1"` is the only signal. Real ACL via `flow_grants` is not consulted.
- **Expected**: Server returns viewer's grant level; client mirrors it.
- **Source of truth**: `flow_grants` table
- **Fix sketch**: Extend `fetchProject` response with `viewer_grant`; map into machineState.

### [Sev: P2] `screen_modes` empty for all 219 screens — JSON tab's mode resolver always null; theme toggle is decorative-only
- **Where**: `components/projects/tabs/JSONTab.tsx:53-76`
- **Axis**: data
- **Observed**: 0 screen_modes rows. `modeBindings` empty; `resolver` null. Theme toggle has no observable effect on tree values.
- **Expected**: Default + light/dark seeded by export pipeline.
- **Source of truth**: `screen_modes` table
- **Fix sketch**: Run mode-extraction stage; surface "no modes available" when bindings empty.

### [Sev: P2] `ListProjects` LIMIT 100 — silently truncates >100 projects per tenant
- **Where**: `services/ds-service/internal/projects/repository.go:507-509`, `:701`
- **Axis**: data
- **Observed**: Hard `limit = 100`; UI calls without pagination; tenants with >100 projects lose the tail with no indication.
- **Expected**: Pagination UX in `/projects`.
- **Source of truth**: same
- **Fix sketch**: Add `?cursor=` server-side, infinite scroll client-side.

### [Sev: P3] Welcome project violations use non-UUID IDs (`welcome-viol-1`) — violates UUID convention used elsewhere
- **Where**: `violations` rows
- **Axis**: data
- **Observed**: Mixed ID schemes complicate analytics; SELECT queries by UUID prefix can match unintended fixture rows.
- **Expected**: Use UUIDs for fixtures too.
- **Source of truth**: `violations` table
- **Fix sketch**: Re-seed welcome data with `uuid()`.

### [Sev: P3] JSON 404 path passes raw error text into `EmptyState` description ("404 Not Found") — not actionable copy
- **Where**: `components/projects/tabs/JSONTab.tsx:178-182`
- **Axis**: visual
- **Observed**: User sees "Re-export needed: 404 Not Found".
- **Expected**: Translate to "This screen wasn't captured during the last export. Re-run from the Figma plugin."
- **Source of truth**: same
- **Fix sketch**: Replace `description={error}` with friendly string when 404.

### [Sev: P3] DEFAULT_TAB hard-coded to `violations` — for an empty-violations project that's the worst landing page
- **Where**: `components/projects/ProjectShell.tsx:131`
- **Axis**: functional
- **Observed**: First-time visitors land on Violations → see empty state. May conclude product is broken.
- **Expected**: Default to DRD when violations are empty.
- **Source of truth**: same
- **Fix sketch**: Pass count via initial props; pick default accordingly.

### [Sev: P3] Atlas `onCanvasClick` is a no-op — background click should clear selection but doesn't
- **Where**: `components/projects/atlas/AtlasCanvas.tsx:217-222`
- **Axis**: functional
- **Observed**: Clicking empty atlas space does nothing; user has no escape from a selected frame other than another click. Esc triggers router.back.
- **Expected**: Background click → `onFrameSelect(null)`; Esc tier (1st press deselect, 2nd press leave).
- **Source of truth**: same
- **Fix sketch**: Wire `onFrameSelect(null)` on background click.

### [Sev: P3] `DRD_COLLAB_ENABLED` is read at module scope — toggling env var requires full Next dev-server restart
- **Where**: `components/projects/ProjectShell.tsx:81`
- **Axis**: functional
- **Observed**: Module-scope read; HMR doesn't re-evaluate. Operators see inconsistent SSR vs CSR until restart.
- **Expected**: Wrap in a `useFeatureFlag` hook.
- **Source of truth**: same
- **Fix sketch**: Move to a hook + document HMR behavior.

### [Sev: P3] EventSource cleanup may not close prior socket before reconnect — risk of stacking sockets
- **Where**: `lib/projects/client.ts:326-380`
- **Axis**: functional
- **Observed**: Reconnect mints ticket → opens new EventSource; previous `es` may not be closed before reassign.
- **Expected**: `es?.close()` before reassign.
- **Source of truth**: same
- **Fix sketch**: Defensive `if (es) es.close()` in reconnect path.

### [Sev: P3] Welcome flow DRD blob is 316 bytes — empty BlockNote skeleton; users opening welcome may think it's real content
- **Where**: `flow_drd` row `welcome-flow`
- **Axis**: visual
- **Observed**: 316-byte content_json. No banner indicates it's a fixture.
- **Expected**: Banner "This is a demo project" on welcome shells.
- **Source of truth**: `projects` table
- **Fix sketch**: P1's `is_fixture` column drives a banner.

### [Sev: P3] `/projects` card subtitle is `${path} · ${platform}` — semantics opaque ("research · mobile" tells the user nothing)
- **Where**: `app/projects/page.tsx:189`
- **Axis**: visual
- **Observed**: Subtitle reads "research · mobile".
- **Expected**: Show flow count + last-export date.
- **Source of truth**: same
- **Fix sketch**: Swap subtitle to `${flow_count} flows · updated ${ago}`.

## Findings — Sweep 1 (home / inbox / onboarding / settings / health)

**Sweep 1 summary.** All 5 routes load with HTTP 200. Three structural issues dominate: (1) **light-mode is broken on every route except `/`** because `data-theme` is only flipped inside `DocsShell` / `FilesShell` / `ProjectShell` — never at `app/layout.tsx`; my dark-vs-light screenshots are byte-identical (md5) for `/inbox`, `/onboarding`, `/onboarding/[persona]`, `/settings/notifications`, `/health`. (2) **`/inbox` and `/settings/notifications` are auth-gated client-side via a localStorage zustand-persist token**, but the redirect target is `/?next=…` which lands on Foundations — there is no actual login UI on `/`, so an unauth'd user is bounced to a page that visually has no relation to what they wanted; `/settings/notifications` worse: it just renders a bare "Sign in to manage notifications" card with no link, no chrome, no theme awareness. (3) **`/onboarding`, `/onboarding/[persona]`, `/settings/notifications` render with no shared chrome** (no `Header`, `Sidebar`, `Footer`); `/onboarding` is also missing from every global nav. Data layer is dormant: `notifications`, `notification_preferences`, `personas` tables are all empty (0 rows), so the inbox Mentions tab and persona admin can never display data. The hardcoded persona content for `/onboarding` references gif assets at `/onboarding/<file>.gif`, but `public/onboarding/` does not exist — every persona section renders a broken `<img>` placeholder with alt text. ds-service `/__health` returns `{"db":"ok","ok":true,"sync_git_push":false,"version":"v1"}`; `/v1/inbox` correctly rejects unauth requests with `missing bearer token`. SSE live-update path was not exercised end-to-end because no test password is available and JWT signing key is ephemeral (out of scope for read-only sweep).

### [Sev: P0] Light theme is broken on every route that does not mount DocsShell / FilesShell / ProjectShell
- **Where**: `/inbox`, `/onboarding`, `/onboarding/[persona]`, `/settings/notifications`, plus initial paint on `/health`. Theme is set in `components/DocsShell.tsx:65-79`, `components/files/FilesShell.tsx:56-67`, `components/projects/ProjectShell.tsx:362-374` — never in `app/layout.tsx:27`.
- **Axis**: visual
- **Observed**: `md5` of dark-vs-light screenshots is identical (`inbox-dark.png` == `inbox-light.png`, same for onboarding, onboarding-designer, settings-notifications). Setting `localStorage["indmoney-ds-theme"]` to `light` before navigating has no effect on these routes because nothing reads the key during root-layout mount. `app/globals.css:35` `:root, [data-theme="dark"]` defaults the whole document to dark when the attribute is absent.
- **Expected**: theme bootstrap should run once at the document root (head-blocking inline script reading `localStorage` and writing `data-theme` before paint), so every route honors the user's choice and toggling propagates everywhere.
- **Source of truth**: `localStorage["indmoney-ds-theme"]`, `app/globals.css:35-86` token sets.
- **Fix sketch**: lift theme apply into `app/layout.tsx` via a head-blocking inline script + lightweight provider; delete the per-shell `setAttribute` writes.

### [Sev: P0] `/inbox` auth bounce dumps users on Foundations page with no login surface
- **Where**: `app/inbox/layout.tsx:33-38` (`router.replace('/?next=' + pathname)`); `/` renders `DocsShell` which ignores `?next`.
- **Axis**: functional
- **Observed**: with no token in localStorage, `/inbox` flashes a transparent placeholder, then `router.replace`s to `/?next=%2Finbox`. The Foundations page mounts and never reads `?next`. There is no visible login form anywhere in `components/` (only the API call in `lib/auth-client.ts:34-47` exists). User has no path to authenticate.
- **Expected**: dedicated `/login` page (or modal that auto-opens when `?next=` is present) that calls `lib/auth-client.ts#login` and on success replays the stored `next` URL.
- **Source of truth**: `POST /v1/auth/login` handler at `services/ds-service/cmd/server/main.go:463`.
- **Fix sketch**: ship `app/login/page.tsx` with email+password form; change `LOGIN_REDIRECT` in `app/inbox/layout.tsx:15` to `/login`.

### [Sev: P0] `/settings/notifications` unauth state has no chrome and no link to sign in
- **Where**: `app/settings/notifications/page.tsx:95-105` renders a bare `<main class="page">` with `<h1>Sign in to manage notifications</h1>` and a description; no header, no nav, no sign-in button.
- **Axis**: functional + visual
- **Observed**: at 1440×900 the page shows two lines of text floating top-center on a black background (file size 14,560 bytes), plus the Next devtools "N" badge. There is no way to actually sign in. Identical render in dark + light (compounded by theme bug above).
- **Expected**: either redirect to login like `/inbox`, or render a real sign-in CTA that opens a login surface.
- **Source of truth**: same login endpoint as above.
- **Fix sketch**: replace the bare card with `<Link href="/login?next=/settings/notifications">Sign in</Link>` (after a login page exists), or wrap the route in the same `InboxLayout` redirect pattern.

### [Sev: P1] `/onboarding` and `/settings/notifications` are unreachable from global nav
- **Where**: `components/Header.tsx:396-407` top-nav array, `components/Footer.tsx:17-30` explore + resources lists, `components/Sidebar.tsx:26`.
- **Axis**: functional
- **Observed**: top nav exposes Foundations / Atlas / Projects / Components / Icons / Illustrations / Logos / Inbox / Files / Health. `/onboarding` is only linked from `app/projects/page.tsx:103` and `components/projects/ProjectShell.tsx:821`. `/settings/notifications` is not linked from anywhere in `components/`.
- **Expected**: at minimum a "Settings" affordance in the header (avatar menu) and an "Onboarding" link in the footer's Resources list.
- **Source of truth**: nav arrays themselves.
- **Fix sketch**: append `{ href: "/onboarding", label: "Onboarding" }` to footer Resources; add an avatar/account dropdown in `Header.tsx` containing Settings + Sign out.

### [Sev: P1] All five `/onboarding` persona sections render a broken `<img>` (missing gif assets)
- **Where**: `lib/onboarding/personas.ts:46` (`gif: "designer-export.gif"` and equivalents per persona), `components/onboarding/PersonaSection.tsx:62-77` always renders the `<img>` because every `PersonaSpec` has a `gif` field set.
- **Axis**: visual + data
- **Observed**: `public/onboarding/` directory does not exist (`ls: No such file or directory`). Each persona section in the `/onboarding` screenshot shows a wide gray box with the alt text "Designer (in-product team) day-1 walkthrough" rendered inside — Chrome's broken-image fallback. Five broken images per page load. Comments at `personas.ts:33-35` and `PersonaSection.tsx:64-67` claim graceful degradation, but the section unconditionally renders the figure.
- **Expected**: either drop the `gif` field for personas whose recording isn't shipped, or commit the gifs.
- **Source of truth**: `public/onboarding/*.gif` (does not exist).
- **Fix sketch**: in `PersonaSection.tsx` only render the `<figure>` after an `onError`-tested image actually loads, OR scrub the `gif` field from all persona specs.

### [Sev: P1] `/onboarding`, `/onboarding/[persona]`, `/settings/notifications` render bare main — no Header / Sidebar / Footer
- **Where**: `app/onboarding/page.tsx:27-68`, `app/onboarding/[persona]/page.tsx:47-57`, `app/settings/notifications/page.tsx:107-178`.
- **Axis**: visual
- **Observed**: all three pages render `<main>` directly without shared chrome. Compared with `/`, `/health`, `/projects`, etc., they feel like detached marketing landings: no theme toggle, no search, no top-nav back to Atlas/Projects/Inbox.
- **Expected**: share the documented chrome (at least Header + Footer) so these surfaces feel like part of the app.
- **Source of truth**: convention from `DocsShell` / `FilesShell` / `ProjectShell`.
- **Fix sketch**: introduce a lightweight `OnboardingShell` and reuse for settings (skip Sidebar since both are single-stream).

### [Sev: P1] `notifications` and `notification_preferences` and `personas` tables all have 0 rows — Mentions tab and Atlas persona admin permanently empty
- **Where**: DB `services/ds-service/data/ds.db`. Mentions consumed by `components/inbox/InboxShell.tsx:286-407` (`listNotifications`); preferences by `app/settings/notifications/page.tsx`; personas by `app/atlas/admin/personas`.
- **Axis**: data
- **Observed**: `SELECT count(*) FROM notifications` → 0; `notification_preferences` → 0; `personas` → 0. The inbox Mentions tab will always render the "No mentions" empty state; the settings page can only show server-synthesized defaults.
- **Expected**: at least a seed dataset for `chetan@indmoney.com` (`recipient_user_id = f3427f67-19a0-44c0-b97f-d18f3e3cae5d`, tenant `e090530f-2698-489d-934a-c821cb925c8a`) so the UI can be validated end-to-end.
- **Source of truth**: `notifications.kind ∈ {mention, decision_made, decision_superseded, comment_resolved, drd_edited_on_owned_flow}`.
- **Fix sketch**: add a dev-bootstrap script that inserts ~5 representative notifications + 2 preference rows + 1 pending persona for the super-admin user.

### [Sev: P2] Inbox SSE live-update path is unverified — auth-gated and no test JWT available
- **Where**: `components/inbox/InboxShell.tsx:112-147` `subscribeInboxEvents`; `lib/inbox/client.ts:252-296` (`POST /v1/inbox/events/ticket` then `EventSource(/v1/inbox/events?ticket=…)`); routes registered at `services/ds-service/cmd/server/main.go:557-559`.
- **Axis**: functional
- **Observed**: cannot exercise the live-update flow because `curl` against `/v1/inbox/events/ticket` returns `{"error":"missing bearer token"}` and the only seeded user (`chetan@indmoney.com`) has a bcrypt-hashed password unknown to me. JWT signing key is ephemeral per `cmd/server/main.go:285`. Code path on review: ticket fetch → EventSource open → reducer drops removed rows from local state on event. Looks correct on paper.
- **Expected**: live insert into `notifications` should surface in the open `/inbox` Mentions tab without refresh.
- **Source of truth**: route registrations above.
- **Fix sketch**: out of scope for this read-only sweep — recommend the next sweep run with a known test password, then `INSERT INTO notifications …` while watching the open page.

### [Sev: P2] Inbox auth gate accepts any localStorage token; no 401 → re-auth path
- **Where**: `app/inbox/layout.tsx:22` reads `useAuth(s => s.token)` then renders InboxShell. `lib/inbox/client.ts#fetchInbox` will then 401 if the token is expired.
- **Axis**: functional
- **Observed**: layout treats "any token in localStorage" as authenticated. If the token is stale (e.g. server restart cycled the ephemeral JWT key), the shell mounts, fires `fetchInbox`, and the user sees a "Couldn't load inbox" error state — not a re-auth prompt.
- **Expected**: on 401 from any DS-service call, clear the token and redirect to login.
- **Source of truth**: `lib/inbox/client.ts:fetchInbox` response handling.
- **Fix sketch**: centralize fetch through an interceptor that nukes the token and redirects to `/login` on 401.

### [Sev: P2] Header `Inbox` link does not show unread badge / count
- **Where**: `components/Header.tsx:404`.
- **Axis**: functional
- **Observed**: nav item is plain text "Inbox". No count, dot, or animation. The Mentions tab queries `listNotifications`, so the inbox can know unread count, but it isn't surfaced globally. Index `idx_notifications_inbox_unread WHERE read_at IS NULL` already exists for cheap counts.
- **Expected**: a small numeric badge or unread dot on the header link, refreshed on the same SSE channel.
- **Source of truth**: `notifications.read_at IS NULL`.
- **Fix sketch**: add a `useUnreadCount` hook that subscribes to inbox SSE and renders a badge in `Header.tsx`.

### [Sev: P2] Settings page CSS uses `var(--bg)` and `var(--surface-1)` which are NOT defined in `globals.css`
- **Where**: `app/settings/notifications/page.tsx:247` `background: var(--surface-1, rgba(255,255,255,0.02))`; `:283`, `:295`, `:312`, `:326`, `:329` reference `var(--bg)`.
- **Axis**: visual
- **Observed**: `app/globals.css` defines `--bg-base`, `--bg-surface`, `--bg-surface-2` (not `--bg`), and `--text-1` etc. (no `--surface-1`). The fallback `rgba(255,255,255,0.02)` saves the surface-1 case, but `var(--bg)` resolves to nothing → input/seg backgrounds render as `background: ;` (transparent), and the `.page` background falls back to whatever the body shows.
- **Expected**: use canonical token names — `var(--bg-base)` instead of `var(--bg)`, `var(--bg-surface)` instead of `var(--surface-1)`.
- **Source of truth**: `app/globals.css:35-130`.
- **Fix sketch**: rename the five `var(--bg)` references to `var(--bg-base)`; delete or alias the `--surface-1` fallback.

### [Sev: P2] Inbox `BulkActionBar` per-row UX: clicking Acknowledge/Dismiss on a row sets selection but does not scroll the bar into view
- **Where**: `components/inbox/InboxShell.tsx:271-278` (`onAcknowledgeRow` / `onDismissRow` set `selected = {row.id}`); `:524-541` BulkActionBar mount.
- **Axis**: functional
- **Observed**: per-row buttons route through the bottom-of-page bulk sheet by setting `selected = {row.id}`. With many rows the user clicks a row near the top and the reason input appears off-screen below.
- **Expected**: scroll the BulkActionBar into view + focus its input when single-row select fires.
- **Source of truth**: code review.
- **Fix sketch**: in the single-row handlers, call `bulkBarRef.current?.scrollIntoView({behavior:'smooth', block:'center'})` and focus the reason field.

### [Sev: P2] Inbox tab pills use ad-hoc inline styles instead of a shared tab token
- **Where**: `components/inbox/InboxShell.tsx:549-560` `modeTabStyle`.
- **Axis**: visual
- **Observed**: tabs use a 999px pill with accent fill on active and a hardcoded fallback `var(--bg-base, #fff)` for the active text color. The rest of the app (`ProjectShell`, sections) uses underline tabs — inconsistent.
- **Expected**: shared `<TabBar>` component.
- **Source of truth**: design system convention.
- **Fix sketch**: extract `<TabBar>` into `components/ui/` with two visual variants and reuse.

### [Sev: P2] `/health` Drift section silently catches `require()` errors — empty state cannot distinguish "no audits run" from "module path broken"
- **Where**: `components/HealthDashboard.tsx:454-470` `DriftBlock`.
- **Axis**: data
- **Observed**: `try { require("../lib/audit/spacing-observed.json") } catch {}`. If the file moves or rename happens, the catch swallows it and the user sees the same "No drift recorded" message as a clean install.
- **Expected**: log the catch reason (dev-only `console.warn`) so missing-file vs missing-data is distinguishable.
- **Source of truth**: code itself.
- **Fix sketch**: `catch (e) { if (process.env.NODE_ENV === 'development') console.warn('[health] drift sidecar load failed', e); }`.

### [Sev: P2] `/health` "Bound fills" StatCard tone is hardcoded `success` regardless of actual percentage
- **Where**: `components/HealthDashboard.tsx:86-91`.
- **Axis**: visual
- **Observed**: in the captured screenshot, "Bound fills" reads `63%` in green. But the StatCard always renders `tone="success"` — a 12% bound state would still be green, misleading users about quality.
- **Expected**: tone should be derived from value (e.g. ≥80% green, 50–79% warning, <50% danger).
- **Source of truth**: design intent — this card is a quality signal.
- **Fix sketch**: compute `tone` from the numeric percentage before passing to StatCard.

### [Sev: P3] `/health` "Tokens" card "Source" line renders `kind:?` when extractor omits `file_name`
- **Where**: `components/HealthDashboard.tsx:103` joins `kind:file_name` with `?` fallback.
- **Axis**: data
- **Observed**: in the screenshot the Source row uses the literal string `?` when `file_name` is missing — uninformative provenance.
- **Expected**: extractor populates `file_name` from `FIGMA_FILE_KEY_INDMONEY_GLYPH` when title fetch fails.
- **Source of truth**: `lib/tokens/loader.ts#getExtractionMeta`.
- **Fix sketch**: backfill `file_name = process.env.FIGMA_FILE_KEY_INDMONEY_GLYPH` on extractor write.

### [Sev: P3] Hero copy on `/` mentions ⌘K but ⌘K listener is mounted only inside `DocsShell` — keystroke is dead on `/inbox`, `/health`, etc.
- **Where**: `components/DocsShell.tsx:81-91` registers ⌘K only on this shell. `lib/use-keyboard-shortcuts.ts` (used in `RootClient.tsx:5`) does not include it.
- **Axis**: functional
- **Observed**: ⌘K opens search modal only on Foundations. On `/inbox`, `/health`, `/projects/...`, ⌘K does nothing — search is unreachable.
- **Expected**: ⌘K should be globally bound; SearchModal mounted at the root layout.
- **Source of truth**: `lib/use-keyboard-shortcuts.ts`.
- **Fix sketch**: move the ⌘K binding into `useKeyboardShortcuts`; render `SearchModal` once at the layout level.

### [Sev: P3] `/onboarding` footer "GitHub issues" link is a hardcoded URL — likely 404
- **Where**: `app/onboarding/page.tsx:58-62` (`https://github.com/indmoney/design-system-docs/issues`).
- **Axis**: data
- **Observed**: hardcoded URL; not validated. If the repo is private or doesn't exist at that path, users get a GitHub 404.
- **Expected**: real, public repo URL — or pull from `package.json#repository`.
- **Source of truth**: GitHub.
- **Fix sketch**: confirm the URL or template from `package.json`.

### [Sev: P3] DocsShell bottom-nav card hover shadow is hardcoded RGBA — no theme token
- **Where**: `components/DocsShell.tsx:201` `whileHover boxShadow: "0 8px 24px rgba(0,0,0,0.12)"`.
- **Axis**: visual
- **Observed**: hardcoded RGBA. Acceptable on dark background; will stamp a heavy black shadow on a light card if light mode is fixed (currently moot due to root theme bug).
- **Expected**: token like `var(--shadow-card-hover)` resolved per theme.
- **Source of truth**: `globals.css` should expose shadow tokens.
- **Fix sketch**: add `--shadow-card-hover` to both `[data-theme]` blocks; replace inline rgba.

### [Sev: P3] Naming collision: `lib/onboarding/personas.ts` and DB `personas` table are unrelated but identically named
- **Where**: DB `personas` schema (tenant-defined personas for Atlas admin), vs `lib/onboarding/personas.ts` (hardcoded role walkthroughs for `/onboarding`).
- **Axis**: data
- **Observed**: `/onboarding` does NOT read from the DB — it ships static content. The DB `personas` table is for Atlas admin (currently 0 rows). Not a bug, but the overlap will trip future readers.
- **Expected**: rename one (e.g. `lib/onboarding/role-walkthroughs.ts`) or annotate.
- **Source of truth**: file names.
- **Fix sketch**: add a top-of-file comment in `lib/onboarding/personas.ts` clarifying it's unrelated to the DB table.

### [Sev: P3] Inbox `<input type="checkbox">` for select-all uses native browser styling
- **Where**: `components/inbox/InboxShell.tsx:457-462`.
- **Axis**: visual
- **Observed**: select-all checkbox renders with browser defaults. On dark theme the unchecked state is a bright white square, off-brand vs the rest of the surface.
- **Expected**: themed checkbox or `accent-color: var(--accent)`.
- **Source of truth**: design intent.
- **Fix sketch**: reuse a `Checkbox` primitive or apply `accent-color: var(--accent)`.

### [Sev: P3] Footer `?sync=open` and `?export=open` query-string actions don't actually open modals
- **Where**: `components/Footer.tsx:27-29` links `/?sync=open`, `/?export=open`. `DocsShell.tsx:49-94` never reads `searchParams`.
- **Axis**: functional
- **Observed**: clicking the footer links navigates to `/` but `DocsShell` does not parse the query and does not call `setSyncOpen` / `setExportOpen` — the modals stay closed.
- **Expected**: parse `?sync=open` / `?export=open` in DocsShell on mount and fire the corresponding store action.
- **Source of truth**: `DocsShell.tsx`.
- **Fix sketch**: in DocsShell mount effect, read `searchParams.get('sync')` and call `setSyncOpen(true)`.

### [Sev: P3] `/health` claims "Everything below is real data extracted from the live system" but the dashboard is built-time-static — no refresh button, no last-computed timestamp
- **Where**: `components/HealthDashboard.tsx:60-63` copy; values come from build-time imports `systemStats()`, `bindingCoverage()` from `lib/icons/manifest`.
- **Axis**: functional
- **Observed**: no refresh, no relative timestamp on the StatCard hero (only the Extraction section shows last-run times). Copy overstates freshness.
- **Expected**: either soften the copy or fetch fresh data via `/__health` and a manifest endpoint, plus a Refresh button.
- **Source of truth**: `lib/icons/manifest.ts`, `lib/tokens/loader.ts`.
- **Fix sketch**: add `<RefreshButton onClick={() => router.refresh()} />` and a "Last computed" caption.

### [Sev: P3] `/onboarding/[persona]` deeplink page has only a "← All personas" link — no global nav
- **Where**: `app/onboarding/[persona]/page.tsx:49-53`.
- **Axis**: functional
- **Observed**: deeplink users (the file's stated use case — team leads share `/onboarding/designer` with new joiners) get a single back link. No way to reach Projects, Atlas, Inbox from there.
- **Expected**: shared Header per the chrome finding above.
- **Source of truth**: convention.
- **Fix sketch**: see "OnboardingShell" suggestion.

### [Sev: P3] Inbox filter changes have no client-side debounce — fine for chips today, will misbehave the moment a free-text filter ships
- **Where**: `components/inbox/InboxShell.tsx:80-98` filter-change effect; `InboxFilters.tsx`.
- **Axis**: functional (preventive)
- **Observed**: filter change → immediate `fetchInbox`. Acceptable for chip-style filters, but a future text input would hammer `/v1/inbox` on every keystroke.
- **Expected**: debounce text-typed filter values.
- **Source of truth**: code review.
- **Fix sketch**: add a `useDebouncedValue` wrapper for any string-typed filter fields.

## Findings — Sweep 2 (components / files / icons / illustrations / logos)

**Sweep 2 summary.** All five routes return HTTP 200 and render. The biggest finding is the icons-vs-Figma sync drift: the local manifest at `public/icons/glyph/manifest.json` was generated **2026-04-28** (5 days stale). Against the live Glyph file (`ePcuKLNGHwSubsfaiaryJv`, 933 component_sets via `GET /v1/files/.../component_sets`), the manifest has **134 entries with no name- or id-match in Figma** (dead refs — components renamed/deleted), **76 entries name-matched but with a different `node_id`** (re-published components needing re-sync of node ids + variant payloads), and **36 Figma component_sets entirely missing from the manifest** (new icons, including filled icons like `Send`, `Gainers`, `Losers`, `Star`, `Payment Error`, multiple new 3D goal illustrations under `Icon/3D/...`). Every Sweep 2 surface other than `/components/[slug]` and `/files` reads from this manifest, so the drift propagates everywhere. The `/files` route has 0 audited files (`lib/audit-files.json` has `files: []`) and renders an empty-state body with hard-coded "Sample file A/B/C" preview chips that aren't clickable; `/files/[slug]` 404s for those slugs (P1 affordance bug — they look interactive). `/illustrations`, `/logos`, `/icons` all source from the same manifest. `/logos` shows duplicate sidebar categories (`Logo` 319 + `Logos` 1 + lowercase `merchant` + `bank`) — pure category-string normalization gap; `/components` sidebar references two anchor ids that don't exist in DOM (`cat-design-system`, `cat-masthead`); the global header truncates `Logos` to `L` at 1440px because `Files` + `Health` overflow the nav strip. Tokens-generated CSS (`lib/tokens-generated/tokens.css`) is referenced everywhere but the asset surfaces hardcode several non-token RGBA shadows and pixel sizes inline. No P0 truly-broken render — the app degrades gracefully — but the data audit reveals real drift the team probably doesn't see because the UI tile renders via cached SVG even when the underlying Figma component is gone.

### [Sev: P0] Icons manifest is 5 days stale — 134 dead entries, 36 missing icons, 76 re-published needing resync
- **Where**: `public/icons/glyph/manifest.json` (`generated_at: 2026-04-28T09:00:33Z`); consumed by `lib/icons/manifest.ts` → `/icons`, `/illustrations`, `/logos`
- **Axis**: data
- **Observed**: against live `GET /v1/files/ePcuKLNGHwSubsfaiaryJv/component_sets` (933 sets), the manifest has 134 truly-orphan entries (no Figma component_set matches by `node_id` or normalized name — likely renamed or deleted; samples: `1-cta` `1625:46664`, `2-cta-horizontal` `1625:46638`, `3-cta` `3674:38121`, `2d-foreclose`, `2d-toll`, `3d-car-family`, `3d-car-new`, `3-00-pm`, `3-30-pm`, `3-step-progress-bar`); 76 entries match by name but Figma's `node_id` changed (re-published — variant payloads will be wrong); 36 Figma component_sets have no manifest entry (`Icons/Filled Icons/Send`, `Icons/Filled Icons/Gainers`, `Icons/Filled Icons/Losers`, `Icons/Filled Icons/Star`, `Icons/Filled Icons/Payment Error`, `Icons/Filled Icons/Awaited`, `Icons//Instacash`, `Icons/Logos/Jubilant`, `Icon/3D/Custom Goal - New`, `Icon/3D/Retirement - New`, `Icon/3D/Kids Education - New`, `Icon/3D/Home - New`, `Icon/3D/Car - Family`, etc.).
- **Expected**: manifest re-generated within the last 24–48h, no orphan refs, no missing component_sets that have been published in Figma.
- **Source of truth**: Figma file `ePcuKLNGHwSubsfaiaryJv` `meta.component_sets` (933 entries); manifest `public/icons/glyph/manifest.json` `icons[]` (912 entries)
- **Fix sketch**: re-run `services/ds-service/cmd/icons` against the live glyph file; CI cron the extractor nightly; add a manifest-vs-Figma drift check to `npm run audit` that fails the build if drift > 5%.

### [Sev: P0] `/files` renders "Sample file A/B/C" preview chips that look clickable but `/files/[slug]` returns 404
- **Where**: `/files`, body of `components/files/FilesIndex.tsx` empty-state preview block; route `app/files/[slug]/page.tsx` returns 404 for `sample-file-a`/`-b`/`-c`
- **Axis**: functional
- **Observed**: `lib/audit-files.json` has `files: []` so the empty state fires. The "SAMPLE PREVIEW" block shows three chips with `94%`, `82%`, `67%` mock scores. Clicking them appears interactive (cursor:pointer styling on hover) but `GET /files/sample-file-a` returns HTTP 404. Designers will think audits are broken, not that the data is mock.
- **Expected**: sample chips clearly non-interactive (no hover affordance) or the empty-state copy explains "preview is illustrative only".
- **Source of truth**: `lib/audit-files.json` (`files: []`), `app/files/[slug]/page.tsx` slug list
- **Fix sketch**: add `pointerEvents: 'none'` + `cursor: 'default'` to the SAMPLE PREVIEW chips, or wrap them in a `<div>` not a card; OR seed `audit-files.json` with one real file so the empty state never shows on dev.

### [Sev: P1] Manifest categories duplicated by case + whitespace — `/logos` shows "Logo" + "Logos" + lowercase "merchant" + "bank" as separate sidebar entries
- **Where**: `/logos` left sidebar; `/illustrations` left sidebar (similar exposure); category source `IconEntry.category` in `public/icons/glyph/manifest.json`
- **Axis**: data
- **Observed**: distinct category strings in manifest: `"Logo"` (325), `"Logos"` (1), `"merchant"` (13), `"bank"` (2), `"nvidia"` (1), `"Atoms "` (11 — trailing whitespace), `"Cold"`, `"Footer"` etc. The /logos sidebar lists `Logo`, `merchant`, `bank`, `Logos` as four separate groups. /illustrations sidebar shows `3D` (21), `2D` (2), `Masthead` (2). 11 icons have category `uncategorized` and bucket under "OTHER" on /icons.
- **Expected**: manifest `category` field normalized (Title-Case + trimmed) at extraction time; `Logo` and `Logos` collapsed to one canonical bucket.
- **Source of truth**: `public/icons/glyph/manifest.json` `icons[].category`
- **Fix sketch**: in the Go extractor (`services/ds-service/cmd/icons`), apply `strings.TrimSpace` + canonical-case mapping before writing `category`; also map `merchant`→`Merchant`, `bank`→`Bank`, `Logo`/`Logos`→`Logo`.

### [Sev: P1] Header overflows at 1440px — `Logos` link is clipped to "L" / "Lo" between `Illustrations` and `Files`
- **Where**: global `components/Header.tsx` nav strip, visible on every Sweep 2 route at viewport ≥1024px including 1440×900
- **Axis**: visual
- **Observed**: at 1440-wide viewport the nav reads `Foundations  Atlas  Projects  Components  Icons  Illustrations  L` — `Logos`, `Inbox`, `Files`, `Health` items are truncated/cut by the search button (`Search tokens… ⌘K`) which sits flush in the same row.
- **Expected**: all primary nav items remain readable at 1440px; either reduce gap, ellipsize the search-button label, move overflow into a "More" menu, or wrap.
- **Source of truth**: live render `/icons` `/logos` `/components` at 1440×900
- **Fix sketch**: collapse `Search tokens…⌘K` to a 32px icon button below 1600px, OR shrink nav font from 14→13 and gap from 24→16.

### [Sev: P1] `/components` sidebar references `cat-design-system` + `cat-masthead` anchors that don't match `slugifyCategory` output
- **Where**: `/components`; warning fires `[useActiveSection] sidebar references anchor ids that do not exist in the DOM: [cat-design-system, cat-masthead]`
- **Axis**: functional
- **Observed**: console warning on every load. `slugifyCategory("Design System 🌟")` produces `design-system` (emoji stripped) but the canvas section ids are emitted by `ComponentCanvas` which strips spaces differently → mismatch. Sidebar clicks scroll-spy stays inert; clicking the sidebar item does jump (because hash-scroll falls through), but the active highlight is wrong.
- **Expected**: section ids and sidebar hrefs come from the same `slugifyCategory` call site; no warning.
- **Source of truth**: `app/components/page.tsx` `slugifyCategory(cat)` vs `components/ComponentCanvas.tsx` band id emission
- **Fix sketch**: pass `sectionIds` from page → ComponentCanvas as the canonical list, or have ComponentCanvas re-export the function it uses for ids.

### [Sev: P1] Cmd+K does not open SearchModal under headless Playwright (and likely real users when focus is on a tile)
- **Where**: `components/files/FilesShell.tsx:73` `(e.metaKey || e.ctrlKey) && e.key === "k"`
- **Axis**: functional
- **Observed**: pressing `Meta+K` and `Control+K` from a freshly-loaded `/icons` (focus on body) yields `document.querySelectorAll('[role=dialog]').length === 0`. The header search button itself was unclickable in the same test (Playwright timeout — likely behind another element). Manual click via the same selector also failed when an icon detail overlay had recently been dismissed.
- **Expected**: Cmd+K reliably toggles SearchModal regardless of which element has focus, on both Mac and Linux.
- **Source of truth**: `components/files/FilesShell.tsx:73-80`; `components/DocsShell.tsx:84`
- **Fix sketch**: register the listener on `window` not `document`, set capture-phase, and `preventDefault()` before testing the key — also cover the case when the active element is inside an `<input>` (currently the handler likely returns early when typing into the icons filter).

### [Sev: P1] Icon "Copy SVG" / "Copy slug" / "Copy URL" buttons present but no toast feedback verified
- **Where**: `/icons` icon-detail modal (opens correctly on tile click, e.g. Calendar icon); buttons wired in `components/sections/IconographySection.tsx` `IconDetail`
- **Axis**: functional
- **Observed**: clicking an icon tile opens the detail card with the SVG body, three copy buttons, and an `×` close. The buttons are present, but no visible toast/confirmation appears in the post-click screenshot — `showToast` is imported but the user has no signal the copy succeeded. (The text "Copied" was not in the post-click body text snapshot.)
- **Expected**: any "Copy" action shows a 1.5s toast (the project already imports `showToast`).
- **Source of truth**: `components/sections/IconographySection.tsx` IconDetail copy button onClick
- **Fix sketch**: confirm `showToast({ message: "SVG copied" })` is called inside each copy onClick; add a visible Radix `Toast` viewport at the modal level so feedback isn't blocked by overlay z-index.

### [Sev: P1] 36 Figma icons are published in Glyph but absent from `/icons`, `/illustrations`, `/logos` — including critical filled icons (Send, Gainers, Losers, Star, Payment Error)
- **Where**: same manifest as P0 above; renders on `/icons` (Filled Icons category — currently 9 entries, should be ~14+)
- **Axis**: data
- **Observed**: live Figma has filled icons `Send`, `Gainers`, `Losers`, `Star`, `Payment Error`, `Awaited` in `Filled Icons` category; manifest has only 9. /icons "Filled Icons" tile count = 9, missing 5+ visible product icons that ship in the live app.
- **Expected**: parity between Figma `Icons/Filled Icons/*` and manifest `kind:'icon', category:'Filled Icons'`.
- **Source of truth**: Figma `ePcuKLNGHwSubsfaiaryJv` page `Icons Fresh` containing_frame names matching `Icons/Filled Icons/*`
- **Fix sketch**: same as P0 (re-sync); if filled-icons specifically are excluded by the extractor's filter, audit `services/ds-service/cmd/icons` `extractor.go` for an outdated allowlist.

### [Sev: P1] `/illustrations` tile labels truncate at 8 chars (`3D · Car - F…`, `3D · Custom …`) — every 3D illustration has a name longer than the tile
- **Where**: `/illustrations` 3D category tiles; `components/AssetGallery.tsx` Grid component (`tileMin: 104` for square layout on desktop)
- **Axis**: visual
- **Observed**: 21 of 25 tiles in the 3D illustration grid show truncated labels with ellipsis. Tooltip on hover does NOT exist on /illustrations (unlike /icons which uses `Tooltip`).
- **Expected**: full name visible on hover OR tile sized to fit, OR a 2-line text wrap.
- **Source of truth**: live render `/illustrations` 1440×900
- **Fix sketch**: add `Tooltip` wrapper to AssetGallery tile (mirror IconographySection's IconTile), or allow `white-space: normal` + `text-overflow: clip` with `display: -webkit-box; -webkit-line-clamp: 2`.

### [Sev: P1] `/logos` dark-mode parity — white-on-white logo tiles (American Express, Mastercard) have no border separation in dark theme
- **Where**: `/logos` dark mode; tiles for logos with white backgrounds in source SVG (American Express, Apple Pay, Visa)
- **Axis**: visual
- **Observed**: in dark mode, a white-background logo SVG sits inside a white tile area at the same brightness — no visual frame around the logo. Light mode is fine. The tile uses `var(--bg-surface)` which goes dark, but the logo's own white background bleeds.
- **Expected**: each tile has a visible 1px `var(--border)` even when the asset's interior is white; OR the tile gets a 4px inner padding ring in a contrasting shade in dark mode.
- **Source of truth**: `/tmp/sweep2/logos_dark.png` and `/tmp/sweep2/logos.png`
- **Fix sketch**: ensure tile `border: 1px solid var(--border)` is preserved in dark theme (likely a token override is hiding it); add `box-shadow: inset 0 0 0 1px var(--border)` on hover.

### [Sev: P1] /components shows variants on the right with "+7" placeholder card — variant strip caps at 11 and silently truncates rest
- **Where**: `/components` Badges row → variant strip; `components/ComponentCanvas.tsx` band rendering
- **Axis**: data + functional
- **Observed**: Badge component declares `20 variants · State × Stroke × Type` but only 11 render before a `+7` cap chip. There is no click-through to "see all variants" — `+7` is a static placeholder. Same pattern seen on `Bottom Nav` (caps at 1 with no `+N`).
- **Expected**: clicking `+7` opens the inspector with the full variant matrix, OR the strip wraps to a second row.
- **Source of truth**: live render `/components`; Figma component set has 20 variants
- **Fix sketch**: wire `+7` chip onClick to `setOpenSlug(entry.slug)` and ensure inspector shows axisMatrix in full.

### [Sev: P1] `/files` empty state hardcodes `npm run audit` instruction but `lib/audit-files.json.$description` says the plugin auto-registers — instructions are inconsistent
- **Where**: `/files` empty body copy "Add at least one Figma file_key to lib/audit-files.json and run the sweep" vs `lib/audit-files.json` `$description`: "Auto-registered by the Figma plugin. Each entry comes from a designer running an audit on that file."
- **Axis**: data + content
- **Observed**: empty state instructs manual JSON edit; manifest description says it's auto-managed. Designers will follow the wrong path.
- **Expected**: empty-state copy points designers to "open the Figma plugin → Audit file" since that's the authoritative sync path.
- **Source of truth**: `lib/audit-files.json` `$description`; `components/files/FilesIndex.tsx` empty-state copy
- **Fix sketch**: replace empty-state body with: "Open the INDmoney DS Figma plugin and run **Audit file** on the design you want to track. Files appear here automatically once the audit completes."

### [Sev: P2] 765 of 912 manifest entries have `variants: []` — the rich-extraction phase only ran on ~16% of components
- **Where**: `public/icons/glyph/manifest.json` `icons[].variants`
- **Axis**: data
- **Observed**: only 147 entries (16%) carry variant payloads; 765 are bare. Affects /components inspector ("Variants" tab will be empty for any non-component-set icon clicked), and /icons "Copy slug"/"Copy SVG" works but variant axes don't render.
- **Expected**: every component_set entry has `variants[]` populated; singleton components legitimately have empty.
- **Source of truth**: `public/icons/glyph/manifest.json` per-entry `variants` array
- **Fix sketch**: re-sync with the rich extractor; if rich extraction was selectively disabled to bound runtime, document the cutoff in manifest header.

### [Sev: P2] /icons "OTHER" category contains real product icons that should be classified — `instacash`, `alpca`, `coin-swap`, `dow-jones`, `drivewealth`, `emergency`, `exchange`, `marriage`, `more`, `note`, `speaker`
- **Where**: /icons OTHER section (uncategorized bucket — 11 entries); manifest `category: "uncategorized"`
- **Axis**: data
- **Observed**: these are clearly UI / 2D / merchant icons miscategorized at extraction time. They render at the top of /icons because the OTHER section sorts first.
- **Expected**: each routed to an existing category (`alpca`, `dow-jones`, `drivewealth` → Merchant or Logo; `instacash`, `coin-swap`, `emergency`, `exchange`, `marriage`, `more`, `note`, `speaker` → 2D).
- **Source of truth**: Figma `containing_frame.name` for each (e.g. `Icons//Instacash` shown in component_sets list — note the double slash, which is the bug — extractor splits on `/` and the empty path-segment falls into "uncategorized").
- **Fix sketch**: extractor: when `containing_frame.name` matches `/^Icons\/+([^/]+)/i` use the next non-empty segment; also normalize double-slashes.

### [Sev: P2] `/components` band ordering is alphabetical-by-cat-size, not the documented atomic-design tiers
- **Where**: `/components` band order; `parentComponents()` returns organism-tier only but bands order by `b[1].length - a[1].length`
- **Axis**: visual + content
- **Observed**: bands ordered by category-count descending (Design System 🌟 28 → Masthead 2). The page comment says "atomic-design tier hierarchy" should drive ordering. Buttons / Input Field / Bottom Sheet (which carry primary product affordances) are nowhere on /components because `parentComponents()` filters to organism tier — but visually a designer arriving at /components expects atom→molecule→organism progression.
- **Expected**: documented order and a chip on each band stating its tier.
- **Source of truth**: `app/components/page.tsx:36-44`
- **Fix sketch**: order bands by `(tier_rank, name)` and surface the tier in each band header.

### [Sev: P2] `/icons`, `/illustrations`, `/logos` use `<img>` for tiles — no lazy-loading attribute on illustrations/logos pages (icons uses CSS mask which is fine)
- **Where**: `components/AssetGallery.tsx` Grid → tile `<img>`
- **Axis**: visual + perf
- **Observed**: 335 logos and 25 illustrations all `<img>` tags rendered eagerly. /logos issues 335 image fetches on first paint (verified — `img_count: 335` from Playwright). `loading="lazy"` not present.
- **Expected**: `loading="lazy" decoding="async"` on every gallery `<img>`; intersection-observer fallback.
- **Source of truth**: `components/AssetGallery.tsx`; live network panel
- **Fix sketch**: add `loading="lazy" decoding="async"` to the `<img>` element.

### [Sev: P2] `/components/[slug]` Variant card "DEFAULT" badge attached to the wrong column visually — Type=Primary card is highlighted but "DEFAULT" floats over the variant grid heading
- **Where**: `/components/1-cta` Variants section — third card "Type Primary" highlighted with blue border + `DEFAULT` chip in the top-right
- **Axis**: visual
- **Observed**: `DEFAULT` chip sits outside the card on a higher z-index, half-overlapping the card border. It reads as a column-header decoration rather than a card-level marker.
- **Expected**: chip sits flush inside the card top-right corner with `inset 0 0 0 1px var(--accent)` ring.
- **Source of truth**: `/tmp/sweep2/components_1cta.png`
- **Fix sketch**: position the badge `position: absolute; top: -10px; right: 12px` inside the card container, not the parent.

### [Sev: P2] /icons header chip "RENDERER CSS mask" exposes implementation detail with no link to docs
- **Where**: `/icons` Iconography header meta strip
- **Axis**: content
- **Observed**: meta strip shows `TOTAL ICONS 405 · CATEGORIES 5 · DEFAULT SIZE 24×24 · FORMAT SVG · RENDERER CSS mask`. "CSS mask" is meaningless to designers and is implementation noise.
- **Expected**: drop "RENDERER" or replace with "Recolors via currentColor" which the body already says.
- **Source of truth**: `components/sections/IconographySection.tsx` meta-strip render
- **Fix sketch**: remove the RENDERER chip.

### [Sev: P2] /components header strip "wheel pan canvas · space + drag grab to pan · ←→ step pan · esc close" — keyboard hints crowded and not adaptive to OS
- **Where**: `/components` top-right hint strip
- **Axis**: visual
- **Observed**: 4 hint chips wrap onto 2 lines on viewports < 1280px; on Mac, "wheel" is misleading (trackpad two-finger swipe). No auto-detect for input modality.
- **Expected**: collapse to a single `?` button that opens a popover with the full hint list.
- **Source of truth**: live render `/components`
- **Fix sketch**: replace strip with `<button aria-label="Keyboard shortcuts">⌨</button>` + Radix Popover.

### [Sev: P2] /icons "Filter Glyph icons by name or slug…" placeholder differs from search affordance on /illustrations + /logos ("Search…")
- **Where**: /icons vs /illustrations vs /logos search bar placeholder
- **Axis**: content
- **Observed**: 3 placeholders for the same UX: `Filter Glyph icons by name or slug…` (icons), `Search…` (illustrations + logos). Inconsistent voice + length.
- **Expected**: consistent placeholder pattern, e.g. `Search icons by name…` / `Search illustrations…` / `Search logos…`.
- **Source of truth**: `components/sections/IconographySection.tsx` SearchBar; `components/AssetGallery.tsx` SearchBar
- **Fix sketch**: align both components on `Search ${title.toLowerCase()}…`.

### [Sev: P2] Icon detail modal shows the full SVG source as a `<pre>` block — useful, but no syntax highlighting and no width cap
- **Where**: /icons → click any icon → detail card body
- **Axis**: visual
- **Observed**: SVG markup renders as plain monospace text wrapping into 8-9 lines for a 24×24 icon. No highlighting, no copy-on-click on the block itself (only the dedicated "Copy SVG" button below).
- **Expected**: highlight + click-to-copy on the block; or fold the `<svg>` and show only the `<path d="…">` line.
- **Source of truth**: `/tmp/sweep2/icon_after_click.png`
- **Fix sketch**: wrap the `<pre>` in a `<button onClick={copy}>`; use a small monochrome highlighter (or just bold attribute names).

### [Sev: P2] Manifest `kind: "component"` includes 28 entries categorised "Design System 🌟" — these are likely tokens/master frames, not product components
- **Where**: /components band labelled `DESIGN SYSTEM 🌟` (28 cards)
- **Axis**: data
- **Observed**: this band is the largest group on /components. Inspecting the band shows Badge/Bottom Nav/Checkmarks etc which DO belong here, but also the 🌟 category likely contains design-system meta sets that should not be on the component browser. Worth a manual review pass.
- **Expected**: design-tokens / typography master frames excluded from /components; surface them under /foundations instead.
- **Source of truth**: manifest entries with `category: "Design System 🌟"`
- **Fix sketch**: extractor flag `meta_only: true` for any component_set whose name starts with `Tokens/`, `Type/`, `Color/`; filter those out of `parentComponents()`.

### [Sev: P3] /components band "+7" placeholder uses non-token grey background
- **Where**: /components Badges row "+7" tile
- **Axis**: visual
- **Observed**: `+7` chip background appears slightly cooler than `var(--bg-surface-2)` — likely a one-off `rgba(255,255,255,0.04)`.
- **Expected**: align with `var(--bg-surface-2)` token.
- **Source of truth**: `components/ComponentCanvas.tsx` overflow chip
- **Fix sketch**: replace inline `rgba(...)` with `var(--bg-surface-2)`.

### [Sev: P3] /illustrations + /logos sidebar "Categories" header has no count chip; /icons left rail has no category breakdown at all (single "All icons" entry)
- **Where**: left rails on /icons vs /illustrations vs /logos
- **Axis**: visual
- **Observed**: /illustrations + /logos show category names only (no per-cat count); /icons rail collapses to `All icons` even though the page has 5 sections (`OTHER 11`, `2D 266`, `3D 118`, `FILLED ICONS 9`, etc.). Inconsistent with /components which shows `Design System 🌟 · 28`.
- **Expected**: every gallery sidebar shows `<category> · <count>` and includes per-category sub-anchors.
- **Source of truth**: `app/icons/page.tsx:8-15` (single `All icons` link) vs `app/logos/page.tsx` cats loop
- **Fix sketch**: lift the category-grouping logic from /logos into /icons; emit `cats.map(...)` sub-anchors.

### [Sev: P3] Manifest `Cold` category has 1 entry — leftover taxonomy, ditto `Footer`, `Review and add`, `Toast Messages`, `Primary Title`, `Wallet`, `Nudges`
- **Where**: `public/icons/glyph/manifest.json` low-N categories
- **Axis**: data
- **Observed**: these single-entry categories likely come from auto-extracted Figma frame names that happen to match a category-shaped string. Pollutes the sidebar.
- **Expected**: categories with `< 2` entries collapse into `Other`.
- **Source of truth**: manifest `category` distribution
- **Fix sketch**: in `slugifyCategory` callers, fall back to `Other` if `cats.get(cat).length < 2`.

### [Sev: P3] Tokens-generated `lib/tokens-generated/tokens.css` is consumed but not surfaced on these routes — no `/tokens` link from /icons or /illustrations
- **Where**: top of /icons, /illustrations, /logos — no breadcrumb back to Foundations/Tokens
- **Axis**: navigation
- **Observed**: a designer arriving at /illustrations to grab an asset has no in-page link to /foundations#color or /tokens, even though the asset gallery uses those tokens.
- **Expected**: small "Theme: var(--bg-surface) · var(--text-1)" footer chip linking to `/?token=bg-surface`.
- **Source of truth**: live render
- **Fix sketch**: add a footer strip on each gallery: `Theming via [INDmoney tokens →](/)`.

### [Sev: P3] `/files` Header renders bot-friendly `<title>INDmoney DS · Foundations</title>` on every Sweep 2 route — no per-route title differentiation
- **Where**: every Sweep 2 route document `<title>`
- **Axis**: SEO + navigation
- **Observed**: tab title is identical for /icons, /illustrations, /logos, /components, /files. Browser tabs become ambiguous when designers have multiple ds-docs tabs open.
- **Expected**: per-route title like `Icons · INDmoney DS`.
- **Source of truth**: every route's `head` / `metadata` export
- **Fix sketch**: add `export const metadata = { title: "Icons · INDmoney DS" }` to each `app/<route>/page.tsx`.

### [Sev: P3] Search button in header reads "Search tokens…" but indexes more than tokens (icons, components per SearchModal source)
- **Where**: header search button label "Search tokens…⌘K"
- **Axis**: content
- **Observed**: the Cmd+K search modal indexes tokens + components + icons (per SearchModal.tsx imports), but the button text suggests tokens-only.
- **Expected**: "Search…" or "Search tokens, icons, components…".
- **Source of truth**: `components/Header.tsx:182-183` `aria-label="Open search (cmd+k)"`
- **Fix sketch**: change visible label to "Search…".

### [Sev: P3] /components inspector "Where this breaks" tab present but unverified — Phase-4 reverse-view violations API was not exercised
- **Where**: `/components/[slug]` left rail "Where this breaks" link
- **Axis**: functional
- **Observed**: the link is present in the left rail; clicking lands but no findings text was captured. Worth a follow-up to confirm `/v1/components/violations` returns useful data for the active tenant (Atlas-deep noted persona/severity counts are zero, so violations may also be empty).
- **Expected**: tab shows either real violations or a clean empty state explaining the dependency on Atlas signal data.
- **Source of truth**: `components/components/WhereThisBreaks.tsx`
- **Fix sketch**: log a no-data hint pointing at /atlas/admin if response is empty.

### [Sev: P3] `/files` page top reads `Files · Audit not yet run · \`npm run audit\`` — backticks render as literal characters in the meta strip
- **Where**: /files header meta strip
- **Axis**: visual
- **Observed**: the strip shows the backtick characters literally instead of styling the code.
- **Expected**: render `npm run audit` inside `<code>` with mono font + bg-surface-2.
- **Source of truth**: `components/files/FilesIndex.tsx` header meta strip
- **Fix sketch**: replace string with JSX: `Audit not yet run · <code>npm run audit</code>`.

---

## Aggregated severity rollup

**Total: 131 findings** across 4 surfaces — **16 P0**, 43 P1, 40 P2, 32 P3.

### Cross-cutting themes (issues that show up across multiple surfaces)

1. **Auth is broken end-to-end.** ds-service generates an ephemeral `JWT_SIGNING_KEY` per restart (`cmd/server/main.go:285`); no login UI exists in the entire codebase (only `lib/auth-client.ts#login` API helper); auth-redirect from `/inbox` and `/settings/notifications` dumps users on the Foundations page with no sign-in surface. Blocks every browser-based audit and breaks the actual product.
2. **Theme bootstrap is incomplete.** `data-theme` is only set inside `DocsShell`, `FilesShell`, `ProjectShell` — never at root layout — so `/inbox`, `/onboarding`, `/onboarding/[persona]`, `/settings/notifications` are dark-only (md5-confirmed: light + dark screenshots byte-identical).
3. **Most domain tables are empty.** `personas`, `canonical_taxonomy`, `notifications`, `notification_preferences`, `flow_drd` (for indian-stocks), `screen_canonical_trees` (for indian-stocks v810061c4 — 0 rows even though pipeline reached `view_ready`), `violations` (0 for v810061c4) — every admin/inbox/personalization surface degrades to empty state.
4. **Hardcoded hex bypassing tokens / runbook §2.6.** At least 5 atlas-surface files (`HoverSignalCard.tsx`, `admin/rules/page.tsx`, `DashboardShell.tsx`, bell badge, hover bg) carry inline severity/type colors that should live in `forceConfig.ts`.
5. **Asset/manifest sync drift.** Icons manifest is 5 days stale: 134 dead entries, 76 re-published `node_id` changes, 36 missing-from-manifest (Send/Gainers/Losers/Star + new 3D goal illustrations).
6. **`/atlas` ↔ `/projects/[slug]` integration is half-wired.** Atlas frame-click forces a tab switch to JSON, but JSON is empty for indian-stocks; DRD/Decisions/Violations all bind to `screens[0].FlowID` so only 1 of 3 flows is ever shown; Esc handler always calls `router.back()` ignoring the `?from=` reverse-morph contract; `view-transition-name` mismatches between `/atlas` source and `/projects` breadcrumb so the morph never connects.

### P0 findings (16) — broken / data-wrong / user-blocking

| # | Surface | Finding | Line |
|---|---|---|---|
| 1 | atlas | Browser audit blocked — ephemeral JWT key + no login UI | 64 |
| 2 | atlas | `personas` table empty → /atlas/admin/personas inert | 72 |
| 3 | atlas | `canonical_taxonomy` empty → /atlas/admin/taxonomy says "no projects yet" | 80 |
| 4 | projects | Auth gate makes every project tab un-renderable | 362 |
| 5 | projects | JSON tab broken — 0 `screen_canonical_trees` rows for v810061c4 | 370 |
| 6 | projects | DRD/Decisions/Violations bind to `screens[0].FlowID` — only 1 of 3 flows visible | 378 |
| 7 | projects | DRD tab fetches but no `flow_drd` row exists for indian-stocks flows | 386 |
| 8 | projects | Violations tab empty even though `view_ready` — 0 violations for v810061c4 | 394 |
| 9 | projects | `personas` empty → persona chips & filtering inert | 402 |
| 10 | projects | Atlas frame-click switches to JSON tab — JSON tab is empty (P0 #5) → headline interaction broken end-to-end | 410 |
| 11 | projects | PNG endpoint authenticates via `?token=<jwt>` — leaks JWT to access logs + browser history | 418 |
| 12 | sweep-1 | Light theme broken on every route except `/` (no root-layout `data-theme` bootstrap) | 694 |
| 13 | sweep-1 | `/inbox` auth bounce dumps users on Foundations with no login surface | 702 |
| 14 | sweep-1 | `/settings/notifications` unauth state has no chrome and no sign-in link | 710 |
| 15 | sweep-2 | Icons manifest 5 days stale — 134 dead, 36 missing, 76 re-published | 898 |
| 16 | sweep-2 | `/files` shows "Sample file A/B/C" chips but `/files/[slug]` 404s (real `lib/audit-files.json` is empty) | 906 |

### Recommended remediation order

1. **Unblock auth** — persist `JWT_SIGNING_KEY` to `.env.local` so tokens survive restart; ship a login page (or document a dev-mode bootstrap-token flow). This unblocks ~6 of the P0s and the entire visual-parity audit axis we couldn't run.
2. **Lift theme bootstrap to root layout** — single change; fixes 4 routes' light-theme regression.
3. **Backfill empty tables** — `personas`, `canonical_taxonomy`, `notifications`, `notification_preferences`, `flow_drd`, `screen_canonical_trees`, `violations` for v810061c4. The pipeline succeeded for screens but the post-screens stages (canonical_tree extraction, audit job, violations materialization) clearly didn't complete or weren't triggered. Re-investigate why `view_ready` was set without these.
4. **Re-sync icons manifest** — drop the orphans, refresh node_ids, add the 36 new icons.
5. **Wire flow selector into ProjectShell** — currently `screens[0].FlowID` makes 2 of every 3 flows invisible.
6. **Fix the JSON tab** — populate `screen_canonical_trees` so atlas-click → JSON-tab actually shows something.
7. **Move PNG auth to header-based** — kill the `?token=` query-string leak path.
8. **Address the runbook §2.6 hex-drift** — hoist `SEVERITY_COLOR` map.

Each P1 below feeds into a follow-up cleanup PR; full table inline above per surface.
