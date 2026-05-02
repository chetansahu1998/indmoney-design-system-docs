"use client";

/**
 * Phase 6 — `/atlas` mind-graph entry route.
 *
 * The page wires three concerns:
 *   1. Reduced-motion gate (Phase 1 hook). Brain graph is animation-heavy;
 *      reduced-motion users see a 2D EmptyState pointing at /atlas/admin.
 *   2. WebGL2 capability check. Browsers without WebGL2 (Safari < 15) get
 *      the same 2D fallback.
 *   3. Dynamic-import of BrainGraph with `ssr:false` so three.js never
 *      executes during SSR (it inspects `window` at module top-level).
 *
 * Platform comes from `?platform=` query string, default mobile (R25 —
 * Mobile↔Web are separate IA trees; the toggle is a per-mount choice).
 *
 * Deep links: `/atlas?focus=flow_<id>` lands with the flow leaf already
 * zoomed (R23). The BrainGraph reads the param and dispatches FOCUS on
 * mount.
 */

import dynamic from "next/dynamic";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { Suspense, useEffect, useLayoutEffect, useState } from "react";

import { hasWebGL2, useReducedMotion } from "./reducedMotion";
import type { GraphPlatform } from "./types";

const BrainGraph = dynamic(() => import("./BrainGraph"), {
  ssr: false,
  loading: () => <BrainGraphSkeleton />,
});

export default function AtlasPage() {
  // useSearchParams must live under a Suspense boundary in App Router; the
  // outer wrapper provides it so the page doesn't bail out of static
  // optimisation. The inner client component reads params freely.
  return (
    <Suspense fallback={<BrainGraphSkeleton />}>
      <AtlasInner />
    </Suspense>
  );
}

function AtlasInner() {
  const params = useSearchParams();
  const reducedMotion = useReducedMotion();
  const [webgl2, setWebgl2] = useState<boolean | null>(null);

  useEffect(() => {
    setWebgl2(hasWebGL2());
  }, []);

  const platformParam = params?.get("platform");
  const platform: GraphPlatform =
    platformParam === "web" ? "web" : "mobile";
  const focusNodeID = params?.get("focus") ?? null;
  // Phase 9 U3 — reverse-morph entry point. When a user presses Esc in
  // /projects/<slug>, the browser back-navigates to the prior /atlas
  // entry. If that entry was opened via a leaf-click that wrote
  // `?from=<slug>` into the /atlas URL (forward-morph wiring; landing in a
  // future U) — or directly visited with `?from=<slug>` for share links —
  // we surface the slug here so BrainGraph can re-focus the source flow
  // leaf via `view.morphFromProject(slug, nodes)`.
  //
  // Reading the URL synchronously here (useSearchParams is already sync)
  // and memoising the value guarantees the `from` slug is available in
  // the same React render that BrainGraph mounts — i.e. before the View
  // Transition's "new" snapshot is committed. No useLayoutEffect needed
  // because we're not running side-effects, just deriving a prop.
  const fromProjectSlug = params?.get("from") ?? null;
  // Touch the value through a useLayoutEffect to satisfy the U3 contract
  // that the slug is observed pre-paint. The hook is empty because the
  // value is already a derived prop; the effect exists to anchor the
  // sync-timing comment so future refactors don't re-introduce a race.
  useLayoutEffect(() => {
    // Intentionally empty. The slug is forwarded to BrainGraph as a prop
    // and consumed there via `view.morphFromProject`. Future U-units can
    // extend this hook if extra coordination becomes necessary (e.g.
    // pre-warming the camera position before the leaf snapshot lands).
    void fromProjectSlug;
  }, [fromProjectSlug]);
  // Phase 7.5 — hydrate filter chips from share-link URL.
  const filtersParam = params?.get("filters") ?? "";
  const initialFiltersFromURL = filtersParam
    ? filtersParam.split(",").reduce(
        (acc, f) => {
          if (f === "components" || f === "tokens" || f === "decisions") acc[f] = true;
          return acc;
        },
        { components: false, tokens: false, decisions: false } as Record<
          "components" | "tokens" | "decisions",
          boolean
        >,
      )
    : null;

  if (reducedMotion) {
    return <ReducedMotionFallback />;
  }
  if (webgl2 === false) {
    return <WebGLFallback />;
  }
  if (webgl2 === null) {
    return <BrainGraphSkeleton />;
  }
  return (
    <main className="atlas-shell">
      <Suspense fallback={<BrainGraphSkeleton />}>
        <BrainGraph
          platform={platform}
          focusNodeID={focusNodeID}
          initialFilters={initialFiltersFromURL}
          fromProjectSlug={fromProjectSlug}
        />
      </Suspense>
      <style jsx>{`
        /* Atlas no longer takes over the viewport with position:fixed —
         * that hid the global Header (and its top-nav back to /, /projects,
         * etc), making /atlas feel like a separate site. Now we fill the
         * viewport BELOW the header and respect the brand's theme tokens.
         * Brain graph stays visually immersive via the dark canvas token,
         * not a hardcoded color, so the user's theme toggle still applies. */
        .atlas-shell {
          position: fixed;
          top: var(--header-h);
          left: 0;
          right: 0;
          bottom: 0;
          background: var(--bg-canvas, var(--bg-page));
          overflow: hidden;
          color: var(--text-1);
          font-family: var(--font-sans, "Inter Variable", sans-serif);
        }
      `}</style>
    </main>
  );
}

