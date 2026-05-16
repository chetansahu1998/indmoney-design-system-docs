# Local End-to-End Test Plan — 2026-05-16

**Purpose.** Verify everything shipped in the 2026-05-13 → 2026-05-16 push works end-to-end on a single laptop, from "fresh checkout" to "designer pushes a flow via plugin and sees it rendered in Atlas." Plus a production-readiness assessment so you know what's NOT yet ready before deploying to Fly.

**Audience.** You, running this manually. Every step has a verification checkpoint. SQL queries assume `sqlite3 services/ds-service/data/ds.db`.

---

## 1. What We Shipped This Session

Three workstreams landed across two branches. Production deployment will require unifying them.

### 1A. Organism Pattern Detection (`feat/organism-pattern-detection`)
Detect hand-built component patterns vs published DS organisms, surface for consolidation or promotion.

- **Migration 0024**: `detected_organism_match` + `promotion_candidate` tables
- **Migration 0026**: `organism_fork_mark` (designer-flagged intentional forks)
- **Pipeline Stage 6.7**: walks every screen's canonical_tree, classifies each FRAME-shaped subtree against the published manifest
- **Admin dashboard**: `/atlas/organisms` (adoption table + ranked promotion candidates)
- **Plugin "Check selection against DS"**: real-time verdict card with diff descriptors, "Mark as fork" + "Open published in Figma" actions
- **Audit-driven bias fixes** (3 commits past branch fork):
  - **#1 Walker generalization** — FRAME-only → FRAME/GROUP/INSTANCE/COMPONENT (unlocked position-card and section-wrapped patterns)
  - **#3 Loose-key clustering** — promotion candidates GROUP BY `atom_signature_json` instead of `fingerprint_hash` (unlocks cross-product clusters where same atom-set lives in different slot topologies)
  - **#5 componentId→slug index** — atom_slug resolution prefers manifest's `variant_id → slug` map over per-file display-name heuristic (cross-file convergence)

### 1B. Figma DB Inventory + Section Subtree Blob (`feat/figma-db`)
Mirror every Figma file in seeded teams; cache section subtrees for offline planner reads.

- **Migration 0025**: `figma_team_seed`, `figma_team`, `figma_project`, `figma_file`, `figma_page`, `figma_section`, `figma_inventory_run`
- **Migration 0027** (now superseded): `figma_node` row-per-Figma-node deep-tree mirror
- **Migration 0030** (just shipped): `figma_section.subtree_json_zstd` + `subtree_node_count`
- **Migration 0031** (just shipped): `DROP TABLE figma_node` — superseded by per-section blob
- **Poller** (`internal/figma/inventory/poller.go`): runs every 5 min, two-tier sync (Tier-A list-mode every cycle, Tier-B deep-fetch bounded to 30 files/cycle)
- **Admin UI** `/atlas/figma-inventory`: TeamBar + FilesTable + RunsStrip
- **CLI** `cmd/figma-inventory-sync`: one-shot seed-and-crawl
- **Storage reduction**: 26.89M figma_node rows × 5 indexes = 13 GB → per-section blob = ~50 MB total

### 1C. Autosync Bridge (`feat/figma-db`)
Convert poller signals into automatic audit-pipeline exports when sections' content changes.

- **Migration 0028**: `figma_auto_sync_state`, `figma_project_mapping`, content_hash/position_hash columns on figma_page + figma_section
- **Migration 0029**: figma_file owner allowlist + figma_section override columns
- **Page classifier** (`figma_page_classifier.go`): picks Final + version pages per file
- **Section parser** (`figma_section_parser.go`): splits `Sub-product/Sub-flow`, classifier CLI for unsupported names
- **Hash function** (`figma_hash.go`): content_hash (subtree-shape) + position_hash (own bbox) per page + section
- **AutoSyncPlanner.Plan()** (read-only): returns `FilePlan` with per-section action (full_export / cheap_update / skip / quarantine)
- **AutoSyncPlanner.Execute()**: full_export builds synthetic ExportRequest, calls `RunExport()` in-process; cheap_update bumps `flows.name` without re-running the pipeline
- **`runExport` extracted from `HandleExport`** — same business logic, different callers (HTTP handler vs in-process autosync)
- **CLIs**: `cmd/figma-autosync-dryrun` (planner only), `cmd/figma-autosync-classify` (section-name overrides)
- **Admin HTTP**: `POST /v1/admin/figma-autosync/execute`
- **Webhook receiver**: **NOT IMPLEMENTED** — webhook delivery is a planned trigger but no `server_figma_webhook.go` exists; manual `POST /v1/admin/figma-inventory/sync` or the 5-minute poll tick is the only entry today

