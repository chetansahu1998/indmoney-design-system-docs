"use client";

/**
 * Pan + zoom controls for the U7 atlas.
 *
 * Wraps drei's `<OrbitControls>` with a configuration tuned for a 2D
 * orthographic plane:
 *
 *   - `enableRotate={false}`   — the camera stays orthogonal to the plane;
 *                                rotation would tilt the textured frames
 *                                and break the "Figma map" mental model.
 *   - `enableZoom`             — wheel + pinch zoom.
 *   - All mouse buttons → PAN — left/middle/right all pan, so users with
 *                                trackpads + mice both work without modifier
 *                                keys. (The default is left=ROTATE which we
 *                                disabled, leaving an unbound left button.)
 *   - `minZoom` / `maxZoom`    — keep the user inside a sensible range so
 *                                they can't accidentally zoom into oblivion
 *                                via a stray pinch on a sensitive trackpad.
 *
 * The component re-uses the `<Canvas>`'s default camera (set as `makeDefault`
 * inside `AtlasCanvas`). drei picks it up via the r3f `useThree` hook.
 */

import { OrbitControls } from "@react-three/drei";
import * as THREE from "three";
import { useEffect, useRef } from "react";
import type { OrbitControls as OrbitControlsImpl } from "three-stdlib";

interface AtlasControlsProps {
  /** Optional callback fired when the user finishes a zoom gesture. */
  onZoomEnd?: (zoom: number) => void;
  /** Lower zoom bound. Default 0.1 (very far out). */
  minZoom?: number;
  /** Upper zoom bound. Default 4.0 (4x native pixel density). */
  maxZoom?: number;
}

export default function AtlasControls({
  onZoomEnd,
  minZoom = 0.1,
  maxZoom = 4.0,
}: AtlasControlsProps) {
  const ref = useRef<OrbitControlsImpl | null>(null);

  // Wire onZoomEnd via OrbitControls' `end` event — there's no dedicated
  // "zoom end" hook, but `end` fires for any gesture termination (pan or
  // zoom). We read camera.zoom inside the handler so we always get the
  // post-gesture value.
  useEffect(() => {
    if (!onZoomEnd) return;
    const controls = ref.current;
    if (!controls) return;
    const handler = () => {
      const cam = controls.object as THREE.OrthographicCamera;
      onZoomEnd(cam.zoom);
    };
    controls.addEventListener("end", handler);
    return () => {
      controls.removeEventListener("end", handler);
    };
  }, [onZoomEnd]);

  return (
    <OrbitControls
      ref={ref}
      enableRotate={false}
      enableZoom
      enablePan
      minZoom={minZoom}
      maxZoom={maxZoom}
      // Pan-only mouse mapping. The default would rotate on left-button,
      // which is meaningless on a flat orthographic plane.
      mouseButtons={{
        LEFT: THREE.MOUSE.PAN,
        MIDDLE: THREE.MOUSE.PAN,
        RIGHT: THREE.MOUSE.PAN,
      }}
      touches={{
        ONE: THREE.TOUCH.PAN,
        TWO: THREE.TOUCH.DOLLY_PAN,
      }}
      // Damping makes pan/zoom feel smoothed; keep modest so it doesn't drift.
      enableDamping
      dampingFactor={0.15}
      // Z is fixed (we look down +Z onto the XY plane), so a screen-space
      // pan in world units suffices.
      screenSpacePanning
    />
  );
}
