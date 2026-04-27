import { notFound } from "next/navigation";
import FileDetail from "@/components/files/FileDetail";
import { loadFileAudit, listAuditedSlugs } from "@/lib/audit/files";

/**
 * /files/<slug> — per-file detail page.
 *
 * generateStaticParams produces one route per audited slug at build time.
 * If a designer runs the plugin against a new file, its <slug>.json lands
 * in lib/audit/, the next build picks it up automatically — no code changes.
 */
export async function generateStaticParams() {
  const slugs = await listAuditedSlugs();
  return slugs.map((slug) => ({ slug }));
}

export default async function FilePage({
  params,
}: {
  params: Promise<{ slug: string }>;
}) {
  const { slug } = await params;
  const result = await loadFileAudit(slug);
  if (!result) notFound();
  return <FileDetail result={result} />;
}
