"use client";

/**
 * lib/drd/customBlocks.ts — Phase 5.1 P2.
 *
 * Custom BlockNote block specs for the DRD's slash menu:
 *
 *   /decision      — embeds a Decision card. The block stores
 *                    decision_id; the renderer pulls the decision via
 *                    fetchDecision and renders DecisionCard inline.
 *   /figma-link    — paste any Figma URL; block stores the URL + a
 *                    cached frame name (resolved on first mount via
 *                    a tiny client-side fetch — the Phase 5 plan
 *                    documents the proxy endpoint as Phase 5.2 polish).
 *   /violation-ref — embeds a Violation card. Block stores violation_id;
 *                    renderer pulls live status via the existing
 *                    GET /v1/projects/:slug/violations/:id endpoint.
 *
 * Each block stores only the entity id; the rendered content fetches
 * fresh on mount so the embed stays consistent with the underlying
 * record. Phase 5.2 polish wires SSE so the cards live-update without
 * a remount.
 */

import { defaultBlockSpecs } from "@blocknote/core";
import { createReactBlockSpec } from "@blocknote/react";
import { useEffect, useState } from "react";
import DecisionCard from "@/components/decisions/DecisionCard";
import { fetchDecision, type Decision } from "@/lib/decisions/client";

// ─── /decision block ────────────────────────────────────────────────────────

export const DecisionRefBlock = createReactBlockSpec(
  {
    type: "decisionRef",
    propSchema: {
      decisionID: { default: "" as string },
    },
    content: "none",
  },
  {
    render: ({ block }) => {
      const id = block.props.decisionID;
      return <DecisionRefRenderer decisionID={id} />;
    },
  },
);

function DecisionRefRenderer({ decisionID }: { decisionID: string }) {
  const [state, setState] = useState<
    { kind: "loading" } | { kind: "ok"; decision: Decision } | { kind: "error"; msg: string }
  >({ kind: "loading" });

  useEffect(() => {
    if (!decisionID) {
      setState({ kind: "error", msg: "Decision id missing — re-pick a decision." });
      return;
    }
    let cancelled = false;
    void fetchDecision(decisionID).then((res) => {
      if (cancelled) return;
      if (!res.ok) {
        setState({ kind: "error", msg: `${res.error} (status ${res.status})` });
        return;
      }
      setState({ kind: "ok", decision: res.data });
    });
    return () => {
      cancelled = true;
    };
  }, [decisionID]);

  if (state.kind === "loading") {
    return (
      <div
        contentEditable={false}
        style={{
          padding: 10,
          border: "1px dashed var(--border)",
          borderRadius: 6,
          fontSize: 11,
          fontFamily: "var(--font-mono)",
          color: "var(--text-3)",
        }}
      >
        Loading decision…
      </div>
    );
  }
  if (state.kind === "error") {
    return (
      <div
        contentEditable={false}
        style={{
          padding: 10,
          border: "1px solid var(--danger)",
          borderRadius: 6,
          fontSize: 11,
          color: "var(--danger)",
        }}
      >
        Couldn't load decision: {state.msg}
      </div>
    );
  }
  return (
    <div contentEditable={false} style={{ margin: "8px 0" }}>
      <DecisionCard decision={state.decision} defaultExpanded={false} />
    </div>
  );
}

// ─── /figma-link block ──────────────────────────────────────────────────────

export const FigmaLinkBlock = createReactBlockSpec(
  {
    type: "figmaLink",
    propSchema: {
      url: { default: "" as string },
      label: { default: "" as string },
    },
    content: "none",
  },
  {
    render: ({ block }) => {
      const { url, label } = block.props;
      return <FigmaLinkRenderer url={url} label={label} />;
    },
  },
);

function FigmaLinkRenderer({ url, label }: { url: string; label: string }) {
  if (!url) {
    return (
      <div
        contentEditable={false}
        style={{
          padding: 10,
          border: "1px dashed var(--border)",
          borderRadius: 6,
          fontSize: 11,
          color: "var(--text-3)",
        }}
      >
        Paste a Figma URL.
      </div>
    );
  }
  // Parse the URL to extract a friendly title from the path. Figma URLs
  // are shaped /file/<key>/<title>?node-id=… → use <title> when present.
  let displayLabel = label;
  if (!displayLabel) {
    try {
      const u = new URL(url);
      const parts = u.pathname.split("/").filter(Boolean);
      displayLabel = parts[parts.length - 1] || u.hostname;
      displayLabel = decodeURIComponent(displayLabel.replace(/-/g, " "));
    } catch {
      displayLabel = url;
    }
  }
  return (
    <a
      href={url}
      target="_blank"
      rel="noopener noreferrer"
      contentEditable={false}
      style={{
        display: "flex",
        alignItems: "center",
        gap: 10,
        padding: 10,
        border: "1px solid var(--border)",
        borderRadius: 6,
        background: "var(--bg-surface)",
        textDecoration: "none",
        color: "var(--text-1)",
        fontSize: 12,
        margin: "8px 0",
      }}
    >
      <span
        aria-hidden
        style={{
          width: 24,
          height: 24,
          borderRadius: 4,
          background: "linear-gradient(135deg, #ff7262, #a259ff)",
          flexShrink: 0,
        }}
      />
      <div style={{ display: "flex", flexDirection: "column", minWidth: 0 }}>
        <span
          style={{
            fontWeight: 600,
            overflow: "hidden",
            textOverflow: "ellipsis",
            whiteSpace: "nowrap",
          }}
        >
          {displayLabel}
        </span>
        <span
          style={{
            fontSize: 10,
            fontFamily: "var(--font-mono)",
            color: "var(--text-3)",
            overflow: "hidden",
            textOverflow: "ellipsis",
            whiteSpace: "nowrap",
          }}
        >
          {url}
        </span>
      </div>
    </a>
  );
}

