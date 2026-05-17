# The `{sub_product}/{sub_flow}` universal slug

**Status:** Active contract (ds-service ships the resolver; downstream adoption rolls per team).
**Owners:** Design Systems (resolver shape), Analytics (Mixpanel), Eng Platform (Sentry, Storybook), PMO (JIRA).
**Plan reference:** [`docs/plans/2026-05-17-002-feat-mcp-ds-service-pm-workflow-plan.md`](../plans/2026-05-17-002-feat-mcp-ds-service-pm-workflow-plan.md), KTD-6 + U9b.

## TL;DR

Every system that names a piece of INDmoney's product surface uses the same string:

```
{sub_product}/{sub_flow}            # entity-level
{sub_product}/{sub_flow}/{state}    # state-level
```

Lowercase, kebab-case, `/` as the separator. No spaces, no version suffixes, no team prefixes.

Examples:

| Surface              | Slug                                       |
|----------------------|--------------------------------------------|
| Dashboard asset widget       | `dashboard/asset-widget`               |
| Dashboard liability widget   | `dashboard/liability-widget`           |
| Wallet M2M settlement        | `wallet/m2m-settlement`                |
| Wallet M2M settlement, cold  | `wallet/m2m-settlement/cold-state`     |
| INDstocks F&O option chain   | `indstocks/f-o/option-chain`           |
| Mutual Funds SIP review      | `mutual-funds/sip-review`              |

## Why one slug

Before this contract every team coined their own identifier:

- Analytics tracked events under ad-hoc names ("dashboard_asset_card", "DashAssetWidget").
- Sentry tagged issues by file path or component class.
- Storybook stories grouped by folder, not by product surface.
- JIRA components reflected reporting structure, not the user-visible flow.

The join cost was enormous: a PM asking "is the asset widget healthy?" had to translate the question into four different vocabularies and reconcile the answers by hand.

The fix is one string. The resolver (`/api/resolve/{slug}` and the MCP `resolve` tool) becomes the single read-side index — fan-out to every downstream system, return a joined view. **The point of shipping the resolver is that the *shape* is committed; downstream population ratchets per team without renegotiating the wire shape.**

## The contract

### Slug shape rules

1. **Lower-case kebab.** Replace any non-`[a-z0-9]` run with a single `-`. Trim leading/trailing hyphens. `subFlowSlugify` in [`services/ds-service/internal/projects/subflow.go`](../../services/ds-service/internal/projects/subflow.go) is the canonical normaliser.
2. **The sub-product uses the *short* slug**, not the display name. `wallet`, not `Wallet`. `mutual-funds`, not `Mutual Funds`. `indstocks`, not `INDstocks`. The display name lives in `sub_product.name`; the slug lives in `sub_product.slug`. Always reach for the slug.
3. **The separator is `/`.** Not `.`, not `-`. Mixpanel events are the one place that uses `.` (see below) but the entity *identifier* is always slash-joined.
4. **Two or three segments only.** `wallet/m2m-settlement` (entity-level) or `wallet/m2m-settlement/cold-state` (state-level). A four-segment slug is a contract violation — surface it as an error rather than truncating.
5. **State segment is the lower-case kebab of the PRD state label.** "Cold state" → `cold-state`. "Empty hero" → `empty-hero`. When in doubt, ask the PM.

### What each segment means

| Segment | Source of truth | Notes |
|---------|----------------|-------|
| `sub_product` | `sub_product.slug` (sqlite, mig 0036) | One per top-level LOB bucket. Wallet, Lending, INDstocks, Mutual Funds, … |
| `sub_flow` | `sub_flow.slug` (sqlite, mig 0036) | One per Figma section. Scoped to a `sub_product`. |
| `state` | `prd_state.label` (sqlite, mig 0037), kebab-cased | One per "Possible State" row in the PRD. Matches the designer's frame name verbatim when an auto-skeleton row exists. |

### Conventions per surface

The resolver promises a joined view. Each downstream system adopts the convention on its own timeline; the resolver tolerates missing data and returns empty arrays for the surfaces that haven't onboarded yet.

