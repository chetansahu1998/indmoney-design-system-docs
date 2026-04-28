---
title: Glyph file → atom / molecule / parent tier mapping
created: 2026-04-28
status: research
---

# Glyph design system — page + tier mapping

This document captures the actual structure of the Glyph file
(`Glyph- Design System`) so the extractor and the docs site can stop
treating it as a flat list of components.

## TL;DR

The 89 entries our manifest currently surfaces under `/components` are
**atoms**, not parent components. Designers compose them into parents on
a different page. The docs site should show parents (the things they
hand to engineers) and let designers drill down to the atoms each parent
is built from.

| Tier | What it is | Lives on | Count (today) |
|------|------------|----------|---------------|
| Atom | Atomic primitives — single Buttons, Inputs, Chips, etc. | `Atoms ` page | 110 COMPONENT_SETs |
| Molecule | Patterns composed of atoms — Bottomsheet headers, list rows, containers | `Bottom Sheet` page | 12 COMPONENT_SETs |
| Parent (Organism) | Final consumed components — Toast, Status Bar, Footer CTA, Bottom Nav | `Design System 🌟` page | 30 COMPONENT_SETs |

## Pages in the file

```
52:8375    Cover Page          — branding cover, no components
87:7405    Design System 🌟     — PARENT components (organisms)
1583:36915 Atoms                — ATOM primitives (current /components)
1440:29094 Icons Fresh          — icon library (already extracted to /icons)
4227:31100 Onboarding tour      — flow scratchpad
3466:24922 Bottom Sheet         — MOLECULE patterns
3715:54655 Dump                 — scratch
32:7432    Colours              — design tokens (already extracted)
688:379    Page Structure       — typography reference
7060:77883 Guidelines           — text guidance
```

## Tier 1 — Atoms (`Atoms ` page, 1583:36915)

Top-level layout: 18 SECTIONs by name, each grouping a category of atomic
primitives. Inside each SECTION, COMPONENT_SETs are the atoms.

Sections:
- Search · Input Field · List · Buttons · Chips & Filters · Market Ticker
- Bottom Nav · Sticky Nav · Toggles · Timeline · Sliders · Search · Badge
- Footer · Review and add · Masthead Atoms · ...

Today's manifest extracts 89 components from this page (filtered down
from 110 — some get classified as logos / illustrations by the kind
heuristic). These are **atoms**, e.g. "1 CTA" (a single button shape),
"Bottom strip" (a Masthead component piece).

## Tier 2 — Molecules (`Bottom Sheet` page, 3466:24922)

Top-level layout: 12 COMPONENT_SETs, no SECTIONs.

Names:
```
Illustrations
Header Bottomsheet-Left Align
List- 343
List-Container
Container w Illustration
Bottomsheet_Body
Default-Right Aligned
Header Bottomsheet-Selection
Selection List
Selection
Header Bottomsheet-Center Aligned
Body- Center Aligned
```

These are mid-tier patterns. A "Header Bottomsheet-Left Align" is itself
not the final Bottom Sheet — it's the header chunk that the parent
Bottom Sheet consumes. They probably reference atoms (icons, type, list
rows) via INSTANCE.

## Tier 3 — Parent components (`Design System 🌟` page, 87:7405)

Top-level layout: 32 SECTIONs, each containing 1 COMPONENT_SET (the
parent) plus a frame thumbnail and a couple TEXT labels. 30 distinct
parents:

```
Badges                  Search bar              Input Field Final
List 343                List 311                notebox
Overlay                 Filters and Tabs        Chips Final
Separators              Keyboard                Checkmarks
Radio button            Bottom Nav              Sliders Final
Single Button Set       Footer CTA              Market Ticker
Progress Bar            Footers                 Status Bar
Sticky Nav              vertical timeline       Nudges 343
Nudges 311              Toast Messages          Masthead/Hot
Masthead/Cold           Toggle Final            scroller
```

These ARE what should appear at `/components`.

## The composition graph (parent → atoms)

Each parent is a COMPONENT_SET. Each variant within the set is a
COMPONENT whose tree contains INSTANCE nodes. Each INSTANCE has a
`componentId` field pointing at the atom (or molecule) it embeds.

### Worked example — `Footer CTA` (1625:49710)

3 variants. The "No. of Buttons=2 Buttons" variant references 6 atoms:

| Instance name in parent | componentId | Likely atom slug |
|-------------------------|-------------|------------------|
| Action Bar_2CTA | 1625:47535 | (atom on Atoms page) |
| Top Strip | 784:15958 | (atom on Atoms page) |
| 2 CTA_Horizontal | 1625:46637 | `2-cta-horizontal` ✓ already in manifest |
| Footer_Button | 4494:66845 | (atom on Atoms page) |
| Footers | 1437:29061 | atom |
| Footer Text | 1437:29040 | atom |

So we can resolve each parent variant to the list of atoms it composes,
then render that as a "Built from" rail beneath the variant in the docs
detail panel.

