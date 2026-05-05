---
title: Convert Basier Circle .ttf to .woff2 to reduce font payload
status: open
created: 2026-05-05
priority: low
area: canvas-v2 / fonts
---

## What

The four Basier Circle weights are shipped as `.ttf` (~55 KB each, ~220 KB total). WOFF2 typically compresses 30–50% smaller for the same font, with identical visual output and broader compression algorithms (Brotli + custom transforms).

## Why it's deferred

The .ttf files arrived in the repo as the source distribution. Converting requires either:
- A build-time codepath (`fonttools` or `woff2_compress`) that emits .woff2 alongside .ttf
- Or a one-shot offline conversion committed to the repo

Neither is a render-fidelity blocker — TTF works in every modern browser. The savings are bandwidth + first-paint timing, not correctness.

## How to do it

Option A (offline, simpler):
```bash
brew install woff2
cd public/fonts/basier-circle/
for f in *.ttf; do woff2_compress "$f"; done
```
Then update `app/atlas/_lib/leafcanvas-v2/canvas-v2.css` to point each `@font-face` `src:` at the `.woff2` first with `.ttf` as fallback:

```css
src: local("Basier Circle Regular"),
     url("/fonts/basier-circle/BasierCircle-Regular.woff2") format("woff2"),
     url("/fonts/basier-circle/BasierCircle-Regular.ttf") format("truetype");
```

Option B (Next.js build-time):
- Add a `next.config.js` rewrite + a small loader that generates .woff2 on first build. Heavier; not recommended.

## Current behavior

Browsers download the full .ttf on every cold load. Cached after first hit.

## Trigger to do this

When canvas-v2 starts being a primary product surface (not just internal designer tooling) and first-paint latency becomes a measured concern.
