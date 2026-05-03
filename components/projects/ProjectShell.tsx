"use client";

/**
 * Project view shell — top half is the atlas slot (PNG-grid placeholder in
 * U6, r3f canvas in U7), bottom half is the 4-tab strip (DRD / Violations /
 * Decisions / JSON).
 *
 * Animation choreography (per Phase 1 plan, Animation Philosophy section):
 *   - On mount: `projectShellOpen(scope)` runs the page-load timeline against
 *     this component's `data-anim="…"` annotated children.
 *   - On tab change: `tabSwitch(outgoing, incoming)` runs the curtain wipe.
 *   - On theme change: theme toggle pulse fires on the bound chips inside
 *     the JSON tab (U8 owns the chip rendering — we only build the timeline
 *     factory call site so the wiring is in place).
 *
 * SSE subscription:
 *   - `subscribeProjectEvents(slug, traceID)` opens the ticket-flow stream.
 *   - The trace ID is generated client-side here for "passive" project views
 *     (i.e. the user opened an already-exported project; no in-flight pipe-
 *     line). When the deeplink arrives from a fresh export it carries the
 *     trace ID in the URL search params; we honour that.
 *   - On `audit_complete` we toast.
 *
 * Auth gating happens one level up in `app/projects/layout.tsx`. We assume
 * `useAuth` will have a token by the time this component mounts.
 */

import {
  useEffect,
  useMemo,
  useReducer,
  useRef,
  useState,
} from "react";
import dynamic from "next/dynamic";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import gsap from "gsap";
import { showToast } from "@/components/ui/Toast";
import EmptyState from "@/components/empty-state/EmptyState";
import RetryableError from "@/components/empty-state/RetryableError";
import { useGSAPContext } from "@/lib/animations/hooks/useGSAPContext";
import { projectShellOpen } from "@/lib/animations/timelines/projectShellOpen";
import { tabSwitch } from "@/lib/animations/timelines/tabSwitch";
import {
  subscribeProjectEvents,
  fetchProject,
} from "@/lib/projects/client";
import type {
  Flow,
  Persona,
  Project,
  ProjectVersion,
  Screen,
  ScreenMode,
} from "@/lib/projects/types";
import {
  buildHash,
  parseHash,
  resolveTheme,
  useProjectView,
  type ProjectTab,
} from "@/lib/projects/view-store";
import {
  auditProgressFromState,
  initialState as initialMachineState,
  isReadOnly,
  reducer as projectViewReducer,
  shouldRenderShell,
} from "@/lib/projects/view-machine";
import ProjectToolbar, {
  type ProjectToolbarAuditState,
} from "./ProjectToolbar";
import DRDTab from "./tabs/DRDTab";
import DRDTabCollab from "./tabs/DRDTabCollab";

// Phase 5.1 P1 — feature-flag dispatch. When NEXT_PUBLIC_DRD_COLLAB === "1"
// the collab-aware editor (Yjs + Hocuspocus) replaces the single-author
// REST flow. Read at module scope so the choice is stable per
// deployment. The cutover is per-deploy so production can flip the flag
// once the sidecar is healthy without a code change.
//
// Pr16: defaults to OFF because DRDTabCollab depends on a Hocuspocus
// sidecar at the URL resolved in `lib/drd/collab.ts` (default
// `ws://localhost:8090`). Until that sidecar ships in the standard local
// dev bring-up, opt-in via env keeps the single-author REST DRDTab as the
// safe default. See `.env.example` for the documented flag.
const DRD_COLLAB_ENABLED = process.env.NEXT_PUBLIC_DRD_COLLAB === "1";
import DecisionsTab from "./tabs/DecisionsTab";
import JSONTab from "./tabs/JSONTab";
import ViolationsTab from "./tabs/ViolationsTab";

// Phase 3 U11: Shepherd.js tour. Lazy-loaded so its ~30KB chunk only
// ships when the tour actually mounts (first-time visitors only).
const ProductTour = dynamic(
  () => import("./../onboarding/ProductTour"),
  { ssr: false },
);

/**
 * Dynamic-imported r3f atlas. ssr:false keeps three.js out of the server
 * module graph — a `next build` of `app/projects/[slug]/page.js` must not
 * contain `three.module.js` symbols (Phase 1 plan, U7 Verification).
 *
 * The loader returns a placeholder div so the layout slot retains its size
 * during the chunk fetch; once the chunk lands the Canvas paints inside it.
 */
const AtlasCanvas = dynamic(
  () => import("./atlas/AtlasCanvas"),
  {
    ssr: false,
    loading: () => (
      <div
        aria-hidden
        data-anim="atlas-canvas-loading"
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
    ),
  },
);

const TAB_DEFINITIONS: Array<{ id: ProjectTab; label: string }> = [
  { id: "drd", label: "DRD" },
  { id: "violations", label: "Violations" },
  { id: "decisions", label: "Decisions" },
  { id: "json", label: "JSON" },
];

const DEFAULT_TAB: ProjectTab = "violations";

interface ProjectShellProps {
  slug: string;
  /** Initial project payload — typically fetched by the page wrapper. */
  initialProject: Project;
  /** Initial version list (may be empty when ds-service hasn't extended GET). */
  initialVersions: ProjectVersion[];
  /** Initial active version (defaults to versions[0]). */
  initialActiveVersionID?: string;
  /** Initial flows for the project — drives the per-tab flow selector
   *  (DRD, Violations, Decisions are flow-scoped). */
  initialFlows?: Flow[];
  /** Initial screens (used for the placeholder atlas grid). */
  initialScreens: Screen[];
  /** Initial persona library scoped to the active version. */
  initialPersonas: Persona[];
  /** Initial screen modes (one row per (screen, mode_label) pair). U8 JSON
   *  tab consumes this for mode resolution; pre-fetched at SSR. */
  initialScreenModes: ScreenMode[];
  /** Optional trace ID to bind SSE to a fresh-export pipeline. */
  initialTraceID?: string;
}

