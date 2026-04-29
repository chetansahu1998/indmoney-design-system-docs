---
date: 2026-04-29
topic: projects-flow-atlas
status: ready-to-plan
---

# Projects · Flow Atlas (canvas + DRD + violations + mind graph)

## Problem Frame

INDmoney designers ship product flows by stitching screens together in Figma, then handing them off via Slack threads, Notion docs with pasted screenshots, and FigJam boards. By the time engineering reads the file, drift has accumulated, decisions have been forgotten, and reviewers have to play archaeologist across 4 surfaces to figure out what was decided. The docs site today shows components and tokens but doesn't carry the *flow* — there's no shipped surface that says "here's how `Onboarding` works in `Indian Stocks` for a `KYC-pending` user, in light + dark, and here's everything wrong with it."

We have most of the foundation already. `services/ds-service/internal/audit/` runs token / spacing / radius drift detection. The Files tab + audit pipeline ships per-file scores. The plugin already publishes / audits / pulls library content. DesignBrain-AI's BlockNote+Yjs editor, journey-graph algorithms, governance-rules engine, and figma-clipboard parser are extraction-ready foundation. What's missing is the unifying surface that turns these into a system designers, DS leads, PMs, and engineers all open daily.

This brainstorm captures **Projects** — a four-lens system over a unified design knowledge graph. Designers export flows from Figma via the plugin; the system stores them as canvas-faithful renderings; an audit engine runs token/theme/component/accessibility/flow rules over each version; a Notion-style DRD anchors decisions; a mind graph navigates the whole system. It is **advisory, not blocking** — for a 300-person org, governance comes from making the right things visible and easy, not from gating PR merges.

The surface is one product with four lenses on the same graph:

```
Project → Flow → Persona → Screen → Component → Token
                       ↓        ↓         ↓
                   Decision  Violation   Mode
```

- **Atlas** — spatial lens (top-half canvas, screens at preserved x/y from the originating Figma section)
- **DRD** — narrative lens (Notion-style document anchored to a flow)
- **Violations** — quality lens (audit rules running over screens, presented as 4 surfaces: per-flow tab, designer inbox, per-component reverse view, DS lead dashboard)
- **Mind graph** — relational lens (Obsidian-style 2D-brain navigator over products → folders → flows → personas → components → tokens → decisions)

---

## Actors

- **A1. Designer (in-product team)** — works in Figma, runs the plugin's *Projects* mode to export selected sections, owns the DRD for flows they ship, addresses violations in their inbox. Primary author. Editor-by-default on flows in their Product.
- **A2. Designer (other product)** — viewer-by-default elsewhere. Browses the atlas + mind graph for cross-product reference and inspiration. Comment-permissioned org-wide.
- **A3. DS lead** — admin everywhere. Curates the Product → folder taxonomy, the persona library, the rule catalog, and severities. Reads the DS lead dashboard weekly. Approves new folders / personas suggested by designers. Reviews dismissed / acknowledged violations and overrides when needed.
- **A4. PM** — commenter org-wide. Browses flows + DRDs to understand product state, attends design reviews where the DRD + Atlas are the artifact, contributes to decisions via comments.
- **A5. Engineer** — commenter org-wide. Pulls the JSON tab to read raw Figma node data when implementing, reads decisions for context, files violations against their own components.
- **A6. ds-service (Go backend)** — runs the audit core, persists projects, fans out re-audit jobs on rule/token changes, exposes WebSocket/SSE for progressive UI updates.
- **A7. Plugin (Figma)** — gains a 4th mode "Projects". Sends export payloads, applies auto-fix on token/style violations, displays inline audit feedback during design.
- **A8. Docs site (Next.js)** — renders the atlas + DRD + violations + JSON tabs and the mind graph; subscribes to backend events for live updates.

---

## Key Flows

### F1. Designer exports selected sections to Projects

