"use client";

/**
 * One textured plane in the U7 atlas — represents a single Figma screen.
 *
 * Behaviour:
 *   - Loads the screen's PNG via the singleton textureCache so theme
 *     toggles don't refetch on roundtrip.
 *   - Hover scale spring 1.0 → 1.015 over ~200ms via `useFrame` lerp.
 *     `useFrame` is the right hook for r3f animations because it ticks
 *     inside the renderer's RAF loop; GSAP/CSS transitions don't drive
 *     three.js mesh transforms.
 *   - Click emits `onSelect(screenID)` so ProjectShell can route the JSON
 *     tab to that screen.
 *   - Coordinate flip: Figma Y grows down; three.js Y grows up. We negate
 *     the position's Y component so frames lay out the way they look in
 *     Figma. The texture itself is drawn upright because TextureLoader
 *     decodes PNGs in image-space (top-left origin); we don't flip the UV.
 */

import { useFrame } from "@react-three/fiber";
import { useEffect, useRef, useState } from "react";
import * as THREE from "three";
import type { Screen } from "@/lib/projects/types";
import { getTexture } from "./textureCache";

interface AtlasFrameProps {
  screen: Screen;
  pngUrl: string;
  onSelect: (screenID: string) => void;
  /** True while the frame is the active selection (renders an outline). */
  selected?: boolean;
}

/** Hover scale factor — Animation Philosophy: subtle 1.5% bump. */
const HOVER_SCALE = 1.015;
/**
 * Per-frame lerp factor toward target scale. ~0.18 settles to within a few
 * percent in ~12 frames at 60fps (~200ms), matching the spec without bringing
 * a spring library into the bundle.
 */
const SCALE_LERP = 0.18;

export default function AtlasFrame({
  screen,
  pngUrl,
  onSelect,
  selected = false,
}: AtlasFrameProps) {
  const meshRef = useRef<THREE.Mesh>(null);
  const [hovered, setHovered] = useState(false);
  const [loaded, setLoaded] = useState(false);
  const [errored, setErrored] = useState(false);
  const textureRef = useRef<THREE.Texture | null>(null);

  // Resolve the texture once per URL — the cache layer dedupes concurrent
  // fetches and survives theme toggles.
  useEffect(() => {
    setLoaded(false);
    setErrored(false);
    const tex = getTexture(
      pngUrl,
      () => setLoaded(true),
      () => setErrored(true),
    );
    textureRef.current = tex;
    if (tex.image) setLoaded(true);
    // We intentionally do NOT dispose on unmount — the cache is shared and
    // theme toggles re-enter this effect. Phase 3 ships LRU eviction.
  }, [pngUrl]);

  // Hover scale tween — runs every frame; cheap.
  useFrame(() => {
    const m = meshRef.current;
    if (!m) return;
    const target = hovered ? HOVER_SCALE : 1;
    const next = THREE.MathUtils.lerp(m.scale.x, target, SCALE_LERP);
    m.scale.set(next, next, 1);
  });

  // Position: center the mesh on its (x, y) Figma origin. Plane geometry is
  // unit-sized at 1×1 with origin at center; we scale by Width/Height and
  // translate by the screen's top-left + half-extent.
  const cx = screen.X + screen.Width / 2;
  const cy = screen.Y + screen.Height / 2;
  // Negate Y to convert Figma's down-positive into three.js up-positive.
  const position: [number, number, number] = [cx, -cy, 0];

  return (
    <mesh
      ref={meshRef}
      position={position}
      scale={[1, 1, 1]}
      onPointerOver={(e) => {
        e.stopPropagation();
        setHovered(true);
        document.body.style.cursor = "pointer";
      }}
      onPointerOut={(e) => {
        e.stopPropagation();
        setHovered(false);
        document.body.style.cursor = "";
      }}
      onClick={(e) => {
        e.stopPropagation();
        onSelect(screen.ID);
      }}
      // Frames are not interactive surfaces for keyboard yet (Phase 1 is
      // mouse/touch only); R3 ships keyboard a11y.
    >
      <planeGeometry args={[screen.Width, screen.Height]} />
      {errored ? (
        // Broken-PNG placeholder — distinct red so it's obvious in QA.
        <meshBasicMaterial color="#ff5555" />
      ) : loaded && textureRef.current ? (
        <meshBasicMaterial
          map={textureRef.current}
          // Selected outline via emissive bump — orthographic + basic
          // material doesn't support outlines natively; brightness shift
          // is the cheapest legible cue without a postprocess pass.
          color={selected ? "#ffffff" : hovered ? "#f4f4f4" : "#e8e8e8"}
          transparent={false}
        />
      ) : (
        // Loading placeholder — neutral wireframe-ish surface so the user
        // sees the frame slot before texture decode finishes.
        <meshBasicMaterial color="#1a1a1a" wireframe />
      )}
    </mesh>
  );
}
