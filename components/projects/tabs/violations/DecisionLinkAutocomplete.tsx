"use client";

/**
 * DecisionLinkAutocomplete — Phase 5 U11. Lists decisions on the active
 * flow as a dropdown so the user can link a violation Acknowledge /
 * Dismiss to a Decision in one keystroke. Optional: empty selection
 * means no link.
 *
 * The data source is /v1/projects/:slug/flows/:flow_id/decisions; we
 * fetch on first focus and cache for the lifetime of the form. Phase 7
 * polish replaces with a search-as-you-type endpoint when decision
 * counts cross ~50/flow.
 */

import { useEffect, useState } from "react";
import { listDecisionsForFlow, type Decision } from "@/lib/decisions/client";

interface Props {
  slug: string;
  flowID: string | null;
  value: string;
  onChange: (decisionID: string) => void;
}

export default function DecisionLinkAutocomplete({
  slug,
  flowID,
  value,
  onChange,
}: Props) {
  const [decisions, setDecisions] = useState<Decision[] | null>(null);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    if (!flowID || decisions !== null) return;
    let cancelled = false;
    setLoading(true);
    void listDecisionsForFlow(slug, flowID).then((r) => {
      if (cancelled) return;
      setLoading(false);
      setDecisions(r.ok ? r.data.decisions ?? [] : []);
    });
    return () => {
      cancelled = true;
    };
  }, [slug, flowID, decisions]);

  if (!flowID) return null;

  return (
    <label
      style={{
        display: "flex",
        flexDirection: "column",
        gap: 4,
        fontSize: 11,
        fontFamily: "var(--font-mono)",
        color: "var(--text-3)",
      }}
    >
      Link to decision (optional)
      <select
        value={value}
        onChange={(e) => onChange(e.target.value)}
        disabled={loading}
        style={{
          padding: "5px 8px",
          fontSize: 11,
          fontFamily: "var(--font-mono)",
          border: "1px solid var(--border)",
          borderRadius: 4,
          background: "var(--bg-base, #fff)",
          color: "var(--text-1)",
        }}
      >
        <option value="">— no link —</option>
        {(decisions ?? []).map((d) => (
          <option key={d.id} value={d.id}>
            [{d.status[0].toUpperCase()}] {d.title.length > 60 ? d.title.slice(0, 60) + "…" : d.title}
          </option>
        ))}
      </select>
    </label>
  );
}