- **Trigger:** Designer selects N frames / sections in Figma → opens plugin → switches to *Projects* mode
- **Actors:** A1, A6, A7
- **Steps:**
  1. Plugin reads selection: walks ancestry, groups frames by enclosing SECTION; loose frames become a "freeform" group.
  2. Plugin auto-detects light/dark mode pairs within each group via spatial geometry + `explicitVariableModes` field on each frame.
  3. Plugin shows the export modal with the auto-grouped preview:
     ```
     Flow A — "Learn Touchpoints in F&O"  (6 frames, 3 light/dark pairs detected)
       Platform [Mobile ▾]  Product [Indian Stocks ▾]  Path [F&O / Learn Touchpoints]  Persona [Default ▾]
     Flow B — freeform 4 frames
       Platform [Mobile ▾]  Product [Indian Stocks ▾]  Path [F&O / Practice Mode]  Persona [Default ▾]
     [+ Add row]   [Send to Projects]
     ```
  4. Designer can edit any field, split a flow, merge two flows, ungroup auto-detected mode pairs, rename, swap personas. Path is autocomplete-suggested from existing folders + DS-lead-curated taxonomy.
  5. On Send → plugin POSTs `{file_id, flows: [{section_id, frames[], platform, product, path, persona, name}]}` to ds-service.
  6. ds-service responds 202 with `{project_id, version_id, deeplink}` immediately.
- **Outcome:** Designer sees a toast "Project created — audit running in background" with a deeplink to the new version. Plugin closes / returns to Projects mode list.
- **Covered by:** R1, R2, R5, R6, R8

### F2. Hybrid two-phase pipeline lands the export

- **Trigger:** ds-service receives the export from F1
- **Actors:** A6, A8
- **Phase 1 (~10–15s, fast preview):**
  1. ds-service pulls raw node JSON depth=3 for each frame via Figma REST.
  2. Renders each frame as PNG at 2x via /v1/images?format=png&scale=2; stores in S3-compatible asset store with CDN.
  3. Identifies mode pairs: same section, same column-x, different row-y, identical structural skeleton, only `explicitVariableModes` differs. Stores ONE canonical_tree per logical screen + a `modes[]` sidecar.
  4. Persists Project / Flow / Version / Screen records. Marks status=`view_ready`.
  5. Emits WebSocket event `project.view_ready` to subscribed clients → frontend can now open the project view.
- **Phase 2 (async, audit):**
  6. Worker queue processes the audit per persona × theme × screen.
  7. Theme parity diff: for each mode pair, asserts no structural delta beyond `explicitVariableModes`. Any other delta = **Critical** theme parity violation.
  8. Token / style / component / accessibility / flow rules run; violations persist to `violations` table with severity from rule catalog.
  9. Cross-persona consistency check: components used in Persona A but missing in Persona B → flagged.
  10. Emits `project.audit_progress` events as each screen completes; final `project.audit_complete` when done.
- **Outcome:** Frontend's violation count ticks upward in real time; at done, project shows full audit + cross-checks.
- **Covered by:** R3, R4, R7, R9, R10

### F3. Designer / PM opens the project view

- **Trigger:** Click deeplink from F1 toast, or click flow leaf in mind graph (F8), or pick from designer inbox (F5)
- **Actors:** A1, A2, A4, A5, A8
- **Steps:**
  1. Top half: atlas surface — three.js + react-three-fiber renders each screen as a textured plane at preserved (x, y) from the Figma section. Pan, zoom, snap-to-frame on click.
  2. Theme toggle + Persona toggle in canvas chrome. Toggling re-resolves the rendered images and re-runs the JSON tab against the active mode/persona.
  3. Bottom half: tabs — DRD · Violations · Decisions · JSON (4 tabs).
     - **DRD**: BlockNote (Yjs-collab-backed) Notion-style editor with custom blocks: `/decision`, `/figma-link`, `/violation-ref`, code, table, image, callout, embed.
     - **Violations**: list grouped by severity (Critical / High / Medium / Low / Info), filtered by active persona × theme. Click → highlights node in atlas; for auto-fixable, shows "Open in plugin to fix" button.
     - **Decisions**: list of first-class Decision entities for this flow. Click → expands inline with full body, supersession chain, links_to_components.
     - **JSON**: tree viewer on the canonical_tree of the currently-selected screen. Toggle between modes shows resolved values; raw `boundVariables` always visible.
  4. Click any frame in atlas → bottom switches to JSON tab focused on that screen.
