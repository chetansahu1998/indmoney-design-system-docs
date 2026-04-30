"use client";

/**
 * /atlas/admin — DS-lead dashboard. Phase 4 U10.
 *
 * Auth gate is shared with the rest of the authenticated routes
 * (token in zustand-persist). Endpoint requires super_admin server-side;
 * non-admins get a 403 from /v1/atlas/admin/summary which the shell
 * surfaces as an error EmptyState. We don't gate the route render here —
 * the server is the source of truth for authz.
 */

import { useEffect, useState } from "react";
import { useRouter, usePathname } from "next/navigation";
import { useAuth } from "@/lib/auth-client";
import { useLenisProvider } from "@/lib/animations/context";
import DashboardShell from "@/components/dashboard/DashboardShell";

const LOGIN_REDIRECT = "/";

export default function AtlasAdminPage() {
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
    const next = encodeURIComponent(pathname || "/atlas/admin");
    router.replace(`${LOGIN_REDIRECT}?next=${next}`);
  }, [hydrated, token, pathname, router]);

  if (!hydrated || !token) {
    return (
      <div aria-hidden style={{ minHeight: "100vh", background: "var(--bg)" }} />
    );
  }
  return <DashboardShell />;
}
