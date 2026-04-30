/**
 * Pure-function mode resolver for the U8 JSON tab.
 *
 * The canonical_tree carries `boundVariables` references in raw form (e.g.
 * `{ fills: { id: "VariableID:abc/123:0" } }`). When the active mode is
 * "light" vs "dark", the resolved fill colour differs even though the tree
 * structure is identical — that's the whole point of Variable Modes (verified
 * in the brainstorm against INDstocks V4).
 *
 * This module:
 *   1. Walks the tree generically (any object with a `boundVariables` field).
 *   2. For each binding, looks up the variable in the active screen mode's
 *      `ExplicitVariableModesJSON` payload.
 *   3. Returns a thin "annotated node" structure where each binding gains a
 *      `_resolved` field carrying the active-mode value, leaving the original
 *      tree untouched.
 *
 * **No structural copy.** We do NOT clone the tree — the U8 viewer receives
 * the original tree plus a memoized resolver that lazily annotates per-node
 * on render. Theme toggle just swaps the active mode label; cached values are
 * invalidated lazily.
 */

/**
 * BoundVariableRef is the raw shape Figma REST returns for a binding.
 * `id` is a Variable ID in the form "VariableID:<collection_hash>/<num>:<num>".
 */
export interface BoundVariableRef {
  type?: string;
  id: string;
}

/**
 * ResolvedValue carries the active-mode value of a bound variable. The shape
 * is intentionally permissive — Figma stores colours as `{r, g, b, a}` floats,
 * dimensions as raw numbers, etc. Callers render based on the discriminated
 * `kind` field.
 */
export type ResolvedValue =
  | { kind: "color"; hex: string; rgba: { r: number; g: number; b: number; a: number } }
  | { kind: "number"; value: number }
  | { kind: "string"; value: string }
  | { kind: "unknown"; raw: unknown };

/**
 * ModeResolver caches per-binding lookups for the active mode. A new instance
 * is created on every theme toggle; React's `useMemo(() => makeResolver(...), [mode, modes])`
 * pattern ensures the cache is dropped when the active mode changes.
 *
 * Construction is O(modes), lookup is O(1) per binding via the inner Map.
 */
export interface ModeResolver {
  /** Active mode label ("light", "dark", etc.) for log/debug. */
  mode: string;
  /** Resolve a binding by its variable id. Returns `null` when the active
   *  mode has no value for this variable. */
  resolve(binding: BoundVariableRef): ResolvedValue | null;
}

/**
 * VariableValueMap is the per-mode map embedded in `ExplicitVariableModesJSON`.
 * Shape mirrors what the Figma REST API returns under
 * `localVariables[varId].valuesByMode[modeId]`. The pipeline (U4) extracted
 * the per-screen subset and persisted it as JSON — we parse it lazily here.
 */
type VariableValueMap = Record<string, unknown>;

/**
 * makeResolver builds a `ModeResolver` for the given active mode label.
 * Inputs:
 *   - `activeMode` — mode label the user is currently viewing (light/dark/…)
 *   - `modeBindings` — array of (mode_label, parsed JSON) tuples extracted from
 *     the screen's ScreenModes. Caller is responsible for parsing
 *     `ExplicitVariableModesJSON` once and passing the parsed object here.
 *
 * If `activeMode` doesn't match any provided mode label, the resolver returns
 * `null` for every binding (UI degrades to showing raw IDs).
 */
export function makeResolver(
  activeMode: string,
  modeBindings: Array<{ label: string; values: VariableValueMap }>,
): ModeResolver {
  const target = modeBindings.find((m) => m.label === activeMode);
  // Local cache so repeated lookups for the same id during a single render
  // pass don't re-execute the type-detection logic. Cleared per resolver
  // instance — i.e. per theme toggle.
  const cache = new Map<string, ResolvedValue | null>();

  return {
    mode: activeMode,
    resolve(binding: BoundVariableRef): ResolvedValue | null {
      if (!binding?.id) return null;
      if (cache.has(binding.id)) return cache.get(binding.id) ?? null;
      if (!target) {
        cache.set(binding.id, null);
        return null;
      }
      const raw = target.values[binding.id];
      const resolved = classifyValue(raw);
      cache.set(binding.id, resolved);
      return resolved;
    },
  };
}

/**
 * classifyValue converts a raw Figma Variable value into a tagged
 * `ResolvedValue`. Distinguishes colour-shaped objects from plain numbers and
 * strings; everything else falls through as `kind: "unknown"`.
 */
function classifyValue(raw: unknown): ResolvedValue | null {
  if (raw === null || raw === undefined) return null;
  if (typeof raw === "number") return { kind: "number", value: raw };
  if (typeof raw === "string") return { kind: "string", value: raw };
  if (typeof raw === "object") {
    const obj = raw as Record<string, unknown>;
    if (
      typeof obj.r === "number" &&
      typeof obj.g === "number" &&
      typeof obj.b === "number"
    ) {
      const r = clamp01(obj.r);
      const g = clamp01(obj.g);
      const b = clamp01(obj.b);
      const a = typeof obj.a === "number" ? clamp01(obj.a) : 1;
      return {
        kind: "color",
        hex: rgbaToHex(r, g, b, a),
        rgba: { r, g, b, a },
      };
    }
  }
  return { kind: "unknown", raw };
}

function clamp01(n: number): number {
  if (n < 0) return 0;
  if (n > 1) return 1;
  return n;
}

function rgbaToHex(r: number, g: number, b: number, a: number): string {
  const ri = Math.round(r * 255);
  const gi = Math.round(g * 255);
  const bi = Math.round(b * 255);
  const hex = `#${[ri, gi, bi].map((v) => v.toString(16).padStart(2, "0")).join("")}`;
  if (a >= 0.999) return hex;
  const ai = Math.round(a * 255);
  return `${hex}${ai.toString(16).padStart(2, "0")}`;
}

/**
 * extractBoundVariables walks a tree node generically and returns all
 * `boundVariables` entries it finds at the top level. Returns `null` when
 * the node has none — callers can early-return without invoking the resolver.
 *
 * This intentionally does NOT recurse into children — the JSON tree viewer
 * walks children itself and calls extractBoundVariables on each node it
 * renders.
 */
export function extractBoundVariables(
  node: unknown,
): Array<{ field: string; binding: BoundVariableRef }> | null {
  if (!node || typeof node !== "object") return null;
  const bv = (node as Record<string, unknown>).boundVariables;
  if (!bv || typeof bv !== "object") return null;

  const out: Array<{ field: string; binding: BoundVariableRef }> = [];
  for (const [field, val] of Object.entries(bv as Record<string, unknown>)) {
    // Single-binding shape: `{ id, type }`.
    if (val && typeof val === "object" && "id" in (val as Record<string, unknown>)) {
      out.push({ field, binding: val as BoundVariableRef });
      continue;
    }
    // Array shape (e.g. fills[]): `[ { id, type }, ... ]`.
    if (Array.isArray(val)) {
      val.forEach((entry, idx) => {
        if (entry && typeof entry === "object" && "id" in entry) {
          out.push({
            field: `${field}[${idx}]`,
            binding: entry as BoundVariableRef,
          });
        }
      });
    }
  }
  return out.length > 0 ? out : null;
}
