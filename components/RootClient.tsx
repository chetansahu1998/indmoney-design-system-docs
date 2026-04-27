"use client";

import { useEffect } from "react";
import { useScrollMemory } from "@/lib/use-scroll-memory";
import { applyDensityFromStore } from "@/lib/ui-store";

/**
 * Client-only init shell. Mounted once in app/layout.tsx body so density
 * + scroll memory + any future global side effects run regardless of which
 * route is rendered.
 *
 * Keeping this in a dedicated component (rather than DocsShell or
 * FilesShell) means the behavior is uniform across every route — there's
 * no risk of one shell forgetting to apply density on first paint.
 */
export default function RootClient() {
  useScrollMemory();
  useEffect(() => {
    applyDensityFromStore();
  }, []);
  return null;
}
