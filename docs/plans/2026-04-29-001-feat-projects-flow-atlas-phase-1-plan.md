---
title: "feat: Projects · Flow Atlas — Phase 1 of 8 (plugin → project view round-trip)"
type: feat
status: active
date: 2026-04-29
deepened: 2026-04-29
origin: docs/brainstorms/2026-04-29-projects-flow-atlas-requirements.md
---

# feat: Projects · Flow Atlas — Phase 1 of 8 (plugin → project view round-trip)

> **This is Phase 1 of an 8-phase product build.** See [Phased Delivery Roadmap](#phased-delivery-roadmap) below for the full arc. Phase 1 ships the foundation everything else hangs off (~3 weeks, 12 implementation units).

## Overview

Phase 1 of the Projects · Flow Atlas product (origin: [`docs/brainstorms/2026-04-29-projects-flow-atlas-requirements.md`](../brainstorms/2026-04-29-projects-flow-atlas-requirements.md)) — a four-lens system (Atlas + DRD + Violations + Mind graph) over a unified design knowledge graph for a 300-person org.

**Phase 1 delivers an end-to-end vertical slice in ~3 weeks:**

- A 4th plugin mode "Projects" that smart-groups selected frames by section, auto-detects light/dark mode pairs via `explicitVariableModes`, and submits to a new backend endpoint.
- A two-phase backend pipeline: fast preview ≤15s p95 (raw JSON pulled, PNGs rendered + size-capped, screens persisted, mode pairs deduped to canonical_trees) + async audit job ≤5min p95.
- A new `/projects/[slug]` route in the docs site with a 4-tab shell (DRD · Violations · Decisions · JSON), atlas surface (r3f frames at preserved x/y), theme + persona toggles, and a cinematic page-load animation choreography that establishes the visual language all subsequent phases build on.
- Foundational infra: SQLite schema with full tenant scoping + FK enforcement + migration discipline, SSE event stream with ticket-based auth, in-process worker pool for the audit queue, animation library (GSAP + ScrollTrigger + Lenis) that Phase 6 click-and-hold-zoom-into-node hangs off.

**Phase 1 deliberately stubs:**
- Audit engine extensions (theme parity / cross-persona / a11y / flow-graph) — Phase 2.
- BlockNote multi-user collab via Yjs — Phase 5.
- Mind graph (`/atlas` route, click-and-hold-zoom-into-node) — Phase 6.
- Auto-fix in plugin — Phase 4.
- Per-resource ACL grants table — Phase 7. Phase 1 piggybacks on existing `cmd/server` JWT roles with denormalized `tenant_id` + scoped repository pattern as the trust boundary.

The plan ships intentionally as a vertical slice: a designer can take a Figma file, export sections via plugin, see them in the docs site at preserved x/y with cinematic animation, toggle themes, browse raw JSON, and the existing audit core's color/spacing/radius drift findings render in the violations tab unchanged.

---

## Phased Delivery Roadmap

This is **Phase 1 of 8**. Subsequent phases get their own `ce-plan` invocations.

| Phase | Title | Outcome | Est. weeks |
|------|-------|---------|------------|
| **1** (this plan) | **Round-trip foundation** | Plugin Projects mode → backend two-phase pipeline → project view with atlas + 4 tabs + theme/persona toggles + cinematic animation foundation. Existing audit core surfaces unchanged in Violations tab. | 3 |
| 2 | Audit engine extensions | Theme parity (mode-pair structural diff), cross-persona consistency, WCAG AA accessibility, flow-graph (dead-end / orphan / cycle). Audit fan-out worker on rule/token publish. Migration of existing `lib/audit/*.json` sidecars into SQLite. | 3-4 |
| 3 | Atlas surface polish | Bloom + ChromaticAberration postprocessing. KTX2 / Basis Universal texture compression. InstancedMesh for >50-frame sections. LOD tiles. Camera-fit + theme-toggle animation choreography refinements. | 2-3 |
| 4 | Violation lifecycle + designer surfaces | Active → Acknowledged → Fixed/Dismissed lifecycle. Designer personal inbox `/inbox`. Per-component reverse view. DS lead dashboard. Auto-fix in plugin (token + style class only, ~60% violation coverage). | 3 |
| 5 | DRD multi-user collab + decisions | Hocuspocus collaboration server. Yjs persistence (sqlite-backed). Custom blocks `/decision` `/figma-link` `/violation-ref` with full data wiring. Decisions first-class entities + supersession chain. Comment threads. Paste-from-Notion / paste-from-Word handlers. | 3-4 |
| 6 | **Mind graph** (`/atlas`) | react-force-graph-3d "2D human brain" with bloom postprocessing. **Click-and-hold-to-zoom-into-node** progressive zoom interaction (resn.co.nz-style hold mechanic). Filter chips for edge classes. Hover signal cards. Recursive expand. Shared-element morph leaf↔project view. Mobile/Web universal toggle crossfade. | 4-5 |
| 7 | Auth, ACL, admin | Per-resource grants table + Next.js middleware for route-group gating. DS lead admin surfaces (rule curation editor, taxonomy curation tree, persona library approval). Audit log infra. Notifications (in-app inbox + Slack/email digest). | 3 |
| 8 | Search, asset migration, activity feed | Pagefind-style global search across flow names + DRD + decisions. Per-flow activity feed. Asset migration to S3 + CDN with signed URLs. | 2-3 |

**Total scope:** ~50 implementation units across 8 phases ≈ 24-30 weeks of compounding delivery.

---

## Problem Frame

Designers ship product flows by stitching screens in Figma and handing off via Slack threads, Notion docs with pasted screenshots, and FigJam boards. Engineering plays archaeologist. The current docs site shows components and tokens but doesn't carry the *flow* — there's no surface that says "here's how Onboarding works in Indian Stocks for a KYC-pending user, in light + dark, and here's what's wrong with it."

Phase 1 plants the spine: section-as-canvas storage, theme-toggle-via-Variable-Modes (verified live against `INDstocks V4` on 2026-04-29 — same node tree, only `explicitVariableModes` differs), the project view that subsequent phases hang DRD/Violations/Decisions/Mind-graph off, and the animation philosophy library that Phase 6's click-and-hold mind-graph zoom + Phase 3's atlas postprocessing all build on.

---

## Animation Philosophy

Phase 1 establishes the animation language. The references:

- **[mhdyousuf.me](https://www.mhdyousuf.me/)** — GSAP-driven, terminal aesthetic, snappy micro-interactions, smooth-scroll, scroll-triggered character/word stagger reveals, magnetic cursor on interactive elements, Cmd+K command palette pattern, monospace + bright accent contrast, sub-400ms easing on UI affordances.
- **[resn.co.nz](https://resn.co.nz/#)** + **[/work/all](https://resn.co.nz/#!/work/all)** — soothing scroll-driven gallery transitions, atmospheric WebGL backgrounds, **click-and-hold mechanic** where holding the mouse progressively charges/zooms-deeper-with-visual-feedback (particles, edge thinning, label brightening) and releasing commits the action; release-too-early springs back. Smooth easing curves (cubic-bezier, spring physics), 800-1200ms transition windows, never abrupt cuts.

**Synthesis for our product:**

| Surface | Phase | Animation treatment |
|---------|-------|---------------------|
| Page-load `/projects/[slug]` | 1 (this plan) | GSAP timeline: toolbar fades + slides down (~400ms ease-out), atlas frames stagger-fade-up (~80ms per frame, ~600ms total), tabs slide in (~500ms). Lenis smooth-scroll on the page. mhdyousuf-style snappy reveal, no overdone bounce. |
| Tab switches (DRD ↔ Violations ↔ JSON) | 1 | GSAP curtain-wipe (~300ms) — outgoing tab fades + slides up, incoming slides up from below. Active-tab indicator on the tablist morphs via Framer Motion `layoutId`. |
| Theme toggle (light ↔ dark) | 1 | Atlas textures crossfade (~400ms cubic-bezier) — texture URL swap on each AtlasFrame; old + new render simultaneously briefly with opacity tween. JSON tab values pulse the bound chips on swap. |
| Atlas hover (frame in canvas) | 1 | r3f material scale 1 → 1.015 (~200ms spring), subtle edge glow on the textured plane. Cursor magnetic pull when within 80px of frame center (mhdyousuf-style). |
| Atlas click-to-snap | 1 | Camera dolly to fit frame to viewport (~600ms ease-in-out). JSON tab simultaneously cross-fades to focus that screen. |
| Violations row hover | 1 | Severity chip pulses + breadcrumb characters stagger-reveal left-to-right (mhdyousuf type-on, ~30ms per char). |
| DRD page open | 1 | BlockNote editor mounts behind a brief mask wipe — content fades up. |
| **Mind graph click-and-hold-zoom-into-node** | **6** | **resn.co.nz mechanic.** While mouse held on a node: camera progressively dollies toward the node (linear-to-quintic ease); the node's label brightens; orbiting particles converge inward; edges to children fade in; a thin ring under the cursor charges (0 → full in ~800ms). Release within charge window → commit zoom (camera lands at child layer). Release before charge complete → spring back, particles dissipate. Visual feedback at every moment under the cursor. |
| Mind graph leaf → project view | 6 | Shared-element morph (~600ms) — leaf circle + label tween into project view title bar (Framer Motion `layoutId` from r3f-projected NDC coords to DOM). Brain dissolves; canvas + tabs render behind. |

**Tech stack added in Phase 1:**
- **GSAP 3.13+** + **GSAP ScrollTrigger** — page-load timelines, tab transitions, hover micro-interactions, scroll-triggered reveals on long DRDs and violation lists
- **Lenis 1.x** — smooth-scroll for the project page (subtle; auto-disabled with `prefers-reduced-motion`)
- **Framer Motion 11+** (already in repo) — `layoutId` shared elements, gesture handling for the future click-and-hold

**Bundle impact:** GSAP ~30KB gz (core + ScrollTrigger), Lenis ~10KB gz. Folded into the `animations` chunk (see Bundle Budgets in Documentation).

**Accessibility:** Every animation respects `prefers-reduced-motion: reduce` — replaced by instant transitions. The Lenis smooth-scroll instance is disabled entirely under reduced-motion.

---

## Requirements Trace

This plan advances a subset of the origin doc's 27 requirements; remaining requirements ship in subsequent phases (see Phased Delivery Roadmap).

**Phase 1 advances:**

- **R1** — Plugin gains a 4th mode "Projects" (full implementation: selection grouping, mode-pair detection, modal preview, submission)
- **R2** — Smart-grouping in plugin (auto-detect sections + light/dark mode pairs; designer can split/merge/ungroup before Send)
- **R3** — Hybrid two-phase backend pipeline (Phase 1 fast preview ≤15s; audit job stub returns existing audit core results — full new rules in Phase 2 plan)
- **R4** — Mode-pair storage (canonical_tree split into separate `screen_canonical_trees` table per H5; viewer toggles by re-resolving Variables; verified mechanism via `INDstocks V4` probe)
- **R5** — Versioning (every export creates new immutable Version; old versions readable; DRD per-flow-living from this plan onward)
- **R6** — Re-export resolution (match by `(tenant_id, file_id, section_id, persona_id)`; idempotency_key prevents concurrent-export races)
- **R12** — Project view (top-half atlas, bottom-half 4-tab shell, theme + persona toggles in canvas chrome, GSAP-driven page-load choreography)
- **R13** — DRD tab (BlockNote editor — single-editor for Phase 1; revision-counter ETag for autosave race; Yjs collab Phase 5)
- **R14** — Violations tab (lists existing audit core output filtered by active persona × theme; new rule classes Phase 2)
- **R16** — JSON tab (tree viewer over canonical_tree; mode + persona resolved at render; default-collapsed at depth ≥2; lazy-fetch from `screen_canonical_trees` table on demand)
- **R25** — Mobile vs Web separate IA trees (platform tagged on every Project; toggle ships in Phase 6, but data partitioned correctly from Phase 1)
- **R26** — Persona library curated by DS lead with designer free-extend (Phase 1 ships storage with `ON CONFLICT` race-safety + plugin pickers; admin curation surface Phase 7)

**Origin actors:** A1 (Designer-own-product), A2 (Designer-other), A3 (DS lead), A4 (PM), A5 (Engineer), A6 (ds-service), A7 (Plugin), A8 (Docs site)

**Origin flows:** F1 (Designer exports), F2 (Hybrid pipeline lands export), F3 (Open project view), F4 (Re-export creates Version) — these four ship in Phase 1. F5–F9 deferred.

**Origin acceptance examples:** AE-1 (Designer exports a flow — full Phase 1), AE-2 (Theme parity — DEFERRED Phase 2), AE-3 (Cross-persona — DEFERRED Phase 2), AE-4 (DRD + decision — partial Phase 1 / full Phase 5), AE-5 (Mind graph reverse lookup — DEFERRED Phase 6), AE-6 (Re-export preserves DRD — full Phase 1), AE-7 (Token publish fans out — DEFERRED Phase 2), AE-8 (Mind graph → flow morph — DEFERRED Phase 6).

---

## Scope Boundaries

### Deferred for later
*(Carried verbatim from origin — product/version sequencing.)*

- Branch + merge review workflow.
- Comprehensive auto-fix beyond token + style class.
- Cross-platform side-by-side comparison surfaces.
- Live mid-file audit (in-Figma "linter while you design" mode).
- PRD / Linear / Jira integration.
- AI-suggested decisions / DRD drafts.
- Mobile designer app / iPad viewer.
- Public read-only sharing (external vendors / candidates).

### Outside this product's identity
*(Carried verbatim from origin — positioning rejection.)*

- Replacing Figma. No editing of frames in our atlas.
- Replacing Notion / Confluence org-wide. DRD is anchored to a flow only.
- Replacing Linear / Jira. Violations are not tickets; decisions are not tasks.
- Replacing Mobbin. We are about INDmoney's own system.
- Hard governance / blocking PRs. Authority stays advisory.

### Deferred to Follow-Up Work
*(Plan-local — implementation work intentionally split into subsequent ce-plan invocations. See Phased Delivery Roadmap.)*

- Phases 2–8 each get their own ce-plan — tracked above.

---

## Context & Research

### Relevant Code and Patterns

- `services/ds-service/internal/audit/engine.go` — pure-engine pattern; `Audit(tree, tokens, candidates, opts)` returns `AuditResult`. Phase 1 worker calls this through a new `RuleRunner` interface (so Phase 2 can swap in `auditTheme`/`auditPersona`/`auditA11y`/`auditFlow` siblings without rewriting `worker.go`).
- `services/ds-service/internal/audit/types.go` — `SchemaVersion = "1.0"` constant. **Stays at 1.0**; Phase 1 introduces a new `ProjectsSchemaVersion = "1.0"` in the new `internal/projects/types.go` namespace to avoid colliding with existing audit JSON sidecar consumers.
- `services/ds-service/internal/audit/persist.go` — atomic write (`.tmp` → `os.Rename`) + `persistMu`. Mirror for any new sidecar.
- `services/ds-service/internal/db/db.go:49` — `migrate(ctx)` runs inline migrations on startup. Phase 1 introduces a `schema_migrations(version, applied_at)` table + numbered migration files at `services/ds-service/migrations/NNNN_description.up.sql` to enable forward-only column-add discipline.
- `services/ds-service/internal/auth/auth.go` — JWT + bcrypt + AES-GCM with role enum. `auth.Claims.TenantID` is the source of truth for tenant scoping. Phase 1 piggybacks; per-resource grants Phase 7.
- `figma-plugin/code.ts` (956 lines) — discriminated `MessageFromUI` / `MessageToUI` unions; `pollHealth` every 5s. Projects mode adds union members + `runProjects()` posting to a new endpoint.
- `figma-plugin/ui.html` (1910 lines) — `.modes` tablist with 3 buttons (`mode-publish`, `mode-audit`, `mode-library`) + `.mode-underline`. Adding 4th mode = 4th button + 4th view + update underline transform math.
- `figma-plugin/manifest.json` — `documentAccess: "dynamic-page"` already set; `figma.loadAllPagesAsync()` required before walking. Phase 1 adds the production ds-service domain to `networkAccess.allowedDomains` (NOT a placeholder string — that risks Figma manifest review rejection).
- `app/components/[slug]/page.tsx` — `generateStaticParams` + `notFound()` + `FilesShell`. Project pages are NOT SSG (data is per-tenant + dynamic) — server components with route-handler-fetched data instead.
- `app/api/sync/route.ts` — `X-Trace-ID` header convention; same key carries through to SSE events.
- `lib/auth-client.ts` — zustand `persist` store, `login()` direct to ds-service, `triggerSync()` via `/api/sync` proxy. Phase 1's project-view auth check piggybacks; SSE auth uses a separate ticket model (see U2).
- `playwright.config.ts` — single project `indmoney-light` (Desktop Chrome). New tests under `tests/projects/`.

### Institutional Learnings

`docs/solutions/` does not exist in this repo (verified). No prior captured learnings for r3f, BlockNote, Yjs, Figma plugin, Go job queues, audit/drift detection, ACL patterns, large-manifest performance, Variable Modes, force-directed layouts, GSAP/Lenis, or click-and-hold WebGL interactions. This plan is among the first major artifacts in these areas; future learnings should be captured under `docs/solutions/` once Phase 1 ships.

### External References

- [react-three-fiber v9 migration](https://r3f.docs.pmnd.rs/tutorials/v9-migration-guide) — requires React 19 (Next 16 ships 19.2.4 by default).
- [Next.js 16 release notes + Turbopack changes](https://nextjs.org/blog/next-16) — `experimental.turbopack` removed; `transpilePackages: ['three', '@react-three/fiber']` may be required.
- [pmndrs/react-three-fiber#3595](https://github.com/pmndrs/react-three-fiber/issues/3595) — Next 16 `componentCache` breaks R3F back/forward navigation. Mitigation: wrap Canvas in `<Suspense>` with remount key on `usePathname()`.
- [BlockNote 0.47+ docs](https://www.blocknotejs.org/docs) — `createReactBlockSpec(blockConfig, blockImplementation)`, `BlockNoteSchema.create({ blockSpecs })`, collaboration via `collaboration: { provider, fragment, user }`.
- [Hocuspocus persistence guide](https://tiptap.dev/docs/hocuspocus/guides/persistence) — Phase 5; deferred.
- [Figma plugin manifest 2026](https://developers.figma.com/docs/plugins/manifest/) — `documentAccess: "dynamic-page"` strongly recommended; `figma.loadAllPagesAsync()` required before walking.
- [GSAP 3.13 + ScrollTrigger](https://gsap.com/docs/v3/) — for page-load + tab + hover animations.
- [Lenis 1.x](https://lenis.darkroom.engineering/) — smooth-scroll provider.
- [react-force-graph](https://github.com/vasturiano/react-force-graph) — Phase 6 for the mind graph (with custom click-and-hold layer on top).
- [resn.co.nz](https://resn.co.nz/) — animation philosophy reference (click-and-hold, soothing transitions, atmospheric WebGL).
- [mhdyousuf.me](https://www.mhdyousuf.me/) — animation philosophy reference (GSAP-driven, terminal aesthetic, snappy micro-interactions).

### Cross-cutting tech context

- **Stack:** Next.js 16.2.1 + React 19.2.4. Tailwind v4. Pagefind for search. Framer Motion installed. `tsc --noEmit` is the lint command; no ESLint.
- **Backend:** Go (stdlib `net/http` with method-prefix routing). `modernc.org/sqlite` (no CGO). JWT (`golang-jwt/jwt/v5`). No worker queue, no SSE infra, no chi/echo/gin.
- **Plugin:** Vanilla JS in `ui.html`. TypeScript `code.ts` compiled via `npx tsc -p .`.

---

## Key Technical Decisions

### Storage & schema

- **SQLite tables, not JSON sidecars.** Existing `lib/audit/*.json` sidecar pattern works for read-only single-tenant audit data, but Projects are write-heavy, multi-user, multi-version, and need ACL filtering at query time. SQLite at `services/ds-service/data/ds.db` already exists.
- **Tenant scoping is denormalized + scoped-repo enforced.** Every table except `personas` (org-wide) and `schema_migrations` carries a `tenant_id NOT NULL` column with index. Repository pattern: `internal/projects/repository.go` exports a `TenantRepo` constructor that takes `tenantID` once and silently injects `WHERE tenant_id = ?` into every query. Plain `Repo` is unexported. Forbid raw SQL outside the repo.
- **FK enforcement turned ON.** `PRAGMA foreign_keys = ON` set per connection in `internal/db/db.go`. All FKs declared with explicit cascade rules:
  - `flows.project_id → projects(id) ON DELETE RESTRICT` (force soft-delete)
  - `project_versions.project_id → projects(id) ON DELETE RESTRICT`
  - `screens.version_id → project_versions(id) ON DELETE CASCADE`
  - `screens.flow_id → flows(id) ON DELETE RESTRICT`
  - `screen_canonical_trees.screen_id → screens(id) ON DELETE CASCADE`
  - `screen_modes.screen_id → screens(id) ON DELETE CASCADE`
  - `violations.version_id → project_versions(id) ON DELETE CASCADE`
  - `violations.persona_id → personas(id) ON DELETE SET NULL` (preserve historical violations even if persona is removed)
  - `audit_jobs.version_id → project_versions(id) ON DELETE CASCADE`
  - `flow_drd.flow_id → flows(id) ON DELETE RESTRICT` (DRD is the most precious user-authored content)
- **Soft-delete by default.** `projects.deleted_at`, `flows.deleted_at`, `personas.deleted_at`. Repo defaults to `WHERE deleted_at IS NULL`. Hard-delete only via admin tool with runbook entry.
- **`canonical_tree` lives in its own table (`screen_canonical_trees`).** A 50KB JSON BLOB per screen × 30 screens × atlas list queries that select `*` would pull megabytes the UI doesn't need. Move to a separate row store; JSON tab fetches lazily on click via `GET /v1/projects/:slug/screens/:id/canonical-tree`.
- **`project_versions.status` collapses to `pending | view_ready | failed`.** Audit lifecycle (`queued | running | done | failed`) lives in `audit_jobs` only — no denormalized state drift. UI joins versions LEFT JOIN audit_jobs to derive "audit_complete".
- **`screen_logical_id`** (UUID) on `screens` — stable across re-exports of the same Figma frame within a flow. Phase 1 sets it but doesn't read it; Phase 4/5 cross-version DRD-refs and violation-refs depend on it.
- **`flow_drd.revision INTEGER NOT NULL DEFAULT 0`** — monotonic counter. PUT uses `WHERE revision = ?` + `RowsAffected() == 1` for ETag concurrency. SQLite `CURRENT_TIMESTAMP` 1-second resolution would lose sub-second writes silently.
- **Audit-job idempotent execution.** Worker wraps each job in a single transaction: `BEGIN IMMEDIATE; DELETE FROM violations WHERE version_id = ?; INSERT new rows; UPDATE audit_jobs SET status = 'done'; COMMIT;`. Crash retries cannot duplicate violations.
- **Worker pool of size 1, with lease columns.** `audit_jobs.leased_by TEXT, audit_jobs.lease_expires_at INTEGER` columns shipped from day one. Phase 1 ships `WorkerPool{ size: 1 }`. Phase 2 changes the constant to 6 workers + adds heartbeat refresh. Channel-notification-on-insert (not 2s polling) to remove dead time.
- **Schema version isolation.** `internal/audit/types.SchemaVersion` stays at `"1.0"` (existing /files/[slug] consumers unchanged). New constant `internal/projects/types.ProjectsSchemaVersion = "1.0"` for the projects API surface.
- **Migration discipline.** `schema_migrations(version INTEGER PRIMARY KEY, applied_at)` table tracks applied migrations. Each migration is a numbered file at `services/ds-service/migrations/NNNN_description.up.sql`. Forward-only column-add policy: always `ALTER TABLE ... ADD COLUMN ... NULL`; never `DROP COLUMN` in same release that stops writing it; renames are 3-release dual-write + cutover + drop-old.

### Network, auth & security

- **Backend lives in `cmd/server`, not `cmd/audit-server`.** `cmd/audit-server` is zero-auth localhost-only single-user. Projects require auth + multi-tenant + ACL.
- **SSE auth via short-lived single-use ticket.** `EventSource` cannot set `Authorization` headers and JWTs in query strings leak. `POST /v1/projects/:slug/events/ticket` (authed via JWT bearer) returns a one-shot opaque token bound to `(user_id, tenant_id, trace_id, expires_at = 60s)`. EventSource connects with `?ticket=<id>`. Server invalidates on first use. SSE subscribers also receive only events whose `tenant_id` matches the user's claim.
- **PNG screenshots NOT in `public/`.** Phase 1 stores at `services/ds-service/data/screens/<tenant_id>/<version_id>/<screen_id>@2x.png` — outside the Next.js static handler. Served via `GET /v1/projects/:slug/screens/:id/png` behind `requireAuth` + tenant scoping with `Cache-Control: private, max-age=300`. Phase 8 migrates to S3 + signed URLs without changing the route shape.
- **PNG long-edge size cap of 4096px.** Server-side downsample before persisting. INDstocks-grade frames at native scale=2 hit ~9772px height which exceeds many WebGL `MAX_TEXTURE_SIZE` and blows iOS Safari's 256MB texture budget at scale. 4096px is the universally-supported ceiling.
- **Idempotency on `/v1/projects/export`.** Plugin generates a UUID `idempotency_key` per export attempt. Backend rejects duplicate keys within a 60s TTL with `409 Conflict, {existing_version_id, deeplink}`. Coalesces concurrent exports of the same flow.
- **Rate limits on `/v1/projects/export`.** Per-user 10/min; per-tenant 200/day. Per-request payload caps: max 20 flows, max 50 frames per flow, max body 10MB. Validated server-side before any Figma REST call.
- **Audit log on every export.** `audit_log` row (existing table from auth) on success AND failure: `action='project.export' | 'project.export.failed'`, `actor_user_id`, `tenant_id`, `file_id`, `project_id`, `version_id`, `ip`, `user_agent`, `trace_id`. Retention ≥1 year.
- **Phase 1 access model — explicit assumption.** Any user with `designer`-or-higher role in tenant T can read AND write any project in tenant T. Per-product editor/viewer split (origin R21) lands in Phase 7. Pre-launch products (Plutus from origin) are flagged with a tenant_admin-only `restricted_visibility` boolean if needed.
- **CSRF posture.** Phase 1 uses `Authorization: Bearer` from zustand store for all API calls; CSRF not applicable. If Phase 7 introduces cookie-based session, CSRF tokens become mandatory — flagged for that plan.
- **Input validation.** All plugin-supplied strings (`product`, `path`, `persona_name`, `name`) capped at 256 chars, allowlist regex `[\w \-_/&·]+`, reject CR/LF/NUL. `screen_id` server-generated UUID never plugin-supplied.
- **PII classification.** New doc at `docs/security/data-classification.md` lists every new column's class (Public / Internal / Confidential / Restricted). `personas.name` (free-text) and `flow_drd.content_json` are Confidential. Right-to-deletion path documented for Phase 7.

### Atlas & rendering

- **Atlas tech in Phase 1 = react-three-fiber + drei `<OrthographicCamera>` + `<OrbitControls>` (pan/zoom-only, rotate disabled).** No bloom, no postprocessing, no instanced meshes — pure baseline. Phase 3 adds bloom + LOD + KTX2 + InstancedMesh.
- **Texture cache by URL, not per-frame `useLoader`.** A shared `TextureCache` map keyed by `screen.png_url` so theme toggles don't re-fetch when toggling back to a previously-seen mode. Disposes textures on unmount.
- **Progressive Suspense on AtlasCanvas.** Each `<AtlasFrame>` has its own Suspense boundary with a wireframe placeholder mesh; frames render as their textures resolve instead of all-or-nothing.
- **Mode-pair detection algorithm.** Within a section, group frames by `explicitVariableModes` collection ID. Pairs = same column-x (within 10px), different row-y, same VariableCollectionId, different mode ID. Cross-validate by structural skeleton diff (path/type pairs at depth=2). Delta > 0 outside `explicitVariableModes` → still pair, computed at audit time as a Phase 2 Critical violation. Phase 1 doesn't persist a `theme_parity_warning` column — Phase 2 computes from canonical_trees on demand to avoid stale-flag risk.
- **Persona handling in Phase 1.** Plugin export modal asks designer to pick from a curated list (Default, New User, Logged-out, KYC-pending, KYC-verified, F&O-activated, MTF-enabled, Plutus, Returning) + free-text "Other" that creates a Pending persona via `INSERT ... ON CONFLICT(tenant_id, name) DO UPDATE ... RETURNING id` (race-safe). Pending personas visible to suggesting designer + DS leads only until approved. DS lead approval surface deferred to Phase 7.

### Pipeline

- **Re-export resolution composite key:** `(tenant_id, file_id, section_id, persona_id)`. Stable across re-exports; new flows auto-created when no match.
- **Mid-pipeline crash recovery.** `pipeline_started_at` and `pipeline_heartbeat_at` columns on `project_versions`. Sweeper goroutine scans every 60s for `status='pending'` rows with stale heartbeat (>30s) → marks `failed` with `error="orphaned by server restart"`. Same heartbeat pattern works for Phase 2 audit fan-out.
- **`RuleRunner` interface.** `worker.go` calls `audit.Audit()` through a `RuleRunner` interface (`Run(ctx, version *Version) ([]Violation, error)`). Phase 1 ships one impl wrapping `audit.Audit` per-screen. Phase 2 adds `ThemeParity`, `CrossPersona`, `A11y`, `FlowGraph` impls and registers them in a slice; the worker iterates. Costs ~30 lines now, saves a worker rewrite later.

### Animation

- **Animation tech stack.** GSAP 3.13 (core + ScrollTrigger ~30KB gz) + Lenis 1.x (~10KB gz) + existing Framer Motion. Bundled separately as the `animations` chunk; lazy-loaded per route.
- **Shared `useAnimationContext` hook** at `lib/animations/context.ts` provides a global GSAP context for component-scoped timeline registration + cleanup, the Lenis singleton, and a `prefersReducedMotion` detector that disables Lenis and replaces GSAP timelines with instant transitions.
- **Animation choreography per surface** captured in [Animation Philosophy](#animation-philosophy) above.

### Plugin

- **Plugin mode add: 4th menu entry in same plugin, not separate plugin.** Per Figma 2026 manifest guidance, "Projects" is additional functionality in design files — extend `menu` array; branch on `figma.command` at runtime. Single plugin ID, single review submission.
- **`networkAccess.allowedDomains` updated to production domain** (e.g., `https://ds-service.indmoney.com`) plus `http://localhost:7475` for dev. NO placeholder strings — Figma re-reviews on `networkAccess` changes.

### Performance budgets (split chunks)

| Chunk | Budget | Notes |
|-------|--------|-------|
| `app/projects/[slug]` initial route shell | ≤200KB gz | RSC + ProjectShell skeleton + toolbar. r3f + atlas + DRD + animations all dynamic-imported |
| `chunks/atlas` | ≤350KB gz | three.js + r3f + drei + atlas components |
| `chunks/drd` | ≤400KB gz | BlockNote + mantine UI |
| `chunks/animations` | ≤50KB gz | GSAP + ScrollTrigger + Lenis |

Enforced by `next build --analyze` parsing in CI.

---

## Open Questions

### Resolved During Planning

- **Mode-pair edge cases (origin Q4)** — Light only → single-mode `modes: [{id: "default"}]`, no parity check. Three+ modes → store all detected; first-found canonical for diff baseline. Non-matching x-columns → nearest-x heuristic + log warning; if no candidate within 50px, skip pairing as `mode_unmatched`.
- **Permission inheritance on folder reorganization (origin Q8)** — Per-flow grants stored at `flow_id`, never at `folder_path`. Path-string moves don't move grants.
- **Persona pending pool visibility (origin Q9)** — Pending personas visible to (a) suggesting designer, (b) any DS lead, (c) any admin. Other designers see "(Pending) Foo" with reduced opacity in pickers. Approved → globally visible.
- **Storage shape for canonical_tree** — Separate `screen_canonical_trees` table (split from H5 finding). Lazy-fetched by JSON tab.
- **Deeplink format from plugin toast** — `https://docs.indmoney.com/projects/<project_slug>?v=<version_id>`. Native browser notification API + plugin in-line confirmation.
- **Phase 1 platform handling** — Designer picks platform in plugin modal; default auto-detected from frame width (<500 → Mobile; ≥1024 → Web). Stored as `projects.platform`.
- **SSE auth model** — Short-lived ticket from authed `POST /events/ticket` (60s TTL, single-use). Never JWT in query string.
- **PNG storage** — `services/ds-service/data/screens/<tenant_id>/<version_id>/<screen_id>@2x.png`. Served via authed route. Never `public/`.
- **PNG size cap** — 4096px long edge, server-side downsample before persisting.
- **Worker architecture** — Worker pool of size 1 in Phase 1 with lease columns; Phase 2 grows pool size. NOT single-goroutine-with-polling.
- **Concurrent-export race** — `idempotency_key` UUID per export attempt; 60s TTL coalesce.
- **DRD ETag** — `revision INTEGER` monotonic counter, not `updated_at`.
- **SchemaVersion** — Audit core stays "1.0"; new `ProjectsSchemaVersion = "1.0"` separate.
- **Animation references** — mhdyousuf.me + resn.co.nz; tech stack GSAP + ScrollTrigger + Lenis + Framer Motion. Click-and-hold-zoom mechanic deferred to Phase 6 mind-graph.

### Deferred to Implementation

- Exact heartbeat refresh interval for the worker pool — start with 5s, tune with profiling.
- The exact React component decomposition of `<ProjectShell>` (one big component vs `<AtlasCanvas>` + `<AtlasFrameLayer>` + `<AtlasOverlay>`) — left to implementer; unit Approach captures data flow.
- Whether to use `next/dynamic` from the page or from a wrapper component for the WebGL canvas — pick at code time based on SSR-safe scaffolding availability.
- BlockNote document persistence schema — Phase 1 ships JSON-encoded blocks; Phase 5 migrates to Yjs binary state.
- GSAP timeline sequence ordering details (e.g., exact ms offsets between toolbar slide and atlas frame stagger) — tune at implementation by feel against the mhdyousuf.me reference.

### Carried Open from origin (Phase 2+)

- **Origin Q1** Decision supersession UX — Phase 5.
- **Origin Q2** Inbox triage at scale — Phase 4.
- **Origin Q3** DRD migration on flow rename — Phase 4 / 5.
- **Origin Q5** Atlas zoom strategy — Phase 3.
- **Origin Q6** Mind graph performance ceiling — Phase 6.
- **Origin Q7** Comment portability — Phase 5.
- **Origin Q10** Slack/email digest content — Phase 7.
- **GDPR/DPDP right-to-deletion** for free-text persona names + DRD-authored content — Phase 7.

---

## Output Structure

```
app/
└── projects/                              ← NEW
    ├── layout.tsx                         ← FilesShell wrapper, gated by auth
    ├── page.tsx                           ← /projects landing
    └── [slug]/
        └── page.tsx                       ← /projects/<slug>?v=<version_id>

components/
└── projects/                              ← NEW
    ├── ProjectShell.tsx                   ← top-half atlas + bottom-half tabs
    ├── ProjectToolbar.tsx                 ← breadcrumb + theme/persona toggles
    ├── atlas/
    │   ├── AtlasCanvas.tsx                ← r3f Canvas wrapper, dynamic-imported
    │   ├── AtlasFrame.tsx                 ← textured plane per screen, hover/click micro-anim
    │   ├── AtlasControls.tsx              ← OrbitControls (pan/zoom only)
    │   └── useAtlasViewport.ts            ← camera fit + persisted zoom
    └── tabs/
        ├── DRDTab.tsx                     ← BlockNote skeleton (Phase 1) / Yjs (Phase 5)
        ├── ViolationsTab.tsx              ← lists existing audit core output
        ├── DecisionsTab.tsx               ← Phase 5; renders empty-state Phase 1
        └── JSONTab.tsx                    ← lazy-fetched canonical_tree viewer

lib/
├── projects/                              ← NEW client-side helpers
│   ├── client.ts                          ← fetch wrappers, SSEClient with ticket
│   ├── types.ts                           ← TS mirror of Go types
│   ├── resolveTreeForMode.ts              ← pure mode resolver
│   └── violationsClient.ts
└── animations/                            ← NEW
    ├── context.ts                         ← global GSAP context + Lenis singleton + reduced-motion
    ├── timelines/
    │   ├── projectShellOpen.ts            ← page-load timeline
    │   ├── tabSwitch.ts                   ← curtain wipe
    │   └── themeToggle.ts                 ← cross-fade timeline
    └── hooks/
        ├── useGSAPContext.ts
        └── useLenis.ts

figma-plugin/
├── manifest.json                          ← MODIFY: 4th menu entry; networkAccess production domain
├── code.ts                                ← MODIFY: Projects mode union + smart-grouping
└── ui.html                                ← MODIFY: 4th .mode tab + view#view-projects panel

services/ds-service/
├── cmd/server/main.go                     ← MODIFY: register /v1/projects/* routes; start worker
├── internal/projects/                     ← NEW package
│   ├── types.go                           ← Project/Version/Flow/Screen/Persona/AuditJob/Violation
│   ├── repository.go                      ← TenantRepo with scoped queries
│   ├── server.go                          ← HTTP handlers (export, get, list, events, ticket, png)
│   ├── pipeline.go                        ← Phase 1 fast-preview logic
│   ├── modepairs.go                       ← detection algorithm
│   ├── worker.go                          ← in-process worker pool of size 1, lease + heartbeat
│   ├── runner.go                          ← RuleRunner interface + Phase 1 wrap of audit.Audit
│   └── ratelimit.go                       ← per-user, per-tenant limits
├── internal/db/db.go                      ← MODIFY: schema_migrations table; PRAGMA foreign_keys
├── internal/sse/                          ← NEW package
│   ├── broker.go                          ← stdlib http.Flusher event broker
│   ├── tickets.go                         ← short-lived single-use SSE tickets
│   └── events.go                          ← typed event payloads
└── migrations/                            ← NEW (was placeholder)
    ├── 0001_projects_schema.up.sql        ← all 9 new tables + FKs + UNIQUE + indexes

tests/projects/                            ← NEW
├── plugin-export-flow.spec.ts
├── canvas-render.spec.ts
├── violations-tab.spec.ts
├── json-tab.spec.ts
├── drd-tab.spec.ts
├── tenant-isolation.spec.ts               ← cross-tenant query attempts
└── animation-reduced-motion.spec.ts       ← prefers-reduced-motion replaces timelines

services/ds-service/internal/projects/     ← Go test files inline
├── modepairs_test.go
├── pipeline_test.go
├── repository_test.go
├── worker_test.go
└── ratelimit_test.go

docs/security/                             ← NEW (Phase 1 introduces)
└── data-classification.md                 ← PII / data-class table
```

---

## High-Level Technical Design

> *This illustrates the intended approach and is directional guidance for review, not implementation specification. The implementing agent should treat it as context, not code to reproduce.*

### Phase 1 data flow

```mermaid
sequenceDiagram
    participant D as Designer in Figma
    participant P as Plugin (Projects mode)
    participant S as ds-service /v1/projects
    participant DB as SQLite
    participant W as Audit Worker Pool (size=1)
    participant U as Docs Site UI
    participant F as Figma REST API

    D->>P: Select sections + open plugin
    P->>P: Walk selection ancestry; group by SECTION; detect mode pairs via explicitVariableModes
    P->>D: Show grouping preview (modal)
    D->>P: Edit names / split / merge / Send (with idempotency_key)
    P->>S: POST /v1/projects/export (idempotency_key, file_id, flows[])
    S->>S: Auth + rate-limit + payload validation; tenant_id from JWT claim
    S->>DB: INSERT project / version (status=pending) / flows / screens (no canonical_tree yet)
    S->>DB: INSERT audit_log (action=project.export)
    S-->>P: 202 {project_id, version_id, deeplink, trace_id}
    Note over S,F: Phase 1 fast preview begins
    S->>F: GET /v1/files/{file_id}/nodes?ids=...&depth=3 (per flow)
    S->>F: GET /v1/images/{file_id}?ids=...&format=png&scale=2
    S->>S: Cap PNG long edge to 4096px; persist to data/screens/<tenant>/<version>/<screen>@2x.png
    S->>DB: BEGIN; INSERT screen_canonical_trees + screen_modes; UPDATE project_versions.status=view_ready; COMMIT
    S->>U: SSE broker.Publish(trace_id, project.view_ready)
    Note over W: Audit job (Phase 1: existing audit.Audit through RuleRunner)
    S->>DB: INSERT audit_jobs (status=queued); notify worker via channel
    W->>DB: BEGIN IMMEDIATE; UPDATE audit_jobs SET status=running, leased_by=...; COMMIT
    W->>S: runner.Run(version) → []Violation
    W->>DB: BEGIN; DELETE violations WHERE version_id; INSERT new violations; UPDATE audit_jobs SET status=done; COMMIT
    W->>U: SSE broker.Publish(trace_id, project.audit_complete)
    D->>U: Click deeplink in toast
    U->>S: POST /v1/projects/:slug/events/ticket (auth → 60s ticket)
    U->>S: GET /v1/projects/:slug?v=<id>
    S->>DB: TenantRepo: SELECT project + version + screens + screen_modes + violations WHERE tenant_id=...
    S-->>U: full project payload
    U->>U: GSAP project-shell-open timeline (toolbar + atlas frames stagger + tabs)
    U->>U: r3f Canvas mounts; frames render at preserved (x, y) progressively
    U->>S: GET /v1/projects/:slug/screens/:id/png (lazy via authed route)
    U->>S: EventSource ?ticket=... subscribes to live updates
    D->>U: Theme toggle → texture URL swap with crossfade animation
    D->>U: Click frame → JSON tab loads canonical_tree lazily (default-collapsed depth>=2)
```

### Plugin smart-grouping pseudo-grammar

```
selection = [Frame | Group | Section | …]

GROUPING:
  if frame.parent.type == "SECTION" → group_key = parent.id
  else                              → group_key = "freeform-" + index

For each group:
  candidates = group.frames
  pairs = []
  for each candidate:
    siblings = candidates where (
      |x − candidate.x| < 10
      AND y != candidate.y
      AND explicitVariableModes shares VariableCollectionId
      AND explicitVariableModes mode != candidate's mode
    )
    if siblings:
      pairs.push({primary: candidate, paired: siblings, modes: [candidate.mode, ...siblings.modes]})
  unpaired = candidates − pairs.flatten

Modal preview rows:
  one row per group, with:
    - count: paired + unpaired
    - editable: name, platform (auto-detected), product, path, persona
    - expandable: per-screen split / merge / ungroup mode-pair toggles

Submission:
  payload = { idempotency_key: uuid(), file_id, flows: [...] }
  POST /v1/projects/export
```

This sketch is directional. The implementer may discover better adjacency thresholds (10px) or smarter cross-validation than path/type diff.

### Animation timeline sketch (project view open)

```
GSAP Timeline (project-shell-open):
  0ms     → toolbar.opacity=0, y=-12
  0ms     → atlas-canvas.opacity=0
  0ms     → tab-strip.opacity=0, y=8
  0ms     → tab-content.opacity=0

  100ms   → toolbar { opacity: 1, y: 0 } ease=expo.out duration=400ms
  300ms   → atlas-canvas { opacity: 1 } ease=cubic.out duration=500ms
  300ms   → atlas-frames stagger { opacity: 1, y: 0 } ease=back.out(1.2) per-frame=80ms (max 600ms total)
  500ms   → tab-strip { opacity: 1, y: 0 } ease=cubic.out duration=400ms
  600ms   → tab-content { opacity: 1 } ease=cubic.out duration=300ms

Total: ~900ms (cinematic but not slow). Scrubable for prefers-reduced-motion (disable, set final state).
```

---

## Implementation Units

- U1. **SQLite schema with full tenant scoping, FK enforcement, migration discipline**

**Goal:** Add the new tables Phase 1 needs to a numbered migration file at `services/ds-service/migrations/0001_projects_schema.up.sql`, with `schema_migrations` tracking, denormalized `tenant_id` everywhere, FK enforcement turned on, all NOT NULL/UNIQUE constraints declared explicitly, soft-delete columns, and the `screen_canonical_trees` split.

**Requirements:** R5, R6, R26 (origin schema implications)

**Dependencies:** None — first unit.

**Files:**
- Create: `services/ds-service/migrations/0001_projects_schema.up.sql`
- Modify: `services/ds-service/internal/db/db.go` — add `schema_migrations` table; load + apply numbered migrations from `migrations/`; set `PRAGMA foreign_keys = ON` per connection
- Create: `services/ds-service/internal/db/migrations.go` — migration runner
- Modify: `services/ds-service/internal/db/db_test.go`

**Approach:**
- Tables created in dependency order. **Every** table except `personas` (org-wide) and `schema_migrations` carries `tenant_id NOT NULL` with index.
- `projects` — id, slug, name, platform, product, path, owner_user_id, tenant_id, deleted_at NULL, created_at, updated_at. UNIQUE(tenant_id, slug). Index(tenant_id, deleted_at).
- `personas` — id, tenant_id, name, status (`approved` | `pending`), created_by_user_id, approved_by_user_id, approved_at, deleted_at NULL, created_at. UNIQUE(tenant_id, name) WHERE status='approved' (partial index). Index(tenant_id, status).
- `flows` — id, project_id, tenant_id, file_id, section_id, name, persona_id NULL, deleted_at NULL, created_at, updated_at. UNIQUE(tenant_id, file_id, section_id, persona_id). FK project_id → projects(id) ON DELETE RESTRICT. FK persona_id → personas(id) ON DELETE SET NULL.
- `project_versions` — id, project_id, tenant_id, version_index, status (`pending` | `view_ready` | `failed`), pipeline_started_at, pipeline_heartbeat_at, error TEXT, created_by_user_id, created_at. UNIQUE(project_id, version_index). Index(project_id, version_index DESC). FK project_id → projects(id) ON DELETE RESTRICT.
- `screens` — id, version_id, flow_id, tenant_id, x REAL, y REAL, width REAL, height REAL, screen_logical_id (UUID — stable across re-export), png_storage_key TEXT (relative path under data/screens/), created_at. Index(version_id), Index(flow_id, screen_logical_id). FK version_id → project_versions(id) ON DELETE CASCADE. FK flow_id → flows(id) ON DELETE RESTRICT.
- `screen_canonical_trees` — screen_id PK, canonical_tree TEXT NOT NULL, hash TEXT, updated_at. FK screen_id → screens(id) ON DELETE CASCADE. Lazily-fetched by JSON tab via `GET /v1/projects/:slug/screens/:id/canonical-tree`.
- `screen_modes` — id, screen_id, tenant_id, mode_label (`light` | `dark` | `default` | string), figma_frame_id, explicit_variable_modes_json TEXT. UNIQUE(screen_id, mode_label). FK screen_id → screens(id) ON DELETE CASCADE.
- `audit_jobs` — id, version_id, tenant_id, status (`queued` | `running` | `done` | `failed`), trace_id, idempotency_key, leased_by TEXT NULL, lease_expires_at INTEGER NULL, created_at, started_at NULL, completed_at NULL, error TEXT (capped 8KB). UNIQUE(version_id) WHERE status IN ('queued','running'). Index(status, created_at). Index(trace_id). FK version_id → project_versions(id) ON DELETE CASCADE.
- `violations` — id, version_id, screen_id, tenant_id, rule_id, severity (`critical` | `high` | `medium` | `low` | `info`), property, observed, suggestion, persona_id NULL, mode_label, status (`active` | `acknowledged` | `dismissed` | `fixed`), created_at. Index(version_id, severity). FK version_id → project_versions(id) ON DELETE CASCADE. FK screen_id → screens(id) ON DELETE CASCADE. FK persona_id → personas(id) ON DELETE SET NULL.
- `flow_drd` — flow_id PK, tenant_id, content_json BLOB, revision INTEGER NOT NULL DEFAULT 0, schema_version TEXT, updated_at, updated_by_user_id. FK flow_id → flows(id) ON DELETE RESTRICT.
- `schema_migrations` — version INTEGER PRIMARY KEY, name TEXT, applied_at INTEGER.
- `internal/db/db.go` opens every pool connection with `_pragma=foreign_keys(1)` and `journal_mode=wal`. Migration runner reads numbered files from `migrations/`, inserts into `schema_migrations`, runs in transaction.

**Patterns to follow:**
- Existing inline migrations in `services/ds-service/internal/db/db.go:55-100` (replaced by numbered files).
- `services/ds-service/internal/audit/persist.go` atomic-write mutex pattern.

**Test scenarios:**
- Happy path: fresh DB starts up; all 10 new tables created; `PRAGMA foreign_keys` returns 1; idempotent re-run leaves schema unchanged.
- Edge case: existing DB with Phase-0 tables; migration adds new tables without dropping data.
- Edge case: every UNIQUE constraint enforces; duplicate `(tenant_id, file_id, section_id, persona_id)` insert fails.
- Edge case: FK violation on orphan insert (e.g., screen with no version) is rejected.
- Edge case: `ON DELETE RESTRICT` on flows blocks project hard-delete; `ON DELETE CASCADE` on screens removes screen_modes + canonical_trees.
- Edge case: soft-delete via `deleted_at` is filtered by default in `TenantRepo.ListProjects`.
- Edge case: `INSERT ... ON CONFLICT(tenant_id, name) DO UPDATE` on personas race-safe across concurrent inserts.
- Error path: malformed migration SQL fails fast at startup with clear error; server exits non-zero.
- Integration: tenant-A user cannot read tenant-B's project even with valid project ID — repo enforces `WHERE tenant_id = ?`.

**Verification:**
- ds-service starts cleanly against fresh `services/ds-service/data/ds.db`. All 10 tables visible via `sqlite3 ds.db ".schema"`.
- `PRAGMA foreign_keys` returns 1 on every pool connection.
- `go test ./internal/db/... -race` passes.
- Existing `users` / `tenants` / `figma_tokens` / `audit_log` rows remain intact.

---

- U2. **SSE infrastructure with ticket-based auth + subscriber cap + heartbeat**

**Goal:** SSE broker that lets the Phase 1 pipeline push `view_ready` and `audit_complete` events to subscribed UI clients, with proper auth (short-lived tickets, never JWT in query string), bounded subscriber count, and configurable heartbeat.

**Requirements:** R3 (hybrid pipeline progressive UI updates)

**Dependencies:** U1 (db for ticket persistence — uses in-memory map but JWT validation needs auth pkg).

**Files:**
- Create: `services/ds-service/internal/sse/broker.go`
- Create: `services/ds-service/internal/sse/tickets.go`
- Create: `services/ds-service/internal/sse/events.go`
- Create: `services/ds-service/internal/sse/broker_test.go`

**Approach:**
- Pure stdlib (`net/http` + `Flusher` + `context`). No new dep.
- `Broker.Subscribe(traceID, tenantID, userID) (<-chan Event, func())` returns a channel + unsubscribe. Internal map keyed by trace_id; events filtered by `event.tenant_id == subscriber.tenant_id`. Channel buffer 32; non-blocking publish drops events for slow subscribers (logged + counter).
- `Broker.Publish(traceID string, event Event)` non-blocking; event carries `tenant_id` and a payload struct.
- **Subscriber cap (default 1024 configurable via flag).** 503 on overflow. Goroutine-leak detection via a sentinel pattern — every subscribe registers a timer that yells if `unsubscribe()` not called within 1h.
- **Heartbeat ping every 15s (configurable; default 15)** — keeps proxies open. Configurable via `cmd/server` flag `--sse-heartbeat`.
- **Tickets**: `tickets.go` exposes `IssueTicket(userID, tenantID, traceID, ttl=60s)` returning UUID. Internal map keyed by ticket → `(user, tenant, trace, expires_at)`. Tickets are single-use: `RedeemTicket(ticketID) → (user, tenant, trace, ok)` deletes on success. Periodic GC of expired tickets every 60s.
- HTTP shape:
  - `POST /v1/projects/:slug/events/ticket` — JWT-authed; calls `IssueTicket` with `(claims.UserID, claims.TenantID, request.TraceID)`. Returns `{ticket}`.
  - `GET /v1/projects/:slug/events?ticket=X` — redeems ticket, validates `tenant_id` against the project's tenant_id (defense in depth), opens SSE stream; `Content-Type: text/event-stream`, `Cache-Control: no-store`, `Connection: keep-alive`. Loops on subscriber channel writing `data: <json>\n\n` and `Flush()`. On disconnect, calls unsubscribe.
- HTTP server tuning: `Server.WriteTimeout = 0` (long-lived connections); document operational requirement to raise file descriptor ulimit on production deploy.

**Execution note:** Build interface-first (`BrokerService` interface, `MemoryBroker` impl) so tests swap in a fake broker easily. Phase 7 will add `RedisBroker` for horizontal scale-out without changing callers.

**Patterns to follow:**
- `services/ds-service/internal/audit/persist.go` mutex pattern for shared maps.
- `app/api/sync/route.ts` X-Trace-ID convention.

**Test scenarios:**
- Happy path: subscribe → publish → subscriber receives event in <100ms.
- Edge case: 100 concurrent subscribers on different trace IDs receive only their own.
- Edge case: subscribe with mismatched tenant_id → events filtered out.
- Edge case: slow subscriber (drains 1 event/sec, publisher fires 50/sec) → events dropped, fast subscribers unaffected.
- Edge case: subscriber count reaches 1024 → 1025th request returns 503.
- Edge case: HTTP client disconnects mid-stream → unsubscribe runs; goroutine exits within 1s. Verified via `runtime.NumGoroutine()` returning to baseline.
- Edge case: TCP RST mid-stream (not graceful close) — same recovery within timeout.
- Error path: ticket expired (>60s) → 401. Ticket already used → 401.
- Error path: ticket valid but project's tenant_id ≠ user's tenant_id → 403 (defense in depth even if ticket leaks).
- Error path: JWT in query string → reject with 400 + log warning.
- Integration: full handler test via `httptest.Server` + manual SSE parser asserts event ordering and ticket lifecycle.

**Verification:**
- `go test ./internal/sse/... -race` passes.
- Manual smoke: get ticket → connect EventSource with ticket → publish event → received in browser console.
- ulimit raise documented in operational notes.

---

- U3. **Plugin 4th mode "Projects" — UI shell + smart-group detection**

**Goal:** Add the Projects tab to the plugin (manifest entry, mode button, view panel) with the smart-grouping algorithm running on selection change. Detection only — no submission yet (U4 ships that).

**Requirements:** R1, R2

**Dependencies:** None — independent of U1/U2.

**Files:**
- Modify: `figma-plugin/manifest.json` — add menu entry `{ name: "Open Projects mode", command: "openProjects" }`. Update `networkAccess.allowedDomains` to include the **production domain** (e.g., `https://ds-service.indmoney.com`) plus `http://localhost:7475`. NOT a placeholder string.
- Modify: `figma-plugin/code.ts` — extend `MessageFromUI` / `MessageToUI` discriminated unions with Projects-mode messages (`projects.detected-groups`, `projects.send`, `projects.send-result`, `projects.send-progress`); implement `runProjectsDetection()` walking selection.
- Modify: `figma-plugin/ui.html` — add 4th `.mode` button (`mode-projects`); update `.mode-underline` transform math for 4 positions; add `<div class="view" id="view-projects">` panel.

**Approach:**
- Selection walking calls `figma.loadAllPagesAsync()` first (per Figma 2026 manifest dynamic-page requirement).
- `groupBySection`: walk frame's parent chain until `SECTION` or page; group key = section ID OR `freeform-<n>`.
- `detectModePairs`: per group, build column index by integer x-coord (rounded); for each frame, find candidates at same column (Δx <10px) with different y AND `explicitVariableModes` sharing a `VariableCollectionId` but different mode IDs.
- Cross-validate by structural skeleton diff at depth=2 (path/type pairs). Delta > 0 outside `explicitVariableModes` → still pair, mark `theme_parity_warning_at_export = true` in payload; backend re-computes at audit time (Phase 2) — Phase 1 doesn't persist this column to avoid stale-flag risk.
- Modal preview renders one row per group with editable: name (default = section name; freeform = `Freeform <n>`), platform (auto-detected from frame width: <500px → mobile; ≥1024px → web; else designer picks), product (dropdown of 9 products), path (autocomplete from existing flows + DS taxonomy), persona (curated dropdown + free-text "Other").
- **Input length caps** enforced client-side: name ≤256, path ≤256, persona_name ≤128. Allowlist regex `[\w \-_/&·]+`. Reject CR/LF/NUL.
- **`idempotency_key` UUID** generated per Send click via `crypto.randomUUID()`. Submit-button disabled while in-flight.

**Patterns to follow:**
- `figma-plugin/code.ts` discriminated union pattern.
- `figma-plugin/ui.html:945-1024` mode shell HTML pattern.

**Test scenarios:**
- Happy path: select 6 frames in section (3 light/3 dark) → modal shows 1 group, 3 pairs, no warnings.
- Edge case: select frames spanning 2 sections → modal shows 2 groups; designer can split/merge.
- Edge case: select frames not in any section → "Freeform 1" with no pairs.
- Edge case: section with light-only frames → group with 0 pairs, all single-mode.
- Edge case: section with 3 modes (light/dark/sepia) at same column → group with 1 multi-mode triple.
- Edge case: pair candidates fail structural diff → still paired, `theme_parity_warning_at_export: true`.
- Edge case: input contains CRLF / NUL → client-side rejection with friendly error.
- Edge case: rapid double-click on Send → second click no-op (button disabled until first response).
- Error path: Figma file lacks `loadAllPagesAsync` → graceful warning, fall back to current page only.

**Verification:**
- Manual plugin test against `INDstocks V4` `Learn Touchpoints in F&O` reproduces 3-pair detection.
- `cd figma-plugin && npx tsc -p .` builds clean.
- Designer can switch to Projects mode and see grouping preview without crashes.

---

- U4. **Backend `/v1/projects/export` — Phase 1 fast preview pipeline + idempotency + rate limiting + audit log**

**Goal:** Receive plugin payload, validate + idempotency-check + rate-limit, persist project skeleton, kick off Figma REST + PNG render + size-cap + dedup, mark `view_ready`, emit SSE. ≤15s p95 for typical 6-frame export.

**Requirements:** R3, R4, R5, R6

**Dependencies:** U1 (schema), U2 (SSE)

**Files:**
- Create: `services/ds-service/internal/projects/types.go`
- Create: `services/ds-service/internal/projects/repository.go` — `TenantRepo` with scoped queries
- Create: `services/ds-service/internal/projects/server.go` — handlers (export, get, list, events, ticket, png)
- Create: `services/ds-service/internal/projects/pipeline.go` — `RunFastPreview(versionID, traceID)` orchestration + `RecoverStuckVersions(ctx)` startup sweep
- Create: `services/ds-service/internal/projects/modepairs.go`
- Create: `services/ds-service/internal/projects/ratelimit.go` — per-user 10/min, per-tenant 200/day, in-memory token-bucket
- Create: `services/ds-service/internal/projects/idempotency.go` — 60s TTL key store
- Create: `services/ds-service/internal/projects/png.go` — long-edge cap to 4096px, server-side downsample
- Modify: `services/ds-service/cmd/server/main.go` — register routes; start worker; start RecoverStuckVersions sweeper
- Create: tests inline.

**Approach:**
- Request payload (TS-shape; Go mirror):
  ```
  { idempotency_key: uuid, file_id: string, file_name: string, flows: [
      { section_id: string|null, frame_ids: string[], platform: "mobile"|"web",
        product: string, path: string, persona_name: string,
        name: string, mode_groups: ModeGroup[] }
  ] }
  ```
- Handler:
  1. **Auth** — JWT must carry designer-or-higher role. `tenant_id` from `auth.Claims.TenantID` (request body cannot override).
  2. **Rate limit** — check per-user + per-tenant counters; 429 if exceeded.
  3. **Payload caps** — max 20 flows, max 50 frames per flow, body ≤10MB. 413/400 if exceeded.
  4. **Input validation** — every plugin-supplied string length-capped + allowlist regex matched. Reject CR/LF/NUL.
  5. **Idempotency check** — `idempotency.Check(idempotency_key, ttl=60s)`. If duplicate within window: return cached `{project_id, version_id, deeplink, trace_id}` from prior call (`409 Conflict` with body).
  6. **`trace_id = uuid.NewString()`**.
  7. For each flow (within a single transaction):
     - `repo.UpsertProject(...)` — resolves project by `(tenant_id, product, platform, path)`.
     - `repo.CreateVersion(project_id, status: "pending", pipeline_started_at: now, pipeline_heartbeat_at: now)`.
     - `repo.UpsertFlow(project_id, file_id, section_id, persona_id)` — `INSERT ... ON CONFLICT DO NOTHING RETURNING id`.
     - `repo.InsertScreens(version_id, frames)` — empty rows (no canonical_tree, no png_storage_key yet).
  8. `audit_log.Insert(action="project.export", actor=user, tenant=tenant, file_id, project_id, version_id, ip, user_agent, trace_id)`.
  9. Respond `202 {project_id, version_id, deeplink, trace_id}`.
  10. Spawn `go pipeline.RunFastPreview(versionID, traceID)`.
- `RunFastPreview`:
  1. **Heartbeat goroutine** updates `project_versions.pipeline_heartbeat_at = now` every 5s.
  2. Fetch frames in batches via Figma REST (`/v1/files/{key}/nodes?ids=...&depth=3`). Retry with backoff on 429 (3 attempts).
  3. Render PNGs (`/v1/images/{key}?ids=...&format=png&scale=2`). Download.
  4. **Cap PNG long edge to 4096px** via `png.Downsample` — server-side decode + bilinear resize. Persist to `services/ds-service/data/screens/<tenant_id>/<version_id>/<screen_id>@2x.png`. NOT under `public/`.
  5. Run server-side mode-pair detection (canonicalize across plugin payload).
  6. For each canonical screen (single transaction): persist `screen_canonical_trees` + `screen_modes` rows.
  7. `BEGIN; UPDATE project_versions SET status='view_ready', pipeline_heartbeat_at=NULL; INSERT audit_jobs (status='queued', trace_id, idempotency_key); COMMIT;`
  8. `sse.Publish(traceID, ProjectViewReady{...})`.
  9. Notify worker via channel signal (no polling lag).
- `RunFastPreview` errors: persist `project_versions.status = failed`, `error` populated; `sse.Publish` `ProjectExportFailed`. Audit_log row (action="project.export.failed").
- **`RecoverStuckVersions(ctx)`** runs at server boot AND every 60s: scan `project_versions WHERE status='pending' AND pipeline_heartbeat_at < now-30s` → mark `failed` with `error="orphaned by server restart"`.

**Patterns to follow:**
- `services/ds-service/internal/audit/server.go:HandleAudit` for handler shape.
- `services/ds-service/internal/audit/persist.go` for atomic-write asset patterns.
- `services/ds-service/cmd/server/main.go` middleware chain.

**Test scenarios:**
- **Covers AE-1.** Happy path: POST 1 flow with 6 frames → 202 with `view_ready` SSE in ≤15s.
- Edge case: re-export same `(tenant_id, file_id, section_id, persona_id)` — Version 2 created; Version 1 stays at `view_ready`.
- Edge case: 50-frame export — completes in ≤45s p95 (relaxed bound for stress).
- Edge case: light-only section — single-mode, no theme_parity flag.
- Edge case: PNG larger than 4096px long edge → server-side downsample applied; persisted file size verified.
- Error path: idempotency_key replay within 60s → 409 with cached payload.
- Error path: rate limit exceeded → 429.
- Error path: payload exceeds 50 frames per flow → 400 with explicit limit message.
- Error path: input has CRLF in path → 400 with allowlist message.
- Error path: tenant-A user provides `tenant_id` from tenant-B in payload → ignored; uses JWT claim.
- Error path: Figma REST returns 429 — retries with backoff (3 attempts), then `failed`.
- Error path: Figma REST returns 403 — fail-fast, `failed`, error to user.
- Error path: PNG download partial failure — version `failed` (all-or-nothing).
- Error path: server crashes mid-pipeline — restart sweeper marks stuck `pending` versions as `failed`.
- **Auth:** unauthenticated → 401; viewer-role → 403; cross-tenant slug attempt → 404 (no existence oracle).
- **Audit log** row written on every success AND failure with required fields.
- Integration: full E2E posting from fixture plugin payload to SSE event.

**Verification:**
- `go test ./internal/projects/... -race` passes.
- Manual: curl POST produces project visible via `GET /v1/projects/<slug>` after ≤15s; SSE stream delivers `view_ready`.

---

- U5. **Audit-job worker pool (size=1) with lease columns + idempotent execution + RuleRunner interface**

**Goal:** Worker pool drains `audit_jobs` (queued → running → done), leases jobs to prevent duplicate work, runs `RuleRunner` against each version, persists violations idempotently via DELETE-then-INSERT in single transaction, emits SSE.

**Requirements:** R3 (Phase 2 of pipeline)

**Dependencies:** U1 (schema), U2 (SSE), U4 (pipeline enqueues jobs)

**Files:**
- Create: `services/ds-service/internal/projects/worker.go` — `WorkerPool{ size: int }` with channel-notification + lease claim
- Create: `services/ds-service/internal/projects/runner.go` — `RuleRunner` interface + `auditCoreRunner` impl wrapping `audit.Audit`
- Modify: `services/ds-service/cmd/server/main.go` — start worker pool on boot
- Create: `services/ds-service/internal/projects/worker_test.go`

**Approach:**
- `WorkerPool{ size, jobs chan struct{}, repo, runner, sse }`. Phase 1 ships `size: 1`. Phase 2 grows to 6.
- Worker goroutine main loop:
  ```
  for {
    select {
    case <-jobs: // notification from pipeline.go
    case <-time.After(30 * time.Second): // safety net
    }
    claimAndProcess()
  }
  ```
- `claimAndProcess`:
  1. `BEGIN IMMEDIATE; UPDATE audit_jobs SET status='running', leased_by='<worker_id>', lease_expires_at=now+60s WHERE status='queued' AND (lease_expires_at IS NULL OR lease_expires_at < now) ORDER BY created_at LIMIT 1 RETURNING id, version_id, trace_id;`
  2. If no row claimed → return.
  3. **Heartbeat goroutine** refreshes `lease_expires_at = now+60s` every 30s while job runs.
  4. `violations, err := runner.Run(ctx, version)`.
  5. **Idempotent persist** in single transaction:
     ```
     BEGIN;
     DELETE FROM violations WHERE version_id = ?;
     INSERT INTO violations ...; -- new
     UPDATE audit_jobs SET status='done', leased_by=NULL, lease_expires_at=NULL, completed_at=now WHERE id=?;
     COMMIT;
     ```
  6. `sse.Publish(traceID, ProjectAuditComplete{...})`.
  7. On panic: `defer recover()` marks job `failed` with stack trace truncated to 8KB; SSE `project.audit_failed`.
- **Crash recovery on startup**: scan `audit_jobs WHERE status='running' AND lease_expires_at < now` → reset to `queued`. Retry-safe via DELETE-then-INSERT idempotency.
- **`RuleRunner` interface**:
  ```
  type RuleRunner interface {
      Run(ctx context.Context, v *Version) ([]Violation, error)
  }
  ```
  Phase 1 ships `auditCoreRunner` wrapping `audit.Audit` per-screen, mode-resolved against light by default. Phase 2 adds `ThemeParityRunner`, `CrossPersonaRunner`, `A11yRunner`, `FlowGraphRunner` registered in a slice; pool iterates.
- **Worker pool size 2-4 deferred to Phase 2** (with rule fan-out workload).

**Execution note:** Worker logic in pure functions taking interfaces (Repo, RuleRunner, Publisher); `worker.go` glue is thin. Tests stub RuleRunner to return fixed result; verify state transitions + SSE emission + lease semantics.

**Patterns to follow:**
- Existing `audit.Audit` invocation in `cmd/audit/main.go` for the function-call shape.

**Test scenarios:**
- Happy path: queue 1 job → worker picks up via channel notification (no 2s polling lag), calls runner, writes 5 violations, publishes SSE; states queued→running→done.
- Edge case: queue 3 jobs simultaneously → worker processes serially (size=1) in created_at order.
- Edge case: lease expires (heartbeat goroutine fails) → another claim attempt picks up the abandoned job.
- Edge case: empty version (0 screens) → completes, 0 violations written.
- Edge case: idempotent retry — partial-write + crash + restart + re-run → final violations table matches what a clean run would produce. No duplicates.
- Error path: RuleRunner panics → recover; job marked `failed`; no rows in violations.
- Error path: ds-service crash mid-job → restart finds `running` row with stale lease → resets to `queued` → re-processed correctly.
- Integration: full pipeline test — POST export → wait `view_ready` → wait `audit_complete` → assert violations queryable.

**Verification:**
- `go test ./internal/projects/... -race` passes including worker tests.
- Manual smoke: export → `audit_complete` SSE within ~10s of `view_ready`.

---

- U6. **`/projects` route group with project view shell + animation choreography**

**Goal:** Next.js `/projects` lists projects accessible to the user; `/projects/[slug]?v=<version>` shows the 4-tab project view with the canvas top-half stubbed (atlas comes in U7). Page-load + tab transitions driven by GSAP timelines.

**Requirements:** R12, R13 (DRD scaffold), R16 (JSON scaffold)

**Dependencies:** U4 (backend list + get endpoints), U2 (SSE client).

**Files:**
- Create: `app/projects/layout.tsx` — `FilesShell` wrapper + auth gate
- Create: `app/projects/page.tsx`
- Create: `app/projects/[slug]/page.tsx`
- Create: `lib/projects/client.ts` — `listProjects`, `fetchProject`, `subscribeProjectEvents` (with ticket flow), `lazyFetchCanonicalTree`
- Create: `lib/projects/types.ts`
- Create: `components/projects/ProjectShell.tsx`
- Create: `components/projects/ProjectToolbar.tsx`
- Create: `components/projects/tabs/{DRDTab,ViolationsTab,DecisionsTab,JSONTab}.tsx` (placeholders; full impl later in this plan)
- Modify: `package.json` — add `gsap@^3.13`, `lenis@^1` (Framer Motion already present)

**Approach:**
- Sidebar nav (FilesShell pattern): groups projects by Product. Nested entries are flows.
- Toolbar at top: theme toggle (Light/Dark/Auto), persona toggle (dropdown filtered to personas with screens for this version), version selector.
- Tab strip: `[ DRD | Violations N | Decisions | JSON ]`. Active tab in URL hash for deeplinks.
- Phase 1 atlas slot in ProjectShell: simple PNG grid at preserved (x, y) using CSS transforms — no WebGL. U7 swaps in r3f.
- **SSE subscription**: `lib/projects/client.ts:subscribeProjectEvents(slug, traceId)` first calls `POST /events/ticket` with JWT (Authorization header), receives ticket, opens `EventSource(/events?ticket=<id>)`. Toasts when `audit_complete` arrives. Auto-reconnect with new ticket on EventSource error.
- **Page-load GSAP timeline** at mount (see Animation Philosophy — `lib/animations/timelines/projectShellOpen.ts`).
- **Tab switch animation** via `lib/animations/timelines/tabSwitch.ts` curtain wipe ~300ms.
- **`prefers-reduced-motion`** detector at `lib/animations/context.ts` — short-circuits all timelines to `gsap.set` final state instantly.

**Execution note:** Build placeholder ViolationsTab (lists existing audit core output) first to anchor right side. DRDTab/JSONTab/DecisionsTab render `<EmptyTab title="Coming in U8/U9/U10" />` initially.

**Patterns to follow:**
- `app/components/[slug]/page.tsx` — server-component data fetch + `notFound()`.
- `components/files/FilesShell.tsx` — sidebar layout pattern.
- `lib/auth-client.ts` — zustand persist + `getToken()`.

**Test scenarios:**
- Happy path: `/projects` lists projects; click navigates to `/projects/[slug]`; toolbar populated.
- Edge case: project has no versions → empty-state with "Export from plugin" CTA.
- Edge case: 3 versions → version selector; switching re-fetches.
- Edge case: theme toggle Auto → respects `prefers-color-scheme`.
- Edge case: persona toggle filters which screens render.
- Edge case: `prefers-reduced-motion: reduce` → animation timelines short-circuit; final state reached instantly.
- Error path: 404 → `notFound()` page.
- Error path: 401 → redirect to login.
- Error path: SSE ticket request fails → silent retry; banner after 30s.
- Error path: SSE EventSource fails mid-stream → reconnect with new ticket.
- **Tenant isolation:** designer in tenant A cannot view tenant B's project by URL guess → 404 (no existence oracle).
- Integration: Playwright loads `/projects/<fixture-slug>` → tab strip visible; switching tabs updates URL hash; GSAP timeline runs.

**Verification:**
- `npx tsc --noEmit` clean.
- `npx playwright test tests/projects/canvas-render.spec.ts` passes.
- `npx playwright test tests/projects/animation-reduced-motion.spec.ts` passes (timeline short-circuits).
- Manual: navigate to `/projects/<slug>`; tabs switch; theme toggle works on placeholder PNG grid; page-load animation runs.

---

- U7. **Atlas surface — r3f canvas with frames at preserved (x, y) + texture caps + progressive Suspense + hover micro-interactions**

**Goal:** Replace U6 placeholder PNG grid with `<Canvas>`-backed r3f scene: each screen is a textured plane positioned at its Figma section-relative (x, y), with pan + zoom, hover scale, click-to-snap. Phase 1 baseline only — no bloom, no LOD, no instancing (Phase 3).

**Requirements:** R12 (atlas top-half)

**Dependencies:** U6 (project shell with canvas slot).

**Files:**
- Create: `components/projects/atlas/AtlasCanvas.tsx` — `<Canvas>` with `<OrthographicCamera>`, `<OrbitControls>`, dynamic-imported via `next/dynamic({ ssr: false })`
- Create: `components/projects/atlas/AtlasFrame.tsx` — `<mesh>` with `<planeGeometry>` + `<meshBasicMaterial map={texture}>`. Hover scale spring 1→1.015 ~200ms. Click handler.
- Create: `components/projects/atlas/AtlasControls.tsx` — drei `<OrbitControls>` (pan/zoom only)
- Create: `components/projects/atlas/useAtlasViewport.ts` — initial camera fit + persisted zoom in localStorage
- Create: `components/projects/atlas/textureCache.ts` — shared TextureCache map keyed by `screen.png_url`
- Modify: `package.json` — add `three`, `@react-three/fiber@^9.6`, `@react-three/drei@^10`
- Modify: `next.config.ts` — `transpilePackages: ['three', '@react-three/fiber', '@react-three/drei']`
- Create: `tests/projects/canvas-render.spec.ts` Playwright

**Approach:**
- Dynamic-import `<AtlasCanvas>` from project page — three.js never enters server bundle. Wrap in `<Suspense>` with `key={pathname}` for **Next 16 componentCache** mitigation per pmndrs/react-three-fiber#3595.
- Camera: orthographic, `zoom` calibrated so section's longest axis fits viewport with 10% padding. Pan limits 50% beyond bounds.
- **Texture loading**: `useLoader(TextureLoader, screen.png_url)` per AtlasFrame, **wrapped in textureCache** so repeated loads (theme toggle to/fro) don't refetch. Disposes textures on unmount.
- **Progressive Suspense**: Suspense boundary at AtlasFrame level (not Canvas level) — frames render as their textures resolve; wireframe placeholder mesh during fetch.
- **PNG fetched via authed route** `GET /v1/projects/:slug/screens/:id/png` → response cached by browser with `Cache-Control: private, max-age=300`. NOT a public/ URL.
- **Theme toggle**: changing theme swaps `screen.png_url` to mode-resolved PNG; texture cache hits if previously seen; otherwise loads. Tween crossfade ~400ms via material opacity dual-tracking (old + new texture render simultaneously briefly).
- **Hover micro-interaction**: scale spring 1→1.015 ~200ms ease-out (mhdyousuf snappy). Subtle edge glow via `MeshBasicMaterial` color tween.
- **Click to snap**: emits `onFrameSelect(screen_id)`; ProjectShell switches JSON tab to that screen; r3f camera dolly to fit frame ~600ms ease-in-out.
- **Texture memory budget**: `<AtlasCanvas>` totals up texture bytes from loaded textures; if > 200MB, drops to scale=1 PNGs (refetch). Logs `renderer.info.memory.textures` on first paint and every 30s for ops visibility.

**Execution note:** Build with simplest possible scene first — one frame, one texture, click handler. Add multi-frame after single case works.

**Patterns to follow:**
- pmndrs/react-three-next starter pattern.

**Test scenarios:**
- Happy path: 6-frame section renders all 6 textured planes at correct (x, y); pan/zoom work.
- Edge case: single frame → camera fits to that frame.
- Edge case: very wide section (3722×3028) → initial fit shows full section; zoom to 1.0 = native pixel-density.
- Edge case: theme toggle → all textures swap; camera position + zoom preserved; cached textures don't refetch on toggle-back.
- Edge case: click frame → JSON tab activates with that screen.
- Edge case: rapid theme toggle (5 in 1s) → final state matches last toggle; no flicker; no orphan textures (verified via `renderer.info.memory.textures`).
- Edge case: hover 30 frames in succession → no memory leak; hover scale springs return to 1 cleanly.
- Edge case: PNG > 4096px long edge → backend already downsampled (U4); client receives ≤4096px; no `MAX_TEXTURE_SIZE` errors.
- Edge case: total texture memory > 200MB → fallback to scale=1.
- Error path: PNG fails to load (404, network) → AtlasFrame shows red placeholder mesh.
- Performance: 60fps maintained on M1 with 30 frames at 1440p (`<Stats />` from drei in dev mode).
- Performance: bundle audit — three.js + r3f do NOT enter SSR bundle (`next build` output).

**Verification:**
- Bundle inspection: `app/projects/[slug]/page.js` server bundle does NOT contain `three.module.js` symbols.
- `chunks/atlas` bundle ≤350KB gz (CI assertion).
- `npx playwright test tests/projects/canvas-render.spec.ts` passes.
- Manual: load real INDstocks `Learn Touchpoints` export; pan + zoom feel responsive; theme toggle smooth; hover frame scales subtly.

---

- U8. **JSON tab — lazy-fetched canonical_tree, default-collapsed, mode-resolved, memoized**

**Goal:** When a frame is clicked in the atlas (or selected via URL), the JSON tab fetches its `canonical_tree` lazily from `screen_canonical_trees`, displays as a collapsible tree default-collapsed at depth ≥2, with mode resolution memoized per-node.

**Requirements:** R16

**Dependencies:** U4 (backend `/screens/:id/canonical-tree` endpoint), U6 (tab routing), U7 (atlas frame click).

**Files:**
- Create: `components/projects/tabs/JSONTab.tsx`
- Create: `components/projects/tabs/JSONTreeNode.tsx` — recursive renderer with collapse state
- Create: `lib/projects/resolveTreeForMode.ts` — pure function, memoized
- Modify: `services/ds-service/internal/projects/server.go` — add `GET /v1/projects/:slug/screens/:id/canonical-tree`
- Create: `tests/projects/json-tab.spec.ts`
- Create: `lib/projects/resolveTreeForMode.test.ts`

**Approach:**
- Lazy-fetch `canonical_tree` only on tab activation (not bundled in project payload). Cached client-side per-screen for the session.
- Pure React tree viewer (no react-json-view dep). ~150 lines.
- **Default-collapsed at depth ≥2** to avoid 1000-node initial render jank. Click row to expand; `expand all` button.
- Each node: type badge (FRAME / TEXT / VECTOR / INSTANCE / RECTANGLE), name, key properties. `boundVariables.fills.id` always shown verbatim; resolved color/dimension shown with "🎯 bound" chip.
- **Memoized `resolveTreeForMode`** — `WeakMap` keyed by node + mode, returns resolved value per-node not per-tree. Avoids full structural copy on theme toggle.
- Search box: filter tree by node name / type / property substring; auto-expands matching paths.
- Right-rail: when node selected, raw JSON via `<pre><code>`.
- Animation: rows fade-in + stagger when expanding (~30ms per child via GSAP).

**Execution note:** Pure-function `resolveTreeForMode` testable exhaustively first; renderer second.

**Test scenarios:**
- Happy path: click frame → JSON tab fetches canonical_tree (lazy), default-collapses at depth ≥2; expand depth 1 → all children visible.
- Happy path: bound node resolves to active mode's hex; chip visible.
- Edge case: no boundVariables → raw fill, no chip.
- Edge case: theme toggle → memoized resolveTreeForMode cache invalidates; tree rerenders (only changed nodes via React reconciliation).
- Edge case: search "FRAME" → highlights all FRAME nodes; auto-expands.
- Edge case: 1000-node tree default-collapsed → initial render <300ms (improved from 1-2s by collapse).
- Edge case: 1000-node tree fully expanded → render time <800ms; jank measured but acceptable for Phase 1.
- Error path: canonical_tree endpoint 404 → tab shows error state with retry.
- Error path: canonical_tree malformed JSON → error state + raw JSON dump for debugging.

**Verification:**
- `npx tsc --noEmit` clean.
- `npx playwright test tests/projects/json-tab.spec.ts` passes.
- Manual: click frame → tree appears default-collapsed; expand to find bound color; toggle theme; resolved value swaps without full re-render.

---

- U9. **DRD tab — BlockNote skeleton + revision-counter ETag + autosave + body cap**

**Goal:** Working Notion-style editor in DRD tab persisting per-flow with debounced autosave. Single-editor only (Yjs collab Phase 5). `revision INTEGER` ETag prevents silent-overwrite races within a 1-second window. PUT body capped at 1MB.

**Requirements:** R13 (single-editor variant)

**Dependencies:** U1 (`flow_drd` table), U6 (DRD tab slot).

**Files:**
- Create: `components/projects/tabs/DRDTab.tsx`
- Create: `lib/projects/drdClient.ts` — fetch + debounced autosave with revision ETag
- Modify: `services/ds-service/internal/projects/server.go` — add `GET /v1/projects/:slug/flows/:flow_id/drd` and `PUT /v1/projects/:slug/flows/:flow_id/drd`
- Modify: `services/ds-service/internal/projects/repository.go` — `GetDRD(flowID)`, `UpsertDRD(flowID, content, expectedRevision, updatedBy)` returns `(newRevision, err)`
- Modify: `package.json` — add `@blocknote/core`, `@blocknote/react`, `@blocknote/mantine`

**Approach:**
- BlockNote via `useCreateBlockNote({ schema: BlockNoteSchema.create({ blockSpecs: { ...defaultBlockSpecs } }) })`. Custom blocks (`/decision`, `/figma-link`, `/violation-ref`) deferred to Phase 4/5.
- Autosave: `editor.onChange(() => debouncedSave(editor.document))`. Debounce 1.5s.
- **PUT body cap at 1MB** — reject 413 with friendly error. Forces oversize images to be uploaded separately (Phase 5).
- **Revision ETag**:
  - GET returns `{content, revision}`.
  - PUT body `{content, expected_revision}`. SQL: `UPDATE flow_drd SET content_json=?, revision=revision+1, updated_at=?, updated_by_user_id=? WHERE flow_id=? AND revision=?`. Check `RowsAffected() == 1`. Else 409 with current revision returned.
  - Client receives 409 → shows banner "This DRD was edited by someone else; reload to see latest"; reload button refetches GET.
- **Bundle**: `chunks/drd` ≤400KB gz. Dynamic-imported via `next/dynamic({ ssr: false })` from DRDTab — editor not loaded until designer clicks tab.
- Mount animation: GSAP fade-in mask wipe ~400ms.

**Execution note:** Single-editor pattern is simplest BlockNote usage. Don't pre-build extension scaffolding for custom blocks — Phase 5 has time.

**Patterns to follow:**
- BlockNote 0.47+ docs: `useCreateBlockNote`, `BlockNoteView`.

**Test scenarios:**
- Happy path: open DRD → editor loads existing content; type → debounced autosave writes after 1.5s; reload → persists.
- Edge case: paste from Notion HTML → BlockNote default paste handler accepts; tables render. (Polished paste Phase 5.)
- Edge case: paste Markdown → BlockNote parses; headings/bold/lists/code blocks render.
- Edge case: concurrent edit (two tabs same user) — second tab's PUT returns 409; banner displays; user reloads.
- Edge case: edit, save successfully (revision 5 → 6); second tab still has revision 5 in memory; second tab's PUT now returns 409 (correct).
- Edge case: empty DRD → starts with empty paragraph block.
- Edge case: PUT body exceeds 1MB → 413; client shows friendly "DRD too large; trim images" message.
- Error path: PUT fails (network) → autosave queues; retries with exponential backoff; status indicator shows "saving / saved / error".
- Performance: typing 100 words doesn't lag; debounce keeps backend writes ≤1 per 1.5s.

**Verification:**
- `npx tsc --noEmit` clean.
- `chunks/drd` bundle ≤400KB gz (CI assertion).
- `npx playwright test tests/projects/drd-tab.spec.ts` passes — type → reload → persistence.
- Manual: type some content; refresh; persists.

---

- U10. **Violations tab — read existing audit core results, severity per-rule mapping, persona × theme filter**

**Goal:** Bottom-half Violations tab lists violations from the `violations` table for the active version, grouped by 5-tier severity, filterable by active persona × theme. Click violation → highlights node in JSON tab.

**Requirements:** R14

**Dependencies:** U5 (worker writes violations), U6 (project shell), U8 (JSON tab to highlight).

**Files:**
- Create: `components/projects/tabs/ViolationsTab.tsx`
- Create: `components/projects/tabs/ViolationRow.tsx`
- Create: `lib/projects/violationsClient.ts`
- Modify: `services/ds-service/internal/projects/repository.go` — `ListViolations(versionID, filters)`
- Modify: `services/ds-service/internal/projects/server.go` — `GET /v1/projects/:slug/versions/:vid/violations`

**Approach:**
- Violations written by U5's worker. Phase 1 mapping table (in `runner.go` adapter):
  ```
  Existing audit.FixCandidate.priority  →  violations.severity
  P1 (deprecated tokens, theme breaks)  →  Critical
  P1 (drift > threshold)                →  High
  P2 (drift within threshold)           →  Medium
  P3 (cosmetic, naming hygiene)         →  Low
  P3 (info-grade, suggestions)          →  Info
  ```
  Adjustable per-rule via `runner.go`'s `severityFor(rule)` function — enables Phase 2 fine-tuning without touching engine.
- Tab UI: collapsible severity groups (Critical/High/Medium/Low/Info) with counts. Each row: severity chip (color-coded) + node breadcrumb + fix suggestion + "View in JSON" button.
- Filter chips at top: persona (defaults to active persona), theme (defaults to active theme). Counts update live via SSE updates.
- Click "View in JSON" → switches to JSON tab, scrolls to + highlights node by ID.
- Lifecycle controls (Acknowledge/Dismiss) deferred to Phase 4. Phase 1 shows disabled with tooltip.
- Animation: rows mount with stagger fade-in (~50ms per row, max 600ms total via GSAP). Severity-chip pulse on hover.

**Patterns to follow:**
- `app/files/[slug]/page.tsx` violation-list rendering (verify; mirror).

**Test scenarios:**
- Happy path: violations exist → grouped list with correct counts.
- Happy path: click "View in JSON" → JSON tab activates; scrolls to node.
- Edge case: zero violations → empty state "No drift detected".
- Edge case: persona filter → only persona-tagged violations.
- Edge case: theme filter → only mode-tagged violations.
- Edge case: 100 violations across 5 screens → tab renders without lag.
- Edge case: violations from deleted screen (orphaned) → gracefully skip.
- Error path: API 500 → error state with retry.
- Integration: full E2E — POST export → wait `audit_complete` → assert violation count matches fixture.

**Verification:**
- `npx tsc --noEmit` clean.
- `npx playwright test tests/projects/violations-tab.spec.ts` passes.
- Manual: after export, violations populate; "View in JSON" works.

---

- U11. **Authenticated PNG route handler + per-resource path enforcement**

**Goal:** Serve project screen PNGs from the non-public storage path (`services/ds-service/data/screens/...`) through an authed Go route handler that verifies JWT, asserts the screen's tenant_id matches the user's claim, and streams the file with `Cache-Control: private`.

**Requirements:** Implicit security requirement (origin: prevent public/scrape attack on pre-launch product flows)

**Dependencies:** U1 (screens table with png_storage_key), U4 (handler chain).

**Files:**
- Create: `services/ds-service/internal/projects/png_handler.go`
- Modify: `services/ds-service/cmd/server/main.go` — register `GET /v1/projects/:slug/screens/:id/png`
- Modify: `lib/projects/client.ts` — `screenPngUrl(slug, screenId)` builds the authed URL

**Approach:**
- `GET /v1/projects/:slug/screens/:id/png`:
  1. JWT auth via existing `requireAuth` middleware.
  2. `repo.GetScreen(tenantID, slug, screenID)` — TenantRepo enforces tenant_id; returns 404 if cross-tenant or not found (no existence oracle).
  3. Stream the file with:
     - `Content-Type: image/png`
     - `Cache-Control: private, max-age=300`
     - `X-Content-Type-Options: nosniff`
     - `Content-Disposition: inline`
- Fallback: if file missing on disk (deleted by mistake) → 404 with explicit "asset missing" message; sentry/log alert.
- **Server-side path validation**: png_storage_key from DB is joined to `data/screens/` base; `filepath.Clean` + `strings.HasPrefix(absPath, baseDir)` rejects path traversal even though `screen_id` is server-generated UUID.
- **No tenant_id in URL** — derived from JWT claim. URL is `…/screens/<screen_id>/png` only; the screen_id is sufficient because TenantRepo filters.

**Patterns to follow:**
- Existing `cmd/server` middleware chain.

**Test scenarios:**
- Happy path: authed request → 200 with PNG; Cache-Control: private; correct Content-Type.
- Error path: unauthenticated → 401.
- Error path: wrong tenant → 404 (NOT 403, no existence oracle).
- Error path: file missing on disk → 404 + log alert.
- Error path: path traversal attempt (e.g., screen_id `../../etc/passwd` — would never happen with UUID, but verify defense) → 400 / clean rejection.
- Integration: atlas frame requests PNG → renders correctly; 30 concurrent frame fetches don't race.

**Verification:**
- `go test ./internal/projects/... -race` passes png_handler tests.
- Manual: open project view; atlas renders; check Network tab — PNG URLs are `/v1/projects/.../png`; Cache-Control: private set.

---

- U12. **Animation library — GSAP + Lenis + reduced-motion + shared timelines**

**Goal:** Foundational animation infrastructure: GSAP with global context, Lenis smooth-scroll singleton, `prefers-reduced-motion` detector, reusable timelines (page-load, tab-switch, theme-toggle, hover patterns) that all subsequent phases extend.

**Requirements:** Animation Philosophy section above; advances R12 (cinematic project view), foundational for Phase 6 click-and-hold mind graph.

**Dependencies:** None — independent. Pulled in by U6, U7, U8, U9, U10.

**Files:**
- Modify: `package.json` — add `gsap@^3.13`, `lenis@^1`
- Create: `lib/animations/context.ts` — global GSAP context manager + Lenis singleton + `useReducedMotion` hook
- Create: `lib/animations/hooks/useGSAPContext.ts` — component-scoped GSAP context with auto-cleanup
- Create: `lib/animations/hooks/useLenis.ts` — Lenis access + raf loop integration
- Create: `lib/animations/timelines/projectShellOpen.ts`
- Create: `lib/animations/timelines/tabSwitch.ts`
- Create: `lib/animations/timelines/themeToggle.ts`
- Create: `lib/animations/easings.ts` — shared easing constants matching mhdyousuf/resn (cubic-bezier strings)
- Create: `tests/projects/animation-reduced-motion.spec.ts`

**Approach:**
- `lib/animations/context.ts`:
  - `gsap.registerPlugin(ScrollTrigger)` on first import.
  - `useReducedMotion()` returns boolean from `window.matchMedia('(prefers-reduced-motion: reduce)')` with subscription to changes.
  - `LenisProvider` instance lifecycle: created on first request, raf loop ties to `requestAnimationFrame`, paused when `prefers-reduced-motion: reduce`.
- `useGSAPContext(scope: RefObject<HTMLElement>)`:
  - On mount: `const ctx = gsap.context(() => {}, scope.current)`.
  - Returns `ctx.add` for component-scoped tweens.
  - On unmount: `ctx.revert()` cleans up all tweens automatically (GSAP best practice for React 19 strict mode).
- Timeline patterns (per Animation Philosophy):
  - `projectShellOpen(scope)` returns a `gsap.timeline()` paused; called on mount; respects reduced-motion.
  - `tabSwitch(outgoing, incoming)` returns a brief curtain wipe.
  - `themeToggle(textures: AtlasTextureMap)` returns a crossfade.
- **Bundle**: ships as `chunks/animations` ≤50KB gz. GSAP core is small; ScrollTrigger separately requested only when scroll-driven animation needed.
- **Easing constants** centralized in `lib/animations/easings.ts`:
  ```
  EASE_PAGE_OPEN = 'expo.out'         // mhdyousuf snappy reveal
  EASE_TAB_SWITCH = 'cubic.inOut'     // smooth swap
  EASE_HOVER = 'back.out(1.2)'        // playful spring
  EASE_THEME_TOGGLE = 'cubic.out'     // soothing
  EASE_DOLLY = 'expo.inOut'           // resn cinematic
  ```

**Execution note:** Build the page-load timeline first (most visible, validates the stack). Then tab-switch. Then theme-toggle. Hover micro-interactions live with their components (U7 atlas, U10 violations row).

**Patterns to follow:**
- mhdyousuf.me reference for snappy reveal cadence.
- resn.co.nz reference for soothing hover/transition curves.

**Test scenarios:**
- Happy path: project view mounts → page-load timeline runs; toolbar fades + slides; atlas frames stagger; tabs slide.
- Happy path: tab click → curtain-wipe ~300ms; outgoing fades+slides up, incoming slides up from below.
- Edge case: `prefers-reduced-motion: reduce` → all timelines short-circuit to final state instantly; Lenis disabled (native scroll).
- Edge case: rapid tab switches (5 clicks in 1s) → previous timeline killed; new one starts; no overlapping animations.
- Edge case: component unmounts mid-timeline → `ctx.revert()` cleans up; no console warnings.
- Edge case: GSAP context cleanup verified via `ctx.scope` checks; no leaked tweens.
- Performance: GSAP timeline runs at 60fps; `<Stats />` shows no frame drops during page-load.

**Verification:**
- `chunks/animations` bundle ≤50KB gz (CI assertion).
- `npx tsc --noEmit` clean.
- `npx playwright test tests/projects/animation-reduced-motion.spec.ts` passes.
- Manual: project view feels smooth; reduce-motion toggles instantly snap to final state; navigation between tabs feels cinematic.

---

## System-Wide Impact

- **Interaction graph:**
  - Plugin → ds-service `cmd/server` (new `/v1/projects/*` routes; existing `requireAuth` + new rate-limit middleware).
  - ds-service → Figma REST (same client used by `cmd/icons` and existing audit pipeline).
  - ds-service → SQLite (new tables; existing `audit_log` table receives audit-write entries).
  - ds-service → SSE → docs site (new flow; ds-service publisher, docs site EventSource subscriber via ticket).
  - Audit worker → existing `audit.Audit()` through new `RuleRunner` interface.
  - Docs site → ds-service authed PNG route (replaces public/ static path).
- **Error propagation:**
  - Plugin → `cmd/server` errors surface as toast notifications.
  - ds-service pipeline failure → SSE `project.export_failed`; UI banner.
  - Audit worker failure → SSE `project.audit_failed`; project still viewable; violations tab "Audit unavailable".
- **State lifecycle risks:**
  - Mid-pipeline crash leaves `project_versions` in `pending` → `RecoverStuckVersions(ctx)` sweeper runs at boot + every 60s.
  - Worker mid-job crash → lease expires; another worker (Phase 2+) or restart claims; idempotent transaction ensures no duplicates.
  - PNG download partial failure → all-or-nothing fail (Phase 1 simple). Reconsider in Phase 4.
  - DRD autosave race within 1s → revision counter ensures no silent overwrite.
- **API surface parity:**
  - `services/ds-service/internal/audit/types.SchemaVersion` UNCHANGED at "1.0". New `internal/projects/types.ProjectsSchemaVersion = "1.0"`.
  - Plugin `MessageFromUI` / `MessageToUI` unions extend with `projects.*` types. Backwards-compatible.
  - Existing `lib/audit/*.json` sidecar pattern unchanged. `/files/[slug]` route works against committed JSON unchanged.
- **Integration coverage:**
  - Cross-layer: Plugin selection → backend ingest → SQLite write → SSE event → UI render. Single Playwright test (`plugin-export-flow.spec.ts`).
  - Cross-mode: light/dark toggle re-resolves Variables → JSON tab + atlas re-render. U7 + U8.
  - Cross-tenant: tenant-isolation test via `tests/projects/tenant-isolation.spec.ts`.
- **Unchanged invariants:**
  - Existing audit core (`internal/audit/`) is read-only Phase 1 — Phase 2 modifies.
  - Existing plugin modes (Publish / Audit / Library) unchanged.
  - Existing `cmd/server` auth middleware unchanged; new routes register behind it.
  - Existing JSON sidecars (`lib/audit/*.json`) unchanged.

---

## Risks & Dependencies

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| Cross-tenant query leak (missed `WHERE tenant_id = ?`) | Low | Critical | Denormalized tenant_id everywhere + scoped TenantRepo enforcing filter at SQL level; Phase 1 test asserts cross-tenant 404. |
| SSE auth bypass via JWT in URL | Low | Critical | Short-lived single-use ticket model (60s TTL); tickets bound to user+tenant+trace; never JWT in query string. |
| PNG public-path leak of pre-launch flows | Critical (if not fixed) | Critical | PNGs stored outside `public/`; served via authed route only. |
| Concurrent re-export race (two designers, same flow) | Medium | High | Idempotency_key UUID per export; 60s TTL coalesce; advisory lock at DB level. |
| Audit-job retry duplicates violations | Medium | High | Idempotent transaction (DELETE-then-INSERT in single tx); lease-based job claim. |
| DRD silent overwrite within 1s window | Medium | High | Revision counter ETag (not updated_at); RowsAffected check; 409 on mismatch. |
| Worker single-goroutine bottlenecks Phase 2 fan-out | Medium | High (Phase 2) | Architecture admits worker pool with size=1 + lease columns from day one; Phase 2 changes constant + adds heartbeat refresh. |
| Texture memory exceeds iOS Safari 256MB cap | High (without mitigation) | Critical | Server-side PNG long-edge cap to 4096px; client-side total-bytes budget with scale=1 fallback at >200MB. |
| Bundle size budget exceeded | High | Medium | Split into 4 chunks (shell ≤200KB / atlas ≤350KB / DRD ≤400KB / animations ≤50KB); CI enforcement via `next build --analyze`. |
| Next 16 `componentCache` breaks R3F navigation | Medium | High | Wrap `<AtlasCanvas>` in `<Suspense>` with `key={pathname}`; verify at U7 implementation. |
| Figma REST 429 rate limit on concurrent exports | Medium | Medium | Per-user 10/min + per-tenant 200/day rate limits in U4; per-tenant Figma REST queue. |
| SQLite write contention (audit_jobs + DRD + screens) | Low | Medium | WAL mode; per-table indexing; benchmark before Phase 2 worker pool growth. |
| SSE through corporate proxies dropping connections | Medium | Medium | 15s heartbeat; auto-reconnect on EventSource error with new ticket; configurable interval. |
| Mode-pair detection misses real-world edge cases | Medium | Medium | Verified against ONE file (INDstocks V4); Phase 2 first-pass smoke test adds golden fixtures from Tax / Plutus / Mutual Funds. |
| BlockNote bundle (~400KB gz) | High | Low | Dynamic-import DRDTab; not loaded until designer clicks tab. |
| Plugin `loadAllPagesAsync` unavailable on older plans | Low | Low | Documented assumption; feature detection fallback to current page. |
| Audit log not written on every export | Medium | Medium | Required step in U4; tested. |
| Rate-limit bypass via plugin in malicious Figma file | Low | Medium | Per-user + per-tenant rate limits in U4; payload caps; audit_log captures. |
| Animation jank on Intel Iris GPU (older Macs) | Medium | Low | `prefers-reduced-motion` short-circuits; GSAP small footprint; bloom Phase 3 only. |
| GSAP timeline cleanup leaks (React 19 StrictMode) | Medium | Low | `gsap.context()` per component + `ctx.revert()` on unmount; verified in test. |
| 5-tier severity ambiguity (P1/P2/P3 mapping) | Low | Low | Per-rule `severityFor(rule)` function in U10; documented mapping table. |
| Screen `screen_logical_id` migration confusion in Phase 4+ | Low | Medium | Phase 1 sets the column; documented intent; Phase 4 plan owns the migration logic. |
| theme_parity_warning re-derived in Phase 2 differs from plugin's snapshot | Low | Medium | Phase 1 doesn't persist the column; Phase 2 audits compute fresh from canonical_trees. |

---

## Documentation / Operational Notes

### Operational changes
- New SQLite tables — fresh migrations on first deploy. Verify backup script captures `services/ds-service/data/ds.db` AND `services/ds-service/data/screens/` directory tree.
- New `cmd/server` route group — production deploy needs new endpoints accessible. Plugin's `networkAccess.allowedDomains` updated with production domain (NOT placeholder).
- Plugin manifest update → Figma plugin-store re-submission. Coordinate with Phase 1 deploy.
- ulimit raise documented: production deploy needs file-descriptor limit ≥ 4096 for SSE concurrent subscribers (default 1024 + headroom).
- Lenis runs an rAF loop globally — disabled under `prefers-reduced-motion`.

### Documentation
- Update `README.md` operator runbook with new export flow.
- Update `docs/architecture.md` to reflect new SQLite schema + SSE pattern + Projects feature surface.
- Add `docs/runbooks/projects-pipeline-debug.md` — inspect a stuck export (query `audit_jobs`, replay from `pending`, etc.).
- Add `docs/security/data-classification.md` — PII classification table per column for new schemas.
- Capture institutional learnings in `docs/solutions/` once Phase 1 ships: r3f + Next 16 setup, mode-pair detection algorithm, SSE on stdlib Flusher, GSAP context discipline in React 19, SQLite migration discipline.

### Monitoring
- Request-log lines for `/v1/projects/export` (latency p50/p95/p99 with `frame_count`, `mode_pair_count`, `figma_fetch_ms`, `png_render_ms`, `db_write_ms`, `total_ms`).
- SSE subscriber-count gauge (alarm at >800 to anticipate 1024 cap).
- Audit-job queue-depth gauge + queue-lag p95 (alarm at >10s).
- Per-tenant export count (rate-limit visibility).
- Audit worker job duration p95 + heartbeat freshness.
- Atlas first-paint client-side metric posted to `POST /v1/metrics` with `frame_count` + `texture_total_bytes` (when `?perf=1` or env-flag enabled).
- Bundle sizes asserted via CI parsing of `next build` output.

### Performance budgets (CI-asserted)
| Metric | Budget | Source |
|--------|--------|--------|
| `app/projects/[slug]` initial route | ≤200KB gz | `next build` chunk analysis |
| `chunks/atlas` | ≤350KB gz | same |
| `chunks/drd` | ≤400KB gz | same |
| `chunks/animations` | ≤50KB gz | same |
| Fast-preview pipeline p95 | ≤15s (6 frames), ≤45s (50 frames stress) | log lines from production fixtures |
| Audit job p95 | ≤5min (50 frames) | same |
| Atlas first-paint on M1 | <2s with 30 frames at 1440p | client metric |
| 60fps maintained on M1 | 30 frames at 1440p | drei `<Stats />` in dev |
| JSON tree initial render (default-collapsed) | <300ms (1000 nodes) | client perf.mark |
| Violation tab populate after audit_complete | <500ms | client perf.mark |

### Performance test fixture set
Committed at `tests/projects/fixtures/`:
- `learn-touchpoints-6-frames.json` — INDstocks V4 sample (3 light + 3 dark pairs)
- `wallet-15-frames.json` — INDstocks V4 sample (light + dark)
- `tax-30-frames.json` — Tax product sample
- `plutus-50-frames.json` — Plutus stress fixture
- p95 measured over 100 exports across these fixtures at 50/30/15/5% distribution from a fresh tenant on LAN.

---

## Sources & References

- **Origin document:** [docs/brainstorms/2026-04-29-projects-flow-atlas-requirements.md](../brainstorms/2026-04-29-projects-flow-atlas-requirements.md) — 27 requirements, 9 flows, 8 actors, 8 acceptance examples, scope boundaries, dependencies, 10 open questions.
- **Predecessor plan:** [docs/plans/2026-04-28-001-feat-files-tab-audit-pipeline-plan.md](2026-04-28-001-feat-files-tab-audit-pipeline-plan.md) — files tab + audit pipeline; this plan reuses the audit core through `RuleRunner` interface.
- **Predecessor brainstorm:** [docs/brainstorms/2026-04-28-files-tab-audit-pipeline-requirements.md](../brainstorms/2026-04-28-files-tab-audit-pipeline-requirements.md) — structural pattern reference.
- **Live verification:** Mode-pair mechanism verified on file `2m7ouydXKfxYk7hhjQxrt7` (INDstocks V4) `Learn Touchpoints in F&O` section — same node tree, only `explicitVariableModes` differs. 96 nodes, 31 bound variables, 0 binding deltas.
- **Tech foundation borrow targets** (extraction-ready in DesignBrain at `~/DesignBrain-AI/`):
  - BlockNote editor: `web/src/components/product-mode/DocumentEditor.tsx` (Phase 5)
  - JourneyGraph algorithms: `web/src/engine/journey/` (Phase 2 — flow-graph audit rules)
  - figmaClipboardImport: `web/src/lib/canvas/figmaClipboardImport.ts` (Phase 5 — paste handlers)
  - CEL governance engine: `internal/governance/` (Phase 7 — rule curation)
- **Repo files referenced (repo-relative):**
  - `services/ds-service/internal/audit/engine.go`, `types.go`, `server.go`, `persist.go`, `output.go`
  - `services/ds-service/internal/db/db.go`
  - `services/ds-service/internal/auth/auth.go`
  - `services/ds-service/cmd/server/main.go`
  - `figma-plugin/code.ts`, `ui.html`, `manifest.json`
  - `app/components/[slug]/page.tsx`, `app/api/sync/route.ts`, `app/layout.tsx`
  - `lib/auth-client.ts`, `lib/audit/types.ts`
  - `playwright.config.ts`, `tests/component-inspector-deep.spec.ts`
- **External docs:**
  - [react-three-fiber installation](https://r3f.docs.pmnd.rs/getting-started/installation)
  - [r3f v9 migration guide](https://r3f.docs.pmnd.rs/tutorials/v9-migration-guide)
  - [pmndrs/react-three-fiber#3595](https://github.com/pmndrs/react-three-fiber/issues/3595)
  - [BlockNote 0.47 docs](https://www.blocknotejs.org/docs)
  - [Hocuspocus collaboration overview](https://tiptap.dev/docs/hocuspocus/getting-started/overview) (Phase 5)
  - [Figma plugin manifest spec](https://developers.figma.com/docs/plugins/manifest/)
  - [react-force-graph](https://github.com/vasturiano/react-force-graph) (Phase 6)
  - [GSAP 3.13 + ScrollTrigger](https://gsap.com/docs/v3/)
  - [Lenis smooth-scroll](https://lenis.darkroom.engineering/)
- **Animation philosophy references:**
  - [mhdyousuf.me](https://www.mhdyousuf.me/) — GSAP-driven, terminal aesthetic, snappy micro-interactions
  - [resn.co.nz](https://resn.co.nz/#) + [resn.co.nz/work/all](https://resn.co.nz/#!/work/all) — click-and-hold zoom mechanic, soothing transitions, atmospheric WebGL (full implementation Phase 6)

---

## Deepening Notes

This plan was deepened on 2026-04-29 with findings from four specialist sub-agent passes:
- **Architecture (3 Critical / 4 High / 4 Medium / 5 Low)** — folded into Key Technical Decisions: tenant_id propagation, SSE auth via tickets, idempotency_key, screen_logical_id, RuleRunner interface, worker pool with lease, RecoverStuckVersions sweeper.
- **Performance (2 Critical / 4 High / 3 Medium / 1 Low)** — folded: PNG long-edge 4096px cap, 4-chunk bundle budget split, JSON tree default-collapsed + memoize, progressive Suspense + texture URL cache, channel-notification worker, BlockNote PUT cap 1MB, performance test fixture set + measurement methodology.
- **Data integrity (4 Critical / 6 High / 5 Medium / 5 Low)** — folded: idempotent audit-job DELETE-then-INSERT, revision-counter DRD ETag, FK enforcement + cascade rules, soft-delete columns, screen_canonical_trees split, project_versions.status simplified, schema_migrations + numbered files + forward-only column-add, tenant_id denormalized, persona ON CONFLICT, NOT NULL + UNIQUE constraints, PII classification doc.
- **Security (2 Critical / 5 High / 3 Medium / 1 Low)** — folded: SSE ticket model, scoped TenantRepo, PNGs out of public/ + authed route, rate limits + payload caps + input validation, audit_log on every export, designer-tenant-wide-read explicit assumption, CSRF posture documented.

User animation directives (mhdyousuf.me + resn.co.nz) folded into a new Animation Philosophy section + new U12 implementation unit + per-surface treatment matrix.

Plan grew from 10 → 12 implementation units. Phase scope unchanged (~3 weeks); the 2 added units (U11 PNG handler, U12 animation library) are essential infra the original plan would have required mid-flight.
