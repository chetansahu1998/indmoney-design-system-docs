"use client";

/**
 * Authenticated layout for `/inbox`. Mirrors `/projects/layout.tsx`'s
 * client-only auth gate (token lives in zustand-persist + localStorage so
 * server components can't see it without a cookie). Phase 7 will move the
 * auth flag to a cookie and let this become a server layout.
 */

import { useEffect, useState } from "react";
import { useRouter, usePathname } from "next/navigation";
import { useAuth } from "@/lib/auth-client";
import { useLenisProvider } from "@/lib/animations/context";

const LOGIN_REDIRECT = "/login";

export default function InboxLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  const token = useAuth((s) => s.token);
  const router = useRouter();
  const pathname = usePathname();

  const [hydrated, setHydrated] = useState(false);
  useEffect(() => {
    setHydrated(true);
  }, []);

  useLenisProvider();

  useEffect(() => {
    if (!hydrated) return;
    if (token) return;
    const next = encodeURIComponent(pathname || "/inbox");
    router.replace(`${LOGIN_REDIRECT}?next=${next}`);
  }, [hydrated, token, pathname, router]);

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
