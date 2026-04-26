# Operator runbook

> Owner: design-system-engineer · Last reviewed: 2026-04-26

Day-to-day operations for ds-service + sync.

## First-time setup

```bash
# 1. Clone + install
git clone https://github.com/chetansahu1998/indmoney-design-system-docs.git
cd indmoney-design-system-docs
npm install

# 2. Generate signing keys (one-time)
cd services/ds-service && go run scripts/genkey.go > /tmp/keys
# Add to .env.local at repo root:
#   JWT_SIGNING_KEY=...
#   JWT_PUBLIC_KEY=...
#   ENCRYPTION_KEY=$(openssl rand -base64 32)
#   BOOTSTRAP_TOKEN=$(openssl rand -hex 16)

# 3. Start ds-service
cd services/ds-service && go run ./cmd/server
# In another terminal:
curl -sS http://localhost:8080/__health   # expect: {"ok":true,"db":"ok",...}

# 4. Bootstrap super-admin (ONE-TIME)
BOOT="$(grep BOOTSTRAP_TOKEN .env.local | cut -d= -f2)"
curl -X POST http://localhost:8080/v1/admin/bootstrap \
  -H "X-Bootstrap-Token: $BOOT" \
  -H "Content-Type: application/json" \
  -d '{"email":"you@indmoney.com","password":"strong-password","tenant":"indmoney"}'

# 5. Login + capture token
TOKEN=$(curl -X POST http://localhost:8080/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"email":"you@indmoney.com","password":"strong-password"}' \
  | jq -r .access_token)

# 6. Upload Figma PAT for indmoney tenant
PAT="figd_..."
curl -X POST http://localhost:8080/v1/admin/figma-token \
  -H "Authorization: Bearer $TOKEN" \
  -d "{\"tenant\":\"indmoney\",\"pat\":\"$PAT\"}"

# 7. First sync
curl -X POST http://localhost:8080/v1/sync/indmoney \
  -H "Authorization: Bearer $TOKEN"
```

## Daily operations

### Check service health
```bash
curl -s http://localhost:8080/__health | jq
```

### Manual extract (without auth)
```bash
cd services/ds-service && \
go run ./cmd/extractor --brand indmoney --out ../../lib/tokens/indmoney
```

### Tail audit log
```bash
sqlite3 services/ds-service/data/ds.db \
  "SELECT ts, event_type, status_code, duration_ms, substr(details, 1, 80)
   FROM audit_log ORDER BY ts DESC LIMIT 20"
```

### Rotate Figma PAT
1. Generate new PAT at https://www.figma.com/developers/api#access-tokens
2. POST to `/v1/admin/figma-token` (overwrites previous, key_version increments)
3. Old PAT can be revoked in Figma after the next successful sync.

### Rotate JWT signing key
1. `go run services/ds-service/scripts/genkey.go > /tmp/newkey`
2. Update `JWT_SIGNING_KEY` + `JWT_PUBLIC_KEY` in `.env.local`
3. Restart ds-service. All existing tokens immediately invalid; users re-login.
4. Frequency: annually, or immediately on suspicion of compromise.

### Rotate AES encryption key
**Caution: encrypted Figma PATs become unreadable after key rotation.**
1. Decrypt all Figma PATs into memory using the OLD key.
2. Update `ENCRYPTION_KEY` to new value.
3. Re-encrypt + re-store with new key + bumped key_version.
4. v1.1 will automate this via a `--rotate-encryption-key` ds-service CLI flag.

### Add a new tenant (e.g. tickertape)
```bash
# Mint via super-admin (no UI in v1):
curl -X POST http://localhost:8080/v1/admin/tenants \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"slug":"tickertape","name":"Tickertape"}'
# Then upload its Figma PAT and sync as above.
```

### Add a designer to indmoney tenant
v1: direct DB write. v1.1: API endpoint.
```bash
sqlite3 services/ds-service/data/ds.db <<SQL
INSERT INTO users (id, email, password_hash, role, created_at)
VALUES (lower(hex(randomblob(16))),
        'designer@indmoney.com',
        '<bcrypt hash>',
        'user',
        datetime('now'));
INSERT INTO tenant_users (tenant_id, user_id, role, status, created_at)
SELECT t.id, u.id, 'designer', 'active', datetime('now')
FROM tenants t, users u
WHERE t.slug = 'indmoney' AND u.email = 'designer@indmoney.com';
SQL
```

## ds-service launchd agent (macOS)

To keep ds-service running after logout/reboot:

`~/Library/LaunchAgents/com.indmoney.ds-service.plist`:
```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>          <string>com.indmoney.ds-service</string>
  <key>ProgramArguments</key>
  <array>
    <string>/usr/local/bin/go</string>
    <string>run</string>
    <string>./cmd/server</string>
  </array>
  <key>WorkingDirectory</key>
  <string>/Users/chetansahu/indmoney-design-system-docs/services/ds-service</string>
  <key>EnvironmentVariables</key>
  <dict>
    <key>PATH</key>
    <string>/Users/chetansahu/.nvm/versions/node/v24.9.0/bin:/usr/local/bin:/usr/bin:/bin</string>
    <key>HOME</key>
    <string>/Users/chetansahu</string>
  </dict>
  <key>RunAtLoad</key>      <true/>
  <key>KeepAlive</key>      <true/>
  <key>StandardOutPath</key><string>/tmp/ds-service.log</string>
  <key>StandardErrorPath</key><string>/tmp/ds-service.err</string>
</dict>
</plist>
```

```bash
launchctl load ~/Library/LaunchAgents/com.indmoney.ds-service.plist
launchctl list | grep indmoney   # verify
```

## Cloudflare Tunnel (production exposure)

```bash
# One-time setup
brew install cloudflared
cloudflared tunnel login                    # browser-based
cloudflared tunnel create indmoney-ds-tunnel
# Edit ~/.cloudflared/config.yml:
#   tunnel: <UUID from create>
#   credentials-file: ~/.cloudflared/<UUID>.json
#   ingress:
#     - hostname: api.ds.indmoney.dev
#       service: http://localhost:8080
#     - service: http_status:404

cloudflared tunnel route dns indmoney-ds-tunnel api.ds.indmoney.dev
cloudflared tunnel run indmoney-ds-tunnel  # foreground for testing

# Then add to launchd alongside ds-service for auto-start
```

Update Vercel project env: `DS_SERVICE_URL=https://api.ds.indmoney.dev`.
