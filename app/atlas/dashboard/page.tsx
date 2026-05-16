"use client";

/**
 * /atlas/dashboard — DS-lead summary tab (formerly /atlas/admin).
 *
 * Renders the existing DashboardShell content (severity charts, top
 * violators, recent decisions) inside the unified Shell chrome so the
 * tab nav stays consistent with the rest of /atlas/*. Permanent redirect
 * from /atlas/admin lives in next.config.ts.
 *
 * Auth gate lives in Shell. DashboardShell itself reads /v1/atlas/summary
 * which still returns 403 for non-admins; the EmptyState surfaces the
 * 403 as a friendly error message.
 */

import { Shell } from "@/app/atlas/_lib/Shell";
import DashboardShell from "@/components/dashboard/DashboardShell";

export default function AtlasDashboardPage() {
  return (
    <Shell
      title="Dashboard"
      description="Severity trend, top violators, and recent decisions across every imported project."
    >
      <DashboardShell />
    </Shell>
  );
}
