---
title: "feat: ds-service MCP layer — spec compliance + Claude Connector readiness"
status: completed
created: 2026-05-18
completed: 2026-05-18
type: feat
depth: deep
audit_source: chat-audit, late 2025/2026 best-practices research
target_repo: indmoney-design-system-docs
ship_commits:
  - 09357ba  # U4 — Tool metadata satellite interfaces (Title/SideEffects/DeferLoading)
  - 0632270  # U1+U2+U3 — JSON-RPC transport, envelope adapter, constitution doc
  - 9fa6fc2  # U5 — boundary descriptions + per-param schemas (26 tools)
  - 15c559d  # U9 — nested _meta object on tools/list descriptors
  - 5f5fda1  # U6 — promote 11 prd.author sub-ops to first-class Visible tools
  - 3aa57d8  # U7 — Atlas /api/prd/.../full migrated to prd.get
  - b5685d4  # U8 — OAuth 2.1 + PKCE for the Claude.ai Connector flow
  - 2211189  # U10 — SSE upgrade on GET /mcp + tools/list_changed notification
  - 2114649  # U11 — HTTP-layer integration tests (incl. OAuth → MCP bridge)
---

# feat: ds-service MCP layer — spec compliance + Claude Connector readiness

This plan brings `services/ds-service/internal/mcp/` to compliance with the November 2025 MCP specification (`anthropic-beta: mcp-client-2025-11-20`) and the Anthropic Claude Connectors contract, while preserving every architectural choice the audit flagged as well-built.

The work is additive — the existing `POST /v1/mcp/invoke/{name}` REST surface stays alive in parallel so Atlas, the local stdio bridge at `~/.claude/plugins/ind-suite/mcp-bridge`, and the `/ind-prd` skill keep working through the migration.

**Origin signal:** the audit (chat) graded the current implementation at ~65%. Architectural choices — two-tier visibility, composite tools, tenant-scoped `Deps`, progressive discovery via `NextActions`/`SchemaHint` — are strong. The 35% gap is almost entirely "we built a REST surface; need an MCP-spec surface alongside it" plus the description/safety quality sweep and OAuth 2.1.

---

## Summary

| Tier | Units | Goal |
|---|---|---|
| **Foundations** | U1 → U4 | Streamable HTTP transport, MCP-shaped envelope, constitution doc, Tool interface extensions |
| **Sweep + migration** | U5 → U7 | Description sweep across all 26 tools, promote `prd.author` op-dispatch to top-level tools, update Atlas + skill consumers |
| **Scale** | U8 → U11 | OAuth 2.1 + PKCE, `defer_loading` markers, `tools/list_changed` SSE, HTTP-layer integration tests |

End state: the binary serves a fully MCP-spec-compliant Streamable HTTP transport at `POST /mcp`, the existing REST surface still serves Atlas + bridge, OAuth 2.1 + PKCE lets Claude.ai onboard the connector through the standard user flow, and every tool description tells Claude when NOT to use the tool. The 26 tools become 26 + 11 (top-level `prd.*` aliases for the op-dispatch sub-verbs) with backwards compatibility preserved.

---

## Requirements Trace

Carried forward from the audit (chat) + Nov 2025 spec.

| R-ID | Requirement | Source |
|---|---|---|
| R1 | Expose Streamable HTTP MCP transport at `POST /mcp` speaking JSON-RPC (initialize, tools/list, tools/call, notifications/initialized) | spec normative |
| R2 | Wrap `Result{Data, NextActions, SchemaHint}` into MCP `{content[], structuredContent, isError}` shape on the new transport without breaking the REST surface | spec normative |
| R3 | Wire `initialize.serverInfo.instructions` with a versioned constitution doc (~300-500 words) | spec normative; community pattern |
| R4 | Convert tool-execution errors to `isError: true` in body; reserve JSON-RPC `-32xxx` codes for protocol errors only | spec normative |
| R5 | Every tool description includes when-to-use AND when-NOT-to-use boundary (arxiv 2602.14878) | best practice |
| R6 | Every `InputSchema()` property carries its own `description` field | observed across mature servers |
| R7 | Tool interface declares `SideEffects()` (ReadOnly \| Mutating \| Destructive) — surfaced as prefix in spec-shape tools/list output | best practice; GitHub MCP convention |
| R8 | Tool interface declares `Title()` for UI display, distinct from machine `Name()` | spec 2025-06-18 |
| R9 | `prd.author` op-dispatch promoted to 11 first-class top-level tools (`prd.add_state`, `prd.add_event`, etc.); op-dispatch kept as deprecated alias for one release | spec tool-call shape doesn't support op subdispatch |
| R10 | All Atlas fetches + the `/ind-prd` skill updated to consume the new top-level `prd.*` tools | consumer migration |
| R11 | OAuth 2.1 with PKCE for the Claude Connector flow — `/v1/oauth/authorize`, `/v1/oauth/token`, refresh-token storage | Connector spec, December 2025 |
| R12 | Per-tool `DeferLoading() bool` annotation, surfaced in MCP `tools/list` so Claude can lazy-load deep tools beyond a threshold | Anthropic January 2026 |
| R13 | Emit `notifications/tools/list_changed` over SSE on tool catalog mutations (today: never; future: per-user capability filtering) | spec capability |
| R14 | HTTP-layer integration tests for the new `/mcp` transport covering initialize handshake, tools/list, tools/call (happy + error), notifications | no current coverage |

---

## Out of Scope