---

## 2. System Architecture (one screen)

```
┌─────────────────┐    ┌──────────────────┐    ┌─────────────────┐
│  Figma Plugin   │    │  Next.js Atlas   │    │ Designer browser│
│ (figma-plugin/) │───▶│ (app/atlas/*)    │◀───│  localhost:3001 │
│  in Figma app   │    │  port 3001       │    └─────────────────┘
└────────┬────────┘    └──────────┬───────┘
         │ Authorization: Bearer  │ Authorization: Bearer
         │ JSON over HTTPS        │ (or NEXT_PUBLIC_AUTH_BYPASS)
         ▼                        ▼
┌─────────────────────────────────────────────────────────────────┐
│ ds-service (Go)  cmd/server/main.go   :8080 (HTTP) :8443 (TLS)  │
│                                                                  │
│  ┌──────────────────┐  ┌──────────────────┐  ┌──────────────┐  │
│  │ HandleExport     │  │ Inventory Poller │  │ Audit Worker │  │
│  │ → RunExport      │  │ (5min loop, two- │  │ Pool (Stages │  │
│  │ → spawn pipeline │  │  tier sync)      │  │ 7,8,9 async) │  │
│  └────────┬─────────┘  └────────┬─────────┘  └──────┬───────┘  │
│           │                     │                   │           │
│           ▼                     ▼                   ▼           │
│  ┌──────────────────────────────────────────────────────────┐  │
│  │ RunFastPreview (Stages 1-9): figma fetch, PNG render,    │  │
│  │ canonical_tree, organism detect (6.7), cluster prerender,│  │
│  │ graph rebuild, SSE publish                                │  │
│  └────────────────────┬──────────────────────────────────────┘  │
│                       │                                          │
│  ┌────────────────────▼──────────────────────────────────────┐  │
│  │ SQLite (data/ds.db) + filesystem (data/screens/*.png +    │  │
│  │ data/assets/*) + manifest (public/icons/glyph/manifest)   │  │
│  └────────────────────────────────────────────────────────────┘  │
│                                                                   │
│  ┌────────────────────────────────────────────────────────────┐  │
│  │ AutoSyncPlanner.Plan + .Execute (in-process, called from   │  │
│  │ poller or POST /v1/admin/figma-autosync/execute) — feeds   │  │
│  │ ExportRequest into RunExport without HTTP round-trip       │  │
│  └────────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────┘
         ▲                                              ▲
         │ Figma REST (Tier-1 12 RPM, Tier-2 40 RPM)    │ webhook
         │                                              │  (NOT WIRED)
┌────────┴───────────┐                          ┌───────┴──────┐
│ api.figma.com      │                          │ Figma cloud  │
└────────────────────┘                          └──────────────┘
```

**Ports.** ds-service: `:8080` (HTTP, primary). `:8443` (TLS, optional via `DS_TLS_CERT`/`DS_TLS_KEY`). Atlas Next.js: `:3001`.

**DB.** SQLite at `services/ds-service/data/ds.db` (singleton; not pooled). Migrations 0001–0031 land on first start. Currently at 836 KB after this session's wipe + VACUUM.

**Process model.** One ds-service process hosts: HTTP listener, inventory poller goroutine, audit worker pool, graph rebuild pool, SSE hub. Frontend runs as separate `next dev`.

---

## 3. Production-Readiness Assessment

### ✅ Ready for production
- Tenant + user bootstrap (`cmd/bootstrap-tenant`)
- JWT auth + super_admin gating
- Audit pipeline (Stages 1–9) — proven by months of plugin-driven exports
- Inventory poller (5-min loop, two-tier sync, per-PAT rate limiting)
- Section subtree blob storage (replaces figma_node, 99.85% smaller; new this session — DB-tested but not yet pollwd on real Figma data)
- Organism detection Stage 6.7 — shipped; running for every export
- Atlas frontend rendering (canonical_tree → React via leafcanvas-v2/nodeToHTML)
- Figma plugin "Check selection against DS"

