/**
 * /projects — redirect to /atlas.
 *
 * The flat project grid is replaced by the spatial brain index at /atlas.
 * Legacy implementation preserved at page.tsx.legacy.bak; Phase 8 deletes
 * it once we've shipped a release with the new shell.
 *
 * We use a permanent redirect (308) so any cached link / bookmark updates.
 */

import { permanentRedirect } from "next/navigation";

export default function ProjectsPage(): never {
  permanentRedirect("/atlas");
}
