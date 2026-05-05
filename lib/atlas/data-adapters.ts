"use client";

/**
 * lib/atlas/data-adapters.ts — converts ds-service responses into the shapes
 * the ported reference UI consumes.
 *
 * One golden rule: every UI-bound shape goes through a pure converter here.
 * The same converter runs on initial load and on SSE patches, so live updates
 * land in the live store with byte-identical structure.
 *
 * Network surface used:
 *   GET  /v1/projects/atlas/brain-nodes?platform=…   → brain Flow nodes
 *   GET  /v1/projects/graph?platform=…               → SYNAPSES (edges)
 *   GET  /v1/projects/{slug}                         → flows + screens
 *   GET  /v1/projects/{slug}/screens/{id}/canonical-tree  → leaf-edge inference
 *   GET  /v1/projects/{slug}/violations              → DisplayViolation[]
 *   GET  /v1/projects/{slug}/flows/{flow_id}/decisions    → DisplayDecision[]
 *   GET  /v1/projects/{slug}/flows/{flow_id}/activity     → ActivityEntry[]
 *   GET  /v1/projects/{slug}/comments?target_kind=flow&target_id=… → Comment[]
 *   GET  /v1/projects/{slug}/flows/{flow_id}/drd     → DRDDocument
 */

import { getToken } from "../auth-client";
import {
  fetchProject,
  listViolations,
  screenPngUrl,
  type ApiResult,
} from "../projects/client";
import type {
  Flow as DSFlow,
  Project as DSProject,
  Screen as DSScreen,
  Violation as DSViolation,
} from "../projects/types";
import { ATLAS_DOMAINS, isPrimaryFlow, productToDomain } from "./taxonomy";
import { ruleMeta } from "./rules-registry";
import type {
  ActivityEntry,
  ActivityKind,
  AtlasState,
  DisplayComment,
  DisplayDecision,
  DisplayViolation,
  DisplayViolationSeverity,
  DisplayViolationStatus,
  DRDDocument,
  Domain,
  Flow,
  Frame,
  Leaf,
  LeafCanvas,
  LeafEdge,
  LeafOverlays,
  Platform,
  Synapse,
} from "./types";

// ─── Shared HTTP helpers ─────────────────────────────────────────────────────

function dsBaseURL(): string {
  return process.env.NEXT_PUBLIC_DS_SERVICE_URL || "http://localhost:8080";
}

function authedHeaders(extra?: Record<string, string>): Record<string, string> {
  const token = getToken();
  const headers: Record<string, string> = { Accept: "application/json", ...extra };
  if (token) headers["Authorization"] = `Bearer ${token}`;
  return headers;
}

async function getJSON<T>(path: string, etag?: string): Promise<{ ok: true; data: T; etag?: string } | { ok: false; status: number; error: string; notModified?: boolean }> {
  try {
    const headers = authedHeaders();
    if (etag) headers["If-None-Match"] = etag;
    const res = await fetch(`${dsBaseURL()}${path}`, { headers });
    if (res.status === 304) {
      return { ok: false, status: 304, error: "not_modified", notModified: true };
    }
    if (!res.ok) {
      const txt = await res.text().catch(() => "");
      return { ok: false, status: res.status, error: txt || `HTTP ${res.status}` };
    }
    const data = (await res.json()) as T;
    const newEtag = res.headers.get("ETag") || undefined;
    return { ok: true, data, etag: newEtag };
  } catch (err) {
    return { ok: false, status: 0, error: err instanceof Error ? err.message : String(err) };
  }
}

// ─── Wire shapes (server) ────────────────────────────────────────────────────

interface AtlasBrainNodesResponse {
  nodes: Array<{
    id: string;
    slug: string;
    name: string;
    platform: string;
    product: string;
    path: string;
    updated_at: string;
    latest_version_id?: string;
    screen_count: number;
    flow_count: number;
    active_violations: number;
  }>;
  count: number;
  platform: string;
}

