# Sync runbook

> Owner: design-system-engineer · Last reviewed: 2026-04-26 · Severity: P1

## When designers click "Sync"

1. Designer opens `https://indmoney.ds.<domain>` (or `localhost:3001` in dev).
2. Clicks the **Sync** button in the header.
3. SyncModal opens. If not signed in, login form appears.
4. After login, click **Sync now**.
5. The frontend calls `POST /api/sync` with their Bearer JWT.
6. Next.js Edge route forwards to `POST <DS_SERVICE_URL>/v1/sync/indmoney` with the
   token + an `X-Trace-ID` header.
7. ds-service runs the orchestrator:
   - Decrypts the per-tenant Figma PAT from SQLite (AES-GCM).
   - Runs the pair-walker extractor against configured sources.
   - Converts to W3C-DTCG JSON, computes canonical SHA-256.
   - If the hash matches `sync_state.canonical_hash`, sync is **skipped**
     (no commit, no rebuild). Returns `status: "skipped_nochange"`.
   - Otherwise writes JSON files to `lib/tokens/indmoney/` (atomically).
   - If `SYNC_GIT_PUSH=true`, commits with `chore(sync): ...` message and
     pushes to `origin/main` (3-attempt rebase retry).
   - Updates `sync_state` and writes audit entry.
8. ds-service returns `SyncResult` with `trace_id`, `canonical_hash`, counts.
9. SyncModal shows success/failure with trace id.
10. After commit + push, Vercel auto-rebuilds (~30s) and the docs site reflects
    the new tokens.

## Concrete failure modes & fixes

### `service_unreachable` from `/api/sync`
- ds-service is not running at `DS_SERVICE_URL`.
- **Fix:** start it locally — `cd services/ds-service && go run ./cmd/server`,
  or check launchd agent status. Verify `curl $DS_SERVICE_URL/__health` returns 200.
- For prod: check Cloudflare Tunnel is up — `cloudflared tunnel info indmoney-ds-tunnel`.

### `unauth` (401)
- JWT expired (7-day lifetime) or never minted.
- **Fix:** sign in again via SyncModal.

### `forbidden` (403)
- User has no role on the tenant or role lacks `sync:figma` permission.
- **Fix:** super-admin grants role via:
  ```sql
  INSERT OR REPLACE INTO tenant_users (tenant_id, user_id, role, status, created_at)
  VALUES ('<tenant_id>', '<user_id>', 'designer', 'active', datetime('now'));
  ```

### Figma 403 / "PAT validation failed"
- Per-tenant Figma PAT expired or scopes changed.
- **Fix:** super-admin uploads a new PAT:
  ```bash
  curl -X POST "$DS_SERVICE_URL/v1/admin/figma-token" \
    -H "Authorization: Bearer $TOKEN" \
    -d '{"tenant":"indmoney","pat":"figd_..."}'
  ```

### Sync runs but produces no diff (always skipped_nochange)
- Tokens haven't actually changed in Figma since last sync.
- This is the expected fast-path. To force a full re-extract:
  ```sql
  DELETE FROM sync_state WHERE tenant_id = (SELECT id FROM tenants WHERE slug = 'indmoney');
  ```

### Commit + push fails after 3 retries
- Most likely: divergent local repo, git auth missing on the runner.
- **Fix:** ensure `git pull --rebase origin main` succeeds locally; check
  `git remote -v` and credential helper.
- Workaround: leave `SYNC_GIT_PUSH=false` and commit manually after each sync:
  ```bash
  git add lib/tokens/indmoney && git commit -m "chore(sync): tokens"
  git push
  ```

## Concurrency

ds-service has no per-tenant lock in v1. If two operators click Sync
simultaneously, both will run the extractor; the second one will see the
first's canonical_hash and skip. No corruption — just wasted Figma API calls.

## SLO targets (v1)

| SLO | Target |
|---|---|
| Sync round-trip P50 | < 90s |
| Sync round-trip P99 | < 180s |
| Sync success rate (30d) | > 99% |
| ds-service availability | > 99.5% |
| Audit-write success rate | > 99.9% |
