import IconographySection from "@/components/sections/IconographySection";
import FilesShell from "@/components/files/FilesShell";
import type { NavGroup } from "@/components/Sidebar";

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
    </FilesShell>
  );
}