// AtlasBrainProductsResponse — server-side aggregation of projects by
// taxonomy product. One row per product = one primary brain node, with the
// constituent projects exposed as `leaves` orbiting that node.
interface AtlasBrainProductsResponse {
  products: Array<{
    product: string;
    project_count: number;
    flow_count: number;
    screen_count: number;
    active_violations: number;
    updated_at: string;
    leaves: Array<{
      id: string;        // project slug
      name: string;
      screen_count: number;
      flow_count: number;
      active_violations: number;
      latest_version_id?: string;
      updated_at: string;
    }>;
  }>;
  count: number;
  platform: string;
}

interface GraphAggregateResponse {
  nodes: Array<{ id: string; type: string; label: string; parent_id?: string }>;
  edges: Array<{ source: string; target: string; class: string }>;
  cache_key: string;
  platform: string;
}

interface DecisionRow {
  id: string;
  flow_id: string;
  summary: string;
  rationale: string;
  status: string;
  supersedes_id?: string | null;
  created_by_user_id: string;
  created_by_email?: string;
  created_at: string;
  linked_violation_id?: string | null;
  linked_screen_id?: string | null;
}

interface CommentRow {
  id: string;
  target_kind: string;
  target_id: string;
  text: string;
  author_user_id: string;
  author_email?: string;
  reaction_count?: number;
  created_at: string;
}

interface FlowActivityRow {
  id: string;
  ts: string;
  event_type: string;
  user_id: string;
  endpoint: string;
  status_code: number;
  details?: string;
}

interface DRDFetchResponse {
  flow_id: string;
  content: unknown;
  revision: number;
  updated_at: string | null;
  updated_by: string | null;
}

// ─── Brain-level fetchers ────────────────────────────────────────────────────

/**
 * Cold-load the entire brain shell. One round-trip to brain-nodes (project
 * roll-up) + one to graph aggregate (synapses). Both are bounded queries.
 */
export async function fetchInitialAtlasState(opts: {
  platform: Platform;
  brainNodesETag?: string;
  graphAggregateETag?: string;
}): Promise<AtlasState> {
  const platform = opts.platform;

  // brain-products is the server-side aggregation: one row per taxonomy
  // product, with constituent projects collapsed into `leaves`. This replaces
  // the older brain-nodes shape (one row per project) which produced too many
  // primary nodes on the brain (e.g. INDstocks appearing 4 times).
  const [brain, graph] = await Promise.all([
    getJSON<AtlasBrainProductsResponse>(
      `/v1/projects/atlas/brain-products?platform=${platform}`,
      opts.brainNodesETag,
    ),
    getJSON<GraphAggregateResponse>(
      `/v1/projects/graph?platform=${platform}`,
      opts.graphAggregateETag,
    ),
  ]);

  const flows: Flow[] = [];
  const leavesByFlow: Record<string, Leaf[]> = {};
  if (brain.ok) {
    for (const p of brain.data.products) {
      const f = productToFlow(p);
      flows.push(f);
      // Pre-populate the leavesByFlow map — one leaf per underlying project.
      // The brain renderer can show these as orbit dots without a follow-up
      // round-trip when the user zooms or hovers a product node.
      leavesByFlow[f.id] = p.leaves.map((leaf) =>
        productLeafToLeaf(leaf, f.id),
      );
    }
  }
  const synapses: Synapse[] = graph.ok ? edgesToSynapses(graph.data.edges, flows) : [];

  return {
    domains: ATLAS_DOMAINS,
    flows,
    synapses,
    leavesByFlow,
    brainNodesETag: brain.ok ? brain.etag : opts.brainNodesETag,
    graphAggregateETag: graph.ok ? graph.etag : opts.graphAggregateETag,
    loadedAt: Date.now(),
  };
}

/** Refetch only the brain nodes (cheaper than full state). Now also returns
 *  the leavesByFlow map so SSE-driven refreshes don't lose the orbit data. */
export async function refetchBrainNodes(
  platform: Platform,
  etag?: string,
): Promise<
  { flows: Flow[]; leavesByFlow: Record<string, Leaf[]>; etag?: string }
  | { notModified: true }