### ⚠️ Ready but needs operational setup
- **AutoSync executor** — `POST /v1/admin/figma-autosync/execute` is wired; no scheduler beyond manual trigger. Add a cron or wire it into the poller's completion path to fire automatically.
- **Figma PAT per tenant** — admin endpoint exists (`POST /v1/admin/figma-token`); env `FIGMA_PAT` fallback works for single-tenant dev. Multi-tenant prod needs each tenant to upload theirs.
- **Project mapping** — `figma_project_mapping` is required for autosync eligibility but has **no admin UI**. Admin must `INSERT INTO figma_project_mapping ...` manually via SQL or PATCH the DB. Future admin endpoint should land before opening this to non-engineering admins.

### 🔴 Not yet ready
- **Figma webhook receiver** — planned but unimplemented. The autosync plan (`docs/plans/2026-05-14-001-*`) calls for `POST /v1/webhooks/figma/file-update` to clear `figma_file.deep_synced_at` so the poller refreshes; webhook signature verification not coded. Today the system relies on the 5-min poll tick. **Acceptable for v1** but reduces freshness to ~5–10 min vs the ~30 sec a webhook would give.
- **Branch unification** — organism Bias #1/#3/#5 fixes (commits 498c10b, d4342fb, 4bb3385) live on `feat/organism-pattern-detection`, NOT on `feat/figma-db`. Without merging them, the production deploy ships the *un-generalized* walker and *strict-identity* promotion clustering. **Section 4 Phase 0 below covers the merge.**
- **CI** — no CI workflow runs the test suite on push. Manual `go test ./...` before deploy.

### 🟡 Performance unknowns (this test plan answers)
- DB size after one full poll cycle on the real INDmoney Figma team
- Memory ceiling during deep-tree fetch + section subtree encoding for a 500-file team
- End-to-end latency from `POST /v1/projects/export` → SSE `project_view_ready` for a 12-frame flow
- Atlas first-paint time for a flow with 12 screens × 8000 canonical_tree nodes

---

## 4. Local Testing Plan

Sequential phases. Each one verifies the previous one's output. **Stop and investigate** if any verification checkpoint fails — don't skip.

### Phase 0: Branch unification (one-time)

The organism bias fixes are not on `feat/figma-db`. Merge them before testing the integrated system.

```bash
# from repo root, on feat/figma-db with clean tree
git checkout feat/figma-db
git cherry-pick 498c10b d4342fb 4bb3385
```

**Expected.** Three clean cherry-picks; no conflicts (verified: files don't overlap with figma-db's changes).

**If conflicts arise:** abort with `git cherry-pick --abort` and report. The 3 commits touch only `pipeline_organism_match.go` + `pipeline_promotion_candidates.go` + their test files; no autosync work touches them.

**Verify.**
```bash
go test ./internal/projects/ -run "TestWalker|TestRebuildPromotion|TestResolveInstanceSlug" -count=1
```
Expect 30+ tests, all green.

### Phase 1: Environment prep

```bash
# 1. confirm .env.local exists (copy from .env.local.example if not)
ls -la .env.local

# 2. confirm these are set (open in your editor; never paste into chat)
grep -E '^(JWT_SIGNING_KEY|JWT_PUBLIC_KEY|ENCRYPTION_KEY|BOOTSTRAP_TOKEN|FIGMA_PAT)=' .env.local
```

If any are missing:
- `JWT_SIGNING_KEY` / `JWT_PUBLIC_KEY` — generate via `openssl genpkey -algorithm ed25519` then base64-encode. Or copy from prior `.env.local` if you have one.
- `ENCRYPTION_KEY` — `openssl rand -base64 32`
- `BOOTSTRAP_TOKEN` — `openssl rand -hex 32`
- `FIGMA_PAT` — generate at https://www.figma.com/developers/api#access-tokens

