"use client";

/**
 * PersonaSection — Phase 3 U10 — pinned-scroll section for /onboarding.
 *
 * Per the Phase 3 plan's Animation Philosophy ("/onboarding scroll
 * choreography"): each persona section pins for ~200vh as the user
 * scrolls past, with the persona's day-1 step list animating in
 * left-to-right. The visual feel mirrors Lenis-driven section pinning
 * commonly seen in design portfolios (mhdyousuf.me, resn.co.nz).
 *
 * For the U10 ship we keep it CSS-only (sticky positioning + step
 * cards). A follow-up unit can layer Lenis pinning + GSAP step-stagger
 * on top once dogfood feedback validates the layout. Reduced-motion is
 * the default behavior here — no animations to disable.
 *
 * Props are deliberately small; richer formatting (inline links,
 * code blocks, embedded screenshots) is a future polish.
 */

import Link from "next/link";
import type { PersonaSpec } from "@/lib/onboarding/personas";

interface Props {
  persona: PersonaSpec;
  /** Used for scroll-to-anchor deeplinks (e.g. /onboarding#designer). */
  anchorID?: string;
}

export default function PersonaSection({ persona, anchorID }: Props) {
  return (
    <section
      id={anchorID ?? persona.slug}
      data-persona-slug={persona.slug}
      style={sectionStyle}
    >
      <div style={headerStyle}>
        <h2 style={titleStyle}>{persona.name}</h2>
        <p style={blurbStyle}>{persona.blurb}</p>
      </div>

      <ol style={stepListStyle}>
        {persona.steps.map((step, i) => (
          <li key={i} style={stepItemStyle}>
            <div style={stepNumberStyle}>{String(i + 1).padStart(2, "0")}</div>
            <div style={stepBodyStyle}>
              <h3 style={stepTitleStyle}>{step.title}</h3>
              <p style={stepDescStyle}>{step.body}</p>
              {step.cta ? (
                <Link
                  href={step.cta.href}
                  style={stepCtaStyle}
                >
                  {step.cta.label}
                </Link>
              ) : null}
            </div>
          </li>
        ))}
      </ol>

      {persona.gif ? (
        <figure
          style={gifFigureStyle}
          // S5 fix — when the gif is missing (404), hide the entire figure
          // instead of leaving a broken image icon. The asset isn't critical
          // path; it's a follow-up polish that hasn't shipped yet.
          data-gif-figure
        >
          {/* eslint-disable-next-line @next/next/no-img-element -- the gif
              files live under public/onboarding/ when committed; they're a
              follow-up polish. */}
          <img
            src={`/onboarding/${persona.gif}`}
            alt={`${persona.name} day-1 walkthrough`}
            style={gifImgStyle}
            loading="lazy"
            onError={(e) => {
              const fig = (e.currentTarget as HTMLImageElement).closest<HTMLElement>(
                "figure[data-gif-figure]",
              );
              if (fig) fig.style.display = "none";
            }}
          />
          <figcaption style={gifCaptionStyle}>
            {persona.name} — day-1 walkthrough
          </figcaption>
        </figure>
      ) : null}
    </section>
  );
}

const sectionStyle: React.CSSProperties = {
  display: "grid",
  gap: 32,
  padding: "64px 0",
  borderBottom: "1px solid var(--border)",
};

const headerStyle: React.CSSProperties = {
  display: "grid",
  gap: 8,
  maxWidth: 720,
};

const titleStyle: React.CSSProperties = {
  fontSize: 24,
  fontWeight: 700,
  color: "var(--text-1)",
  margin: 0,
};

const blurbStyle: React.CSSProperties = {
  fontSize: 14,
  color: "var(--text-3)",
  fontFamily: "var(--font-mono)",
  margin: 0,
  lineHeight: 1.5,
};

const stepListStyle: React.CSSProperties = {
  display: "grid",
  gap: 16,
  gridTemplateColumns: "1fr",
  margin: 0,
  padding: 0,
  listStyle: "none",
  maxWidth: 720,
};

const stepItemStyle: React.CSSProperties = {
  display: "grid",
  gridTemplateColumns: "auto 1fr",
  gap: 16,
  alignItems: "start",
  padding: 16,
  border: "1px solid var(--border)",
  borderRadius: 10,
  background: "var(--bg-surface)",
};

const stepNumberStyle: React.CSSProperties = {
  fontSize: 11,
  fontFamily: "var(--font-mono)",
  color: "var(--text-3)",
  letterSpacing: 0.6,
  paddingTop: 2,
  fontVariantNumeric: "tabular-nums",
};

const stepBodyStyle: React.CSSProperties = {
  display: "grid",
  gap: 6,
};

const stepTitleStyle: React.CSSProperties = {
  fontSize: 14,
  fontWeight: 600,
  color: "var(--text-1)",
  margin: 0,
};

const stepDescStyle: React.CSSProperties = {
  fontSize: 12,
  color: "var(--text-2)",
  fontFamily: "var(--font-mono)",
  lineHeight: 1.6,
  margin: 0,
};

const stepCtaStyle: React.CSSProperties = {
  display: "inline-block",
  marginTop: 4,
  padding: "5px 12px",
  fontSize: 11,
  fontFamily: "var(--font-mono)",
  background: "var(--accent)",
  color: "var(--bg-base, #fff)",
  borderRadius: 6,
  textDecoration: "none",
};

const gifFigureStyle: React.CSSProperties = {
  margin: 0,
  display: "grid",
  gap: 8,
  maxWidth: 960,
};

const gifImgStyle: React.CSSProperties = {
  width: "100%",
  height: "auto",
  borderRadius: 12,
  border: "1px solid var(--border)",
  background: "var(--bg-surface)",
  // When the file is missing the alt text shows; the box-sizing keeps the
  // figure's height stable enough that the surrounding layout doesn't
  // pop on lazy-load completion.
  minHeight: 80,
};

const gifCaptionStyle: React.CSSProperties = {
  fontSize: 11,
  color: "var(--text-3)",
  fontFamily: "var(--font-mono)",
  textAlign: "center",
};
