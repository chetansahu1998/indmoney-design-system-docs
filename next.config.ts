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
  transpilePackages: ["three", "@react-three/fiber", "@react-three/drei"],
};
export default nextConfig;