> {
  const r = await getJSON<AtlasBrainProductsResponse>(
    `/v1/projects/atlas/brain-products?platform=${platform}`,
    etag,
  );
  if (!r.ok) {
    if (r.notModified) return { notModified: true };
    return { flows: [], leavesByFlow: {}, etag };
  }
  const flows: Flow[] = [];
  const leavesByFlow: Record<string, Leaf[]> = {};
  for (const p of r.data.products) {
    const f = productToFlow(p);
    flows.push(f);
    leavesByFlow[f.id] = p.leaves.map((leaf) => productLeafToLeaf(leaf, f.id));
  }
  return { flows, leavesByFlow, etag: r.etag };
}

// ─── Leaf fetchers ───────────────────────────────────────────────────────────

/**
 * Fetch all leaves (= flows) under a project, with rolled-up frame counts and
 * active violation counts. Reuses GET /v1/projects/{slug} which already
 * returns flows[] and screens[] (per-version). Violation counts come from a
 * second call so we can scope by version + status without overfetching the
 * full list.
 */
export async function fetchLeavesForFlow(
  slug: string,
  versionID?: string,
): Promise<Leaf[]> {
  const proj = await fetchProject(slug, versionID);
  if (!proj.ok || !proj.data.flows || !proj.data.screens) return [];

  // Frame counts per flow_id: one pass over screens.
  const framesByFlow = new Map<string, number>();
  for (const s of proj.data.screens) {
    framesByFlow.set(s.FlowID, (framesByFlow.get(s.FlowID) ?? 0) + 1);
  }

  // Violation counts per flow_id: scope to active. Group client-side over the
  // single response — far less roundtrips than a per-flow count endpoint.
  const violations = await listViolations(slug, versionID);
  const violationsByFlow = new Map<string, number>();
  if (violations.ok) {
    const screenToFlow = new Map<string, string>();
    for (const s of proj.data.screens) screenToFlow.set(s.ID, s.FlowID);
    for (const v of violations.data.violations) {
      if (v.Status !== "active") continue;
      const f = screenToFlow.get(v.ScreenID);
      if (!f) continue;
      violationsByFlow.set(f, (violationsByFlow.get(f) ?? 0) + 1);
    }
  }

  return proj.data.flows
    .filter((f) => !f.DeletedAt)
    .map((f) => flowRowToLeaf(f, slug, framesByFlow.get(f.ID) ?? 0, violationsByFlow.get(f.ID) ?? 0));
}

/**
 * Build the Figma-board canvas for a single leaf. Frames are real screens at
 * real x/y/w/h. Edges are walked from canonical_tree navigation refs (lazy,
 * one fetch per visible screen) — failures fall through to an empty edge set.
 */
