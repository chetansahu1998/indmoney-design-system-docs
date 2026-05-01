"use client";

/**
 * Inline Acknowledge / Dismiss controls for a single violation row in the
 * ViolationsTab. Phase 4 U6 — replaces Phase 1 U10's disabled placeholders.
 *
 * Layout: two buttons side-by-side until the user picks one; selecting an
 * action expands an inline reason input below. Submit triggers the PATCH
 * endpoint and tells the parent to fade-out the row on success.
 *
 * The bulk inbox flow uses BulkActionBar for the same job; this component
 * stays inline because the per-row UX in the project shell shouldn't pop
 * a sticky bar — it would conflict with the project-view scroll surface.
 */

import { useState } from "react";
import {
  patchViolationLifecycle,
  type LifecycleAction,
} from "@/lib/inbox/client";
import DecisionLinkAutocomplete from "./DecisionLinkAutocomplete";

interface Props {
  slug: string;
  violationID: string;
  /** Phase 5 U11 — when present, the form surfaces a Decision picker. */
  flowID?: string | null;
  /** Called after a successful PATCH so the parent can collapse the row. */
  onResolved: (action: LifecycleAction) => void;
}

type FormState =
  | { kind: "idle" }
  | { kind: "open"; action: LifecycleAction; reason: string; submitting: boolean }
  | { kind: "error"; action: LifecycleAction; reason: string; error: string };

export default function LifecycleButtons({
  slug,
  violationID,
  flowID,
  onResolved,
}: Props) {
  const [state, setState] = useState<FormState>({ kind: "idle" });
  const [linkDecisionID, setLinkDecisionID] = useState("");

  const open = (action: LifecycleAction) =>
    setState({ kind: "open", action, reason: "", submitting: false });

  const submit = async () => {
    if (state.kind !== "open" && state.kind !== "error") return;
    const reason = state.reason.trim();
    if (!reason) return;
    setState({ kind: "open", action: state.action, reason, submitting: true });
    const res = await patchViolationLifecycle(
      slug,
      violationID,
      state.action,
      reason,
      linkDecisionID || undefined,
    );
    if (!res.ok) {
      setState({
        kind: "error",
        action: state.action,
        reason,
        error: `${res.error} (status ${res.status})`,
      });
      return;
    }
    onResolved(state.action);
  };

  if (state.kind === "idle") {
    return (
      <div style={{ display: "flex", gap: 6, alignSelf: "center" }}>
        <button
          type="button"
          onClick={() => open("acknowledge")}
          style={btnStyle(false)}
          aria-label="Acknowledge violation with reason"
        >
          Acknowledge
        </button>
        <button
          type="button"
          onClick={() => open("dismiss")}
          style={btnStyle(false)}
          aria-label="Dismiss violation with rationale"
        >
          Dismiss
        </button>
      </div>
    );
  }

  const reasonValue = state.kind === "open" ? state.reason : state.reason;
  const submitting = state.kind === "open" && state.submitting;
  const errorText = state.kind === "error" ? state.error : null;

  return (
    <div
      style={{
        display: "flex",
        flexDirection: "column",
        gap: 6,
        alignItems: "stretch",
        minWidth: 220,
      }}
    >
      <textarea
        value={reasonValue}
        onChange={(e) =>
          setState({
            kind: "open",
            action: state.action,
            reason: e.target.value,
            submitting: false,
          })
        }
        placeholder={
          state.action === "dismiss"
            ? "Rationale (required, ≤256 chars)"
            : "Reason (required, ≤256 chars)"
        }
        maxLength={256}
        rows={2}
        style={{
          padding: "6px 8px",
          fontSize: 11,
          fontFamily: "var(--font-mono)",
          border: "1px solid var(--border)",
          borderRadius: 6,
          background: "var(--bg-base, #fff)",
          color: "var(--text-1)",
          resize: "vertical",
        }}
      />
      {flowID && (
        <DecisionLinkAutocomplete
          slug={slug}
          flowID={flowID}
          value={linkDecisionID}
          onChange={setLinkDecisionID}
        />
      )}
      <div style={{ display: "flex", gap: 6, justifyContent: "flex-end" }}>
        <button
          type="button"
          onClick={() => setState({ kind: "idle" })}
          disabled={submitting}
          style={btnStyle(false)}
        >
          Cancel
        </button>
        <button
          type="button"
          onClick={() => void submit()}
          disabled={submitting || !reasonValue.trim()}
          style={btnStyle(true)}
        >
          {submitting ? "Submitting…" : `Confirm ${state.action}`}
        </button>
      </div>
      {errorText && (
        <span style={{ fontSize: 10, color: "var(--danger)" }}>{errorText}</span>
      )}
    </div>
  );
}

function btnStyle(primary: boolean): React.CSSProperties {
  return {
    padding: "6px 10px",
    fontSize: 11,
    fontFamily: "var(--font-mono)",
    background: primary ? "var(--accent)" : "transparent",
    color: primary ? "var(--bg-base, #fff)" : "var(--text-1)",
    border: `1px solid ${primary ? "var(--accent)" : "var(--border)"}`,
    borderRadius: 6,
    cursor: "pointer",
  };
}
