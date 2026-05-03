/**
 * /onboarding — Phase 3 U10 — long-form per-persona day-1 docs.
 *
 * Index renders all 5 personas (Designer / PM / Engineer / DS-lead /
 * Admin) as stacked sections + a top-of-page persona-picker that
 * smooth-scrolls to the picked section's anchor.
 *
 * Public route — no auth required. The Welcome empty state at /projects
 * links here for cold installers; team leads share /onboarding/<persona>
 * with new joiners.
 *
 * Server component — content is static, computed from PERSONAS at build
 * time. No client-side interactivity beyond native anchor scrolling.
 */

import Link from "next/link";
import { PERSONAS } from "@/lib/onboarding/personas";
import PersonaSection from "@/components/onboarding/PersonaSection";
import PageShell from "@/components/PageShell";
import { EXTERNAL_LINKS } from "@/lib/links";

export const metadata = {
  title: "Day-1 onboarding — Projects · Flow Atlas",
  description:
    "Per-persona walkthroughs for Designer / PM / Engineer / DS-lead / Admin.",
};

export default function OnboardingPage() {
  return (
    <PageShell>
    <main style={mainStyle}>
      <header style={heroStyle}>
        <h1 style={heroTitleStyle}>Day 1 with Projects · Flow Atlas</h1>
        <p style={heroBlurbStyle}>
          Pick the role that fits your day-1. Each section is a short,
          ordered walkthrough — bookmarkable, sharable, deep-linkable.
        </p>
        <nav aria-label="Persona quick picker" style={pickerStyle}>
          {PERSONAS.map((p) => (
            <Link
              key={p.slug}
              href={`#${p.slug}`}
              style={pickerLinkStyle}
            >
              {p.name}
            </Link>
          ))}
        </nav>
      </header>

      <div style={contentStyle}>
        {PERSONAS.map((persona) => (
          <PersonaSection key={persona.slug} persona={persona} />
        ))}
      </div>

      <footer style={footerStyle}>
        <p>
          Need something else? File a request in{" "}
          <Link
            href={EXTERNAL_LINKS.githubIssues}
            style={footerLinkStyle}
          >
            GitHub issues
          </Link>
          {" "}or message #design-system on Slack.
        </p>
      </footer>
    </main>
    </PageShell>
  );
}

const mainStyle: React.CSSProperties = {
  maxWidth: 1024,
  margin: "0 auto",
  padding: "48px 32px 96px",
  minHeight: "100vh",
};

const heroStyle: React.CSSProperties = {
  display: "grid",
  gap: 16,
  marginBottom: 64,
  paddingBottom: 48,
  borderBottom: "1px solid var(--border)",
};

const heroTitleStyle: React.CSSProperties = {
  fontSize: 40,
  fontWeight: 700,
  margin: 0,
  color: "var(--text-1)",
  lineHeight: 1.1,
};

const heroBlurbStyle: React.CSSProperties = {
  fontSize: 14,
  color: "var(--text-2)",
  fontFamily: "var(--font-mono)",
  margin: 0,
  maxWidth: 600,
  lineHeight: 1.6,
};

const pickerStyle: React.CSSProperties = {
  display: "flex",
  flexWrap: "wrap",
  gap: 8,
  marginTop: 16,
};

const pickerLinkStyle: React.CSSProperties = {
  padding: "6px 12px",
  fontSize: 11,
  fontFamily: "var(--font-mono)",
  color: "var(--text-1)",
  background: "var(--bg-surface)",
  border: "1px solid var(--border)",
  borderRadius: 999,
  textDecoration: "none",
};

const contentStyle: React.CSSProperties = {
  display: "grid",
  gap: 0,
  scrollBehavior: "smooth",
};

const footerStyle: React.CSSProperties = {
  marginTop: 64,
  padding: "32px 0",
  borderTop: "1px solid var(--border)",
  fontSize: 12,
  color: "var(--text-3)",
  fontFamily: "var(--font-mono)",
  lineHeight: 1.6,
};

const footerLinkStyle: React.CSSProperties = {
  color: "var(--accent)",
  textDecoration: "underline",
};