export default function ProjectShell({
  slug,
  initialProject,
  initialVersions,
  initialActiveVersionID,
  initialFlows,
  initialScreens,
  initialPersonas,
  initialScreenModes,
  initialTraceID,
}: ProjectShellProps) {
  // ─── State ──────────────────────────────────────────────────────────────
  const [project] = useState<Project>(initialProject);
  const [versions, setVersions] = useState<ProjectVersion[]>(initialVersions);
  const [flows] = useState<Flow[]>(initialFlows ?? []);
  const [screens, setScreens] = useState<Screen[]>(initialScreens);
  const [personas, setPersonas] = useState<Persona[]>(initialPersonas);
  const [screenModes, setScreenModes] = useState<ScreenMode[]>(initialScreenModes);
  const [activeVersionID, setActiveVersionID] = useState<string | undefined>(
    initialActiveVersionID ?? initialVersions[0]?.ID,
  );
  // Flow selector — defaults to first flow; falls back to screens[0] when
  // the project payload omits flows (older ds-service versions).
  // Pr9: sort by ID to defend against ds-service `ListScreensByVersion`
  // ordering by `created_at` only (non-deterministic within a single second).
  // Same defensive sort applies to flows so the default is stable across
  // page loads even if the server returns rows in different orders.
  const [selectedFlowID, setSelectedFlowID] = useState<string | null>(() => {
    const sortedFlows = initialFlows
      ? [...initialFlows].sort((a, b) => a.ID.localeCompare(b.ID))
      : [];
    const sortedScreens = [...initialScreens].sort((a, b) =>
      a.ID.localeCompare(b.ID),
    );
    return sortedFlows[0]?.ID ?? sortedScreens[0]?.FlowID ?? null;
  });

  // Active tab + persona derived from URL hash on mount and on hashchange.
  const [activeTab, setActiveTab] = useState<ProjectTab>(DEFAULT_TAB);
  const [activePersonaName, setActivePersonaName] = useState<string | null>(
    null,
  );
  // U14b — `pendingTab` is the incoming tab during a swap. While non-null,
  // both `activeTab` (outgoing) and `pendingTab` (incoming) are mounted so
  // the outgoing fade in `tabSwitch` runs against real DOM. On timeline
  // completion we promote pendingTab → activeTab and clear pendingTab.
  // Pre-U14b code only kept the incoming mounted (React unmounted outgoing
  // synchronously); the outgoing fade animated nothing → hard cut + incoming
  // fade.
  const [pendingTab, setPendingTab] = useState<ProjectTab | null>(null);

  const theme = useProjectView((s) => s.theme);
  const selectedScreenID = useProjectView((s) => s.selectedScreenID);
  const setSelectedScreenID = useProjectView((s) => s.setSelectedScreenID);

  // ─── Refs ───────────────────────────────────────────────────────────────
  const scopeRef = useRef<HTMLDivElement>(null);
  // U14b — separate refs for outgoing vs incoming panels so the paired-
  // curtain tabSwitch timeline can animate two distinct DOM nodes during
  // the swap window.
  const outgoingPaneRef = useRef<HTMLDivElement>(null);
  const incomingPaneRef = useRef<HTMLDivElement>(null);
  const router = useRouter();
  const searchParams = useSearchParams();

  // Phase 3 U7: project-view state machine. Replaces Phase 1+2's ad-hoc
  // useState-based loading flow + the U7-lite ?read_only_preview check.
  // 9 plan states collapse into 6 top-level kinds (audit running/complete/
  // failed all map to view_ready with an `audit` discriminator). See
  // lib/projects/view-machine.ts for the full state diagram.
  const initialActiveVersion =
    initialVersions.find((v) => v.ID === initialActiveVersionID) ??
    initialVersions[0];
  const initialActiveStatus = initialActiveVersion?.Status ?? "view_ready";
  const [machineState, dispatch] = useReducer(
    projectViewReducer,
    {
      initialVersions,
      activeVersionStatus: initialActiveStatus,
      // T4 — feed the failed version's `Error` column + ID to the
      // initialState branch so the export_failed kind can render the
      // retry CTA with a real error message.
      activeVersionError: initialActiveVersion?.Error ?? "",
      activeVersionID: initialActiveVersion?.ID ?? "",
      permissionDeniedFromQuery:
        searchParams.get("read_only_preview") === "1",
    },
    initialMachineState,
  );

  // Audit progress derived from the machine — replaces the standalone
  // useState that U6 added. Threading through view-machine keeps a single
  // source of truth for the audit's running/complete state.
  const auditProgress = auditProgressFromState(machineState);

  // Phase 9 U5 — derive a flat audit-state discriminator for the toolbar
  // badge. The view-machine uses a nested AuditStatus inside view_ready;
  // ProjectToolbar only needs the four-way enum + an optional count or
  // error message to render the small AuditStateBadge.
  const auditBadge: ProjectToolbarAuditState = useMemo(() => {
    if (machineState.kind === "pending") {
      return { kind: "pending" };
    }
    if (machineState.kind !== "view_ready") {
      // loading / error / version_not_found / permission_denied — no
      // audit badge surfaces. The toolbar itself doesn't even render in
      // most of those branches; permission_denied is the exception and
      // there we hide the badge intentionally so the read-only banner
      // dominates.
      return { kind: "pending" };
    }
    if (machineState.audit.kind === "running") {
      return {
        kind: "running",
        completed: machineState.audit.completed,
        total: machineState.audit.total,
      };
    }
    if (machineState.audit.kind === "failed") {
      return { kind: "failed", error: machineState.audit.error };
    }
    return {
      kind: "complete",
      finalCount: machineState.audit.completedTotal,
    };
  }, [machineState]);

  // Phase 9 U5 — slow-render affordance. When the user opens a project
  // page that's still in `pending` (fresh export waiting on view_ready)
  // and the SSE event hasn't arrived, we surface a "Slow to render —
  // refresh?" affordance. Implemented as a piece of local state (not a
  // reducer kind) because it's strictly cosmetic — the underlying
  // machine state is still `pending` and a refresh is the user's choice,
  // not a state transition.
  //
  // Pr19: bumped 15s → 60s. Large exports (>100 frames) legitimately
  // take 30-60s end-to-end through Stage 3 (Figma image render) +
  // Stage 5 (canonical_trees) + Stage 7 (audit). Surfacing "slow to
  // render" too early trains users to refresh into a half-baked
  // pipeline. Configurable via `NEXT_PUBLIC_PROJECT_SLOW_RENDER_MS`
  // for environments that need a tighter SLA.
  const SLOW_RENDER_MS = (() => {
    const raw = process.env.NEXT_PUBLIC_PROJECT_SLOW_RENDER_MS;
    const parsed = raw ? Number.parseInt(raw, 10) : NaN;
    return Number.isFinite(parsed) && parsed > 0 ? parsed : 60_000;
  })();
  const [slowRender, setSlowRender] = useState(false);
  useEffect(() => {
    if (machineState.kind !== "pending") {
      setSlowRender(false);
      return;
    }
    const handle = window.setTimeout(() => {
      setSlowRender(true);
    }, SLOW_RENDER_MS);
    return () => window.clearTimeout(handle);
  }, [machineState.kind, SLOW_RENDER_MS]);

  // ─── GSAP context (shared scope for every component-scoped tween). ──────
  const ctx = useGSAPContext(scopeRef);

  // ─── Page-load timeline runs once, after first mount. ───────────────────
  useEffect(() => {
    if (!ctx) return;
    const scope = scopeRef.current;
    if (!scope) return;
    ctx.add(() => {
      const tl = projectShellOpen(scope);
      tl.play();
    });
    // Intentionally re-run only when the GSAP context handle changes — we
    // want a single timeline per mount, not on prop churn.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [ctx]);

  // ─── Tab switch animation — paired-curtain (U14b). ──────────────────────
  // When `pendingTab` is set we have BOTH outgoing (`activeTab`) and
  // incoming (`pendingTab`) panels mounted. We run the tabSwitch timeline
  // against both DOM nodes so the outgoing fade-up actually animates real
  // DOM (pre-U14b it was dead code — outgoing was already unmounted by the
  // time the effect fired). On completion we promote pendingTab into
  // activeTab so React tears down the old panel.
  useEffect(() => {
    if (!ctx) return;
    if (!pendingTab || pendingTab === activeTab) return;
    const outgoing = outgoingPaneRef.current;
    const incoming = incomingPaneRef.current;
    if (!incoming) {
      // Defensive: if for some reason the incoming pane didn't mount this
      // tick, promote immediately so we don't leave the UI stuck on the
      // outgoing tab.
      setActiveTab(pendingTab);
      setPendingTab(null);
      return;
    }
    let cancelled = false;
    ctx.add(() => {
      const tl = tabSwitch(outgoing, incoming);
      tl.eventCallback("onComplete", () => {
        if (cancelled) return;
        // Clear inline styles GSAP set on the panes before React promotes
        // pendingTab → activeTab. The outgoing pane element will be reused
        // for the new activeTab content (single-pane render after the swap),
        // so its opacity:0/transform from the outgoing tween must be cleared
        // or the user would see a blank pane. The incoming pane unmounts on
        // the same render so it's only cleared defensively for symmetry.
        if (outgoing) gsap.set(outgoing, { clearProps: "all" });
        if (incoming) gsap.set(incoming, { clearProps: "all" });
        setActiveTab((curr) => (pendingTab && !cancelled ? pendingTab : curr));
        setPendingTab(null);
      });
      tl.play();
    });
    return () => {
      cancelled = true;
    };
  }, [pendingTab, activeTab, ctx]);

  // ─── URL hash sync (read on mount + hashchange). ────────────────────────
  // Pr14: hash-driven tab changes go through the same pendingTab lock as
  // click-driven changes. If a swap is already in flight (`pendingTab !==
  // null`), drop the hashchange — the next user gesture or a fresh
  // hashchange after the lock clears will resolve. This avoids two
  // tabSwitch timelines fighting over the same DOM.
  useEffect(() => {
    if (typeof window === "undefined") return;
    // Pr28 — first hash apply (initial mount) bypasses the tabSwitch
    // animation: jump straight to the URL-encoded tab without playing
    // the curtain wipe. Without this guard a fresh /projects/<slug>#tab=drd
    // load shows a flash of "violations" → curtain wipe → drd, instead of
    // landing on drd silently. Subsequent hashchanges still animate.
    let isFirstApply = true;
    const apply = () => {
      const { tab, persona } = parseHash(window.location.hash);
      if (tab) {
        if (isFirstApply) {
          // Snap directly to the hash tab — no pendingTab, no animation.
          setActiveTab(tab);
        } else {
          setActiveTab((curr) => {
            if (curr === tab) return curr;
            setPendingTab((p) => {
              // Lock: if a swap is already pending, don't overwrite it.
              // The lock matches changeTab()'s `if (pendingTab) return;` guard.
              if (p !== null) return p;
              return tab;
            });
            return curr;
          });
        }
      }
      setActivePersonaName(persona);
      isFirstApply = false;
    };
    apply();
    window.addEventListener("hashchange", apply);
    return () => window.removeEventListener("hashchange", apply);
  }, []);

  // ─── Theme: apply to documentElement — mirrors FilesShell pattern. ──────
  // Pr27 — restore the prior data-theme on unmount so the project view's
  // override doesn't leak into other routes (e.g. /atlas which has its own
  // theme bootstrap reading the same attribute). The root layout's inline
  // bootstrap script set the initial value from localStorage; we snapshot
  // it on mount and restore on unmount.
  useEffect(() => {
    if (typeof document === "undefined") return;
    const prior = document.documentElement.getAttribute("data-theme");
    const concrete = resolveTheme(theme);
    document.documentElement.setAttribute("data-theme", concrete);
    return () => {
      if (prior !== null) {
        document.documentElement.setAttribute("data-theme", prior);
      }
    };
  }, [theme]);

  // Re-evaluate Auto on system change so toggling the OS-level dark mode
  // updates the page even without remounting.
  useEffect(() => {
    if (theme !== "auto") return;
    if (typeof window === "undefined") return;
    if (typeof window.matchMedia !== "function") return;
    const mq = window.matchMedia("(prefers-color-scheme: dark)");
    const handler = () => {
      document.documentElement.setAttribute(
        "data-theme",
        mq.matches ? "dark" : "light",
      );
    };
    mq.addEventListener("change", handler);
    return () => mq.removeEventListener("change", handler);
  }, [theme]);

  // ─── Phase 9 U3 — Esc reverse-morph back to /atlas. ─────────────────────
  // Pressing Escape (or browser back) returns the user to /atlas with the
  // source flow leaf re-focused at the same zoom level. The View Transitions
  // morph plays in reverse automatically (the same `view-transition-name`
  // tagged elements from U2b are the source/target — browser handles direc-
  // tion). Reduced-motion users still navigate; the browser falls through
  // to an instant swap with no morph. Spatial-continuity cue for those
  // users is the static breadcrumb on the toolbar (U2c).
  //
  // We must NOT swallow Escape when the user is actively editing — DRD
  // editor (textarea / contenteditable), version selector dropdown, decision
  // form inputs, and any modal-open container should handle Escape locally.
  // The check filters by `e.target.tagName` for INPUT/TEXTAREA/SELECT and
  // by `closest('[contenteditable], [data-modal-open], [role="dialog"]')`
  // so child portals / nested editors are respected.
  useEffect(() => {
    if (typeof window === "undefined") return;
    let firedAt = 0;
    function isEditableTarget(target: EventTarget | null): boolean {
      if (!target || !(target instanceof HTMLElement)) return false;
      const tag = target.tagName;
      if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT") return true;
      if (target.isContentEditable) return true;
      // Modals / dialogs / dropdowns own Escape — do not navigate.
      if (
        target.closest(
          '[contenteditable="true"], [data-modal-open="true"], [role="dialog"], [role="listbox"], [aria-modal="true"]',
        )
      ) {
        return true;
      }
      return false;
    }
    function onKeyDown(e: KeyboardEvent): void {
      if (e.key !== "Escape") return;
      if (e.defaultPrevented) return;
      if (isEditableTarget(e.target)) return;
      // Debounce double-tap so one press doesn't fire router.back() twice.
      const now = Date.now();
      if (now - firedAt < 300) return;
      firedAt = now;
      e.preventDefault();
      // Honor `?from=<flow_id>` (set by LeafMorphHandoff during the morph).
      // When present, navigate explicitly to /atlas with the source flow
      // as a focus hint — deterministic, deep-link-safe. Falls back to
      // router.back() when no marker exists (e.g., direct URL load).
      const fromFlow = searchParams.get("from");
      if (fromFlow) {
        router.push(`/atlas?focus=${encodeURIComponent(fromFlow)}`);
      } else {
        router.back();
      }
    }
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [router, searchParams]);

  // ─── Refresh on version change. Re-fetches the project payload. ─────────
  useEffect(() => {
    if (!activeVersionID) return;
    let cancelled = false;
    void fetchProject(slug, activeVersionID).then((r) => {
      if (cancelled) return;
      if (!r.ok) {
        // Phase 3 U8 stale-version handling preserved + extended through
        // the U7 state machine. A 404 dispatches fetch_failed with the
        // requested version ID so the reducer lands in
        // version_not_found; the redirect-to-latest effect below picks
        // it up and clears activeVersionID. Other errors land in
        // `error` for the RetryableError path.
        dispatch({
          type: "fetch_failed",
          statusCode: r.status,
          message: r.error,
          requestedVersionID: activeVersionID,
        });
        return;
      }
      if (r.data.versions) setVersions(r.data.versions);
      if (r.data.screens) setScreens(r.data.screens);
      if (r.data.screen_modes) setScreenModes(r.data.screen_modes);
      if (r.data.available_personas)
        setPersonas(r.data.available_personas);
      // Pass the active version's status + error into the reducer so it
      // lands in pending / view_ready / export_failed correctly.
      const av = r.data.versions?.find((v) => v.ID === activeVersionID);
      dispatch({
        type: "fetch_succeeded",
        versions: r.data.versions ?? [],
        activeVersionStatus: av?.Status ?? "view_ready",
        activeVersionError: av?.Error ?? "",
        activeVersionID: av?.ID ?? "",
        readOnly: searchParams.get("read_only_preview") === "1",
      });
    });
    return () => {
      cancelled = true;
    };
  }, [slug, activeVersionID, searchParams]);

  // Phase 3 U7: version_not_found recovery — when the reducer lands in
  // that state, redirect to the latest version by setting activeVersionID
  // to versions[0]. The next refresh dispatches fetch_succeeded and the
  // reducer transitions out of version_not_found into view_ready.
  useEffect(() => {
    if (machineState.kind !== "version_not_found") return;
    if (versions.length === 0) return;
    const latest = versions[0];
    if (latest && latest.ID !== machineState.requestedVersionID) {
      setActiveVersionID(latest.ID);
    }
  }, [machineState, versions]);

  // ─── SSE subscription. ──────────────────────────────────────────────────
  // Pr11: only open the EventSource when there's actually something to wait
  // for — either a `pending` version (in-flight pipeline) or an explicit
  // trace ID (fresh export deeplink). Passive views over `view_ready`
  // versions previously held an idle EventSource open per tab; they now
  // skip subscription entirely.
  const traceFromQuery = searchParams.get("trace");
  const sseShouldOpen =
    machineState.kind === "pending" ||
    Boolean(initialTraceID) ||
    Boolean(traceFromQuery);
  useEffect(() => {
    if (typeof window === "undefined") return;
    if (!sseShouldOpen) return;
    // Use the URL-supplied trace ID when present (fresh export deeplink), or
    // a synthetic one for passive views. A synthetic trace gets only heart-
    // beats from the broker (no events match), which is fine for U6.
    const traceID =
      initialTraceID ??
      traceFromQuery ??
      (typeof crypto !== "undefined" && "randomUUID" in crypto
        ? crypto.randomUUID()
        : `trace-${Date.now()}-${Math.random().toString(36).slice(2)}`);

    const unsubscribe = subscribeProjectEvents(slug, traceID, (ev) => {
      if (ev.type === "audit_complete") {
        // Phase 3 U7 + U5: dispatch into the state machine so audit-
        // running transitions cleanly to audit_complete + the Violations
        // tab swaps from progress UI to the populated list. The final
        // violation count (when emitted by the broker) hydrates the
        // toolbar's count badge so the user sees an immediate "N
        // violations" without re-fetching the violations endpoint.
        const finalCount =
          typeof ev.data?.violation_count === "number"
            ? ev.data.violation_count
            : typeof ev.data?.count === "number"
              ? ev.data.count
              : undefined;
        dispatch({ type: "audit_complete", finalCount });
        showToast({
          message: "Audit complete",
          tone: "success",
          detail: "Refresh the Violations tab for the latest run",
        });
      } else if (ev.type === "audit_failed") {
        const error =
          typeof ev.data?.error === "string" ? ev.data.error : "audit failed";
        dispatch({ type: "audit_failed", error });
        showToast({ message: "Audit failed", tone: "danger" });
      } else if (ev.type === "view_ready") {
        // Phase 3 U7: pending → view_ready transition.
        dispatch({ type: "view_ready" });
        showToast({ message: "View ready", tone: "info" });
      } else if (ev.type === "export_failed") {
        const error =
          typeof ev.data?.error === "string" ? ev.data.error : "export failed";
        dispatch({ type: "export_failed", error });
        showToast({ message: "Export failed", tone: "danger" });
      } else if (ev.type === "audit_progress") {
        // Phase 3 U6 + U7 + U5: per-rule progress tick → reducer. We
        // capture Date.now() at receive time so the reducer can drop
        // stale (out-of-order) ticks deterministically.
        const completed =
          typeof ev.data?.completed === "number" ? ev.data.completed : 0;
        const total =
          typeof ev.data?.total === "number" ? ev.data.total : 0;
        if (total > 0) {
          dispatch({
            type: "audit_progress",
            completed,
            total,
            receivedAt: Date.now(),
          });
        }
      }
    });
    return unsubscribe;
  }, [slug, initialTraceID, traceFromQuery, sseShouldOpen]);

  // ─── Persona deeplink → write to hash so reload preserves it. ────────────
  function changePersona(name: string | null): void {
    setActivePersonaName(name);
    if (typeof window === "undefined") return;
    window.location.hash = buildHash(activeTab, name);
  }

  function changeTab(tab: ProjectTab): void {
    if (tab === activeTab) return;
    // U14b — stage as pendingTab. The tab-switch useEffect mounts both
    // outgoing + incoming panels, runs the paired-curtain timeline, and
    // promotes pendingTab → activeTab on completion. Reduced-motion users
    // still go through the same path; tabSwitch handles the snap-style
    // render internally.
    //
    // While a swap is in flight (pendingTab !== null) we ignore further
    // changes to avoid two timelines fighting on the same outgoing DOM
    // node. The whole curtain is ~330ms — a brief block. Repeated rapid
    // clicks therefore latch on the first target rather than thrashing.
    if (pendingTab) return;
    setPendingTab(tab);
    if (typeof window === "undefined") return;
    window.location.hash = buildHash(tab, activePersonaName);
  }

  // Phase 6 U7 — Violations → Decisions cross-link. ViolationsTab calls
  // this when the user clicks "View decision" on a row whose decision_link
  // points at a known decision. We reuse the existing Phase 5.1
  // `?decision=<id>` deep-link pattern so DecisionsTab's outline-pulse
  // effect highlights the target without a second mechanism.
  function viewDecisionFromViolation(decisionID: string): void {
    const params = new URLSearchParams(searchParams.toString());
    params.set("decision", decisionID);
    // Strip any prior violation highlight so the inverse cross-link
    // (Decisions → Violations) doesn't fire on this navigation.
    params.delete("violation");
    // Pr17: use router.push (not replace) so back-button returns the
    // user to the prior tab/highlight state. The cross-link is a real
    // navigation gesture; replace was eating history.
    router.push(`/projects/${slug}?${params.toString()}`);
    if (activeTab !== "decisions") {
      changeTab("decisions");
    }
  }

  // Phase 6 U7 — Decisions → Violations cross-link. DecisionCard calls
  // this when the user clicks "View" on a linked-violation row. We
  // surface a `?violation=<id>` query param + flip to the Violations
  // tab; ViolationsTab reads the URL param via highlightedViolationID
  // and runs the same outline-pulse pattern that the Decisions side
  // ships.
  function viewViolationFromDecision(violationID: string, _screenID: string): void {
    const params = new URLSearchParams(searchParams.toString());
    params.set("violation", violationID);
    params.delete("decision");
    // Pr17: router.push so back-button restores the prior tab/state.
    router.push(`/projects/${slug}?${params.toString()}`);
    if (activeTab !== "violations") {
      changeTab("violations");
    }
  }

  // Read the active violation-highlight target from the URL so reload
  // + back-navigation re-trigger the pulse.
  const highlightedViolationID = searchParams.get("violation");

  // U14b — Render the body of a single tab. Used twice during a swap (one
  // for outgoing, one for incoming) so the paired-curtain timeline has two
  // real DOM nodes to animate. Pre-U14b only the active tab rendered, so
  // outgoing was already gone by the time the GSAP timeline ran.
  function renderTabBody(tab: ProjectTab) {
    if (tab === "drd") {
      return DRD_COLLAB_ENABLED && selectedFlowID ? (
        <DRDTabCollab
          slug={slug}
          flowID={selectedFlowID}
          readOnly={isReadOnly(machineState)}
        />
      ) : (
        <DRDTab
          slug={slug}
          flowID={selectedFlowID}
          readOnly={isReadOnly(machineState)}
        />
      );
    }
    if (tab === "violations") {
      return (
        <ViolationsTab
          slug={slug}
          versionID={activeVersionID}
          flowID={selectedFlowID}
          filters={
            activePersonaName
              ? {
                  persona_id:
                    personas.find((p) => p.Name === activePersonaName)?.ID,
                }
              : undefined
          }
          onViewInJSON={() => changeTab("json")}
          auditProgress={auditProgress}
          personas={personas}
          onViewDecision={viewDecisionFromViolation}
          highlightedViolationID={highlightedViolationID}
        />
      );
    }
    if (tab === "decisions") {
      return (
        <DecisionsTab
          slug={slug}
          flowID={selectedFlowID}
          readOnly={isReadOnly(machineState)}
          onViewViolation={viewViolationFromDecision}
        />
      );
    }
    if (tab === "json") {
      return (
        <JSONTab
          slug={project.Slug}
          screens={screens}
          screenModes={screenModes}
        />
      );
    }
    return null;
  }

  function changeVersion(id: string): void {
    setActiveVersionID(id);
    // Reflect in the search param so reload keeps the version pinned.
    const params = new URLSearchParams(searchParams.toString());
    params.set("v", id);
    router.replace(`/projects/${slug}?${params.toString()}`);
  }

  // Filtered screens by active persona name. Persona resolution from name to
  // ID is done by matching against the persona library; if no match, no filter.
  //
  // Pr18: real filtering via screens → flows.PersonaID join. Screens don't
  // carry a persona linkage directly — it lives on the parent flow. We
  // build a Set of flow IDs whose `PersonaID` matches the active persona,
  // then keep only screens whose `FlowID` is in that set. When a flow has
  // no PersonaID assigned, it's excluded from the filtered view (the
  // persona toggle is exclusive — "no persona" means "all").
  const filteredScreens = useMemo(() => {
    if (!activePersonaName) return screens;
    const persona = personas.find((p) => p.Name === activePersonaName);
    if (!persona) return screens;
    const flowIDsForPersona = new Set(
      flows.filter((f) => f.PersonaID === persona.ID).map((f) => f.ID),
    );
    if (flowIDsForPersona.size === 0) return screens;
    return screens.filter((s) => flowIDsForPersona.has(s.FlowID));
  }, [screens, personas, activePersonaName, flows]);

  // Phase 3 U7: top-level state-machine render branches. Five non-shell
  // states each map to a dedicated EmptyState variant; shell-rendering
  // states (view_ready + permission_denied) fall through to the regular
  // toolbar + atlas + tabs layout below. The shell still respects
  // isReadOnly(machineState) for the DRD tab's edit affordances.
  if (!shouldRenderShell(machineState)) {
    return (
      <div style={fullPageStateStyle}>
        {machineState.kind === "loading" ? (
          <EmptyState variant="loading" title="Loading project…" />
        ) : null}

        {machineState.kind === "pending" ? (
          // U5 — slow-render affordance. After SLOW_RENDER_MS in `pending` (the
          // SSE view_ready event hasn't arrived), surface a manual-
          // refresh CTA so the user has a way out if the backend is
          // genuinely stuck. We don't auto-refresh because the
          // pipeline may still be making progress server-side; a hard
          // refresh would discard partial work the user can see.
          <EmptyState
            variant="loading"
            title="Project landing…"
            description={
              slowRender
                ? "This is taking longer than usual. Slow to render — refresh to retry, or wait for the backend to catch up."
                : "Backend pipeline is finishing the fast preview. Atlas + DRD render here as soon as it's ready."
            }
            action={
              slowRender ? (
                <button
                  type="button"
                  data-testid="project-pending-refresh"
                  onClick={() => {
                    if (typeof window !== "undefined") {
                      window.location.reload();
                    }
                  }}
                  style={{
                    padding: "6px 14px",
                    fontSize: 12,
                    fontFamily: "var(--font-mono)",
                    background: "var(--bg)",
                    color: "var(--text-1)",
                    border: "1px solid var(--border)",
                    borderRadius: 6,
                    cursor: "pointer",
                  }}
                >
                  Refresh
                </button>
              ) : null
            }
          />
        ) : null}

        {machineState.kind === "version_not_found" ? (
          <EmptyState
            variant="error"
            title="Version not found"
            description={`v${machineState.requestedVersionID.slice(
              0,
              8,
            )}… doesn't exist anymore — redirecting to the latest version.`}
          />
        ) : null}

        {machineState.kind === "error" ? (
          <RetryableError
            title="Couldn't load this project"
            detail={`${machineState.message} (status ${machineState.statusCode})`}
            onRetry={async () => {
              dispatch({ type: "retry" });
              const r = await fetchProject(slug, activeVersionID);
              if (r.ok) {
                if (r.data.versions) setVersions(r.data.versions);
                if (r.data.screens) setScreens(r.data.screens);
                if (r.data.screen_modes) setScreenModes(r.data.screen_modes);
                if (r.data.available_personas)
                  setPersonas(r.data.available_personas);
                const av = r.data.versions?.find(
                  (v) => v.ID === activeVersionID,
                );
                dispatch({
                  type: "fetch_succeeded",
                  versions: r.data.versions ?? [],
                  activeVersionStatus: av?.Status ?? "view_ready",
                  activeVersionError: av?.Error ?? "",
                  activeVersionID: av?.ID ?? "",
                  readOnly: searchParams.get("read_only_preview") === "1",
                });
              } else {
                throw new Error(`${r.error} (${r.status})`);
              }
            }}
            offline={machineState.statusCode === 0}
          />
        ) : null}

        {machineState.kind === "export_failed" ? (
          <RetryableError
            title="Render failed"
            detail={
              machineState.error
                ? `Pipeline error: ${machineState.error}`
                : "The Figma render pipeline failed for this version. Click retry to re-run it; the original frame snapshot is reused, so no plugin re-export needed."
            }
            onRetry={async () => {
              if (!machineState.versionID) {
                throw new Error("missing version_id");
              }
              dispatch({ type: "retry" });
              const dsURL =
                process.env.NEXT_PUBLIC_DS_SERVICE_URL || "http://localhost:8080";
              const token =
                typeof window !== "undefined"
                  ? JSON.parse(
                      localStorage.getItem("indmoney-ds-auth") || "{}",
                    )?.state?.token
                  : "";
              const retryRes = await fetch(
                `${dsURL}/v1/projects/${slug}/versions/${machineState.versionID}/retry`,
                {
                  method: "POST",
                  headers: {
                    Authorization: `Bearer ${token}`,
                    "Content-Type": "application/json",
                  },
                },
              );
              if (!retryRes.ok) {
                const body = await retryRes.text().catch(() => "");
                throw new Error(`retry failed (${retryRes.status}): ${body}`);
              }
              // Pipeline runs async on the backend. Re-fetch the project
              // payload — the version's status will now be `pending`, and
              // the SSE listener already wired in this shell will flip the
              // machine to view_ready when the pipeline lands `view_ready`.
              const r = await fetchProject(slug, activeVersionID);
              if (r.ok) {
                if (r.data.versions) setVersions(r.data.versions);
                if (r.data.screens) setScreens(r.data.screens);
                if (r.data.screen_modes) setScreenModes(r.data.screen_modes);
                if (r.data.available_personas)
                  setPersonas(r.data.available_personas);
                const av = r.data.versions?.find(
                  (v) => v.ID === activeVersionID,
                );
                dispatch({
                  type: "fetch_succeeded",
                  versions: r.data.versions ?? [],
                  activeVersionStatus: av?.Status ?? "pending",
                  activeVersionError: av?.Error ?? "",
                  activeVersionID: av?.ID ?? "",
                  readOnly: searchParams.get("read_only_preview") === "1",
                });
              }
            }}
          />
        ) : null}
      </div>
    );
  }

  return (
    <div
      ref={scopeRef}
      style={{
        display: "flex",
        flexDirection: "column",
        height: "100vh",
        minHeight: 0,
        background: "var(--bg)",
        color: "var(--text-1)",
      }}
    >
      {machineState.kind === "permission_denied" ? (
        <div style={permissionDeniedBannerStyle} role="status">
          <span aria-hidden style={{ fontSize: 14 }}>🔒</span>
          <span style={{ flex: 1 }}>
            You're viewing this project in read-only mode
            {machineState.reason === "preview"
              ? " (preview)"
              : ""}
            . Request edit access from the project owner.
          </span>
          <Link
            href="/onboarding/pm"
            style={permissionDeniedLinkStyle}
          >
            Why am I seeing this?
          </Link>
        </div>
      ) : null}

      <ProjectToolbar
        slug={slug}
        project={project}
        versions={versions}
        activeVersionID={activeVersionID}
        onVersionChange={changeVersion}
        personas={personas}
        activePersonaName={activePersonaName}
        onPersonaChange={changePersona}
        flowName={initialProject.Name}
        auditState={auditBadge}
        onAuditRetry={() => {
          // U5: the retry CTA on a failed-audit badge re-fetches the
          // project payload, which dispatches `fetch_succeeded` and
          // resets the audit sub-state. Phase 6 will replace this with
          // a dedicated POST /audit/retry endpoint once the broker
          // supports it.
          if (!activeVersionID) return;
          void fetchProject(slug, activeVersionID).then((r) => {
            if (!r.ok) return;
            if (r.data.versions) setVersions(r.data.versions);
            const av = r.data.versions?.find((v) => v.ID === activeVersionID);
            dispatch({
              type: "fetch_succeeded",
              versions: r.data.versions ?? [],
              activeVersionStatus: av?.Status ?? "view_ready",
              activeVersionError: av?.Error ?? "",
              activeVersionID: av?.ID ?? "",
              readOnly: searchParams.get("read_only_preview") === "1",
            });
          });
        }}
      />

      {/* Phase 3 U11: 4-step Shepherd.js tour. Mounts only for
          first-time visitors (or when ?reset-tour=1 is in the URL).
          The component lazy-loads Shepherd + its CSS so we pay zero
          bundle cost on subsequent visits. */}
      <ProductTour searchParams={searchParams} />

      {/* Atlas slot — top half. r3f canvas (U7) replaces the U6 PNG grid. */}
      <div
        data-anim="atlas-canvas"
        style={{
          flex: "1 1 50%",
          minHeight: 0,
          overflow: "hidden",
          borderBottom: "1px solid var(--border)",
          background:
            "color-mix(in srgb, var(--bg) 92%, var(--text-1) 8%)",
          position: "relative",
        }}
      >
        {filteredScreens.length === 0 ? (
          <div
            style={{
              display: "grid",
              placeItems: "center",
              height: "100%",
              color: "var(--text-3)",
              fontFamily: "var(--font-mono)",
              fontSize: 12,
            }}
          >
            No screens yet. Export from the Figma plugin to populate this canvas.
          </div>
        ) : (
          <AtlasCanvas
            slug={slug}
            screens={filteredScreens}
            versionID={activeVersionID}
            selectedScreenID={selectedScreenID}
            onFrameSelect={(screenID) => {
              setSelectedScreenID(screenID);
              // Switching to JSON tab notifies U8's tab to load this screen.
              if (activeTab !== "json") changeTab("json");
            }}
          />
        )}
      </div>

      {/* Bottom half: tab strip + tab content. */}
      <div
        style={{
          flex: "1 1 50%",
          minHeight: 0,
          display: "flex",
          flexDirection: "column",
        }}
      >
        {flows.length > 1 && (
          <div
            role="radiogroup"
            aria-label="Active flow"
            style={{
              display: "flex",
              gap: 6,
              padding: "8px 16px 4px",
              alignItems: "center",
              background: "var(--bg-surface)",
              flexWrap: "wrap",
            }}
          >
            <span
              style={{
                fontSize: 11,
                fontFamily: "var(--font-mono)",
                color: "var(--text-3)",
                marginRight: 4,
                letterSpacing: "0.04em",
                textTransform: "uppercase",
              }}
            >
              Flow:
            </span>
            {flows.map((f) => {
              const isActive = f.ID === selectedFlowID;
              const screenCount = screens.filter((s) => s.FlowID === f.ID).length;
              return (
                <button
                  key={f.ID}
                  type="button"
                  role="radio"
                  aria-checked={isActive}
                  onClick={() => setSelectedFlowID(f.ID)}
                  style={{
                    padding: "4px 10px",
                    fontSize: 12,
                    fontFamily: "var(--font-mono)",
                    background: isActive ? "var(--text-1)" : "transparent",
                    color: isActive ? "var(--bg-canvas)" : "var(--text-2)",
                    border: `1px solid ${isActive ? "var(--text-1)" : "var(--border)"}`,
                    borderRadius: 999,
                    cursor: "pointer",
                  }}
                >
                  {f.Name}{" "}
                  <span
                    style={{
                      opacity: 0.6,
                      marginLeft: 4,
                      fontSize: 10,
                    }}
                  >
                    {screenCount}
                  </span>
                </button>
              );
            })}
          </div>
        )}
        <div
          data-anim="tab-strip"
          role="tablist"
          aria-label="Project view tabs"
          style={{
            display: "flex",
            gap: 4,
            padding: "8px 16px 0",
            borderBottom: "1px solid var(--border)",
            background: "var(--bg-surface)",
          }}
        >
          {TAB_DEFINITIONS.map((tab) => {
            const active = activeTab === tab.id;
            return (
              <button
                key={tab.id}
                type="button"
                role="tab"
                id={`tab-${tab.id}`}
                data-tour={`tab-${tab.id}`}
                aria-selected={active}
                aria-controls={`tabpanel-${tab.id}`}
                onClick={() => changeTab(tab.id)}
                style={{
                  position: "relative",
                  padding: "8px 14px",
                  fontSize: 12,
                  fontFamily: "var(--font-mono)",
                  background: "transparent",
                  border: "none",
                  borderBottom: active
                    ? "2px solid var(--text-1)"
                    : "2px solid transparent",
                  color: active ? "var(--text-1)" : "var(--text-3)",
                  cursor: "pointer",
                  marginBottom: -1,
                }}
              >
                {tab.label}
              </button>
            );
          })}
        </div>
        {/* U14b — paired-curtain tab swap. While pendingTab is set, BOTH
            outgoing and incoming panes are mounted, stacked absolutely in
            the same container, so the GSAP tabSwitch timeline animates
            real DOM for both fade tracks. On timeline complete, pendingTab
            is promoted into activeTab and the outgoing pane unmounts. */}
        {/* Pr22: outer keeps `position:relative` + `min-height:0` so the
            inner pane can scroll inside the bottom-half flex slot. When a
            tab swap is in flight (`pendingTab` set), BOTH panes need to
            occupy the same box for the paired-curtain timeline; we
            switch the outgoing pane to absolute positioning ONLY in that
            window. In the steady state (single pane mounted) the
            outgoing pane uses `position: relative; height: 100%` so long
            DRD / JSON content sizes naturally and scrolls inside the
            bordered slot rather than getting clipped by `overflow:hidden`.
            `overflow:hidden` stays on the outer container so the
            absolute-positioned incoming pane can't bleed past the slot
            during the swap fade. */}
        <div
          data-anim="tab-content"
          style={{
            flex: 1,
            minHeight: 0,
            overflow: "hidden",
            position: "relative",
            display: "flex",
            flexDirection: "column",
          }}
        >
          <div
            ref={outgoingPaneRef}
            role="tabpanel"
            id={`tabpanel-${activeTab}`}
            aria-labelledby={`tab-${activeTab}`}
            style={
              pendingTab
                ? {
                    position: "absolute",
                    inset: 0,
                    overflow: "auto",
                    padding: 16,
                    // Outgoing is fading out — block clicks so users
                    // can't interact with the about-to-disappear tab
                    // during the swap.
                    pointerEvents: "none",
                  }
                : {
                    position: "relative",
                    flex: 1,
                    minHeight: 0,
                    overflow: "auto",
                    padding: 16,
                  }
            }
          >
            {renderTabBody(activeTab)}
          </div>
          {pendingTab && pendingTab !== activeTab ? (
            <div
              ref={incomingPaneRef}
              role="tabpanel"
              id={`tabpanel-${pendingTab}`}
              aria-labelledby={`tab-${pendingTab}`}
              style={{
                position: "absolute",
                inset: 0,
                overflow: "auto",
                padding: 16,
              }}
            >
              {renderTabBody(pendingTab)}
            </div>
          ) : null}
        </div>
      </div>
    </div>
  );
}

// AtlasPlaceholder removed in U7 — r3f `<AtlasCanvas>` (above) is the new
// surface. The PNG-grid placeholder is preserved in git history (U6 commit)
// for reference if anyone needs to bisect the visual regression of the swap.

// ─── Phase 3 U7 — full-page state styles ────────────────────────────────────

const fullPageStateStyle: React.CSSProperties = {
  display: "flex",
  alignItems: "center",
  justifyContent: "center",
  height: "100vh",
  background: "var(--bg)",
  padding: 32,
};

const permissionDeniedBannerStyle: React.CSSProperties = {
  display: "flex",
  alignItems: "center",
  gap: 12,
  padding: "8px 16px",
  background:
    "color-mix(in oklab, var(--bg-surface) 85%, var(--warning, #c80) 15%)",
  borderBottom: "1px solid var(--border)",
  fontSize: 12,
  fontFamily: "var(--font-mono)",
  color: "var(--text-1)",
};

const permissionDeniedLinkStyle: React.CSSProperties = {
  fontSize: 11,
  color: "var(--accent)",
  textDecoration: "underline",
};
