// figma-plugin/auto-fix/apply.ts — Phase 4 U11
//
// Per-rule auto-fix application module. Wrapped in a `namespace AutoFix`
// because the Figma plugin tsconfig uses `module: "none"` (TypeScript
// concatenates files into a single script global). The namespace lets
// us keep this module testable + re-importable without rewriting every
// reference into a massive global API surface.
//
// Per the plan, Phase 4 covers ~60% of violations:
//   - drift.fill / unbound.fill / deprecated.fill → setBoundVariableForPaint
//   - drift.text / unbound.text → setRangeTextStyleId
//   - drift.padding / drift.gap → snap to nearest 4
//   - drift.radius → snap to nearest in {0, 4, 8, 12, 16, 24, 999}
//
// Phase 5+ extends this with structural reorg, instance-override
// unwinding, and naming-hygiene fixes. New rules add a new branch in
// previewFix + a new applier — no code.ts churn beyond a single
// dispatch line.

namespace AutoFix {
  /** Snap a numeric value to the nearest multiple of `step`. */
  export function snapToStep(value: number, step: number): number {
    if (step <= 0) return value;
    return Math.round(value / step) * step;
  }

  /**
   * Pill-rule radius lookup. The canonical radius scale is
   * {0, 4, 8, 12, 16, 24} plus a "pill" alias (999) for very-large
   * radii that visually round to a half-circle. Values >24 collapse
   * to the pill alias rather than continuing the discrete scale.
   */
  const RADIUS_LADDER = [0, 4, 8, 12, 16, 24] as const;

  export function snapToRadius(value: number): number {
    if (value > 24) return 999;
    let best: number = RADIUS_LADDER[0];
    let bestDiff = Math.abs(value - best);
    for (let i = 1; i < RADIUS_LADDER.length; i++) {
      const diff = Math.abs(value - RADIUS_LADDER[i]);
      if (diff < bestDiff) {
        best = RADIUS_LADDER[i];
        bestDiff = diff;
      }
    }
    return best;
  }

  export interface FixPreview {
    ruleID: string;
    property: string;
    before: string;
    after: string;
    /** Human-readable hint shown above the Apply button. */
    hint: string;
  }

  export interface PreviewArgs {
    ruleID: string;
    property: string;
    observed: string;
    /** For text-style fixes, the target style id. */
    targetTextStyleId?: string;
    /** For variable-binding fixes, the target token path. */
    targetTokenPath?: string;
    /** Numeric value extracted from `observed` for snap fixes. */
    observedNumber?: number;
  }

  export function previewFix(args: PreviewArgs): FixPreview | null {
    const { ruleID, property, observed, observedNumber } = args;
    switch (ruleID) {
      case "drift.fill":
      case "unbound.fill":
      case "deprecated.fill":
        if (!args.targetTokenPath) return null;
        return {
          ruleID,
          property,
          before: observed,
          after: args.targetTokenPath,
          hint: `Will bind ${property} to ${args.targetTokenPath}.`,
        };
      case "drift.text":
      case "unbound.text":
        if (!args.targetTextStyleId) return null;
        return {
          ruleID,
          property,
          before: observed,
          after: args.targetTextStyleId,
          hint: `Will apply text style ${args.targetTextStyleId}.`,
        };
      case "drift.padding":
      case "drift.gap":
        if (typeof observedNumber !== "number") return null;
        return {
          ruleID,
          property,
          before: String(observedNumber),
          after: String(snapToStep(observedNumber, 4)),
          hint: `Will snap ${property} from ${observedNumber} → ${snapToStep(observedNumber, 4)} (4-pt grid).`,
        };
      case "drift.radius":
        if (typeof observedNumber !== "number") return null;
        return {
          ruleID,
          property,
          before: String(observedNumber),
          after: String(snapToRadius(observedNumber)),
          hint: `Will snap ${property} from ${observedNumber} → ${snapToRadius(observedNumber)} (radius scale).`,
        };
    }
    return null;
  }

  /** Returns true when the rule has an auto-fix applier in this release. */
  export function isAutoFixable(ruleID: string): boolean {
    switch (ruleID) {
      case "drift.fill":
      case "unbound.fill":
      case "deprecated.fill":
      case "drift.text":
      case "unbound.text":
      case "drift.padding":
      case "drift.gap":
      case "drift.radius":
        return true;
    }
    return false;
  }

  // ─── Figma-side appliers ──────────────────────────────────────────────────

  export type ApplyResult =
    | { ok: true; appliedTo: string }
    | { ok: false; error: string };

  export async function applyFillBinding(
    nodeId: string,
    variableId: string,
  ): Promise<ApplyResult> {
    try {
      const node: any = await (figma as any).getNodeByIdAsync(nodeId);
      if (!node || !("fills" in node)) {
        return { ok: false, error: "Node not found or has no fills" };
      }
      const variable = await (figma as any).variables.getVariableByIdAsync(variableId);
      if (!variable) {
        return { ok: false, error: "Variable not found" };
      }
      if (Array.isArray(node.fills) && node.fills[0]) {
        const next = (figma as any).variables.setBoundVariableForPaint(
          node.fills[0],
          "color",
          variable,
        );
        node.fills = [next, ...node.fills.slice(1)];
        return { ok: true, appliedTo: nodeId };
      }
      return { ok: false, error: "Node has no fills[0] paint to bind" };
    } catch (err) {
      return { ok: false, error: err instanceof Error ? err.message : String(err) };
    }
  }

  export async function applyTextStyle(
    nodeId: string,
    textStyleId: string,
  ): Promise<ApplyResult> {
    try {
      const node: any = await (figma as any).getNodeByIdAsync(nodeId);
      if (!node || node.type !== "TEXT") {
        return { ok: false, error: "Node not found or not a TEXT node" };
      }
      if (typeof node.setRangeTextStyleIdAsync === "function") {
        await node.setRangeTextStyleIdAsync(0, node.characters.length, textStyleId);
      } else {
        node.setRangeTextStyleId(0, node.characters.length, textStyleId);
      }
      return { ok: true, appliedTo: nodeId };
    } catch (err) {
      return { ok: false, error: err instanceof Error ? err.message : String(err) };
    }
  }

  export async function applySnapPadding(
    nodeId: string,
    property: "paddingLeft" | "paddingRight" | "paddingTop" | "paddingBottom" | "itemSpacing",
    observed: number,
  ): Promise<ApplyResult> {
    try {
      const node: any = await (figma as any).getNodeByIdAsync(nodeId);
      if (!node) return { ok: false, error: "Node not found" };
      const target = snapToStep(observed, 4);
      node[property] = target;
      return { ok: true, appliedTo: nodeId };
    } catch (err) {
      return { ok: false, error: err instanceof Error ? err.message : String(err) };
    }
  }

  export async function applySnapRadius(
    nodeId: string,
    observed: number,
  ): Promise<ApplyResult> {
    try {
      const node: any = await (figma as any).getNodeByIdAsync(nodeId);
      if (!node) return { ok: false, error: "Node not found" };
      const target = snapToRadius(observed);
      if ("cornerRadius" in node) {
        node.cornerRadius = target;
        return { ok: true, appliedTo: nodeId };
      }
      return { ok: false, error: "Node has no cornerRadius" };
    } catch (err) {
      return { ok: false, error: err instanceof Error ? err.message : String(err) };
    }
  }
}
