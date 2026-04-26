# Ported from Field Design System

This repository was forked from [`rjaiswal-design/field-design-system-docs`](https://github.com/rjaiswal-design/field-design-system-docs) at seed commit `a035c17` (2026-04, latest at fork time).

We extend Field's chassis (Next.js 16 + Tailwind v4 + Radix + Framer Motion + cmdk) with:

- **Multi-brand awareness** (`NEXT_PUBLIC_BRAND` env-driven, brand-aware metadata + chrome)
- **Figma sync** via Go extractor (`services/ds-service/`) targeting INDstocks V4 + Glyph
- **Auth/RBAC service** reused from [DesignBrain-AI](https://github.com/.../DesignBrain-AI) (seed `a6b8c7daa95cafa4fc143411236a1e3a67e25f4d`) — locally hosted via launchd, exposed via Cloudflare Tunnel
- **W3C-DTCG token compilation** via [Terrazzo](https://terrazzo.app)
- **Section components edited in place** to reflect INDmoney's actual semantic model

## File-by-file inventory (from Field)

| Path | Status | Notes |
|---|---|---|
| `app/layout.tsx` | edited | brand-aware metadata |
| `app/page.tsx` | unchanged | renders DocsShell |
| `app/globals.css` | will edit | tokens.css imported (generated) |
| `components/DocsShell.tsx` | will edit | + brand switcher chrome, sync chip |
| `components/Header.tsx` | will edit | + last-synced badge + sync button |
| `components/Sidebar.tsx` | unchanged | |
| `components/SearchModal.tsx` | will edit | cmdk listener replaced by `useKeyboardShortcuts` hook |
| `components/Footer.tsx` | unchanged | |
| `components/sections/{Color,Typography,Spacing,Motion,Iconography}Section.tsx` | will edit in place | reads SEMANTIC tokens only, `data-token=` annotations |
| `components/ui/SectionHeading.tsx` | unchanged | |
| `lib/base.tokens.json` | replaced | sourced from Figma extraction |
| `lib/semantic.tokens.json` | replaced | sourced from Figma extraction |
| `lib/text-styles.tokens.json` | replaced | sourced from Figma extraction (Glyph styles) |
| `lib/tokens.ts` | replaced (generated) | written by Terrazzo, NEVER hand-edit |
| `lib/icons.ts` | unchanged | |
| `lib/motion-variants.ts` | unchanged | |
| `lib/use-mobile.ts` | unchanged | |
| `lib/utils.ts` | unchanged | |
| `next-env.d.ts` | edited | adds `Brand` typing for `NEXT_PUBLIC_BRAND` |
| `next.config.ts` | unchanged | |
| `package.json` | edited | name + sync scripts + zod |
| `tailwind.config.*` | unchanged | (Tailwind v4 zero-config) |
| `tsconfig.json` | unchanged | |
| `components.json` | unchanged | shadcn config |

## Files marked `*` after port edit

(none yet — initial commit is the seed)

## Update strategy

If upstream Field ships meaningful improvements, decide **case-by-case** whether to backport or accept divergence. There is no automatic upstream sync.
