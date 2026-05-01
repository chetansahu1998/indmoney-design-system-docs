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


Figma plugin (in-Figma sandbox, designer's machine)
        │
        ├─▶ HTTP localhost:7474   (audit-server: publish + audit endpoints,
        │                          plugin polls /__health every 5s)
        │
        └─▶ HTTPS Vercel /api/projects/export   (Phase 7.8 proxy → ds-service
                                                  via the same tunnel above,
                                                  uses the user's docs-site JWT
                                                  pasted into plugin Settings)
```

## The two backend processes — what each does

| Process | Port | Started by | What the plugin uses it for |
|---|---|---|---|
| **ds-service** (cmd/server) | 8080 | `/tmp/ds-service` binary, manual launch with env vars | Project storage, audit results, mind graph, search, decisions, /api/projects/export proxy target |
| **audit-server** (cmd/audit-server) | 7474 | `npm run audit:serve` | Phase 0/2 plugin pair — receives publish + audit menu calls; the green dot in the plugin status bar polls its `/__health` |

Both must be running for the plugin to work end-to-end. The plugin shows "Audit server unreachable" when 7474 is down even if 8080 is fine — the dot is hard-wired to 7474.

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

# 2. audit-server — required for the Figma plugin's status banner +
#    publish/audit menu commands. Polled at /__health every 5s by the
#    plugin; the "Audit server unreachable" toast in the plugin means
#    this process is down.
npm run audit:serve > /tmp/audit-serve.log 2>&1 &

# 3. Cloudflare quick tunnel — gives a fresh trycloudflare.com URL each run.
cloudflared tunnel --url http://localhost:8080 > /tmp/tunnel.log 2>&1 &

# 4. Read the public URL out of the tunnel log:
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

## Figma plugin — first-time setup

The plugin lives at `figma-plugin/manifest.json`. To install in Figma:

1. Figma desktop → menu bar → Plugins → Development → Import plugin from manifest…
2. Select `figma-plugin/manifest.json`. The plugin appears under Plugins → Development → "INDmoney DS Sync".
3. Run the plugin from any file. The bottom-right status dot polls `localhost:7474/__health`; if it's red, run `npm run audit:serve` and wait 5s.
4. Open Settings (cog icon top-right of the plugin) → "Docs site auth" section.
5. In a separate browser tab, log into the docs site (`indmoney-design-system-docs.vercel.app`). Open DevTools → Console. Run:
   ```js
   JSON.parse(localStorage['indmoney-ds-auth']).state.token
   ```
   Copy the result string.
6. Paste it into the plugin's "JWT token" input. Click **Save token**. The status line shows "Token saved." in green and the input clears.

The token is stored in `figma.clientStorage` and persists across plugin reloads. It expires in 7 days (Phase 0 JWT lifetime); when it does, the plugin's Send button will start returning 401 and you re-paste a fresh one.

## End-to-end test — exporting a real flow

After plugin setup is complete, the canonical happy-path test:

1. Open the target Figma file (e.g. *INDstocks V5*).
2. Select frames inside a SECTION node — the smart-grouping detection groups them by enclosing section + auto-detects light/dark mode pairs.
3. Switch the plugin to **Projects** mode (the third tab).
4. The plugin shows a preview: one row per detected flow, with editable Product / Path / Persona / Platform fields. Set Product = "Indian Stocks", Path = "research" (no slash prefix — the path is a hierarchical string, not a URL), persona stays "Default" or whatever fits.
5. Click **Send to Projects**. The button spins; within ~1-2 seconds you should see a green toast: *"Exported successfully — Project xxxxxxxx… — click to open"*. Click it.
6. Browser opens to `https://indmoney-design-system-docs.vercel.app/projects/<slug>`. The atlas surface shows your screens at preserved (x, y) coordinates within ~10–15s; the audit count ticks up over the next ~30–60s as rule classes finish.

What to verify on the project view:
- **Top half**: a pannable, zoomable canvas with each frame rendered as a textured plane.
- **Bottom half tabs**: DRD (empty document, ready to type), Violations (filled in once audit completes), Decisions (empty), JSON (raw canonical tree of the most recently selected screen).
- **Theme toggle** in the canvas chrome flips light↔dark; the plugin auto-detected mode pairs render correctly.

If the export fails, the toast shows the upstream error. Common causes:
- *401 unauthorized* — JWT expired or wrong; re-paste in plugin Settings.
- *upstream_unreachable* — ds-service or the tunnel is down. Check `/tmp/ds-service.log` and `/tmp/tunnel.log`.
- *no Figma PAT for tenant* — the rendering pipeline can't fetch PNGs from Figma. Set the tenant's PAT via `POST /v1/admin/figma-token` with the bootstrap token.

## Smoke checks the operator should run after every deploy

```bash
# 1. ds-service responds (expect 401)
curl -sS -o /dev/null -w "%{http_code}\n" \
  "$(grep -oE "https://[a-z0-9-]+\.trycloudflare\.com" /tmp/tunnel.log | head -1)/v1/projects/graph?platform=mobile"

# 2. audit-server responds (expect 200)
curl -sS -o /dev/null -w "%{http_code}\n" http://localhost:7474/__health

# 3. Vercel proxy reaches ds-service (expect 401 — proxy passed; auth missing)
curl -sS -o /dev/null -w "%{http_code}\n" \
  -X POST https://indmoney-design-system-docs.vercel.app/api/projects/export \
  -H "Content-Type: application/json" -d '{}'

# 4. Login round-trip (expect 200 + a JWT)
curl -sS -X POST -H "Content-Type: application/json" \
  --data-binary @<(printf '{"email":"chetan@indmoney.com","password":"<your-password>"}') \
  "$(grep -oE "https://[a-z0-9-]+\.trycloudflare\.com" /tmp/tunnel.log | head -1)/v1/auth/login" \
  | python3 -c "import sys,json; print(json.loads(sys.stdin.read()).keys())"
```

All four passing means the stack is end-to-end live; any failure narrows the bug down to a specific layer.
