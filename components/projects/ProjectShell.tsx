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
import ProjectToolbar from "./ProjectToolbar";
import DRDTab from "./tabs/DRDTab";
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
  initialScreens,
  initialPersonas,
  initialScreenModes,
  initialTraceID,
}: ProjectShellProps) {
  // ─── State ──────────────────────────────────────────────────────────────
  const [project] = useState<Project>(initialProject);
  const [versions, setVersions] = useState<ProjectVersion[]>(initialVersions);
  const [screens, setScreens] = useState<Screen[]>(initialScreens);
  const [personas, setPersonas] = useState<Persona[]>(initialPersonas);
  const [screenModes, setScreenModes] = useState<ScreenMode[]>(initialScreenModes);
  const [activeVersionID, setActiveVersionID] = useState<string | undefined>(
    initialActiveVersionID ?? initialVersions[0]?.ID,
  );

  // Active tab + persona derived from URL hash on mount and on hashchange.
  const [activeTab, setActiveTab] = useState<ProjectTab>(DEFAULT_TAB);
  const [activePersonaName, setActivePersonaName] = useState<string | null>(
    null,
  );
  const [previousTab, setPreviousTab] = useState<ProjectTab | null>(null);

  const theme = useProjectView((s) => s.theme);
  const selectedScreenID = useProjectView((s) => s.selectedScreenID);
  const setSelectedScreenID = useProjectView((s) => s.setSelectedScreenID);

  // ─── Refs ───────────────────────────────────────────────────────────────
  const scopeRef = useRef<HTMLDivElement>(null);
  const tabContentRef = useRef<HTMLDivElement>(null);
  const router = useRouter();
  const searchParams = useSearchParams();

  // Phase 3 U7: project-view state machine. Replaces Phase 1+2's ad-hoc
  // useState-based loading flow + the U7-lite ?read_only_preview check.
  // 9 plan states collapse into 6 top-level kinds (audit running/complete/
  // failed all map to view_ready with an `audit` discriminator). See
  // lib/projects/view-machine.ts for the full state diagram.
  const initialActiveStatus =
    initialVersions.find((v) => v.ID === initialActiveVersionID)?.Status ??
    initialVersions[0]?.Status ??
    "view_ready";
  const [machineState, dispatch] = useReducer(
    projectViewReducer,
    {
      initialVersions,
      activeVersionStatus: initialActiveStatus,
      permissionDeniedFromQuery:
        searchParams.get("read_only_preview") === "1",
    },
    initialMachineState,
  );

  // Audit progress derived from the machine — replaces the standalone
  // useState that U6 added. Threading through view-machine keeps a single
  // source of truth for the audit's running/complete state.
  const auditProgress = auditProgressFromState(machineState);

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

  // ─── Tab switch animation — runs after `activeTab` changes. ─────────────
  useEffect(() => {
    if (!ctx) return;
    if (!previousTab || previousTab === activeTab) return;
    const incoming = tabContentRef.current;
    if (!incoming) return;
    ctx.add(() => {
      // Single-pane swap: the same DOM node is reused for outgoing+incoming
      // because we only render one tab at a time. We pass the same ref for
      // both so the tween runs as a fade-in (incoming branch) only.
      const tl = tabSwitch(null, incoming);
      tl.play();
    });
  }, [activeTab, previousTab, ctx]);

  // ─── URL hash sync (read on mount + hashchange). ────────────────────────
  useEffect(() => {
    if (typeof window === "undefined") return;
    const apply = () => {
      const { tab, persona } = parseHash(window.location.hash);
      if (tab) {
        setActiveTab((curr) => {
          if (curr === tab) return curr;
          setPreviousTab(curr);
          return tab;
        });
      }
      setActivePersonaName(persona);
    };
    apply();
    window.addEventListener("hashchange", apply);
    return () => window.removeEventListener("hashchange", apply);
  }, []);

  // ─── Theme: apply to documentElement — mirrors FilesShell pattern. ──────
  useEffect(() => {
    if (typeof document === "undefined") return;
    const concrete = resolveTheme(theme);
    document.documentElement.setAttribute("data-theme", concrete);
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
      // Pass the active version's status into the reducer so it lands in
      // pending / view_ready / view_ready+audit_failed correctly.
      const activeStatus =
        r.data.versions?.find((v) => v.ID === activeVersionID)?.Status ??
        "view_ready";
      dispatch({
        type: "fetch_succeeded",
        versions: r.data.versions ?? [],
        activeVersionStatus: activeStatus,
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
  useEffect(() => {
    if (typeof window === "undefined") return;
    // Use the URL-supplied trace ID when present (fresh export deeplink), or
    // a synthetic one for passive views. A synthetic trace gets only heart-
    // beats from the broker (no events match), which is fine for U6.
    const traceID =
      initialTraceID ??
      searchParams.get("trace") ??
      (typeof crypto !== "undefined" && "randomUUID" in crypto
        ? crypto.randomUUID()
        : `trace-${Date.now()}-${Math.random().toString(36).slice(2)}`);

    const unsubscribe = subscribeProjectEvents(slug, traceID, (ev) => {
      if (ev.type === "audit_complete") {
        // Phase 3 U7: dispatch into the state machine so audit-running
        // transitions cleanly to audit_complete + the Violations tab
        // swaps from progress UI to the populated list.
        dispatch({ type: "audit_complete" });
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
        // Phase 3 U6 + U7: per-rule progress tick → reducer.
        const completed =
          typeof ev.data?.completed === "number" ? ev.data.completed : 0;
        const total =
          typeof ev.data?.total === "number" ? ev.data.total : 0;
        if (total > 0) {
          dispatch({ type: "audit_progress", completed, total });
        }
      }
    });
    return unsubscribe;
  }, [slug, initialTraceID, searchParams]);

  // ─── Persona deeplink → write to hash so reload preserves it. ────────────
  function changePersona(name: string | null): void {
    setActivePersonaName(name);
    if (typeof window === "undefined") return;
    window.location.hash = buildHash(activeTab, name);
  }

  function changeTab(tab: ProjectTab): void {
    if (tab === activeTab) return;
    setPreviousTab(activeTab);
    setActiveTab(tab);
    if (typeof window === "undefined") return;
    window.location.hash = buildHash(tab, activePersonaName);
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
  const filteredScreens = useMemo(() => {
    if (!activePersonaName) return screens;
    const persona = personas.find((p) => p.Name === activePersonaName);
    if (!persona) return screens;
    // Phase 1 placeholder: screens carry no persona linkage yet (it lives on
    // the parent flow). We keep all screens for now; U7 wires real filtering
    // when the GET response surfaces flow → persona joins.
    return screens;
  }, [screens, personas, activePersonaName]);

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
          <EmptyState
            variant="loading"
            title="Project landing…"
            description="Backend pipeline is finishing the fast preview. Atlas + DRD render here as soon as it's ready."
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
                dispatch({
                  type: "fetch_succeeded",
                  versions: r.data.versions ?? [],
                  activeVersionStatus:
                    r.data.versions?.find((v) => v.ID === activeVersionID)
                      ?.Status ?? "view_ready",
                  readOnly: searchParams.get("read_only_preview") === "1",
                });
              } else {
                throw new Error(`${r.error} (${r.status})`);
              }
            }}
            offline={machineState.statusCode === 0}
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
        project={project}
        versions={versions}
        activeVersionID={activeVersionID}
        onVersionChange={changeVersion}
        personas={personas}
        activePersonaName={activePersonaName}
        onPersonaChange={changePersona}
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
        <div
          ref={tabContentRef}
          data-anim="tab-content"
          role="tabpanel"
          id={`tabpanel-${activeTab}`}
          aria-labelledby={`tab-${activeTab}`}
          style={{
            flex: 1,
            minHeight: 0,
            overflow: "auto",
            padding: 16,
          }}
        >
          {activeTab === "drd" && (
            <DRDTab
              slug={slug}
              flowID={screens[0]?.FlowID ?? null}
              // Phase 3 U7-lite: ?read_only_preview=1 simulates a Phase 7
              // ACL-denied path so designers can review the read-only UX
              // before per-resource grants ship. Phase 7 will replace
              // this query-param check with an actual permission check
              // resolved server-side in fetchProject's response.
              // Phase 3 U7: read-only resolves through the machine — covers
              // both the ?read_only_preview=1 preview path and (Phase 7) a
              // server-resolved ACL flag without re-reading the query param.
              readOnly={isReadOnly(machineState)}
            />
          )}
          {activeTab === "violations" && (
            <ViolationsTab
              slug={slug}
              versionID={activeVersionID}
              flowID={screens[0]?.FlowID ?? null}
              filters={
                activePersonaName
                  ? {
                      persona_id:
                        personas.find((p) => p.Name === activePersonaName)
                          ?.ID,
                    }
                  : undefined
              }
              onViewInJSON={() => changeTab("json")}
              auditProgress={auditProgress}
            />
          )}
          {activeTab === "decisions" && (
            <DecisionsTab
              slug={slug}
              flowID={screens[0]?.FlowID ?? null}
              readOnly={isReadOnly(machineState)}
            />
          )}
          {activeTab === "json" && (
            <JSONTab
              slug={project.Slug}
              screens={screens}
              screenModes={screenModes}
            />
          )}
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
