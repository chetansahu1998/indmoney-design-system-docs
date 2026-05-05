Inter font files for the canvas-v2 surface.

Source: https://rsms.me/inter (Inter UI by Rasmus Andersson, SIL Open Font License 1.1).

These woff2s are referenced by `app/atlas/_lib/leafcanvas-v2/canvas-v2.css` so
the strict-TS LeafFrameRenderer keeps text rendering deterministic in PNG
snapshots and offline previews where a Google Fonts <link> wouldn't resolve.

Files:
  Inter-Regular.woff2   — weight 400
  Inter-Medium.woff2    — weight 500
  Inter-SemiBold.woff2  — weight 600
  Inter-Bold.woff2      — weight 700

The same files are aliased to `BasierCircle` in canvas-v2.css as a stub.
Once the Basier Circle licence (R10) lands, drop the real .woff2s next to
these and update the BasierCircle @font-face `src:` URLs.
