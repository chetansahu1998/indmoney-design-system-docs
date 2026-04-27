# INDmoney DS Sync — Figma plugin

A local-first Figma plugin that:

- Pings the live docs site to verify connectivity.
- Reports the user's current selection (id, name, type, dimensions, child count) — feeds into future ds-service analysis.
- Injects every published icon (`kind=icon`) into the current page as native components.
- Injects every published component primitive (`kind=component`) — CTAs, progress bars, action bars — with their multi-color fills preserved.

## Install (development)

1. Build `code.ts` → `code.js`:
   ```bash
   npx tsc --target es2017 --module none figma-plugin/code.ts
   ```
2. In Figma desktop: **Plugins → Development → Import plugin from manifest…** and pick `figma-plugin/manifest.json`.

## Use

1. Run `Plugins → Development → INDmoney DS Sync`.
2. Confirm the docs base URL (defaults to the prod Vercel URL; switch to `http://localhost:3001` for local dev).
3. Click `Ping` to verify reachability.
4. Click `Inject icons` or `Inject components` — a new auto-layout frame appears on the current page named `INDmoney Icons (synced)` / `INDmoney Components (synced)` with each SVG dropped in.
5. Use the sub-frames as you would any imported components.

## What it talks to

- `GET https://<docs-site>/icons/glyph/manifest.json` — manifest.
- `GET https://<docs-site>/icons/glyph/<file>.svg` — each asset.
- (Future) `POST https://<ds-service>/v1/analyze` — selection analysis.

`networkAccess.allowedDomains` in the manifest constrains the plugin to those origins.

## Plumbing notes

- The plugin is **read-only against the docs site** — it never pushes back. Authoring/upload flows happen via the docs operator UI.
- `figma.createNodeFromSvg(svg)` parses the SVG into a Figma vector node. Multi-color logos and components retain their fills because the docs-site post-processor only flattens to `currentColor` for monochrome icons.
- `documentAccess: "dynamic-page"` lets the plugin work in multi-page files without loading the whole document tree upfront.
