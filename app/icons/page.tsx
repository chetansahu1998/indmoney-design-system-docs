import Link from "next/link";
import IconographySection from "@/components/sections/IconographySection";
import FilesShell from "@/components/files/FilesShell";
import type { NavGroup } from "@/components/Sidebar";

// Audit C27: per-route metadata.
export const metadata = { title: "Icons · INDmoney DS" };

export default function IconsPage() {
  // The IconographySection internally groups icons by category and renders
  // its own section heading + meta strip; the sidebar here just gives
  // the page a left rail consistent with /components, /illustrations, /logos.
  const nav: NavGroup[] = [
    {
      label: "Icons",
      defaultOpen: true,
      sub: [{ label: "All icons", href: "#iconography" }],
    },
  ];
  return (
    <FilesShell nav={nav} title="Icons" sectionIds={["iconography"]}>
      <IconographySection />
      {/* Audit C26: cross-link from asset surfaces back into Foundations
       *  so designers looking for tokens after browsing icons aren't
       *  stranded. Mirrors the equivalent footers on /illustrations and
       *  /logos so the three asset routes feel like a set. */}
      <p
        style={{
          marginTop: 32,
          paddingTop: 20,
          borderTop: "1px solid var(--border)",
          fontSize: 12,
          color: "var(--text-3)",
          fontFamily: "var(--font-mono)",
        }}
      >
        Looking for color or spacing tokens?{" "}
        <Link href="/" style={{ color: "var(--accent)", textDecoration: "none" }}>
          See Foundations
        </Link>
        .
      </p>
    </FilesShell>
  );
}
