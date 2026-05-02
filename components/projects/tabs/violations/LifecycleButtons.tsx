"use client";

/**
 * Inline Acknowledge / Dismiss / Reactivate controls for a single violation
 * row in the ViolationsTab. Phase 4 U6 introduced acknowledge/dismiss;
 * Phase 6 U6 adds Reactivate (admin-only, dismissed → active) per R8.
 *
 * Layout: action buttons side-by-side until the user picks one; selecting
 * an action expands an inline reason input below. Submit triggers the
 * PATCH endpoint and tells the parent to fade-out the row on success.
 *
 * Visibility:
 *   - Acknowledge / Dismiss render when status === "active". (Status
 *     transitions for already-acknowledged rows happen elsewhere — the
 *     fix-applied path or the system-only Mark Fixed action.)
 *   - Reactivate renders ONLY when status === "dismissed" AND the caller
 *     has an admin role (super_admin | tenant_admin). The backend
 *     enforces the same gate (`isAdminRole` in
 *     services/ds-service/internal/projects/lifecycle.go); the client
 *     guard is defense-in-depth so the button never appears for users
 *     who would 403 on submit.
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
import { useAuth } from "@/lib/auth-client";
import type { ViolationStatus } from "@/lib/projects/types";
import DecisionLinkAutocomplete from "./DecisionLinkAutocomplete";

interface Props {
  slug: string;
  violationID: string;
  /** Current lifecycle status — drives which actions are shown. */
  status: ViolationStatus;
  /** Phase 5 U11 — when present, the form surfaces a Decision picker. */
  flowID?: string | null;
  /** Called after a successful PATCH so the parent can collapse the row. */
  onResolved: (action: LifecycleAction) => void;
}

const ADMIN_ROLES = new Set(["super_admin", "tenant_admin"]);

type FormState =
  | { kind: "idle" }
  | { kind: "open"; action: LifecycleAction; reason: string; submitting: boolean }
  | { kind: "error"; action: LifecycleAction; reason: string; error: string };

export default function LifecycleButtons({
  slug,
  violationID,
  status,
  flowID,
  onResolved,
}: Props) {
  const [state, setState] = useState<FormState>({ kind: "idle" });
  const [linkDecisionID, setLinkDecisionID] = useState("");
  const role = useAuth((s) => s.role);
  const isAdmin = role !== null && ADMIN_ROLES.has(role);

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
    // Reactivate is the only action available for dismissed rows, and
    // only to admins. Non-admins viewing a dismissed row see no actions
    // (the row exists in the dataset but is informational for them).
    if (status === "dismissed") {
      if (!isAdmin) return null;
      return (
        <div style={{ display: "flex", gap: 6, alignSelf: "center" }}>
          <button
            type="button"
            onClick={() => open("reactivate")}
            style={btnStyle(false)}
            aria-label="Reactivate dismissed violation with reason"
            data-action="reactivate"
          >
            Reactivate
          </button>
        </div>
      );
    }
    // Active rows get the standard acknowledge/dismiss pair. Acknowledged
    // rows have no inline actions — they wait for the system-only
    // Mark Fixed transition (driven by the auto-fix or re-audit paths).
    if (status !== "active") return null;
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
            : state.action === "reactivate"
              ? "Override reason (required, ≤256 chars)"
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
