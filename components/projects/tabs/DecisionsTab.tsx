"use client";

/**
 * Decisions tab — placeholder until Phase 5.
 *
 * Decisions are first-class objects (separate from DRD chatter) introduced
 * in the Phase 5 plan. Phase 1 ships the empty pane to lock the four-tab
 * layout in place from day one — no re-flowing the tab strip later.
 */

import EmptyTab from "./EmptyTab";

export default function DecisionsTab() {
  return (
    <EmptyTab
      title="Decisions coming in Phase 5"
      description="First-class decision records anchored to a flow. Today, capture decisions in DRD when U9 ships."
    />
  );
}