export async function fetchLeafCanvas(
  slug: string,
  flowID: string,
  versionID?: string,
): Promise<LeafCanvas> {
  const proj = await fetchProject(slug, versionID);
  if (!proj.ok || !proj.data.screens) return { frames: [], edges: [] };

  const token = getToken();
  // After the brain-products migration, a "leaf" is a whole ds-service
  // project rather than a single flow row. Pass flowID="" to render the
  // project's full screen catalogue; pass a UUID to filter to one section.
  const screens = (flowID
    ? proj.data.screens.filter((s) => s.FlowID === flowID)
    : proj.data.screens)
    .sort((a, b) => (a.Y - b.Y) || (a.X - b.X));

  const frames: Frame[] = screens.map((s, idx) => screenToFrame(s, idx, slug, token));

  // Walk canonical trees, but probe the first screen serially first — when
  // canonical_tree hasn't been built yet (sheet-sync imports skip the
  // pipeline that fills the column), every screen 404s and we'd otherwise
  // spam 20 requests' worth of console noise on every leaf open.
  //
  // Side effect since U6: every fetched canonical_tree is also stashed in
  // `canonicalTreeByScreenID` so the strict-TS LeafFrameRenderer can skip
  // the network round-trip for above-the-fold frames. Frames not in this
  // initial sample lazy-load their tree directly via lazyFetchCanonicalTree.
  const edges: LeafEdge[] = [];
  const canonicalTreeByScreenID: Record<string, unknown> = {};
  const sample = screens.slice(0, 20);
  const screenIDs = new Set(screens.map((s) => s.ID));
  if (sample.length > 0) {
    const probe = await getJSON<{ canonical_tree: unknown; hash: string | null }>(
      `/v1/projects/${encodeURIComponent(slug)}/screens/${encodeURIComponent(sample[0].ID)}/canonical-tree`,
    );
    if (probe.ok) {
      canonicalTreeByScreenID[sample[0].ID] = probe.data.canonical_tree ?? null;
      const targets = collectNavigationTargets(probe.data.canonical_tree, screenIDs);
      targets.forEach((toID, j) => {
        edges.push({ from: sample[0].ID, to: toID, kind: j === 0 ? "main" : "branch" });
      });
      const trees = await Promise.allSettled(
        sample.slice(1).map((s) =>
          getJSON<{ canonical_tree: unknown; hash: string | null }>(
            `/v1/projects/${encodeURIComponent(slug)}/screens/${encodeURIComponent(s.ID)}/canonical-tree`,
          ),
        ),
      );
      trees.forEach((t, i) => {
        if (t.status !== "fulfilled" || !t.value.ok) return;
        const fromScreen = sample[i + 1];
        canonicalTreeByScreenID[fromScreen.ID] = t.value.data.canonical_tree ?? null;
        const targetIDs = collectNavigationTargets(t.value.data.canonical_tree, screenIDs);
        targetIDs.forEach((toID, j) => {
          edges.push({ from: fromScreen.ID, to: toID, kind: j === 0 ? "main" : "branch" });
        });
      });
    }
    // probe.ok === false → the project has no canonical trees built; skip
    // the parallel walk to avoid 20 redundant 404s in the network panel.
  }

  return { frames, edges, canonicalTreeByScreenID };
}

/**
 * Pull the inspector overlays for a leaf in one parallel batch. Each tab can
 * still re-fetch on its own (live store handles that), but cold load gets one
 * round-trip's worth of latency.
 */
export async function fetchLeafOverlays(
  slug: string,
  flowID: string,
  versionID: string | undefined,
  framesByID: ReadonlyMap<string, Frame>,
  userDirectory: ReadonlyMap<string, string>,
): Promise<LeafOverlays> {
  // When the caller doesn't have a specific flow_id (post brain-products
  // migration: a leaf is a whole project, not a single flow), pick the
  // project's first flow as the context for per-flow endpoints. Project-
  // wide aggregation can come later; this gives the inspector something
  // meaningful to render today.
  let resolvedFlowID = flowID;
  if (!resolvedFlowID) {
    const proj = await fetchProject(slug, versionID);
    if (proj.ok && proj.data.flows && proj.data.flows.length > 0) {
      const first = proj.data.flows.find((f) => !f.DeletedAt) ?? proj.data.flows[0];
      resolvedFlowID = first.ID;
    }
  }
  if (!resolvedFlowID) {
    // No flow yet — surface empty overlays without 404-spamming the per-flow
    // endpoints. The inspector renders empty state for each tab.
    return { violations: [], decisions: [], activity: [], comments: [] };
  }
  const [violations, decisions, activity, comments, drd] = await Promise.all([
    listViolations(slug, versionID),
    getJSON<{ decisions: DecisionRow[] }>(
      `/v1/projects/${encodeURIComponent(slug)}/flows/${encodeURIComponent(resolvedFlowID)}/decisions`,
    ),
    getJSON<{ activity: FlowActivityRow[]; count: number }>(
      `/v1/projects/${encodeURIComponent(slug)}/flows/${encodeURIComponent(resolvedFlowID)}/activity?limit=50`,
    ),
    getJSON<{ comments: CommentRow[] }>(
      `/v1/projects/${encodeURIComponent(slug)}/comments?target_kind=flow&target_id=${encodeURIComponent(resolvedFlowID)}`,
    ),
    getJSON<DRDFetchResponse>(
      `/v1/projects/${encodeURIComponent(slug)}/flows/${encodeURIComponent(resolvedFlowID)}/drd`,
    ),
  ]);

  const flowScreenIDs = new Set(framesByID.keys());

  // Go serializes nil slices as `null` rather than `[]` — every list field
  // below could be null even on a 200 OK response. Coerce to [] before any
  // filter/map call so the inspector renders empty state instead of crashing.
  const violationDisplay: DisplayViolation[] = violations.ok
    ? (violations.data.violations ?? [])
        .filter((v) => flowScreenIDs.has(v.ScreenID))
        .map((v) => violationToDisplay(v, framesByID))
    : [];

  const decisionDisplay: DisplayDecision[] = decisions.ok
    ? (decisions.data.decisions ?? []).map((d) => decisionToDisplay(d, framesByID, userDirectory))
    : [];

  const activityDisplay: ActivityEntry[] = activity.ok
    ? (activity.data.activity ?? []).map((a) => auditLogToActivity(a, userDirectory))
    : [];

  const commentDisplay: DisplayComment[] = comments.ok
    ? (comments.data.comments ?? []).map((c) => commentToDisplay(c, userDirectory))
    : [];

  const drdDoc: DRDDocument | undefined = drd.ok
    ? {
        content: typeof drd.data.content === "string" ? drd.data.content : JSON.stringify(drd.data.content ?? {}),
        revision: drd.data.revision,
        updatedAt: drd.data.updated_at ?? "",
        updatedBy: drd.data.updated_by ?? "",
      }
    : undefined;

  return {
    violations: violationDisplay,
    decisions: decisionDisplay,
    activity: activityDisplay,
    comments: commentDisplay,
    drd: drdDoc,
  };
}

