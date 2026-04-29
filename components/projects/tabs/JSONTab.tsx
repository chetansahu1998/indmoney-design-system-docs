"use client";

/**
 * JSON tab — placeholder until U8 wires the lazy canonical_tree fetch + tree
 * viewer. The wrapper element keeps `data-anim="tab-content"` semantics so
 * U6's projectShellOpen timeline still fades it in.
 */

import EmptyTab from "./EmptyTab";

export default function JSONTab() {
  return (
    <EmptyTab
      title="JSON viewer coming in U8"
      description="Canonical tree viewer with mode + persona resolution. Lazy-fetched per screen on click — U8 hooks `lazyFetchCanonicalTree` into the atlas."
    />
  );
}