| Surface           | Convention                                                                                  | Status |
|-------------------|----------------------------------------------------------------------------------------------|--------|
| **Figma section name** | `{sub_product}/{sub_flow}` (display-case, parsed by [`ParseSectionName`](../../services/ds-service/internal/projects/figma_section_parser.go))     | Live — designers already follow this. |
| **PRD entity**         | `sub_flow.slug` joined with `sub_product.slug`; `prd_state.label` for the state segment | Live — autosync writes the rows on every section detection. |
| **Mixpanel event name** | `{sub_product}.{sub_flow}.{verb}` — dots between segments (Mixpanel's display likes dots); verb taxonomy TBD by analytics team | Shape committed; verb taxonomy + validation deferred (`prd_state_event.name` is unvalidated today — see U9b Execution Notes §A.4). |
| **Storybook story path** | `{sub_product}/{sub_flow}/{state}` matches the Storybook hierarchy verbatim | Shape committed; adoption is per-team Storybook rollout. |
| **Sentry tag**     | `feature={sub_product}/{sub_flow}` (entity-level); state can be a second tag `state={state}` when finer granularity matters | Shape committed; Sentry SDK config is per-platform work. |
| **JIRA component** | `{sub_product}/{sub_flow}` (use the literal slash; JIRA accepts it). If a project's JIRA component naming rule rejects slashes, fall back to `{sub_product}-{sub_flow}` — call it out in the project README so the resolver can map both. | Shape committed; JIRA component creation is per-project ops. |

### What goes in `state` and what doesn't

The state segment is **the smallest unit a PM thinks about as a separable design**. "Cold state", "loading", "error", "logged-out", "empty hero", "success toast" — yes. "Cold state with one item", "loading inside a modal" — no, those are conditions inside a state, expressed as `prd_state.condition_md`. The bar is roughly: would the designer draw it as a separate frame? If yes, it's a state.

## Adoption playbook

The resolver is the carrot. As each system adopts the convention, the resolver's response gets one more populated array, and every consumer of the resolver picks up the new data for free.

1. **Analytics** — start populating `prd_state_event.name` with the `{sub_product}.{sub_flow}.{verb}` shape; the resolver's `mixpanel_event_names` array picks it up immediately. A later milestone wires `recent_events` to live Mixpanel-API reads.
2. **Storybook** — reorganise story files under `src/stories/{sub_product}/{sub_flow}/{state}.stories.tsx`. The resolver's `storybook_paths` array can be populated by a one-time crawl of the Storybook manifest.
3. **Sentry** — set `feature` tag on every captured event (one line in the SDK `beforeSend` hook). `open_sentry_issues` populates via the Sentry API once the tag is reliably present.
4. **JIRA** — create a component for every sub-flow as it ships. Component name = the slug. Existing tickets get re-tagged in a batch. `jira_components` array populates via the JIRA REST API.

None of these are blocking each other. The resolver is the contract; the integrations are async.

## How to use the resolver

```bash
# HTTP
curl -H "Authorization: Bearer $JWT" \
  https://docs.indmoney.com/api/resolve/wallet/m2m-settlement
```

```ts
// In MCP-using code (Claude Code, Connector clients, etc.)
const res = await invoke("resolve", { slug: "wallet/m2m-settlement/cold-state" });
```

Response shape (truncated):

```jsonc
{
  "data": {
    "slug": "wallet/m2m-settlement/cold-state",
    "level": "state",
    "sub_flow": {
      "id": "...",
      "name": "M2M Settlement",
      "slug": "m2m-settlement",
      "sub_product": "Wallet",
      "full_slug": "wallet/m2m-settlement"
    },
    "state": { "id": "...", "label": "Cold state", "position": 0, "frame_name": "Cold state" },
    "prd_exists": true,
    "frame_count": 12,
    "state_count": 7,
    "mixpanel_event_names": ["wallet.m2m_settlement.click_add_money", "wallet.m2m_settlement.view"],
    "drd_exists": true,
    "prototype_url": "",
    "canvas_lifecycle": "design-shipped",
    "recent_events": [],
    "open_sentry_issues": [],
    "storybook_paths": [],
    "jira_components": [],
    "links": {
      "prd_viewer_url": "/prd/wallet/m2m-settlement",
      "figma_url": "https://www.figma.com/file/.../?node-id=...",
      "conventions_doc_url": "/docs/conventions/sub-product-slug"
    }
  },
  "next_actions": [ /* … */ ]
}
```

The empty arrays (`recent_events`, `open_sentry_issues`, `storybook_paths`, `jira_components`) are **load-bearing**: they serialize as `[]`, never `null`, so consumers can key on them with a stable shape.

## Where to file changes

- **Slug normalisation rules** — `subFlowSlugify` in [`services/ds-service/internal/projects/subflow.go`](../../services/ds-service/internal/projects/subflow.go). Changes propagate through autosync.
- **Resolver shape** — [`services/ds-service/internal/mcp/tools_resolve.go`](../../services/ds-service/internal/mcp/tools_resolve.go). Wire-shape changes must bump the schema-version handshake (TBD) before rolling out.
- **This contract** — open a PR against this doc and tag the four owners listed at the top.