```bash
# 3. add the dev bypass for frictionless testing
echo 'DEV_AUTH_BYPASS=1' >> .env.local
echo 'DEV_AUTH_BYPASS_TENANT=e090530f-2698-489d-934a-c821cb925c8a' >> .env.local
echo 'NEXT_PUBLIC_AUTH_BYPASS=1' >> .env.local
echo 'NEXT_PUBLIC_DS_SERVICE_URL=http://localhost:8080' >> .env.local
```

**Verification checkpoint.** `cat .env.local | grep -c '='` returns ≥ 8.

### Phase 2: Bootstrap tenant + users

```bash
set -a && source .env.local && set +a

cd services/ds-service
go run ./cmd/bootstrap-tenant --db data/ds.db
cd -
```

**Expected output.** Tenant `indmoney` (id `e090530f-2698-489d-934a-c821cb925c8a`), 9 designer users, FIGMA_PAT encrypted into `figma_tokens`. Idempotent — safe to re-run.

**Verification checkpoint.**
```sql
SELECT (SELECT COUNT(*) FROM tenants) AS tenants,
       (SELECT COUNT(*) FROM users) AS users,
       (SELECT COUNT(*) FROM tenant_users) AS memberships,
       (SELECT COUNT(*) FROM figma_tokens) AS pats;
```
Expect: `1 | 11 | 10 | 1` (includes the seeded `system@indmoney.local` user + the 9 designers + your bootstrap admin if you ran the `/v1/admin/bootstrap` step).

If you want a super_admin user for the frontend login (instead of dev-bypass):
```bash
sqlite3 services/ds-service/data/ds.db \
  "UPDATE users SET role='super_admin' WHERE email='YOUR_EMAIL';"
```

### Phase 3: Start services

```bash
# Terminal A — ds-service
cd services/ds-service
set -a && source ../../.env.local && set +a
go run ./cmd/server
```

Expected log lines:
- `db ready` with migration count up to **31** (`schema_migrations` rows)
- `WARN DEV_AUTH_BYPASS=1 — JWT verification SKIPPED` (intentional)
- `ds-service listening` on `:8080`
- `inventory poller started` (5-min interval, 30-sec first-tick delay)
- `audit worker pool started`

**Verification checkpoint.**
```bash
curl -s http://localhost:8080/__health | jq
```
Expect `{"ok": true, "migrations": [1,2,...,31], ...}`.

```bash
# Terminal B — Next.js frontend
npm run dev
```

Expected: `ready - started server on 0.0.0.0:3001`. Open http://localhost:3001/atlas in browser.

**Verification checkpoint.** Atlas brain-graph loads without auth prompt. Top-right shows synthetic dev user. Admin tabs visible (Dashboard, Rules, Personas, Figma inventory, Organisms, Figma blocklist).

### Phase 4: Seed Figma team + first poll cycle

The 9 designer users are seeded with `tenant_admin` role on the indmoney tenant. The admin endpoint for adding a Figma team is wrapped in `requireFigmaInventoryAdminTenant` which trusts tenant_admin role.

```bash
# Use a real INDmoney Figma team ID + name. The one used in the wipe-and-recovery
# session was 898419887480849435. Replace with the team you want to crawl.
curl -s -X POST http://localhost:8080/v1/admin/figma-inventory/teams \
  -H "Content-Type: application/json" \
  -d '{"team_id":"898419887480849435","team_name":"INDmoney"}' | jq
```

Expected: `{"added": true, "team_id": "...", "trigger_sync": true}`.

Now watch the poller burn through Tier-A + Tier-B fetches. This will take 5–30 minutes for a ~500-file team (Figma Tier-1 budget is 12 RPM, so 500 files / 12 ≈ 42 min worst-case to deep-fetch all of them; the poller bounds each cycle at 30 files so multiple cycles are required).

**Verification checkpoint (after ~5 min).**
```sql
SELECT 'teams', COUNT(*) FROM figma_team
UNION ALL SELECT 'projects', COUNT(*) FROM figma_project
UNION ALL SELECT 'files (shells)', COUNT(*) FROM figma_file
UNION ALL SELECT 'files (deep)', COUNT(*) FROM figma_file WHERE deep_synced_at IS NOT NULL
UNION ALL SELECT 'pages', COUNT(*) FROM figma_page
UNION ALL SELECT 'sections', COUNT(*) FROM figma_section
UNION ALL SELECT 'sections w/ blob', COUNT(*) FROM figma_section WHERE subtree_json_zstd IS NOT NULL
UNION ALL SELECT 'runs', COUNT(*) FROM figma_inventory_run WHERE status='ok';
```
Expect: teams 1, projects ≥ 5, files ≥ 100, deep-synced steadily climbing, sections-with-blob = deep-synced files × avg sections/file.

