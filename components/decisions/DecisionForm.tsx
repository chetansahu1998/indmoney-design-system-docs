"use client";

/**
 * DecisionForm — shared by the Decisions tab "+ New decision" button and
 * (Phase 5 U5) the DRD `/decision` slash command. Surfaces title + body
 * + optional supersedes_id + optional links. Submits via createDecision.
 *
 * Phase 5 ships a plain textarea body editor. The plan documents the
 * upgrade to a stripped BlockNote-mini at the U5 milestone — the wire
 * shape (body_json string) supports both.
 */

import { useState } from "react";
import {
  createDecision,
  type CreateDecisionInput,
  type Decision,
  type DecisionLinkType,
} from "@/lib/decisions/client";

interface Props {
  slug: string;
  flowID: string;
  /** Pre-fill supersedes_id when launched from "Supersede this decision". */
  supersedesID?: string;
  /** Optional initial links (e.g., violation-acknowledge flow). */
  initialLinks?: { link_type: DecisionLinkType; target_id: string }[];
  onSubmitted: (d: Decision) => void;
  onCancel: () => void;
}

export default function DecisionForm({
  slug,
  flowID,
  supersedesID,
  initialLinks,
  onSubmitted,
  onCancel,
}: Props) {
  const [title, setTitle] = useState("");
  const [body, setBody] = useState("");
  const [status, setStatus] = useState<"proposed" | "accepted">("accepted");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const submit = async () => {
    const trimmed = title.trim();
    if (!trimmed) {
      setError("Title required");
      return;
    }
    setSubmitting(true);
    setError(null);
    const input: CreateDecisionInput = {
      title: trimmed,
      status,
      supersedes_id: supersedesID,
    };
    if (body.trim()) {
      input.body_json = JSON.stringify({
        blocks: [{ type: "paragraph", content: body.trim() }],
      });
    }
    if (initialLinks && initialLinks.length > 0) {
      input.links = initialLinks;
    }
    const res = await createDecision(slug, flowID, input);
    setSubmitting(false);
    if (!res.ok) {
      setError(`${res.error} (status ${res.status})`);
      return;
    }
    onSubmitted(res.data);
  };

  return (
    <form
      data-testid="decision-form"
      onSubmit={(e) => {
        e.preventDefault();
        void submit();
      }}
      style={{
        border: "1px solid var(--border)",
        borderRadius: 8,
        background: "var(--bg-surface)",
        padding: 14,
        display: "flex",
        flexDirection: "column",
        gap: 10,
      }}
    >
      <div style={{ display: "flex", flexDirection: "column", gap: 4 }}>
        <label
          style={{
            fontSize: 11,
            fontFamily: "var(--font-mono)",
            color: "var(--text-3)",
          }}
        >
          Title
        </label>
        <input
          type="text"
          value={title}
          onChange={(e) => setTitle(e.target.value)}
          placeholder="Approved padding-32 over grid-24"
          maxLength={200}
          required
          style={{
            padding: "8px 10px",
            fontSize: 13,
            border: "1px solid var(--border)",
            borderRadius: 6,
            background: "var(--bg-base, #fff)",
            color: "var(--text-1)",
          }}
        />
      </div>

      <div style={{ display: "flex", flexDirection: "column", gap: 4 }}>
        <label
          style={{
            fontSize: 11,
            fontFamily: "var(--font-mono)",
            color: "var(--text-3)",
          }}
        >
          Body (optional)
        </label>
        <textarea
          value={body}
          onChange={(e) => setBody(e.target.value)}
          placeholder="Context, trade-offs, and follow-ups…"
          rows={4}
          style={{
            padding: "8px 10px",
            fontSize: 12,
            fontFamily: "var(--font-mono)",
            border: "1px solid var(--border)",
            borderRadius: 6,
            background: "var(--bg-base, #fff)",
            color: "var(--text-1)",
            resize: "vertical",
          }}
        />
      </div>

      <div style={{ display: "flex", alignItems: "center", gap: 12 }}>
        <label
          style={{
            fontSize: 11,
            fontFamily: "var(--font-mono)",
            color: "var(--text-3)",
            display: "flex",
            alignItems: "center",
            gap: 6,
          }}
        >
          Status:
          <select
            value={status}
            onChange={(e) => setStatus(e.target.value as "proposed" | "accepted")}
            style={{
              padding: "4px 8px",
              fontSize: 11,
              fontFamily: "var(--font-mono)",
              border: "1px solid var(--border)",
              borderRadius: 4,
              background: "var(--bg-base, #fff)",
              color: "var(--text-1)",
            }}
          >
            <option value="accepted">Accepted</option>
            <option value="proposed">Proposed</option>
          </select>
        </label>

        {supersedesID && (
          <span
            style={{
              fontSize: 11,
              fontFamily: "var(--font-mono)",
              color: "var(--text-3)",
            }}
          >
            Supersedes {supersedesID.slice(0, 8)}…
          </span>
        )}

        <div style={{ marginLeft: "auto", display: "flex", gap: 6 }}>
          <button
            type="button"
            onClick={onCancel}
            disabled={submitting}
            style={btnStyle(false)}
          >
            Cancel
          </button>
          <button type="submit" disabled={submitting || !title.trim()} style={btnStyle(true)}>
            {submitting ? "Saving…" : "Save decision"}
          </button>
        </div>
      </div>

      {error && (
        <span style={{ fontSize: 11, color: "var(--danger)" }}>{error}</span>
      )}
    </form>
  );
}

function btnStyle(primary: boolean): React.CSSProperties {
  return {
    padding: "6px 12px",
    fontSize: 12,
    fontFamily: "var(--font-mono)",
    background: primary ? "var(--accent)" : "transparent",
    color: primary ? "var(--bg-base, #fff)" : "var(--text-1)",
    border: `1px solid ${primary ? "var(--accent)" : "var(--border)"}`,
    borderRadius: 6,
    cursor: "pointer",
  };
}
