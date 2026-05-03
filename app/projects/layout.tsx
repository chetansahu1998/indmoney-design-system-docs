"use client";

/**
 * Authenticated layout for the `/projects` route group.
 *
 * Responsibilities (per U6):
 *   1. Auth gate — redirect to login if no JWT in `useAuth`.
 *   2. Lenis smooth-scroll provider via `useLenisProvider()` (auto-disabled
 *      under `prefers-reduced-motion: reduce`).
 *
 * Why client-only: the JWT lives in zustand-persist + localStorage and only
 * resolves on the client. Server components can't read it without a cookie,
 * which Phase 1 doesn't ship. This layout therefore short-circuits the
 * children render until hydration confirms an auth token exists.
 *
 * Login redirect target: `/login?next=<encoded path>` round-trips the user
 * back to the originally requested route after sign-in.
 */

import { useEffect, useState } from "react";
import { useRouter, usePathname } from "next/navigation";
import { useAuth } from "@/lib/auth-client";
import { useLenisProvider } from "@/lib/animations/context";

const LOGIN_REDIRECT = "/login";

export default function ProjectsLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  const token = useAuth((s) => s.token);
  const router = useRouter();
  const pathname = usePathname();

  // Track hydration so we don't redirect before zustand-persist rehydrates
  // the token from localStorage. Without this guard a logged-in reload would
  // briefly bounce to the login screen.
  const [hydrated, setHydrated] = useState(false);
  useEffect(() => {
    setHydrated(true);
  }, []);

  // Mount Lenis singleton for smooth-scroll across the projects pages. Hook
  // is safe to call unconditionally — it returns null on the server and
  // refcounts internally (see lib/animations/context.ts).
  useLenisProvider();

  useEffect(() => {
    if (!hydrated) return;
    if (token) return;
    const next = encodeURIComponent(pathname || "/projects");
    router.replace(`${LOGIN_REDIRECT}?next=${next}`);
  }, [hydrated, token, pathname, router]);

  // Render nothing until hydration completes — avoids the SSR/CSR auth
  // mismatch flash that would otherwise show the page chrome to logged-out
  // users for one frame before redirecting.
  if (!hydrated || !token) {
    return (
      <div
        aria-hidden
        style={{
          minHeight: "100vh",
          background: "var(--bg)",
        }}
      />
    );
  }

  return <>{children}</>;
}
