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

import { useFrame, useThree } from "@react-three/fiber";
import { useEffect, useMemo, useRef, useState } from "react";
import * as THREE from "three";
import type { Screen } from "@/lib/projects/types";
import { getTexture, getTextureKTX2OrPNG } from "./textureCache";
import { lodURL, pickLOD, type LODTier } from "./lod/pickLOD";
import {
  classify as classifyViewport,
  type FrameBounds,
  type ViewportRingTier,
} from "./lod/viewportRing";

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
  // Phase 3.5 U3: tier currently routes to baseURL via lodURL — when
  // backend tier generation lands the URL string flips by tier without
  // touching this component. We poll camera zoom + viewport on every
  // frame and re-route the URL when the tier transition crosses a
  // threshold.
  const { camera, gl } = useThree();
  const [tier, setTier] = useState<LODTier>("full");
  const resolvedURL = useMemo(() => lodURL(pngUrl, tier), [pngUrl, tier]);
  // Phase 3.5 follow-up #3: HOT/WARM viewport ring. cold frames skip
  // texture load + don't render; warm frames pre-load + render at
  // opacity 0; hot frames render normally. Adopted from DesignBrain's
  // ViewportCuller pattern.
  const [ringTier, setRingTier] = useState<ViewportRingTier>("hot");

  // Resolve the texture once per URL — the cache layer dedupes concurrent
  // fetches and survives theme toggles. Phase 3.5 follow-up: try KTX2
  // first via the basisu-transcoded sidecar; fall back to PNG when
  // KTX2 is absent.
  //
  // Phase 3.5 follow-up #3: cold frames skip the fetch entirely. They
  // re-enter the load cycle when the viewport ring promotes them back
  // to warm/hot — re-running this effect via the ringTier dependency.
  useEffect(() => {
    if (ringTier === "cold") return;
    setLoaded(false);
    setErrored(false);
    let cancelled = false;
    void getTextureKTX2OrPNG(
      resolvedURL,
      (tex) => {
        if (cancelled) return;
        textureRef.current = tex;
        setLoaded(true);
      },
      () => {
        if (!cancelled) setErrored(true);
      },
    ).catch((err) => {
      void err;
    });
    return () => {
      cancelled = true;
    };
  }, [resolvedURL, ringTier]);

  // Phase 3.5 U3: re-evaluate LOD tier + viewport ring on every frame.
  // Cheap (a few multiplies + comparisons). Setting state only when
  // the tier OR ring transitions prevents per-frame re-renders.
  useFrame(() => {
    if (!(camera instanceof THREE.OrthographicCamera)) return;
    const nextLOD = pickLOD(
      screen.Width,
      camera.zoom,
      gl.domElement.clientWidth,
    );
    if (nextLOD !== tier) setTier(nextLOD);

    // Compute the camera's world-space viewport rect. OrthographicCamera
    // exposes left/right/top/bottom in NDC; divide by zoom for world.
    const halfW = (gl.domElement.clientWidth / 2) / camera.zoom;
    const halfH = (gl.domElement.clientHeight / 2) / camera.zoom;
    const viewport = {
      minX: camera.position.x - halfW,
      maxX: camera.position.x + halfW,
      minY: camera.position.y - halfH,
      maxY: camera.position.y + halfH,
    };
    const bounds: FrameBounds = {
      id: screen.ID,
      centerX: screen.X + screen.Width / 2,
      // Negate Y for the ring (matches the position transform below).
      centerY: -(screen.Y + screen.Height / 2),
      halfWidth: screen.Width / 2,
      halfHeight: screen.Height / 2,
    };
    // The selected frame is always pinned so click-to-snap doesn't
    // accidentally cull it during the camera dolly.
    const pinned = selected ? new Set([screen.ID]) : new Set<string>();
    const nextRing = classifyViewport(bounds, viewport, camera.zoom, pinned);
    if (nextRing !== ringTier) setRingTier(nextRing);
  });

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

  // Phase 3.5 follow-up #3: cold frames render nothing. We return null
  // instead of <mesh visible={false}/> so r3f doesn't bother allocating
  // the BufferGeometry / Material at all — the mesh structurally
  // doesn't exist while the frame is cold. When the ring promotes the
  // frame back to warm/hot, this component re-mounts with the texture
  // load effect re-firing.
  if (ringTier === "cold") return null;

  // Warm frames pre-load + render at opacity 0 (GPU slot, invisible).
  // Hot frames render normally.
  const ringOpacity = ringTier === "warm" ? 0 : 1;

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
        <meshBasicMaterial
          color="#ff5555"
          transparent={ringOpacity < 1}
          opacity={ringOpacity}
        />
      ) : loaded && textureRef.current ? (
        <meshBasicMaterial
          map={textureRef.current}
          // Selected outline via emissive bump — orthographic + basic
          // material doesn't support outlines natively; brightness shift
          // is the cheapest legible cue without a postprocess pass.
          color={selected ? "#ffffff" : hovered ? "#f4f4f4" : "#e8e8e8"}
          // Phase 3.5 follow-up #3: warm-ring frames render at opacity 0
          // so they hold a GPU slot without painting pixels — instant
          // promote when pan brings them into the hot ring. Hot frames
          // stay opaque (transparent=false in three.js means full
          // opacity AND opaque blending).
          transparent={ringOpacity < 1}
          opacity={ringOpacity}
        />
      ) : (
        // Loading placeholder — neutral wireframe-ish surface so the user
        // sees the frame slot before texture decode finishes.
        <meshBasicMaterial
          color="#1a1a1a"
          wireframe
          transparent={ringOpacity < 1}
          opacity={ringOpacity}
        />
      )}
    </mesh>
  );
}
