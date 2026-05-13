"use client";

/**
 * /atlas/admin/figma-inventory — FIGMA DB Phase 2A.
 *
 * Stacked single-column layout matching the rest of /atlas/admin/* (rules,
 * organisms, personas, figma-blocklist). Three sections:
 *
 *   1. TeamBar     — horizontal chips of seeded teams + "Add team"
 *   2. FilesTable  — primary view: sortable, filterable by project /
 *                    recency / status; rows expand to show pages +
 *                    sections inline. Default sort: most recent first.
 *   3. RunsStrip   — compact recent-runs strip with "Sync now"
 *
 * Data flow: TeamBar owns the selected team_id and pushes it down to
 * FilesTable. Each panel fetches its own slice of the API independently;
 * polling intervals live inside the component that needs the refresh.
 */

import { useState } from "react";

import { AdminShell } from "@/app/atlas/admin/_lib/AdminShell";

import { FilesTable } from "./_components/FilesTable";
import { RunsStrip } from "./_components/RunsStrip";
import { TeamBar } from "./_components/TeamBar";

export default function FigmaInventoryAdminPage() {
  const [selectedTeamID, setSelectedTeamID] = useState<string>("");

  return (
    <AdminShell
      title="Figma inventory"
      description="Team → project → file → page → section mirrored from Figma. Sortable, filterable, drills into each file inline."
    >
      <TeamBar selectedTeamID={selectedTeamID} onSelectTeam={setSelectedTeamID} />
      <FilesTable teamID={selectedTeamID} />
      <RunsStrip />
    </AdminShell>
  );
}