// ─── Pure converters (snapshot-tested) ───────────────────────────────────────

export function brainNodeToFlow(n: AtlasBrainNodesResponse["nodes"][number]): Flow {
  return {
    id: n.slug,
    label: n.name,
    domain: productToDomain(n.product),
    count: n.screen_count,
    primary: isPrimaryFlow(n.screen_count),
    activeViolations: n.active_violations,
    flowCount: n.flow_count,
    latestVersionID: n.latest_version_id,
    product: n.product,
  };
}

/**
 * Convert a server-side product aggregation row into a brain-node `Flow`.
 * The `id` is the product slug (kebab-case) so the leaf canvas + URL state
 * survive server-side aggregation changes.
 */
export function productToFlow(
  p: AtlasBrainProductsResponse["products"][number],
): Flow {
  const slug = productSlug(p.product);
  return {
    id: slug,
    label: p.product,
    domain: productToDomain(p.product),
    count: p.screen_count,
    primary: isPrimaryFlow(p.screen_count),
    activeViolations: p.active_violations,
    flowCount: p.flow_count,
    latestVersionID: undefined, // product-level node — leaves carry per-project version IDs
    product: p.product,
  };
}

/**
 * Convert a per-project leaf (under a product) into the `Leaf` shape the
 * inspector + canvas consume. `frames` doubles as screen count at this
 * stage; the leaf canvas re-fetches real frames on open.
 */
export function productLeafToLeaf(
  leaf: AtlasBrainProductsResponse["products"][number]["leaves"][number],
  parentFlowID: string,
): Leaf {
  return {
    id: leaf.id,
    flow: parentFlowID,
    label: leaf.name,
    frames: leaf.screen_count,
    violations: leaf.active_violations,
  };
}

function productSlug(product: string): string {
  return product
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "");
}

export function edgesToSynapses(
  edges: GraphAggregateResponse["edges"],
  flows: ReadonlyArray<Flow>,
): Synapse[] {
  // graph_index edges target component/token IDs; we only emit synapses
  // between two flow-level nodes. Project-level edges aren't emitted by the
  // current rebuild worker, so this returns [] until that lands. The
  // adapter still walks the data so future emissions surface automatically.
  const flowIDs = new Set(flows.map((f) => f.id));
  const out: Synapse[] = [];
  for (const e of edges) {
    const src = stripPrefix(e.source, "flow:");
    const tgt = stripPrefix(e.target, "flow:");
    if (!src || !tgt) continue;
    if (!flowIDs.has(src) || !flowIDs.has(tgt) || src === tgt) continue;
    out.push([src, tgt]);
  }
  return out;
}

