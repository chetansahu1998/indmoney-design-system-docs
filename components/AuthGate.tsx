"use client";

/**
 * Global auth gate. Mounted in app/layout.tsx so every route except the
 * passthrough list redirects to /login when no JWT is present.
 *
 * Why a global gate (instead of per-route layout.tsx) — there are 7+ user-
 * facing routes that need the same guard (/atlas, /components, /files,
 * /onboarding, /settings, /icons, /illustrations, /logos, root /). Adding
 * one layout per route would duplicate the same hydration-aware redirect
 * logic and is easy to forget on a new route. The /projects and /inbox
 * route layouts predate this gate and are kept because they also mount
 * Lenis — the gate is a no-op once their layouts have already short-
 * circuited the children render.
 *
 * Local-dev bypass: useAuth's persist middleware seeds a synthetic
 * "dev-bypass" token when NEXT_PUBLIC_AUTH_BYPASS=1, so token is non-null
 * on first render and the gate stays out of the way.
 */

import { useEffect, useState } from "react";
import { usePathname, useRouter } from "next/navigation";

import { useAuth } from "@/lib/auth-client";

const PASSTHROUGH_PREFIXES = ["/login", "/health", "/api", "/_next"];

function isPassthrough(pathname: string | null): boolean {
  if (!pathname) return true;
  return PASSTHROUGH_PREFIXES.some(
    (p) => pathname === p || pathname.startsWith(`${p}/`),
  );
}

export default function AuthGate({ children }: { children: React.ReactNode }) {
  const token = useAuth((s) => s.token);
  const pathname = usePathname();
  const router = useRouter();

  // Track hydration so we don't redirect before zustand-persist rehydrates
  // a real token from localStorage. Without this, every reload of a logged-
  // in user would briefly bounce through /login.
  const [hydrated, setHydrated] = useState(false);
  useEffect(() => {
    setHydrated(true);
  }, []);

  const passthrough = isPassthrough(pathname);

  useEffect(() => {
    if (passthrough) return;
    if (!hydrated) return;
    if (token) return;
    const next = encodeURIComponent(pathname || "/");
    router.replace(`/login?next=${next}`);
  }, [hydrated, token, pathname, passthrough, router]);

  if (passthrough) return <>{children}</>;

  // Render nothing on protected routes until hydration confirms a token —
  // avoids a one-frame flash of authenticated content for logged-out users.
  if (!hydrated || !token) {
    return (
      <div
        aria-hidden
        style={{ minHeight: "100vh", background: "var(--bg)" }}
      />
    );
  }

  return <>{children}</>;
}
