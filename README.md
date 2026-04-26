# INDmoney Design System Docs

A multi-brand design-system documentation site, forked from [Field DS](https://github.com/rjaiswal-design/field-design-system-docs) and powered by a Figma → W3C-DTCG token pipeline.

> **Status:** v1 in development (2026-04-26). See `docs/plans/` in the parent repo.

## What this is

- **Foundations docs** for INDmoney (and Tickertape, deferred): Typography, Color, Spacing, Motion, Iconography
- **Tokens sourced from Figma** via a Go extractor that walks the production app file (INDstocks V4) for usage-based color/spacing tokens, plus Glyph for typography
- **Compiled to W3C-DTCG JSON** via [Terrazzo](https://terrazzo.app), exposed as both `tokens.ts` (autocomplete) and `tokens.css` (CSS custom properties)
- **Light + dark mode** rendering, mode-paired tokens
- **Operator-gated `Sync now`** via local ds-service (Go + SQLite, Cloudflare Tunnel)

## Local dev

```bash
nvm use                       # Node 24+ recommended
npm install
cp .env.example .env.local    # fill in FIGMA_PAT + JWT keys
npm run dev                   # http://localhost:3001
```

In a second terminal:

```bash
cd services/ds-service
go run ./cmd/server           # http://localhost:8080
```

## Sync runbook (designers)

Click **Sync now** in the chrome after closing your Figma changes. ~30s later, `<brand>.ds.<domain>` reflects them. The chip turns red after 30 days unsynced.

## Operator runbook (engineers)

```bash
# 1. Re-extract tokens locally:
npm run sync:tokens

# 2. Review diff:
git diff lib/tokens/

# 3. Commit + push (Vercel auto-deploys both brands):
git add lib/tokens && git commit -m "chore(sync): tokens from <commit-sha>"
git push
```

For ds-service ops, see `docs/runbooks/`.

## Architecture (v1)

```
Figma (INDstocks V4 + Glyph)
   │
   ▼
services/ds-service/cmd/extractor (Go) — pair-walker, light/dark cluster, DTCG output
   │
   ▼
lib/tokens/<brand>/{base,semantic,text-styles}.tokens.json  (committed)
   │
   ▼
Terrazzo (build step) — lib/tokens/.generated/{tokens.ts, tokens.css}
   │
   ▼
Next.js sections (Color, Typography, Spacing, Motion, Iconography)
   │
   ▼
Vercel deploy → indmoney.ds.<domain>
```

ds-service runs **locally on the operator's Mac** as a launchd agent, exposed via **Cloudflare Tunnel** so the Vercel-deployed frontend can reach it for sync.

## Provenance

- Field DS chassis seed: `a035c17`
- DesignBrain-AI auth+middleware seed: `a6b8c7daa95cafa4fc143411236a1e3a67e25f4d`

See `PORTED_FROM_FIELD.md` and `services/ds-service/SOURCE.md`.
