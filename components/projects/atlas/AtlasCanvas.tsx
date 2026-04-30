"use client";

/**
 * U7 — react-three-fiber atlas canvas.
 *
 * Replaces the U6 PNG-grid placeholder. Each screen renders as a textured
 * plane positioned at its Figma section-relative `(x, y)`. Pan + zoom via
 * drei `<OrbitControls>` (rotate disabled). Hover scale spring + click-to-
 * snap callback (camera dolly) implemented inline below.
 *
 * Critical Next 16 mitigation:
 *   The pmndrs/react-three-fiber#3595 issue documents Next 16's component-
 *   Cache breaking r3f back/forward navigation: the cached vDOM holds
 *   references to disposed three.js objects. Wrapping our entry node in a
 *   `<Suspense fallback={…}>` with `key={pathname}` forces a fresh subtree
 *   on every navigation, sidestepping the cache. The `usePathname` hook
 *   participates in the Next 16 routing system, so the key flips reliably.
 *
 * SSR avoidance:
 *   Three.js inspects `window` at module top-level (via WebGLRenderer's
 *   feature-detect path), which throws under Node. ProjectShell imports
 *   THIS file via `next/dynamic({ ssr: false })` so we never resolve the
 *   import on the server. The `transpilePackages` entry in `next.config.ts`
 *   handles ESM compatibility.
 */

import {
  Canvas,
  useThree,
  type ThreeEvent,
} from "@react-three/fiber";
import { OrthographicCamera } from "@react-three/drei";
import { usePathname } from "next/navigation";
import {
  Suspense,
  useCallback,
  useEffect,
  useMemo,
  useRef,
} from "react";
import * as THREE from "three";
import type { Screen } from "@/lib/projects/types";
import { screenPngUrl } from "@/lib/projects/client";
import AtlasControls from "./AtlasControls";
import AtlasFrame from "./AtlasFrame";
import { useAtlasViewport } from "./useAtlasViewport";
import { totalBytes, TEXTURE_BUDGET_BYTES } from "./textureCache";

interface AtlasCanvasProps {
  /** URL slug — used by `screenPngUrl()` for the authed PNG route. */
  slug: string;
  /** All screens in the active version; one frame per screen. */
  screens: Screen[];
  /** Active version ID — keys the persisted-zoom localStorage entry. */
  versionID?: string;
  /** Click handler — ProjectShell switches the JSON tab to this screen. */
  onFrameSelect: (screenID: string) => void;
  /** ID of the currently selected screen (highlights the frame). */
  selectedScreenID?: string | null;
}

/** Camera dolly duration on click-to-snap (ms). */
const DOLLY_MS = 600;

/**
 * Inner scene component. Lives below the `<Canvas>` so r3f hooks (`useThree`)
 * can resolve. The default camera is the orthographic camera created here.
 */
