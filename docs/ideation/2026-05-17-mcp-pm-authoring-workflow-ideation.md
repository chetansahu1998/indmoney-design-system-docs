---
date: 2026-05-17
topic: mcp-pm-authoring-workflow
focus: ideation on the just-written plan `docs/plans/2026-05-17-002-feat-mcp-ds-service-pm-workflow-plan.md`
mode: repo-grounded
status: explored
follow_up: plan updated in place to fold in all 7 survivors (non-MVP ambition)
---

# Ideation: MCP server + PM authoring workflow — improvements

## Grounding context

Plan ships 11 implementation units in 2 phases (local-stdio MCP → remote `/mcp` with OAuth shim). Key constraints: designer naming canonical, ds-service DB authoritative, Mixpanel deferred. Past learnings: hocuspocus ticket-auth, `inbox:<tenant_id>` SSE channel, frame thumbnail proxy + 5-min cache, 3-tier re-attachment pattern, read-before-write-tx.

**External signals that materially shaped survivors:**
- Figma's own MCP server (GA 2025) — URL-based node ref, no rename guidance
- Code Connect issue #194 — stable-key request for string-based binding, unresolved
- Supernova MCP — closest prior art, component-centric not state/frame-centric
- Claude org-connector issue #46207 — single shared OAuth token, per-user isolation impossible at connector layer today
- Storybook story-as-spec — structural analogue for "state is a stable contract"
- Klavis Progressive Discovery — 2-3 meta tools + lazy schema avoids tool bloat
- arXiv 2602.14878 — 97.1% of production MCP tools have description smells; 4 MCP servers = 7k cold tokens
- DNS analogy: stable ID + human alias + late-binding resolution

## Topic axes

1. Tool surface design
2. Naming + binding contracts
3. Authoring conversation UX
4. Auth, transport, scale
5. Viewer + downstream consumption

## Ranked survivors (all folded into the revised plan)