**Performance probe.** While the poller works, watch:
- DB size: `du -h services/ds-service/data/ds.db` — should grow gradually, target < 100 MB by full deep-sync of 500 files (per plan 002 estimate)
- Memory: `ps -o rss= -p $(pgrep -f 'cmd/server')` — peak RSS should stay < 500 MB. If it spikes past 1 GB during deep-tree fetch, **flag it** — that's an Out-of-Memory risk on Fly's default 1 GB instance.

### Phase 5: Map projects → (domain, product)

Autosync planner refuses files in projects with no `figma_project_mapping` row. Run the section classifier first (writes overrides for non-standardly-named sections), then mapping. No admin UI yet — use SQL.

```bash
# 5a. List figma_project rows to find the ones you want to map
sqlite3 services/ds-service/data/ds.db \
  "SELECT project_id, name FROM figma_project WHERE tenant_id='e090530f-2698-489d-934a-c821cb925c8a' AND deleted_at IS NULL ORDER BY name;"
```

Pick 1–2 projects to enable. For each, insert a mapping. Example for "Mini App V4":

```sql
INSERT INTO figma_project_mapping (
  tenant_id, project_id, domain, product, platform_default,
  enabled_for_autosync, mapped_by_user_id, mapped_at, updated_at
) VALUES (
  'e090530f-2698-489d-934a-c821cb925c8a',
  '<project_id_from_step_5a>',
  'INDstocks', 'Mini App', 'mobile',
  1, 'system@indmoney.local',
  datetime('now'), datetime('now')
);
```

```bash
# 5b. Optionally run the section name classifier (writes (sub_product, sub_flow) overrides
# for sections without a "/" in their name)
cd services/ds-service
go run ./cmd/figma-autosync-classify -tenant e090530f-2698-489d-934a-c821cb925c8a -dry-run
# review output, then re-run without -dry-run to commit
go run ./cmd/figma-autosync-classify -tenant e090530f-2698-489d-934a-c821cb925c8a
cd -
```

**Verification checkpoint.**
```sql
SELECT COUNT(*) FROM figma_project_mapping WHERE enabled_for_autosync=1;
-- expect >= 1

SELECT COUNT(*) FROM figma_section WHERE classified_source IN ('claude_heuristic','section_name');
-- expect = total sections in mapped projects
```

### Phase 6: AutoSync dry-run + execute

Dry-run first to see what the planner intends to do:

```bash
cd services/ds-service
go run ./cmd/figma-autosync-dryrun \
  -tenant e090530f-2698-489d-934a-c821cb925c8a \
  -skip-empty \
  | jq
cd -
```

Expected JSON shape: per file → array of sections with `action` ∈ {full_export, cheap_update, skip_unchanged, skip_quarantined}. Every section in your mapped projects with a unique content_hash should show `action: "full_export"` (this is the first run — nothing exported yet).

**Stop here** if any file shows `skip_quarantined` with `reason: "hash_not_ready"` — that means the poller hasn't deep-fetched it yet. Wait for the next 5-min cycle and re-dry-run.

Now execute against one file to confirm the pipeline works end-to-end:

```bash
# Pick a small file (find file_key from the dry-run output, e.g. 10-frame test file).
curl -s -X POST http://localhost:8080/v1/admin/figma-autosync/execute \
  -H "Content-Type: application/json" \
  -d '{"file_key":"<file_key_from_dry_run>"}' | jq
```

Expected: `{"tenant_id": "...", "files": [{"file_key": "...", "sections": N, "full_export": N, ...}], "totals": {...}}`. Empty `errors[]`.