Carried forward from the audit's Tier 3 deferrals + the "epitome" mandate (which folded most of Tier 3 in).

### Deferred to Follow-Up Work
- **Per-user tool capability filtering** — once OAuth scopes land (U8), `Registry.ListVisible(claims)` becomes the natural place to filter by role. Plan structure supports it; out of scope here.
- **MCP `prompts/*` and `resources/*` primitives** — spec allows servers to expose prompts (templated user-facing snippets) and resources (read-only blobs). Not needed for our tool surface; revisit when authoring skills need pre-canned prompt snippets.
- **Streaming progress notifications** — none of our 26 tools are long-running today. The Tool interface stays synchronous. If `prd.export` grows expensive or a future tool runs a job, add `notifications/progress` then.
- **`/v1/mcp/invoke/{name}` REST surface deprecation** — kept indefinitely in this plan; the REST shape is what Atlas + the bridge speak. Deprecation is a separate plan after both consumers migrate to the new transport (likely 6+ months out).

### Outside this product's identity
- **Public MCP marketplace listing** — Anthropic's marketplace requires their review process. We control all consumers today; if a marketplace listing becomes a goal later, that's a productization decision.
- **Generic OAuth-as-a-service for ds-service** — this plan adds OAuth strictly for the MCP Connector path. The existing `/v1/auth/login` email/password flow stays the canonical user-onboarding path; OAuth is for delegated agent access.

---

## Key Technical Decisions

### KTD-1: Additive transport, not replacement

The new `POST /mcp` route lives alongside the existing `GET /v1/mcp/tools` and `POST /v1/mcp/invoke/{name}`. Both surfaces dispatch through the same `Registry`. Atlas + the stdio bridge continue speaking REST; Claude Connectors speak MCP-spec JSON-RPC. The `Result` struct stays the canonical in-process return shape; the new transport wraps it into MCP-shape on egress.

*Rationale:* the audit's #1 constraint. Breaking five Atlas fetch call sites + the deployed bridge to switch wire formats is a separate migration with cross-team coordination. Additive ships now.

### KTD-2: Constitution as Go const, not filesystem-loaded

The constitution string is a `const` in `services/ds-service/internal/mcp/constitution.go`, embedded in the binary. Built from a markdown source under `services/ds-service/internal/mcp/constitution.md` via `//go:embed`.

*Rationale:* type-safe versioning, zero runtime file IO, ships with the binary, no environment-specific dependency. PM-edit-ability is preserved because the `.md` source is in git; engineering owns the deploy step.

### KTD-3: `prd.author` migration — promote + alias for one release

`prd.add_state`, `prd.add_event`, etc. become first-class top-level tools registered in their own right. `prd.author` stays registered but its `Invoke` becomes a thin shim that delegates to the new top-level tools and emits a deprecation note in `nextActionsForPRDOp`. After one release (~30 days), `prd.author` returns `ErrDeprecatedTool` with a redirect hint.

*Rationale:* MCP's `tools/call` shape doesn't support nested op subdispatch — Claude can't natively call `{name:"prd.author", arguments:{op:"add_state", args:{...}}}` as a clean tool call. Promotion is the structural fix. Alias preserves Atlas + skill consumers during the migration window.

### KTD-4: SideEffects enum on Tool interface, not annotation registry

A new `SideEffects() SideEffect` method on the `Tool` interface returns one of `ReadOnly | Mutating | Destructive`. The MCP-spec transport prefixes destructive tool descriptions with `[destructive]`. Existing REST clients see no change.

*Rationale:* interface-level enforcement vs. an out-of-band annotation map ensures every new tool author is forced to think about side effects. Compiler-enforced. The 26 existing tools each get a one-line method.

### KTD-5: OAuth 2.1 with PKCE — minimal client storage

Three new endpoints: `GET /v1/oauth/authorize` (returns Claude to the consent UI), `POST /v1/oauth/token` (exchanges authorization code for access + refresh), `POST /v1/oauth/revoke` (refresh-token revocation). Authorization codes + refresh tokens stored in a new `oauth_tokens` SQLite table (TEXT id, user_id FK, kind ENUM, code_challenge, expires_at, revoked_at).

The MCP Connector flow is a thin layer over the existing JWT signing. The refresh-token rotation lives in this plan; broader OAuth-as-a-service (third-party client registration via DCR, scopes-as-permissions) is out of scope.

*Rationale:* Anthropic's Connector docs mandate OAuth 2.1 with PKCE for the public-internet flow. The minimum viable implementation requires authorize + token endpoints + refresh storage. We can skip Dynamic Client Registration (DCR) by hard-coding Claude as a known client.

### KTD-6: DeferLoading on the Tool interface

A new `DeferLoading() bool` method. `Visible` tools default to `false`; `Deep` tools default to `true`. The MCP `tools/list` response includes a `defer_loading: true` field on deferred tools so Claude lazy-loads them via `tool_search`.

*Rationale:* the existing two-tier `Visibility` system maps almost 1:1 to Anthropic's January 2026 `defer_loading` insight. Making it a separate method (vs. derived from Visibility) allows future tools to be Visible but defer-loaded (e.g., `section.inspect` is composite enough that we may want Claude to load it on demand).

### KTD-7: `tools/list_changed` via existing SSE broker

When (future) per-user filtering changes the visible catalog for a user, publish `{type:"mcp:tools:changed", tenant_id, user_id}` on a new channel `mcp:tools:<tenant_id>`. The Streamable HTTP transport subscribes per-session and translates to `notifications/tools/list_changed` JSON-RPC notifications.

