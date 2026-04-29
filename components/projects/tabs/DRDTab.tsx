"use client";

/**
 * DRD tab — placeholder until U9 wires BlockNote.
 *
 * Tabs share the same animation contract: incoming = slides up from below,
 * outgoing = fades + slides up. The shared `tabSwitch` timeline operates on
 * the wrapping pane element (data-anim="tab-content"); each tab's body just
 * needs to render its empty state.
 */

import EmptyTab from "./EmptyTab";

export default function DRDTab() {
  return (
    <EmptyTab
      title="DRD coming in U9"
      description="Decisions, Risks, Discussion — collaborative editor lands when the BlockNote integration ships. Until then, anchor decisions in your existing tools."
    />
  );
}