**Verification checkpoint.**
```sql
-- Real flows + screens written by RunExport
SELECT (SELECT COUNT(*) FROM projects) AS projects,
       (SELECT COUNT(*) FROM project_versions WHERE status='view_ready') AS versions_ready,
       (SELECT COUNT(*) FROM flows) AS flows,
       (SELECT COUNT(*) FROM screens) AS screens,
       (SELECT COUNT(*) FROM screen_canonical_trees) AS canonical_trees;

-- Autosync state ledger
SELECT file_key, section_id, last_attempt_status, last_synced_flow_id
  FROM figma_auto_sync_state
 WHERE tenant_id='e090530f-2698-489d-934a-c821cb925c8a';

-- Pipeline output: Stage 6.7 organism detection
SELECT COUNT(*) AS organism_verdicts,
       COUNT(DISTINCT screen_id) AS distinct_screens
  FROM detected_organism_match;
```

Expect: projects ≥ 1, versions_ready ≥ 1, flows = sum of `full_export` across files, screens > 0, canonical_trees = screens count. Organism verdicts ≥ flows × 5 (multiple candidates per screen typically).

**Performance probe.**
- Time from `POST /v1/admin/figma-autosync/execute` → response: should be < 10 sec for a 1-file plan (RunExport returns 202, async pipeline goroutine continues in background).
- Time from response → SSE `project_view_ready` on Stage 7: should be < 60 sec for a 12-frame flow.
- DB growth: each flow adds ~50 KB (canonical_tree compressed + asset_cache rows). 100 flows ≈ 5 MB.

### Phase 7: Atlas frontend — view + render

Open the Atlas web app (already running on `:3001` from Phase 3).

```
http://localhost:3001/atlas/figma-inventory
```
Expected: TeamBar shows INDmoney, FilesTable lists every file with sort-by-Nodes, Pages, Sections, Recency, etc. RunsStrip shows last poll cycle.

```
http://localhost:3001/atlas
```
Expected: brain-graph canvas. Pan around. Click a project node to drill into its flows.

```
http://localhost:3001/atlas/projects/<your-mapped-project-slug>
```
Expected: project shell with version selector + flow list. Click a flow → leaf canvas renders the screen via `nodeToHTML(canonical_tree)`. Image fills lazy-load from asset_cache (or Figma fallback).

**Verification checkpoint.**
- Page loads in < 2 sec on first paint.
- No console errors in browser devtools.
- Rendered HTML matches the Figma source (eyeball check against the original file). Pixel-perfect isn't required for this test; structural fidelity is.

**Performance probe.** Open DevTools → Performance → record a flow open. Expect FCP < 1500 ms, LCP < 3000 ms. If higher, check the SSE channel — slow Stage 9 (cluster prerender) doesn't block the frontend but does add weight to the cluster-asset path.

### Phase 8: Organism dashboard

```
http://localhost:3001/atlas/organisms
```

Expected:
- **Adoption table**: one row per published organism slug from `manifest.json` (List 343, List 311, List on Surface, List on Card, etc.). Per row: instance / exact-match / near-match / novel counts. Drift signal: 🟢 / 🟡 / 🔴 / ✨.
- **Promotion candidates panel**: ranked list of recurring un-published patterns. Stability score, atom reuse rate, frequency × file count. Editable proposed-name field (in-place commit on blur).

**Verification checkpoint.**
- At least one organism slug shows non-zero counts (if your mapped project has any list rows).
- Promotion candidates surface if you re-run autosync on a second mapped project (cross-product clustering needs ≥ 2 files per cluster). With one file, expect "No promotion candidates yet."

### Phase 9: Figma plugin "Check selection against DS"

Load the plugin in Figma desktop:

1. **Plugins → Development → Import plugin from manifest…**
2. Select `<repo>/figma-plugin/manifest.json`
3. **Plugins → Development → INDmoney DS Sync**
4. Settings → set `ds_service_url` to `http://localhost:8080`. Bearer token field can be empty (dev bypass active).

In Figma, select a single FRAME that looks like a List-on-Surface row but isn't an INSTANCE. Run **Plugins → Development → INDmoney DS Sync → "Check selection against DS"**.

Expected verdict card:
- Kind dot + label: `exact` / `near` / `novel` / `unrelated`
- Suspected slug + confidence %
- Diff descriptors: `added: …`, `missing: …`
- Buttons: "Replace with INSTANCE" (opens Figma deeplink to published organism) / "Mark as fork"

