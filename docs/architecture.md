# INDmoney DS Docs · Architecture

> Last reviewed: 2026-04-26

## High-level

```
                    Figma (INDstocks V4 + Glyph)
                         │
                         │ REST /v1/files/.../nodes
                         ▼
            ┌─────────────────────────────────┐
            │  ds-service (Go, local launchd) │
            │   ├── pair-walker extractor     │
            │   ├── DTCG adapter              │
            │   ├── canonical SHA-256         │
            │   ├── SQLite (auth+audit+state) │
            │   └── git commit + push         │
            └─────────────────────────────────┘
                         │ port 8080 → Cloudflare Tunnel
                         ▼
                api.ds.indmoney.dev (HTTPS)
                         ▲
                         │ Bearer JWT
            ┌─────────────────────────────────┐
            │  Next.js docs site (Vercel)     │
            │   ├── /api/sync (Edge proxy)    │
            │   ├── ColorSection (data-token) │
            │   ├── ⌘K cmdk search            │
            │   ├── TokenExportDialog         │
            │   └── SyncModal                 │
            └─────────────────────────────────┘
                         │
                         ▼
                indmoney.ds.indmoney.dev
```

## Token round-trip (operator-driven)

1. Designer edits Figma; closes the file.
2. Engineer opens `https://indmoney.ds.indmoney.dev/`.
3. `<ProtectedRoute>` chrome (v1.1) checks JWT — if absent, login form via SyncModal.
4. Authenticated user clicks **Sync now**.
5. Browser → `POST /api/sync` (Vercel Edge route) with Bearer token.
6. Edge route → `POST <DS_SERVICE_URL>/v1/sync/indmoney` (HTTPS through Cloudflare Tunnel).
7. ds-service:
   - Validates JWT (Ed25519, 7-day lifetime)
   - Resolves user role on tenant (super_admin OR tenant_users.role)
   - Decrypts per-tenant Figma PAT (AES-GCM in SQLite)
   - Runs pair-walker extractor:
     - Multi-source pool: design-system file (Glyph) + product file (INDstocks V4)
     - Light/dark frame pairing by parent SECTION + name match + dimensions
     - Walk both frames in lockstep, capture (light_fill, dark_fill) tuples
     - Cluster by hex pair → semantic roles
     - Filter noise observations (status bar, decoration)
     - HSL-aware classifier → buckets (surface, text-n-icon, success, danger, etc.)
   - DTCG adapter → JSON files (W3C-DTCG 2024 sRGB object form)
   - Computes canonical SHA-256 over sorted DTCG output
   - Compares with `sync_state.canonical_hash`; skips if unchanged
   - Otherwise: writes `lib/tokens/<brand>/{base,semantic,semantic-dark,_extraction-meta}.json`
   - With `SYNC_GIT_PUSH=true`: commits + pushes (3-attempt rebase retry)
   - Updates `sync_state` + writes audit log
8. ds-service returns `SyncResult` to the Edge route.
9. Edge route maps to `SyncResponse` (typed discriminated union) and returns to browser.
10. SyncModal surfaces success/skip/error with trace_id.
11. Vercel sees the new commit (push), rebuilds, deploys.
12. Page reload reflects new tokens.

## Observability

- **Audit log** — every sync attempt + auth event written to `audit_log` table.
  Query via `GET /v1/audit/:tenant` (tenant_admin only) or sqlite directly.
- **HTTP logs** — slog JSON to stderr for every request.
- **Frontend errors** — surfaced inline in modals; no error tracking service in v1.

## Security model

| Layer | What protects it |
|---|---|
| Figma PATs | AES-256-GCM in SQLite; `ENCRYPTION_KEY` separately deployed |
| User passwords | bcrypt cost-12, never logged |
| JWTs | Ed25519 signed, 7-day lifetime, public key offline-verifiable |
| API auth | Bearer token, role-gated routes, CORS reflects allowlist |
| ds-service exposure | Cloudflare Tunnel HTTPS-only, no inbound port forward |
| Repo secrets | `.env.local` gitignored, GitHub push protection catches PAT leaks |

Per-deploy threat assumptions:
- Single-operator-laptop deployment ⇒ trusted host environment
- Public docs site ⇒ no PII, public OK
- Figma PATs ⇒ leak risk only if `ENCRYPTION_KEY` AND SQLite both compromised

## What we deferred to v1.1

- **GitHub Actions repository_dispatch** — currently ds-service does direct git push.
  GHA flow is documented in the plan but not wired. Avoids GitHub App registration
  + PAT scope debate; v1 just uses the local checkout's git auth.
- **Refresh tokens** — 7-day access only.
- **Idempotency keys** — single-operator low-risk for v1.
- **Multi-tenant admin UI** — sqlite direct edits or curl admin endpoints.
- **Visual regression baselines** (`toHaveScreenshot`) — token-parity covers the
  load-bearing fidelity assertion; visual snapshots are nice-to-have.

## Code map

| Path | Purpose |
|---|---|
| `app/` | Next.js App Router pages |
| `app/api/sync/route.ts` | Edge proxy → ds-service |
| `components/` | UI — DocsShell, Header, sections, SyncModal, TokenExportDialog |
| `lib/brand.ts` | Brand union + assertion |
| `lib/tokens/loader.ts` | DTCG JSON loader, color decode, semantic-pair builder |
| `lib/tokens/exporters.ts` | Multi-format download (JSON/CSS/Swift/XML/Kotlin) |
| `lib/tokens/contract.ts` | Required semantic paths CI gate |
| `lib/ui-store.ts` | Zustand store (theme, density, search/sync/export modals) |
| `lib/auth-client.ts` | Frontend JWT + login + triggerSync |
| `lib/api/sync-types.ts` | Discriminated SyncResponse + Zod schemas |
| `services/ds-service/` | Go service |
| `services/ds-service/internal/figma/` | Pair-walker, REST client, DTCG adapter |
| `services/ds-service/internal/db/` | SQLite layer (users, tenants, audit, sync_state, figma_tokens) |
| `services/ds-service/internal/auth/` | JWT, bcrypt, AES-GCM |
| `services/ds-service/internal/sync/` | Orchestrator |
| `services/ds-service/cmd/server/` | HTTP API |
| `services/ds-service/cmd/extractor/` | CLI extractor (no auth) |
| `tests/token-parity/` | DOM ↔ JSON fidelity tests |
| `terrazzo.config.ts` | DTCG → CSS+TS compiler config |
| `docs/runbooks/` | Operations docs |

## Reproduce locally

See `docs/runbooks/operator.md` for full setup steps.