function Scene({
  slug,
  screens,
  versionID,
  onFrameSelect,
  selectedScreenID,
}: AtlasCanvasProps) {
  const viewport = useAtlasViewport(slug, screens, versionID);
  const { camera, size } = useThree();

  // Animation handle for the click-to-snap dolly. We tween manually via
  // requestAnimationFrame inside a ref so we don't pull in GSAP — r3f's
  // useFrame would also work but the ref-based approach keeps this snap
  // independent from per-frame hover lerps.
  const dollyRafRef = useRef<number | null>(null);

  // Initial camera fit when screens change (e.g. version flip).
  useEffect(() => {
    if (!(camera instanceof THREE.OrthographicCamera)) return;
    if (screens.length === 0) return;
    viewport.fitToBounds(camera, screens, size.width, size.height);
  }, [screens, viewport, camera, size.width, size.height]);

  // Click-to-snap handler — tweens camera to fit the clicked frame.
  const handleSelect = useCallback(
    (screenID: string) => {
      onFrameSelect(screenID);
      const screen = screens.find((s) => s.ID === screenID);
      if (!screen) return;
      if (!(camera instanceof THREE.OrthographicCamera)) return;

      // Cancel any in-flight dolly so rapid clicks don't fight.
      if (dollyRafRef.current !== null) {
        cancelAnimationFrame(dollyRafRef.current);
        dollyRafRef.current = null;
      }

      const startX = camera.position.x;
      const startY = camera.position.y;
      const startZoom = camera.zoom;

      const targetX = screen.X + screen.Width / 2;
      const targetY = -(screen.Y + screen.Height / 2);
      // Fit the frame with 25% padding so the user has spatial context.
      const fitZoom = Math.min(
        size.width / (screen.Width * 1.5),
        size.height / (screen.Height * 1.5),
      );
      const targetZoom = Math.max(0.1, Math.min(4.0, fitZoom));

      const startTime = performance.now();
      const tick = () => {
        const elapsed = performance.now() - startTime;
        const t = Math.min(1, elapsed / DOLLY_MS);
        // Cubic ease-in-out per Animation Philosophy "click-to-snap" row.
        const eased =
          t < 0.5 ? 4 * t * t * t : 1 - Math.pow(-2 * t + 2, 3) / 2;
        camera.position.x = startX + (targetX - startX) * eased;
        camera.position.y = startY + (targetY - startY) * eased;
        camera.zoom = startZoom + (targetZoom - startZoom) * eased;
        camera.updateProjectionMatrix();
        if (t < 1) {
          dollyRafRef.current = requestAnimationFrame(tick);
        } else {
          dollyRafRef.current = null;
          viewport.persistZoom(targetZoom);
        }
      };
      dollyRafRef.current = requestAnimationFrame(tick);
    },
    [camera, onFrameSelect, screens, size.height, size.width, viewport],
  );

  // Cleanup any in-flight dolly on unmount.
  useEffect(() => {
    return () => {
      if (dollyRafRef.current !== null) {
        cancelAnimationFrame(dollyRafRef.current);
      }
    };
  }, []);

  // Texture budget watchdog. Phase 1 just logs; Phase 3 swaps to scale=1.
  useEffect(() => {
    const handle = window.setInterval(() => {
      const total = totalBytes();
      if (total > TEXTURE_BUDGET_BYTES) {
        // eslint-disable-next-line no-console
        console.warn(
          `[atlas] texture budget exceeded: ${(total / 1024 / 1024).toFixed(
            1,
          )} MB / ${(TEXTURE_BUDGET_BYTES / 1024 / 1024).toFixed(0)} MB`,
        );
      }
    }, 30_000);
    return () => window.clearInterval(handle);
  }, []);

  // Background click clears the selection — but we don't reset the camera.
  const onCanvasClick = useCallback((e: ThreeEvent<MouseEvent>) => {
    // The frame's onClick stops propagation, so any event reaching here is
    // a background click. Phase 1 keeps it as a no-op; future phases can
    // emit `onFrameSelect(null)` to deselect.
    void e;
  }, []);

  // Pre-compute per-frame URLs once so React doesn't recreate strings on
  // every render — small but adds up across 30+ frames.
  const frames = useMemo(
    () =>
      screens.map((s) => ({
        screen: s,
        url: screenPngUrl(slug, s.ID),
      })),
    [screens, slug],
  );

  return (
    <>
      <OrthographicCamera
        makeDefault
        position={viewport.initialPosition}
        zoom={viewport.initialZoom}
        near={0.1}
        far={1000}
      />
      <AtlasControls onZoomEnd={viewport.persistZoom} />
      <ambientLight intensity={1} />
      <group onClick={onCanvasClick}>
        {frames.map(({ screen, url }) => (
          <Suspense key={screen.ID} fallback={null}>
            <AtlasFrame
              screen={screen}
              pngUrl={url}
              onSelect={handleSelect}
              selected={selectedScreenID === screen.ID}
            />
          </Suspense>
        ))}
      </group>
    </>
  );
}

/**
 * Dynamic-imported entry. Keyed by `pathname` so Next 16's componentCache
 * cannot replay a stale r3f tree on back/forward navigation.
 */
export default function AtlasCanvas(props: AtlasCanvasProps) {
  const pathname = usePathname() ?? "/";

  return (
    <Suspense
      key={pathname}
      fallback={
        <div
          aria-hidden
          style={{
            display: "grid",
            placeItems: "center",
            height: "100%",
            color: "var(--text-3)",
            fontFamily: "var(--font-mono)",
            fontSize: 12,
          }}
        >
          Loading atlas…
        </div>
      }
    >
      <Canvas
        // Orthographic projection set on the inner camera, not Canvas itself,
        // so we can call `makeDefault` and let drei track the live camera.
        dpr={[1, 2]}
        gl={{ antialias: true, alpha: true, preserveDrawingBuffer: false }}
        // The atlas paints over a section-tinted background defined by the
        // outer DOM div; alpha=true lets that show through.
        style={{ width: "100%", height: "100%" }}
        // Listen for pointer events on the Canvas surface, not just meshes,
        // so the OrbitControls pan handler always sees the gesture.
        eventPrefix="client"
      >
        <Scene {...props} />
      </Canvas>
    </Suspense>
  );
}
