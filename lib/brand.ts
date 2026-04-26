/**
 * Brand union — single source of truth for which brands this docs site can render.
 *
 * Add a new brand by:
 *   1. Pushing the slug into BRANDS below (lowercase, kebab-safe).
 *   2. Adding the human label to BRAND_LABELS.
 *   3. Adding `lib/tokens/<brand>/{base,semantic,text-styles}.tokens.json`.
 *   4. Setting `NEXT_PUBLIC_BRAND=<brand>` on the corresponding Vercel project.
 *
 * Do NOT add per-brand `if (brand === "X")` branches in component code.
 * Brand-conditional rendering belongs in `lib/brand-copy/<brand>.ts` resolvers.
 * See the lint guard in scripts/grep-no-brand-equality.sh.
 */

export const BRANDS = ["indmoney", "tickertape"] as const;

export type Brand = (typeof BRANDS)[number];

export const BRAND_LABELS: Record<Brand, string> = {
  indmoney: "INDmoney",
  tickertape: "Tickertape",
};

export function isBrand(x: unknown): x is Brand {
  return typeof x === "string" && (BRANDS as readonly string[]).includes(x);
}

export function assertBrand(x: unknown): asserts x is Brand {
  if (!isBrand(x)) {
    throw new Error(
      `Invalid brand: ${JSON.stringify(x)}. Must be one of: ${BRANDS.join(", ")}`,
    );
  }
}

/** Resolve at module load. Call sites should import this, not re-read the env. */
export function currentBrand(): Brand {
  const raw = process.env.NEXT_PUBLIC_BRAND ?? "indmoney";
  assertBrand(raw);
  return raw;
}

export function brandLabel(brand: Brand): string {
  return BRAND_LABELS[brand];
}
