---
title: Deploy + domain setup runbook
created: 2026-05-02
status: living
---

# Deploy + domain setup runbook

How the docs site at `indmoney-design-system-docs.vercel.app` reaches the
local ds-service running on the operator's laptop. The architecture doc
(`docs/architecture.md`) describes an aspirational `*.indmoney.dev` zone
that doesn't exist publicly today; this runbook covers what actually
works right now.

## Current production topology (2026-05-02)

```
Browser (anywhere)
        │
        ▼ HTTPS
indmoney-design-system-docs.vercel.app   (Next.js, auto-deployed from origin/main)
        │
        ▼ NEXT_PUBLIC_DS_SERVICE_URL (env at build time)
<random>.trycloudflare.com               (Cloudflare quick tunnel)
        │
        ▼ encrypted tunnel
localhost:8080                            (ds-service binary on operator laptop)
        │
        ▼
services/ds-service/data/ds.db            (SQLite — the production DB)
```

## Why the tunnel

Chrome's **Private Network Access** (CH130+) blocks public HTTPS pages
from calling `localhost`. The Vercel page can't reach
`http://localhost:8080` directly even with the operator's ds-service
running. The tunnel turns localhost into a public HTTPS URL so it's a
public→public request — no PNA prompt, no CORS-loopback block.

## Booting the stack

```bash
# 1. ds-service — must run with CORS_ALLOW_ORIGIN listing the Vercel URL.
cd /Users/chetansahu/indmoney-design-system-docs
go build -o /tmp/ds-service services/ds-service/cmd/server/

REPO_DIR=$(pwd) \
SQLITE_PATH=$(pwd)/services/ds-service/data/ds.db \
GRAPH_INDEX_MANIFEST_PATH=$(pwd)/public/icons/glyph/manifest.json \
GRAPH_INDEX_TOKENS_DIR=$(pwd)/lib/tokens/indmoney \
CORS_ALLOW_ORIGIN="http://localhost:3001,https://indmoney-design-system-docs.vercel.app" \
/tmp/ds-service > /tmp/ds-service.log 2>&1 &

# 2. Cloudflare quick tunnel — gives a fresh trycloudflare.com URL each run.
cloudflared tunnel --url http://localhost:8080 > /tmp/tunnel.log 2>&1 &

# 3. Read the public URL out of the tunnel log:
grep -oE "https://[a-z0-9-]+\.trycloudflare\.com" /tmp/tunnel.log | head -1
```

## Setting the Vercel env var (once per tunnel URL)

Each `cloudflared tunnel --url …` boot generates a NEW trycloudflare.com
hostname. Whenever you restart the tunnel, you have to update the Vercel
env var + trigger a redeploy.

```bash
# Update the env var (replaces the previous value).
vercel env rm NEXT_PUBLIC_DS_SERVICE_URL production --yes
echo "https://NEW-TUNNEL-URL.trycloudflare.com" | \
  vercel env add NEXT_PUBLIC_DS_SERVICE_URL production

# Trigger a Vercel rebuild (pushes the new value into the bundle).
git commit --allow-empty -m "deploy: refresh tunnel URL" && git push origin main
```

Vercel auto-deploys on push to `main` (per the project's git
integration). The `NEXT_PUBLIC_*` prefix means the value is baked into
the JS bundle at build time — restarting the tunnel without rebuilding
will leave the previous (dead) URL in the bundle.

## Why not a stable Cloudflare tunnel

Quick tunnels are ephemeral by design — the URL changes on every
boot. For a permanent URL you need:

1. A Cloudflare account on a real registered zone.
2. `cloudflared tunnel login` then `cloudflared tunnel create`.
3. A DNS record pointing `api.<your-domain>` at the tunnel.
4. `cloudflared tunnel route dns <tunnel-id> api.<your-domain>`.
5. Run the named tunnel in foreground or as a launchd service.

Step 1 needs a registered domain on Cloudflare's nameservers.
`indmoney.dev` (the architecture doc's intended domain) doesn't resolve
publicly today — buy + transfer to Cloudflare first, then this becomes
a one-time setup.

## Operator launch checklist (each time the laptop reboots)

- [ ] `cd ~/indmoney-design-system-docs`
- [ ] Build + start ds-service with the env vars above (paste the
      4-line export block)
- [ ] `cloudflared tunnel --url http://localhost:8080 &`
- [ ] Read the new tunnel URL from `/tmp/tunnel.log`
- [ ] `vercel env rm NEXT_PUBLIC_DS_SERVICE_URL production --yes`
- [ ] `echo "https://NEW-URL" | vercel env add NEXT_PUBLIC_DS_SERVICE_URL production`
- [ ] `git commit --allow-empty -m "deploy: refresh tunnel URL" && git push origin main`
- [ ] Wait ~60s for Vercel build to finish
- [ ] Open `https://indmoney-design-system-docs.vercel.app/atlas` and
      log in — graph should load

This is operator-toil. Phase 9 polish item: register a domain + set up
a named cloudflared tunnel as launchd so the URL is stable across
reboots and the env var doesn't need refreshing.

## Verifying the deploy is alive

```bash
# Tunnel reaches ds-service (expect HTTP 401 — auth required)
curl -sS -o /dev/null -w "%{http_code}\n" \
  "$(grep -oE "https://[a-z0-9-]+\.trycloudflare\.com" /tmp/tunnel.log | head -1)/v1/projects/graph?platform=mobile"

# CORS preflight from Vercel origin (expect HTTP 204 + access-control-allow-origin header)
curl -sS -D - -o /dev/null --max-time 5 \
  -H "Origin: https://indmoney-design-system-docs.vercel.app" \
  -H "Access-Control-Request-Method: GET" \
  -X OPTIONS \
  "$(grep -oE "https://[a-z0-9-]+\.trycloudflare\.com" /tmp/tunnel.log | head -1)/v1/projects/graph?platform=mobile" \
  | grep -iE "access-control|http"
```

Both should pass before the Vercel page works.