// ─── /violation-ref block ───────────────────────────────────────────────────

interface ViolationCardData {
  id: string;
  rule_id: string;
  severity: string;
  status: string;
  property: string;
  observed: string;
  suggestion: string;
  project_slug: string;
  flow_name: string;
}

export const ViolationRefBlock = createReactBlockSpec(
  {
    type: "violationRef",
    propSchema: {
      violationID: { default: "" as string },
      slug: { default: "" as string },
    },
    content: "none",
  },
  {
    render: ({ block }) => {
      const { violationID, slug } = block.props;
      return <ViolationRefRenderer violationID={violationID} slug={slug} />;
    },
  },
);

function dsBaseURL(): string {
  return process.env.NEXT_PUBLIC_DS_SERVICE_URL ?? "http://localhost:8080";
}

function ViolationRefRenderer({ violationID, slug }: { violationID: string; slug: string }) {
  const [state, setState] = useState<
    | { kind: "loading" }
    | { kind: "ok"; data: ViolationCardData }
    | { kind: "error"; msg: string }
  >({ kind: "loading" });

  useEffect(() => {
    if (!violationID || !slug) {
      setState({ kind: "error", msg: "Violation reference missing slug or id." });
      return;
    }
    let cancelled = false;
    const url = `${dsBaseURL()}/v1/projects/${encodeURIComponent(slug)}/violations/${encodeURIComponent(violationID)}`;
    const token = (typeof window !== "undefined" && window.localStorage)
      ? JSON.parse(window.localStorage.getItem("indmoney-ds-auth") ?? "{}")?.state?.token
      : "";
    fetch(url, { headers: { Accept: "application/json", Authorization: `Bearer ${token ?? ""}` } })
      .then(async (res) => {
        if (cancelled) return;
        if (!res.ok) {
          setState({ kind: "error", msg: `HTTP ${res.status}` });
          return;
        }
        const data = (await res.json()) as ViolationCardData;
        setState({ kind: "ok", data });
      })
      .catch((err) => {
        if (cancelled) return;
        setState({
          kind: "error",
          msg: err instanceof Error ? err.message : String(err),
        });
      });
    return () => {
      cancelled = true;
    };
  }, [violationID, slug]);

  const tint =
    state.kind === "ok"
      ? severityTint(state.data.severity)
      : "var(--text-3)";

  return (
    <div
      contentEditable={false}
      style={{
        margin: "8px 0",
        padding: 10,
        border: "1px solid var(--border)",
        borderLeft: `3px solid ${tint}`,
        borderRadius: 6,
        background: "var(--bg-surface)",
        fontSize: 12,
      }}
    >
      {state.kind === "loading" && (
        <span style={{ fontFamily: "var(--font-mono)", color: "var(--text-3)" }}>
          loading violation…
        </span>
      )}
      {state.kind === "error" && (
        <span style={{ color: "var(--danger)" }}>Couldn't load violation: {state.msg}</span>
      )}
      {state.kind === "ok" && (
        <div style={{ display: "flex", flexDirection: "column", gap: 4 }}>
          <div
            style={{
              display: "flex",
              gap: 8,
              alignItems: "center",
              flexWrap: "wrap",
            }}
          >
            <span
              style={{
                fontSize: 10,
                fontFamily: "var(--font-mono)",
                textTransform: "uppercase",
                color: tint,
                border: `1px solid ${tint}`,
                padding: "2px 6px",
                borderRadius: 999,
              }}
            >
              {state.data.severity}
            </span>
            <strong>{state.data.rule_id}</strong>
            <span style={{ color: "var(--text-3)", fontFamily: "var(--font-mono)", fontSize: 11 }}>
              · status: {state.data.status}
            </span>
          </div>
          {state.data.suggestion && (
            <span style={{ color: "var(--text-2)" }}>{state.data.suggestion}</span>
          )}
        </div>
      )}
    </div>
  );
}

function severityTint(s: string): string {
  switch (s) {
    case "critical":
      return "#dc2626";
    case "high":
      return "#ea580c";
    case "medium":
      return "#ca8a04";
    case "low":
      return "#2563eb";
    default:
      return "#64748b";
  }
}

// ─── Schema export ──────────────────────────────────────────────────────────

/**
 * drdBlockSpecs is the merged spec map BlockNote consumes via
 * `BlockNoteSchema.create({ blockSpecs: drdBlockSpecs })`. Defaults are
 * preserved so paragraph/heading/list/etc. continue to work; the three
 * Phase 5.1 P2 customs are added under stable type names.
 *
 * createReactBlockSpec returns a creator function — invoke each one
 * here so the resulting object is `Record<string, BlockSpec<...>>`
 * which matches BlockNote's BlockSpecs type.
 */
export const drdBlockSpecs = {
  ...defaultBlockSpecs,
  decisionRef: DecisionRefBlock(),
  figmaLink: FigmaLinkBlock(),
  violationRef: ViolationRefBlock(),
};
