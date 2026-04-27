# Plugin end-to-end test runbook

Smoke test for the audit pipeline + plugin from a clean slate. ~5 minutes.

## Prerequisites

- `FIGMA_PAT` is set in `.env.local` (already present).
- Go 1.22+ installed (`go version`).
- Figma desktop app.
- The 13 INDmoney product files you have access to (or any one you can open in Figma desktop).

## Step 1 — Build the plugin once

```bash
cd figma-plugin
npx tsc
```

This compiles `code.ts` → `code.js`. The plugin loads `code.js` at runtime; rebuild only when `code.ts` changes.

## Step 2 — Start the local audit server

In a separate terminal (keep it running for the duration of the test):

```bash
npm run audit:serve
```

You should see:

```
INFO audit-server listening addr=http://localhost:7474 endpoint=/v1/audit/run repo=/Users/.../indmoney-design-system-docs
```

The plugin POSTs to `localhost:7474/v1/audit/run` — the server runs the Go audit core on whatever node tree the plugin sends.

## Step 3 — Import the plugin in Figma desktop

1. Open Figma desktop.
2. Menu → **Plugins** → **Development** → **Import plugin from manifest…**
3. Pick `<repo>/figma-plugin/manifest.json`.
4. The plugin appears under **Plugins → Development → INDmoney DS Sync**.

## Step 4 — Run the audit on one file

1. Open any of your 13 product files in Figma (e.g. INDstocks V4).
2. **Plugins → Development → INDmoney DS Sync** → opens the panel.
3. Verify connection: click **Settings → Ping audit server** → should log `Audit server up at http://localhost:7474`.
4. Click **Audit → File**.

What you should see:

- The panel logs `Auditing file…`, then a couple of seconds later
  - `✓ <file> registered in lib/audit-files.json — commit + push to deploy`
  - `✓ Updated /Users/.../lib/audit/<slug>.json`
- A summary card: `Coverage X%` · `DS / Amb / Cust` triple · `P1 N P2 N P3 N` pills.
- A scrollable fix list — each card shows `<node> · <property> <observed> → <token-path>` with priority pill + Apply / Copy path buttons.

In your repo:

```bash
git status
# Expected:
#   modified:   lib/audit-files.json     (one new entry under "files")
#   modified:   lib/audit/index.json     (rebuilt rollup)
#   new file:   lib/audit/<slug>.json    (the per-file audit)
```

## Step 5 — Test click-to-apply (optional)

1. In the plugin's fix list, find a fix with `Apply` button (shown only on `fill` properties when a token match is high-confidence).
2. Click **Apply**.
3. The card flips to "Applied · ⌘Z to undo". The Figma node's fill is now bound to the suggested variable.
4. ⌘Z undoes the binding (Figma's native undo stack).

## Step 6 — Commit and push

```bash
git diff lib/audit/         # review the audit JSON
git add lib/audit lib/audit-files.json
git commit -m "audit: register <file>"
git push origin main
```

Vercel rebuilds. After the deploy:

- `/files` lists the file as a card with coverage % + DS comp %.
- `/files/<slug>` shows per-screen audit panels (token coverage, component usage, drift).
- Foundations color/typography/spacing chips light up wherever the file's drift list referenced a token.

## Step 7 — Add more files (no code changes)

Repeat Step 4 for each Figma file you want to audit. Each file auto-registers in `lib/audit-files.json` and surfaces on `/files` after the next deploy.

## Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| Panel shows "Audit server unreachable" | `npm run audit:serve` not running, or port conflict | Check the audit-server terminal; ensure port 7474 is free; restart |
| `Apply` button errors with "Your Figma plan doesn't allow plugin variable writes" | File's variables aren't local / Figma plan tier | Use Copy path; bind manually in Figma's variable panel |
| Per-file JSON not written after audit | Audit was scoped to selection or page (only "Audit file" persists) | Click Audit → **File**, not Selection / Page |
| Empty fix list with `Coverage 0%` | The DS tokens aren't matching anything in the file's fills | Confirm `lib/tokens/indmoney/semantic.tokens.json` is current — re-run `npm run sync:tokens` if stale |
