---
title: Plumb Figma textAutoResize through canonical_tree to render text correctly
status: open
created: 2026-05-05
priority: medium
area: canvas-v2 / pipeline
---

## What

Figma's TEXT nodes carry a `textAutoResize` field with three values:

- `"NONE"` — fixed-size text box. Bbox is the user's rect; long content overflows.
- `"HEIGHT"` — auto-height. Width fixed by user, height grows to fit wrapped lines.
- `"WIDTH_AND_HEIGHT"` — hugs content. Both dimensions match the rendered text.

The canonical_tree pipeline (`services/ds-service/internal/projects/pipeline.go` + the canonical-tree builder) drops `textAutoResize` — only `style.fontFamily/Size/Weight/letterSpacing/lineHeightPx/textAlignHorizontal` survive. Without it, the renderer can't tell which wrapping mode each text node wants.

## Current workaround

`app/atlas/_lib/leafcanvas-v2/nodeToHTML.ts:202` defaults to:

```ts
whiteSpace: "pre-wrap",
wordBreak: "break-word",
overflow: "hidden",
```

This is correct for `HEIGHT` and `WIDTH_AND_HEIGHT` (the common case), but for `NONE` (rare — designers set fixed bboxes intentionally for tabular layouts and animations) it can soft-clip overflow text where Figma would render it cut-off-at-boundary.

## How to fix properly

1. **Backend** — extend the canonical_tree builder to preserve `textAutoResize` on TEXT nodes. Single-field addition, schema-additive.
2. **Frontend** — extend `TextStyle` in `app/atlas/_lib/leafcanvas-v2/types.ts` with `textAutoResize?: "NONE" | "HEIGHT" | "WIDTH_AND_HEIGHT"`.
3. **Renderer** — in `nodeToHTML.ts:renderText`, switch on the field:

   | textAutoResize | whiteSpace | width | height |
   |---|---|---|---|
   | `NONE` (or unset) | `nowrap` | fixed (sizing) | fixed (sizing) |
   | `HEIGHT` | `pre-wrap` | fixed | `auto` |
   | `WIDTH_AND_HEIGHT` | `pre` | `auto` | `auto` |

## Trigger to do this

When designers report text being clipped or wrapped where it shouldn't be on a real frame. Until then the `pre-wrap` default is the better-of-two-defaults pick.