### 1. Auto-skeleton — autosync pre-creates `prd_state` rows from designer-named frames
**Axis:** Authoring UX
**Basis:** `direct:` KTD-4 already names the designer as canonical; the original plan asks the PM to re-describe what the designer already encoded.
**Description:** When autosync binds a Figma section, ds-service auto-creates one `prd_state` row per frame, pre-titled from the frame name. `/ind-prd` opens at the first empty state instead of a blank doc.
**Rationale:** Removes the most error-prone PM step. The model already exists in frame names; this just promotes it.
**Downsides:** Designers commit to state names earlier; renames cascade into stub state rows (mitigated by #3).
**Confidence:** 85% | **Complexity:** Low | **Status:** Explored — folded into U2b

### 2. Spec stems — typed columns per state instead of free-form prose
**Axis:** Viewer + downstream consumption
**Basis:** `direct:` Mixpanel-deferred-JSONB risk. `reasoned:` PRD value = 1/5 prose, 4/5 mechanical derivations (test stub, a11y check, event scaffold, eng story).
**Description:** `prd_state` carries typed children: `acceptance_criteria[]`, `edge_cases[]`, `copy_strings{}`, `events[]` (typed table), `a11y_notes[]`. Viewer composes prose at render time. Storybook stub gen, Playwright test gen, Mixpanel tracking-plan export, JIRA story creation each read their own stem.
**Rationale:** One PM authoring action populates four downstream systems. Kills the Schrödinger schema by shipping the right shape now.
**Downsides:** Heavier schema; Claude must be coached to call `prd.add_event` rather than write event names as prose.
**Confidence:** 78% | **Complexity:** Medium | **Status:** Explored — folded into U4

### 3. Bind by role, not by name — Figma component property is the stable ID
**Axis:** Naming + binding contracts
**Basis:** `external:` Code Connect issue #194 (name-string binding fragility unresolved). DNS analogy made literal — node ID is the IP, role is the canonical alias, frame name is the cosmetic hostname.
**Description:** Designer applies a Figma component property `@role` to each PM-meaningful frame (`cold_state`, `loading`, `bank_tracking_failed`). `frame_tag` references `(figma_node_id, role)`. Frame rename does nothing; role change is intentional.
**Rationale:** Solves rename fragility structurally rather than via 3-tier fallback. Designer-side cost is one property.
**Downsides:** Needs designer training or a small Figma plugin (U12 in revised plan).
**Confidence:** 70% | **Complexity:** Medium | **Status:** Explored — folded into U5b + U12

### 4. Progressive discovery — 3 visible MCP tools, lazy schemas for the rest
**Axis:** Tool surface design
**Basis:** `external:` Klavis Progressive Discovery + arXiv 2602.14878 (97.1% tool-description smells) + 7k cold tokens for 4 MCP servers.
**Description:** Cold catalog exposes `drd.read`, `prd.author`, `section.inspect`. Each returns inline next-action hints and on-demand schemas. The ~16 underlying tools become reachable, not always-loaded. Cold context drops from ~3-4k to ~1.5k tokens.
**Rationale:** Cold-start UX and tool-chain latency both improve. Three workflow verbs map to PM mental model.
**Downsides:** Two-step resolution adds a roundtrip on first use of each sub-op.
**Confidence:** 65% | **Complexity:** Low | **Status:** Explored — folded into U6b

### 5. File-scoped auth — bind MCP session to Figma file access, not the user
**Axis:** Auth, transport, scale
**Basis:** `external:` Claude org-connector issue #46207 (shared OAuth token; per-user isolation impossible today). `reasoned:` whoever has Figma edit access is already in the team that should author the spec.
**Description:** Phase 2 remote MCP authenticates by Figma file ID. PM presents Figma session or ds-service-minted bridge token. Authz: "do you have edit on file `aIgxN…`?" Identity logged for audit, but authorization is file-scoped.
**Rationale:** Sidesteps the Claude OAuth gap entirely. Matches how Notion+Figma, Linear+Figma actually work.
**Downsides:** Cross-file specs need careful semantics; harder to express read-but-not-author within a file.
**Confidence:** 75% | **Complexity:** Medium | **Status:** Explored — replaces U11

### 6. Coverage wall — `section.outline_states` returns the corkboard view
**Axis:** Authoring UX
**Basis:** `external:` TV writers' room corkboard (Lost, Breaking Bad, The Wire — break a season's beats into cards, walk the wall to see gaps). `direct:` resumption is undefined in the original plan.
**Description:** New tool returns `[{frame_name, role, binding_status, prd_state_id?, word_count, last_touched_by, last_touched_at}]`. `/ind-prd` always opens with the wall. Viewer renders the same wall for design review.
**Rationale:** Resumption + coverage + handoff visibility in one tool. Coverage % becomes the only PM metric needed pre-Mixpanel.
**Downsides:** One more tool (less critical with #4 in play); requires `last_touched_by` threading.
**Confidence:** 88% | **Complexity:** Low | **Status:** Explored — folded into U6 + U9

### 7. Universal join key — `{sub_product}/{sub_flow}` becomes the org-wide spec identifier
**Axis:** Naming + binding contracts (cuts into Viewer + downstream consumption)
**Basis:** `direct:` 1,313 sections already canonically named. `reasoned:` DNS made literal — one identifier joining Figma, PRD, Storybook, Mixpanel, Sentry, JIRA.
**Description:** Lock the slug as canonical key. Mixpanel event prefix (`wallet.m2m_settlement.cold_state_viewed`), Storybook story path (`wallet/m2m-settlement/cold-state`), Sentry tag, JIRA component, ds-service entity. New tool `resolve(slug)` returns Figma frames + PRD states + Storybook stories + last week's events + open Sentry issues — joined by slug.
**Rationale:** This is the compounding move. Every new tool joining on the slug becomes 10× more valuable.
**Downsides:** Cross-team commitment beyond design + PM. Ratchets over quarters, not weeks.
**Confidence:** 72% | **Complexity:** Medium in this team; high coordination outside | **Status:** Explored — folded into U9b + conventions doc

## Rejection summary

| # | Idea | Reason rejected |
|---|------|-----------------|
| R1 | Unified DRD+PRD single document | Strong reframe but radical (collapses 2 storage layers + 6 tools). Honorable mention — future brainstorm. |
| R2 | PRD as state machine (transitions over states) | Strong basis but requires fundamental data model rework. Honorable mention — v2 of the plan. |
| R3 | Designer authors PRD via Figma plugin sidebar | Duplicates #1 at higher cost (plugin surface = new dependency). |
| R4 | Auto-suggest frame tags via embedding match | Speculative; #1 deterministic. |
| R5 | Remove `prd.export` entirely | Conflicts with ind-suite parity goal. |
| R6 | Skip Phase 1 stdio entirely; remote-only from day 1 | #5 file-scoped auth sidesteps the OAuth gap reason without forcing a single phase. |
| R7 | Air traffic control handoff (two-party readback) | Covered by #3 (role binding) + #6 (wall surfaces unbound). |
| R8 | Server-as-namer (hash IDs, no human-canonical names) | Subject-replacement — kills KTD-4. |
| R9 | Zero MCP tools, pure-chat with context injection | Loses tool affordance; Claude can't write `prd_state` deterministically. |
| R10 | Figma-as-truth (no ds-service DB, comments as PRD storage) | Subject-replacement; abandons plan's storage model. |
| R11 | 100 PMs / 1 PRD multiplayer co-authoring | Not grounded; friction not demonstrated. |
| R12 | 1 PM commissions 100 PRDs via roadmap CSV | Unjustified leap on Claude's drafting quality. |
| R13 | PRDs as Markdown in git, reviewed via PR | Subject-replacement; collapses ds-service authority. |
| R14 | Events-only PRDs (the doc IS the tracking plan) | Inverts user value; absorbed by #2 spec stems. |
| R15 | "Change is the unit, not sub-flow" | Strong reframe but rebuilds data model. Honorable for v2. |
| R16 | Diff-since standalone tool | Subsumed by #6 (last_touched_at) + #3 (role-change events). |
| R17 | Frame thumbnails as universal embeddable primitive | Subset of #7 (slug resolver returns frames; proxy already exists). Honorable mention. |
| R18 | Ship minimal Mixpanel event schema now (standalone) | Absorbed into #2 spec stems. |
| R19 | Base camps / named checkpoints | Covered by #6 wall. |
| R20 | Definitions section / role aliases (legal contract) | Same structural idea as #3; #3 is the implementation form. |
| R21 | Lichen / PRD-first mode (proposed/pending/bound) | Plan already supports DRD-before-Figma; extension is incremental. |
| R22 | Newspaper copy-desk / length-agnostic prose | Strong analogy; reframes viewer model. Honorable for v2. |
| R23 | Tool description bloat standalone | Covered by #4 progressive discovery. |
| R24 | Silent rename orphan as standalone pain | Absorbed into basis for #3 and #6. |

## Coverage check

All 5 axes have ≥1 survivor:
- **Tool surface design**: #4
- **Naming + binding**: #3, #7
- **Authoring UX**: #1, #6
- **Auth/transport/scale**: #5
- **Viewer + downstream**: #2, #7

No empty axes; no recovery dispatch needed.
