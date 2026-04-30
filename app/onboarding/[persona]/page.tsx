/**
 * /onboarding/[persona] — Phase 3 U10 — single-persona deeplink.
 *
 * Renders just one persona section (no other personas, no quick-picker)
 * so a team lead can share `/onboarding/designer` with a new joiner and
 * they land on a focused page.
 *
 * Returns a 404 for unknown slugs via Next's notFound() — keeps the
 * URL space tight and prevents "deeplink to a typo'd persona" from
 * silently rendering a near-empty page.
 *
 * generateStaticParams pre-renders all known personas at build time.
 */

import { notFound } from "next/navigation";
import Link from "next/link";
import { PERSONAS, getPersonaBySlug } from "@/lib/onboarding/personas";
import PersonaSection from "@/components/onboarding/PersonaSection";

export function generateStaticParams() {
  return PERSONAS.map((p) => ({ persona: p.slug }));
}

interface Props {
  params: Promise<{ persona: string }>;
}

export async function generateMetadata({ params }: Props) {
  const { persona } = await params;
  const spec = getPersonaBySlug(persona);
  if (!spec) {
    return { title: "Onboarding — Projects · Flow Atlas" };
  }
  return {
    title: `${spec.name} — day-1 walkthrough`,
    description: spec.blurb,
  };
}

export default async function PersonaOnboardingPage({ params }: Props) {
  const { persona } = await params;
  const spec = getPersonaBySlug(persona);
  if (!spec) {
    notFound();
  }

  return (
    <main style={mainStyle}>
      <nav aria-label="Onboarding navigation" style={navStyle}>
        <Link href="/onboarding" style={navLinkStyle}>
          ← All personas
        </Link>
      </nav>
      <PersonaSection persona={spec} />
    </main>
  );
}

const mainStyle: React.CSSProperties = {
  maxWidth: 1024,
  margin: "0 auto",
  padding: "32px 32px 96px",
  minHeight: "100vh",
};

const navStyle: React.CSSProperties = {
  marginBottom: 24,
};

const navLinkStyle: React.CSSProperties = {
  fontSize: 11,
  fontFamily: "var(--font-mono)",
  color: "var(--text-3)",
  textDecoration: "none",
};
