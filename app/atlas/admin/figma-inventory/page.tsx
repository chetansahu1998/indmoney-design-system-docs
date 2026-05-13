"use client";

/**
 * /atlas/admin/figma-inventory — FIGMA DB Phase 2 admin surface.
 *
 * Plan: docs/plans/2026-05-13-002-feat-figma-db-phase-2-plan.md (Phase 2A
 * U1-U4). Three panels:
 *
 *   1. TeamList (U2)      — left column. Seeded teams + add/disable.
 *   2. InventoryTree (U3) — main column. team → project → file → page →
 *                           section, expandable + searchable.
 *   3. RunsPanel (U4)     — right column. Recent poll cycles + sync now.
 *
 * U1 (this scaffolding) wires the AdminShell + three placeholder panels
 * that the subsequent units populate. The selected team_id flows from
 * TeamList → InventoryTree via local state.
 */

import { useState } from "react";

import { AdminShell } from "@/app/atlas/admin/_lib/AdminShell";

import { InventoryTree } from "./_components/InventoryTree";
import { RunsPanel } from "./_components/RunsPanel";
import { TeamList } from "./_components/TeamList";

export default function FigmaInventoryAdminPage() {
  // Currently-selected team_id drives the InventoryTree fetch. Empty
  // string = no team selected → tree shows an empty-state hint.
  const [selectedTeamID, setSelectedTeamID] = useState<string>("");

  return (
    <AdminShell
      title="Figma inventory"
      description="Team → project → file → page → section mirrored from Figma every 5 minutes."
    >
      <div className="inv-layout">
        <aside className="inv-side-left">
          <TeamList
            selectedTeamID={selectedTeamID}
            onSelectTeam={setSelectedTeamID}
          />
        </aside>
        <section className="inv-main">
          <InventoryTree teamID={selectedTeamID} />
        </section>
        <aside className="inv-side-right">
          <RunsPanel />
        </aside>
      </div>
      <style jsx>{`
        .inv-layout {
          display: grid;
          grid-template-columns: 320px 1fr 360px;
          gap: 16px;
          align-items: start;
        }
        .inv-side-left,
        .inv-side-right,
        .inv-main {
          background: var(--surface-1, rgba(255, 255, 255, 0.02));
          border: 1px solid var(--border, rgba(255, 255, 255, 0.08));
          border-radius: 12px;
          padding: 16px;
          min-height: 480px;
        }
        @media (max-width: 1280px) {
          .inv-layout {
            grid-template-columns: 280px 1fr;
          }
          .inv-side-right {
            grid-column: 1 / -1;
            min-height: 240px;
          }
        }
        @media (max-width: 880px) {
          .inv-layout {
            grid-template-columns: 1fr;
          }
        }
      `}</style>
    </AdminShell>
  );
}
