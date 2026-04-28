import { notFound } from "next/navigation";
import FilesShell from "@/components/files/FilesShell";
import ComponentDetail from "@/components/ComponentDetail";
import {
  componentBySlug,
  componentsWithRichData,
} from "@/lib/icons/manifest";
import type { NavGroup } from "@/components/Sidebar";

/**
 * /components/[slug] — full component spec page. Replaces the gallery's
 * inline expansion as the primary path for "give me everything about
 * this component". The inline inspector (gallery view) stays as a quick
 * browse aid; the detail page is the place a designer screenshots into
 * a Slack handoff.
 *
 * SSG'd via generateStaticParams over every component that has rich
 * extraction data — we don't pre-render the empty-data tail.
 */
export function generateStaticParams() {
  return componentsWithRichData().map((c) => ({ slug: c.slug }));
}

export default async function ComponentDetailPage({
  params,
}: {
  params: Promise<{ slug: string }>;
}) {
  const { slug } = await params;
  const entry = componentBySlug(slug);
  if (!entry) {
    notFound();
  }

  // Sidebar mirrors the on-page section anchors so designers can deep-link
  // to "Variant axes" or "Layout" without scrolling.
  const sectionIds = [
    "overview",
    "variants",
    "props",
    "layout",
    "appearance",
    "structure",
    "code",
  ];
  const nav: NavGroup[] = [
    {
      label: entry!.name,
      defaultOpen: true,
      sub: [
        { label: "Overview", href: "#overview" },
        { label: "Variants", href: "#variants" },
        { label: "Props", href: "#props" },
        { label: "Layout", href: "#layout" },
        { label: "Appearance", href: "#appearance" },
        { label: "Structure", href: "#structure" },
        { label: "Code", href: "#code" },
      ],
    },
  ];

  return (
    <FilesShell nav={nav} title="Component" sectionIds={sectionIds}>
      <ComponentDetail entry={entry!} />
    </FilesShell>
  );
}