Today there is no mutation source — registry is frozen post-boot. The wire is in place for future capability filtering.

*Rationale:* reuse the proven SSE broker (`internal/sse/broker.go`'s `MemoryBroker`); no new infrastructure. Future-proofs the connection for the capability filtering work flagged as out of scope.

---

## High-Level Technical Design

The new transport layer is a JSON-RPC dispatcher that wraps the existing `Registry`. Conceptual flow:

```
                          POST /mcp (Streamable HTTP)
                                  │
                                  ▼
         ┌─────────────── JSON-RPC dispatch ───────────────┐
         │  initialize       → return capabilities +       │
         │                     serverInfo.instructions     │
         │  tools/list       → Registry.ListVisible() +    │
         │                     Registry.ListDeferred()     │
         │                     wrapped in MCP-spec shape   │
         │  tools/call       → Registry.Invoke(name,args)  │
         │                     wrap Result → {content,     │
         │                     structuredContent, isError} │
         │  notifications    → SSE subscribe + translate   │
         └──────────────────────┬──────────────────────────┘
                                │
                                ▼
                        Registry (UNCHANGED)
                                │
                                ▼
                Tool.Invoke(ctx, Deps, args) (UNCHANGED)
```

The existing REST routes — `GET /v1/mcp/tools` and `POST /v1/mcp/invoke/{name}` — keep dispatching through the same Registry. The two transports share auth middleware (`requireAuth`) and result types; only the wire format diverges at the edge.

OAuth 2.1 sits in front:

```
Claude.ai
   │
   ├─ GET /v1/oauth/authorize?client_id=claude&redirect_uri=…&code_challenge=…
   │       → user consent in browser, redirect with authorization code
   │
   ├─ POST /v1/oauth/token (code + code_verifier)
   │       → {access_token, refresh_token, expires_in}
   │
   └─ POST /mcp (Authorization: Bearer <access_token>)
           → JSON-RPC dispatch (above)
```

*This sketch communicates intent; implementer should treat it as directional guidance, not a copy-paste specification. The actual Go code follows existing patterns in `internal/auth/` and `internal/mcp/handler.go`.*

---

## Implementation Units

### U1. Streamable HTTP transport scaffolding

**Goal:** New route `POST /mcp` speaking JSON-RPC. Initialize handshake works; tools/list returns the existing catalog in MCP-spec shape; tools/call dispatches to the existing Registry.

**Requirements:** R1, R2 (partial), R4

**Dependencies:** none

**Files:**
- `services/ds-service/internal/mcp/transport_mcp.go` (new)
- `services/ds-service/internal/mcp/transport_mcp_test.go` (new)
- `services/ds-service/internal/mcp/handler.go` (modified — add `RegisterMCPRoutes` alongside existing `RegisterRoutes`)
- `services/ds-service/cmd/server/main.go` (modified — wire new routes around line 1318)

**Approach:**
- Hand-roll JSON-RPC over a single `POST /mcp` endpoint. No new vendored SDK; the protocol is small.
- The dispatcher reads `method` from the request body, switches on `"initialize" | "tools/list" | "tools/call" | "notifications/initialized"`, returns JSON-RPC `{jsonrpc:"2.0", id, result}` or `{jsonrpc:"2.0", id, error}`.
- Tool-execution errors return `result: {content, isError: true}` — JSON-RPC `error` reserved for protocol errors only (unknown method, parse error, invalid params at the protocol level).
- Auth: same `requireAuth` middleware as the REST routes; same `ClaimsReader` closure.
- For the Streamable HTTP transport spec: support both the POST-only mode and the optional GET-upgrade-to-SSE mode for long-running notifications. Start with POST-only; SSE upgrade is added in U10.

**Patterns to follow:**
- Error classification in `handler.go::classifyError` (lines 191-207) — reuse the sentinel-error → status-code map, adapt to JSON-RPC error codes.
- The auth wiring in `cmd/server/main.go:1318-1325` — the new routes wire the same way.
- Tenant resolution via `resolveTenant` in `handler.go:171-187`.

**Test scenarios:**
- Happy: `initialize` returns `{capabilities: {tools: {listChanged: true}}, serverInfo: {name, version, instructions}}`.
- Happy: `tools/list` returns the 3 visible verbs plus the 23 deep tools with `defer_loading: true`.
- Happy: `tools/call` for `section.inspect` returns `{content: [{type:"text", text: "..."}], structuredContent: {...}, isError: false}`.
- Error path: `tools/call` with unknown tool name returns `result: {content: [{type:"text", text:"..."}], isError: true}`, NOT JSON-RPC `-32601`.
- Error path: malformed JSON body returns JSON-RPC `-32700` (parse error).
- Auth: missing `Authorization` header returns HTTP 401 before JSON-RPC dispatch even runs.
- Integration: end-to-end via real Registry + temp SQLite — assert the response body matches MCP-spec shape byte-for-byte for a representative tool.

**Verification:** A bare-bones Python MCP client (`mcp-python-sdk`) can connect to `https://localhost:8443/mcp`, complete the initialize handshake, list tools, and invoke `section.inspect` successfully.

---

### U2. Result → MCP envelope adapter

**Goal:** The new transport correctly wraps the existing `Result{Data, NextActions, SchemaHint}` into MCP `{content[], structuredContent, isError}`. The REST surface remains unchanged.

**Requirements:** R2, R4

**Dependencies:** U1

**Files:**
- `services/ds-service/internal/mcp/transport_mcp.go` (modified)
- `services/ds-service/internal/mcp/registry.go` (modified — add `IsError bool` field to `Result`)
- `services/ds-service/internal/mcp/transport_mcp_test.go` (modified)

**Approach:**
- Extend `Result` with `IsError bool` (defaults to false). Existing tools don't set it; the REST surface ignores it; the new transport wraps based on it.
- Adapter function `wrapMCPContent(res Result, callErr error) mcpToolResult`:
  - On error from Invoke → `{content: [{type:"text", text: callErr.Error()}], isError: true}`.
  - On `res.IsError == true` → `{content: [{type:"text", text: jsonStringify(res.Data)}], isError: true}`.
  - On success → `{content: [{type:"text", text: jsonStringify(res.Data)}], structuredContent: res.Data, isError: false}`.
  - When `res.NextActions` non-empty, append a second `content[1] = {type:"text", text: "Next actions: ..."}` for the LLM (the stdio bridge already does this; we replicate the affordance natively).
- `SchemaHint` is currently unused per the research — leave as-is, repurpose for future structured `outputSchema` work.

**Patterns to follow:**
- The stdio bridge's existing wrapping at `~/.claude/plugins/ind-suite/mcp-bridge/scripts/server.cjs:14168+` — we replicate its output shape natively so behavior between the local-stdio and remote-HTTP paths converges.

**Test scenarios:**
- `Result{Data: {...}}` → wraps to `{content, structuredContent, isError: false}`.
- `Result{Data: {error:"foo"}, IsError: true}` → wraps to `{content, isError: true}` — note structuredContent still set on isError per spec.
- Invoke returns `ErrToolNotFound` → JSON-RPC `-32601` (method not found), NOT a tool-error body.
- Invoke returns `ErrInvalidArgs` → JSON-RPC `-32602` (invalid params), NOT a tool-error body.
- Invoke returns generic error → `{content, isError: true}` tool-error body.
- `Result{Data, NextActions: [...]}` → `content[1]` carries the next-actions hint.

**Verification:** All 26 tools, invoked through the new transport with valid args, produce MCP-spec-shaped responses that a Claude Connector would accept.

---

### U3. Constitution doc + `initialize.serverInfo.instructions`

**Goal:** The initialize handshake returns a 300-500 word constitution covering slug grammar, the four-table data graph, lifecycle states, error catalogue, and common workflows. Claude reads this at session start.

**Requirements:** R3

**Dependencies:** U1

**Files:**
- `services/ds-service/internal/mcp/constitution.md` (new — markdown source, PM-editable in git)
- `services/ds-service/internal/mcp/constitution.go` (new — `//go:embed constitution.md` + version constant)
- `services/ds-service/internal/mcp/transport_mcp.go` (modified — initialize response carries `instructions`)

**Approach:**
- `constitution.md` covers, in order:
  1. **Slug grammar (KTD-6).** Regex `^[a-z0-9][a-z0-9-]*$/^[a-z0-9][a-z0-9-]*$`, max 80 chars per segment. Examples + counterexamples.
  2. **The four-table data graph.** `sub_product → sub_flow → prd → prd_tab → prd_state → prd_state_*`. Plus `projects → project_versions → flows`. Plus `flow_drd`, `drd_anchor`.
  3. **Lifecycle states** (`empty | proto-only | proto-wip | design-shipped`) and what transitions them.
  4. **PRD typed-stems model.** Why we store each event/criterion/copy-string as a typed row, not prose.
  5. **Anchor model.** How prototype clicks resolve to DRD blocks; the contract for prototype HTML.
  6. **Error catalogue.** Map of `Err*` sentinels to "what to do about it".
  7. **Common workflows** as numbered MCP-call sequences: "PM publishes a sub_flow", "Designer parses a DRD", "PM authors PRD states".
- `constitution.go` exposes a `Constitution() string` function + a `ConstitutionVersion` int constant (incremented on schema changes).
- Initialize response: `serverInfo.instructions = Constitution()`.

**Patterns to follow:**
- `docs/conventions/sub-product-slug.md` — that's the existing convention doc; the constitution synthesizes it + the others into one document.
- `//go:embed` usage exists in `services/ds-service/migrations/embed.go` for the migration SQL.

**Test scenarios:**
- `initialize` response body includes `serverInfo.instructions` and the string starts with the expected version marker.
- Constitution length is between 1500 and 3000 chars (spec doesn't mandate but Claude's context budget for serverInfo.instructions is bounded; we cap to keep it tight).
- `ConstitutionVersion` exposes as an integer monotonically incrementing on schema changes — write a test that asserts the current version matches a hard-coded value (forces explicit bumps).

**Verification:** Connect via `mcp-python-sdk`, call `initialize`, assert `result.serverInfo.instructions` contains the slug-grammar regex and the workflow grammar headings.

---

### U4. Tool interface extensions — `Title()`, `SideEffects()`, `DeferLoading()`

**Goal:** New methods on the `Tool` interface. All 26 existing tools gain one-line implementations. New `SideEffect` enum.

**Requirements:** R7, R8, R12

**Dependencies:** none (can run parallel to U1)

**Files:**
- `services/ds-service/internal/mcp/registry.go` (modified — add interface methods + SideEffect type)
- All 26 tool files: `tools_subflow.go`, `tools_drd.go`, `tools_prd.go`, `tools_section.go`, `tools_resolve.go`, `meta.go` (modified — add the three new methods to every tool struct)
- `services/ds-service/internal/mcp/registry_test.go` (modified — assert every tool has non-empty Title and a valid SideEffects)

**Approach:**
- New types in `registry.go`:
  ```go
  type SideEffect int
  const (
      ReadOnly    SideEffect = iota
      Mutating
      Destructive
  )
  ```
- Extended Tool interface:
  ```go
  type Tool interface {
      // existing
      Name() string
      Description() string
      InputSchema() json.RawMessage
      Visibility() ToolVisibility
      Invoke(ctx, deps, args) (Result, error)
      // new
      Title() string          // human-readable display, distinct from machine name
      SideEffects() SideEffect
      DeferLoading() bool     // default: Visible→false, Deep→true
  }
  ```
- Implementer pattern for each tool (~3 lines added per file):
  ```go
  func (drdReadTool) Title() string             { return "Read DRD Content" }
  func (drdReadTool) SideEffects() SideEffect   { return ReadOnly }
  func (drdReadTool) DeferLoading() bool        { return false } // Visible
  ```
- This is a 26-touchpoint sweep; mechanical change. No behavior change to Invoke.
- The MCP transport surfaces `SideEffects() == Destructive` as `[destructive]` prefix in the tool description sent to Claude.

**Patterns to follow:**
- The existing 5-method `Tool` interface in `registry.go:52-58` — same shape, three more methods.
- Test `TestRegistry_AllToolsHaveCompleteMetadata` enforces no tool ships with empty Title or unset SideEffects.

**Test scenarios:**
- All 26 tools implement all three new methods (compile-time enforced).
- Every Title is non-empty and ≤60 chars.
- Every SideEffect is one of the 3 enum values (no zero-value escape).
- DeferLoading defaults to `Visibility() == Deep` — assert this for at least 5 tools.
- Compile error if a future tool omits any of the three methods.

**Verification:** `go build` succeeds; `go vet ./...` clean; registry_test.go passes the metadata-completeness assertion.

---

### U5. Tool description sweep — when-to-use + when-NOT-to-use + per-param descriptions

**Goal:** Every tool's `Description()` carries a clear when-to-use boundary AND a when-NOT-to-use clause. Every `InputSchema()` property has its own `description`.

**Requirements:** R5, R6

**Dependencies:** U4

**Files:**
- All 26 tool files (sweep)
- `services/ds-service/internal/mcp/registry_test.go` (modified — add per-param description lint)

**Approach:**
- Each tool's `Description()` updated to follow the convention:
  > **One sentence** stating what the tool does. **Use when:** [trigger or scenario]. **Don't use when:** [contrasting tool that fits the case better]. [Optional: idempotency note.]
- Examples:
  - `drd.read`: "Read a sub_flow's DRD YDoc state. Use when starting a PM session and you need to know what's already written. Don't use when you need to mutate the DRD — use drd.append for snapshots, or write through the Hocuspocus collab path for live edits."
  - `drd.attach_prototype`: "Bind an HTTPS prototype URL to a sub_flow. Use when the PM published a prototype before the Figma design lands. Don't use when the Figma section is already bound (`design-shipped` lifecycle) — that takes precedence. Idempotent on the URL value."
- Every `InputSchema()` property gets a `description` field. Mechanical sweep of `tools_*.go`.
- Lint test: walks the registry, parses every InputSchema, asserts every leaf property has a non-empty `description` field.

**Patterns to follow:**
- The existing well-written description for `drd.attach_anchor` (tools_drd.go around line 205) — that's the bar.
- The lint pattern from `registry_test.go:156-181` — same shape, walking the JSON Schema tree.

**Test scenarios:**
- Description contains both "Use when" and "Don't use when" (string-search assertion).
- Description ≤150 words (per arxiv 2602.14878).
- Every property in every InputSchema has a description (recursive walk; enums excepted).
- The 1500-byte cold-catalog budget test will fail; bump to ~4000 bytes since we're adding when-not-to-use prose.

**Verification:** A new test, `TestAllToolsHaveBoundaryDescriptions`, passes for all 26 tools.

---

### U6. Promote `prd.author` op-dispatch to top-level tools

**Goal:** The 11 sub-ops behind `prd.author` (`get`, `upsert_tab`, `add_state`, `add_event`, `add_acceptance_criterion`, `add_edge_case`, `upsert_copy_string`, `add_a11y_note`, `attach_frame`, `detach_frame`, `export`) become first-class Visible tools. `prd.author` stays registered as a deprecated alias.

**Requirements:** R9

**Dependencies:** U4

**Files:**
- `services/ds-service/internal/mcp/meta.go` (modified — register the 11 new top-level tools; `prd.author` becomes a thin shim)
- `services/ds-service/internal/mcp/tools_prd.go` (modified — Visibility on the 11 internal tools flips Deep → Visible; titles + side effects added per U4)
- `services/ds-service/internal/mcp/registry_test.go` (modified — assert the 11 top-level tools register; assert prd.author still works as deprecated alias with a deprecation `NextAction`)

**Approach:**
- The 11 tools already exist as internal Deep tools (e.g., `prdAddStateTool{}`). They were registered as Deep purely to keep them out of the cold catalogue.
- Change `Visibility()` from `Deep` to `Visible` on all 11.
- `prdAuthorTool` (the meta-verb) keeps its existing op-dispatch behavior but adds a `nextActions` entry: `{tool: "prd.add_state", when: "prd.author is deprecated; call prd.add_state directly", deprecation: true}`.
- After one release (~30 days), `prd.author` returns `ErrDeprecatedTool` — out of scope for this plan.
- Cold catalog grows from 3 to 14 visible tools. Within the 30-50 threshold.

**Patterns to follow:**
- The existing `prdAuthorOpToTool` map in `meta.go:164-176` — that's the source-of-truth for the 11 sub-ops; the promotion just flips them visible.
- `nextActionsForPRDOp` in `meta.go:233-263` — emits the next-action hints; extend for deprecation note.

**Test scenarios:**
- All 11 new top-level tools appear in `Registry.ListVisible()`.
- `prd.add_state` invoked directly produces the same `Result.Data` as `prd.author{op:"add_state"}` for the same args.
- `prd.author` still works for existing callers, but its `Result.NextActions` includes a deprecation entry.
- Atlas's existing `payload.data` access path unaffected (no shape change inside the envelope).

**Verification:** `tools_prd_test.go` passes for both the new top-level tools and the legacy op-dispatch path.

---

### U7. Atlas + skill consumer migration to top-level prd.* tools

**Goal:** All Atlas fetch call sites and the `/ind-prd` skill stop using `prd.author{op:X}` and call the top-level tools directly. Backwards compatibility via U6 means we can ship them in either order, but updating consumers first reduces the deprecation surface earlier.

**Requirements:** R10

**Dependencies:** U6

**Files:**
- `app/api/prd/[subProduct]/[subFlow]/full/route.ts` (modified — proxies to `prd.get` instead of `prd.author{op:get}`)
- `~/.claude/plugins/ind-suite/skills/ind-prd.md` (modified — call top-level tools directly; the engineer working on ind-suite owns this change per the prior handoff doc)
- Any other Atlas component calling `prd.author` (audit `grep -rn 'prd.author' app/ lib/`)

**Approach:**
- Single-file changes in Atlas; mechanical translation:
  - `body: { op: "get", args: { sub_flow_slug } }` → `body: { sub_flow_slug }`
  - Endpoint: `/v1/mcp/invoke/prd.author` → `/v1/mcp/invoke/prd.get`
- The `/ind-prd` skill change is a separate PR in the ind-suite repo. This plan flags it as a cross-repo dependency and includes the migration spec in the verification.

**Patterns to follow:**
- Existing Atlas fetch call sites at the five locations enumerated in the research digest — same shape, different endpoint name + flatter body.

**Test scenarios:**
- Atlas PRD tab loads with the new endpoint; existing Vitest tests in `lib/atlas/` continue to pass.
- After this lands, `grep -rn 'prd.author' app/ lib/` returns zero results.
- Manual smoke: open `/atlas?project=indian-stocks&leaf=unified-watchlist-screener-indstocks`, switch to PRD tab, verify content loads.

**Verification:** Atlas browser test + manual confirmation that PRD tab still loads on the existing Unified Watchlist Screener leaf.

---

### U8. OAuth 2.1 with PKCE for the Connector flow

**Goal:** Three new endpoints — `GET /v1/oauth/authorize`, `POST /v1/oauth/token`, `POST /v1/oauth/revoke` — that let Claude.ai register and authenticate users via the standard OAuth 2.1 + PKCE flow. Refresh-token storage in SQLite.

**Requirements:** R11

**Dependencies:** U1

**Files:**
- `services/ds-service/migrations/0042_oauth_tokens.up.sql` (new — refresh tokens + authorization codes table)
- `services/ds-service/internal/auth/oauth.go` (new — authorization code mint, token exchange, refresh rotation)
- `services/ds-service/internal/auth/oauth_test.go` (new)
- `services/ds-service/cmd/server/main.go` (modified — register OAuth routes around line 1318; add config for client_id allowlist)
- `services/ds-service/internal/mcp/transport_mcp.go` (modified — validate access tokens with the OAuth-issued kind)

**Approach:**
- SQLite table `oauth_tokens(id PRIMARY KEY, user_id, kind ENUM('authorization_code','refresh_token'), code_challenge, code_challenge_method, client_id, scope, redirect_uri, expires_at, revoked_at, created_at)`.
- `GET /v1/oauth/authorize`: requires existing user session (the JWT from `/v1/auth/login`). Renders a consent prompt; on accept, mints an authorization code with PKCE challenge stored, redirects to `redirect_uri?code=...&state=...`.
- `POST /v1/oauth/token`: exchanges `{code, code_verifier, client_id, redirect_uri}` for `{access_token, refresh_token, expires_in:3600, token_type:"Bearer"}`. Access token is a JWT (reuse `auth.MintAccessToken`, but with a shorter `1h` TTL for OAuth-minted tokens). Refresh token is a 256-bit random string stored hashed.
- `POST /v1/oauth/revoke`: marks a refresh token revoked.
- Token rotation: every refresh use issues a new refresh token and revokes the old one (PKCE best practice).
- Client allowlist: env var `OAUTH_ALLOWED_CLIENTS=claude.ai,...`. For the initial Connector launch, only `claude.ai` is whitelisted. DCR (Dynamic Client Registration) is out of scope.
- The MCP transport's auth middleware accepts BOTH the existing long-lived `/v1/auth/login` JWTs AND OAuth-minted JWTs — same Ed25519 signing key, different `kind` claim. No client-side change required.

**Patterns to follow:**
- The existing JWT minting in `internal/auth/auth.go` (Ed25519, `Claims` struct, `MintAccessToken`).
- The JTI revocation cache pattern in `cmd/server/main.go:1515` — extend with refresh-token revocation.
- The migration pattern in `services/ds-service/migrations/0041_drd_anchor.up.sql`.

**Test scenarios:**
- Happy path: authorize → token → access protected endpoint → refresh → access again.
- PKCE: invalid `code_verifier` returns `invalid_grant`.
- Replay attack: reusing an authorization code returns `invalid_grant`.
- Expired code: code expires after 60s.
- Refresh rotation: the old refresh token is revoked after use; reusing it returns `invalid_grant`.
- Client allowlist: unknown `client_id` returns `invalid_client`.
- Cross-tenant: a refresh token for tenant A cannot be exchanged into an access token for tenant B.
- Integration: a full Connector flow using `mcp-python-sdk` succeeds against the running ds-service.

**Verification:** Claude.ai's "Add custom connector" flow completes against `https://indmoney-ds.fly.dev/mcp` — user clicks Connect, authorizes via OAuth, lands back in Claude, can call tools.

---

### U9. `defer_loading` markers in tools/list output

**Goal:** The MCP `tools/list` response surfaces the `DeferLoading()` annotation so Claude (Sonnet 4.5+) lazy-loads deferred tools via `tool_search`.

**Requirements:** R12

**Dependencies:** U4

**Files:**
- `services/ds-service/internal/mcp/transport_mcp.go` (modified — include `defer_loading` field in tools/list response)
- `services/ds-service/internal/mcp/transport_mcp_test.go` (modified)

**Approach:**
- In `tools/list` handler, each tool descriptor includes `defer_loading: bool`.
- The Anthropic MCP client spec describes this as an optional descriptor field. Including it on every tool (true or false) is more explicit than omitting it.
- For models that don't support `defer_loading` (Sonnet 4.0 and older), Anthropic ignores the field — backwards compatible.

**Patterns to follow:**
- The `catalogEntry` struct in `handler.go:79-101` — same shape, plus the new field.

**Test scenarios:**
- `tools/list` response includes `defer_loading: false` for all 3 visible meta-verbs.
- `tools/list` response includes `defer_loading: true` for all Deep tools.
- After U6 promotes the 11 prd.* tools to Visible, they all carry `defer_loading: false`.

**Verification:** Inspect raw `tools/list` response; assert `defer_loading` field present on every tool.

---

### U10. `tools/list_changed` SSE notification capability

**Goal:** The MCP transport advertises `capabilities.tools.listChanged: true` and, when implemented, publishes `notifications/tools/list_changed` over the SSE upgrade channel of Streamable HTTP. Today the registry is static post-boot, so the publisher is a stub; the infrastructure is in place for future per-user capability filtering.

**Requirements:** R13

**Dependencies:** U1

**Files:**
- `services/ds-service/internal/mcp/transport_mcp.go` (modified — add SSE upgrade handler at `GET /mcp` for the Streamable HTTP optional path; advertise capability)
- `services/ds-service/internal/mcp/transport_mcp_test.go` (modified)

**Approach:**
- The Streamable HTTP spec allows the server to accept `GET /mcp` as an upgrade to SSE for receiving notifications.
- Subscribe to a new SSE channel `mcp:tools:<tenant_id>` via the existing `MemoryBroker`. When a future code path publishes `{type:"mcp:tools:changed"}` on that channel, the SSE upgrade handler translates to a JSON-RPC `notifications/tools/list_changed` event.
- No publisher source exists today — `Registry` is frozen post-boot. We add the wire, leave the emit site for future.
- `initialize` response capabilities: `{tools: {listChanged: true}}`.

**Patterns to follow:**
- The existing SSE broker pattern in `internal/sse/broker.go` — subscribe per-session, drop on full, heartbeat loop.
- The channel-name convention `<topic>:<tenant_id>` (today: `inbox:<tenant_id>`).

**Test scenarios:**
- `initialize` advertises `capabilities.tools.listChanged: true`.
- `GET /mcp` with `Accept: text/event-stream` upgrades to SSE.
- Manual publish to the new channel via test broker → SSE client receives the JSON-RPC notification.

**Verification:** A test that publishes a tools-changed event via `MemoryBroker.Publish` results in the connected SSE client receiving the JSON-RPC notification within 1s.

---

### U11. HTTP-layer integration tests for the new transport

**Goal:** End-to-end tests that exercise the full HTTP stack — JSON-RPC parsing, auth middleware, tenant resolution, Registry dispatch, MCP envelope wrapping, error classification. Currently no such tests exist for the MCP layer.

**Requirements:** R14

**Dependencies:** U1, U2, U3

**Files:**
- `services/ds-service/internal/mcp/transport_mcp_integration_test.go` (new)
- `services/ds-service/internal/mcp/oauth_integration_test.go` (new)

**Approach:**
- Spin up a real `httptest.NewServer` wired with the full middleware stack, including JWT auth.
- Use a real (temp SQLite) DB; mint a real JWT via `auth.MintAccessToken` for the test user.
- Mirror the existing test patterns in `internal/projects/server_test.go::newTestServer` — the closest pattern in the codebase.

**Patterns to follow:**
- `internal/projects/server_test.go` lines 22-54 — `newTestServer` pattern. Adapt for the MCP transport.

**Test scenarios (a representative subset):**
- Initialize handshake returns expected capabilities + serverInfo.
- tools/list returns 14 visible (3 meta + 11 promoted prd) + 23-11=12 deep tools, all with `defer_loading` flags correctly set.
- tools/call `section.inspect` with valid args returns MCP-shape body with structuredContent populated.
- tools/call with missing auth returns 401 before JSON-RPC dispatch.
- tools/call with cross-tenant args (claim says tenantA, slug belongs to tenantB) returns isError:true.
- OAuth happy path: authorize → token → access → call → refresh → call again.
- Constitution version assertion: `serverInfo.instructions` contains the current ConstitutionVersion marker.

**Execution note:** Write integration tests before the units they cover where possible — `transport_mcp_integration_test.go` can be drafted alongside U1/U2/U3 implementation as a test-first discipline.

**Verification:** `go test ./internal/mcp/...` passes with the new integration suite; coverage report shows the HTTP transport at >80%.

---

## System-Wide Impact

| Surface | Impact | Mitigation |
|---|---|---|
| **Atlas frontend** (5 fetch call sites) | One PR migrates `prd.author` → top-level `prd.*` tools. No envelope shape change. | U6 keeps `prd.author` working as a deprecated alias; U7 ships consumer updates after U6 lands. |
| **Local stdio bridge** (`~/.claude/plugins/ind-suite/mcp-bridge/scripts/server.cjs`) | No required change. Bridge continues consuming the REST surface. Optional cleanup: remove the markdown-wrapping of `payload.data` once the bridge can talk to the new MCP transport directly. | None — bridge survives unchanged. |
| **`/ind-prd` skill** in ind-suite repo | Must migrate from `prd.author{op:X}` to top-level tools. Cross-repo change. | Handoff doc to the skills engineer (covered in earlier conversation); `prd.author` stays alive for 30 days post-U6 ship. |
| **Claude Connectors** | New capability — Claude.ai users can install the connector and call any Visible tool via Anthropic's standard flow. | This IS the goal. U8 (OAuth) is the gate. |
| **JWT auth infrastructure** | Adds OAuth-minted tokens alongside existing `/v1/auth/login` JWTs. Same signing key, different `kind` claim. | Both kinds verify identically; only the issuance flow differs. |
| **SQLite DB** | New `oauth_tokens` table (migration 0042). Forward-only; no data migration. | Migration follows existing patterns; tests in oauth_test.go cover the table. |

---

## Risks & Mitigation

| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| The Streamable HTTP spec evolves between plan-write and ship (Anthropic has moved the beta header twice in 2025) | M | M | Pin to `mcp-client-2025-11-20`. If a new beta lands during implementation, add a small adapter — protocol shape is stable enough that the upgrade is mechanical. |
| Claude Connector rejects our OAuth flow because of an undocumented constraint | M | H | Test against `mcp-python-sdk` first (which mirrors Claude's auth expectations), then test the actual Claude.ai Connector flow before declaring U8 done. |
| Promoting `prd.author` sub-ops to top-level breaks an Atlas component we didn't grep | L | M | U7 includes a `grep -rn 'prd.author'` zero-result assertion; CI-style enforcement in the migration PR. |
| Constitution doc drifts from actual schema as the system evolves | H | L | `ConstitutionVersion` constant forces explicit bumps. Add a build-time check that fails CI if `migrations/*.sql` changed without `ConstitutionVersion` incrementing — out of scope but worth a follow-up. |
| OAuth client allowlist is too restrictive | L | L | Env-var driven; can add new clients without a code change. |
| The 1500-byte cold-catalog test (`registry_test.go:156-181`) is a tripwire — it fires when we add per-param descriptions and when-not-to-use prose | H | L | U5 bumps the limit to ~4000 bytes. The point of the test (catalog stays bounded) survives; the threshold updates. |

---

## Verification

Plan-level done criteria, beyond per-unit:

1. **Bare-bones Python client succeeds end-to-end.** A test using `mcp-python-sdk` connects to `https://localhost:8443/mcp`, completes initialize, lists tools, invokes `section.inspect` with the seeded sub_flow, gets back a structuredContent block. This is the most reliable proxy for "Claude Connector will work."
2. **Claude.ai Custom Connector connects successfully.** Manual smoke: in Claude.ai → Settings → Connectors → Add custom → paste `https://indmoney-ds.fly.dev/mcp` → complete OAuth flow → tools appear in Claude → invoke one tool via chat.
3. **All existing tests pass.** `go test ./...` clean. `npx vitest run` clean. `npx tsc --noEmit` clean.
4. **No regressions on Atlas.** Open the Unified Watchlist Screener leaf in `/atlas`, verify DRD + PRD + prototype + chips all render exactly as today.
5. **`grep -rn 'prd.author' app/ lib/` returns zero results** after U7 ships.

---

## Future Considerations

These are not in scope but are worth flagging for the engineer:

- **Per-user capability filtering** — once OAuth scopes land, `Registry.ListVisible(claims)` can filter by `claims.Role`. A `viewer` role might see only read-only tools.
- **DCR (Dynamic Client Registration)** — required if we want to list on Anthropic's public Connector marketplace. Out of scope here; the current allowlist approach handles internal use.
- **Prompts and Resources primitives** — the spec defines `prompts/list` and `resources/list` alongside `tools/list`. Useful for shipping pre-canned PM workflow snippets ("Onboard a new sub_flow", "Run a coverage audit"). Worth a follow-up plan.
- **Streaming progress** — if `prd.export` grows to include async work, add `notifications/progress` per the spec. The infrastructure from U10 makes this incremental.
- **Constitution doc auto-build from schema** — long term, the constitution shouldn't be hand-edited; it should be generated from `migrations/*.sql` + `internal/projects/*.go` doc strings via a `go generate` step.

---

**Plan ready at:** `docs/plans/2026-05-18-002-feat-mcp-connector-compliance-plan.md`
