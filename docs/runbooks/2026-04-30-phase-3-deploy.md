---
date: 2026-04-30
phase: 3
type: runbook
---

# Phase 3 deploy runbook

Operational steps for deploying `feat/projects-flow-atlas-phase-3` to a
new or existing environment.

## Prerequisites

| Component | Required | Notes |
|---|---|---|
| Go ≥ 1.22 | yes | ds-service binaries |
| Node ≥ 22 | yes | docs-site build |
| SQLite (modernc.org/sqlite, no CGO) | bundled | via Go module |
| `basisu` CLI on PATH | optional | KTX2 transcoding (U2 — deferred to Phase 3.5). Without it, server logs warning + serves PNG only. |
| Figma plugin distribution | optional | only needed when designers will run Projects mode |

## First boot

```bash
# 1. Migrations (auto-applied on server boot, but you can dry-run with):
cd services/ds-service
go run ./cmd/server &  # applies 0001 → 0005 in order; idempotent

# 2. Verify Welcome project landed (Phase 3 U12):
sqlite3 services/ds-service/data/ds.db \
  "SELECT slug, name FROM projects WHERE tenant_id='system';"
# Expected: welcome | Welcome to Projects · Flow Atlas

# 3. Provision the first super_admin:
curl -X POST http://localhost:7475/v1/admin/bootstrap \
  -H "X-Bootstrap-Token: $BOOTSTRAP_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"email":"admin@example.com","password":"…"}'

# 4. Build + start the docs site:
npm install
npm run build
npm run start
```

## Environment variables

### ds-service

| Var | Default | Notes |
|---|---|---|
| `DS_AUDIT_WORKERS` | `6` | Pool size; clamped [1, 32]. Phase 2 U7. |
| `DS_AUDIT_LEGACY_SIDECARS` | unset | Set to `"1"` for one-release rollback to lib/audit/*.json writes. **Phase 3 should leave unset; remove the flag entirely once verified.** |
| `DS_SYSTEM_TENANT_ID` | `system` | Used by cmd/migrate-sidecars + the Welcome seed migration. |
| `DS_SYSTEM_USER_ID` | `system` | Same. |
| `DS_BASIS_CLI_PATH` | `basisu` | Override Basis CLI location. Phase 3 U2 (deferred). |
| `DS_AUDIT_BY_SLUG_INCLUDE_SYSTEM` | `1` | Phase 2 U10: when on, /v1/audit/by-slug falls back to system tenant for backfilled sidecar rows. |
| `BOOTSTRAP_TOKEN` | unset | Required for `/v1/admin/bootstrap`; set this at first launch then unset. |

### docs-site (Next.js)

| Var | Default | Notes |
|---|---|---|
| `READ_FROM_SIDECAR` | unset | Phase 2 U10 rollback flag. Set to `"1"` to fall back to build-time JSON imports. **Phase 3 should leave unset.** |
| `DS_TOUR_DEFAULT_SEEN` | unset | Test-only — skips Phase 3 U11 tour mounting in E2E runs. |

## Smoke checks

After deploy, verify the cold-start journey end-to-end:

```bash
# 1. /projects index loads + Welcome project visible.
curl -H "Authorization: Bearer $TOKEN" \
     http://localhost:7475/v1/projects | jq '.projects[0].slug'
# Expected: "welcome"

# 2. /projects/welcome project view loads.
curl -H "Authorization: Bearer $TOKEN" \
     http://localhost:7475/v1/projects/welcome | jq '.project.name'
# Expected: "Welcome to Projects · Flow Atlas"

# 3. Welcome violations land.
curl -H "Authorization: Bearer $TOKEN" \
     http://localhost:7475/v1/projects/welcome/violations | jq '.count'
# Expected: 2

# 4. /onboarding renders (5 personas).
curl -s http://localhost:7474/onboarding | grep -c "data-persona-slug"
# Expected: 5
```

Browser smoke (open in private/incognito to clear localStorage):

1. `/projects` → Welcome project hero card visible.
2. `/projects/welcome` → atlas blooms in over 800ms; toolbar shows
   theme/persona/version selectors.
3. First-time visit: Shepherd tour mounts after 1 frame; 4 steps
   walk persona → theme → JSON tab → Violations tab.
4. Click Violations tab → 2 demo violations grouped by severity.
5. `/onboarding` → 5 persona sections + quick-picker chips at top.
6. `/projects/welcome?read_only_preview=1` → top-level read-only
   banner above toolbar; DRD editor disabled.
7. `/projects/welcome?reset-tour=1` → tour remounts.

## Known issues / open items

- **Phase 1 PR not yet open.** Branch
  `feat/projects-flow-atlas-phase-1` was pushed to origin (commit
  `ea64c2e`); the gh CLI was sandbox-blocked during the Phase 1
  finishing fork. Open the PR via GitHub UI manually + flip Phase 1
  plan frontmatter `status: completed` once merged.

- **U2 KTX2 + U3 InstancedMesh + LOD deferred from Phase 3.** Atlas
  performance work — biggest payload in the original Phase 3 plan.
  Ship in Phase 3.5 once dogfood confirms the bloom + camera-fit
  baseline is stable.

- **Variable resolver Go-side mirror still owed.** Phase 2 prod-wire
  loaders all return identical canonical_trees per mode of the same
  screen. Theme parity catches structural drift today; the headline
  AE-2 case ("hand-painted dark mode fill resolved against the
  Variable") needs the resolver. Track as a Phase 4 polish unit.

- **SSE fanout_started/progress/complete events** are not emitted
  by Phase 2 U8's fan-out endpoint today. The endpoint enqueues +
  returns 202 with `fanout_id`; per-job progress arrives via the
  existing `audit_complete` events filtered by metadata.fanout_id.
  A dedicated channel ships with Phase 7's admin UI.

- **Per-screen audit progress** instead of per-rule (Phase 3 U6
  shipped per-rule; Phase 4+ may extend the RuleRunner interface
  with a screen-completion callback).