function stripPrefix(s: string, prefix: string): string | null {
  if (!s.startsWith(prefix)) return null;
  return s.slice(prefix.length);
}

export function flowRowToLeaf(
  f: DSFlow,
  parentSlug: string,
  frames: number,
  violations: number,
): Leaf {
  return {
    id: f.ID,
    flow: parentSlug,
    label: f.Name,
    frames,
    violations,
  };
}

export function screenToFrame(
  s: DSScreen,
  idx: number,
  slug: string,
  token: string | null,
): Frame {
  // PNG URL: even when PNGStorageKey is null the route still works (server
  // returns 404), but the renderer prefers an empty string so it shows the
  // placeholder card instead of a broken image.
  const pngUrl = s.PNGStorageKey ? screenPngUrl(slug, s.ID, token) : "";
  return {
    id: s.ID,
    idx,
    x: s.X,
    y: s.Y,
    w: s.Width,
    h: s.Height,
    label: humanizeScreenLabel(s.ScreenLogicalID, idx),
    pngUrl,
  };
}

function humanizeScreenLabel(raw: string | undefined, idx: number): string {
  const s = (raw ?? "").trim();
  if (!s) return `Screen ${idx + 1}`;
  // UUID heuristic — when the logical ID is just a 32+char hex/uuid blob,
  // fall back to the index-based label rather than rendering noise.
  if (/^[0-9a-f-]{20,}$/i.test(s)) return `Screen ${idx + 1}`;
  // Strip a common project prefix and humanize: "kyc-pan-step-2" → "Pan Step 2".
  const cleaned = s
    .replace(/^[a-z0-9]+[_-]/i, "") // drop the first segment if it looks like a flow prefix
    .split(/[_\-/]+/)
    .filter(Boolean)
    .map((p) => p.charAt(0).toUpperCase() + p.slice(1))
    .join(" ")
    .trim();
  return cleaned || `Screen ${idx + 1}`;
}

export function violationToDisplay(
  v: DSViolation,
  framesByID: ReadonlyMap<string, Frame>,
): DisplayViolation {
  const meta = ruleMeta(v.RuleID);
  const frame = framesByID.get(v.ScreenID);
  return {
    id: v.ID,
    severity: mapSeverity(v.Severity),
    rule: meta.label,
    ruleId: v.RuleID,
    layer: v.Property || "",
    frameIdx: frame?.idx,
    status: v.Status as DisplayViolationStatus,
    detail: composeDetail(v.Observed, v.Suggestion),
    ago: formatRelative(v.CreatedAt),
    createdAt: v.CreatedAt,
    rawSeverity: v.Severity,
  };
}

function mapSeverity(s: DSViolation["Severity"]): DisplayViolationSeverity {
  switch (s) {
    case "critical":
    case "high":
      return "error";
    case "medium":
      return "warning";
    case "low":
    case "info":
    default:
      return "info";
  }
}

function composeDetail(observed: string, suggestion: string): string {
  const o = (observed || "").trim();
  const s = (suggestion || "").trim();
  if (o && s) return `${o} → ${s}`;
  return o || s || "";
}

export function decisionToDisplay(
  d: DecisionRow,
  framesByID: ReadonlyMap<string, Frame>,
  userDirectory: ReadonlyMap<string, string>,
): DisplayDecision {
  const linkedScreen = d.linked_screen_id ? framesByID.get(d.linked_screen_id) : undefined;
  return {
    id: d.id,
    title: d.summary,
    body: d.rationale,
    author: displayNameFor(d.created_by_user_id, d.created_by_email, userDirectory),
    ago: formatRelative(d.created_at),
    createdAt: d.created_at,
    linksTo: linkedScreen?.idx,
  };
}

