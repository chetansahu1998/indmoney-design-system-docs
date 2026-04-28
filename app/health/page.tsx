import FilesShell from "@/components/files/FilesShell";
import HealthDashboard from "@/components/HealthDashboard";
import type { NavGroup } from "@/components/Sidebar";

/**
 * /health — design-system vitals dashboard.
 *
 * One scroll-down view summarising the system's current state:
 * extraction freshness, component coverage, drift hotspots, audit
 * status. The DS lead's daily-look surface.
 */
export default function HealthPage() {
  const sectionIds = [
    "overview",
    "tokens",
    "components",
    "drift",
    "audits",
    "extraction",
  ];
  const nav: NavGroup[] = [
    {
      label: "Health",
      defaultOpen: true,
      sub: [
        { label: "Overview", href: "#overview" },
        { label: "Tokens", href: "#tokens" },
        { label: "Components", href: "#components" },
        { label: "Drift", href: "#drift" },
        { label: "Audits", href: "#audits" },
        { label: "Extraction", href: "#extraction" },
      ],
    },
  ];

  return (
    <FilesShell nav={nav} title="Health" sectionIds={sectionIds}>
      <HealthDashboard />
    </FilesShell>
  );
}
