"use client";

/**
 * `/inbox` — designer personal inbox of Active violations across every
 * flow they can edit. Filter chips, bulk-acknowledge, and per-row
 * lifecycle controls all live in `InboxShell`.
 *
 * Phase 4 U5 ships this as a single client page; the data fetch is
 * coalesced inside InboxShell's `useEffect` so no SSR roundtrip is
 * needed. The route is auth-gated by `app/inbox/layout.tsx`.
 */

import InboxShell from "@/components/inbox/InboxShell";

export default function InboxPage() {
  return <InboxShell />;
}
