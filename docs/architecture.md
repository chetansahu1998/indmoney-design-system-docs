# INDmoney DS Docs · Architecture

> **Last reviewed: 2026-04-26 — diagram below covers the token round-trip only and predates Phases 4–9 (project view, atlas mind-graph, decisions, inbox, plugin Projects mode). For current production topology including the Figma plugin's projects-export path through audit-server, the Cloudflare quick-tunnel reality, and all the surfaces shipped since, see [`docs/runbooks/deploy-domain-setup.md`](./runbooks/deploy-domain-setup.md). The `*.indmoney.dev` zone shown below is aspirational — production runs on ephemeral `trycloudflare.com` URLs as of 2026-05-02.**

## High-level (token round-trip path — partial; see runbook for full)

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
                         │ port 8080 → Cloudflare quick tunnel
                         │   (ephemeral trycloudflare.com URL —
                         │    NOT a stable .indmoney.dev domain today)
                         ▼
            <ephemeral>.trycloudflare.com (HTTPS)
                         ▲
                         │ Bearer JWT
            ┌─────────────────────────────────┐
            │  Next.js docs site (Vercel)     │
            │   ├── /api/sync (token sync)    │
            │   ├── /api/projects/export      │
            │   │   (browser-fallback only;   │
            │   │    plugin uses localhost:7474│
            │   │    via audit-server)        │
            │   ├── /atlas (mind graph)       │
            │   ├── /projects/[slug]          │
            │   ├── /components (horizontal   │
            │   │   canvas, atomic-tier)      │
            │   └── /inbox                    │
            └─────────────────────────────────┘
                         │
                         ▼
        indmoney-design-system-docs.vercel.app
        (production alias TODAY;
         indmoney.ds.indmoney.dev is future-state)
```

> **Plugin path correction.** The Figma plugin (`figma-plugin/`) does NOT travel through Vercel for the projects-export round-trip. Its primary path is `localhost:7474/v1/projects/export` (audit-server), which forwards to `localhost:8080/v1/projects/export` (ds-service) carrying the user's docs-site JWT. See [`deploy-domain-setup.md`](./runbooks/deploy-domain-setup.md) and [`STATUS.md`](./STATUS.md) for the verified contract (commit `3d1e479` made the switch).

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
