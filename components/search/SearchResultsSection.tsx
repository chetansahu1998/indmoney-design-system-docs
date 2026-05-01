"use client";

/**
 * Phase 8 U10 — server-search results section for the cmdk palette.
 *
 * Renders a CommandGroup per entity-kind (Flows, Decisions, Components,
 * Personas, DRD) with snippets and deep-links. Designed to be dropped into
 * SearchModal.tsx alongside the existing static-token section.
 *
 * The parent owns the query input + open/close state. We only render the
 * results returned by the backend.
 */

import { useRouter } from "next/navigation";

import { CommandGroup, CommandItem } from "@/components/ui/command";

import { type SearchHit, type SearchStatus, useSearch } from "./useSearch";

interface Props {
  query: string;
  /** Limit search to entities present in the user's current mind-graph slice. */
  scope?: "all" | "mind-graph";
  /** Called when the user picks a result so the modal can close itself. */
  onPick?: (hit: SearchHit) => void;
}

const KIND_LABEL: Record<SearchHit["kind"], string> = {
  flow: "Flow",
  drd: "DRD",
  decision: "Decision",
  persona: "Persona",
  component: "Component",
};

const KIND_COLOR: Record<SearchHit["kind"], string> = {
  flow: "#7B9FFF",
  drd: "#9F8FFF",
  decision: "#FFB347",
  persona: "#1FD896",
  component: "#3D99FF",
};

export function SearchResultsSection({ query, scope = "all", onPick }: Props) {
  const { results, status } = useSearch(query, { scope });
  const router = useRouter();

  if (query.trim() === "") return null;
  if (status === "loading") {
    return (
      <CommandGroup heading="Searching…">
        <CommandItem disabled>
          <span style={{ fontSize: 12, color: "var(--text-3)" }}>
            Looking up flows / decisions / DRDs…
          </span>
        </CommandItem>
      </CommandGroup>
    );
  }
  if (status === "error" || results.length === 0) return null;

  // Group by kind so the user can scan visually.
  const grouped: Record<string, SearchHit[]> = {};
  for (const r of results) {
    (grouped[r.kind] ??= []).push(r);
  }

  return (
    <>
      {(Object.keys(grouped) as SearchHit["kind"][]).map((kind) => (
        <CommandGroup key={kind} heading={KIND_LABEL[kind]}>
          {grouped[kind].map((hit) => (
            <CommandItem
              key={`${hit.kind}:${hit.id}`}
              value={`${hit.kind} ${hit.title} ${hit.snippet}`}
              onSelect={() => {
                onPick?.(hit);
                if (hit.open_url) router.push(hit.open_url);
              }}
              style={{
                display: "flex",
                gap: 12,
                alignItems: "flex-start",
                padding: "10px 12px",
              }}
            >
              <div style={{ flex: 1, minWidth: 0 }}>
                <div
                  style={{
                    fontSize: 13,
                    fontWeight: 600,
                    color: "var(--text-1)",
                    marginBottom: 4,
                    overflow: "hidden",
                    textOverflow: "ellipsis",
                    whiteSpace: "nowrap",
                  }}
                >
                  {hit.title}
                </div>
                {hit.snippet && (
                  <div
                    style={{
                      fontSize: 11,
                      lineHeight: 1.5,
                      color: "var(--text-3)",
                      overflow: "hidden",
                      display: "-webkit-box",
                      WebkitLineClamp: 2,
                      WebkitBoxOrient: "vertical",
                    }}
                    // The snippet contains <mark>…</mark> for highlights;
                    // we render it directly. The string is server-side
                    // escaped where needed (FTS5's snippet() handles it).
                    dangerouslySetInnerHTML={{ __html: hit.snippet }}
                  />
                )}
              </div>
              <span
                style={{
                  flexShrink: 0,
                  fontSize: 9,
                  fontWeight: 700,
                  textTransform: "uppercase",
                  letterSpacing: "0.05em",
                  padding: "2px 7px",
                  borderRadius: 4,
                  background: KIND_COLOR[kind] + "22",
                  color: KIND_COLOR[kind],
                }}
              >
                {KIND_LABEL[kind]}
              </span>
            </CommandItem>
          ))}
        </CommandGroup>
      ))}
    </>
  );
}
