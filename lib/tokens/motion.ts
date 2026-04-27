/**
 * Motion tokens loader — reads motion.tokens.json. Provides typed accessors
 * for the three spring tiers, the standard opacity transition, and the
 * press-scale transition.
 *
 * Hand-curated; Figma doesn't expose motion natively.
 */
import motionData from "./indmoney/motion.tokens.json";

export interface SpringPreset {
  token: string;
  name: string;
  description?: string;
  stiffness: number;
  damping: number;
  mass: number;
  duration: string;
  feel: string;
  rn: string;
  android: string;
}

export interface OpacityPreset {
  token: string;
  description?: string;
  duration: number;
  easing: string;
  easingLabel: string;
  rnReanimated: string;
  rnAnimated: string;
}

export interface ScalePreset {
  token: string;
  description?: string;
  scaleTo: number;
  duration: number;
  easing: string;
  easingLabel: string;
  startDelay: number;
  note: string;
}

interface Node<T> {
  $description?: string;
  $value?: T;
  $extensions?: { "com.indmoney.code"?: Record<string, string> };
}

const data = motionData as { motion: { spring: Record<string, Node<Omit<SpringPreset, "token" | "name" | "rn" | "android">>>; opacity: Record<string, Node<Omit<OpacityPreset, "token" | "rnReanimated" | "rnAnimated">>>; scale: Record<string, Node<Omit<ScalePreset, "token">>> } };

export function springPresets(): SpringPreset[] {
  const out: SpringPreset[] = [];
  for (const [name, node] of Object.entries(data.motion.spring)) {
    const v = node.$value!;
    const code = node.$extensions?.["com.indmoney.code"] ?? {};
    out.push({
      token: `motion.spring.${name}`,
      name,
      description: node.$description,
      stiffness: v.stiffness,
      damping: v.damping,
      mass: v.mass,
      duration: v.duration,
      feel: v.feel,
      rn: code.rn ?? "",
      android: code.android ?? "",
    });
  }
  return out;
}

export function opacityPreset(): OpacityPreset {
  const node = data.motion.opacity.standard;
  const v = node.$value!;
  const code = node.$extensions?.["com.indmoney.code"] ?? {};
  return {
    token: "motion.opacity.standard",
    description: node.$description,
    duration: v.duration,
    easing: v.easing,
    easingLabel: v.easingLabel,
    rnReanimated: code.rnReanimated ?? "",
    rnAnimated: code.rnAnimated ?? "",
  };
}

export function scalePreset(): ScalePreset {
  const node = data.motion.scale.press;
  const v = node.$value!;
  return {
    token: "motion.scale.press",
    description: node.$description,
    scaleTo: v.scaleTo,
    duration: v.duration,
    easing: v.easing,
    easingLabel: v.easingLabel,
    startDelay: v.startDelay,
    note: v.note,
  };
}

export function motionProvenance(): string {
  return (
    (motionData as { $extensions?: { "com.indmoney.provenance"?: string } })
      .$extensions?.["com.indmoney.provenance"] ?? "unknown"
  );
}