export function auditLogToActivity(
  row: FlowActivityRow,
  userDirectory: ReadonlyMap<string, string>,
): ActivityEntry {
  const kind = activityKindOf(row.event_type);
  return {
    id: row.id,
    who: displayNameFor(row.user_id, undefined, userDirectory),
    what: activitySentenceOf(row),
    ago: formatRelative(row.ts),
    ts: row.ts,
    kind,
  };
}

function activityKindOf(eventType: string): ActivityKind {
  // Aligned with the reference UI's CSS palette (leafcanvas.css):
  // .kind-edit / .kind-violation / .kind-audit / .kind-decision / .kind-sync.
  // Anything outside those 5 falls back to "edit" (note icon) so it still
  // renders with a visible glyph instead of a blank circle.
  if (eventType.startsWith("decision")) return "decision";
  if (eventType.startsWith("violation")) return "violation";
  if (eventType.startsWith("audit")) return "audit";
  if (eventType.startsWith("figma") || eventType.includes("export") || eventType.includes("sync")) return "sync";
  if (eventType.startsWith("drd") || eventType.startsWith("comment")) return "edit";
  return "edit";
}

function activitySentenceOf(row: FlowActivityRow): string {
  switch (row.event_type) {
    case "drd.edit":
    case "drd.put":
      return "edited DRD";
    case "drd.snapshot":
      return "saved a DRD snapshot";
    case "audit.complete":
      return "completed an audit pass";
    case "audit.violation_created": {
      const n = parseDetail(row.details, "count") ?? 1;
      return `audit flagged ${n} ${n === 1 ? "violation" : "violations"}`;
    }
    case "figma.export":
      return "synced from Figma";
    case "comment.created":
      return "commented";
    case "decision.created":
      return "added a decision";
    case "decision.superseded":
      return "superseded a decision";
    case "violation.acknowledged":
      return "acknowledged a violation";
    case "violation.fixed":
      return "marked a violation fixed";
    case "violation.dismissed":
      return "dismissed a violation";
    default:
      return row.event_type.replace(/[._]/g, " ");
  }
}

function parseDetail(details: string | undefined, key: string): number | null {
  if (!details) return null;
  try {
    const parsed = JSON.parse(details) as Record<string, unknown>;
    const v = parsed[key];
    return typeof v === "number" ? v : null;
  } catch {
    return null;
  }
}

export function commentToDisplay(
  c: CommentRow,
  userDirectory: ReadonlyMap<string, string>,
): DisplayComment {
  return {
    id: c.id,
    who: displayNameFor(c.author_user_id, c.author_email, userDirectory),
    body: c.text,
    ago: formatRelative(c.created_at),
    createdAt: c.created_at,
    reactions: c.reaction_count ?? 0,
  };
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

function displayNameFor(
  userID: string,
  emailHint: string | undefined,
  directory: ReadonlyMap<string, string>,
): string {
  const hit = directory.get(userID);
  if (hit) return hit;
  if (emailHint) return prettifyEmail(emailHint);
  return "—";
}

function prettifyEmail(email: string): string {
  const local = email.split("@")[0] ?? email;
  return local
    .split(/[._-]/)
    .filter(Boolean)
    .map((p) => p.charAt(0).toUpperCase() + p.slice(1))
    .join(" ")
    .trim();
}

const RTF = typeof Intl !== "undefined" && typeof Intl.RelativeTimeFormat === "function"
  ? new Intl.RelativeTimeFormat("en", { numeric: "auto" })
  : null;

export function formatRelative(iso: string | null | undefined, now: number = Date.now()): string {
  if (!iso) return "";
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return "";
  const diffSec = Math.round((t - now) / 1000);
  const abs = Math.abs(diffSec);
  if (RTF) {
    if (abs < 45) return RTF.format(diffSec, "second");
    if (abs < 45 * 60) return RTF.format(Math.round(diffSec / 60), "minute");
    if (abs < 24 * 3600) return RTF.format(Math.round(diffSec / 3600), "hour");
    if (abs < 30 * 86400) return RTF.format(Math.round(diffSec / 86400), "day");
    if (abs < 365 * 86400) return RTF.format(Math.round(diffSec / (30 * 86400)), "month");
    return RTF.format(Math.round(diffSec / (365 * 86400)), "year");
  }
  // Fallback for environments without Intl.RelativeTimeFormat.
  if (abs < 60) return "just now";
  if (abs < 3600) return `${Math.round(abs / 60)}m ago`;
  if (abs < 86400) return `${Math.round(abs / 3600)}h ago`;
  return `${Math.round(abs / 86400)}d ago`;
}

/**
 * Walk a canonical_tree blob looking for `action.navigate_to: "<screen_id>"`
 * pointers. Tolerates unknown shapes by descending into every object/array
 * and pattern-matching loosely. Returns referenced screen IDs that exist in
 * the provided allow-set, in the order they were encountered.
 */
function collectNavigationTargets(tree: unknown, allow: ReadonlySet<string>): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  const stack: unknown[] = [tree];
  while (stack.length) {
    const node = stack.pop();
    if (!node || typeof node !== "object") continue;
    const obj = node as Record<string, unknown>;
    const action = obj.action as Record<string, unknown> | undefined;
    if (action) {
      const target = action.navigate_to ?? action.navigateTo ?? action.target ?? action.screen_id;
      if (typeof target === "string" && allow.has(target) && !seen.has(target)) {
        seen.add(target);
        out.push(target);
      }
    }
    for (const k of Object.keys(obj)) {
      const v = obj[k];
      if (Array.isArray(v)) {
        for (const item of v) stack.push(item);
      } else if (v && typeof v === "object") {
        stack.push(v);
      }
    }
  }
  return out;
}

