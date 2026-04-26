# Designer onboarding

> Last reviewed: 2026-04-26

Welcome! This is the INDmoney Design System docs site. It surfaces the colors,
typography, spacing, motion, and iconography that make up our products.

## What lives where

- **Colors** — extracted from real production usage (currently the INDstocks V4
  Figma file). Each token shows light + dark mode side by side.
- **Typography** — sourced from the Glyph design system file (18 published
  TEXT styles). Font weights/sizes are still being dereferenced (v1.1).
- **Spacing / Motion / Iconography** — placeholder defaults from the Field DS
  fork until we have time to extract these systematically.

## How tokens stay fresh

You don't run sync — engineers do, on a cadence (or after a known design change).
The header shows when tokens were last synced. If the chip says ">7 days" and
your latest Figma changes aren't reflected, ping `@chetan` in Slack.

## Common operations

| You want to… | Do this |
|---|---|
| Find a token | ⌘K, type "primary text", "surface", "success" — token names + paths |
| Copy hex | Click any swatch — copies to clipboard, brief "COPIED" overlay |
| Copy CSS variable | ⌘K → select token → "Copy CSS var" |
| Download all tokens | Click **Download** in header → JSON / CSS / Swift / Android / Kotlin |
| Switch density (compact/comfortable) | Click `M`/`S`/`L` button in header |
| Switch theme | Click sun/moon button in header |
| Direct-link a section | URL hash auto-syncs; share `…/#color-surface` to land on Surface |

## What's a "pair card"?

Every semantic color shows two tiles: **light** and **dark**. The two hex values
are the actual colors used in the corresponding theme. Click either to copy.

If a token shows the same hex on both sides, it's a *mode-invariant* color —
it doesn't flip with theme (typically icons or borders that look right in both).

## Reporting issues

If a token looks wrong:
1. Note the token path (e.g. `colour.surface.action-card`)
2. Note the hex you see vs. what you expect
3. Drop in `#design-system` Slack — the engineer running sync iterates the
   classifier heuristics from there.

## Beyond v1

The plan calls for components docs (Buttons, Cards, Inputs) in v1.1, with
Figma Code Connect deep-links. Tickertape brand also planned. If you're
joining now, you're catching it early.