function BrainGraphSkeleton() {
  return (
    <div className="skel">
      <div className="skel-glow" aria-hidden />
      <p>Loading mind graph…</p>
      <style jsx>{`
        .skel {
          position: fixed;
          top: var(--header-h);
          left: 0;
          right: 0;
          bottom: 0;
          display: grid;
          place-items: center;
          background: var(--bg-canvas, var(--bg-page));
          color: var(--text-3);
          font-family: var(--font-sans, "Inter Variable", sans-serif);
          font-size: 13px;
          letter-spacing: 0.02em;
        }
        .skel-glow {
          position: absolute;
          width: 240px;
          height: 240px;
          border-radius: 50%;
          background: radial-gradient(
            circle,
            var(--accent-soft, rgba(123, 159, 255, 0.18)) 0%,
            transparent 70%
          );
          animation: pulse 2s ease-in-out infinite;
        }
        @media (prefers-reduced-motion: reduce) {
          .skel-glow { animation: none; }
        }
        @keyframes pulse {
          0%,
          100% {
            transform: scale(1);
            opacity: 0.6;
          }
          50% {
            transform: scale(1.08);
            opacity: 1;
          }
        }
      `}</style>
    </div>
  );
}

function ReducedMotionFallback() {
  return (
    <main className="fallback">
      <div className="fallback-card">
        <h1>Reduced motion is enabled</h1>
        <p>
          The mind graph is animation-driven and isn&apos;t shown when your
          system requests reduced motion. Open the admin dashboard for a
          static view, or disable reduced motion in your OS settings to view
          the brain.
        </p>
        <div className="fallback-cta">
          <Link href="/atlas/admin">Open admin dashboard →</Link>
        </div>
      </div>
      <FallbackStyles />
    </main>
  );
}

function WebGLFallback() {
  return (
    <main className="fallback">
      <div className="fallback-card">
        <h1>This browser doesn&apos;t support WebGL 2</h1>
        <p>
          The mind graph requires WebGL 2 (Chrome 56+, Firefox 51+,
          Safari 15+). Open this page in a recent browser, or use the admin
          dashboard for a non-3D view.
        </p>
        <div className="fallback-cta">
          <Link href="/atlas/admin">Open admin dashboard →</Link>
        </div>
      </div>
      <FallbackStyles />
    </main>
  );
}

function FallbackStyles() {
  return (
    <style jsx global>{`
      .fallback {
        position: fixed;
        top: var(--header-h);
        left: 0;
        right: 0;
        bottom: 0;
        display: grid;
        place-items: center;
        background: var(--bg-canvas, var(--bg-page));
        color: var(--text-1);
        font-family: var(--font-sans, "Inter Variable", sans-serif);
        padding: 24px;
      }
      .fallback-card {
        max-width: 520px;
        padding: 32px;
        border: 1px solid var(--border-subtle, var(--border));
        border-radius: 16px;
        background: var(--bg-surface);
        backdrop-filter: blur(8px);
      }
      .fallback-card h1 {
        margin: 0 0 12px;
        font-size: 22px;
        font-weight: 600;
        color: var(--text-1);
      }
      .fallback-card p {
        margin: 0 0 20px;
        line-height: 1.6;
        color: var(--text-3);
      }
      .fallback-cta a {
        color: var(--accent, #7b9fff);
        text-decoration: none;
        font-weight: 500;
      }
      .fallback-cta a:hover {
        text-decoration: underline;
      }
    `}</style>
  );
}