**Verification checkpoint.**
- Verdict appears within 2 sec
- ds-service log shows `POST /v1/audit/organism-match` with non-error response
- `detected_organism_match` row for the selected frame's file+frame_id (if a project version exists for it)

### Phase 10: Performance + memory audit

Run the system under sustained load. Open Activity Monitor (or `htop`).

```bash
# Terminal C — load generator: re-trigger autosync for every mapped file
for i in $(seq 1 5); do
  curl -s -X POST http://localhost:8080/v1/admin/figma-autosync/execute | jq -c '.totals'
  sleep 30
done

# Terminal D — watch DB + memory
while true; do
  ds_size=$(du -h services/ds-service/data/ds.db | cut -f1)
  rss=$(ps -o rss= -p $(pgrep -f 'cmd/server') | awk '{print int($1/1024)" MB"}')
  echo "$(date +%H:%M:%S) | DB: $ds_size | RSS: $rss"
  sleep 10
done
```

**Targets to verify.**

| Metric | Target | What's bad |
|---|---|---|
| ds-service RSS steady-state | < 400 MB | > 1 GB risks Fly OOM kill |
| ds-service RSS peak during deep-fetch | < 700 MB | > 1.5 GB confirms memory leak |
| SQLite DB size after full poll | < 200 MB | > 1 GB means subtree blob isn't compressing as expected |
| Atlas Next.js RSS | < 300 MB | > 700 MB suggests an SSR memory leak |
| POST /v1/projects/export response latency | < 200 ms p99 | > 1 sec means RunExport's inline tx is too fat |
| Stage 6.7 organism detection per-version time | < 3 sec for 12 screens | > 10 sec means walker is too slow |
| Atlas leaf-canvas first-paint | < 1500 ms | > 3 sec means nodeToHTML is too slow |

**If any target is breached:** capture the log output + `pprof` profile and stop. Don't deploy until investigated.

```bash
# heap profile
curl -s http://localhost:8080/debug/pprof/heap > heap.pprof
go tool pprof -top heap.pprof | head -20

# goroutine profile (catch leaks)
curl -s http://localhost:8080/debug/pprof/goroutine > goroutines.pprof
go tool pprof -top goroutines.pprof | head -20
```

---

## 5. Verification SQL reference

```sql
-- Pipeline health
SELECT pv.id, pv.status, pv.created_at, p.slug, COUNT(s.id) screens
  FROM project_versions pv JOIN projects p ON p.id=pv.project_id
  LEFT JOIN screens s ON s.version_id=pv.id
 GROUP BY pv.id ORDER BY pv.created_at DESC LIMIT 10;

-- Asset cache hit rate
SELECT format, COUNT(*) rows, SUM(bytes)/1024/1024 'MB'
  FROM asset_cache GROUP BY format ORDER BY rows DESC;

-- Section subtree blob population
SELECT f.name AS file_name, COUNT(s.section_id) sections,
       SUM(CASE WHEN s.subtree_json_zstd IS NOT NULL THEN 1 ELSE 0 END) with_blob,
       AVG(s.subtree_node_count) avg_nodes
  FROM figma_section s JOIN figma_file f ON f.file_key=s.file_key
 GROUP BY f.file_key ORDER BY sections DESC LIMIT 20;

-- AutoSync ledger
SELECT last_attempt_status, skip_reason, COUNT(*)
  FROM figma_auto_sync_state GROUP BY 1,2 ORDER BY 3 DESC;

-- Organism dashboard sanity
SELECT match_kind, COUNT(*) FROM detected_organism_match GROUP BY 1;
SELECT slug, frequency, file_count, stability_score, atom_reuse_rate
  FROM promotion_candidate ORDER BY frequency*stability_score*atom_reuse_rate DESC LIMIT 10;
```

---

## 6. Troubleshooting playbook

**Symptom: poller logs `GetFileDeepTree failed: 401 Unauthorized`**
→ FIGMA_PAT is invalid or revoked. Regenerate at https://figma.com/developers, update `.env.local`, restart ds-service.

