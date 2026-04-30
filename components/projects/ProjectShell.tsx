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
  useRef,
  useState,
} from "react";
import dynamic from "next/dynamic";
import { useRouter, useSearchParams } from "next/navigation";
import { showToast } from "@/components/ui/Toast";
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
import ProjectToolbar from "./ProjectToolbar";
import DRDTab from "./tabs/DRDTab";
import DecisionsTab from "./tabs/DecisionsTab";
import JSONTab from "./tabs/JSONTab";
import ViolationsTab from "./tabs/ViolationsTab";

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
      if (!r.ok) return; // toolbar already shows an error path via the version selector
      if (r.data.versions) setVersions(r.data.versions);
      if (r.data.screens) setScreens(r.data.screens);
      if (r.data.screen_modes) setScreenModes(r.data.screen_modes);
      if (r.data.available_personas)
        setPersonas(r.data.available_personas);
    });
    return () => {
      cancelled = true;
    };
  }, [slug, activeVersionID]);

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
        showToast({
          message: "Audit complete",
          tone: "success",
          detail: "Refresh the Violations tab for the latest run",
        });
      } else if (ev.type === "audit_failed") {
        showToast({ message: "Audit failed", tone: "danger" });
      } else if (ev.type === "view_ready") {
        showToast({ message: "View ready", tone: "info" });
      } else if (ev.type === "export_failed") {
        showToast({ message: "Export failed", tone: "danger" });
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
      <ProjectToolbar
        project={project}
        versions={versions}
        activeVersionID={activeVersionID}
        onVersionChange={changeVersion}
        personas={personas}
        activePersonaName={activePersonaName}
        onPersonaChange={changePersona}
      />

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
            <DRDTab slug={slug} flowID={screens[0]?.FlowID ?? null} />
          )}
          {activeTab === "violations" && (
            <ViolationsTab
              slug={slug}
              versionID={activeVersionID}
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
            />
          )}
          {activeTab === "decisions" && <DecisionsTab />}
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
