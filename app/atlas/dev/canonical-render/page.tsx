/**
 * /atlas/dev/canonical-render — fidelity-audit harness.
 *
 * Server-component shim that loads a canonical_tree JSON off disk and
 * hands it to a CLIENT-side renderer (`CanonicalRenderClient`). The
 * client uses the real production hooks `useImageRefs` +
 * `useIconClusterURLs` so the audit screenshot reflects production
 * fidelity (raster fills + icon clusters hydrated against ds-service),
 * not a stripped-down harness with empty maps.
 *
 * Inputs (mutually exclusive, `?file=` takes precedence):
 *   - `?file=<absolute-path>` — read canonical_tree JSON straight from
 *     disk via Node `fs`. On-disk shape accepted: bare CanonicalNode,
 *     `{ canonical_tree: ... }` row wrapper, or Figma `{ document, ... }`
 *     envelope.
 *   - `?screenID=<uuid>&slug=<slug>` — fetch via ds-service's existing
 *     `/v1/projects/:slug/screens/:id/canonical-tree` endpoint.
 *
 * Optional hydration params (apply when `?file=` is used — the screenID
 * mode infers `slug` automatically):
 *   - `?slug=<slug>` — the ds-service project slug to use for image-refs
 *     + cluster URLs. Required for any subject that lives in an already-
 *     imported project.
 *   - `?leafID=<slug-or-uuid>` — the leaf identifier; defaults to `slug`
 *     (post brain-products the project slug doubles as the leafID for
 *     image-refs).
 *
 * Auth: PUBLIC dev page. Mount path lives under `/atlas/dev/*` —
 * gate behind NODE_ENV !== "production" before any merge.
 */

import { promises as fs } from "node:fs";
import path from "node:path";

import type {
  CanonicalNode,
} from "../../_lib/leafcanvas-v2/types";
import { CanonicalRenderClient } from "./CanonicalRenderClient";

export const dynamic = "force-dynamic";
export const revalidate = 0;

type SearchParams = { [k: string]: string | string[] | undefined };

interface PageProps {
  searchParams?: Promise<SearchParams> | SearchParams;
}

export default async function Page(props: PageProps) {
  const sp: SearchParams = (await props.searchParams) ?? {};

  const fileParam = pickStr(sp.file);
  const screenIDParam = pickStr(sp.screenID);
  const slugParam = pickStr(sp.slug);
  const leafIDParam = pickStr(sp.leafID);

  let tree: CanonicalNode | null = null;
  let loadError: string | null = null;
  let source: string = "";
  let slug: string | null = null;
  let leafID: string | null = null;

  if (fileParam) {
    source = `file:${fileParam}`;
    try {
      tree = await loadFromFile(fileParam);
    } catch (err) {
      loadError = err instanceof Error ? err.message : String(err);
    }
    slug = slugParam;
    leafID = leafIDParam ?? slugParam;
  } else if (screenIDParam) {
    source = `screenID:${screenIDParam}${slugParam ? `@${slugParam}` : ""}`;
    if (!slugParam) {
      loadError =
        "`screenID` requires `slug` — ds-service routes canonical_tree per project slug.";
    } else {
      try {
        tree = await loadFromDS(slugParam, screenIDParam);
        slug = slugParam;
        leafID = leafIDParam ?? slugParam;
      } catch (err) {
        loadError = err instanceof Error ? err.message : String(err);
      }
    }
  } else {
    loadError = "Provide ?file=<path> or ?screenID=<uuid>&slug=<slug>.";
  }

  if (loadError || !tree) {
    return (
      <div
        data-render-root="true"
        data-render-error="true"
        data-render-ready="true"
        data-render-source={source}
        style={{
          position: "relative",
          padding: 16,
          background: "#fff",
          color: "#900",
          fontFamily: "ui-monospace, SFMono-Regular, Menlo, monospace",
          fontSize: 12,
          whiteSpace: "pre-wrap",
        }}
      >
        {`canonical-render: ${loadError ?? "no tree"}\nsource: ${source}`}
      </div>
    );
  }

  return (
    <CanonicalRenderClient
      tree={tree}
      slug={slug}
      leafID={leafID}
      source={source}
    />
  );
}

// ─── Loaders ─────────────────────────────────────────────────────────────────

async function loadFromFile(filePath: string): Promise<CanonicalNode | null> {
  if (!path.isAbsolute(filePath)) {
    throw new Error(`file path must be absolute, got: ${filePath}`);
  }
  const raw = await fs.readFile(filePath, "utf8");
  let parsed: unknown = JSON.parse(raw);
  if (typeof parsed === "string") {
    parsed = JSON.parse(parsed);
  }
  return unwrapCanonicalTree(parsed);
}

async function loadFromDS(
  slug: string,
  screenID: string,
): Promise<CanonicalNode | null> {
  const base =
    process.env.NEXT_PUBLIC_DS_SERVICE_URL ||
    process.env.DS_SERVICE_URL ||
    "http://localhost:8080";
  const url = `${base}/v1/projects/${encodeURIComponent(
    slug,
  )}/screens/${encodeURIComponent(screenID)}/canonical-tree`;
  const res = await fetch(url, { cache: "no-store" });
  if (!res.ok) {
    throw new Error(`ds-service ${res.status} ${res.statusText} @ ${url}`);
  }
  const body = (await res.json()) as { canonical_tree?: unknown };
  return unwrapCanonicalTree(body?.canonical_tree);
}

/**
 * Accept bare node, `{canonical_tree:...}` row wrapper, or Figma
 * `/files/.../nodes` envelope `{document, components, ...}`.
 */
function unwrapCanonicalTree(raw: unknown): CanonicalNode | null {
  if (!raw || typeof raw !== "object") return null;
  const obj = raw as Record<string, unknown>;
  if (
    obj.canonical_tree &&
    typeof obj.canonical_tree === "object"
  ) {
    return unwrapCanonicalTree(obj.canonical_tree);
  }
  if (obj.document && typeof obj.document === "object") {
    return obj.document as CanonicalNode;
  }
  if (typeof obj.id === "string" && obj.absoluteBoundingBox) {
    return obj as CanonicalNode;
  }
  return null;
}

function pickStr(v: string | string[] | undefined): string | null {
  if (typeof v === "string" && v.length > 0) return v;
  if (Array.isArray(v) && typeof v[0] === "string" && v[0].length > 0) return v[0];
  return null;
}