**Symptom: `figma_section.subtree_json_zstd IS NULL` for every section after 30 min**
→ Either FIGMA_PAT lacks team access (check Figma team settings → API tokens permission), or the poller's Tier-B batch hasn't reached this file yet (5-min loops × 30 files/batch). Force-trigger: `curl -X POST http://localhost:8080/v1/admin/figma-inventory/sync`.

**Symptom: AutoSync dry-run shows every file `file_skip: project_unmapped`**
→ You haven't inserted `figma_project_mapping` rows. Phase 5 step 5a + manual INSERT.

**Symptom: `POST /v1/admin/figma-autosync/execute` returns 200 but no flows appear**
→ Check `figma_auto_sync_state` — if every section is `skip_quarantined`, the planner deemed them ineligible. Look at `skip_reason`. Common causes: no Final/version pages classified, section name parse failure, hash not yet computed.

**Symptom: Atlas shows "Loading project…" forever**
→ The pipeline goroutine errored mid-Stage. Check ds-service logs for `pipeline error` lines. The SSE channel only fires `project_view_ready` after Stage 6 commits; if Stage 4 (image render) failed, the version stays `pending`. Force re-export.

**Symptom: ds-service crashes on startup with `migration 0028: figma_page not found`**
→ Migration ordering issue. Run `sqlite3 data/ds.db "SELECT version FROM schema_migrations ORDER BY version"` to see what's applied. If 25 is missing but 28 tries to apply, you've checked out a branch missing migration 0025 — verify you're on `feat/figma-db` post-Phase-0 merge.

**Symptom: organism dashboard shows zero match counts**
→ Stage 6.7 ran but the canonical_tree had no FRAMEs matching `organismMinAtomInstances` (≥ 2 atom-INSTANCE descendants). Check log for `stage 6.7: no candidates`. Possible if the file has only icons / no list rows.

**Symptom: plugin "Check selection against DS" returns 401**
→ DEV_AUTH_BYPASS isn't recognized. Confirm `DEV_AUTH_BYPASS=1` is in the SAME shell that started ds-service (Terminal A), not just `.env.local`. Restart ds-service after editing.

---

## 7. Deployment readiness checklist

Run this BEFORE the next Fly deploy. Don't deploy if any line is unchecked.

- [ ] **Phase 0 done**: organism Bias #1/#3/#5 cherry-picked onto `feat/figma-db`
- [ ] **All migrations 0001–0031 apply clean** on a fresh DB via `go test ./internal/db/...`
- [ ] **Phase 4 verification passes** locally: full poll cycle produces non-NULL `subtree_json_zstd` on every section in the seeded team
- [ ] **Phase 6 verification passes** locally: one full_export through autosync executor produces a `project_versions` row with `status='view_ready'` and a populated canonical_tree
- [ ] **Phase 10 performance probes** all under their thresholds
- [ ] **Fly DB backed up** before deploy (the wipe earlier today removed everything; if you want a safety net, take a Fly snapshot now: `fly postgres backup create`)
- [ ] **`pre-wipe-20260514-023830.bak`** local backup either copied somewhere safe or knowingly deleted (it's 13 GB)
- [ ] **Plan 002 (this branch's storage refactor) deploy plan reviewed** — migrations 0030+0031 run in order on Fly's empty DB, no backfill needed (already verified — see `docs/plans/2026-05-14-002-feat-figma-section-subtree-blob-plan.md`)
- [ ] **Plugin distribution** — if you want designers other than yourself to use the plugin, ensure the `manifest.json` `ds_service_url` default points at the Fly URL (not `localhost:8080`)
- [ ] **Webhook receiver shipped** OR explicit decision made to live with 5-min poll cadence for v1 (see Section 3 🔴)

---

## 8. Plan-file references

- `docs/plans/2026-05-13-001-feat-organism-pattern-detection-plan.md` — organism workstream
- `docs/plans/2026-05-13-002-feat-figma-db-phase-2-plan.md` — Figma DB Phase 2 (inventory + admin UI)
- `docs/plans/2026-05-14-001-feat-figma-db-autosync-bridge-plan.md` — autosync planner + executor + classifier
- `docs/plans/2026-05-14-002-feat-figma-section-subtree-blob-plan.md` — section subtree blob storage (replaces figma_node)

All four `status: active`. Flip to `completed` once production deploy is verified.
