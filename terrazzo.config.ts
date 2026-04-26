/**
 * Terrazzo configuration — compiles W3C-DTCG JSON tokens to CSS + TS.
 *
 * Inputs:  lib/tokens/<brand>/{base,semantic,text-styles}.tokens.json
 * Outputs: lib/tokens/.generated/{tokens.ts, tokens.css}
 *
 * Wired into the Next.js build via the `prebuild` script in package.json.
 *
 * The generated files are gitignored — they regenerate on every build, scoped
 * to NEXT_PUBLIC_BRAND so each Vercel project gets its own brand's tokens.
 */
import { defineConfig } from "@terrazzo/cli";
import css from "@terrazzo/plugin-css";
import js from "@terrazzo/plugin-js";

const brand = process.env.NEXT_PUBLIC_BRAND ?? "indmoney";

export default defineConfig({
  tokens: [
    `./lib/tokens/${brand}/base.tokens.json`,
    `./lib/tokens/${brand}/semantic.tokens.json`,
    // text-styles deferred — Field's defaults use unresolved {font.heading.h40.size}-style
    // aliases. Phase 5b will replace with Glyph-extracted values once /nodes dereferencing lands.
    // `./lib/tokens/${brand}/text-styles.tokens.json`,
  ],
  outDir: "./lib/tokens-generated/",
  plugins: [
    js({ filename: "index.js" }),
    css({ filename: "tokens.css" }),
  ],
});
