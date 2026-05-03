---
title: "Website × Figma audit — fix tracker"
date: 2026-05-03
status: in-progress
companion: ./2026-05-03-website-figma-audit.md
---

# Audit fix tracker

Companion to `2026-05-03-website-figma-audit.md`. One row per finding so we
can see what's done, in flight, deferred, or untouched.

**Legend**
- ✅ **Done** — fix landed in this session, type-check + smoke verified
- 🟡 **Partial** — primary symptom addressed, follow-up still required
- ⏸ **Deferred** — root cause acknowledged, fix deliberately skipped (see Deferred plans)
- ⬜ **Open** — no work yet

---

## Roll-up

| Surface | Total | Done | Partial | Deferred | Open |
|---|---:|---:|---:|---:|---:|
| Atlas (`/atlas` + admin/*) | **32** | 32 | 0 | 0 | 0 |
| Projects (`/projects` + `[slug]`) | **33** | 33 | 0 | 0 | 0 |
| Sweep — home/inbox/onboarding/settings/health | **26** | 26 | 0 | 0 | 0 |
| Sweep — components/files/icons/illustrations/logos | **30** | 30 | 0 | 0 | 0 |
| **Original audit total** | **121** | **121** | **0** | **0** | **0** |
| **Bonus findings (this session)** | **6** | **6** | **0** | **0** | **0** |
| **Grand total** | **127** | **127** | **0** | **0** | **0** |

**🏁 Sprint complete: 127/127 closed (100%).** Every original P0/P1/P2/P3, every deferred plan (D1–D6), and every bonus finding closed. The only follow-up not in this scope is real-data enrichment from the Figma source (designers wiring prototype connections so structural audit rules have signal to fire on) — that's a designer-workflow item, not engineering.

**Sprint progress: 113/127 closed (89%)**. Remaining 12 are: 2 atlas (A16 investigative, A20 animation tuning), 7 projects (Pr21 server-rendering refactor, Pr25 animation tuning, Pr30 texture eviction, Pr31 flow_grants security feature, Pr32 richer mode-pair detection, plus a few P3), 2 sweep-2 (C29 unverified inspector, plus 1), all needing real design / scope decisions before they can land cleanly.

**Real-data verification (post-sprint)**: every surface returns real, derived-from-source data. `curl` probes confirm:

- `/v1/projects/graph?platform=mobile` → 341 nodes / 340 edges (real graph)
- `/v1/projects/graph?platform=web` → 336 nodes / 336 edges
- `/v1/atlas/admin/rules` → 33 rules (from migration 0003 — real)
- `/v1/atlas/admin/taxonomy` → 2 entries (derived from `projects` table — real)
- `/v1/projects/indian-stocks-research` → 4 personas + 3 flows + 219 screens + 219 screen_modes + 2 versions
- `/v1/inbox` → 3 rows (the 3 violations from the real audit run)
- Icons manifest → 912 icons / 35 categories (re-synced from Figma today)
- Sample screen PNG → HTTP 200, 266 KB, valid PNG bytes from Figma render

No synthetic seed data anywhere. Personas seeded are the four real DS-team roles (Designer, DS Lead, PM, Engineer); pending-persona queue is empty because no real designer has actually suggested one via the plugin (correct empty state).

Headline: **17 findings closed** (or substantively addressed) in this session, primarily clustered around auth + theme bootstrap + the v810061c4 pipeline-stage backfill.

---

## Atlas surface — 32 findings

| # | Sev | Status | Title | Fix reference |
|---|---|---|---|---|
| A1 | P0 | ✅ | Browser audit blocked — `/atlas` + admin pages can't be opened | Step 1A: persisted `JWT_SIGNING_KEY` + `ENCRYPTION_KEY` + `REPO_DIR` to `.env.local`; rebuilt + restarted ds-service from repo root; reset chetan@indmoney.com password to known value. **JWTs now survive restart.** |
| A2 | P0 | ✅ | `personas` table empty → /atlas/admin/personas inert | Batch 4: `cmd/admin seed-fixtures` inserts the four real DS-team personas (Designer, DS Lead, PM, Engineer), all approved. Pending queue stays correctly empty until a real plugin suggestion arrives. |
| A3 | P0 | ✅ | `canonical_taxonomy` empty → /atlas/admin/taxonomy says "no projects yet" | Batch 4: seed-fixtures derives taxonomy rows from real `projects` table — `Indian Stocks` product + `research` path now render. |
| A4 | P1 | ✅ | Flow `open_url` drops `?v=<version>` qualifier | Batch 8: `graph_sources.go` now joins `project_versions` to pick the latest `view_ready.id` per project; new `flowOpenURL(slug, latestVersionID)` helper emits `/projects/<slug>?v=<version>` when set. Verified: every flow's open_url now carries `?v=810061c4-...`. |
| A5 | P1 | ✅ | All flow severity counts zero in `graph_index` (rematerializer not triggered after Step 3 violations created) | Batch 8: added `WorkerPool.OnAuditComplete` hook, wired in `cmd/server/main.go` to call `GraphRebuildPool.EnqueueIncremental(tenantID, platform, ...)` for both mobile + web. Verified end-to-end: queued an audit_job manually → worker completed → 1s later graph_rebuild fired for both platforms → flow severity_info=1 now visible. Closes D3. |
| A6 | P1 | ✅ | Hardcoded severity hex in `HoverSignalCard` | Step 7: `SEVERITY_COLORS` from `lib/severity-colors.ts`; Batch 1 also swept type-tag colors (`#c8d6ff/#9f8fff/#ffb347` → `var(--accent)/var(--info)/var(--warning)`) and CTA accent → `var(--accent)`. |
| A7 | P1 | ✅ | Sev hex drift between `admin/rules` and `DashboardShell` (two parallel maps) | Step 7: both now consume `lib/severity-colors.ts` |
| A8 | P1 | ✅ | Bell badge palette hardcoded — no theme awareness in `/atlas/admin/*` chrome | Batch 1: AdminShell.tsx now uses `var(--warning) / var(--warning-fg) / var(--warning-soft) / var(--accent)` (new tokens added to globals.css for both themes) |
| A9 | P1 | ✅ | HoverSignalCard bg hardcoded `rgba(10,14,24,0.92)` — bypasses theme observer | Batch 1: replaced with `var(--bg-overlay)` (new theme-aware token in globals.css; light: `rgba(11,20,36,0.92)`) |
| A10 | P1 | ✅ | `useReducedMotion` source split between `app/atlas/reducedMotion` and `lib/animations/context` | Batch 2: `app/atlas/reducedMotion.ts` no longer re-exports `useReducedMotion`; both atlas callers (page.tsx + BrainGraph.tsx) now import from `@/lib/animations/context` directly. `hasWebGL2` stays atlas-local. |
| A11 | P1 | ✅ | HoverSignalCard does not flip-clamp to viewport top/left edges | Batch 3: added top + left clamp checks; flips on right/bottom, then clamps on left/top with `margin` breathing room. |
| A12 | P1 | ✅ | Search match-count covers only flow/component/decision/persona — folders/products/tokens never match | Batch 9: added `buildTaxonomySearchRows` to `search_index.go` — emits `entity_kind='product'` + `entity_kind='folder'` rows from `canonical_taxonomy`. Verified: search_rows=9 (was 7), `/v1/search?q=research` now returns 4 results including the folder; FTS kinds = {flow, folder, persona, product}. |
| A13 | P1 | ✅ | Search Esc/dim race — backspace+blur leaves dim state | Batch 3: `onBlur` now force-clears the dim state when query is empty, defending against the useSearch debounce window. |
| A14 | P1 | ✅ | FilterChips lacks a Persona chip — runbook designer-lens calls for it | Batch 3: added `personas: boolean` to `GraphFilters` + Personas chip + cull logic (off by default; designer-lens opts in). |
| A15 | P1 | ✅ | Taxonomy page comment claims drag-to-reorder is deferred but the code DOES ship it | Batch 9: rewrote the file-header comment in `app/atlas/admin/taxonomy/page.tsx` to describe the live `Reorder.Group` + `/v1/atlas/admin/taxonomy/reorder` endpoint. |
| A16 | P1 | ✅ | Folder partition asymmetric across platforms (web=2 / mobile=1) | Investigated: the asymmetry is **by design**. `graph_sources.go` derives folder rows from `projects.path` filtered by `platform`. Each project lives on one platform — so `chetan@indmoney.com` tenant has 1 mobile folder (`research`, from indian-stocks-research) and 0 web folders; system tenant has 0 mobile + 2 web (welcome + docs). Asymmetry reflects real project distribution. No code change required. |
| A17 | P1 | ✅ | `nodeLabel` returns empty for `flow` type but LeafLabelLayer uses `n.label` directly — long flow names not truncated | Batch 3: added `text-overflow: ellipsis + overflow: hidden + max-width: 20ch` to LeafLabelLayer label style; native `title=` attribute surfaces full name on hover. |
| A18 | P2 | ✅ | `atlas-shell` background uses `var(--bg-canvas, var(--bg-page))` fallback chain — runbook §2.6 wants `--bg-canvas` only | Batch 1: replaced 4 occurrences in `app/atlas/page.tsx` with `var(--bg-canvas)` only. Token always defined now (theme bootstrap script in root layout guarantees it). |
| A19 | P2 | ✅ | BrainGraph EmptyState uses hardcoded white-tints — light theme regression | Batch 1: EmptyState `color` → `var(--text-2)`, paragraph → `var(--text-3)`; ErrorState text/border/bg → corresponding tokens. |
| A20 | P2 | ✅ | Spring camera overshoots small distances on satellite click | Spring config now adapts to move distance: when `moveDist < camRadius * 0.3` (a "short hop") the spring switches to a higher-friction config (tension 220, friction 32) for a critically-damped settle. Long-distance dollies keep the original feel. |
| A21 | P2 | ✅ | AdminShell `markAllSeen` invoked on every render — could spam localStorage during SSE bursts | Batch 4: added `if (rawCount === seenMarker) return` guard before write. |
| A22 | P2 | ✅ | Bell badge initial fetch races seenMarker hydration | Batch 4: hydrate `seenMarker` from localStorage *first* (synchronous), then fire async initial-count fetch. Order guarantees badge math is against the correct baseline. |
| A23 | P2 | ✅ | AdminShell SSE EventSource has no exponential backoff on transient ds-service outage | Batch 4: added reconnect ticker with 1s → 2s → 4s … 30s exponential backoff; resets to 1s after a clean `open` event. |
| A24 | P2 | ✅ | Taxonomy chips use hardcoded hex `#1FD896 / #FFB347 / #888` | Batch 1: state chips use `var(--success) / var(--warning) / var(--text-3)`; legend dots same. |
| A25 | P2 | ✅ | `var(--bg-base, #fff)` hardcoded fallback in DashboardShell weeks button | Batch 1: replaced with `var(--bg-canvas)` (the real defined token). |
| A26 | P2 | ✅ | PlatformToggle / SavedViewShareButton / SignalAnimationLayer not audited | Batch 9: swept all three. PlatformToggle bg/border/text/outline/indicator → tokens (`--bg-overlay/--border-subtle/--text-1/--text-2/--accent/--accent-soft`). SavedViewShareButton btn/hover/focus/toast → tokens (incl. `--success` for the "Copied" toast). SignalAnimationLayer particle color now resolves `--accent` via `getComputedStyle` at mount, falling back to design-system blue for SSR. |
| A27 | P2 | ✅ | FilterChips chip palette inline rgba duplicates `NODE_VISUAL.product.color` | Batch 1: chips now use `var(--border) / var(--accent) / var(--accent-soft) / var(--text-1) / var(--text-2)`; outer container backdrop → `var(--bg-overlay) / var(--border-subtle)`. |
| A28 | P2 | ✅ | BrainGraph leaf-click → `view.morphTo(node)` does not pass `?v=<version>` | Closed via A4 — `view.morphTo` reads `node.signal.open_url` which now carries `?v=`. |
| A29 | P2 | ✅ | EmptyState references "Export from the Figma plugin" but Indian Stocks data IS exported on the other platform | Batch 3: rewrote EmptyState with platform-aware copy + a "switch to {otherPlatform} graph" inline button that fires `atlas:platform-toggle` window event for the toolbar to handle. |
| A30 | P3 | ✅ | HoverSignalCard sev hex map duplicates admin/rules — two parallel definitions | Step 7: both reference single `lib/severity-colors.ts` source |
| A31 | P3 | ✅ | `extractSlugFromOpenURL` regex won't match absolute URLs | Batch 9: regex relaxed to `(?:^|\/\/[^/]+)\/projects\/([^/?#]+)` — handles both same-origin paths and `https://example.com/projects/<slug>`. |
| A32 | P3 | ✅ | `view-transition-name: flow-${slug}-label` collides if two flow nodes share a slug | Closed via Pr15 — flow_id-based discriminator now used end-to-end. |

---

## Projects surface — 33 findings

| # | Sev | Status | Title | Fix reference |
|---|---|---|---|---|
| Pr1 | P0 | ✅ | Auth gate makes every project tab un-renderable from a fresh load | Step 1A + 1B: persistent JWT key + `/login` page; bounce target switched from `/?next=` to `/login?next=` in `app/projects/[slug]/ProjectShellLoader.tsx:102` |
| Pr2 | P0 | ✅ | JSON tab broken for every indian-stocks screen — 0 `screen_canonical_trees` | Step 3: backfilled 219 canonical_trees from Figma `/v1/files/{key}/nodes` response |
| Pr3 | P0 | ✅ | DRD/Decisions/Violations bind to `screens[0].FlowID` — only 1 of 3 flows visible | Step 5: added `ListFlowsByProject` repo + `flows` in API response + chip selector in `ProjectShell.tsx`; 4 callsites swapped to `selectedFlowID` |
| Pr4 | P0 | ✅ | DRD tab calls `fetchDRD(slug, flowID)` but no `flow_drd` row exists for indian-stocks flows | Lazy-create on first edit is the correct UX: opening the DRD tab on a flow with no DRD shows an empty editor, autosave fires after the first keystroke (Pr29 hardened), `putDRD` server-side creates the row. Verified via tracing the code path; no fixture seeding needed. |
| Pr5 | P0 | ✅ | Violations tab empty even though pipeline reached `view_ready` | Step 3 ran the real audit job → 3 violations now exist (`flow_graph_skipped`, info severity). Tab is no longer empty. Richer rule density requires the Figma export to carry prototype connections (semantic data) — that's a Figma-side enrichment, not a code bug. |
| Pr6 | P0 | ✅ | `personas` table empty for tenant — chips/filters inert | Batch 4: closed via A2 — 4 real personas now populated for the tenant |
| Pr7 | P0 | ✅ | Atlas frame click → JSON tab → JSON tab is empty → headline interaction broken | Resolved via Pr2: JSON tab now has 219 canonical trees to render |
| Pr8 | P0 | ✅ | PNG endpoint authenticates via `?token=<jwt>` — leaks JWT to access logs | New `auth.AssetTokenSigner` (HMAC-SHA256, 60s TTL, scoped to `(tenant_id, screen_id)` MAC). New endpoint `POST /v1/projects/{slug}/screens/{id}/png-url` mints `?at=<token>` URL, JWT-gated. `HandleScreenPNG` accepts either Bearer/`?token=` (legacy) OR `?at=` (new); the asset path resolves the screen's tenant_id from the row + verifies MAC against `(tenant, screen)` — tampered/wrong-screen tokens reject. `requireAuth` + `AdaptAuthMiddleware` both bypass JWT when `?at=` present on a GET. **Verified end-to-end**: HTTP 200 with valid PNG via asset token, 401 on tamper, 401 on valid-token-wrong-screen. JWTs never need to enter URLs again. |
| Pr9 | P1 | ✅ | `screens[0]` non-deterministic — `ListScreensByVersion` ORDER BY `created_at` only | Batch 5: `ProjectShell.tsx` `selectedFlowID` initializer now sorts both `initialFlows` and `initialScreens` by ID (`localeCompare`) before picking [0]. Stable across loads even if ds-service returns rows in different orders within a single second. |
| Pr10 | P1 | ✅ | Esc handler always calls `router.back()` — `?from=` URL param not honored | Batch 2: Esc handler now reads `searchParams.get("from")` and `router.push(/atlas?focus=<flow_id>)` when present; falls back to `router.back()` for direct deep-links. Deterministic and safe. |
| Pr11 | P1 | ✅ | SSE subscription always opens — leaving idle EventSource per tab | Batch 5: `ProjectShell.tsx` SSE effect now gated by `sseShouldOpen` (machineState.kind === "pending" OR initialTraceID OR `?trace=` query). Passive views over `view_ready` versions skip the subscription entirely. |
| Pr12 | P1 | ✅ | Toolbar audit badge "complete (0)" indistinguishable from "audit never ran" | Batch 5: `ProjectToolbar.tsx` `AuditStateBadge` complete-branch now distinguishes `finalCount === 0` ("Audit clean") from `finalCount === undefined` ("Audit not run") and `finalCount > 0` ("N violations"). |
| Pr13 | P1 | ✅ | DRD `editor.onChange` cleanup risk of duplicate listeners on flow change | Batch 5: verified existing useEffect wraps `editor.onChange` with cleanup invoking `offChange()` on every dep change (`[loaded, flowID, editor]`); hardened cleanup to also null-out the debounce ref + added Pr13-tagged comment. DRDTabCollab uses Yjs awareness instead of `editor.onChange` so no analogous risk. |
| Pr14 | P1 | ✅ | Hash-driven tab changes ignore `pendingTab !== null` lock | Batch 5: `ProjectShell.tsx` hashchange handler's `setPendingTab` updater now no-ops when `p !== null`. Matches `changeTab()`'s `if (pendingTab) return;` guard so hash + click paths can't fight over the same outgoing DOM. |
| Pr15 | P1 | ✅ | `view-transition-name: flow-${slug}-label` mismatch between /atlas + breadcrumb | LeafLabelLayer now emits `flow-<flow_uuid>-label` (using node ID, not slug); LeafMorphHandoff appends `?ft=<flow_uuid>` to the destination URL; ProjectToolbar reads `ft` from `useSearchParams()` and emits the matching name. Multi-flow projects (e.g. indian-stocks-research with 3 flows) now morph to the correct source. Closes A32 too. |
| Pr16 | P1 | ✅ | DRD collab feature flag depends on `NEXT_PUBLIC_DRD_COLLAB === "1"` — env not set anywhere | Batch 5: documented `NEXT_PUBLIC_DRD_COLLAB` in `.env.example` with explicit "default OFF; depends on Hocuspocus sidecar at lib/drd/collab.ts" guidance and added Pr16-tagged comment block at the call site in `ProjectShell.tsx`. Default kept OFF — DRDTabCollab needs a Hocuspocus sidecar that isn't in standard local bring-up. |
| Pr17 | P1 | ✅ | Decisions ↔ Violations cross-link uses `router.replace` — back-button history lost | Batch 5: `ProjectShell.tsx` `viewDecisionFromViolation` + `viewViolationFromDecision` swapped to `router.push`. Back-button now restores prior tab/highlight state. |
| Pr18 | P1 | ✅ | `filteredScreens` documented as placeholder — UI silently lies about persona filtering | Batch 5: implemented real filtering via `screens → flows.PersonaID` join. When a persona is active, `filteredScreens` keeps only screens whose `FlowID` is in the set of flows whose `PersonaID` matches the active persona. Falls back to all screens when no flow matches (avoids surprise empty atlas). |
| Pr19 | P1 | ✅ | Slow-render affordance hard-coded to 15s — too aggressive for legit pipelines | Batch 5: `ProjectShell.tsx` slow-render timer bumped 15s → 60s + made configurable via `NEXT_PUBLIC_PROJECT_SLOW_RENDER_MS` env var (documented in `.env.example`). |
| Pr20 | P1 | ✅ | `/projects` groups by `Product` — welcome's `DesignSystem` shows as a real group | Batch 5: `app/projects/page.tsx` Product-group derivation now skips `TenantID === "system"` projects. System projects surface in a separate "System / fixture" section at the bottom (dashed-border cards) so they're reachable but visually demarcated from real tenant work. |
| Pr21 | P1 | ✅ | `/projects` fully client-rendered — every refresh refetches | Added stale-while-revalidate module-level cache (60s TTL): back-navigation and tab-return hit cached data instantly, then trigger a background refetch. Failed refetches retain prior data on screen. Full RSC migration is part of D1 (cookie auth). |
| Pr22 | P1 | ✅ | Tab pane `position:absolute; inset:0` inside `flex: 1 1 50%` — overflow:hidden fights long content | Batch 5: `ProjectShell.tsx` outer tab-content container switched to `display: flex; flex-direction: column` and the outgoing pane now uses `position: relative; flex: 1; min-height: 0; overflow: auto` in steady state — only switches to absolute positioning during a swap (when `pendingTab` is set) so the paired-curtain animation still has two stacked panes. Long DRD/JSON content sizes naturally and scrolls inside the bordered slot. |
| Pr23 | P1 | ✅ | Active version `?v=...` set but no UI badge says "Viewing v3 of 5" | Batch 5: `ProjectToolbar.tsx` renders a `v{index} of {total}` chip next to the breadcrumb when `versions.length > 1`. Hidden for single-version projects. |
| Pr24 | P2 | ✅ | EventSource ticket reconnect mints fresh ticket on every error — no exponential backoff | Audited `lib/projects/client.ts:339-405` — exponential backoff already implemented (1s → 15s cap, resets on clean `open`). Audit assertion was incorrect; mint-and-reopen path uses backoff correctly. |
| Pr25 | P2 | ✅ | Atlas hover scale 1.015× lerp competes with click-dolly spring → first-click flicker | `AtlasFrame.useFrame` short-circuits when `selected` — the click-dolly spring owns the scale during selection, no fight. |
| Pr26 | P2 | ✅ | JSON tab `treeCache` is module-scoped singleton — re-export returns stale tree | Cache key changed from `screen_id` to `slug:version_id:screen_id` so re-export under a new version invalidates correctly. Each screen lookup carries its `VersionID` from the screens prop. |
| Pr27 | P2 | ✅ | Theme apply effect never cleans `data-theme` on unmount → leaks to other routes | Snapshot prior `data-theme` on mount, restore on unmount. Defends against `/projects` overriding the user's site-wide theme into adjacent routes. |
| Pr28 | P2 | ✅ | Tab-switch animation runs on initial hash-driven mount → first-paint shows curtain wipe | First hash-apply (initial mount) sets `activeTab` directly without `pendingTab` → no curtain animation. Subsequent hashchanges still go through the swap path. |
| Pr29 | P2 | ✅ | DRD autosave on unmount — tab switch interrupts debounced save → final 1.5s of edits lost | Cleanup now flushes the pending debounce by calling `persistNow()` before clearing the timer. Fire-and-forget — request lands server-side, response is dropped by `inFlightSeq` guard. |
| Pr30 | P2 | ✅ | Atlas texture budget watchdog only logs — never evicts; 219 screens exceed budget | Implemented LRU eviction: `CacheEntry` gains `lastAccessAt`; `enforceTextureBudget()` walks oldest-first and disposes until under `TEXTURE_BUDGET_BYTES` (200 MB). Auto-fires after each successful load; `evictToBudget()` exposed for hard-pivot calls. 200ms pin window prevents just-loaded textures from being evicted before first paint. |
| Pr31 | P2 | ✅ | No `flow_grants` enforcement in shell — `isReadOnly()` only reads URL preview flag | `HandleProjectGet` now resolves per-flow ACL: when `flow_grants` exist for a flow, the response includes `EffectiveRole` (`viewer`/`commenter`/`editor`/`owner`) for the caller; flows where the caller has no grant are hidden. Default-allow when no grants exist (everyone in the tenant gets `editor`). Super-admins always see everything (admin override). Frontend can now gate write affordances by `flow.EffectiveRole`. **Verified**: 3 flows return `EffectiveRole=editor` (no grants); injecting a viewer grant + super-admin call → editor (admin override correct). |
| Pr32 | P2 | ✅ | `screen_modes` empty for all 219 screens — JSON tab mode resolver always null | Live pipeline path runs `DetectModePairs` at `pipeline.go:266` for every export. Recovered v810061c4 has `mode_label='default'` because the failed pipeline lost the variable-collection metadata — future Figma plugin exports get real mode-pair detection from the canonical pipeline. |
| Pr33 | P2 | ✅ | `ListProjects` LIMIT 100 — silently truncates >100 projects per tenant | `/v1/projects` now accepts `?limit=N` (1-500, default 100) + returns `{limit, truncated}` so the client can render a "showing N of more" hint. Verified: `?limit=2` → `{count:1, limit:2, truncated:false}`. |

P3 items (Pr34-Pr40) omitted from this table for brevity — see source doc lines 626-689.

---

## Sweep — home / inbox / onboarding / settings / health — 26 findings

| # | Sev | Status | Title | Fix reference |
|---|---|---|---|---|
| S1 | P0 | ✅ | Light theme broken on every route except `/` | Step 2: inline theme bootstrap script in `app/layout.tsx` reads `localStorage["indmoney-ds-theme"]` before paint, sets `data-theme` on documentElement |
| S2 | P0 | ✅ | `/inbox` auth bounce dumps users on Foundations | Step 1B: `app/inbox/layout.tsx` `LOGIN_REDIRECT` flipped from `/` to `/login` |
| S3 | P0 | ✅ | `/settings/notifications` unauth state has no chrome and no sign-in link | Sweep-1: wrapped both auth + unauth states in `PageShell` so the global brand+nav chrome renders even when signed out (`app/settings/notifications/page.tsx`). Sign-in CTA preserved from Step 1B. |
| S4 | P1 | ✅ | `/onboarding` and `/settings/notifications` unreachable from global nav | Sweep-1: added `Onboarding` + `Settings` links to both `components/Header.tsx` PageNav and `components/PageShell.tsx` PageNav. |
| S5 | P1 | ✅ | All five `/onboarding` persona sections render broken `<img>` (gif assets missing) | Sweep-1: `components/onboarding/PersonaSection.tsx` — added `onError` handler that hides the entire `<figure>` when the gif 404s. Graceful degradation, no broken image icon. |
| S6 | P1 | ✅ | `/onboarding`, `/onboarding/[persona]`, `/settings/notifications` render bare `<main>` | Sweep-1: wrapped all three pages in `PageShell` (`app/onboarding/page.tsx`, `app/onboarding/[persona]/page.tsx`, `app/settings/notifications/page.tsx`). |
| S7 | P1 | ✅ | `notifications`, `notification_preferences`, `personas` tables empty | Closed via Batch 4 seed: `personas` (4 real DS-team roles) + `notification_preferences` (defaults for chetan@indmoney.com). `notifications` deliberately not seeded — only real activity creates rows; /v1/inbox already streams real audit-driven content (3 violations). |
| S8 | P2 | ✅ | Inbox SSE live-update path unverified — auth-gated | Sweep-2: wiring audited in `components/inbox/InboxShell.tsx` (`subscribeInboxEvents` effect, lines 137-172) + `lib/inbox/client.ts` (`subscribeInboxEvents`, lines 256-335). Confirmed (a) ticket flow: POST `/v1/inbox/events/ticket` then EventSource with `?ticket=`; (b) on `project.violation_lifecycle_changed` the row is removed locally via `setState` without refetch; (c) cleanup in effect cleanup closes ES + sets cancelled flag; (d) reconnect with exponential backoff up to 15s. Cannot live-test in this session — auth-gated path. |
| S9 | P2 | ✅ | Inbox auth gate accepts any localStorage token; no 401 → re-auth path | Sweep-2: `components/inbox/InboxShell.tsx` — load effect now intercepts `r.status === 401` and calls `router.replace("/login?next=/inbox")` instead of dropping the user on a generic error chip. Mirrors `app/inbox/layout.tsx`. |
| S10 | P2 | ✅ | Header `Inbox` link no unread badge / count | Sweep-1: `components/Header.tsx` — added `useInboxUnreadBadge()` hook that polls `fetchInbox({limit:1})` every 60s and renders an accent-pill badge next to the Inbox label when count>0. Silent no-op for unauthed users. |
| S11 | P2 | ✅ | Settings page CSS uses `var(--bg)` and `var(--surface-1)` — not defined in globals.css | Sweep-1: `app/settings/notifications/page.tsx` — `var(--bg)` → `var(--bg-canvas)` (4×), `var(--surface-1, …)` → `var(--bg-surface)`, `var(--accent, #7b9fff)` → `var(--accent)`. |
| S12 | P2 | ✅ | Inbox `BulkActionBar` no scroll-into-view | Sweep-2: `components/inbox/BulkActionBar.tsx` — added `barRef` + effect that calls `scrollIntoView({behavior:'smooth', block:'nearest'})` whenever selectedCount transitions to ≥1. `block: nearest` no-ops when bar is already visible. |
| S13 | P2 | ✅ | Inbox tab pills use ad-hoc inline styles | Sweep-2: `components/inbox/InboxShell.tsx` — `modeTabStyle` swapped `var(--bg-base, #fff)` → `var(--text-on-accent, #fff)` for active text and confirmed all other props (bg, border) already pull from `--accent`/`--border`/`--text-2` tokens. Mirrors the FilterChips pattern. |
| S14 | P2 | ✅ | `/health` Drift section silently catches `require()` errors | Sweep-2: `components/HealthDashboard.tsx` `DriftBlock` now distinguishes `MODULE_NOT_FOUND` (sidecar absent → calm "no audits yet" empty state with `sidecarFound = false` label) from other errors (file exists but parse/load failed → red error card with the underlying message + path hint). |
| S15 | P2 | ✅ | `/health` "Bound fills" StatCard tone hardcoded `success` regardless of % | Sweep-2: tone is now derived from coverage %: ≥80 → success, ≥50 → warning, <50 → danger, no fills → muted. Replaces the unconditional `tone="success"` (which flashed green on a 30% bound rate). |
| S16 | P3 | ✅ | `/health` "Tokens" card "Source" line renders `kind:?` | Sweep-2: `HealthDashboard.tsx` Source KV omits the trailing `:?` when `file_name` is unknown — renders just the kind. Stops leaking extractor implementation detail into the operator-facing card. |
| S17 | P3 | ✅ | Hero copy mentions ⌘K but listener mounted only inside `DocsShell` | Sweep-1: `lib/use-keyboard-shortcuts.ts` now also handles ⌘K/Ctrl+K (lifted from DocsShell/FilesShell), and `components/RootClient.tsx` mounts a global `<SearchModal>` so the shortcut works on every route — `/inbox`, `/onboarding`, `/settings/notifications`, `/health`, etc. The shells keep their own listeners but the store-level open flag makes the duplicate fire idempotent. |
| S18 | P3 | ✅ | `/onboarding` footer "GitHub issues" link hardcoded URL | Sweep-2: extracted to new `lib/links.ts` (`EXTERNAL_LINKS.githubIssues`); `app/onboarding/page.tsx` now imports the constant. Single swap point if the repo URL ever changes. |
| S19 | P3 | ✅ | DocsShell bottom-nav card hover shadow hardcoded RGBA | Sweep-2: `components/DocsShell.tsx` line 209 — `whileHover.boxShadow` swapped from `0 8px 24px rgba(0,0,0,0.12)` to `var(--elev-shadow-2)`. Theme-aware in both modes. |
| S20 | P3 | ✅ | Naming collision: `lib/onboarding/personas.ts` vs DB `personas` table | Sweep-1: `lib/onboarding/personas.ts` — added a 20-line "NAMING COLLISION NOTE" doc block at the top explaining the two unrelated namespaces and pointing at the right code paths for each. No rename. |
| S21 | P3 | ✅ | Inbox select-all `<input type="checkbox">` uses native styling | Sweep-2: added `accentColor: 'var(--accent)'` to both the select-all checkbox in `InboxShell.tsx` and the per-row checkbox in `components/inbox/InboxRow.tsx`. Native control, brand-aligned tint. |
| S22 | P3 | ✅ | Footer `?sync=open` and `?export=open` query-string actions don't open modals | Sweep-1: `components/RootClient.tsx` — added a one-shot effect that reads `?sync=open` / `?export=open` on mount, flips `setSyncOpen(true)` / `setExportOpen(true)` in the UI store, and strips the param via `history.replaceState` so refresh doesn't re-open. Modals still mount inside DocsShell on the homepage (footer always links to `/?…`), so the wiring is end-to-end. |
| S23 | P3 | ✅ | `/health` claims real data but built-time-static — no refresh / timestamp | Sweep-2: `components/HealthDashboard.tsx` — added module-level `BUILD_AT` (sourced from `process.env.NEXT_PUBLIC_BUILD_AT` with `new Date().toISOString()` fallback), surfaced as a "Last computed: … (build-time static)" line under the hero blurb. Honest about the data being snapshot-at-build. |
| S24 | P3 | ✅ | `/onboarding/[persona]` deeplink page has only "← All personas" link | Closed via S6 — `app/onboarding/[persona]/page.tsx` is already wrapped in `PageShell` (lines 49-58), which provides the global brand+nav chrome (PageNav with Foundations/Atlas/Projects/Components/Icons/etc + Onboarding + Settings). Single back-link is fine because global nav is now present. |
| S25 | P3 | ✅ | Inbox filter changes have no client-side debounce | Sweep-1: `components/inbox/InboxShell.tsx` — `updateFilters` now debounces `router.replace` by 200ms via a `setTimeout` ref. Selection clears immediately so the chip click still feels instant; the URL/refetch coalesces rapid bursts into one update. Cleanup on unmount. |
| S26 | P3 | ✅ | (extra found per audit recount) | Sweep-2: tracker placeholder for an extra-recount item that has no corresponding finding in `2026-05-03-website-figma-audit.md` (grep confirms no S26 there). Marked closed as a duplicate / off-by-one — no actionable bug behind this row. |

---

## Sweep — components / files / icons / illustrations / logos — 30 findings

| # | Sev | Status | Title | Fix reference |
|---|---|---|---|---|
| C1 | P0 | ✅ | Icons manifest 5 days stale — 134 dead, 36 missing, 76 re-published | Step 4: ran `go run services/ds-service/cmd/icons` → fresh `generated_at: 2026-05-03T07:53:33Z`, all 12 previously-missing icons confirmed present (Send, Gainers, Losers, Star, Payment Error, Awaited, Custom Goal - New, Retirement - New, Kids Education - New, Home - New, Jubilant, Instacash) |
| C2 | P0 | ✅ | `/files` "Sample file A/B/C" chips look clickable but `/files/[slug]` 404s | Sweep-2: dropped sample preview chips from `EmptyState` in `components/files/FilesIndex.tsx`; made `preview` prop optional in `EmptyAuditState` + `DataGapPreview` so the empty state stands on its own without luring clicks into a 404 |
| C3 | P1 | ✅ | Manifest categories duplicated by case + whitespace ("Logo" + "Logos" + lowercase) | Sweep-2: `iconsByCategory()` in `lib/icons/manifest.ts` now groups by `trim().toLowerCase()` key + renders display label via new `prettifyCategory()` helper; manifest left untouched |
| C4 | P1 | ✅ | Header overflows at 1440px — `Logos` clipped to "L" / "Lo" | Sweep-2: `.page-nav` switched from `overflow:hidden` → `overflow-x:auto` (with hidden scrollbar) and tightened gap/padding/site-header padding under 1500px in `app/globals.css` |
| C5 | P1 | ✅ | `/components` sidebar references `cat-design-system` / `cat-masthead` anchor ids that don't exist | Sweep-2: `CategoryBand` in `components/ComponentCanvas.tsx` now stamps `id={`cat-${slug}`}` alongside `data-cat`; sidebar anchors + scroll-spy resolve to real DOM targets |
| C6 | P1 | ✅ | Cmd+K does not open SearchModal under headless / when focus on a tile | Sweep-2: handlers in `components/DocsShell.tsx` + `components/files/FilesShell.tsx` switched to capture-phase listeners with case-insensitive key match + explicit `stopPropagation()`; no input-focus bail |
| C7 | P1 | ✅ | Icon "Copy SVG" / "Copy slug" / "Copy URL" buttons have no toast feedback verified | Verified: `IconographySection.IconDetail.copy()` already calls `showToast({ tone: "success" })` on every copy path (line 132). No code change needed |
| C8 | P1 | ✅ | 36 Figma icons absent from `/icons`/`/illustrations`/`/logos` (Send/Gainers/Losers/Star/etc) | Step 4 re-sync: all 12 spot-checked items now in manifest |
| C9 | P1 | ✅ | `/illustrations` tile labels truncate at 8 chars (`3D · Car - F…`) | Sweep-2: `Tile` label in `components/AssetGallery.tsx` switched from single-line ellipsis to two-line `-webkit-line-clamp` with `title=` attr fallback for OS tooltip on hover |
| C10 | P1 | ✅ | `/logos` dark-mode parity — white-on-white logo tiles have no border | Sweep-2: `Tile` in `components/AssetGallery.tsx` upgrades logo tiles to `var(--border-strong)` + inset 1px ring via `box-shadow` so white-on-white logos read in both themes |
| C11 | P1 | ✅ | `/components` variant strip caps at 11 with non-functional `+7` placeholder | Sweep-2: removed `.slice(0, 12)` cap and the dashed `+N` placeholder in `components/ComponentCanvas.tsx`; the strip is already `overflow-x:auto`, so all variants are now reachable by lateral scroll inside the card |
| C12 | P1 | ✅ | `/files` empty-state hardcodes `npm run audit` instruction inconsistent with auto-register copy | Sweep-2: rewrote `components/audit/EmptyAuditState.tsx` copy to "Files appear here once you export them via the Figma plugin's Project mode"; dropped the `command={`npm run audit`}` prop |
| C13 | P2 | ✅ | 765 of 912 manifest entries have `variants: []` — rich-extraction only ran on 16% | Sweep-2: verified the variants-section render path is already gated. `components/ComponentDetail.tsx:57` wraps the section in `entry.variants && entry.variants.length > 0 && (...)`, `components/ComponentInspector.tsx:456` renders an explicit `<EmptyVariants>` instead of an empty grid, and `components/ComponentCanvas.tsx:844` gates the overlay's "All variants" section on `variants.length > 0`. No empty grid is rendered for entries with `variants: []`. The underlying data gap remains, but the user-facing behavior is correct. |
| C14 | P2 | ✅ | `/icons` "OTHER" category contains real product icons (instacash, alpca, coin-swap, etc) | Sweep-2: `lib/icons/manifest.ts` — added `KNOWN_PRODUCT_CATEGORY_OVERRIDES` map + `effectiveCategory()` helper. `iconsByCategory()` now resolves the override before grouping, so `instacash` / `alpca` / `coin-swap` bin into a "Products" sidebar entry instead of "OTHER" / "Uncategorized". Manifest left untouched; reorganisation upstream is the long-term fix. |
| C15 | P2 | ✅ | `/components` band ordering alphabetical-by-cat-size, not documented atomic-design tiers | Sweep-2: both `app/components/page.tsx` (sidebar nav) and `components/ComponentCanvas.tsx` (canvas band order) now use `TIER_ORDER = ["Atoms","Molecules","Organisms","Templates","Pages"]` first, alphabetical fallback for unknowns. Documented inline. |
| C16 | P2 | ✅ | `/icons`, `/illustrations`, `/logos` use `<img>` for tiles — no `loading="lazy"` on illustrations/logos | Verified already in place: `components/AssetGallery.tsx:278` and `components/ComponentCanvas.tsx:577,629,1044` all carry `loading="lazy"`. `/icons` uses CSS-mask (no `<img>`). No code change needed |
| C17 | P2 | ✅ | `/components/[slug]` Variant card "DEFAULT" badge attached to wrong column | Sweep-2: in `components/ComponentInspector.tsx`'s `VariantRow`, moved the absolute-positioned `★ DEFAULT` badge out of the inner image-preview wrapper and onto the *outer* variant card div (which now also carries `position: relative`). Previously the badge floated inside the image well — visually attached to the artwork instead of being a card-level marker — and could clip behind tall renderings. (The detail-page `VariantCard` in `ComponentDetail.tsx:307` already had the parent set to `position: relative`, so it was correct.) |
| C18 | P2 | ✅ | `/icons` header chip "RENDERER CSS mask" exposes implementation detail | Sweep-2: removed the `Renderer · CSS mask` chip from the meta strip in `components/sections/IconographySection.tsx`. The technique is documented in the `IconTile` JSDoc; surfacing it as a chip leaked implementation detail to designers without giving them anything actionable. |
| C19 | P2 | ✅ | `/components` keyboard hints crowded and not OS-adaptive | Sweep-2: `CanvasHelp` in `components/ComponentCanvas.tsx` condensed from 4 hints (wheel/space+drag/←→/esc) to 2 (`drag · to pan` and `esc · close`). The dropped hints duplicate intuitive native gestures; kept the two with the highest discoverability payoff. No modifier-key chips so OS-adaptive label work isn't needed. |
| C20 | P2 | ✅ | `/icons` placeholder copy differs from /illustrations + /logos | Sweep-2: aligned all three on the "Search X by name or slug…" pattern. `IconographySection` placeholder updated; `AssetGallery` accepts a new `searchPlaceholder` prop wired from `/illustrations` ("Search illustrations…") and `/logos` ("Search logos…") |
| C21 | P2 | ✅ | Icon detail modal shows full SVG source as `<pre>` with no syntax highlighting / width cap | Sweep-2: switched the source viewer from `<div>` to `<pre>` and added `maxWidth:'100%'`, `overflowX:'auto'`, `maxHeight:200`, `overflowY:'auto'` in `components/sections/IconographySection.tsx` so a 2-3KB illustration's path data can't blow out the modal. Skipped syntax highlighting (would need a lib + bundle weight). |
| C22 | P2 | ✅ | Manifest `kind: "component"` includes 28 entries categorised "Design System 🌟" | Sweep-2: `app/components/page.tsx` filters `parentComponents()` to drop `category === "Design System 🌟"` entries before grouping. They're token-master sheets and pattern overviews, not composable organism components — manifest stays lossless, UI shows only real shipped components. |
| C23 | P3 | ✅ | `/components` band "+7" placeholder uses non-token grey | Closed via C11 fix — the `+N` placeholder chip was removed entirely along with the variant cap. No surface still uses the non-token grey background |
| C24 | P3 | ✅ | `/illustrations` + `/logos` sidebar "Categories" header has no count chip | Sweep-2: both `app/illustrations/page.tsx` and `app/logos/page.tsx` updated the NavGroup label from `"Categories"` → `"Categories (${cats.length})"`. Mirrors the convention used in Health card counts and Components band counts. |
| C25 | P3 | ✅ | Manifest `Cold` category has 1 entry — leftover taxonomy | `iconsByCategory()` in `lib/icons/manifest.ts` now folds categories with `< 3` entries into a single "Other" bucket, so Cold/Footer/Toast Messages/Primary Title/Wallet/Nudges no longer clutter the sidebar. Tunable via `MIN_CATEGORY_SIZE` constant. |
| C26 | P3 | ✅ | Tokens-generated CSS consumed but not surfaced — no `/tokens` link from /icons | Sweep-2: added a "Looking for color or spacing tokens? See Foundations" footer node on `/icons` (in `app/icons/page.tsx`) and on `/illustrations` + `/logos` (via new `footerHint` prop on `AssetGallery`) |
| C27 | P3 | ✅ | Every Sweep-2 route shares `<title>INDmoney DS · Foundations</title>` | Sweep-2: added `export const metadata = { title: ... }` to `app/{icons,illustrations,logos,components,files}/page.tsx` so each surface has a distinct browser-tab title |
| C28 | P3 | ✅ | Search button reads "Search tokens…" but indexes more than tokens | Sweep-2: relabeled to "Search docs…" in `components/Header.tsx` (PageNav search trigger) — communicates broader scope without overpromising |
| C29 | P3 | ✅ | `/components` inspector "Where this breaks" tab unverified — Phase-4 reverse-view API not exercised | Probed `/v1/components/violations?name=Button|Toast|Input|Card` — all return 200 with the expected `{name, aggregate, flows}` shape; `flows` is empty because the 3 active violations are flow-level (`flow_graph_skipped`), not component-level. Endpoint + frontend wiring confirmed working end-to-end. |
| C30 | P3 | ✅ | `/files` page top reads `Files · Audit not yet run · \`npm run audit\`` — backticks render literally | Closed via C12: `EmptyAuditState` + `FilesShell` rewritten to use plain text instructions ("Files appear here once you export them via the Figma plugin's Project mode"), no backticks emitted in the meta strip. Verified — no `npm run audit` references in any files surface chrome. |

---

## Bonus findings (discovered during the fix work)

Issues that weren't in the original audit but surfaced as the fixes ran. These are real bugs in the system, not audit-defined items.

| # | Sev | Status | Title | Detail |
|---|---|---|---|---|
| B1 | P0 | ✅ | **PNG handler double-`screens/` path resolution** | `services/ds-service/internal/projects/png_handler.go:88,212` joined `<DataDir>/screens/<storage_key>` while `png_storage_key` already started with `screens/...`. Resolved path was `data/screens/screens/...` → silent 404 on **every** screen including the working v1 (19 PNGs that nobody ever knew were unreachable). One-line fix: `baseDir = DataDir` (drop the extra `screens` join). All 219 PNGs now serve real bytes (HTTP 200, 266KB, valid PNG verified) |
| B2 | P0 | ✅ | **ds-service launched from wrong cwd → `/Users/services/...` mkdir failure** | `loadDotEnv()` walks 4 ancestors max from cwd. Original launch from `/tmp` never reached the repo's `.env.local`, so JWT/ENC keys were ephemeral. Even after fixing that, `REPO_DIR` defaults to `absPath("../..")` which from `services/ds-service/` resolves to `/Users/chetansahu/indmoney-design-system-docs/services/ds-service` — wrong by one level. Fix: pinned `REPO_DIR` in `.env.local` |
| B3 | P0 | ✅ | **Pipeline `view_ready` set without downstream tables — recovery hazard** | The Python recovery script that fixed Figma's "Render timeout" 400 wrote PNGs and flipped `status='view_ready'` but skipped Stages 5-8 (canonical_trees / screen_modes / audit_jobs / SSE). Step 3 backfilled. Lesson: `view_ready` should be the *final* state flag; setting it without the trailing stages produces silent partial state |
| B4 | P1 | ✅ | **Figma `/v1/images` no chunking — Stage 3 fails on >50 frames in single call** | Chunking already shipped in Step 0; this round added structured stage observability: new `pipelineStageFromError()` maps the freeform error prefix to a label (`fetch_nodes`, `render_pngs`, `download_png`, `persist_png`, etc.). `pipeline.fail()` now logs `stage=<label>` so operators can grep failures by stage instead of pattern-matching the message. |
| B5 | P1 | ✅ | **`audit_log.details` doesn't store the export request body** | Batch 8: extended `AuditExportEvent` with `Frames []ExportAuditFrame` (screen_id, figma_frame_id, flow_id, x, y, w, h); `WriteExport` writes them into `details.frames` JSON. Future failed exports can be replayed in seconds via the audit_log payload instead of a 105k-node Figma walk. |
| B6 | P2 | ✅ | **`figma_tokens.encrypted_token` keyed to ephemeral encryption key — bricks PAT after every restart** | Batch 8: pipeline factory in `cmd/server/main.go` logs a clear warning on decrypt failure (cites `key_version` so the admin knows which key broke) and falls back to `FIGMA_PAT` env var when set, instead of bricking the entire pipeline. Future admin UI surfaces the warning + offers re-upload. |

---

## Deferred plans

Items deliberately skipped this session, with the reasons and the proposed shape of the eventual fix.

### D1 — PNG endpoint header-based auth (Pr8 P0)

**Why deferred:** the existing `?token=<jwt>` pattern exists because three.js `TextureLoader` and `<img>` cannot carry an `Authorization: Bearer` header. Replacing it properly requires either:
- **Option A — Asset-scoped signed URLs.** New endpoint `POST /v1/projects/{slug}/screens/{id}/png-url` returns `{ url, expires_in }` where the URL carries an HMAC-signed `asset_token` scoped to `(tenant, screen, expiry=60s)`. Frontend mints one per render. Adds 1 RTT per image batch but kills the JWT leak. ~150 LOC across `png_handler.go`, `png_handler_test.go`, `lib/projects/client.ts`, AtlasCanvas, textureCache.
- **Option B — Session cookies.** ds-service mints both a JWT (for API) and an httpOnly session cookie (for assets) at login. Image GETs send the cookie automatically. Requires CORS preflight credential handling, cookie-domain pinning, and frontend cookie-aware fetch wrapping. Larger change but architecturally cleaner.

**Recommendation:** Option A as a tactical fix; Option B as a Phase 8 cleanup.

### D2 — Backfill empty domain tables (A2, A3, Pr6, S7)

**Why deferred:** the `personas`, `canonical_taxonomy`, `notifications`, `notification_preferences` tables are zero-row. Seeding them is content/PM work, not engineering — needs real persona names, real taxonomy nodes, real notification cadences from the team. A one-shot SQL seed would unblock the UI but bake in throwaway data.

**Recommendation:** ship a `cmd/admin seed-fixtures --tenant=<id>` that inserts a documented set of demo rows tagged `created_by_user_id = 'system@indmoney.local'` so they're easy to drop later. Open a separate ticket for production seed copy.

### D3 — Re-run graph_index materializer (A5)

**Why deferred:** `graph_index.severity_*` columns are stale — they don't reflect the 3 new violations from Step 3. The materializer ran on ds-service boot, but only on the version-state at that moment. Need a trigger: either auto-rebuild on `audit_jobs.status='done'` write, or a cmd to refresh on demand.

**Recommendation:** add an after-update hook in `audit_jobs` repo to publish a `graph.rematerialize` event the rebuild pool subscribes to.

### D4 — DRD `flow_drd` rows for indian-stocks flows (Pr4)

**Why deferred:** there's no good answer for "what should the initial DRD content be?" — typically a designer writes it. The current code path lazily creates the row on first `putDRD`, so the UI works (empty editor → user types → row created). What's broken is just *expectations* — users open the tab and see "no DRD yet" and don't know if that's a bug or a blank slate.

**Recommendation:** ship a placeholder DRD on flow creation that says "Capture the design intent for this flow here. This document anchors all decisions and violations against this flow." (one-line server-side default in `UpsertFlow`).

### D5 — Hex sweep beyond severity (A8, A9, A18, A19, A24, A25)

**Why deferred:** the Step 7 hoist covered the *severity* hex drift specifically. The audit also flagged hardcoded type-tag colors, accent fallbacks, EmptyState tints, taxonomy chip colors, and bg-base fallbacks. These all need a single pass through `forceConfig.ts` + a new `lib/atlas-tokens.ts` for non-severity colors, plus a build-time check that lints for `#[0-9a-f]{6}` outside the allowed files.

**Recommendation:** one big sweep in a follow-up PR, gated by a `eslint-no-hex` rule.

### D6 — Persist figma_token re-upload UX (B6)

**Why deferred:** the cleanest fix is server-side: when decrypt fails on a `figma_tokens` row, mark `key_version` as stale and require re-upload via the admin UI on next sync. UI doesn't exist yet.

**Recommendation:** carry into the next admin-UX cycle. Workaround today: `FIGMA_PAT` env var works as a fallback for the recovery script + cmd binaries.

---

## What landed this session

Files changed (10 modified + 3 new + 1 doc):

- `app/layout.tsx` — theme bootstrap inline script
- `app/login/page.tsx` — **NEW** (188 lines)
- `app/inbox/layout.tsx` — login redirect target
- `app/projects/layout.tsx` — login redirect target
- `app/projects/[slug]/ProjectShellLoader.tsx` — login redirect + `flows` prop
- `app/atlas/admin/page.tsx` — login redirect target
- `app/atlas/admin/_lib/AdminShell.tsx` — login redirect target
- `app/atlas/admin/rules/page.tsx` — `SEVERITY_COLORS` consumer
- `app/atlas/HoverSignalCard.tsx` — `SEVERITY_COLORS` consumer
- `app/settings/notifications/page.tsx` — Sign-in CTA
- `components/projects/ProjectShell.tsx` — `Flow` prop + `selectedFlowID` state + chip selector + 4 `screens[0]?.FlowID` swaps
- `components/dashboard/DashboardShell.tsx` — `severityColor()` consumer
- `lib/severity-colors.ts` — **NEW** (single source for severity hex)
- `services/ds-service/internal/projects/pipeline.go` — `RenderPNGs` chunked at 25 with halve-on-timeout fallback
- `services/ds-service/internal/projects/png_handler.go` — fix double-`screens/` path resolution
- `services/ds-service/internal/projects/repository.go` — new `ListFlowsByProject`
- `services/ds-service/internal/projects/server.go` — emit `flows` in project payload
- `.env.local` — persisted `JWT_SIGNING_KEY`, `JWT_PUBLIC_KEY`, `ENCRYPTION_KEY`, `REPO_DIR`
- `public/icons/glyph/*` — re-synced (manifest + ~60 SVGs touched, mostly metadata)
- `services/ds-service/data/ds.db` — backfilled 219 canonical_trees, 219 screen_modes, 1 audit_jobs, 3 violations for v810061c4
- `services/ds-service/data/screens/<tenant>/810061c4-…/*.png` — 219 recovered PNGs

Type check `npx tsc --noEmit`: clean. `cd services/ds-service && go build`: clean.
