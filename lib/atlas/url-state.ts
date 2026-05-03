/**
 * lib/atlas/url-state.ts — round-trip the atlas shell state through the URL.
 *
 * The shell has three pieces of openable state:
 *   ?platform=mobile|web   — which brain to render
 *   ?project=<slug>        — the brain "flow" (project) the inspector is on
 *   ?leaf=<flow_id>        — when set, the leaf canvas is open
 *   ?frame=<screen_id>     — when set + leaf open, this frame is selected
 *
 * Plus pass-through context keys preserved from older deeplinks:
 *   ?v=<version_id>        — pin to a specific project version
 *   ?trace=<trace_id>      — SSE trace correlation
 *   ?persona=<name>        — inspector violation filter
 *   ?from=<slug>           — reverse-morph anchor for back navigation
 */

export type Platform = "mobile" | "web";

export interface AtlasURLState {
  platform: Platform;
  project: string | null;
  leaf: string | null;
  frame: string | null;
  versionID: string | null;
  traceID: string | null;
  persona: string | null;
  from: string | null;
}

export const DEFAULT_ATLAS_URL_STATE: AtlasURLState = {
  platform: "mobile",
  project: null,
  leaf: null,
  frame: null,
  versionID: null,
  traceID: null,
  persona: null,
  from: null,
};

/** Parse the shell state from a URLSearchParams snapshot. */
export function parseAtlasURL(params: URLSearchParams | null | undefined): AtlasURLState {
  if (!params) return { ...DEFAULT_ATLAS_URL_STATE };
  const platformRaw = params.get("platform");
  return {
    platform: platformRaw === "web" ? "web" : "mobile",
    project: params.get("project") || null,
    leaf: params.get("leaf") || null,
    frame: params.get("frame") || null,
    versionID: params.get("v") || null,
    traceID: params.get("trace") || null,
    persona: params.get("persona") || null,
    from: params.get("from") || null,
  };
}

/** Build a new URL string from a state object. Drops null/empty keys. */
export function buildAtlasURL(state: Partial<AtlasURLState>, basePath = "/atlas"): string {
  const p = new URLSearchParams();
  if (state.platform && state.platform !== "mobile") p.set("platform", state.platform);
  if (state.project) p.set("project", state.project);
  if (state.leaf) p.set("leaf", state.leaf);
  if (state.frame) p.set("frame", state.frame);
  if (state.versionID) p.set("v", state.versionID);
  if (state.traceID) p.set("trace", state.traceID);
  if (state.persona) p.set("persona", state.persona);
  if (state.from) p.set("from", state.from);
  const qs = p.toString();
  return qs ? `${basePath}?${qs}` : basePath;
}

/**
 * Diff two states to determine if a navigation should be a `replace` (no
 * history entry) versus a `push`. Replace when only `frame` changed (frame
 * selection is too granular to litter back history); push otherwise.
 */
export function shouldReplaceHistory(
  prev: AtlasURLState,
  next: AtlasURLState,
): boolean {
  return (
    prev.platform === next.platform &&
    prev.project === next.project &&
    prev.leaf === next.leaf &&
    prev.versionID === next.versionID &&
    prev.persona === next.persona &&
    prev.from === next.from
    // intentionally not comparing frame / trace
  );
}