// ─── Graph SSE — real subscription to GraphIndexUpdated events ──────────────

/**
 * Subscribe to the `graph:<tenant>:<platform>` SSE channel. The server
 * publishes a `GraphIndexUpdated` event each time the rebuild worker
 * flushes for the (tenant, platform). We mint a single-use ticket then
 * open EventSource (browsers can't send Authorization on EventSource).
 *
 * Returns a cleanup that closes the stream and prevents auto-reconnect.
 */
export function subscribeGraphEvents(
  platform: Platform,
  onEvent: (evt: { type: "GraphIndexUpdated"; materializedAt: string }) => void,
): () => void {
  let cancelled = false;
  let es: EventSource | null = null;
  let backoffMs = 1000;
  const BACKOFF_MAX_MS = 15_000;

  async function mintAndOpen(): Promise<void> {
    if (cancelled) return;
    try {
      const tres = await fetch(`${dsBaseURL()}/v1/projects/graph/events/ticket`, {
        method: "POST",
        headers: authedHeaders({ "Content-Type": "application/json" }),
        body: JSON.stringify({ platform }),
      });
      if (cancelled) return;
      if (!tres.ok) {
        scheduleReconnect();
        return;
      }
      const body = (await tres.json()) as { ticket: string };
      const url = `${dsBaseURL()}/v1/projects/graph/events?ticket=${encodeURIComponent(body.ticket)}`;
      es = new EventSource(url);
      es.onopen = () => { backoffMs = 1000; };
      es.addEventListener("GraphIndexUpdated", (raw) => {
        const ev = raw as MessageEvent<string>;
        try {
          const data = JSON.parse(ev.data) as { materialized_at?: string };
          onEvent({ type: "GraphIndexUpdated", materializedAt: data.materialized_at ?? "" });
        } catch {
          onEvent({ type: "GraphIndexUpdated", materializedAt: "" });
        }
      });
      es.onerror = () => {
        es?.close();
        es = null;
        if (!cancelled) scheduleReconnect();
      };
    } catch {
      scheduleReconnect();
    }
  }

  function scheduleReconnect(): void {
    if (cancelled) return;
    const delay = backoffMs;
    backoffMs = Math.min(backoffMs * 2, BACKOFF_MAX_MS);
    window.setTimeout(() => {
      if (!cancelled) void mintAndOpen();
    }, delay);
  }

  void mintAndOpen();
  return () => {
    cancelled = true;
    es?.close();
    es = null;
  };
}

// ─── Re-exports for ergonomic callers ────────────────────────────────────────

export type { ApiResult } from "../projects/client";