### Resolution rule

Walk variant tree → collect every node where `type == "INSTANCE"`. Pull
`componentId`. Look up that id in the atom manifest:
1. Match against `set_id` for COMPONENT_SET-level instances
2. Match against any variant's `variant_id` for COMPONENT-level instances
3. If not found in atoms, may be a Bottom Sheet molecule — keep the raw
   name and `componentId` as a "external instance" reference

## What the manifest needs to add

```ts
interface IconEntry {
  // existing fields…
  tier?: "atom" | "molecule" | "parent";
  page?: string;             // "Atoms ", "Design System 🌟", "Bottom Sheet"
  page_id?: string;          // "1583:36915", "87:7405", "3466:24922"

  variants?: VariantEntry[];
}

interface VariantEntry {
  // existing fields…
  /** Per-variant composition — every INSTANCE in this variant's tree. */
  composes?: CompositionRef[];
}

interface CompositionRef {
  /** What the parent calls this slot ("Trailing Icon", "Title Text"). */
  instance_name: string;
  /** Figma node id this instance points to. */
  component_id: string;
  /** Resolved atom slug if the componentId matches an atom we extracted. */
  atom_slug?: string;
  /** Tier of the resolved component when known. */
  resolved_tier?: "atom" | "molecule" | "parent";
}
```

## What the extractor needs to do

Today: walks the Atoms page only, classifies COMPONENT_SETs by SECTION
ancestry, downloads variant SVGs.

Need:
1. Walk **every** page in the file (not just Atoms).
2. Stamp `tier` based on which page a COMPONENT_SET lives on:
   - on `Atoms ` page → tier=atom
   - on `Design System 🌟` page → tier=parent
   - on `Bottom Sheet` page → tier=molecule
3. For tier=parent entries, walk each variant's tree, collect every
   INSTANCE node, and emit `composes[]` with `instance_name` +
   `component_id`.
4. Post-pass: resolve `component_id` → `atom_slug` by cross-referencing
   the atoms table (set_id and per-variant variant_id).
5. Dedupe: an INSTANCE that wraps a COMPONENT_SET reference shows up
   with that set's id; an INSTANCE that wraps a specific variant points
   at the variant's id. Both are valid; surface both forms.

## What the UI needs to do

`/components` filters `iconsByKind("component")` further to
`tier === "parent"`. Atoms get their own browse tab (`/atoms`?) so
designers can still drill into primitives, but the default landing is
parent components.

**Detail panel contents per parent — ordered as a designer would read it:**

1. Hero — large render of default variant + name + description + axis
   pills + tier badge ("Parent · 6 axes")
2. **All variants** — every variant of the parent's COMPONENT_SET
   rendered as a clickable card. Each card shows:
   - The variant render at native size
   - Its axis tuple (`Size=343 · State=Active`)
   - A "default" star when `is_default`
   - Bound-variable count chip
3. **Per-variant deep output** — clicking a variant card expands it
   inline (or pins it as the "active variant" in a dedicated panel
   beside the variant grid) to surface:
   - **Properties** — axis values for this variant + scalar prop values
     (Boolean toggles, Text overrides, Instance swaps actually applied)
   - **Layout** — this variant's autolayout config (mode, padding, gap,
     alignment, sizing) — *captured per variant since variants can
     differ in padding, e.g. small vs large size*
   - **Appearance** — fills, strokes, effects, corner radius for this
     variant's root frame, with Variable bindings called out
   - **Structure** — first-level children (TEXT, INSTANCE, FRAME) with
     property-cascade refs
   - **Built from** — every INSTANCE in this variant's tree, resolved to
     atom thumbnails and names. This is the new piece — it makes the
     atom→parent composition visible inline with the variant's own spec
     so a designer can see "this Footer CTA, in the 2-button variant,
     uses these 6 atoms".
4. Code snippet generation (existing, unchanged).

Variants are **not** summarised into a single section any more — each
one is fully addressable, with its own props/layout/appearance/structure/
composition because the variants can and do differ on every axis.

## Variant-detail data shape (already captured, just needs UI)

The Phase A extraction already stamps per-variant `layout`, `fills`,
`strokes`, `effects`, `corner`, `bound_variables`, and `children` on
every variant. The only addition the rewrite needs is the per-variant
`composes[]` field for "Built from" — everything else the UI just needs
to render.

## Open questions before we ship

- Should molecules (Bottom Sheet items) be a separate top-level tab, or
  folded into the parent's "Built from" view as second-tier nodes?
- Some Atoms-page sets are clearly molecule-grade (Masthead Atoms — the
  full Masthead frame is a parent on Design System page; "Bottom strip"
  on Atoms is a molecule that the parent Masthead consumes). The page-
  based tier rule misclassifies these. Do we trust page placement, or
  add a secondary heuristic (set name starts with "Masthead/" → parent)?
- Five atoms with single variants (`single_variant_set: true`) may
  actually be parents that simply weren't published as a set. Audit
  separately.