- **Outcome:** Designer / PM has the full picture in one view; switching tabs swaps lens, toggles swap dimension.
- **Covered by:** R11, R12, R13, R14, R20

### F4. Re-export creates a new Version

- **Trigger:** Designer iterates in Figma, runs plugin again with same selection (or different)
- **Actors:** A1, A6, A8
- **Steps:**
  1. Plugin sends export payload identifying flows by `section_id` + path + persona; ds-service detects existing flows by (file_id, section_id) + (path) match.
  2. For each matched flow: creates a new Version. Old version becomes read-only. DRD does NOT migrate (it's per-flow, living, single-source).
  3. Decisions stay attached to the version they were made on (immutable). New decisions made on the new version.
  4. Audit re-runs (Phase 2 of F2) on the new version. Active violations from previous version that still exist roll forward; ones now Fixed mark as Fixed; new violations land as Active.
  5. UI's version selector now shows "v2 · today" + "v1 · 2 weeks ago"; default = latest.
- **Outcome:** Continuity: DRD & decisions stay; canvas, screens, violations refresh.
- **Covered by:** R5, R6, R7, R15

### F5. Designer addresses a violation

- **Trigger:** Designer opens personal inbox (`/inbox`)
- **Actors:** A1, A7, A8
- **Steps:**
  1. Inbox lists every Active violation across every flow the designer is editor on, grouped by severity, sortable by age.
  2. Designer picks one → routed to the flow's project view, atlas pre-zoomed to the offending frame, violation highlighted in Violations tab.
  3. For **token / style** violations marked auto-fixable (~60% coverage): a "Fix in Figma" button. Clicking opens the plugin in Audit mode, pre-loaded to this violation. Plugin applies via `setBoundVariableForPaint` / equivalent; designer confirms; plugin sends a success ping to ds-service which updates the violation to Fixed.
  4. For non-auto-fixable: designer fixes manually in Figma, re-exports (F4), violation auto-resolves.
  5. Alternative: designer clicks "Acknowledge" with one-line reason → violation moves out of priority view but stays Active.
  6. Or "Dismiss" with rationale → violation marked Dismissed; carries forward to future versions unless DS lead overrides.
- **Outcome:** Inbox empties as designer works through the queue; DS lead sees aggregate progress.
- **Covered by:** R7, R16, R17, R23

### F6. Designer authors the DRD

- **Trigger:** Designer opens flow's project view → DRD tab
- **Actors:** A1, A4, A5, A8
- **Steps:**
  1. BlockNote editor loads the flow's living DRD content (one document per flow). Yjs collaboration: multiple editors see each other's cursors.
  2. Designer pastes from Notion / Word / Markdown — paste handlers extract structured content (tables, callouts, images embedded as data URLs persisted to asset store).
  3. Designer types `/` → block menu. Custom blocks: Decision, Figma-link (auto-rendered as a card with thumbnail), Violation-ref (auto-rendered as the violation card with current status), Acceptance-criteria, Requirement.
  4. `/decision Title…` creates a first-class Decision entity AND embeds the card. Decision metadata: id, title, body, made_on, made_by, supersedes (optional dropdown of prior decisions in this flow), status (Proposed / Accepted / Superseded), links_to_components[], links_to_screens[].
  5. PMs and engineers can add comments inline (BlockNote's inline-comment system); comments threaded; @mention notifies via in-app inbox + optional Slack digest.
  6. DRD persists continuously via Yjs server. Activity log records every edit attributable to user.
- **Outcome:** DRD becomes the durable narrative + decision record; survives Figma re-iterations.
- **Covered by:** R13, R18, R19

### F7. DS lead curates rules, taxonomy, personas

- **Trigger:** DS lead opens admin surface (gated by admin role)
- **Actors:** A3, A6
- **Steps:**
  1. **Rule curation** — rule catalog editor: each rule has {id, name, description, category, severity (Critical/High/Medium/Low/Info), enabled, target_node_types, expression}. DS lead toggles enabled, edits severity, writes new rule expressions (CEL-based, ported from DesignBrain governance engine).
  2. **Taxonomy curation** — tree editor for Product → first-level folder. Adds folder, renames, archives. Designer-extended sub-folders are listed below; DS lead can promote a designer's path into the canonical taxonomy.
  3. **Persona library curation** — same shape as folders. DS lead approves designer-suggested personas; renames; archives.
  4. **Severity overrides** — DS lead can override a Dismissed violation back to Active or change its severity per-flow.
- **Outcome:** Any rule/taxonomy/persona change emits an event that fans out re-audit of every active flow's latest version.
- **Covered by:** R3, R4, R20, R21

### F8. Browse the mind graph

- **Trigger:** User opens `/atlas` (the mind graph entry route)
- **Actors:** A1, A2, A3, A4, A5, A8
- **Steps:**
  1. Three.js + r3f scene mounts. d3-force simulation places 9 Product nodes + curated folders + flows in a force-directed cloud. Postprocessing: bloom for the holographic glow, subtle organic drift animation.
  2. **Initial state:** "Brain" view. Only labels visible. The 9 Product names glow brighter; everything else dim. Filter chips at top: [Hierarchy] (default on) · [Components] · [Tokens] · [Decisions]. Universal toggle: Mobile ↔ Web (crossfades the entire graph).
  3. **Hover any node** → floating signal card: type, parent path, severity counts (Critical/High/Medium/Low/Info), persona count, last-updated, last-editor, "Open project →" CTA.
  4. **Click a Product** (e.g., Indian Stocks): camera zooms in (smooth tween), other 8 products dim and recede; clicked product's children spring outward (sub-folders + flows).
  5. **Recursive zoom:** click a folder → zoom into its children. Click a flow leaf → **shared-element morph** at ~600ms: leaf's circle + label tween into the project view's title bar; brain dissolves; canvas + tabs render behind.
  6. Click "back" / press Esc on project view → reverse morph; brain reconstitutes around the leaf.
  7. Toggle a filter chip on (e.g., Components) → component nodes fan out as smaller satellite nodes around flows that use them. New edges fade in (`uses` thin neutral). Toggle Tokens chip → token nodes appear; `binds-to` edges (dashed accent). Toggle Decisions chip → Decision nodes + `supersedes` arrows.
- **Outcome:** Mind graph is the navigator + reverse-lookup atlas for the entire system. Every other surface is reachable from here.
- **Covered by:** R22, R24, R25

### F9. Audit fan-out on rule / token change

- **Trigger:** DS lead publishes a token catalog update OR enables / curates a rule
- **Actors:** A3, A6, A8
- **Steps:**
  1. ds-service emits `rule.changed` or `tokens.published` event.
  2. Worker enqueues re-audit jobs for every active flow's latest version. Priority queue: recently-edited flows first, archived flows last; rate-limited so a single token publish doesn't melt the worker pool.
  3. Each job re-runs Phase 2 of F2 against the existing canonical_trees; produces new violations / closes resolved ones.
  4. WebSocket events update the affected project views in real time. Designer inboxes refresh.
- **Outcome:** Violation counts stay current. DS lead sees the org-wide impact of a rule change immediately.
- **Covered by:** R10, R21

---

## Acceptance Examples

- **AE-1** *(Designer exports a flow)* — Aanya selects 6 frames in `INDstocks V4 / Learn Touchpoints in F&O` (3 light, 3 dark) → opens plugin → Projects mode → modal shows "1 flow detected, 3 light/dark pairs auto-detected" → she picks Product=Indian Stocks, Path=F&O/Learn Touchpoints, Persona=Default, Platform=Mobile → Send. Toast appears with deeplink in <12s. Project view opens; canvas renders the 3 logical screens at preserved x/y; theme toggle works. Audit completes ~60s later; violation count badge appears with 4 High and 2 Medium.
- **AE-2** *(Theme parity catches a manual paint)* — Aanya hand-painted a button background in dark mode instead of binding to the token. Audit detects: same section, same column, structural skeleton matches except 1 fill node — light has `boundVariables.fills`, dark has raw color. Critical violation: "Theme parity break — node `Button/CTA/fill` is bound in light, hardcoded in dark." Auto-fix offered: "Bind dark variant to `colour.surface.button-cta`." Aanya clicks Fix in Figma → plugin opens → applies → re-export → violation Fixed.
- **AE-3** *(Cross-persona consistency)* — Aanya exports `Explore` for Persona=Default but forgets to re-export Persona=Logged-out. Audit catches: `Toast` component used in Default but missing from Logged-out. High violation: "Component coverage gap across personas." Aanya acknowledges with reason "Logged-out doesn't trigger network errors yet, deferred to v2."
- **AE-4** *(DRD + decision flow)* — In design review, PM Riya types `/decision` in DRD: "Approved padding-32 instead of grid-24 for the F&O Learn Touchpoint cards — explicit DS exception, will revisit when card grid is unified." Decision card embeds. Aanya's existing P3 padding violation can be linked: from Violations tab → Decisions chip → her violation now shows "Accepted by Decision dec_abc123 · 2026-04-29." DS lead reviewing dashboard sees the dismissal-with-decision is intentional, doesn't flag.
- **AE-5** *(Mind graph reverse lookup)* — DS lead Karthik opens `/atlas`. Hierarchy chip + Components chip on. Hovers `Toast` component (a satellite node). Card shows: "Used in 23 flows across 6 products, 4 critical violations, 7 high." Clicks `Toast` → graph re-centers on the component, with edges to every flow using it. Identifies that 3 flows in Tax product all have `Toast` violations → can plan a Tax design-review week.
- **AE-6** *(Re-export preserves DRD, refreshes audit)* — Two weeks later, Aanya re-exports the same flow with new screens. New version v2 created. DRD content unchanged. Decisions from v1 stay on v1; new decisions made on v2. Violations recomputed; 2 of 4 v1 violations now Fixed (auto-detected), 1 still Active, 1 Acknowledged-from-v1 carries forward, 3 new violations introduced.
- **AE-7** *(Token publish fans out)* — DS lead publishes a renamed token (`colour.surface.bg` → `colour.surface.surface-grey-bg`). Backend emits `tokens.published`; worker queue enqueues re-audit for every active flow's latest version. ~5 minutes later, all designer inboxes show the impact: 47 flows had drift related to the renamed token, now flagged as P2 violations with the rename suggestion.
- **AE-8** *(Mind graph → flow morph)* — Riya opens `/atlas`, clicks Indian Stocks → camera zooms; clicks F&O folder → zooms further; clicks "Learn Touchpoints" leaf. 600ms shared-element morph: the leaf's label travels into the project-view title bar. Brain dissolves. Canvas + DRD + Violations + Decisions + JSON tabs render behind.

---

## Requirements

### Pipeline & data

- **R1.** Plugin gains a 4th mode "Projects" alongside Publish · Audit · Library. The mode owns selection grouping, mode-pair detection, modal preview, and submission to ds-service. Existing modes unchanged.
- **R2.** Smart-grouping in plugin: walks selection ancestry to group frames by enclosing SECTION. Loose frames become "freeform" groups. Auto-detects light/dark mode pairs by spatial geometry (same x-column, different y-row) cross-validated against `explicitVariableModes` deltas. Designer can split / merge / ungroup before Send.
- **R3.** Hybrid two-phase backend pipeline. Phase 1 (≤15s p95): pull node JSON, render PNGs at 2x, persist project/version/screen records, identify mode pairs, mark `view_ready`. Phase 2 (async, ≤5min p95 for 50 frames): run audit engine end-to-end, emit progressive violation events.
- **R4.** Mode-pair storage. Store ONE canonical_tree per logical screen plus a `modes[]` sidecar `[{id: "light", frame_id, explicit_variable_modes}, {id: "dark", frame_id, explicit_variable_modes}]`. The viewer toggles modes by re-rendering the same tree against mode-resolved Variable values from `lib/tokens/indmoney/{semantic-light,semantic-dark}.tokens.json`. JSON tab does not duplicate trees.
- **R5.** Versioning. Every export creates a new Version (immutable). Old versions stay readable. DRD is per-flow, living, does not migrate per version. Decisions attach to the version they were made on. Activity log per write.
- **R6.** Re-export resolution. Existing flows matched by (file_id, section_id) + path + persona. New flows are auto-created when no match.

### Audit & violations

- **R7.** Audit engine extends `services/ds-service/internal/audit/`. Adds rule classes:
  - **Theme parity**: structural diff between mode pairs ≠ 0 nodes outside `explicitVariableModes` → Critical.
  - **Cross-persona consistency**: component sets, token bindings, screen counts compared across personas of same flow.
  - **Component governance**: detached instances, override sprawl, component sprawl.
  - **Accessibility**: WCAG AA contrast (4.5:1 / 3:1 large), touch target ≥44pt.
  - **Flow-level**: dead-end screens, orphan screens, cycles without exit, missing required state coverage (Loading / Empty / Error).
  - Existing rules retained: token color drift (OKLCH), text-style drift, padding/gap drift (4-pt grid), radius drift (pill rule + multiples-of-2), component matching (4-signal).
- **R8.** Violation lifecycle: **Active → Acknowledged → Fixed | Dismissed**. Fixed auto-resolves on re-export when offending node no longer triggers the rule. Acknowledged requires one-line reason, stays Active but de-prioritized in inbox. Dismissed requires rationale, carries forward across versions, can be linked to a Decision; DS lead can override Dismissed → Active.
- **R9.** Severity is 5-tier: Critical · High · Medium · Low · Info. Set per-rule by DS lead in rule curation. No flow rollup score — raw counts only.
- **R10.** Audit re-runs on (a) new export (Version created), (b) DS lead publishing a rule curation change, (c) DS lead publishing a token catalog update. Worker queue is priority + rate-limited.
- **R11.** Auto-fix scope: token + style class only (~60% coverage). Plugin offers "Fix in Figma" for: rebinding a raw fill to nearest semantic token, applying `textStyleId`, snapping a dimension to nearest 4-pt grid value. Other violations are manual-fix only.

### Surfaces

- **R12.** **Project view** — top-half atlas (three.js + r3f, frames as textured planes at preserved x/y, pannable + zoomable, click snaps to frame), bottom-half tabs **DRD · Violations · Decisions · JSON** with global Theme + Persona toggles in the canvas chrome.
- **R13.** **DRD tab** — BlockNote editor with Yjs collaboration. Custom blocks: `/decision`, `/figma-link`, `/violation-ref`, plus standard (table, code, callout, image, embed). Paste from Notion / Word / Markdown supported. Inline comment threads with @mention. Activity-log attribution on every edit.
- **R14.** **Violations tab** — list grouped by severity (Critical → Info), filtered by active persona × theme. Each violation: rule name, severity badge, offending node breadcrumb, fix suggestion, auto-fix CTA when applicable, lifecycle controls (Acknowledge / Dismiss / Open in Figma).
- **R15.** **Decisions tab** — list of first-class Decision records for this flow. Click → expand inline with body, supersession chain, links to Components / Screens. New decisions can be created here OR via DRD `/decision` slash.
- **R16.** **JSON tab** — tree viewer (collapsible, searchable) on the canonical_tree of the screen most recently selected in the atlas. Active mode + persona resolved at render. Raw `boundVariables` always shown.
- **R17.** **Designer personal inbox** (`/inbox`) — Active violations across every flow the designer is editor on, sorted Severity → Age. One-click navigate to the offending screen; bulk Acknowledge.
- **R18.** **Per-component reverse view** — extension of existing component detail pages. Adds a "Where this breaks" section listing every flow + screen with a violation against this component, grouped by severity.
- **R19.** **DS lead dashboard** (`/atlas/admin` or similar) — leaderboards: violations by Product, by severity, by trend over time. Filterable by team, rule, date. Top-violators list. Recent decisions feed.
- **R20.** **Mind graph** (`/atlas`) — three.js + r3f + d3-force + bloom. Initial brain view: 9 Products + folders + flows in force-directed cloud, hierarchy edges only by default. Filter chips at top toggle [Components] [Tokens] [Decisions] edge classes. Universal Mobile ↔ Web toggle crossfades graphs. Hover any node → floating signal card. Click product → camera zoom + child expand (others recede). Click flow leaf → 600ms shared-element morph into project view.

### Auth, comments, search, notifications

- **R21.** Role-based auth with explicit grants. Roles: viewer / commenter / editor / owner / admin. Defaults: designer = editor in own Product / viewer elsewhere; DS lead = admin everywhere; PM/Eng = commenter org-wide. Per-flow grant overrides supported. Identity via existing org SSO. Audit log of every write.
- **R22.** Lightweight comment threads on screens, on violations, on Decisions, inline in DRD. @mention triggers in-app inbox notification + optional Slack digest. Comments threaded; resolvable.
- **R23.** Global search across flow names, DRD content, decision titles + bodies, persona names, component refs. Indexed in primary store (Postgres full-text or Elasticsearch sidecar — chosen at planning). Result types tagged.
- **R24.** Notifications: in-app inbox (existing pattern) + opt-in Slack/email digest. Triggered events: export complete, audit complete, decision made, comment received with @mention, violation added on a flow you own, rule change affecting your flows.

### Cross-cutting

- **R25.** Mobile vs Web are separate IA trees. Each platform has its own copy of Products → folders → flows. Plugin's export modal asks platform first (auto-defaulted from frame width: <500px → Mobile; ≥1024px → Web; ambiguous → designer picks). Mind graph's universal toggle swaps the entire tree. Cross-platform comparison surfaces deferred (not required for this product).
- **R26.** Persona library is curated by DS lead with designer free-extend (autocomplete during plugin export; new entries land in a "Pending" pool DS lead approves). Same governance pattern as folder taxonomy.
- **R27.** Atlas + mind graph share the three.js + r3f render pipeline. Same shader chain (bloom postprocessing, depth-of-field). Transitions between mind graph and project view are shared-element morphs at ~600ms (Framer Motion `layoutId` for label/title hand-off, r3f scene crossfade for canvas).

---

## Scope Boundaries

### Deferred for later

- **Branch + merge review workflow.** Re-exports today create immutable versions. A "proposed branch → review → approve → canonical" flow is more rigorous but adds substantial UX + storage complexity. Defer until governance maturity demands it.
- **Comprehensive auto-fix.** Beyond token + style (R11), stretch into instance-override unwinding, naming-hygiene, structural reorganization. Higher coverage = higher risk; ship safe-only first, observe what designers ask to be auto-fixed, expand intentionally.
- **Cross-platform side-by-side comparison.** Mobile + Web flows are separate trees by design (R25). A specific surface for "show me Onboarding mobile next to Onboarding web" is valuable but not required; can be added as a manual link in DRD or a saved comparison route later.
- **Live mid-file audit (in-Figma)**. Today's plugin Audit mode runs on selection on demand. A continuous "linter while you design" mode is plausible but heavy; not in scope.
- **PRD / Linear / Jira integration.** Mind graph could traverse decisions → external tickets. Phase 2 (after the in-system graph is solid).
- **AI-suggested decisions / DRD drafts.** Could nudge designers to write decisions when violations are dismissed without one. Phase 2; depends on adoption signal.
- **Mobile designer app / iPad viewer.** Atlas viewable read-only on iPad would be nice for design reviews. Not v1.
- **Public read-only sharing.** External vendor or interview-candidate access. Requires sharing-link infrastructure + redaction. Not v1.

### Outside this product's identity

- **Replacing Figma.** This is not a design tool. No editing of frames in our atlas. Figma remains the source of truth.
- **Replacing Notion / Confluence org-wide.** The DRD is anchored to a flow. Cross-flow narrative documents (PRDs, weekly notes, OKRs) belong elsewhere.
- **Replacing Linear / Jira.** Violations are not tickets. Decisions are not tasks. We don't track sprints.
- **Replacing Mobbin.** External pattern reference + competitive research stays on Mobbin / external tools. We are about INDmoney's own system.
- **Hard governance / blocking PRs.** Authority is advisory (R8). The product will not gate engineering merges.

---

## Dependencies & Assumptions

- **Figma access.** Org has a Figma plan that supports plugin distribution + REST API at the volume needed (worst case ~1000 audit re-runs in <10 minutes when a token publishes). Existing FIGMA_PAT pattern carries over.
- **Variable Modes are stable.** The light/dark detection mechanism (R4) depends on `explicitVariableModes` being reliably populated by Figma's REST. Verified live against `INDstocks V4` file (2026-04-29). Assumption: Figma keeps this field stable across plan tiers.
- **DesignBrain reuse.** BlockNote+Yjs editor (DRD), JourneyGraph algorithms (flow validation), CEL governance engine (rule expressions), figmaClipboardImport (paste), MiniMap canvas (foundation only) extract from `~/DesignBrain-AI/`. Mind graph is net-new build.
- **Existing audit core.** `services/ds-service/internal/audit/` and the plugin's audit-server endpoint extend, not replace.
- **Existing manifest.** `public/icons/glyph/manifest.json` (parents + atoms with composes graph from plan-002) is consumed by the audit engine for component matching. No re-extraction required.
- **Identity.** Org SSO is available and integratable. Audit log infra (who did what when) is build-anew or piggybacks on an existing pattern (TBD at planning).
- **WebGL / r3f expertise.** Engineering team has or will hire / train someone comfortable with three.js + react-three-fiber. Fallback story for older browsers (Safari < 15) is "graceful degradation: 2D HTML grid with no animation."
- **Asset storage.** S3-compatible bucket with CDN edge available for screenshots and DRD-pasted images. Sizing assumption: a 50-frame section at 2x retina ≈ 50–150 MB raw; CDN-cached after first request.
- **300-person scale.** Worst-case load: ~50 designers × 5 exports/week = 250 exports/week. Re-audit fan-out on token publish: ~1000 active flows × ~10s audit = 3 hrs serial → must parallelize across 6+ workers to land in <30 min.

---

## Open Questions (left for Planning)

1. **Decision supersession UX.** When a new Decision supersedes an older one in the same flow, does the old one auto-mark as Superseded, or does it stay Accepted with a "see also" link? Affects Decisions tab UI and graph edge semantics.
2. **Inbox triage at scale.** A designer with 47 P1 violations needs bulk operations. Bulk Acknowledge is in R17; spec the exact UX (multi-select, reason templates, undo window).
3. **DRD migration on a flow rename.** If a flow's path moves (e.g., Indian Stocks/F&O/X → Indian Stocks/Trade/X), DRD follows. But search index references? Permalinks? Spec the redirect / link-aliasing.
4. **Mode-pair edge cases.** What if a designer ships a flow with light only? With three modes (light, dark, sepia)? With mode pairs that DON'T have matching x-columns? Spec the detection fallbacks + designer override in plugin.
5. **Atlas zoom strategy.** Frames range from 24×24 icons to 4886-tall mobile flows. At full-zoom-out, a 4886-tall frame is a thin column; at full-zoom-in, a 24×24 icon is a postage stamp. Spec min/max zoom + LOD tiles.
6. **Mind graph performance ceiling.** d3-force at 1000+ nodes degrades. Spec node count budget per zoom level + culling strategy.
7. **Comment portability.** When a screen is replaced (re-export), comments tied to a specific node may have lost their target. Spec the migration / orphan handling.
8. **Permission inheritance.** When a Product folder is reorganized, do existing per-flow grants persist or reset? Spec the inheritance + override semantics.
9. **Persona pending pool.** R26 says new personas land in a Pending pool DS lead approves. Until approval, who can see / use the new persona? Spec the visibility window.
10. **Slack/email digest content.** R24 says opt-in. Spec the digest cadence + format.
