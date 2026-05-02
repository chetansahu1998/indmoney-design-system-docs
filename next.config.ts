import type { NextConfig } from "next";

/**
 * Next.js config for the docs-site.
 *
 * `transpilePackages` lists ESM-only packages that ship un-transpiled and
 * need Next's bundler to compile them. The r3f trio (three / fiber / drei)
 * lives here so the dynamic-imported `<AtlasCanvas>` (U7) builds cleanly on
 * Next 16.
 */
const nextConfig: NextConfig = {
  transpilePackages: [
    "three",
    "@react-three/fiber",
    "@react-three/drei",
    // Phase 6 — react-force-graph-3d wraps three.js + d3-force-3d and ships
    // ESM-only with peer deps that don't resolve cleanly on Next 16 without
    // explicit transpilation. three-spritetext is a transitive ESM dep used
    // for HTML-style labels.
    "react-force-graph-3d",
    "three-spritetext",
  ],
  // Phase 9 U1 — auto-wrap router navigation in document.startViewTransition
  // so cross-route morphs work via the browser-native View Transitions API
  // (CSS view-transition-name on source + target). React 19.2.4 stable does
  // NOT export <ViewTransition> (that's Canary); we use the CSS property +
  // this flag instead. Source: docs/plans/2026-05-02-003-feat-atlas-
  // integration-seams-plan.md U1.
  experimental: {
    viewTransition: true,
  },
};
export default nextConfig;
