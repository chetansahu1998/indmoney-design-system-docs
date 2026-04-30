"use client";

/**
 * Bulk-action bar that anchors to the bottom of the inbox when one or more
 * rows are selected. The reason-template dropdown is shared with the
 * per-row Acknowledge / Dismiss buttons; both flows funnel through the
 * same reason-input form (see InboxShell `pendingAction`).
 */

import { useEffect, useRef, useState } from "react";
import { templatesForAction, type ReasonTemplate } from "./ReasonTemplates";

interface Props {
  selectedCount: number;
  pending: boolean;
  onSubmit: (action: "acknowledge" | "dismiss", reason: string) => void;
  onClear: () => void;
}

export default function BulkActionBar({
  selectedCount,
  pending,
  onSubmit,
  onClear,
}: Props) {
  const [open, setOpen] = useState<"acknowledge" | "dismiss" | null>(null);
  const [templateID, setTemplateID] = useState<string>("");
  const [reason, setReason] = useState<string>("");
  const reasonRef = useRef<HTMLTextAreaElement>(null);

  // Reset the form whenever the selection clears or the user closes the
  // dropdown — prevents stale reason text from leaking into the next bulk.
  useEffect(() => {
    if (selectedCount === 0) {
      setOpen(null);
      setTemplateID("");
      setReason("");
    }
  }, [selectedCount]);

  if (selectedCount === 0) return null;

  const templates = open ? templatesForAction(open) : [];

  const handlePickTemplate = (id: string) => {
    setTemplateID(id);
    const t = templates.find((tpl: ReasonTemplate) => tpl.id === id);
    if (!t) return;
    setReason(t.reason);
    if (t.id === "custom") {
      // Defer focus to next paint so the textarea is in the DOM.
      requestAnimationFrame(() => reasonRef.current?.focus());
    }
  };

  const handleSubmit = () => {
    if (!open || !reason.trim()) return;
    onSubmit(open, reason.trim());
  };

  return (
    <div
      role="region"
      aria-label="Bulk action bar"
      style={{
        position: "sticky",
        bottom: 16,
        marginTop: 16,
        background: "var(--bg-surface)",
        border: "1px solid var(--border)",
        borderRadius: 10,
        boxShadow: "0 6px 24px rgba(0,0,0,0.18)",
        padding: 12,
        display: "flex",
        flexDirection: "column",
        gap: 10,
        zIndex: 10,
      }}
    >
      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: 12,
          fontFamily: "var(--font-mono)",
          fontSize: 12,
        }}
      >
        <strong style={{ color: "var(--text-1)" }}>
          {selectedCount} selected
        </strong>
        <button
          type="button"
          disabled={pending}
          onClick={() => {
            setOpen("acknowledge");
            setTemplateID("");
            setReason("");
          }}
          style={primaryBtn(open === "acknowledge")}
        >
          Acknowledge {selectedCount}
        </button>
        <button
          type="button"
          disabled={pending}
          onClick={() => {
            setOpen("dismiss");
            setTemplateID("");
            setReason("");
          }}
          style={secondaryBtn(open === "dismiss")}
        >
          Dismiss {selectedCount}
        </button>
        <button
          type="button"
          onClick={onClear}
          style={{
            marginLeft: "auto",
            background: "transparent",
            border: "none",
            color: "var(--text-3)",
            fontFamily: "var(--font-mono)",
            fontSize: 11,
            cursor: "pointer",
            textDecoration: "underline",
          }}
        >
          clear selection
        </button>
      </div>

      {open && (
        <div
          style={{
            display: "flex",
            flexDirection: "column",
            gap: 8,
            paddingTop: 8,
            borderTop: "1px dashed var(--border)",
          }}
        >
          <label
            style={{
              fontSize: 11,
              fontFamily: "var(--font-mono)",
              color: "var(--text-3)",
            }}
          >
            Reason (
            <span style={{ color: open === "dismiss" ? "#dc2626" : "var(--text-3)" }}>
              required
            </span>
            )
          </label>
          <select
            value={templateID}
            onChange={(e) => handlePickTemplate(e.target.value)}
            style={{
              padding: "6px 8px",
              fontSize: 12,
              fontFamily: "var(--font-mono)",
              border: "1px solid var(--border)",
              borderRadius: 6,
              background: "var(--bg-base, #fff)",
              color: "var(--text-1)",
            }}
          >
            <option value="">Pick a template…</option>
            {templates.map((t) => (
              <option key={t.id} value={t.id}>
                {t.label}
              </option>
            ))}
          </select>
          <textarea
            ref={reasonRef}
            value={reason}
            onChange={(e) => setReason(e.target.value)}
            placeholder="Reason (≤256 characters)"
            maxLength={256}
            rows={2}
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
          <div style={{ display: "flex", gap: 8, justifyContent: "flex-end" }}>
            <button
              type="button"
              onClick={() => setOpen(null)}
              style={secondaryBtn(false)}
            >
              Cancel
            </button>
            <button
              type="button"
              disabled={pending || !reason.trim()}
              onClick={handleSubmit}
              style={primaryBtn(true)}
            >
              {pending ? "Submitting…" : `Confirm ${open}`}
            </button>
          </div>
        </div>
      )}
    </div>
  );
}

function primaryBtn(active: boolean): React.CSSProperties {
  return {
    padding: "6px 12px",
    fontSize: 12,
    fontFamily: "var(--font-mono)",
    background: active ? "var(--accent)" : "var(--bg-base, #fff)",
    color: active ? "#fff" : "var(--text-1)",
    border: `1px solid ${active ? "var(--accent)" : "var(--border)"}`,
    borderRadius: 6,
    cursor: "pointer",
    transition: "background 150ms ease",
  };
}

function secondaryBtn(active: boolean): React.CSSProperties {
  return {
    padding: "6px 12px",
    fontSize: 12,
    fontFamily: "var(--font-mono)",
    background: active ? "rgba(220,38,38,0.08)" : "transparent",
    color: active ? "#dc2626" : "var(--text-2)",
    border: `1px solid ${active ? "#dc2626" : "var(--border)"}`,
    borderRadius: 6,
    cursor: "pointer",
    transition: "background 150ms ease",
  };
}
